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
        :pending="createPending"
        :error="createError"
        :start-allowed="startAllowed"
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
      <div class="exp-toolbar">
        <stat-strip label="Experiment totals" :stats="totalsShown" />
        <b-field class="exp-toolbar-controls" position="is-right" grouped>
          <b-field v-if="selected.length" class="exp-selection-actions" grouped>
            <b-field v-for="action in bulkActions" :key="action.name">
              <b-tooltip
                :label="action.reason || action.label"
                type="is-dark"
                multilined>
                <b-button
                  :class="`exp-bulk-${action.name}`"
                  :type="action.type"
                  :icon-left="action.icon"
                  :aria-label="action.label"
                  :disabled="action.disabled"
                  @click="bulk(action.name)">
                  {{ action.text }}
                </b-button>
              </b-tooltip>
            </b-field>
            <b-field>
              <b-tooltip label="clear the selection" type="is-dark">
                <b-button
                  type="is-light"
                  icon-left="window-close"
                  aria-label="Clear the experiment selection"
                  @click="selected = []">
                </b-button>
              </b-tooltip>
            </b-field>
          </b-field>
          <div
            v-if="paginationNeeded"
            class="control is-flex is-align-items-center">
            <b-switch
              v-model="table.isPaginated"
              size="is-small"
              type="is-light"
              >Paginate</b-switch
            >
          </div>
          <b-field :addons="false" class="has-addons">
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
            <b-tooltip
              label="create a new experiment"
              type="is-light"
              multilined>
              <button
                aria-label="Create a new experiment"
                class="button is-light"
                @click="creating = true">
                <b-icon icon="plus"></b-icon>
              </button>
            </b-tooltip>
          </p>
        </b-field>
      </div>
      <div class="exp-table">
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
          <!-- hidden, not left out: a column added after mount goes last -->
          <b-table-column
            :visible="selectionOffered"
            field="select"
            label=""
            header-class="exp-select-cell"
            cell-class="exp-select-cell">
            <template #header>
              <b-tooltip label="Select/Unselect All" type="is-dark">
                <b-checkbox
                  v-model="allSelected"
                  :indeterminate="someSelected"
                  :disabled="!selectableShown.length">
                  <span class="is-sr-only">Select all experiments</span>
                </b-checkbox>
              </b-tooltip>
            </template>
            <template #default="props">
              <b-checkbox
                v-model="selected"
                :native-value="props.row.name"
                :disabled="!selectable(props.row)">
                <span class="is-sr-only"
                  >Select experiment {{ props.row.name }}</span
                >
              </b-checkbox>
            </template>
          </b-table-column>
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
            <template v-else-if="rowActions[props.row.name].power.permitted">
              <b-tooltip
                :label="
                  rowActions[props.row.name].power.reason ||
                  getExpControlLabel(props.row.name, props.row.status)
                "
                type="is-dark"
                multilined>
                <button
                  type="button"
                  class="tag is-medium exp-status-control"
                  :class="decorator(props.row.status)"
                  :aria-label="`${rowActions[props.row.name].power.label} experiment ${props.row.name} (${props.row.status})`"
                  :disabled="rowActions[props.row.name].power.disabled"
                  @click="
                    props.row.running
                      ? stop(props.row.name)
                      : start(props.row.name)
                  ">
                  {{ props.row.status }}
                </button>
              </b-tooltip>
            </template>
            <template v-else>
              <span class="tag is-medium" :class="decorator(props.row.status)">
                {{ props.row.status }}
              </span>
            </template>
            <b-tooltip
              v-if="props.row.running && props.row.interrupted"
              class="exp-interrupted"
              :label="props.row.interrupted"
              type="is-dark"
              multilined>
              <span class="tag is-warning">interrupted</span>
              <span class="is-sr-only">: {{ props.row.interrupted }}</span>
            </b-tooltip>
          </b-table-column>
          <b-table-column
            field="topology"
            label="Topology"
            sortable
            header-class="sort-inline"
            v-slot="props">
            <router-link
              v-if="configLinked('Topology', props.row.topology)"
              :to="configViewRoute('Topology', props.row.topology)">
              {{ formatLowercase(props.row.topology) }}
            </router-link>
            <template v-else>
              {{ formatLowercase(props.row.topology) }}
            </template>
          </b-table-column>
          <b-table-column
            field="scenario"
            label="Scenario"
            sortable
            header-class="sort-inline"
            v-slot="props">
            <router-link
              v-if="configLinked('Scenario', props.row.scenario)"
              :to="configViewRoute('Scenario', props.row.scenario)">
              {{ formatLowercase(props.row.scenario) }}
            </router-link>
            <template v-else>
              {{ formatLowercase(props.row.scenario) }}
            </template>
          </b-table-column>
          <b-table-column
            field="start_time"
            label="Start Time"
            sortable
            :custom-sort="sortByStartTime"
            header-class="sort-inline"
            v-slot="props">
            <b-tooltip
              v-if="startTimeParts(props.row.start_time)"
              :label="props.row.start_time"
              type="is-dark">
              <time class="exp-start-time" :datetime="props.row.start_time">
                <span>{{ startTimeParts(props.row.start_time).time }}</span>
                {{ ' ' }}
                <span>{{ startTimeParts(props.row.start_time).date }}</span>
              </time>
              <span class="is-sr-only"> ({{ props.row.start_time }})</span>
            </b-tooltip>
            <template v-else>
              {{ props.row.start_time || 'N/A' }}
            </template>
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
            field="vlan_count"
            label="VLANs"
            centered
            sortable
            :custom-sort="sortByVlans"
            header-class="sort-inline"
            v-slot="props">
            <template v-if="!props.row.partial">
              {{ props.row.vlan_min }} - {{ props.row.vlan_max }} ({{
                props.row.vlan_count
              }})
            </template>
          </b-table-column>
          <b-table-column label="Actions" width="176" centered v-slot="props">
            <div class="exp-row-actions">
              <b-tooltip
                v-for="action in powerButtons(props.row)"
                :key="action.name"
                :label="action.reason || `${action.name} experiment`"
                type="is-dark"
                multilined>
                <button
                  type="button"
                  :class="[
                    'button',
                    'is-small',
                    action.type,
                    `exp-row-${action.name}`,
                    { 'is-loading': action.pending },
                  ]"
                  :aria-label="`${action.label} experiment ${props.row.name}`"
                  :aria-busy="action.pending"
                  :disabled="action.disabled || action.pending"
                  @click="
                    action.name == 'start'
                      ? start(props.row.name)
                      : stop(props.row.name)
                  ">
                  <b-icon :icon="action.icon" />
                </button>
              </b-tooltip>
              <b-tooltip
                v-if="rowActions[props.row.name].delete.permitted"
                :delay="500"
                :label="
                  (!deleting[props.row.name] &&
                    rowActions[props.row.name].delete.reason) ||
                  'delete experiment'
                "
                type="is-light"
                multilined>
                <button
                  :aria-label="`Delete experiment ${props.row.name}`"
                  class="button is-light is-small"
                  :class="{ 'is-loading': deleting[props.row.name] }"
                  :disabled="rowActions[props.row.name].delete.disabled"
                  @click="del(props.row.name)">
                  <b-icon icon="trash"></b-icon>
                </button>
              </b-tooltip>
              <b-tooltip
                v-if="roleAllowed('experiments', 'get', props.row.name)"
                :delay="500"
                label="view state of health"
                type="is-light"
                multilined>
                <router-link
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
                v-if="roleAllowed('experiments', 'get', props.row.name)"
                :delay="500"
                label="view SCORCH runs"
                type="is-light"
                multilined>
                <router-link
                  :aria-label="`View SCORCH runs for experiment ${props.row.name}`"
                  class="button is-light is-small"
                  :to="{
                    name: 'scorchruns',
                    params: { id: props.row.name },
                  }">
                  <b-icon icon="fire"></b-icon>
                </router-link>
              </b-tooltip>
            </div>
          </b-table-column>
        </b-table>
      </div>
    </template>
  </div>
