<!--
  Which version of a draft's unsaved changes to save, when more than one
  tab of this browser holds some (see builder/tabs.js).

  A modal dialog (see BuilderDialog) with one radio button per version:
  this tab's, the other tabs', and those closed tabs left, each with how
  many changes it holds and when the last was made. Save this version
  saves the one chosen to the draft; the others are saved as new drafts,
  or, for a role that cannot make drafts, deleted from this browser, which
  the dialog says first. Download keeps a copy of each version this tab can
  read. Decide later, Escape or a click outside close it: nothing is sent
  meanwhile, and the editor's notice opens it again.
-->
<template>
  <builder-dialog
    title="Choose which changes to save"
    title-id="tabs-choice-title"
    describedby="tabs-choice-message"
    data-testid="tabs-choice"
    @close="$emit('close')">
    <p id="tabs-choice-message">
      Changes to this draft in more than one tab are not saved yet. Only one
      version can be saved to the draft.
      <template v-if="canCreate">The others are saved as new drafts.</template>
      <template v-else>
        Your role cannot make drafts, so the others are deleted from this
        browser. Use Download to keep a copy of them first.
      </template>
    </p>

    <form ref="form" @submit.prevent="choose">
      <fieldset class="builder-field">
        <legend>Version to save</legend>
        <div
          v-for="row in rows"
          :key="row.id"
          class="builder-tabs-choice__row"
          :data-testid="`tabs-choice-row-${row.where}`">
          <label class="builder-choice">
            <input
              v-model="picked"
              type="radio"
              name="tabs-choice"
              :value="row.id" />
            {{ row.text }}
          </label>
          <button
            v-if="downloadable(row)"
            type="button"
            class="builder-button"
            :aria-label="downloadName(row)"
            @click="downloadRow(row)">
            Download
          </button>
        </div>
      </fieldset>

      <p role="status" class="builder-tabs-choice__status">{{ status }}</p>

      <div class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="tabs-choice-later"
          @click="$emit('close')">
          Decide later
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="tabs-choice-save"
          :aria-disabled="busy || !picked || undefined">
          Save this version
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { nextTick, onMounted, ref, watch } from 'vue';

  import BuilderDialog from './BuilderDialog.vue';

  import { focusLost } from '@/builder/commands.js';

  const props = defineProps({
    // choiceRows() in builder/tabs.js: this tab's version first.
    rows: { type: Array, required: true },
    // Whether the role may make drafts, which keep the versions not chosen.
    canCreate: { type: Boolean, default: true },
    // The choice is being carried out.
    busy: { type: Boolean, default: false },
    // (row) => Promise<{name, text}|null>: the file Download saves for a
    // version this tab can read.
    downloadVersion: { type: Function, default: null },
  });

  const emit = defineEmits(['choose', 'close']);

  const form = ref(null);
  const status = ref('');

  // This tab's version when it has changes, else the one changed last.
  function preferred() {
    const [mine, ...others] = props.rows;

    if (mine?.changes > 0) {
      return mine.id;
    }

    const time = (row) => Date.parse(row.changedAt) || 0;
    const latest = [...others].sort((a, b) => time(b) - time(a))[0];

    return latest?.id || mine?.id || '';
  }

  const picked = ref(preferred());

  // A version that goes (its tab sent it, or closed) is no longer offered.
  // Focus on its radio button moves to the one picked in its place.
  watch(
    () => props.rows.map((row) => row.id),
    async (ids) => {
      if (!ids.includes(picked.value)) {
        picked.value = preferred();
      }

      await nextTick();

      if (form.value && focusLost()) {
        form.value.querySelector('input[type="radio"]:checked')?.focus();
      }
    },
  );

  // The versions whose diagram this tab holds: its own and closed tabs'.
  function downloadable(row) {
    return (
      Boolean(props.downloadVersion) &&
      row.changes > 0 &&
      ['this', 'closed'].includes(row.where)
    );
  }

  function downloadName(row) {
    return row.where === 'this'
      ? "Download this tab's changes"
      : `Download the changes of ${row.name.toLowerCase()}`;
  }

  // Saves a file from inside the dialog: the page behind it is inert.
  function download({ name, text }) {
    const url = URL.createObjectURL(
      new Blob([text], { type: 'application/json;charset=utf-8' }),
    );
    const link = document.createElement('a');

    link.href = url;
    link.download = name;
    link.hidden = true;
    form.value.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 30000);

    return name;
  }

  async function downloadRow(row) {
    status.value = '';

    const file = await props.downloadVersion(row);

    if (!form.value) {
      return;
    }

    status.value = file
      ? `Saved ${download(file)}.`
      : 'This browser no longer holds these changes.';
  }

  function choose() {
    if (!props.busy && picked.value) {
      emit('choose', picked.value);
    }
  }

  // BuilderDialog focuses its panel when it opens; this runs after it, so
  // focus starts on the version picked.
  onMounted(async () => {
    await nextTick();
    form.value?.querySelector('input[type="radio"]:checked')?.focus();
  });
</script>

<style scoped>
  .builder-tabs-choice__row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.25rem 0.5rem;
    padding: 0.25rem 0;
  }

  .builder-tabs-choice__row .builder-choice {
    flex: 1 1 14rem;
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .builder-tabs-choice__status {
    margin: 0.25rem 0 0.5rem;
  }
</style>
