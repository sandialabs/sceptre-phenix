// Builder Beta persistence: undo and redo, the server history cursor, the
// History dialog, autosave states, ETag conflicts and local recovery.
//
// Every edit in the Builder is one server snapshot, and undo/redo move the
// draft's history cursor. The tests therefore check both what the editor shows
// and what the server holds, reading the draft through the API.

const {
  test,
  expect,
  blankDocument,
  expectNoFatal,
  uniqueName,
  visit,
  draftPath,
  publishTopology,
  API,
  SAVED,
} = require('./builder-support');

const SNAPSHOTS = '**/api/v1/builder/drafts/*/*/snapshots';
const DRAFT_ROUTES = '**/api/v1/builder/drafts/**';
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

async function listMine(request) {
  const response = await request.get(`${API}/builder/drafts`);
  expect(response.ok(), await response.text()).toBeTruthy();

  return (await response.json()).drafts || [];
}

function countKind(doc, kind) {
  return (doc?.nodes || []).filter((node) => node.kind === kind).length;
}

// The summary reads "N devices, 1 switch, ...". The word boundary keeps
// "1 device" from matching "11 devices".
function summaryPattern({ devices = 0, switches = 0 }) {
  const count = (n, noun, plural = `${noun}s`) =>
    `${n} ${n === 1 ? noun : plural}`;

  return new RegExp(
    `\\b${count(devices, 'device')}, ${count(switches, 'switch', 'switches')},`,
  );
}

async function expectCounts(builder, counts) {
  await expect(builder.summary).toHaveText(summaryPattern(counts));
}

// Waits until the server's current document (the snapshot the cursor points
// at) has the given node counts. Polling avoids racing the "All changes saved"
// text, which still shows the previous state until the next save starts.
async function expectServerCounts(
  builder,
  draft,
  { devices = 0, switches = 0 },
) {
  await expect
    .poll(
      async () => {
        const doc = await builder.serverDocument(draft);

        return {
          devices: countKind(doc, 'device'),
          switches: countKind(doc, 'switch'),
        };
      },
      { message: 'server document node counts', timeout: 20000 },
    )
    .toEqual({ devices, switches });
}

// Adds devices from the palette to a diagram without switches. Click-added
// devices are named node, node-2, node-3, ... in order.
async function addDevices(builder, count) {
  const start = Number(
    /(\d+) devices?\b/.exec(await builder.summary.textContent())[1],
  );

  for (let index = 1; index <= count; index += 1) {
    await builder.palette('device').click();
    await expectCounts(builder, { devices: start + index });
  }
}

// Vue Flow's wrapper around the device with this label: the node's one
// focusable element, which takes the canvas shortcuts.
function deviceNode(builder, label) {
  return builder.page.locator('.vue-flow__node-builderDevice').filter({
    has: builder.page.locator('.builder-node__label', {
      hasText: new RegExp(`^${label}$`),
    }),
  });
}

async function listSnapshots(request, draft) {
  const response = await request.get(`${draftPath(draft)}/snapshots`);
  expect(response.ok(), await response.text()).toBeTruthy();

  return (await response.json()).snapshots;
}

// Writes a snapshot as another editor would: through the API, with the
// current ETag, so the page's own ETag becomes stale.
async function writeElsewhere(request, draft, change) {
  const current = await request.get(draftPath(draft));
  expect(current.ok(), await current.text()).toBeTruthy();
  const body = await current.json();

  const response = await request.post(`${draftPath(draft)}/snapshots`, {
    headers: { 'If-Match': current.headers().etag },
    data: { summary: 'Changed elsewhere', document: change(body.document) },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}

// Freezes page timers so the autosave retry backoff never fires on its own:
// anything sent after this point was sent because the test asked for it.
// Requires page.clock.install() before the page was opened.
async function freezeTimers(page) {
  await page.clock.pauseAt(Date.now() + 1000);
}

// Saves a renamed diagram with one device, has another editor write a newer
// snapshot, then adds a second device so the page hits the ETag conflict, and
// resolves it by forking. Returns the original draft, the fork and the title.
async function forkAfterConflict(builder, testInfo) {
  await builder.open();
  const draft = await builder.createBlank();

  const title = uniqueName(testInfo, 'fork');
  await builder.rename(title);
  await addDevices(builder, 1);
  await expectServerCounts(builder, draft, { devices: 1 });
  await builder.waitSaved();

  await writeElsewhere(builder.request, draft, (doc) => ({
    ...doc,
    description: 'Changed elsewhere',
  }));

  await addDevices(builder, 1);
  await expect(builder.page.getByTestId('builder-conflict')).toBeVisible();

  // The fork's snapshots take a moment, so an edit made meanwhile would land
  // while the new draft is being written (R39).
  let slow = true;
  await builder.page.route(
    (url) => url.pathname.endsWith('/snapshots'),
    async (route) => {
      if (slow) {
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }

      await route.continue();
    },
  );

  const forked = builder.page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname === `${API}/builder/drafts`,
  );
  await builder.page.getByTestId('conflict-fork').click();
  const body = await (await forked).json();
  expect(body.id).not.toBe(draft.id);

  // An edit while the history is saved is refused and said so, instead of
  // being left out of the new draft.
  await builder.palette('device').click();
  await expect
    .soft(builder.liveRegion)
    .toContainText(
      'Not changed: the conflict is being resolved. Edit again once it is.',
    );
  slow = false;

  return { draft, fork: { id: body.id, owner: body.owner }, title };
}

// What this browser keeps for Builder drafts: the draft records, and the
// number of entry snapshots, each a record of its own.
function localDrafts(page) {
  return page.evaluate(
    () =>
      new Promise((resolve, reject) => {
        const request = indexedDB.open('phenix-builder');
        request.onerror = () => reject(request.error);
        request.onsuccess = () => {
          const db = request.result;
          const tx = db.transaction(['drafts', 'entries']);
          const drafts = tx.objectStore('drafts').getAll();
          const entries = tx.objectStore('entries').count();
          tx.oncomplete = () => {
            db.close();
            resolve({ drafts: drafts.result, entries: entries.result });
          };
        };
      }),
  );
}

// Resolves with the next snapshot upload the page makes.
function nextSnapshotPost(page) {
  return page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith('/snapshots'),
  );
}

