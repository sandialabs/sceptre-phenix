// Builder page layout: the editor fills the viewport under the header
// without page scrolling, keeps a usable canvas in short windows, with
// enlarged text and however its side columns are resized, text stays inside
// its boxes, and palette entries show their descriptions as tooltips.
// builder-a11y.spec.js checks the canvas controls' and form fields'
// contrast in both themes. Layout depends on the browser engine, so every
// test here also runs in Firefox.

const {
  test,
  contrast,
  expect,
  expectNoFatal,
  visit,
} = require('./builder-support');

const VIEWPORTS = [
  { width: 2560, height: 1440 },
  { width: 1920, height: 1080 },
  { width: 1440, height: 900 },
  { width: 1280, height: 720 },
  { width: 1100, height: 700 },
];

// AppFooter renders a plain <div>, so find it by its text.
const FOOTER = 'Sandia National Laboratories';

// Returns every visible text element inside `selector` whose text pokes
// outside the content box (inside border and padding) of the element that
// frames it: the nearest ancestor with a border. Using the content box also
// catches text that crosses a rounded border, as on switch nodes.
async function escapedText(page, selector) {
  return page.evaluate((scope) => {
    const framed = (element) => {
      for (let node = element.parentElement; node; node = node.parentElement) {
        const style = getComputedStyle(node);
        if (
          parseFloat(style.borderLeftWidth) > 0 &&
          parseFloat(style.borderRightWidth) > 0
        ) {
          return node;
        }
      }

      return null;
    };

    const found = [];
    for (const root of document.querySelectorAll(scope)) {
      for (const element of root.querySelectorAll('*')) {
        const ownText = [...element.childNodes].some(
          (node) => node.nodeType === Node.TEXT_NODE && node.textContent.trim(),
        );
        if (!ownText || !element.checkVisibility?.()) {
          continue;
        }

        const frame = framed(element);
        if (!frame || !root.contains(frame)) {
          continue;
        }

        const range = document.createRange();
        range.selectNodeContents(element);
        const text = range.getBoundingClientRect();
        const outer = frame.getBoundingClientRect();
        const frameStyle = getComputedStyle(frame);
        const inset = (side) =>
          parseFloat(frameStyle[`border${side}Width`]) +
          parseFloat(frameStyle[`padding${side}`]);
        const box = {
          left: outer.left + inset('Left'),
          right: outer.right - inset('Right'),
          top: outer.top + inset('Top'),
          bottom: outer.bottom - inset('Bottom'),
        };
        // Text an element clips (ellipsis, clamped notes) is only visible
        // inside the element's own box.
        const shown =
          getComputedStyle(element).overflow === 'hidden'
            ? element.getBoundingClientRect()
            : text;

        if (
          shown.left < box.left - 1 ||
          shown.right > box.right + 1 ||
          shown.top < box.top - 1 ||
          shown.bottom > box.bottom + 1
        ) {
          found.push({
            text: element.textContent.trim().slice(0, 40),
            text_box: [shown.left, shown.top, shown.right, shown.bottom].map(
              Math.round,
            ),
            frame: [box.left, box.top, box.right, box.bottom].map(Math.round),
          });
        }
      }
    }

    return found;
  }, selector);
}

// Positions of the editor's main boxes and the page's scroll extent.
async function editorLayout(page) {
  return page.evaluate(() => {
    const rect = (selector) => {
      const box = document.querySelector(selector).getBoundingClientRect();

      return {
        left: box.left,
        top: box.top,
        right: box.right,
        bottom: box.bottom,
      };
    };
    const root = document.querySelector('.builder-root');

    return {
      scrollWidth: document.scrollingElement.scrollWidth,
      scrollHeight: document.scrollingElement.scrollHeight,
      width: window.innerWidth,
      height: window.innerHeight,
      header: rect('.navbar'),
      root: rect('.builder-root'),
      rootScroll: {
        height: root.scrollHeight - root.clientHeight,
        width: root.scrollWidth - root.clientWidth,
      },
      layout: rect('.builder-layout'),
      canvas: rect('[data-testid="builder-canvas"]'),
      back: rect('[data-testid="editor-back"]'),
      name: rect('.builder-header__name'),
      counts: rect('[data-testid="builder-summary"]'),
      start: rect('.builder-header__start'),
      editorHeader: rect('.builder-header'),
      headerGap: parseFloat(
        getComputedStyle(document.querySelector('.builder-header')).columnGap,
      ),
      actions: rect('.builder-header__actions'),
      actionTops: [
        ...document.querySelector('.builder-header__actions').children,
      ].map((element) => Math.round(element.getBoundingClientRect().top)),
      // The header buttons that show their labels, by test id, and whether
      // Commands shows its keys.
      labelled: [
        ...document.querySelectorAll('.builder-header .builder-header__label'),
      ]
        .filter((label) => getComputedStyle(label).position !== 'absolute')
        .map((label) => label.closest('[data-testid]').dataset.testid),
      keycaps: [
        ...document.querySelectorAll('[data-testid="editor-commands"] kbd'),
      ].some((kbd) => kbd.checkVisibility()),
      // The key on the Shortcuts button, which shows at every width.
      shortcutsKey: [
        ...document.querySelectorAll('[data-testid="editor-shortcuts"] kbd'),
      ]
        .filter((kbd) => kbd.checkVisibility())
        .map((kbd) => kbd.textContent),
      // The right edge of the last header link.
      navRight: Math.max(
        ...[...document.querySelectorAll('.navbar .navbar-item')]
          .filter((item) => item.offsetParent)
          .map((item) => item.getBoundingClientRect().right),
      ),
    };
  });
}

// The toolbar's right edge, its buttons past the window's, and where the
// save state is beside Draft History and whether its text is cut off.
async function toolbarLayout(page) {
  return page.evaluate(() => {
    const toolbar = document.querySelector('.builder-toolbar');
    const save = document.querySelector('[data-testid="builder-save-state"]');
    const history = document
      .querySelector('[data-testid="toolbar-history"]')
      .getBoundingClientRect();
    const box = save.getBoundingClientRect();

    return {
      right: toolbar.getBoundingClientRect().right,
      offScreen: [...toolbar.querySelectorAll('button')]
        .filter((button) => button.getBoundingClientRect().right > innerWidth)
        .map((button) => button.dataset.testid),
      save: {
        left: box.left,
        right: box.right,
        // Beside Draft History, on its row.
        afterHistory: box.left - history.right,
        rowOffset: Math.abs(
          (box.top + box.bottom) / 2 - (history.top + history.bottom) / 2,
        ),
        cut:
          save.scrollWidth > save.clientWidth + 1 ||
          save.scrollHeight > save.clientHeight + 1,
      },
    };
  });
}

// Resizes the window and waits two frames, so resize observers and media
// query listeners have run before the layout is measured.
async function resize(page, viewport) {
  await page.setViewportSize(viewport);
  await page.evaluate(
    () =>
      new Promise((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(resolve));
      }),
  );
}

