// A diagram's template copies and the library: the update offered for a
// later version, and what the Node Templates tab lists.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const api = vi.hoisted(() => ({
  listTemplates: vi.fn(),
  createTemplates: vi.fn(),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

import { BUILTIN_TEMPLATES } from '@/builder/catalog.js';
import { parseImport } from '@/builder/decode.js';
import { isDigest } from '@/builder/digest.js';
import { toYAMLString } from '@/builder/exporters.js';
import { mergeDocuments } from '@/builder/merge.js';
import {
  addTemplate,
  createDocument,
  updateTemplate,
} from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import {
  SHOW_BUILTIN,
  collectionNames,
  copiedMessage,
  copyProblem,
  linkedCopy,
  linkedCopyOf,
  nodeOptionsFromTemplate,
  showChoices,
  shownList,
  templateContent,
  templateCopyDigest,
  templateLink,
  templateLinkMark,
  templateLinkText,
  templateLinks,
  templateOrigin,
  templateSource,
  templateUnlinkedLabel,
  templateUpdateQuestion,
  templatesUpdatedLabel,
  updatedCopy,
  withLinkedCopy,
} from '@/builder/templates.js';
import { MAX_TEMPLATES, validateDocument } from '@/builder/validate.js';
import { paletteNode } from '@/components/builder/paletteDnd.js';

import { libraryOf, sampleDocument } from './fixtures.js';

const LIBRARY_ID = '6f1c2a54-3b7e-4c1d-9a2f-0e8b7d6c5a41';

// A template as the server lists it.
function listed(id, init = {}) {
  return {
    id,
    source: 'library',
    name: id.toUpperCase(),
    description: '',
    device: {
      iconKey: 'server',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: id, description: '', vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: `${id}.qc2` }] },
        network: { interfaces: [] },
      },
    },
    version: 1,
    etag: '"1"',
    collections: [],
    ...init,
  };
}

// The PLC template of the library at a version, with its device changed
// from the first version on.
function plc(version = 1) {
  const template = listed(LIBRARY_ID, { name: 'PLC', version });

  template.device.spec.general.hostname = 'plc';
  template.device.spec.hardware.drives[0].image = `plc-v${version}.qc2`;

  return template;
}

// A document with a copy of `template` (see linkedCopy).
function withCopy(template, doc = createDocument()) {
  return addTemplate(doc, linkedCopy(template));
}

describe('a copy of a library template', () => {
  test('names the template, the version copied and its own digest', () => {
    const copy = linkedCopy(plc(3));

    expect(copy).toEqual({
      ...templateContent(plc(3)),
      source: {
        kind: 'library',
        id: LIBRARY_ID,
        version: 3,
        digest: templateCopyDigest(copy),
      },
    });
    expect(isDigest(copy.source.digest)).toBe(true);
    // What a library lists beside a template is not copied.
    expect(copy).not.toHaveProperty('etag');
    expect(copy).not.toHaveProperty('id');
    expect(templateSource(plc(3)).digest).toBe(copy.source.digest);
  });

  test('the digest follows the name, the description and the device only', () => {
    const { template } = withCopy(plc());
    const digest = templateCopyDigest(template);

    expect(templateCopyDigest({ ...template, id: 'other' })).toBe(digest);
    expect(templateCopyDigest({ ...template, source: null })).toBe(digest);
    // A value the document leaves out is no change.
    expect(
      templateCopyDigest({
        ...template,
        description: '',
        device: { ...template.device, icon: '' },
      }),
    ).toBe(digest);
    expect(templateCopyDigest({ ...template, name: 'PLC 2' })).not.toBe(digest);
    expect(
      templateCopyDigest({ ...template, description: 'Controller' }),
    ).not.toBe(digest);
  });

  test('a document keeps it, and its checks pass', () => {
    const { doc, template } = withCopy(plc(2));

    expect(template.source).toMatchObject({ kind: 'library', version: 2 });
    expect(validateDocument(doc)).toEqual([]);
    expect(linkedCopyOf(doc, plc())).toBe(template);
    expect(linkedCopyOf(doc, { ...plc(), source: 'preloaded' })).toBeFalsy();
  });

  test('a download, an upload and YAML keep it', () => {
    const { doc, template } = withCopy(plc(2));
    const named = { ...doc, metadata: { ...doc.metadata, name: 'Lab' } };

    for (const text of [JSON.stringify(named), toYAMLString(named)]) {
      const read = parseImport(text);

      expect(read.ok, text.slice(0, 20)).toBe(true);
      expect(read.document.templates[0].source).toEqual(template.source);
    }
  });

  test('an edit of the copy keeps it, and an empty source removes it', () => {
    const { doc, template } = withCopy(plc());
    const edited = updateTemplate(doc, template.id, { name: 'PLC 2' });

    expect(edited.templates[0].source).toEqual(template.source);
    expect(
      updateTemplate(edited, template.id, { source: null }).templates[0],
    ).not.toHaveProperty('source');
  });

  test('a merge takes its source as one value', () => {
    const { doc: base, template } = withCopy(plc(1));
    const update = (version) =>
      updateTemplate(base, template.id, updatedCopy(plc(version)));
    const merged = mergeDocuments(base, update(2), update(3));

    expect(merged.clashes.map((clash) => clash.key)).toContain(
      `templates["${template.id}"].source`,
    );
    expect(
      merged.clashes.find((clash) => clash.key.endsWith('.source')).label,
    ).toBe('template PLC library link');

    // One side's update, the other side's edit of the name.
    const renamed = updateTemplate(base, template.id, { name: 'Pump PLC' });
    const both = mergeDocuments(base, update(2), renamed);

    expect(both.clashes).toEqual([]);
    expect(both.doc.templates[0].source).toEqual(update(2).templates[0].source);
  });
});

