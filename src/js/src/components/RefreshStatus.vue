<!--
Shows whether the current page's data is loading or how old it is, measured
from when the server read it, whether it may be out of date and why, with a
button to reload it. Pages opt in with createPageLoader.
-->
<template>
  <div v-if="pageStatus.refresh" class="refresh-status" aria-live="polite">
    <span
      class="refresh-label"
      :class="{ 'refresh-stale-label': stale }"
      :title="stale ? staleText : null"
      >{{ label
      }}<span v-if="stale" class="is-sr-only">: {{ staleText }}</span></span
    >
    <b-tooltip
      label="Refresh this page's data"
      type="is-light"
      position="is-bottom">
      <button
        class="button is-small is-dark refresh-button"
        :class="`refresh-${state}`"
        aria-label="Refresh this page's data"
        :disabled="pageStatus.loading"
        @click="pageStatus.refresh()">
        <b-icon
          icon="sync-alt"
          size="is-small"
          :custom-class="pageStatus.loading ? 'fa-spin' : ''"></b-icon>
      </button>
    </b-tooltip>
  </div>
</template>

<script>
  import { currentStaleReason, pageStatus } from '@/utils/pageLoader.js';
  import { resourceFor } from '@/utils/freshness.js';

  const MINUTE = 60 * 1000;

  // how old data read at `at` is, as "just now", "3 min" or null past an hour
  const age = (now, at) => {
    const seconds = Math.max(0, Math.round((now - at) / 1000));
    if (seconds < 60) return 'just now';
    const minutes = Math.round(seconds / 60);
    return minutes < 60 ? `${minutes} min` : null;
  };

  // a duration as the stale tooltip names it
  const span = (ms) =>
    ms < 2 * MINUTE
      ? `${Math.round(ms / 1000)} s`
      : `${Math.round(ms / MINUTE)} min`;

  export default {
    data() {
      return { pageStatus, now: Date.now() };
    },
    created() {
      // keeps "updated … ago" and the stale state current
      this.timer = setInterval(() => (this.now = Date.now()), 15000);
    },
    beforeUnmount() {
      clearInterval(this.timer);
    },
    computed: {
      // when the data on screen was read, by the server's account if it said
      shownAt() {
        return this.pageStatus.observedAt ?? this.pageStatus.updatedAt;
      },
      // why the data on screen may be out of date, or null
      reason() {
        if (!this.shownAt || this.pageStatus.failed) return null;
        return currentStaleReason(this.now);
      },
      stale() {
        return this.reason !== null && !this.pageStatus.loading;
      },
      staleText() {
        switch (this.reason) {
          case 'server':
            return 'minimega was busy; showing earlier data';
          case 'invalidated':
            return 'changed elsewhere since it loaded';
          case 'disconnected':
            return 'live updates disconnected';
          case 'age': {
            const limit = resourceFor(this.pageStatus.resource)?.staleAfterMs;
            return limit ? `older than ${span(limit)}` : 'older than expected';
          }
          default:
            return '';
        }
      },
      // colors the icon: yellow while loading, green after a successful load,
      // orange when the data may be out of date, red after a failed load
      state() {
        if (this.pageStatus.loading) return 'loading';
        if (this.pageStatus.failed) return 'failed';
        if (!this.pageStatus.updatedAt) return 'idle';
        return this.stale ? 'stale' : 'ok';
      },
      label() {
        if (this.pageStatus.loading) {
          if (!this.shownAt) return 'Loading…';
          const old = age(this.now, this.shownAt);
          return old && old !== 'just now'
            ? `Refreshing… (data ${old} old)`
            : 'Refreshing…';
        }
        if (this.pageStatus.failed) return 'Refresh failed';
        if (!this.shownAt) return '';

        const old = age(this.now, this.shownAt);
        let updated;
        if (old === 'just now') updated = 'Updated just now';
        else if (old) updated = `Updated ${old} ago`;
        else updated = `Updated ${new Date(this.shownAt).toLocaleTimeString()}`;

        return this.stale ? `${updated} · may be out of date` : updated;
      },
    },
    watch: {
      'pageStatus.updatedAt'() {
        this.now = Date.now();
      },
    },
  };
</script>

<style scoped>
  .refresh-status {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .refresh-button[disabled] {
    opacity: 1;
  }

  .refresh-loading {
    color: #ffe08a;
  }

  .refresh-ok {
    color: #48c78e;
  }

  .refresh-failed {
    color: #f14668;
  }

  /* 5:1 on the navbar (AA), and the label says it in words too */
  .refresh-stale,
  .refresh-stale-label {
    color: #ffd3a8;
  }

  .refresh-label {
    font-size: 0.85rem;
    opacity: 0.8;
    white-space: nowrap;
  }

  .refresh-stale-label {
    opacity: 1;
  }
</style>
