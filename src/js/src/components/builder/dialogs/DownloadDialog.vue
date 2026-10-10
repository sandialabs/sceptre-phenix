<!--
  Download diagram: builder documents (JSON/YAML), the phenix Topology config
  (Topology YAML), PNG/SVG images and a Gephi (GEXF) graph.

  Topology YAML comes from the server, which owns that conversion, so the file
  is the config Publish would write; a client rendering could disagree with
  it. What keeps the topology from being published yet is said after the
  download.

  Images always cover the whole diagram (all node bounds), not just the part
  currently visible on screen.

  Gephi (GEXF) is the network as a graph for analysis in Gephi, built here
  from the diagram (see gexf.js). It is not a format the Builder imports. A
  stored scenario is read from the server first, for the apps each device
  runs; when it cannot be read, the file leaves the apps out and the status
  says why.

  The buttons are in two rows: the documents and the config (Builder JSON,
  Builder YAML, Topology YAML), then the pictures and the graph (PNG, SVG,
  Gephi (GEXF)). Each row wraps on its own in a narrow dialog.

  A palette command that names a format (Download PNG) opens the dialog with
  `start`: that format's button takes focus and its download starts at once,
  so its result and its errors show here as they do after a press.

  The Inspector's unapplied edits are saved before the dialog opens (see
  leave.js). Edits it cannot apply keep every download from being made,
  rather than being left out, and the dialog says so as it opens.
