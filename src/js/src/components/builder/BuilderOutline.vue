<!--
  Semantic outline.

  This is the accessible mirror of the canvas and a first-class editing
  surface: create, rename, delete, group and connect are all available here
  without a single drag gesture. It lists nodes only; a connection is
  removed with Delete on the canvas, Disconnect in the Inspector's Connection
  points, or Disconnect in the command palette. It
  stays in sync with the canvas because both render the same document.
-->
<template>
  <!-- In a narrow window the outline scrolls. With nothing in it to focus
       (a read-only diagram with no nodes, whose forms are disabled), it is
       focusable itself, so the keyboard can scroll it (WCAG 2.1.1). -->
  <section
    class="builder-outline builder-panel"
    aria-labelledby="outline-title"
    :tabindex="store.readOnly && !outline.length ? 0 : undefined">
    <h2
      id="outline-title"
      ref="titleEl"
      class="builder-outline__title"
      tabindex="-1">
      Outline
    </h2>
    <!-- Describes the list once, not every row on every arrow press. Written
         from the command registry, with this platform's keys. Not shown,
         as the shortcut sheet lists these keys; screen readers still get
         it, since a list of buttons does not say that arrow keys move
         between them, F2 renames or Delete removes. Visually hidden rather
         than hidden, so reading the page reaches it too. -->
    <p :id="HINT_ID" class="builder-visually-hidden">{{ hint }}</p>

    <!-- A list is rendered only with items: an empty list is a list without
         the list items ARIA requires it to own. -->
    <builder-outline-list
      v-if="outline.length"
      :items="outline"
      aria-label="Diagram outline"
      :aria-describedby="HINT_ID"
      class="builder-outline__tree"
      data-testid="builder-outline" />
    <p
      v-else
      class="builder-outline__empty"
      data-testid="builder-outline-empty">
      {{
        store.readOnly
          ? 'No nodes.'
          : 'No nodes yet. Add one from the Add nodes panel.'
      }}
    </p>

    <form class="builder-outline__connect" @submit.prevent="connect">
      <h3 class="builder-outline__subtitle">Add a connection</h3>

      <div class="builder-field">
        <label for="connect-device">Device</label>
        <select
          id="connect-device"
          v-model="connectForm.deviceId"
          :aria-invalid="missingDevice || undefined"
          :aria-describedby="missingDevice ? CONNECT_ERROR_ID : undefined"
          :disabled="store.readOnly">
          <option value="">Select a device</option>
          <option v-for="node in devices" :key="node.id" :value="node.id">
            {{ nodeLabel(node) }}
          </option>
        </select>
      </div>

      <div class="builder-field">
        <label for="connect-interface">Interface</label>
        <select
          id="connect-interface"
          v-model="connectForm.handleId"
          :disabled="store.readOnly">
          <option value="">Add a new interface</option>
          <option
            v-for="handle in availableHandles"
            :key="handle.id"
            :value="handle.id">
            {{ handle.name }}
          </option>
        </select>
      </div>

      <div class="builder-field">
        <label for="connect-switch">Switch</label>
        <select
          id="connect-switch"
          v-model="connectForm.switchId"
          :aria-invalid="missingSwitch || undefined"
          :aria-describedby="missingSwitch ? CONNECT_ERROR_ID : undefined"
          :disabled="store.readOnly">
          <option value="">Select a switch</option>
          <option v-for="node in switches" :key="node.id" :value="node.id">
            {{ switchLabel(node) }}
          </option>
        </select>
      </div>

      <!-- A new key replaces the alert, so a repeated message is announced
           again. Its text follows the fields: once a device is chosen it
           asks only for the switch. -->
      <p
        v-if="connectMessage"
        :id="CONNECT_ERROR_ID"
        :key="connectError.seq"
        class="builder-outline__error"
        role="alert">
        {{ connectMessage }}
      </p>

      <button
        type="submit"
        class="builder-button builder-button--primary"
        :disabled="store.readOnly"
        data-testid="outline-connect">
        <builder-icon name="link" :size="14" />
        Connect
      </button>
    </form>

    <!-- Group membership without a drag: the node moves into the group's box,
         which grows to hold it, or out of the one it leaves. -->
    <form
      class="builder-outline__connect"
      data-testid="outline-regroup"
      @submit.prevent="moveToGroup">
      <h3 class="builder-outline__subtitle">Move to a group</h3>

      <div class="builder-field">
        <label for="regroup-node">Node</label>
        <select
          id="regroup-node"
          v-model="groupForm.nodeId"
          :aria-invalid="missingNode || undefined"
          :aria-describedby="missingNode ? GROUP_ERROR_ID : undefined"
          :disabled="store.readOnly">
          <option value="">Select a node</option>
          <option v-for="node in movable" :key="node.id" :value="node.id">
            {{ nodeLabel(node) }} ({{ node.kind }})
          </option>
        </select>
      </div>

      <div class="builder-field">
        <label for="regroup-group">Group</label>
        <select
          id="regroup-group"
          v-model="groupForm.groupId"
          :disabled="store.readOnly">
          <option value="">No group</option>
          <option v-for="node in groupChoices" :key="node.id" :value="node.id">
            {{ nodeLabel(node) }}
          </option>
        </select>
      </div>

      <p
        v-if="groupError.text"
        :id="GROUP_ERROR_ID"
        :key="groupError.seq"
        class="builder-outline__error"
        role="alert">
        {{ groupError.text }}
      </p>

      <button
        type="submit"
        class="builder-button"
        :disabled="store.readOnly"
        data-testid="outline-regroup-submit">
        <builder-icon name="group" :size="14" />
        Move
      </button>
    </form>

    <section class="builder-outline__networks" aria-labelledby="networks-title">
      <h3
        id="networks-title"
        ref="networksTitleEl"
        class="builder-outline__subtitle"
        tabindex="-1">
        Networks
      </h3>

      <ul
        v-if="networks.length"
        role="list"
        class="builder-outline__tree"
        data-testid="builder-networks">
        <li
          v-for="network in networks"
          :key="network.id"
          class="builder-outline__row">
          <div class="builder-outline__item builder-outline__item--static">
            <builder-icon name="vlan" :size="14" />
            <span class="builder-outline__label" :title="network.name">{{
              network.name
            }}</span>
            <span class="builder-outline__kind">
              {{ network.alias ? `VLAN ${network.alias}` : 'no alias' }}
            </span>
            <span class="builder-outline__count">{{ network.devices }}</span>
            <button
              :id="removeNetworkId(network.id)"
              type="button"
              class="builder-button builder-button--danger"
              :disabled="store.readOnly"
              :aria-label="`Remove network ${network.name}`"
              @click="removeNetwork(network)">
              <builder-icon name="close" :size="12" />
            </button>
          </div>
        </li>
      </ul>
      <p
        v-else
        class="builder-outline__empty"
        data-testid="builder-networks-empty">
        {{
          store.readOnly
            ? 'No networks.'
            : 'No networks yet. Adding a switch creates one.'
        }}
      </p>
    </section>
  </section>
