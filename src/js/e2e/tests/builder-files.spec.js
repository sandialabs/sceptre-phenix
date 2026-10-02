// Builder v2 file references: a topology whose builder-doc annotation names
// a Builder file on the phenix server (builder-doc.path). The server reads
// such a file only below its base directory, so these tests write their
// files there. They need E2E_BASE_DIR, the server's --base-dir.phenix, and
// the server on this machine; without it they skip:
//
//   phenix ui --features builder-v2 --base-dir.phenix /tmp/phenix-e2e
//   E2E_BASE_DIR=/tmp/phenix-e2e npx playwright test builder-files

const fs = require('node:fs');
const path = require('node:path');

const {
  API,
  expect,
  expectAccessible,
  expectDetail,
  expectNoFatal,
  expectNoInvisibleText,
  labDocument,
  test: base,
  uniqueName,
  waitForApi,
} = require('./builder-support');

const BASE_DIR = process.env.E2E_BASE_DIR || '';

const test = base.extend({
  // A directory of the test's own below the server's base directory, removed
  // with its files afterwards.
  // eslint-disable-next-line no-empty-pattern -- Playwright reads the fixtures a fixture needs from this pattern
  filesDir: async ({}, use, testInfo) => {
    const dir = path.join(
      BASE_DIR,
      'e2e-builder-files',
      uniqueName(testInfo, 'files'),
    );
    fs.mkdirSync(dir, { recursive: true });
    await use(dir);
    fs.rmSync(dir, { recursive: true, force: true });
  },
});

test.skip(
  !BASE_DIR,
  'set E2E_BASE_DIR to the --base-dir.phenix of the phenix server (see file header)',
);

test.use({ announceHold: 100 });

// --- local helpers -----------------------------------------------------------

// Stores the topology `document` publishes as, named `name`, with
// `reference` as its builder-doc annotation: what a topology written by hand
// beside its Builder file holds. The config is sent as YAML, where the
// annotation is a nested map.
async function referTopology(request, tracker, name, document, reference) {
  const exported = await request.post(`${API}/builder-v2/export/topology`, {
    data: { document, name },
  });
  expect(exported.ok(), await exported.text()).toBeTruthy();
  const { yaml } = await exported.json();
  const head = `metadata:\n    name: ${name}\n`;
  expect(yaml, 'the exported topology').toContain(head);

  const annotation = [
    '    annotations:',
    '        builder-doc:',
    ...Object.entries(reference).map(
      ([key, value]) => `            ${key}: ${JSON.stringify(value)}`,
    ),
    '',
  ].join('\n');
  const created = await request.post(`${API}/configs`, {
    headers: { 'Content-Type': 'application/x-yaml' },
    data: yaml.replace(head, head + annotation),
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  tracker.config('Topology', name);
}

// Writes `document` as the Builder file `file`.
function writeBuilderFile(file, document) {
  fs.writeFileSync(file, `${JSON.stringify(document, null, 2)}\n`);
}

// What the server reads for topology `name`: the listing row, with the
// document and its digest.
async function topologyDocument(request, name) {
  const response = await request.get(
    `${API}/builder-v2/topologies/${encodeURIComponent(name)}/document`,
  );
  expect(response.ok(), await response.text()).toBeTruthy();

  return response.json();
}

async function listedDocument(request, name) {
  const listed = await request.get(`${API}/builder-v2/documents`);
  expect(listed.ok(), await listed.text()).toBeTruthy();

  return ((await listed.json()).documents || []).find(
    (entry) => entry.target === name,
  );
}

// labDocument(name) without `server-2` and its connection: a diagram that
// publishes as another topology than labDocument(name) does.
function oneServerDocument(name) {
  const document = labDocument(name);
  const nodes = document.nodes.filter(
    (node) => node.device?.hostname !== 'server-2',
  );

  return {
    ...document,
    nodes,
    edges: document.edges.filter((edge) =>
      nodes.some((node) => node.id === edge.sourceNodeId),
    ),
  };
}

// The id Published Diagrams gives the card of a topology read from a file.
function fileHandle(name) {
  return `file/${name}`;
}

async function openPublishedTab(builder) {
  await builder.open();
  await builder.page.getByTestId('drafts-tab-published').click();
}

// Opens the card of a topology read from a file, read only.
async function viewFile(builder, name) {
  await builder.page.getByTestId(`draft-open-${fileHandle(name)}`).click();
  await expect(builder.canvas).toBeVisible();
}

// The banner above a diagram shown read only: its text, without its button.
function banner(page) {
  return page.getByTestId('builder-published').locator('p');
}

// The page-level error: an alert with its own Dismiss button.
function errorBanner(page) {
  return page.getByTestId('builder-error').getByRole('alert');
}

// Records every draft-create request the page sends from now on.
function watchDraftCreates(page) {
  const creates = [];
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname.endsWith(`${API}/builder-v2/drafts`)
    ) {
      creates.push(request.url());
    }
  });

  return creates;
}

