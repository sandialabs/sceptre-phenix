<template>
  <div>
    <b-modal
      v-model="detailsModal.active"
      @close="resetDetailsModal"
      has-modal-card
      :width="1280">
      <div class="modal-card" style="width: auto">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ detailsModal.vm }} Details</p>
        </header>
        <section class="modal-card-body">
          <p class="title is-4">Notes</p>
          <div class="content">
            <ul>
              <li
                v-for="k in Object.keys(detailsModal.tags).filter((k) =>
                  k.startsWith('__notes_'),
                )"
                :key="k">
                <p style="white-space: pre-line">
                  {{ detailsModal.tags[k] }}
                </p>
              </li>
            </ul>
          </div>
          <hr />
          <p class="title is-4">State of Health</p>
          <template v-if="detailsModal.soh">
            <br />
            <div v-if="detailsModal.soh.cpuLoad">
              <p class="title is-6">CPU Load: {{ detailsModal.soh.cpuLoad }}</p>
            </div>
            <br />
            <div v-if="detailsModal.soh.networking">
              <p class="title is-6">Networking</p>
              <b-table
                :data="detailsModal.soh.networking"
                default-sort="timestamp">
                <b-table-column
                  field="timestamp"
                  label="Timestamp"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column
                  field="success"
                  label="Success"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column
                  field="error"
                  label="Error"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br />
            </div>
            <div v-if="detailsModal.soh.reachability">
              <p class="title is-6">Reachability</p>
              <b-table
                :data="detailsModal.soh.reachability"
                default-sort="timestamp">
                <b-table-column
                  field="timestamp"
                  label="Timestamp"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column
                  field="host"
                  label="Source"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.metadata.host }}
                </b-table-column>
                <b-table-column
                  field="target"
                  label="Target"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.metadata.target }}
                </b-table-column>
                <b-table-column
                  field="success"
                  label="Success"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column
                  field="error"
                  label="Error"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br />
            </div>
            <div v-if="detailsModal.soh.processes">
              <p class="title is-6">Processes</p>
              <b-table
                :data="detailsModal.soh.processes"
                default-sort="timestamp">
                <b-table-column
                  field="timestamp"
                  label="Timestamp"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column
                  field="process"
                  label="Process"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.metadata.proc }}
                </b-table-column>
                <b-table-column
                  field="success"
                  label="Success"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column
                  field="error"
                  label="Error"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br />
            </div>
            <div v-if="detailsModal.soh.files">
              <p class="title is-6">Files</p>
              <b-table :data="detailsModal.soh.files" default-sort="timestamp">
                <b-table-column
                  field="timestamp"
                  label="Timestamp"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column
                  field="path"
                  label="Path"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.metadata.path }}
                </b-table-column>
                <b-table-column
                  field="success"
                  label="Success"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column
                  field="error"
                  label="Error"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br />
            </div>
            <div v-if="detailsModal.soh.filesAbsent">
              <p class="title is-6">Files (Absent)</p>
              <b-table
                :data="detailsModal.soh.filesAbsent"
                default-sort="timestamp">
                <b-table-column
                  field="timestamp"
                  label="Timestamp"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column
                  field="path"
                  label="Path"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.metadata.path }}
                </b-table-column>
                <b-table-column
                  field="success"
                  label="Success"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column
                  field="error"
                  label="Error"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br />
            </div>
            <div v-if="detailsModal.soh.services">
              <p class="title is-6">Services</p>
              <b-table
                :data="detailsModal.soh.services"
                default-sort="timestamp">
                <b-table-column
                  field="timestamp"
                  label="Timestamp"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column
                  field="service"
                  label="Service"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.metadata.service }}
                </b-table-column>
                <b-table-column
                  field="success"
                  label="Success"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column
                  field="error"
                  label="Error"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br />
            </div>
            <div v-if="detailsModal.soh.listeners">
              <p class="title is-6">Listeners</p>
              <b-table
                :data="detailsModal.soh.listeners"
                default-sort="timestamp">
                <b-table-column
                  field="timestamp"
                  label="Timestamp"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column
                  field="port"
                  label="Port"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.metadata.port }}
                </b-table-column>
                <b-table-column
                  field="success"
                  label="Success"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column
                  field="error"
                  label="Error"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br />
            </div>
            <div v-if="detailsModal.soh.containers">
              <p class="title is-6">Docker Containers</p>
              <b-table
                :data="detailsModal.soh.containers"
                default-sort="timestamp">
                <b-table-column
                  field="timestamp"
                  label="Timestamp"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column
                  field="container"
                  label="Container"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.metadata.container }}
                </b-table-column>
                <b-table-column
                  field="success"
                  label="Success"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column
                  field="error"
                  label="Error"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br />
            </div>
            <div v-if="detailsModal.soh.customTests">
              <p class="title is-6">Custom User Tests</p>
              <b-table :data="detailsModal.soh.customTests" default-sort="test">
                <b-table-column
                  field="test"
                  label="Test Name"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.metadata.test }}
                </b-table-column>
                <b-table-column
                  field="timestamp"
                  label="Timestamp"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.timestamp }}
                </b-table-column>
                <b-table-column
                  field="success"
                  label="Success"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.success }}
                </b-table-column>
                <b-table-column
                  field="error"
                  label="Error"
                  sortable
                  header-class="sort-inline"
                  v-slot="props">
                  {{ props.row.error }}
                </b-table-column>
              </b-table>
              <br />
            </div>
          </template>
          <template v-else>
            <p>
              There is no state of health data available for
              {{ detailsModal.vm }}.
            </p>
          </template>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <template
            v-if="
              roleAllowed(
                'vms',
                'patch',
                $route.params.id + '/' + detailsModal.vm,
              )
            ">
            <b-tooltip
              label="edit notes/labels"
              type="is-light is-left"
              :delay="400">
              <b-button
                aria-label="Edit notes and labels"
                class="button is-light"
                icon-left="tag"
                @click="showTagsModal(detailsModal.vm, detailsModal.tags)" />
            </b-tooltip>
          </template>
          <template
            v-if="
              roleAllowed(
                'vms',
                'patch',
                $route.params.id + '/' + detailsModal.vm,
              )
            ">
            <b-tooltip
              label="customize style"
              type="is-light is-left"
              :delay="400">
              <b-button
                class="button is-light"
                icon-left="paintbrush"
                @click="showStyleModal(detailsModal.vm)" />
            </b-tooltip>
          </template>
          <template v-if="detailsModal.status.toLowerCase() == 'running'">
            <a :href="vncLoc(detailsModal.vm)" target="_blank">
              <b-tooltip
                label="open vnc for a running vm"
                type="is-light is-left"
                :delay="400">
                <b-button type="is-success">
                  <b-icon icon="tv" />
                </b-button>
              </b-tooltip>
            </a>
          </template>
          <template v-else>
            <b-tooltip
              label="vnc is only available on a running vm"
              type="is-light is-left"
              :delay="400">
              <b-button type="is-danger" disabled>
                <b-icon icon="tv" />
              </b-button>
            </b-tooltip>
          </template>
        </footer>
      </div>
    </b-modal>
    <!-- END MODAL -->
    <!-- STYLE MODAL -->
    <b-modal
      v-model="styleModal.active"
      @close="resetStyleModal"
      has-modal-card>
      <div class="modal-card">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ detailsModal.vm }} Style</p>
        </header>
        <section class="modal-card-body">
          <div class="columns is-vcentered">
            <div class="column is-three-quarters">
              <p class="title is-6 mb-2">Fill Color</p>
              <b-field grouped>
                <b-switch :left-label="true" v-model="styleModal.overrideFill"
                  >Override</b-switch
                >
                <b-colorpicker
                  :append-to-body="true"
                  :disabled="!styleModal.overrideFill"
                  v-model="styleModal.fill" />
              </b-field>

              <p class="title is-6 mb-2">Stroke Color</p>
              <b-field grouped>
                <b-switch :left-label="true" v-model="styleModal.overrideStroke"
                  >Override</b-switch
                >
                <b-colorpicker
                  :append-to-body="true"
                  :disabled="!styleModal.overrideStroke"
                  v-model="styleModal.stroke" />
              </b-field>

              <p class="title is-6 mb-2">Stroke Style</p>
              <b-field grouped>
                <b-switch
                  :left-label="true"
                  v-model="styleModal.overrideStrokeStyle"
                  >Override</b-switch
                >
                <b-select
                  :disabled="!styleModal.overrideStrokeStyle"
                  v-model="styleModal.strokeStyle">
                  <option value="solid">Solid</option>
                  <option value="dashed">Dashed</option>
                  <option value="dotted">Dotted</option>
                </b-select>
              </b-field>
            </div>
            <div class="column has-text-centered">
              <p>Preview:</p>
              <svg height="64" width="64" xmlns="http://www.w3.org/2000/svg">
                <circle
                  cx="8"
                  cy="8"
                  r="5"
                  stroke-width="1.5"
                  transform="scale(4)"
                  :fill="styleModal.baseFill"
                  :stroke="styleModal.baseStroke"
                  :style="styleModalCustomStyle"></circle>
              </svg>
            </div>
          </div>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <b-button label="Close" @click="resetStyleModal()" />
          <b-button
            v-if="
              roleAllowed(
                'vms',
                'patch',
                $route.params.id + '/' + styleModal.vm,
              )
            "
            label="Save"
            type="is-primary"
            @click="saveStyle()" />
        </footer>
      </div>
    </b-modal>
    <!-- END STYLE MODAL -->
    <div class="columns is-centered">
      <div class="column is-2">
        <router-link
          class="button is-dark"
          :to="{
            name: 'experiment',
            params: { id: $route.params.id },
            state: { running: running },
          }">
          <b-icon icon="arrow-left" />
          <span>Go to Experiment</span>
        </router-link>
      </div>
      <div class="column has-text-centered">
        <span style="font-weight: bold; font-size: x-large"
          >State of Health for Experiment: {{ $route.params.id }}</span
        >
      </div>
      <div class="column is-2" />
    </div>
    <div>
      <!-- the tab bar only appears when there is a second tab to switch to -->
      <b-tabs :class="{ 'single-tab': !flows }">
        <b-tab-item label="Topology Graph">
          <div class="soh-topology">
            <div class="soh-graph-head">
              <div class="soh-counts" aria-live="polite">
                <span class="soh-count" title="VMs shown by the current filter">
                  <b>{{ vmCount }}</b> {{ pluralWord(vmCount, 'VM') }}
                </span>
                <span
                  class="soh-count"
                  title="VLANs shown by the current filter">
                  <b>{{ vlanCount }}</b>
                  {{ pluralWord(vlanCount, 'VLAN') }}
                </span>
              </div>
              <div class="soh-controls">
                <b-radio
                  v-model="radioButton"
                  native-value="all"
                  type="is-light"
                  >All</b-radio
                >
                <b-radio
                  v-model="radioButton"
                  native-value="running"
                  type="is-light"
                  >Running</b-radio
                >
                <b-radio
                  v-model="radioButton"
                  native-value="notrunning"
                  type="is-light"
                  >Not running</b-radio
                >
                <b-radio
                  v-model="radioButton"
                  native-value="notboot"
                  type="is-light"
                  >Not booted</b-radio
                >
                <b-radio
                  v-model="radioButton"
                  native-value="notdeploy"
                  type="is-light"
                  >Not deployed</b-radio
                >
                <b-radio
                  v-model="radioButton"
                  native-value="external"
                  type="is-light"
                  >External / HIL</b-radio
                >
                <b-button
                  @click="reload"
                  type="is-light"
                  :loading="pageStatus.loading"
                  :disabled="pageStatus.loading"
                  >Refresh Network</b-button
                >
                <b-button
                  v-if="!loaded || !running || sohRunning || !sohInitialized"
                  type="is-light"
                  disabled
                  >{{ sohState }}</b-button
                >
                <b-button
                  v-else-if="
                    roleAllowed(
                      'experiments/trigger',
                      'create',
                      $route.params.id,
                    )
                  "
                  @click="execSoH"
                  type="is-light"
                  >Run SOH</b-button
                >
              </div>
              <div class="soh-links">
                <b-tooltip
                  label="State of Health documentation"
                  type="is-light"
                  position="is-left"
                  :delay="500">
                  <a
                    class="button is-light soh-docs-link"
                    :href="docsPage('state-of-health')"
                    target="_blank"
                    rel="noopener"
                    aria-label="State of Health documentation">
                    <b-icon icon="book" />
                  </a>
                </b-tooltip>
                <b-tooltip
                  v-if="roleAllowed('experiments', 'get', $route.params.id)"
                  label="view scorch"
                  type="is-light"
                  position="is-left"
                  :delay="500">
                  <router-link
                    class="button is-light scorch-link"
                    aria-label="view scorch"
                    :to="{
                      name: 'scorchruns',
                      params: { id: $route.params.id },
                    }">
                    <b-icon icon="fire" />
                  </router-link>
                </b-tooltip>
              </div>
            </div>
            <ul class="soh-legend" aria-label="Legend">
              <li>
                <img :src="vlan" alt="" />
                <span>VLAN segment</span>
              </li>
              <li v-for="item in legend" :key="item.label">
                <b-icon icon="circle" :style="{ color: item.color }" />
                <span>{{ item.label }}</span>
              </li>
            </ul>
            <div class="soh-graph-box">
              <div class="soh-graph-bar">
                <soh-layout-select
                  v-model="layout"
                  :options="layoutChoices"
                  :busy="layoutBusy"
                  :disabled="nodes == null" />
                <div class="soh-graph-bar-end">
                  <b-tooltip
                    label="Fit the whole graph in view"
                    type="is-dark"
                    :delay="400">
                    <b-button
                      type="is-light"
                      icon-left="expand"
                      :disabled="nodes == null || layoutBusy"
                      @click="fitGraph(true)"
                      >Fit</b-button
                    >
                  </b-tooltip>
                  <soh-download-menu
                    :disabled="nodes == null || layoutBusy"
                    :busy="exporting"
                    @download="download" />
                </div>
              </div>
              <!-- sized to the room left in the window (fitGraphView) -->
              <div ref="graphView" class="soh-graph-view">
                <div v-if="nodes == null" class="soh-graph-empty">
                  <h1 class="title">
                    There are no nodes matching your filter criteria!
                  </h1>
                </div>
                <div v-else id="graph"></div>
              </div>
            </div>
          </div>
        </b-tab-item>
        <b-tab-item v-if="flows" label="Network Volume">
          <div
            style="
              margin-top: 10px;
              border: 2px solid whitesmoke;
              background: #333;
            ">
            <div id="chord"></div>
          </div>
        </b-tab-item>
      </b-tabs>
    </div>
  </div>
