// Builder publication: the Publish and Scenario dialogs, the configs a
// publish writes, and how failures are reported.

const crypto = require('node:crypto');
const fs = require('node:fs');

const {
  API,
  test,
  expect,
  contrast,
  devicesOf,
  draftPath,
  expectAccessible,
  expectNoFatal,
  isPublishPreview,
  isPublishRequest,
  isPublishResponse,
  labDocument,
  openConfigs,
  publishTopology,
  uniqueName,
} = require('./builder-support');

const V2 = 'phenix.sandia.gov/v2';
// What a partial publication's answer says finishes it (writePublishPartial).
const RETRY_ADVICE =
  'Publish again with the same choices to finish. The server skips the steps that are already done.';

// --- local helpers -----------------------------------------------------------

// Waits until the server copy of the draft satisfies `check` and every change
// is saved: publishing reads the server snapshot.
async function synced(builder, draft, check, { soft = false } = {}) {
  await builder.persisted(draft, check, true, { soft });
  await builder.waitSaved();
}

// Two Server devices (`server`, `server-2`) on one switch (network EXP), both
// connected through the Add a connection dialog.
async function buildLab(builder, draft) {
  await builder.palette('template-server').click();
  await builder.palette('template-server').click();
  await builder.palette('switch').click();
  await builder.expectSummary('2 devices, 1 switch, 1 network');

  await builder.connect({ label: 'server' }, { index: 1 });
  await builder.expectSummary('1 connection');
  await builder.connect({ label: 'server-2' }, { index: 1 });
  await builder.expectSummary('2 connections');

  await synced(
    builder,
    draft,
    (document) => (document.edges || []).length === 2,
  );
}

// Seeds labDocument(name) as a draft and opens it. The Publish dialog offers
// the document name as the topology name.
async function openLab(builder, name, document = labDocument(name)) {
  const draft = await builder.seedDraft(document);
  await builder.openDraft(draft);
  await builder.expectSummary('2 connections');

  return draft;
}

async function openPublish(builder) {
  const dialog = await builder.openDialog('publish');
  await expect(
    dialog.getByRole('heading', { name: 'Publish diagram' }),
  ).toBeVisible();

  return dialog;
}

// Fills the Publish dialog. `experiment` switches to topology + experiment
// mode. Does not submit.
async function fillPublish(page, { topology, experiment }) {
  await page.getByTestId('publish-name').fill(topology);

  if (experiment) {
    await page.getByTestId('publish-mode-experiment').check();
    await page.getByTestId('publish-experiment').fill(experiment);
  }
}

// Submits the Publish dialog (confirming an update), expects the publish call
// to answer `status`, and returns the response body.
async function expectPublish(page, status) {
  const submit = page.getByTestId('publish-submit');
  const updates = /update/i.test(await submit.textContent());
  const pending = page.waitForResponse(isPublishResponse);
  await submit.click();
  if (updates) {
    await page.getByTestId('confirm-accept').click();
  }
  const response = await pending;
  const body = await response.text();
  expect(response.status(), body).toBe(status);

  return body;
}

// Records the publish requests (not dry runs) the page sends from now on.
function watchPublishes(page) {
  const sent = [];
  page.on('request', (request) => {
    if (isPublishRequest(request)) {
      sent.push(request.postDataJSON());
    }
  });

  return sent;
}

// The Publish dialog's refusal of an existing name this draft may not update.
function cannotUpdate(name) {
  return (
    `A topology named "${name}" already exists. This diagram cannot update it. ` +
    'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it. ' +
    'Enter another name to create a new topology.'
  );
}

// Stage name -> data-status from the publish result list.
async function stageStatuses(page) {
  const result = page.getByTestId('publish-result');
  // Soft: a missing list yields {} for the caller to report, and merged tests
  // go on to their server-side checks.
  await expect.soft(result.locator('li[data-status]').first()).toBeVisible();

  return result
    .locator('li[data-status]')
    .evaluateAll((items) =>
      Object.fromEntries(
        items.map((item) => [
          item.querySelector('strong').textContent.trim().replace(/:$/, ''),
          item.dataset.status,
        ]),
      ),
    );
}

function nodesByHostname(config) {
  return Object.fromEntries(
    (config.spec.nodes || []).map((node) => [node.general.hostname, node]),
  );
}

// The builder-doc annotation of a published topology, a map that names the
// published document by digest and id, or undefined when it is missing.
function manifestOf(config) {
  return config?.metadata?.annotations?.['builder-doc'];
}

// The record of the document published to topology `name`, as Published
// Diagrams lists it, with the draft and snapshot it came from.
async function publishedRecord(request, name) {
  const listed = await request.get(`${API}/builder/documents`);
  expect(listed.ok(), await listed.text()).toBeTruthy();

  return ((await listed.json()).documents || []).find(
    (entry) => entry.target === name,
  );
}

function topologyHint(page) {
  return page.locator('#publish-topology-action-hint');
}

function closeButton(page) {
  return page.getByRole('button', { name: 'Close', exact: true });
}

// A topology with a legacy Builder diagram, which only the draft imported
// from it may update.
function legacyTopology(name) {
  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: {
      name,
      annotations: { 'builder-xml': '<mxGraphModel />' },
    },
    spec: { nodes: [] },
  };
}

function scenarioYaml(name, app) {
  return [
    `apiVersion: ${V2}`,
    'kind: Scenario',
    'metadata:',
    `  name: ${name}`,
    'spec:',
    '  apps:',
    `    - name: ${app}`,
    '      disabled: true',
    '      metadata:',
    '        purpose: builder e2e',
    '',
  ].join('\n');
}

function scenarioConfig(name, app) {
  return {
    apiVersion: V2,
    kind: 'Scenario',
    metadata: { name },
    spec: {
      apps: [{ name: app, disabled: true, metadata: { purpose: 'seeded' } }],
    },
  };
}

// Opens the Scenarios dialog from the toolbar.
async function openScenarios(builder) {
  const dialog = await builder.openDialog('scenario');
  await expect(
    dialog.getByRole('heading', { name: 'Scenarios', exact: true }),
  ).toBeVisible();

  return dialog;
}

// The box of scenario `name` in the Scenarios dialog's list, which is set
// while the diagram uses the scenario.
function scenarioBox(dialog, name) {
  return dialog.getByRole('checkbox', { name, exact: true });
}

// Chooses `yaml` as the scenario file of the Scenarios dialog.
async function chooseScenarioFile(dialog, yaml, name = 'scenario.yaml') {
  await dialog.getByTestId('scenario-file').setInputFiles({
    name,
    mimeType: 'application/yaml',
    buffer: Buffer.from(yaml),
  });
}

// Saves the Scenarios dialog's list, and waits until the server copy of the
// draft lists `names`.
async function saveScenarios(builder, draft, names) {
  await builder.dialog.getByTestId('scenario-submit').click();
  await expect(builder.dialog).toHaveCount(0);
  await synced(
    builder,
    draft,
    (document) =>
      JSON.stringify(document.scenarios || []) === JSON.stringify(names),
  );
}

// --- topology only -------------------------------------------------------------

test('topology-only publish offers a new topology named after the diagram, and refuses a name the server would refuse and a legacy topology', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'topo');
  const legacy = uniqueName(testInfo, 'legacy');
  const lateLegacy = uniqueName(testInfo, 'legacy-late');
  // Seeded before open(): the editor reads the topology list when it mounts.
  await builder.seedConfig(legacyTopology(legacy));

  await builder.open();
  const draft = await builder.createBlank();
  await builder.rename(topology);
  await buildLab(builder, draft);

  await test.step('the dialog offers a new topology named after the diagram', async () => {
    await openPublish(builder);
    await expect.soft(page.getByTestId('publish-name')).toHaveValue(topology);
    await expect
      .soft(topologyHint(page))
      .toHaveText('Publishing creates a new topology.');
    // A new topology is no warning, and a valid name shows no naming rule.
    await expect
      .soft(topologyHint(page))
      .not.toHaveClass(/\bbuilder-hint--warning\b/);
    await expect
      .soft(topologyHint(page).locator('svg.builder-icon--warning'))
      .toHaveCount(0);
    await expect.soft(page.getByTestId('publish-name-rule')).toHaveCount(0);
    await expect.soft(builder.dialog).not.toContainText('Names can use only');
    await expect
      .soft(page.getByTestId('publish-name'))
      .toHaveAccessibleDescription('Publishing creates a new topology.');
    await expect
      .soft(page.getByTestId('publish-submit'))
      .toHaveText('Create topology');
    await expect
      .soft(builder.dialog)
      .toContainText(
        '2 devices, 1 switch, 1 network and 2 connections are ready to publish.',
      );
  });

  await test.step('a name the server would refuse is refused on its field, before sending', async () => {
    const name = page.getByTestId('publish-name');
    const rule = page.getByTestId('publish-name-rule');
    const RULE =
      'Names can use only letters, numbers, underscores (_), at signs (@), ' +
      'periods (.) and hyphens (-), with no spaces.';

    // While typing, the field says why the name breaks the rule, each
    // character it may not use once, then the rule.
    await name.fill('lab/a#b/c');
    const characters = `This name is not valid: it contains characters that a name cannot contain: "/", "#". ${RULE}`;
    await expect.soft(rule).toHaveText(characters);
    await expect
      .soft(name)
      .toHaveAccessibleDescription(
        `Publishing creates a new topology. ${characters}`,
      );
    // A valid name takes the rule away.
    await name.fill(topology);
    await expect.soft(rule).toHaveCount(0);

    await name.fill('Untitled topology');
    await expect
      .soft(rule)
      .toHaveText(`This name is not valid: it contains a space. ${RULE}`);
    await page.getByTestId('publish-submit').press('Enter');
    const problem =
      'The topology name "Untitled topology" is not valid: it contains a space. ' +
      `${RULE} For example: Untitled-topology`;
    await expect.soft(page.getByTestId('publish-error')).toHaveText(problem);
    await expect.soft(name).toHaveAttribute('aria-invalid', 'true');
    await expect.soft(name).toBeFocused();
    // The error says what the rule does, so the field is described by its
    // hint and the error, and the rule is not read twice.
    await expect
      .soft(name)
      .toHaveAccessibleDescription(
        `Publishing creates a new topology. ${problem}`,
      );
    await expect(page.getByTestId('publish-result')).toHaveCount(0);
  });

  await test.step('a legacy builder-xml topology is refused before sending and left untouched', async () => {
    const sent = watchPublishes(page);
    await fillPublish(page, { topology: legacy });
    // This diagram was not imported from it, so the form does not offer to
    // update it.
    await expect
      .soft(topologyHint(page))
      .toHaveText(
        'A topology with this name already exists. This diagram cannot update it. ' +
          'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it. ' +
          'Enter another name to create a new topology.',
      );
    await expect
      .soft(page.getByTestId('publish-submit'))
      .toHaveText('Create topology');
    await page.getByTestId('publish-submit').click();
    // The refusal is reported on the field it is about.
    const name = page.getByTestId('publish-name');
    await expect
      .soft(page.getByTestId('publish-error'))
      .toHaveText(cannotUpdate(legacy));
    await expect.soft(name).toHaveAttribute('aria-invalid', 'true');
    await expect.soft(name).toBeFocused();
    expect.soft(sent, 'publish requests').toEqual([]);
    // The form must still be showing for the next step to publish again.
    await expect(page.getByTestId('publish-result')).toHaveCount(0);

    const untouched = await builder.config('Topology', legacy);
    expect.soft(untouched, 'legacy topology config').toBeTruthy();
    const annotations = untouched?.metadata?.annotations || {};
    expect.soft(annotations['builder-doc'], 'builder-doc').toBeUndefined();
    expect
      .soft(annotations['builder-xml'], 'builder-xml')
      .toBe('<mxGraphModel />');
    expect.soft(untouched?.spec?.nodes || [], 'legacy nodes').toHaveLength(0);
  });

  await test.step('a legacy topology stored meanwhile is refused by the server, and reported the same way', async () => {
    // Stored after the dialog read the list, so only the server can refuse
    // it: the dialog asked for a new topology of that name.
    await fillPublish(page, { topology: lateLegacy });
    await expect
      .soft(topologyHint(page))
      .toHaveText('Publishing creates a new topology.');
    await builder.seedConfig(legacyTopology(lateLegacy));
    await expectPublish(page, 409);
    const error = page.getByTestId('publish-error');
    await expect
      .soft(error)
      .toHaveText(`Could not publish the diagram. ${cannotUpdate(lateLegacy)}`);
    await expect
      .soft(page.getByTestId('publish-name'))
      .toHaveAttribute('aria-invalid', 'true');

    // The list is read again, so the form now says why the name cannot be
    // used, and publishing again is refused before anything is sent.
    await expect
      .soft(topologyHint(page))
      .toHaveText(
        /^A topology with this name already exists\. This diagram cannot update it\. /,
      );
    const sent = watchPublishes(page);
    await page.getByTestId('publish-submit').click();
    await expect.soft(error).toHaveText(cannotUpdate(lateLegacy));
    expect.soft(sent, 'publish requests').toEqual([]);
    // The form must still be showing for the next step to publish again.
    await expect(page.getByTestId('publish-result')).toHaveCount(0);

    const untouched = await builder.config('Topology', lateLegacy);
    expect
      .soft(untouched?.metadata?.annotations, 'annotations')
      .toEqual({ 'builder-xml': '<mxGraphModel />' });
  });

  expectNoFatal(issues);
});

