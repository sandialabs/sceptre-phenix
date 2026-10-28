// The counts strip the Experiments, Disks and Logs pages show beside their
// search boxes.
import { describe, expect, it } from 'vitest';

import StatStrip from '@/components/StatStrip.vue';
import { mountComponent, textOf } from './helpers/mount.js';

describe('the stat strip', () => {
  const render = (stats) =>
    mountComponent(StatStrip, { props: { label: 'Totals', stats } });

  it('names the group and lists each stat as a term and its value', async () => {
    const wrapper = await render([
      { key: 'all', label: 'Things', value: 4 },
      { key: 'on', label: 'Running', value: 2, tone: 'success' },
      { key: 'size', label: 'Size', value: '3.0 GiB', detail: '(1 unknown)' },
    ]);
    const group = wrapper.find('[role="group"]');
    expect(group.attributes('aria-label')).toBe('Totals');
    expect(
      wrapper
        .findAll('.stat')
        .map((s) => [textOf(s.find('dt')), textOf(s.find('dd'))]),
    ).toEqual([
      ['Things', '4'],
      ['Running', '2'],
      ['Size', '3.0 GiB (1 unknown)'],
    ]);
    expect(wrapper.findAll('.stat.is-success')).toHaveLength(1);
    expect(wrapper.find('.stat-on').classes()).toContain('is-success');
  });

  it('shows no detail when a stat has none', async () => {
    const wrapper = await render([{ key: 'a', label: 'A', value: 0 }]);
    expect(wrapper.find('.stat-detail').exists()).toBe(false);
    expect(textOf(wrapper.find('dd'))).toBe('0');
  });
});
