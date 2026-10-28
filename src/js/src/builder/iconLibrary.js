// The server's shared icon library (web/builder_icons.go): a list without
// images, with images read on lookup and kept by their hash ID.
// The Custom icons dialog gets the library through the Inspector, so the
// Inspector's renderers stay free of the API client.

import { shallowReactive } from 'vue';

import {
  builderApi,
  classifyError,
  endsBulk,
  errorMessage,
  serverSentence,
} from './api.js';
import { holdsImage, iconImageId, indexIcons, sortIcons } from './icons.js';
import { isPlainObject } from './values.js';

// How many images are read at once.
const IMAGE_READS = 4;

// Why a delete of an icon that changed after the list was read (412) was
// not made.
const ICON_CHANGED =
  'The icon changed after the Builder read the list. The list now shows it as it is. Check it, and then try again.';

/**
 * The message for a failed library request. The server's refusal is shown
 * word for word, because the server writes it to be shown.
 */
export function iconLibraryFailure(error) {
  const kind = classifyError(error);

  if (error?.response?.status === 412) {
    return ICON_CHANGED;
  }

  return ['unauthenticated', 'offline'].includes(kind)
    ? errorMessage(kind, error)
    : serverSentence(error) || errorMessage(kind, error);
}

// An icon as the list holds it: all but its image.
function listed(icon) {
  const entry = { ...icon };

  delete entry.data;
  delete entry.pending;

  return entry;
}

// Whether an icon is the one that `name` names: its name, ignoring case.
function named(icon, name) {
  return (
    typeof name === 'string' && icon.name.toLowerCase() === name.toLowerCase()
  );
}

/**
 * The icon library: reactive `state`, and load, ensure, lookup,
 * ensureImages, upload, rename, remove, failure and ends. Each change takes
 * the server's answer into the list, which is not read again.
 */
