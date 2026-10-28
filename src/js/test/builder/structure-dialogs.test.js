// The Add a connection and Move to a group forms (structureForms.js) and
// dialogs, rendered on the server. Focus and keys are left to e2e.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { nextTick, reactive } from 'vue';
import { createPinia } from 'pinia';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import ConnectDialog from '@/components/builder/dialogs/ConnectDialog.vue';
import RegroupDialog from '@/components/builder/dialogs/RegroupDialog.vue';
import {
  optionNames,
  useConnectForm,
  useRegroupForm,
} from '@/components/builder/dialogs/structureForms.js';
import {
  READ_ONLY,
  availability,
  defaultKeys,
  getCommand,
  isCustomizable,
  runCommand,
} from '@/builder/commands.js';
import { loadShortcutSettings, setShortcut } from '@/builder/keymap.js';
import {
  addNode,
  connect,
  createDocument,
  findNode,
  groupNodes,
  removeElements,
  setParent,
} from '@/builder/model.js';
import { shortcutGroups } from '@/builder/shortcuts.js';
import { useBuilderStore } from '@/builder/store.js';

import { memoryStorage, sampleDocument, tags, testId } from './fixtures.js';
import { renderWithStore } from './render.js';

// A store with what the forms use. connect and setParent change the document
// as the Builder store's do and say whether they did.
function formStore(doc, { selected = [], readOnly = false } = {}) {
  const store = reactive({
    doc,
    selection: { nodes: selected, edges: [] },
    readOnly,
    editRefusal: '',
    connect: vi.fn((connection) => {
      const result = connect(store.doc, connection);

      if (result.error) {
        return { error: result.error };
      }

      store.doc = result.doc;

      return { edge: result.edge };
    }),
    setParent: vi.fn((id, parentId) => {
      const next = setParent(store.doc, id, parentId);

      if (next === store.doc) {
        return null;
      }

      store.doc = next;

      return { id: testId() };
    }),
  });

  return store;
}

// The sample diagram with alpha in a group named Lab.
function groupedDocument() {
  const sample = sampleDocument();
  const { doc, group } = groupNodes(sample.doc, [sample.alpha.id], {
    title: 'Lab',
  });

  return { ...sample, doc, group };
}

describe('the commands that open the dialogs', () => {
  // The editor's context, with the view's openDialog.
  function context({ readOnly = false } = {}) {
    return {
      store: {
        doc: sampleDocument().doc,
        readOnly,
        selection: { nodes: [], edges: [] },
        announce: vi.fn(),
      },
      view: { editing: true, openDialog: vi.fn() },
    };
  }

  test('are in the palette with no keys of their own, and open the same dialogs as the Inspector', () => {
    for (const [id, title, group, dialog] of [
      ['dialog.connect', 'Add a connection…', 'Add', 'connect'],
      ['dialog.regroup', 'Move to a group…', 'Structure', 'regroup'],
    ]) {
      const command = getCommand(id);
      const ctx = context();

      expect(command, id).toMatchObject({ title, group });
      expect(command.palette, id).not.toBe(false);
      expect(defaultKeys(id, 'mac'), id).toEqual([]);
      expect(runCommand(id, ctx), id).toBe(true);
      expect(ctx.view.openDialog, id).toHaveBeenCalledExactlyOnceWith(dialog);
    }
  });

  test('are unavailable in a read-only diagram, which says why', () => {
    for (const id of ['dialog.connect', 'dialog.regroup']) {
      const ctx = context({ readOnly: true });

      expect(availability(id, ctx), id).toBe(READ_ONLY);
      expect(runCommand(id, ctx), id).toBe(false);
      expect(ctx.view.openDialog, id).not.toHaveBeenCalled();
      expect(ctx.store.announce, id).toHaveBeenCalledWith(READ_ONLY);
    }
  });
});

