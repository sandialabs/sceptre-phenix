<template>
  <div class="content">
    <b-modal
      v-model="createModal.active"
      :on-cancel="resetCreateModal"
      has-modal-card>
      <div class="modal-card" style="width: 25em">
        <header class="modal-card-head">
          <p class="modal-card-title">Create a New Experiment</p>
        </header>
        <section class="modal-card-body">
          <b-field
            label="Experiment Name"
            :type="createModal.nameErrType"
            :message="createModal.nameErrMsg"
            autofocus>
            <b-input type="text" v-model="createModal.name" v-focus></b-input>
          </b-field>
          <b-field label="Experiment Topology">
            <b-select
              placeholder="Select a topology"
              v-model="createModal.topology"
              @input="getScenarios"
              expanded>
              <option v-for="(t, index) in topologies" :key="index" :value="t">
                {{ t }}
              </option>
            </b-select>
          </b-field>
          <b-tooltip
            label="a scenario is a collection of user app configurations for a topology 
                           and they are optional"
            type="is-light is-right"
            multilined>
            <b-field
              v-if="createModal.showScenarios"
              label="Experiment Scenario"></b-field>
            <b-icon
              v-if="createModal.showScenarios"
              icon="question-circle"
              style="color: #383838"></b-icon>
          </b-tooltip>
          <b-select
            v-if="createModal.showScenarios"
            v-model="createModal.scenario"
            expanded
            placeholder="None">
            <option v-for="(a, s) in createModal.scenarios" :key="s" :value="s">
              {{ s }}
            </option>
          </b-select>
          <br />
          <b-taglist>
            <b-tag
              v-for="(a, index) in createModal.scenarios[createModal.scenario]"
              :key="index"
              type="is-light"
              :class="{ 'is-success': !a.disabled }"
              @click="clickScenario(index)">
              {{ a.name }}
            </b-tag>
          </b-taglist>
          <b-collapse class="card" animation="slide" :open="false">
            <template #trigger="props">
              <div class="card-header" role="button">
                <p class="card-header-title">Options</p>
                <a class="card-header-icon">
                  <b-icon
                    size="is-small"
                    :icon="props.open ? 'chevron-down' : 'chevron-up'"></b-icon>
                </a>
              </div>
            </template>
            <div class="card-content">
              <div class="content">
                <b-field label="Deployment Mode">
                  <b-select v-model="createModal.deploy_mode" expanded>
                    <option
                      v-for="(mode, index) in [
                        '',
                        'no-headnode',
                        'only-headnode',
                        'all',
                      ]"
                      :key="index"
                      :value="mode">
                      {{ mode }}
                    </option>
                  </b-select>
                </b-field>
                <b-field
                  v-if="bridgeMode != 'auto'"
                  label="Default Bridge Name"
                  :type="createModal.bridgeErrType"
                  :message="createModal.bridgeErrMsg">
                  <b-input type="text" v-model="createModal.bridge" />
                </b-field>
                <b-field label="VLAN Range">
                  <b-field>
                    <b-numberinput
                      min="0"
                      max="4094"
                      type="is-light"
                      size="is-small"
                      controls-alignment="right"
                      controls-position="compact"
                      placeholder="min"
                      v-model="createModal.vlan_min" />
                    &nbsp;
                    <b-numberinput
                      min="0"
                      max="4094"
                      type="is-light"
                      size="is-small"
                      controls-alignment="right"
                      controls-position="compact"
                      placeholder="max"
                      v-model="createModal.vlan_max" />
                  </b-field>
                </b-field>
                <b-field label="Git Workflow Branch Name">
                  <b-input type="text" v-model="createModal.branch" />
                </b-field>
              </div>
            </div>
          </b-collapse>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button
            class="button is-light"
            :disabled="!validate()"
            @click="create">
            Create Experiment
          </button>
        </footer>
      </div>
    </b-modal>
    <template v-if="experiments.length == 0">
      <section class="hero is-bold is-large">
        <div class="hero-body">
          <div class="container" style="text-align: center">
            <h1 class="title">There are no experiments!</h1>
            <b-button
              v-if="roleAllowed('experiments', 'create')"
              type="is-success"
              outlined
              @click="
                updateTopologies();
                createModal.active = true;
              "
              >Create One Now!</b-button
            >
          </div>
        </div>
      </section>
    </template>
    <template v-else>
      <hr />
      <b-field position="is-right" grouped>
        <b-field>
          <b-autocomplete
            v-model="searchName"
            placeholder="Find an Experiment"
            icon="search"
            :data="filteredData"
            @select="(option) => (filtered = option)">
            <template #empty> No results found </template>
          </b-autocomplete>
          <p class="control">
            <button class="button input-button" @click="searchName = ''">
              <b-icon icon="window-close"></b-icon>
            </button>
          </p>
        </b-field>
        <p v-if="roleAllowed('experiments', 'create')" class="control">
          <b-tooltip label="create a new experiment" type="is-light" multilined>
            <button
              class="button is-light"
              @click="
                updateTopologies();
                createModal.active = true;
              ">
              <b-icon icon="plus"></b-icon>
            </button>
          </b-tooltip>
        </p>
      </b-field>
      <div>
        <b-table
          :data="filteredExperiments"
          :paginated="table.isPaginated"
          :per-page="table.perPage"
          :current-page.sync="table.currentPage"
          :pagination-simple="table.isPaginationSimple"
          :pagination-size="table.paginationSize"
          :default-sort-direction="table.defaultSortDirection"
          default-sort="name">
          <template #empty>
            <section class="section">
              <div class="content has-text-white has-text-centered">
                Your search turned up empty!
              </div>
            </section>
          </template>
          <b-table-column
            field="name"
            label="Name"
            width="200"
            sortable
            v-slot="props">
            <template v-if="updating(props.row.status)">
              {{ props.row.name }}
            </template>
            <template v-else>
              <router-link
                class="navbar-item"
                :to="{
                  name: 'experiment',
                  params: { id: props.row.name },
                  state: { running: props.row.running },
                }">
                {{ props.row.name }}
              </router-link>
            </template>
          </b-table-column>
          <b-table-column
            field="status"
            label="Status"
            width="100"
            sortable
            centered
            v-slot="props">
            <template v-if="props.row.status == 'starting'">
              <section>
                <b-progress
                  size="is-medium"
                  type="is-warning"
                  show-value
                  :value="props.row.percent"
                  format="percent"></b-progress>
              </section>
            </template>
            <template
              v-else-if="roleAllowed('experiments', 'update', props.row.name)">
              <b-tooltip
                :label="getExpControlLabel(props.row.name, props.row.status)"
                type="is-dark">
                <span
                  class="tag is-medium"
                  :class="decorator(props.row.status)">
                  <div class="field">
                    <div
                      class="field"
                      @click="
                        props.row.running
                          ? stop(props.row.name, props.row.status)
                          : start(props.row.name, props.row.status)
                      ">
                      {{ props.row.status }}
                    </div>
                  </div>
                </span>
              </b-tooltip>
            </template>
            <template v-else>
              <span class="tag is-medium" :class="decorator(props.row.status)">
                {{ props.row.status }}
              </span>
            </template>
          </b-table-column>
          <b-table-column
            field="topology"
            label="Topology"
            width="200"
            v-slot="props">
            {{ formatLowercase(props.row.topology) }}
          </b-table-column>
          <b-table-column
            field="scenario"
            label="Scenario"
            width="200"
            v-slot="props">
            {{ formatLowercase(props.row.scenario) }}
          </b-table-column>
          <b-table-column
            field="start_time"
            label="Start Time"
            width="250"
            sortable
            v-slot="props">
            {{ props.row.start_time }}
          </b-table-column>
          <b-table-column
            field="vm_count"
            label="VMs"
            width="100"
            centered
            sortable
            v-slot="props">
            {{ props.row.vm_count }}
          </b-table-column>
          <b-table-column
            field="vlan_range"
            label="VLANs"
            width="100"
            centered
            v-slot="props">
            {{ props.row.vlan_min }} - {{ props.row.vlan_max }} ({{
              props.row.vlan_count
            }})
          </b-table-column>
          <b-table-column label="Actions" width="150" centered v-slot="props">
            <button
              v-if="roleAllowed('experiments', 'delete', props.row.name)"
              class="button is-light is-small action"
              :disabled="updating(props.row.status)"
              @click="del(props.row.name, props.row.running)">
              <b-icon icon="trash"></b-icon>
            </button>
            <router-link
              v-if="roleAllowed('experiments', 'get', props.row.name)"
              class="button is-light is-small action"
              :disabled="updating(props.row.status)"
              :to="{ name: 'soh', params: { id: props.row.name } }">
              <b-icon icon="heartbeat"></b-icon>
            </router-link>
            <router-link
              v-if="roleAllowed('experiments', 'get', props.row.name)"
              class="button is-light is-small action"
              :disabled="updating(props.row.status)"
              :to="{ name: 'scorchruns', params: { id: props.row.name } }">
              <b-icon icon="fire"></b-icon>
            </router-link>
          </b-table-column>
        </b-table>
        <br />
        <b-field v-if="paginationNeeded" grouped position="is-right">
          <div class="control is-flex">
            <b-switch
              v-model="table.isPaginated"
              size="is-small"
              type="is-light"
              @input="changePaginate()"
              >Paginate</b-switch
            >
          </div>
        </b-field>
      </div>
    </template>
    <b-loading
      :is-full-page="true"
      v-model="isWaiting"
      :can-cancel="false"></b-loading>
  </div>
