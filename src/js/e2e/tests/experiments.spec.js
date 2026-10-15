// Experiments without minimega: creating one from a topology through the
// create experiment card, and a stopped experiment's page. The topology and
// the stopped experiment are made through the REST API, and each test removes
// what it made. The server has no disk images, so the stopped experiment's
// disk list is stood in for.
const {
  test: base,
  expect,
  pageDataLoaded,
  expectAccessible,
  createConfig,
} = require('./helpers');

const TOPOLOGY = 'e2e-topology';
const CREATED = 'e2e-created';
const STOPPED = 'e2e-stopped';
const OUTSIDE_IMAGE = '/data/vms/e2e.img';
const OUTSIDE = 'Outside the standard images directory';

const vm = (hostname, address, extra = {}) => ({
  type: 'VirtualMachine',
  general: { hostname, ...extra.general },
  hardware: {
    os_type: 'linux',
    vcpus: 1,
    memory: 512,
    drives: [{ image: extra.image ?? 'e2e.qc2' }],
  },
  network: {
    interfaces: [
      {
        name: 'IF0',
        vlan: 'EXP',
        address,
        mask: 24,
        gateway: '10.0.0.254',
        proto: 'static',
        type: 'ethernet',
      },
    ],
  },
  ...(extra.annotations && { annotations: extra.annotations }),
});

// e2e-vm-a has a `keep` annotation of its own, which a node annotation for
// every VM must not replace
const topologyConfig = {
  apiVersion: 'phenix.sandia.gov/v1',
  kind: 'Topology',
  metadata: { name: TOPOLOGY },
  spec: {
    nodes: [
      vm('e2e-vm-a', '10.0.0.1', {
        general: { description: 'first e2e VM' },
        annotations: { keep: 'own-value' },
      }),
      vm('e2e-vm-b', '10.0.0.2'),
      // an image outside the minimega files directory
      vm('e2e-vm-c', '10.0.0.3', { image: OUTSIDE_IMAGE }),
    ],
  },
};

// Without minimega, deleting an experiment reports an error (minimega clears
// its snapshots and miniccc responses), but the experiment is gone.
async function removeAll(request) {
  for (const name of [CREATED, STOPPED]) {
    await request.delete(`/api/v1/experiments/${name}`);
  }
  await request.delete(`/api/v1/configs/topology/${TOPOLOGY}`);

  const { experiments } = await (
    await request.get('/api/v1/experiments?vms=false')
  ).json();
  const left = experiments.filter((e) => [CREATED, STOPPED].includes(e.name));
  expect(left).toEqual([]);
}

const test = base.extend({
  // the topology to build experiments from, removed afterwards along with the
  // experiments built from it
  topology: async ({ request }, use) => {
    await removeAll(request);
    await createConfig(request, topologyConfig);

    await use(TOPOLOGY);
    await removeAll(request);
  },
});

// The server's disk list for an experiment whose VMs name e2e.qc2 in the
// files directory `filesDir`, and OUTSIDE_IMAGE: e2e.qc2 at the top of the
// files directory and in a folder, and the image outside it.
const diskList = (filesDir) => {
  const disk = (fullPath, relativePath) => ({
    kind: relativePath ? 'VM' : 'Unknown',
    name: fullPath.split('/').pop(),
    fullPath,
    relativePath,
    outsideFilesDir: !relativePath,
    readOnly: !relativePath,
    size: '1.0 MiB',
    virtualSize: '10 GiB',
    experiments: [{ name: STOPPED, running: false }],
    backingImages: [],
    inUse: false,
  });
  return [
    disk(`${filesDir}/e2e.qc2`, 'e2e.qc2'),
    disk(`${filesDir}/win/e2e.qc2`, 'win/e2e.qc2'),
    disk(OUTSIDE_IMAGE, ''),
  ];
};