describe('the Add a connection form', () => {
  test('starts from the selection: a selected device and a selected switch', () => {
    const { doc, sw, bravo } = sampleDocument();
    const { form } = useConnectForm(
      formStore(doc, { selected: [sw.id, bravo.id] }),
    );

    expect({ ...form }).toEqual({
      deviceId: bravo.id,
      handleId: '',
      switchId: sw.id,
    });

    const empty = useConnectForm(formStore(doc));

    expect({ ...empty.form }).toEqual({
      deviceId: '',
      handleId: '',
      switchId: '',
    });
  });

  test('offers a device’s free interfaces only, and forgets the one picked when the device changes', async () => {
    const { doc, alpha, bravo } = sampleDocument();
    const { form, availableHandles } = useConnectForm(formStore(doc));

    // alpha's eth0 is connected already.
    form.deviceId = alpha.id;
    expect(availableHandles.value).toEqual([]);

    form.deviceId = bravo.id;
    expect(availableHandles.value.map((handle) => handle.name)).toEqual([
      'eth0',
    ]);
    form.handleId = bravo.device.interfaces[0].id;
    form.deviceId = alpha.id;
    await nextTick();
    expect(form.handleId).toBe('');
  });

  test('with a field empty, names the fields still empty and focuses the first', () => {
    const { doc, bravo } = sampleDocument();
    const store = formStore(doc);
    const { form, message, missingDevice, missingSwitch, submit } =
      useConnectForm(store);

    expect(submit()).toEqual({ done: false, focus: 'connect-device' });
    expect(message.value).toBe('Choose a device and a switch.');
    expect([missingDevice.value, missingSwitch.value]).toEqual([true, true]);

    // The message follows the fields as they are filled in.
    form.deviceId = bravo.id;
    expect(message.value).toBe('Choose a switch.');
    expect(missingDevice.value).toBe(false);
    expect(submit()).toEqual({ done: false, focus: 'connect-switch' });
    expect(store.connect).not.toHaveBeenCalled();
  });

  test('says to add a kind the diagram has none of', () => {
    const blank = useConnectForm(formStore(createDocument({ id: testId() })));

    blank.submit();
    expect(blank.message.value).toBe(
      'Add a device and a switch to the diagram first.',
    );

    const { doc: devicesOnly } = addNode(createDocument({ id: testId() }), {
      kind: 'device',
      hostname: 'solo',
      position: { x: 0, y: 0 },
    });
    const noSwitch = useConnectForm(formStore(devicesOnly));

    noSwitch.submit();
    expect(noSwitch.message.value).toBe(
      'Choose a device. Add a switch to the diagram first.',
    );
  });

  test('connects on a new interface or a free one, without the store saying a refusal too', () => {
    const { doc, sw, bravo } = sampleDocument();
    const store = formStore(doc, { selected: [bravo.id, sw.id] });
    const { submit, message } = useConnectForm(store);

    expect(submit()).toEqual({ done: true, focus: '' });
    expect(store.connect).toHaveBeenCalledWith(
      { sourceNodeId: bravo.id, sourceHandleId: null, targetNodeId: sw.id },
      { announce: false },
    );
    expect(store.doc.edges).toHaveLength(2);
    expect(message.value).toBe('');

    const again = formStore(doc);
    const chosen = useConnectForm(again);

    chosen.form.deviceId = bravo.id;
    chosen.form.handleId = bravo.device.interfaces[0].id;
    chosen.form.switchId = sw.id;
    expect(chosen.submit().done).toBe(true);
    expect(again.connect.mock.calls[0][0].sourceHandleId).toBe(
      bravo.device.interfaces[0].id,
    );
  });

  test('shows why the store refused a connection, and stays', () => {
    const { doc, sw, bravo } = sampleDocument();
    const store = formStore(doc, { selected: [bravo.id, sw.id] });

    store.connect.mockReturnValueOnce({ error: 'That cannot be connected.' });

    const { submit, message, error } = useConnectForm(store);

    expect(submit()).toEqual({ done: false, focus: '' });
    expect(message.value).toBe('That cannot be connected.');

    // The same refusal again is a new message, announced again.
    const seq = error.seq;

    store.connect.mockReturnValueOnce({ error: 'That cannot be connected.' });
    submit();
    expect(error.seq).toBe(seq + 1);
  });

  test('a read-only diagram connects nothing', () => {
    const { doc, sw, bravo } = sampleDocument();
    const store = formStore(doc, {
      selected: [bravo.id, sw.id],
      readOnly: true,
    });
    const { submit, message } = useConnectForm(store);

    expect(submit()).toEqual({ done: false, focus: '' });
    expect(store.connect).not.toHaveBeenCalled();
    expect(message.value).toBe('');
  });

  test('unpicks a switch the diagram no longer has', async () => {
    const { doc, sw, bravo } = sampleDocument();
    const store = formStore(doc, { selected: [bravo.id, sw.id] });
    const { form } = useConnectForm(store);

    store.doc = removeElements(store.doc, { nodes: [sw.id], edges: [] });
    await nextTick();
    expect(form.switchId).toBe('');
    expect(form.deviceId).toBe(bravo.id);
  });
});

