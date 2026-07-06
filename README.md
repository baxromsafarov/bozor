# Bozor

**Bozor** — backend торговой площадки объявлений (C2C/B2C classifieds) для рынка Узбекистана, функциональный аналог olx.uz. Проект разрабатывается как **backend-only**: система микросервисов на **Go** с PostgreSQL (database-per-service), событийной интеграцией через NATS JetStream и развёртыванием в Docker Compose. Фронтенда нет — потребители API появятся позже.

> **Статус:** в активной разработке. Единственный источник истины о прогрессе — [`docs/STATUS.yaml`](docs/STATUS.yaml).

---

## Быстрый старт (локально)

> ⚠️ Файл `docker-compose.yml` появится на **Stage 0.3** (скелет инфраструктуры). До этого момента команда ниже — целевой сценарий.

```bash
# 1. Скопировать пример конфигурации
cp .env.example .env

# 2. Поднять всю платформу (инфраструктура + сервисы)
docker compose up -d

# 3. Проверить здоровье gateway
curl http://localhost/healthz
```

Основные make-цели (появляются по мере реализации Stage 0.1):

| Цель | Назначение |
|---|---|
| `make up` / `make down` | поднять/остановить окружение Docker Compose |
| `make lint` | запуск `golangci-lint` по всем сервисам |
| `make test` | unit- и интеграционные тесты |
| `make build` | сборка бинарей/образов всех сервисов |
| `make migrate` | применение миграций (goose) |
| `make seed` | сиды: категории, регионы, каталог услуг, ценообразование |
| `make gen` | генерация кода из proto (buf) |

---

## Структура репозитория (монорепо)

```
/services/<name>/{cmd,internal,api}   # каждый микросервис
/pkg/shared/                          # общие библиотеки: config, logging, errors, otel,
                                      # auth-middleware, http/grpc helpers, outbox
/api/{openapi,proto}/                 # контракты (OpenAPI 3.1, protobuf/buf)
/deploy/{compose,nginx,migrations}/   # инфраструктура развёртывания
/scripts/                             # утилиты (seed, reindex, load-tests)
/docs/                                # SPMS-документы (источник истины по проекту)
```

---

## Сервисы

| Сервис | Описание |
|---|---|
| `gateway` | API Gateway: маршрутизация, проверка JWT, rate limiting, единый формат ошибок |
| `auth` | Авторизация через Telegram-бота (номер телефона), JWT access + refresh с ротацией |
| `user-profile` | Расширенные профили пользователей, настройки, тип аккаунта, настройки уведомлений |
| `catalog` | Дерево категорий и динамические атрибуты/фильтры категорий (uz/ru) |
| `location` | Справочник регионов и городов/районов Узбекистана |
| `listing-ads` | Объявления: CRUD, атрибуты, фото, полный жизненный цикл, счётчик просмотров |
| `media` | Загрузка и обработка изображений (превью, EXIF), хранение в MinIO, отдача через CDN |
| `search` | Полнотекстовый (uz/ru) и фасетный поиск объявлений на Typesense, гео |
| `favorites-savedsearch` | Избранное и сохранённые поиски с алертами о новых совпадениях |
| `chat` | Чат покупатель↔продавец: WebSocket + история, backplane для мультиреплик |
| `notification` | Событийные уведомления через Telegram-бота, локализованные шаблоны (uz/ru) |
| `moderation` | Модерация объявлений: авто-проверки (стоп-слова), ручные действия, жалобы, баны |
| `payments-promotions` | Платные услуги (VIP/Топ/поднятие/лого/наборы), кошелёк, Payme/Click, возвраты |
| `reviews` | Отзывы о продавцах и агрегированные рейтинги |

*(13 прикладных сервисов + gateway.)*

---

## Технологический стек

- **Язык:** Go 1.26+ (chi для HTTP, gRPC + buf для межсервисного взаимодействия)
- **БД:** PostgreSQL (database-per-service) + pgx, чистые репозитории (ADR-005), миграции goose, PgBouncer
- **События:** NATS JetStream (transactional outbox, идемпотентные консьюмеры, DLQ)
- **Кеш:** Redis (cache-aside, rate limiting, идемпотентность, WS-backplane)
- **Поиск:** Typesense (полнотекст uz/ru, фасеты, гео)
- **Хранилище:** MinIO (S3-совместимое) + CDN для изображений
- **Наблюдаемость:** OpenTelemetry, Prometheus, Grafana, Loki, Tempo
- **Платежи:** Payme, Click + Mock-провайдер для dev
- **Развёртывание:** Docker Compose + nginx (edge, TLS, LB); задокументирован путь на Swarm/K8s

---

## Документация (SPMS)

Проект ведётся по Structured Project Memory System. Ключевые документы в [`docs/`](docs/):

| Документ | Назначение |
|---|---|
| [`docs/DESCRIPTION.md`](docs/DESCRIPTION.md) | Видение, цели, аудитория, границы v1 и non-goals |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Дорожная карта: главы → этапы с Definition of Done |
| [`docs/RULES.md`](docs/RULES.md) | Инженерные конвенции (Go, БД, события, тесты, git) |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Границы сервисов, модели данных, каталог событий, путь масштабирования |
| [`docs/STATUS.yaml`](docs/STATUS.yaml) | **Единственный источник истины** о текущем прогрессе |
| [`docs/DECISIONS.md`](docs/DECISIONS.md) | Журнал архитектурных решений (ADR) |
| [`docs/SYNC-PROTOCOL.md`](docs/SYNC-PROTOCOL.md) | Протокол синхронизации контекста между сессиями |
| [`docs/GLOSSARY.md`](docs/GLOSSARY.md) | Глоссарий доменных терминов |

---

## Для разработчиков

**Требования:**

- Go **1.26+**
- Docker (с плагином Docker Compose)
- `make`, `golangci-lint`, `buf`, `goose` — для локальной разработки

**Правила:**

- Коммиты — по стандарту [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:` …); фичеветки, PR с зелёным CI (`lint → test → build`).
- Конфигурация и секреты — только через переменные окружения (`.env`, шаблон в `.env.example`); секреты в git не коммитятся.
- Доступ к чужой БД сервиса запрещён — только API и события; доменные события публикуются через transactional outbox.
- В конце каждого Stage обновляется `docs/STATUS.yaml`, архитектурные решения фиксируются в `docs/DECISIONS.md`.
