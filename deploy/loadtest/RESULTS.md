# Нагрузочный тест Bozor — результаты (Stage 10.1)

Дата: 2026-07-10. Инструмент: k6 (docker `grafana/k6`), путь клиента k6 → nginx → gateway → сервисы (сеть compose `bozor_bozor`). Хост — один Compose-инстанс (dev). Данные: 500 засеянных активных объявлений + существующие (`seed.sh seed 500`, реиндекс Typesense).

Целевые ориентиры (ARCHITECTURE §7.3):

| Поток | Цель |
|---|---|
| p95 публичного чтения (лента/поиск/карточка), прогретый кеш | < 200–300 мс |
| p95 записи (публикация без обработки медиа) | < 500 мс |

## Итог: цели достигнуты (после устранения двух узких мест)

| Сценарий | Ключевая метрика | p95 | Ошибки | RPS | Вердикт |
|---|---|---|---|---|---|
| public_read | поиск | 33 мс | 0.00% | 1761 | ✅ ≪ 300 мс |
| public_read | категории / локации / карточка | 18 / 19 / 24 мс | 0.00% | — | ✅ |
| publish (создание объявления) | create | 27 мс | 0.00% | 629 | ✅ ≪ 500 мс |
| wallet (чтение / топап) | wallet_read / topup | 5 / 20 мс | 0.00% | 943 | ✅ |

Запас к цели — примерно 10× (p95 чтения 30 мс против 300 мс). Все сценарии держат 30 VU без деградации.

## Найденные узкие места и что сделано

Под нагрузкой (не в одиночных запросах) всплыла **цепочка исчерпания эфемерных портов** на каждом сетевом стыке, где HTTP-клиент не переиспользовал соединения.

### 1. nginx → gateway: нет keepalive к апстриму (главное)
Симптом: под нагрузкой ~64% ответов 503; в error-логе nginx `connect() to gateway failed (99: Address not available)`. nginx открывал **новое TCP-соединение к gateway на каждый запрос** (`proxy_pass` по переменной, без `upstream`-блока), эфемерные порты/TIME_WAIT исчерпывались.
Фикс: `deploy/nginx/nginx.conf` — `upstream gateway_up { server gateway:8080 resolve; keepalive 64; }`, `proxy_pass http://gateway_up`, `proxy_set_header Connection ""`. Рантайм-резолвинг (`resolve`) сохранён — nginx стартует и без gateway.
Результат: 64% → 2.45% ошибок.

### 2. gateway → сервисы и search → Typesense: `http.DefaultTransport` (2 idle/host)
Симптом остатка (2.45%): 503 из сервиса **search** на `/ads/search`, в логе `dial tcp typesense:8108: connect: cannot assign requested address`. Клиент Typesense (и прочие межсервисные клиенты) использовал `&http.Client{Timeout:...}` → `http.DefaultTransport` с `MaxIdleConnsPerHost=2`. Каждый поиск делает 2 запроса к Typesense — под конкуренцией порты выжигались.
Фикс: новый `pkg/shared/httpx.PooledTransport()`/`NewClient()` (`MaxIdleConnsPerHost=128`, keepalive, dial-timeout). Применён к: транспорту прокси gateway, клиенту Typesense, клиенту Listing в search и **всем** межсервисным клиентам (reviews/chat/favorites/moderation/payments listingclient, user-profile reviewsclient, notification prefs).
Результат: 2.45% → **0.00%** ошибок; попутно пропускная способность выросла (1050 → 1727 rps на стресс-пробе).

### 3. Rate-limit gateway (20 rps/IP) — не дефект, а корректная защита
Первый прогон дал 99% `429`: token-bucket gateway ограничивает **на ключ IP** (RATE_LIMIT_RPS=20/burst=40). Вся нагрузка k6 идёт с одного IP контейнера, поэтому упирается в лимит. В проде тысячи пользователей — тысячи независимых бакетов. Для замера **ёмкости сервисов** лимит на время теста поднимался (`RATE_LIMIT_RPS`), в дефолт возвращён. Поведение верное — оставлено как есть.

## Как воспроизвести

```bash
# 1) поднять стек
make up
# 2) засеять данные поиска
bash deploy/loadtest/seed.sh seed 500
# 3) (для замера ёмкости) временно поднять лимит gateway:
RATE_LIMIT_RPS=1000000 RATE_LIMIT_BURST=2000000 \
  docker compose -f deploy/compose/docker-compose.yml -f deploy/compose/docker-compose.override.yml --env-file .env up -d gateway
# 4) прогнать сценарии (KEY = JWT_SIGNING_KEY из .env)
KEY=$(grep -E '^JWT_SIGNING_KEY=' .env | cut -d= -f2-)
docker run --rm --network bozor_bozor -v "$PWD/deploy/loadtest:/lt" \
  -e BASE_URL=http://nginx:80 -e SIGNING_KEY="$KEY" grafana/k6 run /lt/scenarios/public_read.js
# 5) очистка + возврат лимита
bash deploy/loadtest/seed.sh clean
docker compose -f deploy/compose/docker-compose.yml -f deploy/compose/docker-compose.override.yml --env-file .env up -d gateway
```

## Ограничения прогона (условия пересмотра)

- Один Compose-хост (dev), нагрузка и цель на одной машине — абсолютные rps не переносятся на прод-железо; важна форма (латентность, отсутствие ошибок, отсутствие деградации).
- Нагрузка с одного IP: обходили rate-limit поднятием лимита; распределённая нагрузка (несколько источников) — при выделенном стенде.
- Чат по WebSocket не покрыт (нагрузка WS — отдельный сценарий); покрыты REST-потоки: поиск/каталог/локации/карточка, публикация, кошелёк.
- OTP-выпуск токена (Telegram-webhook) исключён (внешняя зависимость); авторизация проверяется валидацией JWT на gateway на каждом auth-запросе (токен минтится в k6 тем же ключом).
- Прочие межсервисные клиенты получили пул keepalive в коде, но под нагрузкой отдельно не измерялись (не на горячем пути этого прогона) — превентивное упрочнение.
