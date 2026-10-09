// Builder command palette: opening it from the drafts landing and the
// editor, running commands and their steps, the reasons it gives, recent
// commands, and finding nodes and networks. The palette's axe scans run with
// the other dialogs' in builder-a11y.spec.js; its search and ranking are
// unit tested in test/builder/command-search.test.js. Then the default keys
// of Settings, the layout and Auto-group, and the keyboard shortcut sheet.

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
  await expect(page.getByTestId('builder-name')).toHaveText(name);
  await builder.waitSaved();

  return draft;
}

test.describe('command palette', () => {
  test(
    'runs commands and their steps from anywhere, says why one cannot run, and returns focus',
    { tag: '@cross-browser' },
    async ({ page, builder }, testInfo) => {
      const commands = palette(page);
      const name = `cmdp-${testInfo.project.name}-${Date.now()}`;
      await openDraftByName(page, builder, name);

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
        // Group selection, Ungroup, Auto-group by network, by name and by
        // name pattern, Move to a group…, and Add group.
        await expect.soft(commands.count).toHaveText('7 results');
        await expect
          .soft(commands.options.filter({ hasText: 'Move to a group…' }))
          .toHaveCount(1);
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
        await expect.soft(builder).toHaveAnnounced('Added');
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

      await test.step('the header’s Commands button opens it, and focus returns there', async () => {
        const button = page.getByTestId('editor-commands');
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
        // closing that dialog returns focus to the button too. Download
        // answers to "export" as well, and the first match, Download…, only
        // opens the dialog.
        await commands.field.fill('export');
        await page.keyboard.press('Enter');
        const download = page.getByRole('dialog', { name: 'Download diagram' });
        await expect(download).toBeVisible();
        await expect.soft(download.getByRole('status')).toHaveText('');
        await page.keyboard.press('Escape');
        await expect.soft(button).toBeFocused();
      });

      await test.step('"export" lists every download format, and a format’s command downloads it in the dialog', async () => {
        const opener = page.getByTestId('editor-commands');
        await expect(opener).toBeFocused();
        await page.keyboard.press('ControlOrMeta+k');
        await commands.field.fill('export');
        await expect
          .soft(commands.options.locator('.builder-command-palette__title'))
          .toHaveText([
            'Download…',
            'Download Builder JSON',
            'Download Builder YAML',
            'Download Topology YAML',
            'Download PNG',
            'Download SVG',
            'Download Gephi (GEXF)',
          ]);
        await expect.soft(commands.count).toHaveText('7 results');
        await expect
          .soft(commands.options.filter({ hasText: 'Download Gephi (GEXF)' }))
          .toHaveAccessibleDescription(
            /The network as a graph to analyze in Gephi/,
          );

        // The dialog opens with the format's button focused and its
        // download made, and says so where a press of the button would.
        await commands.field.fill('export png');
        await expect(commands.active).toContainText('Download PNG');
        const [file] = await Promise.all([
          page.waitForEvent('download'),
          page.keyboard.press('Enter'),
        ]);
        const dialog = page.getByRole('dialog', { name: 'Download diagram' });
        await expect(dialog).toBeVisible();
        expect.soft(file.suggestedFilename()).toBe(`${name}.png`);
        await expect
          .soft(dialog.getByRole('status'))
          .toHaveText(`Saved ${name}.png.`);
        await expect.soft(dialog.getByTestId('download-png')).toBeFocused();
        await expect.soft(dialog.getByTestId('download-error')).toHaveCount(0);
        await page.keyboard.press('Escape');
        await expect(dialog).toHaveCount(0);
        await expect.soft(opener).toBeFocused();
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
        await expect
          .soft(builder)
          .toHaveAnnounced('Selected plc-sub-4.', { exact: true });
      });

      await test.step('Enter selects a node found by MAC address alone and focuses it', async () => {
        await page.keyboard.press('ControlOrMeta+k');
        await commands.field.fill('@0a:01:05');
        await page.keyboard.press('Enter');
        await expect(commands.dialog).toHaveCount(0);
        await expect.soft(selected).toHaveCount(1);
        // The device's wrapper: the wrapper of its switch holds the hidden
        // text of the switch's info tooltip, which names the device too.
        await expect
          .soft(
            page
              .locator('.vue-flow__node-builderDevice')
              .filter({ hasText: 'hist-01' }),
          )
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
          .soft(builder)
          .toHaveAnnounced('Selected 1 switch of network ot-net');
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

// Settings, the layout and Auto-group have keys of their own: Alt and Shift
// (Option and Shift on a Mac) with a letter.
test.describe('default keys', () => {
  const keys = (letter) => (mac ? `⌥⇧${letter}` : `Alt+Shift+${letter}`);

  test(
    'Alt+Shift with S, L and G open Settings, run the layout and auto-group by network',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }, testInfo) => {
      const settings = page.getByTestId('settings-dialog');
      const tooltip = page.getByTestId('header-tooltip');
      const draft = await builder.seedDraft(
        rangeDocument(`keys-${testInfo.project.name}-${Date.now()}`),
      );

      await test.step('Settings opens from the drafts page and the app header, and its button says the key', async () => {
        await builder.open();
        const button = page.getByTestId('drafts-settings');
        await expect
          .soft(button)
          .toHaveAttribute('aria-keyshortcuts', 'Alt+Shift+S');
        await expect.soft(button).toHaveAccessibleDescription(keys('S'));
        await button.focus();
        await expect
          .soft(tooltip)
          .toHaveText(`Builder settings (${keys('S')})`);

        await page.keyboard.press('Alt+Shift+KeyS');
        await expect(settings).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(settings).toHaveCount(0);
        await expect.soft(button).toBeFocused();

        // From the checkbox that selects a card as well: it takes no typed
        // text, so the key is not its to keep, and the card stays as it was.
        const box = page.getByTestId(`card-select-${draft.id}`);
        await box.focus();
        await page.keyboard.press('Alt+Shift+KeyS');
        await expect(settings).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(settings).toHaveCount(0);
        await expect.soft(box).toBeFocused();
        await expect.soft(box).not.toBeChecked();

        // From the app header's link too, outside the Builder.
        const link = page.getByTestId('nav-builder');
        await link.focus();
        await page.keyboard.press('Alt+Shift+KeyS');
        await expect(settings).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(link).toBeFocused();
      });

      await builder.openDraft(draft);

      await test.step('Settings opens in the editor, but not from a text field', async () => {
        const button = page.getByTestId('editor-settings');
        await expect
          .soft(button)
          .toHaveAttribute('aria-keyshortcuts', 'Alt+Shift+S');
        await button.hover();
        await expect
          .soft(tooltip)
          .toHaveText(`Builder settings (${keys('S')})`);

        await builder.canvas.focus();
        await page.keyboard.press('Alt+Shift+KeyS');
        await expect(settings).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(builder.canvas).toBeFocused();

        // A text field keeps the key: on a Mac, Option types a character.
        await page.getByTestId('builder-name-edit').click();
        const field = page.getByTestId('builder-name-field');
        await expect(field).toBeFocused();
        await page.keyboard.press('Alt+Shift+KeyS');
        await page.keyboard.press('Alt+Shift+KeyL');
        await page.keyboard.press('Alt+Shift+KeyG');
        await expect.soft(field).toBeFocused();
        await expect.soft(settings).toHaveCount(0);
        await expect.soft(builder.nodes('group')).toHaveCount(0);
        await page.keyboard.press('Escape');
        await expect(field).toHaveCount(0);
      });

      await test.step('the layout key runs the layout, and its menu says so', async () => {
        const layout = builder.toolbar('layout');
        await expect
          .soft(layout)
          .toHaveAccessibleDescription(
            `Choose a layout. ${keys('L')} runs ELK layered, the Settings default`,
          );
        await builder.canvas.focus();
        await page.keyboard.press('Alt+Shift+KeyL');
        await expect(builder).toHaveAnnounced('Applied ELK layered layout');
        await expect.soft(builder.canvas).toBeFocused();
        await expect
          .soft(layout)
          .toHaveAccessibleDescription(
            `Choose a layout, or run it again. ${keys('L')} runs it again`,
          );
      });

      await test.step('the Auto-group key groups by network, and its menu says so', async () => {
        const autoGroup = builder.toolbar('auto-group');
        await expect
          .soft(autoGroup)
          .toHaveAccessibleDescription(
            `Group the ungrouped nodes automatically. ${keys('G')} groups by network`,
          );
        // From an outline row, as from anywhere in the editor but a text
        // field.
        await builder.outlineItem('hist-01').focus();
        await page.keyboard.press('Alt+Shift+KeyG');
        await expect(builder).toHaveAnnounced('Created 1 group by network');
        await expect(builder.nodes('group')).toHaveCount(1);
        await page.keyboard.press('ControlOrMeta+z');
        await expect(builder).toHaveAnnounced(
          'Undid Created 1 group by network.',
        );
        await expect.soft(builder.nodes('group')).toHaveCount(0);
      });

      await test.step('the sheet lists the three keys, and the palette shows them', async () => {
        const sheet = page.getByTestId('shortcuts-dialog');
        const spoken = (letter) =>
          mac ? `Option+Shift+${letter}` : `Alt+Shift+${letter}`;

        await builder.canvas.focus();
        await page.keyboard.press('?');
        await expect(sheet).toBeVisible();
        for (const [id, letter] of [
          ['settings.open', 'S'],
          ['structure.layout', 'L'],
          ['structure.autoGroup.network', 'G'],
        ]) {
          await expect
            .soft(
              sheet.getByTestId(`shortcut-${id}`).getByTestId('shortcut-keys'),
            )
            .toContainText(spoken(letter));
        }
        await page.keyboard.press('Escape');

        const commands = palette(page);
        await page.keyboard.press('ControlOrMeta+k');
        await commands.field.fill('auto layout');
        await expect(commands.active).toContainText('Auto layout');
        await expect
          .soft(commands.active.locator('kbd'))
          .toHaveText(mac ? ['⌥', '⇧', 'L'] : ['Alt', 'Shift', 'L']);
        await page.keyboard.press('Escape');
      });

      expectNoFatal(issues);
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
        const link = page.getByTestId('nav-builder');
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

        // One character finds keys: G is Group's, Ungroup's and
        // Auto-group by network's.
        await filter.fill('g');
        await expect(sheet.locator('.builder-shortcuts__title')).toHaveText([
          'Group selection',
          'Ungroup',
          'Auto-group by network',
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
        await row('draft.download').getByTestId('shortcut-change').click();

        await page.keyboard.press('ControlOrMeta+d');
        await expect(message).toContainText(
          'already runs Duplicate. Choose Use for Download to move it',
        );
        await expect(page.getByTestId('shortcut-take')).toHaveText(
          'Use for Download',
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
          row('draft.download').getByTestId('shortcut-change'),
        ).toBeFocused();
        await expect(keysOf('draft.download')).toHaveText('No shortcut');

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

        // ? no longer opens the sheet, nor do the canvas's description and
        // the zoom buttons offer the single keys; the header's Shortcuts
        // button, which no longer shows the key, still opens it.
        await builder.canvas.focus();
        await page.keyboard.press('?');
        await expect(sheet).toBeHidden();
        await expect(
          page.locator('.vue-flow__controls-zoomin'),
        ).not.toHaveAttribute('aria-keyshortcuts');
        await expect(builder.canvas).toHaveAccessibleDescription(
          /Shortcuts in the header lists every shortcut\.$/,
        );
        const open = page.getByTestId('editor-shortcuts');
        await expect(open.locator('kbd')).toHaveCount(0);
        await expect(open).not.toHaveAttribute('aria-keyshortcuts');
        await open.focus();
        await expect(page.getByTestId('header-tooltip')).toHaveText(
          'Keyboard shortcuts',
        );
        await page.keyboard.press('Enter');
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
        // The Shortcuts button shows its key again.
        await expect(
          page.getByTestId('editor-shortcuts').locator('kbd'),
        ).toHaveText(['?']);

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

  // The sheet sets its groups in columns the browser balances: a group
  // follows the one before it down a column, so a short group leaves no
  // gap beside a long one.
  test(
    'is wider than the other dialogs, with its groups in columns that leave no gaps',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const sheet = page.getByTestId('shortcuts-dialog');
      const initial = page.viewportSize();
      // The sheet's box, whether it or the page scrolls sideways, and its
      // groups by column: the columns' left edges, and in each column the
      // gaps between one group and the next, and the column's height.
      const layout = () =>
        sheet.evaluate((dialog) => {
          const boxes = [
            ...dialog.querySelectorAll('.builder-shortcuts__group'),
          ].map((group) => {
            const box = group.getBoundingClientRect();

            return {
              name: group.querySelector('h3').textContent.trim(),
              left: Math.round(box.left),
              top: box.top,
              bottom: box.bottom,
            };
          });
          const lefts = [...new Set(boxes.map((box) => box.left))];
          const columns = lefts.map((left) =>
            boxes.filter((box) => box.left === left),
          );

          return {
            width: dialog.getBoundingClientRect().width,
            sideways: dialog.scrollWidth - dialog.clientWidth,
            pageSideways:
              document.scrollingElement.scrollWidth - window.innerWidth,
            order: boxes.map((box) => box.name),
            columns: columns.length,
            tops: columns.map((column) => Math.round(column[0].top)),
            gaps: columns.flatMap((column) =>
              column
                .slice(1)
                .map((box, index) =>
                  Math.round(box.top - column[index].bottom),
                ),
            ),
            heights: columns.map((column) =>
              Math.round(column.at(-1).bottom - column[0].top),
            ),
          };
        });
      const open = async (size) => {
        await page.setViewportSize(size);
        await builder.canvas.focus();
        await page.keyboard.press('?');
        await expect(sheet).toBeVisible();
      };
      // What holds at every width: the groups keep the registry's order
      // down the columns, every column starts at the same height, no group
      // is followed by a gap, and nothing scrolls sideways.
      const expectColumns = (found, at) => {
        expect
          .soft(found.order, `${at}: groups in order`)
          .toEqual([
            'General',
            'Edit',
            'Selection',
            'Structure',
            'Go to',
            'View',
            'Draft',
          ]);
        expect.soft(new Set(found.tops).size, `${at}: column tops`).toBe(1);
        expect
          .soft(Math.max(0, ...found.gaps), `${at}: gap under a group`)
          .toBeLessThan(24);
        expect.soft(found.sideways, `${at}: sheet scrolls sideways`).toBe(0);
        expect
          .soft(found.pageSideways, `${at}: page scrolls sideways`)
          .toBeLessThanOrEqual(0);
      };

      await builder.open();
      await builder.createBlank();
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.getByTestId('editor-settings').click();
      const settingsWidth = await page
        .getByTestId('settings-dialog')
        .evaluate((dialog) => dialog.getBoundingClientRect().width);
      await page.keyboard.press('Escape');

      await test.step('1280 pixels wide: three balanced columns, wider than Settings', async () => {
        await open({ width: 1280, height: 800 });
        const wide = await layout();

        expectColumns(wide, '1280');
        expect.soft(wide.width, 'sheet width').toBeGreaterThan(settingsWidth);
        expect.soft(wide.columns, 'columns').toBe(3);
        // No column is much shorter than the tallest.
        expect
          .soft(
            Math.min(...wide.heights) / Math.max(...wide.heights),
            `column heights ${wide.heights}`,
          )
          .toBeGreaterThan(0.6);
        await expectAccessible(page, {
          include: '[data-testid="shortcuts-dialog"]',
          soft: true,
          label: 'axe on the sheet at 1280 pixels',
        });

        // The customization is one column, at the dialogs' usual width.
        await sheet.getByRole('button', { name: 'Change shortcuts' }).click();
        await expect(
          sheet.getByRole('heading', { name: 'Change keyboard shortcuts' }),
        ).toBeVisible();
        const edit = await layout();
        expect.soft(edit.columns, 'customization columns').toBe(1);
        expect.soft(edit.width, 'customization width').toBe(settingsWidth);
        expect.soft(edit.sideways, 'customization scrolls sideways').toBe(0);
        await sheet.getByRole('button', { name: 'Done' }).click();
        expect.soft((await layout()).columns, 'columns again').toBe(3);
        await page.keyboard.press('Escape');
      });

      await test.step('narrower windows take two columns, then one', async () => {
        await open({ width: 800, height: 700 });
        const two = await layout();
        expectColumns(two, '800');
        expect.soft(two.columns, '800: columns').toBe(2);
        await page.keyboard.press('Escape');

        await open({ width: 400, height: 700 });
        const one = await layout();
        expectColumns(one, '400');
        expect.soft(one.columns, '400: columns').toBe(1);
        expect.soft(one.width, '400: sheet width').toBeLessThanOrEqual(400);
        await expectAccessible(page, {
          include: '[data-testid="shortcuts-dialog"]',
          soft: true,
          label: 'axe on the sheet at 400 pixels',
        });
        await page.keyboard.press('Escape');

        // 1280 pixels at 400% (WCAG 1.4.10).
        await open({ width: 320, height: 640 });
        const reflow = await layout();
        expectColumns(reflow, '320');
        expect.soft(reflow.columns, '320: columns').toBe(1);
        await page.keyboard.press('Escape');
      });

      await page.setViewportSize(initial);
      expectNoFatal(issues);
    },
  );
});
