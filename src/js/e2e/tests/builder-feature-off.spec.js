// Builder Beta with its feature flag off. These tests need a server started
// without `--features builder-beta`; run them with E2E_BUILDER_BETA=off and
// E2E_BASE_URL pointing at that server. Against any other server they skip.

const {
  API,
  SCHEMA_URI,
  expect,
  expectNoFatal,
  seedConfig,
  test,
  uniqueName,
  visit,
} = require('./builder-support');

test.skip(
  process.env.E2E_BUILDER_BETA !== 'off',
  'needs a server started without --features builder-beta',
);

const DISABLED = 'Builder Flow is not enabled on this phenix server.';
const NO_ROUTE = 'no API route matches this request';

function apiResponse(page, method, path) {
  return page.waitForResponse(
    (response) =>
      response.request().method() === method &&
      new URL(response.url()).pathname === `${API}${path}`,
  );
}

// A Topology published by Builder Beta while the flag was on. The annotation
// is the document reference the publish handler stores; the document itself
// lives in the Builder store, which this server does not expose.
function betaTopology(name) {
  const reference = {
    id: 'e2e0'.repeat(16),
    digest: `sha256:${'0'.repeat(64)}`,
    size: 1024,
    chunks: 1,
    chunkSize: 524288,
    schema: SCHEMA_URI,
    draftId: '00000000-0000-4000-8000-000000000000',
    snapshotId: '00000000-0000-4000-8000-000000000001',
    createdAt: '2026-01-01T00:00:00Z',
    createdBy: 'global-admin',
  };

  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: {
      name,
      annotations: { 'builder-doc': JSON.stringify(reference) },
    },
    spec: {
      nodes: [
        {
          type: 'VirtualMachine',
          general: { hostname: 'host-a' },
          hardware: {
            os_type: 'linux',
            vcpus: 1,
            memory: 1024,
            drives: [{ image: 'ubuntu.qc2' }],
          },
          network: { interfaces: [] },
        },
      ],
    },
  };
}

async function openConfigs(page) {
  await visit(page, '/configs/');
  await expect(page.locator('table')).toBeVisible({ timeout: 20000 });
}

test('the feature list omits builder-beta and Builder Beta API routes answer a JSON 404', async ({
  request,
}) => {
  await test.step('GET /features', async () => {
    const response = await request.get('/features');
    expect.soft(response.ok()).toBeTruthy();
    expect
      .soft((await response.json().catch(() => ({}))).features ?? [])
      .not.toContain('builder-beta');
  });

  await test.step('Builder Beta API routes', async () => {
    const calls = [
      ['get', '/builder/drafts'],
      ['post', '/builder/drafts'],
      ['get', '/builder/drafts/global-admin/e2e-missing'],
      ['get', '/builder/documents'],
      ['get', '/builder/sources'],
      ['post', '/builder/generate'],
    ];

    for (const [method, path] of calls) {
      const response = await request[method](`${API}${path}`, {
        ...(method === 'post' ? { data: {} } : {}),
      });
      const label = `${method.toUpperCase()} ${path}`;
      expect.soft(response.status(), label).toBe(404);
      expect
        .soft(response.headers()['content-type'], label)
        .toContain('application/json');
      expect
        .soft((await response.json().catch(() => ({}))).message, label)
        .toBe(NO_ROUTE);
    }
  });
});

// The route guard and the header read the same single-flight /features fetch
// from the store, so once the guard has redirected, the header has decided.
test('opening /builder-beta sends the user home, whose header offers only the legacy Builder', async ({
  page,
  issues,
}) => {
  const chunks = [];
  page.on('request', (request) => {
    if (
      /\/assets\/BuilderBeta-[^/]*\.js$/.test(new URL(request.url()).pathname)
    ) {
      chunks.push(request.url());
    }
  });

  await test.step('the route guard sends the user home with an explanation', async () => {
    const features = page.waitForResponse(
      (response) => new URL(response.url()).pathname === '/features',
    );
    await page.goto('/builder-beta');
    expect.soft((await features).ok()).toBeTruthy();
    await expect(page).toHaveURL(/\/experiments$/, { timeout: 20000 });
    await expect
      .soft(page.getByRole('alert').filter({ hasText: DISABLED }))
      .toBeVisible();
    await expect
      .soft(page.getByRole('heading', { name: 'Builder Flow' }))
      .toHaveCount(0);
    await expect.soft(page.getByTestId('builder-canvas')).toHaveCount(0);
  });

  await test.step('the header offers only the legacy Builder', async () => {
    const legacy = page.getByRole('link', { name: 'Builder', exact: true });
    await expect.soft(legacy).toBeVisible({ timeout: 20000 });
    await expect.soft(legacy).toHaveAttribute('href', /\/builder\?token=/);
    await expect.soft(page.getByTestId('nav-builder-beta')).toHaveCount(0);
    await expect
      .soft(page.getByRole('link', { name: /Builder Flow/ }))
      .toHaveCount(0);
  });

  // The route guard denies the route before the editor chunk is fetched.
  expect.soft(chunks).toEqual([]);

  expectNoFatal(issues);
});

