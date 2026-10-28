<!--
  Group node: a labelled container for other nodes. Vue Flow's wrapper is the
  focusable, named element (see DeviceNode.vue).
-->
<template>
  <div
    class="builder-node builder-node--group"
    :class="[
      {
        'is-selected': selected,
        'builder-node--title-only': !data.comment,
      },
      iconClass,
    ]"
    :data-border="border"
    :data-node-id="id"
    data-node-kind="group"
    :data-icon-size="iconSize"
    data-testid="builder-node">
    <span
      v-if="accent"
      class="builder-node__accent"
      :style="{ '--bx-node-accent': accent }"
      aria-hidden="true"></span>
    <div class="builder-node__header">
      <builder-icon
        v-if="showIcon"
        :name="data.iconKey"
        :src="data.iconSrc"
        :size="iconPixels(iconSize)" />
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
  import { useNodeIcon } from './nodeIconSize.js';

  import { drawnColor } from '@/builder/colors.js';
  import {
    BORDER_STYLES,
    DEFAULT_ICON_SIZE,
    iconPixels,
  } from '@/builder/model.js';

  // Vue Flow also passes its node state as attributes. None of them belong
  // on the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const accent = computed(() => drawnColor(props.data.node.group?.color));
  const iconSize = computed(() => props.data.iconSize || DEFAULT_ICON_SIZE);
  const { showIcon, iconClass } = useNodeIcon(() => iconSize.value);
  // A pattern the editor does not know, which only an edited file can
  // hold, is drawn as none: dashed.
  const border = computed(() => {
    const style = props.data.node.group?.borderStyle;

    return BORDER_STYLES.includes(style) ? style : undefined;
  });
</script>
