<!--
  Device node: icon + hostname + interface handles.

  The node is a real button so it is reachable with the keyboard, and its
  comment (spec.general.description) is exposed both as a tooltip on
  hover/focus and through the accessible description.
-->
<template>
  <div
    class="builder-node builder-node--device"
    :class="{ 'is-selected': selected }"
    :data-node-id="id"
    :data-node-kind="'device'"
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
    <span class="builder-node__kind">Device</span>
    <span class="builder-node__meta">
      {{ interfaceCount }}
      {{ interfaceCount === 1 ? 'interface' : 'interfaces' }}
    </span>
    <span v-if="comment" class="builder-node__comment">{{ comment }}</span>

    <div
      v-if="comment && showTooltip"
      :id="`${id}-comment`"
      class="builder-tooltip"
      role="tooltip">
      {{ comment }}
    </div>

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
      :aria-label="handle.label" />

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
      :aria-label="handle.label" />
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
  const ariaLabel = computed(() => `Device ${props.data.label}`);
  const interfaceCount = computed(() => props.data.handles.length);

  function handleStyle(index) {
    const count = Math.max(1, props.data.handles.length);
    const step = 100 / (count + 1);

    return { top: `${step * (index + 1)}%` };
  }
</script>
