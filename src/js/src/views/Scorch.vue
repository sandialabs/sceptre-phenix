<template>
  <div class="content">
    <b-field label-for="scorch-experiment-search" label="Find an experiment">
      <b-input
        id="scorch-experiment-search"
        v-model="searchName"
        type="search"
        :compat-fallthrough="false" />
    </b-field>
    <b-table :data="filteredExperiments" default-sort="name">
      <b-table-column field="name" label="Experiment" sortable v-slot="props">
        <router-link
          :to="{ name: 'scorchruns', params: { id: props.row.name } }"
          >{{ props.row.name }}</router-link
        >
      </b-table-column>
      <b-table-column field="status" label="Experiment status" v-slot="props">{{
        props.row.status
      }}</b-table-column>
      <b-table-column label="Scorch runs" v-slot="props">
        <p
          v-for="execution in props.row.executions"
          :key="execution.executionID">
          Run {{ execution.runID
          }}{{ execution.name ? ' (' + execution.name + ')' : '' }}:
          {{
            execution.wait === 'breakpoint'
              ? 'Waiting at breakpoint'
              : execution.state
          }}
        </p>
        <span v-if="!props.row.executions.length">Idle</span>
      </b-table-column>
      <b-table-column label="Controls" v-slot="props">
        <router-link
          class="button is-small"
          :to="{ name: 'scorchruns', params: { id: props.row.name } }"
          >Manage runs and terminals</router-link
        >
        <button
          v-if="roleAllowed('experiments/start', 'update', props.row.name)"
          class="button is-small"
          :disabled="['starting', 'stopping'].includes(props.row.status)"
          @click="expControl(props.row)">
          {{ props.row.running ? 'Stop experiment' : 'Start experiment' }}
        </button>
      </b-table-column>
    </b-table>
  </div>
</template>

<script>
  import axiosInstance from '@/utils/axios.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';
  import { roleAllowed } from '@/utils/rbac.js';

  export default {
    data() {
      return { experiments: [], searchName: '', poll: null, refreshing: false };
    },
    created() {
      addWsHandler(this.handle);
      this.updateExperiments();
      this.poll = setInterval(() => this.updateExperiments(), 3000);
    },
    beforeUnmount() {
      removeWsHandler(this.handle);
      clearInterval(this.poll);
    },
    computed: {
      filteredExperiments() {
        return this.experiments.filter((e) =>
          e.name.toLowerCase().includes(this.searchName.toLowerCase()),
        );
      },
    },
    methods: {
      roleAllowed,
      async updateExperiments() {
        if (this.refreshing) return;
        this.refreshing = true;
        try {
          const { data } = await axiosInstance.get('experiments');
          const experiments = await Promise.all(
            data.experiments
              .filter((e) => roleAllowed('experiments', 'get', e.name))
              .map(async (e) => {
                const { data: apps } = await axiosInstance.get(
                  `experiments/${e.name}/apps`,
                );
                if (!('scorch' in apps)) return null;
                const { data: snapshot } = await axiosInstance.get(
                  `experiments/${e.name}/scorch/pipelines`,
                );
                return {
                  ...e,
                  executions: Object.values(snapshot.executions ?? {}),
                };
              }),
          );
          this.experiments = experiments.filter(Boolean);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.refreshing = false;
        }
      },
      expControl(exp) {
        const action = exp.running ? 'stop' : 'start';
        this.$buefy.dialog.confirm({
          title: `${action === 'stop' ? 'Stop' : 'Start'} experiment`,
          message: `This will ${action} ${exp.name}${action === 'stop' ? ' and cancel all active Scorch runs after their cleanup' : ''}.`,
          onConfirm: () =>
            axiosInstance
              .post(`experiments/${exp.name}/${action}`)
              .catch(useErrorNotification),
        });
      },
      handle(msg) {
        if (['apps/scorch', 'experiment'].includes(msg.resource.type))
          this.updateExperiments();
      },
    },
  };
</script>
