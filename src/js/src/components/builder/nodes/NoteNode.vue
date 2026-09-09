<!-- Note node: annotation only, never exported to the topology. -->
<template>
  <div
    class="builder-node builder-node--note"
    :class="{ 'is-selected': selected }"
    :data-node-id="id"
    data-node-kind="note"
    data-testid="builder-node"
    tabindex="0"
    role="note"
    :aria-label="`Note ${data.label}`">
    <div class="builder-node__header">
      <builder-icon :name="data.iconKey" :size="16" />
      <span class="builder-node__label">{{ data.label }}</span>
    </div>
    <p class="builder-node__meta">{{ text }}</p>
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';

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
</script>
