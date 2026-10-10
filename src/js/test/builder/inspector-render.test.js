import { afterEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, effectScope, h, nextTick, ref } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';
import {
  Actions,
  coreReducer,
  createDefaultValue,
  mapDispatchToArrayControlProps,
} from '@jsonforms/core';
import { JsonForms } from '@jsonforms/vue';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import {
  INSPECTOR_DEFAULTS,
  INSPECTOR_FIELD_WARNINGS,
  INSPECTOR_RESETS,
  INSPECTOR_SUGGESTIONS,
  suggestionsFor,
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
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags } from './fixtures.js';
import { ICON_DATA } from './png.js';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

// Renders the inspector's device form the way BuilderInspector does, with
// the same schema, UI schema, renderers and validator. A guard counts
// renderer instances so runaway recursion fails the test instead of the
// test run. `general` adds to the spec's general fields, `spec` to the
// spec, and `provides` stands in for what BuilderInspector provides the
// renderers.
async function renderDeviceForm(
  interfaces,
  { readonly = false, general = {}, spec = {}, provides = [] } = {},
) {
  let instances = 0;
  const renderers = inspectorRenderers.map((entry) => ({
    ...entry,
    renderer: {
      ...entry.renderer,
      setup(props, context) {
        instances += 1;
        if (instances > 2000) {
          throw new Error('JSON Forms renderer recursion');
        }

        return entry.renderer.setup?.(props, context);
      },
    },
  }));
  const data = {
    hostname: 'router',
    iconKey: 'router',
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
        ajv: createFormValidator(),
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
  // Regression: a device with two or more interfaces used to freeze the tab
  // (JSON Forms recursed on the untyped allOf interface variants).
  for (const [label, interfaces] of [
    ['no interfaces', []],
    ['one interface', [dhcp('eth0', 'EXP')]],
    ['two interfaces', [dhcp('eth0', 'EXP'), statik('eth1', 'MGMT')]],
    [
      'three interface variants',
      [dhcp('eth0', 'EXP'), statik('eth1', 'MGMT'), serial('eth2', 'SER')],
    ],
    // No variant accepts an interface without a VLAN; its closest variant's
    // fields are shown so the VLAN can be filled in.
    ['an interface without a VLAN', [dhcp('eth0', undefined)]],
  ]) {
    test(`renders a device with ${label}`, async () => {
      const { html, instances } = await renderDeviceForm(interfaces);

      expect(instances).toBeLessThan(2000);
      // Every part of the schema has a renderer: a static or serial
      // interface's DNS servers had none.
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

    // The asterisk is hidden from the accessible name; "Fields marked * are
    // required" and aria-required say it instead.
    expect(html).toMatch(/>Hostname<span class="asterisk" aria-hidden="true">/);
    expect(hostname).toContain('aria-required="true"');
    expect(hostname).toMatch(/aria-describedby="[^"]*-description"/);
    expect(html).toContain(
      'Unique host name; Apply also sets it as the node hostname.',
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

    // The choice that leaves a field unset names the value phenix then
    // uses, and an unset choice is described by it and marked "Default",
    // as a text or number field showing its default is. OS type is set.
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

    // Memory and VCPUs, a number or text to phenix, are whole-number fields
    // with no picker for their kind: text fields with a number keyboard,
    // read as spin buttons. An unset one shows the value phenix gives it,
    // marked as the default and not stored (VCPUs is not set here).
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
    expect(html).toMatch(/<legend class="group-label">Node<\/legend>/);
    expect(html).toMatch(/Add drive\s*<\/button>/);
    expect(html).not.toContain('🗙');
  });

  // Labels, annotations and advanced settings are rows of a named
  // name and value, in the section of rarely used fields, which says what
  // it holds and how much of it is set.
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
      'More settings: Commands, Delay, Injections, Advanced settings, Labels (1), Annotations (2)',
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

  // The description is a tooltip on the label, shown only on hover and
  // focus, and the input's accessible description: a hidden element, so it
  // is read once.
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
        advanced: null,
      },
    });

    // ajv's messages: the form here has no i18n.
    for (const [legend, message] of [
      ['Drives', 'must NOT have fewer than 1 items'],
      ['Advanced settings', 'must be object'],
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
  // server's images are known; a plain text field until then.
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

  test('a read-only form disables every control', async () => {
    const { html } = await renderDeviceForm([dhcp('eth0', 'EXP')], {
      readonly: true,
    });
    const controls = ['input', 'select', 'textarea', 'button'].flatMap((name) =>
      tags(html, name),
    );

    expect(controls.length).toBeGreaterThan(20);
    expect(controls.filter((tag) => !/\sdisabled\b/.test(tag))).toEqual([]);
  });
});

// Renders the whole Inspector for the sample document's alpha device. `schema`
// sets the store's schema state (schemaSource, schemaError), `patch` adds to
// the document and `metadata` to its metadata, and `document` selects
// nothing, so the Inspector shows the diagram's own section. `change` makes
// another document of the sample one, and `select` picks the node to show
// from the sample's parts.
async function renderInspector({
  readOnly = false,
  schema = {},
  patch = {},
  metadata = {},
  document = false,
  draft = {},
  change = (sample) => sample.doc,
  select = (sample) => sample.alpha.id,
} = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(BuilderInspector) });
  app.use(pinia);

  const store = useBuilderStore(pinia);
  const sample = sampleDocument();
  const doc = change(sample);

  store.doc = {
    ...doc,
    ...patch,
    metadata: { ...doc.metadata, ...metadata },
  };
  store.readOnly = readOnly;
  store.rememberDraft(draft);
  Object.assign(store, schema);
  store.select({ nodes: document ? [] : [select(sample)] });

  return renderToString(app);
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

