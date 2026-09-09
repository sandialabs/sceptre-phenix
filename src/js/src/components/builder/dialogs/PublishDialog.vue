<!--
  Publish shell.

  Publish is an intent, not an upload: the server loads the snapshot the draft
  cursor points at and re-runs its own checks, so this dialog never sends the
  document. It confirms the queue is drained first (store.publish does that),
  then names the configs to write and reports the per stage result.
-->
<template>
  <builder-dialog
    title="Publish diagram"
    title-id="publish-dialog-title"
    @close="$emit('close')">
    <form v-if="!result" @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>What to publish</legend>
        <label>
          <input v-model="form.mode" type="radio" value="topology" />
          Topology only
        </label>
        <label>
          <input
            v-model="form.mode"
            type="radio"
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
          aria-describedby="publish-topology-action-hint"
          data-testid="publish-name" />
        <datalist id="publish-topology-names">
          <option
            v-for="name in topologyNames"
            :key="name"
            :value="name"></option>
        </datalist>
        <p id="publish-topology-action-hint" class="builder-hint">
          {{
            topologyExists
              ? 'A topology with this name exists and will be updated.'
              : 'A new topology will be created.'
          }}
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
          aria-describedby="publish-experiment-hint"
          data-testid="publish-experiment" />
        <datalist id="publish-experiment-names">
          <option
            v-for="name in experimentNames"
            :key="name"
            :value="name"></option>
        </datalist>
        <p id="publish-experiment-hint" class="builder-hint">
          {{
            experimentExists
              ? 'An experiment with this name exists and will be updated.'
              : 'A new experiment will be created.'
          }}
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
            data-testid="publish-scenario-name" />
          <datalist id="publish-scenario-names">
            <option
              v-for="name in scenarioNames"
              :key="name"
              :value="name"></option>
          </datalist>

          <fieldset>
            <legend>Scenario action</legend>
            <label>
              <input
                v-model="form.scenarioAction"
                type="radio"
                value="create"
                data-testid="publish-scenario-create" />
              Create a new scenario
            </label>
            <label>
              <input
                v-model="form.scenarioAction"
                type="radio"
                value="update"
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

      <p v-if="error" role="alert" data-testid="publish-error">{{ error }}</p>

      <div class="builder-dialog__actions">
        <button type="button" class="builder-button" @click="$emit('close')">
          Cancel
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="publish-submit"
          :disabled="busy || blocked"
          :aria-disabled="busy || blocked">
          {{ busy ? 'Publishing…' : 'Publish' }}
        </button>
      </div>
    </form>

    <div v-else data-testid="publish-result">
      <p role="status">{{ resultText }}</p>

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
          @click="result = null">
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
  </builder-dialog>
</template>

<script setup>
  import { computed, onMounted, reactive, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';

  import {
    buildPublishIntent,
    describePublishResult,
    scenarioNames as readScenarioNames,
    stageFailed,
  } from '@/builder/publish.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close', 'published']);

  const store = useBuilderStore();
  const busy = ref(false);
  const error = ref('');
  const result = ref(null);

  const scenario = computed(() => store.doc.scenario || null);
  const sources = computed(() => store.sources);

  const scenarioNames = computed(() =>
    readScenarioNames(store.sources.scenarios),
  );
  const topologyNames = computed(() =>
    readScenarioNames(store.sources.topologies),
  );
  const experimentNames = computed(() =>
    readScenarioNames(store.sources.experiments),
  );

  const form = reactive({
    mode: 'topology',
    topologyName: store.doc.name || '',
    experimentName: '',
    scenarioName: scenario.value?.name || '',
    scenarioAction: '',
  });

  onMounted(() => {
    if (!store.sources.topologies.length) {
      store.fetchSources();
    }
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

  const issues = computed(() => store.issues);
  const blocked = computed(() => store.errors.length > 0 || store.readOnly);

  const summaryText = computed(() => {
    const summary = store.summary;

    return `${summary.devices} devices, ${summary.switches} switches, ${summary.networks} networks and ${summary.links} connections are ready to publish.`;
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
    });
  }

  async function submit() {
    error.value = '';

    const { intent, error: problem } = buildIntent();

    if (!intent) {
      error.value = problem;

      return;
    }

    busy.value = true;

    const published = await store.publish(intent);

    busy.value = false;

    if (!published) {
      error.value = store.error || 'Publish failed.';

      return;
    }

    result.value = published;

    if (published.ok) {
      emit('published', published);
    }
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

  fieldset label {
    display: block;
  }

  .builder-hint {
    font-size: 0.85em;
    opacity: 0.85;
  }
</style>
