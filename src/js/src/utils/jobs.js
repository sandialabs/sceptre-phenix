import { defineStore } from 'pinia';

import axiosInstance from '@/utils/axios.js';
import { startPoll } from '@/utils/freshness.js';
import { listText, pluralWord } from '@/utils/plural.js';
import { readPref, writePref } from '@/utils/prefs.js';
import { usePhenixStore } from '@/store.js';

// The server's background jobs the user may see (see docs/content/jobs.md):
// loaded once after sign-in and again after the websocket reconnects, and
// kept current by the websocket's job messages. A message carries a job
// without its items' results; fetch() reads the whole job.

// how often whenDone asks the server, in case a websocket message is lost
const DONE_POLL_MS = 5000;

// how many finished jobs the jobs menu lists
const RECENT_JOBS = 10;

// whether a job is still queued or running
export const isActiveJob = (job) =>
  job?.status === 'queued' || job?.status === 'running';

// The finished jobs this browser has shown in the jobs menu, by job ID, kept
// in localStorage so a reload does not color the menu button again.
const VIEWED_KEY = 'phenix.jobs.viewed';

// the job IDs stored at VIEWED_KEY; anything but a list of short strings is
// dropped, as storage the page did not write
export function parseViewed(text) {
  let ids;
  try {
    ids = JSON.parse(text ?? '[]');
  } catch {
    return [];
  }
  if (!Array.isArray(ids)) return [];
  return ids.filter(
    (id) => typeof id === 'string' && id.length > 0 && id.length <= 256,
  );
}

// the finished statuses that color the menu button, worst first; a canceled
// job was stopped on purpose, so it does not
const UNVIEWED_STATUSES = ['failed', 'partial', 'succeeded'];
const UNVIEWED_WORDS = {
  failed: 'failed',
  partial: 'partly succeeded',
  succeeded: 'succeeded',
};

// how many of the jobs ended in each of UNVIEWED_STATUSES
export function unviewedCounts(jobs) {
  const counts = { failed: 0, partial: 0, succeeded: 0 };
  for (const job of jobs) {
    if (UNVIEWED_STATUSES.includes(job.status)) counts[job.status] += 1;
  }
  return counts;
}

// the menu button's color for those counts: 'failed' when any job failed or
// only partly succeeded, 'succeeded' when the rest succeeded, null for none
export function unviewedTone(counts) {
  if (counts.failed || counts.partial) return 'failed';
  return counts.succeeded ? 'succeeded' : null;
}

// the counts in words: "1 failed job and 2 succeeded jobs not viewed", or ''
export function unviewedText(counts) {
  const parts = UNVIEWED_STATUSES.filter((status) => counts[status]).map(
    (status) =>
      `${counts[status]} ${UNVIEWED_WORDS[status]} ${pluralWord(counts[status], 'job')}`,
  );
  return parts.length ? `${listText(parts)} not viewed` : '';
}

// when a job was created, as [ms, ns past that ms]: Go's RFC3339Nano trims
// trailing zeros and gives the server's zone, so the text does not sort
const createdAt = ({ created }) => [
  Date.parse(created) || 0,
  Number((/\.\d{3}(\d{1,6})/.exec(created ?? '')?.[1] ?? '').padEnd(6, '0')),
];

// newest first
const byCreated = (a, b) => {
  const [aMs, aNano] = createdAt(a);
  const [bMs, bNano] = createdAt(b);
  return bMs - aMs || bNano - aNano;
};

// whether a copy of a job has every item and their results, as GET
// jobs/{id} gives it, rather than a websocket message's shortened copy
const whole = (job) =>
  !job.itemsOmitted &&
  (job.items ?? []).every(
    (item) => item.status !== 'succeeded' || 'result' in item,
  );

// the promises waiting for a job to finish, as { resolve, reject }, by job
// ID; kept out of the store's state, which only holds what the UI shows
const waiters = new Map();

// fails the promises waiting for a job the server no longer has, as after it
// restarts, or for any job once the user signs out, so the pages awaiting them
// stop waiting, and polling
function forget(
  id,
  error = new Error(
    `The server no longer has job ${id}. It may have restarted`,
  ),
) {
  for (const { reject } of waiters.get(id) ?? []) reject(error);
  waiters.delete(id);
}

