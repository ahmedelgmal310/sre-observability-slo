// Chaos: the same traffic, but every request carries W3C baggage asking the
// inventory service (two hops behind the gateway) to fail a share of calls.
// The failure propagates as 502s at the edge and must trip the fast-burn alert.
import { journey, summary } from './lib.js';

const RATE = parseInt(__ENV.RATE || '15', 10);
const DURATION = __ENV.DURATION || '6m';
const ERROR_RATE = __ENV.CHAOS_ERROR_RATE || '0.3';
const TARGET = __ENV.CHAOS_TARGET || 'inventory';

const BAGGAGE = { baggage: `chaos.target=${TARGET},chaos.error_rate=${ERROR_RATE}` };

export const options = {
  scenarios: {
    chaos: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 20,
      maxVUs: 100,
    },
  },
  // Failures are the point of this run; no thresholds.
};

export default function () {
  journey(BAGGAGE);
}

export function handleSummary(data) {
  return summary('chaos', data);
}
