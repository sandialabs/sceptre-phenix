// Browser tests for the Builder Inspector (working copy with Apply and Cancel).
// Fields are found by label inside each spec group's fieldset.

const crypto = require('node:crypto');

const {
  applyEdits,
  blankDocument,
  contrast,
  expect,
  expectAccessible,
  expectDetail,
  expectNoFatal,
  fillField,
  labDocument,
  nextSecond,
  publishTopology,
  specGroup,
  test,
  uniqueName,
} = require('./builder-support');

// Expect a persisted server state within this time. Autosave uploads each
// commit asynchronously after Apply.
const PERSIST = { timeout: 20000 };

// The route a draft's saves are sent to.
const SNAPSHOTS = '**/api/v1/builder/drafts/*/snapshots';

function subject(builder) {
  return builder.inspector.locator('.builder-inspector__subject');
}

// The top-level "Hostname" (the device name) is rendered before the spec, so
// it is the first of the two Hostname fields.
function deviceHostname(builder) {
  return builder.inspector.getByLabel(/^Hostname/).first();
}

// Each connection point's name and network, without its buttons.
function interfaceRows(builder) {
  return builder.inspector.locator(
    '.builder-inspector__ifaces li .builder-inspector__iface-name',
  );
}

// The device `node` on the canvas, its interface handles (without the
// new-interface handle every device has) and its Connection points rows.
async function connectionPoints(page, builder) {
  const node = builder.node('node', 'device');
  const id = await node.getAttribute('data-node-id');

  return {
    node,
    handles: page.locator(
      `.vue-flow__handle[data-nodeid="${id}"]:not([data-handleid="new-interface"])`,
    ),
    rows: interfaceRows(builder),
  };
}

// Memory and VCPUs are `oneOf: [integer, string]` to phenix, and number
// fields named by the field in the Inspector.
async function setNumber(builder, label, value) {
  await fillField(
    specGroup(builder, 'Hardware').getByLabel(label, { exact: true }),
    String(value),
  );
}

// Adds palette items and waits until the server has them, so no autosave is
// in flight when the tracker deletes the draft.
async function addItems(builder, draft, ...items) {
  for (const item of items) {
    await builder.palette(item).click();
  }
  await expect
    .poll(
      async () => (await builder.serverDocument(draft)).nodes.length,
      PERSIST,
    )
    .toBe(items.length);
}

// Opens a blank draft and adds palette items.
async function newDraft(builder, ...items) {
  await builder.open();
  const draft = await builder.createBlank();
  await addItems(builder, draft, ...items);

  return draft;
}

// Connects the first device to the first switch with a new interface.
async function connectFirst(builder, draft) {
  await builder.connect();
  await builder.expectSummary('1 connection');
  await expect
    .poll(async () => (await builder.serverDocument(draft)).edges, PERSIST)
    .toHaveLength(1);
}

function nodeOf(doc, kind) {
  return doc.nodes.find((node) => node.kind === kind);
}

// A node spec as phenix stores it in an experiment: every unset field is
// null or empty. Import removes them, but a draft file or an older draft can
// still hold such a spec.
function experimentSpec(hostname) {
  return {
    advanced: null,
    annotations: null,
    commands: null,
    delay: null,
    deletions: [],
    external: null,
    general: {
      description: '',
      do_not_boot: null,
      hostname,
      snapshot: null,
      vm_type: 'kvm',
    },
    hardware: {
      cpu: '',
      drives: [
        {
          cache_mode: '',
          image: 'bennu.qc2',
          inject_partition: null,
          interface: '',
        },
      ],
      memory: 512,
      os_type: 'linux',
      vcpus: 1,
    },
    injections: [],
    labels: null,
    network: {
      interfaces: [
        {
          address: '10.0.0.1',
          autostart: false,
          baud_rate: 0,
          bridge: '',
          device: '',
          dns: null,
          driver: '',
          gateway: '',
          mac: '',
          mask: 24,
          mtu: 0,
          name: 'eth0',
          proto: 'static',
          qinq: false,
          ruleset_in: '',
          ruleset_out: '',
          type: 'ethernet',
          udp_port: 0,
          vlan: 'EXP',
        },
      ],
      nat: [],
      ospf: null,
      routes: [],
      rulesets: [],
    },
    overrides: null,
    type: 'VirtualMachine',
  };
}

// Answers GET /disks with these images (the test server lists none). Call
// before the editor opens.
async function mockDisks(page, names) {
  await page.route('**/api/v1/disks', (route) =>
    route.fulfill({
      json: { disks: names.map((name) => ({ kind: 'VM', name })) },
    }),
  );
}

// Pans until `element` is out of view, keeping the pointer in the window:
// Firefox loses the release of a drag that ends outside it.
async function panOutOfView(page, element) {
  const pane = page.locator('.vue-flow__pane');

  for (let attempt = 0; attempt < 5; attempt += 1) {
    const drag = await pane.evaluate((surface) => {
      const rect = surface.getBoundingClientRect();

      for (const fx of [0.8, 0.5, 0.2]) {
        for (const fy of [0.8, 0.5, 0.2]) {
          const x = rect.left + rect.width * fx;
          const y = rect.top + rect.height * fy;

          if (document.elementFromPoint(x, y) === surface) {
            return { x, y, toX: rect.left + 5, toY: rect.top + 5 };
          }
        }
      }

      return null;
    });

    expect(drag, 'an empty spot on the canvas').toBeTruthy();
    await page.mouse.move(drag.x, drag.y);
    await page.mouse.down();
    await page.mouse.move(drag.toX, drag.toY, { steps: 8 });
    await page.mouse.up();

    if (!(await isInViewport(element))) {
      return;
    }
  }

  await expect(element).not.toBeInViewport();
}

async function isInViewport(element) {
  try {
    await expect(element).toBeInViewport({ timeout: 500 });

    return true;
  } catch {
    return false;
  }
}

// The selected device's form: its fields and its Apply and Cancel.
function deviceFields(builder) {
  const general = specGroup(builder, 'General');
  const hardware = specGroup(builder, 'Hardware');

  return {
    hostname: deviceHostname(builder),
    apply: builder.inspector.getByTestId('inspector-apply'),
    cancel: builder.inspector.getByTestId('inspector-cancel'),
    errors: builder.inspector.getByTestId('inspector-errors'),
    hardware,
    osType: hardware.getByLabel(/^OS type/),
    image: hardware.getByLabel(/^Image/),
    specHostname: general.getByLabel('Node hostname'),
    snapshot: general.getByLabel('Snapshot', { exact: true }),
  };
}

// web01 as the device form test applies it: a Windows Router, which phenix
// does not configure as a router, with a second drive, data.qc2, that the
// server lacks, and no interfaces.
function appliedDeviceDocument(name) {
  return blankDocument(name, {
    nodes: [
      {
        id: crypto.randomUUID(),
        kind: 'device',
        label: 'web01',
        position: { x: 0, y: 0 },
        device: {
          hostname: 'web01',
          spec: {
            type: 'Router',
            general: {
              hostname: 'web01',
              description: 'Front end web server',
              vm_type: 'kvm',
              snapshot: false,
            },
            hardware: {
              os_type: 'windows',
              memory: 4096,
              vcpus: 2,
              drives: [{ image: 'win10.qc2' }, { image: 'data.qc2' }],
            },
            network: { interfaces: [] },
          },
          interfaces: [],
        },
      },
    ],
  });
}

// Device `node` with eth0 (manual) connected to switch EXP, as Add
// connection point and Add connection leave it.
function wiredDocument(name) {
  const id = () => crypto.randomUUID();
  const network = { id: id(), name: 'EXP' };
  const handle = { id: id(), name: 'eth0', index: 0 };
  const device = {
    id: id(),
    kind: 'device',
    label: 'node',
    position: { x: 0, y: 0 },
    device: {
      hostname: 'node',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: 'node', description: '', vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
        network: {
          interfaces: [
            { name: 'eth0', type: 'ethernet', proto: 'manual', vlan: 'EXP' },
          ],
        },
      },
      interfaces: [handle],
    },
  };
  const sw = {
    id: id(),
    kind: 'switch',
    label: 'EXP',
    position: { x: 0, y: 320 },
    switch: { networkId: network.id },
  };

  return blankDocument(name, {
    nodes: [device, sw],
    networks: [network],
    edges: [
      {
        id: id(),
        sourceNodeId: device.id,
        sourceHandleId: handle.id,
        targetNodeId: sw.id,
        networkId: network.id,
      },
    ],
  });
}

