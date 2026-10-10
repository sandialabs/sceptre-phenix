<!--
  Upload diagram, opened by Upload on the drafts landing and in the editor's
  toolbar.

  Accepts a builder document (JSON or YAML), a published diagram from the
  server, or a diagram of the legacy Builder. Topology and Experiment configs
  are refused with an explanation: the server converts those through Import
  on the drafts landing (see ImportDialog), so nothing is quietly dropped.

  A diagram of the legacy Builder is a file: the XML that editor saved, or a
  Topology config that holds it in its builder-xml annotation. POST
  /builder/legacy converts it on the server. A conversion with warnings
  keeps the dialog open on them, as Import does: the draft is made only when
  the user continues, so Cancel leaves the open draft as it is. So does
  closing the dialog, or choosing another source or file, while a conversion
  is still under way: its answer is then dropped.

  A draft made from a chosen file records the file's name (sourceFile), which
  the Inspector then shows. Pasted text has none. The published diagrams
  offered include those of topologies read from the Builder file they name,
  marked (File).

  A document that carries copies of custom icons (a downloaded file does)
  puts them into the server's icon library first (see ingestIcons): a copy
  the server has as it is, or takes under its name, is dropped; one the
  server refuses, or has with other bytes under that name, stays in the
  draft, and the dialog shows a warning for it, as it shows the warnings of
  a conversion, before the draft is made.

  A Builder package (see package.js), recognized by its $schema, is decoded
  as strictly as a document, and the server says which of what its diagram
  needs it has (POST /builder/package/resolve). That list replaces the form
  (see PackageImport). Continue to editor creates the configs the user
  ticked, one at a time through POST /configs, puts the custom icons the
  diagram carries into the icon library, and opens the diagram as a new
  draft, as any upload does; the diagram's references are never rewritten.
  A config that could not be created, and an icon warning, are shown first,
  as an upload's warnings are. Cancel, or closing the dialog, leaves the
  open draft as it is. While Continue creates the configs and adds the
  icons, the dialog stays open: Cancel is unavailable and says what the
  dialog is doing, and Close, Escape and a click outside do nothing, so
  that no config is created out of sight.
