// Table controls, on the Configs page: the search box's clear button and the
// Paginate toggle, which shows once a table has more rows than a page holds.
const {
  test: base,
  expect,
  pageDataLoaded,
  roleConfig,
  createConfig,
  deleteConfig,
} = require('./helpers');

const test = base.extend({
  // one Role config more than a page holds, removed afterwards
  roles: async ({ request }, use) => {
    const configs = Array.from({ length: 11 }, (_, i) =>
      roleConfig(`e2e-page-${String(i).padStart(2, '0')}`),
    );
    for (const config of configs) await createConfig(request, config);

    await use(configs.map((config) => config.metadata.name));
    await Promise.all(configs.map((config) => deleteConfig(request, config)));
  },
});

test('search clears, and Paginate starts off, pages and is remembered', async ({
  page,
  roles,
}) => {
  await page.goto('/configs/');
  await pageDataLoaded(page);

  const search = page.getByPlaceholder('Find a Config');
  const clear = page.getByRole('button', { name: 'Clear config filters' });
  const rows = page.locator('tbody tr', { hasText: 'e2e-page-' });
  const paginate = page.getByRole('checkbox', { name: 'Paginate' });
  const nextPage = page.getByRole('button', { name: 'Next page' });

  await expect(clear).toBeHidden();
  await search.fill('e2e-page-');
  await expect(clear).toBeVisible();
  await expect(rows).toHaveCount(roles.length);

  await expect(paginate).not.toBeChecked();
  await page.getByText('Paginate', { exact: true }).click();
  await expect(paginate).toBeChecked();
  await expect(rows).toHaveCount(10);
  await nextPage.click();
  await expect(rows).toHaveCount(roles.length - 10);

  await clear.click();
  await expect(search).toHaveValue('');
  await expect(clear).toBeHidden();

  // the setting is kept in this browser
  await page.reload();
  await pageDataLoaded(page);
  await expect(paginate).toBeChecked();
  await expect(page.locator('tbody tr')).toHaveCount(10);

  await page.getByText('Paginate', { exact: true }).click();
  await expect(paginate).not.toBeChecked();
  await search.fill('e2e-page-');
  await expect(rows).toHaveCount(roles.length);
  await expect(nextPage).toBeHidden();
});
