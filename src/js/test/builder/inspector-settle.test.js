// What the Inspector does with unapplied edits before a save (settle, which
// Save now and its key call through view.settleEdits).

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import { INSPECTOR_LOCAL_PROBLEMS } from '@/components/builder/inspector/control.js';

import { inspectorTarget } from '@/builder/adapters/forms.js';
import { addNode, findNode, updateNode } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { experimentNodeSpec, sampleDocument } from './fixtures.js';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

// The Inspector open on a device of `doc`, rendered on the server: its
// watchers run once, so the form stays open on that device whatever the
// store does next, as it does while its edits are unapplied. `edit` changes
// the form's data, as JSON Forms sends it once a field commits.
async function openInspector(doc, node, edit) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(BuilderInspector) });
  let instance = null;

  app.use(pinia);
  app.mixin({
    created() {
      if (this.$.type === BuilderInspector) {
        instance = this.$;
      }
    },
  });

  const store = useBuilderStore(pinia);
  // The draft as loaded, so an undo goes back to it.
  store.commit(doc, '');
  store.select({ nodes: [node.id] });

  await renderToString(app);

  const selection = { type: 'node', id: node.id };
  const data = JSON.parse(
    JSON.stringify(inspectorTarget(store.doc, selection).data),
  );

  edit(data);
  instance.setupState.onChange({ data, errors: [] });

  return {
    store,
    settle: instance.exposed.settle,
    errors: () => instance.exposed.errors.value,
    spec: () => inspectorTarget(store.doc, selection)?.data.spec,
    // As a renderer reports rows that are not in the working copy.
    report: (path, found) =>
      instance.provides[INSPECTOR_LOCAL_PROBLEMS](path, found),
  };
}

// The Inspector open on the sample's alpha device, with an edit to its
// description made in the form and not applied.
async function editedInspector() {
  const { doc, alpha, bravo, sw } = sampleDocument();
  const opened = await openInspector(doc, alpha, (data) => {
    data.spec.general = { ...data.spec.general, description: 'Edited' };
  });

  return {
    ...opened,
    alpha,
    bravo,
    sw,
    description: () => opened.spec()?.general?.description,
  };
}

describe('settling unapplied edits before a save', () => {
  test('valid edits to an unchanged element are applied', async () => {
    const { store, settle, description } = await editedInspector();

    expect(settle()).toBe('');
    expect(description()).toBe('Edited');
    expect(store.history.undoLabel()).toBe('Applied changes to Device alpha');
  });

  // Saving is asked for as Apply is, so a pending redo does not keep the
  // edits back; applying them clears Redo, as Apply does.
  test('valid edits are applied while a redo is pending, clearing Redo', async () => {
    const { store, bravo, settle, description } = await editedInspector();

    store.commit(
      updateNode(store.doc, bravo.id, { position: { x: 40, y: 200 } }),
      'Moved bravo',
    );
    store.undo();
    expect(store.canRedo).toBe(true);

    expect(settle()).toBe('');
    expect(description()).toBe('Edited');
    expect(store.history.undoLabel()).toBe('Applied changes to Device alpha');
    expect(store.canRedo).toBe(false);
  });

  // R16: edits merge into the element as it is now, so what changed it
  // meanwhile stays: they used to be kept back, or, applied, to put the
  // working copy over it.
  // R34: a key/value row with no name, or a name used above it, is kept out
  // of the working copy until it is fixed, and must not be lost unsaid.
  test('rows a renderer holds back keep the edits from being applied', async () => {
    const { report, settle, errors, description } = await editedInspector();
    const problem = {
      path: 'spec.labels.1',
      message: 'Label 2: Name is used by label 1 already',
    };

    report('spec.labels', [problem]);

    expect(settle()).toBe('1 field needs attention');
    expect(description()).not.toBe('Edited');
    expect(errors()).toContainEqual(problem);

    report('spec.labels', []);

    expect(errors()).toEqual([]);
    expect(settle()).toBe('');
    expect(description()).toBe('Edited');
  });

  test('edits to an element changed since are merged into it', async () => {
    const { store, alpha, settle, description } = await editedInspector();

    store.commit(
      updateNode(store.doc, alpha.id, { device: { iconKey: 'router' } }),
      'Changed the icon of Device alpha to router',
    );

    expect(settle()).toBe('');
    expect(description()).toBe('Edited');
    expect(findNode(store.doc, alpha.id).device.iconKey).toBe('router');
  });

  test('an interface added and connected since stays, with its connection', async () => {
    const { store, alpha, sw, settle, description } = await editedInspector();

    store.addInterface(alpha.id, {});
    const [, added] = findNode(store.doc, alpha.id).device.interfaces;
    store.connect({
      sourceNodeId: alpha.id,
      sourceHandleId: added.id,
      targetNodeId: sw.id,
    });
    const edges = store.doc.edges;

    expect(settle()).toBe('');
    expect(description()).toBe('Edited');
    expect(
      findNode(store.doc, alpha.id).device.interfaces.map((h) => h.name),
    ).toEqual(['eth0', 'eth1']);
    expect(store.doc.edges).toEqual(edges);
  });

  test('edits to an element no longer in the diagram say so', async () => {
    const { store, alpha, settle } = await editedInspector();

    store.doc = {
      ...store.doc,
      nodes: store.doc.nodes.filter((node) => node.id !== alpha.id),
    };

    expect(settle()).toBe('Device alpha is no longer in the diagram');
  });
});

// R1: a device generated from a phenix experiment carries values phenix
// accepts and the form's schema does not (advanced null, mac "", gateway
// "", ruleset_in ""), which kept every edit of it from being applied.
// Errors on fields an edit leaves as they were count no more; one on a
// field it changed still does.
describe('a device imported from a phenix experiment', () => {
  function imported() {
    const { doc } = sampleDocument();
    const added = addNode(doc, {
      kind: 'device',
      hostname: 'host-00',
      spec: experimentNodeSpec(),
      interfaces: [{ name: 'eth0' }],
    });

    return { doc: added.doc, node: added.node };
  }

  test('takes an edit, although fields it came with have errors', async () => {
    const { doc, node } = imported();
    const { settle, errors, spec } = await openInspector(doc, node, (data) => {
      data.spec.general.description = 'Imported';
    });

    expect(errors()).toEqual([]);
    expect(settle()).toBe('');
    expect(spec().general.description).toBe('Imported');
    // What it came with is kept as it was.
    expect(spec().advanced).toBeNull();
    expect(spec().network.interfaces[0]).toMatchObject({
      mac: '',
      gateway: '',
      ruleset_in: '',
    });
  });

  test('an error on a field the edit changed still counts', async () => {
    const { doc, node } = imported();
    const { settle, errors, spec } = await openInspector(doc, node, (data) => {
      data.spec.network.interfaces[0].mac = 'zz';
    });

    expect(errors().map((error) => error.message)).toEqual([
      'Interface 1: MAC address must be a MAC address, such as 00:11:22:33:44:55',
    ]);
    expect(settle()).toBe('1 field needs attention');
    expect(spec().network.interfaces[0].mac).toBe('');
  });
});
