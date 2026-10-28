<!--
How an operation on several items went, item by item: groups for the ones
that succeeded, failed, were skipped and were canceled, each failure with
its reason and, when given onRetry, which ones may work if tried again and a
button to retry them.
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
          <b-tag
            v-if="onRetry && item.retryable"
            type="is-info"
            size="is-small"
            class="ml-1">
            can retry
          </b-tag>
        </li>
      </ul>
    </b-collapse>
    <b-button
      v-if="onRetry && retryable.length"
      size="is-small"
      icon-left="redo"
      class="mt-1"
      @click="onRetry(retryable)">
      Retry failed ({{ retryable.length }})
    </b-button>
  </div>
</template>

<script>
  import { summarizeOutcome } from '@/utils/operations.js';
  import { plural } from '@/utils/plural.js';

  const GROUPS = [
    { status: 'failed', label: 'Failed', open: true },
    { status: 'skipped', label: 'Skipped', open: true },
    { status: 'canceled', label: 'Canceled', open: false },
    { status: 'succeeded', label: 'Succeeded', open: false },
  ];

  export default {
    props: {
      // { name, status, error, retryable }, as utils/operations.js gives them
      items: { type: Array, required: true },
      noun: { type: String, default: 'item' },
      // called with the names to retry; a prop, so the button and the
      // 'can retry' tags show only when something can retry them
      onRetry: { type: Function, default: null },
    },
    computed: {
      groups() {
        return GROUPS.map((group) => ({
          ...group,
          items: this.items.filter((item) => item.status === group.status),
        })).filter((group) => group.items.length);
      },
      retryable() {
        return summarizeOutcome(this.items).retryable;
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
