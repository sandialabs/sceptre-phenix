// Device templates of a diagram and of the server's one library: a test
// changes only what it made and never counts the library (see README).

const crypto = require('crypto');

const {
  BUILTIN_TEMPLATE_IDS,
  LIBRARY,
  blankDocument,
  byHostname,
  changeTemplate,
  devicesOf,
  download,
  expect,
  expectAccessible,
  expectNoFatal,
  iconName,
  iconOf,
  deleteLibraryItems,
  libraryChange,
  ownColor,
  pngOf,
  publishTopology,
  readLibrary,
  routeIconLibrary,
  seedIcon,
  seedTemplates,
  templateLibrary,
  test,
  uniqueName,
  waitForApi,
} = require('./builder-support');

const UUID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

// The spec of the device the tests make templates from: `plc-01`, with a
// memory size, a drive image, and eth0 on network EXP with an address.
function plcSpec(hostname, vlan = 'EXP') {
  return {
    type: 'VirtualMachine',
    general: { hostname, vm_type: 'kvm' },
    hardware: {
      os_type: 'linux',
      memory: 2048,
      drives: [{ image: 'plc.qc2' }],
    },
    network: {
      interfaces: [
        {
          name: 'eth0',
          type: 'ethernet',
          proto: 'static',
          address: '10.0.0.5',
          mask: 24,
          vlan,
        },
      ],
    },
  };
}

// A diagram of plc-01 connected by eth0 to the switch of network EXP, with
// `look` added to the device and `extra` to the document.
function plantDocument(name, { look = {}, ...extra } = {}) {
  const id = () => crypto.randomUUID();
  const network = { id: id(), name: 'EXP' };
  const sw = {
    id: id(),
    kind: 'switch',
    label: 'EXP',
    position: { x: 384, y: 320 },
    switch: { networkId: network.id },
  };
  const plc = {
    id: id(),
    kind: 'device',
    label: 'plc-01',
    position: { x: 64, y: 64 },
    device: {
      hostname: 'plc-01',
      iconKey: 'linux',
      ...look,
      spec: plcSpec('plc-01'),
      interfaces: [{ id: id(), name: 'eth0', index: 0 }],
    },
  };

  return {
    ...blankDocument(name, {
      nodes: [plc, sw],
      networks: [network],
      edges: [
        {
          id: id(),
          sourceNodeId: plc.id,
          sourceHandleId: plc.device.interfaces[0].id,
          targetNodeId: sw.id,
          networkId: network.id,
        },
      ],
    }),
    ...extra,
  };
}

// A template as a document keeps it.
function template(name, init = {}) {
  return {
    id: crypto.randomUUID(),
    name,
    device: { iconKey: 'router', spec: plcSpec(name.toLowerCase(), '') },
    ...init,
  };
}

// The template saved from plc-01 in PLANT_LOOK, named PLC and described, its
// hostname plc and its VLAN PLANT, as the diagram keeps it under `id`.
function plcTemplate(id) {
  return {
    id,
    name: 'PLC',
    description: 'Plant controller',
    device: { iconKey: 'linux', ...PLANT_LOOK, spec: plcSpec('plc', 'PLANT') },
  };
}

// The template editor, and the fields of the Inspector's form in it, by
// their data path.
function templateEditor(page) {
  const dialog = page.getByTestId('template-dialog');
  const field = (path) => dialog.locator(`[data-path="${path}"]`);

  return {
    dialog,
    name: dialog.getByTestId('template-name'),
    description: dialog.getByTestId('template-description'),
    nameHint: dialog.getByTestId('template-name-hint'),
    save: dialog.getByTestId('template-save'),
    cancel: dialog.getByTestId('template-cancel'),
    error: dialog.getByTestId('template-error'),
    status: dialog.getByTestId('template-status'),
    question: dialog.getByTestId('template-discard'),
    keep: dialog.getByTestId('template-keep'),
    discard: dialog.getByTestId('template-discard-confirm'),
    errors: dialog.getByTestId('inspector-errors'),
    errorLink: dialog.locator('.builder-inspector__error-link'),
    tooltip: dialog.getByTestId('inspector-tooltip'),
    field,
    input: (path) => field(path).locator('input, select, textarea').first(),
  };
}

// The palette's "+" and library button, its sections of templates, and
// their lists and entries.
function templatePalette(page) {
  const group = (id) => page.getByTestId(`palette-templates-${id}`);

  return {
    plus: page.getByTestId('palette-new-template'),
    libraryButton: page.getByTestId('palette-library'),
    groups: page.locator('.builder-palette__group-name'),
    diagram: group('diagram'),
    library: group('library'),
    builtin: group('builtin'),
    note: page.getByTestId('palette-library-note'),
    retry: page.getByTestId('palette-library-retry'),
    entry: (id) => page.getByTestId(`palette-template-diagram-${id}`),
    // An entry of the library, or a built-in one.
    own: (id) => page.getByTestId(`palette-template-${id}`),
    actions: (id) => page.getByTestId(`palette-template-actions-${id}`),
    action: (id, item) =>
      page.getByTestId(`palette-template-actions-${id}-item-${item}`),
    // The mark of a copy linked to the library, and its button.
    link: (id) => page.getByTestId(`palette-template-link-${id}`),
    update: (id) => page.getByTestId(`palette-template-update-${id}`),
    unlink: (id) => page.getByTestId(`palette-template-unlink-${id}`),
    tooltip: page.getByTestId('palette-tooltip'),
  };
}

const hostnames = (doc) => devicesOf(doc).map((node) => node.device.hostname);
// The colors of the device of the diagram the template tests start from.
const PLANT_LOOK = { outlineColor: '#2f6fbf', fillColor: '#ffeecc' };

// The name of the palette's section of templates of the library in no
// collection, on a page that reads the library without its collections.
const LIBRARY_SECTION = 'Library';

// Has the page read the library without collections, which other tests make
// meanwhile, so a test can count the palette's sections.
async function withoutCollections(page) {
  await page.route(`**${LIBRARY}`, async (route) => {
    if (route.request().method() !== 'GET') {
      await route.fallback();

      return;
    }

    const response = await route.fetch();
    const json = await response.json();

    await route.fulfill({ response, json: { ...json, collections: [] } });
  });
}

// Opens a new draft of plantDocument() in PLANT_LOOK, with `extra`, on a
// page that reads the library without collections. Returns the draft.
async function openPlant(builder, testInfo, extra = {}) {
  const draft = await builder.seedDraft(
    plantDocument(uniqueName(testInfo, 'templates'), {
      look: PLANT_LOOK,
      ...extra,
    }),
  );
  await withoutCollections(builder.page);
  await builder.openDraft(draft);

  return draft;
}

test(
  'a diagram with no template of its own lists the library, and "+" opens the template editor on the selected device in a large dialog',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const palette = templatePalette(page);
    const editor = templateEditor(page);

    await openPlant(builder, testInfo);

    await test.step('a diagram with no template of its own lists the library, then the five built-in templates', async () => {
      // The built-in templates, in this order, in a section of their own.
      // The library's templates come before them: other tests add some.
      await expect(palette.builtin.getByRole('button')).toHaveCount(
        BUILTIN_TEMPLATE_IDS.length,
      );
      for (const [index, id] of BUILTIN_TEMPLATE_IDS.entries()) {
        await expect(
          palette.builtin.getByRole('button').nth(index),
          id,
        ).toHaveAttribute('data-testid', `palette-template-${id}`);
      }
      await expect.soft(palette.diagram).toHaveCount(0);
      await expect
        .soft(palette.builtin)
        .toHaveAttribute('aria-label', 'Built-in');
      await expect.soft(palette.note).toHaveText('');
      await expect
        .soft(palette.plus)
        .toHaveAccessibleName('New device template');
      await palette.plus.hover();
      await expect.soft(palette.tooltip).toHaveText('New device template');
      await page.mouse.move(700, 400);
    });

    await test.step('"+" opens the editor on the selected device, in a dialog that takes most of the window', async () => {
      await builder.selectNode('plc-01');
      await palette.plus.click();
      await expect(editor.dialog).toBeVisible();
      await expect
        .soft(editor.dialog)
        .toHaveAccessibleName('New device template');
      // Named after the device, with the name selected to type over.
      await expect(editor.name).toBeFocused();
      await expect.soft(editor.name).toHaveValue('plc-01');
      await expect.soft(editor.description).toHaveValue('');
      await expect
        .soft(editor.description)
        .toHaveAccessibleDescription(
          'The Nodes & Templates section shows this text as a tooltip. The template does not copy it to the node.',
        );
      await expect.soft(editor.save).toHaveText('Save to diagram');
      // The template's own fields have no heading: their labels name them.
      await expect
        .soft(editor.dialog.getByRole('heading', { level: 3 }))
        .toHaveText(['Node fields']);
      // The node's spec has a heading of its own, a level under Node
      // fields, with no box around its fields.
      await expect
        .soft(editor.dialog.getByRole('heading', { level: 4 }))
        .toHaveText(['Node']);
      await expect
        .soft(
          editor.dialog.getByRole('form', {
            name: 'Template name and description',
          }),
        )
        .toBeVisible();
      await expect.soft(editor.name).toHaveAccessibleName('Template Name');
      await expect
        .soft(editor.description)
        .toHaveAccessibleName('Template Description');

      for (const size of [
        { width: 1600, height: 900 },
        { width: 1280, height: 800 },
      ]) {
        await page.setViewportSize(size);
        const box = await editor.dialog.boundingBox();

        expect
          .soft(box.width / size.width, `width at ${size.width}`)
          .toBeGreaterThanOrEqual(0.7);
        expect
          .soft(box.height / size.height, `height at ${size.height}`)
          .toBeGreaterThanOrEqual(0.7);
        // Its buttons stay in view while the form scrolls.
        await expect.soft(editor.save).toBeInViewport({ ratio: 1 });
      }

      // The template's own fields beside the node's, which take the wider
      // column and lay their fields out in columns of their own.
      const [about, hostname, icon] = await Promise.all(
        [
          editor.name,
          editor.input('hostname'),
          editor.field('iconKey').getByTestId('inspector-icon'),
        ].map((field) => field.boundingBox()),
      );

      expect.soft(hostname.x).toBeGreaterThan(about.x + about.width);
      expect.soft(icon.x).toBeGreaterThan(hostname.x + hostname.width);
      expect.soft(Math.abs(icon.y - hostname.y)).toBeLessThan(4);
      await page.setViewportSize({ width: 1600, height: 900 });
    });

    expectNoFatal(issues);
  },
);

