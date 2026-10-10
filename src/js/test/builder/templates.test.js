// Device templates: what a template is made of, the device it makes, the
// document the template editor edits it in, the templates a diagram keeps
// (model and store), and the palette's groups.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

import { fieldDefault, inspectorTarget } from '@/builder/adapters/forms.js';
import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { parseDocument } from '@/builder/decode.js';
import {
  addNode,
  addTemplate,
  createDocument,
  findNode,
  removeTemplate,
  templateDevice,
  updateNode,
  updateTemplate,
} from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import {
  blankTemplate,
  nodeOptionsFromTemplate,
  paletteTemplateGroups,
  templateByKey,
  templateDocument,
  templateEditorHost,
  templateFromDocument,
  templateFromNode,
  templateKey,
  templateProblem,
  templatesFull,
  templateText,
} from '@/builder/templates.js';
import {
  MAX_TEMPLATES,
  templateIssues,
  validateDocument,
} from '@/builder/validate.js';
import { paletteNode } from '@/components/builder/paletteDnd.js';

import { sampleDocument } from './fixtures.js';
import { ICON_DATA } from './png.js';

const UUID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
// A copy of the custom icon named plc, as a diagram carries one.
const PLC = { data: ICON_DATA };

const errorsOf = (doc) =>
  validateDocument(doc).filter((issue) => issue.level === 'error');

const iface = (name, vlan) => ({
  name,
  type: 'ethernet',
  proto: 'manual',
  vlan,
});

// A template with a look and two interfaces: one on EXP, the network of the
// sample document, in another case; one on a network the sample lacks.
function plcTemplate(init = {}) {
  return {
    name: 'PLC',
    description: 'A controller',
    device: {
      iconKey: 'firewall',
      outlineColor: '#2f6fbf',
      fillColor: '#ffeecc',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: 'plc', description: 'On the plant floor' },
        hardware: { os_type: 'linux', drives: [{ image: 'plc.qc2' }] },
        network: { interfaces: [iface('eth0', 'exp'), iface('eth1', 'PLANT')] },
      },
    },
    ...init,
  };
}

describe('a template from a device', () => {
  function device() {
    const { doc, alpha } = sampleDocument();
    const next = updateNode(doc, alpha.id, {
      device: { outlineColor: '#2f6fbf', icon: 'plc' },
    });

    return findNode(next, alpha.id);
  }

  test('keeps the icon, the colors and the spec, and nothing of the node itself', () => {
    const node = device();
    const template = templateFromNode(node, {
      name: 'Web',
      description: 'A web server',
    });

    expect(template).toEqual({
      name: 'Web',
      description: 'A web server',
      device: {
        iconKey: 'linux',
        outlineColor: '#2f6fbf',
        icon: 'plc',
        spec: node.device.spec,
      },
    });
    // The hostname is the spec's alone, and the handles are the node's.
    expect(template.device).not.toHaveProperty('hostname');
    expect(template.device).not.toHaveProperty('interfaces');
    expect(template.device).not.toHaveProperty('includedFrom');
    expect(template.device.spec.general.hostname).toBe('alpha');
    expect(template).not.toHaveProperty('id');
    expect(templateIssues(template, 'template')).toEqual([]);
  });

  test('the spec is a copy', () => {
    const node = device();
    const template = templateFromNode(node, { name: 'Web' });

    template.device.spec.general.hostname = 'changed';
    expect(node.device.spec.general.hostname).toBe('alpha');
    expect(template).not.toHaveProperty('description');
  });

  // The VLANs of a device on the canvas say what it is connected to there.
  test('a device of the canvas loses its VLANs, one of the editor keeps them', () => {
    const node = device();
    const vlans = (template) =>
      template.device.spec.network.interfaces.map((entry) => entry.vlan);

    expect(vlans(templateFromNode(node, { name: 'Web' }))).toEqual(['EXP']);
    expect(
      vlans(templateFromNode(node, { name: 'Web', clearVLANs: true })),
    ).toEqual(['']);
    // The node keeps its own.
    expect(node.device.spec.network.interfaces[0].vlan).toBe('EXP');
  });

  test('a device from an included topology makes a template that names none', () => {
    const node = device();
    const included = {
      ...node,
      device: { ...node.device, includedFrom: 'site-b' },
    };

    expect(templateFromNode(included).device).not.toHaveProperty(
      'includedFrom',
    );
  });

  test('a field with no value is left out, whatever the field', () => {
    expect(
      templateDevice({
        hostname: 'web',
        iconKey: 'server',
        icon: '',
        outlineColor: null,
        fillColor: undefined,
        // A field devices may come to have.
        badge: 'new',
        spec: { general: { hostname: 'web' } },
        interfaces: [],
      }),
    ).toEqual({
      iconKey: 'server',
      badge: 'new',
      spec: { general: { hostname: 'web' } },
    });
  });

  test('the template a new one starts from is the plain device, with no name', () => {
    const blank = blankTemplate();

    expect(blank.name).toBe('');
    expect(blank.device).toEqual({
      iconKey: 'server',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: 'device', description: '', vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
        network: { interfaces: [] },
      },
    });
  });
});