describe('the Move to a group form', () => {
  test('starts on the first selected node, in the group it is in', () => {
    const { doc, alpha, group } = groupedDocument();
    const { form } = useRegroupForm(formStore(doc, { selected: [alpha.id] }));

    expect({ ...form }).toEqual({ nodeId: alpha.id, groupId: group.id });

    const none = useRegroupForm(formStore(doc));

    expect({ ...none.form }).toEqual({ nodeId: '', groupId: '' });
  });

  test('offers only the groups the node can go into', () => {
    const { doc, alpha, group } = groupedDocument();
    const outer = groupNodes(doc, [group.id], { title: 'Site' });
    const { form, groupChoices } = useRegroupForm(formStore(outer.doc));

    form.nodeId = alpha.id;
    expect(groupChoices.value.map((node) => node.id).sort()).toEqual(
      [group.id, outer.group.id].sort(),
    );

    // Not itself, nor a group inside it.
    form.nodeId = outer.group.id;
    expect(groupChoices.value).toEqual([]);
  });

  test('says when no node is chosen, and focuses Node', () => {
    const { doc } = groupedDocument();
    const store = formStore(doc);
    const { submit, error, missingNode } = useRegroupForm(store);

    expect(submit()).toEqual({ done: false, focus: 'regroup-node' });
    expect(error.text).toBe('Choose a node.');
    expect(missingNode.value).toBe(true);

    const blank = useRegroupForm(formStore(createDocument({ id: testId() })));

    blank.submit();
    expect(blank.error.text).toBe('Add a node to the diagram first.');
    expect(store.setParent).not.toHaveBeenCalled();
  });

  test('a move that changes nothing says why', async () => {
    const { doc, alpha, bravo } = groupedDocument();
    const store = formStore(doc, { selected: [alpha.id] });
    const { form, submit, error } = useRegroupForm(store);

    expect(submit()).toEqual({ done: false, focus: '' });
    expect(error.text).toBe('alpha is in Lab already.');

    // Choosing another node shows the group it is in, none, and takes the
    // message away.
    form.nodeId = bravo.id;
    await nextTick();
    expect(form.groupId).toBe('');
    expect(error.text).toBe('');
    submit();
    expect(error.text).toBe('bravo is in no group already.');
    expect(store.setParent).not.toHaveBeenCalled();
  });

  test('moves a node into a group and out of one', async () => {
    const { doc, alpha, bravo, group } = groupedDocument();
    const store = formStore(doc, { selected: [bravo.id] });
    const { form, submit } = useRegroupForm(store);

    form.groupId = group.id;
    expect(submit()).toEqual({ done: true, focus: '' });
    // The form says why a move is refused, so the store does not.
    expect(store.setParent).toHaveBeenLastCalledWith(bravo.id, group.id, {
      announce: false,
    });

    form.nodeId = alpha.id;
    await nextTick();
    // Group follows the node chosen.
    expect(form.groupId).toBe(group.id);
    form.groupId = '';
    expect(submit().done).toBe(true);
    expect(store.setParent).toHaveBeenLastCalledWith(alpha.id, null, {
      announce: false,
    });
  });

  test('a move the store does not make says why, and stays', () => {
    const { doc, bravo, group } = groupedDocument();
    const store = formStore(doc, { selected: [bravo.id] });
    const { form, submit, error } = useRegroupForm(store);

    form.groupId = group.id;
    store.editRefusal =
      'Not changed: the conflict is not resolved yet. Edit again after you resolve it.';
    store.setParent.mockReturnValueOnce(null);
    expect(submit()).toEqual({ done: false, focus: '' });
    expect(error.text).toBe(
      'Not changed: the conflict is not resolved yet. Edit again after you resolve it.',
    );

    // Without a reason from the store, the message still says nothing
    // moved, as a new message.
    const seq = error.seq;

    store.editRefusal = '';
    store.setParent.mockReturnValueOnce(null);
    expect(submit().done).toBe(false);
    expect(error.text).toBe('The Builder did not move bravo.');
    expect(error.seq).toBe(seq + 1);
    expect(store.doc).toEqual(doc);
  });

  test('a read-only diagram moves nothing', () => {
    const { doc, bravo, group } = groupedDocument();
    const store = formStore(doc, { selected: [bravo.id], readOnly: true });
    const { form, submit } = useRegroupForm(store);

    form.groupId = group.id;
    expect(submit()).toEqual({ done: false, focus: '' });
    expect(store.setParent).not.toHaveBeenCalled();
  });

  test('tells apart nodes that would read alike, by where they are', () => {
    const sample = groupedDocument();
    let { doc } = sample;
    const add = (options) => {
      const added = addNode(doc, options);

      doc = added.doc;

      return added.node;
    };
    const first = add({ kind: 'line', position: { x: 0, y: 400 } });
    const second = add({ kind: 'line', position: { x: 320, y: 400 } });
    const circle = add({
      kind: 'shape',
      shape: 'circle',
      position: { x: 640, y: 400 },
    });
    // Two groups given the same title.
    const zone = add({
      kind: 'group',
      title: 'Zone',
      position: { x: 0, y: 800 },
    });
    const twin = add({
      kind: 'group',
      title: 'Zone',
      position: { x: 480, y: 800 },
    });
    const { nodeNames, groupNames } = useRegroupForm(formStore(doc));
    const at = (node) => `at ${node.position.x}, ${node.position.y}`;

    expect(nodeNames.value.get(first.id)).toBe(`Line (line) ${at(first)}`);
    expect(nodeNames.value.get(second.id)).toBe(`Line (line) ${at(second)}`);
    // A name no other node has stays as it is.
    expect(nodeNames.value.get(circle.id)).toBe('Circle (shape)');
    expect(nodeNames.value.get(sample.alpha.id)).toBe('alpha (device)');
    expect(groupNames.value.get(zone.id)).toBe(`Zone ${at(zone)}`);
    expect(groupNames.value.get(twin.id)).toBe(`Zone ${at(twin)}`);
    expect(groupNames.value.get(sample.group.id)).toBe('Lab');

    // Every option reads differently.
    const names = [...nodeNames.value.values()];

    expect(new Set(names).size).toBe(names.length);
  });
});

