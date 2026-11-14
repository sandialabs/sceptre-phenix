<!-- The controls of the live tiles of this browser tab: the Live / Snapshots
     switch, the "Live tile limit" option and the live count, as in
     "12 of 14 live · 2 off screen". The browser keeps the limit. -->
<script setup>
  import { computed } from 'vue';

  import { LIVE_TILE_LIMITS, saveLiveTileLimit } from '@/utils/prefs.js';
  import { sharedLiveTilePool } from './liveTilePool.js';

  const pool = sharedLiveTilePool();

  function setLimit(value) {
    const limit = Number(value);
    pool.setLimit(limit);
    saveLiveTileLimit(limit);
  }

  const stats = computed(() => {
    const { live, available, offScreen } = pool.stats.value;
    const count = `${live} of ${available} live`;
    return offScreen ? `${count} · ${offScreen} off screen` : count;
  });
</script>

<template>
  <div class="vnc-tile-toolbar">
    <div
      class="buttons has-addons vnc-tile-mode"
      role="group"
      aria-label="Tiles">
      <button
        type="button"
        :class="['button', 'is-small', { 'is-info': pool.liveMode.value }]"
        :aria-pressed="String(pool.liveMode.value)"
        @click="pool.setLiveMode(true)">
        <span class="vnc-live-dot" aria-hidden="true">●</span> Live
      </button>
      <button
        type="button"
        :class="['button', 'is-small', { 'is-info': !pool.liveMode.value }]"
        :aria-pressed="String(!pool.liveMode.value)"
        @click="pool.setLiveMode(false)">
        Snapshots
      </button>
    </div>
    <label class="vnc-tile-limit">
      <span>Live tile limit</span>
      <span class="select is-small">
        <select
          :value="pool.limit.value"
          @change="(event) => setLimit(event.target.value)">
          <option v-for="limit in LIVE_TILE_LIMITS" :key="limit" :value="limit">
            {{ limit }}
          </option>
        </select>
      </span>
    </label>
    <span class="vnc-tile-stats">{{ stats }}</span>
  </div>
</template>

<style scoped>
  .vnc-tile-toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 1rem;
  }

  .vnc-tile-toolbar .buttons {
    margin-bottom: 0;
  }

  .vnc-tile-toolbar .buttons .button {
    margin-bottom: 0;
  }

  .vnc-tile-limit {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    white-space: nowrap;
  }

  .vnc-live-dot {
    margin-right: 0.25rem;
  }

  .vnc-tile-stats {
    white-space: nowrap;
  }
</style>
