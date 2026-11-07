<template>
  <div class="vnc-tiles-toolbar">
    <VncTileToolbar />
    <span class="vnc-tiles-fill"></span>
    <b-field label="Zoom" class="vnc-tiles-zoom" horizontal>
      <b-slider
        aria-label="Zoom level"
        :min="2"
        :max="16"
        :custom-formatter="zoomLabel"
        :model-value="zoom"
        @update:model-value="(value) => $emit('update:zoom', value)">
        <template v-for="val in [4, 8, 12, 16]" :key="val">
          <b-slider-tick :value="val">
            {{ zoomLabel(val) }}
          </b-slider-tick>
        </template>
      </b-slider>
    </b-field>
    <b-tooltip
      :label="split.reason || 'Open the selected VMs in one console page'"
      type="is-dark"
      position="is-left"
      multilined>
      <a
        v-if="!split.reason"
        class="button is-small vnc-open-split"
        :href="split.href"
        @click="(event) => handleConsoleLinkClick(event, split.vms)">
        <b-icon icon="table-cells" size="is-small" />
        <span>Open selected in split</span>
      </a>
      <button
        v-else
        type="button"
        class="button is-small vnc-open-split"
        aria-disabled="true"
        :aria-label="`Open selected in split: ${split.reason}`">
        <b-icon icon="table-cells" size="is-small" />
        <span>Open selected in split</span>
      </button>
    </b-tooltip>
  </div>
  <div class="vnc-tiles-grid">
    <template v-if="consoleVms.length">
      <div v-for="vm in consoleVms" :key="keyOf(vm)" class="vnc-tile-item">
        <VncTile
          :experiment="experiment"
          :vm="vm"
          :picture="vmScreenshot(vm, screenshotsDue)"
          :alt="screenshotAlt(vm, screenshotsDue)"
          :link-label="consoleLinkAlt(vm, screenshotsDue)"
          :width="width"
          @live="(on) => setLive(vm.name, on)">
          <template #bar>
            <a
              v-if="canConsole(vm)"
              class="button is-small is-info"
              :href="consoleOf(vm)"
              :aria-label="`Open the console of VM ${vm.name}`"
              title="Open console"
              @click="(event) => handleConsoleLinkClick(event, [keyOf(vm)])">
              <b-icon icon="up-right-from-square" size="is-small" />
            </a>
            <button
              v-if="canGet(vm)"
              type="button"
              class="button is-small"
              :aria-label="`Details of VM ${vm.name}`"
              title="Details"
              aria-haspopup="dialog"
              @click="$emit('info', vm)">
              <b-icon icon="info-circle" size="is-small" />
            </button>
            <button
              v-for="action in powerOf(vm)"
              :key="action.name"
              type="button"
              :class="['button', 'is-small', action.type]"
              :aria-label="`${action.label} VM ${vm.name}`"
              :title="action.label"
              @click="$emit('action', action.name, vm)">
              <b-icon :icon="action.icon" size="is-small" />
            </button>
          </template>
        </VncTile>
        <button
          v-if="canGet(vm)"
          type="button"
          class="plain-button vnc-tile-name"
          aria-haspopup="dialog"
          @click="$emit('info', vm)">
          {{ vm.name }}
        </button>
        <span v-else class="vnc-tile-name">{{ vm.name }}</span>
      </div>
    </template>
    <template v-else>{{
      experiment ? 'No VMs with a VNC console' : loadingText('VMs')
    }}</template>
  </div>
</template>

