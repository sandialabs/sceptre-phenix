import { afterEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, effectScope, h, markRaw, nextTick, ref } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { JsonForms } from '@jsonforms/vue';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import InspectorIconControl from '@/components/builder/inspector/InspectorIconControl.vue';
import {
  INSPECTOR_COLLAPSE,
  useCollapseState,
} from '@/components/builder/inspector/collapse.js';
import {
  INSPECTOR_DEFAULTS,
  INSPECTOR_FIELD_WARNINGS,
  INSPECTOR_RANDOM_MAC,
  INSPECTOR_RESETS,
  INSPECTOR_SUGGESTIONS,
  suggestionsFor,
  useDefaultReset,
  useFieldDefault,
  useFieldText,
  useFieldWarnings,
} from '@/components/builder/inspector/control.js';
import {
  heldCommit,
  keyEffect,
} from '@/components/builder/inspector/heldCommit.js';

import {
  fieldDefault,
  inspectorRenderers,
  inspectorTarget,
  uiSchemaForKind,
} from '@/builder/adapters/forms.js';
import { createFormValidator } from '@/builder/form-validator.js';
import { iconLibrary } from '@/builder/iconLibrary.js';
import { indexIcons } from '@/builder/icons.js';
import { addNode, updateNetwork, updateNode } from '@/builder/model.js';
import { builderSchemaV1, schemaForKind } from '@/builder/schema.js';

import { sampleDocument, tags, textOf } from './fixtures.js';
import { renderWithStore } from './render.js';
import { ICON_DATA } from './png.js';

// One validator for every form here: ajv compiles each schema once.
const formValidator = createFormValidator();

// Renders the device form as BuilderInspector does. A renderer count guard
// fails runaway recursion, and `provides` stands in for the Inspector's.
async function renderDeviceForm(
  interfaces,
  { readonly = false, general = {}, spec = {}, look = {}, provides = [] } = {},
) {
  let instances = 0;
  const renderers = inspectorRenderers.map((entry) => ({
    ...entry,
    renderer: markRaw({
      ...entry.renderer,
      setup(props, context) {
        instances += 1;
        if (instances > 2000) {
          throw new Error('JSON Forms renderer recursion');
        }

        return entry.renderer.setup?.(props, context);
      },
    }),
  }));
  const data = {
    hostname: 'router',
    iconKey: 'router',
    ...look,
    spec: {
      type: 'Router',
      general: { hostname: 'router', ...general },
      hardware: {
        os_type: 'minirouter',
        memory: 512,
        drives: [{ image: 'miniccc.qc2' }],
      },
      network: { interfaces },
      ...spec,
    },
  };
  const app = createSSRApp({
    render: () =>
      h(JsonForms, {
        data,
        schema: schemaForKind(builderSchemaV1, 'device', { spec: data.spec }),
        uischema: uiSchemaForKind(builderSchemaV1, 'device', {
          spec: data.spec,
        }),
        renderers,
        ajv: formValidator,
        readonly,
      }),
  });

  for (const [key, value] of provides) {
    app.provide(key, value);
  }

  return { html: await renderToString(app), instances };
}

const dhcp = (name, vlan) => ({ name, type: 'ethernet', proto: 'dhcp', vlan });
const statik = (name, vlan) => ({
  name,
  type: 'ethernet',
  proto: 'static',
  vlan,
  address: '10.0.0.1',
  mask: 24,
  gateway: '10.0.0.254',
});
const serial = (name, vlan) => ({
  name,
  type: 'serial',
  proto: 'static',
  vlan,
  address: '10.1.0.1',
  mask: 30,
  device: '/dev/ttyS0',
  udp_port: 8989,
  baud_rate: 9600,
});

describe('inspector device form', () => {
  // The instance bound catches JSON Forms recursing on the interface variants.
  for (const [label, interfaces] of [
    ['no interfaces', []],
    [
      'three interface variants',
      [dhcp('eth0', 'EXP'), statik('eth1', 'MGMT'), serial('eth2', 'SER')],
    ],
    // No variant accepts an interface without a VLAN. Its closest variant's
    // fields are shown so the VLAN can be filled in.
    ['an interface without a VLAN', [dhcp('eth0', undefined)]],
  ]) {
    test(`renders a device with ${label}`, async () => {
      const { html, instances } = await renderDeviceForm(interfaces);

      expect(instances).toBeLessThan(2000);
      expect(html).not.toContain('No applicable renderer');
      for (const iface of interfaces) {
        expect(html).toContain(`value="${iface.name}"`);
      }
      if (interfaces.some((iface) => iface.address)) {
        expect(html).toContain('value="10.0.0.1"');
      }
    });
  }
});

describe('interface addresses', () => {
  // The Address of a static interface comes first, under its kind, and
  // the other fields follow.
  test('a static interface starts with its address', async () => {
    const { html } = await renderDeviceForm([statik('eth0', 'EXP')]);
    const at = (key) =>
      html.indexOf(`data-path="spec.network.interfaces.0.${key}"`);

    expect(html).toContain('>Interface kind<');
    expect(at('address')).toBeGreaterThan(html.indexOf('>Interface kind<'));
    const order = [
      'address',
      'mask',
      'gateway',
      'proto',
      'name',
      'vlan',
      'mac',
    ].map(at);

    expect(order.every((index) => index > 0)).toBe(true);
    expect(order).toEqual([...order].sort((a, b) => a - b));
  });

  test('a MAC address field has a Generate button in the Inspector', async () => {
    const button = /<button[^>]*aria-label="Generate a random MAC address"/;
    const shown = await renderDeviceForm([statik('eth0', 'EXP')], {
      provides: [[INSPECTOR_RANDOM_MAC, () => '02:00:00:00:00:01']],
    });

    expect(shown.html).toMatch(button);
    expect(shown.html).toMatch(/<button[^>]*type="button"[^>]*>/);

    // Not without the Inspector, nor in a read-only form.
    expect((await renderDeviceForm([statik('eth0', 'EXP')])).html).not.toMatch(
      button,
    );
    expect(
      (
        await renderDeviceForm([statik('eth0', 'EXP')], {
          readonly: true,
          provides: [[INSPECTOR_RANDOM_MAC, () => '02:00:00:00:00:01']],
        })
      ).html,
    ).not.toMatch(button);
  });
});

