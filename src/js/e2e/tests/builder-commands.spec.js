// Builder command palette, the default Alt+Shift keys and the shortcut sheet.
// The palette's axe scans are in builder-a11y.spec.js.

const crypto = require('crypto');

const {
  blankDocument,
  download,
  expect,
  expectAccessible,
  expectNoFatal,
  iconName,
  iconOf,
  ownColor,
  pngOf,
  seedIcon,
  seedTemplates,
  templateLibrary,
  test,
  uniqueName,
} = require('./builder-support');

const mac = process.platform === 'darwin';

// Three devices with addresses and a role label on one switch of network
// ot-net, VLAN 101.
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
        labels: { role: hostname.startsWith('plc') ? 'plc' : 'historian' },
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
              .getByRole('option', { name: 'Focus canvas' }),
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
        // The group commands and "Go to the Drafts page" (g, r and p in order).
        await expect.soft(commands.count).toHaveText('8 results');
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

        // A dialog command runs once the palette closes, and closing the dialog
        // returns focus to the button. Download answers to "export" as well.
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
        // The Draft group: a template or an icon of another test can also
        // answer to the word.
        const draftGroup = commands.dialog.getByRole('group', {
          name: 'Draft',
          exact: true,
        });
        await expect
          .soft(draftGroup.locator('.builder-command-palette__title'))
          .toHaveText([
            'Download…',
            'Download Builder JSON',
            'Download Builder YAML',
            'Download Topology YAML',
            'Download PNG',
            'Download SVG',
            'Download draw.io',
            'Download Gephi (GEXF)',
          ]);
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

  test('finds nodes by address and label and networks by VLAN, and builds a selection with Shift+Enter', async ({
    page,
    builder,
  }, testInfo) => {
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
        .toHaveAccessibleDescription(/^eth0 IP address 10\.0\.1\.21 · Device/);
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
        .toContainText('Removed plc-sub-3 from the selection, 1 item selected');
      await expect.soft(commands.dialog).toBeVisible();
      await page.keyboard.press('Escape');
      await expect.soft(selected).toHaveCount(1);
      // Once the palette closes, the live region says the selection once, not
      // every change again late.
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

    await test.step('@ finds nodes by a label, and the second line shows it', async () => {
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill('@role=plc');
      await expect(commands.options).toHaveCount(2);
      await expect.soft(commands.options.first()).toContainText('plc-sub-3');
      await expect
        .soft(commands.options.first())
        .toHaveAccessibleDescription(/^label role=plc · Device/);
      await page.keyboard.press('Escape');
      await expect(commands.dialog).toHaveCount(0);
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
  });
});

