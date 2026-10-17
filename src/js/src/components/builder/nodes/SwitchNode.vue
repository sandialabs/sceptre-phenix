<!--
  Switch node: a single shared bus handle that devices attach to. Vue Flow's
  wrapper is the focusable, named element (see DeviceNode.vue). Hover and
  keyboard focus show the switch's info tooltip: its network, and the
  devices connected to it (see nodeTooltip.js).

  The switch shows only its icon and its name, which is its network's name.
  Its accessible name says that it is a switch, and gives its network and
  VLAN alias (see nodeAccessibleName in diagramText.js). Its outline is the
  color its connections are drawn in, unless it has an outline color of its
  own. It may also have a fill color, as a device may (see nodeColors in
  colors.js), and notes (switch.notes), in a card below its box (see
  NodeNotes.vue). Its description ends with these notes.

  Its icon is drawn at the switch's icon size, or the diagram's. As on a
  device (see DeviceNode.vue), a Small icon is before the name, and a larger
  icon is left of the name. While the setting Show node
  icons is off, it draws no icon (see useNodeIcon in nodeIconSize.js).
-->
<template>
  <div
    class="builder-node builder-node--switch"
    :class="[{ 'is-selected': selected }, iconClass, colorClasses]"
    :style="colorStyle"
    :data-node-id="id"
    data-node-kind="switch"
    :data-icon-size="iconSize"
    data-testid="builder-node">
    <div class="builder-node__header">
      <builder-icon
        v-if="showIcon"
        :name="data.iconKey"
        :size="iconPixels(iconSize)" />
      <span class="builder-node__label">{{ data.label }}</span>
    </div>

    <node-issue-mark v-if="data.issue" :node-id="id" :issue="data.issue" />

    <Handle
      id="bus"
      type="target"
      :position="Position.Left"
      class="builder-handle builder-handle--bus"
      :title="`${data.label} bus`"
      aria-hidden="true" />
    <Handle
      id="bus"
      type="source"
      :position="Position.Right"
      class="builder-handle builder-handle--bus"
      :title="`${data.label} bus`"
      aria-hidden="true" />
  </div>

  <node-notes
    kind="switch"
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
  import { useNodeIcon } from './nodeIconSize.js';
  import { useNodeInfo } from './nodeTooltip.js';

  import {
    customNetworkColor,
    drawnNetworkColor,
    needsCasing,
  } from '@/builder/colors.js';
  // Not nodeNotes: <node-notes> would resolve to it before NodeNotes.
  import {
    DEFAULT_ICON_SIZE,
    iconPixels,
    nodeNotes as notesOf,
  } from '@/builder/model.js';
  import { switchInfo } from '@/builder/nodeInfo.js';

  // Vue Flow also passes its node state as attributes. None of them belong
  // on the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const { colorClasses, colorStyle } = useNodeColors(
    () => props.data.node.switch,
    () => ({
      layerColor: props.data.layerColor,
      defaultOutline: networkOutline.value,
    }),
  );
  const { infoId, infoText } = useNodeInfo(() =>
    switchInfo(props.data.network, props.data.connected, props.data.node),
  );
  const notes = computed(() => notesOf(props.data.node));
  const iconSize = computed(() => props.data.iconSize || DEFAULT_ICON_SIZE);
  const { showIcon, iconClass } = useNodeIcon(() => iconSize.value);
  // The outline a switch takes without one of its own: its network's color,
  // as its connections draw it. A color that addNetwork picks shows in its
  // theme token, which keeps 3:1 against the canvas. A network with no
  // color shows in the token of its place (see networkStyle). A color the
  // user chose gets a ring where it would fade into the canvas, as its
  // connections get a casing.
  const networkOutline = computed(() => {
    const network = props.data.network;
    const token = props.data.networkStyle?.token;

    if (!network) {
      return null;
    }

    const custom = customNetworkColor(network.color);

    if (custom) {
      return { color: custom, low: needsCasing(custom) };
    }

    const color =
      drawnNetworkColor(network.color) ||
      (Number.isInteger(token) ? `var(--bx-net-${token})` : '');

    return color ? { color, low: null } : null;
  });
</script>
