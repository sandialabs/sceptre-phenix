<!-- Nodes & Objects: the node kinds and device templates. Each entry is a button
     first and draggable second, so the keyboard path is never second class. -->
<template>
  <section
    class="builder-palette builder-panel"
    aria-labelledby="palette-title">
    <div class="builder-palette__header">
      <h2 id="palette-title" class="builder-panel__title">
        Nodes &amp; Objects
      </h2>
    </div>

    <ul class="builder-palette__list">
      <li v-for="item in palette" :key="item.id">
        <span :id="`palette-hint-${item.id}`" hidden>{{ item.hint }}</span>
        <button
          type="button"
          class="builder-palette__item"
          :draggable="!store.readOnly"
          :disabled="store.readOnly"
          :data-testid="`palette-${item.id}`"
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
      <h3 class="builder-palette__subtitle">Templates</h3>
      <!-- aria-disabled, not disabled: it keeps focus and its tooltip,
           which then says why it does nothing. -->
      <button
        ref="newButton"
        type="button"
        class="builder-palette__tool builder-palette__tool--first"
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
      <!-- A section header collapses and expands the list below it, which
           stays in the page while hidden, so aria-controls finds it. -->
      <h4 v-if="sectioned" class="builder-palette__group">
        <button
          type="button"
          class="builder-palette__group-toggle"
          :aria-expanded="isOpen(group) ? 'true' : 'false'"
          :aria-controls="sectionListId(group)"
          :aria-label="sectionName(group)"
          :data-testid="`palette-section-${group.id}`"
          @click="toggleSection(group)">
          <builder-icon
            name="chevron-down"
            class="builder-palette__chevron"
            :size="14" />
          <span class="builder-palette__group-title">
            <span class="builder-palette__group-name">{{ group.label }}</span>
            ({{ group.entries.length }})
          </span>
        </button>
      </h4>
      <ul
        v-show="isOpen(group)"
        :id="sectionListId(group)"
        class="builder-palette__list"
        :aria-label="group.label"
        :data-testid="`palette-templates-${group.id}`">
        <li
          v-for="entry in group.entries"
          :key="entry.key"
          class="builder-palette__entry">
          <span v-if="hintOf(entry)" :id="hintId(entry)" hidden>
            {{ hintOf(entry) }}
          </span>
          <button
            type="button"
            class="builder-palette__item"
            :draggable="!store.readOnly"
            :disabled="store.readOnly"
            :data-testid="entry.testid"
            :aria-label="`Add ${deviceName(entry.name)}`"
            :aria-describedby="hintOf(entry) ? hintId(entry) : undefined"
            @click="addTemplate(entry)"
            @dragstart="onDragStart($event, { kind: 'device', key: entry.key })"
            v-on="tipEvents(hintOf(entry))">
            <builder-icon
              :name="entry.iconKey"
              :src="iconSrc(entry.icon, iconsOf(entry), iconLibrary)"
              :size="18" />
            <span class="builder-palette__label">{{ entry.name }}</span>
          </button>
          <builder-menu-button
            v-if="entry.source === 'diagram'"
            class="builder-palette__actions"
            :items="templateActions(entry)"
            :disabled="store.readOnly"
            :testid="`palette-template-actions-${entry.id}`"
            :aria-label="`Actions for template ${entry.name}`"
            @select="(item) => act(item.id, entry)" />
          <!-- A copy that the library has a newer version of, or whose
               library template is gone: a mark, and what to do about it.
               The icon and the words say it, not color alone. -->
          <p
            v-if="templateLinkMark(entry.link)"
            class="builder-palette__link"
            :class="`builder-palette__link--${entry.link.state}`"
            :data-testid="`palette-template-link-${entry.id}`">
            <builder-icon
              :name="entry.link.state === 'update' ? 'refresh' : 'warning'"
              :size="14" />
            <span class="builder-palette__link-text">
              {{ templateLinkMark(entry.link) }}
            </span>
            <button
              v-if="!store.readOnly"
              type="button"
              class="builder-palette__link-action"
              :data-testid="`palette-template-${linkAction(entry)}-${entry.id}`"
              :aria-label="`${LINK_ACTIONS[linkAction(entry)]} template ${entry.name}`"
              :aria-haspopup="
                linkAction(entry) === 'update' ? 'dialog' : undefined
              "
              @click="act(linkAction(entry), entry)">
              {{ LINK_ACTIONS[linkAction(entry)] }}
            </button>
          </p>
        </li>
      </ul>
    </template>

    <!-- Rendered from the start, so screen readers read what it says
         later. -->
    <p
      id="palette-library-note"
      class="builder-palette__note"
      role="status"
      data-testid="palette-library-note">
      {{ libraryNote }}
    </p>
    <!-- While the read it asked for runs, it keeps focus, and a second
         press does nothing. -->
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

    <!-- Update replaces the diagram's copy of a template, which may hold
         changes made in the diagram, so it asks first. Undo brings the
         copy back. -->
    <builder-confirm
      v-if="updating"
      id="palette-template-update"
      :title="updating.title"
      :message="updating.message"
      :confirm-label="updating.confirmLabel"
      @cancel="updating = null"
      @confirm="updateTemplate" />
  </section>