test('topology-only publish writes the diagram, offers an update on reopen, and publishes again once Published Diagrams deletes the topology', async ({
  page,
  builder,
  tracker,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'topo');
  tracker.config('Topology', topology);
  const lab = labDocument(topology);
  // A Router device publishes with node type Router.
  devicesOf(lab)[1].device.spec.type = 'Router';
  const types = { server: 'VirtualMachine', 'server-2': 'Router' };
  const draft = await openLab(builder, topology, lab);
  await openPublish(builder);

  await test.step('a new name publishes hostnames, types, VLANs and the builder-doc manifest', async () => {
    await fillPublish(page, { topology });
    await expect
      .soft(topologyHint(page))
      .toHaveText('Publishing creates a new topology.');
    await expectPublish(page, 200);
    await expect
      .soft(page.getByTestId('publish-result'))
      .toContainText('Published. Every stage succeeded.');
    expect.soft(await stageStatuses(page)).toEqual({
      document: 'created',
      topology: 'created',
      draft: 'ok',
    });
    // The page's live region is unreadable behind the modal, so its message
    // waits until the dialog closes. The dialog's result is what is read.
    await expect.soft(builder).not.toHaveAnnounced('Diagram published.');

    const config = await builder.config('Topology', topology);
    expect(config, 'published topology config').toBeTruthy();
    // The manifest names the immutable document published from this draft,
    // by digest and id and nothing else.
    const manifest = manifestOf(config);
    expect.soft(manifest, 'builder-doc manifest').toEqual({
      digest: expect.stringMatching(/^sha256:[0-9a-f]{64}$/),
      id: expect.stringMatching(/^[0-9a-f]{64}$/),
    });
    expect
      .soft(await publishedRecord(builder.request, topology), 'record')
      .toMatchObject({
        source: 'store',
        id: manifest?.id,
        digest: manifest?.digest,
        draftId: draft.id,
      });

    const nodes = nodesByHostname(config);
    expect.soft(Object.keys(nodes).sort()).toEqual(['server', 'server-2']);
    for (const hostname of ['server', 'server-2']) {
      const interfaces = nodes[hostname]?.network?.interfaces;
      expect
        .soft(nodes[hostname]?.type, `${hostname} type`)
        .toBe(types[hostname]);
      expect.soft(interfaces, `${hostname} interfaces`).toHaveLength(1);
      expect.soft(interfaces?.[0], `${hostname} eth0`).toMatchObject({
        name: 'eth0',
        vlan: 'EXP',
      });
    }

    await closeButton(page).click();
    await expect.soft(builder.dialog).toHaveCount(0);
    // Once the dialog is gone, the held message is read.
    await expect.soft(builder).toHaveAnnounced('Diagram published.');
  });

  await test.step('reopening the draft offers to update the topology', async () => {
    // A fresh mount reads the server's topology list again.
    await builder.openDraft(draft);
    await openPublish(builder);
    await expect.soft(page.getByTestId('publish-name')).toHaveValue(topology);
    await expect
      .soft(topologyHint(page))
      .toHaveText('A topology with this name exists. Publishing updates it.');

    // An update warns: a sign before words that say it without the sign or
    // color, readable in either theme.
    const hint = topologyHint(page);
    const sign = hint.locator('svg.builder-icon--warning');
    await expect.soft(hint).toHaveClass(/\bbuilder-hint--warning\b/);
    await expect.soft(sign).toBeVisible();
    await expect.soft(sign).toHaveAttribute('aria-hidden', 'true');
    expect
      .soft(
        await hint.evaluate((element) =>
          [...element.children].map((child) => child.tagName.toLowerCase()),
        ),
        'the sign comes before the words',
      )
      .toEqual(['svg', 'span']);
    // Reduced motion turns color transitions off, so the colors are
    // measured as they end.
    for (const scheme of ['light', 'dark']) {
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' });
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        scheme,
      );
      const [{ ratio: words }] = await contrast(hint.locator('span'));
      expect
        .soft(words, `${scheme}: the warning's words`)
        .toBeGreaterThanOrEqual(4.5);
      const [{ ratio: icon }] = await contrast(sign);
      expect
        .soft(icon, `${scheme}: the warning sign`)
        .toBeGreaterThanOrEqual(3);
    }
    await page.emulateMedia({ colorScheme: 'light', reducedMotion: null });

    // The button says it overwrites, not just "Publish".
    const submit = page.getByTestId('publish-submit');
    await expect.soft(submit).toHaveText('Update topology');

    // Publish asks before an update, focus on Cancel. Escape returns to the
    // form and sends nothing.
    const sent = watchPublishes(page);
    await submit.press('Enter');
    const confirm = page.getByRole('alertdialog', {
      name: `Replace topology ${topology}?`,
    });
    await expect
      .soft(confirm)
      .toHaveAccessibleDescription(
        `Publishing replaces the topology "${topology}" on the server with this diagram. ` +
          'You cannot undo this.',
      );
    await expect.soft(confirm.getByTestId('confirm-cancel')).toBeFocused();
    await expect
      .soft(confirm.getByTestId('confirm-accept'))
      .toHaveText('Update topology');
    await expectAccessible(page, {
      include: '[data-testid="builder-confirm"]',
      soft: true,
      label: 'Publish confirmation',
    });
    await page.keyboard.press('Escape');
    await expect(confirm).toHaveCount(0);
    await expect.soft(builder.dialog).toBeVisible();
    await expect.soft(submit).toBeFocused();
    expect.soft(sent, 'publish requests').toEqual([]);

    // Nothing changed since the last publish, so the server treats the update
    // as already applied.
    await expectPublish(page, 200);
    await expect
      .soft(page.getByTestId('publish-result'))
      .toContainText('Every stage succeeded');
    expect
      .soft(await stageStatuses(page))
      .toMatchObject({ topology: 'skipped' });
  });

  await test.step('Published Diagrams deletes the topology, and the draft publishes it again', async () => {
    const manifest = manifestOf(await builder.config('Topology', topology));
    expect(manifest?.id, 'builder-doc manifest id').toBeTruthy();

    await closeButton(page).click();
    await builder.backToDrafts();
    await page.getByTestId('drafts-tab-published').click();

    // Deleting asks first, naming the topology, with focus on Cancel.
    const remove = page.getByTestId(`published-delete-${manifest.id}`);
    await remove.click();
    const confirm = page.getByRole('alertdialog', {
      name: `Delete topology ${topology}?`,
    });
    await expect
      .soft(confirm)
      .toHaveAccessibleDescription(
        'This deletes the topology from phēnix. It does not change the drafts and experiments made from it.',
      );
    await expect.soft(confirm.getByTestId('confirm-cancel')).toBeFocused();

    // Held until the button has been checked while the delete runs.
    let release;
    const held = new Promise((resolve) => {
      release = resolve;
    });
    const path = `${API}/builder/documents/${manifest.id}`;
    await page.route(`**${path}`, async (route) => {
      if (route.request().method() === 'DELETE') {
        await held;
      }
      await route.fallback();
    });
    const deleted = page.waitForResponse(
      (response) =>
        response.request().method() === 'DELETE' &&
        new URL(response.url()).pathname === path,
    );
    await confirm.getByTestId('confirm-accept').click();
    await expect.soft(remove).toHaveAttribute('aria-disabled', 'true');
    await expect.soft(remove).toHaveAccessibleName(/^Deleting /);
    // A second click while it runs asks nothing and sends nothing.
    await remove.click({ force: true });
    await expect.soft(page.getByRole('alertdialog')).toHaveCount(0);
    release();
    expect((await deleted).status()).toBe(204);
    await page.unroute(`**${path}`);

    await expect.soft(remove).toHaveCount(0);
    await expect
      .soft(page.getByTestId(`draft-open-${manifest.id}`))
      .toHaveCount(0);
    await expect.soft(builder).toHaveAnnounced(`Deleted topology ${topology}.`);
    // Focus moves to the card that took its place, or to the tab.
    await expect
      .soft(
        page.locator(
          '#panel-published [data-testid^="draft-open-"]:focus, #tab-published:focus',
        ),
      )
      .toHaveCount(1);

    const listed = await builder.request.get(`${API}/configs`);
    expect(listed.ok(), await listed.text()).toBeTruthy();
    const names = ((await listed.json()).configs || [])
      .filter((config) => config.kind === 'Topology')
      .map((config) => config.metadata.name);
    expect.soft(names, 'topologies').not.toContain(topology);

    // The draft that published it creates it again.
    await builder.openDraft(draft);
    await openPublish(builder);
    await expect
      .soft(topologyHint(page))
      .toHaveText('Publishing creates a new topology.');
    await expect
      .soft(page.getByTestId('publish-submit'))
      .toHaveText('Create topology');
    await expectPublish(page, 200);
    expect
      .soft(await stageStatuses(page))
      .toMatchObject({ topology: 'created' });
    expect
      .soft(await builder.config('Topology', topology), 'recreated topology')
      .toBeTruthy();
  });

  expectNoFatal(issues);
});

// --- the builder-doc annotation ------------------------------------------------

