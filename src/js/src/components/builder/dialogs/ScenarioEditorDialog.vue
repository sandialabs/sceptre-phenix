<!--
  A Scenario config, shown or edited as YAML, over the Scenarios dialog.

  - View shows the stored config read only, with Copy.
  - Edit shows it in a text field. Save replaces the stored config with the
    text (PUT /configs/Scenario/<name>). The name must stay the same,
    because a PUT with another name renames the config.
  - Create new scenario shows a new config in the text field. Save creates
    it (POST /configs).

  The parent's `save` stores the config and closes this dialog. Its refusal
  shows here: the server's reason, and each problem it found in the config.
  A problem in the text found before it is sent shows on the field.

  Cancel, Close dialog and Escape ask before they discard text the user
  changed. While Save runs, they do nothing, so the result is not lost.
-->
<template>
  <builder-dialog
    class="scenario-editor"
    :title="title"
    title-id="scenario-editor-title"
    data-testid="scenario-editor"
    @close="requestClose">
    <template v-if="mode === 'view'">
      <p id="scenario-editor-hint" class="builder-hint">
        This is the Scenario config that the server stores.
      </p>
      <pre
        ref="viewText"
        class="scenario-code"
        tabindex="0"
        role="region"
        aria-label="Scenario config (YAML)"
        aria-describedby="scenario-editor-hint"
        data-testid="scenario-editor-view"><code>{{ text }}</code></pre>
    </template>

    <div v-else class="builder-field">
      <label for="scenario-editor-text">Scenario config (YAML)</label>
      <p id="scenario-editor-hint" class="builder-hint">{{ hint }}</p>
      <textarea
        id="scenario-editor-text"
        ref="field"
        v-model="draft"
        class="scenario-code"
        rows="18"
        wrap="off"
        spellcheck="false"
        autocomplete="off"
        autocapitalize="off"
        autocorrect="off"
        :aria-invalid="invalid('text')"
        :aria-describedby="describedBy('text', 'scenario-editor-hint')"
        data-testid="scenario-editor-text"></textarea>
    </div>

    <div
      id="scenario-editor-error"
      class="builder-dialog__message builder-dialog__error"
      role="alert">
      <div v-if="error.text" :key="error.key">
        <span data-testid="scenario-editor-error">{{ error.text }}</span>
        <ul
          v-if="problems.length"
          class="scenario-editor__problems"
          data-testid="scenario-editor-problems">
          <li v-for="(line, index) in problems" :key="index">{{ line }}</li>
        </ul>
      </div>
    </div>

    <p class="builder-dialog__message" role="status">
      <span
        v-if="status.text"
        :key="status.key"
        data-testid="scenario-editor-status"
        >{{ status.text }}</span
      >
    </p>

    <div class="builder-dialog__actions">
      <template v-if="mode === 'view'">
        <button
          type="button"
          class="builder-button"
          data-testid="scenario-editor-copy"
          @click="copy">
          Copy YAML
        </button>
        <button
          type="button"
          class="builder-button builder-button--primary"
          data-testid="scenario-editor-close"
          @click="requestClose">
          Close
        </button>
      </template>
      <template v-else>
        <button
          type="button"
          class="builder-button"
          data-testid="scenario-editor-cancel"
          :aria-disabled="saving || undefined"
          @click="requestClose">
          Cancel
        </button>
        <builder-busy-button
          class="builder-button--primary"
          :busy="saving"
          :busy-label="`Saving scenario ${savingName}`"
          data-testid="scenario-editor-save"
          @click="submit">
          {{ mode === 'create' ? 'Create scenario' : 'Save scenario' }}
        </builder-busy-button>
      </template>
    </div>

    <builder-confirm
      v-if="confirming"
      id="scenario-editor-confirm"
      title="Discard your changes?"
      :message="discardMessage"
      confirm-label="Discard changes"
      cancel-label="Keep editing"
      @confirm="discard"
      @cancel="confirming = false" />
  </builder-dialog>
</template>

