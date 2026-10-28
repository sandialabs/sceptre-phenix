// Device templates: named sets of prefilled fields for a device node. A
// device made from a template keeps no link to it, so a template change
// changes no device. A diagram's copy of a library, built-in or template
// file template records its source and version (linkedCopy, templateLink).
// Nothing here knows about Vue.

import { toRaw } from 'vue';

import { count, listOf } from './announce.js';
import { BUILTIN_TEMPLATES } from './catalog.js';
import { contentDigestSync, isDigest } from './digest.js';
import { settleIcons } from './icons.js';
import {
  ICON_SIZES,
  addNode,
  addTemplate,
  createDocument,
  isLayerId,
  networkByName,
  setLayerSetOptions,
  setLayers,
  setNodeLayer,
  templateDevice,
} from './model.js';
import { utf8Length } from './text.js';
import {
  MAX_TEMPLATE_DESCRIPTION_BYTES,
  MAX_TEMPLATE_DEVICE_BYTES,
  MAX_TEMPLATE_NAME_BYTES,
  MAX_TEMPLATES,
  TEMPLATE_SOURCE_KINDS,
  templateIssues,
  unknownDriveImages,
} from './validate.js';
import { deepCopy } from './values.js';

// The interface entries of a spec, as a list whatever the spec holds.
function specInterfaces(spec) {
  const interfaces = spec?.network?.interfaces;

  return Array.isArray(interfaces) ? interfaces : [];
}

// The store.addNode options that make a device from a template in doc. A VLAN
// that names a network of doc is emptied, as a paste does, because the new
// device connects to nothing. fromTemplate makes the store add a linked copy.
export function nodeOptionsFromTemplate(template, doc) {
  const { spec: _, layer, ...look } = templateDevice(template?.device);
  const spec = deepCopy(template?.device?.spec ?? {});
  const named = new Set();

  for (const iface of specInterfaces(spec)) {
    if (!iface || typeof iface !== 'object') {
      continue;
    }

    if (
      typeof iface.vlan === 'string' &&
      iface.vlan.trim() &&
      networkByName(doc, iface.vlan.trim())
    ) {
      iface.vlan = '';
    }

    if (typeof iface.name === 'string' && iface.name.trim()) {
      named.add(iface.name);
    }
  }

  return {
    kind: 'device',
    hostname: spec.general?.hostname,
    spec,
    look,
    ...(isLayerId(layer) ? { layer } : {}),
    interfaces: [...named].map((name) => ({ name })),
    ...(isLinkable(template) && template.source !== 'builtin'
      ? { fromTemplate: template }
      : {}),
  };
}

// A template of a device node, with a copy of its spec. The hostname stays as
// the base hostname. clearVLANs empties the VLANs, which tell what a device on
// the canvas connects to. Returns no id: the place that stores it gives one.
export function templateFromNode(
  node,
  { name = '', description = '', clearVLANs = false } = {},
) {
  const device = templateDevice(node?.device);

  if (clearVLANs) {
    for (const iface of specInterfaces(device.spec)) {
      if (iface && typeof iface === 'object' && 'vlan' in iface) {
        iface.vlan = '';
      }
    }
  }

  return { name, ...(description ? { description } : {}), device };
}

/** The palette's plain Device with no name, for a new template when no device is selected. */
export function blankTemplate() {
  const { node } = addNode(createDocument(), {
    kind: 'device',
    hostname: 'device',
    look: { iconKey: 'server' },
  });

  return templateFromNode(node);
}

