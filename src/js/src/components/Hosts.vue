<!-- 
The Hosts component presents an information table containing 
the mesh host info. It includes the hostname, number of CPUs, 
general top load report, RAM used and total RAM, the bandwidth 
available for experiments, the number of VMs, and host uptime.
 -->

<template>
  <div class="content">
    <b-table
      :data="hosts"
      :paginated="table.isPaginated"
      :per-page="table.perPage"
      :current-page.sync="table.currentPage"
      :pagination-simple="table.isPaginationSimple"
      :pagination-size="table.paginationSize"
      :default-sort-direction="table.defaultSortDirection"
      default-sort="name">
      <b-table-column field="name" label="Name" sortable v-slot="props">
        {{ hostName(props.row) }}
      </b-table-column>
      <b-table-column
        field="cpus"
        label="CPUs"
        sortable
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
        <span class="tag" :class="decorator(props.row.load[0], props.row.cpus)">
          {{ props.row.load[0] }}
        </span>
        --
        <span class="tag" :class="decorator(props.row.load[1], props.row.cpus)">
          {{ props.row.load[1] }}
        </span>
        --
        <span class="tag" :class="decorator(props.row.load[2], props.row.cpus)">
          {{ props.row.load[2] }}
        </span>
      </b-table-column>
      <b-table-column field="mem_used" label="RAM Used" centered v-slot="props">
        <span
          class="tag"
          :class="decorator(props.row.memused, props.row.memtotal)">
          {{ formatRAM(props.row.memused) }}
        </span>
      </b-table-column>
      <b-table-column
        field="mem_total"
        label="RAM Total"
        centered
        v-slot="props">
        {{ formatRAM(props.row.memtotal) }}
      </b-table-column>
      <b-table-column
        field="disk_used"
        label="Disk Used (% phenix/minimega base)"
        centered
        v-slot="props">
        <span
          class="tag"
          :class="decorator(props.row.diskusage.diskphenix, 100.0)">
          {{ props.row.diskusage.diskphenix }}
        </span>
        /
        <span
          class="tag"
          :class="decorator(props.row.diskusage.diskminimega, 100.0)">
          {{ props.row.diskusage.diskminimega }}
        </span>
      </b-table-column>
      <b-table-column
        field="bandwidth"
        label="Bandwidth (MB/sec)"
        centered
        v-slot="props">
        {{ props.row.bandwidth }}
      </b-table-column>
      <b-table-column
        field="no_vms"
        label="# of VMs"
        sortable
        centered
        v-slot="props">
        {{ props.row.vms }}
      </b-table-column>
      <b-table-column field="uptime" label="Uptime" v-slot="props">
        {{ formatUptime(props.row.uptime) }}
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
    <b-loading
      :is-full-page="true"
      v-model="isWaiting"
      :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  import { usePhenixStore } from '@/stores/phenix.js';
  import axiosInstance from '@/utils/axios.js';
  import { formattingMixin } from '@/mixins/formattingMixin.js';
  import { useErrorNotification } from '@/utils/errorNotif';

  export default {
    mixins: [formattingMixin],
    beforeUnmount() {
      clearInterval(this.update);
    },

    created() {
      this.updateHosts();
      this.periodicUpdateHosts();
    },

    methods: {
      updateHosts() {
        axiosInstance
          .get('hosts')
          .then((response) => {
            const state = response.data;
            if (state.hosts.length === 0) {
              this.isWaiting = true;
            } else {
              this.hosts = state.hosts;
              this.isWaiting = false;
            }
          })
          .catch((err) => {
            this.isWaiting = false;
            useErrorNotification(err);
          });
      },
      periodicUpdateHosts() {
        this.update = setInterval(() => {
          this.updateHosts();
        }, 10000);
      },

      changePaginate() {
        const store = usePhenixStore();
        var user = store.username;
        localStorage.setItem(user + '.lastPaginate', this.table.isPaginated);
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
        const store = usePhenixStore();
        var user = store.username;

        if (localStorage.getItem(user + '.lastPaginate')) {
          this.table.isPaginated =
            localStorage.getItem(user + '.lastPaginate') == 'true';
        }

        if (this.hosts.length <= 10) {
          this.table.isPaginated = false;
          return false;
        } else {
          return true;
        }
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
        hosts: [],
        isWaiting: true,
      };
    },
  };
</script>
