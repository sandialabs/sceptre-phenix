// Device templates: named sets of prefilled fields for a device node.
//
// A template is {id, name, description?, device}. `device` is what a device
// node's payload holds, without its hostname, its interface handles and
// the topology it is included from: the icon, the custom icon, the colors
// and a complete phenix node spec. The spec's general.hostname is the base
// hostname: a device made from the template is named after it.
// `description` is the template's own text, the palette entry's tooltip. It
// is never written into a node.
//
// A diagram keeps its templates in its document (`templates`, see
// addTemplate in model.js), so they go with it. The server keeps one
// template library for all users, with collections that group its
// templates (the store's `templates`, see fetchTemplates in store.js). Every
// user who can use the Builder can change it. Beside it, the server lists
// the built-in templates and the collections of its template files, which
// are read only. The built-in templates are also here, as BUILTIN_TEMPLATES
// in catalog.js, for when the library cannot be read.
//
// A diagram's template can be a copy of a template of the library, of a
// built-in template or of a template file. Its `source` then names that
// template and the version it was copied at (see linkedCopy). When the
// library holds a later version, the editor offers to update the copy (see
// templateLink). A device made from a template is an ordinary device. It
// keeps no link to the template, so an update, a change or a deletion of a
// template changes no device.
//
// Nothing here knows about Vue. The template editor
// (components/builder/dialogs/TemplateDialog.vue) edits a template as the
// one device of a document of its own, with the Inspector (see
// templateDocument and templateEditorHost).

import { toRaw } from 'vue';

import { count } from './announce.js';
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
} from './validate.js';

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

// The interface entries of a spec, as a list whatever the spec holds.
function specInterfaces(spec) {
  const interfaces = spec?.network?.interfaces;

  return Array.isArray(interfaces) ? interfaces : [];
}

/**
 * The store.addNode options that make a device from a template in `doc`:
 * a copy of the template's spec, its look (icon, custom icon and colors),
 * its layer, and a handle for each interface its spec names. The
 * device is named after the template's hostname, which addNode makes
 * unique.
 *
 * An interface's VLAN follows the rule of a paste (see pasteClipboard). A
 * VLAN that names a network of `doc` is emptied, because the new device is
 * connected to nothing. Any other text is kept.
 *
 * A template's custom icon is an icon name, which the icon library resolves
 * (see iconSrc in icons.js): the device names it as the template does.
 *
 * For a template of the library or of a template file, the options also
 * name it (`fromTemplate`), so the store adds a copy of it to the diagram
 * with the device (see withLinkedCopy). model.addNode ignores it.
 *
 * @param {object} template
 * @param {object} doc the document the device is for
 * @returns {object} addNode options, less the position
 */
export function nodeOptionsFromTemplate(template, doc) {
  const { spec: _, layer, ...look } = templateDevice(template?.device);
  const spec = clone(template?.device?.spec ?? {});
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

/**
 * A template of a device node: the node's payload without what belongs to
 * the node alone (see templateDevice), with a copy of its spec. The node's
 * hostname stays in the spec, as the template's base hostname.
 *
 * @param {object} node a device node
 * @param {object} [options]
 * @param {string} [options.name] the template's name
 * @param {string} [options.description] its description
 * @param {boolean} [options.clearVLANs] empties every interface's VLAN. The
 *   VLANs of a device on the canvas say what it is connected to in that
 *   diagram. For a device of the template editor, the VLANs stay as typed
 * @returns {{name: string, description?: string, device: object}} without
 *   an id. The place that stores the template gives it one
 */
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

/**
 * The template a new one starts from when no device is selected: the
 * palette's plain Device, with no name.
 *
 * @returns {{name: string, device: object}}
 */
export function blankTemplate() {
  const { node } = addNode(createDocument(), {
    kind: 'device',
    hostname: 'device',
    look: { iconKey: 'server' },
  });

  return templateFromNode(node);
}

/**
 * The document the template editor edits a template in. It holds exactly
 * one device, made from the template as a diagram makes one. It also holds
 * a copy of the custom icon the template names, when the diagram the
 * template is in carries one, so the editor shows it. It has no networks,
 * so no VLAN is emptied. Its name and description are the template's.
 *
 * A device draws its icon at the icon size of the diagram it is in while it
 * names no size of its own. The document of a template of a diagram has
 * that diagram's icon size, so the Inspector names the size its devices
 * draw at there. The document of a template of a library has none, because
 * its devices draw at the size of the diagram they are added to (see
 * inspectorTarget).
 *
 * The document also has the layers of the diagram the editor opens from,
 * and its match conflict rule, so the Inspector names the layer of the
 * template's device as that diagram names it, and offers the same layers.
 *
 * @param {object} template
 * @param {object|null} [icons] the copies of icons the template's diagram
 *   carries (its `icons`), by name
 * @param {object} [options]
 * @param {string} [options.iconSize] the icon size of the template's
 *   diagram (see documentIconSize). Empty for a template of a library
 * @param {object[]} [options.layers] the layers of the diagram the editor
 *   opens from (its `layers`). Empty for the placeholder layers
 * @param {string} [options.matchConflict] the match conflict rule of that
 *   diagram (see layerSetOptions). Empty for the lowest layer ID
 * @returns {object} document
 */
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
    ? { ...doc, icons: { [name]: clone(icons[name]) } }
    : doc;
}

