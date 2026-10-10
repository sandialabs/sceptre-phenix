// Accessibility, keyboard-only authoring and theming for the Builder.
//
// The outline is the advertised pointer-free editing surface, so these tests
// drive it with the keyboard only: focus, arrow keys, Enter, F2, Delete and
// the Connect form. They also run axe on every surface and dialog in both
// themes, and check the theme toggle, minimap and zoom controls.
//
// Each test makes its own draft and walks related checks on it, one
// test.step per check; each view, dialog and surface is scanned by a test of
// its own in each theme. Checks that do not gate the next step are soft, so
// one failure does not hide the others.

const crypto = require('crypto');

const {
  test,
  expect,
  backdropPoint,
  blankDocument,
  contrast,
  expectAccessible,
  expectNoFatal,
  invisibleText,
  labDocument,
  openConfigs,
  publishTopology,
  uniqueName,
  waitForApi,
} = require('./builder-support');

const THEME_KEY = 'phenix.builder.theme';

// --- local helpers ---------------------------------------------------------

function root(page) {
  return page.locator('.builder-root');
}

function rows(builder) {
  return builder.outline.locator('[data-testid^="outline-item-"]');
}

// The outline row whose visible label is exactly `label`.
function row(builder, label) {
  const exact = label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

  return rows(builder).filter({
    has: builder.page.locator('.builder-outline__label', {
      hasText: new RegExp(`^${exact}$`),
    }),
  });
}

// The outline row's node id (its test id without the prefix).
async function rowNodeId(builder, label) {
  return (await rowTestId(builder, label)).replace('outline-item-', '');
}

async function rowTestId(builder, label) {
  const locator = row(builder, label);
  await expect(locator).toHaveCount(1);

  return locator.getAttribute('data-testid');
}

// Adds a palette item with the keyboard (Enter on the palette button).
async function addWithKeyboard(builder, item, count) {
  await builder.palette(item).press('Enter');
  // The announcer may join queued messages into one.
  await expect(builder).toHaveAnnounced(`Added ${item}`);
  if (count !== undefined) {
    await expect(rows(builder)).toHaveCount(count);
  }
}

// Connects through the toolbar's Add a connection dialog with the keyboard:
// Enter on Add connection, the device and switch chosen, then Enter on
// Connect, which closes the dialog.
async function connectWithKeyboard(builder, device = 'node') {
  const dialog = builder.page.getByTestId('connect-dialog');

  await builder.toolbar('connect').press('Enter');
  await expect(dialog).toBeVisible();
  await dialog.locator('#connect-device').selectOption({ label: device });
  await dialog.locator('#connect-switch').selectOption({ index: 1 });
  await dialog.getByTestId('connect-dialog-submit').press('Enter');
  await expect(dialog).toHaveCount(0);
}

// A new draft with six devices, added with the keyboard, in a row.
async function sixDevices(builder) {
  await builder.open();
  const draft = await builder.createBlank();
  for (let count = 1; count <= 6; count += 1) {
    await addWithKeyboard(builder, 'device', count);
  }

  return draft;
}

// Lists in the editor with no list item, as the start of their markup. ARIA
// requires a list to own list items, and axe reports an empty one.
async function emptyLists(page) {
  return page.evaluate(() =>
    [
      ...document.querySelectorAll(
        '.builder-root ul, .builder-root ol, .builder-root [role="list"]',
      ),
    ]
      .filter(
        (list) =>
          ![...list.children].some((child) =>
            child.matches('li, [role="listitem"]'),
          ),
      )
      .map((list) => list.outerHTML.slice(0, 120)),
  );
}

async function focusInDialog(page) {
  return page.evaluate(
    () => !!document.activeElement?.closest('dialog, [role="dialog"]'),
  );
}

// Controls outside the open dialog that can still take focus. The page
// behind a modal dialog is inert, so there should be none. Tries the first
// few in document order, which include the app header's links.
async function focusableBehindDialog(page) {
  return page.evaluate(() => {
    const dialog = document.querySelector('dialog[open], [role="dialog"]');
    const active = document.activeElement;
    const reached = [
      ...document.querySelectorAll('a[href], button, input, select, textarea'),
    ]
      .filter((element) => !dialog.contains(element))
      .slice(0, 5)
      .filter((element) => {
        element.focus();
        return document.activeElement === element;
      });
    active?.focus();

    return reached.map((element) => element.outerHTML.slice(0, 80));
  });
}

// The canvas node (device or switch) that holds keyboard focus, if any.
async function focusedNodeId(page) {
  return page.evaluate(
    () => document.activeElement?.closest('.vue-flow__node')?.dataset.id,
  );
}

// Media query change events fire during a rendering update, before that
// frame's animation callbacks: two frames later the page has seen the change.
async function afterMediaChange(page) {
  await page.evaluate(
    () =>
      new Promise((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(resolve));
      }),
  );
}

async function openWithScheme(page, builder, scheme) {
  await page.emulateMedia({ colorScheme: scheme });
  await builder.open();
  await expect(root(page)).toHaveAttribute('data-builder-theme', scheme);
}

// Sets the Builder theme preference with the theme button of the editor's
// header, or with `view` 'drafts' the drafts' (keyboard). With `soft`, a
// toggle that never reaches `theme` is recorded and the test goes on.
async function chooseTheme(
  page,
  theme,
  { soft = false, view = 'editor' } = {},
) {
  const toggle = page.getByTestId(`${view}-theme`);
  for (let i = 0; i < 3; i += 1) {
    if (
      (await root(page).getAttribute('data-builder-theme-preference')) === theme
    ) {
      break;
    }
    await toggle.press('Enter');
  }
  await (soft ? expect.soft : expect)(root(page)).toHaveAttribute(
    'data-builder-theme-preference',
    theme,
  );
}

// Current zoom of the Vue Flow viewport.
async function zoomLevel(page) {
  return page.locator('.vue-flow__transformationpane').evaluate((element) => {
    const match = /scale\(([\d.]+)\)/.exec(element.style.transform || '');

    return match ? Number(match[1]) : 1;
  });
}

// The Vue Flow viewport's pan and zoom, as its CSS transform, once it has
// stopped moving. Given `from`, the view a press is about to change, it
// first waits for the viewport to leave that view: the move may begin a
// moment after the press, and a viewport that has not begun to move looks
// like one that has stopped.
async function settledView(page, from) {
  const transform = () =>
    page
      .locator('.vue-flow__transformationpane')
      .evaluate((element) => element.style.transform);
  let last = '';

  if (from !== undefined) {
    await expect
      .poll(transform, { message: 'the view the press changes' })
      .not.toBe(from);
  }

  await expect
    .poll(async () => {
      const now = await transform();
      const same = now === last;

      last = now;
      return same;
    })
    .toBe(true);

  return last;
}

// Node boxes that fall outside the visible canvas.
async function nodesOutsideCanvas(page) {
  return page.locator('.vue-flow').evaluate((flow) => {
    const bounds = flow.getBoundingClientRect();

    return [...flow.querySelectorAll('.vue-flow__node')]
      .map((node) => ({
        id: node.dataset.id,
        box: node.getBoundingClientRect(),
      }))
      .filter(
        ({ box }) =>
          box.left < bounds.left - 1 ||
          box.top < bounds.top - 1 ||
          box.right > bounds.right + 1 ||
          box.bottom > bounds.bottom + 1,
      )
      .map(({ id }) => id);
  });
}

// Canvas nodes drawn under the minimap or the zoom controls, which float
// over the canvas.
async function nodesUnderOverlays(page) {
  return page.locator('.vue-flow').evaluate((flow) => {
    const overlays = [
      ...flow.querySelectorAll('.vue-flow__minimap, .vue-flow__controls'),
    ].map((overlay) => overlay.getBoundingClientRect());
    const overlap = (a, b) =>
      Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 &&
      Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1;

    return [...flow.querySelectorAll('.vue-flow__node')]
      .filter((node) =>
        overlays.some((box) => overlap(node.getBoundingClientRect(), box)),
      )
      .map((node) => node.dataset.id);
  });
}

// Soft: every element matched by `locator` has at least `minimum` contrast.
async function expectReadable(locator, what, minimum = 4.5) {
  const results = await contrast(locator);
  expect.soft(results.length, `${what}: no elements found`).toBeGreaterThan(0);
  const failing = results.filter((item) => item.ratio < minimum);
  expect
    .soft(failing, `${what}: ${JSON.stringify(failing, null, 2)}`)
    .toEqual([]);
}

// The minimap's outline of the visible area: its contrast with the minimap's
// surface, its width as drawn, in CSS pixels, and the surface color.
async function minimapMask(page) {
  const mask = page.locator('.vue-flow__minimap-mask');
  const [{ ratio, background }] = await contrast(mask, 'stroke');
  // A stroke width is in the minimap's own units, which it scales down to
  // fit the whole diagram, unless the stroke does not scale.
  const width = await mask.evaluate((path) => {
    const style = getComputedStyle(path);
    const scale =
      style.vectorEffect === 'non-scaling-stroke'
        ? 1
        : path.ownerSVGElement.getScreenCTM().a;

    return parseFloat(style.strokeWidth) * scale;
  });

  return { width, ratio, surface: background };
}

// Distance between each radio button and the text of its label.
async function radioLayout(dialog) {
  return dialog.locator('input[type="radio"]').evaluateAll((inputs) =>
    inputs.map((input) => {
      const label = input.closest('label');
      const texts = [...label.childNodes].filter(
        (node) => node.nodeType === Node.TEXT_NODE && node.textContent.trim(),
      );
      const range = document.createRange();
      const last = texts[texts.length - 1];
      range.setStart(texts[0], 0);
      range.setEnd(last, last.length);
      const text = range.getBoundingClientRect();
      const box = input.getBoundingClientRect();

      return {
        label: label.textContent.trim(),
        gap: Math.round(text.left - box.right),
        rowOffset: Math.round(
          Math.abs((text.top + text.bottom) / 2 - (box.top + box.bottom) / 2),
        ),
      };
    }),
  );
}

// Soft: each radio group of `dialog` shares one name, so it is one Tab stop
// that arrow keys move within, and each radio button sits on its label's
// row, just before it. A dialog without radio buttons passes.
async function expectRadioGroups(dialog, name) {
  const groups = await dialog
    .locator('fieldset')
    .evaluateAll((sets) =>
      sets
        .map((set) =>
          [...set.querySelectorAll('input[type="radio"]')].map(
            (radio) => radio.name,
          ),
        )
        .filter((names) => names.length > 0),
    );
  for (const names of groups) {
    expect
      .soft(names[0] !== '' && new Set(names).size === 1, `${name}: ${names}`)
      .toBe(true);
  }

  const layout = await radioLayout(dialog);
  const misplaced = layout.filter(
    (item) => item.gap < -1 || item.gap >= 40 || item.rowOffset >= 12,
  );
  expect
    .soft(misplaced, `${name}: ${JSON.stringify(layout, null, 2)}`)
    .toEqual([]);
}

// Visible focus styling of a canvas node wrapper and its inner node box.
async function nodeFocusLook(page, id) {
  return page.locator(`.vue-flow__node[data-id="${id}"]`).evaluate((node) =>
    [node, node.querySelector('.builder-node')]
      .filter(Boolean)
      .map((element) => {
        const style = getComputedStyle(element);
        const outline =
          style.outlineStyle === 'none'
            ? 'none'
            : `${style.outlineStyle} ${style.outlineWidth} ${style.outlineColor}`;

        return `${outline} | ${style.boxShadow} | ${style.borderColor}`;
      })
      .join(' || '),
  );
}

// Reaches a header item with Tab and checks its focus ring: the page's
// yellow ring, drawn inside the item, so the top of the window cuts off none
// of it.
async function expectHeaderRing(page, item, what) {
  await item.focus();
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Tab');
  await expect.soft(item, what).toBeFocused();
  const ring = await item.evaluate((element) => {
    const style = getComputedStyle(element);

    return {
      outline: `${style.outlineStyle} ${style.outlineWidth} ${style.outlineColor}`,
      reach: parseFloat(style.outlineWidth) + parseFloat(style.outlineOffset),
      top: element.getBoundingClientRect().top,
    };
  });
  expect
    .soft(ring.outline, `${what} focus ring`)
    .toBe('solid 3px rgb(255, 209, 102)');
  expect
    .soft(ring.top - Math.max(ring.reach, 0), `${what} focus ring top`)
    .toBeGreaterThanOrEqual(0);
}

// The tracker deletes drafts with If-Match. A snapshot still in flight when
// the test ends changes the ETag under that delete and leaves the draft
// behind, so let autosave settle first (hooks run before fixture teardown).
test.afterEach(async ({ builder }) => {
  if (await builder.saveState.isVisible().catch(() => false)) {
    await builder.waitSaved(10000).catch(() => {});
  }
});

// --- keyboard-only authoring through the outline and the toolbar's dialogs --

// A new draft with a device, a switch and a note, added with the keyboard,
// and nothing selected: the outline's rows and the device's and switch's
// test ids.
async function deviceSwitchAndNote(builder) {
  await builder.open();
  const draft = await builder.createBlank();
  await addWithKeyboard(builder, 'device', 1);
  await addWithKeyboard(builder, 'switch', 2);
  await addWithKeyboard(builder, 'note', 3);
  const deviceTestId = await rowTestId(builder, 'node');
  const switchTestId = await rowTestId(builder, 'EXP');
  // A new node is selected; Escape on the canvas selects nothing.
  await builder.canvas.focus();
  await builder.page.keyboard.press('Escape');

  return {
    draft,
    all: rows(builder),
    deviceTestId,
    deviceId: deviceTestId.replace('outline-item-', ''),
    switchTestId,
  };
}

