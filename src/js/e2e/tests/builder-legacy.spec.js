// Diagrams of the legacy Builder: Upload converts a diagram file, or a
// Topology config that holds one, into a new draft, and Import converts the
// diagram of a stored topology, which publishing the draft then replaces.
//
// The server does the conversion (POST /builder/legacy, and POST
// /builder/generate for a stored topology); the Go tests cover what it makes
// of each kind of cell. This spec covers the dialogs and the way to a
// published topology.

const fs = require('node:fs');
const path = require('node:path');

const {
  API,
  expect,
  expectNoFatal,
  openConfigs,
  test,
  uniqueName,
  waitForApi,
} = require('./builder-support');

// A diagram saved by the legacy Builder: five devices, of which two sit in
// a container titled DMZ, the switch of VLAN users, a line between two
// devices on VLAN b, which has no switch, a line that links nothing, and a
// text. The same bytes as the sample the Go tests convert
// (src/go/types/builder/testdata/legacy/sample.xml).
const SAMPLE = fs.readFileSync(
  path.join(__dirname, 'fixtures', 'legacy-sample.xml'),
  'utf8',
);

const HOSTNAMES = [
  'client-1',
  'firewall-device-1',
  'router-device-0',
  'server-device-4',
  'server-device-5',
];

// What the conversion of SAMPLE says it changed or left out.
const WARNINGS = [
  'Added a switch for 1 network that had none in the legacy diagram: b.',
  'Left out 1 line that was not a network link.',
  'Text and containers were kept as notes and groups. Their colors, fonts and other formatting were not converted.',
];

// The node type each device of SAMPLE shows on the canvas, from the
// settings the diagram holds: the last is external hardware.
const TYPES = {
  'client-1': 'VirtualMachine',
  'firewall-device-1': 'Firewall',
  'router-device-0': 'Router',
  'server-device-4': 'VirtualMachine',
  'server-device-5': 'External',
};

const COUNTS = {
  devices: 5,
  switches: 2,
  networks: 2,
  links: 6,
  groups: 1,
  notes: 1,
};

// A diagram that converts without a warning, so nothing but the dialog
// stands between its conversion and a new draft.
const PLAIN =
  '<mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/>' +
  '<object label="solo" schemaVars="{&quot;general&quot;:{&quot;hostname&quot;:&quot;solo&quot;},' +
  '&quot;device&quot;:&quot;server&quot;}" id="2"><mxCell vertex="1" parent="1">' +
  '<mxGeometry x="40" y="40" width="80" height="80" as="geometry"/></mxCell></object>' +
  '</root></mxGraphModel>';

const NOT_A_DIAGRAM =
  'Could not convert the legacy diagram. This is not a legacy Builder diagram: ' +
  'expected mxGraph XML, or a Topology config with the builder-xml annotation.';

const XML_HINT =
  'This looks like a legacy Builder diagram (XML). ' +
  'Choose "Legacy Builder diagram or Topology" to convert it.';

function vm(hostname, vlans, extra = {}) {
  return {
    type: 'VirtualMachine',
    general: { hostname },
    hardware: {
      vcpus: 1,
      memory: 2048,
      os_type: 'linux',
      drives: [{ image: 'ubuntu.qc2' }],
    },
    network: {
      interfaces: vlans.map((vlan, index) => ({
        name: `eth${index}`,
        vlan,
        type: 'ethernet',
        proto: 'dhcp',
      })),
    },
    ...extra,
  };
}

// The topology SAMPLE was drawn for, as the legacy Builder stored it: the
// nodes in its spec, and the diagram in the builder-xml annotation. The
// spec is the truth of a conversion: client-1 has more memory here than
// the diagram gives it.
function legacyTopology(name) {
  const client = vm('client-1', ['users']);
  client.hardware.memory = 4096;

  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: {
      name,
      annotations: { 'builder-xml': SAMPLE, owner: 'e2e' },
    },
    spec: {
      nodes: [
        vm('router-device-0', ['users', 'b'], { type: 'Router' }),
        vm('firewall-device-1', ['b'], { type: 'Firewall' }),
        client,
        vm('server-device-4', ['users']),
        vm('server-device-5', ['users']),
      ],
    },
  };
}

