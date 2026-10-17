// The Node Editor's host (see dialogs/NodeEditorDialog.vue and the `host`
// prop of BuilderInspector.vue): the Builder store, seen through one node.
//
// The Inspector in the dialog reads the store's document, schema, checks and
// disk images, and its edits go to the store. A commit is store.commit, so
// one Apply is one undo step, and autosave sends it as any edit. The
// connection points, the position and the layer act at once, through
// the store's own actions, as they do beside the canvas.
//
// Two things differ from the store:
//
// - The selection is the node the dialog opened for. A later change of the
//   store's selection does not change what the dialog edits.
// - What the form announces goes to `announce`: the dialog's status region,
//   because the page's live region cannot be heard through a modal dialog.

/**
 * @param {object} store the Builder store
 * @param {string} nodeId the node the dialog edits
 * @param {object} options
 * @param {(message: string) => void} options.announce
 * @returns {object} a host for BuilderInspector.vue
 */
export function nodeEditorHost(store, nodeId, { announce }) {
  return {
    inspectorSelection: Object.freeze({ type: 'node', id: nodeId }),
    get doc() {
      return store.doc;
    },
    get schema() {
      return store.schema;
    },
    get schemaError() {
      return store.schemaError;
    },
    get readOnly() {
      return store.readOnly;
    },
    get disks() {
      return store.disks;
    },
    get issues() {
      return store.issues;
    },
    get canRedo() {
      return store.canRedo;
    },
    commit: (doc, label) => store.commit(doc, label),
    announce,
    addInterface: (...args) => store.addInterface(...args),
    removeInterface: (...args) => store.removeInterface(...args),
    remove: (...args) => store.remove(...args),
    moveNodes: (...args) => store.moveNodes(...args),
    setNodeLayer: (...args) => store.setNodeLayer(...args),
  };
}