// Outline rows are one line: the kind badge sits inside the row.
async function expectBadgesInRows(page, what) {
  const rows = page.locator('[data-testid^="outline-item-"]');
  for (let index = 0; index < (await rows.count()); index += 1) {
    const row = rows.nth(index);
    const rowBox = await row.boundingBox();
    const kindBox = await row.locator('.builder-outline__kind').boundingBox();
    if (!rowBox || !kindBox) {
      expect
        .soft(kindBox, `${what} ${index} kind badge is drawn`)
        .not.toBeNull();
      continue;
    }

    expect
      .soft(kindBox.x + kindBox.width, `${what} ${index} kind badge`)
      .toBeLessThanOrEqual(rowBox.x + rowBox.width + 1);
  }
}

async function headerHeight(page) {
  return (await page.locator('.navbar').boundingBox()).height;
}

test(
  'the editor fills the window at every size, keeps text in its boxes, and leaving restores the page',
  {
    tag: '@cross-browser',
  },
  async ({ page, builder, issues }) => {
    const initial = page.viewportSize();
    let draft;

    await visit(page, '/experiments');
    await expect(page.locator('.navbar')).toBeVisible();
    await expect.soft(page.getByText(FOOTER)).toBeVisible();
    const header = await headerHeight(page);

    // On every page, header links that do not fit wrap rather than push the
    // last ones off the page (1024 wide: 1280 at 125% zoom).
    await resize(page, { width: 1024, height: 720 });
    await expect.soft(page.getByTestId('nav-builder')).toBeVisible();
    expect
      .soft(
        await page.evaluate(() => document.documentElement.scrollWidth),
        '/experiments at 1024: page scroll width',
      )
      .toBeLessThanOrEqual(1024);
    await resize(page, initial);
    // A focused header link is as legible as a hovered one.
    await page.getByTestId('nav-builder').focus();
    const [{ ratio: focusedLink }] = await contrast(
      page.getByTestId('nav-builder'),
    );
    expect
      .soft(focusedLink, 'focused header link text')
      .toBeGreaterThanOrEqual(4.5);

    await test.step('drafts landing is full width, keeps a gutter and hides the footer', async () => {
      await builder.open();
      await expect.soft(page.getByText(FOOTER)).toHaveCount(0);
      expect.soft(await headerHeight(page)).toBe(header);

      const landing = await page.evaluate(() => ({
        root: document.querySelector('.builder-root').getBoundingClientRect(),
        heading: document
          .querySelector('.builder-root h1')
          .getBoundingClientRect(),
        width: window.innerWidth,
      }));
      expect.soft(landing.root.left).toBe(0);
      expect.soft(Math.round(landing.root.right)).toBe(landing.width);
      expect.soft(landing.heading.left).toBeGreaterThanOrEqual(12);

      // In a 1440px window, Commands keeps its label, and the theme,
      // Settings and Help show their icons alone; in a 1600px window all
      // show their labels. (The editor's header, which also has its counts
      // to keep centered, can show fewer.)
      const labelled = () =>
        page.$$eval(
          '.builder-drafts__header .builder-header__label',
          (labels) =>
            labels
              .filter(
                (label) => getComputedStyle(label).position !== 'absolute',
              )
              .map((label) => label.closest('[data-testid]').dataset.testid),
        );
      await resize(page, { width: 1440, height: 900 });
      expect.soft(await labelled()).toEqual(['drafts-commands']);
      await resize(page, { width: 1600, height: 900 });
      expect
        .soft(await labelled())
        .toEqual([
          'drafts-commands',
          'drafts-theme',
          'drafts-settings',
          'drafts-help',
        ]);
    });

    await test.step('build a sample diagram', async () => {
      draft = await builder.createBlank();
      expect.soft(await headerHeight(page)).toBe(header);
      for (const item of [
        'device',
        'template-workstation',
        'template-external',
        'switch',
        'note',
      ]) {
        await builder.palette(item).click();
      }
      await builder.connect();
      await expect.soft(builder.summary).toContainText('1 connection');
      await builder.waitSaved();
      await expect.soft(page.getByText(FOOTER)).toHaveCount(0);
    });

    for (const viewport of VIEWPORTS) {
      const size = `${viewport.width}x${viewport.height}`;

      await test.step(`fills a ${size} window without page scrolling`, async () => {
        await resize(page, viewport);
        const layout = await editorLayout(page);
        const at = (what) => `${size}: ${what}`;

        // No page scrollbars in either direction, and nothing hidden inside the
        // Builder root either: its content fits instead of being clipped.
        expect
          .soft(layout.scrollHeight, at('page scroll height'))
          .toBeLessThanOrEqual(layout.height);
        expect
          .soft(layout.scrollWidth, at('page scroll width'))
          .toBeLessThanOrEqual(layout.width);
        expect
          .soft(layout.rootScroll, at('Builder root overflow'))
          .toEqual({ height: 0, width: 0 });
        expect
          .soft(layout.layout.bottom, at('layout bottom'))
          .toBeLessThanOrEqual(layout.root.bottom);

        // Full width, directly under the header, down to the bottom edge.
        expect.soft(layout.root.left, at('root left')).toBe(0);
        expect
          .soft(Math.round(layout.root.right), at('root right'))
          .toBe(layout.width);
        expect
          .soft(Math.round(layout.root.top), at('root top'))
          .toBe(Math.round(layout.header.bottom));
        expect
          .soft(Math.round(layout.root.bottom), at('root bottom'))
          .toBe(layout.height);

        // The whole canvas is on screen and takes most of the height.
        expect
          .soft(layout.canvas.bottom, at('canvas bottom'))
          .toBeLessThanOrEqual(layout.height);
        expect
          .soft(layout.canvas.bottom - layout.canvas.top, at('canvas height'))
          .toBeGreaterThan((layout.height - layout.header.bottom) * 0.5);

        // Controls keep a gutter from the window edge.
        expect
          .soft(layout.back.left, at('Back to drafts gutter'))
          .toBeGreaterThanOrEqual(12);

        // Back to drafts, the name and its pencil (at most 13rem wide) and
        // the counts, in that order; from 1280px wide the actions share
        // their row, so the header is one control high.
        expect
          .soft(layout.name.left, at('name after Back to drafts'))
          .toBeGreaterThan(layout.back.right);
        expect
          .soft(layout.counts.left, at('counts after the name'))
          .toBeGreaterThan(layout.name.right);
        expect
          .soft(layout.name.right - layout.name.left, at('name width'))
          .toBeLessThanOrEqual(13 * 16 + 1);
        if (viewport.width >= 1280) {
          expect
            .soft(
              layout.editorHeader.bottom - layout.editorHeader.top,
              at('header height'),
            )
            .toBeLessThan(48);
        }

        // The counts are centered on the header, or as near as the start
        // (Back to drafts and the name) and the actions let them be, never
        // over either. The buttons' labels never move them: from 1280px
        // they are centered.
        const width = layout.counts.right - layout.counts.left;
        const centered =
          (layout.editorHeader.left + layout.editorHeader.right - width) / 2;
        const nearest = Math.min(
          Math.max(centered, layout.start.right + layout.headerGap),
          layout.actions.left - layout.headerGap - width,
        );
        expect
          .soft(
            Math.abs(layout.counts.left - nearest),
            at('counts centered, or as near as they fit'),
          )
          .toBeLessThanOrEqual(1);
        if (viewport.width >= 1280) {
          expect
            .soft(
              Math.abs(layout.counts.left - centered),
              at('counts centered'),
            )
            .toBeLessThanOrEqual(1);
        }
        if (Math.abs(layout.counts.left - centered) > 1) {
          expect
            .soft(layout.labelled, at('labels while the counts are off center'))
            .toEqual([]);
        }

        // The labels go in two steps, and only as far as they leave the
        // counts centered: in a wide header every button has its label, in
        // a narrower one Reset view and Commands (without its keys) keep
        // theirs, and in a narrow one none does.
        const labelled = {
          2560: [
            'editor-reset-view',
            'editor-commands',
            'editor-theme',
            'editor-shortcuts',
            'editor-settings',
            'editor-help',
          ],
          1920: ['editor-reset-view', 'editor-commands'],
        };
        expect
          .soft(layout.labelled, at('buttons with their labels'))
          .toEqual(labelled[viewport.width] || []);
        expect
          .soft(layout.keycaps, at('Commands keys'))
          .toBe(viewport.width === 2560);
        // Shortcuts keeps its key beside its icon, with or without its
        // label.
        expect.soft(layout.shortcutsKey, at('Shortcuts key')).toEqual(['?']);

        // The save state is in the toolbar, just after Draft History on its
        // row, whole, and pushes no button off screen.
        const toolbar = await toolbarLayout(page);
        expect
          .soft(toolbar.offScreen, at('toolbar buttons off screen'))
          .toEqual([]);
        expect.soft(toolbar.save.cut, at('save state cut off')).toBe(false);
        expect
          .soft(toolbar.save.afterHistory, at('save state after history'))
          .toBeGreaterThanOrEqual(0);
        expect
          .soft(toolbar.save.afterHistory, at('save state by history'))
          .toBeLessThan(16);
        expect
          .soft(toolbar.save.rowOffset, at('save state on history’s row'))
          .toBeLessThan(4);

        // The checks and the buttons after them wrap as one group, which
        // stays at the right edge.
        expect
          .soft(new Set(layout.actionTops).size, at('header actions in a row'))
          .toBe(1);
        expect
          .soft(layout.actions.right, at('header actions right'))
          .toBeCloseTo(layout.editorHeader.right, 0);

        // The page cannot scroll sideways, so no header link is cut off.
        expect
          .soft(layout.navRight, at('header links'))
          .toBeLessThanOrEqual(layout.width + 1);
      });
    }

    await test.step('splitters resize the side columns, keep a usable canvas and are remembered', async () => {
      await resize(page, { width: 1440, height: 900 });
      const start = page.getByRole('separator', {
        name: 'Resize Add nodes and Outline',
      });
      const end = page.getByRole('separator', { name: 'Resize Inspector' });
      const widthOf = (selector) =>
        page
          .locator(selector)
          .evaluate((element) =>
            Math.round(element.getBoundingClientRect().width),
          );
      const valueOf = async (splitter) =>
        Number(await splitter.getAttribute('aria-valuenow'));

      // WAI-ARIA APG window splitters, each reporting the width of the
      // column it resizes.
      for (const [splitter, pane] of [
        [start, 'builder-pane-start'],
        [end, 'builder-pane-end'],
      ]) {
        await expect
          .soft(splitter)
          .toHaveAttribute('aria-orientation', 'vertical');
        await expect.soft(splitter).toHaveAttribute('aria-controls', pane);
        await expect
          .soft(splitter)
          .toHaveAccessibleDescription(/^Left and Right arrows resize/);
        expect
          .soft(await valueOf(splitter), pane)
          .toBe(await widthOf(`#${pane}`));
      }

      // The arrow keys move the splitter, so Left Arrow widens the
      // Inspector; End widens it as far as the canvas allows, and Enter
      // restores its default width.
      const inspector = await valueOf(end);
      await end.focus();
      await page.keyboard.press('ArrowLeft');
      await expect
        .soft(end)
        .toHaveAttribute('aria-valuenow', String(inspector + 16));
      await page.keyboard.press('End');
      await expect
        .soft(end)
        .toHaveAttribute(
          'aria-valuenow',
          (await end.getAttribute('aria-valuemax')) || '',
        );
      expect
        .soft(await widthOf('[data-testid="builder-canvas"]'), 'canvas')
        .toBeGreaterThanOrEqual(319);
      await page.keyboard.press('Enter');
      await expect
        .soft(end)
        .toHaveAttribute('aria-valuenow', String(inspector));

      // A drag resizes Add nodes and the Outline, and the width is kept for
      // the next visit; a double-click restores the default. The splitter
      // is drawn as wide as the gap, but takes hold of the pointer 24px
      // wide, over the canvas's edge (WCAG 2.5.8), so the drag starts there.
      const palette = await valueOf(start);
      const box = await start.boundingBox();
      expect.soft(Math.round(box.width), 'drawn splitter width').toBe(12);
      const x = box.x + box.width + 11;
      const y = box.y + box.height / 2;
      await page.mouse.move(x, y);
      await page.mouse.down();
      await page.mouse.move(x + 80, y, { steps: 5 });
      await page.mouse.up();
      await expect
        .soft(start)
        .toHaveAttribute('aria-valuenow', String(palette + 80));
      await builder.openDraft(draft);
      await expect
        .soft(start)
        .toHaveAttribute('aria-valuenow', String(palette + 80));
      await start.dblclick();
      await expect
        .soft(start)
        .toHaveAttribute('aria-valuenow', String(palette));
    });

    await test.step('a Widen toggle at each splitter widens its column with a single click, and restores it', async () => {
      // WCAG 2.5.7: a pointer need not drag to resize a column.
      const toggles = {
        start: page.getByRole('button', {
          name: 'Widen Add nodes and Outline',
        }),
        end: page.getByRole('button', { name: 'Widen Inspector' }),
      };
      const splitters = {
        start: page.getByTestId('splitter-start'),
        end: page.getByTestId('splitter-end'),
      };
      const valueOf = async (side) =>
        Number(await splitters[side].getAttribute('aria-valuenow'));
      const widths = {
        start: await valueOf('start'),
        end: await valueOf('end'),
      };

      for (const side of ['start', 'end']) {
        await expect
          .soft(toggles[side])
          .toHaveAttribute('aria-controls', `builder-pane-${side}`);
        await expect
          .soft(toggles[side])
          .toHaveAttribute('aria-pressed', 'false');
        const box = await toggles[side].boundingBox();
        expect
          .soft(Math.min(box.width, box.height), `${side} toggle size`)
          .toBeGreaterThanOrEqual(24);
      }

      // Named by an icon only, so the name is its tooltip too.
      await toggles.end.hover();
      await expect
        .soft(page.getByTestId('pane-toggle-tooltip-end'))
        .toHaveText('Widen Inspector');

      // Pressed, it widens the Inspector as far as the canvas allows. The
      // toggle moves with the splitter, and its tooltip does not stay behind.
      await toggles.end.click();
      await expect.soft(toggles.end).toHaveAttribute('aria-pressed', 'true');
      await expect
        .soft(splitters.end)
        .toHaveAttribute(
          'aria-valuenow',
          (await splitters.end.getAttribute('aria-valuemax')) || '',
        );
      await expect
        .soft(page.getByTestId('pane-toggle-tooltip-end'))
        .toHaveCount(0);
      expect
        .soft(
          await page
            .getByTestId('builder-canvas')
            .evaluate((element) => element.getBoundingClientRect().width),
          'canvas',
        )
        .toBeGreaterThanOrEqual(319);

      // One column is widened at a time: widening the other restores the
      // Inspector first. Both states are kept for the next visit.
      await toggles.start.click();
      await expect.soft(toggles.start).toHaveAttribute('aria-pressed', 'true');
      await expect.soft(toggles.end).toHaveAttribute('aria-pressed', 'false');
      await expect
        .soft(splitters.end)
        .toHaveAttribute('aria-valuenow', String(widths.end));
      await builder.openDraft(draft);
      await expect.soft(toggles.start).toHaveAttribute('aria-pressed', 'true');
      await expect
        .soft(splitters.start)
        .toHaveAttribute(
          'aria-valuenow',
          (await splitters.start.getAttribute('aria-valuemax')) || '',
        );

      // Pressed again, it restores the width the column had.
      await toggles.start.click();
      await expect.soft(toggles.start).toHaveAttribute('aria-pressed', 'false');
      await expect
        .soft(splitters.start)
        .toHaveAttribute('aria-valuenow', String(widths.start));

      // The Inspector's splitter also takes hold of the pointer over the
      // canvas's edge.
      const box = await splitters.end.boundingBox();
      const x = box.x - 11;
      const y = box.y + box.height / 2;
      await page.mouse.move(x, y);
      await page.mouse.down();
      await page.mouse.move(x - 40, y, { steps: 4 });
      await page.mouse.up();
      await expect
        .soft(splitters.end)
        .toHaveAttribute('aria-valuenow', String(widths.end + 40));
      await splitters.end.dblclick();
      await expect
        .soft(splitters.end)
        .toHaveAttribute('aria-valuenow', String(widths.end));
    });

    await test.step('a Hide toggle folds each side column into a strip that shows it again, and a node pressed on the canvas shows the Inspector', async () => {
      const hide = {
        start: page.getByTestId('pane-hide-start'),
        end: page.getByTestId('pane-hide-end'),
      };
      const columns = {
        start: page.locator('#builder-pane-start'),
        end: page.locator('#builder-pane-end'),
      };
      const canvasWidth = () =>
        builder.canvas.evaluate((element) =>
          Math.round(element.getBoundingClientRect().width),
        );
      const wide = await canvasWidth();
      // The width of the first column, which it has again when it is shown.
      const shown = await page
        .getByTestId('splitter-start')
        .getAttribute('aria-valuenow');

      // Directly under each Widen toggle, at least 24px square, and named
      // for what a press does to the column it controls.
      for (const side of ['start', 'end']) {
        const widen = await page
          .getByTestId(`pane-toggle-${side}`)
          .boundingBox();
        const box = await hide[side].boundingBox();
        // Rounded to 0.01px: Firefox 155 measures this 24px button as
        // 23.99998px.
        expect
          .soft(
            Math.round(Math.min(box.width, box.height) * 100) / 100,
            `${side} Hide size`,
          )
          .toBeGreaterThanOrEqual(24);
        expect
          .soft(box.y, `${side} Hide under Widen`)
          .toBeGreaterThanOrEqual(widen.y + widen.height);
        expect
          .soft(Math.round(box.x), `${side} Hide x`)
          .toBe(Math.round(widen.x));
        await expect
          .soft(hide[side])
          .toHaveAttribute('aria-controls', `builder-pane-${side}`);
        await expect.soft(hide[side]).toHaveAttribute('aria-expanded', 'true');
      }
      await expect.soft(hide.end).toHaveAccessibleName('Hide Inspector');
      await hide.end.hover();
      await expect
        .soft(page.getByTestId('pane-toggle-tooltip-end'))
        .toHaveText('Hide Inspector');

      // By keyboard: focus stays on the toggle, which shows the column again
      // from its strip, and the canvas takes the room. The column's Widen
      // toggle and splitter go with it.
      await hide.end.focus();
      await page.keyboard.press('Enter');
      await expect.soft(hide.end).toHaveAttribute('aria-expanded', 'false');
      await expect.soft(hide.end).toHaveAccessibleName('Show Inspector');
      await expect.soft(hide.end).toBeFocused();
      await expect.soft(columns.end).toBeHidden();
      await expect.soft(builder).toHaveAnnounced('Inspector hidden');
      await expect.soft(page.getByTestId('pane-toggle-end')).toHaveCount(0);
      await expect.soft(page.getByTestId('splitter-end')).toHaveCount(0);
      await expect.poll(canvasWidth).toBeGreaterThan(wide + 200);

      // By pointer, the other column. Both stay hidden on the next visit.
      await hide.start.click();
      await expect
        .soft(hide.start)
        .toHaveAccessibleName('Show Add nodes and Outline');
      await expect.soft(columns.start).toBeHidden();
      await builder.openDraft(draft);
      await expect.soft(columns.start).toBeHidden();
      await expect.soft(columns.end).toBeHidden();

      // Moving focus over the nodes leaves the Inspector hidden; Enter on
      // a node shows it with that node, and focus stays on the node.
      const subject = builder.inspector.locator('.builder-inspector__subject');
      await builder.canvas.focus();
      await page.keyboard.press('Escape');
      await page.keyboard.press('ArrowRight');
      const focused = page.locator('.vue-flow__node:focus');
      await expect(focused).toHaveCount(1);
      await expect.soft(columns.end).toBeHidden();
      const node = page.locator(
        `.vue-flow__node[data-id="${await focused.getAttribute('data-id')}"]`,
      );
      // 'Device external, 0 connections, …', as the Inspector names it; a
      // switch it names by its network.
      const [label] = (await node.getAttribute('aria-label'))
        .replace(/^Switch /, 'Network ')
        .split(',');
      await page.keyboard.press('Enter');
      await expect.soft(columns.end).toBeVisible();
      await expect.soft(hide.end).toHaveAttribute('aria-expanded', 'true');
      await expect.soft(builder).toHaveAnnounced('Inspector shown');
      await expect.soft(node).toBeFocused();
      await expect.soft(subject).toContainText(label);

      // A click on the one selected node, with the Inspector hidden, shows
      // it again rather than deselecting the node.
      await hide.end.click();
      await expect.soft(columns.end).toBeHidden();
      await node.click();
      await expect.soft(columns.end).toBeVisible();
      await expect.soft(node).toHaveClass(/\bselected\b/);
      await expect.soft(subject).toContainText(label);

      // The command palette shows the other column; its Widen toggle and
      // splitter work again.
      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+k');
      await page.keyboard.type('Show Add nodes');
      await page.keyboard.press('Enter');
      await expect.soft(columns.start).toBeVisible();
      await expect.soft(hide.start).toHaveAttribute('aria-expanded', 'true');
      const splitter = page.getByTestId('splitter-start');
      // The splitter reports the column's width as soon as the column
      // shows: not its narrowest width until the layout is next measured,
      // which an arrow key pressed meanwhile would resize it from.
      expect
        .soft(
          await splitter.getAttribute('aria-valuenow'),
          'the width the splitter reports once its column shows',
        )
        .toBe(shown);
      const width = Number(shown);
      await splitter.focus();
      await page.keyboard.press('ArrowRight');
      await expect
        .soft(splitter)
        .toHaveAttribute('aria-valuenow', String(width + 16));
      await page.keyboard.press('Enter');
      await page.getByTestId('pane-toggle-start').click();
      await expect
        .soft(splitter)
        .toHaveAttribute(
          'aria-valuenow',
          (await splitter.getAttribute('aria-valuemax')) || '',
        );
      await page.getByTestId('pane-toggle-start').click();
      await expect
        .soft(splitter)
        .toHaveAttribute('aria-valuenow', String(width));

      // That holds before the browser draws another frame. Once the layout
      // has been measured with the column hidden, the column is shown and
      // its splitter read in one task, which leaves the browser no frame in
      // between.
      await hide.start.click();
      await expect.soft(columns.start).toBeHidden();
      const reported = await page.evaluate(async () => {
        const find = (id) => document.querySelector(`[data-testid="${id}"]`);

        await new Promise((resolve) => {
          requestAnimationFrame(() => requestAnimationFrame(resolve));
        });
        find('pane-hide-start').click();

        // The editor draws the column in a microtask of this task.
        for (let turn = 0; turn < 100 && !find('splitter-start'); turn += 1) {
          await Promise.resolve();
        }

        return find('splitter-start')?.getAttribute('aria-valuenow');
      });
      expect
        .soft(reported, 'the width the splitter reports in the same task')
        .toBe(shown);
      await expect.soft(columns.start).toBeVisible();
      expect.soft(await canvasWidth(), 'canvas width again').toBe(wide);
    });

    await test.step('the minimap resizes from its top left corner, by keyboard or drag, and is remembered', async () => {
      const handle = page.getByRole('separator', { name: 'Resize minimap' });
      const minimap = page.locator('.vue-flow__minimap');
      const value = async () =>
        Number(await handle.getAttribute('aria-valuenow'));
      const stored = () =>
        page.evaluate(() => localStorage.getItem('phenix.builder.minimap'));

      // A splitter between the minimap and the canvas, over the minimap's
      // top left corner, that says its keys and the size.
      await expect
        .soft(handle)
        .toHaveAttribute('aria-controls', 'builder-minimap');
      await expect
        .soft(handle)
        .toHaveAttribute('aria-valuetext', '200 by 150 pixels');
      await expect
        .soft(handle)
        .toHaveAccessibleDescription(
          /^Up and Left arrows make the minimap larger/,
        );
      const box = await handle.boundingBox();
      const frame = await minimap.boundingBox();
      expect
        .soft(Math.min(box.width, box.height), 'handle size')
        .toBeGreaterThanOrEqual(24);
      expect.soft(Math.round(box.x), 'handle left').toBe(Math.round(frame.x));
      expect.soft(Math.round(box.y), 'handle top').toBe(Math.round(frame.y));
      expect.soft(Math.round(frame.width), 'minimap frame').toBe(202);

      // Up and Left make it larger, Down and Right smaller, Home and End
      // smallest and largest, and Enter the default again. Each size is
      // said.
      await handle.focus();
      await page.keyboard.press('ArrowUp');
      await expect.soft(handle).toHaveAttribute('aria-valuenow', '216');
      await expect.soft(builder).toHaveAnnounced('Minimap 216 by 162 pixels');
      await expect
        .poll(async () => Math.round((await minimap.boundingBox()).height))
        .toBe(164);
      await page.keyboard.press('ArrowLeft');
      await page.keyboard.press('ArrowRight');
      await page.keyboard.press('ArrowDown');
      await expect.soft(handle).toHaveAttribute('aria-valuenow', '200');
      await page.keyboard.press('Home');
      await expect.soft(handle).toHaveAttribute('aria-valuenow', '120');
      await page.keyboard.press('End');
      const largest = Number(await handle.getAttribute('aria-valuemax'));
      await expect
        .soft(handle)
        .toHaveAttribute('aria-valuenow', String(largest));
      await expect.soft(builder).toHaveAnnounced('(largest)');
      // It never covers more than half the canvas either way.
      const canvas = await page.locator('.vue-flow').boundingBox();
      expect
        .soft(largest, 'largest width')
        .toBeLessThanOrEqual(canvas.width / 2);
      expect
        .soft(largest * 0.75, 'largest height')
        .toBeLessThanOrEqual(canvas.height / 2);

      // Fit keeps the nodes clear of the minimap at its size.
      await builder.canvas.focus();
      await page.keyboard.press('Shift+Digit1');
      await expect
        .poll(() =>
          page.locator('.vue-flow').evaluate((flow) => {
            const map = flow
              .querySelector('.vue-flow__minimap')
              .getBoundingClientRect();

            return [...flow.querySelectorAll('.vue-flow__node')].filter(
              (node) => {
                const box = node.getBoundingClientRect();

                return (
                  Math.min(box.right, map.right) -
                    Math.max(box.left, map.left) >
                    1 &&
                  Math.min(box.bottom, map.bottom) -
                    Math.max(box.top, map.top) >
                    1
                );
              },
            ).length;
          }),
        )
        .toBe(0);

      await handle.focus();
      await page.keyboard.press('Enter');
      await expect.soft(handle).toHaveAttribute('aria-valuenow', '200');
      expect.soft(await stored(), 'default size').toBeNull();

      // A drag up and left, along the minimap's diagonal, takes its corner
      // with the pointer. The size is kept for the next visit.
      const start = await handle.boundingBox();
      const x = start.x + start.width / 2;
      const y = start.y + start.height / 2;
      await page.mouse.move(x, y);
      await page.mouse.down();
      await page.mouse.move(x - 40, y - 30, { steps: 5 });
      await page.mouse.up();
      await expect.soft(handle).toHaveAttribute('aria-valuenow', '240');
      await expect.soft(handle).toBeFocused();
      expect.soft(JSON.parse(await stored())).toEqual({ width: 240 });
      await builder.openDraft(draft);
      await expect.soft(handle).toHaveAttribute('aria-valuenow', '240');
      expect.soft(await value()).toBe(240);
      await handle.dblclick();
      await expect.soft(handle).toHaveAttribute('aria-valuenow', '200');

      // A pointer that does not drag picks a size in the command palette
      // (WCAG 2.5.7).
      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+k');
      await page.keyboard.type('Minimap size');
      await page
        .getByRole('option', { name: /^Minimap size: Largest/ })
        .click();
      await expect
        .soft(handle)
        .toHaveAttribute(
          'aria-valuenow',
          (await handle.getAttribute('aria-valuemax')) || '',
        );
      await expect.soft(page.locator('#builder-minimap')).toBeVisible();
      await handle.dblclick();
    });

    await test.step('Reset view puts the view back as the draft opened, in one press', async () => {
      const reset = page.getByTestId('editor-reset-view');
      const splitters = {
        start: page.getByTestId('splitter-start'),
        end: page.getByTestId('splitter-end'),
      };
      // A section of the Inspector that opens: a device's More settings.
      const section = builder.inspector.getByTestId('inspector-section');
      const side = page.locator('#builder-pane-start');
      const minimapHandle = page.getByRole('separator', {
        name: 'Resize minimap',
      });
      const transform = () =>
        page
          .locator('.vue-flow__transformationpane')
          .evaluate((element) => element.style.transform);
      const stored = () =>
        page.evaluate(() => localStorage.getItem('phenix.builder.panes'));

      // A short window, so the side column scrolls. The zoom and pan the
      // draft opens with, once any pan has settled.
      await resize(page, { width: 1440, height: 640 });
      await builder.openDraft(draft);
      const widths = {
        start: await splitters.start.getAttribute('aria-valuenow'),
        end: await splitters.end.getAttribute('aria-valuenow'),
      };
      let last = '';
      await expect
        .poll(async () => {
          const now = await transform();
          const same = now === last;

          last = now;
          return same;
        })
        .toBe(true);
      const opened = last;

      // As an icon, at this width, its tooltip names it too; its label
      // shows in a wider window, and its tooltip then only says what it
      // does.
      await reset.hover();
      await expect
        .soft(page.getByTestId('header-tooltip'))
        .toHaveText(
          'Reset view: reset column widths, zoom, minimap and scrolling',
        );
      await resize(page, { width: 1920, height: 640 });
      await page.mouse.move(0, 0);
      await reset.hover();
      await expect
        .soft(page.getByTestId('header-tooltip'))
        .toHaveText('Reset column widths, zoom, minimap and scrolling');
      await resize(page, { width: 1440, height: 640 });

      await builder.selectInOutline('node');
      await section.locator('summary').click();
      await expect.soft(section).toHaveAttribute('open');
      await page.getByTestId('pane-toggle-start').click();
      await splitters.end.focus();
      await page.keyboard.press('ArrowLeft');
      await page.getByTestId('pane-hide-end').click();
      await builder.canvas.focus();
      await page.keyboard.press('=');
      // Between the widened column and the Inspector the canvas was at its
      // narrowest, where the minimap cannot grow. It can once the canvas has
      // the Inspector's room, which Vue Flow measures a frame later.
      await expect
        .poll(async () =>
          Number(await minimapHandle.getAttribute('aria-valuemax')),
        )
        .toBeGreaterThanOrEqual(216);
      await minimapHandle.focus();
      await page.keyboard.press('ArrowUp');
      await expect.soft(minimapHandle).toHaveAttribute('aria-valuenow', '216');
      await builder.toolbar('minimap').click();
      const scrolled = await side.evaluate((element) => {
        element.scrollTop = 120;
        return element.scrollTop;
      });
      expect.soft(scrolled, 'the side column scrolls').toBeGreaterThan(0);
      expect.soft(await stored()).not.toBeNull();

      await reset.click();
      await expect.soft(reset).toBeFocused();
      await expect
        .soft(builder)
        .toHaveAnnounced('View reset: default column widths');
      for (const key of ['start', 'end']) {
        await expect
          .soft(splitters[key])
          .toHaveAttribute('aria-valuenow', widths[key]);
      }
      await expect
        .soft(page.getByTestId('pane-toggle-start'))
        .toHaveAttribute('aria-pressed', 'false');
      await expect.soft(page.locator('#builder-pane-end')).toBeVisible();
      await expect
        .soft(page.getByTestId('pane-hide-end'))
        .toHaveAttribute('aria-expanded', 'true');
      expect.soft(await stored(), 'stored widths').toBeNull();
      await expect
        .soft(builder.toolbar('minimap'))
        .toHaveAttribute('aria-pressed', 'true');
      await expect.soft(page.locator('.vue-flow__minimap')).toBeVisible();
      await expect.soft(minimapHandle).toHaveAttribute('aria-valuenow', '200');
      expect
        .soft(
          await page.evaluate(() =>
            localStorage.getItem('phenix.builder.minimap'),
          ),
          'stored minimap size',
        )
        .toBeNull();
      await expect.soft(section).toHaveCount(1);
      await expect.soft(section).not.toHaveAttribute('open');
      // Nothing is under the canvas: the keys are in the shortcut sheet.
      await expect.soft(page.getByTestId('canvas-help')).toHaveCount(0);
      expect.soft(await side.evaluate((element) => element.scrollTop)).toBe(0);
      await expect.poll(transform, { message: 'zoom and pan' }).toBe(opened);
    });

    await test.step('a short window or enlarged text keeps a usable canvas, and the editor scrolls', async () => {
      // WCAG 1.4.4 and 1.4.10: a window too short for the editor scrolls it
      // rather than squeezing the canvas away.
      await resize(page, { width: 1280, height: 320 });
      let layout = await editorLayout(page);
      expect
        .soft(
          layout.canvas.bottom - layout.canvas.top,
          '1280x320: canvas height',
        )
        .toBeGreaterThanOrEqual(200);
      expect
        .soft(layout.rootScroll.height, '1280x320: the editor scrolls')
        .toBeGreaterThan(0);
      expect
        .soft(layout.scrollWidth, '1280x320: page scroll width')
        .toBeLessThanOrEqual(layout.width);

      // 1280x1024 at 400%: the canvas is shorter than the default minimap,
      // which takes what fits, so its resize handle stays on the canvas.
      await resize(page, { width: 320, height: 256 });
      const pane = await page.locator('.vue-flow').boundingBox();
      const handle = await page
        .getByRole('separator', { name: 'Resize minimap' })
        .boundingBox();
      expect
        .soft(handle.y - pane.y, '320x256: minimap handle below the top')
        .toBeGreaterThanOrEqual(0);
      expect
        .soft(handle.x - pane.x, '320x256: minimap handle inside the left')
        .toBeGreaterThanOrEqual(0);

      // Text enlarged on its own stacks the columns instead of squeezing
      // the canvas column to nothing.
      await resize(page, { width: 1280, height: 800 });
      await page.getByTestId('pane-hide-end').click();
      const style = await page.addStyleTag({
        content: 'html { font-size: 200% !important; }',
      });
      await resize(page, { width: 1280, height: 800 });
      layout = await editorLayout(page);
      // The stacked columns have no splitters, nor their toggles, and every
      // column shows, the hidden Inspector too.
      await expect
        .soft(page.getByRole('separator', { name: /^Resize (Add|Insp)/ }))
        .toHaveCount(0);
      await expect
        .soft(
          page.getByRole('button', {
            name: /^(Widen|Hide|Show) (Add nodes|Inspector)/,
          }),
        )
        .toHaveCount(0);
      await expect.soft(page.locator('#builder-pane-end')).toBeVisible();
      expect
        .soft(
          layout.canvas.right - layout.canvas.left,
          '200% text: canvas width',
        )
        .toBeGreaterThanOrEqual(640);
      expect
        .soft(
          layout.canvas.bottom - layout.canvas.top,
          '200% text: canvas height',
        )
        .toBeGreaterThanOrEqual(400);
      expect
        .soft(layout.scrollWidth, '200% text: page scroll width')
        .toBeLessThanOrEqual(layout.width);
      expect
        .soft(layout.navRight, '200% text: header links')
        .toBeLessThanOrEqual(layout.width + 1);
      // Focus in the column the stacked layout showed goes to its Show
      // toggle when the column is hidden again, not to the page.
      await page.locator('#builder-pane-end input').first().focus();
      await style.evaluate((element) => element.remove());
      await resize(page, { width: 1280, height: 800 });
      await expect.soft(page.locator('#builder-pane-end')).toBeHidden();
      await expect.soft(page.getByTestId('pane-hide-end')).toBeFocused();
      await expect.soft(builder).toHaveAnnounced('Inspector hidden.');
      await page.getByTestId('pane-hide-end').click();
      await expect.soft(page.locator('#builder-pane-end')).toBeVisible();
    });

    await resize(page, initial);

    await test.step('panel, outline and node text stays inside its boxes', async () => {
      await builder.selectInOutline('workstation');

      for (const scope of [
        '.builder-palette',
        '.builder-outline',
        '.builder-toolbar',
        '[data-testid="builder-node"]',
      ]) {
        const escaped = await escapedText(page, scope);
        expect
          .soft(escaped, `${scope}: ${JSON.stringify(escaped, null, 2)}`)
          .toEqual([]);
      }

      await expectBadgesInRows(page, 'outline row');
    });

    await test.step('Delete in the outline rename field edits the text, not the diagram', async () => {
      const summary = await builder.summary.textContent();
      const rename = page.locator('.builder-outline__rename');

      await builder.outlineItem('workstation').press('F2');
      await rename.press('Delete');
      await expect.soft(builder.summary).toHaveText(summary);
      await rename.press('Escape');
      await expect(rename).toHaveCount(0);
      await expect(builder.outlineItem('workstation')).toBeVisible();
      await expect.soft(builder.summary).toHaveText(summary);
    });

    await test.step('long names wrap or truncate inside their panels', async () => {
      const long = `host-${'x'.repeat(60)}`;
      const rename = page.locator('.builder-outline__rename');
      await builder.outlineItem('workstation').press('F2');
      await rename.fill(long);
      await rename.press('Enter');
      await expect.soft(builder.inspector).toContainText(long);

      for (const selector of [
        '.builder-inspector',
        '.builder-outline',
        '.builder-layout__side',
      ]) {
        const overflow = await page
          .locator(selector)
          .first()
          .evaluate((element) => element.scrollWidth - element.clientWidth);
        expect.soft(overflow, selector).toBeLessThanOrEqual(0);
      }
      await expectBadgesInRows(page, 'outline row after a long rename');
      await builder.waitSaved();
    });

    await test.step('Focus mode hides the navigation bar and fills the window, in the editor and on the drafts, until its button, its keys or leaving the Builder end it', async () => {
      const nav = page.locator('.navbar');
      const enter = page.getByRole('button', {
        name: 'Focus mode',
        exact: true,
      });
      const canvas = page.getByTestId('builder-canvas');
      // Full screen, where the browser grants it, resizes the window.
      const fills = (what) =>
        expect.soft
          .poll(
            () =>
              page.evaluate(() => {
                const box = document
                  .querySelector('.builder-root')
                  .getBoundingClientRect();

                return {
                  top: box.top,
                  bottom: Math.round(box.bottom) === window.innerHeight,
                  scrolls:
                    document.scrollingElement.scrollHeight > window.innerHeight,
                };
              }),
            { message: what },
          )
          .toEqual({ top: 0, bottom: true, scrolls: false });

      await enter.click();
      await expect.soft(nav).toBeHidden();
      await expect
        .soft(page.getByRole('button', { name: 'Exit focus mode' }))
        .toBeVisible();
      await expect.soft(builder).toHaveAnnounced('Focus mode on');
      await fills('focus mode');

      // Leaving full screen, as the browser's Escape does, leaves focus
      // mode on.
      if (await page.evaluate(() => Boolean(document.fullscreenElement))) {
        await page.evaluate(() => document.exitFullscreen());
        await expect.soft(builder).toHaveAnnounced('Full screen off');
      }
      await expect.soft(nav).toBeHidden();
      await fills('focus mode out of full screen');

      // Exit focus mode is an icon too, so the header stays one row.
      const viewport = page.viewportSize();
      await resize(page, { width: 1366, height: 768 });
      const editorHeader = await page.locator('.builder-header').boundingBox();
      expect
        .soft(editorHeader.height, '1366px in focus mode: header height')
        .toBeLessThan(48);
      await resize(page, viewport);

      // The same keys turn it off and on again, and focus stays put.
      await canvas.focus();
      await page.keyboard.press('ControlOrMeta+Shift+F');
      await expect.soft(nav).toBeVisible();
      await expect.soft(enter).toBeVisible();
      await expect.soft(canvas).toBeFocused();
      await page.keyboard.press('ControlOrMeta+Shift+F');
      await expect.soft(nav).toBeHidden();
      await expect.soft(canvas).toBeFocused();

      // It stays on as the drafts replace the editor. Their header has
      // the same button, and the keys work there too.
      const onDrafts = page.getByTestId('drafts-focus-mode');
      await page.getByTestId('editor-back').click();
      await expect(builder.landingHeading).toBeVisible();
      await expect.soft(nav).toBeHidden();
      await expect.soft(onDrafts).toHaveAccessibleName('Exit focus mode');
      await fills('focus mode on the drafts');
      await page.keyboard.press('ControlOrMeta+Shift+F');
      await expect.soft(nav).toBeVisible();
      await expect.soft(onDrafts).toHaveAccessibleName('Focus mode');

      // Turned on from the drafts, it stays on as a draft opens, and the
      // editor's button ends it.
      await onDrafts.click();
      await expect.soft(nav).toBeHidden();
      await expect.soft(onDrafts).toBeFocused();
      await expect.soft(builder).toHaveAnnounced('Focus mode on');
      await page.getByTestId(`draft-open-${draft.id}`).click();
      await expect(builder.canvas).toBeVisible();
      await expect.soft(nav).toBeHidden();
      const inEditor = page.getByTestId('editor-focus-mode');
      await expect.soft(inEditor).toHaveAccessibleName('Exit focus mode');
      await inEditor.click();
      await expect.soft(nav).toBeVisible();
      await expect.soft(inEditor).toHaveAccessibleName('Focus mode');
    });

    await test.step('leaving the Builder restores the normal page and header height', async () => {
      await page.getByRole('link', { name: 'Experiments' }).click();
      await expect(page).toHaveURL(/\/experiments/);
      await expect.soft(page.getByText(FOOTER)).toBeVisible();
      await expect.soft(page.locator('#main')).toHaveClass(/container/);
      expect.soft(await headerHeight(page)).toBe(header);
    });

    expectNoFatal(issues);
  },
);

