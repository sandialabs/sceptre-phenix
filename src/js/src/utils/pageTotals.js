// The counts the Configs, Users and SCORCH pages show beside their search
// boxes, of everything listed rather than only the rows shown.

// the config kinds phēnix stores, in the order the Configs page lists them
export const CONFIG_KINDS = [
  'Topology',
  'Scenario',
  'Experiment',
  'Image',
  'User',
  'Role',
];

// the configs, and how many there are of each kind present: the known kinds
// in CONFIG_KINDS order, then any others by name
export function configTotals(configs) {
  const byKind = new Map();
  for (const cfg of configs ?? []) {
    byKind.set(cfg.kind, (byKind.get(cfg.kind) ?? 0) + 1);
  }
  const rank = (kind) => {
    const i = CONFIG_KINDS.indexOf(kind);
    return i < 0 ? CONFIG_KINDS.length : i;
  };
  const kinds = [...byKind.keys()].sort(
    (a, b) => rank(a) - rank(b) || String(a).localeCompare(String(b)),
  );
  return {
    configs: configs?.length ?? 0,
    kinds: kinds.map((kind) => ({ kind, count: byKind.get(kind) })),
  };
}

// the users, the distinct roles they hold, and those not yet given one
export function userTotals(users) {
  const roles = new Set();
  let unassigned = 0;
  for (const user of users ?? []) {
    if (user.role_name) roles.add(user.role_name);
    else unassigned += 1;
  }
  return { users: users?.length ?? 0, roles: roles.size, unassigned };
}

// the SCORCH page's experiments, their pipelines, and those with one running
export function scorchTotals(experiments) {
  const totals = { experiments: 0, pipelines: 0, running: 0 };
  for (const exp of experiments ?? []) {
    totals.experiments += 1;
    totals.pipelines += exp.scorch?.runs?.length ?? 0;
    if (exp.scorch?.running) totals.running += 1;
  }
  return totals;
}

// How a pipeline's last run ended, from its components: running, failed when
// any component failed, succeeded once its done node succeeded, else null.
export function runOutcome(run) {
  if (run.running) return 'running';
  const nodes = run.nodes ?? [];
  if (nodes.some((node) => node.status === 'failure')) return 'failed';
  if (nodes.some((node) => node.name === 'done' && node.status === 'success')) {
    return 'succeeded';
  }
  return null;
}

// an experiment's pipelines, by how their last run went
export function scorchRunTotals(runs) {
  const totals = { pipelines: 0, running: 0, succeeded: 0, failed: 0 };
  for (const run of runs ?? []) {
    totals.pipelines += 1;
    const outcome = runOutcome(run);
    if (outcome) totals[outcome] += 1;
  }
  return totals;
}
