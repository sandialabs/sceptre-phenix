<!--
  Import shell.

  Accepts a builder document (JSON or YAML) or a published diagram from the
  server. Topology and Experiment configs are refused with an explanation: the
  server converts those through Generate, so nothing is quietly dropped.
-->
<template>
  <builder-dialog
    title="Import diagram"
    title-id="import-dialog-title"
    @close="$emit('close')">
    <form @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>Source</legend>
        <label>
          <input v-model="form.source" type="radio" value="file" />
          File
        </label>
        <label>
          <input v-model="form.source" type="radio" value="text" />
          Paste text
        </label>
        <label>
          <input v-model="form.source" type="radio" value="published" />
          Published diagram
        </label>
      </fieldset>

      <div v-if="form.source === 'file'" class="builder-field">
        <label for="import-file">Builder document file</label>
        <input
          id="import-file"
          type="file"
          accept=".json,.yaml,.yml"
          data-testid="import-file"
          @change="onFile" />
      </div>

      <div v-else-if="form.source === 'text'" class="builder-field">
        <label for="import-text">Document text (JSON or YAML)</label>
        <textarea
          id="import-text"
          v-model="form.text"
          rows="10"
          data-testid="import-text"></textarea>
      </div>

      <div v-else class="builder-field">
        <label for="import-published">Published diagram</label>
        <select
          id="import-published"
          v-model="form.documentId"
          data-testid="import-published">
          <option value="">Select a diagram</option>
          <option v-for="doc in documents" :key="doc.id" :value="doc.id">
            {{ doc.name || doc.target || doc.id }}
          </option>
        </select>
      </div>

      <p v-if="error" role="alert" data-testid="import-error">{{ error }}</p>

      <div class="builder-dialog__actions">
        <button type="button" class="builder-button" @click="$emit('close')">
          Cancel
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="import-submit">
          Import
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { computed, reactive, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';

  import { MAX_IMPORT_BYTES, parseImport } from '@/builder/decode.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close', 'imported']);

  const store = useBuilderStore();
  const error = ref('');

  const form = reactive({
    source: 'file',
    text: '',
    documentId: '',
  });

  const documents = computed(() => store.documents);

  async function onFile(event) {
    const file = event.target.files?.[0];

    if (!file) {
      return;
    }

    error.value = '';

    if (file.size > MAX_IMPORT_BYTES) {
      error.value = 'The imported file is larger than the 5 MiB limit.';
      form.text = '';
      return;
    }

    form.text = await file.text();
  }

  async function submit() {
    error.value = '';

    if (form.source === 'published') {
      const found = documents.value.find((doc) => doc.id === form.documentId);

      if (!found) {
        error.value = 'Select a published diagram.';
        return;
      }

      const opened = await store.openPublishedDocument(found.id);

      if (!opened) {
        error.value = store.error;

        return;
      }

      emit('imported', { draftCreated: true });
      emit('close');

      return;
    }

    const result = parseImport(form.text);

    if (!result.ok) {
      error.value = result.error;

      return;
    }

    // Import always starts a new draft. Detach the current draft before loading
    // imported content so a later edit can never overwrite that draft.
    store.newDocument({ name: result.document.name });

    const opened = store.setDocument(result.document, {
      label: 'Imported diagram',
    });

    if (!opened) {
      error.value = store.error;

      return;
    }

    emit('imported');
    emit('close');
  }
</script>

<style scoped>
  .builder-dialog__actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
  }

  fieldset label {
    margin-right: 0.75rem;
  }
</style>
