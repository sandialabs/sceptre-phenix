<!--
  The row above a list whose items can be selected: Select all, how many
  are selected, and the buttons that act on the selection (the default
  slot).

  Select all is a native checkbox. It is checked when every item is
  selected and indeterminate when only some are, which browsers tell
  assistive technology as "mixed". Pressing it with none or some selected
  selects every item, and with all selected none (see useListSelection in
  builder/listSelection.js, which gives count, total and state).

  The count has the id bulk-count-<id>, for the buttons to be described by:
  a button that cannot act with nothing selected then says how many are.
  While an action runs (running), Select all is disabled and the row says
  how far the action is; the progress is not a live region, as the page
  says what the run came to.

  The row is not sticky: a sticky row could cover the card focus is on
  (WCAG 2.4.11).
-->
<template>
  <div
    class="builder-bulk"
    role="group"
    :aria-label="`Bulk actions: ${label}`"
    :data-testid="`bulk-bar-${id}`">
    <label class="builder-bulk__all">
      <input
        ref="all"
        type="checkbox"
        :checked="state === 'all'"
        :disabled="disabled || Boolean(running)"
        :data-testid="`bulk-all-${id}`"
        @change="onChange" />
      Select all
    </label>
    <span
      :id="`bulk-count-${id}`"
      class="builder-bulk__count"
      :data-testid="`bulk-count-${id}`"
      >{{ count }} of {{ total }} selected</span
    >
    <slot />
    <span
      v-if="running"
      class="builder-bulk__progress"
      :data-testid="`bulk-progress-${id}`">
      <span class="builder-toolbar__spinner" aria-hidden="true"></span>
      {{ running.label }} {{ running.done }} of {{ running.total }}…
    </span>
  </div>
</template>

<script setup>
  import { nextTick, onMounted, ref, watch } from 'vue';

  const props = defineProps({
    // Names the row's elements: bulk-bar-<id>, bulk-all-<id>,
    // bulk-count-<id>, bulk-progress-<id>.
    id: { type: String, required: true },
    // What the list holds, for the group's name: "My Drafts".
    label: { type: String, required: true },
    // How many items are selected, of how many that can be.
    count: { type: Number, required: true },
    total: { type: Number, required: true },
    // 'none', 'some' or 'all' of them.
    state: { type: String, default: 'none' },
    // The action under way on the selection, if one is: { label, done,
    // total }, shown as "Deleting 2 of 5…".
    running: { type: Object, default: null },
    // Nothing can be selected now, as while an action runs on another list.
    disabled: { type: Boolean, default: false },
  });

  const emit = defineEmits(['toggle-all']);

  const all = ref(null);

  // indeterminate is a property of the element, with no attribute. The
  // browser checks or unchecks the checkbox when it is pressed, whatever
  // the selection then is, so its checked state is set again too.
  function show() {
    if (all.value) {
      all.value.indeterminate = props.state === 'some';
      all.value.checked = props.state === 'all';
    }
  }

  function onChange() {
    emit('toggle-all');
    nextTick(show);
  }

  onMounted(show);
  watch(() => [props.state, props.count], show, { flush: 'post' });
</script>

<style scoped>
  .builder-bulk {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.75rem;
  }

  /* The checkbox and its words are one target, at least 24px high (WCAG
     2.5.8). */
  .builder-bulk__all {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    min-height: 1.5rem;
    cursor: pointer;
  }

  .builder-bulk__all input {
    appearance: auto;
    width: 1.1rem;
    height: 1.1rem;
    margin: 0;
    accent-color: var(--bx-accent);
  }

  .builder-bulk__all:has(input:disabled) {
    cursor: not-allowed;
  }

  .builder-bulk__count {
    color: var(--bx-text-muted);
  }

  .builder-bulk__progress {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
  }
</style>
