<!--
  Download diagram: builder documents (JSON/YAML), the phenix Topology config
  (Topology YAML), PNG/SVG images and a Gephi (GEXF) graph.

  A Builder JSON or YAML file carries a copy of every custom icon the
  diagram uses, taken from the server's icon library, so the file stands on
  its own; an icon the library no longer has is left out, and the status
  says which.

  Topology YAML comes from the server, which owns that conversion, so the file
  is the config Publish would write; a client rendering could disagree with
  it. What keeps the topology from being published yet is said after the
  download.

  Images always cover the whole diagram (all node bounds), not just the part
  currently visible on screen.

  Gephi (GEXF) is the network as a graph for analysis in Gephi, built here
  from the diagram (see gexf.js). It is not a format the Builder imports. The
  scenarios the diagram lists are read from the server first, for the apps
  each device runs; when one cannot be read, the file leaves the apps out
  and the status says why.

  The buttons are in two rows: the documents and the config (Builder JSON,
  Builder YAML, Topology YAML), then the pictures and the graph (PNG, SVG,
  Gephi (GEXF)). Each row wraps on its own in a narrow dialog.

  Builder package, below them, comes from the server (POST /builder/package,
  see package.js): one JSON or YAML file with the diagram and the lists of
  what it needs, carrying also the sections the user ticks, none by
  default. What the package names but does not carry (a scenario the user's
  role cannot read, an icon the server lacks) is listed before the file is
  saved, and the user saves it anyway or does not.

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
        ><builder-icon name="external-link" :size="12" /></a
      >. The Builder cannot open it.
    </p>

    <fieldset
      class="builder-field builder-download__package"
      aria-describedby="download-package-hint">
      <legend>Builder package</legend>
      <p id="download-package-hint" class="builder-hint">
        One file with the diagram and the lists of what it needs on a phenix
        server: scenarios, included topologies, templates, custom icons, apps
        and files. Tick what the file also carries. It never carries file
        contents, scripts or apps.
      </p>
      <div class="builder-download__ticks">
        <label
          v-for="section in PACKAGE_SECTIONS"
          :key="section.id"
          class="builder-choice">
          <input
            v-model="packageSections"
            type="checkbox"
            :value="section.id"
            :data-testid="`download-package-${section.id}`" />
          {{ section.label }}
        </label>
      </div>
      <div class="builder-download__package-actions">
        <label for="download-package-format">Package format</label>
        <select
          id="download-package-format"
          v-model="packageFormat"
          data-testid="download-package-format">
          <option value="json">JSON</option>
          <option value="yaml">YAML</option>
        </select>
        <button
          ref="packageButton"
          type="button"
          class="builder-button"
          data-testid="download-package"
          :disabled="Boolean(unapplied)"
          :aria-disabled="packageBusy ? 'true' : undefined"
          :aria-busy="packageBusy || undefined"
          @click="downloadPackage">
          <span
            v-if="packageBusy"
            class="builder-toolbar__spinner"
            aria-hidden="true"></span>
          <builder-icon v-else name="download" :size="14" />
          Builder package
        </button>
      </div>
    </fieldset>

    <div
      v-if="packageHeld"
      class="builder-download__held"
      data-testid="download-package-held">
      <p id="download-package-held-summary">{{ packageHeldSummary }}</p>
      <ul
        id="download-package-warnings"
        class="builder-issues"
        data-testid="download-package-warnings">
        <li
          v-for="(warning, index) in packageHeld.warnings"
          :key="index"
          data-level="warning"
          data-testid="download-package-warning">
          <strong>Warning:</strong>
          {{ warning.message }}
          <code
            v-if="warning.code"
            class="builder-download__code"
            data-testid="issue-code"
            >{{ warning.code }}</code
          >
        </li>
      </ul>
      <div class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="download-package-discard"
          @click="discardPackage">
          Do not save
        </button>
        <button
          ref="packageSaveButton"
          type="button"
          class="builder-button builder-button--primary"
          aria-describedby="download-package-held-summary download-package-warnings"
          data-testid="download-package-save"
          @click="savePackage">
          Save package
        </button>
      </div>
    </div>

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
  import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
  import { saveAs } from 'file-saver';
  import { toPng, toSvg } from 'html-to-image';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import { useMessage } from './message.js';

  import {
    describeTopologyExport,
    documentBounds,
    downloadDocument,
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
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { unappliedBlock } from '@/builder/leave.js';
  import { toIssue } from '@/builder/issues.js';
  import { documentScenarios } from '@/builder/model.js';
  import { PACKAGE_SECTIONS, packageFileName } from '@/builder/package.js';
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

  // The Builder package: the sections ticked (none by default), its format,
  // and a package held, with its warnings, until the user saves it or not.
  const packageSections = ref([]);
  const packageFormat = ref('json');
  const packageBusy = ref(false);
  const packageHeld = ref(null);
  const packageButton = ref(null);
  const packageSaveButton = ref(null);

  const packageHeldSummary = computed(() => {
    const warnings = packageHeld.value?.warnings.length || 0;

    return `The package names ${count(warnings, 'thing')} it does not carry. Save it anyway?`;
  });

  // Whether the dialog was closed. A package the server sends after that is
  // not saved.
  let closed = false;
  onBeforeUnmount(() => {
    closed = true;
  });

  // An image holds the node notes the canvas shows (see NodeNotes.vue).
  const bounds = computed(() =>
    documentBounds(store.doc, IMAGE_PADDING, {
      showNotes: builderSettings.showNodeNotes,
    }),
  );

  // A Builder JSON or YAML file carries every custom icon the diagram uses
  // (see downloadDocument), from the server's icon library, which is read
  // first unless it was read already.
  async function downloadText(kind) {
    error.clear();

    await iconLibrary.ensure().catch(() => {});

    const { doc, note } = downloadDocument(store.doc, iconLibrary);
    const map = {
      json: {
        text: toJSONString(doc),
        mime: 'application/json',
        ext: 'json',
      },
      yaml: { text: toYAMLString(doc), mime: 'text/yaml', ext: 'yaml' },
    }[kind];

    saveText({
      text: map.text,
      mime: map.mime,
      fileName: exportFileName(doc, map.ext),
      saveAs,
    });

    status.set(
      [`Saved ${exportFileName(doc, map.ext)}.`, note]
        .filter(Boolean)
        .join(' '),
    );
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

  // Saves a package the server made as a file of the chosen format.
  function savePackageFile(pkg, format) {
    const fileName = packageFileName(store.doc, format);

    saveText({
      text: format === 'yaml' ? toYAMLString(pkg) : toJSONString(pkg),
      mime: format === 'yaml' ? 'text/yaml' : 'application/json',
      fileName,
      saveAs,
    });

    status.set(`Saved ${fileName}.`);
  }

  // The Builder package of the diagram, with the sections ticked. When it
  // names something it does not carry, the warnings are shown and the file
  // is saved only when the user says so.
  async function downloadPackage() {
    // A busy button keeps focus, so it can still be pressed.
    if (packageBusy.value) {
      return;
    }

    error.clear();
    packageHeld.value = null;
    status.set('Making the Builder package…');
    packageBusy.value = true;

    const format = packageFormat.value;

    try {
      const built = await store.buildPackage(store.doc, [
        ...packageSections.value,
      ]);

      if (closed) {
        return;
      }

      if (built.warnings.length) {
        status.clear();
        // Each warning is an issue with its code; one the server sends as
        // plain text is shown as it is.
        packageHeld.value = {
          pkg: built.package,
          format,
          warnings: built.warnings.map((entry) => toIssue(entry, 'warning')),
        };
        await nextTick();
        packageSaveButton.value?.focus();

        return;
      }

      savePackageFile(built.package, format);
    } catch (err) {
      status.clear();
      error.set(
        err instanceof TypeError
          ? `Could not download the Builder package. ${err.message}`
          : store.describeError(err, 'download the Builder package'),
      );
    } finally {
      packageBusy.value = false;
    }
  }

  function savePackage() {
    const held = packageHeld.value;

    packageHeld.value = null;
    packageButton.value?.focus();

    if (held) {
      savePackageFile(held.pkg, held.format);
    }
  }

  function discardPackage() {
    packageHeld.value = null;
    packageButton.value?.focus();
    status.set('The Builder package was not saved.');
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
  // change (see lastModified). The scenarios the diagram lists are read
  // again first, for the apps each device runs; when one of them cannot be
  // read, the file lists no apps rather than some of them.
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
    const names = documentScenarios(doc);
    let reads = [];

    if (names.length) {
      status.set('Downloading GEXF…');
      busy.value = true;

      try {
        reads = await Promise.all(
          names.map((name) => store.readScenario(name)),
        );
      } finally {
        busy.value = false;
      }
    }

    const unread = reads.findIndex((read) => read.problem);
    const scenario =
      unread >= 0
        ? { name: names[unread], problem: reads[unread].problem }
        : { name: '', problem: '' };

    try {
      const graph = toGEXF(doc, {
        modified,
        scenarios: scenario.problem ? [] : reads.map((read) => read.content),
      });

      saveText({ text: graph.text, mime: GEXF_MIME, fileName, saveAs });

      const saved = `Saved ${fileName}: ${listOf([
        count(graph.devices, 'device'),
        count(graph.networks, 'network'),
        count(graph.connections, 'connection'),
      ])}.`;

      status.set(
        scenario.problem
          ? `${saved} It lists no scenario apps: ${scenarioProblem(scenario.name, scenario.problem)}.`
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

  /* The package's ticks, then its format and button, each a row that
     wraps on its own. */
  .builder-download__ticks,
  .builder-download__package-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 1rem;
  }

  .builder-download__package-actions {
    margin-top: 0.5rem;
  }

  .builder-download__held {
    margin-bottom: 0.75rem;
  }

  /* The code of a warning, in the warning's own color, as the lists of
     checks show it (see BuilderIssueList.vue). */
  .builder-download__code {
    padding: 0 0.3rem;
    border: 1px solid var(--bx-border);
    border-radius: var(--bx-radius);
    background: transparent;
    color: inherit;
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 0.75rem;
  }
</style>