// The document the template editor edits a template in: one device, the icon
// copy it names, and the layers and match rule of the diagram it opens from.
// A library template gets no icon size: its devices draw at the size of the
// diagram they join (see inspectorTarget).
export function templateDocument(
  template,
  icons = null,
  { iconSize = '', layers = [], matchConflict = '' } = {},
) {
  const layered = setLayers(createDocument(), layers);
  const base = matchConflict
    ? setLayerSetOptions(layered, { matchConflict })
    : layered;
  const empty = {
    ...base,
    metadata: {
      ...base.metadata,
      name: template?.name || '',
      description: template?.description || '',
    },
    ...(ICON_SIZES.includes(iconSize) ? { iconSize } : {}),
  };
  const { doc } = addNode(empty, nodeOptionsFromTemplate(template, empty));
  const name = template?.device?.icon;

  return name &&
    icons &&
    typeof icons === 'object' &&
    Object.hasOwn(icons, name)
    ? { ...doc, icons: { [name]: deepCopy(icons[name]) } }
    : doc;
}

/** Reads the template back from the template editor's document, without an id. */
export function templateFromDocument(doc) {
  const node = (doc?.nodes || []).find((entry) => entry.kind === 'device');
  const template = templateFromNode(node, {
    name: doc?.metadata?.name || '',
    description: doc?.metadata?.description || '',
  });

  return { template };
}

// What the Inspector edits in the template editor in place of the store (the
// host prop of BuilderInspector.vue). A commit drops unneeded icon copies, as
// settleIcons does. announce exists because the page's live region cannot
// speak through the modal dialog.
export function templateEditorHost({
  doc,
  source,
  announce,
  readOnly = false,
  library = null,
}) {
  const node = (doc.nodes || []).find((entry) => entry.kind === 'device');

  return {
    doc,
    inspectorSelection: { type: 'node', id: node?.id },
    readOnly,
    issues: [],
    canRedo: false,
    get schema() {
      return source.schema;
    },
    get schemaError() {
      return source.schemaError;
    },
    get disks() {
      return source.disks;
    },
    commit(next) {
      this.doc = settleIcons(next, library);

      return true;
    },
    announce,
    addInterface() {
      return null;
    },
    removeInterface() {},
    remove() {},
    moveNodes() {
      return false;
    },
    // The layer of the template's device, which waits for the editor's
    // Save like the other fields.
    setNodeLayer(nodeId, layer) {
      const next = setNodeLayer(this.doc, nodeId, layer);

      return !this.readOnly && next !== this.doc && this.commit(next);
    },
  };
}

// --- keys ------------------------------------------------------------------

// How a palette entry names its template: "<source>:<id>", with source
// 'diagram', 'library', 'preloaded' or 'builtin'. Collections use the same form.
export function templateKey(source, id) {
  return `${source}:${id}`;
}

/** Whether a value is an object (an array counts) and not null. */
export function isItem(value) {
  return Boolean(value) && typeof value === 'object';
}

// The built-in templates as the server lists them, for when the library was
// not read: read only, at version 1 (ReadOnlyTemplateVersion in
// api/builder).
const LISTED_BUILTINS = Object.freeze(
  BUILTIN_TEMPLATES.map((template) =>
    Object.freeze({ ...template, source: 'builtin', version: 1 }),
  ),
);

/** The built-in templates as the library lists them, else BUILTIN_TEMPLATES. Never empty. */
export function builtinTemplates(library) {
  const listed = Array.isArray(library?.builtins)
    ? library.builtins.filter(isItem)
    : [];

  return library?.loaded && listed.length ? listed : LISTED_BUILTINS;
}

/** The template a key names (see templateKey). */
export function templateByKey(doc, key, library = null) {
  const text = String(key || '');
  const at = text.indexOf(':');
  const [source, id] = [text.slice(0, at), text.slice(at + 1)];
  const items = Array.isArray(library?.items) ? library.items : [];

  switch (at < 0 ? '' : source) {
    case 'diagram':
      return (doc?.templates || []).find((template) => template.id === id);
    case 'library':
    case 'preloaded':
      return items.find(
        (template) => template?.source === source && template.id === id,
      );
    case 'builtin':
      return builtinTemplates(library).find((template) => template.id === id);
    default:
      return undefined;
  }
}

