// How the canvas presents nodes: types, info tooltips, template devices,
// colors, line styles, groups and custom icons (the icon library is shared).

const crypto = require('crypto');
const fs = require('fs');

const {
  API,
  PNG_SIGNATURE,
  blankDocument,
  contrast,
  devicesOf,
  expect,
  expectAccessible,
  expectNoFatal,
  flowNode,
  iconName,
  iconOf,
  iconPath,
  ownColor,
  pngOf,
  routeIconLibrary,
  seedIcon,
  specGroup,
  test,
  waitForApi,
} = require('./builder-support');

// How many custom icons one document may carry: MAX_DOCUMENT_ICONS in
// src/builder/icons.js and MaxDocumentIcons in customicons.go.
const MAX_DOCUMENT_ICONS = 50;

// The live region's hold per message, in ms (ANNOUNCE_HOLD_MS in
// src/builder/announce.js).
const ANNOUNCE_HOLD_MS = 750;

// Longer than a node's tooltip waits to show (400 ms, see BuilderCanvas.vue)
// and than several of its 200 ms waits to hide (see fixedTooltip.js).
const TIP_RUN_MS = 1000;

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

// The Inspector's field for a data path, within which its color picker (one
// test id for all) is found.
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

// The outline color a device or a switch node is drawn with, as the browser
// computes it, also while its selection border hides it.
function outlineOf(node) {
  return node.evaluate((element) => {
    const probe = document.createElement('span');

    probe.style.color = 'var(--bx-node-outline)';
    element.append(probe);
    const { color } = getComputedStyle(probe);
    probe.remove();

    return color;
  });
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

// Every line of text of `node` is in `ink` at 4.5:1 or more (WCAG 1.4.3), and
// its icon at 3:1 or more (WCAG 1.4.11).
async function expectReadable(node, ink, message) {
  const lines = await contrast(node.locator(NODE_TEXT));

  expect.soft(lines.length, `${message}: lines of text`).toBeGreaterThan(0);
  for (const line of lines) {
    expect.soft(line.color, `${message}: color of "${line.text}"`).toBe(ink);
    expect
      .soft(line.ratio, `${message}: contrast of "${line.text}"`)
      .toBeGreaterThanOrEqual(4.5);
  }
  const [icon] = await contrast(
    node.locator('.builder-node__header > .builder-icon'),
  );

  expect
    .soft(icon?.ratio, `${message}: contrast of the icon`)
    .toBeGreaterThanOrEqual(3);
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

// web-01 and web-02 on switch EXP (web-02 also on MGMT) and a group Zone,
// colored by `look` per label. `edgeIds` names the connections' ids.
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

// web-01 to web-09 on switch EXP (web-01 a Router, on eth0 and eth1), an
// external device and one without a type. The switch is nearest the middle.
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

test('a template adds a device with no description', async ({
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

  await test.step('the stored devices have no description', async () => {
    await builder.persisted(draft, (doc) => doc.nodes.length, 6);
    const devices = Object.fromEntries(
      devicesOf(await builder.serverDocument(draft)).map((node) => [
        node.device.hostname,
        node.device.spec,
      ]),
    );

    for (const [hostname, spec] of Object.entries(devices)) {
      expect.soft(spec.general.description, hostname).toBe('');
    }
    expect.soft(devices.external).toEqual({
      external: true,
      type: 'HIL',
      general: { hostname: 'external', description: '' },
      network: { interfaces: [] },
    });
  });

  await test.step('the Inspector shows the Firewall template’s OS type and image', async () => {
    await builder.selectNode('firewall');
    const hardware = specGroup(builder, 'Hardware');

    await expect
      .soft(builder.inspector.getByLabel(/^Type/))
      .toHaveValue('Firewall');
    await expect.soft(hardware.getByLabel(/^OS type/)).toHaveValue('vyos');
    await expect.soft(hardware.getByLabel(/^Image/)).toHaveValue('vyos.qc2');
    await expect
      .soft(specGroup(builder, 'General').getByLabel(/^Description/))
      .toHaveValue('');
  });

  expectNoFatal(issues);
});

// Opens a draft of tooltipDocument(). Returns the tooltip, web-01 and the
// switch with their wrappers, and parkPointer() to move the pointer off nodes.
async function openTooltipDraft(page, builder, testInfo) {
  const draft = await builder.seedDraft(
    tooltipDocument(`presentation-${testInfo.project.name}-${Date.now()}`),
  );
  // Before the page opens, so that a test can run its timers (TIP_RUN_MS).
  await page.clock.install();
  await builder.openDraft(draft);

  const web01 = builder.node('web-01', 'device');
  const sw = builder.nodes('switch');
  const idOf = (node) => node.getAttribute('data-node-id');

  return {
    tooltip: infoTooltip(page),
    web01,
    sw,
    idOf,
    switchWrapper: flowNode(page, await idOf(sw)),
    web01Wrapper: flowNode(page, await idOf(web01)),
    parkPointer: () =>
      page.getByRole('heading', { name: 'Nodes & Objects' }).hover(),
  };
}

test(
  'a device shows an info tooltip when the pointer rests on it, which the pointer can reach and Escape hides',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const { tooltip, web01, web01Wrapper, parkPointer } =
      await openTooltipDraft(page, builder, testInfo);

    await test.step('each device shows its type: as stored, External, or Device without one', async () => {
      const type = (hostname) =>
        builder.node(hostname, 'device').getByTestId('node-type');

      await expect.soft(type('web-01')).toHaveText('Router');
      await expect.soft(type('web-02')).toHaveText('VirtualMachine');
      await expect.soft(type('plc-1')).toHaveText('External');
      await expect.soft(type('bare')).toHaveText('Device');
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('the pointer resting on a device shows its tooltip after a delay', async () => {
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
      // Past the time the tooltip waits before it goes.
      await page.clock.runFor(TIP_RUN_MS);
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

    await test.step('within the same time, the tooltip goes once the pointer has left the node and the tooltip', async () => {
      await web01.hover();
      await expect(tooltip).toBeVisible();

      await parkPointer();
      await page.clock.runFor(TIP_RUN_MS);
      expect.soft(await tooltip.count(), 'tooltips after the run').toBe(0);
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
      // Past the hold of the messages before it, so a wrong one has shown.
      await page.clock.runFor(2 * ANNOUNCE_HOLD_MS);
      await expect.soft(builder).not.toHaveAnnounced('Selection cleared');

      // The second clears the selection, as Escape does on the canvas.
      await page.keyboard.press('Escape');
      await expect.soft(web01).not.toHaveClass(/is-selected/);
      await expect.soft(builder).toHaveAnnounced('Selection cleared');
      await parkPointer();
    });

    expectNoFatal(issues);
  },
);

test(
  'a switch shows its info tooltip on keyboard focus, which follows it, comes back after a device’s, and goes with the focus',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const { tooltip, web01, sw, switchWrapper, parkPointer } =
      await openTooltipDraft(page, builder, testInfo);

    await test.step('keyboard focus on the switch shows its tooltip at once, and describes the switch to assistive technology', async () => {
      // An arrow key on the canvas itself moves focus to the node nearest
      // the middle of the view.
      await page.locator('#builder-canvas').focus();
      await page.keyboard.press('ArrowDown');
      await expect(switchWrapper).toBeFocused();
      await expect(tooltip).toBeVisible();
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
      await page.clock.runFor(TIP_RUN_MS);
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
      await page.clock.runFor(TIP_RUN_MS);
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

    expectNoFatal(issues);
  },
);

test(
  'notes and groups have no info tooltip, and an SVG image of the diagram has no tooltip text',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const { tooltip, web01, sw, idOf, switchWrapper, parkPointer } =
      await openTooltipDraft(page, builder, testInfo);

    await test.step('a device shows its tooltip, and it goes within the time the notes and groups are given', async () => {
      await parkPointer();
      await web01.hover();
      await expect(tooltip).toBeVisible();

      await parkPointer();
      await page.clock.runFor(TIP_RUN_MS);
      await expect(tooltip).toHaveCount(0);
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
        await page.clock.runFor(TIP_RUN_MS);
        expect.soft(await tooltip.count(), kind).toBe(0);
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

test('a device takes an outline and a fill at once, the text on a fill stays readable, and Undo takes them back', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const { draft } = await seedStyled(builder, testInfo);
  // Before the page opens, so that the test can run its timers.
  await page.clock.install();
  await builder.openDraft(draft);

  const web01 = builder.node('web-01', 'device');
  const outline = inspectorField(builder, 'outlineColor');
  const fill = inspectorField(builder, 'fillColor');
  const apply = builder.inspector.getByTestId('inspector-apply');
  const deviceOf = (doc) =>
    devicesOf(doc).find((node) => node.device.hostname === 'web-01').device;
  const surface = await web01.evaluate(
    (node) => getComputedStyle(node).backgroundColor,
  );

  await test.step('a device has Outline Color and Fill Color under its Icon and its Icon size, each with its own picker', async () => {
    await builder.selectNode('web-01');
    await expect
      .soft(
        builder.inspector.locator('form label.label').filter({
          hasText: /^(Hostname|Icon|Outline Color|Fill Color)/,
        }),
      )
      .toHaveText([
        /^Hostname/,
        /^Icon/,
        /^Icon size/,
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
        /Text and icon turn black or white to stay readable\.$/,
      );
    await expect
      .soft(outline.text)
      .toHaveAccessibleDescription(
        /^Border color of the node on the canvas\.$/,
      );
  });

  await test.step('the icon is in the theme’s accent color, apart from the text', async () => {
    const icon = web01.locator('.builder-node__header > .builder-icon');

    await expect
      .soft(icon)
      .toHaveCSS('color', await themeColor(page, '--bx-accent'));
    await expect
      .soft(icon)
      .not.toHaveCSS('color', await themeColor(page, '--bx-text'));
    const [shown] = await contrast(icon);

    expect.soft(shown.ratio, 'contrast of the icon').toBeGreaterThanOrEqual(3);
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
    await expect.soft(fill.picker).toHaveAccessibleDescription('Blue, #2f6fbf');
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
    await page.clock.runFor(5 * ANNOUNCE_HOLD_MS);
    await expect.soft(builder).not.toHaveAnnounced('No changes to apply.');
  });

  await test.step('a color that is no hex color is refused on its field, and the node keeps its fill', async () => {
    await fill.text.fill('red');
    await fill.text.blur();

    await expect
      .soft(fill.field)
      .toContainText('Fill Color must be a hex color, such as #2f6fbf');
    await expect.soft(fill.text).toHaveAttribute('aria-invalid', 'true');
    await expect.soft(web01).toHaveCSS('background-color', 'rgb(255, 212, 0)');
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
      .toHaveAnnounced('Changed the outline color of Device web-01 to #f4f6f9');
    await expect(web01).toHaveClass(/builder-node--outlined/);

    // Selected, the node shows the selection's border, not its outline,
    // and no badge. It keeps its fill.
    const ring = await themeColor(page, '--bx-selected-ring');
    await expect.soft(web01).toHaveCSS('border-top-color', ring);
    await expect.soft(web01).toHaveCSS('background-color', 'rgb(255, 212, 0)');
    expect
      .soft(
        await web01.evaluate(
          (node) => getComputedStyle(node, '::after').content,
        ),
      )
      .toBe('none');

    // The pale outline does not keep 3:1 on the yellow fill: the icon is
    // in the text's black.
    await expect
      .soft(web01.locator('.builder-node__header > .builder-icon'))
      .toHaveCSS('color', 'rgb(0, 0, 0)');

    // Not selected, it shows its outline, ringed where it would fade.
    await builder.selectNode('web-02');
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
    await expect.soft(web01).toHaveCSS('background-color', 'rgb(255, 212, 0)');
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
    await expect.soft(web01).toHaveCSS('background-color', 'rgb(255, 212, 0)');
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

  expectNoFatal(issues);
});

test('a switch’s network color is Edge Color, and its own outline and fill apply at once', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const { draft } = await seedStyled(builder, testInfo);
  await builder.openDraft(draft);

  const exp = builder.node('EXP', 'switch');
  const outline = inspectorField(builder, 'outlineColor');
  const fill = inspectorField(builder, 'fillColor');
  const apply = builder.inspector.getByTestId('inspector-apply');

  await test.step('a switch’s network color is Edge Color, and its own outline and fill apply at once', async () => {
    const edge = inspectorField(builder, 'color');
    const network = (doc) => doc.networks.find((item) => item.name === 'EXP');
    const switchOf = (doc) =>
      doc.nodes.find(
        (node) =>
          node.kind === 'switch' && node.switch.networkId === network(doc).id,
      ).switch;
    const before = network(await builder.serverDocument(draft));
    const mgmt = builder.node('MGMT', 'switch');
    const [exp0, mgmt1] = [
      await themeColor(page, '--bx-net-0'),
      await themeColor(page, '--bx-net-1'),
    ];

    // Without an outline of its own, a switch is outlined in its
    // network's color, as its connections are drawn.
    await expect.soft(exp).toHaveClass(/builder-node--outlined/);
    await expect.soft(exp).toHaveCSS('border-top-color', exp0);
    await expect.soft(mgmt).toHaveCSS('border-top-color', mgmt1);

    await builder.selectNode('EXP');
    await expect.soft(edge.label).toHaveText(/^Edge Color/);
    await expect.soft(edge.picker).toHaveAccessibleName('Choose edge Color');
    await expect
      .soft(builder.inspector.getByTestId('inspector-color-picker'))
      .toHaveCount(3);
    await expect
      .soft(builder.inspector.getByLabel('Color', { exact: true }))
      .toHaveCount(0);

    // Each applies at once, without Apply, as one edit.
    await fill.picker.click();
    await fill.popup.getByRole('button', { name: 'Olive, #6b6f18' }).click();
    await expect
      .soft(builder)
      .toHaveAnnounced('Changed the fill color of Network EXP to #6b6f18');
    await outline.text.fill('#a3273f');
    await outline.text.blur();
    await expect
      .soft(builder)
      .toHaveAnnounced('Changed the outline color of Network EXP to #a3273f');
    await expect.soft(apply).toHaveCount(0);

    await expect(exp).toHaveCSS('background-color', 'rgb(107, 111, 24)');
    await expectReadable(exp, 'rgb(255, 255, 255)', 'a filled switch');
    // Its own outline replaces its network's.
    await expect.soft.poll(() => outlineOf(exp)).toBe('rgb(163, 39, 63)');

    await builder.selectNode('web-02');
    await expect(exp).not.toHaveClass(/is-selected/);
    await expect.soft(exp).toHaveCSS('border-top-color', 'rgb(163, 39, 63)');
    // The double border that tells a switch from a device.
    await expect.soft(exp).toHaveCSS('border-top-style', 'double');
    // The colors are this switch's, not its network's: the other switch
    // is as it was.
    await expect.soft(mgmt).not.toHaveClass(/builder-node--filled/);
    await expect.soft(mgmt).toHaveCSS('border-top-color', mgmt1);

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

    // Each color is one Undo step. Without its own outline, the switch
    // is outlined in its network's color again.
    await page.getByTestId('toolbar-undo').click();
    await expect.poll(() => outlineOf(exp)).toBe(exp0);
    await expect.soft(exp).toHaveClass(/builder-node--filled/);
    await page.getByTestId('toolbar-undo').click();
    await expect(exp).not.toHaveClass(/builder-node--filled/);
    await expect.soft.poll(() => outlineOf(exp)).toBe(exp0);
    await page.getByTestId('toolbar-redo').click();
    await page.getByTestId('toolbar-redo').click();
    await expect.poll(() => outlineOf(exp)).toBe('rgb(163, 39, 63)');
    await builder.waitSaved();
  });

  expectNoFatal(issues);
});

test(
  'a connection’s color, line style and label and a switch’s color apply at once, and each Undo takes one back',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const { draft, edgeIds } = await seedStyled(builder, testInfo);
    await builder.openDraft(draft);

    const id = edgeIds['web-01 to EXP'];
    const title = 'Connection from web-01 (eth0) to EXP';
    const line = page.locator(
      `.vue-flow__edge[data-id="${id}"] path.builder-edge`,
    );
    const edgeLabel = page.locator(
      `.builder-edge__label[data-edge-id="${id}"]`,
    );
    const exp = builder.node('EXP', 'switch');
    const apply = builder.inspector.getByTestId('inspector-apply');
    const undo = page.getByTestId('toolbar-undo');
    const edgeOf = (doc) => {
      const { color, lineStyle } = doc.edges.find((edge) => edge.id === id);

      return { color, lineStyle };
    };

    await test.step('a connection’s color and line style apply at once, one Undo step each', async () => {
      // The edge's label lets a click through to the line under it.
      const box = await edgeLabel.boundingBox();

      await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
      await expect(builder.inspector).toContainText(title);

      const color = inspectorField(builder, 'color');
      const lineStyle = inspectorField(builder, 'lineStyle');

      await color.text.fill('#a3273f');
      await color.text.blur();
      await expect
        .soft(builder)
        .toHaveAnnounced(`Changed the color of ${title} to #a3273f`);
      await expect(line).toHaveCSS('stroke', 'rgb(163, 39, 63)');
      await lineStyle.select.selectOption({ label: 'Dotted' });
      await expect
        .soft(builder)
        .toHaveAnnounced(`Changed the line style of ${title} to Dotted`);
      await expect(line).toHaveAttribute('data-pattern', 'dotted');
      await expect.soft(apply).toHaveCount(0);
      await builder.persisted(
        draft,
        edgeOf,
        { color: '#a3273f', lineStyle: 'dotted' },
        { soft: true },
      );

      await undo.click();
      await expect(line).not.toHaveAttribute('data-pattern', 'dotted');
      await expect.soft(line).toHaveCSS('stroke', 'rgb(163, 39, 63)');
      await undo.click();
      await expect(line).not.toHaveCSS('stroke', 'rgb(163, 39, 63)');
      await builder.persisted(
        draft,
        edgeOf,
        { color: undefined, lineStyle: undefined },
        { soft: true },
      );
    });

    await test.step('a label applies when it is committed, and Escape takes back text typed in it', async () => {
      const box = await edgeLabel.boundingBox();

      await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);

      const label = builder.inspector.getByLabel('Label', { exact: true });

      // Text typed is no edit for Apply, and Escape puts back the label.
      await label.fill('typed');
      await expect.soft(apply).toHaveCount(0);
      await label.press('Escape');
      await expect.soft(label).toHaveValue('EXP');
      await expect.soft(label).toBeFocused();

      // Enter commits it, as one edit.
      await label.fill('uplink');
      await label.press('Enter');
      await expect
        .soft(builder)
        .toHaveAnnounced(`Changed the label of ${title} to "uplink"`);
      await expect.soft(edgeLabel).toHaveText('uplink');
      await expect.soft(apply).toHaveCount(0);
      await undo.click();
      await expect.soft(edgeLabel).toHaveText('EXP');
    });

    await test.step('a switch’s fill color applies at once, one Undo step', async () => {
      await builder.selectNode('EXP');

      const fill = inspectorField(builder, 'fillColor');

      await fill.picker.click();
      await fill.popup.getByRole('button', { name: 'Olive, #6b6f18' }).click();
      await expect(exp).toHaveCSS('background-color', 'rgb(107, 111, 24)');
      await expect
        .soft(builder)
        .toHaveAnnounced('Changed the fill color of Network EXP to #6b6f18');
      await expect.soft(apply).toHaveCount(0);
      await undo.click();
      await expect(exp).not.toHaveClass(/builder-node--filled/);
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

    await builder.selectNode('MGMT');
    await expect.soft(lineStyle.label).toHaveText(/^Line style/);
    expect
      .soft(await choicesOf(lineStyle.select))
      .toEqual(['Auto (Dashed)', ...PATTERNS]);
    await expect.soft(lineStyle.select).toHaveValue('');

    await builder.selectNode('EXP');
    expect
      .soft(await choicesOf(lineStyle.select))
      .toEqual(['Auto (Solid)', ...PATTERNS]);
    await expect.soft(lineStyle.select).toHaveValue('');
    await expect
      .soft(lineStyle.select)
      .toHaveAccessibleDescription(
        /Auto selects one from the network's position in the diagram, so networks look different without color\./,
      );
  });

  await test.step('Dotted on a network draws every line of it dotted at once, and leaves the other network', async () => {
    // A switch's line style applies at once, without Apply.
    await lineStyle.select.selectOption({ label: 'Dotted' });
    await expect
      .soft(builder)
      .toHaveAnnounced('Changed the line style of Network EXP to Dotted');
    await expect.soft(apply).toHaveCount(0);

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

    // A connection's line style applies at once, without Apply.
    await lineStyle.select.selectOption({ label: 'Solid' });
    await expect
      .soft(builder)
      .toHaveAnnounced(
        'Changed the line style of Connection from web-01 (eth0) to EXP to Solid',
      );
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
    await expect(line('web-01 to EXP')).toHaveAttribute(
      'stroke-dasharray',
      '10 4 2 4',
    );
  });

  const zone = builder.node('Zone', 'group');

  await test.step('a group shows its description under its title, in the border pattern and with the icon chosen', async () => {
    const field = (path) => inspectorField(builder, path);

    await builder.selectNode('Zone');
    await expect
      .soft(builder.inspector.locator('form label.label'))
      .toHaveText([
        /^Title/,
        /^Description/,
        /^Color/,
        /^Border pattern/,
        /^Icon/,
        /^Icon size/,
      ]);
    expect
      .soft(await choicesOf(field('borderStyle').select))
      .toEqual(['Default (Dashed)', 'Solid', 'Dashed', 'Dotted', 'Double']);
    const icon = builder.inspector.getByTestId('inspector-icon');
    const menu = builder.inspector.getByTestId('inspector-icon-menu');

    await expect.soft(icon).toHaveAccessibleName('Icon Default (Container)');
    await expect.soft(zone).toHaveCSS('border-top-style', 'dashed');
    await expect.soft(zone.locator('.builder-icon--container')).toHaveCount(1);
    await expect.soft(zone.locator('.builder-node__comment')).toHaveCount(0);

    const description = field('description').field.locator('textarea');
    await description.fill('DMZ hosts');
    await description.blur();
    // The border pattern and the icon apply at once. The description
    // waits for Apply.
    await field('borderStyle').select.selectOption({ label: 'Double' });
    await expect
      .soft(builder)
      .toHaveAnnounced('Changed the border pattern of Group to Double');
    await icon.click();
    await expect
      .soft(menu.getByRole('menuitemradio').first())
      .toHaveAccessibleName('Default (Container)');
    await menu
      .getByRole('menuitemradio', { name: 'Firewall', exact: true })
      .click();
    await expect
      .soft(builder)
      .toHaveAnnounced(/\bChanged the icon of Group to Firewall\b/i);
    await expect.soft(zone.locator('.builder-node__comment')).toHaveCount(0);
    await applyEdits();
    await expect.soft(builder).toHaveAnnounced('Updated group Zone');

    await expect(zone).toHaveCSS('border-top-style', 'double');
    await expect.soft(zone).toHaveCSS('border-top-width', '4px');
    await expect
      .soft(zone.locator('.builder-node__comment'))
      .toHaveText('DMZ hosts');
    await expect.soft(zone.locator('.builder-icon--firewall')).toHaveCount(1);
    await expect
      .soft(builder.canvasItem('Zone').locator('.builder-icon--firewall'))
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

// The icon a device, a switch or a group draws by its name.
function nodeIcon(node) {
  return node.locator('.builder-node__header > .builder-icon');
}

// The lines of text a device, a switch or a group shows.
const NODE_LINES = `${NODE_TEXT}, .builder-node__origin`;

// Where a node's icon and its lines of text are drawn in its box, in the
// page's pixels: the box, the icon, and each line with its text.
function layoutOf(node) {
  return node.evaluate((element, selector) => {
    const rect = (target) => {
      const { left, top, right, bottom } = target.getBoundingClientRect();

      return { left, top, right, bottom };
    };

    return {
      box: rect(element),
      icon: rect(
        element.querySelector('.builder-node__header > .builder-icon'),
      ),
      lines: [...element.querySelectorAll(selector)].map((line) => ({
        text: line.textContent.trim(),
        ...rect(line),
      })),
    };
  }, NODE_LINES);
}

// The node's icon and every line of its text are inside its box, and no
// line runs under the icon.
async function expectLaidOut(node, message) {
  const { box, icon, lines } = await layoutOf(node);
  const inside = (rect, what) => {
    expect
      .soft(rect.left, `${message}: ${what} left`)
      .toBeGreaterThanOrEqual(box.left - 0.5);
    expect
      .soft(rect.right, `${message}: ${what} right`)
      .toBeLessThanOrEqual(box.right + 0.5);
    expect
      .soft(rect.top, `${message}: ${what} top`)
      .toBeGreaterThanOrEqual(box.top - 0.5);
    expect
      .soft(rect.bottom, `${message}: ${what} bottom`)
      .toBeLessThanOrEqual(box.bottom + 0.5);
  };

  inside(icon, 'icon');
  expect.soft(lines.length, `${message}: lines`).toBeGreaterThan(0);

  for (const line of lines) {
    inside(line, `"${line.text}"`);

    const apart =
      line.right <= icon.left + 0.5 ||
      line.left >= icon.right - 0.5 ||
      line.bottom <= icon.top + 0.5 ||
      line.top >= icon.bottom - 0.5;

    expect
      .soft(apart, `${message}: "${line.text}" clear of the icon`)
      .toBe(true);
  }

  return { box, icon, lines };
}

// Opens a draft of styledDocument() at `iconSize`, with a smallest group Edge.
// Returns its nodes by kind and the diagram's Icon size field.
async function openIconSizeDraft(builder, testInfo, iconSize) {
  const { edgeIds: _, ...document } = styledDocument(
    `icon-size-${testInfo.project.name}-${Date.now()}`,
  );
  document.nodes.push({
    id: crypto.randomUUID(),
    kind: 'group',
    label: 'Edge',
    position: { x: 640, y: 400 },
    size: { width: 120, height: 80 },
    group: { title: 'Edge' },
  });
  if (iconSize) {
    document.iconSize = iconSize;
  }
  const draft = await builder.seedDraft(document);
  await builder.openDraft(draft);

  return {
    draft,
    nodes: {
      device: builder.node('web-01', 'device'),
      switch: builder.node('EXP', 'switch'),
      group: builder.node('Zone', 'group'),
      'least group': builder.node('Edge', 'group'),
    },
    diagramSize: builder.inspector.getByTestId('inspector-icon-size-select'),
  };
}

// A picture of a node, kept in the test's output and attached to its
// report.
async function keepScreenshot(testInfo, locator, name) {
  const path = testInfo.outputPath(name);

  await locator.screenshot({ path });
  await testInfo.attach(name, { path, contentType: 'image/png' });
}

test('node icons are drawn Small until the diagram says Medium or Large, keep their node boxes, and download Large', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const { draft, nodes, diagramSize } = await openIconSizeDraft(
    builder,
    testInfo,
  );
  const PIXELS = { Small: 16, Medium: 24, Large: 32 };
  const boxes = {};
  const accent = await themeColor(page, '--bx-accent');

  // Each node draws its icon at `size`, in the layout of that size, with
  // its box as it was at Small. A screenshot of it goes with the report.
  const expectSize = async (size, kinds = Object.keys(nodes)) => {
    for (const kind of kinds) {
      const node = nodes[kind];
      const pixels = String(PIXELS[size]);

      await expect(node).toHaveAttribute('data-icon-size', size.toLowerCase());
      await expect.soft(nodeIcon(node)).toHaveAttribute('width', pixels);
      await expect.soft(nodeIcon(node)).toHaveCSS('width', `${pixels}px`);
      await expect.soft(nodeIcon(node)).toHaveCSS('height', `${pixels}px`);
      // A node without an outline color has its icon in the accent color
      // at every size. A switch is in its network's color.
      if (kind !== 'switch') {
        await expect.soft(nodeIcon(node)).toHaveCSS('color', accent);
      }

      if (size === 'Small') {
        await expect.soft(node).not.toHaveClass(/builder-node--icon-/);
      } else {
        await expect
          .soft(node)
          .toHaveClass(new RegExp(`builder-node--icon-${size.toLowerCase()}`));
      }

      const { box } = await expectLaidOut(node, `${kind} at ${size}`);
      const width = box.right - box.left;
      const height = box.bottom - box.top;

      boxes[kind] ??= { width, height };
      expect
        .soft(Math.abs(width - boxes[kind].width), `${kind} at ${size}: width`)
        .toBeLessThan(0.5);
      expect
        .soft(
          Math.abs(height - boxes[kind].height),
          `${kind} at ${size}: height`,
        )
        .toBeLessThan(0.5);

      await keepScreenshot(
        testInfo,
        node,
        `icon-size-${kind.replace(' ', '-')}-${size.toLowerCase()}.png`,
      );
    }
  };

  await test.step('a diagram draws Small icons until it says otherwise', async () => {
    await expect(diagramSize).toBeVisible();
    await expect.soft(diagramSize).toHaveAccessibleName('Icon size');
    await expect
      .soft(diagramSize)
      .toHaveAccessibleDescription(
        'Devices, switches and groups draw their icons at this size, unless one has a size of its own.',
      );
    expect
      .soft(await choicesOf(diagramSize))
      .toEqual([
        'Small (16 pixels)',
        'Medium (24 pixels)',
        'Large (32 pixels)',
      ]);
    await expect.soft(diagramSize).toHaveValue('small');
    await expectSize('Small');
  });

  // A device's handle, where its connection meets it, stays where it is.
  const handle = nodes.device.locator('.vue-flow__handle').first();
  const handleBox = await handle.boundingBox();

  await test.step('Medium and Large draw every node’s icon at once, beside its lines', async () => {
    await diagramSize.selectOption('medium');
    await expectSize('Medium');
    await builder.persisted(draft, (doc) => doc.iconSize, 'medium', {
      soft: true,
    });

    await diagramSize.selectOption('large');
    await expectSize('Large');
    await builder.persisted(draft, (doc) => doc.iconSize, 'large', {
      soft: true,
    });

    const moved = await handle.boundingBox();

    expect.soft(Math.abs(moved.x - handleBox.x), 'handle x').toBeLessThan(0.5);
    expect.soft(Math.abs(moved.y - handleBox.y), 'handle y').toBeLessThan(0.5);
  });

  await test.step('the SVG and PNG downloads draw the icons Large, inside their nodes', async () => {
    const dialog = await builder.openDialog('download');
    const save = async (kind) => {
      const [file] = await Promise.all([
        page.waitForEvent('download'),
        dialog.getByTestId(`download-${kind}`).click(),
      ]);

      return fs.readFileSync(await file.path());
    };
    const boundsText = dialog.getByText(/Diagram bounds: \d+ × \d+ px/);

    await expect(boundsText).toBeVisible();

    const [boundsWidth, boundsHeight] = (await boundsText.textContent())
      .match(/(\d+) × (\d+)/)
      .slice(1)
      .map(Number);
    // The image's size: the bounds at the zoom that fits them in 4096
    // pixels, at most 2 (computeExportViewport in exporters.js).
    const zoom = Math.min(2, 4096 / boundsWidth, 4096 / boundsHeight);
    const imageSize = [
      Math.ceil(boundsWidth * zoom),
      Math.ceil(boundsHeight * zoom),
    ];
    const markup = (await save('svg')).toString('utf8');
    const root = markup.match(/^<svg[^>]* width="(\d+)" height="(\d+)"/);

    expect
      .soft(root && root.slice(1).map(Number), 'SVG width and height')
      .toEqual(imageSize);
    expect.soft(markup).toContain('builder-node--icon-large');
    expect
      .soft(markup)
      .toMatch(/<svg[^>]*class="builder-icon[^"]*"[^>]*width="32"/);
    expect
      .soft(markup)
      .not.toMatch(/<svg[^>]*class="builder-icon[^"]*"[^>]*width="16"/);

    // The file is drawn in a page of its own, as a viewer draws it, to
    // measure each Large node's icon and lines against its box.
    const drawn = await page.context().newPage();

    try {
      await drawn.setContent(
        `<!doctype html><body style="margin: 0">${markup}</body>`,
      );

      const large = drawn.locator('.builder-node--icon-large');

      // Two devices, two switches and two groups, the least one too.
      await expect.soft(large).toHaveCount(6);

      for (const node of await large.all()) {
        const kind = await node.getAttribute('data-node-kind');
        const label = await node.locator('.builder-node__label').textContent();
        const name = `${kind} ${label.trim()} in the SVG`;
        const { icon } = await expectLaidOut(node, name);

        // 32 pixels, at the image's zoom.
        expect
          .soft(Math.abs(icon.right - icon.left - 32 * zoom), `${name}: icon`)
          .toBeLessThan(1);
      }
    } finally {
      await drawn.close();
    }

    // The PNG: a picture the browser decodes, of the image's size in device
    // pixels, as its header (IHDR, bytes 16 to 24) says too.
    const png = await save('png');
    const ratio = await page.evaluate(() => window.devicePixelRatio);
    const pixels = imageSize.map((length) => length * ratio);
    const picture = testInfo.outputPath('icon-size-download-large.png');

    fs.writeFileSync(picture, png);
    await testInfo.attach('icon-size-download-large.png', {
      path: picture,
      contentType: 'image/png',
    });

    const decoded = await page.evaluate(
      async ({ data }) => {
        const image = new Image();

        image.src = `data:image/png;base64,${data}`;
        await image.decode();

        return [image.naturalWidth, image.naturalHeight];
      },
      { data: png.toString('base64') },
    );

    expect
      .soft(png.subarray(0, 8).equals(PNG_SIGNATURE), 'PNG signature')
      .toBe(true);
    expect
      .soft([png.readUInt32BE(16), png.readUInt32BE(20)], 'PNG header size')
      .toEqual(pixels);
    expect.soft(decoded, 'PNG size as decoded').toEqual(pixels);
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  });

  expectNoFatal(issues);
});

// Show node icons only changes what the canvas draws: the draft keeps every
// icon, and the setting stays for the next page load.
test('Show node icons hides the icons of devices, switches and groups, on the canvas and in the SVG download', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const { draft, nodes } = await openIconSizeDraft(builder, testInfo, 'large');
  const kinds = Object.keys(nodes);
  const iconKeys = (doc) =>
    doc.nodes.map((node) => node[node.kind]?.iconKey || '').join(',');
  const before = iconKeys(await builder.serverDocument(draft));

  await test.step('Settings turns the icons off, and the lines take their place', async () => {
    await page.getByTestId('editor-settings').click();
    const settings = page.getByTestId('settings-dialog');
    const toggle = settings.getByTestId('settings-node-icons');

    await expect(toggle).toBeChecked();
    await expect
      .soft(toggle)
      .toHaveAccessibleDescription(
        'The icons of devices, switches and groups, on the canvas and in PNG, SVG and draw.io downloads. The diagram keeps its icons.',
      );
    await toggle.click();
    await expect(toggle).not.toBeChecked();
    await settings.getByTestId('settings-done').click();
    await expect(settings).toHaveCount(0);

    for (const kind of kinds) {
      const node = nodes[kind];

      await expect.soft(nodeIcon(node), kind).toHaveCount(0);
      // The document still says Large: only the drawing changes.
      await expect.soft(node, kind).toHaveAttribute('data-icon-size', 'large');
      await expect.soft(node, kind).not.toHaveClass(/builder-node--icon-/);
      // The name starts where the icon was: at the box's padding.
      const gap = await node.evaluate((element) => {
        const label = element.querySelector('.builder-node__label');
        const style = getComputedStyle(element);

        return (
          label.getBoundingClientRect().left -
          element.getBoundingClientRect().left -
          parseFloat(style.borderLeftWidth) -
          parseFloat(style.paddingLeft)
        );
      });

      expect
        .soft(Math.abs(gap), `${kind}: label at the padding`)
        .toBeLessThan(1);
    }
    await keepScreenshot(testInfo, builder.canvas, 'node-icons-hidden.png');
    expect.soft(iconKeys(await builder.serverDocument(draft))).toBe(before);
  });

  await test.step('the SVG download draws no node icon', async () => {
    const dialog = await builder.openDialog('download');
    const [file] = await Promise.all([
      page.waitForEvent('download'),
      dialog.getByTestId('download-svg').click(),
    ]);
    const markup = fs.readFileSync(await file.path(), 'utf8');

    expect.soft(markup).toContain('web-01');
    expect
      .soft(markup)
      .not.toMatch(
        /class="builder-node__header"[^>]*>\s*<svg[^>]*builder-icon/,
      );
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  });

  await test.step('a reload keeps the setting, and the palette shows the icons again', async () => {
    await page.reload();
    await builder.openDraft(draft);
    await expect(nodes.device).toBeVisible();
    await expect.soft(nodeIcon(nodes.device)).toHaveCount(0);

    await builder.canvas.focus();
    await page.keyboard.press('ControlOrMeta+k');
    const search = page.getByRole('combobox', { name: /Search commands/ });

    await search.fill('Show node icons');
    await expect(
      page
        .getByTestId('commands-dialog')
        .getByRole('option', { selected: true }),
    ).toContainText('Show node icons');
    await page.keyboard.press('Enter');
    await expect.soft(builder).toHaveAnnounced('Node icons shown.');
    for (const kind of kinds) {
      await expect.soft(nodeIcon(nodes[kind]), kind).toHaveCount(1);
      await expect
        .soft(nodes[kind], kind)
        .toHaveClass(/builder-node--icon-large/);
    }
  });

  expectNoFatal(issues);
});

test('Node icon size draws every icon at one size on the canvas, and the draft keeps its sizes', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const { draft, nodes, diagramSize } = await openIconSizeDraft(
    builder,
    testInfo,
    'large',
  );
  const kinds = Object.keys(nodes);
  const NOTE =
    'Your Node icon size setting (Small) overrides this on your canvas.';

  await test.step('Settings chooses Small, and every icon is drawn Small', async () => {
    await page.getByTestId('editor-settings').click();
    const settings = page.getByTestId('settings-dialog');
    const off = settings.getByRole('radio', { name: 'Off', exact: true });
    const small = settings.getByRole('radio', { name: 'Small', exact: true });

    await expect(off).toBeChecked();
    await expect
      .soft(small)
      .toHaveAccessibleDescription(
        'Off uses the sizes of the diagram. Another size replaces them on your canvas and in PNG, SVG and draw.io downloads. The diagram does not change.',
      );
    await small.check();
    await settings.getByTestId('settings-done').click();
    await expect(settings).toHaveCount(0);

    for (const kind of kinds) {
      await expect
        .soft(nodes[kind], kind)
        .toHaveAttribute('data-icon-size', 'small');
      await expect
        .soft(nodeIcon(nodes[kind]), kind)
        .toHaveAttribute('width', '16');
    }
    await builder.persisted(draft, (doc) => doc.iconSize, 'large', {
      soft: true,
    });
  });

  await test.step('the Inspector’s Icon size fields say that the setting overrides them', async () => {
    await expect.soft(diagramSize).toHaveValue('large');
    await expect
      .soft(builder.inspector.getByTestId('inspector-icon-size-override'))
      .toHaveText(NOTE);
    await expect
      .soft(diagramSize)
      .toHaveAccessibleDescription(
        `Devices, switches and groups draw their icons at this size, unless one has a size of its own. ${NOTE}`,
      );

    await builder.selectNode('web-01');
    const own = inspectorField(builder, 'iconSize');

    await expect
      .soft(own.field.getByTestId('inspector-field-note'))
      .toHaveText(NOTE);
    await expect
      .soft(own.select)
      .toHaveAccessibleDescription(new RegExp(NOTE.replace(/[().]/g, '\\$&')));
  });

  await test.step('the palette’s Node icon size: Off gives the draft’s sizes back', async () => {
    await builder.canvas.focus();
    await page.keyboard.press('ControlOrMeta+k');
    await page
      .getByRole('combobox', { name: /Search commands/ })
      .fill('Node icon size: Off');
    await expect(
      page
        .getByTestId('commands-dialog')
        .getByRole('option', { selected: true }),
    ).toContainText('Node icon size: Off');
    await page.keyboard.press('Enter');
    await expect
      .soft(builder)
      .toHaveAnnounced('Node icons use the sizes of the diagram.');

    for (const kind of kinds) {
      await expect
        .soft(nodes[kind], kind)
        .toHaveAttribute('data-icon-size', 'large');
    }
    await expect
      .soft(builder.inspector.getByTestId('inspector-field-note'))
      .toHaveCount(0);
  });

  expectNoFatal(issues);
});

test('a device or a switch draws its icon at a size of its own, kept for the next page load', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const { draft, nodes, diagramSize } = await openIconSizeDraft(
    builder,
    testInfo,
    'large',
  );

  await test.step('a device of its own size draws it at once, and Diagram default gives the diagram’s back', async () => {
    const own = inspectorField(builder, 'iconSize');

    await builder.selectNode('web-01');
    await expect.soft(own.label).toHaveText(/^Icon size/);
    expect
      .soft(await choicesOf(own.select))
      .toEqual(['Diagram default (Large)', 'Small', 'Medium', 'Large']);
    await expect.soft(own.select).toHaveValue('');

    await own.select.selectOption({ label: 'Small' });
    // A device's look takes no Apply.
    await expect(nodeIcon(nodes.device)).toHaveAttribute('width', '16');
    await expect.soft(nodes.device).not.toHaveClass(/builder-node--icon-/);
    await expect
      .soft(nodeIcon(builder.node('web-02', 'device')))
      .toHaveAttribute('width', '32');
    await builder.persisted(
      draft,
      (doc) =>
        doc.nodes.find((node) => node.label === 'web-01').device.iconSize,
      'small',
      { soft: true },
    );
  });

  await test.step('a switch of its own size draws it at once', async () => {
    const own = inspectorField(builder, 'iconSize');

    await builder.selectNode('EXP');
    await own.select.selectOption({ label: 'Medium' });
    // As a device's, without Apply.
    await expect(nodeIcon(nodes.switch)).toHaveAttribute('width', '24');
    await expect
      .soft(builder.inspector.getByTestId('inspector-apply'))
      .toHaveCount(0);
    await builder.persisted(
      draft,
      (doc) =>
        doc.nodes.find((node) => node.kind === 'switch' && node.label === 'EXP')
          .switch.iconSize,
      'medium',
      { soft: true },
    );
  });

  await test.step('opened again in a new page load, the draft draws each size as it was left', async () => {
    // Loads the Builder page again and reads the draft from the server.
    await builder.editAndReload(draft, {
      expectSaved: () => builder.waitSaved(),
      via: 'open',
      expectAfterReload: async () => {
        await builder.waitSaved();
        await expect(nodeIcon(nodes.device)).toHaveAttribute('width', '16');
        await expect
          .soft(nodeIcon(builder.node('web-02', 'device')))
          .toHaveAttribute('width', '32');
        await expect
          .soft(nodeIcon(nodes.switch))
          .toHaveAttribute('width', '24');
        await expect
          .soft(nodeIcon(builder.node('MGMT', 'switch')))
          .toHaveAttribute('width', '32');
        await expect.soft(nodeIcon(nodes.group)).toHaveAttribute('width', '32');
        await expect
          .soft(nodeIcon(nodes['least group']))
          .toHaveAttribute('width', '32');
        await expect.soft(diagramSize).toHaveValue('large');
      },
    });
  });

  await test.step('in the dark theme, a selected Large device keeps its ring', async () => {
    await page.emulateMedia({ colorScheme: 'dark' });
    await builder.selectNode('web-02');

    const web02 = builder.node('web-02', 'device');

    await expect(web02).toHaveClass(/is-selected/);
    await expectLaidOut(web02, 'selected web-02 in the dark theme');
    await keepScreenshot(
      testInfo,
      web02,
      'icon-size-device-large-selected-dark.png',
    );
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

// The Icon field of the Inspector, and the Custom icons dialog that its
// menu opens.
function iconField(page, builder) {
  const field = builder.inspector.locator('[data-path="iconKey"]');
  const button = builder.inspector.getByTestId('inspector-icon');
  const menu = builder.inspector.getByTestId('inspector-icon-menu');
  const dialog = page.getByTestId('icon-dialog');

  return {
    field,
    label: field.locator('label.label'),
    button,
    menu,
    name: field.getByTestId('inspector-icon-name'),
    note: field.getByTestId('inspector-icon-note'),
    image: field.locator('img.builder-icon--custom'),
    // Custom icon…, the last item of the menu, opens the dialog.
    custom: menu.getByTestId('inspector-icon-item-custom-icon'),
    async choose() {
      await button.click();
      await menu.getByTestId('inspector-icon-item-custom-icon').click();
    },
    // A built-in icon, by its label, in place of the custom icon.
    async builtIn(label) {
      await button.click();
      await menu
        .getByRole('menuitemradio', { name: label, exact: true })
        .click();
    },
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

// Chooses `file` in the open Custom icons dialog and uploads it under `name`
// (or the proposed name). Returns the POST's response.
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

// Records what the page is asked, and what it fetches, that a picture must
// never cause: the dialogs it opens, and its requests for icon-probe.
function watchPictureEffects(page) {
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

  return { prompts, probes };
}

test(
  'the Icon field shows the icon and opens the Custom icons dialog from its menu, and one click lands on it while the Hostname above is refused',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const { draft } = await seedStyled(builder, testInfo);
    const icons = iconField(page, builder);
    const web01 = builder.node('web-01', 'device');

    await builder.openDraft(draft);

    await test.step('the Icon field shows the built-in icon, and Custom icon… in its menu opens the Custom icons dialog', async () => {
      await builder.selectNode('web-01');
      await expect.soft(icons.label).toHaveText(/^Icon/);
      await expect.soft(icons.name).toHaveText('Linux');
      await expect.soft(icons.note).toHaveCount(0);
      await expect.soft(icons.button).toHaveAccessibleName('Icon Linux');
      await expect
        .soft(icons.button)
        .toHaveAccessibleDescription(
          "Icon of the node on the canvas. Choose Custom icon… to use an image of the server's icon library.",
        );
      await expect.soft(customIcon(web01)).toHaveCount(0);

      // Up Arrow opens the menu on Custom icon…, Enter opens the dialog,
      // and Escape closes it, focus back on the button.
      await icons.button.focus();
      await page.keyboard.press('ArrowUp');
      await expect.soft(icons.custom).toBeFocused();
      await expect.soft(icons.custom).toHaveAccessibleName(/^Custom icon…/);
      await expect.soft(icons.custom).toHaveAttribute('aria-checked', 'false');
      await page.keyboard.press('Enter');
      await expect(icons.dialog).toBeVisible();
      await expect.soft(icons.dialog).toHaveAccessibleName('Custom icons');
      await expect
        .soft(icons.dialog)
        .toContainText(
          'PNG, JPEG, GIF, WebP and SVG files are converted to a PNG of at most 96 by 96 pixels.',
        );
      // Other tests may have icons in the library, so the counts vary.
      // The text is matched trimmed, as the template keeps white space.
      await expect
        .soft(icons.heading)
        .toHaveText(/^\s*Server icons \(\d+\)\s*$/);
      await expect
        .soft(icons.usage)
        .toHaveText(/^\s*The library holds \d+ of 2000 icons\.\s*$/);
      // The diagram carries no copy of an icon.
      await expect
        .soft(icons.dialog.getByTestId('icon-diagram-list'))
        .toHaveCount(0);
      await page.keyboard.press('Escape');
      await expect(icons.dialog).toHaveCount(0);
      await expect.soft(icons.button).toBeFocused();
    });

    // A click's press commits the field typed in, and its error shows only
    // once the click is over, so the button does not move from the pointer.
    await test.step('one click lands on the Icon field and on a color’s picker while the Hostname above them is given a name it cannot have', async () => {
      const hostname = builder.inspector.locator(
        '[data-path="hostname"] input',
      );
      const error = builder.inspector.locator('[data-path="hostname"] p.error');
      const outline = inspectorField(builder, 'outlineColor');
      const cancel = builder.inspector.getByTestId('inspector-cancel');

      await hostname.fill('bad host');
      await expect(error).toHaveCount(0);
      await icons.button.click();
      await expect(icons.menu).toBeVisible();
      await expect.soft(error).toHaveText(/Hostname cannot contain spaces/);
      await page.keyboard.press('Escape');
      await expect(icons.menu).toHaveCount(0);
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

      // A button below Apply does not move while pressed either. The press
      // ends off it, so it adds nothing.
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

    expectNoFatal(issues);
  },
);

test(
  'a custom icon is uploaded under a name as a PNG whatever the file holds, and nothing in an SVG file runs or is fetched',
  { tag: '@cross-browser' },
  async ({ page, builder, request, issues, tracker }, testInfo) => {
    const { draft } = await seedStyled(builder, testInfo);
    const names = { plc: iconName('plc'), pump: iconName('pump') };
    const icons = iconField(page, builder);
    const { prompts, probes } = watchPictureEffects(page);

    await builder.openDraft(draft);
    await builder.selectNode('web-01');

    let plc;

    await test.step('a PNG file is drawn by the browser, named, and sent as a PNG of at most 96 pixels a side', async () => {
      await icons.choose();
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

      // Another picture under a taken name, in any case, is refused, saying
      // who has it. The form stays for another name.
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
          `^Icon name "${names.plc.toUpperCase()}" belongs to an icon \\S+ uploaded. Choose another name\\.$`,
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
      const pump = await response.json();
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

      // The library lists both without their images, by the IDs of their
      // images, and gives each with its PNG, as JSON a browser will not
      // take for anything else.
      const list = await request.get(`${API}/builder/icons`);
      const listed = (await list.json()).icons;

      expect.soft(list.headers()['content-type']).toMatch(/^application\/json/);
      expect.soft(list.headers()['x-content-type-options']).toBe('nosniff');
      expect
        .soft(list.headers()['content-security-policy'])
        .toBe("default-src 'none'; frame-ancestors 'none'");
      for (const icon of [plc, pump]) {
        const found = listed.find((entry) => entry.name === icon.name);
        const read = await request.get(iconPath(icon.name));
        const one = await read.json();

        expect.soft(found?.id, `${icon.name} as listed`).toBe(icon.id);
        expect.soft(found, `${icon.name} as listed`).not.toHaveProperty('data');
        expect
          .soft(read.headers()['content-security-policy'])
          .toBe("default-src 'none'; frame-ancestors 'none'");
        expect.soft(one.data, `${icon.name} as read`).toBe(icon.data);
        expect.soft(isPNG(one.data || '')).toBe(true);
      }
      expect
        .soft([...tracker.icons].sort())
        .toEqual([names.plc, names.pump].sort());
    });

    expect.soft(prompts, 'dialogs opened during the test').toEqual([]);
    expect.soft(probes, 'requests a picture made').toEqual([]);
    expectNoFatal(issues);
  },
);

test(
  'a custom icon shows on a device and on a group at once, by its name, which keeps working when the icon is renamed; deleted, the group shows its built-in icon',
  { tag: '@cross-browser' },
  async ({ page, builder, request, issues, tracker }, testInfo) => {
    const { draft } = await seedStyled(builder, testInfo);
    const names = {
      plc: iconName('plc'),
      pump: iconName('pump'),
      renamed: iconName('pump'),
    };
    // Two icons of the server's library, PNGs of 96 by 48 pixels.
    const plc = await seedIcon(
      request,
      tracker,
      iconOf(pngOf(96, 48, ownColor()), names.plc),
    );
    const pump = await seedIcon(
      request,
      tracker,
      iconOf(pngOf(96, 48, ownColor()), names.pump),
    );
    const icons = iconField(page, builder);
    const web01 = builder.node('web-01', 'device');
    const zone = builder.node('Zone', 'group');
    const { prompts, probes } = watchPictureEffects(page);

    await builder.openDraft(draft);
    await builder.selectNode('web-01');
    await icons.choose();
    await expect(icons.heading).toHaveText(/Server icons \(/);

    const src = () => `data:image/png;base64,${pump.data}`;

    await test.step('Use gives the device the icon at once: on the canvas and in the Inspector', async () => {
      await icons
        .inLibrary(names.pump)
        .getByRole('button', { name: `Use ${names.pump}` })
        .click();
      await expect(icons.dialog).toHaveCount(0);
      await expect.soft(icons.button).toBeFocused();
      await expect
        .soft(builder)
        .toHaveAnnounced(
          `Changed the custom icon of Device web-01 to ${names.pump}`,
        );

      // An image of the PNG, in place of the built-in icon.
      await expect(customIcon(web01)).toHaveAttribute('src', src());
      await expect.soft(customIcon(web01)).toHaveAttribute('alt', '');
      await expect.soft(customIcon(web01)).toHaveJSProperty('naturalWidth', 96);
      await expect
        .soft(web01.locator('.builder-node__header > svg.builder-icon'))
        .toHaveCount(0);
      const box = await customIcon(web01).boundingBox();

      expect.soft([box.width, box.height]).toEqual([16, 16]);
      // The node is named as before: its icon is decoration.
      await expect
        .soft(flowNode(page, await web01.getAttribute('data-node-id')))
        .toHaveAccessibleName(/^Device web-01, /);

      await expect.soft(icons.name).toHaveText(names.pump);
      await expect.soft(icons.note).toHaveText('(custom icon)');
      await expect.soft(icons.image).toHaveAttribute('src', src());
      await expect
        .soft(icons.button)
        .toHaveAccessibleName(`Icon ${names.pump} (custom icon)`);
      // The menu marks Custom icon… as the choice, and opens the dialog
      // again from it.
      await icons.button.click();
      await expect.soft(icons.custom).toHaveAttribute('aria-checked', 'true');
      await page.keyboard.press('Escape');
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

    await test.step('a group takes an icon at once too, and the diagram still carries no copy', async () => {
      await builder.selectNode('Zone');
      await expect.soft(icons.name).toHaveText('Default (Container)');
      await expect
        .soft(icons.button)
        .toHaveAccessibleDescription(
          /Choose Custom icon… to use an image of the server's icon library\.$/,
        );
      await icons.choose();
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
      await expect.soft(icons.name).toHaveText(names.plc);
      await expect
        .soft(icons.image)
        .toHaveAttribute('src', `data:image/png;base64,${plc.data}`);
      await expect
        .soft(builder)
        .toHaveAnnounced(`Changed the custom icon of Group to ${names.plc}`);
      await expect
        .soft(builder.inspector.getByTestId('inspector-apply'))
        .toHaveCount(0);
      await expect(customIcon(zone)).toHaveAttribute(
        'src',
        `data:image/png;base64,${plc.data}`,
      );
      await expect.soft(customIcon(builder.canvasItem('Zone'))).toHaveCount(1);
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
      await builder.selectNode('web-01');
      await icons.choose();
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

    await test.step('Delete on the Icons tab asks first, and the group that names the icon shows its built-in icon', async () => {
      // The Custom icons dialog does not delete icons. The Icons tab does.
      await builder.waitSaved();
      await builder.open();
      await page.getByTestId('drafts-tab-icons').click();

      const panel = page.locator('#panel-icons');

      await panel.getByLabel('Show', { exact: true }).selectOption('server');
      await panel.getByLabel('Filter icons').fill(names.plc);

      const card = page.getByTestId(`icon-card-${names.plc}`);
      const remove = page.getByTestId(`icon-delete-${names.plc}`);
      const confirm = page.getByTestId('builder-confirm');

      await expect
        .soft(remove)
        .toHaveAccessibleName(`Delete ${names.plc} from the server`);
      await remove.click();
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
      await expect.soft(card).toHaveCount(1);

      await remove.click();
      const deleted = waitForApi(page, 'DELETE', `/builder/icons/${names.plc}`);

      await confirm.getByRole('button', { name: 'Delete' }).click();
      expect((await deleted).status()).toBe(204);
      await expect
        .soft(page.getByTestId('icons-tab-status'))
        .toHaveText(`Deleted ${names.plc} from the server.`);
      await expect(card).toHaveCount(0);

      // The group keeps the name. Nothing resolves it now.
      await builder.openDraft(draft);
      await expect(customIcon(zone)).toHaveCount(0);
      await expect
        .soft(zone.locator('.builder-icon--container'))
        .toHaveCount(1);
      await builder.selectNode('Zone');
      await expect.soft(icons.name).toHaveText(names.plc);
      await expect
        .soft(icons.note)
        .toHaveText(
          '(custom icon not found: the node shows its built-in icon)',
        );
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
      await builder.selectNode('web-01');
      await expect.soft(icons.name).toHaveText(names.pump);
    });

    expect.soft(prompts, 'dialogs opened during the test').toEqual([]);
    expect.soft(probes, 'requests a picture made').toEqual([]);
    expectNoFatal(issues);
  },
);

// Seeds a styledDocument() whose web-01 and Zone use `icon`, carried as a
// copy the server's library lacks.
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
    await expect.soft(customIcon(builder.canvasItem('Zone'))).toHaveCount(1);
    // The other device keeps the icon of its key.
    await expect
      .soft(customIcon(builder.node('web-02', 'device')))
      .toHaveCount(0);
    await builder.selectNode('web-01');
    await expect.soft(icons.name).toHaveText(plc.name);
    await expect.soft(icons.image).toHaveAttribute('src', src);
  });

  await test.step('a built-in icon takes the place of the custom icon at once; the diagram keeps the copy while the group uses it, and drops it after', async () => {
    await icons.builtIn('Linux');
    await expect
      .soft(builder)
      .toHaveAnnounced('Removed the custom icon of Device web-01');
    await expect(customIcon(web01)).toHaveCount(0);
    await expect.soft(web01.locator('.builder-icon--linux')).toHaveCount(1);
    await expect.soft(icons.name).toHaveText('Linux');
    // The menu gave focus back to its button.
    await expect.soft(icons.button).toBeFocused();
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

    await builder.selectNode('Zone');
    await icons.builtIn('Default (Container)');
    await expect
      .soft(builder)
      .toHaveAnnounced('Removed the custom icon of Group');
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
    await builder.selectNode('web-01');
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
    await builder.selectNode('web-01');
    await icons.choose();
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
              message: 'icon library is full: it holds at most 2000 icons',
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
      'Icon library is full: it holds at most 2000 icons.',
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
    await builder.selectNode('web-01');
    await icons.choose();
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

// A device for each of `icons`, in rows of eight, the document carrying a
// copy of each.
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

test('several custom icons are selected by keyboard, added to the server at once, and deleted from it at once', async ({
  page,
  builder,
  request,
  tracker,
  issues,
}, testInfo) => {
  // Three copies the diagram carries, which the server lacks: names of the
  // test's own, which the filter of the server's icons finds alone.
  const token = iconName('bulk');
  const icons = ['a', 'b', 'c'].map((part) =>
    iconOf(pngOf(4, 4, ownColor()), `${token}-${part}`),
  );
  const draft = await builder.seedDraft(
    iconsDocument(`icons-bulk-${testInfo.project.name}-${Date.now()}`, icons),
  );
  const field = iconField(page, builder);
  const copies = field.dialog
    .getByTestId('icon-diagram-list')
    .locator(':scope > li');
  const count = (list) => page.getByTestId(`bulk-count-${list}`);

  await builder.openDraft(draft);
  await builder.selectNode('plc-00');
  await field.choose();
  await expect(
    field.dialog.getByRole('heading', { name: 'In this diagram (3)' }),
  ).toBeVisible();
  await expect(field.heading).toHaveText(/^\s*Server icons \(\d+\)\s*$/);

  await test.step('the copies are selected with Space and Shift and the arrow keys, and Escape keeps the dialog open', async () => {
    await expect(
      field.dialog.getByTestId('bulk-bar-icons-diagram'),
    ).toHaveAccessibleName('Bulk actions: In this diagram');
    await expect(count('icons-diagram')).toHaveText('0 of 3 selected');
    await copies.first().focus();
    await expect(copies.first()).toHaveAccessibleName(
      `${icons[0].name} not selected`,
    );
    await page.keyboard.press('Space');
    await expect(count('icons-diagram')).toHaveText('1 of 3 selected');
    await expect(field.status).toHaveText('1 of 3 selected.');
    await page.keyboard.press('Shift+ArrowDown');
    await expect(copies.nth(1)).toBeFocused();
    await expect(count('icons-diagram')).toHaveText('2 of 3 selected');
    await expect(
      field.inDiagram(icons[1].name).getByTestId('icon-select'),
    ).toBeChecked();

    // Escape clears the selection first, and the dialog stays.
    await page.keyboard.press('Escape');
    await expect(count('icons-diagram')).toHaveText('0 of 3 selected');
    await expect(field.dialog).toBeVisible();
    await page.keyboard.press('Home');
    await expect(copies.first()).toBeFocused();
    await page.keyboard.press('Space');
    await page.keyboard.press('Shift+ArrowDown');
    await expect(count('icons-diagram')).toHaveText('2 of 3 selected');
  });

  await test.step('Add selected to server adds each selected copy, and they are on the server', async () => {
    const add = field.dialog.getByTestId('bulk-add-icons-diagram');
    const posts = [];
    const watch = (response) => {
      if (
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname.endsWith('/builder/icons')
      ) {
        posts.push(response.status());
      }
    };

    page.on('response', watch);
    await expect(add).toHaveAccessibleDescription('2 of 3 selected');
    await add.click();
    await expect(field.status).toHaveText(
      'Added 2 icons to the server. The diagram drops its copies with its next edit.',
    );
    page.off('response', watch);
    expect(posts).toEqual([201, 201]);
    for (const icon of icons.slice(0, 2)) {
      await expect(
        field.inDiagram(icon.name).getByTestId('icon-on-server'),
      ).toBeVisible();
      expect((await request.get(iconPath(icon.name))).status()).toBe(200);
    }
    // The copy the server still lacks keeps its checkbox, unselected.
    await expect(count('icons-diagram')).toHaveText('0 of 1 selected');
    await expect(
      field.inDiagram(icons[2].name).getByTestId('icon-add-to-server'),
    ).toBeVisible();
  });

  await test.step('a copy the server got since the library was read is said to be there, not counted as added', async () => {
    // Added through the API: the dialog's list does not know it yet.
    await seedIcon(request, tracker, icons[2]);
    const posts = [];
    const watch = (response) => {
      if (
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname.endsWith('/builder/icons')
      ) {
        posts.push(response.status());
      }
    };

    await field.inDiagram(icons[2].name).getByTestId('icon-select').check();
    await expect(count('icons-diagram')).toHaveText('1 of 1 selected');
    page.on('response', watch);
    await field.dialog.getByTestId('bulk-add-icons-diagram').click();
    await expect(field.status).toHaveText(
      `The server already has ${icons[2].name}.`,
    );
    page.off('response', watch);
    expect(posts).toEqual([200]);
    await expect(
      field.inDiagram(icons[2].name).getByTestId('icon-on-server'),
    ).toBeVisible();
    // No copy the server lacks is left, nor the row above them.
    await expect(
      field.dialog.getByTestId('bulk-bar-icons-diagram'),
    ).toHaveCount(0);
  });

  await test.step('on the Icons tab, Mod+A selects the server icons, and Delete deletes them after one question', async () => {
    // The Custom icons dialog does not delete icons. The Icons tab does.
    await field.close.click();
    await builder.waitSaved();
    await builder.open();
    await page.getByTestId('drafts-tab-icons').click();

    const panel = page.locator('#panel-icons');
    const cards = page.getByTestId('icons-list').locator(':scope > li');

    await panel.getByLabel('Show', { exact: true }).selectOption('server');
    await panel.getByLabel('Filter icons').fill(token);
    await expect(cards).toHaveCount(3);
    await expect(count('icons')).toHaveText('0 of 3 selected');
    await cards.first().focus();
    await page.keyboard.press('ControlOrMeta+A');
    await expect(count('icons')).toHaveText('3 of 3 selected');

    await page.keyboard.press('Delete');
    const confirm = page.getByRole('alertdialog');
    await expect(confirm).toHaveAccessibleName('Delete 3 icons?');
    await expect(confirm.locator('p')).toHaveText(
      `Delete ${icons[0].name}, ${icons[1].name} and ${icons[2].name} from the server? Diagrams and templates that use them, by any of their names, will show their built-in icon instead.`,
    );
    await expect(confirm.getByTestId('confirm-accept')).toHaveText(
      'Delete 3 icons',
    );
    await confirm.getByTestId('confirm-accept').click();
    await expect(page.getByTestId('icons-tab-status')).toHaveText(
      'Deleted 3 icons from the server.',
    );
    await expect(cards).toHaveCount(0);
    await expect(page.getByTestId('icons-tab-upload')).toBeFocused();
    for (const icon of icons) {
      expect((await request.get(iconPath(icon.name))).status()).toBe(404);
    }
  });

  expectNoFatal(issues);
});

// Custom icons keep their own colors in both themes, on nodes and in the
// dialog. The diagram carries as many copies as a document holds.
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
      const extra = iconOf(pngOf(4, 4, ownColor()), 'extra');
      // The routed library: an icon the diagram has a copy of, one it has not
      // under two names, and one more. No real icon is touched.
      const listed = [icons[0], spare, extra].map((icon) => ({
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

      await routeIconLibrary(page, listed);

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

      await builder.selectNode('plc-01');
      await expect(field.name).toHaveText('icon-01');
      await expectAccessible(page, {
        soft: true,
        label: `axe on a canvas of custom icons and the Icon field (${scheme})`,
      });

      // The Icon menu, with each built-in icon and the custom icon in use.
      await field.button.click();
      await expect(field.menu).toBeVisible();
      await expectAccessible(page, {
        include: '[data-testid="inspector-icon-menu"]',
        soft: true,
        label: `axe on the Icon menu (${scheme})`,
      });
      await page.keyboard.press('Escape');
      await expect(field.menu).toHaveCount(0);

      await field.choose();
      await expect(
        field.dialog.getByRole('heading', {
          name: `In this diagram (${MAX_DOCUMENT_ICONS})`,
        }),
      ).toBeVisible();
      await expect(field.heading).toHaveText('Server icons (3)');
      await expect(field.usage).toHaveText(
        /^\s*The library holds 3 of 2000 icons\.\s*$/,
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

      // Copies selected, and the row above them. The server icons have no
      // checkbox: the Icons tab deletes them.
      const copiesCount = field.dialog.getByTestId('bulk-count-icons-diagram');

      await field.inDiagram(icons[1].name).getByTestId('icon-select').check();
      await field.inDiagram(icons[2].name).getByTestId('icon-select').check();
      await expect(copiesCount).toHaveText(
        `2 of ${MAX_DOCUMENT_ICONS - 1} selected`,
      );
      await expect
        .soft(field.inLibrary(spare.name).getByTestId('icon-select'))
        .toHaveCount(0);
      await expect.soft(field.dialog.getByTestId('icon-delete')).toHaveCount(0);
      await expectAccessible(page, {
        soft: true,
        label: `axe on the Custom icons dialog with icons selected (${scheme})`,
      });

      // The server refuses both copies: the summary lists them with why,
      // and takes focus. No real icon is added.
      const ADD_ROUTE = '**/api/v1/builder/icons';

      await page.route(ADD_ROUTE, (route) =>
        route.request().method() === 'POST'
          ? route.fulfill({
              status: 403,
              json: { message: 'not allowed to add icons' },
            })
          : route.fallback(),
      );
      await field.dialog.getByTestId('bulk-add-icons-diagram').click();
      const summary = field.dialog.getByTestId('bulk-summary');

      await expect(summary).toBeFocused();
      await expect(summary).toHaveAccessibleName(
        '2 of 2 icons could not be added to the server.',
      );
      await expect(summary.getByRole('listitem')).toHaveText([
        new RegExp(`^${icons[1].name}: .+$`),
        new RegExp(`^${icons[2].name}: .+$`),
      ]);
      await expectAccessible(page, {
        soft: true,
        label: `axe on the summary of icons not added (${scheme})`,
      });
      await page.unroute(ADD_ROUTE);

      // Escape on a row unselects the copies, and the dialog stays open.
      await field.inDiagram(icons[1].name).focus();
      await page.keyboard.press('Escape');
      await expect(copiesCount).toHaveText(
        `0 of ${MAX_DOCUMENT_ICONS - 1} selected`,
      );
      await expect(field.dialog).toBeVisible();

      // A copy the diagram carries can be given to another node.
      await field.inDiagram(icons[0].name).getByTestId('icon-use').click();
      await expect(field.dialog).toHaveCount(0);
      await expect
        .soft(builder)
        .toHaveAnnounced('Changed the custom icon of Device plc-01 to icon-00');
      await expect.soft(field.name).toHaveText('icon-00');
      // The copy no node uses, and icon-00 (which the server holds as is),
      // leave the diagram.
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

// Text on a fill is black or white, whichever reads, in either theme, down to
// the grey on which both read about equally (4.6:1).
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

      // Selected, a node keeps its fill and its text color. The Inspector
      // shows its color fields, with their pickers.
      await builder.selectNode('web-02');
      await expect(nodeOf('web-02')).toHaveClass(/is-selected/);
      await expectReadable(
        nodeOf('web-02'),
        inks['web-02'],
        `${scheme}: web-02 selected`,
      );
      await builder.selectNode('MGMT');
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

test('the Icons tab shows and counts the built-in icons, which cannot be deleted, previews one and downloads them as PNG files', async ({
  page,
  builder,
  issues,
}) => {
  const by = (id) => page.getByTestId(id);
  const panel = page.locator('#panel-icons');
  const list = by('builtin-icons-list');
  const cards = list.locator(':scope > li');
  // A PNG file's width and height, from its IHDR chunk.
  const pngSize = (bytes) => [bytes.readUInt32BE(16), bytes.readUInt32BE(20)];
  const PNG_SIGNATURE = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);

  await builder.open();
  await by('drafts-tab-icons').click();
  // The count shows once the server's icons are read.
  await expect(by('drafts-tab-icons')).toHaveText(/^Icons \(\d+\)$/);

  await test.step('Show lists the built-in icons first, as cards that can be selected but not deleted', async () => {
    const show = panel.getByLabel('Show', { exact: true });

    // The built-in icons show first. Server icons shows the server's, and
    // Built-in icons shows these again.
    await expect(show).toHaveValue('builtin');
    await expect(list).toBeVisible();
    // ICON_ORDER's keys the registry accepts: 26 or more, the printer icon
    // among them.
    const builtin = await cards.count();

    expect(builtin).toBeGreaterThanOrEqual(26);
    await show.selectOption('server');
    await expect(list).toHaveCount(0);
    // The tab's count and the usage count the built-in icons and the
    // server's icons. Other tests change the server's icons, so all three
    // are read together until they agree.
    await expect
      .poll(async () => {
        const total =
          builtin + (await by('icons-list').locator(':scope > li').count());
        const texts = [
          await by('drafts-tab-icons').textContent(),
          await by('icons-tab-usage').textContent(),
        ];

        return texts.map((text) =>
          text.trim().replace(new RegExp(`\\b${total}\\b`), 'N'),
        );
      })
      .toEqual(['Icons (N)', 'The library holds N of 2000 icons.']);

    await show.selectOption('builtin');
    await expect(list).toBeVisible();
    await expect.soft(by('builtin-icon-card-printer')).toHaveCount(1);
    await expect
      .soft(by('builtin-icon-card-external'))
      .toHaveText(
        /^\s*External device\s*Select External device\s*Key: external\s*Built-in\s*View\s*Download\s*not selected\s*$/,
      );
    await expect
      .soft(list.locator('input[type="checkbox"]'))
      .toHaveCount(builtin);
    await expect
      .soft(list.getByRole('button', { name: /^(Delete|Rename)/ }))
      .toHaveCount(0);
    await expect
      .soft(by('bulk-count-icons'))
      .toHaveText(`0 of ${builtin} selected`);
    await expect
      .soft(by('icons-tab-builtin-note'))
      .toHaveText(
        'These icons are part of phenix. You cannot delete or rename them. Download saves an icon as a PNG file of 96 by 96 pixels.',
      );
    await expect.soft(by('drafts-tab-icons')).toHaveText(/^Icons \(\d+\)$/);

    // The cards are one Tab stop, which the arrow keys and End move.
    await cards.first().focus();
    await page.keyboard.press('End');
    await expect.soft(cards.last()).toBeFocused();
    await expect.soft(cards.last()).toHaveAttribute('tabindex', '0');
    await expect.soft(cards.first()).toHaveAttribute('tabindex', '-1');

    await panel.getByLabel('Filter icons').fill('fire');
    await expect(cards).toHaveCount(1);
    await expect
      .soft(cards.first())
      .toHaveAccessibleName('Firewall not selected');
    await expectAccessible(page, {
      soft: true,
      label: 'axe on the built-in icons',
    });
  });

  await test.step('View shows a built-in icon large and at the node sizes', async () => {
    const preview = by('builtin-icon-preview-firewall');

    await preview.click();
    const dialog = by('icon-preview-dialog');

    await expect(dialog).toBeVisible();
    await expect.soft(dialog).toHaveAccessibleName('Icon Firewall');
    await expect
      .soft(by('icon-preview-image'))
      .toHaveAccessibleName('Firewall at 96 pixels');
    expect.soft((await by('icon-preview-image').boundingBox()).width).toBe(96);
    await expect.soft(by('icon-preview-sizes').locator('svg')).toHaveCount(3);
    await expect
      .soft(by('icon-preview-details'))
      .toHaveText(
        /Name:?\s*Firewall.*Key:?\s*firewall.*Source:?\s*Built-in.*File:?\s*PNG, 96 × 96 pixels/s,
      );
    await expectAccessible(page, {
      soft: true,
      label: 'axe on the preview of a built-in icon',
    });
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect.soft(preview).toBeFocused();
  });

  await test.step('Download saves the drawing as a 96 by 96 PNG file', async () => {
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      by('builtin-icon-download-firewall').click(),
    ]);

    expect(download.suggestedFilename()).toBe('firewall.png');
    const bytes = fs.readFileSync(await download.path());

    expect.soft(bytes.subarray(0, 8)).toEqual(PNG_SIGNATURE);
    expect.soft(pngSize(bytes)).toEqual([96, 96]);
    await expect.soft(by('icons-tab-status')).toHaveText('Saved firewall.png.');

    // The file draws the icon, in its fixed color.
    const painted = await page.evaluate(async (text) => {
      const image = new Image();
      image.src = `data:image/png;base64,${text}`;
      await image.decode();
      const canvas = document.createElement('canvas');
      canvas.width = 96;
      canvas.height = 96;
      const context = canvas.getContext('2d');
      context.drawImage(image, 0, 0);
      const { data } = context.getImageData(0, 0, 96, 96);
      let drawn = 0;
      for (let at = 3; at < data.length; at += 4) {
        drawn += data[at] > 0 ? 1 : 0;
      }

      return drawn;
    }, bytes.toString('base64'));

    expect.soft(painted).toBeGreaterThan(100);
  });

  await test.step('Download selected saves a PNG file of each selected icon, and Delete selected says why it deletes none', async () => {
    await panel.getByLabel('Filter icons').fill('');
    await by('builtin-icon-select-router').check();
    await by('builtin-icon-select-switch').check();
    await expect(by('bulk-count-icons')).toHaveText(/^2 of \d+ selected$/);

    const download = by('bulk-download-icons');
    const files = [];

    await expect
      .soft(download)
      .toHaveAccessibleDescription(/^2 of \d+ selected$/);
    page.on('download', (file) => files.push(file));
    await download.click();
    await expect(by('icons-tab-status')).toHaveText('Downloaded 2 icons.');
    await expect.poll(() => files.length).toBe(2);
    expect(files.map((file) => file.suggestedFilename())).toEqual([
      'router.png',
      'switch.png',
    ]);
    for (const file of files) {
      expect
        .soft(pngSize(fs.readFileSync(await file.path())))
        .toEqual([96, 96]);
    }

    await by('bulk-delete-icons').click();
    const summary = by('bulk-summary');

    await expect(summary).toBeFocused();
    await expect(page.getByTestId('builder-confirm')).toHaveCount(0);
    await expect
      .soft(summary.locator('h3'))
      .toHaveText('2 of 2 icons could not be deleted.');
    await expect
      .soft(summary.locator('li'))
      .toHaveText([
        'Router: You cannot delete a built-in icon.',
        'Switch: You cannot delete a built-in icon.',
      ]);
    await expect.soft(by('builtin-icon-card-router')).toHaveCount(1);
    await expectAccessible(page, {
      soft: true,
      label: 'axe on the summary of Delete selected',
    });
    await by('bulk-summary-dismiss').click();
    await expect(by('bulk-all-icons')).toBeFocused();
  });

  expectNoFatal(issues);
});

test('the Icons tab lists the server’s icons, previews and downloads one, uploads one and deletes several', async ({
  page,
  builder,
  request,
  tracker,
  issues,
}) => {
  // Names of the test's own, which the filter finds alone.
  const token = iconName('tab');
  const wide = await seedIcon(
    request,
    tracker,
    iconOf(pngOf(48, 24, ownColor()), `${token}-wide`),
  );
  const small = await seedIcon(
    request,
    tracker,
    iconOf(pngOf(16, 16, ownColor()), `${token}-small`),
  );
  const by = (id) => page.getByTestId(id);
  const tab = by('drafts-tab-icons');
  const panel = page.locator('#panel-icons');
  const cards = by('icons-list').locator(':scope > li');
  const card = (name) => by(`icon-card-${name}`);
  const confirm = page.getByTestId('builder-confirm');

  await builder.open();

  await test.step('the tab stands after Layers and shows the icons as cards', async () => {
    await by('drafts-tab-layers').focus();
    await page.keyboard.press('ArrowRight');
    await expect(tab).toBeFocused();
    await expect(tab).toHaveAttribute('aria-selected', 'true');
    await expect.soft(tab).toHaveText(/^Icons \(\d+\)$/);
    // The tab shows the built-in icons first.
    const show = panel.getByLabel('Show', { exact: true });
    await expect.soft(show).toHaveValue('builtin');
    await show.selectOption('server');
    await panel.getByLabel('Filter icons').fill(token);
    await expect(cards).toHaveCount(2);
    await expect
      .soft(card(wide.name))
      .toContainText(wide.owner ? `Uploaded by ${wide.owner}` : 'Server');
    await expect.soft(card(wide.name)).toContainText('48 × 24 pixels');
    await expect.soft(card(small.name)).toContainText('16 × 16 pixels');
    await expectAccessible(page, { soft: true, label: 'axe on the Icons tab' });
  });

  await test.step('View shows the icon at its stored size, sharp, and at the node sizes', async () => {
    const preview = by(`icon-preview-${wide.name}`);

    await preview.click();
    const dialog = by('icon-preview-dialog');

    await expect(dialog).toBeVisible();
    await expect.soft(dialog).toHaveAccessibleName(`Icon ${wide.name}`);

    const image = by('icon-preview-image');

    await expect.soft(image).toHaveCSS('image-rendering', 'pixelated');
    expect
      .soft(
        await image.evaluate((element) => [
          element.naturalWidth,
          element.naturalHeight,
          element.getBoundingClientRect().width,
        ]),
      )
      .toEqual([48, 24, 48]);
    await expect.soft(by('icon-preview-sizes').locator('img')).toHaveCount(3);
    await expect
      .soft(by('icon-preview-sizes'))
      .toHaveText(/16 pixels\s*24 pixels\s*32 pixels/);
    await expectAccessible(page, {
      soft: true,
      label: 'axe on the icon preview',
    });
    // The tab is inert behind the dialog, so the dialog says the result.
    await Promise.all([
      page.waitForEvent('download'),
      by('icon-preview-download').click(),
    ]);
    await expect
      .soft(dialog.getByTestId('icon-preview-status'))
      .toHaveText(`Saved ${wide.name}.png.`);
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect.soft(preview).toBeFocused();
  });

  await test.step('Download saves the PNG the server stores, named after the icon', async () => {
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      by(`icon-download-${wide.name}`).click(),
    ]);

    expect(download.suggestedFilename()).toBe(`${wide.name}.png`);
    expect(fs.readFileSync(await download.path())).toEqual(
      Buffer.from(wide.data, 'base64'),
    );
    await expect
      .soft(by('icons-tab-status'))
      .toHaveText(`Saved ${wide.name}.png.`);
  });

  await test.step('Upload icon… names the new icon, which gets a card and focus', async () => {
    const name = `${token}-new`;
    const show = panel.getByLabel('Show', { exact: true });

    // An upload from the built-in icons shows the server's, with the new
    // icon.
    await show.selectOption('builtin');
    await by('icons-tab-file').setInputFiles({
      name: 'new.png',
      mimeType: 'image/png',
      buffer: pngOf(20, 20, ownColor()),
    });
    await expect(by('icons-tab-upload-name')).toBeFocused();
    await by('icons-tab-upload-name').fill(name);
    const added = waitForApi(page, 'POST', '/builder/icons');

    await by('icons-tab-upload-submit').click();
    expect((await added).status()).toBe(201);
    tracker.icon(name);
    await expect
      .soft(by('icons-tab-status'))
      .toHaveText(`Added ${name} to the server.`);
    await expect(by(`icon-preview-${name}`)).toBeFocused();
    await expect.soft(show).toHaveValue('server');
    await expect(cards).toHaveCount(3);
  });

  await test.step('Delete selected deletes several icons after one question', async () => {
    await by(`icon-card-select-${wide.name}`).check();
    await by(`icon-card-select-${small.name}`).check();
    await expect.soft(by('bulk-count-icons')).toContainText('2 of');
    await by('bulk-delete-icons').click();
    await expect(confirm).toBeVisible();
    await expect.soft(confirm).toHaveAccessibleName('Delete 2 icons?');
    await confirm.getByRole('button', { name: 'Delete 2 icons' }).click();
    await expect(card(wide.name)).toHaveCount(0);
    await expect(card(small.name)).toHaveCount(0);
    await expect
      .soft(by('icons-tab-status'))
      .toHaveText('Deleted 2 icons from the server.');
    for (const icon of [wide, small]) {
      expect((await request.get(iconPath(icon.name))).status()).toBe(404);
    }
  });

  expectNoFatal(issues);
});
