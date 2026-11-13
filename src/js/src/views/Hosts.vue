<!-- 
The Hosts component presents an information table containing 
the mesh host info. It includes the hostname, number of CPUs, 
general top load report, RAM used and total RAM, disk usage, the
bandwidth available for experiments, the number of VMs, and host uptime.
 -->

<template>
  <div class="content">
    <b-field v-if="paginationNeeded" grouped position="is-right">
      <div class="control is-flex">
        <b-switch v-model="table.isPaginated" size="is-small" type="is-light"
          >Paginate</b-switch
        >
      </div>
    </b-field>
    <b-table
      :data="hosts"
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
      default-sort="name"
      scrollable>
      <template #empty>
        <section class="section">
          <div class="content has-text-white has-text-centered">
            {{ loaded ? 'No hosts found' : loadingText('hosts') }}
          </div>
        </section>
      </template>
      <b-table-column
        field="name"
        label="Name"
        sortable
        header-class="sort-inline"
        v-slot="props">
        {{ hostName(props.row) }}
      </b-table-column>
      <b-table-column
        field="cpus"
        label="CPUs"
        sortable
        header-class="sort-inline"
        centered
        v-slot="props">
        {{ props.row.cpus }}
      </b-table-column>
      <b-table-column
        field="load"
        label="Load"
        width="200"
        centered
        v-slot="props">
        <span
          class="load"
          title="Load average over the last 1, 5 and 15 minutes">
          <span v-for="(value, i) in props.row.load" :key="i" class="load-avg">
            <span class="tag" :class="decorator(value, props.row.cpus)">
              {{ value }}
            </span>
            <small class="load-span">{{ loadSpans[i] }}</small>
          </span>
        </span>
      </b-table-column>
      <b-table-column
        field="memused"
        label="RAM Used"
        sortable
        header-class="sort-inline"
        centered
        v-slot="props">
        <span
          class="tag"
          :class="decorator(props.row.memused, props.row.memtotal)">
          {{ formatRAM(props.row.memused) }}
        </span>
      </b-table-column>
      <b-table-column
        field="memtotal"
        label="RAM Total"
        sortable
        header-class="sort-inline"
        centered
        v-slot="props">
        {{ formatRAM(props.row.memtotal) }}
      </b-table-column>
      <b-table-column
        field="disk_used"
        label="Disk Used (phenix / minimega base)"
        centered>
        <template #header>
          Disk Used
          <span class="th-sub">phenix / minimega base</span>
        </template>
        <template #default="props">
          <span
            class="tag"
            :class="decorator(props.row.diskusage.diskphenix, 100.0)">
            {{ props.row.diskusage.diskphenix }}%
          </span>
          /
          <span
            class="tag"
            :class="decorator(props.row.diskusage.diskminimega, 100.0)">
            {{ props.row.diskusage.diskminimega }}%
          </span>
        </template>
      </b-table-column>
      <b-table-column
        field="bandwidth"
        label="Bandwidth (MB/sec)"
        centered
        v-slot="props">
        {{ props.row.bandwidth }}
      </b-table-column>
      <b-table-column
        field="vms"
        label="VMs"
        sortable
        header-class="sort-inline"
        centered
        v-slot="props">
        {{ props.row.vms }}
      </b-table-column>
      <b-table-column
        field="uptime"
        label="Uptime"
        sortable
        header-class="sort-inline"
        v-slot="props">
        {{ formatUptime(props.row.uptime) }}
      </b-table-column>
    </b-table>
  </div>
</template>

<script>
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import { useTable } from '@/utils/useTable.js';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';
  import { inForeground } from '@/utils/foreground.js';
  import { RESOURCES, startPoll } from '@/utils/freshness.js';

  export default {
    mixins: [formattingMixin],
    setup() {
      return useTable({ name: 'hosts' });
    },
    beforeUnmount() {
      this.stopPoll?.();
      this.loader.stop();
    },

    created() {
      this.loader = createPageLoader({
        key: 'hosts',
        fetch: pageFetchers.hosts,
        apply: (hosts) => {
          this.hosts = hosts;
          this.loaded = true;
        },
      });
      this.loader.start();
      this.periodicUpdateHosts();
    },

    methods: {
      loadingText,

      periodicUpdateHosts() {
        // every poll has the server ask minimega for host stats and disk
        // usage: skip it while the page is hidden, and while the last
        // poll is still waiting on the server
        this.stopPoll = startPoll(
          () => this.loader.load({ poll: true }),
          RESOURCES.hosts.pollMs,
          { when: () => inForeground() && !this.loader.loading },
        );
      },

      decorator(sum, len) {
        let actual = sum / len;
        if (actual < 0.65) {
          return 'is-success';
        } else if (actual >= 0.65 && actual < 0.85) {
          return 'is-warning';
        } else {
          return 'is-danger';
        }
      },

      hostName(host) {
        if (host.headnode) {
          return host.name + ' (headnode)';
        }

        return host.name;
      },
    },

    computed: {
      paginationNeeded() {
        return this.hosts.length > this.table.perPage;
      },
    },

    data() {
      return {
        hosts: [],
        loaded: false,
        // what each of a host's three load averages covers
        loadSpans: ['1m', '5m', '15m'],
      };
    },
  };
</script>

<style scoped>
  /* headings stay on one line, however narrow the table gets */
  :deep(th) {
    white-space: nowrap;
  }

  /* a heading's detail, on a line of its own so the column stays narrow */
  .th-sub {
    display: block;
    font-size: 0.75rem;
    font-weight: normal;
  }

  .load {
    display: inline-flex;
    gap: 0.5rem;
  }

  .load-avg {
    display: inline-flex;
    flex-direction: column;
    align-items: center;
  }

  .load-span {
    font-size: 0.7rem;
    opacity: 0.75;
  }
</style>
