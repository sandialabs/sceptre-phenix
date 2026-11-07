// The VLANs page over the stubbed sample cluster (vlan.Merge output from Go):
// the three views, the details panel and each dialog. Nothing changes the store.
const { test, expect, expectAccessible } = require('./helpers');
const cluster = require('../../test/helpers/vlansFixture.json');

const ends = (suffix) => (url) => url.pathname.endsWith(`/api/v1/${suffix}`);

// Answers every VLAN route the page calls from the sample cluster. It refuses
// each change and returns the list of them, which a test must leave empty.
async function standInCluster(page) {
  const changes = [];
  const refuse = (route) => {
    changes.push(`${route.request().method()} ${route.request().url()}`);
    return route.abort();
  };
  await page.route(ends('vlans'), (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ json: cluster })
      : refuse(route),
  );
  await page.route(
    (url) => /\/api\/v1\/vlans\/\d+$/.test(url.pathname),
    (route) => {
      const id = Number(
        new URL(route.request().url()).pathname.split('/').pop(),
      );
      return route.fulfill({
        json: {
          id,
          vlans: cluster.vlans.filter((row) => row.id === id),
          vms: [],
          issues: cluster.issues.filter((issue) => issue.vlan === id),
        },
      });
    },
  );
  await page.route(ends('vlans/check'), (route) =>
    route.fulfill({ json: { issues: [] } }),
  );
  await page.route(ends('vlans/free'), (route) =>
    route.fulfill({ json: { start: 1, end: 16 } }),
  );
  await page.route(
    (url) =>
      /\/api\/v1\/vlans\/(blacklist|allocations)\//.test(url.pathname) ||
      /\/api\/v1\/experiments\/[^/]+\/vlans\//.test(url.pathname),
    refuse,
  );
  return changes;
}

// the card of an experiment, an unowned namespace or the blacklist
const card = (page, key) => page.locator(`[data-card="${key}"]`);

// the details panel, once it shows the ID
async function expectPanel(page, id) {
  const panel = page.getByRole('complementary', { name: `VLAN ${id}` });
  await expect(panel).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`[?&]vlan=${id}(&|$)`));
  return panel;
}

// the dialog on screen, once its title is title
async function expectDialog(page, title) {
  const dialog = page.locator('.modal.is-active');
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('.modal-card-title')).toHaveText(title);
  return dialog;
}

// Scans the open dialog, then cancels it.
async function scanAndCancel(page, dialog, label) {
  await expectAccessible(page, label);
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('.modal.is-active')).toHaveCount(0);
}

let changes = [];

test.beforeEach(async ({ page }) => {
  changes = await standInCluster(page);
});

test.afterEach(() => {
  expect(changes, 'changes sent to the server').toEqual([]);
});

