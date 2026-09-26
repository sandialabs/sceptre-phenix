<!--
  Switch node: a single shared bus handle that devices attach to. Vue Flow's
  wrapper is the focusable, named element (see DeviceNode.vue).
-->
<template>
  <div
    class="builder-node builder-node--switch"
    :class="{ 'is-selected': selected }"
    :data-node-id="id"
    data-node-kind="switch"
    data-testid="builder-node"
    @mouseenter="showTooltip"
    @mouseleave="hideTooltip">
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

    <div
      v-if="comment"
      v-show="tooltipOpen"
      class="builder-tooltip builder-tooltip--node"
      data-testid="node-tooltip"
      aria-hidden="true">
      {{ comment }}
    </div>

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
</template>

<script setup>
  import { computed } from 'vue';
  import { Handle, Position } from '@vue-flow/core';

  import BuilderIcon from '../BuilderIcon.vue';
  import { useNodeTooltip } from './nodeTooltip.js';

  import { drawnNetworkColor } from '@/builder/colors.js';

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const comment = computed(() => props.data.comment || '');
  const { tooltipOpen, showTooltip, hideTooltip } = useNodeTooltip(() =>
    Boolean(comment.value),
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
