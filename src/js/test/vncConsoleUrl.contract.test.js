// The console page URL (Q3): /vnc?vm=exp/vm&vm=…&layout=…. The order of the
// vm parameters is the order of the panes, and a bad value is dropped.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  LAYOUTS,
  LAYOUT_SLOTS,
  MAX_PANES,
  consoleLocation,
  defaultLayout,
  layoutFits,
  parseConsoleQuery,
  splitVmKey,
} from '@/vnc/consoleUrl.js';

describe('the VMs of the console', () => {
  it('keeps the order of the vm parameters, from any experiment', () => {
    const query = { vm: ['demo/kali', 'demo/web', 'train/plc-1'] };
    expect(parseConsoleQuery(query).vms).toEqual([
      'demo/kali',
      'demo/web',
      'train/plc-1',
    ]);
  });

  it('reads one vm parameter as one pane', () => {
    expect(parseConsoleQuery({ vm: 'demo/web' })).toEqual({
      vms: ['demo/web'],
      layout: 'single',
    });
  });

  it.each([
    ['no slash', 'demo'],
    ['no experiment', '/web'],
    ['no VM', 'demo/'],
    ['two slashes', 'demo/web/x'],
    ['an empty value', ''],
    ['spaces only', ' / '],
    ['a value with no text', null],
  ])('drops a value with %s', (_, bad) => {
    const query = { vm: ['demo/kali', bad, 'demo/web'] };
    expect(parseConsoleQuery(query).vms).toEqual(['demo/kali', 'demo/web']);
  });

  it('shows a VM that is named twice one time, in its first place', () => {
    const query = { vm: ['demo/web', 'demo/db', 'demo/web'] };
    expect(parseConsoleQuery(query).vms).toEqual(['demo/web', 'demo/db']);
  });

  it(`shows at most ${MAX_PANES} VMs, the first ones`, () => {
    const vm = ['a/1', 'a/2', 'a/3', 'a/4', 'a/5'];
    expect(parseConsoleQuery({ vm }).vms).toEqual(vm.slice(0, 4));
  });

  it('has no VM without a vm parameter', () => {
    expect(parseConsoleQuery({})).toEqual({ vms: [], layout: 'single' });
  });

  it('splits a VM key into its experiment and VM', () => {
    expect(splitVmKey('demo/web')).toEqual({ experiment: 'demo', vm: 'web' });
    expect(splitVmKey('demo')).toBeNull();
  });
});

describe('the layout of the console', () => {
  it.each([
    [1, 'single'],
    [2, 'cols'],
    [3, 'main-2'],
    [4, 'grid'],
  ])('is the default for %i VMs when the URL names none', (count, layout) => {
    const vm = ['a/1', 'a/2', 'a/3', 'a/4'].slice(0, count);
    expect(defaultLayout(count)).toBe(layout);
    expect(parseConsoleQuery({ vm }).layout).toBe(layout);
  });

  it.each(LAYOUTS)('keeps the layout %s from the URL when it fits', (name) => {
    const query = { vm: ['a/1'], layout: name };
    expect(parseConsoleQuery(query).layout).toBe(name);
  });

  it.each([
    ['an unknown layout', 'bogus', ['a/1', 'a/2'], 'cols'],
    ['two layouts', ['rows', 'grid'], ['a/1', 'a/2'], 'cols'],
    ['a layout with too few panes', 'single', ['a/1', 'a/2'], 'cols'],
    ['two panes for three VMs', 'rows', ['a/1', 'a/2', 'a/3'], 'main-2'],
  ])('uses the default for %s', (_, layout, vm, expected) => {
    expect(parseConsoleQuery({ vm, layout }).layout).toBe(expected);
  });

  it('fits a layout that has a pane for each VM', () => {
    expect(LAYOUT_SLOTS).toEqual({
      single: 1,
      cols: 2,
      rows: 2,
      'main-2': 3,
      grid: 4,
    });
    expect(layoutFits('cols', 2)).toBe(true);
    expect(layoutFits('cols', 3)).toBe(false);
    expect(layoutFits('bogus', 1)).toBe(false);
  });
});

describe('the console location', () => {
  it('names each VM in order and leaves out the default layout', () => {
    expect(consoleLocation(['demo/web', 'demo/db'])).toEqual({
      name: 'vnc',
      query: { vm: ['demo/web', 'demo/db'] },
    });
    expect(consoleLocation(['demo/web', 'demo/db'], 'rows')).toEqual({
      name: 'vnc',
      query: { vm: ['demo/web', 'demo/db'], layout: 'rows' },
    });
  });

  const segment = fc.stringMatching(/^[a-z0-9][a-z0-9_.-]{0,11}$/);
  const key = fc.tuple(segment, segment).map(([e, v]) => `${e}/${v}`);

  it('reads back the VMs and layout it writes', () => {
    fc.assert(
      fc.property(
        fc.uniqueArray(key, { minLength: 1, maxLength: MAX_PANES }),
        fc.constantFrom(...LAYOUTS),
        (vms, layout) => {
          const wanted = layoutFits(layout, vms.length)
            ? layout
            : defaultLayout(vms.length);
          const { query } = consoleLocation(vms, wanted);
          expect(parseConsoleQuery(query)).toEqual({ vms, layout: wanted });
        },
      ),
      { numRuns: 200 },
    );
  });
});
