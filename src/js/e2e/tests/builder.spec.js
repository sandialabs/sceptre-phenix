// Smoke tests for the Topology Builder.
//
// Builder is served by the Go binary at /builder, outside the Vue router, so
// these navigate straight there rather than seeding a session first.
//
// The geometry test is the point of this file: four separate reports of dialog
// buttons rendering outside their dialog, or content rendering underneath a
// pinned button row, all came from hand-tuned pixel heights. Asserting that the
// buttons sit inside the dialog catches that class mechanically.
const { test, expect } = require('@playwright/test');
const { attachCapture, settle, fatalOf } = require('./helpers');

async function openBuilder(page, issues) {
  attachCapture(page, issues);
  await page.goto('/builder');
  await settle(page, 2000);
  await expect(page.locator('.geMenubar')).toBeVisible();
}

// Opens a menubar menu and clicks one of its items by visible label.
async function menu(page, menuLabel, itemLabel) {
  await page
    .locator('.geMenubar a')
    .filter({ hasText: new RegExp(`^\\s*${menuLabel}\\s*$`) })
    .first()
    .click();
  await page
    .locator('.mxPopupMenu td')
    .filter({ hasText: itemLabel })
    .first()
    .click();
  await page.waitForTimeout(750);
}

function dialog(page) {
  return page.locator('.geDialog').last();
}

