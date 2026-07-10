// Публичное чтение — горячий путь без авторизации: поиск с фильтрами, каталог,
// локации, карточка объявления. Цель (ARCHITECTURE §7.3): p95 < 300 мс.
//
// Запуск (из сети compose):
//   docker run --rm --network bozor_bozor -v "$PWD/deploy/loadtest:/lt" \
//     -e BASE_URL=http://nginx:80 grafana/k6 run /lt/scenarios/public_read.js
import http from 'k6/http';
import { check, group } from 'k6';
import { Trend } from 'k6/metrics';
import { BASE_URL, THRESHOLDS, CATEGORIES, REGIONS, QUERIES, pick } from '../lib/common.js';

// Отдельные тренды на подпотоки — чтобы видеть, какой из них узкое место.
const tSearch = new Trend('read_search', true);
const tCategories = new Trend('read_categories', true);
const tLocations = new Trend('read_locations', true);
const tCard = new Trend('read_card', true);

export const options = {
  scenarios: {
    ramping: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '20s', target: 30 }, // разгон
        { duration: '40s', target: 30 }, // плато
        { duration: '10s', target: 0 }, // спад
      ],
      gracefulRampDown: '5s',
    },
  },
  thresholds: {
    ...THRESHOLDS.read,
    read_search: ['p(95)<300'],
    read_card: ['p(95)<300'],
  },
};

export default function () {
  group('search', function () {
    const cat = pick(CATEGORIES);
    const region = pick(REGIONS);
    const q = pick(QUERIES);
    const url =
      `${BASE_URL}/api/v1/ads/search?limit=20&category_id=${cat}&region_id=${region}` +
      (q ? `&q=${encodeURIComponent(q)}` : '');
    const res = http.get(url, { tags: { name: 'search' } });
    tSearch.add(res.timings.duration);
    check(res, { 'search 200': (r) => r.status === 200 });
  });

  group('categories', function () {
    const res = http.get(`${BASE_URL}/api/v1/categories`, { tags: { name: 'categories' } });
    tCategories.add(res.timings.duration);
    check(res, { 'categories 200': (r) => r.status === 200 });
  });

  group('locations', function () {
    const res = http.get(`${BASE_URL}/api/v1/locations/regions`, { tags: { name: 'locations' } });
    tLocations.add(res.timings.duration);
    check(res, { 'locations 200': (r) => r.status === 200 });
  });

  group('card', function () {
    // Берём id из свежего поиска и открываем карточку — реалистичная последовательность.
    const list = http.get(`${BASE_URL}/api/v1/ads/search?limit=20`, { tags: { name: 'search_for_card' } });
    let id = null;
    try {
      const hits = list.json('hits');
      if (hits && hits.length > 0) {
        id = hits[Math.floor(Math.random() * hits.length)].id;
      }
    } catch (e) {
      id = null;
    }
    if (id) {
      const res = http.get(`${BASE_URL}/api/v1/ads/${id}`, { tags: { name: 'card' } });
      tCard.add(res.timings.duration);
      check(res, { 'card 200': (r) => r.status === 200 });
    }
  });
}
