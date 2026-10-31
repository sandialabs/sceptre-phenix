<!-- Draft History: the open draft's snapshots, newest first, with Restore and
     Delete. It speaks through its own status, because the page's region waits. -->
<template>
  <builder-dialog
    title="Draft History"
    title-id="history-dialog-title"
    data-testid="history-dialog"
    aria-describedby="history-status"
    @close="$emit('close')">
    <p v-if="store.published" id="history-status">
      {{
        store.published.source === 'file'
          ? 'A diagram read from a Builder file'
          : 'A published diagram'
      }}
      has no draft history. Edit it as a draft to keep one.
    </p>
    <template v-else>
      <div
        :aria-busy="store.historyLoading ? 'true' : undefined"
        data-testid="history-content">
        <!-- A table wider than its box scrolls in it, which then takes
             focus, so the keys scroll it too. -->
        <div
          v-if="listed"
          ref="scrollEl"
          class="builder-history__scroll"
          :class="{ 'is-scrolling': scrolls }"
          :role="scrolls ? 'region' : undefined"
          :aria-labelledby="scrolls ? 'history-dialog-title' : undefined"
          :tabindex="scrolls ? '0' : undefined"
          data-testid="history-scroll">
          <table
            ref="tableEl"
            class="builder-history"
            aria-labelledby="history-dialog-title"
            tabindex="-1"
            data-testid="history-table">
            <thead>
              <tr>
                <th scope="col" class="builder-history__number">
                  <span aria-hidden="true">#</span>
                  <span class="builder-visually-hidden">Number</span>
                </th>
                <th scope="col">Name</th>
                <th scope="col">Date</th>
                <th scope="col">User</th>
                <th
                  v-if="canChange"
                  scope="col"
                  class="builder-history__actions">
                  <span class="builder-visually-hidden">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="{ entry, index } in rows"
                :key="entry.id || index"
                :class="{ 'is-current': entry.current }"
                data-testid="history-row">
                <td class="builder-history__number">{{ index + 1 }}</td>
                <td class="builder-history__name">
                  <button
                    v-if="canChange"
                    type="button"
                    tabindex="-1"
                    class="builder-history__name-button"
                    :aria-describedby="
                      entry.current
                        ? 'history-current-restore'
                        : describedBy('history-name-note', index)
                    "
                    :aria-disabled="entry.current || busy || undefined"
                    data-testid="history-name"
                    @click="restore(entry)">
                    {{ changeName(entry, index) }}
                  </button>
                  <span v-else data-testid="history-name">{{
                    changeName(entry, index)
                  }}</span>
                  <span
                    v-if="automatic(entry)"
                    class="builder-history__badge"
                    data-testid="history-automatic">
                    <builder-icon name="save" :size="12" />
                    <span class="builder-visually-hidden">, </span>Automatic
                  </span>
                  <span
                    v-if="entry.current"
                    class="builder-history__badge builder-history__badge--current"
                    data-testid="history-current">
                    <span class="builder-visually-hidden">, </span>Current
                  </span>
                </td>
                <td class="builder-history__date">
                  <time :datetime="entry.createdAt">{{
                    formatTimestamp(entry.createdAt, { seconds: true })
                  }}</time>
                </td>
                <td class="builder-history__user">{{ entry.createdBy }}</td>
                <td v-if="canChange" class="builder-history__actions">
                  <!-- The snapshot being restored or deleted shows a ring in
                       place of the icon of its button. -->
                  <builder-busy-button
                    class="builder-history__action"
                    icon="restore"
                    :aria-label="`Restore ${snapshotName(entry, index)}`"
                    :aria-describedby="
                      entry.current
                        ? 'history-current-restore'
                        : describedBy('', index)
                    "
                    :busy="restoring === entry.id"
                    :disabled="entry.current || busy"
                    data-testid="history-restore"
                    v-on="
                      tipEvents(
                        entry.current
                          ? `Restore. ${CURRENT_RESTORE_NOTE}`
                          : restoreTip(index),
                      )
                    "
                    @click="restore(entry)" />
                  <span
                    v-if="!entry.current && restoreNote(index, total)"
                    :id="`history-restore-note-${index}`"
                    hidden>
                    {{ restoreNote(index, total) }}.
                  </span>
                  <builder-busy-button
                    class="builder-history__action"
                    icon="trash"
                    :aria-label="`Delete ${snapshotName(entry, index)}`"
                    :aria-describedby="
                      entry.current ? 'history-current-note' : undefined
                    "
                    :busy="deleting === entry.id"
                    :disabled="entry.current || busy"
                    data-testid="history-delete"
                    v-on="
                      tipEvents(
                        entry.current ? `Delete. ${CURRENT_NOTE}` : 'Delete',
                      )
                    "
                    @click="askDelete(entry, index)" />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <span id="history-current-note" hidden>{{ CURRENT_NOTE }}.</span>
        <span id="history-current-restore" hidden>
          {{ CURRENT_RESTORE_NOTE }}.
        </span>
        <span id="history-name-note" hidden>Restores this snapshot.</span>
        <p
          v-if="listed && store.serverHistory.some(automatic)"
          class="builder-hint"
          data-testid="history-automatic-hint">
          Automatic: the Builder saved changes that you did not apply in the
          Inspector. It saved them before you left, published or downloaded the
          diagram.
        </p>
      </div>

      <!-- Shown while something is in progress, or when there is nothing to
           list. Otherwise, only screen readers get the count. -->
      <p
        id="history-status"
        class="builder-dialog__message builder-history__status"
        :class="{
          'builder-visually-hidden': listed && !busy,
          'builder-history__status--empty': !statusText,
        }"
        role="status"
        data-testid="history-status">
        <span
          v-if="store.historyLoading || busy"
          class="builder-history__spinner"
          aria-hidden="true"></span>
        {{ statusText }}
      </p>
      <p class="builder-dialog__message builder-dialog__error" role="alert">
        <span v-if="store.historyError" data-testid="history-error">{{
          store.historyError
        }}</span>
        <span
          v-else-if="problem.text"
          :key="problem.key"
          data-testid="history-problem"
          >{{ problem.text }}</span
        >
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

    <builder-fixed-tooltip :tooltip="tooltip" testid="history-tooltip" />

    <builder-confirm
      v-if="confirming"
      id="history-confirm"
      title="Delete snapshot?"
      :message="`This removes ${confirming.name} from the draft history. You cannot undo this.`"
      confirm-label="Delete snapshot"
      @confirm="confirmDelete"
      @cancel="confirming = null" />
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';

  import BuilderBusyButton from '../BuilderBusyButton.vue';
  import BuilderConfirm from '../BuilderConfirm.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderFixedTooltip from '../BuilderFixedTooltip.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import { useFixedTooltip, useKeepOpenForTip } from '../fixedTooltip.js';
  import { useMessage } from './message.js';

  import { count } from '@/builder/announce.js';
  import { formatTimestamp } from '@/builder/format.js';
  import { savedAutomatically } from '@/builder/history.js';
  import { restoreNote } from '@/builder/newerHistory.js';
  import { useBuilderStore } from '@/builder/store.js';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  // The id of the snapshot being restored, or deleted, or ''.
  const restoring = ref('');
  const deleting = ref('');
  // What the last delete did, for the status line. '' for nothing yet.
  const deleted = ref('');
  // Why the last delete did not happen.
  const problem = useMessage();
  // The snapshot Delete asks about: {entry, index, name}.
  const confirming = ref(null);
  const tableEl = ref(null);

  const CURRENT_NOTE = 'You cannot delete the current snapshot';
  // Restoring it would change nothing.
  const CURRENT_RESTORE_NOTE = 'This is the current version';

  // Read before the first render, so the dialog opens loading rather than
  // showing the list it had last time. A published diagram shown read only,
  // or one read from a Builder file, has no draft, so no history.
  if (!store.published) {
    store.fetchHistory();
  }

  // Newest first. Each row keeps its snapshot's place in the history,
  // which numbers and names it.
  const rows = computed(() =>
    store.serverHistory.map((entry, index) => ({ entry, index })).reverse(),
  );

  const listed = computed(
    () =>
      !store.historyLoading &&
      !store.historyError &&
      store.serverHistory.length > 0,
  );

  const total = computed(() => store.serverHistory.length);

  // A Restore's tooltip: what a change after it removes, if anything.
  function restoreTip(index) {
    const note = restoreNote(index, total.value);

    return note ? `Restore. ${note}` : 'Restore';
  }

  // The ids that describe a Restore, or its name's button (`first`): with
  // what a change after it removes, if anything.
  function describedBy(first, index) {
    const ids = [
      first,
      restoreNote(index, total.value) && `history-restore-note-${index}`,
    ].filter(Boolean);

    return ids.length ? ids.join(' ') : undefined;
  }

  // Restore and Delete are not offered for a read-only diagram.
  const canChange = computed(() => !store.readOnly);
  const busy = computed(() => Boolean(restoring.value || deleting.value));

  const statusText = computed(() => {
    if (store.historyLoading) {
      return 'Loading the draft history…';
    }
    if (restoring.value) {
      return 'Restoring the snapshot…';
    }
    if (deleting.value) {
      return 'Deleting the snapshot…';
    }
    if (store.historyError) {
      return '';
    }

    const listing = listed.value
      ? `${count(store.serverHistory.length, 'snapshot')}, newest first.`
      : 'The server holds no snapshots for this draft yet.';

    return deleted.value ? `${deleted.value} ${listing}` : listing;
  });

  // What a snapshot changed. The first one has no summary: it is the draft
  // as created.
  function changeName(entry, index) {
    return entry.summary || (index === 0 ? 'Draft created' : 'Saved version');
  }

  // Snapshots are named by their change and time, never by their id: two
  // can share a change.
  function snapshotName(entry, index) {
    return [
      changeName(entry, index),
      formatTimestamp(entry.createdAt, { seconds: true }),
    ]
      .filter(Boolean)
      .join(', ');
  }

  // A snapshot of edits the Builder applied for the user.
  function automatic(entry) {
    return savedAutomatically(entry.summary);
  }

  // Try again disappears while the list is read. So focus moves to the
  // dialog first, not to <body> (WCAG 2.4.3). The dialog's description
  // then says it is loading.
  function retry(event) {
    event.currentTarget.closest('dialog')?.focus();
    store.fetchHistory();
  }

  // Whether the table is wider than its box, which then scrolls.
  const scrollEl = ref(null);
  const scrolls = ref(false);
  let resizing = null;

  watch(scrollEl, (element) => {
    resizing?.disconnect();
    resizing = null;

    if (!element || typeof ResizeObserver === 'undefined') {
      return;
    }

    const measure = () => {
      scrolls.value = element.scrollWidth > element.clientWidth;
    };

    resizing = new ResizeObserver(measure);
    resizing.observe(element);
    resizing.observe(element.firstElementChild);
    measure();
  });

  // --- tooltips --------------------------------------------------------

  // Beside the row's actions, Restore's before them and Delete's after, so
  // a tooltip covers neither button and the pointer moves onto it without
  // crossing the other one. Without room after the actions (a narrow
  // window, where the table may scroll the row's name up to them), both go
  // above the row instead, covering none of it.
  const tooltip = useFixedTooltip({
    side: 'end',
    beside: (button) => button.closest('td'),
    above: (button) => button.closest('tr'),
  });
  const { tip, hideTip, tipEvents } = tooltip;

  useKeepOpenForTip(() => Boolean(tip.value));

  // The dialog closes once the snapshot is restored, or once the page alert
  // says why it could not be. A second press meanwhile does nothing, and a
  // dialog closed meanwhile leaves the view's next dialog alone.
  let open = true;
  onBeforeUnmount(() => {
    open = false;
    resizing?.disconnect();
  });

  async function restore(entry) {
    if (busy.value || entry.current) {
      return;
    }

    hideTip();
    restoring.value = entry.id;
    try {
      await store.restoreSnapshot(entry.id);
    } finally {
      restoring.value = '';
    }
    if (open) {
      emit('close');
    }
  }

  // --- deleting --------------------------------------------------------

  function askDelete(entry, index) {
    if (busy.value || entry.current) {
      return;
    }

    hideTip();
    problem.clear();
    confirming.value = { entry, index, name: snapshotName(entry, index) };
  }

  // The confirmation closes first, which returns focus to the Delete
  // button. Once the row is gone, focus moves to the Delete button that
  // takes its place (the next older snapshot), or the one before it when
  // it was the last row.
  async function confirmDelete() {
    if (!confirming.value || busy.value) {
      return;
    }

    const { entry, index, name } = confirming.value;
    const row = store.serverHistory.length - 1 - index;

    confirming.value = null;
    deleting.value = entry.id;
    deleted.value = '';

    let result;
    try {
      result = await store.deleteSnapshot(entry.id);
    } finally {
      deleting.value = '';
    }

    if (!open) {
      return;
    }

    if (result.deleted) {
      deleted.value = `Deleted ${name}.`;
    } else if (result.message) {
      problem.set(result.message);
    }

    await nextTick();

    // A snapshot still listed keeps focus on its Delete.
    const buttons = [
      ...(tableEl.value?.querySelectorAll('[data-testid="history-delete"]') ||
        []),
    ];
    const kept = rows.value.findIndex((item) => item.entry.id === entry.id);
    const next = result.deleted
      ? buttons[row] || buttons[row - 1]
      : buttons[kept];

    (next || tableEl.value)?.focus();
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

  /* Reduced motion stops it turning (see builder.css). The text beside it
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

  /* On a narrow screen the table scrolls sideways here, not the page. It
     holds the headers' hidden text too, which is positioned. */
  .builder-history__scroll {
    position: relative;
    max-width: 100%;
    overflow-x: auto;
  }

  /* Separate borders, so a cell kept in view keeps its own. */
  .builder-history {
    width: 100%;
    min-width: 34rem;
    border-collapse: separate;
    border-spacing: 0;
    font-size: 0.9rem;
  }

  .builder-history th {
    font-weight: 600;
    text-align: start;
    color: var(--bx-text-muted);
    border-bottom: 1px solid var(--bx-border-strong);
  }

  .builder-history th,
  .builder-history td {
    padding: 0.3rem 0.5rem;
    vertical-align: middle;
  }

  .builder-history td {
    border-bottom: 1px solid var(--bx-border);
  }

  .builder-history tr.is-current td {
    background: var(--bx-selected-bg);
  }

  .builder-history__number {
    width: 1%;
    text-align: end;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .builder-history th.builder-history__number {
    text-align: end;
  }

  .builder-history__name {
    overflow-wrap: anywhere;
  }

  /* A name restores its snapshot on a click. It reads as the name, and its
     row's Restore button is the keyboard's way to the same action. */
  .builder-history__name-button {
    min-height: 24px;
    padding: 0;
    border: 0;
    background: none;
    color: var(--bx-accent);
    font: inherit;
    text-align: start;
    text-decoration: underline;
    cursor: pointer;
  }

  .builder-history__name-button[aria-disabled='true'] {
    color: var(--bx-text-muted);
    cursor: not-allowed;
  }

  .builder-history__date,
  .builder-history__user {
    white-space: nowrap;
  }

  /* The actions stay in view while the table scrolls under them. */
  .builder-history__actions {
    position: sticky;
    right: 0;
    width: 1%;
    background: var(--bx-surface);
    white-space: nowrap;
  }

  .is-scrolling .builder-history__actions {
    box-shadow: inset 1px 0 0 var(--bx-border-strong);
  }

  .builder-history__action {
    justify-content: center;
    min-width: 28px;
    min-height: 28px;
    padding: 0.25rem;
  }

  .builder-history__action + .builder-history__action {
    margin-inline-start: 0.25rem;
  }

  /* An action that does not apply (the current snapshot's, or any while
     one is in progress) has the Builder's aria-disabled fill. It also has a
     dashed frame and a fainter icon, which keep 3:1 against the fill
     (WCAG 1.4.11). Thus it cannot pass for the one beside it. */
  .builder-history__action[aria-disabled='true'] {
    border-style: dashed;
    color: var(--bx-border-strong);
  }

  /* The marks of a snapshot the Builder saved for the user, and of the
     current one. Their borders show in forced colors too, and they wrap
     under the name when the column is narrow. */
  .builder-history__badge {
    display: inline-flex;
    align-items: center;
    gap: 0.2rem;
    margin-inline-start: 0.4rem;
    padding: 0 0.3rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: var(--bx-radius);
    font-size: 0.75rem;
    white-space: nowrap;
  }

  .builder-history__badge--current {
    border-color: var(--bx-accent);
    color: var(--bx-accent);
    font-weight: 600;
  }

  @media (pointer: coarse) {
    .builder-history__action,
    .builder-history__name-button {
      min-width: 44px;
      min-height: 44px;
    }
  }

  /* Forced colors draw every border in one color, which would hide the
     turn. The track takes the dialog's background instead. */
  @media (forced-colors: active) {
    .builder-history__spinner {
      border-color: Canvas;
      border-top-color: CanvasText;
    }

    .builder-history__name-button {
      color: LinkText;
    }

    .builder-history__name-button[aria-disabled='true'],
    .builder-history__action[aria-disabled='true'] {
      border-color: GrayText;
      color: GrayText;
    }

    .builder-history tr.is-current td {
      background: none;
    }
  }
</style>
