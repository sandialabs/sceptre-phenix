<!--
  Scenario shell.

  A document may reference a stored scenario config or carry uploaded scenario
  content. Both forms need the config apiVersion and a content digest, so the
  stored list comes from GET /api/v1/builder/sources and uploads are digested
  here before the reference is attached.
-->
<template>
  <builder-dialog
    title="Scenario"
    title-id="scenario-dialog-title"
    @close="$emit('close')">
    <form @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>Scenario reference</legend>
        <label class="builder-choice">
          <input
            v-model="form.kind"
            type="radio"
            name="scenario-kind"
            value="none" />
          No scenario
        </label>
        <label class="builder-choice">
          <input
            v-model="form.kind"
            type="radio"
            name="scenario-kind"
            value="stored"
            data-testid="scenario-kind-stored" />
          Stored scenario
        </label>
        <label class="builder-choice">
          <input
            v-model="form.kind"
            type="radio"
            name="scenario-kind"
            value="uploaded"
            data-testid="scenario-kind-uploaded" />
          Upload scenario
        </label>
      </fieldset>

      <div v-if="form.kind === 'stored'" class="builder-field">
        <label for="scenario-name">Scenario</label>
        <select
          id="scenario-name"
          v-model="form.name"
          :aria-invalid="invalid('name')"
          :aria-describedby="describedBy('name')"
          data-testid="scenario-name">
          <option value="">Choose a scenario</option>
          <option
            v-for="entry in scenarios"
            :key="entry.name"
            :value="entry.name">
            {{ entry.name }}
          </option>
        </select>
        <p v-if="!scenarios.length" data-testid="scenario-empty">
          This phenix instance has no scenario configs.
        </p>
      </div>

      <div v-else-if="form.kind === 'uploaded'" class="builder-field">
        <label for="scenario-file">Scenario config file (JSON or YAML)</label>
        <input
          id="scenario-file"
          type="file"
          accept=".json,.yaml,.yml"
          :aria-invalid="invalid('file')"
          :aria-describedby="describedBy('file', 'scenario-file-hint')"
          data-testid="scenario-file"
          @change="onFile" />
        <p id="scenario-file-hint" class="builder-hint">Up to 5 MiB.</p>
        <p
          v-if="form.digest"
          class="scenario-digest"
          data-testid="scenario-digest">
          Content digest: {{ form.digest }}
        </p>
      </div>

      <p
        id="scenario-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="scenario-error">{{
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
          data-testid="scenario-submit"
          :aria-disabled="busy ? 'true' : undefined">
          Save scenario
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { computed, reactive, ref, watch } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import { parseErrorText, useFieldError } from './message.js';

  import { contentDigest, isDigest } from '@/builder/digest.js';
  import { MAX_IMPORT_BYTES, parseImport } from '@/builder/decode.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { SCENARIO_API_VERSION } from '@/builder/validate.js';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  const { error, invalid, describedBy, fail } = useFieldError(
    'scenario-error',
    { name: 'scenario-name', file: 'scenario-file' },
  );
  const busy = ref(false);

  const current = store.doc.scenario;

  const form = reactive({
    kind: current?.kind || 'none',
    name: current?.name || '',
    apiVersion: current?.apiVersion || '',
    content: current?.content || null,
    digest: current?.digest || '',
  });

  const scenarios = computed(() =>
    (store.sources.scenarios || []).map((entry) =>
      typeof entry === 'string' ? { name: entry } : entry,
    ),
  );

  // A scenario at another apiVersion, such as a v1 one, is refused rather
  // than attached: every save of the diagram would fail on the server.
  function unsupportedVersion(apiVersion) {
    return (
      `This scenario is ${apiVersion}, and the Builder attaches only ` +
      `${SCENARIO_API_VERSION} scenarios. Upgrade it to ` +
      `${SCENARIO_API_VERSION}, then upload it again.`
    );
  }

  // An error about one kind of reference says nothing about another.
  watch(
    () => form.kind,
    () => error.clear(),
  );

  async function onFile(event) {
    const file = event.target.files?.[0];

    error.clear();

    if (!file) {
      return;
    }

    form.content = null;
    form.apiVersion = '';
    form.name = '';
    form.digest = '';

    if (file.size > MAX_IMPORT_BYTES) {
      error.set(
        'The uploaded scenario is larger than the 5 MiB limit.',
        'file',
      );
      return;
    }

    const parsed = parseImport(await file.text(), {
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

    // The server stores and checks uploaded content at this version only,
    // and does not upgrade it (validate.go refuses any other).
    if (config.apiVersion !== SCENARIO_API_VERSION) {
      error.set(unsupportedVersion(config.apiVersion), 'file');

      return;
    }

    form.content = config.spec;
    form.apiVersion = config.apiVersion;
    form.name = config.metadata?.name || file.name;
    form.digest = await contentDigest(form.content);
  }

  async function submit() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    error.clear();

    if (form.kind === 'none') {
      store.setScenario(null);
      emit('close');

      return;
    }

    busy.value = true;

    try {
      if (form.kind === 'stored') {
        const entry = scenarios.value.find((item) => item.name === form.name);

        if (!entry) {
          fail('Choose a scenario.', 'name');

          return;
        }

        const digest =
          entry.digest ||
          (entry.content ? await contentDigest(entry.content) : '');

        if (!isDigest(digest) || !entry.apiVersion) {
          fail(
            'The server did not report the apiVersion and content digest for this scenario, which are required to attach it.',
            'name',
          );

          return;
        }

        store.setScenario({
          kind: 'stored',
          name: entry.name,
          apiVersion: entry.apiVersion,
          content: entry.content || undefined,
          digest,
        });

        emit('close');

        return;
      }

      if (!form.content) {
        fail('Choose a scenario file to upload.', 'file');

        return;
      }

      if (!form.apiVersion) {
        fail('The uploaded file has no apiVersion.', 'file');

        return;
      }

      // An uploaded scenario the diagram already carries from before.
      if (form.apiVersion !== SCENARIO_API_VERSION) {
        fail(unsupportedVersion(form.apiVersion), 'file');

        return;
      }

      store.setScenario({
        kind: 'uploaded',
        name: form.name || undefined,
        apiVersion: form.apiVersion,
        content: form.content,
        digest: form.digest || (await contentDigest(form.content)),
      });

      emit('close');
    } finally {
      busy.value = false;
    }
  }
</script>

<style scoped>
  .builder-dialog__actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
  }

  /* A digest is one long word; it wraps rather than widen the dialog. */
  .scenario-digest {
    overflow-wrap: anywhere;
  }
</style>
