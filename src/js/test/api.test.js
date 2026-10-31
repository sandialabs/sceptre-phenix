// The per-resource API modules in src/api/: the exact request each function
// sends, and that names put into a path are encoded where the function says.
import fc from 'fast-check';
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
import * as webSharkApi from '@/api/webshark.js';
import { useJobStore } from '@/utils/jobs.js';

// what a test passes through untouched: a form, a progress callback, a signal
const form = { form: true };
const onProgress = () => {};
const signal = new AbortController().signal;
// a page fetcher's request config: its signal and freshness meta
const fetchConfig = { signal, meta: {} };

const JSON_TYPE = { 'Content-Type': 'application/json' };
const ACCEPT_JSON = { headers: { Accept: 'application/json' } };
const UPLOAD = { 'Content-Type': 'multipart/form-data' };

// [function, call, axios method, the arguments it is called with]
const requests = [
  // auth
  ['getProxyLogin', () => authApi.getProxyLogin(), 'get', ['login']],
  [
    'logIn',
    () => authApi.logIn('alice', 'pw'),
    'post',
    ['login', { user: 'alice', pass: 'pw' }],
  ],
  [
    'signUp',
    () => authApi.signUp({ username: 'bob' }),
    'post',
    ['signup', { username: 'bob' }],
  ],
  ['logOut', () => authApi.logOut(), 'post', ['logout']],

  // settings
  [
    'getSettings',
    () => settingsApi.getSettings(fetchConfig),
    'get',
    ['settings', fetchConfig],
  ],
  [
    'saveSettings',
    () => settingsApi.saveSettings({ a: 1 }),
    'post',
    ['settings', { a: 1 }, { timeout: 0 }],
  ],
  [
    'getPasswordRequirements',
    () => settingsApi.getPasswordRequirements(),
    'get',
    ['settings/password'],
  ],
  [
    'getTimeoutSettings',
    () => settingsApi.getTimeoutSettings(),
    'get',
    ['settings/timeout'],
  ],

  // users
  [
    'listUsers',
    () => usersApi.listUsers(fetchConfig),
    'get',
    ['users', fetchConfig],
  ],
  [
    'createUser',
    () => usersApi.createUser({ username: 'bob' }),
    'post',
    ['users', { username: 'bob' }],
  ],
  [
    'updateUser',
    () => usersApi.updateUser('bob', { role: 'x' }),
    'patch',
    ['users/bob', { role: 'x' }],
  ],
  ['deleteUser', () => usersApi.deleteUser('bob'), 'delete', ['users/bob']],
  [
    'createUserToken',
    () => usersApi.createUserToken('bob', { desc: 'd', lifetime: '1h' }),
    'post',
    ['users/bob/tokens', { desc: 'd', lifetime: '1h' }],
  ],

  // topologies
  [
    'listTopologies',
    () => topologiesApi.listTopologies(),
    'get',
    ['topologies'],
  ],
  [
    'listTopologyScenarios',
    () => topologiesApi.listTopologyScenarios('a b'),
    'get',
    ['topologies/a%20b/scenarios'],
  ],

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
  [
    'listLogFiles',
    () => logsApi.listLogFiles(signal),
    'get',
    ['logs/files', { signal }],
  ],

  // configs
  [
    'downloadConfigs',
    () => configsApi.downloadConfigs(['Topology/a', 'Scenario/b']),
    'post',
    [
      'configs/download',
      '["Topology/a","Scenario/b"]',
      {
        headers: { ...JSON_TYPE, Accept: 'application/x-yaml' },
        responseType: 'blob',
      },
    ],
  ],
  [
    'deleteConfig',
    () => configsApi.deleteConfig('Topology/a'),
    'delete',
    ['configs/Topology/a'],
  ],
  [
    'uploadConfig',
    () => configsApi.uploadConfig(form),
    'post',
    ['configs', form],
  ],
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
  [
    'getSchemaSpec',
    () => configsApi.getSchemaSpec('v2'),
    'get',
    ['schemas/v2', ACCEPT_JSON],
  ],
  [
    'getKindSchema',
    () => configsApi.getKindSchema('Topology', 'v1'),
    'get',
    ['schemas/Topology/v1', ACCEPT_JSON],
  ],
  [
    'listConfigs',
    () => configsApi.listConfigs(fetchConfig),
    'get',
    ['configs', fetchConfig],
  ],
  [
    'getConfig',
    () => configsApi.getConfig('Topology/a'),
    'get',
    ['configs/Topology/a', ACCEPT_JSON],
  ],

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

  // hosts, roles and jobs
  [
    'listHosts',
    () => hostsApi.listHosts(fetchConfig),
    'get',
    ['hosts', fetchConfig],
  ],
  [
    'listRoles',
    () => rolesApi.listRoles(fetchConfig),
    'get',
    ['roles', fetchConfig],
  ],
  ['listJobs', () => jobsApi.listJobs(), 'get', ['jobs']],
  ['getJob', () => jobsApi.getJob('j1'), 'get', ['jobs/j1']],
  ['cancelJob', () => jobsApi.cancelJob('j1'), 'post', ['jobs/j1/cancel']],
  ['deleteJob', () => jobsApi.deleteJob('j1'), 'delete', ['jobs/j1']],

  // WebShark
  [
    'openWebSharkCapture',
    () => webSharkApi.openWebSharkCapture('e@x', form),
    'post',
    ['experiments/e%40x/webshark', form],
  ],
  [
    'createCaptureToken',
    () => webSharkApi.createCaptureToken('e@x', form),
    'post',
    ['experiments/e%40x/captureTokens', form],
  ],

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
    () => disksApi.getDiskDetails('/phenix/images/kali.qc2'),
    'get',
    ['disks/details', { params: { disk: '/phenix/images/kali.qc2' } }],
  ],
  [
    'getDiskDetails with a config',
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
    'commitDisk',
    () => disksApi.commitDisk('a.qc2'),
    'post',
    ['disks/commit', null, { params: { disk: 'a.qc2' } }],
  ],
  [
    'snapshotDisk',
    () => disksApi.snapshotDisk('a.qc2', 'b.qc2'),
    'request',
    [
      {
        method: 'post',
        url: 'disks/snapshot',
        params: { disk: 'a.qc2', new: 'b.qc2' },
      },
    ],
  ],
  [
    'rebaseDisk',
    () => disksApi.rebaseDisk('a.qc2', 'b.qc2', true),
    'request',
    [
      {
        method: 'post',
        url: 'disks/rebase',
        params: { disk: 'a.qc2', backing: 'b.qc2', unsafe: true },
      },
    ],
  ],
  [
    'resizeDisk',
    () => disksApi.resizeDisk('a.qc2', '+5G'),
    'request',
    [
      {
        method: 'post',
        url: 'disks/resize',
        params: { disk: 'a.qc2', size: '+5G' },
      },
    ],
  ],
  [
    'renameDisk',
    () => disksApi.renameDisk('a.qc2', 'b.qc2'),
    'request',
    [
      {
        method: 'post',
        url: 'disks/rename',
        params: { disk: 'a.qc2', new: 'b.qc2' },
      },
    ],
  ],
  [
    'cloneDisk',
    () => disksApi.cloneDisk('a.qc2', 'b.qc2'),
    'post',
    ['disks/clone/jobs', null, { params: { disk: 'a.qc2', new: 'b.qc2' } }],
  ],

  // experiments
  [
    'getExperimentOptions',
    () => experimentsApi.getExperimentOptions(),
    'get',
    ['/options'],
  ],
  [
    'createExperiment',
    () => experimentsApi.createExperiment({ name: 'exp' }),
    'post',
    ['experiments', { name: 'exp' }, { timeout: 0 }],
  ],
  [
    'listExperiments',
    () => experimentsApi.listExperiments(fetchConfig),
    'get',
    ['experiments?vms=false', fetchConfig],
  ],
  [
    'listSoh',
    () => experimentsApi.listSoh(fetchConfig),
    'get',
    ['soh', fetchConfig],
  ],
  [
    'getExperiment',
    () => experimentsApi.getExperiment('exp', { params: { pageNum: 2 } }),
    'get',
    ['experiments/exp', { params: { pageNum: 2 } }],
  ],
  [
    'updateExperiment',
    () => experimentsApi.updateExperiment('exp', '{"EXP":100}'),
    'patch',
    ['experiments/exp', '{"EXP":100}'],
  ],
  [
    'deleteExperiment',
    () => experimentsApi.deleteExperiment('exp'),
    'delete',
    ['experiments/exp'],
  ],
  [
    'startExperiment',
    () => experimentsApi.startExperiment('exp'),
    'post',
    ['experiments/exp/start'],
  ],
  [
    'stopExperiment',
    () => experimentsApi.stopExperiment('exp'),
    'post',
    ['experiments/exp/stop'],
  ],
  [
    'stopExperiment, forced',
    () => experimentsApi.stopExperiment('exp', { force: true }),
    'post',
    ['experiments/exp/stop', null, { params: { force: true } }],
  ],
  [
    'scheduleExperiment',
    () => experimentsApi.scheduleExperiment('exp', 'isolate-experiment'),
    'post',
    ['experiments/exp/schedule', { algorithm: 'isolate-experiment' }],
  ],
  [
    'triggerApps',
    () => experimentsApi.triggerApps('exp', 'soh,scorch'),
    'post',
    ['experiments/exp/trigger', null, { params: { apps: 'soh,scorch' } }],
  ],
  [
    'getExperimentApps',
    () => experimentsApi.getExperimentApps('exp'),
    'get',
    ['experiments/exp/apps'],
  ],
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
  [
    'getNetflowCapture',
    () => experimentsApi.getNetflowCapture('exp'),
    'get',
    ['experiments/exp/netflow'],
  ],
  [
    'startNetflowCapture',
    () => experimentsApi.startNetflowCapture('exp'),
    'post',
    ['experiments/exp/netflow'],
  ],
  [
    'stopNetflowCapture',
    () => experimentsApi.stopNetflowCapture('exp'),
    'delete',
    ['experiments/exp/netflow'],
  ],
  [
    'listNetflowCaptures',
    () => experimentsApi.listNetflowCaptures('exp'),
    'get',
    ['experiments/exp/netflow/captures'],
  ],
  [
    'headNetflowCapture',
    () =>
      experimentsApi.headNetflowCapture('exp', '20261028T153012.345Z', 'csv'),
    'head',
    [
      'experiments/exp/netflow/captures/20261028T153012.345Z',
      { params: { format: 'csv' } },
    ],
  ],
  [
    'listExperimentCaptures',
    () => experimentsApi.listExperimentCaptures('e@x'),
    'get',
    ['experiments/e%40x/captures'],
  ],

  // experiment files
  [
    'listExperimentFiles',
    () => experimentFilesApi.listExperimentFiles('exp'),
    'get',
    ['experiments/exp/files', undefined],
  ],
  [
    'listExperimentFiles with params',
    () =>
      experimentFilesApi.listExperimentFiles('exp', {
        params: { sortCol: 'date' },
      }),
    'get',
    ['experiments/exp/files', { params: { sortCol: 'date' } }],
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
    'deleteExperimentFile',
    () =>
      experimentFilesApi.deleteExperimentFile('e@x', {
        name: 'a b',
        path: 'x/a b',
      }),
    'delete',
    ['experiments/e%40x/files/a%20b', { params: { path: 'x/a b' } }],
  ],
  [
    'deleteExperimentFiles',
    () => experimentFilesApi.deleteExperimentFiles('e@x', ['a', 'b']),
    'post',
    ['experiments/e%40x/files/delete', { paths: ['a', 'b'] }],
  ],
  [
    'archiveExperimentFiles',
    () => experimentFilesApi.archiveExperimentFiles('e@x', ['a']),
    'post',
    ['experiments/e%40x/files/archive', { paths: ['a'] }],
  ],

  // VMs
  ['listVms', () => vmsApi.listVms('exp'), 'get', ['experiments/exp/vms']],
  [
    'listAllVms',
    () => vmsApi.listAllVms(fetchConfig),
    'get',
    ['vms', fetchConfig],
  ],
  [
    'updateVms',
    () => vmsApi.updateVms('exp', [{ name: 'a', dnb: true }]),
    'patch',
    ['experiments/exp/vms', { vms: [{ name: 'a', dnb: true }], total: 1 }],
  ],
  [
    'startVmActionsJob',
    () => vmsApi.startVmActionsJob('exp', 'restart', ['a', 'b']),
    'post',
    ['experiments/exp/vms/actions', { action: 'restart', vms: ['a', 'b'] }],
  ],
  ['getVm', () => vmsApi.getVm('exp', 'a'), 'get', ['experiments/exp/vms/a']],
  [
    'updateVm',
    () => vmsApi.updateVm('exp', 'a', { cpus: 2 }),
    'patch',
    ['experiments/exp/vms/a', { cpus: 2 }],
  ],
  [
    'redeployVm',
    () => vmsApi.redeployVm('exp', 'a', { cpus: 2, ram: 512 }),
    'post',
    ['experiments/exp/vms/a/redeploy', { cpus: 2, ram: 512 }],
  ],
  [
    'listVmSnapshots',
    () => vmsApi.listVmSnapshots('exp', 'a'),
    'get',
    ['experiments/exp/vms/a/snapshots'],
  ],
  [
    'createVmSnapshot',
    () => vmsApi.createVmSnapshot('exp', 'a', 'snap'),
    'post',
    ['experiments/exp/vms/a/snapshots', { filename: 'snap' }, { timeout: 0 }],
  ],
  [
    'restoreVmSnapshot',
    () => vmsApi.restoreVmSnapshot('exp', 'a', 'snap'),
    'post',
    ['experiments/exp/vms/a/snapshots/snap', {}, { timeout: 0 }],
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
  [
    'getVmScreenshot',
    () => vmsApi.getVmScreenshot('e x', 'a/b', 300, signal),
    'get',
    [
      'experiments/e%20x/vms/a%2Fb/screenshot.png',
      { params: { size: 300, base64: true }, responseType: 'text', signal },
    ],
  ],
  [
    'listVmForwards',
    () => vmsApi.listVmForwards('exp', 'a'),
    'get',
    ['experiments/exp/vms/a/forwards'],
  ],
  [
    'createVmForward',
    () => vmsApi.createVmForward('exp', 'a', { src: 1, host: 'h', dst: 2 }),
    'post',
    [
      'experiments/exp/vms/a/forwards',
      null,
      { params: { src: 1, host: 'h', dst: 2 } },
    ],
  ],
  [
    'deleteVmForward',
    () => vmsApi.deleteVmForward('exp', 'a', { host: 'h', dst: 2 }),
    'delete',
    ['experiments/exp/vms/a/forwards', { params: { host: 'h', dst: 2 } }],
  ],
  [
    'insertVmDisc',
    () => vmsApi.insertVmDisc('exp', 'a', '/isos/a.iso'),
    'post',
    [
      'experiments/exp/vms/a/cdrom',
      null,
      { params: { isoPath: '/isos/a.iso' } },
    ],
  ],
  [
    'ejectVmDisc',
    () => vmsApi.ejectVmDisc('exp', 'a'),
    'delete',
    ['experiments/exp/vms/a/cdrom'],
  ],
  [
    'listVmCaptures',
    () => vmsApi.listVmCaptures('exp', 'a'),
    'get',
    ['experiments/exp/vms/a/captures'],
  ],
  [
    'createVmCapture',
    () => vmsApi.createVmCapture('exp', 'a', 0, 'a_0.pcap'),
    'post',
    ['experiments/exp/vms/a/captures', { interface: 0, filename: 'a_0.pcap' }],
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
  [
    'deleteVmCaptures',
    () => vmsApi.deleteVmCaptures('exp', 'a'),
    'delete',
    ['experiments/exp/vms/a/captures'],
  ],
  [
    'mountVm',
    () => vmsApi.mountVm('exp', 'a'),
    'post',
    ['experiments/exp/vms/a/mount'],
  ],
  [
    'unmountVm',
    () => vmsApi.unmountVm('exp', 'a'),
    'delete',
    ['experiments/exp/vms/a/unmount'],
  ],
  [
    'listVmFiles',
    () => vmsApi.listVmFiles('exp', 'a', '/etc'),
    'get',
    ['experiments/exp/vms/a/files', { params: { path: '/etc' } }],
  ],
  [
    'copyFileToVm',
    () => vmsApi.copyFileToVm('exp', 'a', '/tmp', 'x/b.txt'),
    'post',
    [
      'experiments/exp/vms/a/files/copy',
      null,
      { params: { path: '/tmp', source: 'x/b.txt' } },
    ],
  ],
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

  // SCORCH
  [
    'listScorchPipelines',
    () => scorchApi.listScorchPipelines('exp', { signal }),
    'get',
    [
      'experiments/exp/scorch/pipelines',
      { signal, headers: { Accept: 'application/json' } },
    ],
  ],
  [
    'startScorchRun',
    () => scorchApi.startScorchRun('exp', 0),
    'post',
    ['experiments/exp/scorch/pipelines/0'],
  ],
  [
    'stopScorchRun',
    () => scorchApi.stopScorchRun('exp', 1),
    'delete',
    ['experiments/exp/scorch/pipelines/1'],
  ],
  [
    'clearScorchRun',
    () => scorchApi.clearScorchRun('exp', 1),
    'post',
    ['experiments/exp/scorch/pipelines/1/clear'],
  ],
  [
    'cleanupScorchRun',
    () => scorchApi.cleanupScorchRun('exp', 1),
    'post',
    ['experiments/exp/scorch/pipelines/1/cleanup'],
  ],
  [
    'getScorchLoop',
    () => scorchApi.getScorchLoop('exp', 1, 2),
    'get',
    ['experiments/exp/scorch/pipelines/1/2', ACCEPT_JSON],
  ],
  [
    'listScorchTerminals',
    () => scorchApi.listScorchTerminals('exp'),
    'get',
    ['experiments/exp/scorch/terminals', ACCEPT_JSON],
  ],
  [
    'getScorchComponent',
    () =>
      scorchApi.getScorchComponent({
        exp: 'exp',
        run: 1,
        loop: 2,
        stage: 'start',
        name: 'tap',
        extra: 'ignored',
      }),
    'get',
    ['experiments/exp/scorch/components/1/2/start/tap', ACCEPT_JSON],
  ],
  [
    'exitScorchTerminal',
    () => scorchApi.exitScorchTerminal('/base/api/v1/exit/7'),
    'post',
    ['/base/api/v1/exit/7', null, { baseURL: '' }],
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

beforeEach(() => {
  resetMocks();
  setActivePinia(createPinia());
  const answer = { data: { id: 'job1', status: 'running', revision: 1 } };
  for (const m of METHODS) axios[m].mockResolvedValue(answer);
});

describe('the API modules', () => {
  it.each(requests)('%s sends its request', async (_, call, method, args) => {
    await call();
    expect(onlyCall()).toStrictEqual([method, args]);
  });

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
    const exported = [
      authApi,
      configsApi,
      consoleApi,
      disksApi,
      experimentFilesApi,
      experimentsApi,
      hostsApi,
      jobsApi,
      logsApi,
      rolesApi,
      scorchApi,
      settingsApi,
      topologiesApi,
      usersApi,
      vmsApi,
      webSharkApi,
    ].flatMap((mod) => Object.keys(mod));
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

describe('names encoded into a path', () => {
  it.each([
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
      'getVmScreenshot',
      (name) => vmsApi.getVmScreenshot(name, name, 1, signal),
      (name) => ['experiments', name, 'vms', name, 'screenshot.png'],
    ],
    [
      'getExperimentFile',
      (name) =>
        experimentFilesApi.getExperimentFile('exp', { name, path: name }),
      (name) => ['experiments', 'exp', 'files', name],
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
  ])(
    '%s keeps each name one segment that decodes back to it',
    async (_, call, parts) => {
      await fc.assert(
        fc.asyncProperty(anyName, async (name) => {
          resetMocks();
          setActivePinia(createPinia());
          axios.get.mockResolvedValue({ data: {} });
          axios.post.mockResolvedValue({ data: { id: 'j', revision: 1 } });
          axios.delete.mockResolvedValue({});
          await call(name);
          const [, [url]] = onlyCall();
          expect(url).not.toMatch(/[?#]/);
          expect(decodedParts(url)).toEqual(parts(name));
        }),
      );
    },
  );
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
