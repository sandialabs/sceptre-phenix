// The drafts page: its tabs and cards, Publish from a draft's card, Exp,
// which opens the experiment a diagram was published with, on a published
// diagram's card and in the editor's toolbar, and selecting several cards to
// delete them at once.
//
// Every test is the one user of a server without authentication, beside the
// other specs' tests, so each looks only at the drafts and configs it made.
// The lists hold the other tests' drafts too: a test selects and deletes
// only what it made, and the test that presses Select all deletes nothing.

const fs = require('fs');

const {
  API,
  blankDocument,
  devicesOf,
  draftPath,
  test,
  expect,
  expectAccessible,
  expectNoFatal,
  expectNoInvisibleText,
  labDocument,
  publishTopology,
  uniqueName,
} = require('./builder-support');

// The card of a listed draft or published diagram, by its id.
function cardOf(page, id) {
  return page
    .getByTestId(`draft-open-${id}`)
    .locator('xpath=ancestor::li[contains(@class, "builder-card")]');
}

// The card's buttons, in order: their test ids without the item's id, and
// their boxes.
async function cardButtons(card) {
  return card
    .locator('.builder-card__actions > button')
    .evaluateAll((buttons) =>
      buttons.map((button) => {
        const { width, height } = button.getBoundingClientRect();

        return {
          id: button.dataset.testid.replace(
            /^(draft|published)-([a-z]+)-.*$/,
            '$1-$2',
          ),
          width,
          height,
        };
      }),
    );
}

// Expects the buttons to be one size, and large enough to press (WCAG
// 2.5.8).
function expectOneSize(buttons, label) {
  const [first] = buttons;

  for (const button of buttons) {
    expect
      .soft(
        Math.abs(button.width - first.width),
        `${label}: ${button.id} width`,
      )
      .toBeLessThan(0.5);
    expect
      .soft(
        Math.abs(button.height - first.height),
        `${label}: ${button.id} height`,
      )
      .toBeLessThan(0.5);
  }

  expect.soft(first.height, `${label}: height`).toBeGreaterThanOrEqual(32);
  expect.soft(first.width, `${label}: width`).toBeGreaterThanOrEqual(100);
}

// The id of the published diagram of topology `name`.
async function publishedId(request, name) {
  const listed = await request.get(`${API}/builder/documents`);
  expect(listed.ok(), await listed.text()).toBeTruthy();
  const found = ((await listed.json()).documents || []).find(
    (entry) => entry.target === name,
  );
  expect(found, `published diagram of ${name}`).toBeTruthy();

  return found.id;
}

// Submits the Publish dialog and expects the publish to succeed.
async function publishFromDialog(page) {
  const answered = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      /\/builder\/drafts\/[^/]+\/[^/]+\/publish$/.test(
        new URL(response.url()).pathname,
      ),
  );
  await page.getByTestId('publish-submit').click();
  const response = await answered;
  expect(response.status(), await response.text()).toBe(200);
  await expect(page.getByTestId('publish-result')).toContainText(
    'Every stage succeeded',
  );
}

const SNAPSHOT_SAVES = (url) => url.pathname.endsWith('/snapshots');

// The row above a tab's cards says "2 of 9 selected": both numbers.
async function selectionOf(page, tab) {
  const text = await page.getByTestId(`bulk-count-${tab}`).textContent();
  const [, count, total] = text.trim().match(/^(\d+) of (\d+) selected$/);

  return { count: Number(count), total: Number(total) };
}

// The checkboxes of a tab's cards: how many there are, and how many are
// checked.
async function checkboxesOf(page, tab) {
  return page
    .locator(`#panel-${tab} [data-testid^="card-select-"]`)
    .evaluateAll((boxes) => ({
      total: boxes.length,
      checked: boxes.filter((box) => box.checked).length,
    }));
}

// Has the page read its lists again, as it does when its window comes back
// to the front, and waits for the drafts.
async function relist(page) {
  const listed = page.waitForResponse(
    (response) =>
      response.request().method() === 'GET' &&
      new URL(response.url()).pathname.endsWith(`${API}/builder/drafts`),
  );
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await listed;
}

// Asks Delete selected of `tab` and returns the question, once it is the
// one expected: a test must never confirm a batch that holds more than its
// own cards.
async function askDeleteSelected(page, tab, title) {
  await page.getByTestId(`bulk-delete-${tab}`).click();
  const confirm = page.getByRole('alertdialog');
  await expect(confirm).toBeVisible();
  await expect(confirm).toHaveAccessibleName(title);

  return confirm;
}