test('a published topology names its document in a builder-doc map, in JSON and in the YAML Configs downloads', async ({
  page,
  request,
  builder,
  tracker,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'annotated');
  const { documentId } = await publishTopology(
    request,
    tracker,
    topology,
    labDocument(topology),
  );
  const record = await publishedRecord(request, topology);
  expect(record).toMatchObject({ source: 'store', id: documentId });

  await test.step('the config as JSON holds a map of the digest and id only', async () => {
    const response = await request.get(`${API}/configs/topology/${topology}`, {
      headers: { Accept: 'application/json' },
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    expect(manifestOf(await response.json())).toEqual({
      digest: record.digest,
      id: documentId,
    });
  });

  await test.step('the YAML the Configs page downloads holds the nested map', async () => {
    await openConfigs(page);
    const [file] = await Promise.all([
      page.waitForEvent('download'),
      page
        .getByRole('button', { name: `Download Topology ${topology}` })
        .click(),
    ]);
    expect.soft(file.suggestedFilename()).toBe(`Topology-${topology}.yml`);
    const text = fs.readFileSync(await file.path(), 'utf8');
    // The map's two keys, one level below builder-doc, and nothing else.
    expect(text).toMatch(
      new RegExp(
        `\\n( +)annotations:\\n( +)builder-doc:\\n( +)digest: ${record.digest}\\n\\3id: ${documentId}\\n(?!\\3)`,
      ),
    );
    expect.soft(text).not.toContain('draftId');
  });

  // Before it was a map, the annotation was a JSON string of the whole
  // record. Nothing reads that form: the server refuses the config.
  await test.step('the reference as a string of the record is refused', async () => {
    const name = uniqueName(testInfo, 'annotated-string');
    tracker.config('Topology', name);
    const refused = await request.post(`${API}/configs`, {
      data: {
        apiVersion: 'phenix.sandia.gov/v1',
        kind: 'Topology',
        metadata: {
          name,
          annotations: {
            'builder-doc': JSON.stringify({
              id: documentId,
              digest: record.digest,
              size: record.size,
              chunks: 1,
              chunkSize: 524288,
              schema: 'https://phenix.sandia.gov/schemas/builder/v1',
              draftId: record.draftId,
              snapshotId: record.snapshotId,
              createdAt: record.createdAt,
              createdBy: record.createdBy,
            }),
          },
        },
        spec: { nodes: [] },
      },
    });
    expect(refused.status(), await refused.text()).toBe(400);
    expect.soft(await builder.config('Topology', name)).toBeNull();
  });

  expectNoFatal(issues);
});

// --- topology and experiment ---------------------------------------------------

test('topology and experiment publish carries the VLAN alias, adds the topology to each listed scenario and uses the one picked', async ({
  page,
  builder,
  tracker,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'exp-topo');
  const experiment = uniqueName(testInfo, 'exp-exp');
  const uploaded = uniqueName(testInfo, 'exp-scn');
  const stored = uniqueName(testInfo, 'exp-stored');
  tracker.config('Topology', topology);
  tracker.config('Experiment', experiment);
  // Stored from the Builder, so deleted by name.
  tracker.config('Scenario', uploaded);
  // Seeded before the draft opens: the Scenarios dialog lists it.
  await builder.seedConfig(scenarioConfig(stored, 'builder-e2e-stored'));

  const draft = await openLab(builder, topology);

  await test.step('set the switch VLAN alias in the inspector', async () => {
    await builder.selectNode('EXP');
    await expect(builder.inspector).toContainText('Network EXP');
    const alias = builder.inspector.getByLabel('VLAN alias');
    await alias.fill('101');
    await alias.press('Tab');
    await builder.apply();
    await expect
      .soft(builder.canvasItem('EXP'))
      .toHaveAttribute('aria-label', /VLAN alias 101/);
    await synced(
      builder,
      draft,
      (document) =>
        document.networks.find((n) => n.name === 'EXP')?.alias === 101,
      { soft: true },
    );
  });

  await test.step('a scenario file uploaded in the Builder is stored on the server and listed', async () => {
    const dialog = await openScenarios(builder);
    await expect
      .soft(dialog.getByTestId('scenario-count'))
      .toHaveText('0 scenarios selected.');
    await chooseScenarioFile(
      dialog,
      scenarioYaml(uploaded, 'builder-e2e-upload'),
    );
    // The file names its scenario, which is new to the server.
    await expect
      .soft(dialog.getByTestId('scenario-upload-name'))
      .toHaveValue(uploaded);
    await expect
      .soft(dialog.getByTestId('scenario-upload-hint'))
      .toHaveText('Storing creates a new scenario on the server.');
    await dialog.getByTestId('scenario-upload').click();
    await expect(scenarioBox(dialog, uploaded)).toBeChecked();
    await expect
      .soft(dialog.getByTestId('scenario-status'))
      .toContainText(`Stored scenario ${uploaded} on the server`);
    // Focus goes back to the file field, as the name field is gone.
    await expect.soft(dialog.getByTestId('scenario-file')).toBeFocused();

    const config = await builder.config('Scenario', uploaded);
    expect
      .soft(
        config?.spec?.apps?.map((app) => app.name),
        'stored scenario apps',
      )
      .toEqual(['builder-e2e-upload']);
  });

  await test.step('a stored scenario is added, and the list saved', async () => {
    const dialog = builder.dialog;
    await scenarioBox(dialog, stored).check();
    await expect(scenarioBox(dialog, stored)).toBeChecked();
    await expect
      .soft(dialog.getByTestId('scenario-count'))
      .toHaveText('2 scenarios selected.');
    for (const action of ['View', 'Edit']) {
      await expect
        .soft(
          dialog.getByRole('button', {
            name: `${action} ${stored}`,
            exact: true,
          }),
        )
        .toBeVisible();
    }
    await expectAccessible(page, {
      include: '[data-testid="builder-dialog"]',
      soft: true,
      label: 'Scenarios dialog with two scenarios',
    });
    await saveScenarios(builder, draft, [uploaded, stored]);
  });

  await test.step('the experiment uses the first listed scenario unless another is picked', async () => {
    await openPublish(builder);
    await fillPublish(page, { topology, experiment });
    await expect
      .soft(page.locator('#publish-experiment-hint'))
      .toHaveText('Publishing creates a new experiment.');
    const select = page.getByTestId('publish-scenario-select');
    await expect.soft(select).toHaveValue(uploaded);
    await expect
      .soft(page.getByTestId('publish-scenario-hint'))
      .toHaveText(
        'Publishing adds this topology to the topology annotation of each of ' +
          `the scenarios ${uploaded} and ${stored}. Experiments of the ` +
          'topology can then use it.',
      );
    await expect
      .soft(page.getByLabel('Experiment scenario'))
      .toHaveAccessibleDescription(/Publishing adds this topology/);
    await select.selectOption(stored);
    await expectAccessible(page, {
      include: '[data-testid="builder-dialog"]',
      soft: true,
      label: 'Publish dialog with an experiment scenario',
    });
  });

  // phenix uses "all", in any case, to mean every experiment, so the dialog
  // refuses it, as the server does.
  await test.step('a reserved experiment name is refused before any config is written', async () => {
    const sent = watchPublishes(page);
    const name = page.getByTestId('publish-experiment');
    await name.fill('All');
    await page.getByTestId('publish-submit').click();
    await expect
      .soft(page.getByTestId('publish-error'))
      .toHaveText(
        'The experiment name "All" is reserved: phenix uses it to mean every experiment. Enter another name.',
      );
    await expect.soft(name).toHaveAttribute('aria-invalid', 'true');
    await expect.soft(name).toBeFocused();
    expect.soft(sent, 'publish requests').toEqual([]);
    await expect(page.getByTestId('publish-result')).toHaveCount(0);
    expect.soft(await builder.config('Topology', topology)).toBeNull();
    await name.fill(experiment);
  });

  await test.step('publish adds the topology to both scenarios and creates the experiment', async () => {
    const body = JSON.parse(await expectPublish(page, 200));
    await expect
      .soft(page.getByTestId('publish-result'))
      .toContainText('Every stage succeeded');
    expect.soft(await stageStatuses(page)).toEqual({
      document: 'created',
      topology: 'created',
      scenario: 'updated',
      experiment: 'created',
      draft: 'ok',
    });
    await expect
      .soft(page.getByTestId('publish-result'))
      .toContainText(
        `added topology ${topology} to scenarios ${uploaded}, ${stored}`,
      );
    expect.soft(body.scenario, 'the experiment scenario').toEqual({
      name: stored,
    });
  });

  // The config checks are soft and null-safe, so a missing or wrong scenario
  // config does not hide whether the VLAN alias reached the experiment.
  await test.step('both scenarios name the topology, and keep their apps', async () => {
    for (const [name, app] of [
      [uploaded, 'builder-e2e-upload'],
      [stored, 'builder-e2e-stored'],
    ]) {
      const config = await builder.config('Scenario', name);
      expect
        .soft(config?.metadata?.annotations?.topology, `${name} topology`)
        .toBe(topology);
      expect
        .soft(
          config?.spec?.apps?.map((entry) => entry.name),
          `${name} apps`,
        )
        .toEqual([app]);
    }
  });

  await test.step('the experiment config names the topology, the picked scenario and the VLAN alias', async () => {
    const exp = await builder.config('Experiment', experiment);
    expect.soft(exp, 'published experiment config').toBeTruthy();
    expect
      .soft(exp?.metadata?.annotations, 'experiment annotations')
      .toMatchObject({ topology, scenario: stored });
    expect
      .soft(exp?.spec?.vlans?.aliases, 'experiment VLAN aliases')
      .toMatchObject({ EXP: 101 });

    const manifest = manifestOf(await builder.config('Topology', topology));
    expect
      .soft(Object.keys(manifest || {}).sort(), 'topology builder-doc manifest')
      .toEqual(['digest', 'id']);
    expect
      .soft(
        await publishedRecord(builder.request, topology),
        'published document record',
      )
      .toMatchObject({ id: manifest?.id, draftId: draft.id });
  });

  expectNoFatal(issues);
});

test('a scenario file replaces a stored scenario only once confirmed, and a topology publish adds the topology to it', async ({
  page,
  builder,
  tracker,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'upd-topo');
  const scenario = uniqueName(testInfo, 'upd-scn');
  tracker.config('Topology', topology);
  // Seeded before open: the editor reads the scenario list when it mounts.
  // Its annotation is one a replacement keeps.
  const seeded = scenarioConfig(scenario, 'builder-e2e-seeded');
  seeded.metadata.annotations = { purpose: 'builder-e2e-kept' };
  await builder.seedConfig(seeded);

  const draft = await openLab(builder, topology);
  const apps = async () =>
    (await builder.config('Scenario', scenario))?.spec?.apps?.map(
      (app) => app.name,
    );

  await test.step('a v1 scenario file is refused, on its field', async () => {
    const dialog = await openScenarios(builder);
    const file = dialog.getByTestId('scenario-file');
    await chooseScenarioFile(
      dialog,
      scenarioYaml(scenario, 'builder-e2e-v1').replace(
        V2,
        'phenix.sandia.gov/v1',
      ),
      'scenario-v1.yaml',
    );
    const error = dialog.getByTestId('scenario-error');
    await expect
      .soft(error)
      .toHaveText(
        'This scenario is phenix.sandia.gov/v1. The Builder stores ' +
          `only ${V2} scenarios. Upgrade it to ${V2}. Then upload it again.`,
      );
    await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
    await expect
      .soft(file)
      .toHaveAttribute('aria-describedby', /\bscenario-error\b/);
    await expect
      .soft(dialog.getByTestId('scenario-upload-name'))
      .toHaveCount(0);
  });

  await test.step('a file named as a stored scenario replaces it only once confirmed', async () => {
    const dialog = builder.dialog;
    await chooseScenarioFile(
      dialog,
      scenarioYaml(scenario, 'builder-e2e-replaced'),
    );
    // The file field's error went with the file it was about.
    await expect.soft(dialog.getByTestId('scenario-error')).toHaveCount(0);
    const hint = dialog.getByTestId('scenario-upload-hint');
    // A replacement warns: the words, and a sign on a yellow ground.
    await expect
      .soft(hint)
      .toHaveText(
        `The server has a scenario named ${scenario}. Storing replaces its spec and keeps its annotations.`,
      );
    await expect.soft(hint).toHaveClass(/\bbuilder-hint--warning\b/);

    await dialog.getByTestId('scenario-upload').click();
    const confirm = page.getByRole('alertdialog', {
      name: `Replace scenario ${scenario}?`,
    });
    await expect(confirm).toBeVisible();
    // Focus starts on the button that keeps everything.
    await expect.soft(page.getByTestId('confirm-cancel')).toBeFocused();
    await page.getByTestId('confirm-cancel').click();
    await expect(confirm).toHaveCount(0);
    expect
      .soft(await apps(), 'apps after Cancel')
      .toEqual(['builder-e2e-seeded']);

    await dialog.getByTestId('scenario-upload').click();
    await page.getByTestId('confirm-accept').click();
    await expect(scenarioBox(dialog, scenario)).toBeChecked();
    await expect
      .soft(dialog.getByTestId('scenario-status'))
      .toContainText(`Replaced scenario ${scenario} on the server`);
    expect
      .soft(await apps(), 'apps after the replacement')
      .toEqual(['builder-e2e-replaced']);
    // The file's spec, and the stored scenario's annotations.
    const replaced = await builder.config('Scenario', scenario);
    expect
      .soft(replaced?.metadata?.annotations, 'annotations kept')
      .toMatchObject({ purpose: 'builder-e2e-kept' });
  });

  await test.step('Cancel keeps the list out of the diagram, and Save writes it', async () => {
    await builder.dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(builder.dialog).toHaveCount(0);
    await expect.soft(builder.toolbar('scenario')).toBeFocused();
    expect
      .soft((await builder.serverDocument(draft))?.scenarios)
      .toBeUndefined();

    const dialog = await openScenarios(builder);
    await scenarioBox(dialog, scenario).check();
    await saveScenarios(builder, draft, [scenario]);
    await expect.soft(builder).toHaveAnnounced('Updated scenarios');
  });

  await test.step('a topology publish adds the topology to the listed scenario', async () => {
    await openPublish(builder);
    await fillPublish(page, { topology });
    await expect
      .soft(page.getByTestId('publish-scenario-hint'))
      .toHaveText(
        `Publishing adds this topology to the topology annotation of the scenario ${scenario}. ` +
          'Experiments of the topology can then use it.',
      );
    await expect
      .soft(page.getByTestId('publish-scenario-select'))
      .toHaveCount(0);
    await expectPublish(page, 200);
    expect.soft(await stageStatuses(page)).toMatchObject({
      topology: 'created',
      scenario: 'updated',
    });

    const stored = await builder.config('Scenario', scenario);
    expect.soft(stored?.metadata?.annotations?.topology).toBe(topology);
    expect.soft(await apps()).toEqual(['builder-e2e-replaced']);
  });

  expectNoFatal(issues);
});

test('a stored scenario is listed and published with an experiment, and a draft generated from the experiment lists it', async ({
  page,
  builder,
  tracker,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'stored-topo');
  const experiment = uniqueName(testInfo, 'stored-exp');
  const scenario = uniqueName(testInfo, 'stored-scn');
  tracker.config('Topology', topology);
  tracker.config('Experiment', experiment);
  await builder.seedConfig(scenarioConfig(scenario, 'builder-e2e-stored'));

  const draft = await openLab(builder, topology);
  // Nothing was published from the draft yet: no experiment to open.
  await expect(builder.toolbar('experiment')).toHaveCount(0);

  const dialog = await openScenarios(builder);
  await scenarioBox(dialog, scenario).check();
  await saveScenarios(builder, draft, [scenario]);

  await openPublish(builder);
  await fillPublish(page, { topology, experiment });
  await expect(page.getByTestId('publish-scenario-select')).toHaveValue(
    scenario,
  );
  // The dialog has rendered and the diagram's validation errors are computed
  // synchronously, so the button's state is final: no need to retry.
  expect(
    await page.getByTestId('publish-submit').isEnabled(),
    'Publish is enabled',
  ).toBe(true);
  await expectPublish(page, 200);
  await expect(page.getByTestId('publish-result')).toContainText(
    'Every stage succeeded',
  );

  const exp = await builder.config('Experiment', experiment);
  expect(exp.metadata.annotations).toMatchObject({ topology, scenario });
  const stored = await builder.config('Scenario', scenario);
  expect(stored.metadata.annotations.topology).toContain(topology);

  await test.step('the toolbar then has Exp, named for the experiment the publish made', async () => {
    await closeButton(page).click();
    await expect(builder.dialog).toHaveCount(0);
    await expect(builder.toolbar('experiment')).toHaveAccessibleName(
      `Exp: open experiment ${experiment}`,
    );
  });

  await test.step('a draft generated from the experiment lists the scenario and publishes back to it', async () => {
    // The experiment embeds its own merged copy of the scenario, which the
    // generated draft does not hold: it lists the stored scenario by name.
    const generated = await builder.request.post(`${API}/builder/generate`, {
      data: { source: `experiment/${experiment}` },
    });
    expect(generated.ok(), await generated.text()).toBeTruthy();
    const { document } = await generated.json();
    expect(document.scenarios).toEqual([scenario]);
    expect(document).not.toHaveProperty('scenario');

    const source = await builder.seedDraft(document, {
      sourceToken: `Experiment/${experiment}`,
    });
    const intent = {
      mode: 'topology-experiment',
      topology: { name: topology, action: 'update' },
      scenario: { name: scenario },
      experiment: { name: experiment, action: 'update' },
    };
    const published = await builder.request.post(
      `${draftPath(source)}/publish`,
      { headers: { 'If-Match': source.etag }, data: intent },
    );
    expect(published.status(), await published.text()).toBe(200);
    expect((await published.json()).status).toBe('succeeded');

    // Published again twice, the draft updates the experiment it came from.
    // Memory as a string is decoded to a number, as phenix does.
    let etag = published.headers().etag;
    for (const memory of [4096, '8192']) {
      const edited = await builder.request.post(
        `${draftPath(source)}/snapshots`,
        {
          headers: { 'If-Match': etag },
          data: {
            summary: `Memory ${memory}`,
            document: {
              ...document,
              nodes: document.nodes.map((node) =>
                node.device?.hostname === 'server'
                  ? {
                      ...node,
                      device: {
                        ...node.device,
                        spec: {
                          ...node.device.spec,
                          hardware: { ...node.device.spec.hardware, memory },
                        },
                      },
                    }
                  : node,
              ),
            },
          },
        },
      );
      expect(edited.ok(), await edited.text()).toBeTruthy();
      const again = await builder.request.post(`${draftPath(source)}/publish`, {
        headers: { 'If-Match': edited.headers().etag },
        data: intent,
      });
      expect(again.status(), await again.text()).toBe(200);
      expect(
        (await again.json()).stages.map((stage) => stage.status),
      ).toContain('updated');
      etag = again.headers().etag;

      const updated = await builder.config('Experiment', experiment);
      const server = updated.spec.topology.nodes.find(
        (node) => node.general.hostname === 'server',
      );
      expect(server.hardware.memory).toBe(Number(memory));
    }
  });
});

// Scans the dialog that `selector` finds with axe, in the light theme and
// then the dark one, and goes back to the light theme.
async function scanInBothThemes(page, selector, label) {
  for (const scheme of ['light', 'dark']) {
    // Without motion, so that axe does not read colors in transition.
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' });
    await expect(page.locator('.builder-root')).toHaveAttribute(
      'data-builder-theme',
      scheme,
    );
    await expectAccessible(page, {
      include: selector,
      soft: true,
      label: `${label} (${scheme})`,
    });
  }
  await page.emulateMedia({ colorScheme: 'light', reducedMotion: null });
}

test('the Scenarios dialog shows, edits and creates scenarios', async ({
  page,
  builder,
  tracker,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'scn-dlg');
  const seeded = uniqueName(testInfo, 'scn-dlg-seeded');
  // Create new scenario names it after the diagram, whose name is the
  // topology name that the Publish dialog offers.
  const created = `${topology}-scenario`;
  tracker.config('Scenario', created);
  const config = scenarioConfig(seeded, 'builder-e2e-seeded');
  config.metadata.annotations = { purpose: 'builder-e2e-kept' };
  await builder.seedConfig(config);

  const draft = await openLab(builder, topology);
  const dialog = page.getByRole('dialog', { name: 'Scenarios', exact: true });
  await openScenarios(builder);
  const apps = async (name) =>
    (await builder.config('Scenario', name))?.spec?.apps?.map(
      (app) => app.name,
    );

  await test.step('View shows the stored config read only', async () => {
    await expect(scenarioBox(dialog, seeded)).not.toBeChecked();
    const view = dialog.getByRole('button', {
      name: `View ${seeded}`,
      exact: true,
    });
    await view.click();
    const viewer = page.getByRole('dialog', {
      name: `Scenario ${seeded}`,
      exact: true,
    });
    await expect(viewer).toBeVisible();
    const text = viewer.getByRole('region', { name: 'Scenario config (YAML)' });
    await expect.soft(text).toContainText(`name: ${seeded}`);
    await expect.soft(text).toContainText('name: builder-e2e-seeded');
    await expect.soft(viewer.getByRole('textbox')).toHaveCount(0);
    await scanInBothThemes(
      page,
      '[data-testid="scenario-editor"]',
      'axe on the scenario viewer',
    );

    await viewer.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(viewer).toHaveCount(0);
    await expect.soft(view).toBeFocused();
  });

  await test.step("Edit shows the server's refusal on the field, and Save replaces the scenario", async () => {
    const edit = dialog.getByRole('button', {
      name: `Edit ${seeded}`,
      exact: true,
    });
    await edit.click();
    const editor = page.getByRole('dialog', {
      name: `Edit scenario ${seeded}`,
      exact: true,
    });
    const field = editor.getByLabel('Scenario config (YAML)');
    await expect(field).toBeFocused();
    const text = await field.inputValue();
    // The dialog takes 96% of the window's width, at most 75rem, and its
    // YAML does not scroll sideways.
    const room = await editor.evaluate((element) => ({
      width: element.getBoundingClientRect().width,
      wanted: Math.min(
        window.innerWidth * 0.96,
        75 * parseFloat(getComputedStyle(document.documentElement).fontSize),
      ),
      window: window.innerWidth,
    }));
    expect
      .soft(room.width, 'editor width')
      .toBeGreaterThanOrEqual(room.wanted - 1);
    expect.soft(room.width, 'editor in the window').toBeLessThan(room.window);
    expect
      .soft(
        await field.evaluate(
          (element) => element.scrollWidth <= element.clientWidth,
        ),
        'YAML without sideways scrolling',
      )
      .toBe(true);
    expect.soft(text).toContain('purpose: builder-e2e-kept');
    // The times that the server sets are not for editing.
    expect.soft(text).not.toContain('created:');

    // An app without a name breaks the Scenario schema.
    await field.fill(text.replace('name: builder-e2e-seeded', 'hosts: []'));
    await editor.getByRole('button', { name: 'Save scenario' }).click();
    await expect(editor.getByTestId('scenario-editor-error')).toContainText(
      `Could not save scenario ${seeded}.`,
    );
    await expect.soft(field).toHaveAttribute('aria-invalid', 'true');
    await expect.soft(field).toBeFocused();
    expect
      .soft(await apps(seeded), 'apps after the refusal')
      .toEqual(['builder-e2e-seeded']);

    // Escape asks before it discards the change. Keep editing keeps it.
    await page.keyboard.press('Escape');
    const discard = page.getByRole('alertdialog', {
      name: 'Discard your changes?',
    });
    await discard.getByRole('button', { name: 'Keep editing' }).click();
    await expect(discard).toHaveCount(0);
    await expect(editor).toBeVisible();

    await field.fill(text.replace('builder-e2e-seeded', 'builder-e2e-edited'));
    await scanInBothThemes(
      page,
      '[data-testid="scenario-editor"]',
      'axe on the scenario editor',
    );
    await editor.getByRole('button', { name: 'Save scenario' }).click();
    await expect(editor).toHaveCount(0);
    await expect
      .soft(dialog.getByTestId('scenario-status'))
      .toHaveText(`Saved scenario ${seeded} on the server.`);
    await expect.soft(edit).toBeFocused();
    expect
      .soft(await apps(seeded), 'apps after Save')
      .toEqual(['builder-e2e-edited']);
    expect
      .soft(
        (await builder.config('Scenario', seeded))?.metadata?.annotations,
        'annotations kept',
      )
      .toMatchObject({ purpose: 'builder-e2e-kept' });
  });

  await test.step('Create new scenario opens a config made for this diagram, which Save creates and selects', async () => {
    await dialog.getByRole('button', { name: 'Create new scenario' }).click();
    const creator = page.getByRole('dialog', {
      name: 'Create new scenario',
      exact: true,
    });
    const field = creator.getByLabel('Scenario config (YAML)');
    await expect(field).toHaveValue(
      [
        `apiVersion: ${V2}`,
        'kind: Scenario',
        'metadata:',
        `  name: ${created}`,
        '  annotations:',
        `    topology: ${topology}`,
        'spec:',
        '  apps: []',
        '',
      ].join('\n'),
    );
    // Nothing is stored before Save.
    expect.soft(await builder.config('Scenario', created)).toBeNull();

    await creator.getByRole('button', { name: 'Create scenario' }).click();
    await expect(creator).toHaveCount(0);
    await expect(scenarioBox(dialog, created)).toBeChecked();
    await expect
      .soft(dialog.getByTestId('scenario-status'))
      .toContainText(`Created scenario ${created} on the server`);
    const stored = await builder.config('Scenario', created);
    expect
      .soft(stored?.metadata?.annotations?.topology, 'topology annotation')
      .toBe(topology);
    expect.soft(stored?.spec?.apps ?? [], 'apps').toEqual([]);
  });

  await test.step('the boxes set are saved into the diagram, in the order they were set', async () => {
    await scenarioBox(dialog, seeded).check();
    await expect
      .soft(dialog.getByTestId('scenario-count'))
      .toHaveText('2 scenarios selected.');
    await saveScenarios(builder, draft, [created, seeded]);
  });

  expectNoFatal(issues);
});

test('Enter in the Scenarios filter keeps the dialog open, and Enter in the upload name stores the upload', async ({
  builder,
  tracker,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'scn-enter');
  const base = uniqueName(testInfo, 'scn-enter-seeded');
  const uploaded = uniqueName(testInfo, 'scn-enter-upload');
  tracker.config('Scenario', uploaded);
  // More than eight scenarios show the filter field.
  for (let i = 0; i < 9; i++) {
    await builder.seedConfig(scenarioConfig(`${base}-${i}`, 'builder-e2e'));
  }

  await openLab(builder, topology);
  const dialog = await openScenarios(builder);
  const filter = dialog.getByLabel('Filter scenarios', { exact: true });

  await test.step('Enter in the filter field filters, and does not save or close', async () => {
    await scenarioBox(dialog, `${base}-4`).check();
    await filter.fill(`${base}-4`);
    await filter.press('Enter');
    await expect(dialog).toBeVisible();
    await expect.soft(filter).toBeFocused();
    await expect
      .soft(dialog.getByTestId('scenario-filter-count'))
      .toHaveText(/^Showing 1 of \d+ scenarios\.$/);
    await expect.soft(scenarioBox(dialog, `${base}-4`)).toBeChecked();
  });

  await test.step('Enter in the upload name field stores the upload, and does not close', async () => {
    await chooseScenarioFile(dialog, scenarioYaml(uploaded, 'builder-e2e'));
    const name = dialog.getByTestId('scenario-upload-name');
    await expect(name).toHaveValue(uploaded);
    await name.press('Enter');
    await expect
      .soft(dialog.getByTestId('scenario-status'))
      .toContainText(`Stored scenario ${uploaded} on the server`);
    await expect(dialog).toBeVisible();
    expect.soft(await builder.config('Scenario', uploaded)).toBeTruthy();
  });

  expectNoFatal(issues);
});

// --- refusals and republish -------------------------------------------------------

// A draft updates the topology it published, however often it is
// edited and published again.
test('a draft can publish an update after further edits', async ({
  page,
  request,
  builder,
  tracker,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'republish');

  // The first publish goes through the API, the call the Publish dialog makes,
  // so the editor then mounts with the topology in its source list.
  const { draft } = await publishTopology(
    request,
    tracker,
    topology,
    labDocument(topology),
  );

  await builder.openDraft(draft);

  await test.step('publish again with 3 devices', async () => {
    await builder.palette('template-workstation').click();
    await builder.expectSummary('3 devices');
    await synced(
      builder,
      draft,
      (document) => devicesOf(document).length === 3,
    );

    await openPublish(builder);
    await expect(topologyHint(page)).toHaveText(
      'A topology with this name exists. Publishing updates it.',
    );
    await expect
      .soft(page.getByTestId('publish-submit'))
      .toHaveText('Update topology');
    await expectPublish(page, 200);
    await expect(page.getByTestId('publish-result')).toContainText(
      'Every stage succeeded',
    );
    expect(await stageStatuses(page)).toMatchObject({ topology: 'updated' });

    const config = await builder.config('Topology', topology);
    expect(Object.keys(nodesByHostname(config)).sort()).toEqual([
      'server',
      'server-2',
      'workstation',
    ]);
    await closeButton(page).click();
  });

  await test.step('Inspector changes not applied are published, and ones it cannot apply block Publish and Download', async () => {
    await builder.selectNode('server');
    const memory = builder.inspector
      .locator('legend.group-label', { hasText: /^Hardware$/ })
      .locator('xpath=..')
      .getByLabel('Memory', { exact: true });
    await memory.fill('lots');
    await memory.blur();

    // Publish and Download say what blocks them, rather than leave the
    // changes out.
    const dialog = await openPublish(builder);
    const blocked =
      'Your changes to Device server in the Inspector cannot be published until you fix Memory. Fix or cancel them first.';
    await expect
      .soft(page.getByTestId('publish-unapplied'))
      .toHaveText(blocked);
    await expect.soft(dialog).toHaveAccessibleDescription(blocked);
    await expect.soft(page.getByTestId('publish-submit')).toBeDisabled();
    await page.keyboard.press('Escape');
    await expect(builder.dialog).toHaveCount(0);
    await builder.openDialog('download');
    await expect
      .soft(page.getByTestId('download-unapplied'))
      .toHaveText(/ cannot be downloaded until you fix Memory\. /);
    await expect.soft(page.getByTestId('download-json')).toBeDisabled();
    await expect
      .soft(page.getByTestId('download-topology-yaml'))
      .toBeDisabled();
    await expect.soft(page.getByTestId('download-drawio')).toBeDisabled();
    await expect.soft(page.getByTestId('download-gexf')).toBeDisabled();
    await page.keyboard.press('Escape');
    await expect(builder.dialog).toHaveCount(0);
    // A format's command in the palette opens the dialog on the same
    // block, and downloads nothing.
    await page.keyboard.press('ControlOrMeta+k');
    await page
      .getByRole('combobox', { name: 'Search commands' })
      .fill('download builder json');
    await page.keyboard.press('Enter');
    await expect
      .soft(page.getByTestId('download-unapplied'))
      .toHaveText(/ cannot be downloaded until you fix Memory\. /);
    await expect.soft(builder.dialog.getByRole('status')).toHaveText('');
    await expect.soft(page.getByTestId('download-json')).toBeDisabled();
    await page.keyboard.press('Escape');
    await expect(builder.dialog).toHaveCount(0);

    // Valid, they are saved as one edit before the dialog opens.
    await memory.fill('4096');
    await memory.blur();
    await openPublish(builder);
    await expect.soft(page.getByTestId('publish-unapplied')).toHaveCount(0);
    await expectPublish(page, 200);
    await expect(page.getByTestId('publish-result')).toContainText(
      'Every stage succeeded',
    );
    const config = await builder.config('Topology', topology);
    expect.soft(nodesByHostname(config).server?.hardware?.memory).toBe(4096);
    await closeButton(page).click();
    await expect
      .soft(builder.inspector.getByTestId('inspector-apply'))
      .toHaveCount(0);
    const history = await builder.openDialog('history');
    await expect
      .soft(
        history
          .getByTestId('history-row')
          .filter({ hasText: 'Saved unapplied changes to Device server' })
          .getByTestId('history-automatic'),
      )
      .toBeVisible();
    await page.keyboard.press('Escape');
    await expect(builder.dialog).toHaveCount(0);
  });

  await test.step('a topology someone else changed since is not overwritten', async () => {
    const theirs = await builder.config('Topology', topology);
    theirs.spec.nodes[0].general.description = 'their change';
    const changed = await request.put(`${API}/configs/Topology/${topology}`, {
      data: theirs,
    });
    expect(changed.ok(), await changed.text()).toBeTruthy();

    await builder.palette('template-workstation').click();
    await builder.expectSummary('4 devices');
    await synced(
      builder,
      draft,
      (document) => devicesOf(document).length === 4,
    );

    await openPublish(builder);
    await expectPublish(page, 409);

    const refusal =
      `The topology "${topology}" changed after this diagram published it. Publishing would overwrite that change. ` +
      'Import the topology again to edit it as it is now, or enter another name to create a new topology.';
    const name = page.getByTestId('publish-name');
    const error = page.getByTestId('publish-error');
    await expect
      .soft(error)
      .toHaveText(`Could not publish the diagram. ${refusal}`);
    await expect.soft(name).toHaveAttribute('aria-invalid', 'true');
    await expect.soft(name).toBeFocused();
    // From then on the form says so, and offers only to create.
    await expect
      .soft(topologyHint(page))
      .toHaveText(
        'A topology with this name changed after this diagram published it. Publishing would overwrite that change. ' +
          'Import the topology again to edit it as it is now, or enter another name to create a new topology.',
      );
    await expect
      .soft(page.getByTestId('publish-submit'))
      .toHaveText('Create topology');

    const sent = watchPublishes(page);
    await page.getByTestId('publish-submit').click();
    await expect.soft(error).toHaveText(refusal);
    expect.soft(sent, 'publish requests').toEqual([]);

    const kept = await builder.config('Topology', topology);
    expect(kept.spec.nodes[0].general.description).toBe('their change');
  });
});

// --- disconnected interfaces ----------------------------------------------------

// An interface with no VLAN is a warning in the draft, but publishing refuses
// it (minimega would only at start), as it does an address two devices use.
test('an interface with no VLAN or a used address is refused at publish, and its VLAN connects it', async ({
  page,
  builder,
  tracker,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'disconnected');
  tracker.config('Topology', topology);

  const draft = await openLab(builder, topology);
  // The message of each error, which its item shows with the element it is
  // about and Go to.
  const errors = builder.dialog
    .locator('li[data-level="error"]')
    .getByTestId('issue-message');
  const vlan = builder.inspector
    .locator('[data-path="spec.network.interfaces.0.vlan"]')
    .getByRole('textbox');
  // The device whose eth0 is disconnected.
  let disconnected;

  await test.step('a disconnected interface is refused in the dialog and by the server', async () => {
    // Delete on a canvas connection removes it and keeps the interface.
    await page.locator('g.vue-flow__edge').first().focus();
    await page.keyboard.press('Delete');
    await builder.expectSummary('1 connection');
    await synced(builder, draft, (document) => document.edges?.length === 1);

    const document = await builder.serverDocument(draft);
    disconnected = devicesOf(document).find(
      (node) => node.device.spec.network.interfaces[0].vlan === '',
    )?.device.hostname;
    expect(disconnected, 'the device whose eth0 was disconnected').toBeTruthy();

    // The diagram checks say the warning stops publishing too.
    await page.getByTestId('builder-checks').click();
    const checks = page.getByTestId('checks-dialog');
    await expect(checks.getByTestId('checks-summary')).toContainText(
      'You must fix the warning before you can publish the diagram. Publish lists that warning as an error.',
    );
    await page.keyboard.press('Escape');
    await expect(checks).toHaveCount(0);

    await openPublish(builder);
    await expect(errors).toHaveText([
      `Error: interface "eth0" of "${disconnected}" is not connected to a network and has no VLAN. Thus you cannot publish the interface. Connect it, or type a VLAN for it`,
    ]);
    await expect(page.getByTestId('publish-submit')).toBeDisabled();
    await expect
      .soft(builder.dialog)
      .toContainText('are ready to publish after you fix the errors below.');

    // The refused dry run is not listed again under What publishing changes,
    // which says the errors under Checks block publishing.
    const preview = builder.dialog.getByTestId('publish-preview');
    await expect(preview.getByTestId('publish-preview-status')).toHaveText(
      'What publishing changes: the server would refuse to publish.',
    );
    await expect(preview).toHaveAttribute('aria-busy', 'false');
    await expect(preview.getByTestId('publish-preview-checks')).toHaveText(
      'The errors listed under Checks block publishing.',
    );
    await expect(preview.getByTestId('issue')).toHaveCount(0);
    await expect(errors).toHaveCount(1);

    // Nothing is published (the Go tests check the server's 422 refusal).
    expect(await builder.config('Topology', topology)).toBeNull();

    await builder.dialog.getByRole('button', { name: 'Cancel' }).click();
  });

  // The network goes with its last switch, as nothing else uses it then.
  await test.step('deleting the last switch removes its network, and one Undo restores both', async () => {
    await builder.selectNode('EXP');
    await page.keyboard.press('Delete');
    await expect(builder).toHaveAnnounced(
      'Deleted EXP, 1 connection and network EXP',
    );
    await builder.expectSummary('0 switches, 0 networks, 0 connections');

    await builder.toolbar('undo').click();
    await builder.expectSummary('1 switch, 1 network, 1 connection');
    await builder.toolbar('redo').click();
    await builder.expectSummary('0 switches, 0 networks, 0 connections');
    await builder.toolbar('undo').click();
    await builder.expectSummary('1 switch, 1 network, 1 connection');
  });

  await test.step('a VLAN naming a network connects the interface to its switch', async () => {
    await builder.selectNode(disconnected);
    await vlan.fill('exp');
    await vlan.press('Enter');
    await expect(builder).toHaveAnnounced(
      `Updated device ${disconnected} and connected eth0 to network EXP`,
    );
    await builder.expectSummary('1 switch, 1 network, 2 connections');
    await expect.soft(vlan).toHaveValue('EXP');
  });

  await test.step('publishes once every interface has a VLAN', async () => {
    await synced(builder, draft, (document) => document.edges?.length === 2);
    await openPublish(builder);
    await expect(errors).toHaveCount(0);
    await expectPublish(page, 200);

    const written = await builder.config('Topology', topology);
    expect(
      written.spec.nodes.flatMap((node) =>
        node.network.interfaces.map((iface) => iface.vlan),
      ),
    ).toEqual(['EXP', 'EXP']);
  });

  // An interface with no connection point (from an import) is checked too.
  // VLAN names match exactly, so "exp" is not network EXP.
  await test.step('an interface with no connection point is checked too', async () => {
    const other = uniqueName(testInfo, 'handleless');
    tracker.config('Topology', other);
    const document = labDocument(other);
    const [server, server2] = devicesOf(document);

    server.device.spec.network.interfaces.push({
      name: '',
      proto: 'dhcp',
      type: 'ethernet',
    });
    server2.device.interfaces.push({
      id: crypto.randomUUID(),
      name: 'eth1',
      index: 1,
    });
    server2.device.spec.network.interfaces.push({
      name: 'eth1',
      proto: 'dhcp',
      type: 'ethernet',
      vlan: 'exp',
    });

    const seeded = await builder.seedDraft(document);
    await builder.openDraft(seeded);
    await openPublish(builder);
    await expect(errors).toHaveText([
      'Error: interface #2 of "server" has no name and no VLAN. Thus you cannot publish the interface. Name it. Then connect it, or type a VLAN for it',
    ]);
    await expect(page.getByTestId('publish-submit')).toBeDisabled();
    await expect
      .soft(builder.dialog.locator('li[data-level="warning"]'))
      .toContainText([
        'interface "eth1" of "server-2" is not connected on the canvas, and its VLAN "exp" differs from network EXP only in case. phenix treats them as different VLANs. Thus publishing does not put the interface on network EXP',
      ]);

    expect(await builder.config('Topology', other)).toBeNull();
  });

  // One address on two interfaces is a warning on both devices, which
  // publishing refuses until one changes.
  await test.step('an IP address two devices use is refused until one changes', async () => {
    const shared = uniqueName(testInfo, 'shared-ip');
    tracker.config('Topology', shared);
    const document = labDocument(shared);

    for (const device of devicesOf(document)) {
      Object.assign(device.device.spec.network.interfaces[0], {
        proto: 'static',
        address: '10.0.0.5',
        mask: 24,
      });
    }

    const seeded = await builder.seedDraft(document);
    await builder.openDraft(seeded);
    const uses = (hostname, other) =>
      `IP address 10.0.0.5 of interface "eth0" of "${hostname}" on VLAN "EXP" is also the address of interface "eth0" of "${other}"`;

    await page.getByTestId('builder-checks').click();
    const checks = page.getByTestId('checks-dialog');
    await expect(checks).toContainText(uses('server', 'server-2'));
    await expect(checks).toContainText(uses('server-2', 'server'));
    await page.keyboard.press('Escape');
    await expect(checks).toHaveCount(0);

    await openPublish(builder);
    await expect(errors).toHaveText([
      `Error: ${uses('server', 'server-2')}`,
      `Error: ${uses('server-2', 'server')}`,
    ]);
    await expect(page.getByTestId('publish-submit')).toBeDisabled();

    const usedBy =
      'IP address 10.0.0.5 on VLAN "EXP" is used by interface "eth0" of device "server" and interface "eth0" of device "server-2"';
    await builder.dialog.getByRole('button', { name: 'Cancel' }).click();

    // Topology YAML still downloads, and says why Publish refuses it, as
    // Publish's refusal says it.
    await builder.openDialog('download');
    await builder.dialog.getByTestId('download-topology-yaml').click();
    await expect(builder.dialog.getByRole('status')).toContainText(
      `You cannot publish this topology yet. ${usedBy}.`,
    );
    await page.keyboard.press('Escape');
    await expect(builder.dialog).toHaveCount(0);

    // The Inspector shows the warning at the address field.
    await builder.selectNode('server-2');
    const field = builder.inspector.locator(
      '[data-path="spec.network.interfaces.0.address"]',
    );
    await expect(field.getByTestId('inspector-field-warning')).toHaveText(
      'Warning: This IP address on VLAN "EXP" is also the address of interface "eth0" of "server".',
    );
    await field.getByRole('textbox').fill('10.0.0.6');
    await field.getByRole('textbox').press('Enter');
    await expect(builder).toHaveAnnounced('Updated device server-2');
    await expect(field.getByTestId('inspector-field-warning')).toHaveCount(0);
    await synced(
      builder,
      seeded,
      (saved) =>
        devicesOf(saved)[1].device.spec.network.interfaces[0].address ===
        '10.0.0.6',
    );

    await openPublish(builder);
    await expect(errors).toHaveCount(0);
    await expectPublish(page, 200);
  });

  // phenix brings a manual interface up with no address, and the Inspector
  // does not show one, so an address it still holds is not compared.
  await test.step('an address two manual interfaces hold is not compared', async () => {
    const manual = uniqueName(testInfo, 'manual-ip');
    tracker.config('Topology', manual);
    const document = labDocument(manual);

    for (const device of devicesOf(document)) {
      Object.assign(device.device.spec.network.interfaces[0], {
        proto: 'manual',
        address: '10.0.0.5',
        mask: 24,
      });
    }

    await builder.openDraft(await builder.seedDraft(document));
    await builder.selectNode('server-2');
    await expect(vlan).toHaveValue('EXP');
    await expect(
      builder.inspector.locator(
        '[data-path="spec.network.interfaces.0.address"]',
      ),
    ).toHaveCount(0);
    await expect(
      builder.inspector
        .getByTestId('inspector-field-warning')
        .filter({ hasText: 'IP address' }),
    ).toHaveCount(0);

    await openPublish(builder);
    await expect(errors).toHaveCount(0);
    await expectPublish(page, 200);
  });
});

// --- failures -------------------------------------------------------------------

test('a partial publication lists the failed stage, says how to finish it and lets the user go back', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'partial');
  const experiment = uniqueName(testInfo, 'partial-exp');

  // The publish response is mocked, so the diagram's content does not matter.
  await builder.open();
  await builder.createBlank();

  // The server's partial-failure answer (writePublishPartial), so no config is
  // written. The dialog's dry runs still reach the server.
  await page.route('**/builder/drafts/*/publish', (route) =>
    isPublishPreview(route.request())
      ? route.continue()
      : route.fulfill({
          status: 500,
          contentType: 'application/json',
          body: JSON.stringify({
            status: 'partial',
            stages: [
              {
                name: 'document',
                status: 'created',
                message: 'phenix stored the immutable Builder document',
              },
              {
                name: 'topology',
                status: 'created',
                config: `Topology/${topology}`,
              },
              {
                name: 'experiment',
                status: 'failed',
                message: 'experiment publication failed',
              },
            ],
            warnings: [
              {
                code: 'publish.retry.available',
                severity: 'warning',
                message: RETRY_ADVICE,
              },
            ],
            errors: ['experiment publication failed'],
          }),
        }),
  );

  await openPublish(builder);
  // A blank diagram is "Untitled topology", numbered after the first. The
  // offered topology name is a form of it the server accepts.
  await expect
    .soft(page.getByTestId('publish-name'))
    .toHaveValue(/^Untitled-topology(-\d+)?$/);
  await fillPublish(page, { topology, experiment });
  await expect
    .soft(page.getByTestId('publish-submit'))
    .toHaveText('Create topology and experiment');
  await expectPublish(page, 500);

  // The result replaces the form, and focus moves to its summary.
  const result = page.getByTestId('publish-result');
  const summary = result.getByTestId('publish-summary');
  // The summary says how to finish, and the issues do not repeat it.
  await expect(summary).toHaveText(
    `Published with failures. Publish wrote some configs. The list below shows the failed stages. ${RETRY_ADVICE}`,
  );
  await expect.soft(summary).toBeFocused();
  expect(await stageStatuses(page)).toEqual({
    document: 'created',
    topology: 'created',
    experiment: 'failed',
  });
  await expect(result.locator('li[data-status="failed"]')).toHaveAttribute(
    'data-level',
    'error',
  );
  await expect(
    result.locator('li[data-status="created"]').first(),
  ).toHaveAttribute('data-level', 'info');
  await expect(
    result.locator('li[data-level="error"]:not([data-status])'),
  ).toHaveText('Error: experiment publication failed');
  // Under a heading that counts the errors. It names no element of the
  // diagram, so it has no Go to.
  const reported = result.getByTestId('publish-result-issues');
  await expect
    .soft(reported.getByRole('heading', { level: 3 }))
    .toHaveText(['1 error']);
  await expect.soft(reported.getByTestId('issue-go-to')).toHaveCount(0);
  // The dialog reports the failure. The page's own alert, behind the modal,
  // would only repeat it.
  await expect.soft(page.getByTestId('builder-error')).toBeHidden();
  // The page's live region is unreadable behind the modal. Its message
  // waits until the dialog closes.
  await expect
    .soft(builder)
    .not.toHaveAnnounced(
      `Publish finished with failures. It wrote some configs. ${RETRY_ADVICE}`,
    );
  await expectAccessible(page, { include: '[data-testid="builder-dialog"]' });

  await page.getByTestId('publish-retry').press('Enter');
  await expect(result).toHaveCount(0);
  await expect(page.getByTestId('publish-name')).toHaveValue(topology);
  await expect(page.getByTestId('publish-experiment')).toHaveValue(experiment);
  await expect(page.getByTestId('publish-submit')).toBeEnabled();
  // Back replaces the result, and the button that had focus, with the form.
  await expect.soft(page.getByTestId('publish-submit')).toBeFocused();
  expectNoFatal(issues);
});

