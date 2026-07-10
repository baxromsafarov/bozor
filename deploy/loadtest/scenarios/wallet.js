// Платежи — авторизованный путь: чтение кошелька (read) и пополнение через
// mock-провайдер (write, двухфазный invoice). Проверяет auth-путь платежей и
// леджер под нагрузкой. Требует -e SIGNING_KEY=<JWT_SIGNING_KEY>.
import http from 'k6/http';
import { check, group } from 'k6';
import { Trend } from 'k6/metrics';
import { BASE_URL, SIGNING_KEY, authHeaders } from '../lib/common.js';
import { mintJWT, syntheticSeller } from '../lib/jwt.js';

const tWalletRead = new Trend('wallet_read', true);
const tTopup = new Trend('wallet_topup', true);

export const options = {
  scenarios: {
    ramping: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '15s', target: 10 },
        { duration: '30s', target: 10 },
        { duration: '10s', target: 0 },
      ],
      gracefulRampDown: '5s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    wallet_read: ['p(95)<300'],
    wallet_topup: ['p(95)<500'],
  },
};

export function setup() {
  if (!SIGNING_KEY) {
    throw new Error('SIGNING_KEY не задан: передайте -e SIGNING_KEY=$JWT_SIGNING_KEY');
  }
}

export default function () {
  const user = syntheticSeller(10000 + __VU); // отдельный диапазон от publish
  const token = mintJWT(SIGNING_KEY, user, ['buyer'], 900);

  group('wallet_read', function () {
    const res = http.get(`${BASE_URL}/api/v1/me/wallet`, {
      headers: authHeaders(token),
      tags: { name: 'wallet_read' },
    });
    tWalletRead.add(res.timings.duration);
    check(res, { 'wallet 200': (r) => r.status === 200 });
  });

  group('wallet_topup', function () {
    const res = http.post(
      `${BASE_URL}/api/v1/wallet/topup`,
      JSON.stringify({ amount_uzs: 50000, provider: 'mock' }),
      { headers: authHeaders(token), tags: { name: 'wallet_topup' } }
    );
    tTopup.add(res.timings.duration);
    check(res, { 'topup 201': (r) => r.status === 201 });
  });
}