test(
  "a draft's card says who and when, has buttons of one size, and publishes from the drafts page with the work the server lacks",
  { tag: '@cross-browser' },
  async ({ page, builder, tracker, issues }, testInfo) => {
    test.setTimeout(150000);

    const topology = uniqueName(testInfo, 'card');
    const blockedName = uniqueName(testInfo, 'card-blocked');
    // An interface with no VLAN, which Publish refuses.
    const blocked = labDocument(blockedName);
    blocked.edges.pop();
    delete blocked.nodes[1].device.spec.network.interfaces[0].vlan;

    const draft = await builder.seedDraft(labDocument(topology));
    const stuck = await builder.seedDraft(blocked);
    tracker.config('Topology', topology);

    await builder.open();
    const card = cardOf(page, draft.id);
    const publish = page.getByTestId(`draft-publish-${draft.id}`);
    const dialog = page.getByRole('dialog');
    await expect(card).toBeVisible();

    await test.step('the tab count is text of the tab, the same size and on the same line as its name', async () => {
      const tab = page.getByTestId('drafts-tab-mine');
      await expect(tab).toHaveText(/^My Drafts \(\d+\)$/);
      await expect(tab).toHaveAccessibleName(/^My Drafts \(\d+\)$/);
      await expect(page.getByTestId('drafts-tab-shared')).toHaveText(
        /^Shared Drafts \(\d+\)$/,
      );

      const read = await tab.evaluate((element) => {
        const count = element.querySelector('.builder-tab__count');
        const name = document.createRange();
        name.selectNodeContents(element.firstChild);
        const style = (node, property) => getComputedStyle(node)[property];

        return {
          listed: document.querySelectorAll('#panel-mine .builder-card').length,
          counted: Number(count.textContent.replace(/[()]/g, '')),
          sizes: [style(element, 'fontSize'), style(count, 'fontSize')],
          weights: [style(element, 'fontWeight'), style(count, 'fontWeight')],
          bottoms: [
            name.getBoundingClientRect().bottom,
            count.getBoundingClientRect().bottom,
          ],
        };
      });

      expect.soft(read.counted).toBe(read.listed);
      expect.soft(read.sizes[1], 'font size').toBe(read.sizes[0]);
      expect.soft(read.weights[1], 'font weight').toBe(read.weights[0]);
      expect
        .soft(Math.abs(read.bottoms[1] - read.bottoms[0]), 'line')
        .toBeLessThan(0.5);
    });

    await test.step('the card has the owner on one line and the time on the next, and buttons of one size', async () => {
      const lines = card.locator('p.builder-card__meta');
      await expect.soft(lines.nth(0)).toHaveText(/^\s*Owner: \S+\s*$/);
      await expect
        .soft(page.getByTestId(`card-time-${draft.id}`))
        .toHaveText(/^Updated .+/);
      const owner = await lines.nth(0).boundingBox();
      const time = await page
        .getByTestId(`card-time-${draft.id}`)
        .boundingBox();
      expect
        .soft(time.y, 'the time is below the owner')
        .toBeGreaterThanOrEqual(owner.y + owner.height - 0.5);
      expect.soft(Math.abs(time.x - owner.x), 'left edges').toBeLessThan(0.5);

      // Without authentication there is no one to share with: no Share.
      const buttons = await cardButtons(card);
      expect
        .soft(buttons.map((button) => button.id))
        .toEqual(['draft-open', 'draft-delete', 'draft-publish']);
      expectOneSize(buttons, 'draft card');
      await expect
        .soft(publish)
        .toHaveAccessibleName(new RegExp(`^Publish ${topology}, updated `));
      await expect.soft(publish).toHaveAttribute('aria-haspopup', 'dialog');
    });

    await test.step('at 320 pixels wide the tabs wrap, and nothing runs off the page', async () => {
      const wide = page.viewportSize();
      await page.setViewportSize({ width: 320, height: wide.height });
      const edges = await page.evaluate(() => ({
        page: document.documentElement.clientWidth,
        scroll: document.documentElement.scrollWidth,
        tabs: [...document.querySelectorAll('.builder-tab')].map(
          (tab) => tab.getBoundingClientRect().right,
        ),
      }));

      expect.soft(edges.scroll, 'page width').toBeLessThanOrEqual(edges.page);
      for (const right of edges.tabs) {
        expect
          .soft(right, 'a tab’s right edge')
          .toBeLessThanOrEqual(edges.page);
      }
      expectOneSize(await cardButtons(card), 'draft card at 320 pixels');
      await page.setViewportSize(wide);
    });

    await test.step('a change the server has not received is kept on the card when the draft is closed', async () => {
      await page.getByTestId(`draft-open-${draft.id}`).click();
      await expect(builder.canvas).toBeVisible();
      await builder.waitSaved();
      await page.route(SNAPSHOT_SAVES, (route) => route.abort());
      await builder.selectInOutline('server-2');
      await builder.toolbar('delete').click();
      await builder.expectCounts({
        devices: 1,
        switches: 1,
        networks: 1,
        links: 1,
      });
      await expect(builder.saveState).toHaveText(
        /Offline: 1 change kept on this device/,
      );
      await builder.backToDrafts();
      await expect(page.getByTestId(`draft-save-${draft.id}`)).toHaveText(
        /^Offline: 1 change kept on this device/,
      );
    });

    // What the live region has said before the draft is loaded.
    let said = 0;
    // And before another draft's Publish was pressed.
    let saidBeforeOther = 0;

    await test.step("another draft's Publish opens meanwhile, and Cancel gives its card focus back", async () => {
      const other = page.getByTestId(`draft-publish-${stuck.id}`);
      const select = page.getByTestId(`card-select-${stuck.id}`);
      saidBeforeOther = (await builder.announced()).length;
      await select.check();
      await other.focus();
      await page.keyboard.press('Enter');
      await expect(dialog).toBeVisible();
      await expect.soft(dialog).toHaveAccessibleName(`Publish ${blockedName}`);
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);
      await expect.soft(other).toBeFocused();
      // The first draft's change is still kept on this device only, and
      // nothing says that all is saved. What the live region held back
      // behind the dialog is said by the time the next message is: here,
      // that of the card being unselected again.
      await expect(page.getByTestId(`draft-save-${draft.id}`)).toHaveText(
        /^Offline: 1 change kept on this device/,
      );
      await select.uncheck();
      await expect(builder).toHaveAnnounced(/\b0 of \d+ selected\./);
      expect
        .soft((await builder.announced()).slice(saidBeforeOther).join(' '))
        .not.toMatch(/All changes saved/);
    });

    await test.step('Publish on the card loads the draft with that change, and opens the dialog over the drafts', async () => {
      said = (await builder.announced()).length;
      await publish.focus();
      await page.keyboard.press('Enter');
      await expect(dialog).toBeVisible();
      await expect
        .soft(dialog.getByRole('heading', { level: 2 }).first())
        .toHaveText(`Publish ${topology}`);
      await expect.soft(dialog).toHaveAccessibleName(`Publish ${topology}`);
      // The drafts stay behind the dialog: the editor is not shown.
      await expect.soft(builder.landingHeading).toBeVisible();
      await expect.soft(builder.canvas).toHaveCount(0);
      // The change kept on this device is part of what would be published.
      await expect(dialog).toContainText(
        '1 device, 1 switch, 1 network and 1 connection are ready to publish.',
      );
      await expect
        .soft(dialog.getByRole('button'))
        .toHaveText(['', 'Open draft', 'Cancel', 'Create topology']);
      await expectAccessible(page, {
        include: '[data-testid="builder-dialog"]',
        soft: true,
        label: 'Publish dialog on the drafts page',
      });
    });

    await test.step('what the server lacks is not published, and the dialog says where to settle it', async () => {
      let sent = 0;
      const onRequest = (request) => {
        if (
          request.method() === 'POST' &&
          new URL(request.url()).pathname.endsWith('/publish')
        ) {
          sent += 1;
        }
      };
      page.on('request', onRequest);
      await page.getByTestId('publish-submit').click();
      await expect(page.getByTestId('publish-error')).toHaveText(
        'Your latest changes are not saved on the server yet, so there is nothing new to publish. Wait for the save to finish or retry it. Open the draft to resolve it.',
      );
      page.off('request', onRequest);
      expect.soft(sent, 'publish requests').toBe(0);
      expect.soft(await builder.config('Topology', topology)).toBeNull();
    });

    await test.step('Cancel hands the draft back: its change goes on being saved from the card, and focus returns to Publish', async () => {
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);
      await expect.soft(publish).toBeFocused();
      await expect(page.getByTestId(`draft-save-${draft.id}`)).toHaveText(
        /^Offline: 1 change kept on this device/,
      );
      await expect.soft(page.getByTestId('builder-error')).toHaveCount(0);
      await expect.soft(builder.landingHeading).toBeVisible();
      // The live region waits for the dialog to close. It says what was
      // recovered, and not that a draft opened: none did.
      await expect
        .soft(builder)
        .toHaveAnnounced('Recovered 1 unsaved change from this device.');
      expect
        .soft((await builder.announced()).slice(said).join(' '))
        .not.toMatch(/Draft loaded|Opened /);
      // Nor, since the other draft's Publish was pressed, that everything
      // is saved: this draft's change is not.
      expect
        .soft((await builder.announced()).slice(saidBeforeOther).join(' '))
        .not.toMatch(/All changes saved/);

      // Once the server can be reached, the change is sent from the card.
      await page.unroute(SNAPSHOT_SAVES);
      await builder.persisted(
        draft,
        (document) =>
          document.nodes.filter((node) => node.kind === 'device').length,
        1,
        { timeout: 30000, message: 'devices the server holds' },
      );
    });

    await test.step('the draft publishes from its card; closed while it publishes, the dialog leaves the draft loaded until the server answers', async () => {
      let release;
      const held = new Promise((resolve) => {
        release = resolve;
      });
      const isPublish = (url) => url.pathname.endsWith('/publish');
      await page.route(isPublish, async (route) => {
        await held;
        await route.continue();
      });
      const answered = page.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          isPublish(new URL(response.url())),
      );

      await publish.click();
      await expect(dialog).toBeVisible();
      await expect(page.getByTestId('publish-name')).toHaveValue(topology);
      const sent = page.waitForRequest(
        (request) =>
          request.method() === 'POST' && isPublish(new URL(request.url())),
      );
      await page.getByTestId('publish-submit').click();
      await sent;
      await expect
        .soft(page.getByTestId('publish-submit'))
        .toHaveText('Publishing…');
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);

      // Until the server answers, no other draft is opened or made, and
      // none is deleted: not the one being published either.
      const open = page.getByTestId(`draft-open-${draft.id}`);
      const remove = page.getByTestId(`draft-delete-${draft.id}`);
      await expect.soft(publish).toBeFocused();
      await expect.soft(publish).toHaveAttribute('aria-disabled', 'true');
      await expect.soft(open).toHaveAttribute('aria-disabled', 'true');
      await expect.soft(remove).toHaveAttribute('aria-disabled', 'true');
      await expect
        .soft(page.getByTestId(`draft-delete-${stuck.id}`))
        .toHaveAttribute('aria-disabled', 'true');
      await expect
        .soft(page.getByTestId('drafts-blank'))
        .toHaveAttribute('aria-disabled', 'true');
      await open.click({ force: true });
      await expect.soft(builder.canvas).toHaveCount(0);
      await remove.click({ force: true });
      await expect.soft(remove).toHaveAttribute('aria-disabled', 'true');
      await expect.soft(page.getByRole('alertdialog')).toHaveCount(0);

      release();
      expect((await answered).status()).toBe(200);
      await expect(builder).toHaveAnnounced('Diagram published.');
      await expect(open).not.toHaveAttribute('aria-disabled');
      await expect.soft(remove).not.toHaveAttribute('aria-disabled');
      await page.unroute(isPublish);
      await expect.soft(page.getByTestId('builder-error')).toHaveCount(0);
      await expect.soft(builder.landingHeading).toBeVisible();
      // Nothing is left to send, so the card says nothing of saving.
      await expect
        .soft(page.getByTestId(`draft-save-${draft.id}`))
        .toHaveCount(0);

      // What was published is the draft with the change that was kept.
      const config = await builder.config('Topology', topology);
      expect
        .soft(config.spec.nodes.map((node) => node.general.hostname))
        .toEqual(['server']);

      // The lists were read again: the published diagram is listed.
      const id = await publishedId(builder.request, topology);
      await page.getByTestId('drafts-tab-published').click();
      const row = cardOf(page, id);
      await expect(row).toBeVisible();
      await expect.soft(row.getByRole('heading')).toHaveText(topology);
      await expect
        .soft(page.getByTestId(`card-time-${id}`))
        .toHaveText(/^Published .+/);
      // Published without an experiment: no Exp.
      const buttons = await cardButtons(row);
      expect
        .soft(buttons.map((button) => button.id))
        .toEqual(['draft-open', 'published-delete']);
      expectOneSize(buttons, 'published card');
      await page.getByTestId('drafts-tab-mine').click();
    });

    await test.step('a diagram with errors lists them, and Open draft leads to the editor on that draft', async () => {
      const blockedPublish = page.getByTestId(`draft-publish-${stuck.id}`);
      await blockedPublish.click();
      await expect(dialog).toBeVisible();
      await expect
        .soft(dialog.getByRole('heading', { level: 2 }).first())
        .toHaveText(`Publish ${blockedName}`);
      await expect(
        dialog.locator('.builder-issues li[data-level="error"]'),
      ).not.toHaveCount(0);
      await expect.soft(page.getByTestId('publish-submit')).toBeDisabled();
      await expect
        .soft(page.getByTestId('publish-open-hint'))
        .toHaveText('Open the draft to fix the errors, then publish.');

      await page.getByTestId('publish-open-draft').click();
      await expect(builder.canvas).toBeVisible();
      await expect(dialog).toHaveCount(0);
      await expect.soft(page.getByRole('heading', { level: 1 })).toBeFocused();
      await expect
        .soft(page.getByTestId('builder-name'))
        .toHaveText(blockedName);
      await builder.expectCounts({
        devices: 2,
        switches: 1,
        networks: 1,
        links: 1,
      });
      await builder.waitSaved();

      // It is an open draft like any other: Back to drafts closes it.
      await builder.backToDrafts();
      await expect
        .soft(page.getByTestId(`draft-open-${stuck.id}`))
        .toBeFocused();
      await expect.soft(page.getByTestId('builder-error')).toHaveCount(0);
    });

    expectNoFatal(issues);
  },
);

