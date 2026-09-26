<!--
  Group node: a labelled container that other nodes can be parented to. Vue
  Flow's wrapper is the focusable, named element (see DeviceNode.vue). A
  group's color is an accent bar across its top.
-->
<template>
  <div
    class="builder-node builder-node--group"
    :class="{ 'is-selected': selected }"
    :data-node-id="id"
    data-node-kind="group"
    data-testid="builder-node">
    <span
      v-if="accent"
      class="builder-node__accent"
      :style="{ '--bx-node-accent': accent }"
      aria-hidden="true"></span>
    <div class="builder-node__header">
      <builder-icon :name="data.iconKey" :size="16" />
      <span class="builder-node__label">{{ data.label }}</span>
    </div>
    <span v-if="data.comment" class="builder-node__comment">
      {{ data.comment }}
    </span>
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';

  import { drawnColor } from '@/builder/colors.js';

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const accent = computed(() => drawnColor(props.data.node.group?.color));
</script>