-->
<template>
  <builder-dialog
    title="Upload diagram"
    title-id="upload-dialog-title"
    @close="requestClose">
    <import-warnings
      v-if="warnings.length"
      ref="warningsView"
      prefix="upload"
      :summary="warningSummary"
      :warnings="warnings"
      @cancel="$emit('close')"
      @continue="proceed" />

    <package-import
      v-else-if="packageView"
      ref="packageImportView"
      :dependencies="packageView.dependencies"
      :busy="busy"
      :progress="status.text"
      @cancel="requestClose"
      @continue="continuePackage" />

    <form v-else @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>Source</legend>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="upload-source"
            value="file" />
          File
        </label>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="upload-source"
            value="text" />
          Paste text
        </label>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="upload-source"
            value="published" />
          Published diagram
        </label>
        <label class="builder-choice">
          <input
            v-model="form.source"
            type="radio"
            name="upload-source"
            value="legacy" />
          {{ LEGACY_SOURCE }}
        </label>
      </fieldset>

      <div v-if="form.source === 'file'" class="builder-field">
        <label for="upload-file">Builder document file</label>
        <input
          id="upload-file"
          type="file"
          accept=".json,.yaml,.yml"
          :aria-invalid="invalid('file')"
          :aria-describedby="describedBy('file', 'upload-file-hint')"
          data-testid="upload-file"
          @change="onFile" />
        <p id="upload-file-hint" class="builder-hint">
          JSON or YAML, up to 5 MiB.
        </p>
      </div>

      <div v-else-if="form.source === 'text'" class="builder-field">
        <label for="upload-text">Document text (JSON or YAML)</label>
        <textarea
          id="upload-text"
          v-model="form.text"
          rows="10"
          :aria-invalid="invalid('text')"
          :aria-describedby="describedBy('text')"
          data-testid="upload-text"></textarea>
      </div>

      <div v-else-if="form.source === 'legacy'" class="builder-field">
        <label for="upload-legacy-file">Legacy diagram or Topology file</label>
        <input
          id="upload-legacy-file"
          type="file"
          accept=".xml,.json,.yaml,.yml"
          :aria-invalid="invalid('legacy')"
          :aria-describedby="describedBy('legacy', 'upload-legacy-file-hint')"
          data-testid="upload-legacy-file"
          @change="onLegacyFile" />
        <p id="upload-legacy-file-hint" class="builder-hint">
          A diagram saved by the legacy Builder (XML), or a Topology config that
          has the builder-xml annotation (YAML or JSON). Up to 5 MiB.
        </p>
        <p class="builder-hint">
          The diagram is converted into a new draft. The file and any stored
          topology are not changed.
        </p>
      </div>

      <div v-else class="builder-field">
        <label for="upload-published">Published diagram</label>
        <select
          id="upload-published"
          v-model="form.documentId"
          :aria-invalid="invalid('published')"
          :aria-describedby="describedBy('published')"
          data-testid="upload-published">
          <option value="">Select a diagram</option>
          <option v-for="doc in documents" :key="doc.id" :value="doc.id">
            {{ doc.name || doc.target || doc.id
            }}{{ doc.source === 'file' ? ' (File)' : '' }}
          </option>
        </select>
      </div>

      <p
        id="upload-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="upload-error">{{
          error.text
        }}</span>
      </p>

      <div class="builder-dialog__actions">
        <button type="button" class="builder-button" @click="$emit('close')">
          Cancel
        </button>
        <!-- A published diagram is on the server already: it is opened. A
             busy button keeps focus, so it is not disabled. -->
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="upload-submit"
          :aria-disabled="busy ? 'true' : undefined">
          {{ submitLabel }}
        </button>
      </div>
    </form>

    <p
      class="builder-dialog__message builder-visually-hidden"
      role="status"
      data-testid="upload-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>
  </builder-dialog>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    reactive,
    ref,
    watch,
  } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import ImportWarnings from './ImportWarnings.vue';
  import PackageImport from './PackageImport.vue';
  import {
    LEGACY_SOURCE,
    fileBaseName,
    legacyDiagramHint,
    parseErrorText,
    readChosenFile,
    useFieldError,
    useMessage,
  } from './message.js';

  import { count, describeImport, listOf } from '@/builder/announce.js';
  import { parseImport, tooLargeText } from '@/builder/decode.js';
  import { iconLibrary, ingestIcons } from '@/builder/iconLibrary.js';
  import {
    configNoun,
    createTickedConfigs,
    decodePackage,
    isPackageValue,
    readDependencies,
  } from '@/builder/package.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { hasControlCharacters } from '@/builder/text.js';

  const emit = defineEmits(['close', 'uploaded']);

  const TOO_LARGE = tooLargeText('file');

  const store = useBuilderStore();
  // Errors are about the control of the chosen source.
  const { error, invalid, describedBy, fail } = useFieldError('upload-error', {
    file: 'upload-file',
    text: 'upload-text',
    published: 'upload-published',
    legacy: 'upload-legacy-file',
  });
  // What a legacy conversion is doing, and how many warnings it gave.
  const status = useMessage();
  const busy = ref(false);
  const warnings = ref([]);
  const warningsView = ref(null);
  // An uploaded package, what the server says its diagram needs, and the
  // name of its file, while that list is shown (see PackageImport).
  const packageView = ref(null);
  const packageImportView = ref(null);
  // A conversion, or an uploaded document whose custom icons gave warnings
  // (see ingestIcons), held until the user continues; pendingKind says
  // which: 'legacy' or 'upload'.
  let pending = null;
  const pendingKind = ref('');

  // Whether the dialog was closed. What a submit was still waiting for then
  // changes nothing: the editor keeps the draft it has open.
  let closed = false;
  onBeforeUnmount(() => {
    closed = true;
  });

  // Whether Continue is creating a package's configs and adding its icons:
  // writes to the server that closing could not stop, so the dialog stays
  // open until they are done and says what came of them.
  const creating = ref(false);

  function requestClose() {
    if (creating.value) {
      return;
    }

    emit('close');
  }

  // `file` is the chosen file's state: '' (none), 'read' or 'too-large',
  // and `fileName` the name of the file read.
  const form = reactive({
    source: 'file',
    file: '',
    fileName: '',
    text: '',
    documentId: '',
  });

  // The file chosen for a legacy conversion: `state` as `form.file`, its
  // name and its text.
  const legacy = reactive({ state: '', name: '', text: '' });
  // Counts each time that choice is made anew, so a conversion knows
  // whether the file it was sent for is still the one chosen.
  let legacyChoice = 0;

  const documents = computed(() => store.documents);

  const submitLabel = computed(() => {
    if (form.source === 'published') {
      return 'Open';
    }

    if (form.source === 'legacy') {
      return busy.value ? 'Converting…' : 'Convert';
    }

    return 'Upload';
  });

  // Before the user continues, nothing has been made into a draft.
  const warningSummary = computed(
    () =>
      `This ${pendingKind.value === 'upload' ? 'upload' : 'conversion'} has ${count(warnings.value.length, 'warning')}.`,
  );

  // The read of the chosen file, which a submit waits for.
  let pendingRead = Promise.resolve();

  // An error about one source says nothing about another. The legacy file
  // field is drawn anew, empty, each time its source is chosen.
  watch(
    () => form.source,
    () => {
      error.clear();
      legacyChoice += 1;
      Object.assign(legacy, { state: '', name: '', text: '' });
    },
  );

  async function onFile(event) {
    error.clear();
    form.text = '';
    form.file = '';
    form.fileName = '';
    pendingRead = readChosenFile(event, 'file');

    const read = await pendingRead;

    if (read?.error) {
      form.file = 'too-large';
      error.set(read.error, 'file');
    } else if (read) {
      form.text = read.text;
      form.file = 'read';
      form.fileName = read.name || '';
    }
  }

  async function onLegacyFile(event) {
    error.clear();
    legacyChoice += 1;
    Object.assign(legacy, { state: '', name: '', text: '' });
    pendingRead = readChosenFile(event, 'file');

    const read = await pendingRead;

    if (read?.error) {
      legacy.state = 'too-large';
      error.set(read.error, 'legacy');
    } else if (read) {
      Object.assign(legacy, {
        state: 'read',
        name: read.name || '',
        text: read.text,
      });
    }
  }

  // Loads a converted diagram and has its draft made. As for any upload,
  // the current draft is detached first, so a later edit never overwrites
  // it. The conversion is announced once the draft exists.
  function openConverted(result) {
    store.newDocument({ name: result.document.metadata?.name });

    const opened = store.setDocument(result.document, {
      label: 'Converted diagram',
      announce: false,
    });

    if (!opened) {
      warnings.value = [];
      status.clear();
      error.set(store.error);

      return;
    }

    emit('uploaded', {
      document: opened,
      source: result.source,
      sourceFile: legacy.name,
      sourceToken: result.sourceToken,
      announcement: `${describeImport(result.warnings, { legacy: true })} Draft created.`,
    });
    emit('close');
  }

  async function convert() {
    await pendingRead;

    if (closed) {
      return;
    }

    if (legacy.state !== 'read') {
      fail(
        legacy.state === 'too-large' ? TOO_LARGE : 'Choose a file to convert.',
        'legacy',
      );

      return;
    }

    status.set('Converting…');

    // The diagram is named after its file. The server names it when the
    // file's name is one it would refuse.
    const name = fileBaseName(legacy.name);
    const asked = legacyChoice;
    const errors = store.errorSeq;
    const result = await store.convertLegacy({
      content: legacy.text,
      name: hasControlCharacters(name) ? '' : name,
    });

    // Nobody waits for this answer any more: the dialog was closed, or
    // another source or file was chosen meanwhile. It is dropped, so the
    // open draft stays as it is, and a failure of it is not reported.
    if (closed || asked !== legacyChoice) {
      if (!result && store.errorSeq === errors + 1) {
        store.clearError();
      }

      status.clear();

      return;
    }

    if (!result) {
      status.clear();
      // A refused file is reported on the field that chose it.
      fail(
        store.error || 'Could not convert the legacy diagram.',
        store.errorField === 'content' ? 'legacy' : '',
      );

      return;
    }

    if (!result.warnings.length) {
      status.clear();
      openConverted(result);

      return;
    }

    await hold('legacy', result, result.warnings);
  }

  // Shows the warnings in place of the form, and keeps what they are about
  // until the user continues.
  async function hold(kind, held, found) {
    pending = held;
    pendingKind.value = kind;
    warnings.value = found;
    status.set(warningSummary.value);

    // The warnings replace the form, and the button that had focus with it.
    await nextTick();
    warningsView.value?.continueButton?.focus();
  }

  // Makes the draft once the user has seen the warnings.
  function proceed() {
    if (pendingKind.value === 'upload') {
      openUploaded(pending);
    } else {
      openConverted(pending);
    }
  }

  // Loads an uploaded document and has its draft made. Upload always starts
  // a new draft: the current draft is detached before the uploaded content
  // is loaded, so a later edit can never overwrite that draft. The configs
  // a package's upload created (`created`, as the dialog names them) are
  // announced with it.
  function openUploaded({ document, sourceFile, created = [] }) {
    store.newDocument({ name: document.metadata?.name });

    const opened = store.setDocument(document, {
      label: 'Uploaded diagram',
    });

    if (!opened) {
      warnings.value = [];
      packageView.value = null;
      status.clear();
      error.set(store.error);

      return;
    }

    if (created.length) {
      store.announce(`Created ${listOf(created)} on this server.`);
    }

    // Only a chosen file has a name to record.
    emit('uploaded', sourceFile ? { sourceFile } : {});
    emit('close');
  }

  // Reads an uploaded package and asks the server which of what its
  // diagram needs it has; that list then replaces the form.
  async function openPackage(value) {
    let pkg;

    try {
      pkg = decodePackage(value);
    } catch (err) {
      fail(
        `This Builder package cannot be opened. ${err.message}`,
        form.source,
      );

      return;
    }

    const sourceFile = form.source === 'file' ? form.fileName : '';
    let dependencies;

    busy.value = true;
    status.set('Checking what the package needs on this server…');

    try {
      dependencies = readDependencies(await store.resolvePackage(pkg));
    } catch (err) {
      if (!closed) {
        status.clear();
        fail(store.describeError(err, 'check the package'), '');
      }

      return;
    } finally {
      busy.value = false;
    }

    if (closed) {
      return;
    }

    status.set(`The diagram needs ${count(dependencies.length, 'item')}.`);
    packageView.value = { pkg, dependencies, sourceFile };

    // The list replaces the form, and the button that had focus with it.
    await nextTick();
    packageImportView.value?.focusFirst();
  }

  // Creates the configs of the package the user ticked, puts the custom
  // icons its diagram carries into the icon library, then opens the
  // diagram as a new draft. What could not be created or added is shown
  // first; the configs created stay whatever the user does next.
  async function continuePackage(ticked) {
    if (busy.value || !packageView.value) {
      return;
    }

    const { pkg, sourceFile } = packageView.value;
    const found = [];
    const upload = { document: pkg.document, sourceFile, created: [] };

    busy.value = true;
    creating.value = true;
    status.set(
      ticked.length
        ? `Creating ${count(ticked.length, 'config')} on this server…`
        : 'Opening the diagram…',
    );

    try {
      const create = (config) => store.createPackagedConfig(config);
      const result = await createTickedConfigs(pkg, ticked, create);

      upload.created = result.created.map(
        (dependency) => `${configNoun(dependency.kind)} ${dependency.name}`,
      );
      result.failed.forEach(({ dependency, error: failure }) => {
        found.push(
          store.describeError(
            failure,
            `create ${configNoun(dependency.kind)} ${dependency.name}`,
          ),
        );
      });

      if (carriesIcons(pkg.document)) {
        const ingested = await ingestIcons(pkg.document, iconLibrary);

        upload.document = ingested.doc;
        found.push(...ingested.warnings);
      }
    } finally {
      busy.value = false;
      creating.value = false;
      status.clear();
    }

    if (closed) {
      return;
    }

    if (found.length) {
      packageView.value = null;
      await hold('upload', upload, found);

      return;
    }

    openUploaded(upload);
  }

  async function submit() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    error.clear();

    if (form.source === 'legacy') {
      busy.value = true;

      try {
        await convert();
      } finally {
        busy.value = false;
      }

      return;
    }

    await pendingRead;

    if (closed) {
      return;
    }

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

      emit('uploaded', { draftCreated: true });
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

    // A Builder package names its own schema; anything else is read as a
    // Builder document.
    const raw = parseImport(form.text, { as: 'raw' });

    if (raw.ok && isPackageValue(raw.value)) {
      await openPackage(raw.value);

      return;
    }

    const result = parseImport(form.text);

    if (!result.ok) {
      // XML is no Builder document; the legacy source converts it.
      fail(
        (result.code !== 'too-large' && legacyDiagramHint(form.text)) ||
          parseErrorText(result.error),
        form.source,
      );

      return;
    }

    // The custom icons the file carries go to the server's icon library,
    // so the draft carries only those the server cannot take as they are
    // (see ingestIcons).
    const upload = {
      document: result.document,
      sourceFile: form.source === 'file' ? form.fileName : '',
    };
    let found = [];

    if (carriesIcons(result.document)) {
      busy.value = true;
      status.set('Adding the custom icons of the diagram to the server…');

      try {
        const ingested = await ingestIcons(result.document, iconLibrary);

        upload.document = ingested.doc;
        found = ingested.warnings;
      } finally {
        busy.value = false;
        status.clear();
      }

      if (closed) {
        return;
      }
    }

    if (found.length) {
      await hold('upload', upload, found);

      return;
    }

    openUploaded(upload);
  }

  // Whether a document carries copies of custom icons.
  function carriesIcons(document) {
    const icons = document?.icons;

    return (
      Boolean(icons) &&
      typeof icons === 'object' &&
      !Array.isArray(icons) &&
      Object.keys(icons).length > 0
    );
  }
</script>
