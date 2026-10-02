// Builder v2 with its feature flag off. These tests need a server started
// without `--features builder-v2`; run them with E2E_BUILDER_V2=off and
// E2E_BASE_URL pointing at that server. Against any other server they skip.

const { createHash } = require('node:crypto');

const {
  API,
  expect,
  expectNoFatal,
  openConfigs,
  seedConfig,
  test,
  uniqueName,
  waitForApi,
} = require('./builder-support');

test.skip(
  process.env.E2E_BUILDER_V2 !== 'off',
  'needs a server started without --features builder-v2',
);

const DISABLED = 'Builder v2 is not enabled on this phenix server.';
const NO_ROUTE = 'no API route matches this request';

// A Topology published by Builder v2 while the flag was on. The annotation
// is the document reference the publish handler stores, a map of the
// document's digest and id; the document itself lives in the Builder store,
// which this server does not expose. The document's ID is derived from the
// topology's name and the digest, as the server derives it: it drops a
// reference whose ID belongs to another name.
function v2Topology(name) {
  const digest = `sha256:${'0'.repeat(64)}`;
  const reference = {
    digest,
    id: createHash('sha256').update(`${name}\x1f${digest}`).digest('hex'),
  };

  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: {
      name,
      annotations: { 'builder-doc': reference },
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

test('the feature list omits builder-v2 and Builder v2 API routes answer a JSON 404', async ({
  request,
}) => {
  await test.step('GET /features', async () => {
    const response = await request.get('/features');
    expect.soft(response.ok()).toBeTruthy();
    expect
      .soft((await response.json().catch(() => ({}))).features ?? [])
      .not.toContain('builder-v2');
  });

  await test.step('Builder v2 API routes', async () => {
    const calls = [
      ['get', '/builder-v2/drafts'],
      ['post', '/builder-v2/drafts'],
      ['get', '/builder-v2/drafts/global-admin/e2e-missing'],
      ['get', '/builder-v2/documents'],
      ['get', '/builder-v2/sources'],
      ['post', '/builder-v2/generate'],
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
test('opening /builder-v2 sends the user home, whose header offers only the legacy Builder', async ({
  page,
  issues,
}) => {
  const chunks = [];
  page.on('request', (request) => {
    if (
      /\/assets\/BuilderV2-[^/]*\.js$/.test(new URL(request.url()).pathname)
    ) {
      chunks.push(request.url());
    }
  });

  await test.step('the route guard sends the user home with an explanation', async () => {
    const features = page.waitForResponse(
      (response) => new URL(response.url()).pathname === '/features',
    );
    await page.goto('/builder-v2');
    expect.soft((await features).ok()).toBeTruthy();
    await expect(page).toHaveURL(/\/experiments$/, { timeout: 20000 });
    await expect
      .soft(page.getByRole('alert').filter({ hasText: DISABLED }))
      .toBeVisible();
    await expect
      .soft(page.getByRole('heading', { name: 'Builder v2' }))
      .toHaveCount(0);
    await expect.soft(page.getByTestId('builder-canvas')).toHaveCount(0);
  });

  await test.step('the header offers only the legacy Builder', async () => {
    const legacy = page.getByRole('link', { name: 'Builder', exact: true });
    await expect.soft(legacy).toBeVisible({ timeout: 20000 });
    await expect.soft(legacy).toHaveAttribute('href', /\/builder\?token=/);
    await expect.soft(page.getByTestId('nav-builder-v2')).toHaveCount(0);
    await expect
      .soft(page.getByRole('link', { name: /Builder v2/ }))
      .toHaveCount(0);
  });

  // The route guard denies the route before the editor chunk is fetched.
  expect.soft(chunks).toEqual([]);

  expectNoFatal(issues);
});

test('the Builder schema route is absent', async ({ request }) => {
  // The request falls through to the generic /schemas/{kind}/{version},
  // which has no "builder-v2" kind either.
  const response = await request.get(`${API}/schemas/builder-v2/v1`);
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

  const listed = waitForApi(page, 'GET', '/builder/topologies');
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

test('Configs still labels a Builder v2 topology and shows it read-only', async ({
  page,
  request,
  tracker,
  issues,
}, testInfo) => {
  const name = uniqueName(testInfo, 'v2');
  await seedConfig(request, tracker, v2Topology(name));

  await openConfigs(page);
  const row = page.locator('tr', { hasText: name });
  await expect(row.locator('.tag')).toHaveText('builder v2');

  // The name is a button, so the keyboard reaches the read-only view too.
  const fetched = waitForApi(page, 'GET', `/configs/Topology/${name}`);
  await row
    .getByRole('button', { name: `View Topology ${name}` })
    .press('Enter');
  await fetched;
  const viewer = page.locator('.modal.is-active');
  await expect(viewer.locator('.modal-card-title')).toHaveText(
    `Topology/${name}`,
  );
  const text = viewer.locator('textarea');
  // The reference is shown as the nested map it is.
  await expect(text).toHaveValue(
    /\n( +)builder-doc:\n( +)digest: sha256:0{64}\n\2id: [0-9a-f]{64}\n/,
  );
  await expect(text).toHaveValue(/hostname: host-a/);
  await expect(text).not.toBeEditable();

  expectNoFatal(issues);
});

test('Configs edit of a Builder v2 topology explains why it cannot open', async ({
  page,
  request,
  tracker,
}, testInfo) => {
  const name = uniqueName(testInfo, 'v2-edit');
  await seedConfig(request, tracker, v2Topology(name));

  await openConfigs(page);
  const row = page.locator('tr', { hasText: name });
  const fetched = waitForApi(page, 'GET', `/configs/Topology/${name}`);
  await row
    .locator('.b-tooltip')
    .filter({ hasText: 'edit config file' })
    .getByRole('button')
    .click();
  await fetched;

  // The user stays on Configs and is told why the diagram cannot be edited,
  // instead of landing on the home page, in an alert dialog named by its
  // title and described by its message, so a screen reader reads it.
  const dialog = page.getByRole('alertdialog', {
    name: 'Built by Builder v2',
  });
  await expect(dialog).toBeVisible();
  await expect
    .soft(dialog)
    .toHaveAccessibleDescription(
      /Builder v2 is not enabled on this phenix server\./,
    );
  await expect(page).toHaveURL(/\/configs\/$/);
  await dialog.getByRole('button', { name: 'OK' }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator('tr', { hasText: name })).toBeVisible();
  // Focus returns to the button that opened it, not to the page.
  await expect(
    page.getByRole('button', { name: `Edit Topology ${name}` }),
  ).toBeFocused();
});