describe('how a copy stands against the library', () => {
  const library = (...templates) => libraryOf({ templates });

  test('current, with a later version, or deleted', () => {
    const { template } = withCopy(plc(2));

    expect(templateLink(template, library(plc(2)))).toEqual({
      state: 'current',
      kind: 'library',
      id: LIBRARY_ID,
      version: 2,
      edited: false,
      latest: plc(2),
    });
    expect(templateLink(template, library(plc(4)))).toMatchObject({
      state: 'update',
      latest: plc(4),
    });
    // An earlier version in the library is no update.
    expect(templateLink(template, library(plc(1))).state).toBe('current');
    expect(templateLink(template, library(listed('other')))).toMatchObject({
      state: 'deleted',
      latest: null,
    });
  });

  test('not known before the library is read, or when it cannot be read', () => {
    const { template } = withCopy(plc());

    expect(templateLink(template, null).state).toBe('unknown');
    expect(
      templateLink(template, { ...library(plc()), loaded: false }).state,
    ).toBe('unknown');
    expect(templateLink(template, libraryOf({ damaged: true })).state).toBe(
      'unknown',
    );
  });

  test('a copy changed in the diagram is edited', () => {
    const { doc, template } = withCopy(plc());
    const changed = updateTemplate(doc, template.id, { name: 'PLC 2' })
      .templates[0];

    expect(templateLink(changed, library(plc(2)))).toMatchObject({
      state: 'update',
      edited: true,
    });
    // Without a digest, nothing says it changed.
    expect(
      templateLink(
        { ...changed, source: { ...changed.source, digest: undefined } },
        library(plc(2)),
      ).edited,
    ).toBe(false);
  });

  test('a template of a template file, or a built-in one', () => {
    const p1 = listed('server-p1', { source: 'preloaded' });
    const { template: preloaded } = withCopy(p1);
    const { template: builtin } = withCopy({
      ...BUILTIN_TEMPLATES[2],
      source: 'builtin',
      version: 1,
    });

    expect(templateLink(preloaded, library(p1)).state).toBe('current');
    expect(templateLink(preloaded, library()).state).toBe('deleted');
    // A template file that changed keeps the ID and version 1 of the
    // template, so its content tells.
    const p1Changed = listed('server-p1', {
      source: 'preloaded',
      description: 'Changed in the file',
    });
    const changedLink = templateLink(preloaded, library(p1Changed));

    expect(changedLink).toMatchObject({ state: 'update', latest: p1Changed });
    expect(templateLinkText(changedLink)).toBe(
      'The server has a newer version of this template.',
    );
    expect(
      templateUpdateQuestion([{ ...changedLink, template: preloaded }]).message,
    ).toContain(
      'The template in this diagram gets the newest version from the server.',
    );
    // An update copies the changed content, and the copy is current again.
    const updated = {
      ...preloaded,
      ...updatedCopy(p1Changed),
    };

    expect(templateLink(updated, library(p1Changed)).state).toBe('current');
    // Without a digest, nothing tells that the file changed.
    expect(
      templateLink(
        { ...preloaded, source: { ...preloaded.source, digest: undefined } },
        library(p1Changed),
      ).state,
    ).toBe('current');
    // A damaged library still lists the server's templates.
    expect(
      templateLink(preloaded, { ...library(p1), damaged: true }).state,
    ).toBe('current');
    // The built-in templates are known before the library is read.
    expect(templateLink(builtin, null).state).toBe('current');
    expect(
      templateLink(
        { ...builtin, source: { ...builtin.source, id: 'gone' } },
        null,
      ).state,
    ).toBe('deleted');
  });

  test('a template that is no copy has no link', () => {
    const { template } = addTemplate(createDocument(), plc());

    expect(templateLink(template, library(plc()))).toBeNull();
    expect(
      templateLink({ ...template, source: { kind: 'own' } }, library()),
    ).toBeNull();
  });

  test('the diagram’s copies, in its order', () => {
    let doc = withCopy(plc(1)).doc;

    doc = addTemplate(doc, templateContent(listed('free'))).doc;
    doc = withCopy(listed('gone'), doc).doc;

    expect(
      templateLinks(doc, library(plc(2))).map((link) => [
        link.template.name,
        link.state,
      ]),
    ).toEqual([
      ['PLC', 'update'],
      ['GONE', 'deleted'],
    ]);
    expect(templateLinks({ templates: 'no' }, library())).toEqual([]);
  });

  test('what the editor says of a copy', () => {
    const { template } = withCopy(plc(2));
    const link = (latest, kind = 'library') =>
      templateLink(
        { ...template, source: { ...template.source, kind } },
        library(...latest),
      );

    expect(templateLinkText(link([plc(5)]))).toBe(
      'The library has a newer version of this template (version 5). The diagram has version 2.',
    );
    expect(templateLinkText(link([]))).toBe(
      'Someone deleted the library template.',
    );
    expect(templateLinkText(link([], 'preloaded'))).toBe(
      'The server template is no longer on the server.',
    );
    expect(templateLinkText(link([plc(2)]))).toBe('');
    expect(templateLinkText(null)).toBe('');
    expect(templateLinkMark(link([plc(5)]))).toBe('Update available');
    expect(templateLinkMark(link([]))).toBe('Deleted from library');
    expect(templateLinkMark(link([], 'preloaded'))).toBe('Removed from server');
    expect(templateLinkMark(link([plc(2)]))).toBe('');
    expect(templateLinkMark(null)).toBe('');
  });

  test('what an update asks, and says', () => {
    const { doc, template } = withCopy(plc(1));
    const edited = updateTemplate(doc, template.id, { name: 'PLC 2' })
      .templates[0];
    const latest = library(plc(3));

    expect(
      templateUpdateQuestion([{ ...templateLink(template, latest), template }]),
    ).toEqual({
      title: 'Update template PLC?',
      message:
        'The template in this diagram gets version 3 from the library. Devices made from the template do not change. You can undo this.',
      confirmLabel: 'Update template',
    });
    expect(
      templateUpdateQuestion([
        { ...templateLink(edited, latest), template: edited },
      ]).message,
    ).toBe(
      'The template in this diagram gets version 3 from the library. Devices made from the template do not change. This template has changes in the diagram. The update replaces these changes. You can undo this.',
    );

    const several = (links) => templateUpdateQuestion(links);
    const plain = { ...templateLink(template, latest), template };
    const changed = { ...templateLink(edited, latest), template: edited };

    expect(several([plain, plain])).toEqual({
      title: 'Update 2 templates?',
      message:
        'The templates in this diagram get their newest versions from the library. Devices made from the templates do not change. You can undo this.',
      confirmLabel: 'Update templates',
    });
    expect(several([plain, changed]).message).toContain(
      '1 of these templates has changes in the diagram. The update replaces these changes.',
    );
    expect(several([changed, changed]).message).toContain(
      '2 of these templates have changes in the diagram.',
    );
    expect(templatesUpdatedLabel([template])).toBe(
      'Updated template PLC from the library',
    );
    expect(templatesUpdatedLabel([template, edited])).toBe(
      'Updated 2 templates from the library',
    );
    expect(templateUnlinkedLabel(template)).toBe(
      'Unlinked template PLC from the library',
    );
  });
});

