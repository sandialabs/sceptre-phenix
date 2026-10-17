// How an operation on several items went, item by item.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import OperationOutcome from '@/components/jobs/OperationOutcome.vue';
import { plural } from '@/utils/plural.js';
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

  it('lists each item once, under its outcome, and retries the failures worth it', async () => {
    const outcomes = fc.uniqueArray(
      fc.record({
        name: fc.stringMatching(/^[a-z][a-z0-9]{0,4}$/),
        status: fc.constantFrom('succeeded', 'failed', 'skipped', 'canceled'),
        retryable: fc.boolean(),
      }),
      { selector: (item) => item.name, maxLength: 8 },
    );
    // each outcome's group, in the order they show
    const labels = {
      failed: 'Failed',
      skipped: 'Skipped',
      canceled: 'Canceled',
      succeeded: 'Succeeded',
    };
    await fc.assert(
      fc.asyncProperty(outcomes, async (list) => {
        const wrapper = await render({ items: list, noun: 'VM' });
        const groups = wrapper
          .findAll('.outcome-group')
          .map((group) => [
            textOf(group.find('.outcome-trigger')),
            group.findAll('.outcome-name').map(textOf),
          ]);
        expect(groups).toEqual(
          Object.entries(labels)
            .map(([status, label]) => {
              const names = list
                .filter((i) => i.status === status)
                .map((i) => i.name);
              return [`${label}: ${plural(names.length, 'VM')}`, names];
            })
            .filter(([, names]) => names.length),
        );

        const worth = list.filter((i) => i.status === 'failed' && i.retryable);
        const [retry] = retryButtons(wrapper);
        expect(retry?.text()).toBe(
          worth.length ? `Retry failed (${worth.length})` : undefined,
        );
        await retry?.trigger('click');
        expect(wrapper.emitted('retry')).toEqual(
          worth.length ? [[worth.map((i) => i.name)]] : undefined,
        );
        wrapper.unmount();
      }),
      { numRuns: 20 },
    );
  });
});