/**
 * Reads a template back from the template editor's document (see
 * templateDocument): its one device, as typed, with the document's name and
 * description. Its custom icon is the name the device names.
 *
 * @param {object} doc
 * @returns {{template: object}} the template, without an id
 */
export function templateFromDocument(doc) {
  const node = (doc?.nodes || []).find((entry) => entry.kind === 'device');
  const template = templateFromNode(node, {
    name: doc?.metadata?.name || '',
    description: doc?.metadata?.description || '',
  });

  return { template };
}

/**
 * What the Inspector edits in the template editor, in place of the Builder
 * store (see the `host` prop of BuilderInspector.vue): a document of the
 * template's one device, which is always the selection. A commit replaces
 * the document and drops copies of icons that it does not need, as a
 * commit of the store does (see settleIcons). The schema and the disk
 * images are the Builder store's. The actions of a canvas do nothing. The
 * layer changes the document at once, as on the canvas.
 *
 * The template editor makes it reactive.
 *
 * @param {object} options
 * @param {object} options.doc the document, from templateDocument
 * @param {object} options.source the Builder store
 * @param {(message: string) => void} options.announce receives what the
 *   Inspector says. The editor is a modal dialog, and the page's live
 *   region cannot speak through it
 * @param {boolean} [options.readOnly]
 * @param {{lookup: Function}|null} [options.library] the icon library
 * @returns {object}
 */
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

/**
 * How a palette entry names its template, for a drag and for
 * store.templateByKey:
 * - "diagram:<id>": a template of the diagram
 * - "library:<id>": a template of the library
 * - "preloaded:<id>": a template the server read from a template file
 * - "builtin:<id>": a built-in template
 * A collection is named the same way: "library:<id>" or "preloaded:<id>".
 *
 * @param {string} source 'diagram', 'library', 'preloaded' or 'builtin'
 * @param {string} id the template's id
 * @returns {string}
 */
export function templateKey(source, id) {
  return `${source}:${id}`;
}

