// A stale chunk: which errors are failed loads of build files, and the reload
// that gets the new build, which can happen only once in a while.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RELOAD_GUARD_MS } from '@/utils/staleChunk.js';
import { blockedStorage, memoryStorage } from './helpers/storage.js';

let chunk;

beforeEach(async () => {
  // a fresh page: it has not reloaded or told the user yet
  vi.resetModules();
  chunk = await import('@/utils/staleChunk.js');
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const location = () => ({ assign: vi.fn(), reload: vi.fn() });

describe('isChunkLoadError', () => {
  it.each([
    [
      'Chromium',
      new TypeError(
        'Failed to fetch dynamically imported module: https://h/igor/bb-8/phenix/assets/elk-1a2b.js',
      ),
    ],
    [
      'Firefox',
      new TypeError(
        'error loading dynamically imported module: https://h/assets/Builder-9f.js',
      ),
    ],
    ['Safari', new TypeError('Importing a module script failed.')],
    ['a CSS preload', new Error('Unable to preload CSS for /assets/x.css')],
    ['a chunk', new Error('Loading chunk 12 failed.')],
    [
      'a named error',
      Object.assign(new Error('x'), { name: 'ChunkLoadError' }),
    ],
    ['a message', 'Failed to fetch dynamically imported module: /a.js'],
  ])('is true for %s', (_, err) => {
    expect(chunk.isChunkLoadError(err)).toBe(true);
  });

  it.each([
    ['another error', new Error('layout "elk" returned no positions')],
    ['a type error', new TypeError('Cannot read properties of undefined')],
    ['nothing', undefined],
    ['null', null],
  ])('is false for %s', (_, err) => {
    expect(chunk.isChunkLoadError(err)).toBe(false);
  });
});

describe('reloadForStaleChunk', () => {
  it('reloads to the page asked for, once, and records when', () => {
    const storage = memoryStorage();
    const loc = location();
    const notify = vi.fn();
    const href = '/igor/bb-8/phenix/builder';

    expect(
      chunk.reloadForStaleChunk(href, {
        storage,
        location: loc,
        now: 5e6,
        notify,
      }),
    ).toBe('reloading');
    expect(loc.assign).toHaveBeenCalledWith(href);
    expect(storage.getItem(chunk.RELOAD_GUARD_KEY)).toBe('5000000');

    // the router and Vite both report the same failure: one reload, no notice
    expect(
      chunk.reloadForStaleChunk(href, {
        storage,
        location: loc,
        now: 5e6,
        notify,
      }),
    ).toBe('reloading');
    expect(loc.assign).toHaveBeenCalledTimes(1);
    expect(notify).not.toHaveBeenCalled();
  });

  it('reloads the current page without an address', () => {
    const loc = location();
    chunk.reloadForStaleChunk('', {
      storage: memoryStorage(),
      location: loc,
      now: 5e6,
      notify: vi.fn(),
    });
    expect(loc.reload).toHaveBeenCalledTimes(1);
    expect(loc.assign).not.toHaveBeenCalled();
  });

  it.each([
    ['just after the last reload', 5e6 - 1000, true],
    ['at the end of the guard', 5e6 - RELOAD_GUARD_MS, false],
    ['after a clock moved back', 5e6 + 1000, true],
  ])('tells the user to reload %s', (_, last, blocked) => {
    const storage = memoryStorage();
    storage.setItem(chunk.RELOAD_GUARD_KEY, String(last));
    const loc = location();
    const notify = vi.fn();

    const got = chunk.reloadForStaleChunk('/soh/x', {
      storage,
      location: loc,
      now: 5e6,
      notify,
    });
    expect(got).toBe(blocked ? 'blocked' : 'reloading');
    expect(loc.assign).toHaveBeenCalledTimes(blocked ? 0 : 1);
    expect(notify).toHaveBeenCalledTimes(blocked ? 1 : 0);
  });

  it('tells the user once when the guard blocks', () => {
    const storage = memoryStorage();
    storage.setItem(chunk.RELOAD_GUARD_KEY, '4999000');
    const loc = location();
    const notify = vi.fn();
    const opts = { storage, location: loc, now: 5e6, notify };

    expect(chunk.reloadForStaleChunk('/a', opts)).toBe('blocked');
    expect(chunk.reloadForStaleChunk('/b', opts)).toBe('blocked');
    chunk.showUpdatedNotice(notify);
    expect(notify).toHaveBeenCalledTimes(1);
    expect(notify.mock.calls[0][0]).toMatchObject({
      type: 'is-warning',
      indefinite: true,
      message: `<span role="alert">${chunk.UPDATED_MESSAGE}</span>`,
    });
    expect(loc.assign).not.toHaveBeenCalled();
  });

  it.each([
    ['storage that throws', blockedStorage()],
    ['no storage', null],
  ])('does not reload with %s, as it could loop', (_, storage) => {
    const loc = location();
    const notify = vi.fn();
    expect(
      chunk.reloadForStaleChunk('/a', {
        storage,
        location: loc,
        now: 5e6,
        notify,
      }),
    ).toBe('blocked');
    expect(loc.assign).not.toHaveBeenCalled();
    expect(notify).toHaveBeenCalledTimes(1);
  });

  it('uses the session storage and location of the window', () => {
    const loc = location();
    vi.stubGlobal('location', loc);
    sessionStorage.clear();
    expect(
      chunk.reloadForStaleChunk('/soh/x', { now: 5e6, notify: vi.fn() }),
    ).toBe('reloading');
    expect(loc.assign).toHaveBeenCalledWith('/soh/x');
    expect(sessionStorage.getItem(chunk.RELOAD_GUARD_KEY)).toBe('5000000');
    sessionStorage.clear();
  });

  it('opens the notice through Buefy by default', async () => {
    const open = vi.fn();
    vi.doMock('@/utils/notify.js', () => ({ openNotification: open }));
    vi.resetModules();
    const fresh = await import('@/utils/staleChunk.js');
    fresh.showUpdatedNotice();
    expect(open).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'is-warning' }),
    );
    vi.doUnmock('@/utils/notify.js');
  });
});

