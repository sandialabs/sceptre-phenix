// Builder network addressing in the editor: subnets, connect-time addresses,
// Assign addresses and MAC Generate (the rules are in addressing.contract.test.js).

const {
  applyEdits,
  devicesOf,
  expect,
  expectAccessible,
  expectNoFatal,
  fillField,
  labDocument,
  test,
  uniqueName,
} = require('./builder-support');

// A locally administered unicast MAC address.
const LOCAL_MAC = /^[0-9a-f][26ae](:[0-9a-f]{2}){5}$/;

// The interface `index` (from 1) of the selected device in the Inspector's
// Interfaces list.
function interfaceGroup(builder, index, name) {
  return builder.inspector
    .getByRole('group', { name: 'Interfaces', exact: true })
    .getByRole('group', { name: `Interface ${index}: ${name}` });
}

// labDocument with network EXP on `subnet` with `gateway`, each eth0 static
// with no address, except on the `dhcp` devices.
function addressedLab(name, { subnet, gateway, hostnames, dhcp = [] }) {
  const document = labDocument(name, { hostnames });

  Object.assign(document.networks[0], { subnet, gateway });
  for (const node of devicesOf(document)) {
    if (!dhcp.includes(node.device.hostname)) {
      node.device.spec.network.interfaces[0].proto = 'static';
    }
  }

  return document;
}

function addresses(doc) {
  return devicesOf(doc).map((node) => {
    const [eth0] = node.device.spec.network.interfaces;

    return [node.device.hostname, eth0.address, eth0.mask, eth0.gateway];
  });
}

test('a new network gets a subnet, and a device connected to it gets its next free address', async ({
  builder,
  issues,
}) => {
  await builder.open();
  const draft = await builder.createBlank();
  await builder.palette('device').click();
  await builder.palette('switch').click();
  await builder.expectSummary('1 device, 1 switch, 1 network');

  await test.step('the network has a suggested subnet, and takes a gateway', async () => {
    await builder.selectNode('EXP');
    const subnet = builder.inspector.getByLabel('Subnet', { exact: true });
    const gateway = builder.inspector.getByLabel('Gateway', { exact: true });

    await expect.soft(subnet).toHaveValue('10.1.0.0/24');
    await expect.soft(gateway).toHaveValue('');

    // A gateway outside the subnet cannot be applied.
    await fillField(gateway, '10.9.0.1');
    await expect
      .soft(builder.inspector.getByTestId('inspector-error-list'))
      .toHaveText(
        'Gateway must be a host address of the subnet, such as 10.1.0.1 in 10.1.0.0/24',
      );
    await expect.soft(gateway).toHaveAttribute('aria-invalid', 'true');
    await expect
      .soft(builder.inspector.getByTestId('inspector-apply'))
      .toHaveAttribute('aria-disabled', 'true');

    await fillField(gateway, '10.1.0.1');
    await applyEdits(builder);
    await expect.soft(builder).toHaveAnnounced('Updated network EXP');
    await builder.persisted(
      draft,
      (doc) => doc.networks.map(({ subnet: s, gateway: g }) => [s, g]),
      [['10.1.0.0/24', '10.1.0.1']],
      { soft: true },
    );
  });

  await test.step('a connection gives the new static interface the next free address', async () => {
    await builder.connect();
    await builder.expectSummary('1 connection');
    // The gateway's address is not free.
    await expect
      .soft(builder)
      .toHaveAnnounced('Set the address of eth0 of node to 10.1.0.2/24.');

    await builder.selectNode('node');
    const eth0 = interfaceGroup(builder, 1, 'eth0');

    await expect
      .soft(eth0.getByLabel('Interface kind', { exact: true }))
      .toHaveValue('0');
    await expect.soft(eth0.getByLabel(/^Address/)).toHaveValue('10.1.0.2');
    await expect.soft(eth0.getByLabel(/^Mask/)).toHaveValue('24');
    await expect.soft(eth0.getByLabel(/^Gateway/)).toHaveValue('10.1.0.1');
    await builder.persisted(
      draft,
      (doc) => {
        const [iface] = devicesOf(doc)[0].device.spec.network.interfaces;

        return [iface.proto, iface.address, iface.mask, iface.gateway];
      },
      ['static', '10.1.0.2', 24, '10.1.0.1'],
      { soft: true },
    );
  });

  await test.step('a second switch gets the next subnet', async () => {
    await builder.palette('switch').click();
    await builder.expectSummary('2 networks');
    await builder.persisted(
      draft,
      (doc) => doc.networks.map((network) => network.subnet),
      ['10.1.0.0/24', '10.2.0.0/24'],
      { soft: true },
    );
  });

  expectNoFatal(issues);
});

