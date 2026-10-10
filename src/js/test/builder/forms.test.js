import { describe, expect, test } from 'vitest';

import {
  isTextOrList,
  numberBranch,
  readNumberText,
} from '@/components/builder/inspector/control.js';

import {
  applyFormData,
  fieldChanged,
  fieldDefault,
  formDataChanged,
  inspectorName,
  inspectorTarget,
  issueText,
  lookChangeLabel,
  mergeFormData,
  newListItem,
  PHENIX_DEFAULTS,
  relevantErrors,
  uiSchemaForKind,
} from '@/builder/adapters/forms.js';
import {
  createFormValidator,
  errorRelevance,
  workingCopyErrors,
} from '@/builder/form-validator.js';
import { builderSchemaV1, schemaForKind } from '@/builder/schema.js';
import {
  addInterface,
  addNetwork,
  addNode,
  connect,
  connectionChanges,
  findNetwork,
  findNode,
  lookOf,
  updateEdge,
  updateNetwork,
  updateNode,
} from '@/builder/model.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';
import { ICON_KEY } from './png.js';

describe('generated UI schemas', () => {
  test('are produced from the JSON Schema, not hand written', () => {
    const ui = uiSchemaForKind(builderSchemaV1, 'device');

    expect(ui.type).toBe('VerticalLayout');
    expect(JSON.stringify(ui)).toContain('#/properties/hostname');
    expect(JSON.stringify(ui)).toContain('#/properties/spec');
  });

  // The node spec's groups came in the schema's order (Advanced,
  // Commands, Delay first) and Type last. The fields looked for most come
  // first now, and those rarely set in a section that starts closed.
  test("a device's spec shows the fields looked for most first", () => {
    const ui = uiSchemaForKind(builderSchemaV1, 'device');
    const spec = ui.elements.find(
      (element) => element.scope === '#/properties/spec',
    ).options.detail;
    const keys = (elements) =>
      elements.map((element) => element.scope?.split('/').pop());
    const section = spec.elements.at(-1);

    expect(spec).toMatchObject({ type: 'Group', label: 'Node' });
    expect(keys(spec.elements.slice(0, -1))).toEqual([
      'type',
      'general',
      'hardware',
      'network',
    ]);
    expect(section).toMatchObject({
      type: 'Group',
      label: 'More settings',
      options: { section: true },
    });
    expect(keys(section.elements)).toEqual([
      'commands',
      'delay',
      'injections',
      'advanced',
      'labels',
      'annotations',
    ]);

    const external = uiSchemaForKind(builderSchemaV1, 'device', {
      spec: { external: true },
    }).elements.find((element) => element.scope === '#/properties/spec').options
      .detail;

    expect(keys(external.elements.slice(0, -1))).toEqual([
      'type',
      'external',
      'general',
      'hardware',
      'network',
    ]);
    expect(keys(external.elements.at(-1).elements)).toEqual([
      'labels',
      'annotations',
    ]);
  });

  // The same object each time, which JSON Forms does not render again.
  test('are the same object for the same element kind and lock', () => {
    const device = uiSchemaForKind(builderSchemaV1, 'device', {
      spec: { type: 'Router' },
    });

    expect(uiSchemaForKind(builderSchemaV1, 'device')).toBe(device);
    expect(
      uiSchemaForKind(builderSchemaV1, 'switch', { readonly: ['name'] }),
    ).toBe(uiSchemaForKind(builderSchemaV1, 'switch', { readonly: ['name'] }));
    expect(
      uiSchemaForKind(builderSchemaV1, 'switch', { readonly: ['name'] }),
    ).not.toBe(uiSchemaForKind(builderSchemaV1, 'switch'));
  });

  test('long text fields render multi-line', () => {
    const ui = uiSchemaForKind(builderSchemaV1, 'note');
    const control = ui.elements.find((element) =>
      element.scope?.endsWith('/text'),
    );

    expect(control.options.multi).toBe(true);
  });
});

