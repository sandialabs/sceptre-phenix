<!--
  Generate shell, opened by Import on the drafts landing.

  POST /builder/generate asks the server to build a diagram from an existing
  topology or experiment; the server owns the conversion, so the client only
  picks the source and reports the warnings that come back. A generation with
  warnings keeps the dialog open on them, because each one names something
  the draft left out or changed: the draft is created only when the user
  continues, and Cancel or closing the dialog instead creates nothing.
  Nothing says the diagram was imported until its draft exists.

  The stored configs offered are read again every time the dialog opens, and
  again when the chosen one turns out to have been removed since.
-->
<template>
  <builder-dialog
    title="Import topology or experiment"
    title-id="generate-dialog-title"
    @close="$emit('close')">
    <div v-if="warnings.length" data-testid="generate-result">
      <p>{{ warningSummary }}</p>
      <ul
        id="generate-warnings"
        class="builder-issues"
        data-testid="generate-warnings">
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
          data-testid="generate-cancel"
          @click="$emit('close')">
          Cancel
        </button>
        <button
          ref="continueButton"
          type="button"
          class="builder-button builder-button--primary"
          aria-describedby="generate-warnings"
          data-testid="generate-continue"
          @click="proceed">
          Continue to editor
        </button>
      </div>
    </div>

    <form v-else @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>Source</legend>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="generate-source"
            value="stored" />
          Stored config
        </label>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="generate-source"
            value="uploaded" />
          Uploaded config
        </label>
      </fieldset>

      <div v-if="form.source === 'stored'" class="builder-field">
        <label for="generate-kind">Source kind</label>
        <select
          id="generate-kind"
          v-model="form.kind"
          data-testid="generate-kind"
          @change="form.name = ''">
          <option value="topology">Topology</option>
          <option value="experiment">Experiment</option>
        </select>
      </div>

      <div v-if="form.source === 'stored'" class="builder-field">
        <label for="generate-name">Source name</label>
        <select
          id="generate-name"
          v-model="form.name"
          required
          :aria-invalid="invalid('name')"
          :aria-describedby="describedBy('name')"
          data-testid="generate-name">
          <option value="">Choose {{ withArticle(form.kind) }}</option>
          <option v-for="name in choices" :key="name" :value="name">
            {{ name }}
          </option>
        </select>
      </div>

      <p
        v-if="form.source === 'stored' && !choices.length"
        data-testid="generate-empty">
        This phenix instance has no {{ form.kind }} configs to import.
      </p>

      <div v-if="form.source === 'uploaded'" class="builder-field">
        <label for="generate-file">Topology or Experiment config</label>
        <input
          id="generate-file"
          type="file"
          accept=".json,.yaml,.yml"
          :aria-invalid="invalid('file')"
          :aria-describedby="describedBy('file', 'generate-file-hint')"
          data-testid="generate-file"
          @change="onFile" />
        <p id="generate-file-hint" class="builder-hint">
          JSON or YAML, up to 5 MiB.
        </p>
      </div>

      <p
        id="generate-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="generate-error">{{
          error.text
        }}</span>
      </p>

      <div class="builder-dialog__actions">
        <button type="button" class="builder-button" @click="$emit('close')">
          Cancel
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="generate-submit"
          :disabled="!ready"
          :aria-disabled="busy || !ready ? 'true' : undefined">
          {{ busy ? 'Importing…' : 'Import' }}
        </button>
      </div>
    </form>

    <p
      class="builder-dialog__message builder-visually-hidden"
      role="status"
      data-testid="generate-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import { useFieldError, useMessage } from './message.js';

  import { describeImport } from '@/builder/announce.js';
  import { MAX_IMPORT_BYTES, withArticle } from '@/builder/decode.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close', 'generated']);

  const store = useBuilderStore();
  const busy = ref(false);
  // A source the server refused is reported on the control that chose it.
  const { error, invalid, describedBy, fail } = useFieldError(
    'generate-error',
    { file: 'generate-file', name: 'generate-name' },
  );
  // store.errorField for a refused source, as this form's field.
  const REFUSED_FIELD = { content: 'file', name: 'name' };
  const status = useMessage();
  const warnings = ref([]);
  const continueButton = ref(null);
  // A result with warnings, held until the user continues.
  let pending = null;

  const form = reactive({
    source: 'stored',
    kind: 'topology',
    name: '',
    content: '',
  });

  const choices = computed(() => {
    const list =
      form.kind === 'experiment'
        ? store.sources.experiments
        : store.sources.topologies;

    return (list || []).map((entry) =>
      typeof entry === 'string' ? entry : entry.name,
    );
  });

  // An error about one source says nothing about another.
  watch(
    () => [form.source, form.kind, form.name],
    () => error.clear(),
  );

  onMounted(() => {
    store.fetchSources();
  });

  const ready = computed(() =>
    form.source === 'stored' ? Boolean(form.name) : Boolean(form.content),
  );

  // Before the user continues, nothing has been imported.
  const warningSummary = computed(() => {
    const count = warnings.value.length;

    return count === 1
      ? 'This import has 1 warning.'
      : `This import has ${count} warnings.`;
  });

  // The result for afterImport, which creates the draft and then announces
  // the import.
  function imported(result) {
    return {
      ...result,
      announcement: `${describeImport(result.warnings)} Draft created.`,
    };
  }

  async function onFile(event) {
    const file = event.target.files?.[0];

    if (!file) {
      form.content = '';
      return;
    }

    if (file.size > MAX_IMPORT_BYTES) {
      error.set('The uploaded config is larger than the 5 MiB limit.', 'file');
      form.content = '';
      return;
    }

    error.clear();
    form.content = await file.text();
  }

  async function submit() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value || !ready.value) {
      return;
    }

    busy.value = true;
    error.clear();
    status.set('Importing…');
    warnings.value = [];

    const request =
      form.source === 'uploaded'
        ? { content: form.content }
        : { kind: form.kind, name: form.name };
    const result = await store.generate(request);

    busy.value = false;

    if (!result) {
      const message = store.error || 'Import failed.';
      const field = REFUSED_FIELD[store.errorField] || '';

      status.clear();

      // A stored config removed since the list was read is no longer
      // offered once the list is read again, so the choice goes back to the
      // placeholder. That clears the error of the old choice, so the
      // refusal is shown after it.
      if (field === 'name' && !choices.value.includes(form.name)) {
        form.name = '';
        await nextTick();
      }

      fail(message, field);

      return;
    }

    if (!result.warnings?.length) {
      status.clear();
      emit('generated', imported(result));
      emit('close');

      return;
    }

    pending = result;
    warnings.value = result.warnings;
    status.set(warningSummary.value);

    // The warnings replace the form, and the button that had focus with it.
    await nextTick();
    continueButton.value?.focus();
  }

  // Opens the draft once the user has seen the warnings. It is created as
  // the dialog closes, so focus returns to the page first and then moves on
  // to the editor as it opens.
  function proceed() {
    emit('generated', imported(pending));
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
