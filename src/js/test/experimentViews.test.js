// The stopped experiment page, the VM tiles, and the VM labels and mount
// browser modals.
import { flushPromises } from '@vue/test-utils';
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  capturing,
  lastNotice,
  loaderSpy,
  modules,
  noticeText,
  notices,
  notify,
  rbac,
  resetMocks,
  ws,
} = await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/api/vnc.js', modules.vnc);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/notify.js', modules.notify);
// the pages' lists come from the tests
vi.mock('@/utils/pageLoader.js', capturing);

import DiskSelect from '@/components/experiment/DiskSelect.vue';
import { EXPERIMENT_PROGRESS } from '@/components/experiment/headerProgress.js';
import StoppedExperiment from '@/views/experiment/StoppedExperiment.vue';
import VMtilesView from '@/views/experiment/VMtilesView.vue';
import VMLabelsModal from '@/components/VMLabelsModal.vue';
import VMMountBrowserModal from '@/components/VMMountBrowserModal.vue';
import { never } from './helpers/async.js';
import { pathSegment } from './helpers/arbitraries.js';
import { disk, httpError, wsMsg } from './helpers/fixtures.js';
import {
  byLabel,
  confirmDialog,
  dialogs,
  mountComponent,
  mountPage,
  tableRows,
  textOf,
  toasts,
  tooltipButtons,
} from './helpers/mount.js';
import { allowAll, denyAll, denyOnly } from './helpers/permissions.js';

beforeEach(() => {
  resetMocks({ store: { token: 't', features: [], role: null } });
  // the pages' own lists never arrive
  axios.get.mockImplementation(never);
});

const button = (wrapper, text) =>
  wrapper.findAll('button').find((b) => textOf(b) === text);

