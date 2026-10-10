// Sharing a draft between users: the owner shares it, the people it is
// shared with open it to edit or to view, and access changes while it is
// open. Opt-in: the tests need a server with authentication on, e.g.:
//
//   phenix ui --jwt-signing-key e2e-sharing \
//     --users 'e2e-admin:Testpass1!:Global Admin'
//   E2E_SHARING=1 npx playwright test builder-sharing
//
// E2E_ADMIN_USER and E2E_ADMIN_PASS name another admin. The admin makes a
// role and the users each test needs, and deletes them afterwards.
//
// The UI is the one the other specs use (VITE_AUTH=disabled). Each user's
// session is set up as a sign-in leaves it, in a browser of their own (see
// sharingUsers in builder-support.js). Signing in again without leaving
// the Builder, and logging out with changes the server does not have, are
// tested here too, as they need such a session.
const fs = require('fs');

const {
  ADMIN_PASS,
  ADMIN_USER,
  API,
  DOCUMENT_TIME,
  SAVED,
  USER_PASS,
  blankDocument,
  draftPath,
  expect,
  expectAccessible,
  expectDetail,
  iconName,
  iconOf,
  iconPath,
  labDocument,
  ownColor,
  pngOf,
  recordAnnouncements,
  signIn,
  test,
  visit,
  waitForApi,
} = require('./builder-support');

test.skip(
  process.env.E2E_SHARING !== '1',
  'set E2E_SHARING=1 (see file header)',
);

// Makes a draft of `owner`'s through the API, holding `document`, and
// shares it with `shares`.
async function seedDraft(
  owner,
  name,
  shares = [],
  document = blankDocument(name),
) {
  const created = await owner.api.post(`${API}/builder/drafts`, {
    data: { title: name, document },
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const draft = await created.json();

  if (shares.length) {
    const path = `${draftPath(draft)}/shares`;
    const read = await (await owner.api.get(path)).json();
    const saved = await owner.api.put(path, {
      headers: { 'If-Match': read.sharesEtag },
      data: { shares },
    });
    expect(saved.ok(), await saved.text()).toBeTruthy();
  }

  return draft;
}

async function landing({ page }) {
  await visit(page, '/builder');
  await expect(
    page.getByRole('heading', { name: 'Builder', exact: true }),
  ).toBeVisible({ timeout: 20000 });
}

// Opens a draft listed under Shared Drafts.
async function openShared(user, draft) {
  const { page } = user;

  await landing(user);
  await page.getByTestId('drafts-tab-shared').click();
  await page.getByTestId(`draft-open-${draft.id}`).click();
  await expect(page.getByTestId('builder-canvas')).toBeVisible({
    timeout: 20000,
  });
}

// Opens one of the user's own drafts from the landing.
async function openOwn(user, draft) {
  const { page } = user;

  await landing(user);
  await page.getByTestId(`draft-open-${draft.id}`).click();
  await expect(page.getByTestId('builder-canvas')).toBeVisible({
    timeout: 20000,
  });
  await expect(page.getByTestId('builder-save-state')).toContainText(SAVED);
}

// The Share dialog, once it has read who has access.
async function shareDialog(page) {
  const dialog = page.getByTestId('share-dialog');

  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId('share-loading')).toHaveText('');

  return dialog;
}

// Renames the diagram, and waits for the server to answer the save that
// makes (the save state may say all is saved before it starts).
async function rename(page, name) {
  const field = page.getByTestId('builder-name-field');
  const answered = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname.endsWith('/snapshots'),
  );

  await page.getByTestId('builder-name-edit').click();
  await field.fill(name);
  await field.press('Enter');

  return answered;
}

function editorHeading(page) {
  return page.getByRole('heading', { level: 1, name: /Builder$/ });
}

// labDocument with a third device, server-3, connected as the others are.
function threeServers(name) {
  const doc = labDocument(name);
  const [first] = doc.nodes;
  const sw = doc.nodes.find((node) => node.kind === 'switch');
  const handle = { id: crypto.randomUUID(), name: 'eth0', index: 0 };
  const hostname = 'server-3';
  const third = {
    ...first,
    id: crypto.randomUUID(),
    label: hostname,
    position: { x: 640, y: 0 },
    device: {
      ...first.device,
      hostname,
      spec: {
        ...first.device.spec,
        general: { ...first.device.spec.general, hostname },
      },
      interfaces: [handle],
    },
  };

  return {
    ...doc,
    nodes: [...doc.nodes, third],
    edges: [
      ...doc.edges,
      {
        id: crypto.randomUUID(),
        sourceNodeId: third.id,
        sourceHandleId: handle.id,
        targetNodeId: sw.id,
        networkId: doc.networks[0].id,
      },
    ],
  };
}

// Vue Flow's wrapper around the device with this hostname: the node's one
// focusable element, which takes the canvas keys.
function deviceNode(page, hostname) {
  return page.locator('.vue-flow__node-builderDevice').filter({
    has: page.locator('.builder-node__label', {
      hasText: new RegExp(`^${hostname}$`),
    }),
  });
}

// The response to the page's next snapshot upload, whatever its status.
function nextSave(page) {
  return page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname.endsWith('/snapshots'),
  );
}

// Renames the device `id` from its Outline row: F2, then the new name, which
// replaces the selected old one. Resolves with the response to its save.
async function renameDevice(page, id, from, to) {
  const saved = nextSave(page);
  const field = page.locator(`#rename-${id}`);

  await page.getByTestId(`outline-item-${id}`).focus();
  await page.keyboard.press('F2');
  await expect(field).toBeFocused();
  await expect(field).toHaveValue(from);
  await expect(field).toHaveAccessibleName(new RegExp(`^Rename ${from}\\b`));
  await page.keyboard.type(to);
  await page.keyboard.press('Enter');

  return saved;
}

// The id of the node of the device `hostname` in `doc`.
function deviceId(doc, hostname) {
  return doc.nodes.find((node) => node.device?.hostname === hostname).id;
}

// The draft's current document, as the server holds it.
async function serverDocument(user, draft) {
  const read = await user.api.get(draftPath(draft));
  expect(read.ok(), await read.text()).toBeTruthy();

  return (await read.json()).document;
}

function hostnamesOf(doc) {
  return doc.nodes
    .filter((node) => node.kind === 'device')
    .map((node) => node.device.hostname)
    .sort();
}

// The editor header in a window `width` wide: how wide the name's box is,
// in rem, and how many lines who shared the draft takes.
async function sharedHeader(page, width) {
  await page.setViewportSize({ width, height: page.viewportSize().height });

  return page.evaluate(
    () =>
      new Promise((resolve) => {
        requestAnimationFrame(() =>
          requestAnimationFrame(() => {
            const rem = parseFloat(
              getComputedStyle(document.documentElement).fontSize,
            );
            const name = document
              .querySelector('.builder-header__name')
              .getBoundingClientRect();
            const note = document.createRange();
            note.selectNodeContents(
              document.querySelector('[data-testid="editor-shared-by"]'),
            );

            resolve({
              name: name.width / rem,
              lines: new Set(
                [...note.getClientRects()].map((line) => Math.round(line.top)),
              ).size,
            });
          }),
        );
      }),
  );
}

test(
  'the owner shares a draft by keyboard from the drafts page, and copies a link that opens it',
  { tag: '@cross-browser' },
  async ({ sharingUsers }, testInfo) => {
    const { owner, editor, viewer, stranger } = sharingUsers;
    const name = 'Network lab';
    const draft = await seedDraft(owner, name);
    const { page } = owner;

    await test.step('the owner shares it from the drafts page', async () => {
      await landing(owner);
      const share = page.getByTestId(`draft-share-${draft.id}`);
      await share.press('Enter');
      const dialog = await shareDialog(page);
      const field = dialog.getByTestId('share-user');
      const access = dialog.getByTestId('share-access');
      const options = dialog.getByRole('listbox', { name: 'Users' });
      await expect(field).toBeFocused();
      await expect(dialog.getByTestId('share-users-note')).toHaveText('');

      // Down Arrow opens every user the draft may be shared with (the
      // owner's role may not list users), named "Name (username)", never
      // the owner; typing filters them, and Enter takes the one in view.
      // Other tests' users are offered too, so only this test's are looked
      // for.
      await field.press('ArrowDown');
      await expect(options).toBeVisible();
      await expect(field).toHaveAttribute('aria-expanded', 'true');
      for (const user of [editor, viewer, stranger]) {
        await expect(
          options.getByRole('option', { name: user.username }),
        ).toHaveCount(1);
      }
      await expect(
        options.getByRole('option', { name: owner.username }),
      ).toHaveCount(0);
      await page.keyboard.type(editor.username);
      const choice = options.getByRole('option');
      await expect(choice).toHaveCount(1);
      await expect(choice).toHaveText(
        new RegExp(`^.+ \\(${editor.username}\\)$`),
      );
      const label = (await choice.textContent()).trim();
      await field.press('ArrowDown');
      await expect(field).toHaveAttribute(
        'aria-activedescendant',
        await choice.getAttribute('id'),
      );
      await field.press('Enter');
      await expect(options).toBeHidden();
      await expect(field).toHaveValue(label);
      await access.selectOption('edit');
      await field.press('Enter');
      await expect(dialog.getByTestId('share-status')).toHaveText(
        `${editor.username} added, can edit. Save to apply.`,
      );
      await expect(field).toBeFocused();
      await expect(field).toHaveValue('');

      // A username typed in full is taken as it is.
      await page.keyboard.type(viewer.username);
      await access.selectOption('view');
      await field.press('Enter');
      await expect(
        dialog.getByTestId(`share-row-${viewer.username}`),
      ).toBeVisible();
      await expect(dialog.getByTestId('share-changes')).toHaveText(
        '2 changes not saved',
      );
      await expectAccessible(page, {
        include: '[data-testid="share-dialog"]',
        label: 'Share dialog',
      });

      await dialog.getByTestId('share-save').press('Enter');
      await expect(dialog).toBeHidden();
      await expect(share).toBeFocused();
      await expect(page).toHaveAnnounced(
        `${name} is now shared with ${editor.username} (can edit) and ${viewer.username} (can view).`,
      );
      await expect(
        page.getByTestId(`draft-shared-with-${draft.id}`),
      ).toContainText(`Shared with ${editor.username} and ${viewer.username}`);
    });

    if (testInfo.project.name === 'chromium') {
      await test.step('Copy link copies a link that opens the draft', async () => {
        await owner.context.grantPermissions([
          'clipboard-read',
          'clipboard-write',
        ]);
        await page.getByTestId(`draft-share-${draft.id}`).click();
        const dialog = await shareDialog(page);
        await dialog.getByTestId('share-copy-link').click();
        await expect(dialog.getByTestId('share-copy-status')).toHaveText(
          'Link copied.',
        );
        const link = await page.evaluate(() => navigator.clipboard.readText());
        expect(new URL(link).searchParams.get('draft')).toBe(
          `${owner.username}/${draft.id}`,
        );
        await dialog.getByTestId('share-cancel').click();
        await expect(dialog).toBeHidden();
      });
    }
  },
);