test(
  'palette entries show names only and describe themselves in tooltips',
  {
    tag: '@cross-browser',
  },
  async ({ page, builder }) => {
    await builder.open();
    await builder.createBlank();

    const tooltip = page.getByTestId('palette-tooltip');
    const device = builder.palette('device');
    const router = builder.palette('template-router');
    // Parks the pointer on the empty canvas, away from every palette entry.
    const parkPointer = () => page.mouse.move(700, 400);

    await test.step('entries show their names only', async () => {
      await expect
        .soft(page.locator('.builder-palette__item'))
        .toHaveText([
          'Device',
          'Switch',
          'Note',
          'Group',
          'Server',
          'Workstation',
          'Router',
          'Firewall',
          'External device',
        ]);
      await expect
        .soft(page.locator('.builder-palette'))
        .not.toContainText('A virtual machine, container or external device', {
          useInnerText: true,
        });
      await expect.soft(builder.palette('template-printer')).toHaveCount(0);
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('hover shows the description beside the entry, over the canvas', async () => {
      await device.hover();
      await expect(tooltip).toBeVisible();
      await expect
        .soft(tooltip)
        .toHaveText('A virtual machine, container or external device');
      const deviceBox = await device.boundingBox();
      const tipBox = await tooltip.boundingBox();
      expect
        .soft(tipBox.x, 'tooltip left')
        .toBeGreaterThanOrEqual(deviceBox.x + deviceBox.width);
      await page.mouse.move(deviceBox.x + 600, deviceBox.y + 300);
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('the pointer can rest on the tooltip, and clicks go through it', async () => {
      await device.hover();
      await expect(tooltip).toBeVisible();
      const onTip = await tooltip.boundingBox();
      const point = { x: onTip.x + 4, y: onTip.y + onTip.height / 2 };
      await page.mouse.move(point.x, point.y, { steps: 4 });
      await expect.soft(tooltip).toBeVisible();

      // It never blocks the canvas under it: clicks go through and dismiss it.
      const through = await page.evaluate(
        ({ x, y }) => document.elementFromPoint(x, y)?.dataset?.testid || '',
        point,
      );
      expect.soft(through).not.toBe('palette-tooltip');
      await page.mouse.down();
      await page.mouse.up();
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('Escape dismisses a hover tooltip while focus is elsewhere', async () => {
      await page.getByTestId('editor-back').focus();
      await builder.palette('switch').hover();
      await expect.soft(tooltip).toBeVisible();
      await page.keyboard.press('Escape');
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('keyboard focus shows it, and Escape dismisses it', async () => {
      await router.focus();
      await expect.soft(tooltip).toHaveText('Layer 3 router');
      await router.press('Escape');
      await expect.soft(tooltip).toHaveCount(0);
      // The name says what the entry adds; the tooltip text is its
      // description.
      await expect.soft(router).toHaveAccessibleName('Add Router device');
      await expect.soft(router).toHaveAccessibleDescription('Layer 3 router');
      await expect
        .soft(builder.palette('template-external'))
        .toHaveAccessibleName('Add External device');
      await expect
        .soft(builder.palette('device'))
        .toHaveAccessibleDescription(
          'A virtual machine, container or external device',
        );
    });

    await test.step('a help button beside the Add nodes heading explains it in a tooltip', async () => {
      const help =
        'Select an item to add it to the diagram, or drag it onto the canvas.';
      const button = page.getByRole('button', { name: 'Add nodes help' });

      await expect
        .soft(page.getByRole('heading', { name: 'Add nodes', exact: true }))
        .toBeVisible();
      await expect
        .soft(page.locator('.builder-palette'))
        .not.toContainText(help, { useInnerText: true });
      await expect.soft(button).toHaveAccessibleDescription(help);

      await button.hover();
      await expect.soft(tooltip).toHaveText(help);
      await page.mouse.move(700, 500);
      await expect.soft(tooltip).toHaveCount(0);

      await button.focus();
      await expect.soft(tooltip).toHaveText(help);
      await page.keyboard.press('Escape');
      await expect.soft(tooltip).toHaveCount(0);
      // Activating it shows the tooltip again.
      await page.keyboard.press('Enter');
      await expect.soft(tooltip).toHaveText(help);
      await page.keyboard.press('Escape');
    });

    await test.step('the tooltip follows its entry as the side panel scrolls', async () => {
      await resize(page, { width: 1280, height: 600 });
      // A palette entry scrolled under a resting pointer would rightly take
      // over the tooltip.
      await parkPointer();
      const side = page.locator('.builder-layout__side').first();
      await page.getByTestId('editor-back').focus();
      await router.focus();
      await expect.soft(tooltip).toBeVisible();

      // Beside, above or below the entry, never detached from it.
      const detachedBy = async () => {
        const [tip, entry] = await Promise.all([
          tooltip.boundingBox(),
          router.boundingBox(),
        ]);

        return Math.max(
          0,
          tip.y - (entry.y + entry.height),
          entry.y - (tip.y + tip.height),
        );
      };
      const entryBefore = (await router.boundingBox()).y;
      await side.evaluate((element) => element.scrollBy(0, 40));
      await expect.soft
        .poll(async () => (await router.boundingBox()).y)
        .not.toBe(entryBefore);
      await expect.soft.poll(detachedBy).toBeLessThanOrEqual(5);

      // It hides once the entry scrolls out of view.
      await side.evaluate((element) => element.scrollBy(0, 2000));
      await expect.soft(tooltip).toHaveCount(0);
    });

    await test.step('tooltips stay on screen in the stacked narrow layout', async () => {
      await page.getByTestId('editor-back').focus();
      await page
        .locator('.builder-layout__side')
        .first()
        .evaluate((element) => element.scrollTo(0, 0));
      // The stacked palette runs under the canvas point parkPointer() uses, so
      // rest the pointer on the header instead.
      await page.mouse.move(1, 1);
      const narrow = { width: 850, height: 700 };
      await resize(page, narrow);
      await expect.soft(tooltip).toHaveCount(0);

      for (const item of ['device', 'template-external']) {
        await builder.palette(item).hover();
        await expect(tooltip).toBeVisible();
        const box = await tooltip.boundingBox();
        expect.soft(box.x, `${item} tooltip left`).toBeGreaterThanOrEqual(0);
        expect
          .soft(box.x + box.width, `${item} tooltip right`)
          .toBeLessThanOrEqual(narrow.width);
        expect
          .soft(box.y + box.height, `${item} tooltip bottom`)
          .toBeLessThanOrEqual(narrow.height);
        expect.soft(box.width, `${item} tooltip width`).toBeGreaterThan(120);
      }
    });

    await test.step('in a 390px window the save state wraps in the toolbar, whole and on screen', async () => {
      await resize(page, { width: 390, height: 844 });
      const toolbar = await toolbarLayout(page);
      expect
        .soft(toolbar.offScreen, '390: toolbar buttons off screen')
        .toEqual([]);
      expect.soft(toolbar.save.cut, '390: save state cut off').toBe(false);
      expect
        .soft(toolbar.save.right, '390: save state right')
        .toBeLessThanOrEqual(toolbar.right);
    });

    await test.step('in a 320px window the name stays beside Back to drafts and toolbar menus open on screen', async () => {
      const smallest = { width: 320, height: 800 };
      await resize(page, smallest);

      // Back to drafts is as wide as its longest busy label.
      const [back, name] = await Promise.all([
        page.getByTestId('editor-back').boundingBox(),
        page.locator('.builder-header__name').boundingBox(),
      ]);
      expect
        .soft(name.y, 'name beside Back to drafts')
        .toBeLessThan(back.y + back.height);

      for (const id of ['toolbar-auto-group', 'toolbar-layout']) {
        const button = page.getByTestId(id);
        await button.focus();
        await page.keyboard.press('ArrowDown');
        const menu = page.getByTestId(`${id}-menu`);
        await expect(menu).toBeVisible();
        const box = await menu.boundingBox();
        expect.soft(box.x, `${id} menu left`).toBeGreaterThanOrEqual(0);
        expect
          .soft(box.x + box.width, `${id} menu right`)
          .toBeLessThanOrEqual(smallest.width);
        await page.keyboard.press('Escape');
        await expect.soft(button).toBeFocused();
      }
    });
  },
);
