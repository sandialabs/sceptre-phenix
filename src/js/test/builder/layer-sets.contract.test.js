// Layer sets in the document model: layer matchers, a diagram's layer set
// options, its copy of a set, outline colors and layer set files.

import { Buffer } from 'node:buffer';

import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import { nodeColors } from '@/builder/colors.js';
import { decodeDocument, parseDocument } from '@/builder/decode.js';
import { iconRefs } from '@/builder/icons.js';
import { mergeDocuments } from '@/builder/merge.js';
import {
  LAYER_SET_FILE_SCHEMA_URI,
  MAX_LAYER_SET_FILE_BYTES,
  decodeLayerSetFile,
  downloadedMessage,
  isLayerSetFileValue,
  layerSetContent,
  layerSetFileName,
  layerSetFileOf,
  layerSetFileText,
  layerSetIconNames,
  layerSetsDownloadedMessage,
  parseLayerSetFile,
  uniqueLayerSetName,
} from '@/builder/layerSetFile.js';
import {
  MAX_LAYER_SET_ID_BYTES,
  MAX_LAYER_SET_NAME_BYTES,
  createDocument,
  effectiveLayers,
  findNode,
  isLayerSetId,
  layerOutlineColor,
  layerProblem,
  layerSetContentOf,
  layerSetDescriptionProblem,
  layerSetLink,
  layerSetNameProblem,
  layerSetOptions,
  layersProblem,
  normalizedLayer,
  normalizedLayers,
  relabelLayer,
  renumberLayer,
  sameLayerSetContent,
  setLayerSetOptions,
  setLayers,
  setNodeLayer,
  updateLayer,
  withLayerAdded,
  withLayerSetCopy,
  withLayerUpdated,
} from '@/builder/model.js';
import { layerSetIssues } from '@/builder/validate.js';

import bundle from '../../src/builder/schema/builder-v1.schema.json';
import { sampleDocument } from './fixtures.js';
import { ICON_DATA } from './png.js';

const PLANT = {
  id: 'plant',
  name: 'Plant',
  description: 'Plant levels',
  iconKey: 'plc',
  icon: '',
  matchConflict: 'highest',
  layerOutlines: true,
  version: 3,
  etag: '"3"',
  builtin: false,
  layers: [
    { id: 1, label: 'Control', iconKey: 'plc', color: '#db2777' },
    {
      id: 3,
      label: 'Operations',
      icon: 'ops-icon',
      matchers: { 'general.hostname': '^hmi-' },
    },
  ],
};

describe('the limits of layer sets', () => {
  test('are the schema bundle’s', () => {
    const { layerSet, layerSetSource } = bundle.$defs;

    expect(layerSet.properties.name.maxLength).toBe(MAX_LAYER_SET_NAME_BYTES);
    expect(layerSetSource.properties.id.maxLength).toBe(MAX_LAYER_SET_ID_BYTES);
    expect(layerSetSource.properties.id.pattern).toBe('^[A-Za-z0-9_.:-]+$');
    expect(bundle.$defs.matchConflict.enum).toEqual(['', 'lowest', 'highest']);
    expect(MAX_LAYER_SET_FILE_BYTES).toBe(4 << 20);
  });

  test('a layer set ID is 1 to 128 letters, digits, ., _, : or -', () => {
    expect(isLayerSetId('builtin:purdue')).toBe(true);
    expect(isLayerSetId('6f1c2a8e-4b7d-4e2a-9c3f-1d2e3f4a5b6c')).toBe(true);
    expect(isLayerSetId('a'.repeat(MAX_LAYER_SET_ID_BYTES))).toBe(true);
    expect(isLayerSetId('a'.repeat(MAX_LAYER_SET_ID_BYTES + 1))).toBe(false);
    expect(isLayerSetId('a b')).toBe(false);
    expect(isLayerSetId('')).toBe(false);
    expect(isLayerSetId(5)).toBe(false);
  });
});

