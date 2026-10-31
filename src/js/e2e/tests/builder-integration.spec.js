// Builder integration tests: the header link, the Configs page hand-off and
// the removed legacy API routes.

const crypto = require('node:crypto');

const {
  API,
  blankDocument,
  expect,
  expectAccessible,
  expectNoFatal,
  openConfigs,
  publishTopology,
  seedConfig,
  test,
  uniqueName,
  visit,
  waitForApi,
  watchDraftCreates,
} = require('./builder-support');

function topology(name, annotations) {
  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: annotations ? { name, annotations } : { name },
    spec: { nodes: [] },
  };
}

// A legacy mxGraph diagram holding one labelled rectangle.
function legacyXml(label) {
  return [
    '<mxGraphModel><root>',
    '<mxCell id="0"/><mxCell id="1" parent="0"/>',
    `<mxCell id="2" value="${label}" style="rounded=0;whiteSpace=wrap;html=1;"`,
    ' vertex="1" parent="1">',
    '<mxGeometry x="80" y="80" width="160" height="60" as="geometry"/>',
    '</mxCell></root></mxGraphModel>',
  ].join('');
}

// A Builder document with one manually authored device and no source, so
// publishing it does not depend on any stored config.
function deviceDocument(name, hostname) {
  return blankDocument(name, {
    nodes: [
      {
        id: crypto.randomUUID(),
        kind: 'device',
        label: hostname,
        position: { x: 0, y: 0 },
        device: {
          hostname,
          iconKey: 'linux',
          spec: {
            type: 'VirtualMachine',
            general: { hostname },
            hardware: {
              os_type: 'linux',
              vcpus: 1,
              memory: 1024,
              drives: [{ image: 'ubuntu.qc2' }],
            },
            network: { interfaces: [] },
          },
          interfaces: [],
        },
      },
    ],
  });
}

function configRow(page, name) {
  return page.locator('tr', { hasText: name });
}

// The Builder tag of a topology's row, where it is a link into the Builder.
function builderTag(page, name) {
  return page.locator(`[data-config-builder="Topology/${name}"]`);
}

// The row's icon-only edit button, found by its tooltip label rather than by
// its position among the row actions.
function editButton(row) {
  return row
    .locator('.b-tooltip')
    .filter({ hasText: 'edit config file' })
    .getByRole('button');
}

async function editConfig(page, name) {
  const fetched = waitForApi(page, 'GET', `/configs/Topology/${name}`);
  await editButton(configRow(page, name)).click();
  await fetched;
}

// A dialog's opacity, as a string: Buefy fades a modal in and out.
function opacityOf(dialog) {
  return dialog.evaluate((modal) => getComputedStyle(modal).opacity);
}

