// Builder Flow command palette: opening it from the drafts landing and the
// editor, running commands and their steps, the reasons it gives, recent
// commands, and finding nodes and networks. The palette's axe scans run with
// the other dialogs' in builder-a11y.spec.js; its search and ranking are
// unit tested in test/builder/command-search.test.js.

const crypto = require('crypto');

const {
  blankDocument,
  expect,
  expectAccessible,
  expectNoFatal,
  test,
} = require('./builder-support');

const mac = process.platform === 'darwin';

// Three devices with addresses on one switch of network ot-net, VLAN 101.
function rangeDocument(name) {
  const id = () => crypto.randomUUID();
  const network = { id: id(), name: 'ot-net', alias: 101 };
  const sw = {
    id: id(),
    kind: 'switch',
    label: 'ot-net',
    position: { x: 0, y: 400 },
    switch: { networkId: network.id },
  };
  const devices = [
    ['hist-01', '10.0.1.5', '00:16:3E:0A:01:05'],
    ['plc-sub-3', '10.0.1.21', '00:16:3E:0A:01:21'],
    ['plc-sub-4', '10.0.1.22', '00:16:3E:0A:01:22'],
  ].map(([hostname, address, mac], index) => ({
    id: id(),
    kind: 'device',
    label: hostname,
    position: { x: index * 260, y: 0 },
    device: {
      hostname,
      iconKey: 'linux',
      spec: {
        type: 'VirtualMachine',
        general: { hostname, vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
        network: {
          interfaces: [
            {
              name: 'eth0',
              proto: 'static',
              type: 'ethernet',
              vlan: 'ot-net',
              address,
              mask: 24,
              mac,
            },
          ],
        },
      },
      interfaces: [{ id: id(), name: 'eth0', index: 0 }],
    },
  }));

  return blankDocument(name, {
    nodes: [...devices, sw],
    networks: [network],
    edges: devices.map((device) => ({
      id: id(),
      sourceNodeId: device.id,
      sourceHandleId: device.device.interfaces[0].id,
      targetNodeId: sw.id,
      networkId: network.id,
    })),
  });
}

function palette(page) {
  const dialog = page.getByTestId('commands-dialog');

  return {
    dialog,
    field: page.getByRole('combobox', { name: /Search commands|choose a/ }),
    options: dialog.getByRole('option'),
    message: page.getByTestId('command-palette-message'),
    count: page.getByTestId('command-palette-count'),
    // The highlighted option, which the field names with
    // aria-activedescendant.
    active: dialog.getByRole('option', { selected: true }),
  };
}

async function openDraftByName(page, builder, name) {
  const draft = await builder.seedDraft(rangeDocument(name));
  const commands = palette(page);

  await builder.open();

  // The landing's Commands button shows the platform's key, and opens the
  // palette on the landing's commands.
  const button = page.getByTestId('drafts-commands');
  await expect.soft(button).toHaveAccessibleName('Commands');
  await expect
    .soft(button)
    .toHaveAttribute('aria-keyshortcuts', mac ? 'Meta+K' : 'Control+K');
  await expect
    .soft(button.locator('kbd'))
    .toHaveText(mac ? ['⌘', 'K'] : ['Ctrl', 'K']);
  await button.click();
  await expect(commands.field).toBeFocused();
  await expect
    .soft(commands.options.filter({ hasText: 'Blank diagram' }))
    .toHaveCount(1);
  await expect
    .soft(commands.options.filter({ hasText: 'Undo' }))
    .toHaveCount(0);

  // A draft is found by name, and Enter opens it.
  await commands.field.fill(name);
  await expect(commands.active).toContainText(name);
  await page.keyboard.press('Enter');
  await expect(builder.canvas).toBeVisible();
  await expect(page.getByTestId('builder-name')).toHaveValue(name);
  await builder.waitSaved();

  return draft;
}

test.describe('command palette', () => {
  test(
    'runs commands and their steps from anywhere, says why one cannot run, and returns focus',
    { tag: '@cross-browser' },
    async ({ page, builder }, testInfo) => {
      const commands = palette(page);
      await openDraftByName(
        page,
        builder,
        `cmdp-${testInfo.project.name}-${Date.now()}`,
      );

      await test.step('opens on the canvas with Ctrl+K (⌘K on macOS); Escape returns focus', async () => {
        await builder.canvas.focus();
        await page.keyboard.press('ControlOrMeta+k');
        await expect(commands.field).toBeFocused();
        await expect
          .soft(commands.field)
          .toHaveAttribute('aria-expanded', 'true');
        // Options are grouped, and each group is named by its heading.
        await expect
          .soft(
            commands.dialog
              .getByRole('group', { name: /^Go to$/i })
              .getByRole('option', { name: 'Focus outline' }),
          )
          .toHaveCount(1);
        await page.keyboard.press('Escape');
        await expect(commands.dialog).toHaveCount(0);
        await expect.soft(builder.canvas).toBeFocused();
      });

      await test.step('an unavailable command stays reachable and says why', async () => {
        await page.keyboard.press('ControlOrMeta+k');
        await commands.field.fill('grp');
        const group = commands.options.filter({ hasText: 'Group selection' });
        await expect.soft(group).toHaveAttribute('aria-disabled', 'true');
        await expect
          .soft(group)
          .toHaveAccessibleDescription(
            /Unavailable: Select at least one node to group\./,
          );
        await expect.soft(group.locator('mark')).toHaveText(['Gr', 'p']);

        await page.keyboard.press('ArrowDown');
        await expect.soft(commands.active).toContainText('Ungroup');
        await expect
          .soft(commands.field)
          .toHaveAttribute(
            'aria-activedescendant',
            await commands.active.getAttribute('id'),
          );
        await page.keyboard.press('Enter');
        await expect
          .soft(commands.message)
          .toHaveText('Ungroup is unavailable. Select a group first.');
        await expect.soft(commands.dialog).toBeVisible();
        // Group selection, Ungroup, Auto-group by network and by name, and
        // Add group.
        await expect.soft(commands.count).toHaveText('5 results');
        await page.keyboard.press('Escape');
        await expect.soft(builder.canvas).toBeFocused();
      });

      await test.step('Add device asks for a template; Backspace goes back', async () => {
        await page.keyboard.press('ControlOrMeta+k');
        await commands.field.fill('add dev');
        await page.keyboard.press('Enter');
        await expect
          .soft(page.getByTestId('command-palette-step'))
          .toHaveText('Add device ›');
        await expect
          .soft(commands.dialog.getByRole('combobox'))
          .toHaveAccessibleName('Add device: choose a template');
        await page.keyboard.press('Backspace');
        await expect.soft(commands.field).toHaveValue('add dev');
        await page.keyboard.press('Enter');

        await commands.field.fill('router');
        await page.keyboard.press('Enter');
        await expect(commands.dialog).toHaveCount(0);
        await expect(builder.nodes('device')).toHaveCount(4);
        await expect.soft(builder.liveRegion).toContainText('Added');
        await expect.soft(builder.canvas).toBeFocused();
      });

      await test.step('recent commands come first, with their choices', async () => {
        await page.keyboard.press('ControlOrMeta+k');
        const recent = commands.dialog.getByRole('group', { name: 'Recent' });
        await expect
          .soft(recent.getByRole('option').first())
          .toContainText('Add device › Router');
        await page.keyboard.press('Escape');
      });

      await test.step('the toolbar’s Commands button opens it, and focus returns there', async () => {
        const button = builder.toolbar('commands');
        await expect.soft(button).toHaveAttribute('aria-haspopup', 'dialog');
        await expect
          .soft(button)
          .toHaveAttribute('aria-keyshortcuts', mac ? 'Meta+K' : 'Control+K');
        await button.click();
        await expect(commands.field).toBeFocused();

        // Tab wraps between the field and Close dialog.
        await page.keyboard.press('Tab');
        await expect
          .soft(commands.dialog.getByRole('button', { name: 'Close dialog' }))
          .toBeFocused();
        await page.keyboard.press('Tab');
        await expect.soft(commands.field).toBeFocused();

        // The palette's key closes it from Close dialog too, rather than
        // reaching the browser (Firefox would focus its search bar).
        await page.keyboard.press('Shift+Tab');
        await page.keyboard.press('ControlOrMeta+k');
        await expect(commands.dialog).toHaveCount(0);
        await expect.soft(button).toBeFocused();
        await button.click();
        await expect(commands.field).toBeFocused();

        // A command that opens a dialog runs once the palette has closed:
        // closing that dialog returns focus to the button too.
        await commands.field.fill('export');
        await page.keyboard.press('Enter');
        await expect(
          page.getByRole('dialog', { name: 'Export diagram' }),
        ).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(button).toBeFocused();
      });
    },
  );

  test(
    'finds nodes by address and networks by VLAN, and builds a selection with Shift+Enter',
    { tag: '@cross-browser' },
    async ({ page, builder }, testInfo) => {
      const commands = palette(page);
      await openDraftByName(
        page,
        builder,
        `cmdp-find-${testInfo.project.name}-${Date.now()}`,
      );
      const selected = page.locator('.vue-flow__node[aria-pressed="true"]');

      await test.step('Go to node opens it at @; a node is found by IP address', async () => {
        await builder.canvas.focus();
        await page.keyboard.press('ControlOrMeta+Shift+o');
        await expect(commands.field).toHaveValue('@');
        await commands.field.fill('@10.0.1.2');
        await expect(commands.options).toHaveCount(2);
        await expect.soft(commands.count).toHaveText('2 nodes');
        const first = commands.options.first();
        await expect.soft(first).toContainText('plc-sub-3');
        await expect
          .soft(first)
          .toHaveAccessibleDescription(/^IP address 10\.0\.1\.21 · Device/);
        await expect.soft(first.locator('mark')).toHaveText('10.0.1.2');
      });

      await test.step('Shift+Enter adds nodes to the selection and stays open', async () => {
        await page.keyboard.press('Shift+Enter');
        await expect
          .soft(commands.message)
          .toContainText('Added plc-sub-3 to the selection');
        await page.keyboard.press('ArrowDown');
        await page.keyboard.press('Shift+Enter');
        await expect
          .soft(commands.message)
          .toContainText('Added plc-sub-4 to the selection, 2 items selected');
        await page.keyboard.press('ArrowUp');
        await page.keyboard.press('Shift+Enter');
        await expect
          .soft(commands.message)
          .toContainText(
            'Removed plc-sub-3 from the selection, 1 item selected',
          );
        await expect.soft(commands.dialog).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(selected).toHaveCount(1);
        // Each change was said on the palette's status line. Once it has
        // closed, the live region says what is selected, once, rather than
        // every change again, late ("Added plc-sub-3…" after its removal).
        await expect.soft(builder.liveRegion).toHaveText('Selected plc-sub-4.');
      });

      await test.step('Enter selects a node found by MAC address alone and focuses it', async () => {
        await page.keyboard.press('ControlOrMeta+k');
        await commands.field.fill('@0a:01:05');
        await page.keyboard.press('Enter');
        await expect(commands.dialog).toHaveCount(0);
        await expect.soft(selected).toHaveCount(1);
        await expect
          .soft(page.locator('.vue-flow__node').filter({ hasText: 'hist-01' }))
          .toBeFocused();
      });

      await test.step('# finds a network by its VLAN alias and selects its switch', async () => {
        await page.keyboard.press('ControlOrMeta+k');
        await commands.field.fill('#101');
        await expect(commands.options).toHaveCount(1);
        await expect.soft(commands.options.first()).toContainText('ot-net');
        await page.keyboard.press('Enter');
        await expect(commands.dialog).toHaveCount(0);
        await expect
          .soft(builder.liveRegion)
          .toContainText('Selected 1 switch of network ot-net');
      });

      await test.step('nothing found says so and offers the searches to try', async () => {
        await page.keyboard.press('ControlOrMeta+k');
        await commands.field.fill('vlan 4095');
        await expect
          .soft(page.getByTestId('command-palette-empty'))
          .toContainText('Nothing matches “vlan 4095”.');
        await expect.soft(commands.count).toHaveText('No results');
        await page.keyboard.press('Enter');
        await expect.soft(commands.field).toHaveValue('@vlan 4095');
        await page.keyboard.press('Escape');
      });
    },
  );
});

// The keyboard shortcut sheet (BuilderShortcuts.vue), where shortcuts are
// changed too. Shortcuts are kept per browser, and every test has a browser
// context of its own, so the test's changes end with it.
test.describe('keyboard shortcut sheet', () => {
  test(
    'changes, refuses and resets shortcuts, and turns single keys off',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const sheet = page.getByTestId('shortcuts-dialog');
      const row = (id) => sheet.getByTestId(`shortcut-${id}`);
      const keysOf = (id) => row(id).getByTestId('shortcut-keys');
      const recorder = page.getByTestId('shortcut-recorder-input');
      const message = page.getByTestId('shortcut-recorder-message');
      const minimap = builder.toolbar('minimap');
      const stored = () =>
        page.evaluate(() =>
          JSON.parse(localStorage.getItem('phenix.builder.shortcuts')),
        );

      await test.step('? and the palette’s key answer right after the app header’s link', async () => {
        // Arriving through the header leaves focus on its link, outside
        // the Builder, where it stays.
        await page.goto('/experiments');
        const link = page.getByTestId('nav-builder-beta');
        await link.click();
        await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
        await expect.soft(link).toBeFocused();
        await page.keyboard.press('?');
        await expect(sheet).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(link).toBeFocused();
        await page.keyboard.press('ControlOrMeta+k');
        await expect(page.getByTestId('commands-dialog')).toBeVisible();
        await page.keyboard.press('Escape');
      });

      await builder.createBlank();

      await test.step('? opens the sheet, with the platform keys', async () => {
        await builder.canvas.focus();
        await page.keyboard.press('?');
        await expect(sheet).toBeVisible();
        const filter = sheet.getByLabel('Filter shortcuts');
        await expect(filter).toBeFocused();
        await expect(keysOf('edit.undo')).toContainText(
          mac ? 'Command+Z' : 'Ctrl+Z',
        );

        // One character finds keys: G is Group's and Ungroup's.
        await filter.fill('g');
        await expect(sheet.locator('.builder-shortcuts__title')).toHaveText([
          'Group selection',
          'Ungroup',
        ]);
        await filter.fill('');
        await expectAccessible(page, {
          include: '[data-testid="shortcuts-dialog"]',
          soft: true,
          label: 'axe on the shortcut sheet',
        });
      });

      await test.step('a new shortcut applies at once, everywhere', async () => {
        await sheet.getByRole('button', { name: 'Change shortcuts' }).click();
        await expect(
          sheet.getByRole('heading', { name: 'Change keyboard shortcuts' }),
        ).toBeVisible();

        await row('view.minimap')
          .getByRole('button', {
            name: 'Change shortcut for Show or hide minimap',
          })
          .click();
        await expect(recorder).toBeFocused();
        await page.keyboard.press('ControlOrMeta+Shift+m');
        await expect(message).toContainText('is free.');
        await page.keyboard.press('Enter');

        await expect(
          row('view.minimap').getByTestId('shortcut-change'),
        ).toBeFocused();
        await expect(keysOf('view.minimap')).toContainText(
          mac ? 'Shift+Command+M' : 'Ctrl+Shift+M',
        );
        await expect(minimap).toHaveAttribute(
          'aria-keyshortcuts',
          mac ? 'Shift+Meta+M' : 'Control+Shift+M',
        );
        expect(await stored()).toEqual({
          keys: { 'view.minimap': ['Mod+Shift+M'] },
          singleKeys: true,
        });

        // Closed, the sheet gives focus back to the canvas, where the new
        // key works.
        await page.keyboard.press('Escape');
        await expect(builder.canvas).toBeFocused();
        await expect(minimap).toHaveAttribute('aria-pressed', 'true');
        await page.keyboard.press('ControlOrMeta+Shift+m');
        await expect(minimap).toHaveAttribute('aria-pressed', 'false');
      });

      await test.step('a clash or a key the browser keeps is refused', async () => {
        await page.keyboard.press('?');
        await sheet.getByRole('button', { name: 'Change shortcuts' }).click();
        await row('draft.export').getByTestId('shortcut-change').click();

        await page.keyboard.press('ControlOrMeta+d');
        await expect(message).toContainText(
          'already runs Duplicate. Choose Use for Export to move it',
        );
        await expect(page.getByTestId('shortcut-take')).toHaveText(
          'Use for Export',
        );
        // Enter does not take it from Duplicate.
        await page.keyboard.press('Enter');
        await expect(keysOf('edit.duplicate')).toContainText(
          mac ? 'Command+D' : 'Ctrl+D',
        );

        await page.keyboard.press('ControlOrMeta+0');
        await expect(message).toContainText(
          'to zoom, which people rely on to enlarge text. Choose another.',
        );
        await expect(recorder).toHaveAttribute('aria-invalid', 'true');
        await page.keyboard.press('q');
        await expect(message).toContainText('Letters without');
        // The canvas and the outline take ⌫ and the arrows with any
        // modifiers, before the shortcuts see them.
        await page.keyboard.press('ControlOrMeta+Backspace');
        await expect(message).toContainText(
          mac
            ? 'which take Delete with any modifiers'
            : 'which take Backspace with any modifiers',
        );
        await expectAccessible(page, {
          include: '[data-testid="shortcuts-dialog"]',
          soft: true,
          label: 'axe on changing a shortcut',
        });

        // Escape cancels the recording, not the dialog.
        await page.keyboard.press('Escape');
        await expect(sheet).toBeVisible();
        await expect(
          row('draft.export').getByTestId('shortcut-change'),
        ).toBeFocused();
        await expect(keysOf('draft.export')).toHaveText('No shortcut');

        // The palette works in text fields, so a key that types a
        // character there cannot open it.
        await row('palette.open').getByTestId('shortcut-change').click();
        await page.keyboard.press('/');
        await expect(message).toContainText(
          'types a character in text fields, where Command palette works too',
        );
        // So does Option on macOS (⌥E is the acute accent), but not Alt on
        // Windows and Linux.
        await page.keyboard.press('Alt+KeyE');
        await expect(message).toContainText(
          mac
            ? 'types a character in text fields, where Command palette works too, so its shortcut needs ⌘ or ⌃.'
            : 'is free.',
        );
        await page.keyboard.press('Escape');
        await expect(keysOf('palette.open')).toContainText(
          mac ? 'Command+K' : 'Ctrl+K',
        );
      });

      await test.step('Reset and Reset all give the defaults back', async () => {
        await row('view.minimap').getByTestId('shortcut-reset').click();
        await expect(keysOf('view.minimap')).toHaveText('No shortcut');
        await expect(
          row('view.minimap').getByTestId('shortcut-change'),
        ).toBeFocused();
        await expect(minimap).not.toHaveAttribute('aria-keyshortcuts');

        await row('edit.duplicate').getByTestId('shortcut-remove').click();
        await expect(keysOf('edit.duplicate')).toHaveText('No shortcut');
        const resetAll = sheet.getByRole('button', {
          name: 'Reset all shortcuts',
        });
        await resetAll.click();
        await expect(keysOf('edit.duplicate')).toContainText(
          mac ? 'Command+D' : 'Ctrl+D',
        );
        await expect(resetAll).toHaveAttribute('aria-disabled', 'true');
        await expect(resetAll).toBeFocused();
      });

      await test.step('single-key shortcuts can be turned off', async () => {
        const single = sheet.getByRole('switch', {
          name: 'Single-key shortcuts',
        });
        await expect(single).toHaveAttribute('aria-checked', 'true');
        await single.click();
        await expect(single).toHaveAttribute('aria-checked', 'false');
        expect((await stored()).singleKeys).toBe(false);

        await sheet.getByRole('button', { name: 'Done' }).click();
        await expect(
          sheet.getByRole('button', { name: 'Change shortcuts' }),
        ).toBeFocused();
        await expect(keysOf('shortcuts.open')).toContainText('off');
        await page.keyboard.press('Escape');
        await expect(sheet).toBeHidden();

        // ? no longer opens the sheet, nor do the help and the zoom buttons
        // offer the single keys; the Keyboard help's button still opens it.
        await builder.canvas.focus();
        await page.keyboard.press('?');
        await expect(sheet).toBeHidden();
        await expect(
          page.locator('.vue-flow__controls-zoomin'),
        ).not.toHaveAttribute('aria-keyshortcuts');
        // Space opens it: the canvas's pan key does not take it.
        await page.getByTestId('canvas-help').locator('summary').focus();
        await page.keyboard.press('Space');
        await expect(page.getByTestId('canvas-help')).toHaveJSProperty(
          'open',
          true,
        );
        await expect(page.locator('#builder-canvas-help')).not.toContainText(
          'lists every shortcut',
        );
        const open = page.getByTestId('canvas-help-shortcuts');
        await open.click();
        await expect(sheet).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(open).toBeFocused();
      });

      await test.step('Settings has the same switch, and opens the customization over itself', async () => {
        const opener = page.getByTestId('editor-settings');
        const settings = page.getByTestId('settings-dialog');
        const single = settings.getByRole('switch', {
          name: 'Single-key shortcuts',
        });
        const change = settings.getByRole('button', {
          name: 'Change keyboard shortcuts',
        });

        await opener.press('Enter');
        await expect(single).toHaveAttribute('aria-checked', 'false');
        await single.click();
        await expect(single).toHaveAttribute('aria-checked', 'true');
        expect((await stored()).singleKeys).toBe(true);

        // The sheet opens on the customization; Done closes it, back to
        // Settings and the button that opened it.
        await change.press('Enter');
        await expect(
          sheet.getByRole('heading', { name: 'Change keyboard shortcuts' }),
        ).toBeVisible();
        await expect(sheet.getByLabel('Filter shortcuts')).toBeFocused();
        await sheet.getByRole('button', { name: 'Done' }).click();
        await expect(sheet).toBeHidden();
        await expect(change).toBeFocused();
        await page.keyboard.press('Escape');
        await expect(settings).toBeHidden();
        await expect(opener).toBeFocused();
      });

      expectNoFatal(issues);
    },
  );
});
