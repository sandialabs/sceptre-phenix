<!--
  The drafts page's Node Templates tab: the user's template library.

  The view (Builder.vue) puts it in the templates slot of BuilderDrafts. It
  lists the user's own templates, a card each, or those of one of their
  collections (Show). New template and a card's Edit open the template
  editor, and New collection and Edit collection the collection dialog:
  both are the view's, which this asks for (template, collection). A role
  that cannot add templates has neither New button, and one that cannot
  change or delete them has no Edit or Delete.

  Several templates can be acted on at once: each card has a checkbox, and
  the row above the list (BuilderBulkBar.vue) has Select all, how many are
  selected, Add to collection (a menu of the collections, then "New
  collection…", which starts one with the selected templates), Remove from
  collection while a collection is shown, and Delete selected, each for a
  role that may do it. The selection
  is that of the list shown: showing another list drops it. Every change of
  it is announced.

  Deleting cannot be undone, so it asks first, once for the whole selection,
  and is one request. A deleted template leaves its collections; a deleted
  collection leaves its templates. Devices made from a template are not
  changed. While the library takes a change, nothing else can be changed.
  What the change came to is said by the page's live region, and why it
  failed by the page's alert.

  A library that could not be read says why, with Retry, above whatever an
  earlier read listed.
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
      <!-- While the read it asked for is under way it keeps focus, and a
           second press does nothing. -->
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
    <p v-else-if="library.damaged" data-testid="templates-damaged">
      Your library cannot be read. A newer version of phenix may have saved it.
    </p>
    <template v-else-if="library.loaded">
      <div class="builder-field builder-templates__show">
        <label for="templates-show">Show</label>
        <select
          id="templates-show"
          v-model="shown"
          data-testid="templates-show">
          <option value="">My templates</option>
          <optgroup v-if="collections.length" label="My collections">
            <option
              v-for="entry in collections"
              :key="entry.id"
              :value="entry.id">
              {{ entry.name }}
            </option>
          </optgroup>
        </select>
      </div>

      <!-- The collection shown: what it is, and what can be done to it. -->
      <div
        v-if="collection"
        class="builder-templates__collection"
        data-testid="collection-block">
        <h2>{{ collection.name }}</h2>
        <p class="builder-card__meta" data-testid="collection-count">
          {{ count(items.length, 'template') }}
        </p>
        <p v-if="collection.description" data-testid="collection-about">
          {{ collection.description }}
        </p>
        <div class="builder-templates__collection-actions">
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
            v-if="rights.delete"
            type="button"
            class="builder-button builder-button--danger"
            data-testid="collection-delete"
            aria-haspopup="dialog"
            :aria-disabled="busy || undefined"
            @click="busy || askDeleteCollection()">
            Delete collection
          </button>
        </div>
      </div>

      <builder-bulk-bar
        v-if="selectable && selection.total"
        id="templates"
        :label="collection ? collection.name : 'My templates'"
        :count="selection.count"
        :total="selection.total"
        :state="selection.state"
        :disabled="busy"
        @toggle-all="selectAll">
        <!-- Each says how many are selected, so one that cannot act says
             why: "0 of 12 selected". -->
        <builder-menu-button
          v-if="rights.update && collectMenu.length"
          :items="collectMenu"
          :disabled="!selection.count || busy"
          testid="bulk-collect-templates"
          aria-describedby="bulk-count-templates"
          @select="addToCollection">
          Add to collection
        </builder-menu-button>
        <button
          v-if="collection && rights.update"
          type="button"
          class="builder-button"
          data-testid="bulk-uncollect-templates"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="removeFromCollection">
          Remove from collection
        </button>
        <button
          v-if="rights.delete"
          type="button"
          class="builder-button builder-button--danger"
          data-testid="bulk-delete-templates"
          aria-haspopup="dialog"
          aria-describedby="bulk-count-templates"
          :aria-disabled="!selection.count || busy || undefined"
          @click="askDeleteSelected">
          Delete selected
        </button>
      </builder-bulk-bar>

      <p v-if="!items.length" data-testid="templates-empty">{{ emptyText }}</p>
      <ul
        v-else
        class="builder-cards"
        :aria-busy="busy || undefined"
        data-testid="templates-list">
        <li
          v-for="template in items"
          :key="template.id"
          class="builder-card builder-panel"
          :class="{ 'is-selected': selection.has(template) }"
          :data-testid="`template-card-${template.id}`">
          <div class="builder-card__head">
            <!-- The checkbox is named for the card, as its buttons are. -->
            <label v-if="selectable" class="builder-card__select">
              <input
                type="checkbox"
                :checked="selection.has(template)"
                :disabled="busy"
                :data-testid="`template-select-${template.id}`"
                @change="select(template, $event.target.checked)" />
              <span class="builder-visually-hidden">
                Select {{ cardName(template) }}
              </span>
            </label>
            <!-- The icon devices made from it are drawn with; the name
                 says what the template is. -->
            <h2>
              <builder-icon
                :name="template.device?.iconKey || 'server'"
                :src="iconSrc(template.device?.icon, library.icons)"
                :size="18" />
              <span>{{ template.name }}</span>
            </h2>
          </div>
          <p
            v-if="updated(template)"
            class="builder-card__meta"
            :data-testid="`template-time-${template.id}`">
            Updated {{ updated(template) }}
          </p>
          <p
            v-if="collectionsOf(template).length"
            class="builder-card__meta"
            :data-testid="`template-collections-${template.id}`">
            In {{ listOf(collectionsOf(template)) }}
          </p>
          <p
            v-if="template.description"
            class="builder-card__meta"
            :data-testid="`template-about-${template.id}`">
            {{ template.description }}
          </p>

          <div class="builder-card__actions">
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
          </div>
        </li>
      </ul>
    </template>

    <!-- A deleted template or collection cannot be brought back, so Delete
         asks first (WCAG 3.3.4): once, whatever is selected. -->
    <builder-confirm
      v-if="question"
      :id="question.id"
      :title="question.title"
      :message="question.message"
      :confirm-label="question.confirmLabel"
      @cancel="question = null"
      @confirm="confirmDelete" />
  </div>
</template>

<script setup>
  import { computed, nextTick, reactive, ref, watch } from 'vue';

  import BuilderBulkBar from './BuilderBulkBar.vue';
  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderMenuButton from './BuilderMenuButton.vue';

  import { count, listOf } from '@/builder/announce.js';
  import { focusLost } from '@/builder/commands.js';
  import { formatTimestamp } from '@/builder/format.js';
  import { iconSrc } from '@/builder/icons.js';
  import { useListSelection } from '@/builder/listSelection.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    collectionDeleteQuestion,
    membersMessage,
    templatesDeleteQuestion,
    templatesDeletedMessage,
  } from '@/builder/templates.js';

  // The menu item that starts a new collection with the selected templates.
  const NEW_COLLECTION = 'new-collection';

  const emit = defineEmits([
    // ({mode, id?}): the template editor, on a new template of the library
    // ('library-new') or on one it has ('library-edit').
    'template',
    // ({id?, templateIds?}): the collection dialog, on the collection `id`
    // names, or on a new one, which starts with those templates.
    'collection',
  ]);

  const store = useBuilderStore();
  const rootEl = ref(null);

  const library = computed(() => store.templates);
  const rights = computed(() => store.templateRights);
  const collections = computed(() => store.ownCollections);
  const loading = computed(() => library.value.status === 'loading');
  // The library is taking a change: nothing else is changed meanwhile.
  const busy = ref(false);

  // The list shown: '' for every template of the user's, or a collection's
  // id. A collection that leaves the library gives way to all of them.
  const shown = ref('');
  const collection = computed(
    () => collections.value.find((entry) => entry.id === shown.value) || null,
  );

  watch(collection, (now) => {
    if (!now && shown.value) {
      shown.value = '';
    }
  });

  // A collection lists its templates in the order they joined it.
  const items = computed(() => {
    const own = store.ownTemplates;

    if (!collection.value) {
      return own;
    }

    const byId = new Map(own.map((template) => [template.id, template]));

    return (collection.value.templateIds || [])
      .map((id) => byId.get(id))
      .filter(Boolean);
  });

  const emptyText = computed(() => {
    if (collection.value) {
      return 'This collection has no templates. Select templates under My templates, then Add to collection.';
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
  // and add when it last changed for one that shares its name with another
  // (WCAG 2.4.6).
  function cardName(template) {
    const twins = store.ownTemplates.filter(
      (other) => other.name === template.name,
    ).length;
    const when = updated(template);

    return twins > 1 && when
      ? `${template.name}, updated ${when}`
      : template.name;
  }

  // The names of the user's collections that hold a template.
  function collectionsOf(template) {
    return collections.value
      .filter((entry) => (entry.templateIds || []).includes(template.id))
      .map((entry) => entry.name);
  }

  // --- selection ---------------------------------------------------------

  // A card can be selected when something can be done to several at once.
  const selectable = computed(() => rights.value.update || rights.value.delete);
  const selection = reactive(
    useListSelection(items, (template) => template.id),
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

  function select(template, on) {
    selection.toggle(template, on);
    announceSelection();
  }

  function selectAll() {
    selection.toggleAll();
    announceSelection();
  }

  // --- changes -----------------------------------------------------------

  // Runs one change of the library, unless one is under way. A failure is
  // said by the page's alert, as `action` names what could not be done.
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
  async function confirmDelete() {
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

  // Retry goes once the library is read: focus then moves to the tab
  // rather than fall to the page.
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

  .builder-button[aria-busy='true'] {
    cursor: progress;
  }
</style>
