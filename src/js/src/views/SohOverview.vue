<template>
  <div class="content soh-overview">
    <div class="toolbar">
      <div class="buttons has-addons filters mb-0" role="group">
        <button
          v-for="f in filters"
          :key="f.key"
          type="button"
          class="button is-light"
          :class="{ 'is-selected': filter === f.key }"
          :aria-pressed="filter === f.key"
          @click="filter = f.key">
          {{ f.label }}
          <span
            class="count"
            :class="{ attn: f.key === 'attention' && counts.attention > 0 }"
            >{{ counts[f.key] }}</span
          >
        </button>
      </div>
      <div v-if="loaded" class="summary">
        SOH active on
        <b>{{ totals.active }} of {{ totals.total }}</b>
        experiments ·
        <b :class="{ 't-warn': totals.hostsFailing > 0 }">{{
          totals.hostsFailing
        }}</b>
        {{ pluralWord(totals.hostsFailing, 'host') }} failing checks ·
        <b :class="{ 't-bad': totals.vmsDown > 0 }">{{ totals.vmsDown }}</b>
        {{ pluralWord(totals.vmsDown, 'VM') }} down
      </div>
      <b-field class="mb-0">
        <b-autocomplete
          placeholder="Find an Experiment"
          v-model="searchName"
          icon="search"
          :data="searchSuggestions"
          @select="(option) => (searchName = option ?? searchName)">
          <template #empty> No results found </template>
        </b-autocomplete>
        <p v-if="searchName" class="control">
          <button
            class="button input-button"
            aria-label="Clear search"
            @click="searchName = ''">
            <b-icon icon="window-close"></b-icon>
          </button>
        </p>
      </b-field>
    </div>

    <b-table
      :data="rows"
      detailed
      detail-key="name"
      v-model:opened-detailed="opened"
      :has-detailed-visible="(row) => problemCount(row) > 0"
      :row-class="rowClass"
      :paginated="table.isPaginated"
      :per-page="table.perPage"
      v-model:current-page="table.currentPage"
      :pagination-simple="table.isPaginationSimple"
      :pagination-size="table.paginationSize"
      default-sort="verdict">
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
        <div class="exp-name">
          <router-link
            :to="{ name: 'experiment', params: { id: props.row.name } }">
            {{ props.row.name }}
          </router-link>
          <div class="sub">
            {{ props.row.scenario || 'no scenario' }} ·
            {{ props.row.vms.total }}
            {{ pluralWord(props.row.vms.total, 'VM') }}
          </div>
        </div>
      </b-table-column>

      <b-table-column
        field="status"
        label="Experiment Status"
        header-class="sort-inline"
        sortable
        centered
        v-slot="props">
        <div v-if="props.row.status === 'starting'" class="starting">
          <b-progress
            size="is-medium"
            type="is-warning"
            show-value
            :value="Math.round((props.row.percent ?? 0) * 100)"
            format="percent"></b-progress>
        </div>
        <span
          v-else
          class="tag is-medium"
          :class="statusDecorator(props.row.status)">
          {{ props.row.status }}
        </span>
      </b-table-column>

      <b-table-column
        field="verdict"
        label="SoH"
        header-class="sort-inline"
        sortable
        centered
        :custom-sort="sortByVerdict"
        v-slot="props">
        <span
          class="tag is-medium verdict"
          :class="verdictInfo(props.row).type"
          :title="verdictTitle(props.row)">
          <b-icon :icon="verdictInfo(props.row).icon" size="is-small" />
          <span>{{ verdictLabel(props.row) }}</span>
        </span>
      </b-table-column>

      <b-table-column label="VM Health" v-slot="props">
        <div class="health-cell">
          <template v-if="segments(props.row).length">
            <div
              class="health-bar"
              role="img"
              :aria-label="healthCaption(props.row)">
              <span
                v-for="seg in segments(props.row)"
                :key="seg.key"
                :style="{ flexGrow: seg.count, background: seg.color }"
                :title="`${seg.count} ${seg.label}`"></span>
            </div>
            <div class="sub health-caption">
              <span v-for="(seg, i) in segments(props.row)" :key="seg.key"
                ><template v-if="i > 0"> · </template
                ><b :class="`seg-${seg.key}`">{{ seg.count }}</b>
                {{ seg.label }}</span
              >
            </div>
          </template>
          <template v-else>
            <div class="health-bar is-empty"></div>
            <div class="sub">{{ healthCaption(props.row) }}</div>
          </template>
        </div>
      </b-table-column>

      <b-table-column
        field="checks"
        label="Checks"
        sortable
        :custom-sort="sortByFailing"
        v-slot="props">
        <template v-if="active(props.row)">
          <div class="big" :class="toneClass(props.row)">
            {{ props.row.checks.passing }}
            <span class="of">/ {{ props.row.checks.total }}</span>
          </div>
          <div class="sub">
            {{
              props.row.checks.failing
                ? `${props.row.checks.failing} failing`
                : 'all passing'
            }}
          </div>
        </template>
        <span v-else class="dash">—</span>
      </b-table-column>

      <b-table-column label="Reachability" v-slot="props">
        <template
          v-if="active(props.row) && reachabilityShare(props.row) !== null">
          <div
            class="big"
            :class="{
              't-bad': reachabilityShare(props.row) < healthyReachability,
            }">
            {{ Math.floor(reachabilityShare(props.row) * 100) }}%
          </div>
          <div class="sub">
            {{ props.row.reachability.passing }}/{{
              props.row.reachability.total
            }}
            pairs
          </div>
        </template>
        <span
          v-else-if="active(props.row)"
          class="sub"
          title="The soh app is not testing reachability">
          not tested
        </span>
        <span v-else class="dash">—</span>
      </b-table-column>

      <b-table-column
        :label="`Failing, last ${trendRuns} runs`"
        header-class="trend-head"
        v-slot="props">
        <svg
          v-if="active(props.row) && trend(props.row).length > 1"
          class="sparkline"
          :width="spark.width"
          :height="spark.height"
          :viewBox="`0 0 ${spark.width} ${spark.height}`"
          role="img"
          :aria-label="trendLabel(props.row)">
          <title>{{ trendLabel(props.row) }}</title>
          <polygon
            :points="sparkArea(props.row)"
            :fill="toneColor(props.row)"
            fill-opacity="0.25" />
          <polyline
            :points="sparkLine(props.row)"
            fill="none"
            :stroke="toneColor(props.row)"
            stroke-width="1.5"
            stroke-linejoin="round" />
          <circle
            :cx="sparkLast(props.row)[0]"
            :cy="sparkLast(props.row)[1]"
            r="2.5"
            :fill="toneColor(props.row)" />
        </svg>
        <span
          v-else-if="active(props.row)"
          class="sub"
          title="The trend shows once the soh app has run twice">
          after next run
        </span>
        <span v-else class="dash">—</span>
      </b-table-column>

      <b-table-column
        field="last_run"
        label="Last Check"
        sortable
        v-slot="props">
        <template v-if="active(props.row) && props.row.last_run">
          <div class="num" :title="props.row.last_run">
            {{ clockTime(props.row.last_run) }}
          </div>
          <div class="sub">
            <span v-if="props.row.soh_running" class="t-warn"
              >running now…</span
            >
            <template v-else>{{ ago(props.row.last_run) }}</template>
          </div>
        </template>
        <div v-else class="sub note">{{ lastCheckNote(props.row) }}</div>
      </b-table-column>

      <b-table-column label="Actions" centered v-slot="props">
        <div class="actions">
          <b-tooltip :label="runLabel(props.row)" type="is-dark">
            <button
              class="button is-light is-small"
              :class="{ 'is-loading': props.row.pending }"
              :disabled="!!runBlocked(props.row)"
              :aria-label="runLabel(props.row)"
              @click="runSoh(props.row)">
              <b-icon icon="play" />
            </button>
          </b-tooltip>
          <b-tooltip label="View topology graph" type="is-dark">
            <router-link
              class="button is-light is-small"
              aria-label="View topology graph"
              :to="{ name: 'soh', params: { id: props.row.name } }">
              <b-icon icon="heartbeat" />
            </router-link>
          </b-tooltip>
        </div>
      </b-table-column>

      <template #detail="props">
        <div class="detail-head">
          <h4>
            {{ problemCount(props.row) }}
            {{ pluralWord(problemCount(props.row), 'problem') }} in
            {{ props.row.name }}
          </h4>
          <div class="buttons mb-0">
            <router-link
              class="button is-light is-small"
              :to="{ name: 'soh', params: { id: props.row.name } }">
              <b-icon icon="heartbeat" size="is-small" />
              <span>Open topology graph</span>
            </router-link>
            <button
              class="button is-light is-small"
              :disabled="!!runBlocked(props.row)"
              :title="runLabel(props.row)"
              @click="runSoh(props.row)">
              <b-icon icon="play" size="is-small" />
              <span>Run SOH</span>
            </button>
          </div>
        </div>
        <table class="problems">
          <thead>
            <tr>
              <th>Host</th>
              <th>Check</th>
              <th>Target</th>
              <th>Result</th>
              <th class="has-text-right">Checked</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in shownProblems(props.row)" :key="p.id">
              <td>
                <b>{{ p.host }}</b>
              </td>
              <td>
                <span class="kind">{{ p.check }}</span>
              </td>
              <td class="mono">{{ p.target || '—' }}</td>
              <td class="result">{{ p.error }}</td>
              <td class="has-text-right num">
                {{ p.time ? clockTime(p.time) : 'live' }}
              </td>
            </tr>
          </tbody>
        </table>
        <div class="hint mt-2">
          <template v-if="hiddenProblems(props.row) > 0">
            <a role="button" @click="showAll[props.row.name] = true"
              >Show all {{ problems(props.row).length }} problems</a
            >
            ·
          </template>
          failing checks first, newest first, then VMs that are down
          <template v-if="truncated(props.row)">
            · this list stops at the newest
            {{ props.row.failing_checks.length }} failing checks; the topology
            graph has them all
          </template>
        </div>
      </template>
    </b-table>

    <div v-if="rows.length" class="legend" aria-label="VM health colors">
      <span v-for="item in legend" :key="item.label">
        <i :style="{ background: item.color }"></i>{{ item.label }}
      </span>
    </div>
  </div>