// Go to on an issue of the Publish dialog a draft's card opens: the editor
// opens on that draft with the device selected, and the Inspector field the
// issue names takes focus.
test('Go to in the Publish dialog of a draft’s card opens the editor with the field focused', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const name = uniqueName(testInfo, 'card-go-to');
  // The second device's hostname is "all", which phenix refuses in an
  // experiment, so Publish lists it as an error.
  const document = labDocument(name);
  const [, refused] = devicesOf(document);
  refused.label = 'all';
  refused.device.hostname = 'all';
  refused.device.spec.general.hostname = 'all';
  const draft = await builder.seedDraft(document);

  await builder.open();
  await page.getByTestId(`draft-publish-${draft.id}`).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(page.getByTestId('publish-submit')).toBeDisabled();

  const errors = dialog
    .getByTestId('publish-checks')
    .getByTestId('publish-checks-error');
  await expect(errors.getByTestId('issue-element')).toHaveText(['Device all']);
  await errors
    .getByRole('button', { name: /^Go to device all: hostname "all"/ })
    .click();

  await expect(builder.canvas).toBeVisible();
  await expect(dialog).toHaveCount(0);
  await expect.soft(page.getByTestId('builder-name')).toHaveText(name);
  await expect(builder.outlineItem('all')).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  const hostname = builder.inspector
    .locator('[data-path="hostname"]')
    .getByRole('textbox');
  await expect(hostname).toBeFocused();
  await expect(hostname).toHaveValue('all');
  await expect(builder).toHaveAnnounced('Hostname in device all');
  await builder.waitSaved();
  expectNoFatal(issues);
});

