<!-- The drafts page's Layers tab: the server's layer set library (see
     layerSetStore.js). A built-in set is read only. -->
<template>
  <div ref="rootEl" class="builder-layer-sets" data-testid="layer-sets-tab">
    <p class="builder-layer-sets__note">
      A layer set is a list of layers that every user of this server can use. In
      a diagram, Edit layers… applies a copy of a set. A layer can have node
      matchers, which put devices at the layer by their fields.
    </p>

    <div class="builder-layer-sets__actions">
      <button
        ref="newButton"
        type="button"
        class="builder-button builder-button--primary"
        aria-haspopup="dialog"
        :aria-disabled="Boolean(busy) || undefined"
        data-testid="layer-sets-new"
        @click="busy || openEditor('')">
        <builder-icon name="plus" :size="14" />
        New layer set
      </button>
      <!-- While the file is uploaded, a ring turns in place of the icon. -->
      <builder-busy-button
        icon="upload"
        :busy="busy === 'upload'"
        :disabled="Boolean(busy)"
        data-testid="layer-sets-upload"
        @click="busy || fileField?.click()">
        Upload layer set…
      </builder-busy-button>
      <!-- The button above is the Tab stop. -->
      <input
        ref="fileField"
        type="file"
        hidden
        tabindex="-1"
        aria-hidden="true"
        accept=".yaml,.yml,.json"
        data-testid="layer-sets-file"
        @change="upload" />
    </div>

    <p class="builder-dialog__message" role="status">
      <span
        v-if="status.text"
        :key="status.key"
        data-testid="layer-sets-status"
        >{{ status.text }}</span
      >
    </p>
    <div class="builder-dialog__message builder-dialog__error" role="alert">
      <template v-if="error.text">
        <p :key="error.key" data-testid="layer-sets-error">{{ error.text }}</p>
        <ul v-if="issues.length" data-testid="layer-sets-issues">
          <li v-for="(issue, index) in issues" :key="index">
            {{ issue.path ? `${issue.path}: ` : '' }}{{ issue.message }}
          </li>
        </ul>
      </template>
    </div>

    <div v-if="library.status === 'failed'" class="builder-layer-sets__failed">
      <p role="alert">Could not load the layer sets. {{ library.error }}</p>
      <button
        type="button"
        class="builder-button"
        data-testid="layer-sets-retry"
        @click="retry">
        Retry
      </button>
    </div>

    <p
      v-if="!library.loaded && library.status !== 'failed'"
      role="status"
      data-testid="layer-sets-loading">
      Loading…
    </p>
    <template v-else-if="library.loaded">
      <p v-if="library.damaged">
        The server cannot read
        {{ count(library.damaged, 'layer set') }}. A newer version of phenix may
        have saved {{ library.damaged === 1 ? 'it' : 'them' }}.
      </p>

      <builder-card-filter
        v-if="library.items.length"
        id="layer-sets-filter"
        label="Filter layer sets"
        :model-value="filterText"
        :hint="filterHint"
        :announcement="
          filterHint || `${count(library.items.length, 'layer set')}.`
        "
        @update:model-value="setFilter"
        @announce="(text) => store.announce(text, { slot: 'filter' })" />

      <builder-bulk-bar
        v-if="selection.total"
        id="layer-sets"
        label="Layer sets"
        :count="selection.count"
        :total="selection.total"
        :state="selection.state"
        :running="running"
        :disabled="Boolean(busy)"
        @toggle-all="cards.selectAll">
        <!-- Both say how many are selected, so one that cannot act says
             why: "0 of 4 selected". -->
        <builder-action-button
          action="download"
          data-testid="bulk-download-layer-sets"
          aria-describedby="bulk-count-layer-sets"
          :disabled="!selection.count || Boolean(busy)"
          @click="downloadSelected">
          Download selected
        </builder-action-button>
        <builder-action-button
          action="delete"
          data-testid="bulk-delete-layer-sets"
          aria-haspopup="dialog"
          aria-describedby="bulk-count-layer-sets"
          :disabled="!selection.count || Boolean(busy)"
          @click="askDeleteSelected">
          Delete selected
        </builder-action-button>
      </builder-bulk-bar>

      <!-- What the last batch left undone, under the row. -->
      <builder-bulk-summary
        v-if="outcome"
        :ref="(element) => (summary = element)"
        :heading="outcome.heading"
        :items="outcome.items"
        @dismiss="dismissOutcome" />

      <p
        v-if="library.items.length && !shownSets.length"
        data-testid="layer-sets-filter-none">
        No layer sets match the filter.
      </p>
      <ul
        v-else
        class="builder-cards builder-layer-sets__cards"
        :aria-busy="Boolean(running) || undefined"
        data-testid="layer-sets-list">
        <!-- The card focus was last in is the list's one Tab stop. A card is
             named by its set and whether it is selected. -->
        <li
          v-for="(set, index) in shownSets"
          :key="set.id"
          class="builder-card builder-panel builder-layer-sets__card"
          :class="{ 'is-selected': selection.has(set) }"
          :tabindex="selection.tabStop(set) ? 0 : -1"
          :aria-labelledby="`layer-set-name-${index} layer-set-state-${index}`"
          :data-layer-set="set.id"
          :data-testid="`layer-set-card-${set.id}`"
          @focusin="selection.focused(set)"
          @keydown="onCardKeydown($event, index)">
          <div class="builder-card__head">
            <h2 :id="`layer-set-name-${index}`">
              <builder-icon
                v-if="setIcon(set).key || setIcon(set).src"
                :name="setIcon(set).key || 'image'"
                :src="setIcon(set).src"
                :size="20" />
              <span>{{ set.name }}</span>
              <span
                v-if="set.id === library.defaultId"
                class="builder-layer-sets__tag"
                data-testid="layer-set-default"
                >Default</span
              >
              <span v-if="set.builtin" class="builder-layer-sets__tag"
                >Built-in</span
              >
            </h2>
            <!-- The checkbox, at the card's top right, is named for the card,
                 as its buttons are. -->
            <label class="builder-card__select">
              <input
                type="checkbox"
                :checked="selection.has(set)"
                :disabled="Boolean(busy)"
                data-testid="layer-set-select"
                @pointerdown="cards.shift.pointerdown"
                @keydown="cards.shift.keydown"
                @click="cards.shift.click"
                @change="cards.select(set, $event)" />
              <span class="builder-visually-hidden">Select {{ set.name }}</span>
            </label>
          </div>
          <p
            v-if="set.description"
            class="builder-layer-sets__description"
            data-testid="layer-set-description">
            {{ set.description }}
          </p>
          <p class="builder-card__meta" data-testid="layer-set-count">
            {{ count(effectiveLayerList(set.layers).length, 'layer')
            }}{{ set.builtin ? '. Read only.' : '' }}
          </p>
          <p v-if="changedText(set)" class="builder-card__meta">
            {{ changedText(set) }}
          </p>
          <ul
            class="builder-layer-sets__layers"
            :aria-label="`Layers of ${set.name}`"
            data-testid="layer-set-layers">
            <li v-for="layer in effectiveLayerList(set.layers)" :key="layer.id">
              <layer-name :layer="layer" :library="iconLibrary" />
            </li>
          </ul>

          <div class="builder-card__actions">
            <!-- A built-in set is read only, so View opens it. -->
            <builder-action-button
              :action="set.builtin ? 'view' : 'edit'"
              aria-haspopup="dialog"
              :aria-label="`${set.builtin ? 'View' : 'Edit'} ${set.name}`"
              :disabled="Boolean(busy)"
              data-testid="layer-set-edit"
              @click="busy || openEditor(set.id)" />
            <builder-action-button
              action="duplicate"
              :aria-label="`Duplicate ${set.name}`"
              :disabled="Boolean(busy)"
              data-testid="layer-set-duplicate"
              @click="duplicate(set)" />
            <builder-action-button
              action="download"
              :aria-label="`Download ${set.name}`"
              :disabled="Boolean(busy)"
              data-testid="layer-set-download"
              @click="download(set)" />
            <builder-action-button
              v-if="!set.builtin"
              action="delete"
              aria-haspopup="dialog"
              :aria-label="`Delete ${set.name}`"
              :disabled="Boolean(busy)"
              data-testid="layer-set-delete"
              @click="busy || (deleting = { set, index })" />
            <!-- The grid sets the width, so a press does not move it. -->
            <builder-busy-button
              icon="star"
              class="builder-layer-sets__default"
              :aria-label="`${set.id === library.defaultId ? 'Clear default' : 'Set default'}: ${set.name}`"
              :disabled="Boolean(busy)"
              data-testid="layer-set-default-toggle"
              @click="toggleDefault(set)">
              <span class="builder-button__swap">
                <span :class="{ 'is-off': set.id === library.defaultId }"
                  >Set default</span
                >
                <span :class="{ 'is-off': set.id !== library.defaultId }"
                  >Clear default</span
                >
              </span>
            </builder-busy-button>
          </div>
          <!-- Whether the card is selected, for its name only. -->
          <span :id="`layer-set-state-${index}`" hidden>{{
            selection.has(set) ? 'selected' : 'not selected'
          }}</span>
        </li>
      </ul>
    </template>

    <layers-dialog
      v-if="editing !== null"
      mode="library"
      :set-id="editing"
      @saved="onSaved"
      @close="editing = null" />

    <builder-confirm
      v-if="deleting"
      id="layer-set-delete"
      title="Delete layer set?"
      :message="`Delete ${deleting.set.name} from the server? Diagrams keep their copies of its layers. You cannot undo this.`"
      confirm-label="Delete"
      action="delete"
      @cancel="deleting = null"
      @confirm="confirmDelete" />
    <!-- Delete selected: one question for the batch. -->
    <builder-confirm
      v-if="deletingSelected"
      id="bulk-delete-layer-sets"
      :title="`Delete ${count(deletingSelected.sets.length, 'layer set')}?`"
      :message="
        layerSetsDeleteMessage(
          deletingSelected.sets.map((set) => set.name),
          deletingSelected.builtins.map((set) => set.name),
        )
      "
      :confirm-label="`Delete ${count(deletingSelected.sets.length, 'layer set')}`"
      action="delete"
      @cancel="deletingSelected = null"
      @confirm="confirmDeleteSelected" />
  </div>
