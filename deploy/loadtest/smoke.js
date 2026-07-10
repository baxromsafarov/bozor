// Дымовой прогон: 1 VU, по одному запросу на ключевой эндпоинт с проверками.
// Быстрая верификация, что стек и harness исправны перед нагрузкой (пригодно в CI).
// Требует -e SIGNING_KEY=<JWT_SIGNING_KEY> для авторизованных эндпоинтов.
import http from 'k6/http';
import { check, group } from 'k6';
import { BASE_URL, SIGNING_KEY, WRITE_CATEGORIES, REGIONS, authHeaders, pick } from './lib/common.js';
import { mintJWT, syntheticSeller } from './lib/jwt.js';

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: {
    checks: ['rate==1.0'], // все проверки должны пройти
    http_req_failed: ['rate==0'],
  },
};

export default function () {
  group('public reads', function () {
    check(http.get(`${BASE_URL}/api/v1/ads/search?limit=5`), { 'search 200': (r) => r.status === 200 });
    check(http.get(`${BASE_URL}/api/v1/categories`), { 'categories 200': (r) => r.status === 200 });
    check(http.get(`${BASE_URL}/api/v1/locations/regions`), { 'locations 200': (r) => r.status === 200 });
  });

  if (!SIGNING_KEY) {
    return; // без ключа авторизованные проверки пропускаются
  }
  const token = mintJWT(SIGNING_KEY, syntheticSeller(1), ['buyer', 'seller'], 900);

  group('auth reads', function () {
    check(http.get(`${BASE_URL}/api/v1/me/wallet`, { headers: authHeaders(token) }), {
      'wallet 200': (r) => r.status === 200,
    });
  });

  group('write publish', function () {
    const res = http.post(
      `${BASE_URL}/api/v1/ads`,
      JSON.stringify({
        category_id: pick(WRITE_CATEGORIES),
        title: 'LOADTEST_smoke',
        description: 'smoke',
        price: 123000,
        currency: 'UZS',
        region_id: pick(REGIONS),
        phone_display: true,
        attributes: [],
        images: [],
      }),
      { headers: authHeaders(token) }
    );
    check(res, { 'create 201': (r) => r.status === 201 });
  });
}