</template>
<script>
  import { BColorpicker } from 'buefy';
  import FileSaver from 'file-saver';
  import { roleAllowed } from '@/utils/rbac.js';
  import * as d3 from 'd3';
  import VmLabelsModal from '@/components/VMLabelsModal.vue';
  import SohDownloadMenu from '@/components/soh/SohDownloadMenu.vue';
  import SohLayoutSelect from '@/components/soh/SohLayoutSelect.vue';
  import {
    SOH_ERROR_COLOR,
    SOH_STYLE_LABEL_KEY,
    STATUS_COLORS,
    STATUS_LABELS,
    boundsOf,
    countVlans,
    countVms,
    layoutInput,
    nodeFill,
  } from '@/utils/soh/graph.js';
  import {
    DEFAULT_LAYOUT,
    findLayout,
    layoutOptions,
    readLayoutPref,
    resolveLayout,
    runLayout,
    saveLayoutPref,
  } from '@/utils/soh/layouts.js';

  import Linux from '@/assets/imgs/linux.png';
  import CentOS from '@/assets/imgs/centos.png';
  import RedHat from '@/assets/imgs/redhat.png';
  import Windows from '@/assets/imgs/windows.png';
  import Router from '@/assets/imgs/router.png';
  import Firewall from '@/assets/imgs/firewall.png';
  import VLAN from '@/assets/imgs/vlan.png';

  import axiosInstance from '@/utils/axios.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { createPageLoader, pageStatus } from '@/utils/pageLoader.js';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';
  import { usePhenixStore } from '@/store.js';
  import { docsPage } from '@/utils/docs.js';
  import { plural, pluralWord } from '@/utils/plural.js';
  import { roomInWindow } from '@/utils/viewportFit.js';

  export default {
    components: { BColorpicker, SohDownloadMenu, SohLayoutSelect },
    setup() {
      return {
        roleAllowed,
        // the page's load state, for the Refresh Network button
        pageStatus,
        docsPage,
        pluralWord,
      };
    },
    mounted() {
      window.addEventListener('resize', this.fitGraphView);
      this.fitGraphView();
    },
    beforeUnmount() {
      window.removeEventListener('resize', this.fitGraphView);
      removeWsHandler(this.handleWs);
      // a layout still computing must not draw into the next page
      this.graphGen = (this.graphGen ?? 0) + 1;
      this.stopGraph();
      this.loader.stop();
    },

    async created() {
      addWsHandler(this.handleWs);
      this.statusFilter = '';
      // not cached: the graph is redrawn from scratch on every load anyway
      this.loader = createPageLoader({
        fetch: async (signal) => {
          let url = 'experiments/' + this.$route.params.id + '/soh';
          if (this.statusFilter) {
            url = url + '?statusFilter=' + this.statusFilter;
          }
          return (await axiosInstance.get(url, { signal })).data;
        },
        apply: (state) => {
          this.applyNetwork(state);
          this.loaded = true;
          // the graph and chord containers render with the new data
          this.$nextTick(() => {
            // a Network Volume tab can appear and push the graph down
            this.fitGraphView();
            this.generateGraph();
            if (this.flows) this.generateChord();
          });
        },
      });
      await this.loader.start();
    },

    methods: {
      handleWs(msg) {
        switch (msg.resource.type) {
          case 'experiment': {
            if (msg.resource.name != this.$route.params.id) {
              return;
            }

            switch (msg.resource.action) {
              case 'stop':
              case 'start': {
                this.reload();
                break;
              }
            }

            break;
          }

          // the broker announces app runs as `apps/<app>`: start, then
          // success or error
          case 'apps/soh': {
            if (msg.resource.name != this.$route.params.id) {
              return;
            }

            switch (msg.resource.action) {
              case 'start': {
                this.sohRunning = true;
                break;
              }

              case 'success': {
                this.reload();
                this.sohRunning = false;
                break;
              }

              case 'error': {
                this.$buefy.toast.open({
                  message: 'Triggering State of Health update failed.',
                  type: 'is-danger',
                });

                this.sohRunning = false;
                break;
              }
            }

            break;
          }

          case 'experiment/vm': {
            // exp_name/vm_name
            let resource = msg.resource.name.split('/');
            let expName = resource[0];
            let vmName = resource[1];

            // Ignore this broadcast if it's not for this experiment, or
            // arrives before the graph does.
            if (expName != this.$route.params.id || !this.nodes) {
              return;
            }

            switch (msg.resource.action) {
              case 'stop': {
                for (let i = 0; i < this.nodes.length; i++) {
                  if (this.nodes[i].label == vmName) {
                    this.nodes[i].status = 'notrunning';
                    d3.selectAll('#graph circle').attr(
                      'fill',
                      this.updateNodeColor,
                    );
                  }
                }

                break;
              }
              case 'start': {
                for (let i = 0; i < this.nodes.length; i++) {
                  if (this.nodes[i].label == vmName) {
                    this.nodes[i].status = 'running';
                    d3.selectAll('#graph circle').attr(
                      'fill',
                      this.updateNodeColor,
                    );
                  }
                }

                break;
              }
              case 'delete': {
                for (let i = 0; i < this.nodes.length; i++) {
                  if (this.nodes[i].label == vmName) {
                    this.nodes[i].status = 'notdeploy';
                    d3.selectAll('#graph circle').attr(
                      'fill',
                      this.updateNodeColor,
                    );
                  }
                }

                break;
              }
            }
          }
        }
      },

      // reloads the network, then redraws the graph and chord diagram
      async updateNetwork(filter = '') {
        this.statusFilter = filter;
        await this.loader.load();
      },

      applyNetwork(state) {
        this.running = state.started;
        this.sohInitialized = state.soh_initialized;
        this.sohRunning = state.soh_running;

        this.nodes = state.nodes;
        this.edges = state.edges;

        this.flows = state.host_flows != null;
        this.volume = this.flows
          ? Object.assign(state.host_flows, { names: state.hosts })
          : [];

        if (this.nodes) {
          const detailsNode = this.nodes.find(
            (n) => n.label === this.detailsModal.vm,
          );
          if (detailsNode) {
            this.detailsModal.status = detailsNode.status;
            this.detailsModal.soh = detailsNode.soh;
            this.detailsModal.tags = detailsNode.tags;
          }
        }
      },

      updateNodeImage(node) {
        return 'url(#' + node.image.toLowerCase() + ')';
      },

      updateNodeBorder(node) {
        if (node.soh && node.soh.errors) {
          return SOH_ERROR_COLOR;
        }

        return this.updateNodeColor(node);
      },

      updateNodeColor(node) {
        return nodeFill(node, this.running);
      },

      // Lays out and draws the graph with the chosen layout. Layouts load on
      // demand and may take a moment, so a newer call (a reload, another
      // layout) supersedes one still computing.
      async generateGraph() {
        const gen = (this.graphGen = (this.graphGen ?? 0) + 1);
        if (this.nodes == null) {
          this.stopGraph();
          this.graph = null;
          return;
        }

        const { id, notice } = resolveLayout(this.layout, this.nodes.length);
        if (notice && notice !== this.layoutNotice) {
          this.$buefy.toast.open({
            message: notice,
            type: 'is-warning',
            duration: 6000,
          });
        }
        this.layoutNotice = notice;

        const input = layoutInput(this.nodes, this.edges ?? []);
        const slow = id !== DEFAULT_LAYOUT;
        this.layoutBusy = slow;
        let run;
        try {
          if (slow) {
            // let the spinner paint before a layout blocks the page
            await this.$nextTick();
            await new Promise((resolve) => setTimeout(resolve, 20));
            if (gen !== this.graphGen) return;
          }
          run = await runLayout(id, input, {
            onError: (err) => this.layoutFailed(err, id),
          });
        } catch (err) {
          useErrorNotification(err);
          return;
        } finally {
          if (gen === this.graphGen) this.layoutBusy = false;
        }
        if (gen !== this.graphGen || !document.querySelector('#graph')) return;
        this.drawGraph(run);
      },

      layoutFailed(err, id) {
        const label = findLayout(id)?.label ?? id;
        useErrorNotification(
          new Error(
            `The ${label} layout failed, so the graph is shown with the Force layout: ${err?.message ?? err}`,
          ),
        );
        this.layout = DEFAULT_LAYOUT;
      },

      stopGraph() {
        this.graph?.simulation?.stop();
        this.graph?.svg.interrupt('layout').interrupt('fit');
      },

      // Where each node is drawn now, so a new layout can move nodes from
      // there instead of jumping.
      currentPositions() {
        return new Map(
          (this.graph?.nodes ?? [])
            .filter((n) => Number.isFinite(n.x) && Number.isFinite(n.y))
            .map((n) => [n.id, { x: n.x, y: n.y }]),
        );
      },

      // The graph's drawing size: 400 units tall and as wide as the view's
      // shape, so the graph fills the view whatever the window's size.
      viewSize() {
        const el = document.querySelector('#graph');
        const height = 400;
        if (!el?.clientWidth || !el.clientHeight) return { width: 600, height };
        return {
          width: Math.round((height * el.clientWidth) / el.clientHeight),
          height,
        };
      },

      // Sizes the graph to the room left in the window, so the whole page
      // fits without scrolling, then stretches the drawing to match.
      fitGraphView() {
        const view = this.$refs.graphView;
        const room = roomInWindow(view);
        if (room == null) return;
        view.style.height = `${Math.max(Math.floor(room), 240)}px`;

        const graph = this.graph;
        if (!graph?.svg.node()) return;
        const { width, height } = this.viewSize();
        if (width === graph.width && height === graph.height) return;
        graph.width = width;
        graph.height = height;
        graph.svg.attr('viewBox', [0, 0, width, height]);
        graph.zoom.extent([
          [0, 0],
          [width, height],
        ]);
        // keep the graph in view unless the user has zoomed or panned it
        if (!graph.userMoved) this.fitGraph();
      },

      drawGraph({ id, mod, result }) {
        const { width, height } = this.viewSize();
        const previous = this.graph;
        const prevPositions = this.currentPositions();
        this.stopGraph();

        // copies inherit from the /soh nodes, so the status the websocket
        // updates on this.nodes shows through
        const nodes = this.nodes.map((d) => Object.create(d));
        const byId = new Map(nodes.map((n) => [n.id, n]));
        const links = (this.edges ?? [])
          .filter((e) => byId.has(e.source) && byId.has(e.target))
          .map((d) => Object.create(d));

        d3.select('#graph').select('svg').remove();

        const svg = d3
          .select('#graph')
          .append('svg')
          .attr('viewBox', [0, 0, width, height]);

        const g = svg.append('g');

        const zoom = d3
          .zoom()
          .extent([
            [0, 0],
            [width, height],
          ])
          .scaleExtent([0.05, 10])
          .on('zoom', ({ transform, sourceEvent }) => {
            g.attr('transform', transform);
            // the user zoomed or panned: leave the view where they put it
            if (sourceEvent && this.graph?.svg === svg) {
              this.graph.userMoved = true;
            }
          });
        svg.call(zoom);

        const groups = result?.groups ?? [];
        const groupSel = g
          .append('g')
          .attr('class', 'soh-groups')
          .selectAll('g')
          .data(groups)
          .join('g');
        groupSel
          .append('rect')
          .attr('rx', 6)
          .attr('fill', '#ffffff')
          .attr('fill-opacity', 0.05)
          .attr('stroke', '#9aa7b5')
          .attr('stroke-opacity', 0.55)
          .attr('stroke-width', 0.8)
          .attr('stroke-dasharray', '3 3');
        groupSel
          .filter((d) => d.label)
          .append('text')
          .text((d) => d.label)
          .style('fill', '#dfe6ee')
          .style('font-size', '7px')
          .style('font-weight', '600');

        // edges from hosts packed in a VLAN block are drawn faintly: the
        // block already shows the VLAN
        const faint = result?.faintEdges ?? new Set();
        const link = g
          .append('g')
          .attr('stroke', '#999')
          .attr('stroke-opacity', 0.6)
          .selectAll('line')
          .data(links)
          .join('line')
          .attr('stroke-width', 1)
          .attr('stroke-opacity', (d) => (faint.has(d.id) ? 0.2 : null));

        const defs = svg.append('svg:defs');
        const patterns = [
          ['linux', Linux, 30],
          ['centos', CentOS, 30],
          ['rhel', RedHat, 30],
          ['windows', Windows, 30],
          ['router', Router, 30],
          ['firewall', Firewall, 30],
          ['switch', VLAN, 10],
        ];
        for (const [pid, href, size] of patterns) {
          defs
            .append('svg:pattern')
            .attr('id', pid)
            .attr('width', 50)
            .attr('height', 50)
            .append('svg:image')
            .attr('xlink:href', href)
            .attr('width', size)
            .attr('height', size)
            .attr('x', 0)
            .attr('y', 0);
        }

        const node = g
          .append('g')
          .selectAll('circle')
          .data(nodes)
          .join('circle')
          .attr('class', 'circle')
          .attr('stroke', this.updateNodeBorder)
          .attr('stroke-width', 1.5)
          .attr('r', 5)
          .attr('fill', this.updateNodeColor)
          .attr('width', 5)
          .attr('height', 5)
          .attr('style', (n) =>
            n.image.toLowerCase() == 'switch' || !n.tags
              ? ''
              : n.tags[SOH_STYLE_LABEL_KEY] || '',
          )
          .on('mouseenter', this.entered)
          .on('mouseleave', this.exited)
          .on('click', this.clicked);

        const label = g
          .append('g')
          .selectAll('text')
          .data(nodes)
          .join('text')
          .text((d) => d.label)
          .style('text-anchor', 'start')
          .style('fill', 'whitesmoke')
          .style('font-size', '6px');

        const render = () => {
          link
            .attr('x1', (d) => d.source.x)
            .attr('y1', (d) => d.source.y)
            .attr('x2', (d) => d.target.x)
            .attr('y2', (d) => d.target.y);

          node.attr('cx', (d) => d.x).attr('cy', (d) => d.y);

          label.attr('x', (d) => d.x + 4).attr('y', (d) => d.y + 8);

          groupSel.each(function (grp) {
            const b = boundsOf(
              grp.members.map((m) => byId.get(m)),
              (grp.padding ?? 6) + 5,
            );
            if (!b) return;
            const sel = d3.select(this);
            sel
              .select('rect')
              .attr('x', b.x0)
              .attr('y', b.y0)
              .attr('width', b.x1 - b.x0)
              .attr('height', b.y1 - b.y0);
            sel
              .select('text')
              .attr('x', b.x0 + 3)
              .attr('y', b.y0 - 3);
          });
        };

        // kept off data(): d3 objects must not become reactive
        this.graph = {
          layoutId: id,
          svg,
          zoom,
          nodes,
          simulation: null,
          width,
          height,
        };

        if (mod.kind === 'simulation') {
          const simulation = mod.createSimulation(nodes, links, {
            width,
            height,
          });
          this.graph.simulation = simulation;
          node.call(this.drag(simulation));
          simulation.on('tick', render);
          // same layout on a reload: keep the user's zoom and pan
          if (previous?.layoutId === id && previous.svg.node()) {
            svg.call(zoom.transform, d3.zoomTransform(previous.svg.node()));
          } else {
            // fit the graph in view once it has mostly settled, unless the
            // user has zoomed or panned it by then
            const fitOnce = () => {
              if (simulation.alpha() > 0.1) return;
              simulation.on('tick.fit', null).on('end.fit', null);
              if (
                this.graph?.simulation === simulation &&
                !this.graph.userMoved
              ) {
                this.fitGraph(true);
              }
            };
            simulation.on('tick.fit', fitOnce).on('end.fit', fitOnce);
          }
          return;
        }

        // static layout: move nodes from where they were to their new
        // places, then fit the graph in view
        for (const l of links) {
          l.source = byId.get(l.source);
          l.target = byId.get(l.target);
        }
        const targets = result.positions;
        const known = boundsOf(targets.values());
        const cx = known ? (known.x0 + known.x1) / 2 : width / 2;
        const cy = known ? (known.y0 + known.y1) / 2 : height / 2;
        let moves = false;
        for (const n of nodes) {
          const t = targets.get(n.id) ?? { x: cx, y: cy };
          const p = prevPositions.get(n.id) ?? t;
          n.tx = t.x;
          n.ty = t.y;
          n.x = n.sx = p.x;
          n.y = n.sy = p.y;
          moves ||= p.x !== t.x || p.y !== t.y;
        }

        const settle = () => {
          for (const n of nodes) {
            n.x = n.tx;
            n.y = n.ty;
          }
        };
        node.call(
          d3
            .drag()
            .on('start', () => {
              // a drag during the move finishes it first
              if (this.graph?.moving) {
                svg.interrupt('layout');
                settle();
                render();
              }
            })
            .on('drag', (event) => {
              event.subject.x = event.x;
              event.subject.y = event.y;
              render();
            }),
        );

        render();
        const sameLayout =
          previous?.layoutId === id && previous.svg.node() && !moves;
        if (sameLayout) {
          // a reload of the same layout keeps the user's zoom and pan
          svg.call(zoom.transform, d3.zoomTransform(previous.svg.node()));
          return;
        }
        if (previous?.svg.node()) {
          svg.call(zoom.transform, d3.zoomTransform(previous.svg.node()));
        }
        if (moves) {
          this.graph.moving = true;
          svg
            .transition('layout')
            .duration(650)
            .ease(d3.easeCubicInOut)
            .tween('layout', () => (t) => {
              for (const n of nodes) {
                n.x = n.sx + (n.tx - n.sx) * t;
                n.y = n.sy + (n.ty - n.sy) * t;
              }
              render();
            })
            .on('end interrupt', () => {
              if (this.graph) this.graph.moving = false;
            });
        } else {
          settle();
          render();
        }
        this.fitGraph(
          Boolean(previous),
          nodes.map((n) => ({ x: n.tx, y: n.ty })),
        );
      },

      // Zooms so the whole graph fits the view. points defaults to where the
      // nodes are now; a static layout passes where they are going.
      fitGraph(animate = false, points) {
        const graph = this.graph;
        if (!graph) return;
        const pts = points ?? graph.nodes;
        const b = boundsOf(pts, 16);
        if (!b) return;
        // room for labels, which sit right of and below their node
        b.x1 += 30;
        b.y1 += 6;
        const { width, height } = graph;
        const k = Math.min(
          4,
          0.95 * Math.min(width / (b.x1 - b.x0), height / (b.y1 - b.y0)),
        );
        const t = d3.zoomIdentity
          .translate(
            width / 2 - (k * (b.x0 + b.x1)) / 2,
            height / 2 - (k * (b.y0 + b.y1)) / 2,
          )
          .scale(k);
        if (animate) {
          graph.svg
            .transition('fit')
            .duration(650)
            .ease(d3.easeCubicInOut)
            .call(graph.zoom.transform, t);
        } else {
          graph.svg.call(graph.zoom.transform, t);
        }
      },

      entered(e, n) {
        if (!n.image || n.image.toLowerCase() == 'switch') {
          return;
        }

        let circle = d3.select(e.target);

        circle
          .transition()
          .attr('r', 15)
          .attr('fill', () => this.updateNodeImage(n));
      },

      exited(e, n) {
        let circle = d3.select(e.target);

        circle
          .transition()
          .attr('r', 5)
          .attr('fill', () => this.updateNodeColor(n));
      },

      clicked(_, n) {
        if (n.image.toLowerCase() == 'switch') {
          return;
        }

        // a node that has not booted has no state of health, which the
        // dialog says
        this.detailsModal.active = true;
        this.detailsModal.vm = n.label;
        this.detailsModal.status = n.status;
        this.detailsModal.soh =
          n.status.toLowerCase() == 'notboot' ? null : n.soh;
        this.detailsModal.tags = n.tags ?? {};
      },

      drag(simulation) {
        function dragstarted(event) {
          if (!event.active) simulation.alphaTarget(0.3).restart();
          event.subject.fx = event.subject.x;
          event.subject.fy = event.subject.y;
        }

        function dragged(event) {
          event.subject.fx = event.x;
          event.subject.fy = event.y;
        }

        function dragended(event) {
          if (!event.active) simulation.alphaTarget(0);
          event.subject.fx = null;
          event.subject.fy = null;
        }

        return d3
          .drag()
          .on('start', dragstarted)
          .on('drag', dragged)
          .on('end', dragended);
      },

      generateChord() {
        const names =
          this.volume.names === undefined
            ? d3.range(this.volume.length)
            : this.volume.names;

        const tickStep = d3.tickStep(0, d3.sum(this.volume.flat()), 100);
        const formatValue = d3.format('.1~%');

        const height = 400;
        const width = 600;

        const innerRadius = Math.min(width, height) * 0.35;
        const outerRadius = innerRadius * 1.018;

        const chord = d3
          .chord()
          .padAngle(10 / innerRadius)
          .sortSubgroups(d3.descending)
          .sortChords(d3.descending);

        const arc = d3.arc().innerRadius(innerRadius).outerRadius(outerRadius);

        const ribbon = d3
          .ribbon()
          .radius(innerRadius - 1)
          .padAngle(1 / innerRadius);

        const color = d3.scaleOrdinal(d3.schemeCategory10);

        function ticks(startAngle, endAngle, value) {
          const k = (endAngle - startAngle) / value;
          return d3.range(0, value, tickStep).map((value) => {
            return { value, angle: value * k + startAngle };
          });
        }

        d3.select('#chord').select('svg').remove();

        const svg = d3
          .select('#chord')
          .append('svg')
          .attr('viewBox', [-width / 2, -height / 2, width, height]);

        const chords = chord(this.volume);

        const group = svg
          .append('g')
          .attr('font-size', 10)
          .attr('font-family', 'sans-serif')
          .selectAll('g')
          .data(chords.groups)
          .join('g');

        group
          .append('path')
          .attr('fill', (d) => color(names[d.index]))
          .attr('stroke', (d) => color(names[d.index]))
          .attr('d', arc);

        group
          .append('text')
          .each((d) => {
            d.angle = (d.startAngle + d.endAngle) / 2;
          })
          .attr('dy', '.35em')
          .attr(
            'transform',
            (d) => `
          rotate(${(d.angle * 180) / Math.PI - 90})
          translate(${innerRadius + 18})
          ${d.angle > Math.PI ? 'rotate(180)' : ''}`,
          )
          .attr('text-anchor', (d) => (d.angle > Math.PI ? 'end' : null))
          .text(
            (d) => `${names[d.index]}
          ${formatValue(d.value)}`,
          );

        const groupTick = group
          .append('g')
          .selectAll('g')
          .data(ticks)
          .join('g')
          .attr(
            'transform',
            (d) =>
              `rotate(${(d.angle * 180) / Math.PI - 90}) translate(${outerRadius},0)`,
          );

        groupTick.append('line').attr('stroke', 'currentColor').attr('x2', 6);

        groupTick
          .append('text')
          .attr('x', 8)
          .attr('dy', '0.35em')
          .attr('fill', 'whitesmoke')
          .attr('transform', (d) =>
            d.angle > Math.PI ? 'rotate(180) translate(-16)' : null,
          )
          .attr('text-anchor', (d) => (d.angle > Math.PI ? 'end' : null))
          .text((d) => formatValue(d.value));

        group
          .select('text')
          .attr('font-weight', 'bold')
          .attr('fill', 'whitesmoke')
          .text(function (d) {
            return this.getAttribute('text-anchor') === 'end'
              ? `↑ ${names[d.index]}`
              : `${names[d.index]} ↓`;
          });

        svg
          .append('g')
          .attr('fill-opacity', 0.8)
          .selectAll('path')
          .data(chords)
          .join('path')
          .style('mix-blend-mode', 'multiply')
          .attr('stroke', (d) => d3.rgb(color(names[d.source.index])).darker()) // might want to drop
          .attr('fill', (d) => color(names[d.source.index]))
          .attr('d', ribbon)
          .append('title')
          .text(
            (d) =>
              `${names[d.target.index]} \t➔\t ${names[d.source.index]} \t ${d3.format('.5s')(d.source.value)}${d.source.index === d.target.index ? '' : `\n${names[d.source.index]} \t➔\t ${names[d.target.index]} \t ${d3.format('.5s')(d.target.value)}`}`,
          );
      },

      // Downloads the graph as drawn: PNG, SVG or GEXF.
      async download(format) {
        const graph = this.graph;
        const svgEl = graph?.svg.node();
        if (!svgEl) return;
        const exp = this.$route.params.id;
        const layout = findLayout(graph.layoutId);
        const now = new Date();
        const filter = this.filterLabel;
        this.exporting = true;
        try {
          // loaded on first use, so the page (and the idle prefetch of it)
          // doesn't carry the exporters
          const [{ exportFileName, exportSvg, svgToPng }, { buildGexf }] =
            await Promise.all([
              import('@/utils/soh/exportImage.js'),
              import('@/utils/soh/gexf.js'),
            ]);
          const name = exportFileName(exp, graph.layoutId, format, now);
          if (format === 'gexf') {
            const xml = buildGexf({
              experiment: exp,
              nodes: this.nodes ?? [],
              edges: this.edges ?? [],
              positions: this.currentPositions(),
              running: this.running,
              vms: await this.fetchVms(),
              hosts: this.flows ? this.volume.names : null,
              flows: this.flows ? this.volume : null,
              layout: layout?.label,
              filter,
              now,
            });
            FileSaver.saveAs(
              new Blob([xml], { type: 'application/xml;charset=utf-8' }),
              name,
            );
            return;
          }
          const t = d3.zoomTransform(svgEl);
          const out = await exportSvg(svgEl, {
            transform: { k: t.k, x: t.x, y: t.y },
            title: `State of Health: ${exp}`,
            subtitle:
              `${plural(this.vmCount, 'VM')} · ${plural(this.vlanCount, 'VLAN')} · ` +
              `filter: ${filter} · ` +
              `layout: ${layout?.label ?? graph.layoutId} · ${now.toLocaleString()}`,
          });
          const blob =
            format === 'svg'
              ? new Blob([out.svg], { type: 'image/svg+xml;charset=utf-8' })
              : await svgToPng(out.svg, out.width, out.height, 2);
          FileSaver.saveAs(blob, name);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.exporting = false;
        }
      },

      // The experiment's VMs (host, addresses, CPUs, memory, disk) for the
      // GEXF export; null when the user may not list them.
      async fetchVms() {
        try {
          const url = 'experiments/' + this.$route.params.id + '/vms';
          return (await axiosInstance.get(url)).data?.vms ?? null;
        } catch {
          return null;
        }
      },

      // reloads the network, keeping the chosen filter
      reload() {
        return this.loader.load();
      },

      resetDetailsModal() {
        this.detailsModal = {
          active: false,
          vm: '',
          status: '',
          soh: null,
          tags: {},
        };
      },

      execSoH() {
        let url = 'experiments/' + this.$route.params.id + '/trigger?apps=soh';

        axiosInstance.post(url).catch((err) => {
          useErrorNotification(err);
        });
      },

      vncLoc(vm) {
        const store = usePhenixStore();
        return this.$router.resolve({
          name: 'vnc',
          params: {
            id: this.$route.params.id,
            name: vm,
            token: store.token,
          },
        }).href;
      },

      showTagsModal(vm, tags) {
        const self = this;
        this.$buefy.modal.open({
          component: VmLabelsModal,
          trapFocus: true,
          hasModalCard: true,
          props: {
            vmName: vm,
            experiment: this.$route.params.id,
            tags: tags,
          },
          events: {
            saved() {
              self.reload();
            },
          },
        });
      },

      // STYLE MODAL
      showStyleModal(vm) {
        const styleNode = this.nodes.find(
          (n) => n.label === this.detailsModal.vm,
        );
        this.styleModal.vm = vm;
        this.styleModal.baseFill = this.updateNodeColor(styleNode);
        this.styleModal.baseStroke = this.updateNodeBorder(styleNode);
        this.styleModal.fill = this.updateNodeColor(styleNode);
        this.styleModal.stroke = this.updateNodeBorder(styleNode);
        this.styleModal.strokeStyle = 'solid';

        // parse css in label back to values
        let tempElem = document.createElement('div');
        tempElem.style = styleNode.tags[SOH_STYLE_LABEL_KEY] || '';
        if (tempElem.style['fill'] !== '') {
          this.styleModal.overrideFill = true;
          this.styleModal.fill = tempElem.style['fill'];
        }
        if (tempElem.style['stroke'] !== '') {
          this.styleModal.overrideStroke = true;
          this.styleModal.stroke = tempElem.style['stroke'];
        }
        if (tempElem.style['stroke-dasharray'] !== '') {
          this.styleModal.overrideStrokeStyle = true;
          let dashVal = tempElem.style['stroke-dasharray'];
          if (dashVal === '0') {
            this.styleModal.strokeStyle = 'solid';
          } else if (dashVal.startsWith('0,')) {
            this.styleModal.strokeStyle = 'dotted';
          } else {
            this.styleModal.strokeStyle = 'dashed';
          }
        }

        this.styleModal.active = true;
      },

      saveStyle() {
        let update = {
          tag_update_mode: 'ADD',
          tags: { [SOH_STYLE_LABEL_KEY]: this.styleModalCustomStyle },
        };

        axiosInstance
          .patch(
            'experiments/' +
              this.$route.params.id +
              '/vms/' +
              this.styleModal.vm,
            update,
          )
          .then(
            (_) => {
              this.resetStyleModal();
              this.reload();
            },
            (err) => useErrorNotification(err),
          );
      },

      resetStyleModal() {
        this.styleModal = {
          active: false,
          vm: '',
          baseFill: '',
          baseStroke: '',
          overrideFill: false,
          fill: '',
          overrideStroke: false,
          stroke: '',
          overrideStrokeStyle: false,
          strokeStyle: '',
        };
      },
    },

    watch: {
      radioButton(filter) {
        this.updateNetwork(filter === 'all' ? '' : filter);
      },

      layout(id) {
        saveLayoutPref(id);
        if (this.loaded) this.generateGraph();
      },
    },

    data() {
      return {
        running: false,
        sohInitialized: false,
        sohRunning: false,
        flows: false,
        nodes: [],
        edges: [],
        volume: [],
        radioButton: 'all',
        loaded: false, // false until the first load arrives
        layout: readLayoutPref(), // the chosen layout, kept across visits
        layoutBusy: false, // a layout is loading or computing
        exporting: false,
        vlan: VLAN,
        detailsModal: {
          active: false,
          vm: '',
          status: '',
          soh: null,
          tags: {},
        },
        styleModal: {
          active: false,
          vm: '',
          baseFill: '',
          baseStroke: '',
          overrideFill: false,
          fill: '',
          overrideStroke: false,
          stroke: '',
          overrideStrokeStyle: false,
          strokeStyle: '',
        },
      };
    },
    computed: {
      // VMs (not VLAN segments) that the current filter shows
      vmCount() {
        return countVms(this.nodes);
      },

      vlanCount() {
        return countVlans(this.nodes);
      },

      // the node colors, stacked beside the graph
      legend() {
        return Object.entries(STATUS_COLORS).map(([status, color]) => ({
          label: STATUS_LABELS[status],
          color,
        }));
      },

      layoutChoices() {
        return layoutOptions(this.nodes?.length ?? 0);
      },

      filterLabel() {
        return this.radioButton === 'all'
          ? 'all'
          : (STATUS_LABELS[this.radioButton] ?? this.radioButton);
      },

      // what the disabled SOH button says
      sohState() {
        if (!this.loaded) return 'Loading…';
        if (!this.running) return 'Exp Not Running';
        if (this.sohRunning) {
          return this.sohInitialized ? 'SOH Is Running' : 'SOH Is Initializing';
        }
        return 'SOH Not Initialized';
      },

      styleModalCustomStyle: function () {
        var css = '';
        if (this.styleModal.overrideFill)
          css += `fill: ${this.styleModal.fill}; `;
        if (this.styleModal.overrideStroke)
          css += `stroke: ${this.styleModal.stroke}; `;
        if (this.styleModal.overrideStrokeStyle) {
          if (this.styleModal.strokeStyle == 'solid')
            css += `stroke-dasharray: 0; `;
          // these numbers are based on the circles diameter pi * 10
          else if (this.styleModal.strokeStyle == 'dashed')
            css += `stroke-dasharray: 3 2.236; `;
          else if (this.styleModal.strokeStyle == 'dotted')
            css += `stroke-dasharray: 0 2.094; stroke-linecap: round; `;
        }
        return css;
      },
    },
  };
