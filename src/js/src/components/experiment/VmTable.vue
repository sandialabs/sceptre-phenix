<template>
  <b-table
    :data="vms"
    :paginated="table.isPaginated"
    aria-next-label="Next page"
    aria-previous-label="Previous page"
    aria-page-label="Page"
    aria-current-label="Current page"
    backend-pagination
    :total="table.total"
    :per-page="table.perPage"
    @page-change="(page) => $emit('page-change', page)"
    :pagination-simple="table.isPaginationSimple"
    :pagination-size="table.paginationSize"
    backend-sorting
    default-sort-direction="asc"
    default-sort="name"
    @sort="(column, order) => $emit('sort', column, order)"
    ref="table">
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
        <template v-if="!props.row.busy">
          <div>
            <b-checkbox
              :disabled="props.row.external"
              :model-value="selected"
              @update:model-value="(names) => $emit('update:selected', names)"
              :native-value="props.row.name">
              <span class="is-sr-only"> Select VM {{ props.row.name }} </span>
            </b-checkbox>
          </div>
        </template>
        <template v-else> BUSY </template>
      </template>
    </b-table-column>
    <b-table-column
      field="name"
      label="Node"
      width="150"
      sortable
      header-class="sort-inline"
      centered
      v-slot="props">
      <template
        v-if="
          !props.row.external &&
          roleAllowed('vms', 'get', experiment + '/' + props.row.name)
        ">
        <b-tooltip label="details and actions" type="is-dark">
          <span
            class="tag is-medium is-clickable"
            :class="decorator(props.row.state, props.row.busy)">
            <div class="field">
              <div @click="$emit('info', props.row)">
                {{ props.row.name }}
              </div>
            </div>
          </span>
        </b-tooltip>
      </template>
      <template v-else-if="props.row.external">
        <span class="tag is-medium">
          <div class="field">
            {{ props.row.name }}
          </div>
        </span>
      </template>
      <template v-else>
        <b-tooltip label="get info for the VM" type="is-dark">
          <span
            class="tag is-medium"
            :class="decorator(props.row.state, props.row.busy)">
            <div class="field">
              <div @click="$emit('details', props.row)">
                {{ props.row.name }}
              </div>
            </div>
          </span>
        </b-tooltip>
      </template>
      <section v-if="props.row.busy">
        <b-progress
          size="is-small"
          type="is-warning"
          show-value
          :value="props.row.percent"
          format="percent"></b-progress>
      </section>
    </b-table-column>
    <b-table-column
      v-if="columns.screenshot"
      field="screenshot"
      label="Screenshot"
      centered
      v-slot="props">
      <template v-if="props.row.external">
        <img
          :alt="screenshotAlt(props.row, screenshotsDue)"
          :src="vmScreenshot(props.row, screenshotsDue)"
          width="200"
          height="150" />
      </template>
      <template
        v-else-if="
          (props.row.running && !props.row.busy) ||
          (props.row.delayed_start && props.row.state == 'BUILDING')
        ">
        <a :href="vncLoc(props.row)" target="_blank">
          <img
            :alt="consoleLinkAlt(props.row, screenshotsDue)"
            :src="vmScreenshot(props.row, screenshotsDue)"
            :width="props.row.screenshot ? undefined : 200"
            :height="props.row.screenshot ? undefined : 150" />
        </a>
      </template>
      <template v-else-if="props.row.busy">
        <b-tooltip
          label="Screenshot not available while busy with action"
          type="is-dark">
          <img
            :alt="screenshotAlt(props.row, screenshotsDue)"
            :src="vmScreenshot(props.row, screenshotsDue)"
            width="200"
            height="150" />
        </b-tooltip>
      </template>
      <template v-else>
        <img
          :alt="screenshotAlt(props.row, screenshotsDue)"
          :src="vmScreenshot(props.row, screenshotsDue)"
          width="200"
          height="150" />
      </template>
    </b-table-column>
    <b-table-column
      v-if="isDelayed"
      field="delayed"
      label="Delay"
      sortable
      header-class="sort-inline"
      centered
      v-slot="props">
      <b-tag
        type="is-info"
        v-if="props.row.delayed_start && props.row.state == 'BUILDING'"
        >{{ props.row.delayed_start }}</b-tag
      >
    </b-table-column>
    <b-table-column
      v-if="columns.host"
      field="host"
      label="Host"
      width="150"
      sortable
      header-class="sort-inline"
      v-slot="props">
      <template v-if="props.row.external"> EXTERNAL </template>
      <template v-else>
        {{ props.row.host }}
      </template>
    </b-table-column>
    <b-table-column v-if="columns.ipv4" field="ipv4" label="IP" width="150">
      <template v-slot:default="props">
        <template
          v-if="
            roleAllowed(
              'vms/captures',
              'create',
              experiment + '/' + props.row.name,
            ) &&
            props.row.running &&
            !props.row.busy
          ">
          <b-tooltip :label="updateCaptureLabel(props.row)" type="is-dark">
            <div class="field">
              <div
                v-for="(ip, index) in props.row.ipv4"
                :class="tapDecorator(props.row.captures, index)"
                :key="index"
                :aria-busy="isCapturePending(props.row.name, index)"
                @click="$emit('capture', props.row, index)">
                {{ ip }}
                <span
                  v-if="isCapturePending(props.row.name, index)"
                  class="capture-spinner"
                  title="waiting for the server"></span>
              </div>
            </div>
          </b-tooltip>
        </template>
        <template v-else>
          {{ formatStringify(props.row.ipv4) }}
        </template>
      </template>
    </b-table-column>
    <!-- the server sorts Network by VLAN ID and Taps by name, ties by VM name -->
    <b-table-column
      v-if="columns.network"
      field="network"
      label="Network"
      sortable
      header-class="sort-inline"
      v-slot="props">
      <template
        v-if="
          roleAllowed('vms', 'patch', experiment + '/' + props.row.name) &&
          props.row.running &&
          !props.row.busy
        ">
        <b-tooltip label="change VLANs" type="is-dark">
          <div class="field">
            <div
              v-for="(n, index) in props.row.networks"
              :key="index"
              @click="$emit('change-vlan', props.row, index)">
              {{ formatLowercase(n) }}
              <span
                v-if="
                  (actionPending[props.row.name] ?? []).includes(
                    vlanPendingKey(index),
                  )
                "
                class="capture-spinner"
                title="waiting for the server"></span>
            </div>
          </div>
        </b-tooltip>
      </template>
      <template v-else>
        {{ formatStringify(props.row.networks) }}
      </template>
    </b-table-column>
    <b-table-column
      v-if="columns.taps"
      field="taps"
      label="Taps"
      sortable
      header-class="sort-inline"
      v-slot="props">
      <template
        v-if="
          roleAllowed(
            'vms/captures',
            'create',
            experiment + '/' + props.row.name,
          ) &&
          props.row.running &&
          !props.row.busy
        ">
        <b-tooltip :label="updateCaptureLabel(props.row)" type="is-dark">
          <div class="field">
            <div
              v-for="(t, index) in props.row.taps"
              :class="tapDecorator(props.row.captures, index)"
              :key="index"
              :aria-busy="isCapturePending(props.row.name, index)"
              @click="$emit('capture', props.row, index)">
              {{ formatLowercase(t) }}
              <span
                v-if="isCapturePending(props.row.name, index)"
                class="capture-spinner"
                title="waiting for the server"></span>
            </div>
          </div>
        </b-tooltip>
      </template>
      <template v-else>
        {{ formatStringify(props.row.taps) }}
      </template>
    </b-table-column>
    <b-table-column
      v-if="columns.uptime"
      field="uptime"
      label="Uptime"
      sortable
      header-class="sort-inline"
      cell-class="nowrap-cell"
      v-slot="props">
      <template v-if="props.row.external"> unknown </template>
      <template v-else-if="!props.row.running">
        <span class="vm-uptime-none" aria-hidden="true">—</span>
        <span class="is-sr-only">not running</span>
      </template>
      <template v-else>
        {{ formatUptime(props.row.uptime) }}
      </template>
    </b-table-column>
    <!-- sorted by how many labels and notes, ties by name A to Z -->
    <b-table-column
      v-if="columns.labels"
      field="labels"
      label="Labels"
      sortable
      header-class="sort-inline"
      centered
      v-slot="props">
      <b-tooltip label="View/Edit Labels" type="is-dark">
        <div @click="$emit('edit-labels', props.row)" class="is-clickable">
          <VmLabelCount :tags="props.row.tags" />
        </div>
      </b-tooltip>
    </b-table-column>
    <b-table-column
      v-if="columns.actions && rowActionsAllowed"
      field="actions"
      label="Actions"
      width="120"
      centered
      v-slot="props">
      <VmRowActions
        :vm="props.row"
        :full-name="experiment + '/' + props.row.name"
        :pending="[
          ...(captureAllPending[props.row.name]
            ? ['captureAll', 'stopCaptures']
            : []),
          ...(actionPending[props.row.name] ?? []),
        ]"
        @action="(name) => $emit('action', name, props.row)" />
    </b-table-column>
  </b-table>