describe('inspector field accessibility', () => {
  test('fields are named, described and marked required', async () => {
    const device = { kind: 'device', data: { spec: {} } };
    const { html } = await renderDeviceForm([dhcp('eth0', 'EXP')], {
      provides: [
        [
          INSPECTOR_DEFAULTS,
          ref((path, schema) => fieldDefault(device, path, schema)),
        ],
      ],
    });
    const [hostname, specHostname] = tags(html, 'input').filter((tag) =>
      tag.includes('value="router"'),
    );

    // The asterisk is hidden from the accessible name. "Fields marked * are
    // required" and aria-required say it instead.
    expect(html).toMatch(/>Hostname<span class="asterisk" aria-hidden="true">/);
    expect(hostname).toContain('aria-required="true"');
    expect(hostname).toMatch(/aria-describedby="[^"]*-description"/);
    expect(html).toContain(
      'Unique hostname. Apply also sets it as the node hostname.',
    );
    // Apply sets the spec's copy, so it is read-only, not required, and
    // still reached with Tab (not disabled).
    expect(specHostname).toMatch(/\sreadonly\b/);
    expect(specHostname).not.toMatch(/\sdisabled\b/);
    expect(specHostname).not.toContain('aria-required');

    // Options are named by their text (Chromium ignores <option label>), and
    // oneOf alternatives by their titles.
    expect(tags(html, 'option').some((tag) => tag.includes('label='))).toBe(
      false,
    );
    expect(html).toMatch(/<option[^>]*>\s*minirouter\s*<\/option>/);
    expect(html).not.toContain('oneOf-');
    expect(html).toContain('>Interface kind<');

    // The unset choice names the value phenix then uses, marked "Default".
    // OS type is set.
    const choices = (key) =>
      /<select[^>]*>([\s\S]*?)<\/select>/
        .exec(html.slice(html.indexOf(`${key}-input`) - 200))?.[1]
        .match(/<option[^>]*>[^<]*<\/option>/g)
        .map((option) => option.replace(/<[^>]+>/g, '').trim());
    const select = (key) =>
      tags(html, 'select').find((tag) => tag.includes(`${key}-input`));
    expect(choices('vm_type')).toEqual(['kvm', 'container', 'Default (kvm)']);
    expect(select('vm_type')).toMatch(
      /aria-describedby="[^"]*vm_type-default"/,
    );
    expect(choices('cache_mode')).toContain('Default (writeback)');
    expect(choices('os_type')?.[0]).toBe('Default (linux)');
    expect(select('os_type')).not.toContain('-default');

    // Memory and VCPUs are text fields with a number keyboard, read as spin
    // buttons. Unset VCPUs shows phenix's default, not stored.
    const [memory] = tags(html, 'input').filter((tag) =>
      tag.includes('memory-input'),
    );
    const [vcpus] = tags(html, 'input').filter((tag) =>
      tag.includes('vcpus-input'),
    );
    for (const attribute of [
      'role="spinbutton"',
      'inputmode="numeric"',
      'aria-valuemin="1"',
      'aria-valuenow="512"',
      'type="text"',
      'value="512"',
    ]) {
      expect(memory).toContain(attribute);
    }
    expect(memory).not.toMatch(/\s(min|max|step)=/);
    expect(memory).not.toContain('-default');
    expect(vcpus).toMatch(/aria-valuenow="1"[^>]*type="text"[^>]*value="1"/);
    expect(vcpus).toMatch(/aria-describedby="[^"]*vcpus-default\b/);
    expect(html).not.toMatch(/<option[^>]*>\s*(Megabytes|Whole number)\s*</);
    expect(html).not.toContain('(megabytes)');

    // List items are groups with named buttons, and Add follows the items.
    expect(html).toContain('Drive 1: miniccc.qc2');
    expect(html).toContain('aria-label="Remove drive 1"');
    expect(html).toContain('aria-label="Move interface 1 up"');
    expect(html).toContain('aria-label="Remove interface 1"');
    expect(html).toMatch(/Add interface\s*<\/button>/);
    // The node's fields start under a heading, not inside one more box.
    expect(html).toMatch(
      /<fieldset class="group group--heading"><legend class="group-label"><h3 class="group-heading">Node<\/h3><\/legend>/,
    );
    expect(html).toMatch(/Add drive\s*<\/button>/);
    expect(html).not.toContain('🗙');
  });

  // Labels, annotations and advanced settings are named rows in More
  // settings, which says what it holds and how much is set.
  test('keys and values are named rows in the More settings section', async () => {
    const { html } = await renderDeviceForm([], {
      spec: {
        labels: { 'ntp-server': 'eth0' },
        annotations: { 'phenix/default-apps': false, note: 'true' },
        advanced: null,
      },
    });
    const input = (id) => tags(html, 'input').find((tag) => tag.includes(id));
    const summary = /<summary[^>]*>([\s\S]*?)<\/summary>/.exec(html)?.[1];

    expect(
      summary
        .replace(/<[^>]+>/g, '')
        .replace(/\s+/g, ' ')
        .trim(),
    ).toBe(
      'More settings: miniccc commands, Delay, Injections, Advanced settings, Labels (1), Annotations (2)',
    );
    // A chevron, decoration only, turns as the section opens.
    expect(summary).toMatch(
      /<svg[^>]*class="builder-icon builder-icon--chevron-down inspector-section__chevron"[^>]*aria-hidden="true"/,
    );
    expect(html).toMatch(/<details[^>]*class="inspector-section"/);
    expect(html).not.toMatch(/<details[^>]*\sopen\b/);
    expect(html).toMatch(
      /<span class="builder-visually-hidden"[^>]*>Label 1 <\/span>Name<\/label>/,
    );
    expect(html).toContain('value="ntp-server"');
    expect(html).toContain('value="eth0"');
    // Annotation values read as YAML reads them: false stays false, and
    // text that would read as another value is quoted.
    expect(html).toContain('value="false"');
    expect(html).toContain('value="&quot;true&quot;"');
    expect(html).toContain('aria-label="Remove annotation 2"');
    expect(html).toMatch(/Add advanced setting\s*<\/button>/);
    expect(html).toContain('No advanced settings.');
    // The name field suggests the names phenix knows.
    expect(input('value="ntp-server"')).toMatch(/list="[^"]+-keys"/);
    expect(html).toMatch(/<option value="phenix\/default-apps"/);
  });

  // A collapsed group, list or entry hides its fields, not its legend or an
  // entry's buttons, and says when it hides an error.
  test('groups, lists and entries collapse, and mark the errors they hide', async () => {
    const collapse = useCollapseState(
      'render-test',
      ref(['spec.network.interfaces.0.mac']),
    );

    collapse.collapseAll(['spec.hardware', 'spec.network.interfaces.0']);

    const { html } = await renderDeviceForm(
      [dhcp('eth0', 'EXP'), dhcp('eth1', 'EXP')],
      { provides: [[INSPECTOR_COLLAPSE, collapse]] },
    );
    const disclosures = [
      ...html.matchAll(
        /<button type="button" class="inspector-disclosure nokey" aria-expanded="(true|false)"[^>]*>([\s\S]*?)<\/button>/g,
      ),
    ].map(([, expanded, inner]) => [textOf(inner), expanded]);

    expect(disclosures).toEqual(
      expect.arrayContaining([
        ['General', 'true'],
        ['Hardware', 'false'],
        ['Network', 'true'],
        ['Interfaces', 'true'],
        ['Interface 1: eth0, has errors', 'false'],
        ['Interface 2: eth1', 'true'],
      ]),
    );
    // The hardware fields are hidden, and the group stays in the outline.
    const hardware = html.slice(html.indexOf('data-outline="spec.hardware"'));

    expect(html).toMatch(
      /<fieldset class="group group--collapsed" data-outline="spec\.hardware" data-outline-label="Hardware" data-collapse-path="spec\.hardware">/,
    );
    expect(hardware).toMatch(
      /^[^>]*>\s*<legend[\s\S]*?<\/legend>(<!--\[-->)?<div style="display:none;" class="group-item">/,
    );
    // Interface 1 keeps its buttons, and hides its fields.
    const first = html.slice(
      html.indexOf('data-outline="spec.network.interfaces.0"'),
      html.indexOf('data-outline="spec.network.interfaces.1"'),
    );

    expect(first).toContain('data-outline-errors="true"');
    expect(first).toContain('aria-label="Remove interface 1"');
    expect(first).toContain(
      '<div style="display:none;" class="array-list-item-content">',
    );
    expect(first).toContain('data-testid="inspector-disclosure-error"');
  });

  // The server lists a drive's fields alphabetically. The form shows the
  // image and the inject partition first.
  test("a drive's image and inject partition come before its cache mode", async () => {
    const { html } = await renderDeviceForm([]);
    const at = (key) =>
      html.indexOf(`data-path="spec.hardware.drives.0.${key}"`);

    expect(at('image')).toBeGreaterThan(-1);
    expect(at('image')).toBeLessThan(at('inject_partition'));
    expect(at('inject_partition')).toBeLessThan(at('cache_mode'));
    expect(at('cache_mode')).toBeLessThan(at('interface'));
  });

  // The description is a label tooltip on hover and focus, and the input's
  // description through a hidden element, so it is read once.
  test('descriptions are label tooltips, read once as the description', async () => {
    const { html } = await renderDeviceForm([dhcp('eth0', 'EXP')]);
    const [autostart] = tags(html, 'input').filter((tag) =>
      tag.includes('autostart'),
    );
    const id = /aria-describedby="([^"]*-description)"/.exec(autostart)?.[1];

    expect(id).toBeTruthy();
    expect(html).toContain(`<p id="${id}" hidden>`);
    expect(html).toMatch(/class="label--described label">Autostart</);
    expect(html).not.toContain('class="description"');
    expect(html).not.toContain('data-testid="inspector-tooltip"');
  });

  // Bulma's .input class drew a checkbox as a box whether it was checked or
  // not. An unset checkbox shows the value phenix uses, its schema default.
  test('checkboxes look checked when they are, and show their default', async () => {
    const checkbox = (html, key) =>
      tags(html, 'input').find(
        (tag) => tag.includes('type="checkbox"') && tag.includes(key),
      );
    const { html } = await renderDeviceForm([dhcp('eth0', 'EXP')]);

    expect(
      tags(html, 'input').filter(
        (tag) => tag.includes('type="checkbox"') && tag.includes('class='),
      ),
    ).toEqual([]);
    expect(checkbox(html, 'snapshot')).toMatch(/\schecked\b/);
    expect(checkbox(html, 'autostart')).toMatch(/\schecked\b/);
    expect(checkbox(html, 'qinq')).not.toMatch(/\schecked\b/);
    expect(checkbox(html, 'do_not_boot')).not.toMatch(/\schecked\b/);

    const off = await renderDeviceForm([], { general: { snapshot: false } });

    expect(checkbox(off.html, 'snapshot')).not.toMatch(/\schecked\b/);
  });

  test('number inputs carry the bounds the form checks', async () => {
    const { html } = await renderDeviceForm([dhcp('eth0', 'EXP')]);
    const [mtu] = tags(html, 'input').filter((tag) => tag.includes('mtu'));

    expect(mtu).toContain('aria-valuemin="0"');
    expect(mtu).toContain('aria-valuemax="16000"');

    // A template is text, in a text field that is no spin button.
    const template = await renderDeviceForm([], {
      spec: {
        hardware: {
          os_type: 'linux',
          vcpus: '{{ .VCPUs }}',
          drives: [{ image: 'a.qc2' }],
        },
      },
    });
    const [vcpus] = tags(template.html, 'input').filter((tag) =>
      tag.includes('vcpus-input'),
    );

    expect(vcpus).toContain('value="{{ .VCPUs }}"');
    expect(vcpus).not.toMatch(/role=|inputmode=|aria-value/);
  });

  // A warning does not block Apply: it is tied to its field but does not
  // mark it invalid, and is set apart from errors by more than its color.
  test('warnings show under their field without making it invalid', async () => {
    const path = 'spec.network.interfaces.0.vlan';
    const { html } = await renderDeviceForm([dhcp('eth0', 'EXP')], {
      provides: [
        [
          INSPECTOR_FIELD_WARNINGS,
          ref({ [path]: ['No network is named EXP.', 'Twice.', 'Twice.'] }),
        ],
      ],
    });
    const [vlan] = tags(html, 'input').filter((tag) =>
      tag.includes('value="EXP"'),
    );
    const id = /aria-describedby="([^"]*-warning)\b/.exec(vlan)?.[1];

    expect(id).toBeTruthy();
    expect(vlan).not.toContain('aria-invalid');
    const warning = html.slice(html.indexOf(`<p id="${id}" class="warning"`));

    expect(warning.slice(0, warning.indexOf('</p>'))).toContain(
      '<strong>Warning:</strong> No network is named EXP. Twice.',
    );
    expect(html.match(/class="warning"/g)).toHaveLength(1);
  });

  // A list or a map shows its error once, after its items, and its group
  // is described by it, as a field's input is by the field's.
  test('a list and a map describe their group with their error', async () => {
    const { html } = await renderDeviceForm([], {
      spec: {
        hardware: { os_type: 'minirouter', memory: 512, drives: [] },
        advanced: 'none',
      },
    });

    // ajv's messages: the form here has no i18n.
    for (const [legend, message] of [
      ['Drives', 'must NOT have fewer than 1 items'],
      ['Advanced settings', 'must be object,null'],
    ]) {
      const start = html.lastIndexOf(
        '<fieldset',
        html.indexOf(`>${legend}</span`),
      );
      const [group] = tags(html.slice(start), 'fieldset');
      const id = /aria-describedby="([^"]*-error)\b/.exec(group)?.[1];

      expect(id, legend).toBeTruthy();
      const error = html.slice(html.indexOf(`<p id="${id}" class="error"`));

      expect(error.slice(0, error.indexOf('</p>')).trim()).toMatch(
        new RegExp(`>\\s*${message}$`),
      );
    }
  });

  // A value no choice matches, as an uploaded topology can hold, is a choice
  // of its own, with a warning, rather than a select that shows nothing.
  test("a value outside a drop-down's choices is shown, with a warning", async () => {
    const { html } = await renderDeviceForm([], {
      spec: {
        hardware: {
          os_type: 'ubuntu',
          memory: 512,
          drives: [{ image: 'miniccc.qc2' }],
        },
      },
    });
    const os = tags(html, 'select').find((tag) =>
      tag.includes('hardware.os_type-input'),
    );
    const id = 'field-spec.hardware.os_type-warning';
    const warning = html.slice(html.indexOf(`<p id="${id}" class="warning"`));

    expect(html).toMatch(
      /<option value="ubuntu"[^>]*>\s*ubuntu \(not one of the choices\)/,
    );
    expect(os).toMatch(new RegExp(`aria-describedby="[^"]*\\b${id}\\b`));
    // Quotes are escaped in the markup.
    expect(warning.slice(0, warning.indexOf('</p>'))).toContain(
      '&quot;ubuntu&quot; is not one of the choices.',
    );
  });

  // An editable combobox with list autocomplete (WAI-ARIA APG), once the
  // server's images are known. A plain text field until then.
  test('a drive image suggests the disk images the server has', async () => {
    const image = (html) =>
      tags(html, 'input').find((tag) => tag.includes('value="miniccc.qc2"'));
    const unknown = await renderDeviceForm([], {
      provides: [[INSPECTOR_SUGGESTIONS, ref({ disks: null })]],
    });

    expect(image(unknown.html)).not.toContain('role=');
    expect(unknown.html).not.toContain('role="listbox"');

    const { html } = await renderDeviceForm([], {
      provides: [
        [INSPECTOR_SUGGESTIONS, ref({ disks: ['miniccc.qc2', 'kali.qc2'] })],
      ],
    });
    const input = image(html);
    const listbox = /aria-controls="([^"]+)"/.exec(input)?.[1];

    expect(input).toContain('role="combobox"');
    expect(input).toContain('aria-autocomplete="list"');
    expect(input).toContain('aria-expanded="false"');
    expect(input).not.toContain('aria-activedescendant');
    expect(html).toContain(`id="${listbox}"`);
    expect(html).toMatch(/role="listbox" aria-label="Suggestions for Image"/);
  });

  test("an interface's ruleset fields suggest the device's rulesets", async () => {
    const ruleset = (html, value) =>
      tags(html, 'input').find((tag) => tag.includes(`value="${value}"`));
    const iface = {
      name: 'eth0',
      type: 'ethernet',
      proto: 'static',
      address: '10.0.0.1',
      mask: 24,
      vlan: 'EXP',
      ruleset_in: 'web-in',
      ruleset_out: 'web-out',
    };
    const none = await renderDeviceForm([iface], {
      provides: [[INSPECTOR_SUGGESTIONS, ref({ rulesets: [] })]],
    });

    expect(ruleset(none.html, 'web-in')).not.toContain('role=');

    const { html } = await renderDeviceForm([iface], {
      provides: [[INSPECTOR_SUGGESTIONS, ref({ rulesets: ['web-in'] })]],
    });

    for (const value of ['web-in', 'web-out']) {
      expect(ruleset(html, value)).toContain('role="combobox"');
      expect(ruleset(html, value)).toContain('aria-autocomplete="list"');
    }
  });

  test('a read-only form disables every control', async () => {
    const { html } = await renderDeviceForm([dhcp('eth0', 'EXP')], {
      readonly: true,
    });
    const controls = ['input', 'select', 'textarea', 'button'].flatMap((name) =>
      tags(html, name),
    );

    expect(controls.length).toBeGreaterThan(20);
    // The Icon menu button keeps focus with aria-disabled. Disclosure
    // buttons change only what shows, so they stay enabled.
    const disclosures = controls.filter((tag) =>
      tag.includes('data-testid="inspector-disclosure"'),
    );
    const enabled = controls.filter(
      (tag) => !/\sdisabled\b/.test(tag) && !disclosures.includes(tag),
    );

    expect(disclosures.length).toBeGreaterThan(5);
    expect(disclosures.filter((tag) => /\sdisabled\b/.test(tag))).toEqual([]);
    expect(enabled).toHaveLength(1);
    expect(enabled[0]).toContain('data-testid="inspector-icon"');
    expect(enabled[0]).toContain('aria-disabled="true"');
  });

  // The Icon field is a button, which takes aria-invalid but not
  // aria-required.
  test('an Icon with an error marks its menu button invalid', async () => {
    const { html } = await renderDeviceForm([], { look: { iconKey: 7 } });
    const button = tags(html, 'button').find((tag) =>
      tag.includes('data-testid="inspector-icon"'),
    );
    const [, described] = button.match(/aria-describedby="([^"]+)"/) || [];

    expect(button).toContain('aria-invalid="true"');
    expect(button).not.toContain('aria-required');
    expect(described).toContain('field-iconKey-error');
  });
});

