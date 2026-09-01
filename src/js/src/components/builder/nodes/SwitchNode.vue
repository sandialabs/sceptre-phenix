<!-- Switch node: a single shared bus handle that devices attach to. -->
<template>
  <div
    class="builder-node builder-node--switch"
    :class="{ 'is-selected': selected }"
    :data-node-id="id"
    data-node-kind="switch"
    data-testid="builder-node"
    tabindex="0"
    role="button"
    :aria-label="ariaLabel"
    :aria-describedby="comment ? `${id}-comment` : undefined"
    @mouseenter="showTooltip = true"
    @mouseleave="showTooltip = false"
    @focus="showTooltip = true"
    @blur="showTooltip = false">
    <div class="builder-node__header">
      <builder-icon :name="data.iconKey" :size="16" />
      <span class="builder-node__label">{{ data.label }}</span>
    </div>
    <span class="builder-node__kind">Switch</span>
    <span class="builder-node__meta">{{ networkText }}</span>

    <div
      v-if="comment && showTooltip"
      :id="`${id}-comment`"
      class="builder-tooltip"
      role="tooltip">
      {{ comment }}
    </div>

    <Handle
      id="bus"
      type="target"
      :position="Position.Left"
      class="builder-handle builder-handle--bus"
      :aria-label="`${data.label} bus, incoming`" />
    <Handle
      id="bus"
      type="source"
      :position="Position.Right"
      class="builder-handle builder-handle--bus"
      :aria-label="`${data.label} bus, outgoing`" />
  </div>
</template>

<script setup>
  import { computed, ref } from 'vue';
  import { Handle, Position } from '@vue-flow/core';

  import BuilderIcon from '../BuilderIcon.vue';

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const showTooltip = ref(false);
  const comment = computed(() => props.data.comment || '');
  const ariaLabel = computed(
    () => props.data.node.ariaLabel || `Switch ${props.data.label}`,
  );
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
