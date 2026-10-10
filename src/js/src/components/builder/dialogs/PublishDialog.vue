<!--
  Publish shell.

  Publish is an intent, not an upload. The server loads the snapshot that
  the draft cursor points at and runs its own checks again, so this dialog
  never sends the document. The dialog first makes sure that the queue is
  empty (store.publish does that). It then names the configs to write and
  reports the result of each stage.

  Config names are checked here against the server's naming rule, so a bad
  name is reported on its field before anything is sent. While a name
  breaks the rule, the field says why under it, with the rule. A name that
  keeps the rule shows no rule. Whether each config is created or updated
  follows the server's list of existing configs, which is read again every
  time the dialog opens. The server lets a draft update only a config that
  the draft was loaded from or published, so that the draft can publish
  again after more edits (see updateBlocker). For this reason, the hints and
  the submit button promise an update only then. Any other existing name is
  refused on its field before anything is sent. A hint that promises an
  update warns, because an update replaces the config. Updating a topology
  that still has a diagram of the legacy Builder replaces that diagram, and
  the hint says so.

  An update replaces a config on the server, which no one can undo. For this
  reason, Publish asks first, in an alert dialog that names each config
  replaced (see overwriteConfirmation). Nothing is sent until the user
  confirms. Cancel, Escape or a click outside return to the form, and focus
  returns with them.

  Publishing adds the topology to every scenario the diagram lists, and the
  dialog says so. With an experiment, Experiment scenario picks the
  scenario that it uses: the first one listed, unless the user chooses
  another or No scenario.

  Above the buttons, What publishing changes lists what publishing to the
  names in the form would change. The server works this out from the saved
  snapshot without writing anything (a dry run, previewPublish in
  store.js). The list covers the Topology and Experiment configs, included
  topologies, scenario annotations, disk images and VLAN aliases. It also
  shows what publishing would warn of, or the refusal. It lists no problem
  that the dialog's checks already list, nor one that the server listed
  when it refused Publish (see issuesNotInChecks). While the checks' errors
  block publishing, it says so instead. It is read when the dialog opens,
  after a pause whenever the form changes, and whenever the saved draft
  changes. From the change on, the list shown is marked busy. It never stops
  Publish. When it cannot be read and Publish is available, it says that
  Publish still works.

  The Inspector's unapplied edits are saved before the dialog opens (see
  leave.js). Edits that the Inspector cannot apply stop Publish from
  sending, so that the publish does not leave them out. The dialog says so
  when it opens.

  A draft's card on the drafts page also opens the dialog (landing). The
  draft is loaded, and the editor is not shown. The title then names the
  draft. Open draft goes to the editor, where the user corrects the
  diagram's errors, a conflict or a save that failed. Where one of them
  stops the publish, the dialog says so.

  The diagram's checks, what the server lists when it refuses a publish,
  and the errors and warnings of a result are listed by severity, errors
  first (see BuilderIssueList.vue). Go to on an issue closes the dialog and
  selects the element that the issue is about, with focus on the Inspector
  field it names (see goToIssue in store.js). On the drafts page, Go to
  opens the draft first.
