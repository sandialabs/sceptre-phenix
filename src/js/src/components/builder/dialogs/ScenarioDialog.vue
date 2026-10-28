<!-- The diagram's scenarios: the server's Scenario configs that Publish adds
     the topology to. Save writes the list as one undo step. -->
<template>
  <builder-dialog
    title="Scenarios"
    title-id="scenario-dialog-title"
    @close="$emit('close')">
    <form novalidate @submit.prevent="submit">
      <p class="builder-hint">
        Publishing adds this diagram's topology to each scenario selected here.
        An experiment published with the topology uses one of them, which the
        Publish dialog picks.
      </p>

      <fieldset class="builder-field">
        <legend>Scenarios of this diagram</legend>
        <p v-if="store.readOnly" class="builder-hint">
          The selected scenarios are the ones that this diagram uses.
        </p>

        <template v-if="rows.length > SCENARIO_FILTER_MIN">
          <label for="scenario-filter">Filter scenarios</label>
          <input
            id="scenario-filter"
            v-model="filter"
            type="search"
            autocomplete="off"
            spellcheck="false"
            aria-describedby="scenario-filter-count"
            data-testid="scenario-filter"
            @keydown.enter.prevent />
          <p
            id="scenario-filter-count"
            class="builder-hint"
            data-testid="scenario-filter-count">
            {{ shownText }}
          </p>
        </template>

        <ul v-if="shown.length" class="scenario-list">
          <li
            v-for="(row, index) in shown"
            :key="row.name"
            class="scenario-list__row"
            :data-testid="`scenario-row-${row.name}`">
            <input
              :id="`scenario-check-${index}`"
              type="checkbox"
              :checked="row.checked"
              :disabled="store.readOnly || (full && !row.checked)"
              :aria-describedby="checkDescription(row, index)"
              :data-testid="`scenario-check-${row.name}`"
              @change="toggle(row, $event.target.checked)" />
            <label :for="`scenario-check-${index}`" class="scenario-list__name">
              {{ row.name }}
            </label>
            <span
              v-if="row.missing"
              :id="`scenario-missing-${index}`"
              class="scenario-list__missing"
              data-testid="scenario-missing">
              Not found on the server
            </span>
            <span v-else class="scenario-list__actions">
              <builder-action-button
                action="view"
                :busy="isPending('view', row.name)"
                :aria-label="
                  isPending('view', row.name)
                    ? `Loading ${row.name}`
                    : undefined
                "
                :disabled="Boolean(pending)"
                :data-testid="`scenario-view-${row.name}`"
                @click="openScenario(row.name, 'view')">
                View<span class="builder-visually-hidden"> {{ row.name }}</span>
              </builder-action-button>
              <builder-action-button
                v-if="!store.readOnly"
                action="edit"
                :busy="isPending('edit', row.name)"
                :aria-label="
                  isPending('edit', row.name)
                    ? `Loading ${row.name}`
                    : undefined
                "
                :disabled="Boolean(pending)"
                :data-testid="`scenario-edit-${row.name}`"
                @click="openScenario(row.name, 'edit')">
                Edit<span class="builder-visually-hidden"> {{ row.name }}</span>
              </builder-action-button>
            </span>
          </li>
        </ul>
        <p v-else-if="rows.length">
          No scenario name contains "{{ filter.trim() }}".
        </p>
        <p v-else data-testid="scenario-none">
          There are no scenarios that your role can list.
        </p>

        <p class="builder-hint" data-testid="scenario-count">
          {{ count(names.length, 'scenario') }} selected.
        </p>
        <p
          v-if="full"
          id="scenario-limit"
          class="builder-hint"
          data-testid="scenario-limit">
          {{ limitText() }}
        </p>

        <builder-busy-button
          v-if="!store.readOnly"
          class="scenario-create"
          icon="plus"
          :busy="isPending('create')"
          busy-label="Loading the new scenario"
          :disabled="full || Boolean(pending)"
          :aria-describedby="full ? 'scenario-limit' : undefined"
          data-testid="scenario-create"
          @click="createNew">
          Create new scenario
        </builder-busy-button>
      </fieldset>

      <fieldset v-if="!store.readOnly" class="builder-field">
        <legend id="scenario-file-legend">
          Upload a scenario (YAML or JSON)
        </legend>
        <input
          id="scenario-file"
          ref="fileInput"
          type="file"
          accept=".json,.yaml,.yml"
          aria-labelledby="scenario-file-legend"
          :aria-invalid="invalid('file')"
          :aria-describedby="describedBy('file')"
          data-testid="scenario-file"
          @change="onFile" />

        <template v-if="upload.config">
          <label for="scenario-upload-name">Scenario name</label>
          <input
            id="scenario-upload-name"
            v-model="upload.name"
            type="text"
            :aria-invalid="invalid('uploadName')"
            :aria-describedby="
              describedBy('uploadName', 'scenario-upload-hint')
            "
            data-testid="scenario-upload-name"
            @keydown.enter.prevent="storeUpload" />
          <p
            id="scenario-upload-hint"
            class="builder-hint"
            :class="{ 'builder-hint--warning': replaces }"
            data-testid="scenario-upload-hint">
            <builder-icon v-if="replaces" name="warning" :size="14" />
            <span>{{ uploadHint }}</span>
          </p>
          <builder-busy-button
            :busy="busy"
            :busy-label="`Storing scenario ${upload.name.trim()}`"
            :disabled="full"
            data-testid="scenario-upload"
            @click="storeUpload">
            Store and add
          </builder-busy-button>
        </template>
      </fieldset>

      <p
        id="scenario-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="scenario-error">{{
          error.text
        }}</span>
      </p>

      <p class="builder-dialog__message" role="status">
        <span
          v-if="status.text"
          :key="status.key"
          data-testid="scenario-status"
          >{{ status.text }}</span
        >
      </p>

      <div class="builder-dialog__actions">
        <button type="button" class="builder-button" @click="$emit('close')">
          {{ store.readOnly ? 'Close' : 'Cancel' }}
        </button>
        <button
          v-if="!store.readOnly"
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="scenario-submit"
          :aria-disabled="busy ? 'true' : undefined">
          Save scenarios
        </button>
      </div>
    </form>

    <builder-confirm
      v-if="confirming"
      id="scenario-confirm"
      :title="confirming.title"
      :message="confirming.message"
      :confirm-label="confirming.confirmLabel"
      @confirm="confirmReplace"
      @cancel="confirming = null" />

    <scenario-editor-dialog
      v-if="editor"
      :mode="editor.mode"
      :name="editor.name"
      :text="editor.text"
      :save="saveEdited"
      @close="editor = null" />
  </builder-dialog>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    reactive,
    ref,
  } from 'vue';

  import BuilderActionButton from '../BuilderActionButton.vue';
  import BuilderBusyButton from '../BuilderBusyButton.vue';
  import BuilderConfirm from '../BuilderConfirm.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import ScenarioEditorDialog from './ScenarioEditorDialog.vue';
  import {
    fileBaseName,
    parseErrorText,
    readChosenFile,
    useFieldError,
    useMessage,
  } from './message.js';

  import { count } from '@/builder/announce.js';
  import { sentence, serverReason } from '@/builder/api.js';
  import { parseImport } from '@/builder/decode.js';
  import { documentScenarios } from '@/builder/model.js';
  import { configName, scenarioNames } from '@/builder/publish.js';
  import {
    SCENARIO_FILTER_MIN,
    filterScenarioRows,
    newScenarioConfig,
    scenarioErrorText,
    scenarioKey,
    scenarioRows,
    scenarioText,
    toggleScenario,
  } from '@/builder/scenarios.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    MAX_SCENARIOS,
    SCENARIO_API_VERSION,
    scenarioNameProblem,
  } from '@/builder/validate.js';
  import { isPlainObject } from '@/builder/values.js';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  const { error, invalid, describedBy, fail } = useFieldError(
    'scenario-error',
    {
      file: 'scenario-file',
      uploadName: 'scenario-upload-name',
    },
  );
  const status = useMessage();
  const busy = ref(false);
  const fileInput = ref(null);
  // The confirmation shown before a stored scenario is replaced, with the
  // config it stores.
  const confirming = ref(null);

  // The list being edited, which Save writes into the diagram.
  const names = ref([...documentScenarios(store.doc)]);
  // The list when the dialog opened: a scenario of it that the server does
  // not list keeps its row when its box is cleared.
  const opened = [...names.value];

  const filter = ref('');

  // The request that View, Edit or Create new scenario started, while it
  // runs: {action, name}.
  const pending = ref(null);
  // The scenario editor, while it is open: {mode, name, text}.
  const editor = ref(null);
  // Whether the dialog has closed, which a request that answers later finds
  // out.
  let closed = false;

  // The scenario file read, and the name it is stored under.
  const upload = reactive({ config: null, name: '' });

  onMounted(() => {
    // Scenarios may have been stored since the list was read.
    if (!store.readOnly) {
      store.fetchSources();
    }
  });

  onBeforeUnmount(() => {
    closed = true;
  });

  // Names compare as the server compares them in a document: ignoring case.
  const listed = (name) =>
    names.value.some((item) => scenarioKey(item) === scenarioKey(name));

  const full = computed(() => names.value.length >= MAX_SCENARIOS);

  // The scenarios the server lists for this user.
  const storedNames = computed(() => scenarioNames(store.sources.scenarios));

  const rows = computed(() =>
    scenarioRows(names.value, storedNames.value, opened),
  );
  const shown = computed(() => filterScenarioRows(rows.value, filter.value));
  const shownText = computed(() =>
    shown.value.length === rows.value.length
      ? `Showing all ${count(rows.value.length, 'scenario')}.`
      : `Showing ${shown.value.length} of ${count(rows.value.length, 'scenario')}.`,
  );

  // Whether storing the upload replaces a scenario the server has.
  const replaces = computed(() =>
    storedNames.value.includes(upload.name.trim()),
  );

  const uploadHint = computed(() =>
    replaces.value
      ? `The server has a scenario named ${upload.name.trim()}. Storing replaces its spec and keeps its annotations.`
      : 'Storing creates a new scenario on the server.',
  );

  function limitText() {
    return `A diagram lists at most ${MAX_SCENARIOS} scenarios. To select another, clear one first.`;
  }

  // What a box says after its name: that the server does not list the
  // scenario, or that the list is full.
  function checkDescription(row, index) {
    const ids = [
      row.missing && `scenario-missing-${index}`,
      full.value && !row.checked && 'scenario-limit',
    ].filter(Boolean);

    return ids.join(' ') || undefined;
  }

  function toggle(row, checked) {
    error.clear();

    if (checked && full.value && !listed(row.name)) {
      fail(limitText());

      return;
    }

    names.value = toggleScenario(names.value, row.name, checked);
  }

  function isPending(action, name = '') {
    return pending.value?.action === action && pending.value.name === name;
  }

  // View and Edit: reads the stored config and opens the editor with it.
  async function openScenario(name, mode) {
    if (pending.value) {
      return;
    }

    error.clear();
    pending.value = { action: mode, name };
    status.set(`Loading scenario ${name}…`);

    try {
      const config = await store.readScenarioConfig(name);

      if (closed) {
        return;
      }

      status.clear();
      editor.value = {
        mode,
        name,
        text: scenarioText(config, { editable: mode === 'edit' }),
      };
    } catch (err) {
      if (closed) {
        return;
      }

      status.clear();
      error.set(scenarioErrorText(err, `read scenario ${name}`));
    } finally {
      pending.value = null;
    }
  }

  // Create new scenario: reads the scenarios again, so that the new name is
  // one the server does not have, and opens the editor with a new config.
  async function createNew() {
    if (pending.value) {
      return;
    }

    error.clear();

    if (full.value) {
      fail(limitText());

      return;
    }

    pending.value = { action: 'create', name: '' };
    status.set('Loading the scenario names…');

    try {
      await store.fetchSources();
    } finally {
      pending.value = null;
    }

    if (closed) {
      return;
    }

    status.clear();

    const config = newScenarioConfig(store.doc, [
      ...storedNames.value,
      ...names.value,
    ]);

    editor.value = {
      mode: 'create',
      name: config.metadata.name,
      text: scenarioText(config, { editable: true }),
    };
  }

  // The editor's Save. A refusal is thrown to the editor, which shows it.
  // A scenario created is selected.
  async function saveEdited(config) {
    const mode = editor.value?.mode;
    const { name } = config.metadata;

    await store.saveScenarioConfig(config, {
      replace: mode === 'edit',
      exact: true,
    });

    editor.value = null;

    if (mode === 'create') {
      status.set(`Created scenario ${name} ${listStored(name)}`);
    } else {
      status.set(`Saved scenario ${name} on the server.`);
    }
  }

  // A scenario at another apiVersion, such as a v1 one, is refused: the
  // Builder reads the apps of this version only.
  function unsupportedVersion(apiVersion) {
    return (
      `This scenario is ${apiVersion}. The Builder stores only ` +
      `${SCENARIO_API_VERSION} scenarios. Upgrade it to ` +
      `${SCENARIO_API_VERSION}. Then upload it again.`
    );
  }

  async function onFile(event) {
    const file = event.target.files?.[0];
    // Even no file ends the read of a file chosen before.
    const reading = readChosenFile(event, 'scenario');

    error.clear();
    upload.config = null;
    upload.name = '';

    if (!file) {
      return;
    }

    const read = await reading;

    // A file chosen while this one was read replaces it.
    if (!read) {
      return;
    }

    if (read.error) {
      error.set(read.error, 'file');

      return;
    }

    const parsed = parseImport(read.text, {
      as: 'raw',
      expectedKind: 'Scenario',
    });

    if (!parsed.ok) {
      error.set(parseErrorText(parsed.error), 'file');

      return;
    }

    const config = parsed.value || {};

    if (
      !config.apiVersion ||
      !config.spec ||
      typeof config.spec !== 'object' ||
      Array.isArray(config.spec)
    ) {
      error.set(
        'The uploaded Scenario must include apiVersion and an object spec.',
        'file',
      );

      return;
    }

    if (config.apiVersion !== SCENARIO_API_VERSION) {
      error.set(unsupportedVersion(config.apiVersion), 'file');

      return;
    }

    const named = config.metadata?.name;

    upload.config = config;
    upload.name =
      typeof named === 'string' && !scenarioNameProblem(named)
        ? named
        : configName(fileBaseName(read.name));
  }

  // The Scenario config an upload stores: its apiVersion and spec, and the
  // annotations the file gives it, under the name chosen.
  function uploadedConfig(name) {
    const { apiVersion, spec, metadata } = upload.config;
    const annotations = metadata?.annotations;

    return {
      apiVersion,
      kind: 'Scenario',
      metadata: isPlainObject(annotations) ? { name, annotations } : { name },
      spec,
    };
  }

  function storeUpload() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    error.clear();

    if (!upload.config) {
      fail('Choose a scenario file to upload.', 'file');

      return;
    }

    const name = upload.name.trim();
    const problem = scenarioNameProblem(name);

    if (problem) {
      fail(sentence(problem), 'uploadName');

      return;
    }

    if (full.value && !listed(name)) {
      fail(limitText(), 'uploadName');

      return;
    }

    const config = uploadedConfig(name);

    if (replaces.value) {
      confirming.value = {
        title: `Replace scenario ${name}?`,
        message:
          `Storing this file replaces the spec of the scenario "${name}" on ` +
          'the server, for every diagram and experiment that uses it. The ' +
          'scenario keeps its annotations and also gets those of the file. ' +
          'Thus its topology annotation keeps every topology name that it ' +
          'has. You cannot undo this.',
        confirmLabel: 'Replace scenario',
        config,
      };

      return;
    }

    save(config, false);
  }

  // The confirmation closes, which returns focus to the form, before the
  // scenario it was shown for is stored.
  function confirmReplace() {
    if (!confirming.value || busy.value) {
      return;
    }

    const { config } = confirming.value;

    confirming.value = null;
    save(config, true);
  }

  // Selects a scenario just stored as `name`, and returns the end of the
  // status that says what the list did. A name the list has in another
  // letter case takes the stored config's spelling, which is the config the
  // diagram names from then on. The list never has it twice.
  function listStored(name) {
    const keep = 'Save scenarios to keep the list in this diagram.';
    const index = names.value.findIndex(
      (item) => scenarioKey(item) === scenarioKey(name),
    );

    if (index < 0) {
      names.value.push(name);

      return `on the server and added it to the list. ${keep}`;
    }

    const before = names.value[index];

    if (before === name) {
      return 'on the server. The list already names it.';
    }

    names.value.splice(index, 1, name);

    return `on the server, and the list names it ${name} in place of ${before}. ${keep}`;
  }

  async function save(config, replace) {
    const { name } = config.metadata;

    busy.value = true;
    status.set(`${replace ? 'Replacing' : 'Storing'} scenario ${name}…`);

    try {
      await store.saveScenarioConfig(config, { replace });
    } catch (err) {
      // The server's own words say what it refused: the permission, the
      // config, or a name taken since the list was read.
      const reason = serverReason(err);

      status.clear();
      fail(
        reason
          ? `Could not store scenario ${name}. ${reason}`
          : store.describeError(err, `store scenario ${name}`),
        'uploadName',
      );

      return;
    } finally {
      busy.value = false;
    }

    const listing = listStored(name);

    upload.config = null;
    upload.name = '';

    if (fileInput.value) {
      fileInput.value.value = '';
    }

    status.set(
      `${replace ? 'Replaced' : 'Stored'} scenario ${name} ${listing}`,
    );

    // The name field and its button are gone: focus goes back to the file
    // field, which chose the scenario.
    await nextTick();
    fileInput.value?.focus();
  }

  function submit() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    store.setScenarios(names.value);
    emit('close');
  }
