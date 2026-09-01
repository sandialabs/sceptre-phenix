<!--
  Semantic outline.

  This is the accessible mirror of the canvas and a first-class editing
  surface: create, rename, delete, group, connect and disconnect are all
  available here without a single drag gesture. It stays in sync with the
  canvas because both render the same document.
-->
<template>
  <section
    class="builder-outline builder-panel"
    aria-labelledby="outline-title">
    <h2 id="outline-title" class="builder-outline__title">Outline</h2>
    <p class="builder-outline__hint">
      Keyboard: up and down arrows move, Enter selects, F2 renames, Delete
      removes.
    </p>

    <ul
      aria-label="Diagram outline"
      class="builder-outline__tree"
      data-testid="builder-outline"
      @keydown="onKeydown">
      <li
        v-for="(item, index) in flatItems"
        :key="item.id"
        class="builder-outline__row">
        <div
          :role="renamingId === item.id ? undefined : 'button'"
          :aria-pressed="
            renamingId === item.id ? undefined : isSelected(item.id)
          "
          :aria-label="item.accessibleName"
          :tabindex="index === focusIndex ? 0 : -1"
          :ref="(el) => registerRow(el, index)"
          class="builder-outline__item"
          :data-testid="`outline-item-${item.id}`"
          :style="{ paddingLeft: `${0.35 + item.depth * 0.9}rem` }"
          @click="selectItem(item, index)"
          @focus="focusIndex = index">
          <builder-icon :name="iconFor(item)" :size="14" />

          <template v-if="renamingId === item.id">
            <label class="builder-visually-hidden" :for="`rename-${item.id}`">
              Rename {{ item.label }}
            </label>
            <input
              :id="`rename-${item.id}`"
              v-model="renameValue"
              class="builder-outline__rename"
              type="text"
              @keydown.stop.enter="commitRename(item)"
              @keydown.stop.esc="renamingId = ''"
              @blur="commitRename(item)" />
          </template>
          <template v-else>
            <span class="builder-outline__label">{{ item.label }}</span>
            <span class="builder-outline__kind">{{ item.kind }}</span>
          </template>
        </div>

        <ul v-if="item.connections.length" class="builder-outline__connections">
          <li v-for="link in item.connections" :key="link.id">
            <span>{{ link.accessibleName }}</span>
            <button
              type="button"
              class="builder-button builder-button--danger"
              :disabled="store.readOnly"
              :aria-label="`Disconnect ${link.accessibleName}`"
              @click="disconnect(link)">
              <builder-icon name="close" :size="12" />
            </button>
          </li>
        </ul>
      </li>
    </ul>

    <form class="builder-outline__connect" @submit.prevent="connect">
      <h3 class="builder-outline__subtitle">Add a connection</h3>

      <div class="builder-field">
        <label for="connect-device">Device</label>
        <select
          id="connect-device"
          v-model="connectForm.deviceId"
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
          :disabled="store.readOnly">
          <option value="">Select a switch</option>
          <option v-for="node in switches" :key="node.id" :value="node.id">
            {{ switchLabel(node) }}
          </option>
        </select>
      </div>

      <p v-if="connectError" class="builder-outline__error" role="alert">
        {{ connectError }}
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

    <section class="builder-outline__networks" aria-labelledby="networks-title">
      <h3 id="networks-title" class="builder-outline__subtitle">Networks</h3>

      <ul class="builder-outline__tree" data-testid="builder-networks">
        <li
          v-for="network in networks"
          :key="network.id"
          class="builder-outline__row">
          <div
            class="builder-outline__item"
            :aria-label="network.accessibleName">
            <builder-icon name="vlan" :size="14" />
            <span class="builder-outline__label">{{ network.name }}</span>
            <span class="builder-outline__kind">
              {{ network.alias ? `VLAN ${network.alias}` : 'no alias' }}
            </span>
            <button
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

      <form class="builder-outline__connect" @submit.prevent="addNetwork">
        <div class="builder-field">
          <label for="network-name">New network name</label>
          <input
            id="network-name"
            v-model="networkForm.name"
            type="text"
            :disabled="store.readOnly" />
        </div>
        <div class="builder-field">
          <label for="network-alias">VLAN alias (optional)</label>
          <input
            id="network-alias"
            v-model="networkForm.alias"
            type="number"
            min="1"
            max="4094"
            :disabled="store.readOnly" />
        </div>
        <button
          type="submit"
          class="builder-button"
          :disabled="store.readOnly"
          data-testid="outline-add-network">
          <builder-icon name="plus" :size="14" />
          Add network
        </button>
      </form>
    </section>
  </section>
</template>

