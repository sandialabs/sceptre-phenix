<!--
  The drafts page's Icons tab: the server's icon library, one for every
  Builder user (see iconLibrary.js). The view (Builder.vue) puts it in the icons
  slot of BuilderDrafts.

  Each icon has a card: the icon and its name, its other names, who
  uploaded it ("Server" for an icon that the server added from its template
  files), and
  its size in pixels and bytes. Preview opens a dialog that shows the icon
  at its stored size and at the sizes a node draws it
  (dialogs/IconPreviewDialog.vue). Download saves the PNG file exactly as
  the server stores it, named after the icon (see saveIconFile in
  icons.js). Delete is on every icon the server says can be deleted
  (canDelete): every icon but those of the server's template files. A
  deleted icon cannot come back, so Delete asks first, as the Custom icons
  dialog does.

  Upload icon… (IconUpload.vue, as in the Custom icons dialog) adds an
  icon. A line says how many icons the library holds, of the most the server
  allows. A filter field narrows the cards by name, other name or who
  uploaded it.

  The user can delete several icons at once. A card the user may delete
  has a checkbox at its top right, and the row above the list
  (BuilderBulkBar.vue) has Select all, how many are selected, and Delete
  selected, which asks once for the batch. The cards are one Tab stop, and
  the keys of listSelection.js move through them and select
  (onListKeydown), as on the other tabs. What a batch left undone is listed
  under the row, in a summary that takes focus.

  The status and error regions are rendered, empty, from the start. Focus
  moves:

  - to the new icon's Preview after an upload
  - to the card that takes the place of a deleted one, or to Upload icon…,
    or else to the tab (the `lost-focus` event) after a delete.

  showIcon(name) opens the preview of an icon by its name or another name,
  for the view's openIcons (and the command palette).

  Show chooses the list: the built-in icons (builtinIcons.js, the
  default), or the server's icons. A built-in icon's card shows its
  drawing, its label, its key and "Built-in", with Preview and
  Download. It cannot be selected, deleted or renamed. Download saves its
  drawing as an SVG file. The tab's count is of the server's icons only.
  An upload, and showIcon, show the server's icons, where the icon is.
  When the server's icons cannot be read, the tab says so, with Retry,
  whatever Show shows.
