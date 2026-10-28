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
  instantChangeLabel,
  instantKeys,
  instantValues,
  issueText,
  keepsWorkingCopy,
  LAYER_UI_TYPE,
  lookChangeLabel,
  mergeFormData,
  newListItem,
  PHENIX_DEFAULTS,
  rebasedWorkingCopy,
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

  // The fields looked for most come first, and the rarely set ones in a
  // section that starts closed.
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

  // The template editor shows the layer as a field of the form, after the
  // node's look and above its spec. The canvas form has none.
  test('with layer, put the layer after the look fields', () => {
    const keys = (ui) =>
      ui.elements.map((element) =>
        element.type === LAYER_UI_TYPE
          ? 'layer'
          : element.scope?.split('/').pop(),
      );
    const device = uiSchemaForKind(builderSchemaV1, 'device', {
      layer: true,
    });

    expect(keys(device)).toEqual([
      'hostname',
      'iconKey',
      'iconSize',
      'outlineColor',
      'fillColor',
      'layer',
      'spec',
    ]);
    expect(keys(uiSchemaForKind(builderSchemaV1, 'device'))).not.toContain(
      'layer',
    );
    expect(uiSchemaForKind(builderSchemaV1, 'device', { layer: true })).toBe(
      device,
    );
    expect(
      keys(uiSchemaForKind(builderSchemaV1, 'switch', { layer: true })),
    ).toContain('layer');
  });

  // The Icon field chooses the built-in icon and the custom icon (see
  // InspectorIconControl), so the custom icon has no field of its own.
  test('a node with an icon has one Icon field for both icons', () => {
    const keys = (kind) =>
      uiSchemaForKind(builderSchemaV1, kind).elements.map((element) =>
        element.scope?.split('/').pop(),
      );

    for (const kind of ['device', 'group', 'icon']) {
      expect(keys(kind), kind).toContain('iconKey');
      expect(keys(kind), kind).not.toContain('icon');
      expect(
        schemaForKind(builderSchemaV1, kind).properties.icon,
        kind,
      ).toBeDefined();
    }
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

  // The network's fields, then the switch node's own colors and icon size. Two
  // switches of one network share only the first.
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

  test("a switch edits its network's subnet and gateway, and clears them", () => {
    const { doc, sw, network } = sampleDocument();
    const selection = { type: 'node', id: sw.id };
    const base = inspectorTarget(doc, selection).data;

    expect(base).not.toHaveProperty('subnet');

    const set = applyFormData(doc, selection, {
      ...base,
      subnet: '10.9.0.0/24',
      gateway: '10.9.0.1',
    });

    expect(findNetwork(set, network.id)).toMatchObject({
      subnet: '10.9.0.0/24',
      gateway: '10.9.0.1',
    });
    expect(inspectorTarget(set, selection).data).toMatchObject({
      subnet: '10.9.0.0/24',
      gateway: '10.9.0.1',
    });

    // An emptied field has no key in the form, and removes its value.
    const cleared = applyFormData(set, selection, base);

    expect(findNetwork(cleared, network.id)).not.toHaveProperty('subnet');
    expect(findNetwork(cleared, network.id)).not.toHaveProperty('gateway');
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
  // offer the field. A document that has it keeps it.
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

  // A field emptied in the form loses its key. The element shows an unset
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

  // The working copy holds an unset one as '', which is none of the choices.
  test('an unset line style or border pattern holds no other edit back', () => {
    const { doc, sw, edge } = sampleDocument();
    const group = addNode(doc, { kind: 'group', title: 'Core' });

    const edge1 = { type: 'edge', id: edge.id };
    const group1 = { type: 'node', id: group.node.id };
    const wavy = (name) => [`${name} must be one of the listed options`];

    for (const [kind, selection, edit, messages] of [
      ['switch', { type: 'node', id: sw.id }, { name: 'CORE' }, []],
      ['edge', edge1, { label: 'uplink' }, []],
      ['group', group1, { title: 'Edge' }, []],
      ['edge', edge1, { lineStyle: 'wavy' }, wavy('Line style')],
      ['group', group1, { borderStyle: 'wavy' }, wavy('Border pattern')],
    ]) {
      const base = inspectorTarget(group.doc, selection).data;
      const { fields } = workingCopyErrors(
        createFormValidator(),
        schemaForKind(builderSchemaV1, kind),
        { ...base, ...edit },
        base,
      );

      expect(
        fields.map((field) => field.message),
        kind,
      ).toEqual(messages);
    }
  });

  test("a device's custom icon and colors have no such value", () => {
    const { doc, alpha } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'node', id: alpha.id });

    for (const path of ['icon', 'outlineColor', 'fillColor']) {
      expect(fieldDefault(target, path), path).toBeUndefined();
    }
  });

  // Default, in the Icon menu, names the icon it draws. An icon drawing
  // draws one icon or the other, and has no Default.
  test('a device without an icon draws the default icon', () => {
    const { doc, alpha } = sampleDocument();
    const device = inspectorTarget(doc, { type: 'node', id: alpha.id });

    expect(fieldDefault(device, 'iconKey')).toEqual({
      value: 'server',
      note: 'The default icon',
    });
    expect(fieldDefault({ kind: 'icon' }, 'iconKey')).toBeUndefined();
  });
});