// --- lists of checks and Go to ----------------------------------------------------

// labDocument with hostnames the checks flag: "all" (second node), an error
// at publish, and "phenix", a plain warning.
function flaggedLab(name) {
  const document = labDocument(name);
  const [server, server2] = devicesOf(document);

  for (const [device, hostname] of [
    [server, 'phenix'],
    [server2, 'all'],
  ]) {
    device.label = hostname;
    device.device.hostname = hostname;
    device.device.spec.general.hostname = hostname;
  }

  return document;
}

// The server's disk images include the devices' drive image, so no drive
// raises a warning of its own.
async function listDriveImages(page) {
  await page.route('**/api/v1/disks', (route) =>
    route.fulfill({ json: { disks: [{ kind: 'VM', name: 'ubuntu.qc2' }] } }),
  );
}

test('the Publish and Checks dialogs list errors before warnings, and Go to focuses the field an issue names', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  await listDriveImages(page);
  const draft = await builder.seedDraft(
    flaggedLab(uniqueName(testInfo, 'go-to')),
  );
  await builder.openDraft(draft);
  const hostname = builder.inspector
    .locator('[data-path="hostname"]')
    .getByRole('textbox');

  await test.step('Publish lists the error that blocks publishing, then the warning', async () => {
    const dialog = await openPublish(builder);
    const checks = dialog.getByTestId('publish-checks');
    const errors = checks.getByTestId('publish-checks-error');
    const warnings = checks.getByTestId('publish-checks-warning');

    await expect(errors.getByRole('heading', { level: 4 })).toHaveText(
      '1 error blocks publishing',
    );
    await expect(warnings.getByRole('heading', { level: 4 })).toHaveText(
      '1 warning',
    );
    expect(
      await checks
        .locator('section[data-severity]')
        .evaluateAll((groups) => groups.map((group) => group.dataset.severity)),
    ).toEqual(['error', 'warning']);
    await expect(errors.getByTestId('issue-message')).toHaveText([
      /^Error: hostname "all" is reserved, so the device cannot be published: /,
    ]);
    await expect(errors.getByTestId('issue-element')).toHaveText([
      'Device all',
    ]);
    await expect(warnings.getByTestId('issue-message')).toHaveText([
      /^Warning: hostname "phenix" matches "phenix"/,
    ]);
    await expect(warnings.getByTestId('issue-element')).toHaveText([
      'Device phenix',
    ]);
    await expect.soft(page.getByTestId('publish-submit')).toBeDisabled();

    // Named by the element and the message. A button, so Tab reaches it.
    const goTo = errors.getByTestId('issue-go-to');
    await expect(goTo).toHaveAccessibleName(
      /^Go to device all: hostname "all" is reserved/,
    );
    await expect.soft(goTo).toHaveJSProperty('tabIndex', 0);
    await goTo.press('Enter');
    await expect(builder.dialog).toHaveCount(0);
    await expect(builder.canvasItem('all')).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    await expect(hostname).toBeFocused();
    await expect(hostname).toHaveValue('all');
    await expect(builder).toHaveAnnounced('Hostname in device all');
  });

  await test.step('the Checks dialog lists both as warnings, in diagram order, and Go to focuses the field', async () => {
    await page.getByTestId('builder-checks').click();
    const checks = page.getByTestId('checks-dialog');
    const list = checks.getByTestId('checks-issues');

    await expect(list.getByTestId('checks-issues-error')).toHaveCount(0);
    const warnings = list.getByTestId('checks-issues-warning');
    await expect(warnings.getByRole('heading', { level: 3 })).toHaveText(
      '2 warnings',
    );
    await expect(warnings.getByTestId('issue-element')).toHaveText([
      'Device phenix',
      'Device all',
    ]);
    await expect
      .soft(checks.getByTestId('checks-summary'))
      .toContainText('Go to selects the node or connection an issue is about.');

    await warnings
      .getByRole('button', { name: /^Go to device phenix: / })
      .click();
    await expect(checks).toHaveCount(0);
    await expect(builder.canvasItem('phenix')).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    await expect(hostname).toBeFocused();
    await expect(hostname).toHaveValue('phenix');
    await expect(builder).toHaveAnnounced('Hostname in device phenix');
  });

  await test.step('the Inspector’s checks of the selection go to the field too', async () => {
    await builder.selectNode('all');
    const goTo = builder.inspector
      .getByTestId('inspector-checks')
      .getByTestId('inspector-check-go-to');

    await expect(goTo).toHaveCount(1);
    await expect(goTo).toHaveAccessibleName(
      /^Go to Hostname: hostname "all" is reserved/,
    );
    await goTo.click();
    await expect(hostname).toBeFocused();
    await expect(builder).toHaveAnnounced('Hostname in device all');
  });

  expectNoFatal(issues);
});

