<template>
  <b-table
    :data="vms"
    custom-row-key="name"
    :paginated="table.isPaginated"
    aria-next-label="Next page"
    aria-previous-label="Previous page"
    aria-page-label="Page"
    aria-current-label="Current page"
    backend-pagination
    :total="table.total"
    :per-page="table.perPage"
    :current-page="table.currentPage"
    @page-change="(page) => $emit('page-change', page)"
    :pagination-simple="table.isPaginationSimple"
    :pagination-size="table.paginationSize"
    backend-sorting
    :default-sort-direction="table.defaultSortDirection"
    default-sort="name"
    scrollable
    @sort="(column, order) => $emit('sort', column, order)">
    <template #empty>
      <section class="section">
        <div class="content has-text-white has-text-centered">
          {{ emptyText }}
        </div>
      </section>
    </template>
    <b-table-column field="multiselect" label="">
      <template v-slot:header>
        <b-tooltip label="Select/Unselect All" type="is-dark">
          <b-checkbox
            :model-value="checkAll"
            @update:model-value="(on) => $emit('update:checkAll', on)">
            <span class="is-sr-only">Select all VMs</span>
          </b-checkbox>
        </b-tooltip>
      </template>
      <template v-slot:default="props">
        <div>
          <b-checkbox
            :disabled="props.row.external"
            :model-value="selected"
            :native-value="props.row.name"
            @update:model-value="(names) => $emit('update:selected', names)">
            <span class="is-sr-only">Select VM {{ props.row.name }}</span>
          </b-checkbox>
        </div>
      </template>
    </b-table-column>
    <b-table-column
      field="name"
      label="Node"
      sortable
      header-class="sort-inline"
      v-slot="props">
      <b-tooltip
        v-if="
          !props.row.external &&
          roleAllowed('vms', 'get', experiment + '/' + props.row.name)
        "
        label="details and settings"
        type="is-dark">
        <button
          type="button"
          class="tag is-medium is-clickable vm-name"
          aria-haspopup="dialog"
          @click="$emit('details', props.row)">
          {{ props.row.name }}
        </button>
      </b-tooltip>
      <span v-else class="tag is-medium vm-name">{{ props.row.name }}</span>
    </b-table-column>
    <b-table-column
      v-for="column in SETTING_COLUMNS"
      :key="column.field"
      :field="column.field"
      :label="column.label"
      :visible="columns[column.toggle] !== false"
      :width="column.width"
      :sortable="column.sortable"
      :header-class="column.sortable ? 'sort-inline' : undefined"
      :centered="column.centered"
      v-slot="props">
      <template v-if="column.field == 'ipv4'">
        <div v-for="(ip, index) in props.row.ipv4" :key="index">
          {{ ip || 'unknown' }}
        </div>
      </template>
      <b-tooltip
        v-else-if="column.field == 'labels'"
        label="View/Edit Labels"
        type="is-dark">
        <button
          type="button"
          class="plain-button is-clickable"
          :aria-label="`Labels of ${props.row.name}: ${tagCount(props.row.tags)}`"
          @click="$emit('edit-labels', props.row)">
          <VmLabelCount :tags="props.row.tags" />
        </button>
      </b-tooltip>
      <VmSettingControl
        v-else
        :field="column.field"
        :vm="props.row"
        :hosts="hosts"
        :disks="disks"
        :editable="editable(props.row)"
        :pending="isPending(props.row, column.field)"
        :pending-value="pendingValue(props.row, column.field)"
        @change="(value) => $emit('change', props.row, column.field, value)" />
    </b-table-column>
    <b-table-column
      field="reset"
      label="Reset"
      :visible="anyDefaults"
      centered
      v-slot="props">
      <b-tooltip
        v-if="hasDefaults(props.row) && editable(props.row)"
        :label="resetTip(props.row)"
        type="is-dark"
        position="is-left"
        multilined>
        <b-button
          class="vm-reset"
          size="is-small"
          type="is-light"
          icon-left="undo-alt"
          :aria-label="`Reset settings of VM ${props.row.name}`"
          :aria-disabled="atDefaults(props.row) ? 'true' : undefined"
          :loading="resetPending.includes(props.row.name)"
          @click="atDefaults(props.row) || $emit('reset', props.row)" />
      </b-tooltip>
    </b-table-column>
  </b-table>
