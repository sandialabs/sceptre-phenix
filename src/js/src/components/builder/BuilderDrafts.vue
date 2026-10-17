<!--
  Drafts landing.

  Tabbed lists (the drafts of every Builder user, the latest changed first,
  and the published diagrams) plus the three ways to start: a blank
  diagram, Import (the server converts a topology or experiment config) and
  Upload (a Builder document). The view (Builder.vue) puts the buttons the editor header has
  too after them, in the buttons slot: Commands, the theme, Settings, Help
  and Focus mode. The view reads the lists again whenever they show again,
  so there is no Refresh button.

  While a draft is being made or opened (busy), the buttons that would make
  or open another are aria-disabled: they keep focus, and a second click
  does nothing. The Open of the one being opened says so.

  A draft the server can no longer read (damaged) is listed too, after the
  others, with why it cannot be opened, and Delete.

  A card says who created the draft on one line and when it last changed,
  and by whom, on the next, and its buttons are all one size (see
  .builder-card__actions in builder.css). Every draft has Delete, and
  Publish when the role may change configs, which opens the Publish dialog
  here, on the drafts, once the view has loaded the draft (publishing). A
  published topology has Delete when the role may delete configs, which
  deletes the topology from phenix, and Exp while its publication has an
  experiment, which opens that experiment.

  A topology whose diagram is read from the Builder file it names is listed
  with the published diagrams, tagged File in words, with the file's path
  where a published diagram has its time. It has no Delete: nothing was
  published, and the user deletes the topology on the Configs page.

  A draft that closed while its changes were still saving shows the state
  of those saves (saves, see createBackgroundSaves in builder/leave.js):
  saving, saved, or why not. This text is the description of its Open
  button. To get back changes that could not be saved, open the draft
  again.

  The user can act on several cards at once. A card a batch can act on has
  a checkbox, and its list a row above it (BuilderBulkBar.vue) with Select
  all, how many are selected, and the batch actions: Download selected
  (drafts and published diagrams, but not a topology read from its Builder
  file) and Delete selected. A list with no such card has neither. Each list keeps its own selection, by cardKey, through a change
  of tab and a new read of the lists. Every change of it is announced
  (announce). Delete selected asks once for the batch, naming what goes and
  how many, and the view deletes them (bulk-delete, bulk-delete-published).
  Download selected has the view save each as a Builder file
  of its own (bulk-download). While the view runs a batch (bulk), the row
  shows its progress, and nothing can be selected or deleted. A summary
  under the row lists what a batch left undone (bulkResult,
  BuilderBulkSummary.vue), and takes focus.

  The cards of a list are one Tab stop, and the keys of listSelection.js
  move through them and select (onListKeydown): the arrow keys move focus
  from card to card in the grid, Home and End to the first and last, Space
  selects the focused card, Shift with Space or an arrow key a range, Mod+A
  every card, Escape none, and Delete (Backspace on macOS) asks Delete
  selected's question. A checkbox pressed with Shift makes the range from
  the card last pressed as the box now is: selected, or not.

  The view can add tabs of its own, Node Templates (templates), Layers
  (layers) and Icons (icons), in that order, whose panels are the slots of
  those names.
