<!-- 
The VM Tiles component displays the VM tiles available to the 
VM Viewer user role. The user can drill into available VMs per 
experiment as well as all assigned VMs. The VM information is 
available to the user, however, their only available action is 
to access the VM VNC by clicking on the screenshot. This does 
not currently support the base64 encoded display, which the server 
side will pass.
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
          :data="filteredData"
          @select="(option) => (filtered = option)">
          <template slot="empty">No results found</template>
        </b-autocomplete>
        <p class="control">
          <button
            class="button input-button"
            @click="
              searchVMs('');
              filesTable.category = null;
            ">
            <b-icon icon="window-close"></b-icon>
          </button>
        </p>
      </b-field>

      <b-field>
        <b-dropdown v-model="exp" class="is-right" aria-role="list">
          <template #trigger>
            <button class="button is-light" icon-left="caret" slot="trigger">
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
    <div v-for="chunk in chunkedVMs">
      <div class="tile is-ancestor">
        <div class="tile is-parent">
          <template v-if="exp == null">
            <div v-for="v in chunk" class="tile is-child box is-4">
              <p class="title" style="font-size: medium">
                {{ vmFullName(v) }}
              </p>
              <figure class="image">
                <template v-if="v.running">
                  <a :href="vncLoc(v)" target="_blank">
                    <img :src="v.screenshot" />
                  </a>
                </template>
                <template v-else>
                  <img src="@/assets/imgs/not-running.png" />
                </template>
              </figure>
            </div>
          </template>
          <template v-else>
            <div v-for="v in chunk" class="tile is-child box is-4">
              <p
                v-if="v.experiment === exp"
                class="title"
                style="font-size: medium">
                {{ vmFullName(v) }}
              </p>
              <figure class="image">
                <template v-if="v.running && v.experiment === exp">
                  <a :href="vncLoc(v)" target="_blank">
                    <img :src="v.screenshot" />
                  </a>
                </template>
                <template v-else-if="v.experiment === exp">
                  <img src="@/assets/imgs/not-running.png" />
                </template>
              </figure>
            </div>
          </template>
        </div>
      </div>
    </div>
    <b-loading
      :is-full-page="true"
      :active.sync="isWaiting"
      :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  import { chunk, sortBy } from 'lodash-es';
  import { useErrorNotification } from '@/utils/errorNotif';
  import axiosInstance from '@/utils/axios.js';
  import { usePhenixStore } from '@/stores/phenix';
  export default {
    beforeDestroy() {
      clearInterval(this.update);
    },

    created() {
      this.updateVms();
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

        var name_re = new RegExp(this.searchName, 'i');
        var data = [];

        for (let i in vms) {
          let vm = vms[i];
          let name = vm.name;

          if (!this.exp) {
            name = vm.experiment + '_' + vm.name;
          }

          if (name.match(name_re)) {
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
        let names = [];
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
      updateVms() {
        axiosInstance
          .get('vms?screenshot=500')
          .then((response) => {
            this.vms = response.data.vms;
            this.isWaiting = false;
          })
          .catch((err) => {
            this.isWaiting = false;
            // TODO: do we want to include an error like this to a VM Viewer?
            useErrorNotification(err);
          });
      },

      periodicUpdateVms() {
        this.update = setInterval(() => {
          this.updateVms();
        }, 30000);
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
        exp: null,
        vms: [],
        searchName: '',
        filtered: null,
        isWaiting: true,
      };
    },
  };
</script>
