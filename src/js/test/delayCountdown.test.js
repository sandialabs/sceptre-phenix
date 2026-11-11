// The countdown to the start of a VM that waits for a timer: its text, and the
// one clock that every countdown on the page shares.
import { mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h, nextTick, ref } from 'vue';

import {
  countdownNow,
  countdownText,
  nextDue,
  spanText,
  startDue,
  useCountdownClock,
  watchCountdown,
} from '@/components/experiment/delayCountdown.js';

const START = Date.parse('2026-01-01T10:00:00Z');

// only the intervals and the clock: Vue's own timeouts are not the clock's
beforeEach(() => {
  vi.useFakeTimers({
    now: START,
    toFake: ['setInterval', 'clearInterval', 'Date'],
  });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('the text of a countdown', () => {
  it.each([
    [0, '0s'],
    [400, '1s'],
    [12_000, '12s'],
    [252_000, '4m 12s'],
    [3_852_000, '1h 4m 12s'],
    [3_600_000, '1h 0m 0s'],
  ])('writes %i ms as %s', (ms, text) => {
    expect(spanText(ms)).toBe(text);
  });

  it('says how long until the due time, and "starts now" after it', () => {
    expect(countdownText(START + 252_000, START)).toBe('starts in 4m 12s');
    expect(countdownText(START, START)).toBe('starts now');
    expect(countdownText(START - 5000, START)).toBe('starts now');
  });
});

describe('the due time of a VM', () => {
  const waiting = { state: 'BUILDING', delayed_start: 'timer:5m' };

  it.each([
    ['a timer VM', waiting, '2026-01-01T10:05:00Z', START + 300_000],
    ['a user VM', { ...waiting, delayed_start: 'user' }, undefined, null],
    ['a C2 VM', { ...waiting, delayed_start: 'cc:server' }, '', null],
    [
      'a started VM',
      { ...waiting, state: 'RUNNING' },
      '2026-01-01T10:05:00Z',
      null,
    ],
    ['a bad due time', waiting, 'soon', null],
  ])('of %s', (_, vm, due, want) => {
    expect(startDue(vm, due)).toBe(want);
  });

  it('gives the earliest of several due times', () => {
    expect(
      nextDue({
        a: '2026-01-01T10:05:00Z',
        b: '2026-01-01T10:01:30Z',
        c: 'bad',
      }),
    ).toBe(START + 90_000);
    expect(nextDue({})).toBeNull();
    expect(nextDue(undefined)).toBeNull();
  });
});

describe('the shared clock', () => {
  it('ticks each second with one interval for all its watchers', () => {
    const spy = vi.spyOn(globalThis, 'setInterval');
    const stopA = watchCountdown();
    const stopB = watchCountdown();
    expect(spy).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(1);

    vi.advanceTimersByTime(3000);
    expect(countdownNow.value).toBe(START + 3000);

    stopA();
    // a second call does not count again
    stopA();
    expect(vi.getTimerCount()).toBe(1);
    stopB();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('runs while a component shows a countdown, and stops when it unmounts', async () => {
    const active = ref(false);
    const Shown = defineComponent({
      setup() {
        const now = useCountdownClock(() => active.value);
        return () => h('span', countdownText(START + 10_000, now.value));
      },
    });
    const wrapper = mount(Shown);
    expect(vi.getTimerCount()).toBe(0);

    active.value = true;
    await nextTick();
    expect(vi.getTimerCount()).toBe(1);
    vi.advanceTimersByTime(4000);
    await nextTick();
    expect(wrapper.text()).toBe('starts in 6s');

    active.value = false;
    await nextTick();
    expect(vi.getTimerCount()).toBe(0);

    active.value = true;
    await nextTick();
    wrapper.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
