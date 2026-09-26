<!--
  Upload diagram, opened by Upload on the drafts landing and in the editor's
  toolbar.

  Accepts a builder document (JSON or YAML) or a published diagram from the
  server. Topology and Experiment configs are refused with an explanation: the
  server converts those through Import on the drafts landing (see
  GenerateDialog), so nothing is quietly dropped.
-->
<template>
  <builder-dialog
    title="Upload diagram"
    title-id="import-dialog-title"
    @close="$emit('close')">
    <form @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>Source</legend>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="import-source"
            value="file" />
          File
        </label>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="import-source"
            value="text" />
          Paste text
        </label>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="import-source"
            value="published" />
          Published diagram
        </label>
      </fieldset>

      <div v-if="form.source === 'file'" class="builder-field">
        <label for="import-file">Builder document file</label>
        <input
          id="import-file"
          type="file"
          accept=".json,.yaml,.yml"
          :aria-invalid="invalid('file')"
          :aria-describedby="describedBy('file', 'import-file-hint')"
          data-testid="import-file"
          @change="onFile" />
        <p id="import-file-hint" class="builder-hint">
          JSON or YAML, up to 5 MiB.
        </p>
      </div>

      <div v-else-if="form.source === 'text'" class="builder-field">
        <label for="import-text">Document text (JSON or YAML)</label>
        <textarea
          id="import-text"
          v-model="form.text"
          rows="10"
          :aria-invalid="invalid('text')"
          :aria-describedby="describedBy('text')"
          data-testid="import-text"></textarea>
      </div>

      <div v-else class="builder-field">
        <label for="import-published">Published diagram</label>
        <select
          id="import-published"
          v-model="form.documentId"
          :aria-invalid="invalid('published')"
          :aria-describedby="describedBy('published')"
          data-testid="import-published">
          <option value="">Select a diagram</option>
          <option v-for="doc in documents" :key="doc.id" :value="doc.id">
            {{ doc.name || doc.target || doc.id }}
          </option>
        </select>
      </div>

      <p
        id="import-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="import-error">{{
          error.text
        }}</span>
      </p>

      <div class="builder-dialog__actions">
        <button type="button" class="builder-button" @click="$emit('close')">
          Cancel
        </button>
        <!-- A published diagram is on the server already: it is opened. -->
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="import-submit">
          {{ form.source === 'published' ? 'Open' : 'Upload' }}
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { computed, reactive, watch } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import { parseErrorText, useFieldError } from './message.js';

  import { MAX_IMPORT_BYTES, parseImport } from '@/builder/decode.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close', 'imported']);

  const TOO_LARGE = 'The uploaded file is larger than the 5 MiB limit.';

  const store = useBuilderStore();
  // Errors are about the control of the chosen source.
  const { error, invalid, describedBy, fail } = useFieldError('import-error', {
    file: 'import-file',
    text: 'import-text',
    published: 'import-published',
  });

  // `file` is the chosen file's state: '' (none), 'read' or 'too-large'.
  const form = reactive({
    source: 'file',
    file: '',
    text: '',
    documentId: '',
  });

  const documents = computed(() => store.documents);

  // The read of the chosen file, which a submit waits for.
  let pendingRead = Promise.resolve();

  // An error about one source says nothing about another.
  watch(
    () => form.source,
    () => error.clear(),
  );

  async function onFile(event) {
    const file = event.target.files?.[0];

    error.clear();
    form.text = '';
    form.file = '';
    pendingRead = Promise.resolve();

    if (!file) {
      return;
    }

    if (file.size > MAX_IMPORT_BYTES) {
      form.file = 'too-large';
      error.set(TOO_LARGE, 'file');
      return;
    }

    const read = file.text();
    pendingRead = read;
    const text = await read;

    // A file chosen while this one was read replaces it.
    if (pendingRead === read) {
      form.text = text;
      form.file = 'read';
    }
  }

  async function submit() {
    error.clear();
    await pendingRead;

    if (form.source === 'published') {
      const found = documents.value.find((doc) => doc.id === form.documentId);

      if (!found) {
        fail('Select a published diagram.', 'published');
        return;
      }

      const opened = await store.openPublishedDocument(found.id);

      if (!opened) {
        error.set(store.error);

        return;
      }

      emit('imported', { draftCreated: true });
      emit('close');

      return;
    }

    if (form.source === 'file' && form.file !== 'read') {
      fail(
        form.file === 'too-large' ? TOO_LARGE : 'Choose a file to upload.',
        'file',
      );
      return;
    }

    const result = parseImport(form.text);

    if (!result.ok) {
      fail(parseErrorText(result.error), form.source);

      return;
    }

    // Upload always starts a new draft. Detach the current draft before loading
    // uploaded content so a later edit can never overwrite that draft.
    store.newDocument({ name: result.document.name });

    const opened = store.setDocument(result.document, {
      label: 'Uploaded diagram',
    });

    if (!opened) {
      error.set(store.error);

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
</style>