export function createIconLibrary(api = builderApi) {
  const state = shallowReactive({
    status: 'idle',
    error: '',
    loaded: false,
    icons: [],
    index: new Map(),
    maxIcons: 0,
  });

  // The images read, by icon ID: the base64 data, or null for one that
  // could not be read. A lookup reads it, so what draws an icon is drawn
  // again when its image comes.
  const images = shallowReactive(new Map());

  // What lookup gives for each listed icon, made once for each state of its
  // image, so that a lookup gives the same object until the image changes.
  const entries = new WeakMap();

  // The tag of the list as last read, which the next read names.
  let etag = '';

  // The reads asked for: an answer older than the last one asked for is
  // dropped.
  let reads = 0;
  let pending = null;

  // The images waiting to be read, those being read by ID, and how many
  // requests are out.
  const waiting = [];
  const reading = new Map();
  let active = 0;

  function setIcons(icons) {
    state.icons = sortIcons(icons);
    state.index = indexIcons(state.icons);
  }

  // Keeps the image of an icon that an answer carries, when it is the image
  // of the icon's ID.
  function keepImage(icon) {
    if (
      typeof icon?.id === 'string' &&
      typeof icon.data === 'string' &&
      iconImageId(icon.data) === icon.id
    ) {
      images.set(icon.id, icon.data);
    }
  }

  function readNext() {
    while (active < IMAGE_READS && waiting.length > 0) {
      const { icon, done } = waiting.shift();

      active += 1;

      api
        .getIcon(icon.name)
        .then((read) => keepImage(read))
        .catch(() => {})
        .finally(() => {
          if (!images.has(icon.id)) {
            images.set(icon.id, null);
          }

          active -= 1;
          reading.delete(icon.id);
          done();
          readNext();
        });
    }
  }

  // Reads the image of a listed icon, once. Resolves when it is read or
  // could not be.
  function readImage(icon) {
    if (!icon?.id || images.has(icon.id)) {
      return Promise.resolve();
    }

    if (!reading.has(icon.id)) {
      reading.set(
        icon.id,
        new Promise((done) => {
          waiting.push({ icon, done });
        }),
      );
      readNext();
    }

    return reading.get(icon.id);
  }

  // What lookup gives for a listed icon. An icon given with its image is
  // that icon.
  function entryOf(icon) {
    if (typeof icon.data === 'string') {
      return icon;
    }

    const data = images.get(icon.id);
    const made = entries.get(icon);

    if (made && made.data === data) {
      return made.entry;
    }

    let entry = icon;

    if (typeof data === 'string') {
      entry = { ...icon, data };
    } else if (data === undefined) {
      entry = { ...icon, pending: true };
      readImage(icon);
    }

    entries.set(icon, { data, entry });

    return entry;
  }

  function find(name) {
    return typeof name === 'string'
      ? state.index.get(name.toLowerCase()) || null
      : null;
  }

  // Puts an icon that the server answered a change with into the list, in
  // place of the icon `replaced` names and of one of its own name. The list
  // is no longer the one the tag names, so the next load reads it in full.
  function put(icon, replaced = '') {
    etag = '';
    keepImage(icon);
    setIcons([
      ...state.icons.filter(
        (entry) => !named(entry, icon.name) && !named(entry, replaced),
      ),
      listed(icon),
    ]);
  }

  // Takes an icon out of the list, as put does.
  function drop(name) {
    etag = '';
    setIcons(state.icons.filter((entry) => !named(entry, name)));
  }

  async function load() {
    const read = ++reads;

    state.status = 'loading';

    const request = (async () => {
      try {
        const answer = await api.listIcons(etag);

        if (read === reads) {
          if (!answer.notModified) {
            setIcons(answer.icons);
            state.maxIcons = answer.maxIcons;
            etag = answer.etag || '';
          }

          // An image that could not be read is read again when drawn.
          for (const [id, data] of images) {
            if (data === null) {
              images.delete(id);
            }
          }

          state.error = '';
          state.loaded = true;
          state.status = 'ready';
        }

        return state;
      } catch (error) {
        if (read === reads) {
          state.error = iconLibraryFailure(error);
          state.status = 'failed';
        }

        throw error;
      } finally {
        if (read === reads) {
          pending = null;
        }
      }
    })();

    pending = request;

    return request;
  }

  function ensure() {
    if (pending) {
      return pending;
    }

    return state.status === 'ready' ? Promise.resolve(state) : load();
  }

  return {
    state,
    load,
    ensure,
    lookup(name) {
      const icon = find(name);

      return icon ? entryOf(icon) : null;
    },
    async ensureImages(names) {
      await ensure().catch(() => {});
      await Promise.all([...names].map((name) => readImage(find(name))));
    },
    async upload(icon) {
      const answer = await api.uploadIcon(icon);

      put(answer.icon);

      return answer;
    },
    async rename(name, newName) {
      const before = find(name);
      const icon = await api.renameIcon(name, newName);

      put(icon, before?.name);

      return icon;
    },
    async remove(icon) {
      try {
        await api.deleteIcon(icon.name, icon.etag);
      } catch (error) {
        const status = error?.response?.status;

        if (status === 404) {
          drop(icon.name);
        } else if (status === 412) {
          await load().catch(() => {});
        }

        throw error;
      }

      drop(icon.name);

      return true;
    },
    failure: iconLibraryFailure,
    ends: endsBulk,
  };
}

export const iconLibrary = createIconLibrary();

// The shared part of ingestIcons and ingestTemplateIcons. Each copy whose
// name the library does not have is uploaded, and the warnings tell what
// happened to the others. Returns the copies to keep, the warnings, and
// whether the library could not be read at all. In that case nothing was
// uploaded, and every copy is kept.
async function ingestCopies(carried, library, words) {
  try {
    await library.load();
  } catch (error) {
    return {
      kept: carried,
      warnings: [
        words.unreadable(Object.keys(carried).length, library.failure(error)),
      ],
      unread: true,
    };
  }

  const kept = {};
  const warnings = [];

  for (const [name, entry] of Object.entries(carried)) {
    const shared = library.lookup(name);

    if (shared) {
      if (!holdsImage(shared, entry?.data)) {
        kept[name] = entry;
        warnings.push(words.differs(name));
      }

      continue;
    }

    try {
      const { icon } = await library.upload({ name, data: entry?.data });

      if (icon?.data !== entry?.data) {
        kept[name] = entry;
      }
    } catch (error) {
      kept[name] = entry;
      warnings.push(words.refused(name, library.failure(error)));
    }
  }

  return { kept, warnings, unread: false };
}

/**
 * Puts the custom icons that an uploaded document carries into the icon
 * library. A copy stays in the draft only when its upload is refused or a
 * library icon of that name has different bytes, each with a warning.
 */
