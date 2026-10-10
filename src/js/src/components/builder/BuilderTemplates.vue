<!--
  The drafts page's Node Templates tab: the user's template library.

  The view (Builder.vue) puts it in the templates slot of BuilderDrafts. It
  lists the user's own templates, a card each, or those of one of their
  collections (Show). New template and a card's Edit open the template
  editor, and New collection and Edit collection the collection dialog:
  both are the view's, which this asks for (template, collection). A role
  that cannot add templates has neither New button, and one that cannot
  change or delete them has no Edit or Delete.

  The user can act on several templates at once: each card has a checkbox, and
  the row above the list (BuilderBulkBar.vue) has Select all, how many are
  selected, Add to collection (a menu of the collections, then "New
  collection…", which starts one with the selected templates), Remove from
  collection while a collection is shown, and Delete selected, each for a
  role that may do it. The selection is that of the list shown: another
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
  change. While the library takes a change, nothing else can change. The
  page's live region announces the result of the change, and the page's
  alert says why it failed.

  Share, on a card, on the selection and on a collection shown, opens the
  Share dialog (the view's, which this asks for: share), for a user who may
  share with people (canShare), and also publish server-wide there with a
  role that may (canPublish). Sharing needs an account of the user's own,
  so with sign-in off, when there is no one else, there is no Share, as
  there is none for drafts.
  Templates and collections other users share with the user, and those
  published server-wide, are listed too when Show names them ("Shared with
  me", "Server-wide", or one of their collections). They are read only. A
  card has View, which opens the template editor on it without a way to
  change it, and Copy to my library, which adds a copy the user owns. A
  collection is copied whole. A role that may publish can take another
  user's items back from server-wide, which asks first.

  A library that could not be read says why, with Retry, above whatever an
  earlier read listed. One the server cannot read (damaged) lists none of
  the user's own templates, but still other users'.

  The collections the server read from its template files at start are
  listed under Server in Show, each by its name. They are read only for
  everyone, as other users' items are: View, Copy to my library and
  Export, no Edit, Delete or Share.

  A deleted built-in template can be restored, for a role that may add
  templates. Restore built-in templates is in the row of New template while
  one or more are missing from the library: a button that names the one
  missing, or a menu with each missing one and Restore all. A restored
  template has its original content and is in no collection.

  Export, on a card, on the selection and on a collection shown, saves a
  YAML template file (see templateFile.js) that carries the custom icons
  its templates name. Templates of every source export. Import templates
  opens a dialog that reads such a file into the user's library as a new
  collection (dialogs/TemplateImportDialog.vue), for a role that may add
  templates.
-->
<template>
  <div ref="rootEl" class="builder-templates">
    <div v-if="rights.create" class="builder-templates__actions">
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
      <!-- A deleted built-in template can come back: one by name, or, with
           several missing, each from a menu or all at once. -->
      <button
        v-if="missingBuiltins.length === 1"
        type="button"
        class="builder-button"
        data-testid="templates-restore"
        :aria-disabled="busy || undefined"
        @click="busy || restore([missingBuiltins[0].id])">
        Restore built-in template {{ missingBuiltins[0].name }}
      </button>
      <builder-menu-button
        v-else-if="missingBuiltins.length"
        :items="restoreMenu"
        :disabled="busy"
        testid="templates-restore"
        @select="restoreChosen">
        Restore built-in templates
      </builder-menu-button>
    </div>
    <p v-else class="builder-templates__note" data-testid="templates-view-only">
      Your role can view templates, but not create them.
    </p>

    <div
      v-if="library.error"
      class="builder-panel builder-error builder-templates__failed">
      <p role="alert" data-testid="templates-error">
        Could not load your templates. {{ library.error }}
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
      <!-- A library the server cannot read lists none of the user's own
           templates, but still those other users share or publish. -->
      <p v-if="library.damaged" data-testid="templates-damaged">
        Your library cannot be read. A newer version of phenix may have saved
        it.
      </p>
      <div
        v-if="
          !library.damaged ||
          choices.shared ||
          choices.server ||
          choices.preloadedCollections.length
        "
        class="builder-field builder-templates__show">
        <label for="templates-show">Show</label>
        <select
          id="templates-show"
          v-model="shown"
          data-testid="templates-show">
          <option value="">My templates</option>
          <optgroup v-if="choices.own.length" label="My collections">
            <option
              v-for="entry in choices.own"
              :key="entry.value"
              :value="entry.value">
              {{ entry.label }}
            </option>
          </optgroup>
          <option v-if="choices.shared" :value="SHOW_SHARED">
            Shared with me
          </option>
          <optgroup
            v-if="choices.sharedCollections.length"
            label="Shared collections">
            <option
              v-for="entry in choices.sharedCollections"
              :key="entry.value"
              :value="entry.value">
              {{ entry.label }}
            </option>
          </optgroup>
          <option v-if="choices.server" :value="SHOW_SERVER">
            Server-wide
          </option>
          <optgroup
            v-if="choices.serverCollections.length"
            label="Server-wide collections">
            <option
              v-for="entry in choices.serverCollections"
              :key="entry.value"
              :value="entry.value">
              {{ entry.label }}
            </option>
          </optgroup>
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

      <!-- The collection shown: what it is, and what can be done to it. -->
      <div
        v-if="collection"
        class="builder-templates__collection"
        data-testid="collection-block">
        <h2>
          {{ collection.name }}
          <span
            v-if="collection.serverWide"
            class="builder-templates__tag"
            data-testid="collection-server-wide"
            >Server-wide</span
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
          copy it to your library.
        </p>
        <p
          v-else-if="!mine"
          class="builder-card__meta"
          data-testid="collection-owner">
          Owner: {{ collection.owner }}
        </p>
        <p
          v-if="mine && sharedWith(collection).length"
          class="builder-card__meta"
          data-testid="collection-shared-with">
          Shared with {{ describeShares(sharedWith(collection)) }}
        </p>
        <p v-if="collection.description" data-testid="collection-about">
          {{ collection.description }}
        </p>
        <div v-if="mine" class="builder-templates__collection-actions">
          <button
            v-if="rights.update"
            type="button"
            class="builder-button"
            data-testid="collection-edit"
            aria-haspopup="dialog"
            :aria-disabled="busy || undefined"
            @click="busy || $emit('collection', { id: collection.id })">
            Edit collection
          </button>
          <button
            v-if="mayShare"
            type="button"
            class="builder-button"
            data-testid="collection-share"
            aria-haspopup="dialog"
            :aria-disabled="busy || undefined"
            @click="shareItems([{ ...collection, kind: 'collection' }])">
            <builder-icon name="share" :size="14" />
            Share
          </button>
          <button
            v-if="rights.delete"
            type="button"
            class="builder-button builder-button--danger"
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
            v-if="rights.create"
            type="button"
            class="builder-button"
            data-testid="collection-copy"
            :aria-disabled="busy || !items.length || undefined"
            @click="copyCollection">
            Copy collection to my library
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
          <button
            v-if="library.canPublish && collection.serverWide"
            type="button"
            class="builder-button"
            data-testid="collection-unpublish"
            aria-haspopup="dialog"
            :aria-disabled="busy || undefined"
            @click="
              busy || askUnpublish([{ ...collection, kind: 'collection' }])
            ">
            Remove from server-wide
          </button>
        </div>
      </div>

      <builder-bulk-bar
        v-if="selectable && selection.total"
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
          v-if="mine && rights.update && collectMenu.length"
          :items="collectMenu"
          :disabled="!selection.count || busy"
          testid="bulk-collect-templates"
          aria-describedby="bulk-count-templates"
          @select="addToCollection">
          Add to collection
        </builder-menu-button>
        <button
          v-if="mine && collection && rights.update"
          type="button"
          class="builder-button"
          data-testid="bulk-uncollect-templates"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="removeFromCollection">
          Remove from collection
        </button>
        <button
          v-if="mine && mayShare"
          type="button"
          class="builder-button"
          data-testid="bulk-share-templates"
          aria-haspopup="dialog"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="selection.count && shareItems(selection.selected)">
          Share selected
        </button>
        <button
          v-if="mine && rights.delete"
          type="button"
          class="builder-button builder-button--danger"
          data-testid="bulk-delete-templates"
          aria-haspopup="dialog"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="askDeleteSelected">
          Delete selected
        </button>
        <button
          v-if="!mine && rights.create"
          type="button"
          class="builder-button"
          data-testid="bulk-copy-templates"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="selection.count && copyTemplates(selection.selected)">
          Copy to my library
        </button>
        <button
          v-if="!mine && mayUnpublish"
          type="button"
          class="builder-button"
          data-testid="bulk-unpublish-templates"
          aria-haspopup="dialog"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="askUnpublishSelected">
          Remove from server-wide
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
          :key="`${template.owner}/${template.id}`"
          class="builder-card builder-panel"
          :class="{ 'is-selected': selection.has(template) }"
          :data-testid="testid('template-card', template)"
          :tabindex="selection.tabStop(template) ? 0 : -1"
          :aria-labelledby="
            selectable
              ? `template-name-${index} template-state-${index}`
              : `template-name-${index}`
          "
          @focusin="selection.focused(template)"
          @keydown="onCardKeydown($event, index)">
          <div class="builder-card__head">
            <!-- The icon of devices made from it. The name says what the
                 template is. -->
            <h2 :id="`template-name-${index}`">
              <builder-icon
                :name="template.device?.iconKey || 'server'"
                :src="iconSrc(template.device?.icon, null, iconLibrary)"
                :size="18" />
              <span>
                {{ template.name }}
                <span
                  v-if="template.serverWide"
                  class="builder-templates__tag"
                  :data-testid="testid('template-server-wide', template)"
                  >Server-wide</span
                >
              </span>
            </h2>
            <!-- The checkbox, at the card's top right, is named for the card,
                 as its buttons are. -->
            <label v-if="selectable" class="builder-card__select">
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
          <!-- One line each: whose it is, when it changed, the collections
               that hold it, and who it is shared with. -->
          <p
            v-if="!mine && template.owner"
            class="builder-card__meta"
            :data-testid="testid('template-owner', template)">
            Owner: {{ template.owner }}
          </p>
          <p
            v-if="updated(template)"
            class="builder-card__meta"
            :data-testid="testid('template-time', template)">
            Updated {{ updated(template) }}
          </p>
          <p
            v-if="collectionsOf(template).length"
            class="builder-card__meta"
            :data-testid="testid('template-collections', template)">
            In {{ listOf(collectionsOf(template)) }}
          </p>
          <p
            v-if="mine && sharedWith(template).length"
            class="builder-card__meta"
            :data-testid="testid('template-shared-with', template)">
            Shared with {{ describeShares(sharedWith(template)) }}
          </p>
          <p
            v-if="template.description"
            class="builder-card__meta"
            :data-testid="testid('template-about', template)">
            {{ template.description }}
          </p>

          <div v-if="!mine" class="builder-card__actions">
            <button
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
                  owner: template.owner,
                })
              ">
              View
            </button>
            <button
              v-if="rights.create"
              type="button"
              class="builder-button"
              :data-testid="testid('template-copy', template)"
              :aria-label="`Copy to my library: ${cardName(template)}`"
              :aria-disabled="busy || undefined"
              @click="copyTemplates([template])">
              Copy to my library
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
          <div v-else class="builder-card__actions">
            <button
              v-if="rights.update"
              type="button"
              class="builder-button"
              :data-testid="`template-edit-${template.id}`"
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
              v-if="mayShare"
              type="button"
              class="builder-button"
              :data-testid="`template-share-${template.id}`"
              :aria-label="`Share template ${cardName(template)}`"
              aria-haspopup="dialog"
              :aria-disabled="busy || undefined"
              @click="shareItems([template])">
              <builder-icon name="share" :size="14" />
              Share
            </button>
            <button
              v-if="rights.delete"
              type="button"
              class="builder-button builder-button--danger"
              :data-testid="`template-delete-${template.id}`"
              :aria-label="`Delete template ${cardName(template)}`"
              aria-haspopup="dialog"
              :aria-disabled="busy || undefined"
              @click="busy || askDelete([template])">
              Delete
            </button>
            <button
              type="button"
              class="builder-button"
              :data-testid="`template-export-${template.id}`"
              :aria-label="`Export template ${cardName(template)}`"
              :aria-disabled="busy || undefined"
              @click="exportTemplate(template)">
              Export
            </button>
          </div>
          <!-- Whether the card is selected, for its name only. -->
          <span v-if="selectable" :id="`template-state-${index}`" hidden>{{
            selection.has(template) ? 'selected' : 'not selected'
          }}</span>
        </li>
      </ul>
    </template>

    <!-- Reads a template file into the library as a new collection. -->
    <template-import-dialog v-if="importing" @close="importing = false" />

    <!-- A deleted template or collection cannot be brought back, so Delete
         asks first (WCAG 3.3.4): once, whatever is selected. So does taking
         another user's items back from server-wide, which only they can
         undo. -->
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
  import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
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
  import { describeShares } from '@/builder/share.js';
  import { LibraryError, useBuilderStore } from '@/builder/store.js';
  import { exportTemplateFile } from '@/builder/templateFile.js';
  import {
    SHOW_SERVER,
    SHOW_SHARED,
    collectionDeleteQuestion,
    collectionNames,
    copiedMessage,
    copyProblem,
    membersMessage,
    shareSubject,
    sharedWith,
    shownList,
    showChoices,
    templatesDeleteQuestion,
    templatesDeletedMessage,
    templatesRestoredMessage,
    unpublishQuestion,
  } from '@/builder/templates.js';

  // The menu item that starts a new collection with the selected templates.
  const NEW_COLLECTION = 'new-collection';
  // The menu item that restores every missing built-in template. No
  // built-in template has this id.
  const RESTORE_ALL = 'restore-all';
  // What the row above a whole list calls it, by whose templates it holds.
  const LIST_LABELS = {
    own: 'My templates',
    shared: 'Shared with me',
    server: 'Server-wide',
    preloaded: 'Server',
  };

  const emit = defineEmits([
    // ({mode, id?, owner?}): the template editor, on a new template of the
    // library ('library-new'), on one it has ('library-edit'), or on
    // another user's, which `owner` names, read only ('view').
    'template',
    // ({id?, templateIds?}): the collection dialog, on the collection `id`
    // names, or on a new one, which starts with those templates.
    'collection',
    // ({targets}): the Share dialog, on templates and collections of the
    // user's, as listed, each with its kind.
    'share',
  ]);

  const store = useBuilderStore();
  const rootEl = ref(null);

  const library = computed(() => store.templates);
  const rights = computed(() => store.templateRights);
  const collections = computed(() => store.ownCollections);
  const loading = computed(() => library.value.status === 'loading');
  // The library is taking a change. Nothing else changes meanwhile.
  const busy = ref(false);
  // The Import templates dialog is open.
  const importing = ref(false);
  // Share is offered to a user who may share with people, which changes
  // configs. Publishing server-wide is part of the Share dialog, so a user
  // without an account, as with sign-in off, publishes nothing either: no
  // one else would see it.
  const mayShare = computed(
    () => rights.value.update && library.value.canShare,
  );

  // What Show offers, and the list shown: '' for every template of the
  // user's, a collection's id, SHOW_SHARED or SHOW_SERVER for the templates
  // of other users, or the key of one of their collections (see shownList).
  // A list that leaves the library gives way to the user's templates.
  const choices = computed(() => showChoices(library.value));
  const shown = ref('');
  const list = computed(
    () => shownList(library.value, shown.value) || shownList(library.value, ''),
  );
  const collection = computed(() => list.value.collection);
  // The list holds the user's own templates. For a damaged library, these
  // are the templates the server could not read.
  const mine = computed(() => list.value.source === 'own');
  // The list is a collection the server read from a template file.
  const preloaded = computed(() => list.value.source === 'preloaded');
  const unreadable = computed(() => mine.value && library.value.damaged);

  watch([list, choices], () => {
    const gone =
      (shown.value && !shownList(library.value, shown.value)) ||
      (shown.value === SHOW_SHARED && !choices.value.shared) ||
      (shown.value === SHOW_SERVER && !choices.value.server);

    if (gone) {
      shown.value = '';
    }
  });

  // A collection lists its templates in the order they joined it.
  const items = computed(() => list.value.templates);

  const emptyText = computed(() => {
    if (collection.value) {
      return mine.value
        ? 'This collection has no templates. Select templates under My templates, then Add to collection.'
        : 'This collection has no templates.';
    }

    if (!mine.value) {
      return list.value.source === 'shared'
        ? 'No templates are shared with you.'
        : 'No templates of other users are published server-wide.';
    }

    return rights.value.create
      ? 'Your library has no templates. Select New template to make one.'
      : 'Your library has no templates.';
  });

  // --- cards -------------------------------------------------------------

  function updated(template) {
    return formatTimestamp(template.updated);
  }

  // Names need not differ. A card's checkbox and buttons name its template,
  // and add whose it is for another user's that shares its name with one of
  // someone else's, and when it last changed for one that shares its name
  // with another of the same library (WCAG 2.4.6).
  function cardName(template) {
    const twins = (mine.value ? store.ownTemplates : items.value).filter(
      (other) => other.name === template.name,
    );
    const when = updated(template);
    let name = template.name;

    if (twins.some((other) => other.owner !== template.owner)) {
      name = `${name} (${template.owner})`;
    }

    if (
      when &&
      twins.filter((other) => other.owner === template.owner).length > 1
    ) {
      name = `${name}, updated ${when}`;
    }

    return name;
  }

  // The names of the collections that hold a template, of those the user
  // sees in its library.
  function collectionsOf(template) {
    return collectionNames(library.value, template);
  }

  // A card's test ids: the user's own templates by id, other users' by
  // owner and id, since every library starts with the same built-in ids,
  // and the server's by id after "preloaded".
  function testid(prefix, template) {
    switch (template.source) {
      case 'own':
        return `${prefix}-${template.id}`;
      case 'preloaded':
        return `${prefix}-preloaded-${template.id}`;
      default:
        return `${prefix}-${template.owner}-${template.id}`;
    }
  }

  // --- selection ---------------------------------------------------------

  // Other users' server-wide templates a role that may publish can take
  // back: those published themselves, rather than through a collection.
  const mayUnpublish = computed(
    () =>
      library.value.canPublish &&
      items.value.some((template) => template.serverWide),
  );
  // A card can be selected when something can be done to several at once.
  const selectable = computed(() =>
    mine.value
      ? rights.value.update || rights.value.delete
      : rights.value.create || mayUnpublish.value,
  );
  // Every card is in it, for the keys to move to, and the card that is the
  // list's Tab stop. The cards can be selected while something can be done
  // to several at once.
  const selection = reactive(
    useListSelection(items, (template) => `${template.owner}/${template.id}`, {
      selectable: () => selectable.value,
    }),
  );

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
      locked: busy.value || !selectable.value,
      canDelete: mine.value && rights.value.delete,
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

  // The built-in templates the library does not hold, once it was read.
  const missingBuiltins = computed(() => store.missingBuiltinTemplates);
  // Each missing built-in template, then all of them.
  const restoreMenu = computed(() => [
    ...missingBuiltins.value.map((template) => ({
      id: template.id,
      label: template.name,
    })),
    { id: RESTORE_ALL, label: 'Restore all', separator: true },
  ]);

  function restoreChosen(item) {
    restore(item.id === RESTORE_ALL ? [] : [item.id]);
  }

  // Restores built-in templates (none named: every missing one). The
  // control goes once nothing is missing, so focus moves to the list.
  async function restore(ids) {
    const done = await change(
      ids.length === 1
        ? 'restore the built-in template'
        : 'restore the built-in templates',
      async () => {
        const restored = await store.restoreBuiltinTemplates(ids);

        store.announce(
          templatesRestoredMessage(
            BUILTIN_TEMPLATES.filter((template) =>
              restored.includes(template.id),
            ),
          ),
        );
      },
    );

    if (done) {
      await focusList();
    }
  }

  // The collections the selected templates can join: every one but the one
  // shown, then a new one, for a role that may add a collection.
  const collectMenu = computed(() => [
    ...collections.value
      .filter((entry) => entry.id !== shown.value)
      .map((entry) => ({ id: entry.id, label: entry.name })),
    ...(rights.value.create
      ? [{ id: NEW_COLLECTION, label: 'New collection…', separator: true }]
      : []),
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

  // --- sharing, copying, taking back from server-wide ---------------------

  // The Share dialog, on the user's templates or one of their collections.
  function shareItems(targets) {
    if (!busy.value && targets.length) {
      emit('share', {
        targets: targets.map((target) => ({
          kind: 'template',
          ...target,
        })),
      });
    }
  }

  // Copies of other users' templates, naming the custom icons they name, go
  // to the user's library, as a collection of their own when `from` is
  // one. A library at its most templates takes none.
  async function copyTemplates(templates, from = null) {
    if (!templates.length || busy.value) {
      return;
    }

    const action = from
      ? `copy collection ${from.name} to your library`
      : `copy ${shareSubject(templates)} to your library`;
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

    // The copies are the user's now: copying them again would add more.
    if (done && !from) {
      selection.clear();
    }
  }

  function copyCollection() {
    copyTemplates(items.value, collection.value);
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

  function askUnpublish(targets) {
    question.value = {
      id: 'templates-unpublish',
      ...unpublishQuestion(targets),
      unpublish: targets,
    };
  }

  // Of the selected templates, those published server-wide themselves. One
  // published only through its collection is taken back with the
  // collection.
  function askUnpublishSelected() {
    if (!selection.count || busy.value) {
      return;
    }

    const published = selection.selected.filter(
      (template) => template.serverWide,
    );

    if (!published.length) {
      store.announce(
        'The selected templates are server-wide through a collection. Show the collection, then Remove from server-wide.',
      );

      return;
    }

    askUnpublish(published);
  }

  // One request for each owner whose items are taken back. They may leave
  // the list, and a collection the Show field: focus moves on, as after a
  // delete.
  async function unpublish(targets) {
    const owners = [...new Set(targets.map((target) => target.owner))];
    const done = await change(
      `remove ${shareSubject(targets)} from server-wide`,
      async () => {
        for (const owner of owners) {
          const theirs = targets.filter((target) => target.owner === owner);

          await store.publishLibraryItems(
            {
              templates: theirs
                .filter((target) => target.kind !== 'collection')
                .map((target) => target.id),
              collections: theirs
                .filter((target) => target.kind === 'collection')
                .map((target) => target.id),
            },
            false,
            owner,
          );
        }

        store.announce(`Removed ${shareSubject(targets)} from server-wide.`);
      },
    );

    if (!done) {
      return;
    }

    if (targets.some((target) => target.kind === 'collection')) {
      await nextTick();
      document.getElementById('templates-show')?.focus();
    } else {
      await focusList();
    }
  }

  // --- deleting ----------------------------------------------------------

  // What Delete asks about: { id, title, message, confirmLabel }, with the
  // templates to delete, or the collection. Or what Remove from server-wide
  // takes back (unpublish).
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
    const { templates, collection: gone, unpublish: back } = question.value;

    question.value = null;

    if (back) {
      await unpublish(back);

      return;
    }

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
