// Layer sets: the Layers tab, the layers editor and a diagram's layers. The
// default-set test answers its routes itself: the server's default stays unset.

const fs = require('node:fs');

const {
  API,
  test,
  expect,
  expectAccessible,
  expectNoFatal,
  labDocument,
  seedLayerSet,
  uniqueName,
} = require('./builder-support');

const PURDUE = 'builtin:purdue';
const LAYER_SETS = `${API}/builder/layersets`;

// The layers of a set of the test's own: Operations, with a node matcher
// for host names that start with "ops-" and a color, and Control.
function plantLayers() {
  return [
    {
      id: 3,
      label: 'Operations',
      iconKey: 'terminal',
      color: '#0f766e',
      matchers: { 'general.hostname': '^ops-' },
    },
    { id: 1, label: 'Control', iconKey: 'plc' },
  ];
}

function layerSetsTab(page) {
  const panel = page.getByTestId('layer-sets-tab');

  return {
    tab: page.getByTestId('drafts-tab-layers'),
    panel,
    status: panel.getByTestId('layer-sets-status'),
    card: (id) => panel.locator(`[data-layer-set="${id}"]`),
    named: (name) =>
      panel.locator('[data-layer-set]').filter({
        has: page.getByRole('heading', { name: new RegExp(`^${name}`) }),
      }),
  };
}

async function openLayersTab(builder) {
  await builder.open();

  const tab = layerSetsTab(builder.page);

  await tab.tab.click();
  await expect(tab.card(PURDUE)).toBeVisible();

  return tab;
}

// A device of a document by its host name.
function deviceOf(doc, hostname) {
  return doc.nodes.find((node) => node.device?.hostname === hostname).device;
}

// The outline color a node is drawn with (see nodeColors.js), '' for none.
function outlineOf(node) {
  return node.evaluate((element) =>
    getComputedStyle(element).getPropertyValue('--bx-node-outline').trim(),
  );
}

// Opens the layers editor of the open diagram from the Inspector's Diagram
// section.
async function openDiagramLayers(builder) {
  await builder.canvas.focus();
  await builder.page.keyboard.press('Escape');
  await builder.inspector.getByTestId('inspector-layers-edit').click();

  const dialog = builder.page.getByTestId('layers-dialog');

  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId('layers-link')).not.toHaveText(/^Loading/);

  return dialog;
}