test.describe('keyboard-only authoring', () => {
  test('Add connection says what is missing, connects a device to a switch and gives focus back, without a pointer', async ({
    page,
    builder,
    issues,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();

    await expect.soft(builder.liveRegion).toHaveAttribute('role', 'status');
    await expect
      .soft(builder.liveRegion)
      .toHaveAttribute('aria-live', 'polite');
    // The toolbar's Add connection and the dialog it opens.
    const opener = builder.toolbar('connect');
    const dialog = page.getByTestId('connect-dialog');
    const connect = dialog.getByTestId('connect-dialog-submit');
    const error = dialog.getByTestId('connect-error');

    // A blank diagram says it has no nodes or networks instead of showing
    // empty lists, which ARIA does not allow (a list owns list items).
    await expect
      .soft(page.getByTestId('builder-outline-empty'))
      .toHaveText('No nodes yet. Add one from the Add nodes panel.');
    await expect
      .soft(page.getByTestId('builder-networks-empty'))
      .toHaveText('No networks yet. Adding a switch creates one.');
    expect.soft(await emptyLists(page), 'empty lists').toEqual([]);

    // Enter on Add connection opens the dialog, which takes focus, and
    // Escape closes it onto the button. With nothing to choose, the message
    // says what to add; each time the dialog opens it follows the diagram.
    await expect.soft(opener).toHaveAttribute('aria-haspopup', 'dialog');
    await opener.press('Enter');
    await expect(dialog).toBeVisible();
    await expect.soft(dialog).toHaveAccessibleName('Add a connection');
    await expect.soft(dialog).toBeFocused();
    await connect.press('Enter');
    await expect
      .soft(error)
      .toHaveText('Add a device and a switch to the diagram first.');
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect.soft(opener).toBeFocused();

    await addWithKeyboard(builder, 'device', 1);
    // A selected device fills Device.
    await builder.selectInOutline('node');
    await opener.press('Enter');
    await expect(dialog).toBeVisible();
    await expect
      .soft(dialog.locator('#connect-device option:checked'))
      .toHaveText('node');
    await connect.press('Enter');
    await expect.soft(error).toHaveText('Add a switch to the diagram first.');
    await dialog.locator('#connect-device').selectOption({ index: 0 });
    await connect.press('Enter');
    await expect
      .soft(error)
      .toHaveText('Choose a device. Add a switch to the diagram first.');
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect.soft(opener).toBeFocused();

    await addWithKeyboard(builder, 'switch', 2);
    await expect
      .soft(builder.summary)
      .toContainText('1 device, 1 switch, 1 network');
    const deviceId = await rowNodeId(builder, 'node');
    const switchTestId = await rowTestId(builder, 'EXP');
    const switchId = switchTestId.replace('outline-item-', '');

    await test.step('Add a connection names, marks and focuses a missing device or switch', async () => {
      const device = dialog.locator('#connect-device');
      const sw = dialog.locator('#connect-switch');

      await opener.press('Enter');
      await expect(dialog).toBeVisible();
      // Whatever the selection filled in is taken out again.
      await device.selectOption({ index: 0 });
      await sw.selectOption({ index: 0 });
      await connect.press('Enter');
      await expect.soft(error).toHaveText('Choose a device and a switch.');
      await expect
        .soft(dialog.getByRole('alert'))
        .toHaveText('Choose a device and a switch.');
      await expect.soft(device).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(sw).toHaveAccessibleDescription(/^Choose a device/);
      await expect.soft(device).toBeFocused();

      // The message stops naming a field once it has a value.
      await device.selectOption({ label: 'node' });
      await expect.soft(error).toHaveText('Choose a switch.');
      await expect.soft(sw).toHaveAccessibleDescription('Choose a switch.');

      // A repeated message replaces the alert, so it is announced again.
      await error.evaluate((element) => {
        element.dataset.seen = 'yes';
      });
      await connect.press('Enter');
      await expect.soft(error).toHaveText('Choose a switch.');
      await expect.soft(error).not.toHaveAttribute('data-seen', 'yes');
      await expect.soft(device).not.toHaveAttribute('aria-invalid');
      await expect.soft(sw).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(sw).toBeFocused();
      await expect.soft(builder.summary).toContainText('0 connections');
    });

    await test.step('the completed dialog connects the device, closes and gives focus back to Add connection', async () => {
      await dialog.locator('#connect-switch').selectOption({ index: 1 });
      await connect.press('Enter');
      // The rename step below renames this connection's network.
      await builder.expectSummary('1 connection');
      await expect.soft(dialog).toHaveCount(0);
      await expect.soft(opener).toBeFocused();
      await expect.soft(builder).toHaveAnnounced('Connected nodes');
      await expect
        .soft(row(builder, 'node'))
        .toHaveAttribute('aria-label', 'Device node, 1 connection, on EXP');
      // The outline lists nodes only: no interface or connection rows.
      await expect
        .soft(page.locator('.builder-outline li li'), 'nested rows')
        .toHaveCount(0);
      await expect
        .soft(
          page
            .locator('.builder-outline')
            .getByRole('button', { name: /^Disconnect/ }),
        )
        .toHaveCount(0);
      await expect
        .soft(page.locator('path.builder-edge[data-network="EXP"]'))
        .toHaveCount(1);

      // Only the complete request reached the document.
      await builder.waitSaved();
      const doc = await builder.serverDocument(draft);
      expect.soft(doc.nodes).toHaveLength(2);
      expect.soft(doc.networks).toHaveLength(1);
      expect.soft(doc.edges).toEqual([
        expect.objectContaining({
          sourceNodeId: deviceId,
          targetNodeId: switchId,
          networkId: doc.networks[0]?.id,
        }),
      ]);
    });
    expectNoFatal(issues);
  });

  test('outline selection, F2 on a switch, a second switch, Disconnect and Remove network work without a pointer', async ({
    page,
    builder,
    issues,
  }) => {
    // The device `node`, connected by eth0 to the switch of network EXP.
    const draft = await builder.seedDraft(
      labDocument(`keyboard-network-${Date.now()}`, { hostnames: ['node'] }),
    );
    await builder.openDraft(draft);
    const opener = builder.toolbar('connect');
    const dialog = page.getByTestId('connect-dialog');
    const deviceId = await rowNodeId(builder, 'node');
    const switchTestId = await rowTestId(builder, 'EXP');

    // The outline once counted nodes only, saying "1 node selected" beside a
    // selected connection that a Delete would still remove.
    await test.step('outline selection announcements count a selected connection', async () => {
      const edge = page.locator('g.vue-flow__edge');
      const device = page.getByTestId(`outline-item-${deviceId}`);

      await edge.focus();
      await page.keyboard.press('Escape');
      await page.keyboard.press('Enter');
      await expect.soft(edge).toHaveClass(/\bselected\b/);
      await device.focus();
      await page.keyboard.press('Shift+Enter');
      await expect
        .soft(builder)
        .toHaveAnnounced('Added node to the selection, 2 items selected');
      await page.keyboard.press('Shift+Enter');
      await expect
        .soft(builder)
        .toHaveAnnounced('Removed node from the selection, 1 item selected');
      await expect.soft(edge).toHaveClass(/\bselected\b/);

      // A plain press keeps only the row, and says the connection went.
      await page.keyboard.press('Enter');
      await expect.soft(builder).toHaveAnnounced('Selected node only');
      await expect.soft(edge).not.toHaveClass(/\bselected\b/);
      await page.keyboard.press('Enter');
      await expect.soft(builder).toHaveAnnounced('Deselected node');
    });

    await test.step('F2 on the switch renames its network and edge', async () => {
      await page.getByTestId(switchTestId).focus();
      await page.keyboard.press('F2');
      await expect.soft(page.getByLabel('Rename EXP')).toBeFocused();
      await page.keyboard.type('MGMT');
      await page.keyboard.press('Enter');

      await expect.soft(builder).toHaveAnnounced('Updated network MGMT');
      const networks = page.getByTestId('builder-networks');
      await expect.soft(networks).toContainText('MGMT');
      await expect.soft(networks).not.toContainText('EXP');
      await expect
        .soft(page.getByTestId(switchTestId))
        .toHaveAttribute('aria-label', /network MGMT, 1 connection/);

      const edge = page.locator('path.builder-edge');
      await expect.soft(edge, 'one rendered edge').toHaveCount(1);
      await expect
        .soft(page.locator('path.builder-edge[data-network="MGMT"]'))
        .toHaveCount(1);
      // The focusable Vue Flow edge wrapper carries the accessible name.
      await expect
        .soft(
          page.getByRole('button', {
            name: /^Network MGMT from node \(eth0\) to /,
          }),
        )
        .toHaveCount(1);

      await builder.waitSaved();
      const doc = await builder.serverDocument(draft);
      expect
        .soft(doc.networks.map((network) => network.name))
        .toEqual(['MGMT']);
      expect
        .soft(doc.edges)
        .toEqual([expect.objectContaining({ networkId: doc.networks[0]?.id })]);
    });

    await test.step('a new switch lists its own network; no form adds one apart from a switch', async () => {
      await expect.soft(page.getByLabel('New network name')).toHaveCount(0);
      await expect
        .soft(builder.outline.getByRole('button', { name: 'Add network' }))
        .toHaveCount(0);

      await addWithKeyboard(builder, 'switch', 3);
      const entry = page
        .getByTestId('builder-networks')
        .locator('.builder-outline__item', { hasText: 'EXP' });
      // The device count is text, not a name on a generic element.
      await expect.soft(entry).toContainText('no alias');
      await expect.soft(entry).toContainText('0 devices');
      await expect.soft(entry).not.toHaveAttribute('aria-label');
      await expect
        .soft(page.getByRole('button', { name: 'Remove network EXP' }))
        .toBeVisible();
      await expect.soft(builder.summary).toContainText('2 networks');

      // With WCAG 1.4.12 text spacing the name still shows whole: the alias
      // and count go under it rather than squeezing it.
      const spacing = await page.addStyleTag({
        content:
          '* { line-height: 1.5 !important; letter-spacing: 0.12em !important; word-spacing: 0.16em !important; }',
      });
      await expect.soft
        .poll(() =>
          entry
            .locator('.builder-outline__label')
            .evaluate((label) => label.scrollWidth - label.clientWidth),
        )
        .toBeLessThanOrEqual(0);
      await spacing.evaluate((element) => element.remove());
    });

    await test.step('Disconnect in the command palette and Remove network act without a pointer, and focus stays in place', async () => {
      // The outline has no connection rows, so the palette names the
      // connection from its device's end, with the interface.
      const device = page.getByTestId(`outline-item-${deviceId}`);
      const palette = page.getByTestId('commands-dialog');

      await device.focus();
      await page.keyboard.press('ControlOrMeta+k');
      await page.keyboard.type('Disconnect');
      await page.keyboard.press('Enter');
      await expect
        .soft(palette.getByRole('option'))
        .toHaveText([/^node \(eth0\) to .+network MGMT$/]);
      await page.keyboard.press('Enter');
      await builder.expectSummary('0 connections');
      await expect.soft(palette).toHaveCount(0);
      await expect.soft(page.locator('path.builder-edge')).toHaveCount(0);
      await expect.soft(device).toBeFocused();
      // The interface stays, free to connect again.
      await opener.press('Enter');
      await expect(dialog).toBeVisible();
      await dialog.locator('#connect-device').selectOption({ label: 'node' });
      await expect
        .soft(dialog.locator('#connect-interface option', { hasText: 'eth0' }))
        .toHaveCount(1);
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);
      await expect.soft(opener).toBeFocused();

      // Networks sort by name: EXP, then MGMT.
      await page
        .getByRole('button', { name: 'Remove network EXP' })
        .press('Enter');
      await builder.expectSummary('1 network');
      await expect
        .soft(page.getByRole('button', { name: 'Remove network MGMT' }))
        .toBeFocused();
      await page.keyboard.press('Enter');
      await builder.expectSummary('0 networks');
      await expect
        .soft(page.getByRole('heading', { name: 'Networks' }))
        .toBeFocused();
      await expect
        .soft(page.getByTestId('builder-networks-empty'))
        .toHaveText('No networks yet. Adding a switch creates one.');
      expect.soft(await emptyLists(page), 'empty lists').toEqual([]);
    });
    expectNoFatal(issues);
  });

  // This spec keeps the product's hold on each message (announce.js),
  // which other specs shorten.
  test('edits made while a message is held are announced together after it', async ({
    page,
    builder,
    issues,
  }) => {
    await builder.open();
    await builder.createBlank();

    await test.step('the switch and the note, added during the device’s hold, are read together', async () => {
      // The device is added first. The region shows its message at once, or
      // joined to messages still waiting from the steps before; either way,
      // its hold begins then. The switch is added as soon as the region shows
      // it, and the note 200 ms later: both land inside the 750 ms hold
      // however slow the machine, and are shown together once it ends. With
      // no hold they would be shown one by one, which this step would catch.
      await page.evaluate(async () => {
        const region = document.querySelector(
          '[data-testid="builder-live-region"]',
        );
        const add = (item) =>
          document.querySelector(`[data-testid="palette-${item}"]`).click();
        const shown = new Promise((resolve, reject) => {
          const observer = new MutationObserver(() => {
            if (/(^|\. )Added device(\.|$)/.test(region.textContent.trim())) {
              observer.disconnect();
              resolve();
            }
          });
          observer.observe(region, { childList: true, subtree: true });
          setTimeout(() => reject(new Error('Added device not shown')), 10000);
        });

        add('device');
        await shown;
        add('switch');
        await new Promise((resolve) => setTimeout(resolve, 200));
        add('note');
      });
      await expect.soft(builder).toHaveAnnounced(/(^|\. )Added device(\.|$)/);
      await expect
        .soft(builder)
        .toHaveAnnounced('Added switch. Added note.', { exact: true });
      await expect
        .soft(builder.liveRegion)
        .toHaveText('Added switch. Added note.');
    });
    expectNoFatal(issues);
  });

  test(
    'the skip link moves focus to the canvas, and arrow keys, Home and End move through outline rows',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const { all } = await deviceSwitchAndNote(builder);

      await test.step('skip link moves keyboard focus to the canvas', async () => {
        const skip = page.getByRole('link', { name: 'Skip to diagram canvas' });
        await expect.soft(skip).not.toBeInViewport();
        await skip.focus();
        await expect.soft(skip).toBeInViewport();

        await page.keyboard.press('Enter');
        await expect.soft(page).toHaveURL(/#builder-canvas$/);
        await expect.soft(builder.canvas).toBeVisible();
        // The link lands on the named canvas itself, whose keys then work.
        await expect.soft(builder.canvas).toBeFocused();
        const selected = all.and(page.locator('[aria-pressed="true"]'));
        await page.keyboard.press('ControlOrMeta+A');
        await expect.soft(selected, 'Ctrl+A on the canvas').toHaveCount(3);
        await page.keyboard.press('Escape');
        await expect.soft(selected, 'Escape on the canvas').toHaveCount(0);

        // ? opens the shortcut sheet and Ctrl+K (⌘K on macOS) the command
        // palette, from the canvas too; closing either returns focus.
        await page.keyboard.press('?');
        await expect.soft(page.getByTestId('shortcuts-dialog')).toBeVisible();
        // Where the outline's keys are shown, now that it has no hint.
        await expect
          .soft(page.getByTestId('shortcut-outline.move'))
          .toContainText('Outline rows');
        await page.keyboard.press('Escape');
        await expect.soft(builder.canvas).toBeFocused();
        await page.keyboard.press('ControlOrMeta+k');
        await expect.soft(page.getByTestId('commands-dialog')).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(builder.canvas).toBeFocused();

        await page.keyboard.press('Tab');
        const inCanvas = await page.evaluate(
          () =>
            !!document.activeElement?.closest('[data-testid="builder-canvas"]'),
        );
        expect.soft(inCanvas, 'Tab after the skip link').toBe(true);
      });

      await test.step('arrow keys, Home and End move focus through rows', async () => {
        // Rows sort by kind: switch, device, note.
        const ids = await all.evaluateAll((items) =>
          items.map((item) => item.getAttribute('data-testid')),
        );
        await expect.soft(all.nth(0)).toContainText('EXP');
        await expect.soft(all.nth(1)).toContainText('node');
        await expect.soft(all.nth(2)).toContainText('Note');
        // The keys are not written out beside the rows (the shortcut sheet
        // lists them), but the list still describes them to screen readers,
        // and reading the page reaches them before the list.
        await expect
          .soft(builder.outline)
          .toHaveAccessibleDescription(
            /^Keyboard: Up and Down arrows, Home and End move between rows\. .+ F2 renames\./,
          );
        const hint = page.locator('.builder-outline').getByText(/^Keyboard: /);
        await expect.soft(hint).toHaveClass(/builder-visually-hidden/);
        await expect.soft(hint).not.toHaveAttribute('hidden');

        const expectFocused = async (index, key) => {
          const message = `row ${index} focused after ${key}`;
          await expect
            .soft(page.getByTestId(ids[index]), message)
            .toBeFocused();
          // Roving tabindex: only the focused row is in the tab order.
          await expect
            .soft(
              builder.outline.locator(
                '[data-testid^="outline-item-"][tabindex="0"]',
              ),
              `${message}: roving tabindex`,
            )
            .toHaveAttribute('data-testid', ids[index]);
        };

        await all.first().focus();
        await expectFocused(0, 'focus()');

        await page.keyboard.press('ArrowDown');
        await expectFocused(1, 'ArrowDown');
        await page.keyboard.press('ArrowDown');
        await expectFocused(2, 'ArrowDown');
        await page.keyboard.press('ArrowDown');
        await expectFocused(2, 'ArrowDown on the last row');

        await page.keyboard.press('Home');
        await expectFocused(0, 'Home');
        await page.keyboard.press('ArrowUp');
        await expectFocused(0, 'ArrowUp on the first row');

        await page.keyboard.press('End');
        await expectFocused(2, 'End');
        await page.keyboard.press('ArrowUp');
        await expectFocused(1, 'ArrowUp');
      });
      expectNoFatal(issues);
    },
  );

  test(
    'Enter, Space and a click toggle the focused outline row, and Shift+Enter adds it to the selection',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const { deviceTestId, switchTestId } = await deviceSwitchAndNote(builder);

      await test.step('Enter, Space and a click toggle the focused row; Shift+Enter adds to the selection', async () => {
        const device = page.getByTestId(deviceTestId);
        const sw = page.getByTestId(switchTestId);
        const subject = page.locator('.builder-inspector__subject');

        // Every press is announced, in the canvas's words.
        await device.focus();
        await page.keyboard.press('Enter');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'true');
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'false');
        await expect.soft(subject).toContainText('Device node');
        await expect.soft(builder).toHaveAnnounced('Selected node');
        await expect
          .soft(
            page.locator('.vue-flow__node.selected [data-node-kind="device"]'),
          )
          .toHaveCount(1);

        // A press that drops the selected row says so.
        await page.keyboard.press('ArrowUp');
        await expect.soft(sw).toBeFocused();
        await page.keyboard.press(' ');
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'true');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'false');
        await expect.soft(subject).toContainText('Network EXP');
        await expect.soft(builder).toHaveAnnounced('Selected EXP only');

        // Shift+Enter toggles a row in and out of the selection.
        await page.keyboard.press('ArrowDown');
        await page.keyboard.press('Shift+Enter');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'true');
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'true');
        await expect
          .soft(builder)
          .toHaveAnnounced('Added node to the selection, 2 items selected');
        await expect
          .soft(page.locator('.vue-flow__node.selected'))
          .toHaveCount(2);
        await page.keyboard.press('Shift+Enter');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'false');
        await expect
          .soft(builder)
          .toHaveAnnounced('Removed node from the selection, 1 item selected');

        // A plain press on a row in a larger selection keeps only that row,
        // and says so; on the only selected row it deselects it.
        await page.keyboard.press('Shift+Enter');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'true');
        await page.keyboard.press('Enter');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'true');
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'false');
        await expect.soft(builder).toHaveAnnounced('Selected node only');
        await page.keyboard.press(' ');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'false');
        await expect.soft(builder).toHaveAnnounced('Deselected node');
        await expect
          .soft(page.locator('.vue-flow__node.selected'))
          .toHaveCount(0);

        // A click toggles the same way.
        await sw.click();
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'true');
        await expect.soft(sw).toBeFocused();
        await sw.click();
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'false');
        await expect.soft(builder).toHaveAnnounced('Deselected EXP');
      });
      expectNoFatal(issues);
    },
  );

  test(
    'F2 renames the focused outline row, Save now and the palette’s key commit the rename, and Delete removes the row',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const { draft, all, deviceTestId, deviceId, switchTestId } =
        await deviceSwitchAndNote(builder);

      await test.step('F2 then Enter renames the device hostname', async () => {
        await page.getByTestId(deviceTestId).focus();
        await page.keyboard.press('F2');
        const input = page.locator(`#rename-${deviceId}`);
        await expect.soft(input).toBeFocused();
        await expect.soft(input).toHaveValue('node');
        await expect.soft(page.getByLabel('Rename node')).toBeVisible();

        // The current name is selected, so typing replaces it.
        await page.keyboard.type('web-01');
        await page.keyboard.press('Enter');

        // No rename input may stay open before the Delete step.
        await expect(input).toHaveCount(0);
        await expect.soft(builder).toHaveAnnounced('Renamed device to web-01');
        await expect
          .soft(page.getByTestId(deviceTestId))
          .toHaveAttribute('aria-label', 'Device web-01, 0 connections');
        // The canvas node's Vue Flow wrapper carries the same name.
        await expect
          .soft(page.locator(`.vue-flow__node[data-id="${deviceId}"]`))
          .toHaveAccessibleName('Device web-01, 0 connections');

        // Focus returns to the renamed row after Enter, and after Escape.
        await expect.soft(page.getByTestId(deviceTestId)).toBeFocused();
        await page.keyboard.press('F2');
        await expect.soft(input).toBeFocused();
        await page.keyboard.press('Escape');
        await expect(input).toHaveCount(0);
        await expect.soft(page.getByTestId(deviceTestId)).toBeFocused();

        await builder.waitSaved();
        const doc = await builder.serverDocument(draft);
        const node = doc.nodes.find((item) => item.id === deviceId);
        expect.soft(node?.device?.hostname).toBe('web-01');
        expect.soft(node?.device?.spec?.general?.hostname).toBe('web-01');
      });

      // The rename field keeps its keys, but for the Builder's that work in
      // every text field: they did not reach it, so the browser took them.
      await test.step('Save now and the palette’s key work in the rename field', async () => {
        const item = page.getByTestId(deviceTestId);
        const input = page.locator(`#rename-${deviceId}`);

        // Save now commits the rename first, as Enter does, then saves it.
        await item.focus();
        await page.keyboard.press('F2');
        await expect.soft(input).toBeFocused();
        await page.keyboard.type('web-02');
        await page.keyboard.press('ControlOrMeta+s');
        await expect(input).toHaveCount(0);
        await expect.soft(item).toBeFocused();
        await expect
          .soft(item)
          .toHaveAttribute('aria-label', 'Device web-02, 0 connections');
        await expect.soft(builder).toHaveAnnounced('All changes saved');
        await expect.soft
          .poll(
            async () =>
              (await builder.serverDocument(draft)).nodes.find(
                (node) => node.id === deviceId,
              )?.device?.hostname,
          )
          .toBe('web-02');

        // The palette's key opens the palette; leaving the field for it
        // commits the rename, as leaving it any other way does.
        await page.keyboard.press('F2');
        await page.keyboard.type('web-01');
        await page.keyboard.press('ControlOrMeta+k');
        const palette = page.getByTestId('commands-dialog');
        await expect.soft(palette).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(palette).toBeHidden();
        await expect(input).toHaveCount(0);
        await expect
          .soft(item)
          .toHaveAttribute('aria-label', 'Device web-01, 0 connections');
        await expect.soft(page.locator('body')).not.toBeFocused();
      });

      await test.step('Delete removes the focused row, and focus and the tab stop stay in the outline', async () => {
        await page.getByTestId(deviceTestId).focus();
        await page.keyboard.press('Delete');

        await expect.soft(all).toHaveCount(2);
        await expect.soft(page.getByTestId(deviceTestId)).toHaveCount(0);
        await expect.soft(builder.summary).toContainText('0 devices, 1 switch');
        await expect.soft(builder).toHaveAnnounced('Deleted web-01');
        // Focus moves to the previous row, so Delete can be pressed again.
        await expect.soft(page.getByTestId(switchTestId)).toBeFocused();
        await expect
          .soft(
            page.locator(
              '[data-testid="builder-node"][data-node-kind="device"]',
            ),
          )
          .toHaveCount(0);

        await builder.waitSaved();
        const doc = await builder.serverDocument(draft);
        expect.soft(doc.nodes).toHaveLength(2);
        expect.soft(doc.nodes.map((node) => node.id)).not.toContain(deviceId);

        // A toolbar delete of the row holding the tab stop leaves another
        // row in the tab order.
        await all.last().focus();
        await page.keyboard.press('Enter');
        await builder.toolbar('delete').press('Enter');
        await expect.soft(all).toHaveCount(1);
        await expect
          .soft(
            builder.outline.locator(
              '[data-testid^="outline-item-"][tabindex="0"]',
            ),
          )
          .toHaveAttribute('data-testid', switchTestId);

        // Deleting the only row left moves focus to the Outline heading.
        // Backspace deletes too: a Mac's delete key is Backspace.
        await page.getByTestId(switchTestId).focus();
        await page.keyboard.press('Backspace');
        await expect.soft(all).toHaveCount(0);
        await expect
          .soft(page.getByRole('heading', { name: 'Outline', exact: true }))
          .toBeFocused();
        await expect
          .soft(page.getByTestId('builder-outline-empty'))
          .toBeVisible();
        expect.soft(await emptyLists(page), 'empty lists').toEqual([]);
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the canvas is one Tab stop that the arrow keys and Page Down move through, and focus looks different from selection',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      // Two devices in a row with a note beyond them, and the switch below
      // the first device, connected to both.
      const id = () => crypto.randomUUID();
      const network = { id: id(), name: 'EXP' };
      const device = (hostname, position) => ({
        id: id(),
        kind: 'device',
        label: hostname,
        position,
        device: {
          hostname,
          spec: {
            type: 'VirtualMachine',
            general: { hostname, vm_type: 'kvm' },
            hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
            network: {
              interfaces: [
                { name: 'eth0', proto: 'dhcp', type: 'ethernet', vlan: 'EXP' },
              ],
            },
          },
          interfaces: [{ id: id(), name: 'eth0', index: 0 }],
        },
      });
      const left = device('left', { x: 0, y: 0 });
      const right = device('right', { x: 480, y: 0 });
      const note = {
        id: id(),
        kind: 'note',
        label: 'Lab notes',
        position: { x: 960, y: 0 },
        note: { text: 'Lab notes', color: '' },
      };
      const sw = {
        id: id(),
        kind: 'switch',
        label: 'EXP',
        position: { x: -10, y: 240 },
        switch: { networkId: network.id },
      };
      const [toLeft, toRight] = [left, right].map((end) => ({
        id: id(),
        sourceNodeId: end.id,
        sourceHandleId: end.device.interfaces[0].id,
        targetNodeId: sw.id,
        networkId: network.id,
      }));
      const draft = await builder.seedDraft(
        blankDocument(`canvas-keys-${Date.now()}`, {
          nodes: [left, right, note, sw],
          networks: [network],
          edges: [toLeft, toRight],
        }),
      );
      await builder.openDraft(draft);

      const node = (item) =>
        page.locator(`.vue-flow__node[data-id="${item.id}"]`);
      const edge = (item) =>
        page.locator(`.vue-flow__edge[data-id="${item.id}"]`);
      const items = builder.canvas.locator('.vue-flow__node, .vue-flow__edge');
      const stops = items.and(page.locator('[tabindex="0"]'));
      const zoomIn = page.getByRole('button', { name: 'Zoom in' });

      await test.step('Tab reaches the canvas once, and leaves it for the zoom controls', async () => {
        await page
          .getByRole('separator', { name: 'Resize Add nodes and Outline' })
          .focus();
        await page.keyboard.press('Tab');
        await expect(builder.canvas).toBeFocused();
        await expect
          .soft(builder.canvas)
          .toHaveAccessibleDescription(/^Arrow keys move to the nodes/);
        // Every node and connection takes focus, but none is a Tab stop.
        await expect.soft(items).toHaveCount(6);
        await expect.soft(stops).toHaveCount(0);
        await page.keyboard.press('Tab');
        await expect.soft(zoomIn).toBeFocused();
        await page.keyboard.press('Shift+Tab');
        await expect(builder.canvas).toBeFocused();
      });

      await test.step('from the canvas, an arrow key goes to a node, which becomes the Tab stop', async () => {
        await page.keyboard.press('ArrowDown');
        const first = await focusedNodeId(page);
        expect([left.id, right.id, note.id, sw.id]).toContain(first);
        await expect.soft(stops).toHaveCount(1);
        await expect.soft(builder.canvas).toHaveAttribute('tabindex', '-1');
      });

      await test.step('arrow keys move to the nearest node that way', async () => {
        await node(left).focus();
        const moves = [
          ['ArrowRight', right],
          ['ArrowRight', note],
          ['ArrowRight', note],
          ['ArrowLeft', right],
          ['ArrowLeft', left],
          ['ArrowDown', sw],
          ['ArrowUp', left],
        ];
        for (const [key, to] of moves) {
          await page.keyboard.press(key);
          await expect(node(to), `${key} to ${to.label}`).toBeFocused();
        }
        await expect.soft(stops).toHaveCount(1);
        await expect.soft(node(left)).toHaveAttribute('tabindex', '0');
      });

      await test.step('Page Down and Page Up go round a node’s connections, and arrow keys go back to the nodes', async () => {
        await page.keyboard.press('ArrowDown');
        await expect(node(sw)).toBeFocused();
        await page.keyboard.press('PageDown');
        await expect(edge(toLeft)).toBeFocused();
        await expect
          .soft(edge(toLeft))
          .toHaveAccessibleName('Network EXP from left (eth0) to EXP');
        await page.keyboard.press('PageDown');
        await expect(edge(toRight)).toBeFocused();
        await page.keyboard.press('PageDown');
        await expect(edge(toLeft)).toBeFocused();
        await page.keyboard.press('PageUp');
        await expect(edge(toRight)).toBeFocused();
        await expect.soft(stops).toHaveCount(1);
        await expect
          .soft(edge(toRight))
          .toHaveAccessibleDescription(
            /^Arrow keys move to the nodes, Page Down to the next connection\./,
          );
        // Halfway along the connection, the right device lies to the right.
        await page.keyboard.press('ArrowRight');
        await expect(node(right)).toBeFocused();
        await page.keyboard.press('PageUp');
        await expect(edge(toRight)).toBeFocused();

        await node(note).focus();
        await page.keyboard.press('PageDown');
        await expect.soft(node(note)).toBeFocused();
        await expect
          .soft(builder)
          .toHaveAnnounced('Lab notes has no connections.');
      });

      await test.step('Tab leaves the canvas and Shift+Tab comes back to the same node; focus looks different from selection', async () => {
        await node(left).focus();
        await zoomIn.focus();
        const unfocused = await nodeFocusLook(page, left.id);
        await page.keyboard.press('Shift+Tab');
        await expect(node(left)).toBeFocused();
        await expect.soft(node(left)).toHaveAttribute('aria-pressed', 'false');
        const focused = await nodeFocusLook(page, left.id);
        expect
          .soft(focused, 'focusing the node must change its appearance')
          .not.toBe(unfocused);

        // Shift and an arrow key move only selected nodes, and say so.
        await page.keyboard.press('Shift+ArrowRight');
        await expect
          .soft(builder)
          .toHaveAnnounced('Select the nodes to move first.');

        // Enter selects it, and the next Tab leaves the canvas.
        await page.keyboard.press('Enter');
        await expect.soft(node(left)).toHaveAttribute('aria-pressed', 'true');
        await page.keyboard.press('Tab');
        await expect.soft(zoomIn).toBeFocused();
        expect
          .soft(
            await nodeFocusLook(page, left.id),
            'selection must not look like focus',
          )
          .not.toBe(focused);
      });

      await test.step('Delete moves focus to the nearest node, which becomes the Tab stop', async () => {
        await node(note).focus();
        await page.keyboard.press('Delete');
        await expect(node(note)).toHaveCount(0);
        await expect.soft(node(right)).toBeFocused();
        await expect.soft(stops).toHaveCount(1);
        await expect.soft(node(right)).toHaveAttribute('tabindex', '0');
      });
      expectNoFatal(issues);
    },
  );
});

