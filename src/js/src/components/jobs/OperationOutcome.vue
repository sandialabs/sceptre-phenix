<!--
How an operation on several items went, item by item, in groups by outcome,
each failure with its reason.
-->
<template>
  <div>
    <b-collapse
      v-for="group in groups"
      :key="group.status"
      :open="group.open"
      class="outcome-group">
      <template #trigger="{ open }">
        <button
          type="button"
          class="button is-small is-white outcome-trigger"
          :aria-expanded="String(open)">
          <b-icon
            :icon="open ? 'chevron-up' : 'chevron-down'"
            size="is-small" />
          <span>{{ group.label }}: {{ countText(group.items.length) }}</span>
        </button>
      </template>
      <ul class="outcome-items">
        <li v-for="item in group.items" :key="item.name">
          <span class="outcome-name">{{ item.name }}</span>
          <span v-if="item.error">: {{ item.error }}</span>
        </li>
      </ul>
    </b-collapse>
  </div>
</template>

<script>
  import { plural } from '@/utils/plural.js';

  const GROUPS = [
    { status: 'failed', label: 'Failed', open: true },
    { status: 'skipped', label: 'Skipped', open: true },
    { status: 'canceled', label: 'Canceled', open: false },
    { status: 'succeeded', label: 'Succeeded', open: false },
  ];

  export default {
    props: {
      // { name, status, error }, as utils/operations.js gives them
      items: { type: Array, required: true },
      noun: { type: String, default: 'item' },
    },
    computed: {
      groups() {
        return GROUPS.map((group) => ({
          ...group,
          items: this.items.filter((item) => item.status === group.status),
        })).filter((group) => group.items.length);
      },
    },
    methods: {
      countText(count) {
        return plural(count, this.noun);
      },
    },
  };
</script>

<style scoped>
  .outcome-trigger {
    padding-left: 0;
  }

  .outcome-items {
    margin: 0 0 0.25rem 1.5rem;
    font-size: 0.85rem;
    list-style: disc;
  }

  .outcome-name {
    font-weight: 600;
  }
</style>
