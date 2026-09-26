<!--
  Confirmation before an action that cannot be undone.

  An alert dialog (APG Alert and Message Dialogs pattern): the message names
  what will be lost and is the dialog's description, and focus starts on the
  button that keeps everything, so Enter or Escape never destroys work by
  accident. Escape and a click outside the dialog (see BuilderDialog) cancel.
-->
<template>
  <builder-dialog
    :title="title"
    :title-id="`${id}-title`"
    role="alertdialog"
    :aria-describedby="`${id}-message`"
    data-testid="builder-confirm"
    @close="$emit('cancel')">
    <p :id="`${id}-message`">{{ message }}</p>

    <div class="builder-dialog__actions">
      <button
        ref="cancelButton"
        type="button"
        class="builder-button"
        data-testid="confirm-cancel"
        @click="$emit('cancel')">
        {{ cancelLabel }}
      </button>
      <button
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

  import BuilderDialog from './BuilderDialog.vue';

  defineProps({
    id: { type: String, default: 'builder-confirm' },
    title: { type: String, required: true },
    message: { type: String, required: true },
    confirmLabel: { type: String, required: true },
    cancelLabel: { type: String, default: 'Cancel' },
  });

  defineEmits(['confirm', 'cancel']);

  const cancelButton = ref(null);

  // BuilderDialog focuses its panel when it opens; this runs after it.
  onMounted(() => {
    cancelButton.value?.focus();
  });
</script>

<style scoped>
  .builder-dialog__actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
  }
</style>