describe('placing a template of the library', () => {
  test('names the template in the options, except a built-in one', () => {
    const doc = createDocument();

    expect(nodeOptionsFromTemplate(plc(), doc).fromTemplate).toEqual(plc());
    expect(
      nodeOptionsFromTemplate(listed('p1', { source: 'preloaded' }), doc)
        .fromTemplate,
    ).toMatchObject({ id: 'p1' });
    expect(
      nodeOptionsFromTemplate(
        { ...BUILTIN_TEMPLATES[0], source: 'builtin' },
        doc,
      ),
    ).not.toHaveProperty('fromTemplate');
    // A template of the diagram is already there.
    const { template } = withCopy(plc(), doc);

    expect(nodeOptionsFromTemplate(template, doc)).not.toHaveProperty(
      'fromTemplate',
    );
  });

  test('adds a copy, once, while the diagram has room', () => {
    const first = withLinkedCopy(createDocument(), plc(2));

    expect(first.template).toMatchObject({
      name: 'PLC',
      source: { kind: 'library', id: LIBRARY_ID, version: 2 },
    });
    expect(withLinkedCopy(first.doc, plc(3))).toEqual({
      doc: first.doc,
      template: null,
    });

    for (const template of [
      undefined,
      { ...BUILTIN_TEMPLATES[0], source: 'builtin' },
      { ...plc(), source: 'diagram' },
    ]) {
      expect(withLinkedCopy(first.doc, template).template).toBeNull();
    }

    const full = {
      ...createDocument(),
      templates: Array.from({ length: MAX_TEMPLATES }, (_, index) => ({
        id: `t${index}`,
        name: `T${index}`,
        device: { spec: {} },
      })),
    };

    expect(withLinkedCopy(full, plc())).toEqual({
      doc: full,
      template: null,
      full: `This diagram has ${MAX_TEMPLATES} templates, the most it can hold.`,
    });
  });
});

