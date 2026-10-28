<!--
  Switch node: one shared bus handle that devices attach to. Vue Flow's wrapper
  is the focusable, named element (see DeviceNode.vue).
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
      :id="SWITCH_HANDLE_ID"
      type="target"
      :position="Position.Left"
      class="builder-handle builder-handle--bus"
      :title="`${data.label} bus`"
      aria-hidden="true" />
    <Handle
      :id="SWITCH_HANDLE_ID"
      type="source"
      :position="Position.Right"
      class="builder-handle builder-handle--bus"
      :title="`${data.label} bus`"
      aria-hidden="true" />
  </div>

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
  import { useNodeColors } from './nodeColors.js';
  import { useNodeIcon } from './nodeIconSize.js';
  import { useNodeInfo } from './nodeTooltip.js';

  import {
    customNetworkColor,
    drawnNetworkColor,
    needsCasing,
  } from '@/builder/colors.js';
  import { DEFAULT_ICON_SIZE, iconPixels } from '@/builder/model.js';
  import { switchInfo } from '@/builder/nodeInfo.js';
  import { SWITCH_HANDLE_ID } from '@/builder/adapters/vueflow.js';

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
    switchInfo(props.data.network, props.data.connected),
  );
  const iconSize = computed(() => props.data.iconSize || DEFAULT_ICON_SIZE);
  const { showIcon, iconClass } = useNodeIcon(() => iconSize.value);
  // The outline without one of its own: its network's color, as its connections
  // draw it. A user color that fades into the canvas gets a ring.
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