describe('inspector working copy', () => {
  test('a device edits hostname, icon and the phenix spec', () => {
    const { doc, alpha } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'node', id: alpha.id });

    expect(target.kind).toBe('device');
    expect(target.data.hostname).toBe('alpha');
    expect(target.data.spec.general.hostname).toBe('alpha');
    expect(target.interfaces).toHaveLength(1);

    // The working copy must be a copy: editing it cannot touch the document.
    target.data.spec.general.hostname = 'changed';

    expect(findNode(doc, alpha.id).device.spec.general.hostname).toBe('alpha');
  });

  test('a switch edits its network', () => {
    const { doc, sw, network } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'node', id: sw.id });

    expect(target.kind).toBe('switch');
    expect(target.data).toMatchObject({ name: 'EXP', alias: 100 });
    expect(target.title).toContain(network.name);
  });

  // The network's fields, then the two colors and the icon size that are
  // the switch node's own. Two switches of one network share the first and
  // not the second.
  test("a switch's working copy has its network's line style and its own colors", () => {
    const { doc, sw, network } = sampleDocument();
    const selection = { type: 'node', id: sw.id };

    expect(inspectorTarget(doc, selection).data).toEqual({
      name: 'EXP',
      alias: 100,
      description: '',
      color: network.color,
      lineStyle: '',
      outlineColor: '',
      fillColor: '',
      iconSize: '',
    });

    const second = addNode(doc, { kind: 'switch', networkId: network.id });
    const styled = updateNode(
      updateNetwork(second.doc, network.id, { lineStyle: 'dotted' }),
      sw.id,
      { switch: { outlineColor: '#a3273f', fillColor: '#6b6f18' } },
    );

    expect(inspectorTarget(styled, selection).data).toMatchObject({
      lineStyle: 'dotted',
      outlineColor: '#a3273f',
      fillColor: '#6b6f18',
    });
    expect(
      inspectorTarget(styled, { type: 'node', id: second.node.id }).data,
    ).toMatchObject({ lineStyle: 'dotted', outlineColor: '', fillColor: '' });
  });

  test("a device's working copy has its look: icon, custom icon, icon size, outline and fill", () => {
    const { doc, alpha } = sampleDocument();
    const selection = { type: 'node', id: alpha.id };

    expect(Object.keys(inspectorTarget(doc, selection).data)).toEqual([
      'hostname',
      'iconKey',
      'icon',
      'iconSize',
      'outlineColor',
      'fillColor',
      'spec',
    ]);
    expect(inspectorTarget(doc, selection).data).toMatchObject({
      icon: '',
      outlineColor: '',
      fillColor: '',
    });

    const colored = updateNode(doc, alpha.id, {
      device: { icon: ICON_KEY, outlineColor: '#2f6fbf', fillColor: '#1f7a5a' },
    });

    expect(inspectorTarget(colored, selection).data).toMatchObject({
      icon: ICON_KEY,
      outlineColor: '#2f6fbf',
      fillColor: '#1f7a5a',
    });
    // Apply writes the look back, and an emptied custom icon goes.
    expect(
      findNode(
        applyFormData(colored, selection, {
          ...inspectorTarget(colored, selection).data,
          fillColor: '#111111',
        }),
        alpha.id,
      ).device,
    ).toMatchObject({ icon: ICON_KEY, fillColor: '#111111' });

    const { icon, ...emptied } = inspectorTarget(colored, selection).data;

    expect(icon).toBe(ICON_KEY);
    expect(
      'icon' in
        findNode(applyFormData(colored, selection, emptied), alpha.id).device,
    ).toBe(false);
  });

  test('an edge edits only its label, color and line style, and is named by its ends', () => {
    const { doc, edge } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'edge', id: edge.id });

    expect(Object.keys(target.data)).toEqual(['label', 'color', 'lineStyle']);
    expect(target.title).toBe('Connection from alpha (eth0) to EXP');
  });

  test('nothing selected edits the document', () => {
    const { doc } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'document' });

    expect(target.data).toEqual({ name: 'Sample', description: '' });
  });

  // Apply announces "Updated <name>".
  test('names what it edits, for announcements', () => {
    const { doc, alpha, sw, edge } = sampleDocument();
    const note = addNode(doc, { kind: 'note', text: 'a' });
    const name = (selection) => inspectorName(note.doc, selection);

    expect(name({ type: 'document' })).toBe('diagram Sample');
    expect(name({ type: 'node', id: alpha.id })).toBe('device alpha');
    expect(name({ type: 'node', id: sw.id })).toBe('network EXP');
    expect(name({ type: 'edge', id: edge.id })).toBe(
      'connection from alpha (eth0) to EXP',
    );
    expect(name({ type: 'node', id: note.node.id })).toBe('note');
    expect(name({ type: 'node', id: 'gone' })).toBe('element');
  });
});