test('a device template is checked as the Inspector checks a device, and saved in the diagram from the selected device as one edit', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const palette = templatePalette(page);
  const editor = templateEditor(page);
  const draft = await openPlant(builder, testInfo);

  await builder.selectNode('plc-01');
  await palette.plus.click();
  await expect(editor.dialog).toBeVisible();

  await test.step('the form is the Inspector’s, with the device’s values and nothing of the canvas', async () => {
    const form = editor.dialog.getByRole('region', { name: 'Node fields' });

    await expect(form).toBeVisible();
    // What the dialog's status region says from here on, in order: the
    // page's live region cannot be heard through a modal dialog.
    await editor.dialog.evaluate((dialog) => {
      const said = (window.__templateSaid = []);

      new MutationObserver(() => {
        const text = dialog
          .querySelector('[data-testid="template-status"]')
          ?.textContent.trim();

        if (text && said.at(-1) !== text) {
          said.push(text);
        }
      }).observe(dialog, {
        childList: true,
        subtree: true,
        characterData: true,
      });
    });
    await expect.soft(editor.input('hostname')).toHaveValue('plc-01');
    await expect.soft(editor.input('spec.hardware.memory')).toHaveValue('2048');
    await expect
      .soft(editor.input('spec.hardware.drives.0.image'))
      .toHaveValue('plc.qc2');
    await expect
      .soft(editor.field('fillColor').locator('input[type="text"]'))
      .toHaveValue('#ffeecc');
    await expect
      .soft(editor.field('outlineColor').locator('input[type="text"]'))
      .toHaveValue('#2f6fbf');
    await expect
      .soft(editor.input('spec.network.interfaces.0.address'))
      .toHaveValue('10.0.0.5');
    // The VLAN said what the device is connected to in this diagram.
    await expect
      .soft(editor.input('spec.network.interfaces.0.vlan'))
      .toHaveValue('');
    await expect
      .soft(editor.input('hostname'))
      .toHaveAccessibleDescription(
        'Name new devices start from. Builder adds a number when another device has the name.',
      );

    for (const id of [
      'inspector-actions',
      'inspector-position',
      'inspector-add-interface',
      'inspector-checks',
    ]) {
      await expect.soft(form.getByTestId(id), id).toHaveCount(0);
    }
    await expect.soft(form.getByText('Connection points')).toHaveCount(0);
    await expect
      .soft(form.getByRole('heading', { name: 'Inspector' }))
      .toHaveCount(0);
    // The form shows the template's Layer after the colors, offering the
    // diagram's layers, with no Edit layers… (they are a copy).
    const layer = form.getByLabel('Layer', { exact: true });

    await expect.soft(layer).toHaveValue('');
    await expect
      .soft(layer)
      .toHaveAccessibleDescription(
        'The By layer layout puts higher layer IDs above lower ones.',
      );
    await expect.soft(layer.locator('option')).toHaveCount(8);
    await expect
      .soft(form.getByRole('heading', { name: 'Layer', exact: true }))
      .toHaveCount(0);
    await expect
      .soft(form.getByRole('button', { name: 'Edit layers…' }))
      .toHaveCount(0);
    expect
      .soft(
        await layer.evaluate((select) => {
          const fields = [
            ...select.closest('form').querySelectorAll('[data-path], select'),
          ];
          const at = (element) => fields.indexOf(element);
          const path = (name) =>
            select.closest('form').querySelector(`[data-path="${name}"]`);

          return [
            at(path('fillColor')) < at(select),
            at(select) < at(path('spec.type')),
          ];
        }),
      )
      .toEqual([true, true]);
    // The canvas Inspector behind the dialog still shows the device.
    await expect
      .soft(builder.inspector.getByTestId('inspector-position'))
      .toHaveCount(1);
  });

  await test.step('a field is checked as on the canvas, and Save goes to what needs fixing', async () => {
    await editor.input('hostname').fill('two words');
    await editor.save.click();
    await expect(editor.errors).toHaveText(
      '1 field needs attention before you can save this template.',
    );
    await expect(editor.errorLink).toHaveText('Hostname cannot contain spaces');
    await expect(editor.errorLink).toBeFocused();
    await expect.soft(editor.dialog).toBeVisible();
    await expect
      .soft(editor.input('hostname'))
      .toHaveAttribute('aria-invalid', 'true');
    // Nothing was saved, and nothing says the edit waits for Apply.
    await expect.soft(palette.diagram).toHaveCount(0);
    await expect
      .soft(editor.field('hostname'))
      .not.toHaveClass(/control--changed/);
    await expect
      .soft(editor.dialog.getByTestId('inspector-actions'))
      .toHaveCount(0);

    // The entry of the summary goes to its field.
    await page.keyboard.press('Enter');
    await expect(editor.input('hostname')).toBeFocused();
    // A warning shows under its field as the field commits, and the
    // dialog says it.
    await editor.input('hostname').fill('phenix');
    await editor.input('hostname').blur();
    await expect(editor.field('hostname').locator('.warning')).toContainText(
      'matches "phenix"',
    );
    await expect(editor.status).toContainText(
      /^Warning for Hostname: Hostname "phenix" matches "phenix"/,
    );
    await expect.soft(editor.errors).toHaveCount(0);
    await editor.input('hostname').fill('plc');
    // An image the server lacks is warned of, as on the canvas. A VLAN
    // naming no network of a diagram is not.
    await editor.input('spec.network.interfaces.0.vlan').fill('PLANT');
    await editor.input('spec.network.interfaces.0.vlan').blur();
    await expect
      .soft(editor.field('spec.network.interfaces.0.vlan').locator('.warning'))
      .toHaveCount(0);
    await expect.soft(editor.errors).toHaveCount(0);
  });

  await test.step('a template needs a name', async () => {
    await editor.name.fill('   ');
    await editor.save.click();
    await expect(editor.error).toHaveText('Enter a name.');
    await expect(editor.name).toBeFocused();
    await expect.soft(editor.name).toHaveAttribute('aria-invalid', 'true');
    await expect
      .soft(editor.name)
      .toHaveAccessibleDescription(/Enter a name\./);
    await expect.soft(palette.diagram).toHaveCount(0);

    await editor.name.fill('PLC');
    await editor.description.fill('Plant controller');
  });

  await test.step('Save to diagram keeps the template in the diagram, as one edit', async () => {
    // The edits were never said to wait for Apply: the one thing the
    // dialog said is the warning.
    expect
      .soft(await page.evaluate(() => window.__templateSaid))
      .toEqual([expect.stringMatching(/^Warning for Hostname: /)]);

    // Enter in the name or the description saves, as the button does.
    await editor.description.press('Enter');
    await expect(editor.dialog).toHaveCount(0);
    await expect(builder).toHaveAnnounced('Added template PLC');
    await expect(palette.plus).toBeFocused();
    // "This diagram" first, and the built-in templates last.
    await expect.soft(palette.groups.first()).toHaveText('This diagram');
    await expect.soft(palette.groups.last()).toHaveText('Built-in');
    // Each section's header shows its count after its name, and collapses
    // its list, and shows it again.
    const header = page.getByTestId('palette-section-diagram');

    await expect.soft(header).toHaveText('This diagram (1)');
    await expect.soft(header).toHaveAccessibleName('This diagram, 1 template');
    await expect.soft(header).toHaveAttribute('aria-expanded', 'true');
    await header.press('Enter');
    await expect.soft(header).toHaveAttribute('aria-expanded', 'false');
    await expect.soft(palette.diagram).toBeHidden();
    await header.press('Enter');
    await expect.soft(header).toHaveAttribute('aria-expanded', 'true');
    await expect.soft(palette.diagram).toBeVisible();
    await palette.plus.focus();
    await expect
      .soft(palette.diagram)
      .toHaveAttribute('aria-label', 'This diagram');

    await builder.persisted(draft, (doc) => doc.templates.length, 1);
    const doc = await builder.serverDocument(draft);

    const [saved] = doc.templates;
    expect(saved.id).toMatch(UUID);
    expect.soft(saved).toEqual(plcTemplate(saved.id));
    // The device it was made from is as it was.
    expect
      .soft(byHostname(doc, 'plc-01').device.spec)
      .toEqual(plcSpec('plc-01'));

    const entry = palette.entry(saved.id);

    await expect.soft(entry).toHaveAccessibleName('Add PLC device');
    await expect.soft(entry).toHaveAccessibleDescription('Plant controller');
    await expect.soft(entry).toHaveText('PLC');
    await expect
      .soft(palette.actions(saved.id))
      .toHaveAccessibleName('Actions for template PLC');

    // One edit: Undo takes the template out again, and Redo puts it back.
    await builder.toolbar('undo').click();
    await expect(builder).toHaveAnnounced('Added template PLC');
    await expect(palette.diagram).toHaveCount(0);
    await expect
      .soft(palette.groups.filter({ hasText: 'This diagram' }))
      .toHaveCount(0);
    await builder.toolbar('redo').click();
    await expect(palette.entry(saved.id)).toBeVisible();

    // The browser keeps a collapsed section when the draft opens again.
    await builder.persisted(draft, (doc) => doc.templates.length, 1);
    await builder.waitSaved();
    await header.press('Enter');
    await expect.soft(header).toHaveAttribute('aria-expanded', 'false');
    await page.reload();
    await builder.openDraft(draft);
    await expect.soft(header).toHaveAttribute('aria-expanded', 'false');
    await expect.soft(palette.diagram).toBeHidden();
  });

  expectNoFatal(issues);
});

test(
  'a template of the diagram makes devices with its fields, from the palette and the command palette, and travels with the diagram',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const palette = templatePalette(page);
    // The template made from plc-01 in the test above.
    const saved = plcTemplate(crypto.randomUUID());
    const draft = await openPlant(builder, testInfo, { templates: [saved] });
    await expect(palette.entry(saved.id)).toBeVisible();

    await test.step('its entry adds a device with the template’s fields, by a click and by a drag', async () => {
      const entry = palette.entry(saved.id);

      await entry.focus();
      await expect.soft(palette.tooltip).toHaveText('Plant controller');
      await page.keyboard.press('Enter');
      await expect(builder.nodes('device')).toHaveCount(2);
      await entry.dragTo(page.locator('.vue-flow__pane'), {
        targetPosition: { x: 240, y: 520 },
      });
      await expect(builder.nodes('device')).toHaveCount(3);
      await builder.persisted(draft, hostnames, ['plc-01', 'plc', 'plc-2']);

      const doc = await builder.serverDocument(draft);

      for (const hostname of ['plc', 'plc-2']) {
        const { device } = byHostname(doc, hostname);

        expect.soft(device, hostname).toMatchObject({
          hostname,
          iconKey: 'linux',
          outlineColor: '#2f6fbf',
          fillColor: '#ffeecc',
          spec: plcSpec(hostname, 'PLANT'),
        });
        expect
          .soft(
            device.interfaces.map((handle) => handle.name),
            `${hostname} handles`,
          )
          .toEqual(['eth0']);
        // The template's own description is not the node's.
        expect.soft(JSON.stringify(device)).not.toContain('Plant controller');
      }
      // Neither is connected: only plc-01 is.
      expect.soft(doc.edges).toHaveLength(1);
      // The template is as it was saved.
      expect.soft(doc.templates).toEqual([saved]);
      await expect.soft(builder.node('plc-2', 'device')).toBeVisible();
    });

    await test.step('the command palette lists it under Add device, and offers New device template', async () => {
      const commands = page.getByTestId('commands-dialog');
      const field = page.getByRole('combobox', {
        name: /Search commands|choose a/,
      });
      const first = commands.getByRole('option', { selected: true });

      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+k');
      await field.fill('new device template');
      await expect.soft(first).toContainText('New device template');
      await field.fill('add device');
      await expect(first).toContainText('Add device');
      await page.keyboard.press('Enter');
      await field.fill('PLC');
      await expect(first).toContainText('This diagram · plc.qc2');
      await page.keyboard.press('Enter');
      await expect(commands).toHaveCount(0);
      await expect(builder.nodes('device')).toHaveCount(4);
      await builder.persisted(draft, hostnames, [
        'plc-01',
        'plc',
        'plc-2',
        'plc-3',
      ]);
    });

    await test.step('the template is there after a reload, in the download, and in a draft made by uploading it', async () => {
      await builder.editAndReload(draft, {
        expectSaved: () => builder.waitSaved(),
        expectAfterReload: () => expect(palette.entry(saved.id)).toBeVisible(),
      });

      const dialog = await builder.openDialog('download');
      const file = await download(page, () =>
        dialog.getByTestId('download-json').click(),
      );

      expect
        .soft(JSON.parse(file.buffer.toString('utf8')).templates)
        .toEqual([saved]);
      await dialog.getByRole('button', { name: 'Close', exact: true }).click();
      await expect(builder.dialog).toBeHidden();

      const upload = await builder.openDialog('upload');

      await upload.getByTestId('upload-file').setInputFiles({
        name: file.name,
        mimeType: 'application/json',
        buffer: file.buffer,
      });
      const created = waitForApi(page, 'POST', '/builder/drafts');
      await upload.getByTestId('upload-submit').click();
      const uploaded = await (await created).json();

      await expect(builder.dialog).toBeHidden();
      expect.soft(uploaded.id, 'a new draft').not.toBe(draft.id);
      await expect(palette.entry(saved.id)).toBeVisible();
      await palette.entry(saved.id).click();
      await builder.persisted(
        uploaded,
        (doc) => hostnames(doc).at(-1),
        'plc-4',
      );
      expect
        .soft((await builder.serverDocument(uploaded)).templates)
        .toEqual([saved]);
    });

    expectNoFatal(issues);
  },
);

