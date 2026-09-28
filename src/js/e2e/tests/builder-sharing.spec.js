// Sharing a draft between users: the owner shares it, the people it is
// shared with open it to edit or to view, and access changes while it is
// open. Opt-in: the tests need a server with authentication on, e.g.:
//
//   phenix ui --features builder-beta --jwt-signing-key e2e-sharing \
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
  API,
  SAVED,
  USER_PASS,
  blankDocument,
  draftPath,
  expect,
  expectAccessible,
  signIn,
  test,
  visit,
} = require('./builder-support');

test.skip(
  process.env.E2E_SHARING !== '1',
  'set E2E_SHARING=1 (see file header)',
);

// Makes a draft of `owner`'s through the API and shares it with `shares`.
async function seedDraft(owner, name, shares = []) {
  const created = await owner.api.post(`${API}/builder/drafts`, {
    data: { title: name, document: blankDocument(name) },
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
  await visit(page, '/builder-beta');
  await expect(
    page.getByRole('heading', { name: 'Builder Flow', exact: true }),
  ).toBeVisible({ timeout: 20000 });
}

// Opens a draft listed under Shared with me.
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
  const field = page.getByTestId('builder-name');
  const answered = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname.endsWith('/snapshots'),
  );

  await field.fill(name);
  await field.press('Tab');

  return answered;
}

function editorHeading(page) {
  return page.getByRole('heading', { level: 1, name: /Builder Flow$/ });
}

