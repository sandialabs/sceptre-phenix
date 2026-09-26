// Accessibility, keyboard-only authoring and theming for the Builder Beta.
//
// The outline is the advertised pointer-free editing surface, so these tests
// drive it with the keyboard only: focus, arrow keys, Enter, F2, Delete and
// the Connect form. They also run axe on every surface and dialog in both
// themes, and check the theme toggle, minimap and zoom controls.
//
// Each test walks several related checks on one draft, one test.step per
// check. Checks that do not gate the next step are soft, so one failure does
// not hide the others.

const {
  test,
  expect,
  backdropPoint,
  expectAccessible,
  expectNoFatal,
  invisibleText,
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
  await expect(builder.liveRegion).toContainText(`Added ${item}`);
  if (count !== undefined) {
    await expect(rows(builder)).toHaveCount(count);
  }
}

// Blank draft with one device ("node") and one switch ("EXP").
async function deviceAndSwitch(builder) {
  await builder.open();
  const draft = await builder.createBlank();
  await addWithKeyboard(builder, 'device', 1);
  await addWithKeyboard(builder, 'switch', 2);

  return draft;
}

// Connects through the outline's keyboard form: select the device and switch,
// then press Enter on Connect.
async function connectWithKeyboard(builder, device = 'node') {
  await builder.page.locator('#connect-device').selectOption({ label: device });
  await builder.page.locator('#connect-switch').selectOption({ index: 1 });
  await builder.page.getByTestId('outline-connect').press('Enter');
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

// Sets the Builder theme preference with the toolbar toggle (keyboard). With
// `soft`, a toggle that never reaches `theme` is recorded and the test goes on.
async function chooseTheme(page, theme, { soft = false } = {}) {
  const toggle = page.getByTestId('toolbar-theme');
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

// WCAG contrast ratio of each element's text color against its effective
// background.
async function contrastOf(locator) {
  return locator.evaluateAll((elements) => {
    const transparent = (value) =>
      value === 'transparent' || /rgba\(.*,\s*0\)$/.test(value);

    const background = (element) => {
      for (let node = element; node; node = node.parentElement) {
        const value = getComputedStyle(node).backgroundColor;
        if (!transparent(value)) {
          return value;
        }
      }

      return 'rgb(255, 255, 255)';
    };

    const luminance = (value) => {
      const [r, g, b] = (value.match(/[\d.]+/g) || []).map(Number);
      const channel = (c) => {
        const v = c / 255;

        return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
      };

      return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
    };

    return elements.map((element) => {
      const fg = luminance(getComputedStyle(element).color);
      const bg = luminance(background(element));
      const ratio = (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05);

      return {
        text: element.textContent.trim().slice(0, 30),
        color: getComputedStyle(element).color,
        background: background(element),
        ratio: Math.round(ratio * 100) / 100,
      };
    });
  });
}

// Soft: every element matched by `locator` has at least `minimum` contrast.
async function expectReadable(locator, what, minimum = 4.5) {
  const results = await contrastOf(locator);
  expect.soft(results.length, `${what}: no elements found`).toBeGreaterThan(0);
  const failing = results.filter((item) => item.ratio < minimum);
  expect
    .soft(failing, `${what}: ${JSON.stringify(failing, null, 2)}`)
    .toEqual([]);
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

// The tracker deletes drafts with If-Match. A snapshot still in flight when
// the test ends changes the ETag under that delete and leaves the draft
// behind, so let autosave settle first (hooks run before fixture teardown).
test.afterEach(async ({ builder }) => {
  if (await builder.saveState.isVisible().catch(() => false)) {
    await builder.waitSaved(10000).catch(() => {});
  }
});

// --- keyboard-only authoring through the outline ---------------------------

test.describe('keyboard-only authoring', () => {
  test('Connect, F2 on a switch and a second switch build a diagram without a pointer', async ({
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
    const connect = page.getByTestId('outline-connect');
    const error = page.locator('.builder-outline__error');

    // A blank diagram says it has no nodes or networks instead of showing
    // empty lists, which ARIA does not allow (a list owns list items).
    await expect
      .soft(page.getByTestId('builder-outline-empty'))
      .toHaveText('No nodes yet. Add one from the Add nodes panel.');
    await expect
      .soft(page.getByTestId('builder-networks-empty'))
      .toHaveText('No networks yet. Adding a switch creates one.');
    expect.soft(await emptyLists(page), 'empty lists').toEqual([]);

    // With nothing to choose, the message says what to add, and it follows
    // the diagram as the nodes are added.
    await connect.press('Enter');
    await expect
      .soft(error)
      .toHaveText('Add a device and a switch to the diagram first.');
    await addWithKeyboard(builder, 'device', 1);
    await expect
      .soft(error)
      .toHaveText('Choose a device. Add a switch to the diagram first.');
    await addWithKeyboard(builder, 'switch', 2);
    await expect.soft(error).toHaveText('Choose a device and a switch.');
    await expect
      .soft(builder.summary)
      .toContainText('1 device, 1 switch, 1 network');
    const deviceId = await rowNodeId(builder, 'node');
    const switchTestId = await rowTestId(builder, 'EXP');
    const switchId = switchTestId.replace('outline-item-', '');

    await test.step('Connect form names, marks and focuses a missing device or switch', async () => {
      const device = page.locator('#connect-device');
      const sw = page.locator('#connect-switch');

      await connect.press('Enter');
      await expect.soft(error).toHaveText('Choose a device and a switch.');
      await expect.soft(error).toHaveAttribute('role', 'alert');
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

    await test.step('the completed Connect form connects the device', async () => {
      await page.locator('#connect-switch').selectOption({ index: 1 });
      await connect.press('Enter');
      // The rename step below renames this connection's network.
      await builder.expectSummary('1 connection');
      await expect.soft(error).toHaveCount(0);
      await expect.soft(builder.liveRegion).toContainText('Connected nodes');
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
        .soft(builder.liveRegion)
        .toContainText('Added node to the selection, 2 items selected');
      await page.keyboard.press('Shift+Enter');
      await expect
        .soft(builder.liveRegion)
        .toContainText('Removed node from the selection, 1 item selected');
      await expect.soft(edge).toHaveClass(/\bselected\b/);

      // A plain press keeps only the row, and says the connection went.
      await page.keyboard.press('Enter');
      await expect.soft(builder.liveRegion).toContainText('Selected node only');
      await expect.soft(edge).not.toHaveClass(/\bselected\b/);
      await page.keyboard.press('Enter');
      await expect.soft(builder.liveRegion).toContainText('Deselected node');
    });

    await test.step('F2 on the switch renames its network and edge', async () => {
      await page.getByTestId(switchTestId).focus();
      await page.keyboard.press('F2');
      await expect.soft(page.getByLabel('Rename EXP')).toBeFocused();
      await page.keyboard.type('MGMT');
      await page.keyboard.press('Enter');

      await expect
        .soft(builder.liveRegion)
        .toContainText('Updated network MGMT');
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
        .soft(page.getByRole('button', { name: /^Network MGMT from node to / }))
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
    });

    await test.step('Disconnect in the command palette and Remove network act without a pointer, and focus stays in place', async () => {
      // The outline has no connection rows, so the palette names the
      // connection from its device's end, with the interface.
      const device = page.getByTestId(`outline-item-${deviceId}`);
      const dialog = page.getByTestId('commands-dialog');

      await device.focus();
      await page.keyboard.press('ControlOrMeta+k');
      await page.keyboard.type('Disconnect');
      await page.keyboard.press('Enter');
      await expect
        .soft(dialog.getByRole('option'))
        .toHaveText([/^node \(eth0\) to .+network MGMT$/]);
      await page.keyboard.press('Enter');
      await builder.expectSummary('0 connections');
      await expect.soft(dialog).toHaveCount(0);
      await expect.soft(page.locator('path.builder-edge')).toHaveCount(0);
      await expect.soft(device).toBeFocused();
      // The interface stays, free to connect again.
      await page.locator('#connect-device').selectOption({ label: 'node' });
      await expect
        .soft(page.locator('#connect-interface option', { hasText: 'eth0' }))
        .toHaveCount(1);

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

  test(
    'skip link, arrow keys, Enter, Space, F2 and Delete work on outline rows',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      await builder.open();
      const draft = await builder.createBlank();
      await addWithKeyboard(builder, 'device', 1);
      await addWithKeyboard(builder, 'switch', 2);
      await addWithKeyboard(builder, 'note', 3);
      const all = rows(builder);
      const deviceTestId = await rowTestId(builder, 'node');
      const deviceId = deviceTestId.replace('outline-item-', '');
      const switchTestId = await rowTestId(builder, 'EXP');

      // Each step focuses its own row, so a failed check in one step does not
      // stop the next.
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
        await expect.soft(builder.liveRegion).toContainText('Selected node');
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
        await expect
          .soft(builder.liveRegion)
          .toContainText('Selected EXP only');

        // Shift+Enter toggles a row in and out of the selection.
        await page.keyboard.press('ArrowDown');
        await page.keyboard.press('Shift+Enter');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'true');
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'true');
        await expect
          .soft(builder.liveRegion)
          .toContainText('Added node to the selection, 2 items selected');
        await expect
          .soft(page.locator('.vue-flow__node.selected'))
          .toHaveCount(2);
        await page.keyboard.press('Shift+Enter');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'false');
        await expect
          .soft(builder.liveRegion)
          .toContainText('Removed node from the selection, 1 item selected');

        // A plain press on a row in a larger selection keeps only that row,
        // and says so; on the only selected row it deselects it.
        await page.keyboard.press('Shift+Enter');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'true');
        await page.keyboard.press('Enter');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'true');
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'false');
        await expect
          .soft(builder.liveRegion)
          .toContainText('Selected node only');
        await page.keyboard.press(' ');
        await expect.soft(device).toHaveAttribute('aria-pressed', 'false');
        await expect.soft(builder.liveRegion).toContainText('Deselected node');
        await expect
          .soft(page.locator('.vue-flow__node.selected'))
          .toHaveCount(0);

        // A click toggles the same way.
        await sw.click();
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'true');
        await expect.soft(sw).toBeFocused();
        await sw.click();
        await expect.soft(sw).toHaveAttribute('aria-pressed', 'false');
        await expect.soft(builder.liveRegion).toContainText('Deselected EXP');
      });

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
        await expect
          .soft(builder.liveRegion)
          .toContainText('Renamed device to web-01');
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
        await expect
          .soft(builder.liveRegion)
          .toContainText('All changes saved');
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
        await expect.soft(builder.liveRegion).toContainText('Deleted web-01');
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

  test('a canvas node is one tab stop, and its focus ring differs from selection', async ({
    page,
    builder,
  }) => {
    await deviceAndSwitch(builder);
    await builder.selectInOutline('EXP');
    const id = await rowNodeId(builder, 'node');
    const node = page.locator(`.vue-flow__node[data-id="${id}"]`);

    // The splitter between the Outline and the canvas is the last stop
    // before the canvas.
    await page
      .getByRole('separator', { name: 'Resize Add nodes and Outline' })
      .focus();
    const unfocused = await nodeFocusLook(page, id);

    // The canvas follows the outline in tab order; its first stop is the
    // (unselected) device's Vue Flow wrapper, a toggle button.
    await page.keyboard.press('Tab');
    expect(await focusedNodeId(page)).toBe(id);
    await expect.soft(node).toBeFocused();
    await expect.soft(node).toHaveAttribute('aria-pressed', 'false');
    const focused = await nodeFocusLook(page, id);
    expect
      .soft(focused, 'focusing the node must change its appearance')
      .not.toBe(unfocused);

    // Enter selects it, and the next Tab leaves the node.
    await page.keyboard.press('Enter');
    await expect.soft(node).toHaveAttribute('aria-pressed', 'true');
    await page.keyboard.press('Tab');
    expect
      .soft(await focusedNodeId(page), 'the next Tab must leave the node')
      .not.toBe(id);
    expect
      .soft(await nodeFocusLook(page, id), 'selection must not look like focus')
      .not.toBe(focused);
  });
});

// --- axe scans in both themes -------------------------------------------------

const DIALOGS = [
  { action: 'publish', title: 'Publish diagram' },
  { action: 'import', title: 'Upload diagram' },
  { action: 'export', title: 'Export diagram' },
  { action: 'scenario', title: 'Scenario' },
  { action: 'history', title: 'Draft history' },
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
    await expect
      .soft(builder.dialog.getByRole('heading', { name: title }))
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

  await page.keyboard.press('Escape');
  await expect(builder.dialog).toHaveCount(0);
  await expect.soft(opener, `${surface} returns focus`).toBeFocused();
}

for (const scheme of ['light', 'dark']) {
  test(
    `axe finds no serious violations in the ${scheme} theme`,
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      // Scans every view and dialog in turn, which takes close to a minute.
      test.slow();
      await openWithScheme(page, builder, scheme);
      // The server lists no disk images (it runs no minimega). One that is
      // not the new device's gives the device a drive image warning, so the
      // scans cover the Inspector's checks and the Diagram checks dialog
      // with an issue in each.
      await page.route('**/api/v1/disks', (route) =>
        route.fulfill({ json: { disks: [{ kind: 'VM', name: 'other.qc2' }] } }),
      );
      const draft = await builder.createBlank();

      await test.step('the editor titles the page and its heading takes focus', async () => {
        const heading = page.getByRole('heading', { level: 1 });
        // Blank drafts are numbered after the first (V10).
        await expect
          .soft(heading)
          .toHaveText(/^Untitled topology( \d+)? – Builder Flow\s*$/);
        await expect.soft(heading).toBeFocused();
        await expect
          .soft(page)
          .toHaveTitle(/^Untitled topology( \d+)? – Builder Flow – phēnix$/);
        await expect.soft(page.locator('html')).toHaveAttribute('lang', 'en');
      });

      await test.step('the header has Back to drafts, the name, the counts, the save state, Reset view, Shortcuts, Settings, the Help link of the landing, the checks and Focus mode', async () => {
        const back = page.getByRole('button', { name: 'Back to drafts' });
        const help = page.getByRole('link', {
          name: 'Help (opens in a new tab)',
        });
        const name = page.getByLabel('Diagram name');
        const counts = page.getByRole('list', { name: 'Diagram contents' });
        const countsTip = page.getByTestId('counts-tooltip');
        const reset = page.getByRole('button', { name: 'Reset view' });
        const shortcuts = page.getByRole('button', {
          name: 'Shortcuts',
          exact: true,
        });
        const checks = page.getByTestId('builder-checks');
        const settings = page.getByTestId('editor-settings');
        const focusMode = page.getByRole('button', {
          name: 'Focus mode',
          exact: true,
        });
        const count = (index) => counts.getByRole('listitem').nth(index);

        // Left to right, as Tab goes: Back to drafts, the name, the counts
        // (one stop, at the first count), then past the save state to Reset
        // view, Shortcuts, Settings, Help, the checks and Focus mode.
        await back.focus();
        for (const next of [
          name,
          count(0),
          reset,
          shortcuts,
          settings,
          help,
          checks,
          focusMode,
        ]) {
          await page.keyboard.press('Tab');
          await expect.soft(next).toBeFocused();
        }
        // The save state sits just before the buttons, on their row.
        const saved = await builder.saveState.boundingBox();
        const first = await reset.boundingBox();
        expect
          .soft(
            first.x - (saved.x + saved.width),
            'save state before Reset view',
          )
          .toBeGreaterThanOrEqual(0);
        expect
          .soft(first.x - (saved.x + saved.width), 'save state by Reset view')
          .toBeLessThan(16);
        expect.soft(saved.y, 'save state on the buttons’ row').toBe(first.y);

        // The name's label is read but not shown; a tooltip gives it on
        // hover.
        await expect
          .soft(page.locator('label[for="builder-doc-name"]'))
          .toHaveClass(/builder-visually-hidden/);
        await name.hover();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText('Diagram name');

        // Each count is an icon and a number, read in words, which show on
        // hover and on focus. The arrow keys, Home and End move between the
        // counts, and the list keeps one Tab stop.
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
        await count(3).hover();
        await expect.soft(countsTip).toHaveText('0 connections');
        await count(0).focus();
        await expect.soft(countsTip).toHaveText('0 devices');
        await page.keyboard.press('ArrowRight');
        await expect.soft(count(1)).toBeFocused();
        await expect.soft(countsTip).toHaveText('0 switches');
        await page.keyboard.press('End');
        await expect.soft(countsTip).toHaveText('0 notes');
        await page.keyboard.press('ArrowRight');
        await expect.soft(count(0)).toBeFocused();
        await expect.soft(counts.locator('[tabindex="0"]')).toHaveCount(1);
        await page.keyboard.press('Escape');
        await expect.soft(countsTip).toHaveCount(0);

        // Shortcuts opens the shortcut sheet, and gets focus back from it.
        await shortcuts.focus();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText(/^Keyboard shortcuts/);
        await page.keyboard.press('Enter');
        await expect.soft(page.getByTestId('shortcuts-dialog')).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(shortcuts).toBeFocused();
        // Settings' and Help's tooltips name them too.
        await settings.hover();
        await expect
          .soft(page.getByTestId('header-tooltip'))
          .toHaveText(/^Builder settings/);
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
            'https://phenix.sceptre.dev/latest/configuration/#builder',
          );
        await expect.soft(help).toHaveAttribute('target', '_blank');
        await expect.soft(help).toHaveAttribute('rel', /\bnoopener\b/);
        await expectReadable(back, 'Back to drafts');
        await expectReadable(help, 'editor Help link');
        await expectReadable(reset, 'Reset view');
        await expectReadable(shortcuts, 'Shortcuts');
        await expectReadable(counts, 'counts');
        await expectReadable(builder.saveState, 'save state');
      });

      await addWithKeyboard(builder, 'device', 1);
      await addWithKeyboard(builder, 'switch', 2);
      // A connected device, so the scans cover the Inspector's interface
      // form and its oneOf kind picker.
      await connectWithKeyboard(builder);
      await builder.expectSummary('1 connection');
      await builder.selectInOutline('node');
      await expect(page.locator('.builder-inspector__subject')).toContainText(
        'Device node',
      );
      // And the node's More settings section, open, with a row of its
      // names and values (R34).
      await builder.inspector
        .getByTestId('inspector-section')
        .locator('summary')
        .click();
      await builder.inspector
        .getByRole('button', { name: 'Add label' })
        .click();
      await expect(builder.inspector.getByLabel('Label 1 Name')).toBeFocused();

      // axe reports text colored like its background as incomplete, not as
      // a violation: Bulma once drew Inspector labels and "Error:" prefixes
      // white on white in the light theme (V1, R44).
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

      for (const { action, title } of DIALOGS) {
        await test.step(`${title} dialog`, () =>
          scanDialog(
            page,
            builder,
            builder.toolbar(action),
            `${title} dialog (${scheme})`,
            title,
          ));
      }

      // The command palette's first row is its search field, which takes
      // focus. The scans cover an unavailable command, a node search and no
      // results.
      await test.step('Command palette', async () => {
        const opener = builder.toolbar('commands');
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

      await test.step('Builder settings dialog', () =>
        scanDialog(
          page,
          builder,
          page.getByTestId('editor-settings'),
          `Builder settings dialog (${scheme})`,
          'Builder settings',
        ));

      // Every Builder dialog shares this through BuilderDialog.
      await test.step('a click outside a dialog closes it; a drag across its edge does not', async () => {
        const opener = builder.toolbar('scenario');
        await opener.press('Enter');
        await expect(builder.dialog).toBeVisible();
        for (const name of [
          'No scenario',
          'Stored scenario',
          'Upload scenario',
        ]) {
          await expect
            .soft(builder.dialog.getByRole('radio', { name, exact: true }))
            .toBeVisible();
        }

        // The drag ends in a click on the dialog, outside its box.
        const outside = await backdropPoint(builder.dialog);
        const legend = await builder.dialog
          .getByText('Scenario reference')
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
        await expect.soft(opener, 'focus returns to the opener').toBeFocused();
      });

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

        for (const { action } of DIALOGS) {
          await expect
            .soft(builder.toolbar(action))
            .toHaveAttribute('aria-haspopup', 'dialog');
        }
      });

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
          .toHaveText('Builder Flow');
        await expect.soft(page).toHaveTitle('Builder Flow – phēnix');
        // Import (the Generate dialog) comes before Upload (the Import
        // dialog), then Commands, with its key caps, and Settings; Help, a
        // link to the documentation, is last.
        const actions = page.locator('.builder-drafts__header > div > *');
        await expect
          .soft(actions)
          .toHaveText([
            'Blank diagram',
            'Import',
            'Upload',
            process.platform === 'darwin' ? 'Commands ⌘K' : 'Commands Ctrl+K',
            'Settings',
            'Help (opens in a new tab)',
          ]);
        for (const opener of [
          'drafts-import',
          'drafts-generate',
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
            'https://phenix.sceptre.dev/latest/configuration/#builder',
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
        // The nav link's "beta" tag is part of its name, and readable.
        const nav = page.getByTestId('nav-builder-beta');
        await expect.soft(nav).toHaveAccessibleName('Builder Flow beta');
        await expectReadable(nav.locator('.tag'), 'nav beta tag');
        await expectAccessible(page, {
          soft: true,
          label: `axe on drafts landing (${scheme})`,
        });
      });

      await test.step('drafts tabs follow the APG tabs pattern', async () => {
        const tab = (id) => page.getByTestId(`drafts-tab-${id}`);

        await tab('mine').focus();
        await page.keyboard.press('End');
        await expect.soft(tab('published')).toBeFocused();
        await expect
          .soft(tab('published'))
          .toHaveAttribute('aria-selected', 'true');
        await page.keyboard.press('Home');
        await expect.soft(tab('mine')).toBeFocused();
        await expect.soft(tab('mine')).toHaveAttribute('aria-selected', 'true');

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

      await test.step('Generate dialog', () =>
        scanDialog(
          page,
          builder,
          page.getByTestId('drafts-generate'),
          `Generate dialog (${scheme})`,
          'Import topology or experiment',
        ));
      expectNoFatal(issues);
    },
  );
}

// --- themes, minimap and zoom -------------------------------------------------

test.describe('themes and canvas controls', () => {
  // From System the toggle goes to the opposite of the OS scheme first, so
  // the first press always changes the colors. Its name and tooltip say
  // what the next press does.
  test('theme toggle goes System, Dark, Light on a light OS, says what is next and persists the choice', async ({
    page,
    builder,
    issues,
  }) => {
    await openWithScheme(page, builder, 'light');
    const draft = await builder.createBlank();
    const toggle = page.getByTestId('toolbar-theme');
    const tooltip = page.getByTestId('toolbar-tooltip');
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
    await toggle.focus();
    await expect.soft(tooltip).toHaveText('Switch to Dark theme');

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
      await expect.soft(tooltip).toHaveText(`Switch to ${next} theme`);
      await expect(root(page)).toHaveAttribute(
        'data-builder-theme-preference',
        theme,
      );
      await expect(root(page)).toHaveAttribute('data-builder-theme', resolved);
      await expect(builder.liveRegion).toContainText(`Theme set to ${theme}.`);
      expect(await stored()).toBe(theme);
    }

    await page.reload();
    await expect(
      page.getByRole('heading', { name: 'Builder Flow' }),
    ).toBeVisible({ timeout: 20000 });
    await expect(root(page)).toHaveAttribute('data-builder-theme', 'dark');
    await expect(root(page)).toHaveAttribute(
      'data-builder-theme-preference',
      'dark',
    );

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
    const toggle = page.getByTestId('toolbar-theme');
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

  test(
    'dark theme text, controls and inputs are legible',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      await openWithScheme(page, builder, 'dark');
      await builder.createBlank();
      await addWithKeyboard(builder, 'device', 1);
      await builder.selectInOutline('node');
      await expect(page.locator('.builder-inspector__subject')).toContainText(
        'Device node',
      );

      await test.step('no text is colored like its background', async () => {
        const found = await invisibleText(page);
        expect
          .soft(found, `invisible text: ${JSON.stringify(found, null, 2)}`)
          .toEqual([]);
      });

      // builder-layout.spec.js checks the zoom controls' contrast in the dark
      // theme, along with their borders and the minimap mask.
      await test.step('minimap uses a dark surface', async () => {
        const minimap = page.getByRole('img', { name: 'Diagram minimap' });
        await expect.soft(minimap).toBeVisible();
        // The theme's text color must stay readable on the minimap background.
        await expectReadable(minimap, 'minimap');
      });

      await test.step('outline rename input', async () => {
        await row(builder, 'node').focus();
        await page.keyboard.press('F2');
        const input = page.getByLabel('Rename node');
        await expect.soft(input).toBeFocused();
        await expectReadable(input, 'rename input');
        await page.keyboard.press('Escape');
        // No rename input may stay open while the next step edits.
        await expect(input).toHaveCount(0);
      });

      await test.step('JSON Forms array buttons', async () => {
        // A connection gives the Interfaces array an item with its own toolbar.
        await addWithKeyboard(builder, 'switch', 2);
        await connectWithKeyboard(builder);
        await builder.expectSummary('1 connection');
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
      expectNoFatal(issues);
    },
  );

  test('minimap toggle and zoom in, zoom out and fit controls', async ({
    page,
    builder,
    issues,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();
    for (let count = 1; count <= 6; count += 1) {
      await addWithKeyboard(builder, 'device', count);
    }

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
      await fit.press('Enter');
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);

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
      expect(focusedId, 'Shift+Tab reaches the last device').toBeTruthy();
      await expect
        .poll(() => nodesOutsideCanvas(page))
        .not.toContain(focusedId);
    });

    await test.step('on the canvas, Shift+1 fits, − zooms out and = or + zooms in', async () => {
      const level = (value) => Math.round(value * 1000) / 1000;

      // Focus is on a node, zoomed in as far as it goes.
      await page.keyboard.press('Shift+Digit1');
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      const fitted = await zoomLevel(page);
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

    await test.step('Settings choose the minimap, the zoom a diagram opens with and reduced motion, and keep only those choices', async () => {
      const opener = page.getByTestId('editor-settings');
      const settings = page.getByTestId('settings-dialog');
      const layout = settings.getByRole('combobox', {
        name: 'Auto layout algorithm',
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

      // Zoomed in, Reset view fits the diagram, as Shift+1 does.
      await page.getByTestId('editor-reset-view').click();
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      const opened = level(await zoomLevel(page));
      await builder.canvas.focus();
      await page.keyboard.press('Shift+Digit1');
      await expect.poll(async () => level(await zoomLevel(page))).toBe(opened);
      await expect.soft(minimap).toHaveCount(0);

      // A new page opens the draft fitted, without the minimap.
      await builder.waitSaved();
      await page.reload();
      await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
      await page.getByTestId(`draft-open-${draft.id}`).click();
      await expect(builder.canvas).toBeVisible();
      await expect.poll(async () => level(await zoomLevel(page))).toBe(opened);
      await expect
        .soft(page.getByTestId('toolbar-minimap'))
        .toHaveAttribute('aria-pressed', 'false');
      await expect
        .soft(root(page))
        .toHaveAttribute('data-builder-reduced-motion', 'true');

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
      await other.goto(new URL('/builder-beta', page.url()).href);
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
      const upload = await builder.openDialog('import');
      await upload.getByLabel('Paste text', { exact: true }).check();
      await upload
        .getByTestId('import-text')
        .fill(JSON.stringify(await builder.serverDocument(draft)));
      const created = page.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          new URL(response.url()).pathname.endsWith('/builder/drafts'),
      );
      await upload.getByTestId('import-submit').click();
      const uploaded = await (await created).json();
      expect.soft(uploaded.id, 'a new draft').not.toBe(draft.id);
      await expect(builder.dialog).toBeHidden();
      await expect.poll(async () => level(await zoomLevel(page))).toBe(opened);
      await expect.poll(() => nodesOutsideCanvas(page)).toEqual([]);
      await expect.soft(toggle).toHaveAttribute('aria-pressed', 'false');
      await expect.soft(minimap).toHaveCount(0);
      await expect.soft(builder.toolbar('import')).toBeFocused();
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
});
