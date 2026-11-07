// The live VNC tiles of one browser tab. The pool limits the live
// connections, keeps a tile live only while it is on screen, staggers new
// connections, and keeps one connection for each VM in the tab.
import {
  getCurrentInstance,
  onBeforeUnmount,
  readonly,
  ref,
  shallowRef,
  toValue,
  watch,
} from 'vue';

import { loadLiveTileLimit } from '@/utils/prefs.js';

/** The part of a tile that must be visible for it to be live. */
export const VISIBLE_RATIO = 0.25;
/** How long a live tile stays live off screen or in a hidden tab. */
export const OFF_SCREEN_MS = 10_000;
/** New connections start this many at a time… */
export const STAGGER_BATCH = 4;
/** …and this many milliseconds apart. */
export const STAGGER_MS = 250;

/**
 * @typedef {object} LiveTile
 * @property {string} key the VM, as `exp/vm`
 * @property {Readonly<import('vue').Ref<boolean>>} live whether the tile
 *   may hold a live connection now; else it shows a screenshot
 * @property {(el: Element|null) => void} observe watches the tile element
 *   for visibility (null stops)
 * @property {(available: boolean) => void} setAvailable false for a VM that
 *   is not running (a paused VM is available) or a user without `vms/vnc` get
 * @property {() => void} unregister
 */

/**
 * @typedef {object} LiveTilePool
 * @property {Readonly<import('vue').Ref<number>>} limit
 * @property {Readonly<import('vue').Ref<boolean>>} liveMode false in
 *   Snapshots mode
 * @property {(limit: number) => void} setLimit
 * @property {(on: boolean) => void} setLiveMode
 * @property {(key: string, options?: { available?: boolean }) => LiveTile} register
 * @property {(key: string) => () => void} claim marks the VM as open in the
 *   details modal or a console pane of this tab, so its tile is not live;
 *   returns the release function
 * @property {Readonly<import('vue').Ref<{ live: number, available: number,
 *   offScreen: number }>>} stats for "12 of 14 live · 2 off screen"
 * @property {() => void} dispose
 */

/**
 * Makes a pool of live tiles.
 *
 * @param {{ limit?: number, liveMode?: boolean }} [options]
 * @returns {LiveTilePool}
 */