// Renders the whole Inspector for the sample's `select` node (alpha), or for
// the diagram itself with `document`. The other options adjust the store.
async function renderInspector({
  readOnly = false,
  schema = {},
  patch = {},
  metadata = {},
  document = false,
  draft = {},
  change = (sample) => sample.doc,
  select = (sample) => sample.alpha.id,
  iconControls = [],
} = {}) {
  const sample = sampleDocument();
  const doc = change(sample);
  const { html, instances } = await renderWithStore(BuilderInspector, {
    capture: { icons: InspectorIconControl },
    prepare(store) {
      store.doc = {
        ...doc,
        ...patch,
        metadata: { ...doc.metadata, ...metadata },
      };
      store.readOnly = readOnly;
      store.rememberDraft(draft);
      Object.assign(store, schema);
      store.select({ nodes: document ? [] : [select(sample)] });
    },
  });

  // The Icon fields rendered, for a test to read and choose from.
  iconControls.push(...instances.icons);

  return html;
}

// The part of rendered HTML that is the field of a data path: from its
// control to the next one.
function fieldOf(html, path) {
  const start = html.indexOf(`data-path="${path}"`);

  if (start === -1) {
    return '';
  }

  const next = html.indexOf('data-path="', start + 1);

  return html.slice(start, next === -1 ? undefined : next);
}

// The names of a rendered field's choices, in order.
function choicesOf(field) {
  return (field.match(/<option[^>]*>[^<]*<\/option>/g) || []).map((option) =>
    option.replace(/<[^>]+>/g, '').trim(),
  );
}

// The labels of a rendered form's fields, in order.
function labelsOf(html) {
  return [
    ...html.matchAll(/<label[^>]*class="[^"]*\blabel"[^>]*>([^<]*)</g),
  ].map((match) => match[1].trim());
}

// Fields are read only, not disabled, so Tab reaches them at full contrast.
// Buttons are disabled.
test('a read-only draft locks every Inspector field and disables its buttons', async () => {
  const html = await renderInspector({ readOnly: true });
  const fields = ['input', 'select', 'textarea'].flatMap((name) =>
    tags(html, name),
  );
  // The disclosure buttons of the groups and lists change only what
  // shows, so they stay enabled.
  const buttons = tags(html, 'button').filter(
    (tag) => !tag.includes('data-testid="inspector-disclosure"'),
  );
  const enabled = buttons.filter((tag) => !/\sdisabled\b/.test(tag));

  expect(fields.length).toBeGreaterThan(20);
  expect(fields.filter((tag) => /\sdisabled\b/.test(tag))).toEqual([]);
  // A checkbox has no read-only state, so it says so.
  expect(
    fields.filter((tag) => !/\sreadonly\b|aria-readonly="true"/.test(tag)),
  ).toEqual([]);
  expect(html).toContain('Remove connection point eth0');
  // Open Node Editor opens the node read only. The Icon button and Move keep
  // focus with aria-disabled. There is no Apply or Cancel.
  expect(enabled).toHaveLength(3);
  expect(enabled[0]).toContain('data-testid="inspector-node-editor"');
  expect(enabled[1]).toContain('data-testid="inspector-icon"');
  expect(enabled[1]).toContain('aria-disabled="true"');
  expect(enabled[2]).toContain('data-testid="inspector-move"');
  expect(enabled[2]).toContain('aria-disabled="true"');
});

