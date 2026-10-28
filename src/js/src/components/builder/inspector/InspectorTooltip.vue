<!--
  The tooltip of an Inspector field or list (see fieldTooltip.js). It keeps its
  own state, so the field does not re-render. The spec path is for the eye only.
-->
<template>
  <builder-fixed-tooltip
    v-slot="{ content }"
    :tooltip="tooltip"
    testid="inspector-tooltip">
    <strong
      v-if="content.path"
      class="builder-tooltip__path"
      data-testid="inspector-tooltip-path"
      >{{ content.path }}</strong
    >
    <span data-testid="inspector-tooltip-text">{{ content.text }}</span>
  </builder-fixed-tooltip>
</template>

<script setup>
  import BuilderFixedTooltip from '../BuilderFixedTooltip.vue';
  import { useFieldTooltip } from './fieldTooltip.js';

  const props = defineProps({
    text: { type: String, default: '' },
    // The field's path in the phenix node spec, or '' for none.
    path: { type: String, default: '' },
  });

  const tooltip = useFieldTooltip(() =>
    props.text ? { path: props.path, text: props.text } : null,
  );
  const { show, scheduleHide, hide, onFocusIn } = tooltip;

  defineExpose({ show, scheduleHide, hide, onFocusIn });
</script>