test(
  'a template of the diagram is edited and deleted from its menu, by keyboard, and the editor asks before it drops changes',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    // A long name, which the entry's menu button must still sit beside.
    const rtu = template('Field remote terminal unit', {
      description: 'Remote terminal unit',
      device: { iconKey: 'router', spec: plcSpec('rtu', '') },
    });
    const hmi = template('HMI');
    const draft = await builder.seedDraft(
      plantDocument(uniqueName(testInfo, 'templates-edit'), {
        templates: [rtu, hmi],
      }),
    );
    const palette = templatePalette(page);
    const editor = templateEditor(page);

    await builder.openDraft(draft);
    await expect(palette.diagram.locator('.builder-palette__item')).toHaveText([
      'Field remote terminal unit',
      'HMI',
    ]);

    await test.step('a device made before the template changes stays as it is', async () => {
      await palette.entry(rtu.id).click();
      await builder.persisted(draft, hostnames, ['plc-01', 'rtu']);
    });

    await test.step('the menu of a template opens the editor on it, by keyboard', async () => {
      const actions = palette.actions(rtu.id);

      await palette.entry(rtu.id).focus();
      await page.keyboard.press('Tab');
      await expect(actions).toBeFocused();
      await expect.soft(actions).toHaveAttribute('aria-haspopup', 'menu');
      // The button sits on the row of the long name, and its menu opens
      // inside the window.
      const row = await palette.entry(rtu.id).boundingBox();
      const button = await actions.boundingBox();

      expect
        .soft(button.y, 'menu button on the entry row')
        .toBeCloseTo(row.y, 0);
      expect.soft(button.x).toBeGreaterThan(row.x + row.width - 1);
      await page.keyboard.press('Enter');
      await expect(palette.action(rtu.id, 'edit')).toBeFocused();
      const menu = page.getByTestId(`palette-template-actions-${rtu.id}-menu`);

      await expect.soft(menu).toHaveText('EditSave to libraryDelete');
      expect
        .soft((await menu.boundingBox()).x, 'menu inside the window')
        .toBeGreaterThanOrEqual(0);
      await page.keyboard.press('Enter');

      await expect(editor.dialog).toBeVisible();
      await expect
        .soft(editor.dialog)
        .toHaveAccessibleName('Edit template Field remote terminal unit');
      await expect(editor.name).toBeFocused();
      await expect.soft(editor.name).toHaveValue('Field remote terminal unit');
      await expect.soft(editor.description).toHaveValue('Remote terminal unit');
      await expect.soft(editor.save).toHaveText('Save');
      await expect.soft(editor.input('hostname')).toHaveValue('rtu');
      await expect.soft(editor.nameHint).toHaveText('');
    });

    await test.step('Tab goes from the template’s fields through the form to the buttons, and wraps', async () => {
      await page.keyboard.press('Tab');
      await expect(editor.description).toBeFocused();
      await page.keyboard.press('Tab');
      await expect(editor.input('hostname')).toBeFocused();
      // A field with keyboard focus shows its description. Escape hides
      // that, and the dialog stays.
      await expect(editor.tooltip).toHaveText(
        'Name new devices start from. Builder adds a number when another device has the name.',
      );
      await page.keyboard.press('Escape');
      await expect(editor.tooltip).toHaveCount(0);
      await expect(editor.dialog).toBeVisible();
      await expect.soft(editor.input('hostname')).toBeFocused();
      await expect.soft(editor.question).toBeHidden();

      // Backwards from the name: the dialog's Close, then its last button.
      await editor.name.focus();
      await page.keyboard.press('Shift+Tab');
      await expect(
        editor.dialog.getByRole('button', { name: 'Close dialog' }),
      ).toBeFocused();
      await page.keyboard.press('Shift+Tab');
      await expect(editor.save).toBeFocused();
      await page.keyboard.press('Shift+Tab');
      await expect(editor.cancel).toBeFocused();
      await page.keyboard.press('Tab');
      await page.keyboard.press('Tab');
      await expect(
        editor.dialog.getByRole('button', { name: 'Close dialog' }),
      ).toBeFocused();
    });

    await test.step('with nothing changed Escape closes it, and focus goes back to the menu', async () => {
      await page.keyboard.press('Escape');
      await expect(editor.dialog).toHaveCount(0);
      await expect(palette.actions(rtu.id)).toBeFocused();
      expect.soft(await builder.serverDocument(draft)).toMatchObject({
        templates: [rtu, hmi],
      });
    });

    await test.step('with changes, Escape, Cancel and a click outside ask first', async () => {
      await page.keyboard.press('Enter');
      await page.keyboard.press('Enter');
      await expect(editor.name).toBeFocused();
      await editor.name.fill('hmi');
      // Another template of the diagram has the name: said, not refused.
      await expect(editor.nameHint).toHaveText(
        'Another template has this name.',
      );
      await expect
        .soft(editor.name)
        .toHaveAccessibleDescription('Another template has this name.');

      await page.keyboard.press('Escape');
      await expect(editor.question).toBeVisible();
      await expect
        .soft(editor.question)
        .toContainText('Discard changes to this template?');
      await expect(editor.keep).toBeFocused();
      await expect
        .soft(editor.keep)
        .toHaveAccessibleDescription('Discard changes to this template?');
      await expect.soft(editor.save).toBeHidden();
      // Escape again keeps editing, where the user was.
      await page.keyboard.press('Escape');
      await expect(editor.question).toBeHidden();
      await expect(editor.name).toBeFocused();
      await expect.soft(editor.name).toHaveValue('hmi');

      await editor.cancel.click();
      await expect(editor.keep).toBeFocused();
      await editor.keep.click();
      await expect(editor.cancel).toBeFocused();

      // A change in the form alone counts too.
      await editor.name.fill('Field remote terminal unit');
      await editor.cancel.click();
      await expect(editor.dialog).toHaveCount(0);
      await palette.actions(rtu.id).click();
      await palette.action(rtu.id, 'edit').click();
      await editor.input('spec.hardware.memory').fill('4096');
      await page.mouse.click(4, 450);
      await expect(editor.question).toBeVisible();
      await editor.discard.click();
      await expect(editor.dialog).toHaveCount(0);
      await expect.soft(palette.actions(rtu.id)).toBeFocused();
      expect
        .soft((await builder.serverDocument(draft)).templates)
        .toEqual([rtu, hmi]);
    });

    await test.step('Save changes the template, as one edit, and no device made from it', async () => {
      await palette.actions(rtu.id).click();
      await palette.action(rtu.id, 'edit').click();
      await editor.name.fill('Field RTU');
      await editor.description.fill('');
      await editor.input('hostname').fill('field-rtu');
      await editor.input('spec.hardware.memory').fill('4096');
      await editor.field('iconKey').getByTestId('inspector-icon').click();
      await editor
        .field('iconKey')
        .getByRole('menuitemradio', { name: 'Firewall', exact: true })
        .click();
      await editor.save.click();
      await expect(editor.dialog).toHaveCount(0);
      await expect(builder).toHaveAnnounced('Updated template Field RTU');
      await expect.soft(palette.actions(rtu.id)).toBeFocused();
      await expect(
        palette.diagram.locator('.builder-palette__item'),
      ).toHaveText(['Field RTU', 'HMI']);
      await expect
        .soft(palette.entry(rtu.id))
        .toHaveAccessibleName('Add Field RTU device');
      // It has no description now, so none to show or point at.
      await expect
        .soft(palette.entry(rtu.id))
        .not.toHaveAttribute('aria-describedby');

      await builder.persisted(
        draft,
        (doc) => doc.templates[0].name,
        'Field RTU',
      );
      const doc = await builder.serverDocument(draft);
      const spec = plcSpec('field-rtu', '');

      spec.hardware.memory = 4096;
      expect.soft(doc.templates).toEqual([
        {
          id: rtu.id,
          name: 'Field RTU',
          device: { iconKey: 'firewall', spec },
        },
        hmi,
      ]);
      expect.soft(byHostname(doc, 'rtu').device).toMatchObject({
        iconKey: 'router',
        spec: plcSpec('rtu', ''),
      });

      await palette.entry(rtu.id).click();
      await builder.persisted(draft, hostnames, ['plc-01', 'rtu', 'field-rtu']);
    });

    await test.step('Delete removes it at once, focus goes to the next entry, and Undo brings it back', async () => {
      await palette.actions(rtu.id).focus();
      await page.keyboard.press('ArrowUp');
      await expect(palette.action(rtu.id, 'delete')).toBeFocused();
      await page.keyboard.press('Enter');
      await expect(builder).toHaveAnnounced('Deleted template Field RTU');
      await expect(palette.entry(rtu.id)).toHaveCount(0);
      await expect(palette.entry(hmi.id)).toBeFocused();
      await expect.soft(builder.dialog).toHaveCount(0);
      await builder.persisted(
        draft,
        (doc) => doc.templates.map((entry) => entry.name),
        ['HMI'],
      );
      // The devices made from it stay.
      expect
        .soft(hostnames(await builder.serverDocument(draft)))
        .toEqual(['plc-01', 'rtu', 'field-rtu']);

      // The last one: focus goes to "+", and the group's name with it.
      await palette.actions(hmi.id).click();
      await palette.action(hmi.id, 'delete').click();
      await expect(palette.diagram).toHaveCount(0);
      await expect(palette.plus).toBeFocused();
      await expect
        .soft(palette.groups.filter({ hasText: 'This diagram' }))
        .toHaveCount(0);
      await builder.persisted(draft, (doc) => doc.templates ?? null, null);

      await builder.toolbar('undo').click();
      await expect(palette.entry(hmi.id)).toBeVisible();
      await builder.toolbar('undo').click();
      await expect(
        palette.diagram.locator('.builder-palette__item'),
      ).toHaveText(['Field RTU', 'HMI']);
    });

    expectNoFatal(issues);
  },
);

test('a template keeps its colors and names its custom icon, which the diagram’s copy or the icon library draws', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const plc = iconOf(pngOf(32, 16, ownColor()), 'plc');
  const pump = iconOf(pngOf(16, 32, ownColor()), 'pump');
  const src = (icon) => `data:image/png;base64,${icon.data}`;
  // The diagram carries a copy of plc, which the icon library lacks.
  const draft = await builder.seedDraft(
    plantDocument(uniqueName(testInfo, 'templates-icons'), {
      look: { icon: plc.name, fillColor: '#1b2330' },
      icons: { [plc.name]: plc.entry },
    }),
  );
  const palette = templatePalette(page);
  const editor = templateEditor(page);
  const customIcon = (scope) => scope.locator('img.builder-icon--custom');
  const iconField = editor.field('icon');
  // The icon library the Builder reads: one icon, which the diagram does
  // not carry. No test's icons are touched.
  const listed = [
    {
      name: pump.name,
      id: pump.id,
      owner: 'alice',
      width: 16,
      height: 32,
      bytes: Buffer.from(pump.data, 'base64').length,
      created: '2026-10-02T12:00:00Z',
      updated: '2026-10-02T12:00:00Z',
      aliases: [],
      data: pump.data,
      canRename: false,
      canDelete: false,
    },
  ];

  await routeIconLibrary(page, listed);

  await builder.openDraft(draft);

  let saved;

  await test.step('a template made from a device with an icon and a fill has both', async () => {
    await builder.selectNode('plc-01');
    await palette.plus.click();
    await expect(editor.dialog).toBeVisible();
    await expect(customIcon(iconField)).toHaveAttribute('src', src(plc));
    await expect
      .soft(iconField.getByTestId('inspector-icon-name'))
      .toHaveText(plc.name);
    await expect
      .soft(iconField.getByTestId('inspector-icon'))
      .toHaveAccessibleDescription(
        "Icon drawn on the canvas and under Templates. Choose Custom icon… to use an image of the server's icon library.",
      );
    await editor.name.fill('PLC');
    await editor.save.click();
    await expect(editor.dialog).toHaveCount(0);

    await builder.persisted(draft, (doc) => doc.templates.length, 1);
    const doc = await builder.serverDocument(draft);

    [saved] = doc.templates;
    expect.soft(saved.device).toMatchObject({
      icon: plc.name,
      fillColor: '#1b2330',
    });
    expect.soft(doc.icons).toEqual({ [plc.name]: plc.entry });
    // The palette draws the template's icon, from the diagram's copy.
    await expect(customIcon(palette.entry(saved.id))).toHaveAttribute(
      'src',
      src(plc),
    );
  });

  await test.step('the diagram keeps its copy while only the template names it', async () => {
    await builder.selectNode('plc-01');
    await builder.toolbar('delete').click();
    await builder.persisted(draft, hostnames, []);

    const doc = await builder.serverDocument(draft);

    expect.soft(doc.icons).toEqual({ [plc.name]: plc.entry });
    expect.soft(doc.templates).toEqual([saved]);
    await expect
      .soft(customIcon(palette.entry(saved.id)))
      .toHaveAttribute('src', src(plc));

    await palette.entry(saved.id).click();
    await builder.persisted(draft, hostnames, ['plc-01']);
    await expect(customIcon(builder.node('plc-01', 'device'))).toHaveAttribute(
      'src',
      src(plc),
    );
    expect
      .soft(byHostname(await builder.serverDocument(draft), 'plc-01').device)
      .toMatchObject({ icon: plc.name, fillColor: '#1b2330' });
  });

  await test.step('an icon of the library chosen in the editor is the template’s once it is saved, and the diagram carries no copy of it', async () => {
    await palette.actions(saved.id).click();
    await palette.action(saved.id, 'edit').click();
    // Custom icon…, in the Icon field's menu, opens the dialog.
    await iconField.getByTestId('inspector-icon').click();
    await iconField.getByTestId('inspector-icon-item-custom-icon').click();

    const icons = page.getByTestId('icon-dialog');

    await expect(icons).toBeVisible();
    // The copy of the template's own icon, not the diagram's others.
    await expect
      .soft(icons.getByTestId('icon-diagram-list').locator('[data-icon]'))
      .toHaveCount(1);
    // Another user's icon has Use alone.
    const row = icons
      .getByTestId('icon-library-list')
      .locator(`[data-icon="${pump.name}"]`);

    await expect.soft(row.getByTestId('icon-rename')).toHaveCount(0);
    await row.getByTestId('icon-use').click();
    await expect(icons).toHaveCount(0);
    await expect(editor.dialog).toBeVisible();
    await expect(customIcon(iconField)).toHaveAttribute('src', src(pump));
    await expect
      .soft(iconField.getByTestId('inspector-icon-name'))
      .toHaveText(pump.name);
    // Nothing of the diagram changed yet.
    expect
      .soft((await builder.serverDocument(draft)).templates)
      .toEqual([saved]);

    await editor.field('fillColor').locator('input[type="text"]').fill('');
    await editor
      .field('outlineColor')
      .locator('input[type="text"]')
      .fill('#ffd400');
    await editor.save.click();
    await expect(editor.dialog).toHaveCount(0);
    await expect(builder).toHaveAnnounced('Updated template PLC');

    await builder.persisted(
      draft,
      (doc) => doc.templates[0].device.icon,
      pump.name,
    );
    const doc = await builder.serverDocument(draft);

    expect.soft(doc.templates[0].device).toMatchObject({
      icon: pump.name,
      outlineColor: '#ffd400',
    });
    expect.soft(doc.templates[0].device).not.toHaveProperty('fillColor');
    // The device made before keeps the first icon, whose copy stays. The
    // library draws the template's.
    expect.soft(doc.icons).toEqual({ [plc.name]: plc.entry });
    await expect(customIcon(palette.entry(saved.id))).toHaveAttribute(
      'src',
      src(pump),
    );
  });

  await test.step('deleting the template leaves the device’s copy, and Undo brings the template back', async () => {
    await palette.actions(saved.id).click();
    await palette.action(saved.id, 'delete').click();
    await builder.persisted(draft, (doc) => doc.templates ?? null, null);
    expect
      .soft((await builder.serverDocument(draft)).icons)
      .toEqual({ [plc.name]: plc.entry });

    await builder.toolbar('undo').click();
    await builder.persisted(
      draft,
      (doc) => doc.templates?.[0]?.device.icon ?? null,
      pump.name,
    );
    await expect(customIcon(palette.entry(saved.id))).toHaveAttribute(
      'src',
      src(pump),
    );
  });

  expectNoFatal(issues);
});

test('a new template starts from a plain device when no device is selected, and a read-only diagram offers none', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const palette = templatePalette(page);
  const editor = templateEditor(page);

  await builder.open();
  const draft = await builder.createBlank();

  await test.step('with nothing selected, the editor starts from the plain device, with no name', async () => {
    await palette.plus.click();
    await expect(editor.name).toBeFocused();
    await expect.soft(editor.name).toHaveValue('');
    await expect.soft(editor.input('hostname')).toHaveValue('device');
    await expect
      .soft(editor.field('iconKey').getByTestId('inspector-icon-name'))
      .toHaveText('Server');
    await expect
      .soft(editor.input('spec.hardware.drives.0.image'))
      .toHaveValue('ubuntu.qc2');
    // Nothing was changed, so Cancel asks nothing.
    await editor.cancel.click();
    await expect(editor.dialog).toHaveCount(0);
    await expect(palette.plus).toBeFocused();
  });

  await test.step('the template is too large to save past 16 KiB, and says so', async () => {
    await palette.plus.click();
    await editor.name.fill('Large');
    await editor.input('spec.general.description').fill('x'.repeat(16 * 1024));
    await editor.save.click();
    await expect(editor.error).toHaveText(
      'This template is too large to save (16 KiB at most). Remove some of its settings.',
    );
    await expect.soft(editor.dialog).toBeVisible();
    await editor.input('spec.general.description').fill('Small again');
    await editor.save.click();
    await expect(editor.dialog).toHaveCount(0);
    await builder.persisted(
      draft,
      (doc) => doc.templates.map((entry) => entry.name),
      ['Large'],
    );
  });

  await test.step('a published diagram shown read only lists its templates, and offers no change to them', async () => {
    const name = uniqueName(testInfo, 'templates-published');
    const rtu = template('RTU');
    const published = await publishTopology(
      builder.request,
      builder.tracker,
      name,
      plantDocument(name, { templates: [rtu] }),
    );

    await builder.backToDrafts();
    await page.getByTestId('drafts-tab-published').click();
    await page.getByTestId(`draft-open-${published.documentId}`).click();
    await expect(builder.canvas).toBeVisible();
    await expect(page.getByTestId('published-edit')).toBeVisible();

    await expect(palette.entry(rtu.id)).toBeDisabled();
    await expect
      .soft(palette.actions(rtu.id))
      .toHaveAttribute('aria-disabled', 'true');
    await expect.soft(palette.plus).toHaveAttribute('aria-disabled', 'true');
    await expect
      .soft(palette.plus)
      .toHaveAccessibleDescription('This diagram is read only.');
    await palette.plus.focus();
    await expect.soft(palette.tooltip).toHaveText('This diagram is read only.');
    // It keeps focus, and does nothing.
    await page.keyboard.press('Enter');
    await expect.soft(editor.dialog).toHaveCount(0);
    await expect.soft(palette.plus).toBeFocused();
    await palette.actions(rtu.id).focus();
    await page.keyboard.press('Enter');
    await expect.soft(palette.action(rtu.id, 'edit')).toHaveCount(0);
  });

  expectNoFatal(issues);
});

