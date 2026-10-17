import { defineStore } from 'pinia';

import axiosInstance from '@/utils/axios.js';
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

// a byte count for people: 1.5 GiB
export function bytesText(bytes) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let value = bytes ?? 0;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${unit ? value.toFixed(1) : value} ${units[unit]}`;
}

// newest first
const byCreated = (a, b) =>
  String(b.created ?? '').localeCompare(String(a.created ?? ''));

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
    `The server no longer has job ${id}; it may have restarted`,
  ),
) {
  for (const { reject } of waiters.get(id) ?? []) reject(error);
  waiters.delete(id);
}

export const useJobStore = defineStore('jobs', {
  state: () => ({
    jobs: {},
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

      // in case a websocket message is lost
      const poll = setInterval(() => this.check(id), DONE_POLL_MS);

      return done
        .finally(() => clearInterval(poll))
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