// --- axe scans in both themes -------------------------------------------------

// A diagram of the legacy Builder with nothing drawn in it, which converts
// with one warning.
const EMPTY_LEGACY_DIAGRAM =
  '<mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/></root></mxGraphModel>';

const DIALOGS = [
  { action: 'publish', title: 'Publish diagram' },
  { action: 'upload', title: 'Upload diagram' },
  { action: 'download', title: 'Download diagram' },
  { action: 'scenario', title: 'Scenarios' },
  { action: 'connect', title: 'Add a connection' },
  { action: 'regroup', title: 'Move to a group' },
  { action: 'history', title: 'Draft History' },
];

// Opens a dialog with Enter on `opener`, scans it and walks the modal dialog
// pattern: the dialog takes focus, Tab and Shift+Tab wrap inside it, nothing
// behind it can take focus, and its radio groups are grouped and laid out
// beside their labels. Then closes it with Escape and checks focus returns to
// the opener.
async function scanDialog(page, builder, opener, surface, title) {
  await opener.press('Enter');
  await expect(builder.dialog).toBeVisible();
  if (title) {
    // Exact: a dialog's title may begin the name of a heading inside it,
    // as "Scenarios" does "Scenarios of this diagram".
    await expect
      .soft(builder.dialog.getByRole('heading', { name: title, exact: true }))
      .toBeVisible();
    await expect.soft(builder.dialog).toHaveAttribute('aria-modal', 'true');
  }
  await expect.soft(builder.dialog, `${surface} takes focus`).toBeFocused();

  await expectAccessible(page, { soft: true, label: `axe on ${surface}` });

  // From the dialog, Shift+Tab reaches its last control; Tab from there
  // wraps to the first, Close dialog, and Shift+Tab from that wraps back.
  const close = builder.dialog.getByRole('button', { name: 'Close dialog' });
  await page.keyboard.press('Shift+Tab');
  expect.soft(await focusInDialog(page), `${surface}: Shift+Tab`).toBe(true);
  await page.keyboard.press('Tab');
  await expect
    .soft(close, `${surface}: Tab from the last control`)
    .toBeFocused();
  await page.keyboard.press('Shift+Tab');
  expect
    .soft(await focusInDialog(page), `${surface}: Shift+Tab from Close dialog`)
    .toBe(true);
  expect
    .soft(await focusableBehindDialog(page), `${surface}: page behind`)
    .toEqual([]);
  await expectRadioGroups(builder.dialog, surface);

  // Escape hides the tooltip of a focused control first (WCAG 1.4.13).
  if (await builder.dialog.locator('.builder-tooltip').count()) {
    await page.keyboard.press('Escape');
  }
  await page.keyboard.press('Escape');
  await expect(builder.dialog).toHaveCount(0);
  await expect.soft(opener, `${surface} returns focus`).toBeFocused();
}

// A new draft, made from the drafts landing with the page in `scheme`.
//
// The server lists no disk images (it runs no minimega). One that is not
// the new device's gives a device a drive image warning, so the scans cover
// the Inspector's checks and the Diagram checks dialog with an issue in
// each.
async function newDraftIn(page, builder, scheme) {
  await openWithScheme(page, builder, scheme);
  await page.route('**/api/v1/disks', (route) =>
    route.fulfill({ json: { disks: [{ kind: 'VM', name: 'other.qc2' }] } }),
  );

  return builder.createBlank();
}

// The editor the dialog scans start from: a new draft in `scheme` with a
// device connected to a switch, so the scans cover the Inspector's interface
// form and its oneOf kind picker; the device selected, with its More
// settings section open and a row of its names and values.
async function connectedDraftIn(page, builder, scheme) {
  const draft = await newDraftIn(page, builder, scheme);

  await addWithKeyboard(builder, 'device', 1);
  await addWithKeyboard(builder, 'switch', 2);
  await connectWithKeyboard(builder);
  await builder.expectSummary('1 connection');
  await builder.selectInOutline('node');
  await expect(page.locator('.builder-inspector__subject')).toContainText(
    'Device node',
  );
  await builder.inspector
    .getByTestId('inspector-section')
    .locator('summary')
    .click();
  await builder.inspector.getByRole('button', { name: 'Add label' }).click();
  await expect(builder.inspector.getByLabel('Label 1 Name')).toBeFocused();

  return draft;
}

// The controls of the editor's header.
function headerControls(page) {
  const counts = page.getByRole('list', { name: 'Diagram contents' });

  return {
    back: page.getByRole('button', { name: 'Back to drafts' }),
    help: page.getByRole('link', { name: 'Help (opens in a new tab)' }),
    name: page.getByTestId('builder-name'),
    edit: page.getByRole('button', { name: 'Edit diagram name' }),
    counts,
    countsTip: page.getByTestId('counts-tooltip'),
    reset: page.getByRole('button', { name: 'Reset view' }),
    commands: page.getByTestId('editor-commands'),
    theme: page.getByTestId('editor-theme'),
    shortcuts: page.getByRole('button', { name: 'Shortcuts', exact: true }),
    checks: page.getByTestId('builder-checks'),
    settings: page.getByTestId('editor-settings'),
    focusMode: page.getByRole('button', { name: 'Focus mode', exact: true }),
    count: (index) => counts.getByRole('listitem').nth(index),
    countButton: (index) => counts.getByRole('button').nth(index),
  };
}