describe('StoppedExperiment', () => {
  // the page on experiment exp1, with these VMs once it has loaded
  const stopped = (vms, data) =>
    mountPage(StoppedExperiment, {
      route: '/experiment/exp1',
      data: { ...(vms && { experiment: { name: 'exp1', vms } }), ...data },
    });

  const publish = (msg) => {
    for (const handler of ws.handlers) handler(msg);
    return flushPromises();
  };

  it.each([
    [
      'VM updates',
      (exp) =>
        wsMsg('experiment/vm', `${exp}/vm1`, 'update', {
          name: 'vm1',
          host: 'b',
        }),
    ],
    [
      'schedules',
      (exp) =>
        wsMsg('experiment', exp, 'schedule', {
          schedule: [{ vm: 'vm1', host: 'b' }],
        }),
    ],
  ])('applies %s only for its own experiment', async (_, message) => {
    const page = await stopped([{ name: 'vm1', host: 'a' }], {
      hosts: ['a', 'b'],
    });
    const host = () => byLabel(page, 'Host for VM vm1').element.value;

    await publish(message('exp2'));
    expect(host()).toBe('a');

    await publish(message('exp1'));
    expect(host()).toBe('b');
  });

  it('ignores publishes before the experiment has loaded', async () => {
    await stopped();
    await publish(
      wsMsg('experiment/vm', 'exp1/vm1', 'update', { name: 'vm1' }),
    );
    expect(toasts()).toEqual([]);
  });

  it.each([
    ['shows', allowAll, ['VMs', 'Files']],
    ['hides', denyAll, ['VMs']],
  ])('%s the Files tab by file listing permission', async (_, role, want) => {
    rbac.allowed = vi.fn(role);
    const page = await stopped();
    const tabs = page
      .findAll('.tabs li')
      .filter((li) => li.element.style.display !== 'none');
    expect(tabs.map(textOf)).toEqual(want);
    expect(rbac.allowed).toHaveBeenCalledWith(
      'experiments/files',
      'list',
      'exp1',
    );
  });

  describe('its Start', () => {
    // the header's actions, each with whether it spins or is greyed out
    const actions = (page) =>
      [
        'Start experiment',
        'Set selected VMs to boot',
        'Set selected VMs to not boot',
        'Assign VLAN IDs to aliases',
        'Schedule VMs',
      ].map((label) => {
        const b = byLabel(page, label);
        return [
          label,
          b.classes('is-loading'),
          b.attributes('disabled') !== undefined,
        ];
      });
    const idle = (page) =>
      expect(actions(page).every(([, spins, off]) => !spins && !off)).toBe(
        true,
      );

    it('starts after confirming, spinning while the other actions are greyed out, then shows the running view', async () => {
      const start = Promise.withResolvers();
      axios.post.mockReturnValue(start.promise);
      const page = await stopped([{ name: 'vm1' }], { selectedRows: ['vm1'] });
      idle(page);

      await byLabel(page, 'Start experiment').trigger('click');
      expect(axios.post).not.toHaveBeenCalled();
      expect(textOf(dialogs()[0])).toContain(
        'This will start the exp1 experiment.',
      );
      await confirmDialog();
      expect(axios.post.mock.calls).toEqual([['experiments/exp1/start']]);
      expect(actions(page)).toEqual([
        ['Start experiment', true, false],
        ['Set selected VMs to boot', false, true],
        ['Set selected VMs to not boot', false, true],
        ['Assign VLAN IDs to aliases', false, true],
        ['Schedule VMs', false, true],
      ]);
      expect(page.find('.loading-overlay').exists()).toBe(false);
      // a second press while waiting sends nothing more
      await page.vm.start();
      expect(axios.post).toHaveBeenCalledTimes(1);

      start.resolve({});
      await flushPromises();
      expect(page.emitted('running')).toEqual([[true]]);
    });

    it('does not start when the confirm is canceled', async () => {
      const page = await stopped([{ name: 'vm1' }], { selectedRows: ['vm1'] });

      await byLabel(page, 'Start experiment').trigger('click');
      dialogs()[0].querySelector('footer .button:first-child').click();
      await flushPromises();
      expect(axios.post).not.toHaveBeenCalled();
      idle(page);
      expect(page.router.currentRoute.value.path).toBe('/experiment/exp1');
    });

    it('stays on the stopped view when the start fails', async () => {
      const err = new Error('no');
      axios.post.mockRejectedValue(err);
      const page = await stopped([{ name: 'vm1' }], { selectedRows: ['vm1'] });

      await byLabel(page, 'Start experiment').trigger('click');
      await confirmDialog();
      expect(notify.error).toHaveBeenCalledWith(err);
      expect(page.emitted('running')).toBeUndefined();
      idle(page);
    });

    it('stays on the stopped view after a dry run, tells what it did, and reloads the VMs', async () => {
      const script = '/phenix/experiments/exp1/mm_files/exp1.mm';
      axios.post.mockResolvedValue({
        data: {
          experiment: { name: 'exp1', running: false },
          dryRun: true,
          warnings: [],
          mmScript: script,
        },
      });
      const page = await stopped([{ name: 'vm1' }], { selectedRows: ['vm1'] });
      const loads = loaderSpy.load.mock.calls.length;

      await byLabel(page, 'Start experiment').trigger('click');
      const dry = dialogs()
        .at(-1)
        .querySelector('label.checkbox input[type="checkbox"]');
      dry.checked = true;
      dry.dispatchEvent(new Event('change'));
      await flushPromises();
      await confirmDialog();

      expect(axios.post.mock.calls).toEqual([
        ['experiments/exp1/start', { dryRun: true }],
      ]);
      expect(page.emitted('running')).toBeUndefined();
      idle(page);
      expect(lastNotice().type).toBe('is-info');
      expect(noticeText()).toContain(`minimega script: ${script}`);
      expect(loaderSpy.load.mock.calls.length).toBe(loads + 1);
    });

    it.each([
      ['answers', { data: {} }, ['begin start', 'done']],
      ['fails', null, ['begin start', 'undo']],
      [
        'answers a dry run, which starts nothing',
        { data: { experiment: { name: 'exp1' }, dryRun: true } },
        ['begin start', 'undo'],
      ],
    ])(
      "shows the header's bar from the click, and settles it when the server %s",
      async (_, answer, want) => {
        if (answer) axios.post.mockResolvedValue(answer);
        else axios.post.mockRejectedValue(httpError(500, { message: 'no' }));
        const calls = [];
        const progressTracker = {
          state: {},
          begin: (operation) => {
            calls.push(`begin ${operation}`);
            return {
              done: () => calls.push('done'),
              undo: () => calls.push('undo'),
            };
          },
        };
        const page = await mountPage(StoppedExperiment, {
          route: '/experiment/exp1',
          data: { experiment: { name: 'exp1', vms: [{ name: 'vm1' }] } },
          provide: { [EXPERIMENT_PROGRESS]: progressTracker },
        });

        await byLabel(page, 'Start experiment').trigger('click');
        expect(calls).toEqual([]);
        await confirmDialog();
        expect(calls).toEqual(want);
      },
    );

    it('shows the running view after a start with warnings, and lists them', async () => {
      axios.post.mockResolvedValue({
        data: {
          experiment: { name: 'exp1', running: true },
          warnings: ['vm1: no such bridge'],
        },
      });
      const page = await stopped([{ name: 'vm1' }]);

      await byLabel(page, 'Start experiment').trigger('click');
      await confirmDialog();

      expect(page.emitted('running')).toEqual([[true]]);
      expect(lastNotice().type).toBe('is-warning');
      expect(noticeText()).toContain('vm1: no such bridge');
    });
  });

  it.each([
    [
      'its draft',
      { builder: { draft: 'd1' }, topology: 't' },
      undefined,
      '/builder?draft=d1',
    ],
    [
      'its topology',
      { builder: { document: 'x' }, topology: 't' },
      undefined,
      '/builder?topology=t',
    ],
    [
      'nothing for a role that may not use it',
      { builder: { draft: 'd1' } },
      denyOnly(['builder', 'use']),
      null,
    ],
    [
      'nothing for an experiment it did not make',
      { topology: 't' },
      undefined,
      null,
    ],
  ])('opens in the Builder %s', async (_, fields, role, href) => {
    if (role) rbac.allowed = role;
    const page = await stopped([]);
    await page.setData({ experiment: { name: 'exp1', vms: [], ...fields } });
    const link = page.find('.exp-summary-builder');
    expect(link.exists()).toBe(href !== null);
    if (href) {
      expect(link.attributes('href')).toBe(href);
      expect(textOf(link)).toBe('Open in Builder');
    }
  });

  it('sets boot for the selected VMs, spinning meanwhile, then shows the answer and clears the selection', async () => {
    const answer = Promise.withResolvers();
    axios.patch.mockReturnValue(answer.promise);
    const page = await stopped([
      { name: 'vm1', dnb: false },
      { name: 'vm2', dnb: false },
    ]);
    const boxes = () => page.findAll('td input[type="checkbox"]');
    await boxes().at(1).setValue(true);
    expect(textOf(page.find('.stopped-selection-count'))).toBe('1 selected');

    await byLabel(page, 'Set selected VMs to not boot').trigger('click');
    expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms', {
      vms: [{ name: 'vm2', dnb: true }],
      total: 1,
    });
    // the button spins, the others wait, and the count says what it does
    const dnb = byLabel(page, 'Set selected VMs to not boot');
    expect(dnb.classes('is-loading')).toBe(true);
    expect(
      byLabel(page, 'Set selected VMs to boot').attributes('disabled'),
    ).toBeDefined();
    expect(textOf(page.find('.stopped-selection-count'))).toBe(
      'Setting to not boot 1 VM…',
    );
    // a second press sends nothing more
    await dnb.trigger('click');
    expect(axios.patch).toHaveBeenCalledTimes(1);

    answer.resolve({ data: { vms: [{ name: 'vm2', dnb: true }] } });
    await flushPromises();
    // rows picked one by one are cleared too, not only a whole-page pick
    expect(boxes().map((box) => box.element.checked)).toEqual([false, false]);
    expect(byLabel(page, 'Set selected VMs to not boot').exists()).toBe(false);
    expect(
      tableRows(page).map((cells) =>
        cells.Boot.find('[data-icon="bolt"]').classes(),
      ),
    ).toEqual([['boot'], ['dnb']]);
    expect(toasts().map((t) => t.text)).toEqual(['Set do not boot on 1 VM']);
  });

  it.each([
    ['Set selected VMs to boot', { dnb: false }, 'Set boot on 2 VMs'],
    [
      'Make the disks of selected VMs persistent',
      { snapshot: false },
      'Set persistent disks on 2 VMs',
    ],
    [
      'Make the disks of selected VMs non-persistent',
      { snapshot: true },
      'Set non-persistent disks on 2 VMs',
    ],
  ])('%s, for every selected VM', async (label, update, toast) => {
    axios.patch.mockImplementation(async (_, { vms }) => ({ data: { vms } }));
    const page = await stopped([{ name: 'vm1' }, { name: 'vm2' }]);
    await page.find('th input[type="checkbox"]').setValue(true);

    await byLabel(page, label).trigger('click');
    await flushPromises();
    expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms', {
      vms: [
        { name: 'vm1', ...update },
        { name: 'vm2', ...update },
      ],
      total: 2,
    });
    expect(toasts().map((t) => t.text)).toEqual([toast]);
  });

  it('offers no selection actions to a role that may not change every selected VM', async () => {
    rbac.allowed = denyOnly(['vms', 'patch', 'exp1/vm2']);
    const page = await stopped([{ name: 'vm1' }, { name: 'vm2' }]);
    await page.find('th input[type="checkbox"]').setValue(true);
    expect(textOf(page.find('.stopped-selection-count'))).toBe('2 selected');
    expect(byLabel(page, 'Set selected VMs to boot').exists()).toBe(false);
  });

  it('sums up a selection action the server refused for every VM, offering to retry', async () => {
    axios.patch.mockRejectedValueOnce(
      Object.assign(new Error('Network Error'), {
        isAxiosError: true,
        request: {},
      }),
    );
    const page = await stopped([{ name: 'vm1' }, { name: 'vm2' }]);
    await page.find('th input[type="checkbox"]').setValue(true);

    await byLabel(page, 'Set selected VMs to boot').trigger('click');
    await flushPromises();
    expect(noticeText()).toContain('Set boot on 0 of 2 VMs: 2 failed');
    expect(noticeText()).toContain('Retry failed (2)');

    axios.patch.mockResolvedValue({ data: { vms: [{ name: 'vm1' }] } });
    lastNotice().message.children.at(-1).props.onClick();
    await flushPromises();
    expect(axios.patch.mock.lastCall).toEqual([
      'experiments/exp1/vms',
      {
        vms: [
          { name: 'vm1', dnb: false },
          { name: 'vm2', dnb: false },
        ],
        total: 2,
      },
    ]);
  });

  it('sums up the VMs the server left unchanged in one notice', async () => {
    axios.patch.mockResolvedValue({
      data: {
        vms: [{ name: 'vm1', dnb: true }],
        errors: [
          {
            name: 'vm2',
            status: 'skipped',
            error: 'you do not have permission',
          },
          { name: 'vm3', status: 'failed', error: 'minimega says no' },
        ],
      },
    });
    const page = await stopped([
      { name: 'vm1', dnb: false },
      { name: 'vm2', dnb: false },
      { name: 'vm3', dnb: false },
    ]);
    for (const box of page.findAll('td input[type="checkbox"]')) {
      await box.setValue(true);
    }

    await byLabel(page, 'Set selected VMs to not boot').trigger('click');
    await flushPromises();

    expect(toasts()).toEqual([]);
    expect(notices.open).toHaveBeenCalledTimes(1);
    expect(noticeText()).toContain(
      'Set do not boot on 1 of 3 VMs: 1 failed, 1 skipped',
    );
    expect(noticeText()).toContain('vm3: minimega says no');
    expect(noticeText()).toContain('vm2: you do not have permission');
    // only the failed VM is worth trying again
    expect(noticeText()).toContain('Retry failed (1)');
  });

  describe('VM settings', () => {
    // a VM whose topology gave it 1 CPU, 1 GiB, no host and a persistent disk
    const DEFAULTS = {
      cpus: 1,
      ram: 1024,
      disk: '/phenix/images/web.qc2',
      inject_partition: 1,
      snapshot: false,
      dnb: false,
      host: '',
    };
    const web = (fields) => ({
      name: 'web',
      ...DEFAULTS,
      defaults: { ...DEFAULTS },
      ...fields,
    });
    const cell = (page, column) => tableRows(page)[0][column];
    const spinning = (page, column) =>
      cell(page, column).find('.vm-setting-spinner').exists();

    it.each([
      ['Host for VM web', 'b', { host: 'b' }],
      ['CPUs for VM web', '2', { cpus: 2 }],
      ['Memory for VM web', '2048', { ram: 2048 }],
      ['Partition for VM web', '3', { inject_partition: 3 }],
    ])(
      'saves the %s picked at once, without asking',
      async (label, value, update) => {
        axios.patch.mockImplementation(never);
        const page = await stopped([web({ host: 'a' })], {
          hosts: ['a', 'b'],
        });

        await byLabel(page, label).setValue(value);
        expect(dialogs()).toEqual([]);
        expect(axios.patch).toHaveBeenCalledWith(
          'experiments/exp1/vms/web',
          update,
        );
      },
    );

    it('unassigns the host at once', async () => {
      axios.patch.mockResolvedValue({ data: web({ host: '' }) });
      const page = await stopped([web({ host: 'a' })], { hosts: ['a'] });

      await byLabel(page, 'Unassign host from VM web').trigger('click');
      await flushPromises();
      expect(dialogs()).toEqual([]);
      expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/web', {
        host: '',
      });
      expect(
        byLabel(page, 'Unassign host from VM web').attributes('disabled'),
      ).toBeDefined();
    });

    it('spins a control while it saves, then shows the saved value', async () => {
      const answer = Promise.withResolvers();
      axios.patch.mockReturnValue(answer.promise);
      const page = await stopped([web()]);
      const cpus = () => byLabel(page, 'CPUs for VM web');

      await cpus().setValue('3');
      expect(spinning(page, 'CPUs')).toBe(true);
      // the control stays enabled, so it keeps the keyboard focus
      const fieldset = cell(page, 'CPUs').find('fieldset');
      expect(fieldset.attributes('aria-busy')).toBe('true');
      expect(fieldset.attributes('disabled')).toBeUndefined();
      expect(cpus().element.value).toBe('3');
      // the other settings stay free
      expect(spinning(page, 'Memory')).toBe(false);

      answer.resolve({ data: web({ cpus: 3 }) });
      await flushPromises();
      expect(spinning(page, 'CPUs')).toBe(false);
      expect(cpus().element.value).toBe('3');
    });

    it('tells of a failed save and shows the value it had', async () => {
      const err = httpError(400, 'no');
      axios.patch.mockRejectedValue(err);
      const page = await stopped([web({ cpus: 2 })]);

      await byLabel(page, 'CPUs for VM web').setValue('4');
      await flushPromises();
      expect(notify.error).toHaveBeenCalledWith(err);
      expect(spinning(page, 'CPUs')).toBe(false);
      expect(byLabel(page, 'CPUs for VM web').element.value).toBe('2');
    });

    it('offers a CPU or memory value outside its usual choices that the VM has', async () => {
      const page = await stopped([web({ cpus: 24, ram: 6144 })]);
      const options = (label) =>
        byLabel(page, label)
          .findAll('option')
          .map((o) => textOf(o));
      expect(options('CPUs for VM web')).toEqual([
        ...Array.from({ length: 16 }, (_, i) => String(i + 1)),
        '24',
      ]);
      expect(options('Memory for VM web')).toContain('6 GiB');
      expect(options('Memory for VM web').slice(-6)).toEqual([
        '24 GiB',
        '32 GiB',
        '48 GiB',
        '64 GiB',
        '96 GiB',
        '128 GiB',
      ]);
    });

    it.each([
      [false, 'true', 'Persistent', { snapshot: true }],
      [true, 'false', 'Non-persistent', { snapshot: false }],
    ])(
      'shows persistence as a switch (snapshot %s), which flips it',
      async (snapshot, checked, text, update) => {
        axios.patch.mockImplementation(never);
        const page = await stopped([web({ snapshot })]);
        const toggle = byLabel(page, 'Persistent disk for VM web');
        expect(toggle.attributes('role')).toBe('switch');
        expect(toggle.attributes('aria-checked')).toBe(checked);
        expect(textOf(toggle)).toBe(text);

        await toggle.trigger('click');
        expect(axios.patch).toHaveBeenCalledWith(
          'experiments/exp1/vms/web',
          update,
        );
        expect(spinning(page, 'Persistence')).toBe(true);
      },
    );

    it('flips boot as a switch, spinning while it saves', async () => {
      axios.patch.mockImplementation(never);
      const page = await stopped([web({ dnb: true })]);
      const toggle = byLabel(page, 'Boot VM web');
      expect(toggle.attributes('aria-checked')).toBe('false');

      await toggle.trigger('click');
      expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/web', {
        dnb: false,
      });
      expect(spinning(page, 'Boot')).toBe(true);
      // a second press while it saves sends nothing more
      await toggle.trigger('click');
      expect(axios.patch).toHaveBeenCalledTimes(1);
    });

    it('marks each setting changed from the topology value', async () => {
      const page = await stopped([web({ cpus: 2, snapshot: true })]);
      const marks = page
        .findAll('.vm-setting-changed')
        .map((m) => m.attributes('aria-label'));
      expect(marks).toEqual([
        'Changed from the topology value: 1',
        'Changed from the topology value: persistent',
      ]);
    });

    describe('Reset settings', () => {
      it('is left out of a VM that does not know its topology values', async () => {
        const page = await stopped([{ name: 'web', defaults: null }]);
        expect(byLabel(page, 'Reset settings of VM web').exists()).toBe(false);
        expect(tableRows(page)[0].Reset).toBeUndefined();
      });

      it('is disabled, saying why, while the settings have the topology values', async () => {
        const page = await stopped([web()]);
        const [reset] = tooltipButtons(tableRows(page)[0].Reset);
        expect(reset).toMatchObject({
          label: 'Reset settings of VM web',
          disabled: true,
          tooltip: 'Cannot reset settings: they have the topology values',
        });
        await reset.control.trigger('click');
        expect(axios.post).not.toHaveBeenCalled();
      });

      it('sets a changed VM back, spinning meanwhile', async () => {
        const answer = Promise.withResolvers();
        axios.post.mockReturnValue(answer.promise);
        const page = await stopped([web({ cpus: 4 })]);
        const reset = () => byLabel(page, 'Reset settings of VM web');

        await reset().trigger('click');
        expect(axios.post).toHaveBeenCalledWith(
          'experiments/exp1/vms/resetSettings',
          { vms: ['web'] },
        );
        expect(reset().classes('is-loading')).toBe(true);

        answer.resolve({ data: { vms: [web()] } });
        await flushPromises();
        expect(reset().classes('is-loading')).toBe(false);
        expect(byLabel(page, 'CPUs for VM web').element.value).toBe('1');
        expect(toasts().map((t) => t.text)).toEqual([
          'Reset the settings of 1 VM',
        ]);
      });

      it('offers to retry the VMs whose reset failed', async () => {
        axios.post.mockResolvedValueOnce({
          data: {
            vms: [],
            errors: [{ name: 'web', status: 'failed', error: 'locked' }],
          },
        });
        const page = await stopped([web({ cpus: 4 })]);
        await byLabel(page, 'Reset settings of VM web').trigger('click');
        await flushPromises();
        expect(noticeText()).toContain('Reset the settings of 0 of 1 VM');
        expect(noticeText()).toContain('web: locked');
        expect(noticeText()).toContain('Retry failed (1)');

        axios.post.mockResolvedValue({ data: { vms: [web()] } });
        lastNotice().message.children.at(-1).props.onClick();
        await flushPromises();
        expect(axios.post).toHaveBeenCalledTimes(2);
        expect(axios.post.mock.lastCall).toEqual([
          'experiments/exp1/vms/resetSettings',
          { vms: ['web'] },
        ]);
      });

      it('resets the selected VMs from the toolbar, offered only when one knows its topology values', async () => {
        axios.post.mockResolvedValue({ data: { vms: [web(), web()] } });
        const page = await stopped([
          { name: 'old', defaults: null },
          web({ cpus: 3 }),
        ]);
        const boxes = page.findAll('td input[type="checkbox"]');
        await boxes.at(0).setValue(true);
        expect(byLabel(page, 'Reset settings of selected VMs').exists()).toBe(
          false,
        );

        await boxes.at(1).setValue(true);
        await byLabel(page, 'Reset settings of selected VMs').trigger('click');
        await flushPromises();
        expect(axios.post).toHaveBeenCalledWith(
          'experiments/exp1/vms/resetSettings',
          { vms: ['web'] },
        );
      });

      // each checks every VM and resets them from the toolbar
      it.each([
        [
          'skips the selected VMs it cannot reset, offering no retry for them',
          [
            { name: 'old', defaults: null },
            web({ name: 'same' }),
            web({ cpus: 3 }),
          ],
          { vms: [web()] },
          [['web']],
          [
            'old: its topology values are not known',
            'same: it has the topology values',
          ],
        ],
        [
          'sends nothing when no selected VM can reset',
          [web({ name: 'same' })],
          undefined,
          [],
          ['same: it has the topology values'],
        ],
      ])('%s', async (_, vms, data, sent, reasons) => {
        axios.post.mockResolvedValue({ data });
        const page = await stopped(vms);
        for (const box of page.findAll('td input[type="checkbox"]')) {
          await box.setValue(true);
        }
        await byLabel(page, 'Reset settings of selected VMs').trigger('click');
        await flushPromises();
        expect(axios.post.mock.calls).toEqual(
          sent.map((names) => [
            'experiments/exp1/vms/resetSettings',
            { vms: names },
          ]),
        );
        for (const reason of reasons) expect(noticeText()).toContain(reason);
        expect(noticeText()).not.toContain('Retry');
      });
    });
  });

  describe('VM details', () => {
    const web = {
      name: 'web',
      host: 'a',
      cpus: 1,
      ram: 1024,
      disk: '',
      snapshot: true,
      dnb: false,
      networks: ['EXP'],
      ipv4: ['10.0.0.1'],
    };
    const details = {
      ...web,
      annotations: { 'phenix/startup-autotunnel': ['8080', '2222:22'] },
    };

    async function opened(answer = details) {
      axios.get.mockImplementation((url) =>
        url === 'experiments/exp1/vms/web'
          ? Promise.resolve({ data: answer })
          : never(),
      );
      const page = await stopped([web], { hosts: ['a', 'b'] });
      await tableRows(page)[0].Node.find('button').trigger('click');
      await flushPromises();
      return page;
    }
    const card = (page) => page.findComponent({ name: 'VmDetailsCard' });

    it('opens the details card in its stopped form, with the settings to edit', async () => {
      const page = await opened();
      expect(card(page).props()).toMatchObject({
        stopped: true,
        experiment: 'exp1',
        fullName: 'exp1/web',
        hosts: ['a', 'b'],
      });
      expect(card(page).props('vm').annotations).toEqual(details.annotations);
      const body = card(page);
      expect(textOf(body.find('.vm-state-tag'))).toBe('Stopped');
      expect(body.find('.vm-settings').exists()).toBe(true);
      expect(
        body.findAll('.vm-startup-forwards .vm-list-text').map(textOf),
      ).toEqual(['8080 → 127.0.0.1:8080', '2222 → 127.0.0.1:22']);
    });

    it('saves a setting changed on the card, spinning it in the card', async () => {
      axios.patch.mockImplementation(never);
      const page = await opened();
      const memory = card(page)
        .findAll('select')
        .find((s) => s.attributes('aria-label') === 'Memory for VM web');

      await memory.setValue('4096');
      expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/web', {
        ram: 4096,
      });
      expect(card(page).props('settingsPending')).toEqual({ ram: 4096 });
      expect(
        card(page).find('.vm-setting-ram .vm-setting-spinner').exists(),
      ).toBe(true);
    });

    it("saves the card's annotations, keeping them on the card", async () => {
      const annotations = { 'phenix/startup-autotunnel': ['9090'] };
      axios.patch.mockResolvedValue({ data: { ...web, annotations } });
      const page = await opened();
      const done = vi.fn();

      card(page).vm.$emit('save-annotations', annotations, done);
      await flushPromises();
      expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/web', {
        annotations,
      });
      expect(done).toHaveBeenCalledWith(true);
      expect(card(page).props('vm').annotations).toEqual(annotations);
    });

    it('keeps the card open on a failed save of its annotations', async () => {
      axios.patch.mockRejectedValue(new Error('no'));
      const page = await opened();
      const done = vi.fn();
      card(page).vm.$emit('save-annotations', {}, done);
      await flushPromises();
      expect(done).toHaveBeenCalledWith(false);
      expect(page.vm.expModal.active).toBe(true);
    });

    it('keeps the row open and tells why when the details do not load', async () => {
      const err = new Error('gone');
      axios.get.mockImplementation((url) =>
        url === 'experiments/exp1/vms/web' ? Promise.reject(err) : never(),
      );
      const page = await stopped([web]);
      await tableRows(page)[0].Node.find('button').trigger('click');
      await flushPromises();
      expect(notify.error).toHaveBeenCalledWith(err);
      expect(card(page).props('vm')).toEqual(web);
    });

    it('opens the row as it is to a role that may not get the VM', async () => {
      rbac.allowed = denyOnly(['vms', 'get']);
      const page = await stopped([web]);
      page.vm.showVmDetails(web);
      await flushPromises();
      expect(axios.get).not.toHaveBeenCalledWith('experiments/exp1/vms/web');
      expect(card(page).props('vm').name).toBe('web');
    });

    it('puts a VM update in the open card', async () => {
      const page = await opened();
      await publish(
        wsMsg('experiment/vm', 'exp1/web', 'update', { ...web, cpus: 4 }),
      );
      expect(card(page).props('vm')).toMatchObject({
        cpus: 4,
        annotations: details.annotations,
      });
    });
  });

  describe('its header', () => {
    it('sums the experiment up as stopped, with the hosts its schedule names', async () => {
      axios.get.mockImplementation(async (url) =>
        url === 'experiments/exp1/schedule'
          ? {
              data: {
                schedule: [
                  { vm: 'a', host: 'node2' },
                  { vm: 'b', host: 'node1' },
                  { vm: 'c', host: '' },
                ],
              },
            }
          : never(),
      );
      const page = await stopped([], {
        experiment: {
          name: 'exp1',
          vms: [],
          vm_count: 3,
          vlans: [{ alias: 'EXP', vlan: 0 }],
          apps: ['ntp', 'soh'],
          start_time: '2026-01-01T00:00:00Z',
        },
      });
      const summary = page.findComponent({ name: 'ExperimentSummary' });
      expect(textOf(summary.find('.exp-summary-status'))).toBe('Stopped');
      expect(textOf(summary.find('.exp-fact-vms dd'))).toBe('3');
      expect(
        summary.findAll('.exp-fact-hosts > *').map((el) => textOf(el)),
      ).toEqual(['Scheduled hosts', '2', 'node1, node2']);
      expect(summary.find('.exp-fact-started').exists()).toBe(false);
      expect(
        summary.find('[aria-label^="View state of health"]').exists(),
      ).toBe(true);

      const apps = page.findComponent({ name: 'ExperimentAppsBox' });
      expect(apps.find('.apps-box-trigger').exists()).toBe(false);
      expect(apps.findAll('button.app-tile-body')).toHaveLength(0);
      expect(apps.findAll('.app-tile-state').map(textOf)).toEqual([
        'Built-in',
        'User app',
      ]);
    });

    it('opens expanded when the running page was last left expanded', async () => {
      localStorage.setItem('phenix.experiment.headerExpanded', 'true');
      const page = await stopped([]);
      const toggle = page.find('.experiment-header-toggle');
      expect(toggle.attributes('aria-expanded')).toBe('true');
      expect(page.find('#exp-summary-facts').element.style.display).toBe('');
      await toggle.trigger('click');
      expect(localStorage.getItem('phenix.experiment.headerExpanded')).toBe(
        'false',
      );
      expect(page.find('#exp-summary-facts').element.style.display).toBe(
        'none',
      );
    });

    it('leaves out the hosts tile for a role that may not get the schedule', async () => {
      rbac.allowed = denyOnly(['experiments/schedule', 'get']);
      const page = await stopped([]);
      expect(axios.get).not.toHaveBeenCalledWith('experiments/exp1/schedule');
      expect(page.find('.exp-fact-hosts').exists()).toBe(false);
    });
  });

  describe('its Schedule menu', () => {
    const answers = (schedulers) => async (url) => {
      if (url === 'schedulers') return schedulers();
      if (url === 'experiments/exp1/schedule') {
        return { data: { schedule: [{ vm: 'vm1', host: 'n1' }] } };
      }
      return never();
    };
    // each scheduler offered, and whether it is built in
    const items = (page) =>
      page
        .findAll('.schedule-menu-item')
        .map((item) => [textOf(item.find('span')), item.find('.tag').exists()]);

    it("lists the server's schedulers and how many VMs have a host", async () => {
      axios.get.mockImplementation(
        answers(async () => ({
          data: {
            schedulers: [
              { name: 'round-robin', builtin: true },
              { name: 'mine', builtin: false },
            ],
          },
        })),
      );
      const page = await stopped([{ name: 'vm1' }, { name: 'vm2' }], {
        experiment: { name: 'exp1', vms: [], vm_count: 2 },
      });
      expect(items(page)).toEqual([
        ['round-robin', true],
        ['mine', false],
      ]);
      expect(textOf(page.find('.schedule-menu-note'))).toBe(
        '1 of 2 VMs has a host',
      );
    });

    // Main bug: the page offered scheduler names that do not exist.
    it('falls back to the built-in schedulers when the list fails', async () => {
      axios.get.mockImplementation(
        answers(() => Promise.reject(new Error('gone'))),
      );
      const page = await stopped([]);
      expect(items(page)).toEqual([
        ['isolate-experiment', true],
        ['round-robin', true],
        ['subnet-compute', true],
      ]);
    });

    // Main bug: the page sent round_robin, not round-robin.
    it('asks before it schedules, then spins until the server answers', async () => {
      const answer = Promise.withResolvers();
      axios.post.mockReturnValue(answer.promise);
      const page = await stopped([
        { name: 'vm1', host: '' },
        { name: 'vm2', host: '' },
      ]);

      await page.findAll('.schedule-menu-item').at(1).trigger('click');
      expect(axios.post).not.toHaveBeenCalled();
      expect(textOf(dialogs().at(-1))).toContain(
        'This will assign every VM of the exp1 experiment to a host with the round-robin scheduler.',
      );
      await confirmDialog();
      expect(axios.post).toHaveBeenCalledWith('experiments/exp1/schedule', {
        algorithm: 'round-robin',
      });
      expect(byLabel(page, 'Schedule VMs').classes('is-loading')).toBe(true);

      answer.resolve({
        data: {
          schedule: [
            { vm: 'vm1', host: 'n1' },
            { vm: 'vm2', host: 'n2' },
          ],
        },
      });
      await flushPromises();
      expect(byLabel(page, 'Schedule VMs').classes('is-loading')).toBe(false);
      expect(
        tableRows(page).map((r) => r.Host.find('select').element.value),
      ).toEqual(['n1', 'n2']);
      expect(textOf(page.find('.schedule-menu-note'))).toContain('2 of');
    });

    it('counts a host picked or unassigned in the table', async () => {
      axios.get.mockImplementation(async (url) =>
        url === 'experiments/exp1/schedule'
          ? { data: { schedule: [{ vm: 'vm2', host: 'n1' }] } }
          : never(),
      );
      axios.patch.mockImplementation(async (url, update) => ({
        data: { name: url.split('/').at(-1), ...update },
      }));
      const page = await stopped(
        [
          { name: 'vm1', host: '' },
          { name: 'vm2', host: 'n1' },
        ],
        {
          experiment: {
            name: 'exp1',
            vm_count: 2,
            vms: [
              { name: 'vm1', host: '' },
              { name: 'vm2', host: 'n1' },
            ],
          },
          hosts: ['n1', 'n2'],
        },
      );
      const note = () => textOf(page.find('.schedule-menu-note'));
      expect(note()).toBe('1 of 2 VMs has a host');

      await byLabel(page, 'Host for VM vm1').setValue('n2');
      await flushPromises();
      expect(note()).toBe('2 of 2 VMs have a host');
      expect(textOf(page.find('.exp-fact-host-names'))).toBe('n1, n2');

      await byLabel(page, 'Unassign host from VM vm2').trigger('click');
      await flushPromises();
      expect(note()).toBe('1 of 2 VMs has a host');
    });

    it.each([
      [
        'each warning of a schedule in a notice that stays',
        {
          warnings: [
            'VM vm1 stays on host gone, which is not a schedulable cluster host',
            'host n1: 96 GiB of memory committed, 64 GiB available',
          ],
        },
        [
          {
            type: 'is-warning',
            indefinite: true,
            ariaCloseLabel: 'Dismiss',
            role: 'alert',
            lines: [
              'Scheduled the exp1 experiment with 2 warnings',
              'VM vm1 stays on host gone, which is not a schedulable cluster host',
              'host n1: 96 GiB of memory committed, 64 GiB available',
            ],
          },
        ],
      ],
      ['no notice for no warnings', { warnings: [] }, []],
      ['no notice for an older server', {}, []],
    ])('tells of %s', async (_, extra, want) => {
      axios.post.mockResolvedValue({
        data: { schedule: [{ vm: 'vm1', host: 'gone' }], ...extra },
      });
      const page = await stopped([{ name: 'vm1', host: '' }]);
      await page.findAll('.schedule-menu-item').at(1).trigger('click');
      await confirmDialog();
      await flushPromises();

      const opened = notices.open.mock.calls.map(([notice]) => ({
        type: notice.type,
        indefinite: notice.indefinite,
        ariaCloseLabel: notice.ariaCloseLabel,
        role: notice.message.props.role,
        lines: notice.message.children.map((line) =>
          typeof line.children === 'string'
            ? line.children
            : line.children[0].children,
        ),
      }));
      expect(opened).toEqual(want);
      expect(
        tableRows(page).map((r) => r.Host.find('select').element.value),
      ).toEqual(['gone']);
    });

    it('keeps its success message with the warnings', async () => {
      await stopped([{ name: 'vm1', host: '' }]);
      await publish(
        wsMsg('experiment', 'exp1', 'schedule', {
          schedule: [{ vm: 'vm1', host: 'n1' }],
          warnings: ['host n1: 96 GiB of memory committed, 64 GiB available'],
        }),
      );
      expect(toasts().map((t) => t.text)).toEqual([
        'The VMs for this experiment have been scheduled.',
      ]);
    });

    it('stops spinning and tells of a failed schedule', async () => {
      const err = new Error('no hosts');
      axios.post.mockRejectedValue(err);
      const page = await stopped([]);
      await page.findAll('.schedule-menu-item').at(0).trigger('click');
      await confirmDialog();
      expect(notify.error).toHaveBeenCalledWith(err);
      expect(byLabel(page, 'Schedule VMs').classes('is-loading')).toBe(false);
    });
  });

  describe('its VLAN IDs menu', () => {
    const vlans = [
      { alias: 'EXP', vlan: 101 },
      { alias: 'MGMT', vlan: 0 },
    ];
    async function opened() {
      const page = await stopped([], {
        experiment: { name: 'exp1', vms: [], vlans },
      });
      await page.find('.vlan-menu-trigger').trigger('click');
      await flushPromises();
      return page;
    }
    const save = (page) => page.find('.vlan-menu-save');

    it('lists each alias with its ID, saving only those changed', async () => {
      const answer = Promise.withResolvers();
      axios.patch.mockReturnValue(answer.promise);
      const page = await opened();
      expect(
        page
          .findAll('.vlan-menu-table tr')
          .map((tr) => [textOf(tr.find('th')), tr.find('input').element.value]),
      ).toEqual([
        ['EXP', '101'],
        ['MGMT', '0'],
      ]);
      expect(save(page).attributes('disabled')).toBeDefined();

      await byLabel(page, 'VLAN ID for alias MGMT').setValue('200');
      await page.find('.vlan-menu form').trigger('submit');
      expect(axios.patch).toHaveBeenCalledWith(
        'experiments/exp1',
        '{"MGMT":200}',
      );
      expect(save(page).classes('is-loading')).toBe(true);

      answer.resolve({ status: 204 });
      await flushPromises();
      expect(page.vm.experiment.vlans).toEqual([
        { alias: 'EXP', vlan: 101 },
        { alias: 'MGMT', vlan: 200 },
      ]);
      expect(toasts().map((t) => t.text)).toEqual([
        'Saved the VLAN IDs of 1 alias',
      ]);
    });

    it.each(['4095', '-1', '1.5', '', 'x'])(
      'refuses the ID %j, saying why',
      async (value) => {
        const page = await opened();
        const input = byLabel(page, 'VLAN ID for alias EXP');
        await input.setValue(value);
        expect(input.attributes('aria-invalid')).toBe('true');
        expect(textOf(page.find('.vlan-menu-error'))).toBe(
          'Enter a whole number from 0 to 4094',
        );
        expect(save(page).attributes('disabled')).toBeDefined();
        await page.find('.vlan-menu form').trigger('submit');
        expect(axios.patch).not.toHaveBeenCalled();
      },
    );

    it('keeps the changes on a failed save', async () => {
      const err = new Error('no');
      axios.patch.mockRejectedValue(err);
      const page = await opened();
      await byLabel(page, 'VLAN ID for alias EXP').setValue('5');
      await page.find('.vlan-menu form').trigger('submit');
      await flushPromises();
      expect(notify.error).toHaveBeenCalledWith(err);
      expect(byLabel(page, 'VLAN ID for alias EXP').element.value).toBe('5');
      expect(page.vm.experiment.vlans).toEqual(vlans);
    });

    it('drops the changes not saved as it closes', async () => {
      const page = await opened();
      await byLabel(page, 'VLAN ID for alias EXP').setValue('7');
      page
        .findComponent({ name: 'VlanAliasMenu' })
        .findComponent({ name: 'BDropdown' })
        .vm.$emit('active-change', false);
      await flushPromises();
      expect(byLabel(page, 'VLAN ID for alias EXP').element.value).toBe('101');
    });

    it('says when the experiment has no aliases', async () => {
      const page = await stopped([], {
        experiment: { name: 'exp1', vms: [], vlans: [] },
      });
      await page.find('.vlan-menu-trigger').trigger('click');
      expect(textOf(page.find('.vlan-menu-note'))).toBe(
        'This experiment has no VLAN aliases',
      );
    });
  });

  describe('its columns', () => {
    beforeEach(() => {
      resetMocks({
        store: { token: 't', features: [], role: null, username: 'alice' },
      });
      axios.get.mockImplementation(never);
    });
    // the VM table's column names, after its select-all box
    const headers = (page) =>
      page
        .findComponent({ name: 'StoppedVmTable' })
        .findAll('thead th')
        .map(textOf)
        .slice(1);

    it('hides a column the user unchecks, remembering it for them', async () => {
      const page = await stopped([{ name: 'vm1' }]);
      expect(headers(page)).toEqual([
        'Node',
        'Host',
        'IP',
        'CPUs',
        'Memory',
        'Disk',
        'Partition',
        'Labels',
        'Boot',
        'Persistence',
      ]);
      const ip = page
        .findAll('.dropdown-item .checkbox')
        .find((c) => textOf(c) === 'IP');
      await ip.find('input').setValue(false);
      expect(headers(page)).not.toContain('IP');
      expect(localStorage.getItem('alice.showStoppedIPv4Column')).toBe('false');
    });

    it("opens with the columns the user hid, not another user's", async () => {
      localStorage.setItem('alice.showStoppedDiskColumn', 'false');
      localStorage.setItem('bob.showStoppedHostColumn', 'false');
      const page = await stopped([{ name: 'vm1' }]);
      expect(headers(page)).not.toContain('Disk');
      expect(headers(page)).toContain('Host');
    });
  });

  describe('its filters', () => {
    it('asks the server for the VMs with the labels and on the hosts checked', async () => {
      axios.get.mockImplementation(never);
      const page = await stopped(
        [
          { name: 'vm1', host: 'n1', tags: { role: 'web' } },
          { name: 'vm2', host: 'n2' },
        ],
        { labelFilter: [{ key: 'role', value: 'web' }], hostFilter: ['n1'] },
      );
      loaderSpy.options.fetch('signal');
      expect(axios.get.mock.lastCall).toEqual([
        'experiments/exp1',
        {
          params: expect.objectContaining({
            label: ['role=web'],
            host: ['n1'],
          }),
          signal: 'signal',
          paramsSerializer: { indexes: null },
        },
      ]);
      // a filtered view is not the cached one
      expect(loaderSpy.options.key()).toBeNull();
      expect(page.find('.vm-host-filter').exists()).toBe(true);
      expect(page.find('.vm-label-filter').exists()).toBe(true);
    });

    it.each([
      [
        { hostFilter: ['n1'] },
        'No VMs are assigned to any of the checked hosts',
      ],
      [
        { labelFilter: [{ key: 'a', value: 'b' }] },
        'No VMs have any of the checked labels',
      ],
      [
        { hostFilter: ['n1'], labelFilter: [{ key: 'a', value: 'b' }] },
        'No VMs match the checked labels and hosts',
      ],
      [
        { hostFilter: ['n1'], searchName: 'web' },
        'No VMs match your search and the checked hosts',
      ],
    ])('says why nothing shows for %j', async (data, text) => {
      const page = await stopped([], data);
      expect(textOf(page.find('tbody'))).toBe(text);
    });

    it('lists from the first page on a new filter', async () => {
      const page = await stopped([{ name: 'vm1', host: 'n1' }]);
      page.vm.table.currentPage = 3;
      page.vm.filterByHosts(['n1']);
      page.vm.filterByLabels([{ key: 'k', value: 'v' }]);
      expect(page.vm.table.currentPage).toBe(1);
      expect(loaderSpy.load).toHaveBeenCalledTimes(2);
    });
  });

  it('opens the labels modal on the VM, and loads the VM afresh once saved', async () => {
    axios.patch.mockResolvedValue({ status: 204 });
    axios.get.mockImplementation((url) =>
      url === 'experiments/exp1/vms/vm1'
        ? Promise.resolve({ data: { name: 'vm1', tags: { k: 'v' } } })
        : never(),
    );
    const page = await stopped([{ name: 'vm1', tags: {} }]);
    await byLabel(page, 'Labels of vm1: 0').trigger('click');
    await flushPromises();
    const modal = document.querySelector('.modal-card');
    expect(modal.textContent).toContain('vm1 Labels');
    const [key, value] = modal
      .querySelectorAll('table')[1]
      .querySelectorAll('td input');
    key.value = 'k';
    key.dispatchEvent(new Event('input'));
    value.value = 'v';
    value.dispatchEvent(new Event('input'));
    [...modal.querySelectorAll('button')]
      .find((b) => b.textContent.trim() === 'Save')
      .click();
    await flushPromises();
    expect(axios.get).toHaveBeenCalledWith('experiments/exp1/vms/vm1');
    expect(page.vm.experiment.vms[0].tags).toEqual({ k: 'v' });
  });

  describe('beyond the rows on screen', () => {
    // the page's GETs: the VM list summary, the schedule, and the rest never
    const answering = ({ list, schedule }) =>
      axios.get.mockImplementation(async (url) => {
        if (url === 'experiments/exp1/vms' && list) return list();
        if (url === 'experiments/exp1/schedule' && schedule) return schedule();
        return never();
      });

    it('offers every label and host of the VM list, not only those of the rows shown', async () => {
      answering({
        list: async () => ({
          data: {
            vms: [],
            total: 3,
            labels: [{ key: 'role', value: 'db' }],
            hosts: ['n9'],
          },
        }),
      });
      const page = await stopped([
        { name: 'vm1', host: 'n1', tags: { role: 'web' } },
      ]);
      expect(axios.get).toHaveBeenCalledWith('experiments/exp1/vms', {
        params: { pageNum: 1, perPage: 1 },
      });
      const options = (kind) =>
        page.findAll(`.vm-${kind}-filter-list label`).map((l) => textOf(l));
      expect(options('label')).toEqual(['role=db', 'role=web']);
      expect(options('host')).toEqual(['n1', 'n9']);
    });

    it('counts every VM in the Schedule menu and the header while a search narrows the table', async () => {
      answering({
        list: async () => ({ data: { vms: [], total: 3 } }),
        schedule: async () => ({
          data: {
            schedule: [
              { vm: 'a', host: 'n1' },
              { vm: 'b', host: 'n1' },
              { vm: 'c', host: 'n2' },
            ],
          },
        }),
      });
      const page = await stopped(undefined, {
        searchName: 'a',
        experiment: { name: 'exp1', vms: [{ name: 'a' }], vm_count: 1 },
      });
      expect(textOf(page.find('.schedule-menu-note'))).toBe(
        '3 of 3 VMs have a host',
      );
      expect(textOf(page.find('.exp-fact-vms dd'))).toBe('3');
    });

    it('says how many VMs have a host when the total is not known', async () => {
      answering({
        schedule: async () => ({
          data: { schedule: [{ vm: 'a', host: 'n1' }] },
        }),
      });
      const page = await stopped(undefined, {
        searchName: 'a',
        experiment: { name: 'exp1', vms: [{ name: 'a' }], vm_count: 1 },
      });
      expect(textOf(page.find('.schedule-menu-note'))).toBe('1 VM has a host');
      expect(textOf(page.find('.exp-fact-vms dd'))).toBe('Unknown');
    });

    it('says the scheduled hosts are unknown when the schedule fails, and asks again on the next update', async () => {
      let schedule = () => Promise.reject(httpError(409, 'locked'));
      answering({ schedule: () => schedule() });
      const page = await stopped([{ name: 'vm1' }]);
      const hosts = () =>
        page.findAll('.exp-fact-hosts > *').map((el) => textOf(el));
      expect(hosts()).toEqual(['Scheduled hosts', 'Unknown']);

      schedule = async () => ({
        data: { schedule: [{ vm: 'vm1', host: 'n1' }] },
      });
      loaderSpy.options.apply({ name: 'exp1', vms: [{ name: 'vm1' }] });
      await flushPromises();
      expect(hosts()).toEqual(['Scheduled hosts', '1', 'n1']);
    });
  });

  describe('saves racing other answers', () => {
    const DEFAULTS = { cpus: 1, ram: 1024 };
    const web = (fields) => ({
      name: 'web',
      ...DEFAULTS,
      defaults: { ...DEFAULTS },
      ...fields,
    });
    const value = (page, label) => byLabel(page, label).element.value;

    it('keeps a newer save when an older one answers last', async () => {
      const answers = [];
      axios.patch.mockImplementation(() => {
        const answer = Promise.withResolvers();
        answers.push(answer);
        return answer.promise;
      });
      const page = await stopped([web()]);
      await byLabel(page, 'CPUs for VM web').setValue('2');
      await byLabel(page, 'Memory for VM web').setValue('2048');

      answers[1].resolve({ data: web({ cpus: 2, ram: 2048 }) });
      await flushPromises();
      // read before the memory change committed
      answers[0].resolve({ data: web({ cpus: 2 }) });
      await flushPromises();
      expect(value(page, 'CPUs for VM web')).toBe('2');
      expect(value(page, 'Memory for VM web')).toBe('2048');
    });

    it('keeps a save that answered after a VM list was sent', async () => {
      const list = Promise.withResolvers();
      axios.get.mockImplementation((url) =>
        url === 'experiments/exp1' ? list.promise : never(),
      );
      axios.patch.mockResolvedValue({ data: web({ cpus: 3 }) });
      const page = await stopped([web()]);

      const read = loaderSpy.options.fetch('signal');
      await byLabel(page, 'CPUs for VM web').setValue('3');
      await flushPromises();
      list.resolve({ data: { name: 'exp1', vms: [web()], vm_count: 1 } });
      loaderSpy.options.apply(await read);
      await flushPromises();
      expect(value(page, 'CPUs for VM web')).toBe('3');

      // a list sent after the save shows what the server has now
      axios.get.mockResolvedValue({
        data: { name: 'exp1', vms: [web({ cpus: 4 })], vm_count: 1 },
      });
      loaderSpy.options.apply(await loaderSpy.options.fetch('signal'));
      await flushPromises();
      expect(value(page, 'CPUs for VM web')).toBe('4');
    });

    it('keeps a save when an older websocket update arrives after its answer', async () => {
      axios.patch.mockResolvedValue({
        data: web({ cpus: 3 }),
        headers: { 'x-phenix-seq': '10' },
      });
      const page = await stopped([web()]);
      await byLabel(page, 'CPUs for VM web').setValue('3');
      await flushPromises();

      await publish(
        wsMsg('experiment/vm', 'exp1/web', 'update', web({ ram: 2048 }), 9),
      );
      expect(value(page, 'CPUs for VM web')).toBe('3');
      expect(value(page, 'Memory for VM web')).toBe('2048');

      await publish(
        wsMsg('experiment/vm', 'exp1/web', 'update', web({ cpus: 4 }), 11),
      );
      expect(value(page, 'CPUs for VM web')).toBe('4');
    });
  });

  describe('VM disks', () => {
    const disks = [
      disk('/phenix/images/win/web.qc2', 'win/web.qc2'),
      disk('/phenix/images/web.qc2', 'web.qc2'),
      disk('/data/vms/out.img'),
    ];
    const vms = [
      { name: 'web', disk: '/phenix/images/win/web.qc2' },
      { name: 'out', disk: '/data/vms/out.img' },
      { name: 'gone', disk: '/phenix/images/gone.qc2' },
    ];

    it("lists the disks this experiment's VMs can use, keeping each one whole", async () => {
      axios.get.mockImplementation(async (url) =>
        url === 'disks' ? { data: { disks } } : never(),
      );
      const page = await stopped(vms);

      expect(axios.get).toHaveBeenCalledWith('disks', {
        params: { expName: 'exp1' },
      });
      expect(page.findComponent(DiskSelect).props('disks')).toEqual(disks);
    });

    it("assigns the picked disk's full path at once, without asking", async () => {
      axios.patch.mockImplementation(never);
      const page = await stopped([{ name: 'web', disk: '' }], { disks });

      await byLabel(page, 'Disk for VM web').setValue(
        '/phenix/images/win/web.qc2',
      );
      expect(dialogs()).toEqual([]);
      expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/web', {
        disk: '/phenix/images/win/web.qc2',
      });
    });

    // The Disk column of the page showing `vms`, given the disk list (unloaded
    // when undefined): each VM's picker options or text and its warnings.
    async function diskColumn(data) {
      const page = await stopped(vms, data);
      return Object.fromEntries(
        // the rows are in the VMs' order
        tableRows(page).map((cells, row) => {
          const select = cells.Disk.find('select');
          return [
            vms[row].name,
            {
              shown: select.exists()
                ? select
                    .findAll('option')
                    .map((o) => textOf(o) + (o.element.disabled ? ' (x)' : ''))
                : textOf(cells.Disk.find('span[title]')),
              warnings: tooltipButtons(cells.Disk).map((b) => b.label),
            },
          ];
        }),
      );
    }

    it('offers each VM the listed disks, warning of one outside the files directory or not listed', async () => {
      const offered = ['web.qc2', 'win/web.qc2', '/data/vms/out.img'];
      expect(await diskColumn({ disks })).toEqual({
        web: { shown: offered, warnings: [] },
        out: {
          shown: offered,
          warnings: ['Outside the standard images directory'],
        },
        gone: {
          shown: ['/phenix/images/gone.qc2 (not listed) (x)', ...offered],
          warnings: ['Not in your disk list'],
        },
      });
    });

    it('shows each VM its disk alone, with no warning, to a role that may not list disks', async () => {
      rbac.allowed = denyOnly(['disks']);
      expect(await diskColumn({})).toEqual({
        web: { shown: ['/phenix/images/win/web.qc2'], warnings: [] },
        out: { shown: ['/data/vms/out.img'], warnings: [] },
        gone: { shown: ['/phenix/images/gone.qc2'], warnings: [] },
      });
    });

    it("names each VM's disk to a role that may not change it", async () => {
      rbac.allowed = denyOnly(['vms']);
      expect(await diskColumn({ disks })).toEqual({
        web: { shown: 'win/web.qc2', warnings: [] },
        out: {
          shown: '/data/vms/out.img',
          warnings: ['Outside the standard images directory'],
        },
        gone: {
          shown: '/phenix/images/gone.qc2',
          warnings: ['Not in your disk list'],
        },
      });
    });
  });

  describe('search history', () => {
    // the page with this history, and its history once a search finds VMs
    async function searching(searchHistory) {
      const page = await stopped(undefined, { searchHistory });
      const search = async (text) => {
        await page.find('.autocomplete input').setValue(text);
        // the test answers the search, so drop the page's debounced load
        page.vm.searchVMs.cancel();
        loaderSpy.options.apply({
          name: 'exp1',
          vms: [{ name: 'vm-new' }],
          vm_count: 1,
          vlans: [],
        });
        await flushPromises();
        return page.vm.searchHistory.toSorted();
      };
      return { page, search };
    }

    // the box lists past searches, not VMs, so it never says "No results found"
    // Main bug: the search box listed all past searches, not the matches.
    it('suggests the past searches that hold the text typed', async () => {
      const page = await stopped(undefined, {
        searchHistory: ['web-1', 'db-1', 'web-2'],
      });
      const box = page.findComponent({ name: 'BAutocomplete' });

      await page.find('.autocomplete input').setValue('WEB');
      expect(box.props('data')).toEqual(['web-1', 'web-2']);
      await page.find('.autocomplete input').setValue('dns');
      expect(box.props('data')).toEqual([]);
      expect(box.text()).not.toContain('No results found');
    });

    it('drops the oldest search from a full history, not the last by name', async () => {
      // oldest first
      const full = ['zzzz', ...Array.from({ length: 9 }, (_, i) => `vm-${i}`)];
      const { search } = await searching(full);
      expect(await search('aaaa')).toEqual(
        ['aaaa', ...full.slice(1)].toSorted(),
      );
    });
  });

  describe('debounced search', () => {
    afterEach(() => vi.useRealTimers());

    it('loads nothing for a search typed just before the page closes', async () => {
      const page = await stopped();
      vi.useFakeTimers();
      await page.find('.autocomplete input').setValue('vm-1');
      page.unmount();
      loaderSpy.load.mockClear();
      await vi.runAllTimersAsync();
      expect(loaderSpy.load).not.toHaveBeenCalled();
    });

    it('searches on each open page, not only the last one typed in', async () => {
      const pages = [await stopped(), await stopped()];
      vi.useFakeTimers();
      loaderSpy.load.mockClear();
      for (const page of pages) {
        await page.find('.autocomplete input').setValue('vm-1');
      }
      await vi.runAllTimersAsync();
      expect(loaderSpy.load).toHaveBeenCalledTimes(2);
    });
  });
});