</template>

<script>
  import CreateExperimentCard from '@/components/CreateExperimentCard.vue';
  import StatStrip from '@/components/StatStrip.vue';
  import { createLiveRows } from '@/utils/liveRows.js';
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import * as experimentsApi from '@/api/experiments.js';
  import {
    addWsHandler,
    onWsReconnect,
    removeWsHandler,
  } from '@/utils/websocket';
  import { useTable } from '@/utils/useTable.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { namedList, plural, pluralWord } from '@/utils/plural.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import {
    BULK_ACTIONS,
    EXPERIMENT_ACTIONS,
    bulkActionState,
    experimentActionState,
    offerForcedStop,
    partitionExperiments,
    stopCanBeForced,
  } from '@/utils/experimentActions.js';
  import {
    confirmOperation,
    errorLine,
    notifyOutcome,
    outcomeFromSettled,
  } from '@/utils/operations.js';
  import {
    createPageLoader,
    loadingText,
    stillLoading,
  } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';
  import { configViewRoute } from '@/utils/configCache.js';
  import {
    experimentTotals,
    sortByStartTime,
    sortByVlans,
    startTimeParts,
  } from '@/utils/experimentList.js';

  export default {
    components: { CreateExperimentCard, StatStrip },
    mixins: [formattingMixin],

    setup() {
      return { ...useTable({ name: 'experiments' }), roleAllowed };
    },

    async beforeUnmount() {
      clearTimeout(this.reloadTimer);
      removeWsHandler(this.handleWs);
      this.offWsReconnect?.();
      this.loader.stop();
    },

    async created() {
      addWsHandler(this.handleWs);
      // experiments a selection action is acting on, whose own toasts give
      // way to its one summary
      this.bulkPending = new Set();
      this.liveRows = createLiveRows();
      // a restarted server numbers its messages afresh
      this.offWsReconnect = onWsReconnect(() => this.liveRows.reset());
      this.loader = createPageLoader({
        key: 'experiments',
        fetch: pageFetchers.experiments,
        apply: (experiments, { seq }) => {
          // the server reports a starting experiment's progress from 0 to 1
          const loaded = experiments.map((exp) => ({
            ...exp,
            percent: Math.round((exp.percent ?? 0) * 100),
          }));
          const byName = new Map(loaded.map((exp) => [exp.name, exp]));
          // a row known only from websocket messages takes the loaded
          // details and keeps the progress the messages reported
          this.experiments = this.liveRows
            .merge(loaded, this.experiments, seq)
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
      experimentsApi
        .getExperimentOptions()
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
          if (exp.name.toLowerCase().includes(term)) data.push(exp);
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

      // each row's status control (start or stop) and delete button
      rowActions() {
        const opts = (exp) => ({
          can: roleAllowed,
          deleting: !!this.deleting[exp.name],
          starting: !!this.starting[exp.name],
          stopping: !!this.stopping[exp.name],
        });
        return Object.fromEntries(
          this.experiments.map((exp) => [
            exp.name,
            {
              power: experimentActionState(
                exp.running ? 'stop' : 'start',
                exp,
                opts(exp),
              ),
              delete: experimentActionState('delete', exp, opts(exp)),
              start: experimentActionState('start', exp, opts(exp)),
              stop: experimentActionState('stop', exp, opts(exp)),
            },
          ]),
        );
      },

      // the counts above the table, of every experiment, not only those shown
      totalsShown() {
        const totals = experimentTotals(this.experiments);
        const value = (n) =>
          stillLoading(this.loaded, this.experiments) ? '…' : n;
        return [
          ['experiments', 'Experiments', totals.experiments],
          ['running', 'Running', totals.running, 'success'],
          ['vms', 'Running VMs', totals.runningVms, 'success'],
          ['vlans', 'VLANs', totals.vlans],
        ].map(([key, label, n, tone]) => ({
          key,
          label,
          value: value(n),
          tone,
        }));
      },

      // whether the role may act on any experiment, so offering a selection
      selectionOffered() {
        return this.experiments.some((exp) => this.selectable(exp));
      },

      selectableShown() {
        return this.filteredExperiments.filter((exp) => this.selectable(exp));
      },

      allSelected: {
        get() {
          return (
            this.selectableShown.length > 0 &&
            this.selectableShown.every((exp) =>
              this.selected.includes(exp.name),
            )
          );
        },
        set(on) {
          this.selected = on ? this.selectableShown.map((exp) => exp.name) : [];
        },
      },

      someSelected() {
        return this.selected.length > 0 && !this.allSelected;
      },

      // the selection toolbar's buttons, each counting what it would act on
      bulkActions() {
        const opts = this.actionOpts();
        return BULK_ACTIONS.map((name) => {
          const state = bulkActionState(
            name,
            this.selected,
            this.experiments,
            opts,
          );
          return {
            ...state,
            text: `${EXPERIMENT_ACTIONS[name].label} (${state.count})`,
          };
        });
      },
    },

    watch: {
      // a selected experiment that is gone leaves the selection
      experiments(experiments) {
        const names = new Set(experiments.map((exp) => exp.name));
        if (this.selected.some((name) => !names.has(name))) {
          this.selected = this.selected.filter((name) => names.has(name));
        }
      },

      // a card opened again starts without the last one's error
      creating(open) {
        if (open) {
          this.createAttempt += 1;
          this.createError = null;
        }
      },
    },

    methods: {
      loadingText,
      stillLoading,
      configViewRoute,
      sortByStartTime,
      sortByVlans,
      startTimeParts,

      // whether the experiment's topology or scenario links to its config
      configLinked(kind, name) {
        return (
          !!name &&
          roleAllowed('configs', 'list') &&
          roleAllowed('configs', 'get', `${kind}/${name}`)
        );
      },

      // The row's Start and Stop buttons, each spinning while its request is
      // out or the experiment is starting or stopping.
      powerButtons(exp) {
        const { start, stop } = this.rowActions[exp.name];
        return [
          {
            ...start,
            pending: !!this.starting[exp.name] || exp.status == 'starting',
          },
          {
            ...stop,
            pending: !!this.stopping[exp.name] || exp.status == 'stopping',
          },
        ];
      },

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
          this.liveRows.remove(msg.resource.name, msg.seq);
        } else {
          this.liveRows.touch(msg.resource.name, msg.seq);
        }
        let exp = this.experiments;

        // An experiment this list has not seen yet (created elsewhere, or
        // while the list was loading): show it at once and fetch its details.
        const known = exp.some((e) => e.name == msg.resource.name);
        const ownRow = ['create', 'delete', 'errorStopping'];
        if (!known && !ownRow.includes(msg.resource.action)) {
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

            this.wsToast(msg.resource.name, {
              message:
                'The ' + msg.resource.name + ' experiment has been deleted.',
              type: 'is-success',
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

            const started = { message: toast, type: 'is-success' };
            // a selection's summary leaves out delayed VMs, so they show here
            if (msg.result.delayed_vms > 0) this.$buefy.toast.open(started);
            else this.wsToast(msg.resource.name, started);

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

            this.wsToast(msg.resource.name, {
              message:
                'The ' + msg.resource.name + ' experiment has been stopped.',
              type: 'is-success',
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

            this.wsToast(msg.resource.name, {
              message:
                'The ' + msg.resource.name + ' experiment is being updated.',
              type: 'is-warning',
            });

            break;
          }

          // the experiment may still be running, or stopped with an error
          case 'errorStopping': {
            this.reloadSoon();
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

      // a toast about one experiment, unless a selection action covers it
      wsToast(name, toast) {
        if (!this.bulkPending.has(name)) this.$buefy.toast.open(toast);
      },

      // Several messages about new experiments arrive together; one list
      // request covers them.
      reloadSoon() {
        clearTimeout(this.reloadTimer);
        this.reloadTimer = setTimeout(
          () => this.loader.load({ force: true }),
          250,
        );
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

      // whether the role may take a selection action on the experiment
      selectable(exp) {
        return BULK_ACTIONS.some((name) =>
          roleAllowed(...EXPERIMENT_ACTIONS[name].rbac, exp.name),
        );
      },

      startAllowed(name) {
        return roleAllowed('experiments/start', 'update', name);
      },

      // what the actions' states read: the role and the requests under way
      actionOpts() {
        return {
          can: roleAllowed,
          deleting: this.deleting,
          starting: this.starting,
          stopping: this.stopping,
        };
      },

      // starting is not destructive, and the row shows its progress
      start(name) {
        if (this.starting[name]) return;
        this.startExperiment(name).catch((err) => useErrorNotification(err));
      },

      // Starts one experiment, its controls disabled until the server
      // answers; one that failed to start shows as stopped.
      async startExperiment(name) {
        this.starting[name] = true;
        try {
          await experimentsApi.startExperiment(name);
        } catch (err) {
          const row = this.experiments.find((exp) => exp.name == name);
          if (row) row.status = 'stopped';
          throw err;
        } finally {
          delete this.starting[name];
        }
      },

      stop(name) {
        if (this.stopping[name]) return;
        this.$buefy.dialog.confirm({
          title: 'Stop the Experiment',
          message: 'This will stop the ' + name + ' experiment.',
          cancelText: 'Cancel',
          confirmText: 'Stop',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => this.stopExperiment(name),
        });
      },

      // Stops one experiment; one whose VMs minimega could not kill is still
      // running, and the user may force its stop.
      stopExperiment(name, force = false) {
        return this.sendStop(name, force).catch((err) => {
          useErrorNotification(err);
          if (!force && stopCanBeForced(err)) {
            offerForcedStop(this.$buefy, name, () =>
              this.stopExperiment(name, true),
            );
          }
        });
      },

      // sends one stop, its buttons disabled until the server answers
      async sendStop(name, force = false) {
        this.stopping[name] = true;
        try {
          return await experimentsApi.stopExperiment(name, { force });
        } finally {
          delete this.stopping[name];
        }
      },

      del(name) {
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
            this.deleteExperiment(name).catch((err) =>
              useErrorNotification(err),
            );
          },
        });
      },

      // deletes one experiment, its button spinning meanwhile
      async deleteExperiment(name) {
        this.deleting[name] = true;
        try {
          await experimentsApi.deleteExperiment(name);
          this.experiments = this.experiments.filter((exp) => exp.name != name);
        } finally {
          delete this.deleting[name];
        }
      },

      // Takes an action on the selected experiments: one confirm naming those
      // it acts on and those it skips and why, then one outcome summary.
      async bulk(action) {
        const { allowed, skipped } = partitionExperiments(
          action,
          this.selected,
          this.experiments,
          this.actionOpts(),
        );
        if (!allowed.length) return;

        const { label, type } = EXPERIMENT_ACTIONS[action];
        const confirmed = await confirmOperation(this.$buefy, {
          title: `${label} the ${pluralWord(allowed.length, 'Experiment')}`,
          message:
            `This will ${action === 'delete' ? 'DELETE' : label.toLowerCase()} ` +
            `${namedList(allowed, 'experiment')}.`,
          confirmText: label,
          type,
          skipped,
          noun: 'experiment',
        });
        if (!confirmed) return;

        this.selected = [];
        this.runBulk(action, allowed);
      },

      // Sends the action to each experiment still ready for it and sums up the outcome, holding the
      // websocket's toasts until then; `force` forces stops, offered for those whose VMs survived.
      async runBulk(action, names, { force = false } = {}) {
        const send = {
          start: (name) => this.startExperiment(name),
          stop: (name) => this.sendStop(name, force),
          delete: (name) => this.deleteExperiment(name),
        }[action];

        // an experiment may have changed while the confirm was open
        const { allowed, skipped } = partitionExperiments(
          action,
          names,
          this.experiments,
          this.actionOpts(),
        );
        const skippedItems = skipped.flatMap(({ reason, names }) =>
          names.map((name) => ({ name, status: 'skipped', error: reason })),
        );

        allowed.forEach((name) => this.bulkPending.add(name));
        try {
          const results = await Promise.allSettled(allowed.map(send));
          notifyOutcome(this.$buefy, {
            verbPast: EXPERIMENT_ACTIONS[action].verbPast,
            noun: 'experiment',
            items: [...outcomeFromSettled(allowed, results), ...skippedItems],
            onRetry: (retry) => this.runBulk(action, retry, { force }),
          });
          const forceable = allowed.filter(
            (_, i) =>
              action === 'stop' && !force && stopCanBeForced(results[i].reason),
          );
          if (forceable.length) {
            offerForcedStop(this.$buefy, forceable, () =>
              this.runBulk('stop', forceable, { force: true }),
            );
          }
        } finally {
          allowed.forEach((name) => this.bulkPending.delete(name));
        }
      },

      // Creates the experiment from the card, which stays open, its inputs
      // kept, until the server answers; a failure shows in the card.
      async create(request, { start = false } = {}) {
        const attempt = this.createAttempt;
        this.createPending = true;
        this.createError = null;

        try {
          await experimentsApi.createExperiment(request);
          if (this.createAttempt === attempt) this.creating = false;
          if (start) this.start(request.name);
        } catch (err) {
          // the card was closed meanwhile
          if (this.creating && this.createAttempt === attempt) {
            this.createError = errorLine(err);
          } else {
            useErrorNotification(err);
          }
        } finally {
          this.createPending = false;
        }
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
        createPending: false, // the card's create request is under way
        createError: null, // why the card's last create failed
        createAttempt: 0, // counts the card's openings
        experiments: [],
        searchName: '',
        selected: [], // names of the checked experiments
        deleting: {}, // names of the experiments being deleted
        starting: {}, // names of the experiments whose start request is out
        stopping: {}, // names of the experiments whose stop request is out
        loaded: false, // false until the first list arrives
        options: {},
      };
    },
  };
</script>

<style scoped>
  /* a start phenix could not resume shows under the status */
  .exp-interrupted {
    display: flex;
    justify-content: center;
    margin-top: 0.25rem;
  }

  /* the status tag is the button that starts or stops the experiment */
  .exp-status-control {
    border: 0;
    font-family: inherit;
    cursor: pointer;
  }

  .exp-status-control:disabled {
    cursor: not-allowed;
    opacity: 0.7;
  }

  /* the totals at the left, the search and its controls at the right */
  .exp-toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 0.75rem;
    margin-bottom: 0.75rem;
  }

  .exp-toolbar-controls {
    margin-left: auto;
  }

  /* the checkbox column is only as wide as its checkboxes */
  .exp-table :deep(.exp-select-cell) {
    width: 1%;
    padding-left: 0.75em;
    padding-right: 0.25em;
  }

  .exp-table :deep(.exp-select-cell .b-checkbox.checkbox) {
    margin-right: 0;
  }

  .exp-table :deep(.exp-select-cell .control-label) {
    padding-left: 0;
  }

  /* below about 1,280 px the time and the date wrap onto two lines, each whole */
  .exp-start-time > span {
    white-space: nowrap;
  }

  /* one row of buttons, centered */
  .exp-row-actions {
    display: flex;
    justify-content: center;
    gap: 4px;
  }

  .exp-row-actions .button {
    width: 2rem;
  }

  /* below 1,280 px the cells pad less, so the table fits the screen */
  @media screen and (min-width: 769px) and (max-width: 1279px) {
    .exp-table :deep(th:not(.exp-select-cell)),
    .exp-table :deep(td:not(.exp-select-cell)) {
      padding-inline: 0.375em;
    }
  }

  /* one that can't run now is gray, hovered too; its tooltip says why. The
     :not()s outrank the colored buttons' hover rules in _general.scss. */
  .exp-row-actions
    .button[disabled]:not(.is-loading):not(.is-outlined):not(.is-inverted) {
    border-color: transparent;
    background-color: #5a5a5a;
    color: #a8a8a8;
    opacity: 1;
  }

  .exp-row-actions .button.is-loading[disabled] {
    opacity: 1;
  }
</style>
