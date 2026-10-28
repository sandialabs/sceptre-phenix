// The Builder store's part for the server's layer set library and the links of
// diagrams to its sets. store.js spreads these into its state, getters and
// actions, so `this` is the store. A diagram holds a copy of a set's layers and
// a link to its ID and version (see withLayerSetCopy in model.js).

import {
  builderApi,
  classifyError,
  endsBulk,
  libraryErrorMessage,
  serverCode,
} from './api.js';
import { BulkError, bulkOutcome, runBulk } from './bulk.js';
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
 * The layer set library before it is read. `status` goes from 'idle' to
 * 'loading' to 'ready' or 'failed'. `loaded` tells whether the rest is the
 * server's answer. A failed read keeps what an earlier read answered.
 */
export function emptyLayerSets() {
  return {
    status: 'idle',
    error: '',
    loaded: false,
    items: [],
    defaultId: null,
    defaultEtag: null,
    damaged: 0,
  };
}

// Why a delete or a choice of the default was not made: someone changed the
// library after it was read.
const LIBRARY_CHANGED =
  'Someone changed it after the Builder read the list. The list now shows it as it is. Check it, and then try again.';

const NOT_IN_LIBRARY = 'The library does not have this layer set.';

/** Why Delete selected left a built-in set as it is. */
export const BUILTIN_NOT_DELETED = 'You cannot delete a built-in layer set.';

