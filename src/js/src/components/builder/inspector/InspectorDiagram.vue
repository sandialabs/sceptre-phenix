<!--
  The rest of the Inspector's Diagram section. Details is not a live region,
  because the save state announces each save already.
-->
<template>
  <inspector-template-links />

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
      :aria-describedby="
        overrideNote
          ? 'inspector-icon-size-help inspector-icon-size-override'
          : 'inspector-icon-size-help'
      "
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
    <p
      v-if="overrideNote"
      id="inspector-icon-size-override"
      class="inspector-diagram__none"
      data-testid="inspector-icon-size-override">
      {{ overrideNote }}
    </p>
  </div>

  <div class="inspector-diagram" data-testid="inspector-grid-size">
    <h3 id="inspector-grid-size-label">Grid size</h3>
    <p v-if="store.readOnly" data-testid="inspector-grid-size-value">
      {{ gridSize }} pixels
    </p>
    <template v-else>
      <input
        ref="gridSizeField"
        type="text"
        inputmode="numeric"
        :value="gridSizeText"
        aria-labelledby="inspector-grid-size-label"
        :aria-invalid="gridSizeError ? 'true' : undefined"
        :aria-describedby="
          gridSizeError
            ? 'inspector-grid-size-help inspector-grid-size-error'
            : 'inspector-grid-size-help'
        "
        data-testid="inspector-grid-size-input"
        @input="gridSizeText = $event.target.value"
        @change="commitGridSize($event.target)"
        @keydown.enter="commitGridSize($event.target)" />
      <p
        v-if="gridSizeError"
        id="inspector-grid-size-error"
        class="inspector-diagram__error"
        role="alert"
        data-testid="inspector-grid-size-error">
        <builder-icon name="close" :size="14" />
        <span>{{ gridSizeError }}</span>
      </p>
    </template>
    <p id="inspector-grid-size-help" class="inspector-diagram__none">
      The distance between the lines of the grid, from {{ MIN_GRID_SIZE }} to
      {{ MAX_GRID_SIZE }} pixels. When Snap to grid is on in Builder settings,
      nodes go to this grid.
    </p>
  </div>

  <div class="inspector-diagram" data-testid="inspector-layers">
    <h3>Layers</h3>
    <p id="inspector-layers-help" class="inspector-diagram__none">
      {{ layersSummary }}
    </p>
    <ul class="inspector-diagram__layers" data-testid="inspector-layers-list">
      <li v-for="layer in layerList" :key="layer.id">
        <layer-name :layer="layer" :icons="store.doc?.icons || null" />
      </li>
    </ul>
    <p
      v-if="linkSummary"
      id="inspector-layers-link"
      class="inspector-diagram__none"
      data-testid="inspector-layers-link">
      {{ linkSummary }}
    </p>
    <div v-if="!store.readOnly" class="inspector-diagram__buttons">
      <button
        v-if="store.layerSetLink.status === 'update'"
        type="button"
        class="builder-button"
        aria-describedby="inspector-layers-link"
        data-testid="inspector-layers-update"
        @click="updateLayers">
        <builder-icon name="refresh" :size="14" />
        Update from layer set
      </button>
      <button
        type="button"
        class="builder-button"
        aria-haspopup="dialog"
        aria-describedby="inspector-layers-help"
        data-testid="inspector-layers-edit"
        @click="$emit('layers')">
        <builder-icon name="layer-group" :size="14" />
        Edit layers…
      </button>
    </div>
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
            v-fit-text
            class="inspector-diagram__note-box"
            rows="2"
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

  <div class="inspector-diagram" data-testid="inspector-scenario">
    <h3>Scenarios</h3>
    <p v-if="!scenarioRows.length" class="inspector-diagram__none">
      No scenarios.
    </p>
    <ul v-else class="inspector-diagram__scenarios">
      <li
        v-for="(row, index) in scenarioRows"
        :key="row.name"
        :data-testid="`inspector-scenario-${index + 1}`">
        <!-- A sub-heading for each scenario, with the counts of its apps
             once they are read. -->
        <h4
          class="inspector-diagram__scenario"
          data-testid="inspector-scenario-name">
          Scenario {{ row.name }}
          <span
            v-if="row.counts"
            class="inspector-diagram__counts"
            data-testid="inspector-scenario-counts"
            >({{ row.counts }})</span
          >
        </h4>
        <p v-if="row.note" class="inspector-diagram__none">{{ row.note }}</p>
        <!-- Each app over its hosts. An app that is disabled or has no
             hosts says so beside its name. -->
        <ul
          v-else-if="row.apps.length"
          class="inspector-diagram__apps"
          data-testid="inspector-scenario-apps">
          <li
            v-for="(app, at) in row.apps"
            :key="`${at}-${app.name}`"
            data-testid="inspector-scenario-app">
            <span
              class="inspector-diagram__app"
              data-testid="inspector-scenario-app-name">
              {{ app.name }}
              <span v-if="scenarioAppNote(app)" class="inspector-diagram__muted"
                >({{ scenarioAppNote(app) }})</span
              >
            </span>
            <span
              v-if="app.hosts.length"
              class="inspector-diagram__hosts"
              data-testid="inspector-scenario-hosts"
              >{{ app.hosts.join(', ') }}</span
            >
          </li>
        </ul>
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
  import InspectorTemplateLinks from './InspectorTemplateLinks.vue';
  import LayerName from '../layers/LayerName.vue';
  import { vFitText } from './fitText.js';
  import { useGridSizeField } from './gridSizeField.js';
  import { heldCommit, keyEffect } from './heldCommit.js';

  import { count } from '@/builder/announce.js';
  import { formatTimestamp } from '@/builder/format.js';
  import {
    diagramNoteProblem,
    documentIconSize,
    ICON_SIZE_PIXELS,
    ICON_SIZES,
    MAX_DIAGRAM_NOTES,
    MAX_GRID_SIZE,
    MIN_GRID_SIZE,
    documentScenarios,
    effectiveLayers,
    layerSetOptions,
    metadataOf,
    scenarioApps,
    sourceAnnotations,
  } from '@/builder/model.js';
  import {
    scenarioAppNote,
    scenarioCounts,
  } from '@/builder/scenarioSummary.js';
  import { ICON_SIZE_TITLES } from '@/builder/schema.js';
  import { iconSizeOverrideNote } from '@/builder/settings.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { isBlank } from '@/builder/text.js';

  defineEmits(['scenario', 'layers']);

  const store = useBuilderStore();

  // --- layers ------------------------------------------------------------

  const layersSummary = computed(() => {
    const placeholders = !store.doc?.layers?.length;
    const total = effectiveLayers(store.doc).length;

    return placeholders
      ? `This diagram has ${total} placeholder layers, Layer 0 to Layer ${total - 1}. Devices and switches can be at a layer.`
      : `This diagram has ${count(total, 'layer')}. Devices and switches can be at a layer.`;
  });

  const layerList = computed(() => effectiveLayers(store.doc));

  // How the layers stand to the layer set they were copied from.
  const linkSummary = computed(() => {
    const { status, set, source } = store.layerSetLink;
    const name =
      set?.name || layerSetOptions(store.doc).name || source?.id || '';

    switch (status) {
      case 'linked':
      case 'unknown':
        return `From layer set ${name}, version ${source.version}.`;
      case 'changed':
        return `From layer set ${name}, version ${source.version}, with changes in this diagram.`;
      case 'update':
        return `Layer set ${name} has a newer version, ${set.version}. This diagram has version ${source.version}.`;
      case 'missing':
        return `From layer set ${name}, which the server does not have now.`;
      default:
        return '';
    }
  });

  // Update from layer set. The button goes, so focus goes to Edit layers….
  async function updateLayers() {
    store.updateFromLayerSet();
    await nextTick();
    document.querySelector('[data-testid="inspector-layers-edit"]')?.focus();
  }

  // --- icon size ---------------------------------------------------------

  const iconSize = computed(() => documentIconSize(store.doc));
  const overrideNote = computed(() => iconSizeOverrideNote());
  const iconSizeSelect = ref(null);

  // A size the store refuses (while a conflict is resolved, or in a draft
  // that turned read only) leaves the document's size as it was, and so the
  // select's binding too, which then would not put it back. So this gives
  // the select the document's size again.
  function commitIconSize(size) {
    const entry = store.setIconSize(size);

    if (!entry && iconSizeSelect.value) {
      iconSizeSelect.value.value = iconSize.value;
    }

    return Boolean(entry);
  }

  const iconSizeChoice = heldCommit(commitIconSize);

  // A key that steps the select holds the size it steps to. Any other key,
  // such as Enter or a shortcut, applies the size held.
  function onIconSizeKey(event) {
    const effect = keyEffect(event);

    if (effect === 'hold') {
      iconSizeChoice.key();
    } else if (effect === 'flush') {
      iconSizeChoice.flush();
    }
  }

  onBeforeUnmount(() => iconSizeChoice.flush());

  // --- grid size ---------------------------------------------------------

  const {
    gridSize,
    gridSizeText,
    gridSizeField,
    gridSizeError,
    commitGridSize,
  } = useGridSizeField(store);

  // A user and a time the document holds, as a row: the time in the
  // viewer's locale, then "by" the user. Either may be missing. With
  // neither, there is no row.
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
      'The Builder does not show the apps of this scenario, because your role cannot read it.',
    missing: 'The server has no scenario of this name.',
    invalid: 'The server has no scenario of this name.',
  };

  // Each listed scenario with the apps to list and their counts, or what
  // to say instead of them.
  const scenarioRows = computed(() =>
    scenarioNames.value.map((name) => {
      const read = store.storedScenarios[name];

      if (read?.content) {
        const apps = scenarioApps(read.content);

        return { name, apps, counts: scenarioCounts(apps), note: '' };
      }

      if (!read || read.loading) {
        return { name, apps: [], counts: '', note: 'Reading its apps…' };
      }

      return {
        name,
        apps: [],
        counts: '',
        note: PROBLEMS[read.problem] || 'The Builder could not read its apps.',
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
  // while the row's text cannot be written. A row added and not written yet
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

  // Writes the rows into the diagram as one undo step and always drops blank
  // rows. A row whose text cannot be written keeps its box and its error.
  function writeNotes() {
    store.setDiagramNotes(
      rows.value.map(noteOf).filter((note) => note !== null),
    );
    rows.value = rows.value.filter((row) => !isBlank(row.text));
  }

  // A box left empty takes its row, and the focus may have been on that
  // row's Delete button. A box left with text keeps its row, and the focus
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

  /* As the other headings of the Inspector (see BuilderInspector.vue). */
  .inspector-diagram h3 {
    font-weight: 700;
    font-size: 0.95rem;
    color: var(--bx-text);
    margin: 0.75rem 0 0.25rem;
  }

  .inspector-diagram p {
    margin: 0 0 0.35rem;
  }

  .inspector-diagram__layers {
    margin: 0 0 0.35rem;
    padding: 0;
    list-style: none;
  }

  .inspector-diagram__layers li + li {
    margin-top: 0.1rem;
  }

  .inspector-diagram__buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
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

  /* Values keep their line breaks and wrap anywhere. A long value scrolls
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
    margin-top: 0.75rem;
  }

  /* A scenario's name is a sub-heading: larger and bolder than the text
     under it, with a bar at its side that shows in every theme, forced
     colors too. Its counts are plain text. */
  .inspector-diagram h4.inspector-diagram__scenario {
    margin: 0 0 0.35rem;
    padding: 0.1rem 0 0.1rem 0.45rem;
    border-left: 3px solid var(--bx-accent);
    font-weight: 700;
    font-size: 0.9rem;
    color: var(--bx-text);
    overflow-wrap: anywhere;
  }

  .inspector-diagram__counts {
    font-weight: 400;
  }

  /* Each app is its name over its hosts, indented. */
  .inspector-diagram__apps {
    margin: 0 0 0.4rem;
    padding: 0;
    list-style: none;
  }

  .inspector-diagram__apps > li + li {
    margin-top: 0.3rem;
  }

  .inspector-diagram__app {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .inspector-diagram__app .inspector-diagram__muted {
    font-weight: 400;
  }

  .inspector-diagram__apps .inspector-diagram__hosts {
    display: block;
    margin-left: 0.6rem;
  }

  /* A note is its text box over its Delete button. Its text keeps its line
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
    font-size: 0.8rem;
  }

  .inspector-diagram__note-text {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  /* The message under a box whose text cannot be saved, which also has the
     Inspector's thicker border for aria-invalid: a cross before the words,
     so color is not the only sign. */
  .inspector-diagram p.inspector-diagram__note-error,
  .inspector-diagram p.inspector-diagram__error {
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
