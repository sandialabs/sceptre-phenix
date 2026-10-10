// Sharing the template library between users: the owner shares a template
// and a collection with another user, who uses them read only and copies
// them; a user with the built-in Builder role publishes a template
// server-wide, which every user then lists, a Topology Reviewer cannot
// publish, and another Builder and then the publisher take it back; and a
// user changes and deletes the built-in templates of a library of their
// own. Server-wide publishing is offered only here: with authentication off
// there is no one else, so the Node Templates tab offers no Share. Opt-in, as
// builder-sharing.spec.js is: the tests need a server with authentication
// on, e.g.:
//
//   phenix ui --jwt-signing-key e2e-sharing \
//     --users 'e2e-admin:Testpass1!:Global Admin'
//   E2E_SHARING=1 npx playwright test builder-sharing-templates
//
// E2E_ADMIN_USER and E2E_ADMIN_PASS name another admin. The admin makes the
// roles and users each test needs, and deletes them afterwards with what
// they made (userMaker in builder-support.js). Every user has a library of
// their own, so these tests may change its built-in templates. The tests run
// at the same time, and what is server-wide is listed to every user, so a
// test looks only for its own users' templates.
const fs = require('fs');
const path = require('path');

const {
  API,
  LIBRARY,
  USER_PASS,
  authorRole,
  blankDocument,
  expect,
  expectAccessible,
  expectNoFatal,
  libraryChange,
  signIn,
  templateLibrary,
  test,
  visit,
} = require('./builder-support');

test.skip(
  process.env.E2E_SHARING !== '1',
  'set E2E_SHARING=1 (see file header)',
);

// The example role of the Builder docs that reads configs and changes none.
const REVIEWER_ROLE = fs.readFileSync(
  path.join(
    __dirname,
    '../../../../docs/content/builder/examples/roles/topology-reviewer.role.yaml',
  ),
  'utf8',
);

// The Topology Reviewer role of the docs, under names of the test's own:
// its config's text, and its role name.
function reviewerRole(nonce) {
  const roleName = `Topology Reviewer ${nonce}`;

  return {
    roleName,
    config: REVIEWER_ROLE.replace(
      /^ {2}name: topology-reviewer$/m,
      `  name: topology-reviewer-${nonce}`,
    ).replace(/^ {2}roleName: Topology Reviewer$/m, `  roleName: ${roleName}`),
  };
}

// The device of a template the tests make: a plain Linux machine named
// `hostname`, with one drive.
function unitDevice(hostname) {
  return {
    iconKey: 'server',
    spec: {
      type: 'VirtualMachine',
      general: { hostname, description: '', vm_type: 'kvm' },
      hardware: { os_type: 'linux', drives: [{ image: 'unit.qc2' }] },
      network: { interfaces: [] },
    },
  };
}

function libraryPath(user, tail) {
  return `${LIBRARY}/${encodeURIComponent(user.username)}/${tail}`;
}

// Adds templates to `user`'s library through the API, with `collection`
// ({name}) a collection that holds them. Returns their ids, in order, and
// the collection's id.
async function addTemplates(user, templates, collection) {
  const response = await libraryChange(
    user.api,
    'post',
    libraryPath(user, 'items'),
    { data: { templates, ...(collection ? { collection } : {}) } },
  );
  expect(response.status(), await response.text()).toBe(201);

  const made = await response.json();

  return {
    ids: made.created.map((entry) => entry.id),
    collection: made.collection?.id || '',
  };
}

// The library as the server lists it to `api`'s user.
async function listLibrary(api) {
  const response = await api.get(LIBRARY);
  expect(response.ok(), await response.text()).toBeTruthy();

  return response.json();
}

// What of `owner`'s library a listing holds.
function itemsOf(listing, owner) {
  return [...listing.templates, ...listing.collections].filter(
    (item) => item.owner === owner.username,
  );
}

// The drafts page's Node Templates tab, as `user` sees it once the library
// was read.
async function openLibrary(user) {
  const { page } = user;
  const library = templateLibrary(page);

  await visit(page, '/builder');
  await expect(
    page.getByRole('heading', { name: 'Builder', exact: true }),
  ).toBeVisible({ timeout: 20000 });
  await library.tab.click();
  await expect(library.show).toBeVisible();

  return library;
}

