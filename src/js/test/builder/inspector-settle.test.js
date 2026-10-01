// What the Inspector does with unapplied edits before a save (settle, which
// Save now and its key call through view.settleEdits), and before the
// diagram is left or read whole (saveUnapplied, see leave.js).

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';
import { UPDATE_DATA } from '@jsonforms/core';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import { INSPECTOR_LOCAL_PROBLEMS } from '@/components/builder/inspector/control.js';

import { inspectorTarget } from '@/builder/adapters/forms.js';
import { savedAutomatically } from '@/builder/history.js';
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
    saveUnapplied: instance.exposed.saveUnapplied,
    // The Inspector's own state: the Position fields' text, and the
    // middleware JSON Forms runs every change of its data through.
    setup: instance.setupState,
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

  // Edits merge into the element as it is now, so what changed it
  // meanwhile stays: they used to be kept back, or, applied, to put the
  // working copy over it.
  // A key/value row with no name, or a name used above it, is kept out
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

  // While a conflict is resolved, the store refuses every edit. The typed
  // values stay in the form, to be applied once it is resolved.
  test('edits the store refuses stay unapplied, and it says why', async () => {
    const { store, settle, description } = await editedInspector();

    store.resolvingConflict = true;

    expect(settle()).toBe('the conflict is being resolved');
    expect(description()).not.toBe('Edited');

    store.resolvingConflict = false;

    expect(settle()).toBe('');
    expect(description()).toBe('Edited');
  });
});

// A device generated from a phenix experiment carries values phenix
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

describe('saving unapplied edits before leaving or reading the diagram', () => {
  test('valid edits are applied as one edit marked as saved automatically', async () => {
    const { store, saveUnapplied, description } = await editedInspector();
    const version = store.historyVersion;

    expect(saveUnapplied()).toBeNull();
    expect(description()).toBe('Edited');
    expect(store.historyVersion).toBe(version + 1);
    expect(store.history.undoLabel()).toBe(
      'Saved unapplied changes to Device alpha',
    );
    expect(savedAutomatically(store.history.undoLabel())).toBe(true);

    // Nothing is left to save.
    expect(saveUnapplied()).toBeNull();
    expect(store.historyVersion).toBe(version + 1);
  });

  test('nothing unapplied makes no edit', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, saveUnapplied } = await openInspector(doc, alpha, () => {});
    const version = store.historyVersion;

    expect(saveUnapplied()).toBeNull();
    expect(store.historyVersion).toBe(version);
  });

  test('edits are applied while a redo is pending, clearing Redo', async () => {
    const { store, bravo, saveUnapplied, description } =
      await editedInspector();

    store.commit(
      updateNode(store.doc, bravo.id, { position: { x: 40, y: 200 } }),
      'Moved bravo',
    );
    store.undo();

    expect(saveUnapplied()).toBeNull();
    expect(description()).toBe('Edited');
    expect(store.canRedo).toBe(false);
  });

  test('edits that fail their checks are kept, and their fields named', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, saveUnapplied, errors, spec } = await openInspector(
      doc,
      alpha,
      (data) => {
        data.spec.network.interfaces[0].mac = 'zz';
        data.spec.general = { ...data.spec.general, description: 'Kept' };
      },
    );
    const version = store.historyVersion;

    expect(saveUnapplied()).toEqual({
      title: 'Device alpha',
      fields: ['MAC address (Interface 1)'],
    });
    expect(store.historyVersion).toBe(version);
    expect(spec().general?.description).not.toBe('Kept');
    // Still in the form, to fix or cancel.
    expect(errors()).toHaveLength(1);
  });

  test('rows a renderer holds back are named by their field', async () => {
    const { report, saveUnapplied, description } = await editedInspector();

    report('spec.hardware.memory', [
      {
        path: 'spec.hardware.memory',
        message: 'Memory must be a whole number',
      },
    ]);

    expect(saveUnapplied()).toEqual({
      title: 'Device alpha',
      fields: ['Memory'],
    });
    expect(description()).not.toBe('Edited');
  });

  // A reload cannot wait for JSON Forms' next update, which sends the
  // change a field just committed.
  test('a change JSON Forms has not sent yet is saved too', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, setup, saveUnapplied } = await openInspector(
      doc,
      alpha,
      () => {},
    );
    const selection = { type: 'node', id: alpha.id };
    const data = JSON.parse(
      JSON.stringify(inspectorTarget(store.doc, selection).data),
    );

    data.spec.hardware = { ...data.spec.hardware, memory: 4096 };
    setup.middleware({ data: {} }, { type: UPDATE_DATA }, () => ({ data }));

    expect(saveUnapplied()).toBeNull();
    expect(
      inspectorTarget(store.doc, selection).data.spec.hardware.memory,
    ).toBe(4096);
    expect(store.history.undoLabel()).toBe(
      'Saved unapplied changes to Device alpha',
    );
  });

  test('a position typed and not moved to is saved in the same edit', async () => {
    const { store, alpha, setup, saveUnapplied, description } =
      await editedInspector();
    const version = store.historyVersion;

    setup.position = { x: ' 40 ', y: '-12.6' };

    expect(saveUnapplied()).toBeNull();
    expect(store.historyVersion).toBe(version + 1);
    expect(description()).toBe('Edited');
    expect(findNode(store.doc, alpha.id).position).toEqual({ x: 40, y: -13 });
  });

  test('a position that is no number is named, and the valid edits saved', async () => {
    const { store, alpha, setup, saveUnapplied, description } =
      await editedInspector();

    setup.position = { x: '4O', y: '0' };

    expect(saveUnapplied()).toEqual({
      title: 'Device alpha',
      fields: ['Position X'],
    });
    expect(description()).toBe('Edited');
    expect(findNode(store.doc, alpha.id).position).toEqual({ x: 0, y: 0 });
  });

  test('a read-only draft saves nothing', async () => {
    const { store, saveUnapplied, description } = await editedInspector();

    store.readOnly = true;

    expect(saveUnapplied()).toBeNull();
    expect(description()).not.toBe('Edited');
  });
});

