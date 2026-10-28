<template>
  <div
    class="vlan-grid"
    role="group"
    :aria-label="label"
    :aria-describedby="`${uid}-summary`">
    <p :id="`${uid}-summary`" class="vlan-grid-summary">{{ summary }}</p>
    <div class="vlan-grid-body">
      <ol class="vlan-grid-axis" aria-hidden="true">
        <li v-for="row in axis" :key="row">{{ row }}</li>
      </ol>
      <div
        class="vlan-grid-canvas"
        :style="{ width: `${size}px`, height: `${size}px` }"
        @click="onClick"
        @mousemove="onHover"
        @mouseleave="hovered = null">
        <canvas
          ref="canvas"
          :width="size * scale"
          :height="size * scale"
          :style="{ width: `${size}px`, height: `${size}px` }"
          aria-hidden="true"></canvas>
        <button
          type="button"
          class="vlan-grid-cursor"
          :style="cursorStyle"
          :aria-label="cellLabel(active)"
          :aria-describedby="`${uid}-help`"
          data-testid="vlan-grid-cursor"
          @keydown="onKey"
          @click.stop="$emit('open', active)"></button>
      </div>
    </div>
    <p :id="`${uid}-help`" class="is-sr-only">
      Use the arrow keys to move between IDs, Page Up and Page Down to move 8
      rows, and Home and End to go to the start or end of a row. Press Enter to
      show the details of an ID.
    </p>
    <p class="vlan-grid-status">{{ statusText }}</p>
    <ul class="vlan-grid-legend" aria-label="Legend">
      <li v-for="(info, state) in legend" :key="info.label">
        <i :style="{ background: info.color }" aria-hidden="true"></i
        >{{ info.label
        }}<span class="vlan-grid-count"> ({{ counts[state] }})</span>
      </li>
      <li v-for="o in outlines" :key="o.label">
        <i
          class="is-outline"
          :style="{ borderColor: o.color }"
          aria-hidden="true"></i
        >{{ o.label }}
      </li>
    </ul>
  </div>
</template>

