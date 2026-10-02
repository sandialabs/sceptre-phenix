<!--
  What a bulk action left undone: how many items it could not change, and
  each of them with why.

  It is an alert, and takes focus when it appears (the parent calls
  focus()), as the summary of a refused share list does: a keyboard user is
  then at the list of what to look at. Dismiss, when it has one, is the
  parent's to act on: it removes the summary and says where focus goes.
-->
<template>
  <div
    ref="root"
    class="builder-panel builder-error builder-bulk-summary"
    role="alert"
    tabindex="-1"
    :aria-labelledby="titleId"
    data-testid="bulk-summary">
    <h3 :id="titleId">{{ heading }}</h3>
    <ul>
      <li v-for="item in items" :key="item.key">
        {{ item.name }}: {{ item.reason }}
      </li>
    </ul>
    <button
      v-if="dismissible"
      type="button"
      class="builder-button"
      data-testid="bulk-summary-dismiss"
      @click="$emit('dismiss')">
      Dismiss
    </button>
  </div>
</template>

<script setup>
  import { ref, useId } from 'vue';

  defineProps({
    // "2 of 5 drafts could not be deleted. The other 3 were deleted."
    heading: { type: String, required: true },
    // The items left as they were: { key, name, reason }, the reason a
    // sentence.
    items: { type: Array, default: () => [] },
    dismissible: { type: Boolean, default: true },
  });

  defineEmits(['dismiss']);

  const root = ref(null);
  // Its own id: a page can hold more than one summary, each in a list or a
  // dialog of its own.
  const titleId = `bulk-summary-title-${useId()}`;

  defineExpose({
    focus() {
      root.value?.focus();
    },
  });
</script>

<style scoped>
  .builder-bulk-summary {
    margin-bottom: 0.75rem;
    padding: 0.6rem 0.75rem;
    border: 2px solid var(--bx-danger);
    overflow-wrap: anywhere;
  }

  .builder-bulk-summary h3 {
    margin: 0 0 0.35rem;
    font-size: 0.95rem;
    font-weight: 700;
    color: var(--bx-danger);
  }

  .builder-bulk-summary ul {
    margin: 0 0 0.5rem;
    padding-left: 1.1rem;
  }

  .builder-bulk-summary ul:last-child {
    margin-bottom: 0;
  }
</style>
