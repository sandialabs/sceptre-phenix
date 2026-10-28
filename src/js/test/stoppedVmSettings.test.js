// The settings of a stopped experiment's VMs: the rules that compare them to
// the topology's values, read their port forwards and outcomes, and the
// controls, toolbar and menus that change them.
import { flushPromises } from '@vue/test-utils';
import fc from 'fast-check';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import ScheduleMenu from '@/components/experiment/ScheduleMenu.vue';
import StoppedSelectionToolbar from '@/components/experiment/StoppedSelectionToolbar.vue';
import VlanAliasMenu from '@/components/experiment/VlanAliasMenu.vue';
import VmPickFilter from '@/components/experiment/VmPickFilter.vue';
import VmSettingControl from '@/components/experiment/VmSettingControl.vue';
import { useColumnVisibility } from '@/components/experiment/useColumnVisibility.js';
import {
  SETTING_FIELDS,
  atDefaults,
  batchOutcome,
  changedSettings,
  choicesWith,
  createSaveClock,
  hasDefaults,
  resetPlan,
  scheduleMap,
  scheduledHosts,
  settingText,
  startupForwards,
  vlanIdError,
} from '@/components/experiment/vmSettings.js';
import { buttons, byLabel, mountComponent, textOf } from './helpers/mount.js';
import { allowAll, denyOnly } from './helpers/permissions.js';

beforeEach(() => resetMocks());

const DEFAULTS = {
  host: '',
  cpus: 1,
  ram: 1024,
  disk: '/phenix/images/a.qc2',
  inject_partition: 1,
  snapshot: true,
  dnb: false,
};

// a value of each setting other than its default
const OTHER = {
  host: 'n1',
  cpus: 2,
  ram: 2048,
  disk: '/phenix/images/b.qc2',
  inject_partition: 2,
  snapshot: false,
  dnb: true,
};

describe('the settings changed from the topology values', () => {
  it('are none for a VM that does not know them', () => {
    for (const defaults of [undefined, null, 'x']) {
      expect(changedSettings({ cpus: 2, defaults })).toEqual({});
      expect(hasDefaults({ defaults })).toBe(false);
      expect(atDefaults({ defaults })).toBe(true);
    }
  });

  it('are those that differ, whatever type each value came in', () => {
    const vm = {
      ...DEFAULTS,
      cpus: '1',
      ram: '2048',
      host: undefined,
      snapshot: 1,
      defaults: DEFAULTS,
    };
    expect(changedSettings(vm)).toEqual({ ram: 1024 });
    expect(atDefaults(vm)).toBe(false);
  });

  it('skip a setting the defaults do not give', () => {
    expect(changedSettings({ cpus: 3, defaults: { ram: 1 }, ram: 1 })).toEqual(
      {},
    );
  });

  it('name exactly the settings changed, for any set of changes', () => {
    fc.assert(
      fc.property(fc.subarray([...SETTING_FIELDS]), (fields) => {
        const vm = { ...DEFAULTS, defaults: { ...DEFAULTS } };
        for (const field of fields) vm[field] = OTHER[field];
        const changed = changedSettings(vm);
        expect(Object.keys(changed).sort()).toEqual([...fields].sort());
        for (const field of fields)
          expect(changed[field]).toBe(DEFAULTS[field]);
        expect(atDefaults(vm)).toBe(fields.length === 0);
      }),
      { numRuns: 50 },
    );
  });
});

describe('a setting as text', () => {
  it.each([
    ['host', '', undefined, 'no host'],
    ['host', 'n1', undefined, 'n1'],
    ['ram', 512, undefined, '512 MiB'],
    ['ram', 2048, undefined, '2 GiB'],
    ['ram', 1536, undefined, '1.5 GiB'],
    ['disk', '', undefined, 'no disk'],
    [
      'disk',
      '/p/a.qc2',
      [{ fullPath: '/p/a.qc2', relativePath: 'a.qc2' }],
      'a.qc2',
    ],
    ['disk', '/p/b.qc2', null, '/p/b.qc2'],
    ['snapshot', true, undefined, 'non-persistent'],
    ['snapshot', false, undefined, 'persistent'],
    ['dnb', true, undefined, 'do not boot'],
    ['dnb', false, undefined, 'boot'],
    ['cpus', 4, undefined, '4'],
    ['cpus', undefined, undefined, ''],
  ])('reads %s %j as words', (field, value, disks, text) => {
    expect(settingText(field, value, disks)).toBe(text);
  });
});