test('Exp opens the experiment a diagram was published with, from its card and from the toolbar', async ({
  page,
  builder,
  tracker,
}, testInfo) => {
  test.setTimeout(150000);

  const topology = uniqueName(testInfo, 'exp-topo');
  const experiment = uniqueName(testInfo, 'exp');
  const draft = await builder.seedDraft(labDocument(topology));
  tracker.config('Topology', topology);
  tracker.config('Experiment', experiment);

  await builder.open();
  const dialog = page.getByRole('dialog');
  const toolbarExp = builder.toolbar('experiment');
  const expName = `Exp: open experiment ${experiment}`;
  const expTip = `Open experiment ${experiment}`;
  const atExperiment = new RegExp(`/experiment/${experiment}$`);
  let id;

  await test.step('a draft published with an experiment from its card has Exp on its published diagram', async () => {
    await page.getByTestId(`draft-publish-${draft.id}`).click();
    await expect(dialog).toBeVisible();
    await page.getByTestId('publish-mode-experiment').check();
    await page.getByTestId('publish-experiment').fill(experiment);
    await publishFromDialog(page);
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(dialog).toHaveCount(0);
    // The dialog gives focus back to the card's Publish, and the page says
    // what happened once it has closed.
    await expect
      .soft(page.getByTestId(`draft-publish-${draft.id}`))
      .toBeFocused();
    await expect.soft(builder).toHaveAnnounced('Diagram published.');
    await expect.soft(page.getByTestId('builder-error')).toHaveCount(0);

    id = await publishedId(builder.request, topology);
    await page.getByTestId('drafts-tab-published').click();
    const exp = page.getByTestId(`published-experiment-${id}`);
    await expect(exp).toBeVisible();
    await expect.soft(exp).toHaveText('Exp');
    await expect.soft(exp).toHaveAccessibleName(expName);
    await expect.soft(exp).not.toHaveAttribute('aria-haspopup');

    const buttons = await cardButtons(cardOf(page, id));
    expect
      .soft(buttons.map((button) => button.id))
      .toEqual(['draft-open', 'published-experiment', 'published-delete']);
    expectOneSize(buttons, 'published card');

    // The tooltip names the experiment, on focus and on hover, and Escape
    // dismisses it (WCAG 1.4.13).
    const tooltip = page.getByTestId('drafts-tooltip');
    await exp.focus();
    await expect.soft(tooltip).toHaveText(expTip);
    await page.keyboard.press('Escape');
    await expect.soft(tooltip).toHaveCount(0);
    await page.getByTestId('drafts-tab-published').hover();
    await exp.hover();
    await expect.soft(tooltip).toHaveText(expTip);
    await expectAccessible(page, {
      include: '[data-testid="drafts-list-published"]',
      soft: true,
      label: 'a published card with Exp',
    });
  });

  await test.step('Exp on the card opens the experiment, in the same tab', async () => {
    await page.getByTestId(`published-experiment-${id}`).click();
    await expect(page).toHaveURL(atExperiment);
  });

  await test.step('the published diagram, shown read only, has Exp in the toolbar', async () => {
    await builder.open();
    await page.getByTestId('drafts-tab-published').click();
    await page.getByTestId(`draft-open-${id}`).click();
    await expect(page.getByTestId('builder-published')).toBeVisible();
    await expect.soft(toolbarExp).toHaveAccessibleName(expName);
    await builder.backToDrafts();
  });

  await test.step('the draft has Exp last among the ways out, named for the experiment', async () => {
    await page.getByTestId('drafts-tab-mine').click();
    await page.getByTestId(`draft-open-${draft.id}`).click();
    await expect(builder.canvas).toBeVisible();
    await builder.waitSaved();
    await expect(toolbarExp).toBeVisible();
    await expect.soft(toolbarExp).toHaveText('Exp');
    await expect.soft(toolbarExp).toHaveAccessibleName(expName);
    expect
      .soft(
        await toolbarExp.evaluate((button) => ({
          before: button.previousElementSibling?.dataset.testid,
          last: button.nextElementSibling === null,
        })),
      )
      .toEqual({ before: 'toolbar-publish', last: true });
    await toolbarExp.focus();
    await expect.soft(page.getByTestId('toolbar-tooltip')).toHaveText(expTip);
    await expectAccessible(page, {
      include: '[role="toolbar"]',
      soft: true,
      label: 'the toolbar with Exp',
    });
  });

  await test.step('Exp asks before it leaves changes that cannot be saved; Stay keeps the editor, and focus', async () => {
    await builder.selectInOutline('server');
    const memory = builder.inspector
      .locator('legend.group-label', { hasText: /^Hardware$/ })
      .locator('xpath=..')
      .getByLabel('Memory', { exact: true });
    await memory.fill('lots');
    await memory.blur();

    const confirm = page.getByRole('alertdialog', {
      name: 'Leave with unsaved changes?',
    });
    await toolbarExp.click();
    await expect(confirm).toBeVisible();
    await expect
      .soft(confirm)
      .toHaveAccessibleDescription(/cannot be saved until Memory is fixed\./);
    await confirm.getByRole('button', { name: 'Stay' }).click();
    await expect(confirm).toBeHidden();
    await expect.soft(page).toHaveURL(/\/builder$/);
    await expect.soft(builder.canvas).toBeVisible();
    await expect.soft(toolbarExp).toBeFocused();

    await builder.inspector.getByTestId('inspector-cancel').click();
    await expect(builder.inspector.getByTestId('inspector-apply')).toHaveCount(
      0,
    );
  });

  await test.step('an experiment deleted meanwhile is not opened, and the page says so', async () => {
    // A server without minimega answers the delete with an error from its
    // clean-up, after the config is gone.
    await builder.request.delete(`${API}/configs/Experiment/${experiment}`);
    expect(await builder.config('Experiment', experiment)).toBeNull();

    await toolbarExp.click();
    await expect(page.getByTestId('builder-error')).toContainText(
      `Could not open experiment ${experiment}. It may have been deleted or renamed.`,
    );
    await expect.soft(page).toHaveURL(/\/builder$/);
    await expect.soft(builder.canvas).toBeVisible();
  });

  await test.step('read again, neither the card nor the toolbar has Exp', async () => {
    await builder.backToDrafts();
    await page.getByTestId('drafts-tab-published').click();
    await expect(page.getByTestId(`draft-open-${id}`)).toBeVisible();
    await expect(page.getByTestId(`published-experiment-${id}`)).toHaveCount(0);
    expect
      .soft((await cardButtons(cardOf(page, id))).map((button) => button.id))
      .toEqual(['draft-open', 'published-delete']);

    await page.getByTestId('drafts-tab-mine').click();
    await page.getByTestId(`draft-open-${draft.id}`).click();
    await expect(builder.canvas).toBeVisible();
    await builder.waitSaved();
    await expect(builder.toolbar('publish')).toBeVisible();
    await expect.soft(toolbarExp).toHaveCount(0);
  });
});

