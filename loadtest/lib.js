// Shared user journey: create an order through the gateway, then read it back.
import http from 'k6/http';
import { check } from 'k6';

export const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

const JSON_HEADERS = { 'Content-Type': 'application/json' };

// journey runs one create + read. extraHeaders lets the chaos scenario attach
// W3C baggage that the services use to inject faults.
export function journey(extraHeaders = {}) {
  const headers = Object.assign({}, JSON_HEADERS, extraHeaders);
  const sku = `sku-${1 + Math.floor(Math.random() * 50)}`;

  const created = http.post(`${BASE_URL}/api/orders`, JSON.stringify({ sku, qty: 1 }), {
    headers,
    tags: { name: 'POST /api/orders' },
  });
  const ok = check(created, { 'create: 201': (r) => r.status === 201 });
  if (!ok) {
    return;
  }
  const id = created.json('id');
  const read = http.get(`${BASE_URL}/api/orders/${id}`, {
    headers,
    tags: { name: 'GET /api/orders/{id}' },
  });
  check(read, { 'read: 200': (r) => r.status === 200 });
}

// summary writes the full k6 summary to /results/<name>.json and prints the
// headline numbers to stdout.
export function summary(name, data) {
  const m = data.metrics;
  const dur = m.http_req_duration ? m.http_req_duration.values : {};
  const failed = m.http_req_failed ? m.http_req_failed.values.rate : 0;
  const reqs = m.http_reqs ? m.http_reqs.values : {};
  const line = `${name}: requests=${reqs.count || 0} rate=${(reqs.rate || 0).toFixed(1)}/s ` +
    `p50=${(dur.med || 0).toFixed(1)}ms p95=${(dur['p(95)'] || 0).toFixed(1)}ms ` +
    `p99=${(dur['p(99)'] || 0).toFixed(1)}ms failed=${(failed * 100).toFixed(3)}%\n`;
  return {
    stdout: line,
    [`/results/${name}.json`]: JSON.stringify(data, null, 2),
  };
}
