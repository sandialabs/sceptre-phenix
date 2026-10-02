<!-- 
The VM Tiles component displays the VM tiles available to the 
VM Viewer user role. The user can drill into available VMs per 
experiment as well as all assigned VMs. The VM information is 
available to the user, however, their only available action is 
to access the VM VNC by clicking on the screenshot.
 -->

<template>
  <div class="content">
    <b-field position="is-left">
      <p class="control">
        <template v-if="exp == null">
          <h3>All Experiments</h3>
        </template>
        <template v-else>
          <h3>Experiment: {{ exp }}</h3>
        </template>
      </p>
    </b-field>
    <br /><br />
    <b-field position="is-right" grouped>
      <b-field>
        <b-autocomplete
          v-model="searchName"
          placeholder="Find a VM"
          icon="search"
          :data="filteredData">
          <template #empty>No results found</template>
        </b-autocomplete>
        <p v-if="searchName" class="control">
          <button
            class="button input-button"
            aria-label="Clear VM search"
            @click="searchName = ''">
            <b-icon icon="window-close"></b-icon>
          </button>
        </p>
      </b-field>

      <b-field>
        <b-dropdown v-model="exp" class="is-right" aria-role="list">
          <template #trigger>
            <button class="button is-light" icon-left="caret">
              Select Experiment
            </button>
          </template>

          <b-dropdown-item
            @click="
              searchName = '';
              exp = null;
            ">
            All Experiments
          </b-dropdown-item>
          <b-dropdown-item
            v-for="(e, index) in experiments"
            :key="index"
            :value="e"
            @click="
              searchName = '';
              exp = e;
            ">
            {{ e }}
          </b-dropdown-item>
        </b-dropdown>
      </b-field>
    </b-field>
    <div v-for="(chunk, chunkIndex) in chunkedVMs" :key="chunkIndex">
      <div class="tile is-ancestor">
        <div class="tile is-parent">
          <div
            v-for="v in chunk"
            :key="vmFullName(v)"
            class="tile is-child box is-4">
            <p class="title" style="font-size: medium">
              {{ vmFullName(v) }}
            </p>
            <figure class="image">
              <template v-if="v.running">
                <a :href="vncLoc(v)" target="_blank">
                  <img
                    :src="v.screenshot"
                    :alt="`Open the console of VM ${vmFullName(v)}`" />
                </a>
              </template>
              <template v-else>
                <img
                  src="@/assets/imgs/not-running.png"
                  :alt="`VM ${vmFullName(v)} is not running`" />
              </template>
            </figure>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
  import { chunk, sortBy } from 'lodash-es';
  import { createPageLoader } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';
  import { inForeground } from '@/utils/foreground.js';
  import { usePhenixStore } from '@/store';
  export default {
    beforeUnmount() {
      clearInterval(this.update);
      this.loader.stop();
    },

    created() {
      this.loader = createPageLoader({
        key: 'vmtiles',
        fetch: pageFetchers.vmtiles,
        apply: (vms) => (this.vms = vms),
      });
      this.loader.start();
      this.periodicUpdateVms();
    },

    computed: {
      getVms() {
        let vms = this.vms;

        if (this.exp) {
          vms = vms.filter((vm) => {
            return vm.experiment === this.exp;
          });
        }

        const term = (this.searchName ?? '').toLowerCase();
        var data = [];

        for (let i in vms) {
          let vm = vms[i];
          let name = vm.name;

          if (!this.exp) {
            name = vm.experiment + '_' + vm.name;
          }

          if (name.toLowerCase().includes(term)) {
            data.push(vm);
          }
        }

        return sortBy(data, (vm) => {
          return vm.experiment.toLowerCase() + '_' + vm.name.toLowerCase();
        });
      },

      experiments() {
        return [...new Set(this.vms.map((e) => e.experiment))];
      },

      chunkedVMs() {
        return chunk(this.getVms, 3);
      },

      filteredData() {
        let names;
        let vms = this.getVms;

        if (this.exp) {
          names = vms.map((vm) => {
            return vm.name;
          });
        } else {
          names = vms.map((vm) => {
            return vm.experiment + '_' + vm.name;
          });
        }
        return names.filter((option) => {
          return (
            option
              .toString()
              .toLowerCase()
              .indexOf(this.searchName.toLowerCase()) >= 0
          );
        });
      },
    },

    methods: {
      periodicUpdateVms() {
        this.update = setInterval(() => {
          // skip polling (and its screenshots) unless the page is focused,
          // or while the last poll is still waiting on the server
          if (inForeground() && !this.loader.loading) {
            this.loader.load();
          }
        }, 60000);
      },

      vmFullName(vm) {
        if (this.exp) {
          return vm.name;
        }

        return vm.experiment + '/' + vm.name;
      },

      vncLoc(vm) {
        return this.$router.resolve({
          name: 'vnc',
          params: {
            id: vm.experiment,
            name: vm.name,
            token: usePhenixStore().token,
          },
        }).href;
      },
    },

    data() {
      return {
        // opened from an experiment's page, start with only its VMs
        exp: this.$route.params.id ?? null,
        vms: [],
        searchName: '',
      };
    },
  };
</script>
