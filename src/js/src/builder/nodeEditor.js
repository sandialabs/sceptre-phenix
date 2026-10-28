// The Node Editor's host for BuilderInspector.vue: the Builder store, seen
// through one node. The selection stays the node the dialog opened for.
// Announcements go to the dialog's status region, because the page's live
// region cannot be heard through a modal dialog.

/** A BuilderInspector.vue host for one node, with the dialog's `announce`. */
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
