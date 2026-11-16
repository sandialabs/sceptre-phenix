<template>
  <VmPickFilter
    kind="host"
    title="Hosts"
    icon="server"
    note="VMs running on any of the checked hosts"
    hint="show only the VMs running on any of these hosts"
    empty-reason="no VM is running on a host"
    :options="options"
    :picked="picked"
    :fits="fits"
    @update:picked="(hosts) => $emit('update:picked', hosts)" />
</template>

<script>
  import VmPickFilter from './VmPickFilter.vue';

  // Narrows the VM table to the VMs running on any of the cluster hosts
  // checked, from the hosts running the experiment's VMs.
  export default {
    name: 'VmHostFilter',
    components: { VmPickFilter },
    props: {
      // the host names to offer, and those checked
      options: { type: Array, default: () => [] },
      picked: { type: Array, default: () => [] },
      // whether the server can take a filter by these hosts
      fits: { type: Function, default: () => true },
    },
    emits: ['update:picked'],
  };
</script>