</script>

<style scoped>
  fieldset {
    min-width: 0;
    border: 0;
    padding: 0;
  }

  legend {
    margin-bottom: 0.2rem;
    font-weight: 700;
  }

  /* A long list scrolls inside the dialog, so that the buttons under it
     stay near. */
  .scenario-list {
    max-height: min(40vh, 20rem);
    margin: 0 0 0.5rem;
    padding: 0.25rem;
    overflow-y: auto;
    list-style: none;
    border: 1px solid var(--bx-border);
    border-radius: var(--bx-radius);
  }

  /* Rows are at least 2rem high, so that their small boxes sit at least
     24px apart (WCAG 2.5.8). */
  .scenario-list__row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem 0.5rem;
    min-height: 2rem;
    padding: 0.125rem 0.25rem;
  }

  .scenario-list__row + .scenario-list__row {
    border-top: 1px solid var(--bx-border);
  }

  .scenario-list__row input[type='checkbox'] {
    flex: none;
    margin: 0;
  }

  /* A config name is one word. A long one wraps, so that it does not
     widen the dialog. */
  .builder-field .scenario-list__name {
    flex: 1 1 4rem;
    min-width: 0;
    margin: 0;
    font-size: inherit;
    font-weight: 400;
    overflow-wrap: anywhere;
  }

  .scenario-list__missing {
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }

  .scenario-list__actions {
    display: flex;
    flex: none;
    gap: 0.35rem;
    margin-left: auto;
  }

  .scenario-create {
    margin-bottom: 0.25rem;
  }
</style>
