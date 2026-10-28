<!-- The Node Editor: the Inspector (variant 'editor') on one node in a large
     dialog (see nodeEditorHost in nodeEditor.js). Apply is one undo step. -->
<template>
  <builder-dialog
    :title="title"
    title-id="node-editor-title"
    class="builder-node-editor"
    data-testid="node-editor"
    @close="requestClose">
    <div v-if="!gone" class="builder-node-editor__tools">
      <button
        v-if="sectioned"
        type="button"
        class="builder-button builder-node-editor__toc-toggle"
        aria-controls="node-editor-toc"
        :aria-expanded="tocOpen ? 'true' : 'false'"
        data-testid="node-editor-toc-toggle"
        @click="tocOpen = !tocOpen">
        <builder-icon
          class="builder-node-editor__toc-chevron"
          name="chevron-down"
          :size="14" />
        Table of contents
      </button>
      <span class="builder-node-editor__tools-end">
        <span class="builder-node-editor__search">
          <builder-icon
            class="builder-node-editor__search-icon"
            name="search"
            :size="14" />
          <input
            v-model="query"
            type="search"
            class="builder-node-editor__search-input"
            aria-label="Search fields"
            placeholder="Field name or path"
            autocomplete="off"
            spellcheck="false"
            :aria-describedby="
              searching ? 'node-editor-search-results' : undefined
            "
            data-testid="node-editor-search"
            @keydown="onSearchKey" />
        </span>
        <template v-if="sectioned">
          <button
            type="button"
            class="builder-button"
            data-testid="node-editor-expand-all"
            @click="expandAll">
            Expand all
          </button>
          <button
            type="button"
            class="builder-button"
            data-testid="node-editor-collapse-all"
            @click="collapseAll">
            Collapse all
          </button>
        </template>
      </span>
    </div>
    <p
      v-if="searching"
      id="node-editor-search-results"
      class="builder-node-editor__results"
      data-testid="node-editor-search-results">
      {{ resultText }}
    </p>

    <div class="builder-node-editor__main">
      <nav
        v-if="sectioned"
        id="node-editor-toc"
        class="builder-node-editor-toc"
        :class="{ 'is-open': tocOpen }"
        aria-label="Table of contents"
        data-testid="node-editor-toc">
        <node-editor-toc :entries="toc" :active="active" @go="goTo" />
      </nav>

      <!-- tabindex: Firefox makes a box that scrolls a Tab stop of its own.
           This box holds fields, which Tab reaches and scrolls into view. -->
      <div
        ref="body"
        class="builder-node-editor__body"
        :class="{ 'is-searching': searching }"
        tabindex="-1"
        @focusin="uncover">
        <p
          v-if="gone"
          class="builder-node-editor__gone"
          data-testid="node-editor-gone">
          This node is no longer in the diagram.
        </p>
        <builder-inspector
          v-else
          ref="inspector"
          :host="host"
          variant="editor"
          labelledby="node-editor-title"
          @combine="$emit('combine')"
          @layers="$emit('layers')" />
      </div>
    </div>

    <p class="builder-visually-hidden" role="status">
      <span
        v-if="status.text"
        :key="status.key"
        data-testid="node-editor-status">
        {{ status.text }}
      </span>
    </p>

    <div class="builder-node-editor__footer">
      <p
        id="node-editor-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span
          v-if="error.text"
          :key="error.key"
          data-testid="node-editor-error">
          {{ error.text }}
        </span>
      </p>

      <!-- Both rows stay in the document, one of them shown, so focus can
           go back to the button that asked. -->
      <div
        v-show="confirming"
        class="builder-dialog__actions"
        role="group"
        aria-labelledby="node-editor-discard-question"
        data-testid="node-editor-discard">
        <p
          id="node-editor-discard-question"
          class="builder-node-editor__question">
          Discard changes to this node?
        </p>
        <button
          ref="keepButton"
          type="button"
          class="builder-button"
          aria-describedby="node-editor-discard-question"
          data-testid="node-editor-keep"
          @click="keepEditing">
          Keep editing
        </button>
        <button
          type="button"
          class="builder-button builder-button--danger"
          data-testid="node-editor-discard-confirm"
          @click="$emit('close')">
          Discard
        </button>
      </div>
      <div v-show="!confirming" class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="node-editor-cancel"
          @click="requestClose">
          {{ viewing ? 'Close' : 'Cancel' }}
        </button>
        <button
          v-if="!viewing"
          type="button"
          class="builder-button builder-button--primary"
          data-testid="node-editor-apply"
          @click="apply">
          Apply
        </button>
      </div>
    </div>
  </builder-dialog>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    ref,
    watch,
  } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import BuilderInspector from '../BuilderInspector.vue';
  import { useKeepOpenForTip } from '../fixedTooltip.js';
  import { focusQuietly } from '../inspector/fieldTooltip.js';
  import { useDialogStatus, useFieldError } from './message.js';
  import NodeEditorToc from './NodeEditorToc.vue';

  import { inspectorLock, inspectorName } from '@/builder/adapters/forms.js';
  import { isTextEntry } from '@/builder/commands.js';
  import {
    searchFields,
    searchPath,
    searchResultText,
    searchWords,
  } from '@/builder/fieldSearch.js';
  import { activeEntry, outlineTree } from '@/builder/formOutline.js';
  import { findNode } from '@/builder/model.js';
  import { nodeEditorHost } from '@/builder/nodeEditor.js';
  import { useBuilderStore } from '@/builder/store.js';

  const props = defineProps({
    // The id of the node the dialog edits.
    nodeId: { type: String, required: true },
  });

  const emit = defineEmits(['close', 'combine', 'layers']);

  const store = useBuilderStore();
  const status = useDialogStatus(store);
  const { error, fail } = useFieldError('node-editor-error');

  const body = ref(null);
  const inspector = ref(null);
  const keepButton = ref(null);
  const confirming = ref(false);

  const host = nodeEditorHost(store, props.nodeId, {
    announce: (message) => status.set(message),
  });
  const selection = host.inspectorSelection;

  const gone = computed(() => !findNode(store.doc, props.nodeId));
  // A device's form has sections and lists, which the table of contents
  // lists and Expand all and Collapse all act on.
  const sectioned = computed(
    () => findNode(store.doc, props.nodeId)?.kind === 'device',
  );
  // Nothing to apply: a read-only draft, a device of an included topology,
  // or a node that is gone.
  const viewing = computed(
    () =>
      store.readOnly || gone.value || inspectorLock(store.doc, selection).all,
  );

  // "Edit device plc-01", or "Device plc-01" when it opens read only. Named
  // as it opened: a rename shows once it is applied, as the dialog closes.
  const name = inspectorName(store.doc, selection);
  const title = viewing.value
    ? `${name[0].toUpperCase()}${name.slice(1)}`
    : `Edit ${name}`;

  // What the store announces while the dialog is open: what the actions
  // that act at once did (Move, Add connection point, Layer, a
  // device's color). Save states (with a slot) are left to the page.
  watch(
    () => store.announcementSeq,
    () => {
      if (store.announcement && !store.announcementSlot) {
        status.set(store.announcement);
      }
    },
  );

  // --- closing ---------------------------------------------------------

  // Where focus was when the dialog asked whether to discard changes.
  let asked = null;

  function changed() {
    return !viewing.value && Boolean(inspector.value?.changed);
  }

  // Cancel, Escape, the Close button and a click outside the dialog. With
  // changes, it asks first. Asked again, it keeps editing, as Escape
  // cancels a question.
  async function requestClose() {
    if (confirming.value) {
      keepEditing();

      return;
    }

    if (!changed()) {
      emit('close');

      return;
    }

    asked = document.activeElement;
    confirming.value = true;
    await nextTick();
    keepButton.value?.focus();
  }

  async function keepEditing() {
    confirming.value = false;
    await nextTick();

    (asked?.isConnected && body.value?.contains(asked)
      ? asked
      : firstField()
    )?.focus();
    asked = null;
  }

  useKeepOpenForTip(() =>
    Boolean(body.value?.querySelector('.builder-tooltip--fixed')),
  );

  // --- applying --------------------------------------------------------

  function firstField() {
    return (
      body.value?.querySelector(
        'form :is(input, select, textarea):not([type="hidden"]):not(:disabled)',
      ) || body.value
    );
  }

  // Applies the form's edits as one undo step, and closes the dialog. With
  // errors, focus moves to the form's summary of them, and the dialog stays
  // open.
  async function apply() {
    if (confirming.value || viewing.value) {
      return;
    }

    error.clear();

    // A field of the form being typed in commits what it holds, as its
    // change event would.
    const field = document.activeElement;

    if (isTextEntry(field) && body.value?.contains(field)) {
      field.dispatchEvent(new Event('change', { bubbles: true }));
      await nextTick();
    }

    const reason = inspector.value?.settle() || '';

    if (reason) {
      // The fields with errors show, which a search could hide. They stay
      // expanded (see settle), not collapsed as before the search.
      saved = null;
      query.value = '';
      await fail(`Could not apply the changes: ${reason}.`);
      await nextTick();
      body.value?.querySelector('.builder-inspector__error-link')?.focus();

      return;
    }

    emit('close');
  }

  // --- leaving the draft ---------------------------------------------

  // Before the draft is left (a reload, a sign-out, another page): the
  // field being typed in commits, and the form saves its valid unapplied
  // edits as one undo step (see saveUnapplied in BuilderInspector.vue).
  // Returns the fields it cannot save, or null.
  function saveUnapplied() {
    if (viewing.value) {
      return null;
    }

    const field = document.activeElement;

    if (isTextEntry(field) && body.value?.contains(field)) {
      field.dispatchEvent(new Event('change', { bubbles: true }));
    }

    return inspector.value?.saveUnapplied?.() || null;
  }

  defineExpose({ saveUnapplied });

  // --- sections and the table of contents --------------------------------

  function expandAll() {
    inspector.value?.expandAll();
    status.set('Expanded all sections.');
  }

  function collapseAll() {
    inspector.value?.collapseAll();
    status.set('Collapsed all sections.');
  }

  // The table of contents is open above the fields in a narrow window.
  const tocOpen = ref(false);
  const toc = ref([]);
  // The key of the entry in view (see activeEntry).
  const active = ref('');
  // The keys of the entries, in document order, and those in view.
  let keys = [];
  let elements = [];
  const inView = new Set();
  let mutations = null;
  let intersections = null;
  let frame = null;

  // The outline entries of the form: those of the renderers (sections,
  // lists, entries) and of the Inspector (checks, connection points,
  // position).
  function outlineElements() {
    return [...(body.value?.querySelectorAll('[data-outline]') || [])];
  }

  // How many outline entries hold the element: 0 for a section.
  function depthOf(element) {
    let depth = 0;

    for (
      let at = element.parentElement?.closest('[data-outline]');
      at && body.value?.contains(at);
      at = at.parentElement?.closest('[data-outline]')
    ) {
      depth += 1;
    }

    return depth;
  }

  // The levels the table of contents shows: sections, the lists and
  // groups in them, and the entries of those lists.
  const TOC_DEPTH = 2;

  function readOutline() {
    frame = null;
    markSearch();

    const shown = outlineElements()
      .filter((element) => !element.closest('[data-search="hidden"]'))
      .map((element) => ({ element, depth: depthOf(element) }))
      .filter(({ depth }) => depth <= TOC_DEPTH);
    const entries = shown.map(({ element, depth }) => {
      const count = element.dataset.outlineCount;

      return {
        key: element.dataset.outline,
        label: element.dataset.outlineLabel || '',
        count: count === undefined ? undefined : Number(count),
        errors: element.dataset.outlineErrors === 'true',
        depth,
      };
    });

    keys = entries.map((entry) => entry.key);
    elements = shown.map(({ element }) => element);
    toc.value = outlineTree(entries, TOC_DEPTH);

    if (intersections) {
      intersections.disconnect();
      inView.clear();
      shown.forEach(({ element }) => intersections.observe(element));
    }
  }

  function scheduleOutline() {
    if (frame === null) {
      frame = requestAnimationFrame(readOutline);
    }
  }

  async function goTo(key) {
    tocOpen.value = false;
    active.value = key;
    await inspector.value?.reveal(key);
  }

  // Whether the element is shown, and its top is in the box.
  function topInBox(element, { top, bottom }) {
    const at = element.getBoundingClientRect().top;

    return element.offsetParent !== null && at >= top && at < bottom;
  }

  // The entry in view. When the fields do not scroll, all of them are in
  // view: the entry gone to (or marked before) stays marked while it
  // shows. Scrolled to the end, the entries near the end cannot reach the
  // band at the top: the last one whose top is in view is the one in view
  // then.
  function markActive() {
    const box = body.value;

    if (!box) {
      return;
    }

    const rect = box.getBoundingClientRect();

    if (box.scrollHeight <= box.clientHeight + 2) {
      const marked = elements.find(
        (element) => element.dataset.outline === active.value,
      );

      if (marked && topInBox(marked, rect)) {
        return;
      }
    } else if (box.scrollTop + box.clientHeight >= box.scrollHeight - 2) {
      const last = elements.findLast((element) => topInBox(element, rect));

      if (last) {
        active.value = last.dataset.outline;

        return;
      }
    }

    active.value = activeEntry(keys, inView);
  }

  let scrollFrame = null;

  function onScroll() {
    if (scrollFrame === null) {
      scrollFrame = requestAnimationFrame(() => {
        scrollFrame = null;
        markActive();
      });
    }
  }

  function watchOutline() {
    if (typeof MutationObserver === 'function' && body.value) {
      mutations = new MutationObserver(scheduleOutline);
      mutations.observe(body.value, {
        subtree: true,
        childList: true,
        characterData: true,
        attributes: true,
        attributeFilter: [
          'data-outline',
          'data-outline-label',
          'data-outline-count',
          'data-outline-errors',
        ],
      });
    }

    // The entry in view: the innermost in the band at the top of the
    // fields.
    if (typeof IntersectionObserver === 'function' && body.value) {
      intersections = new IntersectionObserver(
        (changes) => {
          for (const change of changes) {
            const key = change.target.dataset.outline;

            if (change.isIntersecting) {
              inView.add(key);
            } else {
              inView.delete(key);
            }
          }

          markActive();
        },
        { root: body.value, rootMargin: '0px 0px -75% 0px' },
      );
      body.value.addEventListener('scroll', onScroll, { passive: true });
    }

    readOutline();
  }

  // The section headers that stick to the top of the fields (see the style).
  const STICKY =
    '.group--heading > .group-item > * > fieldset.group > legend.group-label, ' +
    '.inspector-section[open] > .inspector-section__summary';

  // The scroll padding keeps a field clear of a header that sticks only
  // where the browser scrolls it into view, not one in view under it. Such
  // a field with focus is scrolled clear of it (WCAG 2.4.11).
  function uncover(event) {
    const field = event.target;

    requestAnimationFrame(() => {
      const box = body.value;

      if (!box || document.activeElement !== field) {
        return;
      }

      const { top } = field.getBoundingClientRect();

      for (const header of box.querySelectorAll(STICKY)) {
        const covers = header.getBoundingClientRect();

        if (
          !header.contains(field) &&
          covers.top <= top &&
          covers.bottom > top
        ) {
          box.scrollBy({ top: top - covers.bottom - 8 });

          return;
        }
      }
    });
  }

  // --- searching the fields ----------------------------------------------

  const query = ref('');
  const searching = computed(() => searchWords(query.value).length > 0);
  // How many fields the search shows (see searchFields).
  const results = ref(0);
  const resultText = computed(() => searchResultText(results.value));
  // What was collapsed and closed before the search expanded all.
  let saved = null;
  let announceTimer = null;

  // The parts of the form a search shows or hides: the fields, and the
  // sections, lists and entries that hold them.
  const PARTS = '[data-path], [data-outline], .control';

  function partLabel(element) {
    const label =
      element.dataset.outlineLabel ??
      element.querySelector(':scope > label')?.textContent ??
      '';

    return label.replace(/\s*\*\s*$/, '').trim();
  }

  // Marks each part with what the search shows of it (data-search), which
  // the style hides. Without a search, the marks go.
  function markSearch() {
    if (!searching.value) {
      body.value
        ?.querySelectorAll('[data-search]')
        .forEach((element) => element.removeAttribute('data-search'));

      return;
    }

    const elements = [...(body.value?.querySelectorAll(PARTS) || [])];
    const index = new Map(elements.map((element, at) => [element, at]));
    const { states, count } = searchFields(
      elements.map((element) => ({
        label: partLabel(element),
        path: searchPath(element.dataset.path ?? element.dataset.outline),
        parent: index.get(element.parentElement?.closest(PARTS)) ?? -1,
        field: element.classList.contains('control'),
      })),
      query.value,
    );

    // A field being edited stays, though an edit can make it match no more.
    const focused = document.activeElement;

    elements.forEach((element, at) => {
      const state =
        states[at] === 'hidden' && element.contains(focused)
          ? 'holds'
          : states[at];

      if (element.dataset.search !== state) {
        element.dataset.search = state;
      }
    });
    results.value = count;
  }

  watch(searching, (now) => {
    if (now) {
      saved = inspector.value?.collapsedState() ?? null;
      inspector.value?.expandAll();
    } else if (saved) {
      inspector.value?.restoreCollapsed(saved);
      saved = null;
    }
  });

  // Says how many fields match once the typing stops.
  watch(query, () => {
    readOutline();
    clearTimeout(announceTimer);
    announceTimer = searching.value
      ? setTimeout(() => status.set(resultText.value), 500)
      : null;
  });

  // Escape empties the search. With no search, it closes the dialog.
  function onSearchKey(event) {
    if (event.key === 'Escape' && query.value) {
      event.preventDefault();
      event.stopPropagation();
      query.value = '';
    }
  }

  onMounted(async () => {
    // The drive image field suggests the server's disk images, and warns
    // of one the server lacks, as beside the canvas.
    store.fetchDisks();
    await nextTick();
    // A tooltip at once would cover the table of contents.
    focusQuietly(firstField());
    watchOutline();
  });

  onBeforeUnmount(() => {
    clearTimeout(announceTimer);
    if (saved) {
      inspector.value?.restoreCollapsed(saved);
    }
    mutations?.disconnect();
    intersections?.disconnect();
    body.value?.removeEventListener('scroll', onScroll);
    for (const pending of [frame, scrollFrame]) {
      if (pending !== null) {
        cancelAnimationFrame(pending);
      }
    }
  });
