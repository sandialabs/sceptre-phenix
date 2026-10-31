// The running experiment page's dialogs on their own: what each shows, and
// what each asks the page to change or do.
import { flushPromises, RouterLinkStub } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);

import OpticalDiscModal from '@/components/experiment/OpticalDiscModal.vue';
import PortForwardModal from '@/components/experiment/PortForwardModal.vue';
import RedeployModal from '@/components/experiment/RedeployModal.vue';
import TriggerAppsModal from '@/components/experiment/TriggerAppsModal.vue';
import VlanModal from '@/components/experiment/VlanModal.vue';
import VmFilenamesModal from '@/components/experiment/VmFilenamesModal.vue';
import { disk } from './helpers/fixtures.js';
import { denyOnly } from './helpers/permissions.js';
import { byLabel, mountComponent, textOf } from './helpers/mount.js';

beforeEach(() => resetMocks());

// the dialog, open, with these props
const open = (component, props) =>
  mountComponent(component, {
    props: { active: true, ...props },
    stubs: { RouterLink: RouterLinkStub },
  });

const title = (wrapper) => textOf(wrapper.find('.modal-card-title'));
const footerButton = (wrapper, text) =>
  wrapper.findAll('footer button').find((b) => textOf(b) === text);

// whether the dialog asked the page to close it
const closed = (wrapper) =>
  wrapper.emitted('update:active')?.some(([on]) => on === false) ?? false;

describe('the port forward dialog', () => {
  const props = { srcPort: '8080', dstHost: '127.0.0.1', dstPort: '80' };

  it('shows its fields and passes on what is typed in each', async () => {
    const wrapper = await open(PortForwardModal, props);
    expect(title(wrapper)).toBe('Create New Port Forward');
    expect(wrapper.find('#port-forward-src-port').element.value).toBe('8080');
    for (const [label, event, value] of [
      ['Source Port', 'update:srcPort', '2222'],
      ['Destination Host', 'update:dstHost', '10.0.0.1'],
      ['Destination Port', 'update:dstPort', '22'],
    ]) {
      const id = wrapper
        .findAll('label')
        .find((l) => textOf(l) === label)
        .attributes('for');
      await wrapper.find(`#${id}`).setValue(value);
      expect(wrapper.emitted(event)).toEqual([[value]]);
    }
  });

  it('asks the page to create it', async () => {
    const wrapper = await open(PortForwardModal, props);
    await footerButton(wrapper, 'Create').trigger('click');
    expect(wrapper.emitted('create')).toEqual([[]]);
  });
});

describe('the VLAN dialog', () => {
  const props = {
    vmName: 'web',
    netIndex: 1,
    fromNet: 'DMZ (100)',
    vlans: [{ alias: 'MGMT', vlan: 200 }],
    vlan: null,
  };

  it('says which interface it moves, offering each VLAN and disconnecting', async () => {
    const wrapper = await open(VlanModal, props);
    expect(textOf(wrapper.find('.modal-card-body'))).toContain(
      'Move interface 1 from dmz (100) to a new one for the web VM.',
    );
    const select = byLabel(wrapper, 'New VLAN for interface 1 of VM web');
    expect(select.findAll('option').map(textOf)).toEqual([
      'disconnect',
      'mgmt (200)',
    ]);

    await select.findAll('option').at(1).setSelected();
    expect(wrapper.emitted('update:vlan')).toEqual([[props.vlans[0]]]);
    await footerButton(wrapper, 'Change').trigger('click');
    expect(wrapper.emitted('change')).toEqual([[]]);
  });
});

describe('the redeploy dialog', () => {
  const vm = (name) => ({
    name,
    cpus: 2,
    ram: 1024,
    disk: `/images/${name}.qc2`,
    inject: false,
  });
  const disks = [disk('/images/web.qc2', 'web.qc2')];

  it('offers each VM its settings, passing on each one picked', async () => {
    const wrapper = await open(RedeployModal, {
      vms: [vm('web'), vm('db')],
      disks,
    });
    expect(title(wrapper)).toBe('Redeploy the VMs');
    expect(byLabel(wrapper, 'CPUs for VM db').element.value).toBe('2');
    expect(
      byLabel(wrapper, 'Memory for VM web')
        .findAll('option')
        .map((o) => [o.element.value, textOf(o)])
        .at(-1),
    ).toEqual(['16384', '16 GB']);

    await byLabel(wrapper, 'CPUs for VM db').setValue('4');
    await byLabel(wrapper, 'Memory for VM web').setValue('2048');
    await byLabel(wrapper, 'Replicate original injections for VM db').setValue(
      'true',
    );
    expect(wrapper.emitted('edit')).toEqual([
      [1, 'cpus', '4'],
      [0, 'ram', '2048'],
      [1, 'inject', 'true'],
    ]);
  });

  it('asks the page to redeploy, or closes on cancel', async () => {
    const wrapper = await open(RedeployModal, { vms: [vm('web')], disks });
    expect(title(wrapper)).toBe('Redeploy the VM');
    await footerButton(wrapper, 'Redeploy').trigger('click');
    expect(wrapper.emitted('redeploy')).toEqual([[]]);

    await footerButton(wrapper, 'Cancel').trigger('click');
    expect(closed(wrapper)).toBe(true);
    expect(wrapper.emitted('close')).toHaveLength(1);
  });
});

