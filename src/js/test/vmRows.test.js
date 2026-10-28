// The running experiment's VM rows: VM lists ordered against the per-VM
// websocket events around them, and what each event does to its row.
import fc from 'fast-check';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';

const { modules, resetMocks, ws } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/websocket', modules.websocket);

import {
  VM_EVENT_TYPES,
  applyVmEvent,
  createVmSeqs,
  eventVmName,
  replaceVm,
  useVmRows,
} from '@/components/experiment/vmRows.js';
import { wsMsg } from './helpers/fixtures.js';
import { mountComponent } from './helpers/mount.js';

beforeEach(() => resetMocks());

// a per-VM event about vm1 of exp1, at seq
const vmMsg = (type, action, result, seq) =>
  wsMsg(type, 'exp1/vm1', action, result, seq);

describe('ordering VM lists against VM changes', () => {
  const vm = (state) => ({ name: 'vm1', state });

  it('keeps a row changed after the list was read', () => {
    const seqs = createVmSeqs();
    const shown = [{ ...vm('QUIT'), screenshot: 'a.png' }];
    seqs.record('vm1', vmMsg('experiment/vm', 'stop', vm('QUIT'), 20));

    expect(seqs.listed(shown, [vm('RUNNING')], 10)).toEqual(shown);
    // a list read after the change is taken, keeping the screenshot shown
    expect(seqs.listed(shown, [vm('RUNNING')], 30)).toEqual([
      { ...vm('RUNNING'), screenshot: 'a.png' },
    ]);
  });

  it('keeps a VM deleted after the list was read out of it', () => {
    const seqs = createVmSeqs();
    seqs.record('vm1', vmMsg('experiment/vm', 'delete', undefined, 20));
    expect(seqs.listed([], [vm('RUNNING')], 10)).toEqual([]);
    expect(seqs.listed([], [vm('RUNNING')], undefined)).toEqual([]);
  });

  it.each([
    ['a message without a seq', vmMsg('experiment/vm', 'stop', {}, undefined)],
    [
      'an error, which changes no state',
      vmMsg('experiment/vm', 'error', {}, 20),
    ],
  ])('records nothing for %s', (_, msg) => {
    const seqs = createVmSeqs();
    seqs.record('vm1', msg);
    expect(seqs.listed([vm('QUIT')], [vm('RUNNING')], 10)).toEqual([
      { ...vm('RUNNING'), screenshot: undefined },
    ]);
  });

  it('forgets every change once cleared', () => {
    const seqs = createVmSeqs();
    seqs.record('vm1', vmMsg('experiment/vm', 'delete', undefined, 20));
    seqs.clear();
    expect(seqs.listed([], [vm('RUNNING')], 3)).toHaveLength(1);
  });

  it('shows each VM as the newest of its change and the lists', () => {
    const state = fc.constantFrom('RUNNING', 'QUIT');
    // a change, or a list read up to `back` changes ago
    const step = fc.record({ state, list: fc.boolean(), back: fc.nat(3) });
    fc.assert(
      fc.property(fc.array(step, { maxLength: 10 }), (steps) => {
        const seqs = createVmSeqs();
        let shown = [vm('RUNNING')];
        let newest = { seq: 0, state: 'RUNNING' };
        // changes arrive in order, and each list after those it read
        let seq = 0;
        let listSeq = 0;
        for (const { state, list, back } of steps) {
          if (list) {
            listSeq = Math.max(listSeq, seq - back);
            shown = seqs.listed(shown, [vm(state)], listSeq);
            if (listSeq >= newest.seq) newest = { seq: listSeq, state };
          } else {
            const action = state == 'QUIT' ? 'stop' : 'start';
            seqs.record('vm1', vmMsg('experiment/vm', action, {}, ++seq));
            shown = [vm(state)];
            newest = { seq, state };
          }
        }
        expect(shown.map((row) => row.state)).toEqual([newest.state]);
      }),
    );
  });
});

describe('useVmRows', () => {
  // a component holding useVmRows' seqs
  const Host = defineComponent({
    setup: () => ({ seqs: useVmRows() }),
    render: () => h('div'),
  });

  it('forgets its changes when the websocket reconnects, until unmounted', async () => {
    const wrapper = await mountComponent(Host);
    const { seqs } = wrapper.vm;
    seqs.record('vm1', vmMsg('experiment/vm', 'delete', undefined, 20));

    ws.reconnect.forEach((f) => f());
    expect(seqs.listed([], [{ name: 'vm1' }], 3)).toHaveLength(1);

    wrapper.unmount();
    expect(ws.reconnect).toEqual([]);
  });
});

describe('eventVmName', () => {
  it.each([
    ['exp1/vm1', { vms: [] }, 'vm1'],
    ['exp2/vm1', { vms: [] }, null],
    ['exp1/vm1', {}, null],
    ['exp1', { vms: [] }, null],
  ])('names the VM of %s, or none', (name, experiment, expected) => {
    const msg = wsMsg('experiment/vm', name, 'update');
    expect(eventVmName(msg, 'exp1', experiment)).toBe(expected);
  });
});

describe('replaceVm', () => {
  it.each([
    ['keeps the screenshot while the VM stays running', true, true, 'old.png'],
    ['drops it when the VM stops', true, false, ''],
    ['drops it when the VM starts again', false, true, ''],
  ])('%s', (_, before, after, screenshot) => {
    const experiment = {
      vms: [{ name: 'vm1', running: before, screenshot: 'old.png' }],
    };
    const vms = experiment.vms;
    replaceVm(experiment, { name: 'vm1', running: after, host: 'h' });
    expect(experiment.vms).not.toBe(vms);
    expect(experiment.vms).toEqual([
      { name: 'vm1', running: after, host: 'h', screenshot },
    ]);
  });

  it('ignores a VM the table does not show', () => {
    const experiment = { vms: [{ name: 'vm1' }] };
    const vms = experiment.vms;
    replaceVm(experiment, { name: 'gone' });
    expect(experiment.vms).toBe(vms);
  });
});

