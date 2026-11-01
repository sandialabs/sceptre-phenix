<template>
  <div class="stat-strip" role="group" :aria-label="label">
    <dl>
      <div
        v-for="stat in stats"
        :key="stat.key"
        :class="[
          'stat',
          `stat-${stat.key}`,
          { [`is-${stat.tone}`]: stat.tone },
        ]">
        <dt>{{ stat.label }}</dt>
        <dd>
          <span class="stat-value">{{ stat.value }}</span
          ><span v-if="stat.detail" class="stat-detail">{{
            ` ${stat.detail}`
          }}</span>
        </dd>
      </div>
    </dl>
  </div>
</template>

<script>
  // A page's counts in one strip, as tall as the search box beside it: each
  // stat's label in small capitals, then its number and an optional detail.
  export default {
    name: 'StatStrip',
    props: {
      // the group's accessible name
      label: { type: String, required: true },
      // [{ key, label, value, detail?, tone?: 'success' }]
      stats: { type: Array, default: () => [] },
    },
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

  /* #dbdbdb on #484848 is 6.6:1 */
  .stat dt {
    color: #dbdbdb;
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
    color: #dbdbdb;
    font-size: 0.875rem;
  }

  /* an accent only, as the label says the same: 5.4:1 on #484848 */
  .stat.is-success .stat-value {
    color: #6fdca8;
  }
</style>