</template>

<script>
  import axiosInstance from '@/utils/axios.js';
  import { debug } from '@/utils/debug.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif.js';
  import { inForeground } from '@/utils/foreground.js';
  import { createLiveRows } from '@/utils/liveRows.js';
  import { pageFetchers } from '@/utils/pageData.js';
  import { plural, pluralWord } from '@/utils/plural.js';
  import {
    createPageLoader,
    loadingText,
    stillLoading,
  } from '@/utils/pageLoader.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { relativeTime } from '@/utils/relativeTime.js';
  import {
    FILTERS,
    HEALTHY_REACHABILITY,
    VERDICTS,
    VM_COLORS,
    clockTime,
    filterCounts,
    overview,
    problemCount,
    problems,
    reachabilityShare,
    sohActive,
    sparkPoints,
    trend,
    verdict,
    verdictLabel,
    vmSegments,
  } from '@/utils/sohOverview.js';
  import { useTable } from '@/utils/useTable.js';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';

  // Several websocket messages often arrive together (a VM restarting, an
  // experiment starting); one summary request covers them.
  export const RELOAD_DELAY_MS = 750;
  // how often the summary reloads while an soh run is writing results
  const RUNNING_POLL_MS = 15000;
  // how long a Run SOH button spins waiting for the run to be reported
  const PENDING_TIMEOUT_MS = 15000;
  // problems listed in an expanded row before "Show all"
  export const PROBLEMS_SHOWN = 6;
  // VM actions that change a VM's state
  const VM_STATE_ACTIONS = new Set([
    'start',
    'stop',
    'redeployed',
    'reset',
    'shutdown',
    'delete',
    'update',
  ]);

  // lighter than Bulma's danger, warning and success so the marks and text
  // keep 4.5:1 contrast on the dark rows
  const TONES = {
    degraded: '#ff9aac',
    failing: '#ffad33',
    healthy: '#5fd3a0',
  };

  export default {
    setup() {
      return { ...useTable(), roleAllowed, pluralWord };
    },

    data() {
      return {
        experiments: [],
        loaded: false,
        filter: 'all',
        searchName: '',
        opened: [],
        showAll: {},
        filters: FILTERS,
        healthyReachability: HEALTHY_REACHABILITY,
        trendRuns: 12,
        spark: { width: 110, height: 26 },
        legend: [
          { label: 'Running', color: VM_COLORS.ok },
          { label: 'Checks failing', color: VM_COLORS.failing },
          { label: 'Not running', color: VM_COLORS.down },
          { label: 'Not booted', color: VM_COLORS.notboot },
          { label: 'Delayed start', color: VM_COLORS.delayed },
          { label: 'External / HIL', color: VM_COLORS.external },
        ],
      };
    },

    created() {
      addWsHandler(this.handleWs);
      this.liveRows = createLiveRows();
      this.loader = createPageLoader({
        key: 'soh',
        fetch: pageFetchers.soh,
        apply: (experiments, { requestedAt }) => {
          this.experiments = this.liveRows.merge(
            experiments,
            this.experiments,
            requestedAt,
          );
          this.loaded = true;
        },
      });
      this.loader.start();
      this.pollTimer = setInterval(() => this.pollRunning(), RUNNING_POLL_MS);
    },

    beforeUnmount() {
      removeWsHandler(this.handleWs);
      clearTimeout(this.reloadTimer);
      clearInterval(this.pollTimer);
      this.experiments.forEach((exp) => this.settle(exp));
      this.loader.stop();
    },

    computed: {
      counts() {
        return filterCounts(this.experiments);
      },

      totals() {
        return overview(this.experiments);
      },

      // rows matching the filter and the search (a plain substring match)
      rows() {
        const search = this.searchName.toLowerCase();
        const shown = FILTERS.find((f) => f.key === this.filter)?.verdicts;
        return this.experiments.filter(
          (exp) =>
            exp.name.toLowerCase().includes(search) &&
            (!shown || shown.includes(verdict(exp))),
        );
      },

      searchSuggestions() {
        return this.rows.map((exp) => exp.name);
      },

      emptyText() {
        if (stillLoading(this.loaded, this.experiments)) {
          return loadingText('state of health');
        }
        if (this.experiments.length === 0) return 'No experiments found';
        if (this.searchName) {
          return 'No experiments match your search';
        }
        const label = FILTERS.find((f) => f.key === this.filter)?.label;
        return `No experiments in ${label}`;
      },
    },

    methods: {
      clockTime,
      problemCount,
      problems,
      reachabilityShare,
      trend(row) {
        return trend(row, this.trendRuns);
      },
      verdictLabel(row) {
        return verdictLabel(row);
      },
      active: sohActive,
      segments: vmSegments,

      verdictInfo(row) {
        return VERDICTS[verdict(row)];
      },

      verdictTitle(row) {
        if (row.error) return `Results could not be read: ${row.error}`;
        if (row.vms_error) return `VM states unavailable: ${row.vms_error}`;
        return '';
      },

      sortByVerdict(a, b, asc) {
        const diff =
          VERDICTS[verdict(a)].rank - VERDICTS[verdict(b)].rank ||
          a.name.localeCompare(b.name);
        return asc ? diff : -diff;
      },

      sortByFailing(a, b, asc) {
        const failing = (row) => (sohActive(row) ? row.checks.failing : -1);
        const diff = failing(a) - failing(b) || a.name.localeCompare(b.name);
        return asc ? diff : -diff;
      },

      rowClass(row) {
        switch (verdict(row)) {
          case 'degraded':
            return 'row-attn';
          case 'failing':
            return 'row-warn';
          default:
            return '';
        }
      },

      statusDecorator(status) {
        switch (status) {
          case 'started':
            return 'is-success';
          case 'stopped':
            return 'is-danger';
          default:
            return 'is-warning';
        }
      },

      toneClass(row) {
        switch (verdict(row)) {
          case 'degraded':
            return 't-bad';
          case 'failing':
            return 't-warn';
          default:
            return '';
        }
      },

      toneColor(row) {
        return TONES[verdict(row)] ?? TONES.healthy;
      },

      healthCaption(row) {
        const total = row.vms?.total ?? 0;
        const vms = plural(total, 'VM');
        if (!row.running) return `${vms}, not running`;
        if (row.status !== 'started') return `${vms}, ${row.status}`;
        if (!row.vms?.known) return `${vms}, states unavailable`;
        return vmSegments(row)
          .map((seg) => `${seg.count} ${seg.label}`)
          .join(', ');
      },

      trendLabel(row) {
        const values = this.trend(row);
        return `Failing checks over the last ${values.length} runs: ${values.join(', ')}`;
      },

      sparkLine(row) {
        return sparkPoints(this.trend(row), this.spark.width, this.spark.height)
          .map(([x, y]) => `${x},${y}`)
          .join(' ');
      },

      sparkArea(row) {
        const { width, height } = this.spark;
        return `0,${height} ${this.sparkLine(row)} ${width},${height}`;
      },

      sparkLast(row) {
        return sparkPoints(
          this.trend(row),
          this.spark.width,
          this.spark.height,
        ).at(-1);
      },

      ago(time) {
        return relativeTime(time);
      },

      lastCheckNote(row) {
        if (!row.configured) return 'soh app not in scenario';
        if (!row.running || row.status !== 'started') {
          return 'starts after post-start';
        }
        if (row.soh_running) return 'first run in progress…';
        return 'no results yet';
      },

      // why Run SOH cannot be pressed for the row, or '' when it can
      runBlocked(row) {
        if (!roleAllowed('experiments/trigger', 'create', row.name)) {
          return 'You may not run SOH on this experiment';
        }
        if (!row.configured) return 'The soh app is not in the scenario';
        if (!row.running || row.status !== 'started') {
          return 'The experiment is not running';
        }
        if (row.soh_running || row.pending) return 'SOH is running';
        if (!row.soh_initialized) {
          return 'SOH is still running its first checks';
        }
        return '';
      },

      runLabel(row) {
        return this.runBlocked(row) || `Run SOH on ${row.name}`;
      },

      runSoh(row) {
        if (this.runBlocked(row)) return;

        row.pending = true;
        clearTimeout(row.pendingTimer);
        row.pendingTimer = setTimeout(
          () => (row.pending = false),
          PENDING_TIMEOUT_MS,
        );
        this.liveRows.touch(row.name);

        axiosInstance
          .post(`experiments/${row.name}/trigger`, null, {
            params: { apps: 'soh' },
          })
          .catch((err) => {
            this.settle(row);
            useErrorNotification(err);
          });
      },

      settle(row) {
        clearTimeout(row.pendingTimer);
        row.pending = false;
      },

      shownProblems(row) {
        const all = problems(row);
        return this.showAll[row.name] ? all : all.slice(0, PROBLEMS_SHOWN);
      },

      hiddenProblems(row) {
        return this.showAll[row.name]
          ? 0
          : Math.max(0, problems(row).length - PROBLEMS_SHOWN);
      },

      truncated(row) {
        return (row.checks?.failing ?? 0) > (row.failing_checks?.length ?? 0);
      },

      find(name) {
        return this.experiments.find((exp) => exp.name === name);
      },

      reloadSoon() {
        clearTimeout(this.reloadTimer);
        this.reloadTimer = setTimeout(
          () => this.loader.load(),
          RELOAD_DELAY_MS,
        );
      },

      // keeps a running soh run's results coming in while someone watches
      pollRunning() {
        if (this.loader.loading || !inForeground()) return;
        if (this.experiments.some((exp) => exp.soh_running || exp.pending)) {
          this.loader.load();
        }
      },

      handleWs(msg) {
        const type = msg.resource?.type;
        const action = msg.resource?.action;

        if (type === 'experiment') {
          this.handleExperiment(msg.resource.name, action, msg.result);
          return;
        }

        if (type === 'apps/soh') {
          this.handleSoh(msg.resource.name, action, msg.result);
          return;
        }

        if (type === 'experiment/vm' && VM_STATE_ACTIONS.has(action)) {
          const [expName] = msg.resource.name.split('/');
          if (this.find(expName)?.running) this.reloadSoon();
        }
      },

      handleExperiment(name, action, result) {
        const exp = this.find(name);

        switch (action) {
          case 'starting':
          case 'stopping':
            if (exp) {
              this.liveRows.touch(name);
              exp.status = action;
              exp.percent = 0;
            }
            break;
          case 'progress':
            if (exp) exp.percent = result?.percent ?? exp.percent;
            break;
          case 'start':
          case 'stop':
          case 'create':
          case 'update':
          case 'delete':
            this.reloadSoon();
            break;
        }
      },

      handleSoh(name, action, result) {
        const exp = this.find(name);
        if (!exp) return;

        switch (action) {
          case 'start':
            this.liveRows.touch(name);
            this.settle(exp);
            exp.soh_running = true;
            break;
          case 'success':
            this.settle(exp);
            this.reloadSoon();
            break;
          case 'error':
            this.settle(exp);
            this.reloadSoon();
            showError(
              `State of health run failed for ${name}`,
              result?.error ?? '',
            );
            break;
          default:
            debug('unhandled soh app message', action);
        }
      },
    },

    watch: {
      filter() {
        this.table.currentPage = 1;
      },
    },
  };
