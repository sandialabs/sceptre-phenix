<!--
  The drafts page's Node Templates tab: the template library.

  The server keeps one library for every Builder user, who can add, change
  and delete its templates and collections.

  The view (Builder.vue) puts it in the templates slot of BuilderDrafts. It
  lists the library's templates, a card each, or those of one of its
  collections (Show). The command palette's Find collection sets Show
  through showList. New template and a card's Edit open the template
  editor, and New collection and Edit collection the collection dialog:
  both are the view's, which this asks for (template, collection). A card
  that cannot be changed (built-in, or from a template file) has View,
  which opens the template editor read only.

  The user can act on several templates at once: each card has a checkbox, and
  the row above the list (BuilderBulkBar.vue) has Select all, how many are
  selected, Add to collection (a menu of the collections, then "New
  collection…", which starts one with the selected templates), Remove from
  collection while a collection is shown, and Delete selected. The selection is that of the list shown: another
  list drops it. Every change of it is announced. The cards are one Tab
  stop, and the keys of
  listSelection.js move through them and select (onListKeydown): arrow
  keys, Home and End move, Space selects, Shift with Space or an arrow key
  selects a range (a checkbox pressed with Shift makes the range as the
  box now is), Mod+A every card, Escape none, and Delete (Backspace on
  macOS) asks Delete selected's question.

  Deleting cannot be undone, so it asks first, once for the whole selection,
  and is one request. A deleted template leaves its collections. A deleted
  collection leaves its templates. Devices made from a template do not
  change, and diagrams keep their copies of it. While the library takes a
  change, nothing else can change. The page's live region announces the
  result of the change, and the page's alert says why it failed.

  The built-in templates (Show: Built-in templates) and the collections the
  server read from its template files at start (under Server in Show, each
  by its name) are read only for everyone. Their cards say so, and have
  View and Export, and Duplicate, which adds a copy to the library, where it
  can be changed. A collection of a
  template file is duplicated whole, as a collection of the library.

  A library that could not be read says why, with Retry, above whatever an
  earlier read listed. One the server cannot read (damaged) lists none of
  its templates, but still the built-in templates and the server's
  collections.

  Export, on a card, on the selection and on a collection shown, saves a
  YAML template file (see templateFile.js) that carries the custom icons
  its templates name. Templates of every source export. Import templates
  opens a dialog that reads such a file into the library as a new
  collection (dialogs/TemplateImportDialog.vue).
-->
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
      <!-- While the read it asked for runs, it keeps focus, and a second
           press does nothing. -->
      <button
        type="button"
        class="builder-button"
        data-testid="templates-retry"
        :aria-disabled="loading || undefined"
        :aria-busy="loading || undefined"
        @click="retry">
        Retry
      </button>
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

      <!-- The collection shown: what it is, and what can be done to it. -->
      <div
        v-if="collection"
        class="builder-templates__collection"
        data-testid="collection-block">
        <h2>
          {{ collection.name }}
          <span
            v-if="preloaded"
            class="builder-templates__tag"
            data-testid="collection-read-only"
            >Read only</span
          >
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
        <p
          v-else-if="updated(collection)"
          class="builder-card__meta"
          data-testid="collection-time">
          Updated {{ updated(collection) }}{{ by(collection) }}
        </p>
        <p v-if="collection.description" data-testid="collection-about">
          {{ collection.description }}
        </p>
        <div v-if="mine" class="builder-templates__collection-actions">
          <button
            type="button"
            class="builder-button"
            data-testid="collection-edit"
            aria-haspopup="dialog"
            :aria-disabled="busy || undefined"
            @click="busy || $emit('collection', { id: collection.id })">
            Edit collection
          </button>
          <button
            type="button"
            class="builder-button"
            data-testid="collection-delete"
            aria-haspopup="dialog"
            :aria-disabled="busy || undefined"
            @click="busy || askDeleteCollection()">
            Delete collection
          </button>
          <button
            type="button"
            class="builder-button"
            data-testid="collection-export"
            :aria-disabled="busy || !items.length || undefined"
            @click="exportCollection">
            <builder-icon name="download" :size="14" />
            Export collection
          </button>
        </div>
        <div v-else class="builder-templates__collection-actions">
          <button
            type="button"
            class="builder-button"
            data-testid="collection-duplicate"
            :aria-disabled="busy || !items.length || undefined"
            @click="duplicateCollection">
            Duplicate collection
          </button>
          <button
            type="button"
            class="builder-button"
            data-testid="collection-export"
            :aria-disabled="busy || !items.length || undefined"
            @click="exportCollection">
            <builder-icon name="download" :size="14" />
            Export collection
          </button>
        </div>
      </div>

      <builder-bulk-bar
        v-if="selection.total"
        id="templates"
        :label="collection ? collection.name : LIST_LABELS[list.source]"
        :count="selection.count"
        :total="selection.total"
        :state="selection.state"
        :disabled="busy"
        @toggle-all="selectAll">
        <!-- Each says how many are selected, so one that cannot act says
             why: "0 of 12 selected". -->
        <builder-menu-button
          v-if="mine && collectMenu.length"
          :items="collectMenu"
          :disabled="!selection.count || busy"
          testid="bulk-collect-templates"
          aria-describedby="bulk-count-templates"
          @select="addToCollection">
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
          Remove from collection
        </button>
        <button
          v-if="mine"
          type="button"
          class="builder-button"
          data-testid="bulk-delete-templates"
          aria-haspopup="dialog"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="askDeleteSelected">
          Delete selected
        </button>
        <button
          v-if="!mine"
          type="button"
          class="builder-button"
          data-testid="bulk-duplicate-templates"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="selection.count && duplicateTemplates(selection.selected)">
          Duplicate selected
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="bulk-export-templates"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="exportSelected">
          Export selected
        </button>
      </builder-bulk-bar>

      <p v-if="!items.length && !unreadable" data-testid="templates-empty">
        {{ emptyText }}
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
          v-for="(template, index) in items"
          :key="itemKey(template)"
          class="builder-card builder-panel"
          :class="{ 'is-selected': selection.has(template) }"
          :data-testid="testid('template-card', template)"
          :tabindex="selection.tabStop(template) ? 0 : -1"
          :aria-labelledby="`template-name-${index} template-state-${index}`"
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
                @pointerdown="shift.pointerdown"
                @keydown="shift.keydown"
                @click="shift.click"
                @change="select(template, $event)" />
              <span class="builder-visually-hidden">
                Select {{ cardName(template) }}
              </span>
            </label>
          </div>
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

          <div class="builder-card__actions">
            <button
              v-if="mine"
              type="button"
              class="builder-button"
              :data-testid="testid('template-edit', template)"
              :aria-label="`Edit template ${cardName(template)}`"
              aria-haspopup="dialog"
              :aria-disabled="busy || undefined"
              @click="
                busy ||
                $emit('template', { mode: 'library-edit', id: template.id })
              ">
              <builder-icon name="pencil" :size="14" />
              Edit
            </button>
            <button
              v-else
              type="button"
              class="builder-button"
              :data-testid="testid('template-view', template)"
              :aria-label="`View template ${cardName(template)}`"
              aria-haspopup="dialog"
              :aria-disabled="busy || undefined"
              @click="
                busy ||
                $emit('template', {
                  mode: 'view',
                  id: template.id,
                  source: template.source,
                })
              ">
              View
            </button>
            <button
              v-if="!mine"
              type="button"
              class="builder-button"
              :data-testid="testid('template-duplicate', template)"
              :aria-label="`Duplicate template ${cardName(template)} to the library`"
              :aria-disabled="busy || undefined"
              @click="duplicateTemplates([template])">
              Duplicate
            </button>
            <button
              v-if="mine"
              type="button"
              class="builder-button"
              :data-testid="testid('template-delete', template)"
              :aria-label="`Delete template ${cardName(template)}`"
              aria-haspopup="dialog"
              :aria-disabled="busy || undefined"
              @click="busy || askDelete([template])">
              Delete
            </button>
            <button
              type="button"
              class="builder-button"
              :data-testid="testid('template-export', template)"
              :aria-label="`Export template ${cardName(template)}`"
              :aria-disabled="busy || undefined"
              @click="exportTemplate(template)">
              Export
            </button>
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
      @cancel="question = null"
      @confirm="confirm" />
  </div>
</template>

<script setup>
  import { saveAs } from 'file-saver';
  import { computed, nextTick, reactive, ref, watch } from 'vue';

  import BuilderBulkBar from './BuilderBulkBar.vue';
  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderMenuButton from './BuilderMenuButton.vue';
  import TemplateImportDialog from './dialogs/TemplateImportDialog.vue';

  import { count, listOf } from '@/builder/announce.js';
  import { focusLost } from '@/builder/commands.js';
  import { formatTimestamp } from '@/builder/format.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { iconSrc } from '@/builder/icons.js';
  import {
    columnsOf,
    onListKeydown,
    shiftPress,
    useListSelection,
  } from '@/builder/listSelection.js';
  import { LibraryError, useBuilderStore } from '@/builder/store.js';
  import { exportTemplateFile } from '@/builder/templateFile.js';
  import {
    SHOW_BUILTIN,
    collectionDeleteQuestion,
    collectionNames,
    copiedMessage,
    copyProblem,
    membersMessage,
    shownList,
    showChoices,
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

  // What Show offers, and the list shown: '' for every template of the
  // library, a collection's id, SHOW_BUILTIN for the built-in templates, or
  // the key of a collection of the server's (see shownList). A list that
  // leaves the library gives way to all its templates.
  const choices = computed(() => showChoices(library.value));
  const shown = ref('');
  const list = computed(
    () => shownList(library.value, shown.value) || shownList(library.value, ''),
  );
  const collection = computed(() => list.value.collection);
  // The list holds templates of the library, which can change. For a
  // damaged library, these are the templates the server could not read.
  const mine = computed(() => list.value.source === 'library');
  // The list is a collection the server read from a template file.
  const preloaded = computed(() => list.value.source === 'preloaded');
  const unreadable = computed(() => mine.value && library.value.damaged);

  watch(list, () => {
    if (shown.value && !shownList(library.value, shown.value)) {
      shown.value = '';
    }
  });

  // A collection lists its templates in the order they joined it.
  const items = computed(() => list.value.templates);

  const emptyText = computed(() => {
    if (collection.value) {
      return mine.value
        ? 'This collection has no templates. Select templates under All library templates, then Add to collection.'
        : 'This collection has no templates.';
    }

    return 'The library has no templates. Select New template to make one.';
  });

  // --- cards -------------------------------------------------------------

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

  // Every card is in it, for the keys to move to, and the card that is the
  // list's Tab stop. Every card can be selected.
  const selection = reactive(useListSelection(items, itemKey));

  // The selection is that of the list shown: another list starts with
  // nothing selected, also of the templates it shares with the last one.
  watch(shown, () => selection.clear());

  // Every change of the selection is said, in a slot of its own, so quick
  // changes come to the last one.
  function announceSelection() {
    store.announce(`${selection.count} of ${selection.total} selected.`, {
      slot: 'selection',
    });
  }

  // Whether Shift was held as a card's checkbox was pressed.
  const shift = shiftPress();

  // A card's checkbox: with Shift, the range from the card last pressed
  // becomes as the box now is. The box then shows what the selection holds.
  function select(template, event) {
    selection.press(template, {
      checked: event.target.checked,
      range: shift.take(),
    });
    event.target.checked = selection.has(template);
    announceSelection();
  }

  // Focuses the card at a position of the list, which becomes its Tab
  // stop.
  function focusCard(index) {
    const template = items.value[index];
    const card = rootEl.value?.querySelector('[data-testid="templates-list"]')
      ?.children[index];

    if (template && card) {
      selection.focused(template);
      card.focus();
    }
  }

  // What the keys change: each is said, as a press of a checkbox is.
  const SELECTING = ['extend', 'toggle', 'range', 'all', 'clear'];

  // The keys of the list of cards (see onListKeydown in listSelection.js).
  // While the library takes a change the selection stays as it is: the
  // keys only move. Delete asks Delete selected's question.
  function onCardKeydown(event, index) {
    const action = onListKeydown(event, {
      selection,
      items: items.value,
      index,
      focus: focusCard,
      columns: columnsOf(event.currentTarget.parentElement),
      locked: busy.value,
      canDelete: mine.value,
      onDelete: askDeleteSelected,
    });

    if (SELECTING.includes(action?.type)) {
      announceSelection();
    }
  }

  function selectAll() {
    selection.toggleAll();
    announceSelection();
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

  // Saves templates as a template file of a collection named `name`, which
  // carries the custom icons they name from the icon library, read first
  // when it was not. The page's live region announces what was saved, and
  // the page's alert says why a save failed.
  async function exportTemplates(name, templates, description = '') {
    if (!templates.length || busy.value) {
      return;
    }

    store.clearError();

    try {
      await iconLibrary.ensure().catch(() => {});

      const { message } = exportTemplateFile(
        { name, description, templates },
        { library: iconLibrary, saveAs },
      );

      store.announce(message);
    } catch (error) {
      store.setError(
        `Could not export the templates. ${error?.message || ''}`.trim(),
      );
    }
  }

  // One template, as a collection of its own name.
  function exportTemplate(template) {
    exportTemplates(template.name, [template], template.description || '');
  }

  // The selection, as a collection named after the list shown.
  function exportSelected() {
    if (selection.count) {
      exportTemplates(
        collection.value?.name || LIST_LABELS[list.value.source],
        selection.selected,
      );
    }
  }

  // The collection shown, as it is.
  function exportCollection() {
    exportTemplates(
      collection.value.name,
      items.value,
      collection.value.description || '',
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

  /**
   * For the command palette's Find collection: shows the list that a value
   * of the Show field names, and focuses the Show field. A value that names
   * no list leaves the list as it is.
   *
   * @param {string} value see shownList
   * @returns {Promise<boolean>} whether the list is shown
   */
  async function showList(value) {
    if (!shownList(library.value, value)) {
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

  .builder-button[aria-busy='true'] {
    cursor: progress;
  }
</style>
