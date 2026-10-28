<!-- Download diagram: Builder JSON and YAML, Topology YAML (from the server,
     which owns that conversion), PNG, SVG, draw.io, Gephi and a Builder package.
     Images cover the whole diagram. Inspector edits that cannot apply stop it. -->
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

    <!-- While a format is made, a ring replaces its icon, and the other
         formats wait (see builder/busy.js). -->
    <div class="builder-download__actions">
      <builder-busy-button
        :ref="formatButtons.json"
        icon="download"
        data-testid="download-json"
        :busy="task === 'json'"
        :locked="Boolean(unapplied)"
        :disabled="waits('json')"
        @click="downloadText('json')">
        Builder JSON
      </builder-busy-button>
      <builder-busy-button
        :ref="formatButtons.yaml"
        icon="download"
        data-testid="download-yaml"
        :busy="task === 'yaml'"
        :locked="Boolean(unapplied)"
        :disabled="waits('yaml')"
        @click="downloadText('yaml')">
        Builder YAML
      </builder-busy-button>
      <builder-busy-button
        :ref="formatButtons.topology"
        icon="download"
        data-testid="download-topology-yaml"
        :busy="task === 'topology'"
        :locked="Boolean(unapplied)"
        :disabled="waits('topology')"
        @click="downloadTopologyYAML">
        Topology YAML
      </builder-busy-button>
    </div>
    <div class="builder-download__actions">
      <builder-busy-button
        :ref="formatButtons.png"
        icon="image"
        data-testid="download-png"
        :busy="task === 'png'"
        :locked="Boolean(unapplied)"
        :disabled="waits('png')"
        @click="downloadImageAs('png')">
        PNG
      </builder-busy-button>
      <builder-busy-button
        :ref="formatButtons.svg"
        icon="image"
        data-testid="download-svg"
        :busy="task === 'svg'"
        :locked="Boolean(unapplied)"
        :disabled="waits('svg')"
        @click="downloadImageAs('svg')">
        SVG
      </builder-busy-button>
      <builder-busy-button
        :ref="formatButtons.drawio"
        icon="download"
        data-testid="download-drawio"
        aria-describedby="download-drawio-hint"
        :busy="task === 'drawio'"
        :locked="Boolean(unapplied)"
        :disabled="waits('drawio')"
        @click="downloadDrawio">
        draw.io
      </builder-busy-button>
      <builder-busy-button
        :ref="formatButtons.gexf"
        icon="download"
        data-testid="download-gexf"
        aria-describedby="download-gexf-hint"
        :busy="task === 'gexf'"
        :locked="Boolean(unapplied)"
        :disabled="waits('gexf')"
        @click="downloadGEXF">
        Gephi (GEXF)
      </builder-busy-button>
    </div>
    <!-- noreferrer keeps this server's address from the other sites. -->
    <p id="download-drawio-hint" class="builder-hint">
      draw.io: the diagram, in the light theme's colors, as a drawing to edit in
      <a
        class="builder-download__link"
        href="https://www.drawio.com/"
        target="_blank"
        rel="noopener noreferrer"
        data-testid="download-drawio-link"
        >draw.io<span class="builder-visually-hidden">
          (opens in a new tab)</span
        ><builder-icon name="external-link" :size="12" /></a
      >.
    </p>
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
      >.
    </p>

    <fieldset
      class="builder-field builder-download__package"
      aria-describedby="download-package-hint">
      <legend>Builder package</legend>
      <p id="download-package-hint" class="builder-hint">
        The package is one file. It holds the diagram and lists what the diagram
        needs on a phenix server: scenarios, included topologies, templates,
        custom icons, apps and files. Tick what the file also carries. It never
        carries file contents, scripts or apps.
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
        <builder-busy-button
          ref="packageButton"
          icon="download"
          data-testid="download-package"
          :busy="task === 'package'"
          :locked="Boolean(unapplied)"
          :disabled="waits('package')"
          @click="downloadPackage">
          Builder package
        </builder-busy-button>
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

  import BuilderBusyButton from '../BuilderBusyButton.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import { useMessage } from './message.js';

  import {
    describeTopologyExport,
    documentBounds,
    downloadDocument,
    exportFileName,
    exportImage,
    pictureIconNote,
    saveText,
    saveTopologyYAML,
    toJSONString,
    toYAMLString,
  } from '@/builder/exporters.js';
  import { count, listOf } from '@/builder/announce.js';
  import { busyTask, runBusy } from '@/builder/busy.js';
  import { DRAWIO_MIME, toDrawio } from '@/builder/drawio.js';
  import { GEXF_MIME, lastModified, toGEXF } from '@/builder/gexf.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { iconRefs } from '@/builder/icons.js';
  import { unappliedBlock } from '@/builder/leave.js';
  import { toIssue } from '@/builder/issues.js';
  import { documentScenarios } from '@/builder/model.js';
  import { PACKAGE_SECTIONS, packageFileName } from '@/builder/package.js';
  import { builderSettings, iconSizeOverride } from '@/builder/settings.js';
  import { useBuilderStore } from '@/builder/store.js';

  const props = defineProps({
    viewportElement: { type: Function, default: () => null },
    // The Inspector's edits that could not be applied (see leave.js).
    unapplied: { type: Object, default: null },
    // The format to download as the dialog opens: json, yaml, topology,
    // png, svg, drawio or gexf. '' waits for a press.
    start: { type: String, default: '' },
  });

  defineEmits(['close']);

  const store = useBuilderStore();
  const status = useMessage();
  const error = useMessage();

  // One format is made at a time: the task, a key of PREPARING, or ''.
  const DOWNLOADS = 'download';
  const task = computed(() => busyTask(DOWNLOADS));

  // What the status says while each format is made.
  const PREPARING = {
    json: 'Preparing Builder JSON…',
    yaml: 'Preparing Builder YAML…',
    topology: 'Preparing Topology YAML…',
    png: 'Preparing PNG…',
    svg: 'Preparing SVG…',
    drawio: 'Preparing draw.io…',
    gexf: 'Preparing Gephi (GEXF)…',
    package: 'Making the Builder package…',
  };

  // Whether a format's button waits for another format to be made. Edits
  // that the Inspector cannot apply disable every button (locked).
  function waits(name) {
    return Boolean(task.value && task.value !== name);
  }

  // Makes a format as the download task, unless one is being made or the
  // Inspector holds edits it cannot apply. The status says what is made.
  function prepare(name, work) {
    if (props.unapplied || task.value) {
      return undefined;
    }

    error.clear();
    status.set(PREPARING[name]);

    return runBusy(DOWNLOADS, name, work);
  }

  // The Builder package: the sections ticked (none by default), its format,
  // and a package held, with its warnings, until the user saves it or not.
  const packageSections = ref([]);
  const packageFormat = ref('json');
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

  const bounds = computed(() => documentBounds(store.doc));

  // Reads the server's icon library first, unless it was read already, and
  // the images of the custom icons that the file carries (see
  // downloadDocument).
  function downloadText(kind) {
    return prepare(kind, () => saveDocument(kind));
  }

  async function saveDocument(kind) {
    await iconLibrary.ensureImages(iconRefs(store.doc));

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

  function downloadTopologyYAML() {
    return prepare('topology', saveTopology);
  }

  async function saveTopology() {
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
  function downloadPackage() {
    return prepare('package', makePackage);
  }

  async function makePackage() {
    packageHeld.value = null;

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
        // Each warning is an issue with its code. A warning that the server
        // sends as plain text shows as it is.
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
    status.set('The Builder did not save the package.');
  }

  // The image is made after the ring shows, as it holds the page for a
  // while on a large diagram (see runBusy).
  function downloadImageAs(format) {
    return prepare(format, () => saveImage(format));
  }

  async function saveImage(format) {
    try {
      // The canvas draws a server icon once its image is read: the picture
      // waits for those it shows.
      await iconLibrary.ensureImages(iconRefs(store.doc));
      await nextTick();
      await exportImage({
        element: props.viewportElement(),
        doc: store.doc,
        format,
        toPng,
        toSvg,
        saveAs,
        backgroundColor: store.resolvedTheme === 'dark' ? '#12171f' : '#ffffff',
      });

      const note = pictureIconNote(store.doc, iconLibrary, {
        showNodeIcons: builderSettings.showNodeIcons,
      });

      status.set(
        [`Saved ${exportFileName(store.doc, format)}.`, note]
          .filter(Boolean)
          .join(' '),
      );
    } catch (err) {
      status.clear();
      error.set(`Image download failed: ${err.message}`);
    }
  }

  // The diagram as a draw.io file (see drawio.js). It waits for the images of
  // the custom icons it shows, and draws the icons as the canvas does. The status names the icons whose images could not be read.
  function downloadDrawio() {
    return prepare('drawio', saveDrawio);
  }

  async function saveDrawio() {
    try {
      await iconLibrary.ensureImages(iconRefs(store.doc));

      const fileName = exportFileName(store.doc, 'drawio');

      saveText({
        text: toDrawio(store.doc, {
          library: iconLibrary,
          showNodeIcons: builderSettings.showNodeIcons,
          showNodeDescriptions: builderSettings.showNodeDescriptions,
          iconSize: iconSizeOverride(),
        }),
        mime: DRAWIO_MIME,
        fileName,
        saveAs,
      });

      const note = pictureIconNote(store.doc, iconLibrary, {
        showNodeIcons: builderSettings.showNodeIcons,
        subject: 'file',
      });

      status.set([`Saved ${fileName}.`, note].filter(Boolean).join(' '));
    } catch (err) {
      status.clear();
      error.set(`draw.io download failed: ${err.message}`);
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

    return `the Builder could not read scenario ${name}`;
  }

  // The network as a GEXF graph (see gexf.js), dated by the diagram's last
  // change (see lastModified). The scenarios are read again first. When one
  // of them cannot be read, the file lists no apps, not some of them.
  function downloadGEXF() {
    return prepare('gexf', saveGEXF);
  }

  async function saveGEXF() {
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
      reads = await Promise.all(names.map((name) => store.readScenario(name)));
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
    drawio: downloadDrawio,
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

  // A command's format starts after the dialog is drawn, from its button,
  // which takes focus as if the user pressed it. When the Inspector has
  // edits that it cannot apply, nothing starts. The dialog says why, as
  // for a press.
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

  /* The package is a section of its own, apart from the hints above it by
     the gap between the other sections of the dialog. */
  .builder-download__package {
    margin-top: 0.75rem;
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
