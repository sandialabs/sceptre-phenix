// The VLANs API module (src/api/vlans.js): the request each function sends,
// with names and aliases encoded into the path.
import fc from 'fast-check';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);

import * as vlansApi from '@/api/vlans.js';

const signal = new AbortController().signal;
const config = { signal, meta: {} };
const FORCE = { params: { force: true } };

// [function, call, axios method, the arguments it is called with]
const requests = [
  [
    'listVlans',
    () => vlansApi.listVlans(undefined, config),
    'get',
    ['vlans', { params: undefined, ...config }],
  ],
  [
    'listVlans filtered',
    () => vlansApi.listVlans({ experiment: 'a', issues: true }),
    'get',
    ['vlans', { params: { experiment: 'a', issues: true } }],
  ],
  [
    'getVlan',
    () => vlansApi.getVlan(102, config),
    'get',
    ['vlans/102', config],
  ],
  [
    'findFreeVlans',
    () => vlansApi.findFreeVlans(),
    'get',
    ['vlans/free', { params: { count: 1 } }],
  ],
  [
    'findFreeVlans outside ranges',
    () =>
      vlansApi.findFreeVlans({
        count: 16,
        min: 10,
        max: 900,
        outsideRanges: true,
      }),
    'get',
    [
      'vlans/free',
      { params: { count: 16, min: 10, max: 900, outside_ranges: true } },
    ],
  ],
  [
    'checkVlans',
    () => vlansApi.checkVlans({ experiment: 'x', aliases: { A: 102 } }),
    'post',
    ['vlans/check', { experiment: 'x', aliases: { A: 102 }, min: 0, max: 0 }],
  ],
  [
    'getVlanBlacklist',
    () => vlansApi.getVlanBlacklist(),
    'get',
    ['vlans/blacklist', undefined],
  ],
  [
    'addVlanToBlacklist',
    () => vlansApi.addVlanToBlacklist(100),
    'put',
    ['vlans/blacklist/100'],
  ],
  [
    'removeVlanFromBlacklist',
    () => vlansApi.removeVlanFromBlacklist(100),
    'delete',
    ['vlans/blacklist/100'],
  ],
  [
    'releaseVlanAllocations',
    () => vlansApi.releaseVlanAllocations('old-demo'),
    'delete',
    ['vlans/allocations/old-demo'],
  ],
  [
    'getExperimentVlans',
    () => vlansApi.getExperimentVlans('exp', config),
    'get',
    ['experiments/exp/vlans', config],
  ],
  [
    'pinVlanAlias',
    () => vlansApi.pinVlanAlias('exp', 'EXP', 105),
    'put',
    ['experiments/exp/vlans/aliases/EXP', { id: 105 }, undefined],
  ],
  [
    'pinVlanAlias forced',
    () => vlansApi.pinVlanAlias('exp', 'EXP', 105, { force: true }),
    'put',
    ['experiments/exp/vlans/aliases/EXP', { id: 105 }, FORCE],
  ],
  [
    'unpinVlanAlias',
    () => vlansApi.unpinVlanAlias('exp', 'EXP'),
    'delete',
    ['experiments/exp/vlans/aliases/EXP'],
  ],
  [
    'setVlanRange',
    () => vlansApi.setVlanRange('exp', 200, 299),
    'put',
    ['experiments/exp/vlans/range', { min: 200, max: 299 }, undefined],
  ],
  [
    'setVlanRange forced',
    () => vlansApi.setVlanRange('exp', 200, 299, { force: true }),
    'put',
    ['experiments/exp/vlans/range', { min: 200, max: 299 }, FORCE],
  ],
  [
    'clearVlanRange',
    () => vlansApi.clearVlanRange('exp'),
    'delete',
    ['experiments/exp/vlans/range'],
  ],
  [
    'pinLiveVlans',
    () => vlansApi.pinLiveVlans('exp'),
    'post',
    ['experiments/exp/vlans/pin-live'],
  ],
];

const METHODS = ['get', 'post', 'put', 'delete'];

beforeEach(() => {
  resetMocks();
  for (const m of METHODS) axios[m].mockResolvedValue({ data: {} });
});

describe('the VLANs API module', () => {
  it.each(requests)('%s sends its request', async (_, call, method, args) => {
    await call();
    const calls = METHODS.flatMap((m) =>
      axios[m].mock.calls.map((c) => [m, c]),
    );
    expect(calls).toStrictEqual([[method, args]]);
  });

  it('has a request case for every exported function', () => {
    const tested = new Set(requests.map(([name]) => name.split(' ')[0]));
    expect(Object.keys(vlansApi).filter((name) => !tested.has(name))).toEqual(
      [],
    );
  });

  it('encodes experiment names, aliases and namespaces into the path', async () => {
    const name = fc.string({ unit: 'grapheme', minLength: 1, maxLength: 12 });
    await fc.assert(
      fc.asyncProperty(name, name, async (exp, alias) => {
        axios.put.mockClear();
        axios.delete.mockClear();
        await vlansApi.pinVlanAlias(exp, alias, 5);
        await vlansApi.releaseVlanAllocations(exp);
        const [url] = axios.put.mock.calls[0];
        expect(url.split('/').map(decodeURIComponent)).toEqual([
          'experiments',
          exp,
          'vlans',
          'aliases',
          alias,
        ]);
        const [released] = axios.delete.mock.calls[0];
        expect(released.split('/').map(decodeURIComponent)).toEqual([
          'vlans',
          'allocations',
          exp,
        ]);
      }),
      { numRuns: 50 },
    );
  });
});