// A served refusal that lists issues is listed as the checks are, with Go to
// for the node an issue names. Nothing is written.
test('a refused publish lists the issues the server names, with Go to their node', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'refused');
  const document = labDocument(topology);
  const [, server2] = devicesOf(document);
  // A server that reserves the hostname, refusing it as phenix refuses the
  // hostname "all", with the code of that refusal.
  const reason =
    'hostname "server-2" is reserved on this server, so the device cannot be published';

  await listDriveImages(page);
  await builder.openDraft(await builder.seedDraft(document));
  await builder.expectSummary('2 connections');
  // The dialog's dry runs, which write nothing, still reach the server.
  await page.route('**/builder/drafts/*/publish', (route) =>
    isPublishPreview(route.request())
      ? route.continue()
      : route.fulfill({
          status: 422,
          contentType: 'application/json',
          body: JSON.stringify({
            message: `phenix cannot publish topology ${topology}: ${reason}`,
            code: 'publish.blocked',
            errors: [
              {
                code: 'node.hostname.reserved',
                severity: 'error',
                message: reason,
                nodeId: server2.id,
                field: 'hostname',
              },
            ],
            warnings: [
              {
                code: 'publish.file.unchanged',
                severity: 'warning',
                message: 'the Builder file of the topology is not changed',
              },
            ],
          }),
        }),
  );

  const dialog = await openPublish(builder);
  await fillPublish(page, { topology });
  await expectPublish(page, 422);

  // The refusal's code is about the topology, so the dialog marks the
  // topology name field as well as listing the issues.
  await expect(page.getByTestId('publish-error')).toContainText(reason);
  await expect
    .soft(page.getByTestId('publish-name'))
    .toHaveAttribute('aria-invalid', 'true');

  const refusal = dialog.getByTestId('publish-refusal');
  await expect(
    refusal.getByTestId('publish-refusal-error').getByRole('heading'),
  ).toHaveText('1 error blocks publishing');
  await expect(
    refusal.getByTestId('publish-refusal-warning').getByRole('heading'),
  ).toHaveText('1 warning');
  await expect(refusal.getByTestId('issue-code')).toHaveText([
    'node.hostname.reserved',
    'publish.file.unchanged',
  ]);
  await expect(refusal.getByTestId('issue-element')).toHaveText([
    'Device server-2',
  ]);
  // The warning names no element of the diagram: it has no Go to.
  await expect(
    refusal.getByTestId('publish-refusal-warning').getByTestId('issue-go-to'),
  ).toHaveCount(0);
  await expectAccessible(page, {
    include: '[data-testid="builder-dialog"]',
    soft: true,
    label: 'Publish dialog listing a refusal',
  });

  await refusal
    .getByRole('button', { name: `Go to device server-2: ${reason}` })
    .click();
  await expect(builder.dialog).toHaveCount(0);
  await expect(builder.canvasItem('server-2')).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  const hostname = builder.inspector
    .locator('[data-path="hostname"]')
    .getByRole('textbox');
  await expect(hostname).toBeFocused();
  await expect(hostname).toHaveValue('server-2');
  expect(await builder.config('Topology', topology)).toBeNull();
  expectNoFatal(issues);
});

