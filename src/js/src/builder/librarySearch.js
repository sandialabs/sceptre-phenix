// What the command palette finds in the template and icon libraries: templates
// (t:), collections (c:) and icons (i:). See matchItem in fuzzy.js.

import { count } from './announce.js';
import { iconLabel } from './iconLabels.js';
import { iconOwnerText, iconSizeText } from './icons.js';
import { matchedLayer } from './layerMatchers.js';
import { isLayerId, layerLabel, placeholderLabel } from './model.js';
import {
  builtinTemplates,
  collectionNames,
  collectionShowValue,
  isItem,
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

// The {label, value} fields of a choice, from [label, value or values] pairs.
// It drops empty and repeated values.
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

// What finds a template beside its name. A template with no layer of its own
// is at the layer of `about.layers` (the open diagram's) that its fields match.
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

// The templates Add device can use: each template of the Nodes & Templates
// sections once, under its first section. `value` is the templateKey.
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

// The library's templates for the drafts page: the library's, the server's,
// then the built-in ones, each once. `value` is the template as listed.
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
      const [image] = templateImages(template);

      return {
        id: templateKey(template.source, template.id),
        ...templateSearch(template, library),
        detail: templateDetail(TEMPLATE_SOURCES[template.source], image),
        icon: template.device?.iconKey || 'server',
        value: template,
      };
    });
}

/**
 * What finds a template of the library, the server or phenix: its name as the
 * title, and the fields that t: in the palette searches.
 */
export function templateSearch(template, library) {
  return {
    title: template?.name || template?.id || '',
    fields: templateFields(template, {
      source: TEMPLATE_SOURCES[template?.source] || '',
      collections: collectionNames(library, template),
    }),
  };
}

/** What finds a layer set: its name as the title, and its description. */
export function layerSetSearch(set) {
  return {
    title: set?.name || set?.id || '',
    fields: fieldsOf([['Description', set?.description]]),
  };
}

// The library's collections, each once: the library's, then the server's.
// `value` is the Show field value that shows it (collectionShowValue).
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

/** The icon library's icons as choices. `value` is the icon's name. */
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
