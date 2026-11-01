import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { BACK_SPREAD_MS, forgetServer, setWsDown } from '@/utils/freshness.js';
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
  test('loads one chunk at a time, in order, and survives failures', async () => {
    const outcome = fc.constantFrom('loads', 'rejects', 'throws');
    await fc.assert(
      fc.asyncProperty(
        fc.array(outcome, { maxLength: 6 }),
        async (outcomes) => {
          const events = [];
          const loaders = outcomes.map((how, i) => () => {
            events.push(`start ${i}`);
            if (how === 'throws') throw new Error('bad chunk');
            return Promise.resolve().then(() => {
              events.push(`end ${i}`);
              if (how === 'rejects') throw new Error('offline');
            });
          });
          await prefetchSequentially(loaders);
          expect(events).toEqual(
            outcomes.flatMap((how, i) =>
              how === 'throws' ? [`start ${i}`] : [`start ${i}`, `end ${i}`],
            ),
          );
        },
      ),
    );
  });
});

describe('prefetchSequentially while the server is away', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    forgetServer();
    vi.useRealTimers();
  });

  test('loads nothing while the server shuts down, and the rest once it is back', async () => {
    const loaded = [];
    const loaders = [0, 1, 2].map((i) => async () => {
      loaded.push(i);
      // the server says it is shutting down while the first chunk loads
      if (i === 0) setWsDown(true, 'shutdown');
    });

    const done = prefetchSequentially(loaders);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(loaded).toEqual([0]);

    setWsDown(false);
    await vi.advanceTimersByTimeAsync(BACK_SPREAD_MS);
    await done;
    expect(loaded).toEqual([0, 1, 2]);
  });
});

describe('schedulePrefetch', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  test('waits for the browser to be idle', () => {
    const load = vi.fn(() => Promise.resolve());
    const requestIdleCallback = vi.fn();
    vi.stubGlobal('requestIdleCallback', requestIdleCallback);

    schedulePrefetch([load]);
    expect(load).not.toHaveBeenCalled();
    requestIdleCallback.mock.calls[0][0]();
    expect(load).toHaveBeenCalledOnce();
  });

  // happy-dom, like Safari, has no idle callbacks
  test('without idle callbacks, waits for the page to finish loading', () => {
    vi.spyOn(document, 'readyState', 'get').mockReturnValue('interactive');
    const load = vi.fn(() => Promise.resolve());

    schedulePrefetch([load]);
    vi.runAllTimers();
    expect(load).not.toHaveBeenCalled();

    window.dispatchEvent(new Event('load'));
    window.dispatchEvent(new Event('load'));
    vi.runAllTimers();
    expect(load).toHaveBeenCalledOnce();
  });

  test('without idle callbacks, starts soon if the page already loaded', () => {
    expect(document.readyState).toBe('complete');
    const addEventListener = vi.spyOn(window, 'addEventListener');
    const load = vi.fn(() => Promise.resolve());

    schedulePrefetch([load]);
    expect(load).not.toHaveBeenCalled();
    expect(addEventListener).not.toHaveBeenCalledWith(
      'load',
      expect.anything(),
      expect.anything(),
    );
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