describe('applyVmEvent', () => {
  // the experiment showing vm1 as `row`, after the event, and the toast
  const apply = (type, action, result, row = {}) => {
    const experiment = { vms: [{ name: 'vm1', running: true, ...row }] };
    const notice = applyVmEvent(experiment, vmMsg(type, action, result), 'vm1');
    return { row: experiment.vms[0], vms: experiment.vms, notice };
  };
  const success = (message) => ({ message, type: 'is-success' });
  const warning = (message) => ({ message, type: 'is-warning' });
  const danger = (message) => ({ message, type: 'is-danger' });

  it('handles each VM event type', () => {
    expect(VM_EVENT_TYPES).toEqual([
      'experiment/vm',
      'experiment/vm/commit',
      'experiment/vm/memorySnapshot',
      'experiment/vm/screenshot',
      'experiment/vm/capture',
      'experiment/vm/snapshot',
    ]);
  });

  // each event, the row it leaves (null: gone), and its toast
  it.each([
    [
      'experiment/vm',
      'update',
      { name: 'vm1', running: true, host: 'h' },
      { host: 'h' },
      success('The VM vm1 has been successfully updated.'),
    ],
    [
      'experiment/vm',
      'delete',
      undefined,
      null,
      success('The vm1 VM was killed.'),
    ],
    [
      'experiment/vm',
      'stop',
      { name: 'vm1', running: false },
      { running: false },
      null,
    ],
    [
      'experiment/vm',
      'cdrom-ejected',
      undefined,
      {},
      success('The optical disc for vm1 was successfully ejected.'),
    ],
    [
      'experiment/vm',
      'redeployed',
      { name: 'vm1', running: true, cpus: 4 },
      { cpus: 4 },
      success('Redeployed vm1'),
    ],
    [
      'experiment/vm',
      'errorRedeploying',
      undefined,
      { busy: false },
      danger('Redeploying the vm1 VM failed.'),
    ],
    ['experiment/vm', 'error', { error: 'boom' }, {}, danger('boom')],
    [
      'experiment/vm/commit',
      'committing',
      { disk: 'd.qc2' },
      { busy: true, percent: 0 },
      warning(
        'phēnix is creating a backing image with name d.qc2 for the vm1 VM.',
      ),
    ],
    [
      'experiment/vm/commit',
      'commit',
      { disk: 'd.qc2', vm: { name: 'vm1', running: true, disk: 'd.qc2' } },
      { busy: false, disk: 'd.qc2' },
      success(
        'The backing image with name d.qc2 for the vm1 VM was successfully created.',
      ),
    ],
    [
      'experiment/vm/memorySnapshot',
      'commit',
      { disk: 'm.elf' },
      { busy: false },
      success(
        'A memory snapshot with name m.elf for the vm1 VM was successfully created.',
      ),
    ],
    [
      'experiment/vm/memorySnapshot',
      'errorCommitting',
      undefined,
      { busy: false },
      danger('Creating a memory snapshot for the vm1 VM failed.'),
    ],
    [
      'experiment/vm/snapshot',
      'progress',
      { percent: 0.426 },
      { busy: true, percent: 43 },
      null,
    ],
    [
      'experiment/vm/snapshot',
      'restoring',
      undefined,
      { busy: true },
      warning('phēnix is restoring a snapshot for the vm1 VM.'),
    ],
    [
      'experiment/vm/snapshot',
      'errorCreating',
      undefined,
      { busy: false },
      danger('Creating a snapshot for the vm1 VM failed.'),
    ],
    [
      'experiment/vm/screenshot',
      'update',
      { screenshot: 'new.png' },
      { screenshot: 'new.png' },
      null,
    ],
    [
      'experiment/vm/capture',
      'start',
      { interface: 1, filename: 'f.pcap' },
      {
        captures: [
          { interface: 0 },
          { vm: 'vm1', interface: 1, filename: 'f.pcap' },
        ],
      },
      success('Packet capture was started on interface 1 of the vm1 VM.'),
    ],
    [
      'experiment/vm/capture',
      'stop',
      undefined,
      { captures: [] },
      success('Packet capture was stopped for the vm1 VM.'),
    ],
  ])('applies %s %s', (type, action, result, row, notice) => {
    const captures = [{ interface: 0 }];
    const applied = apply(type, action, result, { captures });
    if (row === null) {
      expect(applied.vms).toEqual([]);
    } else {
      expect(applied.row).toMatchObject(row);
    }
    expect(applied.notice).toEqual(notice);
  });

  it.each([
    ['experiment/vm/commit', 'committing'],
    ['experiment/vm/memorySnapshot', 'commit'],
    ['experiment/vm/snapshot', 'create'],
  ])('says nothing of %s %s for a VM not shown', (type, action) => {
    const experiment = { vms: [] };
    const msg = vmMsg(type, action, { disk: 'd' });
    expect(applyVmEvent(experiment, msg, 'vm1')).toBeNull();
  });

  it('still says a capture started on a VM not shown', () => {
    const msg = vmMsg('experiment/vm/capture', 'start', {});
    expect(applyVmEvent({ vms: [] }, msg, 'vm1')).toEqual(
      success('Packet capture was started for the vm1 VM.'),
    );
  });

  it('changes a screenshot in place, keeping the rows', () => {
    const { vms, row } = apply('experiment/vm/screenshot', 'update', {
      screenshot: 'b.png',
    });
    expect(vms[0]).toBe(row);
  });
});
