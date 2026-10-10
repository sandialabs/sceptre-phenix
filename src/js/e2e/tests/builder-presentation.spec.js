// How the Builder's canvas presents its nodes: the type a device shows, the
// info tooltip of devices and switches on hover and keyboard focus, what the
// palette's device templates put into the devices they add, the outline and
// fill colors of devices and switches, the line styles of networks and
// connections, a group's description, border pattern and icon, and the
// custom icons of devices and groups.
//
// Every test makes its own draft; the `tracker` fixture deletes it
// afterwards. A check that no later step depends on is soft, so one failure
// does not hide the steps after it.
//
// Every test and every worker shares the server's one icon library. A test
// that adds an icon to it gives it a name of its own (iconName) and builds
// the image itself, in colors drawn at random, so no other test has that
// name or those bytes; the `tracker` fixture deletes what the test added.

const crypto = require('crypto');
const fs = require('fs');

const {
  API,
  PNG_SIGNATURE,
  test,
  blankDocument,
  contrast,
  devicesOf,
  expect,
  expectAccessible,
  expectNoFatal,
  iconName,
  iconOf,
  ownColor,
  pngOf,
  waitForApi,
} = require('./builder-support');

// How many custom icons one document may carry: MAX_DOCUMENT_ICONS in
// src/builder/icons.js and MaxDocumentIcons in customicons.go.
const MAX_DOCUMENT_ICONS = 50;

test.use({ announceHold: 100 });

// Vue Flow's wrapper around a canvas node: the focusable, named element,
// which the node's info describes.
function flowNode(page, id) {
  return page.locator(`.vue-flow__node[data-id="${id}"]`);
}

// A group of the Inspector's device form, by its legend.
function specGroup(builder, title) {
  return builder.inspector
    .locator('legend.group-label', { hasText: new RegExp(`^${title}$`) })
    .locator('xpath=..');
}

// The canvas has one info tooltip for all its nodes, outside them.
function infoTooltip(page) {
  return page.getByTestId('node-tooltip');
}

// The tooltip's rows, as [label, lines] pairs.
function rowsOf(tooltip) {
  return tooltip
    .locator('dl')
    .evaluate((list) =>
      [...list.querySelectorAll('dt')].map((label) => [
        label.textContent.trim(),
        [
          ...label.nextElementSibling.querySelectorAll('.builder-info__line'),
        ].map((line) => line.textContent.trim()),
      ]),
    );
}

// The tooltip is beside its node, above or below it and clear of it, with
// their left edges in line, and inside the window.
async function expectBeside(page, tooltip, node, message) {
  await expect
    .soft(async () => {
      const tip = await tooltip.boundingBox();
      const box = await node.boundingBox();
      const { width, height } = page.viewportSize();
      const gap =
        tip.y >= box.y
          ? tip.y - (box.y + box.height)
          : box.y - (tip.y + tip.height);

      expect(gap, `${message}: gap`).toBeGreaterThanOrEqual(4);
      expect(gap, `${message}: gap`).toBeLessThanOrEqual(24);
      expect(Math.abs(tip.x - box.x), `${message}: left edges`).toBeLessThan(1);
      expect(tip.x, `${message}: left`).toBeGreaterThanOrEqual(0);
      expect(tip.y, `${message}: top`).toBeGreaterThanOrEqual(0);
      expect(tip.x + tip.width, `${message}: right`).toBeLessThanOrEqual(width);
      expect(tip.y + tip.height, `${message}: bottom`).toBeLessThanOrEqual(
        height,
      );
    })
    .toPass({ timeout: 5000 });
}

// The field of the Inspector's form for a data path. A device has two
// color pickers and a switch three, all with one test id, so each is found
// inside its field.
function inspectorField(builder, path) {
  const field = builder.inspector.locator(`[data-path="${path}"]`);

  return {
    field,
    label: field.locator('label.label'),
    text: field.locator('input[type="text"]'),
    select: field.locator('select'),
    picker: field.getByTestId('inspector-color-picker'),
    popup: field.getByTestId('inspector-color-popup'),
  };
}

// The names of a select's choices, in order.
function choicesOf(select) {
  return select
    .locator('option')
    .evaluateAll((options) =>
      options.map((option) => option.textContent.trim()),
    );
}

// The color the theme shown gives a custom property, such as --bx-accent,
// as the browser computes it.
function themeColor(page, property) {
  return page.locator('.builder-root').evaluate((root, name) => {
    const probe = document.createElement('span');

    probe.style.color = `var(${name})`;
    root.append(probe);
    const { color } = getComputedStyle(probe);
    probe.remove();

    return color;
  }, property);
}

// The lines of text a device or switch node shows, and its icon.
const NODE_TEXT =
  '.builder-node__label, .builder-node__kind, .builder-node__meta, .builder-node__comment';

// Every line of text of `node` is drawn in `ink` and keeps 4.5:1 against
// what is behind it (WCAG 1.4.3), and its icon is drawn in `ink` too.
async function expectReadable(node, ink, message) {
  const lines = await contrast(node.locator(NODE_TEXT));

  expect.soft(lines.length, `${message}: lines of text`).toBeGreaterThan(1);
  for (const line of lines) {
    expect.soft(line.color, `${message}: color of "${line.text}"`).toBe(ink);
    expect
      .soft(line.ratio, `${message}: contrast of "${line.text}"`)
      .toBeGreaterThanOrEqual(4.5);
  }
  await expect
    .soft(node.locator('.builder-icon').first(), `${message}: icon`)
    .toHaveCSS('color', ink);
}

// How many pixels of an image are drawn in each of `colors` ([r, g, b]),
// give or take 2 a channel, as the browser draws the image.
async function pixelsOf(page, buffer, type, colors) {
  return page.evaluate(
    async ({ data, mime, wanted }) => {
      const image = new Image();

      image.src = `data:${mime};base64,${data}`;
      await image.decode();

      const canvas = document.createElement('canvas');

      canvas.width = image.width;
      canvas.height = image.height;

      const context = canvas.getContext('2d');

      context.drawImage(image, 0, 0);

      const { data: pixels } = context.getImageData(
        0,
        0,
        canvas.width,
        canvas.height,
      );
      const counts = wanted.map(() => 0);

      for (let at = 0; at < pixels.length; at += 4) {
        wanted.forEach((color, index) => {
          if (
            pixels[at + 3] === 255 &&
            color.every((value, k) => Math.abs(pixels[at + k] - value) <= 2)
          ) {
            counts[index] += 1;
          }
        });
      }

      return counts;
    },
    { data: buffer.toString('base64'), mime: type, wanted: colors },
  );
}

// Two devices, web-01 (a Router with a description) and web-02, each
// connected by eth0 to the switch of network EXP, and web-02 by eth1 to the
// switch of network MGMT too; and a group, Zone, beside them. `look` gives
// nodes their colors: { 'web-01': {...}, EXP: {...} }, by label. The
// returned document has the ids of its connections, by name, as `edgeIds`.
function styledDocument(name, look = {}) {
  const id = () => crypto.randomUUID();
  const networks = ['EXP', 'MGMT'].map((label) => ({ id: id(), name: label }));
  const switches = networks.map((network, index) => ({
    id: id(),
    kind: 'switch',
    label: network.name,
    position: { x: 384, y: 96 + index * 224 },
    switch: { networkId: network.id, ...look[network.name] },
  }));
  const device = (hostname, y, type, vlans, description) => ({
    id: id(),
    kind: 'device',
    label: hostname,
    position: { x: 64, y },
    device: {
      hostname,
      iconKey: 'linux',
      ...look[hostname],
      spec: {
        type,
        general: {
          hostname,
          vm_type: 'kvm',
          ...(description ? { description } : {}),
        },
        hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
        network: {
          interfaces: vlans.map((vlan, index) => ({
            name: `eth${index}`,
            type: 'ethernet',
            proto: 'dhcp',
            vlan,
          })),
        },
      },
      interfaces: vlans.map((_, index) => ({
        id: id(),
        name: `eth${index}`,
        index,
      })),
    },
  });
  const web01 = device('web-01', 80, 'Router', ['EXP'], 'Front end');
  const web02 = device('web-02', 304, 'VirtualMachine', ['EXP', 'MGMT']);
  const group = {
    id: id(),
    kind: 'group',
    label: 'Zone',
    position: { x: 640, y: 96 },
    size: { width: 320, height: 240 },
    group: { title: 'Zone' },
  };
  const edge = (node, handle, sw) => ({
    id: id(),
    sourceNodeId: node.id,
    sourceHandleId: node.device.interfaces[handle].id,
    targetNodeId: sw.id,
    networkId: sw.switch.networkId,
  });
  const edges = [
    edge(web01, 0, switches[0]),
    edge(web02, 0, switches[0]),
    edge(web02, 1, switches[1]),
  ];

  return {
    ...blankDocument(name, {
      nodes: [web01, web02, ...switches, group],
      networks,
      edges,
    }),
    edgeIds: {
      'web-01 to EXP': edges[0].id,
      'web-02 to EXP': edges[1].id,
      'web-02 to MGMT': edges[2].id,
    },
  };
}

// Seeds a draft of styledDocument(), and returns it with the ids of its
// connections.
async function seedStyled(builder, testInfo, look) {
  const { edgeIds, ...document } = styledDocument(
    `presentation-${testInfo.project.name}-${Date.now()}`,
    look,
  );

  return { draft: await builder.seedDraft(document), edgeIds };
}

const LONG_DESCRIPTION = `Front end   web server ${'x'.repeat(100)}`;
const NETWORK_DESCRIPTION = `Experiment network ${'y'.repeat(100)}`;

// Nine devices, web-01 to web-09, each connected by eth0 to the one switch of
// network EXP: odd ones with the address 10.0.0.<n>/24, even ones asking
// DHCP. web-01 is a Router with a long description and a second connection
// to the switch, on eth1. Two more devices are not connected: an external
// one, and one without a node type. Two rows of four devices are above the
// switch, which is nearest the middle of the canvas, and the rest in a row
// below it, all inside the canvas of the test window.
function tooltipDocument(name) {
  const id = () => crypto.randomUUID();
  const network = {
    id: id(),
    name: 'EXP',
    alias: 100,
    description: NETWORK_DESCRIPTION,
  };
  const sw = {
    id: id(),
    kind: 'switch',
    label: 'EXP',
    position: { x: 384, y: 368 },
    switch: { networkId: network.id },
  };
  const device = (hostname, position, spec, names = []) => ({
    id: id(),
    kind: 'device',
    label: hostname,
    position,
    device: {
      hostname,
      iconKey: 'linux',
      spec: { general: { hostname }, ...spec },
      interfaces: names.map((ifaceName, index) => ({
        id: id(),
        name: ifaceName,
        index,
      })),
    },
  });
  const web = Array.from({ length: 9 }, (_, index) => {
    const n = index + 1;
    const hostname = `web-0${n}`;
    const eth0 =
      n % 2
        ? { proto: 'static', address: `10.0.0.${n}`, mask: 24 }
        : { proto: 'dhcp' };
    const interfaces = [
      { name: 'eth0', type: 'ethernet', vlan: 'EXP', ...eth0 },
      ...(n === 1
        ? [{ name: 'eth1', type: 'ethernet', vlan: 'EXP', proto: 'dhcp' }]
        : []),
    ];

    return device(
      hostname,
      index < 8
        ? { x: (index % 4) * 224, y: Math.floor(index / 4) * 160 }
        : { x: 0, y: 480 },
      {
        type: n === 1 ? 'Router' : 'VirtualMachine',
        general: {
          hostname,
          vm_type: 'kvm',
          ...(n === 1 ? { description: LONG_DESCRIPTION } : {}),
        },
        hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
        network: { interfaces },
      },
      interfaces.map((iface) => iface.name),
    );
  });
  const external = device(
    'plc-1',
    { x: 224, y: 480 },
    { external: true, type: 'HIL', network: { interfaces: [] } },
  );
  const untyped = device(
    'bare',
    { x: 448, y: 480 },
    {
      general: { hostname: 'bare', vm_type: 'kvm' },
      hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
      network: { interfaces: [] },
    },
  );

  return blankDocument(name, {
    nodes: [...web, external, untyped, sw],
    networks: [network],
    edges: web.flatMap((node) =>
      node.device.interfaces.map((handle) => ({
        id: id(),
        sourceNodeId: node.id,
        sourceHandleId: handle.id,
        targetNodeId: sw.id,
        networkId: network.id,
      })),
    ),
  });
}