<script setup>
  import { computed, onMounted, ref } from 'vue';

  import BuilderBusyButton from '../BuilderBusyButton.vue';
  import BuilderConfirm from '../BuilderConfirm.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import { parseErrorText, useFieldError, useMessage } from './message.js';

  import { classifyError } from '@/builder/api.js';
  import { detectPlatform } from '@/builder/keymap.js';
  import {
    readScenarioText,
    scenarioErrorText,
    validationLines,
  } from '@/builder/scenarios.js';

  const props = defineProps({
    // 'view', 'edit' or 'create'.
    mode: { type: String, required: true },
    // The scenario's name. For 'create', the name that the text starts with.
    name: { type: String, required: true },
    // The config as YAML.
    text: { type: String, required: true },
    // Stores the config, for 'edit' and 'create', and closes this dialog.
    // Its refusal is thrown.
    save: { type: Function, default: null },
  });

  const emit = defineEmits(['close']);

  const { error, invalid, describedBy, fail } = useFieldError(
    'scenario-editor-error',
    { text: 'scenario-editor-text' },
  );
  const status = useMessage();
  const field = ref(null);
  const viewText = ref(null);
  const draft = ref(props.text);
  // The problems the server found in the config, one for each line.
  const problems = ref([]);
  const saving = ref(false);
  const savingName = ref(props.name);
  const confirming = ref(false);

  const title = computed(() => {
    if (props.mode === 'create') {
      return 'Create new scenario';
    }

    return props.mode === 'edit'
      ? `Edit scenario ${props.name}`
      : `Scenario ${props.name}`;
  });

  const hint = computed(() =>
    props.mode === 'create'
      ? 'Change the config as necessary. Select Create scenario to store it on the server.'
      : `Change the config as necessary. Select Save scenario to replace scenario ${props.name} on the server.`,
  );

  const dirty = computed(
    () => props.mode !== 'view' && draft.value !== props.text,
  );

  const discardMessage = computed(() =>
    props.mode === 'create'
      ? 'The server does not have this scenario yet. If you close the editor, it is lost.'
      : `You changed scenario ${props.name}. If you close the editor, your changes are lost.`,
  );

  onMounted(() => {
    // BuilderDialog focuses its panel when it opens. This runs after that.
    // The text starts at its first line.
    if (field.value) {
      field.value.focus();
      field.value.setSelectionRange?.(0, 0);
      field.value.scrollTop = 0;
    }
  });

  function requestClose() {
    if (saving.value) {
      return;
    }

    if (dirty.value) {
      confirming.value = true;

      return;
    }

    emit('close');
  }

  function discard() {
    confirming.value = false;
    emit('close');
  }

  async function submit() {
    if (saving.value) {
      return;
    }

    error.clear();
    problems.value = [];

    const read = readScenarioText(draft.value, {
      name: props.mode === 'edit' ? props.name : '',
    });

    if (read.error) {
      fail(
        read.parse
          ? `The YAML is not valid: ${parseErrorText(read.error)}`
          : read.error,
        'text',
      );

      return;
    }

    const { name } = read.config.metadata;
    const action = `${props.mode === 'create' ? 'create' : 'save'} scenario ${name}`;

    savingName.value = name;
    saving.value = true;
    status.set(`Saving scenario ${name}…`);

    try {
      await props.save(read.config);
    } catch (err) {
      status.clear();
      problems.value = validationLines(err);

      // A refused config is fixed in the text. Any other refusal leaves
      // focus on Save, to try again.
      if (problems.value.length || classifyError(err) === 'invalid') {
        fail(scenarioErrorText(err, action), 'text');
      } else {
        error.set(scenarioErrorText(err, action));
      }
    } finally {
      saving.value = false;
    }
  }

  async function copy() {
    try {
      if (!globalThis.isSecureContext || !navigator.clipboard?.writeText) {
        throw new Error('The clipboard is not available.');
      }

      await navigator.clipboard.writeText(props.text);
      status.set('Copied the scenario config.');
    } catch {
      // Plain HTTP has no clipboard: the text is selected, to copy.
      const element = viewText.value;

      element?.focus();
      globalThis.getSelection?.()?.selectAllChildren(element);
      status.set(
        `Press ${detectPlatform() === 'mac' ? '⌘C' : 'Ctrl+C'} to copy the selected config.`,
      );
    }
  }
</script>

<style scoped>
  .scenario-editor {
    width: min(56rem, 94vw);
  }

  .scenario-code {
    box-sizing: border-box;
    display: block;
    width: 100%;
    max-height: min(55vh, 32rem);
    margin: 0 0 0.5rem;
    padding: 0.5rem;
    overflow: auto;
    border: 1px solid var(--bx-field-border);
    border-radius: var(--bx-radius);
    background: var(--bx-bg-alt);
    color: var(--bx-text);
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 0.85rem;
    line-height: 1.45;
    tab-size: 2;
    white-space: pre;
  }

  .scenario-code code {
    font: inherit;
    color: inherit;
    background: none;
    padding: 0;
  }

  /* The field keeps the rule of .builder-field, with the code font and a
     height a person can change. */
  .builder-field textarea.scenario-code {
    height: min(55vh, 32rem);
    min-height: 10rem;
    padding: 0.5rem;
    background: var(--bx-surface);
    resize: vertical;
  }

  .scenario-editor__problems {
    margin: 0.25rem 0 0;
    padding-left: 1.25rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 0.85em;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
</style>
