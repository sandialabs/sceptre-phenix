import { afterEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, effectScope, h, nextTick, ref } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';
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
  uiSchemaForKind,
} from '@/builder/adapters/forms.js';
import { createFormValidator } from '@/builder/form-validator.js';
import { builderSchemaV1, schemaForKind } from '@/builder/schema.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

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

// Tags of one element name in rendered HTML.
function tags(html, name) {
  return html.match(new RegExp(`<${name}\\b[^>]*>`, 'g')) || [];
}

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

    // Memory and VCPUs, a number or text to phenix, are number fields with
    // no picker for their kind; an unset one shows the value phenix gives
    // it, marked as the default and not stored (VCPUs is not set here).
    const [memory] = tags(html, 'input').filter((tag) =>
      tag.includes('memory-input'),
    );
    const [vcpus] = tags(html, 'input').filter((tag) =>
      tag.includes('vcpus-input'),
    );
    expect(memory).toMatch(/type="number"[^>]*min="1"[^>]*value="512"/);
    expect(memory).not.toContain('-default');
    expect(vcpus).toMatch(/type="number"[^>]*value="1"/);
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

  // R34, V6: labels, annotations and advanced settings are rows of a named
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

    expect(mtu).toContain('min="0"');
    expect(mtu).toContain('max="16000"');
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
// sets the store's schema state (schemaSource, schemaError).
async function renderInspector({ readOnly = false, schema = {} } = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(BuilderInspector) });
  app.use(pinia);

  const store = useBuilderStore(pinia);
  const { doc, alpha } = sampleDocument();
  store.doc = doc;
  store.readOnly = readOnly;
  Object.assign(store, schema);
  store.select({ nodes: [alpha.id] });

  return renderToString(app);
}

// The whole Inspector for a device of a draft opened read-only (A38): its own
// buttons and fields are disabled as well as the JSON Forms ones.
test('a read-only draft disables every Inspector control', async () => {
  const html = await renderInspector({ readOnly: true });
  const controls = ['input', 'select', 'textarea', 'button'].flatMap((name) =>
    tags(html, name),
  );
  const enabled = controls.filter((tag) => !/\sdisabled\b/.test(tag));

  expect(html).toContain('Remove connection point eth0');
  expect(controls.length).toBeGreaterThan(20);
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