function isItem(value) {
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

/**
 * The built-in templates: as the library lists them once it was read, else
 * as BUILTIN_TEMPLATES holds them. There are always built-in templates.
 *
 * @param {object|null} library the library, as the store keeps it
 * @returns {object[]} each with source 'builtin' and a version
 */
export function builtinTemplates(library) {
  const listed = Array.isArray(library?.builtins)
    ? library.builtins.filter(isItem)
    : [];

  return library?.loaded && listed.length ? listed : LISTED_BUILTINS;
}

/**
 * The template a key names.
 *
 * @param {object} doc
 * @param {string} key see templateKey
 * @param {object} [library] the library, as the store keeps it (`items`:
 *   its templates and the preloaded ones, each with its `source`)
 * @returns {object|undefined}
 */
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

/**
 * The content digest of a template: of its name, its description and its
 * device, as a document keeps them (see templateDevice). The source of a
 * copy records it (see linkedCopy), so a later digest that differs tells
 * that the copy was changed in the diagram.
 *
 * @param {object} template
 * @returns {string} `sha256:<hex>`
 */
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

/**
 * Whether a template is one of the library, a built-in template or one of
 * a template file, as the library lists it: a template that a diagram's
 * copy can link to.
 *
 * @param {object} template
 * @returns {boolean}
 */
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

/**
 * The source that a copy of a template records: the template, the version
 * copied and the digest of the copy (see templateCopyDigest).
 *
 * @param {object} template as the library lists it (see isLinkable)
 * @param {object} [copy] the copy, as the diagram holds it. The template's
 *   own content when not given
 * @returns {{kind: string, id: string, version: number, digest: string}}
 */
export function templateSource(template, copy = template) {
  return {
    kind: template.source,
    id: template.id,
    version: versionOf(template),
    digest: templateCopyDigest(copy),
  };
}

/**
 * A copy of a template of the library (or a built-in template, or one of a
 * template file) for a diagram: its name, its description and its device,
 * with a source that names the template (see templateSource).
 *
 * @param {object} template as the library lists it (see isLinkable)
 * @returns {{name: string, description?: string, device: object, source:
 *   object}}
 */
export function linkedCopy(template) {
  const content = templateContent(template);

  return { ...content, source: templateSource(template, content) };
}

/**
 * The template of the diagram that is a copy of a template, by its source,
 * if any. The first one, when there are several.
 *
 * @param {object} doc
 * @param {{source: string, id: string}} template as the library lists it
 * @returns {object|undefined}
 */
export function linkedCopyOf(doc, template) {
  return (doc?.templates || []).find(
    (entry) =>
      entry?.source?.kind === template?.source &&
      entry.source.id === template?.id,
  );
}

/**
 * The document with a copy of a template of the library, linked to it, when
 * the diagram has no copy linked to it yet and can take one more template
 * (see templatesFull). A device made from the template is added with it
 * (see addNode in store.js), so the diagram keeps the template it used.
 * A built-in template is not copied: it is the same on every server, and
 * it never changes.
 *
 * @param {object} doc
 * @param {object|undefined} template as the library lists it
 * @returns {{doc: object, template: object|null, full?: string}} the copy
 *   added, or null. `full` says why no copy was added when the diagram
 *   holds the most templates it can (see templatesFull)
 */
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

/**
 * How a template of the diagram stands against the template it was copied
 * from:
 * - 'current': the library holds the version that was copied, or an
 *   earlier one
 * - 'update': the library holds a later version, which an update copies
 *   (see updatedCopy). A template of a template file stays at version 1,
 *   so for its copy a content digest that differs from the one copied
 *   tells that the file changed (see contentChanged)
 * - 'deleted': the library no longer holds the template
 * - 'unknown': the library was not read, or the server cannot read it
 * `edited` is true when the copy changed in the diagram after it was copied
 * or last updated. An update replaces those changes.
 *
 * @param {object} copy a template of the diagram
 * @param {object|null} library the library, as the store keeps it
 * @returns {{state: string, kind: string, id: string, version: number,
 *   edited: boolean, latest: object|null}|null} null for a template that is
 *   no copy
 */
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

/**
 * The templates of the diagram that are copies, each with how it stands
 * (see templateLink), in the order of the diagram.
 *
 * @param {object} doc
 * @param {object|null} library the library, as the store keeps it
 * @returns {object[]} each templateLink's result, with the copy as
 *   `template`
 */
export function templateLinks(doc, library) {
  return (Array.isArray(doc?.templates) ? doc.templates : [])
    .filter(isItem)
    .map((template) => {
      const link = templateLink(template, library);

      return link ? { ...link, template } : null;
    })
    .filter(Boolean);
}

/**
 * What an update of a copy changes: the copy's name, description and
 * device become the library template's, and its source names the version
 * copied now. The copy keeps its id. See updateTemplate in model.js.
 *
 * @param {object} latest the template as the library lists it now
 * @returns {{name: string, description: string, device: object, source:
 *   object}} the patch
 */
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

/**
 * What the editor says of a copy that needs attention: a later version, or
 * a template that is gone. Nothing for a copy that is current, or whose
 * library is not known.
 *
 * @param {object|null} link see templateLink
 * @returns {string}
 */
export function templateLinkText(link) {
  switch (link?.state) {
    case 'update':
      return newerVersion(link)
        ? `The library has a newer version of this template (version ${versionOf(link.latest)}). The diagram has version ${link.version}.`
        : 'The server has a newer version of this template.';
    case 'deleted':
      return link.kind === 'library'
        ? 'The library template was deleted.'
        : `The ${SOURCE_NAMES[link.kind]} is no longer on the server.`;
    default:
      return '';
  }
}

/**
 * The short mark that the palette shows on a copy that needs attention:
 * "Update available", or that the template it was copied from is gone.
 * Empty for a copy that is current, or whose library is not known. The
 * whole text is templateLinkText.
 *
 * @param {object|null} link see templateLink
 * @returns {string}
 */
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

/**
 * What an update of copies does, for its confirmation. One copy is named,
 * several are counted. Devices made from the templates do not change. A
 * copy changed in the diagram loses its changes, and the message says so.
 *
 * @param {object[]} links the copies to update (see templateLinks)
 * @returns {{title: string, message: string, confirmLabel: string}}
 */
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
          ? 'This template was changed in the diagram. The update replaces these changes.'
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
        ? '1 of these templates was changed in the diagram. The update replaces these changes.'
        : '',
      edited > 1
        ? `${edited} of these templates were changed in the diagram. The update replaces these changes.`
        : '',
      'You can undo this.',
    ]
      .filter(Boolean)
      .join(' '),
    confirmLabel: 'Update templates',
  };
}

