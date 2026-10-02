<!--
  Switch node: a single shared bus handle that devices attach to. Vue Flow's
  wrapper is the focusable, named element (see DeviceNode.vue). Hover and
  keyboard focus show the switch's info tooltip: its network, and the
  devices connected to it (see nodeTooltip.js).

  The swatch is its network's color, which its connections are drawn in. A
  switch may also have an outline color and a fill color of its own, as a
  device may (see nodeColors in colors.js).
-->
<template>
  <div
    class="builder-node builder-node--switch"
    :class="[{ 'is-selected': selected }, colorClasses]"
    :style="colorStyle"
    :data-node-id="id"
    data-node-kind="switch"
    data-testid="builder-node">
    <div class="builder-node__header">
      <builder-icon :name="data.iconKey" :size="16" />
      <span class="builder-node__label">{{ data.label }}</span>
    </div>
    <div class="builder-node__line">
      <span class="builder-node__kind">Switch</span>
      <!-- The color its connections are drawn in; the name says the rest. -->
      <span
        v-if="swatch"
        class="builder-node__swatch"
        :style="{ '--bx-swatch': swatch }"
        aria-hidden="true"></span>
      <span class="builder-node__meta">{{ networkText }}</span>
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
  import { useNodeInfo } from './nodeTooltip.js';

  import { drawnNetworkColor } from '@/builder/colors.js';
  import { switchInfo } from '@/builder/nodeInfo.js';

  // Vue Flow passes its node state as attributes as well; none belong on
  // the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const { colorClasses, colorStyle } = useNodeColors(
    () => props.data.node.switch,
  );
  const { infoId, infoText } = useNodeInfo(() =>
    switchInfo(props.data.network, props.data.connected),
  );
  // As its connections and the Inspector's Color chip draw it: a color
  // addNetwork picks in its theme token, and no color in the token of the
  // network's place (see networkStyle).
  const swatch = computed(() => {
    const network = props.data.network;
    const token = props.data.networkStyle?.token;

    if (!network) {
      return '';
    }

    return (
      drawnNetworkColor(network.color) ||
      (Number.isInteger(token) ? `var(--bx-net-${token})` : '')
    );
  });
  const networkText = computed(() => {
    const network = props.data.network;

    if (!network) {
      return 'No network';
    }

    return network.alias
      ? `Network ${network.name}, VLAN alias ${network.alias}`
      : `Network ${network.name}`;
  });
</script>