</script>

<style scoped>
  label.radio:hover {
    color: whitesmoke;
  }

  /* legend left of the graph; counts, filters and links above it */
  .soh-topology {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    grid-template-areas:
      '. head'
      'legend graph';
    gap: 10px 1rem;
  }

  /* counts on the graph's left edge, links on its right */
  .soh-graph-head {
    grid-area: head;
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: center;
    gap: 1rem;
  }

  .soh-controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: center;
    gap: 0.5rem 1rem;
  }

  .soh-controls :deep(.radio) {
    white-space: nowrap;
    margin: 0;
  }

  .soh-counts,
  .soh-links {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  /* how many VMs and VLANs the filter shows */
  .soh-count {
    display: inline-flex;
    align-items: baseline;
    gap: 0.3em;
    padding: 0.3em 0.75em;
    border: 1px solid #828282;
    border-radius: 4px;
    background: #484848;
    color: #e8e8e8;
    white-space: nowrap;
    font-size: 0.95rem;
  }

  .soh-count b {
    color: white;
    font-size: 1.1rem;
    font-variant-numeric: tabular-nums;
  }

  .soh-legend {
    grid-area: legend;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    padding-top: 0.5rem;
    color: whitesmoke;
    white-space: nowrap;
  }

  .soh-legend li {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .soh-legend img {
    width: 20px;
    height: 20px;
  }

  .soh-graph-box {
    grid-area: graph;
    border: 2px solid whitesmoke;
    background: #333;
  }

  .soh-graph-view {
    height: 600px;
  }

  .soh-graph-view #graph,
  .soh-graph-view #graph :deep(svg) {
    display: block;
    width: 100%;
    height: 100%;
  }

  .soh-graph-empty {
    display: flex;
    height: 100%;
    align-items: center;
    justify-content: center;
    padding: 1rem;
    background: whitesmoke;
    text-align: center;
  }

  /* layout picker, fit and download, above the graph */
  .soh-graph-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 1rem;
    padding: 0.45rem 0.6rem;
    background: #484848;
    border-bottom: 1px solid #6e6e6e;
  }

  .soh-graph-bar-end {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .soh-graph-bar-end :deep(.button) {
    font-size: 0.85rem;
    height: 2.1em;
  }

  .single-tab :deep(.tabs) {
    display: none;
  }

  .modal-card-head {
    background-color: #686868;
  }

  .modal-card-title {
    color: whitesmoke;
  }

  li::marker {
    color: black;
  }

  ul {
    column-count: 1;
  }
</style>