test(
  'cards are selected one by one or all at once, by keyboard, and the selection outlives a change of tab and a new read of the lists',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const first = await builder.seedDraft(
      blankDocument(uniqueName(testInfo, 'select-a')),
    );
    const second = await builder.seedDraft(
      blankDocument(uniqueName(testInfo, 'select-b')),
    );

    await builder.open();
    const bar = page.getByTestId('bulk-bar-mine');
    const all = page.getByTestId('bulk-all-mine');
    const count = page.getByTestId('bulk-count-mine');
    const remove = page.getByTestId('bulk-delete-mine');
    const box = page.getByTestId(`card-select-${first.id}`);
    const card = cardOf(page, first.id);
    await expect(card).toBeVisible();

    await test.step('the row above the cards has Select all, the count and Delete selected, before the cards in the tab order', async () => {
      await expect(bar).toHaveRole('group');
      await expect(bar).toHaveAccessibleName('Bulk actions: My Drafts');
      await expect(all).toHaveAccessibleName('Select all');
      await expect(all).not.toBeChecked();
      const { count: selected, total } = await selectionOf(page, 'mine');
      expect(selected).toBe(0);
      expect(total).toBe((await checkboxesOf(page, 'mine')).total);
      expect(total).toBeGreaterThanOrEqual(2);

      // Nothing is selected: the button says so, and does nothing.
      await expect(remove).toHaveText('Delete selected');
      await expect(remove).toHaveAttribute('aria-disabled', 'true');
      await expect(remove).toHaveAccessibleDescription(
        `0 of ${total} selected`,
      );
      await remove.focus();
      await page.keyboard.press('Enter');
      await expect(page.getByRole('alertdialog')).toHaveCount(0);
      await expect(remove).toBeFocused();
      // Without sign-in no draft can be shared, so nothing offers it.
      await expect(page.getByTestId('bulk-share-mine')).toHaveCount(0);
      await expect(box).toHaveAccessibleName(
        new RegExp(`^Select ${first.title}, updated .+$`),
      );
      // Without sign-in nothing is shared with the user, so that tab has no
      // row.
      await expect(page.locator('#panel-shared .builder-bulk')).toHaveCount(0);

      // The row's buttons, then the cards' Tab stop (the first card), then
      // what is in that card.
      await page.getByTestId('drafts-tab-mine').focus();
      const order = [];
      for (let stop = 0; stop < 6; stop += 1) {
        await page.keyboard.press('Tab');
        order.push(
          await page.evaluate(() =>
            (document.activeElement.dataset.testid || '').replace(
              /^(card-select|draft-open|draft-card)-.*$/,
              '$1',
            ),
          ),
        );
      }
      expect(order).toEqual([
        'bulk-all-mine',
        'bulk-download-mine',
        'bulk-delete-mine',
        'draft-card',
        'card-select',
        'draft-open',
      ]);
    });

    await test.step('Select all checks every card, frames it, and says how many', async () => {
      await all.focus();
      await page.keyboard.press('Space');
      await expect(all).toBeChecked();
      const { total } = await selectionOf(page, 'mine');
      await expect(count).toHaveText(`${total} of ${total} selected`);
      expect(await checkboxesOf(page, 'mine')).toEqual({
        total,
        checked: total,
      });
      expect(await all.evaluate((element) => element.indeterminate)).toBe(
        false,
      );
      await expect(builder).toHaveAnnounced(`${total} of ${total} selected.`);
      await expect(card).toHaveClass(/is-selected/);
      // The frame is the accent color, as the checked box is.
      expect(
        await card.evaluate((element) => {
          const probe = document.createElement('span');
          probe.style.color = 'var(--bx-accent)';
          element.append(probe);
          const accent = getComputedStyle(probe).color;
          probe.remove();

          return getComputedStyle(element).borderTopColor === accent;
        }),
        'the selected card has the accent frame',
      ).toBe(true);
      await expect(remove).not.toHaveAttribute('aria-disabled', 'true');
      await expect(remove).toHaveAccessibleDescription(
        `${total} of ${total} selected`,
      );
    });

    await test.step('with one card unchecked, Select all is mixed', async () => {
      await box.focus();
      await page.keyboard.press('Space');
      await expect(box).not.toBeChecked();
      const { total } = await selectionOf(page, 'mine');
      await expect(count).toHaveText(`${total - 1} of ${total} selected`);
      expect(
        await all.evaluate((element) => ({
          indeterminate: element.indeterminate,
          checked: element.checked,
        })),
      ).toEqual({ indeterminate: true, checked: false });
      // Browsers tell assistive technology "mixed" from that property.
      await expect(all).toBeChecked({ indeterminate: true });
      await expect(builder).toHaveAnnounced(
        `${total - 1} of ${total} selected.`,
      );
      await expect(card).not.toHaveClass(/is-selected/);

      // Pressed while mixed it selects every card; pressed again, none.
      await all.click();
      await expect(count).toHaveText(`${total} of ${total} selected`);
      await expect(all).toBeChecked();
      await expect(box).toBeChecked();
      await all.click();
      await expect(count).toHaveText(`0 of ${total} selected`);
      await expect(all).not.toBeChecked();
      expect(await all.evaluate((element) => element.indeterminate)).toBe(
        false,
      );
      expect((await checkboxesOf(page, 'mine')).checked).toBe(0);
      await expect(builder).toHaveAnnounced(`0 of ${total} selected.`);
      await expect(remove).toHaveAttribute('aria-disabled', 'true');
    });

    await test.step('the selection is kept through another tab and a new read of the lists, without what left them', async () => {
      await box.check();
      await page.getByTestId(`card-select-${second.id}`).check();
      expect((await selectionOf(page, 'mine')).count).toBe(2);

      await page.getByTestId('drafts-tab-published').click();
      await expect(card).toBeHidden();
      await page.getByTestId('drafts-tab-mine').click();
      await expect(box).toBeChecked();

      await relist(page);
      await expect(box).toBeChecked();
      expect((await selectionOf(page, 'mine')).count).toBe(2);

      // Deleted elsewhere: its card goes, and it is not selected any more.
      const current = await builder.request.get(draftPath(second));
      const gone = await builder.request.delete(draftPath(second), {
        headers: { 'If-Match': current.headers().etag },
      });
      expect(gone.status()).toBe(204);
      await relist(page);
      await expect(page.getByTestId(`draft-open-${second.id}`)).toHaveCount(0);
      await expect(count).toHaveText(/^1 of \d+ selected$/);
      await expect(box).toBeChecked();
      await box.uncheck();
      await expect(count).toHaveText(/^0 of \d+ selected$/);
    });

    expectNoFatal(issues);
  },
);