describe('a device from a template', () => {
  test('takes the spec, the look and a handle for each interface', () => {
    const { doc } = sampleDocument();
    const template = plcTemplate();
    const options = nodeOptionsFromTemplate(template, doc);
    const { node } = addNode(doc, options);

    expect(options.kind).toBe('device');
    expect(options.look).toEqual({
      iconKey: 'firewall',
      outlineColor: '#2f6fbf',
      fillColor: '#ffeecc',
    });
    expect(node.device).toMatchObject({
      hostname: 'plc',
      iconKey: 'firewall',
      outlineColor: '#2f6fbf',
      fillColor: '#ffeecc',
    });
    expect(node.device.interfaces.map((handle) => handle.name)).toEqual([
      'eth0',
      'eth1',
    ]);
    // The spec's own entries describe them: none is added.
    expect(node.device.spec.network.interfaces).toHaveLength(2);
    // The template's own description is not the node's.
    expect(node.device.spec.general.description).toBe('On the plant floor');
    expect(JSON.stringify(node)).not.toContain('A controller');
  });

  // As a paste: the new device is connected to nothing.
  test('a VLAN that names a network of the diagram is emptied, any other kept', () => {
    const { doc } = sampleDocument();
    const template = plcTemplate();
    const { spec } = nodeOptionsFromTemplate(template, doc);

    expect(spec.network.interfaces.map((entry) => entry.vlan)).toEqual([
      '',
      'PLANT',
    ]);
    // In a diagram without that network, both are kept.
    expect(
      nodeOptionsFromTemplate(
        template,
        createDocument(),
      ).spec.network.interfaces.map((entry) => entry.vlan),
    ).toEqual(['exp', 'PLANT']);
    // The template is left as it was.
    expect(template.device.spec.network.interfaces[0].vlan).toBe('exp');
  });

  test('is named after the template’s hostname, with a number once it is taken', () => {
    let doc = createDocument();
    const names = [1, 2, 3].map(() => {
      const added = addNode(doc, nodeOptionsFromTemplate(plcTemplate(), doc));

      doc = added.doc;

      return [added.node.device.hostname, added.node.label];
    });

    expect(names).toEqual([
      ['plc', 'plc'],
      ['plc-2', 'plc-2'],
      ['plc-3', 'plc-3'],
    ]);
    expect(doc.nodes[1].device.spec.general.hostname).toBe('plc-2');
    expect(errorsOf(doc)).toEqual([]);
  });

  test('keeps no link to the template', () => {
    const template = plcTemplate();
    const doc = createDocument();
    const { node } = addNode(doc, nodeOptionsFromTemplate(template, doc));

    node.device.spec.hardware.drives[0].image = 'other.qc2';
    expect(template.device.spec.hardware.drives[0].image).toBe('plc.qc2');
    expect(JSON.stringify(node)).not.toContain('PLC');
  });

  test('an interface without a name gets no handle, and a name used twice one', () => {
    const template = plcTemplate();

    template.device.spec.network.interfaces = [
      iface('eth0', ''),
      iface('eth0', ''),
      iface('', ''),
      'not an interface',
    ];

    const doc = createDocument();
    const options = nodeOptionsFromTemplate(template, doc);

    expect(options.interfaces).toEqual([{ name: 'eth0' }]);
    expect(
      addNode(doc, options).node.device.spec.network.interfaces,
    ).toHaveLength(4);
  });

  test('a template with no spec still makes a device', () => {
    const doc = createDocument();
    const { node } = addNode(
      doc,
      nodeOptionsFromTemplate({ name: 'Empty', device: {} }, doc),
    );

    expect(node.device.hostname).toBe('node');
  });
});

