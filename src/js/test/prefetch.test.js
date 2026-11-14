import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { BACK_SPREAD_MS, forgetServer, setWsDown } from '@/utils/freshness.js';
import {
  lazyRouteLoaders,
  prefetchListedFiles,
  prefetchSequentially,
  schedulePrefetch,
} from '@/utils/prefetch.js';

import { fileListTag } from '../plugins/builder-prefetch.js';

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

  test('skips a route that opts out of prefetching', () => {
    const lazy = () => Promise.resolve({});
    const onDemand = () => Promise.resolve({});
    const routes = [
      { components: { default: onDemand }, meta: { prefetch: false } },
      { components: { default: lazy }, meta: { title: 'Page' } },
    ];
    expect(lazyRouteLoaders(routes)).toEqual([lazy]);
  });

  test('the router does not prefetch the Builder', async () => {
    const { default: router } = await import('@/router.js');
    const builder = router
      .getRoutes()
      .find((route) => route.name === 'builder');
    expect(builder.meta.prefetch).toBe(false);
    expect(lazyRouteLoaders([builder])).toEqual([]);
  });

  test('the router prefetches the VNC console page', async () => {
    const { default: router } = await import('@/router.js');
    const consolePage = router
      .getRoutes()
      .find((route) => route.path === '/vnc');
    expect(consolePage.meta.prefetch).toBeUndefined();
    expect(lazyRouteLoaders([consolePage])).toEqual([
      consolePage.components.default,
    ]);
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

describe('prefetchListedFiles', () => {
  const URLS = [
    '/assets/Builder-A.js',
    '/assets/Builder-A.css',
    '/assets/groupingWorker-B.js',
  ];

  // The list as the build writes it into index.html.
  function addList(urls) {
    const { tag, attrs, children } = fileListTag(urls);
    const list = document.createElement(tag);
    for (const [name, value] of Object.entries(attrs)) {
      list.setAttribute(name, value);
    }
    list.textContent = children;
    document.head.append(list);
  }

  function prefetchLinks() {
    return [...document.head.querySelectorAll('link[rel="prefetch"]')].map(
      (link) => ({
        href: link.getAttribute('href'),
        crossorigin: link.getAttribute('crossorigin'),
      }),
    );
  }

  function idleCallbacks() {
    const requestIdleCallback = vi.fn();
    vi.stubGlobal('requestIdleCallback', requestIdleCallback);
    return () => {
      for (const [start] of requestIdleCallback.mock.calls) start();
    };
  }

  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    document.head.replaceChildren();
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  test('waits for the page to load and then for the browser to be idle', () => {
    vi.spyOn(document, 'readyState', 'get').mockReturnValue('interactive');
    const requestIdleCallback = vi.fn();
    vi.stubGlobal('requestIdleCallback', requestIdleCallback);
    addList(URLS);

    prefetchListedFiles();
    vi.runAllTimers();
    expect(requestIdleCallback).not.toHaveBeenCalled();

    window.dispatchEvent(new Event('load'));
    expect(requestIdleCallback).toHaveBeenCalledOnce();
    expect(prefetchLinks()).toEqual([]);

    requestIdleCallback.mock.calls[0][0]();
    expect(prefetchLinks()).toHaveLength(URLS.length);
  });

  test('adds a prefetch link for each file, with crossorigin only on scripts', () => {
    const runIdle = idleCallbacks();
    addList(URLS);

    prefetchListedFiles();
    runIdle();
    expect(prefetchLinks()).toEqual([
      { href: '/assets/Builder-A.js', crossorigin: '' },
      { href: '/assets/Builder-A.css', crossorigin: null },
      { href: '/assets/groupingWorker-B.js', crossorigin: '' },
    ]);
  });

  test('adds the links for any list of files', () => {
    const file = fc
      .tuple(fc.stringMatching(/^[\w.-]{1,12}$/), fc.constantFrom('js', 'css'))
      .map(([name, ext]) => `/assets/${name}.${ext}`);

    fc.assert(
      fc.property(fc.uniqueArray(file, { maxLength: 8 }), (urls) => {
        document.head.replaceChildren();
        const runIdle = idleCallbacks();
        addList(urls);

        prefetchListedFiles();
        runIdle();
        expect(prefetchLinks()).toEqual(
          urls.map((href) => ({
            href,
            crossorigin: href.endsWith('.js') ? '' : null,
          })),
        );
      }),
      { numRuns: 50 },
    );
  });

  test('adds the links one time, and removes the list', () => {
    const runIdle = idleCallbacks();
    addList(URLS);

    prefetchListedFiles();
    prefetchListedFiles();
    runIdle();
    prefetchListedFiles();
    runIdle();
    expect(prefetchLinks()).toHaveLength(URLS.length);
    expect(document.getElementById(fileListTag([]).attrs.id)).toBeNull();
  });

  test('does nothing without the list, as on the dev server', () => {
    const runIdle = idleCallbacks();

    prefetchListedFiles();
    expect(() => runIdle()).not.toThrow();
    expect(document.head.querySelectorAll('link')).toHaveLength(0);
  });

  test('skips prefetching when the user asked to save data', () => {
    const runIdle = idleCallbacks();
    vi.stubGlobal('navigator', { connection: { saveData: true } });
    addList(URLS);

    prefetchListedFiles();
    runIdle();
    expect(prefetchLinks()).toEqual([]);
  });
});
