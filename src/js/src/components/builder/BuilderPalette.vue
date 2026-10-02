<!--
  Add nodes: the panel of node kinds and device templates.

  Each entry is a button first (click or Enter adds the node in a free spot of
  the canvas in view) and draggable second, so pointer-free operation is never a
  second-class path. An entry shows only its name; its description appears as
  a tooltip on hover and keyboard focus and is its accessible description.

  The device templates come in groups (see paletteTemplateGroups in
  templates.js): those saved in the diagram, under "This diagram", then
  those of the user's library. A group's name shows only when there are two
  groups or more. The "+" beside the heading opens the template editor on a
  new template of the diagram, and the button after it goes to the drafts
  page's Node Templates tab, where the library is managed. A template of
  the diagram has a menu beside its entry, to edit it, copy it into the
  library, or delete it.

  While the library is read for the first time the list says so. When it
  cannot be read, the built-in templates stand in for it, under "Built-in",
  with a note and Retry, so a diagram can still be built. They also stand
  in for a library the server cannot read, with a note alone: reading it
  again would change nothing.
-->
<template>
  <section
    class="builder-palette builder-panel"
    aria-labelledby="palette-title">
    <div class="builder-palette__header">
      <h2 id="palette-title" class="builder-panel__title">Add nodes</h2>
      <button
        type="button"
        class="builder-help-button"
        data-testid="palette-help"
        aria-label="Add nodes help"
        aria-describedby="palette-help"
        v-on="tipEvents(PALETTE_HELP)"
        @click="showTip($event, PALETTE_HELP)">
        <builder-icon name="help" :size="16" />
      </button>
    </div>
    <!-- Screen readers get the tooltips' text from these hidden
         descriptions; sighted users see it in the tooltip. -->
    <p id="palette-help" hidden>{{ PALETTE_HELP }}</p>

    <ul class="builder-palette__list">
      <li v-for="item in palette" :key="item.id">
        <span :id="`palette-hint-${item.id}`" hidden>{{ item.hint }}</span>
        <button
          type="button"
          class="builder-palette__item"
          :draggable="!store.readOnly"
          :disabled="store.readOnly"
          :data-testid="`palette-${item.kind}`"
          :aria-label="`Add ${item.label}`"
          :aria-describedby="`palette-hint-${item.id}`"
          @click="add(item)"
          @dragstart="onDragStart($event, item)"
          v-on="tipEvents(item.hint)">
          <builder-icon :name="item.iconKey" :size="18" />
          <span class="builder-palette__label">{{ item.label }}</span>
        </button>
      </li>
    </ul>

    <div class="builder-palette__header builder-palette__header--templates">
      <h3 class="builder-palette__subtitle">Device templates</h3>
      <!-- aria-disabled rather than disabled: it keeps focus, and its
           tooltip, which then says why it does nothing. -->
      <button
        ref="newButton"
        type="button"
        class="builder-palette__tool"
        data-testid="palette-new-template"
        aria-label="New device template"
        :aria-disabled="newBlocked ? 'true' : undefined"
        :aria-describedby="newBlocked ? 'palette-new-template-why' : undefined"
        v-on="tipEvents(() => newBlocked || NEW_TEMPLATE)"
        @click="newTemplate">
        <builder-icon name="plus" :size="16" />
      </button>
      <button
        ref="libraryButton"
        type="button"
        class="builder-palette__tool"
        data-testid="palette-library"
        aria-label="Node Templates library"
        v-on="tipEvents(LIBRARY)"
        @click="openLibrary">
        <builder-icon name="library" :size="16" />
      </button>
    </div>
    <p id="palette-new-template-why" hidden>{{ newBlocked }}</p>

    <template v-for="group in groups" :key="group.id">
      <h4 v-if="groups.length > 1" class="builder-palette__group">
        {{ group.label }}
      </h4>
      <ul
        class="builder-palette__list"
        :aria-label="group.label"
        :data-testid="`palette-templates-${group.id}`">
        <li
          v-for="entry in group.entries"
          :key="entry.key"
          class="builder-palette__entry">
          <span v-if="entry.description" :id="`${entry.testid}-hint`" hidden>
            {{ entry.description }}
          </span>
          <button
            type="button"
            class="builder-palette__item"
            :draggable="!store.readOnly"
            :disabled="store.readOnly"
            :data-testid="entry.testid"
            :aria-label="`Add ${deviceName(entry.name)}`"
            :aria-describedby="
              entry.description ? `${entry.testid}-hint` : undefined
            "
            @click="addTemplate(entry)"
            @dragstart="onDragStart($event, { kind: 'device', key: entry.key })"
            v-on="tipEvents(entry.description)">
            <builder-icon
              :name="entry.iconKey"
              :src="iconSrc(entry.icon, iconsOf(entry))"
              :size="18" />
            <span class="builder-palette__label">{{ entry.name }}</span>
          </button>
          <builder-menu-button
            v-if="entry.source === 'diagram'"
            class="builder-palette__actions"
            :items="templateActions"
            :disabled="store.readOnly"
            :testid="`palette-template-actions-${entry.id}`"
            :aria-label="`Actions for template ${entry.name}`"
            @select="(item) => act(item.id, entry)" />
        </li>
      </ul>
    </template>

    <!-- Rendered from the start, so what it comes to say is read. -->
    <p
      id="palette-library-note"
      class="builder-palette__note"
      role="status"
      data-testid="palette-library-note">
      {{ libraryNote }}
    </p>
    <!-- While the read it asked for is under way it keeps focus, and a
         second press does nothing. -->
    <button
      v-if="store.templates.error"
      type="button"
      class="builder-button builder-palette__retry"
      data-testid="palette-library-retry"
      aria-describedby="palette-library-note"
      :aria-disabled="libraryLoading || undefined"
      :aria-busy="libraryLoading || undefined"
      @click="retryLibrary">
      Retry
    </button>

    <!-- The text already reaches screen readers as each button's
         description. -->
    <builder-fixed-tooltip :tooltip="tooltip" testid="palette-tooltip" />
  </section>
