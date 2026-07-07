#!/bin/sh
# Создание отдельной БД на каждый сервис (database-per-service).
# Выполняется docker-entrypoint'ом PostgreSQL при первом старте тома.
set -eu

DATABASES="bozor_auth bozor_profile bozor_catalog bozor_location bozor_listing bozor_media bozor_favorites bozor_chat bozor_notification bozor_moderation bozor_payments bozor_reviews"

for db in $DATABASES; do
  echo "creating database: $db"
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
    SELECT 'CREATE DATABASE $db OWNER $POSTGRES_USER'
    WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$db')\\gexec
EOSQL
done

echo "all service databases ready"