</script>

<style scoped>
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem 1rem;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.9rem;
  }

  .filters .button {
    font-size: 0.9rem;
  }

  .filters .button.is-selected {
    background: whitesmoke;
    color: #333;
    border-color: whitesmoke;
  }

  .filters .count {
    display: inline-block;
    min-width: 1.4em;
    margin-left: 0.45em;
    padding: 0 0.35em;
    border-radius: 9px;
    background: rgba(0, 0, 0, 0.25);
    font-size: 0.78rem;
    font-variant-numeric: tabular-nums;
  }

  .filters .button.is-selected .count {
    background: #d0d0d0;
  }

  .filters .count.attn {
    background: #ff9900;
    color: #1d1d1d;
  }

  .summary {
    color: #dcdcdc;
    font-size: 0.9rem;
  }

  .summary b {
    color: #fff;
  }

  .t-bad,
  .summary b.t-bad {
    color: #ff9aac;
  }

  .t-warn,
  .summary b.t-warn {
    color: #ffad33;
  }

  :deep(th) {
    white-space: nowrap;
  }

  :deep(td) {
    vertical-align: middle !important;
  }

  :deep(tr.row-attn td:first-child) {
    box-shadow: inset 3px 0 0 #ff9aac;
  }

  :deep(tr.row-warn td:first-child) {
    box-shadow: inset 3px 0 0 #ffad33;
  }

  .exp-name a {
    font-weight: 600;
  }

  .sub {
    font-size: 0.76rem;
    color: #c8c8c8;
    white-space: nowrap;
  }

  .note {
    white-space: normal;
    max-width: 9rem;
  }

  .big {
    font-size: 1.05rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .big .of {
    font-weight: 400;
    color: #c8c8c8;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }

  .dash {
    color: #9a9a9a;
  }

  .starting {
    min-width: 8rem;
  }

  .verdict {
    gap: 0.35em;
  }

  .tag.is-unconfigured {
    background: transparent;
    color: whitesmoke;
    border: 1px solid #9a9a9a;
  }

  .health-cell {
    width: 13rem;
  }

  .health-bar {
    display: flex;
    height: 10px;
    border-radius: 2px;
    overflow: hidden;
    background: #8a8a8a;
    margin-bottom: 0.3rem;
  }

  .health-bar span {
    flex-basis: 0;
    min-width: 3px;
  }

  .health-caption b {
    color: whitesmoke;
  }

  .health-caption b.seg-failing {
    color: #ffad33;
  }

  .health-caption b.seg-down {
    color: #ff9aac;
  }

  .sparkline {
    display: block;
    overflow: visible;
  }

  .actions {
    display: inline-flex;
    gap: 5px;
  }

  .detail-head {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem 1rem;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.5rem;
  }

  .detail-head h4 {
    margin: 0 !important;
    color: whitesmoke;
    font-size: 1rem;
  }

  /* beats Buefy's light detail row */
  .soh-overview :deep(.b-table .table tr.detail) {
    background: #3b3b3b;
    box-shadow: inset 0 2px 4px rgba(0, 0, 0, 0.35);
  }

  .soh-overview :deep(.b-table .table tr.detail .detail-container) {
    padding: 0.75rem 1rem 1rem 2.2rem;
  }

  table.problems {
    width: 100%;
    font-size: 0.86rem;
    border-collapse: collapse;
    background: transparent !important;
    margin-bottom: 0;
  }

  table.problems th {
    color: #bdbdbd !important;
    font-weight: 600;
    text-align: left;
    border-bottom: 1px solid #5f5f5f !important;
    padding: 0.3rem 0.6rem !important;
  }

  table.problems td {
    padding: 0.32rem 0.6rem !important;
    border-bottom: 1px solid #4a4a4a !important;
    color: #eee;
  }

  table.problems th.has-text-right {
    text-align: right;
  }

  .kind {
    display: inline-block;
    padding: 0 0.4em;
    border-radius: 3px;
    background: #555;
    color: #ddd;
    font-size: 0.72rem;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    white-space: nowrap;
  }

  .mono {
    font-family: monospace;
  }

  .hint {
    font-size: 0.8rem;
    color: #c8c8c8;
  }

  .hint a {
    color: #62c0d7;
  }

  .legend {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 0.4rem 1.4rem;
    margin-top: 1rem;
    font-size: 0.85rem;
    color: whitesmoke;
  }

  .legend i {
    display: inline-block;
    width: 0.8em;
    height: 0.8em;
    margin-right: 0.45em;
    border-radius: 50%;
    border: 1px solid #777;
    vertical-align: -0.05em;
  }
</style>
