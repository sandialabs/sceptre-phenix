<!--
  The drafts page's Layers tab: the server's layer set library, one for
  every Builder user (see layerSetStore.js). The view (Builder.vue)
  puts it in the layers slot of BuilderDrafts.

  Each set has a card: its icon and name, Default when it is the set that
  new blank diagrams copy, Built-in for a set that is part of
  phenix, its description, how many layers it has and who changed it last
  and when, and its layers, each with its icon and color (LayerName.vue).

  - New layer set and a card's Edit open the layers editor on the set
    (dialogs/LayersDialog.vue, library mode). A built-in set cannot change:
    its card has View, which opens the editor read only, and no Delete.
  - Upload layer set… reads a layer set file (YAML or JSON, as Download
    saves it) into the library as a new set (the store's
    uploadLayerSetFile). The custom icons it carries go into the icon
    library. A file that cannot be read says why, with its problems.
  - Download saves the set as a YAML layer set file with the custom icons
    it names (see downloadLayerSetFile).
  - Duplicate adds a copy, named with a number.
  - Delete asks first. Diagrams keep their copies of the layers.
  - Set as default makes the set the one new blank diagrams of every user
    copy. Clear default removes the choice: new diagrams then have the
    placeholder layers.

  The status and error regions are rendered, empty, from the start. Focus
  moves to the card's Edit after a new set, an upload or a duplicate, and
  to the next card's Edit, else to New layer set, after a delete.
-->
<template>
  <div ref="rootEl" class="builder-layer-sets" data-testid="layer-sets-tab">
    <p class="builder-layer-sets__note">
      A layer set is a list of layers that every user of this server can use. In
      a diagram, Edit layers… applies a copy of a set. New blank diagrams copy
      the default set of the server. A layer can have node matchers, which put
      devices at the layer by their fields.
    </p>

    <div class="builder-layer-sets__actions">
      <button
        ref="newButton"
        type="button"
        class="builder-button builder-button--primary"
        aria-haspopup="dialog"
        :aria-disabled="busy || undefined"
        data-testid="layer-sets-new"
        @click="busy || openEditor('')">
        <builder-icon name="plus" :size="14" />
        New layer set
      </button>
      <button
        type="button"
        class="builder-button"
        :aria-disabled="busy || undefined"
        :aria-busy="busy === 'upload' || undefined"
        aria-describedby="layer-sets-upload-hint"
        data-testid="layer-sets-upload"
        @click="busy || fileField?.click()">
        <builder-icon name="upload" :size="14" />
        Upload layer set…
      </button>
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
      <span id="layer-sets-upload-hint" class="builder-layer-sets__note">
        A YAML or JSON file of one layer set, as Download saves it.
      </span>
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
      <p role="alert" data-testid="layer-sets-failed">
        Could not load the layer sets. {{ library.error }}
      </p>
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
      <p v-if="library.damaged" data-testid="layer-sets-damaged">
        The server cannot read
        {{ count(library.damaged, 'layer set') }}. A newer version of phenix may
        have saved {{ library.damaged === 1 ? 'it' : 'them' }}.
      </p>
      <ul class="builder-cards" data-testid="layer-sets-list">
        <li
          v-for="(set, index) in library.items"
          :key="set.id"
          class="builder-card builder-panel builder-layer-sets__card"
          :data-layer-set="set.id"
          :data-testid="`layer-set-card-${set.id}`">
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
              <span
                v-if="set.builtin"
                class="builder-layer-sets__tag"
                data-testid="layer-set-builtin"
                >Built-in</span
              >
            </h2>
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
          <p
            v-if="changedText(set)"
            class="builder-card__meta"
            data-testid="layer-set-updated">
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
            <button
              type="button"
              class="builder-button"
              aria-haspopup="dialog"
              :aria-label="`${set.builtin ? 'View' : 'Edit'} ${set.name}`"
              :aria-disabled="busy || undefined"
              data-testid="layer-set-edit"
              @click="busy || openEditor(set.id)">
              {{ set.builtin ? 'View' : 'Edit' }}
            </button>
            <button
              type="button"
              class="builder-button"
              :aria-label="`Duplicate ${set.name}`"
              :aria-disabled="busy || undefined"
              data-testid="layer-set-duplicate"
              @click="duplicate(set)">
              Duplicate
            </button>
            <button
              type="button"
              class="builder-button"
              :aria-label="`Download ${set.name}`"
              data-testid="layer-set-download"
              @click="download(set)">
              <builder-icon name="download" :size="14" />
              Download
            </button>
            <button
              type="button"
              class="builder-button"
              :aria-label="`${set.id === library.defaultId ? 'Clear default' : 'Set as default'}: ${set.name}`"
              :aria-disabled="busy || undefined"
              data-testid="layer-set-default-toggle"
              @click="toggleDefault(set)">
              {{
                set.id === library.defaultId
                  ? 'Clear default'
                  : 'Set as default'
              }}
            </button>
            <button
              v-if="!set.builtin"
              type="button"
              class="builder-button"
              aria-haspopup="dialog"
              :aria-label="`Delete ${set.name}`"
              :aria-disabled="busy || undefined"
              data-testid="layer-set-delete"
              @click="busy || (deleting = { set, index })">
              Delete
            </button>
          </div>
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
      @cancel="deleting = null"
      @confirm="confirmDelete" />
  </div>
</template>

<script setup>
  import { saveAs } from 'file-saver';
  import { computed, nextTick, onMounted, onBeforeUnmount, ref } from 'vue';

  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import LayersDialog from './dialogs/LayersDialog.vue';
  import { useMessage } from './dialogs/message.js';
  import LayerName from './layers/LayerName.vue';

  import { count } from '@/builder/announce.js';
  import { isIconKey } from '@/builder/catalog.js';
  import { focusLost } from '@/builder/commands.js';
  import { formatTimestamp } from '@/builder/format.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { iconSrc } from '@/builder/icons.js';
  import {
    downloadLayerSetFile,
    MAX_LAYER_SET_FILE_BYTES,
  } from '@/builder/layerSetFile.js';
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
  // What is in progress: 'upload', 'duplicate', 'default' or 'delete', or
  // ''. A press meanwhile does nothing.
  const busy = ref('');
  // The ID of the set the layers editor edits, '' for a new set, or null
  // while it is closed.
  const editing = ref(null);
  // The set Delete asks about, and the place of its card, or null.
  const deleting = ref(null);
  // Set when the tab goes. An answer that comes later changes nothing.
  let closed = false;

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

  // Focuses a card's Edit once the list shows it.
  async function focusEdit(id) {
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

  function download(set) {
    clearMessages();

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

  .builder-layer-sets__actions .builder-layer-sets__note {
    margin: 0;
    font-size: 0.85rem;
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
</style>
