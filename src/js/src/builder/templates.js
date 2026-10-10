// Device templates: named sets of prefilled fields for a device node.
//
// A template is {id, name, description?, device}, where `device` is what a
// device node's payload holds, less its hostname, its interface handles and
// the topology it is included from: the icon, the custom icon, the colors
// and a complete phenix node spec. The spec's general.hostname is the base
// hostname: a device made from the template is named after it. `description`
// is the template's own text, the palette entry's tooltip; it is never
// written into a node.
//
// A diagram keeps its templates in its document (`templates`, see
// addTemplate in model.js), so they travel with it. A user also has a
// library of templates on the server, with collections that group them
// (the store's `templates`, see fetchTemplates in store.js): it starts with
// the built-in ones, which are BUILTIN_TEMPLATES in catalog.js here, for
// when the library cannot be read. A device made from a template is an
// ordinary device: it keeps no link to the template, so changing or deleting
// a template changes no diagram.
//
// Nothing here knows about Vue. The template editor
// (components/builder/dialogs/TemplateDialog.vue) edits a template as the
// one device of a document of its own, with the Inspector (see
// templateDocument and templateEditorHost).

import { count, describeNames } from './announce.js';
import { BUILTIN_TEMPLATES } from './catalog.js';
import { settleIcons } from './icons.js';
import { MAX_SHARES } from './limits.js';
import {
  ICON_SIZES,
  addNode,
  createDocument,
  networkByName,
  setPurdueLevel,
  templateDevice,
} from './model.js';
import { reasonMessage, validUsername } from './share.js';
import { utf8Length } from './text.js';
import {
  MAX_TEMPLATE_DESCRIPTION_BYTES,
  MAX_TEMPLATE_DEVICE_BYTES,
  MAX_TEMPLATE_NAME_BYTES,
  MAX_TEMPLATES,
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
 * its Purdue level, and a handle for each interface its spec names. The
 * device is named after the template's hostname, which addNode makes
 * unique.
 *
 * An interface's VLAN follows the rule of a paste (see pasteClipboard): one
 * that names a network of `doc` is emptied, since the new device is
 * connected to nothing; any other text is kept.
 *
 * A template's custom icon is an icon name, which the icon library resolves
 * (see iconSrc in icons.js): the device names it as the template does.
 *
 * @param {object} template
 * @param {object} doc the document the device is for
 * @returns {object} addNode options, less the position
 */
export function nodeOptionsFromTemplate(template, doc) {
  const { spec: _, purdueLevel, ...look } = templateDevice(template?.device);
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
    ...(purdueLevel ? { purdueLevel } : {}),
    interfaces: [...named].map((name) => ({ name })),
  };
}

/**
 * A template of a device node: the node's payload less what belongs to the
 * node alone (see templateDevice), its spec copied. The node's hostname
 * stays in the spec, as the template's base hostname.
 *
 * @param {object} node a device node
 * @param {object} [options]
 * @param {string} [options.name] the template's name
 * @param {string} [options.description] its description
 * @param {boolean} [options.clearVLANs] empties every interface's VLAN: the
 *   VLANs of a device on the canvas say what it is connected to in that
 *   diagram. Left as typed for a device of the template editor
 * @returns {{name: string, description?: string, device: object}} without
 *   an id, which the place that keeps the template gives it
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
 * The document the template editor edits a template in: one that holds
 * exactly one device, made from the template as a diagram makes one, and a
 * copy of the custom icon the template names when the diagram the template
 * is in carries one, so the editor shows it. It has no networks, so no VLAN
 * is emptied. Its name and description are the template's.
 *
 * A device draws its icon at the icon size of the diagram it is in while it
 * names none of its own. The document of a template of a diagram has that
 * diagram's icon size, so the Inspector names the size its devices draw at
 * there; that of a template of a library has none, as its devices draw at
 * the size of whichever diagram they are added to (see inspectorTarget).
 *
 * @param {object} template
 * @param {object|null} [icons] the copies of icons the template's diagram
 *   carries (its `icons`), by name
 * @param {object} [options]
 * @param {string} [options.iconSize] the icon size of the template's
 *   diagram (see documentIconSize); empty for a template of a library
 * @returns {object} document
 */