// --- links to the library --------------------------------------------------

// The digests of templates by template object. A template of a document
// never changes in place (an edit replaces it), so its digest is computed
// once. Keys are raw objects, so a reactive proxy and its target share an
// entry (as in digest.js).
const copyDigests = new WeakMap();

// The content digest of a template's name, description and device. A copy's
// source records it, so a later digest that differs means the copy changed.
export function templateCopyDigest(template) {
  const raw = isItem(template) ? toRaw(template) : null;

  if (raw && copyDigests.has(raw)) {
    return copyDigests.get(raw);
  }

  const digest = contentDigestSync({
    name: raw?.name || '',
    ...(raw?.description ? { description: raw.description } : {}),
    device: templateDevice(raw?.device),
  });

  if (raw) {
    copyDigests.set(raw, digest);
  }

  return digest;
}

/** Whether a diagram's copy can link to the template: library, built-in or template file. */
function isLinkable(template) {
  return (
    TEMPLATE_SOURCE_KINDS.includes(template?.source) &&
    typeof template.id === 'string' &&
    template.id !== ''
  );
}

// A version as the library lists it: 1 or more.
function versionOf(template) {
  return Number.isInteger(template?.version) && template.version >= 1
    ? template.version
    : 1;
}

/** The source that a copy records: the template, the version copied and the copy's digest. */
export function templateSource(template, copy = template) {
  return {
    kind: template.source,
    id: template.id,
    version: versionOf(template),
    digest: templateCopyDigest(copy),
  };
}

/** A copy of a linkable template for a diagram, with a source that names it. */
export function linkedCopy(template) {
  const content = templateContent(template);

  return { ...content, source: templateSource(template, content) };
}

/** The diagram's first template that is a copy of the given template, if any. */
export function linkedCopyOf(doc, template) {
  return (doc?.templates || []).find(
    (entry) =>
      entry?.source?.kind === template?.source &&
      entry.source.id === template?.id,
  );
}

// The document with a linked copy of a library template, unless one exists or
// the diagram is full (full says why). A built-in template is not copied: it
// is the same on every server and never changes.
export function withLinkedCopy(doc, template) {
  if (
    !isLinkable(template) ||
    template.source === 'builtin' ||
    linkedCopyOf(doc, template)
  ) {
    return { doc, template: null };
  }

  const full = templatesFull(doc);

  if (full) {
    return { doc, template: null, full };
  }

  return addTemplate(doc, linkedCopy(template));
}

// The template a source names, as the library lists it.
function sourceTemplate(library, source) {
  if (source.kind === 'builtin') {
    return builtinTemplates(library).find(
      (template) => template.id === source.id,
    );
  }

  return (Array.isArray(library?.items) ? library.items : []).find(
    (template) => template?.source === source.kind && template.id === source.id,
  );
}

// How a diagram's copy stands against its source: 'current', 'update',
// 'deleted' or 'unknown'. A template file's template stays at version 1, so a
// changed content digest means 'update'. edited means the copy changed in the
// diagram. Returns null for a template that is no copy.
export function templateLink(copy, library) {
  const source = copy?.source;

  if (!isItem(source) || !TEMPLATE_SOURCE_KINDS.includes(source.kind)) {
    return null;
  }

  const link = {
    state: 'unknown',
    kind: source.kind,
    id: String(source.id ?? ''),
    version: versionOf(source),
    edited: isDigest(source.digest)
      ? templateCopyDigest(copy) !== source.digest
      : false,
    latest: null,
  };
  const known =
    source.kind === 'builtin' ||
    (Boolean(library?.loaded) &&
      !(source.kind === 'library' && library.damaged));

  if (!known) {
    return link;
  }

  const latest = sourceTemplate(library, source);

  if (!latest) {
    return { ...link, state: 'deleted' };
  }

  return {
    ...link,
    state:
      versionOf(latest) > link.version || contentChanged(source, latest)
        ? 'update'
        : 'current',
    latest,
  };
}

