<!--
  Shape node: a rectangle or a circle that is never published. Vue Flow's
  wrapper is the focusable, named element, whose name says the figure.
-->
<template>
  <div
    class="builder-node builder-node--shape"
    :class="[{ 'is-selected': selected }, colorClasses]"
    :style="colorStyle"
    :data-shape="figure"
    :data-border="border"
    :data-node-id="id"
    data-node-kind="shape"
    data-testid="builder-node">
    <span v-if="label" class="builder-node__label">{{ label }}</span>
    <node-issue-mark v-if="data.issue" :node-id="id" :issue="data.issue" />
    <node-resize
      :id="id"
      :node="data.node"
      :selected="selected"
      :keep-aspect-ratio="figure === 'circle'" />
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import NodeIssueMark from './NodeIssueMark.vue';
  import NodeResize from './NodeResize.vue';
  import { useNodeColors } from './nodeColors.js';

  import { BORDER_STYLES } from '@/builder/model.js';

  // Vue Flow also passes its node state as attributes. None of them belong
  // on the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const shape = computed(() => props.data.node.shape || {});
  const figure = computed(() =>
    shape.value.shape === 'circle' ? 'circle' : 'rectangle',
  );
  const label = computed(() => shape.value.label || '');
  // A pattern the editor does not know, which only an edited file can
  // hold, is drawn as none: solid.
  const border = computed(() =>
    BORDER_STYLES.includes(shape.value.borderStyle)
      ? shape.value.borderStyle
      : undefined,
  );
  const { colorClasses, colorStyle } = useNodeColors(() => shape.value);
</script>
