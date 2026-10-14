// How fresh each page's data is, and the rules that decide it: the one
// registry of the UI's data caches and reuse windows (the server's are in
// src/go/util/freshness/doc.go).
//
// The server tags every REST response with X-Phenix-Seq and every websocket
// message with seq, from one sequence that only increases within a server
// process: data tagged S reflects every change whose message has a seq of S or
// less. Responses built from minimega reads also carry X-Phenix-Observed-At
// (when the oldest of the data was read) and X-Phenix-Stale (earlier data
// served while minimega was busy). Ages are measured from the server's own
// Date header, so the browser's clock never matters.

const SECOND = 1000;
const MINUTE = 60 * SECOND;

const type = (msg) => msg.resource?.type;

const isExperiment = (msg) => type(msg) === 'experiment';
// experiments created or edited as configs are announced as configs
const isExperimentConfig = (msg) =>
  type(msg) === 'config' && !!msg.resource.name?.startsWith('Experiment/');

// whether msg tells of the experiment `name` or one of its VMs
const aboutExperiment = (msg, name) => {
  const target = msg.resource?.name ?? '';
  return (
    (isExperiment(msg) && target === name) ||
    (type(msg)?.startsWith('experiment/') && target.startsWith(`${name}/`)) ||
    (isExperimentConfig(msg) && target === `Experiment/${name}`)
  );
};

// Each page's data, by page cache key:
//
//   live           the page is kept current by websocket messages, so its
//                  data may be out of date while the websocket is down
//   observed       the server reports when it read the data from minimega
//   reuseMs        how long other pages reuse a cached copy instead of
//                  asking again (see recentPage)
//   pollMs         how often the open page reloads
//   staleAfterMs   past this age the data is shown as may be out of date
//   invalidatedBy  websocket messages after which a cached copy is out of
//                  date and is loaded again on the next visit; each is given
//                  the message and the cache key
export const RESOURCES = {
  experiments: {
    live: true,
    reuseMs: 5 * SECOND,
    invalidatedBy: [isExperiment, isExperimentConfig],
  },
  configs: {
    live: false,
    staleAfterMs: 10 * MINUTE,
    invalidatedBy: [(msg) => type(msg) === 'config'],
  },
  hosts: {
    observed: true,
    pollMs: 30 * SECOND,
    reuseMs: MINUTE,
    staleAfterMs: 90 * SECOND,
  },
  disks: {
    live: true,
    reuseMs: MINUTE,
    invalidatedBy: [(msg) => type(msg) === 'disks'],
  },
  users: { live: true, invalidatedBy: [(msg) => type(msg) === 'user'] },
  logs: { live: true },
  scorch: {
    live: true,
    invalidatedBy: [(msg) => type(msg) === 'apps/scorch', isExperiment],
  },
  soh: {
    observed: true,
    staleAfterMs: 5 * MINUTE,
    invalidatedBy: [isExperiment],
  },
  settings: { live: false, staleAfterMs: 10 * MINUTE },
  vmtiles: { observed: true, pollMs: MINUTE, staleAfterMs: 3 * MINUTE },
};

// Rules for the keys that name one experiment: its page, running or stopped,
// and its SCORCH runs page.
const PREFIXES = [
  {
    prefix: 'scorchruns/',
    resource: {
      live: true,
      invalidatedBy: [
        (msg, key) =>
          type(msg) === 'apps/scorch' &&
          (msg.resource.name ?? '').split('/')[0] === key.slice(11),
      ],
    },
  },
  {
    prefix: 'experiment/',
    resource: {
      live: true,
      invalidatedBy: [
        (msg, key) => aboutExperiment(msg, key.split('/')[1] ?? ''),
      ],
    },
  },
];

// The rules for a page cache key, or undefined for a key without any.
export function resourceFor(key) {
  if (!key) return undefined;
  if (RESOURCES[key]) return RESOURCES[key];
  return PREFIXES.find(({ prefix }) => key.startsWith(prefix))?.resource;
}

// What is known of how fresh one load's data is. A load made of several
// requests combines them (see readFreshness), so it is never reported newer
// than its oldest part. It is a class, not a plain object, so axios passes it
// through a request's config by reference rather than copying it.
export class FreshnessMeta {
  constructor() {
    this.seq = undefined; // the lowest X-Phenix-Seq of the load's responses
    this.observedAt = undefined; // ms on the browser's clock
    this.stale = false;
  }
}

const header = (headers, name) =>
  typeof headers?.get === 'function'
    ? (headers.get(name) ?? undefined)
    : headers?.[name];

// Folds one response's freshness headers into meta: sentAt and receivedAt
// are when its request was sent and its response arrived, on the browser's
// clock. Without X-Phenix-Observed-At the data is taken as read when the
// request was sent.
export function readFreshness(meta, headers, sentAt, receivedAt) {
  const seq = Number(header(headers, 'x-phenix-seq'));
  if (Number.isFinite(seq) && seq > 0) {
    meta.seq = Math.min(meta.seq ?? Infinity, seq);
  }

  let observedAt = sentAt;
  const observed = Date.parse(header(headers, 'x-phenix-observed-at'));
  const date = Date.parse(header(headers, 'date'));
  if (Number.isFinite(observed) && Number.isFinite(date)) {
    // Date has whole seconds, so a fresh read can look up to 1 s ahead
    observedAt = receivedAt - Math.max(0, date - observed);
  }
  if (Number.isFinite(observedAt)) {
    meta.observedAt = Math.min(meta.observedAt ?? Infinity, observedAt);
  }

  if (header(headers, 'x-phenix-stale') === 'true') meta.stale = true;

  return meta;
}

// Folds the freshness of another load (from) into meta, as for a load built
// on another's result.
export function mergeFreshness(meta, from) {
  if (!meta || !from) return meta;
  if (from.seq !== undefined) {
    meta.seq = Math.min(meta.seq ?? Infinity, from.seq);
  }
  if (from.observedAt !== undefined) {
    meta.observedAt = Math.min(meta.observedAt ?? Infinity, from.observedAt);
  }
  if (from.stale) meta.stale = true;
  return meta;
}

// When the live websocket connection dropped, or null while it is up. Kept
// here rather than in websocket.ts so the page utilities need not load it.
let wsDownSince = null;

export function setWsDown(down, now = Date.now()) {
  if (!down) wsDownSince = null;
  else wsDownSince ??= now;
}

export function wsDisconnectedSince() {
  return wsDownSince;
}

// Why data may be out of date, or null when it is not:
//
//   'server'        the server served earlier data (minimega was busy)
//   'invalidated'   it changed elsewhere since it loaded
//   'disconnected'  live updates stopped coming
//   'age'           it is older than the resource's staleAfterMs
//
// state is { observedAt, stale, invalidated }.
export function staleReason(
  state,
  resource,
  now = Date.now(),
  disconnectedSince = wsDownSince,
) {
  if (state.stale) return 'server';
  if (state.invalidated) return 'invalidated';
  if (resource?.live && disconnectedSince !== null) return 'disconnected';
  if (
    resource?.staleAfterMs &&
    state.observedAt !== undefined &&
    state.observedAt !== null &&
    now - state.observedAt > resource.staleAfterMs
  ) {
    return 'age';
  }
  return null;
}