describe('the choices of a numeric setting', () => {
  it('add a value outside them, in order', () => {
    expect(choicesWith([1, 2, 4], 3)).toEqual([1, 2, 3, 4]);
    expect(choicesWith([1, 2, 4], '8')).toEqual([1, 2, 4, 8]);
  });

  it.each([2, '2', 0, '', undefined, 'x'])('are the same for %j', (value) => {
    expect(choicesWith([1, 2, 4], value)).toEqual([1, 2, 4]);
  });
});

describe('the port forwards a VM gets at start', () => {
  it.each([
    [undefined, []],
    [{}, []],
    [{ 'phenix/startup-autotunnel': '80' }, [['80', '127.0.0.1', '80']]],
    [
      { 'phenix/startup-autotunnel': ['80', '1:2', '3:h:4', '', 5, 'a:b:c:d'] },
      [
        ['80', '127.0.0.1', '80'],
        ['1', '127.0.0.1', '2'],
        ['3', 'h', '4'],
        '',
        '5',
        'a:b:c:d',
      ],
    ],
  ])('reads %j', (annotations, want) => {
    expect(
      startupForwards(annotations).map((f) =>
        'invalid' in f ? f.invalid : [f.srcPort, f.dstHost, f.dstPort],
      ),
    ).toEqual(want);
  });

  it('reads back any "port:host:dport" entry', () => {
    const part = fc.stringMatching(/^[a-z0-9.]{1,8}$/);
    fc.assert(
      fc.property(part, part, part, (src, host, dst) => {
        expect(
          startupForwards({
            'phenix/startup-autotunnel': [`${src}:${host}:${dst}`],
          }),
        ).toEqual([{ srcPort: src, dstHost: host, dstPort: dst }]);
      }),
      { numRuns: 50 },
    );
  });
});

describe('the outcome of a change to several VMs', () => {
  it('counts the VMs answered as done, and the errors as failed or skipped', () => {
    expect(
      batchOutcome({
        vms: [{ name: 'a' }],
        errors: [
          { name: 'b', status: 'skipped', error: 'no permission' },
          { name: 'c', error: 'busy' },
        ],
      }),
    ).toEqual([
      { name: 'a', status: 'succeeded', error: null, retryable: false },
      {
        name: 'b',
        status: 'skipped',
        error: 'no permission',
        retryable: false,
      },
      { name: 'c', status: 'failed', error: 'busy', retryable: true },
    ]);
    expect(batchOutcome(undefined)).toEqual([]);
  });
});

describe('the VMs a reset sends', () => {
  it('leaves out the VMs on the page it cannot change, as skipped', () => {
    const vms = [
      { name: 'old', defaults: null },
      { name: 'same', cpus: 1, defaults: { cpus: 1 } },
      { name: 'web', cpus: 2, defaults: { cpus: 1 } },
    ];
    expect(resetPlan(['old', 'same', 'web', 'away'], vms)).toEqual({
      send: ['web', 'away'],
      skipped: [
        {
          name: 'old',
          status: 'skipped',
          error: 'its topology values are not known',
          retryable: false,
        },
        {
          name: 'same',
          status: 'skipped',
          error: 'it has the topology values',
          retryable: false,
        },
      ],
    });
  });
});

describe('the order of reads and saves', () => {
  const row = { name: 'web', cpus: 2, ram: 2048 };

  it('keeps a field saved after a read was sent, until a later read', () => {
    const clock = createSaveClock();
    const before = clock.sent();
    clock.saved('web', ['cpus'], 5);
    const read = { name: 'web', cpus: 1, ram: 1024 };
    expect(clock.merge(read, row, { sentAt: before })).toEqual({
      name: 'web',
      cpus: 2,
      ram: 1024,
    });

    const after = clock.sent();
    expect(clock.merge(read, row, { sentAt: after })).toBe(read);
    // the later read has the save, so it is forgotten
    expect(clock.merge(read, row, { sentAt: before })).toBe(read);
  });

  it('keeps a saved field from a websocket update not newer than the save', () => {
    const clock = createSaveClock();
    clock.saved('web', ['cpus'], 5);
    const update = { name: 'web', cpus: 1, ram: 1024 };
    expect(clock.merge(update, row, { seq: 5 }).cpus).toBe(2);
    expect(clock.merge(update, row, { seq: 6 })).toBe(update);
    expect(clock.merge(update, row, {})).toBe(update);
    clock.saved('web', ['ram']);
    expect(clock.merge(update, row, { seq: 1 })).toEqual({
      name: 'web',
      cpus: 2,
      ram: 1024,
    });
  });

  it('keeps the fields in flight, and takes a VM with no row as read', () => {
    const clock = createSaveClock();
    const read = { name: 'web', cpus: 1 };
    expect(clock.merge(read, row, { keep: ['ram', 'disk'] })).toEqual({
      name: 'web',
      cpus: 1,
      ram: 2048,
    });
    expect(clock.merge(read, undefined, { keep: ['cpus'] })).toBe(read);
  });

  it('never lets a read sent before a save change the saved field', () => {
    fc.assert(
      fc.property(
        fc.array(fc.constantFrom('read', 'save'), { maxLength: 8 }),
        (steps) => {
          const clock = createSaveClock();
          const reads = [];
          let saved = false;
          for (const step of steps) {
            if (step === 'read') reads.push(clock.sent());
            else {
              clock.saved('web', ['cpus']);
              saved = true;
            }
          }
          const lastSave = steps.lastIndexOf('save');
          const sentBefore = steps
            .slice(0, lastSave)
            .filter((s) => s === 'read').length;
          for (const [i, sentAt] of reads.entries()) {
            const merged = clock.merge({ name: 'web', cpus: 1 }, row, {
              sentAt,
            });
            if (saved && i < sentBefore) expect(merged.cpus).toBe(2);
          }
        },
      ),
      { numRuns: 50 },
    );
  });
});