test('a device shows its node type, and a template adds a device with no description', async ({
  page,
  builder,
  issues,
}) => {
  await builder.open();
  const draft = await builder.createBlank();

  for (const item of [
    'device',
    'template-server',
    'template-workstation',
    'template-router',
    'template-firewall',
    'template-external',
  ]) {
    await builder.palette(item).click();
  }
  await builder.expectCounts({ devices: 6 });

  await test.step('each device shows its phenix node type as stored, an external one External', async () => {
    const types = {
      node: 'VirtualMachine',
      server: 'VirtualMachine',
      workstation: 'VirtualMachine',
      router: 'Router',
      firewall: 'Firewall',
      external: 'External',
    };

    for (const [hostname, type] of Object.entries(types)) {
      const label = builder.node(hostname, 'device').getByTestId('node-type');

      await expect.soft(label, hostname).toHaveText(type);
      // As stored, not in capitals as the kind of a switch is.
      await expect.soft(label, hostname).toHaveCSS('text-transform', 'none');
    }
    // No device says only "Device" any more.
    await expect
      .soft(builder.nodes('device').getByText('Device', { exact: true }))
      .toHaveCount(0);

    await builder.palette('switch').click();
    await expect
      .soft(builder.nodes('switch').locator('.builder-node__kind'))
      .toHaveText('Switch');
    await expect
      .soft(builder.nodes('switch').getByTestId('node-type'))
      .toHaveCount(0);
  });

  await test.step('a template leaves the description empty: no last line on the node, no comment in its name', async () => {
    await expect
      .soft(builder.nodes('device').locator('.builder-node__comment'))
      .toHaveCount(0);

    for (const name of [
      'Device server, 0 connections',
      'Device workstation, 0 connections',
      'Device router, 0 connections',
      'Device firewall, 0 connections',
      'Device external, 0 connections',
    ]) {
      await expect
        .soft(page.getByRole('button', { name, exact: true }).first())
        .toBeVisible();
    }

    // The palette entry keeps its description, as its tooltip.
    await expect
      .soft(builder.palette('template-firewall'))
      .toHaveAccessibleDescription('Perimeter firewall');
  });

  await test.step('the stored devices have no description, and the Firewall runs VyOS on vyos.qc2', async () => {
    await builder.persisted(draft, (doc) => doc.nodes.length, 7);
    const devices = Object.fromEntries(
      devicesOf(await builder.serverDocument(draft)).map((node) => [
        node.device.hostname,
        node.device.spec,
      ]),
    );

    for (const [hostname, spec] of Object.entries(devices)) {
      expect.soft(spec.general.description, hostname).toBe('');
    }
    expect.soft(devices.firewall).toMatchObject({
      type: 'Firewall',
      hardware: { os_type: 'vyos', drives: [{ image: 'vyos.qc2' }] },
    });
    expect.soft(devices.router).toMatchObject({
      type: 'Router',
      hardware: {
        os_type: 'minirouter',
        drives: [{ image: 'minirouter.qc2' }],
      },
    });
    expect.soft(devices.external).toEqual({
      external: true,
      type: 'HIL',
      general: { hostname: 'external', description: '' },
      network: { interfaces: [] },
    });
  });

  await test.step('the Inspector shows the Firewall template’s OS type and image', async () => {
    await builder.selectInOutline('firewall');
    const hardware = specGroup(builder, 'Hardware');

    await expect
      .soft(builder.inspector.getByLabel(/^Type/))
      .toHaveValue('Firewall');
    await expect.soft(hardware.getByLabel(/^OS type/)).toHaveValue('vyos');
    await expect.soft(hardware.getByLabel(/^Image/)).toHaveValue('vyos.qc2');
    await expect
      .soft(specGroup(builder, 'General').getByLabel('Description'))
      .toHaveValue('');
  });

  expectNoFatal(issues);
});

