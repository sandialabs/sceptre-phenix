// Builder integration tests: the header link, the Configs page hand-off, and
// the API contracts of the Builder.

const crypto = require('node:crypto');

const {
  API,
  SCHEMA_URI,
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
} = require('./builder-support');

const NO_ROUTE = 'no API route matches this request';

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

// Records every Buefy toast the page adds, so a toast that has already
// faded out, or was opened just before a route change, is still visible to
// the test.
async function recordToasts(page) {
  await page.evaluate(() => {
    const seen = new WeakSet();
    window.__e2eToasts = [];
    new MutationObserver(() => {
      for (const toast of document.querySelectorAll('.toast')) {
        if (!seen.has(toast)) {
          seen.add(toast);
          window.__e2eToasts.push({
            type: toast.className,
            text: toast.textContent.trim(),
          });
        }
      }
    }).observe(document.body, { childList: true, subtree: true });
  });
}

function recordedToasts(page) {
  return page.evaluate(() => window.__e2eToasts);
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
      // Browsers offer crypto.randomUUID and crypto.subtle only in a secure
      // context, and treat localhost as secure, so the server is reached
      // through a made-up host that Playwright routes to it. A page of its
      // own keeps that host's failing websocket out of `issues`.
      const base = new URL(page.url()).origin;
      const origin = 'http://builder.test';
      const other = await page.context().newPage();
      const errors = [];
      other.on('pageerror', (error) => errors.push(String(error)));
      tracker.watch(other);
      // The made-up host has no websocket server; an open mock keeps the
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
      // The page keeps polling; a request still in flight when it closes
      // would otherwise fail the worker and skip the next test.
      await other.unrouteAll({ behavior: 'ignoreErrors' });
      await other.close();
    });

    expectNoFatal(issues);
  });
});