<script setup>
  import { computed, nextTick, reactive, ref } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';

  import { nodeIconKey } from '@/builder/catalog.js';
  import {
    deviceHandles,
    findNetwork,
    findNode,
    nodeLabel,
  } from '@/builder/model.js';
  import { buildOutline, networkOutline } from '@/builder/outline.js';
  import { useBuilderStore } from '@/builder/store.js';

  const store = useBuilderStore();

  const focusIndex = ref(0);
  const renamingId = ref('');
  const renameValue = ref('');
  const connectError = ref('');
  const rows = new Map();

  const connectForm = reactive({ deviceId: '', handleId: '', switchId: '' });
  const networkForm = reactive({ name: '', alias: '' });

  const outline = computed(() => buildOutline(store.doc));
  const networks = computed(() => networkOutline(store.doc));

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

  const devices = computed(() =>
    (store.doc.nodes || []).filter((node) => node.kind === 'device'),
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

  function switchLabel(node) {
    const network = findNetwork(store.doc, node.switch?.networkId);

    return network ? `${nodeLabel(node)} (${network.name})` : nodeLabel(node);
  }

  function registerRow(el, index) {
    if (el) {
      rows.set(index, el);
    } else {
      rows.delete(index);
    }
  }

  function iconFor(item) {
    const node = findNode(store.doc, item.id);

    return nodeIconKey(node || { kind: item.kind });
  }

  function isSelected(id) {
    return store.selection.nodes.includes(id);
  }

  function selectItem(item, index) {
    focusIndex.value = index;
    store.select({ nodes: [item.id], edges: [] });
  }

  function focusRow(index) {
    focusIndex.value = index;
    nextTick(() => rows.get(index)?.focus());
  }

  function startRename(item) {
    if (store.readOnly) {
      return;
    }

    renamingId.value = item.id;
    renameValue.value = item.label;
    nextTick(() => {
      const input = document.getElementById(`rename-${item.id}`);
      input?.focus();
      input?.select();
    });
  }

  // Renaming a node means different things per kind: a device is renamed by its
  // hostname, a switch by the name of the network it publishes.
  function commitRename(item) {
    if (renamingId.value !== item.id) {
      return;
    }

    const label = renameValue.value.trim();

    renamingId.value = '';

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

  function disconnect(link) {
    store.remove({ nodes: [], edges: [link.id] });
  }

  function connect() {
    connectError.value = '';

    if (!connectForm.deviceId || !connectForm.switchId) {
      connectError.value = 'Choose both a device and a switch.';

      return;
    }

    const result = store.connect({
      sourceNodeId: connectForm.deviceId,
      sourceHandleId: connectForm.handleId || null,
      targetNodeId: connectForm.switchId,
    });

    if (result.error) {
      connectError.value = result.error;

      return;
    }

    connectForm.handleId = '';
  }

  function addNetwork() {
    const alias = Number.parseInt(networkForm.alias, 10);
    const network = store.addNetwork({
      name: networkForm.name || 'EXP',
      alias: Number.isInteger(alias) ? alias : undefined,
    });

    networkForm.name = '';
    networkForm.alias = '';

    return network;
  }

  function removeNetwork(network) {
    store.removeNetwork(network.id);
  }

  function onKeydown(event) {
    const items = flatItems.value;

    if (!items.length) {
      return;
    }

    const item = items[focusIndex.value];

    if (event.key === 'ArrowDown') {
      event.preventDefault();
      focusRow(Math.min(items.length - 1, focusIndex.value + 1));
      return;
    }

    if (event.key === 'ArrowUp') {
      event.preventDefault();
      focusRow(Math.max(0, focusIndex.value - 1));
      return;
    }

    if (event.key === 'Home') {
      event.preventDefault();
      focusRow(0);
      return;
    }

    if (event.key === 'End') {
      event.preventDefault();
      focusRow(items.length - 1);
      return;
    }

    if (!item) {
      return;
    }

    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      selectItem(item, focusIndex.value);
      return;
    }

    if (event.key === 'F2' && !store.readOnly) {
      event.preventDefault();
      startRename(item);
      return;
    }

    if (event.key === 'Delete' && !store.readOnly) {
      event.preventDefault();
      store.remove({ nodes: [item.id], edges: [] });
      focusRow(Math.max(0, focusIndex.value - 1));
    }
  }
</script>

<style scoped>
  .builder-outline__title,
  .builder-outline__subtitle {
    font-weight: 700;
    font-size: 0.9rem;
    margin: 0 0 0.35rem;
  }

  .builder-outline__hint {
    font-size: 0.75rem;
    margin-bottom: 0.5rem;
  }

  .builder-outline__tree {
    list-style: none;
    margin: 0 0 0.75rem;
    padding: 0;
  }

  .builder-outline__kind {
    font-size: 0.7rem;
    text-transform: uppercase;
    opacity: 0.75;
  }

  .builder-outline__connections li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.35rem;
    padding: 0.1rem 0;
  }

  .builder-outline__error {
    font-size: 0.78rem;
    margin-bottom: 0.35rem;
  }

  .builder-outline__rename {
    flex: 1;
  }
</style>