describe('the links in the store', () => {
  let store;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    api.listTemplates.mockReset();
    api.createTemplates.mockReset();
    api.listTemplates.mockResolvedValue({
      templates: [plc(1)],
      collections: [],
      builtins: [],
      damaged: false,
      limits: {},
    });
    store.setDocument(sampleDocument().doc);
  });

  // The library holds `version` of the PLC template from now on.
  async function libraryAt(version) {
    api.listTemplates.mockResolvedValue({
      templates: [plc(version)],
      collections: [],
      builtins: [],
      damaged: false,
      limits: {},
    });
    await store.fetchTemplates();
  }

  test('a device placed from a library template adds a linked copy, in the same step', async () => {
    await libraryAt(1);

    const node = store.addNode(
      paletteNode(store, 'device', `library:${LIBRARY_ID}`),
    );

    expect(node.device.hostname).toBe('plc');
    expect(store.doc.templates).toEqual([
      expect.objectContaining({
        name: 'PLC',
        source: expect.objectContaining({ kind: 'library', version: 1 }),
      }),
    ]);
    expect(store.history.undoLabel()).toBe('Added device and template PLC');
    expect(store.templateLinks.map((link) => link.state)).toEqual(['current']);

    // A second device uses the copy the diagram has.
    store.addNode(paletteNode(store, 'device', `library:${LIBRARY_ID}`));
    expect(store.doc.templates).toHaveLength(1);
    expect(store.history.undoLabel()).toBe('Added device');

    store.undo();
    store.undo();
    expect(store.doc).not.toHaveProperty('templates');
  });

  test('a device placed when the diagram has no room for the copy says it has no link', async () => {
    await libraryAt(1);
    store.doc = {
      ...store.doc,
      templates: Array.from({ length: MAX_TEMPLATES }, (_, index) => ({
        id: `t${index}`,
        name: `T${index}`,
        device: { spec: {} },
      })),
    };

    store.addNode(paletteNode(store, 'device', `library:${LIBRARY_ID}`));

    expect(store.doc.templates).toHaveLength(MAX_TEMPLATES);
    expect(store.history.undoLabel()).toBe('Added device');
    expect(store.announcement).toBe(
      `Added device. This diagram has ${MAX_TEMPLATES} templates, the most it can hold. The device has no link to template PLC.`,
    );
    expect(store.notice?.text).toBe(store.announcement);
  });

  test('a later version in the library is offered, and an update is one step', async () => {
    await libraryAt(1);
    store.addNode(paletteNode(store, 'device', `library:${LIBRARY_ID}`));

    const [copy] = store.doc.templates;
    const device = store.doc.nodes.at(-1).device;

    // Checked when the library is read again.
    await libraryAt(2);
    expect(store.templateUpdates.map((link) => link.template.id)).toEqual([
      copy.id,
    ]);
    expect(store.templateLinkNotices).toHaveLength(1);
    expect(
      store.paletteTemplateGroups.find((group) => group.id === 'diagram')
        .entries[0].link.state,
    ).toBe('update');

    expect(store.updateTemplateCopies([copy.id])).toBeTruthy();
    expect(store.history.undoLabel()).toBe(
      'Updated template PLC from the library',
    );
    expect(store.announcement).toBe('Updated template PLC from the library');

    const [updated] = store.doc.templates;

    expect(updated.id).toBe(copy.id);
    expect(updated.device.spec.hardware.drives[0].image).toBe('plc-v2.qc2');
    expect(updated.source).toMatchObject({ version: 2 });
    expect(updated.source.digest).toBe(templateCopyDigest(updated));
    expect(store.templateUpdates).toEqual([]);
    // The device made from the template does not change.
    expect(store.doc.nodes.at(-1).device).toEqual(device);
    // Nothing more to update.
    expect(store.updateTemplateCopies()).toBeNull();

    store.undo();
    expect(store.doc.templates[0]).toEqual(copy);
  });

  test('Update all updates every copy with a later version', async () => {
    await libraryAt(3);

    const old = linkedCopy(plc(1));

    store.addTemplate(old);
    store.addTemplate(old);
    store.addTemplate(templateContent(listed('free')));

    expect(store.updateTemplateCopies()).toBeTruthy();
    expect(store.history.undoLabel()).toBe(
      'Updated 2 templates from the library',
    );
    expect(
      store.doc.templates.map((template) => template.source?.version),
    ).toEqual([3, 3, undefined]);
  });

  test('a deleted library template keeps the copy, and Unlink removes the link only', async () => {
    store.addTemplate(linkedCopy(plc(1)));
    api.listTemplates.mockResolvedValue({
      templates: [],
      collections: [],
      builtins: [],
      damaged: false,
      limits: {},
    });
    await store.fetchTemplates();

    const [copy] = store.doc.templates;

    expect(store.templateLinkNotices.map((link) => link.state)).toEqual([
      'deleted',
    ]);
    expect(store.updateTemplateCopies()).toBeNull();
    expect(store.unlinkTemplate(copy.id)).toBeTruthy();
    expect(store.history.undoLabel()).toBe(
      'Unlinked template PLC from the library',
    );
    expect(store.doc.templates[0]).toEqual(
      Object.fromEntries(
        Object.entries(copy).filter(([key]) => key !== 'source'),
      ),
    );
    expect(store.templateLinkNotices).toEqual([]);
    // A template without a link has nothing to unlink.
    expect(store.unlinkTemplate(copy.id)).toBeNull();
    expect(store.unlinkTemplate('gone')).toBeNull();
  });

  test('Save to library links the diagram’s template to the new library template', async () => {
    const saved = store.addTemplate(templateContent(plc()));

    api.createTemplates.mockResolvedValue({
      created: [{ id: LIBRARY_ID, etag: '"1"' }],
      collection: null,
    });

    await store.saveTemplateToLibrary(saved, {
      library: { lookup: () => null, upload: vi.fn() },
    });

    const [linked] = store.doc.templates;

    expect(linked.source).toEqual({
      kind: 'library',
      id: LIBRARY_ID,
      version: 1,
      digest: templateCopyDigest(saved),
    });
    expect(store.history.undoLabel()).toBe(
      'Linked template PLC to the library',
    );
    expect(store.templateLinks.map((link) => link.state)).toEqual(['current']);
  });

  test('a read-only diagram takes no update or unlink', async () => {
    store.addTemplate(linkedCopy(plc(1)));
    await libraryAt(2);
    store.readOnly = true;

    expect(store.updateTemplateCopies()).toBeNull();
    expect(store.unlinkTemplate(store.doc.templates[0].id)).toBeNull();
    expect(store.doc.templates[0].source.version).toBe(1);
  });
});

