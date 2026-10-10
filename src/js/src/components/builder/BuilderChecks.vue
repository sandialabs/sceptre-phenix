<!--
  Diagram checks, in the editor header.

  A button that counts the diagram's errors and warnings, and opens a dialog
  listing them, the errors first and then the warnings, each group in the
  order of the nodes and connections they are about. Go to on an issue
  closes the dialog and selects its node or connection (see goToIssue in
  store.js): focus moves to the Inspector field the issue names, or else to
  the element on the canvas, which pans it into view. The button is always
  there, so the header does not shift when the first issue comes or the
  last one goes. Its text changes with every edit and is not a live region:
  the Inspector announces the errors an edit makes, and screen readers read
  the counts with the button.

  The checks compare drive images with the disk images on the server. So
  the editor reads the server's images here when it opens, and again when
  the page shows again after a while, because users add images elsewhere.

  Below the issues, Preflight asks the server to check the saved draft
  against the cluster and the server (see preflight.js). It runs the ticked
  checks: none at first, then the last ones ticked in this browser. This
  component keeps the report outside the dialog, so the report is still
  there when the dialog opens again or after Go to on one of its issues,
  until the diagram changes. The dialog's own status region announces the
  result of the checks, because the page behind a modal dialog is inert.
  When the dialog closed before the checks finished, the editor's live
  region announces the result.