// A change of a device's look is applied at once, as an edit named after
// the field it changes: the name is announced, and is the Undo step's.
describe('what a change of look says', () => {
  const none = lookOf({});

  test('names the icon, the outline color or the fill color', () => {
    const title = 'Device web-01';

    expect(lookChangeLabel(title, none, { ...none, iconKey: 'router' })).toBe(
      'Changed the icon of Device web-01 to Router',
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
      'Changed the icon of Device web-01 to Router',
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
    ).toBe('Changed the icon of Device a to Router');
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

// The fields that apply at once, without Apply, on each kind of element,
// and what their change says.
describe('fields that apply at once', () => {
  test('each kind applies its look at once', () => {
    expect(instantKeys('device')).toEqual([
      'iconKey',
      'icon',
      'iconSize',
      'outlineColor',
      'fillColor',
    ]);
    expect(instantKeys('switch')).toEqual([
      'color',
      'lineStyle',
      'outlineColor',
      'fillColor',
      'iconSize',
    ]);
    expect(instantKeys('edge')).toEqual(['label', 'color', 'lineStyle']);
    expect(instantKeys('line')).toEqual(['label', 'color', 'lineStyle']);
    expect(instantKeys('group')).toEqual([
      'color',
      'borderStyle',
      'iconKey',
      'icon',
      'iconSize',
    ]);
    expect(instantKeys('note')).toEqual(['color']);
    expect(instantKeys('shape')).toEqual([
      'fillColor',
      'outlineColor',
      'borderStyle',
    ]);
    expect(instantKeys('icon')).toEqual(['iconKey', 'icon']);
    // What an element is waits for Apply.
    expect(instantKeys('document')).toEqual([]);
    expect(instantKeys('device')).not.toContain('hostname');
    expect(instantKeys('switch')).not.toContain('name');
    expect(instantKeys('shape')).not.toContain('label');
  });

  test('the values are text, empty when not set', () => {
    expect(instantValues('edge', { label: 'up', lineStyle: null })).toEqual({
      label: 'up',
      color: '',
      lineStyle: '',
    });
  });

  test('a change says the field and the value', () => {
    const edge = instantValues('edge', {});
    const say = (kind, before, after) =>
      instantChangeLabel(kind, 'X', before, after);

    expect(say('edge', edge, { ...edge, color: '#2f6fbf' })).toBe(
      'Changed the color of X to #2f6fbf',
    );
    expect(say('edge', edge, { ...edge, lineStyle: 'dashed' })).toBe(
      'Changed the line style of X to Dashed',
    );
    expect(say('edge', { ...edge, lineStyle: 'dashed' }, edge)).toBe(
      'Changed the line style of X to Auto',
    );
    expect(say('line', { ...edge, lineStyle: 'dashed' }, edge)).toBe(
      'Changed the line style of X to the default',
    );
    expect(say('line', edge, { ...edge, label: 'Uplink' })).toBe(
      'Changed the label of X to "Uplink"',
    );
    expect(say('line', { ...edge, label: 'Uplink' }, edge)).toBe(
      'Removed the label of X',
    );
    expect(say('switch', {}, { color: '#2f6fbf' })).toBe(
      'Changed the edge color of X to #2f6fbf',
    );
    expect(say('switch', {}, { lineStyle: 'dotted' })).toBe(
      'Changed the line style of X to Dotted',
    );
    expect(say('switch', { lineStyle: 'dotted' }, { lineStyle: '' })).toBe(
      'Changed the line style of X to Auto',
    );
    expect(say('group', {}, { borderStyle: 'double' })).toBe(
      'Changed the border pattern of X to Double',
    );
    // The look fields are named as a device's.
    expect(say('shape', {}, { fillColor: '#222222' })).toBe(
      'Changed the fill color of X to #222222',
    );
    expect(say('group', {}, { iconSize: 'large' })).toBe(
      'Changed the icon size of X to Large',
    );
    expect(say('group', { icon: 'plc' }, { icon: '' })).toBe(
      'Removed the custom icon of X',
    );
    // A field that is no look of the kind says nothing.
    expect(say('note', {}, { label: 'a' })).toBe('');
    expect(say('edge', edge, { ...edge })).toBe('');
  });

  // One change of the form's data, one Undo step: the field's value merges
  // into the element's data as it is.
  test('a change applies to the element on its own', () => {
    const { doc, alpha } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'node', id: alpha.id });
    const next = applyFormData(
      doc,
      { type: 'node', id: alpha.id },
      { ...target.data, fillColor: '#222222' },
    );

    expect(findNode(next, alpha.id).device.fillColor).toBe('#222222');
    expect(findNode(next, alpha.id).device.hostname).toBe(
      findNode(doc, alpha.id).device.hostname,
    );
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

  test('a field keeps its most relevant error in each alternative, and the oneOf', () => {
    const error = (keyword, schemaPath, params = {}) => ({
      keyword,
      instancePath: schemaPath === '#/oneOf' ? '' : '/address',
      schemaPath,
      params,
    });
    const errors = [
      error('minLength', '#/oneOf/0/properties/address/minLength', {
        limit: 7,
      }),
      error('format', '#/oneOf/0/properties/address/format'),
      error('type', '#/oneOf/1/properties/address/type'),
      error('oneOf', '#/oneOf'),
    ];

    expect(relevantErrors(errors)).toEqual(errors.slice(1));
  });
});

// Memory and VCPUs are a number or text to phenix, edited as a number. DNS
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
// or cancelled. The entries of keys and values were never marked.
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
    // Meanwhile: Add connection point, and a connection made on the canvas.
    const added = addInterface(doc, bravo.id);
    doc = connect(added.doc, {
      sourceNodeId: bravo.id,
      sourceHandleId: added.handle.id,
      targetNodeId: sw.id,
    }).doc;
    // And a rename on the canvas.
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

// A VLAN naming a network with no switch adds one beside the device (connectByVLAN).
test('a switch that a VLAN applied from the Inspector adds follows Snap to grid', () => {
  const { doc, alpha } = sampleDocument();
  const lan = addNetwork(doc, { name: 'LAN' }).doc;
  const selection = { type: 'node', id: alpha.id };
  const data = inspectorTarget(lan, selection).data;
  const placed = (options) => {
    const next = applyFormData(lan, selection, data, options);

    return next.nodes.find(
      (node) =>
        node.kind === 'switch' &&
        !lan.nodes.some((entry) => entry.id === node.id),
    ).position;
  };

  data.spec.network.interfaces[0].vlan = 'LAN';

  const loose = placed({ snap: false });
  const snapped = placed({ snap: true });

  expect(placed()).toEqual(loose);
  expect(snapped).not.toEqual(loose);
  expect([snapped.x / 16, snapped.y / 16].every(Number.isInteger)).toBe(true);
});

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

  // A connected interface removed while another is added is no rename.
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
  // validate.js reports elements by index (nodes[0]). The Inspector names
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
      proto: 'static',
      mask: 24,
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
    ).toEqual({ name: 'eth1', proto: 'static', mask: 24 });
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

// What the Inspector does when the document changes underneath its form,
// as a layout landing from its worker does (see BuilderInspector).
describe('a document change under the Inspector', () => {
  test('keeps the working copy while the form holds anything of the user’s', () => {
    expect(keepsWorkingCopy({})).toBe(false);
    expect(keepsWorkingCopy()).toBe(false);

    for (const state of [
      { dirty: true },
      // Text typed in a field and not committed yet: dirty is still false.
      { typing: true },
      { held: true },
      { holding: true },
      { unsent: true },
      { problems: 1 },
    ]) {
      expect(keepsWorkingCopy(state), JSON.stringify(state)).toBe(true);
    }

    expect(keepsWorkingCopy({ problems: 0, dirty: false })).toBe(false);
  });

  test('moves the working copy onto the element as it is now', () => {
    const loaded = {
      hostname: 'alpha',
      iconKey: 'server',
      labels: { site: 'a' },
      spec: { general: { description: '' } },
    };
    const draft = {
      ...loaded,
      spec: { general: { description: 'Typed' } },
    };
    // Changed elsewhere meanwhile: the icon and a label.
    const current = { ...loaded, iconKey: 'router', labels: { site: 'b' } };
    const rebased = rebasedWorkingCopy(loaded, draft, current);

    expect(rebased).toEqual({
      loaded: current,
      draft: { ...current, spec: { general: { description: 'Typed' } } },
    });
    // What the working copy holds unchanged stays the same object. What comes
    // from the element is a copy that an edit of the form does not reach.
    expect(rebased.draft.spec).toBe(draft.spec);
    expect(rebased.draft.labels).not.toBe(current.labels);
    expect(rebased.loaded).not.toBe(current);
    expect(rebased.loaded.labels).not.toBe(current.labels);
    // Where both changed a field, the working copy's value wins.
    expect(
      rebasedWorkingCopy(loaded, draft, {
        ...current,
        spec: { general: { description: 'Elsewhere' } },
      }).draft.spec.general.description,
    ).toBe('Typed');
  });

  test('leaves the working copy as it is when the element did not change', () => {
    const loaded = { hostname: 'alpha', spec: { general: {} } };
    const draft = { hostname: 'alpha-2', spec: { general: {} } };

    // A layout moves nodes, which no field of the form shows.
    expect(rebasedWorkingCopy(loaded, draft, { ...loaded })).toBeNull();
    // The element is gone: the selection's watch opens another form.
    expect(rebasedWorkingCopy(loaded, draft, undefined)).toBeNull();
  });
});