// Position X and Y are text read as numbers: Firefox's number input took
// " 3 " for no number at all.
describe('the Position fields', () => {
  async function positioned() {
    const { doc, alpha } = sampleDocument();

    return openInspector(doc, alpha, () => {});
  }

  test('take a number with spaces around it', async () => {
    const { setup } = await positioned();

    setup.position = { x: ' 3 ', y: '0' };

    expect(setup.canMove).toBe(true);
    expect(setup.moveTo).toEqual({ x: 3, y: 0 });
    expect(setup.shownPosition('x')).toBe(3);
  });

  test('do not move to text that is no number', async () => {
    const { setup } = await positioned();

    setup.position = { x: '3px', y: '0' };

    expect(setup.canMove).toBe(false);
    expect(setup.shownPosition('x')).toBeUndefined();
  });

  test('ArrowUp and ArrowDown step to the next whole pixel', async () => {
    const { setup } = await positioned();
    const press = (key, axis = 'x') => {
      const event = { key, preventDefault: vi.fn() };

      setup.stepPosition(event, axis);

      return event;
    };

    setup.position = { x: ' 480.5 ', y: '' };
    press('ArrowUp');
    expect(setup.position.x).toBe('481');
    press('ArrowDown');
    press('ArrowDown');
    expect(setup.position.x).toBe('479');
    // An empty field steps from 0.
    press('ArrowDown', 'y');
    expect(setup.position.y).toBe('-1');
    // Text that is no number is left alone.
    setup.position = { x: 'abc', y: '0' };
    expect(press('ArrowUp').preventDefault).not.toHaveBeenCalled();
    expect(setup.position.x).toBe('abc');
  });
});
