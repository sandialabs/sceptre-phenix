import { h } from 'vue';

import {
  brokenDisks,
  brokenDisksSummary,
  diskProblemLine,
  jobDiskProblems,
} from '@/utils/brokenDisks.js';
import { escapeHTML } from '@/utils/escapeHTML.js';
import { openNotification } from '@/utils/notify.js';
import { namedList, plural, pluralWord, upperFirst } from '@/utils/plural.js';
import { noResponseMessage } from '@/utils/unreachable.js';

// What the UI shows before and after an operation on one thing or several:
// whether an action can be taken and why not, which of a selection it skips,
// one confirm dialog for it, and one summary of how it went. The VM actions,
// disk actions, experiment files, and SCORCH pages share these so they always
// read the same.

// the most failures an outcome notification names; it counts the rest
export const LISTED_FAILURES = 8;

// Whether and how to offer the action its first argument describes: why it is
// `unavailable` at all, whether the role is `permitted` it, and why the thing's
// state has it `blocked` now. The result is `permitted` if offered, and
// `disabled` with a `reason` that reads "Can't <verb>: ..." when it cannot be
// taken now; the verb is the label in lower case unless given.
export function actionState(
  { name, label, verb, description, icon, type, outlined },
  { permitted = true, unavailable = null, blocked = null } = {},
) {
  const result = {
    name,
    label,
    description,
    icon,
    type: type ?? null,
    outlined: !!outlined,
    permitted: true,
    disabled: false,
    reason: null,
  };

  const deny = (reason, isPermitted) => ({
    ...result,
    permitted: isPermitted,
    disabled: true,
    reason: `Cannot ${verb ?? label.toLowerCase()}: ${reason}`,
  });

  if (unavailable) return deny(unavailable, false);
  if (!permitted) return deny('you do not have permission', false);
  if (blocked) return deny(blocked, true);
  return result;
}

// Splits the names an action on a selection was asked for by `blocked`, the
// rule that disables the action for one: `allowed` lists those it can act
// on, and `skipped` groups the rest by why they were left out. `lookup` finds
// a name's item; one it cannot find is skipped for `missingReason`.
export function partitionForAction(names, lookup, blocked, missingReason) {
  const allowed = [];
  const skipped = new Map();
  for (const name of [names].flat()) {
    const item = lookup(name);
    const reason = item ? blocked(item) : missingReason;
    if (reason) skipped.set(reason, [...(skipped.get(reason) ?? []), name]);
    else allowed.push(name);
  }
  return {
    allowed,
    skipped: [...skipped].map(([reason, list]) => ({ reason, names: list })),
  };
}

const PLURAL_VERBS = { is: 'are', does: 'do' };

const escapeRegExp = (text) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

// One sentence for each reason partitionForAction skipped things for,
// turning a reason about "the <noun>" into one about all of them: "The VMs a
// and b are not running."
export function skippedText(skipped, noun) {
  const subjectRe = new RegExp(`^the ${escapeRegExp(noun)} (is|does)\\b`);
  return (skipped ?? [])
    .map(({ reason, names }) => {
      const subject = upperFirst(namedList(names, noun));
      const text = reason.replace(
        subjectRe,
        (_, verb) =>
          `${subject} ${pluralWord(names.length, verb, PLURAL_VERBS[verb])}`,
      );
      return `${text}.`;
    })
    .join(' ');
}

// Opens one confirm dialog for an operation, saying under its message (HTML)
// which of the selection it skips and why, and resolves true when confirmed.
export function confirmOperation(
  buefy,
  { title, message, confirmText, type = 'is-primary', skipped = [], noun },
) {
  // grey-lighter is 5:1 on the dialog's $background-ter
  const skippedNote = skipped.length
    ? `<p class="mt-3 has-text-grey-lighter">${escapeHTML(skippedText(skipped, noun))}</p>`
    : '';

  return new Promise((resolve) => {
    buefy.dialog.confirm({
      title,
      message: `<p>${message}</p>${skippedNote}`,
      confirmText,
      cancelText: 'Cancel',
      type,
      hasIcon: true,
      onConfirm: () => resolve(true),
      onCancel: () => resolve(false),
    });
  });
}