// --- the library --------------------------------------------------------------

// The device of a library template the tests make: a plain Linux machine
// named `hostname`, with one drive.
function unitDevice(hostname, init = {}) {
  return {
    iconKey: 'server',
    ...init,
    spec: {
      type: 'VirtualMachine',
      general: { hostname, description: '', vm_type: 'kvm' },
      hardware: { os_type: 'linux', drives: [{ image: 'unit.qc2' }] },
      network: { interfaces: [] },
    },
  };
}

// The page's next answer to a change of the library: `tail` is the end of
// its path after the library's ("items", "collections/<id>", "delete").
function libraryAnswer(page, method, tail) {
  return page.waitForResponse((response) => {
    const { pathname } = new URL(response.url());

    return (
      response.request().method() === method &&
      pathname.endsWith(`/builder/templates/${tail}`)
    );
  });
}

// The templates of the library with these ids, as the server lists them.
async function listedTemplates(request, ids) {
  const { templates } = await readLibrary(request);

  return ids.map((id) => templates.find((entry) => entry.id === id) || null);
}

// Makes a collection `name` of `templateIds` through the API and schedules it
// for deletion. Returns its id.
async function seedCollection(
  request,
  tracker,
  { name, description = '', templateIds },
) {
  const response = await libraryChange(
    request,
    'post',
    `${LIBRARY}/collections`,
    { data: { name, description, templateIds } },
  );
  expect(response.ok(), await response.text()).toBeTruthy();
  const { id } = await response.json();
  tracker.collection(id);

  return id;
}

test(
  'the Node Templates tab lists the library in Tab order, and the built-in templates read only, which Duplicate copies into the library',
  { tag: '@cross-browser' },
  async ({ page, builder, request, tracker, issues }, testInfo) => {
    const library = templateLibrary(page);
    const name = uniqueName(testInfo, 'tab');
    const {
      ids: [id],
    } = await seedTemplates(request, tracker, [
      { name, description: 'Layer 3 unit', device: unitDevice('unit') },
    ]);

    await builder.open();

    await test.step('the tab lists the library’s templates, each with Edit and Delete', async () => {
      await expect(library.tab).toHaveText(/^Node Templates \(\d+\)$/);
      await library.tab.click();
      await expect(library.tab).toHaveAttribute('aria-selected', 'true');
      await expect(library.list).toBeVisible();
      await expect(library.card(id)).toBeVisible();
      await expect.soft(library.card(id).getByRole('heading')).toHaveText(name);
      await expect.soft(library.card(id)).toContainText('Layer 3 unit');
      await expect
        .soft(library.edit(id))
        .toHaveAccessibleName(`Edit template ${name}`);
      await expect
        .soft(library.remove(id))
        .toHaveAccessibleName(`Delete template ${name}`);
      await expect
        .soft(library.select(id))
        .toHaveAccessibleName(`Select ${name}`);
      // A template of the library is not read only.
      await expect
        .soft(library.card(id).getByText('Read only', { exact: true }))
        .toHaveCount(0);
      await expect.soft(library.show).toHaveValue('');
      await expect
        .soft(library.show.locator('option:checked'))
        .toHaveText('All library templates');
      await expect
        .soft(library.panel.getByLabel('Show', { exact: true }))
        .toHaveValue('');
      await expect.soft(library.count).toHaveText(/^0 of \d+ selected$/);
      // Edit is blue with a pencil, and Delete red with a trash can.
      await expect
        .soft(library.edit(id))
        .toHaveClass(/\bbuilder-button--primary\b/);
      await expect
        .soft(library.edit(id).locator('.builder-icon--pencil'))
        .toHaveCount(1);
      await expect
        .soft(library.remove(id))
        .toHaveClass(/\bbuilder-button--danger\b/);
      await expect
        .soft(library.remove(id).locator('.builder-icon--trash'))
        .toHaveCount(1);
      // The card's buttons are one size, as a draft card's are.
      const [edit, remove] = await Promise.all(
        [library.edit(id), library.remove(id)].map((button) =>
          button.boundingBox(),
        ),
      );

      expect.soft(Math.abs(edit.width - remove.width)).toBeLessThan(1);
      expect.soft(Math.abs(edit.height - remove.height)).toBeLessThan(1);
    });

    await test.step('Tab goes through what makes more, Show and the row above the cards, then into the first card', async () => {
      // Other tests add templates at the same time, so the first card is
      // whichever the library holds first.
      const card = library.list.locator('.builder-card').first();

      await library.tab.focus();
      for (const control of [
        library.newTemplate,
        library.newCollection,
        page.getByTestId('templates-import'),
        library.show,
        library.filter,
        library.all,
        library.collect,
        page.getByTestId('bulk-export-templates'),
        library.bulkDelete,
        card,
        card.getByRole('checkbox'),
        card.getByRole('button', { name: /^Edit template/ }),
        card.getByRole('button', { name: /^Delete template/ }),
        card.getByRole('button', { name: /^Download template/ }),
      ]) {
        await page.keyboard.press('Tab');
        await expect(control).toBeFocused();
      }
    });

    await test.step('Filter templates shows the cards that match by name or by a field, and says how many', async () => {
      await expect
        .soft(library.filter)
        .toHaveAccessibleName('Filter templates');
      await library.filter.fill(name);
      await expect(library.list.locator('.builder-card')).toHaveCount(1);
      await expect.soft(library.card(id)).toBeVisible();
      await expect
        .soft(library.filterCount)
        .toHaveText(/^1 of \d+ templates? shown\.$/);
      await expect
        .soft(library.filter)
        .toHaveAccessibleDescription(/^1 of \d+ templates? shown\.$/);
      // Select all acts on the cards shown.
      await library.all.check();
      await expect.soft(library.count).toHaveText('1 of 1 selected');
      await library.all.uncheck();
      // A field that t: in the palette searches finds it too: its
      // description.
      await library.filter.fill(`Layer 3 unit ${name}`);
      await expect.soft(library.card(id)).toBeVisible();
      await library.filter.fill('no template has this name');
      await expect
        .soft(page.getByTestId('templates-filter-none'))
        .toHaveText('No templates match the filter.');
      await library.filter.press('Escape');
      await expect(library.filter).toHaveValue('');
      await expect.soft(library.card(id)).toBeVisible();
    });

    await test.step('Show lists the built-in templates: read only, with View, Duplicate and Download', async () => {
      await library.show.selectOption({ label: 'Built-in templates' });
      await expect(library.builtinNote).toHaveText(
        'Built-in templates are read only. To change one, select Duplicate. This adds a copy to the library.',
      );
      for (const builtin of BUILTIN_TEMPLATE_IDS) {
        await expect
          .soft(library.roCard('builtin', builtin), builtin)
          .toBeVisible();
      }
      await expect(library.list.locator('.builder-card')).toHaveCount(
        BUILTIN_TEMPLATE_IDS.length,
      );

      const router = library.roCard('builtin', 'router');

      await expect.soft(router.getByRole('heading')).toHaveText(/Router/);
      await expect
        .soft(library.roTag('builtin', 'router'))
        .toHaveText('Read only');
      await expect.soft(router).toContainText('Layer 3 router');
      await expect
        .soft(router.getByRole('button'))
        .toHaveText(['View', 'Duplicate', 'Download']);
      await expect
        .soft(library.view('builtin', 'router'))
        .toHaveAccessibleName('View template Router');
      await expect
        .soft(library.view('builtin', 'router').locator('.builder-icon--eye'))
        .toHaveCount(1);
      await expect
        .soft(
          library.duplicate('builtin', 'router').locator('.builder-icon--copy'),
        )
        .toHaveCount(1);
      await expect
        .soft(library.duplicate('builtin', 'router'))
        .toHaveAccessibleName('Duplicate template Router to the library');
      await expect.soft(library.bulkDelete).toHaveCount(0);
      await expect.soft(library.collect).toHaveCount(0);
      await expect
        .soft(library.bar)
        .toHaveAccessibleName('Bulk actions: Built-in templates');
    });

    await test.step('View opens the built-in template read only, with Duplicate to library', async () => {
      const editor = templateEditor(page);

      await library.view('builtin', 'router').click();
      await expect(editor.dialog).toBeVisible();
      await expect
        .soft(editor.dialog.getByRole('heading', { level: 2 }))
        .toHaveText('Template Router');
      await expect
        .soft(editor.dialog.getByTestId('template-origin'))
        .toHaveText('A built-in template, which is read only');
      await expect.soft(editor.name).not.toBeEditable();
      await expect.soft(editor.cancel).toHaveText('Close');
      await expect.soft(editor.save).toHaveText('Duplicate to library');
      await page.keyboard.press('Escape');
      await expect(editor.dialog).toHaveCount(0);
      await expect(library.view('builtin', 'router')).toBeFocused();
    });

    await test.step('Duplicate adds a copy to the library, which can change, and the built-in template stays as it is', async () => {
      const added = libraryAnswer(page, 'POST', 'items');

      await library.duplicate('builtin', 'router').click();

      const answer = await added;

      expect(answer.status()).toBe(201);
      const [{ id: copy }] = (await answer.json()).created;

      await expect(builder).toHaveAnnounced(
        'Added a copy of Router to the library.',
      );

      const stored = await readLibrary(request);
      const builtin = stored.builtins.find((entry) => entry.id === 'router');

      expect
        .soft(stored.templates.find((entry) => entry.id === copy))
        .toMatchObject({
          source: 'library',
          name: 'Router',
          device: builtin.device,
          version: 1,
        });
      expect.soft(builtin).toMatchObject({ source: 'builtin', version: 1 });

      await library.show.selectOption('');
      await expect(library.card(copy)).toBeVisible();
      await expect.soft(library.edit(copy)).toBeVisible();

      // The server refuses a change of a built-in template.
      const refused = await request.put(`${LIBRARY}/items/router`, {
        headers: { 'If-Match': '"1"' },
        data: { name: 'Router', description: '', device: builtin.device },
      });

      expect.soft(refused.status()).toBe(409);
      expect.soft((await refused.json()).code).toBe('template.item.read-only');
    });

    expectNoFatal(issues);
  },
);

