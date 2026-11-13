// The VLANs page over the stubbed sample cluster (vlan.Merge output from Go):
// its layout, keyboard use, dialogs and axe scans. Nothing changes the store.
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

// Cancels the open dialog.
async function cancel(dialog) {
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog).toHaveCount(0);
}

// Each dialog, in the order they open from /vlans: its scan label, its title
// and the steps that open it. The blacklist dialogs open in the Blacklist view.
const DIALOGS = [
  [
    'change pin dialog',
    'Change the VLAN ID of EXP',
    (page) =>
      card(page, 'experiment:scratch')
        .getByRole('button', { name: /^Change EXP to \d+…$/ })
        .click(),
  ],
  [
    'edit IDs dialog',
    'Edit the VLAN IDs of ics-training',
    (page) =>
      card(page, 'experiment:ics-training')
        .getByRole('button', { name: 'Edit IDs…' })
        .click(),
  ],
  [
    'range dialog',
    'Set the VLAN range',
    (page) =>
      card(page, 'experiment:ics-training')
        .getByRole('button', { name: 'Suggest a range…' })
        .click(),
  ],
  [
    'pin live dialog',
    'Pin the Live IDs',
    (page) =>
      card(page, 'experiment:substation-a')
        .getByRole('button', { name: 'Pin live IDs…' })
        .click(),
  ],
  [
    'release dialog',
    'Release Unowned VLANs',
    (page) =>
      card(page, 'namespace:old-demo')
        .getByRole('button', { name: 'Release 4 IDs…' })
        .click(),
  ],
  [
    'blacklist add dialog',
    'Add to the Blacklist',
    async (page) => {
      await page
        .getByRole('group', { name: 'View' })
        .getByRole('button', { name: 'Blacklist', exact: true })
        .click();
      await expect(page).toHaveURL(/[?&]view=blacklist(&|$)/);
      await card(page, 'blacklist')
        .getByRole('button', { name: 'Add an ID…' })
        .click();
    },
  ],
  [
    'blacklist remove dialog',
    'Remove from the Blacklist',
    (page) =>
      card(page, 'blacklist')
        .getByRole('button', { name: 'Remove 999 from the blacklist' })
        .click(),
  ],
];

let changes = [];

test.beforeEach(async ({ page }) => {
  changes = await standInCluster(page);
});

test.afterEach(() => {
  expect(changes, 'changes sent to the server').toEqual([]);
});

test('the By experiment view packs its cards in columns, each alias row on its own line', async ({
  page,
}) => {
  await page.goto('/vlans');
  const grid = page.locator('.vlans-cards');
  await expect(card(page, 'experiment:ics-training')).toBeVisible();
  await expect(grid).toHaveClass(/is-masonry/);

  // each alias row is a line under the one before it, inside its card
  await expect
    .poll(() =>
      page.locator('.vlan-alias-list').evaluateAll((lists) =>
        lists.flatMap((list) => {
          const rows = [...list.children].map((row) =>
            row.getBoundingClientRect(),
          );
          const box = list.closest('article').getBoundingClientRect();
          return rows.flatMap((row, i) => {
            const bad = [];
            const parts = [...list.children[i].querySelectorAll('*')];
            // screen-reader-only text is 1 pixel and placed off to the side
            const seen = parts
              .map((el) => el.getBoundingClientRect())
              .filter((r) => r.width > 1 && r.height > 1);
            const out = [row, ...seen];
            if (
              out.some((r) => r.left < box.left - 1 || r.right > box.right + 1)
            )
              bad.push('outside');
            if (i && row.top < rows[i - 1].bottom - 1) bad.push('overlaps');
            return bad.length
              ? [`${list.closest('article')?.dataset.card} row ${i}: ${bad}`]
              : [];
          });
        }),
      ),
    )
    .toEqual([]);
  // no card is wider than its column, and the page does not scroll sideways
  await expect
    .poll(() =>
      grid.evaluate((el) => {
        const box = el.getBoundingClientRect();
        return [...el.querySelectorAll('article')]
          .filter((a) => a.getBoundingClientRect().right > box.right + 1)
          .map((a) => a.dataset.card);
      }),
    )
    .toEqual([]);
  // the cards pack as a masonry: each card is one gap under the card above it
  await expect
    .poll(() =>
      grid.evaluate((el) => {
        const top = el.getBoundingClientRect().top;
        const gap = parseFloat(getComputedStyle(el).columnGap);
        const boxes = [...el.children].map((c) => c.getBoundingClientRect());
        return boxes.flatMap((box, i) => {
          const above = boxes
            .filter((b) => Math.abs(b.left - box.left) < 1 && b.top < box.top)
            .map((b) => b.bottom + gap);
          const off = box.top - Math.max(top, ...above);
          return Math.abs(off) > 5
            ? [`${el.children[i].dataset.card} ${Math.round(off)}px`]
            : [];
        });
      }),
    )
    .toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    ),
  ).toBeLessThanOrEqual(0);
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
  await expectPanel(page, 102);
});

test('the ID space view opens an ID from the keyboard', async ({ page }) => {
  await page.goto('/vlans?view=grid');
  const cursor = page.getByTestId('vlan-grid-cursor');
  await expect(cursor).toHaveAccessibleName(/^ID 1: Free\./);

  // one row down is 65, then 37 IDs right is 102
  await cursor.focus();
  await page.keyboard.press('ArrowDown');
  for (let i = 0; i < 37; i++) await page.keyboard.press('ArrowRight');
  await expect(cursor).toHaveAccessibleName(
    /^ID 102: Conflict or unowned\. EXP of scratch and SCADA of substation-a\./,
  );
  await page.keyboard.press('Enter');

  await expectPanel(page, 102);
  await expect(page).toHaveURL(/[?&]view=grid(&|$)/);
});

test('each dialog opens and cancels', async ({ page }) => {
  await page.goto('/vlans');
  for (const [label, title, open] of DIALOGS) {
    await test.step(label, async () => {
      await open(page);
      await cancel(await expectDialog(page, title));
    });
  }
});

test(
  'the views and the details panels pass the scan',
  { tag: '@axe' },
  async ({ page }) => {
    await page.goto('/vlans');
    await expect(card(page, 'experiment:substation-a')).toBeVisible();
    await expectAccessible(page, '/vlans');

    await page.goto('/vlans?vlan=102');
    await expectPanel(page, 102);
    await expectAccessible(page, '/vlans details');

    await page.goto('/vlans?view=grid');
    await expect(page.getByTestId('vlan-grid-cursor')).toHaveAccessibleName(
      /^ID 1: Free\./,
    );
    await expectAccessible(page, '/vlans?view=grid');

    await page.goto('/vlans?view=grid&vlan=102');
    await expectPanel(page, 102);
    await expectAccessible(page, '/vlans?view=grid details');
  },
);

test(
  'each dialog and the free ID finder pass the scan',
  { tag: '@axe' },
  async ({ page }) => {
    await page.goto('/vlans');
    for (const [label, title, open] of DIALOGS) {
      await open(page);
      const dialog = await expectDialog(page, title);
      await expectAccessible(page, label);
      await cancel(dialog);
    }

    await page.getByRole('button', { name: 'Find free IDs' }).click();
    const finder = page.getByRole('region', { name: 'Find free IDs' });
    await finder.getByRole('button', { name: 'Find 16 free IDs' }).click();
    await expect(finder).toContainText('Free block found: IDs 1–16.');
    await expectAccessible(page, 'free ID finder');
  },
);
