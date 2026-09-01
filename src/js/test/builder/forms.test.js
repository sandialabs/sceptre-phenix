import { describe, expect, test } from 'vitest';

import {
  applyFormData,
  formErrors,
  inspectorTarget,
  sampleForKind,
  uiSchemaForKind,
} from '@/builder/adapters/forms.js';
import { builderSchemaV1 } from '@/builder/schema.js';
import { addNode, findNetwork, findNode } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';

describe('generated UI schemas', () => {
  test('are produced from the JSON Schema, not hand written', () => {
    const ui = uiSchemaForKind(builderSchemaV1, 'device');

    expect(ui.type).toBe('VerticalLayout');
    expect(JSON.stringify(ui)).toContain('#/properties/hostname');
    expect(JSON.stringify(ui)).toContain('#/properties/spec');
  });

  test('long text fields render multi-line', () => {
    const ui = uiSchemaForKind(builderSchemaV1, 'note');
    const control = ui.elements.find((element) =>
      element.scope?.endsWith('/text'),
    );

    expect(control.options.multi).toBe(true);
  });

  test('sampling never throws for any kind', () => {
    ['device', 'switch', 'note', 'group', 'edge', 'document'].forEach(
      (kind) => {
        expect(() => sampleForKind(builderSchemaV1, kind)).not.toThrow();
      },
    );
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

  test('an edge edits only its label', () => {
    const { doc, edge } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'edge', id: edge.id });

    expect(Object.keys(target.data)).toEqual(['label']);
  });

  test('nothing selected edits the document', () => {
    const { doc } = sampleDocument();
    const target = inspectorTarget(doc, { type: 'document' });

    expect(target.data).toEqual({ name: 'Sample', description: '' });
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

  test('validation errors are surfaced with their path', () => {
    const errors = formErrors([
      { instancePath: '/hostname', message: 'must be a string' },
    ]);

    expect(errors).toEqual([
      { path: '/hostname', message: '/hostname must be a string' },
    ]);
    expect(formErrors(undefined)).toEqual([]);
  });
});