test.describe('Configs page', () => {
  test('tags Builder topologies and routes each edit button to the right editor', async ({
    page,
    request,
    tracker,
    builder,
    issues,
  }, testInfo) => {
    const plain = uniqueName(testInfo, 'plain');
    const legacy = uniqueName(testInfo, 'legacy');
    const built = uniqueName(testInfo, 'built');
    // Published by a draft that is gone since, as a diagram someone else
    // published is to this user.
    const orphan = uniqueName(testInfo, 'orphan');
    const xml = legacyXml('legacy-cell');
    await seedConfig(request, tracker, topology(plain));
    await seedConfig(
      request,
      tracker,
      topology(legacy, { 'builder-xml': xml }),
    );
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

    const creates = [];
    page.on('request', (sent) => {
      if (
        sent.method() === 'POST' &&
        new URL(sent.url()).pathname === `${API}/builder/drafts`
      ) {
        creates.push(sent.url());
      }
    });

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
            url.searchParams.get('draft') ===
              `${publisher.draft.owner}/${publisher.draft.id}`,
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

    await test.step("the viewer's button opens a Builder topology no draft of mine published as a new draft", async () => {
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
            url.searchParams.get('draft') === `${draft.owner}/${draft.id}`,
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
      expect.soft(document.name).toBe(orphan);
      expect
        .soft(document.nodes.map((node) => node.device?.hostname))
        .toEqual(['host-o']);
    });

    await test.step('Back to drafts refreshes the Published tab, which opens read only until edited', async () => {
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
      await builder.selectInOutline('host-b');
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

      // Edit makes the draft; its button goes, and focus moves on to the
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

    await test.step('a role that may not create drafts gets no import controls, and only views a diagram it has no draft of', async () => {
      // No draft of this user published it, or was made from it.
      const viewed = uniqueName(testInfo, 'viewed');
      await publishTopology(
        request,
        tracker,
        viewed,
        deviceDocument(viewed, 'host-v'),
        { keepDraft: false },
      );
      // The UI takes the role from the session, as a sign-in leaves it. This
      // one may edit configs but not create drafts; the server, with
      // authentication off, would allow anything, so no request may try.
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
      const before = creates.length;

      await openConfigs(page);
      // An import makes a draft: the legacy tag is plain text, and the
      // viewer of a topology without a diagram has no Builder button.
      await expect(configRow(page, plain)).toBeVisible();
      await expect.soft(configRow(page, plain).locator('.tag')).toHaveCount(0);
      const fetched = waitForApi(page, 'GET', `/configs/Topology/${plain}`);
      await page
        .getByRole('button', { name: `View Topology ${plain}` })
        .click();
      await fetched;
      const viewer = page.getByRole('dialog', { name: `Topology/${plain}` });
      await expect(viewer).toBeVisible();
      await expect.soft(viewer.getByTestId('viewer-builder')).toHaveCount(0);
      await expect
        .soft(viewer.locator('footer button').first())
        .toHaveText('Edit Config');
      await viewer.getByRole('button', { name: 'Exit' }).click();
      await expect(viewer).toBeHidden();
      // A diagram can still be opened, to look at it.
      await expect.soft(builderTag(page, viewed)).toBeVisible();

      await editConfig(page, viewed);
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      const panel = page.getByTestId('builder-published');
      await expect(panel).toContainText(
        'Your role cannot create drafts, so it cannot be edited.',
      );
      await expect.soft(panel.getByRole('button')).toHaveCount(0);
      for (const action of ['publish', 'upload']) {
        await expect
          .soft(builder.toolbar(action), action)
          .toHaveAttribute('aria-disabled', 'true');
      }

      await builder.backToDrafts();
      await expect.soft(page.getByTestId('drafts-view-only')).toBeVisible();
      for (const id of ['drafts-blank', 'drafts-import', 'drafts-upload']) {
        await expect.soft(page.getByTestId(id), id).toHaveCount(0);
      }
      await expect
        .soft(page.getByRole('button', { name: /^Delete / }))
        .toHaveCount(0);

      // A link to a topology without a diagram says why nothing opens.
      await visit(page, `/builder?topology=${encodeURIComponent(plain)}`);
      await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
      await expect
        .soft(page.getByTestId('builder-error').getByRole('alert'))
        .toHaveText(
          `Topology ${plain} has no Builder diagram, and your role cannot create drafts to import it. Select its name in Configs to view it.`,
        );
      await expect.soft(page.getByRole('dialog')).toHaveCount(0);
      expect.soft(creates.slice(before), 'draft creates').toEqual([]);
    });

    expectNoFatal(issues);
  });

  test('returning from a Builder topology edit shows no empty toast', async ({
    page,
    request,
    tracker,
    builder,
  }, testInfo) => {
    const legacy = uniqueName(testInfo, 'legacy-toast');
    const built = uniqueName(testInfo, 'built-toast');
    await seedConfig(
      request,
      tracker,
      topology(legacy, { 'builder-xml': legacyXml('legacy-cell') }),
    );
    await publishTopology(request, tracker, built);

    await openConfigs(page);
    await expect(configRow(page, legacy)).toBeVisible();
    await recordToasts(page);

    await test.step('after leaving the text editor of a legacy Builder topology', async () => {
      await editConfig(page, legacy);
      await expect(page.locator('.ace_content')).toContainText(
        'builder-xml: <SNIPPED>',
      );
      const reloaded = waitForApi(page, 'GET', '/configs');
      await page.getByRole('button', { name: 'Exit', exact: true }).click();
      await page
        .locator('.dialog.modal.is-active')
        .getByRole('button', { name: 'Continue' })
        .click();
      await reloaded;
      await expect(configRow(page, built)).toBeVisible();

      const empty = (await recordedToasts(page)).filter((toast) => !toast.text);
      expect.soft(empty, JSON.stringify(empty)).toEqual([]);
    });

    await test.step('on the Builder redirect', async () => {
      const seen = (await recordedToasts(page)).length;
      await editConfig(page, built);
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await builder.waitSaved();
      // One message says where the user is now, and in which draft: the
      // one that published the topology, which is this user's.
      await expect
        .soft(builder)
        .toHaveAnnounced(
          `Opened topology ${built} in the Builder, in the draft that published it.`,
        );

      const empty = (await recordedToasts(page))
        .slice(seen)
        .filter((toast) => !toast.text);
      expect.soft(empty, JSON.stringify(empty)).toEqual([]);
    });
  });
});

test.describe('API', () => {
  test('unknown /api/v1 routes answer a JSON 404 and the Builder schema is served under its $id', async ({
    request,
  }) => {
    await test.step('unknown routes', async () => {
      const paths = [
        '/e2e-no-such-route',
        '/builder/e2e-no-such-route',
        '/experiments/e2e/no/such/route',
        // routes of the removed legacy Builder
        '/builder/topologies',
        '/builder/topologies/e2e-none',
      ];

      for (const path of paths) {
        for (const method of ['get', 'post']) {
          const response = await request[method](`${API}${path}`);
          const label = `${method.toUpperCase()} ${path}`;
          expect.soft(response.status(), label).toBe(404);
          expect
            .soft(response.headers()['content-type'], label)
            .toContain('application/json');
          expect
            .soft((await response.json().catch(() => ({}))).message, label)
            .toBe(NO_ROUTE);
        }
      }

      // The removed legacy route for experiments matches
      // /experiments/{name}, which takes no POST.
      const legacy = await request.post(`${API}/experiments/builder`);
      expect.soft(legacy.status(), 'POST /experiments/builder').toBe(405);

      // Paths outside the API still fall back to the single-page app.
      const page = await request.get('/e2e-no-such-page');
      expect.soft(page.status()).toBe(200);
      expect.soft(page.headers()['content-type']).toContain('text/html');
    });

    await test.step('Builder document schema', async () => {
      const response = await request.get(`${API}/schemas/builder/v1`);
      expect.soft(response.status()).toBe(200);
      expect
        .soft(response.headers()['content-type'])
        .toContain('application/json');

      const schema = await response.json().catch(() => ({}));
      expect.soft(schema.$id).toBe(SCHEMA_URI);
      expect
        .soft(schema.$schema)
        .toBe('https://json-schema.org/draft/2020-12/schema');
      expect.soft(schema.type).toBe('object');
      expect.soft(Object.keys(schema.$defs ?? {})).toContain('device');

      // The builder route is registered ahead of the generic schema route and
      // must not shadow it.
      const topologySchema = await request.get(`${API}/schemas/topology/v1`);
      expect.soft(topologySchema.status()).toBe(200);
    });
  });
});
