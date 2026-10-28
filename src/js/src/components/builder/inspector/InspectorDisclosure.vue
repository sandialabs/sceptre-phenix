<!--
  The disclosure button of an Inspector group (see collapse.js). Class nokey
  keeps Vue Flow's pan keys from taking Space. It is the one root, so component
  listeners go to it.
-->
<template>
  <button
    type="button"
    class="inspector-disclosure nokey"
    :aria-expanded="collapsed ? 'false' : 'true'"
    data-outline-focus
    data-testid="inspector-disclosure"
    @click="$emit('toggle')">
    <builder-icon
      class="inspector-disclosure__chevron"
      name="chevron-down"
      :size="14" />
    <span class="inspector-disclosure__label"><slot /></span>
    <span
      v-if="collapsed && errors"
      class="inspector-disclosure__error"
      data-testid="inspector-disclosure-error">
      <builder-icon name="close" :size="12" />
      <span class="builder-visually-hidden">, has errors</span>
    </span>
  </button>
</template>

<script setup>
  import BuilderIcon from '../BuilderIcon.vue';

  defineProps({
    collapsed: { type: Boolean, default: false },
    // A field with an error is inside the group.
    errors: { type: Boolean, default: false },
  });

  defineEmits(['toggle']);
</script>