-->
<template>
  <div ref="rootEl" class="builder-icons-tab" data-testid="icons-tab">
    <p class="builder-icons-tab__note">
      An icon is a small image that every user of this server can use in
      diagrams and templates. A node draws it at 16, 24 or 32 pixels. PNG, JPEG,
      GIF, WebP and SVG files are converted to a PNG of at most 96 by 96 pixels.
    </p>

    <div class="builder-icons-tab__actions">
      <icon-upload
        ref="uploader"
        v-model:busy="busy"
        prefix="icons-tab"
        :library="library"
        :rasterize="rasterize"
        :status="status"
        :error="error"
        @uploaded="onUploaded" />
    </div>

    <p
      v-if="usage"
      class="builder-icons-tab__note"
      data-testid="icons-tab-usage">
      {{ usage }}
    </p>
    <p
      v-if="showing === 'server' && someLocked"
      class="builder-icons-tab__note"
      data-testid="icons-tab-delete-note">
      Icons from the server's template files cannot be deleted.
    </p>

    <p class="builder-dialog__message" role="status">
      <span
        v-if="status.text"
        :key="status.key"
        data-testid="icons-tab-status"
        >{{ status.text }}</span
      >
    </p>
    <p class="builder-dialog__message builder-dialog__error" role="alert">
      <span v-if="error.text" :key="error.key" data-testid="icons-tab-error">{{
        error.text
      }}</span>
    </p>

    <div class="builder-field builder-icons-tab__show">
      <label for="icons-tab-show">Show</label>
      <select
        id="icons-tab-show"
        v-model="showing"
        data-testid="icons-tab-show">
        <option value="builtin">Built-in icons</option>
        <option value="server">Server icons</option>
      </select>
    </div>

    <div
      v-if="library.state.status === 'failed'"
      class="builder-icons-tab__failed">
      <p role="alert" data-testid="icons-tab-failed">
        Could not load the server's icons. {{ library.state.error }}
      </p>
      <button
        type="button"
        class="builder-button"
        data-testid="icons-tab-retry"
        @click="load">
        Retry
      </button>
    </div>

    <p
      v-if="
        showing === 'server' &&
        library.state.status === 'loading' &&
        !library.state.icons.length
      "
      role="status"
      data-testid="icons-tab-loading">
      Loading…
    </p>
    <p
      v-else-if="
        showing === 'server' &&
        library.state.status === 'ready' &&
        !library.state.icons.length
      "
      data-testid="icons-tab-empty">
      The server has no icons yet.
    </p>
    <template v-else-if="showing === 'server' && library.state.icons.length">
      <div class="builder-field builder-icons-tab__filter">
        <label for="icons-tab-filter">Filter icons</label>
        <input
          id="icons-tab-filter"
          v-model="filterText"
          class="builder-input"
          type="search"
          autocomplete="off"
          spellcheck="false"
          aria-describedby="icons-tab-filter-count"
          data-testid="icons-tab-filter" />
        <span id="icons-tab-filter-count" class="builder-icons-tab__note">
          {{ filterCount }}
        </span>
      </div>

      <builder-bulk-bar
        v-if="selection.total"
        id="icons"
        label="Icons"
        :count="selection.count"
        :total="selection.total"
        :state="selection.state"
        :running="running"
        :disabled="Boolean(busy)"
        @toggle-all="selectAll">
        <button
          type="button"
          class="builder-button"
          data-testid="bulk-delete-icons"
          aria-haspopup="dialog"
          aria-describedby="bulk-count-icons"
          :aria-disabled="!selection.count || Boolean(busy) || undefined"
          @click="askDeleteSelected">
          Delete selected
        </button>
      </builder-bulk-bar>
      <builder-bulk-summary
        v-if="outcome"
        :ref="(element) => (summary = element)"
        :heading="outcome.heading"
        :items="outcome.items"
        @dismiss="dismissOutcome" />

      <p v-if="!shown.length" data-testid="icons-tab-none-shown">
        No icon matches the filter.
      </p>
      <ul
        v-else
        class="builder-cards"
        :aria-busy="Boolean(running) || undefined"
        data-testid="icons-list">
        <!-- A card is focusable, one at a time (the list's Tab stop), and
             named by its icon and whether it is selected. Focus on it, or
             on a control in it, makes it the Tab stop. -->
        <li
          v-for="(icon, index) in shown"
          :key="icon.name"
          class="builder-card builder-panel"
          :class="{ 'is-selected': selection.has(icon) }"
          :tabindex="selection.tabStop(icon) ? 0 : -1"
          :aria-labelledby="cardLabelledBy(icon, index)"
          :data-icon="icon.name"
          :data-testid="`icon-card-${icon.name}`"
          @focusin="selection.focused(icon)"
          @keydown="onCardKeydown($event, index)">
          <div class="builder-card__head">
            <!-- The icon is decoration: the name says which it is. -->
            <h2 :id="`icon-card-name-${index}`">
              <builder-icon name="image" :src="srcOf(icon)" :size="32" />
              <span>{{ icon.name }}</span>
            </h2>
            <label v-if="icon.canDelete" class="builder-card__select">
              <input
                type="checkbox"
                :checked="selection.has(icon)"
                :disabled="Boolean(busy)"
                :data-testid="`icon-card-select-${icon.name}`"
                @pointerdown="shift.pointerdown"
                @keydown="shift.keydown"
                @click="shift.click"
                @change="select(icon, $event)" />
              <span class="builder-visually-hidden">
                Select {{ icon.name }}
              </span>
            </label>
          </div>
          <p
            v-if="icon.aliases?.length"
            class="builder-card__meta"
            :data-testid="`icon-card-aliases-${icon.name}`">
            Also named {{ icon.aliases.join(', ') }}
          </p>
          <p
            class="builder-card__meta"
            :data-testid="`icon-card-owner-${icon.name}`">
            {{ iconOwnerText(icon) }}
          </p>
          <p
            class="builder-card__meta"
            :data-testid="`icon-card-size-${icon.name}`">
            {{ iconSizeText(icon) }}
          </p>

          <div class="builder-card__actions">
            <button
              type="button"
              class="builder-button"
              :data-testid="`icon-preview-${icon.name}`"
              :aria-label="`Preview ${icon.name}`"
              aria-haspopup="dialog"
              @click="preview(icon)">
              Preview
            </button>
            <button
              type="button"
              class="builder-button"
              :data-testid="`icon-download-${icon.name}`"
              :aria-label="`Download ${icon.name}`"
              @click="download(icon)">
              <builder-icon name="download" :size="14" />
              Download
            </button>
            <button
              v-if="icon.canDelete"
              type="button"
              class="builder-button"
              :data-testid="`icon-delete-${icon.name}`"
              :aria-label="`Delete ${icon.name} from the server`"
              aria-haspopup="dialog"
              :aria-disabled="Boolean(busy) || undefined"
              @click="askDelete(icon, index)">
              Delete
            </button>
          </div>
          <!-- Whether the card is selected, for its name only. -->
          <span v-if="icon.canDelete" :id="`icon-card-state-${index}`" hidden>{{
            selection.has(icon) ? 'selected' : 'not selected'
          }}</span>
        </li>
      </ul>
    </template>
    <template v-else-if="showing === 'builtin'">
      <p class="builder-icons-tab__note" data-testid="icons-tab-builtin-note">
        These icons are part of phenix. You cannot delete or rename them.
        Download saves an icon as an SVG file.
      </p>
      <div class="builder-field builder-icons-tab__filter">
        <label for="icons-tab-filter">Filter icons</label>
        <input
          id="icons-tab-filter"
          v-model="filterText"
          class="builder-input"
          type="search"
          autocomplete="off"
          spellcheck="false"
          aria-describedby="icons-tab-filter-count"
          data-testid="icons-tab-filter" />
        <span id="icons-tab-filter-count" class="builder-icons-tab__note">
          {{ filterCount }}
        </span>
      </div>
      <p v-if="!builtinShown.length" data-testid="icons-tab-none-shown">
        No icon matches the filter.
      </p>
      <ul v-else class="builder-cards" data-testid="builtin-icons-list">
        <!-- A card is focusable, one at a time (the list's Tab stop), and
             named by its label. The keys move through the cards. -->
        <li
          v-for="(icon, index) in builtinShown"
          :key="icon.key"
          class="builder-card builder-panel"
          :tabindex="builtinFocus.tabStop(icon) ? 0 : -1"
          :aria-labelledby="`builtin-icon-name-${index}`"
          :data-testid="`builtin-icon-card-${icon.key}`"
          @focusin="builtinFocus.focused(icon)"
          @keydown="onBuiltinKeydown($event, index)">
          <div class="builder-card__head">
            <h2 :id="`builtin-icon-name-${index}`">
              <builder-icon :name="icon.key" :size="32" />
              <span>{{ icon.label }}</span>
            </h2>
          </div>
          <p
            class="builder-card__meta"
            :data-testid="`builtin-icon-key-${icon.key}`">
            Key: {{ icon.key }}
          </p>
          <p
            class="builder-card__meta"
            :data-testid="`builtin-icon-owner-${icon.key}`">
            {{ BUILTIN_ICON_OWNER }}
          </p>
          <div class="builder-card__actions">
            <button
              type="button"
              class="builder-button"
              :data-testid="`builtin-icon-preview-${icon.key}`"
              :aria-label="`Preview ${icon.label}`"
              aria-haspopup="dialog"
              @click="preview(icon)">
              Preview
            </button>
            <button
              type="button"
              class="builder-button"
              :data-testid="`builtin-icon-download-${icon.key}`"
              :aria-label="`Download ${icon.label}`"
              @click="download(icon)">
              <builder-icon name="download" :size="14" />
              Download
            </button>
          </div>
        </li>
      </ul>
    </template>

    <icon-preview-dialog
      v-if="previewing"
      :icon="previewing"
      :status="status"
      :error="error"
      @download="download"
      @close="previewing = null" />

    <builder-confirm
      v-if="deleting"
      id="icon-delete"
      title="Delete icon?"
      :message="`Delete ${deleting.icon.name} from the server? Diagrams and templates that use it, by any of its names, will show their built-in icon instead.`"
      confirm-label="Delete"
      @cancel="deleting = null"
      @confirm="confirmDelete" />
    <!-- Delete selected: one question for the batch. -->
    <builder-confirm
      v-if="deletingSelected"
      id="icons-delete"
      :title="`Delete ${count(deletingSelected.length, 'icon')}?`"
      :message="iconsDeleteMessage(deletingSelected.map((icon) => icon.name))"
      :confirm-label="`Delete ${count(deletingSelected.length, 'icon')}`"
      @cancel="deletingSelected = null"
      @confirm="confirmDeleteSelected" />
  </div>
</template>

<script setup>
  import { saveAs } from 'file-saver';
  import {
    computed,
    h,
    nextTick,
    onBeforeUnmount,
    onMounted,
    reactive,
    ref,
    render,
  } from 'vue';

  import BuilderBulkBar from './BuilderBulkBar.vue';
  import BuilderBulkSummary from './BuilderBulkSummary.vue';
  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import IconUpload from './IconUpload.vue';
  import IconPreviewDialog from './dialogs/IconPreviewDialog.vue';
  import { useMessage } from './dialogs/message.js';

  import { count } from '@/builder/announce.js';
  import {
    BUILTIN_ICON_OWNER,
    builtinIconMatches,
    builtinIcons,
    saveBuiltinIconFile,
  } from '@/builder/builtinIcons.js';
  import {
    bulkFailureHeading,
    bulkOutcome,
    iconsDeleteMessage,
    runBulk,
  } from '@/builder/bulk.js';
  import { focusLost } from '@/builder/commands.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import {
    iconOwnerText,
    iconSizeText,
    iconSrc,
    isIconName,
    rasterizeIcon,
    saveIconFile,
    usageText,
  } from '@/builder/icons.js';
  import {
    columnsOf,
    onListKeydown,
    shiftPress,
    useListSelection,
  } from '@/builder/listSelection.js';

  const props = defineProps({
    // The server's icon library (see createIconLibrary in iconLibrary.js).
    library: { type: Object, default: () => iconLibrary },
    // Makes an icon of a chosen file (see rasterizeIcon).
    rasterize: { type: Function, default: rasterizeIcon },
    // Saves a file (file-saver's saveAs).
    save: { type: Function, default: saveAs },
  });

  // lost-focus: focus would fall to <body>, so the view focuses the tab.
  const emit = defineEmits(['lost-focus']);

  const library = props.library;
  const status = useMessage();
  const error = useMessage();

  const rootEl = ref(null);
  const uploader = ref(null);
  const filterText = ref('');
  // What is in progress: 'convert' or 'upload' (IconUpload's), 'delete' or
  // 'bulk', or ''. A busy button keeps focus, and a press is then ignored.
  const busy = ref('');
  // The icon the preview shows, or null.
  const previewing = ref(null);
  // The icon Delete asks about, and the place of its card, or null.
  const deleting = ref(null);
  // The icons Delete selected asks about, or null.
  const deletingSelected = ref(null);
  // The batch in progress, { label, done, total }, and what the last batch
  // left undone, { heading, items }.
  const running = ref(null);
  const outcome = ref(null);
  let summary = null;
  // Set when the tab goes. An answer that comes later changes nothing.
  let closed = false;

  const usage = computed(() => usageText(library.state));

  // Whether some icon has no Delete, which a note then explains.
  const someLocked = computed(() =>
    library.state.icons.some((icon) => !icon.canDelete),
  );

  const shown = computed(() => {
    const words = filterText.value.trim().toLowerCase();

    if (!words) {
      return library.state.icons;
    }

    return library.state.icons.filter((icon) =>
      [icon.name, iconOwnerText(icon), ...(icon.aliases || [])].some((text) =>
        String(text || '')
          .toLowerCase()
          .includes(words),
      ),
    );
  });

  // Which list Show shows: 'builtin' (the default) or 'server'.
  const showing = ref('builtin');
  const BUILTIN_ICONS = builtinIcons();

  const builtinShown = computed(() =>
    BUILTIN_ICONS.filter((icon) => builtinIconMatches(icon, filterText.value)),
  );

  const filterCount = computed(() => {
    if (!filterText.value.trim()) {
      return '';
    }

    const [listed, all] =
      showing.value === 'builtin'
        ? [builtinShown.value, BUILTIN_ICONS]
        : [shown.value, library.state.icons];

    return `${listed.length} of ${all.length} icons shown.`;
  });

  function srcOf(icon) {
    const key = isIconName(icon?.name) ? icon.name : 'icon';

    return iconSrc(key, { [key]: icon });
  }

  async function load() {
    try {
      await library.load();
    } catch {
      // The library's state says why.
    }
  }

  // --- preview and download ----------------------------------------------

  function preview(icon) {
    status.clear();
    error.clear();
    previewing.value = icon;
  }

  // A built-in icon's drawing as XML: BuilderIcon's, drawn apart from the
  // page.
  function builtinMarkup(key) {
    const holder = document.createElement('div');

    render(h(BuilderIcon, { name: key }), holder);

    const drawing = holder.querySelector('g');
    const markup = drawing
      ? new XMLSerializer().serializeToString(drawing)
      : '';

    render(null, holder);

    return markup;
  }

  function download(icon) {
    status.clear();
    error.clear();

    if (icon?.builtin) {
      const fileName = saveBuiltinIconFile(icon, builtinMarkup(icon.key), {
        saveAs: props.save,
      });

      if (fileName) {
        status.set(`Saved ${fileName}.`);
      } else {
        error.set(`${icon.label} cannot be saved.`);
      }

      return;
    }

    const fileName = saveIconFile(icon, { saveAs: props.save });

    if (fileName) {
      status.set(`Saved ${fileName}.`);
    } else {
      error.set(`${icon.name} cannot be saved: its data is not a PNG icon.`);
    }
  }

  /**
   * Opens the preview of an icon, by its name or another of its names,
   * once the library is read.
   *
   * @param {string} name
   * @returns {Promise<boolean>} whether the icon was found
   */
  async function showIcon(name) {
    try {
      await library.ensure();
    } catch {
      // The library's state says why.
    }

    if (closed) {
      return false;
    }

    const icon = library.lookup(name);

    if (!icon) {
      error.set(`The server has no icon named ${name}.`);

      return false;
    }

    // The preview opens over the server's icons, which list the icon.
    showing.value = 'server';
    preview(icon);

    return true;
  }

  // --- upload --------------------------------------------------------------

  // Focuses a card's button once the list shows the card. The filter is
  // cleared first when it hides the card.
  async function focusCardButton(name, testid) {
    showing.value = 'server';

    if (!shown.value.some((icon) => icon.name === name)) {
      filterText.value = '';
    }

    await nextTick();
    rootEl.value
      ?.querySelector(`[data-testid="${testid}-${CSS.escape(name)}"]`)
      ?.focus();
  }

  async function onUploaded({ icon }) {
    if (!closed) {
      await focusCardButton(icon.name, 'icon-preview');
    }
  }

  // --- delete --------------------------------------------------------------

  function askDelete(icon, index) {
    if (!busy.value) {
      deleting.value = { icon, index };
    }
  }

  // Focus on a control that is gone moves to the card at the place of the
  // deleted one, else to Upload icon…, else to the tab.
  async function keepFocus(index) {
    await nextTick();

    if (!focusLost()) {
      return;
    }

    const last = shown.value.length - 1;

    if (last >= 0) {
      focusCard(Math.max(0, Math.min(index, last)));
    } else {
      uploader.value?.focus();
    }

    if (focusLost()) {
      emit('lost-focus');
    }
  }

  async function confirmDelete() {
    const { icon, index } = deleting.value || {};

    deleting.value = null;

    if (!icon || busy.value) {
      return;
    }

    status.clear();
    error.clear();
    busy.value = 'delete';

    try {
      await library.remove(icon.name);

      if (!closed) {
        status.set(`Deleted ${icon.name} from the server.`);
        await keepFocus(index);
      }
    } catch (caught) {
      if (!closed) {
        error.set(library.failure(caught));
      }
    } finally {
      busy.value = '';
    }
  }

  // --- selecting several icons -------------------------------------------

  // The selection, by name, of the icons shown. Only an icon the user may
  // delete can be selected. Every card is in it, for the keys to move to.
  const selection = reactive(
    useListSelection(shown, (icon) => icon.name, {
      selectable: (icon) => Boolean(icon?.canDelete),
    }),
  );

  // Every change of the selection is said in the status region.
  function announceSelection() {
    status.set(`${selection.count} of ${selection.total} selected.`);
  }

  // Whether Shift was held as a card's checkbox was pressed.
  const shift = shiftPress();

  // A card's checkbox. With Shift, every card in the range from the card
  // last pressed takes the box's new state.
  function select(icon, event) {
    selection.press(icon, {
      checked: event.target.checked,
      range: shift.take(),
    });
    event.target.checked = selection.has(icon);
    announceSelection();
  }

  function selectAll() {
    selection.toggleAll();
    announceSelection();
  }

  // A card's name: its icon's name and, for a card that can be selected,
  // whether it is selected.
  function cardLabelledBy(icon, index) {
    const name = `icon-card-name-${index}`;

    return icon.canDelete ? `${name} icon-card-state-${index}` : name;
  }

  // Focuses the card at a position of the list, which becomes its Tab stop.
  function focusCard(index) {
    const icon = shown.value[index];
    const card = rootEl.value?.querySelector('[data-testid="icons-list"]')
      ?.children[index];

    if (icon && card) {
      selection.focused(icon);
      card.focus();
    }
  }

  // The built-in icons' cards: one Tab stop, which the keys move. Nothing in
  // the list can be selected.
  const builtinFocus = reactive(
    useListSelection(builtinShown, (icon) => icon.key, {
      selectable: () => false,
    }),
  );

  function focusBuiltinCard(index) {
    const icon = builtinShown.value[index];
    const card = rootEl.value?.querySelector(
      '[data-testid="builtin-icons-list"]',
    )?.children[index];

    if (icon && card) {
      builtinFocus.focused(icon);
      card.focus();
    }
  }

  function onBuiltinKeydown(event, index) {
    onListKeydown(event, {
      selection: builtinFocus,
      items: builtinShown.value,
      index,
      focus: focusBuiltinCard,
      columns: columnsOf(event.currentTarget.parentElement),
      locked: true,
    });
  }

  // What the keys change: each is said, as a press of a checkbox is.
  const SELECTING = ['extend', 'toggle', 'range', 'all', 'clear'];

  // The keys of the list of cards (see onListKeydown in listSelection.js).
  // While a change is in progress, the selection stays as it is.
  function onCardKeydown(event, index) {
    const action = onListKeydown(event, {
      selection,
      items: shown.value,
      index,
      focus: focusCard,
      columns: columnsOf(event.currentTarget.parentElement),
      locked: Boolean(busy.value),
      canDelete: selection.count > 0,
      onDelete: askDeleteSelected,
    });

    if (
      SELECTING.includes(action?.type) &&
      (action.type !== 'toggle' || shown.value[index]?.canDelete)
    ) {
      announceSelection();
    }
  }

  // Delete selected asks once for the batch (WCAG 3.3.4). For one icon
  // alone, it asks as that icon's own Delete asks.
  function askDeleteSelected() {
    const icons = selection.selected;

    if (!icons.length || busy.value) {
      return;
    }

    if (icons.length === 1) {
      deleting.value = { icon: icons[0], index: shown.value.indexOf(icons[0]) };
    } else {
      deletingSelected.value = icons;
    }
  }

  // Deletes each selected icon, then reads the library once. A session
  // that ends, or a server out of reach, stops the run, and the icons not
  // tried are listed as not attempted.
  async function confirmDeleteSelected() {
    const icons = deletingSelected.value;

    deletingSelected.value = null;

    if (!icons?.length || busy.value) {
      return;
    }

    status.clear();
    error.clear();
    outcome.value = null;
    busy.value = 'bulk';
    running.value = { label: 'Deleting', done: 0, total: icons.length };

    try {
      const results = await runBulk(
        icons,
        (icon) => library.remove(icon.name, { refresh: false }),
        {
          onProgress: (ended) => {
            running.value = { ...running.value, done: ended };
          },
          stop: (caught) => Boolean(library.ends?.(caught)),
        },
      );

      await library.load().catch(() => {});

      if (closed) {
        return;
      }

      const { failures } = bulkOutcome(results, (caught) =>
        library.failure(caught),
      );

      if (failures.length) {
        outcome.value = {
          heading: bulkFailureHeading(
            failures.length,
            icons.length,
            'icon',
            'icons',
            'deleted',
          ),
          items: failures.map(({ item, reason }) => ({
            key: item.name,
            name: item.name,
            reason,
          })),
        };
        await nextTick();
        summary?.focus();
      } else {
        status.set(`Deleted ${count(icons.length, 'icon')} from the server.`);
        await keepFocus(0);
      }
    } finally {
      busy.value = '';
      running.value = null;
    }
  }

  // Dismiss removes the summary, so focus moves to Select all, or to the
  // first card.
  async function dismissOutcome() {
    outcome.value = null;
    await nextTick();

    const all = rootEl.value?.querySelector('[data-testid="bulk-all-icons"]');

    if (all && !all.disabled) {
      all.focus();
    } else {
      await keepFocus(0);
    }
  }

  onMounted(() => {
    library.ensure?.().catch(() => {});
  });

  onBeforeUnmount(() => {
    closed = true;
  });

  defineExpose({ showIcon });
</script>

<style scoped>
  .builder-icons-tab__note {
    margin: 0 0 0.75rem;
    color: var(--bx-text-muted);
  }

  .builder-icons-tab__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    margin-bottom: 0.75rem;
  }

  .builder-icons-tab__failed {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.75rem;
  }

  .builder-icons-tab__failed p {
    margin: 0;
    color: var(--bx-danger);
  }

  .builder-icons-tab__show {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
  }

  .builder-icons-tab__show label {
    margin: 0;
  }

  .builder-icons-tab__show select {
    width: auto;
    max-width: 100%;
  }

  .builder-icons-tab__filter {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    max-width: 24rem;
    margin-bottom: 0.75rem;
  }

  .builder-icons-tab__filter .builder-icons-tab__note {
    margin: 0;
  }

  .builder-card h2 {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-width: 0;
    margin: 0 0 0.25rem;
    font-weight: 700;
  }

  .builder-card h2 > :first-child {
    flex: none;
  }

  .builder-card p.builder-card__meta {
    margin: 0;
  }
</style>
