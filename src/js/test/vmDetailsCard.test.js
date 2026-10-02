// Renders the VM details card and the VM table's Actions buttons to strings
// with the real Buefy components: the storage section's backing chain and
// CD-ROM, the capture buttons' icon and spinner, the Network box's buttons,
// the annotations, and the header's description and links. The annotation
// editor runs with a plain object standing in for the component instance.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const rbac = vi.hoisted(() => ({ allowed: () => true }));
vi.mock('@/utils/rbac.js', () => ({
  roleAllowed: (...args) => rbac.allowed(...args),
}));
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));

import { icon, library } from '@fortawesome/fontawesome-svg-core';

import { faSharkFin } from '@/utils/icons.js';
import VmDetailsCard from '@/components/experiment/VmDetailsCard.vue';
import VmRowActions from '@/components/experiment/VmRowActions.vue';
import { renderSSR, textOf, tooltipButtons } from './helpers/render.js';

const vm = {
  name: 'web',
  state: 'RUNNING',
  running: true,
  cpus: 1,
  ram: 2048,
  ccActive: true,
  snapshot: true,
  disk: '/phenix/images/web_snap.qc2',
  cdRom: '',
  networks: ['DMZ (149)', 'MGMT (151)'],
  ipv4: ['10.0.0.5', '10.1.0.5'],
  taps: ['mega_tap1', 'mega_tap2'],
  captures: [{ vm: 'web', interface: 1 }],
};

beforeEach(() => {
  rbac.allowed = () => true;
});

const card = (props) =>
  renderSSR(VmDetailsCard, {
    vm,
    fullName: 'demo/web',
    experiment: 'demo',
    ...props,
  });

// the text of the storage section, up to the next section's title
const storage = (html) => {
  const start = html.indexOf('Storage');
  return textOf(
    html.slice(start, html.indexOf('<p class="vm-section-title"', start)),
  );
};

describe('VmDetailsCard storage', () => {
  it('leaves out the CD-ROM when none is inserted', async () => {
    const html = await card();
    expect(storage(html)).not.toMatch(/CD-ROM/);
    expect(html).not.toContain('vm-cdrom');
  });

  it('shows the inserted CD-ROM', async () => {
    const html = await card({
      vm: { ...vm, cdRom: '/phenix/images/tools.iso' },
    });
    expect(storage(html)).toMatch(/CD-ROM tools\.iso/);
  });

  it('names the CD-ROM by its label, warning of one outside the files directory', async () => {
    // the warnings in the CD-ROM row
    const warnings = (html) =>
      tooltipButtons(html.slice(html.indexOf('vm-cdrom'))).filter(
        (b) => b.label === 'Outside the standard images directory',
      );
    const inside = {
      kind: 'ISO',
      fullPath: '/phenix/images/isos/tools.iso',
      relativePath: 'isos/tools.iso',
      outsideFilesDir: false,
    };
    let html = await card({
      vm: { ...vm, cdRom: inside.fullPath },
      cdRomDisk: inside,
      filesDir: '/phenix/images',
    });
    expect(storage(html)).toMatch(/CD-ROM isos\/tools\.iso$/);
    expect(html).toContain('title="/phenix/images/isos/tools.iso"');
    expect(warnings(html)).toEqual([]);

    const outside = {
      kind: 'ISO',
      fullPath: '/data/isos/tools.iso',
      relativePath: '',
      outsideFilesDir: true,
    };
    html = await card({
      vm: { ...vm, cdRom: outside.fullPath },
      cdRomDisk: outside,
      filesDir: '/phenix/images',
    });
    expect(storage(html)).toMatch(/CD-ROM \/data\/isos\/tools\.iso /);
    expect(warnings(html).map((b) => b.tooltip)).toEqual([
      expect.stringMatching(
        /^Outside the standard images directory \(\/phenix\/images\)\./,
      ),
    ]);
  });

  it("shows the disk's backing chain under it, nearest first", async () => {
    const html = await card({
      backingChain: ['jammy_hardened.qc2', 'jammy.qc2'],
    });
    const text = storage(html);
    expect(text).toMatch(
      /Disk web_snap\.qc2 .*↓ jammy_hardened\.qc2 ↓ jammy\.qc2/,
    );
  });

  it('names the disk by its label, warning of one outside the files directory', async () => {
    const inside = {
      name: 'web_snap.qc2',
      fullPath: '/phenix/images/web/web_snap.qc2',
      relativePath: 'web/web_snap.qc2',
      outsideFilesDir: false,
    };
    let html = await card({
      vm: { ...vm, disk: inside.fullPath },
      disk: inside,
      filesDir: '/phenix/images',
    });
    expect(storage(html)).toContain('Disk web/web_snap.qc2 ');
    expect(tooltipButtons(html).map((b) => b.label)).not.toContain(
      'Outside the standard images directory',
    );

    const outside = {
      name: 'web.img',
      fullPath: '/data/vms/web.img',
      relativePath: '',
      outsideFilesDir: true,
    };
    html = await card({
      vm: { ...vm, disk: outside.fullPath },
      disk: outside,
      filesDir: '/phenix/images',
    });
    expect(storage(html)).toContain('Disk /data/vms/web.img');
    expect(
      tooltipButtons(html).find(
        (b) => b.label === 'Outside the standard images directory',
      )?.tooltip,
    ).toMatch(/^Outside the standard images directory \(\/phenix\/images\)\./);
  });

  it('names the disk by file name while the disk list is unknown', async () => {
    const html = await card();
    expect(storage(html)).toContain('Disk web_snap.qc2 ');
  });

  it('says none for a VM without a disk', async () => {
    const html = await card({ vm: { ...vm, disk: '' } });
    expect(storage(html)).toContain(' Disk none ');
  });

  it('shows the disk alone when it has no backing image', async () => {
    for (const backingChain of [[], null]) {
      const html = await card({ backingChain });
      expect(html).not.toContain('vm-backing-chain');
    }
  });
});

