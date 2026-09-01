<!--
  Modal shell shared by the builder dialogs.

  Handles the accessibility basics once: labelled dialog role, Escape to
  close, initial focus and focus return to the invoking control.
-->
<template>
  <div
    class="builder-dialog"
    data-testid="builder-dialog"
    @keydown.esc.stop="$emit('close')">
    <div
      ref="panel"
      class="builder-dialog__panel"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="titleId"
      tabindex="-1">
      <div class="builder-dialog__header">
        <h2 :id="titleId">{{ title }}</h2>
        <button
          type="button"
          class="builder-button"
          aria-label="Close dialog"
          @click="$emit('close')">
          <builder-icon name="close" :size="14" />
        </button>
      </div>

      <slot />
    </div>
  </div>
</template>

<script setup>
  import { onBeforeUnmount, onMounted, ref } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';

  const props = defineProps({
    title: { type: String, required: true },
    titleId: { type: String, default: 'builder-dialog-title' },
  });

  defineEmits(['close']);

  const panel = ref(null);
  let previous = null;

  onMounted(() => {
    previous = document.activeElement;
    panel.value?.focus();
  });

  onBeforeUnmount(() => {
    if (previous && typeof previous.focus === 'function') {
      previous.focus();
    }
  });
</script>

<style scoped>
  .builder-dialog__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    margin-bottom: 0.75rem;
  }

  .builder-dialog__header h2 {
    font-size: 1.05rem;
    font-weight: 700;
    margin: 0;
  }
</style>
