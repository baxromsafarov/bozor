# Сервисы Bozor

Каждый сервис — отдельный Go-модуль: `/services/<name>/{cmd,internal,api}`.

| Сервис | Назначение | Появляется на Stage |
|---|---|---|
| gateway | API Gateway: маршрутизация, JWT, rate-limit | 0.4 |
| auth | Авторизация через Telegram-бота, JWT/refresh | 1.1–1.5 |
| user-profile | Профили, настройки, кеш рейтинга | 5.1 |
| catalog | Категории и атрибуты | 2.2–2.3 |
| location | Регионы/города Узбекистана | 2.1 |
| listing-ads | Объявления: жизненный цикл, атрибуты | 3.3–3.6 |
| media | Изображения: MinIO, превью | 3.1–3.2 |
| search | Поиск (Typesense), индексация | 4.1–4.4 |
| favorites-savedsearch | Избранное, сохранённые поиски | 5.2–5.3 |
| chat | Чат (WebSocket + backplane) | 7.1–7.4 |
| notification | Уведомления (Telegram) | 6.1 |
| moderation | Модерация, жалобы, баны | 6.2–6.4 |
| payments-promotions | Платные услуги, кошелёк, Payme/Click | 8.1–8.7 |
| reviews | Отзывы и рейтинги | 9.1–9.2 |