export async function ingestIcons(doc, library = iconLibrary) {
  const carried = isPlainObject(doc?.icons) ? doc.icons : null;

  if (!carried || Object.keys(carried).length === 0) {
    return { doc, warnings: [] };
  }

  const { kept, warnings, unread } = await ingestCopies(carried, library, {
    unreadable: (count, reason) =>
      `The Builder could not read the server's icon library. The diagram keeps its ${count === 1 ? 'custom icon' : `${count} custom icons`}. ${reason}`,
    differs: (name) =>
      `The server already has an icon named ${name} that differs from this diagram's. The diagram keeps its own copy, which it shows in place of the server's.`,
    refused: (name, reason) =>
      `The Builder could not add custom icon ${name} to the server's icon library: ${reason} The diagram keeps its own copy.`,
  });

  if (unread) {
    return { doc, warnings };
  }

  const next = { ...doc };

  delete next.icons;

  if (Object.keys(kept).length > 0) {
    next.icons = kept;
  }

  return { doc: next, warnings };
}

/**
 * Puts the custom icons that a template file carries into the icon library, by
 * the rules of ingestIcons.
 */
export async function ingestTemplateIcons(icons, library = iconLibrary) {
  if (
    !icons ||
    typeof icons !== 'object' ||
    Array.isArray(icons) ||
    !Object.keys(icons).length
  ) {
    return [];
  }

  const { warnings } = await ingestCopies(icons, library, {
    unreadable: (count, reason) =>
      `The Builder could not read the server's icon library. It did not add ${count === 1 ? 'the custom icon' : `the ${count} custom icons`} of this file to the library. The templates that name ${count === 1 ? 'it' : 'them'} show their built-in icon until the server has ${count === 1 ? 'it' : 'them'}. ${reason}`,
    differs: (name) =>
      `The server already has an icon named ${name} that differs from this file's. The imported templates show the server's icon.`,
    refused: (name, reason) =>
      `The Builder could not add custom icon ${name} to the server's icon library: ${reason} The imported templates that name it show their built-in icon.`,
  });

  return warnings;
}

/**
 * The diagram's copy of the custom icon that a template names, by name, when
 * the diagram has one.
 */
export function savedTemplateIcons(template, icons) {
  const name = template?.device?.icon;

  return name && isPlainObject(icons) && Object.hasOwn(icons, name)
    ? { [name]: icons[name] }
    : {};
}

/**
 * Puts a template's custom icon copies into the icon library before the
 * template is saved, by the rules of ingestIcons. The diagram keeps its copies.
 */
export async function ingestSavedTemplateIcons(icons, library = iconLibrary) {
  if (!icons || !Object.keys(icons).length) {
    return [];
  }

  const { warnings } = await ingestCopies(icons, library, {
    unreadable: (count, reason) =>
      `The Builder could not read the server's icon library. It did not add ${count === 1 ? 'the custom icon' : `the ${count} custom icons`} of this diagram to the library. The saved template shows its built-in icon until the server has ${count === 1 ? 'it' : 'them'}. ${reason}`,
    differs: (name) =>
      `The server already has an icon named ${name} that differs from this diagram's. The saved template shows the server's icon.`,
    refused: (name, reason) =>
      `The Builder could not add custom icon ${name} to the server's icon library: ${reason} The saved template shows its built-in icon.`,
  });

  return warnings;
}

/**
 * Puts the custom icons that a layer set names into the icon library before
 * the set is stored, by the rules of ingestIcons.
 */
export async function ingestLayerSetIcons(
  icons,
  library = iconLibrary,
  { from = 'file' } = {},
) {
  if (
    !icons ||
    typeof icons !== 'object' ||
    Array.isArray(icons) ||
    !Object.keys(icons).length
  ) {
    return [];
  }

  const source = from === 'diagram' ? 'diagram' : 'file';
  const { warnings } = await ingestCopies(icons, library, {
    unreadable: (count, reason) =>
      `The Builder could not read the server's icon library. It did not add ${count === 1 ? 'the custom icon' : `the ${count} custom icons`} of this ${source} to the library. The layer set shows no icon for ${count === 1 ? 'it' : 'them'} until the server has ${count === 1 ? 'it' : 'them'}. ${reason}`,
    differs: (name) =>
      `The server already has an icon named ${name} that differs from this ${source}'s. The layer set shows the server's icon.`,
    refused: (name, reason) =>
      `The Builder could not add custom icon ${name} to the server's icon library: ${reason} The layer set shows no icon for it.`,
  });

  return warnings;
}
