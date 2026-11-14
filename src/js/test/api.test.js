// The per-resource API modules in src/api/: each request is in the OpenAPI
// file, request details are exact, and path names are encoded.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import fc from 'fast-check';
import yaml from 'js-yaml';
import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);

import * as authApi from '@/api/auth.js';
import * as configsApi from '@/api/configs.js';
import * as consoleApi from '@/api/console.js';
import * as disksApi from '@/api/disks.js';
import * as experimentFilesApi from '@/api/experimentFiles.js';
import * as experimentsApi from '@/api/experiments.js';
import * as hostsApi from '@/api/hosts.js';
import * as jobsApi from '@/api/jobs.js';
import * as logsApi from '@/api/logs.js';
import * as rolesApi from '@/api/roles.js';
import * as scorchApi from '@/api/scorch.js';
import * as settingsApi from '@/api/settings.js';
import * as topologiesApi from '@/api/topologies.js';
import * as usersApi from '@/api/users.js';
import * as vmsApi from '@/api/vms.js';
import * as vncApi from '@/api/vnc.js';
import * as webSharkApi from '@/api/webshark.js';
import { useJobStore } from '@/utils/jobs.js';

// what a test passes through untouched: a form, a progress callback, a signal
const form = { form: true };
const onProgress = () => {};
const signal = new AbortController().signal;
// a page fetcher's request config: its signal and freshness meta
const fetchConfig = { signal, meta: {} };

const JSON_TYPE = { 'Content-Type': 'application/json' };
const UPLOAD = { 'Content-Type': 'multipart/form-data' };

