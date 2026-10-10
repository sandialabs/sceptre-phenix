<!--
  Group node: a labelled container that other nodes can be parented to. Vue
  Flow's wrapper is the focusable, named element (see DeviceNode.vue). A
  group's color is an accent bar across its top, its description a line
  under its title, and its border the pattern chosen for it (dashed without
  one). A custom icon, when the group has one, is drawn in place of the icon
  of its key. A selected group has handles to resize it with the mouse,
  never smaller than its members need (see NodeResize.vue).
-->
<template>
  <div
    class="builder-node builder-node--group"
    :class="{ 'is-selected': selected }"
    :data-border="border"
    :data-node-id="id"
    data-node-kind="group"
    data-testid="builder-node">
    <span
      v-if="accent"
      class="builder-node__accent"
      :style="{ '--bx-node-accent': accent }"
      aria-hidden="true"></span>
    <div class="builder-node__header">
      <builder-icon :name="data.iconKey" :src="data.iconSrc" :size="16" />
      <span class="builder-node__label">{{ data.label }}</span>
    </div>
    <span v-if="data.comment" class="builder-node__comment">
      {{ data.comment }}
    </span>
    <node-issue-mark v-if="data.issue" :node-id="id" :issue="data.issue" />
    <node-resize :id="id" :node="data.node" :selected="selected" />
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import NodeIssueMark from './NodeIssueMark.vue';
  import NodeResize from './NodeResize.vue';

  import { drawnColor } from '@/builder/colors.js';
  import { BORDER_STYLES } from '@/builder/model.js';

  // Vue Flow passes its node state as attributes as well; none belong on
  // the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const accent = computed(() => drawnColor(props.data.node.group?.color));
  // A pattern the editor does not know, which only an edited file can
  // hold, is drawn as none: dashed.
  const border = computed(() => {
    const style = props.data.node.group?.borderStyle;

    return BORDER_STYLES.includes(style) ? style : undefined;
  });
</script>
