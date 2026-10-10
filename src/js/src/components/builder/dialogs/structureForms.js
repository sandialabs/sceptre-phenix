// The forms of the Add a connection and Move to a group dialogs
// (ConnectDialog.vue, RegroupDialog.vue): what each offers, what it starts
// from, what it says when Connect or Move cannot act, and what those do.
// The dialogs show them. The rules are here, apart from the markup.
//
// Each form starts from the selection when its dialog opens. While the
// dialog is open, the form follows the diagram. When a chosen element
// leaves the diagram (removed, connected elsewhere, undone), the form
// clears that choice. Thus a form never sends what it no longer shows.
// submit() returns {done, focus}. done is true when the store made the
// change. Otherwise, focus is the id of the control that should take focus,
// or ''.

import { computed, reactive, watch } from 'vue';

import {
  deviceHandles,
  findNetwork,
  findNode,
  includedFrom,
  isDescendant,
  nodeLabel,
} from '@/builder/model.js';

// The first node of `list` the selection holds, by id, or ''.
function firstSelected(store, list) {
  const ids = new Set(store.selection?.nodes || []);

  return list.find((node) => ids.has(node.id))?.id || '';
}

// How many times each text is in `texts`.
function tally(texts) {
  const counts = new Map();

  texts.forEach((text) => counts.set(text, (counts.get(text) || 0) + 1));

  return counts;
}

/**
 * The names that a dialog's list gives its nodes. The names tell every
 * option apart for a keyboard or screen reader user. Each node gets its own
 * `name(node)`. Where two or more nodes share a name, as unlabelled drawings
 * of a kind do ("Line (line)"), each of those names adds the node's position
 * as the Inspector shows it ("Line (line) at 120, 48"). Where nodes share
 * that too, each name also adds the node's place among them ("…, 2 of 3").
 *
 * @param {object[]} nodes
 * @param {(node: object) => string} name
 * @returns {Map<string, string>} each node's id, and its name in the list
 */
export function optionNames(nodes, name) {
  const plain = nodes.map((node) => name(node));
  const plainCounts = tally(plain);
  const placed = nodes.map((node, index) => {
    if (plainCounts.get(plain[index]) < 2) {
      return plain[index];
    }

    const x = Math.round(node.position?.x ?? 0);
    const y = Math.round(node.position?.y ?? 0);

    return `${plain[index]} at ${x}, ${y}`;
  });
  const placedCounts = tally(placed);
  const seen = new Map();

  return new Map(
    nodes.map((node, index) => {
      const text = placed[index];
      const total = placedCounts.get(text);

      if (total < 2) {
        return [node.id, text];
      }

      const place = (seen.get(text) || 0) + 1;

      seen.set(text, place);

      return [node.id, `${text}, ${place} of ${total}`];
    }),
  );
}

/**
 * The form of Add a connection: a device, one of its free interfaces or a
 * new one, and a switch. A selected device fills Device, and a selected
 * switch fills Switch.
 *
 * @param {object} store the Builder store: doc, selection, readOnly and
 *   connect(connection, {announce})
 * @returns {object} form (deviceId, handleId, switchId), error, and the
 *   computed devices, switches, availableHandles, missingDevice,
 *   missingSwitch and message, plus switchLabel(node) and submit()
 */