test(
  'the editor changes a shared draft, the owner sees who did, the viewer only views it, and only the owner deletes it or changes its access',
  { tag: '@cross-browser' },
  async ({ sharingUsers }) => {
    test.setTimeout(120000);
    const { owner, editor, viewer, stranger } = sharingUsers;
    const name = 'Network lab';
    const draft = await seedDraft(owner, name, [
      { user: editor.username, access: 'edit' },
      { user: viewer.username, access: 'view' },
    ]);
    const { page } = owner;
    // The time of the editor's save, once it is made.
    let editedAt;
    await landing(owner);

    await test.step('the editor finds it under Shared Drafts and changes it', async () => {
      const { page: theirs } = editor;
      await landing(editor);
      await theirs.getByTestId('drafts-tab-mine').press('ArrowRight');
      await expect(theirs.getByTestId('drafts-tab-shared')).toBeFocused();
      await expect(theirs.getByTestId('drafts-tab-shared')).toHaveText(
        /^Shared Drafts \(\d+\)$/,
      );
      await expect(
        theirs.getByTestId(`draft-access-${draft.id}`),
      ).toContainText('Can edit');
      await expect(theirs.getByTestId(`draft-delete-${draft.id}`)).toHaveCount(
        0,
      );
      // Someone who may edit it may publish it, from its card too.
      await expect(
        theirs.getByTestId(`draft-publish-${draft.id}`),
      ).toHaveAccessibleName(
        new RegExp(`^Publish ${name} by ${owner.username}, updated `),
      );
      await expectAccessible(theirs, { soft: true, label: 'Shared Drafts' });

      await theirs.getByTestId(`draft-open-${draft.id}`).press('Enter');
      await expect(theirs.getByTestId('builder-canvas')).toBeVisible({
        timeout: 20000,
      });
      await expect(theirs).toHaveAnnounced(
        `Opened ${owner.username}'s draft ${name}. You can edit it; others may be editing too.`,
      );
      await expect(theirs.getByTestId('editor-shared-by')).toHaveText(
        `Shared by ${owner.username} · Can edit`,
      );
      await expect(editorHeading(theirs)).toHaveText(
        `${name} – ${owner.username}'s draft – Builder`,
      );
      await expect(theirs.getByTestId('toolbar-share')).toHaveAttribute(
        'aria-disabled',
        'true',
      );

      // The diagram is the owner's: Details names them as its maker and
      // its last editor, until the editor saves a change.
      const made = draft.stamp;
      expect(made).toEqual({
        createdBy: owner.username,
        createdAt: expect.stringMatching(DOCUMENT_TIME),
        updatedBy: owner.username,
        updatedAt: made.createdAt,
      });
      await expectDetail(theirs, 'created', owner.username, made.createdAt);
      await expectDetail(theirs, 'edited', owner.username, made.updatedAt);

      const saved = await rename(theirs, `${name} changed`);
      await expect(theirs.getByTestId('builder-save-state')).toContainText(
        SAVED,
      );

      // The editor's save changes who edited it last, and nothing else.
      const { stamp } = await saved.json();
      expect(stamp).toEqual({
        ...made,
        updatedBy: editor.username,
        updatedAt: expect.stringMatching(DOCUMENT_TIME),
      });
      editedAt = stamp.updatedAt;
      await expectDetail(theirs, 'created', owner.username, made.createdAt);
      await expectDetail(theirs, 'edited', editor.username, editedAt);
    });

    await test.step("the owner's list shows the change, and who made it", async () => {
      await page.reload();
      const card = page
        .getByRole('listitem')
        .filter({ has: page.getByTestId(`draft-open-${draft.id}`) });
      await expect(card.getByRole('heading')).toHaveText(`${name} changed`);
      await expect(card).toContainText(`by ${editor.username}`);

      // Its four buttons, Share among them, are one size.
      const buttons = await card
        .locator('.builder-card__actions > button')
        .evaluateAll((all) =>
          all.map((button) => {
            const { width, height } = button.getBoundingClientRect();

            return {
              action: button.dataset.testid.split('-')[1],
              width,
              height,
            };
          }),
        );
      expect(buttons.map((button) => button.action)).toEqual([
        'open',
        'share',
        'delete',
        'publish',
      ]);
      for (const side of ['width', 'height']) {
        const sizes = buttons.map((button) => button[side]);

        expect
          .soft(Math.max(...sizes) - Math.min(...sizes), side)
          .toBeLessThan(0.5);
      }
    });

    await test.step('the viewer opens it view only', async () => {
      const { page: theirs } = viewer;
      await landing(viewer);
      await theirs.getByTestId('drafts-tab-shared').click();
      await expect(
        theirs.getByTestId(`draft-access-${draft.id}`),
      ).toContainText('Can view');
      for (const action of ['share', 'delete', 'publish']) {
        await expect(
          theirs.getByTestId(`draft-${action}-${draft.id}`),
        ).toHaveCount(0);
      }

      await theirs.getByTestId(`draft-open-${draft.id}`).click();
      await expect(theirs.getByTestId('builder-readonly')).toHaveText(
        `${owner.username} shared this draft with you to view. Use Download to keep a copy.`,
      );
      await expect(theirs).toHaveAnnounced(
        `Opened ${owner.username}'s draft ${name} changed, view only.`,
      );
      await expect(theirs.getByTestId('editor-shared-by')).toHaveText(
        `Shared by ${owner.username} · Can view`,
      );
      // A viewer reads who made the diagram and who edited it last.
      await expectDetail(
        theirs,
        'created',
        owner.username,
        draft.stamp.createdAt,
      );
      await expectDetail(theirs, 'edited', editor.username, editedAt);
      // The name keeps a box at least 8rem wide, and who shared the draft
      // one line, whose username is not broken at its hyphen: the header
      // wraps before either gives way.
      const initial = theirs.viewportSize();
      for (const width of [1100, 1280, 1600]) {
        const header = await sharedHeader(theirs, width);
        expect
          .soft(header.name, `name box at ${width}px, in rem`)
          .toBeGreaterThanOrEqual(8 - 0.05);
        expect.soft(header.lines, `Shared by at ${width}px, lines`).toBe(1);
      }
      await theirs.setViewportSize(initial);
      await expect(theirs.getByTestId('builder-name-edit')).toHaveCount(0);
      for (const action of ['paste', 'scenario', 'publish', 'share']) {
        await expect(theirs.getByTestId(`toolbar-${action}`)).toHaveAttribute(
          'aria-disabled',
          'true',
        );
      }
      await expect(theirs.locator('#toolbar-tip-share')).toContainText(
        `Only ${owner.username} can change who has access`,
      );
      await expect(theirs.getByTestId('toolbar-download')).not.toHaveAttribute(
        'aria-disabled',
        'true',
      );

      // Draft History lists the snapshots, newest first, but a viewer can
      // neither restore nor delete one.
      await theirs.getByTestId('toolbar-history').click();
      const history = theirs.getByTestId('history-dialog');
      await expect(history.getByTestId('history-row')).toHaveCount(2);
      await expect(history.getByTestId('history-name').last()).toHaveText(
        'Draft created',
      );
      for (const action of ['restore', 'delete']) {
        await expect(history.getByTestId(`history-${action}`)).toHaveCount(0);
      }
      await expect(
        history.getByRole('button', { name: 'Draft created' }),
      ).toHaveCount(0);
      await theirs.keyboard.press('Escape');
      await expect(history).toHaveCount(0);
    });

    await test.step('only the owner deletes it or changes who has access, and no one else finds it', async () => {
      const path = draftPath(draft);
      const current = await owner.api.get(path);
      const etag = current.headers().etag;
      const shares = await (await owner.api.get(`${path}/shares`)).json();

      for (const user of [viewer, editor]) {
        const deleted = await user.api.delete(path, {
          headers: { 'If-Match': etag },
        });
        expect(deleted.status(), user.username).toBe(403);
      }
      const changed = await viewer.api.put(`${path}/shares`, {
        headers: { 'If-Match': shares.sharesEtag },
        data: { shares: [] },
      });
      expect(changed.status()).toBe(403);
      // Only the owner learns whom the draft can be shared with: everyone
      // else with an account, those it is shared with too.
      const candidates = `${path}/shares/candidates`;
      expect((await editor.api.get(candidates)).status()).toBe(403);
      const offered = (
        await (await owner.api.get(candidates)).json()
      ).users.map((user) => user.username);
      expect(offered).toEqual(
        expect.arrayContaining([
          editor.username,
          viewer.username,
          stranger.username,
        ]),
      );
      expect(offered).not.toContain(owner.username);

      // A viewer deletes no snapshot; the editor's rename left an older one.
      const older = (
        await (await owner.api.get(`${path}/snapshots`)).json()
      ).snapshots.find((snapshot) => !snapshot.current);
      const snapshot = `${path}/snapshots/${older.id}`;
      const refused = await viewer.api.delete(snapshot, {
        headers: { 'If-Match': etag },
      });
      expect(refused.status()).toBe(403);
      expect(
        (
          await stranger.api.delete(snapshot, {
            headers: { 'If-Match': etag },
          })
        ).status(),
      ).toBe(404);

      expect((await stranger.api.get(path)).status()).toBe(404);
      expect((await stranger.api.get(`${path}/shares`)).status()).toBe(404);
      expect((await stranger.api.get(candidates)).status()).toBe(404);
      const listed = await (
        await stranger.api.get(`${API}/builder/drafts`)
      ).json();
      expect(
        [...(listed.drafts || []), ...(listed.shared || [])].map(
          (entry) => entry.id,
        ),
      ).not.toContain(draft.id);
    });
  },
);