describe('VMtilesView', () => {
  const screenshot = 'data:image/png;base64,AA==';
  const vms = [
    { experiment: 'exp1', name: 'vm[1]', running: true, screenshot },
    { experiment: 'exp1', name: 'vm2', running: false },
    { experiment: 'exp2', name: 'vm[3]', running: false },
  ];

  // the page's heading and its tiles: the name on each, the text of its
  // picture (the console link names it), and the console it opens, if any
  async function tiles(route, searchName = '') {
    const page = await mountComponent(VMtilesView, {
      route,
      data: { vms, searchName },
    });
    return {
      heading: textOf(page.find('h3')),
      tiles: page.findAll('.tile.is-child').map((tile) => ({
        name: textOf(tile.find('.title')),
        alt: tile.find('a.vnc-tile-link').exists()
          ? textOf(tile.get('a.vnc-tile-link'))
          : tile.get('img').attributes('alt'),
        console: tile.find('a.vnc-tile-link').exists()
          ? tile.get('a.vnc-tile-link').attributes('href')
          : null,
      })),
    };
  }

  it("shows the routed experiment's VMs, found by a literal search", async () => {
    // sorted by name
    expect(await tiles('/experiment/exp1')).toEqual({
      heading: 'Experiment: exp1',
      tiles: [
        { name: 'vm2', alt: 'VM vm2 is not running', console: null },
        {
          name: 'vm[1]',
          alt: 'Open the console of VM vm[1]',
          console: '/vnc?vm=exp1/vm[1]',
        },
      ],
    });
    expect(
      (await tiles('/experiment/exp1', '[')).tiles.map((tile) => tile.name),
    ).toEqual(['vm[1]']);
  });

  it("finds the routed experiment's VMs by any part of their names, in any case, sorted", async () => {
    const name = fc.stringMatching(/^[a-cA-C1[(.éÉ]{1,4}$/);
    const others = fc.uniqueArray(name, { maxLength: 6 });
    const vmsAndSearch = fc
      .tuple(others, others)
      .chain(([mine, theirs]) =>
        fc.tuple(
          fc.constant(mine),
          fc.constant(theirs),
          fc.oneof(
            fc.stringMatching(/^[a-cA-C1[(.éÉ]{0,2}$/),
            ...mine.map((n) => fc.nat(n.length).map((i) => n.slice(i, i + 2))),
          ),
        ),
      );
    await fc.assert(
      fc.asyncProperty(vmsAndSearch, async ([mine, theirs, search]) => {
        const page = await mountComponent(VMtilesView, {
          route: '/experiment/exp1',
          data: {
            vms: [
              ...mine.map((n) => ({ experiment: 'exp1', name: n })),
              ...theirs.map((n) => ({ experiment: 'exp2', name: n })),
            ],
          },
        });
        await page.find('.autocomplete input').setValue(search);
        const lower = (n) => n.toLowerCase();
        const want = mine
          .filter((n) => lower(n).includes(lower(search)))
          .sort((a, b) => (lower(a) > lower(b)) - (lower(a) < lower(b)));
        expect(page.findAll('.tile.is-child .title').map(textOf)).toEqual(want);
        page.unmount();
      }),
      { numRuns: 8 },
    );
  });

  it("finds every experiment's VMs by the exp/vm on their tiles", async () => {
    expect((await tiles('/vmtiles', 'exp1/')).tiles.map((t) => t.name)).toEqual(
      ['exp1/vm2', 'exp1/vm[1]'],
    );
    expect((await tiles('/vmtiles', 'exp1_')).tiles).toEqual([]);
  });

  it("offers as matches the names on every experiment's tiles the search finds", async () => {
    const name = fc.stringMatching(/^[ab_/]{1,3}$/);
    await fc.assert(
      fc.asyncProperty(
        fc.uniqueArray(fc.tuple(fc.constantFrom('e', 'e_a'), name), {
          maxLength: 6,
          selector: ([e, n]) => `${e}/${n}`,
        }),
        fc.stringMatching(/^[eab_/]{0,3}$/),
        async (pairs, search) => {
          const page = await mountComponent(VMtilesView, {
            route: '/vmtiles',
            data: {
              vms: pairs.map(([experiment, name]) => ({ experiment, name })),
            },
          });
          await page.find('.autocomplete input').setValue(search);
          const shown = page.findAll('.tile.is-child .title').map(textOf);
          expect(shown.toSorted()).toEqual(
            pairs
              .map(([e, n]) => `${e}/${n}`)
              .filter((full) => full.includes(search))
              .toSorted(),
          );
          expect(
            page.findComponent({ name: 'BAutocomplete' }).props('data'),
          ).toEqual(shown);
          page.unmount();
        },
      ),
      { numRuns: 8 },
    );
  });

  it("shows every experiment's VMs under their experiment's name", async () => {
    expect(await tiles('/vmtiles', '[')).toEqual({
      heading: 'All Experiments',
      tiles: [
        {
          name: 'exp1/vm[1]',
          alt: 'Open the console of VM exp1/vm[1]',
          console: '/vnc?vm=exp1/vm[1]',
        },
        {
          name: 'exp2/vm[3]',
          alt: 'VM exp2/vm[3] is not running',
          console: null,
        },
      ],
    });
  });

  it('shows the VM of the chosen experiment when another has a VM of that name', async () => {
    const page = await mountComponent(VMtilesView, {
      route: '/experiment/exp1',
      data: {
        vms: [
          { experiment: 'exp1', name: 'web', running: true, screenshot },
          { experiment: 'exp2', name: 'web', running: true, screenshot },
        ],
      },
    });
    const links = () =>
      page.findAll('a.vnc-tile-link').map((a) => a.attributes('href'));
    expect(links()).toEqual(['/vnc?vm=exp1/web']);
    const exp2 = page
      .findAll('.dropdown-item')
      .find((item) => textOf(item) === 'exp2');
    await exp2.trigger('click');
    await flushPromises();
    expect(textOf(page.find('h3'))).toBe('Experiment: exp2');
    expect(links()).toEqual(['/vnc?vm=exp2/web']);
  });

  it('keeps each tile in place while a search hides the tiles before it', async () => {
    const page = await mountComponent(VMtilesView, {
      route: '/experiment/exp1',
      data: {
        vms: ['a', 'b', 'c', 'd'].map((name) => ({
          experiment: 'exp1',
          name,
          running: true,
          screenshot,
        })),
      },
    });
    const tileOf = (name) =>
      page
        .findAll('.tile.is-child')
        .find((tile) => textOf(tile.find('.title')) === name).element;
    const before = tileOf('d');
    await page.find('.autocomplete input').setValue('d');
    await flushPromises();
    expect(page.findAll('.tile.is-child')).toHaveLength(1);
    expect(tileOf('d')).toBe(before);
  });
});

describe('VMLabelsModal', () => {
  // a note, which a save keeps as it is
  const NOTE = { __notes_1: 'n' };

  afterEach(() => vi.restoreAllMocks());

  const modal = (tags) =>
    mountComponent(VMLabelsModal, {
      props: { experiment: 'exp1', vmName: 'vm1', tags: { ...NOTE, ...tags } },
    });

  it('saves to the experiment it was given and closes on 200', async () => {
    axios.patch.mockResolvedValue({ status: 200, statusText: '' });
    const labels = await modal({ old: 'x' });
    const [key, value] = labels.findAll('table').at(1).findAll('td input');
    await key.setValue('k');
    await value.setValue('v');

    await button(labels, 'Save').trigger('click');
    await flushPromises();

    expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/vm1', {
      tag_update_mode: 'SET',
      tags: { k: 'v', ...NOTE },
    });
    expect(labels.emitted('saved')).toEqual([[]]);
    expect(labels.emitted('close')).toEqual([[]]);
  });

  it.each([204, 201])('closes on any success, such as %i', async (status) => {
    axios.patch.mockResolvedValue({ status });
    const labels = await modal({ old: 'x' });
    await labels.findAll('table').at(1).find('td input').setValue('k');
    await button(labels, 'Save').trigger('click');
    await flushPromises();
    expect(labels.emitted('close')).toEqual([[]]);
  });

  // Main bug: a label save showed no progress and could be sent twice.
  it('spins and disables Save while it saves, and stays open with the edits on a failure', async () => {
    const answer = Promise.withResolvers();
    axios.patch.mockReturnValue(answer.promise);
    const labels = await modal({ old: 'x' });
    const key = () => labels.findAll('table').at(1).find('td input');
    await key().setValue('k');
    const save = () => button(labels, 'Save');

    await save().trigger('click');
    expect(save().classes('is-loading')).toBe(true);
    expect(save().attributes('disabled')).toBeDefined();
    // a second press while it saves sends nothing more
    await labels.vm.save();
    expect(axios.patch).toHaveBeenCalledTimes(1);

    const err = new Error('no');
    answer.reject(err);
    await flushPromises();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(labels.emitted('close')).toBeUndefined();
    expect(save().classes('is-loading')).toBe(false);
    expect(save().attributes('disabled')).toBeUndefined();
    expect(key().element.value).toBe('k');
  });

  it('opens a VM with no labels or notes on a blank one of each, and saves neither', async () => {
    const labels = await mountComponent(VMLabelsModal, {
      props: { experiment: 'exp1', vmName: 'vm1', tags: {} },
    });
    expect(labels.findAll('textarea')).toHaveLength(1);
    expect(labels.findAll('table').at(1).findAll('td input')).toHaveLength(2);

    await button(labels, 'Save').trigger('click');
    expect(axios.patch).not.toHaveBeenCalled();
    expect(labels.emitted('close')).toEqual([[]]);
  });

  it('drops a note cleared of its text', async () => {
    axios.patch.mockResolvedValue({ status: 200, statusText: '' });
    const blank = fc
      .array(fc.constantFrom(' ', '\t', '\n'), { maxLength: 4 })
      .map((chars) => chars.join(''));
    await fc.assert(
      fc.asyncProperty(blank, async (text) => {
        axios.patch.mockClear();
        const labels = await modal({ old: 'x' });
        await labels.find('textarea').setValue(text);
        await button(labels, 'Save').trigger('click');
        expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/vm1', {
          tag_update_mode: 'SET',
          tags: { old: 'x' },
        });
        labels.unmount();
      }),
      { numRuns: 8 },
    );
  });

  it('sizes each note to its text as it opens, and as it is typed', async () => {
    // happy-dom lays out nothing: give each line of a note 20px
    vi.spyOn(
      HTMLTextAreaElement.prototype,
      'scrollHeight',
      'get',
    ).mockImplementation(function () {
      return this.value.split('\n').length * 20;
    });
    const labels = await modal({ __notes_2: 'a\nb\nc' });
    await flushPromises();
    const heights = () =>
      labels.findAll('textarea').map((t) => t.element.style.height);
    expect(heights()).toEqual(['20px', '60px']);

    await labels.findAll('textarea').at(0).setValue('a\nb');
    await flushPromises();
    expect(heights()).toEqual(['40px', '60px']);
  });

  it.each([
    ['a label', { k: 'v' }],
    ['a hidden tag', { k: 'v', __sohStyle: 'fill: red' }],
  ])(
    'closes without saving when nothing changed on a VM with %s',
    async (_, tags) => {
      const labels = await modal(tags);

      await button(labels, 'Save').trigger('click');
      expect(axios.patch).not.toHaveBeenCalled();
      expect(labels.emitted('close')).toEqual([[]]);
    },
  );

  it('keeps the hidden tags it does not show, such as a node style', async () => {
    axios.patch.mockResolvedValue({ status: 200, statusText: '' });
    const hiddenKey = fc
      .string({ minLength: 1, maxLength: 8 })
      .map((s) => '__' + s)
      .filter((k) => !k.startsWith('__notes_') && k !== '__proto__');
    await fc.assert(
      fc.asyncProperty(
        fc.dictionary(hiddenKey, fc.string(), { maxKeys: 3 }),
        async (hidden) => {
          axios.patch.mockClear();
          const labels = await modal({ old: 'x', ...hidden });
          await labels.findAll('table').at(1).find('td input').setValue('new');
          await button(labels, 'Save').trigger('click');
          expect(axios.patch.mock.lastCall[1].tags).toEqual({
            new: 'x',
            ...NOTE,
            ...hidden,
          });
          labels.unmount();
        },
      ),
      { numRuns: 8 },
    );
  });
});

