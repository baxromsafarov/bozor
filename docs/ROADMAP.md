# Bozor — Дорожная карта (ROADMAP)

> Проект: **Bozor** — backend маркетплейса объявлений (Go, микросервисы).
> Дата создания документа: 2026-07-06.
> Источник: мастер-промт `BOZOR_MASTER_PROMPT.md`, разделы 10 и 12-ROADMAP.
> Всего: **11 глав (CHAPTER 0 … CHAPTER 10), 52 этапа (Stage)**.

---

## Definition of Done (общий шаблон для каждого Stage)

Каждый этап (Stage) считается завершённым, только если выполнены **все** пункты:

1. **Код + тесты** (unit / интеграционные) написаны и зелёные.
2. **Линт чистый** (`golangci-lint` без ошибок).
3. **Миграции применяются** (goose, forward-only).
4. **Сервис поднимается в Docker Compose.**
5. **Эндпоинты соответствуют OpenAPI** (контракты в `api/`).
6. **Трейсы / логи / метрики отдаются** (OpenTelemetry, Prometheus, Loki).
7. **`STATUS.yaml` обновлён** (новый `status_id`, обновлённые `completed` / `current` / `next_actions` / `metrics`).
8. **Решения записаны в `DECISIONS.md`** (формат ADR: контекст → варианты → решение → последствия).

### Обязательное правило синхронизации

> **После каждого Stage — обновление `STATUS.yaml` + запись всех принятых решений в `DECISIONS.md`.**
> Переход к следующему Stage запрещён, пока DoD текущего не выполнен и не собраны доказательства (тесты / запуск). При неоднозначности — вопрос в `open_questions`, статус `blocked`, работа останавливается до решения заказчика.

---

## CHAPTER 0 — Foundations & Platform

Фундамент: монорепо, общие библиотеки, инфраструктура в Compose, gateway, наблюдаемость, миграции.

| ID | Этап | Описание работ |
|---|---|---|
| **0.1** | Монорепо, структура, инструменты | Создание структуры монорепо (`/services`, `/pkg/shared`, `/api`, `/deploy`, `/scripts`, `/docs`), `Makefile`, конфиг `golangci-lint`, CI-скелет (lint → test → build). |
| **0.2** | Общие библиотеки | `/pkg/shared`: config, logging (структурированный JSON), errors (RFC 7807), otel, http/grpc middleware (recover, logging, otel, auth, ratelimit), outbox-хелперы. |
| **0.3** | Скелет docker-compose | Поднятие инфраструктуры: PostgreSQL + PgBouncer, Redis, NATS JetStream, Typesense, MinIO, nginx; `.env.example`; override/prod-профили. |
| **0.4** | API Gateway | Gateway на Go (chi): маршрутизация, проверка JWT и проброс claims, rate-limit (Redis token-bucket), CORS, единый формат ошибок, request-id, health-эндпоинты. Без бизнес-логики. |
| **0.5** | Стек наблюдаемости | Prometheus, Grafana, Loki, Tempo; OTel во всех сервисах; базовые дашборды (RED/USE) и правила алертинга. |
| **0.6** | Фреймворк миграций и сидов | Инструментарий goose (forward-only), автозапуск миграций при старте, механизм сидов (категории, регионы, услуги, цены). |

**DoD главы:** `docker compose up` поднимает платформу; gateway отвечает на health; трейсы/логи/метрики идут; CI зелёный.

---

## CHAPTER 1 — Auth & Identity (Telegram bot phone-auth)

Авторизация по номеру телефона через Telegram-бота, JWT, сессии.

| ID | Этап | Описание работ |
|---|---|---|
| **1.1** | Настройка Telegram-бота и скелет Auth | Webhook + secret token (`X-Telegram-Bot-Api-Secret-Token`), скелет Auth-сервиса, БД `users` (+ `refresh_tokens`, `auth_audit_log`). |
| **1.2** | Flow request_contact | Reply-кнопка `request_contact`, валидация владения контактом (`contact.user_id == from.id`), нормализация телефона в E.164 (+998…), upsert пользователя по `telegram_user_id`, публикация `bozor.user.created`. |
| **1.3** | Nonce / deep-link | Одноразовый nonce (TTL 2–5 мин, single-use, Redis), deep-link `t.me/<bot>?start=<nonce>`, связывание клиентской сессии, эндпоинт опроса статуса `GET /auth/session/{nonce}` (pending / confirmed / expired). |
| **1.4** | JWT access + refresh | Access ~15 мин (`sub`, `roles`, `exp`, `iat`, `jti`); refresh ~30 дней: хранение хешем, ротация при каждом использовании, привязка к `device_id`, reuse-detection (отзыв семейства), отзыв. |
| **1.5** | Auth-middleware в gateway; logout; аудит | Интеграция проверки токенов в gateway, `POST /auth/logout`, аудит чувствительных действий, rate-limit на init/webhook. |

