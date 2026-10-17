// The Builder store's part for layer sets: the server's layer set library,
// the server's default set, and the actions that link a diagram's layers to a
// set. The store (store.js) spreads these into its state, getters and
// actions, so `this` is the store.
//
// The library belongs to the server (web/builder_layersets.go): every user
// of the Builder sees every set, and may add, change and delete any set but
// a built-in one. A diagram holds a copy of the layers of a set and a link
// to the set's ID and version (see withLayerSetCopy in model.js). When the
// library has a later version, the diagram offers to copy it again
// (updateFromLayerSet). Save to layer set writes the diagram's copy back to
// the set it is linked to, which takes the next version, and links the
// diagram to that version.

import {
  builderApi,
  classifyError,
  libraryErrorMessage,
  serverCode,
} from './api.js';
import { iconLibrary, ingestLayerSetIcons } from './iconLibrary.js';
import {
  layerSetContent,
  layerSetIconNames,
  parseLayerSetFile,
  uniqueLayerSetName,
} from './layerSetFile.js';
import {
  MATCH_CONFLICT_HIGHEST,
  effectiveLayers,
  layerSetContentOf,
  layerSetLink,
  layerSetOptions,
  layerName,
  setLayerSetOptions,
  updateLayer,
  withLayerSetCopy,
} from './model.js';
import { onBuilderSessionEnd } from './session.js';

/**
 * The layer set library as it is before it is read. status is 'idle'
 * before it is requested, then 'loading', then 'ready' or 'failed' by the
 * result of the last read, with why it failed (error). loaded says whether
 * the rest is what the server answered: the sets (items), the ID of the
 * server's default set (defaultId, null for none) and how many stored sets
 * the server cannot read (damaged). A read that fails keeps what an earlier
 * read answered.
 *
 * @returns {object}
 */
export function emptyLayerSets() {
  return {
    status: 'idle',
    error: '',
    loaded: false,
    items: [],
    defaultId: null,
    damaged: 0,
  };
}

/**
 * A change of the layer set library, or of a diagram's link to it, that was
 * not made, with why, in words. issues lists the problems of an uploaded
 * file or of a set the server refused, each {code, path, message}.
 */
export class LayerSetError extends Error {
  /**
   * @param {string} reason a sentence
   * @param {object[]} [issues]
   */
  constructor(reason, issues = []) {
    super(reason);
    this.name = 'LayerSetError';
    this.reason = reason;
    this.issues = issues;
  }
}

// The requested reads of the library, and the last read whose answer was
// kept. An answer older than the kept answer is dropped, and so is an
// answer to a read made before the session ended.
let reads = 0;
let readKept = 0;
let epoch = 0;

onBuilderSessionEnd(() => {
  epoch += 1;
});

// The words of a set in a message: "layer set Purdue Model".
function setName(set) {
  return `layer set ${set?.name || set?.id || ''}`.trim();
}

// The content of a set the server stores: a set as the library lists it,
// with fields of `changes` in place of its own.
function contentOf(set, changes = {}) {
  return layerSetContent({ ...set, ...changes });
}

// The diagram's copies of the custom icons that a content names: they go
// into the icon library before the content is saved to a set, which carries
// no icons.
function diagramIcons(doc, content) {
  const copies = doc?.icons;

  if (!copies || typeof copies !== 'object') {
    return {};
  }

  return Object.fromEntries(
    layerSetIconNames(content)
      .filter((name) => Object.hasOwn(copies, name))
      .map((name) => [name, copies[name]]),
  );
}

export const layerSetGetters = {
  /**
   * The server's default layer set, which new blank diagrams copy, while the
   * library lists it. null otherwise.
   *
   * @returns {object|null}
   */
  defaultLayerSet: (state) =>
    (state.layerSets.defaultId &&
      state.layerSets.items.find(
        (set) => set.id === state.layerSets.defaultId,
      )) ||
    null,

  /**
   * How the open diagram's layers stand to the layer set they are linked
   * to (see layerSetLink in model.js): unlinked, missing, update, changed
   * or linked. 'unknown' for linked layers while the library was not read.
   *
   * @returns {{status: string, set: object|null, source: object|null}}
   */
  layerSetLink: (state) => {
    const link = layerSetLink(state.doc, state.layerSets.items);

    return link.status !== 'unlinked' && !state.layerSets.loaded
      ? { ...link, status: 'unknown', set: null }
      : link;
  },
};