describe('applying a working copy', () => {
  test('applies device changes in one document update', () => {
    const { doc, alpha } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'node', id: alpha.id });

    target.data.hostname = 'alpha2';
    target.data.spec.general.description = 'jump host';

    const next = applyFormData(
      doc,
      { type: 'node', id: alpha.id },
      target.data,
    );
    const node = findNode(next, alpha.id);

    expect(node.device.hostname).toBe('alpha2');
    expect(node.device.spec.general.description).toBe('jump host');
    expect(findNode(doc, alpha.id).device.hostname).toBe('alpha');
  });

  test('applying switch changes renames the network', () => {
    const { doc, sw, network } = sampleDocument();
    const next = applyFormData(
      doc,
      { type: 'node', id: sw.id },
      { name: 'CORE', alias: 200, description: 'core', color: '' },
    );

    expect(findNetwork(next, network.id)).toMatchObject({
      name: 'CORE',
      alias: 200,
    });
  });

  // One document from one Apply, so one Undo step: the network's line
  // style, and the outline and the fill of the switch node itself.
  test("applying a switch sets its network's line style and its own colors", () => {
    const { doc, sw, network } = sampleDocument();
    const selection = { type: 'node', id: sw.id };
    const base = inspectorTarget(doc, selection).data;
    const styled = applyFormData(doc, selection, {
      ...base,
      lineStyle: 'dash-dot',
      outlineColor: '#a3273f',
      fillColor: '#6b6f18',
    });

    expect(findNetwork(styled, network.id).lineStyle).toBe('dash-dot');
    expect(findNode(styled, sw.id).switch).toEqual({
      networkId: network.id,
      outlineColor: '#a3273f',
      fillColor: '#6b6f18',
    });
    // The color is the network's, as before, not the switch's.
    expect(findNetwork(styled, network.id).color).toBe(network.color);

    // Emptied fields have no key in the form, and remove their values.
    const { lineStyle, outlineColor, fillColor, ...emptied } = inspectorTarget(
      styled,
      selection,
    ).data;
    const cleared = applyFormData(styled, selection, emptied);

    expect([lineStyle, outlineColor, fillColor]).toEqual([
      'dash-dot',
      '#a3273f',
      '#6b6f18',
    ]);
    expect('lineStyle' in findNetwork(cleared, network.id)).toBe(false);
    expect(findNode(cleared, sw.id).switch).toEqual({ networkId: network.id });
  });

  test('applying a device sets its outline and fill, and an emptied one goes', () => {
    const { doc, alpha } = sampleDocument();
    const selection = { type: 'node', id: alpha.id };
    const base = inspectorTarget(doc, selection).data;
    const colored = applyFormData(doc, selection, {
      ...base,
      outlineColor: '#2f6fbf',
      fillColor: '#1f7a5a',
    });

    expect(findNode(colored, alpha.id).device).toMatchObject({
      outlineColor: '#2f6fbf',
      fillColor: '#1f7a5a',
    });

    const { fillColor, ...emptied } = inspectorTarget(colored, selection).data;
    const cleared = applyFormData(colored, selection, emptied).nodes.find(
      (node) => node.id === alpha.id,
    ).device;

    expect(fillColor).toBe('#1f7a5a');
    expect(cleared.outlineColor).toBe('#2f6fbf');
    expect('fillColor' in cleared).toBe(false);
    // A device given no color keeps the keys it had, and no others.
    expect(
      Object.keys(
        findNode(applyFormData(doc, selection, base), alpha.id).device,
      ),
    ).toEqual(Object.keys(alpha.device));
  });

  test('applying note and group changes keeps the payload shape', () => {
    let { doc } = sampleDocument();
    const note = addNode(doc, { kind: 'note', text: 'a' });
    doc = note.doc;

    const next = applyFormData(
      doc,
      { type: 'node', id: note.node.id },
      { text: 'b', color: '#fff' },
    );

    expect(findNode(next, note.node.id).note).toEqual({
      text: 'b',
      color: '#fff',
    });
  });

  // The canvas does not draw a collapsed group, so the Inspector does not
  // offer the field; a document that has it keeps it.
  test('a group has no Collapsed field, and keeps the value it has', () => {
    let { doc } = sampleDocument();
    const group = addNode(doc, { kind: 'group', title: 'Core' });
    doc = updateNode(group.doc, group.node.id, { group: { collapsed: true } });
    const selection = { type: 'node', id: group.node.id };

    expect(
      Object.keys(schemaForKind(builderSchemaV1, 'group').properties),
    ).toEqual([
      'title',
      'description',
      'color',
      'borderStyle',
      'iconKey',
      'icon',
      'iconSize',
    ]);
    expect(
      JSON.stringify(uiSchemaForKind(builderSchemaV1, 'group')),
    ).not.toContain('collapsed');
    expect(inspectorTarget(doc, selection).data).toEqual({
      title: 'Core',
      description: '',
      color: '',
      borderStyle: '',
      iconKey: '',
      icon: '',
      iconSize: '',
    });

    const next = applyFormData(doc, selection, { title: 'Edge', color: '' });

    expect(findNode(next, group.node.id).group).toEqual({
      title: 'Edge',
      color: '',
      collapsed: true,
    });
  });

  test("a group's description, border pattern, icon and custom icon apply, and emptied ones go", () => {
    const { doc } = sampleDocument();
    const group = addNode(doc, { kind: 'group', title: 'Core' });
    const selection = { type: 'node', id: group.node.id };
    const described = applyFormData(group.doc, selection, {
      title: 'Core',
      description: 'DMZ hosts',
      color: '',
      borderStyle: 'double',
      iconKey: 'firewall',
      icon: ICON_KEY,
    });

    expect(findNode(described, group.node.id).group).toEqual({
      title: 'Core',
      description: 'DMZ hosts',
      color: '',
      borderStyle: 'double',
      iconKey: 'firewall',
      icon: ICON_KEY,
      collapsed: false,
    });
    expect(inspectorTarget(described, selection).data).toEqual({
      title: 'Core',
      description: 'DMZ hosts',
      color: '',
      borderStyle: 'double',
      iconKey: 'firewall',
      icon: ICON_KEY,
      iconSize: '',
    });
    // The description is a long text field, as a network's is.
    expect(
      uiSchemaForKind(builderSchemaV1, 'group').elements.find((element) =>
        element.scope.endsWith('/description'),
      ).options.multi,
    ).toBe(true);

    const cleared = applyFormData(described, selection, { title: 'Core' });

    expect(findNode(cleared, group.node.id).group).toEqual(group.node.group);
  });

  // A field emptied in the form loses its key; the element shows an unset
  // value as ''. Emptying a field that was empty is no change.
  test('an emptied field is no change from an unset one', () => {
    const { doc, sw } = sampleDocument();
    const base = inspectorTarget(doc, { type: 'node', id: sw.id }).data;
    const { description, ...emptied } = base;

    expect(description).toBe('');
    expect(formDataChanged(emptied, base)).toBe(false);
    expect(formDataChanged({ ...emptied, description: 'core' }, base)).toBe(
      true,
    );
    // Filled again, the field comes back as the last key.
    expect(
      formDataChanged(
        { ...emptied, description: 'core' },
        { ...base, description: 'core' },
      ),
    ).toBe(false);
    expect(
      formDataChanged(
        { spec: { general: {} }, hostname: 'a' },
        { hostname: 'a', spec: { general: { description: '' } } },
      ),
    ).toBe(false);
    expect(formDataChanged({ collapsed: false }, { collapsed: true })).toBe(
      true,
    );
  });

  test('an emptied diagram description clears it', () => {
    const { doc } = sampleDocument();
    const described = {
      ...doc,
      metadata: { ...doc.metadata, description: 'Lab' },
    };
    const base = inspectorTarget(described, { type: 'document' }).data;

    expect(base).toEqual({ name: 'Sample', description: 'Lab' });
    expect(formDataChanged({ name: 'Sample' }, base)).toBe(true);

    const applied = applyFormData(
      described,
      { type: 'document' },
      { name: 'Sample' },
    );

    expect(applied.metadata.description).toBe('');
    expect(applied.metadata.id).toBe(doc.metadata.id);
    expect(applied).not.toHaveProperty('description');
  });

  test("a connection's label and color apply, and emptied ones go", () => {
    const { doc, edge } = sampleDocument();
    const selection = { type: 'edge', id: edge.id };
    const colored = applyFormData(doc, selection, {
      label: 'uplink',
      color: '#1f7a5a',
    });

    expect(colored.edges[0]).toMatchObject({
      label: 'uplink',
      color: '#1f7a5a',
    });
    expect(inspectorTarget(colored, selection).data).toEqual({
      label: 'uplink',
      color: '#1f7a5a',
      lineStyle: '',
    });

    const emptied = applyFormData(colored, selection, {});

    expect('label' in emptied.edges[0]).toBe(false);
    expect('color' in emptied.edges[0]).toBe(false);
  });

  test("a connection's line style applies, and an emptied one goes", () => {
    const { doc, edge } = sampleDocument();
    const selection = { type: 'edge', id: edge.id };
    const styled = applyFormData(doc, selection, { lineStyle: 'dotted' });

    expect(styled.edges[0].lineStyle).toBe('dotted');
    expect(inspectorTarget(styled, selection).data.lineStyle).toBe('dotted');
    expect('lineStyle' in applyFormData(styled, selection, {}).edges[0]).toBe(
      false,
    );
  });
});