describe('the names of a list of nodes', () => {
  const at = (id, x, y) => ({ id, position: { x, y } });
  const plain = () => 'Line';

  test('add where a node is only to names that others share', () => {
    expect([
      ...optionNames(
        [at('a', 0, 0), at('b', 10.4, 20.6), { id: 'c', position: null }],
        (node) => (node.id === 'c' ? 'Circle' : 'Line'),
      ),
    ]).toEqual([
      ['a', 'Line at 0, 0'],
      ['b', 'Line at 10, 21'],
      ['c', 'Circle'],
    ]);
  });

  test('add a place among those that share a position too', () => {
    expect([
      ...optionNames([at('a', 5, 5), at('b', 5, 5), at('c', 9, 9)], plain),
    ]).toEqual([
      ['a', 'Line at 5, 5, 1 of 2'],
      ['b', 'Line at 5, 5, 2 of 2'],
      ['c', 'Line at 9, 9'],
    ]);
    expect([...optionNames([], plain)]).toEqual([]);
  });
});

describe('an edit the Builder store refuses', () => {
  const RESOLVING =
    'Not changed: the conflict is not resolved yet. Edit again after you resolve it.';

  // The Builder store on the grouped sample diagram, with bravo and the
  // switch selected, while a conflict is being resolved.
  function resolving() {
    const sample = groupedDocument();
    const store = useBuilderStore(createPinia());

    store.doc = sample.doc;
    store.selection = { nodes: [sample.bravo.id, sample.sw.id], edges: [] };
    store.resolvingConflict = true;

    return { ...sample, store };
  }

  test('keeps either dialog open with the reason, said once, and changes nothing', () => {
    const { store, bravo, group } = resolving();
    const seq = store.announcementSeq;
    const connectForm = useConnectForm(store);

    expect(store.editRefusal).toBe(RESOLVING);
    expect(connectForm.submit()).toEqual({ done: false, focus: '' });
    expect(connectForm.message.value).toBe(RESOLVING);
    expect(store.doc.edges).toHaveLength(1);

    const regroupForm = useRegroupForm(store);

    regroupForm.form.groupId = group.id;
    expect(regroupForm.submit()).toEqual({ done: false, focus: '' });
    expect(regroupForm.error.text).toBe(RESOLVING);
    expect(findNode(store.doc, bravo.id).parentId).toBeFalsy();

    // The dialogs' alerts say it. The store does not say it as well.
    expect(store.announcementSeq).toBe(seq);
    expect(store.error).toBe('');
  });

  test('connect and setParent report a refused edit to every caller', () => {
    const { store, sw, bravo, group } = resolving();
    const connection = {
      sourceNodeId: bravo.id,
      sourceHandleId: null,
      targetNodeId: sw.id,
    };

    // A caller that shows the refusal itself is told it, and nothing is
    // announced.
    expect(store.connect(connection, { announce: false })).toEqual({
      error: RESOLVING,
    });
    expect(store.setParent(bravo.id, group.id, { announce: false })).toBeNull();
    expect(store.announcement).toBe('');

    // Otherwise commit announces it, and connect returns it, not an edge
    // that was never added.
    expect(store.connect(connection)).toEqual({ error: RESOLVING });
    expect(store.announcement).toBe(RESOLVING);
    expect(store.setParent(bravo.id, group.id)).toBeNull();
    expect(store.doc.edges).toHaveLength(1);

    // A read-only draft is refused the same way.
    store.resolvingConflict = false;
    store.readOnly = true;
    expect(store.editRefusal).toBe('This draft is read only.');
    expect(store.connect(connection, { announce: false })).toEqual({
      error: 'This draft is read only.',
    });
    expect(store.setParent(bravo.id, group.id, { announce: false })).toBeNull();
    expect(store.error).toBe('');
  });
});