// Apply, Cancel and the state line appear only once there is something to
// apply. A form just opened ends with its fields.
test('an Inspector with no changes shows no Apply, Cancel or state', async () => {
  const html = await renderInspector();

  expect(html).toContain('data-testid="inspector-add-interface"');
  expect(html).not.toContain('inspector-apply');
  expect(html).not.toContain('inspector-cancel');
  expect(html).not.toContain('builder-inspector__state');
  expect(html).not.toContain('No changes');
});

// The Inspector's heading is read, not shown: it names the section, and
// focus can move to it.
test('the Inspector heading is visually hidden, and names the section', async () => {
  const html = await renderInspector();
  const heading = html.match(/<h2\b[^>]*id="inspector-title"[^>]*>([^<]*)</);

  expect(heading[0]).toContain('builder-visually-hidden');
  expect(heading[0]).toContain('tabindex="-1"');
  expect(heading[1].trim()).toBe('Inspector');
  expect(html).toMatch(/<section\b[^>]*aria-labelledby="inspector-title"/);
});

// A node's layer: a select of None and the layers, highest ID first, above
// the connection points. Read-only text in a read-only draft.
describe('the layer section', () => {
  const atLayer = (layer, layers) => (sample) => ({
    ...sample.doc,
    ...(layers ? { layers } : {}),
    nodes: sample.doc.nodes.map((node) =>
      node.id === sample.alpha.id
        ? { ...node, device: { ...node.device, layer } }
        : node,
    ),
  });

  test('is above the connection points of a device, names every placeholder layer, and has Edit layers… after the select', async () => {
    const html = await renderInspector({ change: atLayer(2) });
    const section = html.indexOf('data-testid="inspector-layer"');
    const select = tags(html, 'select').find((tag) =>
      tag.includes('id="inspector-layer-select"'),
    );
    const options = html.slice(section).match(/<select[\s\S]*?<\/select>/)[0];
    const layerSection = html.slice(section);
    const [button] = tags(layerSection, 'button');

    expect(section).toBeGreaterThan(html.indexOf('</form>'));
    expect(section).toBeLessThan(html.indexOf('Connection points'));
    expect(html).toMatch(
      /<label for="inspector-layer-select"[^>]*>Layer<\/label>/,
    );
    expect(select).toContain('aria-describedby="inspector-layer-hint"');
    expect(choicesOf(options)).toEqual([
      'None',
      'Layer 6',
      'Layer 5',
      'Layer 4',
      'Layer 3',
      'Layer 2',
      'Layer 1',
      'Layer 0',
    ]);
    expect(select).toContain('value="2"');
    expect(button).toContain('data-testid="inspector-layer-edit"');
    expect(button).toContain('aria-haspopup="dialog"');
    expect(textOf(layerSection)).toContain('Edit layers…');
    // The layer stack, as Edit layers… has in the command palette.
    expect(
      layerSection.slice(
        layerSection.indexOf('inspector-layer-edit'),
        layerSection.indexOf('Edit layers…'),
      ),
    ).toContain('builder-icon--layer-group');
    expect(layerSection.indexOf('</select>')).toBeLessThan(
      layerSection.indexOf('inspector-layer-edit'),
    );
  });

  test('names the diagram’s layers, and the node’s own ID when the diagram does not have it', async () => {
    const html = await renderInspector({
      change: atLayer(8, [
        { id: 9, label: 'Enterprise' },
        { id: 1, label: 'Basic control' },
      ]),
    });
    const section = html.indexOf('data-testid="inspector-layer"');
    const options = html.slice(section).match(/<select[\s\S]*?<\/select>/)[0];

    expect(choicesOf(options)).toEqual([
      'None',
      'Enterprise',
      'Layer 8',
      'Basic control',
    ]);
  });

  test('says only how the By layer layout orders layers', async () => {
    const html = await renderInspector();
    const hint = html.match(
      /<p id="inspector-layer-hint"[^>]*>([\s\S]*?)<\/p>/,
    );

    expect(textOf(hint[1])).toBe(
      'The By layer layout puts higher layer IDs above lower ones.',
    );
  });

  test('is shown for a switch, and for no note', async () => {
    const hub = await renderInspector({ select: (sample) => sample.sw.id });

    expect(hub).toContain('data-testid="inspector-layer"');
    expect(textOf(hub)).toContain(
      'The By layer layout puts higher layer IDs above lower ones.',
    );
    expect(textOf(hub)).not.toContain('The layer of this');

    const note = await renderInspector({
      change: (sample) => addNode(sample.doc, { kind: 'note', id: 'n1' }).doc,
      select: () => 'n1',
    });

    expect(note).not.toContain('data-testid="inspector-layer"');
  });

  test('is read-only text in a read-only draft', async () => {
    const html = await renderInspector({ readOnly: true, change: atLayer(2) });
    const field = tags(html, 'input').find((tag) =>
      tag.includes('id="inspector-layer-select"'),
    );

    expect(field).toContain('readonly');
    expect(field).toContain('value="Layer 2"');
    expect(html).not.toContain('data-testid="inspector-layer-edit"');
  });

  test('the diagram says how many layers it has, with Edit layers… unless it is read-only', async () => {
    const html = await renderInspector({ document: true });
    const [button] = tags(html, 'button').filter((tag) =>
      tag.includes('inspector-layers-edit'),
    );

    expect(button).toContain('aria-haspopup="dialog"');
    expect(button).toContain('aria-describedby="inspector-layers-help"');
    expect(textOf(html)).toContain(
      'This diagram has 7 placeholder layers, Layer 0 to Layer 6.',
    );

    const own = await renderInspector({
      document: true,
      change: (sample) => ({
        ...sample.doc,
        layers: [
          { id: 2, label: 'Supervisory control' },
          { id: 1, label: 'Basic control' },
        ],
      }),
    });

    expect(textOf(own)).toContain('This diagram has 2 layers.');
    expect(
      await renderInspector({ document: true, readOnly: true }),
    ).not.toContain('inspector-layers-edit');
  });

  // Node matchers: a device with no layer of its own is at the layer its
  // fields match, which the first choice names in place of None.
  const MATCHING = [
    { id: 3, label: 'Operations', iconKey: 'terminal', color: '#0f766e' },
    { id: 1, label: 'Control', matchers: { 'general.hostname': '^alp' } },
  ];

  test('names the matched layer and its field in place of None', async () => {
    const html = await renderInspector({
      change: atLayer(undefined, MATCHING),
    });
    const section = html.slice(html.indexOf('data-testid="inspector-layer"'));
    const options = section.match(/<select[\s\S]*?<\/select>/)[0];

    expect(choicesOf(options)).toEqual([
      'Control (matched general.hostname)',
      'Operations',
      'Control',
    ]);
    expect(section).not.toContain('inspector-layer-use-matched');
  });

  test('offers Use matched layer while a chosen layer goes first, and shows its icon', async () => {
    const html = await renderInspector({ change: atLayer(3, MATCHING) });
    const section = html.slice(
      html.indexOf('data-testid="inspector-layer"'),
      html.indexOf('Connection points'),
    );
    const [button] = tags(section, 'button').filter((tag) =>
      tag.includes('inspector-layer-use-matched'),
    );

    expect(textOf(section)).toContain(
      'Its fields match Control (general.hostname). The layer that you chose goes first.',
    );
    expect(button).toContain(
      'aria-describedby="inspector-layer-select-matched"',
    );
    // The chosen layer's icon and swatch, before the select.
    const look = section.indexOf('data-testid="inspector-layer-look"');

    expect(look).toBeGreaterThan(-1);
    expect(look).toBeLessThan(section.indexOf('<select'));
    expect(section).toContain('builder-icon--terminal');
    expect(section).toContain('--layer-color:#0f766e');
  });

  test('shows no look for a layer whose icon and color do not show', async () => {
    const html = await renderInspector({
      change: atLayer(3, [
        { id: 3, label: 'Operations', color: 'red', icon: 'not-there' },
      ]),
    });
    const section = html.slice(html.indexOf('data-testid="inspector-layer"'));

    expect(section).not.toContain('data-testid="inspector-layer-look"');
  });

  test('the diagram lists its layers, and offers Update from layer set for a newer version', async () => {
    const { html } = await renderWithStore(BuilderInspector, {
      prepare(store) {
        store.doc = {
          ...sampleDocument().doc,
          layers: MATCHING,
          layerSet: { name: 'Plant', source: { id: 'plant', version: 1 } },
        };
        store.layerSets = {
          ...store.layerSets,
          status: 'ready',
          loaded: true,
          items: [
            {
              id: 'plant',
              name: 'Plant',
              layers: MATCHING,
              version: 2,
              matchConflict: 'lowest',
              layerOutlines: false,
            },
          ],
        };
        store.select({ nodes: [] });
      },
    });
    const list = html.match(
      /<ul[^>]*data-testid="inspector-layers-list"[\s\S]*?<\/ul>/,
    )[0];

    expect(textOf(list)).toBe('Operations Control');
    expect(textOf(html)).toContain(
      'Layer set Plant has a newer version, 2. This diagram has version 1.',
    );
    expect(
      tags(html, 'button').find((tag) =>
        tag.includes('inspector-layers-update'),
      ),
    ).toContain('aria-describedby="inspector-layers-link"');
  });
});

