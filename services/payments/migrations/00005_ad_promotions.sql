-- +goose Up
-- Применение платных услуг к объявлению (Stage 8.4). Одна строка — одна активная
-- услуга (TOP/VIP/BUMP/LOGO), возможно в составе набора (bundle_code). schedule_json
-- хранит расписание авто-BUMP (дни от старта) для воркера авто-поднятий (8.5).
-- amount_uzs — доля стоимости (для пропорционального возврата 8.7). ends_at NULL
-- у разового BUMP. FK на объявление нет — оно в БД Listing (db-per-service).
CREATE TABLE ad_promotions (
    id            uuid PRIMARY KEY,
    ad_id         uuid        NOT NULL,
    user_id       uuid        NOT NULL,
    service_code  text        NOT NULL,
    bundle_code   text,
    status        text        NOT NULL DEFAULT 'active'
                              CHECK (status IN ('active', 'expired', 'refunded', 'suspended')),
    amount_uzs    bigint      NOT NULL DEFAULT 0 CHECK (amount_uzs >= 0),
    starts_at     timestamptz NOT NULL DEFAULT now(),
    ends_at       timestamptz,
    schedule_json jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Активные услуги объявления.
CREATE INDEX idx_ad_promotions_ad ON ad_promotions (ad_id, status);
-- Выборка активных услуг по типу и сроку (ротация Топа 8.6, авто-поднятия 8.5).
CREATE INDEX idx_ad_promotions_active ON ad_promotions (service_code, ends_at) WHERE status = 'active';

-- +goose Down
DROP TABLE ad_promotions;