describe('a schedule', () => {
  it('names the VMs with a host, and its hosts once each, sorted', () => {
    const map = scheduleMap([
      { vm: 'a', host: 'n2' },
      { vm: 'b', host: 'n1' },
      { vm: 'c', host: 'n2' },
      { vm: 'd', host: '' },
      { vm: '', host: 'n3' },
    ]);
    expect(map).toEqual({ a: 'n2', b: 'n1', c: 'n2' });
    expect(scheduledHosts(map)).toEqual(['n1', 'n2']);
    expect(scheduleMap(undefined)).toEqual({});
    expect(scheduledHosts(null)).toEqual([]);
  });
});

describe('a VLAN ID typed in', () => {
  it('is any whole number from 0 to 4094', () => {
    fc.assert(
      fc.property(fc.integer({ min: 0, max: 4094 }), (id) => {
        expect(vlanIdError(String(id))).toBeNull();
        expect(vlanIdError(` ${id} `)).toBeNull();
      }),
      { numRuns: 50 },
    );
  });

  it('is refused for anything else', () => {
    fc.assert(
      fc.property(
        fc.oneof(
          fc.integer({ min: 4095 }).map(String),
          fc.integer({ max: -1 }).map(String),
          fc.double({ noInteger: true, noNaN: true }).map(String),
          fc.constantFrom('', 'x', '1e3', null, undefined),
        ),
        (text) => {
          expect(vlanIdError(text)).toBe('Enter a whole number from 0 to 4094');
        },
      ),
      { numRuns: 50 },
    );
  });
});

describe('the column toggles', () => {
  const TOGGLES = [
    { key: 'host', label: 'Host', storageKey: 'showStoppedHostColumn' },
    { key: 'ipv4', label: 'IP', storageKey: 'showStoppedIPv4Column' },
  ];
  const use = async (username) => {
    let state;
    await mountComponent(
      defineComponent({
        setup() {
          state = useColumnVisibility(TOGGLES, () => username);
          return () => h('div');
        },
      }),
    );
    return state;
  };

  it('show every column until the user hides one, remembering it per user', async () => {
    localStorage.setItem('bob.showStoppedHostColumn', 'false');
    const alice = await use('alice');
    expect(alice.columnVisibility).toEqual({ host: true, ipv4: true });

    alice.columnVisibility.ipv4 = false;
    alice.persistColumnVisibility(TOGGLES[1]);
    expect(localStorage.getItem('alice.showStoppedIPv4Column')).toBe('false');
    expect((await use('alice')).columnVisibility).toEqual({
      host: true,
      ipv4: false,
    });
    expect((await use('bob')).columnVisibility).toEqual({
      host: false,
      ipv4: true,
    });
    expect(alice.columnToggles).toBe(TOGGLES);
  });
});