// Submits the Publish dialog as an update, confirms it, and returns the
// server's answer.
async function publishUpdate(page) {
  const submit = page.getByTestId('publish-submit');
  await expect(submit).toHaveText('Update topology');
  const answered = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname.endsWith('/publish'),
  );
  await submit.click();
  await page.getByTestId('confirm-accept').click();

  return answered;
}

// --- tests -------------------------------------------------------------------

test(
  'a topology that names a Builder file is listed as a file, opens read only, and its draft publishes an update that keeps the path',
  { tag: '@cross-browser' },
  async ({ page, request, builder, tracker, filesDir, issues }, testInfo) => {
    const name = uniqueName(testInfo, 'file');
    const file = path.join(filesDir, `${name}.builder.json`);
    const document = labDocument(name);
    writeBuilderFile(file, document);
    const written = fs.readFileSync(file, 'utf8');
    await referTopology(request, tracker, name, document, { path: file });
    const handle = fileHandle(name);
    const { digest } = await topologyDocument(request, name);
    expect(digest).toMatch(/^sha256:[0-9a-f]{64}$/);

    await test.step('the server lists it without reading the file', async () => {
      expect(await listedDocument(request, name)).toEqual({
        source: 'file',
        target: name,
        kind: 'Topology',
        config: `Topology/${name}`,
        path: file,
      });
    });

    await openPublishedTab(builder);

    await test.step('Published Diagrams marks it as a file, says where it is read from, and offers no Delete', async () => {
      const open = page.getByTestId(`draft-open-${handle}`);
      await expect(open).toBeVisible();
      await expect
        .soft(open)
        .toHaveAccessibleName(`Open ${name}, read from ${file}`);
      await expect
        .soft(page.getByTestId(`published-file-${handle}`))
        .toHaveText('File');
      await expect
        .soft(page.getByTestId(`published-path-${handle}`))
        .toHaveText(`Read from ${file}`);
      // Deleting the topology here would leave the file, and the file is
      // not phenix's to delete.
      await expect
        .soft(page.getByTestId(`published-delete-${handle}`))
        .toHaveCount(0);
    });

    await test.step('it opens read only, and the banner says where it is read from', async () => {
      const creates = watchDraftCreates(page);
      await viewFile(builder, name);
      await expect
        .soft(banner(page))
        .toHaveText(
          `You are viewing the diagram of topology ${name}, read from ${file} on the phenix server. ` +
            'Edit it as a draft to make changes.',
        );
      await expect
        .soft(builder)
        .toHaveAnnounced(`Opened the diagram of topology ${name}, read only.`);
      await expect.soft(page.getByTestId('builder-name')).toHaveText(name);
      await builder.expectCounts(
        { devices: 2, switches: 1, networks: 1, links: 2 },
        { soft: true },
      );
      await expect
        .soft(builder.toolbar('publish'))
        .toHaveAttribute('aria-disabled', 'true');
      await expect.soft(page.getByTestId('builder-name-edit')).toHaveCount(0);
      expect.soft(creates, 'draft creates while viewing').toEqual([]);
    });

    const draft =
      await test.step('Edit as a draft makes a draft that names the file as it was read', async () => {
        const created = waitForApi(page, 'POST', '/builder-v2/drafts');
        await page.getByTestId('published-edit').click();
        const body = await (await created).json();
        await expect(page.getByTestId('builder-published')).toHaveCount(0);
        await builder.waitSaved();
        await expect
          .soft(builder)
          .toHaveAnnounced(
            `Opened the diagram of topology ${name} as a new draft.`,
          );
        expect.soft(body.sourceToken).toBe(`builder-file/${name}/${digest}`);
        // The first snapshot is the file's document, unchanged.
        expect.soft(body.digest, 'digest').toBe(digest);

        return body;
      });

    await test.step('an edited draft publishes an update, which keeps the path and says the file is not written', async () => {
      await builder.palette('note').click();
      await builder.expectCounts({
        devices: 2,
        switches: 1,
        networks: 1,
        links: 2,
        notes: 1,
      });
      await builder.persisted(
        draft,
        (doc) => doc.nodes.filter((node) => node.kind === 'note').length,
        1,
      );
      await builder.waitSaved();

      const dialog = await builder.openDialog('publish');
      await expect(dialog.getByTestId('publish-name')).toHaveValue(name);
      await expect
        .soft(page.locator('#publish-topology-action-hint'))
        .toHaveText('A topology with this name exists and will be updated.');
      const response = await publishUpdate(page);
      const body = await response.text();
      expect(response.status(), body).toBe(200);

      const result = dialog.getByTestId('publish-result');
      await expect(result).toContainText('Published. Every stage succeeded.');
      await expect
        .soft(result.locator('li[data-status]'))
        .toHaveText([
          /^document: created\b/,
          /^topology: updated\b/,
          /^draft: ok\b/,
        ]);
      const warning =
        `Topology ${name} names the Builder file ${file}, which Publish does not change. ` +
        'Export the diagram and replace the file to keep it in step.';
      expect.soft(JSON.parse(body).warnings).toEqual([warning]);
      await expect
        .soft(result.locator('li').filter({ hasText: 'Warning:' }))
        .toHaveText(`Warning: ${warning}`);

      // The annotation now names the stored document too, and keeps the path.
      const config = await builder.config('Topology', name);
      const reference = config?.metadata?.annotations?.['builder-doc'];
      expect(reference).toEqual({
        digest: expect.stringMatching(/^sha256:[0-9a-f]{64}$/),
        id: expect.stringMatching(/^[0-9a-f]{64}$/),
        path: file,
      });
      expect.soft(reference.digest, 'the new digest').not.toBe(digest);
      expect.soft(fs.readFileSync(file, 'utf8'), 'the file').toBe(written);

      await dialog.getByRole('button', { name: 'Close', exact: true }).click();
      await expect(builder.dialog).toHaveCount(0);
    });

    await test.step('the topology is then read from its stored document', async () => {
      const stored = await listedDocument(request, name);
      expect(stored).toMatchObject({ source: 'store', draftId: draft.id });
      expect(stored.path, 'a stored row names no path').toBeUndefined();

      await builder.backToDrafts();
      await page.getByTestId('drafts-tab-published').click();
      await expect(page.getByTestId(`draft-open-${stored.id}`)).toBeVisible();
      await expect
        .soft(page.getByTestId(`published-delete-${stored.id}`))
        .toBeVisible();
      await expect
        .soft(page.getByTestId(`draft-open-${handle}`))
        .toHaveCount(0);
      await expect
        .soft(page.getByTestId(`published-file-${stored.id}`))
        .toHaveCount(0);
    });

    expectNoFatal(issues);
  },
);

