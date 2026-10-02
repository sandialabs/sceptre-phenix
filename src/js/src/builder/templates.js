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
import {
  addNode,
  createDocument,
  networkByName,
  templateDevice,
} from './model.js';
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
 * and a handle for each interface its spec names. The device is named after
 * the template's hostname, which addNode makes unique.
 *
 * An interface's VLAN follows the rule of a paste (see pasteClipboard): one
 * that names a network of `doc` is emptied, since the new device is
 * connected to nothing; any other text is kept.
 *
 * A template's custom icon is an icon id: the document comes to carry the
 * icon when the device is committed, from its own icons or from the store's
 * icon shelf (see settleIcons).
 *
 * @param {object} template
 * @param {object} doc the document the device is for
 * @returns {object} addNode options, less the position
 */
export function nodeOptionsFromTemplate(template, doc) {
  const { spec: _, ...look } = templateDevice(template?.device);
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
 * exactly one device, made from the template as a diagram makes one, and
 * the custom icon the template names. It has no networks, so no VLAN is
 * emptied. Its name and description are the template's.
 *
 * @param {object} template
 * @param {Map<string, object>|object|null} [icons] custom icons by id,
 *   {name?, data}: those kept beside the template (a diagram's `icons`)
 * @returns {object} document
 */
export function templateDocument(template, icons = null) {
  const empty = {
    ...createDocument(),
    name: template?.name || '',
    description: template?.description || '',
  };
  const { doc } = addNode(empty, nodeOptionsFromTemplate(template, empty));

  return settleIcons(doc, icons).doc;
}

/**
 * Reads a template back from the template editor's document (see
 * templateDocument): its one device, as typed, with the document's name and
 * description, and the custom icon it names.
 *
 * @param {object} doc
 * @returns {{template: object, icons: object}} the template, without an
 *   id, and the icons it names, by icon id: {name?, data}
 */
export function templateFromDocument(doc) {
  const node = (doc?.nodes || []).find((entry) => entry.kind === 'device');
  const template = templateFromNode(node, {
    name: doc?.name || '',
    description: doc?.description || '',
  });
  const icons = {};
  const id = template.device.icon;

  if (id && doc?.icons && Object.hasOwn(doc.icons, id)) {
    icons[id] = clone(doc.icons[id]);
  }

  return { template, icons };
}

/**
 * What the Inspector edits in the template editor, in place of the Builder
 * store (see the `host` prop of BuilderInspector.vue): a document of the
 * template's one device, which is always the selection. A commit replaces
 * the document, which then carries the custom icon it names, as a commit of
 * the store does. The schema, the disk images and the icon shelf are the
 * Builder store's; the actions of a canvas do nothing.
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
 * @returns {object}
 */
export function templateEditorHost({
  doc,
  source,
  announce,
  readOnly = false,
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
    get iconShelf() {
      return source.iconShelf;
    },
    shelveIcons(icons) {
      source.shelveIcons(icons);
    },
    commit(next) {
      this.doc = settleIcons(next, source.iconShelf).doc;

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
 * with the user or published server-wide, and "builtin:<id>" for a
 * built-in one.
 *
 * @param {string} source 'diagram', 'own', 'shared', 'server' or 'builtin'
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
      return items.find(
        (template) => template.source === 'own' && template.id === id,
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

/**
 * The custom icon a template names, as the entry kept beside it holds it,
 * for the place the template is copied to: a diagram, or the library.
 *
 * @param {object} template
 * @param {object|null} icons the icons kept where the template is, by icon
 *   id: {name?, data}
 * @returns {object} the one entry the template names, by its id; empty
 *   when it names none, or one that is not there
 */
export function templateIcons(template, icons) {
  const id = template?.device?.icon;

  return id && icons && Object.hasOwn(icons, id) ? { [id]: icons[id] } : {};
}

// --- the palette -----------------------------------------------------------

// The palette's groups of templates, in the order it lists them.
export const TEMPLATE_GROUP_LABELS = {
  diagram: 'This diagram',
  own: 'My library',
  shared: 'Shared with me',
  server: 'Server-wide',
  builtin: 'Built-in',
};

// The first drive image of a template's device, which its command names.
function templateImage(template) {
  const image = template?.device?.spec?.hardware?.drives?.[0]?.image;

  return typeof image === 'string' ? image : '';
}

// What a palette entry's tooltip says: the template's description, and for
// another user's template, whose it is.
function entryDescription(source, template) {
  const whose = {
    shared: `Shared by ${template.owner}.`,
    server: `Published by ${template.owner}.`,
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
    default:
      return `palette-template-${template.id}`;
  }
}

function paletteEntry(source, template) {
  return {
    key: templateKey(source, template.id, template.owner),
    source,
    id: template.id,
    testid: entryTestId(source, template),
    name: template.name,
    description: entryDescription(source, template),
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
 * them, and those published server-wide). While there is no library to
 * show (see libraryUse), the built-in templates stand in for it, so a
 * diagram can still be built; while its first read is under way, nothing
 * does. A group with no template is left out, and a template of the
 * library is listed once.
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
    use === 'ready' && Array.isArray(library.items) ? library.items : [];
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
  const groups = [
    { id: 'diagram', templates: doc?.templates },
    { id: 'own', templates: from('own') },
    { id: 'shared', templates: from('shared') },
    { id: 'server', templates: from('server') },
    { id: 'builtin', templates: use === 'missing' ? BUILTIN_TEMPLATES : [] },
  ];

  return groups
    .map(({ id, templates }) => ({
      id,
      label: TEMPLATE_GROUP_LABELS[id],
      entries: (Array.isArray(templates) ? templates : [])
        .filter((template) => template && typeof template === 'object')
        .map((template) => paletteEntry(id, template)),
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

// The people an item of the library is shared with now: a share whose
// account was removed gives no one anything.
function sharedWith(item) {
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