export function templateDocument(
  template,
  icons = null,
  { iconSize = '' } = {},
) {
  const base = createDocument();
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
 * the document, dropping copies of icons it need not carry, as a commit of
 * the store does (see settleIcons). The schema and the disk images are the
 * Builder store's; the actions of a canvas do nothing. The Purdue layer
 * changes the document at once, as on the canvas.
 *
 * The template editor makes it reactive.
 *
 * @param {object} options
 * @param {object} options.doc the document, from templateDocument
 * @param {object} options.source the Builder store
 * @param {(message: string) => void} options.announce what the Inspector
 *   says goes here: the editor is a modal dialog, which the page's live
 *   region cannot speak through
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
    canCreateDrafts: false,
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
    // The Purdue layer of the template's device, which waits for the
    // editor's Save like the other fields.
    setPurdueLevel(nodeId, level) {
      const next = setPurdueLevel(this.doc, nodeId, level);

      return !this.readOnly && next !== this.doc && this.commit(next);
    },
  };
}

// --- keys ------------------------------------------------------------------

// The templates of other users' libraries are named with their owner.
const OTHERS = ['shared', 'server'];

/**
 * How a palette entry names its template, for a drag and for
 * store.templateByKey: "diagram:<id>" for one of the diagram, "own:<id>"
 * for one of the user's library, "shared:<owner>/<id>" and
 * "server:<owner>/<id>" for one of another user's library that is shared
 * with the user or published server-wide, "preloaded:<id>" for one the
 * server read from a template file, and "builtin:<id>" for a built-in one.
 *
 * @param {string} source 'diagram', 'own', 'shared', 'server', 'preloaded'
 *   or 'builtin'
 * @param {string} id the template's id
 * @param {string} [owner] the owner of another user's template
 * @returns {string}
 */
export function templateKey(source, id, owner = '') {
  return OTHERS.includes(source)
    ? `${source}:${owner}/${id}`
    : `${source}:${id}`;
}

/**
 * The template a key names.
 *
 * @param {object} doc
 * @param {string} key see templateKey
 * @param {object} [library] the user's library, as the store keeps it
 *   (`items`: its templates, each with its `source` and `owner`)
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
    case 'own':
    case 'preloaded':
      return items.find(
        (template) => template.source === source && template.id === id,
      );
    case 'shared':
    case 'server': {
      // An id holds no "/", and a user name may.
      const cut = id.lastIndexOf('/');

      return items.find(
        (template) =>
          template.source === source &&
          template.owner === id.slice(0, cut) &&
          template.id === id.slice(cut + 1),
      );
    }
    case 'builtin':
      return BUILTIN_TEMPLATES.find((template) => template.id === id);
    default:
      return undefined;
  }
}

// --- the palette -----------------------------------------------------------

// The palette's groups of templates, in the order it lists them. The
// server's collections each have a group of their own, after the
// server-wide templates (see preloadedGroupLabel).
export const TEMPLATE_GROUP_LABELS = {
  diagram: 'This diagram',
  own: 'My library',
  shared: 'Shared with me',
  server: 'Server-wide',
  builtin: 'Built-in',
};

// What the Node Templates tab calls the source of the server's collections,
// which every group of one starts with.
export const PRELOADED_GROUP_LABEL = 'Server';

/**
 * The palette group of a server collection: "Server: <collection>", or
 * "Server" alone for a collection with no name to show, so it reads as the
 * server's, as the Node Templates tab lists it, and not as the user's.
 *
 * @param {string} [name] the collection's name
 * @returns {string}
 */
export function preloadedGroupLabel(name = '') {
  return name ? `${PRELOADED_GROUP_LABEL}: ${name}` : PRELOADED_GROUP_LABEL;
}

// The first drive image of a template's device, which its command names.
function templateImage(template) {
  const image = template?.device?.spec?.hardware?.drives?.[0]?.image;

  return typeof image === 'string' ? image : '';
}