-->
<template>
  <section ref="rootEl" aria-labelledby="drafts-title">
    <div class="builder-drafts__header">
      <h1 id="drafts-title">Builder</h1>

      <div class="builder-drafts__actions">
        <button
          type="button"
          class="builder-button builder-button--primary"
          data-testid="drafts-blank"
          :aria-disabled="busy || undefined"
          @click="busy || $emit('blank')">
          <builder-icon name="plus" :size="14" />
          Blank diagram
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="drafts-import"
          aria-haspopup="dialog"
          :aria-disabled="busy || undefined"
          @click="busy || $emit('import')">
          <builder-icon name="layout" :size="14" />
          Import
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="drafts-upload"
          aria-haspopup="dialog"
          :aria-disabled="busy || undefined"
          @click="busy || $emit('upload')">
          <builder-icon name="upload" :size="14" />
          Upload
        </button>
        <!-- The view's buttons: those the editor header has too. -->
        <slot name="buttons" />
      </div>
    </div>

    <div class="builder-tabs" role="tablist" aria-label="Builder collections">
      <button
        v-for="tab in tabs"
        :id="`tab-${tab.id}`"
        :key="tab.id"
        type="button"
        role="tab"
        class="builder-tab"
        :aria-selected="active === tab.id"
        :aria-controls="`panel-${tab.id}`"
        :tabindex="active === tab.id ? 0 : -1"
        :data-testid="`drafts-tab-${tab.id}`"
        @click="active = tab.id"
        @keydown="onTabKeydown($event, tab)">
        {{ tab.label }}
        <span v-if="tab.count != null" class="builder-tab__count"
          >({{ tab.count }})</span
        >
      </button>
    </div>

    <div
      v-for="tab in tabs"
      :id="`panel-${tab.id}`"
      :key="`panel-${tab.id}`"
      role="tabpanel"
      :aria-labelledby="`tab-${tab.id}`"
      :tabindex="tab.custom || tab.items.length ? undefined : 0"
      :hidden="active !== tab.id">
      <builder-bulk-bar
        v-if="tab.selection?.total"
        :id="tab.id"
        :label="tab.label"
        :count="tab.selection.count"
        :total="tab.selection.total"
        :state="tab.selection.state"
        :running="bulk?.tab === tab.id ? bulk : null"
        :disabled="Boolean(bulk)"
        @toggle-all="selectAll(tab)">
        <!-- Both say how many are selected, so one that cannot act says
             why: "0 of 12 selected". -->
        <button
          v-if="canDownloadSome(tab)"
          type="button"
          class="builder-button"
          :data-testid="`bulk-download-${tab.id}`"
          :aria-describedby="`bulk-count-${tab.id} bulk-download-note-${tab.id}`"
          :aria-disabled="
            !downloadable(tab).length || Boolean(bulk) || busy || undefined
          "
          @click="downloadSelected(tab)">
          <builder-icon name="download" :size="14" />
          Download selected
        </button>
        <button
          v-if="canDeleteSome(tab)"
          type="button"
          class="builder-button"
          :data-testid="`bulk-delete-${tab.id}`"
          aria-haspopup="dialog"
          :aria-describedby="`bulk-count-${tab.id}`"
          :aria-disabled="
            !deletable(tab).length || Boolean(bulk) || busy || undefined
          "
          @click="askDeleteSelected(tab)">
          Delete selected
        </button>
        <!-- Each item is a download of its own, which a browser may ask
             the user to allow. -->
        <span
          v-if="canDownloadSome(tab)"
          :id="`bulk-download-note-${tab.id}`"
          class="builder-drafts__bulk-note"
          :data-testid="`bulk-download-note-${tab.id}`">
          Download selected saves a file for each. Your browser may ask to allow
          several downloads.
        </span>
      </builder-bulk-bar>

      <!-- What the last batch on this tab left undone, under the row. -->
      <builder-bulk-summary
        v-if="bulkResult && summaryTab === tab.id"
        :ref="(element) => (summary = element)"
        :heading="bulkResult.heading"
        :items="bulkResult.items"
        @dismiss="dismissResult(tab.id)" />

      <!-- The view's own panel (Node Templates, Layers, Icons), the slot
           named after the tab. Otherwise the cards: a refresh keeps them
           on screen, so focus on them survives. -->
      <slot v-if="tab.custom" :name="tab.id" />
      <p v-else-if="loading && !tab.items.length" role="status">Loading…</p>
      <p v-else-if="!tab.items.length">{{ tab.empty }}</p>

      <ul
        v-else
        class="builder-cards"
        :aria-busy="bulk?.tab === tab.id || undefined"
        :data-testid="`drafts-list-${tab.id}`">
        <!-- A card is focusable, one at a time (the list's Tab stop), and
             named by its title and whether it is selected. Focus on it, or
             on a control in it, makes it the Tab stop. -->
        <li
          v-for="(item, index) in tab.items"
          :key="`${tab.id}-${item.id}`"
          class="builder-card builder-panel"
          :class="{ 'is-selected': tab.selection?.has(item) }"
          :tabindex="tab.selection?.tabStop(item) ? 0 : -1"
          :aria-labelledby="cardLabelledBy(tab, item, index)"
          :data-testid="`draft-card-${item.id}`"
          @focusin="tab.selection?.focused(item)"
          @keydown="onCardKeydown($event, tab, index)">
          <div class="builder-card__head">
            <h2 :id="`card-name-${tab.id}-${index}`">
              {{ itemLabel(item) }}
              <span
                v-if="isFile(item)"
                class="builder-drafts__tag"
                :data-testid="`published-file-${item.id}`"
                >File</span
              >
            </h2>
            <!-- The checkbox, at the card's top right, is named for the card,
                 as its buttons are. -->
            <label
              v-if="tab.selection && selectable(tab.id, item)"
              class="builder-card__select">
              <input
                type="checkbox"
                :checked="tab.selection.has(item)"
                :disabled="Boolean(bulk)"
                :data-testid="`card-select-${item.id}`"
                @pointerdown="shift.pointerdown"
                @keydown="shift.keydown"
                @click="shift.click"
                @change="select(tab, item, $event)" />
              <span class="builder-visually-hidden">
                Select {{ cardName(item) }}
              </span>
            </label>
          </div>
          <!-- One line each: who created the draft, then when it changed
               and who changed it. -->
          <p
            v-if="item.createdBy"
            class="builder-card__meta"
            :data-testid="`draft-created-by-${item.id}`">
            Created by {{ item.createdBy }}
          </p>
          <p
            v-if="stamp(item)"
            class="builder-card__meta"
            :data-testid="`card-time-${item.id}`">
            {{ stamp(item).verb }} {{ stamp(item).time
            }}{{ item.lastModifiedBy ? ` by ${item.lastModifiedBy}` : '' }}
          </p>
          <p
            v-if="isFile(item)"
            class="builder-card__meta"
            :data-testid="`published-path-${item.id}`">
            Read from {{ item.path }}
          </p>
          <p v-if="item.nodeCount != null" class="builder-card__meta">
            {{ item.nodeCount }} nodes
          </p>
          <p v-if="item.description" class="builder-card__meta">
            {{ item.description }}
          </p>
          <!-- The ring turns while the changes are sent. Reduced motion
               stops it (see builder.css). The warning sign is decorative. -->
          <p
            v-if="saveOf(tab, item)"
            :id="`draft-save-${item.id}`"
            class="builder-drafts__save"
            :class="`builder-drafts__save--${saveOf(tab, item).kind}`"
            :data-testid="`draft-save-${item.id}`">
            <span
              v-if="saveOf(tab, item).kind === 'saving'"
              class="builder-toolbar__spinner"
              aria-hidden="true"></span>
            <builder-icon
              v-else
              :name="saveOf(tab, item).kind === 'saved' ? 'check' : 'warning'"
              :size="14" />
            <span>{{ saveOf(tab, item).text }}</span>
          </p>
          <p
            v-if="item.damaged"
            class="builder-drafts__damaged"
            :data-testid="`draft-damaged-${item.id}`">
            <builder-icon name="warning" :size="14" />
            The Builder cannot read this draft, so you cannot open it. A newer
            version of phenix may have saved it.
          </p>

          <div class="builder-card__actions">
            <!-- While it opens: a turning ring and Opening… (see
                 builder.css, reduced motion stops the ring). Both labels
                 hold the button's width. The hidden one does not name the
                 button. -->
            <button
              v-if="!item.damaged"
              type="button"
              class="builder-button"
              :data-testid="`draft-open-${item.id}`"
              :aria-label="`${isOpening(item) ? 'Opening' : 'Open'} ${cardName(item)}`"
              :aria-describedby="
                saveOf(tab, item) ? `draft-save-${item.id}` : undefined
              "
              :aria-disabled="busy || undefined"
              :aria-busy="isOpening(item) || undefined"
              @click="busy || $emit('open', item, itemLabel(item))">
              <span
                v-if="isOpening(item)"
                class="builder-toolbar__spinner"
                aria-hidden="true"></span>
              <span class="builder-button__swap">
                <span :class="{ 'is-off': isOpening(item) }">Open</span>
                <span :class="{ 'is-off': !isOpening(item) }">Opening…</span>
              </span>
            </button>
            <!-- The experiment the diagram's publication made, while it
                 exists: the button's name says which, as its tooltip does. -->
            <button
              v-if="tab.id === 'published' && item.experiment"
              type="button"
              class="builder-button"
              :data-testid="`published-experiment-${item.id}`"
              :aria-label="`Exp: open experiment ${item.experiment}`"
              :aria-disabled="busy || undefined"
              v-on="tipEvents(`Open experiment ${item.experiment}`)"
              @click="busy || $emit('experiment', item)">
              <builder-icon name="experiment" :size="14" />
              Exp
            </button>
            <!-- Delete waits with the other buttons while a draft is made,
                 opened or published: the draft a card's Publish is still
                 publishing must not be deleted under it. -->
            <button
              v-if="mayDelete(tab, item)"
              type="button"
              class="builder-button"
              :data-testid="`draft-delete-${item.id}`"
              :aria-label="`Delete ${cardName(item)}`"
              aria-haspopup="dialog"
              :aria-disabled="busy || Boolean(bulk) || undefined"
              @click="busy || bulk || (confirming = item)">
              Delete
            </button>
            <!-- A published topology's: while the delete runs, a turning
                 ring and Deleting…, as Open shows while it opens. -->
            <button
              v-if="mayDeletePublished(tab, item)"
              type="button"
              class="builder-button"
              :data-testid="`published-delete-${item.id}`"
              :aria-label="`${isDeleting(item) ? 'Deleting' : 'Delete'} ${cardName(item)}`"
              aria-haspopup="dialog"
              :aria-disabled="
                isDeleting(item) || busy || Boolean(bulk) || undefined
              "
              :aria-busy="isDeleting(item) || undefined"
              @click="
                isDeleting(item) || busy || bulk || (confirmingPublished = item)
              ">
              <span
                v-if="isDeleting(item)"
                class="builder-toolbar__spinner"
                aria-hidden="true"></span>
              <span class="builder-button__swap">
                <span :class="{ 'is-off': isDeleting(item) }">Delete</span>
                <span :class="{ 'is-off': !isDeleting(item) }">Deleting…</span>
              </span>
            </button>
            <!-- On a draft, when the role may publish. While the view loads the
                 draft for the dialog: a turning ring and Loading…, as Open
                 shows while it opens. -->
            <button
              v-if="mayPublish(tab, item)"
              type="button"
              class="builder-button"
              :data-testid="`draft-publish-${item.id}`"
              :aria-label="`${isPublishing(item) ? 'Loading' : 'Publish'} ${cardName(item)}`"
              aria-haspopup="dialog"
              :aria-disabled="busy || undefined"
              :aria-busy="isPublishing(item) || undefined"
              @click="busy || $emit('publish', item, itemLabel(item))">
              <span
                v-if="isPublishing(item)"
                class="builder-toolbar__spinner"
                aria-hidden="true"></span>
              <builder-icon v-else name="publish" :size="14" />
              <span class="builder-button__swap">
                <span :class="{ 'is-off': isPublishing(item) }">Publish</span>
                <span :class="{ 'is-off': !isPublishing(item) }">Loading…</span>
              </span>
            </button>
          </div>
          <!-- Whether the card is selected, for its name only. -->
          <span
            v-if="tab.selection && selectable(tab.id, item)"
            :id="`card-state-${tab.id}-${index}`"
            hidden
            >{{ tab.selection.has(item) ? 'selected' : 'not selected' }}</span
          >
        </li>
      </ul>
    </div>

    <builder-confirm
      v-if="confirming"
      id="draft-delete"
      :title="`Delete draft ${cardName(confirming)}?`"
      :message="deleteMessage()"
      confirm-label="Delete draft"
      @cancel="confirming = null"
      @confirm="confirmDelete" />
    <builder-confirm
      v-if="confirmingPublished"
      id="published-delete"
      :title="`Delete topology ${itemLabel(confirmingPublished)}?`"
      message="The topology is deleted from phēnix. Drafts and experiments made from it are not changed."
      confirm-label="Delete topology"
      @cancel="confirmingPublished = null"
      @confirm="confirmDeletePublished" />
    <!-- Delete selected: one question for the batch. For one card alone,
         the question is the one its own Delete asks. -->
    <builder-confirm
      v-if="confirmingSelected"
      :id="selectedQuestion.id"
      :title="selectedQuestion.title"
      :message="selectedQuestion.message"
      :confirm-label="selectedQuestion.confirmLabel"
      @cancel="confirmingSelected = null"
      @confirm="confirmDeleteSelected" />

    <!-- Exp's tooltip. It is aria-hidden: the button's name says the same. -->
    <builder-fixed-tooltip :tooltip="tooltip" testid="drafts-tooltip" />
  </section>
</template>

<script>
  /**
   * Names a listed draft or published diagram apart from every other one,
   * for the view to say which it is opening: its id.
   *
   * @param {{id: string}} item
   * @returns {string}
   */
  export function cardKey(item) {
    return item?.id || '';
  }
</script>

<script setup>
  import { computed, nextTick, reactive, ref, watch } from 'vue';

  import BuilderBulkBar from './BuilderBulkBar.vue';
  import BuilderBulkSummary from './BuilderBulkSummary.vue';
  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderFixedTooltip from './BuilderFixedTooltip.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import {
    draftsDeleteMessage,
    topologiesDeleteMessage,
  } from '@/builder/bulk.js';
  import { focusLost } from '@/builder/commands.js';
  import { formatTimestamp } from '@/builder/format.js';
  import {
    columnsOf,
    onListKeydown,
    shiftPress,
    useListSelection,
  } from '@/builder/listSelection.js';
  import { rowTarget } from '@/builder/roving.js';
  import { deleteMessage, newestFirst } from '@/builder/draftMessages.js';

  const props = defineProps({
    // The drafts of every Builder user.
    drafts: { type: Array, default: () => [] },
    published: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false },
    // What the role may do to phenix configs: publish a draft, and delete a
    // published topology.
    canPublish: { type: Boolean, default: true },
    canDeletePublished: { type: Boolean, default: true },
    // The ids of the published diagrams whose topologies are being deleted.
    deletingPublished: { type: Array, default: () => [] },
    // A draft is being made, opened or published.
    busy: { type: Boolean, default: false },
    // The draft or published diagram being opened, as cardKey names it.
    opening: { type: String, default: '' },
    // The draft being loaded for its card's Publish, as cardKey names it.
    publishing: { type: String, default: '' },
    // The drafts the server can no longer read.
    damaged: { type: Array, default: () => [] },
    // The state of the saves of drafts that closed while saving, by
    // cardKey: kind ('saving', 'saved', 'retrying', 'waiting', 'signin' or
    // 'stopped') and text.
    saves: { type: Object, default: () => ({}) },
    // The batch the view is running on the selection of a tab, if one is:
    // { tab, label, done, total }, shown as "Deleting 2 of 5…".
    bulk: { type: Object, default: null },
    // What the last batch left undone: { tab, heading, items }, each item
    // { key, name, reason }.
    bulkResult: { type: Object, default: null },
    // The view's Node Templates tab, when it has one: { count }, the number
    // its name is followed by, or null while it is not known (the tab then
    // shows no number). Its panel is the templates slot.
    templates: { type: Object, default: null },
    // The view's Icons tab, likewise. Its panel is the icons slot.
    icons: { type: Object, default: null },
    // The view's Layers tab, likewise. Its panel is the layers slot.
    layers: { type: Object, default: null },
  });

  const emit = defineEmits([
    'open',
    'delete',
    'delete-published',
    'publish',
    'experiment',
    'blank',
    'import',
    'upload',
    // (tabId, items, names): the selected drafts to delete, once the user
    // has confirmed, and what their cards call them, by cardKey.
    'bulk-delete',
    // (items, names): the selected published topologies, likewise.
    'bulk-delete-published',
    // (tabId, items, names): the selected drafts or published diagrams to
    // save as Builder files.
    'bulk-download',
    'dismiss-bulk-result',
    // (text, slot): for the page's live region.
    'announce',
  ]);

  const rootEl = ref(null);
  const active = ref('drafts');

  // A damaged draft whose title cannot be read is not named by its id.
  function itemLabel(item) {
    return (
      item.name ||
      item.title ||
      item.target ||
      (item.damaged ? 'Damaged draft' : item.id)
    );
  }

  function isOpening(item) {
    return Boolean(props.opening) && props.opening === cardKey(item);
  }

  function isPublishing(item) {
    return Boolean(props.publishing) && props.publishing === cardKey(item);
  }

  // Publish is on every readable draft when the role may change configs.
  // The editor offers it for the same drafts.
  function mayPublish(tab, item) {
    return tab.id === 'drafts' && !item.damaged && props.canPublish;
  }

  // The state of the saves of a draft that closed while saving, if any.
  function saveOf(tab, item) {
    return (tab.id === 'drafts' && props.saves[cardKey(item)]) || null;
  }

  // Every draft, readable or not, can be deleted.
  function mayDelete(tab) {
    return tab.id === 'drafts';
  }

  // A topology whose diagram is read from the Builder file it names: its
  // card has the file's path, and no published document.
  function isFile(item) {
    return item.source === 'file';
  }

  // Delete is on a published topology's card when the role may delete
  // configs. The user deletes a published experiment on the Experiments
  // page, and a topology read from its Builder file on the Configs page.
  function mayDeletePublished(tab, item) {
    return (
      tab.id === 'published' &&
      item.kind === 'Topology' &&
      !isFile(item) &&
      props.canDeletePublished
    );
  }

  function isDeleting(item) {
    return props.deletingPublished.includes(item.id);
  }

  // Drafts are listed with when they last changed, published diagrams with
  // when they were published.
  function stamp(item) {
    const updated = formatTimestamp(item.updatedAt || item.updated);
    if (updated) {
      return { verb: 'Updated', time: updated };
    }

    const published = formatTimestamp(item.createdAt);

    return published ? { verb: 'Published', time: published } : null;
  }

  // Card buttons name the time too, so drafts with the same title have
  // different names (WCAG 2.4.6).
  function cardName(item) {
    const when = stamp(item);
    const name = itemLabel(item);

    if (isFile(item)) {
      return `${name}, read from ${item.path}`;
    }

    return when ? `${name}, ${when.verb.toLowerCase()} ${when.time}` : name;
  }

  // What each tab lists. Drafts come the latest changed first, and damaged
  // drafts after them.
  const lists = computed(() => ({
    drafts: [...newestFirst(props.drafts), ...(props.damaged || [])],
    published: props.published,
  }));

  // Download selected saves drafts that can be read and published diagrams
  // stored on the server, not a topology's Builder file.
  function mayDownload(tabId, item) {
    switch (tabId) {
      case 'drafts':
        return !item.damaged;
      case 'published':
        return !isFile(item);
      default:
        return false;
    }
  }

  // A card can be selected when a bulk action could act on it: every draft
  // (all can be deleted), and a published diagram the user may download or
  // delete.
  function selectable(tabId, item) {
    switch (tabId) {
      case 'drafts':
        return true;
      case 'published':
        return (
          mayDownload(tabId, item) || mayDeletePublished({ id: tabId }, item)
        );
      default:
        return false;
    }
  }

  // The selection of each tab of cards, by cardKey, which also keeps the
  // card that is the list's Tab stop. Every card is in it, for the keys to
  // move to. Only cards a batch can act on can be selected.
  const selections = Object.fromEntries(
    ['drafts', 'published'].map((id) => [
      id,
      reactive(
        useListSelection(() => lists.value[id], cardKey, {
          selectable: (item) => selectable(id, item),
        }),
      ),
    ]),
  );

  // The drafts, the published diagrams, then the view's own tabs.
  const tabs = computed(() => {
    const { drafts, published } = lists.value;

    // Each tab says how many it lists, after its name.
    return [
      {
        id: 'drafts',
        label: 'Drafts',
        items: drafts,
        empty: 'There are no drafts yet. Start from a blank diagram.',
      },
      {
        id: 'published',
        label: 'Published Diagrams',
        items: published,
        empty: 'There are no published diagrams yet.',
      },
      ...(props.templates
        ? [
            {
              id: 'templates',
              label: 'Node Templates',
              items: [],
              count: props.templates.count,
              custom: true,
            },
          ]
        : []),
      ...(props.layers
        ? [
            {
              id: 'layers',
              label: 'Layers',
              items: [],
              count: props.layers.count,
              custom: true,
            },
          ]
        : []),
      ...(props.icons
        ? [
            {
              id: 'icons',
              label: 'Icons',
              items: [],
              count: props.icons.count,
              custom: true,
            },
          ]
        : []),
    ].map((tab) => ({
      ...tab,
      // A view's tab whose count is not known yet (null) shows none.
      count: 'count' in tab ? tab.count : tab.items.length,
      selection: selections[tab.id] || null,
    }));
  });

  const tooltip = useFixedTooltip({ side: 'below' });
  const { tipEvents } = tooltip;

  // When the shown tab goes (the view removed one of its own), Drafts
  // replaces it.
  watch(tabs, (list) => {
    if (!list.some((tab) => tab.id === active.value)) {
      active.value = 'drafts';
    }
  });

  // APG tabs pattern with automatic activation.
  function onTabKeydown(event, tab) {
    const ids = tabs.value.map((item) => item.id);
    const next = rowTarget(event.key, ids.indexOf(tab.id), ids.length);

    if (next === undefined) {
      return;
    }

    event.preventDefault();
    active.value = ids[next];
    focusTab(active.value);
  }

  function focusTab(id) {
    document.getElementById(`tab-${id}`)?.focus();
  }

  function focusActiveTab() {
    focusTab(active.value);
  }

  // For the command palette's Show commands: selects a tab and focuses it,
  // as the arrow keys do.
  function showTab(id) {
    if (tabs.value.some((tab) => tab.id === id)) {
      active.value = id;
      nextTick(() => focusTab(id));
    }
  }

  /**
   * Focuses the Open button of a listed draft or diagram, or the Delete
   * button of a damaged draft, switching to its tab first.
   *
   * @param {string} id
   * @param {string} [testid] the button to focus first, when it is there
   * @returns {Promise<boolean>} whether the button took focus
   */
  async function focusDraft(id, testid) {
    const tab = tabs.value.find((entry) =>
      entry.items.some((item) => item.id === id),
    );
    if (!id || !tab) {
      return false;
    }

    active.value = tab.id;
    await nextTick();
    const button =
      (testid &&
        rootEl.value?.querySelector(`[data-testid="${CSS.escape(testid)}"]`)) ||
      rootEl.value?.querySelector(
        `[data-testid="draft-open-${CSS.escape(id)}"], [data-testid="draft-delete-${CSS.escape(id)}"]`,
      );
    button?.focus();

    return Boolean(button) && document.activeElement === button;
  }

  // A deleted draft and its history cannot be recovered, so Delete asks
  // first (WCAG 3.3.4).
  const confirming = ref(null);

  function confirmDelete() {
    const item = confirming.value;

    confirming.value = null;
    requestDelete(item);
  }

  // Deleting a card removes the focused Delete button, and a card with focus
  // on it can leave its list when the lists are read again (for example,
  // deleted elsewhere). Once its tab no longer holds the draft, focus moves
  // to the card that took its place, or to the tab when none is left (WCAG
  // 2.4.3). A card that stays can lose the button focus was on instead
  // (a re-sorted card moves): focus then goes back to that button, or to
  // the card's first button when it is gone. A delete that failed
  // leaves focus alone. The draft is named as on its card, with its time,
  // since many drafts have the same title.
  let deleting = null;

  function requestDelete(item) {
    deleting = cardPlace(item);
    emit('delete', item, cardName(item));
  }

  // Where a card is, for focus to go to the one that takes its place.
  function cardPlace(item) {
    const tab = tabs.value.find((entry) => entry.items.includes(item));

    return {
      id: item.id,
      tab: tab?.id || active.value,
      index: tab ? tab.items.indexOf(item) : 0,
    };
  }

  // A deleted topology can only come back by publishing a draft again, so
  // Delete asks first here too (WCAG 3.3.4). The card goes once the server
  // has deleted it, and focus moves on as for a draft.
  const confirmingPublished = ref(null);

  function confirmDeletePublished() {
    const item = confirmingPublished.value;

    confirmingPublished.value = null;
    deleting = cardPlace(item);
    emit('delete-published', item, itemLabel(item));
  }

  // The card focus is on, as requestDelete records it. This reads it
  // before the lists re-render.
  function focusedCard() {
    const card = document.activeElement?.closest?.('.builder-card');
    const button = card?.querySelector(
      '[data-testid^="draft-open-"], [data-testid^="draft-delete-"]',
    );
    if (!button || !rootEl.value?.contains(card)) {
      return null;
    }

    return {
      id: button.dataset.testid.replace(/^draft-(open|delete)-/, ''),
      tab: active.value,
      index: [...card.parentElement.children].indexOf(card),
      testid: document.activeElement.dataset?.testid,
    };
  }

  watch(
    () => [props.drafts, props.damaged, props.published],
    async () => {
      const pending = deleting || focusedCard();
      deleting = null;
      if (!pending) {
        return;
      }

      const items =
        tabs.value.find((entry) => entry.id === pending.tab)?.items || [];
      const stays = items.some((item) => item.id === pending.id);

      await nextTick();
      // Focus is where it was, or the user moved focus elsewhere while the
      // delete ran.
      if (!focusLost()) {
        return;
      }

      const next = stays
        ? pending
        : items[Math.min(pending.index, items.length - 1)];
      if (!next || !(await focusDraft(next.id, stays && pending.testid))) {
        focusActiveTab();
      }
    },
  );

  // --- selection and bulk actions ---------------------------------------

  // Every change of a selection is said, in a slot of its own, so quick
  // changes come to the last one.
  function announceSelection(tab) {
    emit(
      'announce',
      `${tab.selection.count} of ${tab.selection.total} selected.`,
      'selection',
    );
  }

  // Whether Shift was held as a card's checkbox was pressed.
  const shift = shiftPress();

  // A card's checkbox: with Shift, the range from the card last pressed
  // becomes as the box now is. The box then shows what the selection holds.
  function select(tab, item, event) {
    tab.selection.press(item, {
      checked: event.target.checked,
      range: shift.take(),
    });
    event.target.checked = tab.selection.has(item);
    announceSelection(tab);
  }

  function selectAll(tab) {
    tab.selection.toggleAll();
    announceSelection(tab);
  }

  // A card's name: its title, and for a card that can be selected whether
  // it is.
  function cardLabelledBy(tab, item, index) {
    const name = `card-name-${tab.id}-${index}`;

    return tab.selection && selectable(tab.id, item)
      ? `${name} card-state-${tab.id}-${index}`
      : name;
  }

  // Focuses the card at a position of a tab's list, which becomes its Tab
  // stop.
  function focusCard(tab, index) {
    const item = tab.items[index];
    const list = rootEl.value?.querySelector(
      `[data-testid="drafts-list-${CSS.escape(tab.id)}"]`,
    );

    if (item && list?.children[index]) {
      tab.selection.focused(item);
      list.children[index].focus();
    }
  }

  // What the keys change: each is said, as a press of a checkbox is.
  const SELECTING = ['extend', 'toggle', 'range', 'all', 'clear'];

  // The keys of a list of cards (see onListKeydown in listSelection.js).
  // While a batch runs the selection stays as it is: the keys only move.
  function onCardKeydown(event, tab, index) {
    if (!tab.selection) {
      return;
    }

    const locked = Boolean(props.bulk);
    const action = onListKeydown(event, {
      selection: tab.selection,
      items: tab.items,
      index,
      focus: (to) => focusCard(tab, to),
      columns: columnsOf(event.currentTarget.parentElement),
      locked,
      canDelete: canDeleteSome(tab) && deletable(tab).length > 0 && !props.busy,
      onDelete: () => askDeleteSelected(tab),
    });

    if (
      SELECTING.includes(action?.type) &&
      (action.type !== 'toggle' || selectable(tab.id, tab.items[index]))
    ) {
      announceSelection(tab);
    }
  }

  // The selected cards Delete selected deletes. This leaves out a topology
  // that its own card is deleting now.
  function deletable(tab) {
    return tab.selection.selected.filter((item) =>
      tab.id === 'published'
        ? mayDeletePublished(tab, item) && !isDeleting(item)
        : mayDelete(tab, item),
    );
  }

  // The selected cards Download selected saves.
  function downloadable(tab) {
    return tab.selection.selected.filter((item) => mayDownload(tab.id, item));
  }

  // Download selected is there when any card of the tab can be saved.
  function canDownloadSome(tab) {
    return tab.items.some((item) => mayDownload(tab.id, item));
  }

  function downloadSelected(tab) {
    const items = downloadable(tab);

    if (items.length && !props.bulk && !props.busy) {
      emit('bulk-download', tab.id, items, namesOf(items));
    }
  }

  // Delete selected is there when any card of the tab can be deleted.
  function canDeleteSome(tab) {
    return tab.items.some((item) =>
      tab.id === 'published'
        ? mayDeletePublished(tab, item)
        : mayDelete(tab, item),
    );
  }

  // What the cards call the items, by cardKey: the view uses these names
  // when it announces the batch.
  function namesOf(items) {
    return Object.fromEntries(
      items.map((item) => [cardKey(item), cardName(item)]),
    );
  }

  // Deleted drafts, their histories and deleted topologies cannot be
  // recovered, so Delete selected asks first, once for the batch (WCAG
  // 3.3.4): { tab, items }, the tab's id and the cards to delete.
  const confirmingSelected = ref(null);

  function askDeleteSelected(tab) {
    const items = deletable(tab);

    if (items.length && !props.bulk && !props.busy) {
      confirmingSelected.value = { tab: tab.id, items };
    }
  }

  // The question: what goes, by name, and how many. For one card alone,
  // the question is the one its own Delete asks.
  const selectedQuestion = computed(() => {
    const { tab, items } = confirmingSelected.value || { items: [] };
    const [first] = items;
    const n = items.length;
    const names = items.map(itemLabel);

    if (tab === 'published') {
      return n === 1
        ? {
            id: 'published-delete',
            title: `Delete topology ${itemLabel(first)}?`,
            message:
              'The topology is deleted from phēnix. Drafts and experiments made from it are not changed.',
            confirmLabel: 'Delete topology',
          }
        : {
            id: 'bulk-delete-published',
            title: `Delete ${n} topologies?`,
            message: topologiesDeleteMessage(names),
            confirmLabel: `Delete ${n} topologies`,
          };
    }

    if (n === 1) {
      return {
        id: 'draft-delete',
        title: `Delete draft ${cardName(first)}?`,
        message: deleteMessage(),
        confirmLabel: 'Delete draft',
      };
    }

    return {
      id: 'bulk-delete',
      title: `Delete ${n} drafts?`,
      message: draftsDeleteMessage(names),
      confirmLabel: `Delete ${n} drafts`,
    };
  });

  // The dialog gives focus back to Delete selected, which keeps it while
  // the batch runs.
  function confirmDeleteSelected() {
    const { tab, items } = confirmingSelected.value;

    confirmingSelected.value = null;

    if (tab === 'published') {
      emit('bulk-delete-published', items, namesOf(items));
    } else {
      emit('bulk-delete', tab, items, namesOf(items));
    }
  }

  // The summary of what the last batch left undone, and the tab it is
  // shown on: the batch's, or Drafts once that tab is gone.
  let summary = null;
  const summaryTab = computed(() =>
    tabs.value.some((tab) => tab.id === props.bulkResult?.tab)
      ? props.bulkResult.tab
      : 'drafts',
  );

  // It takes focus when it appears, on its tab.
  watch(
    () => props.bulkResult,
    async (now) => {
      if (!now) {
        return;
      }

      active.value = summaryTab.value;
      await nextTick();
      summary?.focus();
    },
    { flush: 'post' },
  );

  // A batch that ended with nothing left undone leaves focus on its
  // button. When the row went with the last card that could be selected,
  // focus moves to the tab instead of falling to <body>.
  watch(
    () => props.bulk,
    async (now, before) => {
      if (now || !before || props.bulkResult) {
        return;
      }

      await nextTick();

      if (focusLost()) {
        focusActiveTab();
      }
    },
    { flush: 'post' },
  );

  // Dismiss removes the summary, so focus moves to Select all, or to the
  // tab when the list has no row.
  async function dismissResult(tabId) {
    emit('dismiss-bulk-result');
    await nextTick();

    const all = rootEl.value?.querySelector(
      `[data-testid="bulk-all-${tabId}"]`,
    );

    if (all && !all.disabled) {
      all.focus();
    } else {
      focusActiveTab();
    }
  }

  // activeTab is the shown tab's id, for the palette's Show commands.
  defineExpose({
    activeTab: active,
    focusActiveTab,
    focusDraft,
    showTab,
  });
