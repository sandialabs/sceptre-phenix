// The inject partition picker of a stopped experiment's VMs: the partitions
// it reads from the disk's details once used, the current value it keeps,
// partitions 1 to 4 while it cannot know them, and one request for each disk.
import { flushPromises } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import PartitionSelect from '@/components/experiment/PartitionSelect.vue';
import VmSettingControl from '@/components/experiment/VmSettingControl.vue';
import { clearPageCache, invalidatePages } from '@/utils/pageCache.js';
import {
  diskDetails,
  httpError,
  partition,
  wsMsg,
} from './helpers/fixtures.js';
import { byLabel, mountComponent, textOf } from './helpers/mount.js';
import { denyOnly } from './helpers/permissions.js';

const KALI = '/phenix/images/kali.qc2';
const JAMMY = '/phenix/images/jammy.qc2';

// jammy.qc2 is a GPT disk with an EFI system partition and an ext4 root
const DETAILS = {
  [KALI]: diskDetails(),
  [JAMMY]: diskDetails({
    fullPath: JAMMY,
    partitionTable: 'gpt',
    partitions: [
      partition(1, 'EFI System', 'C12A7328-F81F-11D2-BA4B-00A0C93EC93B', {
        fs: 'vfat',
      }),
      partition(2, 'Linux filesystem', '0FC63DAF', { fs: 'ext4' }),
    ],
  }),
};

// the disks whose details the server was asked for, in order
const asked = () =>
  axios.get.mock.calls
    .filter(([url]) => url === 'disks/details')
    .map(([, config]) => config.params.disk);

// answers each details request from DETAILS, or 404 for another disk
const answerAll = () =>
  axios.get.mockImplementation(async (_, config) => {
    const details = DETAILS[config.params.disk];
    if (!details) throw httpError(404, { message: 'disk image not found' });
    return { data: details };
  });

// Holds each details request until the test answers it with answer(i).
function holdAll() {
  const held = [];
  axios.get.mockImplementation(
    (_, config) =>
      new Promise((resolve) =>
        held.push(() => resolve({ data: DETAILS[config.params.disk] })),
      ),
  );
  return async (i) => {
    held[i]();
    await flushPromises();
  };
}

const picker = (props) =>
  mountComponent(PartitionSelect, {
    props: { label: 'Partition for VM web', ...props },
  });

const select = (wrapper) => byLabel(wrapper, 'Partition for VM web');
const options = (wrapper) =>
  select(wrapper)
    .findAll('option')
    .map((o) => textOf(o));
// the hidden note the select is described by, or '' when it has none
const note = (wrapper) => {
  const id = select(wrapper).attributes('aria-describedby');
  return id ? textOf(wrapper.find(`[id="${id}"]`)) : '';
};
const tip = (wrapper) =>
  textOf(wrapper.findComponent({ name: 'BTooltip' }).vm.$refs.content);

// the user moves to the picker, which then reads the disk
async function use(wrapper) {
  await select(wrapper).trigger('focusin');
  await flushPromises();
}

beforeEach(() => {
  resetMocks();
  clearPageCache();
});