-->
<template>
  <button
    type="button"
    class="builder-button builder-checks"
    aria-haspopup="dialog"
    data-testid="builder-checks"
    @click="open = true">
    <!-- Named by the hidden text, which says the same as the counts shown
         and holds the commas that the icons stand in for. -->
    <span
      v-for="part in parts"
      :key="part.level"
      class="builder-checks__count"
      :data-level="part.level"
      aria-hidden="true">
      <builder-icon :name="part.icon" :size="14" />
      {{ part.text }}
    </span>
    <span class="builder-visually-hidden">{{ name }}</span>
  </button>

  <builder-dialog
    v-if="open"
    title="Diagram checks"
    title-id="builder-checks-title"
    data-testid="checks-dialog"
    @close="open = false">
    <p data-testid="checks-summary">{{ summary }}</p>
    <p v-if="store.disks === null" class="builder-hint">
      Drive images are not checked: the server did not list its disk images.
    </p>

    <builder-issue-list
      :groups="groups"
      :heading-level="3"
      id-prefix="builder-checks"
      testid="checks-issues"
      @go="goTo" />

    <section
      class="builder-preflight"
      aria-labelledby="builder-preflight-title"
      :aria-busy="preflight.running ? 'true' : 'false'"
      data-testid="preflight">
      <h3 id="builder-preflight-title" class="builder-preflight__title">
        Preflight
      </h3>
      <p class="builder-hint">
        Checks the saved draft against this phenix server and its cluster before
        an experiment is started from it. Nothing is started or changed.
      </p>

      <fieldset class="builder-field builder-preflight__checks">
        <legend>Checks to run</legend>
        <div
          v-for="check in PREFLIGHT_CHECKS"
          :key="check.id"
          class="builder-preflight__check">
          <label class="builder-choice">
            <input
              v-model="selected"
              type="checkbox"
              :value="check.id"
              :aria-describedby="`builder-preflight-${check.id}-hint`"
              :data-testid="`preflight-check-${check.id}`" />
            {{ check.label }}
          </label>
          <p :id="`builder-preflight-${check.id}-hint`" class="builder-hint">
            {{ check.hint }}
          </p>
        </div>
      </fieldset>

      <div class="builder-field">
        <label for="builder-preflight-experiment">Experiment (optional)</label>
        <input
          id="builder-preflight-experiment"
          v-model="experiment"
          type="text"
          list="builder-preflight-experiments"
          autocomplete="off"
          spellcheck="false"
          aria-describedby="builder-preflight-experiment-hint"
          data-testid="preflight-experiment" />
        <datalist id="builder-preflight-experiments">
          <option v-for="option in experiments" :key="option" :value="option" />
        </datalist>
        <p id="builder-preflight-experiment-hint" class="builder-hint">
          The network check uses its VLAN range and default bridge.
        </p>
      </div>

      <button
        type="button"
        class="builder-button builder-button--primary"
        :aria-disabled="canRun ? 'false' : 'true'"
        :aria-describedby="
          selected.length ? undefined : 'builder-preflight-none'
        "
        data-testid="preflight-run"
        @click="runPreflight">
        {{ preflight.running ? 'Running…' : 'Run' }}
      </button>
      <p
        v-if="!selected.length"
        id="builder-preflight-none"
        class="builder-hint">
        Tick at least one check to run.
      </p>

      <p
        class="builder-dialog__message builder-dialog__error"
        role="alert"
        data-testid="preflight-error">
        <span v-if="preflight.error" :key="preflight.errorKey">{{
          preflight.error
        }}</span>
      </p>

      <!-- The page behind the dialog is inert, so this region announces the
           result of the checks. The dialog holds it from the start. -->
      <p
        class="builder-dialog__message"
        role="status"
        data-testid="preflight-outcome">
        <span v-if="outcome" :key="preflight.outcomeKey">{{ outcome }}</span>
      </p>

      <template v-if="preflight.report">
        <ul class="builder-preflight__results" data-testid="preflight-results">
          <li
            v-for="result in results"
            :key="result.name"
            class="builder-preflight__result"
            :data-status="result.status"
            :data-check="result.name"
            data-testid="preflight-result">
            <h4 class="builder-preflight__heading">
              <builder-icon :name="STATUS_ICONS[result.status]" :size="14" />
              <span>{{ result.label }}: {{ result.statusText }}</span>
            </h4>
            <p
              class="builder-preflight__summary"
              data-testid="preflight-summary">
              {{ result.summary }}
            </p>
            <builder-issue-list
              v-if="result.issues.length"
              :groups="result.groups"
              :heading-level="5"
              :id-prefix="`builder-preflight-${result.name}`"
              :testid="`preflight-issues-${result.name}`"
              @go="goTo" />
          </li>
        </ul>
      </template>
    </section>
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

  import BuilderDialog from './BuilderDialog.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderIssueList from './BuilderIssueList.vue';

  import { count } from '@/builder/announce.js';
  import {
    bySeverity,
    countsText,
    issueCounts,
    issueEntries,
    publishingText,
  } from '@/builder/issues.js';
  import { sameButStamp } from '@/builder/model.js';
  import {
    PREFLIGHT_CHECKS,
    checkLabel,
    loadPreflightChecks,
    orderedChecks,
    preflightOutcome,
    savePreflightChecks,
    statusText,
  } from '@/builder/preflight.js';
  import { useBuilderStore } from '@/builder/store.js';

  const store = useBuilderStore();

  // The severities differ by shape as well as color.
  const ICONS = { error: 'close', warning: 'warning' };
  // Each status of a preflight check, by shape as well as by its words.
  const STATUS_ICONS = {
    passed: 'check',
    failed: 'close',
    unavailable: 'warning',
  };

  const open = ref(false);

  const counts = computed(() => issueCounts(store.issues));

  // What the button shows: each count there is, or that there is none.
  const parts = computed(() => {
    const { errors, warnings } = counts.value;
    const shown = [
      errors && {
        level: 'error',
        icon: ICONS.error,
        text: count(errors, 'error'),
      },
      warnings && {
        level: 'warning',
        icon: ICONS.warning,
        text: count(warnings, 'warning'),
      },
    ].filter(Boolean);

    return shown.length
      ? shown
      : [{ level: 'none', icon: 'check', text: 'No issues' }];
  });
  const name = computed(
    () => `${parts.value.map((part) => part.text).join(', ')} in this diagram`,
  );
  // The errors, then the warnings, each in the order of the nodes and
  // connections they are about.
  const groups = computed(() =>
    bySeverity(issueEntries(store.doc, store.issues), store.doc),
  );
  const located = computed(() =>
    groups.value.some((group) => group.issues.some((issue) => issue.target)),
  );

  const summary = computed(() => {
    const text = countsText(counts.value).replace(', ', ' and ');

    if (!text) {
      return 'This diagram has no errors or warnings.';
    }

    const choose = located.value
      ? ' Go to selects the node or connection an issue is about.'
      : '';

    return `This diagram has ${text}. ${publishingText(counts.value)}${choose}`;
  });

  // The dialog closes, which gives focus back to the checks button, before
  // Go to moves it on (see goToIssue).
  async function goTo(issue) {
    open.value = false;
    await nextTick();
    store.goToIssue(issue);
  }

  // --- preflight -----------------------------------------------------------

  // The checks ticked, which this browser remembers for the next time.
  const selected = ref(loadPreflightChecks());
  const experiment = ref('');
  const preflight = reactive({
    running: false,
    report: null,
    error: '',
    errorKey: 0,
    outcomeKey: 0,
  });

  watch(selected, (checks) => savePreflightChecks(checks));

  const canRun = computed(
    () => selected.value.length > 0 && !preflight.running,
  );

  // The experiments the server listed as sources, which the name field
  // suggests. The user can also type any other name.
  const experiments = computed(() => [
    ...new Set(
      (store.sources?.experiments || [])
        .map((source) => source?.name)
        .filter((option) => typeof option === 'string' && option !== ''),
    ),
  ]);

  const outcome = computed(() =>
    preflight.report ? preflightOutcome(preflight.report) : '',
  );

  // Each check's result with its label, its status in words and its issues
  // in the lists' shape, located in the diagram as the other issues are.
  const results = computed(() =>
    (preflight.report?.checks || []).map((result) => ({
      ...result,
      label: checkLabel(result.name),
      statusText: statusText(result.status),
      groups: bySeverity(issueEntries(store.doc, result.issues), store.doc),
    })),
  );

  // Changes to the diagram, except its stamp, which a save writes back.
  // This drops a report on an earlier diagram, and does not show a report
  // still on its way.
  let edits = 0;

  watch(
    () => store.doc,
    (next, previous) => {
      if (!sameButStamp(next, previous)) {
        edits += 1;
        preflight.report = null;
        preflight.error = '';
      }
    },
  );

  // Says a message the dialog shows in its own regions when it is open, or
  // through the editor's live region when it was closed meanwhile.
  function tellClosed(message) {
    if (!open.value) {
      store.announce(message);
    }
  }

  function preflightFailed(message) {
    preflight.error = message;
    preflight.errorKey += 1;
    tellClosed(message);
  }

  async function runPreflight() {
    if (!canRun.value) {
      return;
    }

    const started = edits;

    preflight.running = true;
    preflight.error = '';
    preflight.report = null;

    try {
      const { report, error } = await store.runPreflight({
        checks: orderedChecks(selected.value),
        experiment: experiment.value.trim(),
      });

      if (started !== edits) {
        return;
      }

      if (error) {
        preflightFailed(error);

        return;
      }

      preflight.report = report;
      preflight.outcomeKey += 1;
      tellClosed(preflightOutcome(report));
    } finally {
      preflight.running = false;
    }
  }

  // --- disk images -----------------------------------------------------------

  // The editor reads the images when it opens, and again when the page
  // shows again after they are a while old. Each read asks minimega about
  // every image file, so it does not happen every time.
  const DISKS_STALE_MS = 30000;
  let disksReadAt = 0;

  function readDisks() {
    disksReadAt = Date.now();
    store.fetchDisks();
  }

  function onVisibility() {
    if (
      document.visibilityState === 'visible' &&
      Date.now() - disksReadAt >= DISKS_STALE_MS
    ) {
      readDisks();
    }
  }

  onMounted(() => {
    readDisks();
    document.addEventListener('visibilitychange', onVisibility);
  });

  onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', onVisibility);
  });
