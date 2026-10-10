// The server's icon library, which every user shares (web/builder_icons.go),
// as the Builder keeps it: a list read when the Builder opens and read again
// after every upload, rename and delete made here, and an index of it by
// every name and alias, in lower case, which draws the icons nodes name (see
// iconSrc in icons.js).
//
// The Custom icons dialog is given the library through the Inspector
// (INSPECTOR_ICON_LIBRARY in components/builder/inspector/control.js) and
// never imports the API client itself: the Inspector's renderers stay free
// of it, as the form adapter that lists them is.

import { shallowReactive } from 'vue';

import {
  builderApi,
  classifyError,
  errorMessage,
  serverSentence,
} from './api.js';
import { indexIcons, sortIcons } from './icons.js';

/**
 * What a failed library request says. The server's refusals of an icon are
 * written to be shown ("icon name "plc" is taken by an icon alice uploaded;
 * choose another name"), so its message is used word for word, as a
 * sentence. A session that ended and a server that cannot be reached say
 * what they say everywhere else.
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
 * @returns {object} the library: `state` (reactive: status 'idle',
 *   'loading', 'ready' or 'failed', error, icons as the server lists them,
 *   index, and the caller's usage maxIcons, maxBytes, usedIcons and
 *   usedBytes); load() reads it; ensure() reads it unless it was read or is
 *   being read; lookup(name) finds an icon by its name or an alias,
 *   ignoring case; upload({name, data}, {refresh}) adds a PNG, as
 *   uploadIcon answers; rename(name, newName) and remove(name) rename and
 *   delete an icon; failure(error) says why one of them failed. Each change
 *   reads the library again.
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
    async remove(name) {
      await api.deleteIcon(name);
      await load().catch(() => {});

      return true;
    },
    failure: iconLibraryFailure,
  };
}

export const iconLibrary = createIconLibrary();

// What ingestIcons and ingestTemplateIcons share: each copy whose name the
// library lacks is uploaded, and words say what became of the others.
// Returns the copies to keep, the warnings, and whether the library could
// not be read at all (then nothing was uploaded, and every copy is kept).
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
 * Puts the custom icons an uploaded document carries into the icon library,
 * so the draft made from it carries none it need not: for each copy, a
 * library icon of that name (or alias) with the same bytes means the copy
 * goes; no icon of that name means the copy is uploaded under it, as the
 * user, and goes; a refused upload (no permission, the library full) keeps
 * the copy, with a warning; and a library icon of that name with other
 * bytes keeps the copy, which then wins in the draft, with a warning that
 * names it.
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
 * Puts the custom icons a template file carries into the icon library, by
 * the rules of an upload (see ingestIcons): a library icon of that name with
 * the same bytes is the icon; no icon of that name means the copy is
 * uploaded under it, as the user. A library icon of that name with other
 * bytes, and a refused upload, each give a warning: templates of a library
 * carry no icons, so the imported templates show the server's icon, or their
 * built-in one.
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