// [function, call], or [function, call, axios method, call arguments] for
// a request with details that the OpenAPI check cannot see
const requests = [
  // auth
  ['getProxyLogin', () => authApi.getProxyLogin()],
  ['logIn', () => authApi.logIn('alice', 'pw')],
  [
    'signUp',
    () => authApi.signUp({ username: 'bob', password: 'pw' }),
    'post',
    ['signup', { username: 'bob', password: 'pw' }],
  ],
  ['logOut', () => authApi.logOut()],

  // settings
  ['getSettings', () => settingsApi.getSettings(fetchConfig)],
  [
    'saveSettings',
    () => settingsApi.saveSettings({ a: 1 }),
    'post',
    ['settings', { a: 1 }, { timeout: 0 }],
  ],
  ['getPasswordRequirements', () => settingsApi.getPasswordRequirements()],
  ['getTimeoutSettings', () => settingsApi.getTimeoutSettings()],

  // users
  ['listUsers', () => usersApi.listUsers(fetchConfig)],
  ['createUser', () => usersApi.createUser({ username: 'bob' })],
  ['updateUser', () => usersApi.updateUser('bob', { role: 'x' })],
  ['deleteUser', () => usersApi.deleteUser('bob')],
  [
    'createUserToken',
    () => usersApi.createUserToken('bob', { desc: 'd', lifetime: '1h' }),
    'post',
    ['users/bob/tokens', { desc: 'd', lifetime: '1h' }],
  ],

  // topologies
  ['listTopologies', () => topologiesApi.listTopologies()],
  ['listTopologyScenarios', () => topologiesApi.listTopologyScenarios('a b')],

  // logs
  [
    'listLogs to now',
    () => logsApi.listLogs(new Date(0), null, { signal }),
    'get',
    ['logs?start=1970-01-01T00:00:00.000Z', { signal }],
  ],
  [
    'listLogs to an end',
    () => logsApi.listLogs(new Date(0), new Date(1000), { signal }),
    'get',
    [
      'logs?start=1970-01-01T00:00:00.000Z&end=1970-01-01T00:00:01.000Z',
      { signal },
    ],
  ],
  ['listLogFiles', () => logsApi.listLogFiles(signal)],

  // configs
  [
    'downloadConfigs',
    () => configsApi.downloadConfigs(['Topology/a', 'Scenario/b']),
  ],
  ['deleteConfig', () => configsApi.deleteConfig('Topology/a')],
  ['uploadConfig', () => configsApi.uploadConfig(form)],
  [
    'createConfig',
    () => configsApi.createConfig({ kind: 'Topology' }),
    'post',
    ['configs', '{"kind":"Topology"}', { headers: JSON_TYPE }],
  ],
  [
    'updateConfig without a revision',
    () => configsApi.updateConfig('Topology/a', { kind: 'Topology' }),
    'put',
    ['configs/Topology/a', '{"kind":"Topology"}', { headers: JSON_TYPE }],
  ],
  [
    'updateConfig at a revision',
    () => configsApi.updateConfig('Topology/a', { kind: 'Topology' }, 7),
    'put',
    [
      'configs/Topology/a',
      '{"kind":"Topology"}',
      { headers: { ...JSON_TYPE, 'If-Match': '"r7"' } },
    ],
  ],
  ['getSchemaSpec', () => configsApi.getSchemaSpec('v2')],
  ['getKindSchema', () => configsApi.getKindSchema('Topology', 'v1')],
  ['listConfigs', () => configsApi.listConfigs(fetchConfig)],
  ['getConfig', () => configsApi.getConfig('Topology/a')],

  // console
  ['startConsole', () => consoleApi.startConsole()],
  ['startConsole in a namespace', () => consoleApi.startConsole('exp')],
  ['getConsole', () => consoleApi.getConsole(7)],
  ['endConsole', () => consoleApi.endConsole(7)],
  [
    'resizeConsole',
    () => consoleApi.resizeConsole(7, 80, 24),
    'post',
    ['console/7/size?cols=80&rows=24'],
  ],

  // hosts, roles and jobs
  ['listHosts', () => hostsApi.listHosts(fetchConfig)],
  ['listRoles', () => rolesApi.listRoles(fetchConfig)],
  ['listJobs', () => jobsApi.listJobs()],
  ['getJob', () => jobsApi.getJob('j1')],
  ['cancelJob', () => jobsApi.cancelJob('j1')],
  ['deleteJob', () => jobsApi.deleteJob('j1')],

  // WebShark
  ['openWebSharkCapture', () => webSharkApi.openWebSharkCapture('e@x', form)],
  ['createCaptureToken', () => webSharkApi.createCaptureToken('e@x', form)],

  // disks
  [
    'listDisks',
    () => disksApi.listDisks({ expName: 'exp' }),
    'get',
    ['disks', { params: { expName: 'exp' } }],
  ],
  [
    'listDisks with a config',
    () => disksApi.listDisks({ refresh: true }, fetchConfig),
    'get',
    ['disks', { params: { refresh: true }, ...fetchConfig }],
  ],
  [
    'getDiskDetails',
    () => disksApi.getDiskDetails('kali.qc2', { signal }),
    'get',
    ['disks/details', { params: { disk: 'kali.qc2' }, signal }],
  ],
  [
    'uploadDisk',
    () => disksApi.uploadDisk(form, onProgress),
    'post',
    ['disks', form, { headers: UPLOAD, onUploadProgress: onProgress }],
  ],
  [
    'deleteDisk',
    () => disksApi.deleteDisk('a.qc2'),
    'request',
    [{ method: 'delete', url: 'disks', params: { disk: 'a.qc2' } }],
  ],
  [
    'deleteDisk forced',
    () => disksApi.deleteDisk('a.qc2', { force: true }),
    'request',
    [
      {
        method: 'delete',
        url: 'disks',
        params: { disk: 'a.qc2', force: true },
      },
    ],
  ],
  ['commitDisk', () => disksApi.commitDisk('a.qc2')],
  ['snapshotDisk', () => disksApi.snapshotDisk('a.qc2', 'b.qc2')],
  ['rebaseDisk', () => disksApi.rebaseDisk('a.qc2', 'b.qc2', true)],
  ['resizeDisk', () => disksApi.resizeDisk('a.qc2', '+5G')],
  ['renameDisk', () => disksApi.renameDisk('a.qc2', 'b.qc2')],
  ['cloneDisk', () => disksApi.cloneDisk('a.qc2', 'b.qc2')],

  // experiments
  ['getExperimentOptions', () => experimentsApi.getExperimentOptions()],
  [
    'createExperiment',
    () => experimentsApi.createExperiment({ name: 'exp' }),
    'post',
    ['experiments', { name: 'exp' }, { timeout: 0 }],
  ],
  ['listExperiments', () => experimentsApi.listExperiments(fetchConfig)],
  ['listSoh', () => experimentsApi.listSoh(fetchConfig)],
  [
    'getExperiment',
    () => experimentsApi.getExperiment('exp', { params: { pageNum: 2 } }),
  ],
  [
    'updateExperiment',
    () => experimentsApi.updateExperiment('exp', '{"EXP":100}'),
  ],
  ['deleteExperiment', () => experimentsApi.deleteExperiment('exp')],
  [
    'startExperiment with the default options',
    () => experimentsApi.startExperiment('exp', {}),
    'post',
    ['experiments/exp/start'],
  ],
  [
    'startExperiment with options',
    () => experimentsApi.startExperiment('exp', { dryRun: true, vlanMin: 100 }),
    'post',
    ['experiments/exp/start', { dryRun: true, vlanMin: 100 }],
  ],
  [
    'stopExperiment',
    () => experimentsApi.stopExperiment('exp'),
    'post',
    ['experiments/exp/stop'],
  ],
  [
    'stopExperiment forced',
    () => experimentsApi.stopExperiment('exp', { force: true }),
    'post',
    ['experiments/exp/stop', null, { params: { force: true } }],
  ],
  ['getExperimentSchedule', () => experimentsApi.getExperimentSchedule('exp')],
  ['listSchedulers', () => experimentsApi.listSchedulers()],
  [
    'scheduleExperiment',
    () => experimentsApi.scheduleExperiment('exp', 'isolate-experiment'),
  ],
  [
    'triggerApps at the running stage',
    () => experimentsApi.triggerApps('exp', 'soh', 'running'),
    'post',
    ['experiments/exp/trigger', null, { params: { apps: 'soh' } }],
  ],
  [
    'triggerApps at another stage',
    () => experimentsApi.triggerApps('exp', 'soh', 'configure'),
    'post',
    [
      'experiments/exp/trigger',
      null,
      { params: { apps: 'soh', stage: 'configure' } },
    ],
  ],
  [
    'startVlanCaptures',
    () => experimentsApi.startVlanCaptures('exp', 'DMZ'),
    'post',
    ['experiments/exp/captureVlan', { vlan: 'DMZ' }],
  ],
  [
    'startVlanCaptures in a format',
    () => experimentsApi.startVlanCaptures('exp', 'DMZ', 'pcap'),
    'post',
    ['experiments/exp/captureVlan', { vlan: 'DMZ', format: 'pcap' }],
  ],
  ['stopVlanCaptures', () => experimentsApi.stopVlanCaptures('exp', 'DMZ')],
  [
    'stopVlanCaptures with the name encoded',
    () => experimentsApi.stopVlanCaptures('a/b', 'DMZ'),
    'post',
    ['experiments/a%2Fb/stopCaptureVlan', { vlan: 'DMZ' }],
  ],
  [
    'startExperimentCapture',
    () =>
      experimentsApi.startExperimentCapture('a b', {
        vlans: ['DMZ'],
        filter: 'tcp',
        snaplen: 1600,
        name: '',
        format: 'pcapng',
      }),
  ],
  [
    'getExperimentCapture',
    () => experimentsApi.getExperimentCapture('a b', '3/4'),
  ],
  [
    'stopExperimentCapture',
    () => experimentsApi.stopExperimentCapture('a b', '3'),
  ],
  ['getExperimentApps', () => experimentsApi.getExperimentApps('exp')],
  [
    'getSoh',
    () => experimentsApi.getSoh('exp', '', signal),
    'get',
    ['experiments/exp/soh', { signal }],
  ],
  [
    'getSoh filtered',
    () => experimentsApi.getSoh('exp', 'Error', signal),
    'get',
    ['experiments/exp/soh?statusFilter=Error', { signal }],
  ],
  ['getNetflowCapture', () => experimentsApi.getNetflowCapture('exp')],
  ['startNetflowCapture', () => experimentsApi.startNetflowCapture('exp')],
  ['stopNetflowCapture', () => experimentsApi.stopNetflowCapture('exp')],
  ['listNetflowCaptures', () => experimentsApi.listNetflowCaptures('exp')],
  [
    'headNetflowCapture',
    () =>
      experimentsApi.headNetflowCapture('exp', '20261028T153012.345Z', 'csv'),
  ],
  [
    'listExperimentCaptures',
    () => experimentsApi.listExperimentCaptures('e@x'),
  ],

  // experiment files
  [
    'listExperimentFiles',
    () =>
      experimentFilesApi.listExperimentFiles('exp', {
        params: { sortCol: 'date' },
      }),
  ],
  [
    'getExperimentFile',
    () =>
      experimentFilesApi.getExperimentFile('exp', {
        name: 'a b.json',
        path: 'x/a b.json',
      }),
    'get',
    [
      'experiments/exp/files/a%20b.json',
      {
        params: { path: 'x/a b.json' },
        headers: { Accept: 'text/plain' },
        responseType: 'text',
      },
    ],
  ],
  [
    'downloadExperimentFile',
    () =>
      experimentFilesApi.downloadExperimentFile('e@x', {
        name: 'a b.pcap',
        path: 'x/a b.pcap',
      }),
    'get',
    [
      'experiments/e%40x/files/a%20b.pcap',
      { params: { path: 'x/a b.pcap' }, responseType: 'blob' },
    ],
  ],
  [
    'deleteExperimentFile',
    () =>
      experimentFilesApi.deleteExperimentFile('e@x', {
        name: 'a b',
        path: 'x/a b',
      }),
  ],
  [
    'deleteExperimentFiles',
    () => experimentFilesApi.deleteExperimentFiles('e@x', ['a', 'b']),
  ],
  [
    'archiveExperimentFiles',
    () => experimentFilesApi.archiveExperimentFiles('e@x', ['a']),
  ],

  // VMs
  ['listVms', () => vmsApi.listVms('exp', fetchConfig)],
  ['listAllVms', () => vmsApi.listAllVms(fetchConfig)],
  [
    'updateVms',
    () => vmsApi.updateVms('exp', [{ name: 'a', dnb: true }]),
    'patch',
    ['experiments/exp/vms', { vms: [{ name: 'a', dnb: true }], total: 1 }],
  ],
  [
    'startVmActionsJob',
    () => vmsApi.startVmActionsJob('exp', 'restart', ['a', 'b']),
  ],
  ['getVm', () => vmsApi.getVm('exp', 'a')],
  ['updateVm', () => vmsApi.updateVm('exp', 'a', { cpus: 2 })],
  ['redeployVm', () => vmsApi.redeployVm('exp', 'a', { cpus: 2, ram: 512 })],
  ['listVmSnapshots', () => vmsApi.listVmSnapshots('exp', 'a')],
  ['createVmSnapshot', () => vmsApi.createVmSnapshot('exp', 'a', 'snap')],
  [
    'restoreVmSnapshot',
    () => vmsApi.restoreVmSnapshot('exp', 'a', 'snap'),
    'post',
    ['experiments/exp/vms/a/snapshots/snap', {}, { timeout: 0 }],
  ],
  [
    'deleteVmSnapshot',
    () => vmsApi.deleteVmSnapshot('exp', 'a', 'snap'),
    'delete',
    ['experiments/exp/vms/a/snapshots/snap', { timeout: 0 }],
  ],
  [
    'commitVmDisk',
    () => vmsApi.commitVmDisk('exp', 'a', 'img'),
    'post',
    ['experiments/exp/vms/a/commit', { filename: 'img' }, { timeout: 0 }],
  ],
  [
    'createVmMemorySnapshot',
    () => vmsApi.createVmMemorySnapshot('exp', 'a', 'a.elf'),
    'post',
    [
      'experiments/exp/vms/a/memorySnapshot',
      { filename: 'a.elf' },
      { timeout: 0 },
    ],
  ],
  ['saveVmScreenshot', () => vmsApi.saveVmScreenshot('exp', 'a')],
  ['getVmScreenshot', () => vmsApi.getVmScreenshot('e x', 'a/b', 300, signal)],
  [
    'getVncSession',
    () => vncApi.getVncSession('e x', 'a/b'),
    'get',
    ['experiments/e%20x/vms/a%2Fb/vnc', {}],
  ],
  [
    'getVncSession probe',
    () => vncApi.getVncSession('exp', 'web', { probe: true }),
    'get',
    ['experiments/exp/vms/web/vnc', { params: { probe: true } }],
  ],
  ['listVmForwards', () => vmsApi.listVmForwards('exp', 'a')],
  [
    'createVmForward',
    () => vmsApi.createVmForward('exp', 'a', { src: 1, host: 'h', dst: 2 }),
  ],
  [
    'deleteVmForward',
    () => vmsApi.deleteVmForward('exp', 'a', { host: 'h', dst: 2 }),
  ],
  ['insertVmDisc', () => vmsApi.insertVmDisc('exp', 'a', '/isos/a.iso')],
  ['ejectVmDisc', () => vmsApi.ejectVmDisc('exp', 'a')],
  ['listVmCaptures', () => vmsApi.listVmCaptures('exp', 'a')],
  [
    'createVmCapture',
    () => vmsApi.createVmCapture('exp', 'a', 0, 'a_0.pcap'),
    'post',
    ['experiments/exp/vms/a/captures', { interface: 0, filename: 'a_0.pcap' }],
  ],
  [
    'createVmCapture in a format',
    () => vmsApi.createVmCapture('exp', 'a', 0, 'a_0.pcapng', 'pcapng'),
    'post',
    [
      'experiments/exp/vms/a/captures',
      { interface: 0, filename: 'a_0.pcapng', format: 'pcapng' },
    ],
  ],
  [
    'deleteVmCapture on an interface',
    () => vmsApi.deleteVmCapture('exp', 'a', 0),
    'delete',
    ['experiments/exp/vms/a/captures', { params: { iface: 0 } }],
  ],
  [
    'deleteVmCapture on every interface',
    () => vmsApi.deleteVmCapture('exp', 'a'),
    'delete',
    ['experiments/exp/vms/a/captures', { params: {} }],
  ],
  ['deleteVmCaptures', () => vmsApi.deleteVmCaptures('exp', 'a')],
  ['mountVm', () => vmsApi.mountVm('exp', 'a')],
  ['unmountVm', () => vmsApi.unmountVm('exp', 'a')],
  ['listVmFiles', () => vmsApi.listVmFiles('exp', 'a', '/etc')],
  ['copyFileToVm', () => vmsApi.copyFileToVm('exp', 'a', '/tmp', 'x/b.txt')],
  [
    'uploadVmFile',
    () => vmsApi.uploadVmFile('exp', 'a', '/tmp', form, onProgress),
    'put',
    [
      'experiments/exp/vms/a/files/upload',
      form,
      {
        params: { path: '/tmp' },
        headers: UPLOAD,
        onUploadProgress: onProgress,
      },
    ],
  ],
  ['resetVmSettings', () => vmsApi.resetVmSettings('exp', ['a'])],
  ['getVmQos', () => vmsApi.getVmQos('exp', 'a')],
  ['setVmQos', () => vmsApi.setVmQos('exp', 'a', 1, { loss: '5' })],
  ['clearVmQos', () => vmsApi.clearVmQos('exp', 'a', 1)],

  // SCORCH
  [
    'listScorchPipelines',
    () => scorchApi.listScorchPipelines('exp', { signal }),
  ],
  ['startScorchRun', () => scorchApi.startScorchRun('exp', 0)],
  ['stopScorchRun', () => scorchApi.stopScorchRun('exp', 1)],
  ['clearScorchRun', () => scorchApi.clearScorchRun('exp', 1)],
  ['cleanupScorchRun', () => scorchApi.cleanupScorchRun('exp', 1)],
  ['getScorchLoop', () => scorchApi.getScorchLoop('exp', 1, 2)],
  ['listScorchTerminals', () => scorchApi.listScorchTerminals('exp')],
  [
    'getScorchComponent',
    () =>
      scorchApi.getScorchComponent({
        exp: 'exp',
        run: 1,
        loop: 2,
        stage: 'start',
        name: 'tap',
      }),
  ],
  [
    'exitScorchTerminal',
    () =>
      scorchApi.exitScorchTerminal(
        '/base/api/v1/experiments/exp/scorch/terminals/7/exit/1',
      ),
    'post',
    [
      '/base/api/v1/experiments/exp/scorch/terminals/7/exit/1',
      null,
      { baseURL: '' },
    ],
  ],
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

// the one request sent since the last reset, as { method, url, config }
function sentRequest() {
  const [method, args] = onlyCall();
  if (method === 'request') {
    const { method: verb, url, ...config } = args[0];
    return { method: verb, url, config };
  }
  const withBody = ['post', 'put', 'patch'].includes(method);
  return { method, url: args[0], config: withBody ? args[2] : args[1] };
}

const { paths, components } = yaml.load(
  readFileSync(
    resolve(import.meta.dirname, '../../go/web/public/docs/openapi.yml'),
    'utf8',
  ),
);
const parameter = (p) =>
  p.$ref ? components.parameters[p.$ref.split('/').at(-1)] : p;

// each operation of the OpenAPI file, with its path template as a regex
const operations = Object.entries(paths).flatMap(([template, item]) =>
  ['get', 'head', 'post', 'put', 'patch', 'delete']
    .filter((method) => item[method])
    .map((method) => ({
      method,
      template,
      pattern: new RegExp(
        `^${template.replaceAll('.', '\\.').replace(/\{[^}]+\}/g, '[^/]+')}$`,
      ),
      parameters: [
        ...(item.parameters ?? []),
        ...(item[method].parameters ?? []),
      ].map(parameter),
    })),
);

// the operation for a method and a URL relative to the API base (or with
// the base in it); a literal segment wins over a template parameter
function documentedOperation(method, url) {
  const path = `/${url
    .split('?')[0]
    .replace(/^(.*\/)?api\/v1\//, '')
    .replace(/^\/+/, '')}`;
  const params = (op) => op.template.split('{').length;
  return operations
    .filter((op) => op.method === method && op.pattern.test(path))
    .sort((a, b) => params(a) - params(b))[0];
}

beforeEach(() => {
  resetMocks();
  setActivePinia(createPinia());
  const answer = { data: { id: 'job1', status: 'running', revision: 1 } };
  for (const m of METHODS) axios[m].mockResolvedValue(answer);
});

describe('the API modules', () => {
  it.each(requests)(
    '%s sends a request that the OpenAPI file documents',
    async (_, call) => {
      await call();
      const { method, url, config } = sentRequest();
      const request = `${method.toUpperCase()} ${url}`;
      const op = documentedOperation(method, url);
      expect(op, request).toBeDefined();
      const declared = (where) =>
        op.parameters.filter((p) => p.in === where).map((p) => p.name);
      const query = [
        ...new URLSearchParams(url.split('?')[1] ?? '').keys(),
        ...Object.keys(config?.params ?? {}),
      ];
      expect(
        query.filter((name) => !declared('query').includes(name)),
        request,
      ).toEqual([]);
      const headers = declared('header').map((name) => name.toLowerCase());
      expect(
        Object.keys(config?.headers ?? {})
          .map((name) => name.toLowerCase())
          .filter((name) => !['accept', 'content-type'].includes(name))
          .filter((name) => !headers.includes(name)),
        request,
      ).toEqual([]);
    },
  );

  it.each(requests.filter((row) => row.length > 2))(
    '%s builds its request from its arguments',
    async (_, call, method, args) => {
      await call();
      expect(onlyCall()).toStrictEqual([method, args]);
    },
  );

  it('returns what axios answers, so callers see the response', async () => {
    const response = { status: 204 };
    axios.post.mockResolvedValue(response);
    await expect(authApi.logOut()).resolves.toBe(response);
    const failure = new Error('refused');
    axios.post.mockRejectedValue(failure);
    await expect(authApi.logOut()).rejects.toBe(failure);
  });

  it('returns the started job from the job-starting functions', async () => {
    const job = await vmsApi.startVmActionsJob('exp', 'pause', ['a']);
    expect(job).toBe(useJobStore().jobs.job1);
  });

  it('sums up a bulk file delete, with lists when the server sends none', async () => {
    axios.post.mockResolvedValue({ data: { deleted: ['a'] } });
    await expect(
      experimentFilesApi.deleteExperimentFiles('exp', ['a', 'b']),
    ).resolves.toEqual({ deleted: ['a'], failed: [] });
    axios.post.mockResolvedValue({ data: null });
    await expect(
      experimentFilesApi.deleteExperimentFiles('exp', ['a']),
    ).resolves.toEqual({ deleted: [], failed: [] });
  });

  it('every exported function has a request case', () => {
    const tested = new Set(requests.map(([name]) => name.split(' ')[0]));
    const apiModules = import.meta.glob('../src/api/*.js', { eager: true });
    const exported = Object.entries(apiModules)
      // vlansApi.test.js has its own request-case guard for vlans.js
      .filter(([file]) => !file.endsWith('/vlans.js'))
      .flatMap(([, mod]) => Object.keys(mod));
    // builds a link's URL rather than sending a request (tested below)
    const links = new Set(['netflowCaptureURL']);
    expect(
      exported.filter((name) => !tested.has(name) && !links.has(name)),
    ).toEqual([]);
  });
});

// any name, in well-formed Unicode, which encodeURIComponent accepts
const anyName = fc.string({ unit: 'grapheme', maxLength: 12 });

// the request's URL path split at '/', with each part decoded
const decodedParts = (url) => url.split('/').map(decodeURIComponent);

// [function, call with a name, the decoded path parts it sends]
const encodedPaths = [
  [
    'listTopologyScenarios',
    (name) => topologiesApi.listTopologyScenarios(name),
    (name) => ['topologies', name, 'scenarios'],
  ],
  [
    'listExperimentCaptures',
    (name) => experimentsApi.listExperimentCaptures(name),
    (name) => ['experiments', name, 'captures'],
  ],
  [
    'startExperimentCapture',
    (name) => experimentsApi.startExperimentCapture(name, {}),
    (name) => ['experiments', name, 'captures'],
  ],
  [
    'getExperimentCapture',
    (name) => experimentsApi.getExperimentCapture(name, name),
    (name) => ['experiments', name, 'captures', name],
  ],
  [
    'stopExperimentCapture',
    (name) => experimentsApi.stopExperimentCapture(name, name),
    (name) => ['experiments', name, 'captures', name],
  ],
  [
    'startVlanCaptures',
    (name) => experimentsApi.startVlanCaptures(name, 'DMZ'),
    (name) => ['experiments', name, 'captureVlan'],
  ],
  [
    'stopVlanCaptures',
    (name) => experimentsApi.stopVlanCaptures(name, 'DMZ'),
    (name) => ['experiments', name, 'stopCaptureVlan'],
  ],
  [
    'openWebSharkCapture',
    (name) => webSharkApi.openWebSharkCapture(name, {}),
    (name) => ['experiments', name, 'webshark'],
  ],
  [
    'createCaptureToken',
    (name) => webSharkApi.createCaptureToken(name, {}),
    (name) => ['experiments', name, 'captureTokens'],
  ],
  [
    'getVmScreenshot',
    (name) => vmsApi.getVmScreenshot(name, name, 1, signal),
    (name) => ['experiments', name, 'vms', name, 'screenshot.png'],
  ],
  [
    'getVncSession',
    (name) => vncApi.getVncSession(name, name),
    (name) => ['experiments', name, 'vms', name, 'vnc'],
  ],
  [
    'getExperimentFile',
    (name) => experimentFilesApi.getExperimentFile('exp', { name, path: name }),
    (name) => ['experiments', 'exp', 'files', name],
  ],
  [
    'downloadExperimentFile',
    (name) =>
      experimentFilesApi.downloadExperimentFile(name, { name, path: name }),
    (name) => ['experiments', name, 'files', name],
  ],
  [
    'deleteExperimentFile',
    (name) =>
      experimentFilesApi.deleteExperimentFile(name, { name, path: name }),
    (name) => ['experiments', name, 'files', name],
  ],
  [
    'deleteExperimentFiles',
    (name) => experimentFilesApi.deleteExperimentFiles(name, []),
    (name) => ['experiments', name, 'files', 'delete'],
  ],
  [
    'archiveExperimentFiles',
    (name) => experimentFilesApi.archiveExperimentFiles(name, []),
    (name) => ['experiments', name, 'files', 'archive'],
  ],
  [
    'listNetflowCaptures',
    (name) => experimentsApi.listNetflowCaptures(name),
    (name) => ['experiments', name, 'netflow', 'captures'],
  ],
  [
    'headNetflowCapture',
    (name) => experimentsApi.headNetflowCapture(name, name, 'csv'),
    (name) => ['experiments', name, 'netflow', 'captures', name],
  ],
  [
    'updateUser',
    (name) => usersApi.updateUser(name, {}),
    (name) => ['users', name],
  ],
  [
    'deleteUser',
    (name) => usersApi.deleteUser(name),
    (name) => ['users', name],
  ],
  [
    'createUserToken',
    (name) => usersApi.createUserToken(name, {}),
    (name) => ['users', name, 'tokens'],
  ],
];

describe('names encoded into a path', () => {
  it('keeps each name one segment that decodes back to it', async () => {
    await fc.assert(
      fc.asyncProperty(anyName, async (name) => {
        for (const [fn, call, parts] of encodedPaths) {
          resetMocks();
          setActivePinia(createPinia());
          axios.get.mockResolvedValue({ data: {} });
          axios.post.mockResolvedValue({ data: { id: 'j', revision: 1 } });
          axios.delete.mockResolvedValue({});
          await call(name);
          const [, [url]] = onlyCall();
          expect(url, fn).not.toMatch(/[?#]/);
          expect(decodedParts(url), fn).toEqual(parts(name));
        }
      }),
      // the example makes each missing encoding fail on every run
      { numRuns: 30, examples: [['a/b?c#d%e f']] },
    );
  });
});

describe('a netflow capture download link', () => {
  it('names the encoded experiment, ID and format, and the token when there is one', () => {
    expect(
      experimentsApi.netflowCaptureURL(
        'e x',
        '20261028T153012.345Z',
        'csv',
        't/1',
      ),
    ).toBe(
      '/api/v1/experiments/e%20x/netflow/captures/20261028T153012.345Z?format=csv&token=t%2F1',
    );
    expect(experimentsApi.netflowCaptureURL('exp', 'id', 'text', null)).toBe(
      '/api/v1/experiments/exp/netflow/captures/id?format=text',
    );
  });

  it('keeps any name and ID one segment each, decoding back to them', () => {
    fc.assert(
      fc.property(anyName, anyName, (name, id) => {
        const url = experimentsApi.netflowCaptureURL(name, id, 'jsonl', 'tok');
        const [path, query, ...rest] = url.split('?');
        expect(rest).toEqual([]);
        expect(decodedParts(path)).toEqual([
          '',
          'api',
          'v1',
          'experiments',
          name,
          'netflow',
          'captures',
          id,
        ]);
        expect(Object.fromEntries(new URLSearchParams(query))).toEqual({
          format: 'jsonl',
          token: 'tok',
        });
      }),
    );
  });
});
