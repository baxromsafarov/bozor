-- +goose Up
-- Каталог платных услуг продвижения (Stage 8.1). Код — стабильный идентификатор
-- услуги (TOP/VIP/BUMP/LOGO), используется в pricing и (позже) в ad_promotions.
-- durations_days — допустимые длительности покупки в днях (BUMP={0} — разовое
-- поднятие «здесь и сейчас», без срока действия).
CREATE TABLE promotion_services (
    code           text PRIMARY KEY,
    name_uz        text      NOT NULL,
    name_ru        text      NOT NULL,
    description_uz text      NOT NULL DEFAULT '',
    description_ru text      NOT NULL DEFAULT '',
    durations_days integer[] NOT NULL,
    sort_order     integer   NOT NULL DEFAULT 0,
    is_active      boolean   NOT NULL DEFAULT true
);

-- Наборы услуг: предустановленные комбинации со своим ценником и расписанием
-- авто-поднятий (EASY_START/FAST_SALE/TURBO_SALE).
CREATE TABLE promotion_bundles (
    code           text PRIMARY KEY,
    name_uz        text    NOT NULL,
    name_ru        text    NOT NULL,
    description_uz text    NOT NULL DEFAULT '',
    description_ru text    NOT NULL DEFAULT '',
    sort_order     integer NOT NULL DEFAULT 0,
    is_active      boolean NOT NULL DEFAULT true
);

-- Состав набора: какие услуги входят, на сколько дней и по какому расписанию
-- авто-BUMP. bump_schedule_days — дни от старта для авто-поднятий (FAST_SALE
-- {2,4,6}); для услуг со сроком (TOP/VIP) массив пустой, длительность — в
-- duration_days.
CREATE TABLE promotion_bundle_items (
    bundle_code        text      NOT NULL REFERENCES promotion_bundles (code) ON DELETE CASCADE,
    service_code       text      NOT NULL REFERENCES promotion_services (code),
    duration_days      integer   NOT NULL DEFAULT 0,
    bump_schedule_days integer[] NOT NULL DEFAULT '{}',
    sort_order         integer   NOT NULL DEFAULT 0,
    PRIMARY KEY (bundle_code, service_code)
);

-- Ценообразование (Stage 8.1): цена зависит от продукта (услуга или набор),
-- региона, категории объявления и длительности. region_id/category_id = NULL —
-- базовая цена (действует при отсутствии более специфичного правила). FK на
-- регион/категорию нет — они в других БД (db-per-service); значения ссылочно
-- совместимы. UNIQUE NULLS NOT DISTINCT: базовые строки (NULL,NULL) не дублируются.
CREATE TABLE pricing (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_type  text    NOT NULL CHECK (product_type IN ('service', 'bundle')),
    product_code  text    NOT NULL,
    region_id     integer,          -- NULL = любой регион
    category_id   uuid,             -- NULL = любая категория
    duration_days integer NOT NULL DEFAULT 0,
    amount_uzs    bigint  NOT NULL CHECK (amount_uzs >= 0),
    UNIQUE NULLS NOT DISTINCT (product_type, product_code, region_id, category_id, duration_days)
);

-- +goose Down
DROP TABLE pricing;
DROP TABLE promotion_bundle_items;
DROP TABLE promotion_bundles;
DROP TABLE promotion_services;
