<template>
  <section
    :class="['exp-summary', { 'is-collapsed': collapsed }]"
    aria-labelledby="exp-summary-name">
    <div class="exp-summary-head">
      <div class="exp-summary-title">
        <p class="exp-summary-kind">Experiment</p>
        <h1 id="exp-summary-name" class="exp-summary-name">{{ name }}</h1>
      </div>
      <p v-if="collapsed" class="exp-summary-brief">
        <template v-for="(part, index) in brief" :key="part"
          ><span v-if="index > 0" aria-hidden="true"> · </span
          ><span class="exp-summary-brief-part">{{ part }}</span></template
        >
      </p>
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
        <router-link
          v-if="builderLink && roleAllowed('builder', 'use')"
          class="button is-light is-small exp-summary-builder"
          :to="builderLink">
          <b-icon icon="pencil" size="is-small" />
          <span>Open in Builder</span>
        </router-link>
        <span
          v-if="stopped"
          class="tag is-light is-medium exp-summary-status exp-summary-stopped">
          <b-icon icon="stop" size="is-small" />
          <span>Stopped</span>
        </span>
        <span v-else class="tag is-success is-medium exp-summary-status">
          <span>Running</span>
        </span>
        <template v-if="delayed > 0">
          <b-tooltip
            :label="delayedTip"
            type="is-light"
            position="is-bottom"
            multilined
            :delay="500">
            <span
              class="tag is-info is-medium exp-summary-delayed"
              role="note"
              :aria-label="delayedText"
              aria-describedby="exp-summary-delayed-tip">
              <b-icon icon="hourglass-half" size="is-small" />
              <span>{{ delayedText }}</span>
            </span>
          </b-tooltip>
          <span id="exp-summary-delayed-tip" class="is-sr-only">{{
            delayedTip
          }}</span>
        </template>
      </div>
      <div v-if="$slots.end" class="exp-summary-end">
        <slot name="end" />
      </div>
    </div>
    <dl
      v-show="!collapsed"
      id="exp-summary-facts"
      :class="[
        'exp-summary-facts',
        { 'has-start': startTile, 'has-hosts': hostsTile },
      ]">
      <div class="exp-fact exp-fact-scenarios">
        <dt>
          <b-icon icon="file-code" size="is-small" />
          {{ scenarios.length > 1 ? 'Scenarios' : 'Scenario' }}
        </dt>
        <dd
          v-if="scenarios.length"
          class="exp-fact-scenario-list"
          :title="scenarios.join(', ')">
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
      <div v-if="hostsTile" class="exp-fact exp-fact-hosts">
        <dt>
          <b-icon icon="server" size="is-small" />
          {{ stopped ? 'Scheduled hosts' : 'Hosts' }}
        </dt>
        <template v-if="hosts">
          <dd>{{ hosts.length }}</dd>
          <dd v-if="hosts.length" class="exp-fact-host-names">
            {{ hostNames.shown.join(', ')
            }}<template v-if="hostNames.more > 0"
              >,
              <b-tooltip
                :label="hosts.join(', ')"
                type="is-light"
                position="is-bottom"
                multilined
                :delay="500">
                <span class="exp-fact-more" tabindex="0"
                  >+{{ hostNames.more }} more</span
                >
              </b-tooltip>
              <span class="is-sr-only"
                >: {{ hosts.slice(hostNames.shown.length).join(', ') }}</span
              ></template
            >
          </dd>
        </template>
        <dd v-else class="exp-fact-none">
          {{ hostsFailed ? 'Unknown' : 'Loading…' }}
        </dd>
      </div>
      <div class="exp-fact exp-fact-vms">
        <dt><b-icon icon="desktop" size="is-small" /> VMs</dt>
        <dd>{{ vmsText }}</dd>
        <dd v-if="delayed > 0" class="exp-fact-delayed">
          {{ delayed }} delayed
        </dd>
      </div>
      <div class="exp-fact exp-fact-vlans">
        <dt><b-icon icon="network-wired" size="is-small" /> VLANs</dt>
        <dd>{{ (experiment.vlans ?? []).length }}</dd>
        <dd v-if="vlansLink" class="exp-fact-go-cell">
          <b-tooltip
            label="See the VLAN IDs of all experiments"
            type="is-light"
            position="is-bottom"
            multilined>
            <router-link
              :to="vlansLink"
              class="button is-small is-light exp-fact-go"
              data-testid="summary-vlans"
              :aria-label="`See the VLANs of ${name} in the cluster`">
              <b-icon icon="arrow-right" size="is-small" aria-hidden="true" />
            </router-link>
          </b-tooltip>
        </dd>
      </div>
      <div v-if="startTile" class="exp-fact exp-fact-started">
        <dt><b-icon icon="clock" size="is-small" /> Started</dt>
        <dd><time-with-age :time="experiment.start_time" /></dd>
      </div>
    </dl>
  </section>