describe('the template editor’s document', () => {
  test('holds the template’s one device, and gives the template back', () => {
    const template = plcTemplate();
    const doc = templateDocument(template);

    expect(doc.nodes).toHaveLength(1);
    expect(doc.nodes[0].kind).toBe('device');
    expect(doc.networks).toEqual([]);
    expect(doc.edges).toEqual([]);
    // No network, so no VLAN is emptied.
    expect(templateFromDocument(doc)).toEqual({ template });
    expect(errorsOf(doc)).toEqual([]);
  });

  test('carries the copy of the template’s custom icon its diagram carries', () => {
    const template = plcTemplate();

    template.device.icon = 'plc';

    const doc = templateDocument(template, { plc: PLC, other: PLC });

    expect(doc.icons).toEqual({ plc: PLC });
    expect(doc.icons.plc).not.toBe(PLC);
    expect(templateFromDocument(doc)).toEqual({ template });
    expect(errorsOf(doc)).toEqual([]);
  });

  // The icon library resolves the name, so the device names it all the same.
  test('a custom icon its diagram carries no copy of is named still', () => {
    const template = plcTemplate();

    template.device.icon = 'plc';

    for (const icons of [null, {}, { other: PLC }]) {
      const doc = templateDocument(template, icons);

      expect(doc).not.toHaveProperty('icons');
      expect(doc.nodes[0].device.icon).toBe('plc');
      expect(templateFromDocument(doc).template).toEqual(template);
    }
  });

  test('a template with no name or description round trips as one', () => {
    const blank = blankTemplate();
    const doc = templateDocument(blank);

    expect(doc.metadata.name).toBe('');
    expect(templateFromDocument(doc).template).toEqual(blank);
  });

  test('each built-in template round trips, but for its id', () => {
    for (const template of BUILTIN_TEMPLATES) {
      const { id: _, ...rest } = template;

      expect(templateFromDocument(templateDocument(template)).template).toEqual(
        rest,
      );
    }
  });

  // A device draws at its diagram's icon size while it names none: the
  // Icon size field's Diagram default names the size of the template's
  // diagram, and none for a template of a library, which can go into any.
  test('names the icon size of the template’s diagram, and none for a library’s', () => {
    const template = plcTemplate();
    const select = (doc) => ({ type: 'node', id: doc.nodes[0].id });
    const defaultOf = (doc) =>
      fieldDefault(
        inspectorTarget(doc, select(doc), { template: true }),
        'iconSize',
      );

    const large = templateDocument(template, null, { iconSize: 'large' });
    const small = templateDocument(template, null, { iconSize: 'small' });
    const library = templateDocument(template);

    expect(large.iconSize).toBe('large');
    expect(defaultOf(large)).toEqual({
      value: 'large',
      note: "The diagram's icon size",
    });
    expect(defaultOf(small)?.value).toBe('small');
    expect(library).not.toHaveProperty('iconSize');
    expect(defaultOf(library)).toBeUndefined();
    expect(
      templateDocument(template, null, { iconSize: 'huge' }),
    ).not.toHaveProperty('iconSize');

    // The diagram's own document names its size, Small by default.
    expect(
      fieldDefault(inspectorTarget(library, select(library)), 'iconSize')
        ?.value,
    ).toBe('small');

    // The template read back is the same, whatever the editor's document
    // names.
    expect(templateFromDocument(large)).toEqual({ template });
  });
});

