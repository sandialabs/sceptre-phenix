// How an operation on several items went, item by item.
import { describe, expect, it } from 'vitest';

import OperationOutcome from '@/components/jobs/OperationOutcome.vue';
import { mountComponent, textOf } from './helpers/mount.js';

const items = [
  { name: 'web', status: 'succeeded' },
  { name: 'db', status: 'failed', error: 'vm db is locked', retryable: true },
  { name: 'fw', status: 'failed', error: 'boom' },
  { name: 'dns', status: 'skipped', error: "you don't have permission" },
];

const render = (props) => mountComponent(OperationOutcome, { props });

const retryButtons = (wrapper) =>
  wrapper.findAll('button').filter((b) => b.text().startsWith('Retry'));

describe('OperationOutcome', () => {
  it('groups the items by outcome, failures first, with their reasons', async () => {
    const wrapper = await render({ items, noun: 'VM' });
    const text = textOf(wrapper);

    expect(text.indexOf('Failed: 2 VMs')).toBeLessThan(
      text.indexOf('Skipped: 1 VM'),
    );
    expect(text).toContain('Succeeded: 1 VM');
    expect(text).not.toContain('Canceled');
    expect(text).toContain('db: vm db is locked can retry');
    expect(text).toContain("dns: you don't have permission");
    expect(wrapper.find('[aria-expanded="true"]').exists()).toBe(true);

    const [retry] = retryButtons(wrapper);
    expect(retry.text()).toBe('Retry failed (1)');
    await retry.trigger('click');
    expect(wrapper.emitted('retry')).toEqual([[['db']]]);
  });

  it('offers no retry without a failure worth retrying', async () => {
    const wrapper = await render({
      items: items.filter((item) => !item.retryable),
    });
    expect(retryButtons(wrapper)).toEqual([]);
  });
});