describe('VMMountBrowserModal', () => {
  beforeEach(() => {
    axios.post.mockImplementation(never);
    axios.delete.mockResolvedValue({});
  });

  const browser = (currentPath) =>
    mountComponent(VMMountBrowserModal, {
      props: { targetExp: 'exp1', targetVm: 'vm1' },
      data: { currentPath },
    });

  const crumbs = (wrapper) => wrapper.findAll('.breadcrumb li');

  it('links each crumb to its own position in the path', async () => {
    const wrapper = await browser('/a/a/b');
    expect(crumbs(wrapper).map(textOf)).toEqual(['mnt', 'a', 'a', 'b']);
    expect(crumbs(wrapper).at(-1).classes()).toContain('is-active');

    // the path each crumb opens
    for (const [i, path] of [
      [2, '/a/a'],
      [1, '/a'],
      [0, '/'],
    ]) {
      await crumbs(wrapper).at(i).find('a').trigger('click');
      expect(axios.get.mock.lastCall).toEqual([
        'experiments/exp1/vms/vm1/files',
        { params: { path } },
      ]);
      expect(crumbs(wrapper)).toHaveLength(i + 1);
    }
  });

  it('names each part of any path in a crumb that opens the path up to it', async () => {
    const segment = pathSegment.filter((s) => !/\s/.test(s));
    await fc.assert(
      fc.asyncProperty(
        fc.array(segment, { minLength: 1, maxLength: 4 }),
        fc.nat(),
        async (parts, n) => {
          const wrapper = await browser('/' + parts.join('/'));
          expect(crumbs(wrapper).map(textOf)).toEqual(['mnt', ...parts]);
          const i = n % parts.length;
          await crumbs(wrapper).at(i).find('a').trigger('click');
          expect(axios.get.mock.lastCall[1].params.path).toBe(
            '/' + parts.slice(0, i).join('/'),
          );
          wrapper.unmount();
        },
      ),
      { numRuns: 8 },
    );
  });

  it('shows only the mount at its root', async () => {
    expect(crumbs(await browser('/')).map(textOf)).toEqual(['mnt']);
  });
});
