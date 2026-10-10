// What publishing changes, as the Publish dialog shows it: the server's
// answer to a dry run of the intent the dialog's form builds (see
// previewPublish in store.js), with what the publication would warn of, or
// the refusal it would answer with.
//
// It is read when asked (refresh), and read again after a pause whenever
// the form, the lists the form reads or the saved draft change (schedule).
// From such a change on, what is shown is no longer current: it stays
// shown, marked busy (loading, which the dialog shows as aria-busy), and an
// answer still on its way, which is for the form as it was, is dropped. An
// answer that arrives once the dialog has closed is dropped too. blocked is
// why the form makes no intent, and error why the answer could not be read;
// neither stops Publish. error adds that Publish still works only while the
// dialog lets the user publish, so it never contradicts a Publish button
// the checks keep unavailable.

import { reactive } from 'vue';

import { useMessage } from './message.js';

import { publishChangesSummary } from '@/builder/publish.js';

// How long the form stays unchanged before what publishing changes is read
// again.
export const PREVIEW_DELAY_MS = 300;

/**
 * @param {object} options
 * @param {function(): {intent: object|null, error: string}} options.buildIntent
 *   the form's intent, or why it makes none
 * @param {function(object): Promise<object|null>} options.previewPublish
 *   asks the server, as store.previewPublish does: the answer, a failure
 *   ({failed, message}), or null for an answer the store dropped
 * @param {function(): boolean} options.idle true while nothing is to be
 *   read: the draft is read only, or the dialog shows a result
 * @param {function(): boolean} [options.publishable] false while the
 *   dialog keeps Publish unavailable (the checks' errors, for example)
 * @param {number} [options.delay] the pause, in milliseconds
 * @returns {{preview: object, status: object, refresh: function(): Promise<void>,
 *   schedule: function(): void, close: function(): void}} preview holds
 *   loading, changes, issues (errors, then warnings), error and blocked;
 *   status is the polite line that says the list was read again
 */
export function usePublishPreview({
  buildIntent,
  previewPublish,
  idle,
  publishable = () => true,
  delay = PREVIEW_DELAY_MS,
}) {
  const preview = reactive({
    loading: true,
    changes: null,
    issues: [],
    error: '',
    blocked: '',
  });
  const status = useMessage();
  let timer = null;
  // Bumped by every change and every reading, so an answer asked for
  // before the latest of them is not shown.
  let runs = 0;
  let closed = false;

  function cancel() {
    clearTimeout(timer);
    timer = null;
  }

  // Reads what publishing changes now.
  async function refresh() {
    cancel();

    if (closed || idle()) {
      return;
    }

    runs += 1;

    const run = runs;
    const { intent, error: problem } = buildIntent();

    if (!intent) {
      Object.assign(preview, {
        loading: false,
        changes: null,
        issues: [],
        error: '',
        blocked: problem,
      });

      return;
    }

    preview.loading = true;
    preview.blocked = '';

    const answer = await previewPublish(intent);

    // A later change, or the dialog closing, made this answer stale.
    if (closed || !answer || run !== runs) {
      return;
    }

    if (answer.failed) {
      Object.assign(preview, {
        loading: false,
        changes: null,
        issues: [],
        error: publishable()
          ? `${answer.message} You can still publish.`
          : answer.message,
        blocked: '',
      });
      status.set(preview.error);

      return;
    }

    const warnings = answer.warningIssues || [];

    Object.assign(preview, {
      loading: false,
      changes: answer.changes,
      issues: [...(answer.errorIssues || []), ...warnings],
      error: '',
      blocked: '',
    });
    status.set(
      answer.changes
        ? publishChangesSummary(answer.changes, warnings.length)
        : 'What publishing changes: the server would refuse to publish.',
    );
  }

  // Reads it again once the form has stayed unchanged for the pause.
  function schedule() {
    cancel();

    if (closed || idle()) {
      return;
    }

    runs += 1;
    preview.loading = true;
    timer = setTimeout(refresh, delay);
  }

  // The dialog closed: nothing more is read, and no answer is shown.
  function close() {
    closed = true;
    cancel();
  }

  return { preview, status, refresh, schedule, close };
}