// No text field takes part in the browser's autocomplete, by its form's choice
// or its own.
test("the browser offers none of the values it keeps in the Inspector's text fields", async () => {
  const html = await renderInspector();
  const forms = html.match(/<form\b[^>]*>[\s\S]*?<\/form>/g) || [];
  const offered = forms
    .filter((form) => !/^<form\b[^>]*\sautocomplete="off"/.test(form))
    .flatMap((form) => [...tags(form, 'input'), ...tags(form, 'textarea')])
    .filter(
      (tag) =>
        !/\stype="(checkbox|radio|hidden|file|button)"/.test(tag) &&
        !/\sautocomplete="off"/.test(tag),
    );

  expect(forms).toHaveLength(2);
  expect(tags(forms[0], 'input').length).toBeGreaterThan(10);
  expect(offered).toEqual([]);
});

describe('the diagram section', () => {
  const source = {
    kind: 'topology',
    name: 'core',
    importedAt: '2026-09-28T19:07:00Z',
    annotations: {
      'builder-xml': '<mxGraphModel/>',
      owner: 'alice',
      notes: 'two\nlines',
    },
  };
  // The scenarios the server was read for: one with its apps, one the role
  // may not read. A document names its scenarios and holds none of them.
  const reads = {
    storedScenarios: {
      'ntp-scn': {
        content: {
          apps: [
            {
              name: 'ntp',
              hosts: [{ hostname: 'alpha' }, { hostname: 'bravo' }],
            },
            { name: 'soh', disabled: true },
          ],
        },
        problem: '',
        loading: false,
        read: 1,
      },
      'secret-scn': {
        content: null,
        problem: 'forbidden',
        loading: false,
        read: 1,
      },
    },
  };

  // The server's creation and save stamps in the metadata, and the file the
  // draft was uploaded from.
  describe('Details', () => {
    const stamp = {
      createdBy: 'alice',
      createdAt: '2026-10-01T15:04:05Z',
      updatedBy: 'bob@example.com',
      updatedAt: '2026-10-01T16:10:00Z',
    };
    const details = (html) => {
      const from = html.indexOf('data-testid="inspector-details"');

      return from < 0
        ? ''
        : html.slice(
            html.lastIndexOf('<div', from),
            html.indexOf('</dl>', from),
          );
    };
    const shownTime = (value) =>
      new Intl.DateTimeFormat(undefined, {
        dateStyle: 'medium',
        timeStyle: 'short',
      }).format(new Date(value));

    test('says who made the diagram and who saved it last, each with a machine-readable time', async () => {
      const html = await renderInspector({ document: true, metadata: stamp });
      const block = details(html);

      expect(textOf(block)).toBe(
        `Details Created ${shownTime(stamp.createdAt)} by alice ` +
          `Last edited ${shownTime(stamp.updatedAt)} by bob@example.com`,
      );
      // A heading and a list, as the Annotations and Scenario blocks are.
      expect(block).toMatch(
        /<h3[^>]*>Details<\/h3>\s*<dl class="inspector-diagram__list"/,
      );
      expect(block.match(/<dt[^>]*>[^<]+<\/dt>/g)).toHaveLength(2);
      expect(tags(block, 'time')).toEqual([
        expect.stringContaining('datetime="2026-10-01T15:04:05Z"'),
        expect.stringContaining('datetime="2026-10-01T16:10:00Z"'),
      ]);
      // Read only text: nothing to edit, nothing in the Tab order, and no
      // live region, which would speak over the save state on every save.
      expect(block).not.toMatch(/<(input|button|textarea|select)\b/);
      expect(block).not.toMatch(/tabindex|aria-live|role="(status|alert)"/);
      // It comes before the annotations and the scenario.
      expect(html.indexOf('inspector-details')).toBeLessThan(
        html.indexOf('inspector-scenario'),
      );
    });

    test('leaves out what the document does not say', async () => {
      const some = details(
        await renderInspector({
          document: true,
          metadata: { createdBy: 'alice', updatedAt: stamp.updatedAt },
        }),
      );

      // A user without a time, and a time without a user.
      expect(textOf(some)).toBe(
        `Details Created by alice Last edited ${shownTime(stamp.updatedAt)}`,
      );
      expect(tags(some, 'time')).toHaveLength(1);

      const edited = details(
        await renderInspector({
          document: true,
          metadata: { updatedBy: 'bob', updatedAt: stamp.updatedAt },
        }),
      );

      expect(textOf(edited)).toBe(
        `Details Last edited ${shownTime(stamp.updatedAt)} by bob`,
      );
      expect(edited).not.toContain('inspector-created');

      // A diagram that names no one has no Details.
      const none = await renderInspector({ document: true });

      expect(none).not.toContain('inspector-details');
      expect(textOf(none)).toContain('Scenarios No scenarios.');
    });

    test('a draft made from an uploaded file names the file', async () => {
      const html = await renderInspector({
        document: true,
        metadata: stamp,
        draft: { id: 'd1', sourceFile: 'pump station (v2).builder.json' },
      });

      expect(textOf(details(html))).toBe(
        `Details Created ${shownTime(stamp.createdAt)} by alice ` +
          `Last edited ${shownTime(stamp.updatedAt)} by bob@example.com ` +
          'Source file pump station (v2).builder.json',
      );
      expect(tags(details(html), 'time')).toHaveLength(2);

      // The file alone is enough for the block, and its name is text: it
      // is never markup.
      const only = details(
        await renderInspector({
          document: true,
          draft: { id: 'd1', sourceFile: '<b>x</b>.yaml' },
        }),
      );

      expect(textOf(only)).toBe('Details Source file <b>x</b>.yaml');
      expect(only).not.toContain('<b>');
    });

    test('is shown for a node selection no more than the rest of the Diagram section', async () => {
      const html = await renderInspector({ metadata: stamp });

      expect(html).not.toContain('inspector-details');
    });
  });

  // The notes of the diagram, which its metadata holds: a text box each,
  // with its Delete button, and Add note.
  describe('Notes', () => {
    // The block holds no other <div>, so it ends at the first </div>.
    const notesBlock = (html) => {
      const from = html.indexOf('data-testid="inspector-notes"');

      return from < 0
        ? ''
        : html.slice(
            html.lastIndexOf('<div', from),
            html.indexOf('</div>', from) + '</div>'.length,
          );
    };

    test('come before the scenarios, a two-line box and a Delete button each, then Add note', async () => {
      const html = await renderInspector({
        document: true,
        metadata: { notes: ['Snapshot the PLCs.', 'Two lines\n\tand a tab'] },
      });
      const block = notesBlock(html);
      const boxes = tags(block, 'textarea');
      const buttons = tags(block, 'button');

      expect(html.indexOf('inspector-notes')).toBeLessThan(
        html.indexOf('inspector-scenario'),
      );
      expect(block).toMatch(/<h3[^>]*>Notes<\/h3>/);
      expect(boxes).toHaveLength(2);
      // Two lines tall until the text needs more (see fitText.js).
      expect(boxes.every((box) => box.includes('rows="2"'))).toBe(true);
      expect(boxes[0]).toContain('aria-label="Note 1"');
      expect(boxes[0]).toContain('data-testid="inspector-note-1"');
      expect(boxes[1]).toContain('aria-label="Note 2"');
      // Each box holds its note as written, line breaks and tabs included.
      expect(block).toContain('>Snapshot the PLCs.</textarea>');
      expect(block).toContain('>Two lines\n\tand a tab</textarea>');
      // A Delete button each, named for its note, then Add note.
      expect(textOf(block)).toContain('Delete note 1');
      expect(textOf(block)).toContain('Delete note 2');
      expect(buttons).toHaveLength(3);
      expect(buttons[0]).toContain('class="builder-button');
      expect(buttons[2]).toContain('data-testid="inspector-note-add"');
      expect(buttons[2]).not.toContain(' disabled');
      expect(textOf(block)).toContain('Add note');
      expect(textOf(block)).not.toContain('No notes.');
    });

    test('a note the server would refuse is marked invalid, with its error under its box', async () => {
      const block = notesBlock(
        await renderInspector({
          document: true,
          metadata: {
            notes: ['Snapshot the PLCs.', 'x'.repeat(4097), 'bell\u0007'],
          },
        }),
      );
      const boxes = tags(block, 'textarea');
      const errors = tags(block, 'p').filter((tag) =>
        tag.includes('inspector-note-error-'),
      );

      expect(boxes[0]).not.toContain('aria-invalid');
      expect(boxes[0]).not.toContain('aria-describedby');
      expect(errors).toHaveLength(2);

      for (const [index, number] of [
        [0, 2],
        [1, 3],
      ]) {
        const box = boxes[number - 1];
        const [, id] = box.match(/aria-describedby="([^"]+)"/) || [];

        expect(box).toContain('aria-invalid="true"');
        expect(errors[index]).toContain(`id="${id}"`);
        expect(errors[index]).toContain('role="alert"');
        expect(errors[index]).toContain(
          `data-testid="inspector-note-error-${number}"`,
        );
      }

      expect(textOf(block)).toContain(
        'A note holds at most 4096 bytes in UTF-8. This note is longer. Builder does not save it until it is shorter.',
      );
      expect(textOf(block)).toContain(
        'A note cannot hold control characters other than newline and tab. Builder does not save it until you remove them.',
      );
    });

    test('say so when there are none', async () => {
      const block = notesBlock(await renderInspector({ document: true }));

      expect(textOf(block)).toContain('Notes No notes. Add note');
      expect(tags(block, 'textarea')).toEqual([]);
    });

    test('Add note is disabled, and says why, once the diagram holds the most notes', async () => {
      const block = notesBlock(
        await renderInspector({
          document: true,
          metadata: { notes: Array.from({ length: 100 }, (_, i) => `n${i}`) },
        }),
      );
      const add = tags(block, 'button').find((tag) =>
        tag.includes('inspector-note-add'),
      );

      expect(tags(block, 'textarea')).toHaveLength(100);
      expect(add).toContain(' disabled');
      expect(add).toContain('aria-describedby="inspector-notes-limit"');
      expect(block).toContain('id="inspector-notes-limit"');
      expect(textOf(block)).toContain('A diagram holds at most 100 notes.');
    });

    test('a read-only draft shows them as text, with nothing to edit', async () => {
      const block = notesBlock(
        await renderInspector({
          document: true,
          readOnly: true,
          metadata: { notes: ['Snapshot the PLCs.', '<b>not markup</b>'] },
        }),
      );

      expect(textOf(block)).toBe('Notes Snapshot the PLCs. <b>not markup</b>');
      expect(block).not.toMatch(/<(textarea|button|input)\b/);
      expect(block).not.toContain('<b>');

      const none = notesBlock(
        await renderInspector({ document: true, readOnly: true }),
      );

      expect(textOf(none)).toBe('Notes No notes.');
    });

    test('are shown for the diagram only, not for a node', async () => {
      const html = await renderInspector({ metadata: { notes: ['A note'] } });

      expect(html).not.toContain('inspector-notes');
    });
  });

  test("lists the annotations of its source, sorted, but for the Builders' own", async () => {
    const html = await renderInspector({ document: true, patch: { source } });
    const shown = textOf(html);

    expect(shown).toContain('Annotations From Topology core, imported');
    expect(html).toContain('datetime="2026-09-28T19:07:00Z"');
    expect(shown).toContain('notes two lines owner alice Notes');
    expect(html).toContain('>two\nlines<');
    expect(html).not.toContain('builder-xml');
    // Only a value that scrolls takes focus, which a page rendered on the
    // server cannot know.
    expect(html).not.toMatch(/inspector-diagram__value[^>]*tabindex/);
  });

  test('says when there are no annotations, and shows no source for a diagram drawn here', async () => {
    const imported = await renderInspector({
      document: true,
      patch: { source: { kind: 'experiment', name: 'exp' } },
    });
    const drawn = await renderInspector({ document: true });

    expect(textOf(imported)).toContain(
      'Annotations From Experiment exp No annotations.',
    );
    expect(drawn).not.toContain('inspector-annotations');
    expect(textOf(drawn)).toContain('Scenarios No scenarios. Add scenario');
  });

  test('lists each scenario with the apps it was read with, and offers to edit them', async () => {
    const html = await renderInspector({
      document: true,
      schema: reads,
      patch: { scenarios: ['ntp-scn', 'secret-scn'] },
    });
    const button = tags(html, 'button').find((tag) =>
      tag.includes('inspector-scenario-edit'),
    );

    // Each scenario is a sub-heading with its counts. An app with no hosts
    // or that is disabled says so beside its name.
    expect(textOf(html)).toContain(
      'Scenarios Scenario ntp-scn (2 apps, 2 hosts, 1 disabled app) ntp alpha, bravo soh (disabled, no hosts) ' +
        'Scenario secret-scn The Builder does not show the apps of this scenario, because your role cannot read it. ' +
        'Edit scenarios',
    );
    expect(textOf(html)).not.toContain('Apps and their hosts');
    expect(
      tags(html, 'h4').map((tag) => tag.includes('inspector-scenario-name')),
    ).toEqual([true, true]);
    // A scenario not read has no counts.
    expect(html.match(/data-testid="inspector-scenario-counts"/g)).toHaveLength(
      1,
    );
    expect(html).toContain('data-testid="inspector-scenario-1"');
    expect(html).toContain('data-testid="inspector-scenario-2"');
    expect(button).toContain('aria-haspopup="dialog"');
  });

  test('a scenario not read yet reads its apps from the server', async () => {
    const html = await renderInspector({
      document: true,
      schema: reads,
      patch: { scenarios: ['other-scn'] },
    });

    expect(textOf(html)).toContain(
      'Scenarios Scenario other-scn Reading its apps…',
    );
  });

  test('a read-only draft offers no scenario edits', async () => {
    const html = await renderInspector({
      document: true,
      readOnly: true,
      schema: reads,
      patch: { scenarios: ['ntp-scn'] },
    });

    expect(textOf(html)).toContain('ntp alpha, bravo');
    expect(html).not.toContain('inspector-scenario-edit');
  });
});