test('the arrow keys, Space and Shift select cards, Delete asks about them, and Download selected saves a Builder file for each', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  test.setTimeout(90000);

  const drafts = [];
  for (const part of ['a', 'b', 'c']) {
    drafts.push(
      await builder.seedDraft(
        blankDocument(uniqueName(testInfo, `keys-${part}`)),
      ),
    );
  }

  // The list holds these three drafts alone, in this order, so the keys
  // move between them whatever other tests make meanwhile.
  await page.route('**/api/v1/builder/drafts', async (route) => {
    if (route.request().method() !== 'GET') {
      return route.fallback();
    }

    const response = await route.fetch();
    const body = await response.json();
    const listed = body.drafts || [];

    return route.fulfill({
      response,
      json: {
        ...body,
        drafts: drafts
          .map((draft) => listed.find((entry) => entry.id === draft.id))
          .filter(Boolean),
        damaged: [],
      },
    });
  });

  await builder.open();
  const cards = page.getByTestId('drafts-list-mine').locator(':scope > li');
  const card = (index) => cards.nth(index);
  const count = page.getByTestId('bulk-count-mine');
  const box = (index) => page.getByTestId(`card-select-${drafts[index].id}`);
  await expect(cards).toHaveCount(3);
  // The three cards are one row of the grid.
  expect(
    await cards.evaluateAll(
      (items) => new Set(items.map((item) => item.offsetTop)).size,
    ),
    'rows of cards',
  ).toBe(1);

  await test.step('Tab reaches the first card, which the arrow keys, Home and End move from', async () => {
    await page.getByTestId('bulk-delete-mine').focus();
    await page.keyboard.press('Tab');
    await expect(card(0)).toBeFocused();
    await expect(card(0)).toHaveAccessibleName(
      `${drafts[0].title} not selected`,
    );
    // The focused card has a focus ring.
    await expect(card(0)).toHaveCSS('outline-style', 'solid');

    await page.keyboard.press('ArrowRight');
    await expect(card(1)).toBeFocused();
    await page.keyboard.press('End');
    await expect(card(2)).toBeFocused();
    // Down from the last row goes nowhere.
    await page.keyboard.press('ArrowDown');
    await expect(card(2)).toBeFocused();
    await page.keyboard.press('Home');
    await expect(card(0)).toBeFocused();

    // The cards are one Tab stop: the card focus was last on, or on a
    // control of.
    await page.keyboard.press('ArrowRight');
    await expect(card(1)).toHaveAttribute('tabindex', '0');
    await expect(card(0)).toHaveAttribute('tabindex', '-1');
    await page.getByTestId(`draft-open-${drafts[2].id}`).focus();
    await expect(card(2)).toHaveAttribute('tabindex', '0');
    await expect(card(1)).toHaveAttribute('tabindex', '-1');

    // Shift+Tab from a card goes to the last control of the card before,
    // which makes that card the stop, as Tab from the row above then finds.
    await card(1).focus();
    await page.keyboard.press('Shift+Tab');
    await expect(card(0)).toHaveAttribute('tabindex', '0');
    await expect(card(1)).toHaveAttribute('tabindex', '-1');
    await page.getByTestId('bulk-delete-mine').focus();
    await page.keyboard.press('Tab');
    await expect(card(0)).toBeFocused();
  });

  await test.step('Space selects the focused card, Shift and an arrow key a range, and each change is said', async () => {
    await page.keyboard.press('Space');
    await expect(count).toHaveText('1 of 3 selected');
    await expect(builder).toHaveAnnounced('1 of 3 selected.');
    await expect(box(0)).toBeChecked();
    await expect(card(0)).toHaveAccessibleName(`${drafts[0].title} selected`);
    await expect(card(0)).toHaveClass(/is-selected/);

    await page.keyboard.press('Shift+ArrowRight');
    await expect(card(1)).toBeFocused();
    await expect(count).toHaveText('2 of 3 selected');
    await page.keyboard.press('Shift+ArrowRight');
    await expect(count).toHaveText('3 of 3 selected');
    await expect(builder).toHaveAnnounced('3 of 3 selected.');
    for (const index of [0, 1, 2]) {
      await expect(box(index)).toBeChecked();
    }

    // Back the other way, the range gives up what it took.
    await page.keyboard.press('Shift+ArrowLeft');
    await expect(count).toHaveText('2 of 3 selected');
    await expect(box(2)).not.toBeChecked();
    await page.keyboard.press('Shift+ArrowRight');
    await expect(count).toHaveText('3 of 3 selected');
  });

  await test.step('Delete asks once about the selection, and Cancel deletes nothing', async () => {
    const deletes = [];
    const watch = (request) => {
      if (request.method() === 'DELETE') {
        deletes.push(request.url());
      }
    };

    page.on('request', watch);
    await page.keyboard.press('Delete');
    const confirm = page.getByRole('alertdialog');
    await expect(confirm).toHaveAccessibleName('Delete 3 drafts?');
    await expect(confirm.getByTestId('confirm-cancel')).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(confirm).toHaveCount(0);
    await expect.soft(card(2)).toBeFocused();
    await expect(count).toHaveText('3 of 3 selected');
    page.off('request', watch);
    expect(deletes).toEqual([]);
  });

  await test.step('Escape clears the selection, Mod+A selects every card, and Shift with Space or Home a range back', async () => {
    await card(2).focus();
    await page.keyboard.press('Escape');
    await expect(count).toHaveText('0 of 3 selected');
    await expect(builder).toHaveAnnounced('0 of 3 selected.');
    await page.keyboard.press('ControlOrMeta+A');
    await expect(count).toHaveText('3 of 3 selected');

    // With nothing pressed since Select all, a checkbox cleared with Shift
    // clears its card alone, and stays cleared.
    await box(1).click({ modifiers: ['Shift'] });
    await expect(box(1)).not.toBeChecked();
    await expect(count).toHaveText('2 of 3 selected');
    await expect(box(0)).toBeChecked();
    await expect(box(2)).toBeChecked();

    await card(2).focus();
    await page.keyboard.press('Escape');
    await expect(count).toHaveText('0 of 3 selected');

    // With nothing pressed since, Shift+Space selects the card alone, and
    // Shift+Home the cards from it to the first.
    await page.keyboard.press('Shift+Space');
    await expect(count).toHaveText('1 of 3 selected');
    await expect(box(2)).toBeChecked();
    await page.keyboard.press('Shift+Home');
    await expect(card(0)).toBeFocused();
    await expect(count).toHaveText('3 of 3 selected');
    await page.keyboard.press('Escape');
    await expect(count).toHaveText('0 of 3 selected');

    // A checkbox checked with Shift selects the range from the last pressed.
    await box(0).click();
    await box(2).click({ modifiers: ['Shift'] });
    await expect(count).toHaveText('3 of 3 selected');
    await expect(box(1)).toBeChecked();
    await card(0).focus();
    await page.keyboard.press('Escape');
    await expect(count).toHaveText('0 of 3 selected');
  });

  await test.step('Download selected saves a Builder JSON file for each, one after another', async () => {
    await card(0).focus();
    await page.keyboard.press('Space');
    await page.keyboard.press('ArrowRight');
    await page.keyboard.press('Space');
    await expect(count).toHaveText('2 of 3 selected');

    const download = page.getByTestId('bulk-download-mine');
    await expect(download).toHaveAccessibleDescription(
      '2 of 3 selected Download selected saves a file for each; your browser may ask to allow several downloads.',
    );

    const files = [];
    page.on('download', (file) => files.push(file));
    await download.click();
    await expect(builder).toHaveAnnounced('Downloaded 2 drafts.');
    await expect.poll(() => files.length).toBe(2);

    const saved = [];
    for (const file of files) {
      saved.push({
        name: file.suggestedFilename(),
        doc: JSON.parse(fs.readFileSync(await file.path(), 'utf8')),
      });
    }
    expect(saved.map((file) => file.name)).toEqual([
      `${drafts[0].title}.json`,
      `${drafts[1].title}.json`,
    ]);
    expect(saved.map((file) => file.doc.metadata.name)).toEqual([
      drafts[0].title,
      drafts[1].title,
    ]);
    // The selection stays, and focus on the button.
    await expect(count).toHaveText('2 of 3 selected');
    await expect(download).toBeFocused();
    await expect(page.getByTestId('bulk-summary')).toHaveCount(0);
  });

  expectNoFatal(issues);
});

test('Download selected on Published Diagrams saves a Builder file of each selected published diagram', async ({
  page,
  request,
  builder,
  tracker,
  issues,
}, testInfo) => {
  test.setTimeout(90000);

  const topologies = [
    uniqueName(testInfo, 'dl-topo-a'),
    uniqueName(testInfo, 'dl-topo-b'),
  ];
  const published = [];
  for (const name of topologies) {
    published.push(
      await publishTopology(request, tracker, name, blankDocument(name), {
        keepDraft: false,
      }),
    );
  }
  const nameOf = Object.fromEntries(
    published.map(({ documentId }, index) => [documentId, topologies[index]]),
  );

  await builder.open();
  await page.getByTestId('drafts-tab-published').click();
  const download = page.getByTestId('bulk-download-published');
  const count = page.getByTestId('bulk-count-published');
  await expect(page.getByTestId('bulk-download-note-published')).toHaveText(
    'Download selected saves a file for each; your browser may ask to allow several downloads.',
  );
  await expect(download).toHaveAttribute('aria-disabled', 'true');

  for (const { documentId } of published) {
    await page.getByTestId(`card-select-${documentId}`).check();
  }
  await expect(count).toHaveText(/^2 of \d+ selected$/);
  await expect(download).not.toHaveAttribute('aria-disabled', 'true');

  // The files come in the order of the cards.
  const order = (
    await page
      .getByTestId('drafts-list-published')
      .locator(':scope > li')
      .evaluateAll((cards) =>
        cards.map((card) => card.dataset.testid.replace(/^draft-card-/, '')),
      )
  ).filter((id) => id in nameOf);

  const files = [];
  page.on('download', (file) => files.push(file));
  await download.click();
  await expect(builder).toHaveAnnounced('Downloaded 2 diagrams.');
  await expect.poll(() => files.length).toBe(2);

  const saved = [];
  for (const file of files) {
    saved.push({
      name: file.suggestedFilename(),
      doc: JSON.parse(fs.readFileSync(await file.path(), 'utf8')),
    });
  }
  expect(saved.map((file) => file.name)).toEqual(
    order.map((id) => `${nameOf[id]}.json`),
  );
  expect(saved.map((file) => file.doc.metadata.name)).toEqual(
    order.map((id) => nameOf[id]),
  );
  await expect(count).toHaveText(/^2 of \d+ selected$/);
  await expect(download).toBeFocused();
  await expect(page.getByTestId('bulk-summary')).toHaveCount(0);

  expectNoFatal(issues);
});