describe('what the Node Templates tab lists', () => {
  const library = () =>
    libraryOf({
      templates: [
        listed('plc'),
        listed('hmi'),
        listed('p1', { source: 'preloaded' }),
      ],
      collections: [
        { id: 'plant', name: 'Plant', templateIds: ['hmi', 'plc', 'gone'] },
        {
          id: 'example',
          source: 'preloaded',
          name: 'Example',
          templateIds: ['p1'],
        },
      ],
    });

  test('Show offers the library’s collections and the server’s', () => {
    expect(showChoices(library())).toEqual({
      collections: [{ value: 'plant', label: 'Plant' }],
      preloadedCollections: [{ value: 'preloaded:example', label: 'Example' }],
    });
    expect(showChoices(null)).toEqual({
      collections: [],
      preloadedCollections: [],
    });
  });

  test('each value names a list: where it is kept, which collection, in its order', () => {
    const lib = library();
    const ids = (value) =>
      shownList(lib, value)?.templates.map((template) => template.id);

    expect(shownList(lib, '')).toMatchObject({
      source: 'library',
      collection: null,
    });
    expect(ids('')).toEqual(['plc', 'hmi']);
    expect(shownList(lib, 'plant')).toMatchObject({
      source: 'library',
      collection: { id: 'plant' },
    });
    expect(ids('plant')).toEqual(['hmi', 'plc']);
    expect(ids('preloaded:example')).toEqual(['p1']);
    expect(shownList(lib, SHOW_BUILTIN).source).toBe('builtin');
    expect(ids(SHOW_BUILTIN)).toEqual(
      BUILTIN_TEMPLATES.map((template) => template.id),
    );
    expect(shownList(lib, 'gone')).toBeNull();
    expect(shownList(lib, 'library:plant')).toMatchObject({
      collection: { id: 'plant' },
    });
  });

  test('a card names the collections that hold it, of where it is kept', () => {
    const lib = library();

    expect(collectionNames(lib, lib.items[0])).toEqual(['Plant']);
    expect(collectionNames(lib, lib.items[2])).toEqual(['Example']);
    expect(
      collectionNames(lib, { ...lib.items[0], source: 'preloaded' }),
    ).toEqual([]);
  });

  test('the editor says where a read-only template comes from', () => {
    expect(templateOrigin(listed('plc'))).toBe('A template of the library.');
    expect(templateOrigin({ source: 'builtin' })).toBe(
      'A built-in template, which is read only',
    );
    expect(templateOrigin({ source: 'preloaded' })).toBe(
      'Read from a template file on the phenix server',
    );
    expect(templateOrigin({ source: 'diagram' })).toBe('');
  });

  test('a copy into the library stays within its limit', () => {
    const lib = library();

    expect(copyProblem(lib, 1)).toBe('');
    // The server's templates do not count.
    expect(copyProblem({ ...lib, limits: { templates: 3 } }, 1)).toBe('');
    expect(copyProblem({ ...lib, limits: { templates: 3 } }, 2)).toBe(
      'The library holds 3 templates, the most it can. Delete some first.',
    );
    expect(copiedMessage([listed('plc')])).toBe(
      'Added a copy of PLC to the library.',
    );
    expect(copiedMessage([listed('plc'), listed('hmi')])).toBe(
      'Added copies of 2 templates to the library.',
    );
    expect(copiedMessage([], { name: 'Example' })).toBe(
      'Added a copy of collection Example to the library.',
    );
  });
});