describe('suggestions', () => {
  const disks = ['win10.qc2', 'ubuntu.qc2', 'kali-ubuntu.qc2', 'Ubuntu-20.qc2'];

  test('match anywhere, ignoring case, those that start with the text first', () => {
    expect(suggestionsFor(disks, 'ubu')).toEqual([
      'Ubuntu-20.qc2',
      'ubuntu.qc2',
      'kali-ubuntu.qc2',
    ]);
    expect(suggestionsFor(disks, ' WIN ')).toEqual(['win10.qc2']);
    expect(suggestionsFor(disks, 'nope')).toEqual([]);
  });

  test('empty text suggests everything, up to the limit', () => {
    expect(suggestionsFor(disks, '')).toHaveLength(4);
    expect(suggestionsFor(disks, '', 2)).toEqual([
      'kali-ubuntu.qc2',
      'Ubuntu-20.qc2',
    ]);
    expect(suggestionsFor(null, 'a')).toEqual([]);
  });
});

// A field's text and warnings as a renderer reads them. `control` stands in
// for the JSON Forms control, new on every render.
function fieldState(data) {
  const app = createSSRApp({});
  const resets = ref(0);
  const warnings = ref({});
  const control = ref({ path: 'spec.general.description', data });

  app.provide(INSPECTOR_RESETS, resets);
  app.provide(INSPECTOR_FIELD_WARNINGS, warnings);

  const state = effectScope().run(() =>
    app.runWithContext(() => ({
      ...useFieldText(control),
      fieldWarnings: useFieldWarnings(control),
    })),
  );

  return { control, resets, warnings, ...state };
}

describe('field state', () => {
  // Every field re-rendered each time the warnings were worked out again,
  // and a re-render put a field's data back over text typed in it.
  test('warnings stay the same list while their messages do', () => {
    const { warnings, fieldWarnings } = fieldState('');
    const none = fieldWarnings.value;

    warnings.value = { 'spec.hardware.drives.0.image': ['Missing.'] };
    expect(fieldWarnings.value).toBe(none);

    warnings.value = { 'spec.general.description': ['Long.', 'Long.'] };
    const long = fieldWarnings.value;

    expect(long).toEqual(['Long.']);
    warnings.value = { 'spec.general.description': ['Long.'] };
    expect(fieldWarnings.value).toBe(long);
    warnings.value = { 'spec.general.description': ['Short.'] };
    expect(fieldWarnings.value).toEqual(['Short.']);
  });

  test('typed text stays until the field’s data changes or the form reloads', async () => {
    const { control, resets, warnings, text, onInput, sync } =
      fieldState('Web');

    expect(text.value).toBe('Web');
    onInput({ target: { value: 'Web server' } });

    // A re-render: new warnings and a new control with the same data.
    warnings.value = { 'spec.general.description': ['Long.'] };
    control.value = { ...control.value };
    await nextTick();
    expect(text.value).toBe('Web server');

    // Committed, the field shows the data as the form reads it.
    control.value = { ...control.value, data: 'Web server' };
    await nextTick();
    expect(text.value).toBe('Web server');
    onInput({ target: { value: '07' } });
    control.value = { ...control.value, data: 7 };
    await nextTick();
    expect(text.value).toBe(7);
    onInput({ target: { value: '007' } });
    sync();
    expect(text.value).toBe(7);

    // Cancel or undo reloads the form, also with the same data.
    onInput({ target: { value: 'typed' } });
    resets.value += 1;
    await nextTick();
    expect(text.value).toBe(7);

    control.value = { ...control.value, data: undefined };
    await nextTick();
    expect(text.value).toBe('');
  });
});