describe('a layer’s icon, color and node matchers', () => {
  test('are kept in the order of their fields, and empty ones are left out', () => {
    expect(
      normalizedLayer({
        matchers: { 'labels.b': 'x', 'general.hostname': '^plc' },
        color: '#2f6fbf',
        icon: '',
        iconKey: 'plc',
        label: 'Control',
        id: 1,
      }),
    ).toEqual({
      id: 1,
      label: 'Control',
      iconKey: 'plc',
      color: '#2f6fbf',
      matchers: { 'general.hostname': '^plc', 'labels.b': 'x' },
    });
    expect(
      Object.keys(
        normalizedLayer({ id: 1, label: 'A', matchers: {}, color: '' }),
      ),
    ).toEqual(['id', 'label']);
  });

  test('keep the list from being the placeholders', () => {
    const doc = setLayers(createDocument(), [
      { id: 3, label: 'Layer 3', iconKey: 'server' },
      { id: 0, label: 'Layer 0' },
    ]);

    expect(doc.layers).toHaveLength(2);
    expect(updateLayer(doc, 3, { iconKey: '' }).layers).toHaveLength(2);
    // The seven placeholders with an icon are a list of the document.
    const placeholders = updateLayer(createDocument(), 6, { color: '#15803d' });

    expect(placeholders.layers).toHaveLength(7);
    expect(placeholders.layers[0]).toEqual({
      id: 6,
      label: 'Layer 6',
      color: '#15803d',
    });
    // Without the color, they are the placeholders again.
    expect(updateLayer(placeholders, 6, { color: '' }).layers).toBeUndefined();
  });

  test('are refused as the server refuses them, with a sentence', () => {
    const base = { id: 1, label: 'Control' };

    expect(layerProblem(base)).toBe('');
    expect(layerProblem({ ...base, iconKey: 'tower' })).toMatch(/built-in/);
    expect(layerProblem({ ...base, icon: 'a/b' })).toMatch(/icon name/);
    expect(layerProblem({ ...base, color: 'green' })).toMatch(/hex digits/);
    expect(layerProblem({ ...base, matchers: { 'a b': 'x' } })).toMatch(
      /joined by dots/,
    );
    expect(layerProblem({ ...base, matchers: { a: '(?=x)' } })).toMatch(
      /lookarounds/,
    );
    expect(layersProblem([{ ...base, color: 'green' }])).toMatch(/hex/);
  });

  test('change in one step each, and keep the rest of the layer', () => {
    const doc = setLayers(createDocument(), PLANT.layers);

    expect(
      withLayerUpdated(doc.layers, 1, {
        matchers: { 'general.hostname': '^plc' },
      })[1],
    ).toEqual({
      id: 1,
      label: 'Control',
      iconKey: 'plc',
      color: '#db2777',
      matchers: { 'general.hostname': '^plc' },
    });
    expect(withLayerUpdated(doc.layers, 1, { color: 'green' })).toBeNull();
    expect(withLayerUpdated(doc.layers, 1, { id: 4 })).toBeNull();
    expect(withLayerUpdated(doc.layers, 9, { color: '' })).toBeNull();
    expect(updateLayer(doc, 1, { color: '#db2777' })).toBe(doc);

    // A new label or ID keeps the icon, the color and the matchers.
    const relabeled = relabelLayer(doc, 3, 'Ops');

    expect(relabeled.layers[0]).toEqual({
      id: 3,
      label: 'Ops',
      icon: 'ops-icon',
      matchers: { 'general.hostname': '^hmi-' },
    });
    expect(renumberLayer(doc, 1, 2).layers[1]).toEqual({
      ...PLANT.layers[0],
      id: 2,
    });
  });

  test('the custom icons of the layers are icons the document uses', () => {
    const doc = setLayers(createDocument(), PLANT.layers);

    expect([...iconRefs(doc)]).toEqual(['ops-icon']);
  });
});

