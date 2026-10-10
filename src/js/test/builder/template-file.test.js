// Template files: what Export writes, what Import reads, and the store's
// import of a file into the user's library.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

const phenix = vi.hoisted(() => ({ username: 'alice', role: null }));

vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix }));

const api = vi.hoisted(() => ({
  listTemplates: vi.fn(),
  createTemplates: vi.fn(),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

import { createBuilderApi } from '@/builder/api.js';
import { toYAMLString } from '@/builder/exporters.js';
import { createDocument } from '@/builder/model.js';
import { LibraryError, useBuilderStore } from '@/builder/store.js';
import {
  MAX_TEMPLATE_FILE_BYTES,
  TEMPLATE_FILE_SCHEMA_URI,
  exportTemplateFile,
  importProblem,
  importedMessage,
  parseTemplateFile,
  templateFileName,
  templateFileOf,
  uniqueCollectionName,
  uniqueTemplateNames,
} from '@/builder/templateFile.js';
import {
  PRELOADED_GROUP_LABEL,
  paletteTemplateGroups,
  preloadedGroupLabel,
  showChoices,
  shownList,
  templateByKey,
  templateOrigin,
} from '@/builder/templates.js';
import { MAX_TEMPLATE_NAME_BYTES } from '@/builder/validate.js';

import { libraryOf } from './fixtures.js';
import { ICON_DATA } from './png.js';

// A template as the server lists it, of any source.
function listed(id, init = {}) {
  return {
    id,
    owner: 'alice',
    source: 'own',
    name: id.toUpperCase(),
    description: '',
    device: {
      iconKey: 'server',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: id, description: '', vm_type: 'kvm' },
        hardware: { os_type: 'linux', vcpus: 2, drives: [{ image: 'a.qc2' }] },
        network: { interfaces: [] },
      },
    },
    version: 1,
    etag: '"1"',
    serverWide: false,
    collections: [],
    ...init,
  };
}

// An icon library that holds the icons it is given, by name.
function fakeLibrary(icons = {}) {
  const held = new Map(
    Object.entries(icons).map(([name, data]) => [name.toLowerCase(), data]),
  );

  return {
    load: vi.fn(async () => {}),
    lookup: (name) =>
      held.has(String(name).toLowerCase())
        ? { name, data: held.get(String(name).toLowerCase()) }
        : null,
    upload: vi.fn(async ({ name, data }) => {
      held.set(name.toLowerCase(), data);

      return { icon: { name, data } };
    }),
    failure: (error) => `${error.message}.`,
  };
}

// The text of a template file holding the given value, as YAML.
function fileText(value) {
  return toYAMLString({
    $schema: TEMPLATE_FILE_SCHEMA_URI,
    name: 'Plant floor',
    templates: [
      {
        name: 'PLC',
        device: { spec: { general: { hostname: 'plc' } } },
      },
    ],
    ...value,
  });
}

describe('the server’s collections', () => {
  // The listing of a server with two template files.
  function listing() {
    return {
      owner: 'alice',
      templates: [listed('plc')],
      collections: [],
      preloaded: [
        {
          collection: {
            id: 'server-a',
            source: 'preloaded',
            owner: '',
            name: 'NLR Node Templates',
            templateIds: ['server-t1', 'server-t2'],
          },
          templates: [
            listed('server-t1', { owner: '', source: 'preloaded' }),
            listed('server-t2', { owner: '', source: 'preloaded' }),
          ],
        },
        {
          collection: {
            id: 'server-b',
            name: 'Sandia Node Templates',
            templateIds: ['server-t3'],
          },
          templates: [listed('server-t3')],
        },
        { templates: [listed('orphan')] },
      ],
      canShare: false,
      canPublish: false,
      damaged: false,
      limits: {},
    };
  }

  test('the API client lists them after the user’s own, as source preloaded with no owner', async () => {
    const client = createBuilderApi({
      get: async () => ({ data: listing() }),
    });
    const library = await client.listTemplates();

    expect(
      library.templates.map((item) => [item.id, item.source, item.owner]),
    ).toEqual([
      ['plc', 'own', 'alice'],
      ['server-t1', 'preloaded', ''],
      ['server-t2', 'preloaded', ''],
      ['server-t3', 'preloaded', ''],
    ]);
    expect(
      library.collections.map((item) => [item.id, item.source, item.owner]),
    ).toEqual([
      ['server-a', 'preloaded', ''],
      ['server-b', 'preloaded', ''],
    ]);
  });

  test('the palette, the Show field and the keys list them by collection', async () => {
    const client = createBuilderApi({
      get: async () => ({ data: listing() }),
    });
    const read = await client.listTemplates();
    const library = libraryOf({});

    library.items = read.templates;
    library.collections = read.collections;

    const groups = paletteTemplateGroups(createDocument(), library);

    // Each server collection's group says it is the server's, as the Node
    // Templates tab's Server source does, and each entry that it is read only.
    expect(groups.map((group) => [group.id, group.label])).toEqual([
      ['own', 'My library'],
      ['preloaded:server-a', 'Server: NLR Node Templates'],
      ['preloaded:server-b', 'Server: Sandia Node Templates'],
    ]);
    expect(groups[1].entries[0]).toMatchObject({
      key: 'preloaded:server-t1',
      source: 'preloaded',
      testid: 'palette-template-preloaded-server-t1',
      description:
        'From the server collection NLR Node Templates, which is read only.',
    });
    expect(preloadedGroupLabel('')).toBe(PRELOADED_GROUP_LABEL);
    expect(templateByKey(null, 'preloaded:server-t3', library).id).toBe(
      'server-t3',
    );
    expect(templateByKey(null, 'own:server-t3', library)).toBeUndefined();

    const choices = showChoices(library);

    expect(choices.preloadedCollections).toEqual([
      { value: 'preloaded:server-a', label: 'NLR Node Templates' },
      { value: 'preloaded:server-b', label: 'Sandia Node Templates' },
    ]);
    expect(choices.server).toBe(false);

    const shown = shownList(library, 'preloaded:server-a');

    expect(shown.source).toBe('preloaded');
    expect(shown.collection.name).toBe('NLR Node Templates');
    expect(shown.templates.map((item) => item.id)).toEqual([
      'server-t1',
      'server-t2',
    ]);
    expect(shownList(library, 'preloaded:gone')).toBeNull();
    // The user's own list holds none of them.
    expect(shownList(library, '').templates.map((item) => item.id)).toEqual([
      'plc',
    ]);
    expect(templateOrigin(shown.templates[0])).toBe(
      'Read from a template file on the phenix server',
    );
  });
});

describe('exporting templates', () => {
  test('the file holds the templates without ids, and the icons they name', () => {
    const plc = listed('plc', { description: 'Controller' });
    const hmi = listed('hmi', { owner: 'bob', source: 'shared' });
    const rtu = listed('rtu', { owner: '', source: 'preloaded' });

    plc.device.icon = 'plc-icon';
    rtu.device.icon = 'gone';

    const { file, missing, left } = templateFileOf(
      {
        name: 'Plant floor',
        description: 'Control network',
        templates: [plc, hmi, rtu],
      },
      fakeLibrary({ 'plc-icon': ICON_DATA }),
    );

    expect(file).toEqual({
      $schema: TEMPLATE_FILE_SCHEMA_URI,
      name: 'Plant floor',
      description: 'Control network',
      templates: [
        { name: 'PLC', description: 'Controller', device: plc.device },
        { name: 'HMI', device: hmi.device },
        { name: 'RTU', device: rtu.device },
      ],
      icons: { 'plc-icon': { data: ICON_DATA } },
    });
    expect(missing).toEqual(['gone']);
    expect(left).toEqual([]);
  });

  test('the YAML it saves reads back as the same file', () => {
    const plc = listed('plc', { description: 'Controller: "PLC" #1' });

    plc.device.icon = 'plc-icon';

    const saved = {};
    const result = exportTemplateFile(
      { name: 'Plant / Floor', templates: [plc, listed('hmi')] },
      {
        library: fakeLibrary({ 'plc-icon': ICON_DATA }),
        saveAs: (blob, fileName) => Object.assign(saved, { blob, fileName }),
        BlobCtor: class {
          constructor(parts, options) {
            this.text = parts.join('');
            this.type = options.type;
          }
        },
      },
    );

    expect(saved.fileName).toBe('plant-floor.templates.yaml');
    expect(saved.blob.type).toBe('text/yaml;charset=utf-8');
    expect(result.message).toBe(
      'Exported 2 templates to plant-floor.templates.yaml.',
    );

    // An icon's base64 stays on one line, past the usual width.
    expect(saved.blob.text).toContain(
      `icons:\n  plc-icon:\n    data: ${ICON_DATA}\n`,
    );

    const read = parseTemplateFile(saved.blob.text);

    expect(read).toEqual({ ok: true, file: result.file });
    expect(read.file.templates[0].device.spec.hardware.vcpus).toBe(2);
  });

  test('what the page says of icons the file does not carry', () => {
    const plc = listed('plc');

    plc.device.icon = 'gone';

    const saved = {};
    const { message } = exportTemplateFile(
      { name: 'PLC', templates: [plc] },
      {
        library: fakeLibrary(),
        saveAs: (_, fileName) => (saved.fileName = fileName),
        BlobCtor: class {},
      },
    );

    expect(message).toBe(
      "Exported 1 template to plc.templates.yaml. It does not carry the custom icon gone, which the server's icon library does not have.",
    );
  });

  test('names that differ only in case are numbered apart, so the file imports and loads', () => {
    // A library may hold such names, from different sources.
    const mine = listed('plc', { name: 'PLC' });
    const theirs = listed('plc2', {
      name: 'plc',
      owner: 'bob',
      source: 'shared',
    });
    const numbered = listed('plc3', { name: 'PLC (2)' });
    const spaced = listed('plc4', { name: ' Plc ' });

    const saved = {};
    const result = exportTemplateFile(
      { name: 'Plant', templates: [mine, theirs, numbered, spaced] },
      {
        library: fakeLibrary(),
        saveAs: (blob, fileName) => Object.assign(saved, { blob, fileName }),
        BlobCtor: class {
          constructor(parts) {
            this.text = parts.join('');
          }
        },
      },
    );

    // The first keeps its name; a later one takes the first number no
    // template of the file has.
    expect(result.file.templates.map((template) => template.name)).toEqual([
      'PLC',
      'plc (3)',
      'PLC (2)',
      'Plc (4)',
    ]);
    expect(result.message).toBe(
      'Exported 4 templates to plant.templates.yaml. The templates of a file need names that differ even ignoring case, so it holds "plc" as "plc (3)" and " Plc " as "Plc (4)".',
    );
    expect(parseTemplateFile(saved.blob.text)).toEqual({
      ok: true,
      file: result.file,
    });
  });

  test('a numbered name stays within the name limit', () => {
    const long = 'é'.repeat(MAX_TEMPLATE_NAME_BYTES / 2);
    const { templates, renamed } = uniqueTemplateNames([
      { name: long },
      { name: long.toUpperCase() },
      { name: 'Other' },
    ]);

    expect(templates[0].name).toBe(long);
    expect(templates[1].name.endsWith(' (2)')).toBe(true);
    expect(
      new TextEncoder().encode(templates[1].name).length,
    ).toBeLessThanOrEqual(MAX_TEMPLATE_NAME_BYTES);
    expect(templates[2]).toEqual({ name: 'Other' });
    expect(renamed).toEqual([
      { from: long.toUpperCase(), to: templates[1].name },
    ]);
    // Names that differ are left as they are.
    expect(uniqueTemplateNames([{ name: 'A' }, { name: 'B' }])).toEqual({
      templates: [{ name: 'A' }, { name: 'B' }],
      renamed: [],
    });
  });

  test('a template’s icon size goes into the file and reads back', () => {
    const plc = listed('plc');

    plc.device.iconSize = 'large';

    const { file } = templateFileOf({ name: 'Plant', templates: [plc] });

    expect(file.templates[0].device.iconSize).toBe('large');

    const read = parseTemplateFile(toYAMLString(file));

    expect(read.ok).toBe(true);
    expect(read.file.templates[0].device.iconSize).toBe('large');
  });

  test('a file name is safe, and never hidden', () => {
    expect(templateFileName('NLR Node Templates')).toBe(
      'nlr-node-templates.templates.yaml',
    );
    expect(templateFileName('..hidden')).toBe('hidden.templates.yaml');
    expect(templateFileName('  ')).toBe('node-templates.templates.yaml');
    expect(templateFileName('Ünïcode')).toBe('n-code.templates.yaml');
  });
});

describe('reading a template file', () => {
  test('JSON and YAML read alike', () => {
    const yaml = parseTemplateFile(fileText({ description: 'd' }));
    const json = parseTemplateFile(JSON.stringify(yaml.file));

    expect(yaml.ok).toBe(true);
    expect(json).toEqual(yaml);
    expect(yaml.file).toEqual({
      $schema: TEMPLATE_FILE_SCHEMA_URI,
      name: 'Plant floor',
      description: 'd',
      templates: [
        { name: 'PLC', device: { spec: { general: { hostname: 'plc' } } } },
      ],
    });
  });

  test('what keeps a file from being read says why', () => {
    expect(parseTemplateFile('  ')).toEqual({
      ok: false,
      error: 'The file is empty.',
      issues: [],
    });
    expect(parseTemplateFile('a: &x 1\nb: *x\n').error).toMatch(
      /^YAML aliases \(\*name\) are not supported/,
    );
    expect(parseTemplateFile('[1, 2]').error).toBe(
      'This is not a template file. file: expected an object.',
    );
    expect(parseTemplateFile(fileText({ author: 'alice' })).error).toBe(
      'This is not a template file. file: unknown field "author".',
    );
    expect(
      parseTemplateFile(
        fileText({ templates: [{ id: 'x', name: 'PLC', device: {} }] }),
      ).error,
    ).toBe('This is not a template file. templates[0]: unknown field "id".');
    expect(
      parseTemplateFile(
        fileText({
          templates: [{ name: 'PLC', device: { hostname: 'plc', spec: {} } }],
        }),
      ).error,
    ).toBe(
      'This is not a template file. templates[0].device: unknown field "hostname".',
    );
    expect(parseTemplateFile(fileText({ name: 12 })).error).toBe(
      'This is not a template file. name: expected text.',
    );

    const large = `${fileText({})}#${'x'.repeat(MAX_TEMPLATE_FILE_BYTES)}\n`;

    expect(parseTemplateFile(large).error).toBe(
      'The file is larger than the 8 MiB limit.',
    );
  });

  test('a file that is not valid lists each problem where it is', () => {
    const template = (name) => ({
      name,
      device: { spec: { general: { hostname: 'h' } } },
    });
    const read = parseTemplateFile(
      fileText({
        $schema: 'https://phenix.sandia.gov/schemas/builder/v1',
        name: '',
        templates: [
          template('Router'),
          { name: 'Bad', device: { icon: 'a b', spec: {} } },
          template('router'),
        ],
        icons: { 'plc-icon': { data: 'aGVsbG8=' } },
      }),
    );

    expect(read.ok).toBe(false);
    expect(read.error).toBe(
      'This template file cannot be imported (6 problems):',
    );
    expect(read.issues).toEqual([
      {
        path: '$schema',
        message: `template file schema must be "${TEMPLATE_FILE_SCHEMA_URI}", not "https://phenix.sandia.gov/schemas/builder/v1"`,
      },
      { path: 'name', message: 'collection name is required' },
      {
        path: 'templates[1].device.spec.general.hostname',
        message: 'template hostname is required',
      },
      {
        path: 'templates[1].device.icon',
        message:
          'icon name "a b" must be 1 to 64 letters, digits, "_", "@", "." or "-"',
      },
      {
        path: 'templates[2].name',
        message:
          'template name "router" is also the name of templates[0], ignoring case',
      },
      {
        path: 'icons',
        message: expect.stringMatching(
          /^icon "plc-icon" is not an accepted PNG/,
        ),
      },
    ]);
  });

  test('a file holds one to 200 templates', () => {
    expect(parseTemplateFile(fileText({ templates: [] })).issues).toEqual([
      {
        path: 'templates',
        message: 'a template file holds at least one template',
      },
    ]);

    const many = Array.from({ length: 201 }, (_, index) => ({
      name: `T${index}`,
      device: { spec: { general: { hostname: 'h' } } },
    }));

    expect(parseTemplateFile(fileText({ templates: many })).issues).toEqual([
      {
        path: 'templates',
        message: 'a template file holds at most 200 templates, not 201',
      },
    ]);
  });
});

describe('the collection an import makes', () => {
  test('a name one of the user’s collections has gets a number', () => {
    expect(uniqueCollectionName('Plant', [])).toBe('Plant');
    expect(uniqueCollectionName('Plant', ['plant'])).toBe('Plant (2)');
    expect(uniqueCollectionName('Plant', ['Plant', 'Plant (2)'])).toBe(
      'Plant (3)',
    );

    const long = 'é'.repeat(MAX_TEMPLATE_NAME_BYTES / 2);
    const numbered = uniqueCollectionName(long, [long]);

    expect(numbered.endsWith(' (2)')).toBe(true);
    expect(new TextEncoder().encode(numbered).length).toBeLessThanOrEqual(
      MAX_TEMPLATE_NAME_BYTES,
    );
  });

  test('a library holds so many templates and collections', () => {
    const library = libraryOf({
      templates: [{ id: 'a' }, { id: 'b' }, { id: 'c', source: 'preloaded' }],
      collections: [{ id: 'c1' }],
    });

    expect(importProblem({ ...library, limits: { templates: 3 } }, 1)).toBe('');
    expect(importProblem({ ...library, limits: { templates: 3 } }, 2)).toBe(
      'Your library can hold 3 templates, and this file would take it past that. Delete some first.',
    );
    expect(
      importProblem(
        { ...library, limits: { templates: 9, collections: 1 } },
        1,
      ),
    ).toBe(
      'Your library holds 1 collections, the most it can. Delete one first.',
    );
    expect(importedMessage('Plant', 2)).toBe(
      'Imported 2 templates as collection Plant.',
    );
  });
});

describe('importing a template file in the store', () => {
  let store;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    api.listTemplates.mockReset();
    api.createTemplates.mockReset();
    api.listTemplates.mockResolvedValue({
      owner: 'alice',
      templates: [listed('plc')],
      collections: [
        { id: 'c1', owner: 'alice', source: 'own', name: 'Plant floor' },
        {
          id: 'server-1',
          owner: '',
          source: 'preloaded',
          name: 'Server floor',
          templateIds: [],
        },
      ],
      canShare: false,
      canPublish: false,
      damaged: false,
      limits: {},
    });
    api.createTemplates.mockResolvedValue({ created: [], collection: null });
  });

  test('the templates go to the library as a new collection, and the icons first', async () => {
    const read = parseTemplateFile(
      fileText({
        description: 'From the plant',
        templates: [
          {
            name: 'PLC',
            device: {
              icon: 'new-icon',
              spec: { general: { hostname: 'plc' } },
            },
          },
          {
            name: 'HMI',
            device: { icon: 'taken', spec: { general: { hostname: 'hmi' } } },
          },
        ],
        icons: {
          'new-icon': { data: ICON_DATA },
          taken: { data: ICON_DATA },
        },
      }),
    );
    const library = fakeLibrary({ taken: 'other bytes' });

    await store.fetchTemplates();

    const imported = await store.importTemplateFile(read.file, { library });

    expect(library.upload).toHaveBeenCalledExactlyOnceWith(
      { name: 'new-icon', data: ICON_DATA },
      { refresh: false },
    );
    expect(imported).toEqual({
      collection: 'Plant floor (2)',
      templates: 2,
      warnings: [
        "The server already has an icon named taken that differs from this file's. The imported templates show the server's icon.",
      ],
    });
    expect(api.createTemplates).toHaveBeenCalledExactlyOnceWith('alice', {
      templates: read.file.templates,
      collection: { name: 'Plant floor (2)', description: 'From the plant' },
    });
  });

  test('a template’s icon size comes along into the library', async () => {
    const read = parseTemplateFile(
      fileText({
        templates: [
          {
            name: 'PLC',
            device: {
              iconSize: 'medium',
              spec: { general: { hostname: 'plc' } },
            },
          },
        ],
      }),
    );

    expect(read.ok).toBe(true);

    await store.fetchTemplates();
    await store.importTemplateFile(read.file, { library: fakeLibrary() });

    const [, request] = api.createTemplates.mock.calls[0];

    expect(request.templates[0].device.iconSize).toBe('medium');
  });

  test('an icon size the Builder does not know keeps a file from being imported', () => {
    const read = parseTemplateFile(
      fileText({
        templates: [
          {
            name: 'PLC',
            device: {
              iconSize: 'huge',
              spec: { general: { hostname: 'plc' } },
            },
          },
        ],
      }),
    );

    expect(read.ok).toBe(false);
    expect(read.issues.map((issue) => issue.path)).toEqual([
      'templates[0].device.iconSize',
    ]);
  });

  test('an icon the library refuses does not stop the import', async () => {
    const read = parseTemplateFile(
      fileText({
        name: 'Server floor',
        templates: [
          {
            name: 'PLC',
            device: {
              icon: 'new-icon',
              spec: { general: { hostname: 'plc' } },
            },
          },
        ],
        icons: { 'new-icon': { data: ICON_DATA } },
      }),
    );
    const library = fakeLibrary();

    library.upload.mockRejectedValue(new Error('the icon library is full'));
    await store.fetchTemplates();

    const imported = await store.importTemplateFile(read.file, { library });

    // A server collection's name is not one of the user's.
    expect(imported.collection).toBe('Server floor');
    expect(imported.warnings).toEqual([
      "Custom icon new-icon could not be added to the server's icon library: the icon library is full. The imported templates that name it show their built-in icon.",
    ]);
    expect(api.createTemplates).toHaveBeenCalledTimes(1);
  });

  test('a library that would be too full takes nothing', async () => {
    api.listTemplates.mockResolvedValue({
      owner: 'alice',
      templates: [listed('plc')],
      collections: [],
      limits: { templates: 1 },
    });
    await store.fetchTemplates();

    const read = parseTemplateFile(fileText({}));

    await expect(
      store.importTemplateFile(read.file, { library: fakeLibrary() }),
    ).rejects.toBeInstanceOf(LibraryError);
    expect(api.createTemplates).not.toHaveBeenCalled();
  });
});
