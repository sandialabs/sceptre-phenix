<!-- Confirmation before an action that cannot be undone: an APG alert dialog.
     Focus starts on the button that keeps everything, so Enter or Escape never
     destroys work. -->
<template>
  <builder-dialog
    :title="title"
    :title-id="`${id}-title`"
    role="alertdialog"
    :aria-describedby="`${id}-message`"
    data-testid="builder-confirm"
    @close="$emit('cancel')">
    <p :id="`${id}-message`" class="builder-confirm__message">{{ message }}</p>

    <div class="builder-dialog__actions">
      <button
        ref="cancelButton"
        type="button"
        class="builder-button"
        data-testid="confirm-cancel"
        @click="$emit('cancel')">
        {{ cancelLabel }}
      </button>
      <!-- With an item action, its icon and color. Otherwise red. -->
      <builder-action-button
        v-if="action"
        :action="action"
        data-testid="confirm-accept"
        @click="$emit('confirm')">
        {{ confirmLabel }}
      </builder-action-button>
      <button
        v-else
        type="button"
        class="builder-button builder-button--danger"
        data-testid="confirm-accept"
        @click="$emit('confirm')">
        {{ confirmLabel }}
      </button>
    </div>
  </builder-dialog>
</template>

<script setup>
  import { onMounted, ref } from 'vue';

  import BuilderActionButton from './BuilderActionButton.vue';
  import BuilderDialog from './BuilderDialog.vue';

  defineProps({
    id: { type: String, default: 'builder-confirm' },
    title: { type: String, required: true },
    message: { type: String, required: true },
    confirmLabel: { type: String, required: true },
    cancelLabel: { type: String, default: 'Cancel' },
    // The item action the confirm button does, such as delete.
    action: { type: String, default: '' },
  });

  defineEmits(['confirm', 'cancel']);

  const cancelButton = ref(null);

  // BuilderDialog focuses its panel when it opens. This runs after that.
  onMounted(() => {
    cancelButton.value?.focus();
  });
</script>

<style scoped>
  /* The message can name drafts and topologies. A long, unbroken name wraps
     anywhere and does not run off a narrow dialog. */
  .builder-confirm__message {
    overflow-wrap: anywhere;
  }
</style>
