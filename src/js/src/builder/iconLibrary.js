// The server's icon library, which all users share (web/builder_icons.go),
// as the Builder keeps it. It is a list, read when the Builder opens and read
// again after each upload, rename and delete made here. It also has an index
// of the list by every name and alias, in lower case, which draws the icons
// that nodes name (see iconSrc in icons.js).
//
// The Custom icons dialog gets the library through the Inspector
// (INSPECTOR_ICON_LIBRARY in components/builder/inspector/control.js). It
// never imports the API client itself. Thus the Inspector's renderers stay
// free of the client, as is the form adapter that lists them.

import { shallowReactive } from 'vue';

import {
  builderApi,
  classifyError,
  endsBulk,
  errorMessage,
  serverSentence,
} from './api.js';
import { indexIcons, sortIcons } from './icons.js';

/**
 * The message for a failed library request. The server writes its refusals
 * of an icon to be shown ("icon name "plc" is taken by an icon alice
 * uploaded; choose another name"). Thus this function uses the message word
 * for word, as a sentence. For a session that ended and a server that cannot
 * be reached, it gives the same message as everywhere else.
 *
 * @param {object} error axios-like error
 * @returns {string}
 */
export function iconLibraryFailure(error) {
  const kind = classifyError(error);

  return ['unauthenticated', 'offline'].includes(kind)
    ? errorMessage(kind, error)
    : serverSentence(error) || errorMessage(kind, error);
}

/**
 * Whether a library request was refused because the name is taken (409).
 *
 * @param {object} error axios-like error
 * @returns {boolean}
 */
export function nameTaken(error) {
  return error?.response?.status === 409;
}

/**
 * @param {object} [api] the Builder API client (see createBuilderApi)
 * @returns {object} the library:
 *   - `state` (reactive): status 'idle', 'loading', 'ready' or 'failed',
 *     error, icons as the server lists them, index, and the caller's usage
 *     maxIcons, maxBytes, usedIcons and usedBytes.
 *   - load() reads the library.
 *   - ensure() reads it unless it was read or a read is in progress.
 *   - lookup(name) finds an icon by its name or an alias, case-insensitive.
 *   - upload({name, data}, {refresh}) adds a PNG, with the uploadIcon
 *     response.
 *   - rename(name, newName) and remove(name, {refresh}) rename and delete an
 *     icon.
 *   - failure(error) tells why one of these failed. ends(error) tells
 *     whether that failure ends a batch of them (the session ended, or the
 *     server cannot be reached: endsBulk, as for the drafts' batches).
 *   Each change reads the library again, except an upload or a delete with
 *   refresh: false (one of a batch, which reads the library once at the end).
 */
