<!--
  Publish shell.

  Publish is an intent, not an upload: the server loads the snapshot the draft
  cursor points at and re-runs its own checks, so this dialog never sends the
  document. It confirms the queue is drained first (store.publish does that),
  then names the configs to write and reports the per stage result.

  Config names are checked here against the server's naming rule, so a bad
  name is reported on its field before anything is sent. Whether each config
  is created or updated follows the server's list of existing configs, which
  is read again every time the dialog opens. The server lets a draft update
  only a config it was loaded from or published, so it publishes again after
  further edits, and never one the legacy XML Builder owns (see
  updateBlocker), so the hints and the submit button promise an update only
  then, and any other existing name is refused on its field before anything
  is sent.
-->
<template>
  <builder-dialog
    title="Publish diagram"
    title-id="publish-dialog-title"
    @close="$emit('close')">
    <form v-if="!result" novalidate @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>What to publish</legend>
        <label class="builder-choice">
          <input
            v-model="form.mode"
            type="radio"
            name="publish-mode"
            value="topology" />
          Topology only
        </label>
        <label class="builder-choice">
          <input
            v-model="form.mode"
            type="radio"
            name="publish-mode"
            value="topology-experiment"
            data-testid="publish-mode-experiment" />
          Topology and an experiment
        </label>
      </fieldset>

      <div class="builder-field">
        <label for="publish-name">Topology name</label>
        <input
          id="publish-name"
          v-model="form.topologyName"
          type="text"
          required
          list="publish-topology-names"
          :aria-invalid="invalid('topologyName')"
          :aria-describedby="
            describedBy(
              'topologyName',
              'publish-topology-action-hint publish-name-rule',
            )
          "
          data-testid="publish-name" />
        <datalist id="publish-topology-names">
          <option
            v-for="name in topologyNames"
            :key="name"
            :value="name"></option>
        </datalist>
        <p
          id="publish-topology-action-hint"
          class="builder-hint"
          aria-live="polite">
          {{ targetHint('topology', topologyExists, topologyBlocker) }}
        </p>
        <p id="publish-name-rule" class="builder-hint">
          {{ CONFIG_NAME_RULE }}
        </p>
      </div>

      <div v-if="form.mode === 'topology-experiment'" class="builder-field">
        <label for="publish-experiment">Experiment name</label>
        <input
          id="publish-experiment"
          v-model="form.experimentName"
          type="text"
          required
          list="publish-experiment-names"
          :aria-invalid="invalid('experimentName')"
          :aria-describedby="
            describedBy(
              'experimentName',
              'publish-experiment-hint publish-name-rule',
            )
          "
          data-testid="publish-experiment" />
        <datalist id="publish-experiment-names">
          <option
            v-for="name in experimentNames"
            :key="name"
            :value="name"></option>
        </datalist>
        <p id="publish-experiment-hint" class="builder-hint" aria-live="polite">
          {{ targetHint('experiment', experimentExists, experimentBlocker) }}
        </p>
      </div>

      <div v-if="scenario" class="builder-field" data-testid="publish-scenario">
        <h3>Scenario</h3>
        <p v-if="scenario.kind === 'stored'">
          The stored scenario
          <strong>{{ scenario.name }}</strong>
          will be used as it is on the server.
        </p>
        <template v-else>
          <p>
            This diagram carries an uploaded scenario. Choose the config it
            should be written to.
          </p>
          <label for="publish-scenario-name">Scenario name</label>
          <input
            id="publish-scenario-name"
            v-model="form.scenarioName"
            type="text"
            required
            list="publish-scenario-names"
            :aria-invalid="invalid('scenarioName')"
            :aria-describedby="describedBy('scenarioName', 'publish-name-rule')"
            data-testid="publish-scenario-name" />
          <datalist id="publish-scenario-names">
            <option
              v-for="name in scenarioNames"
              :key="name"
              :value="name"></option>
          </datalist>

          <fieldset :aria-describedby="describedBy('scenarioAction')">
            <legend>Scenario action</legend>
            <label class="builder-choice">
              <input
                id="publish-scenario-create"
                v-model="form.scenarioAction"
                type="radio"
                name="publish-scenario-action"
                value="create"
                :aria-invalid="invalid('scenarioAction')"
                data-testid="publish-scenario-create" />
              Create a new scenario
            </label>
            <label class="builder-choice">
              <input
                v-model="form.scenarioAction"
                type="radio"
                name="publish-scenario-action"
                value="update"
                :aria-invalid="invalid('scenarioAction')"
                data-testid="publish-scenario-update" />
              Update the existing scenario
            </label>
          </fieldset>
        </template>
      </div>

      <div class="builder-field">
        <h3>Checks</h3>
        <p>{{ summaryText }}</p>
        <ul v-if="issues.length" class="builder-issues">
          <li
            v-for="(issue, index) in issues"
            :key="`${issue.path}-${index}`"
            :data-level="issue.level">
            <strong>
              {{ issue.level === 'error' ? 'Error' : 'Warning' }}:
            </strong>
            {{ issue.message }}
          </li>
        </ul>
        <p class="builder-hint">
          The server publishes the last saved snapshot and checks it again.
        </p>
      </div>

      <p
        id="publish-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="publish-error">{{
          error.text
        }}</span>
      </p>

      <div class="builder-dialog__actions">
        <button type="button" class="builder-button" @click="$emit('close')">
          Cancel
        </button>
        <button
          ref="submitButton"
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="publish-submit"
          :disabled="blocked"
          :aria-disabled="busy || blocked ? 'true' : undefined">
          {{ submitLabel }}
        </button>
      </div>
    </form>

    <div v-else data-testid="publish-result">
      <p ref="resultSummary" tabindex="-1" data-testid="publish-summary">
        {{ resultText }}
      </p>

      <ul class="builder-issues">
        <li
          v-for="stage in result.stages"
          :key="stage.name"
          :data-level="stageFailed(stage) ? 'error' : 'info'"
          :data-status="stage.status">
          <strong>{{ stage.name }}:</strong>
          {{ stage.status }}
          <template v-if="stage.message">— {{ stage.message }}</template>
        </li>
        <li v-for="(warning, index) in result.warnings" :key="`w-${index}`">
          <strong>Warning:</strong>
          {{ warning }}
        </li>
        <li
          v-for="(problem, index) in result.errors"
          :key="`e-${index}`"
          data-level="error">
          <strong>Error:</strong>
          {{ problem }}
        </li>
      </ul>

      <div class="builder-dialog__actions">
        <button
          v-if="!result.ok"
          type="button"
          class="builder-button"
          data-testid="publish-retry"
          @click="back">
          Back
        </button>
        <button
          type="button"
          class="builder-button builder-button--primary"
          @click="$emit('close')">
          Close
        </button>
      </div>
    </div>

    <p
      class="builder-dialog__message builder-visually-hidden"
      role="status"
      data-testid="publish-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, reactive, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import { useFieldError, useMessage } from './message.js';

  import { count, listOf } from '@/builder/announce.js';
  import {
    CONFIG_NAME_RULE,
    buildPublishIntent,
    configName,
    describePublishResult,
    publishChecks,
    scenarioNames as readScenarioNames,
    stageFailed,
    targetHint,
    updateBlocker,
  } from '@/builder/publish.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close', 'published']);

  const store = useBuilderStore();
  const busy = ref(false);
  // Errors about a field buildPublishIntent() refused name its control.
  const { error, invalid, describedBy, fail } = useFieldError('publish-error', {
    topologyName: 'publish-name',
    experimentName: 'publish-experiment',
    scenarioName: 'publish-scenario-name',
    scenarioAction: 'publish-scenario-create',
  });
  const status = useMessage();
  const result = ref(null);
  const submitButton = ref(null);
  const resultSummary = ref(null);

  const scenario = computed(() => store.doc.scenario || null);

  const scenarioNames = computed(() =>
    readScenarioNames(store.sources.scenarios),
  );
  const topologyNames = computed(() =>
    readScenarioNames(store.sources.topologies),
  );
  const experimentNames = computed(() =>
    readScenarioNames(store.sources.experiments),
  );

  // The diagram and uploaded scenario names are free text; config names must
  // follow the server's naming rule, so each starts as a valid form of it.
  const form = reactive({
    mode: 'topology',
    topologyName: configName(store.doc.name),
    experimentName: '',
    scenarioName: configName(scenario.value?.name),
    scenarioAction: '',
  });

  // Configs may have been written since the list was last read, by this
  // editor or anyone else, so it is read on every open, with the published
  // diagrams and the saved snapshot that decide which ones this draft may
  // update. Nothing is edited while the dialog is open, so once the queue is
  // saved the snapshot is the one Publish sends.
  let sourcesLoaded = Promise.resolve();

  onMounted(() => {
    // A failed save is reported when Publish is pressed (store.publish).
    sourcesLoaded = Promise.allSettled([
      store.fetchSources(),
      store.fetchDocuments(),
      store.readOnly ? null : store.saveNow(),
    ]);
  });

  const topologyExists = computed(() =>
    (store.sources.topologies || []).some(
      (entry) =>
        (typeof entry === 'string' ? entry : entry?.name) ===
        form.topologyName.trim(),
    ),
  );

  const experimentExists = computed(() =>
    (store.sources.experiments || []).some(
      (entry) =>
        (typeof entry === 'string' ? entry : entry?.name) ===
        form.experimentName.trim(),
    ),
  );

  // What stops this draft from updating a config of that name, if anything.
  const topologyBlocker = computed(() =>
    updateBlocker(
      'topology',
      form.topologyName.trim(),
      store.sources.topologies,
      store.publishDraft,
    ),
  );

  const experimentBlocker = computed(() =>
    updateBlocker(
      'experiment',
      form.experimentName.trim(),
      store.sources.experiments,
      {
        ...store.publishDraft,
        topologyName: form.topologyName.trim(),
        scenarioName:
          scenario.value?.kind === 'stored'
            ? scenario.value.name
            : scenario.value
              ? form.scenarioName.trim()
              : '',
      },
    ),
  );

  // An interface with no VLAN is an error here, though not in the draft.
  const issues = computed(() => publishChecks(store.issues));
  const failed = computed(() =>
    issues.value.some((issue) => issue.level === 'error'),
  );
  const blocked = computed(() => failed.value || store.readOnly);

  // Devices from included topologies are not written to the topology: it
  // names those topologies in includeTopologies instead. Their connections,
  // and the switches and networks only they use, are not counted either.
  const summaryText = computed(() => {
    const summary = store.summary;
    const ready =
      `${count(summary.devices - summary.included, 'device')}, ` +
      `${count(summary.switches - summary.includedSwitches, 'switch', 'switches')}, ` +
      `${count(summary.networks - summary.includedNetworks, 'network')} and ` +
      `${count(summary.links - summary.includedLinks, 'connection')} are ready to publish` +
      `${failed.value ? ' once the errors below are fixed' : ''}.`;

    if (!summary.included) {
      return ready;
    }

    const onlyTheirs = listOf(
      [
        summary.includedSwitches &&
          count(summary.includedSwitches, 'switch', 'switches'),
        summary.includedNetworks && count(summary.includedNetworks, 'network'),
      ].filter(Boolean),
    );

    return (
      `${ready} The ${count(summary.included, 'device')} from included ` +
      `topologies and their ${count(summary.includedLinks, 'connection')}` +
      `${onlyTheirs ? `, and the ${onlyTheirs} only they use,` : ''} ` +
      'are not copied: the topology includes them by reference.'
    );
  });

  // The submit button names what it will do, so overwriting an existing
  // config is stated on the control that does it. A name this draft cannot
  // update is refused, so the draft can only create.
  const submitLabel = computed(() => {
    if (busy.value) {
      return 'Publishing…';
    }

    const topology =
      topologyExists.value && !topologyBlocker.value ? 'Update' : 'Create';

    if (form.mode !== 'topology-experiment') {
      return `${topology} topology`;
    }

    const experiment =
      experimentExists.value && !experimentBlocker.value ? 'update' : 'create';

    return experiment === topology.toLowerCase()
      ? `${topology} topology and experiment`
      : `${topology} topology and ${experiment} experiment`;
  });

  const resultText = computed(() => describePublishResult(result.value));

  /**
   * Builds the publish intent. The document is deliberately absent: the server
   * publishes the snapshot the cursor points at.
   */
  function buildIntent() {
    return buildPublishIntent(form, {
      scenario: scenario.value,
      topologies: store.sources.topologies,
      experiments: store.sources.experiments,
      scenarios: store.sources.scenarios,
      draft: store.publishDraft,
    });
  }

  async function submit() {
    if (busy.value) {
      return;
    }

    busy.value = true;
    error.clear();

    // Create or update is chosen from the list read when the dialog opened.
    await sourcesLoaded;

    const { intent, error: problem, field } = buildIntent();

    if (!intent) {
      busy.value = false;
      fail(problem, field);

      return;
    }

    status.set('Publishing…');

    const published = await store.publish(intent);

    busy.value = false;
    status.clear();

    if (!published) {
      // A refusal about a config names its field, for example a name that
      // exists after all; the list is read again, so the form now says so.
      fail(store.error || 'Publish failed.', store.errorField);

      return;
    }

    result.value = published;

    if (published.ok) {
      emit('published', published);
    }

    // The result replaces the form, and the button that had focus with it.
    await nextTick();
    resultSummary.value?.focus();
  }

  async function back() {
    result.value = null;
    await nextTick();
    submitButton.value?.focus();
  }

  defineExpose({ buildIntent, form, result });
</script>

<style scoped>
  .builder-dialog__actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
  }

  fieldset {
    border: 0;
    padding: 0;
  }
</style>