test.describe('Builder Beta persistence', () => {
  test(
    'keyboard undo and redo move the server cursor, a new edit drops the redo branch, and an undo survives a reload',
    { tag: '@cross-browser' },
    async ({ builder, issues }) => {
      await builder.open();
      const draft = await builder.createBlank();

      await addDevices(builder, 3);
      await expectServerCounts(builder, draft, { devices: 3 });

      // The first device stays on the canvas throughout, so focus stays
      // inside the canvas where the shortcuts are handled. Checks that only
      // report state are soft, so a wording change in one step does not hide
      // the later steps; the counts each next keypress builds on stay hard.
      const first = deviceNode(builder, 'node');

      await test.step('Ctrl+Z, Ctrl+Shift+Z and Ctrl+Y update the server document', async () => {
        await first.press('ControlOrMeta+z');
        await expectCounts(builder, { devices: 2 });
        await expect
          .soft(builder.liveRegion)
          .toHaveText(/Undid Added device\./);
        await first.press('ControlOrMeta+z');
        await expectCounts(builder, { devices: 1 });
        await expectServerCounts(builder, draft, { devices: 1 });

        await first.press('ControlOrMeta+Shift+z');
        await expectCounts(builder, { devices: 2 });
        await expect
          .soft(builder.liveRegion)
          .toHaveText(/Redid Added device\./);
        await expectServerCounts(builder, draft, { devices: 2 });

        // Ctrl+Y redoes on Windows and Linux; macOS has only ⇧⌘Z.
        await first.press(
          process.platform === 'darwin' ? 'Meta+Shift+z' : 'Control+y',
        );
        await expectCounts(builder, { devices: 3 });
        await expectServerCounts(builder, draft, { devices: 3 });
        await builder.waitSaved();

        // Undo and redo move the cursor; they never add snapshots.
        const snapshots = await listSnapshots(builder.request, draft);
        expect.soft(snapshots).toHaveLength(4);
        expect.soft(snapshots.at(-1)?.current).toBe(true);
      });

      await test.step('a new edit after undo drops the redo branch', async () => {
        await first.press('ControlOrMeta+z');
        await expectCounts(builder, { devices: 2 });
        await expectServerCounts(builder, draft, { devices: 2 });

        await builder.palette('switch').click();
        await expectCounts(builder, { devices: 2, switches: 1 });
        await expectServerCounts(builder, draft, { devices: 2, switches: 1 });

        // The new edit dropped the redo branch: Redo is unavailable, and the
        // keyboard redo below finds nothing to redo.
        await expect.soft(builder.toolbar('redo')).toBeDisabled();
        await first.press('ControlOrMeta+Shift+z');
        await expect.soft(builder.liveRegion).toHaveText(/Nothing to redo\./);
        await expectCounts(builder, { devices: 2, switches: 1 });
        await builder.waitSaved();

        // The server discarded the undone snapshot when the switch was
        // appended. The creation snapshot comes first; its summary is not
        // checked (V4).
        const snapshots = await listSnapshots(builder.request, draft);
        expect.soft(snapshots).toHaveLength(4);
        expect
          .soft(snapshots.slice(1).map((snapshot) => snapshot.summary))
          .toEqual(['Added device', 'Added device', 'Added switch']);
        expect.soft((await builder.serverDraft(draft)).canRedo).toBe(false);
      });

      await test.step('an undo is stored as the draft cursor and survives a reload', async () => {
        await first.press('ControlOrMeta+z');
        await expectCounts(builder, { devices: 2, switches: 0 });
        await expectServerCounts(builder, draft, { devices: 2, switches: 0 });
        await builder.waitSaved();

        const server = await builder.serverDraft(draft);
        expect.soft(server.canRedo).toBe(true);
        expect.soft(server.cursor).toBe(2);

        // A fresh page load reads the draft back from the server.
        await builder.openDraft(draft);
        await expectCounts(builder, { devices: 2, switches: 0 });
        await expect.soft(deviceNode(builder, 'node-2')).toBeVisible();
        await expect.soft(builder.nodes('switch')).toHaveCount(0);
      });

      expectNoFatal(issues);
    },
  );

  test('History lists the server snapshots and restores one', async ({
    builder,
    issues,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();

    await addDevices(builder, 1);
    await builder.palette('switch').click();
    await expectCounts(builder, { devices: 1, switches: 1 });
    await expectServerCounts(builder, draft, { devices: 1, switches: 1 });
    await builder.waitSaved();

    // Each read of the list waits here until the test answers it.
    const { page } = builder;
    let answer = null;
    await page.route(SNAPSHOTS, async (route) => {
      if (route.request().method() !== 'GET') {
        return route.fallback();
      }
      const how = await new Promise((resolve) => {
        answer = resolve;
      });
      answer = null;

      return how === 'fail'
        ? route.fulfill({ status: 503, json: { error: 'try again later' } })
        : route.fallback();
    });

    // History opens at once, while the list is read: the list's place is
    // busy, and the status, which describes the dialog, says it loads.
    const dialog = await builder.openDialog('history');
    const status = page.getByTestId('history-status');
    const content = page.getByTestId('history-content');
    await expect(page.getByTestId('history-dialog')).toBeVisible();
    await expect(dialog.getByRole('heading')).toHaveText('Draft history');
    await expect.soft(dialog).toBeFocused();
    await expect(status).toHaveText('Loading the draft history…');
    await expect.soft(status).toHaveRole('status');
    await expect
      .soft(dialog)
      .toHaveAccessibleDescription('Loading the draft history…');
    await expect.soft(content).toHaveAttribute('aria-busy', 'true');

    // A read that fails says so in the dialog, not in the page alert behind
    // it, and Try again reads again, keeping focus in the dialog.
    await expect.poll(() => answer).not.toBeNull();
    answer('fail');
    await expect(page.getByTestId('history-error')).toHaveText(
      /^Could not read the draft history\. /,
    );
    await expect.soft(page.getByTestId('builder-error')).toHaveCount(0);
    await page.getByTestId('history-retry').press('Enter');
    await expect.soft(dialog).toBeFocused();
    await expect.soft(status).toHaveText('Loading the draft history…');
    await expect.poll(() => answer).not.toBeNull();
    answer('pass');
    await page.unroute(SNAPSHOTS);

    const list = page.getByTestId('history-list');
    await expect(list.getByRole('listitem')).toHaveCount(3);
    await expect.soft(content).not.toHaveAttribute('aria-busy');
    await expect
      .soft(dialog)
      .toHaveAccessibleDescription('3 snapshots, oldest first.');
    await expect(
      list.getByRole('button', { name: /^Restore Added switch, / }),
    ).toBeVisible();

    await list.getByRole('button', { name: /^Restore Added device, / }).click();
    await expect(builder.page.getByTestId('history-dialog')).toHaveCount(0);
    await expectCounts(builder, { devices: 1, switches: 0 });
    await expect(builder.liveRegion).toHaveText(/Snapshot restored/);
    await expectServerCounts(builder, draft, { devices: 1, switches: 0 });
    await builder.waitSaved();

    // Restoring moves the cursor; the later snapshot is still in history.
    const snapshots = await listSnapshots(builder.request, draft);
    expect(snapshots).toHaveLength(3);
    expect(snapshots.map((snapshot) => snapshot.current)).toEqual([
      false,
      true,
      false,
    ]);

    // The restored document is the one a fresh page load opens.
    await builder.openDraft(draft);
    await expectCounts(builder, { devices: 1, switches: 0 });

    expectNoFatal(issues);
  });

  // One draft walks through every autosave state in turn. Each step empties
  // the queue before the next begins, so the change counts in the save state
  // are the step's own. The server device counts are cumulative: a step whose
  // upload is lost stops the test at its own hard server count check. The
  // save-state text and Retry button checks are soft, so one wrong status
  // does not hide the later states.
  test(
    'autosave reports queued, offline and failed saves, and Save now’s key and Retry saving upload them',
    { tag: '@cross-browser' },
    async ({ builder, issues, page }) => {
      await page.clock.install();
      await builder.open();
      const draft = await builder.createBlank();

      await test.step('save state counts queued edits and settles once they are saved', async () => {
        // Hold every snapshot upload until the test releases it.
        let release;
        const gate = new Promise((resolve) => {
          release = resolve;
        });
        await page.route(SNAPSHOTS, async (route) => {
          if (route.request().method() === 'POST') {
            await gate;
          }
          await route.fallback();
        });

        await builder.palette('device').click();
        await expect.soft(builder.saveState).toHaveText(/Saving changes/);
        await builder.palette('device').click();
        await expect.soft(builder.saveState).toHaveText(/Saving 2 changes/);
        await expect.soft(builder.toolbar('retry')).toHaveCount(0);

        release();
        await builder.waitSaved();
        await expectServerCounts(builder, draft, { devices: 2 });
        await page.unroute(SNAPSHOTS);
      });

      // From here on the retry backoff cannot send anything by itself.
      await freezeTimers(page);

      await test.step('offline edits are kept and Retry saving uploads them', async () => {
        await page.route(DRAFT_ROUTES, (route) => route.abort());
        await addDevices(builder, 2);
        await expect
          .soft(builder.saveState)
          .toHaveText(/Offline: 2 changes kept on this device/);
        await expectServerCounts(builder, draft, { devices: 2 });

        await page.unroute(DRAFT_ROUTES);
        await expect(builder.toolbar('retry')).toBeVisible();
        await builder.toolbar('retry').press('Enter');
        await builder.waitSaved();
        await expect.soft(builder.toolbar('retry')).toHaveCount(0);
        // The pressed button is gone; focus moves on to the button before
        // it, Commands, which takes the toolbar's Tab stop, instead of
        // falling to <body>.
        await expect
          .soft(builder.toolbar('commands'), 'focus after Retry saving')
          .toBeFocused();
        await expect
          .soft(builder.toolbar('commands'))
          .toHaveAttribute('tabindex', '0');
        await expectServerCounts(builder, draft, { devices: 4 });
      });

      await test.step('Save now’s key sends a queue that is waiting to retry', async () => {
        await page.route(DRAFT_ROUTES, (route) => route.abort());
        await builder.palette('device').click();
        await expect
          .soft(builder.saveState)
          .toHaveText(/Offline: 1 change kept on this device/);
        await page.unroute(DRAFT_ROUTES);

        // The toolbar has no Save button: edits save as they are made.
        await expect.soft(builder.toolbar('save')).toHaveCount(0);
        const sent = nextSnapshotPost(page);
        await page.keyboard.press('ControlOrMeta+s');
        expect.soft((await sent).ok()).toBeTruthy();
        await builder.waitSaved();
        await expectServerCounts(builder, draft, { devices: 5 });
      });

      await test.step('a server error is reported and Retry saving recovers', async () => {
        await page.route(SNAPSHOTS, (route) =>
          route.request().method() === 'POST'
            ? route.fulfill({
                status: 500,
                contentType: 'application/json',
                body: JSON.stringify({ error: 'Simulated storage failure' }),
              })
            : route.fallback(),
        );
        await builder.palette('device').click();
        await expect
          .soft(builder.saveState)
          .toHaveText(/Simulated storage failure/);
        await expect(builder.toolbar('retry')).toBeVisible();
        // The message wraps, and the header's buttons, labelled or not,
        // keep one height rather than growing with its row.
        const heights = await page
          .locator('.builder-header__actions > .builder-button')
          .evaluateAll((buttons) =>
            buttons.map((button) =>
              Math.round(button.getBoundingClientRect().height),
            ),
          );
        expect
          .soft(new Set(heights).size, `header button heights ${heights}`)
          .toBe(1);
        expect
          .soft((await builder.saveState.boundingBox()).height, 'save state')
          .toBeGreaterThan(heights[0]);

        await page.unroute(SNAPSHOTS);
        await builder.toolbar('retry').click();
        await builder.waitSaved();
        await expectServerCounts(builder, draft, { devices: 6 });
      });

      // Last, since a conflict blocks every later save.
      await test.step('a conflict met by the automatic retry takes focus from Retry saving', async () => {
        await page.route(DRAFT_ROUTES, (route) => route.abort());
        await builder.palette('device').click();
        await expect(builder.toolbar('retry')).toBeVisible();
        await builder.toolbar('retry').focus();

        await writeElsewhere(builder.request, draft, (doc) => ({
          ...doc,
          description: 'Changed elsewhere',
        }));
        await page.unroute(DRAFT_ROUTES);
        // Past the first retry delay: the automatic retry, not a press,
        // sends the edit and meets the conflict.
        await page.clock.runFor(2000);

        const banner = page.getByTestId('builder-conflict');
        await expect(banner).toBeVisible();
        // The conflict panel keeps focus; Commands only takes the toolbar's
        // Tab stop from the removed button.
        await expect(
          banner.getByRole('heading', {
            name: 'This draft changed on the server',
          }),
          'focus after the automatic retry',
        ).toBeFocused();
        await expect
          .soft(builder.toolbar('commands'))
          .toHaveAttribute('tabindex', '0');
      });

      expectNoFatal(issues);
    },
  );

  test('a concurrent write raises the conflict banner and Discard loads the server copy', async ({
    builder,
    issues,
  }, testInfo) => {
    await builder.open();
    const draft = await builder.createBlank();

    await addDevices(builder, 1);
    await expectServerCounts(builder, draft, { devices: 1 });
    await builder.waitSaved();

    const elsewhere = uniqueName(testInfo, 'elsewhere');
    await writeElsewhere(builder.request, draft, (doc) => ({
      ...doc,
      name: elsewhere,
    }));

    await addDevices(builder, 1);
    const banner = builder.page.getByTestId('builder-conflict');
    await expect(banner).toBeVisible();
    // A region with an alert, not a non-modal alertdialog. It takes focus,
    // so its two choices are the next Tab stops.
    await expect(banner).toHaveRole('region');
    await expect
      .soft(banner.getByRole('alert'))
      .toContainText('your changes cannot be written over it');
    await expect
      .soft(banner)
      .toContainText('Your 1 unsaved change is kept on this device.');
    await expect(
      banner.getByRole('heading', { name: 'This draft changed on the server' }),
    ).toBeFocused();
    await expect(builder.saveState).toHaveText(
      /This draft changed on the server/,
    );

    // Local work is never written over the newer server copy.
    const server = await builder.serverDocument(draft);
    expect(server.name).toBe(elsewhere);
    expect(countKind(server, 'device')).toBe(1);

    // Discarding local work asks first, with focus on the safe choice.
    await builder.page.getByTestId('conflict-reload').click();
    const confirm = builder.page.getByRole('alertdialog', {
      name: 'Discard your unsaved changes?',
    });
    await expect(confirm).toBeVisible();
    await expect.soft(confirm.getByTestId('confirm-cancel')).toBeFocused();
    await confirm.getByTestId('confirm-accept').click();
    await expect(banner).toHaveCount(0);
    await expect(builder.page.getByTestId('builder-name')).toHaveValue(
      elsewhere,
    );
    // Focus lands in the editor, not on <body>.
    await expect
      .poll(() =>
        builder.page.evaluate(() => document.activeElement !== document.body),
      )
      .toBe(true);
    await expect
      .soft(builder.liveRegion)
      .toContainText(
        'Discarded 1 unsaved change and loaded the server version.',
      );
    await expectCounts(builder, { devices: 1 });
    await builder.waitSaved();

    // The reloaded editor holds the new ETag, so the next edit saves.
    await addDevices(builder, 1);
    await expectServerCounts(builder, draft, { devices: 2 });
    await builder.waitSaved();
    expect((await builder.serverDocument(draft)).name).toBe(elsewhere);

    await test.step('a conflict raised while typing leaves focus and text in the name field', async () => {
      await writeElsewhere(builder.request, draft, (doc) => ({
        ...doc,
        description: 'Changed again',
      }));
      const name = builder.page.getByTestId('builder-name');
      await name.focus();
      await name.press('End');
      // Enter commits "-a", whose save meets the conflict; typing goes on.
      await name.pressSequentially('-a');
      await name.press('Enter');
      await name.pressSequentially('bc');
      await expect(banner).toBeVisible();
      await expect.soft(name).toBeFocused();
      await name.pressSequentially('d');
      await expect.soft(name).toHaveValue(`${elsewhere}-abcd`);
    });

    expectNoFatal(issues);
  });

  test('Save my history as a new draft forks the local history', async ({
    builder,
    issues,
  }, testInfo) => {
    const { draft, fork, title } = await forkAfterConflict(builder, testInfo);

    await expect(builder.page.getByTestId('builder-conflict')).toHaveCount(0);
    await builder.waitSaved();
    await expect(builder.liveRegion).toHaveText(
      /Saved your local history as a new draft/,
    );
    await expectCounts(builder, { devices: 2 });

    await test.step('the fork is titled as a local copy (N8)', async () => {
      expect((await builder.serverDraft(draft)).title).toBe(title);
      expect((await builder.serverDraft(fork)).title).toBe(
        `${title} (local copy)`,
      );
    });

    // The new draft replays the history on screen: the diagram as it was
    // opened (no edit to name), the rename, then two devices (R39).
    await expectServerCounts(builder, fork, { devices: 2 });
    const summaries = (await listSnapshots(builder.request, fork)).map(
      (snapshot) => snapshot.summary || '',
    );
    expect(summaries).toEqual([
      '',
      'Renamed diagram',
      'Added device',
      'Added device',
    ]);

    // The conflicting draft keeps the other editor's version.
    const original = await builder.serverDocument(draft);
    expect(original.description).toBe('Changed elsewhere');
    expect(countKind(original, 'device')).toBe(1);

    await test.step('undo and redo move the new draft’s cursor (R39)', async () => {
      await builder.toolbar('undo').click();
      await expectCounts(builder, { devices: 1 });
      await expectServerCounts(builder, fork, { devices: 1 });
      await builder.waitSaved();
      await builder.toolbar('redo').click();
      await expectServerCounts(builder, fork, { devices: 2 });
      await builder.waitSaved();
      expect(countKind(await builder.serverDocument(draft), 'device')).toBe(1);
    });

    // Editing continues on the new draft.
    await addDevices(builder, 1);
    await expectServerCounts(builder, fork, { devices: 3 });
    await builder.waitSaved();
    expect(countKind(await builder.serverDocument(draft), 'device')).toBe(1);

    await builder.backToDrafts();
    await builder.page.getByTestId('drafts-tab-mine').click();
    await expect(
      builder.page
        .getByTestId('drafts-list-mine')
        .getByTestId(`draft-open-${fork.id}`),
    ).toBeVisible();

    expectNoFatal(issues);
  });

  test('toolbar Undo and Redo follow the history and move the server cursor (R14)', async ({
    builder,
    page,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();
    await expect.soft(builder.toolbar('undo')).toBeDisabled();
    await expect.soft(builder.toolbar('redo')).toBeDisabled();

    // Hard: the clicks below must act on an enabled button.
    await addDevices(builder, 3);
    await expect(builder.toolbar('undo')).toBeEnabled();
    await expect.soft(builder.toolbar('redo')).toBeDisabled();

    await builder.toolbar('undo').click();
    await expectCounts(builder, { devices: 2 });
    await builder.toolbar('undo').click();
    await expectCounts(builder, { devices: 1 });
    await expectServerCounts(builder, draft, { devices: 1 });

    await expect(builder.toolbar('redo')).toBeEnabled();
    await builder.toolbar('redo').click();
    await expectCounts(builder, { devices: 2 });
    await expectServerCounts(builder, draft, { devices: 2 });

    // The shortcuts work outside the canvas too, as the toolbar's
    // aria-keyshortcuts promise (R91): here from a palette button.
    await builder.palette('device').focus();
    await page.keyboard.press('ControlOrMeta+z');
    await expectCounts(builder, { devices: 1 });
    await page.keyboard.press('ControlOrMeta+Shift+z');
    await expectCounts(builder, { devices: 2 });
    await expectServerCounts(builder, draft, { devices: 2 });

    // The toolbar names this platform's keys, and only those work: ⌘ on
    // macOS, Ctrl elsewhere.
    const mac = process.platform === 'darwin';
    await expect
      .soft(builder.toolbar('undo'))
      .toHaveAttribute('aria-keyshortcuts', mac ? 'Meta+Z' : 'Control+Z');
    await expect
      .soft(builder.toolbar('redo'))
      .toHaveAttribute(
        'aria-keyshortcuts',
        mac ? 'Shift+Meta+Z' : 'Control+Shift+Z Control+Y',
      );
    await builder.toolbar('undo').hover();
    await expect
      .soft(page.getByTestId('toolbar-tooltip'))
      .toHaveText(mac ? 'Undo (⌘Z)' : 'Undo (Ctrl+Z)');
    await builder.palette('device').focus();
    await page.keyboard.press(mac ? 'Control+z' : 'Meta+z');
    await expectCounts(builder, { devices: 2 });

    // Save now and the command palette answer in a text field too. Save
    // now saves the name being typed, which the field commits only on
    // change.
    const name = page.getByTestId('builder-name');
    await name.fill('Saved by its key');
    await page.keyboard.press('ControlOrMeta+s');
    await expect.soft(builder.liveRegion).toContainText('All changes saved');
    await expect.soft
      .poll(async () => (await builder.serverDocument(draft)).name)
      .toBe('Saved by its key');
    await expect.soft(name).toBeFocused();
    // With nothing left to save, the key sends nothing, so it makes no
    // snapshot, and says so.
    await builder.waitSaved();
    const sent = [];
    const onRequest = (request) => {
      if (request.method() !== 'GET') {
        sent.push(request.url());
      }
    };
    page.on('request', onRequest);
    await page.keyboard.press('ControlOrMeta+s');
    await expect.soft(builder.liveRegion).toContainText('No changes to save.');
    page.off('request', onRequest);
    expect.soft(sent, 'requests for nothing to save').toEqual([]);
    await page.keyboard.press('ControlOrMeta+k');
    await expect.soft(page.getByTestId('commands-dialog')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect.soft(name).toBeFocused();
  });

  test('History entries are readable: no raw ids, no double numbering (V4)', async ({
    builder,
  }) => {
    await builder.open();
    await builder.createBlank();
    await addDevices(builder, 1);
    await builder.waitSaved();

    await builder.openDialog('history');
    const list = builder.page.getByTestId('history-list');
    const buttons = list.getByRole('button');
    await expect(buttons).toHaveCount(2);

    // Soft, so a fix for one of the two problems still reports the other.
    const listNumbered = await list.evaluate(
      (element) => getComputedStyle(element).listStyleType !== 'none',
    );
    const entries = await buttons.evaluateAll((elements) =>
      elements.map((element) => ({
        text: element.textContent.trim(),
        label: element.getAttribute('aria-label') || '',
      })),
    );

    for (const { text, label } of entries) {
      expect.soft(text, `"${text}" shows a raw id`).not.toMatch(UUID);
      expect.soft(label, `"${label}" reads a raw id`).not.toMatch(UUID);
      expect
        .soft(
          listNumbered && /^\d+\./.test(text),
          `"${text}" repeats the list's own numbering`,
        )
        .toBe(false);
    }

    // The first snapshot is the draft as created.
    await expect
      .soft(buttons.first())
      .toHaveText(/^\s*Restore Draft created, /);
    // The name is the visible text, so it adds no number of its own.
    await expect
      .soft(buttons.first())
      .toHaveAccessibleName(/^Restore Draft created, /);
    // "Restore" says what an entry does; a note that restoring replaces the
    // diagram only repeated it.
    await expect.soft(buttons.first()).toHaveAccessibleDescription('');
    await expect
      .soft(builder.dialog)
      .not.toContainText('Restoring a snapshot replaces the diagram');

    // Entries made in the same second share a name, so the list number is
    // what tells them apart, and the dialog must not clip it. The room
    // before an entry holds a three-digit number: the server keeps 50
    // snapshots, but a draft saved before that limit was lowered holds 100.
    const marker = await list.evaluate((element) => {
      const dialog = element.closest('dialog');
      const item = element.querySelector('li');
      const probe = document.createElement('span');
      probe.textContent = '100. ';
      probe.style.cssText =
        'position: absolute; white-space: pre; font-variant-numeric: tabular-nums';
      item.append(probe);
      const width = probe.getBoundingClientRect().width;
      probe.remove();

      return {
        width,
        room:
          item.getBoundingClientRect().left -
          dialog.getBoundingClientRect().left -
          dialog.clientLeft,
      };
    });
    expect
      .soft(marker.room, 'room before an entry for its list number "100."')
      .toBeGreaterThanOrEqual(marker.width);
  });

  test('edits made offline survive a reload (R12)', async ({
    builder,
    page,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();

    await page.route(DRAFT_ROUTES, (route) => route.abort());
    await addDevices(builder, 2);
    await expect(builder.saveState).toHaveText(
      /Offline: 2 changes kept on this device/,
    );

    // Each edit's snapshot is a record of its own, written once; the draft
    // record keeps the queue and no snapshot (R88).
    const local = await localDrafts(page);
    expect(local.drafts).toHaveLength(1);
    expect(local.drafts[0].queue).toHaveLength(2);
    expect(
      local.drafts[0].entries.filter((entry) => 'snapshot' in entry),
    ).toEqual([]);
    expect(local.entries).toBe(2);

    // Reload while the draft routes still fail, so nothing reaches the
    // server before the page is gone. The browser asks first (R57).
    const prompts = [];
    page.once('dialog', (dialog) => {
      prompts.push(dialog.type());
      dialog.accept().catch(() => {});
    });
    await page.reload();
    expect.soft(prompts, 'prompt before the reload').toEqual(['beforeunload']);
    await expect(
      page.getByRole('heading', { name: 'Builder Flow' }),
    ).toBeVisible({ timeout: 20000 });
    await page.unroute(DRAFT_ROUTES);
    await expectServerCounts(builder, draft, { devices: 0 });

    await page.getByTestId(`draft-open-${draft.id}`).click();
    await expect(builder.canvas).toBeVisible();
    await expectCounts(builder, { devices: 2 });
    await builder.waitSaved();
    await expectServerCounts(builder, draft, { devices: 2 });

    // Once saved, nothing of the draft is left on this device (R86).
    await expect
      .poll(() => localDrafts(page))
      .toEqual({ drafts: [], entries: 0 });

    await test.step('a save whose answer never came is not a conflict after a reload (R84)', async () => {
      // The server stores the snapshot, but the page is gone before the
      // answer arrives, so the edit is still queued on this device.
      const stored = [];
      await page.route(SNAPSHOTS, async (route) => {
        if (route.request().method() !== 'POST') {
          return route.fallback();
        }

        stored.push(route.request().postDataJSON().opId);
        await route.fetch();
      });
      await addDevices(builder, 1);
      await expectServerCounts(builder, draft, { devices: 3 });
      expect((await localDrafts(page)).drafts).toHaveLength(1);

      page.once('dialog', (dialog) => dialog.accept().catch(() => {}));
      await page.unroute(SNAPSHOTS);
      await page.reload();
      const appends = [];
      page.on('request', (request) => {
        if (
          request.method() === 'POST' &&
          request.url().endsWith('/snapshots')
        ) {
          appends.push(request.url());
        }
      });
      await page.getByTestId(`draft-open-${draft.id}`).click();
      await expectCounts(builder, { devices: 3 });
      await builder.waitSaved();

      await expect(page.getByTestId('builder-conflict')).toHaveCount(0);
      expect(appends, 'snapshots sent again').toEqual([]);
      expect((await listSnapshots(builder.request, draft)).at(-1).opId).toBe(
        stored[0],
      );
      await expect
        .poll(() => localDrafts(page))
        .toEqual({ drafts: [], entries: 0 });
    });

    await test.step('an undo made offline is what reopens after a reload (R83)', async () => {
      await addDevices(builder, 1);
      await expectServerCounts(builder, draft, { devices: 4 });
      await builder.waitSaved();

      await page.route(DRAFT_ROUTES, (route) => route.abort());
      await builder.toolbar('undo').click();
      await expectCounts(builder, { devices: 3 });
      await expect(builder.saveState).toHaveText(
        /Offline: 1 change kept on this device/,
      );

      page.once('dialog', (dialog) => dialog.accept().catch(() => {}));
      await page.reload();
      await expect(
        page.getByRole('heading', { name: 'Builder Flow' }),
      ).toBeVisible({ timeout: 20000 });
      await page.unroute(DRAFT_ROUTES);
      await page.getByTestId(`draft-open-${draft.id}`).click();

      // The screen shows the undo, the server's cursor follows it, and the
      // undone edit can be redone.
      await expectCounts(builder, { devices: 3 });
      await builder.waitSaved();
      await expectServerCounts(builder, draft, { devices: 3 });
      await expect.soft(builder.toolbar('redo')).toBeEnabled();
    });
  });

  test('a snapshot the server rejects does not block later saves (R13)', async ({
    builder,
    page,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();
    await addDevices(builder, 1);
    await expectServerCounts(builder, draft, { devices: 1 });

    async function renameDevice(from, to) {
      const item = builder.outlineItem(from);
      await item.focus();
      await item.press('F2');
      const input = page.getByRole('textbox', { name: `Rename ${from}` });
      await input.fill(to);
      await input.press('Enter');
    }

    // The server answers 422 to every upload of a document that holds the
    // rejected hostname, as it does for a hostname with whitespace. The
    // rejection is keyed on the document, not on the first request, so the
    // automatic retry of that snapshot is rejected too. A valid hostname is
    // used so a client-side hostname check cannot stop the edit from being
    // sent, which would hide the defect instead of fixing it.
    const REJECTED = 'rejected-host';
    await page.route(SNAPSHOTS, (route) => {
      const body = route.request().postDataJSON() || {};
      const nodes = body.document?.nodes || [];
      const rejectedHost = nodes.some(
        (node) => node.device?.hostname === REJECTED,
      );

      return route.request().method() === 'POST' && rejectedHost
        ? route.fulfill({
            status: 422,
            contentType: 'application/json',
            body: JSON.stringify({
              message: `unable to save builder draft ${draft.id}`,
              cause: `hostname ${REJECTED} is not allowed`,
            }),
          })
        : route.fallback();
    });

    const rejected = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().endsWith('/snapshots') &&
        response.status() === 422,
    );
    await renameDevice('node', REJECTED);
    await rejected;
    await expect(builder.saveState).toHaveClass(/builder-status--error/);
    // Sending the same snapshot again cannot succeed, so Retry saving is not
    // offered; the status says what to change instead.
    await expect.soft(builder.toolbar('retry')).toHaveCount(0);
    // The status gives the server's reason and what to do, never the id
    // from the server's message (A14), and it is announced.
    await expect
      .soft(builder.saveState)
      .toContainText(
        `Could not save your last change. Hostname ${REJECTED} is not allowed. Fix the diagram and it saves again.`,
      );
    await expect.soft(builder.saveState).not.toHaveText(UUID);
    await expect
      .soft(builder.liveRegion)
      .toContainText(`Hostname ${REJECTED} is not allowed.`);

    // The user fixes the name. That valid edit must reach the server.
    await renameDevice(REJECTED, 'web-server');
    await expect(deviceNode(builder, 'web-server')).toBeVisible();
    await expect(builder.saveState).toContainText(SAVED);
    await expect
      .poll(
        async () =>
          (await builder.serverDocument(draft)).nodes.find(
            (node) => node.kind === 'device',
          )?.device?.hostname,
      )
      .toBe('web-server');
  });

  test('Ctrl+Z restores a node deleted with the Delete key', async ({
    builder,
  }) => {
    await builder.open();
    await builder.createBlank();
    await addDevices(builder, 1);

    // The palette selects the new device; focus it and delete it. Focus
    // stays in the canvas, where Ctrl+Z is handled.
    await deviceNode(builder, 'node').press('Delete');
    await expectCounts(builder, { devices: 0 });
    await expect(builder.canvas).toBeFocused();

    await builder.page.keyboard.press('ControlOrMeta+z');
    await expectCounts(builder, { devices: 1 });
  });

  test('leaving the editor with unsaved changes asks first, and logging out clears them (R57, R86, R87)', async ({
    builder,
    page,
  }) => {
    // With a blank draft listed, the new one is numbered apart from it (V10).
    await builder.seedDraft(blankDocument('Untitled topology'));
    await builder.open();
    const mine = page.getByTestId('drafts-list-mine');
    await expect(
      mine.getByRole('heading', { name: 'Untitled topology', exact: true }),
    ).not.toHaveCount(0);
    const listed = (await mine.getByRole('heading').allTextContents()).map(
      (text) => text.trim(),
    );

    // A double click on Blank diagram makes one draft (R95).
    const creates = [];
    const onRequest = (request) => {
      if (
        request.method() === 'POST' &&
        new URL(request.url()).pathname === `${API}/builder/drafts`
      ) {
        creates.push(request.url());
      }
    };
    page.on('request', onRequest);
    const created = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === `${API}/builder/drafts`,
    );
    await page.getByTestId('drafts-blank').dblclick();
    const draft = await (await created).json();
    await expect(builder.canvas).toBeVisible();
    await builder.waitSaved();
    page.off('request', onRequest);
    expect.soft(creates, 'drafts made by a double click').toHaveLength(1);
    const title = await page.getByTestId('builder-name').inputValue();
    expect.soft(title).toMatch(/^Untitled topology \d+$/);
    expect.soft(listed, 'titles listed before').not.toContain(title);

    await page.route(DRAFT_ROUTES, (route) => route.abort());
    await addDevices(builder, 1);
    await expect(builder.saveState).toHaveText(
      /Offline: 1 change kept on this device/,
    );

    const back = page.getByRole('button', { name: 'Back to drafts' });
    const confirm = page.getByRole('alertdialog', {
      name: 'Leave with unsaved changes?',
    });

    await test.step('Back to drafts asks, and Stay keeps the editor', async () => {
      await back.click();
      await expect(confirm).toBeVisible();
      await expect
        .soft(confirm)
        .toContainText('Your 1 unsaved change is kept on this device.');
      // Focus starts on the choice that keeps the work.
      await expect
        .soft(confirm.getByRole('button', { name: 'Stay' }))
        .toBeFocused();
      await confirm.getByRole('button', { name: 'Stay' }).click();
      await expect(confirm).toBeHidden();
      await expect(builder.canvas).toBeVisible();
      await expect.soft(back, 'focus after Stay').toBeFocused();
    });

    await test.step('another page asks too, and Leave anyway goes there', async () => {
      await page.getByRole('link', { name: 'Experiments' }).click();
      await expect(confirm).toBeVisible();
      await expect.soft(page).toHaveURL(/\/builder-beta$/);
      await confirm.getByRole('button', { name: 'Leave anyway' }).click();
      await expect(page).toHaveURL(/\/experiments$/);
    });

    await test.step('logging out, with the Builder closed, clears what it kept here but the preferences', async () => {
      const kept = () =>
        page.evaluate(
          () =>
            new Promise((resolve) => {
              const keys = Object.keys(localStorage)
                .filter((key) => key.startsWith('phenix.builder.'))
                .sort();
              const request = indexedDB.open('phenix-builder');
              request.onsuccess = () => {
                const db = request.result;
                const count = db
                  .transaction('drafts')
                  .objectStore('drafts')
                  .count();
                count.onsuccess = () => {
                  db.close();
                  resolve({ keys, drafts: count.result });
                };
              };
            }),
        );
      // Preferences, and recent commands, which name drafts by id.
      await page.evaluate(() => {
        localStorage.setItem('phenix.builder.theme', 'dark');
        localStorage.setItem(
          'phenix.builder.settings',
          '{"showMinimap":false}',
        );
        localStorage.setItem(
          'phenix.builder.shortcuts',
          '{"keys":{"palette.open":["Mod+J"]},"singleKeys":true}',
        );
        localStorage.setItem(
          'phenix.builder.recentCommands',
          '[{"id":"drafts.open","choices":["My Drafts:admin:d1"]}]',
        );
      });
      expect(await kept()).toEqual({
        keys: [
          'phenix.builder.recentCommands',
          'phenix.builder.settings',
          'phenix.builder.shortcuts',
          'phenix.builder.theme',
        ],
        drafts: 1,
      });

      // A server without authentication refuses GET /logout, which the
      // header's Logout asks first.
      await page.route('**/api/v1/logout', (route) =>
        route.fulfill({ status: 204 }),
      );
      await page.locator('.navbar-item', { hasText: 'Logout' }).click();
      await expect(page).toHaveURL(/\/signin$/);
      await expect.poll(kept).toEqual({
        keys: [
          'phenix.builder.settings',
          'phenix.builder.shortcuts',
          'phenix.builder.theme',
        ],
        drafts: 0,
      });
    });

    await test.step('the draft reopens as the server has it, with the preferences kept', async () => {
      await page.unroute(DRAFT_ROUTES);
      await builder.openDraft(draft);
      await expectCounts(builder, { devices: 0 });
      await expect.soft(builder.liveRegion).not.toContainText('Recovered');
      await expect
        .soft(page.locator('.builder-root'))
        .toHaveAttribute('data-builder-theme', 'dark');
      await expect
        .soft(page.getByTestId('toolbar-commands'))
        .toHaveAttribute('aria-keyshortcuts', /\+J$/);
      await expect
        .soft(page.getByTestId('toolbar-minimap'))
        .toHaveAttribute('aria-pressed', 'false');
    });

    await test.step('a save that lands while it asks leaves at once', async () => {
      // The save takes longer than leaving waits for it before it asks.
      await page.route(
        (url) => url.pathname.endsWith('/snapshots'),
        async (route) => {
          await new Promise((resolve) => setTimeout(resolve, 3500));
          await route.continue();
        },
      );
      await addDevices(builder, 1);
      await back.click();
      await expect(confirm).toBeVisible();
      // Once the server has the change, there is nothing left to ask about.
      await expect(confirm).toBeHidden({ timeout: 10000 });
      await expect(page.getByTestId('drafts-list-mine')).toBeVisible();
      await expectServerCounts(builder, draft, { devices: 1 });
    });
  });

  test('a ?topology= link edits one draft of the diagram, which a reload reopens (R42, R54, R71)', async ({
    builder,
    page,
    tracker,
  }, testInfo) => {
    const target = uniqueName(testInfo, 'deeplink');
    const { documentId } = await publishTopology(
      builder.request,
      tracker,
      target,
    );
    const opened = async () =>
      (await listMine(builder.request)).filter(
        (item) => item.sourceToken === `builder-doc/${documentId}`,
      );
    const deepLink = `/builder-beta?topology=${encodeURIComponent(target)}`;

    await visit(page, deepLink);
    await expect(builder.canvas).toBeVisible({ timeout: 20000 });
    await builder.waitSaved();
    const [draft, ...others] = await opened();
    expect(others).toEqual([]);
    // The address names the draft now, not the published diagram.
    await expect
      .soft(page)
      .toHaveURL(
        (url) => url.searchParams.get('draft') === `${draft.owner}/${draft.id}`,
      );

    await addDevices(builder, 1);
    await expectServerCounts(builder, draft, { devices: 1 });
    await builder.waitSaved();

    await test.step('a reload reopens the draft, edits and all', async () => {
      await page.reload();
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await builder.waitSaved();
      await expectCounts(builder, { devices: 1 });
    });

    await test.step('the link from Configs opens the same draft again', async () => {
      await visit(page, deepLink);
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await builder.waitSaved();
      await expectCounts(builder, { devices: 1 });
      await expect
        .soft(builder.liveRegion)
        .toContainText(
          `Opened topology ${target} in Builder Flow, in your draft of it.`,
        );
      expect
        .soft(await opened(), 'drafts opened from the published document')
        .toHaveLength(1);
    });

    // Back to drafts drops the draft from the address.
    await builder.backToDrafts();
    await expect.soft(page).toHaveURL(/\/builder-beta$/);
  });
});
