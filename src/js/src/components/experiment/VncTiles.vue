<template>
  <b-field
    label="Zoom Level"
    style="width: 300px; margin-left: 32px"
    horizontal>
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
  <div
    style="
      display: flex;
      flex-direction: row;
      flex-wrap: wrap;
      align-items: flex-end;
      justify-content: center;
      gap: 4px;
    ">
    <template v-if="consoleVms.length">
      <div v-for="vm in consoleVms" :key="vm.name">
        <a :href="vncLoc(vm)" target="_blank">
          <img
            :alt="consoleLinkAlt(vm, screenshotsDue)"
            :src="vmScreenshot(vm, screenshotsDue)"
            :width="width"
            style="display: block" />
        </a>
        <a
          v-if="roleAllowed('vms', 'get', experiment + '/' + vm.name)"
          class="vnc-tile-name"
          @click="$emit('info', vm)"
          >{{ vm.name }}</a
        >
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
  import { consoleLinkAlt, vmScreenshot, vncHref } from './vmScreens.js';

  // The running experiment's VNC tab: a tile per VM with a console, its
  // screenshot opening the console, at the zoom the slider picks.
  export default {
    name: 'VncTiles',
    components: { BSlider, BSliderTick },
    setup() {
      return { roleAllowed, loadingText, consoleLinkAlt, vmScreenshot };
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
    },
    emits: ['info', 'update:zoom'],

    computed: {
      // External/HIL nodes and DNB (do not boot) nodes have no VNC console
      // to display.
      consoleVms() {
        return this.vms.filter((vm) => !vm.external && !vm.dnb);
      },
    },

    methods: {
      zoomLabel(val) {
        return 0.25 + (val - 1) * 0.25 + 'x';
      },

      vncLoc(vm) {
        return vncHref(this.$router, this.$route.params.id, vm);
      },
    },
  };
</script>

<style scoped>
  .vnc-tile-name {
    color: whitesmoke;
    display: block;
    background-color: grey;
    text-align: center;
    padding: 2px 0px;
  }
</style>
