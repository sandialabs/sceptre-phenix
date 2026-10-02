// The VM disk picker the stopped experiment's Disk column and the Redeploy
// window share, rendered with the real Buefy components: the disks it offers,
// how it shows the VM's disk, and the warning beside it.
import { describe, expect, it, vi } from 'vitest';

vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));

import DiskSelect from '@/components/experiment/DiskSelect.vue';
import {
  renderSSR,
  selectOptions,
  textOf,
  tooltipButtons,
} from './helpers/render.js';

// A disk as GET disks lists it: inside the files directory /phenix/images
// when given its path within it, else outside it.
const disk = (fullPath, relativePath = '') => ({
  kind: 'VM',
  name: fullPath.replace(/^.*\//, ''),
  fullPath,
  relativePath,
  outsideFilesDir: !relativePath,
  readOnly: !relativePath,
});

const disks = [
  disk('/phenix/images/win/base.qc2', 'win/base.qc2'),
  disk('/phenix/images/base.qc2', 'base.qc2'),
  disk('/phenix/images/my disk.qc2', 'my disk.qc2'),
  disk('/data/vms/e2e.img'),
];

// The picker for a VM whose disk is `modelValue`, given the disk list: its
// select, its options in order and the warnings beside it.
async function render(modelValue, list = disks) {
  const html = await renderSSR(DiskSelect, {
    modelValue,
    disks: list,
    label: 'Disk for VM web',
  });
  const select = html.match(/<select\b[^>]*>[\s\S]*?<\/select>/)[0];
  return {
    select,
    options: selectOptions(select),
    warnings: tooltipButtons(html).map(({ label, tooltip }) => ({
      label,
      tooltip,
    })),
    html,
  };
}

const OUTSIDE = 'Outside the standard images directory';

describe('DiskSelect', () => {
  it('offers the disks by label, valued by full path, those outside apart', async () => {
    const { select, options, warnings } = await render(
      '/phenix/images/win/base.qc2',
    );

    expect(select).toContain('aria-label="Disk for VM web"');
    expect(select).toContain('title="/phenix/images/win/base.qc2"');
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
    const { options, warnings, html } = await render('/data/vms/e2e.img');

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
    const describedBy = html.match(/aria-describedby="([^"]*)"/)[1];
    expect(
      textOf(html.match(new RegExp(`id="${describedBy}"[^>]*>([^<]*)<`))[1]),
    ).toBe(warnings[0].tooltip);
    expect(tooltipButtons(html)[0].attrs).toMatch(/\stype="button"/);
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

  it('names a same-named disk in another folder the list does not hold as not listed', async () => {
    const { options } = await render('/phenix/images/other/base.qc2');
    expect(options[0].text).toBe('/phenix/images/other/base.qc2 (not listed)');
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
