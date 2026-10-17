// What the command palette finds in the template library and the icon
// library: node templates (t:), template collections (c:) and custom icons
// (i:), as choices of the commands Find template, Find collection and Find
// icon (commands.js). A plain search also lists a few of each.
//
// Each choice has `fields`, each {label, value}: what else finds it beside
// its name, so the palette can say which one matched ("Updated by: bob",
// "In collection: Pumps"). See matchItem in fuzzy.js.
//
// Nothing here knows about Vue or the server.

import { count } from './announce.js';
import { iconLabel } from './iconLabels.js';
import { iconOwnerText, iconSizeText } from './icons.js';
import { matchedLayer } from './layerMatchers.js';
import { isLayerId, layerLabel, placeholderLabel } from './model.js';
import {
  builtinTemplates,
  collectionNames,
  collectionShowValue,
  shownList,
  templateKey,
} from './templates.js';

// What the Node Templates tab calls the list of each source of templates.
const TEMPLATE_SOURCES = {
  library: 'Library',
  preloaded: 'Server',
  builtin: 'Built-in',
};

// What the Node Templates tab's Show field calls the collections of each
// source.
const COLLECTION_SOURCES = {
  library: 'Library collections',
  preloaded: 'Server',
};

function isItem(value) {
  return Boolean(value) && typeof value === 'object';
}

/**
 * The fields of a choice, from [label, value or values] pairs. Empty and
 * repeated values are left out.
 *
 * @param {[string, *][]} pairs
 * @returns {{label: string, value: string}[]}
 */
function fieldsOf(pairs) {
  const seen = new Set();

  return pairs.flatMap(([label, values]) =>
    [values]
      .flat()
      .filter((value) => typeof value === 'string' || Number.isFinite(value))
      .map((value) => String(value).trim())
      .filter((value) => {
        const key = `${label}\u0000${value}`;
        const fresh = value !== '' && !seen.has(key);

        seen.add(key);

        return fresh;
      })
      .map((value) => ({ label, value })),
  );
}

// The drive images of a template's device.
function templateImages(template) {
  const drives = template?.device?.spec?.hardware?.drives;

  return Array.isArray(drives)
    ? drives
        .map((drive) => drive?.image)
        .filter((image) => typeof image === 'string' && image)
    : [];
}

/**
 * What finds a template beside its name: its description, icon, custom
 * icon, layer ("Layer 3", and the label the diagram gives it), node type,
 * hostname, images, the collections that hold it, who created and updated
 * it, and its source.
 *
 * @param {object} template as a diagram or the library lists it
 * @param {object} [about]
 * @param {string} [about.source] the name of its list ("Library")
 * @param {string[]} [about.collections] the names of its collections
 * @param {object[]} [about.layers] the layers of the open diagram (its
 *   `layers`), which label the template's layer. A template with no layer
 *   of its own is at the layer its fields match there.
 * @param {string} [about.matchConflict] that diagram's match conflict rule
 * @returns {{label: string, value: string}[]}
 */
export function templateFields(
  template,
  { source = '', collections = [], layers = [], matchConflict = '' } = {},
) {
  const device = isItem(template?.device) ? template.device : {};
  const spec = isItem(device.spec) ? device.spec : {};
  const layerId = isLayerId(device.layer)
    ? device.layer
    : matchedLayer(device, layers, matchConflict)?.id;
  const layer = isLayerId(layerId)
    ? [placeholderLabel(layerId), layerLabel({ layers }, layerId)]
    : [];

  return fieldsOf([
    ['Description', template?.description],
    // The label, then the key: "redhat" also finds a Red Hat template.
    ['Icon', device.iconKey ? [iconLabel(device.iconKey), device.iconKey] : []],
    ['Custom icon', device.icon],
    ['Layer', layer],
    ['Type', spec.type],
    ['Hostname', spec.general?.hostname],
    ['Image', templateImages(template)],
    ['In collection', collections],
    ['Created by', template?.createdBy],
    ['Updated by', template?.updatedBy],
    ['Source', source],
  ]);
}

// A template's second line: its list and its image.
function templateDetail(list, image) {
  return [list, image || 'no image'].filter(Boolean).join(' · ');
}

