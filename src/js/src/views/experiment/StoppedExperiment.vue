<template>
  <div class="content">
    <b-modal v-model="expModal.active" has-modal-card>
      <div class="modal-card">
        <header class="modal-card-head">
          <p class="modal-card-title">
            {{ expModal.vm.name ? expModal.vm.name : 'unknown' }}
          </p>
        </header>
        <section class="modal-card-body">
          <p>Host: {{ expModal.vm.host }}</p>
          <p>Description: {{ expModal.vm.description || 'unknown' }}</p>
          <p>IP: {{ formatStringify(expModal.vm.ipv4) }}</p>
          <p>
            {{ pluralWord(expModal.vm.cpus, 'CPU') }}: {{ expModal.vm.cpus }}
          </p>
          <p>Memory: {{ formatRAM(expModal.vm.ram) }}</p>
          <p>Disk: {{ expModal.vm.disk }}</p>
          <p>Uptime: {{ formatUptime(expModal.vm.uptime) }}</p>
          <p>
            {{ pluralWord(expModal.vm.networks?.length, 'Network') }}:
            {{ formatStringify(expModal.vm.networks) }}
          </p>
          <p>Taps: {{ formatStringify(expModal.vm.taps) }}</p>
        </section>
      </div>
    </b-modal>
    <b-modal v-model="vlanModal.active" has-modal-card>
      <div class="modal-card" style="width: 25em; height: 30em">
        <header class="modal-card-head">
          <p class="modal-card-title">
            VLAN Assignments for <br />
            {{ $route.params.id }} Experiment
          </p>
        </header>
        <section class="modal-card-body">
          <div v-for="(vlan, index) in vlanModal.vlans" :key="index">
            <table>
              <tbody>
                <tr>
                  <td style="width: 50%">{{ vlan.alias }}</td>
                  <td>
                    <b-numberinput
                      :aria-label="`VLAN ID for alias ${vlan.alias}`"
                      :aria-minus-label="`Decrease VLAN ID for alias ${vlan.alias}`"
                      :aria-plus-label="`Increase VLAN ID for alias ${vlan.alias}`"
                      min="0"
                      max="4094"
                      type="is-light"
                      size="is-small"
                      controls-alignment="right"
                      controls-position="compact"
                      v-model="vlan.vlan" />
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button is-light" @click="updateVLANs">
            Update VLAN(s)
          </button>
        </footer>
      </div>
    </b-modal>
    <div class="level is-vcentered">
      <div class="level-left is-block">
        <span style="font-weight: bold; font-size: x-large"
          >Experiment: {{ $route.params.id }}</span
        ><br />
        <span v-if="experiment.scenario" style="font-weight: bold"
          >Scenario: {{ experiment.scenario }}</span
        >
      </div>
      <div
        class="level-right"
        v-if="experiment.scenario"
        style="max-width: 50%">
        <span style="font-weight: bold">Apps:</span>&nbsp;
        <div style="display: flex; flex-wrap: wrap; gap: 4px">
          <b-tag v-for="(a, index) in experiment.apps" :key="index">
            {{ a }}
          </b-tag>
        </div>
      </div>
    </div>
    <b-field
      v-if="roleAllowed('experiments', 'get', experiment.name)"
      position="is-right"
      grouped>
      <template v-if="selectedRows.length != 0">
        <b-field>
          <b-tooltip label="Set to Boot" type="is-light">
            <b-button
              v-if="
                selectedRows.every((vm) =>
                  roleAllowed('vms', 'patch', experiment.name + '/' + vm),
                )
              "
              aria-label="Set selected VMs to boot"
              class="button is-light boot"
              icon-right="bolt"
              @click="setBoot(false)" />
          </b-tooltip>
        </b-field>
        <b-field>
          <b-tooltip label="Set to Do Not Boot" type="is-light">
            <b-button
              v-if="
                selectedRows.every((vm) =>
                  roleAllowed('vms', 'patch', experiment.name + '/' + vm),
                )
              "
              aria-label="Set selected VMs to not boot"
              class="button is-light dnb"
              icon-right="bolt"
              @click="setBoot(true)" />
          </b-tooltip>
        </b-field>
        <hr style="width: 1px; height: 100%; margin: 0" />
      </template>
      <b-field v-if="roleAllowed('experiments', 'patch', experiment.name)">
        <b-tooltip
          label="assign VLAN ID to alias"
          type="is-light"
          position="is-top">
          <b-button
            aria-label="Assign VLAN IDs to aliases"
            class="button is-light"
            icon-right="network-wired"
            @click="vlanModal.active = true"></b-button>
        </b-tooltip>
      </b-field>
      <b-field v-if="activeTab == 1">
        <b-tooltip label="search on a specific category" type="is-light">
          <b-select
            aria-label="Filter files by category"
            v-model="fileCategory"
            @update:modelValue="(value) => assignCategory(value)"
            placeholder="All Categories">
            <option
              v-for="(category, index) in fileCategories"
              :key="index"
              :value="category">
              {{ category }}
            </option>
          </b-select>
        </b-tooltip>
      </b-field>
      <b-field>
        <b-autocomplete
          v-model="searchName"
          :placeholder="searchPlaceholder"
          icon="search"
          :data="searchHistory"
          @typing="searchVMs"
          @select="(option) => searchVMs(option)">
          <template #empty>No results found</template>
        </b-autocomplete>
        <p v-if="searchName || fileCategory" class="control">
          <button
            aria-label="Clear VM search"
            class="button input-button"
            @click="
              searchVMs('');
              fileCategory = null;
            ">
            <b-icon icon="window-close"></b-icon>
          </button>
        </p>
      </b-field>
      <b-field>
        <b-tooltip label="Start experiment" type="is-light" position="is-top">
          <b-button
            aria-label="Start experiment"
            v-if="roleAllowed('experiments/start', 'update', experiment.name)"
            class="button is-success"
            icon-right="play"
            @click="start"></b-button>
        </b-tooltip>
      </b-field>
      <b-field
        v-if="roleAllowed('experiments/schedule', 'create', experiment.name)">
        <b-tooltip
          label="menu for scheduling hosts to the experiment"
          type="is-light"
          position="is-top"
          multilined>
          <b-dropdown v-model="algorithm" class="is-right" aria-role="list">
            <template #trigger>
              <b-button
                aria-label="Update schedule"
                icon-right="bars"
                class="button is-light"></b-button>
            </template>
            <b-dropdown-item
              v-for="(s, index) in schedules"
              :key="index"
              :value="s"
              @click="updateSchedule">
              {{ s }}
            </b-dropdown-item>
          </b-dropdown>
        </b-tooltip>
      </b-field>
      <router-link
        :aria-label="`View state of health for experiment ${$route.params.id}`"
        class="button is-light"
        :to="{ name: 'soh', params: { id: $route.params.id } }">
        <b-icon icon="heartbeat"></b-icon>
      </router-link>
    </b-field>
    <div style="margin-top: -4em">
      <b-tabs v-model="activeTab">
        <b-tab-item label="VMs" icon="desktop">
          <b-field v-if="paginationNeeded" grouped position="is-right">
            <div class="control is-flex">
              <b-switch
                v-model="table.isPaginated"
                @update:modelValue="updateExperiment()"
                size="is-small"
                type="is-light"
                >Paginate</b-switch
              >
            </div>
          </b-field>
          <b-table
            :key="table.key"
            :data="experiment.vms"
            :paginated="table.isPaginated"
            aria-next-label="Next page"
            aria-previous-label="Previous page"
            aria-page-label="Page"
            aria-current-label="Current page"
            backend-pagination
            :total="table.total"
            :per-page="table.perPage"
            v-model:current-page="table.currentPage"
            @page-change="onPageChange"
            :pagination-simple="table.isPaginationSimple"
            :pagination-size="table.paginationSize"
            backend-sorting
            :default-sort-direction="table.defaultSortDirection"
            default-sort="name"
            @sort="onSort"
            ref="vmTable">
            <template #empty>
              <section class="section">
                <div class="content has-text-white has-text-centered">
                  {{ vmsEmptyText }}
                </div>
              </section>
            </template>
            <b-table-column field="multiselect" label="">
              <template v-slot:header>
                <b-tooltip label="Select/Unselect All" type="is-dark">
                  <b-checkbox v-model="checkAll">
                    <span class="is-sr-only">Select all VMs</span>
                  </b-checkbox>
                </b-tooltip>
              </template>
              <template v-slot:default="props">
                <div>
                  <b-checkbox
                    :disabled="props.row.external"
                    v-model="selectedRows"
                    :native-value="props.row.name">
                    <span class="is-sr-only">
                      Select VM {{ props.row.name }}
                    </span>
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
              <template
                v-if="
                  !props.row.external &&
                  roleAllowed(
                    'vms',
                    'get',
                    experiment.name + '/' + props.row.name,
                  )
                ">
                <b-tooltip
                  label="get info on the vm"
                  type="is-dark"
                  class="is-clickable">
                  <div class="field">
                    <div
                      @click="
                        expModal.active = true;
                        expModal.vm = props.row;
                      ">
                      {{ props.row.name }}
                    </div>
                  </div>
                </b-tooltip>
              </template>
              <template v-else>
                {{ props.row.name }}
              </template>
            </b-table-column>
            <b-table-column
              field="host"
              label="Host"
              width="200"
              sortable
              header-class="sort-inline"
              v-slot="props">
              <template
                v-if="
                  !props.row.external &&
                  roleAllowed(
                    'vms',
                    'patch',
                    experiment.name + '/' + props.row.name,
                  )
                ">
                <b-tooltip
                  label="assign the vm to a specific host"
                  type="is-dark">
                  <b-field>
                    <b-select
                      :aria-label="`Host for VM ${props.row.name}`"
                      v-model="props.row.host"
                      expanded
                      @update:modelValue="
                        (value) => assignHost(props.row.name, value)
                      ">
                      <option
                        v-for="(h, index) in hosts"
                        :key="index"
                        :value="h">
                        {{ h }}
                      </option>
                    </b-select>
                    <p class="control">
                      <b-button
                        :aria-label="`Unassign host from VM ${props.row.name}`"
                        class="button input-button"
                        icon-right="window-close"
                        @click="unassignHost(props.row.name, props.row.host)">
                      </b-button>
                    </p>
                  </b-field>
                </b-tooltip>
              </template>
              <template v-else-if="props.row.external"> EXTERNAL </template>
              <template v-else>
                {{ props.row.host }}
              </template>
            </b-table-column>
            <b-table-column field="ipv4" label="IP" v-slot="props">
              <div v-for="(ip, index) in props.row.ipv4" :key="index">
                {{ ip || 'unknown' }}
              </div>
            </b-table-column>
            <b-table-column
              field="cpus"
              label="CPUs"
              width="100"
              sortable
              header-class="sort-inline"
              centered
              v-slot="props">
              <template
                v-if="
                  !props.row.external &&
                  roleAllowed(
                    'vms',
                    'patch',
                    experiment.name + '/' + props.row.name,
                  )
                ">
                <b-tooltip
                  label="menu for assigning the VM's CPUs"
                  type="is-dark">
                  <b-select
                    :aria-label="`CPUs for VM ${props.row.name}`"
                    v-model="props.row.cpus"
                    expanded
                    @update:modelValue="
                      (value) => assignCpu(props.row.name, value)
                    ">
                    <option value="1">1</option>
                    <option value="2">2</option>
                    <option value="3">3</option>
                    <option value="4">4</option>
                  </b-select>
                </b-tooltip>
              </template>
              <template v-else>
                {{ props.row.cpus || 'unknown' }}
              </template>
            </b-table-column>
            <b-table-column
              field="ram"
              label="Memory"
              width="112"
              sortable
              header-class="sort-inline"
              centered
              v-slot="props">
              <template
                v-if="
                  !props.row.external &&
                  roleAllowed(
                    'vms',
                    'patch',
                    experiment.name + '/' + props.row.name,
                  )
                ">
                <b-tooltip
                  label="menu for assigning the VM's memory"
                  type="is-dark">
                  <b-select
                    :aria-label="`Memory for VM ${props.row.name}`"
                    v-model="props.row.ram"
                    expanded
                    @update:modelValue="
                      (value) => assignRam(props.row.name, value)
                    ">
                    <option value="512">512 MB</option>
                    <option value="1024">1 GB</option>
                    <option value="2048">2 GB</option>
                    <option value="3072">3 GB</option>
                    <option value="4096">4 GB</option>
                    <option value="8192">8 GB</option>
                    <option value="12288">12 GB</option>
                    <option value="16384">16 GB</option>
                  </b-select>
                </b-tooltip>
              </template>
              <template v-else>
                {{ props.row.ram || 'unknown' }}
              </template>
            </b-table-column>
            <b-table-column field="disk" label="Disk" v-slot="props">
              <template
                v-if="
                  !props.row.external &&
                  roleAllowed(
                    'vms',
                    'patch',
                    experiment.name + '/' + props.row.name,
                  )
                ">
                <DiskSelect
                  :model-value="props.row.disk"
                  :disks="disks"
                  :label="`Disk for VM ${props.row.name}`"
                  expanded
                  @update:model-value="
                    (path) => assignDisk(props.row.name, path)
                  " />
              </template>
              <template v-else>
                <span :title="props.row.disk">
                  {{ diskText(props.row.disk) }}
                </span>
                <DiskWarning
                  v-if="diskWarning(disks, props.row.disk)"
                  :reason="diskWarning(disks, props.row.disk)"
                  :files-dir="filesDir(disks)" />
              </template>
            </b-table-column>
            <b-table-column
              field="inject_partition"
              label="Partition"
              sortable
              header-class="sort-inline"
              centered
              v-slot="props">
              <template
                v-if="
                  !props.row.external &&
                  roleAllowed(
                    'vms',
                    'patch',
                    experiment.name + '/' + props.row.name,
                  )
                ">
                <b-tooltip
                  label="menu for assigning inject partition"
                  type="is-dark">
                  <b-select
                    :aria-label="`Partition for VM ${props.row.name}`"
                    v-model="props.row.inject_partition"
                    expanded
                    @update:modelValue="
                      (value) => assignPartition(props.row.name, value)
                    ">
                    <option value="1">1</option>
                    <option value="2">2</option>
                    <option value="3">3</option>
                    <option value="4">4</option>
                  </b-select>
                </b-tooltip>
              </template>
              <template v-else>
                {{ props.row.inject_partition }}
              </template>
            </b-table-column>
            <b-table-column label="Labels" centered v-slot="props">
              <b-tooltip label="View/Edit Labels" type="is-dark">
                <div @click="showTagsModal(props.row)" class="is-clickable">
                  <font-awesome-layers full-width>
                    <font-awesome-icon icon="tag" />
                    <font-awesome-layers-text
                      counter
                      :value="tagCount(props.row.tags)" />
                  </font-awesome-layers>
                </div>
              </b-tooltip>
            </b-table-column>
            <b-table-column label="Boot" centered v-slot="props">
              <template
                v-if="
                  roleAllowed(
                    'vms',
                    'patch',
                    experiment.name + '/' + props.row.name,
                  )
                ">
                <b-tooltip
                  :label="getBootLabel(props.row)"
                  type="is-dark"
                  class="is-clickable">
                  <div @click="updateDnb(props.row)">
                    <font-awesome-icon
                      :class="bootDecorator(props.row)"
                      icon="bolt" />
                  </div>
                </b-tooltip>
              </template>
            </b-table-column>
            <b-table-column label="Persistence" centered v-slot="props">
              <template
                v-if="
                  roleAllowed(
                    'vms',
                    'patch',
                    experiment.name + '/' + props.row.name,
                  )
                ">
                <b-tooltip :label="getSnapshotLabel(props.row)" type="is-dark">
                  <div>
                    <b-select
                      :aria-label="`Persistence for VM ${props.row.name}`"
                      v-model="props.row.snapshot"
                      expanded
                      @update:modelValue="
                        (value) => updateSnapshot(props.row.name, value)
                      ">
                      <option value="true">Non-Persistent</option>
                      <option value="false">Persistent</option>
                    </b-select>
                  </div>
                </b-tooltip>
              </template>
            </b-table-column>
          </b-table>
        </b-tab-item>
        <b-tab-item label="Files" icon="file-alt" :visible="canListFiles">
          <ExperimentFilesTab
            ref="filesTab"
            paginate-key="stopped-files"
            :filter="searchName"
            :category="fileCategory"
            @categories="(list) => (fileCategories = list)"
            @found="addSearchHistory"
            @waiting="(on) => (isWaiting = on)" />
        </b-tab-item>
      </b-tabs>
    </div>
    <b-loading
      :is-full-page="false"
      v-model="isWaiting"
      :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  import DiskWarning from '@/components/DiskWarning.vue';
  import DiskSelect from '@/components/experiment/DiskSelect.vue';
  import ExperimentFilesTab from '@/components/experiment/ExperimentFilesTab.vue';
  import { debounce } from 'lodash-es';
  import { tagCount } from '@/utils/tagCount';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';
  import VMLabelsModal from '@/components/VMLabelsModal.vue';
  import { roleAllowed } from '@/utils/rbac.js';
  import { plural, pluralWord } from '@/utils/plural.js';
  import axiosInstance from '@/utils/axios.js';
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import {
    diskLabel,
    diskWarning,
    filesDir,
    findDisk,
  } from '@/utils/diskChain.js';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import {
    schedulableHostNames,
    stoppedExperimentKey,
  } from '@/utils/pageData.js';
  import { useTable } from '@/utils/useTable.js';

  export default {
    emits: ['running'],
    components: { DiskSelect, DiskWarning, ExperimentFilesTab },
    mixins: [formattingMixin],
    setup() {
      const { table } = useTable({
        name: 'stopped-vms',
        fields: { key: 0, total: 0, sortColumn: 'name' },
      });
      return {
        table,
        roleAllowed,
        tagCount,
        pluralWord,
        diskWarning,
        filesDir,
      };
    },
    beforeUnmount() {
      removeWsHandler(this.handleWs);
      this.loader.stop();
    },

    async created() {
      addWsHandler(this.handleWs);
      this.loader = createPageLoader({
        // only the default view is cached, as its request is the same each
        // visit
        key: () =>
          this.searchName || this.table.isPaginated || !this.defaultSort
            ? null
            : stoppedExperimentKey(this.$route.params.id),
        fetch: async (signal) => {
          const params = {
            show_dnb: true,
            filter: this.searchName,
            sortCol: this.table.sortColumn,
            sortDir: this.table.defaultSortDirection,
          };

          if (this.table.isPaginated) {
            params.pageNum = this.table.currentPage;
            params.perPage = this.table.perPage;
          }

          const resp = await axiosInstance.get(
            'experiments/' + this.$route.params.id,
            { params, signal },
          );
          return resp.data;
        },
        apply: (experiment) => {
          if (experiment.running) {
            // started since the page was opened; Base shows the running view
            this.$emit('running', true);
            return;
          }
          this.experiment = experiment;
          // vms holds only the current page; vm_count is the total before paging
          this.table.total = experiment.vm_count ?? 0;

          this.vlanModal.vlans = this.experiment.vlans.map((vlan) => {
            return vlan;
          });

          // Only add successful searches to the search history
          if (this.table.total > 0) {
            this.addSearchHistory();
          }
        },
        refresh: () => {
          this.updateLists();
          return this.loader.load();
        },
      });
      this.loader.start();
      this.updateLists();
    },

    computed: {
      defaultSort() {
        return (
          this.table.sortColumn === 'name' &&
          this.table.defaultSortDirection === 'asc'
        );
      },
      vmsEmptyText() {
        if (!this.experiment.name) return loadingText('VMs');
        if (this.searchName) return 'No VMs match your search';
        return 'This experiment has no VMs';
      },

      canListFiles() {
        return roleAllowed('experiments/files', 'list', this.$route.params.id);
      },

      paginationNeeded() {
        return this.table.total > this.table.perPage;
      },
    },

    methods: {
      searchVMs: debounce(function (term) {
        if (term === null) {
          term = '';
        }
        this.searchName = term;
        if (this.activeTab == 0) {
          this.updateExperiment();
          return;
        }

        this.$refs.filesTab?.reload();
      }, 250),

      bootDecorator(vm) {
        if (vm.external) {
          return 'dnb';
        }

        if (vm.dnb) {
          return 'dnb';
        } else {
          return 'boot';
        }
      },

      onPageChange(page) {
        this.table.currentPage = page;
        this.updateExperiment();
      },

      onSort(column, order) {
        this.table.sortColumn = column;
        this.table.defaultSortDirection = order;
        this.updateExperiment();
      },

      handleWs(msg) {
        // experiment resources are named "exp", VM resources "exp/vm"
        const [exp] = (msg.resource?.name ?? '').split('/');
        if (exp !== this.$route.params.id || !this.experiment.vms) {
          return;
        }

        switch (msg.resource.type) {
          case 'experiment': {
            // We only care about experiment publishes pertaining to the
            // schedule action when in a stopped experiment.
            if (msg.resource.action != 'schedule') {
              return;
            }

            this.applySchedule(msg.result.schedule);

            this.$buefy.toast.open({
              message: 'The VMs for this experiment have been scheduled.',
              type: 'is-success',
            });

            break;
          }

          case 'experiment/vm': {
            // We only care about experiment VM publishes pertaining to
            // the update action when in a stopped experiment.
            if (msg.resource.action != 'update') {
              return;
            }

            let vms = this.experiment.vms;

            for (let i = 0; i < vms.length; i++) {
              if (vms[i].name == msg.result.name) {
                vms[i] = msg.result;

                break;
              }
            }

            this.experiment.vms = [...vms];

            this.$buefy.toast.open({
              message:
                'The VM ' + msg.result.name + ' has been successfully updated.',
              type: 'is-success',
            });

            break;
          }
        }
      },

      // reloads the VM table for the current search, sort and page
      async updateExperiment() {
        await this.loader.load();
        this.isWaiting = false;
      },

      // the host and disk choices for editing VMs; loaded on open and on
      // refresh rather than with every search, since listing disks is slow
      updateLists() {
        if (roleAllowed('hosts', 'list')) {
          this.updateHosts();
        }
        if (roleAllowed('disks', 'list')) {
          this.updateDisks();
        }
      },

      // shares the Hosts page's cached list, as listing hosts scans the
      // whole cluster
      updateHosts() {
        schedulableHostNames().then(
          (hosts) => {
            this.hosts = hosts;
          },
          (err) => {
            useErrorNotification(err);
          },
        );
      },

      // the images inside the minimega files directory and the ones this
      // experiment names outside it
      updateDisks() {
        axiosInstance
          .get('disks', { params: { expName: this.$route.params.id } })
          .then(
            (response) => {
              this.disks = response.data.disks ?? [];
            },
            (err) => {
              useErrorNotification(err);
            },
          );
      },

      // a VM's disk by its label, or by its full path when not listed
      diskText(path) {
        return diskLabel(findDisk(this.disks, path)) || path || 'unknown';
      },

      updateVLANs() {
        this.vlanModal.active = false;
        let vlans = {};

        for (let i = 0; i < this.vlanModal.vlans.length; i++) {
          let obj = this.vlanModal.vlans[i];
          if (obj.vlan !== 0) {
            vlans[obj.alias] = obj.vlan;
          }
        }

        let body = JSON.stringify(vlans);

        axiosInstance.patch('experiments/' + this.$route.params.id, body).then(
          (_) => {
            this.$buefy.toast.open({
              message:
                'Updating the VLAN Assignment for the ' +
                this.$route.params.id +
                ' Experiment was successful.',
              type: 'is-success',
              duration: 4000,
            });
          },
          (err) => {
            useErrorNotification(err);
          },
        );
      },

      assignCategory(value) {
        this.fileCategory = value;
        this.$refs.filesTab?.reload();
      },

      addSearchHistory() {
        if (this.searchHistory.length >= this.searchHistoryLength) {
          this.searchHistory.pop();
        }
        this.searchHistory.push(this.searchName.trim());
        this.searchHistory = this.getUniqueItems(this.searchHistory);
      },

      start() {
        this.$buefy.dialog.confirm({
          title: 'Start the Experiment',
          message:
            'This will start the ' + this.$route.params.id + ' experiment.',
          cancelText: 'Cancel',
          confirmText: 'Start',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            axiosInstance
              .post('experiments/' + this.$route.params.id + '/start')
              .then(
                () => {
                  this.$router.replace('/experiments/');
                },
                (err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                },
              );
          },
        });
      },

      assignHost(name, host) {
        this.$buefy.dialog.confirm({
          title: 'Assign a Host',
          message:
            'This will assign the ' + name + ' VM to the ' + host + ' host.',
          cancelText: 'Cancel',
          confirmText: 'Assign Host',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            let update = { host: host };

            axiosInstance
              .patch(
                'experiments/' + this.$route.params.id + '/vms/' + name,
                update,
              )
              .then(
                (response) => {
                  let vms = this.experiment.vms;

                  for (let i = 0; i < vms.length; i++) {
                    if (vms[i].name == response.data.name) {
                      vms[i] = response.data;
                      break;
                    }
                  }

                  this.experiment.vms = [...vms];

                  this.isWaiting = false;
                },
                (err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                },
              );
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          },
        });
      },

      unassignHost(name, _) {
        this.$buefy.dialog.confirm({
          title: 'Unassign a Host',
          message: 'This will cancel the host assignment for ' + name + ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Unassign Host',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            let update = { host: '' };

            axiosInstance
              .patch(
                'experiments/' + this.$route.params.id + '/vms/' + name,
                update,
              )
              .then(
                (response) => {
                  let vms = this.experiment.vms;

                  for (let i = 0; i < vms.length; i++) {
                    if (vms[i].name == response.data.name) {
                      vms[i] = response.data;
                      break;
                    }
                  }

                  this.experiment.vms = [...vms];

                  this.isWaiting = false;
                },
                (err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                },
              );
          },
        });
      },

      assignCpu(name, cpus) {
        this.$buefy.dialog.confirm({
          title: 'Assign CPUs',
          message:
            'This will assign ' +
            plural(cpus, 'CPU') +
            ' to the ' +
            name +
            ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Assign CPUs',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            let update = { cpus: cpus };

            axiosInstance
              .patch(
                'experiments/' + this.$route.params.id + '/vms/' + name,
                update,
              )
              .then(
                (response) => {
                  let vms = this.experiment.vms;

                  for (let i = 0; i < vms.length; i++) {
                    if (vms[i].name == response.data.name) {
                      vms[i] = response.data;
                      break;
                    }
                  }

                  this.experiment.vms = [...vms];

                  this.isWaiting = false;
                },
                (err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                },
              );
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          },
        });
      },

      assignRam(name, ram) {
        this.$buefy.dialog.confirm({
          title: 'Assign Memory',
          message:
            'This will assign ' + ram + ' of memory to the ' + name + ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Assign Memory',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            let update = { ram: ram };

            axiosInstance
              .patch(
                'experiments/' + this.$route.params.id + '/vms/' + name,
                update,
              )
              .then(
                (response) => {
                  let vms = this.experiment.vms;

                  for (let i = 0; i < vms.length; i++) {
                    if (vms[i].name == response.data.name) {
                      vms[i] = response.data;
                      break;
                    }
                  }

                  this.experiment.vms = [...vms];

                  this.isWaiting = false;
                },
                (err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                },
              );
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          },
        });
      },

      assignDisk(name, disk) {
        this.$buefy.dialog.confirm({
          title: 'Assign a Disk Image',
          message:
            'This will assign the ' +
            escapeHTML(diskLabel(findDisk(this.disks, disk)) || disk) +
            ' disk image to the ' +
            escapeHTML(name) +
            ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Assign Disk',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            let update = { disk: disk };

            axiosInstance
              .patch(
                'experiments/' + this.$route.params.id + '/vms/' + name,
                update,
              )
              .then(
                (response) => {
                  let vms = this.experiment.vms;

                  for (let i = 0; i < vms.length; i++) {
                    if (vms[i].name == response.data.name) {
                      vms[i] = response.data;
                      break;
                    }
                  }

                  this.experiment.vms = [...vms];

                  this.isWaiting = false;
                },
                (err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                },
              );
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          },
        });
      },
      assignPartition(name, partition) {
        this.$buefy.dialog.confirm({
          title: 'Assign an Image Partition',
          message:
            'This will assign the image partition ' +
            partition +
            ' to the ' +
            name +
            ' VM.',
          cancelText: 'Cancel',
          confirmText: 'Assign Partition',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            let update = { inject_partition: partition };

            axiosInstance
              .patch(
                'experiments/' + this.$route.params.id + '/vms/' + name,
                update,
              )
              .then(
                (response) => {
                  let vms = this.experiment.vms;

                  for (let i = 0; i < vms.length; i++) {
                    if (vms[i].name == response.data.name) {
                      vms[i] = response.data;
                      break;
                    }
                  }

                  this.experiment.vms = [...vms];

                  this.isWaiting = false;
                },
                (err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                },
              );
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          },
        });
      },

      showTagsModal(vm) {
        this.$buefy.modal.open({
          component: VMLabelsModal,
          trapFocus: true,
          hasModalCard: true,
          props: {
            vmName: vm.name,
            experiment: this.$route.params.id,
            tags: vm.tags,
          },
        });
      },

      updateDnb(vm) {
        if (vm.external) {
          return;
        }

        this.isWaiting = true;

        let update = { dnb: !vm.dnb };

        axiosInstance
          .patch(
            'experiments/' + this.$route.params.id + '/vms/' + vm.name,
            update,
          )
          .then(
            (response) => {
              let vms = this.experiment.vms;

              for (let i = 0; i < vms.length; i++) {
                if (vms[i].name == response.data.name) {
                  vms[i] = response.data;
                  break;
                }
              }

              this.experiment.vms = [...vms];

              this.isWaiting = false;
            },
            (err) => {
              useErrorNotification(err);
              this.isWaiting = false;
            },
          );
      },

      updateSnapshot(name, persistence) {
        let persistenceMessage;
        if (persistence == 'true') {
          persistenceMessage = 'Non-Persistent';
        } else {
          persistenceMessage = 'Persistent';
        }
        if (persistence == 'true') {
          persistence = true;
        } else {
          persistence = false;
        }
        this.$buefy.dialog.confirm({
          title: 'Assign Image Persistence',
          message:
            'This will assign the ' +
            name +
            " VM's disk to be " +
            persistenceMessage,
          cancelText: 'Cancel',
          confirmText: 'Confirm',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            let update = { snapshot: persistence };

            axiosInstance
              .patch(
                'experiments/' + this.$route.params.id + '/vms/' + name,
                update,
              )
              .then(
                (response) => {
                  let vms = this.experiment.vms;

                  for (let i = 0; i < vms.length; i++) {
                    if (vms[i].name == response.data.name) {
                      vms[i] = response.data;
                      break;
                    }
                  }

                  this.experiment.vms = [...vms];

                  this.isWaiting = false;
                },
                (err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                },
              );
          },
          onCancel: () => {
            // force table to be rerendered so selected value resets
            this.table.key += 1;
          },
        });
      },
      updateSchedule() {
        this.$buefy.dialog.confirm({
          title: 'Assign a Host Schedule',
          message:
            'This will schedule hosts with the ' +
            this.algorithm +
            ' algorithm for the ' +
            this.$route.params.id +
            ' experiment.',
          cancelText: 'Cancel',
          confirmText: 'Assign Schedule',
          type: 'is-success',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            axiosInstance
              .post('experiments/' + this.$route.params.id + '/schedule', {
                algorithm: this.algorithm,
              })
              .then(
                (response) => {
                  this.applySchedule(response.data.schedule);
                  this.isWaiting = false;
                },
                (err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                },
              );
          },
        });
      },

      // sets each VM's host from a schedule of {vm, host} assignments
      applySchedule(schedule) {
        if (!schedule || !this.experiment.vms) {
          return;
        }

        const hosts = new Map(schedule.map((s) => [s.vm, s.host]));
        this.experiment.vms = this.experiment.vms.map((vm) =>
          hosts.has(vm.name) ? { ...vm, host: hosts.get(vm.name) } : vm,
        );
      },

      getBootLabel(vm) {
        return vm.dnb ? `Boot ${vm.name}` : `Do Not Boot ${vm.name}`;
      },
      getSnapshotLabel(vm) {
        return vm.snapshot
          ? `${vm.name}'s disk will not persist`
          : `${vm.name}'s disk will persist`;
      },

      setBoot(dnb) {
        let vms = [...this.selectedRows];

        let successMessage = '';

        if (vms.length == 0) {
          return;
        }

        const selected =
          vms.length == 1 ? 'The selected VM was' : 'The selected VMs were';
        successMessage = `${selected} set to ${dnb ? 'not boot' : 'boot'}`;

        let requestList = [];

        vms.forEach((vmName) => {
          let update = { name: vmName, dnb: dnb };
          requestList.push(update);
        });

        axiosInstance
          .patch('experiments/' + this.$route.params.id + '/vms', {
            vms: requestList,
            total: requestList.length,
          })
          .then(
            (response) => {
              let vms = this.experiment.vms;

              for (let i = 0; i < response.data.vms.length; i++) {
                for (let j = 0; j < vms.length; j++) {
                  if (response.data.vms[i].name == vms[j].name) {
                    vms[j] = response.data.vms[i];
                    break;
                  }
                }
              }

              this.experiment.vms = [...vms];
              this.isWaiting = false;

              this.$buefy.toast.open({
                message: successMessage,
                type: 'is-success',
                duration: 4000,
              });
            },
            (err) => {
              useErrorNotification(err);
              this.isWaiting = false;
            },
          );

        // clear the selection; the checkAll watcher does not fire when it
        // is already false, so rows picked one by one are cleared here
        this.checkAll = false;
        this.selectedRows = [];
      },
    },

    watch: {
      checkAll(newVal) {
        if (newVal) {
          var visibleItems = this.$refs['vmTable'].visibleData;
          // add all visible items
          this.selectedRows = [];
          for (var i = 0; i < visibleItems.length; i++) {
            let item = visibleItems[i];

            if (!item.external) {
              this.selectedRows.push(item.name);
            }
          }
        } else {
          this.selectedRows = [];
        }
      },
      activeTab(newVal) {
        // Clear search history and
        // search filter when switching tabs
        this.searchHistory = [];
        this.searchName = '';

        if (newVal == 0) {
          this.searchPlaceholder = 'Find a VM';
          this.updateExperiment();
        } else {
          this.searchPlaceholder = 'Find a File';
          this.$refs.filesTab?.reload();
        }
      },
    },

    data() {
      return {
        expModal: {
          active: false,
          vm: [],
        },
        schedules: ['isolate_experiment', 'round_robin'],
        experiment: [],
        // the Files tab's category picker, and the categories it offers
        fileCategory: null,
        fileCategories: [],
        hosts: [],
        // the disk list, null until it has loaded
        disks: null,
        searchName: '',
        algorithm: null,
        isWaiting: false, // set while a change is being saved
        searchHistory: [],
        searchHistoryLength: 10,
        checkAll: false,
        selectedRows: [],
        searchPlaceholder: 'Find a VM',
        activeTab: 0,
        vlanModal: {
          active: false,
          vlans: [],
        },
      };
    },
  };
</script>

<style lang="scss" scoped>
  @import '@/assets/_variables.scss';

  .boot {
    color: $primary;
  }

  .dnb {
    color: #ffffff;
  }

  .fa-layers-counter {
    /* counter on tag icon */
    transform: scale(0.7) translateX(50%) translateY(-50%);
  }

  :deep(.tabs ul) {
    margin-left: 0px !important;
  }

  :deep(.b-tabs .tab-content) {
    padding: 1rem 0 0 0;
  }
</style>