describe('a VM setting control', () => {
  const control = (field, vm, props) =>
    mountComponent(VmSettingControl, {
      props: { field, vm: { name: 'web', ...vm }, editable: true, ...props },
    });

  it('shows an external VM as such', async () => {
    const wrapper = await control(
      'host',
      { external: true },
      { editable: false },
    );
    expect(textOf(wrapper)).toBe('EXTERNAL');
  });

  it.each([
    ['cpus', {}, 'unknown'],
    ['ram', {}, 'unknown'],
    ['host', {}, 'none'],
  ])('says %s is not known as text', async (field, vm, text) => {
    const wrapper = await control(field, vm, { editable: false });
    expect(textOf(wrapper)).toBe(text);
  });

  it("offers the VM's host when the host list does not hold it", async () => {
    const wrapper = await control('host', { host: 'old' }, { hosts: ['n1'] });
    expect(
      byLabel(wrapper, 'Host for VM web')
        .findAll('option')
        .map((o) => textOf(o)),
    ).toEqual(['old', 'n1']);
  });

  it('offers a VM with no host the hosts, with No host picked', async () => {
    const wrapper = await control('host', { host: '' }, { hosts: ['n1'] });
    const options = byLabel(wrapper, 'Host for VM web').findAll('option');
    expect(options.map((o) => [textOf(o), o.element.disabled])).toEqual([
      ['No host', true],
      ['n1', false],
    ]);
  });

  it('stays enabled while a change saves, so the keyboard focus stays on it', async () => {
    const wrapper = await mountComponent(VmSettingControl, {
      props: { field: 'cpus', vm: { name: 'web', cpus: 1 }, editable: true },
      attachTo: document.body,
    });
    const select = byLabel(wrapper, 'CPUs for VM web');
    select.element.focus();
    await wrapper.setProps({ pending: true, pendingValue: 2 });
    expect(wrapper.find('fieldset').attributes('disabled')).toBeUndefined();
    expect(wrapper.find('fieldset').attributes('aria-busy')).toBe('true');
    expect(select.element.disabled).toBe(false);
    expect(document.activeElement).toBe(select.element);
  });

  it('says Saving in a status region that was there before the change', async () => {
    const wrapper = await control('cpus', { cpus: 1 });
    const status = wrapper.find('[role="status"]');
    expect(status.exists()).toBe(true);
    expect(textOf(status)).toBe('');
    await wrapper.setProps({ pending: true, pendingValue: 2 });
    expect(wrapper.find('[role="status"]').element).toBe(status.element);
    expect(textOf(status)).toBe('Saving');
  });

  it('marks a switch unavailable while its change saves', async () => {
    const wrapper = await control(
      'snapshot',
      { snapshot: false },
      { pending: true, pendingValue: true },
    );
    const toggle = byLabel(wrapper, 'Persistent disk for VM web');
    expect(toggle.attributes('aria-disabled')).toBe('true');
    expect(toggle.attributes('disabled')).toBeUndefined();
    await toggle.trigger('click');
    expect(wrapper.emitted('change')).toBeUndefined();
  });

  it('sends the last value picked while a change saved, once it is saved', async () => {
    const wrapper = await control(
      'cpus',
      { cpus: 1 },
      { pending: true, pendingValue: 2 },
    );
    await byLabel(wrapper, 'CPUs for VM web').setValue('3');
    expect(wrapper.emitted('change')).toBeUndefined();

    await wrapper.setProps({ pending: false, vm: { name: 'web', cpus: 2 } });
    expect(wrapper.emitted('change')).toEqual([[3]]);
  });

  it('sends nothing more when the value picked while it saved was saved', async () => {
    const wrapper = await control(
      'cpus',
      { cpus: 1 },
      { pending: true, pendingValue: 2 },
    );
    await byLabel(wrapper, 'CPUs for VM web').setValue('2');
    await wrapper.setProps({ pending: false, vm: { name: 'web', cpus: 2 } });
    expect(wrapper.emitted('change')).toBeUndefined();
  });

  it('sends nothing while a change saves', async () => {
    const wrapper = await control(
      'dnb',
      { dnb: false },
      { pending: true, pendingValue: true },
    );
    wrapper.vm.change(false);
    expect(wrapper.emitted('change')).toBeUndefined();
    expect(byLabel(wrapper, 'Boot VM web').attributes('aria-checked')).toBe(
      'false',
    );
  });
});

describe('the stopped selection toolbar', () => {
  const toolbar = (props) =>
    mountComponent(StoppedSelectionToolbar, {
      props: { selected: ['a', 'b'], experiment: 'exp1', ...props },
    });

  it('offers Reset settings only when the selection can reset', async () => {
    rbac.allowed = allowAll;
    expect(buttons(await toolbar())).toEqual([
      'Set selected VMs to boot',
      'Set selected VMs to not boot',
      'Make the disks of selected VMs persistent',
      'Make the disks of selected VMs non-persistent',
    ]);
    expect(buttons(await toolbar({ canReset: true }))).toContain(
      'Reset settings of selected VMs',
    );
  });

  it('greys out every button while the experiment starts', async () => {
    const wrapper = await toolbar({ disabled: true });
    expect(buttons(wrapper).every((b) => b.endsWith('(disabled)'))).toBe(true);
  });

  it('asks the page to act, but not while an action waits', async () => {
    const wrapper = await toolbar();
    await byLabel(wrapper, 'Set selected VMs to boot').trigger('click');
    expect(wrapper.emitted('act')).toEqual([['boot']]);

    await wrapper.setProps({ pending: 'reset', canReset: true });
    expect(textOf(wrapper.find('.stopped-selection-count'))).toBe(
      'Resetting 2 VMs…',
    );
    expect(buttons(wrapper)).toContain(
      'Reset settings of selected VMs (spinning)',
    );
  });

  it('offers nothing to a role that may not change every selected VM', async () => {
    rbac.allowed = denyOnly(['vms', 'patch', 'exp1/b']);
    expect(buttons(await toolbar())).toEqual([]);
  });
});