// Makes a draft of `user`'s and opens it, for its palette.
async function openDraft(user, name) {
  const { page } = user;
  const created = await user.api.post(`${API}/builder/drafts`, {
    data: { title: name, document: blankDocument(name) },
  });
  expect(created.ok(), await created.text()).toBeTruthy();

  const draft = await created.json();

  await visit(page, '/builder');
  await page.getByTestId(`draft-open-${draft.id}`).click();
  await expect(page.getByTestId('builder-canvas')).toBeVisible({
    timeout: 20000,
  });

  return draft;
}

function devices(page) {
  return page
    .getByTestId('builder-node')
    .and(page.locator('[data-node-kind="device"]'));
}

// The Share dialog of the template library, once it is open.
async function shareDialog(page) {
  const root = page.getByTestId('template-share-dialog');
  const by = (id) => root.getByTestId(id);

  await expect(root).toBeVisible();

  return {
    root,
    title: root.getByRole('heading', { level: 2 }),
    user: by('template-share-user'),
    usersNote: by('template-share-users-note'),
    add: by('template-share-add'),
    people: by('template-share-people'),
    row: (user) => by(`template-share-row-${user.username}`),
    adding: (user) => by(`template-share-adding-${user.username}`),
    serverWide: by('template-share-server-wide'),
    changes: by('template-share-changes'),
    keepEditing: by('template-share-keep-editing'),
    discard: by('template-share-discard'),
    save: by('template-share-save'),
  };
}

// The template editor, on another user's template or one of the user's.
function templateEditor(page) {
  const dialog = page.getByTestId('template-dialog');

  return {
    dialog,
    title: dialog.getByRole('heading', { level: 2 }),
    origin: dialog.getByTestId('template-origin'),
    name: dialog.getByTestId('template-name'),
    description: dialog.getByTestId('template-description'),
    hostname: dialog.locator('[data-path="hostname"]').locator('input').first(),
    save: dialog.getByTestId('template-save'),
    cancel: dialog.getByTestId('template-cancel'),
  };
}

