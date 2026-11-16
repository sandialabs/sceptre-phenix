// noVNC loads once, as its own lazy chunk, through loadRfb.
import { expect, it, vi } from 'vitest';

const loads = vi.hoisted(() => ({ count: 0 }));
vi.mock('@novnc/novnc', () => {
  loads.count += 1;
  return { default: class FakeRFB {} };
});

import { loadRfb } from '@/vnc/loadRfb.js';

it('imports noVNC once and gives its RFB class to every caller', async () => {
  const first = loadRfb();
  expect(loadRfb()).toBe(first);
  const RFB = await first;
  expect(RFB.name).toBe('FakeRFB');
  await expect(loadRfb()).resolves.toBe(RFB);
  expect(loads.count).toBe(1);
});