// Whether a template of a template file changed after it was copied. Its ID
// comes from the file name and the template name, and its version is always
// 1, so only its content tells. A source with no digest tells nothing.
function contentChanged(source, latest) {
  return (
    source.kind === 'preloaded' &&
    isDigest(source.digest) &&
    templateCopyDigest(latest) !== source.digest
  );
}

// Whether an update of a copy gets a later version number, not only changed
// content (see contentChanged).
function newerVersion(link) {
  return versionOf(link?.latest) > link?.version;
}

/** The diagram's copies, each with how it stands (see templateLink), in diagram order. */
export function templateLinks(doc, library) {
  return (Array.isArray(doc?.templates) ? doc.templates : [])
    .filter(isItem)
    .map((template) => {
      const link = templateLink(template, library);

      return link ? { ...link, template } : null;
    })
    .filter(Boolean);
}

/** The patch an update of a copy applies. The copy keeps its id (updateTemplate in model.js). */
export function updatedCopy(latest) {
  const { source, ...content } = linkedCopy(latest);

  return { description: '', ...content, source };
}

// What a link's source is called in a sentence.
const SOURCE_NAMES = {
  library: 'library template',
  builtin: 'built-in template',
  preloaded: 'server template',
};

/** What the editor says of a copy with a later version or a deleted source, else ''. */
export function templateLinkText(link) {
  switch (link?.state) {
    case 'update':
      return newerVersion(link)
        ? `The library has a newer version of this template (version ${versionOf(link.latest)}). The diagram has version ${link.version}.`
        : 'The server has a newer version of this template.';
    case 'deleted':
      return link.kind === 'library'
        ? 'Someone deleted the library template.'
        : `The ${SOURCE_NAMES[link.kind]} is no longer on the server.`;
    default:
      return '';
  }
}

/** The short palette mark of a copy that needs attention, else ''. */
export function templateLinkMark(link) {
  switch (link?.state) {
    case 'update':
      return 'Update available';
    case 'deleted':
      return link.kind === 'library'
        ? 'Deleted from library'
        : 'Removed from server';
    default:
      return '';
  }
}

/** The confirmation of an update of copies. It says when edits in the diagram are lost. */
export function templateUpdateQuestion(links) {
  const edited = links.filter((link) => link.edited).length;

  if (links.length === 1) {
    const [link] = links;

    return {
      title: `Update template ${link.template.name}?`,
      message: [
        newerVersion(link)
          ? `The template in this diagram gets version ${versionOf(link.latest)} from the library.`
          : 'The template in this diagram gets the newest version from the server.',
        'Devices made from the template do not change.',
        edited
          ? 'This template has changes in the diagram. The update replaces these changes.'
          : '',
        'You can undo this.',
      ]
        .filter(Boolean)
        .join(' '),
      confirmLabel: 'Update template',
    };
  }

  return {
    title: `Update ${links.length} templates?`,
    message: [
      'The templates in this diagram get their newest versions from the library.',
      'Devices made from the templates do not change.',
      edited === 1
        ? '1 of these templates has changes in the diagram. The update replaces these changes.'
        : '',
      edited > 1
        ? `${edited} of these templates have changes in the diagram. The update replaces these changes.`
        : '',
      'You can undo this.',
    ]
      .filter(Boolean)
      .join(' '),
    confirmLabel: 'Update templates',
  };
}

/** The history label of an update of copies, which the page also announces. */
export function templatesUpdatedLabel(templates) {
  return templates.length === 1
    ? `Updated template ${templates[0].name} from the library`
    : `Updated ${count(templates.length, 'template')} from the library`;
}

/** The history label of an unlink, which the page also announces. */
export function templateUnlinkedLabel(template) {
  return `Unlinked template ${template.name} from the library`;
}

// --- the palette -----------------------------------------------------------

