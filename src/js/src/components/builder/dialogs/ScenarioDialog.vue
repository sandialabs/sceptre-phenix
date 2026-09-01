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
        <label>
          <input v-model="form.kind" type="radio" value="none" />
          No scenario
        </label>
        <label>
          <input
            v-model="form.kind"
            type="radio"
            value="stored"
            data-testid="scenario-kind-stored" />
          Stored scenario config
        </label>
        <label>
          <input
            v-model="form.kind"
            type="radio"
            value="uploaded"
            data-testid="scenario-kind-uploaded" />
          Upload scenario content
        </label>
      </fieldset>

      <div v-if="form.kind === 'stored'" class="builder-field">
        <label for="scenario-name">Scenario</label>
        <select
          id="scenario-name"
          v-model="form.name"
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
          data-testid="scenario-file"
          @change="onFile" />
        <p v-if="form.digest" data-testid="scenario-digest">
          Content digest: {{ form.digest }}
        </p>
      </div>

      <p v-if="error" role="alert" data-testid="scenario-error">{{ error }}</p>

      <div class="builder-dialog__actions">
        <button type="button" class="builder-button" @click="$emit('close')">
          Cancel
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="scenario-submit"
          :disabled="busy"
          :aria-disabled="busy">
          Save scenario
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { computed, reactive, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';

  import { contentDigest, isDigest } from '@/builder/digest.js';
  import { MAX_IMPORT_BYTES, parseImport } from '@/builder/decode.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  const error = ref('');
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

  async function onFile(event) {
    const file = event.target.files?.[0];

    error.value = '';

    if (!file) {
      return;
    }

    form.content = null;
    form.apiVersion = '';
    form.name = '';
    form.digest = '';

    if (file.size > MAX_IMPORT_BYTES) {
      error.value = 'The uploaded scenario is larger than the 5 MiB limit.';
      return;
    }

    const parsed = parseImport(await file.text(), {
      as: 'raw',
      expectedKind: 'Scenario',
    });

    if (!parsed.ok) {
      error.value = parsed.error;

      return;
    }

    const config = parsed.value || {};

    if (
      !config.apiVersion ||
      !config.spec ||
      typeof config.spec !== 'object' ||
      Array.isArray(config.spec)
    ) {
      error.value =
        'The uploaded Scenario must include apiVersion and an object spec.';
      return;
    }

    form.content = config.spec;
    form.apiVersion = config.apiVersion;
    form.name = config.metadata?.name || file.name;
    form.digest = await contentDigest(form.content);
  }

  async function submit() {
    error.value = '';

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
          error.value = 'Choose a scenario.';

          return;
        }

        const digest =
          entry.digest ||
          (entry.content ? await contentDigest(entry.content) : '');

        if (!isDigest(digest) || !entry.apiVersion) {
          error.value =
            'The server did not report the apiVersion and content digest for this scenario, which are required to attach it.';

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
        error.value = 'Choose a scenario file to upload.';

        return;
      }

      if (!form.apiVersion) {
        error.value = 'The uploaded file has no apiVersion.';

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

  fieldset label {
    display: block;
  }
</style>