test(
  'a template of the library is made and changed in the large editor, which keeps what is typed when the server refuses it',
  { tag: '@cross-browser' },
  async ({ page, builder, request, issues }, testInfo) => {
    const name = uniqueName(testInfo, 'node');
    const library = templateLibrary(page);
    const editor = templateEditor(page);
    const title = editor.dialog.getByRole('heading', { level: 2 });
    // Another template of the library, whose name the editor says is taken.
    const twin = uniqueName(testInfo, 'twin');

    await seedTemplates(request, builder.tracker, [
      { name: twin, description: '', device: unitDevice('twin') },
    ]);

    let id;

    await builder.open();
    await library.tab.click();
    await expect(library.list).toBeVisible();

    await test.step('New template opens the large editor on a plain device, and Save to library adds it', async () => {
      await library.newTemplate.click();
      await expect(editor.dialog).toBeVisible();
      await expect.soft(title).toHaveText('New library template');
      await expect.soft(editor.save).toHaveText('Save to library');
      await expect(editor.name).toBeFocused();
      await expect.soft(editor.name).toHaveValue('');
      await expect.soft(editor.input('hostname')).toHaveValue('device');

      const size = page.viewportSize();
      const box = await editor.dialog.boundingBox();

      expect.soft(box.width / size.width).toBeGreaterThanOrEqual(0.7);

      // A template needs a name, here as in a diagram.
      await editor.save.click();
      await expect(editor.error).toHaveText('Enter a name.');
      await expect.soft(editor.name).toBeFocused();

      await editor.name.fill(name);
      await editor.description.fill('First words');
      await editor.input('hostname').fill('node-a');

      // While the server saves, Save says so and waits, and the editor
      // stays: a second press, Escape and Cancel do nothing.
      const ITEMS = `**${LIBRARY}/items`;
      const added = libraryAnswer(page, 'POST', 'items');
      let sent = 0;
      let answerNow;
      const held = new Promise((resolve) => {
        answerNow = resolve;
      });

      // Held until the test lets it go. Requests after that go through.
      await page.route(ITEMS, async (route) => {
        sent += 1;
        await held;
        await route.fallback();
      });
      await editor.save.click();
      await expect(editor.save).toHaveAttribute('aria-busy', 'true');
      await expect
        .soft(editor.dialog.getByTestId('template-status'))
        .toHaveText('Saving the template…');
      await editor.save.press('Enter');
      await page.keyboard.press('Escape');
      await editor.cancel.click();
      await expect(editor.dialog).toBeVisible();
      await expect.soft(editor.question).toBeHidden();
      await expect.soft(editor.save).toHaveAttribute('aria-busy', 'true');
      answerNow();

      const answer = await added;

      expect(sent, 'requests to add the template').toBe(1);
      expect(answer.status()).toBe(201);
      // No id is sent: the server names the template.
      expect(answer.request().postDataJSON()).toEqual({
        templates: [
          {
            name,
            description: 'First words',
            device: expect.objectContaining({ iconKey: 'server' }),
          },
        ],
      });
      [{ id }] = (await answer.json()).created;
      expect(id).toMatch(UUID);

      await expect(editor.dialog).toHaveCount(0);
      await expect(builder).toHaveAnnounced(`Saved ${name} to the library.`);
      await expect(library.newTemplate).toBeFocused();
      await expect(library.card(id).getByRole('heading')).toHaveText(name);
      await expect.soft(library.card(id)).toContainText('First words');
      await expect
        .soft(page.getByTestId(`template-time-${id}`))
        .toHaveText(/^\s*Updated \S/);

      const [stored] = await listedTemplates(request, [id]);

      expect.soft(stored).toMatchObject({
        name,
        description: 'First words',
        source: 'library',
        version: 1,
        device: { iconKey: 'server' },
      });
      expect.soft(stored.device.spec.general.hostname).toBe('node-a');
    });

    await test.step('Edit opens it with its values, says a name that is taken, and Save replaces it', async () => {
      await library.edit(id).click();
      await expect(editor.dialog).toBeVisible();
      await expect.soft(title).toHaveText(`Edit template ${name}`);
      await expect.soft(editor.save).toHaveText('Save');
      await expect.soft(editor.name).toHaveValue(name);
      await expect.soft(editor.description).toHaveValue('First words');
      await expect.soft(editor.input('hostname')).toHaveValue('node-a');

      // The names of the library's templates, not of a diagram's.
      await editor.name.fill(twin);
      await expect(editor.nameHint).toHaveText(
        'Another template has this name.',
      );
      await editor.name.fill(name);
      await expect(editor.nameHint).toHaveText('');

      await editor.description.fill('Second words');

      const replaced = libraryAnswer(page, 'PUT', `items/${id}`);

      await editor.description.press('Enter');

      const answer = await replaced;

      expect(answer.status()).toBe(200);
      expect.soft(answer.request().headers()['if-match']).toBe('"1"');
      await expect(editor.dialog).toHaveCount(0);
      await expect(builder).toHaveAnnounced(`Updated template ${name}.`);
      await expect(library.edit(id)).toBeFocused();
      await expect(library.card(id)).toContainText('Second words');
      await expect.soft(library.card(id)).not.toContainText('First words');
    });

    await test.step('a template changed elsewhere since the editor opened is not replaced unasked', async () => {
      await library.edit(id).click();
      await expect(editor.dialog).toBeVisible();
      await editor.description.fill('Third words');

      // Another user, or another tab, saves it meanwhile.
      await changeTemplate(request, id, (listed) => ({
        ...listed,
        description: 'Elsewhere',
      }));

      const refused = libraryAnswer(page, 'PUT', `items/${id}`);

      await editor.save.click();
      expect((await refused).status()).toBe(412);
      await expect(editor.error).toHaveText(
        'This template changed after you opened it, by another user or in another window. To replace that version, select Save again. To keep it, select Cancel. Then open the template again.',
      );
      // Nothing typed is lost, and nothing was replaced.
      await expect.soft(editor.description).toHaveValue('Third words');
      expect
        .soft((await listedTemplates(request, [id]))[0].description)
        .toBe('Elsewhere');

      await editor.save.click();
      await expect(editor.dialog).toHaveCount(0);
      await expect(library.card(id)).toContainText('Third words');
      expect.soft((await listedTemplates(request, [id]))[0]).toMatchObject({
        description: 'Third words',
        version: 4,
      });
    });

    await test.step('a save the server refuses keeps the editor open, with why', async () => {
      await library.edit(id).click();
      await editor.description.fill('Too much');
      let refusing = true;

      await page.route(`**${LIBRARY}/items/*`, (route) =>
        refusing
          ? route.fulfill({
              status: 413,
              json: {
                message:
                  'a library takes at most 524288 bytes, and this one would take 600000',
              },
            })
          : route.fallback(),
      );
      await editor.save.click();
      await expect(editor.error).toHaveText(
        'Could not save the template. A library takes at most 524288 bytes, and this one would take 600000.',
      );
      await expect.soft(editor.description).toHaveValue('Too much');
      await expect.soft(editor.save).toHaveText('Save');
      refusing = false;

      // Cancel asks before it drops what was typed.
      await editor.cancel.click();
      await expect(editor.keep).toBeFocused();
      await editor.discard.click();
      await expect(editor.dialog).toHaveCount(0);
      await expect.soft(library.edit(id)).toBeFocused();
      await expect.soft(library.card(id)).toContainText('Third words');
    });

    expectNoFatal(issues);
  },
);

test(
  'a template of the library makes devices in a diagram, which keeps a linked copy, the library button goes to it, and Delete takes it out of the library only',
  { tag: '@cross-browser' },
  async ({ page, builder, request, tracker, issues }, testInfo) => {
    const name = uniqueName(testInfo, 'node');
    // A plain device's template, as New template makes it, named and
    // described, its hostname node-a.
    const {
      ids: [id],
    } = await seedTemplates(request, tracker, [
      {
        name,
        description: 'Third words',
        device: {
          iconKey: 'server',
          spec: {
            type: 'VirtualMachine',
            general: { hostname: 'node-a', description: '', vm_type: 'kvm' },
            hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
            network: { interfaces: [] },
          },
        },
      },
    ]);
    const draft = await builder.seedDraft(
      blankDocument(uniqueName(testInfo, 'library')),
    );
    const library = templateLibrary(page);
    const palette = templatePalette(page);

    await builder.open();

    await test.step('Templates lists it with the library, and its entry makes a device with its fields', async () => {
      await page.getByTestId('drafts-tab-drafts').click();
      await page.getByTestId(`draft-open-${draft.id}`).click();
      await expect(builder.canvas).toBeVisible();

      const entry = palette.own(id);

      await expect(entry).toBeVisible();
      await expect
        .soft(palette.library.locator(`[data-testid="palette-template-${id}"]`))
        .toHaveCount(1);
      await expect.soft(entry).toHaveAccessibleName(`Add ${name} device`);
      await expect.soft(entry).toHaveAccessibleDescription('Third words');
      // A library template has no menu: it is changed on the tab.
      await expect.soft(palette.actions(id)).toHaveCount(0);

      // The device comes with a copy of the template, in the same step,
      // linked to the library template.
      await entry.click();
      await expect(builder.nodes('device')).toHaveCount(1);
      await expect(builder).toHaveAnnounced(
        `Added device and template ${name}`,
      );
      await expect(
        palette.diagram.getByRole('button', { name: `Add ${name} device` }),
      ).toHaveCount(1);
      await builder.persisted(draft, (doc) => doc.templates?.length ?? 0, 1);

      const [copy] = (await builder.serverDocument(draft)).templates;

      expect.soft(copy).toMatchObject({
        name,
        description: 'Third words',
        source: { kind: 'library', id, version: 1 },
      });
      expect.soft(copy.source.digest).toMatch(/^sha256:[0-9a-f]{64}$/);

      // The command palette lists it under Add device, by its section.
      const commands = page.getByTestId('commands-dialog');
      const field = page.getByRole('combobox', {
        name: /Search commands|choose a/,
      });
      const first = commands.getByRole('option', { selected: true });

      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+k');
      await field.fill('add device');
      await expect(first).toContainText('Add device');
      await page.keyboard.press('Enter');
      await field.fill(name);
      // The diagram's copy comes first. It adds no second copy.
      await expect(first).toContainText('This diagram · ubuntu.qc2');
      await page.keyboard.press('Enter');
      await expect(commands).toHaveCount(0);
      await builder.persisted(draft, hostnames, ['node-a', 'node-a-2']);
      expect
        .soft((await builder.serverDocument(draft)).templates)
        .toHaveLength(1);
    });

    await test.step('the library button says where it goes, and goes there: the tab takes focus', async () => {
      await expect
        .soft(palette.libraryButton)
        .toHaveAccessibleName('Node Templates library');
      await palette.libraryButton.hover();
      await expect(palette.tooltip).toHaveText('Node Templates library');
      await page.mouse.move(700, 400);
      await builder.waitSaved();

      await palette.libraryButton.focus();
      await page.keyboard.press('Enter');
      await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
      await expect(library.tab).toHaveAttribute('aria-selected', 'true');
      await expect(library.tab).toBeFocused();
      await expect(library.card(id)).toBeVisible();
    });

    await test.step('Delete asks first, and takes the template out of the library, not out of the diagram', async () => {
      await library.remove(id).click();
      await expect(library.confirm).toBeVisible();
      await expect
        .soft(library.confirm)
        .toHaveAccessibleName(`Delete template ${name}?`);
      await expect
        .soft(library.confirm)
        .toHaveAccessibleDescription(
          'This removes the template from the library and its collections, for all users. Diagrams keep their copies of it. You cannot undo this.',
        );
      await expect.soft(library.accept).toHaveText('Delete template');
      await expect(library.cancel).toBeFocused();

      // Escape keeps it, and focus goes back to the card's Delete.
      await page.keyboard.press('Escape');
      await expect(library.confirm).toHaveCount(0);
      await expect(library.remove(id)).toBeFocused();
      await expect.soft(library.card(id)).toBeVisible();

      await page.keyboard.press('Enter');

      const deleted = libraryAnswer(page, 'POST', 'delete');

      await library.accept.click();

      const answer = await deleted;

      // Each at the tag the library listed.
      expect(answer.request().postDataJSON()).toEqual({
        templates: [{ id, etag: expect.stringMatching(/^"\d+"$/) }],
      });
      expect(await answer.json()).toEqual({
        deleted: { templates: 1, collections: 0 },
      });
      await expect(library.card(id)).toHaveCount(0);
      await expect(builder).toHaveAnnounced(`Deleted template ${name}.`);
      await expect(library.all).toBeFocused();

      const [gone] = await listedTemplates(request, [id]);

      expect(gone).toBeNull();

      const doc = await builder.serverDocument(draft);

      expect.soft(hostnames(doc)).toEqual(['node-a', 'node-a-2']);
      // The diagram keeps its copy, linked to the deleted template.
      expect.soft(doc.templates).toHaveLength(1);
      expect.soft(doc.templates[0].source).toMatchObject({ id });
    });

    expectNoFatal(issues);
  },
);

// A device of a template of the library at a version: version 1 has the
// drive image v1.qc2, and each later version its own.
function versionDevice(version) {
  const device = unitDevice('linked');

  device.spec.hardware.drives[0].image = `v${version}.qc2`;

  return device;
}

test(
  'a copy of a library template offers the newer version: Update replaces the copy in one undo step, and Unlink keeps a copy whose library template was deleted',
  { tag: '@cross-browser' },
  async ({ page, builder, request, tracker, issues }, testInfo) => {
    const name = uniqueName(testInfo, 'linked');
    const {
      ids: [id],
    } = await seedTemplates(request, tracker, [
      { name, description: 'Version one', device: versionDevice(1) },
    ]);
    const draft = await builder.seedDraft(
      blankDocument(uniqueName(testInfo, 'linked')),
    );
    const palette = templatePalette(page);
    const notice = builder.inspector.getByTestId('inspector-template-links');
    const confirm = page.getByTestId('builder-confirm');
    const accept = page.getByTestId('confirm-accept');
    // The library's next version of the template, through the API, as
    // another user would save it.
    const nextVersion = (version, description) =>
      changeTemplate(request, id, (listed) => ({
        ...listed,
        description,
        device: versionDevice(version),
      }));
    // The copy's source in a document, and its version.
    const source = (doc) => doc.templates?.[0]?.source ?? null;
    const version = (doc) => source(doc)?.version;
    let copy;

    await withoutCollections(page);
    await builder.openDraft(draft);

    await test.step('placing the library template adds a copy linked to it, which is current', async () => {
      await palette.own(id).click();
      await expect(builder).toHaveAnnounced(
        `Added device and template ${name}`,
      );
      await expect(palette.groups.first()).toHaveText('This diagram');
      await expect.soft(palette.groups.nth(1)).toHaveText(LIBRARY_SECTION);
      await builder.persisted(draft, (doc) => doc.templates?.length ?? 0, 1);
      [{ id: copy }] = (await builder.serverDocument(draft)).templates;
      expect.soft(source(await builder.serverDocument(draft))).toMatchObject({
        kind: 'library',
        id,
        version: 1,
      });
      await expect(palette.entry(copy)).toBeVisible();
      await expect.soft(palette.link(copy)).toHaveCount(0);
      await expect.soft(notice).toHaveCount(0);
      await builder.waitSaved();
    });

    await test.step('a newer version in the library is offered when the diagram opens again', async () => {
      await nextVersion(2, 'Version two');
      await page.reload();
      await builder.openDraft(draft);

      await expect(palette.link(copy)).toContainText('Update available');
      await expect
        .soft(palette.update(copy))
        .toHaveAccessibleName(`Update template ${name}`);
      await expect
        .soft(palette.entry(copy))
        .toHaveAccessibleDescription(
          'Version one The library has a newer version of this template (version 2). The diagram has version 1.',
        );
      // The template's menu offers it too.
      await palette.actions(copy).click();
      await expect
        .soft(
          page
            .getByTestId(`palette-template-actions-${copy}-menu`)
            .getByRole('menuitem'),
        )
        .toHaveText(['Edit', 'Update', 'Save to library', 'Unlink', 'Delete']);
      await page.keyboard.press('Escape');

      // The Inspector's Diagram section lists it, with Update.
      await expect(notice).toBeVisible();
      await expect
        .soft(notice.getByRole('heading', { level: 3 }))
        .toHaveText('Template updates');
      await expect
        .soft(notice.getByTestId('inspector-template-links-summary'))
        .toHaveText(
          'The library has a newer version of 1 template in this diagram. Devices made from these templates do not change.',
        );
      await expect
        .soft(notice.getByTestId(`inspector-template-update-${copy}`))
        .toHaveAccessibleName(`Update template ${name}`);
      await expect
        .soft(notice.getByTestId('inspector-template-update-all'))
        .toHaveCount(0);
    });

    await test.step('Update asks first, then copies the new version as one undo step, and the device stays as it is', async () => {
      await palette.update(copy).click();
      await expect(confirm).toBeVisible();
      await expect
        .soft(confirm)
        .toHaveAccessibleName(`Update template ${name}?`);
      await expect
        .soft(confirm)
        .toHaveAccessibleDescription(
          'The template in this diagram gets version 2 from the library. Devices made from the template do not change. You can undo this.',
        );
      await expect.soft(accept).toHaveText('Update template');
      await expect(page.getByTestId('confirm-cancel')).toBeFocused();
      // Escape changes nothing, and gives focus back.
      await page.keyboard.press('Escape');
      await expect(confirm).toHaveCount(0);
      await expect(palette.update(copy)).toBeFocused();

      await page.keyboard.press('Enter');
      await accept.click();
      await expect(builder).toHaveAnnounced(
        `Updated template ${name} from the library`,
      );
      await expect(palette.link(copy)).toHaveCount(0);
      await expect.soft(notice).toHaveCount(0);
      // The mark is gone: focus is on the template's entry, not lost.
      await expect(palette.entry(copy)).toBeFocused();
      await builder.persisted(draft, version, 2);

      const doc = await builder.serverDocument(draft);

      expect.soft(doc.templates[0]).toMatchObject({
        id: copy,
        name,
        description: 'Version two',
        device: { spec: { hardware: { drives: [{ image: 'v2.qc2' }] } } },
      });
      // The device made from version 1 is as it was.
      expect
        .soft(devicesOf(doc)[0].device.spec.hardware.drives[0].image)
        .toBe('v1.qc2');

      // Undo brings version 1 back, and the offer with it. Redo updates
      // it again.
      await builder.toolbar('undo').click();
      await expect(palette.link(copy)).toContainText('Update available');
      await builder.persisted(draft, version, 1);
      await builder.toolbar('redo').click();
      await expect(palette.link(copy)).toHaveCount(0);
      await builder.persisted(draft, version, 2);
    });

    await test.step('a copy changed in the diagram says that Update replaces the changes, from the Inspector', async () => {
      const editor = templateEditor(page);

      await palette.actions(copy).click();
      await palette.action(copy, 'edit').click();
      await expect(editor.dialog).toBeVisible();
      await editor.description.fill('Changed here');
      await editor.save.click();
      await expect(editor.dialog).toHaveCount(0);

      await nextVersion(3, 'Version three');
      // The library is read again when the window comes to the front.
      await page.evaluate(() => window.dispatchEvent(new Event('focus')));
      await expect(notice).toBeVisible();

      await notice.getByTestId(`inspector-template-update-${copy}`).click();
      await expect(confirm).toBeVisible();
      await expect
        .soft(confirm)
        .toHaveAccessibleDescription(
          'The template in this diagram gets version 3 from the library. Devices made from the template do not change. This template has changes in the diagram. The update replaces these changes. You can undo this.',
        );
      await accept.click();
      await expect(builder).toHaveAnnounced(
        `Updated template ${name} from the library`,
      );
      await expect(notice).toHaveCount(0);
      // The notice is gone: focus goes to the Inspector's heading.
      await expect(builder.inspector.locator('#inspector-title')).toBeFocused();
      await builder.persisted(draft, version, 3);
      expect
        .soft((await builder.serverDocument(draft)).templates[0].description)
        .toBe('Version three');
    });

    await test.step('a deleted library template is said, and Unlink keeps the copy without its link', async () => {
      const deleted = await deleteLibraryItems(request, { templates: [id] });

      expect(deleted.ok(), await deleted.text()).toBeTruthy();
      await page.evaluate(() => window.dispatchEvent(new Event('focus')));
      await expect(palette.link(copy)).toContainText('Deleted from library');
      await expect
        .soft(notice.getByTestId(`inspector-template-link-text-${copy}`))
        .toHaveText('Someone deleted the library template.');
      await expect
        .soft(notice.getByTestId(`inspector-template-unlink-${copy}`))
        .toHaveAccessibleName(`Unlink template ${name}`);
      // Nothing deletes the copy.
      await expect.soft(palette.entry(copy)).toBeVisible();

      await palette.unlink(copy).click();
      await expect(builder).toHaveAnnounced(
        `Unlinked template ${name} from the library`,
      );
      await expect(palette.link(copy)).toHaveCount(0);
      await expect.soft(notice).toHaveCount(0);
      await expect(palette.entry(copy)).toBeFocused();
      await builder.persisted(draft, source, null);
      expect
        .soft((await builder.serverDocument(draft)).templates)
        .toHaveLength(1);
    });

    expectNoFatal(issues);
  },
);

