// The live tile pool (T32): the limit, the visibility rules (25%, 30 s), the
// stagger (4 at a time, 250 ms), and one connection for each VM in a tab.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h, nextTick, ref } from 'vue';

import {
  OFF_SCREEN_MS,
  STAGGER_BATCH,
  STAGGER_MS,
  VISIBLE_RATIO,
  createLiveTilePool,
  sharedLiveTilePool,
  useLiveTile,
} from '@/vnc/liveTilePool.js';
import { writePref } from '@/utils/prefs.js';
import { mountComponent } from './helpers/mount.js';

// An IntersectionObserver that reports what the test says is on screen.
class FakeObserver {
  static all = [];
  constructor(callback, options) {
    this.callback = callback;
    this.options = options;
    this.targets = new Set();
    FakeObserver.all.push(this);
  }
  observe(el) {
    this.targets.add(el);
  }
  unobserve(el) {
    this.targets.delete(el);
  }
  disconnect() {
    this.targets.clear();
  }
}

// reports el as `ratio` visible to the observer that watches it
function show(el, ratio = 1) {
  for (const observer of FakeObserver.all) {
    if (observer.targets.has(el)) {
      observer.callback([
        { target: el, intersectionRatio: ratio, isIntersecting: ratio > 0 },
      ]);
    }
  }
}

let hidden = false;
function setTabHidden(value) {
  hidden = value;
  document.dispatchEvent(new Event('visibilitychange'));
}

let pool;

// n tiles of VMs vm0…vm(n-1), each with its element
function tiles(n, { onScreen = true } = {}) {
  return Array.from({ length: n }, (_, i) => {
    const el = document.createElement('div');
    const tile = pool.register(`demo/vm${i}`);
    tile.observe(el);
    if (onScreen) show(el);
    return Object.assign(tile, { el });
  });
}

const liveCount = (list) => list.filter((t) => t.live.value).length;

beforeEach(() => {
  vi.useFakeTimers();
  FakeObserver.all = [];
  vi.stubGlobal('IntersectionObserver', FakeObserver);
  hidden = false;
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() =>
    hidden ? 'hidden' : 'visible',
  );
});

