// What publishing changes: the server's dry run of the Publish form's intent
// (see previewPublish in store.js). An answer for an old form is dropped, and
// the preview shown stays, marked busy, until the current one arrives.

import { reactive } from 'vue';

import { useMessage } from './message.js';

import { publishChangesSummary } from '@/builder/publish.js';

// How long the form stays unchanged before what publishing changes is read
// again.
export const PREVIEW_DELAY_MS = 300;

/**
 * Reads the preview now (refresh) or after a pause (schedule). `idle` stops
 * reads, and the error says Publish still works only while `publishable`.
 */
export function usePublishPreview({
  buildIntent,
  previewPublish,
  idle,
  publishable = () => true,
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
    timer = setTimeout(refresh, PREVIEW_DELAY_MS);
  }

  // The dialog closed: nothing more is read, and no answer is shown.
  function close() {
    closed = true;
    cancel();
  }

  return { preview, status, refresh, schedule, close };
}