export const layerSetActions = {
  /**
   * Reads the layer set library and the server's default set.
   *
   * @returns {Promise<object>} the library state (layerSets)
   */
  async fetchLayerSets() {
    const read = (reads += 1);
    const started = epoch;

    this.layerSets = { ...this.layerSets, status: 'loading' };

    try {
      const library = await builderApi.listLayerSets();

      if (started === epoch && read > readKept) {
        readKept = read;
        this.layerSets = {
          status: read === reads ? 'ready' : 'loading',
          error: '',
          loaded: true,
          items: library.layerSets,
          defaultId: library.defaultId,
          damaged: library.damaged,
        };
      }
    } catch (error) {
      if (started === epoch && read === reads) {
        this.layerSets = {
          ...(classifyError(error) === 'forbidden'
            ? emptyLayerSets()
            : this.layerSets),
          status: 'failed',
          error: libraryErrorMessage(error),
        };
      }
    }

    return this.layerSets;
  },

  /**
   * Reads the library unless it was read already, so that what needs it
   * (a new blank diagram, the link of a diagram) can wait for it.
   *
   * @returns {Promise<object>} the library state
   */
  async layerSetsReady() {
    if (!this.layerSets.loaded) {
      await this.fetchLayerSets();
    }

    return this.layerSets;
  },

  /**
   * @param {string} id
   * @returns {object|null} the set of the library with that ID
   */
  findLayerSet(id) {
    return this.layerSets.items.find((set) => set.id === id) || null;
  },

  /**
   * Why a change of the layer set library failed, for the dialog or the
   * page that asked for it.
   *
   * @param {object} error what the change rejected with
   * @param {string} action what could not be done: "save the layer set"
   * @returns {string}
   */
  describeLayerSetError(error, action) {
    if (error instanceof LayerSetError) {
      return `Could not ${action}. ${error.reason}`;
    }

    if (serverCode(error) === 'layerset.stale') {
      return `Could not ${action}. Someone changed the layer set after it was read. Read it again, and then make your change again.`;
    }

    return `Could not ${action}. ${libraryErrorMessage(error)}`;
  },

  /**
   * Adds a set to the library, and reads the library again. Custom icons
   * that `icons` carries go into the icon library first. It rejects with
   * the failure, which the caller reports (see describeLayerSetError).
   *
   * @param {object} content {name, description?, iconKey?, icon?,
   *   matchConflict?, layerOutlines?, layers}
   * @param {{icons?: object, from?: 'file'|'diagram'}} [options]
   * @returns {Promise<{set: object, warnings: string[]}>} the set as stored
   */
  async createLayerSet(content, { icons = null, from = 'file' } = {}) {
    const warnings = await ingestLayerSetIcons(icons, iconLibrary, { from });
    const set = await builderApi.createLayerSet(layerSetContent(content));

    await this.fetchLayerSets();

    return { set, warnings };
  },

  /**
   * Replaces the content of a set of the library, which takes the next
   * version, and reads the library again. The server refuses the change
   * when the set changed after the library was read (see
   * describeLayerSetError), and a change of a built-in set.
   *
   * @param {string} id
   * @param {object} content the whole content (see createLayerSet)
   * @param {string} [etag] the ETag of the version the change starts
   *   from, which an editor keeps from when it opened. Without it, the
   *   ETag of the set as the library has it now.
   * @returns {Promise<object>} the set as it is now
   */
  async saveLayerSet(id, content, etag) {
    const set = this.findLayerSet(id);

    if (!set) {
      throw new LayerSetError('The library does not have this layer set.');
    }

    if (set.builtin) {
      throw new LayerSetError(
        'You cannot change a built-in layer set. Duplicate it and change the copy.',
      );
    }

    const saved = await builderApi.updateLayerSet(
      id,
      layerSetContent(content),
      etag || set.etag,
    );

    await this.fetchLayerSets();

    return saved;
  },

  /**
   * Deletes a set of the library, and reads the library again. Diagrams
   * keep their copies of its layers.
   *
   * @param {string} id
   */
  async deleteLayerSet(id) {
    await builderApi.deleteLayerSet(id);
    await this.fetchLayerSets();

    return true;
  },

  /**
   * Adds a copy of a set to the library, named after it with a number (see
   * uniqueLayerSetName). A built-in set is copied this way to change it.
   *
   * @param {string} id
   * @returns {Promise<object>} the new set
   */
  async duplicateLayerSet(id) {
    const set = this.findLayerSet(id);

    if (!set) {
      throw new LayerSetError('The library does not have this layer set.');
    }

    const name = uniqueLayerSetName(
      set.name,
      this.layerSets.items.map((entry) => entry.name),
    );
    const { set: copy } = await this.createLayerSet(contentOf(set, { name }));

    return copy;
  },

  /**
   * Chooses the set that the new blank diagrams of every user copy, or with
   * null removes the choice. The server keeps one choice for all users.
   *
   * @param {string|null} id
   * @returns {Promise<string|null>} the choice as stored
   */
  async setDefaultLayerSet(id) {
    const chosen = await builderApi.setDefaultLayerSet(id || null);

    this.layerSets = { ...this.layerSets, defaultId: chosen };

    return chosen;
  },

  /**
   * Adds the set that the text of a layer set file holds to the library, as
   * a new set (see parseLayerSetFile). The custom icons the file carries go
   * into the icon library. A name that a set of the library has takes a
   * number.
   *
   * @param {string} text
   * @returns {Promise<{set: object, warnings: string[]}>}
   * @throws {LayerSetError} for a file that cannot be read, with its issues
   */
  async uploadLayerSetFile(text) {
    const parsed = parseLayerSetFile(text);

    if (!parsed.ok) {
      throw new LayerSetError(parsed.error, parsed.issues);
    }

    await this.layerSetsReady();

    const { icons, ...file } = parsed.file;
    const name = uniqueLayerSetName(
      file.name,
      this.layerSets.items.map((entry) => entry.name),
    );

    return this.createLayerSet(contentOf(file, { name }), { icons });
  },

  // --- the open diagram's layers ----------------------------------------

  /**
   * Replaces the layers of the diagram and its layer set options with a
   * copy of a set of the library, linked to the set's version, in one undo
   * step (see withLayerSetCopy in model.js). Nodes keep the layers chosen
   * for them.
   *
   * @param {string} id
   * @returns {object|null} the history entry, or null when the library
   *   does not have the set, nothing changed or the draft is read only
   */
  applyLayerSet(id) {
    const set = this.findLayerSet(id);
    const next = set ? withLayerSetCopy(this.doc, set) : this.doc;

    if (next === this.doc) {
      return null;
    }

    return this.commit(next, `Applied ${setName(set)}`);
  },

  /**
   * Copies the version of the linked set that the library has now into the
   * diagram, in one undo step. Changes made to the diagram's copy are
   * replaced.
   *
   * @returns {object|null} the history entry, or null when the layers are
   *   linked to no set of the library, or nothing changed
   */
  updateFromLayerSet() {
    const { set } = layerSetLink(this.doc, this.layerSets.items);
    const next = set ? withLayerSetCopy(this.doc, set) : this.doc;

    if (next === this.doc) {
      return null;
    }

    return this.commit(
      next,
      `Updated the layers from ${setName(set)}, version ${set.version}`,
    );
  },

  /**
   * Removes the link of the diagram's layers to a set. The layers and the
   * options stay.
   *
   * @returns {object|null} the history entry, or null when they are linked
   *   to no set
   */
  unlinkLayerSet() {
    const { source, name } = layerSetOptions(this.doc);
    const next = setLayerSetOptions(this.doc, { source: null });

    if (!source || next === this.doc) {
      return null;
    }

    return this.commit(
      next,
      `Removed the link to layer set ${name || source.id}`,
    );
  },

  /**
   * Writes the diagram's layers and options to the set they are linked to,
   * which takes the next version, and links the diagram to that version in
   * one undo step. The set keeps its name, description and icon. Custom
   * icons that the layers name and the diagram carries go into the icon
   * library first.
   *
   * @returns {Promise<{entry: object|null, set: object, warnings:
   *   string[]}>}
   * @throws {LayerSetError} when the layers are linked to no set that can
   *   change, or the server's refusal
   */
  async saveToLayerSet() {
    const { source } = layerSetOptions(this.doc);
    const set = source ? this.findLayerSet(source.id) : null;

    if (!set) {
      throw new LayerSetError(
        'The layers are not linked to a layer set of the library. Save them as a new layer set.',
      );
    }

    // The copy starts from the version it is linked to. A later version
    // is someone else's change, which the save must not overwrite.
    if (Number.isInteger(set.version) && set.version > source.version) {
      throw new LayerSetError(
        'The layer set has a newer version. Update from the layer set first.',
      );
    }

    const content = contentOf(set, layerSetContentOf(this.doc));
    const warnings = await ingestLayerSetIcons(
      diagramIcons(this.doc, content),
      iconLibrary,
      { from: 'diagram' },
    );
    const saved = await this.saveLayerSet(set.id, content);

    return { entry: this.linkLayerSet(saved), set: saved, warnings };
  },

  /**
   * Adds the diagram's layers and options to the library as a new set, and
   * links the diagram to it in one undo step.
   *
   * @param {{name: string, description?: string, iconKey?: string, icon?:
   *   string}} about the new set's name, description and icon
   * @returns {Promise<{entry: object|null, set: object, warnings:
   *   string[]}>}
   */
  async saveAsNewLayerSet(about) {
    const content = layerSetContent({
      ...about,
      ...layerSetContentOf(this.doc),
    });
    const { set, warnings } = await this.createLayerSet(content, {
      icons: diagramIcons(this.doc, content),
      from: 'diagram',
    });

    return { entry: this.linkLayerSet(set), set, warnings };
  },

  // Links the diagram's layers to a version of a set that holds them, with
  // the set's name, in one undo step.
  linkLayerSet(set) {
    const next = setLayerSetOptions(this.doc, {
      name: set.name,
      source: { id: set.id, version: set.version },
    });

    if (next === this.doc) {
      return null;
    }

    return this.commit(
      next,
      `Saved the layers to ${setName(set)}, version ${set.version}`,
    );
  },

  /**
   * Changes the diagram's layer set options, in one undo step: the match
   * conflict rule, or whether node outlines take the color of their layer.
   *
   * @param {{matchConflict?: string, layerOutlines?: boolean}} changes
   * @returns {object|null} the history entry, or null when nothing changed
   */
  setLayerSetOptions(changes) {
    const next = setLayerSetOptions(this.doc, changes);

    if (next === this.doc) {
      return null;
    }

    const { matchConflict, layerOutlines } = layerSetOptions(next);
    const label =
      'layerOutlines' in changes
        ? `Turned ${layerOutlines ? 'on' : 'off'} layer colors for node outlines`
        : `Matched nodes go to the ${matchConflict === MATCH_CONFLICT_HIGHEST ? 'highest' : 'lowest'} layer ID`;

    return this.commit(next, label);
  },

  /**
   * Changes fields of a layer of the diagram, in one undo step (see
   * updateLayer in model.js): its icon, its color or its node matchers. A
   * change that cannot be written takes no step.
   *
   * @param {number} id
   * @param {object} changes
   * @returns {object|null} the history entry, or null when nothing changed
   */
  updateLayer(id, changes) {
    const next = updateLayer(this.doc, id, changes);

    if (next === this.doc) {
      return null;
    }

    const field = Object.keys(changes)
      .map(
        (key) =>
          ({
            label: 'label',
            iconKey: 'icon',
            icon: 'icon',
            color: 'color',
            matchers: 'node matchers',
          })[key],
      )
      .filter((name, at, list) => name && list.indexOf(name) === at);

    return this.commit(
      next,
      `Changed the ${field.join(' and ')} of ${layerName(next, id)}`,
    );
  },

  /**
   * The layers a new blank diagram starts with: a copy of the server's
   * default set, linked to it. Empty (the placeholder layers) without a
   * default.
   *
   * @param {object} doc a new document
   * @returns {object} the document with the copy
   */
  withDefaultLayerSet(doc) {
    const set = this.defaultLayerSet;

    return set ? withLayerSetCopy(doc, set) : doc;
  },

  /**
   * The layers a template of the library offers in the template editor:
   * those of the server's default set, else the placeholders (empty).
   *
   * @returns {object[]}
   */
  defaultLayers() {
    const set = this.defaultLayerSet;

    return set ? effectiveLayers({ layers: set.layers }) : [];
  },
};