describe('the layer set options of a diagram', () => {
  test('are at their defaults while the diagram has none', () => {
    expect(layerSetOptions(createDocument())).toEqual({
      name: '',
      matchConflict: 'lowest',
      layerOutlines: false,
      source: null,
    });
  });

  test('keep only what is not at its default', () => {
    const doc = createDocument();
    const highest = setLayerSetOptions(doc, { matchConflict: 'highest' });

    expect(highest.layerSet).toEqual({ matchConflict: 'highest' });
    expect(setLayerSetOptions(highest, { matchConflict: 'highest' })).toBe(
      highest,
    );

    const back = setLayerSetOptions(highest, { matchConflict: 'lowest' });

    expect('layerSet' in back).toBe(false);
    expect(setLayerSetOptions(doc, { layerOutlines: false })).toBe(doc);
    expect(
      setLayerSetOptions(doc, {
        layerOutlines: true,
        source: { id: 'plant', version: 2 },
      }).layerSet,
    ).toEqual({ layerOutlines: true, source: { id: 'plant', version: 2 } });
  });

  test('refuse a value the option does not take', () => {
    const doc = createDocument();

    expect(setLayerSetOptions(doc, { matchConflict: 'first' })).toBe(doc);
    expect(setLayerSetOptions(doc, { layerOutlines: 'yes' })).toBe(doc);
    expect(setLayerSetOptions(doc, { source: { id: 'a b', version: 1 } })).toBe(
      doc,
    );
    expect(setLayerSetOptions(doc, { source: { id: 'a', version: 0 } })).toBe(
      doc,
    );
    expect(setLayerSetOptions(doc, { name: 'a\tb' })).toBe(doc);
  });

  test('a new document starts with the options it is given', () => {
    expect(
      createDocument({
        layers: PLANT.layers,
        layerSet: { name: 'Plant', layerOutlines: true },
      }).layerSet,
    ).toEqual({ name: 'Plant', layerOutlines: true });
  });

  test('round-trip through decoding and validation', () => {
    const doc = withLayerSetCopy(sampleDocument().doc, PLANT);

    expect(parseDocument(JSON.parse(JSON.stringify(doc)))).toEqual(doc);
    expect(decodeDocument({ ...doc, layerSet: null }).layerSet).toBeUndefined();
    expect(
      decodeDocument({
        ...doc,
        layerSet: { name: null, source: null },
        layers: [{ id: 1, label: 'A', color: null, matchers: { a: null } }],
      }),
    ).toMatchObject({
      layerSet: {},
      layers: [{ id: 1, label: 'A', matchers: { a: '' } }],
    });
  });

  test('a layer field that is not text is refused when decoded', () => {
    const { doc } = sampleDocument();

    expect(() =>
      decodeDocument({ ...doc, layers: [{ id: 1, label: 'A', color: 5 }] }),
    ).toThrowError(/"layers\[0\]\.color" must be text/);
    expect(() =>
      decodeDocument({ ...doc, layerSet: { source: { id: 5, version: 1 } } }),
    ).toThrowError(/"layerSet\.source\.id" must be text/);
    expect(() =>
      decodeDocument({ ...doc, layerSet: { source: { id: 'a', v: 1 } } }),
    ).toThrowError(/unknown field "v"/);
  });
});

