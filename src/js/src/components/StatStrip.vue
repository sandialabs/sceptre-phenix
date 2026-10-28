<template>
  <div class="stat-strip" role="group" :aria-label="label">
    <dl>
      <div
        v-for="stat in stats"
        :key="stat.key"
        :class="[
          'stat',
          `stat-${stat.key}`,
          {
            [`is-${stat.tone}`]: stat.tone,
            'is-clickable': stat.filter,
            'is-active': stat.filter && stat.active,
          },
        ]">
        <dt>{{ stat.label }}</dt>
        <dd>
          <!-- the button covers the whole tile (see .stat-button::after) -->
          <button
            v-if="stat.filter"
            type="button"
            class="stat-button"
            :aria-pressed="stat.active ? 'true' : 'false'"
            :aria-label="`${stat.label} ${stat.value}${stat.detail ? ` ${stat.detail}` : ''}`"
            :title="stat.filter"
            @click="$emit('select', stat.key)">
            <span class="stat-value">{{ stat.value }}</span
            ><span v-if="stat.detail" class="stat-detail">{{
              ` ${stat.detail}`
            }}</span>
          </button>
          <template v-else>
            <span class="stat-value">{{ stat.value }}</span
            ><span v-if="stat.detail" class="stat-detail">{{
              ` ${stat.detail}`
            }}</span>
          </template>
        </dd>
      </div>
    </dl>
  </div>
</template>

<script>
  // A page's counts in one strip, as tall as the search box beside it: each
  // stat's label in small capitals, then its number and an optional detail.
  // A stat with a filter (what a click does) is a toggle button that emits
  // select with the stat's key; active marks it pressed.
  export default {
    name: 'StatStrip',
    props: {
      // the group's accessible name
      label: { type: String, required: true },
      // [{ key, label, value, detail?, tone?: 'success', filter?, active? }]
      stats: { type: Array, default: () => [] },
    },
    emits: ['select'],
  };
</script>

<style scoped>
  /* 2.5em: the height of a normal Bulma input */
  .stat-strip dl {
    display: inline-flex;
    align-items: stretch;
    height: 2.5em;
    margin: 0;
    border: 1px solid #686868;
    border-left: 3px solid #da622d;
    border-radius: 4px;
    background-color: #484848;
    white-space: nowrap;
  }

  .stat {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0 0.875rem;
  }

  .stat + .stat {
    border-left: 1px solid #686868;
  }

  /* #e6e6e6 on #484848 is 7.3:1 */
  .stat dt {
    color: #e6e6e6;
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .stat dd {
    margin: 0;
    font-variant-numeric: tabular-nums;
  }

  .stat-value {
    color: white;
    font-size: 1.125rem;
    font-weight: 700;
  }

  .stat-detail {
    color: #e6e6e6;
    font-size: 0.875rem;
  }

  .stat.is-clickable {
    position: relative;
  }

  .stat-button {
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    cursor: pointer;
  }

  /* the whole tile takes the click, the hover and the focus ring */
  .stat-button::after {
    content: '';
    position: absolute;
    inset: 0;
  }

  .stat.is-clickable:hover {
    background-color: #3a3a3a;
  }

  .stat-button:focus-visible {
    outline: none;
  }

  .stat-button:focus-visible::after {
    outline: 2px solid #ffd166;
    outline-offset: -2px;
  }

  /* the filter in use: #e6e6e6 on #2f2f2f is 12:1 */
  .stat.is-active {
    background-color: #2f2f2f;
    box-shadow: inset 0 -3px 0 #da622d;
  }

  /* an accent only, as the label says the same: 7.4:1 on #484848 */
  .stat.is-success .stat-value {
    color: #caefde;
  }
</style>