</template>

<script setup>
  import { roleAllowed } from '@/utils/rbac.js';
  import { useErrorNotification } from '@/utils/errorNotif';
</script>

<script>
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import axiosInstance from '@/utils/axios.js';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';

  export default {
    mixins: [formattingMixin],

    async beforeUnmount() {
      removeWsHandler(this.handleWs);
    },

    async created() {
      addWsHandler(this.handleWs);
      this.updateExperiments();
      axiosInstance
        .get('/options')
        .then((resp) => {
          this.options = resp.data;
        })
        .catch((err) => {
          console.log(err);
        });
    },

    computed: {
      filteredExperiments: function () {
        let experiments = this.experiments;

        var name_re = new RegExp(this.searchName, 'i');
        var data = [];

        for (let i in experiments) {
          let exp = experiments[i];
          if (exp.name.match(name_re)) {
            exp.start_time = exp.start_time == '' ? 'N/A' : exp.start_time;
            data.push(exp);
          }
        }

        return data;
      },

      filteredData() {
        let names = this.experiments.map((exp) => {
          return exp.name;
        });

        return names.filter((option) => {
          return (
            option
              .toString()
              .toLowerCase()
              .indexOf(this.searchName.toLowerCase()) >= 0
          );
        });
      },

      paginationNeeded() {
        var user = localStorage.getItem('user');

        if (localStorage.getItem(user + '.lastPaginate')) {
          this.table.isPaginated =
            localStorage.getItem(user + '.lastPaginate') == 'true';
        }

        if (this.experiments.length <= 10) {
          this.table.isPaginated = false;
          return false;
        } else {
          return true;
        }
      },

      bridgeMode() {
        return this.options['bridge-mode'];
      },
    },

    methods: {
      handleWs(msg) {
        // We only care about publishes pertaining to an experiment resource.
        if (msg.resource.type != 'experiment') {
          return;
        }

        let exp = this.experiments;

        switch (msg.resource.action) {
          case 'create': {
            msg.result.status = 'stopped';
            exp.push(msg.result);

            this.experiments = [...exp];

            this.$buefy.toast.open({
              message:
                'The ' + msg.resource.name + ' experiment has been created.',
              type: 'is-success',
              duration: 4000,
            });

            break;
          }

          case 'delete': {
            for (let i = 0; i < exp.length; i++) {
              if (exp[i].name == msg.resource.name) {
                exp.splice(i, 1);

                break;
              }
            }

            this.experiments = [...exp];

            this.$buefy.toast.open({
              message:
                'The ' + msg.resource.name + ' experiment has been deleted.',
              type: 'is-success',
              duration: 4000,
            });

            break;
          }

          case 'start': {
            for (let i = 0; i < exp.length; i++) {
              if (exp[i].name == msg.resource.name) {
                exp[i] = msg.result;
                exp[i].status = 'started';

                break;
              }
            }

            this.experiments = [...exp];

            let toast = `The ${msg.resource.name} experiment has been started`;

            if (msg.resource.delayed_vms > 0) {
              toast = `${toast} (with ${msg.resource.delayed_vms} delayed VMs).`;
            } else {
              toast = `${toast}.`;
            }

            this.$buefy.toast.open({
              message: toast,
              type: 'is-success',
              duration: 4000,
            });

            break;
          }

          case 'stop': {
            for (let i = 0; i < exp.length; i++) {
              if (exp[i].name == msg.resource.name) {
                exp[i] = msg.result;
                exp[i].status = 'stopped';

                break;
              }
            }

            this.experiments = [...exp];

            this.$buefy.toast.open({
              message:
                'The ' + msg.resource.name + ' experiment has been stopped.',
              type: 'is-success',
              duration: 4000,
            });

            break;
          }

          case 'starting': // fallthru to `stopping`
          case 'stopping': {
            for (let i = 0; i < exp.length; i++) {
              if (exp[i].name == msg.resource.name) {
                exp[i].status = msg.resource.action;
                exp[i].percent = 0;

                break;
              }
            }

            this.experiments = [...exp];

            this.$buefy.toast.open({
              message:
                'The ' + msg.resource.name + ' experiment is being updated.',
              type: 'is-warning',
            });

            break;
          }

          case 'progress': {
            let percent = (msg.result.percent * 100).toFixed(0);

            for (let i = 0; i < exp.length; i++) {
              if (exp[i].name == msg.resource.name) {
                exp[i].percent = parseInt(percent);
                break;
              }
            }

            this.experiments = [...exp];

            break;
          }
        }
      },

      updateExperiments() {
        axiosInstance
          .get('experiments')
          .then((response) => {
            this.experiments = response.data.experiments;
            this.isWaiting = false;
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },

      updateTopologies() {
        axiosInstance
          .get('topologies')
          .then((response) => {
            const state = response.data;
            this.topologies = state.topologies;
            this.isWaiting = false;
          })
          .catch((err) => {
            this.isWaiting = false;
            useErrorNotification(err);
          });
      },

      changePaginate() {
        var user = localStorage.getItem('user');
        localStorage.setItem(user + '.lastPaginate', this.table.isPaginated);
      },

      updating: function (status) {
        return status === 'starting' || status === 'stopping';
      },

      decorator(status) {
        switch (status) {
          case 'started':
            return 'is-success';
          case 'starting':
          case 'stopping':
            return 'is-warning';
          case 'stopped':
            return 'is-danger';
        }
      },

      start(name, status) {
        if (status == 'starting' || status == 'stopping') {
          this.$buefy.toast.open({
            message:
              'The ' +
              name +
              ' experiment is currently ' +
              status +
              '. You cannot make any changes at this time.',
            type: 'is-warning',
          });

          return;
        }

        this.$buefy.dialog.confirm({
          title: 'Start the Experiment',
          message: 'This will start the ' + name + ' experiment.',
          cancelText: 'Cancel',
          confirmText: 'Start',
          type: 'is-success',
          hasIcon: true,
          onConfirm: async () => {
            try {
              await axiosInstance.post('experiments/' + name + '/start');
              console.log('experiment started');
            } catch (err) {
              useErrorNotification(err);

              for (let i = 0; i < this.experiments.length; i++) {
                if (this.experiments[i].name == name) {
                  this.$set(this.experiments[i], 'status', 'stopped');
                  break;
                }
              }
            }
          },
        });
      },

      stop(name, status) {
        if (status == 'starting' || status == 'stopping') {
          this.$buefy.toast.open({
            message:
              'The ' +
              name +
              ' experiment is currently ' +
              status +
              '. You cannot make any changes at this time.',
            type: 'is-warning',
          });

          return;
        }

        this.$buefy.dialog.confirm({
          title: 'Stop the Experiment',
          message: 'This will stop the ' + name + ' experiment.',
          cancelText: 'Cancel',
          confirmText: 'Stop',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => {
            axiosInstance
              .post('experiments/' + name + '/stop')
              .then((response) => {
                console.log('experiment stopped: ' + response);
              })
              .catch((err) => {
                useErrorNotification(err);
                this.isWaiting = false;
              });
          },
        });
      },

      del(name, running) {
        if (running) {
          this.$buefy.toast.open({
            message:
              'The ' +
              name +
              ' experiment is running; you must stop it before deleting it.',
            type: 'is-warning',
            duration: 4000,
          });
        } else {
          this.$buefy.dialog.confirm({
            title: 'Delete the Experiment',
            message:
              'This will DELETE the ' +
              name +
              ' experiment. Are you sure you want to do this?',
            cancelText: 'Cancel',
            confirmText: 'Delete',
            type: 'is-danger',
            hasIcon: true,
            onConfirm: () => {
              this.isWaiting = true;

              axiosInstance
                .delete('experiments/' + name)
                .then((response) => {
                  if (response.status == 204) {
                    let exp = this.experiments;
                    for (let i = 0; i < exp.length; i++) {
                      if (exp[i].name == name) {
                        exp.splice(i, 1);
                        break;
                      }
                    }
                    this.experiments = [...exp];
                  }
                  this.isWaiting = false;
                })
                .catch((err) => {
                  useErrorNotification(err);
                  this.isWaiting = false;
                });
            },
          });
        }
      },
      create() {
        var disabledApps = [];
        if (this.createModal.scenario != null) {
          disabledApps = this.createModal.scenarios[this.createModal.scenario]
            .filter((item) => item.disabled)
            .map((item) => item.name);
        }

        const experimentData = {
          name: this.createModal.name,
          topology: this.createModal.topology,
          scenario: this.createModal.scenario,
          vlan_min: +this.createModal.vlan_min,
          vlan_max: +this.createModal.vlan_max,
          workflow_branch: this.createModal.branch,
          deploy_mode: this.createModal.deploy_mode,
          disabled_apps: disabledApps,
          default_bridge: this.createModal.bridge,
        };

        if (!this.createModal.name) {
          this.$buefy.toast.open({
            message: 'You must include a name for the experiment.',
            type: 'is-warning',
            duration: 4000,
          });

          return {};
        }

        if (!this.createModal.topology) {
          this.$buefy.toast.open({
            message: 'You must select an experiment topology.',
            type: 'is-warning',
            duration: 4000,
          });

          return {};
        }

        this.isWaiting = true;

        axiosInstance
          .post('experiments', experimentData, { timeout: 0 })
          .then((response) => {
            this.isWaiting = false;
          })
          .catch((err) => {
            useErrorNotification(err);
            this.isWaiting = false;
          });

        this.createModal.active = false;
        this.resetCreateModal();
      },

      getScenarios(topo) {
        // Reset these values for the case where a topo with scenarios was
        // initially selected, then another topo with no scenarios was
        // subsequently selected.
        this.createModal.scenarios = {};
        this.createModal.showScenarios = false;

        axiosInstance
          .get('topologies/' + topo + '/scenarios')
          .then((response) => {
            const state = response.data;
            if (
              state.scenarios != null &&
              Object.keys(state.scenarios).length != 0
            ) {
              let scenarioObj = {};
              for (const [name, apps] of Object.entries(state.scenarios)) {
                let appList = [];
                for (var appIdx = 0; appIdx < apps.length; appIdx++) {
                  appList.push({ name: apps[appIdx], disabled: false });
                }
                scenarioObj[name] = appList;
              }

              this.createModal.scenarios = scenarioObj;
              this.createModal.showScenarios = true;
            }
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },
      clickScenario(id) {
        let listOfApps = this.createModal.scenarios[this.createModal.scenario];
        listOfApps[id].disabled = !listOfApps[id].disabled;
      },

      resetCreateModal() {
        this.createModal = {
          active: false,
          name: null,
          bridgeErrType: null,
          bridgeErrMsg: null,
          nameErrType: null,
          nameErrMsg: null,
          topology: null,
          showScenarios: false,
          scenarios: {},
          scenario: null,
          vlan_min: null,
          vlan_max: null,
          deploy_mode: null,
        };
      },

      validate() {
        if (!this.createModal.name) {
          return false;
        }

        if (this.bridgeMode === 'auto') {
          if (this.createModal.name && this.createModal.name.length > 15) {
            this.createModal.nameErrType = 'is-danger';
            this.createModal.nameErrMsg =
              'experiment name must be 15 characters or less when using auto bridge mode';
            return false;
          }
        }

        for (let i = 0; i < this.experiments.length; i++) {
          if (this.experiments[i].name == this.createModal.name) {
            this.createModal.nameErrType = 'is-danger';
            this.createModal.nameErrMsg =
              'experiment with this name already exists';
            return false;
          }
        }

        if (/\s/.test(this.createModal.name)) {
          this.createModal.nameErrType = 'is-danger';
          this.createModal.nameErrMsg = 'experiment names cannot have a space';
          return false;
        } else if (this.createModal.name == 'create') {
          this.createModal.nameErrType = 'is-danger';
          this.createModal.nameErrMsg = 'experiment names cannot be create!';
          return false;
        } else {
          this.createModal.nameErrType = null;
          this.createModal.nameErrMsg = null;
        }

        if (!this.createModal.topology) {
          return false;
        }

        if (this.createModal.vlan_max < this.createModal.vlan_min) {
          return false;
        }

        if (this.createModal.vlan_min < 0) {
          return false;
        }

        if (this.createModal.vlan_min > 4094) {
          return false;
        }

        if (this.createModal.bridge && this.createModal.bridge.length > 15) {
          this.createModal.bridgeErrType = 'is-danger';
          this.createModal.bridgeErrMsg =
            'default bridge name must be 15 characters or less';
          return false;
        } else {
          this.createModal.bridgeErrType = null;
          this.createModal.bridgeErrMsg = null;
        }

        return true;
      },

      getExpControlLabel(expName, expStatus) {
        return expStatus.toUpperCase() == 'STARTED'
          ? 'Stop experiment ' + expName
          : 'Start experiment ' + expName;
      },
    },

    directives: {
      focus: {
        inserted(el) {
          if (el.tagName == 'INPUT') {
            el.focus();
          } else {
            el.querySelector('input').focus();
          }
        },
      },
    },

    data() {
      return {
        table: {
          isPaginated: false,
          perPage: 10,
          currentPage: 1,
          isPaginationSimple: true,
          paginationSize: 'is-small',
          defaultSortDirection: 'asc',
        },
        createModal: {
          active: false,
          name: null,
          bridgeErrType: null,
          bridgeErrMsg: null,
          nameErrType: null,
          nameErrMsg: null,
          topology: null,
          showScenarios: false,
          scenarios: {},
          scenario: null,
          vlan_min: null,
          vlan_max: null,
          branch: null,
          deploy_mode: null,
          bridge: null,
        },
        experiments: [],
        topologies: [],
        searchName: '',
        filtered: null,
        isMenuActive: false,
        action: null,
        rowName: null,
        isWaiting: true,
        options: {},
      };
    },
  };
</script>

<style lang="scss" scoped>
  .action:not(:last-child) {
    margin-right: 5px;
  }
</style>
