<!--
  Draft history: the open draft's snapshots on the server, oldest first, each
  of which can be restored.

  The dialog opens at once and reads the list while it shows (see
  fetchHistory in store.js). Until the list arrives the dialog is described
  by a status line saying so, beside a spinner, and the list's place is
  marked busy; the status then says how many snapshots there are. A failed
  read is reported here, with a button to read again, rather than by the
  page alert behind the dialog.
-->
<template>
  <builder-dialog
    title="Draft history"
    title-id="history-dialog-title"
    data-testid="history-dialog"
    aria-describedby="history-status"
    @close="$emit('close')">
    <p v-if="store.published" id="history-status">
      A published diagram has no draft history. Edit it as a draft to keep one.
    </p>
    <template v-else>
      <div
        :aria-busy="store.historyLoading ? 'true' : undefined"
        data-testid="history-content">
        <!-- The visible text is the whole name: the list gives the order,
             so the name adds no number of its own (WCAG 2.5.3). -->
        <ol v-if="listed" class="builder-history" data-testid="history-list">
          <li
            v-for="(entry, index) in store.serverHistory"
            :key="entry.id || index">
            <button
              type="button"
              class="builder-button"
              :aria-disabled="restoring || undefined"
              @click="restore(entry)">
              Restore {{ snapshotName(entry, index) }}
            </button>
          </li>
        </ol>
      </div>

      <!-- Shown while something is under way, or when there is nothing to
           list; otherwise only screen readers get the count. -->
      <p
        id="history-status"
        class="builder-dialog__message builder-history__status"
        :class="{
          'builder-visually-hidden': listed && !restoring,
          'builder-history__status--empty': !statusText,
        }"
        role="status"
        data-testid="history-status">
        <span
          v-if="store.historyLoading || restoring"
          class="builder-history__spinner"
          aria-hidden="true"></span>
        {{ statusText }}
      </p>
      <p class="builder-dialog__message builder-dialog__error" role="alert">
        <span v-if="store.historyError" data-testid="history-error">{{
          store.historyError
        }}</span>
      </p>
      <div v-if="store.historyError" class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="history-retry"
          @click="retry">
          <builder-icon name="refresh" :size="14" />
          Try again
        </button>
      </div>
    </template>
  </builder-dialog>
</template>

<script setup>
  import { computed, onBeforeUnmount, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';

  import { count } from '@/builder/announce.js';
  import { formatTimestamp } from '@/builder/format.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  const restoring = ref(false);

  // Read before the first render, so the dialog opens loading rather than
  // showing the list it had last time. A published diagram shown read only
  // has no draft, so no history.
  if (!store.published) {
    store.fetchHistory();
  }

  const listed = computed(
    () =>
      !store.historyLoading &&
      !store.historyError &&
      store.serverHistory.length > 0,
  );

  const statusText = computed(() => {
    if (store.historyLoading) {
      return 'Loading the draft history…';
    }
    if (restoring.value) {
      return 'Restoring the snapshot…';
    }
    if (store.historyError) {
      return '';
    }

    return listed.value
      ? `${count(store.serverHistory.length, 'snapshot')}, oldest first.`
      : 'The server holds no snapshots for this draft yet.';
  });

  // Snapshots are named by their change and time, never by their id. The
  // first one has no summary: it is the draft as created.
  function snapshotName(entry, index) {
    const change =
      entry.summary || (index === 0 ? 'Draft created' : 'Saved version');

    return [change, formatTimestamp(entry.createdAt, { seconds: true })]
      .filter(Boolean)
      .join(', ');
  }

  // Try again goes while the list is read, so focus moves to the dialog
  // first rather than falling to <body> (WCAG 2.4.3); the dialog's
  // description then says it is loading.
  function retry(event) {
    event.currentTarget.closest('dialog')?.focus();
    store.fetchHistory();
  }

  // The dialog closes once the snapshot is restored, or once the page alert
  // says why it could not be. A second press meanwhile does nothing, and a
  // dialog closed meanwhile leaves the view's next dialog alone.
  let open = true;
  onBeforeUnmount(() => {
    open = false;
  });

  async function restore(entry) {
    if (restoring.value) {
      return;
    }

    restoring.value = true;
    try {
      await store.restoreSnapshot(entry.id);
    } finally {
      restoring.value = false;
    }
    if (open) {
      emit('close');
    }
  }
</script>

<style scoped>
  .builder-history__status {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  /* Takes no room until there is something to say. */
  .builder-history__status--empty {
    margin: 0;
  }

  /* Reduced motion stops it turning (see builder.css); the text beside it
     says the same. */
  .builder-history__spinner {
    flex: none;
    width: 1rem;
    height: 1rem;
    border: 2px solid var(--bx-border);
    border-top-color: var(--bx-accent);
    border-radius: 50%;
    animation: builder-history-spin 0.8s linear infinite;
  }

  @keyframes builder-history-spin {
    to {
      transform: rotate(360deg);
    }
  }

  .builder-dialog__actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
  }

  /* Forced colors draw every border in one color, which would hide the
     turn; the track takes the dialog's background instead. */
  @media (forced-colors: active) {
    .builder-history__spinner {
      border-color: Canvas;
      border-top-color: CanvasText;
    }
  }
</style>
