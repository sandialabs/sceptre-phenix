<!--
  The warnings of an Import or of a legacy conversion, shown in place of the
  dialog's form (see ImportDialog and UploadDialog).

  Each warning names something the diagram left out or changed. For this
  reason, the draft is made only when the user continues. Cancel makes
  nothing. After this view replaces the dialog's form, the dialog moves
  focus to Continue to editor, which the list describes.

  The ids and test ids start with the dialog's own prefix: import-warnings,
  upload-continue.
-->
<template>
  <div :data-testid="`${prefix}-result`">
    <p>{{ summary }}</p>
    <ul
      :id="`${prefix}-warnings`"
      class="builder-issues"
      :data-testid="`${prefix}-warnings`">
      <li
        v-for="(warning, index) in warnings"
        :key="index"
        data-level="warning">
        <strong>Warning:</strong>
        {{ warning }}
      </li>
    </ul>

    <div class="builder-dialog__actions">
      <button
        type="button"
        class="builder-button"
        :data-testid="`${prefix}-cancel`"
        @click="$emit('cancel')">
        Cancel
      </button>
      <button
        ref="continueButton"
        type="button"
        class="builder-button builder-button--primary"
        :aria-describedby="`${prefix}-warnings`"
        :data-testid="`${prefix}-continue`"
        @click="$emit('continue')">
        Continue to editor
      </button>
    </div>
  </div>
</template>

<script setup>
  import { ref } from 'vue';

  defineProps({
    // The dialog's id prefix: 'import' or 'upload'.
    prefix: { type: String, required: true },
    // How many warnings there are, as a sentence.
    summary: { type: String, required: true },
    warnings: { type: Array, required: true },
  });

  defineEmits(['cancel', 'continue']);

  const continueButton = ref(null);

  // The button the dialog moves focus to.
  defineExpose({ continueButton });
</script>