function xmlFile(name, text = SAMPLE) {
  return { name, mimeType: 'text/xml', buffer: Buffer.from(text) };
}

// Records every draft-create request the page sends from now on.
function watchDraftCreates(page) {
  const creates = [];
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname.endsWith(`${API}/builder/drafts`)
    ) {
      creates.push(request.url());
    }
  });

  return creates;
}

// The Upload dialog, from the landing's button or the editor's toolbar,
// with its legacy source chosen.
async function openLegacyUpload(page, opener) {
  await opener.click();
  const dialog = page.getByRole('dialog', { name: 'Upload diagram' });
  await expect(dialog).toBeVisible();
  await dialog
    .getByLabel('Legacy Builder diagram or Topology', { exact: true })
    .check();

  return dialog;
}

// Chooses `file` in the legacy source, presses Convert and returns the POST
// /builder/legacy response.
async function convert(page, dialog, file) {
  await dialog.getByTestId('upload-legacy-file').setInputFiles(file);
  const converted = waitForApi(page, 'POST', '/builder/legacy');
  await dialog.getByTestId('upload-submit').click();

  return converted;
}

// The node type a device shows on the canvas.
function typeOf(builder, hostname) {
  return builder.node(hostname, 'device').getByTestId('node-type');
}

// The rows of the canvas's info tooltip, as [label, lines] pairs.
function tooltipRows(tooltip) {
  return tooltip
    .locator('dl')
    .evaluate((list) =>
      [...list.querySelectorAll('dt')].map((label) => [
        label.textContent.trim(),
        [
          ...label.nextElementSibling.querySelectorAll('.builder-info__line'),
        ].map((line) => line.textContent.trim()),
      ]),
    );
}

// The diagram SAMPLE converts to, as the editor shows it.
async function expectSampleDiagram(builder) {
  await builder.expectCounts(COUNTS, { soft: true });
  for (const hostname of HOSTNAMES) {
    await expect.soft(builder.outlineItem(hostname), hostname).toBeVisible();
  }
  await expect.soft(builder.outlineItem('DMZ')).toBeVisible();
  await expect.soft(builder.outlineItem('a note')).toBeVisible();
}