</template>

<script setup>
  import { computed, inject, nextTick, ref } from 'vue';

  import BuilderFixedTooltip from './BuilderFixedTooltip.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderMenuButton from './BuilderMenuButton.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import { PALETTE } from '@/builder/catalog.js';
  import { focusLost, runCommand } from '@/builder/commands.js';
  import { iconSrc } from '@/builder/icons.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    diagramTemplateActions,
    templateIcons,
    templatesFull,
  } from '@/builder/templates.js';
  import {
    PALETTE_MIME,
    PALETTE_TEMPLATE_MIME,
    addInView,
    paletteNode,
  } from './paletteDnd.js';

  const store = useBuilderStore();
  // The view's command context (Builder.vue): where the canvas is
  // looking, so a node is added in view.
  const commands = inject('builderCommands', null);
  const palette = PALETTE;
  const groups = computed(() => store.paletteTemplateGroups);
  const newButton = ref(null);
  const libraryButton = ref(null);
  const PALETTE_HELP =
    'Select an item to add it to the diagram, or drag it onto the canvas.';
  const NEW_TEMPLATE = 'New device template';
  const LIBRARY = 'Node Templates library';
  // What the menu of a template of the diagram offers.
  const templateActions = computed(() =>
    diagramTemplateActions(store.templateRights),
  );

  // The custom icons kept where an entry's template is: the diagram's, or
  // the library's.
  function iconsOf(entry) {
    return entry.source === 'diagram' ? store.doc.icons : store.templates.icons;
  }

  // The library's first read is under way, or the last read failed: Retry
  // then stays until a read answers.
  const libraryLoading = computed(() => store.templates.status === 'loading');
  const libraryNote = computed(() => {
    if (store.templates.error) {
      return 'Your library could not be loaded.';
    }

    if (store.templates.damaged) {
      return 'Your library cannot be read. A newer version of phenix may have saved it.';
    }

    return libraryLoading.value && !store.templates.loaded
      ? 'Loading your templates…'
      : '';
  });

  // Why "+" does nothing, or '' when it opens the template editor.
  const newBlocked = computed(() =>
    store.readOnly ? 'This diagram is read only.' : templatesFull(store.doc),
  );

  // "Router device", but "External device" rather than "External device
  // device".
  function deviceName(label) {
    return /\bdevice$/i.test(label) ? label : `${label} device`;
  }

  // Palette descriptions, and the palette's own instructions on its help
  // button, are tooltips (WCAG 1.4.13): shown on hover and focus, kept while
  // the pointer moves onto them, and dismissed with Escape.
  const tooltip = useFixedTooltip();
  const { showTip, tipEvents } = tooltip;

  // In a free spot of the part of the canvas in view, as the command
  // palette's Add commands do.
  function add(item) {
    addInView(commands || { store }, { kind: item.kind });
  }

  function addTemplate(entry) {
    addInView(commands || { store }, paletteNode(store, 'device', entry.key));
  }

  // A template goes by its key, so the canvas adds the node a click adds.
  function onDragStart(event, item) {
    event.dataTransfer?.setData(PALETTE_MIME, item.kind);

    if (item.key) {
      event.dataTransfer?.setData(PALETTE_TEMPLATE_MIME, item.key);
    }

    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = 'copy';
    }
  }

  // The template editor, on a new template of the diagram (see the
  // templates.new command, which says why when it cannot open).
  function newTemplate() {
    if (commands) {
      runCommand('templates.new', commands);
    }
  }

  // The drafts page's Node Templates tab (see the templates.library
  // command), which leaves the draft as Back to drafts does.
  function openLibrary() {
    if (commands) {
      runCommand('templates.library', commands);
    }
  }

  // Retry goes once the library is read: focus then moves to the library
  // button rather than fall to the page.
  async function retryLibrary() {
    if (libraryLoading.value) {
      return;
    }

    await store.fetchTemplates();
    await nextTick();

    if (focusLost()) {
      libraryButton.value?.focus();
    }
  }

  // A copy of a template of the diagram, with the custom icon it names,
  // goes to the user's library. The template stays in the diagram.
  async function saveToLibrary(entry) {
    try {
      await store.createLibraryTemplates([entry.template], {
        icons: templateIcons(entry.template, store.doc.icons),
      });
      store.announce(`Saved ${entry.name} to your library.`);
    } catch (error) {
      store.setError(
        store.describeLibraryError(error, `save ${entry.name} to your library`),
      );
    }
  }

  // What a template's menu does. Deleting is one edit, which Undo brings
  // back, so it asks nothing. Its entry goes with it: focus moves to the
  // entry now in its place, or to "+" after the last one.
  async function act(action, entry) {
    if (action === 'edit') {
      commands?.view.openDialog('template', {
        mode: 'diagram-edit',
        id: entry.id,
      });

      return;
    }

    if (action === 'library') {
      await saveToLibrary(entry);

      return;
    }

    const index = store.paletteTemplateGroups
      .find((group) => group.id === 'diagram')
      ?.entries.findIndex((other) => other.id === entry.id);

    if (!store.removeTemplate(entry.id)) {
      return;
    }

    await nextTick();

    const next = store.paletteTemplateGroups.find(
      (group) => group.id === 'diagram',
    )?.entries[index];

    (
      (next && document.querySelector(`[data-testid="${next.testid}"]`)) ||
      newButton.value
    )?.focus();
  }