<script>
  import {
    GRID_SIZE,
    ID_SPACE,
    MAX_VLAN,
    MIN_VLAN,
    STATE,
    STATE_INFO,
    cellText,
  } from '@/utils/vlans.js';

  // each cell is CELL px with a GAP px gap
  const CELL = 8;
  const GAP = 1;
  const PITCH = CELL + GAP;
  const ROWS_PER_PAGE = 8;

  let grids = 0;

  // canvas pixels per CSS pixel, rounded up so the cells stay sharp
  const pixelScale = () =>
    Math.min(Math.max(Math.ceil(window.devicePixelRatio || 1), 1), 3);

  // The ID space view: every ID from 0 to 4095 as a cell of a 64 by 64 grid,
  // drawn on a canvas. One button moves over the cells, so the keyboard
  // reaches each ID with the arrow keys and Enter opens its details. The
  // button's name is the cell, so a screen reader says each move once.
  export default {
    name: 'VlanGrid',
    props: {
      // the page data (normalizeVlans) and its idStates
      data: { type: Object, required: true },
      states: { type: Uint8Array, required: true },
      // the ID the keyboard is on (v-model:active)
      active: { type: Number, default: MIN_VLAN },
      // sets of IDs to outline, each { label, color, ids: Set }
      outlines: { type: Array, default: () => [] },
      // the IDs the filters show, or null for all; the others are dimmed
      shown: { type: Set, default: null },
    },
    // open(id): show an ID's details
    emits: ['open', 'update:active'],
    data() {
      return {
        uid: `vlan-grid-${++grids}`,
        hovered: null,
        scale: pixelScale(),
      };
    },
    computed: {
      size() {
        return GRID_SIZE * PITCH - GAP;
      },
      axis() {
        return Array.from(
          { length: GRID_SIZE / ROWS_PER_PAGE },
          (_, i) => i * GRID_SIZE * ROWS_PER_PAGE,
        );
      },
      label() {
        return 'VLAN ID space: IDs 0 to 4095 in 64 rows of 64';
      },
      legend() {
        return STATE_INFO;
      },
      counts() {
        const counts = STATE_INFO.map(() => 0);
        for (const state of this.states) counts[state] += 1;
        return counts;
      },
      summary() {
        const c = this.counts;
        return (
          `${c[STATE.FREE] + c[STATE.IN_RANGE]} free, ` +
          `${c[STATE.ALLOCATED]} allocated, ${c[STATE.PINNED]} pinned only, ` +
          `${c[STATE.BLACKLISTED]} blacklisted, ` +
          `${c[STATE.CONFLICT]} with a conflict or unowned.`
        );
      },
      statusText() {
        const id = this.hovered ?? this.active;
        return this.cellLabel(id);
      },
      cursorStyle() {
        const { x, y } = this.position(this.active);
        return {
          left: `${x - 2}px`,
          top: `${y - 2}px`,
          width: `${CELL + 4}px`,
          height: `${CELL + 4}px`,
        };
      },
    },
    watch: {
      states: 'draw',
      outlines: 'draw',
      shown: 'draw',
      // a new canvas size clears the canvas
      scale: { handler: 'draw', flush: 'post' },
    },
    mounted() {
      this.draw();
      this.followScale();
    },
    beforeUnmount() {
      this.scaleQuery?.removeEventListener?.('change', this.followScale);
    },
    methods: {
      // reads the pixel ratio, and reads it again when it changes, as when
      // the window moves to another screen or the page zooms
      followScale() {
        this.scaleQuery?.removeEventListener?.('change', this.followScale);
        this.scale = pixelScale();
        this.scaleQuery = window.matchMedia?.(
          `(resolution: ${window.devicePixelRatio || 1}dppx)`,
        );
        this.scaleQuery?.addEventListener?.('change', this.followScale);
      },
      position(id) {
        return {
          x: (id % GRID_SIZE) * PITCH,
          y: Math.floor(id / GRID_SIZE) * PITCH,
        };
      },
      cellLabel(id) {
        return cellText(this.data, this.states, id);
      },
      // the ID under a mouse event, or null between cells
      idAt(event) {
        const rect = this.$refs.canvas.getBoundingClientRect();
        const x = event.clientX - rect.left;
        const y = event.clientY - rect.top;
        const col = Math.floor(x / PITCH);
        const row = Math.floor(y / PITCH);
        if (col < 0 || row < 0 || col >= GRID_SIZE || row >= GRID_SIZE) {
          return null;
        }
        return row * GRID_SIZE + col;
      },
      onHover(event) {
        this.hovered = this.idAt(event);
      },
      onClick(event) {
        const id = this.idAt(event);
        if (id === null || id < MIN_VLAN || id > MAX_VLAN) return;
        this.$emit('update:active', id);
        this.$emit('open', id);
      },
      // moves the cursor by the key, staying on valid IDs
      onKey(event) {
        const id = this.active;
        const col = id % GRID_SIZE;
        const moves = {
          ArrowRight: id + 1,
          ArrowLeft: id - 1,
          ArrowDown: id + GRID_SIZE,
          ArrowUp: id - GRID_SIZE,
          PageDown: id + GRID_SIZE * ROWS_PER_PAGE,
          PageUp: id - GRID_SIZE * ROWS_PER_PAGE,
          Home: event.ctrlKey ? MIN_VLAN : id - col,
          End: event.ctrlKey ? MAX_VLAN : id - col + GRID_SIZE - 1,
        };
        // Alt and Meta keys belong to the browser, such as Alt+Left for Back
        if (event.altKey || event.metaKey || !(event.key in moves)) return;
        event.preventDefault();
        const next = Math.min(Math.max(moves[event.key], MIN_VLAN), MAX_VLAN);
        if (next === id) return;
        this.$emit('update:active', next);
      },
      draw() {
        const canvas = this.$refs.canvas;
        const ctx = canvas?.getContext?.('2d');
        if (!ctx) return;
        ctx.setTransform(this.scale, 0, 0, this.scale, 0, 0);
        ctx.clearRect(0, 0, this.size, this.size);
        for (let id = 0; id < ID_SPACE; id++) {
          const { x, y } = this.position(id);
          const valid = id >= MIN_VLAN && id <= MAX_VLAN;
          ctx.globalAlpha =
            !valid || !this.shown || this.shown.has(id) ? 1 : 0.3;
          ctx.fillStyle = STATE_INFO[this.states[id]].color;
          ctx.fillRect(x, y, CELL, CELL);
        }
        ctx.globalAlpha = 1;
        for (const outline of this.outlines) this.outline(ctx, outline);
      },
      // draws the outer edges of a set of cells
      outline(ctx, { ids, color }) {
        ctx.strokeStyle = color;
        ctx.lineWidth = 2;
        ctx.beginPath();
        const has = (id, same = true) => same && ids.has(id);
        for (const id of ids) {
          const { x, y } = this.position(id);
          const col = id % GRID_SIZE;
          const x0 = x - GAP / 2;
          const y0 = y - GAP / 2;
          const x1 = x + CELL + GAP / 2;
          const y1 = y + CELL + GAP / 2;
          if (!has(id - GRID_SIZE)) {
            ctx.moveTo(x0, y0);
            ctx.lineTo(x1, y0);
          }
          if (!has(id + GRID_SIZE)) {
            ctx.moveTo(x0, y1);
            ctx.lineTo(x1, y1);
          }
          if (!has(id - 1, col > 0)) {
            ctx.moveTo(x0, y0);
            ctx.lineTo(x0, y1);
          }
          if (!has(id + 1, col < GRID_SIZE - 1)) {
            ctx.moveTo(x1, y0);
            ctx.lineTo(x1, y1);
          }
        }
        ctx.stroke();
      },
    },
  };
