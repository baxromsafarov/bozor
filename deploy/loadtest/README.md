# Нагрузочное тестирование (k6) — Bozor

Сценарии k6 на ключевые потоки (Stage 10.1). Прогоняются в контейнере
`grafana/k6` внутри сети compose, путь клиента: **k6 → nginx → gateway → сервисы**.

Результаты последнего прогона и найденные узкие места — в [RESULTS.md](RESULTS.md).

## Структура

```
loadtest/
  lib/
    common.js      — BASE_URL, пороги из NFR, категории/регионы, хелперы
    jwt.js         — минтер HS256 access-токена (тем же JWT_SIGNING_KEY)
  scenarios/
    public_read.js — поиск + каталог + локации + карточка (без auth), цель p95<300мс
    publish.js     — создание объявления (auth), цель p95<500мс
    wallet.js      — кошелёк: чтение + топап mock (auth, платежи)
  smoke.js         — 1 VU, по одному запросу на эндпоинт (быстрая проверка/CI)
  seed.sh          — сид/очистка активных объявлений + реиндекс Typesense
  RESULTS.md       — отчёт прогона (метрики, узкие места, фиксы)
```

## Предпосылки

- Поднятый стек: `make up`.
- `docker pull grafana/k6` (один раз).
- Данные для поиска: `bash deploy/loadtest/seed.sh seed 500`.
- `SIGNING_KEY` для авторизованных сценариев = `JWT_SIGNING_KEY` из `.env`.

Сеть compose называется `bozor_bozor` (проект `bozor`). Если имя проекта иное —
подставьте `--network <project>_bozor`.

## Запуск

```bash
KEY=$(grep -E '^JWT_SIGNING_KEY=' .env | cut -d= -f2-)

# дымовой прогон (проверить harness и эндпоинты)
docker run --rm --network bozor_bozor -v "$PWD/deploy/loadtest:/lt" \
  -e BASE_URL=http://nginx:80 -e SIGNING_KEY="$KEY" grafana/k6 run /lt/smoke.js

# публичное чтение (главный целевой поток)
docker run --rm --network bozor_bozor -v "$PWD/deploy/loadtest:/lt" \
  -e BASE_URL=http://nginx:80 grafana/k6 run /lt/scenarios/public_read.js

# публикация (запись) и кошелёк (платежи)
docker run --rm --network bozor_bozor -v "$PWD/deploy/loadtest:/lt" \
  -e BASE_URL=http://nginx:80 -e SIGNING_KEY="$KEY" grafana/k6 run /lt/scenarios/publish.js
docker run --rm --network bozor_bozor -v "$PWD/deploy/loadtest:/lt" \
  -e BASE_URL=http://nginx:80 -e SIGNING_KEY="$KEY" grafana/k6 run /lt/scenarios/wallet.js
```

Замечание про rate-limit: gateway ограничивает частоту **на IP** (RATE_LIMIT_RPS,
дефолт 20). Вся нагрузка k6 идёт с одного IP, поэтому для замера **ёмкости
сервисов** лимит на время теста поднимают (см. RESULTS.md), затем возвращают.

## Очистка

Все тестовые объявления помечены title-префиксом `LOADTEST_`.

```bash
bash deploy/loadtest/seed.sh clean   # удалить объявления + реиндекс
```

Кошельки/платежи синтетических пользователей нагрузки — `user_id LIKE 'a5eddddd-%'`
в БД `bozor_payments` (при необходимости очищаются вручную).