</script>

<style scoped>
  .builder-panel__title,
  .builder-palette__subtitle {
    font-weight: 700;
    font-size: 0.9rem;
    margin: 0 0 0.35rem;
  }

  .builder-palette__header {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    margin-bottom: 0.35rem;
  }

  .builder-palette__header .builder-panel__title,
  .builder-palette__header .builder-palette__subtitle {
    margin: 0;
  }

  .builder-palette__header--templates {
    justify-content: space-between;
    margin-top: 0.75rem;
  }

  /* The name of a group of templates, smaller than the heading above it. */
  .builder-palette__group {
    margin: 0.5rem 0 0.25rem;
    font-size: 0.75rem;
    font-weight: 700;
    color: var(--bx-text-muted);
  }

  /* An icon button beside the Device templates heading, a target of 24 by
     24 pixels at least (WCAG 2.5.8). */
  .builder-palette__tool {
    display: inline-flex;
    flex: none;
    align-items: center;
    justify-content: center;
    min-width: 24px;
    min-height: 24px;
    padding: 0;
    border: 1px solid var(--bx-border);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    color: var(--bx-text);
    cursor: pointer;
  }

  .builder-palette__tool:hover:not([aria-disabled='true']) {
    background: var(--bx-bg-alt);
  }

  .builder-palette__tool[aria-disabled='true'] {
    color: var(--bx-text-muted);
    cursor: not-allowed;
  }

  /* The heading takes the room, so the two buttons stand together at the
     end of its row. */
  .builder-palette__header--templates .builder-palette__subtitle {
    flex: 1 1 auto;
  }

  /* What the list says of the library: that it is being read, or could
     not be. It takes no room while it says nothing. */
  .builder-palette__note {
    margin: 0.5rem 0 0.35rem;
    font-size: 0.8rem;
    color: var(--bx-text-muted);
  }

  .builder-palette__note:empty {
    margin: 0;
  }

  .builder-palette__retry[aria-busy='true'] {
    cursor: progress;
  }

  /* A template of the diagram: its entry, then its menu, as tall as the
     entry. The menu opens under its button, from the button's end, so it
     stays inside the column. */
  .builder-palette__entry {
    display: flex;
    align-items: stretch;
    gap: 0.25rem;
  }

  .builder-palette__entry .builder-palette__item {
    flex: 1 1 auto;
  }

  .builder-palette__entry :deep(.builder-menu) {
    flex: none;
    margin-bottom: 0.3rem;
  }

  .builder-palette__entry :deep(.builder-menu__button) {
    min-width: 24px;
    padding-inline: 0.2rem;
  }

  .builder-palette__entry :deep(.builder-menu__list) {
    right: 0;
    left: auto;
  }

  .builder-palette__list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
</style>