test('a Builder file phenix cannot use is still listed, and opening it says why', async ({
  page,
  request,
  builder,
  tracker,
  filesDir,
  issues,
}, testInfo) => {
  const names = Object.fromEntries(
    ['outside', 'missing', 'pinned', 'invalid'].map((part) => [
      part,
      uniqueName(testInfo, `file-${part}`),
    ]),
  );
  const files = {
    // Beside the base directory, not below it. The file need not exist: the
    // server refuses the path before it looks.
    outside: path.join(
      path.dirname(BASE_DIR),
      `${path.basename(BASE_DIR)}-outside`,
      `${names.outside}.builder.json`,
    ),
    missing: path.join(filesDir, `${names.missing}.builder.json`),
    pinned: path.join(filesDir, `${names.pinned}.builder.json`),
    invalid: path.join(filesDir, `${names.invalid}.builder.yaml`),
  };
  writeBuilderFile(files.pinned, labDocument(names.pinned));
  // Nothing of the file's text may come back in the error.
  fs.writeFileSync(files.invalid, 'secret: e2e-must-not-be-shown\n');

  const cases = [
    {
      part: 'outside',
      what: 'a path outside the base directory',
      reference: { path: files.outside },
      status: 422,
      sentence: `Builder file ${files.outside} is outside ${BASE_DIR}, the directory phenix reads Builder files from.`,
    },
    {
      part: 'missing',
      what: 'a file that does not exist',
      reference: { path: files.missing },
      status: 404,
      sentence: `Builder file ${files.missing} does not exist on this phenix server.`,
    },
    {
      part: 'pinned',
      what: 'a file that is not the one the topology pins by digest',
      reference: { path: files.pinned, digest: `sha256:${'0'.repeat(64)}` },
      status: 422,
      sentence: `Builder file ${files.pinned} does not match the digest topology ${names.pinned} records for it.`,
    },
    {
      part: 'invalid',
      what: 'a file that is not a Builder document',
      reference: { path: files.invalid },
      status: 422,
      sentence: `Builder file ${files.invalid} is not a valid Builder document. Upload it in the Builder to see why.`,
    },
  ];
  for (const { part, reference } of cases) {
    await referTopology(
      request,
      tracker,
      names[part],
      labDocument(names[part]),
      reference,
    );
  }

  const creates = watchDraftCreates(page);
  await openPublishedTab(builder);

  for (const { part, what, status, sentence } of cases) {
    await test.step(what, async () => {
      const name = names[part];
      const open = page.getByTestId(`draft-open-${fileHandle(name)}`);
      // The listing reads no file, so it lists the topology all the same.
      await expect(open).toBeVisible();
      await expect
        .soft(page.getByTestId(`published-path-${fileHandle(name)}`))
        .toHaveText(`Read from ${files[part]}`);

      const read = waitForApi(
        page,
        'GET',
        `/builder-v2/topologies/${name}/document`,
      );
      await open.click();
      const response = await read;
      expect.soft(response.status(), what).toBe(status);
      expect
        .soft(await response.json(), what)
        .toEqual({ message: sentence, cause: '' });
      await expect(errorBanner(page)).toHaveText(
        `Could not open the diagram of topology ${name}. ${sentence}`,
      );
      // The drafts page stays, and its other cards can still be opened.
      await expect.soft(builder.canvas).toHaveCount(0);
      await expect.soft(builder.landingHeading).toBeVisible();
      await expect.soft(open).not.toHaveAttribute('aria-disabled', 'true');
    });
  }

  expect.soft(creates, 'draft creates').toEqual([]);
  expectNoFatal(issues);
});