describe('a diagram’s copy of a layer set', () => {
  test('replaces its layers and options, and links to the set’s version', () => {
    const { doc, alpha } = sampleDocument();
    const manual = setNodeLayer(doc, alpha.id, 9);
    const copy = withLayerSetCopy(manual, PLANT);

    expect(copy.layers).toEqual(normalizedLayers(PLANT.layers));
    expect(copy.layerSet).toEqual({
      name: 'Plant',
      matchConflict: 'highest',
      layerOutlines: true,
      source: { id: 'plant', version: 3 },
    });
    // A node keeps the layer chosen for it.
    expect(findNode(copy, alpha.id).device.layer).toBe(9);
    expect(withLayerSetCopy(copy, PLANT)).toBe(copy);
  });

  test('of a set of only the placeholders leaves the list out', () => {
    const doc = withLayerSetCopy(setLayers(createDocument(), PLANT.layers), {
      id: 'blank',
      name: 'Blank',
      version: 1,
      layers: effectiveLayers(createDocument()),
    });

    expect('layers' in doc).toBe(false);
    expect(doc.layerSet).toEqual({
      name: 'Blank',
      source: { id: 'blank', version: 1 },
    });
  });

  test('is not made from a set whose layers cannot be written', () => {
    const doc = createDocument();

    expect(withLayerSetCopy(doc, { ...PLANT, layers: [] })).toBe(doc);
    expect(
      withLayerSetCopy(doc, {
        ...PLANT,
        layers: [{ id: 1, label: 'A', color: 'green' }],
      }),
    ).toBe(doc);
    expect(withLayerSetCopy(doc, null)).toBe(doc);
  });

  test('without an ID and a version, is not linked', () => {
    const content = { ...PLANT };

    delete content.id;
    delete content.version;

    expect(withLayerSetCopy(createDocument(), content).layerSet).toEqual({
      name: 'Plant',
      matchConflict: 'highest',
      layerOutlines: true,
    });
  });

  test('says how it stands to the set it is linked to', () => {
    const copy = withLayerSetCopy(createDocument(), PLANT);
    const later = { ...PLANT, version: 4, name: 'Plant levels' };

    expect(layerSetLink(createDocument(), [PLANT]).status).toBe('unlinked');
    expect(layerSetLink(copy, []).status).toBe('missing');
    expect(layerSetLink(copy, [PLANT])).toEqual({
      status: 'linked',
      set: PLANT,
      source: { id: 'plant', version: 3 },
    });
    expect(layerSetLink(copy, [later]).status).toBe('update');

    const changed = updateLayer(copy, 1, { color: '' });

    expect(layerSetLink(changed, [PLANT]).status).toBe('changed');
    expect(
      layerSetLink(setLayerSetOptions(copy, { layerOutlines: false }), [PLANT])
        .status,
    ).toBe('changed');
    expect(sameLayerSetContent(copy, PLANT)).toBe(true);
  });

  test('is the content a set gets when the diagram saves to it', () => {
    const copy = withLayerSetCopy(createDocument(), PLANT);

    expect(layerSetContentOf(copy)).toEqual({
      matchConflict: 'highest',
      layerOutlines: true,
      layers: normalizedLayers(PLANT.layers),
    });
    // A diagram without layers saves its placeholders.
    expect(layerSetContentOf(createDocument()).layers).toHaveLength(7);
  });
});

describe('layer outline colors', () => {
  test('color a node at a layer with a color while the options say so', () => {
    const { doc, alpha, bravo, sw } = sampleDocument();
    let copy = withLayerSetCopy(doc, PLANT);

    copy = setNodeLayer(copy, alpha.id, 1);
    copy = setNodeLayer(copy, sw.id, 1);
    copy = setNodeLayer(copy, bravo.id, 3);

    expect(layerOutlineColor(copy, findNode(copy, alpha.id))).toBe('#db2777');
    expect(layerOutlineColor(copy, findNode(copy, sw.id))).toBe('#db2777');
    // Layer 3 has no color.
    expect(layerOutlineColor(copy, findNode(copy, bravo.id))).toBe('');

    const off = setLayerSetOptions(copy, { layerOutlines: false });

    expect(layerOutlineColor(off, findNode(off, alpha.id))).toBe('');
  });

  test('come after the node’s own outline color, and keep its contrast rules', () => {
    expect(nodeColors({ outlineColor: '#000000' }, '#db2777').outline).toBe(
      '#000000',
    );
    expect(nodeColors({}, '#db2777')).toMatchObject({
      outline: '#db2777',
      low: expect.any(Object),
    });
    expect(nodeColors({}, '#ffffff').low).toEqual({ light: true, dark: false });
    expect(nodeColors({}, 'green').outline).toBe('');
    expect(nodeColors({}).outline).toBe('');
  });
});