<script>
  import { BSlider, BSliderTick } from 'buefy';
  import { loadingText } from '@/utils/pageLoader.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { onWsReconnect, sendWsMsg } from '@/utils/websocket';
  import { MAX_PANES, consoleHref } from '@/vnc/consoleUrl.js';
  import { handleConsoleLinkClick } from '@/vnc/openConsole.js';
  import VncTile from '@/vnc/VncTile.vue';
  import VncTileToolbar from '@/vnc/VncTileToolbar.vue';
  import { consoleAvailable } from '@/vnc/vmScreen.js';
  import { POWER_ACTIONS, vmButtonStates } from './vmActions.js';
  import { consoleLinkAlt, screenshotAlt, vmScreenshot } from './vmScreens.js';

  // The running experiment's VNC tab: a live, view-only tile per VM with a
  // console, at the zoom the slider picks. A tile opens the console page, and
  // its hover bar offers the details and the power actions.
  export default {
    name: 'VncTiles',
    components: { BSlider, BSliderTick, VncTile, VncTileToolbar },
    setup() {
      return {
        loadingText,
        consoleLinkAlt,
        screenshotAlt,
        vmScreenshot,
        handleConsoleLinkClick,
      };
    },
    props: {
      vms: { type: Array, default: () => [] },
      // the experiment's name; empty until it has loaded
      experiment: { type: String, default: '' },
      // the slider's zoom step; the tiles are 50px wide per step
      zoom: { type: Number, required: true },
      // the tiles' width in pixels
      width: { type: Number, required: true },
      // whether VMs still without screenshots have waited long enough
      screenshotsDue: { type: Boolean, default: false },
      // the names of the VMs selected in the VM table
      selected: { type: Array, default: () => [] },
    },
    emits: ['info', 'action', 'update:zoom'],

    data() {
      return {
        // the VMs whose tiles draw a live screen
        live: [],
      };
    },

    computed: {
      // External/HIL nodes and DNB (do not boot) nodes have no VNC console
      // to display.
      consoleVms() {
        return this.vms.filter((vm) => !vm.external && !vm.dnb);
      },

      // the console page of the selected VMs that have a console, in tile
      // order, or why there is none
      split() {
        const chosen = this.consoleVms.filter(
          (vm) => this.selected.includes(vm.name) && this.canConsole(vm),
        );
        if (!chosen.length) {
          return {
            reason: 'Select one to four running VMs in the VMs tab first',
          };
        }
        if (chosen.length > MAX_PANES) {
          return {
            reason: 'Select at most four VMs: one console page shows four',
          };
        }
        const vms = chosen.map((vm) => this.keyOf(vm));
        return { vms, href: consoleHref(this.$router, vms), reason: '' };
      },

      liveKeys() {
        return this.live.map((name) => `${this.experiment}/${name}`).sort();
      },
    },

    watch: {
      liveKeys(keys, old) {
        if (keys.join() !== old.join()) this.sendLive();
      },
    },

    created() {
      // the server forgets the list when the websocket reconnects
      this.offWsReconnect = onWsReconnect(() => {
        if (this.live.length) this.sendLive();
      });
    },

    beforeUnmount() {
      this.offWsReconnect();
      if (this.live.length) {
        this.live = [];
        this.sendLive();
      }
    },

    methods: {
      zoomLabel(val) {
        return 0.25 + (val - 1) * 0.25 + 'x';
      },

      keyOf(vm) {
        return `${this.experiment}/${vm.name}`;
      },

      canGet(vm) {
        return roleAllowed('vms', 'get', this.keyOf(vm));
      },

      // the VM has a screen the role may see
      canConsole(vm) {
        return consoleAvailable(this.experiment, vm);
      },

      consoleOf(vm) {
        return consoleHref(this.$router, [this.keyOf(vm)]);
      },

      // the power actions the role may run on the VM now
      powerOf(vm) {
        const can = (resource, verb) =>
          roleAllowed(resource, verb, this.keyOf(vm));
        return vmButtonStates(POWER_ACTIONS, vm, { can }).filter(
          (action) => action.permitted && !action.disabled,
        );
      },

      setLive(name, on) {
        const others = this.live.filter((n) => n !== name);
        this.live = on ? [...others, name] : others;
      },

      // The server sends no screenshots of the VMs whose tiles draw a live
      // screen.
      sendLive() {
        sendWsMsg({
          resource: { type: 'metadata/screenshot', action: 'live' },
          request: { vms: this.liveKeys },
        });
      },
    },
  };
</script>

<style scoped>
  .vnc-tiles-toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 1rem;
    margin: 0 0 0.75rem 0;
  }

  .vnc-tiles-fill {
    flex: 1 1 0;
  }

  .vnc-tiles-zoom {
    width: 300px;
    margin-bottom: 0 !important;
  }

  .vnc-tiles-grid {
    display: flex;
    flex-direction: row;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: center;
    gap: 4px;
  }

  /* whitesmoke on #4d4d4d is 7.7:1 */
  .vnc-tile-name {
    color: whitesmoke;
    display: block;
    width: 100%;
    background-color: #4d4d4d;
    text-align: center;
    padding: 2px 0px;
  }
</style>
