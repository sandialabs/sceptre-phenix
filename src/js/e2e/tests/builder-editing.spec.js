// Builder Beta canvas and editing commands: palette adds (click and drag),
// node and connection gestures, delete, clipboard, keyboard selection and
// nudging, groups, auto layout, and inspector edits of notes and groups.
//
// Every test starts from its own blank draft; the `tracker` fixture deletes it
// afterwards. Persisted state is read back through the drafts API so a test
// proves what autosave stored, not only what the canvas shows. Tests that
// check several variants on one draft run each variant as a test.step; a
// check that no later step depends on is soft, so one failure does not hide
// the steps after it.

const { test, expect, expectNoFatal } = require('./builder-support');

const SNAPSHOTS = /\/api\/v1\/builder\/drafts\/[^/]+\/[^/]+\/snapshots$/;

// Default snap grid of a new document: drops and drags land on it.
const GRID = 16;

// Soft polls: a failed check is reported without skipping the steps after it.
const softly = expect.configure({ soft: true });

// The header's counts, as read: "1 device, 0 switches, …".
function summaryText({
  devices = 0,
  switches = 0,
  networks = 0,
  links = 0,
  groups = 0,
  notes = 0,
}) {
  const count = (n, noun, plural = `${noun}s`) =>
    `${n} ${n === 1 ? noun : plural}`;

  return [
    count(devices, 'device'),
    count(switches, 'switch', 'switches'),
    count(networks, 'network'),
    count(links, 'connection'),
    count(groups, 'group'),
    count(notes, 'note'),
  ].join(', ');
}

async function blankDraft(builder) {
  await builder.open();

  return builder.createBlank();
}

// Resolves with the next successful autosave snapshot upload. Only use it once
// earlier edits are persisted, or it can resolve for one of them.
function nextSnapshot(page) {
  return page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      SNAPSHOTS.test(new URL(response.url()).pathname) &&
      response.ok(),
  );
}

// Polls the saved draft until `pick` returns `expected`. A document that
// does not have the picked part yet (for example a node still being saved)
// counts as "not yet", not as a failure. With `soft`, a mismatch is recorded
// and the test goes on.
async function expectPersisted(
  builder,
  draft,
  pick,
  expected,
  { soft = false } = {},
) {
  await (soft ? softly : expect)
    .poll(
      async () => {
        const doc = await builder.serverDocument(draft);

        try {
          return pick(doc);
        } catch {
          return undefined;
        }
      },
      { timeout: 20000 },
    )
    .toEqual(expected);
}

function devicesOf(doc) {
  return doc.nodes.filter((node) => node.kind === 'device');
}

function byHostname(doc, hostname) {
  return devicesOf(doc).find((node) => node.device.hostname === hostname);
}

function positions(doc) {
  return Object.fromEntries(
    doc.nodes.map((node) => [node.id, { ...node.position }]),
  );
}

function vlansOf(device) {
  return device.device.spec.network.interfaces.map(
    (iface) => `${iface.name}:${iface.vlan}`,
  );
}

function canvasNode(page, id) {
  return page.locator(`[data-testid="builder-node"][data-node-id="${id}"]`);
}

// Moves focus into the canvas so its keyboard shortcuts apply. A node's one
// Tab stop is Vue Flow's wrapper around it. Vue Flow keeps a new node
// `visibility: hidden` until it is measured, and focus() on a hidden element
// is silently ignored, so wait for it to show and confirm the focus.
async function focusNode(page, id) {
  const node = flowNode(page, id);
  await expect(node).toBeVisible();
  await node.focus();
  await expect(node).toBeFocused();
}

// Vue Flow's wrapper around a canvas node: the focusable, named element.
function flowNode(page, id) {
  return page.locator(`.vue-flow__node[data-id="${id}"]`);
}

function outlineRow(builder, name) {
  return builder.outline.getByRole('button', { name, exact: true });
}

async function onlyNodeId(builder, kind) {
  await expect(builder.nodes(kind)).toHaveCount(1);

  return builder.nodes(kind).getAttribute('data-node-id');
}

// Ids of the canvas nodes of one kind, in the order they were added.
async function nodeIds(builder, kind, count) {
  await expect(builder.nodes(kind)).toHaveCount(count);

  return builder
    .nodes(kind)
    .evaluateAll((nodes) => nodes.map((node) => node.dataset.nodeId));
}

// Id of the canvas node whose accessible name starts with this, e.g.
// "Device router" for "Device router, 0 connections".
async function nodeIdNamed(page, name) {
  const node = page.locator(`.vue-flow__node[aria-label^="${name},"]`);
  await expect(node, `one canvas node named "${name}"`).toHaveCount(1);

  return node.getAttribute('data-id');
}

function bus(page, switchId, side) {
  return page.locator(
    `.vue-flow__handle[data-nodeid="${switchId}"][data-handleid="bus"][data-handlepos="${side}"]`,
  );
}

function newInterface(page, deviceId) {
  return page.locator(
    `.vue-flow__handle[data-nodeid="${deviceId}"][data-handleid="new-interface"]`,
  );
}