describe('the template editor’s host', () => {
  function hosted({ doc = templateDocument(plcTemplate()), library } = {}) {
    const source = {
      schema: { $defs: {} },
      schemaError: '',
      disks: ['a.qc2'],
    };
    const said = [];
    const host = templateEditorHost({
      doc,
      source,
      announce: (message) => said.push(message),
      library,
    });

    return { host, source, said };
  }

  test('selects the template’s device, and reads the store’s schema and images', () => {
    const { host, source } = hosted();

    expect(host.inspectorSelection).toEqual({
      type: 'node',
      id: host.doc.nodes[0].id,
    });
    expect(host.schema).toBe(source.schema);
    expect(host.disks).toEqual(['a.qc2']);
    expect(host.readOnly).toBe(false);
    expect(host.issues).toEqual([]);
    expect(host.canRedo).toBe(false);

    // They follow the store.
    source.disks = null;
    source.schemaError = 'bundled';
    expect(host.disks).toBeNull();
    expect(host.schemaError).toBe('bundled');
  });

  test('a commit replaces the document, which names the icon and carries no copy of it', () => {
    const { host } = hosted();
    const id = host.doc.nodes[0].id;
    const next = updateNode(host.doc, id, { device: { icon: 'plc' } });

    expect(host.commit(next, 'Changed the custom icon')).toBe(true);
    expect(host.doc).toBe(next);
    expect(host.doc).not.toHaveProperty('icons');
    expect(templateFromDocument(host.doc).template.device.icon).toBe('plc');
  });

  test('a commit drops a copy the device stops naming, or the library holds as it is', () => {
    const template = plcTemplate();

    template.device.icon = 'plc';

    const doc = templateDocument(template, { plc: PLC });
    const id = doc.nodes[0].id;
    const moved = (host) =>
      updateNode(host.doc, id, { position: { x: 4, y: 4 } });

    // Kept while the device names it and the library lacks it.
    const kept = hosted({ doc, library: { lookup: () => undefined } });

    kept.host.commit(moved(kept.host));
    expect(kept.host.doc.icons).toEqual({ plc: PLC });
    kept.host.commit(updateNode(kept.host.doc, id, { device: { icon: '' } }));
    expect(kept.host.doc).not.toHaveProperty('icons');

    const held = hosted({
      doc,
      library: { lookup: (name) => (name === 'plc' ? PLC : undefined) },
    });

    held.host.commit(moved(held.host));
    expect(held.host.doc).not.toHaveProperty('icons');
  });

  test('says what the Inspector says, and does nothing of the canvas', () => {
    const { host, said } = hosted();
    const doc = host.doc;

    host.announce('Warning for Image: not on the server');
    expect(said).toEqual(['Warning for Image: not on the server']);
    expect(host.addInterface(doc.nodes[0].id, {})).toBeNull();
    host.removeInterface(doc.nodes[0].id, 'x');
    host.remove({ nodes: [doc.nodes[0].id], edges: [] });
    expect(host.moveNodes([])).toBe(false);
    expect(host.doc).toBe(doc);
  });
});

describe('the templates of a diagram', () => {
  test('a saved template gets an id of its own, and the diagram stays valid', () => {
    const { doc } = sampleDocument();
    const added = addTemplate(doc, { ...plcTemplate(), id: 'server' });

    expect(added.template.id).toMatch(UUID);
    expect(added.doc.templates).toEqual([added.template]);
    expect(added.template).toEqual({ id: added.template.id, ...plcTemplate() });
    // The document given is not changed.
    expect(doc).not.toHaveProperty('templates');
    expect(errorsOf(added.doc)).toEqual([]);

    // The same template again is another one.
    const again = addTemplate(added.doc, added.template);

    expect(again.template.id).not.toBe(added.template.id);
    expect(again.doc.templates).toHaveLength(2);
    expect(errorsOf(again.doc)).toEqual([]);
  });

  test('a template is kept as a copy, without an empty description or look', () => {
    const template = plcTemplate({ description: '' });

    template.device.icon = '';

    const { doc, template: kept } = addTemplate(createDocument(), template);

    expect(kept).not.toHaveProperty('description');
    expect(kept.device).not.toHaveProperty('icon');

    template.device.spec.general.hostname = 'changed';
    expect(doc.templates[0].device.spec.general.hostname).toBe('plc');
  });

  test('an update changes what the patch names, and nothing else', () => {
    const { doc, template } = addTemplate(createDocument(), plcTemplate());
    const second = addTemplate(doc, plcTemplate({ name: 'Other' }));
    const renamed = updateTemplate(second.doc, template.id, { name: 'RTU' });

    expect(renamed.templates[0]).toEqual({ ...template, name: 'RTU' });
    expect(renamed.templates[1]).toBe(second.doc.templates[1]);

    const emptied = updateTemplate(renamed, template.id, { description: '' });

    expect(emptied.templates[0]).not.toHaveProperty('description');
    expect(emptied.templates[0].name).toBe('RTU');

    const device = { iconKey: 'router', spec: { general: { hostname: 'r' } } };
    const replaced = updateTemplate(emptied, template.id, { device });

    expect(replaced.templates[0]).toEqual({
      id: template.id,
      name: 'RTU',
      device,
    });
    expect(errorsOf(replaced)).toEqual([]);
    // No such template: the same document.
    expect(updateTemplate(replaced, 'none', { name: 'x' })).toBe(replaced);
  });

  test('removing the last template leaves a diagram that never had one', () => {
    const empty = createDocument();
    const first = addTemplate(empty, plcTemplate());
    const second = addTemplate(first.doc, plcTemplate({ name: 'Other' }));
    const one = removeTemplate(second.doc, first.template.id);

    expect(one.templates).toEqual([second.template]);
    expect(removeTemplate(one, 'none')).toBe(one);

    const none = removeTemplate(one, second.template.id);

    expect(none).not.toHaveProperty('templates');
    expect(none).toEqual(empty);
  });

  test('a diagram with templates is read back as it was written', () => {
    const { doc } = addTemplate(sampleDocument().doc, plcTemplate());

    expect(parseDocument(JSON.parse(JSON.stringify(doc)))).toEqual(doc);
  });
});