</script>

<style scoped>
  /* A large part of the window, so the form has room, or all of a
     narrow window. The body scrolls, and the footer stays in view. */
  .builder-node-editor[open] {
    display: flex;
    flex-direction: column;
    width: min(96vw, 1200px);
    max-width: none;
    height: min(92vh, 900px);
    max-height: none;
    /* Clip, not hidden: a script that scrolls a field into view cannot
       scroll the dialog, so the title and the tools stay in view. */
    overflow: hidden;
    overflow: clip;
  }

  @media (max-width: 699px) {
    .builder-node-editor[open] {
      width: 100vw;
      height: 100vh;
      height: 100dvh;
      margin: 0;
      border: 0;
      border-radius: 0;
    }
  }

  /* The search, Expand all and Collapse all at the end of the row, and the
     Table of contents button at its start in a narrow window. There, the
     search takes a row of its own. */
  .builder-node-editor__tools {
    display: flex;
    flex: none;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
  }

  .builder-node-editor__tools-end {
    display: flex;
    flex: 1 1 auto;
    flex-wrap: wrap;
    justify-content: flex-end;
    align-items: center;
    gap: 0.5rem;
    margin-left: auto;
  }

  .builder-node-editor__search {
    position: relative;
    display: flex;
    flex: 1 1 14rem;
    align-items: center;
    max-width: 22rem;
  }

  .builder-node-editor__search-icon {
    position: absolute;
    left: 0.55rem;
    color: var(--bx-text-muted);
    pointer-events: none;
  }

  .builder-node-editor__search-input {
    width: 100%;
    min-width: 0;
    min-height: 2rem;
    padding: 0.3rem 0.45rem 0.3rem 1.9rem;
    border: 1px solid var(--bx-field-border);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    color: var(--bx-text);
    font: inherit;
  }

  .builder-node-editor__search-input::placeholder {
    color: var(--bx-text-muted);
    opacity: 1;
  }

  .builder-node-editor__results {
    flex: none;
    margin: 0 0 0.5rem;
    color: var(--bx-text-muted);
    font-size: 0.85rem;
  }

  /* While a search shows some fields, the parts of the form that do not
     match hide, and so do the rows that hold only those, and Add in a list
     that holds a match. */
  .builder-node-editor__body.is-searching :deep([data-search='hidden']),
  .builder-node-editor__body.is-searching
    :deep(
      :is(
        .vertical-layout-item,
        .group-item,
        .inspector-section__item,
        .array-list-item-wrapper
      ):not(
        :has(
          [data-search='match'],
          [data-search='holds'],
          [data-search='inside']
        )
      )
    ),
  .builder-node-editor__body.is-searching
    :deep([data-search='holds'] > :is(.array-list-add, .array-list-no-data)) {
    display: none;
  }

  .builder-node-editor__toc-chevron {
    transform: rotate(-90deg);
    transition: transform var(--bx-motion);
  }

  .builder-node-editor__toc-toggle[aria-expanded='true']
    .builder-node-editor__toc-chevron {
    transform: none;
  }

  /* The table of contents beside the fields, each with a scroll of its
     own. */
  .builder-node-editor__main {
    display: flex;
    flex: 1 1 auto;
    gap: 0.75rem;
    min-height: 0;
  }

  /* Relative, so text hidden from sight inside it is clipped by it and does
     not make the dialog overflow. */
  .builder-node-editor-toc {
    position: relative;
    flex: none;
    width: 14rem;
    padding-right: 0.5rem;
    overflow-y: auto;
    border-right: 1px solid var(--bx-border);
  }

  .builder-node-editor__toc-toggle {
    display: none;
  }

  /* The scroll padding keeps a field that takes focus, or an entry gone
     to, clear of the section header that sticks (WCAG 2.4.11). */
  .builder-node-editor__body {
    position: relative;
    flex: 1 1 auto;
    min-width: 0;
    min-height: 0;
    padding-right: 0.25rem;
    overflow-y: auto;
    scroll-padding-top: 2.75rem;
  }

  /* The headers of the node's sections (General, Hardware, Network) and of
     More settings stay at the top of the fields as they scroll, over them.
     A floated legend is not the fieldset's rendered legend, so it is a row
     of the fieldset's grid (see builder.css) that can stick. */
  .builder-node-editor__body
    :deep(
      .group--heading > .group-item > * > fieldset.group > legend.group-label
    ),
  .builder-node-editor__body
    :deep(.inspector-section[open] > .inspector-section__summary) {
    position: sticky;
    top: 0;
    z-index: 3;
  }

  .builder-node-editor__body
    :deep(
      .group--heading > .group-item > * > fieldset.group > legend.group-label
    ) {
    float: left;
    grid-column: 1 / -1;
    width: 100%;
    margin: 0 0 0.35rem;
    padding: 0.2rem 0;
    border-bottom: 1px solid var(--bx-border);
    background: var(--bx-surface);
  }

  .builder-node-editor__body
    :deep(
      .group--heading
        > .group-item
        > *
        > fieldset.group--collapsed
        > legend.group-label
    ) {
    margin-bottom: 0;
    border-bottom: 0;
  }

  /* In a narrow window the fields take the width, and the table of
     contents opens above them. */
  @media (max-width: 899px) {
    .builder-node-editor__main {
      flex-direction: column;
      gap: 0.5rem;
    }

    .builder-node-editor__toc-toggle {
      display: inline-flex;
    }

    .builder-node-editor-toc {
      display: none;
      width: auto;
      max-height: 40vh;
      padding: 0 0 0.5rem;
      border-right: 0;
      border-bottom: 1px solid var(--bx-border);
    }

    .builder-node-editor-toc.is-open {
      display: block;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .builder-node-editor__toc-chevron {
      transition: none;
    }
  }

  .builder-node-editor__gone {
    margin: 0;
  }

  .builder-node-editor__footer {
    flex: none;
    margin-top: 0.75rem;
    padding-top: 0.75rem;
    border-top: 1px solid var(--bx-border);
  }

  .builder-node-editor__footer .builder-dialog__actions {
    align-items: center;
  }

  .builder-node-editor__question {
    flex: 1 1 12rem;
    margin: 0;
    font-weight: 600;
  }
</style>
