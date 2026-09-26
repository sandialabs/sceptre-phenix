// Builder Beta publication: the Publish and Scenario dialogs, the configs a
// publish writes, and how failures are reported.

const crypto = require('node:crypto');

const {
  API,
  test,
  expect,
  blankDocument,
  draftPath,
  expectAccessible,
  expectNoFatal,
  publishTopology,
  uniqueName,
} = require('./builder-support');

const V2 = 'phenix.sandia.gov/v2';

// --- local helpers -----------------------------------------------------------

// Waits until the server copy of the draft satisfies `check` and the editor
// reports that every change is saved. Publishing reads the server snapshot, so
// a test publishes only after this resolves. With `soft`, a check that never
// holds is recorded and the test goes on once the editor has saved.
async function synced(builder, draft, check, { soft = false } = {}) {
  await (soft ? expect.soft : expect)
    .poll(async () => check(await builder.serverDocument(draft)), {
      timeout: 20000,
    })
    .toBeTruthy();
  await builder.waitSaved();
}

function devicesOf(document) {
  return (document.nodes || []).filter((node) => node.kind === 'device');
}

// Two Server devices (`server`, `server-2`) on one switch (network EXP), both
// connected through the outline form.
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

// The diagram buildLab() draws, written directly: `server` and `server-2`,
// each connected by eth0 to the switch of network EXP. Tests whose subject is
// not drawing the diagram start from this instead of clicking it together.
function labDocument(name) {
  const id = () => crypto.randomUUID();
  const network = { id: id(), name: 'EXP' };
  const sw = {
    id: id(),
    kind: 'switch',
    label: 'EXP',
    position: { x: 0, y: 400 },
    switch: { networkId: network.id },
  };
  const devices = ['server', 'server-2'].map((hostname, index) => ({
    id: id(),
    kind: 'device',
    label: hostname,
    position: { x: index * 320, y: 0 },
    device: {
      hostname,
      iconKey: 'linux',
      spec: {
        type: 'VirtualMachine',
        general: { hostname, vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
        network: {
          interfaces: [
            { name: 'eth0', proto: 'dhcp', type: 'ethernet', vlan: 'EXP' },
          ],
        },
      },
      interfaces: [{ id: id(), name: 'eth0', index: 0 }],
    },
  }));

  return blankDocument(name, {
    nodes: [...devices, sw],
    networks: [network],
    edges: devices.map((device) => ({
      id: id(),
      sourceNodeId: device.id,
      sourceHandleId: device.device.interfaces[0].id,
      targetNodeId: sw.id,
      networkId: network.id,
    })),
  });
}

// Seeds labDocument(name) as a draft and opens it. The Publish dialog offers
// the document name as the topology name.
async function openLab(builder, name) {
  const draft = await builder.seedDraft(labDocument(name));
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

// Submits the Publish dialog, expects the publish call to answer `status` and
// returns the response body. The body is the assertion message, so a wrong
// status shows the server's reason.
async function expectPublish(page, status) {
  const pending = page.waitForResponse(
    (candidate) =>
      candidate.request().method() === 'POST' &&
      /\/builder\/drafts\/[^/]+\/[^/]+\/publish$/.test(
        new URL(candidate.url()).pathname,
      ),
  );
  await page.getByTestId('publish-submit').click();
  const response = await pending;
  const body = await response.text();
  expect(response.status(), body).toBe(status);

  return body;
}

// Records the publish requests the page sends from now on, so a test can
// check that a refusal came before anything was sent.
function watchPublishes(page) {
  const sent = [];
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      /\/builder\/drafts\/[^/]+\/[^/]+\/publish$/.test(
        new URL(request.url()).pathname,
      )
    ) {
      sent.push(request.postDataJSON());
    }
  });

  return sent;
}

// The Publish dialog's refusal of an existing name this draft may not update.
function cannotUpdate(name) {
  return (
    `A topology named "${name}" already exists, and this diagram cannot update it: ` +
    'the diagram was not imported from it, opened from its published diagram or published to it. ' +
    'Enter another name to create a new topology.'
  );
}