async function render(component, prepare) {
  return (await renderWithStore(component, { prepare })).html;
}

// The markup of the <select> of that id, to its end.
function select(html, id) {
  const start = html.indexOf(`<select id="${id}"`);

  expect(start, id).toBeGreaterThan(-1);

  return html.slice(start, html.indexOf('</select>', start));
}

// The texts of the options of the <select> of that id, in order.
function options(html, id) {
  return [...select(html, id).matchAll(/<option[^>]*>([^<]*)<\/option>/g)].map(
    ([, text]) => text.trim(),
  );
}

// The text of the option the <select> of that id starts on.
function chosen(html, id) {
  return (
    select(html, id)
      .match(/<option[^>]*\sselected\b[^>]*>([^<]*)</)?.[1]
      .trim() || ''
  );
}

describe('Add connection and Move to group', () => {
  afterEach(() => loadShortcutSettings(memoryStorage()));

  // The Inspector beside the canvas, on the node `id` of `doc`.
  async function inspector(doc, id, { readOnly = false } = {}) {
    return render(BuilderInspector, (store) => {
      store.commit(doc, '');
      store.readOnly = readOnly;
      store.select({ nodes: [id], edges: [] });
    });
  }

  // The Inspector header's buttons, by test id, in order.
  function headButtons(html) {
    const start = html.indexOf('builder-inspector__head-actions');
    const head =
      start < 0 ? '' : html.slice(start, html.indexOf('</div>', start));

    return tags(head, 'button').map((tag) => [
      tag.match(/data-testid="([^"]+)"/)?.[1],
      tag,
    ]);
  }

  test('are in the Inspector’s header before Open Node Editor', async () => {
    const { doc, alpha } = sampleDocument();
    const buttons = headButtons(await inspector(doc, alpha.id));

    expect(buttons.map(([testid]) => testid)).toEqual([
      'inspector-connect',
      'inspector-regroup',
      'inspector-node-editor',
    ]);
    for (const [testid, tag] of buttons) {
      expect(tag, testid).toContain('type="button"');
      expect(tag, testid).toContain('aria-haspopup="dialog"');
    }
    expect(buttons[0][1]).toContain('aria-label="Add connection"');
    expect(buttons[1][1]).toContain('aria-label="Move to group"');
    // No keys by default.
    expect(buttons[0][1]).not.toContain('aria-keyshortcuts');
    expect(buttons[1][1]).not.toContain('aria-keyshortcuts');
  });

  test('a group or a drawing has only Move to group; a read-only draft neither', async () => {
    const { doc, group } = groupedDocument();
    const note = addNode(doc, { kind: 'note', text: 'n' });

    for (const id of [group.id, note.node.id]) {
      expect(
        headButtons(await inspector(note.doc, id)).map(([testid]) => testid),
      ).toEqual(['inspector-regroup', 'inspector-node-editor']);
    }
    expect(
      headButtons(await inspector(note.doc, group.id, { readOnly: true })).map(
        ([testid]) => testid,
      ),
    ).toEqual(['inspector-node-editor']);
  });

  test('can have keys of their own, which the sheet lists and the buttons name', async () => {
    for (const id of ['dialog.connect', 'dialog.regroup']) {
      expect(isCustomizable(id), id).toBe(true);
    }
    const rows = shortcutGroups({ customize: true }).flatMap((group) =>
      group.rows.map((row) => [row.id, group.group]),
    );

    expect(rows).toContainEqual(['dialog.connect', 'Add']);
    expect(rows).toContainEqual(['dialog.regroup', 'Structure']);

    setShortcut('dialog.connect', ['Alt+Shift+C'], memoryStorage());
    setShortcut('dialog.regroup', ['Alt+Shift+G'], memoryStorage());
    const { doc, alpha } = sampleDocument();
    const [connect, regroup] = headButtons(await inspector(doc, alpha.id));

    expect(connect[1]).toContain('aria-keyshortcuts="Alt+Shift+C"');
    expect(regroup[1]).toContain('aria-keyshortcuts="Alt+Shift+G"');
  });
});

