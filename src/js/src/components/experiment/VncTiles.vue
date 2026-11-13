<template>
  <div class="vnc-tiles-toolbar">
    <VncTileToolbar />
    <span class="vnc-tiles-fill"></span>
    <div class="buttons vnc-tiles-select">
      <button
        type="button"
        class="button is-small vnc-select-all"
        :disabled="shownSelectable.every((name) => selected.includes(name))"
        @click="selectShown(true)">
        Select all
      </button>
      <button
        type="button"
        class="button is-small vnc-clear-selection"
        :disabled="!shownSelectable.some((name) => selected.includes(name))"
        @click="selectShown(false)">
        Clear selection
      </button>
    </div>
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
        <span>{{ splitLabel }}</span>
      </a>
      <button
        v-else
        type="button"
        class="button is-small vnc-open-split"
        aria-disabled="true"
        :aria-label="`${splitLabel}: ${split.reason}`">
        <b-icon icon="table-cells" size="is-small" />
        <span>{{ splitLabel }}</span>
      </button>
    </b-tooltip>
  </div>
  <div class="vnc-tiles-grid">
    <template v-if="consoleVms.length">
      <div
        v-for="vm in consoleVms"
        :key="keyOf(vm)"
        :class="[
          'vnc-tile-item',
          { 'is-selected': selected.includes(vm.name) },
        ]">
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
              :aria-label="toolLabel(action, vm)"
              :title="action.reason || action.label"
              :aria-disabled="action.disabled ? 'true' : undefined"
              @click="action.disabled || $emit('action', action.name, vm)">
              <b-icon :icon="action.icon" size="is-small" />
            </button>
            <button
              v-for="tool in toolsOf(vm)"
              :key="tool.name"
              type="button"
              :class="[
                'button',
                'is-small',
                `vnc-tile-${tool.name}`,
                { 'is-loading': tool.pending },
              ]"
              :aria-label="toolLabel(tool, vm)"
              :title="tool.reason || tool.description"
              :aria-disabled="
                tool.disabled || tool.pending ? 'true' : undefined
              "
              :aria-busy="tool.pending ? 'true' : undefined"
              @click="
                tool.disabled || tool.pending || $emit('action', tool.name, vm)
              ">
              <b-icon :icon="tool.icon" size="is-small" />
            </button>
          </template>
        </VncTile>
        <div class="vnc-tile-label">
          <!-- the VM table's checkbox: none while the VM is busy -->
          <b-checkbox
            v-if="!vm.busy"
            class="vnc-tile-select"
            :model-value="selected"
            :native-value="vm.name"
            @update:model-value="(names) => $emit('update:selected', names)">
            <span class="is-sr-only">Select VM {{ vm.name }}</span>
          </b-checkbox>
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
  import { sharedLiveTilePool } from '@/vnc/liveTilePool.js';
  import { handleConsoleLinkClick } from '@/vnc/openConsole.js';
  import VncTile from '@/vnc/VncTile.vue';
  import VncTileToolbar from '@/vnc/VncTileToolbar.vue';
  import { consoleAvailable } from '@/vnc/vmScreen.js';
  import { POWER_ACTIONS, shownAction, vmButtonStates } from './vmActions.js';
  import { consoleLinkAlt, screenshotAlt, vmScreenshot } from './vmScreens.js';

  // the tools of a tile's hover bar, after its power actions
  const TILE_TOOLS = ['screenshot', 'snapshot'];

  // The running experiment's VNC tab: a live, view-only tile per VM with a
  // console, at the zoom the slider picks. A tile opens the console page, and
  // its hover bar offers the details, the power actions and, in Live mode, a
  // screenshot and a snapshot. The bar also shows while focus is in the tile.
  // A checkbox under each tile selects its VM, as in the VM table.
  export default {
    name: 'VncTiles',
    components: { BSlider, BSliderTick, VncTile, VncTileToolbar },
    setup() {
      return {
        liveMode: sharedLiveTilePool().liveMode,
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
      // the names of the VMs selected, shared with the VM table
      selected: { type: Array, default: () => [] },
      // the actions running in the background for each VM, by its name
      actionPending: { type: Object, default: () => ({}) },
    },
    emits: ['info', 'action', 'update:zoom', 'update:selected'],

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
        const count = chosen.length;
        if (!count) {
          return { count, reason: 'Select one to four running VMs first' };
        }
        if (count > MAX_PANES) {
          return {
            count,
            reason: 'Select at most four VMs: one console page shows four',
          };
        }
        const vms = chosen.map((vm) => this.keyOf(vm));
        return { count, vms, href: consoleHref(this.$router, vms), reason: '' };
      },

      splitLabel() {
        const { count } = this.split;
        return count
          ? `Open ${count} selected in split`
          : 'Open selected in split';
      },

      // the names of the shown tiles that can be selected
      shownSelectable() {
        return this.consoleVms.filter((vm) => !vm.busy).map((vm) => vm.name);
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

      // the power actions the VM's state allows now, greyed out with the
      // permission they need when the role may not run them
      powerOf(vm) {
        const allowed = vmButtonStates(POWER_ACTIONS, vm, { can: () => true });
        return vmButtonStates(POWER_ACTIONS, vm, {
          can: this.canOn(vm),
        }).filter((action, i) => shownAction(action) && !allowed[i].disabled);
      },

      // in Live mode, the tools on the VM, greyed out with the reason when
      // the VM's state or the role rules them out
      toolsOf(vm) {
        if (!this.liveMode) return [];
        const pending = this.actionPending[vm.name] ?? [];
        return vmButtonStates(TILE_TOOLS, vm, { can: this.canOn(vm) })
          .filter(shownAction)
          .map((tool) => ({ ...tool, pending: pending.includes(tool.name) }));
      },

      // a tool's accessible name, with the reason when it is greyed out
      toolLabel(tool, vm) {
        const label = `${tool.label} VM ${vm.name}`;
        return tool.disabled ? `${label}: ${tool.reason}` : label;
      },

      // answers RBAC for the VM
      canOn(vm) {
        return (resource, verb) => roleAllowed(resource, verb, this.keyOf(vm));
      },

      // selects the shown tiles, or clears them from the selection
      selectShown(on) {
        const shown = this.shownSelectable;
        const others = this.selected.filter((name) => !shown.includes(name));
        this.$emit('update:selected', on ? [...others, ...shown] : others);
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

  .vnc-tiles-select {
    margin-bottom: 0 !important;
  }

  .vnc-tiles-select .button {
    margin-bottom: 0;
  }

  .vnc-tile-item {
    outline: 3px solid transparent;
  }

  .vnc-tile-item.is-selected {
    outline-color: #62c0d7;
  }

  .vnc-tile-label {
    display: flex;
    align-items: center;
    background-color: #4d4d4d;
  }

  /* whitesmoke on #4d4d4d is 7.7:1 */
  .vnc-tile-name {
    color: whitesmoke;
    display: block;
    flex: 1 1 auto;
    width: 100%;
    background-color: #4d4d4d;
    text-align: center;
    padding: 2px 0px;
  }

  .vnc-tile-select {
    margin: 0 0 0 4px !important;
  }

  /* the box's border on #4d4d4d is 7.7:1 */
  .vnc-tile-select :deep(.check) {
    border-color: whitesmoke;
  }
</style>