export function useConnectForm(store) {
  // A device from an included topology keeps its connections, so it is
  // not offered.
  const devices = computed(() =>
    (store.doc.nodes || []).filter(
      (node) => node.kind === 'device' && !includedFrom(node),
    ),
  );

  const switches = computed(() =>
    (store.doc.nodes || []).filter((node) => node.kind === 'switch'),
  );

  const form = reactive({
    deviceId: firstSelected(store, devices.value),
    handleId: '',
    switchId: firstSelected(store, switches.value),
  });

  // `missing`: Connect was pressed with a field empty, and the message is
  // built from the fields. `refusal`: why the store refused the
  // connection. A new seq replaces the message, so a repeated one is
  // announced again.
  const error = reactive({ refusal: '', missing: false, seq: 0 });

  // Only free interfaces are offered: a handle may take part in exactly
  // one connection, which is the same rule the server enforces.
  const availableHandles = computed(() => {
    const device = findNode(store.doc, form.deviceId);

    if (!device) {
      return [];
    }

    const used = new Set(
      (store.doc.edges || []).flatMap((edge) =>
        [edge.sourceHandleId, edge.targetHandleId].filter(Boolean),
      ),
    );

    return deviceHandles(device).filter((handle) => !used.has(handle.id));
  });

  const missingDevice = computed(() => error.missing && !form.deviceId);
  const missingSwitch = computed(() => error.missing && !form.switchId);

  // Names only the fields still empty, so the message never goes stale as
  // the user fills them. A kind the diagram has none of cannot be chosen,
  // so the message says to add one instead.
  const message = computed(() => {
    if (!error.missing) {
      return error.refusal;
    }

    const empty = [
      missingDevice.value && { kind: 'device', options: devices.value },
      missingSwitch.value && { kind: 'switch', options: switches.value },
    ].filter(Boolean);
    const choose = empty.filter((field) => field.options.length);
    const add = empty.filter((field) => !field.options.length);
    const kinds = (fields) => fields.map((field) => field.kind).join(' and a ');

    return [
      choose.length ? `Choose a ${kinds(choose)}.` : '',
      add.length ? `Add a ${kinds(add)} to the diagram first.` : '',
    ]
      .filter(Boolean)
      .join(' ');
  });

  function clearError() {
    error.refusal = '';
    error.missing = false;
  }

  function showError({ refusal = '', missing = false }) {
    error.refusal = refusal;
    error.missing = missing;
    error.seq += 1;
  }

  // The message clears when every field it named has a value.
  watch([missingDevice, missingSwitch], ([device, sw]) => {
    if (error.missing && !device && !sw) {
      clearError();
    }
  });

  // Another device has other interfaces, so clear the interface picked for
  // the last device.
  watch(
    () => form.deviceId,
    () => {
      form.handleId = '';
    },
  );
  watch([devices, switches, availableHandles], ([device, sw, handles]) => {
    const has = (list, id) => list.some((item) => item.id === id);

    if (form.deviceId && !has(device, form.deviceId)) {
      form.deviceId = '';
    }
    if (form.switchId && !has(sw, form.switchId)) {
      form.switchId = '';
    }
    if (form.handleId && !has(handles, form.handleId)) {
      form.handleId = '';
    }
  });

  // A switch is named with its network, which is what a connection to it
  // joins.
  function switchLabel(node) {
    const network = findNetwork(store.doc, node.switch?.networkId);

    return network ? `${nodeLabel(node)} (${network.name})` : nodeLabel(node);
  }

  // With a field empty, the message names it and the first empty field
  // takes focus. The form's message reads out a refusal of the connection
  // or of the edit (a conflict being resolved). For this reason, the store
  // does not also announce the refusal. The store says what was connected.
  // Only an edge that the store returns means the change was made.
  function submit() {
    if (store.readOnly) {
      return { done: false, focus: '' };
    }

    if (!form.deviceId || !form.switchId) {
      showError({ missing: true });

      return {
        done: false,
        focus: form.deviceId ? 'connect-switch' : 'connect-device',
      };
    }

    const result = store.connect(
      {
        sourceNodeId: form.deviceId,
        sourceHandleId: form.handleId || null,
        targetNodeId: form.switchId,
      },
      { announce: false },
    );

    if (!result?.edge) {
      showError({ refusal: result?.error || 'Not connected.' });

      return { done: false, focus: '' };
    }

    clearError();

    return { done: true, focus: '' };
  }

  return {
    form,
    error,
    devices,
    switches,
    availableHandles,
    missingDevice,
    missingSwitch,
    message,
    switchLabel,
    submit,
  };
}

