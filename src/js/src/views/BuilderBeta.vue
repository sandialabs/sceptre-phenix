<!--
  Builder Beta view.

  Owns the editor shell: theme, live regions, conflict banner, dialogs and the
  drafts landing. The legacy /builder editor is untouched and still reachable
  from the header.
-->
<template>
  <div
    ref="rootEl"
    class="builder-root"
    :data-builder-reduced-motion="String(reducedMotion)">
    <a class="builder-skip-link" href="#builder-canvas-region">
      Skip to diagram canvas
    </a>

    <p
      class="builder-visually-hidden"
      role="status"
      aria-live="polite"
      data-testid="builder-live-region">
      {{ store.announcement }}
    </p>

    <p v-if="store.error" class="builder-panel builder-error" role="alert">
      {{ store.error }}
    </p>

    <template v-if="!editing">
      <builder-drafts
        :mine="store.drafts.mine"
        :shared="store.drafts.shared"
        :published="store.documents"
        :loading="store.loading"
        @blank="startBlank"
        @import="openLanding('import')"
        @generate="openLanding('generate')"
        @refresh="refresh"
        @open="openDraft"
        @delete="deleteDraft" />
    </template>

    <template v-else>
      <div class="builder-header">
        <div class="builder-field builder-header__name">
          <label for="builder-doc-name">Diagram name</label>
          <input
            id="builder-doc-name"
            :value="store.doc.name"
            type="text"
            data-testid="builder-name"
            @change="rename($event.target.value)" />
        </div>

        <p class="builder-status" data-testid="builder-summary">
          {{ store.statusText }}
        </p>

        <button type="button" class="builder-button" @click="closeEditor">
          Back to drafts
        </button>
      </div>

      <div
        v-if="store.hasConflict"
        class="builder-panel builder-conflict"
        role="alertdialog"
        aria-labelledby="conflict-title"
        data-testid="builder-conflict">
        <h2 id="conflict-title">This draft changed on the server</h2>
        <p>
          Someone else saved a newer version of this draft. Your edits are kept
          on this device. They cannot be written over the newer server version,
          so choose how to keep them.
        </p>
        <div class="builder-drafts__actions">
          <button
            type="button"
            class="builder-button builder-button--primary"
            data-testid="conflict-fork"
            :disabled="resolving"
            @click="resolveConflict('fork')">
            Save my history as a new draft
          </button>
          <button
            type="button"
            class="builder-button"
            data-testid="conflict-reload"
            :disabled="resolving"
            @click="resolveConflict('reload')">
            Discard mine and load the server version
          </button>
        </div>
      </div>

      <div
        v-if="store.readOnly"
        class="builder-panel builder-error"
        role="status"
        data-testid="builder-readonly">
        You can view this draft but not change it. Use Export to keep a copy.
      </div>

      <builder-toolbar
        :minimap="showMinimap"
        @publish="dialog = 'publish'"
        @export="dialog = 'export'"
        @import="dialog = 'import'"
        @scenario="dialog = 'scenario'"
        @history="openHistory"
        @cycle-theme="cycleTheme"
        @toggle-minimap="showMinimap = !showMinimap" />

      <div class="builder-layout">
        <div class="builder-layout__side">
          <builder-palette />
          <builder-outline />
        </div>

        <div id="builder-canvas-region" class="builder-layout__main">
          <builder-canvas
            ref="canvas"
            :show-minimap="showMinimap"
            :reduced-motion="reducedMotion" />
        </div>

        <div class="builder-layout__side">
          <builder-inspector />
        </div>
      </div>
    </template>

    <publish-dialog v-if="dialog === 'publish'" @close="dialog = ''" />
    <import-dialog
      v-if="dialog === 'import'"
      @close="dialog = ''"
      @imported="afterImport" />
    <generate-dialog
      v-if="dialog === 'generate'"
      @close="dialog = ''"
      @generated="afterImport" />
    <export-dialog
      v-if="dialog === 'export'"
      :viewport-element="viewportElement"
      @close="dialog = ''" />
    <scenario-dialog v-if="dialog === 'scenario'" @close="dialog = ''" />

    <builder-dialog
      v-if="dialog === 'history'"
      title="Draft history"
      data-testid="history-dialog"
      @close="dialog = ''">
      <p v-if="!store.serverHistory.length">
        The server holds no snapshots for this draft yet.
      </p>
      <ol v-else class="builder-history" data-testid="history-list">
        <li
          v-for="(entry, index) in store.serverHistory"
          :key="entry.id || index">
          <button
            type="button"
            class="builder-button"
            :aria-label="`Restore snapshot ${index + 1}: ${entry.summary || entry.id}`"
            @click="restore(entry)">
            {{ index + 1 }}. {{ entry.summary || entry.id }}
          </button>
        </li>
      </ol>
    </builder-dialog>
  </div>
</template>

