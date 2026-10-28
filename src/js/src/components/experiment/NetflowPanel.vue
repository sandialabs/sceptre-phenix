<template>
  <div class="netflow-panel">
    <div class="netflow-header">
      <p v-if="lines.length > 0" class="netflow-summary">
        {{ summary }}
      </p>
      <p v-else class="netflow-empty has-text-white">
        {{
          starting
            ? 'Starting netflow capture…'
            : 'No netflow captures yet. Start one with the netflow button above.'
        }}
      </p>
      <NetflowCaptures v-if="experiment" :experiment="experiment" />
    </div>
    <div
      v-if="lines.length > 0"
      ref="view"
      class="netflow-view"
      :style="{ maxHeight }">
      <table class="table is-narrow is-fullwidth netflow-table">
        <thead>
          <tr>
            <th>Time</th>
            <th>Source</th>
            <th>Destination</th>
            <th>Protocol</th>
            <th class="has-text-right">Packets</th>
            <th class="has-text-right">Bytes</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="line in rows"
            :key="line.id"
            :class="{ 'netflow-marker': line.marker }">
            <td>{{ line.time }}</td>
            <td v-if="line.marker" colspan="5">{{ line.marker }}</td>
            <template v-else>
              <td>{{ line.src }}</td>
              <td>{{ line.dst }}</td>
              <td>{{ line.proto }}</td>
              <td class="has-text-right">{{ line.packets }}</td>
              <td class="has-text-right">{{ line.bytes }}</td>
            </template>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script>
  import { roomInWindow } from '@/utils/viewportFit.js';
  import NetflowCaptures from './NetflowCaptures.vue';
  import {
    NETFLOW_SHOWN_LINES,
    matchNetflow,
    netflowSummary,
  } from './netflow.js';

  // The running experiment's netflow tab: the captured flows and markers,
  // newest first, those matching the search box alone. The page calls fit()
  // when it changes height.
  export default {
    name: 'NetflowPanel',
    components: { NetflowCaptures },
    props: {
      // the experiment, whose recorded captures the panel offers to
      // download; empty until the page has loaded it
      experiment: { type: String, default: '' },
      // captured flows and start/stop markers, oldest first (see netflow.js)
      lines: { type: Array, required: true },
      // whether a capture is starting, before it has any lines
      starting: { type: Boolean, default: false },
      // the search box's text, which lines must contain to be shown
      search: { type: String, default: '' },
    },
    expose: ['fit'],

    data() {
      return {
        // sized to the window so the page never scrolls; null until measured
        maxHeight: null,
      };
    },

    computed: {
      // every line matching the search, oldest first
      matches() {
        return matchNetflow(this.lines, this.search);
      },
      // the newest NETFLOW_SHOWN_LINES of those, newest first
      rows() {
        return this.matches.slice(-NETFLOW_SHOWN_LINES).reverse();
      },
      summary() {
        return netflowSummary(this.matches.length, this.lines.length);
      },
    },

    watch: {
      // the table appears with the first line
      'lines.length'(count, before) {
        if (before === 0 && count > 0) this.$nextTick(this.fit);
      },
    },

    mounted() {
      window.addEventListener('resize', this.fit);
    },

    beforeUnmount() {
      window.removeEventListener('resize', this.fit);
    },

    methods: {
      // Sizes the table to the room left in the window, so the table scrolls
      // instead of the page and the footer stays on screen.
      fit() {
        const room = roomInWindow(this.$refs.view);
        if (room == null) return;
        this.maxHeight = `${Math.max(Math.floor(room), 240)}px`;
      },
    },
  };
</script>

<style scoped>
  /* scrolls within the window, so the page below stays reachable */
  .netflow-view {
    max-height: calc(100vh - 18rem);
    min-height: 100px;
    overflow: auto;
  }

  .netflow-table td {
    font-family: monospace;
    white-space: nowrap;
  }

  /* the header and its row take the table's color, so the sticky header
     cells cover the rows scrolling under them */
  .netflow-table thead,
  .netflow-table thead tr,
  .netflow-table thead th {
    background-color: inherit;
  }

  .netflow-table thead th {
    position: sticky;
    top: 0;
    z-index: 1;
  }

  .netflow-marker td {
    font-weight: bold;
    font-style: italic;
  }

  /* the summary or empty text, and the Download captures button */
  .netflow-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 0.5rem;
  }

  .netflow-summary {
    opacity: 0.8;
  }
</style>