test(
  'the owner shares a draft by keyboard; the editor changes it and the viewer only views it',
  { tag: '@cross-browser' },
  async ({ sharingUsers }, testInfo) => {
    test.setTimeout(120000);
    const { owner, editor, viewer, stranger } = sharingUsers;
    const name = 'Network lab';
    const draft = await seedDraft(owner, name);
    const { page } = owner;
    const live = page.getByTestId('builder-live-region');

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

      // Down Arrow opens the users the draft may be shared with, named
      // "Name (username)", never the owner; typing filters them, and Enter
      // takes the one in view.
      await field.press('ArrowDown');
      await expect(options).toBeVisible();
      await expect(field).toHaveAttribute('aria-expanded', 'true');
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
      await expect(live).toContainText(
        `${name} is now shared with ${editor.username} (can edit) and ${viewer.username} (can view).`,
      );
      await expect(
        page.getByTestId(`draft-shared-with-${draft.id}`),
      ).toContainText(`Shared with ${editor.username} and ${viewer.username}`);
    });

    await test.step('the editor finds it under Shared with me and changes it', async () => {
      const { page: theirs } = editor;
      await landing(editor);
      await theirs.getByTestId('drafts-tab-mine').press('ArrowRight');
      await expect(theirs.getByTestId('drafts-tab-shared')).toBeFocused();
      await expect(theirs.getByTestId('drafts-tab-shared')).toHaveText(
        /Shared with me/,
      );
      await expect(
        theirs.getByTestId(`draft-access-${draft.id}`),
      ).toContainText('Can edit');
      await expect(theirs.getByTestId(`draft-delete-${draft.id}`)).toHaveCount(
        0,
      );
      await expectAccessible(theirs, { soft: true, label: 'Shared with me' });

      await theirs.getByTestId(`draft-open-${draft.id}`).press('Enter');
      await expect(theirs.getByTestId('builder-canvas')).toBeVisible({
        timeout: 20000,
      });
      await expect(theirs.getByTestId('builder-live-region')).toContainText(
        `Opened ${owner.username}'s draft ${name}. You can edit it; others may be editing too.`,
      );
      await expect(theirs.getByTestId('editor-shared-by')).toHaveText(
        `Shared by ${owner.username} · Can edit`,
      );
      await expect(editorHeading(theirs)).toHaveText(
        `${name} – ${owner.username}'s draft – Builder Flow`,
      );
      await expect(theirs.getByTestId('toolbar-share')).toHaveAttribute(
        'aria-disabled',
        'true',
      );

      await rename(theirs, `${name} changed`);
      await expect(theirs.getByTestId('builder-save-state')).toContainText(
        SAVED,
      );
    });

    await test.step("the owner's list shows the change, and who made it", async () => {
      await page.reload();
      const card = page
        .getByRole('listitem')
        .filter({ has: page.getByTestId(`draft-open-${draft.id}`) });
      await expect(card.getByRole('heading')).toHaveText(`${name} changed`);
      await expect(card).toContainText(`by ${editor.username}`);
    });

    await test.step('the viewer opens it view only', async () => {
      const { page: theirs } = viewer;
      await landing(viewer);
      await theirs.getByTestId('drafts-tab-shared').click();
      await expect(
        theirs.getByTestId(`draft-access-${draft.id}`),
      ).toContainText('Can view');
      for (const action of ['share', 'delete']) {
        await expect(
          theirs.getByTestId(`draft-${action}-${draft.id}`),
        ).toHaveCount(0);
      }

      await theirs.getByTestId(`draft-open-${draft.id}`).click();
      await expect(theirs.getByTestId('builder-readonly')).toHaveText(
        `${owner.username} shared this draft with you to view. Use Export to keep a copy.`,
      );
      await expect(theirs.getByTestId('builder-live-region')).toContainText(
        `Opened ${owner.username}'s draft ${name} changed, view only.`,
      );
      await expect(theirs.getByTestId('editor-shared-by')).toHaveText(
        `Shared by ${owner.username} · Can view`,
      );
      await expect(theirs.getByTestId('builder-name')).not.toBeEditable();
      for (const action of ['paste', 'scenario', 'publish', 'share']) {
        await expect(theirs.getByTestId(`toolbar-${action}`)).toHaveAttribute(
          'aria-disabled',
          'true',
        );
      }
      await expect(theirs.locator('#toolbar-tip-share')).toContainText(
        `Only ${owner.username} can change who has access`,
      );
      await expect(theirs.getByTestId('toolbar-export')).not.toHaveAttribute(
        'aria-disabled',
        'true',
      );

      // Draft History lists the snapshots, but a viewer can neither restore
      // nor delete one.
      await theirs.getByTestId('toolbar-history').click();
      const history = theirs.getByTestId('history-dialog');
      await expect(history.getByTestId('history-row')).toHaveCount(2);
      await expect(history.getByTestId('history-name').first()).toHaveText(
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
      // Only the owner learns whom the draft can be shared with.
      const candidates = `${path}/shares/candidates`;
      expect((await editor.api.get(candidates)).status()).toBe(403);
      const offered = (await (await owner.api.get(candidates)).json()).users;
      expect(offered.map((user) => user.username)).toEqual(
        expect.arrayContaining([editor.username, viewer.username]),
      );

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
    await expect(page.getByTestId('builder-live-region')).toContainText(
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
    await expect(theirs.getByTestId('builder-name')).toHaveValue(
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
    await visit(viewer.page, `/builder-beta?draft=${link}`);
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
    await expect(theirs.getByTestId('builder-name')).toHaveValue(
      `${name} (owner again)`,
    );
  });

  await test.step('the owner deletes it while the editor has it open', async () => {
    const { page: theirs } = editor;

    // The landing shows the lists it has at once and reads them again; the
    // delete needs the draft as it is now.
    const relisted = page.waitForResponse(
      (response) =>
        response.request().method() === 'GET' &&
        new URL(response.url()).pathname === `${API}/builder/drafts`,
    );
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
    // Refused where it was typed, when the list does not offer them.
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
      const live = page.getByTestId('builder-live-region');
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
      await expect(live).toContainText('Signed in again. Saving your changes.');
      await expect(saveState).toContainText(SAVED);
      await expect(page.getByTestId('builder-signin-notice')).toBeHidden();
      await expect(page).toHaveURL(/\/builder-beta/);
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
          '1 change to Builder Flow drafts has not reached the server. Logging out deletes it from this browser. Use Export to keep a copy.',
        );
      await expect
        .soft(warning.getByRole('button', { name: 'Stay signed in' }))
        .toBeFocused();
      await expect
        .soft(warning.getByTestId('logout-warning-countdown'))
        .toHaveCount(0);

      const [file] = await Promise.all([
        page.waitForEvent('download'),
        warning.getByRole('button', { name: 'Export' }).click(),
      ]);
      expect.soft(file.suggestedFilename()).toBe('logout-lab.json');
      const exported = JSON.parse(fs.readFileSync(await file.path(), 'utf8'));
      expect
        .soft(exported.nodes.filter((node) => node.kind === 'device'))
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
          'You have been inactive for a while. 1 change to Builder Flow drafts has not reached the server. Logging out deletes it from this browser. Use Export to keep a copy. Logging out in 60 seconds.',
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