// Sends a change that names the tag the library has. A refusal for a tag
// that is no longer current (412) reads the library again and rejects with
// a LayerSetError that says so. Any other failure rejects as it is.
async function staleChecked(store, send) {
  try {
    return await send();
  } catch (error) {
    if (error?.response?.status !== 412) {
      throw error;
    }

    await store.fetchLayerSets();

    throw new LayerSetError(LIBRARY_CHANGED);
  }
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
  /** The server's default layer set, which new blank diagrams copy, or null. */
  defaultLayerSet: (state) =>
    (state.layerSets.defaultId &&
      state.layerSets.items.find(
        (set) => set.id === state.layerSets.defaultId,
      )) ||
    null,

  /**
   * How the diagram's layers stand to their linked set (see layerSetLink in
   * model.js), or 'unknown' while the library is not read.
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
          defaultEtag: library.defaultEtag,
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
   * Reads the library unless it was read already, so a caller can wait for it.
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
   * Why a change of the layer set library failed, such as "Could not save the
   * layer set. ...".
   */
  describeLayerSetError(error, action) {
    if (error instanceof LayerSetError) {
      return `Could not ${action}. ${error.reason}`;
    }

    if (serverCode(error) === 'layerset.stale') {
      return `Could not ${action}. Someone changed the layer set after the Builder read it. Read it again, and then make your change again.`;
    }

    return `Could not ${action}. ${libraryErrorMessage(error)}`;
  },

  /**
   * Adds a set to the library, and reads the library again. The custom icons in
   * `icons` go into the icon library first. The caller reports a rejection (see
   * describeLayerSetError).
   */
  async createLayerSet(content, { icons = null, from = 'file' } = {}) {
    const warnings = await ingestLayerSetIcons(icons, iconLibrary, { from });
    const set = await builderApi.createLayerSet(layerSetContent(content));

    await this.fetchLayerSets();

    return { set, warnings };
  },

  /**
   * Replaces a set's content, which takes the next version, and reads the
   * library again. `etag` is the version the editor opened, else the library's.
   * The server refuses a stale change and a change of a built-in set.
   */
  async saveLayerSet(id, content, etag) {
    const set = this.findLayerSet(id);

    if (!set) {
      throw new LayerSetError(NOT_IN_LIBRARY);
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
   * Deletes a library set at the version the library has, and reads the library
   * again. Diagrams keep their copies. A stale delete rejects with a
   * LayerSetError.
   */
  async deleteLayerSet(id) {
    const set = this.findLayerSet(id);

    if (!set) {
      throw new LayerSetError(NOT_IN_LIBRARY);
    }

    await staleChecked(this, () => builderApi.deleteLayerSet(id, set.etag));
    await this.fetchLayerSets();

    return true;
  },

  /**
   * Deletes sets one at a time, then reads the library once. A built-in set
   * is not sent. Returns the failures (bulkOutcome), each built-in set too.
   */
  async deleteLayerSets(sets, { onProgress } = {}) {
    const results = await runBulk(
      sets.filter((set) => !set.builtin),
      async (set) => {
        const listed = this.findLayerSet(set.id);

        if (!listed) {
          throw new BulkError(NOT_IN_LIBRARY);
        }

        await staleChecked(this, () =>
          builderApi.deleteLayerSet(set.id, listed.etag),
        );
      },
      { limit: 1, onProgress, stop: endsBulk },
    );

    await this.fetchLayerSets();

    const { done, failures } = bulkOutcome(results, (error) =>
      error instanceof LayerSetError
        ? error.reason
        : libraryErrorMessage(error),
    );
    const builtins = sets
      .filter((set) => set.builtin)
      .map((item) => ({ item, reason: BUILTIN_NOT_DELETED }));

    return {
      done,
      failures: [...builtins, ...failures].sort(
        (a, b) => sets.indexOf(a.item) - sets.indexOf(b.item),
      ),
    };
  },

  /**
   * Adds a numbered copy of a set to the library. A built-in set is changed
   * through a copy.
   */
  async duplicateLayerSet(id) {
    const set = this.findLayerSet(id);

    if (!set) {
      throw new LayerSetError(NOT_IN_LIBRARY);
    }

    const name = uniqueLayerSetName(
      set.name,
      this.layerSets.items.map((entry) => entry.name),
    );
    const { set: copy } = await this.createLayerSet(contentOf(set, { name }));

    return copy;
  },

  /**
   * Chooses the set that new blank diagrams of every user copy, or none with
   * null. A stale choice rejects with a LayerSetError after the library is read
   * again.
   */
  async setDefaultLayerSet(id) {
    const chosen = await staleChecked(this, () =>
      builderApi.setDefaultLayerSet(id || null, this.layerSets.defaultEtag),
    );

    this.layerSets = { ...this.layerSets, ...chosen };

    return chosen.defaultId;
  },

  /**
   * Adds the set of a layer set file's text to the library as a new set, with
   * its custom icons. A taken name takes a number. A bad file throws a
   * LayerSetError with its issues.
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
   * Replaces the diagram's layers and options with a linked copy of a library
   * set in one undo step. Nodes keep the layers chosen for them. Returns the
   * history entry, or null.
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
   * Copies the linked set's current version into the diagram in one undo step,
   * which replaces changes to the copy. Returns the history entry, or null.
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
   * Removes the link of the diagram's layers to a set. The layers and options
   * stay.
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
   * Writes the diagram's layers and options to the linked set, which takes the
   * next version, and links to it in one undo step. The set keeps its name,
   * description and icon. The custom icons go into the icon library first.
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
   * Adds the diagram's layers and options as a new library set, and links to it
   * in one undo step.
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

    return {
      entry: this.linkLayerSet(
        set,
        `Added ${setName(set)}, and linked the diagram to it`,
      ),
      set,
      warnings,
    };
  },

  // Links the diagram's layers to a version of a set that holds them, with
  // the set's name, in one undo step.
  linkLayerSet(
    set,
    label = `Saved the layers to ${setName(set)}, version ${set.version}`,
  ) {
    const next = setLayerSetOptions(this.doc, {
      name: set.name,
      source: { id: set.id, version: set.version },
    });

    if (next === this.doc) {
      return null;
    }

    return this.commit(next, label);
  },

  /**
   * Changes the diagram's match conflict rule or layer outlines in one undo
   * step.
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
   * Changes a layer's icon, color or node matchers in one undo step (see
   * updateLayer in model.js). A change that cannot be written takes no step.
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
   * A new blank diagram with a linked copy of the server's default set, or the
   * placeholder layers.
   */
  withDefaultLayerSet(doc) {
    const set = this.defaultLayerSet;

    return set ? withLayerSetCopy(doc, set) : doc;
  },

  /**
   * The layers that the template editor offers: the default set's, else the
   * placeholders.
   */
  defaultLayers() {
    const set = this.defaultLayerSet;

    return set ? effectiveLayers({ layers: set.layers }) : [];
  },
};