test('Delete selected deletes the selected drafts or topologies after one question, and lists what it could not delete', async ({
  page,
  request,
  builder,
  tracker,
  issues,
}, testInfo) => {
  test.setTimeout(120000);

  const names = ['one', 'two', 'three', 'four', 'five', 'six'].map((part) =>
    uniqueName(testInfo, `bulk-${part}`),
  );
  // A name with no place to break it, which the question must still fit.
  names[0] += `_${'W'.repeat(110)}`;
  const drafts = [];
  for (const name of names) {
    drafts.push(await builder.seedDraft(blankDocument(name)));
  }
  const [one, two, three, four, five, six] = drafts;
  const topologies = [
    uniqueName(testInfo, 'bulk-topo-a'),
    uniqueName(testInfo, 'bulk-topo-b'),
  ];
  const published = [];
  for (const name of topologies) {
    published.push(
      await publishTopology(request, tracker, name, blankDocument(name), {
        keepDraft: false,
      }),
    );
  }

  await builder.open();
  const count = page.getByTestId('bulk-count-mine');
  const remove = page.getByTestId('bulk-delete-mine');
  const select = (draft) => page.getByTestId(`card-select-${draft.id}`);
  const open = (draft) => page.getByTestId(`draft-open-${draft.id}`);
  await expect(open(one)).toBeVisible();

  // Every DELETE of a draft the page sends, and every read of one.
  const sent = [];
  page.on('response', (response) => {
    const { pathname } = new URL(response.url());
    const method = response.request().method();

    if (
      /\/builder\/drafts\/[^/]+\/[^/]+$/.test(pathname) &&
      ['DELETE', 'GET'].includes(method)
    ) {
      sent.push(`${method} ${pathname.split('/').pop()} ${response.status()}`);
    }
  });

  await test.step('one question names the drafts and how many, and Cancel deletes nothing', async () => {
    for (const draft of [one, two, three]) {
      await select(draft).check();
    }
    await expect(count).toHaveText(/^3 of \d+ selected$/);
    await expect(remove).toHaveAttribute('aria-haspopup', 'dialog');

    const confirm = await askDeleteSelected(page, 'mine', 'Delete 3 drafts?');
    const message = await confirm.locator('p').textContent();
    for (const draft of [one, two, three]) {
      expect(message).toContain(draft.title);
    }
    expect(message).toMatch(
      / and .+\. The drafts and their whole histories are removed from the server\. This cannot be undone\.$/,
    );
    await expect(confirm.getByTestId('confirm-accept')).toHaveText(
      'Delete 3 drafts',
    );
    // Focus starts on the button that keeps everything.
    await expect(confirm.getByTestId('confirm-cancel')).toBeFocused();

    // At 320 pixels wide the long name wraps inside the question, which
    // does not scroll sideways (WCAG 1.4.10).
    const wide = page.viewportSize();
    await page.setViewportSize({ width: 320, height: 800 });
    const fit = await confirm.evaluate((element) => ({
      scroll: element.scrollWidth,
      client: element.clientWidth,
      message: element.querySelector('p').getBoundingClientRect().right,
      edge: element.getBoundingClientRect().right,
    }));
    expect
      .soft(fit.scroll, 'the width the question scrolls over')
      .toBeLessThanOrEqual(fit.client);
    expect.soft(fit.message, 'message ends').toBeLessThanOrEqual(fit.edge);
    await page.setViewportSize(wide);

    await page.keyboard.press('Enter');
    await expect(confirm).toHaveCount(0);
    await expect(remove).toBeFocused();
    await expect(count).toHaveText(/^3 of \d+ selected$/);
    expect(sent).toEqual([]);
  });

  await test.step('confirmed, the drafts are deleted, one although it changed since the list was read, and one deleted elsewhere counts', async () => {
    // Deleted elsewhere since the list was read: its card is still here.
    const gone = await request.get(draftPath(three));
    const deleted = await request.delete(draftPath(three), {
      headers: { 'If-Match': gone.headers().etag },
    });
    expect(deleted.status(), await deleted.text()).toBe(204);

    // The card's ETag is then older than the server's.
    const current = await request.get(draftPath(two));
    const changed = await request.post(`${draftPath(two)}/snapshots`, {
      headers: { 'If-Match': current.headers().etag },
      data: {
        summary: 'Changed elsewhere',
        document: {
          ...blankDocument(two.title),
          viewport: { x: 40, y: 40, zoom: 1 },
        },
      },
    });
    expect(changed.ok(), await changed.text()).toBeTruthy();

    const confirm = await askDeleteSelected(page, 'mine', 'Delete 3 drafts?');
    await confirm.getByTestId('confirm-accept').click();

    for (const draft of [one, two, three]) {
      await expect(open(draft)).toHaveCount(0);
    }
    await expect(builder).toHaveAnnounced('Deleted 3 drafts.');
    // Refused for its old ETag, the draft is deleted with the one the
    // refusal carries, without being read. The draft that was gone
    // already is what was asked for: no failure.
    expect([...sent].sort()).toEqual(
      [
        `DELETE ${one.id} 204`,
        `DELETE ${two.id} 412`,
        `DELETE ${two.id} 204`,
        `DELETE ${three.id} 404`,
      ].sort(),
    );
    for (const draft of [one, two, three]) {
      expect((await request.get(draftPath(draft))).status()).toBe(404);
    }
    // Focus stays on the button, and nothing is selected.
    await expect(remove).toBeFocused();
    await expect(count).toHaveText(/^0 of \d+ selected$/);
    await expect(page.getByTestId('bulk-summary')).toHaveCount(0);
    await expect(page.getByTestId('builder-error')).toHaveCount(0);
  });

  await test.step('a draft that could not be deleted is listed with why, in a summary that takes focus, and stays selected', async () => {
    const FAILING = `**${draftPath(five)}`;
    await page.route(FAILING, (route) =>
      route.request().method() === 'DELETE'
        ? route.fulfill({ status: 500, json: { message: 'boom' } })
        : route.fallback(),
    );
    for (const draft of [four, five, six]) {
      await select(draft).check();
    }
    await expect(count).toHaveText(/^3 of \d+ selected$/);

    const confirm = await askDeleteSelected(page, 'mine', 'Delete 3 drafts?');
    await confirm.getByTestId('confirm-accept').click();

    const summary = page.getByTestId('bulk-summary');
    await expect(summary).toBeVisible();
    await expect(summary).toBeFocused();
    await expect(summary).toHaveRole('alert');
    await expect(summary).toHaveAccessibleName(
      '1 of 3 drafts could not be deleted. The other 2 were deleted.',
    );
    await expect(summary.getByRole('listitem')).toHaveText([
      new RegExp(`^${five.title}, updated .+: Boom\\.$`),
    ]);
    // It is between the row and the cards of the tab, and the page alert
    // is not used.
    expect(
      await page
        .locator('#panel-mine > *')
        .evaluateAll((children) =>
          children.map((child) => child.dataset.testid),
        ),
    ).toEqual(['bulk-bar-mine', 'bulk-summary', 'drafts-list-mine']);
    await expect(page.getByTestId('builder-error')).toHaveCount(0);
    await expect(builder).not.toHaveAnnounced('Deleted 2 drafts.');

    await expect(open(four)).toHaveCount(0);
    await expect(open(six)).toHaveCount(0);
    await expect(open(five)).toBeVisible();
    await expect(select(five)).toBeChecked();
    await expect(count).toHaveText(/^1 of \d+ selected$/);
    expect((await request.get(draftPath(five))).status()).toBe(200);

    // Dismiss removes it, and focus moves to Select all.
    await summary.getByTestId('bulk-summary-dismiss').click();
    await expect(summary).toHaveCount(0);
    await expect(page.getByTestId('bulk-all-mine')).toBeFocused();
    await page.unroute(FAILING);
  });

  await test.step('one draft alone is asked about as its own Delete asks, and deleted the same way', async () => {
    await expect(select(five)).toBeChecked();
    const confirm = await askDeleteSelected(
      page,
      'mine',
      new RegExp(`^Delete draft ${five.title}, updated .+\\?$`),
    );
    await expect(confirm.locator('p')).toHaveText(
      'The draft and its whole history are removed from the server. This cannot be undone.',
    );
    await expect(confirm.getByTestId('confirm-accept')).toHaveText(
      'Delete draft',
    );
    await confirm.getByTestId('confirm-accept').click();
    await expect(open(five)).toHaveCount(0);
    await expect(builder).toHaveAnnounced('Deleted 1 draft.');
    expect((await request.get(draftPath(five))).status()).toBe(404);
  });

  await test.step('published topologies are deleted the same way, one question for the batch', async () => {
    await page.getByTestId('drafts-tab-published').click();
    const [first, second] = published.map(({ documentId }) => documentId);
    await expect(page.getByTestId('bulk-bar-published')).toHaveAccessibleName(
      'Bulk actions: Published Diagrams',
    );
    await expect(page.getByTestId(`card-select-${first}`)).toHaveAccessibleName(
      new RegExp(`^Select ${topologies[0]}, published .+$`),
    );
    await page.getByTestId(`card-select-${first}`).check();
    await page.getByTestId(`card-select-${second}`).check();
    await expect(page.getByTestId('bulk-count-published')).toHaveText(
      /^2 of \d+ selected$/,
    );

    const confirm = await askDeleteSelected(
      page,
      'published',
      'Delete 2 topologies?',
    );
    const message = await confirm.locator('p').textContent();
    for (const name of topologies) {
      expect(message).toContain(name);
    }
    expect(message).toMatch(
      / and .+\. The topologies are deleted from phēnix\. Drafts and experiments made from them are not changed\.$/,
    );
    await expect(confirm.getByTestId('confirm-cancel')).toBeFocused();
    await expect(confirm.getByTestId('confirm-accept')).toHaveText(
      'Delete 2 topologies',
    );
    await confirm.getByTestId('confirm-accept').click();

    await expect(page.getByTestId(`draft-open-${first}`)).toHaveCount(0);
    await expect(page.getByTestId(`draft-open-${second}`)).toHaveCount(0);
    await expect(builder).toHaveAnnounced('Deleted 2 topologies.');
    for (const name of topologies) {
      expect(await builder.config('Topology', name)).toBeNull();
    }
    await expect(page.getByTestId('bulk-summary')).toHaveCount(0);
    await expect(page.getByTestId('builder-error')).toHaveCount(0);
  });

  expectNoFatal(issues);
});

