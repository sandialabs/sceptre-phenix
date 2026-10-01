<!--
  Export shell: builder documents (JSON/YAML), the phenix Topology config
  (Topology YAML), PNG/SVG images and a Gephi (GEXF) graph.

  Topology YAML comes from the server, which owns that conversion, so the file
  is the config Publish would write; a client rendering could disagree with
  it. What keeps the topology from being published yet is said after the
  download.

  Image exports always cover the whole diagram (all node bounds), not just the
  part currently visible on screen.

  Gephi (GEXF) is the network as a graph for analysis in Gephi, built here
  from the diagram (see gexf.js). It is not a format the Builder imports. A
  stored scenario is read from the server first, for the apps each device
  runs; when it cannot be read, the file leaves the apps out and the status
  says why.

  The Inspector's unapplied edits are saved before the dialog opens (see
  leave.js). Edits it cannot apply keep every export from being made,
  rather than being left out, and the dialog says so as it opens.
-->
<template>
  <builder-dialog
    title="Export diagram"
    title-id="export-dialog-title"
    :describedby="unapplied ? 'export-unapplied' : ''"
    @close="$emit('close')">
    <p
      v-if="unapplied"
      id="export-unapplied"
      class="builder-field builder-dialog__error"
      data-testid="export-unapplied">
      {{ unappliedBlock(unapplied, 'exported') }}
    </p>
    <p class="builder-field">
      Diagram bounds: {{ bounds.width }} × {{ bounds.height }} px
    </p>

    <div class="builder-export__actions">
      <button
        type="button"
        class="builder-button"
        data-testid="export-json"
        :disabled="Boolean(unapplied)"
        @click="exportText('json')">
        <builder-icon name="download" :size="14" />
        Builder JSON
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="export-yaml"
        :disabled="Boolean(unapplied)"
        @click="exportText('yaml')">
        <builder-icon name="download" :size="14" />
        Builder YAML
      </button>
      <!-- While the server makes the file, a turning ring in place of the
           icon; reduced motion stops it turning (see builder.css). -->
      <button
        type="button"
        class="builder-button"
        data-testid="export-topology-yaml"
        aria-describedby="export-topology-hint"
        :disabled="Boolean(unapplied)"
        :aria-disabled="topologyBusy ? 'true' : undefined"
        :aria-busy="topologyBusy || undefined"
        @click="exportTopologyYAML">
        <span
          v-if="topologyBusy"
          class="builder-toolbar__spinner"
          aria-hidden="true"></span>
        <builder-icon v-else name="download" :size="14" />
        Topology YAML
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="export-png"
        :disabled="Boolean(unapplied)"
        :aria-disabled="busy ? 'true' : undefined"
        @click="exportImageAs('png')">
        <builder-icon name="image" :size="14" />
        PNG
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="export-svg"
        :disabled="Boolean(unapplied)"
        :aria-disabled="busy ? 'true' : undefined"
        @click="exportImageAs('svg')">
        <builder-icon name="image" :size="14" />
        SVG
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="export-gexf"
        aria-describedby="export-gexf-hint"
        :disabled="Boolean(unapplied)"
        :aria-disabled="busy ? 'true' : undefined"
        @click="exportGEXF">
        <builder-icon name="download" :size="14" />
        Gephi (GEXF)
      </button>
    </div>
    <p id="export-topology-hint" class="builder-hint">
      Topology YAML is the phenix Topology config Publish would write.
    </p>
    <p id="export-gexf-hint" class="builder-hint">
      Gephi (GEXF): the devices, networks and connections, with their settings
      and scenario apps, as a graph to analyze in Gephi. Builder v2 cannot open
      it.
    </p>

    <p class="builder-dialog__message" role="status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>
    <p class="builder-dialog__message builder-dialog__error" role="alert">
      <span v-if="error.text" :key="error.key" data-testid="export-error">{{
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
  import { computed, ref } from 'vue';
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
    saveText,
    saveTopologyYAML,
    toJSONString,
    toYAMLString,
  } from '@/builder/exporters.js';
  import { count, listOf } from '@/builder/announce.js';
  import { GEXF_MIME, toGEXF } from '@/builder/gexf.js';
  import { unappliedBlock } from '@/builder/leave.js';
  import { storedScenarioName } from '@/builder/model.js';
  import { useBuilderStore } from '@/builder/store.js';

  const props = defineProps({
    viewportElement: { type: Function, default: () => null },
    // The Inspector's edits that could not be applied (see leave.js).
    unapplied: { type: Object, default: null },
  });

  defineEmits(['close']);

  const store = useBuilderStore();
  const busy = ref(false);
  const topologyBusy = ref(false);
  const status = useMessage();
  const error = useMessage();

  const bounds = computed(() => documentBounds(store.doc));

  function exportText(kind) {
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

  async function exportTopologyYAML() {
    // A busy button keeps focus, so it can still be pressed.
    if (topologyBusy.value) {
      return;
    }

    error.clear();
    status.set('Exporting Topology YAML…');
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
          ? `Could not export Topology YAML. ${err.message}`
          : store.describeError(err, 'export Topology YAML'),
      );
    } finally {
      topologyBusy.value = false;
    }
  }

  async function exportImageAs(format) {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    error.clear();
    status.set(`Exporting ${format.toUpperCase()}…`);
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
      });

      status.set(`Saved ${exportFileName(store.doc, format)}.`);
    } catch (err) {
      status.clear();
      error.set(`Image export failed: ${err.message}`);
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
  // change: this tab's, else the server's. A stored scenario is read again
  // first, for the apps each device runs.
  async function exportGEXF() {
    // A busy button keeps focus, so it can still be pressed.
    if (busy.value) {
      return;
    }

    error.clear();

    const doc = store.doc;
    const modified = store.saveState.changedAt || store.draftRecord.updated;
    const fileName = exportFileName(doc, 'gexf');
    const stored = storedScenarioName(doc.scenario);
    let scenario = { content: doc.scenario?.content ?? null, problem: '' };

    if (stored) {
      status.set('Exporting GEXF…');
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
      error.set(`GEXF export failed: ${err.message}`);
    }
  }
</script>

<style scoped>
  .builder-export__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
  }
</style>