test('a Builder file changed after it was opened is neither edited nor published over', async ({
  page,
  request,
  builder,
  tracker,
  filesDir,
}, testInfo) => {
  const name = uniqueName(testInfo, 'file-changed');
  const file = path.join(filesDir, `${name}.builder.json`);
  const document = labDocument(name);
  writeBuilderFile(file, document);
  await referTopology(request, tracker, name, document, { path: file });
  const opened = await topologyDocument(request, name);

  await openPublishedTab(builder);
  await viewFile(builder, name);
  await expect(banner(page)).toContainText(`read from ${file}`);

  // The same diagram with a description: another document, which publishes
  // as the same topology.
  const rewrite = (description) =>
    writeBuilderFile(file, { ...document, description });

  await test.step('Edit as a draft of a view the file has left behind is refused', async () => {
    rewrite('changed while it was shown');
    const created = waitForApi(page, 'POST', '/builder-v2/drafts');
    await page.getByTestId('published-edit').click();
    expect((await created).status()).toBe(409);
    await expect(errorBanner(page)).toHaveText(
      'Could not create the draft. ' +
        `The Builder file of topology ${name} changed since it was opened. Open its diagram again.`,
    );
    // The diagram stays as it was read, still read only.
    await expect.soft(page.getByTestId('builder-published')).toBeVisible();
    await expect.soft(page.getByTestId('builder-name-edit')).toHaveCount(0);
  });

  const draft =
    await test.step('opened again, it is read as it now is, and edited as a draft', async () => {
      await builder.backToDrafts();
      await page.getByTestId('drafts-tab-published').click();
      await viewFile(builder, name);
      const current = await topologyDocument(request, name);
      expect(current.digest, 'the digest of the changed file').not.toBe(
        opened.digest,
      );

      const created = waitForApi(page, 'POST', '/builder-v2/drafts');
      await page.getByTestId('published-edit').click();
      const response = await created;
      expect(response.status(), await response.text()).toBe(201);
      const body = await response.json();
      expect
        .soft(body.sourceToken)
        .toBe(`builder-file/${name}/${current.digest}`);
      await builder.waitSaved();

      return body;
    });

  await test.step('an update is refused once the file has changed again, and the topology is left as it was', async () => {
    rewrite('changed after the draft was made');
    const dialog = await builder.openDialog('publish');
    await expect(dialog.getByTestId('publish-name')).toHaveValue(name);
    const response = await publishUpdate(page);
    expect(response.status(), await response.text()).toBe(409);
    await expect(dialog.getByTestId('publish-error')).toHaveText(
      'Could not publish the diagram. ' +
        `Topology ${name} or its Builder file changed after this draft was opened from the file.`,
    );
    await expect
      .soft(dialog.getByTestId('publish-name'))
      .toHaveAttribute('aria-invalid', 'true');
    await expect(dialog.getByTestId('publish-result')).toHaveCount(0);

    const config = await builder.config('Topology', name);
    expect
      .soft(config?.metadata?.annotations?.['builder-doc'])
      .toEqual({ path: file });
    expect
      .soft(await listedDocument(request, name))
      .toMatchObject({ source: 'file', path: file });
    expect.soft(draft.id).toBeTruthy();
  });
});