describe('templates in the store', () => {
  let store;
  let announced;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    store.setDocument(sampleDocument().doc);
    announced = [];
    store.$onAction(({ name, args }) => {
      if (name === 'announce') {
        announced.push(args[0]);
      }
    });
  });

  test('adding, changing and deleting a template are one edit each', () => {
    const added = store.addTemplate(plcTemplate());

    expect(added.id).toMatch(UUID);
    expect(store.doc.templates).toEqual([added]);
    expect(store.history.undoLabel()).toBe('Added template PLC');

    const updated = store.updateTemplate(added.id, { name: 'RTU' });

    expect(updated.name).toBe('RTU');
    expect(store.history.undoLabel()).toBe('Updated template RTU');
    expect(store.removeTemplate(added.id)).toBe(true);
    expect(store.doc).not.toHaveProperty('templates');
    expect(announced).toEqual([
      'Added template PLC',
      'Updated template RTU',
      'Deleted template RTU',
    ]);

    // Each is undone on its own.
    store.undo();
    expect(store.doc.templates[0].name).toBe('RTU');
    store.undo();
    expect(store.doc.templates[0].name).toBe('PLC');
    store.undo();
    expect(store.doc).not.toHaveProperty('templates');
    expect(store.canUndo).toBe(false);
  });

  test('a template that is not there changes nothing', () => {
    expect(store.updateTemplate('none', { name: 'x' })).toBeNull();
    expect(store.removeTemplate('none')).toBe(false);
    expect(store.canUndo).toBe(false);
    expect(announced).toEqual([]);
  });

  test('a diagram takes 50 templates, and says so at the 51st', () => {
    store.setDocument({
      ...store.doc,
      templates: Array.from({ length: MAX_TEMPLATES - 1 }, (_, index) => ({
        ...plcTemplate({ name: `T${index}` }),
        id: `00000000-0000-4000-9000-${String(index).padStart(12, '0')}`,
      })),
    });

    expect(templatesFull(store.doc)).toBe('');
    expect(store.addTemplate(plcTemplate())).not.toBeNull();
    expect(store.doc.templates).toHaveLength(MAX_TEMPLATES);
    expect(errorsOf(store.doc)).toEqual([]);
    expect(templatesFull(store.doc)).toBe(
      'This diagram has 50 templates, the most it can hold.',
    );

    expect(store.addTemplate(plcTemplate({ name: 'One more' }))).toBeNull();
    expect(store.doc.templates).toHaveLength(MAX_TEMPLATES);
    expect(announced.at(-1)).toBe(
      'Not saved. This diagram has 50 templates, the most it can hold.',
    );
  });

  test('a read-only draft takes no template', () => {
    store.readOnly = true;

    expect(store.addTemplate(plcTemplate())).toBeNull();
    expect(store.doc).not.toHaveProperty('templates');
  });

  // A template names its custom icon, which the icon library resolves: the
  // diagram carries no copy for it.
  test('a template’s custom icon is a name, and a device made from it names it too', () => {
    const template = plcTemplate();

    template.device.icon = 'plc';

    const added = store.addTemplate(template);

    expect(added.device.icon).toBe('plc');
    expect(store.doc).not.toHaveProperty('icons');
    expect(errorsOf(store.doc)).toEqual([]);

    const node = store.addNode(
      paletteNode(store, 'device', `diagram:${added.id}`),
    );

    expect(findNode(store.doc, node.id).device.icon).toBe('plc');
    expect(store.doc).not.toHaveProperty('icons');
  });

  // A copy the diagram carries, as an uploaded diagram may, stays while a
  // template or a device names it.
  test('a copy of a template’s custom icon leaves with its last use', () => {
    const template = plcTemplate();

    template.device.icon = 'plc';

    const added = store.addTemplate(template);
    const node = store.addNode(
      paletteNode(store, 'device', `diagram:${added.id}`),
    );

    store.setDocument({ ...store.doc, icons: { plc: PLC } });

    store.removeTemplate(added.id);
    expect(store.doc.icons).toEqual({ plc: PLC });

    store.remove({ nodes: [node.id], edges: [] });
    expect(store.doc).not.toHaveProperty('icons');
    expect(errorsOf(store.doc)).toEqual([]);
  });

  test('an update can give a template another icon', () => {
    const added = store.addTemplate(plcTemplate());
    const device = { ...added.device, icon: 'plc' };

    store.updateTemplate(added.id, { device });

    expect(store.doc.templates[0].device.icon).toBe('plc');
    expect(store.doc).not.toHaveProperty('icons');
  });

  test('the palette lists the diagram’s templates, then the built-in ones', () => {
    expect(store.paletteTemplateGroups.map((group) => group.id)).toEqual([
      'builtin',
    ]);

    const added = store.addTemplate(plcTemplate());

    expect(store.paletteTemplateGroups.map((group) => group.label)).toEqual([
      'This diagram',
      'Built-in',
    ]);
    expect(store.templateByKey(`diagram:${added.id}`)).toEqual(added);
    expect(store.templateByKey('builtin:router')).toBe(BUILTIN_TEMPLATES[2]);
  });

  test('a palette entry adds the device of its template, or a plain one', () => {
    const added = store.addTemplate(plcTemplate());
    const fromDiagram = paletteNode(store, 'device', `diagram:${added.id}`);

    expect(fromDiagram.spec.hardware.drives[0].image).toBe('plc.qc2');
    // EXP is a network of the sample diagram.
    expect(fromDiagram.spec.network.interfaces[0].vlan).toBe('');
    expect(paletteNode(store, 'device', 'builtin:firewall').hostname).toBe(
      'firewall',
    );
    // A key that names no template, as a template deleted during a drag.
    expect(paletteNode(store, 'device', 'diagram:gone')).toEqual({
      kind: 'device',
    });
    expect(paletteNode(store, 'device', '')).toEqual({ kind: 'device' });
    expect(paletteNode(store, 'switch')).toEqual({ kind: 'switch' });
  });
});

