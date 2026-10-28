<!-- Diagram checks in the editor header: a count button and an issues dialog
     with Fix and Preflight. The dialog has its own status region, because the
     page behind the modal is inert. The preflight report outlives the dialog. -->
<template>
  <button
    type="button"
    class="builder-button builder-checks"
    aria-haspopup="dialog"
    :aria-describedby="tipFor ? 'header-tip-checks' : undefined"
    data-testid="builder-checks"
    v-on="tipFor ? tipFor('checks') : {}"
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
    <p ref="summaryEl" tabindex="-1" data-testid="checks-summary">
      {{ summary }}
    </p>
    <p v-if="store.disks === null" class="builder-hint">
      The Builder does not check drive images, because the server did not list
      its disk images.
    </p>

    <builder-issue-list
      ref="issueList"
      :groups="groups"
      :heading-level="3"
      id-prefix="builder-checks"
      testid="checks-issues"
      @go="goTo"
      @fix="fix" />

    <!-- What the last Fix changed. The dialog holds it from the start. -->
    <p class="builder-dialog__message" role="status" data-testid="checks-fixed">
      <span v-if="fixed.text" :key="fixed.key">{{ fixed.text }}</span>
    </p>

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
        you start an experiment from it. Preflight does not start or change
        anything.
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
          aria-describedby="builder-preflight-experiment-hint" />
        <datalist id="builder-preflight-experiments">
          <option v-for="option in experiments" :key="option" :value="option" />
        </datalist>
        <p id="builder-preflight-experiment-hint" class="builder-hint">
          The network check uses its VLAN range and default bridge.
        </p>
      </div>

      <!-- While the checks run, a ring turns over its label. -->
      <builder-busy-button
        class="builder-button--primary"
        :busy="preflight.running"
        :disabled="!canRun"
        :aria-describedby="
          selected.length ? undefined : 'builder-preflight-none'
        "
        data-testid="preflight-run"
        @click="runPreflight">
        Run
      </builder-busy-button>
      <p
        v-if="!selected.length"
        id="builder-preflight-none"
        class="builder-hint">
        Select at least one check to run.
      </p>

      <p class="builder-dialog__message builder-dialog__error" role="alert">
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
        <ul class="builder-preflight__results">
          <li
            v-for="result in results"
            :key="result.name"
            class="builder-preflight__result"
            :data-status="result.status"
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
  import { computed, nextTick, reactive, ref, watch } from 'vue';

  import BuilderBusyButton from './BuilderBusyButton.vue';
  import BuilderDialog from './BuilderDialog.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderIssueList from './BuilderIssueList.vue';
  import { useDialogStatus } from './dialogs/message.js';

  import { count } from '@/builder/announce.js';
  import { useDiskReads } from '@/builder/diskReads.js';
  import {
    bySeverity,
    countsText,
    issueCounts,
    issueEntries,
    publishingText,
  } from '@/builder/issues.js';
  import { issueFix } from '@/builder/issueFixes.js';
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

  defineProps({
    // The header's tooltip events of a button, by its key (see headerTip in
    // Builder.vue). Without it, the button has no tooltip.
    tipFor: { type: Function, default: null },
  });

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
  // Each issue has its fix, when one edit fixes it and the draft can change.
  const groups = computed(() =>
    bySeverity(
      issueEntries(store.doc, store.issues).map((issue) => ({
        ...issue,
        fix: store.readOnly ? null : issueFix(store.doc, issue),
      })),
      store.doc,
    ),
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

  // --- fixes ---------------------------------------------------------------

  const summaryEl = ref(null);
  const issueList = ref(null);
  // The page's live region does not say a fix again after the dialog
  // closes (see useDialogStatus).
  const fixed = useDialogStatus(store);

  // A fix message is only for the dialog it was made in.
  watch(open, (isOpen) => {
    if (!isOpen) {
      fixed.clear();
    }
  });

  // The fixed issue leaves the list, so focus goes to the Fix or Go to of
  // the issue now at its position, or of the last one, or to the summary.
  async function fix(issue, position) {
    const label = store.fixIssue(issue);

    if (!label) {
      return;
    }

    fixed.set(`${label}.`);
    await nextTick();

    const items = [
      ...(issueList.value?.$el?.querySelectorAll?.('[data-testid="issue"]') ||
        []),
    ];
    const item = items[Math.min(position, items.length - 1)];
    const target =
      item?.querySelector('[data-testid="issue-fix"]') ||
      item?.querySelector('button');

    (target || summaryEl.value)?.focus();
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

  const outcome = computed(() => {
    if (preflight.running) {
      return 'Running the checks…';
    }

    return preflight.report ? preflightOutcome(preflight.report) : '';
  });

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
  // shows again after they are a while old.
  useDiskReads(store);
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