export const useJobStore = defineStore('jobs', {
  state: () => ({
    jobs: {},
    // the IDs of the finished jobs shown in the jobs menu (see VIEWED_KEY)
    viewed: new Set(parseViewed(readPref(VIEWED_KEY))),
  }),

  getters: {
    // every job, newest first
    list: (state) => Object.values(state.jobs).sort(byCreated),
    // the jobs queued or running
    active() {
      return this.list.filter(isActiveJob);
    },
    // the jobs the jobs menu lists: every active one, and the newest finished
    recent() {
      const finished = this.list.filter((job) => !isActiveJob(job));
      return [...this.active, ...finished.slice(0, RECENT_JOBS)];
    },
    // the finished jobs the menu lists but has not shown, canceled ones aside
    unviewed() {
      return this.recent.filter(
        (job) =>
          UNVIEWED_STATUSES.includes(job.status) && !this.viewed.has(job.id),
      );
    },
  },

  actions: {
    // keeps job unless the store already holds a newer copy, and tells those
    // waiting once it has finished
    put(job) {
      if (!job?.id) return null;

      const held = this.jobs[job.id];
      if (held && held.revision > job.revision) return held;

      // a websocket copy of the same revision leaves out what the held copy,
      // read whole, has
      const keepHeld =
        held?.revision === job.revision && !whole(job) && whole(held);
      this.jobs[job.id] = keepHeld ? held : job;

      if (!isActiveJob(job)) {
        for (const { resolve } of waiters.get(job.id) ?? [])
          resolve(this.jobs[job.id]);
        waiters.delete(job.id);
      }
      return this.jobs[job.id];
    },

    // asks the server for every job the user may see
    async load() {
      const { data } = await axiosInstance.get('jobs');
      const listed = new Set();
      for (const job of data?.jobs ?? []) {
        listed.add(job.id);
        this.put(job);
      }
      // forgotten while the socket was down
      for (const id of Object.keys(this.jobs)) {
        if (!listed.has(id)) delete this.jobs[id];
      }
      this.pruneViewed(listed);
      // asks after the awaited ones, which may have started since the list
      for (const id of waiters.keys()) {
        if (!listed.has(id)) this.check(id);
      }
    },

    // a websocket message: a job changed or is gone
    apply(msg) {
      if (msg?.resource?.type !== 'job') return;
      if (msg.resource.action === 'remove') {
        delete this.jobs[msg.resource.name];
        this.dropViewed(msg.resource.name);
        forget(msg.resource.name);
        return;
      }
      this.put(msg.result);
    },

    // starts a job with a POST to url and returns it
    async start(url, body, config) {
      const { data } = await axiosInstance.post(url, body, config);
      return this.put(data);
    },

    // reads the whole job, with every item's result
    async fetch(id) {
      const { data } = await axiosInstance.get(`jobs/${id}`);
      return this.put(data);
    },

    // reads the job again, and forgets it if the server no longer has it
    async check(id) {
      try {
        await this.fetch(id);
      } catch (err) {
        if (err?.response?.status !== 404) return;
        delete this.jobs[id];
        this.dropViewed(id);
        forget(id);
      }
    },

    async cancel(id) {
      const { data } = await axiosInstance.post(`jobs/${id}/cancel`);
      return this.put(data);
    },

    // forgets a finished job, on the server too
    async dismiss(id) {
      await axiosInstance.delete(`jobs/${id}`);
      delete this.jobs[id];
      this.dropViewed(id);
    },

    // marks the jobs as shown in the jobs menu, in this browser
    markViewed(jobs) {
      const before = this.viewed.size;
      for (const job of jobs) this.viewed.add(job.id);
      if (this.viewed.size !== before) this.saveViewed();
    },

    // keeps only the viewed IDs of the jobs the server listed, a Set of IDs
    pruneViewed(ids) {
      const before = this.viewed.size;
      for (const id of [...this.viewed]) {
        if (!ids.has(id)) this.viewed.delete(id);
      }
      if (this.viewed.size !== before) this.saveViewed();
    },

    // forgets that a job the server no longer has was viewed
    dropViewed(id) {
      if (this.viewed.delete(id)) this.saveViewed();
    },

    saveViewed() {
      writePref(VIEWED_KEY, JSON.stringify([...this.viewed]));
    },

    // resolves with the job once it has finished, read whole from the
    // server; rejects if the server no longer has it
    whenDone(id) {
      const held = this.jobs[id];
      const done = new Promise((resolve, reject) => {
        if (held && !isActiveJob(held)) {
          resolve(held);
          return;
        }
        waiters.set(id, [...(waiters.get(id) ?? []), { resolve, reject }]);
      });

      // in case a websocket message is lost; one check at a time, paused
      // while the server is away (see startPoll)
      let checking = false;
      const stopPoll = startPoll(
        () => {
          checking = true;
          this.check(id).finally(() => (checking = false));
        },
        DONE_POLL_MS,
        { when: () => !checking },
      );

      return done
        .finally(stopPoll)
        .then(() => this.fetch(id).catch(() => this.jobs[id] ?? held));
    },

    // where the job's artifact downloads from
    artifactURL(job) {
      const token = usePhenixStore().token ?? '';
      const query = token ? `?token=${encodeURIComponent(token)}` : '';
      return `${import.meta.env.BASE_URL}api/v1/jobs/${job.id}/artifact${query}`;
    },

    // downloads the job's artifact through a link rather than a new window,
    // which a browser blocks once the click that asked for it is long past
    downloadArtifact(job) {
      const link = document.createElement('a');
      link.href = this.artifactURL(job);
      link.download = job.artifact?.name ?? '';
      document.body.appendChild(link);
      link.click();
      link.remove();
    },

    // forgets every job, as on sign-out, failing whatever waits for one
    clear() {
      this.jobs = {};
      // canceled on purpose, so error reports leave it out, as for a
      // request canceled when the user leaves a page
      const error = Object.assign(
        new Error('Signed out while waiting for the job'),
        { code: 'ERR_CANCELED' },
      );
      for (const id of [...waiters.keys()]) forget(id, error);
    },
  },
});