**DoD главы:** сквозной вход через бота выдаёт JWT; refresh/ротация/logout работают; тесты (включая отклонение чужого контакта).

---

## CHAPTER 2 — Catalog & Location

Справочники: регионы/города Узбекистана, дерево категорий, динамические атрибуты.

| ID | Этап | Описание работ |
|---|---|---|
| **2.1** | Location-сервис + сид | Таблицы `regions` / `cities` (name_uz/name_ru, гео), сид реальными данными по Узбекистану (Ташкент и районы, Самарканд, Бухара и т.д.). |
| **2.2** | Catalog: дерево категорий | Модель дерева (`categories`: parent_id, slug, name_uz/ru, level, path), CRUD под ролью admin/moderator, агрессивный кеш (Redis) с инвалидацией по событию. |
| **2.3** | Атрибуты категорий | Определения атрибутов (`attributes`: type enum/int/decimal/bool/string, is_required, is_filterable, options), связка `category_attributes` с наследованием вниз по дереву; сид 12 разделов OLX.uz + атрибуты для Недвижимости/Транспорта/Электроники. |
| **2.4** | Публичные API + кеширование | `GET /categories` (дерево), `GET /categories/{id}/attributes`, `GET /regions`, `GET /regions/{id}/cities`; кеширование ответов. |

**DoD главы:** дерево категорий и атрибуты запрашиваются; регионы/города запрашиваются; закешировано; засеяно.

---

## CHAPTER 3 — Listings & Media

Объявления (источник истины) и обработка изображений.

| ID | Этап | Описание работ |
|---|---|---|
| **3.1** | Media-сервис: загрузка | `POST /media` (multipart), валидация (тип, размер, лимит на объявление), сохранение оригинала в MinIO, таблица `media`. |
| **3.2** | Воркер обработки медиа | Генерация превью (120/480/1080 px), удаление EXIF, presigned/CDN URL, очистка «сирот» по TTL; задачи через брокер. |
| **3.3** | Listing-сервис: модель | Таблицы `ads`, `ad_attribute_values` (валидация значений против Catalog), `ad_images` (привязка изображений, обложка), `outbox`. |
| **3.4** | Жизненный цикл объявления | Переходы draft → pending → active / rejected; active → sold / expired / archived; blocked; повторная модерация после правки; renew; воркер истечения по `expires_at`. |
| **3.5** | Счётчик просмотров | Асинхронный инкремент: буфер в Redis, периодический флеш в БД (без write-hotspot). |
| **3.6** | API объявлений + outbox-события | `POST /ads`, `GET /ads/{id}`, `PATCH /ads/{id}`, `DELETE /ads/{id}`, `POST /ads/{id}/sold`, `POST /ads/{id}/renew`, `GET /ads` (лента), `GET /me/ads`; публикация `bozor.ad.*` через transactional outbox. |

**DoD главы:** создание объявления с фото и атрибутами; переходы статусов; события публикуются; API «все объявления» и «объявление по id».

---

## CHAPTER 4 — Search

Полнотекстовый и фасетный поиск на Typesense (CQRS-lite read-модель).

| ID | Этап | Описание работ |
|---|---|---|
| **4.1** | Search-сервис + схема коллекции | Коллекция Typesense `ads`: title/description (uz/ru), category_path, регион/город, цена, `attrs.*` (фасеты), created_at/bumped_at, is_top, promotion_rank, geo. |
| **4.2** | Индексатор на событиях | Потребитель `bozor.ad.created|updated|deleted|approved|bumped|sold|expired` (outbox → consumer → Typesense), идемпотентность, DLQ; инструмент полной переиндексации (reindex). |
| **4.3** | API поиска | `GET /ads/search`: полнотекст (uz/ru, typo-tolerance), фильтры (категория, регион/город, цена, атрибуты-фасеты), сортировки (релевантность, цена, дата, bumped_at), пагинация, гео-поиск; `GET /ads/search/facets`. |
| **4.4** | Заглушка топ-блока | Место для featured/TOP-объявлений в выдаче до появления Promotions (CHAPTER 8). |

**DoD главы:** API search-ads с фасетами/фильтрами/сортировкой; индекс консистентен с листингами.

---