function legacyRefusal(name) {
  return (
    `The topology "${name}" belongs to the legacy XML Builder and cannot be updated here. ` +
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

// The parsed builder-doc manifest annotation of a published topology, or
// undefined when the annotation is missing.
function manifestOf(config) {
  const raw = config?.metadata?.annotations?.['builder-doc'];

  return raw ? JSON.parse(raw) : undefined;
}

function topologyHint(page) {
  return page.locator('#publish-topology-action-hint');
}

function closeButton(page) {
  return page.getByRole('button', { name: 'Close', exact: true });
}

// A topology the legacy XML Builder owns; Builder Beta must not overwrite it.
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

// Attaches `yaml` as the draft's uploaded scenario through the Scenario dialog.
async function attachUploadedScenario(builder, draft, name, yaml) {
  const dialog = await builder.openDialog('scenario');
  await dialog.getByTestId('scenario-kind-uploaded').check();
  await dialog.getByTestId('scenario-file').setInputFiles({
    name: 'scenario.yaml',
    mimeType: 'application/yaml',
    buffer: Buffer.from(yaml),
  });
  await expect(dialog.getByTestId('scenario-digest')).toContainText(
    /Content digest: sha256:[0-9a-f]{64}/,
  );
  await dialog.getByTestId('scenario-submit').click();
  await expect(builder.dialog).toHaveCount(0);
  await synced(
    builder,
    draft,
    (document) =>
      document.scenario?.kind === 'uploaded' &&
      document.scenario?.name === name,
  );
}

// --- topology only -------------------------------------------------------------

test('topology-only publish refuses a legacy topology, writes the diagram and offers an update on reopen', async ({
  page,
  builder,
  tracker,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'topo');
  const legacy = uniqueName(testInfo, 'legacy');
  tracker.config('Topology', topology);
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
      .toHaveText('A new topology will be created.');
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
    await name.fill('Untitled topology');
    await page.getByTestId('publish-submit').press('Enter');
    await expect
      .soft(page.getByTestId('publish-error'))
      .toHaveText(
        'The topology name "Untitled topology" is not allowed. ' +
          'Names can use only letters, numbers, underscores (_), at signs (@), ' +
          'periods (.) and hyphens (-), with no spaces. ' +
          'For example: Untitled-topology',
      );
    await expect.soft(name).toHaveAttribute('aria-invalid', 'true');
    await expect.soft(name).toBeFocused();
    await expect
      .soft(name)
      .toHaveAccessibleDescription(/no spaces\. The topology name .*$/);
    await expect(page.getByTestId('publish-result')).toHaveCount(0);
  });

  await test.step('a legacy builder-xml topology is refused before sending and left untouched', async () => {
    const sent = watchPublishes(page);
    await fillPublish(page, { topology: legacy });
    // No diagram may update it, so the form does not offer to.
    await expect
      .soft(topologyHint(page))
      .toHaveText(
        'A topology with this name belongs to the legacy XML Builder and cannot be updated here. ' +
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
      .toHaveText(legacyRefusal(legacy));
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

  await test.step('a new name publishes hostnames, VLANs and the builder-doc manifest', async () => {
    await fillPublish(page, { topology });
    await expect
      .soft(topologyHint(page))
      .toHaveText('A new topology will be created.');
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
    // waits until the dialog closes; the dialog's result is what is read.
    await expect
      .soft(builder.liveRegion)
      .not.toContainText('Diagram published.');

    const config = await builder.config('Topology', topology);
    expect(config, 'published topology config').toBeTruthy();
    // The manifest points at the immutable document published from this draft.
    expect.soft(manifestOf(config), 'builder-doc manifest').toMatchObject({
      draftId: draft.id,
      digest: expect.stringMatching(/^sha256:[0-9a-f]{64}$/),
    });

    const nodes = nodesByHostname(config);
    expect.soft(Object.keys(nodes).sort()).toEqual(['server', 'server-2']);
    for (const hostname of ['server', 'server-2']) {
      const interfaces = nodes[hostname]?.network?.interfaces;
      expect
        .soft(nodes[hostname]?.type, `${hostname} type`)
        .toBe('VirtualMachine');
      expect.soft(interfaces, `${hostname} interfaces`).toHaveLength(1);
      expect.soft(interfaces?.[0], `${hostname} eth0`).toMatchObject({
        name: 'eth0',
        vlan: 'EXP',
      });
    }

    await closeButton(page).click();
    await expect.soft(builder.dialog).toHaveCount(0);
    // Once the dialog is gone, the held message is read.
    await expect.soft(builder.liveRegion).toContainText('Diagram published.');
  });

  await test.step('reopening the draft offers to update the topology', async () => {
    // A fresh mount reads the server's topology list again.
    await builder.openDraft(draft);
    await openPublish(builder);
    await expect.soft(page.getByTestId('publish-name')).toHaveValue(topology);
    await expect
      .soft(topologyHint(page))
      .toHaveText('A topology with this name exists and will be updated.');
    // The button says it overwrites, not just "Publish".
    await expect
      .soft(page.getByTestId('publish-submit'))
      .toHaveText('Update topology');

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

  expectNoFatal(issues);
});

// --- topology and experiment ---------------------------------------------------

test(
  'topology and experiment publish carries the VLAN alias and creates the uploaded scenario',
  { tag: '@cross-browser' },
  async ({ page, builder, tracker, issues }, testInfo) => {
    const topology = uniqueName(testInfo, 'exp-topo');
    const experiment = uniqueName(testInfo, 'exp-exp');
    const scenario = uniqueName(testInfo, 'exp-scn');
    tracker.config('Topology', topology);
    tracker.config('Experiment', experiment);
    tracker.config('Scenario', scenario);

    const draft = await openLab(builder, topology);

    await test.step('set the switch VLAN alias in the inspector', async () => {
      await builder.selectInOutline('EXP');
      await expect(builder.inspector).toContainText('Network EXP');
      const alias = builder.inspector.getByLabel('VLAN alias');
      await alias.fill('101');
      await alias.press('Tab');
      await builder.apply();
      await expect
        .soft(page.getByTestId('builder-networks'))
        .toContainText('VLAN 101');
      await synced(
        builder,
        draft,
        (document) =>
          document.networks.find((n) => n.name === 'EXP')?.alias === 101,
        { soft: true },
      );
    });

    await test.step('attach an uploaded scenario', async () => {
      await attachUploadedScenario(
        builder,
        draft,
        scenario,
        scenarioYaml(scenario, 'builder-e2e-upload'),
      );
    });

    await test.step('the dialog requires a create or update choice for the scenario', async () => {
      await openPublish(builder);
      await fillPublish(page, { topology, experiment });
      await expect
        .soft(page.locator('#publish-experiment-hint'))
        .toHaveText('A new experiment will be created.');
      await expect
        .soft(page.getByTestId('publish-scenario'))
        .toContainText('This diagram carries an uploaded scenario');
      await expect
        .soft(page.getByTestId('publish-scenario-name'))
        .toHaveValue(scenario);

      // No create/update choice yet: the dialog refuses before calling the
      // server.
      let publishCalls = 0;
      page.on('request', (request) => {
        if (request.method() === 'POST' && request.url().endsWith('/publish')) {
          publishCalls += 1;
        }
      });
      await page.getByTestId('publish-submit').click();
      await expect
        .soft(page.getByTestId('publish-error'))
        .toHaveText(
          'Choose whether the uploaded scenario creates a new config or updates an existing one.',
        );
      expect.soft(publishCalls, 'publish requests sent').toBe(0);
      await expect
        .soft(page.getByTestId('publish-scenario-create'))
        .toHaveAttribute('aria-invalid', 'true');

      // A missing scenario name is reported as missing, on its own field.
      const scenarioName = page.getByTestId('publish-scenario-name');
      await scenarioName.fill('');
      await page.getByTestId('publish-scenario-create').check();
      await page.getByTestId('publish-submit').click();
      await expect
        .soft(page.getByTestId('publish-error'))
        .toHaveText('Enter a name for the scenario.');
      await expect.soft(scenarioName).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(scenarioName).toBeFocused();
      expect.soft(publishCalls, 'publish requests sent').toBe(0);
      await scenarioName.fill(scenario);
      await expectAccessible(page, {
        include: '[data-testid="builder-dialog"]',
        soft: true,
        label: 'Publish dialog with an uploaded scenario',
      });
    });

    await test.step('publish creates the topology, scenario and experiment', async () => {
      await page.getByTestId('publish-scenario-create').check();
      await expectPublish(page, 200);
      await expect
        .soft(page.getByTestId('publish-result'))
        .toContainText('Every stage succeeded');
      expect.soft(await stageStatuses(page)).toEqual({
        document: 'created',
        topology: 'created',
        scenario: 'created',
        experiment: 'created',
        draft: 'ok',
      });
    });

    // The config checks are soft and null-safe, so a missing or wrong scenario
    // config does not hide whether the VLAN alias reached the experiment.
    await test.step('the scenario config holds the uploaded apps and names the topology', async () => {
      const stored = await builder.config('Scenario', scenario);
      expect.soft(stored, 'published scenario config').toBeTruthy();
      expect
        .soft(
          stored?.spec?.apps?.map((app) => app.name),
          'scenario apps',
        )
        .toEqual(['builder-e2e-upload']);
      expect
        .soft(stored?.metadata?.annotations?.topology, 'scenario topology')
        .toContain(topology);
    });

    await test.step('the experiment config names the topology, scenario and VLAN alias', async () => {
      const exp = await builder.config('Experiment', experiment);
      expect.soft(exp, 'published experiment config').toBeTruthy();
      expect
        .soft(exp?.metadata?.annotations, 'experiment annotations')
        .toMatchObject({ topology, scenario });
      expect
        .soft(exp?.spec?.vlans?.aliases, 'experiment VLAN aliases')
        .toMatchObject({ EXP: 101 });

      const published = await builder.config('Topology', topology);
      expect
        .soft(manifestOf(published), 'topology builder-doc manifest')
        .toMatchObject({ draftId: draft.id });
    });

    expectNoFatal(issues);
  },
);

test('uploaded scenario can update an existing scenario config', async ({
  page,
  builder,
  tracker,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'upd-topo');
  const experiment = uniqueName(testInfo, 'upd-exp');
  const scenario = uniqueName(testInfo, 'upd-scn');
  tracker.config('Topology', topology);
  tracker.config('Experiment', experiment);
  // Seeded before open: the editor reads the scenario list when it mounts.
  await builder.seedConfig(scenarioConfig(scenario, 'builder-e2e-seeded'));

  const draft = await openLab(builder, topology);

  await test.step('a v1 scenario upload is refused, on its field (R23)', async () => {
    const dialog = await builder.openDialog('scenario');
    await dialog.getByTestId('scenario-kind-uploaded').check();
    const file = dialog.getByTestId('scenario-file');
    await file.setInputFiles({
      name: 'scenario-v1.yaml',
      mimeType: 'application/yaml',
      buffer: Buffer.from(
        scenarioYaml(scenario, 'builder-e2e-v1').replace(
          V2,
          'phenix.sandia.gov/v1',
        ),
      ),
    });
    const error = dialog.getByTestId('scenario-error');
    await expect
      .soft(error)
      .toHaveText(
        'This scenario is phenix.sandia.gov/v1, and the Builder attaches ' +
          `only ${V2} scenarios. Upgrade it to ${V2}, then upload it again.`,
      );
    await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
    await expect
      .soft(file)
      .toHaveAttribute('aria-describedby', /\bscenario-error\b/);
    await expect.soft(dialog.getByTestId('scenario-digest')).toHaveCount(0);

    // Nothing was attached: saving asks for a file, on the file field.
    await dialog.getByTestId('scenario-submit').click();
    await expect.soft(error).toHaveText('Choose a scenario file to upload.');
    await expect.soft(file).toBeFocused();
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(builder.dialog).toHaveCount(0);
    await expect.soft(builder.toolbar('scenario')).toBeFocused();
  });

  await attachUploadedScenario(
    builder,
    draft,
    scenario,
    scenarioYaml(scenario, 'builder-e2e-replaced'),
  );

  await openPublish(builder);
  await fillPublish(page, { topology, experiment });
  await expect(page.getByTestId('publish-scenario-name')).toHaveValue(scenario);
  await page.getByTestId('publish-scenario-update').check();
  await expectPublish(page, 200);
  await expect(page.getByTestId('publish-result')).toContainText(
    'Every stage succeeded',
  );
  expect(await stageStatuses(page)).toMatchObject({
    topology: 'created',
    scenario: 'updated',
    experiment: 'created',
  });

  const stored = await builder.config('Scenario', scenario);
  expect(stored.spec.apps.map((app) => app.name)).toEqual([
    'builder-e2e-replaced',
  ]);
  expect(stored.metadata.annotations.topology).toContain(topology);
  expectNoFatal(issues);
});

test('stored scenario can be attached and published with an experiment', async ({
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

  const dialog = await builder.openDialog('scenario');
  await dialog.getByTestId('scenario-kind-stored').check();
  await dialog.getByTestId('scenario-name').selectOption(scenario);
  await dialog.getByTestId('scenario-submit').click();
  await expect(builder.dialog).toHaveCount(0);
  await synced(
    builder,
    draft,
    (document) => document.scenario?.kind === 'stored',
  );

  await openPublish(builder);
  await expect(page.getByTestId('publish-scenario')).toContainText(
    `The stored scenario ${scenario} will be used as it is on the server.`,
  );
  await fillPublish(page, { topology, experiment });
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

  await test.step('a draft generated from the experiment publishes back to it', async () => {
    // The experiment embeds its own merged copy of the scenario, so the
    // generated draft references the stored scenario by the digest the
    // sources list reports, which is what publishing with "use" checks.
    const generated = await builder.request.post(`${API}/builder/generate`, {
      data: { source: `experiment/${experiment}` },
    });
    expect(generated.ok(), await generated.text()).toBeTruthy();
    const { document } = await generated.json();
    const sources = await (
      await builder.request.get(`${API}/builder/sources`)
    ).json();
    const listed = sources.scenarios.find((entry) => entry.name === scenario);
    expect(document.scenario).toEqual({
      kind: 'stored',
      name: scenario,
      apiVersion: V2,
      digest: listed.digest,
    });

    const source = await builder.seedDraft(document, {
      sourceToken: `Experiment/${experiment}`,
    });
    const intent = {
      mode: 'topology-experiment',
      topology: { name: topology, action: 'update' },
      scenario: { name: scenario, action: 'use' },
      experiment: { name: experiment, action: 'update' },
    };
    const published = await builder.request.post(
      `${draftPath(source)}/publish`,
      { headers: { 'If-Match': source.etag }, data: intent },
    );
    expect(published.status(), await published.text()).toBe(200);
    expect((await published.json()).status).toBe('succeeded');

    // R6: edited and published again, twice, the draft updates the
    // topology and the experiment it changed, which it was imported from.
    // The first publish changed nothing in the experiment, so the second
    // changes it, and the third finds it changed by the draft itself. The
    // schema allows memory as a string, which the experiment update decodes
    // as phenix does, to a number (R64).
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

// --- refusals and republish -------------------------------------------------------

test('legacy builder-xml refusal names the legacy Builder', async ({
  page,
  builder,
}, testInfo) => {
  const legacy = uniqueName(testInfo, 'legacy-msg');

  await openLab(builder, uniqueName(testInfo, 'legacy-msg-draft'));
  await openPublish(builder);
  await fillPublish(page, { topology: legacy });
  await expect(page.getByTestId('publish-submit')).toHaveText(
    'Create topology',
  );
  // Stored after the dialog read the list, so only the server can refuse it.
  await builder.seedConfig(legacyTopology(legacy));
  await expectPublish(page, 409);
  const error = page.getByTestId('publish-error');
  await expect(error).toHaveText(
    `Could not publish the diagram. ${legacyRefusal(legacy)}`,
  );

  // The list is read again, so the form now says why the name cannot be used,
  // and publishing again is refused before anything is sent.
  await expect
    .soft(topologyHint(page))
    .toHaveText(/^A topology with this name belongs to the legacy XML Builder/);
  const sent = watchPublishes(page);
  await page.getByTestId('publish-submit').click();
  await expect.soft(error).toHaveText(legacyRefusal(legacy));
  expect.soft(sent, 'publish requests').toEqual([]);
});

test('update hint is current right after publishing in the same session', async ({
  page,
  builder,
  tracker,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'stale-hint');
  const later = uniqueName(testInfo, 'stale-later');
  tracker.config('Topology', topology);
  const emptyTopology = (name) => ({
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: { name },
    spec: { nodes: [] },
  });
  // The editor reads the topology list when it mounts; with one listed,
  // an editor that relied on that list would not read it again.
  await builder.seedConfig(emptyTopology(uniqueName(testInfo, 'other')));

  await openLab(builder, topology);
  await openPublish(builder);
  await expectPublish(page, 200);
  await expect(page.getByTestId('publish-result')).toContainText(
    'Every stage succeeded',
  );
  expect(await builder.config('Topology', topology)).toBeTruthy();
  await closeButton(page).click();

  await openPublish(builder);
  await expect(topologyHint(page)).toHaveText(
    'A topology with this name exists and will be updated.',
  );
  await expect
    .soft(page.getByTestId('publish-submit'))
    .toHaveText('Update topology');

  await test.step('a topology created after the dialog opened is reported on the name field', async () => {
    await fillPublish(page, { topology: later });
    await expect(page.getByTestId('publish-submit')).toHaveText(
      'Create topology',
    );
    await builder.seedConfig(emptyTopology(later));
    await expectPublish(page, 409);

    // This draft was not loaded from that topology, so the server would
    // refuse an update too: the message does not promise one.
    const name = page.getByTestId('publish-name');
    const error = page.getByTestId('publish-error');
    await expect
      .soft(error)
      .toHaveText(`Could not publish the diagram. ${cannotUpdate(later)}`);
    await expect.soft(name).toHaveAttribute('aria-invalid', 'true');
    await expect.soft(name).toBeFocused();
    // The list is read again, so the form now says the name cannot be used.
    await expect
      .soft(topologyHint(page))
      .toHaveText(
        'A topology with this name already exists, and this diagram cannot update it: ' +
          'the diagram was not imported from it, opened from its published diagram or published to it. ' +
          'Enter another name to create a new topology.',
      );
    await expect
      .soft(page.getByTestId('publish-submit'))
      .toHaveText('Create topology');

    // Publishing again is refused on the field, before anything is sent.
    const sent = watchPublishes(page);
    await page.getByTestId('publish-submit').click();
    await expect.soft(error).toHaveText(cannotUpdate(later));
    await expect.soft(name).toBeFocused();
    expect.soft(sent, 'publish requests').toEqual([]);
  });
});

// R6: a draft updates the topology it published, however often it is
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

  for (const [devices, hostnames] of [
    [3, ['server', 'server-2', 'workstation']],
    [4, ['server', 'server-2', 'workstation', 'workstation-2']],
  ]) {
    await test.step(`publish again with ${devices} devices`, async () => {
      await builder.palette('template-workstation').click();
      await builder.expectSummary(`${devices} devices`);
      await synced(
        builder,
        draft,
        (document) => devicesOf(document).length === devices,
      );

      await openPublish(builder);
      await expect(topologyHint(page)).toHaveText(
        'A topology with this name exists and will be updated.',
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
      expect(Object.keys(nodesByHostname(config)).sort()).toEqual(hostnames);
      await closeButton(page).click();
    });
  }

  await test.step('a topology someone else changed since is not overwritten', async () => {
    const theirs = await builder.config('Topology', topology);
    theirs.spec.nodes[0].general.description = 'their change';
    const changed = await request.put(`${API}/configs/Topology/${topology}`, {
      data: theirs,
    });
    expect(changed.ok(), await changed.text()).toBeTruthy();

    await builder.palette('template-workstation').click();
    await builder.expectSummary('5 devices');
    await synced(
      builder,
      draft,
      (document) => devicesOf(document).length === 5,
    );

    await openPublish(builder);
    await expectPublish(page, 409);

    const refusal =
      `The topology "${topology}" changed after this diagram published it, and publishing would overwrite that change. ` +
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
        'A topology with this name changed after this diagram published it, and publishing would overwrite that change. ' +
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

// --- device types ---------------------------------------------------------------

test('router and firewall templates publish with node types Router and Firewall', async ({
  page,
  builder,
  tracker,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'templates');
  tracker.config('Topology', topology);

  await builder.open();
  const draft = await builder.createBlank();
  await builder.rename(topology);
  await builder.palette('template-router').click();
  await builder.palette('template-firewall').click();

  // Before any interface exists, the node's is the only Type field.
  for (const [template, type] of [
    ['router', 'Router'],
    ['firewall', 'Firewall'],
  ]) {
    await test.step(`the Inspector shows ${template} as type ${type}`, async () => {
      await builder.selectInOutline(template);
      await expect
        .soft(builder.inspector.getByLabel(/^Type/))
        .toHaveValue(type);
    });
  }

  await builder.palette('switch').click();
  await builder.connect({ label: 'router' }, { index: 1 });
  await builder.expectSummary('1 connection');
  await builder.connect({ label: 'firewall' }, { index: 1 });
  await builder.expectSummary('2 connections');
  await synced(builder, draft, (document) => document.edges?.length === 2);

  await openPublish(builder);
  await expectPublish(page, 200);
  await expect
    .soft(page.getByTestId('publish-result'))
    .toContainText('Every stage succeeded');

  const nodes = nodesByHostname(await builder.config('Topology', topology));
  for (const [template, type] of [
    ['router', 'Router'],
    ['firewall', 'Firewall'],
  ]) {
    await test.step(`${template} template publishes as ${type}`, async () => {
      expect.soft(nodes[template]?.type, `${template} node type`).toBe(type);
    });
  }
});

// --- disconnected interfaces ----------------------------------------------------

// R20: phenix stores a topology with an interface VLAN of "", and minimega
// refuses it only when the experiment starts. The draft keeps such an
// interface, as a warning, but publishing refuses it, in the dialog and on
// the server, and says which interface to fix. Typing its VLAN connects it,
// on a switch added for a network that has none.
test('an interface with no VLAN is refused at publish, and its VLAN connects it', async ({
  page,
  builder,
  tracker,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'disconnected');
  tracker.config('Topology', topology);

  const draft = await openLab(builder, topology);
  const errors = builder.dialog.locator('li[data-level="error"]');
  const vlan = builder.inspector
    .locator('[data-path="spec.network.interfaces.0.vlan"]')
    .getByRole('textbox');

  await test.step('a disconnected interface is refused in the dialog and by the server', async () => {
    // Delete on a canvas connection removes it and keeps the interface.
    await page.locator('g.vue-flow__edge').first().focus();
    await page.keyboard.press('Delete');
    await builder.expectSummary('1 connection');
    await synced(builder, draft, (document) => document.edges?.length === 1);

    const document = await builder.serverDocument(draft);
    const disconnected = devicesOf(document).find(
      (node) => node.device.spec.network.interfaces[0].vlan === '',
    )?.device.hostname;
    expect(disconnected, 'the device whose eth0 was disconnected').toBeTruthy();

    await openPublish(builder);
    await expect(errors).toHaveText([
      `Error: interface "eth0" of "${disconnected}" is not connected to a network and has no VLAN, so it cannot be published: connect it, or type a VLAN for it`,
    ]);
    await expect(page.getByTestId('publish-submit')).toBeDisabled();
    await expect
      .soft(builder.dialog)
      .toContainText('are ready to publish once the errors below are fixed.');

    // The server refuses it too, before writing anything.
    const current = await builder.request.get(draftPath(draft));
    const refused = await builder.request.post(`${draftPath(draft)}/publish`, {
      headers: { 'If-Match': current.headers().etag },
      data: {
        mode: 'topology',
        topology: { name: topology, action: 'create' },
      },
    });
    expect(refused.status(), await refused.text()).toBe(422);
    // Named in the message, which is what the dialog shows.
    expect((await refused.json()).message).toBe(
      `topology ${topology} cannot be published: interface "eth0" of device "${disconnected}" has no VLAN: connect it to a network, or type a VLAN for it`,
    );
    expect(await builder.config('Topology', topology)).toBeNull();

    await builder.dialog.getByRole('button', { name: 'Cancel' }).click();
  });

  // Deleting the switch keeps its network, with no switch on the canvas.
  await test.step('a VLAN naming a network with no switch adds one, in the same edit', async () => {
    await builder.selectInOutline('EXP');
    await page.keyboard.press('Delete');
    await builder.expectSummary('0 switches, 1 network, 0 connections');

    await builder.selectInOutline('server');
    await vlan.fill('exp');
    await vlan.press('Enter');
    await expect(builder.liveRegion).toContainText(
      'Updated device server, added a switch for network EXP and connected eth0 to network EXP',
    );
    await builder.expectSummary('1 switch, 1 network, 1 connection');
    await expect.soft(vlan).toHaveValue('EXP');

    // One Undo takes back the switch with the connection.
    await builder.toolbar('undo').click();
    await builder.expectSummary('0 switches, 1 network, 0 connections');
    await builder.toolbar('redo').click();
    await builder.expectSummary('1 switch, 1 network, 1 connection');

    await builder.selectInOutline('server-2');
    await vlan.fill('EXP');
    await vlan.press('Enter');
    await expect(builder.liveRegion).toContainText(
      'Updated device server-2 and connected eth0 to network EXP',
    );
    await builder.expectSummary('1 switch, 1 network, 2 connections');
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

  // The server checks every interface of the spec, so the dialog does too,
  // including one with no connection point, as an imported or uploaded
  // diagram can have. A VLAN is published as it is, and phenix matches VLAN
  // names exactly, so "exp" is not network EXP.
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
      'Error: interface #2 of "server" has no name and no VLAN, so it cannot be published: name it, then connect it or type a VLAN for it',
    ]);
    await expect(page.getByTestId('publish-submit')).toBeDisabled();
    await expect
      .soft(builder.dialog.locator('li[data-level="warning"]'))
      .toContainText([
        'interface "eth1" of "server-2" is not connected on the canvas, and its VLAN "exp" differs from network EXP only in case: phenix treats them as different VLANs, so publishing does not put it on network EXP',
      ]);

    const current = await builder.request.get(draftPath(seeded));
    const refused = await builder.request.post(`${draftPath(seeded)}/publish`, {
      headers: { 'If-Match': current.headers().etag },
      data: { mode: 'topology', topology: { name: other, action: 'create' } },
    });
    expect(refused.status(), await refused.text()).toBe(422);
    expect((await refused.json()).message).toBe(
      `topology ${other} cannot be published: interface #2 of device "server" has no VLAN: connect it to a network, or type a VLAN for it`,
    );
    expect(await builder.config('Topology', other)).toBeNull();
  });
});

// --- failures -------------------------------------------------------------------

test('a partial publication lists the failed stage and lets the user go back', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'partial');
  const experiment = uniqueName(testInfo, 'partial-exp');

  // The publish response is mocked, so the diagram's content does not matter.
  await builder.open();
  await builder.createBlank();

  // The server's partial-failure response (writePublishPartial), served
  // without touching the server so the test writes no configs.
  await page.route('**/builder/drafts/*/*/publish', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({
        status: 'partial',
        stages: [
          {
            name: 'document',
            status: 'created',
            message: 'immutable builder document stored',
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
        warnings: [],
        errors: ['experiment publication failed'],
      }),
    }),
  );

  await openPublish(builder);
  // A blank diagram is "Untitled topology", numbered after the first; the
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
  await expect(summary).toHaveText(
    'Published with failures. Some configs were written; the failed stages are listed below.',
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
  // The dialog reports the failure; the page's own alert, behind the modal,
  // would only repeat it.
  await expect.soft(page.getByTestId('builder-error')).toBeHidden();
  // The page's live region is unreadable behind the modal; its message
  // waits until the dialog closes.
  await expect
    .soft(builder.liveRegion)
    .not.toContainText(
      'Publish finished with failures. Some configs were written.',
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

// R25: the server refuses an experiment name experiment.Create would
// refuse before it writes the document or the topology.
test('an invalid experiment name is refused before any config is written', async ({
  page,
  builder,
  tracker,
}, testInfo) => {
  const topology = uniqueName(testInfo, 'bad-exp');
  tracker.config('Topology', topology);

  await openLab(builder, topology);
  await openPublish(builder);
  const sent = watchPublishes(page);
  // phenix uses "all", in any case, to mean every experiment, so the dialog
  // refuses it on its field, as the server does (422) before it writes
  // anything.
  await fillPublish(page, { topology, experiment: 'All' });
  await page.getByTestId('publish-submit').click();

  const name = page.getByTestId('publish-experiment');
  await expect
    .soft(page.getByTestId('publish-error'))
    .toHaveText(
      'The experiment name "All" is reserved: phenix uses it to mean every experiment. Enter another name.',
    );
  await expect.soft(name).toHaveAttribute('aria-invalid', 'true');
  await expect.soft(name).toBeFocused();
  expect.soft(sent, 'publish requests').toEqual([]);
  await expect(page.getByTestId('publish-result')).toHaveCount(0);
  expect(await builder.config('Topology', topology)).toBeNull();
});