// Three templates of the library, made through the API and named for the
// test: alfa, bravo and delta. Returns their names and ids.
async function seedThreeTemplates(request, tracker, testInfo) {
  const names = ['alfa', 'bravo', 'delta'].map((part) =>
    uniqueName(testInfo, part),
  );
  const { ids } = await seedTemplates(
    request,
    tracker,
    names.map((name, index) => ({
      name,
      description: '',
      device: unitDevice(`unit-${index}`),
    })),
  );

  return { names, ids };
}

test('collections group templates of the library, and selected templates are added to one, or start one', async ({
  page,
  builder,
  request,
  tracker,
  issues,
}, testInfo) => {
  const first = uniqueName(testInfo, 'set-one');
  const second = uniqueName(testInfo, 'set-two');
  const {
    ids: [alfa, bravo, delta],
  } = await seedThreeTemplates(request, tracker, testInfo);
  const library = templateLibrary(page);
  const { dialog } = library;
  let one;

  await builder.open();
  await library.tab.click();
  await expect(library.card(alfa)).toBeVisible();

  await test.step('New collection needs a name, and makes an empty collection the Show field lists', async () => {
    await library.newCollection.click();
    await expect(dialog.root).toBeVisible();
    await expect.soft(dialog.root).toHaveAccessibleName('New collection');
    await expect(dialog.name).toBeFocused();
    await expect.soft(dialog.save).toHaveText('Create collection');
    await expect.soft(dialog.members).toHaveCount(0);

    await dialog.save.click();
    await expect(dialog.error).toHaveText('Enter a name.');
    await expect.soft(dialog.name).toBeFocused();
    await expect.soft(dialog.name).toHaveAttribute('aria-invalid', 'true');

    await dialog.name.fill(first);
    await dialog.description.fill('The first set');

    const added = libraryAnswer(page, 'POST', 'collections');

    await dialog.name.press('Enter');
    one = (await (await added).json()).id;
    expect(one).toMatch(UUID);
    await expect(dialog.root).toHaveCount(0);
    await expect(builder).toHaveAnnounced(`Created collection ${first}.`);
    await expect(library.newCollection).toBeFocused();
    await expect
      .soft(library.show.locator('optgroup').first())
      .toHaveAttribute('label', 'Library collections');
    await expect(library.show.locator(`option[value="${one}"]`)).toHaveText(
      first,
    );
  });

  await test.step('cards are selected by keyboard, and the row says how many', async () => {
    await expect
      .soft(library.bar)
      .toHaveAccessibleName('Bulk actions: Library');
    await expect.soft(library.collect).toHaveAttribute('aria-disabled', 'true');
    await expect
      .soft(library.bulkDelete)
      .toHaveAttribute('aria-disabled', 'true');
    await expect
      .soft(library.bulkDelete)
      .toHaveAccessibleDescription(/^0 of \d+ selected$/);
    await expect.soft(library.uncollect).toHaveCount(0);

    await library.select(alfa).focus();
    await page.keyboard.press('Space');
    await expect(library.select(alfa)).toBeChecked();
    await expect(builder).toHaveAnnounced(/\b1 of \d+ selected\./);
    await library.select(bravo).focus();
    await page.keyboard.press('Space');
    await expect(library.count).toHaveText(/^2 of \d+ selected$/);
    await expect.soft(library.card(alfa)).toHaveClass(/is-selected/);
    await expect.soft(library.card(delta)).not.toHaveClass(/is-selected/);
    // Some, not all: Select all shows the mixed state.
    expect
      .soft(await library.all.evaluate((box) => box.indeterminate))
      .toBe(true);
    await expect
      .soft(library.collect)
      .not.toHaveAttribute('aria-disabled', 'true');
  });

  await test.step('Add to collection lists the collections, and adds the selected templates to the one chosen', async () => {
    await library.collect.focus();
    await page.keyboard.press('Enter');
    // The menu opens on its first item. Collections other tests made in the
    // shared library may come before this one: the arrow keys reach it.
    await expect(
      page
        .getByTestId('bulk-collect-templates-menu')
        .getByRole('menuitem')
        .first(),
    ).toBeFocused();
    for (
      let step = 0;
      step < 50 &&
      !(await library
        .collectItem(one)
        .evaluate((item) => item === document.activeElement));
      step += 1
    ) {
      await page.keyboard.press('ArrowDown');
    }
    await expect(library.collectItem(one)).toBeFocused();
    await expect.soft(library.collectItem(one)).toHaveText(first);
    await expect
      .soft(library.collectItem('new-collection'))
      .toHaveText('New collection…');

    const changed = libraryAnswer(page, 'PUT', `collections/${one}`);

    await page.keyboard.press('Enter');

    const answer = await changed;

    expect(answer.status()).toBe(200);
    expect(answer.request().postDataJSON()).toEqual({
      name: first,
      description: 'The first set',
      templateIds: [alfa, bravo],
    });
    await expect(builder).toHaveAnnounced(`Added 2 templates to ${first}.`);
    await expect(library.collect).toBeFocused();
    await expect(page.getByTestId(`template-collections-${alfa}`)).toHaveText(
      `In ${first}`,
    );
    // They stay selected, for what comes next.
    await expect.soft(library.count).toHaveText(/^2 of \d+ selected$/);

    // Adding them again changes nothing, and says so.
    await library.collect.click();
    await library.collectItem(one).click();
    await expect(builder).toHaveAnnounced(
      `The selected templates are already in ${first}.`,
    );
  });

  await test.step('"New collection…" starts a collection with the selected templates', async () => {
    await library.select(delta).check();
    await library.select(alfa).uncheck();
    await library.collect.click();
    await library.collectItem('new-collection').click();
    await expect(dialog.root).toBeVisible();
    await expect(dialog.members).toHaveText(
      'Create collection adds the 2 selected templates to the new collection.',
    );

    // A name another collection has is said, and allowed.
    await dialog.name.fill(first);
    await expect(dialog.nameHint).toHaveText(
      'Another collection has this name.',
    );
    await dialog.name.fill(second);
    await expect(dialog.nameHint).toHaveText('');

    const added = libraryAnswer(page, 'POST', 'collections');

    await dialog.save.click();

    const answer = await added;

    expect(answer.request().postDataJSON()).toEqual({
      name: second,
      description: '',
      templateIds: [bravo, delta],
    });
    await expect(dialog.root).toHaveCount(0);
    await expect(builder).toHaveAnnounced(
      `Created collection ${second} with 2 templates.`,
    );
    await expect(page.getByTestId(`template-collections-${bravo}`)).toHaveText(
      `In ${first} and ${second}`,
    );
  });

  expectNoFatal(issues);
});

test('a collection shows what it is and its templates, which are taken out of it, and it is renamed and deleted, and its templates deleted at once', async ({
  page,
  builder,
  request,
  tracker,
  issues,
}, testInfo) => {
  const first = uniqueName(testInfo, 'set-one');
  const second = uniqueName(testInfo, 'set-two');
  const {
    names,
    ids: [alfa, bravo, delta],
  } = await seedThreeTemplates(request, tracker, testInfo);
  // The two collections the test above makes: alfa and bravo in the first,
  // bravo and delta in the second.
  const one = await seedCollection(request, tracker, {
    name: first,
    description: 'The first set',
    templateIds: [alfa, bravo],
  });
  const two = await seedCollection(request, tracker, {
    name: second,
    templateIds: [bravo, delta],
  });
  const library = templateLibrary(page);
  const { dialog } = library;
  const collections = async () =>
    (await readLibrary(request)).collections.filter((entry) =>
      [first, second, `${first} two`].includes(entry.name),
    );

  await builder.open();
  await library.tab.click();
  await expect(library.card(alfa)).toBeVisible();

  await test.step('Show lists one collection: what it is, its templates, and Remove from collection', async () => {
    await library.show.selectOption(one);
    await expect(library.block).toBeVisible();
    await expect.soft(library.block.getByRole('heading')).toHaveText(first);
    await expect
      .soft(page.getByTestId('collection-count'))
      .toHaveText('2 templates');
    await expect
      .soft(page.getByTestId('collection-about'))
      .toHaveText('The first set');
    await expect
      .soft(library.block.getByRole('button'))
      .toHaveText([
        'Edit collection',
        'Delete collection',
        'Download collection',
      ]);
    await expect(library.list.locator('.builder-card')).toHaveCount(2);
    await expect(library.card(alfa)).toBeVisible();
    await expect(library.card(bravo)).toBeVisible();
    // Showing another list drops the selection.
    await expect(library.count).toHaveText('0 of 2 selected');
    await expect
      .soft(library.bar)
      .toHaveAccessibleName(`Bulk actions: ${first}`);
    await expect
      .soft(library.uncollect)
      .toHaveAttribute('aria-disabled', 'true');
    // The collection shown is not one to add to.
    await library.select(alfa).check();
    await library.collect.click();
    await expect(library.collectItem(two)).toBeVisible();
    await expect.soft(library.collectItem(one)).toHaveCount(0);
    await page.keyboard.press('Escape');

    const changed = libraryAnswer(page, 'PUT', `collections/${one}`);

    await library.uncollect.click();
    expect((await changed).request().postDataJSON().templateIds).toEqual([
      bravo,
    ]);
    await expect(builder).toHaveAnnounced(`Removed ${names[0]} from ${first}.`);
    await expect(library.card(alfa)).toHaveCount(0);
    await expect(library.list.locator('.builder-card')).toHaveCount(1);
    await expect(library.all).toBeFocused();
    await expect
      .soft(page.getByTestId('collection-count'))
      .toHaveText('1 template');
    // It left the collection, not the library.
    expect((await listedTemplates(request, [alfa]))[0]).not.toBeNull();
  });

  await test.step('Edit collection renames it, and Delete collection keeps its templates', async () => {
    await library.editCollection.click();
    await expect(dialog.root).toBeVisible();
    await expect
      .soft(dialog.root)
      .toHaveAccessibleName(`Edit collection ${first}`);
    await expect.soft(dialog.name).toHaveValue(first);
    await expect.soft(dialog.description).toHaveValue('The first set');
    await expect.soft(dialog.save).toHaveText('Save');
    await dialog.name.fill(`${first} two`);
    await dialog.save.click();
    await expect(dialog.root).toHaveCount(0);
    await expect(builder).toHaveAnnounced(`Updated collection ${first} two.`);
    await expect(library.editCollection).toBeFocused();
    await expect(library.block.getByRole('heading')).toHaveText(`${first} two`);
    // Its templates are as they were.
    expect
      .soft((await collections()).find((entry) => entry.id === one))
      .toMatchObject({ name: `${first} two`, templateIds: [bravo] });

    await library.deleteCollection.click();
    await expect(library.confirm).toBeVisible();
    await expect
      .soft(library.confirm)
      .toHaveAccessibleName(`Delete collection ${first} two?`);
    await expect
      .soft(library.confirm)
      .toHaveAccessibleDescription(
        'This removes the collection for all users. Its templates stay in the library.',
      );
    await expect.soft(library.accept).toHaveText('Delete collection');
    await library.accept.click();
    await expect(builder).toHaveAnnounced(`Deleted collection ${first} two.`);
    await expect(library.block).toHaveCount(0);
    await expect(library.show).toHaveValue('');
    await expect(library.show).toBeFocused();
    await expect(library.show.locator(`option[value="${one}"]`)).toHaveCount(0);
    await expect(library.card(bravo)).toBeVisible();
    await expect(page.getByTestId(`template-collections-${bravo}`)).toHaveText(
      `In ${second}`,
    );
  });

  await test.step('Select all and Delete selected, on a collection the test made, delete its templates after one question', async () => {
    await library.show.selectOption(two);
    await expect(library.list.locator('.builder-card')).toHaveCount(2);
    await library.all.focus();
    await page.keyboard.press('Space');
    await expect(library.count).toHaveText('2 of 2 selected');
    await expect(builder).toHaveAnnounced('2 of 2 selected.');
    await expect(library.all).toBeChecked();

    await library.bulkDelete.click();
    await expect(library.confirm).toBeVisible();
    // The question says what goes: the two of this collection.
    await expect(library.confirm).toHaveAccessibleName('Delete 2 templates?');
    await expect
      .soft(library.confirm)
      .toHaveAccessibleDescription(
        'This removes the templates from the library and its collections, for all users. Diagrams keep their copies of them. You cannot undo this.',
      );
    await expect.soft(library.accept).toHaveText('Delete templates');
    await expect(library.cancel).toBeFocused();

    // Cancel deletes nothing, and gives focus back.
    await library.cancel.click();
    await expect(library.bulkDelete).toBeFocused();
    await expect(library.list.locator('.builder-card')).toHaveCount(2);

    await library.bulkDelete.click();

    const deleted = libraryAnswer(page, 'POST', 'delete');

    await library.accept.click();

    const answer = await deleted;

    // One request for both, each at the tag the library listed.
    expect(answer.request().postDataJSON()).toEqual({
      templates: [bravo, delta].map((id) => ({
        id,
        etag: expect.stringMatching(/^"\d+"$/),
      })),
    });
    await expect(builder).toHaveAnnounced('Deleted 2 templates.');
    await expect(library.empty).toHaveText(
      'This collection has no templates. Select templates under All library templates, then Add to collection.',
    );
    // No list is left to select in: focus goes to the tab.
    await expect(library.bar).toHaveCount(0);
    await expect(library.tab).toBeFocused();
    await expect
      .soft(page.getByTestId('collection-count'))
      .toHaveText('0 templates');

    const [kept, ...gone] = await listedTemplates(request, [
      alfa,
      bravo,
      delta,
    ]);

    expect(kept).not.toBeNull();
    expect(gone).toEqual([null, null]);
    // The collection is still there, empty.
    expect
      .soft((await collections()).map((entry) => entry.templateIds))
      .toEqual([[]]);
  });

  expectNoFatal(issues);
});

