<template>
  <div class="content">
    <b-field position="is-right" grouped>
      <b-field>
        <b-autocomplete
          placeholder="Find an Experiment"
          v-model="searchName"
          icon="search"
          :data="filteredData"
          @select="(option) => (filtered = option)">
          <template #empty> No results found </template>
        </b-autocomplete>
        <p v-if="searchName" class="control">
          <button
            class="button input-button"
            aria-label="Clear experiment search"
            @click="searchName = ''">
            <b-icon icon="window-close"></b-icon>
          </button>
        </p>
      </b-field>
    </b-field>
    <b-table
      :data="filteredExperiments"
      :paginated="table.isPaginated"
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
            {{ emptyText }}
          </div>
        </section>
      </template>
      <b-table-column
        field="name"
        label="Experiment"
        header-class="sort-inline"
        sortable
        v-slot="props">
        <template v-if="roleAllowed('experiments', 'get', props.row.name)">
          <b-tooltip label="view SCORCH components" type="is-dark">
            <router-link
              class="navbar-item"
              :to="{
                name: 'scorchruns',
                params: { id: props.row.name },
              }">
              {{ props.row.name }}
            </router-link>
          </b-tooltip>
        </template>
        <template v-else>
          {{ props.row.name }}
        </template>
      </b-table-column>
      <b-table-column
        field="status"
        label="Experiment Status"
        sortable
        header-class="sort-inline"
        centered
        v-slot="props">
        <section v-if="props.row.status == 'starting'">
          <b-progress
            size="is-medium"
            type="is-warning"
            show-value
            :value="props.row.percent"
            format="percent"></b-progress>
        </section>
        <div v-else class="status-cell">
          <span
            class="tag is-medium"
            :class="expStatusDecorator(props.row.status)">
            {{ props.row.status }}
          </span>
          <b-tooltip
            v-if="expControlAllowed(props.row)"
            :label="expControlLabel(props.row)"
            type="is-dark">
            <button
              class="button is-small is-white"
              :class="{ 'is-loading': props.row.status == 'stopping' }"
              :aria-label="expControlLabel(props.row)"
              @click="expControl(props.row)">
              <b-icon :icon="props.row.running ? 'stop' : 'play'" />
            </button>
          </b-tooltip>
          <b-tooltip
            v-if="roleAllowed('experiments', 'get', props.row.name)"
            label="Go to experiment"
            type="is-dark">
            <router-link
              class="button is-small is-white"
              aria-label="Go to experiment"
              :to="{ name: 'experiment', params: { id: props.row.name } }">
              <b-icon icon="arrow-right" />
            </router-link>
          </b-tooltip>
        </div>
      </b-table-column>
      <b-table-column label="Scorch Status" centered v-slot="props">
        <div class="status-cell">
          <span class="tag is-medium" :class="scorchStatusDecorator(props.row)">
            {{ scorchStatus(props.row) }}
          </span>
          <b-tooltip
            v-if="scorchControlAllowed(props.row)"
            :label="scorchControlLabel(props.row)"
            type="is-dark">
            <button
              class="button is-small is-white"
              :class="{ 'is-loading': props.row.scorch.pending }"
              :disabled="props.row.scorch.pending"
              :aria-label="scorchControlLabel(props.row)"
              @click="scorchControl(props.row)">
              <b-icon :icon="props.row.scorch.running ? 'stop' : 'play'" />
            </button>
          </b-tooltip>
          <b-tooltip
            v-if="roleAllowed('experiments', 'get', props.row.name)"
            label="Go to SCORCH pipelines"
            type="is-dark">
            <router-link
              class="button is-small is-white"
              aria-label="Go to SCORCH pipelines"
              :to="{ name: 'scorchruns', params: { id: props.row.name } }">
              <b-icon icon="arrow-right" />
            </router-link>
          </b-tooltip>
        </div>
      </b-table-column>
      <b-table-column label="Terminal" centered v-slot="props">
        <button
          :aria-label="`Open terminal for experiment ${props.row.name}`"
          v-if="
            props.row.terminal &&
            roleAllowed('experiments', 'get', props.row.name)
          "
          class="button is-small is-white"
          @click="showExperimentTerminal(props.row.name)">
          <b-icon icon="terminal"></b-icon>
        </button>
      </b-table-column>
    </b-table>
    <b-modal
      v-model="terminal.modal"
      :can-cancel="terminal.ro"
      @close="resetTerminal"
      has-modal-card>
      <div class="modal-card" style="width: 60em">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ terminalName() }}</p>
        </header>
        <section class="modal-card-body">
          <vue-terminal :wsPath="terminal.loc"></vue-terminal>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <div v-if="terminal.ro">
            <b-tooltip
              label="this will close but not exit the terminal"
              type="is-light is-left"
              :delay="1000">
              <button class="button is-light" @click="resetTerminal">
                Close
              </button>
            </b-tooltip>
          </div>
          <div v-else>
            <b-tooltip
              label="this will EXIT the terminal"
              type="is-danger is-left"
              :delay="1000">
              <button class="button is-danger" @click="exitTerminal">
                Exit
              </button>
            </b-tooltip>
          </div>
        </footer>
      </div>
    </b-modal>
  </div>
