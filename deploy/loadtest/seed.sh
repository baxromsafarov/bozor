#!/usr/bin/env bash
# Сид/очистка данных для нагрузочного теста Bozor.
#
#   seed.sh seed [N]   — вставить N активных объявлений (по умолчанию 500) в
#                        bozor_listing и переиндексировать Typesense.
#   seed.sh clean      — удалить все LOADTEST-объявления (сид + черновики publish)
#                        и переиндексировать.
#
# Все объявления помечены title-префиксом LOADTEST_ — ключ для очистки.
# Запускать из корня репозитория (E:\bozor). Требует поднятый стек (make up).
set -euo pipefail

cd "$(dirname "$0")/../.."
PGUSER="$(grep -E '^POSTGRES_USER=' .env | cut -d= -f2)"
PG="docker exec -i bozor-postgres-1 psql -U ${PGUSER} -d bozor_listing -v ON_ERROR_STOP=1"

reindex() {
  echo "-> переиндексация Typesense из активных объявлений Listing..."
  # MSYS_NO_PATHCONV: не давать Git Bash (Windows) превращать /search в путь.
  MSYS_NO_PATHCONV=1 docker exec bozor-search-1 /search -reindex
}

case "${1:-}" in
  seed)
    N="${2:-500}"
    echo "-> вставка ${N} активных LOADTEST-объявлений в bozor_listing..."
    $PG <<SQL
INSERT INTO ads (id, user_id, category_id, title, description, price, currency,
                 region_id, status, phone_display, published_at, expires_at, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'a5eddddd-0000-4000-8000-000000000001'::uuid,
  (ARRAY[
    'a0000000-0000-4000-8000-000000000201',
    'a0000000-0000-4000-8000-000000000003',
    'a0000000-0000-4000-8000-000000000005',
    'a0000000-0000-4000-8000-000000000006'
  ])[1 + (g % 4)]::uuid,
  'LOADTEST_seed_' || g,
  'Нагрузочные данные, к очистке. Товар номер ' || g,
  100000 + (g * 137) % 5000000,
  'UZS',
  (ARRAY[1,2,3,4,5,7,10,14])[1 + (g % 8)]::smallint,
  'active',
  true,
  now() - (g % 720) * interval '1 hour',
  now() + interval '30 days',
  now() - (g % 720) * interval '1 hour',
  now()
FROM generate_series(1, ${N}) AS g;
SQL
    echo "-> вставлено. Активных объявлений теперь:"
    $PG -t -c "SELECT count(*) FROM ads WHERE status='active';"
    reindex
    ;;
  clean)
    echo "-> удаление LOADTEST-объявлений (сид + черновики publish)..."
    $PG -c "DELETE FROM ads WHERE title LIKE 'LOADTEST%';"
    echo "-> активных объявлений осталось:"
    $PG -t -c "SELECT count(*) FROM ads WHERE status='active';"
    reindex
    ;;
  *)
    echo "usage: seed.sh {seed [N]|clean}" >&2
    exit 1
    ;;
esac
echo "-> готово."
