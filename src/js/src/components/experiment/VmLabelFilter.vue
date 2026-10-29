<template>
  <VmPickFilter
    kind="label"
    title="Labels"
    icon="tag"
    note="VMs with any of the checked labels"
    hint="show only the VMs with any of these labels"
    empty-reason="no VM has a label"
    :options="options"
    :picked="picked"
    :id-of="labelId"
    :text-of="labelText"
    :fits="fits"
    @update:picked="(labels) => $emit('update:picked', labels)" />
</template>

<script>
  import VmPickFilter from './VmPickFilter.vue';
  import { labelId, labelText } from './experimentHeader.js';

  // Narrows the VM table to the VMs carrying any of the labels checked, from
  // the labels on the experiment's VMs.
  export default {
    name: 'VmLabelFilter',
    components: { VmPickFilter },
    setup() {
      return { labelId, labelText };
    },
    props: {
      // the labels to offer, and those checked, as { key, value }
      options: { type: Array, default: () => [] },
      picked: { type: Array, default: () => [] },
      // whether the server can take a filter by these labels
      fits: { type: Function, default: () => true },
    },
    emits: ['update:picked'],
  };
</script>
