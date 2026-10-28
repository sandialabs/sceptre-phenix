// Builder with several users: one changes another's draft, and an ended session
// logs out. Opt-in: needs a server with auth on (see README).
const {
  API,
  DOCUMENT_TIME,
  SAVED,
  USER_PASS,
  blankDocument,
  draftPath,
  expect,
  expectDetail,
  signIn,
  test,
  visit,
} = require('./builder-support');

test.skip(
  process.env.E2E_MULTI_USER !== '1',
  'set E2E_MULTI_USER=1 (see README)',
);

// Makes a draft through `user`'s API client, holding `document`.
async function seedDraft(user, name, document = blankDocument(name)) {
  const created = await user.api.post(`${API}/builder/drafts`, {
    data: { title: name, document },
  });
  expect(created.ok(), await created.text()).toBeTruthy();

  return created.json();
}

async function landing({ page }) {
  await visit(page, '/builder');
  await expect(
    page.getByRole('heading', { name: 'Builder', exact: true }),
  ).toBeVisible({ timeout: 20000 });
}

async function openDraft(user, draft) {
  const { page } = user;

  await landing(user);
  await page.getByTestId(`draft-open-${draft.id}`).click();
  await expect(page.getByTestId('builder-canvas')).toBeVisible({
    timeout: 20000,
  });
  await expect(page.getByTestId('builder-save-state')).toContainText(SAVED);
}

// Renames the diagram, and resolves with the server's answer to its save.
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

test("a builder user opens and changes another user's draft, and both see who created and last changed it", async ({
  userMaker,
}) => {
  test.setTimeout(120000);
  const alice = await userMaker.user('alice');
  const bob = await userMaker.user('bob');
  const name = `Network lab ${userMaker.nonce}`;
  const hers = await seedDraft(alice, name);

  await test.step("bob opens alice's draft and changes it, and Details names them both", async () => {
    const { page } = bob;

    await landing(bob);
    await expect(page.getByTestId(`draft-card-${hers.id}`)).toContainText(
      `Created by ${alice.username}`,
    );
    await page.getByTestId(`draft-open-${hers.id}`).press('Enter');
    await expect(page.getByTestId('builder-canvas')).toBeVisible({
      timeout: 20000,
    });
    await expect(page.getByTestId('builder-save-state')).toContainText(SAVED);

    const made = hers.stamp;
    expect(made).toEqual({
      createdBy: alice.username,
      createdAt: expect.stringMatching(DOCUMENT_TIME),
      updatedBy: alice.username,
      updatedAt: made.createdAt,
    });
    await expectDetail(page, 'edited', alice.username, made.updatedAt);

    const saved = await rename(page, `${name} changed`);
    expect(saved.ok(), await saved.text()).toBeTruthy();
    await expect(page.getByTestId('builder-save-state')).toContainText(SAVED);

    // Bob's save changes who edited it last, and nothing else.
    const { stamp } = await saved.json();
    expect(stamp).toEqual({
      ...made,
      updatedBy: bob.username,
      updatedAt: expect.stringMatching(DOCUMENT_TIME),
    });
    await expectDetail(page, 'created', alice.username, made.createdAt);
    await expectDetail(page, 'edited', bob.username, stamp.updatedAt);
  });

  await test.step("alice's list shows the change, who created the draft and who changed it last", async () => {
    await landing(alice);
    const card = alice.page.getByTestId(`draft-card-${hers.id}`);
    await expect(card.getByRole('heading')).toHaveText(`${name} changed`);
    await expect(card).toContainText(`Created by ${alice.username}`);
    await expect(card).toContainText(bob.username);
  });
});

test("a session the server ends logs out, and the Builder's data in this browser goes with it", async ({
  userMaker,
  playwright,
}, testInfo) => {
  test.setTimeout(120000);
  const alice = await userMaker.user('alice');
  const { page } = alice;
  const draft = await seedDraft(alice, 'Ended session lab');
  // The number of drafts this browser keeps.
  const records = () =>
    page.evaluate(
      () =>
        new Promise((resolve) => {
          const request = indexedDB.open('phenix-builder');
          request.onsuccess = () => {
            const db = request.result;

            if (!db.objectStoreNames.contains('drafts')) {
              db.close();
              resolve(0);
              return;
            }

            const count = db
              .transaction('drafts')
              .objectStore('drafts')
              .count();
            count.onsuccess = () => {
              db.close();
              resolve(count.result);
            };
          };
        }),
    );

  // A change the server cannot get yet stays queued in this browser.
  const snapshots = (url) => url.pathname.endsWith('/snapshots');
  await openDraft(alice, draft);
  await page.route(snapshots, (route) => route.abort());
  await page.getByTestId('palette-switch').click();
  await expect(page.getByTestId('builder-save-state')).toContainText(
    'Offline: 1 change kept on this device',
  );
  expect(await records()).toBe(1);

  // The server forgets the page's session, as a logout elsewhere does. The
  // next save gets a 401, and the page logs out.
  expect((await alice.api.get(`${API}/logout`)).status()).toBe(204);
  await page.unroute(snapshots);
  await page.getByTestId('palette-switch').click();
  await expect(page).toHaveURL(/\/signin$/);
  await expect.poll(records).toBe(0);

  // The session the fixture cleans up with is gone.
  const guest = await playwright.request.newContext({
    baseURL: testInfo.project.use.baseURL,
  });
  const again = await signIn(guest, alice.username, USER_PASS);
  const headers = { 'X-Phenix-Auth-Token': `bearer ${again.token}` };
  const read = await guest.get(draftPath(draft), { headers });
  const removed = await guest.delete(draftPath(draft), {
    headers: { ...headers, 'If-Match': read.headers().etag },
  });
  expect.soft(removed.ok(), await removed.text()).toBeTruthy();
  await guest.dispose();
});
