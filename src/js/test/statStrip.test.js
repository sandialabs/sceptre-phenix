// The counts strip the list pages show beside their search boxes.
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

describe('clickable stats', () => {
  const stats = [
    { key: 'all', label: 'Things', value: 4, filter: 'Show all things' },
    {
      key: 'on',
      label: 'Running',
      value: 2,
      detail: '(1 new)',
      filter: 'Show only running things',
      active: true,
    },
    { key: 'size', label: 'Size', value: '3.0 GiB' },
  ];
  const render = () =>
    mountComponent(StatStrip, { props: { label: 'Totals', stats } });

  it('makes a stat with a filter a toggle button, pressed when active', async () => {
    const wrapper = await render();
    const tiles = wrapper.findAll('button');
    expect(
      tiles.map((b) => [
        b.attributes('type'),
        b.attributes('aria-label'),
        b.attributes('aria-pressed'),
        b.attributes('title'),
      ]),
    ).toEqual([
      ['button', 'Things 4', 'false', 'Show all things'],
      ['button', 'Running 2 (1 new)', 'true', 'Show only running things'],
    ]);
    // the stat without a filter stays text
    expect(wrapper.find('.stat-size button').exists()).toBe(false);
    expect(wrapper.find('.stat-size').classes()).not.toContain('is-clickable');
    expect(wrapper.find('.stat-on').classes()).toEqual(
      expect.arrayContaining(['is-clickable', 'is-active']),
    );
    expect(wrapper.find('.stat-all').classes()).not.toContain('is-active');
    // a term and its value, as before
    expect(textOf(wrapper.find('.stat-on dd'))).toBe('2 (1 new)');
  });

  it('emits select with the key of the stat clicked', async () => {
    const wrapper = await render();
    await wrapper.find('.stat-on button').trigger('click');
    await wrapper.find('.stat-all button').trigger('click');
    expect(wrapper.emitted('select')).toEqual([['on'], ['all']]);
  });
});
