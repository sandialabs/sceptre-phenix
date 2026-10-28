<!--
  The State of Health graph's layout picker: a segmented strip of layout
  buttons with a hint for the chosen one. Self-contained so the page can swap
  in another presentation (a select, a dropdown) without other changes.
-->
<template>
  <div class="soh-layout-select">
    <span class="soh-layout-caption" id="soh-layout-caption">Layout</span>
    <div
      class="soh-layout-seg"
      role="radiogroup"
      aria-labelledby="soh-layout-caption">
      <b-tooltip
        v-for="opt in options"
        :key="opt.id"
        :label="opt.reason || `${opt.label}: ${opt.hint}`"
        :type="opt.reason ? 'is-warning' : 'is-dark'"
        position="is-bottom"
        :multilined="Boolean(opt.reason)"
        :delay="300">
        <b-radio-button
          :model-value="modelValue"
          :native-value="opt.id"
          :disabled="opt.disabled || disabled"
          type=""
          size="is-small"
          :aria-label="opt.label"
          @update:model-value="$emit('update:modelValue', $event)">
          <svg
            class="soh-layout-glyph"
            width="17"
            height="17"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="1.3"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true">
            <component
              :is="el[0]"
              v-for="(el, i) in glyphs[opt.id] ?? []"
              :key="i"
              v-bind="el[1]" />
          </svg>
          <span>{{ opt.short }}</span>
        </b-radio-button>
      </b-tooltip>
    </div>
    <span class="soh-layout-hint">
      <span v-if="busy" class="soh-layout-spinner" aria-hidden="true" />
      {{ busy ? 'Computing layout…' : hint }}
    </span>
  </div>
</template>

<script>
  // small line drawings of each layout's shape; filled marks are nodes
  const dot = (cx, cy, r) => ['circle', { cx, cy, r, class: 'f' }];
  const line = (x1, y1, x2, y2) => ['line', { x1, y1, x2, y2 }];
  const box = (x, y, width, height, filled) => [
    'rect',
    { x, y, width, height, rx: filled ? 1 : 2, class: filled ? 'f' : null },
  ];
  const poly = (points) => ['polyline', { points }];

  const GLYPHS = {
    force: [
      line(10, 10, 4, 5),
      line(10, 10, 16, 6),
      line(10, 10, 6, 16),
      line(10, 10, 15, 15),
      line(16, 6, 18, 12),
      dot(10, 10, 2.2),
      dot(4, 5, 1.7),
      dot(16, 6, 1.7),
      dot(6, 16, 1.7),
      dot(15, 15, 1.7),
      dot(18, 12, 1.3),
    ],
    radial: [
      ['circle', { cx: 10, cy: 10, r: 7, 'stroke-dasharray': '2 2' }],
      line(10, 10, 10, 3),
      line(10, 10, 16.1, 13.5),
      line(10, 10, 3.9, 13.5),
      dot(10, 10, 2.2),
      dot(10, 3, 1.7),
      dot(16.1, 13.5, 1.7),
      dot(3.9, 13.5, 1.7),
    ],
    layered: [
      line(10, 3, 5, 10),
      line(10, 3, 15, 10),
      line(5, 10, 3, 17),
      line(5, 10, 8, 17),
      line(15, 10, 12, 17),
      line(15, 10, 17, 17),
      dot(10, 3, 1.8),
      dot(5, 10, 1.8),
      dot(15, 10, 1.8),
      dot(3, 17, 1.5),
      dot(8, 17, 1.5),
      dot(12, 17, 1.5),
      dot(17, 17, 1.5),
    ],
    groups: [
      box(1.5, 1.5, 8, 8),
      box(10.5, 10.5, 8, 8),
      line(8, 8, 12, 12),
      dot(4.5, 4.5, 1.3),
      dot(6.8, 6.6, 1.3),
      dot(13.5, 13.5, 1.3),
      dot(15.8, 15.6, 1.3),
      dot(15.5, 12.8, 1.1),
    ],
    elk: [
      box(7, 1.5, 6, 4, true),
      poly('10,5.5 10,8 4.5,8 4.5,11'),
      poly('10,8 15.5,8 15.5,11'),
      poly('4.5,14 4.5,16 10,16 10,15'),
      box(2, 11, 5, 3.5, true),
      box(13, 11, 5, 3.5, true),
      box(7.5, 15, 5, 3.5, true),
    ],
  };

  export default {
    name: 'SohLayoutSelect',
    props: {
      modelValue: { type: String, required: true },
      // [{id, label, short, hint, disabled, reason}] from layoutOptions()
      options: { type: Array, required: true },
      busy: { type: Boolean, default: false },
      disabled: { type: Boolean, default: false },
    },
    emits: ['update:modelValue'],
    computed: {
      glyphs: () => GLYPHS,
      hint() {
        return this.options.find((o) => o.id === this.modelValue)?.hint ?? '';
      },
    },
  };
</script>

<style scoped>
  .soh-layout-select {
    display: flex;
    align-items: center;
    gap: 0.5rem 0.75rem;
    flex-wrap: wrap;
  }

  .soh-layout-caption {
    font-size: 0.8rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: #cfcfcf;
  }

  .soh-layout-seg {
    display: inline-flex;
    border: 1px solid #828282;
    border-radius: 4px;
    /* no overflow: hidden, it would clip the tooltips; round the ends */
  }

  .soh-layout-seg > :first-child :deep(.b-radio.radio.button) {
    border-radius: 3px 0 0 3px;
  }

  .soh-layout-seg > :last-child :deep(.b-radio.radio.button) {
    border-radius: 0 3px 3px 0;
  }

  .soh-layout-seg :deep(.control) {
    margin: 0;
  }

  .soh-layout-seg :deep(.b-radio.radio.button) {
    border: 0;
    border-radius: 0;
    margin: 0;
    height: 2.1em;
    padding: 0 0.8em;
    gap: 0.45em;
    font-size: 0.92rem;
    background: #5a5a5a;
    color: whitesmoke;
  }

  .soh-layout-seg > :not(:first-child) :deep(.b-radio.radio.button) {
    box-shadow: inset 1px 0 0 #828282;
  }

  .soh-layout-seg :deep(.b-radio.radio.button:hover) {
    background: #686868;
  }

  .soh-layout-seg :deep(.b-radio.radio.button.is-selected) {
    background: whitesmoke;
    color: #2b2b2b;
    font-weight: 600;
  }

  .soh-layout-seg :deep(.b-radio.radio.button.is-disabled) {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .soh-layout-glyph {
    flex: none;
  }

  .soh-layout-glyph :deep(.f) {
    fill: currentColor;
    stroke: none;
  }

  .soh-layout-hint {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.82rem;
    color: #cfcfcf;
  }

  .soh-layout-spinner {
    width: 0.9em;
    height: 0.9em;
    border: 2px solid #cfcfcf;
    border-right-color: transparent;
    border-radius: 50%;
    animation: soh-spin 0.7s linear infinite;
  }

  @keyframes soh-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
