// State of health across experiments: the verdict for each experiment's
// summary (GET /soh) and the numbers the State of Health tab shows.

// An experiment is degraded, rather than only failing checks, when any VM
// that should be up is down, when at least this share of its checked hosts
// fail a check, or when fewer than this share of its reachability pairs pass.
const DEGRADED_HOST_SHARE = 0.2;
export const HEALTHY_REACHABILITY = 0.9;

// verdicts, worst first; rank orders the SoH column
export const VERDICTS = {
  degraded: {
    label: 'degraded',
    type: 'is-danger',
    icon: 'times-circle',
    rank: 0,
  },
  failing: {
    label: 'checks failing',
    type: 'is-warning',
    icon: 'exclamation-triangle',
    rank: 1,
  },
  waiting: {
    label: 'waiting for start',
    type: 'is-light',
    icon: 'hourglass-half',
    rank: 2,
  },
  healthy: {
    label: 'healthy',
    type: 'is-success',
    icon: 'check-circle',
    rank: 3,
  },
  unconfigured: {
    label: 'not configured',
    type: 'is-unconfigured',
    icon: 'ban',
    rank: 4,
  },
};

// the table's filters and the verdicts each one shows
export const FILTERS = [
  { key: 'all', label: 'All', verdicts: null },
  {
    key: 'attention',
    label: 'Needs attention',
    verdicts: ['degraded', 'failing'],
  },
  { key: 'healthy', label: 'Healthy', verdicts: ['healthy'] },
  {
    key: 'nodata',
    label: 'No SOH data',
    verdicts: ['waiting', 'unconfigured'],
  },
];

// VMs that should be running but are not (stopped or flushed); VMs that are
// not booted on purpose, external, or waiting for a delayed start are not down
export function vmsDown(summary) {
  const vms = summary?.vms;
  if (!vms?.known) return 0;
  return (vms.notrunning ?? 0) + (vms.notdeploy ?? 0);
}

// whether the experiment is up and its soh app has results to judge
export function sohActive(summary) {
  return (
    !!summary?.configured &&
    !!summary.running &&
    summary.status === 'started' &&
    (!!summary.soh_initialized || (summary.checks?.total ?? 0) > 0)
  );
}

// share of reachability pairs passing, from 0 to 1, or null with none tested
export function reachabilityShare(summary) {
  const reach = summary?.reachability;
  if (!reach?.total) return null;
  return reach.passing / reach.total;
}

// The experiment's verdict: one of the VERDICTS keys.
export function verdict(summary) {
  if (!summary?.configured) return 'unconfigured';
  if (!sohActive(summary)) return 'waiting';

  const hosts = summary.hosts ?? 0;
  const hostShare = hosts ? (summary.hosts_with_errors ?? 0) / hosts : 0;
  const reach = reachabilityShare(summary);

  if (
    vmsDown(summary) > 0 ||
    hostShare >= DEGRADED_HOST_SHARE ||
    (reach !== null && reach < HEALTHY_REACHABILITY)
  ) {
    return 'degraded';
  }

  if ((summary.checks?.failing ?? 0) > 0) return 'failing';
  return 'healthy';
}

// The verdict's label, naming why the experiment is waiting when it is.
export function verdictLabel(summary) {
  const key = verdict(summary);
  if (key === 'waiting' && summary?.running && summary.status === 'started') {
    return 'waiting for first run';
  }
  return VERDICTS[key].label;
}

// counts of experiments per filter key
export function filterCounts(summaries) {
  const counts = {};
  for (const filter of FILTERS) {
    counts[filter.key] = summaries.filter(
      (s) => !filter.verdicts || filter.verdicts.includes(verdict(s)),
    ).length;
  }
  return counts;
}

// The numbers for the line above the table.
export function overview(summaries) {
  const running = summaries.filter((s) => s.running);
  return {
    active: summaries.filter(sohActive).length,
    total: summaries.length,
    hostsFailing: running.reduce((n, s) => n + (s.hosts_with_errors ?? 0), 0),
    vmsDown: running.reduce((n, s) => n + vmsDown(s), 0),
  };
}

// The VM health bar's segments, in drawing order, without empty ones. Colors
// come from the topology graph's legend, except that its yellow means not
// deployed there and delayed start here (not-deployed VMs count as down).
export const VM_COLORS = {
  ok: '#4F8F00',
  failing: '#FF9900',
  down: '#670b00',
  notboot: '#000000',
  delayed: '#FFD479',
  external: '#005493',
};

export function vmSegments(summary) {
  const vms = summary?.vms;
  if (!summary?.running || !vms?.known) return [];

  const segments = [
    ['ok', (vms.running ?? 0) - (vms.failing ?? 0), 'ok'],
    ['failing', vms.failing ?? 0, 'failing'],
    ['down', vmsDown(summary), 'down'],
    ['delayed', vms.delayed ?? 0, 'delayed'],
    ['notboot', vms.notboot ?? 0, 'not booted'],
    ['external', vms.external ?? 0, 'ext'],
  ];

  return segments
    .filter(([, count]) => count > 0)
    .map(([key, count, label]) => ({
      key,
      count,
      label,
      color: VM_COLORS[key],
    }));
}

// The failing checks and down VMs to list for an experiment: failing checks
// first (newest first, as the server sends them), then VMs that are down.
export function problems(summary) {
  const checks = (summary?.failing_checks ?? []).map((c, i) => ({
    id: `check-${i}`,
    host: c.host,
    check: c.check,
    target: c.target,
    error: c.error,
    time: c.time,
  }));
  const down = (summary?.down_vms ?? []).map((name) => ({
    id: `vm-${name}`,
    host: name,
    check: 'vm',
    target: '',
    error: 'VM is not running',
    time: null,
  }));
  return [...checks, ...down];
}

// How many problems the experiment has in all, including any the summary
// left out of its lists.
export function problemCount(summary) {
  if (!summary?.running) return 0;
  return (summary.checks?.failing ?? 0) + vmsDown(summary);
}

// The failing-check counts of the last `count` runs, oldest first.
export function trend(summary, count) {
  return (summary?.history ?? []).slice(-count).map((run) => run.failing ?? 0);
}

// An SVG polyline's points for values in a width x height box, with pad
// pixels kept clear top and bottom. A flat line sits at the bottom.
export function sparkPoints(values, width, height, pad = 2) {
  if (values.length === 0) return [];
  const max = Math.max(...values);
  const step = values.length > 1 ? width / (values.length - 1) : 0;
  return values.map((v, i) => [
    Math.round(i * step * 10) / 10,
    Math.round(
      (height - pad - (max > 0 ? (v / max) * (height - 2 * pad) : 0)) * 10,
    ) / 10,
  ]);
}

// "14:03:40": a time of day in the browser's zone, or '' for no time
export function clockTime(time) {
  if (!time) return '';
  const date = new Date(time);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString([], { hour12: false });
}