test.describe('header', () => {
  test('links to the Builder', async ({ page, issues, tracker }) => {
    await visit(page, '/experiments');

    // One link is named Builder, with no tag in its name.
    const nav = page.getByRole('link', { name: 'Builder', exact: true });
    await expect(nav).toBeVisible({ timeout: 20000 });
    await expect(nav).toHaveCount(1);
    await expect(nav).toHaveAttribute('data-testid', 'nav-builder');
    await expect(nav).toHaveAccessibleName('Builder');
    await expect(nav.locator('.tag')).toHaveCount(0);
    // It opens the editor in this window, and no header link carries a
    // session token.
    await expect.soft(nav).not.toHaveAttribute('target');
    await expect.soft(nav).toHaveAttribute('href', /\/builder$/);
    await expect.soft(page.locator('.navbar a[href*="token="]')).toHaveCount(0);

    await nav.click();
    await expect(page).toHaveURL(/\/builder$/);
    await expect(
      page.getByRole('heading', { name: 'Builder', exact: true }),
    ).toBeVisible();

    await test.step('over plain HTTP from another host the editor works', async () => {
      // A made-up host, routed to the server, is no secure context (localhost
      // is), so it has no crypto.randomUUID or crypto.subtle.
      const base = new URL(page.url()).origin;
      const origin = 'http://builder.test';
      const other = await page.context().newPage();
      const errors = [];
      other.on('pageerror', (error) => errors.push(String(error)));
      tracker.watch(other);
      // The made-up host has no websocket server. An open mock keeps the
      // app's "connection closed" toast from covering the page.
      await other.routeWebSocket(/^wss?:\/\/builder\.test\//, () => {});
      await other.route(`${origin}/**`, async (route) => {
        const request = route.request();
        const response = await route.fetch({
          url: request.url().replace(origin, base),
          // The Vite dev server refuses hosts it does not know.
          headers: {
            ...(await request.allHeaders()),
            host: new URL(base).host,
          },
        });
        await route.fulfill({ response });
      });

      await other.goto(`${origin}/builder`);
      await expect(
        other.getByRole('heading', { name: 'Builder', exact: true }),
      ).toBeVisible({ timeout: 20000 });
      expect
        .soft(
          await other.evaluate(() => [
            window.isSecureContext,
            typeof crypto.randomUUID,
            typeof crypto.subtle,
          ]),
        )
        .toEqual([false, 'undefined', 'undefined']);

      // The new draft, and the device added to it, get new ids.
      const created = waitForApi(other, 'POST', '/builder/drafts');
      await other.getByTestId('drafts-blank').click();
      expect((await created).ok()).toBe(true);
      await expect(other.getByTestId('builder-canvas')).toBeVisible();
      await other.getByTestId('palette-device').click();
      await expect(other.getByTestId('builder-summary')).toContainText(
        '1 device',
      );
      await expect(other.getByTestId('builder-save-state')).toContainText(
        'All changes saved',
        { timeout: 20000 },
      );
      expect.soft(errors, 'page errors').toEqual([]);
      // The page keeps polling. A request still in flight when it closes
      // would otherwise fail the worker and skip the next test.
      await other.unrouteAll({ behavior: 'ignoreErrors' });
      await other.close();
    });

    expectNoFatal(issues);
  });
});

test.describe('Configs page', () => {
  test('tags Builder topologies with a link into the Builder, and the viewer shows each kind read only', async ({
    page,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const plain = uniqueName(testInfo, 'plain');
    const legacy = uniqueName(testInfo, 'legacy');
    const built = uniqueName(testInfo, 'built');
    await seedConfig(request, tracker, topology(plain));
    await seedConfig(
      request,
      tracker,
      topology(legacy, { 'builder-xml': legacyXml('legacy-cell') }),
    );
    await publishTopology(
      request,
      tracker,
      built,
      deviceDocument(built, 'host-a'),
    );

    await openConfigs(page);
    await expect(configRow(page, plain)).toBeVisible();

    await test.step('each Builder topology is tagged with its builder, and the tag is a link into it', async () => {
      await expect.soft(configRow(page, plain).locator('.tag')).toHaveCount(0);
      await expect
        .soft(configRow(page, plain).getByRole('link'))
        .toHaveCount(0);

      // The name starts with the text the tag shows.
      const converts = builderTag(page, legacy);
      await expect
        .soft(converts)
        .toHaveAccessibleName(
          `builder legacy: import Topology ${legacy} into the Builder`,
        );
      await expect.soft(converts).toHaveText('builder legacy');
      await expect
        .soft(converts)
        .toHaveAttribute('href', new RegExp(`/builder\\?topology=${legacy}$`));
      await expect
        .soft(
          configRow(page, legacy)
            .locator('.b-tooltip')
            .filter({ hasText: 'import into Builder' }),
        )
        .toHaveCount(1);

      const opens = builderTag(page, built);
      await expect
        .soft(opens)
        .toHaveAccessibleName(`builder: open Topology ${built} in the Builder`);
      await expect.soft(opens).toHaveText('builder');
      await expect.soft(opens).toHaveClass(/\btag\b/);
      await expect
        .soft(opens)
        .toHaveAttribute('href', new RegExp(`/builder\\?topology=${built}$`));
      await expect
        .soft(
          configRow(page, built)
            .locator('.b-tooltip')
            .filter({ hasText: 'open in Builder' }),
        )
        .toHaveCount(1);
      // A pointer target of at least 24 by 24 CSS pixels (WCAG 2.5.8).
      const box = await opens.boundingBox();
      expect.soft(box.height, 'tag height').toBeGreaterThanOrEqual(24);
      expect.soft(box.width, 'tag width').toBeGreaterThanOrEqual(24);
    });

    await test.step('a legacy Builder topology opens in the read-only viewer', async () => {
      const view = page.getByRole('button', {
        name: `View Topology ${legacy}`,
      });
      await view.click();
      const viewer = page.getByRole('dialog', { name: `Topology/${legacy}` });
      await expect(viewer).toBeVisible();
      // The diagram is left out of the text, and no error is shown.
      const text = await viewer.getByRole('textbox').inputValue();
      expect.soft(text).toContain(`name: ${legacy}`);
      expect.soft(text).toContain('builder-xml: <SNIPPED>');
      await expect.soft(page.locator('.notification.is-danger')).toHaveCount(0);
      // Its diagram is converted by an import, which the first button
      // starts.
      await expect
        .soft(viewer.locator('footer button').first())
        .toHaveText('Import into Builder');
      await expect
        .soft(viewer.locator('footer button').nth(1))
        .toHaveText('Edit Config');

      await viewer.getByRole('button', { name: 'Exit' }).click();
      await expect(viewer).toBeHidden();
      await expect.soft(view).toBeFocused();
    });

    await test.step('a Builder topology opens in the read-only viewer', async () => {
      const view = page.getByRole('button', { name: `View Topology ${built}` });
      const fetched = waitForApi(page, 'GET', `/configs/Topology/${built}`);
      // The name is a button, so the keyboard reaches the viewer too.
      await view.focus();
      await page.keyboard.press('Enter');
      await fetched;
      const viewer = page.getByRole('dialog', { name: `Topology/${built}` });
      await expect(viewer).toBeVisible();
      const text = viewer.getByRole('textbox');
      // The reference is shown as the nested map it is.
      await expect
        .soft(text)
        .toHaveValue(
          /\n( +)builder-doc:\n( +)digest: sha256:[0-9a-f]{64}\n\2id: [0-9a-f]{64}\n/,
        );
      await expect.soft(text).toHaveValue(/hostname: host-a/);
      await expect.soft(text).not.toBeEditable();
      // The Builder button comes first, left of Edit Config.
      await expect
        .soft(viewer.locator('footer button'))
        .toHaveText(['Open in Builder', 'Edit Config', '', 'Exit']);
      await expect
        .soft(viewer.getByTestId('viewer-builder'))
        .toHaveText('Open in Builder');

      await viewer.getByRole('button', { name: 'Exit' }).click();
      await expect(viewer).toBeHidden();
      await expect.soft(view).toBeFocused();
    });

    await test.step('a viewer opened as the one before it closes is shown, and Exit closes it', async () => {
      const first = page.getByRole('dialog', { name: `Topology/${legacy}` });
      await page
        .getByRole('button', { name: `View Topology ${legacy}` })
        .click();
      await expect(first).toBeVisible();
      await expect.poll(() => opacityOf(first)).toBe('1');

      // Exit, then the next name on the first frame the viewer is hidden,
      // while the page may still be removing it.
      const fetched = waitForApi(page, 'GET', `/configs/Topology/${plain}`);
      await first.evaluate(
        (modal, next) =>
          new Promise((resolve) => {
            const exit = [...modal.querySelectorAll('footer button')].find(
              (button) => button.textContent.trim() === 'Exit',
            );
            const openNext = () => {
              if (getComputedStyle(modal).display !== 'none') {
                requestAnimationFrame(openNext);
                return;
              }
              document
                .querySelector(`[data-config-view="${CSS.escape(next)}"]`)
                .click();
              resolve();
            };
            exit.click();
            requestAnimationFrame(openNext);
          }),
        `Topology/${plain}`,
      );
      await fetched;

      // It fades in, and does not stay invisible over the page.
      const next = page.getByRole('dialog', { name: `Topology/${plain}` });
      await expect(next).toBeVisible();
      await expect.poll(() => opacityOf(next)).toBe('1');
      await next.getByRole('button', { name: 'Exit' }).click();
      // No dialog is left over the page.
      await expect(page.getByRole('dialog')).toHaveCount(0);
    });

    expectNoFatal(issues);
  });

  test('the YAML editor opens a legacy Builder topology without its diagram and saves the diagram back, and opens an ordinary topology', async ({
    page,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const plain = uniqueName(testInfo, 'plain');
    const legacy = uniqueName(testInfo, 'legacy');
    const built = uniqueName(testInfo, 'built');
    const xml = legacyXml('legacy-cell');
    await seedConfig(request, tracker, topology(plain));
    await seedConfig(
      request,
      tracker,
      topology(legacy, { 'builder-xml': xml }),
    );
    await publishTopology(
      request,
      tracker,
      built,
      deviceDocument(built, 'host-a'),
    );

    await openConfigs(page);
    await expect(configRow(page, legacy)).toBeVisible();

    await test.step('a legacy Builder topology opens in the YAML editor, which leaves its diagram out and saves it back', async () => {
      await editConfig(page, legacy);
      await expect
        .soft(
          page.getByRole('heading', {
            level: 1,
            name: `Edit Topology/${legacy}`,
          }),
        )
        .toBeFocused();
      const content = page.locator('.ace_content');
      await expect.soft(content).toContainText(`name: ${legacy}`);
      // The diagram is long XML on one line: the text shows a placeholder.
      await expect.soft(content).toContainText('builder-xml: <SNIPPED>');
      await expect.soft(content).not.toContainText('mxGraphModel');
      await expect.soft(page).toHaveURL(/\/configs\/$/);
      await expect.soft(page.locator('.dialog.modal.is-active')).toHaveCount(0);
      const save = page.getByRole('button', { name: 'Save', exact: true });
      await expect(save).toBeEnabled();

      // An edit beside the placeholder is saved, and the diagram with it,
      // as it was.
      await page.locator('.ace_editor').evaluate((element) => {
        const { editor } = element.env;
        editor.setValue(
          editor
            .getValue()
            .replace(/^( *)builder-xml: <SNIPPED>$/m, '$&\n$1owner: e2e'),
          -1,
        );
      });
      await expect.soft(content).toContainText('owner: e2e');
      const saved = waitForApi(page, 'PUT', `/configs/Topology/${legacy}`);
      await save.click();
      await page
        .locator('.dialog.modal.is-active')
        .getByRole('button', { name: 'Save' })
        .click();
      const response = await saved;
      expect(response.ok(), await response.text()).toBeTruthy();
      expect
        .soft(response.request().postDataJSON().metadata.annotations)
        .toEqual({ 'builder-xml': xml, owner: 'e2e' });
      await expect(configRow(page, legacy)).toBeVisible();

      const stored = await (
        await request.get(`${API}/configs/Topology/${legacy}`)
      ).json();
      expect
        .soft(stored.metadata?.annotations)
        .toEqual({ 'builder-xml': xml, owner: 'e2e' });
      // It is still a legacy Builder topology.
      await expect
        .soft(configRow(page, legacy).locator('.tag'))
        .toHaveText('builder legacy');
    });

    await test.step('an ordinary topology opens in the YAML editor', async () => {
      await editConfig(page, plain);
      // Focus starts on the editor's heading, not on the page.
      await expect
        .soft(
          page.getByRole('heading', {
            level: 1,
            name: `Edit Topology/${plain}`,
          }),
        )
        .toBeFocused();
      await expect
        .soft(page.locator('.ace_content'))
        .toContainText(`name: ${plain}`);

      // Tab leaves the text editor once Escape has stopped typing in it.
      const content = page.getByRole('group', { name: /^Editor content/ });
      await content.focus();
      await page.keyboard.press('Enter');
      await expect.soft(page.locator('.ace_text-input')).toBeFocused();
      await page.keyboard.press('Escape');
      await expect.soft(content).toBeFocused();
      await page.keyboard.press('Tab');
      await expect.soft(page.locator('.ace_text-input')).not.toBeFocused();
      await expect.soft(content).not.toBeFocused();
      await expect
        .soft(page.getByRole('button', { name: 'Save' }))
        .toBeEnabled();
      await expect.soft(page).toHaveURL(/\/configs\/$/);
      await expect.soft(page.locator('.dialog.modal.is-active')).toHaveCount(0);

      // Leave the unchanged editor to get back to the list.
      await page.getByRole('button', { name: 'Exit', exact: true }).click();
      await page
        .locator('.dialog.modal.is-active')
        .getByRole('button', { name: 'Continue' })
        .click();
      await expect(configRow(page, built)).toBeVisible();
    });

    expectNoFatal(issues);
  });

  test('the viewer’s button and the tag of a legacy Builder topology open the Import dialog, which makes nothing until Import', async ({
    page,
    request,
    tracker,
    builder,
    issues,
  }, testInfo) => {
    const plain = uniqueName(testInfo, 'plain');
    const legacy = uniqueName(testInfo, 'legacy');
    const built = uniqueName(testInfo, 'built');
    await seedConfig(request, tracker, topology(plain));
    await seedConfig(
      request,
      tracker,
      topology(legacy, { 'builder-xml': legacyXml('legacy-cell') }),
    );
    await publishTopology(
      request,
      tracker,
      built,
      deviceDocument(built, 'host-a'),
    );

    await openConfigs(page);
    await expect(configRow(page, plain)).toBeVisible();
    const creates = watchDraftCreates(page);

    await test.step("the viewer's button opens the Import dialog on a plain topology, and makes nothing until Import", async () => {
      const fetched = waitForApi(page, 'GET', `/configs/Topology/${plain}`);
      await page
        .getByRole('button', { name: `View Topology ${plain}` })
        .click();
      await fetched;
      const viewer = page.getByRole('dialog', { name: `Topology/${plain}` });
      const button = viewer.getByTestId('viewer-builder');
      await expect(button).toHaveText('Import into Builder');
      // It is the footer's first Tab stop, and Enter presses it.
      await expect
        .soft(viewer.locator('footer button').first())
        .toHaveText('Import into Builder');
      await button.focus();
      await page.keyboard.press('Enter');

      const dialog = page.getByRole('dialog', {
        name: 'Import topology or experiment',
      });
      await expect(dialog).toBeVisible({ timeout: 20000 });
      const intro = `Topology ${plain} has no Builder diagram to open. Import it to make one.`;
      await expect.soft(dialog.getByTestId('import-intro')).toHaveText(intro);
      await expect.soft(dialog).toHaveAccessibleDescription(intro);
      await expect.soft(dialog.getByLabel('Stored config')).toBeChecked();
      await expect
        .soft(dialog.getByTestId('import-kind'))
        .toHaveValue('topology');
      await expect(dialog.getByTestId('import-name')).toHaveValue(plain);
      // It has no includes: a copy is the only choice.
      await expect.soft(dialog.getByTestId('import-includes')).toHaveCount(0);
      await expect.soft(dialog.getByTestId('import-copy')).not.toBeChecked();
      await expect.soft(dialog.getByTestId('import-submit')).toBeEnabled();
      // The address no longer names the topology, so a reload does not ask
      // again.
      await expect.soft(page).toHaveURL(/\/builder$/);
      await expectAccessible(page, {
        soft: true,
        label: 'axe on the Import dialog opened from Configs',
      });

      // Choosing another source takes the sentence about this one away.
      await dialog.getByTestId('import-name').selectOption(built);
      await expect.soft(dialog.getByTestId('import-intro')).toHaveCount(0);
      await expect.soft(dialog).toHaveAccessibleDescription('');
      await dialog.getByTestId('import-name').selectOption(plain);
      await expect.soft(dialog.getByTestId('import-intro')).toHaveText(intro);

      // Cancel makes nothing, and focus goes to the landing's Import.
      await dialog.getByRole('button', { name: 'Cancel' }).click();
      await expect(dialog).toBeHidden();
      await expect.soft(page.getByTestId('drafts-import')).toBeFocused();
      await expect.soft(builder.landingHeading).toBeVisible();
      expect.soft(creates, 'draft creates').toEqual([]);
    });

    await test.step('the tag of a legacy Builder topology opens the Import dialog, which converts it', async () => {
      await openConfigs(page);
      await builderTag(page, legacy).click();
      const dialog = page.getByRole('dialog', {
        name: 'Import topology or experiment',
      });
      await expect(dialog).toBeVisible({ timeout: 20000 });
      await expect
        .soft(dialog.getByTestId('import-intro'))
        .toHaveText(
          `Topology ${legacy} has a legacy Builder diagram. Import it to convert the diagram.`,
        );
      await expect(dialog.getByTestId('import-name')).toHaveValue(legacy);
      await expect
        .soft(dialog.getByTestId('import-legacy-hint'))
        .toContainText('Publishing the draft to this topology replaces');
      // A copy leaves the topology, and its legacy diagram, as they are.
      await dialog.getByTestId('import-copy').check();
      await expect
        .soft(dialog.getByTestId('import-legacy-hint'))
        .toHaveText(
          'This topology has a legacy Builder diagram. Its layout is converted.',
        );
      await dialog.getByTestId('import-copy').uncheck();
      expect.soft(creates, 'draft creates').toEqual([]);

      const created = waitForApi(page, 'POST', '/builder/drafts');
      await dialog.getByTestId('import-submit').click();
      const next = dialog.getByTestId('import-continue');
      await expect
        .poll(
          async () => (await next.isVisible()) || !(await dialog.isVisible()),
        )
        .toBe(true);
      if (await next.isVisible()) {
        await next.click();
      }
      const draft = await (await created).json();
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await expect.soft(builder).toHaveAnnounced(/legacy diagram/);
      await builder.waitSaved();
      expect
        .soft((await builder.serverDraft(draft)).sourceToken)
        .toBe(`Topology/${legacy}`);
    });

    expectNoFatal(issues);
  });

  test('the tag of a Builder topology opens the draft that published it, and the viewer opens one no draft published as a new draft', async ({
    page,
    request,
    tracker,
    builder,
    issues,
  }, testInfo) => {
    const built = uniqueName(testInfo, 'built');
    // Published by a draft that is gone since.
    const orphan = uniqueName(testInfo, 'orphan');
    const publisher = await publishTopology(
      request,
      tracker,
      built,
      deviceDocument(built, 'host-a'),
    );
    await publishTopology(
      request,
      tracker,
      orphan,
      deviceDocument(orphan, 'host-o'),
      { keepDraft: false },
    );
    const creates = watchDraftCreates(page);

    await test.step('the tag of a Builder topology opens the draft that published it', async () => {
      const before = creates.length;
      await openConfigs(page);
      await builderTag(page, built).focus();
      await page.keyboard.press('Enter');
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      // The ?topology= link Configs followed now names the draft.
      await expect
        .soft(page)
        .toHaveURL(
          (url) =>
            url.pathname.endsWith('/builder') &&
            url.searchParams.get('draft') === publisher.draft.id,
        );
      await expect.soft(page.getByTestId('builder-name')).toHaveText(built);
      await expect.soft(builder.node('host-a', 'device')).toBeVisible();
      await expect
        .soft(builder)
        .toHaveAnnounced(
          `Opened topology ${built} in the Builder, in the draft that published it.`,
        );
      await builder.waitSaved();
      expect.soft(creates.slice(before), 'draft creates').toEqual([]);
    });

    await test.step("the viewer's button opens a Builder topology no draft published as a new draft", async () => {
      await openConfigs(page);
      const fetched = waitForApi(page, 'GET', `/configs/Topology/${orphan}`);
      await page
        .getByRole('button', { name: `View Topology ${orphan}` })
        .click();
      await fetched;
      const created = waitForApi(page, 'POST', '/builder/drafts');
      await page
        .getByRole('dialog', { name: `Topology/${orphan}` })
        .getByRole('button', { name: 'Open in Builder' })
        .click();
      const draft = await (await created).json();
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await expect
        .soft(page)
        .toHaveURL(
          (url) =>
            url.pathname.endsWith('/builder') &&
            url.searchParams.get('draft') === draft.id,
        );
      await expect.soft(page.getByTestId('builder-name')).toHaveText(orphan);
      await expect.soft(builder.summary).toContainText('1 device');
      await expect.soft(builder.node('host-o', 'device')).toBeVisible();
      await expect
        .soft(builder)
        .toHaveAnnounced(
          `Opened topology ${orphan} in the Builder as a new draft.`,
        );
      await builder.waitSaved();

      const stored = await builder.serverDraft(draft);
      expect.soft(stored.sourceToken).toMatch(/^builder-doc\//);
      const document = await builder.serverDocument(draft);
      expect.soft(document.metadata.name).toBe(orphan);
      expect
        .soft(document.nodes.map((node) => node.device?.hostname))
        .toEqual(['host-o']);
    });

    expectNoFatal(issues);
  });

  test('The Drafts button refreshes the Published tab, which opens a diagram read only until edited', async ({
    page,
    request,
    tracker,
    builder,
    issues,
  }, testInfo) => {
    const built = uniqueName(testInfo, 'built');
    const publisher = await publishTopology(
      request,
      tracker,
      built,
      deviceDocument(built, 'host-a'),
    );
    // The editor open, after the landing page has loaded its lists.
    await builder.openDraft(publisher.draft);
    const creates = watchDraftCreates(page);

    await test.step('The Drafts button refreshes the Published tab, which opens read only until edited', async () => {
      // Published while the editor is open, after the landing page loaded its
      // lists: only the refresh on leaving the editor can list it.
      const later = uniqueName(testInfo, 'later');
      await publishTopology(
        request,
        tracker,
        later,
        deviceDocument(later, 'host-b'),
        { keepDraft: false },
      );

      await builder.backToDrafts();
      await page.getByTestId('drafts-tab-published').click();
      const published = page.getByTestId('drafts-list-published');
      await expect
        .soft(published.locator('li', { hasText: built }))
        .toBeVisible();
      const card = published.locator('li', { hasText: later });
      await expect(card).toBeVisible();

      // Opening it makes no draft: the diagram is only looked at.
      const before = creates.length;
      await card.getByRole('button', { name: `Open ${later}` }).click();
      await expect(builder.canvas).toBeVisible();
      await expect.soft(page.getByTestId('builder-name')).toHaveText(later);
      await expect.soft(builder.summary).toContainText('1 device');
      await expect.soft(builder.node('host-b', 'device')).toBeVisible();
      const panel = page.getByTestId('builder-published');
      await expect(panel).toContainText(
        `You are viewing the published diagram ${later}.`,
      );
      await expect
        .soft(builder)
        .toHaveAnnounced(`Opened published diagram ${later}, read only.`);
      await expect
        .soft(builder.toolbar('publish'))
        .toHaveAttribute('aria-disabled', 'true');
      await expect.soft(page.getByTestId('builder-name-edit')).toHaveCount(0);
      // The Inspector's fields are read only, not disabled: Tab reaches
      // them, with their descriptions, and their values keep full contrast.
      await builder.selectNode('host-b');
      const hostname = builder.inspector.getByRole('textbox', {
        name: 'Hostname',
        exact: true,
      });
      await expect.soft(hostname).not.toBeEditable();
      await expect.soft(hostname).toBeEnabled();
      await expect
        .soft(
          builder.inspector.locator(':is(input, select, textarea):disabled'),
        )
        .toHaveCount(0);
      await expectAccessible(page, {
        soft: true,
        label: 'axe on a published diagram',
      });
      expect
        .soft(creates.slice(before), 'draft creates while viewing')
        .toEqual([]);

      // Edit makes the draft. Its button goes, and focus moves on to the
      // editor's heading rather than to <body>.
      const created = waitForApi(page, 'POST', '/builder/drafts');
      await panel.getByRole('button', { name: 'Edit as a draft' }).click();
      const draft = await (await created).json();
      await expect(panel).toHaveCount(0);
      await expect.soft(page.getByRole('heading', { level: 1 })).toBeFocused();
      await builder.waitSaved();
      await expect.soft(page.getByTestId('builder-name-edit')).toBeVisible();
      expect
        .soft((await builder.serverDraft(draft)).sourceToken)
        .toMatch(/^builder-doc\//);
    });

    expectNoFatal(issues);
  });

  test('a role without the Builder permission has no Builder link, and the Builder says so instead of opening a diagram', async ({
    page,
    request,
    tracker,
    builder,
    issues,
  }, testInfo) => {
    const viewed = uniqueName(testInfo, 'viewed');
    await publishTopology(
      request,
      tracker,
      viewed,
      deviceDocument(viewed, 'host-v'),
      { keepDraft: false },
    );
    await openConfigs(page);
    const creates = watchDraftCreates(page);

    // A session role that may edit configs but has no `builder use`. The
    // server has auth off, so no request may try what the role forbids.
    await page.evaluate(() => {
      sessionStorage.setItem('phenix.user', 'e2e-editor');
      sessionStorage.setItem('phenix.token', 'authorized');
      sessionStorage.setItem('phenix.auth', 'true');
      sessionStorage.setItem(
        'phenix.role',
        JSON.stringify({
          name: 'E2E Config Editor',
          policies: [
            {
              resources: ['*'],
              resourceNames: ['*', '*/*'],
              verbs: ['list', 'get', 'update'],
            },
          ],
        }),
      );
    });
    const notice = page.getByText('Your role does not include the Builder.');

    await test.step("the header has no Builder link, and Configs gives a Builder topology's tag as text with no way into the Builder", async () => {
      await openConfigs(page);
      await expect(page.getByTestId('nav-logout')).toBeVisible();
      await expect.soft(page.getByTestId('nav-builder')).toHaveCount(0);

      const row = configRow(page, viewed);
      await expect(row.locator('.tag')).toHaveText('builder');
      await expect.soft(builderTag(page, viewed)).toHaveCount(0);
      await expect.soft(row.getByRole('link')).toHaveCount(0);
      await page
        .getByRole('button', { name: `View Topology ${viewed}` })
        .click();
      const dialog = page.getByRole('dialog', { name: `Topology/${viewed}` });
      await expect(dialog).toBeVisible();
      await expect.soft(dialog.getByTestId('viewer-builder')).toHaveCount(0);
      await dialog.getByRole('button', { name: 'Exit' }).click();
      await expect(dialog).toBeHidden();
    });

    await test.step('the Builder, and a link to a diagram in it, show only a notice', async () => {
      for (const path of [
        '/builder',
        `/builder?topology=${encodeURIComponent(viewed)}`,
      ]) {
        await visit(page, path);
        await expect(notice, path).toBeVisible({ timeout: 20000 });
        await expect
          .soft(page.getByTestId('drafts-blank'), path)
          .toHaveCount(0);
        await expect.soft(builder.canvas, path).toHaveCount(0);
      }
      await expectAccessible(page, {
        soft: true,
        label: 'the Builder of a role without the Builder permission',
      });
      expect.soft(creates, 'draft creates').toEqual([]);
    });

    expectNoFatal(issues);
  });
});

test.describe('API routes', () => {
  // The Go router tests of the Builder do not register the experiment routes.
  test('the removed legacy experiment route and an unknown experiment path are not API routes', async ({
    request,
  }) => {
    const legacy = await request.post(`${API}/experiments/builder`);
    expect.soft(legacy.status(), 'POST /experiments/builder').toBe(405);

    const unknown = await request.get(`${API}/experiments/e2e/no/such/route`);
    expect.soft(unknown.status()).toBe(404);
    expect
      .soft((await unknown.json().catch(() => ({}))).message)
      .toBe('no API route matches this request');
  });
});