// Answers the page's disk list requests (GET /api/v1/disks, whatever its
// query) with `disks`.
const standInDisks = (page, disks) =>
  page.route(
    (url) => url.pathname.endsWith('/api/v1/disks'),
    (route) =>
      route.request().method() === 'GET'
        ? route.fulfill({ json: { disks } })
        : route.continue(),
  );

// Adds a custom annotation to one of the card's annotation lists.
async function addCustomAnnotation(page, list, key, value) {
  await list.getByRole('button', { name: 'Add annotation' }).click();
  await page.getByRole('menuitem', { name: /Custom annotation/ }).click();
  const rows = list.getByRole('textbox', { name: / key$/ });
  await rows.last().fill(key);
  await list.getByRole('textbox', { name: `${key} value` }).fill(value);
}

test('create card builds an experiment with experiment and node annotations', async ({
  page,
  request,
  topology,
}) => {
  await page.goto('/experiments');
  await pageDataLoaded(page);

  // an empty list offers its own button
  await page
    .getByRole('button', { name: 'Create a new experiment' })
    .or(page.getByRole('button', { name: 'Create One Now!' }))
    .click();
  const card = page.locator('.create-exp-card');
  await card.getByRole('textbox', { name: 'Experiment Name' }).fill(CREATED);
  await card
    .getByRole('combobox', { name: 'Experiment Topology' })
    .selectOption(topology);

  await card.getByRole('button', { name: 'Advanced Options' }).click();
  const nodeList = card.getByRole('group', {
    name: 'Annotations for Every VM',
  });
  await addCustomAnnotation(page, nodeList, 'keep', 'every-vm');
  await addCustomAnnotation(page, nodeList, 'e2e/note', 'from the card');

  const expList = card.getByRole('group', { name: 'Experiment Annotations' });
  await expList.getByRole('button', { name: 'Add annotation' }).click();
  await page.getByRole('menuitem', { name: /phenix\.workflow\/tags/ }).click();
  await expList
    .getByRole('textbox', { name: 'phenix.workflow/tags value' })
    .fill('suite=e2e');
  await expectAccessible(page, 'create-experiment-modal');

  // phēnix sets the topology annotation itself, so the card refuses it
  const create = card.getByRole('button', { name: 'Create Experiment' });
  await addCustomAnnotation(page, expList, 'topology', 'other');
  await expect(
    expList.getByText('Set by phēnix from the topology'),
  ).toBeVisible();
  await expect(create).toBeDisabled();
  await expList.getByRole('button', { name: 'Remove topology' }).click();

  await create.click();
  await expect(
    page.getByRole('link', { name: CREATED, exact: true }),
  ).toBeVisible({ timeout: 15000 });

  const annotations = async (name) => {
    const vm = await request.get(`/api/v1/experiments/${CREATED}/vms/${name}`);
    expect(vm.ok(), await vm.text()).toBe(true);
    return (await vm.json()).annotations;
  };
  expect(await annotations('e2e-vm-a')).toEqual({
    keep: 'own-value',
    'e2e/note': 'from the card',
  });
  expect(await annotations('e2e-vm-b')).toEqual({
    keep: 'every-vm',
    'e2e/note': 'from the card',
  });

  const config = await request.get(`/api/v1/configs/experiment/${CREATED}`, {
    headers: { Accept: 'application/json' },
  });
  expect(config.ok(), await config.text()).toBe(true);
  expect((await config.json()).metadata.annotations).toMatchObject({
    topology,
    'phenix.workflow/tags': 'suite=e2e',
  });
});

