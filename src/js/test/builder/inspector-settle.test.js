// @vitest-environment happy-dom
// The Inspector's unapplied edits before Save now (settle) and before the
// diagram is left or read whole (saveUnapplied, see leave.js), mounted.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import {
  INSPECTOR_ICON_LIBRARY,
  INSPECTOR_ICONS,
} from '@/components/builder/inspector/control.js';
import { resetCollapsed } from '@/components/builder/inspector/collapse.js';

import { inspectorTarget } from '@/builder/adapters/forms.js';
import { savedAutomatically } from '@/builder/history.js';
import { iconLibrary } from '@/builder/iconLibrary.js';
import { indexIcons } from '@/builder/icons.js';
import { addNode, findNode, moveNodes, updateNode } from '@/builder/model.js';

import { experimentNodeSpec, sampleDocument } from './fixtures.js';
import {
  actionsShown,
  disclosureOf,
  fieldAt,
  formData,
  keydown,
  mountInspector,
  submitWithEnter,
  sendFormData,
  type,
} from './inspector-mount.js';
import { ICON_DATA } from './png.js';

enableAutoUnmount(afterEach);
afterEach(resetCollapsed);

// The Inspector mounted on `node` of `doc`. `edit` changes the form's data,
// which JSON Forms then sends, after `prepare` sets up the store.
async function openInspector(doc, node, edit, prepare = () => {}) {
  const { wrapper, store } = await mountInspector({ doc, element: node });

  prepare(store);
  await flushPromises();

  const selection = { type: node.sourceNodeId ? 'edge' : 'node', id: node.id };
  const data = JSON.parse(
    JSON.stringify(inspectorTarget(store.doc, selection).data),
  );

  edit(data);
  await sendFormData(wrapper, data);

  return {
    wrapper,
    store,
    settle: () => wrapper.vm.settle(),
    saveUnapplied: () => wrapper.vm.saveUnapplied(),
    errors: () => wrapper.vm.errors,
    spec: () => inspectorTarget(store.doc, selection)?.data.spec,
    // The custom icons that the Icon field's Custom icon… item and its
    // dialog work with.
    icons: wrapper.vm.$.provides[INSPECTOR_ICONS],
    library: wrapper.vm.$.provides[INSPECTOR_ICON_LIBRARY],
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

// Save now and leaving the diagram treat unapplied edits alike.
describe.each([
  ['settle', '', 'Applied changes to Device alpha', '1 field needs attention'],
  [
    'saveUnapplied',
    null,
    'Saved unapplied changes to Device alpha',
    { title: 'Device alpha', fields: ['Memory'] },
  ],
])('%s', (method, done, label, heldBack) => {
  test('applies valid edits as one edit, named in Undo', async () => {
    const opened = await editedInspector();
    const { store, description } = opened;
    const version = store.historyVersion;

    expect(opened[method]()).toBe(done);
    expect(description()).toBe('Edited');
    expect(store.historyVersion).toBe(version + 1);
    expect(store.history.undoLabel()).toBe(label);
    expect(savedAutomatically(label)).toBe(method === 'saveUnapplied');

    // Nothing is left to apply.
    expect(opened[method]()).toBe(done);
    expect(store.historyVersion).toBe(version + 1);
  });

  test('applies valid edits while a redo is pending, clearing Redo', async () => {
    const opened = await editedInspector();
    const { store, bravo, description } = opened;

    store.commit(
      updateNode(store.doc, bravo.id, { position: { x: 40, y: 200 } }),
      'Moved bravo',
    );
    store.undo();
    expect(store.canRedo).toBe(true);

    expect(opened[method]()).toBe(done);
    expect(description()).toBe('Edited');
    expect(store.history.undoLabel()).toBe(label);
    expect(store.canRedo).toBe(false);
  });

  // Rows a renderer keeps out of the working copy must not be lost unsaid:
  // Memory holds text it cannot read, which it does not commit.
  test('rows a renderer holds back keep the edits back until fixed', async () => {
    const opened = await editedInspector();
    const { wrapper, errors, description } = opened;
    const problem = {
      path: 'spec.hardware.memory',
      message: 'Memory must be a whole number',
    };
    const memory = fieldAt(wrapper, 'spec.hardware.memory');
    const shown = memory.element.value;

    await memory.setValue('lots');

    expect(opened[method]()).toEqual(heldBack);
    expect(description()).not.toBe('Edited');
    expect(errors()).toContainEqual(problem);

    await memory.setValue(shown);

    expect(errors()).toEqual([]);
    expect(opened[method]()).toBe(done);
    expect(description()).toBe('Edited');
  });
});

describe('settling unapplied edits before a save', () => {
  // Edits merge into the element as it is now, so what changed it meanwhile
  // stays.
  test('edits to an element changed since are merged into it', async () => {
    const { store, alpha, settle, description } = await editedInspector();

    store.commit(
      updateNode(store.doc, alpha.id, { device: { iconKey: 'router' } }),
      'Changed the icon of Device alpha to Router',
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

  // So the fields that need attention show.
  test('the collapsed sections and entries with the errors expand', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper, settle } = await openInspector(doc, alpha, (data) => {
      data.spec.network.interfaces[0].mac = 'zz';
    });
    const expanded = (path) =>
      disclosureOf(wrapper, path).attributes('aria-expanded');

    // Innermost first, as each stays clickable while what holds it is open.
    for (const path of [
      'spec.network.interfaces.0',
      'spec.network.interfaces',
      'spec.network',
      'spec.hardware',
    ]) {
      await disclosureOf(wrapper, path).trigger('click');
      expect(expanded(path)).toBe('false');
    }

    expect(settle()).toBe('1 field needs attention');
    await flushPromises();
    expect(expanded('spec.network')).toBe('true');
    expect(expanded('spec.network.interfaces')).toBe('true');
    expect(expanded('spec.network.interfaces.0')).toBe('true');
    expect(expanded('spec.hardware')).toBe('false');
  });

  // While a conflict is resolved, the store refuses every edit. The typed
  // values stay in the form until it is resolved.
  test('edits the store refuses stay unapplied, and it says why', async () => {
    const { store, settle, description } = await editedInspector();

    store.resolvingConflict = true;

    expect(settle()).toBe('the conflict is not resolved');
    expect(description()).not.toBe('Edited');

    store.resolvingConflict = false;

    expect(settle()).toBe('');
    expect(description()).toBe('Edited');
  });
});

// A device generated from a phenix experiment carries values the form's
// schema refuses. Only errors on fields an edit changes count.
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
  test('nothing unapplied makes no edit', async () => {
    const { doc, alpha } = sampleDocument();
    const { store, saveUnapplied } = await openInspector(doc, alpha, () => {});
    const version = store.historyVersion;

    expect(saveUnapplied()).toBeNull();
    expect(store.historyVersion).toBe(version);
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

  // A reload cannot wait for JSON Forms' next update, which sends the
  // change a field just committed.
  test('a change JSON Forms has not sent yet is saved too', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper, store, saveUnapplied } = await openInspector(
      doc,
      alpha,
      () => {},
    );
    const selection = { type: 'node', id: alpha.id };
    const memory = fieldAt(wrapper, 'spec.hardware.memory').element;

    // Memory commits, and the reload comes before JSON Forms' next update.
    memory.value = '4096';
    memory.dispatchEvent(new Event('change', { bubbles: true }));

    expect(saveUnapplied()).toBeNull();
    expect(
      inspectorTarget(store.doc, selection).data.spec.hardware.memory,
    ).toBe(4096);
    expect(store.history.undoLabel()).toBe(
      'Saved unapplied changes to Device alpha',
    );
  });

  test('a position typed and not moved to is saved in the same edit', async () => {
    const { wrapper, store, alpha, saveUnapplied, description } =
      await editedInspector();
    const version = store.historyVersion;

    await wrapper.get('#inspector-position-x').setValue(' 40 ');
    await wrapper.get('#inspector-position-y').setValue('-12.6');

    expect(saveUnapplied()).toBeNull();
    expect(store.historyVersion).toBe(version + 1);
    expect(description()).toBe('Edited');
    expect(findNode(store.doc, alpha.id).position).toEqual({ x: 40, y: -13 });
  });

  test('a position that is no number is named, and the valid edits saved', async () => {
    const { wrapper, store, alpha, saveUnapplied, description } =
      await editedInspector();

    await wrapper.get('#inspector-position-x').setValue('4O');
    await wrapper.get('#inspector-position-y').setValue('0');

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

// A device's look (icon, custom icon, outline and fill color) reaches the
// canvas at once, as an edit of its own, and is no unapplied edit for Apply.
describe("a device's look is applied without Apply", () => {
  const lookOf = (store, node) => {
    const { outlineColor, fillColor, iconKey } = findNode(
      store.doc,
      node.id,
    ).device;

    return { iconKey, outlineColor, fillColor };
  };

  test('a fill color chosen in the form is on the device at once, named in Undo', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper, store, settle } = await openInspector(
      doc,
      alpha,
      (data) => {
        data.fillColor = '#2f6fbf';
      },
    );
    const version = store.historyVersion;

    expect(lookOf(store, alpha).fillColor).toBe('#2f6fbf');
    expect(store.history.undoLabel()).toBe(
      'Changed the fill color of Device alpha to #2f6fbf',
    );
    // Nothing is left for Apply or a save to apply.
    expect(actionsShown(wrapper)).toBe(false);
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
      'Changed the icon of Device alpha to Router',
    );
  });

  // The dialog's choice is a name (see InspectorIconControl), which the
  // icon library resolves: the document carries no copy for it.
  test('a custom icon chosen in its dialog is on the device at once, by its name', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper, store, settle } = await openInspector(
      doc,
      alpha,
      (data) => {
        data.icon = 'plc';
      },
    );
    const device = () => findNode(store.doc, alpha.id).device;

    expect(device().icon).toBe('plc');
    expect('icons' in store.doc).toBe(false);
    expect(store.history.undoLabel()).toBe(
      'Changed the custom icon of Device alpha to plc',
    );
    expect(store.announcement).toBe(
      'Changed the custom icon of Device alpha to plc',
    );
    // Nothing is left for Apply or a save to apply.
    expect(actionsShown(wrapper)).toBe(false);
    expect(settle()).toBe('');

    store.undo();

    expect('icon' in device()).toBe(false);
  });

  test('a custom icon removed says so, and its copy leaves the document', async () => {
    const { doc, alpha } = sampleDocument();
    const using = {
      ...updateNode(doc, alpha.id, { device: { icon: 'plc' } }),
      icons: { plc: { data: ICON_DATA } },
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
  });

  // The device names an icon the server may get later: its node draws the
  // built-in icon meanwhile.
  test('a name nothing resolves is the device’s all the same', async () => {
    const { doc, alpha } = sampleDocument();
    const { store } = await openInspector(doc, alpha, (data) => {
      data.icon = 'not-uploaded';
    });

    expect(findNode(store.doc, alpha.id).device.icon).toBe('not-uploaded');
    expect('icons' in store.doc).toBe(false);
  });

  test('the field and the dialog see the diagram’s copies, then the icon library’s icons', async () => {
    const { doc, alpha } = sampleDocument();
    const { icons, library } = await openInspector(
      doc,
      alpha,
      () => {},
      (store) => {
        store.doc = { ...store.doc, icons: { plc: { data: ICON_DATA } } };
      },
    );

    expect(icons.diagram()).toEqual([{ name: 'plc', data: ICON_DATA }]);
    expect(icons.entry('plc')).toEqual({ name: 'plc', data: ICON_DATA });
    expect(icons.entry('rtu')).toBeUndefined();
    // Nothing of an object's own makes a name look known.
    expect(icons.entry('constructor')).toBeUndefined();

    // A name the server's icon library knows, by any of an icon's names.
    iconLibrary.state.index = indexIcons([
      { name: 'RTU', aliases: ['old-rtu'], data: ICON_DATA },
    ]);

    try {
      expect(icons.entry('old-rtu')).toMatchObject({ data: ICON_DATA });
      expect(icons.entry('rtu')).toMatchObject({ data: ICON_DATA });
    } finally {
      iconLibrary.state.index = indexIcons([]);
    }

    // The dialog's library is the server's.
    expect(library).toBe(iconLibrary);
  });

  // The field shows the error. The device keeps the color it has, and a
  // valid change of another look field made with it still goes through.
  test('a color that is no #rrggbb is not applied, and its field says why', async () => {
    const { doc, alpha } = sampleDocument();
    const filled = updateNode(doc, alpha.id, {
      device: { fillColor: '#2f6fbf' },
    });
    const { wrapper, store, errors, settle } = await openInspector(
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
      'Changed the icon of Device alpha to Router',
    );
    expect(errors().map((error) => error.message)).toEqual([
      'Fill Color must be a hex color, such as #2f6fbf',
    ]);
    // The bad color stays in the form as an unapplied edit, which a save
    // does not apply.
    expect(actionsShown(wrapper)).toBe(true);
    expect(settle()).toBe('1 field needs attention');
    expect(lookOf(store, alpha).fillColor).toBe('#2f6fbf');

    const short = await openInspector(doc, alpha, (data) => {
      data.outlineColor = '#abc';
    });

    expect('outlineColor' in findNode(short.store.doc, alpha.id).device).toBe(
      false,
    );
    expect(short.errors().map((error) => error.message)).toEqual([
      'Outline Color must be a hex color, such as #2f6fbf',
    ]);
  });

  test('other edits made with it stay unapplied until Apply or a save', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper, store, settle, spec } = await openInspector(
      doc,
      alpha,
      (data) => {
        data.fillColor = '#1f7a5a';
        data.spec.general = { ...data.spec.general, description: 'Edited' };
      },
    );

    expect(lookOf(store, alpha).fillColor).toBe('#1f7a5a');
    expect(spec().general.description).not.toBe('Edited');
    expect(actionsShown(wrapper)).toBe(true);

    expect(settle()).toBe('');
    expect(spec().general.description).toBe('Edited');
    expect(lookOf(store, alpha).fillColor).toBe('#1f7a5a');
    expect(store.history.undoLabel()).toBe('Applied changes to Device alpha');
  });

  // Only the look fields a change of the form sets are applied, so a stale
  // color in the form is not put back by another edit or by Apply.
  test('a color changed elsewhere is not put back by an edit of another field', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper, store, settle, spec } = await openInspector(
      doc,
      alpha,
      (data) => {
        data.fillColor = '#2f6fbf';
        data.spec.general = { ...data.spec.general, description: 'Edited' };
      },
    );
    const device = () => findNode(store.doc, alpha.id).device;
    const stale = formData(wrapper);

    expect(device().fillColor).toBe('#2f6fbf');
    store.commit(
      updateNode(store.doc, alpha.id, { device: { fillColor: '#ffd400' } }),
      'Changed elsewhere',
    );
    await flushPromises();

    const edited = formData(wrapper);

    expect(stale.fillColor).toBe('#2f6fbf');
    // The working copy moves onto the device as it is now.
    expect(edited.fillColor).toBe('#ffd400');
    edited.spec.general.description = 'Edited again';
    await sendFormData(wrapper, edited);

    expect(device().fillColor).toBe('#ffd400');
    expect(store.history.undoLabel()).toBe('Changed elsewhere');
    expect(settle()).toBe('');
    expect(spec().general.description).toBe('Edited again');
    expect(device().fillColor).toBe('#ffd400');
    await flushPromises();

    // A new icon chosen then is applied alone.
    await sendFormData(wrapper, { ...formData(wrapper), iconKey: 'router' });

    expect(device()).toMatchObject({ iconKey: 'router', fillColor: '#ffd400' });
    expect(store.history.undoLabel()).toBe(
      'Changed the icon of Device alpha to Router',
    );
  });

  // While a conflict is resolved, the look stays in the form as an unapplied
  // edit, for Apply once it is resolved.
  test('a look the store refuses stays unapplied', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper, store, settle } = await openInspector(
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
    expect(actionsShown(wrapper)).toBe(true);
    expect(settle()).toBe('the conflict is not resolved');

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
});