/**
 * The history label of an update of copies, which the page also announces.
 *
 * @param {object[]} templates the copies updated, as the diagram holds them
 * @returns {string} "Updated template PLC from the library", "Updated 3
 *   templates from the library"
 */
export function templatesUpdatedLabel(templates) {
  return templates.length === 1
    ? `Updated template ${templates[0].name} from the library`
    : `Updated ${count(templates.length, 'template')} from the library`;
}

/**
 * The history label of an unlink, which the page also announces.
 *
 * @param {object} template the copy, as the diagram holds it
 * @returns {string}
 */
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

/**
 * The section of a server collection: "Server: <collection>", or "Server"
 * alone for a collection with no name to show. So it reads as the server's
 * read-only collection, as the Node Templates tab lists it, and not as one
 * of the library.
 *
 * @param {string} [name] the collection's name
 * @returns {string}
 */
export function preloadedGroupLabel(name = '') {
  return name ? `${PRELOADED_GROUP_LABEL}: ${name}` : PRELOADED_GROUP_LABEL;
}

/**
 * The name of a collection's section: its name for one of the library,
 * and the server's for one of a template file (see preloadedGroupLabel).
 *
 * @param {object} collection as the library lists it
 * @returns {string}
 */
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

/**
 * The device templates the palette offers, in sections:
 * 1. "This diagram": the templates saved in the diagram, each with how it
 *    stands against the template it was copied from (`link`)
 * 2. the templates of the library that are in no collection, as "Library",
 *    or "Other library templates" when the library has collections
 * 3. a section for each collection: those of the library, then those of
 *    the server's template files (see collectionGroupLabel)
 * 4. "Built-in": the built-in templates, always
 * A template in several collections is in the section of each. A section
 * with no template is left out. A library that the server cannot read
 * (damaged) lists none of its templates, but still lists the server's.
 *
 * A section's id is 'diagram', 'library' or 'builtin', or for a collection
 * its key (see templateKey): "library:<id>" or "preloaded:<id>". The first
 * entry of a template has the template's test id. A further entry of the
 * same template adds "-in-<section id>", so each test id is unique.
 *
 * @param {object} doc
 * @param {object} [library] the library, as the store keeps it
 * @returns {{id: string, label: string, entries: object[]}[]} entries are
 *   {key, source, id, testid, name, description, iconKey, icon, image,
 *   link, template}
 */
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

/**
 * Why a diagram takes no further template, or '' when it does.
 *
 * @param {object} doc
 * @returns {string}
 */
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

/**
 * A name or a description as it is saved: on one line, because neither may
 * hold control characters. Each run of control characters (a pasted tab or
 * line break) becomes a space, and the text has no spaces around it.
 *
 * @param {string} text
 * @returns {string}
 */
export function templateText(text) {
  return String(text ?? '')
    .replace(CONTROL_RUN, ' ')
    .trim();
}

