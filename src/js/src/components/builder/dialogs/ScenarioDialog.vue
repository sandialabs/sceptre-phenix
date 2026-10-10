<!--
  Scenarios of the diagram.

  A document names the Scenario configs of the server it is used with
  (`scenarios`) and holds none of their content. Publish adds the topology
  to the topology annotation of each, and an experiment published with the
  topology uses one of them. This dialog edits that list:

  - Remove removes a name from it.
  - Add a stored scenario adds one that the server lists (GET
    /builder/sources).
  - Upload a scenario file stores a Scenario config on the server and adds
    its name.

  Save writes the list into the diagram as one undo step (the store's
  setScenarios). Cancel and Escape leave it as it was, but a scenario
  stored meanwhile stays on the server.

  An upload is a phenix.sandia.gov/v2 Scenario file (JSON or YAML). Its
  name is the file's metadata.name, or the file name made into a config
  name. The user can change the name before it is stored. POST /configs
  creates it. A name that the server already lists is replaced with PUT
  /configs/Scenario/<name>, but only after the user confirms, because
  nothing undoes it. A replacement takes the file's spec and keeps the
  stored scenario's annotations, with the file's annotations added to them.
  Thus its topology annotation still names every topology that it named
  (the store's saveScenarioConfig). The server checks the caller's configs
  permission and the config, and its refusal shows in the dialog's alert. A
  name that the list already has, in any letter case, is not listed twice.
  The list takes the stored config's spelling.

  A read-only draft shows the list only.
-->
<template>
  <builder-dialog
    title="Scenarios"
    title-id="scenario-dialog-title"
    @close="$emit('close')">
    <form novalidate @submit.prevent="submit">
      <p class="builder-hint">
        Publishing adds this diagram's topology to each scenario listed here. An
        experiment published with the topology uses one of them, which the
        Publish dialog picks.
      </p>

      <div class="builder-field">
        <h3 id="scenario-list-heading">Scenarios of this diagram</h3>
        <ul
          v-if="names.length"
          class="scenario-list"
          aria-labelledby="scenario-list-heading"
          data-testid="scenario-list">
          <li
            v-for="(name, index) in names"
            :key="name"
            class="scenario-list__row"
            :data-testid="`scenario-row-${index + 1}`">
            <span class="scenario-list__name">{{ name }}</span>
            <button
              v-if="!store.readOnly"
              :ref="(element) => setRemoveButton(name, element)"
              type="button"
              class="builder-button"
              :data-testid="`scenario-remove-${index + 1}`"
              @click="remove(index)">
              Remove<span class="builder-visually-hidden">
                scenario {{ name }}</span
              >
            </button>
          </li>
        </ul>
        <p v-else data-testid="scenario-none">No scenarios.</p>
        <p v-if="full" class="builder-hint" data-testid="scenario-limit">
          A diagram lists at most {{ MAX_SCENARIOS }} scenarios.
        </p>
      </div>

      <template v-if="!store.readOnly">
        <div class="builder-field">
          <label for="scenario-name">Add a stored scenario</label>
          <div class="scenario-add">
            <select
              id="scenario-name"
              ref="storedSelect"
              v-model="form.stored"
              :aria-invalid="invalid('stored')"
              :aria-describedby="describedBy('stored')"
              data-testid="scenario-name">
              <option value="">Choose a scenario</option>
              <option v-for="name in available" :key="name" :value="name">
                {{ name }}
              </option>
            </select>
            <button
              type="button"
              class="builder-button"
              data-testid="scenario-add"
              :aria-disabled="full || undefined"
              @click="addStored">
              Add
            </button>
          </div>
          <p v-if="!storedNames.length" data-testid="scenario-empty">
            This phenix instance has no scenario configs your role can list.
          </p>
        </div>

        <fieldset class="builder-field">
          <legend>Upload a scenario file</legend>
          <label for="scenario-file">Scenario config file (JSON or YAML)</label>
          <input
            id="scenario-file"
            ref="fileInput"
            type="file"
            accept=".json,.yaml,.yml"
            :aria-invalid="invalid('file')"
            :aria-describedby="describedBy('file', 'scenario-file-hint')"
            data-testid="scenario-file"
            @change="onFile" />
          <p id="scenario-file-hint" class="builder-hint">
            A {{ SCENARIO_API_VERSION }} Scenario of up to 5 MiB. It is stored
            on the server as a Scenario config.
          </p>

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
              data-testid="scenario-upload-name" />
            <p
              id="scenario-upload-hint"
              class="builder-hint"
              :class="{ 'builder-hint--warning': replaces }"
              data-testid="scenario-upload-hint">
              <builder-icon v-if="replaces" name="warning" :size="14" />
              <span>{{ uploadHint }}</span>
            </p>
            <button
              type="button"
              class="builder-button"
              data-testid="scenario-upload"
              :aria-disabled="busy || full || undefined"
              @click="storeUpload">
              {{ busy ? 'Storing…' : 'Store and add' }}
            </button>
          </template>
        </fieldset>
      </template>

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
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, reactive, ref } from 'vue';

  import BuilderConfirm from '../BuilderConfirm.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import {
    fileBaseName,
    parseErrorText,
    readChosenFile,
    useFieldError,
    useMessage,
  } from './message.js';

  import { sentence, serverReason } from '@/builder/api.js';
  import { parseImport } from '@/builder/decode.js';
  import { documentScenarios } from '@/builder/model.js';
  import { configName, scenarioNames } from '@/builder/publish.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    MAX_SCENARIOS,
    SCENARIO_API_VERSION,
    scenarioNameProblem,
  } from '@/builder/validate.js';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  const { error, invalid, describedBy, fail } = useFieldError(
    'scenario-error',
    {
      stored: 'scenario-name',
      file: 'scenario-file',
      uploadName: 'scenario-upload-name',
    },
  );
  const status = useMessage();
  const busy = ref(false);
  const storedSelect = ref(null);
  const fileInput = ref(null);
  // The confirmation shown before a stored scenario is replaced, with the
  // config it stores.
  const confirming = ref(null);

  // The list being edited, which Save writes into the diagram.
  const names = ref([...documentScenarios(store.doc)]);

  const form = reactive({ stored: '' });

  // The scenario file read, and the name it is stored under.
  const upload = reactive({ config: null, name: '' });

  onMounted(() => {
    // Scenarios may have been stored since the list was read.
    if (!store.readOnly) {
      store.fetchSources();
    }
  });

  // Names compare as the server compares them in a document: ignoring case.
  const key = (name) => String(name).toLowerCase();
  const listed = (name) => names.value.some((item) => key(item) === key(name));

  const full = computed(() => names.value.length >= MAX_SCENARIOS);

  // The scenarios the server lists for this user.
  const storedNames = computed(() => scenarioNames(store.sources.scenarios));

  // Those not listed yet, which Add offers.
  const available = computed(() =>
    storedNames.value.filter((name) => !listed(name)),
  );

  // Whether storing the upload replaces a scenario the server has.
  const replaces = computed(() =>
    storedNames.value.includes(upload.name.trim()),
  );

  const uploadHint = computed(() =>
    replaces.value
      ? `The server has a scenario named ${upload.name.trim()}: storing replaces its spec and keeps its annotations.`
      : 'A new scenario will be stored on the server.',
  );

  // The Remove buttons, by scenario name, which focus moves between.
  const removeButtons = new Map();

  function setRemoveButton(name, element) {
    if (element) {
      removeButtons.set(name, element);
    } else {
      removeButtons.delete(name);
    }
  }

  async function remove(index) {
    const [name] = names.value.splice(index, 1);

    error.clear();
    status.set(`Removed scenario ${name} from the list.`);

    // Focus moves to the Remove button now in that place, or else the last
    // one, or else the scenario picker. Thus focus never falls to the page.
    await nextTick();

    const next = names.value[Math.min(index, names.value.length - 1)];

    (next ? removeButtons.get(next) : storedSelect.value)?.focus();
  }

  function limitText() {
    return `A diagram lists at most ${MAX_SCENARIOS} scenarios. Remove one first.`;
  }

  function addStored() {
    error.clear();

    if (full.value) {
      fail(limitText(), 'stored');

      return;
    }

    const name = form.stored;

    if (!name) {
      fail('Choose a scenario to add.', 'stored');

      return;
    }

    names.value.push(name);
    form.stored = '';
    status.set(`Added scenario ${name} to the list.`);
  }

  // A scenario at another apiVersion, such as a v1 one, is refused: the
  // Builder reads the apps of this version only.
  function unsupportedVersion(apiVersion) {
    return (
      `This scenario is ${apiVersion}, and the Builder stores only ` +
      `${SCENARIO_API_VERSION} scenarios. Upgrade it to ` +
      `${SCENARIO_API_VERSION}, then upload it again.`
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
    const object =
      Boolean(annotations) &&
      typeof annotations === 'object' &&
      !Array.isArray(annotations);

    return {
      apiVersion,
      kind: 'Scenario',
      metadata: object ? { name, annotations } : { name },
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
          'the server, for every diagram and experiment that uses it. Its ' +
          'annotations are kept, with those of the file added, so its ' +
          'topology annotation still names every topology it names. This ' +
          'cannot be undone.',
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

  // Lists a scenario just stored as `name`, and returns the end of the
  // status that says what the list did. A name the list has in another
  // letter case takes the stored config's spelling, which is the config the
  // diagram names from then on. The list never has it twice.
  function listStored(name) {
    const keep = 'Save scenarios to keep the list in this diagram.';
    const index = names.value.findIndex((item) => key(item) === key(name));

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
    border: 0;
    padding: 0;
  }

  .scenario-list {
    margin: 0 0 0.5rem;
    padding: 0;
    list-style: none;
  }

  .scenario-list__row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
  }

  .scenario-list__row + .scenario-list__row {
    margin-top: 0.25rem;
  }

  /* A config name is one word. A long one wraps, so that it does not
     widen the dialog. */
  .scenario-list__name {
    overflow-wrap: anywhere;
  }

  .scenario-add {
    display: flex;
    gap: 0.5rem;
  }

  .scenario-add select {
    flex: 1;
    min-width: 0;
  }
</style>