describe('the validation of a layer set', () => {
  test('accepts a set the library accepts', () => {
    expect(layerSetIssues(layerSetContent(PLANT))).toEqual([]);
  });

  test('names each problem with the server’s code and path', () => {
    const codes = (set, path) =>
      layerSetIssues(set, path).map((entry) => `${entry.code} ${entry.path}`);

    expect(codes({ name: ' ', layers: [] })).toEqual([
      'layerset.name.required name',
      'layerset.layers.empty layers',
    ]);
    expect(
      codes({
        name: 'a'.repeat(MAX_LAYER_SET_NAME_BYTES + 1),
        description: 'a\nb',
        iconKey: 'tower',
        icon: 'a/b',
        matchConflict: 'first',
        layers: [
          { id: 1, label: 'A', color: 'green' },
          { id: 1, label: '' },
        ],
      }),
    ).toEqual([
      'layerset.name.too-long name',
      'layerset.description.control description',
      'layerset.icon-key.unknown iconKey',
      'layerset.icon.invalid icon',
      'layerset.match-conflict.unknown matchConflict',
      'layer.color.invalid layers[0].color',
      'layer.id.duplicate layers[1].id',
      'layer.label.required layers[1].label',
    ]);
    expect(
      codes({ name: 'A', layers: [{ id: 1, label: 'A' }] }, 'set'),
    ).toEqual([]);
    expect(
      codes(
        { name: 'A', description: 'd'.repeat(1025), layers: [{ id: 1 }] },
        'set',
      ),
    ).toEqual([
      'layerset.description.too-long set.description',
      'layer.label.required set.layers[0].label',
    ]);
  });

  test('a name or a description reads as a sentence for the editor', () => {
    expect(layerSetNameProblem('Plant')).toBe('');
    expect(layerSetNameProblem('')).toBe('Type a name for the layer set.');
    expect(layerSetNameProblem('', { required: false })).toBe('');
    expect(layerSetNameProblem('a'.repeat(129))).toMatch(/at most 128 bytes/);
    expect(layerSetNameProblem('a\tb')).toMatch(/control characters/);
    expect(layerSetDescriptionProblem('Plant levels')).toBe('');
    expect(layerSetDescriptionProblem('a\nb')).toMatch(/line break/);
    expect(layerSetDescriptionProblem('a'.repeat(1025))).toMatch(/1024 bytes/);
  });
});