test.describe('Builder inspector', () => {
  // Each step checks one behavior. Checks that no later step acts on are
  // soft, so a failure in one step does not hide the steps after it.
  test('device fields describe themselves in tooltips, and Cancel discards unapplied edits', async ({
    page,
    builder,
    issues,
  }) => {
    await mockDisks(page, ['ubuntu.qc2', 'win10.qc2']);
    const draft = await newDraft(builder, 'device');
    await builder.selectNode('node');
    const { hostname, apply, cancel, osType, snapshot } = deviceFields(builder);

    // A field's description is a tooltip on its label, read once as its
    // description. The tooltip's bold path line is for the eye only.
    await test.step('field descriptions are tooltips on hover and keyboard focus', async () => {
      const whole = builder.inspector.getByTestId('inspector-tooltip');
      const tip = whole.getByTestId('inspector-tooltip-text');
      const path = whole.getByTestId('inspector-tooltip-path');
      const label = specGroup(builder, 'General').locator('label', {
        hasText: /^Snapshot$/,
      });
      const text =
        /^Run the VM on a copy-on-write snapshot of its first drive\b/;

      await expect.soft(whole).toHaveCount(0);
      await expect.soft(snapshot).toHaveAccessibleDescription(text);
      await label.hover();
      await expect(tip).toHaveText(text);
      await expect.soft(path).toHaveText('general.snapshot');
      await expect.soft(path).toHaveCSS('font-weight', '700');
      await expect.soft(path).toHaveCSS('display', 'block');
      expect
        .soft((await path.boundingBox()).y)
        .toBeLessThan((await tip.boundingBox()).y);
      await expect.soft(whole).toHaveAttribute('aria-hidden', 'true');

      // Beside the Inspector, over the canvas, where it covers no field.
      // The pointer can move onto it, and Escape closes it.
      const box = await whole.boundingBox();
      expect
        .soft(box.x + box.width)
        .toBeLessThanOrEqual((await label.boundingBox()).x);
      await page.mouse.move(box.x + 10, box.y + box.height / 2, {
        steps: 8,
      });
      await expect.soft(tip).toBeVisible();
      await page.keyboard.press('Escape');
      await expect.soft(whole).toHaveCount(0);

      // Keyboard focus shows it too, and Escape leaves focus where it is. Tab
      // comes to Snapshot from Node hostname.
      await specGroup(builder, 'General').getByLabel('Node hostname').focus();
      await page.keyboard.press('Tab');
      await expect.soft(snapshot).toBeFocused();
      await expect.soft(tip).toHaveText(text);
      await page.keyboard.press('Escape');
      await expect.soft(whole).toHaveCount(0);
      await expect.soft(snapshot).toBeFocused();

      // At phone width the tooltip sits above its label. Moving the pointer
      // onto it keeps it.
      const viewport = page.viewportSize();
      await page.setViewportSize({ width: 320, height: 640 });
      await label.scrollIntoViewIfNeeded();
      await label.hover();
      await expect(tip).toHaveText(text);
      const above = await whole.boundingBox();
      await page.mouse.move(above.x + 10, above.y + 4, { steps: 8 });
      await expect.soft(tip).toHaveText(text);
      await page.keyboard.press('Escape');

      // With the pointer left there, each field Tab reaches shows its own
      // tooltip (a list's button shows the list's description).
      await snapshot.focus();
      for (let step = 0; step < 4; step += 1) {
        await page.keyboard.press('Tab');
        const shown = await page.evaluate(() => {
          const field = document.activeElement.closest('[aria-describedby]');
          const own = (field?.getAttribute('aria-describedby') || '')
            .split(' ')
            .map((id) => document.getElementById(id)?.textContent.trim())
            .join(' ');
          const tooltip = document.querySelector(
            '[data-testid="inspector-tooltip-text"]',
          );

          return { own, tip: tooltip?.textContent.trim() || '' };
        });
        expect.soft(shown.own).toContain(shown.tip);
      }
      await page.setViewportSize(viewport);
    });

    await test.step('Cancel discards unapplied edits and announces it', async () => {
      // With nothing to apply the form ends with its fields: no Apply, no
      // Cancel and no state line.
      await expect.soft(apply).toHaveCount(0);
      await expect.soft(cancel).toHaveCount(0);
      await expect
        .soft(builder.inspector.locator('.builder-inspector__state'))
        .toHaveCount(0);

      // They appear with the first keystroke and go when the text is typed
      // back, also once the field is left.
      await hostname.pressSequentially('x');
      await expect.soft(apply).toBeEnabled();
      await expect.soft(cancel).toBeVisible();
      // In a bar that sticks to the bottom of the panel, in view from the
      // top of the long form.
      await expect.soft(apply).toBeInViewport();
      await expect
        .soft(builder.inspector.getByTestId('inspector-actions'))
        .toContainText('Unapplied changes');
      await hostname.press('Backspace');
      await hostname.blur();
      await expect.soft(apply).toHaveCount(0);
      await expect.soft(cancel).toHaveCount(0);

      // A committed change typed back is not applied: Apply says so
      // instead of adding an Undo step for an unchanged device.
      await fillField(hostname, 'scratch');
      await hostname.fill('node');
      await hostname.press('Enter');
      await expect.soft(builder).toHaveAnnounced('No changes to apply.');
      await expect.soft(apply).toHaveCount(0);
      await expect.soft(hostname).toBeFocused();

      // Clicked straight from the field, Apply and Cancel stay for the click,
      // and focus returns to the field.
      for (const button of [apply, cancel]) {
        await fillField(hostname, 'scratch');
        await hostname.fill('node');
        await button.click();
        await expect.soft(apply).toHaveCount(0);
        await expect.soft(hostname).toBeFocused();
      }
      await expect.soft(builder).toHaveAnnounced('No changes to discard.');

      await fillField(hostname, 'scratch');
      await osType.selectOption('rhel');
      await expect.soft(apply).toBeEnabled();
      await expect(cancel).toBeEnabled();
      await expect.soft(builder).toHaveAnnounced('Unapplied changes.');

      await cancel.press('Enter');

      await expect
        .soft(builder)
        .toHaveAnnounced('Discarded unapplied changes.');
      // Cancel goes with the changes, and focus returns to the field
      // edited last rather than falling to <body>.
      await expect.soft(cancel).toHaveCount(0);
      await expect.soft(apply).toHaveCount(0);
      await expect.soft(osType).toBeFocused();
      await expect.soft(hostname).toHaveValue('node');
      await expect.soft(osType).toHaveValue('linux');
      await expect
        .soft(builder.inspector)
        .not.toContainText(/No changes|Unapplied changes/);
      await expect.soft(subject(builder)).toHaveText(/^\s*Device node\b/);

      await builder.waitSaved();
      const device = nodeOf(await builder.serverDocument(draft), 'device');
      expect.soft(device.device.hostname).toBe('node');
      expect.soft(device.device.spec.hardware.os_type).toBe('linux');
    });

    expectNoFatal(issues);
  });

  test(
    'invalid device fields are flagged and block Apply, and fixed and edited fields apply',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      await mockDisks(page, ['ubuntu.qc2', 'win10.qc2']);
      const draft = await newDraft(builder, 'device');
      await builder.selectNode('node');
      const {
        hostname,
        apply,
        cancel,
        errors,
        hardware,
        osType,
        image,
        specHostname,
        snapshot,
      } = deviceFields(builder);

      await test.step('invalid fields are flagged, described and listed, and block Apply', async () => {
        await fillField(hostname, 'bad host');
        await fillField(image, '');

        await expect
          .soft(errors)
          .toHaveText(
            '2 fields need attention before you can apply these changes.',
          );
        // Each error names its field in plain language, is tied to the
        // field, and marks it invalid.
        await expect.soft(hostname).toHaveAttribute('aria-invalid', 'true');
        await expect
          .soft(hostname)
          .toHaveAccessibleDescription(/^Hostname cannot contain spaces\b/);
        // An emptied field removes its value, so the drive's image is missing.
        await expect
          .soft(image)
          .toHaveAccessibleDescription(/^Image must have a value\b/);
        await expect
          .soft(builder.inspector)
          .toContainText('Fix the fields marked with errors');
        await expect.soft(apply).toBeDisabled();

        // Submitting the form with Enter must not apply the invalid value.
        await hostname.press('Enter');
        await expect.soft(subject(builder)).toHaveText(/^\s*Device node\b/);

        // The summary lists each field. An entry moves focus to its field.
        await builder.inspector
          .getByTestId('inspector-error-list')
          .getByRole('button', { name: 'Drive 1: Image must have a value' })
          .click();
        await expect.soft(image).toBeFocused();
      });

      await test.step('fixing the hostname and editing fields applies them', async () => {
        await fillField(hostname, 'web01');
        await fillField(image, 'ubuntu.qc2');
        await expect.soft(errors).toBeHidden();
        await expect.soft(hostname).not.toHaveAttribute('aria-invalid');
        await expect.soft(apply).toBeEnabled();

        await osType.selectOption('windows');

        // The image field suggests the server's disk images as it is typed
        // in: a combobox with list autocomplete (WAI-ARIA APG).
        const imageBox = hardware.getByRole('combobox', { name: /^Image/ });
        const suggestions = hardware
          .getByRole('listbox', { name: 'Suggestions for Image' })
          .getByRole('option');
        await imageBox.fill('WIN');
        await expect.soft(imageBox).toHaveAttribute('aria-expanded', 'true');
        await expect.soft(suggestions).toHaveText(['win10.qc2']);
        await imageBox.press('ArrowDown');
        await expect
          .soft(suggestions.first())
          .toHaveAttribute('aria-selected', 'true');
        await expect
          .soft(imageBox)
          .toHaveAttribute(
            'aria-activedescendant',
            await suggestions.first().getAttribute('id'),
          );
        await imageBox.press('Enter');
        await expect.soft(imageBox).toHaveValue('win10.qc2');
        await expect.soft(imageBox).toHaveAttribute('aria-expanded', 'false');
        await expect.soft(imageBox).toBeFocused();
        await imageBox.press('Alt+ArrowDown');
        await expect.soft(imageBox).toHaveAttribute('aria-expanded', 'true');
        await imageBox.press('Escape');
        await expect.soft(imageBox).toHaveAttribute('aria-expanded', 'false');
        await expect(imageBox).toHaveValue('win10.qc2');

        // Snapshot is on until turned off, as phenix runs a VM.
        await expect.soft(snapshot).toBeChecked();
        await snapshot.click();
        await expect.soft(snapshot).not.toBeChecked();
        // Off, its Default button turns it on again and gives it focus.
        const snapshotReset = builder.inspector
          .locator('[data-path="spec.general.snapshot"]')
          .getByTestId('inspector-field-reset-default');
        await expect
          .soft(snapshotReset)
          .toHaveAccessibleName('Reset Snapshot to the default, on');
        await snapshotReset.click();
        await expect.soft(snapshot).toBeChecked();
        await expect.soft(snapshot).toBeFocused();
        await expect.soft(snapshotReset).toHaveCount(0);
        await snapshot.click();
        await expect.soft(snapshot).not.toBeChecked();

        // Add drive is announced and moves focus to the new drive's image.
        const drives = hardware
          .locator('fieldset.array-list')
          .filter({ has: page.locator('legend', { hasText: 'Drives' }) });
        // Unset, a choice names the value it comes to, marked as the
        // default as a text field's is.
        const cacheMode = drives
          .getByRole('group', { name: /^Drive 1\b/ })
          .getByLabel('Cache mode', { exact: true });
        await expect
          .soft(cacheMode.locator('option:checked'))
          .toHaveText('Default (writeback)');
        await expect
          .soft(cacheMode)
          .toHaveAccessibleDescription(
            /\. Default: The default, used while the field is empty\.$/,
          );
        await expect
          .soft(
            drives
              .getByRole('group', { name: /^Drive 1\b/ })
              .locator('.control > label'),
          )
          .toHaveText([
            /^Image/,
            /^Inject partition/,
            /^Cache mode/,
            /^Interface/,
          ]);
        await drives.getByRole('button', { name: 'Add drive' }).click();
        const second = drives.getByRole('group', { name: 'Drive 2' });
        await expect.soft(second.getByLabel(/^Image/)).toBeFocused();
        await expect.soft(builder).toHaveAnnounced('Added drive 2.');
        await expect
          .soft(second.getByRole('button', { name: 'Remove drive 2' }))
          .toBeEnabled();
        await fillField(second.getByLabel(/^Image/), 'data.qc2');

        // Memory and VCPUs are number fields. Unset or emptied, each shows
        // phenix's default, marked as such and not stored.
        const memory = hardware.getByLabel('Memory', { exact: true });
        const vcpus = hardware.getByLabel('VCPUs', { exact: true });
        const memoryDefault = hardware
          .locator('[data-path="spec.hardware.memory"]')
          .getByTestId('inspector-field-default');
        await expect.soft(memory).toHaveRole('spinbutton');
        await expect.soft(memory).toHaveAttribute('inputmode', 'numeric');
        await expect.soft(memory).toHaveValue('512');
        await expect.soft(memoryDefault).toHaveText(/^Default/);
        await expect
          .soft(memory)
          .toHaveAccessibleDescription(
            /\. Default: phenix uses this value while the field is empty\.$/,
          );
        await expect.soft(vcpus).toHaveValue('1');
        await expect
          .soft(hardware.getByRole('combobox', { name: /^(Memory|VCPUs)\b/ }))
          .toHaveCount(0);
        // A click into a field showing its default selects it, so what is
        // typed replaces it.
        await memory.click();
        await page.keyboard.type('2048');
        await expect.soft(memory).toHaveValue('2048');
        await setNumber(builder, 'Memory', 4096);
        await expect.soft(memoryDefault).toHaveCount(0);
        // Its Default button sets it back to 512 and gives it focus.
        const memoryReset = hardware
          .locator('[data-path="spec.hardware.memory"]')
          .getByTestId('inspector-field-reset-default');
        await expect
          .soft(memoryReset)
          .toHaveAccessibleName('Reset Memory to the default, 512');
        await memoryReset.click();
        await expect.soft(memory).toHaveValue('512');
        await expect.soft(memory).toBeFocused();
        await expect.soft(memoryDefault).toBeVisible();
        await expect.soft(memoryReset).toHaveCount(0);
        await setNumber(builder, 'Memory', 4096);
        await expect.soft(memoryDefault).toHaveCount(0);
        // The field the working copy changed is marked until applied.
        await expect
          .soft(memory)
          .toHaveAccessibleDescription(/Changed, not applied yet\.$/);

        // Enter in a field showing its default applies the other edits,
        // stores no value and keeps focus.
        await fillField(
          specGroup(builder, 'General').getByLabel(/^Description/),
          'Front end web server',
        );
        await expect(builder.inspector).toContainText('Unapplied changes');
        await vcpus.press('Enter');
        await expect(apply).toHaveCount(0);
        await expect.soft(builder).toHaveAnnounced('Updated device web01');
        await expect.soft(vcpus).toBeFocused();
        await expect.soft(vcpus).toHaveValue('1');
        // A whole-number field takes no fraction or exponent.
        for (const typed of ['1.5', '1e3']) {
          await setNumber(builder, 'VCPUs', typed);
          await expect
            .soft(vcpus)
            .toHaveAccessibleDescription(/^VCPUs must be a whole number\b/);
          await expect.soft(vcpus).toHaveAttribute('aria-invalid', 'true');
          await expect.soft(apply).toBeDisabled();
        }
        // Spaces around a whole number are dropped, and the arrow keys step
        // it.
        await setNumber(builder, 'VCPUs', ' 3 ');
        await expect.soft(vcpus).toHaveValue('3');
        await expect.soft(vcpus).not.toHaveAttribute('aria-invalid');
        await vcpus.press('ArrowDown');
        await expect.soft(vcpus).toHaveValue('2');
        await expect.soft(vcpus).toHaveAttribute('aria-valuenow', '2');
        await expect.soft(memory).toHaveValue('4096');
        await builder.inspector.getByLabel(/^Type/).selectOption('Router');

        await expect.soft(errors).toBeHidden();
        await expect.soft(builder.inspector).toContainText('Unapplied changes');
        await applyEdits(builder);

        await expect.soft(builder).toHaveAnnounced('Updated device web01');
        await expect.soft(subject(builder)).toHaveText(/^\s*Device web01\b/);
        await expect.soft(specHostname).toHaveValue('web01');
        // Apply goes with the changes it applied, and focus returns to the
        // field edited last.
        await expect.soft(apply).toHaveCount(0);
        await expect.soft(builder.inspector.getByLabel(/^Type/)).toBeFocused();
        // Hard: the tooltip step hovers and focuses this node.
        await expect(builder.node('web01', 'device')).toBeVisible();
        await expect.soft(builder.canvasItem('web01')).toBeVisible();

        await expect.soft
          .poll(async () => {
            const device = nodeOf(
              await builder.serverDocument(draft),
              'device',
            );

            return device.device;
          }, PERSIST)
          .toMatchObject({
            hostname: 'web01',
            spec: {
              type: 'Router',
              general: {
                hostname: 'web01',
                description: 'Front end web server',
                snapshot: false,
              },
              hardware: {
                os_type: 'windows',
                memory: 4096,
                vcpus: 2,
                drives: [{ image: 'win10.qc2' }, { image: 'data.qc2' }],
              },
            },
          });

        // Emptied, a number field shows its default again, and stores none.
        await fillField(vcpus, '');
        await expect.soft(vcpus).toHaveValue('1');
        await cancel.press('Enter');
        await expect.soft(vcpus).toHaveValue('2');
      });

      expectNoFatal(issues);
    },
  );

  test(
    'an applied device shows its type and description, warns of a missing drive image, and its checks lead to it',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }, testInfo) => {
      await mockDisks(page, ['ubuntu.qc2', 'win10.qc2']);
      const draft = await builder.seedDraft(
        appliedDeviceDocument(uniqueName(testInfo, 'applied')),
      );
      await builder.openDraft(draft);
      await builder.selectNode('web01');
      const { hostname, cancel } = deviceFields(builder);

      await test.step('the node shows its type, and its description in its info tooltip on hover and keyboard focus', async () => {
        const node = builder.node('web01', 'device');
        const id = await node.getAttribute('data-node-id');
        // Vue Flow's wrapper is the node's focusable, named element.
        const wrapper = page.locator(`.vue-flow__node[data-id="${id}"]`);
        // The canvas has one tooltip for all its nodes, outside them.
        const tooltip = page.getByTestId('node-tooltip');
        const description = tooltip.locator('dd').first();
        await expect.soft(tooltip).toHaveCount(0);
        // The type applied above, as stored.
        await expect.soft(node.getByTestId('node-type')).toHaveText('Router');
        // The description ends the node's name, and is its last line.
        await expect
          .soft(wrapper)
          .toHaveAccessibleName(/, comment: Front end web server$/);
        await expect
          .soft(node.locator('.builder-node__comment'))
          .toHaveText('Front end web server');

        // The pointer can move onto the tooltip, and Escape closes it (WCAG
        // 1.4.13). A still pointer over a new node shows nothing.
        await page.getByRole('heading', { name: 'Nodes & Objects' }).hover();
        await node.hover();
        await expect.soft(description).toHaveText('Front end web server');
        await expect
          .soft(tooltip.locator('dt'))
          .toHaveText(['Description', 'Interfaces', 'OS type']);
        await expect.soft(tooltip.locator('dd').last()).toHaveText('windows');
        const box = await tooltip.boundingBox();
        if (box) {
          await page.mouse.move(box.x + 10, box.y + box.height / 2, {
            steps: 8,
          });
          await expect.soft(tooltip).toBeVisible();
        }
        await page.keyboard.press('Escape');
        await expect.soft(tooltip).toHaveCount(0);

        await page.getByRole('heading', { name: 'Nodes & Objects' }).hover();

        // Keyboard: the node shows it when Tab brings focus back to the
        // canvas, whose Tab stop is the node that had focus last.
        await wrapper.focus();
        await page.keyboard.press('Shift+Tab');
        await expect.soft(wrapper).not.toBeFocused();
        await page.keyboard.press('Tab');
        await expect.soft(wrapper).toBeFocused();
        await expect.soft(description).toHaveText('Front end web server');
        await page.keyboard.press('Shift+Tab');
        await expect.soft(wrapper).not.toBeFocused();
        await expect.soft(tooltip).toHaveCount(0);
      });

      // web01's second drive is data.qc2, which the server does not have.
      await test.step('a drive image the server does not have is a warning, under its field and at the top', async () => {
        // The control of the field its warnings are keyed by.
        const drive = builder.inspector.locator(
          '[data-path="spec.hardware.drives.1.image"]',
        );
        const driveImage = drive.getByRole('combobox');
        const checks = builder.inspector.getByTestId('inspector-checks');
        const missing = 'The server has no disk image named "data.qc2".';

        await expect.soft(drive).toContainText(missing);
        // The device's own checks head the Inspector, above its fields.
        await expect
          .soft(checks)
          .toContainText(
            'Warning: drive image "data.qc2" of "web01" is not among the server\'s disk images',
          );
        await expect.soft(checks).toContainText('Checks: 3 warnings');
        expect
          .soft((await checks.boundingBox())?.y)
          .toBeLessThan((await hostname.boundingBox())?.y);

        // The working copy is checked before Apply. The checks at the top
        // are of the device as applied.
        await fillField(driveImage, 'win10.qc2');
        await expect.soft(drive).not.toContainText(missing);
        await expect.soft(checks).toContainText('drive image "data.qc2"');
        await fillField(driveImage, 'gone.qc2');
        await expect
          .soft(drive)
          .toContainText('The server has no disk image named "gone.qc2".');
        await cancel.press('Enter');
        await expect.soft(drive).toContainText(missing);
      });

      await test.step('the header counts the checks, and its dialog shows the device an issue is about', async () => {
        const button = page.getByTestId('builder-checks');
        const node = builder.node('web01', 'device');
        const id = await node.getAttribute('data-node-id');
        const wrapper = page.locator(`.vue-flow__node[data-id="${id}"]`);
        const driveImage = builder.inspector
          .locator('[data-path="spec.hardware.drives.1.image"]')
          .getByRole('combobox');

        await expect
          .soft(button)
          .toHaveAccessibleName('3 warnings in this diagram');
        await expect.soft(button).toHaveAttribute('aria-haspopup', 'dialog');

        // Nothing selected, and the device out of view.
        await builder.canvas.focus();
        await page.keyboard.press('Escape');
        await expect(subject(builder)).toHaveText(/^\s*Diagram\b/);
        await panOutOfView(page, wrapper);

        await button.click();
        const dialog = page.getByRole('dialog', { name: 'Diagram checks' });
        await expect(dialog).toBeVisible();
        // Each issue names the device it is about, and so does its Go to.
        await expect
          .soft(dialog.getByTestId('issue-element'))
          .toHaveText(['Device web01', 'Device web01', 'Device web01']);
        const issue = dialog.getByRole('button', {
          name: /^Go to device web01: drive image "data\.qc2"/,
        });
        await issue.click();

        // The dialog closes, the device is selected and in view, and focus
        // moves to the field the issue is about.
        await expect(dialog).toHaveCount(0);
        await expect.soft(driveImage).toBeFocused();
        await expect.soft(wrapper).toBeInViewport();
        await expect
          .soft(builder.canvasItem('web01'))
          .toHaveAttribute('aria-pressed', 'true');
        await expect.soft(subject(builder)).toHaveText(/^\s*Device web01\b/);
        await expect
          .soft(builder)
          .toHaveAnnounced(/\(Drive 2\) in device web01\b/);

        // Closed without a choice, it returns focus to the button.
        await button.press('Enter');
        await expect(dialog).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(dialog).toHaveCount(0);
        await expect.soft(button).toBeFocused();
      });

      // Stacked, Go to on the warning of no interfaces focuses Add interface
      // and scrolls it into view.
      await test.step('in the narrow stacked layout, the dialog scrolls to the field it names', async () => {
        const viewport = page.viewportSize();
        const button = page.getByTestId('builder-checks');
        const add = builder.inspector
          .getByRole('group', { name: 'Interfaces', exact: true })
          .getByRole('button', { name: 'Add interface' });

        await page.setViewportSize({ width: 640, height: 740 });
        await button.scrollIntoViewIfNeeded();

        await button.click();
        await page
          .getByRole('dialog', { name: 'Diagram checks' })
          .getByRole('button', {
            name: /^Go to device web01: device "web01" has no interfaces/,
          })
          .click();
        await expect.soft(add).toBeFocused();
        await expect.soft(add).toBeInViewport();
        await expect
          .soft(builder)
          .toHaveAnnounced('Interfaces in device web01');
        await page.setViewportSize(viewport);
      });

      // At phone width, while errors show, Apply and Cancel wrap to two rows
      // and must not cover the field with focus.
      await test.step('at phone width, Apply and Cancel never cover the field with focus', async () => {
        const viewport = page.viewportSize();
        const bar = builder.inspector.getByTestId('inspector-actions');
        // What the field with focus is, when Apply and Cancel cover it.
        const covered = () =>
          page.evaluate(() => {
            const field = document.activeElement;
            const actions = document.querySelector(
              '[data-testid="inspector-actions"]',
            );

            if (
              !actions ||
              actions.contains(field) ||
              !field.closest('.builder-inspector form')
            ) {
              return '';
            }

            const [a, b] = [
              field.getBoundingClientRect(),
              actions.getBoundingClientRect(),
            ];

            return a.bottom > b.top && a.top < b.bottom ? field.id : '';
          });

        await page.setViewportSize({ width: 390, height: 800 });
        await builder.selectNode('web01');
        await setNumber(builder, 'VCPUs', 0);
        await expect(bar).toHaveAttribute('data-state', 'error');

        // The error summary's links are stacked, so each is at least 24
        // pixels tall (WCAG 2.5.8).
        const heights = await builder.inspector
          .locator('.builder-inspector__error-link')
          .evaluateAll((links) =>
            links.map((link) => link.getBoundingClientRect().height),
          );
        expect(heights.length).toBeGreaterThan(0);
        for (const height of heights) {
          expect.soft(height).toBeGreaterThanOrEqual(24);
        }

        // In forced colors, Apply, which cannot apply, looks disabled
        // rather than like Cancel. Chromium emulates them.
        if (page.context().browser().browserType().name() === 'chromium') {
          await page.emulateMedia({ forcedColors: 'active' });
          const [applyColor, cancelColor] = await page.evaluate(() =>
            ['inspector-apply', 'inspector-cancel'].map(
              (id) =>
                getComputedStyle(
                  document.querySelector(`[data-testid="${id}"]`),
                ).color,
            ),
          );
          expect.soft(applyColor).not.toBe(cancelColor);
          await page.emulateMedia({ forcedColors: 'none' });
        }

        await hostname.focus();
        for (let step = 0; step < 30; step += 1) {
          await page.keyboard.press('Tab');
          await expect.soft.poll(covered, { timeout: 2000 }).toBe('');
        }

        // In a short window, as at 400% zoom of 1280 by 720, they end the
        // form rather than stick over the little of it in view.
        await page.setViewportSize({ width: 320, height: 180 });
        await hostname.focus();
        for (let step = 0; step < 8; step += 1) {
          await page.keyboard.press('Tab');
          await expect.soft.poll(covered, { timeout: 2000 }).toBe('');
          await expect
            .soft(builder.inspector.locator('form :focus'))
            .toBeInViewport();
        }
        await cancel.press('Enter');
        await expect.soft(bar).toHaveCount(0);
        await page.setViewportSize(viewport);
      });

      expectNoFatal(issues);
    },
  );

  // One test per selection kind, each on its own draft: select, check, edit,
  // apply and poll the server.
  test('shows and applies the diagram’s fields, and takes a rename from the header', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    await builder.open();
    const draft = await builder.createBlank();
    const nameField = builder.inspector.getByLabel('Name');
    const apply = builder.inspector.getByTestId('inspector-apply');
    const name = uniqueName(testInfo, 'diagram');
    const renamed = `${name}-renamed`;

    async function persisted(read) {
      return read(await builder.serverDocument(draft));
    }

    await test.step('diagram name and description, and a header rename', async () => {
      await expect.soft(subject(builder)).toHaveText(/^\s*Diagram\b/);
      await expect.soft(nameField).toHaveValue(/^Untitled topology( \d+)?$/);

      // An empty field filled and emptied again is no change: Apply goes,
      // and Enter adds no Undo step.
      const description = builder.inspector.getByLabel('Description');
      await fillField(description, 'x');
      await expect.soft(apply).toBeVisible();
      await fillField(description, '');
      await expect.soft(apply).toHaveCount(0);
      await nameField.press('Enter');
      await expect.soft(builder).toHaveAnnounced('No changes to apply.');
      await expect
        .soft(page.getByTestId('toolbar-undo'))
        .toHaveAttribute('aria-disabled', 'true');

      await fillField(nameField, name);
      await fillField(
        builder.inspector.getByLabel('Description'),
        'Inspector test lab',
      );
      await applyEdits(builder);

      await expect.soft(builder).toHaveAnnounced(`Updated diagram ${name}`);
      await expect.soft(page.getByTestId('builder-name')).toHaveText(name);
      await expect.soft(nameField).toHaveValue(name);
      await expect.soft
        .poll(
          () =>
            persisted((doc) => ({
              name: doc.metadata.name,
              description: doc.metadata.description,
            })),
          PERSIST,
        )
        .toEqual({ name, description: 'Inspector test lab' });

      // A rename in the header flows back into the (clean) inspector form.
      await builder.rename(renamed);
      await expect.soft(nameField).toHaveValue(renamed);
      await expect.soft
        .poll(() => persisted((doc) => doc.metadata.name), PERSIST)
        .toBe(renamed);
    });

    expectNoFatal(issues);
  });

  test('shows and applies a device’s fields, keeps text typed while disk images arrive, and changes its icon at once', async ({
    page,
    builder,
    issues,
  }) => {
    // The server's disk images are held back until the device step has
    // typed in a field, as a slow server's answer would be.
    let answerDisks;
    const disksAsked = new Promise((resolve) => {
      answerDisks = resolve;
    });
    await page.route('**/api/v1/disks', async (route) => {
      await disksAsked;
      await route.fulfill({
        json: { disks: [{ kind: 'VM', name: 'kali.qc2' }] },
      });
    });

    // The device is not added last, so the click below selects it rather than
    // deselecting the one selected node.
    const draft = await newDraft(builder, 'device', 'switch', 'note', 'group');
    const apply = builder.inspector.getByTestId('inspector-apply');
    const addInterface = builder.inspector.getByTestId(
      'inspector-add-interface',
    );

    async function persisted(read) {
      return read(await builder.serverDocument(draft));
    }

    await test.step('device', async () => {
      await builder.node('node', 'device').click();
      await expect.soft(subject(builder)).toHaveText(/^\s*Device node\b/);
      await expect.soft(deviceHostname(builder)).toHaveValue('node');
      await expect
        .soft(builder.inspector.getByTestId('inspector-icon'))
        .toBeVisible();
      await expect.soft(specGroup(builder, 'Node')).toBeVisible();
      await expect.soft(addInterface).toBeVisible();

      // Text typed and not yet committed stays when the server's disk
      // images arrive meanwhile.
      const description = specGroup(builder, 'General').getByLabel(
        /^Description/,
      );
      await description.fill('Typed before the images came');
      answerDisks();
      await expect
        .soft(builder.inspector)
        .toContainText('The server has no disk image named "ubuntu.qc2".');
      await expect
        .soft(description)
        .toHaveValue('Typed before the images came');
      await expect.soft(description).toBeFocused();
      await description.fill('');
      await description.blur();
      await expect.soft(apply).toHaveCount(0);

      // The icon applies at once. Its menu opens on the checked icon. Home,
      // letters, Escape and Enter work, and one Undo takes it back.
      const icon = builder.inspector.getByTestId('inspector-icon');
      const menu = builder.inspector.getByTestId('inspector-icon-menu');
      const node = builder.node('node', 'device');
      const router = node.locator('.builder-icon--router');
      const linux = menu.getByRole('menuitemradio', {
        name: 'Linux',
        exact: true,
      });
      await expect.soft(icon).toHaveAccessibleName(/^Icon /);
      await expect
        .soft(icon)
        .toHaveAccessibleDescription(
          /Choose Custom icon… to use an image of the server's icon library\.$/,
        );
      await expect.soft(icon).toHaveAttribute('aria-expanded', 'false');
      await icon.focus();
      await page.keyboard.press('ArrowDown');
      await expect(menu).toBeVisible();
      await expect.soft(icon).toHaveAttribute('aria-expanded', 'true');
      await expect.soft(menu).toHaveAccessibleName('Icon');
      await expect.soft(linux).toBeFocused();
      await expect.soft(linux).toHaveAttribute('aria-checked', 'true');
      await page.keyboard.press('Home');
      await expect.soft(menu.getByRole('menuitemradio').first()).toBeFocused();
      await page.keyboard.press('r');
      await expect
        .soft(menu.getByRole('menuitemradio', { name: 'Router', exact: true }))
        .toBeFocused();
      await expectAccessible(page, {
        include: '[data-testid="inspector-icon-menu"]',
        soft: true,
        label: 'axe on the Icon menu',
      });
      await page.keyboard.press('Escape');
      await expect.soft(menu).toHaveCount(0);
      await expect.soft(icon).toBeFocused();
      await expect.soft(router).toHaveCount(0);

      await page.keyboard.press('ArrowDown');
      await expect.soft(linux).toBeFocused();
      await page.keyboard.press('Home');
      await page.keyboard.press('r');
      await page.keyboard.press('Enter');
      await expect.soft(menu).toHaveCount(0);
      await expect.soft(router).toHaveCount(1);
      await expect
        .soft(builder)
        .toHaveAnnounced(/\bChanged the icon of Device node to Router\b/i);
      await expect.soft(icon).toBeFocused();
      await expect.soft(icon).toHaveAccessibleName('Icon Router');
      await expect.soft(apply).toHaveCount(0);

      await builder.toolbar('undo').click();
      await expect
        .soft(builder)
        .toHaveAnnounced(
          /\bUndid Changed the icon of Device node to Router\b/i,
        );
      await expect.soft(router).toHaveCount(0);
      await builder.toolbar('redo').click();
      await expect.soft(router).toHaveCount(1);

      // A click on an icon in the menu chooses it.
      await builder.selectNode('node');
      await icon.click();
      await menu
        .getByRole('menuitemradio', { name: 'Server', exact: true })
        .click();
      await expect.soft(menu).toHaveCount(0);
      await expect.soft(node.locator('.builder-icon--server')).toHaveCount(1);
      await expect
        .soft(builder)
        .toHaveAnnounced(/\bChanged the icon of Device node to Server\b/i);
      await expect.soft
        .poll(
          () => persisted((doc) => nodeOf(doc, 'device')?.device?.iconKey),
          PERSIST,
        )
        .toBe('server');
    });

    expectNoFatal(issues);
  });

  test(
    'shows and applies the fields of a note and of a group',
    { tag: '@cross-browser' },
    async ({ builder, issues }) => {
      const draft = await newDraft(builder, 'note', 'group');
      const apply = builder.inspector.getByTestId('inspector-apply');

      async function persisted(read) {
        return read(await builder.serverDocument(draft));
      }

      await test.step('note', async () => {
        await builder.selectNode('Note');
        await expect.soft(subject(builder)).toHaveText(/^\s*Note\b/);

        // The color applies at once, without Apply, and so does the color
        // removed with Enter.
        const color = builder.inspector.getByLabel('Color', { exact: true });
        await expect.soft(color).toHaveValue('');
        await fillField(color, '#111111');
        await expect
          .soft(builder)
          .toHaveAnnounced('Changed the color of Note to #111111');
        await expect.soft(apply).toHaveCount(0);
        await color.fill('');
        await color.press('Enter');
        await expect.soft(builder).toHaveAnnounced('Removed the color of Note');
        await expect.soft(apply).toHaveCount(0);

        await fillField(
          builder.inspector.getByLabel(/^Text/),
          'Remember the DMZ',
        );
        await applyEdits(builder);
        await expect
          .soft(builder.nodes('note'))
          .toContainText('Remember the DMZ');
        await expect.soft
          .poll(
            () => persisted((doc) => nodeOf(doc, 'note')?.note?.text),
            PERSIST,
          )
          .toBe('Remember the DMZ');

        // Position fields move a node without dragging (WCAG 2.5.7), to
        // whole pixels: the form is novalidate, so a fraction gets through.
        const x = builder.inspector.getByLabel('X', { exact: true });
        await x.fill('480.5');
        const y = builder.inspector.getByLabel('Y', { exact: true });
        await y.fill('320');
        await y.press('Enter');
        await expect.soft
          .poll(
            () => persisted((doc) => nodeOf(doc, 'note')?.position),
            PERSIST,
          )
          .toEqual({ x: 481, y: 320 });
        await expect.soft(x).toHaveValue('481');

        // Spaces around a number are dropped, and the arrow keys still step
        // a field by one.
        await x.fill('');
        await x.pressSequentially(' 3 ');
        await expect.soft(x).toHaveValue(' 3 ');
        await expect.soft(x).toHaveRole('spinbutton');
        await expect.soft(x).toHaveAttribute('aria-valuenow', '3');
        await x.press('Enter');
        await expect.soft
          .poll(
            () => persisted((doc) => nodeOf(doc, 'note')?.position),
            PERSIST,
          )
          .toEqual({ x: 3, y: 320 });
        await expect.soft(x).toHaveValue('3');
        await y.press('ArrowUp');
        await expect.soft(y).toHaveValue('321');
        await y.press('ArrowDown');
        await expect.soft(y).toHaveValue('320');
      });

      await test.step('group', async () => {
        await builder.selectNode('Group');
        await expect.soft(subject(builder)).toHaveText(/^\s*Group\b/);
        const title = builder.inspector.getByLabel('Title');
        await expect.soft(title).toHaveValue('Group');

        // One Enter in a text field commits the field and applies it, in
        // Firefox too, where the form submits before the field's change.
        await title.fill('Enclave');
        await title.press('Enter');
        await expect.soft(builder).toHaveAnnounced('Updated group Enclave');
        await expect.soft(builder.nodes('group')).toContainText('Enclave');
        await expect.soft(builder.canvasItem('Enclave')).toBeVisible();
        await expect.soft
          .poll(
            () => persisted((doc) => nodeOf(doc, 'group')?.group?.title),
            PERSIST,
          )
          .toBe('Enclave');
      });

      expectNoFatal(issues);
    },
  );

  // The switch and connection steps need the device on the switch.
  test('shows and applies the fields of a switch and its network, a suggested color, and a connection', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const name = uniqueName(testInfo, 'wired');
    const draft = await builder.seedDraft(wiredDocument(name));
    await builder.openDraft(draft);
    const nameField = builder.inspector.getByLabel('Name');
    const apply = builder.inspector.getByTestId('inspector-apply');
    const addInterface = builder.inspector.getByTestId(
      'inspector-add-interface',
    );

    async function persisted(read) {
      return read(await builder.serverDocument(draft));
    }

    await test.step('switch Name and VLAN alias apply to its network', async () => {
      await builder.selectNode('EXP');
      await expect.soft(subject(builder)).toHaveText(/^\s*Network EXP\b/);
      await expect
        .soft(builder.inspector.getByLabel(/^Name/))
        .toHaveValue('EXP');
      await expect.soft(addInterface).toBeHidden();

      await fillField(builder.inspector.getByLabel(/^Name/), 'MGMT');
      await fillField(builder.inspector.getByLabel('VLAN alias'), '101');
      // Pointer path: one click where Apply is drawn, straight from a field
      // that has not committed, applies it.
      await builder.inspector.getByLabel('Description').fill('Management VLAN');
      const box = await apply.boundingBox();
      await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);

      await expect.soft(builder).toHaveAnnounced('Updated network MGMT');
      await expect.soft(subject(builder)).toHaveText(/^\s*Network MGMT\b/);
      await expect.soft(builder.node('MGMT', 'switch')).toBeVisible();
      // The switch's canvas label follows its network's name.
      await expect
        .soft(builder.nodes('switch').locator('.builder-node__label'))
        .toHaveText('MGMT');
      // The switch shows only its name. Its accessible name says the
      // network and the VLAN alias.
      await expect
        .soft(builder.nodes('switch'))
        .not.toContainText('VLAN alias');
      await expect
        .soft(builder.canvasItem('MGMT'))
        .toHaveAttribute('aria-label', /network MGMT, VLAN alias 101/);
      await expect
        .soft(page.locator('path.builder-edge'), 'the edge network')
        .toHaveAttribute('data-network', 'MGMT');
      await expect.soft
        .poll(
          () =>
            persisted((doc) => ({
              networks: doc.networks,
              vlan: nodeOf(doc, 'device')?.device?.spec?.network
                ?.interfaces?.[0]?.vlan,
            })),
          PERSIST,
        )
        .toMatchObject({
          networks: [
            { name: 'MGMT', alias: 101, description: 'Management VLAN' },
          ],
          vlan: 'MGMT',
        });
    });

    // A suggested network color is drawn in the theme's token, on the
    // connections, the switch's outline and the picker's chip alike.
    await test.step('a suggested network color is drawn in its theme token', async () => {
      await builder.selectNode('MGMT');
      // A switch has three color pickers: this one is Edge Color's.
      const edgeColor = builder.inspector.locator('[data-path="color"]');
      const picker = edgeColor.getByTestId('inspector-color-picker');
      const popup = edgeColor.getByTestId('inspector-color-popup');
      const chip = picker.locator('.inspector-color__chip');
      await expect
        .soft(builder.inspector.getByTestId('inspector-color-picker'))
        .toHaveCount(3);
      await expect.soft(picker).toHaveAccessibleName('Choose edge Color');
      // The switch's outline color, which its selection border hides.
      const outline = () =>
        builder.node('MGMT', 'switch').evaluate((node) => {
          const probe = document.createElement('span');

          probe.style.color = 'var(--bx-node-outline)';
          node.append(probe);
          const { color } = getComputedStyle(probe);
          probe.remove();

          return color;
        });
      const edge = page.locator('path.builder-edge');
      // The color the theme gives --bx-net-N.
      const token = (index) =>
        page.locator('.builder-root').evaluate((root, at) => {
          const probe = document.createElement('span');

          probe.style.color = `var(--bx-net-${at})`;
          root.append(probe);
          const { color } = getComputedStyle(probe);
          probe.remove();

          return color;
        }, index);

      for (const [name, index] of [
        ['Red, #a3273f', 4],
        ['Plum, #8a4b8f', 7],
      ]) {
        // It applies at once, without Apply.
        await picker.click();
        await popup.getByRole('button', { name }).click();
        await expect
          .soft(builder)
          .toHaveAnnounced(
            `Changed the edge color of Network MGMT to ${name.split(', ')[1]}`,
          );
        await expect.soft(apply).toHaveCount(0);
        const drawn = await token(index);
        await expect.soft(edge).toHaveCSS('stroke', drawn);
        await expect.soft.poll(outline).toBe(drawn);
        await expect.soft(chip).toHaveCSS('background-color', drawn);
      }
      await expect.soft
        .poll(() => persisted((doc) => doc.networks[0].color), PERSIST)
        .toBe('#8a4b8f');

      // At phone width the picker opens in view, not below the window.
      const viewport = page.viewportSize();
      await page.setViewportSize({ width: 390, height: 800 });
      await picker.evaluate((button) =>
        button.scrollIntoView({ block: 'end' }),
      );
      await picker.press('Enter');
      await expect.soft(popup).toBeInViewport({ ratio: 1 });
      await page.keyboard.press('Escape');
      await expect.soft(picker).toBeFocused();
      await page.setViewportSize(viewport);
    });

    await test.step('clicking the empty canvas shows the diagram again', async () => {
      await page
        .locator('.vue-flow__pane')
        .click({ position: { x: 400, y: 600 } });
      await expect.soft(subject(builder)).toHaveText(/^\s*Diagram\b/);
      await expect.soft(nameField).toHaveValue(name);
    });

    // Relabelling is covered in builder-editing.spec.js. Here the color and
    // label apply at once, without Apply.
    await test.step('connection', async () => {
      // The edge's label lets a click through to the line under it.
      const label = page.locator('.builder-edge__label');
      await expect(label, 'the connection is drawn').toBeVisible();
      const box = await label.boundingBox();
      await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
      await expect.soft(subject(builder)).toHaveText(/^\s*Connection\b/);

      // An unlabelled connection is drawn with its network's name, which
      // its Label shows as the default.
      const labelField = builder.inspector.getByLabel('Label', {
        exact: true,
      });
      await expect.soft(labelField).toHaveValue('MGMT');
      await expect
        .soft(labelField)
        .toHaveAccessibleDescription("Default: The network's name.");

      // The swatch button opens a dialog of named swatches, one Tab stop moved
      // through with the arrow keys, which Escape closes.
      const picker = builder.inspector.getByTestId('inspector-color-picker');
      const popup = builder.inspector.getByTestId('inspector-color-popup');
      await expect.soft(picker).toHaveAccessibleName('Choose color');
      await expect.soft(picker).toHaveAccessibleDescription('No color');
      await picker.press('Enter');
      await expect(popup).toHaveRole('dialog');
      await expect
        .soft(popup.getByRole('button', { name: 'Blue, #2f6fbf' }))
        .toBeFocused();
      // Only the picker, which this step opens.
      await expectAccessible(page, {
        include: '[data-testid="inspector-color-popup"]',
        soft: true,
        label: 'axe on the color picker',
      });
      await page.keyboard.press('Escape');
      await expect.soft(popup).toHaveCount(0);
      await expect.soft(picker).toBeFocused();
      await expect.soft(picker).toHaveAttribute('aria-expanded', 'false');

      // Save with the picker's Custom color focused chooses no color: the
      // color input commits only on its own change.
      await builder.waitSaved();
      await picker.press('Enter');
      await popup.getByLabel('Custom color').focus();
      await page.keyboard.press('ControlOrMeta+s');
      await expect.soft(builder).toHaveAnnounced('No changes to save.');
      await expect.soft(popup).toBeVisible();
      await page.keyboard.press('Escape');
      await expect.soft(popup).toHaveCount(0);
      await expect
        .soft(builder.inspector.getByLabel('Color', { exact: true }))
        .toHaveValue('');
      await expect.soft(picker).toHaveAccessibleDescription('No color');

      await picker.press('Enter');
      await page.keyboard.press('ArrowRight');
      await page.keyboard.press('ArrowRight');
      await expect
        .soft(popup.getByRole('button', { name: 'Green, #1f7a5a' }))
        .toBeFocused();
      await page.keyboard.press('Enter');
      await expect.soft(popup).toHaveCount(0);
      await expect.soft(picker).toBeFocused();
      await expect
        .soft(builder.inspector.getByLabel('Color', { exact: true }))
        .toHaveValue('#1f7a5a');
      await expect.soft(picker).toHaveAccessibleDescription('Green, #1f7a5a');
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Changed the color of Connection from node (eth0) to MGMT to #1f7a5a',
        );

      await fillField(labelField, 'uplink');
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Changed the label of Connection from node (eth0) to MGMT to "uplink"',
        );
      await expect.soft(apply).toHaveCount(0);
      await expect.soft(label).toHaveText('uplink');
      await expect
        .soft(page.locator('path.builder-edge'))
        .toHaveCSS('stroke', 'rgb(31, 122, 90)');
      await expect.soft
        .poll(
          () =>
            persisted((doc) => ({
              label: doc.edges?.[0]?.label,
              color: doc.edges?.[0]?.color,
            })),
          PERSIST,
        )
        .toEqual({ label: 'uplink', color: '#1f7a5a' });
    });

    expectNoFatal(issues);
  });

  // Checks that no later step acts on are soft. The interface names are
  // hard: the next step selects or removes interfaces by name.
  test('Add connection point adds a handle and an interface row, which takes edits and connects', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await newDraft(builder, 'device', 'switch');
    await builder.selectNode('node');
    const { node, handles, rows } = await connectionPoints(page, builder);

    await test.step('Add connection point', async () => {
      await expect.soft(handles).toHaveCount(0);
      await expect.soft(rows).toHaveCount(0);
      await expect.soft(node).toContainText('0 interfaces');

      // Named apart from the node's Interfaces list's "Add interface", and
      // says that it acts without Apply.
      const add = builder.inspector.getByTestId('inspector-add-interface');
      await expect.soft(add).toHaveAccessibleName('Add connection point');
      await expect
        .soft(add)
        .toHaveAccessibleDescription(
          /the change occurs at once, without Apply/,
        );
      await expect
        .soft(builder.inspector.getByRole('button', { name: /interface$/ }))
        .toHaveText(['Add interface']);

      // The hints of Connection points and Position are tooltips on their
      // headings, on hover and on focus of their controls.
      const tip = builder.inspector.getByTestId('inspector-tooltip');
      await expect
        .soft(builder.inspector.locator('h3'))
        .toContainText(['Connection points', 'Position']);
      await builder.inspector
        .getByRole('heading', { name: 'Connection points' })
        .hover();
      await expect
        .soft(tip)
        .toHaveText(
          /^These are the interfaces of this device\. Each interface has a connection point on the canvas to connect from\./,
        );
      await page.keyboard.press('Escape');
      await expect.soft(tip).toHaveCount(0);
      await add.focus();
      await page.keyboard.press('Tab');
      await expect
        .soft(builder.inspector.getByLabel('X', { exact: true }))
        .toBeFocused();
      await expect
        .soft(tip)
        .toHaveText(
          'X and Y are in canvas pixels. Select Move to move the node without dragging it.',
        );
      await expect
        .soft(builder.inspector.getByLabel('X', { exact: true }))
        .toHaveAccessibleDescription(
          'X and Y are in canvas pixels. Select Move to move the node without dragging it.',
        );
      await page.keyboard.press('Escape');

      // An edit left unapplied and the interface added meanwhile are both
      // kept by Apply in the next step.
      await fillField(
        specGroup(builder, 'General').getByLabel(/^Description/),
        'Edited before adding',
      );
      await expect(builder.inspector).toContainText('Unapplied changes');
      await add.click();

      await expect.soft(builder).toHaveAnnounced('Added interface eth0');
      await expect(rows).toHaveText([/^eth0\b/]);
      await expect.soft(rows).toHaveText(['eth0 — not connected']);
      await expect.soft(node).toContainText('1 interface');
      // One source handle on the right and one target handle on the left.
      await expect.soft(handles).toHaveCount(2);
      await expect
        .soft(handles.first())
        .toHaveAttribute('title', 'eth0, not connected');
    });

    // The form leaves an unconnected interface's VLAN optional, so the device's
    // other fields still apply.
    await test.step('the device takes edits while an interface is not connected', async () => {
      await setNumber(builder, 'Memory', 2048);
      await expect
        .soft(builder.inspector.getByTestId('inspector-errors'))
        .toBeHidden();
      await applyEdits(builder);
      await expect.soft(builder).toHaveAnnounced('Updated device node');
      await expect.soft(rows).toHaveText(['eth0 — not connected']);
      await expect.soft
        .poll(async () => {
          const { spec } = nodeOf(
            await builder.serverDocument(draft),
            'device',
          ).device;

          return {
            memory: spec.hardware.memory,
            description: spec.general.description,
            interfaces: spec.network.interfaces.map((iface) => iface.name),
          };
        }, PERSIST)
        .toEqual({
          memory: 2048,
          description: 'Edited before adding',
          interfaces: ['eth0'],
        });
    });

    await test.step('connect the new interface', async () => {
      // The Inspector's Add connection, beside Open Node Editor.
      const opener = builder.inspectorButton('connect');
      const dialog = page.getByTestId('connect-dialog');
      const device = dialog.locator('#connect-device');
      const picked = dialog.locator('#connect-interface');

      // The selected device fills Device. A device picked again, or
      // another, never keeps the interface picked for the last.
      await opener.click();
      await expect(dialog).toBeVisible();
      await expect.soft(device.locator('option:checked')).toHaveText('node');
      await picked.selectOption({ label: 'eth0' });
      await device.selectOption({ index: 0 });
      await expect.soft(picked).toHaveValue('');
      await device.selectOption({ index: 1 });
      await expect.soft(picked).toHaveValue('');
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);

      // Connected while an edit is unapplied, it stays connected once
      // the edit is applied.
      await fillField(
        specGroup(builder, 'General').getByLabel(/^Description/),
        'Edited before wiring',
      );
      await expect(builder.inspector).toContainText('Unapplied changes');

      const view = async () => ({
        page: await page.evaluate(() => window.scrollY),
        canvas: (await builder.canvas.boundingBox())?.y,
      });
      await opener.scrollIntoViewIfNeeded();
      const before = await view();
      await opener.click();
      await expect(dialog).toBeVisible();
      await picked.selectOption({ label: 'eth0' });
      await dialog.locator('#connect-switch').selectOption({ index: 1 });
      await dialog.getByTestId('connect-dialog-submit').click();

      await expect.soft(builder.summary).toContainText('1 connection');
      await expect.soft(rows).toHaveText(['eth0 — network EXP']);
      // Connected, the dialog closes onto Add connection, and neither the
      // page nor the canvas has scrolled away.
      await expect.soft(dialog).toHaveCount(0);
      await expect.soft(opener).toBeFocused();
      expect
        .soft(await view(), 'the page and canvas after Connect')
        .toEqual(before);

      await applyEdits(builder);
      await expect.soft(builder).toHaveAnnounced('Updated device node');
      await expect(builder.summary).toContainText('1 connection');
      await expect.soft(rows).toHaveText(['eth0 — network EXP']);
      await expect
        .soft(handles.first())
        .toHaveAttribute('title', 'eth0 on network EXP');
      await expect.soft
        .poll(async () => {
          const doc = await builder.serverDocument(draft);
          const { spec } = nodeOf(doc, 'device').device;

          return {
            description: spec.general.description,
            vlans: spec.network.interfaces.map((iface) => iface.vlan),
            edges: (doc.edges || []).length,
          };
        }, PERSIST)
        .toEqual({
          description: 'Edited before wiring',
          vlans: ['EXP'],
          edges: 1,
        });

      // The interface is a named group with a named kind picker, apart from the
      // Connection points list.
      const iface = builder.inspector
        .getByRole('group', { name: 'Interfaces', exact: true })
        .getByRole('group', { name: 'Interface 1: eth0' });
      // Added as a connection point, it is a static interface.
      const kind = iface.getByLabel('Interface kind', { exact: true });
      await expect.soft(kind).toHaveValue('0');
      await expect
        .soft(kind.locator('option'))
        .toHaveText(['Static or OSPF', 'DHCP or manual', 'Serial']);
      await expect
        .soft(iface.getByRole('button', { name: 'Remove interface 1' }))
        .toBeVisible();

      // Switching kind asks first, but only once chosen: a key that steps the
      // closed picker only holds it.
      const confirm = page.getByRole('alertdialog', {
        name: 'Switch Interface kind to Serial?',
      });
      await kind.focus();
      await page.keyboard.type('s');
      await expect.soft(kind).toHaveValue('2');
      await expect.soft(confirm).toHaveCount(0);
      await page.keyboard.press('Enter');
      await expect.soft(confirm).toBeVisible();
      await confirm.getByRole('button', { name: 'Keep current value' }).click();
      await expect.soft(confirm).toHaveCount(0);
      await expect.soft(kind).toBeFocused();
      await expect.soft(kind).toHaveValue('0');
    });

    expectNoFatal(issues);
  });

  test('a connected interface’s fields wait for Apply, and an applied VLAN sets its connection', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const draft = await builder.seedDraft(
      wiredDocument(uniqueName(testInfo, 'wired')),
    );
    await builder.openDraft(draft);
    await builder.selectNode('node');
    const { handles, rows } = await connectionPoints(page, builder);
    await expect(rows).toHaveText(['eth0 — network EXP']);

    // Edits in the node's Interfaces list wait for Apply, and are cancelled
    // here: the next steps add and remove interfaces as connection points.
    await test.step("the node's Interfaces list: yes-or-no fields, MTU bounds and Add interface", async () => {
      const list = builder.inspector.getByRole('group', {
        name: 'Interfaces',
        exact: true,
      });
      const first = list.getByRole('group', { name: 'Interface 1: eth0' });
      const apply = builder.inspector.getByTestId('inspector-apply');

      // An unset yes-or-no field shows its default (Autostart is on), and
      // a click shows the box checked.
      const qinq = first.getByLabel('QinQ', { exact: true });
      await expect
        .soft(first.getByLabel('Autostart', { exact: true }))
        .toBeChecked();
      await expect.soft(qinq).not.toBeChecked();
      await qinq.click();
      await expect.soft(qinq).toBeChecked();

      // An MTU outside what phenix applies cannot be applied. 0 leaves it
      // unset.
      const mtu = first.getByLabel('MTU', { exact: true });
      await expect.soft(mtu).toHaveAttribute('aria-valuemin', '0');
      await expect.soft(mtu).toHaveAttribute('aria-valuemax', '16000');
      await fillField(mtu, '-1');
      await expect
        .soft(builder.inspector.getByTestId('inspector-error-list'))
        .toHaveText('Interface 1: MTU must be between 0 and 16000');
      await expect.soft(mtu).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(apply).toBeDisabled();
      // Enter in another field leaves focus there: the form's errors are
      // the only check, not the browser's.
      const vlan = builder.inspector
        .locator('[data-path="spec.network.interfaces.0.vlan"]')
        .getByRole('textbox');
      await vlan.press('Enter');
      await expect.soft(vlan).toBeFocused();
      await fillField(mtu, '0');
      await expect.soft(apply).toBeEnabled();

      // A new interface is named as a drawn connection names one, static
      // Ethernet, with its Address first and focused.
      await list.getByRole('button', { name: 'Add interface' }).click();
      const second = list.getByRole('group', { name: 'Interface 2: eth1' });
      await expect.soft(builder).toHaveAnnounced('Added interface 2.');
      await expect
        .soft(second.getByLabel('Interface kind', { exact: true }))
        .toHaveValue('0');
      await expect.soft(second.getByLabel(/^Address/)).toBeFocused();
      await expect.soft(second.getByLabel(/^Type/)).toHaveValue('ethernet');
      await expect.soft(second.getByLabel(/^Proto/)).toHaveValue('static');
      await expect.soft(second.getByLabel(/^Mask/)).toHaveValue('24');
      await expect.soft
        .poll(async () =>
          (await second.locator('label.label').allTextContents())
            .slice(0, 8)
            .map((text) => text.replace('*', '').trim()),
        )
        .toEqual([
          'Interface kind',
          'Address',
          'Mask',
          'Gateway',
          'Protocol',
          'Name',
          'VLAN',
          'MAC address',
        ]);

      await builder.inspector.getByTestId('inspector-cancel').press('Enter');
      await expect(
        list.getByRole('group', { name: /^Interface 2/ }),
      ).toHaveCount(0);
    });

    // The control JSON Forms renders for the field at spec.network.
    // interfaces.0.vlan, which its warnings are keyed by.
    await test.step('a VLAN that names no network is a warning under its field', async () => {
      const field = builder.inspector.locator(
        '[data-path="spec.network.interfaces.0.vlan"]',
      );
      const vlan = field.getByRole('textbox');
      const ghost = 'This diagram has no network named "GHOST".';
      // A warning is tied to its field but does not make it invalid.
      const vlanWarning = field.getByTestId('inspector-field-warning');

      // Shown as soon as the field commits, before Apply, which it does not
      // block.
      await fillField(vlan, 'GHOST');
      await expect.soft(field).toContainText(ghost);
      await expect.soft(vlanWarning).toHaveText(`Warning: ${ghost}`);
      await expect
        .soft(vlan)
        .toHaveAccessibleDescription(
          new RegExp(`^Warning: ${ghost.replace(/[.]/g, '\\.')}`),
        );
      await expect.soft(vlan).not.toHaveAttribute('aria-invalid');
      // Focus stays in the field, whose new description is not read: the
      // warning is announced.
      await expect.soft(builder).toHaveAnnounced(`Warning for VLAN: ${ghost}`);
      await expect
        .soft(builder.inspector.getByTestId('inspector-apply'))
        .toBeEnabled();
      // VLAN names match regardless of case.
      await fillField(vlan, 'exp');
      await expect.soft(field).not.toContainText(ghost);
      await builder.inspector.getByTestId('inspector-cancel').press('Enter');
      await expect.soft(vlan).toHaveValue('EXP');
    });

    // Typing an interface's VLAN chooses its connection, in one edit. One
    // that names no network is kept, as phenix makes a VLAN of any name.
    await test.step('an applied VLAN sets the connection, and one naming no network is kept', async () => {
      const vlan = builder.inspector
        .locator('[data-path="spec.network.interfaces.0.vlan"]')
        .getByRole('textbox');
      const vlanOnServer = async () => {
        const doc = await builder.serverDocument(draft);

        return {
          vlan: nodeOf(doc, 'device').device.spec.network.interfaces[0].vlan,
          edges: doc.edges.length,
        };
      };

      await fillField(vlan, 'GHOST');
      await applyEdits(builder);
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Updated device node and disconnected eth0 from network EXP',
        );
      // The warnings the edit brought, which the checks list, are
      // announced after it.
      await expect
        .soft(builder)
        .toHaveAnnounced(
          '2 new warnings: interface "eth0" of "node" is not connected to a network. interface "eth0" of "node" uses VLAN "GHOST", which is not a network in this diagram',
        );
      await expect(rows).toHaveText(['eth0 — not connected']);
      await expect.soft(vlan).toHaveValue('GHOST');
      await expect
        .soft(builder.inspector.getByTestId('inspector-checks'))
        .toContainText(
          'interface "eth0" of "node" uses VLAN "GHOST", which is not a network in this diagram',
        );
      await expect.soft.poll(vlanOnServer, PERSIST).toEqual({
        vlan: 'GHOST',
        edges: 0,
      });

      await fillField(vlan, 'exp');
      await applyEdits(builder);
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Updated device node and connected eth0 to network EXP',
        );
      await expect(rows).toHaveText(['eth0 — network EXP']);
      await expect.soft(vlan).toHaveValue('EXP');

      // Undo takes back the connection and the VLAN together.
      await builder.toolbar('undo').click();
      await expect.soft(builder.summary).toContainText('0 connections');
      await expect
        .soft(handles.first())
        .toHaveAttribute('title', 'eth0, not connected');
      await builder.toolbar('redo').click();
      await expect(builder.summary).toContainText('1 connection');
      await expect.soft.poll(vlanOnServer, PERSIST).toEqual({
        vlan: 'EXP',
        edges: 1,
      });
      await builder.selectNode('node');

      // An emptied VLAN applies too, and disconnects the interface
      // in the same edit. Undo connects it again for the steps that follow.
      await fillField(vlan, '');
      await applyEdits(builder);
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Updated device node and disconnected eth0 from network EXP',
        );
      await expect(rows).toHaveText(['eth0 — not connected']);
      await expect
        .soft(builder.inspector.getByTestId('inspector-checks'))
        .toContainText(
          'interface "eth0" of "node" is not connected to a network and has no VLAN. Thus you cannot publish the interface',
        );
      await expect.soft.poll(vlanOnServer, PERSIST).toEqual({
        vlan: '',
        edges: 0,
      });
      await builder.toolbar('undo').click();
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Undid Updated device node and disconnected eth0 from network EXP',
        );
      await expect(builder.summary).toContainText('1 connection');
      await expect.soft.poll(vlanOnServer, PERSIST).toEqual({
        vlan: 'EXP',
        edges: 1,
      });
      await builder.selectNode('node');
    });

    expectNoFatal(issues);
  });

  test('Disconnect drops a connection point’s connection, and Remove connection point removes them', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const draft = await builder.seedDraft(
      wiredDocument(uniqueName(testInfo, 'wired')),
    );
    await builder.openDraft(draft);
    await builder.selectNode('node');
    const { node, handles, rows } = await connectionPoints(page, builder);
    await expect(rows).toHaveText(['eth0 — network EXP']);

    // Each connected connection point has a Disconnect button: the
    // connection goes, the interface stays.
    await test.step("Disconnect removes a connection point's connection", async () => {
      const disconnect = builder.inspector.getByRole('button', {
        name: 'Disconnect eth0 from network EXP',
      });
      await expect.soft(disconnect).toHaveText('Disconnect');
      await disconnect.press('Enter');

      await expect
        .soft(builder)
        .toHaveAnnounced('Deleted the connection between node (eth0) and EXP');
      await expect(rows).toHaveText(['eth0 — not connected']);
      await expect.soft(builder.summary).toContainText('0 connections');
      // Focus moves to the row's Remove button, not to <body>.
      await expect
        .soft(
          builder.inspector.getByRole('button', {
            name: 'Remove connection point eth0',
          }),
        )
        .toBeFocused();
      await expect
        .soft(builder.inspector.getByTestId('inspector-disconnect'))
        .toHaveCount(0);
      await expect.soft
        .poll(async () => {
          const doc = await builder.serverDocument(draft);

          return {
            edges: doc.edges.length,
            interfaces: nodeOf(doc, 'device').device.interfaces.length,
          };
        }, PERSIST)
        .toEqual({ edges: 0, interfaces: 1 });

      await builder.toolbar('undo').click();
      await expect(builder.summary).toContainText('1 connection');
      await builder.selectNode('node');
      await expect(rows).toHaveText(['eth0 — network EXP']);
    });

    await test.step('Add connection point adds a second interface', async () => {
      await builder.inspector.getByTestId('inspector-add-interface').click();
      await expect(rows).toHaveText([/^eth0\b/, /^eth1\b/]);
      await expect.soft(handles).toHaveCount(4);
    });

    await test.step('Remove connection point', async () => {
      await builder.inspector
        .getByRole('button', { name: 'Remove connection point eth0' })
        .click();

      await expect.soft(builder).toHaveAnnounced('Removed interface eth0');
      await expect(rows).toHaveText([/^eth1\b/]);
      // Focus stays in the list, on the next row's Remove button.
      await expect
        .soft(
          builder.inspector.getByRole('button', {
            name: 'Remove connection point eth1',
          }),
        )
        .toBeFocused();
      await expect.soft(rows).toHaveText(['eth1 — not connected']);
      await expect.soft(handles).toHaveCount(2);
      await expect.soft(node).toContainText('1 interface');
      await expect.soft(builder.summary).toContainText('0 connections');

      await builder.inspector
        .getByRole('button', { name: 'Remove connection point eth1' })
        .click();
      await expect.soft(rows).toHaveCount(0);
      await expect
        .soft(builder.inspector.locator('.builder-inspector__ifaces'))
        .toContainText('No connection points.');
      await expect
        .soft(builder.inspector.getByTestId('inspector-add-interface'))
        .toBeFocused();
      await expect.soft(handles).toHaveCount(0);
      await expect.soft(node).toContainText('0 interfaces');

      await expect.soft
        .poll(async () => {
          const doc = await builder.serverDocument(draft);
          const device = nodeOf(doc, 'device').device;

          return {
            handles: device.interfaces.length,
            interfaces: device.spec.network.interfaces.length,
            edges: (doc.edges || []).length,
          };
        }, PERSIST)
        .toEqual({ handles: 0, interfaces: 0, edges: 0 });

      // Select all shows the diagram instead, which removes the focused
      // button. Focus goes to the Inspector's heading, not the page.
      await page.keyboard.press('ControlOrMeta+a');
      await expect
        .soft(builder.inspector.getByRole('heading', { name: 'Inspector' }))
        .toBeFocused();

      // The heading is visually hidden, so the Inspector shows the focus
      // ring for it, in both themes.
      for (const scheme of ['light', 'dark']) {
        await page.emulateMedia({ colorScheme: scheme });
        await expect
          .soft(page.locator('.builder-root'))
          .toHaveAttribute('data-builder-theme', scheme);
        await expect
          .soft(builder.inspector.getByRole('heading', { name: 'Inspector' }))
          .toBeFocused();
        await expect
          .soft(builder.inspector)
          .toHaveCSS('outline-style', 'solid');
        await expect.soft(builder.inspector).toHaveCSS('outline-width', '3px');
      }
      await page.emulateMedia({ colorScheme: null });
      await builder.selectNode('node');
    });

    expectNoFatal(issues);
  });

  test('a selection change or Save now settles unapplied edits, and an edit that makes a diagram error announces it', async ({
    builder,
    issues,
  }) => {
    const draft = await newDraft(builder, 'device', 'switch');
    await builder.selectNode('node');

    // A selection change applies valid edits and discards invalid ones, and the
    // live region says which.
    await test.step('a selection change applies or discards unapplied edits, and says which', async () => {
      const description = specGroup(builder, 'General').getByLabel(
        /^Description/,
      );
      await fillField(description, 'Unsaved notes');
      await expect.soft(builder.inspector).toContainText('Unapplied changes');

      await builder.selectNode('EXP');
      await expect
        .soft(builder)
        .toHaveAnnounced('Applied changes to Device node');
      await builder.selectNode('node');
      await expect.soft(description).toHaveValue('Unsaved notes');

      await fillField(deviceHostname(builder), 'bad host');
      await builder.selectNode('EXP');
      await expect
        .soft(builder)
        .toHaveAnnounced('Discarded unapplied changes to Device node');
      await builder.selectNode('node');
      await expect.soft(deviceHostname(builder)).toHaveValue('node');

      // Save now's key settles them the same way. Invalid edits stay in the
      // form and the result says so.
      await description.fill('Saved by its key');
      await description.press('ControlOrMeta+s');
      await expect
        .soft(builder)
        .toHaveAnnounced('Applied changes to Device node');
      await expect.soft
        .poll(
          async () =>
            nodeOf(await builder.serverDocument(draft), 'device').device.spec
              .general?.description,
          PERSIST,
        )
        .toBe('Saved by its key');
      await expect.soft(description).toBeFocused();

      await builder.waitSaved();
      await deviceHostname(builder).fill('bad host');
      await deviceHostname(builder).press('ControlOrMeta+s');
      await expect
        .soft(builder)
        .toHaveAnnounced(
          "No changes to save. The Inspector's changes are not applied yet: 1 field needs attention.",
        );
      await expect.soft(deviceHostname(builder)).toHaveValue('bad host');
      await builder.inspector.getByTestId('inspector-cancel').click();

      // Saving is asked for as Apply is: with a redo pending, valid edits
      // are applied all the same, and Redo goes as it does on Apply.
      await fillField(description, 'Undone');
      await applyEdits(builder);
      await builder.toolbar('undo').click();
      await expect.soft(builder.toolbar('redo')).toBeEnabled();
      await builder.selectNode('node');
      await description.fill('Saved over a redo');
      await description.press('ControlOrMeta+s');
      await expect
        .soft(builder)
        .toHaveAnnounced('Applied changes to Device node');
      await expect.soft(builder.toolbar('redo')).toBeDisabled();
      await expect.soft
        .poll(
          async () =>
            nodeOf(await builder.serverDocument(draft), 'device').device.spec
              .general?.description,
          PERSIST,
        )
        .toBe('Saved over a redo');
    });

    // The diagram checks list is far from the edit, so a new error is also
    // announced, after the edit, in the Builder's one live region.
    await test.step('an edit that makes a diagram error announces it', async () => {
      await builder.palette('device').click();
      await builder.selectNode('node-2');
      await fillField(deviceHostname(builder), 'node');
      await applyEdits(builder);
      await expect
        .soft(builder)
        .toHaveAnnounced(
          '1 new diagram error: Device node #2: duplicate hostname "node"',
        );
      // The server refuses the duplicate. Undo it so the draft saves again
      // before the test deletes it.
      await builder.toolbar('undo').click();
      await builder.waitSaved();
    });

    expectNoFatal(issues);
  });

  // A device spec as a phenix experiment stores it carries values the form's
  // schema does not (advanced null, mac ""), and still takes edits.
  test('an imported device takes edits, and its labels, annotations and advanced settings', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const id = () => crypto.randomUUID();
    const network = { id: id(), name: 'EXP' };
    const sw = {
      id: id(),
      kind: 'switch',
      label: 'EXP',
      position: { x: 0, y: 320 },
      switch: { networkId: network.id },
    };
    const device = {
      id: id(),
      kind: 'device',
      label: 'host-00',
      position: { x: 0, y: 0 },
      device: {
        hostname: 'host-00',
        iconKey: 'linux',
        spec: experimentSpec('host-00'),
        interfaces: [{ id: id(), name: 'eth0', index: 0 }],
      },
    };
    const draft = await builder.seedDraft(
      blankDocument(uniqueName(testInfo, 'imported'), {
        nodes: [device, sw],
        networks: [network],
        edges: [
          {
            id: id(),
            sourceNodeId: device.id,
            sourceHandleId: device.device.interfaces[0].id,
            targetNodeId: sw.id,
            networkId: network.id,
          },
        ],
      }),
    );
    await builder.openDraft(draft);
    await builder.selectNode('host-00');

    const errors = builder.inspector.getByTestId('inspector-errors');
    const node = specGroup(builder, 'Node');
    const section = builder.inspector.getByTestId('inspector-section');
    const summary = section.locator('summary');
    const map = (title) =>
      section
        .locator('fieldset.inspector-map')
        .filter({ has: page.locator('legend', { hasText: title }) });
    const spec = async () =>
      nodeOf(await builder.serverDocument(draft), 'device').device.spec;

    await test.step('an edit applies although fields it came with have errors', async () => {
      await expect.soft(errors).toBeHidden();
      await fillField(
        specGroup(builder, 'General').getByLabel(/^Description/),
        'Imported',
      );
      await expect.soft(errors).toBeHidden();
      await applyEdits(builder);
      await expect.soft(builder).toHaveAnnounced('Updated device host-00');

      // A field the edit changes still has its errors.
      const mac = builder.inspector
        .locator('[data-path="spec.network.interfaces.0.mac"]')
        .getByRole('textbox');
      await expect.soft(mac).not.toHaveAttribute('aria-invalid');
      await fillField(mac, 'zz');
      await expect
        .soft(builder.inspector.getByTestId('inspector-error-list'))
        .toHaveText(
          'Interface 1: MAC address must be a MAC address, such as 00:11:22:33:44:55',
        );
      await expect.soft(mac).toHaveAttribute('aria-invalid', 'true');
      await expect
        .soft(builder.inspector.getByTestId('inspector-apply'))
        .toBeDisabled();
      await builder.inspector.getByTestId('inspector-cancel').press('Enter');
      await expect.soft(errors).toBeHidden();

      // A field says one thing wrong, the most relevant: "1" is too short
      // and no IPv4 address, and only the second is said.
      const address = builder.inspector
        .locator('[data-path="spec.network.interfaces.0.address"]')
        .getByRole('textbox');
      await fillField(address, '1');
      await expect
        .soft(builder.inspector.getByTestId('inspector-error-list'))
        .toHaveText(
          'Interface 1: Address must be an IPv4 address, such as 10.0.0.1',
        );
      await expect
        .soft(address)
        .toHaveAccessibleDescription(
          /^Address must be an IPv4 address, such as 10\.0\.0\.1 IPv4/,
        );
      // Every part of the form has a renderer: a static interface's DNS
      // servers, one address or a list, are one line of text.
      await expect
        .soft(builder.inspector)
        .not.toContainText('No applicable renderer');
      await expect
        .soft(builder.inspector.getByLabel('DNS', { exact: true }))
        .toHaveValue('');
      await builder.inspector.getByTestId('inspector-cancel').press('Enter');
      await expect.soft(errors).toBeHidden();
    });

    await test.step('the fields looked for most come first, the rest in a closed section', async () => {
      await expect
        .soft(node.locator(':scope > .group-item label').first())
        .toHaveText(/^Type/);
      await expect
        .soft(node.locator(':scope > .group-item > div > fieldset > legend'))
        .toHaveText(['General', 'Hardware', 'Network']);
      await expect
        .soft(summary)
        .toHaveText(
          'More settings: miniccc commands, Delay, Injections, Advanced settings, Labels, Annotations',
        );
      await expect.soft(section).toHaveJSProperty('open', false);
      await summary.focus();
      await page.keyboard.press('Enter');
      await expect(section).toHaveJSProperty('open', true);
      // Space toggles it too: the canvas's pan key does not take it.
      await page.keyboard.press('Space');
      await expect.soft(section).toHaveJSProperty('open', false);
      await page.keyboard.press('Space');
      await expect(section).toHaveJSProperty('open', true);
    });

    await test.step('labels, annotations and advanced settings are names and values', async () => {
      const labels = map('Labels');

      await labels.getByRole('button', { name: 'Add label' }).click();
      await expect.soft(builder).toHaveAnnounced('Added label 1.');
      await expect(labels.getByLabel('Label 1 Name')).toBeFocused();
      await page.keyboard.type('ntp-server');
      await page.keyboard.press('Tab');
      await page.keyboard.type('eth0');
      await labels.getByLabel('Label 1 Value').blur();
      // Marked as changed until applied, as other fields are.
      await expect
        .soft(labels.getByTestId('inspector-map-row'))
        .toHaveClass(/\bcontrol--changed\b/);
      await expect
        .soft(labels.getByLabel('Label 1 Value'))
        .toHaveAccessibleDescription('Changed, not applied yet.');

      // A second of one name is not taken, and says why.
      await labels.getByRole('button', { name: 'Add label' }).click();
      await fillField(labels.getByLabel('Label 2 Name'), 'ntp-server');
      await expect
        .soft(labels.getByLabel('Label 2 Name'))
        .toHaveAccessibleDescription('Label 1 already uses this name');
      await expect
        .soft(labels.getByLabel('Label 2 Name'))
        .toHaveAttribute('aria-invalid', 'true');
      // It is left out of the edits until fixed, so Apply is refused, and
      // the Inspector lists it with a link to it.
      const errorList = builder.inspector.getByTestId('inspector-error-list');
      await expect
        .soft(errorList)
        .toHaveText('Label 2: Label 1 already uses this name');
      await expect
        .soft(builder.inspector.getByTestId('inspector-apply'))
        .toBeDisabled();
      await errorList.getByRole('button').click();
      await expect.soft(labels.getByLabel('Label 2 Name')).toBeFocused();
      await labels.getByRole('button', { name: 'Remove label 2' }).click();
      await expect
        .soft(labels.getByRole('button', { name: 'Remove label 1' }))
        .toBeFocused();

      // phenix reads a delay timer with Go's time.ParseDuration.
      const timer = section.getByLabel('Timer');
      await fillField(timer, '5 minutes');
      await expect
        .soft(errorList)
        .toHaveText('Timer must be a duration, such as 30s, 5m or 1h30m');
      await expect.soft(timer).toHaveAttribute('aria-invalid', 'true');
      await fillField(timer, '1h30m');
      await expect.soft(errorList).toBeHidden();

      // phenix reads phenix/default-apps as a yes or no.
      const annotations = map('Annotations');
      await annotations.getByRole('button', { name: 'Add annotation' }).click();
      await fillField(
        annotations.getByLabel('Annotation 1 Name'),
        'phenix/default-apps',
      );
      await fillField(annotations.getByLabel('Annotation 1 Value'), 'false');

      const advanced = map('Advanced settings');
      await advanced
        .getByRole('button', { name: 'Add advanced setting' })
        .click();
      await fillField(
        advanced.getByLabel('Advanced setting 1 Name'),
        'qemu-append',
      );
      // Enter in a field applies the edits, and focus stays in the field.
      const value = advanced.getByLabel('Advanced setting 1 Value');
      await value.fill('-vga std');
      await value.press('Enter');
      await expect.soft(builder).toHaveAnnounced('Updated device host-00');
      await expect.soft(value).toBeFocused();
      await expect.soft(section.locator('.control--changed')).toHaveCount(0);
      await expect
        .soft(summary)
        .toHaveText(
          'More settings: miniccc commands, Delay (1), Injections, Advanced settings (1), Labels (1), Annotations (1)',
        );
      await expect
        .soft(labels.getByLabel('Label 1 Name'))
        .toHaveValue('ntp-server');
      await expect.soft
        .poll(async () => {
          const {
            labels: tags,
            annotations: hints,
            advanced: settings,
            ...rest
          } = await spec();

          return {
            labels: tags,
            annotations: hints,
            advanced: settings,
            description: rest.general.description,
            mac: rest.network.interfaces[0].mac,
            timer: rest.delay?.timer,
          };
        }, PERSIST)
        .toEqual({
          labels: { 'ntp-server': 'eth0' },
          annotations: { 'phenix/default-apps': false },
          advanced: { 'qemu-append': '-vga std' },
          description: 'Imported',
          mac: '',
          timer: '1h30m',
        });
    });

    expectNoFatal(issues);
  });

  // A save's answer stamps the document. The Inspector must not reload its
  // form and drop text being typed.
  test('text being typed in a field is kept when a save is answered', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    await builder.open();
    await builder.createBlank();
    // Times are whole seconds, so the save below is answered in a later second
    // than the draft was made in.
    const made = Date.now();
    const edited = page.getByTestId('inspector-edited').locator('time');
    const before = await edited.getAttribute('datetime');

    // The save waits here until the test lets it go.
    let release = () => {};
    const held = new Promise((resolve) => {
      release = resolve;
    });
    let waiting = 0;
    await page.route(SNAPSHOTS, async (route) => {
      if (route.request().method() === 'POST') {
        waiting += 1;
        await held;
      }

      return route.fallback();
    });

    const saved = builder.nextSnapshot();
    await builder.rename(uniqueName(testInfo, 'typing'));

    // Typed and not left: the field commits its text when it loses focus.
    const description = builder.inspector.getByLabel('Description');
    await description.click();
    await description.pressSequentially('typed while saving');
    await expect.poll(() => waiting).toBeGreaterThan(0);

    await nextSecond(made);
    release();

    const { stamp } = await (await saved).json();
    await builder.waitSaved();

    // The answer reached the document, with a later time than it had.
    expect(stamp.updatedAt, 'time of the save').not.toBe(before);
    await expectDetail(page, 'edited', stamp.updatedBy, stamp.updatedAt, {
      soft: false,
    });
    await expect(description).toHaveValue('typed while saving');

    expectNoFatal(issues);
  });

  // A layout landing must not reload the form and drop text being typed.
  // Apply then records the text as its own Undo step.
  test('text being typed in a field is kept when a layout lands', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await newDraft(builder, 'device', 'switch');
    await connectFirst(builder, draft);
    await builder.selectNode('node');
    await expect(subject(builder)).toHaveText(/^\s*Device node\b/);
    await builder.waitSaved();

    // ELK loads as the first layout runs. Held back, the layout is under
    // way until the test lets it go.
    let release = () => {};
    const released = new Promise((resolve) => {
      release = resolve;
    });
    const elk = /elk-(api|worker)/;
    await page.route(elk, async (route) => {
      await released;
      await route.continue();
    });

    const layoutButton = builder.toolbar('layout');
    await layoutButton.click();
    await page
      .getByRole('menuitemradio', { name: 'ELK layered', exact: true })
      .click();
    await expect(layoutButton).toHaveAttribute('aria-busy', 'true');

    // Typed and not left: the field commits its text when it loses focus.
    const description = specGroup(builder, 'General').getByLabel(
      /^Description/,
    );
    await description.click();
    await description.pressSequentially('typed during the layout');

    release();
    await expect(builder).toHaveAnnounced('Applied ELK layered layout');
    await page.unroute(elk);
    await expect(layoutButton).not.toHaveAttribute('aria-busy');

    await expect(description).toHaveValue('typed during the layout');
    await expect(description).toBeFocused();
    await expect(subject(builder)).toHaveText(/^\s*Device node\b/);

    // Apply keeps the layout, and adds the text as its own step.
    const described = (doc) => ({
      description: nodeOf(doc, 'device').device.spec.general?.description || '',
      layout: doc.layout,
    });

    await applyEdits(builder);
    await expect(builder).toHaveAnnounced('Updated device node');
    await builder.persisted(draft, described, {
      description: 'typed during the layout',
      layout: 'elk',
    });

    await builder.toolbar('undo').click();
    await expect(builder).toHaveAnnounced('Undid Updated device node');
    await builder.persisted(draft, described, {
      description: '',
      layout: 'elk',
    });

    expectNoFatal(issues);
  });

  // Diagram notes are written when their box is left. Delete or clearing
  // removes one, and one too long is never written.
  test('diagram notes are kept by the draft, and a deleted one stays deleted', async ({
    page,
    builder,
    issues,
  }) => {
    await builder.open();
    const draft = await builder.createBlank();
    const section = builder.inspector.getByTestId('inspector-notes');
    const box = (number) =>
      section.getByRole('textbox', { name: `Note ${number}`, exact: true });
    const first = 'The core switch sits in rack 4.';
    const second = 'Firewall rules are reviewed\nevery Monday.';

    await test.step('two notes are added from the keyboard and written', async () => {
      await expect.soft(section.getByRole('heading')).toHaveText('Notes');
      await expect.soft(section).toContainText('No notes.');

      for (const [index, text] of [first, second].entries()) {
        await section.getByTestId('inspector-note-add').press('Enter');
        await expect(box(index + 1)).toBeFocused();
        await box(index + 1).fill(text);
        await box(index + 1).blur();
        await expect.soft(builder).toHaveAnnounced('Updated diagram notes');
      }

      await builder.persisted(draft, (doc) => doc.metadata.notes, [
        first,
        second,
      ]);
    });

    await test.step('Notes come before Scenarios, and a box is as wide as the Inspector, two lines tall, and grows to fit its text', async () => {
      // The lines of text a box shows, without scrolling.
      const lines = (locator) =>
        locator.evaluate((element) => {
          const style = getComputedStyle(element);
          const padding =
            Number.parseFloat(style.paddingTop) +
            Number.parseFloat(style.paddingBottom);

          return Math.round(
            (element.clientHeight - padding) /
              Number.parseFloat(style.lineHeight),
          );
        });
      const scrolls = (locator) =>
        locator.evaluate(
          (element) => element.scrollHeight > element.clientHeight + 1,
        );
      // How far the widths of two boxes are apart, in pixels.
      const widthApart = async (one, other) => {
        const [a, b] = await Promise.all([
          one.boundingBox(),
          other.boundingBox(),
        ]);

        return Math.abs(a.width - b.width);
      };
      const lineTexts = (count) =>
        Array.from({ length: count }, (_, at) => `line ${at + 1}`).join('\n');

      // Notes come before Scenarios on the page and in the Tab order.
      const scenarios = builder.inspector.getByTestId('inspector-scenario');
      expect
        .soft(
          await section.evaluate(
            (notes, other) =>
              Boolean(
                notes.compareDocumentPosition(other) &
                  Node.DOCUMENT_POSITION_FOLLOWING,
              ),
            await scenarios.elementHandle(),
          ),
        )
        .toBe(true);
      await section.getByTestId('inspector-note-add').focus();
      await page.keyboard.press('Tab');
      await expect
        .soft(builder.inspector.getByTestId('inspector-scenario-edit'))
        .toBeFocused();

      const description = builder.inspector.getByLabel('Description');

      // Two lines tall, and as wide as the section a note is in, and as
      // the diagram's Name.
      expect.soft(await lines(box(1))).toBe(2);
      expect.soft(await widthApart(box(1), section)).toBeLessThan(2);
      expect.soft(await lines(description)).toBe(2);
      expect
        .soft(
          await widthApart(description, builder.inspector.getByLabel('Name')),
        )
        .toBeLessThan(2);

      // Typed text makes the box taller, up to eight lines. Then it
      // scrolls.
      await box(1).fill(lineTexts(5));
      expect.soft(await lines(box(1))).toBe(5);
      await box(1).fill(lineTexts(20));
      expect.soft(await lines(box(1))).toBe(8);
      expect.soft(await scrolls(box(1))).toBe(true);
      await description.fill(lineTexts(4));
      expect.soft(await lines(description)).toBe(4);

      // The text as it was, written again.
      await box(1).fill(first);
      await box(1).blur();
      expect.soft(await lines(box(1))).toBe(2);
      await description.fill('');
      await description.blur();
      expect.soft(await lines(description)).toBe(2);
      await builder.persisted(draft, (doc) => doc.metadata.notes, [
        first,
        second,
      ]);
    });

    await test.step('the draft opened again shows both', async () => {
      await builder.backToDrafts();
      await builder.openDraft(draft);
      await expect(box(1)).toHaveValue(first);
      await expect(box(2)).toHaveValue(second);
      await expect.soft(section).not.toContainText('No notes.');
    });

    await test.step('Delete takes one away, and it stays away', async () => {
      await section
        .getByRole('button', { name: 'Delete note 1', exact: true })
        .click();
      await expect(box(1)).toHaveValue(second);
      await expect(box(2)).toHaveCount(0);
      await builder.persisted(draft, (doc) => doc.metadata.notes, [second]);

      await page.reload();
      await builder.openDraft(draft);
      await expect(box(1)).toHaveValue(second);
      await expect(box(2)).toHaveCount(0);
    });

    await test.step('a note too long to save shows its error, and the draft keeps the note as saved', async () => {
      // fill puts the whole text in at once, as a paste does: 4097 bytes,
      // one past the limit.
      await box(1).fill('x'.repeat(4097));

      const error = section.getByTestId('inspector-note-error-1');

      await expect(error).toBeVisible();
      await expect.soft(error).toHaveAttribute('role', 'alert');
      await expect
        .soft(error)
        .toContainText('A note holds at most 4096 bytes in UTF-8');
      await expect(box(1)).toHaveAttribute('aria-invalid', 'true');

      const errorId = await error.getAttribute('id');

      expect(errorId).toMatch(/^inspector-note-error-/);
      await expect(box(1)).toHaveAttribute('aria-describedby', errorId);

      // Leaving the box writes nothing: the text stays, with its error.
      await box(1).blur();
      await expect(box(1)).toHaveValue('x'.repeat(4097));
      await expect(error).toBeVisible();
      await builder.waitSaved();
      await builder.persisted(draft, (doc) => doc.metadata.notes, [second]);

      await page.reload();
      await builder.openDraft(draft);
      await expect(box(1)).toHaveValue(second);
      await expect(box(1)).not.toHaveAttribute('aria-invalid', 'true');
      await expect(section.getByRole('alert')).toHaveCount(0);
    });

    await test.step('a note cleared and left is removed, and stays removed', async () => {
      await box(1).fill('');
      await box(1).blur();
      await expect(box(1)).toHaveCount(0);
      await expect(section).toContainText('No notes.');
      await builder.persisted(draft, (doc) => 'notes' in doc.metadata, false);

      await page.reload();
      await builder.openDraft(draft);
      await expect(section).toContainText('No notes.');
      await expect(box(1)).toHaveCount(0);
    });

    expectNoFatal(issues);
  });

  // A new device has no interfaces. Go to on its warning focuses Add
  // interface in the node's Interfaces list.
  test('Go to the warning of a device with no interfaces focuses Add interface', async ({
    page,
    builder,
    issues,
  }) => {
    await newDraft(builder, 'device');
    await page.getByTestId('builder-checks').click();
    const checks = page.getByTestId('checks-dialog');
    const goTo = checks.getByRole('button', {
      name: /^Go to device [^:]+: device "[^"]+" has no interfaces/,
    });

    await expect(checks.getByTestId('issue-code')).toContainText([
      'device.interfaces.none',
    ]);
    await goTo.click();
    await expect(checks).toHaveCount(0);
    await expect(
      builder.inspector
        .getByRole('group', { name: 'Interfaces', exact: true })
        .getByRole('button', { name: 'Add interface' }),
    ).toBeFocused();

    expectNoFatal(issues);
  });

  // A device's layer applies at once. Its choices are the diagram's layers,
  // or the 7 placeholder layers.
  test('the layer of a device applies at once and is kept by the draft', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await newDraft(builder, 'device', 'switch');
    await builder.selectNode('node');

    const layer = builder.inspector.getByLabel('Layer', { exact: true });

    await expect
      .soft(builder.inspector.locator('h3'))
      .toContainText(['Layer', 'Connection points', 'Position']);
    await expect.soft(layer).toHaveValue('');
    await expect
      .soft(layer)
      .toHaveAccessibleDescription(
        'The By layer layout puts higher layer IDs above lower ones.',
      );
    await expect
      .soft(layer.locator('option'))
      .toHaveText([
        'None',
        'Layer 6',
        'Layer 5',
        'Layer 4',
        'Layer 3',
        'Layer 2',
        'Layer 1',
        'Layer 0',
      ]);

    await layer.selectOption({ label: 'Layer 1' });
    await expect
      .soft(builder)
      .toHaveAnnounced('Changed the layer of Device node to Layer 1');
    await expect(builder.inspector).not.toContainText('Unapplied changes');
    await builder.persisted(
      draft,
      (doc) => nodeOf(doc, 'device').device.layer,
      1,
    );
    // The placeholder layers are not written into the diagram.
    expect.soft('layers' in (await builder.serverDocument(draft))).toBe(false);

    await layer.selectOption({ label: 'None' });
    await builder.persisted(
      draft,
      (doc) => 'layer' in nodeOf(doc, 'device').device,
      false,
    );

    await expectAccessible(page, {
      soft: true,
      label: 'axe on the layer section',
    });
    expectNoFatal(issues);
  });

  // The toggle beside Description and the setting Show node descriptions
  // change only what the canvas draws.
  test('a device’s description shows or hides on the canvas by its toggle and by the setting', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const draft = await builder.seedDraft(
      appliedDeviceDocument(uniqueName(testInfo, 'described')),
    );
    await builder.openDraft(draft);
    await builder.selectNode('web01');

    const node = builder.node('web01', 'device');
    const comment = node.locator('.builder-node__comment');
    const toggle = builder.inspector.getByRole('button', {
      name: 'Show description on the canvas',
    });
    const hidden = (doc) => 'hideDescription' in nodeOf(doc, 'device').device;

    await test.step('the toggle hides the description at once, as one edit', async () => {
      await expect(comment).toHaveText('Front end web server');
      await expect.soft(toggle).toHaveAttribute('aria-pressed', 'true');
      await toggle.click();
      await expect
        .soft(builder)
        .toHaveAnnounced('Hid the description of Device web01 on the canvas');
      await expect(comment).toHaveCount(0);
      await expect.soft(toggle).toHaveAttribute('aria-pressed', 'false');
      await expect(builder.inspector).not.toContainText('Unapplied changes');
      await builder.persisted(draft, hidden, true);
      expect
        .soft(nodeOf(await builder.serverDocument(draft), 'device').device)
        .toMatchObject({ hideDescription: true });

      await toggle.press('Enter');
      await expect(comment).toHaveText('Front end web server');
      await builder.persisted(draft, hidden, false);
    });

    await test.step('Hide node descriptions hides every description and changes no node', async () => {
      await builder.canvas.focus();
      await page.keyboard.press('ControlOrMeta+k');
      const search = page.getByRole('combobox', { name: /Search commands/ });

      await search.fill('Hide node descriptions');
      await expect(
        page
          .getByTestId('commands-dialog')
          .getByRole('option', { selected: true }),
      ).toContainText('Hide node descriptions');
      await page.keyboard.press('Enter');
      await expect.soft(builder).toHaveAnnounced('Node descriptions hidden.');
      await expect(comment).toHaveCount(0);
      expect.soft(hidden(await builder.serverDocument(draft))).toBe(false);

      await builder.selectNode('web01');
      await expect
        .soft(builder.inspector.getByTestId('inspector-field-note'))
        .toContainText('because your Show node descriptions setting is off');
      await expect.soft(toggle).toHaveAttribute('aria-pressed', 'true');
    });

    await test.step('the setting in Settings shows them again', async () => {
      await page.getByTestId('editor-settings').click();
      const settings = page.getByTestId('settings-dialog');
      const setting = settings.getByTestId('settings-node-descriptions');

      await expect(setting).not.toBeChecked();
      await setting.click();
      await expect(setting).toBeChecked();
      await settings.getByTestId('settings-done').click();
      await expect(comment).toHaveText('Front end web server');
    });

    await expectAccessible(page, {
      soft: true,
      label: 'axe on the description toggle',
    });
    expectNoFatal(issues);
  });

  // Each field of the layers editor is one undo step when left. Bad values
  // write nothing, and a removed layer's devices keep its ID.
  test('the layers editor edits the layers of the diagram', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await newDraft(builder, 'device', 'switch');
    await builder.selectNode('node');

    const layer = builder.inspector.getByLabel('Layer', { exact: true });
    await layer.selectOption({ label: 'Layer 1' });

    const edit = builder.inspector.getByRole('button', {
      name: 'Edit layers…',
    });
    await expect.soft(edit).toHaveAttribute('aria-haspopup', 'dialog');
    await edit.click();

    const dialog = page.getByTestId('layers-dialog');
    const status = dialog.getByTestId('layers-status');
    const rows = dialog.getByTestId('layer-row');
    await expect(dialog).toBeVisible();
    await expect.soft(dialog).toHaveAccessibleName('Layers of this diagram');
    await expect.soft(dialog.getByTestId('layers-placeholders')).toBeVisible();
    await expect.soft(rows).toHaveCount(7);
    await expect
      .soft(dialog.getByLabel('Layer 1 label'))
      .toHaveAccessibleDescription('1 node is at this layer.');
    await expect
      .soft(dialog.getByTestId('layers-link'))
      .toHaveText('These layers are not linked to a layer set.');

    await test.step('the first change writes the placeholder layers into the diagram', async () => {
      const label = dialog.getByLabel('Layer 1 label');
      await label.fill('Basic control');
      await label.press('Tab');
      await expect.soft(status).toHaveText('Renamed Layer 1 to Basic control.');
      await builder.persisted(
        draft,
        (doc) => doc.layers.map(({ id, label: text }) => `${id} ${text}`),
        [
          '6 Layer 6',
          '5 Layer 5',
          '4 Layer 4',
          '3 Layer 3',
          '2 Layer 2',
          '1 Basic control',
          '0 Layer 0',
        ],
      );
      await expect
        .soft(dialog.getByTestId('layers-placeholders'))
        .toHaveCount(0);
    });

    await test.step('an ID in use, out of range or a blank label shows an error and writes nothing', async () => {
      const id = dialog.getByLabel('Layer 2 ID');
      await id.fill('1');
      await id.press('Tab');
      await expect
        .soft(dialog.getByTestId('layer-id-error'))
        .toHaveText('Layer ID 1 is in use. Enter a different ID.');
      await expect.soft(id).toHaveAttribute('aria-invalid', 'true');
      await expect
        .soft(id)
        .toHaveAccessibleDescription(
          'Layer ID 1 is in use. Enter a different ID.',
        );
      await id.fill('120');
      await id.press('Tab');
      await expect
        .soft(dialog.getByTestId('layer-id-error'))
        .toHaveText('Enter a layer ID from 0 to 99.');
      await id.fill('2');
      await id.press('Tab');
      await expect.soft(dialog.getByTestId('layer-id-error')).toHaveCount(0);
      await expect.soft(id).not.toHaveAttribute('aria-invalid');

      const label = dialog.getByLabel('Layer 3 label');
      await label.fill('  ');
      await label.press('Tab');
      await expect
        .soft(dialog.getByTestId('layer-label-error'))
        .toHaveText('Type a label for the layer.');
      await label.fill('Operations');
      await label.press('Tab');
      await expect.soft(dialog.getByTestId('layer-label-error')).toHaveCount(0);
      await builder.persisted(
        draft,
        (doc) => doc.layers.find((entry) => entry.id === 3)?.label,
        'Operations',
      );
    });

    await test.step('Add layer takes the next free ID, and focus goes to its label', async () => {
      await dialog.getByRole('button', { name: 'Add layer' }).click();
      await expect.soft(status).toHaveText('Added Layer 7.');
      await expect.soft(rows).toHaveCount(8);
      await expect.soft(dialog.getByLabel('Layer 7 label')).toBeFocused();
      await expect.soft(rows.first()).toHaveAttribute('data-layer-id', '7');
    });

    await test.step('a new ID moves the layer, its devices and its row, and focus stays', async () => {
      const id = dialog.getByLabel('Layer 1 ID');
      await id.fill('9');
      await id.press('Tab');
      await expect
        .soft(status)
        .toHaveText('Changed the ID of layer Basic control to 9.');
      await expect.soft(rows.first()).toHaveAttribute('data-layer-id', '9');
      await expect.soft(dialog.getByLabel('Icon of layer 9')).toBeFocused();
      await builder.persisted(
        draft,
        (doc) => nodeOf(doc, 'device').device.layer,
        9,
      );
    });

    await test.step('the icon and the color of a layer apply at once', async () => {
      await dialog.getByLabel('Icon of layer 9').click();
      await page.getByTestId('layer-icon-item-plc').click();
      await expect
        .soft(status)
        .toHaveText('Changed the icon of layer Basic control.');
      const color = dialog.getByLabel('Color of layer 9', { exact: true });
      await color.fill('blue');
      await color.press('Tab');
      await expect
        .soft(dialog.getByTestId('layer-color-error'))
        .toHaveText('A layer color is # and 6 hex digits, such as #2f6fbf.');
      await color.fill('#0f766e');
      await color.press('Tab');
      await expect
        .soft(status)
        .toHaveText('Changed the color of layer Basic control.');
      await builder.persisted(
        draft,
        (doc) => doc.layers.find((entry) => entry.id === 9),
        { id: 9, label: 'Basic control', iconKey: 'plc', color: '#0f766e' },
      );
    });

    await test.step('a removed layer leaves its devices at its ID', async () => {
      await dialog
        .getByRole('button', { name: 'Remove layer 9 – Basic control' })
        .click();
      await expect
        .soft(status)
        .toHaveText('Removed layer Basic control. 1 node stays at Layer 9.');
      await expect.soft(rows).toHaveCount(7);
      await expect
        .soft(dialog.getByTestId('layers-unlisted'))
        .toContainText('not in this list: 9.');
      await builder.persisted(
        draft,
        (doc) => [
          nodeOf(doc, 'device').device.layer,
          doc.layers.some((entry) => entry.id === 9),
        ],
        [9, false],
      );
    });

    await dialog.getByTestId('layers-done').click();
    await expect(dialog).toBeHidden();
    await expect.soft(edit).toBeFocused();
    await expect
      .soft(layer.locator('option'))
      .toHaveText([
        'None',
        'Layer 9',
        'Layer 7',
        'Layer 6',
        'Layer 5',
        'Layer 4',
        'Operations',
        'Layer 2',
        'Layer 0',
      ]);
    await expect.soft(layer).toHaveValue('9');

    await test.step('Settings has no layers: its Edit layers… opens the layers editor', async () => {
      await page.getByTestId('editor-settings').click();
      const settings = page.getByTestId('settings-dialog');
      await expect(settings).toBeVisible();
      await expect.soft(settings.getByTestId('layer-row')).toHaveCount(0);
      await settings.getByTestId('settings-edit-layers').click();
      await expect(settings).toBeHidden();
      await expect(dialog).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(dialog).toBeHidden();
      await expect.soft(page.getByTestId('editor-settings')).toBeFocused();
    });

    expectNoFatal(issues);
  });

  // The layers editor scrolls, and Done stays in view, on a narrow screen
  // and on a large one. Nothing scrolls sideways.
  test('the layers editor fits a narrow and a large window', async ({
    page,
    builder,
    issues,
  }) => {
    await newDraft(builder);
    const dialog = page.getByTestId('layers-dialog');

    for (const size of [
      { width: 320, height: 640 },
      { width: 1440, height: 1100 },
    ]) {
      await test.step(`${size.width}x${size.height}`, async () => {
        await page.setViewportSize(size);
        await page.keyboard.press('ControlOrMeta+k');
        await page
          .getByRole('combobox', { name: /Search commands/ })
          .fill('Edit layers');
        await page.keyboard.press('Enter');

        await expect(dialog).toBeVisible();
        await expect.soft(dialog.getByTestId('layers-done')).toBeInViewport();
        expect
          .soft(
            await dialog
              .locator('.builder-layers-dialog__body')
              .evaluate((body) => body.scrollWidth - body.clientWidth),
            'no sideways scroll',
          )
          .toBeLessThanOrEqual(0);
        await dialog.getByTestId('layer-add').scrollIntoViewIfNeeded();
        await expect.soft(dialog.getByTestId('layer-add')).toBeInViewport();
        await expect.soft(dialog.getByTestId('layers-done')).toBeInViewport();
        // Each field of a row is as wide as it needs to be read.
        const label = dialog.getByLabel('Layer 6 label');
        expect
          .soft((await label.boundingBox()).width, 'label width')
          .toBeGreaterThan(150);
        await dialog.getByTestId('layers-done').click();
        await expect(dialog).toBeHidden();
      });
    }
    expectNoFatal(issues);
  });
});