describe('PartitionSelect', () => {
  it('offers the partitions of the disk, with file system and start', async () => {
    answerAll();
    const wrapper = await picker({ modelValue: 1, disk: KALI });
    await use(wrapper);

    expect(asked()).toEqual([KALI]);
    expect(options(wrapper)).toEqual([
      '1: ext4, start 1.0 MiB',
      '2: extended, start 2.0 MiB',
      '5: Linux swap, start 5.0 MiB',
    ]);
    expect(select(wrapper).element.value).toBe('1');
    expect(note(wrapper)).toBe('');
    expect(tip(wrapper)).toBe('menu for assigning inject partition');
  });

  it('keeps a value the disk does not have, and says so', async () => {
    answerAll();
    const wrapper = await picker({ modelValue: 3, disk: KALI });
    await use(wrapper);

    expect(options(wrapper)).toEqual([
      '1: ext4, start 1.0 MiB',
      '2: extended, start 2.0 MiB',
      '3 (not found on the disk)',
      '5: Linux swap, start 5.0 MiB',
    ]);
    expect(select(wrapper).element.value).toBe('3');
    expect(wrapper.emitted('update:modelValue')).toBeUndefined();
  });

  it('emits the number of the partition picked', async () => {
    answerAll();
    const wrapper = await picker({ modelValue: 1, disk: KALI });
    await use(wrapper);
    await select(wrapper).setValue('5');
    expect(wrapper.emitted('update:modelValue')).toEqual([[5]]);
  });

  it('reads nothing until it is used, and offers 1 to 4', async () => {
    answerAll();
    const wrapper = await picker({ modelValue: 2, disk: KALI });
    await flushPromises();

    expect(asked()).toEqual([]);
    expect(options(wrapper)).toEqual(['1', '2', '3', '4']);
    expect(note(wrapper)).toBe('');
  });

  it('reads the disk when the pointer moves onto it', async () => {
    answerAll();
    const wrapper = await picker({ modelValue: 1, disk: KALI });
    await wrapper.find('.partition-select').trigger('pointerenter');
    await flushPromises();
    expect(asked()).toEqual([KALI]);
    expect(options(wrapper)[0]).toBe('1: ext4, start 1.0 MiB');
  });

  it('offers 1 to 4 and says why while the details load', async () => {
    const answer = holdAll();
    const wrapper = await picker({ modelValue: 6, disk: KALI });
    await use(wrapper);

    expect(options(wrapper)).toEqual(['1', '2', '3', '4', '6']);
    const why =
      'Reading the partitions of the disk. The menu shows partitions 1 to 4.';
    expect(note(wrapper)).toBe(why);
    expect(tip(wrapper)).toBe(why);

    await answer(0);
    expect(options(wrapper)).toHaveLength(4);
    expect(options(wrapper)).toContain('6 (not found on the disk)');
    expect(note(wrapper)).toBe('');
  });

  it.each([
    [
      'a failed request',
      () => axios.get.mockRejectedValue(httpError(500, { message: 'boom.' })),
      'phēnix cannot read the partitions of the disk: boom. The menu shows partitions 1 to 4.',
    ],
    [
      'a disk with no partition table',
      () =>
        axios.get.mockResolvedValue({
          data: diskDetails({ partitionTable: 'none', partitions: [] }),
        }),
      'The disk has no partition table. The menu shows partitions 1 to 4.',
    ],
    [
      'an image phēnix cannot read',
      () =>
        axios.get.mockResolvedValue({
          data: diskDetails({
            partitionTable: '',
            partitionsUnavailable: 'phēnix cannot read raw images over 2 TiB',
          }),
        }),
      'phēnix cannot read the partitions: phēnix cannot read raw images over 2 TiB. The menu shows partitions 1 to 4.',
    ],
  ])('offers 1 to 4 for %s, and says why', async (_, answer, why) => {
    answer();
    const wrapper = await picker({ modelValue: 1, disk: KALI });
    await use(wrapper);

    expect(options(wrapper)).toEqual(['1', '2', '3', '4']);
    expect(note(wrapper)).toBe(why);
    expect(tip(wrapper)).toBe(why);
  });

  it('asks nothing for a role that may not list the disk', async () => {
    answerAll();
    rbac.allowed = denyOnly(['disks', 'list']);
    const wrapper = await picker({ modelValue: 1, disk: KALI });
    await use(wrapper);

    expect(asked()).toEqual([]);
    expect(options(wrapper)).toEqual(['1', '2', '3', '4']);
    expect(note(wrapper)).toBe(
      'Your role cannot read the partitions of the disk. The menu shows partitions 1 to 4.',
    );
  });

  it('says so for a VM with no disk', async () => {
    answerAll();
    const wrapper = await picker({ modelValue: 1, disk: '' });
    await use(wrapper);
    expect(asked()).toEqual([]);
    expect(note(wrapper)).toBe(
      'The VM has no disk. The menu shows partitions 1 to 4.',
    );
  });

  it('reads the new disk when the disk changes, and drops the old answer', async () => {
    const answer = holdAll();
    const wrapper = await picker({ modelValue: 1, disk: KALI });
    await use(wrapper);
    await wrapper.setProps({ disk: JAMMY });
    await flushPromises();
    expect(asked()).toEqual([KALI, JAMMY]);

    // kali's answer comes last but one, for a disk the VM no longer has
    await answer(0);
    expect(options(wrapper)).toEqual(['1', '2', '3', '4']);

    await answer(1);
    expect(options(wrapper)).toEqual([
      '1: FAT, start 1.0 MiB',
      '2: ext4, start 2.0 MiB',
    ]);
  });

  it('asks once for each disk, however many VMs use it', async () => {
    answerAll();
    const wrappers = await Promise.all(
      [KALI, KALI, JAMMY, KALI, JAMMY].map((disk) =>
        picker({ modelValue: 1, disk }),
      ),
    );
    await Promise.all(wrappers.map(use));

    expect(asked()).toEqual([KALI, JAMMY]);
    expect(options(wrappers[3])[0]).toBe('1: ext4, start 1.0 MiB');
    expect(options(wrappers[4])[0]).toBe('1: FAT, start 1.0 MiB');

    // a picker drawn later shows the details read before at once
    const later = await picker({ modelValue: 1, disk: KALI });
    expect(options(later)[0]).toBe('1: ext4, start 1.0 MiB');
    expect(asked()).toHaveLength(2);
  });

  it('reads a disk again once the server says the disks changed', async () => {
    answerAll();
    const first = await picker({ modelValue: 1, disk: KALI });
    await use(first);
    invalidatePages(wsMsg('disks', '', 'update', {}));

    const second = await picker({ modelValue: 1, disk: KALI });
    expect(options(second)).toEqual(['1', '2', '3', '4']);
    await use(second);
    expect(asked()).toEqual([KALI, KALI]);
    expect(options(second)[0]).toBe('1: ext4, start 1.0 MiB');
  });
});

describe('the partition setting of a stopped VM', () => {
  it('offers the partitions of the VM disk and sends the one picked', async () => {
    answerAll();
    const wrapper = await mountComponent(VmSettingControl, {
      props: {
        field: 'inject_partition',
        vm: { name: 'web', disk: JAMMY, inject_partition: 1 },
        editable: true,
      },
    });
    await use(wrapper);

    expect(options(wrapper)).toEqual([
      '1: FAT, start 1.0 MiB',
      '2: ext4, start 2.0 MiB',
    ]);
    await select(wrapper).setValue('2');
    expect(wrapper.emitted('change')).toEqual([[2]]);
  });
});
