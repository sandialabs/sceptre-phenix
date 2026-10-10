<!--
  The rest of the Inspector's Diagram section, below the diagram's Name and
  Description: the size of the diagram's node icons, who made the diagram
  and who saved it last, the annotations of the config the diagram was
  imported from, its scenarios, each with the hosts each of its apps runs
  on, and the notes of the diagram. All but the icon size and the notes are
  read only here. Edit scenarios (Add scenario when there is none) opens the
  Scenario dialog the toolbar's Scenarios button opens, and like it, not in
  a read-only draft.

  Icon size is the size devices, switches and groups draw their icons at,
  unless one has a size of its own (see nodeIconSize in model.js). It is
  presentation only, so a choice applies at once, as one undo step (the
  store's setIconSize). As in a device's form, one choice is one step: a
  size chosen with the pointer applies at once, and one stepped to with the
  arrow keys when the choice is made, on Enter or when focus leaves the
  select (see heldCommit.js). A size the store refuses leaves the select
  showing the diagram's size. A read-only draft shows the size as text.

  Details shows what the server wrote into the document's metadata when it
  stored it: who made it and when, and the user and time of the save that
  stored the content shown. The editor never sets them, so they are those
  of the last save the server confirmed, and of an older save after an
  undo. A draft made from an uploaded file also names that file. It is
  text, not a live region: the values change with every save, which the
  save state announces already.

  A document names its scenarios and holds none of their content, so the
  apps of each are read from its config (see the store's fetchScenario),
  when the role may read it.

  Notes are the metadata's notes, one text box each. A note is written
  into the diagram when its box changes and loses focus, as one undo step
  (the store's setDiagramNotes); a box left empty drops its note. Text the
  server would refuse (longer than its byte limit, or holding a control
  character other than newline and tab) shows an error under its box, and
  the diagram keeps the note as last saved until the text is fixed. A
  read-only draft shows the notes as text.
-->
<template>
  <div class="inspector-diagram" data-testid="inspector-icon-size">
    <h3 id="inspector-icon-size-label">Icon size</h3>
    <p v-if="store.readOnly" data-testid="inspector-icon-size-value">
      {{ ICON_SIZE_TITLES[iconSize] }}
    </p>
    <select
      v-else
      ref="iconSizeSelect"
      :value="iconSize"
      aria-labelledby="inspector-icon-size-label"
      aria-describedby="inspector-icon-size-help"
      data-testid="inspector-icon-size-select"
      @pointerdown="iconSizeChoice.point()"
      @keydown="onIconSizeKey"
      @change="iconSizeChoice.change($event.target.value)"
      @blur="iconSizeChoice.flush()">
      <option v-for="size in ICON_SIZES" :key="size" :value="size">
        {{ ICON_SIZE_TITLES[size] }} ({{ ICON_SIZE_PIXELS[size] }} pixels)
      </option>
    </select>
    <p id="inspector-icon-size-help" class="inspector-diagram__none">
      Devices, switches and groups draw their icons at this size, unless one has
      a size of its own.
    </p>
  </div>

  <!-- A diagram stored before the server kept these has none to show. -->
  <div
    v-if="details.length"
    class="inspector-diagram"
    data-testid="inspector-details">
    <h3>Details</h3>
    <dl class="inspector-diagram__list">
      <div
        v-for="row in details"
        :key="row.id"
        :data-testid="`inspector-${row.id}`">
        <dt>{{ row.label }}</dt>
        <dd class="inspector-diagram__detail">
          <time v-if="row.time" :datetime="row.at">{{ row.time }}</time
          >{{ row.text }}
        </dd>
      </div>
    </dl>
  </div>

  <!-- A diagram drawn here, not imported, has no source to list. -->
  <div
    v-if="source"
    class="inspector-diagram"
    data-testid="inspector-annotations">
    <h3>Annotations</h3>
    <p class="inspector-diagram__from" data-testid="inspector-source">
      From {{ source.kind }} <strong>{{ source.name }}</strong
      ><template v-if="source.imported"
        >, imported
        <time :datetime="source.importedAt">{{
          source.imported
        }}</time></template
      >
    </p>
    <dl v-if="annotations.length" class="inspector-diagram__list">
      <div v-for="([key, value], index) in annotations" :key="key">
        <dt :id="`inspector-annotation-${index}`">{{ key }}</dt>
        <!-- A long value scrolls in its box, which then takes focus, so
             the keys scroll it too. v-text keeps its line breaks and
             spaces exactly, with nothing around them. -->
        <dd>
          <div
            :ref="(element) => observe(key, element)"
            v-text="value"
            class="inspector-diagram__value"
            :role="scrolling.has(key) ? 'region' : undefined"
            :aria-labelledby="
              scrolling.has(key) ? `inspector-annotation-${index}` : undefined
            "
            :tabindex="scrolling.has(key) ? 0 : undefined" />
        </dd>
      </div>
    </dl>
    <p v-else class="inspector-diagram__none">No annotations.</p>
  </div>

  <div class="inspector-diagram" data-testid="inspector-scenario">
    <h3>Scenarios</h3>
    <p v-if="!scenarioRows.length" class="inspector-diagram__none">
      No scenarios.
    </p>
    <ul
      v-else
      class="inspector-diagram__scenarios"
      data-testid="inspector-scenarios">
      <li
        v-for="(row, index) in scenarioRows"
        :key="row.name"
        :data-testid="`inspector-scenario-${index + 1}`">
        <p
          class="inspector-diagram__from"
          data-testid="inspector-scenario-name">
          Scenario <strong>{{ row.name }}</strong>
        </p>
        <p v-if="row.note" class="inspector-diagram__none">{{ row.note }}</p>
        <template v-else-if="row.apps.length">
          <p class="inspector-diagram__label">Apps and their hosts</p>
          <dl
            class="inspector-diagram__list"
            data-testid="inspector-scenario-apps">
            <div v-for="(app, at) in row.apps" :key="`${at}-${app.name}`">
              <dt>
                {{ app.name }}
                <span v-if="app.disabled" class="inspector-diagram__muted"
                  >(disabled)</span
                >
              </dt>
              <dd class="inspector-diagram__hosts">
                {{ app.hosts.length ? app.hosts.join(', ') : 'No hosts' }}
              </dd>
            </div>
          </dl>
        </template>
        <p v-else class="inspector-diagram__none">No apps.</p>
      </li>
    </ul>
    <!-- One button, whose name follows the list, so focus stays on it when
         the dialog closes after adding or removing scenarios. -->
    <button
      v-if="!store.readOnly"
      type="button"
      class="builder-button"
      aria-haspopup="dialog"
      data-testid="inspector-scenario-edit"
      @click="$emit('scenario')">
      <builder-icon name="document" :size="14" />
      {{ scenarioRows.length ? 'Edit scenarios' : 'Add scenario' }}
    </button>
  </div>

  <div class="inspector-diagram" data-testid="inspector-notes">
    <h3>Notes</h3>
    <template v-if="store.readOnly">
      <ul v-if="notes.length" class="inspector-diagram__notes">
        <li
          v-for="(note, index) in notes"
          :key="index"
          v-text="note"
          class="inspector-diagram__note-text"
          :data-testid="`inspector-note-${index + 1}`" />
      </ul>
      <p v-else class="inspector-diagram__none">No notes.</p>
    </template>
    <template v-else>
      <ul v-if="rows.length" class="inspector-diagram__notes">
        <li
          v-for="(row, index) in rows"
          :key="row.key"
          class="inspector-diagram__note">
          <textarea
            :ref="(element) => setNoteBox(row.key, element)"
            v-model="row.text"
            class="inspector-diagram__note-box"
            rows="3"
            :aria-label="`Note ${index + 1}`"
            :aria-invalid="noteProblem(row) ? 'true' : undefined"
            :aria-describedby="
              noteProblem(row) ? `inspector-note-error-${row.key}` : undefined
            "
            :data-testid="`inspector-note-${index + 1}`"
            @change="commitNotes" />
          <p
            v-if="noteProblem(row)"
            :id="`inspector-note-error-${row.key}`"
            class="inspector-diagram__note-error"
            role="alert"
            :data-testid="`inspector-note-error-${index + 1}`">
            <builder-icon name="close" :size="14" />
            <span>{{ noteProblem(row) }}</span>
          </p>
          <button
            type="button"
            class="builder-button inspector-diagram__note-delete"
            :data-testid="`inspector-note-delete-${index + 1}`"
            @click="deleteNote(index)">
            <builder-icon name="trash" :size="14" />
            Delete<span class="builder-visually-hidden">
              note {{ index + 1 }}</span
            >
          </button>
        </li>
      </ul>
      <p v-else class="inspector-diagram__none">No notes.</p>
      <button
        ref="addNoteButton"
        type="button"
        class="builder-button"
        data-testid="inspector-note-add"
        :disabled="notesFull"
        :aria-describedby="notesFull ? 'inspector-notes-limit' : undefined"
        @click="addNote">
        <builder-icon name="plus" :size="14" />
        Add note
      </button>
      <p
        v-if="notesFull"
        id="inspector-notes-limit"
        class="inspector-diagram__none"
        data-testid="inspector-notes-limit">
        A diagram holds at most {{ MAX_DIAGRAM_NOTES }} notes.
      </p>
    </template>
  </div>
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

  import BuilderIcon from '../BuilderIcon.vue';
  import { heldCommit, keyEffect } from './heldCommit.js';

  import { formatTimestamp } from '@/builder/format.js';
  import {
    diagramNoteProblem,
    documentIconSize,
    ICON_SIZE_PIXELS,
    ICON_SIZES,
    MAX_DIAGRAM_NOTES,
    documentScenarios,
    metadataOf,
    scenarioApps,
    sourceAnnotations,
  } from '@/builder/model.js';
  import { ICON_SIZE_TITLES } from '@/builder/schema.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { isBlank } from '@/builder/text.js';

  defineEmits(['scenario']);

  const store = useBuilderStore();

  // --- icon size ---------------------------------------------------------

  const iconSize = computed(() => documentIconSize(store.doc));
  const iconSizeSelect = ref(null);

  // A size the store refuses (while a conflict is resolved, or in a draft
  // that turned read only) leaves the document's size as it was, and so the
  // select's binding too, which then would not put it back: the select is
  // given the document's size again here.
  function commitIconSize(size) {
    const entry = store.setIconSize(size);

    if (!entry && iconSizeSelect.value) {
      iconSizeSelect.value.value = iconSize.value;
    }

    return Boolean(entry);
  }

  const iconSizeChoice = heldCommit(commitIconSize);

  // A key that steps the select holds the size it steps to; any other, such
  // as Enter or a shortcut, applies the size held.
  function onIconSizeKey(event) {
    const effect = keyEffect(event);

    if (effect === 'hold') {
      iconSizeChoice.key();
    } else if (effect === 'flush') {
      iconSizeChoice.flush();
    }
  }

  onBeforeUnmount(() => iconSizeChoice.flush());

  // A user and a time the document holds, as a row: the time in the
  // viewer's locale, then "by" the user. Either may be missing; with
  // neither there is no row.
  function stampRow(id, label, user, at) {
    const time = formatTimestamp(at);
    const by = typeof user === 'string' && user ? `by ${user}` : '';

    return time || by
      ? { id, label, at, time, text: time && by ? ` ${by}` : by }
      : null;
  }

  // Who made the diagram and who saved it last, and when, and the uploaded
  // file the draft was made from.
  const details = computed(() => {
    const { createdBy, createdAt, updatedBy, updatedAt } = metadataOf(
      store.doc,
    );
    const file = store.draftRecord.sourceFile;

    return [
      stampRow('created', 'Created', createdBy, createdAt),
      stampRow('edited', 'Last edited', updatedBy, updatedAt),
      file
        ? { id: 'source-file', label: 'Source file', time: '', text: file }
        : null,
    ].filter(Boolean);
  });

  const KINDS = { topology: 'Topology', experiment: 'Experiment' };

  // Where the diagram came from, when it was imported from a config, and
  // when, in the viewer's locale.
  const source = computed(() => {
    const { kind, name, importedAt } = store.doc.source || {};

    return KINDS[kind] && name
      ? {
          kind: KINDS[kind],
          name,
          importedAt,
          imported: formatTimestamp(importedAt),
        }
      : null;
  });

  const annotations = computed(() => sourceAnnotations(store.doc));

  // The Scenario configs the diagram lists, which it names but does not
  // hold: their apps are read from the server.
  const scenarioNames = computed(() => documentScenarios(store.doc));

  // Those whose apps have not been read, also one whose reading was dropped
  // since, as storing a scenario again drops it (see saveScenarioConfig).
  const unread = computed(() =>
    scenarioNames.value.filter((name) => !store.storedScenarios[name]),
  );

  watch(
    () => unread.value.join('\n'),
    () => unread.value.forEach((name) => store.fetchScenario(name)),
    { immediate: true },
  );

  const PROBLEMS = {
    forbidden:
      'Your role cannot read this scenario, so its apps are not listed.',
    missing: 'The server has no scenario of this name.',
    invalid: 'The server has no scenario of this name.',
  };

  // Each listed scenario with the apps to list, or what to say instead of
  // them.
  const scenarioRows = computed(() =>
    scenarioNames.value.map((name) => {
      const read = store.storedScenarios[name];

      if (read?.content) {
        return { name, apps: scenarioApps(read.content), note: '' };
      }

      if (!read || read.loading) {
        return { name, apps: [], note: 'Reading its apps…' };
      }

      return {
        name,
        apps: [],
        note: PROBLEMS[read.problem] || 'Its apps could not be read.',
      };
    }),
  );

  // --- notes -----------------------------------------------------------

  // The notes the diagram holds. The same list comes back while they stay
  // the same (an edit elsewhere copies the list as it is), so the rows below
  // are not reset by it, and lose no text being typed.
  const NO_NOTES = Object.freeze([]);
  const notes = computed(() => {
    const held = metadataOf(store.doc).notes;

    return Array.isArray(held) && held.length > 0 ? held : NO_NOTES;
  });

  // The notes being edited, one row each, with a key of their own so a row
  // keeps its text box, and the focus in it, while other rows come and go.
  // A row's `saved` is the note the diagram holds for it, which it keeps
  // while the row's text cannot be written; a row added and not written yet
  // has none, and is only here.
  const rows = ref([]);
  const noteBoxes = new Map();
  const addNoteButton = ref(null);
  let nextKey = 0;

  function setNoteBox(key, element) {
    if (element) {
      noteBoxes.set(key, element);
    } else {
      noteBoxes.delete(key);
    }
  }

  // Why a row's text cannot be written into the diagram (too long, or a
  // control character in it), or ''. A blank row has none: it is dropped.
  function noteProblem(row) {
    return isBlank(row.text) ? '' : diagramNoteProblem(row.text);
  }

  // The note a row puts in the diagram: its text, or, while that cannot be
  // written, the note the diagram holds for it, so a note that is too long
  // keeps its last saved text until it is fixed. A blank row, and a new one
  // whose text cannot be written, put none.
  function noteOf(row) {
    if (isBlank(row.text)) {
      return null;
    }

    return noteProblem(row) ? (row.saved ?? null) : row.text;
  }

  // The rows follow the diagram's notes. Rows that already put in what the
  // diagram holds are kept as they are, the text that cannot be written
  // included, less the blank ones, which the diagram never holds.
  watch(
    notes,
    (held) => {
      const kept = rows.value.filter((row) => !isBlank(row.text));
      const written = kept.filter((row) => noteOf(row) !== null);
      const same =
        written.length === held.length &&
        written.every((row, index) => noteOf(row) === held[index]);

      if (same) {
        written.forEach((row) => {
          row.saved = noteOf(row);
        });
        rows.value = kept;
      } else {
        rows.value = held.map((text) => ({
          key: (nextKey += 1),
          text,
          saved: text,
        }));
      }
    },
    { immediate: true },
  );

  const notesFull = computed(() => rows.value.length >= MAX_DIAGRAM_NOTES);

  // Where the focus goes when the row it was in goes: the text box now at
  // that place, else the last one, else Add note, so it never falls to the
  // page. A focus that is still somewhere is left there.
  function keepFocus(index) {
    nextTick(() => {
      const active = document.activeElement;

      if (active && active !== document.body) {
        return;
      }

      const row = rows.value[Math.min(index, rows.value.length - 1)];
      const target = row ? noteBoxes.get(row.key) : addNoteButton.value;

      target?.focus();
    });
  }

  // Writes the rows into the diagram, as one undo step; a blank row is
  // dropped, also when the diagram's notes stay as they were. A row whose
  // text cannot be written keeps its text box, with its error, and the
  // diagram its saved note.
  function writeNotes() {
    const entry = store.setDiagramNotes(
      rows.value.map(noteOf).filter((note) => note !== null),
    );

    if (!entry) {
      rows.value = rows.value.filter((row) => !isBlank(row.text));
    }
  }

  // A box left empty takes its row, and the focus may have been on that
  // row's Delete button; a box left with text keeps its row, and the focus
  // goes wherever the user moved it.
  function commitNotes(event) {
    const index = rows.value.findIndex(
      (row) => noteBoxes.get(row.key) === event?.target,
    );
    const dropped = index >= 0 && isBlank(rows.value[index].text);

    writeNotes();

    if (dropped) {
      keepFocus(index);
    }
  }

  function deleteNote(index) {
    const [removed] = rows.value.splice(index, 1);

    if (removed) {
      noteBoxes.delete(removed.key);
    }

    writeNotes();
    keepFocus(index);
  }

  async function addNote() {
    if (notesFull.value) {
      return;
    }

    const row = { key: (nextKey += 1), text: '', saved: null };

    rows.value.push(row);
    await nextTick();
    noteBoxes.get(row.key)?.focus();
  }

  // --- values that scroll ----------------------------------------------

  // The keys whose values are taller than their box, which then scrolls.
  const scrolling = ref(new Set());
  const boxes = new Map();
  let resizing = null;

  function measure() {
    const next = [...boxes]
      .filter(([, element]) => element.scrollHeight > element.clientHeight)
      .map(([key]) => key);

    if (
      next.length !== scrolling.value.size ||
      next.some((key) => !scrolling.value.has(key))
    ) {
      scrolling.value = new Set(next);
    }
  }

  function observe(key, element) {
    const previous = boxes.get(key);

    if (previous === element) {
      return;
    }

    if (previous) {
      resizing?.unobserve(previous);
      boxes.delete(key);
    }

    if (element) {
      boxes.set(key, element);
      resizing?.observe(element);
    }
  }

  onMounted(() => {
    if (typeof ResizeObserver !== 'undefined') {
      resizing = new ResizeObserver(measure);
      boxes.forEach((element) => resizing.observe(element));
    }

    measure();
  });

  onBeforeUnmount(() => resizing?.disconnect());
</script>

<style scoped>
  .inspector-diagram {
    font-size: 0.8rem;
  }

  .inspector-diagram h3 {
    font-weight: 700;
    font-size: 0.85rem;
    margin: 0.75rem 0 0.25rem;
  }

  .inspector-diagram p {
    margin: 0 0 0.35rem;
  }

  .inspector-diagram__label {
    font-weight: 600;
  }

  .inspector-diagram__none,
  .inspector-diagram__muted {
    color: var(--bx-text-muted);
  }

  /* Each entry is a key over its value, indented, which reads better in a
     narrow column than two columns would. */
  .inspector-diagram__list {
    margin: 0 0 0.4rem;
  }

  .inspector-diagram__list > div + div {
    margin-top: 0.3rem;
  }

  .inspector-diagram__list dt {
    font-weight: 600;
  }

  .inspector-diagram__list dd {
    margin: 0 0 0 0.6rem;
  }

  /* Values keep their line breaks and wrap anywhere; a long one scrolls
     in a box of about eight lines. */
  .inspector-diagram__value {
    max-height: 8.5rem;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .inspector-diagram__hosts,
  .inspector-diagram__detail {
    overflow-wrap: anywhere;
  }

  .inspector-diagram__scenarios {
    margin: 0 0 0.4rem;
    padding: 0;
    list-style: none;
  }

  .inspector-diagram__scenarios > li + li {
    margin-top: 0.5rem;
  }

  /* A note is its text box over its Delete button; its text keeps its line
     breaks when the draft is read only. */
  .inspector-diagram__notes {
    margin: 0 0 0.4rem;
    padding: 0;
    list-style: none;
  }

  .inspector-diagram__notes > li + li {
    margin-top: 0.5rem;
  }

  .inspector-diagram__note {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.25rem;
  }

  .inspector-diagram__note-box {
    width: 100%;
    min-height: 3.5rem;
    font-size: 0.8rem;
    resize: vertical;
  }

  .inspector-diagram__note-text {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  /* The message under a box whose text cannot be saved, which also has the
     Inspector's thicker border for aria-invalid: a cross before the words,
     so color is not the only sign. */
  .inspector-diagram p.inspector-diagram__note-error {
    display: flex;
    align-items: baseline;
    gap: 0.25rem;
    margin: 0;
    color: var(--bx-danger);
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .inspector-diagram__value[tabindex] {
    padding: 0.15rem 0.3rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: var(--bx-radius);
    background: var(--bx-bg-alt);
  }
</style>