// The names of the sections of templates that are not a collection. The
// section of the library's templates in no collection is "Library" while
// the library has no collection, and "Other library templates" when it has
// some.
export const TEMPLATE_GROUP_LABELS = {
  diagram: 'This diagram',
  library: 'Library',
  other: 'Other library templates',
  builtin: 'Built-in',
};

// What the Node Templates tab calls the source of the server's collections,
// which every section of one starts with.
export const PRELOADED_GROUP_LABEL = 'Server';

/** The section of a read-only server collection: "Server: <collection>", or "Server". */
export function preloadedGroupLabel(name = '') {
  return name ? `${PRELOADED_GROUP_LABEL}: ${name}` : PRELOADED_GROUP_LABEL;
}

/** The name of a collection's section: its own, or the server's for a template file. */
function collectionGroupLabel(collection) {
  return collection?.source === 'preloaded'
    ? preloadedGroupLabel(collection.name)
    : collection?.name || '';
}

// The first drive image of a template's device, which its command names.
function templateImage(template) {
  const image = template?.device?.spec?.hardware?.drives?.[0]?.image;

  return typeof image === 'string' ? image : '';
}

// What a palette entry's tooltip says: the template's description. For a
// template of one of the server's collections, it also says which
// collection.
function entryDescription(source, template, collection = '') {
  const from =
    source === 'preloaded'
      ? collection
        ? `From the server collection ${collection}, which is read only.`
        : 'From a server collection, which is read only.'
      : '';

  return [template.description, from].filter(Boolean).join(' ');
}

// The test id of a palette entry. The templates of the library and the
// built-in templates share their form: a built-in template's id is a name,
// and a library template's id is a UUID. So "palette-template-router" is the
// built-in Router.
function entryTestId(source, template) {
  switch (source) {
    case 'diagram':
      return `palette-template-diagram-${template.id}`;
    case 'preloaded':
      return `palette-template-preloaded-${template.id}`;
    default:
      return `palette-template-${template.id}`;
  }
}

function paletteEntry(source, template, collection, library) {
  return {
    key: templateKey(source, template.id),
    source,
    id: template.id,
    testid: entryTestId(source, template),
    name: template.name,
    description: entryDescription(source, template, collection),
    iconKey: template.device?.iconKey || '',
    icon: template.device?.icon || '',
    image: templateImage(template),
    // How a template of the diagram stands against the template it was
    // copied from (see templateLink): null for the others.
    link: source === 'diagram' ? templateLink(template, library) : null,
    template,
  };
}

// The sources of the library's collections and templates, in the order the
// palette lists them.
const SOURCE_ORDER = ['library', 'preloaded'];

function bySource(items) {
  return SOURCE_ORDER.flatMap((source) =>
    items.filter((item) => item.source === source),
  );
}