describe('layer set files', () => {
  const library = {
    lookup: (name) => (name === 'ops-icon' ? { data: ICON_DATA } : null),
  };

  test('hold the content of a set and the custom icons it names', () => {
    const { file, missing, left } = layerSetFileOf(
      { ...PLANT, icon: 'set-icon' },
      library,
    );

    expect(file).toEqual({
      $schema: LAYER_SET_FILE_SCHEMA_URI,
      name: 'Plant',
      description: 'Plant levels',
      iconKey: 'plc',
      icon: 'set-icon',
      matchConflict: 'highest',
      layerOutlines: true,
      layers: [
        {
          id: 3,
          label: 'Operations',
          icon: 'ops-icon',
          matchers: { 'general.hostname': '^hmi-' },
        },
        { id: 1, label: 'Control', iconKey: 'plc', color: '#db2777' },
      ],
      icons: { 'ops-icon': { data: ICON_DATA } },
    });
    expect(missing).toEqual(['set-icon']);
    expect(left).toEqual([]);
    expect(layerSetIconNames({ ...PLANT, icon: 'set-icon' })).toEqual([
      'set-icon',
      'ops-icon',
    ]);
  });

  test('download as YAML and upload again to the same set', () => {
    const { file } = layerSetFileOf(PLANT, library);
    const text = layerSetFileText(file);
    const parsed = parseLayerSetFile(text);

    expect(text).toContain(`$schema: ${LAYER_SET_FILE_SCHEMA_URI}`);
    expect(parsed).toEqual({ ok: true, file });
    expect(parseLayerSetFile(JSON.stringify(file))).toEqual({
      ok: true,
      file,
    });
    expect(isLayerSetFileValue(file)).toBe(true);
    expect(isLayerSetFileValue({ ...file, $schema: 'x' })).toBe(false);
  });

  test('are named after the set', () => {
    expect(layerSetFileName('Purdue Model')).toBe('purdue-model.layers.yaml');
    expect(layerSetFileName('..')).toBe('layer-set.layers.yaml');
    expect(downloadedMessage('a.layers.yaml', 7, { missing: ['x'] })).toBe(
      "Downloaded 7 layers to a.layers.yaml. It does not carry the custom icon x, which the server's icon library does not have.",
    );
  });

  test('Download selected says how many sets it saved, and which files lack icons', () => {
    expect(layerSetsDownloadedMessage(1)).toBe('Downloaded 1 layer set.');
    expect(layerSetsDownloadedMessage(3, ['Plant'])).toBe(
      'Downloaded 3 layer sets. Some custom icons are not in the file of Plant.',
    );
    expect(layerSetsDownloadedMessage(3, ['Plant', 'Office'])).toBe(
      'Downloaded 3 layer sets. Some custom icons are not in the files of Plant and Office.',
    );
  });

  test('refuse what is not a valid layer set file, with the server’s codes', () => {
    expect(parseLayerSetFile('').error).toBe('The file is empty.');
    expect(parseLayerSetFile('a: &x 1\nb: *x').error).toMatch(/YAML aliases/);
    expect(parseLayerSetFile('[1').error).toMatch(/neither JSON nor YAML/);
    expect(parseLayerSetFile('{"name": "A", "colour": 1}').error).toBe(
      'This is not a layer set file. file: unknown field "colour".',
    );
    expect(
      parseLayerSetFile('{"layers": [{"id": 1, "label": "A", "matchers": []}]}')
        .error,
    ).toMatch(/expected an object of patterns/);
    expect(parseLayerSetFile('{"layerOutlines": "yes"}').error).toMatch(
      /true or false/,
    );

    const refused = parseLayerSetFile(
      JSON.stringify({
        $schema: 'https://phenix.sandia.gov/schemas/builder/templates/v1',
        name: 'A',
        layers: [{ id: 1, label: 'A', matchers: { 'general.hostname': '(' } }],
      }),
    );

    expect(refused.ok).toBe(false);
    expect(refused.error).toBe(
      'You cannot upload this layer set file (2 problems):',
    );
    expect(refused.issues.map((entry) => entry.code)).toEqual([
      'layerset.file.schema-mismatch',
      'layer.matcher-pattern.invalid',
    ]);
  });

  test('decode nulls as no value', () => {
    expect(
      decodeLayerSetFile({
        $schema: LAYER_SET_FILE_SCHEMA_URI,
        name: 'A',
        description: null,
        layerOutlines: null,
        layers: [{ id: 1, label: 'A', iconKey: null, matchers: null }],
        icons: null,
      }),
    ).toEqual({
      $schema: LAYER_SET_FILE_SCHEMA_URI,
      name: 'A',
      layers: [{ id: 1, label: 'A' }],
    });
  });

  test('a new set takes a name no set of the library has', () => {
    expect(uniqueLayerSetName('Plant', ['Other'])).toBe('Plant');
    expect(uniqueLayerSetName('Plant', ['plant', 'Plant (2)'])).toBe(
      'Plant (3)',
    );
    expect(uniqueLayerSetName('', [])).toBe('Layer set');
    expect(
      uniqueLayerSetName('a'.repeat(MAX_LAYER_SET_NAME_BYTES), [
        'a'.repeat(MAX_LAYER_SET_NAME_BYTES),
      ]),
    ).toBe(`${'a'.repeat(MAX_LAYER_SET_NAME_BYTES - 4)} (2)`);
  });
});

