<!--
The button that reloads the current page's data. Its color says how old the
data is, measured from when the server read it, and its tooltip says when it
was updated and whether it may be out of date. Pages opt in with
createPageLoader.
-->
<template>
  <div v-if="pageStatus.refresh" class="refresh-status">
    <!-- announces only a failed reload or data that may be out of date, not
         each poll's loading and age changes -->
    <span class="refresh-alert is-sr-only" role="status" aria-live="polite">{{
      alert
    }}</span>
    <!-- left of the button: a long tooltip below it would pass the window edge -->
    <b-tooltip :label="tip" type="is-light" position="is-left">
      <button
        type="button"
        class="button nav-icon-button refresh-button"
        :class="`refresh-${tone}`"
        :aria-label="name"
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
  // For data with no age limit: fresh for a minute, aging up to five.
  const FRESH_MS = MINUTE;
  const AGING_MS = 5 * MINUTE;

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
      // what a screen reader is told: only a problem with the data shown
      return { pageStatus, now: Date.now(), alert: '' };
    },
    created() {
      // keeps the age, its color and the stale state current
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
            return 'minimega was busy. Showing earlier data';
          case 'invalidated':
            return 'changed elsewhere since it loaded';
          case 'shutdown':
            return 'phēnix server shutting down or restarting';
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
      // The icon's color: fresh, aging, stale, failed or idle. A reload keeps
      // the color the data had.
      tone() {
        if (this.pageStatus.failed) return 'failed';
        if (!this.shownAt) return 'idle';
        if (this.reason) return 'stale';
        const old = this.now - this.shownAt;
        const limit = resourceFor(this.pageStatus.resource)?.staleAfterMs;
        if (limit) return old < limit / 2 ? 'fresh' : 'aging';
        if (old < FRESH_MS) return 'fresh';
        return old < AGING_MS ? 'aging' : 'stale';
      },
      // the problem with the data shown, '' for none, or null while a load
      // runs and the outcome is not yet known
      problem() {
        if (this.pageStatus.loading) return null;
        if (this.pageStatus.failed) return 'Refresh failed';
        return this.stale ? `Data may be out of date: ${this.staleText}` : '';
      },
      // the tooltip: what the load is doing, or when the data was updated
      tip() {
        if (this.pageStatus.loading) return 'Refreshing…';
        if (this.pageStatus.failed) return 'Refresh failed';
        if (!this.shownAt) return "Refresh this page's data";

        const old = age(this.now, this.shownAt);
        let updated;
        if (old === 'just now') updated = 'Updated just now';
        else if (old) updated = `Updated ${old} ago`;
        else updated = `Updated ${new Date(this.shownAt).toLocaleTimeString()}`;

        return this.stale
          ? `${updated} · may be out of date: ${this.staleText}`
          : updated;
      },
      // the button's name: what it does, then the tooltip's state
      name() {
        const action = "Refresh this page's data";
        if (this.tip === action) return action;
        return `${action}, ${this.tip[0].toLowerCase()}${this.tip.slice(1)}`;
      },
    },
    watch: {
      'pageStatus.updatedAt'() {
        this.now = Date.now();
      },
      // keeps the announcement through a reload, so a problem that outlasts
      // it is not cleared and announced again on every poll
      problem: {
        handler(value) {
          if (value !== null) this.alert = value;
        },
        immediate: true,
      },
    },
  };
</script>

<style scoped>
  .refresh-status {
    display: flex;
    align-items: center;
  }

  .refresh-button[disabled] {
    opacity: 1;
  }

  /* outranks the outline style the header gives its icon buttons */
  .navbar .refresh-status .refresh-button.refresh-fresh {
    color: #48c78e;
  }

  .navbar .refresh-status .refresh-button.refresh-aging {
    color: #ffe08a;
  }

  .navbar .refresh-status .refresh-button.refresh-stale {
    color: #f29f63;
  }

  .navbar .refresh-status .refresh-button.refresh-failed {
    color: #f14668;
  }
</style>