describe('the palette’s groups', () => {
  test('with no template in the diagram, only the built-in ones', () => {
    const groups = paletteTemplateGroups(createDocument());

    expect(groups).toHaveLength(1);
    expect(groups[0]).toMatchObject({ id: 'builtin', label: 'Built-in' });
    expect(groups[0].entries.map((entry) => entry.testid)).toEqual([
      'palette-template-server',
      'palette-template-workstation',
      'palette-template-router',
      'palette-template-firewall',
      'palette-template-external',
    ]);
    expect(groups[0].entries[0]).toEqual({
      key: 'builtin:server',
      source: 'builtin',
      id: 'server',
      testid: 'palette-template-server',
      name: 'Server',
      description: 'Generic Linux server',
      iconKey: 'server',
      icon: '',
      image: 'ubuntu.qc2',
      template: BUILTIN_TEMPLATES[0],
    });
    // The external device has no drive.
    expect(groups[0].entries[4].image).toBe('');
  });

  test('the diagram’s templates come first, each once, under their own ids', () => {
    let doc = createDocument();
    const template = plcTemplate();

    template.device.icon = 'plc';
    doc = addTemplate(doc, template).doc;
    doc = addTemplate(
      doc,
      plcTemplate({ name: 'Server', description: '' }),
    ).doc;

    const [diagram, builtin] = paletteTemplateGroups(doc);
    const [first, second] = doc.templates;

    expect(diagram).toMatchObject({ id: 'diagram', label: 'This diagram' });
    expect(diagram.entries).toEqual([
      {
        key: `diagram:${first.id}`,
        source: 'diagram',
        id: first.id,
        testid: `palette-template-diagram-${first.id}`,
        name: 'PLC',
        description: 'A controller',
        iconKey: 'firewall',
        icon: 'plc',
        image: 'plc.qc2',
        template: first,
      },
      expect.objectContaining({
        key: `diagram:${second.id}`,
        name: 'Server',
        description: '',
      }),
    ]);
    // A diagram template named as a built-in one hides neither.
    expect(builtin.entries).toHaveLength(5);

    const keys = [...diagram.entries, ...builtin.entries].map(
      (entry) => entry.key,
    );

    expect(new Set(keys).size).toBe(keys.length);
  });

  test('a key names its template, and anything else none', () => {
    const { doc, template } = addTemplate(createDocument(), plcTemplate());

    expect(templateKey('diagram', template.id)).toBe(`diagram:${template.id}`);
    expect(templateByKey(doc, `diagram:${template.id}`)).toBe(template);
    expect(templateByKey(doc, 'builtin:external')).toBe(BUILTIN_TEMPLATES[4]);

    for (const key of [
      '',
      null,
      undefined,
      'router',
      template.id,
      `builtin:${template.id}`,
      'diagram:router',
      'own:router',
      'diagram:',
    ]) {
      expect(templateByKey(doc, key), String(key)).toBeUndefined();
    }

    expect(templateByKey(createDocument(), `diagram:${template.id}`)).toBe(
      undefined,
    );
  });

  test('a diagram whose templates are not a list has none', () => {
    expect(
      paletteTemplateGroups({ templates: 'no' }).map((group) => group.id),
    ).toEqual(['builtin']);
    expect(
      paletteTemplateGroups({ templates: [null, 7] }).map((group) => group.id),
    ).toEqual(['builtin']);
    expect(paletteTemplateGroups(undefined)).toHaveLength(1);
  });
});

