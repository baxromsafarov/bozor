// Общие настройки нагрузочных сценариев Bozor.
//
// BASE_URL — точка входа. По умолчанию edge nginx внутри сети compose
// (http://nginx:80), т.е. полный путь клиента через балансировщик и gateway.
// Переопределяется переменной окружения k6: -e BASE_URL=...
//
// SIGNING_KEY — тот же JWT_SIGNING_KEY, что у сервисов (передаётся -e SIGNING_KEY=...).
export const BASE_URL = __ENV.BASE_URL || 'http://nginx:80';
export const SIGNING_KEY = __ENV.SIGNING_KEY || '';

// Целевые пороги производительности из ARCHITECTURE §7.3 (NFR):
//   p95 публичного чтения (лента/поиск/карточка), прогретый кеш < 200–300 мс
//   p95 записи (публикация без обработки медиа) < 500 мс
// Берём верхнюю границу как порог провала теста.
export const THRESHOLDS = {
  read: {
    http_req_failed: ['rate<0.01'], // < 1% ошибок
    http_req_duration: ['p(95)<300', 'p(99)<600'],
  },
  write: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
  },
};

// Реальные данные каталога/регионов (сид bozor), по которым фильтрует поиск.
// CATEGORIES — для чтения/поиска (в них есть засеянные объявления, вкл. 201).
export const CATEGORIES = [
  'a0000000-0000-4000-8000-000000000201',
  'a0000000-0000-4000-8000-000000000003',
  'a0000000-0000-4000-8000-000000000005',
  'a0000000-0000-4000-8000-000000000006',
];
// WRITE_CATEGORIES — листовые категории без обязательных атрибутов (201 наследует
// обязательные brand/year от родителя «транспорт», поэтому для публикации не годится).
export const WRITE_CATEGORIES = [
  'a0000000-0000-4000-8000-000000000003',
  'a0000000-0000-4000-8000-000000000005',
  'a0000000-0000-4000-8000-000000000006',
  'a0000000-0000-4000-8000-000000000007',
];
export const REGIONS = [1, 2, 3, 4, 5, 7, 10, 14];
export const QUERIES = ['', 'телефон', 'машина', 'дом', 'ноутбук', 'iphone', 'диван'];

export function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

// jsonHeaders / authHeaders — заголовки для запросов.
export function jsonHeaders() {
  return { 'Content-Type': 'application/json' };
}

export function authHeaders(token) {
  return { 'Content-Type': 'application/json', Authorization: 'Bearer ' + token };
}