test('a draft of a Builder file that differs from its topology cannot update the topology, and publishes as a new one', async ({
  page,
  request,
  builder,
  tracker,
  filesDir,
}, testInfo) => {
  const name = uniqueName(testInfo, 'file-differs');
  const other = uniqueName(testInfo, 'file-differs-new');
  const file = path.join(filesDir, `${name}.builder.json`);
  // The stored topology lacks a device the file has: it was changed, or the
  // file was, after the two were written.
  writeBuilderFile(file, labDocument(name));
  await referTopology(request, tracker, name, oneServerDocument(name), {
    path: file,
  });
  expect((await topologyDocument(request, name)).topologyDiffers).toBe(true);
  const before = await builder.config('Topology', name);

  await openPublishedTab(builder);
  await viewFile(builder, name);
  await expect(banner(page)).toContainText(
    `It differs from topology ${name} as stored. A draft made from it can be published ` +
      `as a new topology, not as an update of ${name}.`,
  );
  await page.getByTestId('published-edit').click();
  await expect(page.getByTestId('builder-published')).toHaveCount(0);
  await builder.waitSaved();

  const dialog = await builder.openDialog('publish');
  await expect(dialog.getByTestId('publish-name')).toHaveValue(name);

  await test.step('the update is refused, on the name', async () => {
    const response = await publishUpdate(page);
    expect(response.status(), await response.text()).toBe(409);
    await expect(dialog.getByTestId('publish-error')).toHaveText(
      'Could not publish the diagram. ' +
        `Topology ${name} is not what its Builder file publishes, so this draft cannot update it.`,
    );
    await expect
      .soft(dialog.getByTestId('publish-name'))
      .toHaveAttribute('aria-invalid', 'true');
    expect
      .soft(await builder.config('Topology', name), 'the topology')
      .toEqual(before);
  });

  await test.step('another name creates a topology, which names no file', async () => {
    tracker.config('Topology', other);
    await dialog.getByTestId('publish-name').fill(other);
    const submit = dialog.getByTestId('publish-submit');
    await expect(submit).toHaveText('Create topology');
    const answered = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname.endsWith('/publish'),
    );
    await submit.click();
    const response = await answered;
    const body = await response.text();
    expect(response.status(), body).toBe(200);
    expect.soft(JSON.parse(body).warnings || []).toEqual([]);

    const created = await builder.config('Topology', other);
    expect
      .soft(Object.keys(created?.metadata?.annotations?.['builder-doc'] || {}))
      .toEqual(['digest', 'id']);
    expect.soft(created?.spec?.nodes).toHaveLength(2);
  });
});

