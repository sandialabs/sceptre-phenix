// The VNC console page and the live tiles without minimega (T39, T40), with
// the stand-in experiment and RFB servers of vnc-support.js.
const { test, expect, expectNoAxeViolations } = require('./helpers');
const {
  EXP,
  WIDTH,
  HEIGHT,
  COLORS,
  VMS,
  SET_PIXEL_FORMAT,
  SET_ENCODINGS,
  UPDATE_REQUEST,
  standInExperiment,
} = require('./vnc-support');

const consoleOf = (page, vm) =>
  page.getByRole('application', {
    name: `Console of VM ${vm} in experiment ${EXP}`,
  });

const paneOf = (page, n, vm) =>
  page.getByRole('region', { name: `Pane ${n}: ${vm}` });

// The RGBA color at the center of the canvas of a console.
const centerColor = (canvas) =>
  canvas.evaluate((c) =>
    Array.from(
      c
        .getContext('2d')
        .getImageData(Math.floor(c.width / 2), Math.floor(c.height / 2), 1, 1)
        .data,
    ),
  );

async function expectDrawn(canvas, vm) {
  await expect(canvas).toHaveJSProperty('width', WIDTH);
  await expect(canvas).toHaveJSProperty('height', HEIGHT);
  await expect.poll(() => centerColor(canvas)).toEqual([...COLORS[vm], 255]);
}

const consoleUrl = (...vms) =>
  `/vnc?${vms.map((vm) => `vm=${EXP}/${vm}`).join('&')}`;

test.describe('the VNC console page', () => {
  test(
    'draws the screen of a VM between its banners',
    { tag: ['@vnc', '@cross-browser'] },
    async ({ page }) => {
      const servers = await standInExperiment(page);
      await page.goto(consoleUrl('web'));

      await expectDrawn(consoleOf(page, 'web').locator('canvas'), 'web');
      await expect(
        page.getByRole('button', { name: /Connected/ }).first(),
      ).toBeVisible();
      await expect(page.getByText(`EXP: ${EXP} - VM: web`)).toHaveCount(2);
      expect(servers.of('web')).toHaveLength(1);
      expect(servers.of('web')[0].types.slice(0, 3)).toEqual([
        SET_PIXEL_FORMAT,
        SET_ENCODINGS,
        UPDATE_REQUEST,
      ]);
    },
  );

  test(
    'lays out split panes and keeps their connections when the layout changes',
    { tag: ['@vnc', '@cross-browser'] },
    async ({ page }) => {
      const servers = await standInExperiment(page);
      await page.goto(consoleUrl('web', 'db'));

      const first = paneOf(page, 1, 'web');
      const second = paneOf(page, 2, 'db');
      await expectDrawn(consoleOf(page, 'web').locator('canvas'), 'web');
      await expectDrawn(consoleOf(page, 'db').locator('canvas'), 'db');

      // two VMs open in two columns
      const layout = page.getByRole('group', { name: 'Layout' });
      await expect(
        layout.getByRole('button', { name: 'Two columns' }),
      ).toHaveAttribute('aria-pressed', 'true');
      let [a, b] = [await first.boundingBox(), await second.boundingBox()];
      expect(b.x).toBeGreaterThanOrEqual(a.x + a.width - 1);
      expect(Math.abs(b.y - a.y)).toBeLessThan(2);

      await layout.getByRole('button', { name: 'Two rows' }).click();
      await expect(page).toHaveURL(/[?&]layout=rows(&|$)/);
      await expect
        .poll(async () => {
          [a, b] = [await first.boundingBox(), await second.boundingBox()];
          return b.y >= a.y + a.height - 1 && Math.abs(b.x - a.x) < 2;
        })
        .toBe(true);

      await layout
        .getByRole('button', { name: 'One large pane and two stacked' })
        .click();
      await expect(page.getByText('Empty pane')).toHaveCount(1);
      await layout.getByRole('button', { name: 'Grid of four' }).click();
      await expect(page.getByText('Empty pane')).toHaveCount(2);
      await expect(
        layout.getByRole('button', { name: 'One pane' }),
      ).toHaveAttribute('aria-disabled', 'true');

      // the panes moved, but each VM kept its one connection
      await expectDrawn(consoleOf(page, 'web').locator('canvas'), 'web');
      await expectDrawn(consoleOf(page, 'db').locator('canvas'), 'db');
      expect(servers.of('web')).toHaveLength(1);
      expect(servers.of('db')).toHaveLength(1);
    },
  );

  test(
    'moves the keyboard to another pane and out of the console with Ctrl+]',
    { tag: ['@vnc', '@cross-browser'] },
    async ({ page }) => {
      const servers = await standInExperiment(page);
      await page.goto(consoleUrl('web', 'db'));
      const web = consoleOf(page, 'web').locator('canvas');
      const db = consoleOf(page, 'db').locator('canvas');
      await expectDrawn(web, 'web');
      await expectDrawn(db, 'db');
      const keyboardTag = (n, vm) => paneOf(page, n, vm).getByText('Keyboard');

      await web.click();
      await expect(keyboardTag(1, 'web')).toBeVisible();
      await page.keyboard.press('a');
      await expect.poll(() => servers.keysymsOf('web')).toContain(0x61);

      // Ctrl+] then 2 gives the keyboard to pane 2
      await page.keyboard.press('Control+BracketRight');
      await page.keyboard.press('Digit2');
      await expect(keyboardTag(2, 'db')).toBeVisible();
      await expect(keyboardTag(1, 'web')).toHaveCount(0);
      await page.keyboard.press('b');
      await expect.poll(() => servers.keysymsOf('db')).toContain(0x62);

      // Ctrl+] then Tab leaves the console for the top bar
      await page.keyboard.press('Control+BracketRight');
      await page.keyboard.press('Tab');
      await expect(
        page.getByRole('group', { name: 'Console controls' }).locator(':focus'),
      ).toHaveCount(1);
      await expect(keyboardTag(2, 'db')).toHaveCount(0);

      // a key typed on each console after the chords shows that every key
      // sent before it has arrived: the chord keys never reached a VM
      await db.click();
      await page.keyboard.press('c');
      await expect.poll(() => servers.keysymsOf('db')).toContain(0x63);
      await web.click();
      await page.keyboard.press('d');
      await expect.poll(() => servers.keysymsOf('web')).toContain(0x64);
      const chordKeys = [0x5d, 0x32, 0xff09];
      for (const vm of ['web', 'db']) {
        expect(
          servers.keysymsOf(vm).filter((k) => chordKeys.includes(k)),
          `chord keys sent to ${vm}`,
        ).toEqual([]);
      }
    },
  );
});

