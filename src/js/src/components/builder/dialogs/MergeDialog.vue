<!-- Review and merge: Keep mine or Keep theirs for each field that both versions
     changed (see merge.js). Save merged waits until each field has a choice. -->
<template>
  <builder-dialog
    :title="`Merge changes from ${from}`"
    title-id="merge-dialog-title"
    describedby="merge-dialog-intro"
    class="builder-merge"
    data-testid="merge-dialog"
    @close="$emit('close')">
    <p id="merge-dialog-intro" class="builder-merge__intro">
      <template v-if="clashes.length">
        You and {{ from }} changed these fields differently. Choose the version
        to keep for each field. Save merged keeps every other change, yours and
        theirs, as it is. It saves the merged diagram on top of their version.
      </template>
      <template v-else>
        You and {{ from }} made no changes that clash. There is nothing to
        choose. But the Builder cannot save the merged diagram as it is.
      </template>
    </p>

    <form ref="form" @submit.prevent="save">
      <div v-if="clashes.length" class="builder-merge__all">
        <button
          type="button"
          class="builder-button"
          data-testid="merge-all-mine"
          @click="chooseAll('mine')">
          Keep all mine
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="merge-all-theirs"
          @click="chooseAll('theirs')">
          Keep all theirs
        </button>
      </div>

      <div class="builder-merge__clashes">
        <fieldset
          v-for="(clash, index) in clashes"
          :key="clash.key"
          class="builder-field builder-merge__clash"
          :data-testid="`merge-clash-${index}`">
          <legend>{{ clash.label }}</legend>
          <p :id="`merge-clash-${index}-summary`" class="builder-hint">
            {{ clash.summary }}
          </p>
          <label class="builder-choice">
            <input
              v-model="choices[clash.key]"
              type="radio"
              :name="`merge-clash-${index}`"
              value="mine"
              :aria-describedby="`merge-clash-${index}-summary`"
              :data-testid="`merge-clash-${index}-mine`" />
            Keep mine: {{ clash.mineText }}
          </label>
          <label class="builder-choice">
            <input
              v-model="choices[clash.key]"
              type="radio"
              :name="`merge-clash-${index}`"
              value="theirs"
              :aria-describedby="`merge-clash-${index}-summary`"
              :data-testid="`merge-clash-${index}-theirs`" />
            Keep theirs: {{ clash.theirsText }}
          </label>
        </fieldset>
      </div>

      <p
        v-if="clashes.length"
        id="merge-count"
        role="status"
        class="builder-merge__count"
        data-testid="merge-count">
        {{ chosen }} of {{ clashes.length }} chosen
      </p>

      <!-- Why Save merged did not save: a field without a choice, what
           the strict check refuses in the merged diagram, or why the save
           failed. -->
      <div
        id="merge-error"
        role="alert"
        class="builder-dialog__message builder-dialog__error">
        <div v-if="error.text || issues.length" :key="error.key">
          <p data-testid="merge-error">{{ error.text }}</p>
          <ul v-if="issues.length" data-testid="merge-issues">
            <li v-for="issue in issues" :key="issue">{{ issue }}</li>
          </ul>
        </div>
      </div>

      <div class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="merge-cancel"
          @click="$emit('close')">
          Cancel
        </button>
        <!-- While it saves, a ring turns over its label. -->
        <builder-busy-button
          type="submit"
          class="builder-button--primary"
          data-testid="merge-save"
          :busy="busy"
          :disabled="!clashes.length || !ready"
          :aria-describedby="clashes.length ? 'merge-count' : 'merge-error'">
          Save merged
        </builder-busy-button>
      </div>

      <p class="builder-visually-hidden" role="status">
        <span v-if="busy">Saving the merged diagram…</span>
      </p>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, reactive, ref } from 'vue';

  import BuilderBusyButton from '../BuilderBusyButton.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import { useMessage } from './message.js';

  import { mergeSaveFailure, useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close', 'merged']);

  const store = useBuilderStore();
  const form = ref(null);
  // The merge as it is when the dialog opens: the page behind it is inert,
  // so the diagram does not change while it is open.
  const review = store.mergeReview();
  const clashes = review?.clashes || [];
  const from = store.merge?.from || 'another editor';
  // The version kept of each clash, by its key: 'mine' or 'theirs'.
  const choices = reactive({});
  const chosen = computed(
    () => clashes.filter((clash) => choices[clash.key]).length,
  );
  const ready = computed(() => chosen.value === clashes.length);
  const busy = ref(false);
  const error = useMessage();
  // What the strict check refuses in the merged diagram, as "path: message".
  const issues = ref([]);

  function chooseAll(side) {
    clashes.forEach((clash) => {
      choices[clash.key] = side;
    });
    error.clear();
    issues.value = [];
  }

  // Focus on the first radio button of the first field without a choice.
  function focusUnchosen() {
    const index = clashes.findIndex((clash) => !choices[clash.key]);

    form.value?.querySelector(`input[name="merge-clash-${index}"]`)?.focus();
  }

  // Why the merged diagram was refused. With clashes, another choice may
  // change that. With none, only Cancel is left.
  function refused(found) {
    issues.value = found;
    error.set(
      clashes.length
        ? 'The Builder cannot save the merged diagram. Make different choices, or select Cancel. After Cancel, you can save your history as a new draft or discard it.'
        : 'The Builder cannot save the merged diagram because of the problems listed below. After Cancel, you can save your history as a new draft or discard it.',
    );
  }

  // With nothing to choose, why the merged diagram cannot be saved.
  function explain() {
    refused(store.mergeProblems({}));
  }

  async function save() {
    if (busy.value) {
      return;
    }

    // Save merged is unavailable: it says again why.
    if (!clashes.length) {
      explain();

      return;
    }

    if (!ready.value) {
      issues.value = [];
      error.set(
        `Choose the version to keep for each field. You chose ${chosen.value} of ${clashes.length}.`,
      );
      focusUnchosen();

      return;
    }

    error.clear();
    issues.value = [];
    busy.value = true;

    let result;

    // Whatever happens, the dialog leaves Saving…, keeps the choices, and
    // Cancel goes back to the conflict panel, which still offers saving the
    // history as a new draft and discarding it.
    try {
      result = await store.saveMergeChoices({ ...choices });
    } catch (failure) {
      result = { saved: false, issues: [], error: mergeSaveFailure(failure) };
    } finally {
      busy.value = false;
    }

    if (result.saved) {
      emit('merged');

      return;
    }

    // Another merge is in progress (a newer version arrived while the user
    // chose). Nothing is saved, and the dialog closes when the review it
    // shows is no longer offered (see mergeReview in Builder.vue).
    if (result.busy) {
      error.set('Another change arrived. The Builder starts the merge again.');

      return;
    }

    // The merge could not be stored: nothing was saved, and the same
    // choices can be saved again.
    if (result.error) {
      error.set(
        `${result.error} Try again, or select Cancel. After Cancel, you can save your history as a new draft or discard it.`,
      );

      return;
    }

    refused(result.issues);
  }

  // BuilderDialog focuses its panel when it opens. This runs after that, so
  // focus starts on the first field to choose for. With nothing to choose,
  // the dialog says immediately why the merged diagram cannot be saved.
  onMounted(async () => {
    await nextTick();

    if (clashes.length) {
      focusUnchosen();
    } else {
      explain();
    }
  });
</script>

<style scoped>
  .builder-merge {
    width: min(40rem, 92vw);
  }

  .builder-merge__intro {
    margin: 0 0 0.6rem;
  }

  .builder-merge__all {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-bottom: 0.6rem;
  }

  .builder-merge__clash {
    margin: 0 0 0.6rem;
    overflow-wrap: anywhere;
  }

  .builder-merge__clash .builder-hint {
    margin: 0 0 0.25rem;
  }

  .builder-merge__clash .builder-choice {
    display: block;
    padding: 0.15rem 0;
  }

  .builder-merge__count {
    margin: 0.25rem 0 0.5rem;
  }

  .builder-merge ul {
    margin: 0.25rem 0 0;
    padding-left: 1.25rem;
  }
</style>