</template>

<script setup>
  import { saveAs } from 'file-saver';
  import {
    computed,
    nextTick,
    onMounted,
    onBeforeUnmount,
    reactive,
    ref,
  } from 'vue';

  import BuilderActionButton from './BuilderActionButton.vue';
  import BuilderBulkBar from './BuilderBulkBar.vue';
  import BuilderBulkSummary from './BuilderBulkSummary.vue';
  import BuilderBusyButton from './BuilderBusyButton.vue';
  import BuilderCardFilter from './BuilderCardFilter.vue';
  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import LayersDialog from './dialogs/LayersDialog.vue';
  import { useMessage } from './dialogs/message.js';
  import LayerName from './layers/LayerName.vue';

  import { count } from '@/builder/announce.js';
  import {
    bulkFailureHeading,
    bulkOutcome,
    layerSetsDeleteMessage,
    runBulk,
  } from '@/builder/bulk.js';
  import {
    filterItems,
    keepCardFilter,
    readCardFilter,
  } from '@/builder/cardFilter.js';
  import { isIconKey } from '@/builder/catalog.js';
  import { focusLost } from '@/builder/commands.js';
  import { formatTimestamp } from '@/builder/format.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { iconSrc } from '@/builder/icons.js';
  import {
    downloadLayerSetFile,
    layerSetIconNames,
    layerSetsDownloadedMessage,
    MAX_LAYER_SET_FILE_BYTES,
  } from '@/builder/layerSetFile.js';
  import { BUILTIN_NOT_DELETED } from '@/builder/layerSetStore.js';
  import { layerSetSearch } from '@/builder/librarySearch.js';
  import {
    useCardSelection,
    useListSelection,
  } from '@/builder/listSelection.js';
  import { effectiveLayerList } from '@/builder/model.js';
  import { useBuilderStore } from '@/builder/store.js';

  // lost-focus: focus would fall to <body>, so the view focuses the tab.
  const emit = defineEmits(['lost-focus']);

  const store = useBuilderStore();
  const library = computed(() => store.layerSets);
  const status = useMessage();
  const error = useMessage();
  // The problems of a file that Upload refused, each {path, message}.
  const issues = ref([]);

  const rootEl = ref(null);
  const newButton = ref(null);
  const fileField = ref(null);
  // What is in progress: 'upload', 'duplicate', 'default', 'delete' or
  // 'bulk', or ''. A press meanwhile does nothing.
  const busy = ref('');
  // The ID of the set the layers editor edits, '' for a new set, or null
  // while it is closed.
  const editing = ref(null);
  // The set Delete asks about, and the place of its card, or null.
  const deleting = ref(null);
  // What Delete selected asks about: { sets, builtins }, or null.
  const deletingSelected = ref(null);
  // The batch in progress, { label, done, total }, and what the last batch
  // left undone, { heading, items }.
  const running = ref(null);
  const outcome = ref(null);
  let summary = null;
  // Set when the tab goes. An answer that comes later changes nothing.
  let closed = false;

  // --- filter --------------------------------------------------------------

  // The text is kept for the browser tab's session, as a drafts tab's is.
  const filterText = ref(readCardFilter('layers'));

  // The sets that match the filter by name or description.
  const shownSets = computed(() =>
    filterItems(library.value.items, filterText.value, layerSetSearch),
  );

  // "2 of 5 layer sets shown.", or '' with no filter.
  const filterHint = computed(() =>
    filterText.value.trim()
      ? `${shownSets.value.length} of ${count(library.value.items.length, 'layer set')} shown.`
      : '',
  );

  function setFilter(text) {
    filterText.value = text;
    keepCardFilter('layers', text);
  }

  function setIcon(set) {
    return {
      key: isIconKey(set.iconKey) ? set.iconKey : '',
      src: set.icon ? iconSrc(set.icon, null, iconLibrary) : '',
    };
  }

  function changedText(set) {
    const at = formatTimestamp(set.updated || set.created);
    const by = set.updatedBy || set.createdBy;

    if (!at && !by) {
      return '';
    }

    return [`Changed${by ? ` by ${by}` : ''}`, at ? `on ${at}` : '']
      .filter(Boolean)
      .join(' ')
      .concat('.');
  }

  function clearMessages() {
    status.clear();
    error.clear();
    issues.value = [];
  }

  function fail(text, list = []) {
    error.set(text);
    issues.value = list;
  }

  // Focuses a card's Edit once the list shows it. The filter is cleared
  // first when it hides the card.
  async function focusEdit(id) {
    if (!shownSets.value.some((set) => set.id === id)) {
      setFilter('');
    }

    await nextTick();
    rootEl.value
      ?.querySelector(
        `[data-layer-set="${CSS.escape(id)}"] [data-testid="layer-set-edit"]`,
      )
      ?.focus();
  }

  function openEditor(id) {
    clearMessages();
    editing.value = id;
  }

  function onSaved({ set, created }) {
    status.set(
      created
        ? `Added layer set ${set.name}.`
        : `Saved layer set ${set.name}, version ${set.version}.`,
    );

    // The editor gives focus back to its opener as it closes. A new set's
    // card takes it then.
    if (created) {
      setTimeout(() => !closed && focusEdit(set.id));
    }
  }

  async function run(kind, task) {
    if (busy.value) {
      return;
    }

    clearMessages();
    busy.value = kind;

    try {
      await task();
    } finally {
      busy.value = '';
    }
  }

  function upload(event) {
    const file = event.target.files?.[0];

    event.target.value = '';

    if (!file) {
      return;
    }

    run('upload', async () => {
      if (file.size > MAX_LAYER_SET_FILE_BYTES) {
        fail(
          `Could not upload ${file.name}. A layer set file holds at most ${MAX_LAYER_SET_FILE_BYTES / (1024 * 1024)} MiB.`,
        );

        return;
      }

      try {
        const { set, warnings } = await store.uploadLayerSetFile(
          await file.text(),
        );

        if (closed) {
          return;
        }

        status.set(
          [
            `Uploaded ${file.name} as layer set ${set.name}, with ${count(set.layers.length, 'layer')}.`,
            ...warnings,
          ].join(' '),
        );
        await focusEdit(set.id);
      } catch (caught) {
        if (!closed) {
          fail(
            store.describeLayerSetError(caught, `upload ${file.name}`),
            caught?.issues || [],
          );
        }
      }
    });
  }

  async function download(set) {
    if (busy.value) {
      return;
    }

    clearMessages();

    // The library has no images: the file waits for those it carries.
    await iconLibrary.ensureImages(layerSetIconNames(set));

    try {
      const { message } = downloadLayerSetFile(set, {
        library: iconLibrary,
        saveAs,
      });

      status.set(message);
    } catch (caught) {
      fail(`Could not download ${set.name}. ${caught?.message || ''}`.trim());
    }
  }

  function duplicate(set) {
    run('duplicate', async () => {
      try {
        const copy = await store.duplicateLayerSet(set.id);

        if (!closed) {
          status.set(`Added layer set ${copy.name}, a copy of ${set.name}.`);
          await focusEdit(copy.id);
        }
      } catch (caught) {
        if (!closed) {
          fail(store.describeLayerSetError(caught, `duplicate ${set.name}`));
        }
      }
    });
  }

  function toggleDefault(set) {
    const clear = set.id === library.value.defaultId;

    run('default', async () => {
      try {
        await store.setDefaultLayerSet(clear ? null : set.id);

        if (!closed) {
          status.set(
            clear
              ? 'New blank diagrams start with the placeholder layers.'
              : `New blank diagrams start with a copy of ${set.name}.`,
          );
        }
      } catch (caught) {
        if (!closed) {
          fail(
            store.describeLayerSetError(
              caught,
              clear ? 'clear the default' : `make ${set.name} the default`,
            ),
          );
        }
      }
    });
  }

  // Retry goes while the library loads. Focus goes back to it when the
  // read fails again, else to New layer set.
  async function retry() {
    await store.fetchLayerSets();
    await nextTick();

    if (focusLost()) {
      (
        rootEl.value?.querySelector('[data-testid="layer-sets-retry"]') ||
        newButton.value
      )?.focus();
    }
  }

  // Focus on a control that is gone moves to the card at the place of the
  // deleted one, else to New layer set, else to the tab.
  async function keepFocus(index) {
    await nextTick();

    if (!focusLost()) {
      return;
    }

    const edits = rootEl.value?.querySelectorAll(
      '[data-testid="layer-set-edit"]',
    );
    const target = edits?.length
      ? edits[Math.min(index, edits.length - 1)]
      : newButton.value;

    target?.focus();

    if (focusLost()) {
      emit('lost-focus');
    }
  }

  function confirmDelete() {
    const { set, index } = deleting.value || {};

    deleting.value = null;

    if (!set) {
      return;
    }

    run('delete', async () => {
      try {
        await store.deleteLayerSet(set.id);

        if (!closed) {
          status.set(`Deleted layer set ${set.name} from the server.`);
          await keepFocus(index);
        }
      } catch (caught) {
        if (!closed) {
          fail(store.describeLayerSetError(caught, `delete ${set.name}`));
        }
      }
    });
  }

  // --- selecting several sets -------------------------------------------

  // The selection, by ID, of the sets shown. Every set can be selected:
  // Download selected saves a built-in set too.
  const selection = reactive(useListSelection(shownSets, (set) => set.id));

  // Each change of the selection is said in a slot of its own, so quick
  // changes come to the last one.
  const cards = useCardSelection(selection, {
    items: shownSets,
    announce: (text) => store.announce(text, { slot: 'selection' }),
  });

  // While a change runs, the keys only move focus. Delete asks Delete
  // selected's question.
  function onCardKeydown(event, index) {
    cards.keydown(event, index, {
      locked: Boolean(busy.value),
      canDelete: !busy.value,
      onDelete: askDeleteSelected,
    });
  }

  // Runs a batch on `sets` with progress on the row. What it left undone
  // shows in a summary under the row, which takes focus.
  async function runSelected(label, sets, work) {
    if (busy.value || !sets.length) {
      return null;
    }

    clearMessages();
    outcome.value = null;
    busy.value = 'bulk';
    running.value = { label, done: 0, total: sets.length };

    try {
      return await work((done) => {
        running.value = { ...running.value, done };
      });
    } finally {
      busy.value = '';
      running.value = null;
    }
  }

  // Shows what a batch on `total` sets left undone: { item, reason } each.
  async function showFailures(failures, total, done) {
    outcome.value = {
      heading: bulkFailureHeading(
        failures.length,
        total,
        'layer set',
        'layer sets',
        done,
      ),
      items: failures.map(({ item, reason }) => ({
        key: item.id,
        name: item.name,
        reason,
      })),
    };
    await nextTick();
    summary?.focus();
  }

  // Saves a layer set file for each selected set, one after another, as
  // Download does.
  async function downloadSelected() {
    const sets = selection.selected;
    const incomplete = [];

    const failures = await runSelected(
      'Downloading',
      sets,
      async (progress) => {
        await iconLibrary.ensureImages(sets.flatMap(layerSetIconNames));

        const results = await runBulk(
          sets,
          async (set) => {
            if (
              downloadLayerSetFile(set, { library: iconLibrary, saveAs })
                .incomplete
            ) {
              incomplete.push(set.name);
            }
          },
          { limit: 1, onProgress: progress },
        );

        return bulkOutcome(results, (caught) =>
          `The Builder could not save the file. ${caught?.message || ''}`.trim(),
        ).failures;
      },
    );

    if (!failures || closed) {
      return;
    }

    if (failures.length) {
      await showFailures(failures, sets.length, 'downloaded');
    } else {
      status.set(layerSetsDownloadedMessage(sets.length, incomplete));
    }
  }

  // Delete selected asks once for the batch (WCAG 3.3.4) and keeps built-in
  // sets. For one set alone, it asks as that set's own Delete asks.
  function askDeleteSelected() {
    const chosen = selection.selected;

    if (!chosen.length || busy.value) {
      return;
    }

    const sets = chosen.filter((set) => !set.builtin);
    const builtins = chosen.filter((set) => set.builtin);

    if (!sets.length) {
      clearMessages();
      showFailures(
        builtins.map((item) => ({ item, reason: BUILTIN_NOT_DELETED })),
        builtins.length,
        'deleted',
      );
    } else if (sets.length === 1 && !builtins.length) {
      deleting.value = {
        set: sets[0],
        index: shownSets.value.indexOf(sets[0]),
      };
    } else {
      deletingSelected.value = { sets, builtins };
    }
  }

  // Deletes each selected set that is not built in. The dialog gives focus
  // back to Delete selected, which keeps it while the batch runs.
  async function confirmDeleteSelected() {
    const { sets, builtins } = deletingSelected.value || {};
    const chosen = [...(sets || []), ...(builtins || [])];

    deletingSelected.value = null;

    const result = await runSelected('Deleting', sets || [], (progress) =>
      store.deleteLayerSets(chosen, { onProgress: progress }),
    );

    if (!result || closed) {
      return;
    }

    if (result.failures.length) {
      await showFailures(result.failures, chosen.length, 'deleted');
    } else {
      status.set(`Deleted ${count(sets.length, 'layer set')} from the server.`);
      await keepFocus(0);
    }
  }

  // Dismiss removes the summary, so focus moves to Select all, or to the
  // first card.
  async function dismissOutcome() {
    outcome.value = null;
    await nextTick();

    const all = rootEl.value?.querySelector(
      '[data-testid="bulk-all-layer-sets"]',
    );

    if (all && !all.disabled) {
      all.focus();
    } else {
      await keepFocus(0);
    }
  }

  onMounted(() => {
    store.layerSetsReady();
    iconLibrary.ensure?.().catch(() => {});
  });

  onBeforeUnmount(() => {
    closed = true;
  });