describe('the Default button', () => {
  const MEMORY = {
    value: 512,
    note: 'phenix uses this value while the field is empty',
  };

  // The reset of a Memory field with the given control, and the changes
  // it sends.
  function resetState(control, { locked = false } = {}) {
    const app = createSSRApp({});
    const changes = [];

    app.provide(
      INSPECTOR_DEFAULTS,
      ref((path) => (path === 'spec.hardware.memory' ? MEMORY : undefined)),
    );

    const state = effectScope().run(() =>
      app.runWithContext(() =>
        useDefaultReset(
          {
            control,
            handleChange: (path, value) => changes.push([path, value]),
          },
          { fallback: useFieldDefault(control), locked: ref(locked) },
        ),
      ),
    );

    return { ...state, changes };
  }

  test('shows while a field with a default has another value, and clears it', () => {
    const control = ref({
      path: 'spec.hardware.memory',
      label: 'Memory',
      enabled: true,
      data: 4096,
    });
    const { resettable, resetDefault, changes } = resetState(control);

    expect(resettable.value).toEqual({
      name: 'Reset Memory to the default, 512',
      description: 'phenix uses this value while the field is empty.',
    });
    resetDefault();
    expect(changes).toEqual([['spec.hardware.memory', undefined]]);

    // Showing its default, the field has nothing to reset.
    control.value = { ...control.value, data: undefined };
    expect(resettable.value).toBeUndefined();
    control.value = { ...control.value, data: '' };
    expect(resettable.value).toBeUndefined();
    // Nor does it with the default as its value.
    control.value = { ...control.value, data: 512 };
    expect(resettable.value).toBeUndefined();
    resetDefault();
    expect(changes).toHaveLength(1);
  });

  test('sets the default in a field that cannot be empty', () => {
    const control = ref({
      path: 'spec.hardware.memory',
      label: 'Memory',
      enabled: true,
      required: true,
      data: 4096,
    });
    const { resettable, resetDefault, changes } = resetState(control);

    resetDefault();
    expect(changes).toEqual([['spec.hardware.memory', 512]]);
    control.value = { ...control.value, data: 512 };
    expect(resettable.value).toBeUndefined();
  });

  test('is not there in a field with no default, or one that cannot change', () => {
    const other = ref({
      path: 'spec.general.description',
      label: 'Description',
      enabled: true,
      data: 'Web',
    });
    expect(resetState(other).resettable.value).toBeUndefined();

    const memory = {
      path: 'spec.hardware.memory',
      label: 'Memory',
      enabled: true,
      data: 4096,
    };
    expect(
      resetState(ref(memory), { locked: true }).resettable.value,
    ).toBeUndefined();
    expect(
      resetState(ref({ ...memory, enabled: false })).resettable.value,
    ).toBeUndefined();
  });

  test('the form shows it beside set fields, and not in a read-only form', async () => {
    const device = { kind: 'device', data: { spec: {} } };
    const provides = [
      [
        INSPECTOR_DEFAULTS,
        ref((path, schema) => fieldDefault(device, path, schema)),
      ],
    ];
    const { html } = await renderDeviceForm([dhcp('eth0', 'EXP')], {
      provides,
    });
    const buttons = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="inspector-field-reset-default"'),
    );
    // Each button, by its name, which names its field.
    const byName = Object.fromEntries(
      buttons.map((tag) => [(/aria-label="([^"]+)"/.exec(tag) || [])[1], tag]),
    );

    // Type and OS type have other values. Memory has its default, 512, and
    // VCPUs shows its default.
    expect(Object.keys(byName).sort()).toEqual([
      'Reset OS type to the default, linux',
      'Reset Type to the default, VirtualMachine',
    ]);
    const osType = byName['Reset OS type to the default, linux'];
    const [, described] = /aria-describedby="([^"]+)"/.exec(osType) || [];
    expect(html).toContain(
      `<p id="${described}" hidden>phenix uses this value while the field is empty.</p>`,
    );
    expect(osType).toContain('title="Select to use the default value."');
    expect(html).toMatch(
      /vcpus-default"[^>]*data-testid="inspector-field-default"/,
    );

    const locked = await renderDeviceForm([dhcp('eth0', 'EXP')], {
      readonly: true,
      provides,
    });
    expect(locked.html).not.toContain('inspector-field-reset-default');
  });
});

describe('held commit', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function recorder(result = true) {
    const commits = [];

    return {
      commits,
      commit: (value) => {
        commits.push(value);

        return result;
      },
    };
  }

  // Arrow keys on a closed select change it at every step on Windows and
  // Linux, so only the last step commits.
  test('the values stepped to with keys commit once, the last, when flushed', () => {
    vi.useFakeTimers();
    const { commits, commit } = recorder();
    const held = heldCommit(commit);

    for (const value of ['desktop', 'firewall', 'router']) {
      held.key();
      expect(held.change(value)).toBe(false);
      vi.advanceTimersByTime(700);
    }

    expect(held.held).toBe('router');
    vi.advanceTimersByTime(10000);
    expect(commits).toEqual([]);
    expect(held.flush()).toBe(true);
    expect(commits).toEqual(['router']);
    expect(held.held).toBeNull();

    // The next change with no key before it commits at once.
    expect(held.change('server')).toBe(true);
    expect(commits).toEqual(['router', 'server']);
  });

  test('a choice with the pointer commits at once, in place of one held', () => {
    const { commits, commit } = recorder();
    const held = heldCommit(commit);

    expect(held.change('router')).toBe(true);
    expect(commits).toEqual(['router']);

    held.key();
    held.change('desktop');
    held.point();
    expect(held.change('firewall')).toBe(true);
    expect(commits).toEqual(['router', 'firewall']);
    expect(held.held).toBeNull();
  });

  test('flush commits the value held at once, and only once', () => {
    const { commits, commit } = recorder();
    const held = heldCommit(commit);

    expect(held.flush()).toBe(false);
    held.key();
    held.change('router');
    expect(held.flush()).toBe(true);
    expect(commits).toEqual(['router']);
    expect(held.flush()).toBe(false);
    expect(commits).toEqual(['router']);

    const unchanged = recorder(false);
    const same = heldCommit(unchanged.commit);

    same.key();
    same.change('server');
    expect(same.flush()).toBe(false);
    expect(unchanged.commits).toEqual(['server']);
  });

  test('a key that steps the select holds, and any other but a modifier flushes', () => {
    const effect = (key, init = {}) => keyEffect({ key, ...init });

    for (const key of [
      'ArrowDown',
      'ArrowUp',
      'ArrowLeft',
      'ArrowRight',
      'Home',
      'End',
      'PageUp',
      'PageDown',
      'r',
      'R',
      ' ',
    ]) {
      expect(effect(key), key).toBe('hold');
    }

    // Alt+Down opens the list on Windows and Linux.
    expect(effect('ArrowDown', { altKey: true })).toBe('hold');

    for (const [key, init] of [
      ['Enter'],
      ['Escape'],
      ['Tab'],
      ['F2'],
      ['z', { ctrlKey: true }],
      ['z', { metaKey: true }],
      ['ArrowDown', { ctrlKey: true }],
    ]) {
      expect(effect(key, init), key).toBe('flush');
    }

    for (const key of ['Shift', 'Control', 'Alt', 'Meta', 'Dead', '']) {
      expect(effect(key), key).toBe('');
    }

    expect(effect('r', { isComposing: true })).toBe('');
  });
});

// The subject line names the element alone. Only the bundled fallback is
// mentioned, in plain words, by the schema error the store sets for it.
test('the Inspector shows the schema error only when there is one', async () => {
  const served = await renderInspector({ schema: { schemaError: '' } });

  expect(served).toMatch(
    /class="builder-inspector__subject"[^>]*>Device alpha</,
  );
  expect(served).not.toMatch(/schema:/);
  expect(served).not.toContain('inspector-schema-error');

  const bundled = await renderInspector({
    schema: {
      schemaError:
        "Could not load this server's form fields. The Inspector shows the fields built into the Builder instead.",
    },
  });

  expect(bundled).toMatch(
    /role="alert" data-testid="inspector-schema-error"[^>]*>\s*Could not load this server&#39;s form fields\./,
  );
  expect(bundled).not.toMatch(/schema:/);
});