describe('the dialogs, as rendered', () => {
  test('Add a connection is a labelled dialog that starts from the selection', async () => {
    const { doc, sw, bravo } = sampleDocument();
    const html = await render(ConnectDialog, (store) => {
      store.doc = doc;
      store.selection = { nodes: [bravo.id, sw.id], edges: [] };
    });
    const [dialog] = tags(html, 'dialog');
    const [submit] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="connect-dialog-submit"'),
    );

    expect(dialog).toContain('data-testid="connect-dialog"');
    expect(dialog).toContain('aria-modal="true"');
    expect(dialog).toContain('aria-labelledby="connect-dialog-title"');
    expect(html).toMatch(
      /<h2 id="connect-dialog-title"[^>]*>Add a connection<\/h2>/,
    );
    for (const [id, label] of [
      ['connect-device', 'Device'],
      ['connect-interface', 'Interface'],
      ['connect-switch', 'Switch'],
    ]) {
      expect(html).toMatch(
        new RegExp(`<label for="${id}"[^>]*>${label}</label>`),
      );
    }
    expect(options(html, 'connect-device')).toEqual([
      'Select a device',
      'alpha',
      'bravo',
    ]);
    // The selected device and switch fill their fields, and Interface
    // starts on a new one.
    expect(chosen(html, 'connect-device')).toBe('bravo');
    expect(chosen(html, 'connect-switch')).toMatch(/\(EXP\)$/);
    expect(chosen(html, 'connect-interface')).toBe('Add a new interface');
    // A switch is named with its network.
    expect(options(html, 'connect-switch')).toEqual([
      'Select a switch',
      expect.stringMatching(/\(EXP\)$/),
    ]);
    // Interface offers a new one first, then the selected device's free
    // interface.
    expect(options(html, 'connect-interface')).toEqual([
      'Add a new interface',
      'eth0',
    ]);
    expect(submit).toContain('type="submit"');
    expect(submit).not.toMatch(/\sdisabled\b/);
    // The message region is there, empty, before any message.
    expect(html).toMatch(/<p id="connect-error"[^>]*role="alert"[^>]*>\s*<!--/);
  });

  test('Move to a group is a labelled dialog that names each node’s kind', async () => {
    const { doc, group } = groupedDocument();
    const html = await render(RegroupDialog, (store) => {
      store.doc = doc;
      store.selection = { nodes: [group.id], edges: [] };
    });
    const [dialog] = tags(html, 'dialog');

    expect(dialog).toContain('data-testid="regroup-dialog"');
    expect(dialog).toContain('aria-modal="true"');
    expect(dialog).toContain('aria-labelledby="regroup-dialog-title"');
    expect(html).toMatch(
      /<h2 id="regroup-dialog-title"[^>]*>Move to a group<\/h2>/,
    );
    expect(options(html, 'regroup-node')).toEqual(
      expect.arrayContaining([
        'Select a node',
        'alpha (device)',
        'bravo (device)',
        'Lab (group)',
      ]),
    );
    // The selected group is the node to move, and it cannot go into itself.
    expect(chosen(html, 'regroup-node')).toBe('Lab (group)');
    expect(options(html, 'regroup-group')).toEqual(['No group']);
    expect(html).toContain('data-testid="regroup-dialog-submit"');
    expect(html).toMatch(/<p id="regroup-error"[^>]*role="alert"[^>]*>\s*<!--/);
  });

  test('Move to a group names two unlabelled lines apart', async () => {
    const one = addNode(groupedDocument().doc, {
      kind: 'line',
      position: { x: 0, y: 400 },
    });
    const two = addNode(one.doc, {
      kind: 'line',
      position: { x: 320, y: 400 },
    });
    const html = await render(RegroupDialog, (store) => {
      store.doc = two.doc;
    });
    const lines = options(html, 'regroup-node').filter((text) =>
      text.startsWith('Line (line)'),
    );

    expect(lines).toEqual([
      `Line (line) at ${one.node.position.x}, ${one.node.position.y}`,
      `Line (line) at ${two.node.position.x}, ${two.node.position.y}`,
    ]);
  });

  test('in a read-only diagram, every field and the submit are disabled', async () => {
    const { doc, alpha } = groupedDocument();
    const readOnly = (store) => {
      store.doc = doc;
      store.selection = { nodes: [alpha.id], edges: [] };
      store.readOnly = true;
    };

    for (const [component, ids] of [
      [
        ConnectDialog,
        [
          'connect-device',
          'connect-interface',
          'connect-switch',
          'connect-dialog-submit',
        ],
      ],
      [
        RegroupDialog,
        ['regroup-node', 'regroup-group', 'regroup-dialog-submit'],
      ],
    ]) {
      const html = await render(component, readOnly);

      for (const id of ids) {
        const [control] = [
          ...tags(html, 'select'),
          ...tags(html, 'button'),
        ].filter(
          (tag) =>
            tag.includes(`id="${id}"`) || tag.includes(`data-testid="${id}"`),
        );

        expect(control, id).toMatch(/\sdisabled\b/);
      }
    }
  });
});
