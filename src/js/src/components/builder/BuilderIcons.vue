<!-- The drafts page's Icons tab: the built-in icons and the server's icon
     library, one for every Builder user (iconLibrary.js). Only this tab deletes
     icons. The status and error regions render empty from the start. -->
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
      You cannot delete icons from the server's template files.
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
      <p role="alert">
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
    <template
      v-else-if="
        showing === 'server' &&
        library.state.status === 'ready' &&
        !library.state.icons.length
      ">
      <p data-testid="icons-tab-empty">The server has no icons yet.</p>
      <!-- A run that took the last icons off the list still says what failed. -->
      <builder-bulk-summary
        v-if="outcome"
        :ref="(element) => (summary = element)"
        :heading="outcome.heading"
        :items="outcome.items"
        @dismiss="dismissOutcome" />
    </template>
    <template v-else-if="showing === 'builtin' || library.state.icons.length">
      <p
        v-if="showing === 'builtin'"
        class="builder-icons-tab__note"
        data-testid="icons-tab-builtin-note">
        These icons are part of phenix. You cannot delete or rename them.
        Download saves an icon as a PNG file of {{ BUILTIN_ICON_FILE_SIZE }} by
        {{ BUILTIN_ICON_FILE_SIZE }} pixels.
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
        <builder-action-button
          action="download"
          data-testid="bulk-download-icons"
          aria-describedby="bulk-count-icons"
          :busy="running?.label === 'Downloading'"
          :disabled="!selection.count || Boolean(busy)"
          @click="downloadSelected">
          Download selected
        </builder-action-button>
        <builder-action-button
          action="delete"
          data-testid="bulk-delete-icons"
          :aria-haspopup="showing === 'server' ? 'dialog' : undefined"
          aria-describedby="bulk-count-icons"
          :busy="running?.label === 'Deleting'"
          :disabled="!selection.count || Boolean(busy)"
          @click="askDeleteSelected">
          Delete selected
        </builder-action-button>
      </builder-bulk-bar>
      <builder-bulk-summary
        v-if="outcome"
        :ref="(element) => (summary = element)"
        :heading="outcome.heading"
        :items="outcome.items"
        @dismiss="dismissOutcome" />

      <p v-if="!listed.length" data-testid="icons-tab-none-shown">
        No icon matches the filter.
      </p>
      <ul
        v-else-if="showing === 'builtin'"
        ref="listEl"
        class="builder-cards"
        :aria-busy="Boolean(running) || undefined"
        data-testid="builtin-icons-list">
        <!-- A card is focusable, one at a time (the list's Tab stop), and
             named by its label and whether it is selected. -->
        <li
          v-for="(icon, index) in builtinShown"
          :key="icon.key"
          class="builder-card builder-panel"
          :class="{ 'is-selected': selection.has(icon) }"
          :tabindex="selection.tabStop(icon) ? 0 : -1"
          :aria-labelledby="`builtin-icon-name-${index} builtin-icon-state-${index}`"
          :data-testid="`builtin-icon-card-${icon.key}`"
          @focusin="selection.focused(icon)"
          @keydown="onCardKeydown($event, index)">
          <div class="builder-card__head">
            <h2 :id="`builtin-icon-name-${index}`">
              <builder-icon :name="icon.key" :size="32" />
              <span>{{ icon.label }}</span>
            </h2>
            <label class="builder-card__select">
              <input
                type="checkbox"
                :checked="selection.has(icon)"
                :disabled="Boolean(busy)"
                :data-testid="`builtin-icon-select-${icon.key}`"
                @pointerdown="shift.pointerdown"
                @keydown="shift.keydown"
                @click="shift.click"
                @change="select(icon, $event)" />
              <span class="builder-visually-hidden">
                Select {{ icon.label }}
              </span>
            </label>
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
            <builder-action-button
              action="view"
              :data-testid="`builtin-icon-preview-${icon.key}`"
              :aria-label="`View ${icon.label}`"
              aria-haspopup="dialog"
              @click="preview(icon)" />
            <builder-action-button
              action="download"
              :data-testid="`builtin-icon-download-${icon.key}`"
              :aria-label="`Download ${icon.label}`"
              @click="download(icon)" />
          </div>
          <!-- Whether the card is selected, for its name only. -->
          <span :id="`builtin-icon-state-${index}`" hidden>{{
            selection.has(icon) ? 'selected' : 'not selected'
          }}</span>
        </li>
      </ul>
      <ul
        v-else
        ref="listEl"
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
          :aria-labelledby="`icon-card-name-${index} icon-card-state-${index}`"
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
            <label class="builder-card__select">
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
            <builder-action-button
              action="view"
              :data-testid="`icon-preview-${icon.name}`"
              :aria-label="`View ${icon.name}`"
              aria-haspopup="dialog"
              @click="preview(icon)" />
            <builder-action-button
              action="download"
              :data-testid="`icon-download-${icon.name}`"
              :aria-label="`Download ${icon.name}`"
              @click="download(icon)" />
            <builder-action-button
              v-if="icon.canDelete"
              action="delete"
              :data-testid="`icon-delete-${icon.name}`"
              :aria-label="`Delete ${icon.name} from the server`"
              aria-haspopup="dialog"
              :busy="busy === 'delete' && busyIcon === icon.name"
              :disabled="Boolean(busy)"
              @click="askDelete(icon, index)" />
          </div>
          <!-- Whether the card is selected, for its name only. -->
          <span :id="`icon-card-state-${index}`" hidden>{{
            selection.has(icon) ? 'selected' : 'not selected'
          }}</span>
        </li>
      </ul>
    </template>

    <icon-preview-dialog
      v-if="previewing"
      :icon="previewed"
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
      action="delete"
      @cancel="deleting = null"
      @confirm="confirmDelete" />
    <!-- Delete selected: one question for the icons it can delete. -->
    <builder-confirm
      v-if="deletingSelected"
      id="icons-delete"
      :title="`Delete ${count(deletingSelected.icons.length, 'icon')}?`"
      :message="selectedQuestion"
      :confirm-label="`Delete ${count(deletingSelected.icons.length, 'icon')}`"
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
    onBeforeUnmount,
    onMounted,
    reactive,
    ref,
    watch,
  } from 'vue';

  import BuilderActionButton from './BuilderActionButton.vue';
  import BuilderBulkBar from './BuilderBulkBar.vue';
  import BuilderBulkSummary from './BuilderBulkSummary.vue';
  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import IconUpload from './IconUpload.vue';
  import IconPreviewDialog from './dialogs/IconPreviewDialog.vue';
  import { useMessage } from './dialogs/message.js';

  import { count } from '@/builder/announce.js';
  import {
    BUILTIN_ICON_FILE_SIZE,
    BUILTIN_ICON_OWNER,
    builtinIconMatches,
    builtinIcons,
    saveBuiltinIconFile,
  } from '@/builder/builtinIcons.js';
  import {
    BulkError,
    bulkDoneMessage,
    bulkFailureHeading,
    bulkOutcome,
    iconsDeleteMessage,
    runBulk,
  } from '@/builder/bulk.js';
  import { focusLost } from '@/builder/commands.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { iconPath } from '@/builder/iconPaths.js';
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
    // Makes a PNG icon of an image file: of a chosen file, and of a built-in
    // icon's SVG for Download (see rasterizeIcon).
    rasterize: { type: Function, default: rasterizeIcon },
  });

  // lost-focus: focus would fall to <body>, so the view focuses the tab.
  const emit = defineEmits(['lost-focus']);

  const library = props.library;
  const status = useMessage();
  const error = useMessage();

  const rootEl = ref(null);
  const listEl = ref(null);
  const uploader = ref(null);
  const filterText = ref('');
  // What is in progress: 'convert' or 'upload' (IconUpload's), 'delete' or
  // 'bulk', or ''. A busy button keeps focus, and a press is then ignored.
  const busy = ref('');
  // The icon that 'delete' works on, so only its button turns.
  const busyIcon = ref('');
  // The icon the preview shows, or null.
  const previewing = ref(null);
  // The icon Delete asks about, and the place of its card, or null.
  const deleting = ref(null);
  // What Delete selected asks about: { icons, skipped }, the selected icons
  // it deletes and those it cannot, or null.
  const deletingSelected = ref(null);
  // The batch in progress, { label, done, total }, and what the last batch
  // left undone, { heading, items }.
  const running = ref(null);
  const outcome = ref(null);
  let summary = null;
  // Set when the tab goes. An answer that comes later changes nothing.
  let closed = false;

  // Which list Show shows: 'builtin' (the default) or 'server'.
  const showing = ref('builtin');
  const BUILTIN_ICONS = builtinIcons();

  // The count includes the built-in icons, as the tab's count does.
  const usage = computed(() => usageText(library.state, BUILTIN_ICONS.length));

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

  const builtinShown = computed(() =>
    BUILTIN_ICONS.filter((icon) => builtinIconMatches(icon, filterText.value)),
  );

  // The cards Show shows now.
  const listed = computed(() =>
    showing.value === 'builtin' ? builtinShown.value : shown.value,
  );

  const filterCount = computed(() => {
    if (!filterText.value.trim()) {
      return '';
    }

    const all =
      showing.value === 'builtin' ? BUILTIN_ICONS : library.state.icons;

    return `${listed.value.length} of ${all.length} icons shown.`;
  });

  // What the tab calls an icon: a built-in icon's label, else its name.
  function nameOf(icon) {
    return icon?.builtin ? icon.label : icon?.name;
  }

  // A key for an icon of either list. A server icon can have a built-in
  // icon's key as its name.
  function keyOf(icon) {
    return icon?.builtin ? `builtin:${icon.key}` : `server:${icon?.name}`;
  }

  // A server icon is drawn from the library, which reads its image when it
  // is first drawn.
  function srcOf(icon) {
    return isIconName(icon?.name) ? iconSrc(icon.name, null, library) : '';
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

  // The icon the preview shows: a server icon as the library looks it up,
  // with its image once read.
  const previewed = computed(() => {
    const icon = previewing.value;

    return icon && !icon.builtin ? library.lookup(icon.name) || icon : icon;
  });

  // Why an icon was not saved, as the summary of a batch lists it.
  const NOT_DRAWN = 'The Builder could not draw it as a PNG file.';
  const NOT_READ = 'The Builder could not read its image.';
  const NOT_PNG = 'Its data is not a PNG icon.';

  // Saves an icon as a PNG file. A built-in icon is drawn at
  // BUILTIN_ICON_FILE_SIZE. Returns the file name or throws a BulkError.
  async function saveIcon(icon) {
    if (icon?.builtin) {
      const fileName = await saveBuiltinIconFile(icon, iconPath(icon.key), {
        saveAs,
        rasterize: props.rasterize,
      }).catch(() => '');

      if (!fileName) {
        throw new BulkError(NOT_DRAWN);
      }

      return fileName;
    }

    // The list has no images: the file waits for this icon's.
    await library.ensureImages([icon.name]);

    const read = library.lookup(icon.name);

    if (typeof read?.data !== 'string') {
      throw new BulkError(NOT_READ);
    }

    const fileName = saveIconFile(read, { saveAs });

    if (!fileName) {
      throw new BulkError(NOT_PNG);
    }

    return fileName;
  }

  async function download(icon) {
    status.clear();
    error.clear();

    try {
      const fileName = await saveIcon(icon);

      if (!closed) {
        status.set(`Saved ${fileName}.`);
      }
    } catch (caught) {
      const reason =
        caught instanceof BulkError ? caught.reason : library.failure(caught);

      if (!closed) {
        error.set(
          `Could not save ${nameOf(icon)}: ${reason.charAt(0).toLowerCase()}${reason.slice(1)}`,
        );
      }
    }
  }

  /** Opens the preview of an icon by any of its names once the library is read. */
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

    const last = listed.value.length - 1;

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
    busyIcon.value = icon.name;

    try {
      await library.remove(icon);

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
      busyIcon.value = '';
    }
  }

  // --- selecting several icons -------------------------------------------

  // The selection of the cards Show shows. Every icon can be selected, so
  // another Show, or the filter, takes the icons it hides off the selection.
  const selection = reactive(useListSelection(listed, keyOf));

  // A summary tells of icons of one Show, so another Show removes it.
  watch(showing, () => {
    outcome.value = null;
  });

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

  // Focuses the card at a position of the list, which becomes its Tab stop.
  function focusCard(index) {
    const icon = listed.value[index];
    const card = listEl.value?.children[index];

    if (icon && card) {
      selection.focused(icon);
      card.focus();
    }
  }

  // What the keys change: each is said, as a press of a checkbox is.
  const SELECTING = ['extend', 'toggle', 'range', 'all', 'clear'];

  // The keys of the list of cards (see onListKeydown in listSelection.js).
  // While a change is in progress, the selection stays as it is.
  function onCardKeydown(event, index) {
    const action = onListKeydown(event, {
      selection,
      items: listed.value,
      index,
      focus: focusCard,
      columns: columnsOf(event.currentTarget.parentElement),
      locked: Boolean(busy.value),
      canDelete: selection.count > 0,
      onDelete: askDeleteSelected,
    });

    if (SELECTING.includes(action?.type)) {
      announceSelection();
    }
  }

  // Why Delete selected leaves a selected icon as it is.
  const BUILTIN_NOT_DELETED = 'You cannot delete a built-in icon.';
  const SERVER_NOT_DELETED =
    "You cannot delete an icon from the server's template files.";

  function notDeleted(icons) {
    return icons.map((icon) => ({
      item: icon,
      reason: icon.builtin ? BUILTIN_NOT_DELETED : SERVER_NOT_DELETED,
    }));
  }

  // The question of Delete selected: the icons it deletes, by name, and how
  // many selected icons it leaves.
  const selectedQuestion = computed(() => {
    const { icons = [], skipped = [] } = deletingSelected.value || {};
    const question = iconsDeleteMessage(icons.map((icon) => icon.name));

    return skipped.length
      ? `${question} You cannot delete ${count(skipped.length, 'other selected icon')}.`
      : question;
  });

  // Delete selected asks once for the batch (WCAG 3.3.4). For one icon
  // alone, it asks as that icon's own Delete asks. With no icon it can
  // delete, it only lists why.
  async function askDeleteSelected() {
    const selected = selection.selected;

    if (!selected.length || busy.value) {
      return;
    }

    const icons = selected.filter((icon) => icon.canDelete);
    const skipped = selected.filter((icon) => !icon.canDelete);

    if (!icons.length) {
      status.clear();
      error.clear();
      await showOutcome(notDeleted(skipped), selected.length, 'deleted');
    } else if (selected.length === 1) {
      deleting.value = {
        icon: icons[0],
        index: listed.value.indexOf(icons[0]),
      };
    } else {
      deletingSelected.value = { icons, skipped };
    }
  }

  // Lists what a batch left undone, with focus on the list.
  async function showOutcome(failures, total, done) {
    outcome.value = {
      heading: bulkFailureHeading(
        failures.length,
        total,
        'icon',
        'icons',
        done,
      ),
      items: failures.map(({ item, reason }) => ({
        key: keyOf(item),
        name: nameOf(item),
        reason,
      })),
    };
    await nextTick();
    summary?.focus();
  }

  // Runs `work` on each icon, `limit` at a time, with progress on the row.
  // A session that ends, or a server out of reach, stops the run, and the
  // icons not tried are listed as not attempted.
  async function runSelected(icons, work, { label, limit }) {
    status.clear();
    error.clear();
    outcome.value = null;
    busy.value = 'bulk';
    running.value = { label, done: 0, total: icons.length };

    try {
      const results = await runBulk(icons, work, {
        limit,
        onProgress: (ended) => {
          running.value = { ...running.value, done: ended };
        },
        stop: (caught) => Boolean(library.ends?.(caught)),
      });

      return closed
        ? null
        : bulkOutcome(results, (caught) => library.failure(caught)).failures;
    } finally {
      busy.value = '';
      running.value = null;
    }
  }

  // Deletes each icon Delete selected can delete. The library takes each
  // answer into its list. The icons it cannot delete are listed with the
  // icons that failed.
  async function confirmDeleteSelected() {
    const { icons, skipped = [] } = deletingSelected.value || {};

    deletingSelected.value = null;

    if (!icons?.length || busy.value) {
      return;
    }

    const failures = await runSelected(icons, (icon) => library.remove(icon), {
      label: 'Deleting',
    });

    if (!failures) {
      return;
    }

    const left = [...failures, ...notDeleted(skipped)];

    if (left.length) {
      await showOutcome(left, icons.length + skipped.length, 'deleted');
    } else {
      status.set(`Deleted ${count(icons.length, 'icon')} from the server.`);
      await keepFocus(0);
    }
  }

  // Download selected saves one PNG file for each selected icon, one after
  // another.
  async function downloadSelected() {
    const icons = selection.selected;

    if (!icons.length || busy.value) {
      return;
    }

    const failures = await runSelected(icons, saveIcon, {
      label: 'Downloading',
      limit: 1,
    });

    if (!failures) {
      return;
    }

    if (failures.length) {
      await showOutcome(failures, icons.length, 'downloaded');
    } else {
      status.set(bulkDoneMessage(icons.length, 'icon', 'icons', 'downloaded'));
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