test('with Assign addresses on connect off, a connection gives no address', async ({
  page,
  builder,
  issues,
}) => {
  await builder.open();
  const draft = await builder.createBlank();
  await builder.palette('device').click();
  await builder.palette('switch').click();
  await builder.expectSummary('1 device, 1 switch, 1 network');

  await test.step('Settings turns the setting off', async () => {
    await page.getByTestId('editor-settings').click();
    const settings = page.getByTestId('settings-dialog');
    const toggle = settings.getByRole('switch', {
      name: 'Assign addresses on connect',
    });

    await expect(toggle).toBeChecked();
    await toggle.click();
    await expect(toggle).not.toBeChecked();
    await settings.getByTestId('settings-done').click();
    await expect(settings).toHaveCount(0);
  });

  await test.step('a connection gives the new static interface no address', async () => {
    await builder.connect();
    await builder.expectSummary('1 connection');
    await builder.persisted(
      draft,
      (doc) => {
        const [iface] = devicesOf(doc)[0].device.spec.network.interfaces;

        return [iface.proto, iface.address ?? null];
      },
      ['static', null],
      { soft: true },
    );
  });

  expectNoFatal(issues);
});

test('Assign addresses gives the static interfaces of a network the next free addresses, as one Undo step', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const draft = await builder.seedDraft(
    addressedLab(uniqueName(testInfo, 'assign'), {
      subnet: '192.0.2.0/29',
      gateway: '192.0.2.1',
      hostnames: ['server', 'server-2', 'server-3'],
      dhcp: ['server-3'],
    }),
  );
  await builder.openDraft(draft);
  const none = [
    ['server', undefined, undefined, undefined],
    ['server-2', undefined, undefined, undefined],
    ['server-3', undefined, undefined, undefined],
  ];
  const assigned = [
    ['server', '192.0.2.2', 29, '192.0.2.1'],
    ['server-2', '192.0.2.3', 29, '192.0.2.1'],
    // DHCP gives its address.
    ['server-3', undefined, undefined, undefined],
  ];

  await test.step('in the Inspector of the switch', async () => {
    await builder.selectNode('EXP');
    const section = builder.inspector.getByTestId('inspector-addresses');
    const assign = section.getByRole('button', { name: 'Assign addresses' });

    await expect
      .soft(assign)
      .toHaveAccessibleDescription(
        'Select Assign addresses to give each static interface on this network that has no address the next free address in 192.0.2.0/29.',
      );
    await assign.click();
    await expect
      .soft(builder)
      .toHaveAnnounced('Assigned 2 addresses on network EXP');
    await expect.soft(assign).toBeFocused();
    await builder.persisted(draft, addresses, assigned, { soft: true });
  });

  await test.step('Undo takes all of them back', async () => {
    await builder.toolbar('undo').click();
    await builder.persisted(draft, addresses, none, { soft: true });
  });

  await test.step('in the command palette', async () => {
    await builder.canvas.focus();
    await page.keyboard.press('ControlOrMeta+k');
    await page.keyboard.type('Assign addresses');
    await page.keyboard.press('Enter');
    await page
      .getByTestId('commands-dialog')
      .getByRole('option', { name: /^EXP/ })
      .click();
    await expect
      .soft(builder)
      .toHaveAnnounced('Assigned 2 addresses on network EXP');
    await builder.persisted(draft, addresses, assigned, { soft: true });
  });

  await test.step('a network without a subnet offers no Assign addresses', async () => {
    await builder.selectNode('EXP');
    await fillField(
      builder.inspector.getByLabel('Gateway', { exact: true }),
      '',
    );
    await fillField(
      builder.inspector.getByLabel('Subnet', { exact: true }),
      '',
    );
    await applyEdits(builder);
    await expect.soft(builder).toHaveAnnounced('Updated network EXP');

    const section = builder.inspector.getByTestId('inspector-addresses');

    await expect
      .soft(section.getByRole('button', { name: 'Assign addresses' }))
      .toBeDisabled();
    await expect
      .soft(section)
      .toContainText(
        'This network has no subnet. Apply a Subnet to assign addresses.',
      );
  });

  expectNoFatal(issues);
});

