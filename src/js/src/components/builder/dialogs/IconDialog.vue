<!-- Custom icons: the server's icon library and the diagram's icon copies. Use
     gives the Icon field the chosen name (the `use` event). -->
<template>
  <builder-dialog
    title="Custom icons"
    title-id="icon-dialog-title"
    class="builder-icons"
    data-testid="icon-dialog"
    @close="$emit('close')">
    <p class="builder-hint builder-icons__intro">
      An icon is a small image that every user of this server can use. A node
      draws it at its icon size: 16, 24 or 32 pixels. PNG, JPEG, GIF, WebP and
      SVG files are converted to a PNG of at most 96 by 96 pixels.
    </p>

    <!-- Upload icon…, its file chooser and the form that names the new
         icon (see IconUpload.vue). -->
    <icon-upload
      ref="uploader"
      v-model:busy="busy"
      :library="library"
      :rasterize="rasterize"
      :status="status"
      :error="error"
      @uploaded="onUploaded" />

    <p class="builder-dialog__message" role="status">
      <span v-if="status.text" :key="status.key" data-testid="icon-status">{{
        status.text
      }}</span>
    </p>
    <p
      id="icon-dialog-error"
      class="builder-dialog__message builder-dialog__error"
      role="alert">
      <span v-if="error.text" :key="error.key" data-testid="icon-error">{{
        error.text
      }}</span>
    </p>

    <template v-if="diagram.length">
      <h3 class="builder-icons__heading">
        In this diagram ({{ diagram.length }})
      </h3>
      <p class="builder-hint">
        The diagram carries these copies from the file it was uploaded from.
      </p>
      <builder-bulk-bar
        v-if="copies.total"
        id="icons-diagram"
        label="In this diagram"
        :count="copies.count"
        :total="copies.total"
        :state="copies.state"
        :running="running"
        :disabled="Boolean(busy)"
        @toggle-all="selectAll">
        <builder-busy-button
          data-testid="bulk-add-icons-diagram"
          aria-describedby="bulk-count-icons-diagram"
          :busy="busy === 'bulk'"
          :disabled="
            !addable.length || library.state.status !== 'ready' || Boolean(busy)
          "
          @click="addSelected">
          Add selected to server
        </builder-busy-button>
      </builder-bulk-bar>
      <builder-bulk-summary
        v-if="outcome"
        :ref="(element) => (summary = element)"
        :heading="outcome.heading"
        :items="outcome.items"
        :dismissible="false" />
      <ul class="builder-icons__list" data-testid="icon-diagram-list">
        <li
          v-for="(icon, index) in diagram"
          :key="icon.name"
          class="builder-icons__row"
          :class="{ 'is-selected': copies.has(icon) }"
          :data-icon="icon.name"
          :tabindex="copies.tabStop(icon) ? 0 : -1"
          :aria-labelledby="rowLabelledBy(icon, index)"
          @focusin="copies.focused(icon)"
          @keydown="onRowKeydown($event, index)">
          <label v-if="rowSelectable(icon)" class="builder-card__select">
            <input
              type="checkbox"
              :checked="copies.has(icon)"
              :disabled="Boolean(busy)"
              data-testid="icon-select"
              @pointerdown="shift.pointerdown"
              @keydown="shift.keydown"
              @click="shift.click"
              @change="select(icon, $event)" />
            <span class="builder-visually-hidden">Select {{ icon.name }}</span>
          </label>
          <builder-icon name="image" :src="srcOf(icon)" :size="32" />
          <span class="builder-icons__text">
            <span
              :id="`icon-name-diagram-${index}`"
              class="builder-icons__name"
              >{{ icon.name }}</span
            >
            <span class="builder-hint">{{ iconSizeText(icon) }}</span>
          </span>
          <span
            v-if="rowSelectable(icon)"
            :id="`icon-state-diagram-${index}`"
            hidden
            >{{ copies.has(icon) ? 'selected' : 'not selected' }}</span
          >
          <span class="builder-icons__buttons">
            <builder-busy-button
              icon="check"
              data-testid="icon-use"
              :aria-label="`Use ${icon.name}`"
              @click="use(icon)">
              Use
            </builder-busy-button>
            <span
              v-if="onServer(icon)"
              class="builder-hint"
              data-testid="icon-on-server">
              On the server
            </span>
            <builder-busy-button
              v-else-if="library.state.status === 'ready'"
              data-testid="icon-add-to-server"
              :aria-label="`Add ${icon.name} to the server`"
              :busy="busy === 'add' && busyIcon === icon.name"
              :disabled="Boolean(busy)"
              @click="addToServer(icon)">
              Add to server
            </builder-busy-button>
          </span>
        </li>
      </ul>
    </template>

    <h3 class="builder-icons__heading" data-testid="icon-library-heading">
      Server icons ({{ library.state.icons.length }})
    </h3>
    <p v-if="usage" class="builder-hint" data-testid="icon-usage">
      {{ usage }}
    </p>

    <form
      v-if="renaming"
      class="builder-icons__form"
      data-testid="icon-rename-form"
      aria-labelledby="icon-rename-title"
      @submit.prevent="submitRename">
      <h3 id="icon-rename-title" class="builder-icons__heading">
        Rename {{ renaming.name }}
      </h3>
      <span class="builder-icons__field">
        <label for="icon-rename-name">New name</label>
        <input
          id="icon-rename-name"
          ref="renameName"
          v-model="renameText"
          class="builder-input"
          type="text"
          required
          maxlength="64"
          autocomplete="off"
          spellcheck="false"
          :aria-invalid="renameInvalid ? 'true' : undefined"
          :aria-describedby="
            renameInvalid
              ? 'icon-rename-hint icon-dialog-error'
              : 'icon-rename-hint'
          "
          data-testid="icon-rename-name"
          @input.stop
          @change.stop />
        <span id="icon-rename-hint" class="builder-hint">
          The old name, {{ renaming.name }}, keeps working as another name of
          this icon, so diagrams and templates that use it keep showing it.
          {{ ICON_NAME_HINT }}
        </span>
      </span>
      <div class="builder-icons__buttons">
        <builder-busy-button
          type="submit"
          class="builder-button--primary"
          data-testid="icon-rename-submit"
          :busy="busy === 'rename'"
          :disabled="Boolean(busy)">
          Rename
        </builder-busy-button>
        <button
          type="button"
          class="builder-button"
          data-testid="icon-rename-cancel"
          @click="cancelRename">
          Cancel
        </button>
      </div>
    </form>

    <p
      v-if="library.state.status === 'loading' && !library.state.icons.length"
      class="builder-hint">
      Loading the server's icons…
    </p>
    <div
      v-else-if="
        library.state.status === 'failed' && !library.state.icons.length
      "
      class="builder-icons__failed">
      <p class="builder-dialog__error" role="alert">
        {{ library.state.error }}
      </p>
      <button
        type="button"
        class="builder-button"
        data-testid="icon-retry"
        @click="load">
        Retry
      </button>
    </div>
    <p v-else-if="!library.state.icons.length" class="builder-hint">
      The server has no icons yet.
    </p>
    <template v-else>
      <div class="builder-icons__filter">
        <label for="icon-filter">Filter icons</label>
        <input
          id="icon-filter"
          v-model="filterText"
          class="builder-input"
          type="search"
          autocomplete="off"
          spellcheck="false"
          aria-describedby="icon-filter-count"
          data-testid="icon-filter"
          @input.stop
          @change.stop />
        <span id="icon-filter-count" class="builder-hint">
          {{ filterCount }}
        </span>
      </div>
      <ul class="builder-icons__list" data-testid="icon-library-list">
        <li
          v-for="(icon, index) in shown"
          :key="icon.name"
          class="builder-icons__row"
          :data-icon="icon.name"
          :tabindex="libraryTabStop(icon) ? 0 : -1"
          :aria-labelledby="`icon-name-library-${index}`"
          @focusin="libraryStop = icon.name"
          @keydown="onLibraryKeydown($event, index)">
          <builder-icon name="image" :src="srcOf(icon)" :size="32" />
          <span class="builder-icons__text">
            <span
              :id="`icon-name-library-${index}`"
              class="builder-icons__name"
              >{{ icon.name }}</span
            >
            <span class="builder-hint">{{ detailsOf(icon) }}</span>
          </span>
          <span class="builder-icons__buttons">
            <builder-busy-button
              icon="check"
              data-testid="icon-use"
              :aria-label="`Use ${icon.name}`"
              @click="use(icon)">
              Use
            </builder-busy-button>
            <!-- Rename is an uploaded icon's Edit: blue, with the pencil. -->
            <builder-action-button
              v-if="icon.canRename"
              action="edit"
              data-testid="icon-rename"
              :aria-label="`Rename ${icon.name}`"
              :disabled="Boolean(busy)"
              @click="askRename(icon, $event)">
              Rename
            </builder-action-button>
          </span>
        </li>
      </ul>
    </template>

    <div class="builder-dialog__actions">
      <button
        type="button"
        class="builder-button"
        data-testid="icon-close"
        @click="$emit('close')">
        Close
      </button>
    </div>
  </builder-dialog>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    reactive,
    ref,
    watch,
  } from 'vue';

  import BuilderActionButton from '../BuilderActionButton.vue';
  import BuilderBulkBar from '../BuilderBulkBar.vue';
  import BuilderBulkSummary from '../BuilderBulkSummary.vue';
  import BuilderBusyButton from '../BuilderBusyButton.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import IconUpload from '../IconUpload.vue';
  import {
    useInspectorIconLibrary,
    useInspectorIcons,
  } from '../inspector/control.js';
  import { useMessage } from './message.js';

  import { count, describeNames } from '@/builder/announce.js';
  import { bulkFailureHeading, bulkOutcome, runBulk } from '@/builder/bulk.js';
  import {
    ICON_NAME_HINT,
    holdsImage,
    iconOwnerText,
    iconSizeText,
    iconSrc,
    isIconName,
    rasterizeIcon,
    sortIcons,
    usageText,
  } from '@/builder/icons.js';
  import {
    isTyping,
    listKeyAction,
    onListKeydown,
    shiftPress,
    useListSelection,
  } from '@/builder/listSelection.js';

  defineProps({
    // Makes an icon of a chosen file (see rasterizeIcon).
    rasterize: { type: Function, default: rasterizeIcon },
  });

  const emit = defineEmits(['close', 'use']);

  // What a refused name says beside the name rule.
  const BAD_NAME = 'Enter a name for the icon.';

  const icons = useInspectorIcons();
  // The server's icon library (see iconLibrary.js).
  const library = useInspectorIconLibrary();
  const status = useMessage();
  const error = useMessage();

  // Upload icon… (see IconUpload.vue).
  const uploader = ref(null);
  const renameName = ref(null);
  // The server icon being renamed, the new name typed, and the button that
  // opened the form, which takes focus back when it closes.
  const renaming = ref(null);
  const renameText = ref('');
  // The dialog's error is about the new name, so the field is invalid.
  const renameInvalid = computed(
    () => Boolean(renaming.value) && error.field === 'rename',
  );
  let renameOpener = null;
  const filterText = ref('');
  // What is in progress: 'convert' or 'upload' (IconUpload's), 'add',
  // 'rename', 'bulk' or ''. A busy button keeps focus and ignores a press.
  const busy = ref('');
  // The icon that 'add' works on, so only its button turns.
  const busyIcon = ref('');
  // Set when the dialog closes. An answer that comes later changes
  // nothing.
  let closed = false;

  // The copies the diagram carries, by name.
  const diagram = computed(() => sortIcons(icons.diagram()));

  const usage = computed(() => usageText(library.state));

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

  const filterCount = computed(() =>
    filterText.value.trim()
      ? `${shown.value.length} of ${library.state.icons.length} icons shown.`
      : '',
  );

  // A copy in the diagram carries its image. A server icon is drawn from
  // the library, which reads its image when it is first drawn.
  function srcOf(icon) {
    if (typeof icon?.data !== 'string' && isIconName(icon?.name)) {
      return iconSrc(icon.name, null, library);
    }

    const key = isIconName(icon?.name) ? icon.name : 'new-icon';

    return iconSrc(key, { [key]: icon });
  }

  function detailsOf(icon) {
    const parts = [
      iconOwnerText(icon),
      iconSizeText(icon),
      icon.aliases?.length ? `also named ${icon.aliases.join(', ')}` : '',
    ];

    return parts.filter(Boolean).join(' · ');
  }

  // Whether the server has a copy as it is, under its name or an alias.
  function onServer(icon) {
    return holdsImage(library.lookup(icon.name), icon.data);
  }

  async function load() {
    try {
      await library.load();
    } catch {
      // The library's state says why.
    }
  }

  // Focuses a row's button once the list shows the row.
  async function focusRow(list, name, testid) {
    await nextTick();
    document
      .querySelector(
        `[data-testid="${list}"] [data-icon="${CSS.escape(name)}"] [data-testid="${testid}"]`,
      )
      ?.focus();
  }

  // Why a typed name cannot be sent, or ''.
  function nameProblem(name) {
    return isIconName(name) ? '' : `${BAD_NAME} ${ICON_NAME_HINT}`;
  }

  // After an upload, focus moves to the new icon's Use.
  async function onUploaded({ icon }) {
    if (!closed) {
      await focusRow('icon-library-list', icon.name, 'icon-use');
    }
  }

  async function addToServer(icon) {
    if (busy.value) {
      return;
    }

    status.clear();
    error.clear();
    busy.value = 'add';
    busyIcon.value = icon.name;

    try {
      const answer = await library.upload({ name: icon.name, data: icon.data });

      if (closed) {
        return;
      }

      status.set(
        answer.created
          ? `Added ${answer.icon.name} to the server. The diagram drops its copy with its next edit.`
          : `The server already has this icon as ${answer.icon.name}.`,
      );
      // Add to server is gone after the upload, so focus moves to Use.
      await focusRow('icon-diagram-list', icon.name, 'icon-use');
    } catch (caught) {
      if (!closed) {
        error.set(library.failure(caught));
      }
    } finally {
      busy.value = '';
      busyIcon.value = '';
    }
  }

  async function askRename(icon, event) {
    if (busy.value) {
      return;
    }

    status.clear();
    error.clear();
    renameOpener = event?.currentTarget || null;
    renaming.value = icon;
    renameText.value = icon.name;
    await nextTick();
    renameName.value?.focus();
    renameName.value?.select();
  }

  async function cancelRename() {
    const opener = renameOpener;

    renaming.value = null;
    renameOpener = null;
    await nextTick();
    opener?.focus();
  }

  async function submitRename() {
    const icon = renaming.value;
    const name = renameText.value.trim();

    if (busy.value || !icon) {
      return;
    }

    status.clear();
    error.clear();

    const problem = nameProblem(name);

    if (problem) {
      error.set(problem, 'rename');
      renameName.value?.focus();

      return;
    }

    if (name === icon.name) {
      await cancelRename();

      return;
    }

    busy.value = 'rename';

    try {
      const renamed = await library.rename(icon.name, name);

      if (closed) {
        return;
      }

      renaming.value = null;
      renameOpener = null;
      filterText.value = '';
      status.set(
        `Renamed ${icon.name} to ${renamed?.name || name}. ${icon.name} keeps working as another name of it.`,
      );
      await focusRow('icon-library-list', renamed?.name || name, 'icon-rename');
    } catch (caught) {
      if (!closed) {
        error.set(library.failure(caught), 'rename');
        renameName.value?.focus();
      }
    } finally {
      busy.value = '';
    }
  }

  function use(icon) {
    emit('use', { name: icon.name });
  }

  // --- selecting several icons ---------------------------------------------

  // Whether the library was read once. A later read in progress (after
  // each change) keeps what the last read listed.
  const libraryRead = ref(library.state.status === 'ready');

  watch(
    () => library.state.status,
    (status) => {
      if (status === 'ready') {
        libraryRead.value = true;
      }
    },
  );

  // The diagram's copies, by name. Those the server lacks can be selected.
  const copies = reactive(
    useListSelection(diagram, (icon) => icon.name, {
      selectable: (icon) => rowSelectable(icon),
    }),
  );
  // The server icon that is its list's Tab stop, by name.
  const libraryStop = ref(null);

  // The batch in progress, { label, done, total }, and what the last batch
  // left undone, { heading, items }. Both show at the copies' row.
  const running = ref(null);
  const outcome = ref(null);
  let summary = null;

  // The copies Add selected to server sends.
  const addable = computed(() =>
    copies.selected.filter((icon) => !onServer(icon)),
  );

  // The dialog's status says every change of the selection, because the
  // page's live region waits while a dialog is open.
  function announceSelection() {
    status.set(`${copies.count} of ${copies.total} selected.`);
  }

  // Whether Shift was held as a row's checkbox was pressed.
  const shift = shiftPress();

  // A row's checkbox. With Shift, every row in the range from the row last
  // pressed takes the box's new state.
  function select(icon, event) {
    copies.press(icon, {
      checked: event.target.checked,
      range: shift.take(),
    });
    event.target.checked = copies.has(icon);
    announceSelection();
  }

  function selectAll() {
    copies.toggleAll();
    announceSelection();
  }

  // Whether a row's icon can be selected: a copy the server lacks, once the
  // library has been read (until then no one knows).
  function rowSelectable(icon) {
    return libraryRead.value && !onServer(icon);
  }

  // A copy's row name: its icon's name and, for a row that can be selected,
  // whether it is selected.
  function rowLabelledBy(icon, index) {
    const name = `icon-name-diagram-${index}`;

    return rowSelectable(icon) ? `${name} icon-state-diagram-${index}` : name;
  }

  // Focuses the row at a position of a list. focusin makes it the Tab stop.
  function focusListRow(testid, index) {
    document
      .querySelector(`[data-testid="${testid}"]`)
      ?.children[index]?.focus();
  }

  function libraryTabStop(icon) {
    const items = shown.value;
    const listed = items.some((entry) => entry.name === libraryStop.value);

    return listed ? icon.name === libraryStop.value : items[0] === icon;
  }

  // What the keys change: each is said, as a press of a checkbox is.
  const SELECTING = ['extend', 'toggle', 'range', 'all', 'clear'];

  // The keys of the copies (onListKeydown in listSelection.js). While a
  // change is in progress, the keys only move focus.
  function onRowKeydown(event, index) {
    const action = onListKeydown(event, {
      selection: copies,
      items: diagram.value,
      index,
      focus: (to) => focusListRow('icon-diagram-list', to),
      locked: Boolean(busy.value),
    });

    if (
      SELECTING.includes(action?.type) &&
      (action.type !== 'toggle' || rowSelectable(diagram.value[index]))
    ) {
      announceSelection();
    }
  }

  // In the server icons, the keys only move focus.
  function onLibraryKeydown(event, index) {
    if (event.defaultPrevented || event.isComposing || isTyping(event.target)) {
      return;
    }

    const action = listKeyAction(event, { index, count: shown.value.length });

    if (action?.type === 'focus') {
      event.preventDefault();
      focusListRow('icon-library-list', action.index);
    }
  }

  // After a batch, focus on a control that is gone moves to Upload icon….
  async function keepFocus() {
    await nextTick();

    const active = document.activeElement;

    if (!active || active === document.body || !active.isConnected) {
      uploader.value?.focus();
    }
  }

  // Runs `work` for each icon. An ended session or a lost server stops the run,
  // and a summary that takes focus lists what it left undone.
  async function runOnIcons(
    icons,
    { label, done, message, note = () => '', work },
  ) {
    status.clear();
    error.clear();
    outcome.value = null;
    busy.value = 'bulk';
    running.value = { label, done: 0, total: icons.length };

    try {
      const results = await runBulk(icons, work, {
        onProgress: (ended) => {
          running.value = { ...running.value, done: ended };
        },
        stop: (caught) => Boolean(library.ends?.(caught)),
      });

      if (closed) {
        return;
      }

      const { failures } = bulkOutcome(results, (caught) =>
        library.failure(caught),
      );

      if (failures.length) {
        const said = note(results);

        if (said) {
          status.set(said);
        }

        outcome.value = {
          heading: bulkFailureHeading(
            failures.length,
            icons.length,
            'icon',
            'icons',
            done,
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
        status.set(message(results));
        await keepFocus();
      }
    } finally {
      busy.value = '';
      running.value = null;
    }
  }

  // The copies the server answered it already had (added since the library
  // was read, or under a name another of its names matches), as Add to
  // server says of one: "The server already has plc.", "… has plc as
  // pump.", or ''.
  function keptText(results) {
    const kept = results
      .filter((result) => result.ok && !result.value?.created)
      .map(({ item, value }) => {
        const name = value?.icon?.name || item.name;

        return name === item.name ? name : `${item.name} as ${name}`;
      });

    return kept.length ? `The server already has ${describeNames(kept)}.` : '';
  }

  // What Add selected to server says once every copy was sent: how many
  // icons the server added (the diagram then drops those copies), and the
  // copies it already had.
  function addedText(results) {
    const added = results.filter(
      (result) => result.ok && result.value?.created,
    ).length;
    const parts = added
      ? [
          `Added ${count(added, 'icon')} to the server. The diagram drops its ${added === 1 ? 'copy' : 'copies'} with its next edit.`,
        ]
      : [];

    return [...parts, keptText(results)].filter(Boolean).join(' ');
  }

  // Add selected to server: each selected copy the server lacks, under its
  // name, as Add to server adds one.
  function addSelected() {
    const icons = addable.value;

    if (!icons.length || busy.value || library.state.status !== 'ready') {
      return;
    }

    runOnIcons(icons, {
      label: 'Adding',
      done: 'added to the server',
      message: addedText,
      note: keptText,
      work: (icon) => library.upload({ name: icon.name, data: icon.data }),
    });
  }

  onMounted(load);

  onBeforeUnmount(() => {
    closed = true;
  });
</script>

<style scoped>
  .builder-icons {
    width: min(38rem, 92vw);
  }

  .builder-icons .builder-icons__intro {
    margin: 0 0 0.6rem;
  }

  .builder-icons__heading {
    margin: 0.9rem 0 0.4rem;
    font-size: 0.95rem;
    font-weight: 700;
  }

  .builder-icons__list {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  /* A row wraps in a narrow window: the buttons go under the name. */
  .builder-icons__row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem 0.6rem;
    padding: 0.35rem 0;
    border-top: 1px solid var(--bx-border);
  }

  /* A selected row has a bar in the accent color along its start. The
     checked box is what says so. The bar only makes it easy to find. */
  .builder-icons__row.is-selected {
    box-shadow: inset 3px 0 0 var(--bx-accent);
  }

  .builder-icons__text,
  .builder-icons__field {
    display: flex;
    flex: 1 1 10rem;
    flex-direction: column;
    gap: 0.2rem;
    min-width: 0;
  }

  .builder-icons__name {
    overflow-wrap: anywhere;
  }

  .builder-icons__buttons {
    display: flex;
    flex: none;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
  }

  .builder-icons__form {
    margin: 0.6rem 0;
    padding: 0.6rem;
    border: 1px solid var(--bx-border);
    border-radius: 6px;
  }

  .builder-icons__form .builder-icons__buttons {
    margin-top: 0.5rem;
  }

  .builder-icons__filter {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    margin-bottom: 0.4rem;
  }

  .builder-icons__failed {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
  }

  .builder-icons__failed p {
    margin: 0;
  }

  .builder-icons .builder-dialog__actions {
    margin-top: 0.9rem;
  }
</style>
