<!--
  Device node: icon + hostname + interface handles.

  Vue Flow's wrapper around this component takes focus (the canvas's roving
  Tab stop, see BuilderCanvas.vue) and carries its accessible name (see
  adapters/vueflow.js), so nothing in here is focusable. Under its name the
  device shows its phenix node type as stored (VirtualMachine, Router, ...),
  or External. The comment (spec.general.description) ends the accessible
  name and is the device's last line. Hover and keyboard focus show the
  device's info tooltip (see nodeTooltip.js), and the hidden text beside
  the node says the same to assistive technology, as the wrapper's
  description.

  A device from an included topology is read only: it says where it comes
  from in place of its type, has a dashed border, and offers no handle for a
  new interface. Its accessible name says the same.

  A device may have an outline color and a fill color of its own (see
  nodeColors in colors.js). On a fill, every line of text and the icon are
  black or white, whichever reads on it. A custom icon, when the device has
  one, is drawn in place of the icon of its key, in its own colors.

  The device's notes (its spec's general.notes) show in a card below its
  box (see NodeNotes.vue), and its description ends with them.

  Its icon is drawn at the device's icon size, or the diagram's (see
  nodeIconSize in model.js). A Small icon sits before the hostname; a Medium
  or a Large one stands left of all its lines, which make a column beside it
  (see iconSizeClass in nodeIconSize.js and builder.css). The box keeps its
  size either way.
-->
<template>
  <div
    class="builder-node builder-node--device"
    :class="[
      {
        'is-selected': selected,
        'builder-node--included': Boolean(data.includedFrom),
      },
      iconClass,
      colorClasses,
    ]"
    :style="colorStyle"
    :data-node-id="id"
    :data-node-kind="'device'"
    :data-icon-size="iconSize"
    data-testid="builder-node">
    <div class="builder-node__header">
      <builder-icon
        :name="data.iconKey"
        :src="data.iconSrc"
        :size="iconPixels(iconSize)" />
      <span class="builder-node__label">{{ data.label }}</span>
    </div>
    <span
      v-if="data.includedFrom"
      class="builder-node__origin"
      data-testid="node-included-from">
      Included from {{ data.includedFrom }}
    </span>
    <span
      v-else
      class="builder-node__kind builder-node__kind--type"
      data-testid="node-type">
      {{ data.typeLabel }}
    </span>
    <span class="builder-node__meta">
      {{ interfaceCount }}
      {{ interfaceCount === 1 ? 'interface' : 'interfaces' }}
    </span>
    <span v-if="comment" class="builder-node__comment">{{ comment }}</span>
    <node-issue-mark v-if="data.issue" :node-id="id" :issue="data.issue" />

    <!-- Handles are pointer-only: the toolbar's Add connection dialog, also
         reached from the command palette, is the keyboard path, so they are
         hidden from assistive technology. -->
    <Handle
      v-for="(handle, index) in data.handles"
      :id="handle.id"
      :key="handle.id"
      type="source"
      :position="Position.Right"
      class="builder-handle"
      :style="handleStyle(index)"
      :class="{ 'is-connected': handle.connected }"
      :title="handle.label"
      :data-interface="handle.name"
      aria-hidden="true" />

    <Handle
      v-for="(handle, index) in data.handles"
      :id="handle.id"
      :key="`${handle.id}-target`"
      type="target"
      :position="Position.Left"
      class="builder-handle"
      :class="{ 'is-connected': handle.connected }"
      :style="handleStyle(index)"
      :title="handle.label"
      aria-hidden="true" />

    <!-- Drag from here, or drop a connection anywhere on the device, to join
         a switch or another device on a new interface. -->
    <Handle
      v-if="!data.includedFrom"
      :id="NEW_INTERFACE_HANDLE_ID"
      type="source"
      :position="Position.Bottom"
      class="builder-handle builder-handle--new"
      data-testid="device-new-interface"
      :title="`Drag to connect ${data.label} on a new interface`"
      aria-hidden="true" />
  </div>

  <node-notes
    kind="device"
    :notes="notes"
    :selected="selected"
    :color-style="colorStyle" />

  <!-- What the info tooltip shows, as the wrapper's description. It is
       beside the node, not in it, so the node's own text stays what the
       node shows. -->
  <span :id="infoId" class="builder-node__info" hidden>{{ infoText }}</span>
</template>

<script setup>
  import { computed } from 'vue';
  import { Handle, Position } from '@vue-flow/core';

  import BuilderIcon from '../BuilderIcon.vue';
  import NodeIssueMark from './NodeIssueMark.vue';
  import NodeNotes from './NodeNotes.vue';
  import { useNodeColors } from './nodeColors.js';
  import { iconSizeClass } from './nodeIconSize.js';
  import { useNodeInfo } from './nodeTooltip.js';

  import { NEW_INTERFACE_HANDLE_ID } from '@/builder/adapters/vueflow.js';
  // Not nodeNotes: <node-notes> would resolve to it before NodeNotes.
  import {
    DEFAULT_ICON_SIZE,
    iconPixels,
    nodeNotes as notesOf,
  } from '@/builder/model.js';
  import { deviceInfo } from '@/builder/nodeInfo.js';

  // Vue Flow passes its node state as attributes as well; none belong on
  // the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const comment = computed(() => props.data.comment || '');
  const { colorClasses, colorStyle } = useNodeColors(
    () => props.data.node.device,
  );
  const { infoId, infoText } = useNodeInfo(() => deviceInfo(props.data.node));
  const interfaceCount = computed(() => props.data.handles.length);
  const notes = computed(() => notesOf(props.data.node));
  const iconSize = computed(() => props.data.iconSize || DEFAULT_ICON_SIZE);
  const iconClass = computed(() => iconSizeClass(iconSize.value));

  function handleStyle(index) {
    const count = Math.max(1, props.data.handles.length);
    const step = 100 / (count + 1);

    return { top: `${step * (index + 1)}%` };
  }
</script>