// The visible text of rendered HTML, with its tags and runs of spaces gone.
function text(html) {
  return html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
    .replace(/ ([,.])/g, '$1')
    .trim();
}

// The whole Inspector for a device of a draft opened read-only: its fields
// are read only rather than disabled, so Tab reaches them and their values
// keep full contrast, and its buttons are disabled.
test('a read-only draft locks every Inspector field and disables its buttons', async () => {
  const html = await renderInspector({ readOnly: true });
  const fields = ['input', 'select', 'textarea'].flatMap((name) =>
    tags(html, name),
  );
  const buttons = tags(html, 'button');
  const enabled = buttons.filter((tag) => !/\sdisabled\b/.test(tag));

  expect(fields.length).toBeGreaterThan(20);
  expect(fields.filter((tag) => /\sdisabled\b/.test(tag))).toEqual([]);
  // A checkbox has no read-only state, so it says so.
  expect(
    fields.filter((tag) => !/\sreadonly\b|aria-readonly="true"/.test(tag)),
  ).toEqual([]);
  expect(html).toContain('Remove connection point eth0');
  // Move keeps focus, so it says so with aria-disabled. Nothing can change,
  // so there is no Apply or Cancel.
  expect(enabled).toHaveLength(1);
  expect(enabled[0]).toContain('data-testid="inspector-move"');
  expect(enabled[0]).toContain('aria-disabled="true"');
});

// Apply, Cancel and the state line appear only once there is something to
// apply; a form just opened ends with its fields.
test('an Inspector with no changes shows no Apply, Cancel or state', async () => {
  const html = await renderInspector();

  expect(html).toContain('data-testid="inspector-add-interface"');
  expect(html).not.toContain('inspector-apply');
  expect(html).not.toContain('inspector-cancel');
  expect(html).not.toContain('builder-inspector__state');
  expect(html).not.toContain('No changes');
});

