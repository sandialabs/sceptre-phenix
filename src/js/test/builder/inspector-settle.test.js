// What the Inspector does with unapplied edits before a save (settle, which
// Save now and its key call through view.settleEdits), and before the
// diagram is left or read whole (saveUnapplied, see leave.js).

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';
import { UPDATE_DATA } from '@jsonforms/core';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import {
  INSPECTOR_ICON_LIBRARY,
  INSPECTOR_ICONS,
  INSPECTOR_LOCAL_PROBLEMS,
} from '@/components/builder/inspector/control.js';

import { inspectorTarget } from '@/builder/adapters/forms.js';
import { savedAutomatically } from '@/builder/history.js';
import { addNode, findNode, updateNode } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { experimentNodeSpec, sampleDocument } from './fixtures.js';
import { ICON_DATA, ICON_KEY } from './png.js';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

// The Inspector open on a device of `doc`, rendered on the server: its
// watchers run once, so the form stays open on that device whatever the
// store does next, as it does while its edits are unapplied. `edit` changes
// the form's data, as JSON Forms sends it once a field commits, and is
// given what the Inspector provides its renderers; `prepare` sets the store
// up first, once the form is open.
async function openInspector(doc, node, edit, prepare = () => {}) {
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
  prepare(store);

  const selection = { type: 'node', id: node.id };
  const data = JSON.parse(
    JSON.stringify(inspectorTarget(store.doc, selection).data),
  );

  edit(data, instance.provides);
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
    // The custom icons the Custom icon field and its dialog work with.
    icons: instance.provides[INSPECTOR_ICONS],
    library: instance.provides[INSPECTOR_ICON_LIBRARY],
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

// A device's look (its icon, custom icon, outline color and fill color) is
// presentation only: a change of it in the form reaches the canvas at once,
// as an edit of its own, and is no unapplied edit for Apply.
describe("a device's look is applied without Apply", () => {
  const PLC = { name: 'plc', data: ICON_DATA };

  const lookOf = (store, node) => {
    const { outlineColor, fillColor, iconKey } = findNode(
      store.doc,
      node.id,
    ).device;

    return { iconKey, outlineColor, fillColor };
  };

  test('a fill color chosen in the form is on the device at once, named in Undo', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, settle, setup } = await openInspector(doc, alpha, (data) => {
      data.fillColor = '#2f6fbf';
    });
    const version = store.historyVersion;

    expect(lookOf(store, alpha).fillColor).toBe('#2f6fbf');
    expect(store.history.undoLabel()).toBe(
      'Changed the fill color of Device alpha to #2f6fbf',
    );
    // Nothing is left for Apply or a save to apply.
    expect(setup.dirty).toBe(false);
    expect(settle()).toBe('');
    expect(store.historyVersion).toBe(version);

    store.undo();

    expect('fillColor' in findNode(store.doc, alpha.id).device).toBe(false);
  });

  test('an outline color, and a color removed, each say so', async () => {
    const { doc, alpha } = sampleDocument();
    const outlined = await openInspector(doc, alpha, (data) => {
      data.outlineColor = '#A3273F';
    });

    expect(lookOf(outlined.store, alpha).outlineColor).toBe('#A3273F');
    expect(outlined.store.history.undoLabel()).toBe(
      'Changed the outline color of Device alpha to #A3273F',
    );

    const filled = updateNode(doc, alpha.id, {
      device: { fillColor: '#2f6fbf' },
    });
    // An emptied field has no key in the form.
    const emptied = await openInspector(filled, alpha, (data) => {
      delete data.fillColor;
    });

    expect('fillColor' in findNode(emptied.store.doc, alpha.id).device).toBe(
      false,
    );
    expect(emptied.store.history.undoLabel()).toBe(
      'Removed the fill color of Device alpha',
    );
  });

  test('an icon is still applied at once, under its own name', async () => {
    const { doc, alpha } = sampleDocument();
    const { store } = await openInspector(doc, alpha, (data) => {
      data.iconKey = 'router';
    });

    expect(lookOf(store, alpha).iconKey).toBe('router');
    expect(store.history.undoLabel()).toBe(
      'Changed the icon of Device alpha to router',
    );
  });

  // The dialog makes the icon known before the field takes its id (see
  // InspectorIconControl), and the commit of the field copies it into the
  // document.
  test('a custom icon chosen in its dialog is on the device at once, and in the document', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, settle, setup, icons } = await openInspector(
      doc,
      alpha,
      (data, provides) => {
        provides[INSPECTOR_ICONS].shelve(ICON_KEY, PLC);
        data.icon = ICON_KEY;
      },
    );
    const device = () => findNode(store.doc, alpha.id).device;

    expect(device().icon).toBe(ICON_KEY);
    expect(store.doc.icons).toEqual({ [ICON_KEY]: PLC });
    // Said, and undone, by the icon's name, not by its id.
    expect(store.history.undoLabel()).toBe(
      'Changed the custom icon of Device alpha to plc',
    );
    expect(store.announcement).toBe(
      'Changed the custom icon of Device alpha to plc',
    );
    // Nothing is left for Apply or a save to apply.
    expect(setup.dirty).toBe(false);
    expect(settle()).toBe('');

    // The field and the dialog see the diagram's icons.
    expect(icons.entry(ICON_KEY)).toEqual(PLC);
    expect(icons.diagram()).toEqual([{ id: ICON_KEY, ...PLC }]);
    expect(icons.full(ICON_KEY)).toBe(false);

    store.undo();

    expect('icon' in device()).toBe(false);
    expect('icons' in store.doc).toBe(false);
    expect(icons.diagram()).toEqual([]);
    // Chosen once, the icon stays known for the session.
    expect(icons.entry(ICON_KEY)).toEqual(PLC);
  });

  test('a custom icon removed says so, and leaves the document', async () => {
    const { doc, alpha } = sampleDocument();
    const using = {
      ...updateNode(doc, alpha.id, { device: { icon: ICON_KEY } }),
      icons: { [ICON_KEY]: { data: ICON_DATA } },
    };
    // An emptied field has no key in the form.
    const { store } = await openInspector(using, alpha, (data) => {
      delete data.icon;
    });

    expect('icon' in findNode(store.doc, alpha.id).device).toBe(false);
    expect('icons' in store.doc).toBe(false);
    expect(store.history.undoLabel()).toBe(
      'Removed the custom icon of Device alpha',
    );

    // One without a name is said to be so.
    const renamed = await openInspector(doc, alpha, (data, provides) => {
      provides[INSPECTOR_ICONS].shelve(ICON_KEY, { name: '', data: ICON_DATA });
      data.icon = ICON_KEY;
    });

    expect(renamed.store.history.undoLabel()).toBe(
      'Changed the custom icon of Device alpha to an unnamed icon',
    );
    expect(renamed.store.doc.icons).toEqual({
      [ICON_KEY]: { data: ICON_DATA },
    });
  });

  // An icon the dialog never made known is not in the document, so the
  // device cannot name it: the edit is made without it, and says so.
  test('an icon that is not known is left out of the edit', async () => {
    const { doc, alpha } = sampleDocument();
    const { store } = await openInspector(doc, alpha, (data) => {
      data.icon = ICON_KEY;
    });

    expect('icon' in findNode(store.doc, alpha.id).device).toBe(false);
    expect('icons' in store.doc).toBe(false);
    expect(store.announcement).toBe(
      '1 custom icon was left out: a diagram holds at most 32.',
    );
  });

  test('a diagram with 32 icons is full for an icon it does not carry', async () => {
    const { doc, alpha } = sampleDocument();
    const carried = Object.fromEntries(
      Array.from({ length: 32 }, (_, index) => [
        `sha256:${String(index).padStart(64, '0')}`,
        { data: ICON_DATA },
      ]),
    );
    const { icons, library } = await openInspector(
      doc,
      alpha,
      () => {},
      (store) => {
        store.doc = { ...store.doc, icons: carried };
      },
    );

    expect(icons.diagram()).toHaveLength(32);
    expect(icons.full(ICON_KEY)).toBe(true);
    expect(icons.full(Object.keys(carried)[0])).toBe(false);
    // Nothing of an object's own makes an id look carried.
    expect(icons.full('constructor')).toBe(true);
    expect(icons.entry('constructor')).toBeUndefined();
    // The dialog's library is the user's own, on the server.
    expect(Object.keys(library)).toEqual([
      'list',
      'upload',
      'remove',
      'failure',
    ]);
  });

  // The field shows the error; the device keeps the color it has, and a
  // valid change of another look field made with it still goes through.
  test('a color that is no #rrggbb is not applied, and its field says why', async () => {
    const { doc, alpha } = sampleDocument();
    const filled = updateNode(doc, alpha.id, {
      device: { fillColor: '#2f6fbf' },
    });
    const { store, errors, settle, setup } = await openInspector(
      filled,
      alpha,
      (data) => {
        data.fillColor = 'red';
        data.iconKey = 'router';
      },
    );

    expect(lookOf(store, alpha)).toEqual({
      iconKey: 'router',
      outlineColor: undefined,
      fillColor: '#2f6fbf',
    });
    expect(store.history.undoLabel()).toBe(
      'Changed the icon of Device alpha to router',
    );
    expect(errors().map((error) => error.message)).toEqual([
      'Fill Color must be a hex color, such as #2f6fbf',
    ]);
    // The bad color stays in the form as an unapplied edit, which a save
    // does not apply.
    expect(setup.dirty).toBe(true);
    expect(settle()).toBe('1 field needs attention');
    expect(lookOf(store, alpha).fillColor).toBe('#2f6fbf');

    for (const value of ['#abc', '#2f6fbf80', 'rgb(1, 2, 3)', '2f6fbf']) {
      const opened = await openInspector(doc, alpha, (data) => {
        data.outlineColor = value;
      });

      expect(
        'outlineColor' in findNode(opened.store.doc, alpha.id).device,
        value,
      ).toBe(false);
      expect(
        opened.errors().map((error) => error.message),
        value,
      ).toEqual(['Outline Color must be a hex color, such as #2f6fbf']);
    }
  });

  test('other edits made with it stay unapplied until Apply or a save', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, settle, spec, setup } = await openInspector(
      doc,
      alpha,
      (data) => {
        data.fillColor = '#1f7a5a';
        data.spec.general = { ...data.spec.general, description: 'Edited' };
      },
    );

    expect(lookOf(store, alpha).fillColor).toBe('#1f7a5a');
    expect(spec().general.description).not.toBe('Edited');
    expect(setup.dirty).toBe(true);

    expect(settle()).toBe('');
    expect(spec().general.description).toBe('Edited');
    expect(lookOf(store, alpha).fillColor).toBe('#1f7a5a');
    expect(store.history.undoLabel()).toBe('Applied changes to Device alpha');
  });

  // The form keeps its data while it has unapplied edits, so it may show a
  // color the device no longer has once the device is changed elsewhere.
  // Only the look fields a change of the form sets are applied, so an edit
  // of another field does not put that color back, and neither does Apply.
  test('a color changed elsewhere is not put back by an edit of another field', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, settle, spec, setup } = await openInspector(
      doc,
      alpha,
      (data) => {
        data.fillColor = '#2f6fbf';
        data.spec.general = { ...data.spec.general, description: 'Edited' };
      },
    );
    const device = () => findNode(store.doc, alpha.id).device;
    const formData = () => JSON.parse(JSON.stringify(setup.draft));

    expect(device().fillColor).toBe('#2f6fbf');
    store.commit(
      updateNode(store.doc, alpha.id, { device: { fillColor: '#ffd400' } }),
      'Changed elsewhere',
    );

    const edited = formData();

    expect(edited.fillColor).toBe('#2f6fbf');
    edited.spec.general.description = 'Edited again';
    setup.onChange({ data: edited, errors: [] });

    expect(device().fillColor).toBe('#ffd400');
    expect(store.history.undoLabel()).toBe('Changed elsewhere');
    expect(settle()).toBe('');
    expect(spec().general.description).toBe('Edited again');
    expect(device().fillColor).toBe('#ffd400');

    // A new icon chosen then is applied alone.
    setup.onChange({
      data: { ...formData(), iconKey: 'router' },
      errors: [],
    });

    expect(device()).toMatchObject({ iconKey: 'router', fillColor: '#ffd400' });
    expect(store.history.undoLabel()).toBe(
      'Changed the icon of Device alpha to router',
    );
  });

  // While a conflict is resolved, the store refuses every edit. The look
  // then stays in the form as an unapplied edit, for Apply once it is
  // resolved.
  test('a look the store refuses stays unapplied', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, settle, setup } = await openInspector(
      doc,
      alpha,
      (data) => {
        data.fillColor = '#2f6fbf';
      },
      (opened) => {
        opened.resolvingConflict = true;
      },
    );
    const device = () => findNode(store.doc, alpha.id).device;

    expect('fillColor' in device()).toBe(false);
    expect(setup.dirty).toBe(true);
    expect(settle()).toBe('the conflict is being resolved');

    store.resolvingConflict = false;

    expect(settle()).toBe('');
    expect(device().fillColor).toBe('#2f6fbf');
  });

  test('a read-only draft takes no look', async () => {
    const { doc, alpha } = sampleDocument();
    const { store } = await openInspector(
      doc,
      alpha,
      (data) => {
        data.fillColor = '#2f6fbf';
      },
      (opened) => {
        opened.readOnly = true;
      },
    );

    expect('fillColor' in findNode(store.doc, alpha.id).device).toBe(false);
  });

  // A switch's form is short, so its colors wait for Apply with the rest.
  test("a switch's colors wait for Apply", async () => {
    const { doc, sw } = sampleDocument();
    const { store, settle } = await openInspector(doc, sw, (data) => {
      data.fillColor = '#6b6f18';
      data.outlineColor = '#a3273f';
      data.lineStyle = 'dotted';
    });

    expect(findNode(store.doc, sw.id).switch).toEqual(sw.switch);
    expect(settle()).toBe('');
    expect(findNode(store.doc, sw.id).switch).toEqual({
      ...sw.switch,
      outlineColor: '#a3273f',
      fillColor: '#6b6f18',
    });
    expect(store.doc.networks[0].lineStyle).toBe('dotted');
    // One edit, so one Undo puts all three back.
    expect(store.history.undoLabel()).toBe('Applied changes to Network EXP');
    store.undo();
    expect(findNode(store.doc, sw.id).switch).toEqual(sw.switch);
    expect('lineStyle' in store.doc.networks[0]).toBe(false);
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