// Every kind's look applies at once as one Undo step, named after the field.
// What the element is waits for Apply.
describe('the look of every kind is applied without Apply', () => {
  // The Inspector open on `node` of `doc`, with `change` made in the form.
  async function changed(doc, node, change) {
    const opened = await openInspector(doc, node, (data) => {
      Object.assign(data, change);
    });

    return { ...opened, label: () => opened.store.history.undoLabel() };
  }

  test("a switch's colors, its network's color and line style, and its icon size", async () => {
    const { doc, sw } = sampleDocument();
    const { wrapper, store, settle, label } = await changed(doc, sw, {
      fillColor: '#6b6f18',
      outlineColor: '#a3273f',
      color: '#123456',
      iconSize: 'large',
      lineStyle: 'dotted',
    });

    expect(findNode(store.doc, sw.id).switch).toEqual({
      ...sw.switch,
      outlineColor: '#a3273f',
      fillColor: '#6b6f18',
      iconSize: 'large',
    });
    expect(store.doc.networks[0].color).toBe('#123456');
    // The network's line style is in the look too.
    expect(store.doc.networks[0].lineStyle).toBe('dotted');
    expect(label()).toBe('Changed the edge color of Network EXP to #123456');
    expect(actionsShown(wrapper)).toBe(false);
    expect(settle()).toBe('');

    // The look was one edit, so one Undo takes it back.
    store.undo();
    expect(findNode(store.doc, sw.id).switch).toEqual(sw.switch);
    expect(store.doc.networks[0].color).toBe(doc.networks[0].color);
    expect('lineStyle' in store.doc.networks[0]).toBe(false);
  });

  test("a switch's line style alone is one Undo step, and its name waits for Apply", async () => {
    const { doc, sw } = sampleDocument();
    const { wrapper, store, settle, label } = await changed(doc, sw, {
      name: 'LAN',
      lineStyle: 'dash-dot',
    });

    expect(store.doc.networks[0].lineStyle).toBe('dash-dot');
    expect(store.doc.networks[0].name).toBe('EXP');
    expect(label()).toBe('Changed the line style of Network EXP to Dash-dot');
    expect(actionsShown(wrapper)).toBe(true);
    expect(settle()).toBe('');
    expect(store.doc.networks[0].name).toBe('LAN');

    store.undo();
    store.undo();
    expect(store.doc.networks[0]).toEqual(doc.networks[0]);
  });

  test("a connection's label, color and line style", async () => {
    const { doc, edge } = sampleDocument();
    const { wrapper, store, label } = await changed(doc, edge, {
      label: 'uplink',
    });
    const now = () => store.doc.edges.find((entry) => entry.id === edge.id);

    expect(now().label).toBe('uplink');
    expect(label()).toMatch(/^Changed the label of Connection .* to "uplink"$/);
    expect(actionsShown(wrapper)).toBe(false);

    const styled = await changed(doc, edge, {
      color: '#a3273f',
      lineStyle: 'dashed',
    });
    const styledEdge = styled.store.doc.edges.find(
      (entry) => entry.id === edge.id,
    );

    expect(styledEdge).toMatchObject({ color: '#a3273f', lineStyle: 'dashed' });
    expect(styled.label()).toMatch(/^Changed the color of Connection /);
    styled.store.undo();
    expect(
      styled.store.doc.edges.find((entry) => entry.id === edge.id),
    ).toEqual(edge);
  });

  test("a line's label, color and line style, not its points", async () => {
    const { doc } = sampleDocument();
    const { doc: drawn, node } = addNode(doc, {
      kind: 'line',
      position: { x: 40, y: 40 },
    });
    const { wrapper, store, settle, label } = await changed(drawn, node, {
      label: 'Uplink',
      color: '#2f6fbf',
      lineStyle: 'dotted',
    });
    const line = () => findNode(store.doc, node.id).line;

    expect(line()).toMatchObject({
      label: 'Uplink',
      color: '#2f6fbf',
      lineStyle: 'dotted',
    });
    expect(line().points).toEqual(node.line.points);
    expect(label()).toBe('Changed the label of Line to "Uplink"');
    expect(actionsShown(wrapper)).toBe(false);
    expect(settle()).toBe('');

    // The three fields were one change, so one Undo takes them back.
    store.undo();
    expect(findNode(store.doc, node.id).line).toEqual(node.line);

    // A color that is not #rrggbb has an error, and waits.
    const refused = await changed(drawn, node, { color: 'red' });

    expect('color' in findNode(refused.store.doc, node.id).line).toBe(false);
    expect(actionsShown(refused.wrapper)).toBe(true);
  });

  test("a group's color, border pattern, icon and icon size", async () => {
    const { doc } = sampleDocument();
    const { doc: grouped, node } = addNode(doc, {
      kind: 'group',
      title: 'Site',
      icon: 'plc',
    });
    const { wrapper, store, settle, label } = await changed(grouped, node, {
      title: 'Plant',
      color: '#2f6fbf',
      borderStyle: 'double',
      iconKey: 'firewall',
      icon: undefined,
      iconSize: 'large',
    });
    const group = () => findNode(store.doc, node.id).group;

    expect(group()).toMatchObject({
      color: '#2f6fbf',
      borderStyle: 'double',
      iconKey: 'firewall',
      iconSize: 'large',
    });
    expect(group().icon).toBeFalsy();
    expect(group().title).toBe('Site');
    expect(label()).toBe('Changed the color of Group to #2f6fbf');
    // The title waits for Apply.
    expect(actionsShown(wrapper)).toBe(true);
    expect(settle()).toBe('');
    expect(group().title).toBe('Plant');
  });

  test("a note's color, a shape's colors and border, and an icon drawing's icon", async () => {
    const { doc } = sampleDocument();
    const note = addNode(doc, { kind: 'note', text: 'Hello' });
    const noted = await changed(note.doc, note.node, { color: '#fff3bf' });

    expect(findNode(noted.store.doc, note.node.id).note.color).toBe('#fff3bf');
    expect(noted.label()).toBe('Changed the color of Note to #fff3bf');

    const shape = addNode(doc, { kind: 'shape' });
    const shaped = await changed(shape.doc, shape.node, {
      fillColor: '#eef3fb',
      borderStyle: 'dashed',
      label: 'DMZ',
    });

    expect(findNode(shaped.store.doc, shape.node.id).shape).toMatchObject({
      fillColor: '#eef3fb',
      borderStyle: 'dashed',
    });
    expect(findNode(shaped.store.doc, shape.node.id).shape.label).toBeFalsy();
    expect(actionsShown(shaped.wrapper)).toBe(true);

    const icon = addNode(doc, { kind: 'icon', iconKey: 'router' });
    const iconed = await changed(icon.doc, icon.node, { icon: 'plc' });

    // Exactly one icon: the custom one in place of the key.
    expect(findNode(iconed.store.doc, icon.node.id).icon).toMatchObject({
      icon: 'plc',
    });
    expect(findNode(iconed.store.doc, icon.node.id).icon.iconKey).toBeFalsy();
    expect(iconed.label()).toBe('Changed the custom icon of Icon to plc');
    expect(actionsShown(iconed.wrapper)).toBe(false);
  });

  // Choosing a built-in icon over a custom one changes both keys in one
  // change of the form's data (see InspectorIconControl).
  test('a built-in icon in place of a custom icon is one Undo step', async () => {
    const { doc, alpha } = sampleDocument();
    const custom = updateNode(doc, alpha.id, {
      device: { iconKey: 'linux', icon: 'plc' },
    });
    const { wrapper, store, label } = await changed(custom, alpha, {
      iconKey: 'firewall',
      icon: undefined,
    });
    const device = () => findNode(store.doc, alpha.id).device;

    expect(device().iconKey).toBe('firewall');
    expect(device().icon).toBeFalsy();
    expect(label()).toBe('Changed the icon of Device alpha to Firewall');
    expect(actionsShown(wrapper)).toBe(false);

    store.undo();
    expect(device()).toMatchObject({ iconKey: 'linux', icon: 'plc' });
    // What is left to undo is the draft as it was loaded.
    expect(label()).toBe('');
  });

  // A Line style stepped to with keys is held, as Icon size is (see
  // icon-size.test.js), and applied as one step once the choice is made.
  test.each([
    [
      "a connection's",
      'edge',
      ['dashed', 'dotted'],
      /^Changed the line style of Connection .* to Dotted$/,
    ],
    [
      "a switch's",
      'sw',
      ['solid', 'dashed'],
      /^Changed the line style of Network EXP to Dashed$/,
    ],
  ])(
    '%s line style stepped to with keys is applied on Enter',
    async (_, which, steps, named) => {
      const sample = sampleDocument();
      const { wrapper, store, label } = await changed(
        sample.doc,
        sample[which],
        {},
      );
      const select = fieldAt(wrapper, 'lineStyle');
      // What carries the line style: the connection, or the switch's network.
      const styled = () =>
        which === 'edge'
          ? store.doc.edges.find((entry) => entry.id === sample.edge.id)
          : store.doc.networks[0];
      const before = styled();

      for (const step of steps) {
        await select.trigger('keydown', { key: 'ArrowDown' });
        await select.setValue(step);
      }
      expect('lineStyle' in styled()).toBe(false);
      expect(actionsShown(wrapper)).toBe(false);

      await select.trigger('keydown', { key: 'Enter' });
      expect(styled().lineStyle).toBe(steps.at(-1));
      expect(label()).toMatch(named);

      store.undo();
      expect(styled()).toEqual(before);
    },
  );

  // Text typed in a look field brings up no Apply. Escape puts back the text
  // committed last, and is the field's own.
  test('text typed in a label shows no Apply, and Escape puts it back', async () => {
    const { doc, edge, sw } = sampleDocument();
    const { wrapper } = await changed(doc, edge, {});
    const field = fieldAt(wrapper, 'label');
    const reached = vi.fn();

    field.element.focus();
    await field.setValue('uplink');
    await type(field, 'uplink 2');
    expect(wrapper.vm.typing).toBe(true);
    expect(wrapper.vm.typingInstant).toBe(true);
    expect(actionsShown(wrapper)).toBe(false);

    document.addEventListener('keydown', reached);

    try {
      const escape = keydown(field, 'Escape');

      await flushPromises();
      expect(field.element.value).toBe('uplink');
      expect(wrapper.vm.typing).toBe(false);
      expect(escape.defaultPrevented).toBe(true);
      expect(reached).not.toHaveBeenCalled();

      // With nothing typed, Escape is the Inspector's own, as before.
      keydown(field, 'Escape');
      expect(reached).toHaveBeenCalledTimes(1);
    } finally {
      document.removeEventListener('keydown', reached);
    }

    // Text typed in a field that waits for Apply brings up Apply.
    const named = await changed(doc, sw, {});
    const name = fieldAt(named.wrapper, 'name');

    name.element.focus();
    await type(name, 'eth9');
    expect(named.wrapper.vm.typingInstant).toBe(false);
    expect(actionsShown(named.wrapper)).toBe(true);
  });
});

