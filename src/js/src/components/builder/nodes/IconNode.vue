<!--
  Icon node: a built-in or custom icon on the canvas, never published. Without a
  resolved custom icon, it shows the kind's built-in icon.
-->
<template>
  <div
    class="builder-node builder-node--icon"
    :class="{ 'is-selected': selected }"
    :data-node-id="id"
    data-node-kind="icon"
    data-testid="builder-node">
    <span class="builder-node__figure">
      <builder-icon :name="data.iconKey" :src="data.iconSrc" size="100%" />
    </span>
    <span v-if="label" class="builder-node__label">{{ label }}</span>
    <node-issue-mark v-if="data.issue" :node-id="id" :issue="data.issue" />
    <node-resize :id="id" :node="data.node" :selected="selected" />
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import NodeIssueMark from './NodeIssueMark.vue';
  import NodeResize from './NodeResize.vue';

  // Vue Flow also passes its node state as attributes. None of them belong
  // on the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const label = computed(() => props.data.node.icon?.label || '');
</script>