describe('the filename dialog', () => {
  const props = {
    title: 'Create a Disk Image',
    kind: 'Disk image',
    prompt: 'Create disk image of the',
    vms: [
      { name: 'web', filename: 'web_1' },
      {
        name: 'db',
        filename: 'db 1',
        nameErrType: 'is-danger',
        nameErrMsg: 'bad',
      },
    ],
  };

  it("asks each VM's filename, showing the page's verdict on it", async () => {
    const wrapper = await open(VmFilenamesModal, { ...props, valid: false });
    expect(textOf(wrapper.find('.modal-card-body'))).toMatch(
      /^Create disk image of the web VM with filename: .*Create disk image of the db VM with filename:/,
    );
    expect(
      byLabel(wrapper, 'Disk image filename for VM db').element.value,
    ).toBe('db 1');
    expect(textOf(wrapper.find('.help.is-danger'))).toBe('bad');
    expect(footerButton(wrapper, 'Create').attributes('disabled')).toBe('');

    await byLabel(wrapper, 'Disk image filename for VM db').setValue('db_1');
    expect(wrapper.emitted('update:filename')).toEqual([[1, 'db_1']]);
  });

  it('asks the page to create the files once their names are valid', async () => {
    const wrapper = await open(VmFilenamesModal, { ...props, valid: true });
    await footerButton(wrapper, 'Create').trigger('click');
    expect(wrapper.emitted('create')).toEqual([[]]);
    await footerButton(wrapper, 'Cancel').trigger('click');
    expect(closed(wrapper)).toBe(true);
  });
});

describe('the apps dialog', () => {
  it('passes on the apps picked, and triggers them', async () => {
    const wrapper = await open(TriggerAppsModal, {
      experiment: 'demo',
      triggerable: ['soh', 'mirror'],
      apps: [],
    });
    const trigger = () => footerButton(wrapper, 'Trigger Apps');
    expect(trigger().attributes('disabled')).toBeDefined();

    await wrapper.findAll('input[type="checkbox"]').at(1).setValue(true);
    expect(wrapper.emitted('update:apps')).toEqual([[['mirror']]]);
    await wrapper.setProps({ apps: ['mirror'] });
    await trigger().trigger('click');
    expect(wrapper.emitted('trigger')).toEqual([[]]);
  });

  it('says when there are none, and offers no trigger to a role that may not', async () => {
    rbac.allowed = denyOnly(['experiments/trigger', 'create']);
    const wrapper = await open(TriggerAppsModal, {
      experiment: 'demo',
      triggerable: [],
    });
    expect(textOf(wrapper.find('.modal-card-body'))).toBe(
      'This experiment does not include any triggerable apps.',
    );
    expect(footerButton(wrapper, 'Trigger Apps')).toBeUndefined();
  });
});

describe('the CD-ROM picker', () => {
  const isos = [
    disk('/images/tools.iso', 'tools.iso', { kind: 'ISO' }),
    disk('/data/isos/win.iso', '', { kind: 'ISO' }),
  ];
  const picker = (props) =>
    open(OpticalDiscModal, { vmName: 'web', isos, ...props });

  it('names the inserted ISO, and offers to change or eject it', async () => {
    const wrapper = await picker({
      current: '/images/tools.iso',
      disc: '/images/tools.iso',
    });
    expect(title(wrapper)).toBe('Change the CD-ROM for web');
    expect(textOf(wrapper.find('.optical-disc-current'))).toBe(
      'Inserted: tools.iso',
    );
    // nothing to change to yet
    expect(footerButton(wrapper, 'Change').attributes('disabled')).toBe('');

    await wrapper.find('select').setValue('/data/isos/win.iso');
    expect(wrapper.emitted('update:disc')).toEqual([['/data/isos/win.iso']]);
    await footerButton(wrapper, 'Eject').trigger('click');
    expect(wrapper.emitted('eject')).toEqual([[]]);
  });

  it('inserts the ISO picked', async () => {
    const wrapper = await picker({ disc: '/images/tools.iso' });
    expect(title(wrapper)).toBe('Insert a CD-ROM for web');
    expect(footerButton(wrapper, 'Eject')).toBeUndefined();
    await footerButton(wrapper, 'Insert').trigger('click');
    expect(wrapper.emitted('insert')).toEqual([[]]);
  });

  it.each([
    [{ loading: true }, 'Loading ISO images…'],
    [{ error: 'nope' }, 'nope'],
    [{ isos: [] }, /^There are no ISO images to insert\./],
  ])('says why it offers no images: %o', async (props, text) => {
    const wrapper = await picker(props);
    expect(textOf(wrapper.find('.optical-disc-state'))).toMatch(text);
    expect(wrapper.find('select').exists()).toBe(false);
  });

  it('closes on cancel', async () => {
    const wrapper = await picker();
    await footerButton(wrapper, 'Cancel').trigger('click');
    await flushPromises();
    expect(closed(wrapper)).toBe(true);
    expect(wrapper.emitted('close')).toHaveLength(1);
  });
});
