<!-- Group node: a labelled container that other nodes can be parented to. -->
<template>
  <div
    class="builder-node builder-node--group"
    :class="{ 'is-selected': selected }"
    :data-node-id="id"
    data-node-kind="group"
    data-testid="builder-node"
    tabindex="0"
    role="group"
    :aria-label="`Group ${data.label}`">
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
  import BuilderIcon from '../BuilderIcon.vue';

  defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });
</script>
