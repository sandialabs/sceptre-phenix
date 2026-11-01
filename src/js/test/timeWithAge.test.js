// The shared timestamp: the time as the server sent it, to the second, then
// how long ago it was, kept current.
import fc from 'fast-check';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';

import TimeWithAge from '@/components/TimeWithAge.vue';
import { relativeTime } from '@/utils/relativeTime.js';
import { isoTimeArb } from './helpers/arbitraries.js';
import { componentDeclarations } from './helpers/css.js';
import { mountComponent, textOf } from './helpers/mount.js';

const render = (props) => mountComponent(TimeWithAge, { props });

afterEach(() => {
  vi.useRealTimers();
});

describe('a time', () => {
  it('shows the time as sent, then its age in parentheses', async () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-29T02:03:22Z') });
    const wrapper = await render({ time: '2026-10-28T14:03:22Z' });

    expect(textOf(wrapper)).toBe('2026-10-28T14:03:22Z (12 hours ago)');
    const time = wrapper.find('time');
    expect(time.attributes('datetime')).toBe('2026-10-28T14:03:22Z');
    expect(textOf(time)).toBe('2026-10-28T14:03:22Z');
    expect(textOf(wrapper.find('.time-with-age-age'))).toBe('(12 hours ago)');
  });

  it('shows a fractional time to the second, the full time in datetime', async () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-28T15:35:00Z') });
    const wrapper = await render({ time: '2026-10-28T15:30:12.345678Z' });

    expect(textOf(wrapper)).toBe('2026-10-28T15:30:12Z (4 minutes ago)');
    expect(wrapper.find('time').attributes('datetime')).toBe(
      '2026-10-28T15:30:12.345678Z',
    );
  });

  it('keeps its age current', async () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-28T14:03:52Z') });
    const wrapper = await render({ time: '2026-10-28T14:03:22Z' });
    expect(textOf(wrapper.find('.time-with-age-age'))).toBe('(just now)');

    vi.advanceTimersByTime(60_000);
    await nextTick();
    expect(textOf(wrapper.find('.time-with-age-age'))).toBe('(1 minute ago)');

    wrapper.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('shows the time and the age the helpers give, for any offset', async () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-28T12:00:00Z') });
    await fc.assert(
      fc.asyncProperty(isoTimeArb, async ({ time, ms }) => {
        const wrapper = await render({ time });
        const shown = textOf(wrapper.find('time'));
        expect(Date.parse(shown)).toBe(ms - (ms % 1000));
        expect(textOf(wrapper.find('.time-with-age-age'))).toBe(
          `(${relativeTime(ms)})`,
        );
        wrapper.unmount();
      }),
      { numRuns: 20 },
    );
  });
});

describe('no time', () => {
  it.each([
    ['', 'N/A', 'N/A'],
    ['', '', ''],
    ['garbled', 'N/A', 'garbled'],
    ['1', 'N/A', '1'],
  ])(
    '%j with fallback %j shows %j and no age',
    async (time, fallback, text) => {
      const wrapper = await render({ time, fallback });
      expect(textOf(wrapper)).toBe(text);
      expect(wrapper.find('time').exists()).toBe(false);
      expect(wrapper.find('.time-with-age-age').exists()).toBe(false);
    },
  );
});

// A narrow column wraps between the date, the time and the age, never inside
// one, so the time does not widen a table.
it('wraps only between its parts', async () => {
  const wrapper = await render({ time: '2026-10-28T14:03:22-06:00' });
  const parts = wrapper.findAll('.time-with-age-part').map(textOf);
  expect(parts.slice(0, 2)).toEqual(['2026-10-28T', '14:03:22-06:00']);
  expect(wrapper.find('time wbr').exists()).toBe(true);
  expect(
    componentDeclarations(
      'src/components/TimeWithAge.vue',
      '.time-with-age-part',
    ),
  ).toEqual({ 'white-space': 'nowrap' });
});