test.describe('command palette library search', () => {
  test('finds templates, collections and icons, adds a device from a template, and shows a collection and an icon', async ({
    page,
    builder,
    request,
    tracker,
  }, testInfo) => {
    const commands = palette(page);
    const name = uniqueName(testInfo, 'cmdp-pump');
    const set = uniqueName(testInfo, 'cmdp-set');
    const icon = iconOf(pngOf(8, 8, ownColor()), iconName('cmdp'));

    await seedIcon(request, tracker, icon);
    const seeded = await seedTemplates(
      request,
      tracker,
      [
        {
          name,
          description: 'Found by the command palette',
          device: {
            iconKey: 'server',
            spec: {
              type: 'VirtualMachine',
              general: { hostname: 'cmdp-pump', vm_type: 'kvm' },
              hardware: {
                os_type: 'linux',
                drives: [{ image: 'cmdp.qc2' }],
              },
              network: { interfaces: [] },
            },
          },
        },
      ],
      { collection: { name: set, description: '' } },
    );
    const library = templateLibrary(page);

    await openDraftByName(
      page,
      builder,
      `cmdp-lib-${testInfo.project.name}-${Date.now()}`,
    );

    await test.step('? lists the prefixes t:, c: and i:', async () => {
      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill('?');
      for (const title of [
        't: Find a template',
        'c: Find a collection',
        'i: Find an icon',
      ]) {
        await expect
          .soft(commands.options.filter({ hasText: title }))
          .toHaveCount(1);
      }
      await page.keyboard.press('Escape');
    });

    await test.step('a plain search adds the collection and its template, found by the collection name', async () => {
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill(set);
      const collections = commands.dialog.getByRole('group', {
        name: 'Collections',
        exact: true,
      });
      const templates = commands.dialog.getByRole('group', {
        name: 'Templates',
        exact: true,
      });
      await expect(collections.getByRole('option')).toHaveCount(1);
      await expect.soft(collections.getByRole('option')).toContainText(set);
      await expect
        .soft(templates.getByRole('option'))
        .toHaveAccessibleDescription(new RegExp(`^${set} · cmdp\\.qc2`));
      await page.keyboard.press('Escape');
    });

    await test.step('t: finds the template, and Enter adds a device made from it', async () => {
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill(`t:${name}`);
      await expect(commands.options).toHaveCount(1);
      await expect.soft(commands.count).toHaveText('1 template');
      await expect.soft(commands.active).toContainText(name);
      await page.keyboard.press('Enter');
      await expect(commands.dialog).toHaveCount(0);
      await expect(builder.nodes('device')).toHaveCount(4);
      await expect(
        builder.nodes('device').filter({ hasText: 'cmdp-pump' }),
      ).toHaveCount(1);
    });

    await test.step('c: shows the collection on the Node Templates tab, after the editor closes', async () => {
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill(`c:${set}`);
      await expect(commands.active).toContainText(set);
      await expect
        .soft(commands.active)
        .toHaveAccessibleDescription(/^Library collections · 1 template/);
      await page.keyboard.press('Enter');
      await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
      await expect(library.tab).toHaveAttribute('aria-selected', 'true');
      await expect(library.show).toHaveValue(seeded.collection);
      await expect.soft(library.show).toBeFocused();
      await expect.soft(library.card(seeded.ids[0])).toBeVisible();
    });

    await test.step('on the drafts page, t: opens the template in the template editor', async () => {
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill(`t:${name}`);
      await expect(commands.active).toContainText(name);
      await expect
        .soft(commands.active)
        .toHaveAccessibleDescription(/^Library · cmdp\.qc2/);
      await page.keyboard.press('Enter');
      const editor = page.getByTestId('template-dialog');
      await expect(editor).toBeVisible();
      await expect.soft(editor.getByTestId('template-name')).toHaveValue(name);
      await page.keyboard.press('Escape');
      await expect(editor).toHaveCount(0);
    });

    await test.step('i: shows the icon’s preview on the Icons tab', async () => {
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill(`i:${icon.name}`);
      await expect(commands.active).toContainText(icon.name);
      await page.keyboard.press('Enter');
      await expect(page.getByTestId('drafts-tab-icons')).toHaveAttribute(
        'aria-selected',
        'true',
      );
      const preview = page.getByTestId('icon-preview-dialog');
      await expect(preview).toBeVisible();
      await expect.soft(preview).toHaveAccessibleName(`Icon ${icon.name}`);
      await page.keyboard.press('Escape');
      // Behind the preview, the tab shows the server's icons, with this one.
      await expect
        .soft(page.locator('#panel-icons').getByLabel('Show', { exact: true }))
        .toHaveValue('server');
      await expect
        .soft(page.getByTestId(`icon-card-${icon.name}`))
        .toHaveCount(1);
    });
  });
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
          .toHaveAccessibleDescription(`Change layout (${keys('L')})`);
        await builder.canvas.focus();
        await page.keyboard.press('Alt+Shift+KeyL');
        await expect(builder).toHaveAnnounced('Applied ELK layered layout');
        await expect.soft(builder.canvas).toBeFocused();
        await expect
          .soft(layout)
          .toHaveAccessibleDescription(`Change layout (${keys('L')})`);
      });

      await test.step('the Auto-group key groups by network, and its menu says so', async () => {
        const autoGroup = builder.toolbar('auto-group');
        await expect
          .soft(autoGroup)
          .toHaveAccessibleDescription(
            `Group the ungrouped nodes automatically. ${keys('G')} groups by network`,
          );
        // From a node on the canvas, as from anywhere in the editor but a
        // text field.
        await builder.canvasItem('hist-01').focus();
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

// The shortcut sheet. Shortcuts are kept per browser context, so each test's
// changes end with it.
test.describe('keyboard shortcut sheet', () => {
  test('changes and refuses shortcuts, gives Snap to grid a key, and turns single keys off', async ({
    page,
    builder,
    issues,
  }) => {
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

    await test.step('Snap to grid, which has no default key, turns on from the palette and off by a key given in the sheet', async () => {
      const commands = palette(page);
      const snap = row('view.snapToGrid');
      const settings = () =>
        page.evaluate(() =>
          JSON.parse(localStorage.getItem('phenix.builder.settings')),
        );

      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill('snap to grid');
      await expect(commands.active).toContainText('Turn on snap to grid');
      await page.keyboard.press('Enter');
      await expect(builder).toHaveAnnounced('Snap to grid is on.');
      expect((await settings())?.snapToGrid).toBe(true);

      await builder.canvas.focus();
      await page.keyboard.press('?');
      await expect(sheet).toBeVisible();
      await expect(snap).toHaveCount(0);
      await sheet.getByRole('button', { name: 'Change shortcuts' }).click();
      await snap.getByTestId('shortcut-change').click();
      await expect(recorder).toBeFocused();
      await page.keyboard.press('Alt+Shift+KeyJ');
      await expect(message).toContainText('is free.');
      await page.keyboard.press('Enter');
      await expect(keysOf('view.snapToGrid')).toContainText(
        mac ? 'Option+Shift+J' : 'Alt+Shift+J',
      );
      await page.keyboard.press('Escape');
      await expect(sheet).toBeHidden();

      await builder.canvas.focus();
      await page.keyboard.press('Alt+Shift+KeyJ');
      await expect(builder).toHaveAnnounced('Snap to grid is off.');
      expect((await settings())?.snapToGrid).toBeUndefined();
    });

    await test.step('a clash is refused', async () => {
      await page.keyboard.press('?');
      await sheet.getByRole('button', { name: 'Change shortcuts' }).click();
      await row('draft.download').getByTestId('shortcut-change').click();

      await page.keyboard.press('ControlOrMeta+d');
      await expect(message).toContainText(
        'already runs Duplicate. Select Use for Download to move it',
      );
      // Enter does not take it from Duplicate.
      await page.keyboard.press('Enter');
      await expect(keysOf('edit.duplicate')).toContainText(
        mac ? 'Command+D' : 'Ctrl+D',
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

      // ? no longer opens the sheet and nothing offers the single keys. The
      // header's Shortcuts button still opens it.
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

      // The sheet opens on the customization. Done closes it, back to
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
  });

  // The groups sit in balanced columns, so a short group leaves no gap.
  test(
    'is wider than the other dialogs, with its groups in columns that leave no gaps',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      const sheet = page.getByTestId('shortcuts-dialog');
      const initial = page.viewportSize();
      // The sheet's box, sideways scroll, and per column the left edge, the
      // gaps between groups and the height.
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
      // At every width: registry order down the columns, one top for all
      // columns, no gaps, and no sideways scroll.
      const expectColumns = (found, at) => {
        expect
          .soft(found.order, `${at}: groups in order`)
          .toEqual([
            'General',
            'Edit',
            'Selection',
            'Structure',
            'Add',
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

// The view commands of the canvas: hidden connections, selecting devices by
// network or by image, and Go to node at a readable zoom.
test.describe('canvas view commands', () => {
  // The scale of the live canvas's pan and zoom.
  const zoomOf = (page) =>
    page
      .locator(
        '#builder-canvas .vue-flow__pane > .vue-flow__transformationpane',
      )
      .evaluate(
        (pane) => new DOMMatrixReadOnly(getComputedStyle(pane).transform).a,
      );

  test('Show or hide connections hides them on the canvas only, and the SVG download keeps them', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const draft = await builder.seedDraft(
      rangeDocument(uniqueName(testInfo, 'links')),
    );
    const edges = page.locator('#builder-canvas .vue-flow__edge');
    const labels = page.locator('#builder-canvas .builder-edge__label');
    const keys = mac ? 'Option+Shift+C' : 'Alt+Shift+C';

    await builder.openDraft(draft);
    await expect(edges).toHaveCount(3);
    await expect(edges.first()).toBeVisible();

    await test.step('its key hides them and says so', async () => {
      await builder.canvas.focus();
      await page.keyboard.press('Alt+Shift+KeyC');
      await expect(builder).toHaveAnnounced('Connections hidden.');
      for (const index of [0, 1, 2]) {
        await expect.soft(edges.nth(index)).toBeHidden();
        await expect.soft(labels.nth(index)).toBeHidden();
      }
    });

    await test.step('the keys pass hidden connections by', async () => {
      await page.keyboard.press('ControlOrMeta+a');
      await expect(builder).toHaveAnnounced('Selected every node.');
      await expect
        .soft(page.locator('.vue-flow__edge[aria-pressed="true"]'))
        .toHaveCount(0);
      await expect
        .soft(page.locator('.vue-flow__node[aria-pressed="true"]'))
        .toHaveCount(4);

      const node = builder.canvasItem('hist-01');

      await node.focus();
      await page.keyboard.press('PageDown');
      await expect(builder).toHaveAnnounced(
        `Connections are hidden. Press ${keys} to show them.`,
      );
      await expect.soft(node).toBeFocused();
    });

    await test.step('the draft and the SVG download keep every connection', async () => {
      expect.soft((await builder.serverDocument(draft)).edges).toHaveLength(3);

      const dialog = await builder.openDialog('download');
      const file = await download(page, () =>
        dialog.getByTestId('download-svg').click(),
      );
      const drawn = await page.evaluate((markup) => {
        const svg = new DOMParser().parseFromString(markup, 'image/svg+xml');

        return [...svg.querySelectorAll('.vue-flow__edge')].map(
          (edge) => edge.getAttribute('style') || '',
        );
      }, file.text);

      expect.soft(drawn).toHaveLength(3);
      expect
        .soft(drawn.filter((style) => /visibility:\s*hidden/.test(style)))
        .toEqual([]);
      await dialog.getByRole('button', { name: 'Close', exact: true }).click();
    });

    await test.step('a reload keeps them hidden, and the palette shows them', async () => {
      await page.reload();
      await builder.openDraft(draft);
      await expect(edges).toHaveCount(3);
      await expect.soft(edges.first()).toBeHidden();

      await builder.runFromPalette('Show connections');
      await expect(builder).toHaveAnnounced('Connections shown.');
      await expect.soft(edges.first()).toBeVisible();
    });

    expectNoFatal(issues);
  });

  test('selects the devices on a network, or of a disk image, from the palette', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const doc = rangeDocument(uniqueName(testInfo, 'select-by'));
    const plc = doc.nodes.find((node) => node.label === 'plc-sub-4');

    plc.device.spec.hardware.drives = [{ image: 'plc.qc2' }];

    const draft = await builder.seedDraft(doc);
    const commands = palette(page);
    const selected = page.locator('.vue-flow__node[aria-pressed="true"]');

    await builder.openDraft(draft);

    await test.step('Select devices on network', async () => {
      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill('select devices on network');
      await expect(commands.active).toContainText('Select devices on network');
      await page.keyboard.press('Enter');
      await expect(commands.active).toContainText('ot-net');
      await expect
        .soft(commands.active)
        .toHaveAccessibleDescription('3 devices · VLAN 101');
      await page.keyboard.press('Enter');
      await expect(commands.dialog).toHaveCount(0);
      await expect(builder).toHaveAnnounced(
        'Selected 3 devices on network ot-net',
      );
      await expect.soft(selected).toHaveCount(3);
    });

    await test.step('Select devices using image', async () => {
      await page.keyboard.press('ControlOrMeta+k');
      await commands.field.fill('select devices using image');
      await expect(commands.active).toContainText('Select devices using image');
      await page.keyboard.press('Enter');
      await expect(commands.options).toHaveCount(2);
      await expect.soft(commands.options.first()).toContainText('plc.qc2');
      await expect.soft(commands.options.last()).toContainText('ubuntu.qc2');
      await page.keyboard.press('Enter');
      await expect(builder).toHaveAnnounced(
        'Selected 1 device using image plc.qc2',
      );
      await expect.soft(selected).toHaveCount(1);
      await expect.soft(selected).toContainText('plc-sub-4');
    });

    expectNoFatal(issues);
  });

  test('Go to node zooms in below 60%, and only pans from there', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const draft = await builder.seedDraft(
      rangeDocument(uniqueName(testInfo, 'goto-zoom')),
    );
    const commands = palette(page);
    const goTo = async (name) => {
      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+Shift+o');
      await commands.field.fill(`@${name}`);
      await expect(commands.active).toContainText(name);
      await page.keyboard.press('Enter');
      await expect(commands.dialog).toHaveCount(0);
    };

    // The diagram opens at 40%, a zoom too small to read.
    await builder.useSettings({ openZoom: 'custom', openZoomPercent: 40 });
    await builder.openDraft(draft);
    await expect.poll(() => zoomOf(page)).toBeCloseTo(0.4, 2);

    await goTo('plc-sub-4');
    await expect.poll(() => zoomOf(page)).toBeCloseTo(1, 2);
    const node = builder.canvasItem('plc-sub-4');

    await expect.soft(node).toBeFocused();
    await expect.soft(node).toBeInViewport();

    // At 100%, Go to keeps the zoom.
    await goTo('hist-01');
    await expect.soft(builder.canvasItem('hist-01')).toBeFocused();
    await expect.soft(builder.canvasItem('hist-01')).toBeInViewport();
    expect.soft(await zoomOf(page)).toBeCloseTo(1, 2);

    expectNoFatal(issues);
  });

  test('a slow Download format shows a ring on its button and starts once', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const draft = await builder.seedDraft(
      rangeDocument(uniqueName(testInfo, 'slow-download')),
    );
    let release;
    const held = new Promise((resolve) => {
      release = resolve;
    });
    let exports = 0;

    await page.route('**/api/v1/builder/export/topology', async (route) => {
      exports += 1;
      await held;
      await route.continue();
    });
    await builder.openDraft(draft);

    const dialog = await builder.openDialog('download');
    const topology = dialog.getByTestId('download-topology-yaml');
    const png = dialog.getByTestId('download-png');

    await topology.click();
    await expect(topology).toHaveAttribute('aria-busy', 'true');
    await expect.soft(topology.getByTestId('builder-spinner')).toBeVisible();
    await expect.soft(topology).toHaveAccessibleName('Topology YAML');
    await expect.soft(topology).toBeFocused();
    await expect.soft(png).toHaveAttribute('aria-disabled', 'true');
    await expect
      .soft(dialog.getByRole('status'))
      .toHaveText('Preparing Topology YAML…');

    // A second press starts nothing.
    await topology.click({ force: true });

    const file = await download(page, async () => release());

    expect.soft(file.name).toMatch(/\.topology\.yaml$/);
    expect.soft(exports).toBe(1);
    await expect.soft(topology).not.toHaveAttribute('aria-busy');
    await expect.soft(png).not.toHaveAttribute('aria-disabled');

    expectNoFatal(issues);
  });
});