// The device templates the palette offers, in sections: This diagram, Library,
// one per collection, then Built-in. A template in several collections is in
// each, and its later test ids get "-in-<section id>" to stay unique. A
// damaged library lists none of its templates but still lists the server's.
export function paletteTemplateGroups(doc, library = null) {
  const loaded = Boolean(library?.loaded);
  const items = bySource(
    loaded && Array.isArray(library.items) ? library.items.filter(isItem) : [],
  );
  const collections = bySource(
    loaded && Array.isArray(library.collections)
      ? library.collections.filter(isItem)
      : [],
  );
  // Each template once, by its source and id.
  const templates = new Map();

  for (const template of items) {
    const key = templateKey(template.source, template.id);

    if (!templates.has(key)) {
      templates.set(key, template);
    }
  }

  const placed = new Set();
  const sections = collections.map((collection) => {
    const keys = [...new Set(collection.templateIds || [])]
      .map((id) => templateKey(collection.source, id))
      .filter((key) => templates.has(key));

    keys.forEach((key) => placed.add(key));

    return {
      id: templateKey(collection.source, collection.id),
      label: collectionGroupLabel(collection),
      collection: collection.source === 'preloaded' ? collection.name : '',
      templates: keys.map((key) => templates.get(key)),
    };
  });
  const rest = [...templates]
    .filter(
      ([key, template]) => !placed.has(key) && template.source === 'library',
    )
    .map(([, template]) => template);
  const groups = [
    {
      id: 'diagram',
      source: 'diagram',
      templates: Array.isArray(doc?.templates)
        ? doc.templates.filter(isItem)
        : [],
    },
    {
      id: 'library',
      label: collections.some((collection) => collection.source === 'library')
        ? TEMPLATE_GROUP_LABELS.other
        : TEMPLATE_GROUP_LABELS.library,
      templates: rest,
    },
    ...sections,
    {
      id: 'builtin',
      source: 'builtin',
      templates: builtinTemplates(library),
    },
  ];
  const testIds = new Set();

  return groups
    .filter((group) => group.templates.length > 0)
    .map(({ id, label, source, collection, templates: listed }) => ({
      id,
      label: label || TEMPLATE_GROUP_LABELS[id],
      entries: listed.map((template) => {
        const entry = paletteEntry(
          source || template.source,
          template,
          collection,
          library,
        );

        if (testIds.has(entry.testid)) {
          entry.testid = `${entry.testid}-in-${id}`;
        }

        testIds.add(entry.testid);

        return entry;
      }),
    }));
}

/** Why a diagram takes no further template, or '' when it does. */
export function templatesFull(doc) {
  return (doc?.templates || []).length >= MAX_TEMPLATES
    ? `This diagram has ${MAX_TEMPLATES} templates, the most it can hold.`
    : '';
}

// --- the editor's checks ---------------------------------------------------

// A control character, as the checks of a template find one (see
// hasControlCharacters in text.js), and what follows it of the same kind.
// eslint-disable-next-line no-control-regex
const CONTROL_RUN = /[\u0000-\u001f\u007f]+/g;

// A name or description on one line, as saved: each run of control characters
// becomes a space, and the text is trimmed.
export function templateText(text) {
  return String(text ?? '')
    .replace(CONTROL_RUN, ' ')
    .trim();
}

// What the template editor says of what keeps a template from being saved,
// by the part of the template the check is about.
function problemText(at, { code, message }) {
  switch (at) {
    case '.name':
      return code === 'template.name.required'
        ? 'Enter a name.'
        : `Use a shorter name (${MAX_TEMPLATE_NAME_BYTES} bytes at most).`;
    case '.description':
      return `Use a shorter description (${MAX_TEMPLATE_DESCRIPTION_BYTES} bytes at most).`;
    case '.device':
      return `This template is too large to save (${MAX_TEMPLATE_DEVICE_BYTES / 1024} KiB at most). Remove some of its settings.`;
    default:
      // A value of the device, which the form checks before this call. The
      // message uses the words of the check.
      return `Builder cannot save this template: ${message}.`;
  }
}

/** The first problem that keeps a template from being saved, or null (see templateIssues). */
export function templateProblem(template) {
  const [issue] = templateIssues(template, 'template');

  if (!issue) {
    return null;
  }

  const at = issue.path.slice('template'.length);

  return {
    field: ['.name', '.description'].includes(at) ? at.slice(1) : 'device',
    message: problemText(at, issue),
  };
}

// --- the library -----------------------------------------------------------

/** What the palette menu of a diagram's template offers: Save to library, Update and Unlink. */
export function diagramTemplateActions(link = null) {
  return [
    { id: 'edit', label: 'Edit' },
    ...(link?.state === 'update' ? [{ id: 'update', label: 'Update' }] : []),
    { id: 'library', label: 'Save to library' },
    ...(link ? [{ id: 'unlink', label: 'Unlink' }] : []),
    { id: 'delete', label: 'Delete' },
  ];
}