// What the template editor says of what keeps a template from being saved,
// by the part of the template the check is about.
function problemText(at, message) {
  switch (at) {
    case '.name':
      return /is required$/.test(message)
        ? 'Enter a name.'
        : `Use a shorter name (${MAX_TEMPLATE_NAME_BYTES} bytes at most).`;
    case '.description':
      return `Use a shorter description (${MAX_TEMPLATE_DESCRIPTION_BYTES} bytes at most).`;
    case '.device':
      return `This template is too large to save (${MAX_TEMPLATE_DEVICE_BYTES / 1024} KiB at most). Remove some of its settings.`;
    default:
      // A value of the device, which the form checks before this call. The
      // message uses the words of the check.
      return `This template cannot be saved: ${message}.`;
  }
}

/**
 * What keeps a template from being saved, in the template editor's words:
 * the checks every document and library makes of a template (see
 * templateIssues in validate.js).
 *
 * @param {object} template
 * @returns {{field: 'name'|'description'|'device', message: string}|null}
 *   the first problem, and the part of the editor it is about. null for a
 *   template that can be saved
 */
export function templateProblem(template) {
  const [issue] = templateIssues(template, 'template');

  if (!issue) {
    return null;
  }

  const at = issue.path.slice('template'.length);

  return {
    field: ['.name', '.description'].includes(at) ? at.slice(1) : 'device',
    message: problemText(at, issue.message),
  };
}

// --- the library -----------------------------------------------------------

/**
 * What the menu of a template of the diagram offers, in the palette. Save
 * to library adds a template to the library. Update, for a copy that the library has a later version of, copies that
 * version (see templateLink). Unlink, for a copy, removes its link to the
 * template it was copied from, and keeps the copy.
 *
 * @param {object|null} [link] how the template stands against the template
 *   it was copied from (see templateLink), or null for no copy
 * @returns {{id: string, label: string}[]}
 */
export function diagramTemplateActions(link = null) {
  return [
    { id: 'edit', label: 'Edit' },
    ...(link?.state === 'update' ? [{ id: 'update', label: 'Update' }] : []),
    { id: 'library', label: 'Save to library' },
    ...(link ? [{ id: 'unlink', label: 'Unlink' }] : []),
    { id: 'delete', label: 'Delete' },
  ];
}

/**
 * What a template of a diagram or of a library is saved as elsewhere: its
 * name, its description and a copy of its device. It does not include the
 * id, the source, or other data that the place it came from keeps of it.
 *
 * @param {object} template
 * @returns {{name: string, description?: string, device: object}}
 */
export function templateContent(template) {
  return {
    name: template?.name || '',
    ...(template?.description ? { description: template.description } : {}),
    device: clone(template?.device ?? {}),
  };
}

/**
 * What keeps a collection from being saved, in the collection dialog's
 * words: the checks the server makes of its name and description.
 *
 * @param {{name: string, description?: string}} collection
 * @returns {{field: 'name'|'description', message: string}|null}
 */
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
/**
 * What the deletion of templates of the library does, for the one
 * confirmation the delete gets. One template is named, several are
 * counted. The library is the same for every user, so they all lose it.
 * Diagrams keep their copies, which then say that the library template was
 * deleted (see templateLink).
 *
 * @param {object[]} templates the templates to delete, as listed
 * @returns {{title: string, message: string, confirmLabel: string}}
 */
export function templatesDeleteQuestion(templates) {
  const [first] = templates;

  if (templates.length === 1) {
    return {
      title: `Delete template ${first.name}?`,
      message:
        'It is removed from the library and its collections, for all users. Diagrams keep their copies of it. You cannot undo this.',
      confirmLabel: 'Delete template',
    };
  }

  return {
    title: `Delete ${templates.length} templates?`,
    message:
      'They are removed from the library and its collections, for all users. Diagrams keep their copies of them. You cannot undo this.',
    confirmLabel: 'Delete templates',
  };
}

/**
 * What deleting a collection does, for its confirmation.
 *
 * @param {object} collection as listed
 * @returns {{title: string, message: string, confirmLabel: string}}
 */
export function collectionDeleteQuestion(collection) {
  return {
    title: `Delete collection ${collection.name}?`,
    message:
      'The collection is removed for all users. Its templates stay in the library.',
    confirmLabel: 'Delete collection',
  };
}