test(
  'the owner shares a template and a collection with another user, who uses them read only and copies them',
  { tag: '@cross-browser' },
  async ({ userMaker, playwright, request }, testInfo) => {
    test.setTimeout(180000);
    const role = authorRole(userMaker.nonce);

    await userMaker.role(role);

    const owner = await userMaker.user('owner', role.spec.roleName);
    const recipient = await userMaker.user('recipient', role.spec.roleName);
    const stranger = await userMaker.user('stranger', role.spec.roleName);
    const {
      ids: [plc],
    } = await addTemplates(owner, [
      { name: 'Plant PLC', description: 'Line one', device: unitDevice('plc') },
    ]);
    const kit = await addTemplates(
      owner,
      [{ name: 'Kit RTU', description: '', device: unitDevice('rtu') }],
      { name: 'Kit' },
    );
    const [rtu] = kit.ids;
    const { page } = owner;

    await test.step('the owner shares a template from its card, by keyboard', async () => {
      const library = await openLibrary(owner);

      await expect(library.share(plc)).toHaveAccessibleName(
        'Share template Plant PLC',
      );
      await library.share(plc).press('Enter');

      const dialog = await shareDialog(page);

      await expect(dialog.title).toHaveText('Share Plant PLC');
      await expect(dialog.user).toBeFocused();
      await expect(dialog.usersNote).toHaveText('');
      await expect.soft(dialog.people).toContainText(`${owner.username} (you)`);
      // This role may not publish server-wide.
      await expect.soft(dialog.serverWide).toHaveCount(0);
      await expect.soft(dialog.save).toHaveAttribute('aria-disabled', 'true');

      // Down Arrow offers every user but the owner.
      await dialog.user.press('ArrowDown');
      const options = dialog.root.getByRole('listbox', { name: 'Users' });

      await expect(options).toBeVisible();
      for (const user of [recipient, stranger]) {
        await expect
          .soft(options.getByRole('option', { name: user.username }))
          .toHaveCount(1);
      }
      await expect
        .soft(options.getByRole('option', { name: owner.username }))
        .toHaveCount(0);
      await page.keyboard.press('Escape');
      await expect(dialog.root).toBeVisible();

      // A username typed in full is added as it is.
      await page.keyboard.type(recipient.username);
      await dialog.user.press('Enter');
      await expect(dialog.adding(recipient)).toBeVisible();
      await expect(dialog.user).toBeFocused();
      await expect(dialog.changes).toHaveText('1 change not saved');
      await expectAccessible(page, {
        soft: true,
        include: '[data-testid="template-share-dialog"]',
        label: 'Share dialog of a template',
      });

      await dialog.save.press('Enter');
      await expect(dialog.root).toHaveCount(0);
      await expect(library.share(plc)).toBeFocused();
      await expect(page).toHaveAnnounced(
        `Shared Plant PLC with ${recipient.username}.`,
      );
      await expect(page.getByTestId(`template-shared-with-${plc}`)).toHaveText(
        `Shared with ${recipient.username}`,
      );
    });

    await test.step('the owner shares a collection from its block', async () => {
      const library = templateLibrary(page);

      await library.show.selectOption(kit.collection);
      await expect(library.block).toBeVisible();
      await library.shareCollection.click();

      const dialog = await shareDialog(page);

      await expect(dialog.title).toHaveText('Share Kit');
      await expect(dialog.usersNote).toHaveText('');
      await dialog.user.fill(recipient.username);
      await dialog.add.click();
      await expect(dialog.adding(recipient)).toBeVisible();
      await dialog.save.click();
      await expect(dialog.root).toHaveCount(0);
      await expect(page).toHaveAnnounced(
        `Shared Kit with ${recipient.username}.`,
      );
      await expect(page.getByTestId('collection-shared-with')).toHaveText(
        `Shared with ${recipient.username}`,
      );
      expectNoFatal(owner.issues);
    });

    await test.step('the recipient finds both under Shared with me, read only', async () => {
      const theirs = recipient.page;
      const library = await openLibrary(recipient);

      await expect(library.show.locator('option[value="shared:"]')).toHaveText(
        'Shared with me',
      );
      await expect(
        library.show.locator(
          `option[value="shared:${owner.username}/${kit.collection}"]`,
        ),
      ).toHaveText(`Kit (${owner.username})`);

      await library.show.selectOption('shared:');
      // The template of the shared collection is shared too.
      for (const id of [plc, rtu]) {
        await expect(library.theirCard(owner.username, id)).toBeVisible();
      }

      const card = library.theirCard(owner.username, plc);

      await expect
        .soft(library.owner(owner.username, plc))
        .toHaveText(`Owner: ${owner.username}`);
      await expect.soft(card).toContainText('Line one');
      await expect
        .soft(library.view(owner.username, plc))
        .toHaveAccessibleName('View template Plant PLC');
      await expect
        .soft(library.copy(owner.username, plc))
        .toHaveAccessibleName('Copy to my library: Plant PLC');
      // Nothing that changes it.
      await expect
        .soft(card.getByRole('button'))
        .toHaveText(['View', 'Copy to my library', 'Export']);
      await expect.soft(library.bulkCopy).toHaveText('Copy to my library');
      await expect.soft(library.bulkDelete).toHaveCount(0);
      await expect.soft(library.bulkShare).toHaveCount(0);
      await expectAccessible(theirs, {
        soft: true,
        label: 'templates shared with the user',
      });

      // The server keeps it read only too.
      const listing = await listLibrary(recipient.api);
      const listed = listing.templates.find(
        (item) => item.owner === owner.username && item.id === plc,
      );

      expect.soft(listed).toMatchObject({ source: 'shared' });
      expect.soft(listed).not.toHaveProperty('shares');

      const changed = await recipient.api.put(
        libraryPath(owner, `items/${plc}`),
        {
          headers: { 'If-Match': listed.etag },
          data: { name: 'Taken', description: '', device: listed.device },
        },
      );

      expect.soft(changed.status()).toBe(404);
    });

    await test.step('someone it was not shared with sees none of it', async () => {
      const library = await openLibrary(stranger);

      await expect(library.show.locator('option[value="shared:"]')).toHaveCount(
        0,
      );
      expect(itemsOf(await listLibrary(stranger.api), owner)).toEqual([]);
    });

    await test.step('it is in the recipient’s Add nodes, named for its owner, and makes devices', async () => {
      const theirs = recipient.page;

      await openDraft(recipient, 'Shared templates');

      const entry = theirs.getByTestId(
        `palette-template-shared-${owner.username}-${plc}`,
      );

      await expect(entry).toBeVisible();
      await expect(
        theirs.getByTestId('palette-templates-shared'),
      ).toHaveAccessibleName('Shared with me');
      await expect.soft(entry).toHaveAccessibleName('Add Plant PLC device');
      await expect
        .soft(entry)
        .toHaveAccessibleDescription(`Line one Shared by ${owner.username}.`);
      await entry.click();
      await expect(devices(theirs)).toHaveCount(1);
    });

    await test.step('the owner’s later change reaches the recipient', async () => {
      const listed = (await listLibrary(owner.api)).templates.find(
        (item) => item.source === 'own' && item.id === plc,
      );
      const changed = await libraryChange(
        owner.api,
        'put',
        libraryPath(owner, `items/${plc}`),
        {
          headers: { 'If-Match': listed.etag },
          data: {
            name: 'Plant PLC',
            description: 'Line two',
            device: listed.device,
          },
        },
      );

      expect(changed.status(), await changed.text()).toBe(200);

      const library = await openLibrary(recipient);

      await library.show.selectOption('shared:');
      await expect(library.theirCard(owner.username, plc)).toContainText(
        'Line two',
      );
    });

    await test.step('View shows every field, locked, and copies it into the recipient’s library', async () => {
      const theirs = recipient.page;
      const library = templateLibrary(theirs);
      const editor = templateEditor(theirs);

      await library.view(owner.username, plc).click();
      await expect(editor.dialog).toBeVisible();
      await expect.soft(editor.title).toHaveText('Template Plant PLC');
      await expect
        .soft(editor.origin)
        .toHaveText(`Shared by ${owner.username}`);
      await expect.soft(editor.name).toHaveValue('Plant PLC');
      await expect.soft(editor.name).not.toBeEditable();
      await expect.soft(editor.description).toHaveValue('Line two');
      await expect.soft(editor.description).not.toBeEditable();
      await expect.soft(editor.hostname).toHaveValue('plc');
      await expect.soft(editor.hostname).not.toBeEditable();
      await expect.soft(editor.cancel).toHaveText('Close');
      await expect.soft(editor.save).toHaveText('Copy to my library');
      await expectAccessible(theirs, {
        soft: true,
        include: '[data-testid="template-dialog"]',
        label: 'another user’s template in the editor',
      });

      // Escape closes it, as nothing can change.
      await theirs.keyboard.press('Escape');
      await expect(editor.dialog).toHaveCount(0);
      await expect(library.view(owner.username, plc)).toBeFocused();

      await library.view(owner.username, plc).click();
      await editor.save.click();
      await expect(editor.dialog).toHaveCount(0);
      await expect(theirs).toHaveAnnounced('Copied Plant PLC to your library.');

      const copy = (await listLibrary(recipient.api)).templates.find(
        (item) => item.source === 'own' && item.name === 'Plant PLC',
      );

      expect(copy, 'the copy in the recipient’s library').toBeTruthy();
      expect.soft(copy.description).toBe('Line two');
      expect.soft(copy.device.spec.general.hostname).toBe('plc');

      await library.show.selectOption('');
      await expect(library.card(copy.id)).toContainText('Line two');
    });

    await test.step('the recipient copies the shared collection whole', async () => {
      const theirs = recipient.page;
      const library = templateLibrary(theirs);

      await library.show.selectOption(
        `shared:${owner.username}/${kit.collection}`,
      );
      await expect(library.block).toBeVisible();
      await expect
        .soft(theirs.getByTestId('collection-owner'))
        .toHaveText(`Owner: ${owner.username}`);
      await expect
        .soft(theirs.getByTestId('collection-count'))
        .toHaveText('1 template');
      await expect.soft(library.editCollection).toHaveCount(0);
      await expect.soft(library.shareCollection).toHaveCount(0);
      await expect.soft(library.deleteCollection).toHaveCount(0);

      await library.copyCollection.click();
      await expect(theirs).toHaveAnnounced(
        'Copied collection Kit to your library.',
      );

      const listing = await listLibrary(recipient.api);
      const copied = listing.collections.find(
        (item) => item.source === 'own' && item.name === 'Kit',
      );

      expect(copied, 'the copied collection').toBeTruthy();
      expect(
        copied.templateIds.map(
          (id) => listing.templates.find((item) => item.id === id)?.name,
        ),
      ).toEqual(['Kit RTU']);
      expectNoFatal(recipient.issues);
    });

    await test.step('an account made again under the recipient’s name sees nothing, and the owner sees the share as removed', async () => {
      const { admin } = userMaker;
      const deleted = await admin.delete(`${API}/users/${recipient.username}`);

      expect(deleted.ok(), await deleted.text()).toBeTruthy();

      const made = await admin.post(`${API}/users`, {
        data: {
          username: recipient.username,
          password: USER_PASS,
          first_name: 'recipient',
          last_name: 'E2E',
          role_name: role.spec.roleName,
          resource_names: ['*'],
        },
      });

      expect(made.ok(), await made.text()).toBeTruthy();

      const session = await signIn(request, recipient.username, USER_PASS);
      const again = await playwright.request.newContext({
        baseURL: testInfo.project.use.baseURL,
        extraHTTPHeaders: { 'X-Phenix-Auth-Token': `bearer ${session.token}` },
      });

      try {
        expect(itemsOf(await listLibrary(again), owner)).toEqual([]);
      } finally {
        await again.dispose();
      }

      const library = await openLibrary(owner);

      await library.share(plc).click();

      const dialog = await shareDialog(page);
      const row = dialog.row(recipient);

      await expect(row).toContainText('Account removed');
      await expect.soft(row).toHaveClass(/is-removed/);
      await expect(dialog.changes).toHaveText('1 change not saved');
      await dialog.save.click();
      await expect(dialog.root).toHaveCount(0);
      await expect(page).toHaveAnnounced('Only you can use Plant PLC now.');
      await expect(page.getByTestId(`template-shared-with-${plc}`)).toHaveCount(
        0,
      );
      expectNoFatal(owner.issues);
    });
  },
);