// Outline, fill and network colors: every picker shares a test id, so each
// is found by its field's data path.
describe('colors, line styles and group fields', () => {
  const picker = (html, path) =>
    tags(fieldOf(html, path), 'button').find((tag) =>
      tag.includes('data-testid="inspector-color-picker"'),
    );
  const chip = (html, path) =>
    tags(fieldOf(html, path), 'span').find((tag) =>
      tag.includes('inspector-color__chip'),
    );

  test('a switch has Edge Color, Line style, Outline Color and Fill Color', async () => {
    const html = await renderInspector({
      select: ({ sw }) => sw.id,
      change: ({ doc, sw, network }) =>
        updateNode(
          updateNetwork(doc, network.id, { color: '#a3273f' }),
          sw.id,
          {
            switch: { outlineColor: '#A3273F', fillColor: '#6b6f18' },
          },
        ),
    });

    expect(labelsOf(html).slice(0, 9)).toEqual([
      'Name',
      'VLAN alias',
      'Subnet',
      'Gateway',
      'Description',
      'Edge Color',
      'Line style',
      'Outline Color',
      'Fill Color',
    ]);
    expect(picker(html, 'color')).toContain('aria-label="Choose edge Color"');
    expect(picker(html, 'outlineColor')).toContain(
      'aria-label="Choose outline Color"',
    );
    expect(picker(html, 'fillColor')).toContain(
      'aria-label="Choose fill Color"',
    );
    expect(
      (html.match(/data-testid="inspector-color-picker"/g) || []).length,
    ).toBe(3);

    // The network's color shows in its theme token, as the canvas draws it.
    // The switch's own colors show as chosen.
    expect(chip(html, 'color')).toContain('background:var(--bx-net-4)');
    expect(chip(html, 'outlineColor')).toContain('background:#a3273f');
    expect(chip(html, 'fillColor')).toContain('background:#6b6f18');
    expect(fieldOf(html, 'outlineColor')).toContain('value="#A3273F"');

    // The line style left to the canvas is Auto, and names the pattern
    // the network's place in the diagram gives it.
    expect(choicesOf(fieldOf(html, 'lineStyle'))).toEqual([
      'Auto (Solid)',
      'Solid',
      'Dashed',
      'Dotted',
      'Dash-dot',
    ]);
    expect(fieldOf(html, 'lineStyle')).toContain(
      'Default: Auto: chosen by the network&#39;s place in the diagram.',
    );
    expect(html).not.toContain('Not set');
  });

  test('a device has Icon size, Outline Color and Fill Color after its Icon', async () => {
    const html = await renderInspector({
      change: ({ doc, alpha }) =>
        updateNode(doc, alpha.id, { device: { fillColor: '#ffd400' } }),
    });

    expect(labelsOf(html).slice(0, 5)).toEqual([
      'Hostname',
      'Icon',
      'Icon size',
      'Outline Color',
      'Fill Color',
    ]);
    // Without a size of its own, the device draws the diagram's.
    expect(choicesOf(fieldOf(html, 'iconSize'))).toEqual([
      'Diagram default (Small)',
      'Small',
      'Medium',
      'Large',
    ]);
    expect(fieldOf(html, 'iconSize')).toContain(
      'Default: The diagram&#39;s icon size.',
    );
    expect(picker(html, 'outlineColor')).toContain(
      'aria-label="Choose outline Color"',
    );
    expect(picker(html, 'outlineColor')).toMatch(
      /aria-describedby="[^"]*-current"/,
    );
    expect(fieldOf(html, 'outlineColor')).toMatch(/hidden[^>]*>No color</);
    expect(picker(html, 'fillColor')).toContain(
      'aria-label="Choose fill Color"',
    );
    expect(chip(html, 'fillColor')).toContain('background:#ffd400');
    expect(fieldOf(html, 'fillColor')).toContain('value="#ffd400"');
    // Two pickers: a device has no color but these.
    expect(
      (html.match(/data-testid="inspector-color-picker"/g) || []).length,
    ).toBe(2);
    expect(fieldOf(html, 'fillColor')).toContain(
      'Text and icon turn black or white to stay readable.',
    );
  });

  test('a group has Description, Border pattern and Icon, each default named', async () => {
    let group;
    const iconControls = [];
    const html = await renderInspector({
      change: ({ doc }) => {
        const added = addNode(doc, { kind: 'group', title: 'Core' });

        group = added.node;

        return added.doc;
      },
      select: () => group.id,
      iconControls,
    });

    expect(labelsOf(html).slice(0, 6)).toEqual([
      'Title',
      'Description',
      'Color',
      'Border pattern',
      'Icon',
      'Icon size',
    ]);
    expect(choicesOf(fieldOf(html, 'iconSize'))).toEqual([
      'Diagram default (Small)',
      'Small',
      'Medium',
      'Large',
    ]);
    expect(tags(fieldOf(html, 'description'), 'textarea')).toHaveLength(1);
    expect(choicesOf(fieldOf(html, 'borderStyle'))).toEqual([
      'Default (Dashed)',
      'Solid',
      'Dashed',
      'Dotted',
      'Double',
    ]);

    const icons = iconControls[0].setupState.items.map((item) => item.label);

    expect(icons[0]).toBe('Default (Container)');
    expect(icons).toContain('Firewall');
    expect(icons).toContain('Printer');
    // Its one color keeps the plain name.
    expect(picker(html, 'color')).toContain('aria-label="Choose color"');
  });

  // The form's own renderers, with what the Inspector provides them left
  // out: the choice that stands for no style is still Auto.
  test('a connection has Label, Color and Line style, which is its network’s unless chosen', async () => {
    const { doc, edge, network } = sampleDocument();
    const styled = updateNetwork(doc, network.id, { lineStyle: 'dashed' });
    const target = inspectorTarget(styled, { type: 'edge', id: edge.id });
    const render = (provides = []) => {
      const app = createSSRApp({
        render: () =>
          h(JsonForms, {
            data: target.data,
            schema: schemaForKind(builderSchemaV1, 'edge'),
            uischema: uiSchemaForKind(builderSchemaV1, 'edge'),
            renderers: inspectorRenderers,
            ajv: formValidator,
          }),
      });

      for (const [key, value] of provides) {
        app.provide(key, value);
      }

      return renderToString(app);
    };
    const html = await render([
      [
        INSPECTOR_DEFAULTS,
        ref((path, schema) => fieldDefault(target, path, schema)),
      ],
    ]);

    expect(labelsOf(html)).toEqual(['Label', 'Color', 'Line style']);
    expect(choicesOf(fieldOf(html, 'lineStyle'))).toEqual([
      'Auto (Dashed)',
      'Solid',
      'Dashed',
      'Dotted',
      'Dash-dot',
    ]);
    expect(fieldOf(html, 'lineStyle')).toContain(
      'Default: Auto: its network&#39;s line style.',
    );
    expect(choicesOf(fieldOf(await render(), 'lineStyle'))[0]).toBe('Auto');
  });
});

// The Icon field: a menu button showing the node's icon and name, choosing
// a built-in or custom icon (InspectorIconControl).
describe('the Icon field', () => {
  // The menu button, in the part of the field that is the custom icon's.
  const button = (html) =>
    tags(fieldOf(html, 'icon'), 'button').find((tag) =>
      tag.includes('data-testid="inspector-icon"'),
    );
  const images = (html) => tags(fieldOf(html, 'icon'), 'img');
  // The device names the icon plc. `entry` is the diagram's copy of it.
  const withIcon = (entry) => ({
    change: ({ doc, alpha }) =>
      updateNode(doc, alpha.id, { device: { icon: 'plc' } }),
    patch: entry ? { icons: { plc: entry } } : {},
  });
  // The Icon field rendered with `options` (its menu, used, is in
  // inspector-icon-field.test.js).
  async function iconField(options = {}) {
    return { html: await renderInspector(options) };
  }

  afterEach(() => {
    iconLibrary.state.index = indexIcons([]);
  });

  test('shows the built-in icon by its label, in a menu button named Icon', async () => {
    const { html } = await iconField();
    const menu = button(html);
    const id = menu.match(/ id="([^"]+)"/)[1];

    expect(menu).toContain('type="button"');
    expect(menu).toContain('aria-haspopup="menu"');
    expect(menu).toContain('aria-expanded="false"');
    expect(menu).not.toContain('aria-disabled');
    // Its name is the label, then the icon's name, and the help text is
    // its description.
    expect(menu).toContain(`aria-labelledby="${id}-label ${id}-value"`);
    expect(html).toContain(`<label id="${id}-label" for="${id}"`);
    expect(menu).toMatch(/aria-describedby="[^"]*-description"/);
    expect(fieldOf(html, 'icon')).toMatch(
      new RegExp(
        `id="${id}-value"[^>]*><span data-testid="inspector-icon-name"[^>]*>Linux</span>`,
      ),
    );
    expect(fieldOf(html, 'icon')).toContain('builder-icon--linux');
    expect(images(html)).toEqual([]);
    expect(html).toContain('Choose Custom icon… to use an image');
    // The menu and the dialog are there only once they are opened.
    expect(html).not.toContain('role="menu"');
    expect(html).not.toContain('data-testid="icon-dialog"');
  });

  test('shows a custom icon and its name', async () => {
    const { html } = await iconField(withIcon({ data: ICON_DATA }));
    const [image] = images(html);

    expect(images(html)).toHaveLength(1);
    expect(image).toContain(`src="data:image/png;base64,${ICON_DATA}"`);
    expect(image).toContain('class="builder-icon builder-icon--custom"');
    expect(image).toContain('width="20"');
    // Decoration: its alternative text is empty.
    expect(image).toMatch(/ alt(=""|\s)/);
    expect(image).toContain('aria-hidden="true"');
    expect(fieldOf(html, 'icon')).toMatch(
      /data-testid="inspector-icon-name"[^>]*>plc<\/span>/,
    );
    expect(textOf(fieldOf(html, 'icon'))).toContain('plc (custom icon)');
  });

  test('shows an icon of the icon library the diagram carries no copy of', async () => {
    iconLibrary.state.index = indexIcons([
      { name: 'PLC', aliases: [], data: ICON_DATA },
    ]);

    const { html } = await iconField(withIcon(null));

    expect(images(html)).toHaveLength(1);
    expect(textOf(fieldOf(html, 'icon'))).toContain('plc (custom icon)');
  });

  test('says when nothing resolves the name the device names', async () => {
    const { html } = await iconField(withIcon(null));

    expect(textOf(fieldOf(html, 'icon'))).toContain(
      'plc (custom icon not found: the node shows its built-in icon)',
    );
    // Nothing is drawn from an icon that is not known: the built-in icon.
    expect(images(html)).toEqual([]);
    expect(fieldOf(html, 'icon')).toContain('builder-icon--linux');
  });

  // The bytes of an icon reach the page as the address of an image and as
  // nothing else: data that is no base64 gives no image at all.
  test('draws nothing from data that is not base64', async () => {
    const { html } = await iconField(
      withIcon({ data: '"><script>alert(1)</script>' }),
    );

    expect(images(html)).toEqual([]);
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('alert(1)');
  });

  // The button keeps focus, and shows the icon.
  test('a read-only draft shows the icon, and the menu does not open', async () => {
    const { html } = await iconField({
      ...withIcon({ data: ICON_DATA }),
      readOnly: true,
    });

    expect(button(html)).toContain('aria-disabled="true"');
    expect(textOf(fieldOf(html, 'icon'))).toContain('plc (custom icon)');
  });
});
