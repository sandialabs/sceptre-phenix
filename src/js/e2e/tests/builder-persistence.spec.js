// Builder persistence: undo and redo, Draft History, autosave, conflicts and
// local recovery, checked both on screen and in the server's copy.

const {
  test,
  expect,
  blankDocument,
  expectAccessible,
  expectNoFatal,
  uniqueName,
  visit,
  draftPath,
  publishTopology,
  API,
  BuilderPage,
  waitForApi,
} = require('./builder-support');

const SNAPSHOTS = '**/api/v1/builder/drafts/*/snapshots';
const DRAFT_ROUTES = '**/api/v1/builder/drafts/**';
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

async function listDrafts(request) {
  const response = await request.get(`${API}/builder/drafts`);
  expect(response.ok(), await response.text()).toBeTruthy();

  return (await response.json()).drafts || [];
}

function countKind(doc, kind) {
  return (doc?.nodes || []).filter((node) => node.kind === kind).length;
}

// Waits until the server's current document has these node counts. "All
// changes saved" can show before the next save starts.
async function expectServerCounts(
  builder,
  draft,
  { devices = 0, switches = 0 },
) {
  await builder.persisted(
    draft,
    (doc) => ({
      devices: countKind(doc, 'device'),
      switches: countKind(doc, 'switch'),
    }),
    { devices, switches },
    { message: 'server document node counts' },
  );
}

// Adds devices from the palette to a diagram without switches. Click-added
// devices are named node, node-2, node-3, ... in order.
async function addDevices(builder, count) {
  const start = Number(
    /(\d+) devices?\b/.exec(await builder.summary.textContent())[1],
  );

  for (let index = 1; index <= count; index += 1) {
    await builder.palette('device').click();
    await builder.expectCounts({ devices: start + index });
  }
}