function center(box) {
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

// Presses and releases the mouse between two points in small steps, the way
// a user drags. Vue Flow needs intermediate moves to start a drag.
async function drag(page, start, end, steps = 12) {
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  for (let step = 1; step <= steps; step += 1) {
    await page.mouse.move(
      start.x + ((end.x - start.x) * step) / steps,
      start.y + ((end.y - start.y) * step) / steps,
    );
  }
  await page.mouse.up();
}

async function dragBetween(page, from, to) {
  await drag(
    page,
    center(await from.boundingBox()),
    center(await to.boundingBox()),
  );
}

async function dragBy(page, locator, dx, dy) {
  const box = await locator.boundingBox();
  const start = { x: box.x + 30, y: box.y + 14 };

  await drag(page, start, { x: start.x + dx, y: start.y + dy });
}

// True when `work` settles within `ms`. A frozen renderer never acknowledges
// input or evaluation, and Playwright does not time out an input event it has
// already dispatched, so hang checks need their own bound.
async function settlesWithin(work, ms) {
  let timer;
  const expired = new Promise((resolve) => {
    timer = setTimeout(() => resolve(false), ms);
  });
  const settled = work.then(
    () => true,
    () => true,
  );
  const answer = await Promise.race([settled, expired]);
  clearTimeout(timer);

  return answer;
}

// JSON Forms text controls commit on `change`; blur() fires it in Chrome and
// Firefox alike (Tab can leave focus on the last field in Firefox).
async function commitField(locator, value) {
  await locator.fill(value);
  await locator.blur();
}

// Adds a device and a switch by click and connects them through the outline.
async function deviceOnSwitch(builder) {
  await builder.palette('device').click();
  await builder.palette('switch').click();
  await builder.connect();
  await builder.expectSummary('1 connection');

  return {
    deviceId: await onlyNodeId(builder, 'device'),
    switchId: await onlyNodeId(builder, 'switch'),
  };
}

// Clicks the first connection where its label is drawn: on the line, which
// takes the click through the label.
async function selectEdge(page) {
  const box = await page.locator('.builder-edge__label').first().boundingBox();
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
}

// Soft-checks that the one connection is drawn and answers whether it is. A
// step that clicks the connection skips its clicks when it is missing, so the
// failure is recorded and the node-only steps after it still run.
async function connectionDrawn(page) {
  const label = page.locator('.builder-edge__label');
  await expect
    .soft(label, 'the connection is drawn on the canvas')
    .toHaveCount(1);

  return (await label.count()) === 1;
}

test.describe('Builder Beta canvas editing', () => {
  test('adds every palette item and device template by click', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await blankDraft(builder);

    const steps = [
      ['device', 'device'],
      ['device', 'device'],
      ['switch', 'switch'],
      ['switch', 'switch'],
      ['note', 'note'],
      ['group', 'group'],
      ['template-server', 'device'],
      ['template-workstation', 'device'],
      ['template-router', 'device'],
      ['template-firewall', 'device'],
      ['template-external', 'device'],
      ['template-router', 'device'],
    ];
    const counts = { devices: 0, switches: 0, networks: 0, groups: 0 };
    const plural = { device: 'devices', switch: 'switches', group: 'groups' };

    for (const [item, kind] of steps) {
      await builder.palette(item).click();
      if (kind === 'note') {
        counts.notes = (counts.notes || 0) + 1;
      } else {
        counts[plural[kind]] += 1;
      }
      if (kind === 'switch') {
        counts.networks += 1;
      }
      await expect(builder.summary, `after adding ${item}`).toHaveText(
        summaryText(counts),
      );
    }

    await expect(builder.nodes('device')).toHaveCount(8);
    await expect(builder.nodes('switch')).toHaveCount(2);
    await expect(builder.nodes('note')).toHaveCount(1);
    await expect(builder.nodes('group')).toHaveCount(1);
    await expect(page.locator('[data-testid^="outline-item-"]')).toHaveCount(
      12,
    );

    for (const name of [
      'Group, 0 members',
      'Switch EXP, network EXP, 0 connections',
      'Switch EXP-2, network EXP-2, 0 connections',
      'Device node, 0 connections',
      'Device node-2, 0 connections',
      'Device server, 0 connections, comment: Generic Linux server',
      'Device workstation, 0 connections, comment: Operator workstation',
      'Device router, 0 connections, comment: Layer 3 router',
      'Device router-2, 0 connections, comment: Layer 3 router',
      'Device firewall, 0 connections, comment: Perimeter firewall',
      'Device external, 0 connections, comment: Hardware in the loop device',
      'Note',
    ]) {
      await expect(outlineRow(builder, name)).toBeVisible();
    }

    await expectPersisted(builder, draft, (doc) => doc.nodes.length, 12);
    const doc = await builder.serverDocument(draft);

    expect(
      devicesOf(doc)
        .map((node) => node.device.hostname)
        .sort(),
    ).toEqual([
      'external',
      'firewall',
      'node',
      'node-2',
      'router',
      'router-2',
      'server',
      'workstation',
    ]);
    expect(doc.networks.map((network) => network.name).sort()).toEqual([
      'EXP',
      'EXP-2',
    ]);

    const vm = 'VirtualMachine';
    const expected = {
      node: { type: vm, iconKey: 'linux', os: 'linux', image: 'ubuntu.qc2' },
      server: { type: vm, iconKey: 'server', os: 'linux', image: 'ubuntu.qc2' },
      workstation: {
        type: vm,
        iconKey: 'desktop',
        os: 'windows',
        image: 'windows10.qc2',
      },
      // phenix's vrouter app configures only these two types, and those
      // through a router OS type (R4).
      router: {
        type: 'Router',
        iconKey: 'router',
        os: 'minirouter',
        image: 'minirouter.qc2',
      },
      firewall: {
        type: 'Firewall',
        iconKey: 'firewall',
        os: 'minirouter',
        image: 'minirouter.qc2',
      },
    };
    for (const [hostname, want] of Object.entries(expected)) {
      const node = byHostname(doc, hostname);
      expect.soft(node.device.spec.type, hostname).toBe(want.type);
      expect.soft(node.device.iconKey, hostname).toBe(want.iconKey);
      expect.soft(node.device.spec.hardware.os_type, hostname).toBe(want.os);
      expect
        .soft(node.device.spec.hardware.drives[0].image, hostname)
        .toBe(want.image);
      expect.soft(node.device.interfaces, hostname).toEqual([]);
    }

    const external = byHostname(doc, 'external');
    expect(external.device.iconKey).toBe('external');
    expect(external.device.spec.external).toBe(true);
    expect(external.device.spec.type).toBe('HIL');

    // Click placement never puts a node on another (R94), after a deletion
    // either, and puts it in view after a pan.
    // Each kind's default size (model.js DEFAULT_SIZES).
    const SIZES = {
      device: [160, 96],
      switch: [180, 72],
      note: [200, 120],
      group: [320, 240],
    };
    const overlapping = (saved) =>
      saved.nodes.flatMap((a, i) =>
        saved.nodes.slice(i + 1).flatMap((b) => {
          const [wa, ha] = SIZES[a.kind];
          const [wb, hb] = SIZES[b.kind];

          return a.position.x < b.position.x + wb &&
            b.position.x < a.position.x + wa &&
            a.position.y < b.position.y + hb &&
            b.position.y < a.position.y + ha
            ? [`${a.kind} ${a.id} and ${b.kind} ${b.id}`]
            : [];
        }),
      );
    expect.soft(overlapping(doc)).toEqual([]);

    await test.step('a node added after a deletion or a pan lands clear, in view', async () => {
      await builder.selectInOutline('node-2');
      await builder.toolbar('delete').click();
      await builder.palette('device').click();
      await expect(builder.nodes('device')).toHaveCount(8);

      // Pan the diagram away by dragging the canvas where no node is.
      const pane = await page.locator('.vue-flow').boundingBox();
      const empty = await page.evaluate((box) => {
        for (let y = box.y + 20; y < box.y + box.height / 2; y += 12) {
          for (let x = box.x + 20; x < box.x + box.width - 20; x += 12) {
            if (
              document
                .elementFromPoint(x, y)
                ?.classList.contains('vue-flow__pane')
            ) {
              return { x, y };
            }
          }
        }

        return null;
      }, pane);
      expect(empty, 'an empty spot of the canvas').not.toBeNull();
      await drag(page, empty, { x: empty.x - 1500, y: empty.y - 900 });

      const palette = builder.palette('device');
      await palette.click();
      await expect(builder.nodes('device')).toHaveCount(9);
      const added = builder.nodes('device').last();
      await expect
        .poll(async () => {
          const box = await added.boundingBox();

          return (
            box.x >= pane.x &&
            box.y >= pane.y &&
            box.x + box.width <= pane.x + pane.width &&
            box.y + box.height <= pane.y + pane.height
          );
        }, 'the new node is in view')
        .toBe(true);
      await expect.soft(palette).toBeFocused();

      await expectPersisted(builder, draft, (saved) => saved.nodes.length, 13);
      expect.soft(overlapping(await builder.serverDocument(draft))).toEqual([]);
    });

    expectNoFatal(issues);
  });

  test(
    'drops generic palette items where they are dragged',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const draft = await blankDraft(builder);
      const pane = page.locator('.vue-flow__pane');
      const drops = {
        device: { x: 120, y: 120 },
        switch: { x: 480, y: 120 },
        note: { x: 120, y: 360 },
        group: { x: 480, y: 360 },
      };

      for (const [kind, targetPosition] of Object.entries(drops)) {
        await builder.palette(kind).dragTo(pane, { targetPosition });
        await expect(builder.nodes(kind)).toHaveCount(1);
      }

      await expect(builder.summary).toHaveText(
        summaryText({
          devices: 1,
          switches: 1,
          networks: 1,
          groups: 1,
          notes: 1,
        }),
      );

      await expectPersisted(builder, draft, (doc) => doc.nodes.length, 4);
      const doc = await builder.serverDocument(draft);

      for (const [kind, target] of Object.entries(drops)) {
        const node = doc.nodes.find((entry) => entry.kind === kind);
        expect
          .soft(Math.abs(node.position.x - target.x), `${kind} x`)
          .toBeLessThanOrEqual(GRID);
        expect
          .soft(Math.abs(node.position.y - target.y), `${kind} y`)
          .toBeLessThanOrEqual(GRID);
      }
      expect(byHostname(doc, 'node').device.spec.hardware.os_type).toBe(
        'linux',
      );

      expectNoFatal(issues);
    },
  );

  test(
    'dragging a device template drops that template',
    { tag: '@cross-browser' },
    async ({ page, builder }) => {
      const draft = await blankDraft(builder);

      await builder
        .palette('template-router')
        .dragTo(page.locator('.vue-flow__pane'), {
          targetPosition: { x: 200, y: 200 },
        });
      await expect(builder.nodes('device')).toHaveCount(1);
      await expectPersisted(builder, draft, (doc) => doc.nodes.length, 1);

      // The node a click on the template adds (R46).
      const [device] = devicesOf(await builder.serverDocument(draft));
      expect(device.device.hostname).toBe('router');
      expect(device.device.iconKey).toBe('router');
      expect(device.device.spec.type).toBe('Router');
      expect(device.device.spec.hardware.os_type).toBe('minirouter');
      expect(device.device.spec.hardware.drives[0].image).toBe(
        'minirouter.qc2',
      );
    },
  );

  test(
    'dragging connected nodes moves them, keeps the connection and saves the positions',
    { tag: '@cross-browser' },
    async ({ page, builder }) => {
      const draft = await blankDraft(builder);
      const { deviceId, switchId } = await deviceOnSwitch(builder);
      await expectPersisted(builder, draft, (doc) => doc.edges.length, 1);
      const before = await builder.serverDocument(draft);
      const edge = page.locator('path.builder-edge[data-network="EXP"]');
      await expect
        .soft(edge, 'the connection is drawn on the canvas')
        .toHaveCount(1);
      const pathBefore = (await edge.count())
        ? await edge.getAttribute('d')
        : null;

      // Short moves: near the canvas edge Vue Flow auto-pans, which would
      // change the distance travelled. Each move is one saved snapshot.
      for (const [id, dx, dy, name] of [
        [deviceId, 160, 128, 'node'],
        [switchId, -48, 160, 'EXP'],
      ]) {
        const saved = nextSnapshot(page);
        await dragBy(page, canvasNode(page, id), dx, dy);
        // Named after the node that moved.
        await expect.soft(builder.liveRegion).toContainText(`Moved ${name}`);
        await saved;
      }

      // A click whose pointer wobbles less than a grid step leaves the
      // switch where it is, so it is a click: it deselects the switch, the
      // only selected node, instead of reporting a move that did not happen.
      const exp = flowNode(page, switchId);
      await expect.soft(exp).toHaveAttribute('aria-pressed', 'true');
      await dragBy(page, canvasNode(page, switchId), 2, 0);
      await expect.soft(exp).toHaveAttribute('aria-pressed', 'false');
      await expect.soft(builder.liveRegion).toContainText('Deselected EXP');

      // Still connected, and the line follows the moved nodes.
      await expect.soft(builder.summary).toContainText('1 connection');
      if (pathBefore !== null) {
        await softly
          .poll(() => edge.getAttribute('d'), {
            message: 'the connection line follows the moved nodes',
          })
          .not.toBe(pathBefore);
      }

      const after = await builder.serverDocument(draft);
      const at = (doc, id) => doc.nodes.find((node) => node.id === id).position;
      expect
        .soft(
          Math.abs(at(after, deviceId).x - at(before, deviceId).x - 160),
          'device x moved by the drag',
        )
        .toBeLessThanOrEqual(GRID);
      expect
        .soft(
          Math.abs(at(after, deviceId).y - at(before, deviceId).y - 128),
          'device y moved by the drag',
        )
        .toBeLessThanOrEqual(GRID);
      expect
        .soft(
          Math.abs(at(after, switchId).y - at(before, switchId).y - 160),
          'switch y moved by the drag',
        )
        .toBeLessThanOrEqual(GRID);
      expect.soft(after.edges, 'saved connections').toEqual(before.edges);
      expect.soft(after.networks, 'saved networks').toEqual(before.networks);
    },
  );

  test(
    'drag-to-connect joins devices and switches from either end',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const draft = await blankDraft(builder);
      const subject = builder.inspector.locator('.builder-inspector__subject');
      // Each connection point's name and network, without its buttons.
      const rows = builder.inspector.locator(
        '.builder-inspector__ifaces li .builder-inspector__iface-name',
      );
      const notice = page.getByTestId('canvas-notice');

      // Left to right: two switches, a plain device and a router. The palette
      // leaves the router, added last, selected in the inspector.
      for (const item of ['switch', 'switch', 'device', 'template-router']) {
        await builder.palette(item).click();
      }
      const exp = await nodeIdNamed(page, 'Switch EXP');
      const exp2 = await nodeIdNamed(page, 'Switch EXP-2');
      const nodeId = await nodeIdNamed(page, 'Device node');
      const routerId = await nodeIdNamed(page, 'Device router');
      await expect(subject).toContainText('Device router');

      await test.step('two switches are refused and the canvas says why', async () => {
        await dragBetween(
          page,
          bus(page, exp, 'right'),
          canvasNode(page, exp2),
        );

        await expect
          .soft(builder.liveRegion)
          .toContainText('Two switches cannot be connected');
        // Sighted users see why nothing happened, too. The notice has no
        // time limit; it stays until it is dismissed (WCAG 2.2.1).
        await expect
          .soft(notice)
          .toContainText('Two switches cannot be connected');
        await builder.expectSummary('0 connections');
        await notice.getByRole('button', { name: 'Dismiss message' }).click();
        await expect.soft(notice).toHaveCount(0);
        await expect.soft(builder.canvas).toBeFocused();
      });

      await test.step('an interface handle dropped on a switch bus uses that interface', async () => {
        await builder.inspector.getByTestId('inspector-add-interface').click();
        await expect(
          page.locator(
            `.vue-flow__handle[data-nodeid="${routerId}"]:not([data-handleid="new-interface"])`,
          ),
        ).toHaveCount(2);
        const source = page.locator(
          `.vue-flow__handle[data-nodeid="${routerId}"][data-handlepos="right"][data-interface="eth0"]`,
        );
        await expect
          .soft(source)
          .toHaveAttribute('title', 'eth0, not connected');

        await dragBetween(page, source, bus(page, exp2, 'left'));

        // The next step releases beside this now used interface.
        await expect(builder.summary).toHaveText(
          summaryText({ devices: 2, switches: 2, networks: 2, links: 1 }),
        );
        await expect
          .soft(source)
          .toHaveAttribute('title', 'eth0 on network EXP-2');
        await expect.soft(rows).toHaveText(['eth0 — network EXP-2']);
        await expect
          .soft(
            outlineRow(
              builder,
              'Device router, 1 connection, on EXP-2, comment: Layer 3 router',
            ),
          )
          .toBeVisible();
        await expect
          .soft(
            page.getByRole('button', {
              name: 'Network EXP-2 from router to EXP-2',
            }),
            'the connection is drawn as a labelled edge',
          )
          .toBeVisible();
        await expect
          .soft(page.locator('path.builder-edge[data-network="EXP-2"]'))
          .toHaveCount(1);

        // Deselect the router, so the inspector first renders it with two
        // interfaces in the guarded step below, not during the next drag.
        await page
          .locator('.vue-flow__pane')
          .click({ position: { x: 400, y: 600 } });
        await expect(subject).toHaveText(/^\s*Diagram\b/);
      });

      await test.step('a switch released beside a used interface adds a new one', async () => {
        const used = await page
          .locator(
            `.vue-flow__handle[data-nodeid="${routerId}"][data-handlepos="left"]:not([data-handleid="new-interface"])`,
          )
          .boundingBox();
        await drag(page, center(await bus(page, exp, 'right').boundingBox()), {
          x: used.x + 20,
          y: used.y + used.height / 2,
        });

        await builder.expectSummary('2 connections');
        await expect.soft(notice).toHaveCount(0);
      });

      await test.step('a switch dropped on a device without interfaces adds one', async () => {
        const device = canvasNode(page, nodeId);
        await expect.soft(device).toContainText('0 interfaces');

        await dragBetween(
          page,
          bus(page, exp2, 'right'),
          device.locator('.builder-node__header'),
        );

        await builder.expectSummary('3 connections');
        await expect.soft(device).toContainText('1 interface');
      });

      await test.step('a new-interface handle dropped on a switch adds an interface', async () => {
        const plus = newInterface(page, nodeId);
        await expect
          .soft(plus)
          .toHaveAttribute('title', 'Drag to connect node on a new interface');

        await dragBetween(page, plus, canvasNode(page, exp));

        await builder.expectSummary('4 connections');
        await expect
          .soft(canvasNode(page, nodeId))
          .toContainText('2 interfaces');
      });

      await test.step('selecting a device with two connected interfaces keeps the editor responsive', async () => {
        // Every edit is saved before the page can freeze, so no late autosave
        // races the tracker's delete.
        await expectPersisted(builder, draft, (doc) => doc.edges.length, 4);
        await builder.waitSaved();
        // Nothing is selected since the router was deselected above, so this
        // click is the first time the inspector renders two interfaces.
        await expect(subject).toHaveText(/^\s*Diagram\b/);

        const clicked = canvasNode(page, routerId).click();
        clicked.catch(() => {});
        await settlesWithin(clicked, 5000);
        // Ask the page separately, so a click that is merely blocked (not a
        // hung renderer) fails below with its own error instead of reading as
        // a hang.
        const responsive = await settlesWithin(
          page.evaluate(() => true),
          3000,
        );
        if (!responsive) {
          // Close the frozen page so failure screenshots and trace snapshots
          // do not stall until the test timeout.
          await page.close();
        }
        expect(responsive, 'page answers after selecting the router').toBe(
          true,
        );
        await clicked;
        await expect.soft(subject).toContainText('Device router');
        await expect
          .soft(rows)
          .toHaveText(['eth0 — network EXP-2', 'eth1 — network EXP']);
        // The JSON Forms device form renders the second interface, too.
        await expect
          .soft(builder.inspector.locator('input[value="eth1"]').first())
          .toBeVisible();
      });

      await test.step('the saved document has each interface on its network', async () => {
        const doc = await builder.serverDocument(draft);
        const router = byHostname(doc, 'router');
        const networkId = (name) =>
          doc.networks.find((network) => network.name === name).id;

        expect
          .soft(vlansOf(router), 'router')
          .toEqual(['eth0:EXP-2', 'eth1:EXP']);
        expect
          .soft(vlansOf(byHostname(doc, 'node')), 'node')
          .toEqual(['eth0:EXP-2', 'eth1:EXP']);
        // Drags add connection handles in step with the spec interfaces.
        const handles = (device) =>
          device?.device.interfaces.map((handle) => handle.name);
        expect
          .soft(handles(router), 'router handles')
          .toEqual(['eth0', 'eth1']);
        expect
          .soft(handles(byHostname(doc, 'node')), 'node handles')
          .toEqual(['eth0', 'eth1']);
        // The handle-to-bus drag connected the dragged interface.
        expect
          .soft(
            doc.edges.find(
              (edge) => edge.sourceHandleId === router.device.interfaces[0].id,
            ),
            'edge of the dragged interface',
          )
          .toMatchObject({
            sourceNodeId: routerId,
            targetNodeId: exp2,
            networkId: networkId('EXP-2'),
          });
        // Every connection is on the network of the switch it reaches.
        for (const edge of doc.edges) {
          const end = doc.nodes.find(
            (node) =>
              node.kind === 'switch' &&
              [edge.sourceNodeId, edge.targetNodeId].includes(node.id),
          );
          expect
            .soft(edge.networkId, `network of edge ${edge.id}`)
            .toBe(end?.switch.networkId);
        }
      });

      expectNoFatal(issues);
    },
  );

  test('dragging between devices adds a switch; releasing on a group or note cancels quietly', async ({
    page,
    builder,
  }) => {
    const draft = await blankDraft(builder);
    for (const item of ['device', 'device', 'group', 'note']) {
      await builder.palette(item).click();
    }
    const [first, second] = await nodeIds(builder, 'device', 2);
    const plus = newInterface(page, first);

    await test.step('releasing on a group or a note is a quiet cancel', async () => {
      for (const kind of ['group', 'note']) {
        await dragBetween(page, plus, builder.nodes(kind));
      }

      // The next step counts the connections it adds from here.
      await builder.expectSummary('0 connections');
      await expect.soft(page.getByTestId('canvas-notice')).toHaveCount(0);
    });

    await test.step('dragging between two devices joins them through a new switch', async () => {
      await dragBetween(
        page,
        plus,
        canvasNode(page, second).locator('.builder-node__header'),
      );

      await expect(builder.summary).toHaveText(
        summaryText({
          devices: 2,
          switches: 1,
          networks: 1,
          links: 2,
          groups: 1,
          notes: 1,
        }),
      );
      await expect
        .soft(builder.liveRegion)
        .toContainText('Connected devices through a new switch');
      await expectPersisted(builder, draft, (doc) => doc.edges.length, 2);
      const doc = await builder.serverDocument(draft);
      const network = doc.networks[0];
      for (const hostname of ['node', 'node-2']) {
        expect
          .soft(vlansOf(byHostname(doc, hostname)), hostname)
          .toEqual([`eth0:${network.name}`]);
      }
    });

    await test.step('one undo removes the switch and both connections', async () => {
      await focusNode(page, first);
      await page.keyboard.press('ControlOrMeta+z');
      await expect(builder.summary).toHaveText(
        summaryText({ devices: 2, groups: 1, notes: 1 }),
      );
    });
  });

  test('edits and deletes canvas selections: notes, groups, connections and everything', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await blankDraft(builder);
    const subject = builder.inspector.locator('.builder-inspector__subject');
    const { deviceId } = await deviceOnSwitch(builder);
    await builder.palette('note').click();
    await builder.palette('group').click();
    const noteId = await onlyNodeId(builder, 'note');
    const groupId = await onlyNodeId(builder, 'group');
    const label = page.locator('.builder-edge__label');
    const rest = { devices: 1, switches: 1, networks: 1, groups: 1, notes: 1 };

    await test.step('a clicked note and group are edited in the inspector', async () => {
      await expect
        .soft(canvasNode(page, noteId))
        .toContainText('Select the note and use the inspector to add text.');
      await canvasNode(page, noteId).click();
      await expect(subject).toContainText('Note');
      await commitField(
        builder.inspector.getByLabel('Text'),
        'Remember uplink',
      );
      await commitField(
        builder.inspector.getByLabel('Color', { exact: true }),
        '#b00020',
      );
      await builder.apply();
      await expect
        .soft(canvasNode(page, noteId))
        .toContainText('Remember uplink');
      // Named after its text, once (N15), and drawn in its color (R93).
      await expect
        .soft(flowNode(page, noteId))
        .toHaveAccessibleName('Note: Remember uplink');
      await expect
        .soft(canvasNode(page, noteId).locator('.builder-node__accent'))
        .toHaveCSS('background-color', 'rgb(176, 0, 32)');

      await canvasNode(page, groupId).click({ position: { x: 8, y: 8 } });
      await expect(subject).toContainText('Group');
      await commitField(builder.inspector.getByLabel('Title'), 'DMZ');
      await commitField(
        builder.inspector.getByLabel('Color', { exact: true }),
        '#1f7a5a',
      );
      await builder.apply();
      await expect
        .soft(flowNode(page, groupId))
        .toHaveAccessibleName('Group DMZ, 0 members');
      await expect.soft(canvasNode(page, groupId)).toContainText('DMZ');
      await expect
        .soft(outlineRow(builder, 'Group DMZ, 0 members'))
        .toBeVisible();
      await expect
        .soft(canvasNode(page, groupId).locator('.builder-node__accent'))
        .toHaveCSS('background-color', 'rgb(31, 122, 90)');

      await expectPersisted(
        builder,
        draft,
        (doc) =>
          doc.nodes
            .filter((node) => ['note', 'group'].includes(node.kind))
            .map((node) =>
              node.kind === 'note'
                ? { kind: node.kind, label: node.label, note: node.note.text }
                : {
                    kind: node.kind,
                    label: node.label,
                    title: node.group.title,
                  },
            ),
        [
          // Not "Note": named after its first line (model.nodeLabel).
          { kind: 'note', label: undefined, note: 'Remember uplink' },
          { kind: 'group', label: 'DMZ', title: 'DMZ' },
        ],
        { soft: true },
      );
    });

    await test.step("a network's color is its connections' and its switch's (R93)", async () => {
      await builder.selectInOutline('EXP');
      await commitField(
        builder.inspector.getByLabel('Color', { exact: true }),
        '#ff00ff',
      );
      await builder.apply();

      // Still with its dash pattern and label beside it.
      await expect
        .soft(page.locator('path.builder-edge'))
        .toHaveCSS('stroke', 'rgb(255, 0, 255)');
      await expect.soft(label).toHaveText('EXP');
      await expect
        .soft(builder.nodes('switch').locator('.builder-node__swatch'))
        .toHaveCSS('background-color', 'rgb(255, 0, 255)');
    });

    await test.step('select all selects every node and connection', async () => {
      await focusNode(page, deviceId);
      await page.keyboard.press('ControlOrMeta+a');

      await expect
        .soft(builder.liveRegion)
        .toContainText('Selected everything.');
      await expect
        .soft(page.locator('.vue-flow__node.selected'), 'selected nodes')
        .toHaveCount(4);
      await expect
        .soft(page.locator('.vue-flow__edge.selected'), 'selected connections')
        .toHaveCount(1);
      await expect
        .soft(
          page.locator('[data-testid^="outline-item-"][aria-pressed="true"]'),
          'pressed outline rows',
        )
        .toHaveCount(4);
    });

    await test.step('a clicked connection is relabelled, and cleared back to its network', async () => {
      if (!(await connectionDrawn(page))) {
        return;
      }
      await expect.soft(label).toHaveText('EXP');
      // Selecting the edge replaces the node selection.
      await selectEdge(page);
      await expect
        .soft(page.locator('.vue-flow__edge.selected'))
        .toHaveCount(1);
      await expect
        .soft(page.locator('.vue-flow__node.selected'))
        .toHaveCount(0);
      await expect(subject).toContainText('Connection');

      const field = builder.inspector.getByLabel('Label');
      await commitField(field, 'uplink');
      await builder.apply();
      await expect.soft(label).toHaveText('uplink');
      await expectPersisted(
        builder,
        draft,
        (doc) => doc.edges[0].label,
        'uplink',
        { soft: true },
      );

      // Clearing the label falls back to the network name, which the
      // field shows again.
      await commitField(field, '');
      await builder.apply();
      await expect.soft(label).toHaveText('EXP');
      await expect.soft(field).toHaveValue('EXP');
      await expectPersisted(
        builder,
        draft,
        (doc) => 'label' in doc.edges[0],
        false,
        { soft: true },
      );
    });

    await test.step('the toolbar deletes the selected connection and keeps the interface', async () => {
      if (!(await connectionDrawn(page))) {
        return;
      }
      // The connection is still selected from the step before. It is a
      // toggle: a click on the only selected item deselects it, and the next
      // click selects it again.
      const selected = page.locator('.vue-flow__edge.selected');
      await expect.soft(selected).toHaveCount(1);
      await selectEdge(page);
      await expect.soft(selected).toHaveCount(0);
      await expect
        .soft(builder.liveRegion)
        .toContainText('Deselected the connection between node and EXP');
      await selectEdge(page);
      // The toolbar deletes whatever is selected.
      await expect(selected).toHaveCount(1);
      await builder.toolbar('delete').click();

      await expect.soft(builder.summary).toHaveText(summaryText(rest));
      await expect.soft(page.locator('.vue-flow__edge')).toHaveCount(0);
      await expect
        .soft(builder.liveRegion)
        .toContainText('Deleted the connection between node and EXP');

      // The interface stays on the device, now unconnected.
      await canvasNode(page, deviceId).click();
      await expect
        .soft(
          builder.inspector.locator(
            '.builder-inspector__ifaces li .builder-inspector__iface-name',
          ),
        )
        .toHaveText(['eth0 — not connected']);
      await expectPersisted(
        builder,
        draft,
        (doc) => ({
          edges: doc.edges.length,
          vlans: byHostname(doc, 'node').device.spec.network.interfaces.map(
            (iface) => iface.vlan,
          ),
        }),
        { edges: 0, vlans: [''] },
        { soft: true },
      );
    });

    await test.step('select all and Delete remove every node', async () => {
      await focusNode(page, deviceId);
      await page.keyboard.press('ControlOrMeta+a');
      await page.keyboard.press('Delete');

      await expect
        .soft(builder.summary)
        .toHaveText(summaryText({ networks: 1 }));
      await expectPersisted(
        builder,
        draft,
        (doc) => [doc.nodes.length, doc.edges.length],
        [0, 0],
        { soft: true },
      );
    });

    expectNoFatal(issues);
  });

  test(
    'a connection takes focus: Delete removes a clicked one, Enter selects a focused one',
    { tag: '@cross-browser' },
    async ({ page, builder }) => {
      const draft = await blankDraft(builder);
      await deviceOnSwitch(builder);
      const subject = builder.inspector.locator('.builder-inspector__subject');
      const edge = page.locator('g.vue-flow__edge');
      await expect(page.locator('.builder-edge__label')).toHaveText('EXP');
      // Handles are 12px targets, and a switch's bus is square: Vue Flow's
      // round 6px default does not win (R96).
      await expect
        .soft(page.locator('.builder-handle--bus').first())
        .toHaveCSS('border-radius', '2px');
      await expect
        .soft(page.locator('.builder-handle--bus').first())
        .toHaveCSS('width', '12px');

      await test.step('a clicked connection takes focus, so Delete removes it', async () => {
        // A click beside the line, clear of its label, reaches it (R97).
        const beside = await page
          .locator('path.builder-edge')
          .evaluate((path) => {
            const at = path.getPointAtLength(path.getTotalLength() / 4);
            const ctm = path.getScreenCTM();

            return { x: at.x * ctm.a + ctm.e + 6, y: at.y * ctm.d + ctm.f + 6 };
          });
        await page.mouse.click(beside.x, beside.y);
        await expect(subject).toContainText('Connection');
        await expect(edge).toBeFocused();
        await page.keyboard.press('Delete');

        await expect(builder.summary).toHaveText(
          summaryText({ devices: 1, switches: 1, networks: 1 }),
        );
        await expectPersisted(builder, draft, (doc) => doc.edges.length, 0);
        // Focus moves on to a node, so Ctrl+Z still reaches the canvas.
        await expect(page.locator('.vue-flow__node:focus')).toHaveCount(1);
        await page.keyboard.press('ControlOrMeta+z');
        await expect(edge).toHaveCount(1);
      });

      await test.step('Enter toggles a focused connection and Escape clears it', async () => {
        await edge.focus();
        await expect(edge).toHaveAttribute('aria-pressed', 'false');
        await page.keyboard.press('Enter');
        await expect(edge).toHaveAttribute('aria-pressed', 'true');
        await expect(subject).toContainText('Connection');

        // Enter on the only selected item deselects it; Space selects again.
        await page.keyboard.press('Enter');
        await expect(edge).toHaveAttribute('aria-pressed', 'false');
        await expect
          .soft(builder.liveRegion)
          .toContainText('Deselected the connection between node and EXP');
        await page.keyboard.press(' ');
        await expect(edge).toHaveAttribute('aria-pressed', 'true');

        await page.keyboard.press('Escape');
        await expect(edge).toHaveAttribute('aria-pressed', 'false');
        await expect(edge).toBeFocused();
      });
    },
  );

  test('deletes devices with Delete, Backspace and the toolbar, and a network from the outline', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await blankDraft(builder);
    for (const item of ['device', 'device', 'device', 'switch']) {
      await builder.palette(item).click();
    }
    await builder.connect({ label: 'node' }, { index: 1 });
    await builder.connect({ label: 'node-3' }, { index: 1 });
    await expect(builder.summary).toHaveText(
      summaryText({ devices: 3, switches: 1, networks: 1, links: 2 }),
    );
    const [first, second, third] = await nodeIds(builder, 'device', 3);

    await test.step('Delete removes a connected device and its connection', async () => {
      await canvasNode(page, first).click();
      await page.keyboard.press('Delete');
      await expect(builder.summary).toHaveText(
        summaryText({ devices: 2, switches: 1, networks: 1, links: 1 }),
      );
      await expect.soft(canvasNode(page, first)).toHaveCount(0);
    });

    await test.step('Backspace removes a device', async () => {
      await canvasNode(page, second).click();
      await page.keyboard.press('Backspace');
      await expect(builder.summary).toHaveText(
        summaryText({ devices: 1, switches: 1, networks: 1, links: 1 }),
      );
    });

    await test.step('removing a network in the outline removes its switch and connections', async () => {
      await page.getByRole('button', { name: 'Remove network EXP' }).click();

      await expect(builder.summary).toHaveText(summaryText({ devices: 1 }));
      await expect.soft(builder.nodes('switch')).toHaveCount(0);
      await expect.soft(page.locator('.vue-flow__edge')).toHaveCount(0);
      await expect
        .soft(page.getByTestId('builder-networks').locator('li'))
        .toHaveCount(0);
      await expect
        .soft(outlineRow(builder, 'Device node-3, 0 connections'))
        .toBeVisible();
      await expectPersisted(
        builder,
        draft,
        (doc) => ({
          kinds: doc.nodes.map((node) => node.kind),
          networks: doc.networks.length,
          edges: doc.edges.length,
          vlans: byHostname(doc, 'node-3').device.spec.network.interfaces.map(
            (iface) => iface.vlan,
          ),
        }),
        { kinds: ['device'], networks: 0, edges: 0, vlans: [''] },
        { soft: true },
      );
    });

    await test.step('the toolbar deletes the selected device', async () => {
      await canvasNode(page, third).click();
      await builder.toolbar('delete').click();
      await expect.soft(builder.summary).toHaveText(summaryText({}));
      await expect.soft(builder.toolbar('delete')).toBeDisabled();
      await expect
        .soft(page.locator('[data-testid^="outline-item-"]'))
        .toHaveCount(0);
      await expectPersisted(builder, draft, (doc) => doc.nodes.length, 0, {
        soft: true,
      });
    });

    expectNoFatal(issues);
  });

  test('groups a selection, then moves, duplicates, deletes and ungroups groups', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await blankDraft(builder);
    await builder.palette('device').click();
    await builder.palette('device').click();
    await builder.palette('switch').click();
    await builder.connect({ label: 'node' }, { index: 1 });
    await builder.expectSummary('1 connection');
    const [first, second] = await nodeIds(builder, 'device', 2);
    const members = [first, second];
    const wired = { devices: 2, switches: 1, networks: 1, links: 1 };
    let groupId;
    let grouped;
    let copyGroupId;

    await test.step('Shift+click and Group wrap two devices in a group', async () => {
      await canvasNode(page, first).click();
      await canvasNode(page, second).click({ modifiers: ['Shift'] });
      await expect(page.locator('.vue-flow__node.selected')).toHaveCount(2);
      await builder.toolbar('group').click();

      await expect.soft(builder.liveRegion).toContainText('Grouped selection');
      await expect(builder.summary).toHaveText(
        summaryText({ ...wired, groups: 1 }),
      );
      groupId = await onlyNodeId(builder, 'group');
      await expect
        .soft(flowNode(page, groupId))
        .toHaveAccessibleName('Group, 2 members');
      // Grouping leaves the new group selected; the next steps act on it.
      await expect(outlineRow(builder, 'Group, 2 members')).toHaveAttribute(
        'aria-pressed',
        'true',
      );

      // The outline lists the members right after their group, in a list
      // nested in the group's list item, and indented.
      const outline = () =>
        page.locator('[data-testid^="outline-item-"]').evaluateAll((rows) =>
          rows.map((row) => {
            let level = 0;
            for (let node = row; node; node = node.parentElement) {
              level += node.tagName === 'UL' ? 1 : 0;
            }

            return {
              name: row.getAttribute('aria-label'),
              level,
              indent: row.getBoundingClientRect().left,
            };
          }),
        );
      await softly
        .poll(async () => (await outline()).map((row) => row.name), {
          message: 'outline rows in order',
        })
        .toEqual([
          'Group, 2 members',
          'Device node, in Group, 1 connection, on EXP',
          'Device node-2, in Group, 0 connections',
          'Switch EXP, network EXP, 1 connection',
        ]);
      const rows = await outline();
      expect
        .soft(
          rows.map((row) => row.level),
          'list nesting level',
        )
        .toEqual([1, 2, 2, 1]);
      expect
        .soft(rows[1].indent, 'member row is indented')
        .toBeGreaterThan(rows[0].indent);
      expect
        .soft(rows[2].indent, 'members share one level')
        .toBe(rows[1].indent);
      expect
        .soft(rows[3].indent, 'the switch stays top level')
        .toBe(rows[0].indent);

      await expectPersisted(
        builder,
        draft,
        (doc) => devicesOf(doc).map((node) => node.parentId),
        [groupId, groupId],
      );
      grouped = await builder.serverDocument(draft);
      const group = grouped.nodes.find((node) => node.id === groupId);
      for (const node of devicesOf(grouped)) {
        const name = node.device.hostname;
        expect
          .soft(node.position.x, name)
          .toBeGreaterThanOrEqual(group.position.x);
        expect
          .soft(node.position.y, name)
          .toBeGreaterThanOrEqual(group.position.y);
        expect
          .soft(node.position.x + 160, name)
          .toBeLessThanOrEqual(group.position.x + group.size.width);
        expect
          .soft(node.position.y + 96, name)
          .toBeLessThanOrEqual(group.position.y + group.size.height);
      }
    });

    await test.step('an arrow key moves the group together with its members', async () => {
      const before = positions(grouped);
      const moved = (id) => ({ x: before[id].x + 10, y: before[id].y });

      await focusNode(page, groupId);
      await page.keyboard.press('ArrowRight');

      await expectPersisted(
        builder,
        draft,
        positions,
        {
          ...before,
          [groupId]: moved(groupId),
          [first]: moved(first),
          [second]: moved(second),
        },
        { soft: true },
      );
    });

    await test.step('the outline moves a member out of its group and back, and Alt+Shift+arrows resize the group (R56)', async () => {
      const node = page.locator('#regroup-node');
      const group = page.locator('#regroup-group');
      const move = page.getByTestId('outline-regroup-submit');
      const alert = page.getByTestId('outline-regroup').getByRole('alert');
      // Each kind's default size (model.js DEFAULT_SIZES).
      const sizes = { device: [160, 96], switch: [180, 72] };
      const membership = (doc) => {
        const box = (id) => {
          const found = doc.nodes.find((entry) => entry.id === id);
          const [width, height] = sizes[found.kind] || [0, 0];

          return {
            ...found.position,
            width: found.size?.width ?? width,
            height: found.size?.height ?? height,
          };
        };
        const [g, n] = [box(groupId), box(second)];
        const apart = (a, b) =>
          a.x >= b.x + b.width ||
          a.y >= b.y + b.height ||
          a.x + a.width <= b.x ||
          a.y + a.height <= b.y;

        return {
          parent: doc.nodes.find((entry) => entry.id === second).parentId,
          inside:
            n.x >= g.x &&
            n.y >= g.y &&
            n.x + n.width <= g.x + g.width &&
            n.y + n.height <= g.y + g.height,
          outside: apart(n, g),
          // It lands on no other node (R56).
          clear: doc.nodes
            .filter((entry) => entry.kind !== 'group' && entry.id !== second)
            .every((entry) => apart(box(entry.id), n)),
        };
      };

      await node.selectOption({ label: 'node-2 (device)' });
      // The form shows the group the node is in now.
      await expect.soft(group).toHaveValue(groupId);
      await group.selectOption({ label: 'No group' });
      await move.click();
      await expect
        .soft(builder.liveRegion)
        .toContainText('Removed node-2 from its group');
      await expect
        .soft(outlineRow(builder, 'Device node-2, 0 connections'))
        .toBeVisible();
      await expectPersisted(builder, draft, membership, {
        parent: undefined,
        inside: false,
        outside: true,
        clear: true,
      });

      // A move to where it is already changes nothing, and says why.
      await move.click();
      await expect.soft(alert).toHaveText('node-2 is in no group already.');

      await group.selectOption({ label: 'Group' });
      await move.click();
      await expect
        .soft(builder.liveRegion)
        .toContainText('Added node-2 to group Group');
      await expect.soft(alert).toHaveCount(0);
      await expect.soft(move).toBeFocused();
      await expectPersisted(builder, draft, membership, {
        parent: groupId,
        inside: true,
        outside: false,
        clear: true,
      });

      // The group is still selected. Right and Down grow it, Left and Up
      // shrink it, never smaller than its members need.
      const sizeOf = (doc) =>
        doc.nodes.find((entry) => entry.id === groupId).size;
      const before = sizeOf(await builder.serverDocument(draft));
      await focusNode(page, groupId);
      await page.keyboard.press('Alt+Shift+ArrowRight');
      await expect
        .soft(builder.liveRegion)
        .toContainText(
          `Resized Group to ${before.width + 10} by ${before.height}`,
        );
      await page.keyboard.press('Alt+Shift+ArrowLeft');
      await expectPersisted(builder, draft, sizeOf, before);
      for (let press = 0; press < 6; press += 1) {
        await page.keyboard.press('Alt+Shift+ArrowUp');
      }
      await expect
        .soft(builder.liveRegion)
        .toContainText('Group cannot be smaller: its members need the room.');
      await builder.waitSaved();
      grouped = await builder.serverDocument(draft);
      expect.soft(sizeOf(grouped).height).toBeLessThan(before.height);
      expect.soft(membership(grouped).inside).toBe(true);
    });

    await test.step('Duplicate copies a group with its members', async () => {
      await page.keyboard.press('ControlOrMeta+d');
      await expect(builder.summary).toHaveText(
        summaryText({ ...wired, devices: 4, groups: 2 }),
      );

      await expectPersisted(builder, draft, (doc) => doc.nodes.length, 7);
      const doc = await builder.serverDocument(draft);
      copyGroupId = doc.nodes.find(
        (node) => node.kind === 'group' && node.id !== groupId,
      ).id;
      const parents = (inGroup) =>
        devicesOf(doc)
          .filter((node) => members.includes(node.id) === inGroup)
          .map((node) => node.parentId);
      expect
        .soft(parents(false), 'copied members')
        .toEqual([copyGroupId, copyGroupId]);
      expect
        .soft(parents(true), 'original members')
        .toEqual([groupId, groupId]);
    });

    await test.step('deleting a group removes its members and their connections', async () => {
      // The copy sits 40px lower right, so this corner is the original's.
      await canvasNode(page, groupId).click({ position: { x: 8, y: 8 } });
      await expect(flowNode(page, groupId)).toHaveClass(/\bselected\b/);
      await expect(page.locator('.vue-flow__node.selected')).toHaveCount(1);
      await builder.toolbar('delete').click();

      await expect(builder.summary).toHaveText(
        summaryText({ devices: 2, switches: 1, networks: 1, groups: 1 }),
      );
      await expect.soft(canvasNode(page, first)).toHaveCount(0);
      await expect.soft(canvasNode(page, second)).toHaveCount(0);
      await expect.soft(page.locator('.vue-flow__edge')).toHaveCount(0);
      // Hard: the next step reads the saved document as its starting point.
      await expectPersisted(
        builder,
        draft,
        (doc) => ({
          groups: doc.nodes
            .filter((node) => node.kind === 'group')
            .map((node) => node.id),
          members: doc.nodes.filter((node) => members.includes(node.id)).length,
          edges: doc.edges.length,
        }),
        { groups: [copyGroupId], members: 0, edges: 0 },
      );
    });

    await test.step('Ungroup frees the members where they are', async () => {
      const before = await builder.serverDocument(draft);
      await canvasNode(page, copyGroupId).click({ position: { x: 8, y: 8 } });
      await expect(flowNode(page, copyGroupId)).toHaveClass(/\bselected\b/);
      await builder.toolbar('ungroup').click();

      await expect.soft(builder.liveRegion).toContainText('Ungrouped nodes');
      await expect
        .soft(builder.summary)
        .toHaveText(summaryText({ devices: 2, switches: 1, networks: 1 }));
      await expect.soft(builder.nodes('group')).toHaveCount(0);
      await expectPersisted(
        builder,
        draft,
        (doc) => ({
          parents: devicesOf(doc).map((node) => node.parentId ?? null),
          positions: positions(doc),
        }),
        {
          parents: [null, null],
          positions: Object.fromEntries(
            before.nodes
              .filter((node) => node.kind !== 'group')
              .map((node) => [node.id, node.position]),
          ),
        },
        { soft: true },
      );

      // Ungroup is only for a group: with a device selected it is off (R56).
      // Focus is on a member's outline row, where Enter selects it.
      await expect(page.locator(':focus')).toHaveAttribute(
        'data-testid',
        /^outline-item-/,
      );
      await page.keyboard.press('Enter');
      await expect
        .soft(builder.toolbar('ungroup'))
        .toHaveAttribute('aria-disabled', 'true');
    });

    await test.step('Ctrl+G groups and Ctrl+Shift+G ungroups (⌘ on macOS)', async () => {
      const focused = page.locator(':focus');

      // Ungroup left focus on a member's outline row. Grouping moves that
      // row into the group, and focus goes to the new group's row.
      await page.keyboard.press('ControlOrMeta+a');
      await page.keyboard.press('ControlOrMeta+g');
      await expect.soft(builder.liveRegion).toContainText('Grouped selection');
      await expect(builder.nodes('group')).toHaveCount(1);
      const newGroup = await onlyNodeId(builder, 'group');
      await expect
        .soft(focused)
        .toHaveAttribute('data-testid', `outline-item-${newGroup}`);
      await page.keyboard.press('ControlOrMeta+Shift+g');
      await expect.soft(builder.liveRegion).toContainText('Ungrouped nodes');
      await expect(builder.nodes('group')).toHaveCount(0);
      await expect
        .soft(focused)
        .toHaveAttribute('data-testid', /^outline-item-/);

      // Undo and Redo move the focused row in and out of the group again;
      // focus stays on it.
      const row = await focused.getAttribute('data-testid');
      await page.keyboard.press('ControlOrMeta+z');
      await expect(builder.nodes('group')).toHaveCount(1);
      await expect.soft(focused).toHaveAttribute('data-testid', row);
      await page.keyboard.press('ControlOrMeta+Shift+z');
      await expect(builder.nodes('group')).toHaveCount(0);
      await expect.soft(focused).toHaveAttribute('data-testid', row);

      // From the canvas, the group's node takes focus; an Undo that
      // removes it leaves focus on the canvas, not the page.
      await focusNode(page, row.replace('outline-item-', ''));
      await page.keyboard.press('Enter');
      await page.keyboard.press('ControlOrMeta+g');
      await expect(builder.nodes('group')).toHaveCount(1);
      await expect
        .soft(flowNode(page, await onlyNodeId(builder, 'group')))
        .toBeFocused();
      await page.keyboard.press('ControlOrMeta+z');
      await expect(builder.nodes('group')).toHaveCount(0);
      await expect.soft(builder.canvas).toBeFocused();
    });

    expectNoFatal(issues);
  });

  test(
    'copies, pastes and duplicates devices with unique hostnames',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const draft = await blankDraft(builder);
      const subject = builder.inspector.locator('.builder-inspector__subject');

      await builder.toolbar('paste').click();
      await expect(builder.liveRegion).toContainText('Clipboard is empty.');
      await expect(builder.summary).toHaveText(summaryText({}));

      await builder.palette('device').click();
      const sourceId = await onlyNodeId(builder, 'device');
      // Click near the top-left corner: pasted copies overlap the source.
      const source = canvasNode(page, sourceId);
      const corner = { position: { x: 8, y: 8 } };
      const pressed = flowNode(page, sourceId);

      // A new node is selected already. A node is a toggle: a click on the
      // only selected item deselects it, and the next click selects it again.
      await expect.soft(pressed).toHaveAttribute('aria-pressed', 'true');
      await source.click(corner);
      await expect(pressed).toHaveAttribute('aria-pressed', 'false');
      await expect.soft(builder.liveRegion).toContainText('Deselected node');
      await source.click(corner);
      await expect(pressed).toHaveAttribute('aria-pressed', 'true');

      await builder.toolbar('copy').click();
      await expect(builder.liveRegion).toContainText('Copied node.');
      await builder.toolbar('paste').click();
      await expect(builder.liveRegion).toContainText('Pasted 1 node');
      await expect(builder.summary).toContainText('2 devices');
      // The paste becomes the selection.
      await expect(subject).toContainText('Device node-2');

      await source.click(corner);
      await expect(subject).toHaveText(/^Device node$/);
      await page.keyboard.press('ControlOrMeta+c');
      await page.keyboard.press('ControlOrMeta+v');
      await expect(builder.summary).toContainText('3 devices');
      await expect(subject).toContainText('Device node-3');

      await source.click(corner);
      await page.keyboard.press('ControlOrMeta+d');
      await expect(builder.summary).toContainText('4 devices');
      await expect(subject).toContainText('Device node-4');

      await expectPersisted(
        builder,
        draft,
        (doc) =>
          devicesOf(doc)
            .map((node) => node.device.hostname)
            .sort(),
        ['node', 'node-2', 'node-3', 'node-4'],
      );
      const doc = await builder.serverDocument(draft);
      const origin = byHostname(doc, 'node').position;
      // Each paste of the same node cascades past the copies before it (N15).
      for (const [step, hostname] of ['node-2', 'node-3', 'node-4'].entries()) {
        const copy = byHostname(doc, hostname);
        const offset = 40 * (step + 1);
        expect.soft(copy.id, hostname).not.toBe(sourceId);
        expect.soft(copy.device.spec.general.hostname).toBe(hostname);
        expect
          .soft(copy.position, hostname)
          .toEqual({ x: origin.x + offset, y: origin.y + offset });
      }

      expectNoFatal(issues);
    },
  );

  // R79: a copy is named after its own hostname, not its source's.
  test('a duplicated device is labelled with its own hostname', async ({
    page,
    builder,
  }) => {
    const draft = await blankDraft(builder);
    await builder.palette('device').click();
    const id = await onlyNodeId(builder, 'device');

    // A new node is selected already.
    await focusNode(page, id);
    await page.keyboard.press('ControlOrMeta+d');
    await expect(builder.nodes('device')).toHaveCount(2);
    await expectPersisted(builder, draft, (doc) => doc.nodes.length, 2);

    const copy = byHostname(await builder.serverDocument(draft), 'node-2');
    expect(copy.label).toBe('node-2');
    await expect(flowNode(page, copy.id)).toHaveAccessibleName(
      /^Device node-2, /,
    );
    await expect(
      outlineRow(builder, 'Device node-2, 0 connections'),
    ).toBeVisible();
  });

  test('duplicating a connected device copies each interface once', async ({
    page,
    builder,
  }) => {
    const draft = await blankDraft(builder);
    const { deviceId } = await deviceOnSwitch(builder);

    await focusNode(page, deviceId);
    await page.keyboard.press('ControlOrMeta+a');
    await page.keyboard.press('ControlOrMeta+d');
    await expect(builder.summary).toHaveText(
      summaryText({ devices: 2, switches: 2, networks: 1, links: 2 }),
    );
    await expectPersisted(builder, draft, (doc) => doc.edges.length, 2);

    const copy = byHostname(await builder.serverDocument(draft), 'node-2');
    const specNames = copy.device.spec.network.interfaces.map(
      (iface) => iface.name,
    );
    expect(copy.device.interfaces.map((handle) => handle.name)).toEqual([
      'eth0',
    ]);
    expect(specNames).toEqual(['eth0']);
  });

  test('arrow keys nudge by 10px and Shift+arrow by 1px', async ({
    page,
    builder,
    issues,
  }) => {
    // The canvas pans without animation, so a pan is seen within a frame.
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const draft = await blankDraft(builder);
    await builder.palette('device').click();
    await builder.palette('device').click();
    const [first, second] = await nodeIds(builder, 'device', 2);
    await expectPersisted(builder, draft, (doc) => doc.nodes.length, 2);
    const start = positions(await builder.serverDocument(draft));
    const at = (id, dx, dy) => ({ x: start[id].x + dx, y: start[id].y + dy });

    await canvasNode(page, first).click();
    // Key, expected offset from the start, and whether to check the saved
    // position there. The server is checked where earlier moves do not cancel
    // out, so each kind of nudge is proven saved: (10, 10) after the plain
    // arrows, (1, 1) after the Shift arrows, and the end of the sequence.
    const moves = [
      ['ArrowRight', 10, 0],
      ['ArrowDown', 10, 10, true],
      ['ArrowLeft', 0, 10],
      ['ArrowUp', 0, 0],
      ['Shift+ArrowRight', 1, 0],
      ['Shift+ArrowDown', 1, 1, true],
      ['Shift+ArrowLeft', 0, 1],
      ['Shift+ArrowUp', 0, 0],
      ['Shift+ArrowRight', 1, 0, true],
    ];
    for (const [key, dx, dy, saved] of moves) {
      await page.keyboard.press(key);
      const want = at(first, dx, dy);
      // Vue Flow's own arrow handler must not add a second move on screen.
      await expect(
        flowNode(page, first),
        `${key} moves the node once`,
      ).toHaveCSS('transform', `matrix(1, 0, 0, 1, ${want.x}, ${want.y})`);
      if (saved) {
        await expectPersisted(
          builder,
          draft,
          (doc) => positions(doc)[first],
          want,
        );
      }
    }
    // The announcement names the node and where it went.
    const end = at(first, 1, 0);
    await expect(builder.liveRegion).toContainText(
      `Moved node to x ${end.x}, y ${end.y}`,
    );

    // An arrow key on a node outside the selection moves nothing: the
    // selected node would move out of sight of the focused one.
    await focusNode(page, second);
    await page.keyboard.press('ArrowRight');
    await expect(flowNode(page, first)).toHaveCSS(
      'transform',
      `matrix(1, 0, 0, 1, ${end.x}, ${end.y})`,
    );

    // A multi-node nudge moves every selected node once, as one commit.
    await page.keyboard.press('ControlOrMeta+a');
    await page.keyboard.press('ArrowRight');
    await expect(builder.liveRegion).toContainText('Moved 2 nodes right by 10');

    // From the canvas itself, as after the skip link, arrow keys move the
    // selection and leave the view alone. With the Keyboard help open the
    // canvas is taller than the diagram, and keeping the canvas "in view"
    // once panned the diagram up on every press.
    await page.getByTestId('canvas-help').locator('summary').click();
    await builder.canvas.focus();
    const view = page.locator('.vue-flow__transformationpane');
    const viewBefore = await view.evaluate(
      (element) => element.style.transform,
    );
    for (const [key, dx] of [
      ['ArrowLeft', 0],
      ['ArrowRight', 10],
    ]) {
      await page.keyboard.press(key);
      const want = at(second, dx, 0);
      await expect(flowNode(page, second)).toHaveCSS(
        'transform',
        `matrix(1, 0, 0, 1, ${want.x}, ${want.y})`,
      );
      await page.evaluate(
        () =>
          new Promise((resolve) => {
            requestAnimationFrame(() => requestAnimationFrame(resolve));
          }),
      );
      expect
        .soft(
          await view.evaluate((element) => element.style.transform),
          `${key} from the canvas does not pan the diagram`,
        )
        .toBe(viewBefore);
    }
    await focusNode(page, second);

    // Enter on one of several selected nodes selects it alone.
    await page.keyboard.press('Enter');
    await expect(flowNode(page, first)).toHaveAttribute(
      'aria-pressed',
      'false',
    );
    await expect(flowNode(page, second)).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    await expect.soft(builder.liveRegion).toContainText('Selected node-2 only');

    // Keys on the canvas's own controls keep their own meaning: an arrow key
    // or Delete on a zoom button never edits the diagram.
    await builder.canvas.getByRole('button', { name: 'Zoom in' }).focus();
    await page.keyboard.press('ArrowRight');
    await page.keyboard.press('Delete');
    await expect(builder.nodes('device')).toHaveCount(2);
    const moved = at(second, 10, 0);
    await expect(flowNode(page, second)).toHaveCSS(
      'transform',
      `matrix(1, 0, 0, 1, ${moved.x}, ${moved.y})`,
    );

    await expectPersisted(builder, draft, positions, {
      [first]: at(first, 11, 0),
      [second]: at(second, 10, 0),
    });

    expectNoFatal(issues);
  });

  // ELK, the default layout, runs in a Web Worker: in Firefox too.
  test(
    'auto layout is deterministic, undoes in one step and can be put back',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const draft = await blankDraft(builder);
      await builder.palette('device').click();
      await builder.palette('device').click();
      await builder.palette('switch').click();
      await builder.connect({ label: 'node' }, { index: 1 });
      await builder.connect({ label: 'node-2' }, { index: 1 });
      await expect(builder.summary).toContainText('2 connections');
      await expectPersisted(builder, draft, (doc) => doc.edges.length, 2);
      const initial = positions(await builder.serverDocument(draft));

      // ELK's worker: one for every layout, until the Builder closes.
      const elkWorkers = { started: 0, closed: 0 };
      page.on('worker', (worker) => {
        if (/elk-worker/.test(worker.url())) {
          elkWorkers.started += 1;
          worker.on('close', () => {
            elkWorkers.closed += 1;
          });
        }
      });

      const layoutButton = builder.toolbar('layout');
      await expect(layoutButton).toHaveAccessibleName('Auto layout');
      await expect(layoutButton).toHaveAccessibleDescription(
        'Arrange the nodes automatically',
      );
      // At this width the toolbar only just fits, so a wider label would wrap
      // the button onto another row, out from under the pointer.
      const viewport = page.viewportSize();
      await page.setViewportSize({ ...viewport, width: 776 });
      await layoutButton.scrollIntoViewIfNeeded();
      const box = await layoutButton.boundingBox();

      // ELK loads as the first layout runs. Held back, it shows the layout
      // under way: the button is busy, with a ring in place of its icon,
      // and keeps its place.
      let release;
      const released = new Promise((resolve) => {
        release = resolve;
      });
      const elk = /elk-(api|worker)/;
      await page.route(elk, async (route) => {
        await released;
        await route.continue();
      });
      await layoutButton.click();
      await expect.soft(layoutButton).toHaveAttribute('aria-busy', 'true');
      await expect
        .soft(layoutButton.locator('.builder-toolbar__spinner'))
        .toBeVisible();
      expect
        .soft(await layoutButton.boundingBox(), 'the busy button stays put')
        .toEqual(box);
      release();
      await expect(builder.liveRegion).toContainText(
        'Applied automatic layout',
      );
      await expect.soft(layoutButton).not.toHaveAttribute('aria-busy');
      await page.unroute(elk);
      // Right after a layout, the same button offers to put it back.
      await expect(layoutButton).toHaveAccessibleName('Restore layout');
      await expect(layoutButton).toHaveAccessibleDescription(
        'Put every node back where it was before Auto layout',
      );
      expect(await layoutButton.boundingBox(), 'the button stays put').toEqual(
        box,
      );
      await page.setViewportSize(viewport);
      await expect
        .poll(async () => positions(await builder.serverDocument(draft)), {
          timeout: 20000,
        })
        .not.toEqual(initial);
      await builder.waitSaved();
      const laidOut = positions(await builder.serverDocument(draft));
      // ELK, the default, runs lines left to right: the switch right of the
      // devices it connects.
      const [switchId] = await nodeIds(builder, 'switch', 1);
      const devices = await nodeIds(builder, 'device', 2);
      const rightOf = (placed) =>
        devices.every((id) => placed[switchId].x >= placed[id].x + 160);
      const below = (placed) =>
        devices.every((id) => placed[switchId].y >= placed[id].y + 96);
      expect(rightOf(laidOut), 'ELK puts the switch right').toBe(true);

      // Move a node away, then lay out again: the result does not depend on the
      // starting positions.
      const [deviceId] = await nodeIds(builder, 'device', 2);
      await canvasNode(page, deviceId).click();
      await page.keyboard.press('ArrowRight');
      const nudged = {
        ...laidOut,
        [deviceId]: { x: laidOut[deviceId].x + 10, y: laidOut[deviceId].y },
      };
      await expectPersisted(builder, draft, positions, nudged);
      // Any other edit drops the layout to put back.
      await expect(layoutButton).toHaveAccessibleName('Auto layout');

      const uploads = [];
      const onRequest = (request) => {
        if (
          request.method() === 'POST' &&
          SNAPSHOTS.test(new URL(request.url()).pathname)
        ) {
          uploads.push(request.url());
        }
      };
      page.on('request', onRequest);
      await builder.toolbar('layout').click();
      await expectPersisted(builder, draft, positions, laidOut);
      await builder.waitSaved();
      page.off('request', onRequest);
      expect(uploads, 'one snapshot per layout').toHaveLength(1);

      // One undo reverts the whole layout.
      await focusNode(page, deviceId);
      await page.keyboard.press('ControlOrMeta+z');
      await expect(builder.liveRegion).toContainText(
        'Undid Applied automatic layout.',
      );
      await expect(flowNode(page, deviceId)).toHaveCSS(
        'transform',
        `matrix(1, 0, 0, 1, ${nudged[deviceId].x}, ${nudged[deviceId].y})`,
      );
      await expectPersisted(builder, draft, positions, nudged);
      // So does an undo.
      await expect(layoutButton).toHaveAccessibleName('Auto layout');

      // From the keyboard: lay out, then press the same button again to put
      // every node back, as one snapshot, with focus kept on the button.
      await layoutButton.focus();
      await page.keyboard.press('Enter');
      await expect(layoutButton).toHaveAccessibleName('Restore layout');
      await expectPersisted(builder, draft, positions, laidOut);
      await builder.waitSaved();
      uploads.length = 0;
      page.on('request', onRequest);
      await page.keyboard.press('Enter');
      await expect(builder.liveRegion).toContainText(
        'Restored previous layout',
      );
      await expect(layoutButton).toBeFocused();
      await expect(layoutButton).toHaveAccessibleName('Auto layout');
      await expect(flowNode(page, deviceId)).toHaveCSS(
        'transform',
        `matrix(1, 0, 0, 1, ${nudged[deviceId].x}, ${nudged[deviceId].y})`,
      );
      await expectPersisted(builder, draft, positions, nudged);
      await builder.waitSaved();
      page.off('request', onRequest);
      expect(uploads, 'one snapshot for the restore').toHaveLength(1);
      expect(elkWorkers, 'one ELK worker for three layouts').toEqual({
        started: 1,
        closed: 0,
      });

      // Auto layout follows the Settings dialog's algorithm: Standard, the
      // original, puts the switch below the devices.
      await page.getByRole('button', { name: 'Settings', exact: true }).click();
      const settings = page.getByTestId('settings-dialog');
      await settings
        .getByLabel('Auto layout algorithm')
        .selectOption('standard');
      await settings.getByRole('button', { name: 'Done' }).click();
      await expect(settings).toBeHidden();
      await layoutButton.click();
      await expect(builder.liveRegion).toContainText(
        'Applied automatic layout',
      );
      await expect(layoutButton).toHaveAccessibleName('Restore layout');
      await expectPersisted(
        builder,
        draft,
        (doc) => below(positions(doc)) && !rightOf(positions(doc)),
        true,
      );

      // Leaving the Builder ends ELK's worker.
      await builder.waitSaved();
      await page.getByRole('link', { name: 'Experiments' }).click();
      await expect(page).toHaveURL(/\/experiments/);
      await expect
        .poll(() => elkWorkers, { message: 'the ELK worker ends' })
        .toEqual({ started: 1, closed: 1 });

      expectNoFatal(issues);
    },
  );
});