test(
  'axe finds no serious violations in a selection, the question, the summary of what failed, and the dialog that shares several drafts',
  { tag: '@axe' },
  async ({ page, request, builder, tracker }, testInfo) => {
    test.slow();

    const drafts = [];
    for (const part of ['a', 'b', 'c']) {
      drafts.push(
        await builder.seedDraft(
          blankDocument(uniqueName(testInfo, `bulk-axe-${part}`)),
        ),
      );
    }
    const ours = new Set(drafts.map((draft) => draft.id));
    // A published diagram, for the row of Published Diagrams.
    const topologyName = uniqueName(testInfo, 'bulk-axe-topo');
    const topology = await publishTopology(
      request,
      tracker,
      topologyName,
      blankDocument(topologyName),
      { keepDraft: false },
    );

    // Sharing needs sign-in, which this server has not: the listing says
    // these drafts can be shared, and names two users to share them with,
    // so the dialog can be opened and scanned. Nothing is shared.
    await page.route('**/api/v1/builder/drafts', async (route) => {
      if (route.request().method() !== 'GET') {
        return route.fallback();
      }

      const response = await route.fetch();
      const body = await response.json();

      return route.fulfill({
        response,
        json: {
          ...body,
          drafts: (body.drafts || []).map((draft) =>
            ours.has(draft.id) ? { ...draft, canShare: true } : draft,
          ),
        },
      });
    });
    await page.route(
      '**/api/v1/builder/drafts/*/*/shares/candidates',
      (route) =>
        route.fulfill({
          json: {
            users: [
              { username: 'bob', name: 'Bob Builder' },
              { username: 'carol', name: '' },
            ],
          },
        }),
    );
    // One of the drafts is not deleted, so the summary shows.
    await page.route(`**${draftPath(drafts[1])}`, (route) =>
      route.request().method() === 'DELETE'
        ? route.fulfill({ status: 500, json: { message: 'boom' } })
        : route.fallback(),
    );

    // Reduced motion turns off color transitions, so every surface is
    // measured at its final colors.
    await page.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' });
    await builder.open();

    // Scans what is on screen in the light theme, then in the dark one.
    const scan = async (label, include) => {
      for (const scheme of ['light', 'dark']) {
        await page.emulateMedia({
          colorScheme: scheme,
          reducedMotion: 'reduce',
        });
        await expect(page.locator('.builder-root')).toHaveAttribute(
          'data-builder-theme',
          scheme,
        );
        await expectAccessible(page, {
          include,
          soft: true,
          label: `axe on ${label} (${scheme})`,
        });
        await expectNoInvisibleText(page, include);
      }
    };

    await test.step('the drafts with some selected', async () => {
      for (const draft of drafts) {
        await page.getByTestId(`card-select-${draft.id}`).check();
      }
      await expect(page.getByTestId('bulk-count-mine')).toHaveText(
        /^3 of \d+ selected$/,
      );
      await scan('the drafts with a selection');
    });

    await test.step('the dialog that shares them', async () => {
      await page.getByTestId('bulk-share-mine').click();
      const dialog = page.getByTestId('bulk-share-dialog');
      await expect(dialog).toBeVisible();
      await expect(dialog).toHaveAccessibleName('Share 3 drafts');
      const field = dialog.getByTestId('bulk-share-user');
      await expect(field).toBeFocused();
      await expect(dialog.getByTestId('bulk-share-users-note')).toHaveText('');
      await scan('the bulk Share dialog', '[data-testid="bulk-share-dialog"]');

      // With the list of users open, someone added, and a refused name.
      await field.fill('carol');
      await field.press('Enter');
      await expect(dialog.getByTestId('bulk-share-row-carol')).toBeVisible();
      await field.fill('carol');
      await field.press('Enter');
      await expect(dialog.getByTestId('bulk-share-user-error')).toHaveText(
        'carol is already in the list.',
      );
      await field.fill('');
      await field.press('ArrowDown');
      await expect(
        dialog.getByRole('listbox', { name: 'Users' }),
      ).toBeVisible();
      await scan(
        'the bulk Share dialog with people',
        '[data-testid="bulk-share-dialog"]',
      );

      // Escape closes the list first, then the dialog; nothing was shared.
      await page.keyboard.press('Escape');
      await expect(dialog).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);
      await expect(page.getByTestId('bulk-share-mine')).toBeFocused();
    });

    await test.step('the question Delete selected asks', async () => {
      const confirm = await askDeleteSelected(page, 'mine', 'Delete 3 drafts?');
      await scan('the bulk delete question', '[data-testid="builder-confirm"]');
      await confirm.getByTestId('confirm-accept').click();
    });

    await test.step('the summary of what was not deleted', async () => {
      const summary = page.getByTestId('bulk-summary');
      await expect(summary).toBeFocused();
      await expect(summary).toHaveAccessibleName(
        '1 of 3 drafts could not be deleted. The other 2 were deleted.',
      );
      await scan('the drafts with a failure summary');
    });

    await test.step('Published Diagrams with a selection, Download selected and its note', async () => {
      await page.getByTestId('drafts-tab-published').click();
      await page.getByTestId(`card-select-${topology.documentId}`).check();
      await expect(page.getByTestId('bulk-count-published')).toHaveText(
        /^1 of \d+ selected$/,
      );
      await expect(
        page.getByTestId('bulk-download-note-published'),
      ).toBeVisible();
      await scan('Published Diagrams with a selection', '#panel-published');
    });
  },
);