test('access that changes while the draft is open', async ({
  sharingUsers,
}) => {
  test.setTimeout(150000);
  const { owner, editor, viewer } = sharingUsers;
  const name = 'Access lab';
  const draft = await seedDraft(owner, name, [
    { user: editor.username, access: 'edit' },
    { user: viewer.username, access: 'view' },
  ]);
  const { page } = owner;

  await test.step('the editor and the owner open it', async () => {
    await openShared(editor, draft);
    await openOwn(owner, draft);
  });

  await test.step('the owner makes the editor a viewer from the toolbar, and keeps saving', async () => {
    const share = page.getByTestId('toolbar-share');
    // The palette finds Share by "send" too.
    await page.getByTestId('builder-canvas').focus();
    await page.keyboard.press('ControlOrMeta+k');
    await page.getByRole('combobox', { name: 'Search commands' }).fill('send');
    await expect(
      page
        .getByTestId('commands-dialog')
        .getByRole('option', { selected: true }),
    ).toContainText('Share…');
    await page.keyboard.press('Enter');
    await shareDialog(page);
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('share-dialog')).toHaveCount(0);
    await share.click();
    const dialog = await shareDialog(page);
    await dialog
      .getByTestId(`share-row-access-${editor.username}`)
      .selectOption('view');
    await expect(
      dialog.getByTestId(`share-row-${editor.username}`),
    ).toContainText('Changed');
    await dialog.getByTestId('share-save').click();
    await expect(dialog).toBeHidden();
    await expect(share).toBeFocused();
    await expect(page).toHaveAnnounced(
      `${name} is now shared with ${editor.username} (can view) and ${viewer.username} (can view).`,
    );

    await rename(page, `${name} (owner)`);
    await expect(page.getByTestId('builder-save-state')).toContainText(SAVED);
    await expect(page.getByTestId('builder-conflict')).toHaveCount(0);
  });

  await test.step("the editor's next change finds they may only view it, and a new draft keeps their work", async () => {
    const { page: theirs } = editor;
    await rename(theirs, 'Kept by the editor');
    const panel = theirs.getByTestId('builder-access-lost');
    await expect(panel).toBeVisible();
    await expect(panel.getByRole('alert')).toHaveText(
      `You can no longer edit this draft. ${owner.username} changed your access to view only.`,
    );
    await expect(theirs.getByTestId('editor-shared-by')).toHaveText(
      `Shared by ${owner.username} · Can view`,
    );
    await expect(theirs.getByTestId('builder-readonly')).toHaveCount(0);
    await expectAccessible(theirs, { soft: true, label: 'Access lost' });

    await panel.getByTestId('access-lost-fork').click();
    await expect(panel).toBeHidden();
    await expect(editorHeading(theirs)).toBeFocused();
    await expect(theirs.getByTestId('editor-shared-by')).toHaveCount(0);
    await expect(theirs.getByTestId('builder-name')).toHaveText(
      'Kept by the editor (local copy)',
    );
    const listed = await (await editor.api.get(`${API}/builder/drafts`)).json();
    expect((listed.drafts || []).map((entry) => entry.title)).toContain(
      'Kept by the editor (local copy)',
    );
  });

  await test.step('the owner removes the viewer, whose card and link then find nothing', async () => {
    const { page: theirs } = viewer;
    await landing(viewer);
    await theirs.getByTestId('drafts-tab-shared').click();
    await expect(theirs.getByTestId(`draft-open-${draft.id}`)).toBeVisible();

    await page.getByTestId('toolbar-share').click();
    const dialog = await shareDialog(page);
    await dialog.getByTestId(`share-row-remove-${viewer.username}`).click();
    await expect(
      dialog.getByTestId(`share-row-${viewer.username}`),
    ).toContainText('Removed when you save');
    await dialog
      .getByTestId(`share-row-access-${editor.username}`)
      .selectOption('edit');
    await dialog.getByTestId('share-save').click();
    await expect(dialog).toBeHidden();

    // The card leaves the list once opening it fails, and focus moves to
    // the tab rather than falling to the page.
    await theirs.getByTestId(`draft-open-${draft.id}`).press('Enter');
    await expect(theirs.getByTestId('builder-error')).toContainText(
      'This draft does not exist, or it is not shared with you.',
    );
    await expect(theirs.getByTestId(`draft-open-${draft.id}`)).toHaveCount(0);
    await expect(theirs.getByTestId('drafts-tab-shared')).toBeFocused();

    const link = encodeURIComponent(`${owner.username}/${draft.id}`);
    await visit(viewer.page, `/builder?draft=${link}`);
    await expect(viewer.page.getByTestId('builder-error')).toContainText(
      'This draft does not exist, or it is not shared with you.',
    );
  });

  await test.step("a newer version the owner saved is named when the editor's change conflicts", async () => {
    const { page: theirs } = editor;
    await openShared(editor, draft);
    await expect(theirs.getByTestId('editor-shared-by')).toHaveText(
      `Shared by ${owner.username} · Can edit`,
    );
    await rename(page, `${name} (owner again)`);
    await expect(page.getByTestId('builder-save-state')).toContainText(SAVED);

    await rename(theirs, 'Too late');
    const panel = theirs.getByTestId('builder-conflict');
    // A UI built with sign-in (VITE_AUTH) names who saved it; the one these
    // tests use is built without, and says someone else.
    await expect(panel.getByRole('alert')).toHaveText(
      new RegExp(
        `^(${owner.username}|Someone else) saved a newer version of this draft, so your changes cannot be written over it\\.$`,
      ),
    );
    await expect(panel.getByTestId('conflict-fork')).toBeVisible();
    await panel.getByTestId('conflict-reload').click();
    await theirs
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Discard and load the server version' })
      .click();
    await expect(panel).toBeHidden();
    await expect(theirs.getByTestId('builder-name')).toHaveText(
      `${name} (owner again)`,
    );
  });

  await test.step('the owner deletes it while the editor has it open', async () => {
    const { page: theirs } = editor;

    // The landing shows the lists it has at once and reads them again; the
    // delete needs the draft as it is now.
    const relisted = waitForApi(page, 'GET', '/builder/drafts');
    await page.getByTestId('editor-back').click();
    await relisted;
    await page.getByTestId(`draft-delete-${draft.id}`).click();
    await page.getByRole('button', { name: 'Delete draft' }).click();
    await expect(page.getByTestId(`draft-open-${draft.id}`)).toHaveCount(0);

    await rename(theirs, 'After the delete');
    const panel = theirs.getByTestId('builder-access-lost');
    await expect(panel.getByRole('alert')).toHaveText(
      'This draft is no longer shared with you, or it was deleted.',
    );
    await expect(theirs.getByTestId('editor-shared-by')).toHaveCount(0);
    await expect(theirs.getByTestId('toolbar-share')).toHaveCount(0);
    const listed = await (await editor.api.get(`${API}/builder/drafts`)).json();
    expect((listed.shared || []).map((entry) => entry.id)).not.toContain(
      draft.id,
    );
  });
});