describe('handlePreloadError', () => {
  const failed = new TypeError(
    'Failed to fetch dynamically imported module: /assets/Builder-9f.js',
  );

  it('reloads to the page of a navigation under way', () => {
    const loc = location();
    const notify = vi.fn();
    const opts = { storage: memoryStorage(), location: loc, now: 5e6, notify };
    expect(chunk.handlePreloadError(failed, '/phenix/builder', opts)).toBe(
      'reloading',
    );
    expect(loc.assign).toHaveBeenCalledWith('/phenix/builder');
    expect(notify).not.toHaveBeenCalled();
  });

  it('tells the user, and does not reload, outside a navigation', () => {
    const loc = location();
    const notify = vi.fn();
    const opts = { storage: memoryStorage(), location: loc, now: 5e6, notify };
    expect(chunk.handlePreloadError(failed, null, opts)).toBe('blocked');
    expect(loc.assign).not.toHaveBeenCalled();
    expect(loc.reload).not.toHaveBeenCalled();
    expect(notify).toHaveBeenCalledTimes(1);
  });

  it('leaves a file that a page loads to the notice of that page', async () => {
    const notify = vi.fn();
    const seen = [];
    // as Vite reports a failed import() before the import rejects
    const load = () =>
      Promise.reject(failed).catch((err) => {
        seen.push(chunk.handlePreloadError(err, null, { notify }));
        throw err;
      });

    await expect(chunk.loadInPage(load)).rejects.toBe(failed);
    await expect(
      chunk.loadInPage(() => {
        seen.push(chunk.handlePreloadError(failed, null, { notify }));
        throw failed;
      }),
    ).rejects.toBe(failed);
    await expect(chunk.loadInPage(() => 'mod')).resolves.toBe('mod');
    expect(seen).toEqual([null, null]);
    expect(notify).not.toHaveBeenCalled();

    // a navigation under way still reloads to its page
    const loc = location();
    const opts = { storage: memoryStorage(), location: loc, now: 5e6, notify };
    const reload = chunk.loadInPage(() => {
      seen.push(chunk.handlePreloadError(failed, '/phenix/soh', opts));
      return Promise.reject(failed);
    });
    await expect(reload).rejects.toBe(failed);
    expect(seen[2]).toBe('reloading');
    expect(loc.assign).toHaveBeenCalledWith('/phenix/soh');
  });

  it('tells the user about other files once the page has loaded its own', async () => {
    const notify = vi.fn();
    await expect(chunk.loadInPage(() => Promise.reject(failed))).rejects.toBe(
      failed,
    );
    expect(chunk.handlePreloadError(failed, null, { notify })).toBe('blocked');
    expect(notify).toHaveBeenCalledTimes(1);
  });

  it('leaves other errors alone', () => {
    const notify = vi.fn();
    expect(
      chunk.handlePreloadError(new Error('boom'), '/a', { notify }),
    ).toBeNull();
    expect(notify).not.toHaveBeenCalled();
  });
});