export function createIconLibrary(api = builderApi) {
  const state = shallowReactive({
    status: 'idle',
    error: '',
    icons: [],
    index: new Map(),
    maxIcons: 0,
    maxBytes: 0,
    usedIcons: 0,
    usedBytes: 0,
  });

  // The reads asked for: an answer older than the last one asked for is
  // dropped.
  let reads = 0;
  let pending = null;

  async function load() {
    const read = ++reads;

    state.status = 'loading';

    const request = (async () => {
      try {
        const answer = await api.listIcons();

        if (read === reads) {
          state.icons = sortIcons(answer.icons);
          state.index = indexIcons(answer.icons);
          state.maxIcons = answer.maxIcons;
          state.maxBytes = answer.maxBytes;
          state.usedIcons = answer.usedIcons;
          state.usedBytes = answer.usedBytes;
          state.error = '';
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

  return {
    state,
    load,
    ensure() {
      if (pending) {
        return pending;
      }

      return state.status === 'ready' ? Promise.resolve(state) : load();
    },
    lookup(name) {
      return typeof name === 'string'
        ? state.index.get(name.toLowerCase()) || null
        : null;
    },
    async upload(icon, { refresh = true } = {}) {
      const answer = await api.uploadIcon(icon);

      if (refresh) {
        await load().catch(() => {});
      }

      return answer;
    },
    async rename(name, newName) {
      const icon = await api.renameIcon(name, newName);

      await load().catch(() => {});

      return icon;
    },
    async remove(name, { refresh = true } = {}) {
      await api.deleteIcon(name);

      if (refresh) {
        await load().catch(() => {});
      }

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
  let uploaded = false;

  for (const [name, entry] of Object.entries(carried)) {
    const shared = library.lookup(name);

    if (shared) {
      if (shared.data !== entry?.data) {
        kept[name] = entry;
        warnings.push(words.differs(name));
      }

      continue;
    }

    try {
      const { icon } = await library.upload(
        { name, data: entry?.data },
        { refresh: false },
      );

      uploaded = true;

      if (icon?.data !== entry?.data) {
        kept[name] = entry;
      }
    } catch (error) {
      kept[name] = entry;
      warnings.push(words.refused(name, library.failure(error)));
    }
  }

  if (uploaded) {
    await library.load().catch(() => {});
  }

  return { kept, warnings, unread: false };
}

/**
 * Puts the custom icons that an uploaded document carries into the icon
 * library, so the draft made from it carries only the copies it needs. For
 * each copy:
 *   - A library icon of that name (or alias) with the same bytes: the copy
 *     goes.
 *   - No icon of that name: the copy is uploaded under that name, as the
 *     user, and goes.
 *   - A refused upload (no permission, the library is full): the copy stays,
 *     with a warning.
 *   - A library icon of that name with different bytes: the copy stays and
 *     wins in the draft, with a warning that names it.
 *
 * @param {object} doc the uploaded document
 * @param {object} [library] the icon library (see createIconLibrary)
 * @returns {Promise<{doc: object, warnings: string[]}>} the document, the
 *   same object when it carries no copy
 */
export async function ingestIcons(doc, library = iconLibrary) {
  const carried =
    doc?.icons && typeof doc.icons === 'object' && !Array.isArray(doc.icons)
      ? doc.icons
      : null;

  if (!carried || Object.keys(carried).length === 0) {
    return { doc, warnings: [] };
  }

  const { kept, warnings, unread } = await ingestCopies(carried, library, {
    unreadable: (count, reason) =>
      `The server's icon library could not be read, so the diagram keeps its ${count === 1 ? 'custom icon' : `${count} custom icons`}. ${reason}`,
    differs: (name) =>
      `The server already has an icon named ${name} that differs from this diagram's. The diagram keeps its own copy, which it shows in place of the server's.`,
    refused: (name, reason) =>
      `Custom icon ${name} could not be added to the server's icon library: ${reason} The diagram keeps its own copy.`,
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
 * Puts the custom icons that a template file carries into the icon library,
 * by the rules of an upload (see ingestIcons). A library icon of that name
 * with the same bytes is the icon. If no icon has that name, the copy is
 * uploaded under that name, as the user. A library icon of that name with
 * different bytes, and a refused upload, each give a warning. Templates of a
 * library carry no icons, so the imported templates then show the server's
 * icon, or their built-in icon.
 *
 * @param {object|null|undefined} icons the file's copies, by icon name
 * @param {object} [library] the icon library (see createIconLibrary)
 * @returns {Promise<string[]>} the warnings
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
      `The server's icon library could not be read, so ${count === 1 ? 'the custom icon' : `the ${count} custom icons`} of this file ${count === 1 ? 'was' : 'were'} not added to it: the templates that name ${count === 1 ? 'it' : 'them'} show their built-in icon until the server has ${count === 1 ? 'it' : 'them'}. ${reason}`,
    differs: (name) =>
      `The server already has an icon named ${name} that differs from this file's. The imported templates show the server's icon.`,
    refused: (name, reason) =>
      `Custom icon ${name} could not be added to the server's icon library: ${reason} The imported templates that name it show their built-in icon.`,
  });

  return warnings;
}

/**
 * The diagram's copy (in its `icons`) of the custom icon that a template of
 * the diagram names, by name, when the diagram has one. Saving the template
 * to the user's library first puts this copy into the icon library (see
 * ingestSavedTemplateIcons). A template of a library carries no icon.
 *
 * @param {object} template
 * @param {object|null|undefined} icons the diagram's copies, by name
 * @returns {object} the copy by its name, or no entry
 */
export function savedTemplateIcons(template, icons) {
  const name = template?.device?.icon;

  return name &&
    icons &&
    typeof icons === 'object' &&
    !Array.isArray(icons) &&
    Object.hasOwn(icons, name)
    ? { [name]: icons[name] }
    : {};
}

/**
 * Puts the copies of custom icons that a template of the diagram names into
 * the icon library before the template is saved to the user's library. It
 * uses the rules of an upload (see ingestIcons). A library icon of that name
 * with the same bytes is the icon. If no icon has that name, the copy is
 * uploaded under that name, as the user. A library icon of that name with
 * different bytes, and a refused upload, each give a warning. The saved
 * template carries no icon, so it then shows the server's icon, or its
 * built-in icon. The diagram keeps its copies.
 *
 * @param {object} icons the copies, by icon name (see savedTemplateIcons)
 * @param {object} [library] the icon library (see createIconLibrary)
 * @returns {Promise<string[]>} the warnings
 */
export async function ingestSavedTemplateIcons(icons, library = iconLibrary) {
  if (!icons || !Object.keys(icons).length) {
    return [];
  }

  const { warnings } = await ingestCopies(icons, library, {
    unreadable: (count, reason) =>
      `The server's icon library could not be read, so ${count === 1 ? 'the custom icon' : `the ${count} custom icons`} of this diagram ${count === 1 ? 'was' : 'were'} not added to it: the saved template shows its built-in icon until the server has ${count === 1 ? 'it' : 'them'}. ${reason}`,
    differs: (name) =>
      `The server already has an icon named ${name} that differs from this diagram's. The saved template shows the server's icon.`,
    refused: (name, reason) =>
      `Custom icon ${name} could not be added to the server's icon library: ${reason} The saved template shows its built-in icon.`,
  });

  return warnings;
}