<script setup>
  import { onBeforeUnmount, onMounted, ref } from 'vue';
  import { useRoute } from 'vue-router';

  import '@/builder/builder.css';

  import BuilderCanvas from '@/components/builder/BuilderCanvas.vue';
  import BuilderDialog from '@/components/builder/BuilderDialog.vue';
  import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';
  import BuilderInspector from '@/components/builder/BuilderInspector.vue';
  import BuilderOutline from '@/components/builder/BuilderOutline.vue';
  import BuilderPalette from '@/components/builder/BuilderPalette.vue';
  import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
  import ExportDialog from '@/components/builder/dialogs/ExportDialog.vue';
  import GenerateDialog from '@/components/builder/dialogs/GenerateDialog.vue';
  import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';
  import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';
  import ScenarioDialog from '@/components/builder/dialogs/ScenarioDialog.vue';

  import {
    applyTheme,
    prefersReducedMotion,
    watchSystemTheme,
  } from '@/builder/theme.js';
  import { useBuilderStore } from '@/builder/store.js';

  const store = useBuilderStore();
  const route = useRoute();

  const rootEl = ref(null);
  const canvas = ref(null);
  const editing = ref(false);
  const dialog = ref('');
  const showMinimap = ref(true);
  const reducedMotion = ref(false);
  const resolving = ref(false);
  let stopWatchingSystemTheme = () => {};

  function viewportElement() {
    return canvas.value?.viewportElement?.() || null;
  }

  async function refresh() {
    await Promise.all([store.fetchDrafts(), store.fetchDocuments()]);
  }

  async function startBlank() {
    store.newDocument({ name: 'Untitled topology' });

    const created = await store.createDraft({ title: 'Untitled topology' });

    editing.value = Boolean(created);
  }

  function openLanding(which) {
    store.newDocument({ name: 'Untitled topology' });
    editing.value = false;
    dialog.value = which;
  }

  async function openDraft(item) {
    let opened = null;

    if (item.owner && item.id) {
      opened = await store.loadDraft(item.owner, item.id);
    } else if (item.id) {
      opened = await store.openPublishedDocument(item.id);
    }

    editing.value = Boolean(opened);
  }

  async function deleteDraft(item) {
    await store.deleteDraft(item.owner, item.id, item.etag);
    await refresh();
  }

  async function resolveConflict(choice) {
    resolving.value = true;

    try {
      await store.resolveConflict(choice);
    } finally {
      resolving.value = false;
    }
  }

  async function openHistory() {
    await store.fetchHistory();
    dialog.value = 'history';
  }

  async function restore(entry) {
    await store.restoreSnapshot(entry.id);
    dialog.value = '';
  }

  async function afterImport(result = {}) {
    let ready = Boolean(result.draftCreated || store.etag);

    if (!result.draftCreated && !store.etag) {
      const source = result.source;
      const sourceToken = source?.fullName
        ? source.stored
          ? source.fullName
          : `uploaded/${source.fullName}`
        : '';

      ready = Boolean(
        await store.createDraft({
          document: result.document || store.doc,
          title: (result.document || store.doc).name,
          sourceToken,
        }),
      );
    }

    editing.value = ready;
  }

  function rename(name) {
    if (name === store.doc.name) {
      return;
    }

    store.setInfo({ name });
  }

  function closeEditor() {
    editing.value = false;
    refresh();
  }

  function cycleTheme() {
    store.cycleTheme(rootEl.value);
  }

  function onGlobalKeydown(event) {
    if (event.key === 'Escape' && dialog.value) {
      dialog.value = '';
    }
  }

  onMounted(async () => {
    const matchMedia =
      typeof window !== 'undefined' && window.matchMedia
        ? window.matchMedia.bind(window)
        : undefined;

    store.initTheme(rootEl.value);
    reducedMotion.value = prefersReducedMotion(matchMedia);
    stopWatchingSystemTheme = watchSystemTheme(matchMedia, () => {
      if (store.theme === 'system') {
        store.resolvedTheme = applyTheme(rootEl.value, store.theme, matchMedia);
      }
    });

    window.addEventListener('keydown', onGlobalKeydown);

    await Promise.all([store.fetchSchema(), refresh(), store.fetchSources()]);

    const topology = String(route.query.topology || '');
    if (topology) {
      const published = store.documents.find(
        (document) => document.target === topology,
      );

      if (published) {
        const opened = await store.openPublishedDocument(published.id);
        editing.value = Boolean(opened);
      } else {
        store.error = `No published Builder Beta document exists for topology ${topology}.`;
      }
    }
  });

  onBeforeUnmount(() => {
    window.removeEventListener('keydown', onGlobalKeydown);
    stopWatchingSystemTheme();
    store.autosave?.dispose();
  });
</script>

<style scoped>
  .builder-header {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 0.75rem;
    margin-bottom: 0.5rem;
  }

  .builder-header__name {
    min-width: 18rem;
    margin-bottom: 0;
  }

  .builder-error,
  .builder-conflict {
    padding: 0.6rem 0.75rem;
    margin-bottom: 0.75rem;
  }

  .builder-conflict h2 {
    font-weight: 700;
    margin: 0 0 0.25rem;
  }
</style>