</template>

<script>
  import { roleAllowed } from '@/utils/rbac.js';
  import { tagCount } from '@/utils/tagCount';
  import VmLabelCount from './VmLabelCount.vue';
  import VmSettingControl from './VmSettingControl.vue';
  import { atDefaults, hasDefaults } from './vmSettings.js';

  // the columns after the VM's name, each with its column toggle's key
  const SETTING_COLUMNS = [
    {
      field: 'host',
      label: 'Host',
      toggle: 'host',
      width: 200,
      sortable: true,
    },
    { field: 'ipv4', label: 'IP', toggle: 'ipv4', sortable: true },
    {
      field: 'cpus',
      label: 'CPUs',
      toggle: 'cpus',
      width: 100,
      sortable: true,
      centered: true,
    },
    {
      field: 'ram',
      label: 'Memory',
      toggle: 'ram',
      width: 120,
      sortable: true,
      centered: true,
    },
    { field: 'disk', label: 'Disk', toggle: 'disk' },
    {
      field: 'inject_partition',
      label: 'Partition',
      toggle: 'partition',
      sortable: true,
      centered: true,
    },
    { field: 'labels', label: 'Labels', toggle: 'labels', centered: true },
    { field: 'dnb', label: 'Boot', toggle: 'boot', centered: true },
    {
      field: 'snapshot',
      label: 'Persistence',
      toggle: 'persistence',
      centered: true,
    },
  ];

  // The stopped experiment's VM table: each VM's settings as controls that
  // change them, and its labels. It asks the page, by event, to save a change.
  export default {
    name: 'StoppedVmTable',
    components: { VmLabelCount, VmSettingControl },
    setup() {
      return {
        SETTING_COLUMNS,
        atDefaults,
        hasDefaults,
        roleAllowed,
        tagCount,
      };
    },
    props: {
      vms: { type: Array, default: () => [] },
      experiment: { type: String, default: '' },
      // the page's useTable state: paging and sort
      table: { type: Object, required: true },
      // whether each column shows, by its toggle's key
      columns: { type: Object, default: () => ({}) },
      emptyText: { type: String, default: '' },
      hosts: { type: Array, default: () => [] },
      disks: { type: Array, default: null },
      // the changes waiting on the server: by VM name, each field's value
      pending: { type: Object, default: () => ({}) },
      // the VMs whose settings reset waits on the server
      resetPending: { type: Array, default: () => [] },
      selected: { type: Array, default: () => [] },
      checkAll: { type: Boolean, default: false },
    },
    emits: [
      'page-change',
      'sort',
      'details',
      'edit-labels',
      // (vm, field, value): save a setting
      'change',
      // (vm): reset its settings to the topology's
      'reset',
      'update:selected',
      'update:checkAll',
    ],
    computed: {
      anyDefaults() {
        return (this.vms ?? []).some(hasDefaults);
      },
    },
    methods: {
      editable(vm) {
        return (
          !vm.external &&
          roleAllowed('vms', 'patch', `${this.experiment}/${vm.name}`)
        );
      },
      isPending(vm, field) {
        return field in (this.pending[vm.name] ?? {});
      },
      pendingValue(vm, field) {
        return this.pending[vm.name]?.[field];
      },
      resetTip(vm) {
        return atDefaults(vm)
          ? 'Cannot reset settings: they have the topology values'
          : 'Set every setting back to the topology value';
      },
    },
  };
</script>

<style scoped>
  .vm-name.tag {
    border: 0;
  }
</style>