test(
  'Upload converts a legacy diagram file by keyboard, after its warnings, and refuses what is not one',
  { tag: '@cross-browser' },
  async ({ page, builder, issues }, testInfo) => {
    const title = uniqueName(testInfo, 'legacy');
    await builder.open();
    const creates = watchDraftCreates(page);

    await page.getByTestId('drafts-upload').click();
    const dialog = page.getByRole('dialog', { name: 'Upload diagram' });
    await expect(dialog).toBeVisible();
    const error = dialog.getByTestId('upload-error');
    const submit = dialog.getByTestId('upload-submit');

    // A Builder document is never XML: the sources that read one say where
    // a legacy diagram goes.
    await test.step('the File and Paste text sources point XML at the legacy source', async () => {
      const file = dialog.getByTestId('upload-file');
      await file.setInputFiles(xmlFile('diagram.xml'));
      await submit.click();
      await expect.soft(error).toHaveText(XML_HINT);
      await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(file).toBeFocused();

      await dialog.getByLabel('Paste text', { exact: true }).check();
      const text = dialog.getByTestId('upload-text');
      await text.fill(`\n  ${SAMPLE}`);
      await submit.click();
      await expect.soft(error).toHaveText(XML_HINT);
      await expect.soft(text).toBeFocused();
      await text.fill('');
      expect(creates).toEqual([]);
    });

    const file = dialog.getByTestId('upload-legacy-file');

    await test.step('the arrow keys reach the legacy source, and Tab its one field', async () => {
      const sources = dialog.getByRole('radio');
      await expect(sources).toHaveCount(4);
      await sources.nth(1).focus();
      await page.keyboard.press('ArrowDown');
      await page.keyboard.press('ArrowDown');
      const legacy = dialog.getByRole('radio', {
        name: 'Legacy Builder diagram or Topology',
        exact: true,
      });
      await expect(legacy).toBeChecked();
      await expect.soft(legacy).toBeFocused();
      // What the other source was told says nothing of this one.
      await expect.soft(error).toHaveCount(0);
      await expect.soft(submit).toHaveText('Convert');

      await page.keyboard.press('Tab');
      await expect.soft(file).toBeFocused();
      await expect
        .soft(file)
        .toHaveAccessibleName('Legacy diagram or Topology file');
      await expect
        .soft(file)
        .toHaveAccessibleDescription(
          'A diagram saved by the legacy Builder (XML), or a Topology config that has the builder-xml annotation (YAML or JSON). Up to 5 MiB.',
        );
      await expect
        .soft(dialog)
        .toContainText(
          'The diagram is converted into a new draft. The file and any stored topology are not changed.',
        );
      // A file is the only way in: there is no field to paste into.
      await expect.soft(dialog.getByRole('textbox')).toHaveCount(0);
      await page.keyboard.press('Tab');
      await expect
        .soft(dialog.getByRole('button', { name: 'Cancel' }))
        .toBeFocused();
      await page.keyboard.press('Tab');
      await expect.soft(submit).toBeFocused();
    });

    await test.step('Convert with no file names the field and moves to it', async () => {
      await submit.press('Enter');
      await expect.soft(error).toHaveText('Choose a file to convert.');
      await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(file).toBeFocused();
      await expect
        .soft(file)
        .toHaveAccessibleDescription(
          /Up to 5 MiB\. Choose a file to convert\.$/,
        );
    });

    await test.step("a file that is no diagram is refused in the server's words, on the field", async () => {
      const refused = await convert(page, dialog, {
        name: 'hello.txt',
        mimeType: 'text/plain',
        buffer: Buffer.from('hello'),
      });
      expect.soft(refused.status(), 'status').toBe(422);
      await expect.soft(error).toHaveText(NOT_A_DIAGRAM);
      await expect
        .soft(dialog.locator('#upload-error'))
        .toHaveAttribute('role', 'alert');
      await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(file).toBeFocused();
      await expect
        .soft(file)
        .toHaveAccessibleDescription(/builder-xml annotation\.$/);
      await expect.soft(submit).toHaveText('Convert');

      // A Topology config that has no diagram is for Import.
      const plain = uniqueName(testInfo, 'plain');
      const topology = legacyTopology(plain);
      delete topology.metadata.annotations['builder-xml'];
      const answered = await convert(page, dialog, {
        name: 'plain.json',
        mimeType: 'application/json',
        buffer: Buffer.from(JSON.stringify(topology)),
      });
      expect.soft(answered.status(), 'status').toBe(422);
      await expect
        .soft(error)
        .toHaveText(
          `Could not convert the legacy diagram. Topology ${plain} has no legacy Builder diagram ` +
            '(no builder-xml annotation); use Import to make a diagram from it.',
        );
      await expect(dialog).toBeVisible();
      expect(creates).toEqual([]);
    });

    const next = dialog.getByRole('button', { name: 'Continue to editor' });

    await test.step('the diagram is converted, and its warnings are shown before any draft is made', async () => {
      await file.setInputFiles(xmlFile(`${title}.xml`));
      await page.keyboard.press('Tab');
      await page.keyboard.press('Tab');
      await expect(submit).toBeFocused();

      // The request is held so the busy button can be looked at: it says
      // what it is doing, keeps focus, and sends nothing more when pressed
      // again.
      let release;
      const held = new Promise((resolve) => {
        release = resolve;
      });
      const sent = [];
      await page.route(`**${API}/builder/legacy`, async (route) => {
        sent.push(route.request().url());
        await held;
        await route.continue();
      });
      const converted = waitForApi(page, 'POST', '/builder/legacy');
      await page.keyboard.press('Enter');
      await expect.soft(submit).toHaveText('Converting…');
      await expect.soft(submit).toHaveAttribute('aria-disabled', 'true');
      await expect.soft(submit).toHaveJSProperty('disabled', false);
      await expect.soft(submit).toBeFocused();
      await expect.soft(dialog.getByRole('status')).toHaveText('Converting…');
      await page.keyboard.press('Enter');
      release();
      const response = await converted;
      await page.unroute(`**${API}/builder/legacy`);
      expect(response.status(), await response.text()).toBe(200);
      expect.soft(sent, 'conversions sent').toHaveLength(1);
      // The diagram is named after its file.
      expect.soft(response.request().postDataJSON()).toEqual({
        content: SAMPLE,
        name: title,
      });
      const body = await response.json();
      expect.soft(body.warnings).toEqual(WARNINGS);
      expect.soft(body).not.toHaveProperty('source');

      const shown = dialog.getByTestId('upload-warnings');
      await expect(shown.locator('li')).toHaveText(
        WARNINGS.map((warning) => `Warning: ${warning}`),
      );
      await expect
        .soft(dialog.getByTestId('upload-result'))
        .toContainText('This conversion has 3 warnings.');
      await expect
        .soft(dialog.locator('.builder-dialog__actions button'))
        .toHaveText(['Cancel', 'Continue to editor']);
      // Focus is on the way on, which the warnings describe, and the
      // dialog's status says how many there are.
      await expect.soft(next).toBeFocused();
      await expect
        .soft(next)
        .toHaveAccessibleDescription(/Added a switch for 1 network/);
      await expect
        .soft(dialog.getByRole('status'))
        .toHaveText('This conversion has 3 warnings.');
      expect.soft(creates, 'draft creates before Continue').toEqual([]);
      await expect.soft(builder).not.toHaveAnnounced(/converted/i);
    });

    const created = waitForApi(page, 'POST', '/builder/drafts');
    await page.keyboard.press('Enter');
    const made = await created;
    const draft = await made.json();
    await expect(dialog).toBeHidden();
    await expect(builder.canvas).toBeVisible();
    await builder.waitSaved();

    await test.step('the draft holds the diagram, and says it came from a legacy file', async () => {
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'The legacy diagram was converted with 3 warnings. Draft created.',
        );
      await expect.soft(page.getByTestId('builder-name')).toHaveText(title);
      await expectSampleDiagram(builder);
      // Focus follows into the editor rather than falling to the page.
      expect
        .soft(
          await page.evaluate(() => document.activeElement !== document.body),
        )
        .toBe(true);

      // A diagram without a topology names no config, so its draft is an
      // upload's: it can only publish a new topology.
      expect
        .soft(made.request().postDataJSON().sourceFile)
        .toBe(`${title}.xml`);
      const stored = await builder.serverDraft(draft);
      expect.soft(stored.sourceToken).toBe('uploaded/legacy-xml');
      expect.soft(stored.sourceFile).toBe(`${title}.xml`);
      const document = await builder.serverDocument(draft);
      expect.soft(document.source).toMatchObject({
        kind: 'manual',
        warnings: WARNINGS,
      });
      // The diagram's own settings are the nodes', its variables resolved.
      const router = document.nodes.find(
        (node) => node.device?.hostname === 'router-device-0',
      );
      expect.soft(router?.device.spec.type).toBe('Router');
      expect
        .soft(router?.device.spec.hardware.drives)
        .toEqual([{ image: 'vyos.qc2' }]);
    });

    // A converted node is a node like any other: the canvas shows what the
    // legacy diagram said of it.
    await test.step('each device shows its node type, and the info tooltips what the diagram held', async () => {
      for (const [hostname, type] of Object.entries(TYPES)) {
        await expect.soft(typeOf(builder, hostname), hostname).toHaveText(type);
      }

      const tooltip = page.getByTestId('node-tooltip');
      // A node shows its tooltip for a pointer that moves onto it.
      const parkPointer = () =>
        page.getByRole('heading', { name: 'Outline' }).hover();

      await parkPointer();
      await builder.node('router-device-0', 'device').hover();
      await expect(tooltip).toBeVisible();
      expect.soft(await tooltipRows(tooltip)).toEqual([
        ['Description', ['None']],
        ['Interfaces', ['eth0 — DHCP', 'eth1 — DHCP']],
        ['OS type', ['linux']],
      ]);

      // The switch the conversion added for VLAN b, between its devices.
      await parkPointer();
      await expect(tooltip).toHaveCount(0);
      await builder.node('b', 'switch').hover();
      await expect(tooltip).toBeVisible();
      expect.soft(await tooltipRows(tooltip)).toEqual([
        ['Network', ['b']],
        ['VLAN alias', ['no alias']],
        ['Description', ['None']],
        [
          'Connected devices (2)',
          ['firewall-device-1 — DHCP', 'router-device-0 — DHCP'],
        ],
      ]);
      await parkPointer();
      await expect(tooltip).toHaveCount(0);

      // The diagram's own switch, whose VLAN ID became the alias, says the
      // same of its devices to assistive technology, wherever it is drawn.
      const users = builder.node('users', 'switch');
      const wrapper = page.locator(
        `.vue-flow__node[data-id="${await users.getAttribute('data-node-id')}"]`,
      );
      await expect.soft(wrapper).toHaveAccessibleName(/VLAN alias 101/);
      await expect
        .soft(wrapper)
        .toHaveAccessibleDescription(
          /^4 connected devices: client-1 10\.10\.10\.10\/24, router-device-0 DHCP, server-device-4 DHCP, server-device-5 no address\./,
        );
    });

    await test.step('Publish proposes a new topology', async () => {
      const publish = await builder.openDialog('publish');
      await expect.soft(page.getByTestId('publish-name')).toHaveValue(title);
      await expect
        .soft(publish.locator('#publish-topology-action-hint'))
        .toHaveText('A new topology will be created.');
      await expect
        .soft(page.getByTestId('publish-submit'))
        .toHaveText('Create topology');
      await page.keyboard.press('Escape');
      await expect(builder.dialog).toHaveCount(0);
    });

    // In the editor, Upload opens over the open draft. Until the user goes
    // on past the warnings that draft is the one being edited.
    await test.step('Cancel on the warnings leaves the open draft as it is', async () => {
      const before = creates.length;
      const again = await openLegacyUpload(page, builder.toolbar('upload'));
      const answered = await convert(page, again, xmlFile('another.xml'));
      expect(answered.status()).toBe(200);
      await expect(again.getByTestId('upload-warnings')).toBeVisible();
      await again.getByTestId('upload-cancel').click();
      await expect(again).toBeHidden();
      await expect.soft(builder.toolbar('upload')).toBeFocused();

      await expect.soft(page.getByTestId('builder-name')).toHaveText(title);
      await builder.expectCounts(COUNTS, { soft: true });
      expect.soft(creates.length, 'draft creates after Cancel').toBe(before);
      // The next edit is saved to that draft.
      await builder.palette('switch').click();
      await builder.waitSaved();
      await builder.persisted(
        draft,
        (document) =>
          document.nodes.filter((node) => node.kind === 'switch').length,
        3,
        { soft: true },
      );
    });

    // A conversion takes a moment. Whoever closes the dialog meanwhile has
    // cancelled it: its answer, a diagram or a failure, changes nothing.
    await test.step('Cancel while a conversion is under way drops its answer', async () => {
      const before = creates.length;

      for (const answer of ['a failure', 'a diagram']) {
        const again = await openLegacyUpload(page, builder.toolbar('upload'));
        await again
          .getByTestId('upload-legacy-file')
          .setInputFiles(xmlFile('late.xml', PLAIN));

        let release;
        const held = new Promise((resolve) => {
          release = resolve;
        });
        await page.route(`**${API}/builder/legacy`, async (route) => {
          await held;
          await (answer === 'a failure' ? route.abort() : route.continue());
        });
        const finished = page.waitForEvent(
          answer === 'a failure' ? 'requestfailed' : 'requestfinished',
          (request) =>
            new URL(request.url()).pathname.endsWith(`${API}/builder/legacy`),
        );
        await again.getByTestId('upload-submit').click();
        await expect(again.getByTestId('upload-submit')).toHaveText(
          'Converting…',
        );
        await again.getByRole('button', { name: 'Cancel' }).click();
        await expect(again, answer).toBeHidden();
        release();
        const request = await finished;
        await page.unroute(`**${API}/builder/legacy`);

        // The diagram would have opened at once: it has no warnings.
        if (answer === 'a diagram') {
          const body = await (await request.response()).json();
          expect(body.warnings, 'warnings of the plain diagram').toEqual([]);
        }
      }

      // The draft that was open is still the one being edited: the next
      // edit is saved to it, under its name, with no word of a conversion.
      await builder.palette('switch').click();
      await builder.waitSaved();
      await builder.persisted(
        draft,
        (document) =>
          document.nodes.filter((node) => node.kind === 'switch').length,
        4,
      );
      await expect.soft(page.getByTestId('builder-name')).toHaveText(title);
      // Each switch added here brought a network of its own.
      await builder.expectCounts(
        { ...COUNTS, switches: 4, networks: 4 },
        { soft: true },
      );
      await expect.soft(page.getByTestId('builder-error')).toHaveCount(0);
      expect.soft(creates.length, 'draft creates after Cancel').toBe(before);
    });

    expectNoFatal(issues);
  },
);