/** A template's name, description and device copy, without its id or source. */
export function templateContent(template) {
  return {
    name: template?.name || '',
    ...(template?.description ? { description: template.description } : {}),
    device: deepCopy(template?.device ?? {}),
  };
}

/** What keeps a collection from being saved, as the server checks its name and description. */
export function collectionProblem({ name, description = '' }) {
  if (!name) {
    return { field: 'name', message: 'Enter a name.' };
  }

  if (utf8Length(name) > MAX_TEMPLATE_NAME_BYTES) {
    return {
      field: 'name',
      message: `Use a shorter name (${MAX_TEMPLATE_NAME_BYTES} bytes at most).`,
    };
  }

  return utf8Length(description) > MAX_TEMPLATE_DESCRIPTION_BYTES
    ? {
        field: 'description',
        message: `Use a shorter description (${MAX_TEMPLATE_DESCRIPTION_BYTES} bytes at most).`,
      }
    : null;
}
// The confirmation of a library template delete. Every user loses the
// templates. Diagrams keep their copies, which then show the template deleted.
export function templatesDeleteQuestion(templates) {
  const [first] = templates;

  if (templates.length === 1) {
    return {
      title: `Delete template ${first.name}?`,
      message:
        'This removes the template from the library and its collections, for all users. Diagrams keep their copies of it. You cannot undo this.',
      confirmLabel: 'Delete template',
    };
  }

  return {
    title: `Delete ${templates.length} templates?`,
    message:
      'This removes the templates from the library and its collections, for all users. Diagrams keep their copies of them. You cannot undo this.',
    confirmLabel: 'Delete templates',
  };
}

/** What deleting a collection does, for its confirmation. */
export function collectionDeleteQuestion(collection) {
  return {
    title: `Delete collection ${collection.name}?`,
    message:
      'This removes the collection for all users. Its templates stay in the library.',
    confirmLabel: 'Delete collection',
  };
}

/** What the page says once templates were deleted from the library. */
export function templatesDeletedMessage(templates) {
  return templates.length === 1
    ? `Deleted template ${templates[0].name}.`
    : `Deleted ${count(templates.length, 'template')}.`;
}

/** What the page says once templates joined or left a collection. */
export function membersMessage(change, templates, collection) {
  const what =
    templates.length === 1
      ? templates[0].name
      : count(templates.length, 'template');

  return change === 'add'
    ? `Added ${what} to ${collection}.`
    : `Removed ${what} from ${collection}.`;
}

// The most templates the library holds (api/builder), until the server
// says.
const MAX_LIBRARY_TEMPLATES = 200;

/** Why templates cannot be added to the library (its limit), or '' when they can. */
export function copyProblem(library, adding) {
  const max = library?.limits?.templates ?? MAX_LIBRARY_TEMPLATES;
  const held = (library?.items || []).filter(
    (template) => template?.source === 'library',
  ).length;

  return held + adding > max
    ? `The library holds ${max} templates, the most it can. Delete some first.`
    : '';
}

/** What the page says once templates were duplicated into the library. */
export function copiedMessage(templates, collection = null) {
  if (collection) {
    return `Added a copy of collection ${collection.name} to the library.`;
  }

  return templates.length === 1
    ? `Added a copy of ${templates[0].name} to the library.`
    : `Added copies of ${count(templates.length, 'template')} to the library.`;
}

// --- what the Node Templates tab lists -------------------------------------

// The Show field's value for the built-in templates. An id of the library
// holds no ':'. So it names no collection of the library, whose value is
// its id.
export const SHOW_BUILTIN = 'builtin:';

// The Show field's value for the templates of the library that name a disk
// image the server does not have. It holds ':', as SHOW_BUILTIN does.
export const SHOW_MISSING_IMAGES = 'missing-images:';

