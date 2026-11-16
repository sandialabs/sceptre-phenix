// The VLAN checks and confirmations that the VLANs page, the VLAN IDs menu
// and the config editor share.
import * as vlansApi from '@/api/vlans.js';
import { escapeHTML } from '@/utils/escapeHTML.js';
import { confirmOperation } from '@/utils/operations.js';
import { roleAllowed } from '@/utils/rbac.js';
import { conflictIssues } from '@/utils/vlans.js';

// the issue kinds that name no other experiment, only what minimega holds
const CLUSTER_KINDS = new Set(['pin-blacklisted', 'unowned']);

// Whether the role may save over these issues with force: it must see each
// other experiment they name, and the blacklist for a blacklisted pin.
function forceAllowed(issues, experiment) {
  return issues.every((issue) => {
    if (/another experiment/.test(issue.message ?? '')) return false;
    if (CLUSTER_KINDS.has(issue.kind) && !roleAllowed('vlans', 'list', '*')) {
      return false;
    }
    return (issue.experiments ?? [])
      .filter((name) => name !== experiment)
      .every((name) => roleAllowed('vlans', 'list', name));
  });
}

// the issues' messages as an HTML list, each once
const issueList = (issues) =>
  `<ul class="vlan-issue-list">${[
    ...new Set(issues.map((issue) => issue.message).filter(Boolean)),
  ]
    .map((message) => `<li>${escapeHTML(message)}</li>`)
    .join('')}</ul>`;

// Asks whether to save over the issues; resolves true to save anyway.
const confirmVlanIssues = (buefy, issues, intro) =>
  confirmOperation(buefy, {
    title: 'VLAN Conflicts',
    message: `${escapeHTML(intro)}</p>${issueList(issues)}<p>Save anyway?`,
    confirmText: 'Save anyway',
    type: 'is-warning',
  });

// Checks a proposed VLAN spec against the cluster before a save, and asks
// about any issues. Resolves true to save. A failed check does not block the
// save: the server checks the spec again when the experiment starts.
export async function checkBeforeSave(buefy, spec) {
  if (!roleAllowed('vlans', 'list', spec.experiment)) return true;
  let issues;
  try {
    issues = (await vlansApi.checkVlans(spec)).data?.issues ?? [];
  } catch {
    return true;
  }
  if (!issues.length) return true;
  return confirmVlanIssues(
    buefy,
    issues,
    `The VLAN IDs of ${spec.experiment} have these issues in the cluster:`,
  );
}

// The aliases and range of an Experiment config, as POST /vlans/check takes
// them.
export function specOfConfig(config) {
  const vlans = config?.spec?.vlans ?? {};
  const aliases = {};
  for (const [alias, id] of Object.entries(vlans.aliases ?? {})) {
    const n = Number(id);
    if (Number.isInteger(n) && n > 0) aliases[alias] = n;
  }
  return {
    experiment: config?.metadata?.name ?? '',
    aliases,
    min: Number(vlans.min) || 0,
    max: Number(vlans.max) || 0,
  };
}

// Sends a pin or range change with send(force). A 409 for conflicts the role
// may save over asks first, then sends it again with force. Resolves to the
// response, or null when the user keeps the old IDs; other errors reject.
export async function saveWithForce(buefy, experiment, send) {
  try {
    return await send(false);
  } catch (err) {
    const issues = conflictIssues(err);
    if (!issues || !forceAllowed(issues, experiment)) throw err;
    const sure = await confirmVlanIssues(
      buefy,
      issues,
      `This change gives ${experiment} these issues in the cluster:`,
    );
    return sure ? send(true) : null;
  }
}

// how long a dialog waits after the last key before it checks the IDs typed
export const CHECK_DELAY_MS = 300;

// A check of proposed IDs as they are typed: run(spec) checks spec once
// typing stops and calls apply({ checking, issues, failed }); a newer run
// or cancel() drops an older answer.
export function createVlanCheck(apply) {
  let timer = null;
  let n = 0;
  const run = (spec) => {
    clearTimeout(timer);
    const mine = ++n;
    apply({ checking: true, issues: null, failed: false });
    timer = setTimeout(async () => {
      try {
        const issues = (await vlansApi.checkVlans(spec)).data?.issues ?? [];
        if (mine === n) apply({ checking: false, issues, failed: false });
      } catch {
        if (mine === n) apply({ checking: false, issues: null, failed: true });
      }
    }, CHECK_DELAY_MS);
  };
  run.cancel = () => {
    clearTimeout(timer);
    n++;
  };
  return run;
}