afterEach(() => {
  pool?.dispose();
  pool = null;
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('the stagger', () => {
  it('starts new connections 4 at a time, 250 ms apart', async () => {
    pool = createLiveTilePool({ limit: 32 });
    const list = tiles(10);

    expect(STAGGER_BATCH).toBe(4);
    expect(STAGGER_MS).toBe(250);
    expect(liveCount(list)).toBe(4);
    await vi.advanceTimersByTimeAsync(249);
    expect(liveCount(list)).toBe(4);
    await vi.advanceTimersByTimeAsync(1);
    expect(liveCount(list)).toBe(8);
    await vi.advanceTimersByTimeAsync(250);
    expect(liveCount(list)).toBe(10);
    // in the order the tiles registered
    expect(list.map((t) => t.live.value)).toEqual(Array(10).fill(true));
  });

  it('waits out the gap after a batch for a tile that comes later', async () => {
    pool = createLiveTilePool();
    tiles(4);
    const [late] = tiles(1);
    expect(late.live.value).toBe(false);
    await vi.advanceTimersByTimeAsync(STAGGER_MS);
    expect(late.live.value).toBe(true);
  });
});

describe('the limit', () => {
  it('keeps at most the limit live, and the rest as snapshots', async () => {
    pool = createLiveTilePool({ limit: 4 });
    const list = tiles(6);
    await vi.advanceTimersByTimeAsync(1000);
    expect(list.map((t) => t.live.value)).toEqual([
      true,
      true,
      true,
      true,
      false,
      false,
    ]);
    expect(pool.stats.value).toEqual({ live: 4, available: 6, offScreen: 0 });
  });

  it('gives a freed slot to the next tile', async () => {
    pool = createLiveTilePool({ limit: 4 });
    const list = tiles(5);
    await vi.advanceTimersByTimeAsync(1000);
    list[1].unregister();
    await vi.advanceTimersByTimeAsync(STAGGER_MS);
    expect(list[4].live.value).toBe(true);
  });

  it('drops the last tiles at once when the limit goes down, and adds them when it goes up', async () => {
    pool = createLiveTilePool({ limit: 8 });
    const list = tiles(8);
    await vi.advanceTimersByTimeAsync(1000);
    pool.setLimit(4);
    expect(list.map((t) => t.live.value)).toEqual([
      ...Array(4).fill(true),
      ...Array(4).fill(false),
    ]);
    pool.setLimit(8);
    await vi.advanceTimersByTimeAsync(STAGGER_MS);
    expect(liveCount(list)).toBe(8);
  });

  it('turns every tile off in snapshots mode', async () => {
    pool = createLiveTilePool();
    const list = tiles(3);
    pool.setLiveMode(false);
    expect(liveCount(list)).toBe(0);
    pool.setLiveMode(true);
    await vi.advanceTimersByTimeAsync(STAGGER_MS);
    expect(liveCount(list)).toBe(3);
  });

  it('keeps a tile whose VM cannot be watched out of the count', async () => {
    pool = createLiveTilePool({ limit: 4 });
    const list = tiles(5);
    list[0].setAvailable(false);
    await vi.advanceTimersByTimeAsync(1000);
    expect(list.map((t) => t.live.value)).toEqual([
      false,
      true,
      true,
      true,
      true,
    ]);
    expect(pool.stats.value.available).toBe(4);
  });
});

describe('the visibility rules', () => {
  it('makes a tile live only while at least 25% of it is visible', () => {
    pool = createLiveTilePool();
    const [tile] = tiles(1, { onScreen: false });
    expect(VISIBLE_RATIO).toBe(0.25);
    expect(FakeObserver.all[0].options.threshold).toContain(VISIBLE_RATIO);

    show(tile.el, 0.24);
    expect(tile.live.value).toBe(false);
    show(tile.el, 0.25);
    expect(tile.live.value).toBe(true);
  });

  it('disconnects a tile 30 s after it leaves the screen', async () => {
    pool = createLiveTilePool();
    const [tile] = tiles(1);
    show(tile.el, 0);
    expect(OFF_SCREEN_MS).toBe(30_000);
    expect(pool.stats.value.offScreen).toBe(1);
    await vi.advanceTimersByTimeAsync(OFF_SCREEN_MS - 1);
    expect(tile.live.value).toBe(true);
    await vi.advanceTimersByTimeAsync(1);
    expect(tile.live.value).toBe(false);
  });

  it('keeps a tile that comes back within 30 s', async () => {
    pool = createLiveTilePool();
    const [tile] = tiles(1);
    show(tile.el, 0);
    await vi.advanceTimersByTimeAsync(25_000);
    show(tile.el, 1);
    await vi.advanceTimersByTimeAsync(OFF_SCREEN_MS);
    expect(tile.live.value).toBe(true);
  });

  it('does not start a tile that leaves the screen before its turn', async () => {
    pool = createLiveTilePool();
    const list = tiles(5);
    show(list[4].el, 0);
    await vi.advanceTimersByTimeAsync(STAGGER_MS);
    expect(list[4].live.value).toBe(false);
  });

  it('disconnects every tile 30 s after the browser tab is hidden', async () => {
    pool = createLiveTilePool();
    const list = tiles(2);
    setTabHidden(true);
    await vi.advanceTimersByTimeAsync(OFF_SCREEN_MS - 1);
    expect(liveCount(list)).toBe(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(liveCount(list)).toBe(0);
    setTabHidden(false);
    expect(liveCount(list)).toBe(2);
  });

  it('counts every tile as on screen without an IntersectionObserver', () => {
    vi.stubGlobal('IntersectionObserver', undefined);
    pool = createLiveTilePool();
    const tile = pool.register('demo/web');
    tile.observe(document.createElement('div'));
    expect(tile.live.value).toBe(true);
  });
});

describe('one connection for each VM in a tab', () => {
  it('disconnects a tile while the modal or a console pane of its VM is open', async () => {
    pool = createLiveTilePool();
    const [tile, other] = tiles(2);
    const releaseModal = pool.claim('demo/vm0');
    const releasePane = pool.claim('demo/vm0');
    expect(tile.live.value).toBe(false);
    expect(other.live.value).toBe(true);

    releaseModal();
    releaseModal();
    expect(tile.live.value).toBe(false);
    releasePane();
    await vi.advanceTimersByTimeAsync(STAGGER_MS);
    expect(tile.live.value).toBe(true);
  });

  it('keeps a paused tile live', () => {
    pool = createLiveTilePool();
    const [tile] = tiles(1);
    // a paused VM can still be watched: its tile stays available
    tile.setAvailable(true);
    expect(tile.live.value).toBe(true);
  });
});

describe('the tab pool and the tile composable', () => {
  it('starts the tab pool at the stored limit', async () => {
    writePref('phenix.vnc.liveTileLimit', '4');
    const shared = sharedLiveTilePool();
    expect(sharedLiveTilePool()).toBe(shared);
    expect(shared.limit.value).toBe(4);
    shared.dispose();
  });

  it('registers a tile while its component is mounted', async () => {
    pool = createLiveTilePool();
    const available = ref(true);
    let tile;
    const Tile = defineComponent({
      setup() {
        tile = useLiveTile('demo/web', { available, pool });
        return () => h('div', { ref: tile.el });
      },
    });
    const wrapper = await mountComponent(Tile);
    show(wrapper.element);
    expect(tile.live.value).toBe(true);

    available.value = false;
    await nextTick();
    expect(tile.live.value).toBe(false);

    wrapper.unmount();
    expect(pool.stats.value.available).toBe(0);
  });
});