// Two editors of one draft each save a change made to the draft as they
// opened it. Changes to different devices are merged without asking; a
// device both renamed asks the second to save which name to keep.
test("two editors' changes are merged, and only a name both changed asks which to keep", async ({
  sharingUsers,
}) => {
  test.setTimeout(150000);
  const { owner, editor } = sharingUsers;
  const name = 'Merge lab';
  // Snapping is off: the editor's 10-pixel move leaves server-2 off the
  // grid, and a canvas that snaps draws a node opened off the grid at the
  // nearest grid point, not where the document puts it.
  const seeded = {
    ...threeServers(name),
    grid: { enabled: true, size: 16, snap: false },
  };
  const ids = Object.fromEntries(
    ['server', 'server-2', 'server-3'].map((hostname) => [
      hostname,
      deviceId(seeded, hostname),
    ]),
  );
  const draft = await seedDraft(
    owner,
    name,
    [{ user: editor.username, access: 'edit' }],
    seeded,
  );
  const { page } = owner;
  const { page: theirs } = editor;
  // Who the merge names: the UI these tests use is built without sign-in,
  // and names no one.
  const from = `(${owner.username}|another editor)`;
  let moved;

  await test.step('both open it', async () => {
    await openShared(editor, draft);
    await expect(theirs.getByTestId('builder-save-state')).toContainText(SAVED);
    await openOwn(owner, draft);
  });

  await test.step("the owner renames server; the editor's move of server-2 is merged with it", async () => {
    await renameDevice(page, ids.server, 'server', 'web');
    await expect(page.getByTestId('builder-save-state')).toContainText(SAVED);

    const before = (await serverDocument(owner, draft)).nodes.find(
      (node) => node.id === ids['server-2'],
    );
    moved = { x: before.position.x + 10, y: before.position.y };

    // The editor opened the draft before the owner's save: theirs meets it.
    const refused = nextSave(theirs);
    await deviceNode(theirs, 'server-2').click();
    await theirs.keyboard.press('Shift+ArrowRight');
    expect((await refused).status()).toBe(412);

    await expect(theirs).toHaveAnnounced(
      new RegExp(`Merged ${from}'s changes with yours\\.`),
    );
    await expect(theirs.getByTestId('builder-save-state')).toContainText(SAVED);
    await expect(theirs.getByTestId('builder-conflict')).toHaveCount(0);

    const doc = await serverDocument(owner, draft);
    expect(hostnamesOf(doc)).toEqual(['server-2', 'server-3', 'web']);
    expect(
      doc.nodes.find((node) => node.id === ids['server-2']).position,
    ).toEqual(moved);
  });

  await test.step("both sessions show the rename and the move once they open the draft's link again", async () => {
    const link = encodeURIComponent(`${draft.owner}/${draft.id}`);

    for (const user of [owner, editor]) {
      await visit(user.page, `/builder?draft=${link}`);
      await expect(user.page.getByTestId('builder-canvas')).toBeVisible({
        timeout: 20000,
      });
      await expect(user.page.getByTestId('builder-save-state')).toContainText(
        SAVED,
      );
      await expect(deviceNode(user.page, 'web')).toHaveCount(1);
      await expect(deviceNode(user.page, 'server')).toHaveCount(0);
      await expect(deviceNode(user.page, 'server-2')).toHaveCSS(
        'transform',
        `matrix(1, 0, 0, 1, ${moved.x}, ${moved.y})`,
      );
    }
  });

  await test.step('both rename server-3: the second to save chooses which name to keep', async () => {
    await renameDevice(page, ids['server-3'], 'server-3', 'db-owner');
    await expect(page.getByTestId('builder-save-state')).toContainText(SAVED);

    // The editor opened the draft before the owner's save.
    expect(
      (
        await renameDevice(theirs, ids['server-3'], 'server-3', 'db-editor')
      ).status(),
    ).toBe(412);
    const panel = theirs.getByTestId('builder-conflict');
    await expect(panel).toBeVisible();
    await expect(panel.getByTestId('conflict-merge-note')).toHaveText(
      new RegExp(
        `^1 change of yours clashes with ${from}'s\\. Review and merge to choose which to keep\\.$`,
      ),
    );
    await expect(panel.getByTestId('conflict-fork')).toBeVisible();
    await expect(panel.getByTestId('conflict-reload')).toBeVisible();
    await expectAccessible(theirs, {
      include: '[data-testid="builder-conflict"]',
      label: 'Conflict panel with Review and merge',
    });

    await panel.getByTestId('conflict-merge').click();
    const dialog = theirs.getByTestId('merge-dialog');
    const save = dialog.getByTestId('merge-save');
    const count = dialog.getByTestId('merge-count');
    await expect(dialog).toBeVisible();
    await expect(
      dialog.getByRole('heading', {
        name: new RegExp(`^Merge changes from ${from}$`),
      }),
    ).toBeVisible();
    await expect(
      dialog.getByRole('group', { name: 'server-3 name' }),
    ).toBeVisible();
    // Neither is chosen, and focus is on the first.
    await expect(dialog.getByTestId('merge-clash-0-mine')).toBeFocused();
    await expect(dialog.getByTestId('merge-clash-0-mine')).not.toBeChecked();
    await expect(dialog.getByTestId('merge-clash-0-theirs')).not.toBeChecked();
    await expect(count).toHaveText('0 of 1 chosen');
    await expect(save).toHaveAttribute('aria-disabled', 'true');
    await expectAccessible(theirs, {
      include: '[data-testid="merge-dialog"]',
      label: 'Merge dialog',
    });

    // Save merged says what is missing, and the dialog stays. Playwright
    // waits for an aria-disabled control to be enabled before it clicks;
    // the button still takes the click.
    await save.click({ force: true });
    await expect(dialog.getByTestId('merge-error')).toHaveText(
      'Choose which version to keep of every field: 0 of 1 chosen.',
    );
    await expect(dialog.getByTestId('merge-clash-0-mine')).toBeFocused();

    await dialog.getByRole('radio', { name: 'Keep mine: db-editor' }).check();
    await expect(count).toHaveText('1 of 1 chosen');
    await expect(save).not.toHaveAttribute('aria-disabled');

    const saved = nextSave(theirs);
    await save.click();
    expect((await saved).ok()).toBeTruthy();
    await expect(dialog).toHaveCount(0);
    await expect(panel).toHaveCount(0);
    await expect(editorHeading(theirs)).toBeFocused();
    await expect(theirs.getByTestId('builder-save-state')).toContainText(SAVED);

    expect(hostnamesOf(await serverDocument(owner, draft))).toEqual([
      'db-editor',
      'server-2',
      'web',
    ]);
  });
});

