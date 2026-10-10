<!--
  Auto-group by name pattern, opened by the third item of the toolbar's
  Auto-group menu and by the palette's command.

  The user types a regular expression. Group runs it on the names of the
  ungrouped devices and switches (the selected ones, when nodes are
  selected). It groups together the nodes where it matches the same text
  (see autoGroupByPattern in the store). The pattern runs only then, in a
  Web Worker. The dialog ends the worker when it takes too long, so the page
  never freezes.

  When something keeps Group from grouping, the reason shows under the field
  and the dialog stays open. After Group makes groups, the dialog closes.
  The Builder's live region waits while a dialog is open, and then says how
  many groups Group made. The pattern used last is
  offered again, selected, so typing replaces it. Closing the dialog while a
  pattern runs ends the run.
-->
<template>
  <builder-dialog
    title="Auto-group by name pattern"
    title-id="group-pattern-dialog-title"
    class="builder-group-pattern"
    data-testid="group-pattern-dialog"
    @close="$emit('close')">
    <form @submit.prevent="submit">
      <p
        class="builder-hint builder-group-pattern__intro"
        data-testid="group-pattern-intro">
        {{
          selected
            ? 'Groups the selected ungrouped devices and switches whose names match.'
            : 'Groups the ungrouped devices and switches whose names match.'
        }}
      </p>

      <div class="builder-field">
        <label for="group-pattern">Name pattern</label>
        <input
          id="group-pattern"
          ref="field"
          v-model="pattern"
          type="text"
          class="builder-group-pattern__field"
          :maxlength="GROUP_PATTERN_MAX"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          :aria-invalid="error.field === 'pattern' ? 'true' : undefined"
          :aria-describedby="
            error.text
              ? 'group-pattern-hint group-pattern-error'
              : 'group-pattern-hint'
          "
          data-testid="group-pattern-field" />
        <p id="group-pattern-hint" class="builder-hint">
          A regular expression, matched against each name, ignoring case. Names
          with the same matched text go in one group, named after that text.
          With parentheses, the text of the first pair is used. For example,
          <code>^[a-z]+</code> puts web-01 and web-02 in a group named web.
        </p>
        <p
          id="group-pattern-error"
          class="builder-dialog__message builder-dialog__error"
          role="alert">
          <span
            v-if="error.text"
            :key="error.key"
            data-testid="group-pattern-error"
            >{{ error.text }}</span
          >
        </p>
      </div>

      <div class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="group-pattern-cancel"
          @click="$emit('close')">
          Cancel
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="group-pattern-submit"
          :aria-busy="busy || undefined"
          :aria-disabled="busy ? 'true' : undefined">
          {{ busy ? 'Grouping…' : 'Group' }}
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import { useMessage } from './message.js';

  import { readGroupPattern } from '@/builder/grouping.js';
  import { GROUP_PATTERN_MAX } from '@/builder/groupingPattern.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  // `field` is 'pattern' while the message is about the pattern itself.
  const error = useMessage();
  const field = ref(null);
  const pattern = ref(readGroupPattern());
  const busy = ref(false);
  // The dialog is modal, so the selection is what it was when it opened.
  const selected = store.selection.nodes.length > 0;
  // Aborted when the dialog closes, which ends a pattern still running.
  const closing = new AbortController();

  async function submit() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    error.clear();
    busy.value = true;

    const result = await store.autoGroupByPattern(pattern.value, {
      signal: closing.signal,
    });

    busy.value = false;

    if (closing.signal.aborted) {
      return;
    }

    if (result.problem) {
      error.set(result.problem, result.invalid ? 'pattern' : '');
      await nextTick();
      field.value?.focus();

      return;
    }

    emit('close');
  }

  onMounted(() => {
    field.value?.focus();
    field.value?.select();
  });

  onBeforeUnmount(() => closing.abort());
</script>

<style scoped>
  .builder-group-pattern {
    width: min(34rem, 92vw);
  }

  .builder-group-pattern .builder-hint {
    margin: 0.2rem 0 0;
  }

  .builder-group-pattern .builder-group-pattern__intro {
    margin: 0 0 0.6rem;
  }

  .builder-group-pattern__field,
  .builder-group-pattern code {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  }

  /* The example is the hint's own text, set apart by its font alone: not
     the application's colors for code. */
  .builder-group-pattern code {
    padding: 0;
    font-size: inherit;
    color: inherit;
    background: none;
  }
</style>
