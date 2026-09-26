// Browser tests for the Builder Beta Inspector: the JSON Forms panel that edits
// the selected element through a working copy with Apply and Cancel.
//
// JSON Forms derives input ids from the schema scope and de-duplicates them
// with numeric suffixes, so fields are located by their label inside the
// fieldset JSON Forms renders for each spec group ("General", "Hardware", ...).
//
// The working-copy logic itself (inspectorTarget, applyFormData, formErrors)
// is unit tested in test/builder/forms.test.js, and the device form render in
// test/builder/inspector-render.test.js. These tests cover the UI around it.
//
// The live region can hold a message that is still being read together with
// the next one, so announcements are checked with toContainText.

const crypto = require('node:crypto');

const {
  test,
  expect,
  blankDocument,
  expectAccessible,
  expectNoFatal,
  uniqueName,
} = require('./builder-support');

// Expect a persisted server state within this time. Autosave uploads each
// commit asynchronously after Apply.
const PERSIST = { timeout: 20000 };

function subject(builder) {
  return builder.inspector.locator('.builder-inspector__subject');
}

// The fieldset JSON Forms renders for a spec group such as "General".
function specGroup(builder, title) {
  return builder.inspector
    .locator('legend.group-label', { hasText: new RegExp(`^${title}$`) })
    .locator('xpath=..');
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

// The labels of the fields in the inspector form, in order.
function fieldLabels(builder) {
  return builder.inspector.locator('form label.label');
}

// JSON Forms text controls commit on change, so leave the field after typing.
// Blur rather than Tab: the last field of a form can have no tabbable
// successor, and headless Firefox then keeps focus (and never fires change).
async function fillField(field, value) {
  await field.fill(value);
  await field.blur();
}

// Activates Apply from the keyboard. A mouse click right after an edit is
// lost to a layout shift; the switch step of the selection-kinds test
// covers that path.
async function applyEdits(builder) {
  const apply = builder.inspector.getByTestId('inspector-apply');
  await expect(apply).toBeEnabled();
  await apply.press('Enter');
}

// Memory and VCPUs are `oneOf: [integer, string]` to phenix, and number
// fields named by the field in the Inspector.
async function setNumber(builder, label, value) {
  await fillField(
    specGroup(builder, 'Hardware').getByLabel(label, { exact: true }),
    String(value),
  );
}

// Adds palette items to the open draft. Waits until the server has the added
// nodes: the tracker deletes drafts with If-Match after the test, and an
// autosave still in flight at that point makes the delete fail.
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

// A node spec as phenix stores it in an experiment, which Generate brings
// into a draft as it is: every unset field is null or empty.
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

// Answers GET /disks, which drive images are checked against, with these
// image files. The test server runs no minimega, so it lists none, and the
// editor then leaves drive images unchecked. Call before the editor opens.
async function mockDisks(page, names) {
  await page.route('**/api/v1/disks', (route) =>
    route.fulfill({
      json: { disks: names.map((name) => ({ kind: 'VM', name })) },
    }),
  );
}

// Drags the canvas from an empty spot of its pane to the pane's top left
// corner until `element` is out of view. The pointer stays in the window:
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

test.describe('Builder Beta inspector', () => {
  // Each step checks one behavior. Checks that no later step acts on are
  // soft, so a failure in one step does not hide the steps after it.
  test(
    'device edits validate, cancel, apply and show a description tooltip',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }) => {
      await mockDisks(page, ['ubuntu.qc2', 'win10.qc2']);
      const draft = await newDraft(builder, 'device');
      await builder.selectInOutline('node');

      const hostname = deviceHostname(builder);
      const apply = builder.inspector.getByTestId('inspector-apply');
      const cancel = builder.inspector.getByTestId('inspector-cancel');
      const errors = builder.inspector.getByTestId('inspector-errors');
      const hardware = specGroup(builder, 'Hardware');
      const osType = hardware.getByLabel(/^OS type/);
      const image = hardware.getByLabel(/^Image/);
      const specHostname = specGroup(builder, 'General').getByLabel(
        'Node hostname',
      );
      const snapshot = specGroup(builder, 'General').getByLabel('Snapshot');

      await test.step('fields are named without their asterisk and marked required', async () => {
        await expect
          .soft(builder.inspector)
          .toContainText('Fields marked * are required.');
        await expect.soft(hostname).toHaveAccessibleName('Hostname');
        await expect.soft(hostname).toHaveAttribute('aria-required', 'true');
        // Its description is read without focusing the field.
        await expect
          .soft(hostname)
          .toHaveAccessibleDescription(
            'Unique host name; Apply also sets it as the node hostname.',
          );
        // The subject names what is edited, and nothing about the schema.
        await expect.soft(subject(builder)).toHaveText('Device node');
        // Apply copies it into the spec, so the spec's copy is read-only:
        // were it editable, Apply would silently revert it.
        await expect.soft(specHostname).not.toBeEditable();
        await expect.soft(specHostname).not.toHaveAttribute('aria-required');
      });

      // The schema's description of a field is a tooltip on its label, and
      // is read once, as the field's description.
      await test.step('field descriptions are tooltips on hover and keyboard focus', async () => {
        const tip = builder.inspector.getByTestId('inspector-tooltip');
        const label = specGroup(builder, 'General').locator('label', {
          hasText: /^Snapshot$/,
        });
        const text =
          /^Run the VM on a copy-on-write snapshot of its first drive\b/;

        await expect.soft(tip).toHaveCount(0);
        await expect.soft(snapshot).toHaveAccessibleDescription(text);
        await label.hover();
        await expect(tip).toHaveText(text);
        await expect.soft(tip).toHaveAttribute('aria-hidden', 'true');

        // Beside the Inspector, over the canvas, where it covers no field.
        // The pointer can move onto it, and Escape closes it.
        const box = await tip.boundingBox();
        expect
          .soft(box.x + box.width)
          .toBeLessThanOrEqual((await label.boundingBox()).x);
        await page.mouse.move(box.x + 10, box.y + box.height / 2, {
          steps: 8,
        });
        await expect.soft(tip).toBeVisible();
        await page.keyboard.press('Escape');
        await expect.soft(tip).toHaveCount(0);

        // Keyboard focus shows it too, and Escape leaves focus where it is.
        await specHostname.focus();
        await page.keyboard.press('Tab');
        await expect.soft(snapshot).toBeFocused();
        await expect.soft(tip).toHaveText(text);
        await page.keyboard.press('Escape');
        await expect.soft(tip).toHaveCount(0);
        await expect.soft(snapshot).toBeFocused();
      });

      await test.step('Cancel discards unapplied edits and announces it', async () => {
        // With nothing to apply the form ends with its fields: no Apply, no
        // Cancel and no state line.
        await expect.soft(apply).toHaveCount(0);
        await expect.soft(cancel).toHaveCount(0);
        await expect
          .soft(builder.inspector.locator('.builder-inspector__state'))
          .toHaveCount(0);

        // They appear with the first keystroke, before the field commits,
        // and go when the text is typed back. Leaving the field then sends
        // no change, which used to leave them showing with nothing to apply.
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
        await expect
          .soft(builder.liveRegion)
          .toContainText('No changes to apply.');
        await expect.soft(apply).toHaveCount(0);
        await expect.soft(hostname).toBeFocused();

        // Clicked straight from the field, Apply and Cancel stay for the
        // click although leaving the field takes the edit back, and focus
        // returns to the field rather than falling to <body>.
        for (const button of [apply, cancel]) {
          await fillField(hostname, 'scratch');
          await hostname.fill('node');
          await button.click();
          await expect.soft(apply).toHaveCount(0);
          await expect.soft(hostname).toBeFocused();
        }
        await expect
          .soft(builder.liveRegion)
          .toContainText('No changes to discard.');

        await fillField(hostname, 'scratch');
        await osType.selectOption('rhel');
        await expect.soft(apply).toBeEnabled();
        await expect(cancel).toBeEnabled();
        await expect
          .soft(builder.liveRegion)
          .toContainText('Unapplied changes.');

        await cancel.press('Enter');

        await expect
          .soft(builder.liveRegion)
          .toContainText('Discarded unapplied changes.');
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

      await test.step('invalid fields are flagged, described and listed, and block Apply', async () => {
        await fillField(hostname, 'bad host');
        await fillField(image, '');

        await expect
          .soft(errors)
          .toHaveText(
            '2 fields need attention before these changes can be applied.',
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
          .toHaveAccessibleDescription(/^Image is required\b/);
        await expect
          .soft(builder.inspector)
          .toContainText('Fix the fields marked with errors');
        await expect.soft(apply).toBeDisabled();

        // Submitting the form with Enter must not apply the invalid value.
        await hostname.press('Enter');
        await expect.soft(subject(builder)).toHaveText(/^\s*Device node\b/);

        // The summary lists each field; an entry moves focus to its field.
        await builder.inspector
          .getByTestId('inspector-error-list')
          .getByRole('button', { name: 'Drive 1: Image is required' })
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

        // Snapshot is on until turned off, as phenix runs a VM, and a click
        // turns it off; the box used to show neither.
        await expect.soft(snapshot).toBeChecked();
        await expect.soft(snapshot).toHaveCSS('appearance', 'auto');
        await snapshot.click();
        await expect.soft(snapshot).not.toBeChecked();

        // A second drive through the Drives list's "Add drive" button,
        // which is announced and moves focus to the new drive's first field.
        const drives = hardware
          .locator('fieldset.array-list')
          .filter({ has: page.locator('legend', { hasText: 'Drives' }) });
        // Unset, a choice names the value it comes to, marked as the
        // default as a text field's is.
        const cacheMode = drives
          .getByRole('group', { name: /^Drive 1\b/ })
          .getByLabel('Cache mode');
        await expect
          .soft(cacheMode.locator('option:checked'))
          .toHaveText('Default (writeback)');
        await expect
          .soft(cacheMode)
          .toHaveAccessibleDescription(
            /\. Default: The default, used while the field is empty\.$/,
          );
        await drives.getByRole('button', { name: 'Add drive' }).click();
        const second = drives.getByRole('group', { name: 'Drive 2' });
        await expect.soft(second.getByLabel('Cache mode')).toBeFocused();
        await expect.soft(builder.liveRegion).toContainText('Added drive 2.');
        await expect
          .soft(second.getByRole('button', { name: 'Remove drive 2' }))
          .toBeEnabled();
        await fillField(second.getByLabel(/^Image/), 'data.qc2');

        // Memory and VCPUs, a number or text to phenix, are number fields
        // with no picker for their kind. Unset, each shows the value phenix
        // gives it (512 megabytes), marked as the default and not stored;
        // emptied, it shows it again.
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
        // typed replaces it; it was typed after it, as 5122048.
        await memory.click();
        await page.keyboard.type('2048');
        await expect.soft(memory).toHaveValue('2048');
        await setNumber(builder, 'Memory', 4096);
        await expect.soft(memoryDefault).toHaveCount(0);
        // The field the working copy changed is marked until applied.
        await expect
          .soft(memory)
          .toHaveAccessibleDescription(/Changed, not applied yet\.$/);

        // Enter in a field showing its default applies the other edits,
        // stores no value and keeps focus.
        await fillField(
          specGroup(builder, 'General').getByLabel('Description'),
          'Front end web server',
        );
        await expect(builder.inspector).toContainText('Unapplied changes');
        await vcpus.press('Enter');
        await expect(apply).toHaveCount(0);
        await expect
          .soft(builder.liveRegion)
          .toContainText('Updated device web01');
        await expect.soft(vcpus).toBeFocused();
        await expect.soft(vcpus).toHaveValue('1');
        // A whole-number field takes no fraction or exponent, which it cut
        // to 1 without a word.
        for (const typed of ['1.5', '1e3']) {
          await setNumber(builder, 'VCPUs', typed);
          await expect
            .soft(vcpus)
            .toHaveAccessibleDescription(/^VCPUs must be a whole number\b/);
          await expect.soft(vcpus).toHaveAttribute('aria-invalid', 'true');
          await expect.soft(apply).toBeDisabled();
        }
        // Spaces around a whole number are dropped, which Firefox's number
        // input refused as no number, and the arrow keys step it.
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

        await expect
          .soft(builder.liveRegion)
          .toContainText('Updated device web01');
        await expect.soft(subject(builder)).toHaveText(/^\s*Device web01\b/);
        await expect.soft(specHostname).toHaveValue('web01');
        // Apply goes with the changes it applied, and focus returns to the
        // field edited last.
        await expect.soft(apply).toHaveCount(0);
        await expect.soft(builder.inspector.getByLabel(/^Type/)).toBeFocused();
        // Hard: the tooltip step hovers and focuses this node.
        await expect(builder.node('web01', 'device')).toBeVisible();
        await expect.soft(builder.outlineItem('web01')).toBeVisible();

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

      await test.step('the description is a tooltip on hover and keyboard focus', async () => {
        const node = builder.node('web01', 'device');
        const id = await node.getAttribute('data-node-id');
        // Vue Flow's wrapper is the node's focusable, named element.
        const wrapper = page.locator(`.vue-flow__node[data-id="${id}"]`);
        const tooltip = node.getByTestId('node-tooltip');
        await expect.soft(tooltip).toBeHidden();
        // The tooltip only shows sighted users the end of the node's name.
        await expect
          .soft(wrapper)
          .toHaveAccessibleName(/, comment: Front end web server$/);

        // The pointer can move from the node onto the tooltip, across the
        // gap between them, and Escape closes it (WCAG 1.4.13).
        await node.hover();
        await expect.soft(tooltip).toHaveText('Front end web server');
        const box = await tooltip.boundingBox();
        if (box) {
          await page.mouse.move(box.x + 10, box.y + box.height / 2, {
            steps: 8,
          });
          await expect.soft(tooltip).toBeVisible();
        }
        await page.keyboard.press('Escape');
        await expect.soft(tooltip).toBeHidden();

        await page.getByRole('heading', { name: 'Outline' }).hover();

        // Keyboard: the node's one Tab stop shows it too.
        await wrapper.focus();
        await expect.soft(tooltip).toHaveText('Front end web server');
        await page.keyboard.press('Shift+Tab');
        await expect.soft(wrapper).not.toBeFocused();
        await expect.soft(tooltip).toBeHidden();
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
        await expect.soft(checks).toContainText('Checks: 2 warnings');
        expect
          .soft((await checks.boundingBox())?.y)
          .toBeLessThan((await hostname.boundingBox())?.y);

        // The working copy is checked before Apply; the checks at the top
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

        await expect
          .soft(button)
          .toHaveAccessibleName('2 warnings in this diagram');
        await expect.soft(button).toHaveAttribute('aria-haspopup', 'dialog');

        // Nothing selected, and the device out of view.
        await builder.canvas.focus();
        await page.keyboard.press('Escape');
        await expect(subject(builder)).toHaveText(/^\s*Diagram\b/);
        await panOutOfView(page, wrapper);

        await button.click();
        const dialog = page.getByRole('dialog', { name: 'Diagram checks' });
        await expect(dialog).toBeVisible();
        await expect
          .soft(dialog.getByRole('heading', { name: 'Device web01' }))
          .toBeVisible();
        const issue = dialog.getByRole('button', {
          name: /^Warning: drive image "data\.qc2"/,
        });
        await expect.soft(issue).toHaveAccessibleDescription('Device web01');
        await issue.click();

        // The dialog closes, the device is selected, and focus moves to it
        // on the canvas, which pans it into view.
        await expect(dialog).toHaveCount(0);
        await expect.soft(wrapper).toBeFocused();
        await expect.soft(wrapper).toBeInViewport();
        await expect
          .soft(builder.outlineItem('web01'))
          .toHaveAttribute('aria-pressed', 'true');
        await expect.soft(subject(builder)).toHaveText(/^\s*Device web01\b/);
        await expect.soft(builder.liveRegion).toContainText('Selected web01');

        // Closed without a choice, it returns focus to the button.
        await button.press('Enter');
        await expect(dialog).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(dialog).toHaveCount(0);
        await expect.soft(button).toBeFocused();
      });

      // Stacked under the Outline and the Inspector, the canvas starts below
      // the fold, and focus moved there does not scroll the page.
      await test.step('in the narrow stacked layout, the dialog scrolls the canvas to the device', async () => {
        const viewport = page.viewportSize();
        const button = page.getByTestId('builder-checks');
        const id = await builder
          .node('web01', 'device')
          .getAttribute('data-node-id');
        const wrapper = page.locator(`.vue-flow__node[data-id="${id}"]`);

        await page.setViewportSize({ width: 640, height: 740 });
        await button.scrollIntoViewIfNeeded();
        await expect.soft(wrapper).not.toBeInViewport();

        await button.click();
        await page
          .getByRole('dialog', { name: 'Diagram checks' })
          .getByRole('button', { name: /^Warning: drive image "data\.qc2"/ })
          .click();
        await expect.soft(wrapper).toBeFocused();
        await expect.soft(wrapper).toBeInViewport();
        await page.setViewportSize(viewport);
      });

      // At phone width, while errors show, Apply and Cancel wrap to two
      // rows. The scroll padding kept a field that took focus clear of one,
      // and the field was drawn under them.
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
        await builder.selectInOutline('web01');
        await setNumber(builder, 'VCPUs', 0);
        await expect(bar).toHaveAttribute('data-state', 'error');
        await hostname.focus();
        for (let step = 0; step < 30; step += 1) {
          await page.keyboard.press('Tab');
          await expect.soft.poll(covered, { timeout: 2000 }).toBe('');
        }
        await cancel.press('Enter');
        await expect.soft(bar).toHaveCount(0);
        await page.setViewportSize(viewport);
      });

      expectNoFatal(issues);
    },
  );

  // One draft with every selection kind. Each step selects one kind, checks
  // the fields the inspector shows for it, edits and applies them, and polls
  // the server for the result. The diagram step runs before any item is
  // added, and the step that needs a rendered connection comes last, so a
  // problem with items or canvas edges does not hide the other kinds.
  test(
    'shows and applies the fields of every selection kind',
    { tag: '@cross-browser' },
    async ({ page, builder, issues }, testInfo) => {
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

      await builder.open();
      const draft = await builder.createBlank();
      const fields = fieldLabels(builder);
      const nameField = builder.inspector.getByLabel('Name');
      const apply = builder.inspector.getByTestId('inspector-apply');
      const addInterface = builder.inspector.getByTestId(
        'inspector-add-interface',
      );
      const name = uniqueName(testInfo, 'diagram');
      const renamed = `${name}-renamed`;

      async function persisted(read) {
        return read(await builder.serverDocument(draft));
      }

      await test.step('diagram name and description, and a header rename', async () => {
        await expect.soft(subject(builder)).toHaveText(/^\s*Diagram\b/);
        await expect.soft(fields).toHaveText([/^Name/, /^Description/]);
        await expect.soft(nameField).toHaveValue(/^Untitled topology( \d+)?$/);

        // An empty field filled and emptied again is no change: Apply goes,
        // and Enter adds no Undo step.
        const description = builder.inspector.getByLabel('Description');
        await fillField(description, 'x');
        await expect.soft(apply).toBeVisible();
        await fillField(description, '');
        await expect.soft(apply).toHaveCount(0);
        await nameField.press('Enter');
        await expect
          .soft(builder.liveRegion)
          .toContainText('No changes to apply.');
        await expect
          .soft(page.getByTestId('toolbar-undo'))
          .toHaveAttribute('aria-disabled', 'true');

        await fillField(nameField, name);
        await fillField(
          builder.inspector.getByLabel('Description'),
          'Inspector test lab',
        );
        await applyEdits(builder);

        await expect
          .soft(builder.liveRegion)
          .toContainText(`Updated diagram ${name}`);
        await expect.soft(page.getByTestId('builder-name')).toHaveValue(name);
        await expect.soft(nameField).toHaveValue(name);
        await expect.soft
          .poll(
            () =>
              persisted((doc) => ({
                name: doc.name,
                description: doc.description,
              })),
            PERSIST,
          )
          .toEqual({ name, description: 'Inspector test lab' });

        // A rename in the header flows back into the (clean) inspector form.
        await builder.rename(renamed);
        await expect.soft(nameField).toHaveValue(renamed);
        await expect.soft
          .poll(() => persisted((doc) => doc.name), PERSIST)
          .toBe(renamed);
      });

      await addItems(builder, draft, 'device', 'switch', 'note', 'group');

      await test.step('device', async () => {
        await builder.node('node', 'device').click();
        await expect.soft(subject(builder)).toHaveText(/^\s*Device node\b/);
        await expect.soft(deviceHostname(builder)).toHaveValue('node');
        await expect.soft(builder.inspector.getByLabel('Icon')).toBeVisible();
        await expect.soft(specGroup(builder, 'Node')).toBeVisible();
        await expect.soft(addInterface).toBeVisible();

        // Text typed and not yet committed stays when the server's disk
        // images arrive meanwhile. The drive warning they bring re-rendered
        // every field, which put its data back over the text.
        const description = specGroup(builder, 'General').getByLabel(
          'Description',
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

        // The icon is presentation only: a new one shows on the canvas
        // without Apply, rather than once the selection changes. Arrow keys
        // on the closed select change it at every step on Windows and
        // Linux, and a screen reader reads each option, so the steps come
        // far apart. The icon stepped to commits once chosen (here with
        // Enter), as one edit, which one Undo takes back.
        const icon = builder.inspector.getByLabel('Icon');
        const node = builder.node('node', 'device');
        const stepped = node.locator(
          '.builder-icon--router, .builder-icon--firewall, .builder-icon--desktop',
        );
        await expect
          .soft(icon)
          .toHaveAccessibleDescription(/applies at once, without Apply\.$/);
        await icon.focus();
        // Each step is a key press and the change it makes, `gap` ms apart,
        // as on Windows and Linux (macOS opens the list instead).
        const step = (keys, gap) =>
          icon.evaluate(
            async (select, [values, wait]) => {
              for (const value of values) {
                select.dispatchEvent(
                  new KeyboardEvent('keydown', {
                    key: 'ArrowDown',
                    bubbles: true,
                    cancelable: true,
                  }),
                );
                select.value = value;
                select.dispatchEvent(new Event('input', { bubbles: true }));
                select.dispatchEvent(new Event('change', { bubbles: true }));
                await new Promise((resolve) => setTimeout(resolve, wait));
              }
            },
            [keys, gap],
          );
        await step(['desktop', 'firewall', 'router'], 600);
        await expect.soft(stepped).toHaveCount(0);
        await expect
          .soft(builder.liveRegion)
          .not.toContainText('Changed the icon');
        await expect.soft(apply).toHaveCount(0);
        await icon.evaluate((select) =>
          select.dispatchEvent(
            new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }),
          ),
        );
        await expect.soft(node.locator('.builder-icon--router')).toHaveCount(1);
        await expect
          .soft(builder.liveRegion)
          .toContainText('Changed the icon of Device node to router');
        await expect.soft(icon).toBeFocused();
        await expect.soft(apply).toHaveCount(0);

        await builder.toolbar('undo').click();
        await expect
          .soft(builder.liveRegion)
          .toContainText('Undid Changed the icon of Device node to router');
        await expect.soft(stepped).toHaveCount(0);
        await builder.toolbar('redo').click();
        await expect.soft(node.locator('.builder-icon--router')).toHaveCount(1);

        // A choice from the list with the pointer commits at once, in place
        // of one stepped to with keys.
        await builder.selectInOutline('node');
        await icon.focus();
        await step(['desktop'], 0);
        await icon.evaluate((select) => {
          for (const type of ['pointerdown', 'pointerup']) {
            select.dispatchEvent(
              new PointerEvent(type, { bubbles: true, button: 0 }),
            );
          }
          select.value = 'server';
          select.dispatchEvent(new Event('input', { bubbles: true }));
          select.dispatchEvent(new Event('change', { bubbles: true }));
        });
        await expect.soft(node.locator('.builder-icon--server')).toHaveCount(1);
        await expect
          .soft(builder.liveRegion)
          .toContainText('Changed the icon of Device node to server');
        await expect.soft
          .poll(
            () => persisted((doc) => nodeOf(doc, 'device')?.device?.iconKey),
            PERSIST,
          )
          .toBe('server');
      });

      await test.step('note', async () => {
        await builder.selectInOutline('Note');
        await expect.soft(subject(builder)).toHaveText(/^\s*Note\b/);
        await expect.soft(fields).toHaveText([/^Text/, /^Color/]);

        // An empty field filled and emptied again, then Enter straight from
        // it: nothing to apply.
        const color = builder.inspector.getByLabel('Color', { exact: true });
        await expect.soft(color).toHaveValue('');
        await fillField(color, '#111111');
        await color.fill('');
        await color.press('Enter');
        await expect
          .soft(builder.liveRegion)
          .toContainText('No changes to apply.');
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
      });

      await test.step('group', async () => {
        await builder.selectInOutline('Group');
        await expect.soft(subject(builder)).toHaveText(/^\s*Group\b/);
        await expect
          .soft(fields)
          .toHaveText([/^Title/, /^Color/, /^Collapsed/]);
        const title = builder.inspector.getByLabel('Title');
        await expect.soft(title).toHaveValue('Group');

        // One Enter in a text field commits the field and applies it, in
        // Firefox too, where the form submits before the field's change.
        await title.fill('Enclave');
        await title.press('Enter');
        await expect
          .soft(builder.liveRegion)
          .toContainText('Updated group Enclave');
        await expect.soft(builder.nodes('group')).toContainText('Enclave');
        await expect.soft(builder.outlineItem('Enclave')).toBeVisible();
        await expect.soft
          .poll(
            () => persisted((doc) => nodeOf(doc, 'group')?.group?.title),
            PERSIST,
          )
          .toBe('Enclave');
      });

      // The switch and connection steps need the device on the switch.
      await connectFirst(builder, draft);

      await test.step('switch Name and VLAN alias apply to its network', async () => {
        await builder.selectInOutline('EXP');
        await expect.soft(subject(builder)).toHaveText(/^\s*Network EXP\b/);
        await expect
          .soft(fields)
          .toHaveText([/^Name/, /^VLAN alias/, /^Description/, /^Color/]);
        await expect
          .soft(builder.inspector.getByLabel(/^Name/))
          .toHaveValue('EXP');
        await expect.soft(addInterface).toBeHidden();

        await fillField(builder.inspector.getByLabel(/^Name/), 'MGMT');
        await fillField(builder.inspector.getByLabel('VLAN alias'), '101');
        // Pointer path: one click on Apply, straight from a field that has
        // not committed yet, applies it. Click where Apply is drawn, as
        // a pointer does, without waiting for it to settle.
        await builder.inspector
          .getByLabel('Description')
          .fill('Management VLAN');
        const box = await apply.boundingBox();
        await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);

        await expect
          .soft(builder.liveRegion)
          .toContainText('Updated network MGMT');
        await expect.soft(subject(builder)).toHaveText(/^\s*Network MGMT\b/);
        await expect.soft(builder.node('MGMT', 'switch')).toBeVisible();
        await expect
          .soft(builder.nodes('switch'))
          .toContainText('Network MGMT, VLAN alias 101');
        const networks = page.getByTestId('builder-networks');
        await expect.soft(networks).toContainText('MGMT');
        await expect.soft(networks).toContainText('VLAN 101');
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

      // A suggested color chosen for a network changed nothing on the
      // canvas. It is drawn in the theme's token for it, on the network's
      // connections, its switch's swatch and the picker's chip alike.
      await test.step('a suggested network color is drawn in its theme token', async () => {
        await builder.selectInOutline('MGMT');
        const picker = builder.inspector.getByTestId('inspector-color-picker');
        const popup = builder.inspector.getByTestId('inspector-color-popup');
        const chip = picker.locator('.inspector-color__chip');
        const swatch = builder
          .node('MGMT', 'switch')
          .locator('.builder-node__swatch');
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
          await picker.click();
          await popup.getByRole('button', { name }).click();
          await applyEdits(builder);
          await expect
            .soft(builder.liveRegion)
            .toContainText('Updated network MGMT');
          const drawn = await token(index);
          await expect.soft(edge).toHaveCSS('stroke', drawn);
          await expect.soft(swatch).toHaveCSS('background-color', drawn);
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
        await expect.soft(fields).toHaveText([/^Name/, /^Description/]);
        await expect.soft(nameField).toHaveValue(renamed);
      });

      // Relabelling a clicked connection, and clearing its label back to the
      // network name, are covered in builder-editing.spec.js by the test
      // "edits and deletes canvas selections: notes, groups, connections and
      // everything", step "a clicked connection is relabelled, and cleared
      // back to its network". This step checks that the form has no other
      // field and that Apply announces the edit.
      await test.step('connection', async () => {
        // The edge's label lets a click through to the line under it.
        const label = page.locator('.builder-edge__label');
        await expect(label, 'the connection is drawn').toBeVisible();
        const box = await label.boundingBox();
        await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
        await expect.soft(subject(builder)).toHaveText(/^\s*Connection\b/);
        await expect.soft(fields).toHaveText([/^Label/, /^Color/]);

        // An unlabelled connection is drawn with its network's name, which
        // its Label shows, as the default; it used to be empty.
        const labelField = builder.inspector.getByLabel('Label');
        await expect.soft(labelField).toHaveValue('MGMT');
        await expect
          .soft(labelField)
          .toHaveAccessibleDescription("Default: The network's name.");

        // Its color comes from the picker the Color field's swatch button
        // opens: a dialog of named swatches, one Tab stop moved through
        // with the arrow keys, that Escape closes, returning focus.
        const picker = builder.inspector.getByTestId('inspector-color-picker');
        const popup = builder.inspector.getByTestId('inspector-color-popup');
        await expect.soft(picker).toHaveAccessibleName('Choose color');
        await expect.soft(picker).toHaveAccessibleDescription('No color');
        await picker.press('Enter');
        await expect(popup).toHaveRole('dialog');
        await expect
          .soft(popup.getByRole('button', { name: 'Blue, #2f6fbf' }))
          .toBeFocused();
        await expectAccessible(page, {
          soft: true,
          label: 'axe on the color picker',
        });
        await page.keyboard.press('Escape');
        await expect.soft(popup).toHaveCount(0);
        await expect.soft(picker).toBeFocused();
        await expect.soft(picker).toHaveAttribute('aria-expanded', 'false');
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

        await fillField(labelField, 'uplink');
        await applyEdits(builder);
        await expect
          .soft(builder.liveRegion)
          .toContainText('Updated connection from node to MGMT');
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
    },
  );

  // Checks that no later step acts on are soft. The interface names are
  // hard: the next step selects or removes interfaces by name.
  test('Add and Remove connection point update handles and interface rows', async ({
    page,
    builder,
    issues,
  }) => {
    const draft = await newDraft(builder, 'device', 'switch');
    await builder.selectInOutline('node');

    const node = builder.node('node', 'device');
    const id = await node.getAttribute('data-node-id');
    // Interface handles only; every device also has a new-interface handle.
    const handles = page.locator(
      `.vue-flow__handle[data-nodeid="${id}"]:not([data-handleid="new-interface"])`,
    );
    const rows = interfaceRows(builder);

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
        .toHaveAccessibleDescription(/takes effect at once, without Apply/);
      await expect
        .soft(builder.inspector.getByRole('button', { name: /interface$/ }))
        .toHaveText(['Add interface']);

      // The hints of Connection points and of Position, which follows it,
      // are tooltips on their headings, shown on hover and on keyboard
      // focus of their controls.
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
          /^Interfaces with a handle on the canvas to connect from\./,
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
        .toHaveText('Canvas pixels. Moves the node without dragging.');
      await expect
        .soft(builder.inspector.getByLabel('X', { exact: true }))
        .toHaveAccessibleDescription(
          'Canvas pixels. Moves the node without dragging.',
        );
      await page.keyboard.press('Escape');

      // An edit left unapplied is no reason to lose the interface
      // added meanwhile. Apply, in the next step, keeps both; it used to put
      // the form's copy of the device, without the interface, over it.
      await fillField(
        specGroup(builder, 'General').getByLabel('Description'),
        'Edited before adding',
      );
      await expect(builder.inspector).toContainText('Unapplied changes');
      await add.click();

      await expect
        .soft(builder.liveRegion)
        .toContainText('Added interface eth0');
      await expect(rows).toHaveText([/^eth0\b/]);
      await expect.soft(rows).toHaveText(['eth0 — not connected']);
      await expect.soft(node).toContainText('1 interface');
      // One source handle on the right and one target handle on the left.
      await expect.soft(handles).toHaveCount(2);
      await expect
        .soft(handles.first())
        .toHaveAttribute('title', 'eth0, not connected');
    });

    // The phenix schema requires the VLAN that an interface not
    // connected yet does not have. The form leaves it optional, so the
    // device's other fields still apply.
    await test.step('the device takes edits while an interface is not connected', async () => {
      await setNumber(builder, 'Memory', 2048);
      await expect
        .soft(builder.inspector.getByTestId('inspector-errors'))
        .toBeHidden();
      await applyEdits(builder);
      await expect
        .soft(builder.liveRegion)
        .toContainText('Updated device node');
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
      const device = page.locator('#connect-device');
      const picked = page.locator('#connect-interface');

      // A device picked again, or another, never keeps the interface picked
      // for the last.
      await device.selectOption({ index: 1 });
      await picked.selectOption({ label: 'eth0' });
      await device.selectOption({ index: 0 });
      await expect.soft(picked).toHaveValue('');
      await device.selectOption({ index: 1 });
      await expect.soft(picked).toHaveValue('');

      // Connected while an edit is unapplied, it stays connected once
      // the edit is applied.
      await fillField(
        specGroup(builder, 'General').getByLabel('Description'),
        'Edited before wiring',
      );
      await expect(builder.inspector).toContainText('Unapplied changes');

      await picked.selectOption({ label: 'eth0' });
      await page.locator('#connect-switch').selectOption({ index: 1 });
      const connect = page.getByTestId('outline-connect');
      const view = async () => ({
        page: await page.evaluate(() => window.scrollY),
        canvas: (await builder.canvas.boundingBox())?.y,
      });
      await connect.scrollIntoViewIfNeeded();
      const before = await view();
      await connect.click();

      await expect.soft(builder.summary).toContainText('1 connection');
      await expect.soft(rows).toHaveText(['eth0 — network EXP']);
      // Connect keeps focus and scrolls neither the page nor the canvas
      // away.
      await expect.soft(connect).toBeFocused();
      expect
        .soft(await view(), 'the page and canvas after Connect')
        .toEqual(before);

      await applyEdits(builder);
      await expect
        .soft(builder.liveRegion)
        .toContainText('Updated device node');
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

      // The node's interface is a named group whose kind picker is named
      // and offers named kinds. The node's list is named apart from
      // the Connection points list, which acts without Apply.
      const iface = builder.inspector
        .getByRole('group', { name: 'Interfaces', exact: true })
        .getByRole('group', { name: 'Interface 1: eth0' });
      const kind = iface.getByLabel('Interface kind', { exact: true });
      await expect.soft(kind).toHaveValue('1');
      await expect
        .soft(kind.locator('option'))
        .toHaveText([
          'Ethernet, static or OSPF',
          'Ethernet, DHCP or manual',
          'Serial, static',
        ]);
      await expect
        .soft(iface.getByRole('button', { name: 'Remove interface 1' }))
        .toBeVisible();
    });

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
      const qinq = first.getByLabel('QinQ');
      await expect.soft(first.getByLabel('Autostart')).toBeChecked();
      await expect.soft(qinq).not.toBeChecked();
      await qinq.click();
      await expect.soft(qinq).toBeChecked();
      await expect.soft(qinq).toHaveCSS('appearance', 'auto');

      // An MTU outside what phenix applies cannot be applied; 0 leaves it
      // unset.
      const mtu = first.getByLabel('MTU');
      await expect.soft(mtu).toHaveAttribute('aria-valuemin', '0');
      await expect.soft(mtu).toHaveAttribute('aria-valuemax', '16000');
      await fillField(mtu, '-1');
      await expect
        .soft(builder.inspector.getByTestId('inspector-error-list'))
        .toHaveText('Interface 1: MTU must be between 0 and 16000');
      await expect.soft(mtu).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(apply).toBeDisabled();
      // Enter in another field leaves focus there: the form's errors are
      // the only check. The browser's own check of the MTU's bounds took
      // Enter, and focus, to the MTU with a message of its own.
      const vlan = builder.inspector
        .locator('[data-path="spec.network.interfaces.0.vlan"]')
        .getByRole('textbox');
      await vlan.press('Enter');
      await expect.soft(vlan).toBeFocused();
      await fillField(mtu, '0');
      await expect.soft(apply).toBeEnabled();

      // A new interface is named after the device's, the way a drawn
      // connection names one, and is Ethernet with no address.
      await list.getByRole('button', { name: 'Add interface' }).click();
      const second = list.getByRole('group', { name: 'Interface 2: eth1' });
      await expect.soft(builder.liveRegion).toContainText('Added interface 2.');
      await expect.soft(second.getByLabel(/^Type/)).toHaveValue('ethernet');
      await expect.soft(second.getByLabel(/^Proto/)).toHaveValue('manual');

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
      const ghost = 'No network in this diagram is named "GHOST".';
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
        .soft(builder.liveRegion)
        .toContainText(
          'Updated device node and disconnected eth0 from network EXP',
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
        .soft(builder.liveRegion)
        .toContainText('Updated device node and connected eth0 to network EXP');
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
      await builder.selectInOutline('node');

      // An emptied VLAN applies too, and disconnects the interface
      // in the same edit. Undo connects it again for the steps that follow.
      await fillField(vlan, '');
      await applyEdits(builder);
      await expect
        .soft(builder.liveRegion)
        .toContainText(
          'Updated device node and disconnected eth0 from network EXP',
        );
      await expect(rows).toHaveText(['eth0 — not connected']);
      await expect
        .soft(builder.inspector.getByTestId('inspector-checks'))
        .toContainText(
          'interface "eth0" of "node" is not connected to a network and has no VLAN, so it cannot be published',
        );
      await expect.soft.poll(vlanOnServer, PERSIST).toEqual({
        vlan: '',
        edges: 0,
      });
      await builder.toolbar('undo').click();
      await expect
        .soft(builder.liveRegion)
        .toContainText(
          'Undid Updated device node and disconnected eth0 from network EXP',
        );
      await expect(builder.summary).toContainText('1 connection');
      await expect.soft.poll(vlanOnServer, PERSIST).toEqual({
        vlan: 'EXP',
        edges: 1,
      });
      await builder.selectInOutline('node');
    });

    // Each connected connection point has a Disconnect button, as the
    // Outline's rows once did: the connection goes, the interface stays.
    await test.step("Disconnect removes a connection point's connection", async () => {
      const disconnect = builder.inspector.getByRole('button', {
        name: 'Disconnect eth0 from network EXP',
      });
      await expect.soft(disconnect).toHaveText('Disconnect');
      await disconnect.press('Enter');

      await expect
        .soft(builder.liveRegion)
        .toContainText('Deleted the connection between node and EXP');
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
      await builder.selectInOutline('node');
      await expect(rows).toHaveText(['eth0 — network EXP']);
    });

    // Regression: a device with two or more interfaces used to hang the tab
    // in a JSON Forms oneOf re-render loop. Click and wait for the next task
    // inside one evaluate: a hung tab never answers, and a Playwright click
    // (or its trace snapshot) would wait on it forever.
    await test.step('a second interface keeps the inspector responsive', async () => {
      await builder.waitSaved();
      const responsive = await Promise.race([
        page.evaluate(
          () =>
            new Promise((resolve) => {
              // The hang logs console.debug thousands of times a second.
              console.debug = () => {};
              document
                .querySelector('[data-testid="inspector-add-interface"]')
                .click();
              setTimeout(() => resolve(true), 100);
            }),
        ),
        new Promise((resolve) => setTimeout(() => resolve(false), 5000)),
      ]);
      if (!responsive) {
        // Close the hung tab so fixture teardown does not wait on it.
        await page.close();
      }
      expect(responsive, 'the tab stayed busy for 5 s').toBe(true);

      await expect(rows).toHaveText([/^eth0\b/, /^eth1\b/]);
      await expect
        .soft(rows)
        .toHaveText(['eth0 — network EXP', 'eth1 — not connected']);
      await expect.soft(node).toContainText('2 interfaces');
      await expect.soft(handles).toHaveCount(4);

      // Each is an Ethernet interface with no address management.
      await expect.soft
        .poll(async () =>
          nodeOf(
            await builder.serverDocument(draft),
            'device',
          ).device.spec.network.interfaces.map(
            ({ name, type, proto }) => `${name} ${type} ${proto}`,
          ),
        )
        .toEqual(['eth0 ethernet manual', 'eth1 ethernet manual']);
    });

    await test.step('Remove connection point', async () => {
      await builder.inspector
        .getByRole('button', { name: 'Remove connection point eth0' })
        .click();

      await expect
        .soft(builder.liveRegion)
        .toContainText('Removed interface eth0');
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
      // button; focus goes to the Inspector's heading, not the page.
      await page.keyboard.press('ControlOrMeta+a');
      await expect
        .soft(builder.inspector.getByRole('heading', { name: 'Inspector' }))
        .toBeFocused();
      await builder.selectInOutline('node');
    });

    // A selection change never drops unapplied edits silently. Valid
    // edits are applied to the element they were made on; invalid ones are
    // discarded; either way the live region says which.
    await test.step('a selection change applies or discards unapplied edits, and says which', async () => {
      const description = specGroup(builder, 'General').getByLabel(
        'Description',
      );
      await fillField(description, 'Unsaved notes');
      await expect.soft(builder.inspector).toContainText('Unapplied changes');

      await builder.selectInOutline('EXP');
      await expect
        .soft(builder.liveRegion)
        .toContainText('Applied changes to Device node');
      await builder.selectInOutline('node');
      await expect.soft(description).toHaveValue('Unsaved notes');

      await fillField(deviceHostname(builder), 'bad host');
      await builder.selectInOutline('EXP');
      await expect
        .soft(builder.liveRegion)
        .toContainText('Discarded unapplied changes to Device node');
      await builder.selectInOutline('node');
      await expect.soft(deviceHostname(builder)).toHaveValue('node');

      // Save now's key settles them the same way, from the field being
      // typed in: valid edits are applied and saved. Invalid ones stay in
      // the form, and the result says so, after saying there was nothing
      // else to save.
      await description.fill('Saved by its key');
      await description.press('ControlOrMeta+s');
      await expect
        .soft(builder.liveRegion)
        .toContainText('Applied changes to Device node');
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
        .soft(builder.liveRegion)
        .toContainText(
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
      await builder.selectInOutline('node');
      await description.fill('Saved over a redo');
      await description.press('ControlOrMeta+s');
      await expect
        .soft(builder.liveRegion)
        .toContainText('Applied changes to Device node');
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
      await builder.selectInOutline('node-2');
      await fillField(deviceHostname(builder), 'node');
      await applyEdits(builder);
      await expect
        .soft(builder.liveRegion)
        .toContainText(
          '1 new diagram error: Device node #2: duplicate hostname "node"',
        );
      // The server refuses the duplicate; undo it so the draft saves again
      // before the test deletes it.
      await builder.toolbar('undo').click();
      await builder.waitSaved();
    });

    expectNoFatal(issues);
  });

  // Both ways to rename a network, the inspector and F2 on the switch's
  // outline row, rename its switch everywhere. A soft poll after
  // each rename shows which places still show the old name.
  test('a switch shows its network name after a rename', async ({
    page,
    builder,
  }) => {
    await newDraft(builder, 'device', 'switch');
    const node = builder.nodes('switch');
    const id = await node.getAttribute('data-node-id');
    const row = page.getByTestId(`outline-item-${id}`);
    // Vue Flow's wrapper is the named element (adapters/vueflow.js).
    const wrapper = page.locator(`.vue-flow__node[data-id="${id}"]`);
    const networks = page.getByTestId('builder-networks');
    const text = async (locator) =>
      (await locator.allTextContents()).join(' ').replace(/\s+/g, ' ').trim();

    // The switch's canvas label, its canvas node's accessible name and its
    // outline row.
    const shown = async () => ({
      canvasLabel: await text(node.locator('.builder-node__label')),
      canvasName: await wrapper.getAttribute('aria-label'),
      outlineRow: await text(row.locator('.builder-outline__label')),
    });
    const showing = (name) => ({
      canvasLabel: name,
      canvasName: expect.stringMatching(new RegExp(`^Switch ${name}\\b`)),
      outlineRow: name,
    });

    await builder.selectInOutline('EXP');
    await fillField(builder.inspector.getByLabel(/^Name/), 'MGMT');
    await applyEdits(builder);
    await expect.soft(subject(builder)).toHaveText(/^\s*Network MGMT\b/);
    await expect.soft(networks).toContainText('MGMT');
    await expect.soft
      .poll(shown, 'after a rename in the inspector')
      .toEqual(showing('MGMT'));

    await row.focus();
    await page.keyboard.press('F2');
    await expect(row.getByRole('textbox')).toBeFocused();
    await page.keyboard.type('CORE');
    await page.keyboard.press('Enter');
    await expect(networks).toContainText('CORE');
    await expect.soft
      .poll(shown, 'after an F2 rename in the outline')
      .toEqual(showing('CORE'));
  });

  // The device comes from a phenix experiment, whose node
  // specs carry values phenix accepts and the form's schema does not
  // (advanced null, mac "", gateway ""): they kept every edit of it from
  // being applied. Its rarely used fields are in a section that starts
  // closed, after the fields looked for most; labels, annotations and
  // advanced settings in it are edited as names and values.
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
    await builder.selectInOutline('host-00');

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
        specGroup(builder, 'General').getByLabel('Description'),
        'Imported',
      );
      await expect.soft(errors).toBeHidden();
      await applyEdits(builder);
      await expect
        .soft(builder.liveRegion)
        .toContainText('Updated device host-00');

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
          'More settings: Commands, Delay, Injections, Advanced settings, Labels, Annotations',
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
      await expect.soft(builder.liveRegion).toContainText('Added label 1.');
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
        .toHaveAccessibleDescription('Name is used by label 1 already');
      await expect
        .soft(labels.getByLabel('Label 2 Name'))
        .toHaveAttribute('aria-invalid', 'true');
      // It is left out of the edits until fixed, so Apply is refused, and
      // the Inspector lists it with a link to it.
      const errorList = builder.inspector.getByTestId('inspector-error-list');
      await expect
        .soft(errorList)
        .toHaveText('Label 2: Name is used by label 1 already');
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
      await expect
        .soft(builder.liveRegion)
        .toContainText('Updated device host-00');
      await expect.soft(value).toBeFocused();
      await expect.soft(section.locator('.control--changed')).toHaveCount(0);
      await expect
        .soft(summary)
        .toHaveText(
          'More settings: Commands, Delay (1), Injections, Advanced settings (1), Labels (1), Annotations (1)',
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
});