// A link from the Configs page names a topology. The draft that published
// its diagram opens for its owner and for those it is shared with for
// editing; anyone else edits the diagram in a draft of their own.
test('a published topology opens in the draft that published it only for those who may edit that draft', async ({
  sharingUsers,
}) => {
  test.setTimeout(120000);
  const { owner, editor, viewer } = sharingUsers;
  const topology = `e2e-shared-${Date.now().toString(36)}`;
  const link = `/builder?topology=${encodeURIComponent(topology)}`;
  const draft = await seedDraft(owner, topology, [
    { user: editor.username, access: 'edit' },
    { user: viewer.username, access: 'view' },
  ]);
  // Sharing gave the draft a new ETag.
  const current = await owner.api.get(draftPath(draft));
  const published = await owner.api.post(`${draftPath(draft)}/publish`, {
    headers: { 'If-Match': current.headers().etag },
    data: { mode: 'topology', topology: { name: topology, action: 'create' } },
  });

  try {
    expect(published.ok(), await published.text()).toBeTruthy();

    // The drafts a user's page makes from now on.
    const createsOf = ({ page }) => {
      const creates = [];
      page.on('request', (request) => {
        if (
          request.method() === 'POST' &&
          new URL(request.url()).pathname === `${API}/builder/drafts`
        ) {
          creates.push(request.url());
        }
      });

      return creates;
    };
    const opened = async ({ page }) => {
      await visit(page, link);
      await expect(page.getByTestId('builder-canvas')).toBeVisible({
        timeout: 20000,
      });
      await expect(page.getByTestId('builder-name')).toHaveText(topology);
    };
    const names = (who, id) => (url) =>
      url.searchParams.get('draft') === `${who}/${id}`;

    await test.step('someone it is shared with for editing opens that draft', async () => {
      const creates = createsOf(editor);
      await opened(editor);
      await expect(editor.page).toHaveURL(names(owner.username, draft.id));
      await expect(editor.page).toHaveAnnounced(
        `Opened ${owner.username}'s draft ${topology}. You can edit it; others may be editing too.`,
      );
      await expect(editor.page.getByTestId('editor-shared-by')).toContainText(
        owner.username,
      );
      // Nothing calls it a draft of their own, and none is made.
      await expect(editor.page).not.toHaveAnnounced(/in your draft|new draft/);
      expect(creates, 'draft creates').toEqual([]);
    });

    await test.step('someone who may only view that draft gets a draft of their own', async () => {
      const creates = createsOf(viewer);
      const made = waitForApi(viewer.page, 'POST', '/builder/drafts');
      await opened(viewer);
      const own = await (await made).json();
      expect(own.owner).toBe(viewer.username);
      expect(own.id).not.toBe(draft.id);
      expect(own.sourceToken).toMatch(/^builder-doc\//);
      await expect(viewer.page).toHaveURL(names(viewer.username, own.id));
      await expect(viewer.page).toHaveAnnounced(
        `Opened topology ${topology} in the Builder as a new draft.`,
      );
      await expect(viewer.page.getByTestId('builder-save-state')).toContainText(
        SAVED,
      );

      // The link opens that draft again, and makes no other.
      await opened(viewer);
      await expect(viewer.page).toHaveURL(names(viewer.username, own.id));
      await expect(viewer.page).toHaveAnnounced(
        `Opened topology ${topology} in the Builder, in your draft of it.`,
      );
      expect(creates, 'draft creates').toHaveLength(1);
    });

    await test.step('its owner opens it too', async () => {
      const creates = createsOf(owner);
      await opened(owner);
      await expect(owner.page).toHaveURL(names(owner.username, draft.id));
      await expect(owner.page).toHaveAnnounced(
        `Opened topology ${topology} in the Builder, in the draft that published it.`,
      );
      expect(creates, 'draft creates').toEqual([]);
    });
  } finally {
    await owner.api
      .delete(`${API}/configs/Topology/${topology}`)
      .catch(() => {});
  }
});

// A user of the sharing role may read no experiment and no other user's
// draft; the administrator may do both.
test("a card offers what its user may do: Exp, Delete on another user's draft, and Publish", async ({
  sharingUsers,
  browser,
  playwright,
}, testInfo) => {
  test.setTimeout(120000);
  const { owner, editor, viewer } = sharingUsers;
  const { baseURL, viewport } = testInfo.project.use;
  const nonce = `${Date.now().toString(36)}${Math.floor(Math.random() * 1e6).toString(36)}`;
  const topology = `e2e-exp-topo-${nonce}`;
  const experiment = `e2e-exp-${nonce}`;
  const theirs = await seedDraft(owner, `Other's draft ${nonce}`);

  const guest = await playwright.request.newContext({ baseURL });
  const session = await signIn(guest, ADMIN_USER, ADMIN_PASS);
  const admin = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { 'X-Phenix-Auth-Token': `bearer ${session.token}` },
  });
  const context = await browser.newContext({ baseURL, viewport });
  await context.addInitScript(recordAnnouncements);
  await context.addInitScript((signedIn) => {
    sessionStorage.setItem('phenix.user', signedIn.user.username);
    sessionStorage.setItem('phenix.token', signedIn.token);
    sessionStorage.setItem('phenix.role', JSON.stringify(signedIn.user.role));
    sessionStorage.setItem('phenix.auth', 'true');
  }, session);
  const page = await context.newPage();
  let made = null;

  // The published diagram of the topology, as `api`'s user is told of it.
  const rowOf = async (api) => {
    const listed = await api.get(`${API}/builder/documents`);
    expect(listed.ok(), await listed.text()).toBeTruthy();

    return ((await listed.json()).documents || []).find(
      (entry) => entry.target === topology,
    );
  };

  try {
    // The administrator publishes a diagram with an experiment.
    const created = await admin.post(`${API}/builder/drafts`, {
      data: { title: topology, document: labDocument(topology) },
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    made = await created.json();
    const published = await admin.post(`${draftPath(made)}/publish`, {
      headers: { 'If-Match': created.headers().etag },
      data: {
        mode: 'topology-experiment',
        topology: { name: topology, action: 'create' },
        experiment: { name: experiment, action: 'create' },
      },
    });
    expect(published.status(), await published.text()).toBe(200);
    const row = await rowOf(admin);
    expect(row.experiment).toBe(experiment);

    await test.step('a user who may not get the experiment is not told its name, and has no Exp', async () => {
      const seen = await rowOf(viewer.api);
      expect(seen.id).toBe(row.id);
      expect(seen).not.toHaveProperty('experiment');

      await landing(viewer);
      await viewer.page.getByTestId('drafts-tab-published').click();
      await expect(
        viewer.page.getByTestId(`draft-open-${row.id}`),
      ).toBeVisible();
      await expect(
        viewer.page.getByTestId(`published-experiment-${row.id}`),
      ).toHaveCount(0);
      // Nor are other users' drafts listed for them.
      await expect(viewer.page.getByTestId('drafts-tab-others')).toHaveCount(0);
    });

    await test.step('the administrator has Exp, which opens the experiment', async () => {
      await landing({ page });
      await page.getByTestId('drafts-tab-published').click();
      const exp = page.getByTestId(`published-experiment-${row.id}`);
      await expect(exp).toHaveAccessibleName(
        `Exp: open experiment ${experiment}`,
      );
      await exp.click();
      await expect(page).toHaveURL(new RegExp(`/experiment/${experiment}$`));
    });

    await test.step("the administrator selects other users' drafts and deletes them at once, told whose they are", async () => {
      const first = await seedDraft(owner, `Left behind ${nonce} a`);
      const second = await seedDraft(editor, `Left behind ${nonce} b`);
      const mine = await seedDraft(viewer, `Kept ${nonce}`);

      await landing({ page });
      await page.getByTestId('drafts-tab-others').click();
      await expect(page.getByTestId('bulk-bar-others')).toHaveAccessibleName(
        "Bulk actions: Other users' drafts",
      );
      // Another user's drafts are deleted from here, never shared.
      await expect(page.getByTestId('bulk-share-others')).toHaveCount(0);
      await expect(
        page.getByTestId(`card-select-${first.id}`),
      ).toHaveAccessibleName(
        new RegExp(
          `^Select Left behind ${nonce} a by ${owner.username}, updated .+$`,
        ),
      );
      await page.getByTestId(`card-select-${first.id}`).check();
      await page.getByTestId(`card-select-${second.id}`).check();
      await expect(page.getByTestId('bulk-count-others')).toHaveText(
        /^2 of \d+ selected$/,
      );

      await page.getByTestId('bulk-delete-others').click();
      const confirm = page.getByRole('alertdialog', {
        name: 'Delete 2 drafts?',
      });
      await expect(confirm).toBeVisible();
      const message = await confirm.locator('p').textContent();
      expect(message).toContain(`Left behind ${nonce} a`);
      expect(message).toContain(`Left behind ${nonce} b`);
      // Each owner is named, once.
      expect(message).toMatch(
        new RegExp(
          `\\. They belong to (${owner.username} and ${editor.username}|${editor.username} and ${owner.username})\\. ` +
            'The drafts and their whole histories are removed from the server\\. This cannot be undone\\.$',
        ),
      );
      await expect(confirm.getByTestId('confirm-cancel')).toBeFocused();
      await confirm.getByTestId('confirm-accept').click();

      await expect(page.getByTestId(`draft-open-${first.id}`)).toHaveCount(0);
      await expect(page.getByTestId(`draft-open-${second.id}`)).toHaveCount(0);
      await expect(page).toHaveAnnounced('Deleted 2 drafts.');
      expect((await owner.api.get(draftPath(first))).status()).toBe(404);
      expect((await editor.api.get(draftPath(second))).status()).toBe(404);
      // What was not selected is left.
      await expect(page.getByTestId(`draft-open-${mine.id}`)).toBeVisible();
      expect((await viewer.api.get(draftPath(mine))).status()).toBe(200);
    });

    await test.step("the administrator deletes another user's draft from its card, after being asked", async () => {
      const listed = await (await admin.get(`${API}/builder/drafts`)).json();
      expect(
        (listed.shared || []).find((entry) => entry.id === theirs.id),
      ).toMatchObject({ owner: owner.username, via: 'role', canDelete: true });

      await landing({ page });
      await page.getByTestId('drafts-tab-others').click();
      const remove = page.getByTestId(`draft-delete-${theirs.id}`);
      await expect(remove).toHaveAccessibleName(
        new RegExp(`^Delete Other's draft ${nonce} by ${owner.username}, `),
      );
      await remove.click();
      const confirm = page.getByRole('alertdialog', {
        name: new RegExp(`^Delete draft Other's draft ${nonce} by `),
      });
      await expect(confirm).toBeVisible();
      await confirm.getByTestId('confirm-accept').click();
      await expect(page.getByTestId(`draft-open-${theirs.id}`)).toHaveCount(0);
      expect((await owner.api.get(draftPath(theirs))).status()).toBe(404);
    });

    await test.step('Publish on a card finds that the draft may now only be viewed, and publishes nothing', async () => {
      const name = `Lent ${nonce}`;
      const lent = await seedDraft(owner, name, [
        { user: editor.username, access: 'edit' },
      ]);
      const mine = editor.page;
      await landing(editor);
      await mine.getByTestId('drafts-tab-shared').click();
      const publish = mine.getByTestId(`draft-publish-${lent.id}`);
      await expect(publish).toBeVisible();

      // The owner lets them only view it, after their list was read.
      const path = `${draftPath(lent)}/shares`;
      const read = await (await owner.api.get(path)).json();
      const saved = await owner.api.put(path, {
        headers: { 'If-Match': read.sharesEtag },
        data: { shares: [{ user: editor.username, access: 'view' }] },
      });
      expect(saved.ok(), await saved.text()).toBeTruthy();

      await publish.focus();
      await mine.keyboard.press('Enter');
      await expect(mine.getByTestId('builder-error')).toContainText(
        `You can view ${name} but not change it, so you cannot publish it.`,
      );
      await expect(mine.getByRole('dialog')).toHaveCount(0);
      // The drafts stay, read again: the card has no Publish any more, and
      // focus, which was on it, is on the card's Open (WCAG 2.4.3).
      await expect(mine.getByTestId('builder-canvas')).toHaveCount(0);
      await expect(mine.getByTestId(`draft-open-${lent.id}`)).toBeVisible();
      await expect(publish).toHaveCount(0);
      await expect(mine.getByTestId(`draft-open-${lent.id}`)).toBeFocused();
      await expect(mine.getByTestId(`draft-access-${lent.id}`)).toContainText(
        'Can view',
      );
    });
  } finally {
    await admin
      .delete(`${API}/configs/Experiment/${experiment}`)
      .catch(() => {});
    await admin.delete(`${API}/configs/Topology/${topology}`).catch(() => {});
    if (made) {
      const current = await admin.get(draftPath(made)).catch(() => null);
      await admin
        .delete(draftPath(made), {
          headers: { 'If-Match': current?.headers().etag || '' },
        })
        .catch(() => {});
    }
    await context.close().catch(() => {});
    await admin.dispose();
    await guest.dispose();
  }
});

test('several drafts are shared at once: the people are added to each, no one is removed, and what failed is listed', async ({
  sharingUsers,
}) => {
  test.setTimeout(120000);
  const { owner, editor, viewer } = sharingUsers;
  const { page } = owner;
  const plain = await seedDraft(owner, 'Bulk plain');
  // Shared already: the viewer stays as they are, and the editor, who may
  // only view it, gets the access chosen for the batch.
  const lent = await seedDraft(owner, 'Bulk lent', [
    { user: viewer.username, access: 'view' },
    { user: editor.username, access: 'view' },
  ]);
  const refused = await seedDraft(owner, 'Bulk refused');
  const sharesOf = async (draft) =>
    (
      await (await owner.api.get(`${draftPath(draft)}/shares`)).json()
    ).shares.map(({ user, access }) => ({ user, access }));
  const select = (draft) => page.getByTestId(`card-select-${draft.id}`);

  await landing(owner);
  const count = page.getByTestId('bulk-count-mine');
  const share = page.getByTestId('bulk-share-mine');
  const dialog = page.getByTestId('bulk-share-dialog');
  const field = dialog.getByTestId('bulk-share-user');
  const error = dialog.getByTestId('bulk-share-user-error');

  await test.step('Share selected opens one dialog for the selected drafts', async () => {
    await expect(share).toHaveText('Share selected');
    await expect(share).toHaveAttribute('aria-disabled', 'true');
    await expect(share).toHaveAccessibleDescription('0 of 3 selected');
    for (const draft of [plain, lent, refused]) {
      await select(draft).check();
    }
    await expect(count).toHaveText('3 of 3 selected');
    await expect(share).not.toHaveAttribute('aria-disabled', 'true');

    await share.press('Enter');
    await expect(dialog).toBeVisible();
    await expect(dialog).toHaveAccessibleName('Share 3 drafts');
    await expect(dialog).toHaveAccessibleDescription(
      /^The people you add get access to every selected draft, with the access you choose\. .+ No one is removed\. They find the drafts under Shared Drafts\.$/,
    );
    await expect(field).toBeFocused();
    await expect(dialog.getByTestId('bulk-share-users-note')).toHaveText('');
    await expect(dialog.getByTestId('bulk-share-empty')).toHaveText(
      'No one yet.',
    );
    const submit = dialog.getByTestId('bulk-share-submit');
    await expect(submit).toHaveText('Share 3 drafts');
    await expect(submit).toHaveAttribute('aria-disabled', 'true');
  });

  await test.step('people are named as in the Share dialog, and a name that cannot be added is refused where it was typed', async () => {
    await field.fill(owner.username);
    await field.press('Enter');
    await expect(error).toHaveText('You own these drafts.');
    await expect(field).toBeFocused();
    await expect(field).toHaveAttribute('aria-invalid', 'true');

    // The list offers the users the drafts may be shared with; typing
    // filters it, and Enter takes the one in view.
    await field.fill(editor.username);
    const options = dialog.getByRole('listbox', { name: 'Users' });
    await expect(options.getByRole('option')).toHaveCount(1);
    await field.press('ArrowDown');
    await field.press('Enter');
    await expect(options).toBeHidden();
    await expect(field).toHaveValue(new RegExp(`\\(${editor.username}\\)$`));
    await expect(error).toHaveText('');
    await field.press('Enter');
    await expect(
      dialog.getByTestId(`bulk-share-row-${editor.username}`),
    ).toBeVisible();
    await expect(field).toHaveValue('');
    await expect(field).toBeFocused();

    await field.fill(editor.username);
    await field.press('Enter');
    await expect(error).toHaveText(
      `${editor.username} is already in the list.`,
    );

    // Someone added by mistake is removed again, and focus moves on.
    await field.fill(viewer.username);
    await field.press('Enter');
    const row = dialog.getByTestId(`bulk-share-row-${viewer.username}`);
    await expect(row).toBeVisible();
    await row
      .getByRole('button', { name: `Remove ${viewer.username}` })
      .click();
    await expect(row).toHaveCount(0);
    await expect(field).toBeFocused();

    // One access for everyone added.
    await dialog.getByTestId('bulk-share-access').selectOption('edit');
    await expectAccessible(page, {
      include: '[data-testid="bulk-share-dialog"]',
      label: 'bulk Share dialog',
    });
  });

  await test.step('a draft that could not be shared is listed with why, and stays selected', async () => {
    // The server refuses the person for one of the drafts.
    const REFUSED = `**${draftPath(refused)}/shares`;
    await page.route(REFUSED, (route) =>
      route.request().method() === 'PUT'
        ? route.fulfill({
            status: 422,
            json: {
              message: 'Some people could not be added.',
              errors: [{ user: editor.username, reason: 'unknown-user' }],
            },
          })
        : route.fallback(),
    );

    await dialog.getByTestId('bulk-share-submit').click();
    const summary = dialog.getByTestId('bulk-summary');
    await expect(summary).toBeVisible();
    await expect(summary).toBeFocused();
    await expect(summary).toHaveAccessibleName(
      '1 of 3 drafts could not be shared. The other 2 were shared.',
    );
    await expect(summary.getByRole('listitem')).toHaveText([
      new RegExp(
        `^Bulk refused, updated .+: No user named ${editor.username}\\. People must have signed in to phēnix at least once\\.$`,
      ),
    ]);
    // Nothing more to do here but close: the form is gone.
    await expect(summary.getByTestId('bulk-summary-dismiss')).toHaveCount(0);
    await expect(field).toHaveCount(0);
    await expect(dialog.getByTestId('bulk-share-submit')).toHaveCount(0);
    await expectAccessible(page, {
      include: '[data-testid="bulk-share-dialog"]',
      label: 'bulk Share dialog with a summary',
    });

    // The other two were shared: the editor was added, or raised to the
    // access chosen, and the viewer was left as they were.
    expect(await sharesOf(plain)).toEqual([
      { user: editor.username, access: 'edit' },
    ]);
    // The server lists them by name.
    expect(await sharesOf(lent)).toEqual([
      { user: editor.username, access: 'edit' },
      { user: viewer.username, access: 'view' },
    ]);
    expect(await sharesOf(refused)).toEqual([]);

    await dialog.getByTestId('bulk-share-close').click();
    await expect(dialog).toHaveCount(0);
    await page.unroute(REFUSED);
    await expect(share).toBeFocused();
    // The cards say who they are shared with now; only the draft that was
    // not shared is still selected.
    await expect(page.getByTestId(`draft-shared-with-${plain.id}`)).toHaveText(
      `Shared with ${editor.username}`,
    );
    await expect(select(refused)).toBeChecked();
    await expect(select(plain)).not.toBeChecked();
    await expect(select(lent)).not.toBeChecked();
    await expect(count).toHaveText('1 of 3 selected');
  });

  await test.step('tried again, the draft is shared, the dialog closes and the page says so', async () => {
    await share.click();
    await expect(dialog).toHaveAccessibleName('Share 1 draft');
    await field.fill(editor.username);
    await field.press('Enter');
    const submit = dialog.getByTestId('bulk-share-submit');
    await expect(submit).toHaveText('Share 1 draft');
    await submit.click();

    await expect(dialog).toHaveCount(0);
    await expect(page).toHaveAnnounced(
      `Shared 1 draft with ${editor.username} (can view).`,
    );
    await expect(share).toBeFocused();
    await expect(count).toHaveText('0 of 3 selected');
    expect(await sharesOf(refused)).toEqual([
      { user: editor.username, access: 'view' },
    ]);

    // The editor finds all three under Shared Drafts, with their access.
    const listed = await (await editor.api.get(`${API}/builder/drafts`)).json();
    const access = Object.fromEntries(
      (listed.shared || []).map((entry) => [entry.id, entry.access]),
    );
    expect(access).toMatchObject({
      [plain.id]: 'edit',
      [lent.id]: 'edit',
      [refused.id]: 'view',
    });
  });

  await test.step('a draft the list was read before is deleted with the others, although sharing changed it', async () => {
    // Sharing gave each draft a new ETag, which its card took.
    for (const draft of [plain, lent, refused]) {
      await select(draft).check();
    }
    await page.getByTestId('bulk-delete-mine').click();
    const confirm = page.getByRole('alertdialog', { name: 'Delete 3 drafts?' });
    await expect(confirm.locator('p')).toContainText(
      '3 of them are shared: the people with access lose it too.',
    );
    const statuses = [];
    page.on('response', (response) => {
      if (response.request().method() === 'DELETE') {
        statuses.push(response.status());
      }
    });
    await confirm.getByTestId('confirm-accept').click();
    await expect(page).toHaveAnnounced('Deleted 3 drafts.');
    expect(statuses).toEqual([204, 204, 204]);
    // The row went with the last card: focus moves to the tab.
    await expect(page.getByTestId('bulk-bar-mine')).toHaveCount(0);
    await expect(page.getByTestId('drafts-tab-mine')).toBeFocused();
  });
});

test('Download selected on Shared Drafts saves a Builder file of each selected draft shared with the user', async ({
  sharingUsers,
}) => {
  test.setTimeout(120000);
  const { owner, viewer } = sharingUsers;
  const { page } = viewer;
  // Names that are file names as they are.
  const names = ['shared-download-alfa', 'shared-download-bravo'];
  const drafts = [];
  for (const name of names) {
    drafts.push(
      await seedDraft(owner, name, [{ user: viewer.username, access: 'view' }]),
    );
  }

  await landing(viewer);
  await page.getByTestId('drafts-tab-shared').click();
  const count = page.getByTestId('bulk-count-shared');
  const download = page.getByTestId('bulk-download-shared');

  await test.step('the row has Select all, the count and Download selected, with a note on several downloads', async () => {
    await expect(page.getByTestId('bulk-bar-shared')).toHaveAccessibleName(
      'Bulk actions: Shared Drafts',
    );
    await expect(count).toHaveText('0 of 2 selected');
    await expect(download).toHaveAccessibleDescription(
      '0 of 2 selected Download selected saves a file for each; your browser may ask to allow several downloads.',
    );
    await expect(download).toHaveAttribute('aria-disabled', 'true');
    // A share lets the user download a draft, never share or delete it.
    await expect(page.getByTestId('bulk-share-shared')).toHaveCount(0);
    await expect(page.getByTestId('bulk-delete-shared')).toHaveCount(0);

    for (const draft of drafts) {
      await page.getByTestId(`card-select-${draft.id}`).check();
    }
    await expect(count).toHaveText('2 of 2 selected');
    await expectAccessible(page, {
      include: '#panel-shared',
      label: 'Shared Drafts with a selection and Download selected',
    });
  });

  await test.step('Download selected saves each, in the order of the cards', async () => {
    const order = (
      await page
        .getByTestId('drafts-list-shared')
        .locator(':scope > li')
        .evaluateAll((cards) =>
          cards.map((card) => card.dataset.testid.replace(/^draft-card-/, '')),
        )
    ).map((id) => names[drafts.findIndex((draft) => draft.id === id)]);
    const files = [];

    page.on('download', (file) => files.push(file));
    await download.click();
    await expect(page).toHaveAnnounced('Downloaded 2 drafts.');
    await expect.poll(() => files.length).toBe(2);

    const saved = [];
    for (const file of files) {
      saved.push({
        name: file.suggestedFilename(),
        doc: JSON.parse(fs.readFileSync(await file.path(), 'utf8')),
      });
    }
    expect(saved.map((file) => file.name)).toEqual(
      order.map((name) => `${name}.json`),
    );
    expect(saved.map((file) => file.doc.metadata.name)).toEqual(order);
    await expect(count).toHaveText('2 of 2 selected');
    await expect(download).toBeFocused();
    await expect(page.getByTestId('bulk-summary')).toHaveCount(0);
  });
});

test('mistakes and changes from elsewhere in the Share dialog', async ({
  sharingUsers,
}) => {
  test.setTimeout(120000);
  const { owner, editor, viewer } = sharingUsers;
  // A long unbroken name, so the dialog title must wrap at 320 pixels.
  const draft = await seedDraft(
    owner,
    'Mistakes_lab_with_a_really_long_unbroken_name_2026',
  );
  const { page } = owner;
  // The users the server offers, less those of other tests, and someone
  // whose account is gone by the time the list is saved.
  const nobody = `nobody-${Date.now()}`;
  const candidates = '**/api/v1/builder/drafts/*/*/shares/candidates';
  let failUsers = false;
  await page.route(candidates, async (route) => {
    if (failUsers) {
      return route.fulfill({ status: 503, json: { message: 'try later' } });
    }

    const response = await route.fetch();
    const { users } = await response.json();
    const ours = [owner, editor, viewer].map((user) => user.username);

    return route.fulfill({
      response,
      json: {
        users: [
          ...users.filter((user) => ours.includes(user.username)),
          { username: nobody, name: '' },
        ],
      },
    });
  });

  await landing(owner);
  await page.getByTestId(`draft-share-${draft.id}`).click();
  const dialog = await shareDialog(page);
  const field = dialog.getByTestId('share-user');
  const error = dialog.getByTestId('share-user-error');

  await test.step('the owner, and someone already listed, are refused where they were typed', async () => {
    await field.fill(owner.username);
    await field.press('Enter');
    await expect(error).toHaveText('You own this draft.');
    await expect(field).toBeFocused();
    await expect(field).toHaveAttribute('aria-invalid', 'true');

    await field.fill(editor.username);
    await field.press('Enter');
    await expect(
      dialog.getByTestId(`share-row-${editor.username}`),
    ).toBeVisible();
    await field.fill(editor.username);
    await field.press('Enter');
    await expect(error).toHaveText(`${editor.username} already has access.`);
    await expect(
      dialog.getByTestId(`share-row-access-${editor.username}`),
    ).toBeFocused();
  });

  await test.step('someone the server does not know is named in a summary that takes focus', async () => {
    // Refused where it was typed, when the list does not offer them, once
    // the users are read (until then the server checks the name on save).
    await expect(dialog.getByTestId('share-users-note')).toHaveText('');
    await field.fill(`${nobody}-typed`);
    await field.press('Enter');
    await expect(error).toHaveText(`No user named ${nobody}-typed.`);
    await expect(field).toBeFocused();

    await dialog.getByTestId('share-access').selectOption('edit');
    await field.fill(nobody);
    await field.press('Enter');
    // The next person starts at Can view again.
    await expect(dialog.getByTestId('share-access')).toHaveValue('view');
    await dialog.getByTestId('share-save').click();
    const summary = dialog.getByTestId('share-summary');
    await expect(summary).toBeFocused();
    await expect(summary).toContainText(`No user named ${nobody}.`);

    await summary.getByRole('button').first().click();
    await expect(
      dialog.getByTestId(`share-row-access-${nobody}`),
    ).toBeFocused();
    await dialog.getByTestId(`share-row-remove-${nobody}`).click();
    await dialog.getByTestId('share-save').click();
    await expect(dialog).toBeHidden();
  });

  await test.step('a change saved in another tab is merged in, and saved again', async () => {
    const other = await owner.context.newPage();
    await landing({ page: other });

    await page.getByTestId(`draft-share-${draft.id}`).click();
    const first = await shareDialog(page);
    await other.getByTestId(`draft-share-${draft.id}`).click();
    const second = await shareDialog(other);
    await second.getByTestId('share-user').fill(viewer.username);
    await second.getByTestId('share-user').press('Enter');
    await second.getByTestId('share-save').click();
    await expect(second).toBeHidden();

    // The editor was added as Can view, above.
    await first
      .getByTestId(`share-row-access-${editor.username}`)
      .selectOption('edit');
    await first.getByTestId('share-save').click();
    const alert = first.getByTestId('share-alert');
    await expect(alert).toBeFocused();
    await expect(alert).toContainText(
      'Sharing for this draft changed in another tab or window.',
    );
    await expect(
      first.getByTestId(`share-row-${viewer.username}`),
    ).toBeVisible();
    await expect(
      first.getByTestId(`share-row-access-${editor.username}`),
    ).toHaveValue('edit');
    await first.getByTestId('share-save').click();
    await expect(first).toBeHidden();

    const saved = await (
      await owner.api.get(`${draftPath(draft)}/shares`)
    ).json();
    expect(
      saved.shares.map(({ user, access }) => `${user}:${access}`).sort(),
    ).toEqual([`${editor.username}:edit`, `${viewer.username}:view`].sort());
    await other.close();
  });

  await test.step('adding waits until the list has loaded', async () => {
    const shares = `**/builder/drafts/*/${draft.id}/shares`;
    await page.route(shares, async (route) => {
      if (route.request().method() === 'GET') {
        await new Promise((resolve) => setTimeout(resolve, 1500));
      }
      await route.continue();
    });
    await page.getByTestId(`draft-share-${draft.id}`).click();
    const slow = page.getByTestId('share-dialog');
    const typed = slow.getByTestId('share-user');
    await typed.fill(editor.username);
    await typed.press('Enter');
    await expect(slow.getByTestId('share-user-error')).toHaveText(
      'Wait until the list has loaded.',
    );
    await expect(typed).toBeFocused();
    await expect(typed).toHaveValue(editor.username);
    await expect(slow.getByTestId('share-loading')).toHaveText('');
    await expect(slow.getByTestId('share-user-error')).toHaveText('');
    await page.unroute(shares);
    await slow.getByTestId('share-cancel').click();
    await expect(slow).toBeHidden();
  });

  await test.step('a save refused because the session ended signs in again, and says so in the dialog', async () => {
    const shares = `**/builder/drafts/*/${draft.id}/shares`;
    await page.route(shares, (route) =>
      route.request().method() === 'PUT'
        ? route.fulfill({ status: 401, json: { message: 'unauthorized' } })
        : route.continue(),
    );
    await page.getByTestId(`draft-share-${draft.id}`).click();
    const dialog = await shareDialog(page);
    const alert = dialog.getByTestId('share-alert');
    const save = dialog.getByTestId('share-save');
    await dialog
      .getByTestId(`share-row-access-${viewer.username}`)
      .selectOption('edit');
    await save.click();
    const signIn = page.getByRole('dialog', { name: 'Sign in again' });
    await expect(signIn).toBeVisible();
    await expect(alert).toHaveText(
      'Your session has ended. Sign in again to continue.',
    );

    await page.unroute(shares);
    await signIn.getByLabel('Password').fill(USER_PASS);
    await signIn.getByRole('button', { name: 'Sign in' }).click();
    await expect(signIn).toBeHidden();
    // The page's own announcement waits behind the Share dialog.
    await expect(alert).toHaveText('');
    await expect(dialog.getByTestId('share-status')).toHaveText(
      'Signed in again. Save to apply your changes.',
    );
    await expect(save).toBeFocused();
    await save.click();
    await expect(dialog).toBeHidden();
  });

  await test.step('the list says when no one is left to add, and a failed read offers Retry', async () => {
    await page.getByTestId(`draft-share-${draft.id}`).click();
    const full = await shareDialog(page);
    const add = full.getByTestId('share-add');
    await expect(full.getByTestId('share-users-note')).toHaveText('');
    await full.getByTestId('share-user-toggle').click();
    await full.getByRole('option', { name: nobody }).click();
    await add.click();
    await expect(full.getByTestId('share-users-note')).toHaveText(
      'No other users to share with.',
    );
    await expect(add).toHaveAttribute('aria-disabled', 'true');
    await expect(add).toHaveAccessibleDescription(
      'No other users to share with.',
    );
    await full.getByTestId('share-cancel').click();
    await full.getByTestId('share-discard-confirm').click();
    await expect(full).toBeHidden();

    failUsers = true;
    await page.getByTestId(`draft-share-${draft.id}`).click();
    const failed = await shareDialog(page);
    const retry = failed.getByTestId('share-users-retry');
    await expect(failed.getByTestId('share-users-note')).toContainText(
      'Could not load users. Try later.',
    );
    await expect(failed.getByTestId('share-user')).toHaveAccessibleDescription(
      /^Could not load users\./,
    );
    failUsers = false;
    await retry.click();
    await expect(failed.getByTestId('share-user')).toBeFocused();
    await expect(failed.getByTestId('share-users-note')).toHaveText('');
    await failed.getByTestId('share-cancel').click();
    await expect(failed).toBeHidden();
  });

  await test.step('at 320 pixels wide the dialog fits, and every control can be reached', async () => {
    await page.setViewportSize({ width: 320, height: 640 });
    await page.getByTestId(`draft-share-${draft.id}`).click();
    const narrow = await shareDialog(page);
    const overflow = await page.evaluate(() => {
      const box = document.querySelector('[data-testid="share-dialog"]');
      const root = document.documentElement;

      return [
        root.scrollWidth - root.clientWidth,
        box.scrollWidth - box.clientWidth,
      ];
    });
    expect(overflow).toEqual([0, 0]);

    for (const id of [
      'share-user',
      'share-access',
      'share-add',
      `share-row-access-${editor.username}`,
      `share-row-remove-${viewer.username}`,
      'share-copy-link',
      'share-cancel',
      'share-save',
    ]) {
      const control = narrow.getByTestId(id);
      await control.focus();
      await expect(control, id).toBeInViewport();
    }
    const close = narrow.getByRole('button', { name: 'Close dialog' });
    await close.focus();
    await expect(close).toBeInViewport({ ratio: 1 });
    await narrow.getByTestId('share-cancel').click();
    await expect(narrow).toBeHidden();
  });
});

test(
  'a session the server ends signs in again in place; logging out with a change the server does not have warns first, and an idle logout waits a minute',
  { tag: '@cross-browser' },
  async ({ sharingUsers, playwright }, testInfo) => {
    test.setTimeout(120000);
    const { owner } = sharingUsers;
    const { page } = owner;
    const draft = await seedDraft(owner, 'Logout lab');
    const logout = page.getByRole('button', { name: 'Logout' });
    const records = () =>
      page.evaluate(
        () =>
          new Promise((resolve) => {
            const request = indexedDB.open('phenix-builder');
            request.onsuccess = () => {
              const db = request.result;
              const all = db
                .transaction('drafts')
                .objectStore('drafts')
                .getAll();
              all.onsuccess = () => {
                db.close();
                resolve(all.result.map((record) => record.queue.length));
              };
            };
          }),
      );

    // The idle timeout, shortened to a minute. The page's clock runs as
    // usual until the idle steps stop it, and then moves only when a step
    // moves it on, so the seconds left are the ones the step expects.
    await page.clock.install();
    await page.route('**/api/v1/settings/timeout', (route) =>
      route.fulfill({
        json: { enabled: true, timeout_min: 1, warning_min: 0 },
      }),
    );
    await openOwn(owner, draft);

    await test.step('a session the server ended signs in again in place, and the change it refused is saved then', async () => {
      const dialog = page.getByRole('dialog', { name: 'Sign in again' });
      const password = dialog.getByLabel('Password');
      const saveState = page.getByTestId('builder-save-state');
      // Logging out would remove the Builder's keys, this tab's id among
      // them (see builder/session.js).
      const tabId = () =>
        page.evaluate(() => sessionStorage.getItem('phenix.builder.tab'));
      const tab = await tabId();

      expect(tab).toBeTruthy();

      // The server forgets the page's session, as a logout elsewhere does.
      expect((await owner.api.get(`${API}/logout`)).status()).toBe(204);
      await page.getByTestId('palette-switch').click();
      await expect(dialog).toBeVisible();
      await expect(dialog).toHaveAccessibleDescription(
        'Your session has ended. Sign in again to keep working; changes not saved yet stay in this browser until then.',
      );
      await expect(dialog.getByLabel('Username')).toHaveValue(owner.username);
      await expect(dialog.getByLabel('Username')).not.toBeEditable();
      await expect(password).toBeFocused();
      await expect(saveState).toContainText(
        'Not saved: your session has ended. Sign in again to save your changes.',
      );
      expect(await records()).toEqual([1]);
      await expectAccessible(page, {
        include: '[data-testid="builder-signin"]',
        label: 'Sign in again',
      });

      await password.fill('not-the-password');
      await password.press('Enter');
      await expect(dialog.getByTestId('signin-error')).toHaveText(
        'The password is incorrect.',
      );
      await expect(password).toBeFocused();
      await expect(password).toHaveAttribute('aria-invalid', 'true');

      // Cancel keeps the change here; Retry saving asks again.
      await dialog.getByRole('button', { name: 'Cancel' }).click();
      await expect(dialog).toBeHidden();
      await expect(page.getByTestId('builder-signin-notice')).toBeVisible();
      expect(await records()).toEqual([1]);
      await page.getByTestId('toolbar-retry').click();
      await expect(dialog).toBeVisible();

      await password.fill(USER_PASS);
      await dialog.getByRole('button', { name: 'Sign in' }).click();
      await expect(dialog).toBeHidden();
      await expect(page).toHaveAnnounced(
        'Signed in again. Saving your changes.',
      );
      await expect(saveState).toContainText(SAVED);
      await expect(page.getByTestId('builder-signin-notice')).toBeHidden();
      await expect(page).toHaveURL(/\/builder/);
      expect(await tabId()).toBe(tab);

      // The server has the change, read with the page's new session.
      const saved = await page.evaluate(async (path) => {
        const response = await fetch(path, {
          headers: {
            'X-Phenix-Auth-Token': `bearer ${sessionStorage.getItem('phenix.token')}`,
          },
        });
        const body = await response.json();

        return body.document.nodes.map((node) => node.kind);
      }, draftPath(draft));
      expect(saved).toContain('switch');
    });

    await page.route('**/api/v1/builder/drafts/**', (route) => route.abort());
    await page.getByTestId('palette-device').click();
    await expect(page.getByTestId('builder-save-state')).toContainText(
      'Offline: 1 change kept on this device',
    );

    await test.step('Logout sends the change first, then asks, and waits', async () => {
      const warning = page.getByRole('alertdialog', {
        name: 'Log out with unsaved changes?',
      });

      await logout.click();
      await expect(warning).toBeVisible();
      await expect
        .soft(warning)
        .toHaveAccessibleDescription(
          '1 change to Builder drafts has not reached the server. Logging out deletes it from this browser. Use Download to keep a copy.',
        );
      await expect
        .soft(warning.getByRole('button', { name: 'Stay signed in' }))
        .toBeFocused();
      await expect
        .soft(warning.getByTestId('logout-warning-countdown'))
        .toHaveCount(0);

      const [file] = await Promise.all([
        page.waitForEvent('download'),
        warning.getByRole('button', { name: 'Download' }).click(),
      ]);
      expect.soft(file.suggestedFilename()).toBe('logout-lab.json');
      const downloaded = JSON.parse(fs.readFileSync(await file.path(), 'utf8'));
      expect
        .soft(downloaded.nodes.filter((node) => node.kind === 'device'))
        .toHaveLength(1);

      await warning.getByRole('button', { name: 'Stay signed in' }).click();
      await expect(warning).toBeHidden();
      await expect.soft(logout, 'focus after Stay signed in').toBeFocused();
      expect(await records()).toEqual([1]);
    });

    const idle = page.getByRole('alertdialog', {
      name: 'You will be logged out',
    });

    await test.step('an idle minute warns for another, said again near its end, and Stay signed in keeps the session', async () => {
      await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000));
      await page.clock.fastForward('01:00');
      await expect(idle).toBeVisible();
      await expect
        .soft(idle)
        .toHaveAccessibleDescription(
          'You have been inactive for a while. 1 change to Builder drafts has not reached the server. Logging out deletes it from this browser. Use Download to keep a copy. Logging out in 60 seconds.',
        );
      await expect
        .soft(idle.getByRole('button', { name: 'Stay signed in' }))
        .toBeFocused();
      await expect
        .soft(page.getByTestId('logout-warning-notice'))
        .toHaveText('');

      await page.clock.fastForward('00:50');
      await expect
        .soft(page.getByTestId('logout-warning-countdown'))
        .toHaveText('Logging out in 10 seconds.');
      await expect
        .soft(page.getByTestId('logout-warning-notice'))
        .toHaveText('Logging out in 10 seconds.');

      await idle.getByRole('button', { name: 'Stay signed in' }).click();
      await expect(idle).toBeHidden();
      await expect(page.getByTestId('builder-canvas')).toBeVisible();
      await page.clock.fastForward('00:30');
      await expect(idle).toBeHidden();
    });

    await test.step('left alone, it logs out once the minute is up, and the change goes with the session', async () => {
      await page.clock.fastForward('00:31');
      await expect(idle).toBeVisible();
      await page.clock.fastForward('01:00');
      await expect(page).toHaveURL(/\/signin$/);
      await expect.poll(records).toEqual([]);
      // The server has forgotten the session.
      expect((await owner.api.get(`${API}/builder/drafts`)).status()).toBe(401);
    });

    // The session the fixture cleans up with is gone.
    const guest = await playwright.request.newContext({
      baseURL: testInfo.project.use.baseURL,
    });
    const again = await signIn(guest, owner.username, USER_PASS);
    const headers = { 'X-Phenix-Auth-Token': `bearer ${again.token}` };
    const read = await guest.get(draftPath(draft), { headers });
    const removed = await guest.delete(draftPath(draft), {
      headers: { ...headers, 'If-Match': read.headers().etag },
    });
    expect.soft(removed.ok(), await removed.text()).toBeTruthy();
    await guest.dispose();
  },
);

