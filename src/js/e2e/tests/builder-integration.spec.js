// Builder integration tests: the legacy /builder page, the header links, the
// Configs page hand-off to both builders, and the API contracts added with
// Builder Beta. They need a server started with --features builder-beta; the
// flag-off behavior lives in builder-feature-off.spec.js.

const crypto = require('node:crypto');

const {
  API,
  SCHEMA_URI,
  blankDocument,
  expect,
  expectAccessible,
  expectNoFatal,
  publishTopology,
  seedConfig,
  test,
  uniqueName,
  visit,
} = require('./builder-support');
const { fatalOf } = require('./helpers');

const NO_ROUTE = 'no API route matches this request';
const LEGACY_PALETTES = [
  'VM Networking',
  'VM Hosts',
  'External Networking',
  'External Hosts',
];

// Script errors the legacy Save to phēnix dialog has always thrown (they
// predate Builder Beta). Chrome and Firefox word them differently. Fixing the
// legacy Builder is out of this suite's scope, so the legacy tests ignore
// them.
const LEGACY_COPY_SCRIPT = /onclick/;
const LEGACY_NO_SCENARIOS = /reading 'length'|data\.configs is null/;

test.skip(
  process.env.E2E_BUILDER_BETA === 'off',
  'needs a server started with --features builder-beta',
);

function apiResponse(page, method, path, search) {
  return page.waitForResponse((response) => {
    const url = new URL(response.url());

    return (
      response.request().method() === method &&
      url.pathname === `${API}${path}` &&
      (search === undefined || url.search === search)
    );
  });
}

function topology(name, annotations) {
  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: annotations ? { name, annotations } : { name },
    spec: { nodes: [] },
  };
}

// A legacy mxGraph diagram holding one labelled rectangle.
function legacyXml(label) {
  return [
    '<mxGraphModel><root>',
    '<mxCell id="0"/><mxCell id="1" parent="0"/>',
    `<mxCell id="2" value="${label}" style="rounded=0;whiteSpace=wrap;html=1;"`,
    ' vertex="1" parent="1">',
    '<mxGeometry x="80" y="80" width="160" height="60" as="geometry"/>',
    '</mxCell></root></mxGraphModel>',
  ].join('');
}

// A Builder document with one manually authored device and no source, so
// publishing it does not depend on any stored config.
function deviceDocument(name, hostname) {
  return blankDocument(name, {
    nodes: [
      {
        id: crypto.randomUUID(),
        kind: 'device',
        label: hostname,
        position: { x: 0, y: 0 },
        device: {
          hostname,
          iconKey: 'linux',
          spec: {
            type: 'VirtualMachine',
            general: { hostname },
            hardware: {
              os_type: 'linux',
              vcpus: 1,
              memory: 1024,
              drives: [{ image: 'ubuntu.qc2' }],
            },
            network: { interfaces: [] },
          },
          interfaces: [],
        },
      },
    ],
  });
}

async function openConfigs(page) {
  await visit(page, '/configs/');
  await expect(page.locator('table')).toBeVisible({ timeout: 20000 });
}

function configRow(page, name) {
  return page.locator('tr', { hasText: name });
}

// The row's icon-only edit button, found by its tooltip label rather than by
// its position among the row actions.
function editButton(row) {
  return row
    .locator('.b-tooltip')
    .filter({ hasText: 'edit config file' })
    .getByRole('button');
}

async function editConfig(page, name) {
  const fetched = apiResponse(page, 'GET', `/configs/Topology/${name}`);
  await editButton(configRow(page, name)).click();
  await fetched;
}

// Records every Buefy toast the page adds, so a toast that has already
// faded out, or was opened just before a route change, is still visible to
// the test.
async function recordToasts(page) {
  await page.evaluate(() => {
    const seen = new WeakSet();
    window.__e2eToasts = [];
    new MutationObserver(() => {
      for (const toast of document.querySelectorAll('.toast')) {
        if (!seen.has(toast)) {
          seen.add(toast);
          window.__e2eToasts.push({
            type: toast.className,
            text: toast.textContent.trim(),
          });
        }
      }
    }).observe(document.body, { childList: true, subtree: true });
  });
}

function recordedToasts(page) {
  return page.evaluate(() => window.__e2eToasts);
}

async function openLegacy(page) {
  await page.goto('/builder');
  await expect(page.locator('#editor .geDiagramContainer')).toBeVisible({
    timeout: 20000,
  });
}