// Firefox keeps what a form sends and offers it back under the field typed
// in, once its lookup returns, and first scrolls that field into view: the
// Inspector moved away from a pointer pressing a button below. No text
// field takes part, by its form's choice or its own.
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

  // Who made the diagram and who saved it last, as the server wrote them
  // into the document's metadata, and the uploaded file the draft was made
  // from.
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

      expect(text(block)).toBe(
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
      expect(text(some)).toBe(
        `Details Created by alice Last edited ${shownTime(stamp.updatedAt)}`,
      );
      expect(tags(some, 'time')).toHaveLength(1);

      const edited = details(
        await renderInspector({
          document: true,
          metadata: { updatedBy: 'bob', updatedAt: stamp.updatedAt },
        }),
      );

      expect(text(edited)).toBe(
        `Details Last edited ${shownTime(stamp.updatedAt)} by bob`,
      );
      expect(edited).not.toContain('inspector-created');
    });

    test('a diagram that names no one, stored before the server kept them, has no Details', async () => {
      const html = await renderInspector({ document: true });

      expect(html).not.toContain('inspector-details');
      expect(text(html)).not.toContain('Details');
      expect(text(html)).toContain('Scenarios No scenarios.');
    });

    test('a draft made from an uploaded file names the file', async () => {
      const html = await renderInspector({
        document: true,
        metadata: stamp,
        draft: { id: 'd1', sourceFile: 'pump station (v2).builder.json' },
      });

      expect(text(details(html))).toBe(
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

      expect(text(only)).toBe('Details Source file &lt;b&gt;x&lt;/b&gt;.yaml');
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

    test('come after the scenario, a box and a Delete button each, then Add note', async () => {
      const html = await renderInspector({
        document: true,
        metadata: { notes: ['Snapshot the PLCs.', 'Two lines\n\tand a tab'] },
      });
      const block = notesBlock(html);
      const boxes = tags(block, 'textarea');
      const buttons = tags(block, 'button');

      expect(html.indexOf('inspector-scenario')).toBeLessThan(
        html.indexOf('inspector-notes'),
      );
      expect(block).toMatch(/<h3[^>]*>Notes<\/h3>/);
      expect(boxes).toHaveLength(2);
      expect(boxes[0]).toContain('aria-label="Note 1"');
      expect(boxes[0]).toContain('data-testid="inspector-note-1"');
      expect(boxes[1]).toContain('aria-label="Note 2"');
      // Each box holds its note as written, line breaks and tabs included.
      expect(block).toContain('>Snapshot the PLCs.</textarea>');
      expect(block).toContain('>Two lines\n\tand a tab</textarea>');
      // A Delete button each, named for its note, then Add note.
      expect(text(block)).toContain('Delete note 1');
      expect(text(block)).toContain('Delete note 2');
      expect(buttons).toHaveLength(3);
      expect(buttons[0]).toContain('class="builder-button');
      expect(buttons[2]).toContain('data-testid="inspector-note-add"');
      expect(buttons[2]).not.toContain(' disabled');
      expect(text(block)).toContain('Add note');
      expect(text(block)).not.toContain('No notes.');
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

      expect(text(block)).toContain(
        'A note holds at most 4096 bytes in UTF-8, and this one is longer, so it is not saved until it is shorter.',
      );
      expect(text(block)).toContain(
        'A note cannot hold control characters other than newline and tab, so it is not saved until they are removed.',
      );
    });

    test('say so when there are none', async () => {
      const block = notesBlock(await renderInspector({ document: true }));

      expect(text(block)).toContain('Notes No notes. Add note');
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
      expect(text(block)).toContain('A diagram holds at most 100 notes.');
    });

    test('a read-only draft shows them as text, with nothing to edit', async () => {
      const block = notesBlock(
        await renderInspector({
          document: true,
          readOnly: true,
          metadata: { notes: ['Snapshot the PLCs.', '<b>not markup</b>'] },
        }),
      );

      expect(text(block)).toBe(
        'Notes Snapshot the PLCs. &lt;b&gt;not markup&lt;/b&gt;',
      );
      expect(block).not.toMatch(/<(textarea|button|input)\b/);
      expect(block).not.toContain('<b>');

      const none = notesBlock(
        await renderInspector({ document: true, readOnly: true }),
      );

      expect(text(none)).toBe('Notes No notes.');
    });

    test('are shown for the diagram only, not for a node', async () => {
      const html = await renderInspector({ metadata: { notes: ['A note'] } });

      expect(html).not.toContain('inspector-notes');
    });
  });

  test("lists the annotations of its source, sorted, but for the Builders' own", async () => {
    const html = await renderInspector({ document: true, patch: { source } });
    const shown = text(html);

    expect(shown).toContain('Annotations From Topology core, imported');
    expect(html).toContain('datetime="2026-09-28T19:07:00Z"');
    expect(shown).toContain('notes two lines owner alice Scenarios');
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

    expect(text(imported)).toContain(
      'Annotations From Experiment exp No annotations.',
    );
    expect(drawn).not.toContain('inspector-annotations');
    expect(text(drawn)).toContain('Scenarios No scenarios. Add scenario');
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

    expect(text(html)).toContain(
      'Scenarios Scenario ntp-scn Apps and their hosts ntp alpha, bravo soh (disabled) No hosts ' +
        'Scenario secret-scn Your role cannot read this scenario, so its apps are not listed. ' +
        'Edit scenarios',
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

    expect(text(html)).toContain(
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

    expect(text(html)).toContain('ntp alpha, bravo');
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

// A field's text and warnings, the way a renderer reads them, with what
// BuilderInspector provides. `control` stands in for the JSON Forms control,
// a new object whenever the form re-renders.
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
  // Linux; each step was an Undo step, a draft save and an announcement. A
  // screen reader reads each option, so the steps come far apart.
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
test('the Inspector says which schema it uses only when it is not the server', async () => {
  const served = await renderInspector({
    schema: { schemaSource: 'server', schemaError: '' },
  });

  expect(served).toMatch(
    /class="builder-inspector__subject"[^>]*>Device alpha</,
  );
  expect(served).not.toMatch(/schema:/);
  expect(served).not.toContain('inspector-schema-error');

  const bundled = await renderInspector({
    schema: {
      schemaSource: 'bundled',
      schemaError:
        "Could not load this server's form fields. The Inspector shows the fields built into the Builder instead.",
    },
  });

  expect(bundled).toMatch(
    /role="alert" data-testid="inspector-schema-error"[^>]*>\s*Could not load this server&#39;s form fields\./,
  );
  expect(bundled).not.toMatch(/schema:/);
});

// A device and a switch each have an outline and a fill beside their other
// fields, and the network's color on a switch says what it colors. Every
// picker has the one test id, so each is found by its field's data path
// and named after its field.
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

    expect(labelsOf(html).slice(0, 7)).toEqual([
      'Name',
      'VLAN alias',
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

    // The network's color is shown as the canvas draws it, in its theme's
    // token; the switch's own outline and fill as chosen, even when they
    // are a color addNetwork picks.
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

  test('a device has Custom icon, Icon size, Outline Color and Fill Color after its Icon', async () => {
    const html = await renderInspector({
      change: ({ doc, alpha }) =>
        updateNode(doc, alpha.id, { device: { fillColor: '#ffd400' } }),
    });

    expect(labelsOf(html).slice(0, 6)).toEqual([
      'Hostname',
      'Icon',
      'Custom icon',
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

  test('a group has Description, Border pattern, Icon and Custom icon, each default named', async () => {
    let group;
    const html = await renderInspector({
      change: ({ doc }) => {
        const added = addNode(doc, { kind: 'group', title: 'Core' });

        group = added.node;

        return added.doc;
      },
      select: () => group.id,
    });

    expect(labelsOf(html).slice(0, 7)).toEqual([
      'Title',
      'Description',
      'Color',
      'Border pattern',
      'Icon',
      'Custom icon',
      'Icon size',
    ]);
    expect(choicesOf(fieldOf(html, 'iconSize'))).toEqual([
      'Diagram default (Small)',
      'Small',
      'Medium',
      'Large',
    ]);
    expect(text(fieldOf(html, 'icon'))).toContain('None Choose…');
    expect(tags(fieldOf(html, 'description'), 'textarea')).toHaveLength(1);
    expect(choicesOf(fieldOf(html, 'borderStyle'))).toEqual([
      'Default (Dashed)',
      'Solid',
      'Dashed',
      'Dotted',
      'Double',
    ]);

    const icons = choicesOf(fieldOf(html, 'iconKey'));

    expect(icons[0]).toBe('Default (container)');
    expect(icons).toContain('firewall');
    // Retired keys are not offered.
    expect(icons).not.toContain('printer');
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
            ajv: createFormValidator(),
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

// The Custom icon field of a device and a group: what it is now, and the
// buttons that open the Custom icons dialog or take the icon away.
describe('the Custom icon field', () => {
  const button = (html, testid) =>
    tags(fieldOf(html, 'icon'), 'button').find((tag) =>
      tag.includes(`data-testid="${testid}"`),
    );
  const images = (html) => tags(fieldOf(html, 'icon'), 'img');
  // The device names the icon plc; `entry` is the diagram's copy of it.
  const withIcon = (entry) => ({
    change: ({ doc, alpha }) =>
      updateNode(doc, alpha.id, { device: { icon: 'plc' } }),
    patch: entry ? { icons: { plc: entry } } : {},
  });

  afterEach(() => {
    iconLibrary.state.index = indexIcons([]);
  });

  test('says None and offers Choose… while the device has none', async () => {
    const html = await renderInspector();
    const field = fieldOf(html, 'icon');
    const choose = button(html, 'inspector-icon-choose');

    expect(text(field)).toContain('Custom icon None Choose…');
    expect(choose).toContain('aria-label="Choose custom icon"');
    expect(choose).toContain('aria-haspopup="dialog"');
    expect(choose).toContain('type="button"');
    expect(choose).not.toContain('disabled');
    expect(button(html, 'inspector-icon-remove')).toBeUndefined();
    expect(images(html)).toEqual([]);
    // The label is the button's, and the help text its description.
    const id = choose.match(/ id="([^"]+)"/)[1];

    expect(field).toContain(`<label for="${id}"`);
    expect(choose).toMatch(/aria-describedby="[^"]*-description"/);
    expect(field).toContain(
      'An image of the server&#39;s icon library, drawn in place of the icon.',
    );
    // The dialog is there only once it is opened.
    expect(html).not.toContain('data-testid="icon-dialog"');
  });

  test('shows the icon and its name, with Change… and Remove', async () => {
    const html = await renderInspector(withIcon({ data: ICON_DATA }));
    const [image] = images(html);

    expect(images(html)).toHaveLength(1);
    expect(image).toContain(`src="data:image/png;base64,${ICON_DATA}"`);
    expect(image).toContain('class="builder-icon builder-icon--custom"');
    expect(image).toContain('width="20"');
    expect(image).toContain('height="20"');
    // Decoration: its alternative text is empty.
    expect(image).toMatch(/ alt(=""|\s)/);
    expect(image).toContain('aria-hidden="true"');
    expect(fieldOf(html, 'icon')).toMatch(
      /data-testid="inspector-icon-name"[^>]*>plc<\/span>/,
    );
    expect(button(html, 'inspector-icon-choose')).toContain(
      'aria-label="Change custom icon"',
    );
    expect(text(fieldOf(html, 'icon'))).toContain('Change… Remove');
    expect(button(html, 'inspector-icon-remove')).toContain(
      'aria-label="Remove custom icon"',
    );
    expect(button(html, 'inspector-icon-remove')).not.toContain('disabled');
  });

  test('shows an icon of the icon library the diagram carries no copy of', async () => {
    iconLibrary.state.index = indexIcons([
      { name: 'PLC', aliases: [], data: ICON_DATA },
    ]);

    const html = await renderInspector(withIcon(null));

    expect(images(html)).toHaveLength(1);
    expect(images(html)[0]).toContain(
      `src="data:image/png;base64,${ICON_DATA}"`,
    );
    // The field holds the name the device names.
    expect(fieldOf(html, 'icon')).toContain('>plc</span>');
  });

  test('says when nothing resolves the name the device names', async () => {
    const unknown = await renderInspector(withIcon(null));

    expect(fieldOf(unknown, 'icon')).toContain(
      '>plc (not found: the built-in icon is shown)</span>',
    );
    // Nothing is drawn from an icon that is not known: the built-in mark.
    expect(images(unknown)).toEqual([]);
    expect(fieldOf(unknown, 'icon')).toContain('builder-icon--image');
    expect(button(unknown, 'inspector-icon-remove')).toContain(
      'aria-label="Remove custom icon"',
    );
  });

  // The bytes of an icon reach the page as the address of an image and as
  // nothing else: data that is no base64 gives no image at all.
  test('draws nothing from data that is not base64', async () => {
    const html = await renderInspector(
      withIcon({ data: '"><script>alert(1)</script>' }),
    );

    expect(images(html)).toEqual([]);
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('alert(1)');
  });

  test('a read-only draft disables its buttons', async () => {
    const html = await renderInspector({
      ...withIcon({ data: ICON_DATA }),
      readOnly: true,
    });

    expect(button(html, 'inspector-icon-choose')).toContain(' disabled');
    expect(button(html, 'inspector-icon-remove')).toContain(' disabled');
    expect(fieldOf(html, 'icon')).toContain('>plc</span>');
  });
});

// A device's notes (general.notes), which a new experiment copies to its
// VM's notes: a list in General in which each note is a text area of its
// own, named after its place in the list.
describe('the Notes list', () => {
  const NOTES = 'spec.general.notes';
  const node = builderSchemaV1.$defs['phenix.v1.minimega_node'];
  const notesSchema = node.properties.general.properties.notes;
  const notes = ['Domain controller.', 'Reset the password\nbefore each run.'];

  // The whole Inspector for the alpha device, holding these notes.
  const renderNotes = (held) =>
    renderInspector({
      change: ({ doc, alpha }) =>
        updateNode(doc, alpha.id, {
          device: {
            spec: {
              ...alpha.device.spec,
              general: { ...alpha.device.spec?.general, notes: held },
            },
          },
        }),
    });

  // The Notes list of a rendered form: its group, up to the group's end
  // after its Add button.
  function notesList(html) {
    const at = html.indexOf(`data-path="${NOTES}"`);

    if (at === -1) {
      return '';
    }

    const add = html.indexOf('array-list-add', at);

    return html.slice(
      html.lastIndexOf('<fieldset', at),
      html.indexOf('</fieldset>', add),
    );
  }

  // The text of the label of the control with an id.
  function labelFor(html, id) {
    const at = id ? html.indexOf(` for="${id}"`) : -1;

    if (at === -1) {
      return '';
    }

    const start = html.indexOf('>', at) + 1;

    return text(html.slice(start, html.indexOf('</label>', start)));
  }

  // Each text area of rendered HTML: the text of its label, and its own.
  function textAreas(html) {
    const areas = html.matchAll(/<textarea\b([^>]*)>([^<]*)<\/textarea>/g);

    return Array.from(areas, ([, attributes, value]) => ({
      label: labelFor(html, /\sid="([^"]+)"/.exec(attributes)?.[1]),
      value,
    }));
  }

  // Changes notes with the functions JSON Forms gives a list control
  // (useJsonFormsArrayControl), which the list's Add note and Remove note
  // buttons call, and returns the notes they leave.
  function editNotes(held, edit) {
    const data = { spec: { general: { hostname: 'alpha', notes: held } } };
    let core = coreReducer(undefined, Actions.init(data, { type: 'object' }));
    const list = mapDispatchToArrayControlProps((action) => {
      core = coreReducer(core, action);
    });

    edit(list);

    return core.data.spec.general.notes;
  }

  const addButton = (list) =>
    tags(list, 'button').find((tag) => tag.includes('array-list-add'));

  test('each note is a text area named after its place, between Node hostname and Snapshot', async () => {
    const html = await renderNotes(notes);
    const list = notesList(html);
    const at = (path) => html.indexOf(`data-path="${path}"`);

    expect(list).toContain('>Notes</span>');
    expect(textAreas(list)).toEqual([
      { label: 'Note 1', value: 'Domain controller.' },
      { label: 'Note 2', value: 'Reset the password\nbefore each run.' },
    ]);
    // No one-line field holds a note, which would lose its line breaks.
    expect(tags(list, 'input')).toEqual([]);
    expect(list).toContain('aria-label="Remove note 1"');
    expect(list).toContain('aria-label="Move note 2 up"');
    expect(list).toMatch(/Add note\s*<\/button>/);
    expect(addButton(list)).not.toMatch(/\sdisabled\b/);

    expect(at('spec.general.hostname')).toBeGreaterThan(-1);
    expect(at(NOTES)).toBeGreaterThan(at('spec.general.hostname'));
    expect(at(NOTES)).toBeLessThan(at('spec.general.snapshot'));
  });

  test('Add note adds an empty note at the end, and Remove note takes one away', async () => {
    // What Add note appends: the default value of a note's schema.
    const added = editNotes(notes, ({ addItem }) =>
      addItem(NOTES, createDefaultValue(notesSchema.items, notesSchema))(),
    );

    expect(added).toEqual([...notes, '']);
    expect(textAreas(notesList(await renderNotes(added)))).toEqual([
      { label: 'Note 1', value: 'Domain controller.' },
      { label: 'Note 2', value: 'Reset the password\nbefore each run.' },
      { label: 'Note 3', value: '' },
    ]);

    const removed = editNotes(added, ({ removeItems }) =>
      removeItems(NOTES, [0])(),
    );

    expect(removed).toEqual([notes[1], '']);
    expect(textAreas(notesList(await renderNotes(removed)))).toEqual([
      { label: 'Note 1', value: 'Reset the password\nbefore each run.' },
      { label: 'Note 2', value: '' },
    ]);

    // With none left, the list says so and still offers Add note.
    const none = editNotes([notes[0]], ({ removeItems }) =>
      removeItems(NOTES, [0])(),
    );
    const empty = notesList(await renderNotes(none));

    expect(none).toEqual([]);
    expect(textAreas(empty)).toEqual([]);
    expect(text(empty)).toContain('No notes.');
    expect(addButton(empty)).not.toMatch(/\sdisabled\b/);
  });

  test('Add note is unavailable at 100 notes, the most a node takes', async () => {
    const many = Array.from({ length: 100 }, (_, i) => `Note ${i + 1}.`);
    const full = notesList(await renderNotes(many));

    expect(textAreas(full)).toHaveLength(100);
    expect(addButton(full)).toMatch(/\sdisabled\b/);
  });
});
