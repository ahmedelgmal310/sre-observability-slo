// Baseline: steady traffic with no faults. Thresholds mirror the gateway SLOs,
// so a regression in latency or availability fails the run (and CI).
import { journey, summary } from './lib.js';

const RATE = parseInt(__ENV.RATE || '15', 10);
const DURATION = __ENV.DURATION || '2m';

export const options = {
  scenarios: {
    baseline: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 20,
      maxVUs: 100,
    },
  },
  thresholds: {
    // Latency SLO: p95 < 300ms (client-side, includes network).
    http_req_duration: ['p(95)<300'],
    // Availability SLO: 99.5% success.
    http_req_failed: ['rate<0.005'],
    checks: ['rate>0.995'],
  },
};

export default function () {
  journey();
}

export function handleSummary(data) {
  return summary('baseline', data);
}
