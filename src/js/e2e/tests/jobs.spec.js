// Background jobs against a server with no minimega: the jobs API answers
// for an empty registry, and the header's jobs menu renders and passes axe,
// empty and with jobs the server's list is stood in for.
const { test, expect, expectAccessible } = require('./helpers');

test('the jobs API lists no jobs and refuses unknown ones', async ({
  request,
}) => {
  const list = await request.get('/api/v1/jobs');
  expect(list.status()).toBe(200);
  expect(await list.json()).toEqual({ jobs: [] });

  for (const [method, path] of [
    ['get', '/api/v1/jobs/j0000000000000000'],
    ['post', '/api/v1/jobs/j0000000000000000/cancel'],
    ['delete', '/api/v1/jobs/j0000000000000000'],
    ['get', '/api/v1/jobs/j0000000000000000/artifact'],
  ]) {
    const resp = await request[method](path);
    expect(resp.status(), `${method} ${path}`).toBe(404);
  }
});

test('the header jobs menu opens empty and is accessible', async ({ page }) => {
  await page.goto('/experiments');

  const menu = page.getByRole('button', {
    name: 'Background jobs (0 jobs running)',
  });
  await expect(menu).toBeVisible();
  await menu.click();
  await expect(page.getByText('No background jobs')).toBeVisible();

  await expectAccessible(page, 'jobs-menu-empty');
});

test('the header jobs menu shows running and finished jobs', async ({
  page,
}) => {
  const job = (id, fields) => ({
    id,
    kind: 'vms.action',
    owner: 'global-admin',
    experiment: 'e2e',
    title: 'Restart 3 VMs',
    status: 'running',
    cancelable: true,
    progress: { done: 1, total: 3 },
    items: [],
    created: '2026-10-14T15:00:00Z',
    updated: '2026-10-14T15:00:01Z',
    revision: 2,
    ...fields,
  });
  const jobs = [
    job('j0000000000000001', {}),
    job('j0000000000000002', {
      kind: 'files.archive',
      title: 'Archive 2 files of e2e',
      status: 'partial',
      cancelable: true,
      progress: { done: 2, total: 2, bytes: 10, bytesTotal: 10 },
      artifact: {
        name: 'e2e-files-incomplete.zip',
        size: 10,
        incomplete: true,
      },
      created: '2026-10-14T14:00:00Z',
    }),
  ];
  await page.route(
    (url) => url.pathname.endsWith('/api/v1/jobs'),
    (route) => route.fulfill({ json: { jobs } }),
  );

  await page.goto('/experiments');
  const menu = page.getByRole('button', {
    name: 'Background jobs (1 job running)',
  });
  await expect(menu).toBeVisible();
  await menu.click();

  await expect(page.getByText('Restart 3 VMs')).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Cancel Restart 3 VMs' }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Download incomplete archive' }),
  ).toBeVisible();

  await expectAccessible(page, 'jobs-menu');
});