## CHAPTER 5 — Profiles, Favorites & Saved Searches

Профили пользователей, избранное, сохранённые поиски с алертами.

| ID | Этап | Описание работ |
|---|---|---|
| **5.1** | User/Profile-сервис | Таблицы `profiles` (user_type individual/business, город, видимость телефона), `notification_prefs`, `user_ratings_cache`; API `GET/PATCH /me`, `GET /users/{id}`, `GET/PUT /me/notification-prefs`; потребление `bozor.user.created`. |
| **5.2** | Избранное | Таблица `favorites`; `POST/DELETE /favorites/{adId}`, `GET /me/favorites`. |
| **5.3** | Сохранённые поиски + matcher | Таблица `saved_searches`; на `bozor.ad.approved` matcher оценивает фильтры сохранённых поисков против нового объявления (батчинг, дедуп, защита от лавины) и публикует событие для Notification; стратегия фиксируется в ARCHITECTURE.md. |

**DoD главы:** API профиля; избранное; алерты сохранённых поисков срабатывают на совпадениях.

---

## CHAPTER 6 — Moderation & Notifications

Модерация объявлений, жалобы, баны; событийные уведомления через Telegram.

| ID | Этап | Описание работ |
|---|---|---|
| **6.1** | Notification-сервис | Канал Telegram-бота (абстракция каналов на будущее), локализованные шаблоны (uz/ru) по типам событий, учёт `notification_prefs`, батчинг/rate limiting, статусы доставки, ретраи с backoff, DLQ. |
| **6.2** | Moderation: очередь и авто-проверки | `moderation_tasks` на каждое новое/отредактированное объявление; авто-проверки: стоп-слова uz/ru («копия», «реплика», «подделка», «под оригинал» + словарь), запрещённые категории, детекция дублей; pass → active, fail → ручная очередь. |
| **6.3** | Ручные действия модератора | approve / reject (с причиной) / request-edit (с правом правки → повторная модерация); API под ролью moderator/admin. |
| **6.4** | Жалобы и баны | `POST /reports` (публичный), очередь жалоб, действия (предупреждение/снятие/бан), баны временные/постоянные/shadow; публикация `bozor.ad.approved|rejected`, `bozor.user.banned` → уведомления. |

**DoD главы:** новое объявление проходит модерацию до активации; отклонения уведомляют; жалобы и баны работают.

---

## CHAPTER 7 — Chat

Чат покупатель↔продавец: WebSocket, история, масштабирование на репликах.

| ID | Этап | Описание работ |
|---|---|---|
| **7.1** | Модель и REST-история | `conversations` (уникальность по ad_id + buyer_id + seller_id), `messages`; `GET /conversations`, `GET /conversations/{id}/messages`. |
| **7.2** | WebSocket + backplane | WS-эндпоинт `/ws`, аутентификация по JWT; Redis Pub/Sub или NATS как backplane для доставки между репликами; sticky/консистентная балансировка. |
| **7.3** | Доставка и статусы | Онлайн — через WS; оффлайн — событие → Telegram-уведомление; непрочитанные счётчики, отметки о прочтении. |
| **7.4** | Модерация и лимиты в чате | Блокировка пользователя, жалоба на сообщение → Moderation, rate limiting на отправку. |

**DoD главы:** двое пользователей переписываются по объявлению; оффлайн получает Telegram-уведомление; масштабируется на репликах.

---

## CHAPTER 8 — Payments & Promotions

Монетизация: платные услуги, кошелёк, Payme/Click, ротация Топа, возвраты.

| ID | Этап | Описание работ |
|---|---|---|
| **8.1** | Каталог услуг и ценообразование | Сид услуг: TOP (7/30 дней), VIP (7 дней), BUMP, LOGO (30 дней), наборы EASY_START / FAST_SALE / TURBO_SALE; таблица `pricing` (регион × категория × длительность, суммы в UZS). |
| **8.2** | Кошелёк и транзакции | `wallets`, `wallet_transactions` (леджер, стиль двойной записи), пополнение, списание при покупке; `GET /me/wallet`, `POST /wallet/topup`. |
| **8.3** | Провайдеры оплаты | Интерфейс `PaymentProvider { CreateInvoice; HandleCallback }`; адаптеры Payme (JSON-RPC merchant API), Click (Prepare/Complete), Mock для dev; идемпотентные колбэки (провайдеры ретраят). |
| **8.4** | Применение услуг к объявлениям | `ad_promotions` (starts_at/ends_at/status/schedule_json), покупка услуги/набора (`POST /ads/{id}/promote`), saga списание→активация→компенсация; асинхронная обработка. |
| **8.5** | Воркер авто-поднятий | Плановые bump по расписаниям наборов: обновление `bumped_at`, публикация `bozor.ad.bumped` → переиндексация в поиске. |
| **8.6** | Ротация Топа | Макс. 5 активных TOP-объявлений на категорию, случайная выборка при каждом запросе; формирование топ-блока в поиске/ленте. |
| **8.7** | Возвраты и взаимодействие сроков | Пропорциональный возврат неиспользованных дней в кошелёк при удалении объявления модерацией (транзакция `refund`); приостановка услуги при истечении объявления до реактивации. |