// A line style, a border pattern and a group's icon each have a choice
// that stands for none, which names what the canvas then draws.
describe('what an unset presentation field comes to', () => {
  test("a network's line style is the pattern of its place in the diagram", () => {
    const { doc, sw, network } = sampleDocument();
    const second = addNetwork(doc, { name: 'MGMT' });
    const other = addNode(second.doc, {
      kind: 'switch',
      networkId: second.network.id,
    });
    const target = (document, id) =>
      inspectorTarget(document, { type: 'node', id });
    const note = "Auto: chosen by the network's place in the diagram";

    expect(fieldDefault(target(other.doc, sw.id), 'lineStyle')).toEqual({
      value: 'solid',
      note,
    });
    expect(fieldDefault(target(other.doc, other.node.id), 'lineStyle')).toEqual(
      { value: 'dashed', note },
    );

    // A style chosen for it does not change what Auto would give.
    const styled = updateNetwork(other.doc, network.id, {
      lineStyle: 'dotted',
    });

    expect(fieldDefault(target(styled, sw.id), 'lineStyle').value).toBe(
      'solid',
    );
  });

  test("a connection's line style is its network's, chosen or not", () => {
    const { doc, edge, network } = sampleDocument();
    const selection = { type: 'edge', id: edge.id };
    const note = "Auto: its network's line style";

    expect(fieldDefault(inspectorTarget(doc, selection), 'lineStyle')).toEqual({
      value: 'solid',
      note,
    });

    const styled = updateNetwork(doc, network.id, { lineStyle: 'dash-dot' });

    expect(
      fieldDefault(inspectorTarget(styled, selection), 'lineStyle'),
    ).toEqual({ value: 'dash-dot', note });
    // Its own style does not change what its network's is.
    expect(
      fieldDefault(
        inspectorTarget(
          updateEdge(styled, edge.id, { lineStyle: 'dotted' }),
          selection,
        ),
        'lineStyle',
      ).value,
    ).toBe('dash-dot');
  });

  test("a group's border is dashed and its icon the group icon", () => {
    const { doc } = sampleDocument();
    const group = addNode(doc, { kind: 'group', title: 'Core' });
    const target = inspectorTarget(group.doc, {
      type: 'node',
      id: group.node.id,
    });

    expect(fieldDefault(target, 'borderStyle')).toEqual({
      value: 'dashed',
      note: 'The default border',
    });
    expect(fieldDefault(target, 'iconKey')).toEqual({
      value: 'container',
      note: 'The group icon',
    });
    // Its other fields have none, nor has a device's icon.
    expect(fieldDefault(target, 'title')).toBeUndefined();
    expect(fieldDefault(target, 'color')).toBeUndefined();
  });

  // The working copy shows an unset one as '', which is none of the
  // patterns: that is how the element came, and holds no edit back.
  test('an unset line style or border pattern keeps no other edit from being applied', () => {
    const { doc, sw, edge } = sampleDocument();
    const group = addNode(doc, { kind: 'group', title: 'Core' });
    const found = (kind, selection, edit) => {
      const base = inspectorTarget(group.doc, selection).data;

      return workingCopyErrors(
        createFormValidator(),
        schemaForKind(builderSchemaV1, kind),
        { ...base, ...edit },
        base,
      ).fields.map((field) => field.message);
    };
    const selections = {
      switch: { type: 'node', id: sw.id },
      edge: { type: 'edge', id: edge.id },
      group: { type: 'node', id: group.node.id },
    };

    expect(found('switch', selections.switch, { name: 'CORE' })).toEqual([]);
    expect(found('edge', selections.edge, { label: 'uplink' })).toEqual([]);
    expect(found('group', selections.group, { title: 'Edge' })).toEqual([]);
    expect(found('switch', selections.switch, { lineStyle: 'dotted' })).toEqual(
      [],
    );
    // A value that is none of them, set by the edit, does.
    expect(found('edge', selections.edge, { lineStyle: 'wavy' })).toEqual([
      'Line style must be one of the listed options',
    ]);
    expect(found('group', selections.group, { borderStyle: 'wavy' })).toEqual([
      'Border pattern must be one of the listed options',
    ]);
  });

  test("a device's icon and colors have no such value", () => {
    const { doc, alpha } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'node', id: alpha.id });

    for (const path of ['iconKey', 'icon', 'outlineColor', 'fillColor']) {
      expect(fieldDefault(target, path), path).toBeUndefined();
    }
  });
});