-->
<template>
  <builder-dialog
    title="Download diagram"
    title-id="download-dialog-title"
    :describedby="unapplied ? 'download-unapplied' : ''"
    @close="$emit('close')">
    <p
      v-if="unapplied"
      id="download-unapplied"
      class="builder-field builder-dialog__error"
      data-testid="download-unapplied">
      {{ unappliedBlock(unapplied, 'downloaded') }}
    </p>
    <p class="builder-field">
      Diagram bounds: {{ bounds.width }} × {{ bounds.height }} px
    </p>

    <div class="builder-download__actions">
      <button
        :ref="formatButtons.json"
        type="button"
        class="builder-button"
        data-testid="download-json"
        :disabled="Boolean(unapplied)"
        @click="downloadText('json')">
        <builder-icon name="download" :size="14" />
        Builder JSON
      </button>
      <button
        :ref="formatButtons.yaml"
        type="button"
        class="builder-button"
        data-testid="download-yaml"
        :disabled="Boolean(unapplied)"
        @click="downloadText('yaml')">
        <builder-icon name="download" :size="14" />
        Builder YAML
      </button>
      <!-- While the server makes the file, a turning ring in place of the
           icon; reduced motion stops it turning (see builder.css). -->
      <button
        :ref="formatButtons.topology"
        type="button"
        class="builder-button"
        data-testid="download-topology-yaml"
        aria-describedby="download-topology-hint"
        :disabled="Boolean(unapplied)"
        :aria-disabled="topologyBusy ? 'true' : undefined"
        :aria-busy="topologyBusy || undefined"
        @click="downloadTopologyYAML">
        <span
          v-if="topologyBusy"
          class="builder-toolbar__spinner"
          aria-hidden="true"></span>
        <builder-icon v-else name="download" :size="14" />
        Topology YAML
      </button>
    </div>
    <div class="builder-download__actions">
      <button
        :ref="formatButtons.png"
        type="button"
        class="builder-button"
        data-testid="download-png"
        :disabled="Boolean(unapplied)"
        :aria-disabled="busy ? 'true' : undefined"
        @click="downloadImageAs('png')">
        <builder-icon name="image" :size="14" />
        PNG
      </button>
      <button
        :ref="formatButtons.svg"
        type="button"
        class="builder-button"
        data-testid="download-svg"
        :disabled="Boolean(unapplied)"
        :aria-disabled="busy ? 'true' : undefined"
        @click="downloadImageAs('svg')">
        <builder-icon name="image" :size="14" />
        SVG
      </button>
      <button
        :ref="formatButtons.gexf"
        type="button"
        class="builder-button"
        data-testid="download-gexf"
        aria-describedby="download-gexf-hint"
        :disabled="Boolean(unapplied)"
        :aria-disabled="busy ? 'true' : undefined"
        @click="downloadGEXF">
        <builder-icon name="download" :size="14" />
        Gephi (GEXF)
      </button>
    </div>
    <p id="download-topology-hint" class="builder-hint">
      Topology YAML is the phenix Topology config Publish would write.
    </p>
    <!-- noreferrer keeps this server's address from the Gephi site. -->
    <p id="download-gexf-hint" class="builder-hint">
      Gephi (GEXF): the devices, networks and connections, with their settings
      and scenario apps, as a graph to analyze in
      <a
        class="builder-download__link"
        href="https://gephi.org/"
        target="_blank"
        rel="noopener noreferrer"
        data-testid="download-gephi-link"
        >Gephi<span class="builder-visually-hidden"> (opens in a new tab)</span
        ><builder-icon name="external" :size="12" /></a
      >. The Builder cannot open it.
    </p>

    <p class="builder-dialog__message" role="status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>
    <p class="builder-dialog__message builder-dialog__error" role="alert">
      <span v-if="error.text" :key="error.key" data-testid="download-error">{{
        error.text
      }}</span>
    </p>

    <div class="builder-dialog__actions">
      <button type="button" class="builder-button" @click="$emit('close')">
        Close
      </button>
    </div>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, ref } from 'vue';
  import { saveAs } from 'file-saver';
  import { toPng, toSvg } from 'html-to-image';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import { useMessage } from './message.js';

  import {
    describeTopologyExport,
    documentBounds,
    exportFileName,
    exportImage,
    IMAGE_PADDING,
    saveText,
    saveTopologyYAML,
    toJSONString,
    toYAMLString,
  } from '@/builder/exporters.js';
  import { count, listOf } from '@/builder/announce.js';
  import { GEXF_MIME, lastModified, toGEXF } from '@/builder/gexf.js';
  import { unappliedBlock } from '@/builder/leave.js';
  import { storedScenarioName } from '@/builder/model.js';
  import { builderSettings } from '@/builder/settings.js';
  import { useBuilderStore } from '@/builder/store.js';

  const props = defineProps({
    viewportElement: { type: Function, default: () => null },
    // The Inspector's edits that could not be applied (see leave.js).
    unapplied: { type: Object, default: null },
    // The format to download as the dialog opens: json, yaml, topology,
    // png, svg or gexf; '' to wait for a press.
    start: { type: String, default: '' },
  });

  defineEmits(['close']);

  const store = useBuilderStore();
  const busy = ref(false);
  const topologyBusy = ref(false);
  const status = useMessage();
  const error = useMessage();

  // An image holds the node notes the canvas shows (see NodeNotes.vue).
  const bounds = computed(() =>
    documentBounds(store.doc, IMAGE_PADDING, {
      showNotes: builderSettings.showNodeNotes,
    }),
  );

  function downloadText(kind) {
    error.clear();

    const map = {
      json: {
        text: toJSONString(store.doc),
        mime: 'application/json',
        ext: 'json',
      },
      yaml: { text: toYAMLString(store.doc), mime: 'text/yaml', ext: 'yaml' },
    }[kind];

    saveText({
      text: map.text,
      mime: map.mime,
      fileName: exportFileName(store.doc, map.ext),
      saveAs,
    });

    status.set(`Saved ${exportFileName(store.doc, map.ext)}.`);
  }

  async function downloadTopologyYAML() {
    // A busy button keeps focus, so it can still be pressed.
    if (topologyBusy.value) {
      return;
    }

    error.clear();
    status.set('Downloading Topology YAML…');
    topologyBusy.value = true;

    try {
      const saved = await saveTopologyYAML({
        doc: store.doc,
        exportTopology: store.exportTopology,
        saveAs,
      });

      status.set(describeTopologyExport(saved));
    } catch (err) {
      status.clear();
      error.set(
        err instanceof TypeError
          ? `Could not download Topology YAML. ${err.message}`
          : store.describeError(err, 'download Topology YAML'),
      );
    } finally {
      topologyBusy.value = false;
    }
  }

  async function downloadImageAs(format) {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    error.clear();
    status.set(`Downloading ${format.toUpperCase()}…`);
    busy.value = true;

    try {
      await exportImage({
        element: props.viewportElement(),
        doc: store.doc,
        format,
        toPng,
        toSvg,
        saveAs,
        backgroundColor: store.resolvedTheme === 'dark' ? '#12171f' : '#ffffff',
        showNotes: builderSettings.showNodeNotes,
      });

      status.set(`Saved ${exportFileName(store.doc, format)}.`);
    } catch (err) {
      status.clear();
      error.set(`Image download failed: ${err.message}`);
    } finally {
      busy.value = false;
    }
  }

  // Why a stored scenario's apps are not in the file (see classifyError).
  function scenarioProblem(name, problem) {
    if (problem === 'forbidden') {
      return `your role cannot read scenario ${name}`;
    }

    if (problem === 'missing' || problem === 'invalid') {
      return `the server has no scenario ${name}`;
    }

    return `scenario ${name} could not be read`;
  }

  // The network as a GEXF graph (see gexf.js), dated by the diagram's last
  // change (see lastModified). A stored scenario is read again first, for
  // the apps each device runs.
  async function downloadGEXF() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    error.clear();

    const doc = store.doc;
    const modified = lastModified({
      changedAt: store.saveState.changedAt,
      doc,
      updated: store.draftRecord.updated,
    });
    const fileName = exportFileName(doc, 'gexf');
    const stored = storedScenarioName(doc.scenario);
    let scenario = { content: doc.scenario?.content ?? null, problem: '' };

    if (stored) {
      status.set('Downloading GEXF…');
      busy.value = true;

      try {
        scenario = await store.readScenario(stored);
      } finally {
        busy.value = false;
      }
    }

    try {
      const graph = toGEXF(doc, { modified, scenario: scenario.content });

      saveText({ text: graph.text, mime: GEXF_MIME, fileName, saveAs });

      const saved = `Saved ${fileName}: ${listOf([
        count(graph.devices, 'device'),
        count(graph.networks, 'network'),
        count(graph.connections, 'connection'),
      ])}.`;

      status.set(
        scenario.problem
          ? `${saved} It lists no scenario apps: ${scenarioProblem(stored, scenario.problem)}.`
          : saved,
      );
    } catch (err) {
      status.clear();
      error.set(`GEXF download failed: ${err.message}`);
    }
  }

  // What each format's button does, by the name a palette command starts
  // it with.
  const FORMATS = {
    json: () => downloadText('json'),
    yaml: () => downloadText('yaml'),
    topology: downloadTopologyYAML,
    png: () => downloadImageAs('png'),
    svg: () => downloadImageAs('svg'),
    gexf: downloadGEXF,
  };

  // Each format's button, for the one a command starts.
  const buttons = {};
  const formatButtons = Object.fromEntries(
    Object.keys(FORMATS).map((format) => [
      format,
      (element) => {
        buttons[format] = element;
      },
    ]),
  );

  // A command's format starts once the dialog is drawn, from its button,
  // which takes focus as if it had been pressed. With edits the Inspector
  // cannot apply nothing starts: the dialog says why, as for a press.
  onMounted(async () => {
    const download = Object.hasOwn(FORMATS, props.start)
      ? FORMATS[props.start]
      : null;

    if (!download || props.unapplied) {
      return;
    }

    await nextTick();
    buttons[props.start]?.focus();
    download();
  });
</script>

<style scoped>
  /* Two rows of buttons, each wrapping on its own. */
  .builder-download__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
  }

  .builder-download__actions + .builder-download__actions {
    margin-bottom: 0.75rem;
  }

  /* Underlined, so the link is not told from the text around it by color
     alone. */
  .builder-download__link,
  .builder-download__link:hover {
    color: var(--bx-accent);
    text-decoration: underline;
  }

  .builder-download__link .builder-icon {
    margin-inline-start: 0.2em;
    vertical-align: -0.1em;
  }
</style>
