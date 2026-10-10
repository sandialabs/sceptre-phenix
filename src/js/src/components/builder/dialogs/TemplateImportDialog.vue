<!--
  Import templates: reads a template file (YAML or JSON, see
  builder/templateFile.js) into the user's library as a new collection.
  Opened by Import templates on the drafts page's Node Templates tab.

  Choosing a file reads and checks it immediately. When something keeps the
  file from being imported, the dialog shows each problem with where it is
  in the file ("templates[2].name: ..."). For a file that can be imported,
  the dialog shows its collection's name and how many templates and custom
  icons it holds. When one of the user's collections has that name, " (2)"
  and so on follows it.

  Import first adds the custom icons that the file carries to the icon
  library. It then adds the templates with the collection, each as a copy
  under an id that the server gives it. An icon that could not be added, or
  whose name the server holds with other bytes, does not stop the import.
  The dialog then stays open and lists what became of each such icon.
  Otherwise, the dialog closes and the page says what was imported.
-->
<template>
  <builder-dialog
    title="Import templates"
    title-id="template-import-title"
    class="builder-template-import"
    data-testid="template-import-dialog"
    @close="requestClose">
    <form novalidate @submit.prevent="submit">
      <div class="builder-field">
        <label for="template-import-file">Template file</label>
        <input
          id="template-import-file"
          ref="fileField"
          type="file"
          accept=".yaml,.yml,.json"
          :disabled="Boolean(done) || undefined"
          :aria-invalid="problem ? 'true' : undefined"
          :aria-describedby="
            problem
              ? 'template-import-file-hint template-import-error'
              : 'template-import-file-hint'
          "
          data-testid="template-import-file"
          @change="onFile" />
        <p id="template-import-file-hint" class="builder-hint">
          A YAML or JSON file of one collection, as Export saves it. Its
          templates are added to your library as a new collection.
        </p>
      </div>

      <p
        v-if="file && !done"
        class="builder-template-import__summary"
        data-testid="template-import-summary">
        {{ summary }}
      </p>

      <div
        id="template-import-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <template v-if="problem">
          <p data-testid="template-import-error">{{ problem.error }}</p>
          <ul
            v-if="problem.issues.length"
            class="builder-template-import__issues">
            <li
              v-for="(entry, index) in problem.issues"
              :key="index"
              data-testid="template-import-issue">
              {{ entry.path }}: {{ entry.message }}
            </li>
          </ul>
        </template>
      </div>

      <!-- Rendered from the start, so a screen reader reads what it later
           says. The page's live region cannot speak through a modal dialog. -->
      <div class="builder-template-import__done" role="status">
        <template v-if="done">
          <p data-testid="template-import-done">{{ done.message }}</p>
          <ul>
            <li
              v-for="(warning, index) in done.warnings"
              :key="index"
              data-testid="template-import-warning">
              {{ warning }}
            </li>
          </ul>
        </template>
      </div>

      <div class="builder-dialog__actions">
        <template v-if="done">
          <button
            ref="closeButton"
            type="button"
            class="builder-button builder-button--primary"
            data-testid="template-import-close"
            @click="requestClose">
            Close
          </button>
        </template>
        <template v-else>
          <button
            type="button"
            class="builder-button"
            data-testid="template-import-cancel"
            @click="requestClose">
            Cancel
          </button>
          <!-- While the library takes the templates, it keeps focus and
               does nothing. -->
          <button
            type="submit"
            class="builder-button builder-button--primary"
            :aria-disabled="importing || !file || undefined"
            :aria-busy="importing || undefined"
            data-testid="template-import-submit">
            {{ importing ? 'Importing…' : 'Import' }}
          </button>
        </template>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';

  import { count } from '@/builder/announce.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    MAX_TEMPLATE_FILE_BYTES,
    importProblem,
    importedMessage,
    parseTemplateFile,
    uniqueCollectionName,
  } from '@/builder/templateFile.js';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();

  const fileField = ref(null);
  const closeButton = ref(null);
  // The file chosen, once it was read and can be imported.
  const file = ref(null);
  // Why the file chosen cannot be read or imported: {error, issues}.
  const problem = ref(null);
  // What the import came to, when the dialog stays open to say it:
  // {message, warnings}.
  const done = ref(null);
  const importing = ref(false);

  // The read in progress. A file chosen while another is read replaces it.
  let reads = 0;

  // The name the new collection gets in the library as it is now.
  const collectionName = computed(() =>
    file.value
      ? uniqueCollectionName(
          file.value.name,
          store.ownCollections.map((entry) => entry.name),
        )
      : '',
  );

  const summary = computed(() => {
    if (!file.value) {
      return '';
    }

    const icons = Object.keys(file.value.icons || {}).length;
    const holds = [
      count(file.value.templates.length, 'template'),
      ...(icons ? [count(icons, 'custom icon')] : []),
    ].join(' and ');
    const renamed =
      collectionName.value === file.value.name
        ? ''
        : ` Your library has a collection of that name, so it is added as ${collectionName.value}.`;

    return `${file.value.name}: ${holds}.${renamed}`;
  });

  function requestClose() {
    if (!importing.value) {
      emit('close');
    }
  }

  async function onFile(event) {
    const read = (reads += 1);
    const chosen = event.target.files?.[0];

    file.value = null;
    problem.value = null;

    if (!chosen) {
      return;
    }

    if (chosen.size > MAX_TEMPLATE_FILE_BYTES) {
      problem.value = {
        error: `The file is larger than the ${MAX_TEMPLATE_FILE_BYTES / 1024 / 1024} MiB limit.`,
        issues: [],
      };

      return;
    }

    const text = await chosen.text();

    if (read !== reads) {
      return;
    }

    const parsed = parseTemplateFile(text);

    if (parsed.ok) {
      file.value = parsed.file;
    } else {
      problem.value = { error: parsed.error, issues: parsed.issues };
    }
  }

  async function submit() {
    if (importing.value || done.value) {
      return;
    }

    if (!file.value) {
      problem.value = problem.value || {
        error: 'Choose a template file to import.',
        issues: [],
      };
      fileField.value?.focus();

      return;
    }

    const full = importProblem(store.templates, file.value.templates.length);

    if (full) {
      problem.value = { error: full, issues: [] };

      return;
    }

    importing.value = true;
    problem.value = null;

    try {
      const imported = await store.importTemplateFile(file.value);
      const message = importedMessage(imported.collection, imported.templates);

      if (imported.warnings.length) {
        done.value = { message, warnings: imported.warnings };
        store.announce(message);
        await nextTick();
        closeButton.value?.focus();
      } else {
        store.announce(message);
        emit('close');
      }
    } catch (failure) {
      problem.value = {
        error: store.describeLibraryError(failure, 'import the templates'),
        issues: [],
      };
    } finally {
      importing.value = false;
    }
  }

  onMounted(() => {
    fileField.value?.focus();
  });
</script>

<style scoped>
  .builder-template-import {
    width: min(36rem, 92vw);
  }

  .builder-template-import .builder-hint {
    margin: 0.2rem 0 0;
  }

  .builder-template-import__summary {
    margin: 0.6rem 0 0;
    overflow-wrap: anywhere;
  }

  /* The dialog scrolls when the lists are long: they never scroll on their
     own, so no region needs a way in from the keyboard. */
  .builder-template-import__issues,
  .builder-template-import__done ul {
    margin: 0.3rem 0 0;
    padding-left: 1.2rem;
    overflow-wrap: anywhere;
  }

  .builder-template-import__done:not(:empty) {
    margin-top: 0.6rem;
  }

  .builder-template-import__done p {
    margin: 0;
  }
</style>