describe('a merge of two versions', () => {
  test('takes each side’s change of the layers and of the options', () => {
    const base = withLayerSetCopy(sampleDocument().doc, PLANT);
    const mine = updateLayer(base, 1, { color: '#2f6fbf' });
    const theirs = setLayerSetOptions(base, { layerOutlines: false });
    const { doc, clashes } = mergeDocuments(base, mine, theirs);

    expect(clashes).toEqual([]);
    expect(doc.layers).toEqual(mine.layers);
    expect(doc.layerSet).toEqual(theirs.layerSet);
  });

  test('keeps a link that only one side made', () => {
    const base = sampleDocument().doc;
    const mine = withLayerSetCopy(base, PLANT);
    const { doc, clashes } = mergeDocuments(base, mine, base);

    expect(clashes).toEqual([]);
    expect(doc.layerSet).toEqual(mine.layerSet);
    expect(doc.layers).toEqual(mine.layers);
  });
});

describe('layer set files and names, for any set', () => {
  // Text YAML could take for something else, or must quote.
  const text = fc.constantFrom(
    'Plant',
    'yes',
    'no',
    'null',
    '~',
    '1e3',
    '0x1F',
    '2001-12-14',
    'x: y',
    '- a',
    '#c',
    '[a]',
    "it's",
    '"q"',
    '😀 levels',
    'İstanbul',
  );
  const layerChange = fc.record(
    {
      color: fc.constantFrom('#2f6fbf', '#DB2777'),
      iconKey: fc.constantFrom('plc', 'server'),
      matchers: fc.constantFrom(
        { 'general.hostname': '^plc-' },
        { 'labels.zone': 'a|b', 'general.hostname': '[0-9]+$' },
      ),
    },
    { requiredKeys: [] },
  );
  // A set made the way the editor makes one, with the layer helpers. A set
  // holds at least one layer.
  const layerSet = fc
    .record({
      name: text,
      description: fc.option(text, { nil: undefined }),
      matchConflict: fc.constantFrom('lowest', 'highest'),
      layerOutlines: fc.boolean(),
      layers: fc.array(fc.tuple(text, layerChange), {
        minLength: 1,
        maxLength: 5,
      }),
    })
    .map(({ layers, ...set }) => {
      let list = [];

      for (const [label, change] of layers) {
        const added = withLayerAdded(list, label);

        list = withLayerUpdated(added.layers, added.layer.id, change);
      }

      return { ...set, layers: list };
    });

  test('a file written as text uploads as the same file', () => {
    fc.assert(
      fc.property(layerSet, (set) => {
        const { file } = layerSetFileOf(set);

        expect(parseLayerSetFile(layerSetFileText(file))).toEqual({
          ok: true,
          file,
        });
      }),
      { numRuns: 200 },
    );
  });

  // Names near the limit, in letters of one to four bytes.
  const setName = fc.oneof(
    fc.string({ unit: 'grapheme', maxLength: 20 }),
    fc
      .array(fc.constantFrom('a', 'é', '😀', 'İ', ' '), {
        minLength: 30,
        maxLength: 140,
      })
      .map((letters) => letters.join('')),
  );

  test('a new name is taken by no set, and stays within the limit', () => {
    fc.assert(
      fc.property(
        setName,
        fc.array(fc.oneof(text, fc.string({ maxLength: 5 })), {
          maxLength: 6,
        }),
        fc.nat(3),
        (name, others, copies) => {
          // The name and as many numbered copies of it are taken already.
          const taken = [...others];

          for (let n = 0; n < copies; n += 1) {
            taken.push(uniqueLayerSetName(name, taken).toUpperCase());
          }
          const result = uniqueLayerSetName(name, taken);
          const used = taken.map((entry) => entry.toLowerCase());

          expect(used).not.toContain(result.toLowerCase());
          if (Buffer.byteLength(name.trim()) <= MAX_LAYER_SET_NAME_BYTES) {
            expect(Buffer.byteLength(result)).toBeLessThanOrEqual(
              MAX_LAYER_SET_NAME_BYTES,
            );
          }
        },
      ),
      { numRuns: 200 },
    );
  });
});
