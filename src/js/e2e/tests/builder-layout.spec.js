// Builder Flow page layout: the editor fills the viewport under the header
// without page scrolling, keeps a usable canvas in short windows, with
// enlarged text and however its side columns are resized, text stays inside
// its boxes, palette entries and zoom buttons show their descriptions and
// names as tooltips, and the canvas controls and form fields keep their
// contrast in both themes. Layout depends on the browser engine, so every
// test here also runs in Firefox.

const { test, expect, expectNoFatal, visit } = require('./builder-support');

const VIEWPORTS = [
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
      name: rect('#builder-doc-name'),
      counts: rect('[data-testid="builder-summary"]'),
      editorHeader: rect('.builder-header'),
      actions: rect('.builder-header__actions'),
      actionTops: [
        ...document.querySelector('.builder-header__actions').children,
      ].map((element) => Math.round(element.getBoundingClientRect().top)),
      // The right edge of the last header link.
      navRight: Math.max(
        ...[...document.querySelectorAll('.navbar .navbar-item')]
          .filter((item) => item.offsetParent)
          .map((item) => item.getBoundingClientRect().right),
      ),
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

        // Back to drafts, the name field (at most 13rem wide) and the
        // counts, in that order; from 1280px wide the actions share their
        // row, so the header is one control high.
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

        // The save state and the buttons after it wrap as one group, which
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

    await test.step('Reset view puts the view back as the draft opened, in one press', async () => {
      const reset = page.getByTestId('editor-reset-view');
      const splitters = {
        start: page.getByTestId('splitter-start'),
        end: page.getByTestId('splitter-end'),
      };
      const help = page.getByTestId('canvas-help');
      const side = page.locator('#builder-pane-start');
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

      await reset.hover();
      await expect
        .soft(page.getByTestId('header-tooltip'))
        .toHaveText('Reset column widths, zoom, minimap and scrolling');

      await page.getByTestId('pane-toggle-start').click();
      await splitters.end.focus();
      await page.keyboard.press('ArrowLeft');
      await builder.canvas.focus();
      await page.keyboard.press('=');
      await builder.toolbar('minimap').click();
      await help.locator('summary').click();
      const scrolled = await side.evaluate((element) => {
        element.scrollTop = 120;
        return element.scrollTop;
      });
      expect.soft(scrolled, 'the side column scrolls').toBeGreaterThan(0);
      expect.soft(await stored()).not.toBeNull();

      await reset.click();
      await expect.soft(reset).toBeFocused();
      await expect
        .soft(builder.liveRegion)
        .toContainText('View reset: default column widths');
      for (const key of ['start', 'end']) {
        await expect
          .soft(splitters[key])
          .toHaveAttribute('aria-valuenow', widths[key]);
      }
      await expect
        .soft(page.getByTestId('pane-toggle-start'))
        .toHaveAttribute('aria-pressed', 'false');
      expect.soft(await stored(), 'stored widths').toBeNull();
      await expect
        .soft(builder.toolbar('minimap'))
        .toHaveAttribute('aria-pressed', 'true');
      await expect.soft(page.locator('.vue-flow__minimap')).toBeVisible();
      await expect.soft(help).not.toHaveAttribute('open');
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

      // Text enlarged on its own stacks the columns instead of squeezing
      // the canvas column to nothing.
      await resize(page, { width: 1280, height: 800 });
      const style = await page.addStyleTag({
        content: 'html { font-size: 200% !important; }',
      });
      await resize(page, { width: 1280, height: 800 });
      layout = await editorLayout(page);
      // The stacked columns have no splitters, nor their toggles.
      await expect
        .soft(page.getByRole('separator', { name: /^Resize / }))
        .toHaveCount(0);
      await expect
        .soft(page.getByRole('button', { name: /^Widen / }))
        .toHaveCount(0);
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
      await style.evaluate((element) => element.remove());
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

    await test.step('Focus mode hides the navigation bar and fills the window, until its button, its keys or leaving the editor end it', async () => {
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
      await expect.soft(builder.liveRegion).toContainText('Focus mode on');
      await fills('focus mode');

      // Leaving full screen, as the browser's Escape does, leaves focus
      // mode on.
      if (await page.evaluate(() => Boolean(document.fullscreenElement))) {
        await page.evaluate(() => document.exitFullscreen());
        await expect.soft(builder.liveRegion).toContainText('Full screen off');
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

      // The drafts have no button to leave it, so it ends with the editor.
      await page.getByTestId('editor-back').click();
      await expect.soft(nav).toBeVisible();
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
      await page.getByTestId('builder-name').focus();
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
      await page.getByTestId('builder-name').focus();
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
      await page.getByTestId('builder-name').focus();
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

    await test.step('in a 320px window the name stays beside Back to drafts and toolbar menus open on screen', async () => {
      const smallest = { width: 320, height: 800 };
      await resize(page, smallest);

      // Back to drafts is as wide as its longest busy label.
      const [back, name] = await Promise.all([
        page.getByTestId('editor-back').boundingBox(),
        page.locator('#builder-doc-name').boundingBox(),
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

// WCAG contrast ratio between each element's text color and the first
// opaque background behind it, or between two named colors.
async function contrast(locator, property = 'color') {
  return locator.evaluateAll((elements, prop) => {
    const opaque = (value) =>
      value !== 'transparent' && !/rgba\(.*,\s*0\)$/.test(value);
    const behind = (element) => {
      for (let node = element; node; node = node.parentElement) {
        const value = getComputedStyle(node).backgroundColor;
        if (opaque(value)) {
          return value;
        }
      }

      return 'rgb(255, 255, 255)';
    };
    const luminance = (value) => {
      const [r, g, b] = (value.match(/[\d.]+/g) || []).map(Number);
      const channel = (c) => {
        const v = c / 255;

        return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
      };

      return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
    };
    const ratio = (a, b) => {
      const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);

      return (hi + 0.05) / (lo + 0.05);
    };

    return elements.map((element) => {
      const style = getComputedStyle(element);
      const color = style[prop];
      // A border is judged against what surrounds the element.
      const ground =
        prop === 'color'
          ? behind(element)
          : behind(element.parentElement || element);

      return Math.round(ratio(color, ground) * 100) / 100;
    });
  }, property);
}

// Contrast ratio between the minimap's visible-area outline and its surface,
// plus the outline's stroke width and the surface color.
async function minimapMask(page) {
  return page.locator('.vue-flow__minimap-mask').evaluate((path) => {
    const luminance = (value) => {
      const [r, g, b] = (value.match(/[\d.]+/g) || []).map(Number);
      const channel = (c) => {
        const v = c / 255;

        return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
      };

      return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
    };
    const stroke = getComputedStyle(path).stroke;
    const surface = getComputedStyle(
      path.closest('.vue-flow__minimap'),
    ).backgroundColor;
    const [hi, lo] = [luminance(stroke), luminance(surface)].sort(
      (x, y) => y - x,
    );

    return {
      width: parseFloat(getComputedStyle(path).strokeWidth),
      ratio: (hi + 0.05) / (lo + 0.05),
      surface,
    };
  });
}

test(
  'zoom controls and their tooltips, minimap and form fields are legible in the light and dark themes',
  {
    tag: '@cross-browser',
  },
  async ({ page, builder }) => {
    // Reduced motion turns off color transitions, so the switch to the dark
    // theme below is measured at its final colors.
    await page.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' });
    await builder.open();
    await builder.createBlank();
    await builder.palette('device').click();
    // The Inspector's form holds the device's text fields.
    await builder.selectInOutline('node');

    for (const theme of ['light', 'dark']) {
      await test.step(`${theme} theme`, async () => {
        // The Builder follows the system theme until one is chosen.
        await page.emulateMedia({
          colorScheme: theme,
          reducedMotion: 'reduce',
        });
        // A wrong theme is reported, and only this theme's measurements are
        // skipped.
        const errors = test.info().errors.length;
        await expect
          .soft(page.locator('.builder-root'))
          .toHaveAttribute('data-builder-theme', theme);
        if (test.info().errors.length > errors) {
          return;
        }

        // The +, - and fit glyphs are readable text on their buttons.
        const buttons = page.locator('.vue-flow__controls-button');
        await expect.soft(buttons).toHaveCount(3);
        for (const [index, ratio] of (await contrast(buttons)).entries()) {
          expect
            .soft(ratio, `${theme}: zoom control ${index} text`)
            .toBeGreaterThanOrEqual(4.5);
        }

        // Each shows its name, and the keys that do the same on the
        // canvas, in a readable tooltip on hover, never in a title
        // attribute (WCAG 1.4.13). Those keys do nothing on the buttons,
        // so the buttons do not claim them in aria-keyshortcuts.
        const tip = page.getByTestId('zoom-tooltip');
        for (const [index, name] of [
          'Zoom in (= or + on the canvas)',
          'Zoom out (− on the canvas)',
          `Fit diagram to view (${process.platform === 'darwin' ? '⇧1' : 'Shift+1'} on the canvas)`,
        ].entries()) {
          await expect.soft(buttons.nth(index)).not.toHaveAttribute('title');
          await expect
            .soft(buttons.nth(index))
            .not.toHaveAttribute('aria-keyshortcuts');
          await buttons.nth(index).hover();
          await expect.soft(tip).toHaveText(name);
          const [ratio] = await contrast(tip);
          expect
            .soft(ratio, `${theme}: ${name} tooltip text`)
            .toBeGreaterThanOrEqual(4.5);
        }
        await page.mouse.move(700, 400);
        await expect.soft(tip).toHaveCount(0);

        // The controls and the minimap have a frame that stands out from the
        // canvas (WCAG 1.4.11: 3:1 for UI component boundaries).
        for (const frame of ['.vue-flow__controls', '.vue-flow__minimap']) {
          const [ratio] = await contrast(page.locator(frame), 'borderTopColor');
          expect
            .soft(ratio, `${theme}: ${frame} border`)
            .toBeGreaterThanOrEqual(3);
        }

        // The outline of the visible area inside the minimap stands out from
        // the minimap surface.
        const mask = await minimapMask(page);
        expect
          .soft(mask.width, `${theme}: minimap mask stroke width`)
          .toBeGreaterThanOrEqual(1.5);
        expect
          .soft(mask.ratio, `${theme}: minimap mask stroke contrast`)
          .toBeGreaterThanOrEqual(3);

        if (theme === 'dark') {
          // No bright white boxes on the dark canvas.
          expect
            .soft(mask.surface, 'dark: minimap surface')
            .not.toBe('rgb(255, 255, 255)');
        }

        // Fields share their panel's fill, so their border is what shows
        // where they are (WCAG 1.4.11).
        for (const field of [
          '#builder-doc-name',
          '#connect-device',
          '.builder-inspector input[type="text"]',
        ]) {
          const locator = page.locator(field).first();
          await expect.soft(locator, `${theme}: ${field}`).toBeVisible();
          const [ratio] = await contrast(locator, 'borderTopColor');
          expect
            .soft(ratio, `${theme}: ${field} border`)
            .toBeGreaterThanOrEqual(3);
        }
      });
    }

    await test.step('keyboard focus shows a zoom tooltip too, and Escape dismisses it', async () => {
      const tip = page.getByTestId('zoom-tooltip');
      const fit = page.getByRole('button', { name: 'Fit diagram to view' });

      await fit.focus();
      await expect
        .soft(tip)
        .toHaveText(/^Fit diagram to view \(.+1 on the canvas\)$/);
      await page.keyboard.press('Escape');
      await expect.soft(tip).toHaveCount(0);
      await expect.soft(fit).toBeFocused();
    });
  },
);