// A change of a device's look is applied at once, as an edit named after
// the field it changes: the name is announced, and is the Undo step's.
describe('what a change of look says', () => {
  const none = lookOf({});

  test('names the icon, the outline color or the fill color', () => {
    const title = 'Device web-01';

    expect(lookChangeLabel(title, none, { ...none, iconKey: 'router' })).toBe(
      'Changed the icon of Device web-01 to router',
    );
    expect(lookChangeLabel(title, { ...none, iconKey: 'router' }, none)).toBe(
      'Changed the icon of Device web-01 to the default',
    );
    expect(
      lookChangeLabel(title, none, { ...none, outlineColor: '#2f6fbf' }),
    ).toBe('Changed the outline color of Device web-01 to #2f6fbf');
    expect(
      lookChangeLabel(title, { ...none, outlineColor: '#2f6fbf' }, none),
    ).toBe('Removed the outline color of Device web-01');
    expect(
      lookChangeLabel(title, none, { ...none, fillColor: '#2f6fbf' }),
    ).toBe('Changed the fill color of Device web-01 to #2f6fbf');
    expect(
      lookChangeLabel(title, { ...none, fillColor: '#2f6fbf' }, none),
    ).toBe('Removed the fill color of Device web-01');
  });

  // A custom icon is said by its name, which is what the field holds.
  test('names a custom icon by its name', () => {
    const title = 'Device web-01';
    const custom = { ...none, icon: 'plc' };

    expect(lookChangeLabel(title, none, custom)).toBe(
      'Changed the custom icon of Device web-01 to plc',
    );
    expect(lookChangeLabel(title, custom, none)).toBe(
      'Removed the custom icon of Device web-01',
    );
    // The icon of its key comes first when both change.
    expect(lookChangeLabel(title, none, { ...custom, iconKey: 'router' })).toBe(
      'Changed the icon of Device web-01 to router',
    );
    // And the custom icon before a color.
    expect(
      lookChangeLabel(title, none, { ...custom, fillColor: '#222222' }),
    ).toBe('Changed the custom icon of Device web-01 to plc');
  });

  test('names the first field that differs, and nothing when none does', () => {
    expect(
      lookChangeLabel('Device a', none, {
        iconKey: 'router',
        outlineColor: '#111111',
        fillColor: '#222222',
      }),
    ).toBe('Changed the icon of Device a to router');
    expect(lookChangeLabel('Device a', none, { ...none })).toBe('');
  });

  test('the look of a payload is its five fields as text', () => {
    expect(lookOf(undefined)).toEqual({
      iconKey: '',
      icon: '',
      iconSize: '',
      outlineColor: '',
      fillColor: '',
    });
    expect(
      lookOf({ hostname: 'a', iconKey: 'router', fillColor: '#222222' }),
    ).toEqual({
      iconKey: 'router',
      icon: '',
      iconSize: '',
      outlineColor: '',
      fillColor: '#222222',
    });
    expect(lookOf({ icon: ICON_KEY }).icon).toBe(ICON_KEY);
    expect(lookOf({ outlineColor: null }).outlineColor).toBe('');
  });
});

// A field shows one message: of the errors one value breaks, the one that
// says best what a valid value looks like.
describe('the most relevant error of a field', () => {
  test('format and pattern, then choices, bounds, length, type, required', () => {
    const ranked = [
      { keyword: 'required', params: { missingProperty: 'a' } },
      { keyword: 'type', params: { type: 'string' } },
      { keyword: 'minLength', params: { limit: 7 } },
      { keyword: 'maximum', params: { limit: 9 } },
      { keyword: 'enum', params: {} },
      { keyword: 'format', params: { format: 'ipv4' } },
    ]
      .sort((a, b) => errorRelevance(a) - errorRelevance(b))
      .map((error) => error.keyword);

    expect(ranked).toEqual([
      'format',
      'enum',
      'maximum',
      'minLength',
      'type',
      'required',
    ]);
    expect(errorRelevance({ keyword: 'pattern' })).toBe(
      errorRelevance({ keyword: 'format' }),
    );
    // An empty value is said to be empty before anything else.
    expect(
      errorRelevance({ keyword: 'minLength', params: { limit: 1 } }),
    ).toBeLessThan(errorRelevance({ keyword: 'pattern' }));
  });

  test('an address too short and no IPv4 address says the second', () => {
    const { doc, alpha } = sampleDocument();
    const staticSpec = {
      ...alpha.device.spec,
      network: {
        interfaces: [
          {
            name: 'eth0',
            type: 'ethernet',
            proto: 'static',
            vlan: 'EXP',
            address: '10.0.0.1',
            mask: 24,
          },
        ],
      },
    };
    const target = inspectorTarget(
      updateNode(doc, alpha.id, {
        device: { ...alpha.device, spec: staticSpec },
      }),
      { type: 'node', id: alpha.id },
    );
    const schema = schemaForKind(builderSchemaV1, 'device', {
      spec: staticSpec,
    });
    const edited = JSON.parse(JSON.stringify(target.data));

    edited.spec.network.interfaces[0].address = '1';

    const found = workingCopyErrors(
      createFormValidator(),
      schema,
      edited,
      target.data,
    );
    const atAddress = (errors) =>
      errors.filter(
        (error) =>
          error.instancePath === '/spec/network/interfaces/0/address' &&
          error.schemaPath.includes('/oneOf/0/'),
      );

    expect(atAddress(found.errors).map((error) => error.keyword)).toEqual(
      expect.arrayContaining(['minLength', 'format']),
    );
    expect(
      atAddress(relevantErrors(found.errors)).map((error) => error.keyword),
    ).toEqual(['format']);
    // The oneOf's own error stays: JSON Forms shows a field the errors of
    // the alternative it renders while it is listed.
    expect(
      relevantErrors(found.errors).some((error) => error.keyword === 'oneOf'),
    ).toBe(true);
    expect(found.fields.map((field) => field.message)).toEqual([
      'Interface 1: Address must be an IPv4 address, such as 10.0.0.1',
    ]);
  });
});