// What a palette entry's tooltip says: the template's description, and for
// another user's template, whose it is; for one of the server's
// collections, which one.
function entryDescription(source, template, collection = '') {
  const whose = {
    shared: `Shared by ${template.owner}.`,
    server: `Published by ${template.owner}.`,
    preloaded: collection
      ? `From the server collection ${collection}, which is read only.`
      : 'From a server collection, which is read only.',
  }[source];

  return [template.description, whose].filter(Boolean).join(' ');
}

// The test id of a palette entry. The user's own templates and the
// built-in ones share theirs: a library starts with the built-in templates,
// under their ids, so "palette-template-router" is the Router entry
// whether the library was read or not.
function entryTestId(source, template) {
  switch (source) {
    case 'diagram':
      return `palette-template-diagram-${template.id}`;
    case 'shared':
    case 'server':
      return `palette-template-${source}-${template.owner}-${template.id}`;
    case 'preloaded':
      return `palette-template-preloaded-${template.id}`;
    default:
      return `palette-template-${template.id}`;
  }
}

function paletteEntry(source, template, collection = '') {
  return {
    key: templateKey(source, template.id, template.owner),
    source,
    id: template.id,
    testid: entryTestId(source, template),
    name: template.name,
    description: entryDescription(source, template, collection),
    iconKey: template.device?.iconKey || '',
    icon: template.device?.icon || '',
    image: templateImage(template),
    template,
  };
}

/**
 * What the palette can take from the user's library: 'ready' once it was
 * read, 'pending' while its first read is under way, and 'missing' when
 * there is none to show: none was asked for, it could not be read, or the
 * server cannot read what it stored (damaged). A read tried again after
 * one that failed leaves it missing until it answers, so what stands in
 * for the library stays in view meanwhile.
 *
 * @param {object|null} library the user's library, as the store keeps it
 * @returns {'ready'|'pending'|'missing'}
 */
export function libraryUse(library) {
  if (library?.loaded) {
    return library.damaged ? 'missing' : 'ready';
  }

  return library?.status === 'loading' && !library.error
    ? 'pending'
    : 'missing';
}

/**
 * The device templates the palette offers, in groups: those saved in the
 * diagram, then those of the user's library (their own, those shared with
 * them, and those published server-wide), then each collection the server
 * read from a template file, in a group of its own ("preloaded:<id>") whose
 * label says it is the server's (see preloadedGroupLabel).
 * While there is no library to show (see libraryUse), the built-in
 * templates stand in for it, so a diagram can still be built; while its
 * first read is under way, nothing does. A library the server cannot read
 * (damaged) lists none of the user's own, but still other users' templates
 * and the server's. A group with no template is left out, and a template of
 * the library is listed once.
 *
 * @param {object} doc
 * @param {object} [library] the user's library, as the store keeps it
 * @returns {{id: string, label: string, entries: object[]}[]} entries are
 *   {key, source, id, testid, name, description, iconKey, icon, image,
 *   template}
 */