/**
 * The templates the editor can add a device from: those of the sections of
 * Nodes & Templates (see paletteTemplateGroups in templates.js), each once,
 * under its first section. A choice's `value` is the template's key (see
 * templateKey), as Add device takes it.
 *
 * @param {object} store the Builder store: paletteTemplateGroups and
 *   templates (the library)
 * @returns {object[]} choices
 */
export function editorTemplateChoices(store) {
  const library = store.templates || null;
  const seen = new Set();

  return (store.paletteTemplateGroups || []).flatMap((group) =>
    group.entries
      .filter((entry) => {
        const first = !seen.has(entry.key);

        seen.add(entry.key);

        return first;
      })
      .map((entry) => {
        const { template } = entry;

        return {
          id: entry.key,
          title: entry.name,
          detail: templateDetail(group.label, entry.image),
          icon: entry.iconKey || 'server',
          fields: templateFields(template, {
            source: group.label,
            collections:
              entry.source === 'diagram'
                ? []
                : collectionNames(library, template),
            layers: store.doc?.layers,
            matchConflict: store.doc?.layerSet?.matchConflict,
          }),
          value: entry.key,
        };
      }),
  );
}

/**
 * The templates of the library, for the drafts page: those of the library,
 * then the server's, then the built-in templates, each once. A choice's
 * `value` is the template as listed.
 *
 * @param {object|null} library the library, as the store keeps it
 * @returns {object[]} choices
 */
export function libraryTemplateChoices(library) {
  const items = Array.isArray(library?.items)
    ? library.items.filter(isItem)
    : [];
  const seen = new Set();

  return [...items, ...builtinTemplates(library)]
    .filter((template) => {
      const key = templateKey(template.source, template.id);
      const first =
        Object.hasOwn(TEMPLATE_SOURCES, template.source) && !seen.has(key);

      seen.add(key);

      return first;
    })
    .map((template) => {
      const list = TEMPLATE_SOURCES[template.source];
      const [image] = templateImages(template);

      return {
        id: templateKey(template.source, template.id),
        title: template.name || template.id,
        detail: templateDetail(list, image),
        icon: template.device?.iconKey || 'server',
        fields: templateFields(template, {
          source: list,
          collections: collectionNames(library, template),
        }),
        value: template,
      };
    });
}

/**
 * The collections of the library, each once: those of the library, then
 * the server's. A choice's `value` is the Show field's value that shows it
 * (see collectionShowValue in templates.js).
 *
 * @param {object|null} library the library, as the store keeps it
 * @returns {object[]} choices
 */
export function collectionChoices(library) {
  const collections = Array.isArray(library?.collections)
    ? library.collections.filter(isItem)
    : [];
  const seen = new Set();

  return collections
    .filter((collection) => {
      const key = templateKey(collection.source, collection.id);
      const first =
        Object.hasOwn(COLLECTION_SOURCES, collection.source) && !seen.has(key);

      seen.add(key);

      return first;
    })
    .map((collection) => {
      const value = collectionShowValue(collection);
      const list = COLLECTION_SOURCES[collection.source];
      const templates = shownList(library, value)?.templates || [];

      return {
        id: templateKey(collection.source, collection.id),
        title: collection.name || collection.id,
        detail: [list, count(templates.length, 'template')].join(' · '),
        icon: 'library',
        fields: fieldsOf([
          ['Description', collection.description],
          ['Template', templates.map((template) => template.name)],
          ['Created by', collection.createdBy],
          ['Updated by', collection.updatedBy],
          ['Source', list],
        ]),
        value,
      };
    });
}

/**
 * The icons of the icon library. A choice's `value` is the icon's name.
 *
 * @param {object[]} icons as the icon library lists them (iconLibrary.js)
 * @returns {object[]} choices
 */
export function iconChoices(icons) {
  return (Array.isArray(icons) ? icons : [])
    .filter((icon) => isItem(icon) && typeof icon.name === 'string')
    .map((icon) => {
      const owner = iconOwnerText(icon);
      const size = iconSizeText(icon);

      return {
        id: icon.name,
        title: icon.name,
        detail: [owner, size].filter(Boolean).join(' · '),
        icon: 'image',
        fields: fieldsOf([
          ['Also named', icon.aliases || []],
          ['Size', size],
          ['Source', owner],
          ['Created by', icon.createdBy],
          ['Updated by', icon.updatedBy],
        ]),
        value: icon.name,
      };
    });
}
