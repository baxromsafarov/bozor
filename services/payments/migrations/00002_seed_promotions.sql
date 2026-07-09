-- +goose Up
-- Сид каталога платных услуг и наборов (ADR-007: идемпотентно, ON CONFLICT DO
-- UPDATE) — соответствует ARCHITECTURE §4.12.

-- Отдельные услуги.
INSERT INTO promotion_services (code, name_uz, name_ru, description_uz, description_ru, durations_days, sort_order) VALUES
 ('TOP',  'Top e''lon',        'Топ-объявление',
          'E''lon rukn yuqorisida, «TOP» belgisi bilan',
          'Объявление вверху рубрики со значком «ТОП»',                ARRAY[7, 30], 1),
 ('VIP',  'Bosh sahifada',     'Размещение на главной',
          'E''lon bosh sahifada ko''rsatiladi',
          'Объявление показывается на главной странице',               ARRAY[7],     2),
 ('BUMP', 'Ko''tarish',        'Поднятие в топ списка',
          'E''lonni ro''yxat boshiga ko''tarish',
          'Разовое поднятие объявления в верх списка',                 ARRAY[0],     3),
 ('LOGO', 'Ro''yxatda logotip', 'Логотип в списке',
          'E''lon yonida logotip ko''rsatiladi',
          'Логотип продавца рядом с объявлением в списке',             ARRAY[30],    4)
ON CONFLICT (code) DO UPDATE SET
    name_uz = EXCLUDED.name_uz, name_ru = EXCLUDED.name_ru,
    description_uz = EXCLUDED.description_uz, description_ru = EXCLUDED.description_ru,
    durations_days = EXCLUDED.durations_days, sort_order = EXCLUDED.sort_order, is_active = true;

-- Наборы.
INSERT INTO promotion_bundles (code, name_uz, name_ru, description_uz, description_ru, sort_order) VALUES
 ('EASY_START', 'Oson start', 'Лёгкий старт',
                'Top 3 kun',                                            'Топ на 3 дня',                            1),
 ('FAST_SALE',  'Tez sotuv',  'Быстрая продажа',
                'Top 7 kun + 3 avto-ko''tarish (2/4/6-kun)',            'Топ 7 дней + 3 авто-поднятия (2/4/6-й день)', 2),
 ('TURBO_SALE', 'Turbo sotuv', 'Турбо-продажа',
                'Top 30 kun + VIP 7 kun + 9 avto-ko''tarish',           'Топ 30 дней + VIP 7 дней + 9 авто-поднятий',  3)
ON CONFLICT (code) DO UPDATE SET
    name_uz = EXCLUDED.name_uz, name_ru = EXCLUDED.name_ru,
    description_uz = EXCLUDED.description_uz, description_ru = EXCLUDED.description_ru,
    sort_order = EXCLUDED.sort_order, is_active = true;

-- Состав наборов.
INSERT INTO promotion_bundle_items (bundle_code, service_code, duration_days, bump_schedule_days, sort_order) VALUES
 ('EASY_START', 'TOP',  3,  '{}',                       1),
 ('FAST_SALE',  'TOP',  7,  '{}',                       1),
 ('FAST_SALE',  'BUMP', 0,  '{2,4,6}',                  2),
 ('TURBO_SALE', 'TOP',  30, '{}',                       1),
 ('TURBO_SALE', 'VIP',  7,  '{}',                       2),
 ('TURBO_SALE', 'BUMP', 0,  '{3,6,9,12,15,18,21,24,27}', 3)
ON CONFLICT (bundle_code, service_code) DO UPDATE SET
    duration_days = EXCLUDED.duration_days,
    bump_schedule_days = EXCLUDED.bump_schedule_days,
    sort_order = EXCLUDED.sort_order;

-- Базовые цены (регион и категория = NULL) — действуют по всей стране.
INSERT INTO pricing (product_type, product_code, region_id, category_id, duration_days, amount_uzs) VALUES
 ('service', 'TOP',  NULL, NULL, 7,  30000),
 ('service', 'TOP',  NULL, NULL, 30, 90000),
 ('service', 'VIP',  NULL, NULL, 7,  50000),
 ('service', 'BUMP', NULL, NULL, 0,  5000),
 ('service', 'LOGO', NULL, NULL, 30, 40000),
 ('bundle',  'EASY_START', NULL, NULL, 0, 15000),
 ('bundle',  'FAST_SALE',  NULL, NULL, 0, 45000),
 ('bundle',  'TURBO_SALE', NULL, NULL, 0, 150000)
ON CONFLICT (product_type, product_code, region_id, category_id, duration_days) DO UPDATE SET
    amount_uzs = EXCLUDED.amount_uzs;

-- Демонстрация надбавок регион × категория (приоритет: чем конкретнее, тем выше).
-- region_id=1 — город Ташкент (сид Location); category a0…002 — Транспорт (сид Catalog).
INSERT INTO pricing (product_type, product_code, region_id, category_id, duration_days, amount_uzs) VALUES
 ('service', 'TOP', 1,    NULL,                                   7,  45000),   -- надбавка по Ташкенту
 ('service', 'TOP', 1,    NULL,                                   30, 130000),
 ('service', 'TOP', NULL, 'a0000000-0000-4000-8000-000000000002', 7,  40000),   -- надбавка по Транспорту
 ('service', 'TOP', 1,    'a0000000-0000-4000-8000-000000000002', 7,  60000)    -- Ташкент + Транспорт (самое конкретное)
ON CONFLICT (product_type, product_code, region_id, category_id, duration_days) DO UPDATE SET
    amount_uzs = EXCLUDED.amount_uzs;

-- +goose Down
DELETE FROM pricing;
DELETE FROM promotion_bundle_items;
DELETE FROM promotion_bundles;
DELETE FROM promotion_services;
