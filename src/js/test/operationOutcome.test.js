// How an operation on several items went, item by item.
import { describe, expect, it } from 'vitest';

import OperationOutcome from '@/components/jobs/OperationOutcome.vue';
import { makeContext } from './helpers/context.js';
import { buttons, renderSSR, textOf } from './helpers/render.js';

const items = [
  { name: 'web', status: 'succeeded' },
  { name: 'db', status: 'failed', error: 'vm db is locked', retryable: true },
  { name: 'fw', status: 'failed', error: 'boom' },
  { name: 'dns', status: 'skipped', error: "you don't have permission" },
];

describe('OperationOutcome', () => {
  it('groups the items by outcome, failures first, with their reasons', async () => {
    const html = await renderSSR(OperationOutcome, { items, noun: 'VM' });
    const text = textOf(html);

    expect(text.indexOf('Failed: 2 VMs')).toBeLessThan(
      text.indexOf('Skipped: 1 VM'),
    );
    expect(text).toContain('Succeeded: 1 VM');
    expect(text).not.toContain('Canceled');
    expect(text).toContain('db : vm db is locked can retry');
    expect(text).toContain("dns : you don't have permission");
    expect(html).toContain('aria-expanded="true"');
    expect(buttons(html)).toContain('Retry failed (1)');
  });

  it('offers no retry without a failure worth retrying', async () => {
    const html = await renderSSR(OperationOutcome, {
      items: items.filter((item) => !item.retryable),
    });
    expect(buttons(html).some((b) => b.startsWith('Retry'))).toBe(false);
  });

  it('asks to retry the failures worth it', () => {
    const emitted = [];
    const ctx = makeContext(OperationOutcome, {
      items,
      noun: 'VM',
      $emit: (...args) => emitted.push(args),
    });
    expect(ctx.retryable).toEqual(['db']);
  });
});