// The preflight report as the dialog says it once the checks are done.
function preflightOutcome(report) {
  const parts = ['passed', 'failed', 'unavailable'].flatMap((status) => {
    const n = report.checks.filter((check) => check.status === status).length;

    return n ? [`${n} ${status}`] : [];
  });

  return `Preflight: ${parts.join(', ')}`;
}

// Ticks the preflight checks given in the open Checks dialog, runs them and
// returns the report the server answered with.
async function runPreflight(page, labels) {
  const preflight = page.getByTestId('preflight');

  for (const label of labels) {
    await preflight.getByRole('checkbox', { name: label }).check();
  }

  const answered = page.waitForResponse(
    (candidate) =>
      candidate.request().method() === 'POST' &&
      /\/builder\/drafts\/[^/]+\/preflight$/.test(
        new URL(candidate.url()).pathname,
      ),
  );
  await preflight.getByTestId('preflight-run').click();
  const response = await answered;
  const body = await response.text();
  expect(response.status(), body).toBe(200);

  return JSON.parse(body);
}

test('the Checks dialog runs the preflight checks ticked and reports each one once', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const draft = await openLab(builder, uniqueName(testInfo, 'preflight'));

  await page.getByTestId('builder-checks').click();
  const preflight = page.getByTestId('checks-dialog').getByTestId('preflight');
  const run = preflight.getByTestId('preflight-run');
  const results = preflight.getByTestId('preflight-result');

  await test.step('no check is ticked at first, and Run says why it does nothing', async () => {
    for (const id of ['capacity', 'network', 'disks', 'apps']) {
      await expect(
        preflight.getByTestId(`preflight-check-${id}`),
      ).not.toBeChecked();
    }
    await expect(run).toHaveAttribute('aria-disabled', 'true');
    await expect(run).toHaveAccessibleDescription(
      'Select at least one check to run.',
    );
    // Playwright waits for an aria-disabled button to be enabled.
    await run.click({ force: true });
    await expect(results).toHaveCount(0);
  });

  const before = await builder.request.get(draftPath(draft));
  const report = await runPreflight(page, ['Disk images', 'Scenario apps']);

  await test.step('each check ticked is reported once, with its status and a summary', async () => {
    expect(report.checks.map((check) => check.name)).toEqual(['disks', 'apps']);
    expect(
      [...report.passed, ...report.failed, ...report.unavailable].sort(),
    ).toEqual(['apps', 'disks']);

    await expect(results).toHaveCount(2);
    await expect(results.getByRole('heading', { level: 4 })).toHaveText([
      /^Disk images: (Passed|Failed|Unavailable)$/,
      'Scenario apps: Passed',
    ]);
    await expect(results.nth(1).getByTestId('preflight-summary')).toHaveText(
      'The diagram lists no scenarios, so it needs no apps.',
    );

    // Without minimega the server lists no disk images, and the check says
    // so in an issue of its own: whatever its status, each issue is listed.
    const [disks] = report.checks;
    expect(disks.summary).not.toBe('');
    await expect(results.first().getByTestId('preflight-summary')).toHaveText(
      disks.summary,
    );
    await expect(results.first().getByTestId('issue-code')).toHaveCount(
      disks.issues.length,
    );
    expect(disks.status === 'passed' || disks.issues.length > 0).toBe(true);

    // Said in the dialog's own status region: the page behind is inert.
    await expect(preflight).toHaveAttribute('aria-busy', 'false');
    const outcome = preflight.getByTestId('preflight-outcome');
    await expect(outcome).toHaveAttribute('role', 'status');
    await expect(outcome).toHaveText(preflightOutcome(report));
  });

  await test.step('nothing is written, and an unknown check is refused', async () => {
    const after = await builder.request.get(draftPath(draft));
    expect(after.headers().etag).toBe(before.headers().etag);

    const refused = await builder.request.post(
      `${draftPath(draft)}/preflight`,
      {
        data: { checks: ['everything'] },
      },
    );
    expect(refused.status(), await refused.text()).toBe(400);
    expect((await refused.json()).code).toBe('request.invalid');
  });

  await test.step('the report and the choice stay when the dialog opens again, until the diagram changes', async () => {
    await page.keyboard.press('Escape');
    await page.getByTestId('builder-checks').click();
    await expect(preflight.getByTestId('preflight-check-disks')).toBeChecked();
    await expect(preflight.getByTestId('preflight-check-apps')).toBeChecked();
    await expect(results).toHaveCount(2);
    await page.keyboard.press('Escape');

    await builder.palette('switch').click();
    await page.getByTestId('builder-checks').click();
    await expect(results).toHaveCount(0);
    await expect(preflight.getByTestId('preflight-check-disks')).toBeChecked();
    await page.keyboard.press('Escape');
  });

  expectNoFatal(issues);
});