test('a template of a diagram is saved to the library naming its icon, and linked to it, and the built-in templates stay when the library cannot be read', async ({
  page,
  builder,
  request,
  tracker,
  issues,
}, testInfo) => {
  const name = uniqueName(testInfo, 'unit');
  // An icon of the server's icon library, which the template names.
  const icon = iconOf(pngOf(8, 8, ownColor()), iconName('unit'));

  await seedIcon(request, tracker, icon);

  const unit = template(name, {
    description: 'A unit',
    device: unitDevice('unit', { iconKey: 'router', icon: icon.name }),
  });
  const draft = await builder.seedDraft(
    plantDocument(uniqueName(testInfo, 'to-library'), { templates: [unit] }),
  );
  const palette = templatePalette(page);
  const library = templateLibrary(page);
  const READS = `**${LIBRARY}`;
  // Whether the test fails the reads of the library, from the page.
  let unreachable = true;
  let id;

  await builder.openDraft(draft);

  await test.step('Save to library, in the menu of a template of the diagram, copies it into the library and links the diagram’s template to it', async () => {
    await expect(palette.groups.first()).toHaveText('This diagram');
    await palette.actions(unit.id).click();
    await expect(
      page
        .getByTestId(`palette-template-actions-${unit.id}-menu`)
        .getByRole('menuitem'),
    ).toHaveText(['Edit', 'Save to library', 'Delete']);

    const added = libraryAnswer(page, 'POST', 'items');

    await palette.action(unit.id, 'library').click();

    const answer = await added;

    expect(answer.status()).toBe(201);
    // A copy, naming its custom icon, and without its id.
    expect(answer.request().postDataJSON()).toEqual({
      templates: [{ name, description: 'A unit', device: unit.device }],
    });
    [{ id }] = (await answer.json()).created;
    await expect(builder).toHaveAnnounced(`Saved ${name} to the library.`);
    await expect(palette.actions(unit.id)).toBeFocused();

    // The diagram's template is now a copy of the library's, at its
    // version, and its menu offers Unlink.
    await builder.persisted(
      draft,
      (doc) => doc.templates[0].source?.id ?? null,
      id,
    );
    expect
      .soft((await builder.serverDocument(draft)).templates[0].source)
      .toMatchObject({ kind: 'library', id, version: 1 });
    await expect.soft(palette.link(unit.id)).toHaveCount(0);
    await palette.actions(unit.id).click();
    await expect
      .soft(
        page
          .getByTestId(`palette-template-actions-${unit.id}-menu`)
          .getByRole('menuitem'),
      )
      .toHaveText(['Edit', 'Save to library', 'Unlink', 'Delete']);
    await page.keyboard.press('Escape');

    // It stays in the diagram, and is in the library too.
    await expect(palette.entry(unit.id)).toBeVisible();
    await expect(palette.own(id)).toHaveText(name);
    await expect.soft(palette.own(id)).toHaveAccessibleDescription('A unit');
    // The icon library draws the icon the template names.
    await expect
      .soft(palette.own(id).locator('img.builder-icon--custom'))
      .toHaveAttribute('src', `data:image/png;base64,${icon.data}`);

    const stored = await readLibrary(request);

    expect
      .soft(stored.templates.find((entry) => entry.id === id).device)
      .toEqual(unit.device);
    expect.soft(stored).not.toHaveProperty('icons');
  });

  await test.step('a template added elsewhere is listed once the window comes back to the front', async () => {
    const later = uniqueName(testInfo, 'later');
    const { ids } = await seedTemplates(request, tracker, [
      { name: later, description: '', device: unitDevice('later') },
    ]);

    await expect(palette.own(ids[0])).toHaveCount(0);
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await expect(palette.own(ids[0])).toHaveText(later);
  });

  await test.step('a device made from the library’s copy names its icon, and the diagram carries no image of it', async () => {
    await palette.actions(unit.id).click();
    await palette.action(unit.id, 'delete').click();
    await expect(palette.diagram).toHaveCount(0);
    await builder.persisted(draft, (doc) => doc.templates ?? null, null);

    await palette.own(id).click();
    await builder.persisted(draft, hostnames, ['plc-01', 'unit']);

    const doc = await builder.serverDocument(draft);

    expect.soft(byHostname(doc, 'unit').device).toMatchObject({
      iconKey: 'router',
      icon: icon.name,
    });
    expect.soft(doc).not.toHaveProperty('icons');
    await expect
      .soft(builder.node('unit', 'device').locator('img.builder-icon--custom'))
      .toHaveAttribute('src', `data:image/png;base64,${icon.data}`);
    await builder.waitSaved();
  });

  await test.step('a library that cannot be read says so on its tab, with Retry', async () => {
    // The address names the open draft. Leave it so the reload shows the landing.
    await builder.backToDrafts();
    await expect(page).toHaveURL(/\/builder$/);
    await page.route(READS, (route) =>
      unreachable && route.request().method() === 'GET'
        ? route.abort()
        : route.fallback(),
    );
    await page.reload();
    await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
    await expect(library.tab).toHaveText('Node Templates (0)');
    await library.tab.click();
    await expect(library.error).toHaveText(
      'Could not load the template library. The Builder could not reach the server. Check the connection and try again.',
    );
    await expect.soft(library.retry).toBeVisible();
    await expect.soft(library.list).toHaveCount(0);
    // The drafts are listed all the same.
    await page.getByTestId('drafts-tab-drafts').click();
    await expect(page.getByTestId(`draft-open-${draft.id}`)).toBeVisible();
  });

  await test.step('Templates then offers the built-in templates, which still make devices, and Retry reads the library', async () => {
    await page.getByTestId(`draft-open-${draft.id}`).click();
    await expect(builder.canvas).toBeVisible();
    await expect(palette.builtin.getByRole('button')).toHaveCount(5);
    await expect
      .soft(palette.builtin)
      .toHaveAttribute('aria-label', 'Built-in');
    await expect.soft(palette.library).toHaveCount(0);
    await expect(palette.note).toHaveText(
      'The Builder could not load the template library.',
    );
    await expect.soft(palette.note).toHaveAttribute('role', 'status');
    await expect
      .soft(palette.retry)
      .toHaveAccessibleDescription(
        'The Builder could not load the template library.',
      );

    // The built-in Router: its entry is the one the library's has.
    await palette.own('router').click();
    await builder.persisted(draft, hostnames, ['plc-01', 'unit', 'router']);

    // Still unreachable: the built-in ones stay, with the note.
    await palette.retry.click();
    await expect(palette.retry).not.toHaveAttribute('aria-busy', 'true');
    await expect.soft(palette.builtin.getByRole('button')).toHaveCount(5);
    await expect
      .soft(palette.note)
      .toHaveText('The Builder could not load the template library.');

    unreachable = false;
    await palette.retry.focus();
    await page.keyboard.press('Enter');
    await expect(palette.own(id)).toBeVisible();
    await expect.soft(palette.builtin.getByRole('button')).toHaveCount(5);
    await expect.soft(palette.note).toHaveText('');
    await expect.soft(palette.retry).toHaveCount(0);
    // Retry is gone: focus is on the library button, not lost.
    await expect(palette.libraryButton).toBeFocused();
  });

  await test.step('the command palette opens the library from the editor, and shows its tab on the drafts page', async () => {
    const commands = page.getByTestId('commands-dialog');
    const field = page.getByRole('combobox', {
      name: /Search commands|choose a/,
    });
    const first = commands.getByRole('option', { selected: true });

    await builder.waitSaved();
    await builder.canvas.focus();
    await page.keyboard.press('ControlOrMeta+k');
    await field.fill('templates library');
    await expect(first).toContainText('Open Node Templates library');
    await page.keyboard.press('Enter');
    await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
    await expect(library.tab).toHaveAttribute('aria-selected', 'true');
    await expect(library.tab).toBeFocused();
    await expect(library.card(id)).toBeVisible();

    await page.getByTestId('drafts-tab-drafts').click();
    await page.keyboard.press('ControlOrMeta+k');
    await field.fill('show node templates');
    await expect(first).toContainText('Show Node Templates');
    await page.keyboard.press('Enter');
    await expect(library.tab).toHaveAttribute('aria-selected', 'true');
    await expect(library.tab).toBeFocused();
  });

  expectNoFatal(issues);
});

test('a card names the drive images the server does not have, and Show lists those templates', async ({
  page,
  builder,
  request,
  tracker,
  issues,
}, testInfo) => {
  const names = ['images-gone', 'images-kept'].map((part) =>
    uniqueName(testInfo, part),
  );
  const kept = unitDevice('images-b');

  kept.spec.hardware.drives = [{ image: 'kept.qc2' }];

  const seeded = await seedTemplates(request, tracker, [
    { name: names[0], description: '', device: unitDevice('images-a') },
    { name: names[1], description: '', device: kept },
  ]);
  const [goneId, keptId] = seeded.ids;
  const library = templateLibrary(page);
  const said = 'The server has no disk image named "unit.qc2".';

  await test.step('while the server lists no disk images, no card is marked and Show does not offer the list', async () => {
    await page.route('**/api/v1/disks', (route) =>
      route.fulfill({ json: { disks: [] } }),
    );
    await builder.open();

    // The tab reads the disk images when it shows. Its answer comes first.
    const listed = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith('/api/v1/disks'),
    );

    await library.tab.click();
    await listed;
    await expect(library.card(goneId)).toBeVisible();
    await expect.soft(library.missingImages(goneId)).toHaveCount(0);
    await expect
      .soft(library.show.getByRole('option', { name: /missing images/ }))
      .toHaveCount(0);
  });

  await test.step('a card says which image is missing, in its description too', async () => {
    await page.route('**/api/v1/disks', (route) =>
      route.fulfill({ json: { disks: [{ kind: 'VM', name: 'kept.qc2' }] } }),
    );
    await page.reload();
    await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
    await library.tab.click();
    await expect(library.missingImages(goneId)).toHaveText(said);
    await expect.soft(library.card(goneId)).toHaveAccessibleDescription(said);
    await expect.soft(library.missingImages(keptId)).toHaveCount(0);
  });

  await test.step('Show lists the templates with missing images', async () => {
    await library.show.selectOption({ label: 'Templates with missing images' });
    await expect(library.missingNote).toHaveText(
      'These templates name a drive image that the server does not have. To choose another image, select Edit.',
    );
    await expect(library.card(goneId)).toBeVisible();
    await expect.soft(library.card(keptId)).toHaveCount(0);
    await expect
      .soft(library.bar)
      .toHaveAccessibleName('Bulk actions: Templates with missing images');
  });

  expectNoFatal(issues);
});