</template>

<script setup>
  import { computed, inject, nextTick, ref } from 'vue';

  import BuilderConfirm from './BuilderConfirm.vue';
  import BuilderFixedTooltip from './BuilderFixedTooltip.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderMenuButton from './BuilderMenuButton.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import { PALETTE } from '@/builder/catalog.js';
  import { focusLost, runCommand } from '@/builder/commands.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { iconSrc } from '@/builder/icons.js';
  import {
    loadCollapsedSections,
    saveCollapsedSections,
    toggledSections,
  } from '@/builder/paletteSections.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    diagramTemplateActions,
    templateLinkMark,
    templateLinkText,
    templateUpdateQuestion,
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
  // With one section only, its list shows under no header, and it does not
  // collapse.
  const sectioned = computed(() => groups.value.length > 1);
  // The ids of the collapsed sections, oldest first (see paletteSections.js).
  const collapsed = ref(loadCollapsedSections());
  const newButton = ref(null);
  const libraryButton = ref(null);
  const NEW_TEMPLATE = 'New device template';
  const LIBRARY = 'Node Templates library';
  // What the menu of a template of the diagram offers: with Update and
  // Unlink for a copy linked to the library.
  function templateActions(entry) {
    return diagramTemplateActions(entry.link);
  }

  // The button on the mark of a copy: Update for a newer version, else
  // Unlink.
  const LINK_ACTIONS = { update: 'Update', unlink: 'Unlink' };

  function linkAction(entry) {
    return entry.link?.state === 'update' ? 'update' : 'unlink';
  }

  // What an entry's tooltip and description say: the template's
  // description, then how a copy stands against the library.
  function hintOf(entry) {
    return [entry.description, templateLinkText(entry.link)]
      .filter(Boolean)
      .join(' ');
  }

  // The copies of icons kept where an entry's template is: the diagram's.
  // The template library keeps none: its templates' icons are the icon
  // library's.
  function iconsOf(entry) {
    return entry.source === 'diagram' ? store.doc.icons : null;
  }

  // The library's first read is in progress, or the last read failed. Retry
  // then stays until a read answers.
  const libraryLoading = computed(() => store.templates.status === 'loading');
  const libraryNote = computed(() => {
    if (store.templates.error) {
      return 'The Builder could not load the template library.';
    }

    if (store.templates.damaged) {
      return 'The Builder cannot read the template library. A newer version of phenix may have saved it.';
    }

    return libraryLoading.value && !store.templates.loaded
      ? 'Loading the template library…'
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

  // The id of an entry's description, made safe for an id.
  function hintId(entry) {
    return `${encodeURIComponent(entry.testid)}-hint`;
  }

  // The id of a section's list, made safe for an id.
  function sectionListId(group) {
    return `palette-section-list-${encodeURIComponent(group.id)}`;
  }

  // The name of a section's header, for example "Plant, 3 templates". The
  // header shows "Plant (3)".
  function sectionName(group) {
    const count = group.entries.length;

    return `${group.label}, ${count} ${count === 1 ? 'template' : 'templates'}`;
  }

  function isOpen(group) {
    return !sectioned.value || !collapsed.value.includes(group.id);
  }

  function toggleSection(group) {
    collapsed.value = toggledSections(collapsed.value, group.id);
    saveCollapsedSections(collapsed.value);
  }

  // Palette descriptions are tooltips (WCAG 1.4.13): shown on hover and
  // focus, kept while the pointer moves onto them, and dismissed with Escape.
  const tooltip = useFixedTooltip();
  const { tipEvents } = tooltip;

  // In a free spot of the part of the canvas in view, as the command
  // palette's Add commands do.
  function add(item) {
    addInView(commands || { store }, paletteNode(store, item.id));
  }

  function addTemplate(entry) {
    addInView(commands || { store }, paletteNode(store, 'device', entry.key));
  }

  // An entry goes by its id, and a template by its key, so the canvas adds
  // the node a click adds.
  function onDragStart(event, item) {
    event.dataTransfer?.setData(PALETTE_MIME, item.id || item.kind);

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
  // command), which leaves the draft as the Drafts button does.
  function openLibrary() {
    if (commands) {
      runCommand('templates.library', commands);
    }
  }

  // Retry disappears after a successful read of the library. Focus then
  // moves to the library button and does not fall to the page.
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

  // Copies a diagram template to the library, its custom icon first (see
  // saveTemplateToLibrary). The diagram's template stays, linked to the copy.
  async function saveToLibrary(entry) {
    try {
      const warnings = await store.saveTemplateToLibrary(entry.template);

      store.announce(
        [`Saved ${entry.name} to the library.`, ...warnings].join(' '),
      );

      if (warnings.length > 0) {
        store.notice = {
          text: warnings.join(' '),
          seq: store.announcementSeq,
        };
      }
    } catch (error) {
      store.setError(
        store.describeLibraryError(error, `save ${entry.name} to the library`),
      );
    }
  }

  // The question before Update: the copy to update, with what the update
  // does (see templateUpdateQuestion).
  const updating = ref(null);

  // Focuses the entry of a template of the diagram, after a change that
  // removed the control focus was on.
  async function focusEntry(entry) {
    await nextTick();

    if (focusLost()) {
      document.querySelector(`[data-testid="${entry.testid}"]`)?.focus();
    }
  }

  // Updates the copy the question is about (see updateTemplateCopies). Its
  // mark goes, so focus moves to its entry.
  async function updateTemplate() {
    const { entry } = updating.value;

    updating.value = null;
    store.updateTemplateCopies([entry.id]);
    await focusEntry(entry);
  }

  // What a template's menu does. Deleting is one edit, which Undo brings
  // back, so it asks nothing. Its entry goes with it: focus moves to the
  // entry now in its place, or to "+" after the last one. Update asks
  // first. Unlink is one edit, which Undo brings back, so it asks nothing.
  async function act(action, entry) {
    if (action === 'update') {
      if (entry.link?.state === 'update') {
        updating.value = {
          ...templateUpdateQuestion([
            { ...entry.link, template: entry.template },
          ]),
          entry,
        };
      }

      return;
    }

    if (action === 'unlink') {
      store.unlinkTemplate(entry.id);
      await focusEntry(entry);

      return;
    }

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
    margin-top: 0.75rem;
  }

  /* A section header: a full-width button with a chevron, the name and the
     count in parentheses, in text smaller than the heading above it. */
  .builder-palette__group {
    margin: 0.5rem 0 0.25rem;
    font-size: 0.75rem;
    font-weight: 700;
  }

  .builder-palette__group-toggle {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    width: 100%;
    min-height: 24px;
    padding: 0.15rem 0.25rem;
    border: 0;
    border-radius: var(--bx-radius);
    background: none;
    color: var(--bx-text-muted);
    font: inherit;
    text-align: left;
    cursor: pointer;
  }

  .builder-palette__group-toggle:hover {
    background: var(--bx-bg-alt);
    color: var(--bx-text);
  }

  .builder-palette__group-title {
    flex: 1 1 auto;
    min-width: 0;
    overflow-wrap: anywhere;
  }

  /* The chevron points down while the section is expanded, and to the end
     of the line while it is collapsed. */
  .builder-palette__chevron {
    flex: none;
    transition: transform var(--bx-motion) ease-in-out;
  }

  .builder-palette__group-toggle[aria-expanded='false']
    .builder-palette__chevron {
    transform: rotate(-90deg);
  }

  /* An icon button beside the Templates heading, a target of 24 by 24
     pixels at least (WCAG 2.5.8). */
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

  /* "+" and the library button stand together at the end of the row. */
  .builder-palette__tool--first {
    margin-inline-start: auto;
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
    flex-wrap: wrap;
    align-items: stretch;
    gap: 0 0.25rem;
  }

  /* The mark of a copy that needs attention: a line of its own under the
     entry, an icon and words, then its button. */
  .builder-palette__link {
    display: flex;
    flex: 1 0 100%;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem;
    margin: -0.15rem 0 0.3rem;
    padding-inline-start: 0.4rem;
    font-size: 0.75rem;
    color: var(--bx-text-muted);
  }

  .builder-palette__link--update {
    color: var(--bx-accent);
  }

  .builder-palette__link--deleted {
    color: var(--bx-warning);
  }

  .builder-palette__link-text {
    flex: 1 1 auto;
    min-width: 0;
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  /* A small button, still a target of 24 by 24 pixels (WCAG 2.5.8). */
  .builder-palette__link-action {
    flex: none;
    min-width: 24px;
    min-height: 24px;
    padding: 0 0.4rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    color: var(--bx-text);
    font: inherit;
    cursor: pointer;
  }

  .builder-palette__link-action:hover {
    background: var(--bx-bg-alt);
  }

  /* The item's width: 100% would fill the row and push the menu below it. */
  .builder-palette__entry .builder-palette__item {
    flex: 1 1 0;
    width: auto;
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
