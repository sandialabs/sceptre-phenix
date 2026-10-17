// The VM disk picker of the stopped experiment and the Redeploy window, with
// real Buefy: the disks it offers, the VM's disk and the warning beside it.
import { describe, expect, it, vi } from 'vitest';

vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));

import DiskSelect from '@/components/experiment/DiskSelect.vue';
import { disk } from './helpers/fixtures.js';
import { mountComponent, textOf, tooltipButtons } from './helpers/mount.js';

const disks = [
  disk('/phenix/images/win/base.qc2', 'win/base.qc2'),
  disk('/phenix/images/base.qc2', 'base.qc2'),
  disk('/phenix/images/my disk.qc2', 'my disk.qc2'),
  disk('/data/vms/e2e.img'),
];

// each option of a select, in order, with the label of its optgroup
const optionsOf = (select) =>
  select.findAll('option').map((o) => ({
    group:
      o.element.parentElement.localName === 'optgroup'
        ? o.element.parentElement.label
        : null,
    value: o.element.value,
    text: textOf(o),
    disabled: o.element.disabled,
  }));

// The picker for a VM whose disk is `modelValue`, given the disk list: its
// select, its options in order and the warnings beside it.
async function render(modelValue, list = disks) {
  const picker = await mountComponent(DiskSelect, {
    props: { modelValue, disks: list, label: 'Disk for VM web' },
  });
  const select = picker.find('select');
  return {
    picker,
    select,
    options: optionsOf(select),
    warnings: tooltipButtons(picker).map(({ label, tooltip }) => ({
      label,
      tooltip,
    })),
  };
}

const OUTSIDE = 'Outside the standard images directory';

describe('DiskSelect', () => {
  it('offers the disks by label, valued by full path, those outside apart', async () => {
    const { select, options, warnings } = await render(
      '/phenix/images/win/base.qc2',
    );

    expect(select.attributes('aria-label')).toBe('Disk for VM web');
    expect(select.attributes('title')).toBe('/phenix/images/win/base.qc2');
    expect(options).toEqual([
      {
        group: null,
        value: '/phenix/images/base.qc2',
        text: 'base.qc2',
        disabled: false,
      },
      {
        group: null,
        value: '/phenix/images/my disk.qc2',
        text: 'my disk.qc2 (minimega cannot use this name)',
        disabled: true,
      },
      {
        group: null,
        value: '/phenix/images/win/base.qc2',
        text: 'win/base.qc2',
        disabled: false,
      },
      {
        group: OUTSIDE,
        value: '/data/vms/e2e.img',
        text: '/data/vms/e2e.img',
        disabled: false,
      },
    ]);
    // a disk inside the files directory needs no warning
    expect(warnings).toEqual([]);
  });

  it('warns of a disk outside the standard images directory', async () => {
    const { picker, options, warnings } = await render('/data/vms/e2e.img');

    // the VM's disk is one of the options, not added again
    expect(options.map((o) => o.value)).toHaveLength(4);
    expect(warnings).toEqual([
      {
        label: OUTSIDE,
        tooltip:
          'Outside the standard images directory (/phenix/images). minimega ' +
          'does not copy this image to other cluster nodes, so on a ' +
          'multi-node cluster every node that may run a VM using it needs ' +
          'the same file at this path.',
      },
    ]);
    // the button is described by the same text, for screen readers, and
    // submits no form it is in
    const [{ control }] = tooltipButtons(picker);
    const describedBy = control.attributes('aria-describedby');
    expect(textOf(picker.find(`[id="${describedBy}"]`))).toBe(
      warnings[0].tooltip,
    );
    expect(control.attributes('type')).toBe('button');
  });

  it('shows a disk the loaded list does not hold as not listed, with a warning', async () => {
    const { options, warnings } = await render('/phenix/images/gone.qc2');

    expect(options[0]).toEqual({
      group: null,
      value: '/phenix/images/gone.qc2',
      text: '/phenix/images/gone.qc2 (not listed)',
      disabled: true,
    });
    expect(options).toHaveLength(5);
    expect(warnings).toEqual([
      {
        label: 'Not in your disk list',
        tooltip:
          'This image is not in your disk list: it may be missing, ' +
          'unreadable, or hidden from your role.',
      },
    ]);
  });

  it('shows the disk as it is, with no warning, while the list is unknown', async () => {
    // not loaded yet, or the role may not list disks
    const { options, warnings } = await render('/phenix/images/gone.qc2', null);

    expect(options).toEqual([
      {
        group: null,
        value: '/phenix/images/gone.qc2',
        text: '/phenix/images/gone.qc2',
        disabled: false,
      },
    ]);
    expect(warnings).toEqual([]);
  });

  it('says so for a VM without a disk', async () => {
    const { options, warnings } = await render('', []);
    expect(options).toEqual([
      { group: null, value: '', text: 'No disk', disabled: true },
    ]);
    expect(warnings).toEqual([]);
  });
});