// The drive images each template names that the server does not have, by
// template key. Empty while the disk images are unknown (disks is null).
export function missingImages(templates, disks) {
  const known = Array.isArray(disks) ? new Set(disks) : null;
  const found = new Map();

  if (!known) {
    return found;
  }

  (Array.isArray(templates) ? templates : []).forEach((template) => {
    const images = [
      ...new Set(
        unknownDriveImages(template?.device?.spec, known).map(
          ({ image }) => image,
        ),
      ),
    ];

    if (images.length) {
      found.set(templateKey(template.source, template.id), images);
    }
  });

  return found;
}

/** What a template card says of drive images the server does not have, or ''. */
export function missingImagesText(images) {
  if (!images?.length) {
    return '';
  }

  const names = listOf(images.map((image) => `"${image}"`));

  return images.length === 1
    ? `The server has no disk image named ${names}.`
    : `The server has no disk images named ${names}.`;
}

/** The collections the Node Templates tab's Show field offers: the library's, then the server's. */
export function showChoices(library) {
  const collections = Array.isArray(library?.collections)
    ? library.collections.filter(isItem)
    : [];
  const of = (source) =>
    collections
      .filter((collection) => collection.source === source)
      .map((collection) => ({
        value: collectionShowValue(collection),
        label: collection.name,
      }));

  return {
    collections: of('library'),
    preloadedCollections: of('preloaded'),
  };
}

/** The Show field value of a collection: its id in the library, else its key. */
export function collectionShowValue(collection) {
  return collection?.source === 'library'
    ? collection.id
    : templateKey(collection?.source, collection?.id);
}

// The list a Show field value names: source, collection, missingImages and
// templates. Returns null for a collection the library no longer lists, and
// for SHOW_MISSING_IMAGES while the disk images are unknown.
export function shownList(library, value, { disks = null } = {}) {
  const items = Array.isArray(library?.items) ? library.items : [];
  const collections = Array.isArray(library?.collections)
    ? library.collections
    : [];
  const text = String(value || '');
  const mine = items.filter((template) => template?.source === 'library');

  if (!text) {
    return {
      source: 'library',
      collection: null,
      missingImages: false,
      templates: mine,
    };
  }

  if (text === SHOW_MISSING_IMAGES) {
    if (!Array.isArray(disks)) {
      return null;
    }

    const missing = missingImages(mine, disks);

    return {
      source: 'library',
      collection: null,
      missingImages: true,
      templates: mine.filter((template) =>
        missing.has(templateKey(template.source, template.id)),
      ),
    };
  }

  if (text === SHOW_BUILTIN) {
    return {
      source: 'builtin',
      collection: null,
      missingImages: false,
      templates: builtinTemplates(library),
    };
  }

  const at = text.indexOf(':');
  const [source, id] =
    at < 0 ? ['library', text] : [text.slice(0, at), text.slice(at + 1)];
  const collection = collections.find(
    (entry) => entry?.source === source && entry.id === id,
  );

  if (!collection) {
    return null;
  }

  const byId = new Map(
    items
      .filter((template) => template?.source === collection.source)
      .map((template) => [template.id, template]),
  );

  return {
    source: collection.source,
    collection,
    missingImages: false,
    templates: (collection.templateIds || [])
      .map((entry) => byId.get(entry))
      .filter(Boolean),
  };
}

/** The names of the collections that hold a template, from the template's own source. */
export function collectionNames(library, template) {
  return (library?.collections || [])
    .filter(
      (collection) =>
        collection?.source === template?.source &&
        (collection.templateIds || []).includes(template.id),
    )
    .map((collection) => collection.name);
}

/** Where a read-only template comes from, for the template editor, or '' for none. */
export function templateOrigin(template) {
  switch (template?.source) {
    case 'library':
      return 'A template of the library.';
    case 'builtin':
      return 'A built-in template, which is read only';
    case 'preloaded':
      return 'Read from a template file on the phenix server';
    default:
      return '';
  }
}
