// Scenarios for the tests of the scenario UI (M19): the example of plan §2.10, with the references as the
// configuration stores them (UUIDs).
import type { Scenario } from '../api/generated/model';

export const ESP = 'a1b2c3d4-0000-4000-8000-000000000042';

/** The scenario of plan §2.10 (with the references as UUIDs, as the configuration stores them). */
export const mqttOutage: Scenario = {
  name: 'mqtt-outage',
  target: { device: ESP },
  capture: true,
  steps: [
    { id: 'normal', at: '0s', profile: 'normal' },
    { id: 'slow', at: '10s', fault: { latency: '200ms', jitter: '50ms' } },
    { id: 'lossy', at: '20s', fault: { latency: '200ms', jitter: '50ms', loss: '10%' } },
    { id: 'cut', at: '30s', rule: { action: 'drop', protocol: 'tcp', ports: [8883], cut_existing: true } },
    { id: 'restore', at: '45s', restore: true },
  ],
  checks: [
    { reconnected: { protocol: 'tcp', ports: [8883] }, window: { from: 'restore', within: '30s' } },
    { dns_query_seen: { name: 'broker.example.com' }, window: { from: 'restore', within: '30s' } },
  ],
};