</script>

<style scoped>
  .vlan-grid {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .content p.vlan-grid-summary,
  .content p.vlan-grid-status {
    margin: 0;
    font-size: 0.875rem;
  }

  .content p.vlan-grid-status {
    min-height: 2.6em;
    font-variant-numeric: tabular-nums;
  }

  .vlan-grid-body {
    display: flex;
    gap: 0.4rem;
    overflow-x: auto;
  }

  /* one label for each 8 rows of 64 IDs */
  .content ol.vlan-grid-axis {
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: 0.7rem;
    color: #e6e6e6;
    text-align: right;
  }

  .vlan-grid-axis li {
    height: 72px;
    margin: 0;
    line-height: 1;
  }

  .vlan-grid-canvas {
    position: relative;
    flex: none;
    background-color: #2a2a2a;
    cursor: pointer;
  }

  .vlan-grid-canvas canvas {
    display: block;
  }

  /* it is larger than a cell, so clicks go through it to the cell below */
  .vlan-grid-cursor {
    position: absolute;
    pointer-events: none;
    padding: 0;
    border: 2px solid white;
    border-radius: 2px;
    background: transparent;
    box-shadow: 0 0 0 1px black;
    cursor: pointer;
  }

  .vlan-grid-cursor:focus-visible {
    outline: 2px solid #62c0d7;
    outline-offset: 1px;
  }

  .content ul.vlan-grid-legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1rem;
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: 0.8rem;
  }

  .vlan-grid-legend li {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    margin: 0;
  }

  .vlan-grid-legend i {
    display: inline-block;
    width: 0.8rem;
    height: 0.8rem;
    border: 1px solid #b5b5b5;
  }

  .vlan-grid-legend i.is-outline {
    background: transparent;
    border-width: 2px;
  }

  .vlan-grid-count {
    color: #e6e6e6;
  }

  /* one column; the global ul rule makes two */
  ul.vlan-grid-legend {
    columns: auto;
  }
</style>