// The form loads a changed element again only while it holds nothing of the
// user's. The document's watch sees each change made elsewhere.
describe('a document change while the form holds the user’s work', () => {
  async function typing() {
    const { doc, alpha } = sampleDocument();
    const opened = await openInspector(doc, alpha, () => {});
    const description = fieldAt(opened.wrapper, 'spec.general.description');

    return { ...opened, alpha, description, resets: opened.wrapper.vm.resets };
  }

  // Commits a change made elsewhere, and lets the Inspector see it.
  async function changeElsewhere({ store }, change, label) {
    store.commit(change(store.doc), label);
    await flushPromises();
  }

  // How often the form loaded the element's data (see reset), which no
  // element shows.
  const resetsOf = ({ wrapper }) => wrapper.vm.resets;
  const iconName = ({ wrapper }) =>
    wrapper.get('[data-testid="inspector-icon-name"]').text();

  test('text being typed is kept when a layout moves the node, and applying records it', async () => {
    const opened = await typing();
    const { wrapper, store, settle, spec, alpha, resets, description } = opened;

    // A field holds text it has not committed.
    description.element.focus();
    await type(description, 'Typed');
    await changeElsewhere(
      opened,
      (doc) => moveNodes(doc, [{ id: alpha.id, position: { x: 480, y: 320 } }]),
      'Applied ELK layered layout',
    );

    // The form was not loaded again, so the field keeps its text.
    expect(resetsOf(opened)).toBe(resets);
    expect(wrapper.vm.typing).toBe(true);
    expect(description.element.value).toBe('Typed');

    // The field commits its text as focus leaves it.
    await description.trigger('change');
    description.element.blur();
    await flushPromises();
    expect(wrapper.vm.typing).toBe(false);

    expect(settle()).toBe('');
    expect(spec().general.description).toBe('Typed');
    expect(findNode(store.doc, alpha.id).position).toEqual({ x: 480, y: 320 });
    expect(store.history.undoLabel()).toBe('Applied changes to Device alpha');
    store.undo();
    expect(spec().general?.description).not.toBe('Typed');
    expect(findNode(store.doc, alpha.id).position).toEqual({ x: 480, y: 320 });
  });

  test('a field changed elsewhere shows in the form, and applying keeps it', async () => {
    const opened = await typing();
    const { wrapper, store, settle, spec, alpha, resets, description } = opened;

    description.element.focus();
    await type(description, 'Typed');
    await changeElsewhere(
      opened,
      (doc) => updateNode(doc, alpha.id, { device: { iconKey: 'router' } }),
      'Changed the icon of Device alpha to Router',
    );

    expect(resetsOf(opened)).toBe(resets);
    expect(iconName(opened)).toBe('Router');
    expect(formData(wrapper).iconKey).toBe('router');
    expect(wrapper.vm.dirty).toBe(false);

    await description.trigger('change');

    expect(settle()).toBe('');
    expect(spec().general.description).toBe('Typed');
    expect(findNode(store.doc, alpha.id).device.iconKey).toBe('router');
  });

  // Text that commits to no change (Memory's default with a space) still
  // brought up Apply, which then holds focus.
  test('focus on Apply or Cancel keeps the working copy too', async () => {
    const opened = await typing();
    const { wrapper, alpha } = opened;
    const memory = fieldAt(wrapper, 'spec.hardware.memory');

    memory.element.focus();
    await type(memory, `${memory.element.value} `);
    expect(actionsShown(wrapper)).toBe(true);

    const apply = wrapper.get('[data-testid="inspector-apply"]');

    apply.element.focus();
    await flushPromises();
    expect(wrapper.vm.dirty).toBe(false);

    const resets = resetsOf(opened);

    await changeElsewhere(
      opened,
      (doc) => updateNode(doc, alpha.id, { device: { iconKey: 'router' } }),
      'Changed the icon of Device alpha to Router',
    );

    expect(resetsOf(opened)).toBe(resets);
    expect(wrapper.vm.held).toBe(true);
    expect(actionsShown(wrapper)).toBe(true);
    expect(document.activeElement).toBe(apply.element);
    expect(iconName(opened)).toBe('Router');
  });

  // Enter applies with focus still in the field. The form shows the value
  // as the document took it (a VLAN takes its network's name).
  test('a value Apply commits shows as the document took it, with focus still in its field', async () => {
    const { doc, bravo } = sampleDocument();
    const { wrapper, store, spec } = await openInspector(doc, bravo, () => {});
    const path = 'spec.network.interfaces.0.vlan';
    const field = fieldAt(wrapper, path);
    const vlan = () => formData(wrapper).spec.network.interfaces[0].vlan;

    field.element.focus();
    await type(field, 'exp');
    expect(actionsShown(wrapper)).toBe(true);
    await submitWithEnter(wrapper, field);

    expect(store.history.undoLabel()).toBe(
      'Updated device bravo and connected eth0 to network EXP',
    );
    expect(spec().network.interfaces[0].vlan).toBe('EXP');
    // The document change has reached the form, with focus in the field.
    expect(vlan()).toBe('EXP');
    expect(fieldAt(wrapper, path).element.value).toBe('EXP');
    expect(document.activeElement).toBe(fieldAt(wrapper, path).element);
    expect(actionsShown(wrapper)).toBe(false);

    // Undo takes the edit back, VLAN and connection.
    store.undo();

    expect(spec().network.interfaces[0].vlan ?? '').toBe('');
  });

  // Save now, and leaving or reading the diagram, apply the edits too.
  test('a value a save applies shows as the document took it too', async () => {
    for (const save of ['settle', 'saveUnapplied']) {
      const { doc, bravo } = sampleDocument();
      const opened = await openInspector(doc, bravo, () => {});
      const { wrapper, spec } = opened;
      const path = 'spec.network.interfaces.0.vlan';
      const field = fieldAt(wrapper, path);

      // The field commits as a save does first, with focus still in it.
      field.element.focus();
      await type(field, 'exp');
      await field.trigger('change');
      opened[save]();
      await flushPromises();

      expect(spec().network.interfaces[0].vlan, save).toBe('EXP');
      expect(formData(wrapper).spec.network.interfaces[0].vlan, save).toBe(
        'EXP',
      );
      expect(fieldAt(wrapper, path).element.value, save).toBe('EXP');
      expect(actionsShown(wrapper), save).toBe(false);
      wrapper.unmount();
    }
  });

  test('with nothing of the user’s in it, the form loads the element again', async () => {
    const opened = await typing();
    const { alpha, resets } = opened;

    await changeElsewhere(
      opened,
      (doc) => updateNode(doc, alpha.id, { device: { iconKey: 'router' } }),
      'Changed the icon of Device alpha to Router',
    );

    expect(resetsOf(opened)).toBe(resets + 1);
    expect(iconName(opened)).toBe('Router');
  });
});

