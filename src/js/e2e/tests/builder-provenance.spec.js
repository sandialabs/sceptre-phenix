// Builder document provenance: who made a diagram and who saved it last.
// The server writes both into the document's metadata when it stores it,
// answers each create and save with them (`stamp`), and the Inspector's
// Details block shows them. With authentication off, as here, every user is
// the same one; builder-sharing.spec.js checks that a second user's edit
// changes the last editor only.

const fs = require('node:fs');

const {
  API,
  DOCUMENT_TIME,
  blankDocument,
  expect,
  expectDetail,
  expectNoFatal,
  isPublishResponse,
  labDocument,
  nextSecond,
  provenanceOf,
  publishTopology,
  test,
  uniqueName,
  waitForApi,
} = require('./builder-support');

test.use({ announceHold: 100 });

const STAMP_KEYS = ['createdBy', 'createdAt', 'updatedBy', 'updatedAt'];

// The Details block, and its Source file row.
function details(builder) {
  return builder.inspector.getByTestId('inspector-details');
}

function sourceFile(builder) {
  return builder.inspector.getByTestId('inspector-source-file');
}

test('a new draft says who made it and who edited it last, an edit moves only the last edit, and Builder JSON holds both', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  await builder.open();
  const answered = waitForApi(page, 'POST', '/builder/drafts');
  await page.getByTestId('drafts-blank').click();
  const draft = await (await answered).json();
  const madeAt = Date.now();
  await expect(builder.canvas).toBeVisible();
  await builder.waitSaved();
  // With authentication off the server names every user the same.
  const user = draft.owner;

  await test.step('the draft is made by, and last edited by, its maker', async () => {
    expect(draft.stamp, 'stamp of the create').toEqual({
      createdBy: user,
      createdAt: expect.stringMatching(DOCUMENT_TIME),
      updatedBy: user,
      updatedAt: draft.stamp.createdAt,
    });

    await expect(details(builder).getByRole('heading')).toHaveText('Details');
    await expectDetail(page, 'created', user, draft.stamp.createdAt);
    await expectDetail(page, 'edited', user, draft.stamp.updatedAt);
    // A draft drawn here was not made from a file.
    await expect.soft(sourceFile(builder)).toHaveCount(0);
    expect.soft(draft.sourceFile, 'sourceFile').toBeUndefined();
  });

  const title = uniqueName(testInfo, 'stamped');
  const stamp =
    await test.step('an edit moves Last edited and leaves Created', async () => {
      await nextSecond(madeAt);
      const saved = builder.nextSnapshot();
      await builder.rename(title);
      const { stamp: written } = await (await saved).json();

      expect(written, 'stamp of the save').toEqual({
        ...draft.stamp,
        updatedAt: expect.stringMatching(DOCUMENT_TIME),
      });
      expect(
        Date.parse(written.updatedAt),
        'the time of the edit',
      ).toBeGreaterThan(Date.parse(draft.stamp.updatedAt));
      await expectDetail(page, 'created', user, draft.stamp.createdAt);
      await expectDetail(page, 'edited', user, written.updatedAt);
      await builder.waitSaved();

      return written;
    });

  await test.step('the stored document holds them in its metadata, after its name', async () => {
    const stored = await builder.serverDocument(draft);
    expect.soft(provenanceOf(stored)).toEqual(stamp);
    expect
      .soft(Object.keys(stored).slice(0, 4))
      .toEqual(['$schema', 'revision', 'metadata', 'nodes']);
    const keys = Object.keys(stored.metadata);
    expect.soft(keys.slice(keys.indexOf('name') + 1)).toEqual(STAMP_KEYS);
  });

  await test.step('the Builder JSON download holds the four fields', async () => {
    const dialog = await builder.openDialog('download');
    const [file] = await Promise.all([
      page.waitForEvent('download'),
      dialog.getByTestId('download-json').click(),
    ]);
    const downloaded = JSON.parse(fs.readFileSync(await file.path(), 'utf8'));
    expect.soft(downloaded.metadata.name).toBe(title);
    expect.soft(provenanceOf(downloaded)).toEqual(stamp);
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(builder.dialog).toBeHidden();
  });

  await test.step('the draft opened again shows what the server stored', async () => {
    await builder.backToDrafts();
    await builder.openDraft(draft);
    await expectDetail(page, 'created', user, stamp.createdAt);
    await expectDetail(page, 'edited', user, stamp.updatedAt);
  });

  expectNoFatal(issues);
});