/**
 * What the page says once templates were deleted from the library.
 *
 * @param {object[]} templates the deleted templates
 * @returns {string} "Deleted template PLC.", "Deleted 3 templates."
 */
export function templatesDeletedMessage(templates) {
  return templates.length === 1
    ? `Deleted template ${templates[0].name}.`
    : `Deleted ${count(templates.length, 'template')}.`;
}

/**
 * What the page says once templates joined or left a collection.
 *
 * @param {'add'|'remove'} change
 * @param {object[]} templates the templates that joined or left it
 * @param {string} collection the collection's name
 * @returns {string} "Added PLC to Plant floor.", "Removed 2 templates from
 *   Plant floor."
 */
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

/**
 * Why templates cannot be added to the library, or '' when they can. The
 * library holds a maximum number of templates.
 *
 * @param {object|null} library the library, as the store keeps it
 * @param {number} adding how many templates the change adds
 * @returns {string}
 */
export function copyProblem(library, adding) {
  const max = library?.limits?.templates ?? MAX_LIBRARY_TEMPLATES;
  const held = (library?.items || []).filter(
    (template) => template?.source === 'library',
  ).length;

  return held + adding > max
    ? `The library holds ${max} templates, the most it can. Delete some first.`
    : '';
}

/**
 * What the page says once templates were duplicated into the library (see
 * Duplicate on the Node Templates tab), where they can be changed.
 *
 * @param {object[]} templates the templates copied
 * @param {object} [collection] the collection copied with them
 * @returns {string}
 */
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

/**
 * What the Node Templates tab's Show field offers beside all the templates
 * of the library and the built-in templates (SHOW_BUILTIN):
 * 1. the collections of the library, by their id
 * 2. the collections the server read from its template files, by their key
 *    (see templateKey), under Server
 *
 * @param {object|null} library the library, as the store keeps it
 * @returns {{collections: object[], preloadedCollections: object[]}} each
 *   collection as {value, label}
 */
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

/**
 * The value of the Node Templates tab's Show field that shows a collection
 * (see shownList): its id for one of the library, else its key.
 *
 * @param {object} collection as the library lists it
 * @returns {string}
 */
export function collectionShowValue(collection) {
  return collection?.source === 'library'
    ? collection.id
    : templateKey(collection?.source, collection?.id);
}

/**
 * The list that a value of the Show field names:
 * - where its templates are kept ('library', 'builtin' or 'preloaded')
 * - the collection it is, if any
 * - its templates, in the order of the library, or of the collection
 *
 * @param {object|null} library the library, as the store keeps it
 * @param {string} value '' for every template of the library, SHOW_BUILTIN,
 *   the id of a collection of the library, or the key of a collection of
 *   the server's (see templateKey)
 * @returns {{source: string, collection: object|null, templates:
 *   object[]}|null} null for a collection the library no longer lists
 */
export function shownList(library, value) {
  const items = Array.isArray(library?.items) ? library.items : [];
  const collections = Array.isArray(library?.collections)
    ? library.collections
    : [];
  const text = String(value || '');

  if (!text) {
    return {
      source: 'library',
      collection: null,
      templates: items.filter((template) => template?.source === 'library'),
    };
  }

  if (text === SHOW_BUILTIN) {
    return {
      source: 'builtin',
      collection: null,
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
    templates: (collection.templateIds || [])
      .map((entry) => byId.get(entry))
      .filter(Boolean),
  };
}

/**
 * The collections that hold a template, by name: those of the library for
 * a template of the library, the server's for one of a template file.
 *
 * @param {object|null} library the library, as the store keeps it
 * @param {object} template as listed
 * @returns {string[]}
 */
export function collectionNames(library, template) {
  return (library?.collections || [])
    .filter(
      (collection) =>
        collection?.source === template?.source &&
        (collection.templateIds || []).includes(template.id),
    )
    .map((collection) => collection.name);
}

/**
 * Where a template comes from, for its view in the template editor, which
 * shows a template that the user cannot change. A built-in template and a
 * template of a template file are read only.
 *
 * @param {object} template as listed
 * @returns {string} '' for a template with no source, such as one of the
 *   diagram
 */
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