describe('the Schedule menu', () => {
  const menu = (props) =>
    mountComponent(ScheduleMenu, {
      props: { schedulers: [{ name: 'round-robin', builtin: true }], ...props },
    });

  it.each([
    [null, 3, 'Pick a scheduler to assign every VM to a host'],
    [0, 3, 'No VM has a host yet'],
    [2, 3, '2 of 3 VMs have a host'],
    [1, 1, '1 of 1 VM has a host'],
  ])('says %j of %i VMs have a host', async (pinned, total, text) => {
    const wrapper = await menu({ pinned, total });
    expect(textOf(wrapper.find('.schedule-menu-note'))).toBe(text);
  });

  it('says how many VMs have a host when the total is not known', async () => {
    const wrapper = await menu({ pinned: 2, total: null });
    expect(textOf(wrapper.find('.schedule-menu-note'))).toBe(
      '2 VMs have a host',
    );
  });

  it('is a named menu of menu items, which its note describes', async () => {
    const wrapper = await menu({ pinned: 0, total: 3 });
    const content = wrapper.find('.dropdown-content');
    expect(content.attributes('role')).toBe('menu');
    expect(content.attributes('aria-label')).toBe('Schedulers');
    const note = wrapper.find('.schedule-menu-note');
    expect(content.attributes('aria-describedby')).toBe(note.attributes('id'));
    expect(note.attributes('aria-hidden')).toBe('true');
    expect(
      wrapper.findAll('.schedule-menu-item').map((i) => i.attributes('role')),
    ).toEqual(['menuitem']);
  });

  it('spins and holds its button while a schedule waits', async () => {
    const wrapper = await menu({ scheduling: true });
    expect(buttons(wrapper)).toEqual(['Schedule (spinning) (disabled)']);
  });

  it('says what each scheduler does in its tooltip', async () => {
    const wrapper = await menu({
      schedulers: [
        { name: 'round-robin', builtin: true, description: 'Spreads VMs.' },
        { name: 'mine', builtin: false },
      ],
    });
    const items = wrapper.findAll('.schedule-menu-item');
    const tips = wrapper
      .findAllComponents({ name: 'BTooltip' })
      .filter((t) => t.classes('schedule-menu-tip'));
    expect(tips.map((t) => t.props('label'))).toEqual([
      'Spreads VMs.',
      'A user scheduler: the server runs phenix-scheduler-mine.',
    ]);
    expect(items.map((i) => i.attributes('aria-description'))).toEqual([
      'Spreads VMs.',
      'A user scheduler: the server runs phenix-scheduler-mine.',
    ]);
    expect(tips.map((t) => t.props('position'))).toEqual([
      'is-left',
      'is-left',
    ]);
  });

  it('asks the page to schedule with the scheduler picked', async () => {
    const wrapper = await menu();
    await wrapper.find('.schedule-menu-item').trigger('click');
    await flushPromises();
    expect(wrapper.emitted('schedule')).toEqual([['round-robin']]);
  });
});

describe('the dialogs of the toolbar menus', () => {
  it('names the VLAN IDs dialog', async () => {
    const wrapper = await mountComponent(VlanAliasMenu, {
      props: { experiment: 'exp1', vlans: [{ alias: 'EXP', vlan: 1 }] },
    });
    const content = wrapper.find('.dropdown-content');
    expect(content.attributes('role')).toBe('dialog');
    expect(content.attributes('aria-label')).toBe('VLAN IDs of the aliases');
  });

  it('names a filter dialog by its title', async () => {
    const wrapper = await mountComponent(VmPickFilter, {
      props: {
        kind: 'host',
        title: 'Hosts',
        icon: 'server',
        note: 'n',
        hint: 'h',
        emptyReason: 'e',
        options: ['n1'],
      },
    });
    const content = wrapper.find('.dropdown-content');
    expect(content.attributes('role')).toBe('dialog');
    expect(content.attributes('aria-label')).toBe('Hosts filter');
  });
});