test('the Builder schema route is absent', async ({ request }) => {
  // The request falls through to the generic /schemas/{kind}/{version},
  // which has no "builder" kind either.
  const response = await request.get(`${API}/schemas/builder/v1`);
  expect(response.status(), await response.text()).toBe(404);
});

test('legacy Builder still opens and lists legacy diagrams', async ({
  page,
  request,
  tracker,
  issues,
}, testInfo) => {
  const legacy = uniqueName(testInfo, 'legacy');
  await seedConfig(request, tracker, {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: {
      name: legacy,
      annotations: { 'builder-xml': '<mxGraphModel/>' },
    },
    spec: { nodes: [] },
  });

  await page.goto('/builder');
  await expect(page.locator('#editor .geDiagramContainer')).toBeVisible({
    timeout: 20000,
  });
  await expect(page.locator('.geSidebarContainer .geTitle')).toContainText([
    'VM Networking',
    'VM Hosts',
    'External Networking',
    'External Hosts',
  ]);

  const listed = apiResponse(page, 'GET', '/builder/topologies');
  await page.locator('.geMenubar a.geItem', { hasText: 'File' }).click();
  await page
    .locator('tr.mxPopupMenuItem', { hasText: 'Import from phēnix' })
    .click();
  expect((await listed).ok()).toBeTruthy();
  await expect(
    page.locator(`#topology-name option[value="${legacy}"]`),
  ).toHaveCount(1);

  expectNoFatal(issues);
});

test('Configs still labels a Builder Beta topology and shows it read-only', async ({
  page,
  request,
  tracker,
  issues,
}, testInfo) => {
  const name = uniqueName(testInfo, 'beta');
  await seedConfig(request, tracker, betaTopology(name));

  await openConfigs(page);
  const row = page.locator('tr', { hasText: name });
  await expect(row.locator('.tag')).toHaveText('builder flow');

  const fetched = apiResponse(page, 'GET', `/configs/Topology/${name}`);
  await row.getByText(name, { exact: true }).click();
  await fetched;
  const viewer = page.locator('.modal.is-active');
  await expect(viewer.locator('.modal-card-title')).toHaveText(
    `Topology/${name}`,
  );
  const text = viewer.locator('textarea');
  await expect(text).toHaveValue(/builder-doc/);
  await expect(text).toHaveValue(/hostname: host-a/);
  await expect(text).not.toBeEditable();

  expectNoFatal(issues);
});

test('Configs edit of a Builder Beta topology explains why it cannot open', async ({
  page,
  request,
  tracker,
}, testInfo) => {
  const name = uniqueName(testInfo, 'beta-edit');
  await seedConfig(request, tracker, betaTopology(name));

  await openConfigs(page);
  const row = page.locator('tr', { hasText: name });
  const fetched = apiResponse(page, 'GET', `/configs/Topology/${name}`);
  await row
    .locator('.b-tooltip')
    .filter({ hasText: 'edit config file' })
    .getByRole('button')
    .click();
  await fetched;

  // The user stays on Configs and is told why the diagram cannot be edited,
  // instead of landing on the home page.
  const dialog = page
    .locator('.modal.is-active')
    .filter({ hasText: /Builder Flow/ });
  await expect(dialog).toBeVisible();
  await expect
    .soft(dialog)
    .toContainText('Builder Flow is not enabled on this phenix server.');
  await expect(page).toHaveURL(/\/configs\/$/);
  await dialog.getByRole('button', { name: 'OK' }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator('tr', { hasText: name })).toBeVisible();
  // Focus returns to the button that opened it, not to the page.
  await expect(
    page.locator(`[data-config-edit="Topology/${name}"]`),
  ).toBeFocused();
});
