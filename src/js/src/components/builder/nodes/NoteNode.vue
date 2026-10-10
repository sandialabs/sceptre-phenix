<!--
  Note node: annotation only, never exported to the topology. Vue Flow's
  wrapper is the focusable, named element (see DeviceNode.vue). A note's
  color is an accent bar across its top. A selected note has handles to
  resize it with the mouse (see NodeResize.vue).
-->
<template>
  <div
    class="builder-node builder-node--note"
    :class="{ 'is-selected': selected, 'has-resize-frame': framed }"
    :data-node-id="id"
    data-node-kind="note"
    data-testid="builder-node">
    <span
      v-if="accent"
      class="builder-node__accent"
      :style="{ '--bx-node-accent': accent }"
      aria-hidden="true"></span>
    <div class="builder-node__header">
      <builder-icon :name="data.iconKey" :size="16" />
      <span class="builder-node__label">{{ title }}</span>
    </div>
    <p class="builder-node__text">{{ text }}</p>
    <node-issue-mark v-if="data.issue" :node-id="id" :issue="data.issue" />
    <node-resize :id="id" :node="data.node" :selected="selected" />
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import NodeIssueMark from './NodeIssueMark.vue';
  import { useResizeFrame } from './canvasEditing.js';
  import NodeResize from './NodeResize.vue';

  import { drawnColor } from '@/builder/colors.js';

  // Vue Flow also passes its node state as attributes. None of them belong
  // on the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const text = computed(
    () =>
      props.data.node.note?.text ||
      'Select the note and use the inspector to add text.',
  );
  // A note is named after its first line (model.nodeLabel), which the text
  // below already shows. The header thus names the kind, or the label the
  // note was given.
  const title = computed(() => {
    const label = props.data.node.label;

    return label && label !== 'Note' ? label : 'Note';
  });
  const accent = computed(() => drawnColor(props.data.node.note?.color));
  const framed = useResizeFrame(() => props.selected);
</script>