describe('what the template editor says before it saves', () => {
  test('a template that can be saved has no problem', () => {
    expect(templateProblem(plcTemplate())).toBeNull();
    expect(templateProblem(blankTemplate())).toEqual({
      field: 'name',
      message: 'Enter a name.',
    });
  });

  test.each([
    [{ name: '   ' }, 'name', 'Enter a name.'],
    [
      { name: 'n'.repeat(129) },
      'name',
      'Use a shorter name (128 bytes at most).',
    ],
    // Bytes, not characters.
    [
      { name: 'é'.repeat(65) },
      'name',
      'Use a shorter name (128 bytes at most).',
    ],
    [
      { description: 'd'.repeat(1025) },
      'description',
      'Use a shorter description (1024 bytes at most).',
    ],
  ])('%j', (init, field, message) => {
    expect(templateProblem(plcTemplate(init))).toEqual({ field, message });
  });

  test('the limits themselves are allowed', () => {
    expect(
      templateProblem(
        plcTemplate({ name: 'n'.repeat(128), description: 'd'.repeat(1024) }),
      ),
    ).toBeNull();
  });

  test('a device of more than 16 KiB is too large', () => {
    const template = plcTemplate();

    template.device.spec.general.description = 'x'.repeat(16 * 1024);

    expect(templateProblem(template)).toEqual({
      field: 'device',
      message:
        'This template is too large to save (16 KiB at most). Remove some of its settings.',
    });
  });

  test('anything else the checks find is said in their words', () => {
    const template = plcTemplate();

    template.device.spec.general.hostname = 'two words';

    expect(templateProblem(template)).toEqual({
      field: 'device',
      message:
        'This template cannot be saved: template hostname "two words" must not contain whitespace.',
    });
  });

  test('a name and a description are saved on one line, trimmed', () => {
    expect(templateText('  Edge\trouter\r\n')).toBe('Edge router');
    expect(templateText('a\u0000\u0001b\u007fc')).toBe('a b c');
    expect(templateText('\n\t')).toBe('');
    expect(templateText(undefined)).toBe('');
    expect(templateText('Plain name')).toBe('Plain name');
    expect(
      templateIssues({ ...plcTemplate(), name: 'a\tb' }, 't'),
    ).toHaveLength(1);
    expect(
      templateProblem(plcTemplate({ name: templateText('a\tb') })),
    ).toBeNull();
  });
});