// Splits an error chain into its steps, outermost first. The server nests
// errors with hashicorp/go-multierror, which writes each level as
// "1 error occurred:\n\t* cause", so a failure deep in a SCORCH run otherwise
// reads as one long run-on sentence.
export function errorSteps(text) {
  // bullets come only after a marker, so text before one keeps its " * "
  const [head, ...lists] = String(text ?? '').split(
    /\s*\d+ errors? occurred:\s*/,
  );
  return [head]
    .concat(
      lists.flatMap((list) =>
        list.split(/(?:^|\n)\s*\*\s+|^\*\s+|\s\*\s+(?=\S)/),
      ),
    )
    .map((step) => step.trim().replace(/:$/, '').trim())
    .filter(Boolean);
}

// A response body as the server's JSON Error, parsing one a request for text
// left as a string; null when it is not one.
export function errorBody(data) {
  if (data && typeof data === 'object' && !(data instanceof Blob)) return data;
  if (typeof data !== 'string' || !data.trimStart().startsWith('{'))
    return null;
  try {
    const body = JSON.parse(data);
    return body && typeof body === 'object' ? body : null;
  } catch {
    return null;
  }
}

// An error's message and cause as text: from a string, an Error, or a
// request error, whose body may be the server's JSON error, text, or a Blob
// (a download's), which cannot be read at once and so gives the status.
function errorParts(err) {
  if (err == null) return ['Unknown error'];
  if (typeof err === 'string') return [err];

  const response = err.response;
  if (!response) return [noResponseMessage(err) ?? err.message ?? String(err)];

  const data = response.data;
  const body = errorBody(data);
  if (body) return [body.message ?? err.message, body.cause];
  if (typeof data === 'string' && data.trim()) return [data];
  return [
    response.statusText
      ? `${response.status} ${response.statusText}`
      : (err.message ?? `Request failed with status ${response.status}`),
  ];
}

// An error as a title and the steps that led to it, the innermost cause
// last.
export function errorText(err) {
  const [message, cause] = errorParts(err);
  const steps = [...errorSteps(message), ...errorSteps(cause)];
  const title = steps.shift() ?? 'Unknown error';
  return { title, steps };
}

// One line for an error: its title and its steps.
export function errorLine(err) {
  const { title, steps } = errorText(err);
  return [title, ...steps].join(': ');
}

// statuses that say trying again later may work: busy (409, 423), too many
// requests (429), and a proxy or the server not answering (502-504)
const RETRYABLE_STATUSES = new Set([409, 423, 429, 502, 503, 504]);

// Whether trying a failed request again may succeed: it got no answer, timed
// out, or the server said it is busy.
export function isRetryable(err) {
  if (!err || typeof err !== 'object') return false;
  if (err.code === 'ERR_CANCELED') return false;
  if (err.code === 'ECONNABORTED' || err.code === 'ETIMEDOUT') return true;
  if (!err.response) return err.isAxiosError === true || !!err.request;
  return RETRYABLE_STATUSES.has(err.response.status);
}

// The outcome of each name's request, from Promise.allSettled's results in
// the same order: { name, status, error, retryable }, and `details` lines for
// a start refused for broken disks.
export function outcomeFromSettled(names, results) {
  return names.map((name, i) => {
    const result = results[i];
    if (result?.status === 'fulfilled') {
      return { name, status: 'succeeded', error: null, retryable: false };
    }
    const disks = brokenDisks(errorBody(result?.reason?.response?.data));
    if (disks) {
      const vms = new Set(disks.map((disk) => disk.vm)).size;
      return {
        name,
        status: 'failed',
        error: brokenDisksSummary(disks.length, vms),
        retryable: false,
        details: disks.map((disk) => `VM ${disk.vm}: ${diskProblemLine(disk)}`),
      };
    }
    return {
      name,
      status: 'failed',
      error: errorLine(result?.reason),
      retryable: isRetryable(result?.reason),
    };
  });
}

// The outcome of each of a background job's items, with `details` lines for
// a VM failed for its broken disks.
export function outcomeFromJob(job) {
  return (job?.items ?? []).map((item) => {
    const outcome = {
      name: item.name,
      status: item.status,
      error: item.error ?? null,
      retryable: !!item.retryable,
    };
    const problems = jobDiskProblems(item.error);
    if (problems?.length) {
      outcome.error = brokenDisksSummary(problems.length);
      outcome.details = problems;
    }
    return outcome;
  });
}