for (const scheme of ['light', 'dark']) {
  test(
    `axe finds no serious violations in the Node Templates tab, its dialogs, the palette’s library and the offer of a newer template in the ${scheme} theme`,
    { tag: '@axe' },
    async ({ page, builder, request, tracker, issues }, testInfo) => {
      const names = ['axe-alfa', 'axe-bravo'].map((part) =>
        uniqueName(testInfo, part),
      );
      const set = uniqueName(testInfo, 'axe-set');
      const icon = iconOf(pngOf(8, 8, ownColor()), iconName('axe'));

      await seedIcon(request, tracker, icon);

      const seeded = await seedTemplates(
        request,
        tracker,
        [
          {
            name: names[0],
            description: 'With an icon of its own',
            device: unitDevice('axe-a', { icon: icon.name }),
          },
          { name: names[1], description: '', device: unitDevice('axe-b') },
        ],
        { collection: { name: set, description: 'A set for the scan' } },
      );
      const [alfa] = seeded.ids;
      // The diagram has a copy of alfa's version 1, and the library then
      // gets version 2: the palette and the Inspector offer it.
      const copy = template(names[0], {
        description: 'With an icon of its own',
        device: unitDevice('axe-a', { icon: icon.name }),
        source: { kind: 'library', id: alfa, version: 1 },
      });
      const draft = await builder.seedDraft(
        plantDocument(uniqueName(testInfo, 'library-axe'), {
          templates: [template('RTU'), copy],
        }),
      );

      await changeTemplate(request, alfa, (listed) => ({
        ...listed,
        description: 'A newer version',
      }));

      const library = templateLibrary(page);
      const palette = templatePalette(page);
      const editor = templateEditor(page);
      const notice = builder.inspector.getByTestId('inspector-template-links');
      // Whether the test fails the reads of the library, from the page.
      let unreachable = false;

      await page.route(`**${LIBRARY}`, (route) =>
        unreachable && route.request().method() === 'GET'
          ? route.abort()
          : route.fallback(),
      );
      // The server lists other.qc2 alone: the cards name a missing image.
      await page.route('**/api/v1/disks', (route) =>
        route.fulfill({ json: { disks: [{ kind: 'VM', name: 'other.qc2' }] } }),
      );

      await page.emulateMedia({ colorScheme: scheme });
      await builder.open();
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        scheme,
      );

      // The tab: its cards, a selection, and the menu of collections.
      await library.tab.click();
      await expect(library.card(alfa)).toBeVisible();
      await library.select(alfa).check();
      await library.collect.click();
      await expect(library.collectItem(seeded.collection)).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the Node Templates tab with a selection and its menu (${scheme})`,
      });
      await page.keyboard.press('Escape');

      // A collection, and the question before it is deleted.
      await library.show.selectOption(seeded.collection);
      await expect(library.block).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on a collection of the library (${scheme})`,
      });
      await library.deleteCollection.click();
      await expect(library.confirm).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the question before a collection is deleted (${scheme})`,
      });
      await library.cancel.click();

      // The collection dialog, with an error.
      await library.editCollection.click();
      await library.dialog.name.fill('');
      await library.dialog.save.click();
      await expect(library.dialog.error).toHaveText('Enter a name.');
      await expectAccessible(page, {
        soft: true,
        label: `axe on the collection dialog with an error (${scheme})`,
      });
      await library.dialog.cancel.click();

      // The built-in templates, read only.
      await library.show.selectOption({ label: 'Built-in templates' });
      await expect(library.roCard('builtin', 'router')).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the built-in templates of the tab (${scheme})`,
      });

      // The templates of the library with a missing image.
      await library.show.selectOption({
        label: 'Templates with missing images',
      });
      await expect(library.missingNote).toBeVisible();
      await expect(library.missingImages(alfa)).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the templates with missing images (${scheme})`,
      });
      await library.show.selectOption('');

      // The template editor on a template of the library.
      await library.edit(alfa).click();
      await expect(editor.dialog).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the editor of a library template (${scheme})`,
      });
      await editor.cancel.click();
      await expect(editor.dialog).toHaveCount(0);

      // In a narrow window the tab's rows wrap, and nothing runs off it.
      await page.setViewportSize({ width: 360, height: 640 });
      expect
        .soft(
          await page.evaluate(
            () =>
              document.documentElement.scrollWidth -
              document.documentElement.clientWidth,
          ),
          `sideways scroll of the tab (${scheme})`,
        )
        .toBe(0);
      await expectAccessible(page, {
        soft: true,
        label: `axe on the Node Templates tab in a narrow window (${scheme})`,
      });
      await page.setViewportSize({ width: 1280, height: 720 });

      // The palette: the diagram's group and the library's.
      await page.getByTestId('drafts-tab-drafts').click();
      await page.getByTestId(`draft-open-${draft.id}`).click();
      await expect(builder.canvas).toBeVisible();
      await expect(palette.own(alfa)).toBeVisible();
      await expect(palette.groups.first()).toHaveText('This diagram');
      // The copy's mark, and the notice of the Inspector's Diagram section.
      await expect(palette.link(copy.id)).toContainText('Update available');
      await expect(notice).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the palette’s library group, a mark of a newer template and the Inspector’s notice (${scheme})`,
      });
      await palette.update(copy.id).click();
      await expect(page.getByTestId('builder-confirm')).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the question before a template is updated (${scheme})`,
      });
      await page.getByTestId('confirm-cancel').click();

      // A library that cannot be read: its tab, and the palette's note.
      await builder.waitSaved();
      await builder.backToDrafts();
      await expect(page).toHaveURL(/\/builder$/);
      unreachable = true;
      await page.reload();
      await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
      await library.tab.click();
      await expect(library.error).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the tab of a library that cannot be read (${scheme})`,
      });
      await page.getByTestId('drafts-tab-drafts').click();
      await page.getByTestId(`draft-open-${draft.id}`).click();
      await expect(palette.retry).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the palette without the library (${scheme})`,
      });
      unreachable = false;
      await builder.waitSaved();

      expectNoFatal(issues);
    },
  );
}

for (const scheme of ['light', 'dark']) {
  test(
    `axe finds no serious violations in the template editor and the palette’s templates in the ${scheme} theme`,
    { tag: '@axe' },
    async ({ page, builder, issues }, testInfo) => {
      await page.emulateMedia({ colorScheme: scheme });

      // A copy the diagram carries, of an icon the server lacks.
      const plc = iconOf(pngOf(8, 8, ownColor()), iconName('plc'));
      const draft = await builder.seedDraft(
        plantDocument(uniqueName(testInfo, `templates-axe-${scheme}`), {
          look: { icon: plc.name, fillColor: '#ffeecc' },
          icons: { [plc.name]: plc.entry },
          templates: [
            template('RTU', {
              description: 'Remote terminal unit',
              device: {
                iconKey: 'router',
                icon: plc.name,
                outlineColor: '#2f6fbf',
                spec: plcSpec('rtu', ''),
              },
            }),
            template('HMI'),
          ],
        }),
      );
      const palette = templatePalette(page);
      const editor = templateEditor(page);
      const [rtu] = (await builder.serverDocument(draft)).templates;

      await builder.openDraft(draft);
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        scheme,
      );
      await expect(palette.groups.first()).toHaveText('This diagram');

      await palette.actions(rtu.id).click();
      await expect(palette.action(rtu.id, 'edit')).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the palette’s template groups and a template’s menu (${scheme})`,
      });

      await palette.action(rtu.id, 'edit').click();
      await expect(editor.dialog).toBeVisible();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the template editor (${scheme})`,
      });

      // With a field in error, its summary, a name that is taken, and the
      // question it asks before it drops changes.
      await editor.name.fill('HMI');
      await editor.input('hostname').fill('two words');
      await editor.save.click();
      await expect(editor.errorLink).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(editor.keep).toBeFocused();
      await expectAccessible(page, {
        soft: true,
        label: `axe on the template editor with errors and its question (${scheme})`,
      });

      // In a narrow window the editor fills it, in one column, and nothing
      // runs off its side.
      await page.setViewportSize({ width: 360, height: 640 });
      await editor.keep.click();
      const box = await editor.dialog.boundingBox();

      expect.soft(box).toEqual({ x: 0, y: 0, width: 360, height: 640 });
      expect
        .soft(
          await editor.dialog
            .locator('.builder-template-editor__body')
            .evaluate((body) => body.scrollWidth - body.clientWidth),
          'sideways scroll of the form',
        )
        .toBe(0);
      await expect.soft(editor.save).toBeInViewport({ ratio: 1 });
      await expectAccessible(page, {
        soft: true,
        label: `axe on the template editor in a narrow window (${scheme})`,
      });

      expectNoFatal(issues);
    },
  );
}

test('a collection is exported as a template file, which Import reads back into the library as a new collection that makes devices', async ({
  page,
  builder,
  request,
  tracker,
  issues,
}, testInfo) => {
  const names = ['file-alfa', 'file-bravo'].map((part) =>
    uniqueName(testInfo, part),
  );
  const set = uniqueName(testInfo, 'file-set');
  const icon = iconOf(pngOf(8, 8, ownColor()), iconName('file'));

  await seedIcon(request, tracker, icon);

  const seeded = await seedTemplates(
    request,
    tracker,
    [
      {
        name: names[0],
        description: 'Carries its icon',
        device: unitDevice('file-alfa', { icon: icon.name }),
      },
      { name: names[1], description: '', device: unitDevice('file-bravo') },
    ],
    { collection: { name: set, description: 'Exported by a test' } },
  );
  const draft = await builder.seedDraft(
    plantDocument(uniqueName(testInfo, 'template-file')),
  );
  const library = templateLibrary(page);
  const palette = templatePalette(page);
  const dialog = page.getByTestId('template-import-dialog');
  let exported;
  let imported;

  await builder.open();
  await library.tab.click();

  await test.step('Download collection saves a YAML template file that carries the icon its templates name', async () => {
    await library.show.selectOption(seeded.collection);
    await expect(library.block).toBeVisible();

    exported = await download(page, () =>
      page.getByTestId('collection-export').click(),
    );

    const fileName = `${set.toLowerCase().replace(/[^a-z0-9._-]+/g, '-')}.templates.yaml`;
    const text = exported.buffer.toString('utf8');

    expect(exported.name).toBe(fileName);
    expect(text).toContain(
      '$schema: https://phenix.sandia.gov/schemas/builder/templates/v1\n',
    );
    expect(text).toContain(`name: ${set}\n`);
    expect(text).toContain('description: Exported by a test\n');
    expect(text).toContain(`  - name: ${names[0]}\n`);
    expect(text).toContain(`  - name: ${names[1]}\n`);
    // No ids: where a template is kept gives it one.
    expect(text).not.toContain(seeded.ids[0]);
    expect(text).toContain(`icons:\n  ${icon.name}:\n    data: ${icon.data}\n`);
    await expect(builder).toHaveAnnounced(
      `Downloaded 2 templates to ${fileName}.`,
    );
  });

  await test.step('a file that is not valid is refused, each problem with where it is', async () => {
    await page.getByTestId('templates-import').click();
    await expect(dialog).toBeVisible();
    await expect(dialog.getByTestId('template-import-file')).toBeFocused();

    const twins = exported.buffer
      .toString('utf8')
      .replace(
        `  - name: ${names[1]}\n`,
        `  - name: ${names[0].toUpperCase()}\n`,
      );

    await dialog.getByTestId('template-import-file').setInputFiles({
      name: 'twins.templates.yaml',
      mimeType: 'text/yaml',
      buffer: Buffer.from(twins),
    });
    await expect(dialog.getByTestId('template-import-error')).toHaveText(
      'You cannot import this template file:',
    );
    await expect(dialog.getByTestId('template-import-issue')).toHaveText([
      `templates[1].name: template name "${names[0].toUpperCase()}" is also the name of templates[0], ignoring case`,
    ]);
    await expect
      .soft(dialog.getByTestId('template-import-file'))
      .toHaveAttribute('aria-invalid', 'true');
  });

  await test.step('Import reads the exported file into the library as a new collection, under a name of its own', async () => {
    await dialog.getByTestId('template-import-file').setInputFiles({
      name: exported.name,
      mimeType: 'text/yaml',
      buffer: exported.buffer,
    });
    await expect(dialog.getByTestId('template-import-error')).toHaveCount(0);
    await expect(dialog.getByTestId('template-import-summary')).toHaveText(
      `${set}: 2 templates and 1 custom icon. The library has a collection of that name, so Import adds it as ${set} (2).`,
    );

    const added = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname.endsWith('/builder/templates/items'),
    );

    await dialog.getByTestId('template-import-submit').click();

    const answer = await added;

    expect(answer.status()).toBe(201);
    expect(answer.request().postDataJSON()).toMatchObject({
      templates: [
        { name: names[0], description: 'Carries its icon' },
        { name: names[1] },
      ],
      collection: { name: `${set} (2)`, description: 'Exported by a test' },
    });
    imported = await answer.json();
    await expect(dialog).toHaveCount(0);
    await expect(builder).toHaveAnnounced(
      `Imported 2 templates as collection ${set} (2).`,
    );
    // The icon library held the icon with the same bytes: nothing was
    // uploaded, nothing warned, and the dialog gave focus back.
    await expect.soft(page.getByTestId('templates-import')).toBeFocused();

    const stored = await readLibrary(request);
    const collection = stored.collections.find(
      (entry) => entry.id === imported.collection.id,
    );

    expect(collection).toMatchObject({
      name: `${set} (2)`,
      source: 'library',
    });
    expect(collection.templateIds).toEqual(
      imported.created.map((entry) => entry.id),
    );
    expect(
      stored.templates.find((entry) => entry.id === imported.created[0].id)
        .device,
    ).toMatchObject({
      icon: icon.name,
      spec: { general: { hostname: 'file-alfa' } },
    });

    await library.show.selectOption(imported.collection.id);
    await expect(library.card(imported.created[0].id)).toBeVisible();
    await expect(library.card(imported.created[1].id)).toBeVisible();
  });

  await test.step('a template of the imported collection makes a device', async () => {
    await page.getByTestId('drafts-tab-drafts').click();
    await page.getByTestId(`draft-open-${draft.id}`).click();
    await expect(builder.canvas).toBeVisible();
    await palette.own(imported.created[0].id).click();
    await builder.persisted(draft, hostnames, ['plc-01', 'file-alfa']);

    const doc = await builder.serverDocument(draft);

    expect.soft(byHostname(doc, 'file-alfa').device).toMatchObject({
      icon: icon.name,
    });
    await builder.waitSaved();
  });

  expectNoFatal(issues);
});