// Position X and Y are text read as numbers.
describe('the Position fields', () => {
  test('take a number with spaces around it, and ArrowUp and ArrowDown step to the next whole pixel', async () => {
    const { doc, alpha } = sampleDocument();
    const { wrapper, store } = await openInspector(doc, alpha, () => {});
    const x = wrapper.get('#inspector-position-x');
    const y = wrapper.get('#inspector-position-y');
    const move = wrapper.get('[data-testid="inspector-move"]');
    const press = async (key, field = x) => {
      const event = keydown(field, key);

      await flushPromises();

      return event;
    };

    await x.setValue(' 3 ');
    await y.setValue('0');
    expect(move.attributes('aria-disabled')).toBe('false');
    expect(x.attributes('aria-valuenow')).toBe('3');

    // Text that is no number is not moved to, nor stepped.
    await x.setValue('3px');
    expect(move.attributes('aria-disabled')).toBe('true');
    expect(x.attributes('aria-valuenow')).toBeUndefined();
    expect((await press('ArrowUp')).defaultPrevented).toBe(false);
    expect(x.element.value).toBe('3px');

    await x.setValue(' 480.5 ');
    await y.setValue('');
    expect((await press('ArrowUp')).defaultPrevented).toBe(true);
    expect(x.element.value).toBe('481');
    expect((await press('ArrowDown')).defaultPrevented).toBe(true);
    await press('ArrowDown');
    expect(x.element.value).toBe('479');
    // An empty field steps from 0.
    await press('ArrowDown', y);
    expect(y.element.value).toBe('-1');

    // Move takes the node to the whole pixels typed.
    await x.setValue(' 3 ');
    await y.setValue('0');
    await move.trigger('click');
    expect(findNode(store.doc, alpha.id).position).toEqual({ x: 3, y: 0 });
  });
});