</template>

<script setup>
  import {
    computed,
    inject,
    nextTick,
    provide,
    reactive,
    ref,
    watch,
  } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';
  import BuilderOutlineList from './BuilderOutlineList.vue';

  import { nodeIconKey } from '@/builder/catalog.js';
  import {
    deviceHandles,
    findNetwork,
    findNode,
    includedFrom,
    includedReason,
    isDescendant,
    networkRefusal,
    nodeLabel,
  } from '@/builder/model.js';
  import { outlineHint, textFieldCommand } from '@/builder/commands.js';
  import {
    buildOutline,
    networkOutline,
    rowNodeIds,
  } from '@/builder/outline.js';
  import { pressSelection } from '@/builder/selection.js';
  import { useBuilderStore } from '@/builder/store.js';

  const HINT_ID = 'outline-hint';
  const CONNECT_ERROR_ID = 'connect-error';
  const GROUP_ERROR_ID = 'regroup-error';

  const store = useBuilderStore();
  // The view's command context (BuilderBeta.vue), whose view brings nodes
  // into view on the canvas.
  const commands = inject('builderCommands', null);

  // Id of the row that holds the outline's single tab stop.
  const activeId = ref('');
  const renamingId = ref('');
  const renameValue = ref('');
  // `missing`: Connect was pressed with a field empty, and the message is
  // built from the fields; `refusal`: why the store refused the connection.
  const connectError = reactive({ refusal: '', missing: false, seq: 0 });
  const titleEl = ref(null);
  const networksTitleEl = ref(null);

  const connectForm = reactive({ deviceId: '', handleId: '', switchId: '' });
  const groupForm = reactive({ nodeId: '', groupId: '' });
  // Why Move did nothing; `missing` when no node was chosen.
  const groupError = reactive({ text: '', missing: false, seq: 0 });

  const outline = computed(() => buildOutline(store.doc));
  const hint = computed(() => outlineHint({ readOnly: store.readOnly }));
  const networks = computed(() => networkOutline(store.doc));

  // Rows in the order they are shown, which is the arrow key order.
  const flatItems = computed(() => {
    const flat = [];

    const walk = (items) => {
      items.forEach((item) => {
        flat.push(item);
        walk(item.children || []);
      });
    };

    walk(outline.value);

    return flat;
  });

  // Falls back to the first row when the active row is gone, deleted here, on
  // the canvas or from the toolbar, so Tab always reaches the outline.
  const rovingId = computed(() => {
    const items = flatItems.value;

    return items.some((item) => item.id === activeId.value)
      ? activeId.value
      : items[0]?.id || '';
  });

  // A device from an included topology keeps its connections, so it is not
  // offered.
  const devices = computed(() =>
    (store.doc.nodes || []).filter(
      (node) => node.kind === 'device' && !includedFrom(node),
    ),
  );

  const switches = computed(() =>
    (store.doc.nodes || []).filter((node) => node.kind === 'switch'),
  );

  // Only free interfaces are offered: a handle may take part in exactly one
  // connection, which is the same rule the server enforces.
  const availableHandles = computed(() => {
    const device = findNode(store.doc, connectForm.deviceId);

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

  const missingDevice = computed(
    () => connectError.missing && !connectForm.deviceId,
  );
  const missingSwitch = computed(
    () => connectError.missing && !connectForm.switchId,
  );

  // Names only the fields still empty, so the message never goes stale as
  // they are filled in. A kind the diagram has none of cannot be chosen, so
  // the message says to add one instead.
  const connectMessage = computed(() => {
    if (!connectError.missing) {
      return connectError.refusal;
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

  // The message goes once every field it named has a value.
  watch([missingDevice, missingSwitch], ([device, sw]) => {
    if (connectError.missing && !device && !sw) {
      clearConnectError();
    }
  });

  // Another device has other interfaces, so the one picked for the last
  // goes. A device, switch or interface that is gone (removed, connected
  // elsewhere, undone) is unpicked, so the form never sends what it no
  // longer shows.
  watch(
    () => connectForm.deviceId,
    () => {
      connectForm.handleId = '';
    },
  );
  watch([devices, switches, availableHandles], ([device, sw, handles]) => {
    const has = (list, id) => list.some((item) => item.id === id);

    if (connectForm.deviceId && !has(device, connectForm.deviceId)) {
      connectForm.deviceId = '';
    }
    if (connectForm.switchId && !has(sw, connectForm.switchId)) {
      connectForm.switchId = '';
    }
    if (connectForm.handleId && !has(handles, connectForm.handleId)) {
      connectForm.handleId = '';
    }
  });

  // Every node can move to a group or out of one.
  const movable = computed(() => store.doc.nodes || []);

  // The groups the chosen node can move to: not itself, nor a group inside
  // it.
  const groupChoices = computed(() =>
    (store.doc.nodes || []).filter(
      (node) =>
        node.kind === 'group' &&
        node.id !== groupForm.nodeId &&
        !isDescendant(store.doc, node.id, groupForm.nodeId),
    ),
  );

  const missingNode = computed(() => groupError.missing && !groupForm.nodeId);

  // The group shows where the chosen node is now, so Move takes it
  // elsewhere; a node or group that is gone is unpicked.
  watch(
    () => groupForm.nodeId,
    (id) => {
      groupForm.groupId = findNode(store.doc, id)?.parentId || '';
      if (id) {
        clearGroupError();
      }
    },
  );
  watch([movable, groupChoices], ([nodes, groups]) => {
    if (groupForm.nodeId && !nodes.some((n) => n.id === groupForm.nodeId)) {
      groupForm.nodeId = '';
    }
    if (groupForm.groupId && !groups.some((n) => n.id === groupForm.groupId)) {
      groupForm.groupId = '';
    }
  });

  provide(
    'builderOutline',
    reactive({
      rovingId,
      renamingId,
      renameValue,
      rowId,
      iconFor,
      isSelected,
      onRowClick,
      onRowKeydown,
      onRowFocus,
      commitRename,
      cancelRename,
      onRenameKeydown,
      onRenameChange,
    }),
  );

  function switchLabel(node) {
    const network = findNetwork(store.doc, node.switch?.networkId);

    return network ? `${nodeLabel(node)} (${network.name})` : nodeLabel(node);
  }

  function rowId(id) {
    return `outline-row-${id}`;
  }

  function removeNetworkId(id) {
    return `outline-remove-network-${id}`;
  }

  // Focuses an element by id once the DOM reflects the latest change, or
  // `fallback` when the element is gone.
  function focusLater(id, fallback) {
    nextTick(() => {
      const element = id ? document.getElementById(id) : null;

      (element || fallback?.())?.focus();
    });
  }

  // Each row's icon, worked out once per document: every row asks again
  // whenever the selection changes.
  const iconKeys = computed(() => {
    const keys = new Map();

    for (const node of store.doc.nodes || []) {
      if (!keys.has(node.id)) {
        keys.set(node.id, nodeIconKey(node));
      }
    }

    return keys;
  });

  function iconFor(item) {
    return iconKeys.value.get(item.id) || nodeIconKey({ kind: item.kind });
  }

  function isSelected(id) {
    return store.selection.nodes.includes(id);
  }

  // A plain press selects the row alone, or deselects it when it is already
  // the only thing selected, so the pressed state always toggles; with
  // `additive` it adds the row to the selection or takes it out, so several
  // nodes can be grouped or deleted together. The canvas presses its nodes
  // the same way, and says the same.
  function pressRow(item, additive) {
    const { selection, message } = pressSelection(
      store.doc,
      store.selection,
      { kind: 'nodes', id: item.id },
      additive,
    );

    activeId.value = item.id;
    store.select(selection);
    store.announce(message);
  }

  function focusRow(id) {
    activeId.value = id;
    focusLater(rowId(id), () => titleEl.value);
  }

  function onRowFocus(item) {
    activeId.value = item.id;
  }

  // A plain click, Enter or Space selects or deselects the row; with Shift,
  // Ctrl or Cmd it toggles the row in the selection. The row takes focus, so
  // a row activated from screen reader browse mode is the one that F2 and
  // Delete then act on. The canvas shows the row's nodes, a group's members
  // and a switch's network with them, when any is out of view; focus stays
  // on the row.
  function onRowClick(item, event) {
    const row = document.getElementById(rowId(item.id));

    if (row && document.activeElement !== row) {
      row.focus();
    }

    pressRow(item, event.shiftKey || event.ctrlKey || event.metaKey);
    commands?.view?.revealNode?.(rowNodeIds(store.doc, item.id));
  }

  // Bound to each row rather than the list, so the rename field that takes a
  // row's place keeps its own Enter, Space and arrow keys.
  function onRowKeydown(item, event) {
    const items = flatItems.value;
    const index = items.findIndex((entry) => entry.id === item.id);
    const target = {
      ArrowDown: index + 1,
      ArrowUp: index - 1,
      Home: 0,
      End: items.length - 1,
    }[event.key];

    if (target !== undefined) {
      event.preventDefault();
      focusRow(items[Math.min(items.length - 1, Math.max(0, target))].id);
      return;
    }

    // Handled here rather than left to the button's click, which in Firefox
    // does not carry the Shift key; preventDefault stops that click.
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onRowClick(item, event);
      return;
    }

    if (event.key === 'F2' && !store.readOnly) {
      event.preventDefault();
      startRename(item);
      return;
    }

    // Backspace too: a Mac keyboard's delete key is Backspace, and it has
    // no forward delete without Fn.
    if (
      (event.key === 'Delete' || event.key === 'Backspace') &&
      !store.readOnly
    ) {
      event.preventDefault();
      removeItem(item, index);
    }
  }

  // Delete or Backspace removes the focused row, or the whole selection when
  // the row is part of it. Focus moves to the nearest remaining row,
  // preferring the one above, or to the Outline heading once no rows are
  // left.
  function removeItem(item, index) {
    const order = flatItems.value.map((entry) => entry.id);

    if (isSelected(item.id)) {
      store.removeSelection();
    } else {
      store.remove({ nodes: [item.id], edges: [] });
    }

    const remaining = new Set(flatItems.value.map((entry) => entry.id));

    // A row that could not be removed (see removalRefusal) keeps focus.
    if (remaining.has(item.id)) {
      focusRow(item.id);

      return;
    }
    const next =
      order
        .slice(0, index)
        .reverse()
        .find((id) => remaining.has(id)) ||
      order.slice(index + 1).find((id) => remaining.has(id));

    if (next) {
      focusRow(next);
    } else {
      focusLater('', () => titleEl.value);
    }
  }

  function startRename(item) {
    if (store.readOnly) {
      return;
    }

    // Refused before anything is typed: an included device keeps its name,
    // and so does a network one is on.
    const node = findNode(store.doc, item.id);
    const refusal =
      includedReason(node) ||
      (node?.kind === 'switch'
        ? networkRefusal(store.doc, item.networkId)
        : '');

    if (refusal) {
      store.refuse(refusal);

      return;
    }

    activeId.value = item.id;
    renamingId.value = item.id;
    renameValue.value = item.label;
    nextTick(() => {
      const input = document.getElementById(`rename-${item.id}`);
      input?.focus();
      input?.select();
    });
  }

  function cancelRename(item) {
    if (renamingId.value !== item.id) {
      return;
    }

    renamingId.value = '';
    focusRow(item.id);
  }

  // The rename field keeps its keys to itself, Enter and Escape above all,
  // but for the Builder's keys that work in text fields (Command palette,
  // Save now), which go on to its key dispatcher as they do from any field.
  function onRenameKeydown(event) {
    if (!textFieldCommand(event)) {
      event.stopPropagation();
    }
  }

  // Save now commits the focused field first by sending it a change (see
  // settleEdits in BuilderBeta.vue), which commits the rename as Enter does,
  // focus going back to the row. The change the browser sends as focus
  // leaves, with focus already gone, commits it as leaving does.
  function onRenameChange(item, event) {
    commitRename(item, document.activeElement === event.target);
  }

  // Renaming a node means different things per kind: a device is renamed by its
  // hostname, a switch by the name of the network it publishes. Enter returns
  // focus to the row, which may have moved as rows sort by label; leaving the
  // field (blur) commits without taking focus back.
  function commitRename(item, refocus) {
    if (renamingId.value !== item.id) {
      return;
    }

    const label = renameValue.value.trim();

    renamingId.value = '';

    if (refocus) {
      focusRow(item.id);
    }

    if (!label || label === item.label) {
      return;
    }

    const node = findNode(store.doc, item.id);

    if (!node) {
      return;
    }

    if (node.kind === 'device') {
      store.updateNode(
        item.id,
        { device: { hostname: label } },
        `Renamed device to ${label}`,
      );

      return;
    }

    if (node.kind === 'switch') {
      store.updateNetwork(node.switch.networkId, { name: label });

      return;
    }

    if (node.kind === 'group') {
      store.updateNode(item.id, { group: { title: label } }, 'Renamed group');

      return;
    }

    store.updateNode(item.id, { label }, 'Renamed node');
  }

  function clearConnectError() {
    connectError.refusal = '';
    connectError.missing = false;
  }

  function showConnectError({ refusal = '', missing = false }) {
    connectError.refusal = refusal;
    connectError.missing = missing;
    connectError.seq += 1;
  }

  function connect() {
    if (!connectForm.deviceId || !connectForm.switchId) {
      showConnectError({ missing: true });
      focusLater(connectForm.deviceId ? 'connect-switch' : 'connect-device');

      return;
    }

    // The alert below reads a refusal out; the store need not as well.
    const result = store.connect(
      {
        sourceNodeId: connectForm.deviceId,
        sourceHandleId: connectForm.handleId || null,
        targetNodeId: connectForm.switchId,
      },
      { announce: false },
    );

    if (result.error) {
      showConnectError({ refusal: result.error });

      return;
    }

    clearConnectError();
    connectForm.handleId = '';
  }

  function clearGroupError() {
    groupError.text = '';
    groupError.missing = false;
  }

  function showGroupError(text, missing = false) {
    groupError.text = text;
    groupError.missing = missing;
    groupError.seq += 1;
  }

  // The store announces the move; a move that would change nothing says why
  // here instead. Focus stays on Move.
  function moveToGroup() {
    const node = findNode(store.doc, groupForm.nodeId);

    if (!node) {
      showGroupError(
        movable.value.length
          ? 'Choose a node.'
          : 'Add a node to the diagram first.',
        true,
      );
      focusLater('regroup-node');

      return;
    }

    const groupId = groupForm.groupId || '';

    if ((node.parentId || '') === groupId) {
      const group = findNode(store.doc, groupId);

      showGroupError(
        group
          ? `${nodeLabel(node)} is in ${nodeLabel(group)} already.`
          : `${nodeLabel(node)} is in no group already.`,
      );

      return;
    }

    clearGroupError();
    store.setParent(node.id, groupId || null);
  }

  // Focus moves to the next network's Remove button, or the previous one, or
  // the Networks heading once the list is empty.
  function removeNetwork(network) {
    const list = networks.value;
    const index = list.findIndex((entry) => entry.id === network.id);
    const neighbour = list[index + 1] || list[index - 1];

    // A refused removal leaves focus on its button.
    if (!store.removeNetwork(network.id)) {
      return;
    }

    focusLater(
      neighbour ? removeNetworkId(neighbour.id) : '',
      () => networksTitleEl.value,
    );
  }
</script>

<style scoped>
  .builder-outline__title,
  .builder-outline__subtitle {
    font-weight: 700;
    font-size: 0.9rem;
    margin: 0 0 0.35rem;
  }

  .builder-outline__tree {
    list-style: none;
    margin: 0 0 0.75rem;
    padding: 0;
  }

  .builder-outline__empty {
    font-size: 0.78rem;
    margin: 0 0 0.75rem;
  }

  .builder-outline__error {
    font-size: 0.78rem;
    margin-bottom: 0.35rem;
  }
</style>