// The axe scans in the theme `scheme`. Each view, dialog and surface is
// scanned by a test of its own, which makes its own draft. Each is tagged as
// the scans of every surface are: they run in Firefox too, and in CI's axe
// job.
function axeScans(scheme) {
  const tag = ['@cross-browser', '@axe'];

  test(
    'a new draft titles the page, its heading takes focus, and the Inspector shows its Details',
    { tag },
    async ({ page, builder, issues }) => {
      await newDraftIn(page, builder, scheme);

      await test.step('the editor titles the page and its heading takes focus', async () => {
        const heading = page.getByRole('heading', { level: 1 });
        // Blank drafts are numbered after the first.
        await expect
          .soft(heading)
          .toHaveText(/^Untitled topology( \d+)? – Builder\s*$/);
        await expect.soft(heading).toBeFocused();
        await expect
          .soft(page)
          .toHaveTitle(/^Untitled topology( \d+)? - Builder - phēnix$/);
        await expect
          .soft(page.locator('html'))
          .toHaveAttribute('lang', 'en-US');
      });

      // With nothing selected the Inspector shows the diagram, and below
      // its fields who made it and who edited it last.
      await test.step('the Inspector’s Details of a new draft', async () => {
        const inspector = 'section[aria-labelledby="inspector-title"]';
        const details = builder.inspector.getByTestId('inspector-details');
        await expect
          .soft(details.getByRole('heading', { level: 3 }))
          .toHaveText('Details');
        await expect
          .soft(details.locator('dt'))
          .toHaveText(['Created', 'Last edited']);
        for (const time of await details.locator('time').all()) {
          await expect
            .soft(time)
            .toHaveAttribute('datetime', /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/);
        }
        await expectAccessible(page, {
          include: inspector,
          soft: true,
          label: `axe on the Inspector's Details (${scheme})`,
        });
        const found = await invisibleText(page, inspector);
        expect
          .soft(found, `invisible text: ${JSON.stringify(found, null, 2)}`)
          .toEqual([]);
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the header goes in Tab order, the save state sits beside Draft History, and the name has a pencil and a field',
    { tag },
    async ({ page, builder, issues }) => {
      await newDraftIn(page, builder, scheme);
      const {
        back,
        help,
        name,
        edit,
        reset,
        commands,
        theme,
        shortcuts,
        checks,
        settings,
        focusMode,
        countButton,
      } = headerControls(page);

      await test.step('the header has Back to drafts, the name and its pencil, the counts, the checks, Reset view, Commands, the theme, Shortcuts, Settings, the Help link of the landing and Focus mode', async () => {
        // Left to right, as Tab goes: Back to drafts, the name's pencil, the
        // counts (one stop, at the first count), then the checks, Reset
        // view, Commands, the theme, Shortcuts, Settings, Help and Focus
        // mode. The order is the same in either theme, so it is checked in
        // the light one.
        if (scheme === 'light') {
          await back.focus();
          for (const next of [
            edit,
            countButton(0),
            checks,
            reset,
            commands,
            theme,
            shortcuts,
            settings,
            help,
            focusMode,
          ]) {
            await page.keyboard.press('Tab');
            await expect.soft(next).toBeFocused();
          }
        }
        // The save state is in the toolbar, just after Draft History on its
        // row: text, which neither Tab nor the toolbar's arrow keys stop at.
        const history = builder.toolbar('history');
        const saved = await builder.saveState.boundingBox();
        const before = await history.boundingBox();
        expect
          .soft(saved.x - (before.x + before.width), 'save state after history')
          .toBeGreaterThanOrEqual(0);
        expect
          .soft(saved.x - (before.x + before.width), 'save state by history')
          .toBeLessThan(16);
        expect
          .soft(
            Math.abs(
              saved.y + saved.height / 2 - (before.y + before.height / 2),
            ),
            'save state on history’s row',
          )
          .toBeLessThan(4);
        await expect
          .soft(builder.saveState.locator('xpath=self::p'))
          .toHaveCount(1);
        await expect.soft(builder.saveState).not.toHaveAttribute('tabindex');

        // The name is text, the pencil after it is named for what it does
        // and described by the name, and a tooltip says what it does.
        await expect.soft(name).toHaveText(/^Untitled topology( \d+)?$/);
        const title = (await name.textContent()).trim();
        await expect.soft(edit).toHaveAccessibleDescription(title);
        await edit.hover();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText('Edit diagram name');
        const target = await edit.boundingBox();
        expect.soft(target.width, 'pencil width').toBeGreaterThanOrEqual(24);
        expect.soft(target.height, 'pencil height').toBeGreaterThanOrEqual(24);

        // The pencil shows a field in the name's place, the name selected,
        // whose label is read but not shown. Escape keeps the name, Enter
        // saves it; either way focus goes back to the pencil, and the
        // outcome is announced.
        const field = page.getByRole('textbox', { name: 'Diagram name' });
        await edit.press('Enter');
        await expect.soft(field).toBeFocused();
        await expect.soft(field).toHaveValue(title);
        expect
          .soft(
            await field.evaluate((input) => [
              input.selectionStart,
              input.selectionEnd,
            ]),
            'the name is selected',
          )
          .toEqual([0, title.length]);
        await expect
          .soft(page.locator('label[for="builder-doc-name"]'))
          .toHaveClass(/builder-visually-hidden/);
        await field.press('End');
        await field.pressSequentially(' draft');
        await field.press('Escape');
        await expect.soft(edit).toBeFocused();
        await expect.soft(field).toHaveCount(0);
        await expect.soft(name).toHaveText(title);
        await expect.soft(builder).toHaveAnnounced('Diagram name not changed.');
        await edit.press('Enter');
        await field.fill(`${title} with a name long enough to be cut off`);
        await field.press('Enter');
        await expect.soft(edit).toBeFocused();
        await expect.soft(builder).toHaveAnnounced('Renamed diagram');
        // A long name is cut off with an ellipsis, and stays whole in the
        // text, the pencil's description and the tooltips.
        const long = `${title} with a name long enough to be cut off`;
        await expect.soft(name).toHaveText(long);
        await expect.soft(edit).toHaveAccessibleDescription(long);
        expect
          .soft(
            await name.evaluate(
              (text) =>
                getComputedStyle(text).textOverflow === 'ellipsis' &&
                text.scrollWidth > text.clientWidth,
            ),
            'the long name is cut off',
          )
          .toBe(true);
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText(`Edit diagram name: ${long}`);
        // The pointer, left where the pencil was, may be over the longer
        // name now: it moves off, then onto the name.
        await page.mouse.move(0, 0);
        await name.hover();
        await expect.soft(page.getByTestId('header-tooltip')).toHaveText(long);
        // Emptied, it shows a muted Untitled diagram.
        await edit.click();
        await field.fill('');
        await field.press('Enter');
        await expect.soft(name).toHaveText('Untitled diagram');
        await expect.soft(name).toHaveClass(/is-empty/);
        await expectReadable(name, 'Untitled diagram');
        await edit.click();
        await field.fill(title);
        await field.press('Enter');
        await expect.soft(name).toHaveText(title);
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the header’s counts are buttons in one Tab stop, with tooltips, that the arrow keys, Home and End move between',
    { tag },
    async ({ page, builder, issues }) => {
      await newDraftIn(page, builder, scheme);
      const { counts, countsTip, count, countButton } = headerControls(page);

      await test.step('the counts of a blank diagram', async () => {
        // Each count is a button: an icon and a number, read in words. Its
        // tooltip, on hover and on focus, says what it selects, which for a
        // count of none is the count itself. The arrow keys, Home and End
        // move between the counts, and the list keeps one Tab stop.
        await expect.soft(counts.locator('svg.builder-icon')).toHaveCount(6);
        await expect
          .soft(counts.getByRole('listitem'))
          .toHaveText([
            '0 devices,',
            '0 switches,',
            '0 networks,',
            '0 connections,',
            '0 groups,',
            '0 notes',
          ]);
        // The comma is the list's, not part of a button's name. With
        // nothing to select a count is unavailable and has no description.
        await expect
          .soft(counts.getByRole('button'))
          .toHaveText([
            '0 devices',
            '0 switches',
            '0 networks',
            '0 connections',
            '0 groups',
            '0 notes',
          ]);
        for (let index = 0; index < 6; index += 1) {
          await expect
            .soft(countButton(index))
            .toHaveAttribute('aria-disabled', 'true');
          await expect.soft(countButton(index)).toHaveAccessibleDescription('');
        }
        await count(3).hover();
        await expect.soft(countsTip).toHaveText('0 connections');
        await countButton(0).focus();
        await expect.soft(countsTip).toHaveText('0 devices');
        await page.keyboard.press('ArrowRight');
        await expect.soft(countButton(1)).toBeFocused();
        await expect.soft(countsTip).toHaveText('0 switches');
        await page.keyboard.press('End');
        await expect.soft(countsTip).toHaveText('0 notes');
        // The counts are in a box, which holds a count's focus ring. A
        // count is large enough to press (WCAG 2.5.8).
        await expect.soft(counts).toHaveCSS('border-top-style', 'solid');
        const room = await countButton(5).evaluate((item) => {
          const own = item.getBoundingClientRect();
          const box = item.closest('ul').getBoundingClientRect();
          const style = getComputedStyle(item);
          const reach =
            parseFloat(style.outlineWidth) + parseFloat(style.outlineOffset);

          return {
            ring: Math.min(
              own.top - reach - box.top,
              box.bottom - own.bottom - reach,
              box.right - own.right - reach,
            ),
            size: Math.min(own.width, own.height),
          };
        });
        expect
          .soft(room.ring, 'focus ring inside the counts box')
          .toBeGreaterThan(1);
        expect.soft(room.size, 'a count’s size').toBeGreaterThanOrEqual(24);
        await page.keyboard.press('ArrowRight');
        await expect.soft(countButton(0)).toBeFocused();
        await expect.soft(counts.locator('[tabindex="0"]')).toHaveCount(1);
        await page.keyboard.press('Escape');
        await expect.soft(countsTip).toHaveCount(0);
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the header’s Shortcuts, Commands, theme, Settings, Help and Focus mode say what they do, give their keys and are readable',
    { tag },
    async ({ page, builder, issues }) => {
      await newDraftIn(page, builder, scheme);
      const {
        back,
        help,
        counts,
        reset,
        commands,
        theme,
        shortcuts,
        settings,
        focusMode,
      } = headerControls(page);

      await test.step('Shortcuts, Commands, the theme, Settings, Help and Focus mode', async () => {
        // Shortcuts shows the key that opens the shortcut sheet, as a
        // picture of it: its name stays Shortcuts, and its description says
        // the key. It opens the sheet, and gets focus back from it.
        await expect.soft(shortcuts.locator('kbd')).toHaveText(['?']);
        await expect.soft(shortcuts.locator('kbd')).toBeVisible();
        await expect.soft(shortcuts).toHaveAccessibleDescription('?');
        await shortcuts.focus();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText('Keyboard shortcuts (?)');
        await page.keyboard.press('Enter');
        await expect.soft(page.getByTestId('shortcuts-dialog')).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(shortcuts).toBeFocused();
        // Commands shows the palette's key, and opens the palette; the theme
        // button shows the theme in use, and its name and tooltip say what
        // a press changes it to.
        await expect.soft(commands).toHaveAccessibleName('Commands');
        await expect
          .soft(commands)
          .toHaveAttribute(
            'aria-keyshortcuts',
            process.platform === 'darwin' ? 'Meta+K' : 'Control+K',
          );
        await expect.soft(commands.locator('kbd')).toHaveCount(2);
        await commands.focus();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText(/^Command palette \(/);
        await page.keyboard.press('Enter');
        await expect.soft(page.getByTestId('commands-dialog')).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(commands).toBeFocused();
        const next = scheme === 'dark' ? 'Light' : 'Dark';
        await expect
          .soft(theme)
          .toHaveAccessibleName(`Theme: System. Switch to ${next} theme.`);
        await expect.soft(theme).toHaveText('System');
        // At this width it shows only its icon, which leaves the counts
        // centered, so its tooltip names it too. In a window wide enough
        // for its label, the tooltip only says what a press does.
        await theme.hover();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText(`Theme: System. Switch to ${next} theme`);
        const initial = page.viewportSize();
        await page.setViewportSize({ width: 2560, height: initial.height });
        await page.mouse.move(0, 0);
        await theme.hover();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText(`Switch to ${next} theme`);
        await page.setViewportSize(initial);
        // The narrower window leaves the pointer outside it, where the theme
        // button was in the wide one. It comes back into the page, and the
        // theme's tooltip goes, before it moves onto Settings: in Firefox a
        // pointer that enters the page straight onto a control need not
        // bring the pointermove with the mouseenter that a tooltip waits
        // for (see whenPointed in fixedTooltip.js).
        await page.mouse.move(0, 0);
        await expect(page.getByTestId('header-tooltip')).toHaveCount(0);
        // Settings' and Help's tooltips name them too, and Settings' gives
        // its key.
        const settingsKeys =
          process.platform === 'darwin' ? '⌥⇧S' : 'Alt+Shift+S';
        await settings.hover();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText(`Builder settings (${settingsKeys})`);
        await expect
          .soft(settings)
          .toHaveAttribute('aria-keyshortcuts', 'Alt+Shift+S');
        await expect.soft(settings).toHaveAccessibleDescription(settingsKeys);
        await help.focus();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText('Help (opens in a new tab)');

        // Focus mode shows only its icon, named and described by its
        // tooltip; a press keeps focus on it as it becomes Exit focus mode,
        // and the tooltip follows.
        await focusMode.focus();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText(/^Focus mode: hide the navigation bar/);
        await expect
          .soft(focusMode)
          .toHaveAccessibleDescription(/^Hide the navigation bar/);
        await expect
          .soft(focusMode)
          .toHaveAttribute(
            'aria-keyshortcuts',
            /^(Shift\+Meta|Control\+Shift)\+F$/,
          );
        await page.keyboard.press('Enter');
        const exit = page.getByRole('button', { name: 'Exit focus mode' });
        await expect.soft(exit).toBeFocused();
        await expect.soft(page.locator('.navbar')).toBeHidden();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText(/^Exit focus mode: show the navigation bar again/);
        await expectReadable(exit, 'Exit focus mode');
        await page.keyboard.press('Enter');
        await expect.soft(focusMode).toBeFocused();
        await expect.soft(page.locator('.navbar')).toBeVisible();

        await expect.soft(back.locator('svg.builder-icon')).toHaveCount(1);
        await expect
          .soft(help)
          .toHaveAttribute(
            'href',
            'https://phenix.sceptre.dev/latest/builder/',
          );
        await expect.soft(help).toHaveAttribute('target', '_blank');
        await expect.soft(help).toHaveAttribute('rel', /\bnoopener\b/);
        await expectReadable(back, 'Back to drafts');
        await expectReadable(help, 'editor Help link');
        await expectReadable(reset, 'Reset view');
        await expectReadable(commands, 'Commands');
        await expectReadable(theme, 'theme button');
        await expectReadable(shortcuts, 'Shortcuts');
        await expectReadable(counts, 'counts');
        await expectReadable(builder.saveState, 'save state');
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the editor of a connected device has no text colored like its background, and passes axe with the System theme',
    { tag },
    async ({ page, builder, issues }) => {
      await connectedDraftIn(page, builder, scheme);

      // axe reports text colored like its background as incomplete, not as
      // a violation: Bulma once drew Inspector labels and "Error:" prefixes
      // white on white in the light theme.
      await test.step('no text is colored like its background', async () => {
        const found = await invisibleText(page);
        expect
          .soft(found, `invisible text: ${JSON.stringify(found, null, 2)}`)
          .toEqual([]);
      });

      await test.step('editor, System preference', () =>
        expectAccessible(page, {
          soft: true,
          label: `axe on editor (${scheme}, system)`,
        }));
      expectNoFatal(issues);
    },
  );

  for (const { action, title } of DIALOGS) {
    test(
      `the ${title} dialog passes axe and keeps focus inside itself`,
      { tag },
      async ({ page, builder, issues }) => {
        await connectedDraftIn(page, builder, scheme);

        await test.step(`${title} dialog`, () =>
          scanDialog(
            page,
            builder,
            builder.toolbar(action),
            `${title} dialog (${scheme})`,
            title,
          ));
        expectNoFatal(issues);
      },
    );
  }

  test(
    'the Upload dialog passes axe with its legacy source and with the warnings of a conversion',
    { tag },
    async ({ page, builder, issues }) => {
      await connectedDraftIn(page, builder, scheme);

      // Upload's legacy source, and the warnings of a conversion, which
      // take the place of the form. Escape there makes no draft, so the
      // open one stays.
      await test.step('Upload dialog, legacy source and its warnings', async () => {
        const opener = builder.toolbar('upload');
        const surface = `Upload dialog, legacy source (${scheme})`;

        await opener.press('Enter');
        await expect(builder.dialog).toBeVisible();
        await builder.dialog
          .getByLabel('Legacy Builder diagram or Topology', { exact: true })
          .check();
        const file = builder.dialog.getByTestId('upload-legacy-file');
        await expect(file).toBeVisible();
        await expectAccessible(page, {
          soft: true,
          label: `axe on ${surface}`,
        });
        await expectRadioGroups(builder.dialog, surface);

        await file.setInputFiles({
          name: 'empty.xml',
          mimeType: 'text/xml',
          buffer: Buffer.from(EMPTY_LEGACY_DIAGRAM),
        });
        await builder.dialog.getByTestId('upload-submit').click();
        await expect(
          builder.dialog.getByTestId('upload-warnings'),
        ).toContainText('The diagram has no nodes.');
        await expect
          .soft(builder.dialog.getByTestId('upload-continue'))
          .toBeFocused();
        await expectAccessible(page, {
          soft: true,
          label: `axe on the warnings of ${surface}`,
        });
        expect
          .soft(await focusableBehindDialog(page), `${surface}: page behind`)
          .toEqual([]);

        await page.keyboard.press('Escape');
        await expect(builder.dialog).toHaveCount(0);
        await expect.soft(opener, `${surface} returns focus`).toBeFocused();
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the command palette passes axe with an unavailable command, a node search and no results',
    { tag },
    async ({ page, builder, issues }) => {
      await connectedDraftIn(page, builder, scheme);

      // The command palette's first row is its search field, which takes
      // focus. The scans cover an unavailable command, a node search and no
      // results.
      await test.step('Command palette', async () => {
        const opener = page.getByTestId('editor-commands');
        const field = builder.dialog.getByRole('combobox', {
          name: 'Search commands',
        });
        const surface = `command palette (${scheme})`;

        await opener.press('Enter');
        await expect(field).toBeFocused();
        await expect
          .soft(builder.dialog)
          .toHaveAccessibleName('Command palette');
        await expectAccessible(page, {
          soft: true,
          label: `axe on ${surface}`,
        });
        for (const [query, shown] of [
          ['grp', builder.dialog.getByRole('option').first()],
          ['@node', builder.dialog.getByRole('option', { name: 'node' })],
          ['zzz', page.getByTestId('command-palette-empty')],
        ]) {
          await field.fill(query);
          await expect(shown).toBeVisible();
          await expectAccessible(page, {
            soft: true,
            label: `axe on ${surface}, "${query}"`,
          });
        }
        expect
          .soft(await focusableBehindDialog(page), `${surface}: page behind`)
          .toEqual([]);

        await page.keyboard.press('Escape');
        await expect(builder.dialog).toHaveCount(0);
        await expect.soft(opener, `${surface} returns focus`).toBeFocused();
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the Diagram checks dialog passes axe with a drive image warning in it',
    { tag },
    async ({ page, builder, issues }) => {
      await connectedDraftIn(page, builder, scheme);

      await test.step('Diagram checks dialog', async () => {
        await expect
          .soft(builder.inspector.getByTestId('inspector-checks'))
          .toContainText('drive image "ubuntu.qc2"');
        await scanDialog(
          page,
          builder,
          page.getByTestId('builder-checks'),
          `Diagram checks dialog (${scheme})`,
          'Diagram checks',
        );
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the Builder settings dialog passes axe and keeps focus inside itself',
    { tag },
    async ({ page, builder, issues }) => {
      await connectedDraftIn(page, builder, scheme);

      await test.step('Builder settings dialog', () =>
        scanDialog(
          page,
          builder,
          page.getByTestId('editor-settings'),
          `Builder settings dialog (${scheme})`,
          'Builder settings',
        ));
      expectNoFatal(issues);
    },
  );

  test(
    'the Auto-group by name pattern dialog passes axe with its message showing',
    { tag },
    async ({ page, builder, issues }) => {
      await connectedDraftIn(page, builder, scheme);

      // Opened from the Auto-group menu, with focus in its field, and
      // scanned with its message showing.
      await test.step('Auto-group by name pattern dialog', async () => {
        const opener = builder.toolbar('auto-group');
        const surface = `Auto-group by name pattern dialog (${scheme})`;

        await opener.press('ArrowUp');
        await page.keyboard.press('Enter');
        await expect(
          builder.dialog.getByRole('heading', {
            name: 'Auto-group by name pattern',
          }),
        ).toBeVisible();
        const field = builder.dialog.getByRole('textbox', {
          name: 'Name pattern',
        });
        await expect.soft(field, `${surface} focuses its field`).toBeFocused();
        await field.fill('(');
        await page.keyboard.press('Enter');
        await expect(
          builder.dialog.getByTestId('group-pattern-error'),
        ).toHaveText(/^That is not a valid regular expression\./);
        await expectAccessible(page, {
          soft: true,
          label: `axe on ${surface}`,
        });
        await expectReadable(builder.dialog, surface);
        expect
          .soft(await focusableBehindDialog(page), `${surface}: page behind`)
          .toEqual([]);
        // Tab wraps inside the dialog: from Group, its last control, to
        // Close dialog, its first.
        await builder.dialog.getByRole('button', { name: 'Group' }).focus();
        await page.keyboard.press('Tab');
        await expect
          .soft(builder.dialog.getByRole('button', { name: 'Close dialog' }))
          .toBeFocused();
        await page.keyboard.press('Escape');
        await expect(builder.dialog).toHaveCount(0);
        await expect.soft(opener, `${surface} returns focus`).toBeFocused();
      });
      expectNoFatal(issues);
    },
  );

  // The same in either theme, so checked in the light one.
  if (scheme === 'light') {
    test(
      'a click outside a dialog closes it; a drag across its edge does not',
      { tag },
      async ({ page, builder, issues }) => {
        await connectedDraftIn(page, builder, scheme);

        // Every Builder dialog shares this through BuilderDialog.
        await test.step('a click outside a dialog closes it; a drag across its edge does not', async () => {
          const opener = builder.toolbar('scenario');
          await opener.press('Enter');
          await expect(builder.dialog).toBeVisible();
          // The Scenarios dialog's controls are named by their labels.
          await expect
            .soft(builder.dialog.getByLabel('Add a stored scenario'))
            .toBeVisible();
          await expect
            .soft(
              builder.dialog.getByLabel('Scenario config file (JSON or YAML)'),
            )
            .toBeVisible();
          await expect
            .soft(
              builder.dialog.getByRole('group', {
                name: 'Upload a scenario file',
              }),
            )
            .toBeVisible();

          // The drag ends in a click on the dialog, outside its box.
          const outside = await backdropPoint(builder.dialog);
          const legend = await builder.dialog
            .getByRole('heading', { name: 'Scenarios of this diagram' })
            .boundingBox();
          const inside = { x: legend.x + 2, y: legend.y + legend.height / 2 };
          await page.mouse.move(inside.x, inside.y);
          await page.mouse.down();
          await page.mouse.move(outside.x, outside.y, { steps: 5 });
          await page.mouse.up();
          await expect.soft(builder.dialog, 'after a drag out').toBeVisible();

          // A press outside released inside ends in a click on the dialog too.
          await page.mouse.down();
          await page.mouse.move(inside.x, inside.y, { steps: 5 });
          await page.mouse.up();
          await expect.soft(builder.dialog, 'after a drag in').toBeVisible();

          await page.mouse.click(outside.x, outside.y);
          await expect(builder.dialog).toHaveCount(0);
          await expect
            .soft(opener, 'focus returns to the opener')
            .toBeFocused();
        });
        expectNoFatal(issues);
      },
    );
  }

  test(
    'the editor passes axe with its theme chosen explicitly',
    { tag },
    async ({ page, builder, issues }) => {
      await connectedDraftIn(page, builder, scheme);

      await test.step(`editor, ${scheme} chosen explicitly`, async () => {
        await chooseTheme(page, scheme, { soft: true });
        await expect
          .soft(root(page))
          .toHaveAttribute('data-builder-theme', scheme);
        await expectAccessible(page, {
          soft: true,
          label: `axe on editor (${scheme}, explicit)`,
        });
      });
      expectNoFatal(issues);
    },
  );

  // The same in either theme, so checked in the light one.
  if (scheme === 'light') {
    test(
      'the toolbar is one Tab stop that arrow keys, Home and End move through',
      { tag },
      async ({ page, builder, issues }) => {
        await connectedDraftIn(page, builder, scheme);

        await test.step('the toolbar is one Tab stop that arrow keys, Home and End move through', async () => {
          const buttons = page
            .getByRole('toolbar', { name: 'Builder actions' })
            .getByRole('button');
          const tabStops = buttons.and(page.locator('[tabindex="0"]'));

          await expect.soft(tabStops).toHaveCount(1);
          await builder.toolbar('copy').focus();
          await page.keyboard.press('ArrowRight');
          await expect.soft(builder.toolbar('paste')).toBeFocused();
          await page.keyboard.press('ArrowLeft');
          await expect.soft(builder.toolbar('copy')).toBeFocused();
          await page.keyboard.press('Home');
          await expect.soft(builder.toolbar('undo')).toBeFocused();
          await page.keyboard.press('End');
          await expect.soft(buttons.last()).toBeFocused();
          await expect.soft(tabStops).toHaveCount(1);
          await expect.soft(tabStops).toBeFocused();
          // Draft History ends it, just after Minimap, in the same group;
          // Commands and the theme are in the header. The save state after
          // it is text, which the arrow keys pass by.
          const history = builder.toolbar('history');
          await expect.soft(history).toBeFocused();
          await expect.soft(history).toHaveAccessibleName('Draft History');
          await page.keyboard.press('ArrowRight');
          await expect.soft(builder.toolbar('undo')).toBeFocused();
          await page.keyboard.press('ArrowLeft');
          await expect.soft(history).toBeFocused();
          expect
            .soft(
              await history.evaluate(
                (button) => button.previousElementSibling?.dataset.testid,
              ),
              'the button before Draft History',
            )
            .toBe('toolbar-minimap');
          // Add connection and Move to group come just before Minimap, in
          // the same group, and say that they open a dialog.
          expect
            .soft(
              await history.evaluate((button) =>
                [...button.parentElement.querySelectorAll('button')].map(
                  (each) => each.dataset.testid,
                ),
              ),
              'the buttons of the last group',
            )
            .toEqual([
              'toolbar-connect',
              'toolbar-regroup',
              'toolbar-minimap',
              'toolbar-history',
            ]);
          for (const [action, name] of [
            ['connect', 'Add connection'],
            ['regroup', 'Move to group'],
          ]) {
            await expect
              .soft(builder.toolbar(action))
              .toHaveAccessibleName(name);
            await expect
              .soft(builder.toolbar(action))
              .toHaveAttribute('aria-haspopup', 'dialog');
          }
          await expect
            .soft(
              buttons.filter({ hasText: /^\s*(Commands|System|Light|Dark)/ }),
            )
            .toHaveCount(0);

          // The menu buttons are in the sequence; Down opens a menu, whose
          // items are not in it, and Escape closes the menu onto its button.
          await builder.toolbar('ungroup').focus();
          await page.keyboard.press('ArrowRight');
          await expect.soft(builder.toolbar('auto-group')).toBeFocused();
          await page.keyboard.press('ArrowRight');
          await expect.soft(builder.toolbar('layout')).toBeFocused();
          await page.keyboard.press('ArrowDown');
          await expect
            .soft(page.getByRole('menuitemradio', { name: 'ELK layered' }))
            .toBeFocused();
          await page.keyboard.press('Escape');
          await expect.soft(builder.toolbar('layout')).toBeFocused();
          await expect.soft(page.getByRole('menu')).toHaveCount(0);
          await expect.soft(tabStops).toHaveCount(1);

          for (const { action } of DIALOGS) {
            await expect
              .soft(builder.toolbar(action))
              .toHaveAttribute('aria-haspopup', 'dialog');
          }
        });
        expectNoFatal(issues);
      },
    );
  }

  test(
    'the layout menu passes axe',
    { tag },
    async ({ page, builder, issues }) => {
      await connectedDraftIn(page, builder, scheme);

      await test.step('axe finds no serious violations in the layout menu', async () => {
        await builder.toolbar('layout').focus();
        await page.keyboard.press('ArrowDown');
        await expect
          .soft(page.getByRole('menuitemradio', { name: 'ELK layered' }))
          .toBeFocused();
        await expectAccessible(page, {
          soft: true,
          label: `axe on the layout menu (${scheme})`,
        });
        await page.keyboard.press('Escape');
        await expect.soft(builder.toolbar('layout')).toBeFocused();
      });
      expectNoFatal(issues);
    },
  );

  test(
    'toolbar Ungroup and Delete move focus to the outline row that takes their place',
    { tag },
    async ({ page, builder, issues }) => {
      await connectedDraftIn(page, builder, scheme);

      await test.step('toolbar Ungroup and Delete move focus to the outline row that takes their place', async () => {
        await builder.selectInOutline('node');
        await builder.toolbar('group').press('Enter');
        await expect(rows(builder)).toHaveCount(3);
        await builder.toolbar('ungroup').press('Enter');
        await expect(rows(builder)).toHaveCount(2);
        await expect.soft(row(builder, 'node'), 'after Ungroup').toBeFocused();

        // Rows sort by kind: the switch, then the device.
        await builder.selectInOutline('node');
        // Delete looks like the toolbar's other buttons: the danger colors
        // are for the confirmations that ask before something is lost.
        const look = (action) =>
          builder.toolbar(action).evaluate((element) => {
            const { borderTopColor, color } = getComputedStyle(element);

            return { borderTopColor, color };
          });
        expect
          .soft(await look('delete'), 'Delete is styled as Copy is')
          .toEqual(await look('copy'));
        await builder.toolbar('delete').press('Enter');
        await expect(rows(builder)).toHaveCount(1);
        await expect.soft(row(builder, 'EXP'), 'after Delete').toBeFocused();

        // With no row left, focus goes to the canvas.
        await builder.selectInOutline('EXP');
        await builder.toolbar('delete').press('Enter');
        await expect(rows(builder)).toHaveCount(0);
        await expect
          .soft(builder.canvas, 'after the last Delete')
          .toBeFocused();
      });
      expectNoFatal(issues);
    },
  );

  test(
    'a diagram with shapes, icons and lines passes axe with a line and with a rectangle selected',
    { tag },
    async ({ page, builder, issues }) => {
      await newDraftIn(page, builder, scheme);

      await test.step('axe finds no serious violations in a diagram with shapes, icons and lines', async () => {
        for (const item of ['rectangle', 'circle', 'icon', 'line']) {
          await builder.palette(item).click();
        }
        await expect(rows(builder)).toHaveCount(4);
        // The new line is selected: its points have their handles.
        await expect(page.getByTestId('line-point-1')).toBeVisible();
        await expectAccessible(page, {
          soft: true,
          label: `axe on a diagram with a selected line (${scheme})`,
        });
        // A selected rectangle has the handles that resize it.
        await builder.selectInOutline('Rectangle');
        await expect(
          page.locator('.vue-flow__resize-control').first(),
        ).toBeVisible();
        await expectAccessible(page, {
          soft: true,
          label: `axe on a diagram with a selected rectangle (${scheme})`,
        });
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the drafts landing passes axe, names its actions and gives focus to the card of the draft just closed',
    { tag },
    async ({ page, builder, issues }) => {
      const draft = await newDraftIn(page, builder, scheme);
      // The landing's theme button shows the theme chosen in the editor.
      await chooseTheme(page, scheme);

      await test.step('drafts landing', async () => {
        await builder.backToDrafts();
        const open = page.getByTestId(`draft-open-${draft.id}`);
        // Focus goes from Back to drafts to the card of the draft just closed.
        await expect.soft(open).toBeFocused();
        await expect
          .soft(open)
          .toHaveAccessibleName(
            /^Open Untitled topology( \d+)?, updated \S.*\d/,
          );
        await expect
          .soft(page.getByRole('heading', { level: 1 }))
          .toHaveText('Builder');
        await expect.soft(page).toHaveTitle('Builder - phēnix');
        // Import comes before Upload, each named as its test id says, then
        // the editor header's buttons: Commands, with its key caps, the
        // theme, Settings, Help, a link to the documentation, and Focus
        // mode, an icon. They look as the editor's do. The theme is
        // the one chosen in the editor above.
        const theme = scheme === 'dark' ? 'Dark' : 'Light';
        const actions = page.locator('.builder-drafts__header > div > *');
        await expect
          .soft(actions)
          .toHaveText([
            'Blank diagram',
            'Import',
            'Upload',
            process.platform === 'darwin' ? 'Commands ⌘K' : 'Commands Ctrl+K',
            theme,
            'Settings',
            'Help (opens in a new tab)',
            'Focus mode',
          ]);
        for (const id of ['commands', 'theme', 'settings', 'focus-mode']) {
          await expect
            .soft(page.getByTestId(`drafts-${id}`))
            .toHaveClass(/\bbuilder-header__button\b/);
        }
        await expect
          .soft(page.getByTestId('drafts-theme'))
          .toHaveAccessibleName(`Theme: ${theme}. Switch to System theme.`);
        await expectReadable(page.getByTestId('drafts-theme'), 'drafts theme');
        await expect
          .soft(page.getByTestId('drafts-import'))
          .toHaveText('Import');
        await expect
          .soft(page.getByTestId('drafts-upload'))
          .toHaveText('Upload');
        for (const opener of [
          'drafts-import',
          'drafts-upload',
          'drafts-commands',
          'drafts-settings',
        ]) {
          await expect
            .soft(page.getByTestId(opener))
            .toHaveAttribute('aria-haspopup', 'dialog');
        }
        const help = page.getByRole('link', {
          name: 'Help (opens in a new tab)',
        });
        await expect
          .soft(help)
          .toHaveAttribute(
            'href',
            'https://phenix.sceptre.dev/latest/builder/',
          );
        await expect.soft(help).toHaveAttribute('target', '_blank');
        await expect.soft(help).toHaveAttribute('rel', /\bnoopener\b/);
        await expectReadable(help, 'Help link');
        // Settings opens the Builder's settings here too, and gets focus
        // back from them.
        const settings = page.getByTestId('drafts-settings');
        await settings.focus();
        await page.keyboard.press('Enter');
        await expect.soft(page.getByTestId('settings-dialog')).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(settings).toBeFocused();
        // The skip link only exists where its target does.
        await expect
          .soft(page.getByRole('link', { name: 'Skip to diagram canvas' }))
          .toHaveCount(0);
        const nav = page.getByTestId('nav-builder');
        await expect.soft(nav).toHaveAccessibleName('Builder');
        await expectHeaderRing(page, nav, 'nav link');
        await expectHeaderRing(page, page.getByTestId('nav-logout'), 'Logout');
        await expectAccessible(page, {
          soft: true,
          label: `axe on drafts landing (${scheme})`,
        });
      });
      expectNoFatal(issues);
    },
  );

  // The same in either theme, so checked in the light one.
  if (scheme === 'light') {
    test(
      'drafts tabs follow the APG tabs pattern',
      { tag },
      async ({ page, builder, issues }) => {
        await openWithScheme(page, builder, scheme);

        await test.step('drafts tabs follow the APG tabs pattern', async () => {
          const tab = (id) => page.getByTestId(`drafts-tab-${id}`);

          // The last tab is Node Templates: other users' drafts have a
          // tab only while there are some, and without sign-in there are
          // none.
          await tab('mine').focus();
          await page.keyboard.press('End');
          await expect.soft(tab('templates')).toBeFocused();
          await expect
            .soft(tab('templates'))
            .toHaveAttribute('aria-selected', 'true');
          await page.keyboard.press('Home');
          await expect.soft(tab('mine')).toBeFocused();
          await expect
            .soft(tab('mine'))
            .toHaveAttribute('aria-selected', 'true');

          // The command palette opens on the landing too.
          await page.keyboard.press('ControlOrMeta+k');
          await expect.soft(page.getByTestId('commands-dialog')).toBeVisible();
          await page.keyboard.press('Escape');
          await expect.soft(tab('mine')).toBeFocused();

          // Choosing a tab changes no tab's text: bold selected text once
          // widened the tab and pushed the others along.
          const labels = () =>
            page.getByRole('tab').evaluateAll((tabs) =>
              tabs.map((tab) => {
                const range = document.createRange();
                range.selectNodeContents(tab.firstChild);
                const { fontSize, fontWeight } = getComputedStyle(tab);
                const { width, height } = tab.getBoundingClientRect();

                return [
                  tab.firstChild.textContent.trim(),
                  range.getBoundingClientRect().width.toFixed(1),
                  fontSize,
                  fontWeight,
                  height.toFixed(1),
                  width > 0,
                ].join(' ');
              }),
            );
          const before = await labels();
          await page.keyboard.press('ArrowRight');
          await expect
            .soft(tab('shared'))
            .toHaveAttribute('aria-selected', 'true');
          expect
            .soft(await labels(), 'tabs after choosing another')
            .toEqual(before);
          await page.keyboard.press('Home');

          // A panel with nothing focusable in it is a Tab stop itself.
          const unreachable = await page
            .getByRole('tabpanel', { includeHidden: true })
            .evaluateAll((panels) =>
              panels
                .filter(
                  (panel) =>
                    !panel.querySelector('button') && panel.tabIndex !== 0,
                )
                .map((panel) => panel.id),
            );
          expect.soft(unreachable, 'empty tab panels').toEqual([]);
        });
        expectNoFatal(issues);
      },
    );
  }

  test(
    'the Import dialog passes axe and keeps focus inside itself',
    { tag },
    async ({ page, builder, issues }) => {
      await openWithScheme(page, builder, scheme);

      await test.step('Import dialog', () =>
        scanDialog(
          page,
          builder,
          page.getByTestId('drafts-import'),
          `Import dialog (${scheme})`,
          'Import topology or experiment',
        ));
      expectNoFatal(issues);
    },
  );

  test(
    'the Import dialog passes axe with every option and a refused name',
    { tag },
    async ({ page, builder, issues }, testInfo) => {
      await openWithScheme(page, builder, scheme);

      // A topology that includes another shows every option of Import: the
      // "Included topologies" choice, the copy box and, once one of them
      // asks for it, the new name, here with what is wrong with it.
      await test.step('Import dialog, with its options and a refused name', async () => {
        const opener = page.getByTestId('drafts-import');
        const surface = `Import dialog with its options (${scheme})`;
        const included = uniqueName(testInfo, 'axe-inc');
        const including = uniqueName(testInfo, 'axe-root');
        const config = (name, spec) => ({
          apiVersion: 'phenix.sandia.gov/v1',
          kind: 'Topology',
          metadata: { name },
          spec,
        });
        await builder.seedConfig(config(included, { nodes: [] }));
        await builder.seedConfig(
          config(including, { nodes: [], includeTopologies: [included] }),
        );

        await opener.press('Enter');
        await expect(builder.dialog).toBeVisible();
        await builder.dialog.getByTestId('import-name').selectOption(including);
        await expect(
          builder.dialog.getByRole('group', { name: 'Included topologies' }),
        ).toBeVisible();
        await builder.dialog.getByTestId('import-copy').check();
        const name = builder.dialog.getByLabel('New topology name');
        await name.fill(included);
        await name.press('Enter');
        await expect(builder.dialog.getByTestId('import-error')).toHaveText(
          `A topology named ${included} already exists. Enter another name.`,
        );
        await expect.soft(name).toBeFocused();
        // The Import button changed color when a source was chosen: its
        // colors are measured once they have settled.
        await builder.dialog.evaluate((dialog) =>
          Promise.all(
            dialog
              .getAnimations({ subtree: true })
              .map((animation) => animation.finished),
          ),
        );
        await expectAccessible(page, {
          soft: true,
          label: `axe on ${surface}`,
        });
        await expectRadioGroups(builder.dialog, surface);
        expect
          .soft(await focusableBehindDialog(page), `${surface}: page behind`)
          .toEqual([]);

        // Combine takes the copy box away; what is left is scanned too.
        await builder.dialog
          .getByLabel('Combine into one new topology')
          .check();
        await expect(builder.dialog.getByTestId('import-copy')).toHaveCount(0);
        await expectAccessible(page, {
          soft: true,
          label: `axe on ${surface}, Combine chosen`,
        });

        await page.keyboard.press('Escape');
        await expect(builder.dialog).toHaveCount(0);
        await expect.soft(opener, `${surface} returns focus`).toBeFocused();
      });
      expectNoFatal(issues);
    },
  );

  test(
    'the Configs page’s Builder tag and the viewer’s Builder button pass axe',
    { tag },
    async ({ page, builder, issues }, testInfo) => {
      await openWithScheme(page, builder, scheme);

      // The Builder controls of the Configs page: the tag of a Builder
      // topology, which is a link, and the first button of its viewer.
      await test.step('Configs page, the Builder tag and the viewer button', async () => {
        const name = uniqueName(testInfo, 'axe-built');
        await publishTopology(builder.request, builder.tracker, name);
        await openConfigs(page);

        const tag = page.locator(`[data-config-builder="Topology/${name}"]`);
        await expect(tag).toBeVisible();
        await expect
          .soft(tag)
          .toHaveAccessibleName(
            `builder: open Topology ${name} in the Builder`,
          );
        // Keyboard focus shows on the tag, in a ring that stands out from
        // the row behind it (WCAG 1.4.11).
        await tag.focus();
        expect
          .soft(
            await tag.evaluate((link) => getComputedStyle(link).outlineStyle),
            'tag focus outline',
          )
          .toBe('solid');
        const [ring] = await contrast(tag, 'outlineColor');
        expect
          .soft(ring.ratio, `tag focus ring: ${JSON.stringify(ring)}`)
          .toBeGreaterThanOrEqual(3);
        await expectAccessible(page, {
          include: `tr:has([data-config-builder="Topology/${name}"])`,
          soft: true,
          label: 'axe on the Configs row of a Builder topology',
        });

        const fetched = waitForApi(page, 'GET', `/configs/Topology/${name}`);
        await page
          .getByRole('button', { name: `View Topology ${name}` })
          .click();
        await fetched;
        const viewer = page.getByRole('dialog', { name: `Topology/${name}` });
        await expect(viewer.getByTestId('viewer-builder')).toHaveText(
          'Open in Builder',
        );
        // The viewer fades in: its colors are measured once it has.
        await expect
          .poll(() =>
            viewer.evaluate((modal) => {
              let opacity = 1;

              for (
                let element = modal.querySelector('.modal-card');
                element;
                element = element.parentElement
              ) {
                opacity *= parseFloat(getComputedStyle(element).opacity);
              }

              return opacity;
            }),
          )
          .toBe(1);
        // The button alone: the viewer's other buttons are not the
        // Builder's, and white on their colors is below 4.5:1.
        await expectAccessible(page, {
          include: '[data-testid="viewer-builder"]',
          soft: true,
          label: 'axe on the Builder button of the Configs viewer',
        });
        await viewer.getByRole('button', { name: 'Exit' }).click();
        await expect(viewer).toBeHidden();
      });
      expectNoFatal(issues);
    },
  );
}

for (const scheme of ['light', 'dark']) {
  test.describe(`axe scans in the ${scheme} theme`, () => axeScans(scheme));
}

// A device, a switch and a group whose icons are Medium or Large lay their
// text out beside the icon: a diagram drawn at `iconSize`, with a second
// group at the least size a group can be resized to (120 by 80) and the
// Inspector's Icon size in view, is scanned in both themes.
function iconSizeDocument(name, iconSize) {
  const id = () => crypto.randomUUID();
  const network = { id: id(), name: 'EXP' };
  const handle = { id: id(), name: 'eth0', index: 0 };
  const device = {
    id: id(),
    kind: 'device',
    label: 'web-01',
    position: { x: 64, y: 96 },
    device: {
      hostname: 'web-01',
      iconKey: 'server',
      spec: {
        type: 'VirtualMachine',
        general: {
          hostname: 'web-01',
          vm_type: 'kvm',
          description: 'Front end web server',
        },
        hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
        network: {
          interfaces: [
            { name: 'eth0', type: 'ethernet', proto: 'dhcp', vlan: 'EXP' },
          ],
        },
      },
      interfaces: [handle],
    },
  };
  const sw = {
    id: id(),
    kind: 'switch',
    label: 'EXP',
    position: { x: 352, y: 112 },
    switch: { networkId: network.id },
  };

  return {
    ...blankDocument(name, {
      nodes: [
        device,
        sw,
        {
          id: id(),
          kind: 'group',
          label: 'Zone',
          position: { x: 640, y: 64 },
          size: { width: 320, height: 240 },
          group: { title: 'Zone', description: 'Web tier' },
        },
        {
          id: id(),
          kind: 'group',
          label: 'Edge',
          position: { x: 640, y: 352 },
          size: { width: 120, height: 80 },
          group: { title: 'Edge' },
        },
      ],
      networks: [network],
      edges: [
        {
          id: id(),
          sourceNodeId: device.id,
          sourceHandleId: handle.id,
          targetNodeId: sw.id,
          networkId: network.id,
        },
      ],
    }),
    iconSize,
  };
}

test(
  'axe finds no serious violations on canvases whose icons are Medium or Large',
  { tag: ['@axe'] },
  async ({ page, builder, issues }, testInfo) => {
    // Two drafts, each opened in both themes.
    test.slow();

    for (const size of ['large', 'medium']) {
      const title = size === 'large' ? 'Large' : 'Medium';
      const name = `${size}-icons-${testInfo.project.name}-${Date.now()}`;
      const draft = await builder.seedDraft(iconSizeDocument(name, size));
      const sized = new RegExp(`builder-node--icon-${size}`);

      for (const scheme of ['light', 'dark']) {
        await test.step(`${title} icons, the ${scheme} theme`, async () => {
          await page.emulateMedia({ colorScheme: scheme });
          await builder.openDraft(draft);

          const select = builder.inspector.getByTestId(
            'inspector-icon-size-select',
          );

          await expect(select).toHaveValue(size);
          await expect
            .soft(
              builder.nodes().locator('.builder-node__header > .builder-icon'),
            )
            .toHaveCount(4);
          for (const kind of ['device', 'switch', 'group']) {
            await expect.soft(builder.nodes(kind).first()).toHaveClass(sized);
          }
          // The group of the least size lays its text out beside the icon
          // too.
          await expect.soft(builder.node('Edge', 'group')).toHaveClass(sized);

          // Keyboard: the select takes focus, and is named and described.
          await select.focus();
          await expect.soft(select).toBeFocused();
          await expect.soft(select).toHaveAccessibleName('Icon size');
          await expect
            .soft(select)
            .toHaveAccessibleDescription(
              'Devices, switches and groups draw their icons at this size, unless one has a size of its own.',
            );

          await expectAccessible(page, {
            soft: true,
            label: `axe on a canvas of ${title} icons, ${scheme} theme`,
          });
        });
      }
    }

    expectNoFatal(issues);
  },
);

// --- themes, minimap and zoom -------------------------------------------------

test.describe('themes and canvas controls', () => {
  // From System the toggle goes to the opposite of the OS scheme first, so
  // the first press always changes the colors. Its name and tooltip say
  // what the next press does.
  test('theme toggle goes System, Dark, Light on a light OS, says what is next and persists the choice, in the editor and on the drafts', async ({
    page,
    builder,
    issues,
  }) => {
    await openWithScheme(page, builder, 'light');
    const draft = await builder.createBlank();
    const toggle = page.getByTestId('editor-theme');
    const tooltip = page.getByTestId('header-tooltip');
    const stored = () =>
      page.evaluate((key) => localStorage.getItem(key), THEME_KEY);

    await expect(toggle).toHaveAttribute(
      'aria-label',
      'Theme: System. Switch to Dark theme.',
    );
    await expect(toggle).toContainText('System');
    await expect(root(page)).toHaveAttribute(
      'data-builder-theme-preference',
      'system',
    );
    // An icon in a 1440px window, so its tooltip names it too.
    await page.setViewportSize({ width: 1440, height: 900 });
    await toggle.focus();
    await expect
      .soft(tooltip)
      .toHaveText('Theme: System. Switch to Dark theme');

    const steps = [
      { theme: 'dark', label: 'Dark', resolved: 'dark', next: 'Light' },
      { theme: 'light', label: 'Light', resolved: 'light', next: 'System' },
      { theme: 'system', label: 'System', resolved: 'light', next: 'Dark' },
      { theme: 'dark', label: 'Dark', resolved: 'dark', next: 'Light' },
    ];
    for (const { theme, label, resolved, next } of steps) {
      await toggle.press('Enter');
      await expect(toggle).toHaveAttribute(
        'aria-label',
        `Theme: ${label}. Switch to ${next} theme.`,
      );
      await expect(toggle).toContainText(label);
      // The tooltip stays up after a key press, and follows the button.
      await expect
        .soft(tooltip)
        .toHaveText(`Theme: ${label}. Switch to ${next} theme`);
      await expect(root(page)).toHaveAttribute(
        'data-builder-theme-preference',
        theme,
      );
      await expect(root(page)).toHaveAttribute('data-builder-theme', resolved);
      await expect(builder).toHaveAnnounced(`Theme set to ${theme}.`);
      expect(await stored()).toBe(theme);
    }

    await page.reload();
    await expect(
      page.getByRole('heading', { name: 'Builder', exact: true }),
    ).toBeVisible({ timeout: 20000 });
    await expect(root(page)).toHaveAttribute('data-builder-theme', 'dark');
    await expect(root(page)).toHaveAttribute(
      'data-builder-theme-preference',
      'dark',
    );

    // The drafts have the same button, which goes the same way.
    const drafts = page.getByTestId('drafts-theme');
    await expect
      .soft(drafts)
      .toHaveAttribute('aria-label', 'Theme: Dark. Switch to Light theme.');
    await drafts.press('Enter');
    await expect(root(page)).toHaveAttribute('data-builder-theme', 'light');
    await expect
      .soft(drafts)
      .toHaveAttribute('aria-label', 'Theme: Light. Switch to System theme.');
    await expect.soft(drafts).toBeFocused();
    await expect
      .soft(tooltip)
      .toHaveText('Theme: Light. Switch to System theme');
    await expect.soft(builder).toHaveAnnounced('Theme set to light.');
    expect(await stored()).toBe('light');
    await chooseTheme(page, 'dark', { view: 'drafts' });

    await page.getByTestId(`draft-open-${draft.id}`).press('Enter');
    await expect(builder.canvas).toBeVisible();
    // The heading that takes focus is visually hidden, so the header it
    // titles shows the focus indicator instead (WCAG 2.4.7).
    await expect.soft(page.getByRole('heading', { level: 1 })).toBeFocused();
    await expect
      .soft(page.locator('.builder-header'), 'focus indicator')
      .toHaveCSS('outline-style', 'solid');
    await expect(toggle).toHaveAttribute(
      'aria-label',
      'Theme: Dark. Switch to Light theme.',
    );
    await expect(root(page)).toHaveAttribute('data-builder-theme', 'dark');

    // The Settings dialog's Theme is the same preference.
    const settings = page.getByTestId('editor-settings');
    const dialog = page.getByTestId('settings-dialog');
    await settings.press('Enter');
    await expect(dialog.getByRole('radio', { name: 'Dark' })).toBeChecked();
    await dialog.getByRole('radio', { name: 'Light' }).check();
    await expect(root(page)).toHaveAttribute('data-builder-theme', 'light');
    await expect(toggle).toHaveAttribute(
      'aria-label',
      'Theme: Light. Switch to System theme.',
    );
    expect(await stored()).toBe('light');
    await page.keyboard.press('Escape');
    await expect(settings).toBeFocused();
    expectNoFatal(issues);
  });

  test('System theme follows the OS color scheme, explicit themes do not, and the toggle goes by it', async ({
    page,
    builder,
    issues,
  }) => {
    // An invalid stored preference falls back to System.
    await page.addInitScript((key) => {
      localStorage.setItem(key, 'neon');
    }, THEME_KEY);
    await openWithScheme(page, builder, 'dark');
    await expect
      .soft(root(page), 'an invalid stored preference falls back to System')
      .toHaveAttribute('data-builder-theme-preference', 'system');
    await builder.createBlank();

    // On a dark OS the toggle goes System, Light, Dark, System.
    const toggle = page.getByTestId('editor-theme');
    const theme = () => expect.soft(root(page));
    for (const [preference, resolved, next] of [
      ['light', 'light', 'Dark'],
      ['dark', 'dark', 'System'],
      ['system', 'dark', 'Light'],
    ]) {
      await toggle.press('Enter');
      await theme().toHaveAttribute(
        'data-builder-theme-preference',
        preference,
      );
      await theme().toHaveAttribute('data-builder-theme', resolved);
      await expect
        .soft(toggle)
        .toHaveAttribute('aria-label', new RegExp(`Switch to ${next} theme.$`));
    }

    // System follows each OS change, and so does the toggle's next theme.
    await page.emulateMedia({ colorScheme: 'light' });
    await theme().toHaveAttribute('data-builder-theme', 'light');
    await expect
      .soft(toggle)
      .toHaveAttribute('aria-label', 'Theme: System. Switch to Dark theme.');
    await page.emulateMedia({ colorScheme: 'dark' });
    await theme().toHaveAttribute('data-builder-theme', 'dark');
    await page.emulateMedia({ colorScheme: 'light' });
    await theme().toHaveAttribute('data-builder-theme', 'light');

    // An explicit choice ignores it; the toggle still goes by it.
    await chooseTheme(page, 'light');
    await page.emulateMedia({ colorScheme: 'dark' });
    await afterMediaChange(page);
    await theme().toHaveAttribute('data-builder-theme', 'light');
    await expect
      .soft(toggle)
      .toHaveAttribute('aria-label', 'Theme: Light. Switch to Dark theme.');

    await chooseTheme(page, 'dark');
    await page.emulateMedia({ colorScheme: 'light' });
    await afterMediaChange(page);
    await theme().toHaveAttribute('data-builder-theme', 'dark');
    expectNoFatal(issues);
  });

  // One draft is measured in the light theme, then in the dark one, whose
  // text, inputs and buttons get checks of their own after that.
  test(
    'zoom controls and their tooltips, minimap, form fields and text are legible in the light and dark themes',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      // Reduced motion turns off color transitions, so the switch to the
      // dark theme below is measured at its final colors.
      await page.emulateMedia({
        colorScheme: 'light',
        reducedMotion: 'reduce',
      });
      await builder.open();
      await builder.createBlank();
      await addWithKeyboard(builder, 'device', 1);
      // The Inspector's form holds the device's text fields.
      await builder.selectInOutline('node');
      await expect(page.locator('.builder-inspector__subject')).toContainText(
        'Device node',
      );

      for (const theme of ['light', 'dark']) {
        await test.step(`${theme} theme`, async () => {
          // The Builder follows the system theme until one is chosen.
          await page.emulateMedia({
            colorScheme: theme,
            reducedMotion: 'reduce',
          });
          // A wrong theme is reported, and only this theme's measurements
          // are skipped.
          const errors = test.info().errors.length;
          await expect
            .soft(root(page))
            .toHaveAttribute('data-builder-theme', theme);
          if (test.info().errors.length > errors) {
            return;
          }

          // The +, - and fit glyphs are readable text on their buttons.
          const buttons = page.locator('.vue-flow__controls-button');
          await expect.soft(buttons).toHaveCount(3);
          for (const [index, { ratio }] of (
            await contrast(buttons)
          ).entries()) {
            expect
              .soft(ratio, `${theme}: zoom control ${index} text`)
              .toBeGreaterThanOrEqual(4.5);
          }

          // Each shows its name, and the keys that do the same on the
          // canvas, in a readable tooltip on hover, never in a title
          // attribute (WCAG 1.4.13). Those keys do nothing on the buttons,
          // so the buttons do not claim them in aria-keyshortcuts.
          const tip = page.getByTestId('zoom-tooltip');
          for (const [index, name] of [
            'Zoom in (= or + on the canvas)',
            'Zoom out (− on the canvas)',
            `Fit diagram to view (${process.platform === 'darwin' ? '⇧1' : 'Shift+1'} on the canvas)`,
          ].entries()) {
            await expect.soft(buttons.nth(index)).not.toHaveAttribute('title');
            await expect
              .soft(buttons.nth(index))
              .not.toHaveAttribute('aria-keyshortcuts');
            await buttons.nth(index).hover();
            await expect.soft(tip).toHaveText(name);
            const [{ ratio }] = await contrast(tip);
            expect
              .soft(ratio, `${theme}: ${name} tooltip text`)
              .toBeGreaterThanOrEqual(4.5);
          }
          await page.mouse.move(700, 400);
          await expect.soft(tip).toHaveCount(0);

          // The controls and the minimap have a frame that stands out from
          // the canvas (WCAG 1.4.11: 3:1 for UI component boundaries).
          for (const frame of ['.vue-flow__controls', '.vue-flow__minimap']) {
            const [{ ratio }] = await contrast(
              page.locator(frame),
              'borderTopColor',
            );
            expect
              .soft(ratio, `${theme}: ${frame} border`)
              .toBeGreaterThanOrEqual(3);
          }

          // The outline of the visible area inside the minimap stands out
          // from the minimap surface.
          const mask = await minimapMask(page);
          expect
            .soft(mask.width, `${theme}: minimap mask stroke width`)
            .toBeGreaterThanOrEqual(1.5);
          expect
            .soft(mask.ratio, `${theme}: minimap mask stroke contrast`)
            .toBeGreaterThanOrEqual(3);

          if (theme === 'dark') {
            // No bright white boxes on the dark canvas.
            expect
              .soft(mask.surface, 'dark: minimap surface')
              .not.toBe('rgb(255, 255, 255)');
          }

          // The name's pencil is an icon alone, which stands out as its
          // own boundary would (WCAG 1.4.11).
          const edit = page.getByTestId('builder-name-edit');
          const [{ ratio: pencil }] = await contrast(edit);
          expect
            .soft(pencil, `${theme}: Edit diagram name icon`)
            .toBeGreaterThanOrEqual(3);

          // Fields share their panel's fill, so their border is what shows
          // where they are (WCAG 1.4.11). The name's field shows while the
          // name is edited.
          await edit.click();
          for (const field of [
            '#builder-doc-name',
            '.builder-inspector input[type="text"]',
          ]) {
            const locator = page.locator(field).first();
            await expect.soft(locator, `${theme}: ${field}`).toBeVisible();
            const [{ ratio }] = await contrast(locator, 'borderTopColor');
            expect
              .soft(ratio, `${theme}: ${field} border`)
              .toBeGreaterThanOrEqual(3);
          }
          await page.locator('#builder-doc-name').press('Escape');
          await expect.soft(edit).toBeFocused();

          // A select, in the dialog the toolbar's Add connection opens.
          const opener = builder.toolbar('connect');
          await opener.press('Enter');
          const select = page.locator('#connect-device');
          await expect.soft(select, `${theme}: #connect-device`).toBeVisible();
          const [{ ratio: selectBorder }] = await contrast(
            select,
            'borderTopColor',
          );
          expect
            .soft(selectBorder, `${theme}: #connect-device border`)
            .toBeGreaterThanOrEqual(3);
          await page.keyboard.press('Escape');
          await expect(page.getByTestId('connect-dialog')).toHaveCount(0);
          await expect.soft(opener).toBeFocused();

          // The Inspector's heading is smaller and dimmer than what it
          // heads, and still readable.
          const heading = page.locator('#inspector-title');
          await expectReadable(heading, `${theme}: Inspector heading`);
          expect
            .soft(
              await heading.evaluate((element) => {
                const style = getComputedStyle(element);
                const rem = parseFloat(
                  getComputedStyle(document.documentElement).fontSize,
                );
                const panel = getComputedStyle(
                  element.closest('.builder-inspector') || document.body,
                );

                return {
                  rem: parseFloat(style.fontSize) / rem,
                  weight: style.fontWeight,
                  muted: style.color !== panel.color,
                };
              }),
              `${theme}: Inspector heading style`,
            )
            .toEqual({ rem: 0.75, weight: '600', muted: true });
        });
      }

      await test.step('keyboard focus shows a zoom tooltip too, and Escape dismisses it', async () => {
        const tip = page.getByTestId('zoom-tooltip');
        const fit = page.getByRole('button', { name: 'Fit diagram to view' });

        await fit.focus();
        await expect
          .soft(tip)
          .toHaveText(/^Fit diagram to view \(.+1 on the canvas\)$/);
        await page.keyboard.press('Escape');
        await expect.soft(tip).toHaveCount(0);
        await expect.soft(fit).toBeFocused();
      });

      // The dark theme is on from here.
      await test.step('dark: no text is colored like its background', async () => {
        const found = await invisibleText(page);
        expect
          .soft(found, `invisible text: ${JSON.stringify(found, null, 2)}`)
          .toEqual([]);
      });

      await test.step('dark: the minimap uses a dark surface', async () => {
        const minimap = page.getByRole('img', { name: 'Diagram minimap' });
        await expect.soft(minimap).toBeVisible();
        // The theme's text color must stay readable on the minimap background.
        await expectReadable(minimap, 'minimap');
      });

      await test.step('dark: outline rename input', async () => {
        await row(builder, 'node').focus();
        await page.keyboard.press('F2');
        const input = page.getByLabel('Rename node');
        await expect.soft(input).toBeFocused();
        await expectReadable(input, 'rename input');
        await page.keyboard.press('Escape');
        // No rename input may stay open while the next step edits.
        await expect(input).toHaveCount(0);
      });

      await test.step('dark: a node the diagram checks flag is marked, and says why', async () => {
        // A device on no network yet: a warning, drawn as a triangle.
        const device = page.locator('.vue-flow__node-builderDevice');

        await expect
          .soft(device.getByTestId('node-issue'))
          .toHaveAttribute('data-level', 'warning');
        // After what its info tooltip shows, and before the canvas's keys.
        await expect
          .soft(device)
          .toHaveAccessibleDescription(
            /^No interfaces\. OS type linux\. 1 warning: device "node" has no interfaces\. Arrow keys move between nodes/,
          );
      });

      await test.step('dark: JSON Forms array buttons', async () => {
        // A connection gives the Interfaces array an item with its own toolbar.
        await addWithKeyboard(builder, 'switch', 2);
        await connectWithKeyboard(builder);
        await builder.expectSummary('1 connection');
        // Connected, the device has nothing to mark.
        await expect.soft(page.getByTestId('node-issue')).toHaveCount(0);
        await builder.selectInOutline('node');
        await expect
          .soft(
            builder.inspector
              .locator('.array-list-item-toolbar button')
              .first(),
          )
          .toBeVisible();

        await expectReadable(
          builder.inspector.locator(
            'button.array-list-add:not(:disabled), .array-list-item-toolbar button:not(:disabled)',
          ),
          'array buttons',
        );
      });

      // Forced colors draw a box's background in the system's color; the
      // switch's color swatch keeps its own.
      if (page.context().browser()?.browserType().name() === 'chromium') {
        await test.step('forced colors keep a switch’s color swatch', async () => {
          const swatch = page.locator('.builder-node__swatch');
          const color = () =>
            swatch.evaluate(
              (element) => getComputedStyle(element).backgroundColor,
            );
          const shown = await color();

          await page.emulateMedia({ forcedColors: 'active' });
          await expect
            .poll(() =>
              page.evaluate(
                () => matchMedia('(forced-colors: active)').matches,
              ),
            )
            .toBe(true);
          expect.soft(await color()).toBe(shown);
          await page.emulateMedia({ forcedColors: 'none' });
        });
      }
      expectNoFatal(issues);
    },
  );

  test('minimap toggle, and zoom in, zoom out and fit by the controls and on the canvas', async ({
    page,
    builder,
    issues,
  }) => {
    await sixDevices(builder);

    await test.step('Minimap toggle reports its state and shows or hides the minimap', async () => {
      const toggle = page.getByTestId('toolbar-minimap');
      const minimap = page.getByRole('img', { name: 'Diagram minimap' });

      // The zoom step below does not depend on the minimap's state.
      await expect.soft(toggle).toHaveAttribute('aria-pressed', 'true');
      await expect.soft(minimap).toBeVisible();

      await toggle.press('Enter');
      await expect.soft(toggle).toHaveAttribute('aria-pressed', 'false');
      await expect.soft(minimap).toBeHidden();

      await toggle.press(' ');
      await expect.soft(toggle).toHaveAttribute('aria-pressed', 'true');
      await expect.soft(minimap).toBeVisible();
    });

    await test.step('zoom in, zoom out and fit change the viewport', async () => {
      const controls = page.getByRole('group', {
        name: 'Canvas zoom controls',
      });
      const zoomIn = controls.getByRole('button', { name: 'Zoom in' });
      const zoomOut = controls.getByRole('button', { name: 'Zoom out' });
      const fit = controls.getByRole('button', { name: 'Fit diagram to view' });

      // Each control step scales by 1.2; wait for the exact level so a
      // transition in progress is never mistaken for the result.
      const start = await zoomLevel(page);
      const level = (value) => Math.round(value * 1000) / 1000;
      await zoomIn.press('Enter');
      await expect
        .poll(async () => level(await zoomLevel(page)))
        .toBe(level(start * 1.2));
      await zoomIn.press('Enter');
      await expect
        .poll(async () => level(await zoomLevel(page)))
        .toBe(level(start * 1.44));
      await zoomOut.press('Enter');
      await expect
        .poll(async () => level(await zoomLevel(page)))
        .toBe(level(start * 1.2));

      // Zoomed in, the row of devices runs past the canvas edge; Fit brings
      // every node back into view.
      await zoomIn.press('Enter');
      await zoomIn.press('Enter');
      await expect.poll(() => nodesOutsideCanvas(page)).not.toEqual([]);

      // An outline row whose node is out of view brings it into view, at the
      // same zoom, and keeps focus.
      const [outside] = await nodesOutsideCanvas(page);
      const zoomed = await zoomLevel(page);
      const outsideRow = page.getByTestId(`outline-item-${outside}`);
      await outsideRow.press('Enter');
      await expect.poll(() => nodesOutsideCanvas(page)).not.toContain(outside);
      await expect.soft(outsideRow).toBeFocused();
      expect.soft(await zoomLevel(page)).toBe(zoomed);

      const before = await settledView(page);
      await fit.press('Enter');
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      await expect.poll(() => nodesUnderOverlays(page)).toEqual([]);
      // It says so, and what a second press does, which ⇧1 on the canvas
      // does not show.
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Fitted the diagram to the view. A second press restores the previous view.',
        );

      // Fit then goes back to the view from before it, and says so; the
      // button's name, icon and tooltip follow. Pressed again, it fits.
      const restore = controls.getByRole('button', {
        name: 'Restore previous view',
      });
      const tip = page.getByTestId('zoom-tooltip');
      const icon = (name) =>
        controls.locator(`.vue-flow__controls-fitview .builder-icon--${name}`);
      await expect.soft(restore).toBeFocused();
      await expect.soft(icon('fit-view-restore')).toHaveCount(1);
      await expect
        .soft(tip)
        .toHaveText(/^Restore previous view \(.+1 on the canvas\)$/);
      const fitted = await settledView(page);
      await restore.press('Enter');
      expect(await settledView(page, fitted), 'the view from before Fit').toBe(
        before,
      );
      await expect.soft(builder).toHaveAnnounced('Restored the previous view');
      await expect.soft(fit).toBeFocused();
      await expect.soft(icon('fit-view')).toHaveCount(1);
      await expect.soft(tip).toHaveText(/^Fit diagram to view /);
      await fit.press('Enter');
      expect(await settledView(page, before), 'fitted again').toBe(fitted);
      await expect.soft(restore).toBeFocused();

      // Any other change of the view forgets the view from before Fit: a
      // pan, here by dragging the empty canvas, and the button fits again.
      const pane = await page.locator('.vue-flow__pane').boundingBox();
      await page.mouse.move(pane.x + 12, pane.y + 12);
      await page.mouse.down();
      await page.mouse.move(pane.x + 72, pane.y + 52, { steps: 4 });
      await page.mouse.up();
      await expect(fit).toBeVisible();
      await expect(restore).toHaveCount(0);
      const panned = await settledView(page);
      expect(panned, 'the pan moved the view').not.toBe(fitted);
      await fit.press('Enter');
      expect(await settledView(page, panned), 'fitted after the pan').toBe(
        fitted,
      );

      // The last device is the canvas's Tab stop, once it has had focus.
      const last = page.locator('.vue-flow__node').last();
      const lastId = await last.getAttribute('data-id');
      await last.focus();

      // At its limit a zoom button stays focusable and reports that it is
      // unavailable, so focus never falls to the page.
      await zoomIn.focus();
      for (let press = 0; press < 10; press += 1) {
        await page.keyboard.press('Enter');
      }
      await expect.soft(zoomIn).toHaveAttribute('aria-disabled', 'true');
      await expect.soft(zoomIn).toBeFocused();

      // Zoomed in, the last device is off screen. Shift+Tab from the zoom
      // controls focuses it, and the canvas pans it into view (WCAG 2.4.11).
      await page.keyboard.press('Shift+Tab');
      const focusedId = await page.evaluate(
        () => document.activeElement?.closest('.vue-flow__node')?.dataset.id,
      );
      expect(focusedId, 'Shift+Tab reaches the last device').toBe(lastId);
      await expect
        .poll(() => nodesOutsideCanvas(page))
        .not.toContain(focusedId);
    });

    await test.step('on the canvas, Shift+1 fits, − zooms out and = or + zooms in', async () => {
      const level = (value) => Math.round(value * 1000) / 1000;

      // Focus is on a node, zoomed in as far as it goes. Shift+1 fits, then
      // goes back to that zoom, as the Fit button does, and fits again.
      await page.keyboard.press('Shift+Digit1');
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      const fitted = await zoomLevel(page);
      await expect
        .soft(page.getByRole('button', { name: 'Restore previous view' }))
        .toHaveCount(1);
      await page.keyboard.press('Shift+Digit1');
      await expect.poll(async () => level(await zoomLevel(page))).toBe(2);
      await page.keyboard.press('Shift+Digit1');
      await expect.poll(() => zoomLevel(page)).toBe(fitted);
      await page.keyboard.press('-');
      await expect
        .poll(async () => level(await zoomLevel(page)))
        .toBe(level(fitted / 1.2));
      await page.keyboard.press('=');
      await expect
        .poll(async () => level(await zoomLevel(page)))
        .toBe(level(fitted));
      await page.keyboard.press('+');
      await expect
        .poll(async () => level(await zoomLevel(page)))
        .toBe(level(fitted * 1.2));
    });
    expectNoFatal(issues);
  });

  test('Settings choose the minimap, the zoom a diagram opens with and reduced motion, and keep only those choices', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await sixDevices(builder);
    // Zoomed in, the row of devices runs past the canvas edge.
    const zoomIn = page.getByRole('button', { name: 'Zoom in' });
    for (let press = 0; press < 4; press += 1) {
      await zoomIn.press('Enter');
    }
    await expect.poll(() => nodesOutsideCanvas(page)).not.toEqual([]);

    await test.step('Settings choose the minimap, the zoom a diagram opens with and reduced motion, and keep only those choices', async () => {
      const opener = page.getByTestId('editor-settings');
      const settings = page.getByTestId('settings-dialog');
      const layout = settings.getByRole('combobox', {
        name: 'Default layout',
      });
      const showMinimap = settings.getByRole('switch', {
        name: 'Show the minimap',
      });
      // The page behind the dialog is inert, so not by role.
      const minimap = page.locator('.vue-flow__minimap');
      const level = (value) => Math.round(value * 1000) / 1000;
      const stored = () =>
        page.evaluate(() => localStorage.getItem('phenix.builder.settings'));

      await opener.press('Enter');
      await expect.soft(settings).toBeFocused();
      await expect.soft(layout).toHaveValue('elk');
      await layout.selectOption({ label: 'Dagre' });
      await showMinimap.click();
      await expect.soft(showMinimap).toHaveAttribute('aria-checked', 'false');
      await expect.soft(minimap).toHaveCount(0);
      await settings
        .getByRole('radio', { name: 'Fit the whole diagram in view' })
        .check();
      await settings.getByRole('switch', { name: 'Reduce motion' }).click();
      await expect
        .soft(root(page))
        .toHaveAttribute('data-builder-reduced-motion', 'true');
      // Nothing moves then, not even a switch's knob, drawn by ::after.
      expect
        .soft(
          await showMinimap.evaluate(
            (element) =>
              getComputedStyle(
                element.querySelector('.builder-switch__track'),
                '::after',
              ).transitionDuration,
          ),
          'switch knob transition',
        )
        .toBe('0s');
      expect(JSON.parse(await stored())).toEqual({
        layoutAlgorithm: 'dagre',
        showMinimap: false,
        openZoom: 'fit',
        reduceMotion: true,
      });
      await page.keyboard.press('Escape');
      await expect.soft(opener).toBeFocused();
      // A draft with no layout of its own says Default whatever the
      // setting, which is only what a layout run uses.
      await expect
        .soft(page.getByTestId('toolbar-layout'))
        .toHaveAccessibleName('Default layout');

      // Zoomed in, Reset view fits the diagram, as Shift+1 does.
      await page.getByTestId('editor-reset-view').click();
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      const opened = level(await zoomLevel(page));

      // A double-click on the empty canvas zooms in at once too: the view
      // takes one new transform, not the frames of a transition.
      const pane = await page.locator('.vue-flow__pane').boundingBox();
      const transforms = page.evaluate(
        () =>
          new Promise((resolve) => {
            const view = document.querySelector(
              '.vue-flow__transformationpane',
            );
            const seen = new Set();
            const start = performance.now();
            const sample = () => {
              seen.add(view.style.transform);
              if (performance.now() - start < 500) {
                requestAnimationFrame(sample);
              } else {
                resolve(seen.size);
              }
            };
            requestAnimationFrame(sample);
          }),
      );
      await page.mouse.dblclick(pane.x + 12, pane.y + 12);
      expect
        .soft(await transforms, 'views drawn after a double-click')
        .toBeLessThanOrEqual(2);
      await expect
        .poll(async () => level(await zoomLevel(page)))
        .toBeGreaterThan(opened);
      await builder.canvas.focus();
      await page.keyboard.press('Shift+Digit1');
      await expect.poll(async () => level(await zoomLevel(page))).toBe(opened);
      await expect.soft(minimap).toHaveCount(0);

      // A new page opens the draft fitted, without the minimap.
      await builder.editAndReload(draft, {
        expectSaved: () => builder.waitSaved(),
        expectAfterReload: async () => {
          await expect
            .poll(async () => level(await zoomLevel(page)))
            .toBe(opened);
          await expect
            .soft(page.getByTestId('toolbar-minimap'))
            .toHaveAttribute('aria-pressed', 'false');
          await expect
            .soft(root(page))
            .toHaveAttribute('data-builder-reduced-motion', 'true');
        },
      });

      // The toolbar shows the minimap for this diagram. Another tab storing
      // the same settings, in another order, changes none of them and
      // leaves it shown.
      const toggle = page.getByTestId('toolbar-minimap');
      await toggle.click();
      await expect.soft(minimap).toHaveCount(1);
      await page.evaluate(() => {
        window.settingsHeard = new Promise((resolve) =>
          window.addEventListener('storage', (event) => {
            if (event.key === 'phenix.builder.settings') {
              resolve();
            }
          }),
        );
      });
      const other = await page.context().newPage();
      await other.goto(new URL('/builder', page.url()).href);
      await other.evaluate(() =>
        localStorage.setItem(
          'phenix.builder.settings',
          JSON.stringify({
            reduceMotion: true,
            openZoom: 'fit',
            showMinimap: false,
            layoutAlgorithm: 'dagre',
          }),
        ),
      );
      await page.evaluate(() => window.settingsHeard);
      await other.close();
      await page.evaluate(() => new Promise(requestAnimationFrame));
      await expect.soft(toggle).toHaveAttribute('aria-pressed', 'true');
      await expect.soft(minimap).toHaveCount(1);

      // Upload opens another diagram in the editor: fitted, and without the
      // minimap, as the settings say.
      await page.locator('.vue-flow__controls-zoomin').click();
      await expect
        .poll(async () => level(await zoomLevel(page)))
        .not.toBe(opened);
      const upload = await builder.openDialog('upload');
      await upload.getByLabel('Paste text', { exact: true }).check();
      await upload
        .getByTestId('upload-text')
        .fill(JSON.stringify(await builder.serverDocument(draft)));
      const created = waitForApi(page, 'POST', '/builder/drafts');
      await upload.getByTestId('upload-submit').click();
      const uploaded = await (await created).json();
      expect.soft(uploaded.id, 'a new draft').not.toBe(draft.id);
      await expect(builder.dialog).toBeHidden();
      await expect.poll(async () => level(await zoomLevel(page))).toBe(opened);
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      await expect.soft(toggle).toHaveAttribute('aria-pressed', 'false');
      await expect.soft(minimap).toHaveCount(0);
      await expect.soft(builder.toolbar('upload')).toBeFocused();
      await builder.waitSaved();

      // Reset to defaults puts every choice back, and forgets them.
      await opener.press('Enter');
      await expect.soft(layout).toHaveValue('dagre');
      const reset = settings.getByRole('button', { name: 'Reset to defaults' });
      await reset.click();
      await expect.soft(layout).toHaveValue('elk');
      await expect.soft(showMinimap).toHaveAttribute('aria-checked', 'true');
      await expect.soft(minimap).toHaveCount(1);
      await expect
        .soft(root(page))
        .toHaveAttribute('data-builder-reduced-motion', 'false');
      await expect
        .soft(settings.getByTestId('settings-status'))
        .toHaveText('Every setting is back to its default.');
      await expect.soft(reset).toBeFocused();
      await expect.soft(reset).toHaveAttribute('aria-disabled', 'true');
      expect(await stored()).toBeNull();
      await page.keyboard.press('Escape');
    });
    expectNoFatal(issues);
  });

  test('a custom zoom opens a diagram at a percentage, which Reset view goes back to', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await sixDevices(builder);

    await test.step('a custom zoom opens a diagram at a percentage, which Reset view goes back to', async () => {
      const opener = page.getByTestId('editor-settings');
      const settings = page.getByTestId('settings-dialog');
      const zooms = settings.locator('input[name="settings-zoom"]');
      const custom = settings.getByRole('radio', { name: 'Custom' });
      const percent = settings.getByRole('spinbutton', {
        name: 'Custom zoom, percent',
      });
      const refused = settings.getByTestId('settings-zoom-percent-error');
      const status = settings.getByTestId('settings-status');
      const level = (value) => Math.round(value * 1000) / 1000;
      const view = () =>
        page
          .locator('.vue-flow__transformationpane')
          .evaluate((element) => element.style.transform);
      const stored = async () =>
        JSON.parse(
          await page.evaluate(() =>
            localStorage.getItem('phenix.builder.settings'),
          ),
        );

      await opener.press('Enter');
      // 100%, the fit, then Custom with its percentage beside it: a field
      // that is always enabled, described by its range.
      await expect.soft(zooms).toHaveCount(3);
      expect
        .soft(
          await zooms.evaluateAll((radios) =>
            radios.map((radio) => [radio.value, radio.checked]),
          ),
        )
        .toEqual([
          ['actual', true],
          ['fit', false],
          ['custom', false],
        ]);
      await expect.soft(percent).toHaveValue('100');
      await expect.soft(percent).toBeEnabled();
      await expect.soft(percent).toHaveAttribute('min', '20');
      await expect.soft(percent).toHaveAttribute('max', '200');
      await expect.soft(percent).toHaveAttribute('step', '5');
      await expect
        .soft(percent)
        .toHaveAccessibleDescription(
          'From 20 to 200. Reset view goes back to it too.',
        );
      const beside = await Promise.all([
        custom.boundingBox(),
        percent.boundingBox(),
      ]);
      expect
        .soft(
          Math.abs(
            beside[0].y +
              beside[0].height / 2 -
              (beside[1].y + beside[1].height / 2),
          ),
          'the field is on the line of the Custom choice',
        )
        .toBeLessThan(12);

      // Out of range: nothing is kept, and the field says why.
      await percent.fill('500');
      await percent.press('Enter');
      await expect(refused).toHaveText('Enter a number from 20 to 200.');
      await expect.soft(percent).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(percent).toHaveValue('500');
      await expect
        .soft(percent)
        .toHaveAccessibleDescription(/Enter a number from 20 to 200\.$/);
      await expect.soft(custom).not.toBeChecked();
      expect(await stored()).toBeNull();
      await percent.fill('');
      await percent.blur();
      await expect(refused).toHaveText('Enter a number from 20 to 200.');
      expect(await stored()).toBeNull();

      // A number in range is rounded to a step of 5, kept, and chooses
      // Custom.
      await percent.fill('33');
      await percent.press('Enter');
      await expect(percent).toHaveValue('35');
      await expect.soft(refused).toHaveCount(0);
      await expect.soft(percent).not.toHaveAttribute('aria-invalid');
      await expect.soft(custom).toBeChecked();
      await expect.soft(status).toHaveText('Diagrams open at 35%.');
      expect(await stored()).toEqual({
        openZoom: 'custom',
        openZoomPercent: 35,
      });
      await percent.fill('75');
      await percent.blur();
      await expect.soft(status).toHaveText('Diagrams open at 75%.');
      expect(await stored()).toEqual({
        openZoom: 'custom',
        openZoomPercent: 75,
      });

      // Another choice keeps the percentage for the next time; Custom on
      // its own takes it up again, and drops a value the field refused.
      await settings.getByRole('radio', { name: '100%' }).check();
      expect(await stored()).toEqual({ openZoomPercent: 75 });
      await percent.fill('7');
      await percent.blur();
      await expect(refused).toHaveText('Enter a number from 20 to 200.');
      await custom.check();
      await expect.soft(percent).toHaveValue('75');
      await expect.soft(refused).toHaveCount(0);
      expect(await stored()).toEqual({
        openZoom: 'custom',
        openZoomPercent: 75,
      });
      await page.keyboard.press('Escape');
      await expect.soft(opener).toBeFocused();

      // Reset view goes to the percentage, from the diagram's origin.
      await page.getByTestId('editor-reset-view').click();
      await expect.poll(async () => level(await zoomLevel(page))).toBe(0.75);
      await expect.poll(view).toMatch(/^translate\(0px, 0px\) scale\(0\.75\)$/);

      // A new page opens the draft at it.
      await builder.editAndReload(draft, {
        expectSaved: () => builder.waitSaved(),
        expectAfterReload: async () => {
          await expect
            .poll(async () => level(await zoomLevel(page)))
            .toBe(0.75);
          await expect
            .poll(view)
            .toMatch(/^translate\(0px, 0px\) scale\(0\.75\)$/);
        },
      });

      // Reset to defaults puts both back.
      await opener.press('Enter');
      await expect.soft(custom).toBeChecked();
      await expect.soft(percent).toHaveValue('75');
      await settings.getByRole('button', { name: 'Reset to defaults' }).click();
      await expect
        .soft(settings.getByRole('radio', { name: '100%' }))
        .toBeChecked();
      await expect.soft(percent).toHaveValue('100');
      expect(await stored()).toBeNull();
      await page.keyboard.press('Escape');
      await page.getByTestId('editor-reset-view').click();
      await expect.poll(async () => level(await zoomLevel(page))).toBe(1);
    });
    expectNoFatal(issues);
  });

  test('a switch row brings its whole network into view, however far apart its nodes are', async ({
    page,
    builder,
    issues,
  }) => {
    await test.step('a switch row brings its whole network into view, however far apart its nodes are', async () => {
      const id = () => crypto.randomUUID();
      const network = { id: id(), name: 'tall-net' };
      const sw = {
        id: id(),
        kind: 'switch',
        label: 'tall-net',
        position: { x: 3000, y: 0 },
        switch: { networkId: network.id },
      };
      // Far below 0.2 zoom is needed to show the network whole.
      const devices = [0, 9000].map((y, index) => ({
        id: id(),
        kind: 'device',
        label: `far-${index}`,
        position: { x: 0, y },
        device: {
          hostname: `far-${index}`,
          spec: {
            type: 'VirtualMachine',
            general: { hostname: `far-${index}`, vm_type: 'kvm' },
            hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
            network: {
              interfaces: [
                {
                  name: 'eth0',
                  proto: 'dhcp',
                  type: 'ethernet',
                  vlan: 'tall-net',
                },
              ],
            },
          },
          interfaces: [{ id: id(), name: 'eth0', index: 0 }],
        },
      }));
      const tall = await builder.seedDraft(
        blankDocument(`tall-${Date.now()}`, {
          nodes: [...devices, sw],
          networks: [network],
          edges: devices.map((device) => ({
            id: id(),
            sourceNodeId: device.id,
            sourceHandleId: device.device.interfaces[0].id,
            targetNodeId: sw.id,
            networkId: network.id,
          })),
        }),
      );
      await builder.openDraft(tall);

      const row = page.getByTestId(`outline-item-${sw.id}`);
      await expect.poll(() => nodesOutsideCanvas(page)).not.toEqual([]);
      await row.press('Enter');
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      await expect.soft(row).toBeFocused();
    });
    expectNoFatal(issues);
  });

  test('a large diagram is shown whole, clear of the minimap and zoom controls, by Fit, the fit opening zoom, Reset view, zooming out and its outline', async ({
    page,
    builder,
    issues,
  }) => {
    // An editor to measure the canvas in and to choose the opening zoom in.
    await builder.openDraft(
      await builder.seedDraft(blankDocument(`fit-${Date.now()}`)),
    );

    // Its corner devices sit where the zoom controls and the minimap float,
    // so each fit must keep them clear of both.
    await test.step('a large diagram is shown whole, clear of the minimap and zoom controls, by Fit, the fit opening zoom, Reset view, zooming out and its outline', async () => {
      const level = (value) => Math.round(value * 1000) / 1000;
      const id = () => crypto.randomUUID();
      await page.setViewportSize({ width: 1440, height: 900 });
      const pane = await page.locator('.vue-flow').boundingBox();
      const network = { id: id(), name: 'wide-net' };
      const sw = {
        id: id(),
        kind: 'switch',
        label: 'wide-net',
        position: { x: 3600, y: -300 },
        switch: { networkId: network.id },
      };
      // Four devices at the corners of a 7200 wide area, each connected to
      // the one switch: in a 1440 by 900 window, it takes a zoom of about
      // 0.1 to see them all. Few nodes keep the draft quick to open on a
      // slow machine; how far apart they are is what sets the zoom. The
      // diagram, switch and all, has the pane's shape, so a fit into the
      // whole pane would put the bottom corners under the zoom controls and
      // the minimap.
      const bottom = Math.round((7360 * pane.height) / pane.width) - 396;
      const corners = [
        { x: 0, y: 0 },
        { x: 7200, y: 0 },
        { x: 0, y: bottom },
        { x: 7200, y: bottom },
      ];
      const devices = corners.map((position, index) => {
        const hostname = `wide-${index + 1}`;

        return {
          id: id(),
          kind: 'device',
          label: hostname,
          position,
          device: {
            hostname,
            spec: {
              type: 'VirtualMachine',
              general: { hostname, vm_type: 'kvm' },
              hardware: {
                os_type: 'linux',
                drives: [{ image: 'ubuntu.qc2' }],
              },
              network: {
                interfaces: [
                  {
                    name: 'eth0',
                    proto: 'dhcp',
                    type: 'ethernet',
                    vlan: 'wide-net',
                  },
                ],
              },
            },
            interfaces: [{ id: id(), name: 'eth0', index: 0 }],
          },
        };
      });
      const wide = await builder.seedDraft(
        blankDocument(`wide-${Date.now()}`, {
          nodes: [...devices, sw],
          networks: [network],
          edges: devices.map((device) => ({
            id: id(),
            sourceNodeId: device.id,
            sourceHandleId: device.device.interfaces[0].id,
            targetNodeId: sw.id,
            networkId: network.id,
          })),
        }),
      );
      const controls = page.getByRole('group', {
        name: 'Canvas zoom controls',
      });
      const zoomIn = controls.getByRole('button', { name: 'Zoom in' });
      const zoomOut = controls.getByRole('button', { name: 'Zoom out' });
      const fit = controls.getByRole('button', { name: 'Fit diagram to view' });
      // Each press zooms in by 1.2.
      const zoomInBy = async (presses) => {
        const target = (await zoomLevel(page)) * 1.2 ** presses;

        for (let press = 0; press < presses; press += 1) {
          await zoomIn.press('Enter');
        }
        await expect.poll(() => zoomLevel(page)).toBeGreaterThan(target * 0.99);
      };

      // Opened with the zoom that fits it.
      await page.getByTestId('editor-settings').press('Enter');
      await page
        .getByTestId('settings-dialog')
        .getByRole('radio', { name: 'Fit the whole diagram in view' })
        .check();
      await page.keyboard.press('Escape');
      await builder.openDraft(wide);
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      await expect.poll(() => nodesUnderOverlays(page)).toEqual([]);
      const fitted = level(await zoomLevel(page));
      expect(fitted, 'below the usual least zoom').toBeLessThan(0.2);
      await expect.soft(zoomOut).not.toHaveAttribute('aria-disabled');

      await zoomInBy(2);
      await expect.poll(() => nodesOutsideCanvas(page)).not.toEqual([]);
      await fit.press('Enter');
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      await expect.poll(async () => level(await zoomLevel(page))).toBe(fitted);
      await expect.poll(() => nodesUnderOverlays(page)).toEqual([]);

      // With the minimap hidden, Fit keeps no room for it: the diagram is
      // larger. Showing or hiding the minimap leaves the view as it is, so
      // the button first goes back to the view from before Fit.
      const minimapToggle = page.getByTestId('toolbar-minimap');
      const restore = controls.getByRole('button', {
        name: 'Restore previous view',
      });
      await minimapToggle.click();
      await restore.press('Enter');
      await fit.press('Enter');
      await expect
        .poll(async () => level(await zoomLevel(page)))
        .toBeGreaterThan(fitted);
      await expect.poll(() => nodesUnderOverlays(page)).toEqual([]);
      await minimapToggle.click();
      await restore.press('Enter');
      await fit.press('Enter');
      await expect.poll(async () => level(await zoomLevel(page))).toBe(fitted);

      // Zooming out by hand goes past the fitted zoom, then stops, and says
      // so.
      await zoomInBy(2);
      for (let press = 0; press < 12; press += 1) {
        await zoomOut.press('Enter');
      }
      await expect.soft(zoomOut).toHaveAttribute('aria-disabled', 'true');
      await expect.soft(zoomOut).toBeFocused();
      await expect.soft(zoomIn).not.toHaveAttribute('aria-disabled');
      expect
        .soft(await zoomLevel(page), 'zoomed out past the fitted zoom')
        .toBeLessThan(fitted);
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);

      // A larger window raises the least zoom past this one; scrolling out
      // then keeps the zoom rather than zooming in to the new least.
      const least = await zoomLevel(page);
      const frames = () =>
        page.evaluate(
          () =>
            new Promise((resolve) =>
              requestAnimationFrame(() => requestAnimationFrame(resolve)),
            ),
        );
      await page.setViewportSize({ width: 1920, height: 1200 });
      await frames();
      const canvasBox = await page.locator('.vue-flow__pane').boundingBox();
      await page.mouse.move(
        canvasBox.x + canvasBox.width / 2,
        canvasBox.y + canvasBox.height / 2,
      );
      await page.mouse.wheel(0, 400);
      await frames();
      await expect.soft(zoomOut).toHaveAttribute('aria-disabled', 'true');
      expect(await zoomLevel(page), 'scrolling out did not zoom in').toBe(
        least,
      );
      await page.setViewportSize({ width: 1440, height: 900 });

      await zoomInBy(5);
      await expect.poll(() => nodesOutsideCanvas(page)).not.toEqual([]);
      await page.getByTestId('editor-reset-view').click();
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      await expect.poll(async () => level(await zoomLevel(page))).toBe(fitted);
      await expect.poll(() => nodesUnderOverlays(page)).toEqual([]);

      // The switch's row shows its network: every node.
      await zoomInBy(2);
      await expect.poll(() => nodesOutsideCanvas(page)).not.toEqual([]);
      const row = page.getByTestId(`outline-item-${sw.id}`);
      await row.press('Enter');
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      await expect.poll(() => nodesUnderOverlays(page)).toEqual([]);
      await expect.soft(row).toBeFocused();
    });
    expectNoFatal(issues);
  });
});