**DoD главы:** покупка набора через (mock) оплату; объявление получает TOP/VIP; авто-поднятия запланированы; возвраты при удалении; ротация работает.

---

## CHAPTER 9 — Reviews & Ratings

Отзывы о продавцах и агрегированные рейтинги.

| ID | Этап | Описание работ |
|---|---|---|
| **9.1** | Модель отзыва и создание | `reviews` (rating 1..5, текст); ограничения: один отзыв на взаимодействие, анти-абьюз (нельзя себе, нельзя спамить); модерация текста; `POST /reviews`, `GET /users/{id}/reviews`. |
| **9.2** | Агрегированный рейтинг | Пересчёт avg_rating/count при создании отзыва, событие `bozor.review.created` → обновление `user_ratings_cache` в Profile + уведомление продавцу. |

**DoD главы:** отзыв оставляется; рейтинг профиля обновляется; абьюз ограничен.

---

## CHAPTER 10 — Hardening: Scale, Reliability, Security, Perf

Доводка под целевую нагрузку: десятки тысяч DAU, тысячи одновременных пользователей.

| ID | Этап | Описание работ |
|---|---|---|
| **10.1** | Нагрузочное тестирование | Сценарии k6 на ключевые потоки (авторизация, публикация, поиск, чат, платежи); выявление узких мест; сверка с целевыми метриками (p95 чтения < 200–300 мс и т.д.). |
| **10.2** | Проход по кешированию | Cache-aside + инвалидация на горячих путях (каталог, локации, карточки, поиск). |
| **10.3** | Тюнинг PostgreSQL | Индексы, PgBouncer (transaction pooling), готовность к read-репликам, партиционирование `ads` по `created_at` (помесячно), путь к шардированию/Citus. |
| **10.4** | Устойчивость | Ретраи с backoff+jitter, circuit breakers, таймауты, bulkhead, DLQ, graceful shutdown, аудит идемпотентности всех write-путей и консьюмеров. |
| **10.5** | Ревью безопасности | Проверки авторизации (IDOR), rate-limit, анти-спам квоты, работа с секретами (только env), скан зависимостей (govulncheck), OWASP API Security. |
| **10.6** | Масштабирование Compose | nginx LB + реплики stateless-сервисов (`--scale`); задокументированный путь миграции Compose → Swarm → Kubernetes; вынос тяжёлых компонентов. |
| **10.7** | Бэкапы и ранбуки | PostgreSQL PITR (WAL-архивация), резервирование/репликация MinIO, ранбуки восстановления. |

**DoD главы:** система удовлетворяет целевым метрикам под нагрузочным тестом; устойчивость проверена; чек-лист безопасности пройден; путь масштабирования задокументирован.

---

## Сводная таблица

| Глава | Название | Число этапов | Статус |
|---|---|---:|---|
| CHAPTER 0 | Foundations & Platform | 6 | not_started |
| CHAPTER 1 | Auth & Identity (Telegram bot phone-auth) | 5 | not_started |
| CHAPTER 2 | Catalog & Location | 4 | not_started |
| CHAPTER 3 | Listings & Media | 6 | not_started |
| CHAPTER 4 | Search | 4 | not_started |
| CHAPTER 5 | Profiles, Favorites & Saved Searches | 3 | not_started |
| CHAPTER 6 | Moderation & Notifications | 4 | not_started |
| CHAPTER 7 | Chat | 4 | not_started |
| CHAPTER 8 | Payments & Promotions | 7 | not_started |
| CHAPTER 9 | Reviews & Ratings | 2 | not_started |
| CHAPTER 10 | Hardening: Scale, Reliability, Security, Perf | 7 | not_started |
| **Итого** | | **52** | текущий этап — см. `STATUS.yaml` |

> Актуальный прогресс по этапам ведётся **только** в `STATUS.yaml` (единственный источник истины). Статусы в таблице выше фиксируются на дату создания документа и не заменяют `STATUS.yaml`.
