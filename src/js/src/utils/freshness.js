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

// experiment/* actions that change no state: progress reports and replies to
// one client's queries
const NO_CHANGE_ACTIONS = new Set(['progress', 'list', 'search']);

// whether msg is about one of an experiment's VMs and changes its state (a
// recurring screenshot changes none)
const isVmChange = (msg) =>
  !!type(msg)?.startsWith('experiment/') &&
  !type(msg).endsWith('/screenshot') &&
  !NO_CHANGE_ACTIONS.has(msg.resource.action);

// whether msg tells of a change to the experiment `name` or one of its VMs
const aboutExperiment = (msg, name) => {
  const target = msg.resource?.name ?? '';
  return (
    (isExperiment(msg) && target === name) ||
    (isVmChange(msg) && target.startsWith(`${name}/`)) ||
    (isExperimentConfig(msg) && target === `Experiment/${name}`)
  );
};

// the name of the experiment msg tells of being deleted, if any
const deletedExperiment = (msg) =>
  isExperiment(msg) && msg.resource.action === 'delete'
    ? msg.resource.name
    : undefined;

// whether msg drops any cached page under a removedBy rule below
export const removesPages = (msg) => deletedExperiment(msg) !== undefined;

// Each page's data, by page cache key:
//
//   live           the page is kept current by websocket messages, so its
//                  data may be out of date while the websocket is down
//   reuseMs        how long other pages reuse a cached copy instead of
//                  asking again (see recentPage)
//   pollMs         how often the open page reloads
//   staleAfterMs   past this age the data is shown as may be out of date
//   invalidatedBy  websocket messages after which a cached copy is out of
//                  date and is loaded again on the next visit; each is given
//                  the message and the cache key
//   removedBy      websocket messages after which what a cached copy shows no
//                  longer exists, so it is dropped; given the same arguments
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
    staleAfterMs: 5 * MINUTE,
    invalidatedBy: [isExperiment],
  },
  settings: { live: false, staleAfterMs: 10 * MINUTE },
  vmtiles: { pollMs: MINUTE, staleAfterMs: 3 * MINUTE },
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
      removedBy: [(msg, key) => deletedExperiment(msg) === key.slice(11)],
    },
  },
  {
    prefix: 'experiment/',
    resource: {
      live: true,
      invalidatedBy: [
        (msg, key) => aboutExperiment(msg, key.split('/')[1] ?? ''),
      ],
      removedBy: [(msg, key) => deletedExperiment(msg) === key.split('/')[1]],
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

// The close code the server sends every websocket, and only then, as it shuts
// down, and the code it sends a client too slow to keep up with its messages.
const GOING_AWAY = 1001;
const TRY_AGAIN_LATER = 1013;

// Why a websocket closed, from its CloseEvent: 'shutdown' (the server is
// shutting down), 'behind' (it dropped a client too slow), else 'lost'.
export function wsCloseCause(event) {
  if (event?.code === GOING_AWAY) return 'shutdown';
  if (event?.code === TRY_AGAIN_LATER) return 'behind';
  return 'lost';
}

// How long a shutdown is believed without the server answering: it may not
// come back, and by then a failed reconnect means it is down.
export const SHUTDOWN_HOLD_MS = MINUTE;

// When the live websocket connection dropped, or null while it is up, and why
// (a wsCloseCause). Kept here rather than in websocket.ts so the page
// utilities and error notifications need not load it.
let wsDownSince = null;
let wsDownCause = null;
// when the server said it is shutting down, or null
let shutdownAt = null;

// whether a request got no response at all and none has been answered since
let unanswered = false;
// no background request before this time (ms), from a 503's Retry-After
let retryAfterUntil = 0;

const shutdownKnown = (now = Date.now()) =>
  shutdownAt !== null && now - shutdownAt < SHUTDOWN_HOLD_MS;

// whether background requests are holding back or backing off, so the
// server's next answer brings them back (see onServerBack)
const awayNoted = () =>
  shutdownAt !== null || unanswered || retryAfterUntil > 0;

export function setWsDown(down, cause = 'lost') {
  if (!down) {
    const away = awayNoted();
    wsDownSince = null;
    wsDownCause = null;
    shutdownAt = null;
    unanswered = false;
    retryAfterUntil = 0;
    if (away) announceBack();
    return;
  }
  wsDownSince ??= Date.now();
  // a shutdown holds through the failed reconnects that follow it
  if (shutdownKnown()) {
    wsDownCause = 'shutdown';
    return;
  }
  wsDownCause = cause;
  shutdownAt = cause === 'shutdown' ? Date.now() : null;
}

// The server answered a request sent at sentAt (ms), so it is serving again;
// one sent before it said it was shutting down may only have been finished.
export function serverResponded(sentAt) {
  if (shutdownAt !== null && sentAt < shutdownAt) return;
  if (!awayNoted()) return;
  if (shutdownAt !== null && wsDownCause === 'shutdown') wsDownCause = 'lost';
  shutdownAt = null;
  unanswered = false;
  retryAfterUntil = 0;
  announceBack();
}

// A request got no response at all: polls back off until one is answered
// (see startPoll), as from a server that is down.
export function serverUnanswered() {
  unanswered = true;
}

// Whether a request got no response and none has been answered since.
export function requestsUnanswered() {
  return unanswered;
}

// the longest Retry-After honored, so a bad header cannot stop polling for good
export const MAX_RETRY_AFTER_MS = 5 * MINUTE;

// Retry-After in ms from now: delay-seconds or an HTTP date; 0 if unreadable.
export function retryAfterMs(value, now = Date.now()) {
  if (value === undefined || value === null) return 0;
  const text = String(value).trim();
  if (text === '') return 0;
  const ms = /^\d+$/.test(text)
    ? Number(text) * SECOND
    : Date.parse(text) - now;
  if (!Number.isFinite(ms)) return 0;
  return Math.min(Math.max(ms, 0), MAX_RETRY_AFTER_MS);
}

// The server refused a request with 503 and Retry-After, as it does while
// shutting down: background requests wait for it, and at least that long.
export function serverUnavailable(retryAfter) {
  const now = Date.now();
  shutdownAt = now;
  if (wsDownCause !== null) wsDownCause = 'shutdown';
  retryAfterUntil = Math.max(
    retryAfterUntil,
    now + retryAfterMs(retryAfter, now),
  );
}

// How long until a 503's Retry-After has passed, in ms (0 once it has).
export function retryAfterLeftMs(now = Date.now()) {
  return Math.max(retryAfterUntil - now, 0);
}

// Why the live websocket connection is down (a wsCloseCause), or null.
export function wsDownReason() {
  if (wsDownCause === 'shutdown' && !shutdownKnown()) return 'lost';
  return wsDownCause;
}

// Whether the server said it was shutting down and has not answered since.
export function serverShuttingDown() {
  return shutdownKnown();
}

// Whether the server turns out to be shutting down within ms: a socket the
// server closes may drop before the live socket hears why.
export async function shutdownWithin(ms = 1500) {
  if (serverShuttingDown()) return true;
  await new Promise((resolve) => setTimeout(resolve, ms));
  return serverShuttingDown();
}

// Background requests pause while the server shuts down, back off while
// unanswered, and resume spread out; a merely lost websocket pauses nothing.

// the longest a poll backs off while requests go unanswered
export const MAX_POLL_BACKOFF_MS = 5 * MINUTE;
// what waits for the server's return starts within this long of it
export const BACK_SPREAD_MS = 2 * SECOND;

const backListeners = new Set();
// the spread-out calls not yet made, so forgetServer can cancel them
const spreadTimers = new Set();

// Calls f after a random part of BACK_SPREAD_MS; returns a function that
// cancels it.
export function afterSpread(f) {
  const timer = setTimeout(() => {
    spreadTimers.delete(timer);
    f();
  }, Math.random() * BACK_SPREAD_MS);
  spreadTimers.add(timer);
  return () => {
    clearTimeout(timer);
    spreadTimers.delete(timer);
  };
}

function announceBack() {
  const backAt = Date.now();
  for (const listener of backListeners) {
    afterSpread(() => {
      // removed since
      if (!backListeners.has(listener)) return;
      try {
        listener(backAt);
      } catch (err) {
        console.error('server back listener error', err);
      }
    });
  }
}

// Calls f(backAt), spread out, each time the server answers after a shutdown,
// a Retry-After or unanswered requests. Returns a function that removes it.
export function onServerBack(f) {
  backListeners.add(f);
  return () => backListeners.delete(f);
}

// Forgets what is known of the server without telling anyone, as on signing
// out.
export function forgetServer() {
  wsDownSince = null;
  wsDownCause = null;
  shutdownAt = null;
  unanswered = false;
  retryAfterUntil = 0;
  for (const timer of spreadTimers) clearTimeout(timer);
  spreadTimers.clear();
}

// How long background requests should still hold back, in ms: while the
// server is believed to be shutting down, and until its Retry-After.
export function holdOffMs(now = Date.now()) {
  const shutdown = shutdownKnown(now) ? shutdownAt + SHUTDOWN_HOLD_MS - now : 0;
  return Math.max(shutdown, retryAfterUntil - now, 0);
}

// How long a poll of intervalMs waits between requests after `misses` of its
// turns in a row went unanswered: doubled for each, up to MAX_POLL_BACKOFF_MS.
export function pollDelayMs(intervalMs, misses = 0) {
  const backoff = intervalMs * 2 ** Math.min(misses, 16);
  return Math.min(backoff, Math.max(MAX_POLL_BACKOFF_MS, intervalMs));
}

// Resolves once background requests may go ahead: at once, or when the
// server is back or the hold ends.
export function whenServing() {
  if (holdOffMs() === 0) return Promise.resolve();
  return new Promise((resolve) => {
    let timer = null;
    let off = () => {};
    const done = () => {
      clearTimeout(timer);
      off();
      resolve();
    };
    const check = () => {
      const hold = holdOffMs();
      if (hold > 0) timer = setTimeout(check, hold);
      else done();
    };
    off = onServerBack(done);
    check();
  });
}

// Calls tick every intervalMs while when() allows, pausing and backing off as
// above, and on the server's return if a turn was put off; returns a stopper.
export function startPoll(tick, intervalMs, { when = () => true } = {}) {
  let timer = null;
  let stopped = false;
  // due times count from lastRun; misses counts its turns in a row taken
  // while requests went unanswered, so a burst of failures counts once
  let lastRun = Date.now();
  let misses = 0;
  // a due turn was put off by a hold or by backing off
  let owed = false;

  const schedule = (ms) => {
    clearTimeout(timer);
    timer = setTimeout(() => fire(), ms);
  };

  const fire = (force = false) => {
    clearTimeout(timer);
    timer = null;
    if (stopped) return;
    const now = Date.now();
    const hold = holdOffMs(now);
    if (hold > 0) {
      owed = true;
      return schedule(hold);
    }
    // still unanswered since its last turn: that turn missed too
    const due = lastRun + pollDelayMs(intervalMs, unanswered ? misses + 1 : 0);
    if (!force && now < due) {
      owed = true;
      return schedule(due - now);
    }
    lastRun = now;
    owed = false;
    schedule(intervalMs);
    if (!when()) return;
    misses = unanswered ? misses + 1 : 0;
    try {
      tick();
    } catch (err) {
      console.error('poll error', err);
    }
  };

  // a turn it took itself, answered, owes nothing
  const offBack = onServerBack(() => {
    if (owed) fire(true);
  });
  schedule(intervalMs);

  return () => {
    stopped = true;
    clearTimeout(timer);
    offBack();
  };
}

// Why data may be out of date, or null when it is not:
//
//   'server'        the server served earlier data (minimega was busy)
//   'invalidated'   it changed elsewhere since it loaded
//   'shutdown'      live updates stopped as the server shut down
//   'disconnected'  live updates stopped coming for any other reason
//   'age'           it is older than the resource's staleAfterMs
//
// state is { observedAt, stale, invalidated }.
export function staleReason(
  state,
  resource,
  now = Date.now(),
  disconnectedSince = wsDownSince,
  disconnectedCause = wsDownReason(),
) {
  if (state.stale) return 'server';
  if (state.invalidated) return 'invalidated';
  if (resource?.live && disconnectedSince !== null) {
    return disconnectedCause === 'shutdown' ? 'shutdown' : 'disconnected';
  }
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