test.describe('the Layers tab', () => {
  test('lists the built-in Purdue Model, and adds, edits, duplicates and deletes a set', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const name = uniqueName(testInfo, 'set');
    const tab = await openLayersTab(builder);
    const dialog = page.getByTestId('layers-dialog');

    await test.step('the Purdue Model is built in and read only', async () => {
      const purdue = tab.card(PURDUE);

      await expect
        .soft(purdue.getByRole('heading', { level: 2 }))
        .toHaveText(/Purdue Model\s*Built-in/);
      await expect
        .soft(purdue.getByTestId('layer-set-count'))
        .toHaveText('7 layers. Read only.');
      await expect
        .soft(purdue.getByTestId('layer-set-layers').getByRole('listitem'))
        .toHaveText([
          'L5 Enterprise',
          'L4 Business',
          'L3.5 Industrial DMZ',
          'L3 Operations',
          'L2 Supervisory control',
          'L1 Basic control',
          'L0 Process',
        ]);
      await expect.soft(purdue.getByTestId('layer-set-delete')).toHaveCount(0);
      // A read-only set has View, with an eye, in place of Edit.
      await expect
        .soft(purdue.getByTestId('layer-set-edit'))
        .toHaveText('View');
      await expect
        .soft(
          purdue.getByTestId('layer-set-edit').locator('.builder-icon--eye'),
        )
        .toHaveCount(1);

      await purdue.getByRole('button', { name: 'View Purdue Model' }).click();
      await expect(dialog).toBeVisible();
      await expect.soft(dialog).toHaveAccessibleName('Layer set Purdue Model');
      await expect.soft(dialog.getByTestId('layers-builtin')).toBeVisible();
      await expect
        .soft(dialog.getByTestId('layers-set-name'))
        .toHaveAttribute('readonly', '');
      await expect.soft(dialog.getByTestId('layers-save')).toHaveCount(0);
      await expect.soft(dialog.getByTestId('layer-remove')).toHaveCount(0);
      await dialog.getByTestId('layers-done').click();
      await expect(dialog).toBeHidden();
    });

    await test.step('New layer set starts with the placeholder layers, and Save adds it', async () => {
      await page.getByTestId('layer-sets-new').click();
      await expect(dialog).toBeVisible();
      await expect.soft(dialog).toHaveAccessibleName('New layer set');
      await expect.soft(dialog.getByTestId('layer-row')).toHaveCount(7);

      // A set needs a name.
      await dialog.getByTestId('layers-save').click();
      await expect
        .soft(dialog.getByTestId('layers-error'))
        .toHaveText('Type a name for the layer set.');
      await expect.soft(dialog.getByTestId('layers-set-name')).toBeFocused();

      await dialog.getByTestId('layers-set-name').fill(name);
      const label = dialog.getByLabel('Layer 3 label');
      await label.fill('Operations');
      await label.press('Tab');
      await expect
        .soft(dialog.getByTestId('layers-status'))
        .toHaveText('Renamed Layer 3 to Operations.');

      // A node matcher, with a pattern the server reads differently.
      await dialog
        .getByRole('button', { name: 'Add node matcher to layer 3' })
        .click();
      const field = dialog.getByLabel('Field of node matcher 1 of layer 3');
      const pattern = dialog.getByLabel('Pattern of node matcher 1 of layer 3');
      await expect.soft(field).toBeFocused();
      await field.fill('general.hostname');
      await pattern.fill('^(?=ops)');
      await pattern.press('Tab');
      await expect
        .soft(dialog.getByTestId('layer-matcher-pattern-error'))
        .toHaveText(
          'This is not a regular expression that the server and the editor read the same way. Do not use lookarounds, backreferences, flags or \\p classes.',
        );
      await expect.soft(pattern).toHaveAttribute('aria-invalid', 'true');
      await pattern.fill('^ops-');
      await pattern.press('Tab');
      await expect
        .soft(dialog.getByTestId('layers-status'))
        .toHaveText('Changed the node matchers of layer Operations.');

      await dialog.getByTestId('layers-conflict').selectOption('highest');
      await dialog.getByTestId('layers-save').click();
      await expect(dialog).toBeHidden();
      await expect.soft(tab.status).toHaveText(`Added layer set ${name}.`);
      await expect
        .soft(page.getByRole('button', { name: `Edit ${name}` }))
        .toBeFocused();
    });

    const id = await tab.named(name).getAttribute('data-layer-set');
    const stored = async () =>
      (await (await page.request.get(`${LAYER_SETS}/${id}`)).json()) || {};

    await expect
      .poll(async () => {
        const set = await stored();

        return [
          set.version,
          set.matchConflict,
          set.layers?.find((layer) => layer.id === 3),
        ];
      })
      .toEqual([
        1,
        'highest',
        {
          id: 3,
          label: 'Operations',
          matchers: { 'general.hostname': '^ops-' },
        },
      ]);

    await test.step('Edit changes the set, which takes its next version', async () => {
      await tab.named(name).getByTestId('layer-set-edit').click();
      await expect(dialog).toBeVisible();
      await expect.soft(dialog).toHaveAccessibleName(`Edit layer set ${name}`);
      await dialog.getByTestId('layers-set-description').fill('Our plant.');
      await dialog.getByTestId('layers-outlines').click();

      // Cancel asks first, as the copy changed.
      await dialog.getByTestId('layers-cancel').click();
      const confirm = page.getByTestId('builder-confirm');
      await expect.soft(confirm).toHaveAccessibleName('Close without saving?');
      await confirm.getByTestId('confirm-cancel').click();

      // Save writes a node matcher typed just before it, with no Tab.
      await dialog
        .getByRole('button', { name: 'Add node matcher to layer 1' })
        .click();
      await dialog
        .getByLabel('Field of node matcher 1 of layer 1')
        .fill('general.hostname');
      await dialog
        .getByLabel('Pattern of node matcher 1 of layer 1')
        .fill('^plc-');

      await dialog.getByTestId('layers-save').click();
      await expect(dialog).toBeHidden();
      await expect
        .soft(tab.status)
        .toHaveText(`Saved layer set ${name}, version 2.`);
      await expect
        .soft(tab.named(name).getByTestId('layer-set-description'))
        .toHaveText('Our plant.');
      const saved = await stored();
      expect.soft(saved.layerOutlines).toBe(true);
      expect
        .soft(saved.layers?.find((layer) => layer.id === 1)?.matchers)
        .toEqual({ 'general.hostname': '^plc-' });
    });

    await test.step('Duplicate adds a copy with a number', async () => {
      await tab.named(name).getByTestId('layer-set-duplicate').first().click();
      await expect
        .soft(tab.status)
        .toHaveText(`Added layer set ${name} (2), a copy of ${name}.`);
      await expect
        .soft(page.getByRole('button', { name: `Edit ${name} (2)` }))
        .toBeFocused();
    });

    await test.step('Delete asks first, and focus goes to the next card', async () => {
      const copy = tab.named(`${name} \\(2\\)`);
      await copy.getByTestId('layer-set-delete').click();
      const confirm = page.getByTestId('builder-confirm');
      await expect.soft(confirm).toHaveAccessibleName('Delete layer set?');
      await expect
        .soft(confirm)
        .toContainText('Diagrams keep their copies of its layers.');
      await confirm.getByTestId('confirm-accept').click();
      await expect
        .soft(tab.status)
        .toHaveText(`Deleted layer set ${name} (2) from the server.`);
      await expect.soft(copy).toHaveCount(0);
      await expect
        .soft(page.locator(':focus'))
        .toHaveAttribute('data-testid', 'layer-set-edit');
    });

    expectNoFatal(issues);
  });

  test('a set downloads as a YAML file, and the file uploads as a new set', async ({
    page,
    builder,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const name = uniqueName(testInfo, 'file');
    const set = await seedLayerSet(request, tracker, {
      name,
      description: 'From a file.',
      layers: plantLayers(),
    });
    const tab = await openLayersTab(builder);

    const [download] = await Promise.all([
      page.waitForEvent('download'),
      tab.card(set.id).getByTestId('layer-set-download').click(),
    ]);
    const text = fs.readFileSync(await download.path(), 'utf8');

    expect.soft(download.suggestedFilename()).toBe(`${name}.layers.yaml`);
    expect
      .soft(text)
      .toMatch(
        /^\$schema: https:\/\/phenix\.sandia\.gov\/schemas\/builder\/layersets\/v1$/m,
      );
    expect.soft(text).toContain(`name: ${name}`);
    expect.soft(text).toContain('general.hostname: ^ops-');
    await expect
      .soft(tab.status)
      .toHaveText(`Downloaded 2 layers to ${name}.layers.yaml.`);

    await test.step('Upload adds the file as a new set, with a number', async () => {
      await tab.panel.getByTestId('layer-sets-file').setInputFiles({
        name: `${name}.layers.yaml`,
        mimeType: 'text/yaml',
        buffer: Buffer.from(text),
      });
      await expect
        .soft(tab.status)
        .toHaveText(
          `Uploaded ${name}.layers.yaml as layer set ${name} (2), with 2 layers.`,
        );
      await expect
        .soft(page.getByRole('button', { name: `Edit ${name} (2)` }))
        .toBeFocused();
      await expect
        .soft(
          tab
            .named(`${name} \\(2\\)`)
            .getByTestId('layer-set-layers')
            .getByRole('listitem'),
        )
        .toHaveText(['Operations', 'Control']);
    });

    await test.step('a file that is not a layer set says why', async () => {
      await tab.panel.getByTestId('layer-sets-file').setInputFiles({
        name: 'empty.layers.yaml',
        mimeType: 'text/yaml',
        buffer: Buffer.from(
          '$schema: https://phenix.sandia.gov/schemas/builder/layersets/v1\nname: Empty\nlayers: []\n',
        ),
      });
      await expect
        .soft(tab.panel.getByTestId('layer-sets-error'))
        .toContainText('Could not upload empty.layers.yaml.');
      await expect
        .soft(tab.panel.getByTestId('layer-sets-issues'))
        .toContainText('layers');
    });

    expectNoFatal(issues);
  });

  test('no label of a card button wraps or overflows, and the buttons keep two equal columns, also on a narrow screen', async ({
    page,
    builder,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const set = await seedLayerSet(request, tracker, {
      name: uniqueName(testInfo, 'labels'),
      description: 'For the labels.',
      layers: plantLayers(),
    });
    const tab = await openLayersTab(builder);

    // The most lines that a text of a button takes.
    const lines = (button) =>
      button.evaluate((element) => {
        const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
        let most = 0;

        for (let node = walker.nextNode(); node; node = walker.nextNode()) {
          if (node.textContent.trim()) {
            const range = document.createRange();

            range.selectNodeContents(node);
            most = Math.max(
              most,
              new Set(
                [...range.getClientRects()].map((rect) => Math.round(rect.top)),
              ).size,
            );
          }
        }

        return most;
      });
    const viewport = page.viewportSize();

    for (const width of [viewport.width, 360]) {
      await page.setViewportSize({ width, height: viewport.height });
      for (const id of [PURDUE, set.id]) {
        const buttons = tab.card(id).locator('.builder-card__actions button');

        for (const button of await buttons.all()) {
          expect
            .soft(
              await lines(button),
              `${width}px: ${await button.textContent()}`,
            )
            .toBe(1);
        }

        // A button is a column or the whole row wide. Its label ends inside
        // it, and its icon is at the left padding.
        const boxes = await buttons.evaluateAll((elements) =>
          elements.map((element) => {
            const box = element.getBoundingClientRect();
            const icon = element.querySelector('svg').getBoundingClientRect();
            const label = element
              .querySelector('.builder-button__swap > :not(.is-off)')
              .getBoundingClientRect();
            const style = getComputedStyle(element);

            return {
              text: element.textContent.trim(),
              width: box.width,
              start:
                icon.left -
                box.left -
                parseFloat(style.paddingLeft) -
                parseFloat(style.borderLeftWidth),
              over: label.right - (box.right - parseFloat(style.paddingRight)),
            };
          }),
        );
        const column = Math.min(...boxes.map((box) => box.width));
        const row = Math.max(...boxes.map((box) => box.width));

        for (const box of boxes) {
          const at = `${width}px: ${box.text}`;

          expect
            .soft(
              Math.min(Math.abs(box.width - column), Math.abs(box.width - row)),
              `${at} width`,
            )
            .toBeLessThan(0.5);
          expect.soft(Math.abs(box.start), `${at} icon`).toBeLessThan(0.5);
          expect.soft(box.over, `${at} label inside`).toBeLessThanOrEqual(0.5);
        }
      }
    }
    await page.setViewportSize(viewport);

    expectNoFatal(issues);
  });

  test('the filter finds a set, and Download selected and Delete selected act on the selection but keep the built-in set', async ({
    page,
    builder,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const names = ['a', 'b'].map((part) =>
      uniqueName(testInfo, `bulk-${part}`),
    );
    const sets = [];

    for (const name of names) {
      sets.push(
        await seedLayerSet(request, tracker, {
          name,
          description: 'For Delete selected.',
          layers: plantLayers(),
        }),
      );
    }

    const tab = await openLayersTab(builder);
    const filter = tab.panel.getByTestId('layer-sets-filter');
    const count = tab.panel.getByTestId('bulk-count-layer-sets');
    const select = (id) => tab.card(id).getByTestId('layer-set-select');

    await test.step('the filter shows the sets whose name matches, and says how many', async () => {
      await expect.soft(filter).toHaveAccessibleName('Filter layer sets');
      await filter.fill(names[0]);
      await expect(tab.panel.locator('[data-layer-set]')).toHaveCount(1);
      await expect.soft(tab.card(sets[0].id)).toBeVisible();
      await expect
        .soft(tab.panel.getByTestId('layer-sets-filter-count'))
        .toHaveText(/^1 of \d+ layer sets shown\.$/);
      await expect.soft(filter).toHaveAccessibleDescription(/^1 of \d+/);
      await filter.press('Escape');
      await expect(filter).toHaveValue('');
      await expect.soft(tab.card(PURDUE)).toBeVisible();
    });

    await test.step('Download selected saves a layer set file for each selected set', async () => {
      await select(sets[0].id).check();
      await select(sets[1].id).check();
      await expect(count).toHaveText(/^2 of \d+ selected$/);

      const download = tab.panel.getByTestId('bulk-download-layer-sets');
      const files = [];

      page.on('download', (file) => files.push(file));
      await download.click();
      await expect.soft(tab.status).toHaveText('Downloaded 2 layer sets.');
      await expect.poll(() => files.length).toBe(2);
      expect
        .soft(files.map((file) => file.suggestedFilename()).sort())
        .toEqual(names.map((name) => `${name}.layers.yaml`).sort());
      await expect.soft(download).toBeFocused();
    });

    await test.step('Delete selected asks once, keeps the built-in set, and lists it in the summary', async () => {
      await select(PURDUE).check();
      await expect(count).toHaveText(/^3 of \d+ selected$/);
      await tab.panel.getByTestId('bulk-delete-layer-sets').click();

      const confirm = page.getByTestId('builder-confirm');

      await expect.soft(confirm).toHaveAccessibleName('Delete 2 layer sets?');
      await expect
        .soft(confirm)
        .toContainText(
          'Purdue Model stays because you cannot delete a built-in layer set.',
        );
      await confirm.getByTestId('confirm-accept').click();

      const summary = page.getByTestId('bulk-summary');

      await expect(summary).toBeFocused();
      await expect
        .soft(summary)
        .toContainText(
          '1 of 3 layer sets could not be deleted. The other 2 were deleted.',
        );
      await expect
        .soft(summary)
        .toContainText('Purdue Model: You cannot delete a built-in layer set.');
      for (const set of sets) {
        await expect.soft(tab.card(set.id)).toHaveCount(0);
      }
      // The built-in set stays selected.
      await expect.soft(select(PURDUE)).toBeChecked();
      await summary.getByTestId('bulk-summary-dismiss').click();
      await expect
        .soft(tab.panel.getByTestId('bulk-all-layer-sets'))
        .toBeFocused();
    });

    expectNoFatal(issues);
  });

  // The default is the same for every test, so the test answers its routes.
  test('Set default makes new blank diagrams copy the set', async ({
    page,
    builder,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const name = uniqueName(testInfo, 'default');
    const set = await seedLayerSet(request, tracker, {
      name,
      layers: plantLayers(),
    });
    let chosen = null;
    // The tag of the choice, which each change names and moves on.
    let version = 0;
    const sent = [];

    await page.route(`**${LAYER_SETS}/default`, async (route) => {
      if (route.request().method() === 'PUT') {
        sent.push(route.request().headers()['if-match']);
        chosen = route.request().postDataJSON()?.id || null;
        version += 1;
      }

      await route.fulfill({ json: { id: chosen, etag: `"${version}"` } });
    });
    await page.route(`**${LAYER_SETS}`, async (route) => {
      if (route.request().method() !== 'GET') {
        await route.fallback();

        return;
      }

      const response = await route.fetch();
      const json = await response.json();

      await route.fulfill({
        response,
        json: { ...json, defaultId: chosen, defaultEtag: `"${version}"` },
      });
    });

    const tab = await openLayersTab(builder);
    const card = tab.card(set.id);

    await card.getByRole('button', { name: `Set default: ${name}` }).click();
    await expect
      .soft(tab.status)
      .toHaveText(`New blank diagrams start with a copy of ${name}.`);
    await expect
      .soft(card.getByTestId('layer-set-default'))
      .toHaveText('Default');
    expect.soft(chosen).toBe(set.id);

    const draft = await builder.createBlank();

    await builder.persisted(
      draft,
      (doc) => [doc.layers.map((layer) => layer.label), doc.layerSet],
      [['Operations', 'Control'], { name, source: { id: set.id, version: 1 } }],
    );
    await expect
      .soft(builder.inspector.getByTestId('inspector-layers-link'))
      .toHaveText(`From layer set ${name}, version 1.`);

    await builder.backToDrafts();
    await tab.tab.click();
    await card.getByRole('button', { name: `Clear default: ${name}` }).click();
    await expect
      .soft(tab.status)
      .toHaveText('New blank diagrams start with the placeholder layers.');
    await expect.soft(card.getByTestId('layer-set-default')).toHaveCount(0);
    expect.soft(chosen).toBeNull();
    // Each change named the tag of the choice it changed.
    expect.soft(sent).toEqual(['"0"', '"1"']);
    expectNoFatal(issues);
  });
});

test.describe('the layers of a diagram', () => {
  test('Apply copies the Purdue Model into the diagram and links it, in one undo step', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const draft = await builder.seedDraft(
      labDocument(uniqueName(testInfo, 'purdue'), { hostnames: ['hmi-01'] }),
    );
    await builder.openDraft(draft);

    const dialog = await openDiagramLayers(builder);
    await expect
      .soft(dialog.getByTestId('layers-link'))
      .toHaveText('These layers are not linked to a layer set.');
    await dialog
      .getByTestId('layers-choose')
      .selectOption({ label: 'Purdue Model (built-in)' });
    await dialog.getByTestId('layers-apply').click();
    await expect
      .soft(dialog.getByTestId('layers-status'))
      .toHaveText('Applied layer set Purdue Model.');
    await expect
      .soft(dialog.getByTestId('layers-link'))
      .toHaveText('Linked to layer set Purdue Model, version 1.');
    await expect.soft(dialog.getByTestId('layer-row')).toHaveCount(7);
    // A built-in set cannot take the diagram's changes.
    await expect.soft(dialog.getByTestId('layers-save-to-set')).toHaveCount(0);

    await builder.persisted(
      draft,
      (doc) => [doc.layers.length, doc.layers[0], doc.layerSet],
      [
        7,
        {
          id: 6,
          label: 'L5 Enterprise',
          iconKey: 'server',
          color: '#2f6fbf',
        },
        { name: 'Purdue Model', source: { id: PURDUE, version: 1 } },
      ],
    );

    await dialog.getByTestId('layers-done').click();
    await expect
      .soft(
        builder.inspector
          .getByTestId('inspector-layers-list')
          .getByRole('listitem'),
      )
      .toHaveCount(7);

    await builder.selectNode('hmi-01');
    const layer = builder.inspector.getByLabel('Layer', { exact: true });
    await layer.selectOption({ label: 'L3 Operations' });
    await expect
      .soft(builder.inspector.getByTestId('inspector-layer-look'))
      .toBeVisible();

    // Undo takes the whole copy back.
    await builder.canvas.focus();
    await page.keyboard.press('ControlOrMeta+z');
    await page.keyboard.press('ControlOrMeta+z');
    await builder.persisted(
      draft,
      (doc) => ['layers' in doc, 'layerSet' in doc],
      [false, false],
    );
    expectNoFatal(issues);
  });

  test('a node matcher puts a device at a layer as its host name changes, and a chosen layer goes first', async ({
    builder,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const name = uniqueName(testInfo, 'match');
    const set = await seedLayerSet(request, tracker, {
      name,
      layers: plantLayers(),
    });
    const draft = await builder.seedDraft(
      labDocument(uniqueName(testInfo, 'match'), { hostnames: ['web-01'] }),
    );
    await builder.openDraft(draft);

    const dialog = await openDiagramLayers(builder);
    await dialog.getByTestId('layers-choose').selectOption(set.id);
    await dialog.getByTestId('layers-apply').click();
    await dialog.getByTestId('layers-done').click();

    await builder.selectNode('web-01');
    const layer = builder.inspector.getByLabel('Layer', { exact: true });
    const hostname = builder.inspector.getByLabel(/^Hostname/).first();

    await expect.soft(layer.locator('option').first()).toHaveText('None');

    await test.step('a new host name that matches moves the device', async () => {
      await hostname.fill('ops-01');
      await builder.inspector.getByTestId('inspector-apply').click();
      await expect
        .soft(layer.locator('option').first())
        .toHaveText('Operations (matched general.hostname)');
      await expect.soft(layer).toHaveValue('');
      // The device keeps no layer of its own: the match is live.
      await builder.persisted(
        draft,
        (doc) => 'layer' in deviceOf(doc, 'ops-01'),
        false,
      );
    });

    await test.step('a layer chosen by hand goes first, and Use matched layer takes it back', async () => {
      await layer.selectOption({ label: 'Control' });
      await expect
        .soft(builder.inspector.getByTestId('inspector-layer-matched'))
        .toHaveText(
          'Its fields match Operations (general.hostname). The layer that you chose goes first.',
        );
      await builder.persisted(draft, (doc) => deviceOf(doc, 'ops-01').layer, 1);

      const use = builder.inspector.getByRole('button', {
        name: 'Use matched layer',
      });
      await use.click();
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Changed the layer of Device ops-01 to the matched layer Operations',
        );
      await expect.soft(layer).toBeFocused();
      await expect.soft(use).toHaveCount(0);
      await builder.persisted(
        draft,
        (doc) => 'layer' in deviceOf(doc, 'ops-01'),
        false,
      );
    });

    await test.step('a host name that no longer matches leaves the layer', async () => {
      await hostname.fill('db-01');
      await builder.inspector.getByTestId('inspector-apply').click();
      await expect.soft(layer.locator('option').first()).toHaveText('None');
    });

    expectNoFatal(issues);
  });

  test('the command palette sets the layer of the selection in one undo step, and selects the nodes on a layer', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const doc = labDocument(uniqueName(testInfo, 'palette'), {
      hostnames: ['ops-01', 'web-01', 'web-02'],
    });

    doc.layers = plantLayers();

    const draft = await builder.seedDraft(doc);
    const commands = page.getByTestId('commands-dialog');
    const field = page.getByTestId('command-palette-input');
    const active = commands.getByRole('option', { selected: true });
    const selected = page.locator('.vue-flow__node[aria-pressed="true"]');
    const layers = (saved) =>
      ['ops-01', 'web-01', 'web-02'].map(
        (hostname) => deviceOf(saved, hostname).layer ?? null,
      );

    await builder.openDraft(draft);

    await test.step('Set layer of selection lists the layers, with their icons', async () => {
      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+a');
      await expect(selected).toHaveCount(4);
      await page.keyboard.press('ControlOrMeta+k');
      await field.fill('set layer');
      await expect(active).toContainText('Set layer of selection');
      await expect
        .soft(active)
        .toHaveAccessibleDescription('3 devices and 1 switch selected');
      await page.keyboard.press('Enter');
      await expect(commands.getByRole('option')).toHaveText([
        /Operations/,
        /Control/,
        /No layer/,
      ]);
      // Each layer with an icon shows it, as the Inspector does.
      await expect
        .soft(commands.getByTestId('command-palette-layer'))
        .toHaveCount(2);

      // A layer's icon takes the colors of the other icons: the accent
      // color while its option is active, else the muted text color.
      const colorOf = (icon) =>
        icon.evaluate((element) => getComputedStyle(element).color);
      const layerIcon = (name) =>
        commands.getByRole('option', { name }).getByTestId('layer-name-icon');
      const plainIcon = commands
        .getByRole('option', { name: 'No layer' })
        .locator('.builder-icon');
      await expect(active).toContainText('Operations');
      const accent = await colorOf(layerIcon('Operations'));
      const muted = await colorOf(plainIcon);
      expect.soft(accent).not.toBe(muted);
      await expect.soft(layerIcon('Control')).toHaveCSS('color', muted);
      await page.keyboard.press('ArrowDown');
      await page.keyboard.press('ArrowDown');
      await expect(active).toContainText('No layer');
      await expect.soft(plainIcon).toHaveCSS('color', accent);
      await expect.soft(layerIcon('Operations')).toHaveCSS('color', muted);
    });

    await test.step('a layer chosen goes to every selected node, as one step', async () => {
      await field.fill('control');
      await expect(active).toContainText('Control');
      await page.keyboard.press('Enter');
      await expect(commands).toHaveCount(0);
      await expect
        .soft(builder)
        .toHaveAnnounced('Changed the layer of 4 nodes to Control');
      await builder.persisted(draft, layers, [1, 1, 1]);

      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+z');
      await builder.persisted(draft, layers, [null, null, null]);
    });

    await test.step('Select nodes on layer selects the nodes a matcher puts there', async () => {
      await page.keyboard.press('ControlOrMeta+k');
      await field.fill('select nodes on layer');
      await expect(active).toContainText('Select nodes on layer');
      await page.keyboard.press('Enter');
      await expect(active).toContainText('Operations');
      await expect.soft(active).toHaveAccessibleDescription('1 node');
      await page.keyboard.press('Enter');
      await expect(commands).toHaveCount(0);
      await expect
        .soft(builder)
        .toHaveAnnounced('Selected 1 node on layer Operations');
      await expect(selected).toHaveCount(1);
      await expect.soft(selected).toContainText('ops-01');
    });

    expectNoFatal(issues);
  });

  test('outlines take the layer color, and an outline color of the node goes first', async ({
    builder,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const set = await seedLayerSet(request, tracker, {
      name: uniqueName(testInfo, 'outline'),
      layers: plantLayers(),
    });
    const draft = await builder.seedDraft(
      labDocument(uniqueName(testInfo, 'outline'), {
        hostnames: ['ops-01', 'ops-02'],
      }),
    );
    await builder.openDraft(draft);

    const first = builder.node('ops-01', 'device');
    const second = builder.node('ops-02', 'device');

    const dialog = await openDiagramLayers(builder);
    await dialog.getByTestId('layers-choose').selectOption(set.id);
    await dialog.getByTestId('layers-apply').click();
    await expect.poll(() => outlineOf(first)).toBe('');

    const outlines = dialog.getByTestId('layers-outlines');
    await expect.soft(outlines).toHaveAttribute('aria-checked', 'false');
    await outlines.click();
    await expect.soft(outlines).toHaveAttribute('aria-checked', 'true');
    await expect
      .soft(dialog.getByTestId('layers-status'))
      .toHaveText('Turned on layer colors for node outlines.');
    await builder.persisted(draft, (doc) => doc.layerSet.layerOutlines, true);
    // The diagram's copy now differs from the set.
    await expect
      .soft(dialog.getByTestId('layers-link'))
      .toContainText('You changed the layers in this diagram after the copy.');
    await dialog.getByTestId('layers-done').click();

    await expect.poll(() => outlineOf(first)).toBe('#0f766e');
    await expect.poll(() => outlineOf(second)).toBe('#0f766e');

    // A node's own outline color goes before the layer's.
    await builder.selectNode('ops-02');
    const own = builder.inspector
      .locator('[data-path="outlineColor"]')
      .locator('input[type="text"]');
    await own.fill('#a3273f');
    await own.press('Tab');
    await expect.poll(() => outlineOf(second)).toBe('#a3273f');
    await expect.poll(() => outlineOf(first)).toBe('#0f766e');
    expectNoFatal(issues);
  });

  test('Update from layer set copies a newer version, and Save to layer set and Save as new set write the copy', async ({
    builder,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const name = uniqueName(testInfo, 'update');
    const set = await seedLayerSet(request, tracker, {
      name,
      layers: plantLayers(),
    });
    const draft = await builder.seedDraft(
      labDocument(uniqueName(testInfo, 'update'), { hostnames: ['web-01'] }),
    );
    await builder.openDraft(draft);

    let dialog = await openDiagramLayers(builder);
    await dialog.getByTestId('layers-choose').selectOption(set.id);
    await dialog.getByTestId('layers-apply').click();
    await dialog.getByTestId('layers-done').click();

    await test.step('a newer version of the set offers Update from layer set', async () => {
      const current = await request.get(`${LAYER_SETS}/${set.id}`);
      const changed = await request.put(`${LAYER_SETS}/${set.id}`, {
        headers: { 'If-Match': current.headers().etag },
        data: {
          name,
          layers: [...plantLayers(), { id: 0, label: 'Field' }],
        },
      });
      expect(changed.status(), await changed.text()).toBe(200);

      // Coming back to the page reads the library again, so the Inspector
      // offers the update without the layers editor.
      await builder.page.evaluate(() => {
        document.dispatchEvent(new Event('visibilitychange'));
        window.dispatchEvent(new Event('focus'));
      });
      await expect(
        builder.inspector.getByTestId('inspector-layers-update'),
      ).toBeVisible();

      dialog = await openDiagramLayers(builder);
      await expect
        .soft(dialog.getByTestId('layers-link'))
        .toHaveText(
          `Layer set ${name} has a newer version, 2. This diagram has a copy of version 1.`,
        );
      await dialog.getByTestId('layers-done').click();

      const update = builder.inspector.getByTestId('inspector-layers-update');
      await expect
        .soft(builder.inspector.getByTestId('inspector-layers-link'))
        .toHaveText(
          `Layer set ${name} has a newer version, 2. This diagram has version 1.`,
        );
      await update.click();
      await expect
        .soft(builder)
        .toHaveAnnounced(
          `Updated the layers from layer set ${name}, version 2`,
        );
      await expect
        .soft(builder.inspector.getByTestId('inspector-layers-edit'))
        .toBeFocused();
      await builder.persisted(
        draft,
        (doc) => [doc.layers.length, doc.layerSet.source.version],
        [3, 2],
      );
    });

    await test.step('Save to layer set writes the changed copy to the set', async () => {
      dialog = await openDiagramLayers(builder);
      const label = dialog.getByLabel('Layer 0 label');
      await label.fill('Process');
      await label.press('Tab');
      await expect
        .soft(dialog.getByTestId('layers-link'))
        .toHaveText(
          `Linked to layer set ${name}, version 2. You changed the layers in this diagram after the copy.`,
        );
      await dialog.getByTestId('layers-save-to-set').click();
      await expect
        .soft(dialog.getByTestId('layers-status'))
        .toHaveText(`Saved the layers to layer set ${name}, version 3.`);
      await expect
        .soft(dialog.getByTestId('layers-link'))
        .toHaveText(`Linked to layer set ${name}, version 3.`);
      await expect.soft(dialog.getByTestId('layers-save-new')).toBeFocused();
      const stored = await (
        await request.get(`${LAYER_SETS}/${set.id}`)
      ).json();
      expect.soft(stored.layers.at(-1)).toEqual({ id: 0, label: 'Process' });
    });

    await test.step('Save as new set adds the copy as a set and links the diagram to it', async () => {
      const copy = `${name} copy`;

      // Cancel closes the form, and focus goes back to the button.
      await dialog.getByTestId('layers-save-new').click();
      await dialog.getByTestId('layers-new-cancel').click();
      await expect(dialog.getByTestId('layers-new-set')).toHaveCount(0);
      await expect.soft(dialog.getByTestId('layers-save-new')).toBeFocused();

      await dialog.getByTestId('layers-save-new').click();
      await dialog.getByTestId('layers-new-name').fill(copy);
      await dialog.getByTestId('layers-new-save').click();
      await expect
        .soft(dialog.getByTestId('layers-status'))
        .toHaveText(`Added layer set ${copy}, and linked the diagram to it.`);
      await expect
        .soft(dialog.getByTestId('layers-link'))
        .toHaveText(`Linked to layer set ${copy}, version 1.`);
      await builder.persisted(
        draft,
        (doc) => [doc.layerSet.name, doc.layerSet.source.version],
        [copy, 1],
      );

      await dialog.getByTestId('layers-unlink').click();
      await expect
        .soft(dialog.getByTestId('layers-link'))
        .toHaveText('These layers are not linked to a layer set.');
      // Unlink goes with the link, and focus does not go to the page.
      await expect.soft(dialog.getByTestId('layers-save-new')).toBeFocused();
      await builder.persisted(
        draft,
        (doc) => [doc.layers.length, doc.layerSet?.source],
        [3, undefined],
      );
    });

    expectNoFatal(issues);
  });
});

// axe on the Layers tab and the layers editor of a set, in either theme.
test(
  'axe finds no serious violations in the Layers tab and the layers editor in the light and the dark theme',
  { tag: '@axe' },
  async ({ page, builder, request, tracker, issues }, testInfo) => {
    await seedLayerSet(request, tracker, {
      name: uniqueName(testInfo, 'axe'),
      description: 'A set for the scan.',
      layers: plantLayers(),
    });

    for (const scheme of ['light', 'dark']) {
      await page.emulateMedia({ colorScheme: scheme });

      const tab = await openLayersTab(builder);

      await expectAccessible(page, {
        soft: true,
        include: '#panel-layers',
        label: `axe on the Layers tab (${scheme})`,
      });

      await tab.card(PURDUE).getByTestId('layer-set-duplicate').click();
      await expect(tab.status).toContainText('a copy of Purdue Model.');
      await page.locator(':focus').click();

      const dialog = page.getByTestId('layers-dialog');

      await expect(dialog).toBeVisible();
      await dialog
        .getByRole('button', { name: 'Add node matcher to layer 3' })
        .click();
      await expectAccessible(page, {
        soft: true,
        include: '[data-testid="layers-dialog"]',
        label: `axe on the layers editor of a set (${scheme})`,
      });
      await dialog.getByTestId('layers-cancel').click();
      await expect(dialog).toBeHidden();
    }

    await page.emulateMedia({ colorScheme: null });
    expectNoFatal(issues);
  },
);
