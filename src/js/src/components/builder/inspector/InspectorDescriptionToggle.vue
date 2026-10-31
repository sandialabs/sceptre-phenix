<!-- Shows or hides the node's description on the canvas, at once and as one
     Undo step. The diagram keeps the description (see BuilderInspector). -->
<template>
  <button
    type="button"
    class="builder-button control__description-toggle"
    :aria-pressed="String(toggle.shown)"
    aria-label="Show description on the canvas"
    title="Show description on the canvas"
    :disabled="toggle.locked"
    data-testid="inspector-description-toggle"
    @click="toggle.set(!toggle.shown)">
    <builder-icon :name="toggle.shown ? 'eye' : 'eye-off'" :size="14" />
  </button>
</template>

<script setup>
  import BuilderIcon from '../BuilderIcon.vue';

  defineProps({
    // {shown, locked, set} (see useDescriptionToggle).
    toggle: { type: Object, required: true },
  });
</script>