test('builder loads with no JS errors', async ({ page }) => {
  const issues = [];
  await openBuilder(page, issues);

  // The editor canvas and the node palette both have to come up. The format
  // panel also carries .geSidebarContainer, so exclude it.
  await expect(page.locator('.geDiagramContainer')).toBeVisible();
  await expect(
    page.locator('.geSidebarContainer:not(.geFormatContainer)'),
  ).toBeVisible();

  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

test('About dialog has a Close button and no corner X', async ({ page }) => {
  const issues = [];
  await openBuilder(page, issues);
  await menu(page, 'Help', 'About');

  const about = dialog(page);
  await expect(about).toBeVisible();
  await expect(about).toContainText('mxGraph 4.2.2');

  // AboutDialog supplies its own Close button, so it passes hideCloseImage
  // and mxGraph renders no .geDialogClose image.
  await expect(page.locator('.geDialogClose')).toHaveCount(0);

  await about.getByText('Close', { exact: true }).click();
  await expect(page.locator('.geDialog')).toHaveCount(0);
});

test('About dialog still dismisses on backdrop click', async ({ page }) => {
  const issues = [];
  await openBuilder(page, issues);
  await menu(page, 'Help', 'About');
  await expect(dialog(page)).toBeVisible();

  // The dialog stays closable so the backdrop handler is registered; only the
  // corner image is suppressed. Click a corner the dialog does not cover.
  await page
    .locator('div.background')
    .last()
    .click({ position: { x: 5, y: 5 } });

  await expect(page.locator('.geDialog')).toHaveCount(0);
});

test('Experiment Variables ships the four defaults', async ({ page }) => {
  const issues = [];
  await openBuilder(page, issues);
  await menu(page, 'File', 'Edit Variables');

  const vars = dialog(page);
  await expect(vars).toBeVisible();

  // Defaults come from window.experiment_vars in js/Dialogs.js. JSONEditor
  // assigns each input's value as a DOM property, so input[value=...] never
  // matches, and it renders collapsed rows whose inputs are not visible, so
  // waiting on visibility does not work either. Poll the live values until the
  // editor has rendered them.
  await expect
    .poll(
      async () =>
        vars.locator('input').evaluateAll((els) => els.map((el) => el.value)),
      { timeout: 15000 },
    )
    .toEqual(
      expect.arrayContaining([
        'DEFAULT_MEMORY',
        'DEFAULT_VCPU',
        'DEFAULT_VM_IMAGE',
        'DEFAULT_ROUTER_IMAGE',
      ]),
    );

  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

// Every dialog's buttons must render inside the dialog box. Regression guard
// for the cut-off / squished dialog reports.
const dialogsUnderTest = [
  { name: 'About', menu: 'Help', item: 'About' },
  { name: 'Edit Variables', menu: 'File', item: 'Edit Variables' },
  { name: 'Save to phenix', menu: 'File', item: 'Save to ph' },
];

for (const spec of dialogsUnderTest) {
  test(`${spec.name}: buttons stay inside the dialog`, async ({ page }) => {
    const issues = [];
    await openBuilder(page, issues);
    await menu(page, spec.menu, spec.item);

    const dlg = dialog(page);
    await expect(dlg).toBeVisible();
    // "Save to phenix" builds JSON behind a spinner before filling in.
    await page.waitForTimeout(1000);

    const box = await dlg.boundingBox();
    expect(box, 'dialog should have a bounding box').not.toBeNull();

    const buttons = dlg.locator('.geBtn');
    const count = await buttons.count();
    expect(count, 'dialog should have at least one button').toBeGreaterThan(0);

    for (let i = 0; i < count; i++) {
      const btn = buttons.nth(i);
      if (!(await btn.isVisible())) continue;

      const b = await btn.boundingBox();
      if (b === null) continue;

      const label = (await btn.innerText()).trim() || `button ${i}`;
      // 1px of slack for sub-pixel rounding.
      expect(
        b.y + b.height,
        `${spec.name}: "${label}" bottom must stay inside the dialog`,
      ).toBeLessThanOrEqual(box.y + box.height + 1);
      expect(
        b.y,
        `${spec.name}: "${label}" top must stay inside the dialog`,
      ).toBeGreaterThanOrEqual(box.y - 1);
    }
  });
}

// The format panel clips horizontally (overflow-x: hidden), so a control wider
// than the panel is silently cut off rather than pushing the layout.
test('format panel controls stay inside the panel', async ({ page }) => {
  const issues = [];
  await openBuilder(page, issues);

  const panel = page.locator('.geFormatContainer');
  await expect(panel).toBeVisible();

  const panelBox = await panel.boundingBox();
  expect(panelBox, 'format panel should have a bounding box').not.toBeNull();

  const controls = panel.locator('.geBtn, select, input[type="text"]');
  const count = await controls.count();
  expect(count, 'format panel should have controls').toBeGreaterThan(0);

  for (let i = 0; i < count; i++) {
    const ctl = controls.nth(i);
    if (!(await ctl.isVisible())) continue;

    const b = await ctl.boundingBox();
    if (b === null) continue;

    const label = (await ctl.getAttribute('title')) || `control ${i}`;
    expect(
      b.x + b.width,
      `"${label}" right edge must stay inside the format panel`,
    ).toBeLessThanOrEqual(panelBox.x + panelBox.width + 1);
  }
});

// Format.js gives panel rows a fixed height with overflow:hidden, so content
// taller than the row is silently clipped rather than pushing the layout. This
// caught the Diagram tab and its option rows being cut off under bootstrap's
// larger line height.
test('format panel clips none of its content', async ({ page }) => {
  const issues = [];
  await openBuilder(page, issues);
  await expect(page.locator('.geFormatContainer')).toBeVisible();

  const clipped = await page.evaluate(() => {
    const panel = document.querySelector('.geFormatContainer');
    const bad = [];

    for (const el of panel.querySelectorAll('*')) {
      if (getComputedStyle(el).overflow === 'visible') continue;
      // 1px of slack for sub-pixel rounding.
      if (el.scrollHeight > el.clientHeight + 1) {
        bad.push({
          text: el.textContent.trim().slice(0, 30),
          clientH: el.clientHeight,
          scrollH: el.scrollHeight,
        });
      }
    }

    return bad;
  });

  expect(clipped, JSON.stringify(clipped, null, 1)).toHaveLength(0);
});

// A dropped closing brace in grapheditor.css silently disabled every rule after
// it, including the box-sizing overrides the panels depend on. The tooltip rule
// is the last phenix override in the file, so it proves the stylesheet parsed
// all the way through.
test('phenix stylesheet overrides are live', async ({ page }) => {
  const issues = [];
  await openBuilder(page, issues);

  const fontFamily = await page.evaluate(() => {
    const probe = document.createElement('div');
    probe.className = 'mxTooltip';
    document.body.appendChild(probe);
    const value = getComputedStyle(probe).fontFamily;
    probe.remove();

    return value;
  });

  expect(fontFamily).toContain('monospace');
});

// The node palettes are the main content of the sidebar, so they open by
// default. addPalette hides a collapsed palette with display:none.
test('node palettes are expanded by default', async ({ page }) => {
  const issues = [];
  await openBuilder(page, issues);

  const palette = page.locator('.geSidebarContainer:not(.geFormatContainer)');
  await expect(palette).toBeVisible();

  // Every stencil image lives in a .geSidebar body; at least the node palettes
  // must be showing their entries without a click.
  const shown = palette.locator('.geSidebar:visible .geItem');
  await expect(shown.first()).toBeVisible();

  const count = await shown.count();
  expect(
    count,
    'node palettes should show entries without a click',
  ).toBeGreaterThan(4);
});