for (const scheme of ['light', 'dark']) {
  test(
    `axe finds no serious violations in a Builder file's card and read-only view in the ${scheme} theme`,
    { tag: '@axe' },
    async ({ page, request, builder, tracker, filesDir }, testInfo) => {
      const name = uniqueName(testInfo, `file-axe-${scheme}`);
      const file = path.join(filesDir, `${name}.builder.json`);
      const made = {
        author: 'alice',
        createdAt: '2020-01-02T03:04:05Z',
        updatedBy: 'bob',
        updatedAt: '2020-02-03T04:05:06Z',
      };
      // The stored topology lacks a device the file has, so the banner says
      // that the two differ.
      writeBuilderFile(file, { ...labDocument(name), ...made });
      await referTopology(request, tracker, name, oneServerDocument(name), {
        path: file,
      });

      // Reduced motion turns off color transitions, so Edit as a draft,
      // which is busy while the diagram opens, is measured at its final
      // colors.
      await page.emulateMedia({
        colorScheme: scheme,
        reducedMotion: 'reduce',
      });
      await openPublishedTab(builder);
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        scheme,
      );

      await test.step('the card on Published Diagrams', async () => {
        await expect(
          page.getByTestId(`published-file-${fileHandle(name)}`),
        ).toBeVisible();
        await expectAccessible(page, {
          soft: true,
          label: `axe on Published Diagrams with a file (${scheme})`,
        });
        await expectNoInvisibleText(page);
      });

      await test.step('the read-only view, its banner and its Details', async () => {
        await viewFile(builder, name);
        await expect(banner(page)).toHaveText(
          `You are viewing the diagram of topology ${name}, read from ${file} on the phenix server. ` +
            `It differs from topology ${name} as stored. A draft made from it can be published ` +
            `as a new topology, not as an update of ${name}. ` +
            'Edit it as a draft to make changes.',
        );
        // A file's document holds what its author wrote: the server does not
        // stamp it.
        await expectDetail(page, 'created', made.author, made.createdAt);
        await expectDetail(page, 'edited', made.updatedBy, made.updatedAt);
        await expect(page.getByTestId('published-edit')).not.toHaveAttribute(
          'aria-disabled',
          'true',
        );
        await expectAccessible(page, {
          soft: true,
          label: `axe on a Builder file shown read only (${scheme})`,
        });
        await expectNoInvisibleText(page);
      });
    },
  );
}