test('Edit as a draft of a published diagram keeps who made and last edited it, and publishing it unchanged changes nothing', async ({
  page,
  request,
  builder,
  tracker,
  issues,
}, testInfo) => {
  const name = uniqueName(testInfo, 'kept');
  // The draft that published it is gone, so Edit as a draft makes one from
  // the published document, as it does for a user who did not publish it.
  const published = await publishTopology(
    request,
    tracker,
    name,
    labDocument(name),
    { keepDraft: false },
  );
  const publishedAt = Date.now();
  const read = await request.get(
    `${API}/builder/topologies/${encodeURIComponent(name)}/document`,
  );
  expect(read.ok(), await read.text()).toBeTruthy();
  const record = await read.json();
  const made = provenanceOf(record.document);
  expect(record).toMatchObject({ source: 'store', id: published.documentId });
  expect(made).toEqual({
    createdBy: published.draft.owner,
    createdAt: expect.stringMatching(DOCUMENT_TIME),
    updatedBy: published.draft.owner,
    updatedAt: expect.stringMatching(DOCUMENT_TIME),
  });

  await builder.open();
  await page.getByTestId('drafts-tab-published').click();
  await page.getByTestId(`draft-open-${published.documentId}`).click();
  await expect(builder.canvas).toBeVisible();
  await expect(page.getByTestId('builder-published')).toContainText(
    `You are viewing the published diagram ${name}.`,
  );

  await test.step('the published diagram shows who made it, read only', async () => {
    await expectDetail(page, 'created', made.createdBy, made.createdAt);
    await expectDetail(page, 'edited', made.updatedBy, made.updatedAt);
  });

  const draft =
    await test.step('the draft is the published document, not a new edit', async () => {
      // A copy stamped as an edit would be dated now.
      await nextSecond(publishedAt);
      const created = waitForApi(page, 'POST', '/builder/drafts');
      await page.getByTestId('published-edit').click();
      const body = await (await created).json();
      await expect(page.getByTestId('builder-published')).toHaveCount(0);
      await builder.waitSaved();

      expect.soft(body.id, 'a new draft').not.toBe(published.draft.id);
      expect.soft(body.sourceToken).toBe(`builder-doc/${published.documentId}`);
      expect.soft(body.digest, 'digest').toBe(record.digest);
      expect.soft(body.stamp, 'stamp').toEqual(made);
      await expectDetail(page, 'created', made.createdBy, made.createdAt);
      await expectDetail(page, 'edited', made.updatedBy, made.updatedAt);
      expect
        .soft(provenanceOf(await builder.serverDocument(body)))
        .toEqual(made);

      return body;
    });

  await test.step('publishing it unchanged leaves the topology as it is', async () => {
    const dialog = await builder.openDialog('publish');
    await expect(dialog.getByTestId('publish-name')).toHaveValue(name);
    const submit = dialog.getByTestId('publish-submit');
    await expect(submit).toHaveText('Update topology');
    const answered = page.waitForResponse(isPublishResponse);
    await submit.click();
    await page.getByTestId('confirm-accept').click();
    const response = await answered;
    expect(response.status(), await response.text()).toBe(200);

    const result = dialog.getByTestId('publish-result');
    await expect(result).toContainText('Published. Every stage succeeded.');
    await expect
      .soft(result.locator('li[data-status]').nth(1))
      .toHaveText(/^topology: skipped\b/);

    // The topology still names the document it named, and no other was
    // stored for it.
    const config = await builder.config('Topology', name);
    expect
      .soft(config?.metadata?.annotations?.['builder-doc'])
      .toEqual({ digest: record.digest, id: record.id });
    const listed = await request.get(`${API}/builder/documents`);
    expect(
      ((await listed.json()).documents || [])
        .filter((entry) => entry.target === name)
        .map((entry) => entry.id),
    ).toEqual([record.id]);
    expect(draft.digest).toBe(record.digest);
  });

  expectNoFatal(issues);
});

test('a pasted document keeps the creator it names, and has no source file', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const title = uniqueName(testInfo, 'pasted');
  const claimed = {
    createdBy: 'alice',
    createdAt: '2020-01-02T03:04:05Z',
    updatedBy: 'alice',
    updatedAt: '2020-02-03T04:05:06Z',
  };
  // The blank document of the title, its metadata naming these too.
  const claiming = (fields) => {
    const doc = blankDocument(title);

    return { ...doc, metadata: { ...doc.metadata, ...fields } };
  };

  await builder.open();
  await page.getByTestId('drafts-upload').click();
  const dialog = page.getByRole('dialog', { name: 'Upload diagram' });
  await dialog.getByLabel('Paste text', { exact: true }).check();
  await dialog
    .getByTestId('upload-text')
    .fill(JSON.stringify(claiming(claimed)));
  const answered = waitForApi(page, 'POST', '/builder/drafts');
  await dialog.getByTestId('upload-submit').click();
  const response = await answered;
  const draft = await response.json();
  await expect(builder.canvas).toBeVisible();
  await builder.waitSaved();

  // Pasted text has no file name to record.
  expect
    .soft(response.request().postDataJSON())
    .not.toHaveProperty('sourceFile');
  expect.soft(draft.sourceFile, 'sourceFile').toBeUndefined();
  await expect.soft(sourceFile(builder)).toHaveCount(0);

  // The creator and the time it was made are the document's own; the last
  // edit is the upload, by the user who made it.
  expect(draft.stamp).toEqual({
    createdBy: claimed.createdBy,
    createdAt: claimed.createdAt,
    updatedBy: draft.owner,
    updatedAt: expect.stringMatching(DOCUMENT_TIME),
  });
  expect.soft(draft.stamp.updatedAt).not.toBe(claimed.updatedAt);
  await expectDetail(page, 'created', claimed.createdBy, claimed.createdAt);
  await expectDetail(page, 'edited', draft.owner, draft.stamp.updatedAt);

  // A malformed time is refused, on the text field, and nothing is made.
  await builder.backToDrafts();
  await page.getByTestId('drafts-upload').click();
  await dialog.getByLabel('Paste text', { exact: true }).check();
  const field = dialog.getByTestId('upload-text');
  await field.fill(
    JSON.stringify(claiming({ ...claimed, createdAt: '2020-01-02 03:04:05' })),
  );
  await dialog.getByTestId('upload-submit').click();
  await expect(dialog.getByTestId('upload-error')).toContainText(
    'metadata.createdAt must be a UTC time in the form YYYY-MM-DDTHH:MM:SSZ',
  );
  await expect.soft(field).toHaveAttribute('aria-invalid', 'true');

  expectNoFatal(issues);
});