// Memory and VCPUs are a number or text to phenix, edited as a number; DNS
// servers are one address or a list, edited as one line.
describe('fields edited as one value', () => {
  const spec = schemaForKind(builderSchemaV1, 'device').properties.spec;
  const hardware = spec.properties.hardware.properties;
  const address = spec.properties.network.properties.interfaces.items.oneOf[0];

  test('a number or text is a number, with its bounds', () => {
    expect(numberBranch(hardware.memory)).toMatchObject({
      type: 'integer',
      minimum: 1,
    });
    expect(numberBranch(hardware.vcpus)).toMatchObject({ type: 'integer' });
    expect(numberBranch(hardware.os_type)).toBeUndefined();
    expect(numberBranch({ oneOf: [{ const: 1 }, { const: 'a' }] })).toBe(
      undefined,
    );
  });

  test('one text value or a list is a line of text', () => {
    expect(isTextOrList(address.properties.dns)).toBe(true);
    expect(isTextOrList(hardware.memory)).toBe(false);
    expect(isTextOrList({ type: 'string' })).toBe(false);
  });

  // A whole-number field read "1.5" and "1e3" as 1 with parseInt, and said
  // nothing.
  test('text is read as a number, and a whole number only as digits', () => {
    expect(readNumberText(' 3 ', true)).toBe(3);
    expect(readNumberText('-07', true)).toBe(-7);
    expect(readNumberText('1.5', true)).toBeNaN();
    expect(readNumberText('1e3', true)).toBeNaN();
    expect(readNumberText('1.', true)).toBeNaN();
    expect(readNumberText('1.5', false)).toBe(1.5);
    expect(readNumberText('1e3', false)).toBe(1000);
    // No number at all, such as a template.
    expect(readNumberText('{{ .Memory }}', true)).toBeUndefined();
    expect(readNumberText('', false)).toBeUndefined();
  });
});

// An unset field shows the value it comes to all the same, which is not
// stored.
describe('what an unset field shows', () => {
  const { doc, alpha, edge, network } = sampleDocument();
  const device = inspectorTarget(doc, { type: 'node', id: alpha.id });
  const memory = { default: 1024 };

  // The schema said 1024, where phenix gives a VM 512 (setDefaults in
  // src/go/types/version/v1/node.go), as the field's description says.
  test("the schema's Memory default is what phenix gives a VM", () => {
    for (const kind of ['minimega_node', 'external_node']) {
      const hardware =
        builderSchemaV1.$defs[`phenix.v1.${kind}`].properties.hardware;

      expect(hardware.properties.memory.default).toBe(
        PHENIX_DEFAULTS['hardware.memory'],
      );
    }
  });

  test("a device's spec field shows what phenix gives it", () => {
    // phenix gives a VM 512 megabytes, whatever the schema says.
    expect(fieldDefault(device, 'spec.hardware.memory', memory)).toEqual({
      value: 512,
      note: 'phenix uses this value while the field is empty',
    });
    expect(
      fieldDefault(device, 'spec.hardware.drives.1.inject_partition', {}),
    ).toMatchObject({ value: 1 });
    // Else the schema's default.
    expect(
      fieldDefault(device, 'spec.network.interfaces.0.mtu', { default: 1500 }),
    ).toEqual({
      value: 1500,
      note: 'The default, used while the field is empty',
    });
    expect(fieldDefault(device, 'spec.general.description', {})).toBe(
      undefined,
    );
  });

  test('a device phenix does not deploy has only its schema defaults', () => {
    const external = {
      ...device,
      data: { ...device.data, spec: { external: true } },
    };

    expect(fieldDefault(external, 'spec.hardware.memory', memory)).toEqual(
      expect.objectContaining({ value: 1024 }),
    );
  });

  test("a connection's label is its network's name", () => {
    const connection = inspectorTarget(doc, { type: 'edge', id: edge.id });

    expect(fieldDefault(connection, 'label', {})).toEqual({
      value: findNetwork(doc, network.id).name,
      note: "The network's name",
    });
    expect(fieldDefault(connection, 'color', {})).toBe(undefined);
  });
});

