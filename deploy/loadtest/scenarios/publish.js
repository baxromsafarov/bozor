// Публикация — авторизованный путь записи: создание объявления (черновик) через
// gateway (валидация JWT + проброс идентичности). Цель (ARCHITECTURE §7.3):
// p95 записи < 500 мс. Медиа не обрабатывается (создаётся черновик без изображений).
//
// Черновики помечаются title-префиксом LOADTEST_ для последующей очистки
// (deploy/loadtest/seed.sh clean). Требует -e SIGNING_KEY=<JWT_SIGNING_KEY>.
import http from 'k6/http';
import { check } from 'k6';
import { Trend } from 'k6/metrics';
import { BASE_URL, SIGNING_KEY, THRESHOLDS, WRITE_CATEGORIES, REGIONS, authHeaders, pick } from '../lib/common.js';
import { mintJWT, syntheticSeller } from '../lib/jwt.js';

const tCreate = new Trend('write_create_ad', true);

export const options = {
  scenarios: {
    ramping: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '15s', target: 15 },
        { duration: '30s', target: 15 },
        { duration: '10s', target: 0 },
      ],
      gracefulRampDown: '5s',
    },
  },
  thresholds: {
    ...THRESHOLDS.write,
    write_create_ad: ['p(95)<500'],
  },
};

export function setup() {
  if (!SIGNING_KEY) {
    throw new Error('SIGNING_KEY не задан: передайте -e SIGNING_KEY=$JWT_SIGNING_KEY');
  }
}

export default function () {
  const seller = syntheticSeller(__VU);
  const token = mintJWT(SIGNING_KEY, seller, ['buyer', 'seller'], 900);

  const payload = JSON.stringify({
    category_id: pick(WRITE_CATEGORIES),
    title: 'LOADTEST_' + __VU + '_' + __ITER,
    description: 'Нагрузочный черновик, к очистке',
    price: 100000 + Math.floor(Math.random() * 900000),
    currency: 'UZS',
    region_id: pick(REGIONS),
    phone_display: true,
    attributes: [],
    images: [],
  });

  const res = http.post(`${BASE_URL}/api/v1/ads`, payload, {
    headers: authHeaders(token),
    tags: { name: 'create_ad' },
  });
  tCreate.add(res.timings.duration);
  check(res, { 'create 201': (r) => r.status === 201 });
}