</template>

<script>
  import { configViewRoute } from '@/utils/configCache.js';
  import TimeWithAge from '@/components/TimeWithAge.vue';
  import { plural } from '@/utils/plural.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { countdownText, useCountdownClock } from './delayCountdown.js';
  import {
    builderRoute,
    experimentScenarios,
    hostsShown,
  } from './experimentHeader.js';

  // how many host names the hosts tile shows before "+N more"
  const HOSTS_SHOWN = 3;

  // what the tag of the VMs that wait for a delayed start means
  const DELAYED_TIP =
    'These VMs have a delayed start and did not start yet. Each VM starts ' +
    'after its timer, when its C2 hosts check in, or when a user starts it.';

  // The experiment's name, links to its state of health, SCORCH runs and
  // Builder draft, its status and VMs that wait for a delayed start, and as
  // labeled tiles its scenarios, topology, hosts, counts and start. A stopped
  // experiment shows the hosts its schedule names, and no start.
  // Collapsed, it shows its counts on one line in place of the tiles.
  export default {
    name: 'ExperimentSummary',
    components: { TimeWithAge },
    props: {
      // the experiment's name, shown before the experiment has loaded
      name: { type: String, default: '' },
      experiment: { type: Object, default: () => ({}) },
      // how many of its VMs are running, null until the VM list says
      running: { type: Number, default: null },
      // how many of its VMs wait for a delayed start, null until the VM list says
      delayed: { type: Number, default: null },
      // when the next VM that waits for a timer starts, in milliseconds, or null
      nextStart: { type: Number, default: null },
      // the cluster hosts that run its VMs, null until the VM list says; the
      // hosts tile is left out when this is not given
      hosts: { type: Array, default: undefined },
      // whether the hosts could not be loaded
      hostsFailed: { type: Boolean, default: false },
      // how many VMs it has, when not experiment.vm_count (which a stopped
      // experiment's search narrows); null while not known
      vmCount: { type: Number, default: undefined },
      // whether the experiment is stopped
      stopped: { type: Boolean, default: false },
      // whether only the head shows, with the counts on one line for the tiles
      collapsed: { type: Boolean, default: false },
      // how many apps it has, which the one-line counts name
      appCount: { type: Number, default: 0 },
    },
    setup(props) {
      return {
        // the shared clock of the countdown to the next timer VM start
        now: useCountdownClock(
          () => props.delayed > 0 && props.nextStart !== null,
        ),
        configViewRoute,
        delayedTip: DELAYED_TIP,
        roleAllowed,
      };
    },
    computed: {
      scenarios() {
        return experimentScenarios(this.experiment);
      },
      builderLink() {
        return builderRoute(this.experiment);
      },
      // the VLANs page, showing this experiment
      vlansLink() {
        if (!this.name || !roleAllowed('vlans', 'list', this.name)) return null;
        return { name: 'vlans', query: { experiment: this.name } };
      },
      hostsTile() {
        return this.hosts !== undefined;
      },
      startTile() {
        return !this.stopped && !!this.experiment.start_time;
      },
      hostNames() {
        return hostsShown(this.hosts, HOSTS_SHOWN);
      },
      // how many VMs it has, null while not known
      vmTotal() {
        return this.vmCount === undefined
          ? (this.experiment.vm_count ?? 0)
          : this.vmCount;
      },
      // "18 running of 25", or the total alone until the running count is known
      vmsText() {
        const total = this.vmTotal;
        if (total === null) return 'Unknown';
        return this.running === null
          ? `${total}`
          : `${this.running} running of ${total}`;
      },
      // the collapsed head's counts: "18 of 25 VMs running", "3 VLANs",
      // "2 hosts", "4 apps"; a count not yet known is left out
      brief() {
        const parts = [];
        const total = this.vmTotal;
        if (total !== null) {
          parts.push(
            this.running === null
              ? plural(total, 'VM')
              : `${this.running} of ${plural(total, 'VM')} running`,
          );
        }
        parts.push(plural((this.experiment.vlans ?? []).length, 'VLAN'));
        if (this.hosts) {
          parts.push(
            plural(this.hosts.length, this.stopped ? 'scheduled host' : 'host'),
          );
        }
        if (this.appCount > 0) parts.push(plural(this.appCount, 'app'));
        return parts;
      },
      // "3 delayed VMs waiting"
      delayedText() {
        const waiting = `${plural(this.delayed, 'delayed VM')} waiting`;
        if (this.nextStart === null) return waiting;
        return `${waiting}, next ${countdownText(this.nextStart, this.now)}`;
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

  /* collapsed: one row, its kind before its name */
  .exp-summary.is-collapsed {
    padding: 0.4rem 0.75rem 0.4rem 1rem;
  }

  .is-collapsed .exp-summary-head {
    flex-wrap: wrap;
    gap: 0.5rem 1rem;
  }

  .is-collapsed .exp-summary-title {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
  }

  .content .is-collapsed h1.exp-summary-name {
    font-size: 1.25rem;
  }

  .content p.exp-summary-brief {
    flex: 1;
    min-width: 0;
    margin: 0;
    color: #e6e6e6;
    font-variant-numeric: tabular-nums;
  }

  .exp-summary-brief-part {
    white-space: nowrap;
  }

  /* what its page adds after the status, such as the expand toggle */
  .exp-summary-end {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex: none;
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
    margin-left: auto;
  }

  .exp-summary-status.tag.is-success,
  .exp-summary-status.tag.is-light,
  .exp-summary-delayed.tag.is-info {
    gap: 0.25rem;
    flex: none;
  }

  /* two rows of tiles: what it was made from, then its counts and start */
  .content dl.exp-summary-facts {
    flex: 1;
    display: grid;
    grid-template-columns: repeat(12, minmax(0, 1fr));
    grid-auto-rows: auto;
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

  .exp-fact-hosts,
  .exp-fact-vms {
    grid-column: span 5;
  }

  /* a count of a few digits */
  .exp-fact-vlans {
    grid-column: span 2;
  }

  .exp-summary-facts:not(.has-hosts):not(.has-start) .exp-fact-vms {
    grid-column: span 10;
  }

  /* with a start, the hosts move up beside the topology */
  .has-start.has-hosts .exp-fact-scenarios {
    grid-column: span 5;
  }

  .has-start .exp-fact-hosts {
    grid-column: span 3;
  }

  /* the VMs tile is wider, for its running count */
  .has-start .exp-fact-vms {
    grid-column: span 4;
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
    color: #e6e6e6;
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

  /* the link text color: 7.4:1 on the tile */
  .content .exp-fact dd a {
    color: #d0ecf3;
  }

  .content .exp-fact dd a:hover {
    color: white;
    text-decoration: underline;
  }

  /* the count, then the arrow to the VLANs page */
  .exp-fact.exp-fact-vlans {
    display: grid;
    grid-template-columns: auto 1fr;
    align-items: center;
    column-gap: 0.4rem;
  }

  .exp-fact-vlans dt {
    grid-column: 1 / -1;
  }

  .content .exp-fact dd.exp-fact-go-cell {
    overflow: visible;
  }

  .exp-fact-go.button {
    height: 1.5rem;
    padding: 0 0.4rem;
  }

  /* a narrow tile wraps the start between its date, time and age */
  .content .exp-fact-started dd {
    white-space: normal;
  }

  /* the scenarios wrap, cut off with an ellipsis past three lines */
  .content .exp-fact dd.exp-fact-scenario-list {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    white-space: normal;
    overflow-wrap: anywhere;
  }

  /* the first few hosts, wrapped; "+N more" lists the rest */
  .content .exp-fact dd.exp-fact-host-names {
    white-space: normal;
    overflow-wrap: anywhere;
    font-size: 0.85rem;
    font-weight: 400;
  }

  .exp-fact-more {
    text-decoration: underline dotted;
    cursor: help;
  }

  .content .exp-fact dd.exp-fact-none {
    font-style: italic;
    font-weight: 400;
    color: #e6e6e6;
  }
</style>