// The icon library is the server's: every user sees and uses every icon,
// and only its uploader (or a role with builder-icons) renames or deletes
// it. The sharing users' role has no builder-icons.
test('another user sees and uses an icon one user uploaded, and may neither rename nor delete it', async ({
  sharingUsers,
}) => {
  const { owner, stranger } = sharingUsers;
  const { page } = stranger;
  const icon = iconOf(pngOf(12, 12, ownColor()), iconName('shared'));
  const path = iconPath(icon.name);
  const draft = await seedDraft(stranger, 'Icon lab');

  const uploaded = await owner.api.post(`${API}/builder/icons`, {
    data: { name: icon.name, data: icon.data },
  });
  expect(uploaded.status(), await uploaded.text()).toBe(201);

  try {
    await test.step('the listing gives every user the icon, and says who may change it', async () => {
      const listed = async (user) =>
        (await (await user.api.get(`${API}/builder/icons`)).json()).icons.find(
          (entry) => entry.name === icon.name,
        );

      expect(await listed(owner)).toMatchObject({
        owner: owner.username,
        canRename: true,
        canDelete: true,
      });
      expect(await listed(stranger)).toMatchObject({
        owner: owner.username,
        data: icon.data,
        canRename: false,
        canDelete: false,
      });
    });

    await test.step('the server refuses another user’s rename and delete', async () => {
      const renamed = await stranger.api.put(path, {
        data: { name: iconName('taken-over') },
      });

      expect(renamed.status()).toBe(403);
      expect((await stranger.api.delete(path)).status()).toBe(403);
      expect((await stranger.api.get(path)).status()).toBe(200);
    });

    await test.step('the Custom icons dialog offers Use alone on another user’s icon, and the device names it', async () => {
      await openOwn(stranger, draft);
      // The device added is selected, so the Inspector edits it.
      await page.getByTestId('palette-device').click();

      const field = page
        .locator('section[aria-labelledby="inspector-title"]')
        .locator('[data-path="icon"]');

      await field.getByTestId('inspector-icon-choose').click();

      const dialog = page.getByTestId('icon-dialog');
      const row = dialog
        .getByTestId('icon-library-list')
        .locator(`[data-icon="${icon.name}"]`);

      await expect(row).toContainText(`Uploaded by ${owner.username}`);
      await expect.soft(row.getByTestId('icon-rename')).toHaveCount(0);
      await expect.soft(row.getByTestId('icon-delete')).toHaveCount(0);
      await row.getByRole('button', { name: `Use ${icon.name}` }).click();
      await expect(dialog).toHaveCount(0);
      await expect(field.locator('img.builder-icon--custom')).toHaveAttribute(
        'src',
        `data:image/png;base64,${icon.data}`,
      );
      await expect(page.getByTestId('builder-save-state')).toContainText(SAVED);
    });

    await test.step('its uploader renames it, and the other user’s device still shows it', async () => {
      const renamed = await owner.api.put(path, {
        data: { name: iconName('renamed') },
      });

      expect(renamed.status()).toBe(200);
      // A new page load reads the icon library again.
      await openOwn(stranger, draft);
      await expect(
        page.locator('.vue-flow__node img.builder-icon--custom'),
      ).toHaveAttribute('src', `data:image/png;base64,${icon.data}`);
    });
  } finally {
    await owner.api.delete(path).catch(() => {});
  }
});