// How many items succeeded, failed, were skipped, and were canceled, and the
// names of the failures worth retrying.
export function summarizeOutcome(items) {
  const summary = {
    succeeded: 0,
    failed: 0,
    skipped: 0,
    canceled: 0,
    retryable: [],
  };
  for (const item of items ?? []) {
    if (item.status in summary) summary[item.status] += 1;
    if (item.status === 'failed' && item.retryable) {
      summary.retryable.push(item.name);
    }
  }
  return summary;
}

// The one-line summary of an outcome: "Restarted 3 VMs", or "Restarted 2 of
// 3 VMs: 1 failed, 1 skipped".
export function outcomeTitle({ verbPast, noun, items }) {
  const total = (items ?? []).length;
  const counts = summarizeOutcome(items);
  const count = plural(total, noun);
  if (counts.succeeded === total) return `${verbPast} ${count}`;

  const rest = ['failed', 'skipped', 'canceled']
    .filter((status) => counts[status] > 0)
    .map((status) => `${counts[status]} ${status}`);
  return `${verbPast} ${counts.succeeded} of ${count}: ${rest.join(', ')}`;
}

// The entries naming an outcome's failures and skips, at most
// LISTED_FAILURES: { line, details }, details the item's lines, if any.
function outcomeEntries(items) {
  const unsuccessful = (items ?? []).filter(
    (item) => item.status === 'failed' || item.status === 'skipped',
  );
  const entries = unsuccessful.slice(0, LISTED_FAILURES).map((item) => ({
    line: item.error
      ? `${item.name}: ${item.error}`
      : `${item.name}: ${item.status}`,
    details: item.details ?? [],
  }));
  if (unsuccessful.length > LISTED_FAILURES) {
    entries.push({
      line: `and ${unsuccessful.length - LISTED_FAILURES} more`,
      details: [],
    });
  }
  return entries;
}

// the lines naming an outcome's failures and skips, at most LISTED_FAILURES
export const outcomeLines = (items) =>
  outcomeEntries(items).map((entry) => entry.line);

// An entry's details, in a disclosure the keyboard reaches: open when it is
// the only entry, so one failure shows them at once.
const entryDetails = (details, only) =>
  h('details', { class: 'error-details', open: only }, [
    h('summary', `Show ${plural(details.length, 'problem')}`),
    h(
      'ul',
      details.map((line) => h('li', line)),
    ),
  ]);

// Tells how an operation on several items went: a toast when every item
// succeeded, and otherwise one lasting notification with the summary, the
// failures (up to LISTED_FAILURES), and, when some may work if tried again
// and `onRetry` is given, a Retry failed button that calls it with their
// names. Nothing is shown for an operation on no items.
export function notifyOutcome(buefy, { verbPast, noun, items, onRetry }) {
  if (!items?.length) return;

  const title = outcomeTitle({ verbPast, noun, items });
  const counts = summarizeOutcome(items);

  if (counts.succeeded === items.length) {
    buefy.toast.open({
      message: escapeHTML(title),
      type: 'is-success',
    });
    return;
  }

  const retry = onRetry && counts.retryable.length ? counts.retryable : null;
  let notice;

  const body = h(
    'span',
    { role: 'alert' },
    [
      h('span', { class: 'error-title' }, [h('b', title)]),
      ...outcomeEntries(items).flatMap(({ line, details }, _, entries) => [
        h('span', { class: 'error-step' }, line),
        ...(details.length
          ? [entryDetails(details, entries.length === 1)]
          : []),
      ]),
      retry &&
        h(
          'button',
          {
            type: 'button',
            class: 'button is-small is-light mt-2',
            onClick: () => {
              notice?.close();
              onRetry(retry);
            },
          },
          `Retry failed (${retry.length})`,
        ),
    ].filter(Boolean),
  );

  notice = openNotification({
    type: counts.succeeded > 0 ? 'is-warning' : 'is-danger',
    hasIcon: true,
    position: 'is-top',
    indefinite: true,
    ariaCloseLabel: 'Dismiss',
    message: body,
  });
}