test('Generate puts a random MAC address in an interface, which Apply keeps', async ({
  page,
  builder,
  issues,
}, testInfo) => {
  const draft = await builder.seedDraft(
    labDocument(uniqueName(testInfo, 'mac')),
  );
  await builder.openDraft(draft);
  await builder.selectNode('server');

  const eth0 = interfaceGroup(builder, 1, 'eth0');
  const mac = builder.inspector
    .locator('[data-path="spec.network.interfaces.0.mac"]')
    .getByRole('textbox');
  const generate = eth0.getByRole('button', {
    name: 'Generate a random MAC address',
  });

  await expect.soft(mac).toHaveValue('');
  await generate.click();
  await expect.soft(mac).toHaveValue(LOCAL_MAC);
  await expect.soft(builder).toHaveAnnounced('Generated MAC address ');
  await expect(builder.inspector).toContainText('Unapplied changes');

  // From the keyboard: the button follows the field, and Enter generates
  // another address.
  const first = await mac.inputValue();
  await mac.focus();
  await page.keyboard.press('Tab');
  await expect.soft(generate).toBeFocused();
  await page.keyboard.press('Enter');
  await expect.soft(mac).not.toHaveValue(first);
  await expect.soft(mac).toHaveValue(LOCAL_MAC);

  const generated = await mac.inputValue();
  await applyEdits(builder);
  await expect.soft(builder).toHaveAnnounced('Updated device server');
  await builder.persisted(
    draft,
    (doc) =>
      devicesOf(doc).find((node) => node.device.hostname === 'server').device
        .spec.network.interfaces[0].mac,
    generated,
    { soft: true },
  );

  expectNoFatal(issues);
});

// The Addresses section of a switch and the Generate button of a MAC
// address field, in each theme.
for (const scheme of ['light', 'dark']) {
  test(
    `axe finds no serious violations in a network's addresses and the Generate button in the ${scheme} theme`,
    { tag: '@axe' },
    async ({ page, builder, issues }, testInfo) => {
      await page.emulateMedia({ colorScheme: scheme });
      const draft = await builder.seedDraft(
        addressedLab(uniqueName(testInfo, `addresses-${scheme}`), {
          subnet: '10.1.0.0/24',
          gateway: '10.1.0.1',
        }),
      );
      await builder.openDraft(draft);
      await expect(page.locator('.builder-root')).toHaveAttribute(
        'data-builder-theme',
        scheme,
      );

      await builder.selectNode('EXP');
      await expect(
        builder.inspector.getByTestId('inspector-assign-addresses'),
      ).toBeVisible();
      await expectAccessible(page, {
        include: 'section[aria-labelledby="inspector-title"]',
        soft: true,
        label: `axe on the Inspector of a switch (${scheme})`,
      });

      await builder.selectNode('server');
      await expect(
        builder.inspector.getByTestId('inspector-generate-mac'),
      ).toBeVisible();
      await expectAccessible(page, {
        include: 'section[aria-labelledby="inspector-title"]',
        soft: true,
        label: `axe on the Inspector of a device (${scheme})`,
      });

      expectNoFatal(issues);
    },
  );
}
