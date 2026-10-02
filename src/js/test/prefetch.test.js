import { afterEach, describe, expect, test, vi } from 'vitest';

import {
  lazyRouteLoaders,
  prefetchSequentially,
  schedulePrefetch,
} from '@/utils/prefetch.js';

describe('lazyRouteLoaders', () => {
  test('returns only lazy (function) components', () => {
    const lazy = () => Promise.resolve({});
    const eager = { render() {} };
    const routes = [
      { components: { default: lazy } },
      { components: { default: eager } },
      { redirect: '/' },
      { components: { default: undefined } },
    ];
    expect(lazyRouteLoaders(routes)).toEqual([lazy]);
  });
});

describe('prefetchSequentially', () => {
  test('loads one chunk at a time and survives failures', async () => {
    const events = [];
    const loader = (name, fail) => async () => {
      events.push('start ' + name);
      await Promise.resolve();
      events.push('end ' + name);
      if (fail) throw new Error('offline');
    };
    await prefetchSequentially([loader('a', true), loader('b')]);
    expect(events).toEqual(['start a', 'end a', 'start b', 'end b']);
  });
});

describe('schedulePrefetch', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  // a browser without idle callbacks (Safari), whose page is in this state
  function withoutIdleCallbacks(readyState) {
    vi.useFakeTimers();
    vi.stubGlobal('requestIdleCallback', undefined);
    vi.stubGlobal('document', { readyState });
    const win = { addEventListener: vi.fn() };
    vi.stubGlobal('window', win);
    return win;
  }

  test('waits for the browser to be idle', () => {
    const load = vi.fn(() => Promise.resolve());
    const requestIdleCallback = vi.fn();
    vi.stubGlobal('requestIdleCallback', requestIdleCallback);

    schedulePrefetch([load]);
    expect(load).not.toHaveBeenCalled();
    requestIdleCallback.mock.calls[0][0]();
    expect(load).toHaveBeenCalledOnce();
  });

  test('without idle callbacks, waits for the page to finish loading', () => {
    const win = withoutIdleCallbacks('interactive');
    const load = vi.fn(() => Promise.resolve());

    schedulePrefetch([load]);
    vi.runAllTimers();
    expect(load).not.toHaveBeenCalled();

    const [event, onLoad] = win.addEventListener.mock.calls[0];
    expect(event).toBe('load');
    onLoad();
    vi.runAllTimers();
    expect(load).toHaveBeenCalledOnce();
  });

  test('without idle callbacks, starts soon if the page already loaded', () => {
    const win = withoutIdleCallbacks('complete');
    const load = vi.fn(() => Promise.resolve());

    schedulePrefetch([load]);
    expect(win.addEventListener).not.toHaveBeenCalled();
    vi.runAllTimers();
    expect(load).toHaveBeenCalledOnce();
  });

  test('skips prefetching when the user asked to save data', () => {
    const requestIdleCallback = vi.fn();
    vi.stubGlobal('requestIdleCallback', requestIdleCallback);
    vi.stubGlobal('navigator', { connection: { saveData: true } });

    schedulePrefetch([vi.fn()]);
    expect(requestIdleCallback).not.toHaveBeenCalled();
  });
});