test('a topology with a legacy diagram converts from a file and from the store, and publishing replaces the diagram', async ({
  page,
  request,
  builder,
  issues,
}, testInfo) => {
  const name = uniqueName(testInfo, 'legacy-topo');
  const topology = legacyTopology(name);
  await builder.seedConfig(topology);
  // The same topology with a diagram no import can read: its base64 text.
  const unread = uniqueName(testInfo, 'legacy-unread');
  const unreadable = legacyTopology(unread);
  unreadable.metadata.annotations['builder-xml'] =
    Buffer.from(SAMPLE).toString('base64');
  await builder.seedConfig(unreadable);
  await builder.open();

  // The file Configs downloads for such a topology still holds the diagram.
  await test.step('Upload converts the topology as a file into a draft that updates no stored topology', async () => {
    const dialog = await openLegacyUpload(
      page,
      page.getByTestId('drafts-upload'),
    );
    const converted = await convert(page, dialog, {
      name: `${name}.json`,
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(topology)),
    });
    expect(converted.status(), await converted.text()).toBe(200);
    const body = await converted.json();
    expect.soft(body.warnings).toEqual(WARNINGS);
    expect.soft(body.source).toMatchObject({
      fullName: `Topology/${name}`,
      stored: false,
      builder: 'builder-xml',
    });

    const created = waitForApi(page, 'POST', '/builder/drafts');
    await dialog.getByTestId('upload-continue').click();
    const draft = await (await created).json();
    await expect(builder.canvas).toBeVisible();
    await builder.waitSaved();
    await expect.soft(page.getByTestId('builder-name')).toHaveText(name);
    await expectSampleDiagram(builder);
    const stored = await builder.serverDraft(draft);
    expect.soft(stored.sourceToken).toBe(`uploaded/Topology/${name}`);
    expect.soft(stored.sourceFile).toBe(`${name}.json`);

    // The stored topology of that name is not what the file was read from.
    await builder.openDialog('publish');
    await expect.soft(page.getByTestId('publish-name')).toHaveValue(name);
    await expect
      .soft(page.locator('#publish-topology-action-hint'))
      .toHaveText(
        /^A topology with this name already exists, and this diagram cannot update it: /,
      );
    await expect
      .soft(page.getByTestId('publish-submit'))
      .toHaveText('Create topology');
    await page.keyboard.press('Escape');
    await expect(builder.dialog).toHaveCount(0);
    await builder.backToDrafts();
  });

  let draft;

  await test.step('Import marks the stored topology, says what its import does and converts it', async () => {
    await page.getByTestId('drafts-import').click();
    const dialog = page.getByRole('dialog', {
      name: 'Import topology or experiment',
    });
    await expect(dialog).toBeVisible();
    const choices = dialog.getByTestId('import-name');
    const hint = dialog.getByTestId('import-legacy-hint');
    await expect(choices.locator(`option[value="${name}"]`)).toHaveText(
      `${name} (legacy Builder diagram)`,
    );
    await expect.soft(hint).toHaveCount(0);

    await choices.selectOption(name);
    await expect
      .soft(hint)
      .toHaveText(
        'This topology has a legacy Builder diagram. Its layout is converted. ' +
          'Publishing the draft to this topology replaces the legacy diagram.',
      );
    await expect
      .soft(choices)
      .toHaveAccessibleDescription(
        /^This topology has a legacy Builder diagram\./,
      );

    const generated = waitForApi(page, 'POST', '/builder/generate');
    await dialog.getByTestId('import-submit').click();
    const response = await generated;
    expect(response.status(), await response.text()).toBe(200);
    expect.soft((await response.json()).source.builder).toBe('builder-xml');

    await expect(
      dialog.getByTestId('import-warnings').locator('li'),
    ).toHaveText(WARNINGS.map((warning) => `Warning: ${warning}`));
    const next = dialog.getByTestId('import-continue');
    await expect.soft(next).toBeFocused();
    await expect
      .soft(dialog.getByRole('status'))
      .toHaveText('This import has 3 warnings.');

    const created = waitForApi(page, 'POST', '/builder/drafts');
    await next.click();
    draft = await (await created).json();
    await expect(builder.canvas).toBeVisible();
    await builder.waitSaved();
    await expect
      .soft(builder)
      .toHaveAnnounced(
        'The legacy diagram was converted with 3 warnings. Draft created.',
      );
    await expectSampleDiagram(builder);

    expect
      .soft((await builder.serverDraft(draft)).sourceToken)
      .toBe(`Topology/${name}`);
    const document = await builder.serverDocument(draft);
    expect.soft(document.source).toMatchObject({ kind: 'topology', name });
    // The topology's spec is the truth, not the settings in the diagram.
    const client = document.nodes.find(
      (node) => node.device?.hostname === 'client-1',
    );
    expect.soft(client?.device.spec.hardware.memory).toBe(4096);
    // So is each node's type: the topology runs server-device-5 as a
    // virtual machine, which the diagram drew as external hardware.
    await expect
      .soft(typeOf(builder, 'server-device-5'))
      .toHaveText('VirtualMachine');
    await expect.soft(typeOf(builder, 'router-device-0')).toHaveText('Router');
    // The diagram says where each node sits: these two are in its DMZ.
    const group = document.nodes.find((node) => node.kind === 'group');
    expect.soft(group?.group.title).toBe('DMZ');
    expect
      .soft(
        document.nodes
          .filter((node) => node.parentId === group?.id)
          .map((node) => node.label)
          .sort(),
      )
      .toEqual(['b', 'firewall-device-1', 'router-device-0']);
  });

  await test.step('Publish says the legacy diagram is replaced, asks, and replaces it', async () => {
    await builder.openDialog('publish');
    await expect.soft(page.getByTestId('publish-name')).toHaveValue(name);
    await expect
      .soft(page.locator('#publish-topology-action-hint'))
      .toHaveText(
        'A topology with this name exists and will be updated. ' +
          'Its legacy Builder diagram is replaced by this diagram.',
      );
    const submit = page.getByTestId('publish-submit');
    await expect(submit).toHaveText('Update topology');

    const published = page.waitForResponse(
      (candidate) =>
        candidate.request().method() === 'POST' &&
        /\/builder\/drafts\/[^/]+\/[^/]+\/publish$/.test(
          new URL(candidate.url()).pathname,
        ),
    );
    await submit.click();
    // Replacing a config cannot be undone, so Publish asks first.
    const confirm = page.getByRole('alertdialog');
    await expect(confirm).toContainText(`Replace topology ${name}?`);
    await page.getByTestId('confirm-accept').click();
    const response = await published;
    expect(response.status(), await response.text()).toBe(200);

    const result = page.getByTestId('publish-result');
    await expect
      .soft(result)
      .toContainText('Published. Every stage succeeded.');
    await expect
      .soft(result)
      .toContainText(
        `Warning: The legacy Builder diagram of topology ${name} was replaced by this diagram.`,
      );

    const config = await builder.config('Topology', name);
    const annotations = config?.metadata?.annotations || {};
    expect.soft(annotations, 'builder-xml').not.toHaveProperty('builder-xml');
    expect.soft(annotations['builder-doc'], 'builder-doc').toMatchObject({
      digest: expect.stringMatching(/^sha256:[0-9a-f]{64}$/),
    });
    // What the legacy Builder did not write is kept.
    expect.soft(annotations.owner, 'owner').toBe('e2e');
    expect
      .soft(
        (config?.spec?.nodes || []).map((node) => node.general.hostname).sort(),
      )
      .toEqual(HOSTNAMES);

    await page.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(builder.dialog).toHaveCount(0);
  });

  await test.step('the topology is then an ordinary Builder topology', async () => {
    // Publishing again updates it, with nothing left to replace.
    await builder.openDialog('publish');
    await expect
      .soft(page.locator('#publish-topology-action-hint'))
      .toHaveText('A topology with this name exists and will be updated.');
    await page.keyboard.press('Escape');
    await expect(builder.dialog).toHaveCount(0);

    const sources = await (await request.get(`${API}/builder/sources`)).json();
    expect
      .soft(sources.topologies.find((entry) => entry.name === name)?.builder)
      .toBe('builder-doc');

    await openConfigs(page);
    const row = page.locator('tr', { hasText: name });
    await expect(row).toBeVisible();
    await expect.soft(row.locator('.tag')).toHaveText('builder');
  });

  // Nothing of a diagram the import could not read is in the draft, so no
  // text says the draft replaces it.
  await test.step('a legacy diagram that cannot be read is said to be removed, not replaced', async () => {
    await builder.open();
    await page.getByTestId('drafts-import').click();
    const dialog = page.getByRole('dialog', {
      name: 'Import topology or experiment',
    });
    await dialog.getByTestId('import-name').selectOption(unread);
    const generated = waitForApi(page, 'POST', '/builder/generate');
    await dialog.getByTestId('import-submit').click();
    const response = await generated;
    expect(response.status(), await response.text()).toBe(200);
    await expect(
      dialog.getByTestId('import-warnings').locator('li').first(),
    ).toHaveText(
      new RegExp(
        `^Warning: The legacy diagram of topology ${unread} could not be read \\(.*\\), ` +
          'so its layout was not used\\. Nodes were placed automatically\\.$',
      ),
    );

    const created = waitForApi(page, 'POST', '/builder/drafts');
    await dialog.getByTestId('import-continue').click();
    await created;
    await expect(builder.canvas).toBeVisible();
    await builder.waitSaved();

    await builder.openDialog('publish');
    await expect.soft(page.getByTestId('publish-name')).toHaveValue(unread);
    await expect
      .soft(page.locator('#publish-topology-action-hint'))
      .toHaveText(
        'A topology with this name exists and will be updated. ' +
          'Its legacy Builder diagram could not be read and is removed.',
      );

    const published = page.waitForResponse(
      (candidate) =>
        candidate.request().method() === 'POST' &&
        /\/builder\/drafts\/[^/]+\/[^/]+\/publish$/.test(
          new URL(candidate.url()).pathname,
        ),
    );
    await page.getByTestId('publish-submit').click();
    await page.getByTestId('confirm-accept').click();
    const answered = await published;
    expect(answered.status(), await answered.text()).toBe(200);

    const result = page.getByTestId('publish-result');
    await expect
      .soft(result)
      .toContainText(
        `Warning: The legacy Builder diagram of topology ${unread} could not be read and was removed.`,
      );
    await expect.soft(result).not.toContainText('replaced by this diagram');

    const annotations =
      (await builder.config('Topology', unread))?.metadata?.annotations || {};
    expect.soft(annotations, 'builder-xml').not.toHaveProperty('builder-xml');
    expect.soft(annotations, 'builder-doc').toHaveProperty('builder-doc');
    expect.soft(annotations.owner, 'owner').toBe('e2e');
  });

  expectNoFatal(issues);
});