// The Inspector's Memory field, of the device it shows.
function memoryField(builder) {
  return builder.inspector
    .locator('legend.group-label', { hasText: /^Hardware$/ })
    .locator('xpath=..')
    .getByLabel('Memory', { exact: true });
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

// The document with `fields` written into its metadata, as another editor
// renaming or describing the diagram would.
function withMetadata(doc, fields) {
  return { ...doc, metadata: { ...doc.metadata, ...fields } };
}

// The first device renamed to the hostname the page gives its next device,
// so the page's next save meets a conflict that cannot be merged.
function withNextHostname(doc) {
  const devices = doc.nodes.filter((node) => node.kind === 'device');
  const taken = new Set(devices.map((node) => node.device.hostname));
  let hostname = 'node';

  for (let index = 2; taken.has(hostname); index += 1) {
    hostname = `node-${index}`;
  }

  return {
    ...doc,
    nodes: doc.nodes.map((node) =>
      node === devices[0]
        ? {
            ...node,
            label: hostname,
            device: {
              ...node.device,
              hostname,
              spec: {
                ...node.device.spec,
                general: { ...node.device.spec.general, hostname },
              },
            },
          }
        : node,
    ),
  };
}

// Freezes timers so the retry backoff never sends on its own. It needs
// page.clock.install() before the page opened.
async function freezeTimers(page) {
  await page.clock.pauseAt(Date.now() + 1000);
}

// Makes a draft another editor changed and published, meets the conflict and
// forks it. Returns the original draft, the fork and the title.
async function forkAfterConflict(builder, testInfo) {
  await builder.open();
  const draft = await builder.createBlank();

  const title = uniqueName(testInfo, 'fork');
  await builder.rename(title);
  await addDevices(builder, 1);
  await expectServerCounts(builder, draft, { devices: 1 });
  await builder.waitSaved();

  await writeElsewhere(builder.request, draft, (doc) =>
    withNextHostname(withMetadata(doc, { description: 'Changed elsewhere' })),
  );
  builder.tracker.config('Topology', title);
  const current = await builder.request.get(draftPath(draft));
  const published = await builder.request.post(`${draftPath(draft)}/publish`, {
    headers: { 'If-Match': current.headers().etag },
    data: { mode: 'topology', topology: { name: title, action: 'create' } },
  });
  expect(published.ok(), await published.text()).toBeTruthy();

  await addDevices(builder, 1);
  await expect(builder.page.getByTestId('builder-conflict')).toBeVisible();

  // The fork's snapshots are held, so the edits below land while the new
  // draft is being written.
  let release;
  const held = new Promise((resolve) => {
    release = resolve;
  });
  await builder.page.route(
    (url) => url.pathname.endsWith('/snapshots'),
    async (route) => {
      await held;
      await route.continue();
    },
  );

  const forked = waitForApi(builder.page, 'POST', '/builder/drafts');
  await builder.page.getByTestId('conflict-fork').click();
  const response = await forked;
  const body = await response.json();
  expect(body.id).not.toBe(draft.id);
  // The fork gets what the draft published but not its source token, so the
  // published diagram still opens the draft that published it.
  expect.soft(response.request().postDataJSON().forkOf).toBe(draft.id);
  expect.soft(body.forked?.documentId).toBeTruthy();
  expect.soft(body.sourceToken || '').not.toMatch(/^builder-doc\//);

  // An edit while the history is saved is refused and said so, instead of
  // being left out of the new draft.
  const refused =
    'Not changed: the conflict is not resolved yet. Edit again after you resolve it.';
  await builder.palette('device').click();
  await expect.soft(builder).toHaveAnnounced(refused);

  // A refused drag draws the node back where the draft has it.
  const node = builder.page.locator('.vue-flow__node').first();
  const drawn = await node.evaluate(
    (element) => getComputedStyle(element).transform,
  );
  const box = await node.boundingBox();
  await builder.page.mouse.move(box.x + 30, box.y + 14);
  await builder.page.mouse.down();
  await builder.page.mouse.move(box.x + 90, box.y + 54, { steps: 8 });
  await builder.page.mouse.up();
  await expect.soft(builder).toHaveAnnounced(refused);
  await expect.soft(node).toHaveCSS('transform', drawn);
  release();

  return { draft, fork: { id: body.id }, title };
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

// The drafts whose unload copy this browser keeps in localStorage: what a
// page left before IndexedDB stored it.
function unloadCopies(page) {
  return page.evaluate(() =>
    Object.keys(localStorage).filter((key) =>
      key.startsWith('phenix.builder.unload.'),
    ),
  );
}

test.describe('Builder persistence', () => {
  test('undo and redo by key and toolbar move the server cursor, a new edit drops the redo branch, and an undo survives a reload', async ({
    builder,
    issues,
    page,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();
    await expect.soft(builder.toolbar('undo')).toBeDisabled();
    await expect.soft(builder.toolbar('redo')).toBeDisabled();

    await addDevices(builder, 3);
    await expectServerCounts(builder, draft, { devices: 3 });
    await expect(builder.toolbar('undo')).toBeEnabled();
    await expect.soft(builder.toolbar('redo')).toBeDisabled();

    // The first device stays on the canvas, so focus stays where the
    // shortcuts are handled.
    const first = deviceNode(builder, 'node');

    await test.step('Ctrl+Z, Ctrl+Shift+Z and Ctrl+Y update the server document', async () => {
      await first.press('ControlOrMeta+z');
      await builder.expectCounts({ devices: 2 });
      await expect.soft(builder).toHaveAnnounced(/Undid Added device\./);
      await first.press('ControlOrMeta+z');
      await builder.expectCounts({ devices: 1 });
      await expectServerCounts(builder, draft, { devices: 1 });

      await first.press('ControlOrMeta+Shift+z');
      await builder.expectCounts({ devices: 2 });
      await expect.soft(builder).toHaveAnnounced(/Redid Added device\./);
      await expectServerCounts(builder, draft, { devices: 2 });

      // Ctrl+Y redoes on Windows and Linux. macOS has only ⇧⌘Z.
      await first.press(
        process.platform === 'darwin' ? 'Meta+Shift+z' : 'Control+y',
      );
      await builder.expectCounts({ devices: 3 });
      await expectServerCounts(builder, draft, { devices: 3 });
      await builder.waitSaved();

      // Undo and redo move the cursor. They never add snapshots.
      const snapshots = await listSnapshots(builder.request, draft);
      expect.soft(snapshots).toHaveLength(4);
      expect.soft(snapshots.at(-1)?.current).toBe(true);
    });

    await test.step('toolbar Undo and Redo follow the history', async () => {
      await builder.toolbar('undo').click();
      await builder.expectCounts({ devices: 2 });
      await builder.toolbar('undo').click();
      await builder.expectCounts({ devices: 1 });
      await expectServerCounts(builder, draft, { devices: 1 });

      await expect(builder.toolbar('redo')).toBeEnabled();
      await builder.toolbar('redo').click();
      await builder.expectCounts({ devices: 2 });
      await builder.toolbar('redo').click();
      await builder.expectCounts({ devices: 3 });
      await expectServerCounts(builder, draft, { devices: 3 });
      await builder.waitSaved();
    });

    await test.step('a new edit after undo drops the redo branch', async () => {
      await first.press('ControlOrMeta+z');
      await builder.expectCounts({ devices: 2 });
      await expectServerCounts(builder, draft, { devices: 2 });

      await builder.palette('switch').click();
      await builder.expectCounts({ devices: 2, switches: 1, networks: 1 });
      await expectServerCounts(builder, draft, { devices: 2, switches: 1 });

      // The new edit dropped the redo branch: Redo is unavailable, and the
      // keyboard redo below finds nothing to redo.
      await expect.soft(builder.toolbar('redo')).toBeDisabled();
      await first.press('ControlOrMeta+Shift+z');
      await expect.soft(builder).toHaveAnnounced(/Nothing to redo\./);
      await builder.expectCounts({ devices: 2, switches: 1, networks: 1 });
      await builder.waitSaved();

      // The switch replaced the undone snapshot. Creation comes first.
      const snapshots = await listSnapshots(builder.request, draft);
      expect.soft(snapshots).toHaveLength(4);
      expect
        .soft(snapshots.slice(1).map((snapshot) => snapshot.summary))
        .toEqual(['Added device', 'Added device', 'Added switch']);
      expect.soft((await builder.serverDraft(draft)).canRedo).toBe(false);
    });

    await test.step('an undo is stored as the draft cursor', async () => {
      await first.press('ControlOrMeta+z');
      await builder.expectCounts({ devices: 2, switches: 0 });
      await expectServerCounts(builder, draft, { devices: 2, switches: 0 });
      await builder.waitSaved();

      const server = await builder.serverDraft(draft);
      expect.soft(server.canRedo).toBe(true);
      expect.soft(server.cursor).toBe(2);
    });

    await test.step("the keys work outside the canvas and in the name field, and only this platform's", async () => {
      // The shortcuts work outside the canvas too, as the toolbar's
      // aria-keyshortcuts promise: here from a palette button.
      await builder.palette('device').focus();
      await page.keyboard.press('ControlOrMeta+z');
      await builder.expectCounts({ devices: 1 });
      await page.keyboard.press('ControlOrMeta+Shift+z');
      await builder.expectCounts({ devices: 2 });
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
      await builder.expectCounts({ devices: 2 });

      // Save now and the palette work in a text field too. Save now saves the
      // name being typed, as Enter does.
      const edit = page.getByTestId('builder-name-edit');
      const name = page.getByTestId('builder-name-field');
      await edit.click();
      await name.fill('Saved by its key');
      await page.keyboard.press('ControlOrMeta+s');
      await expect.soft(builder).toHaveAnnounced('All changes saved');
      await expect.soft
        .poll(async () => (await builder.serverDocument(draft)).metadata.name)
        .toBe('Saved by its key');
      await expect.soft(edit).toBeFocused();
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
      await edit.click();
      await expect.soft(name).toBeFocused();
      await page.keyboard.press('ControlOrMeta+s');
      await expect.soft(builder).toHaveAnnounced('No changes to save.');
      page.off('request', onRequest);
      expect.soft(sent, 'requests for nothing to save').toEqual([]);
      // The palette takes focus from the field, which stays open for it.
      await edit.click();
      await page.keyboard.press('ControlOrMeta+k');
      await expect.soft(page.getByTestId('commands-dialog')).toBeVisible();
      await page.keyboard.press('Escape');
      await expect.soft(name).toBeFocused();
      await page.keyboard.press('Escape');
      await expect.soft(edit).toBeFocused();
    });

    // A page load starts a new undo history, so this step comes last.
    await test.step('the undone switch stays undone after a reload', async () => {
      await builder.openDraft(draft);
      await builder.expectCounts({ devices: 2, switches: 0 });
      await expect.soft(deviceNode(builder, 'node-2')).toBeVisible();
      await expect.soft(builder.nodes('switch')).toHaveCount(0);
    });

    expectNoFatal(issues);
  });

  test('Draft History lists the server snapshots and restores one', async ({
    builder,
    issues,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();

    await addDevices(builder, 1);
    await builder.palette('switch').click();
    await builder.expectCounts({ devices: 1, switches: 1, networks: 1 });
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

    // Draft History opens at once, while the list is read: the list's place
    // is busy, and the status, which describes the dialog, says it loads.
    const dialog = await builder.openDialog('history');
    const status = page.getByTestId('history-status');
    const content = page.getByTestId('history-content');
    await expect(page.getByTestId('history-dialog')).toBeVisible();
    await expect(dialog.getByRole('heading')).toHaveText('Draft History');
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

    const table = page.getByTestId('history-table');
    await expect(table.getByTestId('history-row')).toHaveCount(3);
    await expect.soft(content).not.toHaveAttribute('aria-busy');
    await expect
      .soft(dialog)
      .toHaveAccessibleDescription('3 snapshots, newest first.');
    await expect(
      table.getByRole('button', { name: /^Restore Added switch, / }),
    ).toBeVisible();

    // Clicking a snapshot's name restores it, as its Restore does.
    await table
      .getByTestId('history-name')
      .filter({ hasText: 'Added device' })
      .click();
    await expect(builder.page.getByTestId('history-dialog')).toHaveCount(0);
    await builder.expectCounts({ devices: 1, switches: 0 });
    await expect(builder).toHaveAnnounced(/Snapshot restored/);
    await expectServerCounts(builder, draft, { devices: 1, switches: 0 });
    await builder.waitSaved();

    // Restoring moves the cursor. The later snapshot is still in history.
    const snapshots = await listSnapshots(builder.request, draft);
    expect(snapshots).toHaveLength(3);
    expect(snapshots.map((snapshot) => snapshot.current)).toEqual([
      false,
      true,
      false,
    ]);

    // The restored document is the one a fresh page load opens.
    await builder.openDraft(draft);
    await builder.expectCounts({ devices: 1, switches: 0 });

    await test.step('a notice says what the next change removes, and Return to latest shows the newest snapshot again', async () => {
      const notice = page.getByTestId('builder-newer-history');
      await expect(page.getByTestId('newer-history-text')).toHaveText(
        /^Your next change removes 1 newer snapshot \(latest by .+, .+\)\.$/,
      );
      await expect(builder).toHaveAnnounced('Your next change removes 1 newer');
      await expectAccessible(page, {
        include: '[data-testid="builder-newer-history"]',
        label: 'Newer snapshots notice',
      });
      await page.getByTestId('newer-history-latest').click();
      await expect(notice).toHaveCount(0);
      await builder.expectCounts({ devices: 1, switches: 1, networks: 1 });
      await builder.waitSaved();
      const latest = await listSnapshots(builder.request, draft);
      expect(latest.map((snapshot) => snapshot.current)).toEqual([
        false,
        false,
        true,
      ]);
    });

    expectNoFatal(issues);
  });

  // One draft walks through every autosave state. Each step empties the queue,
  // so its change counts are its own, while server counts add up.
  test('autosave reports queued, offline and failed saves, and Save now’s key and Retry saving upload them', async ({
    builder,
    issues,
    page,
  }) => {
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

      // The toolbar keeps its rows as the save state changes, so the
      // canvas below it stays put.
      const toolbar = page.getByRole('toolbar', { name: 'Builder actions' });
      const height = async () => (await toolbar.boundingBox()).height;
      const before = await height();
      await builder.palette('device').click();
      await expect.soft(builder.saveState).toHaveText(/Saving changes/);
      expect.soft(await height(), 'toolbar height, saving').toBe(before);
      await builder.palette('device').click();
      await expect.soft(builder.saveState).toHaveText(/Saving 2 changes/);
      await expect.soft(builder.toolbar('retry')).toHaveCount(0);

      release();
      await builder.waitSaved();
      expect.soft(await height(), 'toolbar height, saved').toBe(before);
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
      // The pressed button is gone: focus moves to Draft History, which takes
      // the toolbar's Tab stop.
      await expect
        .soft(builder.toolbar('history'), 'focus after Retry saving')
        .toBeFocused();
      await expect
        .soft(builder.toolbar('history'))
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
      const sent = builder.nextSnapshot();
      await page.keyboard.press('ControlOrMeta+s');
      expect.soft((await sent).ok()).toBeTruthy();
      await builder.waitSaved();
      await expectServerCounts(builder, draft, { devices: 5 });
    });

    await test.step('a server error is reported and Retry saving recovers', async () => {
      // A message long enough to wrap in the toolbar.
      const failure =
        'simulated save failure: the server could not write the change to its database, and the change stays in this browser until a save succeeds';
      await page.route(SNAPSHOTS, (route) =>
        route.request().method() === 'POST'
          ? route.fulfill({
              status: 500,
              contentType: 'application/json',
              body: JSON.stringify({ message: failure }),
            })
          : route.fallback(),
      );
      await builder.palette('device').click();
      await expect
        .soft(builder.saveState)
        .toContainText(
          'Could not save your changes. Simulated save failure: the server could not write the change to its database, and the change stays in this browser until a save succeeds. Saving retries automatically.',
        );
      await expect(builder.toolbar('retry')).toBeVisible();
      // The message wraps in the toolbar, whose buttons keep one height
      // rather than growing with its row, and stays on screen.
      const heights = await page
        .locator('.builder-toolbar button')
        .evaluateAll((buttons) =>
          buttons.map((button) =>
            Math.round(button.getBoundingClientRect().height),
          ),
        );
      expect
        .soft(new Set(heights).size, `toolbar button heights ${heights}`)
        .toBe(1);
      const stateBox = await builder.saveState.boundingBox();
      expect.soft(stateBox.height, 'save state').toBeGreaterThan(heights[0]);
      expect
        .soft(stateBox.x + stateBox.width, 'save state right')
        .toBeLessThanOrEqual(page.viewportSize().width);

      await page.unroute(SNAPSHOTS);
      await builder.toolbar('retry').click();
      await builder.waitSaved();
      await expectServerCounts(builder, draft, { devices: 6 });
    });

    await test.step('a change the server refuses offers no Retry saving, and the next edit saves', async () => {
      await page.route(SNAPSHOTS, (route) =>
        route.request().method() === 'POST'
          ? route.fulfill({
              status: 422,
              contentType: 'application/json',
              body: JSON.stringify({
                message: `unable to save Builder draft ${draft.id}`,
                cause: 'the document is not valid',
              }),
            })
          : route.fallback(),
      );
      await builder.palette('device').click();
      await expect(builder.saveState).toHaveClass(/builder-status--error/);
      // No Retry saving for a refusal. The announcement waits for the edit's
      // own, which the frozen timers hold.
      await expect.soft(builder.toolbar('retry')).toHaveCount(0);
      await expect
        .soft(builder.saveState)
        .toContainText(
          'Could not save your last change. The document is not valid. When you fix the diagram, the Builder saves it again.',
        );
      await page.clock.runFor(1000);
      await expect.soft(builder).toHaveAnnounced('The document is not valid.');

      // The next edit replaces the refused one.
      await page.unroute(SNAPSHOTS);
      await builder.palette('device').click();
      await builder.waitSaved();
      await expectServerCounts(builder, draft, { devices: 8 });
    });

    // Last, since a conflict blocks every later save.
    await test.step('a conflict met by the automatic retry takes focus from Retry saving', async () => {
      await page.route(DRAFT_ROUTES, (route) => route.abort());
      await builder.palette('device').click();
      await expect(builder.toolbar('retry')).toBeVisible();
      await builder.toolbar('retry').focus();

      await writeElsewhere(builder.request, draft, (doc) =>
        withNextHostname(
          withMetadata(doc, { description: 'Changed elsewhere' }),
        ),
      );
      await page.unroute(DRAFT_ROUTES);
      // Past the first retry delay: the automatic retry, not a press,
      // sends the edit and meets the conflict.
      await page.clock.runFor(2000);

      const banner = page.getByTestId('builder-conflict');
      await expect(banner).toBeVisible();
      // The conflict panel keeps focus. Draft History only takes the
      // toolbar's Tab stop from the removed button.
      await expect(
        banner.getByRole('heading', {
          name: 'This draft changed on the server',
        }),
        'focus after the automatic retry',
      ).toBeFocused();
      await expect
        .soft(builder.toolbar('history'))
        .toHaveAttribute('tabindex', '0');
    });

    expectNoFatal(issues);
  });

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
    await writeElsewhere(builder.request, draft, (doc) =>
      withNextHostname(withMetadata(doc, { name: elsewhere })),
    );

    await addDevices(builder, 1);
    const banner = builder.page.getByTestId('builder-conflict');
    await expect(banner).toBeVisible();
    // A region with an alert, not a non-modal alertdialog. It takes focus,
    // so its two choices are the next Tab stops.
    await expect(banner).toHaveRole('region');
    await expect
      .soft(banner.getByRole('alert'))
      .toContainText('Your changes cannot replace it');
    await expect
      .soft(banner)
      .toContainText('This device keeps your 1 unsaved change.');
    await expect(
      banner.getByRole('heading', { name: 'This draft changed on the server' }),
    ).toBeFocused();
    await expect(builder.saveState).toHaveText(
      /This draft changed on the server/,
    );

    // Local work is never written over the newer server copy.
    const server = await builder.serverDocument(draft);
    expect(server.metadata.name).toBe(elsewhere);
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
    await expect(builder.page.getByTestId('builder-name')).toHaveText(
      elsewhere,
    );
    // Focus lands in the editor, not on <body>.
    await expect
      .poll(() =>
        builder.page.evaluate(() => document.activeElement !== document.body),
      )
      .toBe(true);
    await expect
      .soft(builder)
      .toHaveAnnounced(
        'Discarded 1 unsaved change and loaded the server version.',
      );
    await builder.expectCounts({ devices: 1 });
    await builder.waitSaved();

    // The reloaded editor holds the new ETag, so the next edit saves.
    await addDevices(builder, 1);
    await expectServerCounts(builder, draft, { devices: 2 });
    await builder.waitSaved();
    expect((await builder.serverDocument(draft)).metadata.name).toBe(elsewhere);

    await test.step('a conflict raised while typing leaves focus and text in the name field', async () => {
      const { page } = builder;
      await writeElsewhere(builder.request, draft, (doc) =>
        withNextHostname(withMetadata(doc, { description: 'Changed again' })),
      );
      // An edit's save, held until the name is being typed, meets the
      // conflict. Typing goes on.
      let release;
      const released = new Promise((resolve) => {
        release = resolve;
      });
      await page.route(SNAPSHOTS, async (route) => {
        await released;
        await route.fallback();
      });
      await addDevices(builder, 1);
      await page.getByTestId('builder-name-edit').click();
      const name = page.getByTestId('builder-name-field');
      await expect(name).toBeFocused();
      await name.press('End');
      await name.pressSequentially('-a');
      release();
      await expect(banner).toBeVisible();
      await expect.soft(name).toBeFocused();
      await name.pressSequentially('bc');
      await expect.soft(name).toHaveValue(`${elsewhere}-abc`);
      await page.unroute(SNAPSHOTS);
    });

    expectNoFatal(issues);
  });

  test(
    'a name both editors changed is reviewed in the merge dialog, which passes an axe scan',
    { tag: '@axe' },
    async ({ builder, issues }, testInfo) => {
      const { page } = builder;

      await builder.open();
      const draft = await builder.createBlank();
      const first = uniqueName(testInfo, 'merge');
      await builder.rename(first);
      // The save state can say all is saved before the rename's save
      // starts: the server holds the rename once it says so.
      await builder.persisted(draft, (doc) => doc.metadata.name, first);
      await builder.waitSaved();

      // Another editor renames the diagram too, and adds a note, which
      // clashes with nothing.
      const theirs = uniqueName(testInfo, 'theirs');
      await writeElsewhere(builder.request, draft, (doc) => ({
        ...withMetadata(doc, { name: theirs }),
        nodes: [
          ...doc.nodes,
          {
            id: crypto.randomUUID(),
            kind: 'note',
            position: { x: 0, y: 320 },
            note: { text: 'From elsewhere' },
          },
        ],
      }));
      await builder.rename(uniqueName(testInfo, 'mine'));

      const panel = page.getByTestId('builder-conflict');
      await expect(panel).toBeVisible();
      await expect(
        panel.getByRole('heading', {
          name: 'This draft changed on the server',
        }),
      ).toBeFocused();
      await expect(panel.getByTestId('conflict-merge-note')).toHaveText(
        'You and another editor changed 1 field differently. Review and merge to choose which version to keep.',
      );
      await expectAccessible(page, {
        include: '[data-testid="builder-conflict"]',
        label: 'Conflict panel with Review and merge',
      });

      await panel.getByTestId('conflict-merge').click();
      const dialog = page.getByTestId('merge-dialog');
      await expect(dialog).toBeVisible();
      await expect(
        dialog.getByRole('group', { name: 'Diagram name' }),
      ).toBeVisible();
      await expectAccessible(page, {
        include: '[data-testid="merge-dialog"]',
        label: 'Merge dialog',
      });

      // Cancel goes back to the panel. Keep all theirs then chooses for
      // every field.
      await dialog.getByTestId('merge-cancel').click();
      await expect(dialog).toHaveCount(0);
      await expect(panel.getByTestId('conflict-merge')).toBeFocused();
      await panel.getByTestId('conflict-merge').click();
      await dialog.getByTestId('merge-all-theirs').click();
      await expect(dialog.getByTestId('merge-count')).toHaveText(
        '1 of 1 chosen',
      );
      await dialog.getByTestId('merge-save').click();
      await expect(dialog).toHaveCount(0);
      await expect(panel).toHaveCount(0);
      await builder.waitSaved();

      const server = await builder.serverDocument(draft);
      expect(server.metadata.name).toBe(theirs);
      expect(countKind(server, 'note')).toBe(1);
      await expect(page.getByTestId('builder-name')).toHaveText(theirs);

      expectNoFatal(issues);
    },
  );

  test('Save my history as a new draft forks the local history', async ({
    builder,
    issues,
  }, testInfo) => {
    const { draft, fork, title } = await forkAfterConflict(builder, testInfo);

    await expect(builder.page.getByTestId('builder-conflict')).toHaveCount(0);
    await builder.waitSaved();
    await expect(builder).toHaveAnnounced(
      /Saved your local history as a new draft/,
    );
    await builder.expectCounts({ devices: 2 });

    await test.step('the fork is titled as a local copy', async () => {
      expect((await builder.serverDraft(draft)).title).toBe(title);
      expect((await builder.serverDraft(fork)).title).toBe(
        `${title} (local copy)`,
      );
    });

    // The new draft replays the history on screen: the diagram as it was
    // opened (no edit to name), the rename, then two devices.
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
    expect(original.metadata.description).toBe('Changed elsewhere');
    expect(countKind(original, 'device')).toBe(1);

    // Editing continues on the new draft.
    await addDevices(builder, 1);
    await expectServerCounts(builder, fork, { devices: 3 });
    await builder.waitSaved();
    expect(countKind(await builder.serverDocument(draft), 'device')).toBe(1);

    await test.step('the new draft updates the topology the draft published', async () => {
      const current = await builder.request.get(draftPath(fork));
      const published = await builder.request.post(
        `${draftPath(fork)}/publish`,
        {
          headers: { 'If-Match': current.headers().etag },
          data: {
            mode: 'topology',
            topology: { name: title, action: 'update' },
          },
        },
      );
      expect(published.status(), await published.text()).toBe(200);
      const topology = await builder.config('Topology', title);
      expect.soft(topology?.spec?.nodes).toHaveLength(3);
    });

    expectNoFatal(issues);
  });

  test('Draft History reads no raw ids, keeps its tooltips off the actions, and deletes a snapshot after asking, but never the current one', async ({
    builder,
    issues,
    page,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();
    await addDevices(builder, 2);
    await expectServerCounts(builder, draft, { devices: 2 });
    await builder.waitSaved();

    const dialog = await builder.openDialog('history');
    const rows = dialog.getByTestId('history-row');
    const deletes = dialog.getByTestId('history-delete');
    const tooltip = page.getByTestId('history-tooltip');
    // The oldest of three: a change after its restore removes the others.
    const RESTORE_OLDEST =
      'Restore. Your next change after this restore removes 2 newer snapshots';
    await expect(rows).toHaveCount(3);

    await test.step('no row shows or reads a raw id', async () => {
      const entries = await rows.evaluateAll((elements) =>
        elements.map((row) => ({
          text: row.textContent.replace(/\s+/g, ' ').trim(),
          labels: [...row.querySelectorAll('[aria-label]')].map((element) =>
            element.getAttribute('aria-label'),
          ),
        })),
      );
      for (const { text, labels } of entries) {
        expect.soft(text, `"${text}" shows a raw id`).not.toMatch(UUID);
        for (const label of labels) {
          expect.soft(label, `"${label}" reads a raw id`).not.toMatch(UUID);
        }
      }
    });

    await test.step('the current snapshot looks unavailable, and a press on it does nothing', async () => {
      const current = rows.first().getByTestId('history-restore');
      for (const button of [current, deletes.first()]) {
        await expect.soft(button).toHaveCSS('border-top-style', 'dashed');
      }
      await expect
        .soft(rows.last().getByTestId('history-restore'))
        .toHaveCSS('border-top-style', 'solid');
      await current.press('Enter');
      await expect.soft(current).toBeFocused();
      await expect(rows).toHaveCount(3);
      await expectAccessible(page, {
        include: '[data-testid="history-dialog"]',
        label: 'Draft History',
      });
    });

    await test.step("a row's tooltips sit beside its actions, or above the row in a narrow window", async () => {
      const oldest = rows.last();
      const restore = oldest.getByTestId('history-restore');

      // The pointer moves onto each tooltip without crossing the other button.
      for (const [action, text] of [
        ['history-restore', RESTORE_OLDEST],
        ['history-delete', 'Delete'],
      ]) {
        await oldest.getByTestId(action).hover();
        await expect(tooltip).toHaveText(text);
        const shown = await tooltip.boundingBox();
        const cell = await oldest
          .locator('td', { has: page.getByTestId('history-restore') })
          .boundingBox();
        expect
          .soft(
            shown.x + shown.width <= cell.x || shown.x >= cell.x + cell.width,
            `${text} is beside the actions`,
          )
          .toBe(true);
        await page.mouse.move(
          shown.x + shown.width / 2,
          shown.y + shown.height / 2,
          { steps: 10 },
        );
        await expect.soft(tooltip).toHaveText(text);
        await page.mouse.move(0, 0);
        await expect(tooltip).toHaveCount(0);
      }

      // At 390 pixels the table scrolls in its own box, not the page.
      const wide = page.viewportSize();
      await page.setViewportSize({ width: 390, height: 800 });
      const box = dialog.getByTestId('history-scroll');
      await expect(box).toHaveAttribute('tabindex', '0');
      const overflow = await page.evaluate(() => {
        const root = document.documentElement;
        const shown = document.querySelector('[data-testid="history-dialog"]');

        return [
          root.scrollWidth - root.clientWidth,
          shown.scrollWidth - shown.clientWidth,
        ];
      });
      expect.soft(overflow).toEqual([0, 0]);
      await expect.soft(restore).toBeInViewport({ ratio: 1 });

      // With no room after the actions, tooltips go above the row. Escape
      // hides one and keeps the dialog.
      const remove = rows.first().getByTestId('history-delete');
      await remove.hover();
      await expect(tooltip).toHaveText(/^Delete\. /);
      let shown = await tooltip.boundingBox();
      let row = await rows.first().boundingBox();
      expect
        .soft(shown.y + shown.height, 'Delete is above its row')
        .toBeLessThanOrEqual(row.y);
      const at = await remove.boundingBox();
      await page.mouse.move(at.x + at.width / 2, shown.y + shown.height / 2, {
        steps: 10,
      });
      await expect.soft(tooltip).toHaveText(/^Delete\. /);
      await page.keyboard.press('Escape');
      await expect(tooltip).toHaveCount(0);
      await expect.soft(dialog).toBeVisible();

      await restore.focus();
      await expect(tooltip).toHaveText(RESTORE_OLDEST);
      shown = await tooltip.boundingBox();
      row = await oldest.boundingBox();
      expect
        .soft(shown.y + shown.height, 'Restore is above its row')
        .toBeLessThanOrEqual(row.y);
      await restore.blur();
      await page.setViewportSize(wide);
    });

    // Delete asks first, on Cancel, which keeps the snapshot and gives focus
    // back. The second snapshot keeps its number, 2.
    const second = rows
      .filter({ has: page.getByRole('cell', { name: '2', exact: true }) })
      .getByTestId('history-delete');
    const confirm = page.getByRole('alertdialog', { name: 'Delete snapshot?' });
    await second.press('Enter');
    await expect(confirm).toBeVisible();
    await expect(confirm.getByRole('button', { name: 'Cancel' })).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(confirm).toHaveCount(0);
    await expect(second).toBeFocused();
    await expect(rows).toHaveCount(3);

    // Someone saves meanwhile: the delete reads the list again and says
    // so, and trying again deletes it.
    await writeElsewhere(builder.request, draft, (doc) =>
      withMetadata(doc, { description: 'Changed elsewhere' }),
    );
    const before = await listSnapshots(builder.request, draft);
    await second.press('Enter');
    await confirm.getByRole('button', { name: 'Delete snapshot' }).click();
    await expect(dialog.getByTestId('history-problem')).toHaveText(
      'This draft changed on the server after Builder read its history. The list now shows the latest history. Try again.',
    );
    await expect(rows).toHaveCount(4);
    await expect.soft(second).toBeFocused();

    await second.press('Enter');
    await confirm.getByRole('button', { name: 'Delete snapshot' }).click();
    await expect(rows).toHaveCount(3);
    await expect
      .soft(dialog.getByTestId('history-status'))
      .toHaveText(
        /^\s*Deleted Added device, .+\. 3 snapshots, newest first\.$/,
      );
    // Focus moves to the Delete of the row that took its place, the next
    // older snapshot.
    const created = dialog.getByRole('button', {
      name: /^Delete Draft created, /,
    });
    await expect.soft(created).toBeFocused();
    await expect.soft(deletes.last()).toBeFocused();
    const after = await listSnapshots(builder.request, draft);
    expect(after.map((snapshot) => snapshot.id)).toEqual(
      before.filter((_, index) => index !== 1).map((snapshot) => snapshot.id),
    );

    // Deleting the last row moves focus to the one above it.
    await created.press('Enter');
    await confirm.getByRole('button', { name: 'Delete snapshot' }).click();
    await expect(rows).toHaveCount(2);
    await expect.soft(deletes.last()).toBeFocused();
    await expect
      .soft(deletes.last())
      .toHaveAccessibleName(/^Delete Added device, /);

    // Escape hides the focused Delete's tooltip first, then the dialog.
    await expect.soft(tooltip).toHaveText('Delete');
    await page.keyboard.press('Escape');
    await expect.soft(tooltip).toHaveCount(0);
    await expect(dialog).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    expectNoFatal(issues);
  });

  test('edits made offline survive a reload', async ({ builder, page }) => {
    await builder.open();
    const draft = await builder.createBlank();

    // Reloaded while the draft routes still fail, so nothing reaches the
    // server before the page is gone. The browser asks first.
    const prompts = await builder.editAndReload(draft, {
      edit: async () => {
        await page.route(DRAFT_ROUTES, (route) => route.abort());
        await addDevices(builder, 2);
      },
      expectSaved: async () => {
        await expect(builder.saveState).toHaveText(
          /Offline: 2 changes kept on this device/,
        );

        // Each edit's snapshot is a record of its own, written once. The
        // draft record keeps the queue and no snapshot.
        const local = await localDrafts(page);
        expect(local.drafts).toHaveLength(1);
        expect(local.drafts[0].queue).toHaveLength(2);
        expect(
          local.drafts[0].entries.filter((entry) => 'snapshot' in entry),
        ).toEqual([]);
        expect(local.entries).toBe(2);
      },
      offline: DRAFT_ROUTES,
      expectOnLanding: async () => {
        await expectServerCounts(builder, draft, { devices: 0 });
      },
      expectAfterReload: async () => {
        await builder.expectCounts({ devices: 2 });
        await builder.waitSaved();
        await expectServerCounts(builder, draft, { devices: 2 });
      },
    });
    expect.soft(prompts, 'prompt before the reload').toEqual(['beforeunload']);

    // Once saved, nothing of the draft is left on this device.
    await expect
      .poll(() => localDrafts(page))
      .toEqual({ drafts: [], entries: 0 });

    await test.step('a save whose answer never came is not a conflict after a reload', async () => {
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
      const appends = [];
      await builder.editAndReload(draft, {
        edit: () => addDevices(builder, 1),
        expectSaved: async () => {
          await expectServerCounts(builder, draft, { devices: 3 });
          expect((await localDrafts(page)).drafts).toHaveLength(1);
          await page.unroute(SNAPSHOTS);
        },
        expectOnLanding: () => {
          page.on('request', (request) => {
            if (
              request.method() === 'POST' &&
              request.url().endsWith('/snapshots')
            ) {
              appends.push(request.url());
            }
          });
        },
        expectAfterReload: async () => {
          await builder.expectCounts({ devices: 3 });
          await builder.waitSaved();
        },
      });

      await expect(page.getByTestId('builder-conflict')).toHaveCount(0);
      expect(appends, 'snapshots sent again').toEqual([]);
      expect((await listSnapshots(builder.request, draft)).at(-1).opId).toBe(
        stored[0],
      );
      await expect
        .poll(() => localDrafts(page))
        .toEqual({ drafts: [], entries: 0 });
    });

    await test.step('an undo made offline is what reopens after a reload', async () => {
      await addDevices(builder, 1);
      await expectServerCounts(builder, draft, { devices: 4 });
      await builder.waitSaved();

      await builder.editAndReload(draft, {
        edit: async () => {
          await page.route(DRAFT_ROUTES, (route) => route.abort());
          await builder.toolbar('undo').click();
        },
        expectSaved: async () => {
          await builder.expectCounts({ devices: 3 });
          await expect(builder.saveState).toHaveText(
            /Offline: 1 change kept on this device/,
          );
        },
        offline: DRAFT_ROUTES,
        // The screen shows the undo, the server's cursor follows it, and
        // the undone edit can be redone.
        expectAfterReload: async () => {
          await builder.expectCounts({ devices: 3 });
          await builder.waitSaved();
          await expectServerCounts(builder, draft, { devices: 3 });
          await expect.soft(builder.toolbar('redo')).toBeEnabled();
        },
      });
    });

    await test.step('Inspector changes typed and not applied are kept by a reload made offline', async () => {
      const prompts = await builder.editAndReload(draft, {
        edit: async () => {
          await builder.selectNode('node');
          await page.route(DRAFT_ROUTES, (route) => route.abort());
          await memoryField(builder).fill('4096');
        },
        // Focus stays in the field, which has not committed its text yet.
        expectSaved: () => expect.soft(memoryField(builder)).toBeFocused(),
        offline: DRAFT_ROUTES,
        expectAfterReload: async () => {
          await builder.selectNode('node');
          await expect.soft(memoryField(builder)).toHaveValue('4096');
          await builder.waitSaved();
        },
      });
      expect
        .soft(prompts, 'prompt before the reload')
        .toEqual(['beforeunload']);

      const device = (await builder.serverDocument(draft)).nodes.find(
        (node) => node.label === 'node',
      );
      expect.soft(device?.device?.spec?.hardware?.memory).toBe(4096);
      expect
        .soft((await listSnapshots(builder.request, draft)).at(-1).summary)
        .toBe('Saved unapplied changes to Device node');
      // The copy the reload kept at once, in case IndexedDB had not stored
      // the change yet, goes once the change is saved.
      await expect
        .poll(async () => ({
          ...(await localDrafts(page)),
          copies: await unloadCopies(page),
        }))
        .toEqual({ drafts: [], entries: 0, copies: [] });
    });

    await test.step('a save that reaches the server twice is stored once, without a conflict', async () => {
      // The first save is stored, then the browser's own copy arrives as a
      // resend would. The server refuses it and the editor finds it stored.
      const sent = [];
      await page.route(SNAPSHOTS, async (route) => {
        if (route.request().method() !== 'POST') {
          return route.fallback();
        }

        sent.push(route.request().postDataJSON().opId);
        if (sent.length === 1) {
          await route.fetch();
        }

        await route.continue();
      });
      const before = await listSnapshots(builder.request, draft);
      await addDevices(builder, 1);
      await expectServerCounts(builder, draft, { devices: 4 });
      await builder.waitSaved();
      await page.unroute(SNAPSHOTS);

      await expect(page.getByTestId('builder-conflict')).toHaveCount(0);
      expect(sent, 'snapshots the browser sent').toHaveLength(1);
      const after = await listSnapshots(builder.request, draft);
      expect(after.slice(0, -1).map((snapshot) => snapshot.id)).toEqual(
        before.map((snapshot) => snapshot.id),
      );
      expect(after.at(-1).opId).toBe(sent[0]);
      await expect
        .poll(() => localDrafts(page))
        .toEqual({ drafts: [], entries: 0 });
    });
  });

  test(
    'a draft open in two tabs warns, keeps the changes of each, and asks which to save',
    { tag: '@cross-browser' },
    async ({ builder, page, request, tracker }, testInfo) => {
      const title = uniqueName(testInfo, 'tabs');
      const draft = await builder.seedDraft(blankDocument(title));
      await builder.openDraft(draft);
      const second = await page.context().newPage();
      tracker.watch(second);
      const other = new BuilderPage(second, request, tracker);
      await other.openDraft(draft);
      const notice = (tab) => tab.page.getByTestId('builder-tabs');
      const choice = (tab) =>
        tab.page.getByRole('dialog', { name: 'Choose which changes to save' });

      await test.step('each tab says the draft is open in the other', async () => {
        for (const tab of [builder, other]) {
          await expect(notice(tab)).toHaveText(
            'This draft is also open in another tab, where all changes are saved. If you change this draft in both tabs, Builder cannot keep both sets of changes.',
          );
        }
      });

      await test.step('offline, each tab keeps its own changes, and says what the other holds', async () => {
        await page.context().setOffline(true);
        await addDevices(builder, 1);
        await expect(builder.saveState).toHaveText(
          /Offline: 1 change kept on this device/,
        );
        await addDevices(other, 2);
        await expect(other.saveState).toHaveText(
          /Offline: 2 changes kept on this device/,
        );
        await expect(notice(builder)).toContainText(
          'This draft is also open in another tab, which has 2 changes not saved yet.',
        );
        await expect(notice(other)).toContainText(
          'which has 1 change not saved yet.',
        );
        // A queue per tab on this device: neither replaced the other's.
        await expect
          .poll(async () =>
            (await localDrafts(page)).drafts
              .filter((record) => record.draftId === draft.id)
              .map((record) => record.queue.length)
              .sort(),
          )
          .toEqual([1, 2]);
      });

      await test.step('back online, neither saves: each asks which changes to save', async () => {
        await page.context().setOffline(false);
        const dialog = choice(builder);
        await expect(dialog).toBeVisible();
        await expect(choice(other)).toBeVisible();
        await expect
          .soft(builder.saveState)
          .toHaveText('1 change not saved yet. Choose which changes to save.');
        const group = dialog.getByRole('group', { name: 'Version to save' });
        await expect(group.getByRole('radio')).toHaveCount(2);
        const mine = group.getByRole('radio', {
          name: /^This tab: 1 change, last at \S.*\d/,
        });
        await expect(mine).toBeChecked();
        // Focus starts on the version picked.
        await expect.soft(mine).toBeFocused();
        await expect
          .soft(
            group.getByRole('radio', {
              name: /^Another tab: 2 changes, last at \S.*\d/,
            }),
          )
          .not.toBeChecked();
        await expect
          .soft(
            dialog.getByRole('button', { name: "Download this tab's changes" }),
          )
          .toBeVisible();
        await expectAccessible(page, {
          include: '[data-testid="tabs-choice"]',
          soft: true,
          label: 'the choice of which changes to save',
        });
        await expectServerCounts(builder, draft, { devices: 0 });
      });

      await test.step("the other tab's changes chosen, they are saved, and this tab's go to a new draft", async () => {
        const dialog = choice(builder);
        await dialog.getByRole('radio', { name: /^Another tab/ }).check();
        await dialog.getByRole('button', { name: 'Save this version' }).click();
        await expect(dialog).toBeHidden({ timeout: 20000 });
        await expect(choice(other)).toBeHidden();
        await expectServerCounts(builder, draft, { devices: 2 });
        await other.waitSaved();

        const copy = `${title} (local copy)`;
        await expect(page.getByTestId('builder-name')).toHaveText(copy);
        await builder.waitSaved();
        const fork = (await listDrafts(request)).find(
          (item) => item.title === copy,
        );
        expect(fork, 'the new draft').toBeTruthy();
        await expectServerCounts(builder, fork, { devices: 1 });

        // The tabs no longer have the same draft open, and focus stayed in
        // the page.
        await expect.soft(notice(builder)).toHaveCount(0);
        await expect.soft(notice(other)).toHaveCount(0);
        expect
          .soft(
            await page.evaluate(
              () =>
                document.activeElement &&
                document.activeElement !== document.body,
            ),
          )
          .toBe(true);
      });
    },
  );

  test('leaving the editor with a change the server lacks keeps it, another page asks first, and logging out clears it', async ({
    builder,
    page,
  }) => {
    // With a blank draft listed, the new one is numbered apart from it.
    await builder.seedDraft(blankDocument('Untitled topology'));
    await builder.open();
    const mine = page.getByTestId('drafts-list-drafts');
    await expect(
      mine.getByRole('heading', { name: 'Untitled topology', exact: true }),
    ).not.toHaveCount(0);
    const listed = (await mine.getByRole('heading').allTextContents()).map(
      (text) => text.trim(),
    );

    // A double click on Blank diagram makes one draft.
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
    const created = waitForApi(page, 'POST', '/builder/drafts');
    await page.getByTestId('drafts-blank').dblclick();
    const draft = await (await created).json();
    await expect(builder.canvas).toBeVisible();
    await builder.waitSaved();
    page.off('request', onRequest);
    expect.soft(creates, 'drafts made by a double click').toHaveLength(1);
    const title = (await page.getByTestId('builder-name').textContent()).trim();
    expect.soft(title).toMatch(/^Untitled topology \d+$/);
    expect.soft(listed, 'titles listed before').not.toContain(title);

    await page.route(DRAFT_ROUTES, (route) => route.abort());
    await addDevices(builder, 1);
    await expect(builder.saveState).toHaveText(
      /Offline: 1 change kept on this device/,
    );

    const back = page.getByRole('button', { name: 'Drafts', exact: true });
    const confirm = page.getByRole('alertdialog', {
      name: 'Leave with unsaved changes?',
    });

    await test.step("The Drafts button goes at once, and the draft's card says the change is kept", async () => {
      await back.click();
      await expect(builder.landingHeading).toBeVisible();
      await expect.soft(confirm).toHaveCount(0);
      const open = page.getByTestId(`draft-open-${draft.id}`);
      await expect
        .soft(page.getByTestId(`draft-save-${draft.id}`))
        .toHaveText(/^Offline: 1 change kept on this device/);
      // Focus goes to the card, whose Open is the way back to the change.
      await expect.soft(open).toBeFocused();
    });

    await test.step('another page asks, as the change is not saved yet; Stay keeps the drafts and Leave anyway goes there', async () => {
      const experiments = page.getByRole('link', { name: 'Experiments' });
      await experiments.click();
      await expect(confirm).toBeVisible();
      await expect
        .soft(confirm)
        .toHaveAccessibleDescription(
          'This device keeps your 1 unsaved change to a draft you closed. The Builder saves it when you next open that draft in this browser, unless you log out first.',
        );
      // Focus starts on the choice that keeps the work.
      await expect
        .soft(confirm.getByRole('button', { name: 'Stay' }))
        .toBeFocused();
      await confirm.getByRole('button', { name: 'Stay' }).click();
      await expect(confirm).toBeHidden();
      await expect.soft(page).toHaveURL(/\/builder$/);
      await expect.soft(builder.landingHeading).toBeVisible();

      await experiments.click();
      await confirm.getByRole('button', { name: 'Leave anyway' }).click();
      await expect(page).toHaveURL(/\/experiments$/);
    });

    await test.step('logging out, with the Builder closed, clears the change the server lacks but keeps the preferences', async () => {
      const kept = () =>
        page.evaluate(
          () =>
            new Promise((resolve) => {
              const theme = localStorage.getItem('phenix.builder.theme');
              const request = indexedDB.open('phenix-builder');
              request.onsuccess = () => {
                const db = request.result;
                const count = db
                  .transaction('drafts')
                  .objectStore('drafts')
                  .count();
                count.onsuccess = () => {
                  db.close();
                  resolve({ theme, drafts: count.result });
                };
              };
            }),
        );
      await page.evaluate(() =>
        localStorage.setItem('phenix.builder.theme', 'dark'),
      );
      expect(await kept()).toEqual({ theme: 'dark', drafts: 1 });

      // A server without authentication refuses GET /logout, which the
      // header's Logout asks first.
      await page.route('**/api/v1/logout', (route) =>
        route.fulfill({ status: 204 }),
      );
      await page.getByRole('button', { name: 'Logout' }).click();
      await expect(page).toHaveURL(/\/signin$/);
      await expect.poll(kept).toEqual({ theme: 'dark', drafts: 0 });
    });

    await test.step('the draft reopens as the server has it, with the preferences kept', async () => {
      await page.unroute(DRAFT_ROUTES);
      await builder.open();
      // While a slow draft opens, its Open says so and takes no second
      // click. The editor's heading takes focus once it shows.
      const reads = [];
      const read = (url) =>
        url.pathname.endsWith(`/builder/drafts/${draft.id}`);
      let release;
      const held = new Promise((resolve) => {
        release = resolve;
      });
      await page.route(read, async (route) => {
        reads.push(route.request().method());
        await held;
        await route.fallback();
      });
      const open = page.getByTestId(`draft-open-${draft.id}`);
      await open.click();
      await expect.soft(open).toHaveAttribute('aria-busy', 'true');
      await expect.soft(open).toHaveAttribute('aria-disabled', 'true');
      await expect
        .soft(open)
        .toHaveAccessibleName(/^Opening Untitled topology/);
      await expect
        .soft(open.locator('.builder-toolbar__spinner'))
        .toBeVisible();
      await expect
        .soft(builder)
        .toHaveAnnounced(/Opening Untitled topology \d+…/);
      await open.click({ force: true });
      release();
      await expect(builder.canvas).toBeVisible();
      await expect.soft(page.getByRole('heading', { level: 1 })).toBeFocused();
      expect.soft(reads, 'draft reads').toEqual(['GET']);
      await page.unroute(read);
      await builder.waitSaved();
      await builder.expectCounts({ devices: 0 });
      await expect.soft(builder).not.toHaveAnnounced('Recovered');
      await expect
        .soft(page.locator('.builder-root'))
        .toHaveAttribute('data-builder-theme', 'dark');
    });
  });

  test('The Drafts button does not wait for a slow save, saves Inspector changes not applied, and asks only of those it cannot apply', async ({
    builder,
    page,
    request,
  }) => {
    // With a blank draft listed, the new one is numbered apart from it.
    const listed = await builder.seedDraft(blankDocument('Untitled topology'));
    await builder.open();
    await expect(page.getByTestId(`draft-open-${listed.id}`)).toBeVisible();
    const draft = await builder.createBlank();
    const back = page.getByRole('button', { name: 'Drafts', exact: true });
    const confirm = page.getByRole('alertdialog', {
      name: 'Leave with unsaved changes?',
    });

    await test.step('The Drafts button does not wait for a slow save: the card says it goes on, then that it is done', async () => {
      // The save is held until the landing shows it going on. The route goes at
      // the step's end, so later saves are not held.
      let release;
      const held = new Promise((resolve) => {
        release = resolve;
      });
      const slow = (url) => url.pathname.endsWith('/snapshots');
      await page.route(slow, async (route) => {
        await held;
        await route.continue();
      });
      await addDevices(builder, 1);
      await back.click();
      // The draft was listed: the landing shows at once, and focus goes to
      // its card, which says the change is being sent.
      await expect(page.getByTestId('drafts-list-drafts')).toBeVisible();
      const card = page.getByTestId(`draft-save-${draft.id}`);
      await expect(card).toHaveText('Saving 1 change…');
      await expect.soft(confirm).toHaveCount(0);
      await expect
        .soft(card.locator('.builder-toolbar__spinner'))
        .toBeVisible();
      await expect
        .soft(page.getByTestId(`draft-open-${draft.id}`))
        .toBeFocused();
      release();
      await expect(card).toHaveText('All changes saved.', { timeout: 10000 });
      await expect
        .soft(builder)
        .toHaveAnnounced(/Saved your changes to Untitled topology \d+\./);
      await expectServerCounts(builder, draft, { devices: 1 });
      await expectAccessible(page, {
        include: '[data-testid="drafts-list-drafts"]',
        soft: true,
        label: 'a card saying its changes are saved',
      });
      await page.unroute(slow);
    });

    await test.step('Inspector changes not applied are saved when the Drafts button closes the draft, and Draft History marks them', async () => {
      const open = page.getByTestId(`draft-open-${draft.id}`);
      await open.click();
      await expect(builder.canvas).toBeVisible();
      await builder.waitSaved();
      await builder.selectNode('node');
      await memoryField(builder).fill('4096');
      await memoryField(builder).blur();
      await expect(
        builder.inspector.getByTestId('inspector-apply'),
      ).toBeVisible();

      // Nothing is left to ask about: the changes are saved as one edit,
      // sent once the landing shows.
      await back.click();
      await expect(builder.landingHeading).toBeVisible({ timeout: 20000 });
      await expect.soft(confirm).toHaveCount(0);
      await expect.soft(open).toBeFocused();
      await expect(page.getByTestId(`draft-save-${draft.id}`)).toHaveText(
        'All changes saved.',
        { timeout: 20000 },
      );
      const device = (await builder.serverDocument(draft)).nodes.find(
        (node) => node.kind === 'device',
      );
      expect.soft(device?.device?.spec?.hardware?.memory).toBe(4096);
      expect
        .soft((await listSnapshots(request, draft)).at(-1).summary)
        .toBe('Saved unapplied changes to Device node');

      await open.click();
      await expect(builder.canvas).toBeVisible();
      await builder.selectNode('node');
      await expect.soft(memoryField(builder)).toHaveValue('4096');
      const dialog = await builder.openDialog('history');
      const saved = dialog
        .getByTestId('history-row')
        .filter({ hasText: 'Saved unapplied changes to Device node' });
      await expect(saved).toBeVisible();
      await expect
        .soft(saved.getByTestId('history-automatic'))
        .toContainText('Automatic');
      await expect
        .soft(dialog.getByTestId('history-automatic-hint'))
        .toHaveText(
          'Automatic: the Builder saved changes that you did not apply in the Inspector. It saved them before you left, published or downloaded the diagram.',
        );
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);
    });

    await test.step('Inspector changes that cannot be applied keep the question, naming their fields', async () => {
      await memoryField(builder).fill('lots');
      await memoryField(builder).blur();
      await back.click();
      await expect(confirm).toBeVisible();
      await expect
        .soft(confirm)
        .toHaveAccessibleDescription(
          'Your changes to Device node in the Inspector cannot be saved until you fix Memory.',
        );
      await expect
        .soft(confirm.getByRole('button', { name: 'Stay' }))
        .toBeFocused();
      await expect
        .soft(confirm.getByRole('button', { name: 'Leave without them' }))
        .toBeVisible();
      await confirm.getByRole('button', { name: 'Stay' }).click();
      await expect(confirm).toBeHidden();
      await expect.soft(back, 'focus after Stay').toBeFocused();
      await expect.soft(memoryField(builder)).toHaveValue('lots');
      await builder.inspector.getByTestId('inspector-cancel').click();
      await expect(
        builder.inspector.getByTestId('inspector-apply'),
      ).toHaveCount(0);
      await back.click();
      await expect(builder.landingHeading).toBeVisible();
    });

    await test.step('a draft deleted elsewhere meanwhile leaves focus on the landing', async () => {
      await page.getByTestId(`draft-open-${draft.id}`).click();
      await expect(builder.canvas).toBeVisible();
      const current = await request.get(draftPath(draft));
      const deleted = await request.delete(draftPath(draft), {
        headers: { 'If-Match': current.headers().etag },
      });
      expect(deleted.ok(), await deleted.text()).toBeTruthy();

      // The landing shows the listed card at once, and the lists read
      // behind it drop it: focus goes to the card in its place or the tab.
      await back.click();
      await expect(page.getByTestId(`draft-open-${draft.id}`)).toHaveCount(0);
      await expect
        .poll(() =>
          page.evaluate(() =>
            Boolean(
              document.activeElement?.closest(
                '[role="tabpanel"], [role="tablist"]',
              ),
            ),
          ),
        )
        .toBe(true);
    });
  });

  test('a ?topology= link edits one draft of the diagram, which a reload reopens and which still opens after it publishes again', async ({
    builder,
    page,
    tracker,
  }, testInfo) => {
    const target = uniqueName(testInfo, 'deeplink');
    // The draft that published it is gone, so the diagram opens in a draft
    // made from it.
    const { documentId } = await publishTopology(
      builder.request,
      tracker,
      target,
      undefined,
      { keepDraft: false },
    );
    // Every draft of this diagram: each is named after it.
    const drafts = async () =>
      (await listDrafts(builder.request)).filter(
        (item) => item.title === target,
      );
    const deepLink = `/builder?topology=${encodeURIComponent(target)}`;
    const currentDocument = async () => {
      const listed = await builder.request.get(`${API}/builder/documents`);
      expect(listed.ok(), await listed.text()).toBeTruthy();

      return ((await listed.json()).documents || []).find(
        (entry) => entry.target === target,
      );
    };

    await visit(page, deepLink);
    await expect(builder.canvas).toBeVisible({ timeout: 20000 });
    await builder.waitSaved();
    const [draft, ...others] = await drafts();
    expect(others).toEqual([]);
    expect.soft(draft.sourceToken).toBe(`builder-doc/${documentId}`);
    await expect
      .soft(builder)
      .toHaveAnnounced(
        `Opened topology ${target} in the Builder as a new draft.`,
      );
    const namesDraft = (url) => url.searchParams.get('draft') === draft.id;
    // The address names the draft now, not the published diagram.
    await expect.soft(page).toHaveURL(namesDraft);

    await addDevices(builder, 1);
    await expectServerCounts(builder, draft, { devices: 1 });
    await builder.waitSaved();

    await test.step('a reload reopens the draft, edits and all', async () => {
      await page.reload();
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await builder.waitSaved();
      await builder.expectCounts({ devices: 1 });
    });

    await test.step('the link from Configs opens the same draft again', async () => {
      await visit(page, deepLink);
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await builder.waitSaved();
      await builder.expectCounts({ devices: 1 });
      await expect
        .soft(builder)
        .toHaveAnnounced(
          `Opened topology ${target} in the Builder, in its existing draft.`,
        );
      await expect.soft(page).toHaveURL(namesDraft);
      expect
        .soft(await drafts(), 'drafts of the published diagram')
        .toHaveLength(1);
    });

    // Publishing stores a new document, with a new id, which the draft's
    // source token does not name: the draft is found by what it published.
    await test.step('once the draft has published the topology again, the link still opens it', async () => {
      const current = await builder.request.get(draftPath(draft));
      const response = await builder.request.post(
        `${draftPath(draft)}/publish`,
        {
          headers: { 'If-Match': current.headers().etag },
          data: {
            mode: 'topology',
            topology: { name: target, action: 'update' },
          },
        },
      );
      expect(response.status(), await response.text()).toBe(200);
      const republished = await currentDocument();
      expect(republished.id, 'the new document id').not.toBe(documentId);
      expect.soft(republished.draftId).toBe(draft.id);

      await visit(page, deepLink);
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await builder.waitSaved();
      await builder.expectCounts({ devices: 1 });
      await expect
        .soft(builder)
        .toHaveAnnounced(
          `Opened topology ${target} in the Builder, in the draft that published it.`,
        );
      await expect.soft(page).toHaveURL(namesDraft);
      // No second draft was made from the new document.
      expect
        .soft(
          (await drafts()).map((item) => item.id),
          'drafts of the published diagram',
        )
        .toEqual([draft.id]);
    });

    // The Drafts button drops the draft from the address.
    await builder.backToDrafts();
    await expect.soft(page).toHaveURL(/\/builder$/);
  });
});
