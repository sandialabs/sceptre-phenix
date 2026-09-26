<!--
  Note node: annotation only, never exported to the topology. Vue Flow's
  wrapper is the focusable, named element (see DeviceNode.vue). A note's
  color is an accent bar across its top.
-->
<template>
  <div
    class="builder-node builder-node--note"
    :class="{ 'is-selected': selected }"
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

  const text = computed(
    () =>
      props.data.node.note?.text ||
      'Select the note and use the inspector to add text.',
  );
  // A note is named after its first line (model.nodeLabel), which the text
  // below shows already: the header names the kind, or the label it was
  // given.
  const title = computed(() => {
    const label = props.data.node.label;

    return label && label !== 'Note' ? label : 'Note';
  });
  const accent = computed(() => drawnColor(props.data.node.note?.color));
</script>
