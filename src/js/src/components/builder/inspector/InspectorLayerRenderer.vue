<!--
  The layer as one field of the form, where a UI schema puts it (see
  LAYER_UI_TYPE in adapters/forms.js): after the node's look fields, as
  the template editor and the Node Editor show it. The Inspector that
  renders the form provides the node's layer and what a choice does
  (INSPECTOR_LAYER). A form rendered anywhere else shows nothing here.
-->
<template>
  <inspector-layer
    v-if="layer?.state.value.kind"
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