test(
  'axe finds no serious violations in the Checks dialog with a preflight report',
  { tag: ['@axe'] },
  async ({ page, builder, issues }, testInfo) => {
    const draft = await builder.seedDraft(
      labDocument(uniqueName(testInfo, 'axe-preflight')),
    );
    await builder.openDraft(draft);

    await page.getByTestId('builder-checks').click();
    await runPreflight(page, [
      'Host capacity',
      'Networks',
      'Disk images',
      'Scenario apps',
    ]);

    for (const scheme of ['light', 'dark']) {
      await test.step(`the ${scheme} theme`, async () => {
        await page.emulateMedia({ colorScheme: scheme });
        await expect(page.getByTestId('preflight-result')).toHaveCount(4);
        await expectAccessible(page, {
          include: '[data-testid="checks-dialog"]',
          soft: true,
          label: `axe on the Checks dialog's preflight report (${scheme})`,
        });
      });
    }

    await page.keyboard.press('Escape');
    await expect(builder.dialog).toHaveCount(0);
    expectNoFatal(issues);
  },
);

test(
  'axe finds no serious violations in the Publish and Checks dialogs listing errors and warnings',
  { tag: ['@axe'] },
  async ({ page, builder, issues }, testInfo) => {
    await listDriveImages(page);
    const draft = await builder.seedDraft(
      flaggedLab(uniqueName(testInfo, 'axe-checks')),
    );

    for (const scheme of ['light', 'dark']) {
      await test.step(`the ${scheme} theme`, async () => {
        await page.emulateMedia({ colorScheme: scheme });
        await builder.openDraft(draft);

        await openPublish(builder);
        await expect(
          builder.dialog.getByTestId('publish-checks-error'),
        ).toBeVisible();
        await expect(
          builder.dialog.getByTestId('publish-checks-warning'),
        ).toBeVisible();
        await expectAccessible(page, {
          include: '[data-testid="builder-dialog"]',
          soft: true,
          label: `axe on the Publish dialog's checks (${scheme})`,
        });
        await page.keyboard.press('Escape');
        await expect(builder.dialog).toHaveCount(0);

        await page.getByTestId('builder-checks').click();
        await expect(
          builder.dialog.getByTestId('checks-issues-warning'),
        ).toBeVisible();
        // The Checks dialog's test id replaces the shared builder-dialog.
        await expectAccessible(page, {
          include: '[data-testid="checks-dialog"]',
          soft: true,
          label: `axe on the Checks dialog (${scheme})`,
        });
        await page.keyboard.press('Escape');
        await expect(builder.dialog).toHaveCount(0);
      });
    }

    expectNoFatal(issues);
  },
);