/**
 * The form of Move to a group: a node, and the group it goes into, or No
 * group. The first selected node fills Node, and Group shows the group it
 * is in now, so Move takes it elsewhere.
 *
 * @param {object} store the Builder store: doc, selection, readOnly,
 *   editRefusal and setParent(id, parentId, {announce})
 * @returns {object} form (nodeId, groupId), error, and the computed
 *   movable, groupChoices, nodeNames, groupNames and missingNode, plus
 *   submit()
 */
export function useRegroupForm(store) {
  // Every node can move to a group or out of one.
  const movable = computed(() => store.doc.nodes || []);

  const first = findNode(store.doc, store.selection?.nodes?.[0]);

  const form = reactive({
    nodeId: first?.id || '',
    groupId: first?.parentId || '',
  });

  // Why Move did nothing. `missing` is true when no node was chosen. A new seq
  // replaces the message, so a repeated one is announced again.
  const error = reactive({ text: '', missing: false, seq: 0 });

  // The groups the chosen node can move to: not itself, nor a group inside
  // it.
  const groupChoices = computed(() =>
    (store.doc.nodes || []).filter(
      (node) =>
        node.kind === 'group' &&
        node.id !== form.nodeId &&
        !isDescendant(store.doc, node.id, form.nodeId),
    ),
  );

  // What each option of Node and of Group reads: a node by its label and
  // kind, a group by its title, told apart where they read alike (see
  // optionNames).
  const nodeNames = computed(() =>
    optionNames(movable.value, (node) => `${nodeLabel(node)} (${node.kind})`),
  );
  const groupNames = computed(() =>
    optionNames(groupChoices.value, (node) => nodeLabel(node)),
  );

  const missingNode = computed(() => error.missing && !form.nodeId);

  function clearError() {
    error.text = '';
    error.missing = false;
  }

  function showError(text, missing = false) {
    error.text = text;
    error.missing = missing;
    error.seq += 1;
  }

  // Group shows where the chosen node is now. A node or group that is gone
  // is cleared from the form.
  watch(
    () => form.nodeId,
    (id) => {
      form.groupId = findNode(store.doc, id)?.parentId || '';
      if (id) {
        clearError();
      }
    },
  );
  watch([movable, groupChoices], ([nodes, groups]) => {
    if (form.nodeId && !nodes.some((node) => node.id === form.nodeId)) {
      form.nodeId = '';
    }
    if (form.groupId && !groups.some((node) => node.id === form.groupId)) {
      form.groupId = '';
    }
  });

  // The store says where the node went. A move that would change nothing,
  // or that the store refuses (a conflict being resolved), says why
  // instead. Focus then stays where it is.
  function submit() {
    if (store.readOnly) {
      return { done: false, focus: '' };
    }

    const node = findNode(store.doc, form.nodeId);

    if (!node) {
      showError(
        movable.value.length
          ? 'Choose a node.'
          : 'Add a node to the diagram first.',
        true,
      );

      return { done: false, focus: 'regroup-node' };
    }

    const groupId = form.groupId || '';

    if ((node.parentId || '') === groupId) {
      const group = findNode(store.doc, groupId);

      showError(
        group
          ? `${nodeLabel(node)} is in ${nodeLabel(group)} already.`
          : `${nodeLabel(node)} is in no group already.`,
      );

      return { done: false, focus: '' };
    }

    if (!store.setParent(node.id, groupId || null, { announce: false })) {
      showError(store.editRefusal || `${nodeLabel(node)} was not moved.`);

      return { done: false, focus: '' };
    }

    clearError();

    return { done: true, focus: '' };
  }

  return {
    form,
    error,
    movable,
    groupChoices,
    nodeNames,
    groupNames,
    missingNode,
    submit,
  };
}
