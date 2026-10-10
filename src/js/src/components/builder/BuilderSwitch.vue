<!--
  An on/off switch for a setting that applies at once (APG switch pattern):
  a button with role="switch", named by its slot. The words On and Off
  beside the track say its state as well as the knob's side and the fill.
  Attributes given to it, such as aria-describedby and data-testid, go to
  the button.
-->
<template>
  <button
    type="button"
    role="switch"
    class="builder-switch"
    :aria-checked="String(modelValue)"
    @click="$emit('update:modelValue', !modelValue)">
    <span class="builder-switch__track" aria-hidden="true" />
    <slot />
    <span class="builder-switch__state" aria-hidden="true">
      {{ modelValue ? 'On' : 'Off' }}
    </span>
  </button>
</template>

<script setup>
  defineProps({
    modelValue: { type: Boolean, default: false },
  });

  defineEmits(['update:modelValue']);
</script>

<style scoped>
  .builder-switch {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    min-height: 2rem;
    padding: 0.2rem 0.3rem 0.2rem 0;
    border: 0;
    border-radius: var(--bx-radius);
    background: none;
    font-weight: 600;
    text-align: start;
    cursor: pointer;
  }

  /* The knob is drawn with a border, so forced colors keep it. On is the
     knob at the right and the word On, not only the fill. */
  .builder-switch__track {
    position: relative;
    flex: none;
    width: 2.25rem;
    height: 1.25rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: 999px;
    background: var(--bx-bg-alt);
  }

  .builder-switch__track::after {
    content: '';
    position: absolute;
    top: 2px;
    left: 2px;
    width: 0;
    height: 0;
    border: calc(0.625rem - 3px) solid var(--bx-border-strong);
    border-radius: 50%;
    transition: left var(--bx-motion) ease-in-out;
  }

  .builder-switch[aria-checked='true'] .builder-switch__track {
    border-color: var(--bx-accent);
    background: var(--bx-accent);
  }

  .builder-switch[aria-checked='true'] .builder-switch__track::after {
    left: calc(100% - 1.25rem + 4px);
    border-color: var(--bx-accent-contrast);
  }

  .builder-switch__state {
    min-width: 1.75em;
    font-weight: 400;
    color: var(--bx-text-muted);
  }
</style>