</template>
<script>
  import Terminal from '@/components/MiniTerminal.vue';

  import axiosInstance from '@/utils/axios.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif';
  import {
    createPageLoader,
    loadingText,
    stillLoading,
  } from '@/utils/pageLoader.js';
  import {
    pageFetchers,
    scorchListed,
    scorchPipelines,
    scorchRow,
  } from '@/utils/pageData.js';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';
  import { useTable } from '@/utils/useTable.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { createLiveRows } from '@/utils/liveRows.js';

  // how long a SCORCH button spins waiting for the server to report that the
  // run started or stopped, in case that update never arrives
  const PENDING_TIMEOUT_MS = 15000;

  export default {
    setup() {
      const { table } = useTable();
      return { table, roleAllowed };
    },
    components: {
      'vue-terminal': Terminal,
    },

    async created() {
      addWsHandler(this.handle);
      this.liveRows = createLiveRows();
      this.loader = createPageLoader({
        key: 'scorch',
        fetch: pageFetchers.scorch,
        apply: (experiments, { requestedAt }) => {
          this.experiments = this.liveRows.merge(
            experiments,
            this.experiments,
            requestedAt,
          );
          this.loaded = true;
          this.getTerminals();
        },
      });
      this.loader.start();
    },

    beforeUnmount() {
      removeWsHandler(this.handle);
      this.loader.stop();
      // the rows live on in the page cache: stop their spinners
      this.experiments.forEach((exp) => this.settle(exp));
    },

    computed: {
      emptyText() {
        if (stillLoading(this.loaded, this.experiments)) {
          return loadingText('experiments');
        }
        if (this.experiments.length === 0) {
          return 'No experiments found with SCORCH pipelines';
        }
        return 'No experiments with SCORCH pipelines match your search';
      },
      filteredExperiments: function () {
        // a plain substring match: the search box is not a regular expression
        const search = this.searchName.toLowerCase();
        return this.experiments.filter((exp) =>
          exp.name.toLowerCase().includes(search),
        );
      },

      // the search box's suggestions
      filteredData() {
        return this.filteredExperiments.map((exp) => exp.name);
      },
    },

    methods: {
      expStatusDecorator(status) {
        switch (status) {
          case 'started':
          case 'running':
            return 'is-success';
          case 'starting':
          case 'stopping':
            return 'is-warning';
          case 'stopped':
            return 'is-danger';
        }
      },

      expControlLabel(exp) {
        return exp.running
          ? `Stop experiment ${exp.name}`
          : `Start experiment ${exp.name}`;
      },

      expControl(exp) {
        if (exp.status == 'starting' || exp.status == 'stopping') {
          this.$buefy.toast.open({
            message: `The ${exp.name} experiment is currently ${exp.status}. You cannot make any changes at this time.`,
            type: 'is-warning',
          });

          return;
        }

        if (exp.running) {
          this.$buefy.dialog.confirm({
            title: 'Stop the Experiment',
            message: `This will stop the ${exp.name} experiment.`,
            cancelText: 'Cancel',
            confirmText: 'Stop',
            type: 'is-danger',
            hasIcon: true,

            onConfirm: () => {
              axiosInstance
                .post(`experiments/${exp.name}/stop`)
                .catch((err) => {
                  useErrorNotification(err);
                });
            },
          });
        } else {
          this.$buefy.dialog.confirm({
            title: 'Start the Experiment',
            message: `This will start the ${exp.name} experiment.`,
            cancelText: 'Cancel',
            confirmText: 'Start',
            type: 'is-success',
            hasIcon: true,

            onConfirm: () => {
              axiosInstance
                .post(`experiments/${exp.name}/start`)
                .catch((err) => {
                  useErrorNotification(err);
                });
            },
          });
        }
      },

      scorchStatusDecorator(exp) {
        return exp.scorch.running ? 'is-success' : 'is-danger';
      },

      scorchStatus(exp) {
        return exp.scorch.running ? 'running' : 'stopped';
      },

      // a run's name, or its number when it has none
      runName(exp, run) {
        return exp.scorch.runs?.[run] || `run ${run}`;
      },

      expControlAllowed(exp) {
        return roleAllowed(
          exp.running ? 'experiments/stop' : 'experiments/start',
          'update',
          exp.name,
        );
      },

      scorchControlAllowed(exp) {
        return roleAllowed(
          'experiments/trigger',
          exp.scorch.running ? 'delete' : 'create',
          exp.name,
        );
      },

      // the button stops the current run, or starts the first one
      scorchControlLabel(exp) {
        return exp.scorch.running
          ? `Stop ${this.runName(exp, exp.scorch.run)}`
          : `Start ${this.runName(exp, 0)}`;
      },

      scorchControl(exp) {
        const scorch = exp.scorch;
        if (scorch.pending) return;

        const url = `experiments/${exp.name}/scorch/pipelines`;
        const request = scorch.running
          ? axiosInstance.delete(`${url}/${scorch.run}`)
          : axiosInstance.post(`${url}/0`);

        // the button spins until the server reports over the websocket that
        // the run started or stopped, or, if that never comes, for
        // PENDING_TIMEOUT_MS
        scorch.pending = true;
        clearTimeout(scorch.pendingTimer);
        scorch.pendingTimer = setTimeout(
          () => (scorch.pending = false),
          PENDING_TIMEOUT_MS,
        );
        request.catch((err) => {
          this.settle(exp);
          useErrorNotification(err);
        });
      },

      settle(exp) {
        clearTimeout(exp.scorch.pendingTimer);
        exp.scorch.pending = false;
      },

      findExperiment(name) {
        return this.experiments.find((exp) => exp.name == name);
      },
      // Replaces the open terminals with the current ones, so terminals that
      // exited while the page was not listening do not linger.
      async getTerminals() {
        const lists = await Promise.all(
          this.experiments.map((exp) =>
            axiosInstance
              .get(`experiments/${exp.name}/scorch/terminals`, {
                headers: { Accept: 'application/json' },
              })
              .then((resp) => resp.data.terminals ?? [])
              .catch((err) => {
                useErrorNotification(err);
                return [];
              }),
          ),
        );

        const terminals = {};
        lists.flat().forEach((t) => (terminals[t.exp] = t));
        this.terminals = terminals;
        this.experiments.forEach((exp) => {
          exp.terminal = exp.name in terminals;
        });
      },

      terminalName() {
        let name = `Terminal (${this.terminal.exp})`;

        if (this.terminal.ro) {
          name += ' (read-only)';
        }

        return name;
      },

      resetTerminal(force = false) {
        if (force || this.terminal.ro) {
          this.terminal = {
            modal: false,
            exp: '',
            loc: '',
            exit: '',
            ro: false,
          };
        }
      },

      exitTerminal() {
        // terminal.exit is a server-provided path that already includes the
        // base path; clear baseURL so axios doesn't prepend it again
        axiosInstance
          .post(this.terminal.exit, null, { baseURL: '' })
          .then(() => {
            // read the experiment before resetTerminal clears it
            const exp = this.terminal.exp;
            this.resetTerminal(true);
            this.experimentTerminal(exp, false);
            delete this.terminals[exp];
          })
          .catch(useErrorNotification);
      },

      experimentTerminal(name, enabled) {
        const exp = this.findExperiment(name);
        if (exp) exp.terminal = enabled;
      },

      showExperimentTerminal(exp) {
        const comp = this.terminals[exp];
        if (comp) {
          const endpoint = `experiments/${comp.exp}/scorch/components/${comp.run}/${comp.loop}/${comp.stage}/${comp.name}`;

          axiosInstance
            .get(endpoint, {
              headers: { Accept: 'application/json' },
            })
            .then((resp) => {
              if (resp.data.terminal) {
                let t = resp.data.terminal;

                this.terminal.loc = t.loc;
                this.terminal.exit = t.exit;
                this.terminal.exp = t.exp;
                this.terminal.ro = t.readOnly;
                this.terminal.modal = true;
              } else {
                // the component exited since the terminal was listed
                this.experimentTerminal(exp, false);
                delete this.terminals[exp];
                this.$buefy.toast.open({
                  message: `The SCORCH terminal for ${exp} has closed`,
                  type: 'is-info',
                  duration: 4000,
                });
              }
            })
            .catch((err) => {
              useErrorNotification(err);
            });
        }
      },

      wsHandleScorch(msg) {
        const [expName, runID] = msg.resource.name.split('/');
        const exp = this.findExperiment(expName);

        switch (msg.resource.action) {
          case 'start': {
            if (exp) {
              exp.scorch.running = true;
              exp.scorch.run = parseInt(runID);
              this.settle(exp);
            }

            break;
          }

          case 'success': {
            if (exp) {
              exp.scorch.running = false;
              this.settle(exp);
            }

            break;
          }

          case 'error': {
            if (exp) {
              exp.scorch.running = false;
              this.settle(exp);
            }

            showError(
              msg.result?.error ??
                `SCORCH run ${runID} failed for experiment ${expName}`,
            );

            break;
          }

          case 'terminal-create': {
            this.terminals[expName] = msg.result;
            this.experimentTerminal(expName, true);

            break;
          }

          case 'terminal-exit': {
            this.experimentTerminal(expName, false);
            delete this.terminals[expName];

            break;
          }
        }
      },
      wsHandleExperiment(msg) {
        const name = msg.resource.name;

        if (msg.resource.action == 'create') {
          this.addIfScorch(msg.result);
          return;
        }

        const i = this.experiments.findIndex((exp) => exp.name == name);
        if (i < 0) return;
        const exp = this.experiments[i];

        switch (msg.resource.action) {
          case 'delete': {
            this.experiments.splice(i, 1);
            delete this.terminals[name];

            this.$buefy.toast.open({
              message: `The ${name} experiment has been deleted.`,
              type: 'is-success',
              duration: 4000,
            });

            break;
          }

          case 'start': {
            // the published experiment has no SCORCH state; keep ours
            this.experiments[i] = {
              ...msg.result,
              status: 'started',
              scorch: exp.scorch,
              terminal: exp.terminal,
            };

            this.$buefy.toast.open({
              message: `The ${name} experiment has been started.`,
              type: 'is-success',
              duration: 4000,
            });

            break;
          }

          case 'stop': {
            // stopping ends any SCORCH run and its terminals
            this.settle(exp);
            this.experiments[i] = {
              ...msg.result,
              status: 'stopped',
              scorch: { ...exp.scorch, running: false },
              terminal: false,
            };
            delete this.terminals[name];

            this.$buefy.toast.open({
              message: `The ${name} experiment has been stopped.`,
              type: 'is-success',
              duration: 4000,
            });

            break;
          }

          case 'starting':
          case 'stopping': {
            exp.status = msg.resource.action;
            exp.percent = 0;

            this.$buefy.toast.open({
              message: `The ${name} experiment is being updated.`,
              type: 'is-warning',
            });

            break;
          }

          case 'progress': {
            exp.percent = Math.round(msg.result.percent * 100);
            break;
          }
        }
      },

      // adds a newly created experiment if it has SCORCH configured (the
      // created experiment carries its app names)
      async addIfScorch(exp) {
        // the same experiments the SCORCH list shows
        if (!scorchListed(exp)) return;

        try {
          const pipelines = await scorchPipelines(exp.name);
          // a new experiment is stopped, so SCORCH is not running in it
          this.experiments.push({
            ...scorchRow(exp, pipelines),
            status: 'stopped',
          });

          this.$buefy.toast.open({
            message: `The ${exp.name} experiment has been created.`,
            type: 'is-success',
            duration: 4000,
          });
        } catch (err) {
          useErrorNotification(err);
        }
      },

      handle(msg) {
        switch (msg.resource.type) {
          case 'apps/scorch': {
            this.liveRows.touch(msg.resource.name.split('/')[0]);
            this.wsHandleScorch(msg);
            break;
          }
          case 'experiment': {
            this.liveRows.touch(msg.resource.name);
            this.wsHandleExperiment(msg);
            break;
          }
        }
      },
    },

    data() {
      return {
        experiments: [], // experiments with scorch configured
        terminals: {}, // open SCORCH terminals by experiment name
        terminal: {
          // terminal currently being viewed
          modal: false,
          exp: '',
          loc: '',
          exit: '',
          ro: false,
        },
        searchName: '',
        loaded: false, // false until the first list arrives
      };
    },
  };
</script>
<style scoped>
  div.autocomplete :deep(a.dropdown-item) {
    color: #383838 !important;
  }

  /* headings stay on one line, however narrow the table gets */
  :deep(th) {
    white-space: nowrap;
  }

  /* a status tag with its buttons beside it */
  .status-cell {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
  }
</style>
