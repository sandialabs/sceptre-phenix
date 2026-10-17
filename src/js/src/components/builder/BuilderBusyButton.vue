<!--
  A button that a spinner replaces while the request it started runs.

  The button stays in its place and keeps focus, so focus never falls to
  the page and returns to it after a dialog the request opens. While busy,
  its label is hidden but keeps the button's size, a ring turns in its
  place, and the button has no frame. aria-busy and the busy label (for
  example "Loading nera") tell screen readers. A press while busy does
  nothing. Reduced motion stops the ring turning (see builder.css), and
  the ring still shows the wait.

  `disabled` only marks the button aria-disabled, so it keeps focus and
  its press still reaches the parent, which can say why it does not apply.
-->
<template>
  <button
    type="button"
    class="builder-button builder-busy-button"
    :class="{ 'is-busy': busy }"
    :aria-busy="busy || undefined"
    :aria-disabled="busy || disabled || undefined"
    :aria-label="busy ? busyLabel : undefined"
    @click="busy || $emit('click', $event)">
    <span class="builder-busy-button__label" :aria-hidden="busy || undefined">
      <slot />
    </span>
    <span
      v-if="busy"
      class="builder-toolbar__spinner builder-busy-button__ring"
      aria-hidden="true"
      data-testid="builder-spinner"></span>
  </button>
</template>

<script setup>
  defineProps({
    busy: { type: Boolean, default: false },
    // What screen readers say while busy, such as "Loading nera".
    busyLabel: { type: String, default: 'Loading' },
    disabled: { type: Boolean, default: false },
  });

  defineEmits(['click']);
</script>

<style scoped>
  .builder-busy-button {
    position: relative;
    justify-content: center;
  }

  /* The label keeps the button's size while the ring takes its place. */
  .builder-busy-button.is-busy .builder-busy-button__label {
    visibility: hidden;
  }

  .builder-busy-button.is-busy {
    border-color: transparent;
    background: transparent;
    cursor: progress;
  }

  .builder-busy-button__ring {
    position: absolute;
    top: calc(50% - 7px);
    left: calc(50% - 7px);
  }
</style>