test.describe('the live tiles of the VNC tab', () => {
  test(
    'show the running and paused VMs live and send no input to them',
    { tag: ['@vnc', '@cross-browser'] },
    async ({ page }) => {
      const servers = await standInExperiment(page);
      await page.goto(`/experiment/${EXP}?tab=vnc`);

      const tiles = page.locator('.vnc-tile');
      await expect(tiles).toHaveCount(VMS.length);
      const live = page.locator('.vnc-tile.is-live');
      await expect(live).toHaveCount(3);
      for (const vm of ['web', 'db', 'plc']) {
        const canvas = page
          .getByRole('application', {
            name: `Live screen of VM ${vm} in experiment ${EXP}`,
          })
          .locator('canvas');
        await expectDrawn(canvas, vm);
      }
      // the shut down VM has no screen and no tag
      await expect(page.locator('.vnc-tile-tag')).toHaveText([
        'LIVE',
        'LIVE',
        'PAUSED',
      ]);

      // the pointer and keys of a user, and the same events sent straight
      // to each canvas, where a console that is not view only takes them
      for (let i = 0; i < 3; i++) {
        await live.nth(i).hover();
        await page.mouse.wheel(0, 40);
      }
      for (let i = 0; i < 3; i++) {
        await live
          .nth(i)
          .locator('canvas')
          .evaluate((c) => {
            const box = c.getBoundingClientRect();
            const at = {
              clientX: box.x + 10,
              clientY: box.y + 10,
              bubbles: true,
            };
            c.dispatchEvent(new MouseEvent('mousemove', at));
            c.dispatchEvent(new MouseEvent('mousedown', at));
            // noVNC takes the mouse until a mouseup on its capture element
            const capture = document.getElementById('noVNC_mouse_capture_elem');
            (capture ?? c).dispatchEvent(new MouseEvent('mouseup', at));
            c.dispatchEvent(new WheelEvent('wheel', { ...at, deltaY: 40 }));
            for (const type of ['keydown', 'keyup']) {
              c.dispatchEvent(
                new KeyboardEvent(type, {
                  key: 'x',
                  code: 'KeyX',
                  bubbles: true,
                }),
              );
            }
          });
      }
      await page.keyboard.press('x');

      // A screen update makes each tile ask for the next one. Its request
      // comes after any input the tile sent, so then no input can still come.
      const tileConnections = servers.connections.filter((c) =>
        ['web', 'db', 'plc'].includes(c.vm),
      );
      expect(tileConnections).toHaveLength(3);
      for (const conn of tileConnections) conn.sendUpdate();
      await expect
        .poll(() =>
          tileConnections.every((c) =>
            c.types.slice(c.pushed).includes(UPDATE_REQUEST),
          ),
        )
        .toBe(true);
      for (const conn of tileConnections) {
        expect(
          [...new Set(conn.types)].sort(),
          `message types from the tile of ${conn.vm}`,
        ).toEqual([SET_PIXEL_FORMAT, SET_ENCODINGS, UPDATE_REQUEST]);
      }
    },
  );
});

test.describe('axe scans of the VNC console', () => {
  test(
    'the console page, alone and split, and the VNC tab',
    { tag: ['@axe', '@vnc'] },
    async ({ page }) => {
      await standInExperiment(page);

      await page.goto(consoleUrl('web'));
      await expectDrawn(consoleOf(page, 'web').locator('canvas'), 'web');
      await expectNoAxeViolations(page, 'vnc-single');

      await page.goto(`${consoleUrl('web', 'db')}&layout=grid`);
      await expectDrawn(consoleOf(page, 'web').locator('canvas'), 'web');
      await expectDrawn(consoleOf(page, 'db').locator('canvas'), 'db');
      await expectNoAxeViolations(page, 'vnc-split');

      await page.goto(`/experiment/${EXP}?tab=vnc`);
      await expect(page.locator('.vnc-tile.is-live')).toHaveCount(3);
      await expectNoAxeViolations(page, 'vnc-tab');
    },
  );
});
