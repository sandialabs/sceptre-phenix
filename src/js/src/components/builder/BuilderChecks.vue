<!--
  Diagram checks, in the editor header.

  A button that counts the diagram's errors and warnings, and opens a dialog
  listing them, the errors first and then the warnings, each group in the
  order of the nodes and connections they are about. Go to on an issue
  closes the dialog and selects its node or connection (see goToIssue in
  store.js): focus moves to the Inspector field the issue names, or else to
  the element on the canvas, which pans it into view. The button is always
  there, so the header does not shift as the first issue comes or the last
  one goes. Its text changes with every edit and is not a live region: the
  Inspector announces the errors an edit makes, and the counts are read
  when the button is.

  Drive images are checked against the disk images the server has, so the
  editor reads them here as it opens, and again when the page comes back into
  view after a while, since images are added elsewhere.
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
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';

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
  import { useBuilderStore } from '@/builder/store.js';

  const store = useBuilderStore();

  // Told apart by shape as well as color.
  const ICONS = { error: 'close', warning: 'warning' };

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

  // The images are read as the editor opens, and again when the page comes
  // back into view once they are a while old. Reading them asks minimega
  // about every image file, so not every time.
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
</style>
