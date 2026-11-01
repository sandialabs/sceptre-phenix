// The per-resource API modules in src/api/: the exact request each function
// sends.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);

import * as consoleApi from '@/api/console.js';
import * as jobsApi from '@/api/jobs.js';

// [function, call, axios method, the arguments it is called with]
const requests = [
  // console
  ['startConsole', () => consoleApi.startConsole(), 'post', ['console']],
  ['getConsole', () => consoleApi.getConsole(7), 'get', ['console/7']],
  ['endConsole', () => consoleApi.endConsole(7), 'delete', ['console/7']],
  [
    'resizeConsole',
    () => consoleApi.resizeConsole(7, 80, 24),
    'post',
    ['console/7/size?cols=80&rows=24'],
  ],
  // jobs
  ['listJobs', () => jobsApi.listJobs(), 'get', ['jobs']],
  ['getJob', () => jobsApi.getJob('j1'), 'get', ['jobs/j1']],
  ['cancelJob', () => jobsApi.cancelJob('j1'), 'post', ['jobs/j1/cancel']],
  ['deleteJob', () => jobsApi.deleteJob('j1'), 'delete', ['jobs/j1']],
];

// the axios methods the modules call
const METHODS = ['get', 'head', 'post', 'put', 'patch', 'delete', 'request'];

// the one axios call made since the last reset, as [method, args]
function onlyCall() {
  const calls = METHODS.flatMap((m) =>
    axios[m].mock.calls.map((args) => [m, args]),
  );
  expect(calls).toHaveLength(1);
  return calls[0];
}

beforeEach(() => {
  resetMocks();
  const answer = { data: { id: 'job1', status: 'running', revision: 1 } };
  for (const m of METHODS) axios[m].mockResolvedValue(answer);
});

describe('the API modules', () => {
  it.each(requests)('%s sends its request', async (_, call, method, args) => {
    await call();
    expect(onlyCall()).toStrictEqual([method, args]);
  });

  it('every exported function has a request case', () => {
    const tested = new Set(requests.map(([name]) => name.split(' ')[0]));
    const exported = [consoleApi, jobsApi].flatMap((mod) => Object.keys(mod));
    expect(exported.filter((name) => !tested.has(name))).toEqual([]);
  });
});
