<!--
  Device node: icon + hostname + interface handles.

  Vue Flow's wrapper around this component is the node's single Tab stop and
  carries its accessible name (see adapters/vueflow.js), so nothing in here is
  focusable. The comment (spec.general.description) ends that name and is
  shown to sighted users as a tooltip on hover and focus.

  A device from an included topology is read only: it says where it comes
  from in place of its kind, has a dashed border, and offers no handle for a
  new interface. Its accessible name says the same.
-->
<template>
  <div
    class="builder-node builder-node--device"
    :class="{
      'is-selected': selected,
      'builder-node--included': Boolean(data.includedFrom),
    }"
    :data-node-id="id"
    :data-node-kind="'device'"
    data-testid="builder-node"
    @mouseenter="showTooltip"
    @mouseleave="hideTooltip">
    <div class="builder-node__header">
      <builder-icon :name="data.iconKey" :size="16" />
      <span class="builder-node__label">{{ data.label }}</span>
    </div>
    <span
      v-if="data.includedFrom"
      class="builder-node__origin"
      data-testid="node-included-from">
      Included from {{ data.includedFrom }}
    </span>
    <span v-else class="builder-node__kind">Device</span>
    <span class="builder-node__meta">
      {{ interfaceCount }}
      {{ interfaceCount === 1 ? 'interface' : 'interfaces' }}
    </span>
    <span v-if="comment" class="builder-node__comment">{{ comment }}</span>

    <div
      v-if="comment"
      v-show="tooltipOpen"
      class="builder-tooltip builder-tooltip--node"
      data-testid="node-tooltip"
      aria-hidden="true">
      {{ comment }}
    </div>

    <!-- Handles are pointer-only: the outline's Connect form is the keyboard
         path, so they are hidden from assistive technology. -->
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
</template>

<script setup>
  import { computed } from 'vue';
  import { Handle, Position } from '@vue-flow/core';

  import BuilderIcon from '../BuilderIcon.vue';
  import { useNodeTooltip } from './nodeTooltip.js';

  import { NEW_INTERFACE_HANDLE_ID } from '@/builder/adapters/vueflow.js';

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const comment = computed(() => props.data.comment || '');
  const { tooltipOpen, showTooltip, hideTooltip } = useNodeTooltip(() =>
    Boolean(comment.value),
  );
  const interfaceCount = computed(() => props.data.handles.length);

  function handleStyle(index) {
    const count = Math.max(1, props.data.handles.length);
    const step = 100 / (count + 1);

    return { top: `${step * (index + 1)}%` };
  }
</script>