-->
<template>
  <builder-dialog
    :title="landing && name ? `Publish ${name}` : 'Publish diagram'"
    title-id="publish-dialog-title"
    :describedby="unapplied ? 'publish-unapplied' : ''"
    @close="$emit('close')">
    <form v-if="!result" novalidate @submit.prevent="submit">
      <fieldset class="builder-field">
        <legend>What to publish</legend>
        <label class="builder-choice">
          <input
            v-model="form.mode"
            type="radio"
            name="publish-mode"
            value="topology" />
          Topology only
        </label>
        <label class="builder-choice">
          <input
            v-model="form.mode"
            type="radio"
            name="publish-mode"
            value="topology-experiment"
            data-testid="publish-mode-experiment" />
          Topology and an experiment
        </label>
      </fieldset>

      <div class="builder-field">
        <label for="publish-name">Topology name</label>
        <input
          id="publish-name"
          v-model="form.topologyName"
          type="text"
          required
          list="publish-topology-names"
          :aria-invalid="invalid('topologyName')"
          :aria-describedby="
            describedBy(
              'topologyName',
              nameHintIds(
                'topologyName',
                'publish-topology-action-hint',
                'publish-name-rule',
              ),
            )
          "
          data-testid="publish-name" />
        <datalist id="publish-topology-names">
          <option
            v-for="name in topologyNames"
            :key="name"
            :value="name"></option>
        </datalist>
        <!-- An update replaces a config, which no one can undo, so its hint
             warns: the sign before the words, on a yellow ground. The words
             say it as well, for screen readers and without color. -->
        <p
          id="publish-topology-action-hint"
          class="builder-hint"
          :class="{ 'builder-hint--warning': topologyUpdates }"
          aria-live="polite"
          data-testid="publish-topology-action-hint">
          <builder-icon v-if="topologyUpdates" name="warning" :size="14" />
          <span>{{ topologyHint }}</span>
        </p>
        <!-- Shown only while the name breaks the naming rule: why, then the
             rule. -->
        <p
          v-if="nameHints.topologyName"
          id="publish-name-rule"
          class="builder-hint"
          data-testid="publish-name-rule">
          {{ nameHints.topologyName }}
        </p>
      </div>

      <div v-if="form.mode === 'topology-experiment'" class="builder-field">
        <label for="publish-experiment">Experiment name</label>
        <input
          id="publish-experiment"
          v-model="form.experimentName"
          type="text"
          required
          list="publish-experiment-names"
          :aria-invalid="invalid('experimentName')"
          :aria-describedby="
            describedBy(
              'experimentName',
              nameHintIds(
                'experimentName',
                'publish-experiment-hint',
                'publish-experiment-rule',
              ),
            )
          "
          data-testid="publish-experiment" />
        <datalist id="publish-experiment-names">
          <option
            v-for="name in experimentNames"
            :key="name"
            :value="name"></option>
        </datalist>
        <p
          id="publish-experiment-hint"
          class="builder-hint"
          :class="{ 'builder-hint--warning': experimentUpdates }"
          aria-live="polite"
          data-testid="publish-experiment-hint">
          <builder-icon v-if="experimentUpdates" name="warning" :size="14" />
          <span>{{ experimentHint }}</span>
        </p>
        <p
          v-if="nameHints.experimentName"
          id="publish-experiment-rule"
          class="builder-hint"
          data-testid="publish-experiment-rule">
          {{ nameHints.experimentName }}
        </p>
      </div>

      <!-- The scenarios the diagram lists, which publishing adds the
           topology to in either mode. An experiment uses one of them, or
           none. -->
      <div
        v-if="scenarios.length"
        class="builder-field"
        data-testid="publish-scenario">
        <template v-if="form.mode === 'topology-experiment'">
          <label for="publish-scenario">Experiment scenario</label>
          <select
            id="publish-scenario"
            v-model="form.scenarioName"
            :aria-invalid="invalid('scenarioName')"
            :aria-describedby="
              describedBy('scenarioName', 'publish-scenario-hint')
            "
            data-testid="publish-scenario-select">
            <option v-for="name in scenarios" :key="name" :value="name">
              {{ name }}
            </option>
            <option value="">No scenario</option>
          </select>
        </template>
        <h3 v-else>Scenarios</h3>
        <p
          id="publish-scenario-hint"
          class="builder-hint"
          data-testid="publish-scenario-hint">
          {{ scenarioHint }}
        </p>
      </div>

      <div class="builder-field">
        <h3>Checks</h3>
        <p
          v-if="unapplied"
          id="publish-unapplied"
          class="builder-dialog__error"
          data-testid="publish-unapplied">
          {{ unappliedBlock(unapplied, 'published') }}
        </p>
        <p>{{ summaryText }}</p>
        <builder-issue-list
          v-if="issues.length"
          :groups="issueGroups"
          blocking
          :heading-level="4"
          id-prefix="publish-checks"
          testid="publish-checks"
          @go="goTo" />
        <p
          v-if="landing && failed"
          class="builder-hint"
          data-testid="publish-open-hint">
          Open the draft to fix the errors, then publish.
        </p>
        <p class="builder-hint">
          The server publishes the last saved snapshot and checks it again.
        </p>
      </div>

      <p
        id="publish-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="publish-error">{{
          error.text
        }}</span>
      </p>

      <!-- What the server listed when it refused the publish. -->
      <builder-issue-list
        v-if="refusalIssues.length"
        :groups="refusalGroups"
        blocking
        :heading-level="3"
        id-prefix="publish-refusal"
        testid="publish-refusal"
        @go="goTo" />

      <!-- What publishing to these names changes. The server works it out
           without writing anything, each time the form or the saved draft
           changes. The words say what is added, removed or kept. -->
      <section
        v-if="!store.readOnly"
        class="builder-field builder-publish-preview"
        aria-labelledby="publish-preview-title"
        :aria-busy="preview.loading ? 'true' : 'false'"
        data-testid="publish-preview">
        <h3 id="publish-preview-title">What publishing changes</h3>
        <p
          v-if="preview.blocked"
          class="builder-hint"
          data-testid="publish-preview-blocked">
          {{ preview.blocked }}
        </p>
        <p
          v-else-if="preview.error"
          class="builder-hint"
          data-testid="publish-preview-error">
          {{ preview.error }}
        </p>
        <template v-else>
          <!-- The errors under Checks are not listed again here, though
               the server finds them too. -->
          <p
            v-if="failed"
            class="builder-hint"
            data-testid="publish-preview-checks">
            Publishing is blocked by the errors listed under Checks.
          </p>
          <template v-if="preview.changes">
            <div
              v-for="group in previewGroups"
              :key="group.key"
              class="builder-publish-preview__group">
              <h4 :id="`publish-preview-${group.key}-title`">
                {{ group.title }}
              </h4>
              <ul
                :aria-labelledby="`publish-preview-${group.key}-title`"
                :data-testid="`publish-preview-${group.key}`">
                <li v-for="(line, index) in group.lines" :key="index">
                  {{ line }}
                </li>
              </ul>
            </div>
            <p v-if="!changedItems(preview.changes)">
              Nothing outside the Topology changes.
            </p>
          </template>
          <!-- Why the server would refuse to publish, or what publishing
               would warn of, such as a legacy diagram it replaces: only
               what Checks does not list already. -->
          <builder-issue-list
            v-if="previewIssues.length"
            :groups="previewIssueGroups"
            blocking
            :heading-level="4"
            id-prefix="publish-preview-issues"
            testid="publish-preview-issues"
            @go="goTo" />
          <p
            v-else-if="!preview.changes && preview.loading"
            class="builder-hint">
            Working out what publishing changes…
          </p>
        </template>
        <!-- Polite, as the dialog's own status line is, but no second
             status role: it says only that the list was read again. -->
        <p
          class="builder-visually-hidden"
          aria-live="polite"
          aria-atomic="true"
          data-testid="publish-preview-status">
          <span v-if="previewStatus.text" :key="previewStatus.key">{{
            previewStatus.text
          }}</span>
        </p>
      </section>

      <div class="builder-dialog__actions">
        <button
          v-if="landing"
          type="button"
          class="builder-button"
          data-testid="publish-open-draft"
          :aria-disabled="busy || undefined"
          @click="busy || $emit('open-draft')">
          Open draft
        </button>
        <button type="button" class="builder-button" @click="$emit('close')">
          Cancel
        </button>
        <button
          ref="submitButton"
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="publish-submit"
          :disabled="blocked"
          :aria-disabled="busy || blocked ? 'true' : undefined">
          {{ submitLabel }}
        </button>
      </div>
    </form>

    <div v-else data-testid="publish-result">
      <p ref="resultSummary" tabindex="-1" data-testid="publish-summary">
        {{ resultText }}
      </p>

      <ul class="builder-issues">
        <li
          v-for="stage in result.stages"
          :key="stage.name"
          :data-level="stageFailed(stage) ? 'error' : 'info'"
          :data-status="stage.status">
          <strong>{{ stage.name }}:</strong>
          {{ stage.status }}
          <template v-if="stage.message">— {{ stage.message }}</template>
        </li>
      </ul>

      <!-- The errors and warnings the server reported with the result. -->
      <builder-issue-list
        v-if="resultIssues.length"
        class="builder-publish-result__issues"
        :groups="resultGroups"
        :heading-level="3"
        id-prefix="publish-result"
        testid="publish-result-issues"
        @go="goTo" />

      <div class="builder-dialog__actions">
        <button
          v-if="!result.ok"
          type="button"
          class="builder-button"
          data-testid="publish-retry"
          @click="back">
          Back
        </button>
        <button
          type="button"
          class="builder-button builder-button--primary"
          @click="$emit('close')">
          Close
        </button>
      </div>
    </div>

    <p
      class="builder-dialog__message builder-visually-hidden"
      role="status"
      data-testid="publish-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>

    <builder-confirm
      v-if="confirming"
      id="publish-confirm"
      :title="confirming.title"
      :message="confirming.message"
      :confirm-label="confirming.confirmLabel"
      @confirm="confirmOverwrite"
      @cancel="confirming = null" />
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

  import BuilderConfirm from '../BuilderConfirm.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import BuilderIssueList from '../BuilderIssueList.vue';
  import { useFieldError, useMessage, useRefusalIssues } from './message.js';
  import { usePublishPreview } from './publishPreview.js';

  import { count, listOf } from '@/builder/announce.js';
  import {
    bySeverity,
    issueEntries,
    issuesNotInChecks,
    responseIssues,
  } from '@/builder/issues.js';
  import { unappliedBlock } from '@/builder/leave.js';
  import { documentScenarios } from '@/builder/model.js';
  import {
    buildPublishIntent,
    changedItems,
    configName,
    configNameHint,
    describePublishResult,
    keptIncludesText,
    legacyDiagramUpdate,
    overwriteConfirmation,
    publishChangeGroups,
    publishLabel,
    scenarioNames as readScenarioNames,
    scenarioStageHint,
    stageFailed,
    targetHint,
    updateBlocker,
  } from '@/builder/publish.js';
  import { useBuilderStore } from '@/builder/store.js';

  const props = defineProps({
    // The Inspector's edits that could not be applied (see leave.js).
    unapplied: { type: Object, default: null },
    // Opened from a draft's card on the drafts page, with the editor not
    // shown, and the draft's name as its card gives it.
    landing: { type: Boolean, default: false },
    name: { type: String, default: '' },
  });

  const emit = defineEmits(['close', 'published', 'open-draft']);

  const store = useBuilderStore();
  const busy = ref(false);
  // Errors about a field buildPublishIntent() refused name its control.
  const { error, invalid, describedBy, fail } = useFieldError('publish-error', {
    topologyName: 'publish-name',
    experimentName: 'publish-experiment',
    scenarioName: 'publish-scenario',
  });
  const status = useMessage();
  const result = ref(null);
  const submitButton = ref(null);
  const resultSummary = ref(null);
  // The confirmation shown before an update, with the intent it sends.
  const confirming = ref(null);

  // The Scenario configs the diagram lists.
  const scenarios = computed(() => documentScenarios(store.doc));
  const scenarioHint = computed(() => scenarioStageHint(scenarios.value));

  const topologyNames = computed(() =>
    readScenarioNames(store.sources.topologies),
  );
  const experimentNames = computed(() =>
    readScenarioNames(store.sources.experiments),
  );

  // The diagram name is free text. Config names must follow the server's
  // naming rule, so the topology's name starts as a valid form of it. An
  // experiment uses the first scenario the diagram lists unless another, or
  // none, is chosen.
  const form = reactive({
    mode: 'topology',
    topologyName: configName(store.doc.metadata?.name),
    experimentName: '',
    scenarioName: scenarios.value[0] || '',
  });

  // Configs may have been written since the list was last read, by this
  // editor or anyone else, so it is read on every open, with the published
  // diagrams and the saved snapshot that decide which ones this draft may
  // update. Nothing is edited while the dialog is open, so once the queue is
  // saved the snapshot is the one Publish sends.
  let sourcesLoaded = Promise.resolve();
  // Whether the dialog has closed, which a submit that was waiting for the
  // lists finds out.
  let closed = false;

  // What publishing changes (see publishPreview.js). It is read when the
  // lists above are read. It is read again after a pause in typing whenever
  // the form or those lists change, and whenever the saved draft changes.
  // Nothing is asked for a draft that the dialog cannot publish, or while
  // the dialog shows a result.
  const {
    preview,
    status: previewStatus,
    refresh: refreshPreview,
    schedule: schedulePreview,
    close: closePreview,
  } = usePublishPreview({
    buildIntent: () => buildIntent(),
    previewPublish: (intent) => store.previewPublish(intent),
    idle: () => store.readOnly || Boolean(result.value),
    publishable: () => !blocked.value,
  });

  onBeforeUnmount(() => {
    closed = true;
    closePreview();
  });

  // What the server listed when it refused a publish shows while the dialog
  // is open and the diagram is the one refused.
  useRefusalIssues(store);

  onMounted(() => {
    // A failed save is reported when Publish is pressed (store.publish).
    sourcesLoaded = Promise.allSettled([
      store.fetchSources(),
      store.fetchDocuments(),
      store.readOnly ? null : store.saveNow(),
    ]);
    sourcesLoaded.then(() => refreshPreview());
  });

  const previewGroups = computed(() => publishChangeGroups(preview.changes));

  // The targets, and the lists that say whether each is created or updated.
  watch(
    () => [
      form.mode,
      form.topologyName,
      form.experimentName,
      form.scenarioName,
      store.sources,
      store.documents,
    ],
    schedulePreview,
  );
  // The saved draft: its ETag changes with every save the server takes.
  watch(() => store.etag, schedulePreview);

  const topologyExists = computed(() =>
    (store.sources.topologies || []).some(
      (entry) =>
        (typeof entry === 'string' ? entry : entry?.name) ===
        form.topologyName.trim(),
    ),
  );

  const experimentExists = computed(() =>
    (store.sources.experiments || []).some(
      (entry) =>
        (typeof entry === 'string' ? entry : entry?.name) ===
        form.experimentName.trim(),
    ),
  );

  // What stops this draft from updating a config of that name, if anything.
  const topologyBlocker = computed(() =>
    updateBlocker(
      'topology',
      form.topologyName.trim(),
      store.sources.topologies,
      store.publishDraft,
    ),
  );

  // What the update does with the legacy Builder diagram the topology of
  // that name still has, if it has one.
  const topologyLegacy = computed(() =>
    legacyDiagramUpdate(
      form.topologyName.trim(),
      store.sources.topologies,
      store.doc.source,
    ),
  );

  const experimentBlocker = computed(() =>
    updateBlocker(
      'experiment',
      form.experimentName.trim(),
      store.sources.experiments,
      {
        ...store.publishDraft,
        topologyName: form.topologyName.trim(),
        scenarioName: form.scenarioName,
      },
    ),
  );

  // What publishing does with each name, under its field. Updating a config
  // this draft may update is a warning: it replaces the config.
  const topologyHint = computed(() =>
    targetHint(
      'topology',
      topologyExists.value,
      topologyBlocker.value,
      topologyLegacy.value,
    ),
  );
  const experimentHint = computed(() =>
    targetHint('experiment', experimentExists.value, experimentBlocker.value),
  );
  const topologyUpdates = computed(
    () => topologyExists.value && !topologyBlocker.value,
  );
  const experimentUpdates = computed(
    () => experimentExists.value && !experimentBlocker.value,
  );

  // Why each name breaks the naming rule, with the rule, while it does. ''
  // for a name that keeps the rule, which then shows no rule.
  const nameHints = computed(() => ({
    topologyName: configNameHint(form.topologyName),
    experimentName: configNameHint(form.experimentName),
  }));

  // The ids that describe a name field: its hint, when it has one, then its
  // rule while it shows. The rule is left out while the field's error
  // shows, which says the same.
  function nameHintIds(field, hintId, ruleId) {
    const rule = nameHints.value[field] && !invalid(field) ? ruleId : '';

    return [hintId, rule].filter(Boolean).join(' ');
  }

  // What publishing refuses is an error here, though not in the draft (see
  // toIssue): an interface with no VLAN, an address that two interfaces
  // use, and a hostname that phenix refuses. Errors are listed first, then
  // warnings.
  const issues = computed(() =>
    issueEntries(store.doc, store.issues, { publishing: true }),
  );
  const issueGroups = computed(() => bySeverity(issues.value, store.doc));
  const failed = computed(() =>
    issues.value.some((issue) => issue.severity === 'error'),
  );

  // What the server listed with a refusal, and with a result.
  const refusalIssues = computed(() =>
    issueEntries(store.doc, store.publishIssues),
  );
  const refusalGroups = computed(() =>
    bySeverity(refusalIssues.value, store.doc),
  );

  // What the dry run would warn of or refuse that neither Checks nor the
  // refusal of a Publish already lists. A problem that Checks lists stays
  // there. A problem that only the server finds is listed under the refusal
  // after Publish was refused, whichever of the two answers came first.
  const previewIssues = computed(() =>
    issueEntries(
      store.doc,
      issuesNotInChecks(store.doc, preview.issues, [
        ...issues.value,
        ...refusalIssues.value,
      ]),
    ),
  );
  const previewIssueGroups = computed(() =>
    bySeverity(previewIssues.value, store.doc),
  );
  // A result keeps its errors and warnings as issues, each with its code
  // (errorIssues and warningIssues, see readPublishResult), beside their
  // messages alone.
  const resultIssues = computed(() =>
    issueEntries(
      store.doc,
      responseIssues({
        errors: result.value?.errorIssues ?? result.value?.errors,
        warnings: result.value?.warningIssues ?? result.value?.warnings,
      }),
    ),
  );
  const resultGroups = computed(() =>
    bySeverity(resultIssues.value, store.doc),
  );
  const blocked = computed(
    () => failed.value || store.readOnly || Boolean(props.unapplied),
  );

  // Devices from included topologies are not written to the topology: it
  // names those topologies in includeTopologies instead. Their connections,
  // and the switches and networks only they use, are not counted either.
  const summaryText = computed(() => {
    const summary = store.summary;
    const ready =
      `${count(summary.devices - summary.included, 'device')}, ` +
      `${count(summary.switches - summary.includedSwitches, 'switch', 'switches')}, ` +
      `${count(summary.networks - summary.includedNetworks, 'network')} and ` +
      `${count(summary.links - summary.includedLinks, 'connection')} are ready to publish` +
      `${failed.value ? ' once the errors below are fixed' : ''}.`;

    // A diagram that shows none of the nodes of the topologies it includes
    // still publishes the references to them.
    if (!summary.included) {
      return `${ready}${keptIncludesText(store.doc.source?.includeTopologies)}`;
    }

    const onlyTheirs = listOf(
      [
        summary.includedSwitches &&
          count(summary.includedSwitches, 'switch', 'switches'),
        summary.includedNetworks && count(summary.includedNetworks, 'network'),
      ].filter(Boolean),
    );

    return (
      `${ready} The ${count(summary.included, 'device')} from included ` +
      `topologies and their ${count(summary.includedLinks, 'connection')}` +
      `${onlyTheirs ? `, and the ${onlyTheirs} only they use,` : ''} ` +
      'are not copied: the topology includes them by reference.'
    );
  });

  // The submit button names what it will do, so overwriting an existing
  // config is stated on the control that does it. A name this draft cannot
  // update is refused, so the draft can only create.
  const submitLabel = computed(() => {
    if (busy.value) {
      return 'Publishing…';
    }

    const action = (exists, blocker) =>
      exists && !blocker ? 'update' : 'create';

    return publishLabel(
      action(topologyExists.value, topologyBlocker.value),
      form.mode === 'topology-experiment'
        ? action(experimentExists.value, experimentBlocker.value)
        : undefined,
    );
  });

  const resultText = computed(() => describePublishResult(result.value));

  const OPEN_TO_RESOLVE = 'Open the draft to resolve it.';

  // Whether the draft has changes the server does not hold, which is what
  // store.publish refuses to publish over.
  function notSaved() {
    return store.saveState.status !== 'saved' || store.saveState.pending > 0;
  }

  /**
   * Builds the publish intent. The document is deliberately absent: the server
   * publishes the snapshot the cursor points at.
   */
  function buildIntent() {
    return buildPublishIntent(form, {
      scenarios: scenarios.value,
      topologies: store.sources.topologies,
      experiments: store.sources.experiments,
      draft: store.publishDraft,
    });
  }

  async function submit() {
    if (busy.value) {
      return;
    }

    busy.value = true;
    error.clear();
    store.publishIssues = [];

    // Create or update is chosen from the list read when the dialog opened.
    await sourcesLoaded;

    // Closed while it waited: nothing is sent.
    if (closed) {
      return;
    }

    const { intent, error: problem, field } = buildIntent();

    if (!intent) {
      busy.value = false;
      fail(problem, field);

      return;
    }

    const overwrite = overwriteConfirmation(intent);

    if (overwrite) {
      busy.value = false;
      confirming.value = { ...overwrite, intent };

      return;
    }

    await send(intent);
  }

  // The confirmation closes, which returns focus to the form, before the
  // intent it was shown for is sent.
  async function confirmOverwrite() {
    // A second click before the confirmation closes sends nothing more.
    if (!confirming.value || busy.value) {
      return;
    }

    const { intent } = confirming.value;

    confirming.value = null;
    busy.value = true;
    await send(intent);
  }

  async function send(intent) {
    status.set('Publishing…');

    const published = await store.publish(intent);

    busy.value = false;
    status.clear();

    if (!published) {
      // A refusal about a config names its field, for example a name that
      // exists after all. The list is read again, so the form now says so.
      // Changes the server does not hold (a conflict, a save that failed)
      // are settled in the editor, which the drafts page says how to reach.
      const reason = store.error || 'Publish failed.';

      fail(
        props.landing && notSaved() ? `${reason} ${OPEN_TO_RESOLVE}` : reason,
        store.errorField,
      );

      return;
    }

    result.value = published;

    if (published.ok) {
      emit('published', published);
    }

    // The result replaces the form, and the button that had focus with it.
    await nextTick();
    resultSummary.value?.focus();
  }

  async function back() {
    result.value = null;
    await nextTick();
    submitButton.value?.focus();
    refreshPreview();
  }

  // Go to on an issue. The dialog closes, which gives focus back to what
  // opened it, before Go to moves focus (see goToIssue). On the drafts
  // page, the editor first opens the draft, and Go to then goes there.
  async function goTo(issue) {
    if (props.landing) {
      emit('open-draft', issue);

      return;
    }

    emit('close');
    await nextTick();
    store.goToIssue(issue);
  }

  defineExpose({ buildIntent, form, result });
</script>

<style scoped>
  fieldset {
    border: 0;
    padding: 0;
  }

  .builder-publish-result__issues {
    margin-top: 0.75rem;
  }

  .builder-publish-preview__group h4 {
    margin: 0.5rem 0 0.25rem;
    font-size: inherit;
  }

  .builder-publish-preview__group ul {
    margin: 0;
    padding-left: 1.25rem;
    list-style: disc;
  }
</style>
