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
      // The header buttons that show their labels, by test id, and whether
      // Commands shows its keys.
      labelled: [
        ...document.querySelectorAll('.builder-header .builder-header__label'),
      ]
        .filter((label) => getComputedStyle(label).position !== 'absolute')
        .map((label) => label.closest('[data-testid]').dataset.testid),
      keycaps: [...document.querySelectorAll('.builder-header kbd')].some(
        (kbd) => kbd.checkVisibility(),
      ),
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

      // In a 1600px window, Commands keeps its label, and the theme,
      // Settings and Help show their icons alone, as in the editor's header.
      await resize(page, { width: 1600, height: 900 });
      const labelled = await page.$$eval(
        '.builder-drafts__header .builder-header__label',
        (labels) =>
          labels
            .filter((label) => getComputedStyle(label).position !== 'absolute')
            .map((label) => label.closest('[data-testid]').dataset.testid),
      );
      expect.soft(labelled).toEqual(['drafts-commands']);
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

        // The labels go in two steps: in a wide header every button has
        // its label, in a narrower one Reset view and Commands (without its
        // keys) keep theirs, and in a narrow one none does.
        const labelled = {
          1920: [
            'editor-reset-view',
            'editor-commands',
            'editor-theme',
            'editor-shortcuts',
            'editor-settings',
            'editor-help',
          ],
          1440: ['editor-reset-view', 'editor-commands'],
        };
        expect
          .soft(layout.labelled, at('buttons with their labels'))
          .toEqual(labelled[viewport.width] || []);
        expect
          .soft(layout.keycaps, at('Commands keys'))
          .toBe(viewport.width === 1920);

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

      // Directly under each Widen toggle, at least 24px square, and named
      // for what a press does to the column it controls.
      for (const side of ['start', 'end']) {
        const widen = await page
          .getByTestId(`pane-toggle-${side}`)
          .boundingBox();
        const box = await hide[side].boundingBox();
        expect
          .soft(Math.min(box.width, box.height), `${side} Hide size`)
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
      await expect.soft(builder.liveRegion).toContainText('Inspector hidden');
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
      // 'Device external, 0 connections, …', as the Inspector names it.
      const [label] = (await node.getAttribute('aria-label')).split(',');
      await page.keyboard.press('Enter');
      await expect.soft(columns.end).toBeVisible();
      await expect.soft(hide.end).toHaveAttribute('aria-expanded', 'true');
      await expect.soft(builder.liveRegion).toContainText('Inspector shown');
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
      const width = Number(await splitter.getAttribute('aria-valuenow'));
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
      await expect
        .soft(builder.liveRegion)
        .toContainText('Minimap 216 by 162 pixels');
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
      await expect.soft(builder.liveRegion).toContainText('(largest)');
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
      const help = page.getByTestId('canvas-help');
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

      // Its label shows at this width, so its tooltip only says what it
      // does; as an icon, in a narrower window, it names it too.
      await reset.hover();
      await expect
        .soft(page.getByTestId('header-tooltip'))
        .toHaveText('Reset column widths, zoom, minimap and scrolling');
      await resize(page, { width: 1280, height: 640 });
      await page.mouse.move(0, 0);
      await reset.hover();
      await expect
        .soft(page.getByTestId('header-tooltip'))
        .toHaveText(
          'Reset view: reset column widths, zoom, minimap and scrolling',
        );
      await resize(page, { width: 1440, height: 640 });

      await page.getByTestId('pane-toggle-start').click();
      await splitters.end.focus();
      await page.keyboard.press('ArrowLeft');
      await page.getByTestId('pane-hide-end').click();
      await builder.canvas.focus();
      await page.keyboard.press('=');
      await minimapHandle.focus();
      await page.keyboard.press('ArrowUp');
      await expect.soft(minimapHandle).toHaveAttribute('aria-valuenow', '216');
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
      await expect.soft(builder.liveRegion).toContainText('Inspector hidden.');
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
      await expect.soft(builder.liveRegion).toContainText('Focus mode on');
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
// plus the outline's width as drawn, in CSS pixels, and the surface color.
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

    // A stroke width is in the minimap's own units, which it scales down to
    // fit the whole diagram, unless the stroke does not scale.
    const style = getComputedStyle(path);
    const scale =
      style.vectorEffect === 'non-scaling-stroke'
        ? 1
        : path.ownerSVGElement.getScreenCTM().a;

    return {
      width: parseFloat(style.strokeWidth) * scale,
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
