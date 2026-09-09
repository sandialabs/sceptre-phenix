<!--
  Generate shell.

  POST /builder/generate asks the server to build a diagram from an existing
  topology or experiment; the server owns the conversion, so the client only
  picks the source and reports the warnings that come back.
-->
<template>
  <builder-dialog
    title="Generate diagram"
    title-id="generate-dialog-title"
    @close="$emit('close')">
    <form @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>Source</legend>
        <label>
          <input v-model="form.source" type="radio" value="stored" />
          Stored config
        </label>
        <label>
          <input v-model="form.source" type="radio" value="uploaded" />
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
          data-testid="generate-name">
          <option value="">Choose a {{ form.kind }}</option>
          <option v-for="name in choices" :key="name" :value="name">
            {{ name }}
          </option>
        </select>
      </div>

      <p
        v-if="form.source === 'stored' && !choices.length"
        data-testid="generate-empty">
        This phenix instance has no {{ form.kind }} configs to generate from.
      </p>

      <div v-if="form.source === 'uploaded'" class="builder-field">
        <label for="generate-file">Topology or Experiment config</label>
        <input
          id="generate-file"
          type="file"
          accept=".json,.yaml,.yml"
          data-testid="generate-file"
          @change="onFile" />
      </div>

      <ul
        v-if="warnings.length"
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

      <p v-if="error" role="alert" data-testid="generate-error">{{ error }}</p>

      <div class="builder-dialog__actions">
        <button type="button" class="builder-button" @click="$emit('close')">
          Cancel
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="generate-submit"
          :disabled="
            busy || (form.source === 'stored' ? !form.name : !form.content)
          "
          :aria-disabled="
            busy || (form.source === 'stored' ? !form.name : !form.content)
          ">
          {{ busy ? 'Generating…' : 'Generate' }}
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { computed, reactive, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';

  import { MAX_IMPORT_BYTES } from '@/builder/decode.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close', 'generated']);

  const store = useBuilderStore();
  const busy = ref(false);
  const error = ref('');
  const warnings = ref([]);

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

  async function onFile(event) {
    const file = event.target.files?.[0];

    if (!file) {
      form.content = '';
      return;
    }

    if (file.size > MAX_IMPORT_BYTES) {
      error.value = 'The uploaded config is larger than the 5 MiB limit.';
      form.content = '';
      return;
    }

    error.value = '';
    form.content = await file.text();
  }

  async function submit() {
    busy.value = true;
    error.value = '';
    warnings.value = [];

    const request =
      form.source === 'uploaded'
        ? { content: form.content }
        : { kind: form.kind, name: form.name };
    const result = await store.generate(request);

    busy.value = false;

    if (!result) {
      error.value = store.error || 'Generation failed.';

      return;
    }

    warnings.value = result.warnings;

    emit('generated', result);
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
