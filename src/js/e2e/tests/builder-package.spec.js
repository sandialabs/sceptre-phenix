// Builder packages: Download's Builder package, and its upload on a server
// that lacks some of what the diagram needs.
//
// One server plays both servers: the package is downloaded with the
// Scenario configs the diagram names, one of them is deleted on the server,
// and the package is uploaded again. Every config and draft the tests make
// is removed by the `tracker` fixture.

const crypto = require('crypto');
const fs = require('fs');

const {
  API,
  blankDocument,
  expect,
  expectAccessible,
  expectNoFatal,
  test,
  uniqueName,
  waitForApi,
} = require('./builder-support');

// Saves what trigger downloads, and returns its name and its text.
async function download(page, trigger) {
  const [file] = await Promise.all([page.waitForEvent('download'), trigger()]);

  return {
    name: file.suggestedFilename(),
    text: fs.readFileSync(await file.path(), 'utf8'),
  };
}

// The landing's Upload button opens the Upload dialog, found by its title.
async function openUpload(page) {
  await page.getByTestId('drafts-upload').click();
  const dialog = page.getByRole('dialog', { name: 'Upload diagram' });
  await expect(dialog).toBeVisible();

  return dialog;
}

// A Scenario config with one app on the diagram's device.
function scenarioConfig(name, app) {
  return {
    apiVersion: 'phenix.sandia.gov/v2',
    kind: 'Scenario',
    metadata: { name },
    spec: { apps: [{ name: app, hosts: [{ hostname: 'pkg-host-01' }] }] },
  };
}

// A diagram of one device that names the given scenarios.
function packageDocument(title, scenarios) {
  return {
    ...blankDocument(title, {
      nodes: [
        {
          id: crypto.randomUUID(),
          kind: 'device',
          label: 'pkg-host-01',
          position: { x: 64, y: 64 },
          device: {
            hostname: 'pkg-host-01',
            iconKey: 'linux',
            spec: {
              type: 'VirtualMachine',
              general: { hostname: 'pkg-host-01', vm_type: 'kvm' },
              hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
              network: { interfaces: [] },
            },
            interfaces: [],
          },
        },
      ],
    }),
    scenarios,
  };
}

// A package of a diagram, as the server makes one with Scenario configs
// ticked: it carries the given Scenario configs and lists what the diagram
// needs.
function packageOf(document, configs) {
  const apps = configs.flatMap((config) =>
    config.spec.apps.map((app) => app.name),
  );

  return {
    $schema: 'https://phenix.sandia.gov/schemas/builder/package/v1',
    document,
    scenarios: Object.fromEntries(
      configs.map((config) => [config.metadata.name, config]),
    ),
    requirements: {
      scenarios: document.scenarios || [],
      topologies: [],
      templates: [],
      icons: [],
      images: [],
      apps: [...new Set(apps)].sort(),
      files: [],
    },
  };
}

// Chooses a package file in the open Upload dialog and uploads it; resolves
// once the dialog shows what its diagram needs.
async function choosePackage(page, dialog, title, pkg) {
  await dialog.getByTestId('upload-file').setInputFiles({
    name: `${title}.package.json`,
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(pkg)),
  });

  const resolved = waitForApi(page, 'POST', '/builder/package/resolve');

  await dialog.getByTestId('upload-submit').click();
  expect((await resolved).status()).toBe(200);
  await expect(dialog.getByTestId('upload-package')).toBeVisible();
}

// Uploads a package from the drafts page and returns the dialog showing
// what its diagram needs.
async function uploadPackage(page, builder, title, pkg) {
  await builder.open();

  const dialog = await openUpload(page);

  await choosePackage(page, dialog, title, pkg);

  return dialog;
}

// Whether a request is a POST to the API path suffix.
function isPost(request, suffix) {
  return (
    request.method() === 'POST' &&
    new URL(request.url()).pathname === `${API}${suffix}`
  );
}

// A promise and the function that settles it, for a stubbed route to wait
// on.
function gate() {
  let release;
  const held = new Promise((resolve) => {
    release = resolve;
  });

  return { held, release };
}