</template>

<script>
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import { isInterfaceCapturing } from '@/utils/captures.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import VmLabelCount from './VmLabelCount.vue';
  import VmRowActions from './VmRowActions.vue';
  import {
    ROW_ACTIONS,
    ROW_SWAPS,
    actionPermitted,
    vlanPendingKey,
    vmStateType,
  } from './vmActions.js';
  import {
    consoleLinkAlt,
    screenshotAlt,
    vmScreenshot,
    vncHref,
  } from './vmScreens.js';

  // The running experiment's VM table: a row per VM with its screenshot,
  // interfaces, labels and Actions column. It asks the page, by event, to
  // page, sort, select, or act on a VM.
  export default {
    name: 'VmTable',
    components: { VmLabelCount, VmRowActions },
    mixins: [formattingMixin],
    setup() {
      return {
        roleAllowed,
        consoleLinkAlt,
        screenshotAlt,
        vmScreenshot,
        vlanPendingKey,
      };
    },
    props: {
      vms: { type: Array, default: () => [] },
      // the experiment's name, which RBAC checks VM permissions under
      experiment: { type: String, default: '' },
      // the useTable() state the server pages and sorts the rows by
      table: { type: Object, required: true },
      // which optional columns show, by key
      columns: { type: Object, required: true },
      // what the table says when it has no rows
      emptyText: { type: String, default: '' },
      // whether VMs still without screenshots have waited long enough
      screenshotsDue: { type: Boolean, default: false },
      // "vm/interface" keys of the capture buttons waiting on the server
      capturePending: { type: Object, default: () => ({}) },
      // names of the VMs starting or stopping captures on all interfaces
      captureAllPending: { type: Object, default: () => ({}) },
      // the actions running in the background for each VM, by its name
      actionPending: { type: Object, default: () => ({}) },
      // the names of the VMs selected, and whether all of them are
      selected: { type: Array, default: () => [] },
      checkAll: { type: Boolean, default: false },
    },
    emits: [
      // (vm, interface): the IP or tap of an interface was clicked
      'capture',
      // (vm, interface): a network was clicked
      'change-vlan',
      // (vm): the name of a VM the role may not get was clicked
      'details',
      'edit-labels',
      // (vm): the name of a VM was clicked, for its details and actions
      'info',
      // (action, vm): a button of the Actions column was clicked
      'action',
      'page-change',
      'sort',
      'update:checkAll',
      'update:selected',
    ],
    expose: ['visibleData'],

    computed: {
      // whether a VM has a delayed start still to come
      isDelayed() {
        return this.vms.some(
          (vm) => vm.delayed_start && vm.state == 'BUILDING',
        );
      },

      // whether the role may take any of the Actions column's actions on
      // some VM; the column is hidden otherwise
      rowActionsAllowed() {
        return [...ROW_ACTIONS, ...Object.values(ROW_SWAPS)].some((name) =>
          actionPermitted(name, roleAllowed),
        );
      },
    },

    methods: {
      // the rows the table shows now, on its page and in its order
      visibleData() {
        return this.$refs.table.visibleData;
      },

      vncLoc(vm) {
        return vncHref(this.$router, this.$route.params.id, vm);
      },

      decorator(state, busy) {
        return vmStateType({ state, busy });
      },

      tapDecorator(captures, iface) {
        return isInterfaceCapturing(captures, iface) ? 'is-success' : undefined;
      },

      isCapturePending(vmName, iface) {
        return !!this.capturePending[`${vmName}/${iface}`];
      },

      updateCaptureLabel(vm) {
        return vm.captures.length == 0
          ? 'start packet capture'
          : 'stop packet capture';
      },
    },
  };
</script>

<style scoped>
  /* 4.9:1 on the table's $background-bis (#484848) */
  .vm-uptime-none {
    color: #bdbdbd;
  }

  /* a small spinner beside an IP, tap or network whose request is waiting */
  .capture-spinner {
    display: inline-block;
    width: 0.85em;
    height: 0.85em;
    margin-left: 0.35em;
    vertical-align: -0.1em;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-top-color: transparent;
    border-radius: 50%;
    animation: capture-spin 0.6s linear infinite;
  }

  @keyframes capture-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