// --- what publishing changes ------------------------------------------------------

// The lines of one list of the Publish dialog's What publishing changes:
// configs, includes, scenarios, images or aliases.
function previewLines(page, list) {
  return page.getByTestId(`publish-preview-${list}`).getByRole('listitem');
}

// A topology another one includes, with one device, `hostname`.
function includedTopology(name, hostname) {
  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: { name },
    spec: {
      nodes: [
        {
          type: 'VirtualMachine',
          general: { hostname, vm_type: 'kvm' },
          hardware: { os_type: 'linux', drives: [{ image: 'centos.qc2' }] },
          network: {
            interfaces: [
              { name: 'eth0', proto: 'dhcp', type: 'ethernet', vlan: 'EXP' },
            ],
          },
        },
      ],
    },
  };
}

// labDocument(name), including the topology `included` and listing the
// scenario `scenario`.
function previewLab(name, { included, scenario }) {
  const document = labDocument(name);

  if (included) {
    document.source = { kind: 'manual', includeTopologies: [included] };
  }

  if (scenario) {
    document.scenarios = [scenario];
  }

  return document;
}

test('the Publish dialog shows what publishing changes, follows the form, and writes nothing until Publish', async ({
  page,
  builder,
  tracker,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'preview');
  const renamed = uniqueName(testInfo, 'preview-b');
  const experiment = uniqueName(testInfo, 'preview-exp');
  const included = uniqueName(testInfo, 'preview-inc');
  const scenario = uniqueName(testInfo, 'preview-scn');
  tracker.config('Topology', topology);
  await builder.seedConfig(includedTopology(included, 'preview-inc-host'));
  await builder.seedConfig(scenarioConfig(scenario, 'builder-e2e-preview'));

  await builder.openDraft(
    await builder.seedDraft(previewLab(topology, { included, scenario })),
  );
  await builder.expectSummary('2 connections');

  const dialog = await openPublish(builder);
  await expect(
    dialog.getByRole('heading', { name: 'What publishing changes' }),
  ).toBeVisible();
  await expect(previewLines(page, 'configs')).toHaveText([
    `Creates Topology config ${topology}`,
  ]);
  await expect(previewLines(page, 'includes')).toHaveText([
    `Adds included topology ${included}`,
  ]);
  await expect(previewLines(page, 'scenarios')).toHaveText([
    `Adds topology ${topology} to Scenario ${scenario}`,
  ]);
  // Whether the server has the image depends on its minimega, which the
  // line says only when the server's images can be read.
  await expect(previewLines(page, 'images')).toHaveText([
    /^Disk image ubuntu\.qc2 is new \(server and server-2 use it\)/,
  ]);
  await expect(dialog.getByTestId('publish-preview')).toHaveAttribute(
    'aria-busy',
    'false',
  );
  // The preview never holds Publish back.
  await expect(page.getByTestId('publish-submit')).toBeEnabled();

  await test.step('the preview follows the names and the mode', async () => {
    // From the change on, the lines shown are marked as being read again,
    // through the pause before the server is asked.
    await page.getByTestId('publish-name').fill(renamed);
    await expect(dialog.getByTestId('publish-preview')).toHaveAttribute(
      'aria-busy',
      'true',
    );

    await fillPublish(page, { topology: renamed, experiment });
    await expect(previewLines(page, 'configs')).toHaveText([
      `Creates Topology config ${renamed}`,
      `Creates Experiment config ${experiment}`,
    ]);
    await expect(previewLines(page, 'scenarios')).toHaveText([
      `Adds topology ${renamed} to Scenario ${scenario}`,
    ]);

    // A name the server would refuse is not sent: the preview says why.
    await page.getByTestId('publish-name').fill('not a name');
    await expect(page.getByTestId('publish-preview-blocked')).toContainText(
      'The topology name "not a name" is not valid',
    );

    await page.getByRole('radio', { name: 'Topology only' }).check();
    await page.getByTestId('publish-name').fill(topology);
    await expect(previewLines(page, 'configs')).toHaveText([
      `Creates Topology config ${topology}`,
    ]);
  });

  // Previewing wrote nothing.
  expect(await builder.config('Topology', topology)).toBeNull();
  expect(await builder.config('Topology', renamed)).toBeNull();
  expect(await builder.config('Experiment', experiment)).toBeNull();
  expect(
    (await builder.config('Scenario', scenario)).metadata.annotations?.topology,
  ).toBeUndefined();

  await expectPublish(page, 200);
  await expect(page.getByTestId('publish-result')).toContainText(
    'Every stage succeeded',
  );
  await closeButton(page).click();
  await expect(builder.dialog).toHaveCount(0);

  await test.step('reopened, the dialog says publishing again changes nothing', async () => {
    await openPublish(builder);
    await expect(page.getByTestId('publish-name')).toHaveValue(topology);
    await expect(previewLines(page, 'configs')).toHaveText([
      `Topology config ${topology} is unchanged: it already holds this diagram`,
    ]);
    await expect(previewLines(page, 'includes')).toHaveText([
      `Keeps included topology ${included}`,
    ]);
    await expect(previewLines(page, 'scenarios')).toHaveText([
      `Scenario ${scenario} already names topology ${topology}`,
    ]);
    await expect(previewLines(page, 'images')).toHaveText([
      /^Disk image ubuntu\.qc2 is still in use \(server and server-2 use it\)/,
    ]);
  });

  expectNoFatal(issues);
});

test(
  'axe finds no serious violations in the Publish dialog showing what publishing changes',
  { tag: ['@axe'] },
  async ({ page, builder, issues }, testInfo) => {
    const scenario = uniqueName(testInfo, 'axe-preview-scn');
    await builder.seedConfig(scenarioConfig(scenario, 'builder-e2e-axe'));
    const draft = await builder.seedDraft(
      previewLab(uniqueName(testInfo, 'axe-preview'), { scenario }),
    );

    for (const scheme of ['light', 'dark']) {
      await test.step(`the ${scheme} theme`, async () => {
        await page.emulateMedia({ colorScheme: scheme });
        await builder.openDraft(draft);

        await openPublish(builder);
        await expect(previewLines(page, 'scenarios')).toHaveCount(1);
        await expect(
          builder.dialog.getByTestId('publish-preview'),
        ).toHaveAttribute('aria-busy', 'false');
        await expectAccessible(page, {
          include: '[data-testid="builder-dialog"]',
          soft: true,
          label: `axe on the Publish dialog's preview (${scheme})`,
        });
        await page.keyboard.press('Escape');
        await expect(builder.dialog).toHaveCount(0);
      });
    }

    expectNoFatal(issues);
  },
);
