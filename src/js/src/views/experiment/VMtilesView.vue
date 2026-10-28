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
            :ref="(el) => trackTile(v, el)"
            class="tile is-child box is-4">
            <p class="title" style="font-size: medium">
              {{ vmFullName(v) }}
            </p>
            <figure class="image">
              <template v-if="v.running">
                <a :href="vncLoc(v)" target="_blank">
                  <img
                    :src="shotOf(v)"
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
  import loadingScreenshotImg from '@/assets/imgs/loading-screenshot.svg';
  import notAvailableImg from '@/assets/imgs/not-available.png';
  import * as vmsApi from '@/api/vms.js';
  import { createPageLoader } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';
  import { inForeground } from '@/utils/foreground.js';
  import { RESOURCES, startPoll } from '@/utils/freshness.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { trackVisibility } from '@/utils/visibility.js';
  import { usePhenixStore } from '@/store';

  // the tiles' screenshot width, one of the widths the server takes them at
  const TILE_SCREENSHOT_WIDTH = 600;

  // screenshots asked for at once; minimega takes them one at a time
  const SCREENSHOT_FETCHES = 2;

  const tileKey = (vm) => `${vm.experiment}/${vm.name}`;

  export default {
    beforeUnmount() {
      this.stopPoll?.();
      this.loader.stop();
      this.tiles.stop();
      this.shotsAborted.abort();
    },

    created() {
      // Screenshots are fetched only for the tiles on screen, once per load
      // of the VMs: shotLoad is the load each tile's screenshot was asked for.
      this.tiles = trackVisibility((keys) => this.fetchShots(keys));
      this.shotsAborted = new AbortController();
      this.shotQueue = [];
      this.shotsFetching = 0;
      this.vmLoad = 0;
      this.shotLoad = new Map();

      this.loader = createPageLoader({
        key: 'vmtiles',
        fetch: pageFetchers.vmtiles,
        apply: (vms) => {
          this.vms = vms;
          this.vmLoad++;
          this.fetchShots(this.tiles.shownKeys());
        },
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

        for (const vm of vms) {
          if (this.vmFullName(vm).toLowerCase().includes(term)) {
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
        // getVms holds only the VMs whose names match the search
        return this.getVms.map((vm) => this.vmFullName(vm));
      },
    },

    methods: {
      periodicUpdateVms() {
        // skip polling (and its screenshots) while the page is hidden,
        // or while the last poll is still waiting on the server
        this.stopPoll = startPoll(
          () => this.loader.load({ poll: true }),
          RESOURCES.vmtiles.pollMs,
          { when: () => inForeground() && !this.loader.loading },
        );
      },

      trackTile(vm, el) {
        this.tiles.track(tileKey(vm), el);
      },

      // the picture on a running VM's tile
      shotOf(vm) {
        return this.shots[tileKey(vm)] || vm.screenshot || loadingScreenshotImg;
      },

      // asks for the screenshots of the tiles with these keys that are of
      // running VMs and have none from this load of the VMs yet
      fetchShots(keys) {
        const byKey = new Map(this.vms.map((vm) => [tileKey(vm), vm]));
        for (const key of keys) {
          const vm = byKey.get(key);
          if (!vm?.running || this.shotLoad.get(key) === this.vmLoad) continue;
          this.shotLoad.set(key, this.vmLoad);
          if (!roleAllowed('vms/screenshot', 'get', key)) {
            this.shots[key] = notAvailableImg;
            continue;
          }
          this.shotQueue.push(vm);
        }
        this.drainShots();
      },

      drainShots() {
        while (
          this.shotsFetching < SCREENSHOT_FETCHES &&
          this.shotQueue.length
        ) {
          const vm = this.shotQueue.shift();
          const key = tileKey(vm);
          // scrolled away while waiting: asked for again when it returns
          if (!this.tiles.isShown(key)) {
            this.shotLoad.delete(key);
            continue;
          }
          this.shotsFetching++;
          this.fetchShot(vm).finally(() => {
            this.shotsFetching--;
            this.drainShots();
          });
        }
      },

      async fetchShot(vm) {
        const key = tileKey(vm);
        try {
          const { data } = await vmsApi.getVmScreenshot(
            vm.experiment,
            vm.name,
            TILE_SCREENSHOT_WIDTH,
            this.shotsAborted.signal,
          );
          this.shots[key] =
            typeof data === 'string' && data.startsWith('data:image/png;')
              ? data
              : notAvailableImg;
        } catch {
          if (!this.shotsAborted.signal.aborted) {
            this.shots[key] = notAvailableImg;
          }
        }
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
        // the screenshot on each running VM's tile, by experiment/VM
        shots: {},
        searchName: '',
      };
    },
  };
</script>