</script>

<style scoped>
  .builder-checks {
    flex: none;
    gap: 0.6rem;
  }

  .builder-checks__count {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
  }

  .builder-checks__count[data-level='error'] .builder-icon {
    color: var(--bx-danger);
  }

  .builder-checks__count[data-level='warning'] .builder-icon {
    color: var(--bx-warning);
  }

  .builder-checks__count[data-level='none'] .builder-icon {
    color: var(--bx-success);
  }

  .builder-preflight {
    margin-top: 1rem;
    padding-top: 0.75rem;
    border-top: 1px solid var(--bx-border);
  }

  .builder-preflight__title {
    margin: 0 0 0.3rem;
    font-size: 0.95rem;
    font-weight: 700;
  }

  .builder-preflight__checks {
    margin: 0.5rem 0 0.6rem;
    padding: 0;
    border: 0;
  }

  .builder-preflight__checks legend {
    margin-bottom: 0.2rem;
    font-weight: 600;
    font-size: 0.85rem;
  }

  .builder-preflight__check .builder-hint {
    margin: 0 0 0.3rem 1.6rem;
  }

  .builder-preflight__results {
    list-style: none;
    margin: 0.5rem 0 0;
    padding: 0;
  }

  .builder-preflight__result {
    padding: 0.4rem 0.5rem;
    border: 1px solid var(--bx-border);
    border-left-width: 3px;
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    overflow-wrap: anywhere;
  }

  .builder-preflight__result + .builder-preflight__result {
    margin-top: 0.4rem;
  }

  .builder-preflight__result[data-status='passed'] {
    border-left-color: var(--bx-success);
  }

  .builder-preflight__result[data-status='failed'] {
    border-left-color: var(--bx-danger);
  }

  .builder-preflight__result[data-status='unavailable'] {
    border-left-color: var(--bx-warning);
  }

  .builder-preflight__heading {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    margin: 0;
    font-size: 0.85rem;
    font-weight: 700;
  }

  .builder-preflight__result[data-status='passed']
    .builder-preflight__heading
    .builder-icon {
    color: var(--bx-success);
  }

  .builder-preflight__result[data-status='failed']
    .builder-preflight__heading
    .builder-icon {
    color: var(--bx-danger);
  }

  .builder-preflight__result[data-status='unavailable']
    .builder-preflight__heading
    .builder-icon {
    color: var(--bx-warning);
  }

  .builder-preflight__summary {
    margin: 0.15rem 0 0.35rem;
    font-size: 0.85rem;
  }
</style>