export function createLiveTilePool({ limit = 16, liveMode = true } = {}) {
  const limitRef = ref(limit);
  const liveModeRef = ref(liveMode);
  const stats = shallowRef({ live: 0, available: 0, offScreen: 0 });
  const tiles = new Set();
  const byElement = new Map();
  const claims = new Map();
  const queue = [];
  let order = 0;
  let cooldown = null;
  let started = 0;
  let graceTimer = null;

  const Observer = globalThis.IntersectionObserver;
  const observer = Observer
    ? new Observer(
        (entries) => {
          for (const entry of entries) {
            const tile = byElement.get(entry.target);
            if (tile) tile.ratio = entry.intersectionRatio;
          }
          update();
        },
        { threshold: [0, VISIBLE_RATIO] },
      )
    : null;

  const tabHidden = () => globalThis.document?.visibilityState === 'hidden';
  const onScreen = (tile) => tile.ratio >= VISIBLE_RATIO && !tabHidden();

  function setLive(tile, live) {
    if (tile.live.value !== live) tile.live.value = live;
  }

  function refreshStats() {
    let live = 0;
    let available = 0;
    let offScreen = 0;
    for (const tile of tiles) {
      if (tile.live.value) live += 1;
      if (!tile.available) continue;
      available += 1;
      if (!onScreen(tile)) offScreen += 1;
    }
    const last = stats.value;
    if (
      last.live !== live ||
      last.available !== available ||
      last.offScreen !== offScreen
    ) {
      stats.value = { live, available, offScreen };
    }
  }

  // Starts queued tiles: at most STAGGER_BATCH in each STAGGER_MS window.
  function drain() {
    while (queue.length > 0 && started < STAGGER_BATCH) {
      const tile = queue.shift();
      tile.queued = false;
      setLive(tile, true);
      started += 1;
      cooldown ??= setTimeout(() => {
        cooldown = null;
        started = 0;
        drain();
        refreshStats();
      }, STAGGER_MS);
    }
  }

  function dequeue(tile) {
    const i = queue.indexOf(tile);
    if (i >= 0) queue.splice(i, 1);
    tile.queued = false;
  }

  // Works out which tiles are live now, and when to look again.
  function update() {
    const now = Date.now();
    clearTimeout(graceTimer);
    graceTimer = null;
    let nextGraceEnd = Infinity;

    const eligible = [];
    for (const tile of tiles) {
      const seen = onScreen(tile);
      if (seen) tile.offSince = null;
      else tile.offSince ??= now;
      const inGrace =
        tile.live.value && !seen && now - tile.offSince < OFF_SCREEN_MS;
      if (inGrace) nextGraceEnd = Math.min(nextGraceEnd, tile.offSince);
      if (
        liveModeRef.value &&
        tile.available &&
        !claims.has(tile.key) &&
        (seen || inGrace)
      ) {
        eligible.push(tile);
      }
    }

    // tiles that have a slot keep it before others get one
    const slotted = (tile) => tile.live.value || tile.queued;
    eligible.sort((a, b) => slotted(b) - slotted(a) || a.order - b.order);
    const wanted = new Set(eligible.slice(0, limitRef.value));

    for (const tile of tiles) {
      if (wanted.has(tile)) {
        if (!slotted(tile)) {
          tile.queued = true;
          queue.push(tile);
        }
      } else {
        dequeue(tile);
        setLive(tile, false);
      }
    }
    queue.sort((a, b) => a.order - b.order);
    drain();

    if (nextGraceEnd !== Infinity) {
      graceTimer = setTimeout(update, nextGraceEnd + OFF_SCREEN_MS - now);
    }
    refreshStats();
  }

  function onVisibility() {
    update();
  }
  globalThis.document?.addEventListener('visibilitychange', onVisibility);

  function register(key, { available = true } = {}) {
    const tile = {
      key,
      order: (order += 1),
      live: ref(false),
      available,
      // without an IntersectionObserver every tile counts as on screen
      ratio: observer ? 0 : 1,
      offSince: null,
      queued: false,
      el: null,
    };
    tiles.add(tile);

    const handle = {
      key,
      live: readonly(tile.live),
      observe(el) {
        if (tile.el === el) return;
        if (tile.el) {
          observer?.unobserve(tile.el);
          byElement.delete(tile.el);
        }
        tile.el = el;
        if (observer) tile.ratio = 0;
        if (el) {
          byElement.set(el, tile);
          observer?.observe(el);
        }
        update();
      },
      setAvailable(value) {
        if (tile.available === value) return;
        tile.available = value;
        update();
      },
      unregister() {
        if (!tiles.delete(tile)) return;
        handle.observe(null);
        dequeue(tile);
        setLive(tile, false);
        update();
      },
    };
    update();
    return handle;
  }

  function claim(key) {
    claims.set(key, (claims.get(key) ?? 0) + 1);
    update();
    let released = false;
    return () => {
      if (released) return;
      released = true;
      const count = claims.get(key) - 1;
      if (count > 0) claims.set(key, count);
      else claims.delete(key);
      update();
    };
  }

  return {
    limit: readonly(limitRef),
    liveMode: readonly(liveModeRef),
    setLimit(value) {
      limitRef.value = value;
      update();
    },
    setLiveMode(on) {
      liveModeRef.value = Boolean(on);
      update();
    },
    register,
    claim,
    stats: readonly(stats),
    dispose() {
      clearTimeout(cooldown);
      clearTimeout(graceTimer);
      cooldown = null;
      started = 0;
      observer?.disconnect();
      globalThis.document?.removeEventListener(
        'visibilitychange',
        onVisibility,
      );
      for (const tile of tiles) setLive(tile, false);
      tiles.clear();
      queue.length = 0;
      refreshStats();
    },
  };
}

let shared = null;

/**
 * The pool of this browser tab, which the VNC tab, the VM Tiles page, the
 * details modal and the console panes share. It starts at the live tile
 * limit that the browser keeps (utils/prefs.js).
 *
 * @returns {LiveTilePool}
 */
export function sharedLiveTilePool() {
  if (!shared) {
    const pool = createLiveTilePool({ limit: loadLiveTileLimit() });
    const dispose = pool.dispose;
    shared = Object.assign(pool, {
      dispose() {
        dispose();
        shared = null;
      },
    });
  }
  return shared;
}

/**
 * Registers a tile while its component is mounted. Bind `el` as the
 * function ref of the tile element. The key does not change, so key the
 * component by its VM. Outside a component, call `unregister` when done.
 *
 * @param {string} key the VM, as `exp/vm`
 * @param {object} [options]
 * @param {import('vue').MaybeRefOrGetter<boolean>} [options.available] see
 *   LiveTile.setAvailable
 * @param {LiveTilePool} [options.pool] sharedLiveTilePool() if not given
 * @returns {{ live: Readonly<import('vue').Ref<boolean>>,
 *   el: (el: Element|null) => void, unregister: () => void }}
 */
export function useLiveTile(key, { available = true, pool } = {}) {
  const tile = (pool ?? sharedLiveTilePool()).register(key, {
    available: Boolean(toValue(available)),
  });
  const stop = watch(
    () => Boolean(toValue(available)),
    (value) => tile.setAvailable(value),
  );
  const unregister = () => {
    stop();
    tile.unregister();
  };
  if (getCurrentInstance()) onBeforeUnmount(unregister);
  return { live: tile.live, el: (el) => tile.observe(el), unregister };
}