// A field the working copy changed is marked until the change is applied
// or cancelled; the entries of keys and values were never marked.
describe('which fields a working copy changed', () => {
  const base = {
    name: 'EXP',
    description: '',
    spec: {
      hardware: { memory: 512 },
      labels: { role: 'web', 'app.kubernetes.io/name': 'nginx' },
    },
  };
  const edit = (change) => {
    const data = JSON.parse(JSON.stringify(base));

    change(data);

    return data;
  };

  test('a field, not the list or group it is in', () => {
    const data = edit((copy) => {
      copy.spec.hardware.memory = 1024;
      delete copy.description;
    });

    expect(fieldChanged(data, base, 'spec.hardware.memory')).toBe(true);
    expect(fieldChanged(data, base, 'spec.hardware')).toBe(false);
    // An unset field is the same as an empty one.
    expect(fieldChanged(data, base, 'description')).toBe(false);
    expect(fieldChanged(base, base, 'name')).toBe(false);
  });

  test('an entry of keys and values, by its key', () => {
    const data = edit((copy) => {
      copy.spec.labels['app.kubernetes.io/name'] = 'httpd';
      copy.spec.labels.tier = '';
    });

    // A key may hold dots.
    expect(
      fieldChanged(data, base, 'spec.labels', 'app.kubernetes.io/name'),
    ).toBe(true);
    // Added, even with no value.
    expect(fieldChanged(data, base, 'spec.labels', 'tier')).toBe(true);
    expect(fieldChanged(data, base, 'spec.labels', 'role')).toBe(false);
    // Removed, and with the whole map removed.
    expect(
      fieldChanged(
        edit((copy) => delete copy.spec.labels.role),
        base,
        'spec.labels',
        'role',
      ),
    ).toBe(true);
    expect(
      fieldChanged(
        edit((copy) => delete copy.spec.labels),
        base,
        'spec.labels',
        'role',
      ),
    ).toBe(true);
    expect(fieldChanged(base, base, 'spec.advanced', 'qemu-append')).toBe(
      false,
    );
  });
});

// Apply, Save now and a selection change merge the working copy's edits
// into the element as it is then, rather than put the working copy over it.
describe('merging a working copy into the element as it is now', () => {
  const selection = (node) => ({ type: 'node', id: node.id });

  test('an interface added and connected since stays, with its connection', () => {
    let { doc, bravo, sw } = sampleDocument();
    const base = inspectorTarget(doc, selection(bravo)).data;
    const edited = JSON.parse(JSON.stringify(base));

    edited.spec.general.description = 'Edited before wiring';
    // Meanwhile: Add connection point, and a connection from the outline.
    const added = addInterface(doc, bravo.id);
    doc = connect(added.doc, {
      sourceNodeId: bravo.id,
      sourceHandleId: added.handle.id,
      targetNodeId: sw.id,
    }).doc;
    // And a rename in the outline.
    doc = updateNode(doc, bravo.id, { device: { hostname: 'renamed' } });

    const current = inspectorTarget(doc, selection(bravo)).data;
    const next = applyFormData(
      doc,
      selection(bravo),
      mergeFormData(base, edited, current),
    );
    const node = findNode(next, bravo.id);

    expect(node.device.hostname).toBe('renamed');
    expect(node.device.spec.general.description).toBe('Edited before wiring');
    expect(node.device.interfaces.map((handle) => handle.name)).toEqual([
      'eth0',
      'eth1',
    ]);
    expect(
      node.device.spec.network.interfaces.map((iface) => iface.vlan),
    ).toEqual(['', 'EXP']);
    expect(next.edges.filter((edge) => edge.sourceNodeId === bravo.id)).toEqual(
      doc.edges.filter((edge) => edge.sourceNodeId === bravo.id),
    );
  });

  test('a VLAN left as it was takes the connection made since', () => {
    const iface = (name, vlan) =>
      vlan === undefined ? { name } : { name, vlan };
    const spec = (...interfaces) => ({ network: { interfaces } });

    expect(
      mergeFormData(
        spec(iface('eth0', ''), iface('eth1', 'EXP')),
        {
          general: { description: 'edited' },
          ...spec(iface('eth0'), iface('eth1', 'LAN'), iface('eth2', 'EXP')),
        },
        spec(iface('eth0', 'EXP'), iface('eth1', 'EXP')),
      ),
    ).toEqual({
      general: { description: 'edited' },
      ...spec(iface('eth0', 'EXP'), iface('eth1', 'LAN'), iface('eth2', 'EXP')),
    });
  });

  test('named list items: renamed in place, removed since stay removed, added since stay', () => {
    const base = [
      { name: 'eth0', vlan: 'A' },
      { name: 'eth1', vlan: 'B' },
    ];
    // Renames eth0 and changes eth1, and adds eth2.
    const edited = [
      { name: 'ens3', vlan: 'A' },
      { name: 'eth1', vlan: 'C' },
      { name: 'eth2', vlan: '' },
    ];

    // Meanwhile eth1 was removed and eth5 added.
    expect(
      mergeFormData(base, edited, [
        { name: 'eth0', vlan: 'A', mtu: 9000 },
        { name: 'eth5', vlan: 'D' },
      ]),
    ).toEqual([
      { name: 'ens3', vlan: 'A', mtu: 9000 },
      { name: 'eth5', vlan: 'D' },
      { name: 'eth2', vlan: '' },
    ]);
    // With the same items as then, the working copy's order wins.
    expect(mergeFormData(base, [...edited].reverse(), base)).toEqual(
      [...edited].reverse(),
    );
    // Removed in the working copy, it goes.
    expect(
      mergeFormData(base, [base[1]], [base[0], { ...base[1], vlan: 'X' }]),
    ).toEqual([{ name: 'eth1', vlan: 'X' }]);
  });

  test("where both changed a field, the working copy's value wins", () => {
    expect(
      mergeFormData(
        { name: 'a', color: 'red', note: 'x' },
        { name: 'b', color: 'red', note: 'x' },
        { name: 'c', color: 'blue' },
      ),
    ).toEqual({ name: 'b', color: 'blue' });
  });
});