test(
  'a device and a switch show an info tooltip on hover and keyboard focus',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const draft = await builder.seedDraft(
      tooltipDocument(`presentation-${testInfo.project.name}-${Date.now()}`),
    );
    await builder.openDraft(draft);

    const tooltip = infoTooltip(page);
    const web01 = builder.node('web-01', 'device');
    const sw = builder.nodes('switch');
    const idOf = (node) => node.getAttribute('data-node-id');
    const switchWrapper = flowNode(page, await idOf(sw));
    const web01Wrapper = flowNode(page, await idOf(web01));
    // Away from every node, on the Outline's heading. A node shows its
    // tooltip for a pointer that moves onto it, not for one it is drawn
    // under, so the pointer starts from here.
    const parkPointer = () =>
      page.getByRole('heading', { name: 'Outline' }).hover();

    await test.step('each device shows its type: as stored, External, or Device without one', async () => {
      const type = (hostname) =>
        builder.node(hostname, 'device').getByTestId('node-type');

      await expect.soft(type('web-01')).toHaveText('Router');
      await expect.soft(type('web-02')).toHaveText('VirtualMachine');
      await expect.soft(type('plc-1')).toHaveText('External');
      await expect.soft(type('bare')).toHaveText('Device');
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('the pointer resting on a device shows its description, interfaces and OS type', async () => {
      // When the pointer entered the node and when the tooltip came up.
      await web01Wrapper.evaluate((wrapper) => {
        window.__tipTimes = {};
        wrapper.addEventListener(
          'mouseenter',
          () => {
            window.__tipTimes.entered = performance.now();
          },
          { once: true },
        );
        new MutationObserver((_, observer) => {
          if (document.querySelector('[data-testid="node-tooltip"]')) {
            window.__tipTimes.shown = performance.now();
            observer.disconnect();
          }
        }).observe(document.body, { childList: true, subtree: true });
      });

      await parkPointer();
      await web01.hover();
      await expect(tooltip).toBeVisible();
      expect.soft(await rowsOf(tooltip)).toEqual([
        // Cut to 80 characters, its white space single spaces.
        ['Description', [`Front end web server ${'x'.repeat(59)}…`]],
        ['Interfaces', ['eth0 — 10.0.0.1/24', 'eth1 — DHCP']],
        ['OS type', ['linux']],
      ]);
      await expect.soft(tooltip).toHaveAttribute('aria-hidden', 'true');
      await expectBeside(page, tooltip, web01, 'a device tooltip');

      // It waits for the pointer to rest: 400 ms.
      const { entered, shown } = await page.evaluate(() => window.__tipTimes);
      expect.soft(shown - entered, 'the delay').toBeGreaterThanOrEqual(390);

      await expectAccessible(page, {
        soft: true,
        label: 'with a node tooltip showing',
      });
    });

    // WCAG 1.4.13: hoverable, dismissible, persistent.
    await test.step('the pointer can move onto the tooltip, and Escape hides it', async () => {
      const box = await tooltip.boundingBox();
      const point = { x: box.x + 12, y: box.y + box.height / 2 };

      await page.mouse.move(point.x, point.y, { steps: 10 });
      // Longer than the tooltip waits before it goes.
      await page.waitForTimeout(500);
      await expect.soft(tooltip).toBeVisible();

      // It lets clicks through to the canvas beneath it.
      const through = await page.evaluate(
        ({ x, y }) => document.elementFromPoint(x, y)?.dataset?.testid || '',
        point,
      );
      expect.soft(through).not.toBe('node-tooltip');

      await page.keyboard.press('Escape');
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('a click selects the node without the tooltip, and Escape on the tooltip keeps the selection', async () => {
      await parkPointer();
      await web01.click();
      await expect(web01).toHaveClass(/is-selected/);
      await expect.soft(tooltip).toHaveCount(0);

      // Off the node and onto it again.
      await parkPointer();
      await web01.hover();
      await expect(tooltip).toBeVisible();

      // The first Escape only hides the tooltip.
      await page.keyboard.press('Escape');
      await expect(tooltip).toHaveCount(0);
      await expect.soft(web01).toHaveClass(/is-selected/);
      await expect.soft(web01Wrapper).toHaveAttribute('aria-pressed', 'true');
      await expect.soft(web01Wrapper).toBeFocused();
      await expect.soft(builder).not.toHaveAnnounced('Selection cleared');

      // The second clears the selection, as Escape does on the canvas.
      await page.keyboard.press('Escape');
      await expect.soft(web01).not.toHaveClass(/is-selected/);
      await expect.soft(builder).toHaveAnnounced('Selection cleared');
      await parkPointer();
    });

    await test.step('other devices say what they lack', async () => {
      await builder.node('plc-1', 'device').hover();
      await expect(tooltip).toBeVisible();
      expect.soft(await rowsOf(tooltip)).toEqual([
        ['Description', ['None']],
        ['Interfaces', ['None']],
        ['OS type', ['Not set']],
      ]);
      await parkPointer();
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('keyboard focus on the switch shows its network and its connected devices at once', async () => {
      // An arrow key on the canvas itself moves focus to the node nearest
      // the middle of the view.
      await page.locator('#builder-canvas').focus();
      await page.keyboard.press('ArrowDown');
      await expect(switchWrapper).toBeFocused();
      await expect(tooltip).toBeVisible();

      expect.soft(await rowsOf(tooltip)).toEqual([
        ['Network', ['EXP']],
        ['VLAN alias', ['100']],
        ['Description', [`Experiment network ${'y'.repeat(61)}…`]],
        [
          'Connected devices (9)',
          [
            // Both of web-01's connections to this switch.
            'web-01 — 10.0.0.1/24, DHCP',
            'web-02 — DHCP',
            'web-03 — 10.0.0.3/24',
            'web-04 — DHCP',
            'web-05 — 10.0.0.5/24',
            'web-06 — DHCP',
            'web-07 — 10.0.0.7/24',
            'web-08 — DHCP',
            '+1 more',
          ],
        ],
      ]);
      await expectBeside(page, tooltip, sw, 'a switch tooltip');

      // The same reaches assistive technology as the node's description,
      // before the canvas's keys.
      await expect
        .soft(switchWrapper)
        .toHaveAccessibleDescription(
          new RegExp(
            `^Description: Experiment network y{61}…\\. 9 connected devices: ` +
              `web-01 10\\.0\\.0\\.1/24, DHCP, web-02 DHCP, .*web-08 DHCP, ` +
              `and 1 more\\. Arrow keys move between nodes`,
          ),
        );
      await expect
        .soft(switchWrapper)
        .toHaveAccessibleName(
          'Switch EXP, network EXP, VLAN alias 100, 10 connections',
        );
    });

    await test.step('the tooltip follows its node when the view zooms', async () => {
      const before = await sw.boundingBox();

      await page.keyboard.press('=');
      await expect
        .poll(async () => (await sw.boundingBox()).width)
        .toBeGreaterThan(before.width);
      await expect.soft(tooltip).toBeVisible();
      await expectBeside(page, tooltip, sw, 'after zooming in');
      await page.keyboard.press('-');
      await expect
        .poll(async () => Math.round((await sw.boundingBox()).width))
        .toBe(Math.round(before.width));
      await expectBeside(page, tooltip, sw, 'after zooming out');
    });

    // WCAG 1.4.13: the tooltip lasts as long as the keyboard focus does.
    await test.step('the focused switch has its tooltip back once the pointer has left the devices it rested on', async () => {
      const labels = async () =>
        (await rowsOf(tooltip)).map(([label]) => label);
      const interfaces = async () => (await rowsOf(tooltip))[1]?.[1];
      const web02 = builder.node('web-02', 'device');

      // The pointer resting on a device shows that device in its place.
      await web01.hover();
      await expect.poll(labels).toContain('Interfaces');
      expect
        .soft(await interfaces())
        .toEqual(['eth0 — 10.0.0.1/24', 'eth1 — DHCP']);
      await expect.soft(switchWrapper).toBeFocused();

      // The pointer can still move onto that tooltip.
      const box = await tooltip.boundingBox();

      await page.mouse.move(box.x + 12, box.y + box.height / 2, { steps: 10 });
      await page.waitForTimeout(500);
      expect.soft(await labels()).toContain('Interfaces');

      // Straight from one device onto the next shows the next one.
      await web01.hover();
      await web02.hover();
      await expect.poll(interfaces).toEqual(['eth0 — DHCP']);

      // Off the devices, the switch has its tooltip again.
      await parkPointer();
      await expect.poll(labels).toContain('Network');
      await expectBeside(page, tooltip, sw, 'back on the focused switch');
      await expect.soft(switchWrapper).toBeFocused();
      await page.waitForTimeout(500);
      await expect.soft(tooltip).toBeVisible();
    });

    await test.step('Escape on the focused node hides the tooltip and keeps the selection', async () => {
      await page.keyboard.press('Enter');
      await expect(sw).toHaveClass(/is-selected/);
      await expect.soft(tooltip).toBeVisible();

      await page.keyboard.press('Escape');
      await expect(tooltip).toHaveCount(0);
      await expect.soft(sw).toHaveClass(/is-selected/);
      await expect.soft(switchWrapper).toBeFocused();

      await page.keyboard.press('Escape');
      await expect.soft(sw).not.toHaveClass(/is-selected/);
    });

    await test.step('an arrow key to a device shows that device, and leaving the canvas hides the tooltip', async () => {
      await page.keyboard.press('ArrowUp');
      const focused = page.locator('.vue-flow__node:focus');
      await expect(focused).toHaveAccessibleName(/^Device web-0\d, /);
      await expect(tooltip).toBeVisible();

      const rows = await rowsOf(tooltip);
      expect
        .soft(rows.map(([label]) => label))
        .toEqual(['Description', 'Interfaces', 'OS type']);
      expect.soft(rows[1][1][0]).toMatch(/^eth0 — (10\.0\.0\.\d\/24|DHCP)$/);
      await expect
        .soft(focused)
        .toHaveAccessibleDescription(
          /^Interfaces: eth0 (10\.0\.0\.\d\/24|DHCP)(, eth1 DHCP)?\. OS type linux\. /,
        );

      await page.keyboard.press('Shift+Tab');
      await expect.soft(focused).toHaveCount(0);
      await expect.soft(tooltip).toHaveCount(0);

      // Back on the canvas, its Tab stop is the node that had focus.
      await page.keyboard.press('Tab');
      await expect(page.locator('.vue-flow__node:focus')).toHaveAccessibleName(
        /^Device web-0\d, /,
      );
      await expect.soft(tooltip).toBeVisible();
      await page.keyboard.press('Shift+Tab');
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('notes and groups have no tooltip, and an SVG image of the diagram has no tooltip text', async () => {
      await builder.palette('note').click();
      await builder.palette('group').click();
      await expect(builder.nodes('note')).toHaveCount(1);
      await expect(builder.nodes('group')).toHaveCount(1);

      for (const kind of ['note', 'group']) {
        const node = builder.nodes(kind);

        await parkPointer();
        await node.hover();
        // Longer than a device's tooltip takes to show.
        await page.waitForTimeout(600);
        await expect.soft(tooltip, kind).toHaveCount(0);
        await expect
          .soft(flowNode(page, await idOf(node)), kind)
          .not.toHaveAttribute('aria-describedby', /builder-node-info-/);
      }

      // The hidden text that describes a node is beside the node, inside
      // its wrapper, so the node's own text is what it shows.
      await expect.soft(sw).not.toContainText('connected devices');
      await expect
        .soft(switchWrapper.locator('.builder-node__info'))
        .toHaveText(/9 connected devices/);

      const dialog = await builder.openDialog('download');
      const [file] = await Promise.all([
        page.waitForEvent('download'),
        dialog.getByTestId('download-svg').click(),
      ]);
      const markup = fs.readFileSync(await file.path(), 'utf8');

      expect.soft(markup).toContain('>web-01<');
      expect.soft(markup).not.toContain('builder-node__info');
      expect.soft(markup).not.toContain('connected devices');
      await dialog.getByRole('button', { name: 'Close', exact: true }).click();
    });

    expectNoFatal(issues);
  },
);

test(
  'a device and a switch take an outline and a fill, and the text on a fill stays readable',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const { draft } = await seedStyled(builder, testInfo);
    await builder.openDraft(draft);

    const web01 = builder.node('web-01', 'device');
    const exp = builder.node('EXP', 'switch');
    const outline = inspectorField(builder, 'outlineColor');
    const fill = inspectorField(builder, 'fillColor');
    const apply = builder.inspector.getByTestId('inspector-apply');
    const deviceOf = (doc) =>
      devicesOf(doc).find((node) => node.device.hostname === 'web-01').device;
    const surface = await web01.evaluate(
      (node) => getComputedStyle(node).backgroundColor,
    );

    await test.step('a device has Outline Color and Fill Color under its Icon and its Custom icon, each with its own picker', async () => {
      await builder.selectInOutline('web-01');
      await expect
        .soft(
          builder.inspector.locator('form label.label').filter({
            hasText: /^(Hostname|Icon|Custom icon|Outline Color|Fill Color)/,
          }),
        )
        .toHaveText([
          /^Hostname/,
          /^Icon/,
          /^Custom icon/,
          /^Outline Color/,
          /^Fill Color/,
        ]);
      // The pickers share one test id, and are told apart by their field.
      await expect
        .soft(builder.inspector.getByTestId('inspector-color-picker'))
        .toHaveCount(2);
      await expect
        .soft(outline.picker)
        .toHaveAccessibleName('Choose outline Color');
      await expect.soft(fill.picker).toHaveAccessibleName('Choose fill Color');
      await expect.soft(fill.picker).toHaveAccessibleDescription('No color');
      await expect
        .soft(fill.text)
        .toHaveAccessibleDescription(
          /Text and icon turn black or white to stay readable\. Applies at once, without Apply\.$/,
        );
      await expect
        .soft(outline.text)
        .toHaveAccessibleDescription(/Applies at once, without Apply\.$/);
    });

    await test.step('a fill chosen in the picker shows on the node at once, with white text on a dark fill', async () => {
      await fill.picker.click();
      await expect(fill.popup).toHaveRole('dialog');
      await expect.soft(fill.popup).toHaveAccessibleName('Fill Color picker');
      await fill.popup.getByRole('button', { name: 'Blue, #2f6fbf' }).click();

      // Without Apply: the canvas has it, and nothing is left to apply.
      await expect(web01).toHaveCSS('background-color', 'rgb(47, 111, 191)');
      await expect
        .soft(builder)
        .toHaveAnnounced('Changed the fill color of Device web-01 to #2f6fbf');
      await expect.soft(apply).toHaveCount(0);
      await expect.soft(fill.text).toHaveValue('#2f6fbf');
      await expect.soft(fill.picker).toBeFocused();
      await expect
        .soft(fill.picker)
        .toHaveAccessibleDescription('Blue, #2f6fbf');
      await expect
        .soft(fill.picker.locator('.inspector-color__chip'))
        .toHaveCSS('background-color', 'rgb(47, 111, 191)');

      // The node is selected: it keeps its fill under the selection.
      await expect.soft(web01).toHaveClass(/is-selected/);
      await expectReadable(web01, 'rgb(255, 255, 255)', 'a dark fill');
      await builder.persisted(
        draft,
        (doc) => deviceOf(doc).fillColor,
        '#2f6fbf',
        { soft: true },
      );
    });

    await test.step('a light fill typed with Enter turns the text black, and says only what changed', async () => {
      await fill.text.fill('#FFD400');
      await fill.text.press('Enter');

      await expect(web01).toHaveCSS('background-color', 'rgb(255, 212, 0)');
      await expect
        .soft(builder)
        .toHaveAnnounced('Changed the fill color of Device web-01 to #FFD400');
      await expectReadable(web01, 'rgb(0, 0, 0)', 'a light fill');
      await expect.soft(apply).toHaveCount(0);
      // Enter also asks to apply what is left, which is nothing: the color
      // was the edit, and it has been said.
      await page.waitForTimeout(300);
      await expect.soft(builder).not.toHaveAnnounced('No changes to apply.');
    });

    await test.step('a color that is no hex color is refused on its field, and the node keeps its fill', async () => {
      await fill.text.fill('red');
      await fill.text.blur();

      await expect
        .soft(fill.field)
        .toContainText('Fill Color must be a hex color, such as #2f6fbf');
      await expect.soft(fill.text).toHaveAttribute('aria-invalid', 'true');
      await expect
        .soft(web01)
        .toHaveCSS('background-color', 'rgb(255, 212, 0)');
      await expect(apply).toBeVisible();
      await expect.soft(apply).toHaveAttribute('aria-disabled', 'true');

      await builder.inspector.getByTestId('inspector-cancel').press('Enter');
      await expect.soft(fill.text).toHaveValue('#FFD400');
      await expect.soft(apply).toHaveCount(0);
      await builder.persisted(
        draft,
        (doc) => deviceOf(doc).fillColor,
        '#FFD400',
        { soft: true },
      );
    });

    await test.step('an outline colors the border, and one that fades into the canvas keeps a ring in the text color', async () => {
      // The light canvas's own color.
      await outline.text.fill('#f4f6f9');
      await outline.text.press('Enter');
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Changed the outline color of Device web-01 to #f4f6f9',
        );
      await expect(web01).toHaveClass(/builder-node--outlined/);

      // Selected, the node shows the selection's border, not its outline,
      // and its check mark; it keeps its fill.
      const accent = await themeColor(page, '--bx-accent');
      await expect.soft(web01).toHaveCSS('border-top-color', accent);
      await expect
        .soft(web01)
        .toHaveCSS('background-color', 'rgb(255, 212, 0)');
      expect
        .soft(
          await web01.evaluate(
            (node) => getComputedStyle(node, '::after').content,
          ),
        )
        .toContain('\u2713');

      // Not selected, it shows its outline, ringed where it would fade.
      await builder.selectInOutline('web-02');
      await expect(web01).not.toHaveClass(/is-selected/);
      await expect
        .soft(web01)
        .toHaveCSS('border-top-color', 'rgb(244, 246, 249)');
      const text = await themeColor(page, '--bx-text');
      await expect
        .soft(web01)
        .toHaveCSS(
          'box-shadow',
          new RegExp(`^${text.replace(/[()]/g, '\\$&')} 0px 0px 0px 1px`),
        );
    });

    await test.step('the dark theme draws the same fill and text, and no ring round an outline that shows there', async () => {
      await page.emulateMedia({ colorScheme: 'dark' });
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        'dark',
      );
      await expect
        .soft(web01)
        .toHaveCSS('background-color', 'rgb(255, 212, 0)');
      await expectReadable(web01, 'rgb(0, 0, 0)', 'a light fill, dark theme');
      await expect
        .soft(web01)
        .toHaveCSS('border-top-color', 'rgb(244, 246, 249)');
      await expect.soft(web01).not.toHaveCSS('box-shadow', /0px 0px 0px 1px/);
      await page.emulateMedia({ colorScheme: 'light' });
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        'light',
      );
    });

    // Forced colors replace the fill, the text and the outline with the
    // system's: the chosen colors are not drawn, and the text stays readable.
    if (page.context().browser()?.browserType().name() === 'chromium') {
      await test.step('forced colors draw the node in the system’s colors', async () => {
        await page.emulateMedia({ forcedColors: 'active' });
        await expect
          .poll(() =>
            page.evaluate(() => matchMedia('(forced-colors: active)').matches),
          )
          .toBe(true);
        await expect
          .soft(web01)
          .not.toHaveCSS('background-color', 'rgb(255, 212, 0)');
        await expect
          .soft(web01)
          .not.toHaveCSS('border-top-color', 'rgb(244, 246, 249)');
        const lines = await contrast(web01.locator(NODE_TEXT));

        for (const line of lines) {
          expect
            .soft(line.ratio, `forced colors: contrast of "${line.text}"`)
            .toBeGreaterThanOrEqual(4.5);
        }
        // The icon is drawn in the text's color, not the ink of the fill.
        await expect
          .soft(web01.locator('.builder-icon').first())
          .toHaveCSS('color', lines[0].color);
        await page.emulateMedia({ forcedColors: 'none' });
        await expect(web01).toHaveCSS('background-color', 'rgb(255, 212, 0)');
      });
    }

    await test.step('Undo takes the colors back one at a time', async () => {
      const undo = page.getByTestId('toolbar-undo');

      await undo.click();
      await expect(web01).not.toHaveClass(/builder-node--outlined/);
      await expect
        .soft(web01)
        .toHaveCSS('background-color', 'rgb(255, 212, 0)');
      await undo.click();
      await expect(web01).toHaveCSS('background-color', 'rgb(47, 111, 191)');
      await undo.click();
      await expect(web01).not.toHaveClass(/builder-node--filled/);
      await expect.soft(web01).toHaveCSS('background-color', surface);
      await builder.persisted(
        draft,
        (doc) =>
          ['outlineColor', 'fillColor'].filter((key) => key in deviceOf(doc)),
        [],
        { soft: true },
      );

      // And Redo gives them back.
      await page.getByTestId('toolbar-redo').click();
      await expect(web01).toHaveCSS('background-color', 'rgb(47, 111, 191)');
    });

    await test.step('a switch’s network color is Edge Color, and its own outline and fill wait for Apply', async () => {
      const edge = inspectorField(builder, 'color');
      const network = (doc) => doc.networks.find((item) => item.name === 'EXP');
      const switchOf = (doc) =>
        doc.nodes.find(
          (node) =>
            node.kind === 'switch' && node.switch.networkId === network(doc).id,
        ).switch;
      const before = network(await builder.serverDocument(draft));

      await builder.selectInOutline('EXP');
      await expect.soft(edge.label).toHaveText(/^Edge Color/);
      await expect.soft(edge.picker).toHaveAccessibleName('Choose edge Color');
      await expect
        .soft(builder.inspector.getByTestId('inspector-color-picker'))
        .toHaveCount(3);
      await expect
        .soft(builder.inspector.getByLabel('Color', { exact: true }))
        .toHaveCount(0);

      await fill.picker.click();
      await fill.popup.getByRole('button', { name: 'Olive, #6b6f18' }).click();
      await outline.text.fill('#a3273f');
      await outline.text.blur();
      // Not yet: a switch's short form applies with Apply.
      await expect.soft(exp).not.toHaveClass(/builder-node--filled/);
      await expect(apply).toBeEnabled();
      await apply.press('Enter');
      await expect.soft(builder).toHaveAnnounced('Updated network EXP');

      await expect(exp).toHaveCSS('background-color', 'rgb(107, 111, 24)');
      await expectReadable(exp, 'rgb(255, 255, 255)', 'a filled switch');
      // The swatch is still the network's color, framed in the text color.
      const swatch = exp.locator('.builder-node__swatch');
      await expect
        .soft(swatch)
        .toHaveCSS('background-color', await themeColor(page, '--bx-net-0'));
      await expect
        .soft(swatch)
        .toHaveCSS('border-top-color', 'rgb(255, 255, 255)');

      await builder.selectInOutline('web-02');
      await expect(exp).not.toHaveClass(/is-selected/);
      await expect.soft(exp).toHaveCSS('border-top-color', 'rgb(163, 39, 63)');
      // The double border that tells a switch from a device.
      await expect.soft(exp).toHaveCSS('border-top-style', 'double');
      // The colors are this switch's, not its network's: the other switch
      // is as it was.
      await expect
        .soft(builder.node('MGMT', 'switch'))
        .not.toHaveClass(/builder-node--(filled|outlined)/);

      await builder.persisted(
        draft,
        (doc) => ({ switch: switchOf(doc), network: network(doc) }),
        {
          switch: {
            networkId: before.id,
            outlineColor: '#a3273f',
            fillColor: '#6b6f18',
          },
          network: before,
        },
        { soft: true },
      );

      // One Apply is one Undo step.
      await page.getByTestId('toolbar-undo').click();
      await expect(exp).not.toHaveClass(/builder-node--(filled|outlined)/);
      await page.getByTestId('toolbar-redo').click();
      await expect(exp).toHaveClass(/builder-node--filled/);
      await builder.waitSaved();
    });

    expectNoFatal(issues);
  },
);

test('a network and a connection take a line style, a group a description, a border pattern and an icon, and downloads show them', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const { draft, edgeIds } = await seedStyled(builder, testInfo, {
    'web-02': { outlineColor: '#102030', fillColor: '#ffd400' },
    MGMT: { outlineColor: '#a3273f', fillColor: '#6b6f18' },
  });
  await builder.openDraft(draft);

  const lineStyle = inspectorField(builder, 'lineStyle');
  const apply = builder.inspector.getByTestId('inspector-apply');
  const line = (name) =>
    page.locator(
      `.vue-flow__edge[data-id="${edgeIds[name]}"] path.builder-edge`,
    );
  // The edge's label lets a click through to the line under it.
  const selectLine = async (name) => {
    const box = await page
      .locator(`.builder-edge__label[data-edge-id="${edgeIds[name]}"]`)
      .boundingBox();

    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  };
  const applyEdits = async () => {
    await expect(apply).toBeEnabled();
    await apply.press('Enter');
  };
  const PATTERNS = ['Solid', 'Dashed', 'Dotted', 'Dash-dot'];

  await test.step('Auto comes first, and names the pattern the canvas picks for the network', async () => {
    // By their places in the diagram: the first network solid, the second
    // dashed.
    await expect
      .soft(line('web-01 to EXP'))
      .toHaveAttribute('data-pattern', 'solid');
    await expect
      .soft(line('web-02 to EXP'))
      .toHaveAttribute('data-pattern', 'solid');
    await expect
      .soft(line('web-02 to MGMT'))
      .toHaveAttribute('data-pattern', 'dashed');

    await builder.selectInOutline('MGMT');
    await expect.soft(lineStyle.label).toHaveText(/^Line style/);
    expect
      .soft(await choicesOf(lineStyle.select))
      .toEqual(['Auto (Dashed)', ...PATTERNS]);
    await expect.soft(lineStyle.select).toHaveValue('');

    await builder.selectInOutline('EXP');
    expect
      .soft(await choicesOf(lineStyle.select))
      .toEqual(['Auto (Solid)', ...PATTERNS]);
    await expect.soft(lineStyle.select).toHaveValue('');
    await expect
      .soft(lineStyle.select)
      .toHaveAccessibleDescription(
        /Auto picks one by the network's place in the diagram, so networks differ without color\./,
      );
  });

  await test.step('Dotted on a network draws every line of it dotted, and leaves the other network', async () => {
    await lineStyle.select.selectOption({ label: 'Dotted' });
    await applyEdits();
    await expect.soft(builder).toHaveAnnounced('Updated network EXP');

    for (const name of ['web-01 to EXP', 'web-02 to EXP']) {
      await expect(line(name)).toHaveAttribute('data-pattern', 'dotted');
      await expect.soft(line(name)).toHaveAttribute('stroke-dasharray', '2 4');
      // Color, label and name still say which network it is.
      await expect.soft(line(name)).toHaveAttribute('data-network', 'EXP');
    }
    await expect
      .soft(line('web-02 to MGMT'))
      .toHaveAttribute('data-pattern', 'dashed');
    await expect
      .soft(
        page.locator(`.vue-flow__edge[data-id="${edgeIds['web-01 to EXP']}"]`),
      )
      .toHaveAccessibleName('Network EXP from web-01 (eth0) to EXP');
    // Auto still names what the canvas would pick.
    await expect.soft(lineStyle.select).toHaveValue('dotted');
    expect.soft((await choicesOf(lineStyle.select))[0]).toBe('Auto (Solid)');
    await builder.persisted(
      draft,
      (doc) => doc.networks.map((network) => network.lineStyle),
      ['dotted', undefined],
      { soft: true },
    );
  });

  await test.step('a connection takes a style of its own in place of its network’s, and Auto gives it back', async () => {
    await selectLine('web-01 to EXP');
    await expect
      .soft(builder.inspector.locator('form label.label'))
      .toHaveText([/^Label/, /^Color/, /^Line style/]);
    // Auto is its network's style, chosen or not.
    expect
      .soft(await choicesOf(lineStyle.select))
      .toEqual(['Auto (Dotted)', ...PATTERNS]);
    await expect
      .soft(lineStyle.select)
      .toHaveAccessibleDescription(
        /Its label and the switch it joins still name the network\./,
      );

    await lineStyle.select.selectOption({ label: 'Solid' });
    await applyEdits();
    await expect
      .soft(builder)
      .toHaveAnnounced('Updated connection from web-01 (eth0) to EXP');
    await expect(line('web-01 to EXP')).toHaveAttribute(
      'data-pattern',
      'solid',
    );
    await expect
      .soft(line('web-01 to EXP'))
      .not.toHaveAttribute('stroke-dasharray', /./);
    await expect
      .soft(line('web-02 to EXP'))
      .toHaveAttribute('data-pattern', 'dotted');
    await builder.persisted(
      draft,
      (doc) =>
        doc.edges.find((edge) => edge.id === edgeIds['web-01 to EXP'])
          .lineStyle,
      'solid',
      { soft: true },
    );

    await lineStyle.select.selectOption({ index: 0 });
    await applyEdits();
    await expect(line('web-01 to EXP')).toHaveAttribute(
      'data-pattern',
      'dotted',
    );
    await builder.persisted(
      draft,
      (doc) =>
        'lineStyle' in
        doc.edges.find((edge) => edge.id === edgeIds['web-01 to EXP']),
      false,
      { soft: true },
    );

    // Dash-dot, for the downloads below.
    await lineStyle.select.selectOption({ label: 'Dash-dot' });
    await applyEdits();
    await expect(line('web-01 to EXP')).toHaveAttribute(
      'stroke-dasharray',
      '10 4 2 4',
    );
  });

  const zone = builder.node('Zone', 'group');

  await test.step('a group shows its description under its title, in the border pattern and with the icon chosen', async () => {
    const field = (path) => inspectorField(builder, path);

    await builder.selectInOutline('Zone');
    await expect
      .soft(builder.inspector.locator('form label.label'))
      .toHaveText([
        /^Title/,
        /^Description/,
        /^Color/,
        /^Border pattern/,
        /^Icon/,
        /^Custom icon/,
      ]);
    expect
      .soft(await choicesOf(field('borderStyle').select))
      .toEqual(['Default (Dashed)', 'Solid', 'Dashed', 'Dotted', 'Double']);
    expect
      .soft((await choicesOf(field('iconKey').select))[0])
      .toBe('Default (container)');
    await expect.soft(zone).toHaveCSS('border-top-style', 'dashed');
    await expect.soft(zone.locator('.builder-icon--container')).toHaveCount(1);
    await expect.soft(zone.locator('.builder-node__comment')).toHaveCount(0);

    const description = field('description').field.locator('textarea');
    await description.fill('DMZ hosts');
    await description.blur();
    await field('borderStyle').select.selectOption({ label: 'Double' });
    await field('iconKey').select.selectOption('firewall');
    // A group's form waits for Apply.
    await expect.soft(zone).toHaveCSS('border-top-style', 'dashed');
    await applyEdits();
    await expect.soft(builder).toHaveAnnounced('Updated group Zone');

    await expect(zone).toHaveCSS('border-top-style', 'double');
    await expect.soft(zone).toHaveCSS('border-top-width', '4px');
    await expect
      .soft(zone.locator('.builder-node__comment'))
      .toHaveText('DMZ hosts');
    await expect.soft(zone.locator('.builder-icon--firewall')).toHaveCount(1);
    await expect
      .soft(builder.outlineItem('Zone').locator('.builder-icon--firewall'))
      .toHaveCount(1);
    await expect
      .soft(flowNode(page, await zone.getAttribute('data-node-id')))
      .toHaveAccessibleName('Group Zone, 0 members, comment: DMZ hosts');
    await builder.persisted(
      draft,
      (doc) => doc.nodes.find((node) => node.kind === 'group').group,
      {
        title: 'Zone',
        description: 'DMZ hosts',
        borderStyle: 'double',
        iconKey: 'firewall',
      },
      { soft: true },
    );

    for (const [label, style] of [
      ['Dotted', 'dotted'],
      ['Solid', 'solid'],
      ['Double', 'double'],
    ]) {
      await field('borderStyle').select.selectOption({ label });
      await applyEdits();
      await expect.soft(zone, label).toHaveCSS('border-top-style', style);
    }

    // A pattern is no color: forced colors keep it.
    if (page.context().browser()?.browserType().name() === 'chromium') {
      await page.emulateMedia({ forcedColors: 'active' });
      await expect
        .poll(() =>
          page.evaluate(() => matchMedia('(forced-colors: active)').matches),
        )
        .toBe(true);
      await expect.soft(zone).toHaveCSS('border-top-style', 'double');
      await expect
        .soft(line('web-02 to EXP'))
        .toHaveAttribute('stroke-dasharray', '2 4');
      await page.emulateMedia({ forcedColors: 'none' });
    }
    await builder.waitSaved();
  });

  await test.step('the SVG and PNG downloads show the colors, the line styles and the group’s border', async () => {
    const dialog = await builder.openDialog('download');
    const save = async (kind) => {
      const [file] = await Promise.all([
        page.waitForEvent('download'),
        dialog.getByTestId(`download-${kind}`).click(),
      ]);

      return fs.readFileSync(await file.path());
    };
    const svg = await save('svg');
    const markup = svg.toString('utf8');

    // The fill and outline of web-02 and of the MGMT switch, as drawn.
    expect.soft(markup).toContain('border-box rgb(255, 212, 0)');
    expect.soft(markup).toContain('border: 2px solid rgb(16, 32, 48)');
    expect.soft(markup).toContain('border-box rgb(107, 111, 24)');
    expect.soft(markup).toContain('border: 4px double rgb(163, 39, 63)');
    // The dotted network, the dash-dot connection, and the group.
    expect.soft(markup).toContain('stroke-dasharray: 2px, 4px');
    expect.soft(markup).toContain('stroke-dasharray: 10px, 4px, 2px, 4px');
    expect.soft(markup).toMatch(/data-border="double"[^>]*border: 4px double/);
    expect.soft(markup).toContain('>DMZ hosts<');

    const colors = [
      [255, 212, 0],
      [107, 111, 24],
    ];
    const png = await save('png');

    for (const [kind, buffer, type] of [
      ['SVG', svg, 'image/svg+xml'],
      ['PNG', png, 'image/png'],
    ]) {
      const [yellow, olive] = await pixelsOf(page, buffer, type, colors);

      // Each fill covers most of a node: thousands of pixels.
      expect.soft(yellow, `${kind}: the device's fill`).toBeGreaterThan(5000);
      expect.soft(olive, `${kind}: the switch's fill`).toBeGreaterThan(5000);
    }
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  });

  expectNoFatal(issues);
});

function isPNG(base64) {
  return Buffer.from(base64, 'base64').subarray(0, 8).equals(PNG_SIGNATURE);
}

// An SVG picture, in `color`, of a file that tries what a picture must not
// do: run a script as it loads, and fetch from `origin`.
function hostileSVG(color, origin) {
  return [
    '<svg xmlns="http://www.w3.org/2000/svg" width="48" height="24"',
    ' viewBox="0 0 48 24"',
    " onload=\"window.__iconRan = 'onload'; alert('onload')\">",
    "<script>window.__iconRan = 'script'; alert('script')</script>",
    `<rect width="48" height="24" fill="rgb(${color.join(', ')})"/>`,
    `<image href="${origin}/icon-probe.png" width="8" height="8"/>`,
    '</svg>',
  ].join('');
}

// The Custom icon field of the Inspector, and the Custom icons dialog it
// opens.
function iconField(page, builder) {
  const field = builder.inspector.locator('[data-path="icon"]');
  const dialog = page.getByTestId('icon-dialog');

  return {
    field,
    label: field.locator('label.label'),
    name: field.getByTestId('inspector-icon-name'),
    choose: field.getByTestId('inspector-icon-choose'),
    remove: field.getByTestId('inspector-icon-remove'),
    image: field.locator('img.builder-icon--custom'),
    dialog,
    status: dialog.getByTestId('icon-status'),
    error: dialog.getByTestId('icon-error'),
    upload: dialog.getByTestId('icon-upload'),
    file: dialog.getByTestId('icon-file'),
    // The form that names a converted image before it is uploaded.
    uploadName: dialog.getByTestId('icon-upload-name'),
    uploadSubmit: dialog.getByTestId('icon-upload-submit'),
    uploadCancel: dialog.getByTestId('icon-upload-cancel'),
    heading: dialog.getByTestId('icon-library-heading'),
    usage: dialog.getByTestId('icon-usage'),
    renameName: dialog.getByTestId('icon-rename-name'),
    renameSubmit: dialog.getByTestId('icon-rename-submit'),
    renameCancel: dialog.getByTestId('icon-rename-cancel'),
    // The row of an icon, by its name, in the list of the server's icons,
    // and in that of the copies the diagram carries.
    inLibrary: (name) =>
      dialog.getByTestId('icon-library-list').locator(`[data-icon="${name}"]`),
    inDiagram: (name) =>
      dialog.getByTestId('icon-diagram-list').locator(`[data-icon="${name}"]`),
    close: dialog.getByTestId('icon-close'),
  };
}

// Chooses `file` ({name, mimeType, buffer}) in the open Custom icons
// dialog, and uploads the image it becomes under `name`, or under the name
// the dialog proposes. Returns the POST's response.
async function uploadIcon(page, icons, file, name) {
  await icons.file.setInputFiles(file);
  await expect(icons.uploadName).toBeVisible();

  if (name !== undefined) {
    await icons.uploadName.fill(name);
  }

  const added = waitForApi(page, 'POST', '/builder/icons');

  await icons.uploadSubmit.click();

  return added;
}

// A custom icon as a node draws it.
function customIcon(scope) {
  return scope.locator('img.builder-icon--custom');
}

test(
  'a custom icon is uploaded under a name as a PNG whatever the file holds, shows on a device and a group by its name, and is renamed and deleted on the server',
  { tag: '@cross-browser' },
  async ({ page, builder, request, issues, tracker }, testInfo) => {
    const { draft } = await seedStyled(builder, testInfo);
    const names = {
      plc: iconName('plc'),
      pump: iconName('pump'),
      renamed: iconName('pump'),
    };
    const icons = iconField(page, builder);
    const web01 = builder.node('web-01', 'device');
    const zone = builder.node('Zone', 'group');
    // What the page was asked, and what it fetched, that a picture must
    // never cause.
    const prompts = [];
    const probes = [];

    page.on('dialog', (prompt) => {
      prompts.push(`${prompt.type()}: ${prompt.message()}`);
      prompt.accept().catch(() => {});
    });
    page.on('request', (sent) => {
      if (sent.url().includes('icon-probe')) {
        probes.push(sent.url());
      }
    });

    await builder.openDraft(draft);

    let plc;
    let pump;

    await test.step('the Custom icon field says None, and its button opens the Custom icons dialog', async () => {
      await builder.selectInOutline('web-01');
      await expect.soft(icons.label).toHaveText(/^Custom icon/);
      await expect.soft(icons.name).toHaveText('None');
      await expect.soft(icons.choose).toHaveText('Choose…');
      await expect
        .soft(icons.choose)
        .toHaveAccessibleName('Choose custom icon');
      await expect
        .soft(icons.choose)
        .toHaveAccessibleDescription(
          /An image of the server's icon library, drawn in place of the icon\. The node names it, so a renamed icon keeps showing\. Applies at once, without Apply\./,
        );
      await expect.soft(icons.remove).toHaveCount(0);
      await expect.soft(customIcon(web01)).toHaveCount(0);

      // With the keyboard: Enter opens the dialog, Escape closes it, and
      // focus is back on the button.
      await icons.choose.focus();
      await page.keyboard.press('Enter');
      await expect(icons.dialog).toBeVisible();
      await expect.soft(icons.dialog).toHaveAccessibleName('Custom icons');
      await expect
        .soft(icons.dialog)
        .toContainText(
          'PNG, JPEG, GIF, WebP and SVG files are converted to a PNG of at most 96 by 96 pixels.',
        );
      // Other tests may have icons in the library: its heading counts them,
      // and the usage says what this user uploaded.
      // The text is matched trimmed: the template's white space is kept as
      // a space at either end.
      await expect
        .soft(icons.heading)
        .toHaveText(/^\s*Server icons \(\d+\)\s*$/);
      await expect
        .soft(icons.usage)
        .toHaveText(
          /^\s*You uploaded \d+ of 64 icons, [\d.]+ KiB of 1 MiB\.\s*$/,
        );
      // The diagram carries no copy of an icon.
      await expect
        .soft(icons.dialog.getByTestId('icon-diagram-list'))
        .toHaveCount(0);
      await page.keyboard.press('Escape');
      await expect(icons.dialog).toHaveCount(0);
      await expect.soft(icons.choose).toBeFocused();
    });

    // The press of a click takes focus from the field being typed in, which
    // commits its value then. The error it brings shows once the click is
    // over, so the button under the pointer does not move away from it.
    await test.step('one click lands on Choose… and on a color’s picker while the Hostname above them is given a name it cannot have', async () => {
      const hostname = builder.inspector.locator(
        '[data-path="hostname"] input',
      );
      const error = builder.inspector.locator('[data-path="hostname"] p.error');
      const outline = inspectorField(builder, 'outlineColor');
      const cancel = builder.inspector.getByTestId('inspector-cancel');

      await hostname.fill('bad host');
      await expect(error).toHaveCount(0);
      await icons.choose.click();
      await expect(icons.dialog).toBeVisible();
      await expect.soft(error).toHaveText(/Hostname cannot contain spaces/);
      await page.keyboard.press('Escape');
      await expect(icons.dialog).toHaveCount(0);
      await cancel.click();
      await expect(hostname).toHaveValue('web-01');
      await expect(error).toHaveCount(0);

      await hostname.fill('bad host');
      await outline.picker.click();
      await expect(outline.popup).toBeVisible();
      await expect.soft(error).toHaveText(/Hostname cannot contain spaces/);
      await page.keyboard.press('Escape');
      await expect(outline.popup).toHaveCount(0);
      await cancel.click();
      await expect(hostname).toHaveValue('web-01');

      // A button below the form, under Apply and what it says of the
      // form's state, does not move while it is pressed either. The press
      // ends off the button, so it adds nothing.
      const add = builder.inspector.getByTestId('inspector-add-interface');
      const actions = builder.inspector.getByTestId('inspector-actions');
      const apply = builder.inspector.getByTestId('inspector-apply');

      await hostname.fill('bad host');
      await add.scrollIntoViewIfNeeded();
      await expect(actions).toContainText('Unapplied changes');

      const pressed = await add.boundingBox();

      await page.mouse.move(
        pressed.x + pressed.width / 2,
        pressed.y + pressed.height / 2,
      );
      await page.mouse.down();
      // The field has committed its value, which Apply refuses.
      await expect(apply).toHaveAttribute('aria-disabled', 'true');
      await expect.soft(actions).toContainText('Unapplied changes');
      await expect.soft(error).toHaveCount(0);
      expect.soft(await add.boundingBox()).toEqual(pressed);

      await page.mouse.move(pressed.x - 40, pressed.y - 40, { steps: 4 });
      await page.mouse.up();
      await expect(actions).toContainText('Fix the fields marked with errors');
      await expect.soft(error).toHaveText(/Hostname cannot contain spaces/);
      await expect
        .soft(builder.inspector.locator('.builder-inspector__ifaces li'))
        .toHaveCount(1);
      await cancel.click();
      await expect(hostname).toHaveValue('web-01');
    });

    await test.step('a PNG file is drawn by the browser, named, and sent as a PNG of at most 96 pixels a side', async () => {
      await icons.choose.click();
      await expect(icons.heading).toHaveText(/Server icons \(/);

      const [chooser] = await Promise.all([
        page.waitForEvent('filechooser'),
        icons.upload.click(),
      ]);

      expect.soft(chooser.isMultiple()).toBe(false);
      await expect
        .soft(icons.file)
        .toHaveAttribute(
          'accept',
          /\.png,.*\.svg,image\/png,.*image\/svg\+xml$/,
        );
      await chooser.setFiles({
        name: `${names.plc}.png`,
        mimeType: 'image/png',
        buffer: pngOf(200, 100, ownColor()),
      });

      // The name is asked for, proposed from the file's, and selected.
      await expect(icons.uploadName).toBeFocused();
      await expect.soft(icons.uploadName).toHaveValue(names.plc);
      await expect.soft(icons.uploadName).toHaveAccessibleName('Name');
      await expect
        .soft(icons.uploadName)
        .toHaveAccessibleDescription(
          /^Nodes and templates name the icon by it\./,
        );

      // A name that breaks the rule is refused before anything is sent.
      await icons.uploadName.fill('plc icon');
      await icons.uploadSubmit.click();
      await expect(icons.error).toContainText('Enter a name for the icon.');
      await expect
        .soft(icons.uploadName)
        .toHaveAttribute('aria-invalid', 'true');
      await expect.soft(icons.uploadName).toBeFocused();

      await icons.uploadName.fill(names.plc);

      const added = waitForApi(page, 'POST', '/builder/icons');

      await icons.uploadSubmit.click();

      const response = await added;
      const sent = response.request().postDataJSON();

      expect(response.status()).toBe(201);
      plc = await response.json();
      // Only a name and a PNG are sent: nothing of the file's own.
      expect.soft(Object.keys(sent).sort()).toEqual(['data', 'name']);
      expect.soft(sent.name).toBe(names.plc);
      expect.soft(plc.name).toBe(names.plc);
      expect.soft(isPNG(sent.data), 'what is sent is a PNG').toBe(true);
      expect.soft(isPNG(plc.data), 'what is stored is a PNG').toBe(true);
      // 200 by 100 pixels, scaled to fit inside 96.
      expect.soft([plc.width, plc.height]).toEqual([96, 48]);
      expect.soft(plc.id).toBe(iconOf(Buffer.from(plc.data, 'base64')).id);
      expect.soft([plc.canRename, plc.canDelete]).toEqual([true, true]);

      await expect(icons.status).toHaveText(
        `Added ${names.plc} to the server.`,
      );
      await expect.soft(icons.uploadName).toHaveCount(0);
      const row = icons.inLibrary(names.plc);

      await expect(row).toHaveCount(1);
      await expect.soft(row).toContainText(names.plc);
      await expect
        .soft(row)
        .toContainText(/Uploaded by \S+ · 96 × 48 pixels, [\d.]+ KiB/);
      await expect
        .soft(customIcon(row))
        .toHaveAttribute('src', `data:image/png;base64,${plc.data}`);
      // Focus is on the new icon's Use.
      await expect
        .soft(row.getByRole('button', { name: `Use ${names.plc}` }))
        .toBeFocused();

      // The same picture again, under its name, is the icon the server has.
      const again = await uploadIcon(page, icons, {
        name: `${names.plc}.png`,
        mimeType: 'image/png',
        buffer: Buffer.from(sent.data, 'base64'),
      });

      expect.soft(again.status()).toBe(200);
      await expect
        .soft(icons.status)
        .toHaveText(`The server already has this icon as ${names.plc}.`);
      await expect.soft(icons.inLibrary(names.plc)).toHaveCount(1);

      // Another picture under a name the server has is refused, and says
      // who has the name; the form stays for another name. The name is
      // taken whatever its case.
      const taken = await uploadIcon(
        page,
        icons,
        {
          name: 'other.png',
          mimeType: 'image/png',
          buffer: pngOf(8, 8, ownColor()),
        },
        names.plc.toUpperCase(),
      );

      expect.soft(taken.status()).toBe(409);
      await expect(icons.error).toHaveText(
        new RegExp(
          `^Icon name "${names.plc.toUpperCase()}" is taken by an icon \\S+ uploaded; choose another name\\.$`,
        ),
      );
      await expect.soft(icons.uploadName).toBeFocused();
      await icons.uploadCancel.click();
      await expect(icons.uploadName).toHaveCount(0);
      await expect.soft(icons.upload).toBeFocused();
    });

    await test.step('an SVG file with a script in it becomes a PNG too: nothing in it runs, and nothing is fetched for it', async () => {
      const color = ownColor();
      // The name proposed from the file's is the one sent.
      const response = await uploadIcon(page, icons, {
        name: `${names.pump}.svg`,
        mimeType: 'image/svg+xml',
        buffer: Buffer.from(hostileSVG(color, new URL(page.url()).origin)),
      });
      const sent = response.request().postDataJSON();

      expect(response.status()).toBe(201);
      pump = await response.json();
      expect.soft(sent.name).toBe(names.pump);
      expect.soft(isPNG(sent.data), 'what is sent is a PNG').toBe(true);
      expect.soft(isPNG(pump.data), 'what is stored is a PNG').toBe(true);
      for (const data of [sent.data, pump.data]) {
        expect
          .soft(Buffer.from(data, 'base64').toString('latin1'))
          .not.toMatch(/<svg|script|onload|icon-probe/i);
      }
      // A vector image is drawn as large as an icon may be.
      expect.soft([pump.width, pump.height]).toEqual([96, 48]);
      await expect(icons.status).toHaveText(
        `Added ${names.pump} to the server.`,
      );

      // The picture is the file's: its rectangle, in the test's color.
      const [painted] = await pixelsOf(
        page,
        Buffer.from(pump.data, 'base64'),
        'image/png',
        [color],
      );

      expect.soft(painted, 'pixels of the rectangle').toBeGreaterThan(4000);

      expect.soft(prompts, 'dialogs the picture opened').toEqual([]);
      expect.soft(probes, 'requests the picture made').toEqual([]);
      expect.soft(await page.evaluate(() => window.__iconRan)).toBeUndefined();

      // The library lists both as PNGs, as JSON a browser will not take
      // for anything else.
      const list = await request.get(`${API}/builder/icons`);
      const listed = (await list.json()).icons;

      expect.soft(list.headers()['content-type']).toMatch(/^application\/json/);
      expect.soft(list.headers()['x-content-type-options']).toBe('nosniff');
      expect
        .soft(list.headers()['content-security-policy'])
        .toBe("default-src 'none'; frame-ancestors 'none'");
      for (const icon of [plc, pump]) {
        const found = listed.find((entry) => entry.name === icon.name);

        expect.soft(found?.data, `${icon.name} as listed`).toBe(icon.data);
        expect.soft(found?.id, `${icon.name} as listed`).toBe(icon.id);
        expect.soft(isPNG(found?.data || '')).toBe(true);
      }
      expect
        .soft([...tracker.icons].sort())
        .toEqual([names.plc, names.pump].sort());
    });

    const src = () => `data:image/png;base64,${pump.data}`;

    await test.step('Use gives the device the icon at once: on the canvas, in the outline and in the Inspector', async () => {
      await icons
        .inLibrary(names.pump)
        .getByRole('button', { name: `Use ${names.pump}` })
        .click();
      await expect(icons.dialog).toHaveCount(0);
      await expect.soft(icons.choose).toBeFocused();
      await expect
        .soft(builder)
        .toHaveAnnounced(
          `Changed the custom icon of Device web-01 to ${names.pump}`,
        );

      // An image of the PNG, in place of the built-in icon.
      await expect(customIcon(web01)).toHaveAttribute('src', src());
      await expect.soft(customIcon(web01)).toHaveAttribute('alt', '');
      await expect.soft(customIcon(web01)).toHaveJSProperty('naturalWidth', 96);
      await expect.soft(web01.locator('svg.builder-icon')).toHaveCount(0);
      const box = await customIcon(web01).boundingBox();

      expect.soft([box.width, box.height]).toEqual([16, 16]);
      await expect
        .soft(customIcon(builder.outlineItem('web-01')))
        .toHaveAttribute('src', src());
      // The node is named as before: its icon is decoration.
      await expect
        .soft(flowNode(page, await web01.getAttribute('data-node-id')))
        .toHaveAccessibleName(/^Device web-01, /);

      await expect.soft(icons.name).toHaveText(names.pump);
      await expect.soft(icons.image).toHaveAttribute('src', src());
      await expect.soft(icons.choose).toHaveText('Change…');
      await expect
        .soft(icons.choose)
        .toHaveAccessibleName('Change custom icon');
      await expect
        .soft(icons.remove)
        .toHaveAccessibleName('Remove custom icon');
      // No Apply is left to press.
      await expect
        .soft(builder.inspector.getByTestId('inspector-apply'))
        .toHaveCount(0);

      // The device names the icon, and the draft carries no image of it.
      await builder.waitSaved();
      await builder.persisted(
        draft,
        (doc) => ({
          icons: 'icons' in doc,
          icon: devicesOf(doc).find((node) => node.device.hostname === 'web-01')
            .device.icon,
        }),
        { icons: false, icon: names.pump },
      );
      expect
        .soft(JSON.stringify(await builder.serverDocument(draft)))
        .not.toMatch(/<svg|<script|onload|icon-probe/i);
    });

    await test.step('a group takes an icon with Apply, and the diagram still carries no copy', async () => {
      await builder.selectInOutline('Zone');
      await expect.soft(icons.name).toHaveText('None');
      await expect
        .soft(icons.choose)
        .toHaveAccessibleDescription(
          /The node names it, so a renamed icon keeps showing\.$/,
        );
      await icons.choose.click();
      await expect(icons.heading).toHaveText(/Server icons \(/);
      await expect
        .soft(icons.dialog.getByTestId('icon-diagram-list'))
        .toHaveCount(0);

      // The filter finds an icon by its name.
      const filter = icons.dialog.getByTestId('icon-filter');

      await filter.fill(names.plc);
      await expect.soft(icons.inLibrary(names.pump)).toHaveCount(0);
      await expect
        .soft(icons.dialog.locator('#icon-filter-count'))
        .toHaveText(/^\s*1 of \d+ icons shown\.\s*$/);
      await icons.inLibrary(names.plc).getByTestId('icon-use').click();
      await expect(icons.dialog).toHaveCount(0);
      // A group's form waits for Apply: the field shows the choice, the
      // canvas not yet.
      await expect.soft(icons.name).toHaveText(names.plc);
      await expect
        .soft(icons.image)
        .toHaveAttribute('src', `data:image/png;base64,${plc.data}`);
      await expect.soft(customIcon(zone)).toHaveCount(0);

      const apply = builder.inspector.getByTestId('inspector-apply');

      await expect(apply).toBeEnabled();
      await apply.click();
      await expect.soft(builder).toHaveAnnounced('Updated group Zone');
      await expect(customIcon(zone)).toHaveAttribute(
        'src',
        `data:image/png;base64,${plc.data}`,
      );
      await expect.soft(customIcon(builder.outlineItem('Zone'))).toHaveCount(1);
      await builder.waitSaved();
      await builder.persisted(
        draft,
        (doc) => [
          'icons' in doc,
          doc.nodes.find((node) => node.kind === 'group').group.icon,
        ],
        [false, names.plc],
      );
    });

    await test.step('Rename gives the icon a new name, and the device that names the old one keeps showing it', async () => {
      await builder.selectInOutline('web-01');
      await icons.choose.click();
      const row = icons.inLibrary(names.pump);
      const rename = row.getByRole('button', { name: `Rename ${names.pump}` });

      await rename.click();
      await expect(icons.renameName).toBeFocused();
      await expect.soft(icons.renameName).toHaveValue(names.pump);
      await expect
        .soft(icons.renameName)
        .toHaveAccessibleDescription(
          new RegExp(`^The old name, ${names.pump}, keeps working`),
        );
      // Cancel keeps the name, and focus goes back to Rename.
      await icons.renameCancel.click();
      await expect(icons.renameName).toHaveCount(0);
      await expect.soft(rename).toBeFocused();

      await rename.click();
      await icons.renameName.fill(names.renamed);

      const renamed = waitForApi(page, 'PUT', `/builder/icons/${names.pump}`);

      await icons.renameSubmit.click();

      const response = await renamed;

      expect(response.status()).toBe(200);
      expect.soft(response.request().postDataJSON()).toEqual({
        name: names.renamed,
      });
      expect.soft((await response.json()).aliases).toEqual([names.pump]);
      await expect(icons.status).toHaveText(
        `Renamed ${names.pump} to ${names.renamed}. ${names.pump} keeps working as another name of it.`,
      );

      const moved = icons.inLibrary(names.renamed);

      await expect(moved).toHaveCount(1);
      await expect.soft(icons.inLibrary(names.pump)).toHaveCount(0);
      await expect.soft(moved).toContainText(`also named ${names.pump}`);
      await expect
        .soft(moved.getByRole('button', { name: `Rename ${names.renamed}` }))
        .toBeFocused();
      await icons.close.click();
      await expect(icons.dialog).toHaveCount(0);

      // The device names the old name, which still resolves.
      await expect(customIcon(web01)).toHaveAttribute('src', src());
      await expect.soft(icons.name).toHaveText(names.pump);
      await expect.soft(icons.image).toHaveAttribute('src', src());
    });

    await test.step('Delete asks first, and the group that names the icon shows its built-in icon', async () => {
      await builder.selectInOutline('Zone');
      await icons.choose.click();
      const row = icons.inLibrary(names.plc);
      const confirm = page.getByTestId('builder-confirm');

      await row
        .getByRole('button', { name: `Delete ${names.plc} from the server` })
        .click();
      await expect(confirm).toBeVisible();
      await expect.soft(confirm).toHaveAccessibleName('Delete icon?');
      await expect
        .soft(confirm)
        .toContainText(
          `Delete ${names.plc} from the server? Diagrams and templates that use it, by any of its names, will show their built-in icon instead.`,
        );
      // Cancel is where focus starts, and keeps the icon.
      await expect.soft(confirm.getByTestId('confirm-cancel')).toBeFocused();
      await confirm.getByTestId('confirm-cancel').click();
      await expect(confirm).toHaveCount(0);
      await expect.soft(row).toHaveCount(1);

      await row.getByTestId('icon-delete').click();
      const deleted = waitForApi(page, 'DELETE', `/builder/icons/${names.plc}`);

      await confirm.getByRole('button', { name: 'Delete' }).click();
      expect((await deleted).status()).toBe(204);
      await expect(icons.status).toHaveText(
        `Deleted ${names.plc} from the server.`,
      );
      await expect(row).toHaveCount(0);
      await expect.soft(icons.upload).toBeFocused();
      await icons.close.click();
      await expect(icons.dialog).toHaveCount(0);

      // The group keeps the name; nothing resolves it now.
      await expect(customIcon(zone)).toHaveCount(0);
      await expect
        .soft(zone.locator('.builder-icon--container'))
        .toHaveCount(1);
      await expect
        .soft(icons.name)
        .toHaveText(`${names.plc} (not found: the built-in icon is shown)`);
      await expect.soft(icons.image).toHaveCount(0);
      await expect(customIcon(web01)).toHaveAttribute('src', src());
    });

    await test.step('after a new page load the diagram reads its icons from the server again', async () => {
      await builder.waitSaved();
      await builder.openDraft(draft);

      const listed = (await (await request.get(`${API}/builder/icons`)).json())
        .icons;

      expect.soft(listed.map((icon) => icon.name)).not.toContain(names.plc);
      // The old name finds the renamed icon, ignoring case too.
      const old = await request.get(
        `${API}/builder/icons/${names.pump.toUpperCase()}`,
      );

      expect.soft(old.status()).toBe(200);
      expect.soft((await old.json()).name).toBe(names.renamed);
      await expect(customIcon(web01)).toHaveAttribute('src', src());
      await expect.soft(customIcon(web01)).toHaveJSProperty('naturalWidth', 96);
      await expect.soft(customIcon(zone)).toHaveCount(0);
      await builder.selectInOutline('web-01');
      await expect.soft(icons.name).toHaveText(names.pump);
    });

    expect.soft(prompts, 'dialogs opened during the test').toEqual([]);
    expect.soft(probes, 'requests a picture made').toEqual([]);
    expectNoFatal(issues);
  },
);

// Seeds a draft of styledDocument() whose web-01 device and Zone group use
// `icon`, a copy of which the document carries: the server has no icon of
// its name.
async function seedWithIcon(builder, testInfo, icon) {
  const { edgeIds: _, ...document } = styledDocument(
    `icons-${testInfo.project.name}-${Date.now()}`,
    { 'web-01': { icon: icon.name } },
  );

  document.nodes.find((node) => node.kind === 'group').group.icon = icon.name;

  return builder.seedDraft({
    ...document,
    icons: { [icon.name]: icon.entry },
  });
}

test('a copy of a custom icon a diagram carries is removed and undone, copied with its node and drawn in the downloads, and a file that is no picture is refused', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const color = ownColor();
  const plc = iconOf(pngOf(32, 16, color), iconName('plc'));
  const src = `data:image/png;base64,${plc.data}`;
  const draft = await seedWithIcon(builder, testInfo, plc);
  const icons = iconField(page, builder);
  const web01 = builder.node('web-01', 'device');
  const zone = builder.node('Zone', 'group');
  // The requests that change the icon library: none here adds an icon.
  const changes = [];

  page.on('request', (sent) => {
    const { pathname } = new URL(sent.url());

    if (pathname.includes('/builder/icons') && sent.method() !== 'GET') {
      changes.push(`${sent.method()} ${pathname}`);
    }
  });

  await builder.openDraft(draft);

  await test.step('the diagram shows the copy it carries, of an icon the server lacks', async () => {
    await expect(customIcon(web01)).toHaveAttribute('src', src);
    await expect.soft(customIcon(zone)).toHaveAttribute('src', src);
    await expect.soft(customIcon(builder.outlineItem('Zone'))).toHaveCount(1);
    // The other device keeps the icon of its key.
    await expect
      .soft(customIcon(builder.node('web-02', 'device')))
      .toHaveCount(0);
    await builder.selectInOutline('web-01');
    await expect.soft(icons.name).toHaveText(plc.name);
    await expect.soft(icons.image).toHaveAttribute('src', src);
  });

  await test.step('Remove takes the icon off the device at once; the diagram keeps it while the group uses it, and drops it after', async () => {
    await icons.remove.click();
    await expect
      .soft(builder)
      .toHaveAnnounced('Removed the custom icon of Device web-01');
    await expect(customIcon(web01)).toHaveCount(0);
    await expect.soft(web01.locator('.builder-icon--linux')).toHaveCount(1);
    await expect.soft(icons.name).toHaveText('None');
    // Remove went with the icon: focus is on the button that stays.
    await expect.soft(icons.choose).toBeFocused();
    await expect.soft(icons.choose).toHaveText('Choose…');
    await builder.waitSaved();
    await builder.persisted(
      draft,
      (doc) => [
        doc.icons,
        'icon' in
          devicesOf(doc).find((node) => node.device.hostname === 'web-01')
            .device,
      ],
      [{ [plc.name]: plc.entry }, false],
    );

    await builder.selectInOutline('Zone');
    await icons.remove.click();
    // A group's form waits for Apply.
    await expect.soft(customIcon(zone)).toHaveCount(1);
    await builder.inspector.getByTestId('inspector-apply').click();
    await expect.soft(builder).toHaveAnnounced('Updated group Zone');
    await expect(customIcon(zone)).toHaveCount(0);
    await expect.soft(zone.locator('.builder-icon--container')).toHaveCount(1);
    await builder.waitSaved();
    await builder.persisted(draft, (doc) => 'icons' in doc, false);
  });

  await test.step('Undo puts the icons back, and the diagram carries the image again', async () => {
    const undo = page.getByTestId('toolbar-undo');

    await undo.click();
    await expect(customIcon(zone)).toHaveAttribute('src', src);
    await undo.click();
    await expect
      .soft(builder)
      .toHaveAnnounced('Undid Removed the custom icon of Device web-01');
    await expect(customIcon(web01)).toHaveAttribute('src', src);
    await builder.waitSaved();
    await builder.persisted(draft, (doc) => doc.icons, {
      [plc.name]: plc.entry,
    });
  });

  await test.step('a copy of the device has the icon too, and the diagram still one image of it', async () => {
    await builder.selectInOutline('web-01');
    await builder.toolbar('copy').click();
    await builder.toolbar('paste').click();
    await expect.soft(builder).toHaveAnnounced('Pasted 1 node');
    await expect(customIcon(builder.nodes('device'))).toHaveCount(2);
    await builder.waitSaved();
    await builder.persisted(
      draft,
      (doc) => [
        Object.keys(doc.icons),
        devicesOf(doc).filter((node) => node.device.icon === plc.name).length,
      ],
      [[plc.name], 2],
    );
  });

  await test.step('the SVG and PNG downloads show the icon, as the image it is', async () => {
    const dialog = await builder.openDialog('download');
    const save = async (kind) => {
      const [file] = await Promise.all([
        page.waitForEvent('download'),
        dialog.getByTestId(`download-${kind}`).click(),
      ]);

      return fs.readFileSync(await file.path());
    };
    const svg = await save('svg');
    const markup = svg.toString('utf8');

    expect.soft(markup).toContain(src);
    expect.soft(markup).not.toMatch(/<script/i);

    const png = await save('png');

    for (const [kind, buffer, type] of [
      ['SVG', svg, 'image/svg+xml'],
      ['PNG', png, 'image/png'],
    ]) {
      const [painted] = await pixelsOf(page, buffer, type, [color]);

      // Three nodes draw it, 16 by 8 pixels each before the image's zoom.
      expect.soft(painted, `${kind}: the icon's color`).toBeGreaterThan(200);
    }
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  });

  await test.step('what cannot be made an icon is said in the dialog, and nothing is added', async () => {
    await builder.selectInOutline('web-01');
    await icons.choose.click();
    await expect(icons.heading).toHaveText(/Server icons \(/);
    await expect
      .soft(icons.dialog.getByRole('heading', { name: 'In this diagram (1)' }))
      .toBeVisible();
    await expect
      .soft(icons.inDiagram(plc.name))
      .toContainText('32 × 16 pixels, 0.1 KiB');
    // The server lacks the diagram's icon: it can be added there.
    await expect
      .soft(icons.inDiagram(plc.name).getByTestId('icon-add-to-server'))
      .toHaveAccessibleName(`Add ${plc.name} to the server`);
    await expect.soft(icons.inLibrary(plc.name)).toHaveCount(0);

    await icons.file.setInputFiles({
      name: 'notes.png',
      mimeType: 'image/png',
      buffer: Buffer.from('not a picture'),
    });
    await expect(icons.error).toHaveText(
      'This file is not an image the browser can read. Use a PNG, JPEG, GIF, WebP or SVG file.',
    );

    await icons.file.setInputFiles({
      name: 'large.png',
      mimeType: 'image/png',
      buffer: Buffer.alloc(5 * 1024 * 1024 + 1),
    });
    await expect(icons.error).toHaveText(
      'The image file is larger than 5 MiB.',
    );
    await expect.soft(icons.uploadName).toHaveCount(0);

    // What the server says of an icon it refuses is shown as a sentence,
    // and the form stays.
    const refuse = (route) =>
      route.request().method() === 'POST'
        ? route.fulfill({
            status: 413,
            json: {
              message:
                'icon library is full for you: each user may upload at most 64 icons',
              cause: 'builder: too large: icon library',
            },
          })
        : route.fallback();

    await page.route('**/api/v1/builder/icons', refuse);
    await uploadIcon(
      page,
      icons,
      {
        name: 'one-more.png',
        mimeType: 'image/png',
        buffer: pngOf(8, 8, ownColor()),
      },
      iconName('one-more'),
    );
    await expect(icons.error).toHaveText(
      'Icon library is full for you: each user may upload at most 64 icons.',
    );
    await expect.soft(icons.status).toHaveCount(0);
    await expect.soft(icons.uploadName).toBeVisible();
    await expect.soft(icons.upload).toHaveCount(0);
    await page.unroute('**/api/v1/builder/icons', refuse);
    await icons.uploadCancel.click();
    await expect.soft(icons.upload).toBeFocused();
    await icons.close.click();
    expect
      .soft(changes, 'changes of the icon library')
      .toEqual([expect.stringMatching(/^POST .*\/builder\/icons$/)]);
  });

  await test.step('a library that cannot be read says why, the diagram still shows its copy, and Retry reads it', async () => {
    const fail = (route) =>
      route.request().method() === 'GET'
        ? route.fulfill({
            status: 500,
            json: { message: 'unable to list the icons' },
          })
        : route.fallback();

    // A new page load reads the library from the start.
    await builder.waitSaved();
    await page.route('**/api/v1/builder/icons', fail);
    await builder.openDraft(draft);
    await expect(customIcon(web01)).toHaveAttribute('src', src);
    await builder.selectInOutline('web-01');
    await icons.choose.click();
    await expect(
      icons.dialog.getByRole('alert').filter({ hasText: 'Unable' }),
    ).toHaveText('Unable to list the icons.');
    await expect.soft(icons.heading).toHaveText('Server icons (0)');
    // The diagram's copies need no library, but cannot be added to it.
    await expect
      .soft(icons.inDiagram(plc.name).getByTestId('icon-use'))
      .toBeVisible();
    await expect
      .soft(icons.inDiagram(plc.name).getByTestId('icon-add-to-server'))
      .toHaveCount(0);

    await page.unroute('**/api/v1/builder/icons', fail);
    await icons.dialog.getByTestId('icon-retry').click();
    await expect(icons.heading).toHaveText(/^\s*Server icons \(\d+\)\s*$/);
    await expect
      .soft(icons.inDiagram(plc.name).getByTestId('icon-add-to-server'))
      .toBeVisible();
    await icons.close.click();
  });

  expectNoFatal(issues);
});

// A device for each of `icons`, in rows of eight, each with its icon, a copy
// of which the document carries: as many copies as a diagram holds, when
// there are MAX_DOCUMENT_ICONS.
function iconsDocument(name, icons) {
  return {
    ...blankDocument(name, {
      nodes: icons.map((icon, index) => {
        const hostname = `plc-${String(index).padStart(2, '0')}`;

        return {
          id: crypto.randomUUID(),
          kind: 'device',
          label: hostname,
          position: { x: 48 + (index % 8) * 192, y: 48 + (index >> 3) * 128 },
          device: {
            hostname,
            iconKey: 'linux',
            icon: icon.name,
            spec: {
              type: 'VirtualMachine',
              general: { hostname, vm_type: 'kvm' },
              hardware: {
                os_type: 'linux',
                drives: [{ image: 'ubuntu.qc2' }],
              },
              network: { interfaces: [] },
            },
            interfaces: [],
          },
        };
      }),
    }),
    icons: Object.fromEntries(icons.map((icon) => [icon.name, icon.entry])),
  };
}

// Custom icons are images in their own colors, the same in both themes, on
// nodes, outline rows and the rows of the Custom icons dialog. The diagram
// here is full: it carries as many copies as a document holds.
for (const scheme of ['light', 'dark']) {
  test(
    `axe finds no serious violations in the Custom icons dialog and on a canvas of custom icons in the ${scheme} theme`,
    { tag: '@axe' },
    async ({ page, builder, issues }, testInfo) => {
      await page.emulateMedia({ colorScheme: scheme });

      const icons = Array.from({ length: MAX_DOCUMENT_ICONS }, (_, index) =>
        iconOf(
          pngOf(4, 4, ownColor()),
          `icon-${String(index).padStart(2, '0')}`,
        ),
      );
      const spare = iconOf(pngOf(4, 4, ownColor()), 'spare');
      // The library the dialog reads: one icon the diagram has a copy of,
      // with the same bytes, and one it has not, which another name also
      // names. No test's icons are touched.
      const listed = [icons[0], spare].map((icon) => ({
        name: icon.name,
        id: icon.id,
        owner: 'alice',
        width: 4,
        height: 4,
        bytes: Buffer.from(icon.data, 'base64').length,
        created: '2026-10-02T12:00:00Z',
        updated: '2026-10-02T12:00:00Z',
        aliases: icon === spare ? ['spare-old'] : [],
        data: icon.data,
        canRename: true,
        canDelete: true,
      }));

      await page.route('**/api/v1/builder/icons', (route) =>
        route.fulfill({
          json: {
            icons: listed,
            maxIcons: 64,
            maxBytes: 1048576,
            usedIcons: listed.length,
            usedBytes: listed.reduce((sum, icon) => sum + icon.bytes, 0),
          },
        }),
      );

      const draft = await builder.seedDraft(
        iconsDocument(
          `icons-${scheme}-${testInfo.project.name}-${Date.now()}`,
          icons,
        ),
      );
      const field = iconField(page, builder);

      await builder.openDraft(draft);
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        scheme,
      );
      await expect(customIcon(builder.nodes('device'))).toHaveCount(
        MAX_DOCUMENT_ICONS,
      );
      await expect
        .soft(customIcon(page.getByTestId('builder-outline')))
        .toHaveCount(MAX_DOCUMENT_ICONS);

      await builder.selectInOutline('plc-01');
      await expect(field.name).toHaveText('icon-01');
      await expectAccessible(page, {
        soft: true,
        label: `axe on a canvas of custom icons and the Custom icon field (${scheme})`,
      });

      await field.choose.click();
      await expect(
        field.dialog.getByRole('heading', {
          name: `In this diagram (${MAX_DOCUMENT_ICONS})`,
        }),
      ).toBeVisible();
      await expect(field.heading).toHaveText('Server icons (2)');
      await expect(field.usage).toHaveText(
        /^\s*You uploaded 2 of 64 icons, [\d.]+ KiB of 1 MiB\.\s*$/,
      );
      await expect
        .soft(field.inDiagram(icons[0].name))
        .toContainText('On the server');
      // Every copy of the diagram but the one the server holds as it is can
      // be added to the server.
      await expect
        .soft(field.dialog.getByTestId('icon-add-to-server'))
        .toHaveCount(MAX_DOCUMENT_ICONS - 1);
      await expect
        .soft(field.inLibrary(spare.name))
        .toContainText('Uploaded by alice · 4 × 4 pixels');
      await expect
        .soft(field.inLibrary(spare.name))
        .toContainText('also named spare-old');

      // The rename form, open in the dialog.
      const rename = field.inLibrary(spare.name).getByTestId('icon-rename');

      await rename.click();
      await expect(field.renameName).toBeFocused();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the Custom icons dialog, renaming an icon (${scheme})`,
      });
      await field.renameCancel.click();
      await expect(field.renameName).toHaveCount(0);
      await expect.soft(rename).toBeFocused();

      await field.inLibrary(spare.name).getByTestId('icon-delete').click();
      const confirm = page.getByTestId('builder-confirm');

      await expect(confirm).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on Delete icon? (${scheme})`,
      });
      await confirm.getByTestId('confirm-cancel').click();
      await expect(confirm).toHaveCount(0);
      // Focus is back on the button that asked.
      await expect
        .soft(field.inLibrary(spare.name).getByTestId('icon-delete'))
        .toBeFocused();

      // A copy the diagram carries can be given to another node.
      await field.inDiagram(icons[0].name).getByTestId('icon-use').click();
      await expect(field.dialog).toHaveCount(0);
      await expect
        .soft(builder)
        .toHaveAnnounced('Changed the custom icon of Device plc-01 to icon-00');
      await expect.soft(field.name).toHaveText('icon-00');
      // The copy plc-01 used is no longer used, and the server holds
      // icon-00 as it is: both leave the diagram, which keeps two copies
      // fewer than it holds.
      await builder.waitSaved();
      await builder.persisted(
        draft,
        (doc) => [
          Object.keys(doc.icons).length,
          icons[0].name in doc.icons,
          icons[1].name in doc.icons,
        ],
        [MAX_DOCUMENT_ICONS - 2, false, false],
        { soft: true },
      );

      expectNoFatal(issues);
    },
  );
}

// Text on a fill is black or white, whichever reads on it, in either theme:
// the fills here are a dark one, a light one, and the grey on which the two
// read about equally, the worst case (4.6:1).
for (const scheme of ['light', 'dark']) {
  test(
    `axe finds no serious violations on a canvas of filled and outlined nodes in the ${scheme} theme`,
    { tag: '@axe' },
    async ({ page, builder, issues }, testInfo) => {
      await page.emulateMedia({ colorScheme: scheme });
      const { draft } = await seedStyled(builder, testInfo, {
        'web-01': { fillColor: '#1b2330', outlineColor: '#ffd400' },
        'web-02': { fillColor: '#777777' },
        EXP: { fillColor: '#ffd400', outlineColor: '#102030' },
        MGMT: { fillColor: '#2f6fbf', outlineColor: '#f4f6f9' },
      });
      await builder.openDraft(draft);
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        scheme,
      );

      const inks = {
        'web-01': 'rgb(255, 255, 255)',
        'web-02': 'rgb(0, 0, 0)',
        EXP: 'rgb(0, 0, 0)',
        MGMT: 'rgb(255, 255, 255)',
      };
      const nodeOf = (label) =>
        builder.node(label, label.startsWith('web') ? 'device' : 'switch');

      await expect(
        builder.nodes().and(page.locator('.builder-node--filled')),
      ).toHaveCount(4);
      for (const [label, ink] of Object.entries(inks)) {
        await expectReadable(nodeOf(label), ink, `${scheme}: ${label}`);
      }
      await expectAccessible(page, {
        soft: true,
        label: `axe on filled nodes (${scheme})`,
      });

      // Selected, a node keeps its fill and its text color; the Inspector
      // shows its color fields, with their pickers.
      await builder.selectInOutline('web-02');
      await expect(nodeOf('web-02')).toHaveClass(/is-selected/);
      await expectReadable(
        nodeOf('web-02'),
        inks['web-02'],
        `${scheme}: web-02 selected`,
      );
      await builder.selectInOutline('MGMT');
      await inspectorField(builder, 'fillColor').picker.click();
      await expect(inspectorField(builder, 'fillColor').popup).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on a selected filled switch and its color picker (${scheme})`,
      });
      await page.keyboard.press('Escape');

      expectNoFatal(issues);
    },
  );
}
