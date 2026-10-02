<!--
  Import topology or experiment, opened by Import on the drafts landing.

  POST /builder/generate asks the server to build a diagram from an existing
  topology or experiment; the server owns the conversion, so the client only
  picks the source and reports the warnings that come back. A generation with
  warnings keeps the dialog open on them, because each one names something
  the draft left out or changed: the draft is created only when the user
  continues, and Cancel or closing the dialog instead creates nothing.
  Nothing says the diagram was imported until its draft exists.

  The stored configs offered are read again every time the dialog opens, and
  again when the chosen one turns out to have been removed since.

  A topology that still has a diagram of the legacy Builder (a builder-xml
  annotation) is marked in the list. The server converts that diagram as it
  imports the topology, and the dialog says so before and after.

  A topology that includes other topologies is imported with their nodes
  read only, or with those nodes combined into it; a stored topology may
  also be imported as a copy. Either gives the diagram a new name and links
  it to no config, so publishing it creates a new topology. The controls
  for these show only when they apply (see importOptions.js), after the
  control that reveals them, and focus stays where it is.

  A link from the Configs page opens the dialog on the topology it names
  (`initial`), with a sentence above the form that says why.

  A draft imported from a config file records the chosen file's name
  (sourceFile), which the Inspector then shows.
-->
<template>
  <builder-dialog
    title="Import topology or experiment"
    title-id="import-dialog-title"
    :describedby="intro ? 'import-intro' : ''"
    @close="$emit('close')">
    <p
      v-if="intro"
      id="import-intro"
      class="builder-import__intro"
      data-testid="import-intro">
      {{ intro }}
    </p>

    <import-warnings
      v-if="warnings.length"
      ref="warningsView"
      prefix="import"
      :summary="warningSummary"
      :warnings="warnings"
      @cancel="$emit('close')"
      @continue="proceed" />

    <!-- novalidate: the dialog checks the new topology name itself, and
         says what is wrong with it in its own words (see submit). -->
    <form v-else novalidate @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>Source</legend>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="import-source"
            value="stored" />
          Stored config
        </label>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="import-source"
            value="file" />
          Config file
        </label>
      </fieldset>

      <div v-if="form.source === 'stored'" class="builder-field">
        <label for="import-kind">Source kind</label>
        <select
          id="import-kind"
          v-model="form.kind"
          data-testid="import-kind"
          @change="form.name = ''">
          <option value="topology">Topology</option>
          <option value="experiment">Experiment</option>
        </select>
      </div>

      <div v-if="form.source === 'stored'" class="builder-field">
        <label for="import-name">Source name</label>
        <select
          id="import-name"
          v-model="form.name"
          required
          :aria-invalid="invalid('name')"
          :aria-describedby="
            describedBy('name', options.legacy ? 'import-legacy-hint' : '')
          "
          data-testid="import-name">
          <option value="">Choose {{ withArticle(form.kind) }}</option>
          <option
            v-for="choice in choices"
            :key="choice.name"
            :value="choice.name">
            {{ choice.label }}
          </option>
        </select>
        <p
          v-if="options.legacy"
          id="import-legacy-hint"
          class="builder-hint"
          data-testid="import-legacy-hint">
          This topology has a legacy Builder diagram. Its layout is converted.
          <template v-if="options.mode === 'import'">
            Publishing the draft to this topology replaces the legacy diagram.
          </template>
        </p>
      </div>

      <p
        v-if="form.source === 'stored' && !choices.length"
        data-testid="import-empty">
        This phenix instance has no {{ form.kind }} configs to import.
      </p>

      <div v-if="form.source === 'file'" class="builder-field">
        <label for="import-file">Topology or Experiment config</label>
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

      <fieldset
        v-if="options.showIncludes"
        class="builder-field"
        data-testid="import-includes">
        <legend>Included topologies</legend>
        <p id="import-includes-hint" class="builder-hint">
          {{ options.includesText }}
        </p>
        <label class="builder-choice">
          <input
            v-model="options.includes"
            type="radio"
            name="import-includes"
            value="keep"
            aria-describedby="import-includes-hint import-keep-hint"
            data-testid="import-keep" />
          Keep included nodes read only
        </label>
        <p id="import-keep-hint" class="builder-hint builder-import__detail">
          Their nodes are shown but cannot be changed here. Publishing keeps the
          includes.
        </p>
        <label class="builder-choice">
          <input
            v-model="options.includes"
            type="radio"
            name="import-includes"
            value="combine"
            aria-describedby="import-includes-hint import-combine-hint"
            data-testid="import-combine" />
          Combine into one new topology
        </label>
        <p id="import-combine-hint" class="builder-hint builder-import__detail">
          Their nodes are copied into the diagram and can be changed. Publishing
          creates a new topology.
        </p>
      </fieldset>

      <div v-if="options.showCopy" class="builder-field">
        <label class="builder-choice">
          <input
            v-model="options.copy"
            type="checkbox"
            aria-describedby="import-copy-hint"
            data-testid="import-copy" />
          Create a new topology as a copy
        </label>
        <p id="import-copy-hint" class="builder-hint builder-import__detail">
          The draft is not linked to {{ options.copyOf }}. Publishing creates a
          new topology and leaves {{ options.copyOf }} as it is.
        </p>
      </div>

      <div v-if="options.showNewName" class="builder-field">
        <label for="import-new-name">New topology name</label>
        <input
          id="import-new-name"
          :value="options.newName"
          type="text"
          required
          autocomplete="off"
          spellcheck="false"
          :aria-invalid="invalid('newName')"
          :aria-describedby="describedBy('newName', 'import-new-name-hint')"
          data-testid="import-new-name"
          @input="onNewName" />
        <p id="import-new-name-hint" class="builder-hint">
          {{ CONFIG_NAME_RULE }}
        </p>
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
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="import-submit"
          :disabled="!ready"
          :aria-disabled="busy || !ready ? 'true' : undefined">
          {{ busy ? 'Importing…' : 'Import' }}
        </button>
      </div>
    </form>

    <p
      class="builder-dialog__message builder-visually-hidden"
      role="status"
      data-testid="import-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import ImportWarnings from './ImportWarnings.vue';
  import {
    importIntro,
    notAvailable,
    useImportOptions,
  } from './importOptions.js';
  import { readChosenFile, useFieldError, useMessage } from './message.js';

  import { describeImport } from '@/builder/announce.js';
  import { LEGACY_ANNOTATION } from '@/builder/configs.js';
  import { withArticle } from '@/builder/decode.js';
  import { CONFIG_NAME_RULE } from '@/builder/publish.js';
  import { useBuilderStore } from '@/builder/store.js';

  const props = defineProps({
    // The stored topology the form opens on, when a link from the Configs
    // page asked for it: { kind: 'topology', name }.
    initial: { type: Object, default: null },
  });

  const emit = defineEmits(['close', 'imported']);

  const store = useBuilderStore();
  const busy = ref(false);
  // A source the server refused is reported on the control that chose it,
  // and a new name it refused on the field it was typed in.
  const { error, invalid, describedBy, fail } = useFieldError('import-error', {
    file: 'import-file',
    name: 'import-name',
    newName: 'import-new-name',
  });
  // store.errorField for a refused source or name, as this form's field.
  const REFUSED_FIELD = { content: 'file', name: 'name', newName: 'newName' };
  const status = useMessage();
  const warnings = ref([]);
  const warningsView = ref(null);
  // A result with warnings, held until the user continues.
  let pending = null;

  // `fileName` is the name of the config file whose text is `content`.
  const form = reactive({
    source: 'stored',
    kind: props.initial?.kind === 'experiment' ? 'experiment' : 'topology',
    name: props.initial?.name || '',
    content: '',
    fileName: '',
  });

  // What to do with included topologies, and whether to make a copy.
  const options = useImportOptions(form, () => store.sources.topologies);

  // The stored configs of the chosen kind: the name of each, what the list
  // calls it, and whether it has a diagram of the legacy Builder.
  const choices = computed(() => {
    const list =
      form.kind === 'experiment'
        ? store.sources.experiments
        : store.sources.topologies;

    return (list || []).map((entry) => {
      const name = typeof entry === 'string' ? entry : entry.name;
      const legacy =
        form.kind === 'topology' && entry?.builder === LEGACY_ANNOTATION;

      return {
        name,
        legacy,
        label: legacy ? `${name} (legacy Builder diagram)` : name,
      };
    });
  });

  // Why the dialog opened on a topology, above the form while that
  // topology is the one chosen.
  const intro = computed(() =>
    props.initial?.name &&
    !warnings.value.length &&
    form.source === 'stored' &&
    form.kind === 'topology' &&
    form.name === props.initial.name
      ? importIntro(form.name, options.legacy)
      : '',
  );

  // An error about one source says nothing about another, and one about a
  // choice nothing about the other choice.
  watch(
    () => [
      form.source,
      form.kind,
      form.name,
      form.content,
      options.includes,
      options.copy,
    ],
    () => error.clear(),
  );

  // The topology a link named may be gone, or not one the user may read,
  // by the time the configs are read again.
  onMounted(async () => {
    await store.fetchSources();

    if (
      props.initial?.name &&
      form.name === props.initial.name &&
      !choices.value.some((choice) => choice.name === form.name)
    ) {
      form.name = '';
      await nextTick();
      fail(notAvailable(props.initial.name), 'name');
    }
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
  // the import: as a conversion when the source had a legacy diagram, and
  // as a copy or a combined topology, with its new name, when it is one.
  function imported({ mode, ...result }) {
    const said = describeImport(result.warnings, {
      mode,
      legacy: result.source?.builder === LEGACY_ANNOTATION,
      source: result.source?.name || '',
      name: result.document?.name || '',
    });

    return { ...result, announcement: `${said} Draft created.` };
  }

  function onNewName(event) {
    options.typeName(event.target.value);

    if (error.field === 'newName') {
      error.clear();
    }
  }

  async function onFile(event) {
    form.content = '';
    form.fileName = '';

    const read = await readChosenFile(event, 'config');

    if (read?.error) {
      error.set(read.error, 'file');
    } else if (read) {
      error.clear();
      form.content = read.text;
      form.fileName = read.name || '';
    }
  }

  async function submit() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value || !ready.value) {
      return;
    }

    // The new name is checked before anything is asked of the server.
    const problem = options.problem();

    if (problem) {
      status.clear();
      fail(problem, 'newName');

      return;
    }

    busy.value = true;
    error.clear();
    status.set('Importing…');
    warnings.value = [];

    const { mode } = options;
    // Only a config file has a file name to record.
    const sourceFile = form.source === 'file' ? form.fileName : '';
    const generated = await store.generate(options.request());
    const result = generated && { ...generated, sourceFile, mode };

    busy.value = false;

    if (!result) {
      const message = store.error || 'Import failed.';
      const field = REFUSED_FIELD[store.errorField] || '';

      status.clear();

      // A stored config removed since the list was read is no longer
      // offered once the list is read again, so the choice goes back to the
      // placeholder. That clears the error of the old choice, so the
      // refusal is shown after it.
      if (
        field === 'name' &&
        !choices.value.some((choice) => choice.name === form.name)
      ) {
        form.name = '';
        await nextTick();
      }

      fail(message, field);

      return;
    }

    if (!result.warnings?.length) {
      status.clear();
      emit('imported', imported(result));
      emit('close');

      return;
    }

    pending = result;
    warnings.value = result.warnings;
    status.set(warningSummary.value);

    // The warnings replace the form, and the button that had focus with it.
    await nextTick();
    warningsView.value?.continueButton?.focus();
  }

  // Opens the draft once the user has seen the warnings. It is created as
  // the dialog closes, so focus returns to the page first and then moves on
  // to the editor as it opens.
  function proceed() {
    emit('imported', imported(pending));
    emit('close');
  }
</script>

<style scoped>
  .builder-import__intro {
    margin: 0 0 0.75rem;
  }

  /* What a choice does, under its text and clear of its button. */
  .builder-import__detail {
    margin: 0 0 0.35rem 1.4rem;
  }
</style>
