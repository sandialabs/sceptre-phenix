<!--
  The layer as one form field (LAYER_UI_TYPE in adapters/forms.js). Without
  INSPECTOR_LAYER from the Inspector, it shows nothing.
-->
<template>
  <inspector-layer
    v-if="layer?.state.value.ids"
    as="field"
    v-bind="layer.state.value"
    @choose="layer.choose"
    @use-matched="layer.useMatched"
    @edit-layers="layer.editLayers" />
</template>

<script setup>
  import { rendererProps } from '@jsonforms/vue';

  import InspectorLayer from './InspectorLayer.vue';
  import { useInspectorLayer } from './control.js';

  defineProps(rendererProps());

  const layer = useInspectorLayer();
</script>