export function paletteTemplateGroups(doc, library = null) {
  const use = libraryUse(library);
  const items =
    library?.loaded && Array.isArray(library.items) ? library.items : [];
  const seen = new Set();
  // Own before shared before server-wide.
  const from = (source) =>
    items.filter((template) => {
      const key = `${template?.owner}/${template?.id}`;

      if (template?.source !== source || seen.has(key)) {
        return false;
      }

      seen.add(key);

      return true;
    });
  // The server's collections, each with its templates in its order.
  const preloaded = (
    library?.loaded && Array.isArray(library.collections)
      ? library.collections
      : []
  )
    .filter((collection) => collection?.source === 'preloaded')
    .map((collection) => ({
      id: `preloaded:${collection.id}`,
      source: 'preloaded',
      label: preloadedGroupLabel(collection.name),
      collection: collection.name || '',
      templates: (collection.templateIds || []).map((id) =>
        items.find(
          (template) => template?.source === 'preloaded' && template.id === id,
        ),
      ),
    }));
  const groups = [
    { id: 'diagram', templates: doc?.templates },
    { id: 'own', templates: from('own') },
    { id: 'shared', templates: from('shared') },
    { id: 'server', templates: from('server') },
    ...preloaded,
    { id: 'builtin', templates: use === 'missing' ? BUILTIN_TEMPLATES : [] },
  ];

  return groups
    .map(({ id, source = id, label, collection, templates }) => ({
      id,
      label: label || TEMPLATE_GROUP_LABELS[id],
      entries: (Array.isArray(templates) ? templates : [])
        .filter((template) => template && typeof template === 'object')
        .map((template) => paletteEntry(source, template, collection)),
    }))
    .filter((group) => group.entries.length > 0);
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
 * A name or a description as it is saved: on one line, since neither may
 * hold control characters, with each run of them (a pasted tab or line
 * break) turned into a space, and without spaces around it.
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
      // A value of the device, which the form checks before this is
      // asked: said as the check words it.
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
 *   the first problem, and the part of the editor it is about; null for a
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
 * to library adds a template to the user's library, which the role must
 * allow.
 *
 * @param {{create?: boolean}} rights what the role may do to its library
 * @returns {{id: string, label: string}[]}
 */
export function diagramTemplateActions(rights) {
  return [
    { id: 'edit', label: 'Edit' },
    ...(rights?.create ? [{ id: 'library', label: 'Save to library' }] : []),
    { id: 'delete', label: 'Delete' },
  ];
}

/**
 * What a template of a diagram or of a library is saved as elsewhere: its
 * name, its description and a copy of its device, without the id and what
 * else the place it came from keeps of it.
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
 * The people an item of the library is shared with now: a share whose
 * account was removed gives no one anything.
 *
 * @param {object} item a template or a collection, as listed
 * @returns {string[]} usernames
 */
export function sharedWith(item) {
  return (item?.shares || [])
    .filter((share) => share?.user && !share.stale)
    .map((share) => share.user);
}

/**
 * What deleting templates of the library does, for the one confirmation
 * the delete gets: one template is named, several are counted. The people
 * a template is shared with lose it too, which is said when there are
 * some.
 *
 * @param {object[]} templates the templates to delete, as listed
 * @returns {{title: string, message: string, confirmLabel: string}}
 */
export function templatesDeleteQuestion(templates) {
  const [first] = templates;

  if (templates.length === 1) {
    const who = describeNames(sharedWith(first));

    return {
      title: `Delete template ${first.name}?`,
      message: `It is removed from your library and its collections.${who ? ` ${who} will lose it too.` : ''} Diagrams that used it are not changed. This cannot be undone.`,
      confirmLabel: 'Delete template',
    };
  }

  const shared = templates.some((template) => sharedWith(template).length);

  return {
    title: `Delete ${templates.length} templates?`,
    message: `They are removed from your library and its collections.${shared ? ' People you shared them with lose them too.' : ''} Diagrams that used them are not changed. This cannot be undone.`,
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
  const lost =
    sharedWith(collection).length || collection.serverWide
      ? ' People who had them only through this collection lose them.'
      : '';

  return {
    title: `Delete collection ${collection.name}?`,
    message: `The collection is removed. Its templates stay in your library.${lost}`,
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
 * The built-in templates the user's library does not hold, in the order
 * of BUILTIN_TEMPLATES: the ones a restore can add back. It is empty
 * until the library was read, and while the server cannot read it
 * (see libraryUse). A built-in template the user changed keeps its id,
 * so it is never missing.
 *
 * @param {object|null} library the user's library, as the store keeps it
 * @returns {object[]} built-in templates
 */
export function missingBuiltinTemplates(library) {
  if (libraryUse(library) !== 'ready') {
    return [];
  }

  const own = new Set(
    (library.items || [])
      .filter((item) => item?.source === 'own')
      .map((item) => item.id),
  );

  return BUILTIN_TEMPLATES.filter((template) => !own.has(template.id));
}

/**
 * What the page says once built-in templates were restored.
 *
 * @param {object[]} templates the restored templates
 * @returns {string} "Restored template Router.", "Restored 3 templates.",
 *   or what it means when there was none to restore
 */
export function templatesRestoredMessage(templates) {
  if (!templates.length) {
    return 'Your library already holds these built-in templates.';
  }

  return templates.length === 1
    ? `Restored template ${templates[0].name}.`
    : `Restored ${count(templates.length, 'template')}.`;
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

// --- what the Node Templates tab lists -------------------------------------

// The Show field's values for the templates other users share with the user
// and for those published server-wide. An id of a library holds no ':', so
// none of these names a collection of the user's own, whose value is its id.
export const SHOW_SHARED = 'shared:';
export const SHOW_SERVER = 'server:';

/**
 * What the Node Templates tab's Show field offers beside "My templates":
 * the user's collections; then, when other users share some with the user,
 * "Shared with me" and the collections shared; when some are published
 * server-wide, "Server-wide" and those collections; and the collections the
 * server read from its template files, under Server. Another user's
 * collection is named with its owner, and one of the server's by its name.
 *
 * @param {object|null} library the library, as the store keeps it
 * @returns {{own: object[], shared: boolean, sharedCollections: object[],
 *   server: boolean, serverCollections: object[], preloadedCollections:
 *   object[]}} each collection as {value, label}
 */
export function showChoices(library) {
  const items = Array.isArray(library?.items) ? library.items : [];
  const collections = Array.isArray(library?.collections)
    ? library.collections
    : [];
  const label = (source, collection) => {
    switch (source) {
      case 'own':
      case 'preloaded':
        return collection.name;
      default:
        return `${collection.name} (${collection.owner})`;
    }
  };
  const of = (source) =>
    collections
      .filter((collection) => collection?.source === source)
      .map((collection) => ({
        value:
          source === 'own'
            ? collection.id
            : templateKey(source, collection.id, collection.owner),
        label: label(source, collection),
      }));
  const has = (source) =>
    [...items, ...collections].some((item) => item?.source === source);

  return {
    own: of('own'),
    shared: has('shared'),
    sharedCollections: of('shared'),
    server: has('server'),
    serverCollections: of('server'),
    preloadedCollections: of('preloaded'),
  };
}

/**
 * The list a value of the Show field names: whose templates it holds (own:
 * the user's; shared or server: other users'), the collection it is, if
 * any, and its templates, in the order the library lists them, or the
 * collection holds them.
 *
 * @param {object|null} library the library, as the store keeps it
 * @param {string} value '' for every template of the user's, the id of
 *   one of the user's collections, SHOW_SHARED, SHOW_SERVER, or the key of
 *   another user's collection or of one of the server's (see templateKey)
 * @returns {{source: string, collection: object|null, templates:
 *   object[]}|null} null for a collection the library no longer lists
 */
export function shownList(library, value) {
  const items = Array.isArray(library?.items) ? library.items : [];
  const collections = Array.isArray(library?.collections)
    ? library.collections
    : [];
  const text = String(value || '');

  if (!text || text === SHOW_SHARED || text === SHOW_SERVER) {
    const source = text ? text.slice(0, -1) : 'own';

    return {
      source,
      collection: null,
      templates: items.filter((template) => template?.source === source),
    };
  }

  const at = text.indexOf(':');
  let collection;

  if (at < 0) {
    collection = collections.find(
      (entry) => entry?.source === 'own' && entry.id === text,
    );
  } else if (text.slice(0, at) === 'preloaded') {
    collection = collections.find(
      (entry) =>
        entry?.source === 'preloaded' && entry.id === text.slice(at + 1),
    );
  } else {
    // An id holds no "/", and a user name may.
    const rest = text.slice(at + 1);
    const cut = rest.lastIndexOf('/');

    collection = collections.find(
      (entry) =>
        entry?.source === text.slice(0, at) &&
        entry.owner === rest.slice(0, cut) &&
        entry.id === rest.slice(cut + 1),
    );
  }

  if (!collection) {
    return null;
  }

  // The templates of the collection's library: another user's are listed
  // as shared or server-wide, as each one is reached; the server's are its
  // own.
  const ofLibrary = (template) => {
    switch (collection.source) {
      case 'own':
      case 'preloaded':
        return template?.source === collection.source;
      default:
        return (
          !['own', 'preloaded'].includes(template?.source) &&
          template?.owner === collection.owner
        );
    }
  };
  const byId = new Map(
    items.filter(ofLibrary).map((template) => [template.id, template]),
  );

  return {
    source: collection.source,
    collection,
    templates: (collection.templateIds || [])
      .map((id) => byId.get(id))
      .filter(Boolean),
  };
}

/**
 * The collections of a template's library that hold it, of those the user
 * sees, by name.
 *
 * @param {object|null} library the library, as the store keeps it
 * @param {object} template as listed
 * @returns {string[]}
 */
export function collectionNames(library, template) {
  const own = template?.source === 'own';

  return (library?.collections || [])
    .filter(
      (collection) =>
        (collection?.source === 'own') === own &&
        collection.owner === template.owner &&
        (collection.templateIds || []).includes(template.id),
    )
    .map((collection) => collection.name);
}

/**
 * Whom another user's template comes from, for its view in the template
 * editor.
 *
 * @param {object} template as listed
 * @returns {string} "Shared by bob", "Published by bob", "Read from a
 *   template file on the phenix server", or '' for one of the user's own
 */
export function templateOrigin(template) {
  switch (template?.source) {
    case 'shared':
      return `Shared by ${template.owner}`;
    case 'server':
      return `Published by ${template.owner}`;
    case 'preloaded':
      return 'Read from a template file on the phenix server';
    default:
      return '';
  }
}

// --- sharing and publishing ------------------------------------------------

// What items of the library are, together: templates, collections, or
// items when they are both.
function itemsNoun(items) {
  const kinds = new Set(
    items.map((item) =>
      item?.kind === 'collection' ? 'collection' : 'template',
    ),
  );

  return kinds.size === 1 ? [...kinds][0] : 'item';
}

/**
 * What the Share dialog, and what it comes to, call the items it shares:
 * one by its name, several counted.
 *
 * @param {{kind?: string, name: string}[]} targets templates (kind
 *   'template' or none) and collections (kind 'collection')
 * @returns {string} "PLC", "3 templates", "2 items"
 */
export function shareSubject(targets) {
  return targets.length === 1
    ? targets[0].name
    : count(targets.length, itemsNoun(targets));
}

// What the owner is told when they name themselves.
function ownedText(targets) {
  const noun = itemsNoun(targets);

  return targets.length === 1
    ? `You own this ${noun}.`
    : `You own these ${noun}s.`;
}

/**
 * The people one item is shared with, as the Share dialog lists them: a
 * share whose account was removed (stale) starts marked for removal.
 *
 * @param {object} item a template or a collection, as listed
 * @returns {{user: string, stale: boolean, removed: boolean}[]}
 */
export function shareRows(item) {
  return (item?.shares || [])
    .filter((share) => share?.user)
    .map((share) => ({
      user: share.user,
      stale: share.stale === true,
      removed: share.stale === true,
    }));
}

/**
 * Checks a person before the Share dialog adds them to the people to add.
 * Someone one item has, marked for removal, is kept rather than added; a
 * share whose account was removed is replaced by one for the account the
 * name has now.
 *
 * @param {string} name as typed
 * @param {object} options
 * @param {object[]} options.targets the items shared
 * @param {string[]} [options.people] the people to add, so far
 * @param {object[]} [options.rows] the one item's people (see shareRows);
 *   none for several items
 * @param {string} options.owner the items' owner, who shares them
 * @param {string[]|null} options.knownUsers the usernames the items may be
 *   shared with, or null while they are not known
 * @param {number} [options.max] the most people an item is shared with
 * @returns {{user: string, error: string, row?: object}} error is '' when
 *   the person can be added; row is their row, to keep
 */
export function validateTemplateShareAdd(
  name,
  { targets, people = [], rows = [], owner, knownUsers, max = MAX_SHARES },
) {
  const user = String(name ?? '').trim();
  const row = rows.find((entry) => entry.user === user && !entry.stale);

  if (!user) {
    return { user, error: 'Enter a username.' };
  }

  if (user === owner) {
    return { user, error: ownedText(targets) };
  }

  if (people.includes(user)) {
    return { user, error: `${user} is already in the list.` };
  }

  if (row) {
    return row.removed
      ? { user, error: '', row }
      : { user, error: `${user} already has access.`, row };
  }

  if (!validUsername(user)) {
    return { user, error: reasonMessage('invalid-user', user) };
  }

  if (Array.isArray(knownUsers) && !knownUsers.includes(user)) {
    return { user, error: `No user named ${user}.` };
  }

  if (targets.length === 1) {
    const kept = rows.filter((entry) => !entry.stale && !entry.removed);

    if (kept.length + people.length >= max) {
      return {
        user,
        error: `${targets[0].name} can be shared with at most ${max} people.`,
      };
    }
  } else if (people.length >= max) {
    return { user, error: `At most ${max} people can be added at once.` };
  }

  return { user, error: '' };
}

/**
 * What the Share dialog changes when it is saved.
 *
 * @param {object} state
 * @param {object[]} state.targets the items shared
 * @param {string[]} state.people the people to add
 * @param {object[]} state.rows the one item's people (see shareRows)
 * @param {boolean|null} state.serverWide whether the items are to be
 *   server-wide; null to leave them as they are
 * @returns {{add: string[], remove: string[], publish: boolean|null,
 *   count: number, asked: number}} publish is null when server-wide does
 *   not change; count counts every change, and asked those the user made,
 *   which leaves out the shares marked for removal because their account
 *   was removed
 */
export function templateShareChange({ targets, people, rows, serverWide }) {
  const add = [...people];
  // Someone added again is not removed: a share whose account was removed
  // is replaced by one for the account the name has now.
  const removing = rows.filter((row) => row.removed && !add.includes(row.user));
  const unchanged =
    serverWide === null ||
    serverWide === undefined ||
    (targets.length === 1 && serverWide === Boolean(targets[0].serverWide));
  const publish = unchanged ? null : serverWide;
  const changes = add.length + removing.length + (publish === null ? 0 : 1);

  return {
    add,
    remove: removing.map((row) => row.user),
    publish,
    count: changes,
    asked: changes - removing.filter((row) => row.stale).length,
  };
}

/**
 * Whether other people reach a template of the user's through one of the
 * collections that hold it: one shared with someone, or server-wide.
 *
 * @param {object|null} library the library, as the store keeps it
 * @param {object} item a template or a collection, as listed
 * @returns {boolean}
 */
export function reachedThroughCollection(library, item) {
  if (item?.kind === 'collection') {
    return false;
  }

  return (library?.collections || []).some(
    (collection) =>
      collection?.source === 'own' &&
      (collection.templateIds || []).includes(item?.id) &&
      (collection.serverWide || sharedWith(collection).length > 0),
  );
}

/**
 * What the page says once the Share dialog saved: whom the items were
 * shared with, whether they are server-wide now, and, for one item, when
 * only its owner can use it now.
 *
 * @param {object[]} targets the items shared
 * @param {{add: string[], remove: string[], publish: boolean|null}} change
 *   see templateShareChange
 * @param {object} [options]
 * @param {object[]} [options.rows] the one item's people, as the dialog
 *   left them
 * @param {boolean} [options.reached] other people still reach the one item
 *   through a collection (see reachedThroughCollection)
 * @returns {string}
 */
export function templateSharedMessage(
  targets,
  { add, remove, publish },
  { rows = [], reached = false } = {},
) {
  const subject = shareSubject(targets);
  const parts = [];

  if (add.length) {
    parts.push(`Shared ${subject} with ${describeNames(add)}.`);
  }

  if (publish === true) {
    parts.push(`Published ${subject} server-wide.`);
  } else if (publish === false) {
    parts.push(`Removed ${subject} from server-wide.`);
  }

  if (targets.length === 1) {
    const kept = rows.filter((row) => !row.stale && !row.removed).length;
    const wide = publish === null ? Boolean(targets[0].serverWide) : publish;
    // Only people who had it: a share whose account was removed gave no
    // one anything.
    const stopped = remove.filter((user) =>
      rows.some((row) => row.user === user && !row.stale),
    );

    if (
      (remove.length || publish === false) &&
      kept + add.length === 0 &&
      !wide &&
      !reached
    ) {
      parts.push(`Only you can use ${subject} now.`);
    } else if (stopped.length) {
      parts.push(`Stopped sharing ${subject} with ${describeNames(stopped)}.`);
    }
  }

  return parts.join(' ') || `Saved sharing for ${subject}.`;
}

/**
 * Why the server left items as they were, a sentence each.
 *
 * @param {object[]} targets the items shared
 * @param {{kind: string, id: string, reason: string}[]} failed see
 *   readLibraryResult in api.js
 * @param {number} [max] the most people an item is shared with
 * @returns {string[]}
 */
export function templateShareFailures(targets, failed, max = MAX_SHARES) {
  return failed.map(({ kind, id, reason }) => {
    const item = targets.find(
      (target) =>
        target.id === id &&
        (target.kind === 'collection' ? 'collection' : 'template') === kind,
    );
    const name = item?.name || id;

    switch (reason) {
      case 'too-many':
        return sharedWith(item).length >= max
          ? `${name} is already shared with ${max} people, the most allowed.`
          : `Adding these people would share ${name} with more than ${max} people, the most allowed.`;
      case 'not-found':
        return `${name} is no longer in your library.`;
      default:
        return `${name} could not be changed.`;
    }
  });
}

/**
 * Why the server refused a person the Share dialog adds (a 422 reason
 * code), in words.
 *
 * @param {string} code
 * @param {string} user
 * @param {object[]} targets the items shared
 * @param {number} [max] the most people one request adds
 * @returns {string}
 */
export function templateShareReason(code, user, targets, max = MAX_SHARES) {
  switch (code) {
    case 'owner':
      return ownedText(targets);
    case 'too-many':
      return `At most ${max} people can be added at once.`;
    default:
      return reasonMessage(code, user);
  }
}

// --- other users' templates ------------------------------------------------

// The most templates a library holds (api/builder), until the server says.
const MAX_LIBRARY_TEMPLATES = 200;

/**
 * Why templates cannot be copied into the user's library, or '' when they
 * can: a library holds so many templates at most.
 *
 * @param {object|null} library the library, as the store keeps it
 * @param {number} adding how many templates the copy adds
 * @returns {string}
 */
export function copyProblem(library, adding) {
  const max = library?.limits?.templates ?? MAX_LIBRARY_TEMPLATES;
  const own = (library?.items || []).filter(
    (template) => template?.source === 'own',
  ).length;

  return own + adding > max
    ? `Your library holds ${max} templates, the most it can. Delete some first.`
    : '';
}

/**
 * What the page says once templates of other users were copied into the
 * user's library.
 *
 * @param {object[]} templates the templates copied
 * @param {object} [collection] the collection copied with them
 * @returns {string}
 */
export function copiedMessage(templates, collection = null) {
  if (collection) {
    return `Copied collection ${collection.name} to your library.`;
  }

  return templates.length === 1
    ? `Copied ${templates[0].name} to your library.`
    : `Copied ${count(templates.length, 'template')} to your library.`;
}

/**
 * What taking other users' items back from server-wide does, for its
 * confirmation.
 *
 * @param {object[]} items templates or collections, as listed (kind
 *   'collection' for a collection)
 * @returns {{title: string, message: string, confirmLabel: string}}
 */
export function unpublishQuestion(items) {
  if (items.length === 1) {
    const [item] = items;

    return {
      title: `Remove ${item.name} from server-wide?`,
      message: `It stays in ${item.owner}'s library. Other people can no longer use it unless it is shared with them.`,
      confirmLabel: 'Remove',
    };
  }

  return {
    title: `Remove ${shareSubject(items)} from server-wide?`,
    message:
      "They stay in their owners' libraries. Other people can no longer use them unless they are shared with them.",
    confirmLabel: 'Remove',
  };
}
