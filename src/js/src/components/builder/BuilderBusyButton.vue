<!-- A button that shows a turning ring while its work runs (builder/busy.js).
     It keeps its place, size and focus, and a press while busy does nothing.
     disabled only sets aria-disabled, so the press still reaches the parent. -->
<template>
  <button
    ref="buttonEl"
    :type="type"
    class="builder-button builder-busy-button"
    :class="{
      'is-busy': busy,
      'has-icon': Boolean(icon),
      'is-replaced': Boolean(busyLabel),
    }"
    :disabled="locked || undefined"
    :aria-busy="busy || undefined"
    :aria-disabled="busy || disabled || locked || undefined"
    :aria-label="busy && busyLabel ? busyLabel : undefined"
    @click="press">
    <template v-if="icon">
      <span
        v-if="busy"
        class="builder-toolbar__spinner builder-busy-button__ring"
        aria-hidden="true"
        data-testid="builder-spinner"></span>
      <builder-icon v-else :name="icon" :size="iconSize" />
      <slot />
    </template>
    <template v-else>
      <span
        class="builder-busy-button__label"
        :aria-hidden="(busy && Boolean(busyLabel)) || undefined">
        <slot />
      </span>
      <span
        v-if="busy"
        class="builder-toolbar__spinner builder-busy-button__ring"
        aria-hidden="true"
        data-testid="builder-spinner"></span>
    </template>
  </button>
</template>

<script setup>
  import { ref } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';

  const props = defineProps({
    busy: { type: Boolean, default: false },
    // What screen readers say while busy, such as "Loading nera". Without
    // it, the button keeps its name.
    busyLabel: { type: String, default: '' },
    disabled: { type: Boolean, default: false },
    locked: { type: Boolean, default: false },
    // The icon before the label, which the ring replaces while busy.
    icon: { type: String, default: '' },
    iconSize: { type: Number, default: 14 },
    type: {
      type: String,
      default: 'button',
      validator: (value) => ['button', 'submit'].includes(value),
    },
  });

  const emit = defineEmits(['click']);

  const buttonEl = ref(null);

  function press(event) {
    if (props.busy) {
      event.preventDefault();

      return;
    }

    emit('click', event);
  }

  defineExpose({
    focus: (options) => buttonEl.value?.focus(options),
  });
</script>

<style scoped>
  .builder-busy-button {
    position: relative;
    justify-content: center;
  }

  .builder-busy-button.is-busy {
    cursor: progress;
  }

  /* The ring in the button's own text color, which keeps 3:1 on its
     background in both themes, a primary button's too. */
  .builder-busy-button .builder-busy-button__ring {
    border-color: color-mix(in srgb, currentColor 30%, transparent);
    border-top-color: currentColor;
  }

  /* The label keeps the button's size, and its name, while the ring turns
     over it. */
  .builder-busy-button.is-busy:not(.has-icon) .builder-busy-button__label {
    opacity: 0;
  }

  .builder-busy-button:not(.has-icon) .builder-busy-button__ring {
    position: absolute;
    top: calc(50% - 7px);
    left: calc(50% - 7px);
  }

  .builder-busy-button.is-busy.is-replaced {
    border-color: transparent;
    background: transparent;
  }

  /* Two system colors, so the turn still shows (see builder.css). */
  @media (forced-colors: active) {
    .builder-busy-button .builder-busy-button__ring {
      border-color: ButtonFace;
      border-top-color: ButtonText;
    }
  }
</style>