// The form was the only way to rename an interface, and a rename
// minted a new handle, which lost the interface its connection.
describe('renaming an interface in the Inspector', () => {
  test('keeps its handle and its connection', () => {
    const { doc, alpha } = sampleDocument();
    const selection = { type: 'node', id: alpha.id };
    const data = inspectorTarget(doc, selection).data;
    const [handle] = findNode(doc, alpha.id).device.interfaces;

    data.spec.network.interfaces[0].name = 'ens3';

    const next = applyFormData(doc, selection, data);
    const node = findNode(next, alpha.id);

    expect(node.device.interfaces).toEqual([
      { id: handle.id, name: 'ens3', index: 0 },
    ]);
    expect(next.edges).toEqual(doc.edges);
    expect(node.device.spec.network.interfaces[0].vlan).toBe('EXP');
    expect(connectionChanges(doc, next, alpha.id)).toEqual([]);
  });

  // A connected interface removed while another is added is no
  // rename, so the new one is not said to be disconnected.
  test('an interface replaced by a new one does not take its handle', () => {
    const { doc, alpha } = sampleDocument();
    const selection = { type: 'node', id: alpha.id };
    const data = inspectorTarget(doc, selection).data;
    const [handle] = findNode(doc, alpha.id).device.interfaces;

    data.spec.network.interfaces = [
      { name: 'eth1', type: 'ethernet', proto: 'dhcp' },
    ];

    const next = applyFormData(doc, selection, data);
    const [added] = findNode(next, alpha.id).device.interfaces;

    expect(added.name).toBe('eth1');
    expect(added.id).not.toBe(handle.id);
    expect(next.edges).toEqual([]);
    expect(connectionChanges(doc, next, alpha.id)).toEqual([]);
  });

  test('gives each of two interfaces of one name a handle of its own', () => {
    const { doc, alpha } = sampleDocument();
    const selection = { type: 'node', id: alpha.id };
    const data = inspectorTarget(doc, selection).data;

    data.spec.network.interfaces.push({ name: 'eth0', type: 'ethernet' });

    const handles = findNode(applyFormData(doc, selection, data), alpha.id)
      .device.interfaces;

    expect(handles.map((handle) => handle.name)).toEqual(['eth0', 'eth0']);
    expect(new Set(handles.map((handle) => handle.id)).size).toBe(2);
  });
});

describe('diagram check messages', () => {
  // validate.js reports elements by index (nodes[0]); the Inspector names
  // them, numbering devices only when their hostnames collide.
  test('name elements instead of indexing them', () => {
    const { doc, bravo } = sampleDocument();
    const clash = updateNode(doc, bravo.id, {
      device: { ...bravo.device, hostname: 'alpha' },
    });
    const texts = validateDocument(clash)
      .filter((issue) => issue.level === 'error')
      .map((issue) => issueText(clash, issue));

    expect(texts).toContain(
      'Device alpha #2: duplicate hostname "alpha" (also device alpha #1)',
    );
    expect(
      issueText(doc, { path: 'networks[0].name', message: 'bad name' }),
    ).toBe('Network EXP: bad name');
    expect(issueText(doc, { path: 'id', message: 'document ID' })).toBe(
      'document ID',
    );
  });
});

// The Inspector's Add interface names a new interface the way a connection
// drawn on the canvas does, and makes it Ethernet with no address.
describe('a list item added in the Inspector', () => {
  const selectionOf = (node) => ({ type: 'node', id: node.id });

  test('a new interface follows the device and working copy interfaces', () => {
    const { doc, alpha } = sampleDocument();
    const path = 'spec.network.interfaces';
    const working = (...names) => ({
      spec: {
        ...alpha.device.spec,
        network: { interfaces: names.map((name) => ({ name })) },
      },
    });

    // alpha has eth0, as a handle and in its spec.
    expect(newListItem(doc, selectionOf(alpha), path)).toEqual({
      name: 'eth1',
      type: 'ethernet',
      proto: 'manual',
    });
    expect(
      newListItem(doc, selectionOf(alpha), path, working('eth0', 'eth4')),
    ).toMatchObject({ name: 'eth5' });
  });

  test("an external device's interface has no type", () => {
    const { doc, alpha } = sampleDocument();
    const external = { spec: { external: true, type: 'HIL', network: {} } };

    expect(
      newListItem(doc, selectionOf(alpha), 'spec.network.interfaces', external),
    ).toEqual({ name: 'eth1', proto: 'manual' });
  });

  test('other lists and elements add their schema default', () => {
    const { doc, alpha, sw } = sampleDocument();

    expect(
      newListItem(doc, selectionOf(alpha), 'spec.hardware.drives'),
    ).toBeUndefined();
    expect(
      newListItem(doc, selectionOf(sw), 'spec.network.interfaces'),
    ).toBeUndefined();
  });
});