async function legacyFileMenu(page, item) {
  await page.locator('.geMenubar a.geItem', { hasText: 'File' }).click();
  await page.locator('tr.mxPopupMenuItem', { hasText: item }).click();
}

// Opens File > Save to phēnix and waits for its scenario list to load.
async function openLegacySave(page) {
  const scenarios = apiResponse(page, 'GET', '/configs', '?kind=scenario');
  await legacyFileMenu(page, 'Save to phēnix');
  expect((await scenarios).ok()).toBeTruthy();
  await expect(page.locator('#topo-name')).toBeVisible();
}

// The page errors a legacy test saw, minus the known legacy script errors.
function legacyFatal(issues) {
  return fatalOf(issues).filter(
    (issue) =>
      !LEGACY_COPY_SCRIPT.test(issue.text) &&
      !LEGACY_NO_SCENARIOS.test(issue.text),
  );
}

function expectNoLegacyFatal(issues) {
  const fatal = legacyFatal(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toEqual([]);
}

function legacySuccess(page, name) {
  return page
    .locator('.ui-dialog')
    .filter({ hasText: `The ${name} topology was added to phēnix store` });
}

test.describe('legacy Builder', () => {
  // The legacy editor parses and re-serializes its XML with the browser's DOM
  // APIs, so this also runs in Firefox.
  test(
    'renders the editor, and Import from phēnix lists only legacy diagrams and reopens one',
    {
      tag: '@cross-browser',
    },
    async ({ page, request, tracker, issues }, testInfo) => {
      await test.step('the editor renders with the phēnix palettes', async () => {
        await openLegacy(page);
        await expect
          .soft(page.locator('.geSidebarContainer .geTitle'))
          .toContainText(LEGACY_PALETTES);
        await expect
          .soft(page.locator('.geMenubar a.geItem'))
          .toContainText(['File', 'Edit', 'View']);

        // Rendering the editor raises no script error at all; the known legacy
        // errors come from the Save to phēnix dialog.
        const fatal = fatalOf(issues);
        expect.soft(fatal, JSON.stringify(fatal, null, 2)).toEqual([]);
      });

      // The Import list is fetched when its menu item is chosen, so configs
      // seeded after the page has loaded are listed.
      const legacy = uniqueName(testInfo, 'legacy');
      const plain = uniqueName(testInfo, 'plain');
      const beta = uniqueName(testInfo, 'beta');
      const label = `cell-${crypto.randomUUID().slice(0, 8)}`;
      const seeded = legacyXml(label);
      await seedConfig(
        request,
        tracker,
        topology(legacy, { 'builder-xml': seeded }),
      );
      await seedConfig(request, tracker, topology(plain));
      await publishTopology(request, tracker, beta);

      await test.step('Import from phēnix lists only legacy diagrams', async () => {
        const listed = apiResponse(page, 'GET', '/builder/topologies');
        await legacyFileMenu(page, 'Import from phēnix');
        expect.soft((await listed).ok()).toBeTruthy();

        const select = page.locator('#topology-name');
        await expect(select.locator(`option[value="${legacy}"]`)).toHaveCount(
          1,
        );
        await expect
          .soft(select.locator(`option[value="${plain}"]`))
          .toHaveCount(0);
        await expect
          .soft(select.locator(`option[value="${beta}"]`))
          .toHaveCount(0);
      });

      await test.step('a reopened legacy diagram saves back to its topology', async () => {
        await page.locator('#topology-name').selectOption(legacy);
        const loaded = apiResponse(
          page,
          'GET',
          `/builder/topologies/${legacy}`,
        );
        await page.getByRole('button', { name: 'Open', exact: true }).click();
        expect.soft((await loaded).ok()).toBeTruthy();
        await expect
          .soft(
            page
              .locator('.geDiagramContainer')
              .getByText(label, { exact: true }),
          )
          .toBeVisible();

        // The imported name becomes the save target, so saving updates it.
        await openLegacySave(page);
        await expect(page.locator('#topo-name')).toHaveValue(legacy);
        const updated = apiResponse(page, 'PUT', `/configs/topology/${legacy}`);
        await page.getByRole('button', { name: 'Add Topology' }).click();
        expect((await updated).ok()).toBeTruthy();
        await expect.soft(legacySuccess(page, legacy)).toBeVisible();

        const stored = await (
          await request.get(`${API}/configs/Topology/${legacy}`)
        ).json();
        // The editor re-serializes the diagram (the cell becomes an <object>
        // with a label attribute), so this proves the save wrote the editor's
        // model rather than leaving the seeded XML in place.
        const saved = stored.metadata?.annotations?.['builder-xml'];
        expect.soft(saved).not.toBe(seeded);
        expect.soft(saved).toContain(`label="${label}"`);
      });

      expectNoLegacyFatal(issues);
    },
  );

  test('Save to phēnix offers stored scenarios and adds a legacy topology', async ({
    page,
    request,
    tracker,
    issues,
  }, testInfo) => {
    const scenario = uniqueName(testInfo, 'scenario');
    const name = uniqueName(testInfo, 'saved');
    await seedConfig(request, tracker, {
      apiVersion: 'phenix.sandia.gov/v2',
      kind: 'Scenario',
      metadata: { name: scenario },
      spec: { apps: [] },
    });

    await openLegacy(page);
    await openLegacySave(page);
    const topoName = page.locator('#topo-name');
    const scenarios = page.locator('#scenario-name');
    await expect(topoName).toHaveValue('FIXME');
    await expect(scenarios.locator(`option[value="${scenario}"]`)).toHaveCount(
      1,
    );

    await scenarios.selectOption(scenario);
    await expect(
      page.getByRole('button', { name: 'Create Experiment' }),
    ).toBeVisible();
    await scenarios.selectOption('');
    const add = page.getByRole('button', { name: 'Add Topology' });
    await expect(add).toBeVisible();

    await topoName.fill(name);
    await expect(page.locator('#jsonString')).toHaveValue(
      new RegExp(`name: ${name}`),
    );
    tracker.config('Topology', name);
    const created = apiResponse(page, 'POST', '/configs');
    await add.click();
    expect((await created).status()).toBe(201);
    await expect(legacySuccess(page, name)).toBeVisible();

    const stored = await (
      await request.get(`${API}/configs/Topology/${name}`)
    ).json();
    expect(stored.metadata.annotations['builder-xml']).toContain(
      '<mxGraphModel',
    );

    expectNoLegacyFatal(issues);
  });
});

test.describe('header', () => {
  test('links to both the legacy Builder and Builder Beta', async ({
    page,
    issues,
    tracker,
  }) => {
    await visit(page, '/experiments');

    const legacy = page.getByRole('link', { name: 'Builder', exact: true });
    await expect(legacy).toBeVisible({ timeout: 20000 });
    await expect(legacy).toHaveAttribute('href', /\/builder\?token=/);
    await expect(legacy).toHaveAttribute('target', '_blank');
    const beta = page.getByTestId('nav-builder-beta');
    await expect(beta).toBeVisible();
    // The tab keeps its "beta" tag, which is part of its name.
    await expect(beta).toHaveAccessibleName('Builder Flow beta');
    await expect.soft(beta.locator('.tag')).toHaveText('beta');

    const popup = page.context().waitForEvent('page');
    await legacy.click();
    const tab = await popup;
    await expect(tab.locator('#editor .geDiagramContainer')).toBeVisible({
      timeout: 20000,
    });
    await tab.close();

    await beta.click();
    await expect(page).toHaveURL(/\/builder-beta$/);
    await expect(
      page.getByRole('heading', { name: 'Builder Flow' }),
    ).toBeVisible();

    await test.step('over plain HTTP from another host the editor works', async () => {
      // Browsers offer crypto.randomUUID and crypto.subtle only in a secure
      // context, and treat localhost as secure, so the server is reached
      // through a made-up host that Playwright routes to it. A page of its
      // own keeps that host's failing websocket out of `issues`.
      const base = new URL(page.url()).origin;
      const origin = 'http://builder-flow.test';
      const other = await page.context().newPage();
      const errors = [];
      other.on('pageerror', (error) => errors.push(String(error)));
      tracker.watch(other);
      // The made-up host has no websocket server; an open mock keeps the
      // app's "connection closed" toast from covering the page.
      await other.routeWebSocket(/^wss?:\/\/builder-flow\.test\//, () => {});
      await other.route(`${origin}/**`, async (route) => {
        const request = route.request();
        const response = await route.fetch({
          url: request.url().replace(origin, base),
          // The Vite dev server refuses hosts it does not know.
          headers: {
            ...(await request.allHeaders()),
            host: new URL(base).host,
          },
        });
        await route.fulfill({ response });
      });

      await other.goto(`${origin}/builder-beta`);
      await expect(
        other.getByRole('heading', { name: 'Builder Flow', exact: true }),
      ).toBeVisible({ timeout: 20000 });
      expect
        .soft(
          await other.evaluate(() => [
            window.isSecureContext,
            typeof crypto.randomUUID,
            typeof crypto.subtle,
          ]),
        )
        .toEqual([false, 'undefined', 'undefined']);

      // The new draft, and the device added to it, get new ids.
      const created = other.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          new URL(response.url()).pathname === `${API}/builder/drafts`,
      );
      await other.getByTestId('drafts-blank').click();
      expect((await created).ok()).toBe(true);
      await expect(other.getByTestId('builder-canvas')).toBeVisible();
      await other.getByTestId('palette-device').click();
      await expect(other.getByTestId('builder-summary')).toContainText(
        '1 device',
      );
      await expect(other.getByTestId('builder-save-state')).toContainText(
        'All changes saved',
        { timeout: 20000 },
      );
      expect.soft(errors, 'page errors').toEqual([]);
      // The page keeps polling; a request still in flight when it closes
      // would otherwise fail the worker and skip the next test.
      await other.unrouteAll({ behavior: 'ignoreErrors' });
      await other.close();
    });

    expectNoFatal(issues);
  });
});

test.describe('Configs page', () => {
  test('tags Builder topologies and routes each edit button to the right editor', async ({
    page,
    request,
    tracker,
    builder,
    issues,
  }, testInfo) => {
    const plain = uniqueName(testInfo, 'plain');
    const legacy = uniqueName(testInfo, 'legacy');
    const beta = uniqueName(testInfo, 'beta');
    const xml = legacyXml('legacy-cell');
    await seedConfig(request, tracker, topology(plain));
    await seedConfig(
      request,
      tracker,
      topology(legacy, { 'builder-xml': xml }),
    );
    await publishTopology(
      request,
      tracker,
      beta,
      deviceDocument(beta, 'host-a'),
    );

    await openConfigs(page);
    await expect(configRow(page, plain)).toBeVisible();

    await test.step('each Builder topology is tagged with its builder', async () => {
      await expect.soft(configRow(page, plain).locator('.tag')).toHaveCount(0);
      await expect
        .soft(configRow(page, legacy).locator('.tag'))
        .toHaveText('builder legacy');
      await expect
        .soft(configRow(page, beta).locator('.tag'))
        .toHaveText('builder flow');
    });

    await test.step('a legacy Builder topology is blocked from raw editing', async () => {
      await editConfig(page, legacy);
      const dialog = page.locator('.dialog.modal.is-active');
      await expect.soft(dialog).toContainText('Built by Builder');
      await expect
        .soft(dialog)
        .toContainText('This configuration can only be edited in Builder');
      await expect.soft(page).toHaveURL(/\/configs\/$/);
      await expect
        .soft(page.getByRole('button', { name: 'Save' }))
        .toBeDisabled();
      await dialog.getByRole('button', { name: 'OK' }).click();
      await expect(dialog).toBeHidden();
      await expect(configRow(page, legacy)).toBeVisible();

      const stored = await (
        await request.get(`${API}/configs/Topology/${legacy}`)
      ).json();
      expect.soft(stored.metadata?.annotations?.['builder-xml']).toBe(xml);
    });

    await test.step('an ordinary topology opens in the YAML editor', async () => {
      await editConfig(page, plain);
      await expect
        .soft(page.locator('.ace_content'))
        .toContainText(`name: ${plain}`);
      await expect
        .soft(page.getByRole('button', { name: 'Save' }))
        .toBeEnabled();
      await expect.soft(page).toHaveURL(/\/configs\/$/);
      await expect.soft(page.locator('.dialog.modal.is-active')).toHaveCount(0);

      // Leave the unchanged editor to get back to the list.
      await page.getByRole('button', { name: 'Exit', exact: true }).click();
      await page
        .locator('.dialog.modal.is-active')
        .getByRole('button', { name: 'Continue' })
        .click();
      await expect(configRow(page, beta)).toBeVisible();
    });

    await test.step('a Builder Beta topology opens in Builder Beta as a new draft', async () => {
      const created = apiResponse(page, 'POST', '/builder/drafts');
      await editConfig(page, beta);
      const draft = await (await created).json();
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      // The ?topology= link Configs followed now names the draft.
      await expect
        .soft(page)
        .toHaveURL(
          (url) =>
            url.pathname.endsWith('/builder-beta') &&
            url.searchParams.get('draft') === `${draft.owner}/${draft.id}`,
        );
      await expect.soft(page.getByTestId('builder-name')).toHaveValue(beta);
      await expect.soft(builder.summary).toContainText('1 device');
      await expect.soft(builder.node('host-a', 'device')).toBeVisible();
      await builder.waitSaved();

      const stored = await builder.serverDraft(draft);
      expect.soft(stored.sourceToken).toMatch(/^builder-doc\//);
      const document = await builder.serverDocument(draft);
      expect.soft(document.name).toBe(beta);
      expect
        .soft(document.nodes.map((node) => node.device?.hostname))
        .toEqual(['host-a']);
    });

    await test.step('Back to drafts refreshes the Published tab, which opens read only until edited', async () => {
      // Published while the editor is open, after the landing page loaded its
      // lists: only the refresh on leaving the editor can list it.
      const later = uniqueName(testInfo, 'later');
      await publishTopology(
        request,
        tracker,
        later,
        deviceDocument(later, 'host-b'),
      );

      await builder.backToDrafts();
      await page.getByTestId('drafts-tab-published').click();
      const published = page.getByTestId('drafts-list-published');
      await expect
        .soft(published.locator('li', { hasText: beta }))
        .toBeVisible();
      const card = published.locator('li', { hasText: later });
      await expect(card).toBeVisible();

      // Opening it makes no draft: the diagram is only looked at.
      const creates = [];
      const onRequest = (request) => {
        if (
          request.method() === 'POST' &&
          new URL(request.url()).pathname === `${API}/builder/drafts`
        ) {
          creates.push(request.url());
        }
      };
      page.on('request', onRequest);
      await card.getByRole('button', { name: `Open ${later}` }).click();
      await expect(builder.canvas).toBeVisible();
      await expect.soft(page.getByTestId('builder-name')).toHaveValue(later);
      await expect.soft(builder.summary).toContainText('1 device');
      await expect.soft(builder.node('host-b', 'device')).toBeVisible();
      const panel = page.getByTestId('builder-published');
      await expect(panel).toContainText(
        `You are viewing the published diagram ${later}.`,
      );
      await expect
        .soft(builder.liveRegion)
        .toContainText(`Opened published diagram ${later}, read only.`);
      await expect
        .soft(builder.toolbar('publish'))
        .toHaveAttribute('aria-disabled', 'true');
      await expect.soft(page.getByTestId('builder-name')).not.toBeEditable();
      await expectAccessible(page, {
        soft: true,
        label: 'axe on a published diagram',
      });
      expect.soft(creates, 'draft creates while viewing').toEqual([]);
      page.off('request', onRequest);

      // Edit makes the draft; its button goes, and focus moves on to the
      // editor's heading rather than to <body>.
      const created = apiResponse(page, 'POST', '/builder/drafts');
      await panel.getByRole('button', { name: 'Edit as a draft' }).click();
      const draft = await (await created).json();
      await expect(panel).toHaveCount(0);
      await expect.soft(page.getByRole('heading', { level: 1 })).toBeFocused();
      await builder.waitSaved();
      await expect.soft(page.getByTestId('builder-name')).toBeEditable();
      expect
        .soft((await builder.serverDraft(draft)).sourceToken)
        .toMatch(/^builder-doc\//);
    });

    await test.step('a role that may not create drafts only views the diagram', async () => {
      // The UI takes the role from the session, as a sign-in leaves it. This
      // one may edit configs but not create drafts; the server, with
      // authentication off, would allow anything, so no request may try.
      await page.evaluate(() => {
        sessionStorage.setItem('phenix.user', 'e2e-editor');
        sessionStorage.setItem('phenix.token', 'authorized');
        sessionStorage.setItem('phenix.auth', 'true');
        sessionStorage.setItem(
          'phenix.role',
          JSON.stringify({
            name: 'E2E Config Editor',
            policies: [
              {
                resources: ['*'],
                resourceNames: ['*'],
                verbs: ['list', 'get', 'update'],
              },
            ],
          }),
        );
      });
      const creates = [];
      page.on('request', (request) => {
        if (
          request.method() === 'POST' &&
          new URL(request.url()).pathname === `${API}/builder/drafts`
        ) {
          creates.push(request.url());
        }
      });

      await openConfigs(page);
      await editConfig(page, beta);
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      const panel = page.getByTestId('builder-published');
      await expect(panel).toContainText(
        'Your role cannot create drafts, so it cannot be edited.',
      );
      await expect.soft(panel.getByRole('button')).toHaveCount(0);
      for (const action of ['publish', 'import']) {
        await expect
          .soft(builder.toolbar(action), action)
          .toHaveAttribute('aria-disabled', 'true');
      }

      await builder.backToDrafts();
      await expect.soft(page.getByTestId('drafts-view-only')).toBeVisible();
      for (const id of ['drafts-blank', 'drafts-generate', 'drafts-import']) {
        await expect.soft(page.getByTestId(id), id).toHaveCount(0);
      }
      await expect
        .soft(page.getByRole('button', { name: /^Delete / }))
        .toHaveCount(0);
      expect.soft(creates, 'draft creates').toEqual([]);
    });

    expectNoFatal(issues);
  });

  test('returning from a Builder topology edit shows no empty toast', async ({
    page,
    request,
    tracker,
    builder,
  }, testInfo) => {
    const legacy = uniqueName(testInfo, 'legacy-toast');
    const beta = uniqueName(testInfo, 'beta-toast');
    await seedConfig(
      request,
      tracker,
      topology(legacy, { 'builder-xml': legacyXml('legacy-cell') }),
    );
    await publishTopology(request, tracker, beta);

    await openConfigs(page);
    await expect(configRow(page, legacy)).toBeVisible();
    await recordToasts(page);

    await test.step('after the legacy Builder notice', async () => {
      await editConfig(page, legacy);
      const reloaded = apiResponse(page, 'GET', '/configs');
      await page
        .locator('.dialog.modal.is-active')
        .getByRole('button', { name: 'OK' })
        .click();
      await reloaded;
      await expect(configRow(page, beta)).toBeVisible();

      const empty = (await recordedToasts(page)).filter((toast) => !toast.text);
      expect.soft(empty, JSON.stringify(empty)).toEqual([]);
    });

    await test.step('on the Builder Beta redirect', async () => {
      const seen = (await recordedToasts(page)).length;
      await editConfig(page, beta);
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await builder.waitSaved();
      // One message says where the user is now and that a draft was made.
      await expect
        .soft(builder.liveRegion)
        .toContainText(
          `Opened topology ${beta} in Builder Flow as a new draft.`,
        );

      const empty = (await recordedToasts(page))
        .slice(seen)
        .filter((toast) => !toast.text);
      expect.soft(empty, JSON.stringify(empty)).toEqual([]);
    });
  });
});

test.describe('API', () => {
  test('unknown /api/v1 routes answer a JSON 404 and the Builder schema is served under its $id', async ({
    request,
  }) => {
    await test.step('unknown routes', async () => {
      const paths = [
        '/e2e-no-such-route',
        '/builder/e2e-no-such-route',
        '/experiments/e2e/no/such/route',
      ];

      for (const path of paths) {
        for (const method of ['get', 'post']) {
          const response = await request[method](`${API}${path}`);
          const label = `${method.toUpperCase()} ${path}`;
          expect.soft(response.status(), label).toBe(404);
          expect
            .soft(response.headers()['content-type'], label)
            .toContain('application/json');
          expect
            .soft((await response.json().catch(() => ({}))).message, label)
            .toBe(NO_ROUTE);
        }
      }

      // Paths outside the API still fall back to the single-page app.
      const page = await request.get('/e2e-no-such-page');
      expect.soft(page.status()).toBe(200);
      expect.soft(page.headers()['content-type']).toContain('text/html');
    });

    await test.step('Builder document schema', async () => {
      const response = await request.get(`${API}/schemas/builder/v1`);
      expect.soft(response.status()).toBe(200);
      expect
        .soft(response.headers()['content-type'])
        .toContain('application/json');

      const schema = await response.json().catch(() => ({}));
      expect.soft(schema.$id).toBe(SCHEMA_URI);
      expect
        .soft(schema.$schema)
        .toBe('https://json-schema.org/draft/2020-12/schema');
      expect.soft(schema.type).toBe('object');
      expect.soft(Object.keys(schema.$defs ?? {})).toContain('device');

      // The builder route is registered ahead of the generic schema route and
      // must not shadow it.
      const topologySchema = await request.get(`${API}/schemas/topology/v1`);
      expect.soft(topologySchema.status()).toBe(200);
    });
  });
});