test('the By experiment view shows a card for each owner, issues first', async ({
  page,
}) => {
  await page.goto('/vlans');
  await expect(card(page, 'experiment:substation-a')).toBeVisible();

  await expect(page.locator('.vlans-banner')).toContainText(
    '7 issues: 3 conflicts, 2 differences, 2 warnings.',
  );
  await expect(page.locator('.vlan-card-select')).toHaveText([
    'lab',
    'old-demo',
    'scratch',
    'substation-a',
    'red-team-2',
    'ics-training',
  ]);
  // each experiment's card shows the issues from its own point of view
  await expect(card(page, 'experiment:scratch')).toContainText(
    'Also pinned by substation-a (SCADA).',
  );
  await expect(card(page, 'experiment:substation-a')).toContainText(
    'Also pinned by scratch (EXP).',
  );
  await expect(card(page, 'experiment:substation-a')).toContainText(
    '150–199 overlaps ics-training.',
  );
  // each alias row is a line under the one before it, inside its card
  const overlaps = await page.locator('.vlan-alias-list').evaluateAll((lists) =>
    lists.flatMap((list) => {
      const rows = [...list.children].map((row) => row.getBoundingClientRect());
      const box = list.closest('article').getBoundingClientRect();
      return rows.flatMap((row, i) => {
        const bad = [];
        const parts = [...list.children[i].querySelectorAll('*')];
        // screen-reader-only text is 1 pixel and placed off to the side
        const seen = parts
          .map((el) => el.getBoundingClientRect())
          .filter((r) => r.width > 1 && r.height > 1);
        const out = [row, ...seen];
        if (out.some((r) => r.left < box.left - 1 || r.right > box.right + 1))
          bad.push('outside');
        if (i && row.top < rows[i - 1].bottom - 1) bad.push('overlaps');
        return bad.length
          ? [`${list.closest('article')?.dataset.card} row ${i}: ${bad}`]
          : [];
      });
    }),
  );
  expect(overlaps).toEqual([]);
  // no card is wider than its column, and the page does not scroll sideways
  const wide = await page.locator('.vlans-cards').evaluate((grid) => {
    const box = grid.getBoundingClientRect();
    return [...grid.querySelectorAll('article')]
      .filter((a) => a.getBoundingClientRect().right > box.right + 1)
      .map((a) => a.dataset.card);
  });
  expect(wide).toEqual([]);
  // the cards pack as a masonry: each card is one gap under the card above it
  const placed = await page.locator('.vlans-cards').evaluate((grid) => {
    const top = grid.getBoundingClientRect().top;
    const gap = parseFloat(getComputedStyle(grid).columnGap);
    const boxes = [...grid.children].map((c) => c.getBoundingClientRect());
    return boxes.flatMap((box, i) => {
      const above = boxes
        .filter((b) => Math.abs(b.left - box.left) < 1 && b.top < box.top)
        .map((b) => b.bottom + gap);
      const off = box.top - Math.max(top, ...above);
      return Math.abs(off) > 5
        ? [`${grid.children[i].dataset.card} ${Math.round(off)}px`]
        : [];
    });
  });
  expect(placed).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    ),
  ).toBeLessThanOrEqual(0);
  await expectAccessible(page, '/vlans');
});

test('a card alias opens the details panel from the keyboard', async ({
  page,
}) => {
  await page.goto('/vlans');
  const scada = card(page, 'experiment:substation-a').getByRole('button', {
    name: 'Show VLAN 102',
    exact: true,
  });
  await scada.focus();
  await page.keyboard.press('Enter');

  const panel = await expectPanel(page, 102);
  await expect(panel).toContainText('substation-a pins SCADA = 102');
  await expect(panel).toContainText('scratch pins EXP = 102');
  await expect(panel).toContainText(
    'scratch EXP: Also pinned by substation-a (SCADA).',
  );
  await expectAccessible(page, '/vlans details');

  // the panel shows the owner's card, and closes
  await panel
    .locator('.vlan-details-links')
    .filter({
      has: page.getByRole('heading', { name: 'scratch', exact: true }),
    })
    .getByRole('button', { name: 'Show the card' })
    .click();
  await expect(card(page, 'experiment:scratch')).toHaveClass(/is-selected/);
  await panel.getByRole('button', { name: 'Close the VLAN details' }).click();
  await expect(panel).toBeHidden();
});

test('the ID space view opens an ID from the keyboard', async ({ page }) => {
  await page.goto('/vlans?view=grid');
  const cursor = page.getByTestId('vlan-grid-cursor');
  await expect(cursor).toHaveAccessibleName(/^ID 1: Free\./);
  await expect(
    page.getByRole('button', { name: 'ID space', pressed: true }),
  ).toBeVisible();
  await expectAccessible(page, '/vlans?view=grid');

  // one row down is 65, then 37 IDs right is 102
  await cursor.focus();
  await page.keyboard.press('ArrowDown');
  for (let i = 0; i < 37; i++) await page.keyboard.press('ArrowRight');
  await expect(cursor).toHaveAccessibleName(
    /^ID 102: Conflict or unowned\. EXP of scratch and SCADA of substation-a\./,
  );
  await page.keyboard.press('Enter');

  const panel = await expectPanel(page, 102);
  await expect(page).toHaveURL(/[?&]view=grid(&|$)/);
  await expect(panel).toContainText('Conflict or unowned');
  await expectAccessible(page, '/vlans?view=grid details');
});

