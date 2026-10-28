<!-- The drafts page's Node Templates tab: the server's template library, the
     same for every Builder user. Built-in and server file templates are read only. -->
<template>
  <div ref="rootEl" class="builder-templates">
    <div class="builder-templates__actions">
      <button
        type="button"
        class="builder-button builder-button--primary"
        data-testid="templates-new"
        aria-haspopup="dialog"
        :aria-disabled="busy || undefined"
        @click="busy || $emit('template', { mode: 'library-new' })">
        <builder-icon name="plus" :size="14" />
        New template
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="collections-new"
        aria-haspopup="dialog"
        :aria-disabled="busy || undefined"
        @click="busy || $emit('collection', {})">
        <builder-icon name="plus" :size="14" />
        New collection
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="templates-import"
        aria-haspopup="dialog"
        :aria-disabled="busy || undefined"
        @click="busy || (importing = true)">
        <builder-icon name="upload" :size="14" />
        Import templates
      </button>
    </div>

    <div
      v-if="library.error"
      class="builder-panel builder-error builder-templates__failed">
      <p role="alert" data-testid="templates-error">
        Could not load the template library. {{ library.error }}
      </p>
      <!-- While the read it asked for runs, a ring turns over its label, it
           keeps focus, and a second press does nothing. -->
      <builder-busy-button
        data-testid="templates-retry"
        :busy="loading"
        @click="retry">
        Retry
      </builder-busy-button>
    </div>

    <p v-if="!library.loaded && !library.error" role="status">Loading…</p>
    <template v-else-if="library.loaded">
      <!-- A library the server cannot read lists none of its templates,
           but still the built-in templates and the server's collections. -->
      <p v-if="library.damaged" data-testid="templates-damaged">
        The Builder cannot read the template library. A newer version of phenix
        may have saved it.
      </p>
      <div class="builder-field builder-templates__show">
        <label for="templates-show">Show</label>
        <select
          id="templates-show"
          v-model="shown"
          data-testid="templates-show">
          <option value="">All library templates</option>
          <!-- Offered while the server lists its disk images. -->
          <option
            v-if="disksKnown || imagesLost"
            :value="SHOW_MISSING_IMAGES"
            :disabled="imagesLost">
            {{
              imagesLost
                ? 'Templates with missing images (disk images unknown)'
                : 'Templates with missing images'
            }}
          </option>
          <optgroup
            v-if="choices.collections.length"
            label="Library collections">
            <option
              v-for="entry in choices.collections"
              :key="entry.value"
              :value="entry.value">
              {{ entry.label }}
            </option>
          </optgroup>
          <option :value="SHOW_BUILTIN">Built-in templates</option>
          <!-- The collections the server read from its template files. -->
          <optgroup
            v-if="choices.preloadedCollections.length"
            label="Server"
            data-testid="templates-show-preloaded">
            <option
              v-for="entry in choices.preloadedCollections"
              :key="entry.value"
              :value="entry.value">
              {{ entry.label }}
            </option>
          </optgroup>
        </select>
      </div>

      <!-- The built-in templates: what they are, and how to change one. -->
      <p
        v-if="list.source === 'builtin'"
        class="builder-templates__note"
        data-testid="templates-builtin-note">
        Built-in templates are read only. To change one, select Duplicate. This
        adds a copy to the library.
      </p>

      <!-- The library's templates whose images the server does not have. -->
      <p
        v-if="list.missingImages && items.length"
        class="builder-templates__note"
        data-testid="templates-missing-note">
        These templates name a drive image that the server does not have. To
        choose another image, select Edit.
      </p>

      <!-- The collection shown: what it is, and what can be done to it. -->
      <div
        v-if="collection"
        class="builder-templates__collection"
        data-testid="collection-block">
        <h2>
          {{ collection.name }}
          <span v-if="preloaded" class="builder-templates__tag">Read only</span>
        </h2>
        <p class="builder-card__meta" data-testid="collection-count">
          {{ count(items.length, 'template') }}
        </p>
        <p
          v-if="preloaded"
          class="builder-card__meta"
          data-testid="collection-preloaded">
          Read from a template file on the phenix server. To change a template,
          select Duplicate. This adds a copy to the library.
        </p>
        <p v-else-if="updated(collection)" class="builder-card__meta">
          Updated {{ updated(collection) }}{{ by(collection) }}
        </p>
        <p v-if="collection.description" data-testid="collection-about">
          {{ collection.description }}
        </p>
        <div v-if="mine" class="builder-templates__collection-actions">
          <builder-action-button
            action="edit"
            data-testid="collection-edit"
            aria-haspopup="dialog"
            :disabled="busy"
            @click="busy || $emit('collection', { id: collection.id })">
            Edit collection
          </builder-action-button>
          <builder-action-button
            action="delete"
            data-testid="collection-delete"
            aria-haspopup="dialog"
            :disabled="busy"
            @click="busy || askDeleteCollection()">
            Delete collection
          </builder-action-button>
          <builder-action-button
            action="download"
            data-testid="collection-export"
            :busy="exporting === 'collection'"
            :disabled="busy || !items.length"
            @click="exportCollection">
            Download collection
          </builder-action-button>
        </div>
        <div v-else class="builder-templates__collection-actions">
          <builder-action-button
            action="duplicate"
            data-testid="collection-duplicate"
            :disabled="busy || !items.length"
            @click="duplicateCollection">
            Duplicate collection
          </builder-action-button>
          <builder-action-button
            action="download"
            data-testid="collection-export"
            :busy="exporting === 'collection'"
            :disabled="busy || !items.length"
            @click="exportCollection">
            Download collection
          </builder-action-button>
        </div>
      </div>

      <!-- Filters the cards of the list Show chose. -->
      <builder-card-filter
        v-if="items.length"
        id="templates-filter"
        label="Filter templates"
        :model-value="filterText"
        :hint="filterHint"
        :announcement="filterHint || `${count(items.length, 'template')}.`"
        @update:model-value="setFilter"
        @announce="(text) => store.announce(text, { slot: 'filter' })" />

      <builder-bulk-bar
        v-if="selection.total"
        id="templates"
        :label="listLabel"
        :count="selection.count"
        :total="selection.total"
        :state="selection.state"
        :disabled="busy"
        @toggle-all="cards.selectAll">
        <!-- Each says how many are selected, so one that cannot act says
             why: "0 of 12 selected". -->
        <builder-menu-button
          v-if="mine && collectMenu.length"
          :items="collectMenu"
          :disabled="!selection.count || busy"
          testid="bulk-collect-templates"
          aria-describedby="bulk-count-templates"
          @select="addToCollection">
          <builder-icon name="library" :size="14" />
          Add to collection
        </builder-menu-button>
        <button
          v-if="mine && collection"
          type="button"
          class="builder-button"
          data-testid="bulk-uncollect-templates"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="removeFromCollection">
          <builder-icon name="close" :size="14" />
          Remove from collection
        </button>
        <builder-action-button
          v-if="!mine"
          action="duplicate"
          data-testid="bulk-duplicate-templates"
          aria-describedby="bulk-count-templates"
          :disabled="!selection.count || busy"
          @click="selection.count && duplicateTemplates(selection.selected)">
          Duplicate selected
        </builder-action-button>
        <builder-action-button
          action="download"
          data-testid="bulk-export-templates"
          aria-describedby="bulk-count-templates"
          :busy="exporting === 'selected'"
          :disabled="!selection.count || busy"
          @click="exportSelected">
          Download selected
        </builder-action-button>
        <builder-action-button
          v-if="mine"
          action="delete"
          data-testid="bulk-delete-templates"
          aria-haspopup="dialog"
          aria-describedby="bulk-count-templates"
          :disabled="!selection.count || busy"
          @click="askDeleteSelected">
          Delete selected
        </builder-action-button>
      </builder-bulk-bar>

      <p v-if="!items.length && !unreadable" data-testid="templates-empty">
        {{ emptyText }}
      </p>
      <p
        v-else-if="items.length && !shownItems.length"
        data-testid="templates-filter-none">
        No templates match the filter.
      </p>
      <ul
        v-else-if="items.length"
        class="builder-cards"
        :aria-busy="busy || undefined"
        data-testid="templates-list">
        <!-- A card is focusable, one at a time (the list's Tab stop), and
             named by its template and whether it is selected. Focus on it,
             or on a control in it, makes it the Tab stop. -->
        <li
          v-for="(template, index) in shownItems"
          :key="itemKey(template)"
          class="builder-card builder-panel"
          :class="{ 'is-selected': selection.has(template) }"
          :data-testid="testid('template-card', template)"
          :tabindex="selection.tabStop(template) ? 0 : -1"
          :aria-labelledby="`template-name-${index} template-state-${index}`"
          :aria-describedby="
            missingOf(template).length ? `template-images-${index}` : undefined
          "
          @focusin="selection.focused(template)"
          @keydown="onCardKeydown($event, index)">
          <div class="builder-card__head">
            <!-- The icon of devices made from it. The name says what the
                 template is, and the tag that it is read only. -->
            <h2 :id="`template-name-${index}`">
              <builder-icon
                :name="template.device?.iconKey || 'server'"
                :src="iconSrc(template.device?.icon, null, iconLibrary)"
                :size="18" />
              <span>
                {{ template.name }}
                <span
                  v-if="!mine"
                  class="builder-templates__tag"
                  :data-testid="testid('template-read-only', template)"
                  >Read only</span
                >
              </span>
            </h2>
            <!-- The checkbox, at the card's top right, is named for the card,
                 as its buttons are. -->
            <label class="builder-card__select">
              <input
                type="checkbox"
                :checked="selection.has(template)"
                :disabled="busy"
                :data-testid="testid('template-select', template)"
                @pointerdown="cards.shift.pointerdown"
                @keydown="cards.shift.keydown"
                @click="cards.shift.click"
                @change="cards.select(template, $event)" />
              <span class="builder-visually-hidden">
                Select {{ cardName(template) }}
              </span>
            </label>
          </div>
          <!-- The drive images the server does not have, which the card's
               description says. The sign and the words say it, not color. -->
          <p
            v-if="missingOf(template).length"
            :id="`template-images-${index}`"
            class="builder-templates__missing"
            :data-testid="testid('template-missing-images', template)">
            <builder-icon name="warning" :size="14" />
            <span>{{ missingImagesText(missingOf(template)) }}</span>
          </p>
          <!-- One line each: when it changed and who changed it, and the
               collections that hold it. -->
          <p
            v-if="updated(template)"
            class="builder-card__meta"
            :data-testid="testid('template-time', template)">
            Updated {{ updated(template) }}{{ by(template) }}
          </p>
          <p
            v-if="collectionsOf(template).length"
            class="builder-card__meta"
            :data-testid="testid('template-collections', template)">
            In {{ listOf(collectionsOf(template)) }}
          </p>
          <p
            v-if="template.description"
            class="builder-card__meta"
            :data-testid="testid('template-about', template)">
            {{ template.description }}
          </p>

          <!-- A template that cannot change has View, and Duplicate makes a
               copy that can. -->
          <div class="builder-card__actions">
            <builder-action-button
              v-if="mine"
              action="edit"
              :data-testid="testid('template-edit', template)"
              :aria-label="`Edit template ${cardName(template)}`"
              aria-haspopup="dialog"
              :disabled="busy"
              @click="
                busy ||
                $emit('template', { mode: 'library-edit', id: template.id })
              " />
            <builder-action-button
              v-else
              action="view"
              :data-testid="testid('template-view', template)"
              :aria-label="`View template ${cardName(template)}`"
              aria-haspopup="dialog"
              :disabled="busy"
              @click="
                busy ||
                $emit('template', {
                  mode: 'view',
                  id: template.id,
                  source: template.source,
                })
              " />
            <builder-action-button
              v-if="!mine"
              action="duplicate"
              :data-testid="testid('template-duplicate', template)"
              :aria-label="`Duplicate template ${cardName(template)} to the library`"
              :disabled="busy"
              @click="duplicateTemplates([template])" />
            <builder-action-button
              v-if="mine"
              action="delete"
              :data-testid="testid('template-delete', template)"
              :aria-label="`Delete template ${cardName(template)}`"
              aria-haspopup="dialog"
              :disabled="busy"
              @click="busy || askDelete([template])" />
            <builder-action-button
              action="download"
              :data-testid="testid('template-export', template)"
              :aria-label="`Download template ${cardName(template)}`"
              :busy="exporting === testid('template-export', template)"
              :disabled="busy"
              @click="exportTemplate(template)" />
          </div>
          <!-- Whether the card is selected, for its name only. -->
          <span :id="`template-state-${index}`" hidden>{{
            selection.has(template) ? 'selected' : 'not selected'
          }}</span>
        </li>
      </ul>
    </template>

    <!-- Reads a template file into the library as a new collection. -->
    <template-import-dialog v-if="importing" @close="importing = false" />

    <!-- A deleted template or collection cannot be brought back, so Delete
         asks first (WCAG 3.3.4): once, whatever is selected. -->
    <builder-confirm
      v-if="question"
      :id="question.id"
      :title="question.title"
      :message="question.message"
      :confirm-label="question.confirmLabel"
      action="delete"
      @cancel="question = null"
      @confirm="confirm" />
  </div>
</template>

<script setup>
  import { saveAs } from 'file-saver';
  import { computed, nextTick, reactive, ref, watch } from 'vue';

  import BuilderActionButton from './BuilderActionButton.vue';
  import BuilderBulkBar from './BuilderBulkBar.vue';
  import BuilderBusyButton from './BuilderBusyButton.vue';
  import BuilderCardFilter from './BuilderCardFilter.vue';
  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderMenuButton from './BuilderMenuButton.vue';
  import TemplateImportDialog from './dialogs/TemplateImportDialog.vue';

  import { count, listOf } from '@/builder/announce.js';
  import { busyTask, runBusy } from '@/builder/busy.js';
  import {
    filterItems,
    keepCardFilter,
    readCardFilter,
  } from '@/builder/cardFilter.js';
  import { focusLost } from '@/builder/commands.js';
  import { useDiskReads } from '@/builder/diskReads.js';
  import { formatTimestamp } from '@/builder/format.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { iconRefs, iconSrc } from '@/builder/icons.js';
  import { templateSearch } from '@/builder/librarySearch.js';
  import {
    useCardSelection,
    useListSelection,
  } from '@/builder/listSelection.js';
  import { LibraryError, useBuilderStore } from '@/builder/store.js';
  import { exportTemplateFile } from '@/builder/templateFile.js';
  import {
    SHOW_BUILTIN,
    SHOW_MISSING_IMAGES,
    collectionDeleteQuestion,
    collectionNames,
    copiedMessage,
    copyProblem,
    membersMessage,
    missingImages,
    missingImagesText,
    shownList,
    showChoices,
    templateKey,
    templatesDeleteQuestion,
    templatesDeletedMessage,
  } from '@/builder/templates.js';

  // The menu item that starts a new collection with the selected templates.
  const NEW_COLLECTION = 'new-collection';
  // What the row above a whole list calls it, by where its templates are
  // kept.
  const LIST_LABELS = {
    library: 'Library',
    builtin: 'Built-in templates',
    preloaded: 'Server',
  };

  const props = defineProps({
    // The tab is shown. A hidden tab does not read the disk images.
    active: { type: Boolean, default: true },
  });

  const emit = defineEmits([
    // ({mode, id?, source?}): the template editor, on a new template of the
    // library ('library-new'), on one it has ('library-edit'), or on one
    // of `source`, read only ('view').
    'template',
    // ({id?, templateIds?}): the collection dialog, on the collection `id`
    // names, or on a new one, which starts with those templates.
    'collection',
  ]);

  const store = useBuilderStore();
  const rootEl = ref(null);

  const library = computed(() => store.templates);
  const collections = computed(() => store.libraryCollections);
  const loading = computed(() => library.value.status === 'loading');
  // The library is taking a change. Nothing else changes meanwhile.
  const busy = ref(false);
  // The Import templates dialog is open.
  const importing = ref(false);

  // The cards say which drive images the server does not have. The tab
  // reads the disk images when it shows, as the editor does when it opens.
  // While they are unknown, nothing is said of images.
  useDiskReads(store, () => props.active);
  const disksKnown = computed(() => Array.isArray(store.disks));

  // What Show offers, and the list shown: '' for every template of the
  // library, SHOW_MISSING_IMAGES for those with missing images, a
  // collection's id, SHOW_BUILTIN for the built-in templates, or the key of
  // a collection of the server's (see shownList). A list that leaves the
  // library gives way to all the templates of the library.
  const choices = computed(() => showChoices(library.value));
  const shown = ref('');
  const listFor = (value) =>
    shownList(library.value, value, { disks: store.disks });
  // Show is on the templates with missing images, and the disk images are
  // unknown. The choice stays, with no templates, until they are known.
  const imagesLost = computed(
    () => shown.value === SHOW_MISSING_IMAGES && !disksKnown.value,
  );
  const list = computed(() =>
    imagesLost.value
      ? {
          source: 'library',
          collection: null,
          missingImages: true,
          templates: [],
        }
      : listFor(shown.value) || listFor(''),
  );
  const collection = computed(() => list.value.collection);
  // The list holds templates of the library, which can change. For a
  // damaged library, these are the templates the server could not read.
  const mine = computed(() => list.value.source === 'library');
  // The list is a collection the server read from a template file.
  const preloaded = computed(() => list.value.source === 'preloaded');
  const unreadable = computed(() => mine.value && library.value.damaged);

  watch(list, () => {
    if (shown.value && !imagesLost.value && !listFor(shown.value)) {
      shown.value = '';
    }
  });

  watch(imagesLost, (lost) => {
    if (lost) {
      store.announce(
        'The server’s disk images are unknown. The Builder cannot list the templates with missing images.',
      );
    }
  });

  // A collection lists its templates in the order they joined it.
  const items = computed(() => list.value.templates);

  // The filter's text, kept for the browser tab's session as a drafts
  // tab's is. It stays when Show chooses another list.
  const filterText = ref(readCardFilter('templates'));

  // The cards shown: the templates of the list that match the filter, by
  // what t: in the command palette finds them by.
  const shownItems = computed(() =>
    filterItems(items.value, filterText.value, (template) =>
      templateSearch(template, library.value),
    ),
  );

  // "2 of 5 templates shown.", or '' with no filter.
  const filterHint = computed(() =>
    filterText.value.trim()
      ? `${shownItems.value.length} of ${count(items.value.length, 'template')} shown.`
      : '',
  );

  function setFilter(text) {
    filterText.value = text;
    keepCardFilter('templates', text);
  }

  const emptyText = computed(() => {
    if (imagesLost.value) {
      return 'The server’s disk images are unknown. The Builder cannot list the templates with missing images. To see templates, choose another list in Show.';
    }

    if (list.value.missingImages) {
      return 'The server has the disk images of every library template.';
    }

    if (collection.value) {
      return mine.value
        ? 'This collection has no templates. Select templates under All library templates, then Add to collection.'
        : 'This collection has no templates.';
    }

    // The tab counts only the library's templates. Show lists the others.
    const elsewhere =
      mine.value &&
      (listFor(SHOW_BUILTIN)?.templates.length ||
        choices.value.preloadedCollections.length);

    return `The library has no templates. Select New template to make one.${
      elsewhere
        ? ' Built-in templates and server collections are under Show.'
        : ''
    }`;
  });

  // What the row above the list and an export of the selection call it.
  const listLabel = computed(() => {
    if (collection.value) {
      return collection.value.name;
    }

    return list.value.missingImages
      ? 'Templates with missing images'
      : LIST_LABELS[list.value.source];
  });

  // --- cards -------------------------------------------------------------

  // The drive images of each card's template that the server does not have.
  const missing = computed(() => missingImages(items.value, store.disks));

  function missingOf(template) {
    return missing.value.get(templateKey(template.source, template.id)) || [];
  }

  function updated(item) {
    return formatTimestamp(item.updated);
  }

  // Who changed a template or collection last, after when: " by alice".
  function by(item) {
    return item.updatedBy ? ` by ${item.updatedBy}` : '';
  }

  // Names need not differ. A card's checkbox and buttons name its template,
  // and add when it last changed for one that shares its name with another
  // of the list (WCAG 2.4.6).
  function cardName(template) {
    const when = updated(template);
    const twins = items.value.filter((other) => other.name === template.name);

    return when && twins.length > 1
      ? `${template.name}, updated ${when}`
      : template.name;
  }

  // The names of the collections that hold a template.
  function collectionsOf(template) {
    return collectionNames(library.value, template);
  }

  // A template once in every list: by where it is kept and its id.
  function itemKey(template) {
    return `${template.source}/${template.id}`;
  }

  // A card's test ids: a template of the library by its id, the others by
  // their source and id.
  function testid(prefix, template) {
    return template.source === 'library'
      ? `${prefix}-${template.id}`
      : `${prefix}-${template.source}-${template.id}`;
  }

  // --- selection ---------------------------------------------------------

  // The selection of the cards shown, which also holds the list's Tab stop.
  // Every card can be selected. A card the filter hides leaves it.
  const selection = reactive(useListSelection(shownItems, itemKey));

  // The selection is that of the list shown: another list starts with
  // nothing selected, also of the templates it shares with the last one.
  watch(shown, () => selection.clear());

  // Each change of the selection is said in a slot of its own, so quick
  // changes come to the last one.
  const cards = useCardSelection(selection, {
    items: shownItems,
    announce: (text) => store.announce(text, { slot: 'selection' }),
  });

  // While the library takes a change, the keys only move focus. Delete asks
  // Delete selected's question.
  function onCardKeydown(event, index) {
    cards.keydown(event, index, {
      locked: busy.value,
      canDelete: mine.value,
      onDelete: askDeleteSelected,
    });
  }

  // --- changes -----------------------------------------------------------

  // Runs one change of the library, unless one is in progress. The page's
  // alert reports a failure, as `action` names what could not be done.
  async function change(action, work) {
    if (busy.value) {
      return false;
    }

    busy.value = true;
    store.clearError();

    try {
      await work();

      return true;
    } catch (error) {
      store.setError(store.describeLibraryError(error, action));

      return false;
    } finally {
      busy.value = false;
    }
  }

  // After a change that removes the control focus was on: Select all, or
  // the tab when the list has no row left.
  async function focusList() {
    await nextTick();

    const all = rootEl.value?.querySelector(
      '[data-testid="bulk-all-templates"]',
    );

    if (all && !all.disabled) {
      all.focus();
    } else {
      document.getElementById('tab-templates')?.focus();
    }
  }

  // The collections the selected templates can join: every one but the one
  // shown, then a new one.
  const collectMenu = computed(() => [
    ...collections.value
      .filter((entry) => entry.id !== shown.value)
      .map((entry) => ({ id: entry.id, label: entry.name })),
    { id: NEW_COLLECTION, label: 'New collection…', separator: true },
  ]);

  function addToCollection(item) {
    const templates = selection.selected;

    if (!templates.length || busy.value) {
      return;
    }

    if (item.id === NEW_COLLECTION) {
      emit('collection', {
        templateIds: templates.map((template) => template.id),
      });

      return;
    }

    const target = collections.value.find((entry) => entry.id === item.id);

    if (!target) {
      return;
    }

    // Those it does not hold yet: the others stay where they are.
    const joining = templates.filter(
      (template) => !(target.templateIds || []).includes(template.id),
    );

    if (!joining.length) {
      store.announce(
        templates.length === 1
          ? `${templates[0].name} is already in ${target.name}.`
          : `The selected templates are already in ${target.name}.`,
      );

      return;
    }

    change(`add to collection ${target.name}`, async () => {
      await store.changeCollectionMembers(target.id, {
        add: joining.map((template) => template.id),
      });
      store.announce(membersMessage('add', joining, target.name));
    });
  }

  // The templates leave the collection, and so its list: focus moves on.
  async function removeFromCollection() {
    const templates = selection.selected;
    const from = collection.value;

    if (!templates.length || !from) {
      return;
    }

    const done = await change(
      `remove from collection ${from.name}`,
      async () => {
        await store.changeCollectionMembers(from.id, {
          remove: templates.map((template) => template.id),
        });
        store.announce(membersMessage('remove', templates, from.name));
      },
    );

    if (done) {
      await focusList();
    }
  }

  // --- duplicating ------------------------------------------------------

  // What a change names: one template by its name, several by their count.
  function subject(templates) {
    return templates.length === 1
      ? `template ${templates[0].name}`
      : count(templates.length, 'template');
  }

  // Copies of read-only templates, naming the custom icons they name, go to
  // the library, as a collection of their own when `from` is one. A library
  // at its most templates takes none.
  async function duplicateTemplates(templates, from = null) {
    if (!templates.length || busy.value) {
      return;
    }

    const action = from
      ? `duplicate collection ${from.name}`
      : `duplicate ${subject(templates)}`;
    const done = await change(action, async () => {
      const full = copyProblem(library.value, templates.length);

      if (full) {
        throw new LibraryError(full);
      }

      await store.createLibraryTemplates(templates, {
        collection: from
          ? {
              name: from.name,
              ...(from.description ? { description: from.description } : {}),
            }
          : null,
      });
      store.announce(copiedMessage(templates, from));
    });

    // The copies are in the library now: a second Duplicate would add more.
    if (done && !from) {
      selection.clear();
    }
  }

  function duplicateCollection() {
    duplicateTemplates(items.value, collection.value);
  }

  // --- exporting ---------------------------------------------------------

  // One export at a time: the task, a card's test id, 'selected' or
  // 'collection', or ''.
  const EXPORTS = 'template-export';
  const exporting = computed(() => busyTask(EXPORTS));

  // Saves templates as a template file of a collection named `name`, which
  // carries the custom icons they name from the icon library, read first
  // when it was not. The page's live region announces what was saved, and
  // the page's alert says why a save failed. While the icon library is
  // read, the button that started the export (`task`) shows a ring.
  async function exportTemplates(name, templates, description, task) {
    if (!templates.length || busy.value || exporting.value) {
      return;
    }

    store.clearError();

    await runBusy(EXPORTS, task, async () => {
      try {
        // The library has no images: the file waits for those it carries.
        await iconLibrary.ensureImages(iconRefs({ templates }));

        const { message } = exportTemplateFile(
          { name, description, templates },
          { library: iconLibrary, saveAs },
        );

        store.announce(message);
      } catch (error) {
        store.setError(
          `Could not download the templates. ${error?.message || ''}`.trim(),
        );
      }
    });
  }

  // One template, as a collection of its own name.
  function exportTemplate(template) {
    exportTemplates(
      template.name,
      [template],
      template.description || '',
      testid('template-export', template),
    );
  }

  // The selection, as a collection named after the list shown.
  function exportSelected() {
    if (selection.count) {
      exportTemplates(listLabel.value, selection.selected, '', 'selected');
    }
  }

  // The collection shown, as it is.
  function exportCollection() {
    exportTemplates(
      collection.value.name,
      items.value,
      collection.value.description || '',
      'collection',
    );
  }

  // --- deleting ----------------------------------------------------------

  // What Delete asks about: { id, title, message, confirmLabel }, with the
  // templates to delete, or the collection.
  const question = ref(null);

  function askDelete(templates) {
    question.value = {
      id: 'templates-delete',
      ...templatesDeleteQuestion(templates),
      templates,
    };
  }

  function askDeleteSelected() {
    if (selection.count && !busy.value) {
      askDelete(selection.selected);
    }
  }

  function askDeleteCollection() {
    question.value = {
      id: 'collection-delete',
      ...collectionDeleteQuestion(collection.value),
      collection: collection.value,
    };
  }

  // The confirmation gives focus back to the button that asked. A template's
  // card goes with it, and a collection's block: focus then moves to Select
  // all, or to the Show field for a collection.
  async function confirm() {
    const { templates, collection: gone } = question.value;

    question.value = null;

    if (gone) {
      const done = await change(`delete collection ${gone.name}`, async () => {
        await store.deleteLibraryItems({ collections: [gone.id] });
        store.announce(`Deleted collection ${gone.name}.`);
      });

      if (done) {
        await nextTick();
        document.getElementById('templates-show')?.focus();
      }

      return;
    }

    const done = await change(
      templates.length === 1 ? 'delete the template' : 'delete the templates',
      async () => {
        await store.deleteLibraryItems({
          templates: templates.map((template) => template.id),
        });
        store.announce(templatesDeletedMessage(templates));
      },
    );

    if (done) {
      await focusList();
    }
  }

  // For Find collection: shows the list that a Show value names and focuses
  // Show. Returns whether the list is shown.
  async function showList(value) {
    if (!listFor(value)) {
      return false;
    }

    shown.value = value;
    await nextTick();
    document.getElementById('templates-show')?.focus();

    return true;
  }

  defineExpose({ showList });

  // Retry disappears after a successful read of the library. Focus then
  // moves to the tab and does not fall to the page.
  async function retry() {
    if (loading.value) {
      return;
    }

    await store.fetchTemplates();
    await nextTick();

    if (focusLost()) {
      document.getElementById('tab-templates')?.focus();
    }
  }
</script>

<style scoped>
  .builder-templates__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    margin-bottom: 0.75rem;
  }

  .builder-templates__note {
    margin: 0 0 0.75rem;
  }

  .builder-templates__failed {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
    padding: 0.6rem 0.75rem;
    overflow-wrap: anywhere;
  }

  .builder-templates__failed p {
    margin: 0;
  }

  /* The field is as wide as its longest option, not the page. */
  .builder-templates__show {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
  }

  .builder-templates__show label {
    margin: 0;
  }

  .builder-templates__show select {
    width: auto;
    max-width: 100%;
  }

  .builder-templates__collection {
    margin-bottom: 0.75rem;
    overflow-wrap: anywhere;
  }

  .builder-templates__collection h2 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 700;
  }

  .builder-templates__collection p {
    margin: 0;
  }

  .builder-templates__collection-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    margin-top: 0.5rem;
  }

  .builder-cards {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  /* The icon, then the name, which wraps beside it. */
  .builder-card h2 {
    display: flex;
    align-items: flex-start;
    gap: 0.4rem;
    min-width: 0;
    margin: 0 0 0.25rem;
    font-weight: 700;
  }

  .builder-card h2 > :first-child {
    flex: none;
    margin-top: 0.15em;
  }

  /* When it changed, its collections and its description, a line each. */
  .builder-card p.builder-card__meta {
    margin: 0;
  }

  /* A template or collection everyone who can use the Builder can use. */
  .builder-templates__tag {
    display: inline-block;
    margin-left: 0.25rem;
    padding: 0 0.35rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: var(--bx-radius);
    font-size: 0.75rem;
    font-weight: 600;
    vertical-align: middle;
  }

  /* The warning sign is decorative: the text says the same. */
  .builder-templates__missing {
    display: flex;
    align-items: flex-start;
    gap: 0.35rem;
    margin: 0 0 0.25rem;
    font-weight: 600;
    color: var(--bx-warning);
    overflow-wrap: anywhere;
  }

  .builder-templates__missing > :first-child {
    flex: none;
    margin-top: 0.15em;
  }

  .builder-button[aria-busy='true'] {
    cursor: progress;
  }
</style>