test('a Builder publishes a template server-wide, which every user lists; a Topology Reviewer cannot publish, and another Builder and the publisher take it back', async ({
  userMaker,
}) => {
  test.setTimeout(180000);
  const role = authorRole(userMaker.nonce);
  const reviewer = reviewerRole(userMaker.nonce);

  await userMaker.role(role);
  await userMaker.role(reviewer.config);

  // The built-in Builder role may publish templates server-wide.
  const publisher = await userMaker.user('publisher', 'Builder');
  const curator = await userMaker.user('curator', 'Builder');
  const author = await userMaker.user('author', role.spec.roleName);
  const reader = await userMaker.user('reader', reviewer.roleName);
  const {
    ids: [ids],
  } = await addTemplates(publisher, [
    {
      name: 'Edge IDS',
      description: 'Watches the edge',
      device: unitDevice('ids'),
    },
  ]);

  await test.step('the publisher publishes it from its Share dialog', async () => {
    const { page } = publisher;
    const library = await openLibrary(publisher);

    await library.share(ids).click();

    const dialog = await shareDialog(page);

    await expect(dialog.title).toHaveText('Share Edge IDS');
    // People and server-wide, both.
    await expect.soft(dialog.user).toBeVisible();
    await expect(dialog.serverWide).not.toBeChecked();
    await expect
      .soft(dialog.serverWide)
      .toHaveAccessibleName(
        'Published server-wide: anyone who can use the Builder on this server can use it',
      );
    await dialog.serverWide.focus();
    await page.keyboard.press('Space');
    await expect(dialog.serverWide).toBeChecked();
    await expect(dialog.changes).toHaveText('1 change not saved');
    // Escape asks before it drops the change; asked again, it keeps it.
    await page.keyboard.press('Escape');
    await expect(dialog.keepEditing).toBeFocused();
    await expect
      .soft(dialog.discard)
      .toContainText('Discard 1 unsaved change?');
    await page.keyboard.press('Escape');
    await expect(dialog.serverWide).toBeFocused();
    // Reduced motion turns off color transitions, so the dialog is measured
    // at its final colors in each theme.
    for (const scheme of ['light', 'dark']) {
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' });
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        scheme,
      );
      await expectAccessible(page, {
        soft: true,
        include: '[data-testid="template-share-dialog"]',
        label: `Share dialog of a role that may publish (${scheme})`,
      });
    }
    await page.emulateMedia({ colorScheme: 'light', reducedMotion: null });

    const published = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().endsWith(libraryPath(publisher, 'publish')),
    );

    await dialog.save.click();
    expect
      .soft((await published).request().postDataJSON())
      .toEqual({ templates: [ids], serverWide: true });
    await expect(dialog.root).toHaveCount(0);
    await expect.soft(library.share(ids)).toBeFocused();
    await expect(page).toHaveAnnounced('Published Edge IDS server-wide.');
    await expect(page.getByTestId(`template-server-wide-${ids}`)).toHaveText(
      'Server-wide',
    );
    expectNoFatal(publisher.issues);
  });

  await test.step('another user lists it under Server-wide, and may copy it but not take it back', async () => {
    const { page } = author;
    const library = await openLibrary(author);

    await library.show.selectOption('server:');

    const card = library.theirCard(publisher.username, ids);

    await expect(card).toBeVisible();
    await expect
      .soft(library.owner(publisher.username, ids))
      .toHaveText(`Owner: ${publisher.username}`);
    await expect
      .soft(
        page.getByTestId(`template-server-wide-${publisher.username}-${ids}`),
      )
      .toHaveText('Server-wide');
    await expect.soft(library.copy(publisher.username, ids)).toBeVisible();
    await expect.soft(library.bulkCopy).toBeVisible();
    await expect.soft(library.bulkUnpublish).toHaveCount(0);
    await expectAccessible(page, {
      soft: true,
      label: 'server-wide templates',
    });

    await openDraft(author, 'Server-wide templates');

    const entry = page.getByTestId(
      `palette-template-server-${publisher.username}-${ids}`,
    );

    await expect(entry).toBeVisible();
    await expect
      .soft(entry)
      .toHaveAccessibleDescription(
        `Watches the edge Published by ${publisher.username}.`,
      );
    await entry.click();
    await expect(devices(page)).toHaveCount(1);
    expectNoFatal(author.issues);
  });

  await test.step('a Topology Reviewer lists it, and can neither copy nor publish', async () => {
    const { page } = reader;
    const library = await openLibrary(reader);

    await expect
      .soft(page.getByTestId('templates-view-only'))
      .toHaveText('Your role can view templates, but not create them.');
    // Of their own library, nothing to share or publish.
    await expect(library.card('server')).toBeVisible();
    await expect.soft(library.share('server')).toHaveCount(0);
    await expect.soft(library.bulkShare).toHaveCount(0);

    await library.show.selectOption('server:');
    await expect(library.view(publisher.username, ids)).toBeVisible();
    await expect.soft(library.copy(publisher.username, ids)).toHaveCount(0);
    await expect
      .soft(library.theirSelect(publisher.username, ids))
      .toHaveCount(0);
    await expect.soft(library.bulkUnpublish).toHaveCount(0);

    const listing = await listLibrary(reader.api);

    expect.soft(listing.canPublish).toBe(false);

    const publish = await reader.api.post(libraryPath(reader, 'publish'), {
      data: { templates: ['server'], serverWide: true },
    });
    const takeBack = await reader.api.post(libraryPath(publisher, 'publish'), {
      data: { templates: [ids], serverWide: false },
    });

    expect.soft(publish.status()).toBe(403);
    expect.soft(takeBack.status()).toBe(403);
    expectNoFatal(reader.issues);
  });

  await test.step('another Builder takes it back from server-wide, after a question', async () => {
    const { page } = curator;
    const library = await openLibrary(curator);

    await library.show.selectOption('server:');
    await library.theirSelect(publisher.username, ids).check();
    await expect(library.bulkUnpublish).toHaveText('Remove from server-wide');
    await library.bulkUnpublish.click();
    await expect(library.confirm).toBeVisible();
    await expect
      .soft(library.confirm)
      .toHaveAccessibleName('Remove Edge IDS from server-wide?');
    await expect
      .soft(library.confirm)
      .toHaveAccessibleDescription(
        `It stays in ${publisher.username}'s library. Other people can no longer use it unless it is shared with them.`,
      );
    await expect.soft(library.accept).toHaveText('Remove');
    await expect.soft(library.cancel).toBeFocused();
    await expectAccessible(page, {
      soft: true,
      label: 'the question before a template is taken back',
    });
    await library.accept.click();
    await expect(page).toHaveAnnounced('Removed Edge IDS from server-wide.');
    await expect(library.theirCard(publisher.username, ids)).toHaveCount(0);

    expect(itemsOf(await listLibrary(author.api), publisher)).toEqual([]);

    const [own] = itemsOf(await listLibrary(publisher.api), publisher).filter(
      (item) => item.id === ids,
    );

    expect.soft(own.serverWide).toBe(false);
    expectNoFatal(curator.issues);
  });

  await test.step('the publisher takes it back from its Share dialog', async () => {
    const { page } = publisher;
    const again = await publisher.api.post(libraryPath(publisher, 'publish'), {
      data: { templates: [ids], serverWide: true },
    });

    expect(again.status()).toBe(200);

    const library = await openLibrary(publisher);

    await expect(page.getByTestId(`template-server-wide-${ids}`)).toHaveText(
      'Server-wide',
    );
    await library.share(ids).click();

    const dialog = await shareDialog(page);

    await expect(dialog.serverWide).toBeChecked();
    await dialog.serverWide.uncheck();
    await dialog.save.click();
    await expect(dialog.root).toHaveCount(0);
    await expect(page).toHaveAnnounced('Removed Edge IDS from server-wide.');
    await expect(page.getByTestId(`template-server-wide-${ids}`)).toHaveCount(
      0,
    );

    const [own] = itemsOf(await listLibrary(publisher.api), publisher).filter(
      (item) => item.id === ids,
    );

    expect.soft(own.serverWide).toBe(false);
    expectNoFatal(publisher.issues);
  });
});