test('each dialog opens, passes the scan and cancels', async ({ page }) => {
  await page.goto('/vlans');
  const scratch = card(page, 'experiment:scratch');
  const ics = card(page, 'experiment:ics-training');
  await expect(scratch).toBeVisible();

  await test.step('change a conflicting pin', async () => {
    await scratch.getByRole('button', { name: /^Change EXP to \d+…$/ }).click();
    const dialog = await expectDialog(page, 'Change the VLAN ID of EXP');
    await expect(dialog).toContainText(/\d+ is free in the cluster\./);
    await scanAndCancel(page, dialog, 'change pin dialog');
  });

  await test.step('edit the IDs', async () => {
    await ics.getByRole('button', { name: 'Edit IDs…' }).click();
    const dialog = await expectDialog(
      page,
      'Edit the VLAN IDs of ics-training',
    );
    await scanAndCancel(page, dialog, 'edit IDs dialog');
  });

  await test.step('set a range', async () => {
    await ics.getByRole('button', { name: 'Suggest a range…' }).click();
    const dialog = await expectDialog(page, 'Set the VLAN range');
    await scanAndCancel(page, dialog, 'range dialog');
  });

  await test.step('pin the live IDs', async () => {
    await card(page, 'experiment:substation-a')
      .getByRole('button', { name: 'Pin live IDs…' })
      .click();
    const dialog = await expectDialog(page, 'Pin the Live IDs');
    await expect(dialog).toContainText('CORP: 104 → 110');
    await scanAndCancel(page, dialog, 'pin live dialog');
  });

  await test.step('release the IDs of an unowned namespace', async () => {
    await card(page, 'namespace:old-demo')
      .getByRole('button', { name: 'Release 4 IDs…' })
      .click();
    const dialog = await expectDialog(page, 'Release Unowned VLANs');
    await scanAndCancel(page, dialog, 'release dialog');
  });

  await test.step('add an ID to the blacklist', async () => {
    const view = page.getByRole('group', { name: 'View' });
    await view.getByRole('button', { name: 'Blacklist', exact: true }).click();
    await expect(
      view.getByRole('button', { name: 'Blacklist', pressed: true }),
    ).toBeVisible();
    await expect(page).toHaveURL(/[?&]view=blacklist(&|$)/);
    await card(page, 'blacklist')
      .getByRole('button', { name: 'Add an ID…' })
      .click();
    const dialog = await expectDialog(page, 'Add to the Blacklist');
    await expect(dialog.getByLabel('VLAN ID to blacklist')).toBeVisible();
    await scanAndCancel(page, dialog, 'blacklist add dialog');
  });

  await test.step('remove an ID from the blacklist', async () => {
    await card(page, 'blacklist')
      .getByRole('button', { name: 'Remove 999 from the blacklist' })
      .click();
    const dialog = await expectDialog(page, 'Remove from the Blacklist');
    await scanAndCancel(page, dialog, 'blacklist remove dialog');
  });

  await test.step('find free IDs', async () => {
    await page.getByRole('button', { name: 'Find free IDs' }).click();
    const finder = page.getByRole('region', { name: 'Find free IDs' });
    await finder.getByRole('button', { name: 'Find 16 free IDs' }).click();
    await expect(finder).toContainText('Free block found: IDs 1–16.');
    // a block found shows in the ID space view
    await expect(page).toHaveURL(/[?&]view=grid(&|$)/);
    await expectAccessible(page, 'free ID finder');
    await finder.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(finder).toBeHidden();
  });
});