describe('VmDetailsCard capture buttons', () => {
  it('start with the capture icon and stop with the stop icon', async () => {
    const html = await card();
    expect(html).toMatch(/vm-capture-start[^>]*>[\s\S]*?data-icon="shark-fin"/);
    expect(html).toMatch(/vm-capture-stop[^>]*>[\s\S]*?data-icon="stop"/);
  });

  it('shows a spinner on the interface waiting on the server', async () => {
    const html = await card({ capturePending: [0] });
    const buttons = html.match(/<button[^>]*vm-capture-[^>]*>/g);
    expect(buttons).toHaveLength(2);
    expect(buttons[0]).toContain('is-loading');
    expect(buttons[1]).not.toContain('is-loading');
  });
});

describe('VmDetailsCard Open in WebShark', () => {
  const links = (html) => html.match(/<a[^>]*vm-webshark[" ][^>]*>/g) ?? [];

  it('opens each running capture on the WebShark page', async () => {
    const html = await card({ features: ['webshark'] });
    const found = links(html);
    // only interface 1 is capturing
    expect(found).toHaveLength(1);
    expect(found[0]).toMatch(
      /href="\/packets\?exp=demo&(amp;)?vm=web&(amp;)?iface=1"/,
    );
    expect(found[0]).toContain('aria-label="Open in WebShark"');
    // not the capture icon, which starts captures
    expect(html).toMatch(/vm-webshark[^>]*>[\s\S]*?data-icon="search"/);
  });

  it('needs WebShark installed', async () => {
    expect(links(await card())).toEqual([]);
  });

  it('needs permission to list the VM captures', async () => {
    rbac.allowed = (resource, verb) =>
      !(resource === 'vms/captures' && verb === 'list');
    expect(links(await card({ features: ['webshark'] }))).toEqual([]);
  });
});

// the Network box's buttons: [tooltip, attributes] for each of them
const networkButtons = (html) => {
  const title = html.match(/vm-section-title[^>]*> Network[\s\S]*?<\/p>/)[0];
  return tooltipButtons(title).map((b) => [b.tooltip, b.attrs]);
};
const networkButton = (html, name) =>
  networkButtons(html).find(([, attrs]) => attrs.includes(`vm-${name}`));

describe('VmDetailsCard Network buttons', () => {
  const twoCaptured = [
    { vm: 'web', interface: 0 },
    { vm: 'web', interface: 1 },
  ];

  it('start captures on every interface and stop them all', async () => {
    const html = await card();
    const [start, stop] = networkButtons(html);
    expect(start[0]).toBe(
      'Start a packet capture on every connected interface of the VM',
    );
    expect(start[1]).toMatch(/vm-captureAll[^"]*is-success/);
    expect(start[1]).not.toContain('disabled');
    expect(stop[0]).toBe('Stop every packet capture running on the VM');
    expect(stop[1]).toMatch(/vm-stopCaptures[^"]*is-danger/);
    expect(stop[1]).not.toContain('disabled');
    expect(html).toMatch(/vm-captureAll[^>]*>[\s\S]*?data-icon="play"/);
    expect(html).toMatch(/vm-stopCaptures[^>]*>[\s\S]*?data-icon="stop"/);
  });

  it('disable stop, saying why, when nothing is captured', async () => {
    const html = await card({ vm: { ...vm, captures: [] } });
    const [label, attrs] = networkButton(html, 'stopCaptures');
    expect(attrs).toContain('disabled');
    expect(label).toBe(
      "Can't stop all packet captures: no packet capture is running",
    );
    expect(networkButton(html, 'captureAll')[1]).not.toContain('disabled');
  });

  it('disable start, saying why, when every interface is captured', async () => {
    const html = await card({ vm: { ...vm, captures: twoCaptured } });
    const [label, attrs] = networkButton(html, 'captureAll');
    expect(attrs).toContain('disabled');
    expect(label).toBe(
      "Can't capture all interfaces: every connected interface is already being captured",
    );
    expect(networkButton(html, 'stopCaptures')[1]).not.toContain('disabled');
  });

  it('spin the one waiting on the server and hold the other', async () => {
    const html = await card({
      vm: { ...vm, captures: [] },
      captureAllPending: true,
    });
    const start = networkButton(html, 'captureAll')[1];
    const stop = networkButton(html, 'stopCaptures')[1];
    expect(start).toContain('is-loading');
    expect(stop).not.toContain('is-loading');
    expect(start).toContain('disabled');
    expect(stop).toContain('disabled');
  });

  it("leave out what the role can't do", async () => {
    rbac.allowed = (resource, verb) =>
      !(resource === 'vms/captures' && verb === 'create');
    const html = await card();
    expect(networkButton(html, 'captureAll')).toBeUndefined();
    expect(networkButton(html, 'stopCaptures')).toBeDefined();
  });

  it("link to the WebShark tab on the VM's running capture", async () => {
    const html = await card({ features: ['webshark'] });
    const [label, attrs] = networkButton(html, 'webshark-tab');
    expect(label).toBe("Open this VM's running capture in the WebShark tab");
    expect(attrs).toMatch(
      /href="\/packets\?exp=demo&(amp;)?vm=web&(amp;)?iface=1"/,
    );
  });

  it('link to the first of several running captures', async () => {
    const html = await card({
      vm: { ...vm, captures: [...twoCaptured].reverse() },
      features: ['webshark'],
    });
    const [label, attrs] = networkButton(html, 'webshark-tab');
    expect(label).toBe(
      "Open this VM's capture of interface 0 in the WebShark tab",
    );
    expect(attrs).toMatch(/iface=0"/);
  });

  it("link to the experiment's captures when none is running", async () => {
    const html = await card({
      vm: { ...vm, captures: [] },
      features: ['webshark'],
    });
    const [label, attrs] = networkButton(html, 'webshark-tab');
    expect(label).toBe("Open the WebShark tab on this experiment's captures");
    expect(attrs).toMatch(/href="\/packets\?exp=demo"/);
  });

  it('link to the WebShark tab only where it is installed', async () => {
    expect(networkButton(await card(), 'webshark-tab')).toBeUndefined();
  });
});

describe('VmDetailsCard annotations', () => {
  // the annotations section's text
  const section = (html) => {
    const start = html.indexOf('vm-annotations');
    return start < 0
      ? null
      : textOf(html.slice(start, html.indexOf('vm-lists', start)));
  };

  it("wait for the VM's own details, which carry them", async () => {
    expect(section(await card())).toBeNull();
    expect(
      section(await card({ vm: { ...vm, annotations: null } })),
    ).toBeNull();
  });

  it('list each one by key, hiding passwords', async () => {
    const html = await card({
      vm: {
        ...vm,
        annotations: {
          vncBanner: 'Authorized use only',
          'vrouter/vyos-password': 'hunter2',
          'phenix/startup-autotunnel': ['8080', '2222:22'],
          'phenix/default-apps': false,
        },
      },
    });
    const text = section(html);
    expect(text).toContain(
      'phenix/default-apps false ' +
        'phenix/startup-autotunnel ["8080","2222:22"] ' +
        'vncBanner Authorized use only ' +
        'vrouter/vyos-password ••••••',
    );
    expect(html).not.toContain('hunter2');
    expect(html).toContain('aria-label="Show vrouter/vyos-password"');
  });

  it('say when there are none', async () => {
    const text = section(await card({ vm: { ...vm, annotations: {} } }));
    expect(text).toContain('No annotations');
  });

  it('offer editing to roles that can patch the VM', async () => {
    const withAnnotations = { vm: { ...vm, annotations: {} } };
    expect(await card(withAnnotations)).toContain('vm-edit-annotations');

    rbac.allowed = (resource, verb) =>
      !(resource === 'vms' && verb === 'patch');
    expect(await card(withAnnotations)).not.toContain('vm-edit-annotations');
  });

  const { editAnnotations, saveAnnotations } = VmDetailsCard.methods;

  it('edit as rows and save the whole set, keeping unchanged values as they were', () => {
    const ctx = {
      vm: {
        annotations: {
          'phenix/startup-autotunnel': ['8080', '2222:22'],
          'phenix/default-apps': 'no',
          retries: 3,
        },
      },
      $emit: vi.fn(),
    };
    editAnnotations.call(ctx);
    expect(
      ctx.annotationRows.map(({ key, type, value }) => [key, type, value]),
    ).toEqual([
      // not a boolean, so a custom row that can be fixed
      ['phenix/default-apps', 'text', 'no'],
      ['phenix/startup-autotunnel', 'list', '8080, 2222:22'],
      ['retries', 'text', '3'],
    ]);

    ctx.annotationRows[1].value = '8080';
    saveAnnotations.call(ctx);
    expect(ctx.annotationsSaving).toBe(true);
    const [event, payload, done] = ctx.$emit.mock.calls[0];
    expect(event).toBe('save-annotations');
    expect(payload).toEqual({
      'phenix/default-apps': 'no',
      'phenix/startup-autotunnel': ['8080'],
      retries: 3,
    });

    done(false);
    expect(ctx.annotationsSaving).toBe(false);
    expect(ctx.annotationRows).not.toBeNull();
    done(true);
    expect(ctx.annotationRows).toBeNull();
  });
});

describe('VmDetailsCard header', () => {
  it('puts the description between the name and the links', async () => {
    const html = await card({
      vm: { ...vm, description: 'Public web server' },
    });
    const [head, body] = html.split('</header>');
    expect(head).toMatch(
      /vm-card-heading[\s\S]*vm-card-description[^>]*>\s*Public web server\s*<\/p>[\s\S]*vm-card-links/,
    );
    expect(head).toContain('has-description');
    expect(body).not.toContain('Public web server');
  });

  it('leaves out the description box without one', async () => {
    const html = await card();
    expect(html).not.toContain('vm-card-description');
    expect(html).not.toContain('has-description');
  });

  it("links to the experiment's SOH and SCORCH pages", async () => {
    const html = await card();
    expect(html).toMatch(/href="\/soh\/demo"[^>]*aria-label="view soh"/);
    expect(html).toMatch(/href="\/scorch\/demo"[^>]*aria-label="view scorch"/);
  });

  it("hides the SOH and SCORCH links from roles that can't get the experiment", async () => {
    rbac.allowed = (resource, verb) =>
      !(resource === 'experiments' && verb === 'get');
    const html = await card();
    expect(html).not.toContain('aria-label="view soh"');
    expect(html).not.toContain('aria-label="view scorch"');
    expect(html).toContain('vm-card-docs');
  });

  it('links to the VM docs and the Disks page', async () => {
    const html = await card();
    expect(html).toMatch(
      /class="[^"]*vm-card-docs"[^>]*href="https:\/\/phenix\.sceptre\.dev\/latest\/vms\/"/,
    );
    expect(html).toMatch(/href="\/disks\/"[^>]*vm-card-disks/);

    rbac.allowed = (resource) => resource !== 'disks';
    expect(await card()).not.toContain('vm-card-disks');
  });

  it('puts the miniccc agent first among the stats', async () => {
    const html = await card();
    const labels = [...html.matchAll(/vm-stat-label[^>]*>([^<]*)</g)].map((m) =>
      m[1].trim(),
    );
    expect(labels).toEqual(['miniccc agent', 'vCPU', 'Memory', 'Interfaces']);
  });
});

describe('VmRowActions', () => {
  it('shows a spinner on an action still running', async () => {
    const html = await renderSSR(VmRowActions, {
      vm: { ...vm, captures: [] },
      fullName: 'demo/web',
      pending: ['captureAll'],
    });
    const button = html.match(/<button[^>]*vm-row-captureAll[^>]*>/)[0];
    expect(button).toContain('is-loading');
    expect(button).toContain('disabled');
    expect(html).toMatch(/vm-row-captureAll[\s\S]*?data-icon="shark-fin"/);
  });
});

describe('the capture icon', () => {
  it('is a solid icon Font Awesome can draw by name', () => {
    library.add(faSharkFin);
    const svg = icon({ prefix: 'fas', iconName: 'shark-fin' });
    expect(svg).toBeDefined();
    expect(svg.html.join('')).toMatch(/<svg[^>]*viewBox="0 0 512 512"/);
  });
});