test.describe('Builder packages', () => {
  test('a package carries the ticked scenarios, and its upload creates a missing one only when ticked', async ({
    page,
    request,
    builder,
    issues,
  }, testInfo) => {
    const title = uniqueName(testInfo, 'package');
    const missing = `${title}-gone`;
    const changed = `${title}-changed`;

    await builder.seedConfig(scenarioConfig(missing, 'ntp'));
    await builder.seedConfig(scenarioConfig(changed, 'ntp'));

    const draft = await builder.seedDraft(
      packageDocument(title, [missing, changed]),
    );

    await builder.openDraft(draft);

    const pkg =
      await test.step('the download carries the ticked Scenario configs and lists what the diagram needs', async () => {
        const dialog = await builder.openDialog('download');

        await dialog.getByTestId('download-package-scenarios').check();
        await dialog.getByTestId('download-package-images').check();

        const built = waitForApi(page, 'POST', '/builder/package');
        const file = await download(page, () =>
          dialog.getByTestId('download-package').click(),
        );

        expect
          .soft((await built).request().postDataJSON().include)
          .toEqual(['scenarios', 'images']);
        expect(file.name).toBe(`${title}.package.json`);

        const saved = JSON.parse(file.text);

        expect(saved.$schema).toBe(
          'https://phenix.sandia.gov/schemas/builder/package/v1',
        );
        expect(Object.keys(saved.scenarios).sort()).toEqual(
          [missing, changed].sort(),
        );
        expect(saved.requirements.scenarios).toEqual([missing, changed]);
        expect(saved.requirements.apps).toEqual(['ntp']);
        expect(saved.requirements.images).toEqual([
          { name: 'ubuntu.qc2', usedBy: ['pkg-host-01'] },
        ]);
        expect(saved.document.scenarios).toEqual([missing, changed]);
        await expect
          .soft(dialog.getByRole('status'))
          .toHaveText(`Saved ${file.name}.`);
        await dialog
          .getByRole('button', { name: 'Close', exact: true })
          .click();

        return saved;
      });

    // The server loses one scenario, and the file's copy of the other is
    // not what the server has.
    expect(
      (await request.delete(`${API}/configs/Scenario/${missing}`)).ok(),
    ).toBeTruthy();

    const original = await builder.config('Scenario', changed);
    const file = {
      ...pkg,
      scenarios: {
        ...pkg.scenarios,
        [changed]: {
          ...pkg.scenarios[changed],
          spec: { apps: [{ name: 'ntp', disabled: true }] },
        },
      },
    };

    await test.step('left unticked, the missing scenario is not created, and the draft still names it', async () => {
      const dialog = await uploadPackage(page, builder, title, file);
      const create = dialog.getByTestId(
        `upload-package-create-scenario-${missing}`,
      );

      await expect(
        dialog.getByTestId(`upload-package-status-scenario-${missing}`),
      ).toHaveText('Missing, in the package');
      await expect(create).not.toBeChecked();
      await expect(create).toBeFocused();
      await expect(
        dialog.getByTestId(`upload-package-status-scenario-${changed}`),
      ).toHaveText('Different, in the package');
      await expect(
        dialog.getByTestId(`upload-package-create-scenario-${changed}`),
      ).toHaveCount(0);
      await expect(
        dialog.getByTestId('upload-package-status-image-ubuntu.qc2'),
      ).toHaveText(/^(Present|Missing|Not checked)$/);
      await expect(
        dialog.getByTestId('upload-package-create-image-ubuntu.qc2'),
      ).toHaveCount(0);

      const created = waitForApi(page, 'POST', '/builder/drafts');

      await dialog.getByTestId('upload-package-continue').click();

      const uploaded = await (await created).json();

      await expect(builder.canvas).toBeVisible();
      await builder.waitSaved();
      await builder.persisted(uploaded, (doc) => doc.scenarios, [
        missing,
        changed,
      ]);
      expect(await builder.config('Scenario', missing)).toBeNull();
    });

    await test.step('ticked, it is created from the package, and the different one is left as it is', async () => {
      const dialog = await uploadPackage(page, builder, title, file);

      await dialog
        .getByTestId(`upload-package-create-scenario-${missing}`)
        .check();

      const stored = waitForApi(page, 'POST', '/configs');
      const created = waitForApi(page, 'POST', '/builder/drafts');

      await dialog.getByTestId('upload-package-continue').click();

      const posted = await stored;

      expect(posted.status()).toBe(201);
      expect.soft(posted.request().postDataJSON().metadata.name).toBe(missing);

      const uploaded = await (await created).json();

      await expect(builder.canvas).toBeVisible();
      await builder.waitSaved();
      await builder.persisted(uploaded, (doc) => doc.scenarios, [
        missing,
        changed,
      ]);

      const recreated = await builder.config('Scenario', missing);
      const kept = await builder.config('Scenario', changed);

      expect(recreated?.spec).toEqual(pkg.scenarios[missing].spec);
      expect(kept?.spec).toEqual(original.spec);
    });

    expectNoFatal(issues);
  });

  test(
    'the package options and the list of what a package needs pass an axe scan',
    { tag: '@axe' },
    async ({ page, builder, issues }, testInfo) => {
      const title = uniqueName(testInfo, 'package-axe');
      const scenario = `${title}-sc`;

      await builder.seedConfig(scenarioConfig(scenario, 'ntp'));

      const draft = await builder.seedDraft(
        packageDocument(title, [scenario, `${title}-none`]),
      );

      await builder.openDraft(draft);

      const dialog = await builder.openDialog('download');

      await dialog.getByTestId('download-package-scenarios').check();

      const file = await download(page, async () => {
        await dialog.getByTestId('download-package').click();

        // A scenario that does not exist is listed before the file is saved.
        await expect(
          dialog.getByTestId('download-package-warnings'),
        ).toContainText(`Scenario config ${title}-none does not exist`);
        await expect(dialog.getByTestId('download-package-save')).toBeFocused();
        await expectAccessible(page, {
          include: 'dialog[open]',
          label: 'Download diagram',
        });
        await dialog.getByTestId('download-package-save').click();
      });
      const pkg = JSON.parse(file.text);

      await dialog.getByRole('button', { name: 'Close', exact: true }).click();

      const upload = await uploadPackage(page, builder, title, pkg);

      await expect(
        upload.getByTestId(`upload-package-status-scenario-${title}-none`),
      ).toHaveText('Missing');
      await expectAccessible(page, {
        include: 'dialog[open]',
        label: 'Upload diagram',
      });
      await upload.getByTestId('upload-package-cancel').click();
      await expect(upload).toBeHidden();

      expectNoFatal(issues);
    },
  );

  test(
    'a config that cannot be created is shown before the draft opens, the other is still created, and the dialog stays open meanwhile',
    { tag: '@axe' },
    async ({ page, builder, issues }, testInfo) => {
      const title = uniqueName(testInfo, 'package-fail');
      const refused = `${title}-refused`;
      const created = `${title}-created`;
      const pkg = packageOf(packageDocument(title, [refused, created]), [
        scenarioConfig(refused, 'ntp'),
        scenarioConfig(created, 'ntp'),
      ]);
      const creation = gate();
      const drafts = [];

      // The config the upload creates is removed after the test.
      builder.tracker.config('Scenario', created);

      // The server refuses the first config, and holds the second until the
      // test lets it through.
      await page.route(
        (url) => url.pathname === `${API}/configs`,
        async (route) => {
          const request = route.request();

          if (request.method() !== 'POST') {
            return route.fallback();
          }

          if (request.postDataJSON()?.metadata?.name === refused) {
            return route.fulfill({
              status: 403,
              json: { message: `creating ${refused} not allowed` },
            });
          }

          await creation.held;

          return route.fallback();
        },
      );
      page.on('request', (request) => {
        if (isPost(request, '/builder/drafts')) {
          drafts.push(request);
        }
      });

      const dialog = await uploadPackage(page, builder, title, pkg);
      const cancel = dialog.getByTestId('upload-package-cancel');

      for (const name of [refused, created]) {
        await dialog
          .getByTestId(`upload-package-create-scenario-${name}`)
          .check();
      }

      await dialog.getByTestId('upload-package-continue').click();

      await test.step('while the configs are created, Cancel and closing the dialog do nothing', async () => {
        const progress = dialog.getByTestId('upload-package-progress');

        await expect(progress).toHaveText('Creating 2 configs on this server…');
        await expect(cancel).toHaveAttribute('aria-disabled', 'true');
        await expect(cancel).toHaveAttribute(
          'aria-describedby',
          'upload-package-progress',
        );
        await expectAccessible(page, {
          include: 'dialog[open]',
          label: 'Upload diagram',
        });

        // Playwright clicks no aria-disabled button by itself: Cancel is
        // pressed by force, and by its keyboard path.
        await cancel.click({ force: true });
        await cancel.focus();
        await page.keyboard.press('Enter');
        await page.keyboard.press('Escape');
        await dialog.getByRole('button', { name: 'Close dialog' }).click();
        await expect(dialog).toBeVisible();
        await expect(progress).toBeVisible();
      });

      creation.release();

      await test.step('the refusal is shown before the draft opens, and the other config is created', async () => {
        const warnings = dialog.getByTestId('upload-warnings');

        await expect(warnings).toContainText(refused);
        await expect(warnings.locator('li')).toHaveCount(1);
        expect(drafts).toHaveLength(0);
        expect(await builder.config('Scenario', created)).not.toBeNull();
        expect(await builder.config('Scenario', refused)).toBeNull();

        const opened = waitForApi(page, 'POST', '/builder/drafts');

        await dialog.getByTestId('upload-continue').click();
        expect((await opened).status()).toBe(201);
        await expect(builder.canvas).toBeVisible();
        await expect(dialog).toBeHidden();
      });

      expectNoFatal(issues);
    },
  );

  test('Cancel on the list of what a package needs leaves the open draft as it is', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const title = uniqueName(testInfo, 'package-cancel');
    const scenario = `${title}-sc`;
    const draft = await builder.seedDraft(blankDocument(title));
    const drafts = [];

    await builder.openDraft(draft);
    await builder.expectCounts({});

    const address = page.url();

    page.on('request', (request) => {
      if (isPost(request, '/builder/drafts')) {
        drafts.push(request);
      }
    });

    const pkg = packageOf(packageDocument(`${title}-other`, [scenario]), [
      scenarioConfig(scenario, 'ntp'),
    ]);
    const dialog = await builder.openDialog('upload');

    await choosePackage(page, dialog, title, pkg);
    await dialog
      .getByTestId(`upload-package-create-scenario-${scenario}`)
      .check();
    await dialog.getByTestId('upload-package-cancel').click();
    await expect(dialog).toBeHidden();

    // The editor still shows the draft it had open, empty, at its own
    // address, and nothing was made.
    await builder.expectCounts({});
    expect(page.url()).toBe(address);
    expect(drafts).toHaveLength(0);
    expect(await builder.config('Scenario', scenario)).toBeNull();

    expectNoFatal(issues);
  });

  test('the Download dialog saves a package with warnings only when told to, and none once it is closed', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const title = uniqueName(testInfo, 'package-held');
    const absent = `${title}-none`;
    const draft = await builder.seedDraft(packageDocument(title, [absent]));
    const saved = [];

    await builder.openDraft(draft);
    page.on('download', (file) => saved.push(file.suggestedFilename()));

    // Each step ends with a Builder JSON download: a package saved before
    // it would be saved first.
    const json = `${title}.json`;

    await test.step('Do not save saves nothing', async () => {
      const dialog = await builder.openDialog('download');

      await dialog.getByTestId('download-package').click();
      await expect(
        dialog.getByTestId('download-package-warnings'),
      ).toContainText(`Scenario config ${absent} does not exist`);
      // Each warning shows the server's code for it.
      await expect(
        dialog
          .getByTestId('download-package-warning')
          .getByTestId('issue-code'),
      ).toHaveText('package.config.unreadable');
      await dialog.getByTestId('download-package-discard').click();
      await expect(dialog.getByTestId('download-package-held')).toHaveCount(0);
      await expect(dialog.getByRole('status')).toHaveText(
        'The Builder package was not saved.',
      );
      await expect(dialog.getByTestId('download-package')).toBeFocused();

      const file = await download(page, () =>
        dialog.getByTestId('download-json').click(),
      );

      expect(file.name).toBe(json);
      expect(saved).toEqual([json]);
    });

    await test.step('Save package saves it', async () => {
      const dialog = builder.dialog;

      await dialog.getByTestId('download-package').click();
      await expect(dialog.getByTestId('download-package-save')).toBeFocused();

      const file = await download(page, () =>
        dialog.getByTestId('download-package-save').click(),
      );

      expect(file.name).toBe(`${title}.package.json`);
      expect(JSON.parse(file.text).requirements.scenarios).toEqual([absent]);
      expect(saved).toEqual([json, file.name]);
      await dialog.getByRole('button', { name: 'Close', exact: true }).click();
      await expect(dialog).toBeHidden();
    });

    await test.step('a package the server sends once the dialog is closed is not saved', async () => {
      const answer = gate();

      await page.route(
        (url) => url.pathname === `${API}/builder/package`,
        async (route) => {
          await answer.held;

          return route.fallback();
        },
      );

      const dialog = await builder.openDialog('download');
      const requested = page.waitForRequest((request) =>
        isPost(request, '/builder/package'),
      );

      await dialog.getByTestId('download-package').click();
      await requested;
      await dialog.getByRole('button', { name: 'Close', exact: true }).click();
      await expect(dialog).toBeHidden();

      const before = saved.length;
      const answered = waitForApi(page, 'POST', '/builder/package');

      answer.release();
      expect((await answered).status()).toBe(200);

      const again = await builder.openDialog('download');
      const file = await download(page, () =>
        again.getByTestId('download-json').click(),
      );

      expect(file.name).toBe(json);
      expect(saved.slice(before)).toEqual([json]);
    });

    expectNoFatal(issues);
  });
});