test('stopped experiment page lists, searches and describes its VMs', async ({
  page,
  request,
  topology,
}) => {
  const exp = await request.post('/api/v1/experiments', {
    data: { name: STOPPED, topology },
  });
  expect(exp.ok(), await exp.text()).toBe(true);
  // the files directory the server resolves the topology's e2e.qc2 in
  const { vms } = await (
    await request.get(`/api/v1/experiments/${STOPPED}`)
  ).json();
  const topDisk = vms.find((vm) => vm.name === 'e2e-vm-a').disk;
  const filesDir = topDisk.slice(0, topDisk.lastIndexOf('/'));
  await standInDisks(page, diskList(filesDir));

  await page.goto('/experiments');
  await pageDataLoaded(page);
  const link = page.getByRole('link', { name: STOPPED, exact: true });
  await expect(link).toBeVisible();
  await expectAccessible(page, 'experiments-list');
  await link.click();
  await expect(page).toHaveURL(new RegExp(`/experiment/${STOPPED}$`));
  await pageDataLoaded(page);

  const row = (name) => page.locator('tbody tr', { hasText: name });
  await expect(row('e2e-vm-a')).toBeVisible();
  await expect(row('e2e-vm-b')).toBeVisible();

  // each VM's disk picker offers the images by their path within the files
  // directory, so the two e2e.qc2 images are two options, and those outside
  // it in a group of their own; a VM on one of those shows a warning
  const disk = (name) =>
    page.getByRole('combobox', { name: `Disk for VM ${name}` });
  const warning = (name) => row(name).getByRole('button', { name: OUTSIDE });
  await expect(disk('e2e-vm-a')).toHaveValue(topDisk);
  await expect(disk('e2e-vm-a').locator('option')).toHaveText([
    'e2e.qc2',
    'win/e2e.qc2',
    OUTSIDE_IMAGE,
  ]);
  await expect(disk('e2e-vm-a').locator('optgroup')).toHaveAttribute(
    'label',
    OUTSIDE,
  );
  await expect(warning('e2e-vm-a')).toHaveCount(0);
  await expect(disk('e2e-vm-c')).toHaveValue(OUTSIDE_IMAGE);
  // the warning explains itself on keyboard focus, not only on hover
  const explanation = page.locator('.disk-warning-content');
  await expect(explanation).toBeHidden();
  await warning('e2e-vm-c').focus();
  await expect(explanation).toBeVisible();
  await expect(explanation).toContainText(
    `${OUTSIDE} (${filesDir}). minimega does not copy this image`,
  );
  await page.keyboard.press('Escape');
  await expect(explanation).toBeHidden();

  // the search filters on the server; its clear button shows only while there
  // is something to clear
  const clear = page.getByRole('button', { name: 'Clear VM search' });
  await expect(clear).toBeHidden();
  await page.getByPlaceholder('Find a VM').fill('e2e-vm-b');
  await expect(row('e2e-vm-a')).toBeHidden();
  await expect(row('e2e-vm-b')).toBeVisible();
  await clear.click();
  await expect(row('e2e-vm-a')).toBeVisible();
  await expect(page.getByPlaceholder('Find a VM')).toHaveValue('');
  await expect(clear).toBeHidden();

  // selecting a VM shows the boot buttons; Buefy draws its box over the
  // native checkbox, so click the label as a user does
  const select = page.getByRole('checkbox', { name: 'Select VM e2e-vm-a' });
  await page.locator('label', { has: select }).click();
  await expect(select).toBeChecked();
  await expect(
    page.getByRole('button', { name: 'Set selected VMs to boot' }),
  ).toBeVisible();
  await expectAccessible(page, 'stopped-experiment');

  await row('e2e-vm-a').getByText('e2e-vm-a', { exact: true }).click();
  const info = page.locator('.modal-card', { hasText: 'Description:' });
  await expect(info).toContainText('Description: first e2e VM');
  await expect(info).toContainText('IP: 10.0.0.1');
  await expectAccessible(page, 'vm-info-modal');
  await page.keyboard.press('Escape');
  await expect(info).toBeHidden();

  await page.getByRole('tab', { name: 'Files' }).click();
  await expect(
    page.getByText('This experiment has no files yet'),
  ).toBeVisible();
  await expect(page.getByPlaceholder('Find a File')).toBeVisible();
  await expectAccessible(page, 'experiment-files');
});
