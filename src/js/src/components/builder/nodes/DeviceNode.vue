<!--
  Device node: icon, hostname and interface handles. Vue Flow's wrapper takes
  focus and carries the name (see adapters/vueflow.js), so nothing here is
  focusable.
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
        v-if="showIcon"
        :name="data.iconKey"
        :src="data.iconSrc"
        :size="iconPixels(iconSize)" />
      <span class="builder-node__label">{{ data.label }}</span>
    </div>
    <span v-if="data.includedFrom" class="builder-node__origin">
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

    <!-- Handles are for the pointer only. The keyboard path is the
         Inspector's Add connection dialog, which the command palette and
         its keys also open. For
         this reason, the handles are hidden from assistive technology. -->
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
      v-if="!data.includedFrom && !data.readOnly"
      :id="NEW_INTERFACE_HANDLE_ID"
      type="source"
      :position="Position.Bottom"
      class="builder-handle builder-handle--new"
      :title="`Drag to connect ${data.label} on a new interface`"
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

  import { NEW_INTERFACE_HANDLE_ID } from '@/builder/adapters/vueflow.js';
  import { DEFAULT_ICON_SIZE, iconPixels } from '@/builder/model.js';
  import { deviceInfo } from '@/builder/nodeInfo.js';

  // Vue Flow also passes its node state as attributes. None of them belong
  // on the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const comment = computed(() => props.data.comment || '');
  const { colorClasses, colorStyle } = useNodeColors(
    () => props.data.node.device,
    () => ({ layerColor: props.data.layerColor }),
  );
  const { infoId, infoText } = useNodeInfo(() => deviceInfo(props.data.node));
  const interfaceCount = computed(() => props.data.handles.length);
  const iconSize = computed(() => props.data.iconSize || DEFAULT_ICON_SIZE);
  const { showIcon, iconClass } = useNodeIcon(() => iconSize.value);

  function handleStyle(index) {
    const count = Math.max(1, props.data.handles.length);
    const step = 100 / (count + 1);

    return { top: `${step * (index + 1)}%` };
  }
</script>
