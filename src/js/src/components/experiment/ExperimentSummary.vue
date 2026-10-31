<template>
  <section class="exp-summary" aria-labelledby="exp-summary-name">
    <div class="exp-summary-head">
      <div class="exp-summary-title">
        <p class="exp-summary-kind">Experiment</p>
        <h1 id="exp-summary-name" class="exp-summary-name">{{ name }}</h1>
      </div>
      <div class="exp-summary-actions">
        <template v-if="roleAllowed('experiments', 'get', name)">
          <b-tooltip label="view state of health" type="is-light" :delay="500">
            <router-link
              :aria-label="`View state of health for experiment ${name}`"
              class="button is-light is-small"
              :to="{ name: 'soh', params: { id: name } }">
              <b-icon icon="heartbeat" size="is-small" />
            </router-link>
          </b-tooltip>
          <b-tooltip label="view SCORCH runs" type="is-light" :delay="500">
            <router-link
              :aria-label="`View SCORCH runs for experiment ${name}`"
              class="button is-light is-small"
              :to="{ name: 'scorchruns', params: { id: name } }">
              <b-icon icon="fire" size="is-small" />
            </router-link>
          </b-tooltip>
        </template>
        <span class="tag is-success is-medium exp-summary-status">
          <b-icon icon="play" size="is-small" />
          <span>Running</span>
        </span>
      </div>
    </div>
    <dl
      :class="['exp-summary-facts', { 'has-start': !!experiment.start_time }]">
      <div class="exp-fact exp-fact-scenarios">
        <dt>
          <b-icon icon="file-code" size="is-small" />
          {{ scenarios.length > 1 ? 'Scenarios' : 'Scenario' }}
        </dt>
        <dd v-if="scenarios.length">
          <template v-for="(scenario, index) in scenarios" :key="scenario">
            <router-link
              v-if="configLinked('Scenario', scenario)"
              :to="configViewRoute('Scenario', scenario)"
              >{{ scenario }}</router-link
            ><template v-else>{{ scenario }}</template
            ><template v-if="index < scenarios.length - 1">, </template>
          </template>
        </dd>
        <dd v-else class="exp-fact-none">None</dd>
      </div>
      <div class="exp-fact exp-fact-topology">
        <dt><b-icon icon="share-nodes" size="is-small" /> Topology</dt>
        <dd v-if="experiment.topology">
          <router-link
            v-if="configLinked('Topology', experiment.topology)"
            :to="configViewRoute('Topology', experiment.topology)"
            >{{ experiment.topology }}</router-link
          >
          <template v-else>{{ experiment.topology }}</template>
        </dd>
        <dd v-else class="exp-fact-none">None</dd>
      </div>
      <div class="exp-fact exp-fact-count exp-fact-vms">
        <dt><b-icon icon="desktop" size="is-small" /> VMs</dt>
        <dd>{{ vmsText }}</dd>
      </div>
      <div class="exp-fact exp-fact-count exp-fact-vlans">
        <dt><b-icon icon="network-wired" size="is-small" /> VLANs</dt>
        <dd>{{ (experiment.vlans ?? []).length }}</dd>
      </div>
      <div v-if="experiment.start_time" class="exp-fact exp-fact-started">
        <dt><b-icon icon="clock" size="is-small" /> Started</dt>
        <dd><time-with-age :time="experiment.start_time" /></dd>
      </div>
    </dl>
  </section>
</template>

<script>
  import { configViewRoute } from '@/utils/configCache.js';
  import TimeWithAge from '@/components/TimeWithAge.vue';
  import { roleAllowed } from '@/utils/rbac.js';
  import { experimentScenarios } from './experimentHeader.js';

  // The running experiment's name, links to its state of health and SCORCH
  // runs, and as labeled tiles its scenarios and topology, counts and start.
  export default {
    name: 'ExperimentSummary',
    components: { TimeWithAge },
    props: {
      // the experiment's name, shown before the experiment has loaded
      name: { type: String, default: '' },
      experiment: { type: Object, default: () => ({}) },
      // how many of its VMs are running, null until the VM list says
      running: { type: Number, default: null },
    },
    setup() {
      return { configViewRoute, roleAllowed };
    },
    computed: {
      scenarios() {
        return experimentScenarios(this.experiment);
      },
      // "18 running of 25", or the total alone until the running count is known
      vmsText() {
        const total = this.experiment.vm_count ?? 0;
        return this.running === null
          ? `${total}`
          : `${this.running} running of ${total}`;
      },
    },
    methods: {
      // whether a scenario or topology links to its config, as on the
      // Experiments page
      configLinked(kind, name) {
        return (
          !!name &&
          roleAllowed('configs', 'list') &&
          roleAllowed('configs', 'get', `${kind}/${name}`)
        );
      },
    },
  };
</script>

<style scoped>
  .exp-summary {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    background-color: #3d3d3d;
    border: 1px solid #686868;
    border-left: 4px solid #da622d;
    border-radius: 8px;
    padding: 0.6rem 0.75rem 0.75rem 1rem;
  }

  .exp-summary-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
  }

  .exp-summary-title {
    min-width: 0;
  }

  .exp-summary-kind {
    margin: 0 !important;
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.08em;
    line-height: 1.2;
    text-transform: uppercase;
    color: #dbdbdb;
  }

  .content h1.exp-summary-name {
    margin: 0;
    font-size: 1.5rem;
    line-height: 1.2;
    color: white;
    overflow-wrap: anywhere;
  }

  /* its state of health and SCORCH links, left of its status */
  .exp-summary-actions {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex: none;
  }

  .exp-summary-status.tag.is-success {
    gap: 0.25rem;
    flex: none;
  }

  /* two rows of tiles: what it was made from, then its counts and start */
  .content dl.exp-summary-facts {
    flex: 1;
    display: grid;
    grid-template-columns: repeat(12, minmax(0, 1fr));
    grid-auto-rows: 1fr;
    gap: 0.5rem;
    margin: 0;
    container-type: inline-size;
  }

  .exp-fact-scenarios {
    grid-column: span 8;
  }

  .exp-fact-topology {
    grid-column: span 4;
  }

  .exp-fact-count {
    grid-column: span 6;
  }

  /* the VMs tile is wider, for its running count */
  .has-start .exp-fact-vms {
    grid-column: span 4;
  }

  .has-start .exp-fact-vlans {
    grid-column: span 2;
  }

  .exp-fact-started {
    grid-column: span 6;
  }

  /* a wide card gives the start its time and age on one line, the counts less */
  @container (min-width: 44rem) {
    .has-start .exp-fact-vms {
      grid-column: span 3;
    }

    .exp-fact-started {
      grid-column: span 7;
    }
  }

  .exp-fact {
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 0.15rem;
    min-width: 0;
    padding: 0.4rem 0.6rem;
    background-color: #484848;
    border: 1px solid #686868;
    border-radius: 6px;
  }

  .exp-fact dt {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #dbdbdb;
  }

  /* the side padding leaves room for a focused link's outline */
  .content .exp-fact dd {
    margin: 0 -4px;
    padding: 0 4px;
    color: white;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* lighter than the page's links: 5.2:1 on the tile */
  .content .exp-fact dd a {
    color: #7fcfe0;
  }

  .content .exp-fact dd a:hover {
    color: white;
    text-decoration: underline;
  }

  /* a narrow tile wraps the start between its date, time and age */
  .content .exp-fact-started dd {
    white-space: normal;
  }

  .content .exp-fact dd.exp-fact-none {
    font-style: italic;
    font-weight: 400;
    color: #dbdbdb;
  }
</style>