</script>

<style scoped>
  .builder-layer-sets__note {
    margin: 0 0 0.75rem;
    color: var(--bx-text-muted);
  }

  .builder-layer-sets__actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem 0.6rem;
    margin-bottom: 0.75rem;
  }

  .builder-layer-sets__failed {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.75rem;
  }

  .builder-layer-sets__failed p {
    margin: 0;
    color: var(--bx-danger);
  }

  .builder-card h2 {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
    min-width: 0;
    margin: 0 0 0.25rem;
    font-weight: 700;
  }

  /* Says in words that the set is the default or built in. The frame only
     sets the word apart from the name. */
  .builder-layer-sets__tag {
    display: inline-block;
    padding: 0 0.35rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: var(--bx-radius);
    font-size: 0.75rem;
    font-weight: 600;
  }

  .builder-layer-sets__description {
    margin: 0 0 0.25rem;
  }

  .builder-card p.builder-card__meta {
    margin: 0;
  }

  .builder-layer-sets__layers {
    margin: 0.4rem 0 0;
    padding: 0;
    list-style: none;
    font-size: 0.85rem;
  }

  .builder-layer-sets__layers li + li {
    margin-top: 0.15rem;
  }

  /* A layer set card is wide enough for Clear default in half of it. */
  .builder-layer-sets__cards {
    grid-template-columns: repeat(auto-fill, minmax(min(20.5rem, 100%), 1fr));
  }

  /* Clear default takes the full row of a card too narrow for it in half. */
  @container builder-card (max-width: 18.5rem) {
    .builder-layer-sets__default {
      grid-column: 1 / -1;
    }
  }
</style>