test('a user changes and deletes built-in templates of their own library, and they stay so', async ({
  userMaker,
}) => {
  test.setTimeout(120000);
  const role = authorRole(userMaker.nonce);

  await userMaker.role(role);

  const keeper = await userMaker.user('keeper', role.spec.roleName);
  const { page } = keeper;
  const editor = templateEditor(page);
  let library = await openLibrary(keeper);

  await test.step('Edit changes the built-in Router, and Delete takes External device away', async () => {
    await expect(library.tab).toHaveText('Node Templates (5)');

    await library.edit('router').click();
    await expect(editor.dialog).toBeVisible();
    await expect.soft(editor.title).toHaveText('Edit template Router');
    await editor.description.fill('The plant’s edge router');
    await editor.save.click();
    await expect(editor.dialog).toHaveCount(0);
    await expect(page).toHaveAnnounced('Updated template Router.');

    await library.remove('external').click();
    await expect(library.confirm).toBeVisible();
    await expect
      .soft(library.confirm)
      .toHaveAccessibleName('Delete template External device?');
    await library.accept.click();
    await expect(page).toHaveAnnounced('Deleted template External device.');
    await expect(library.tab).toHaveText('Node Templates (4)');
  });

  await test.step('both stay so after a reload, on the tab and in Add nodes', async () => {
    library = await openLibrary(keeper);
    await expect(library.card('router')).toContainText(
      'The plant’s edge router',
    );
    await expect(library.card('external')).toHaveCount(0);
    await expect(library.tab).toHaveText('Node Templates (4)');

    await openDraft(keeper, 'Built-in templates');
    await expect(
      page.getByTestId('palette-template-router'),
    ).toHaveAccessibleDescription('The plant’s edge router');
    await expect(page.getByTestId('palette-template-external')).toHaveCount(0);

    const { templates } = await listLibrary(keeper.api);

    // Of the user's own: other users' server-wide templates are listed too.
    expect(
      templates.filter((item) => item.source === 'own').map((item) => item.id),
    ).toEqual(['server', 'workstation', 'router', 'firewall']);
    expectNoFatal(keeper.issues);
  });
});