</script>

<style scoped>
  /* The container its buttons' labels follow, as the editor header's do
     (see .builder-header__label in builder.css). */
  .builder-drafts__header {
    container: builder-header / inline-size;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    margin-bottom: 0.75rem;
  }

  .builder-drafts__header h1 {
    font-size: 1.2rem;
    font-weight: 700;
    margin: 0;
  }

  .builder-drafts__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }

  /* What Download selected does, beside it in the row. */
  .builder-drafts__bulk-note {
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }

  .builder-cards {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .builder-card h2 {
    min-width: 0;
    font-weight: 700;
    margin: 0 0 0.25rem;
  }

  /* Who created it, the time and the rest, a line each. */
  .builder-card p.builder-card__meta {
    margin: 0;
  }

  /* Says in words where the diagram is read from. The frame only sets the
     word apart from the name. */
  .builder-drafts__tag {
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
  .builder-drafts__damaged {
    display: flex;
    align-items: flex-start;
    gap: 0.35rem;
    margin: 0 0 0.4rem;
  }

  .builder-drafts__damaged > :first-child {
    flex: none;
    margin-top: 0.15em;
  }

  .builder-drafts__save {
    display: flex;
    align-items: flex-start;
    gap: 0.35rem;
    margin: 0 0 0.4rem;
    overflow-wrap: anywhere;
  }

  .builder-drafts__save > :first-child {
    flex: none;
    margin-top: 0.15em;
  }

  .builder-drafts__save--saved {
    color: var(--bx-success);
  }

  .builder-drafts__save--retrying,
  .builder-drafts__save--waiting,
  .builder-drafts__save--signin {
    color: var(--bx-warning);
  }

  .builder-drafts__save--stopped {
    color: var(--bx-danger);
  }

  .builder-button[aria-busy='true'] {
    cursor: progress;
  }
</style>