// The Node Editor: one node's Inspector fields in a large dialog, opened four
// ways, applied as one undo step, focus back to the node.
test.describe('Node Editor', () => {
  test('opens by double-click, button, keys and palette, and applies its edits as one undo step', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await newDraft(builder, 'device', 'switch');
    const editor = page.getByTestId('node-editor');
    const title = editor.getByRole('heading', { level: 2 });
    const hostname = editor.getByLabel(/^Hostname/).first();
    const description = editor
      .locator('legend.group-label', { hasText: /^General$/ })
      .locator('xpath=..')
      .getByLabel(/^Description/);
    const cancel = editor.getByTestId('node-editor-cancel');
    const open = builder.inspector.getByRole('button', {
      name: 'Open Node Editor',
    });
    const device = (doc) => nodeOf(doc, 'device').device;

    // The dialog names the node, and the first field has focus. Cancel
    // with no changes closes it, and focus goes back to the node.
    const expectOpen = async (how) => {
      await expect(editor, how).toBeVisible();
      await expect.soft(title, how).toHaveText('Edit device node');
      await expect.soft(hostname, how).toBeFocused();
    };
    const close = async (how, label = 'node') => {
      await cancel.click();
      await expect(editor, how).toHaveCount(0);
      await expect.soft(builder.canvasItem(label), how).toBeFocused();
    };

    await test.step('a double-click on the node opens it', async () => {
      await builder.canvasItem('node').dblclick();
      await expectOpen('double-click');
      // The fields have room: the dialog is wider than the Inspector.
      expect
        .soft((await editor.boundingBox()).width)
        .toBeGreaterThan((await builder.inspector.boundingBox()).width);
      await expectAccessible(page, {
        include: '[data-testid="node-editor"]',
        soft: true,
        label: 'axe on the Node Editor',
      });
      await close('double-click');
    });

    await test.step('Open Node Editor in the Inspector opens it, and its tooltip names its keys', async () => {
      await builder.selectNode('node');
      await open.hover();
      await expect
        .soft(page.getByTestId('inspector-head-tooltip'))
        .toHaveText(/^Open Node Editor \((⌥⇧E|Alt\+Shift\+E)\)$/);
      await open.click();
      await expectOpen('button');
      await close('button');
    });

    await test.step('Alt+Shift+E on the node opens it', async () => {
      await builder.canvasItem('node').focus();
      await page.keyboard.press('Alt+Shift+KeyE');
      await expectOpen('keys');
      await close('keys');
    });

    await test.step('Escape with changes asks first, and Discard drops them', async () => {
      await page.keyboard.press('Alt+Shift+KeyE');
      await expectOpen('keys again');
      await hostname.fill('dropped');
      await page.keyboard.press('Escape');
      // The first Escape may only close the field's tooltip.
      if (!(await editor.getByTestId('node-editor-keep').isVisible())) {
        await page.keyboard.press('Escape');
      }
      await expect(editor.getByTestId('node-editor-keep')).toBeFocused();
      await expect
        .soft(editor.getByTestId('node-editor-discard'))
        .toContainText('Discard changes to this node?');
      await editor.getByTestId('node-editor-discard-confirm').click();
      await expect(editor).toHaveCount(0);
      await expect.soft(builder.canvasItem('node')).toBeFocused();
    });

    await test.step('the command palette opens it, and Apply changes two fields in one undo step', async () => {
      await page.keyboard.press('ControlOrMeta+k');
      await page
        .getByRole('combobox', { name: /Search commands/ })
        .fill('Open Node Editor');
      await page
        .getByTestId('commands-dialog')
        .getByRole('option', { name: /^Open Node Editor/ })
        .first()
        .click();
      await expectOpen('palette');

      await hostname.fill('plc-01');
      await description.fill('Line 1 controller');
      await editor.getByTestId('node-editor-apply').click();
      await expect(editor).toHaveCount(0);
      await expect
        .soft(builder)
        .toHaveAnnounced(/Applied changes to Device node/);
      await expect.soft(builder.canvasItem('plc-01')).toBeFocused();
      await builder.persisted(
        draft,
        (doc) => [device(doc).hostname, device(doc).spec.general.description],
        ['plc-01', 'Line 1 controller'],
      );

      // One undo takes back both fields.
      await builder.canvasItem('plc-01').focus();
      await page.keyboard.press('ControlOrMeta+z');
      await expect(builder.canvasItem('node')).toBeVisible();
      await builder.persisted(
        draft,
        (doc) => [
          device(doc).hostname,
          device(doc).spec.general.description || '',
        ],
        ['node', ''],
      );
    });

    await test.step('the Inspector applies its own edits before the editor opens', async () => {
      await builder.selectNode('node');
      await deviceHostname(builder).fill('plc-02');
      await open.click();
      await expect(editor).toBeVisible();
      await expect.soft(title).toHaveText('Edit device plc-02');
      await expect.soft(hostname).toHaveValue('plc-02');
      await close('settled', 'plc-02');
    });

    expectNoFatal(issues);
  });

  // The Inspector's header: Add connection and Move to group where they apply,
  // then Open Node Editor, all one size.
  test('the Inspector’s header has Add connection and Move to group beside Open Node Editor, all one size', async ({
    page,
    builder,
    issues,
  }) => {
    await newDraft(builder, 'device', 'switch', 'group');
    const head = builder.inspector.locator('.builder-inspector__head');
    const buttons = head.getByRole('button');
    const connect = builder.inspectorButton('connect');
    const regroup = builder.inspectorButton('regroup');
    const open = builder.inspector.getByTestId('inspector-node-editor');
    const tooltip = page.getByTestId('inspector-head-tooltip');
    // The header's buttons have these names, in this order.
    // toHaveAccessibleName takes one name, not a list.
    const expectNames = async (names) => {
      await expect.soft(buttons).toHaveCount(names.length);
      for (const [index, name] of names.entries()) {
        await expect.soft(buttons.nth(index)).toHaveAccessibleName(name);
      }
    };
    const accent = () =>
      page.locator('.builder-root').evaluate((root) => {
        const probe = document.createElement('span');

        probe.style.color = 'var(--bx-accent)';
        root.append(probe);
        const { color } = getComputedStyle(probe);
        probe.remove();

        return color;
      });

    await test.step('a device shows all three, in order, named and described by their tooltips', async () => {
      await builder.selectNode('node');
      await expect(buttons).toHaveCount(3);
      await expectNames([
        'Add connection',
        'Move to group',
        'Open Node Editor',
      ]);
      for (const button of [connect, regroup, open]) {
        await expect.soft(button).toHaveAttribute('aria-haspopup', 'dialog');
      }
      await connect.hover();
      await expect.soft(tooltip).toHaveText('Add connection');
      await regroup.hover();
      await expect.soft(tooltip).toHaveText('Move to group');
    });

    await test.step('the three are one size, and Open Node Editor is filled in the accent color', async () => {
      const boxes = await Promise.all(
        [connect, regroup, open].map((button) => button.boundingBox()),
      );
      const icons = await Promise.all(
        [connect, regroup, open].map((button) =>
          button.locator('.builder-icon').boundingBox(),
        ),
      );

      // One size, at least 24 by 24 pixels (WCAG 2.5.8), with one icon size.
      for (const [index, box] of boxes.entries()) {
        expect.soft(box.width, `width ${index}`).toBeGreaterThanOrEqual(24);
        expect.soft(box.height, `height ${index}`).toBeGreaterThanOrEqual(24);
        expect.soft(box.width, `width ${index}`).toBeCloseTo(boxes[0].width, 0);
        expect
          .soft(icons[index].width, `icon ${index}`)
          .toBeCloseTo(icons[0].width, 0);
      }
      await expect.soft(open).toHaveCSS('background-color', await accent());
      await expect
        .soft(connect)
        .not.toHaveCSS('background-color', await accent());
      const [icon] = await contrast(open.locator('.builder-icon'));

      expect.soft(icon.ratio, 'icon on the accent').toBeGreaterThanOrEqual(3);
    });

    await test.step('a group has Move to group only, and a connection none', async () => {
      await builder.selectNode('Group');
      await expectNames(['Move to group', 'Open Node Editor']);
      await builder.connect({ label: 'node' }, { index: 1 });
      const edge = page.locator('g.vue-flow__edge').first();

      await edge.focus();
      await page.keyboard.press('Escape');
      await page.keyboard.press('Enter');
      await expect(edge).toHaveClass(/\bselected\b/);
      await expect.soft(buttons).toHaveCount(0);
    });

    await test.step('the header fits a 320 pixel wide window and a 1440 by 1100 one', async () => {
      await builder.selectNode('node');
      await expect(buttons).toHaveCount(3);
      for (const [width, height] of [
        [320, 800],
        [1440, 1100],
      ]) {
        await page.setViewportSize({ width, height });
        await head.scrollIntoViewIfNeeded();
        const fits = await head.evaluate(
          (element) => element.scrollWidth <= element.clientWidth + 1,
        );

        expect.soft(fits, `${width}: no sideways scroll`).toBe(true);
        for (const button of [connect, regroup, open]) {
          await expect.soft(button, `${width}`).toBeInViewport({ ratio: 1 });
        }
      }
    });

    expectNoFatal(issues);
  });

  test('waits for the Inspector’s edits, keeps its own edits across a reload, and opens read only in a published diagram', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const draft = await newDraft(builder, 'device');
    const editor = page.getByTestId('node-editor');
    const title = editor.getByRole('heading', { level: 2 });
    const hostname = editor.getByLabel(/^Hostname/).first();
    const open = builder.inspector.getByRole('button', {
      name: 'Open Node Editor',
    });

    await test.step('with no node selected, the palette asks for a node and opens the editor on it', async () => {
      await builder.selectNode('node');
      await page.keyboard.press('Escape');
      await expect.soft(builder).toHaveAnnounced('Selection cleared');
      await page.keyboard.press('ControlOrMeta+k');
      const palette = page.getByTestId('commands-dialog');
      const field = palette.getByRole('combobox');

      await field.fill('Open Node Editor');
      await page.keyboard.press('Enter');
      await expect
        .soft(page.getByTestId('command-palette-step'))
        .toHaveText('Open Node Editor ›');
      await expect
        .soft(field)
        .toHaveAccessibleName('Open Node Editor: choose a node');
      await field.fill('node');
      await page.keyboard.press('Enter');
      await expect(palette).toHaveCount(0);
      await expect(editor).toBeVisible();
      await expect.soft(title).toHaveText('Edit device node');
      await editor.getByTestId('node-editor-cancel').click();
      await expect(editor).toHaveCount(0);
      // The chosen node is selected, and focus goes back to it.
      await expect.soft(builder.canvasItem('node')).toBeFocused();
      await expect
        .soft(builder.canvasItem('node'))
        .toHaveAttribute('aria-pressed', 'true');
    });

    await test.step('edits the Inspector cannot apply keep it closed', async () => {
      await builder.selectNode('node');
      await setNumber(builder, 'VCPUs', 0);
      await open.click();
      await expect
        .soft(builder)
        .toHaveAnnounced(
          'Could not open the Node Editor: 1 field needs attention in the Inspector.',
        );
      await expect.soft(editor).toHaveCount(0);
      await expect
        .soft(
          builder.inspector.locator('.builder-inspector__error-link').first(),
        )
        .toBeFocused();
      await builder.inspector.getByTestId('inspector-cancel').click();
    });

    await test.step('a reload saves the edits the editor holds unapplied', async () => {
      await builder.canvasItem('node').dblclick();
      await expect(editor).toBeVisible();
      await hostname.fill('plc-03');
      await builder.editAndReload(draft, {
        // Focus stays in the field, which has not committed its text yet.
        expectSaved: () => expect.soft(hostname).toBeFocused(),
        expectAfterReload: async (reopened) => {
          await expect(reopened.canvasItem('plc-03')).toBeVisible();
        },
      });
      await builder.persisted(
        draft,
        (doc) => nodeOf(doc, 'device').device.hostname,
        'plc-03',
      );
    });

    await test.step('a published diagram, shown read only, opens it read only, with Close only', async () => {
      const name = uniqueName(testInfo, 'inspector-read-only');
      const { documentId } = await publishTopology(
        builder.request,
        builder.tracker,
        name,
        labDocument(name, { hostnames: ['plc-03'] }),
        { keepDraft: false },
      );

      await builder.open();
      await page.getByTestId('drafts-tab-published').click();
      await page.getByTestId(`draft-open-${documentId}`).click();
      await expect(builder.canvas).toBeVisible();
      await expect(page.getByTestId('builder-published')).toBeVisible();

      await builder.canvasItem('plc-03').dblclick();
      await expect(editor).toBeVisible();
      await expect.soft(title).toHaveText('Device plc-03');
      await expect.soft(editor.getByTestId('node-editor-apply')).toHaveCount(0);
      await editor.getByRole('button', { name: 'Close', exact: true }).click();
      await expect(editor).toHaveCount(0);
      await expect.soft(builder.canvasItem('plc-03')).toBeFocused();
    });

    expectNoFatal(issues);
  });

  // The layers editor opens over the Node Editor, which keeps its edits and
  // offers a new layer at once.
  test('Edit layers… opens the layers editor over it, and the Node Editor stays open', async ({
    page,
    builder,
    issues,
  }) => {
    await newDraft(builder, 'device', 'switch');
    await builder.selectNode('node');
    await builder.inspector
      .getByRole('button', { name: 'Open Node Editor' })
      .click();

    const editor = page.getByTestId('node-editor');
    const layers = page.getByTestId('layers-dialog');
    const hostname = editor.getByLabel(/^Hostname/).first();
    const edit = editor.getByRole('button', { name: 'Edit layers…' });
    await expect(editor).toBeVisible();
    await hostname.fill('plc-01');

    await edit.click();
    await expect(layers).toBeVisible();
    await layers.getByRole('button', { name: 'Add layer' }).click();
    await expect
      .soft(layers.getByTestId('layers-status'))
      .toHaveText('Added Layer 7.');
    await page.keyboard.press('Escape');
    await expect(layers).toBeHidden();
    await expect.soft(editor).toBeVisible();
    await expect.soft(edit).toBeFocused();
    await expect.soft(hostname).toHaveValue('plc-01');
    await expect
      .soft(editor.getByLabel('Layer', { exact: true }).locator('option'))
      .toContainText(['None', 'Layer 7']);
    expectNoFatal(issues);
  });

  // Sections and entries collapse, a table of contents goes to each, errors
  // show on collapsed ones, and Reset view expands them.
  test('collapses sections and entries, goes to them from a table of contents, and marks their errors', async ({
    page,
    builder,
    issues,
  }) => {
    await page.setViewportSize({ width: 1440, height: 1100 });
    await newDraft(builder, 'device', 'switch');

    const editor = page.getByTestId('node-editor');
    const toc = editor.getByRole('navigation', { name: 'Table of contents' });
    const status = editor.getByTestId('node-editor-status');
    // The fields, apart from the table of contents, which names the same
    // sections.
    const fields = editor.locator('.builder-node-editor__body');
    const header = (name) => fields.getByRole('button', { name, exact: true });
    const general = header('General');
    const hardware = fields.getByRole('button', { name: /^Hardware\b/ });
    const drive = fields.getByRole('button', { name: /^Drive 1\b/ });
    const vcpus = editor.getByLabel('VCPUs', { exact: true });
    const description = editor.getByLabel(/^Description/);
    const open = async () => {
      await builder.canvasItem('node').dblclick();
      await expect(editor).toBeVisible();
    };
    const close = async () => {
      await editor.getByTestId('node-editor-cancel').click();
      if (await editor.getByTestId('node-editor-discard').isVisible()) {
        await editor.getByTestId('node-editor-discard-confirm').click();
      }
      await expect(editor).toHaveCount(0);
    };
    // Going to an entry scrolls only the fields: the dialog, its title and
    // the search stay where they are.
    const expectDialogUnscrolled = async () => {
      expect
        .soft(
          await editor.evaluate((dialog) => dialog.scrollTop),
          'the dialog does not scroll',
        )
        .toBe(0);
      await expect.soft(editor.locator('#node-editor-title')).toBeInViewport();
      await expect
        .soft(editor.getByRole('searchbox', { name: 'Search fields' }))
        .toBeInViewport();
    };

    await test.step('a section and a list entry collapse, and an entry keeps its buttons', async () => {
      await open();
      // The node's fields start under a heading, with no box around them.
      await expect
        .soft(
          editor.getByRole('heading', { level: 3, name: 'Node', exact: true }),
        )
        .toBeVisible();
      await expect
        .soft(editor.locator('fieldset.group--heading'))
        .toHaveCSS('border-top-style', 'none');

      await expect.soft(general).toHaveAttribute('aria-expanded', 'true');
      await general.click();
      await expect.soft(general).toHaveAttribute('aria-expanded', 'false');
      await expect.soft(description).toBeHidden();
      await general.press('Enter');
      await expect.soft(general).toHaveAttribute('aria-expanded', 'true');
      await expect.soft(description).toBeVisible();

      await drive.click();
      await expect.soft(drive).toHaveAttribute('aria-expanded', 'false');
      await expect.soft(editor.getByLabel(/^Image/)).toBeHidden();
      await expect
        .soft(editor.getByRole('button', { name: 'Remove drive 1' }))
        .toBeVisible();

      // A new entry is expanded, and its first field takes focus.
      await editor.getByRole('button', { name: 'Add drive' }).click();
      await expect
        .soft(header('Drive 2'))
        .toHaveAttribute('aria-expanded', 'true');
      await expect
        .soft(
          editor
            .getByRole('group', { name: /^Drive 2\b/ })
            .getByLabel(/^Image/),
        )
        .toBeFocused();
      await expect.soft(drive).toHaveAttribute('aria-expanded', 'false');
      await close();
    });

    await test.step('collapsed entries stay so for the next node opened, apart from the Inspector', async () => {
      await open();
      await expect.soft(drive).toHaveAttribute('aria-expanded', 'false');
      await close();
      await builder.selectNode('node');
      await expect
        .soft(builder.inspector.getByRole('button', { name: /^Drive 1\b/ }))
        .toHaveAttribute('aria-expanded', 'true');
    });

    await test.step('Collapse all and Expand all act on every section and entry', async () => {
      await open();
      await editor.getByRole('button', { name: 'Collapse all' }).click();
      await expect.soft(status).toHaveText('Collapsed all sections.');
      for (const name of ['General', 'Network']) {
        await expect
          .soft(header(name))
          .toHaveAttribute('aria-expanded', 'false');
      }
      await expect
        .soft(editor.getByTestId('inspector-section'))
        .toHaveJSProperty('open', false);
      await expect
        .soft(editor.getByRole('button', { name: 'Collapse all' }))
        .toBeFocused();

      // With the fields not scrolling (the dialog made taller), the entry gone
      // to stays marked in the table of contents.
      await page.setViewportSize({ width: 1440, height: 1500 });
      await editor.evaluate((dialog) => {
        dialog.style.height = '1400px';
      });
      await expect
        .poll(() =>
          fields.evaluate((box) => box.scrollHeight <= box.clientHeight + 2),
        )
        .toBe(true);
      await toc
        .getByRole('button', { name: 'Connection points', exact: true })
        .click();
      await expect
        .soft(toc.locator('[aria-current="location"]'))
        .toHaveText('Connection points');
      await editor.evaluate((dialog) => {
        dialog.style.removeProperty('height');
      });
      await page.setViewportSize({ width: 1440, height: 1100 });

      await editor.getByRole('button', { name: 'Expand all' }).click();
      await expect.soft(status).toHaveText('Expanded all sections.');
      await expect.soft(general).toHaveAttribute('aria-expanded', 'true');
      await expect.soft(drive).toHaveAttribute('aria-expanded', 'true');
      await expect
        .soft(editor.getByTestId('inspector-section'))
        .toHaveJSProperty('open', true);
    });

    await test.step('the table of contents lists the sections, and goes to one', async () => {
      await expect(toc).toBeVisible();
      await expect
        .soft(editor.getByTestId('node-editor-toc-toggle'))
        .toBeHidden();
      await expect
        .soft(toc.locator(':scope > ul > li > button'))
        .toHaveText([
          'Checks',
          'General',
          'Hardware',
          'Network',
          'More settings',
          'Connection points',
          'Position',
        ]);
      await expect
        .soft(toc.getByRole('button', { name: 'Drives (1)', exact: true }))
        .toBeVisible();

      await hardware.click();
      await expect.soft(hardware).toHaveAttribute('aria-expanded', 'false');
      await toc.getByRole('button', { name: /^Drive 1\b/ }).click();
      await expect.soft(hardware).toHaveAttribute('aria-expanded', 'true');
      await expect.soft(drive).toBeFocused();
      await expect.soft(drive).toBeInViewport();
      await expect
        .soft(toc.locator('[aria-current="location"]'))
        .toHaveText(/^Drive 1\b/);
      await expectDialogUnscrolled();

      await toc.getByRole('button', { name: 'Position', exact: true }).click();
      await expect
        .soft(editor.getByRole('heading', { name: 'Position' }))
        .toBeFocused();
      await expectDialogUnscrolled();
    });

    await test.step('a collapsed section with an error says so, and Apply or going to the error expands it', async () => {
      await fillField(vcpus, '0');
      await hardware.click();
      await expect
        .soft(hardware)
        .toHaveAccessibleName(/^Hardware\s*, has errors$/);
      await expect
        .soft(hardware.getByTestId('inspector-disclosure-error'))
        .toBeVisible();
      await expect
        .soft(toc.getByRole('button', { name: /^Hardware\b/ }))
        .toHaveAccessibleName(/^Hardware\s*, has errors$/);

      await editor.getByTestId('node-editor-apply').click();
      const link = editor.locator('.builder-inspector__error-link').first();
      await expect(link).toBeFocused();
      await expect.soft(hardware).toHaveAttribute('aria-expanded', 'true');

      await hardware.click();
      await expect.soft(hardware).toHaveAttribute('aria-expanded', 'false');
      await link.click();
      await expect.soft(hardware).toHaveAttribute('aria-expanded', 'true');
      await expect.soft(vcpus).toBeFocused();
    });

    await test.step('field tooltips start with the field’s path in the spec', async () => {
      const tip = editor.getByTestId('inspector-tooltip');

      await editor.locator('[data-path="spec.hardware.vcpus"] > label').hover();
      await expect
        .soft(tip.getByTestId('inspector-tooltip-path'))
        .toHaveText('hardware.vcpus');
      await page.keyboard.press('Escape');
      // A field of the Builder's own, not of the spec, has no path.
      await editor.locator('[data-path="hostname"] > label').hover();
      await expect(tip.getByTestId('inspector-tooltip-text')).toBeVisible();
      await expect
        .soft(tip.getByTestId('inspector-tooltip-path'))
        .toHaveCount(0);
      await page.keyboard.press('Escape');
    });

    await test.step('axe finds nothing', async () => {
      await drive.click();
      // Without motion, so that axe does not read colors in transition.
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await expectAccessible(page, {
        soft: true,
        include: '[data-testid="node-editor"]',
        label: 'axe on the Node Editor with its table of contents',
      });
      await page.emulateMedia({ reducedMotion: null });
    });

    await test.step('in a narrow window the table of contents opens above the fields', async () => {
      await page.setViewportSize({ width: 320, height: 640 });
      const toggle = editor.getByTestId('node-editor-toc-toggle');

      await expect(toggle).toBeVisible();
      await expect.soft(toggle).toHaveAttribute('aria-expanded', 'false');
      await expect.soft(toc).toBeHidden();
      await toggle.click();
      await expect.soft(toggle).toHaveAttribute('aria-expanded', 'true');
      await expect(toc).toBeVisible();
      expect
        .soft(
          await editor.evaluate(
            (dialog) => dialog.scrollWidth <= dialog.clientWidth,
          ),
          'no sideways scroll',
        )
        .toBe(true);
      await toc.getByRole('button', { name: 'General', exact: true }).click();
      await expect.soft(toc).toBeHidden();
      await expect.soft(general).toBeFocused();
      await expectDialogUnscrolled();
      await close();
    });

    await test.step('Reset view expands them all again', async () => {
      await page.setViewportSize({ width: 1440, height: 1100 });
      await builder.selectNode('node');
      const inspectorHardware = builder.inspector.getByRole('button', {
        name: /^Hardware\b/,
      });

      await inspectorHardware.click();
      await expect
        .soft(inspectorHardware)
        .toHaveAttribute('aria-expanded', 'false');
      // Another node and back: it stays collapsed.
      await builder.selectNode('EXP');
      await builder.selectNode('node');
      await expect
        .soft(inspectorHardware)
        .toHaveAttribute('aria-expanded', 'false');
      await page.getByTestId('editor-reset-view').click();
      await expect
        .soft(inspectorHardware)
        .toHaveAttribute('aria-expanded', 'true');
      await open();
      await expect.soft(drive).toHaveAttribute('aria-expanded', 'true');
      await close();
    });

    expectNoFatal(issues);
  });

  // Search fields, sticky section headers, and a field's Default button.
  test('searches its fields, keeps section headers in view, and resets a field to its default', async ({
    page,
    builder,
    issues,
  }) => {
    await page.setViewportSize({ width: 1440, height: 1100 });
    const draft = await newDraft(builder, 'device', 'switch');

    const editor = page.getByTestId('node-editor');
    const toc = editor.getByRole('navigation', { name: 'Table of contents' });
    const fields = editor.locator('.builder-node-editor__body');
    const search = editor.getByRole('searchbox', { name: 'Search fields' });
    const results = editor.getByTestId('node-editor-search-results');
    const general = fields.getByRole('button', {
      name: 'General',
      exact: true,
    });
    const hardware = fields.getByRole('button', { name: /^Hardware\b/ });
    const description = editor.getByLabel(/^Description/);
    const memory = editor.getByLabel('Memory', { exact: true });
    const vcpus = editor.getByLabel('VCPUs', { exact: true });
    const osType = editor.getByLabel(/^OS type/);
    const sections = toc.locator(':scope > ul > li > button');
    const open = async () => {
      await builder.canvasItem('node').dblclick();
      await expect(editor).toBeVisible();
    };
    const device = (doc) => nodeOf(doc, 'device').device;

    await open();

    await test.step('Search fields, left of Expand all, shows the fields a name matches', async () => {
      const box = await search.boundingBox();
      const expand = await editor
        .getByRole('button', { name: 'Expand all' })
        .boundingBox();
      expect.soft(box.x + box.width).toBeLessThanOrEqual(expand.x);
      expect.soft(Math.abs(box.y - expand.y)).toBeLessThan(expand.height);
      await expect
        .soft(search)
        .toHaveAttribute('placeholder', 'Field name or path');

      await hardware.click();
      await expect.soft(hardware).toHaveAttribute('aria-expanded', 'false');

      await search.fill('vcpus');
      await expect.soft(vcpus).toBeVisible();
      // What holds it expands, and the rest hides.
      await expect.soft(hardware).toHaveAttribute('aria-expanded', 'true');
      await expect.soft(memory).toBeHidden();
      await expect.soft(general).toBeHidden();
      await expect.soft(description).toBeHidden();
      await expect.soft(sections).toHaveText(['Hardware']);
    });

    await test.step('a search that matches nothing shows no section, and Escape shows all the fields, collapsed as before', async () => {
      await search.fill('no such field');
      await expect.soft(sections).toHaveCount(0);

      await search.press('Escape');
      await expect(editor).toBeVisible();
      await expect.soft(search).toHaveValue('');
      await expect.soft(search).toBeFocused();
      await expect.soft(results).toHaveCount(0);
      await expect.soft(description).toBeVisible();
      await expect.soft(hardware).toHaveAttribute('aria-expanded', 'false');
      await expect
        .soft(toc.getByRole('button', { name: 'General', exact: true }))
        .toBeVisible();

      // With no search, Escape closes the dialog.
      await search.press('Escape');
      await expect(editor).toHaveCount(0);
      await open();
      await hardware.click();
      await expect(hardware).toHaveAttribute('aria-expanded', 'true');
    });

    await test.step('a section header stays at the top of the fields as they scroll, and a field under it is scrolled clear as it takes focus', async () => {
      // A shorter window, so that the fields scroll far enough.
      await page.setViewportSize({ width: 1440, height: 800 });
      const legend = fields.locator('legend.group-label', {
        hasText: /^Hardware$/,
      });
      const top = async (locator) => (await locator.boundingBox()).y;

      await expect.soft(legend).toHaveCSS('position', 'sticky');
      // VCPUs at the top of the fields, under the Hardware header.
      await vcpus.evaluate((field) => {
        const box = field.closest('.builder-node-editor__body');

        box.scrollTop +=
          field.getBoundingClientRect().top - box.getBoundingClientRect().top;
      });
      await expect
        .poll(async () => Math.abs((await top(legend)) - (await top(fields))))
        .toBeLessThanOrEqual(1);
      expect
        .soft(await top(legend.locator('xpath=..')))
        .toBeLessThan(await top(fields));
      await expect.soft(hardware).toBeInViewport();

      await vcpus.focus();
      await expect
        .poll(async () => {
          const header = await legend.boundingBox();

          return (await top(vcpus)) - (header.y + header.height);
        })
        .toBeGreaterThanOrEqual(0);
      await page.setViewportSize({ width: 1440, height: 1100 });
    });

    await test.step('Default resets a field to its default, by keyboard and pointer, and Apply applies it', async () => {
      const field = editor.locator('[data-path="spec.hardware.memory"]');
      const reset = field.getByTestId('inspector-field-reset-default');
      const shown = field.getByTestId('inspector-field-default');

      await expect.soft(shown).toBeVisible();
      await expect.soft(reset).toHaveCount(0);
      await fillField(memory, '4096');
      await expect.soft(shown).toHaveCount(0);
      await expect(reset).toBeVisible();

      // Tab comes to it after the field, and Enter resets the field, which
      // takes focus back.
      await memory.focus();
      await page.keyboard.press('Tab');
      await expect(reset).toBeFocused();
      await page.keyboard.press('Enter');
      await expect.soft(memory).toHaveValue('512');
      await expect.soft(memory).toBeFocused();
      await expect.soft(shown).toBeVisible();
      await expect.soft(reset).toHaveCount(0);

      // A required choice resets to its default choice, not to empty.
      await osType.selectOption('windows');
      const osReset = editor
        .locator('[data-path="spec.hardware.os_type"]')
        .getByTestId('inspector-field-reset-default');
      await osReset.click();
      await expect.soft(osType).toHaveValue('linux');
      await expect.soft(osType).toBeFocused();

      // A click resets the field too. Apply applies what it shows.
      await fillField(memory, '2048');
      await fillField(vcpus, '2');
      await reset.click();
      await expect.soft(memory).toHaveValue('512');
      await editor.getByTestId('node-editor-apply').click();
      await expect(editor).toHaveCount(0);
      await builder.persisted(
        draft,
        (doc) => [
          device(doc).spec.hardware.memory ?? null,
          device(doc).spec.hardware.os_type ?? null,
          device(doc).spec.hardware.vcpus,
        ],
        [null, 'linux', 2],
      );
    });

    await test.step('axe finds nothing with a search and a Default button', async () => {
      await open();
      await search.fill('hardware');
      await expect(vcpus).toBeVisible();
      await expect(
        editor
          .locator('[data-path="spec.hardware.vcpus"]')
          .getByTestId('inspector-field-reset-default'),
      ).toBeVisible();
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await expectAccessible(page, {
        soft: true,
        include: '[data-testid="node-editor"]',
        label: 'axe on the Node Editor with a search',
      });
      await page.emulateMedia({ reducedMotion: null });
    });

    await test.step('at 320 pixels wide the search takes a row of its own, with no sideways scroll', async () => {
      await page.setViewportSize({ width: 320, height: 640 });
      const box = await search.boundingBox();
      const dialog = await editor.boundingBox();

      expect.soft(box.width).toBeGreaterThan(dialog.width / 2);
      expect
        .soft(
          await editor.evaluate(
            (element) => element.scrollWidth <= element.clientWidth,
          ),
          'no sideways scroll',
        )
        .toBe(true);
      await page.setViewportSize({ width: 1440, height: 1100 });
    });

    expectNoFatal(issues);
  });
});
