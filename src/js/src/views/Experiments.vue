<template>
  <div class="content">
    <!-- the card focuses its name input, which the modal would take over -->
    <b-modal
      v-model="creating"
      has-modal-card
      :auto-focus="false"
      aria-role="dialog"
      aria-label="Create a new experiment"
      aria-modal>
      <create-experiment-card
        :experiment-names="experimentNames"
        :options="options"
        @create="create" />
    </b-modal>
    <template
      v-if="!stillLoading(loaded, experiments) && experiments.length == 0">
      <section class="hero is-bold is-large">
        <div class="hero-body">
          <div class="container" style="text-align: center">
            <h1 class="title">There are no experiments!</h1>
            <b-button
              v-if="roleAllowed('experiments', 'create')"
              type="is-success"
              outlined
              @click="creating = true"
              >Create One Now!</b-button
            >
          </div>
        </div>
      </section>
    </template>
    <template v-else>
      <b-field position="is-right" grouped>
        <div
          v-if="paginationNeeded"
          class="control is-flex is-align-items-center">
          <b-switch v-model="table.isPaginated" size="is-small" type="is-light"
            >Paginate</b-switch
          >
        </div>
        <b-field>
          <b-autocomplete
            v-model="searchName"
            placeholder="Find an Experiment"
            icon="search"
            :data="filteredData">
            <template #empty> No results found </template>
          </b-autocomplete>
          <p v-if="searchName" class="control">
            <button
              aria-label="Clear experiment search"
              class="button input-button"
              @click="searchName = ''">
              <b-icon icon="window-close"></b-icon>
            </button>
          </p>
        </b-field>
        <p v-if="roleAllowed('experiments', 'create')" class="control">
          <b-tooltip label="create a new experiment" type="is-light" multilined>
            <button
              aria-label="Create a new experiment"
              class="button is-light"
              @click="creating = true">
              <b-icon icon="plus"></b-icon>
            </button>
          </b-tooltip>
        </p>
      </b-field>
      <div>
        <b-table
          :data="filteredExperiments"
          :paginated="table.isPaginated && paginationNeeded"
          aria-next-label="Next page"
          aria-previous-label="Previous page"
          aria-page-label="Page"
          aria-current-label="Current page"
          :per-page="table.perPage"
          v-model:current-page="table.currentPage"
          :pagination-simple="table.isPaginationSimple"
          :pagination-size="table.paginationSize"
          :default-sort-direction="table.defaultSortDirection"
          default-sort="name">
          <template #empty>
            <section class="section">
              <div class="content has-text-white has-text-centered">
                {{
                  stillLoading(loaded, experiments)
                    ? loadingText('experiments')
                    : 'No experiments match your search'
                }}
              </div>
            </section>
          </template>
          <b-table-column
            field="name"
            label="Name"
            sortable
            header-class="sort-inline"
            v-slot="props">
            <template
              v-if="
                updating(props.row.status) ||
                !roleAllowed('experiments', 'get', props.row.name)
              ">
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
            header-class="sort-inline"
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
              v-else-if="
                roleAllowed(
                  props.row.running ? 'experiments/stop' : 'experiments/start',
                  'update',
                  props.row.name,
                )
              ">
              <b-tooltip
                :label="getExpControlLabel(props.row.name, props.row.status)"
                type="is-dark">
                <span
                  class="tag is-medium"
                  :class="decorator(props.row.status)">
                  <div class="field is-clickable">
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
            sortable
            header-class="sort-inline"
            v-slot="props">
            {{ formatLowercase(props.row.topology) }}
          </b-table-column>
          <b-table-column
            field="scenario"
            label="Scenario"
            sortable
            header-class="sort-inline"
            v-slot="props">
            {{ formatLowercase(props.row.scenario) }}
          </b-table-column>
          <b-table-column
            field="start_time"
            label="Start Time"
            sortable
            header-class="sort-inline"
            v-slot="props">
            {{ props.row.start_time }}
          </b-table-column>
          <b-table-column
            field="vm_count"
            label="VMs"
            width="50"
            centered
            sortable
            header-class="sort-inline"
            v-slot="props">
            {{ props.row.vm_count }}
          </b-table-column>
          <b-table-column
            field="vlan_range"
            label="VLANs"
            centered
            v-slot="props">
            <template v-if="!props.row.partial">
              {{ props.row.vlan_min }} - {{ props.row.vlan_max }} ({{
                props.row.vlan_count
              }})
            </template>
          </b-table-column>
          <b-table-column label="Actions" width="125" centered v-slot="props">
            <b-tooltip
              class="action"
              :delay="500"
              label="delete experiment"
              type="is-light"
              multilined>
              <button
                :aria-label="`Delete experiment ${props.row.name}`"
                v-if="roleAllowed('experiments', 'delete', props.row.name)"
                class="button is-light is-small"
                :class="{ 'is-loading': deleting[props.row.name] }"
                :disabled="
                  updating(props.row.status) || deleting[props.row.name]
                "
                @click="del(props.row.name, props.row.running)">
                <b-icon icon="trash"></b-icon>
              </button>
            </b-tooltip>
            <b-tooltip
              class="action"
              :delay="500"
              label="view soh"
              type="is-light"
              multilined>
              <router-link
                v-if="roleAllowed('experiments', 'get', props.row.name)"
                :aria-label="`View state of health for experiment ${props.row.name}`"
                class="button is-light is-small"
                :to="{
                  name: 'soh',
                  params: { id: props.row.name },
                }">
                <b-icon icon="heartbeat"></b-icon>
              </router-link>
            </b-tooltip>
            <b-tooltip
              class="action"
              :delay="500"
              label="view scorch"
              type="is-light"
              multilined>
              <router-link
                v-if="roleAllowed('experiments', 'get', props.row.name)"
                :aria-label="`View SCORCH runs for experiment ${props.row.name}`"
                class="button is-light is-small"
                :to="{
                  name: 'scorchruns',
                  params: { id: props.row.name },
                }">
                <b-icon icon="fire"></b-icon>
              </router-link>
            </b-tooltip>
          </b-table-column>
        </b-table>
      </div>
    </template>
    <b-loading
      :is-full-page="false"
      v-model="isWaiting"
      :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  import CreateExperimentCard from '@/components/CreateExperimentCard.vue';
  import { createLiveRows } from '@/utils/liveRows.js';
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import axiosInstance from '@/utils/axios.js';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';
  import { useTable } from '@/utils/useTable.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { plural } from '@/utils/plural.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import {
    createPageLoader,
    loadingText,
    stillLoading,
  } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';

  export default {
    components: { CreateExperimentCard },
    mixins: [formattingMixin],

    setup() {
      return { ...useTable({ name: 'experiments' }), roleAllowed };
    },

    async beforeUnmount() {
      clearTimeout(this.reloadTimer);
      removeWsHandler(this.handleWs);
      this.loader.stop();
    },

    async created() {
      addWsHandler(this.handleWs);
      this.liveRows = createLiveRows();
      this.loader = createPageLoader({
        key: 'experiments',
        fetch: pageFetchers.experiments,
        apply: (experiments, { requestedAt }) => {
          // the server reports a starting experiment's progress from 0 to 1
          const loaded = experiments.map((exp) => ({
            ...exp,
            percent: Math.round((exp.percent ?? 0) * 100),
          }));
          const byName = new Map(loaded.map((exp) => [exp.name, exp]));
          // a row known only from websocket messages takes the loaded
          // details and keeps the progress the messages reported
          this.experiments = this.liveRows
            .merge(loaded, this.experiments, requestedAt)
            .map((exp) => {
              const row = exp.partial && byName.get(exp.name);
              if (!row) return exp;
              return exp.status == row.status
                ? { ...row, percent: Math.max(exp.percent, row.percent) }
                : row;
            });
          this.loaded = true;
        },
      });
      this.loader.start();
      axiosInstance
        .get('/options')
        .then((resp) => {
          this.options = resp.data;
        })
        .catch((err) => {
          console.warn('failed to get experiment options', err);
        });
    },

    computed: {
      filteredExperiments: function () {
        let experiments = this.experiments;

        const term = (this.searchName ?? '').toLowerCase();
        var data = [];

        for (let i in experiments) {
          let exp = experiments[i];
          if (exp.name.toLowerCase().includes(term)) {
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

      experimentNames() {
        return this.experiments.map((exp) => exp.name);
      },

      paginationNeeded() {
        return this.experiments.length > this.table.perPage;
      },
    },

    methods: {
      loadingText,
      stillLoading,

      handleWs(msg) {
        // Experiments created as configs (the Configs page, workflows) are
        // announced as configs, not experiments.
        if (
          msg.resource.type == 'config' &&
          msg.resource.name?.startsWith('Experiment/')
        ) {
          this.reloadSoon();
          return;
        }

        // We only care about publishes pertaining to an experiment resource.
        if (msg.resource.type != 'experiment') {
          return;
        }

        if (msg.resource.action == 'delete') {
          this.liveRows.remove(msg.resource.name);
        } else {
          this.liveRows.touch(msg.resource.name);
        }
        let exp = this.experiments;

        // An experiment this list has not seen yet (created elsewhere, or
        // while the list was loading): show it at once and fetch its details.
        const known = exp.some((e) => e.name == msg.resource.name);
        if (!known && !['create', 'delete'].includes(msg.resource.action)) {
          this.experiments = [
            ...exp,
            {
              name: msg.resource.name,
              status: {
                starting: 'starting',
                progress: 'starting',
                stopping: 'stopping',
              }[msg.resource.action],
              percent: 0,
              partial: true,
            },
          ];
          exp = this.experiments;
          this.reloadSoon();
        }

        switch (msg.resource.action) {
          case 'create': {
            msg.result.status = 'stopped';
            // a list loaded meanwhile may already have it
            const i = exp.findIndex((e) => e.name == msg.resource.name);
            if (i < 0) exp.push(msg.result);
            else exp[i] = msg.result;

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

            if (msg.result.delayed_vms > 0) {
              toast = `${toast} (with ${plural(msg.result.delayed_vms, 'delayed VM')}).`;
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

      // Several messages about new experiments arrive together; one list
      // request covers them.
      reloadSoon() {
        clearTimeout(this.reloadTimer);
        this.reloadTimer = setTimeout(() => this.loader.load(), 250);
      },

      updating(status) {
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
          onConfirm: () => {
            axiosInstance
              .post('experiments/' + name + '/start')
              .catch((err) => {
                for (let i = 0; i < this.experiments.length; i++) {
                  if (this.experiments[i].name == name) {
                    this.experiments[i].status = 'stopped';
                    break;
                  }
                }

                useErrorNotification(err);
              });
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
            axiosInstance.post('experiments/' + name + '/stop').catch((err) => {
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
              // only this experiment's button spins while it is deleted
              this.deleting[name] = true;

              axiosInstance
                .delete('experiments/' + name)
                .then(() => {
                  this.experiments = this.experiments.filter(
                    (exp) => exp.name != name,
                  );
                })
                .catch((err) => useErrorNotification(err))
                .finally(() => delete this.deleting[name]);
            },
          });
        }
      },
      create(request) {
        this.creating = false;
        this.isWaiting = true;

        axiosInstance
          .post('experiments', request, { timeout: 0 })
          .catch((err) => useErrorNotification(err))
          .finally(() => {
            this.isWaiting = false;
          });
      },

      getExpControlLabel(expName, expStatus) {
        return expStatus.toUpperCase() == 'STARTED'
          ? 'Stop experiment ' + expName
          : 'Start experiment ' + expName;
      },
    },

    data() {
      return {
        creating: false, // the create card is open
        experiments: [],
        searchName: '',
        isWaiting: false, // set while a change is being saved
        deleting: {}, // names of the experiments being deleted
        loaded: false, // false until the first list arrives
        options: {},
      };
    },
  };
</script>
