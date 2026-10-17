<!--
  The disclosure button in the header of a section, list or list entry of
  the Inspector, which collapses and expands its fields (see collapse.js).
  It goes in the group's legend, so the group keeps its name. It says
  whether the group is expanded (aria-expanded), with a chevron that turns
  as decoration.

  A collapsed group that holds a field with an error has a cross in the
  danger color in its header, and its name says so to screen readers.
  The Node Editor's table of contents goes to the button
  (data-outline-focus). Its class nokey keeps Vue Flow's pan key handler
  from taking Space, which presses the button. It is the component's one
  root element, so a listener set on the component (focus, blur) is the
  button's.
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
