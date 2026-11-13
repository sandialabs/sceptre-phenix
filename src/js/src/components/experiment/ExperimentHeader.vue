<template>
  <div :class="['experiment-header', { 'is-collapsed': !expanded }]">
    <ExperimentSummary
      class="experiment-header-summary"
      :name="name"
      :experiment="experiment"
      :running="running"
      :delayed="delayed"
      :next-start="nextStart"
      :hosts="hosts"
      :hosts-failed="hostsFailed"
      :vm-count="vmCount"
      :stopped="stopped"
      :collapsed="!expanded"
      :app-count="apps.length">
      <template #end>
        <AppsTriggerButton
          v-if="!expanded && apps.length && !stopped"
          :experiment="name"
          :apps="apps"
          position="is-bottom"
          @trigger="$emit('trigger')" />
        <DetailsToggle
          v-if="!toggleInApps"
          :expanded="expanded"
          :controls="controls"
          @click="toggle" />
      </template>
    </ExperimentSummary>
    <ExperimentAppsBox
      v-if="apps.length"
      v-show="expanded"
      id="experiment-header-apps"
      class="experiment-header-apps"
      :experiment="name"
      :apps="apps"
      :states="states"
      :stopped="stopped"
      @trigger="(...app) => $emit('trigger', ...app)">
      <template #end>
        <DetailsToggle
          v-if="toggleInApps"
          :expanded="expanded"
          :controls="controls"
          @click="toggle" />
      </template>
    </ExperimentAppsBox>
    <ExperimentProgress
      v-if="progressShown"
      class="experiment-header-progress"
      :name="name"
      :progress="progressState.progress"
      :failure="progressState.failure" />
  </div>
</template>

<script>
  import ExperimentProgress from '@/components/ExperimentProgress.vue';
  import { progressActive } from '@/utils/experimentProgress.js';
  import { readPref, writePref } from '@/utils/prefs.js';
  import AppsTriggerButton from './AppsTriggerButton.vue';
  import DetailsToggle from './DetailsToggle.vue';
  import ExperimentAppsBox from './ExperimentAppsBox.vue';
  import ExperimentSummary from './ExperimentSummary.vue';
  import { HEADER_EXPANDED_PREF } from './experimentHeader.js';
  import { EXPERIMENT_PROGRESS } from './headerProgress.js';

  // The experiment page's header: its summary and Apps box, or collapsed
  // (the default) one line with its name, status, links, counts and Trigger.
  // The browser remembers which, for every experiment page. Details stays at
  // the header's top right: in the Apps box when it is beside the summary.
  // Under them is the bar of a start or stop, or why it failed.
  export default {
    name: 'ExperimentHeader',
    components: {
      AppsTriggerButton,
      DetailsToggle,
      ExperimentAppsBox,
      ExperimentProgress,
      ExperimentSummary,
    },
    // the page's start and stop progress (see headerProgress.js)
    inject: { progressTracker: { from: EXPERIMENT_PROGRESS, default: null } },
    props: {
      // see ExperimentSummary for each of these
      name: { type: String, default: '' },
      experiment: { type: Object, default: () => ({}) },
      running: { type: Number, default: null },
      delayed: { type: Number, default: null },
      nextStart: { type: Number, default: null },
      hosts: { type: Array, default: undefined },
      hostsFailed: { type: Boolean, default: false },
      vmCount: { type: Number, default: undefined },
      stopped: { type: Boolean, default: false },
      // the state the page last heard of for each app, by name
      states: { type: Object, default: () => ({}) },
    },
    // (app?): open the trigger dialog, with app picked
    emits: ['trigger'],
    data() {
      return {
        expanded: readPref(HEADER_EXPANDED_PREF) === 'true',
        // whether the Apps box is too wide to go beside the summary
        wrapped: false,
      };
    },
    computed: {
      apps() {
        return this.experiment.apps ?? [];
      },
      progressState() {
        return this.progressTracker?.state ?? {};
      },
      progressShown() {
        return (
          !!this.progressState.failure ||
          progressActive(this.progressState.progress)
        );
      },
      toggleInApps() {
        return this.expanded && this.apps.length > 0 && !this.wrapped;
      },
      // the parts the toggle shows and hides
      controls() {
        return this.apps.length
          ? 'exp-summary-facts experiment-header-apps'
          : 'exp-summary-facts';
      },
    },
    mounted() {
      this.measure();
      if (typeof ResizeObserver === 'undefined') return;
      this.observer = new ResizeObserver(() => this.measure());
      this.observer.observe(this.$el);
    },
    beforeUnmount() {
      this.observer?.disconnect();
    },
    methods: {
      // The flex row wraps when the two boxes' bases and gap outgrow it.
      measure() {
        const basis = (selector) => {
          const el = this.$el.querySelector(selector);
          return el ? parseFloat(getComputedStyle(el).flexBasis) || 0 : 0;
        };
        const apps = basis('.experiment-header-apps');
        const need =
          basis('.experiment-header-summary') +
          (parseFloat(getComputedStyle(this.$el).columnGap) || 0) +
          apps;
        this.wrapped = apps > 0 && this.$el.clientWidth < need;
      },
      // Focus follows Details when it moves between the boxes.
      async toggle() {
        this.expanded = !this.expanded;
        writePref(HEADER_EXPANDED_PREF, this.expanded);
        await this.$nextTick();
        this.$el.querySelector('.experiment-header-toggle')?.focus();
      },
    },
  };
</script>

<style scoped>
  /* the experiment's facts on the left, its apps on the right */
  .experiment-header {
    display: flex;
    flex-wrap: wrap;
    align-items: stretch;
    gap: 1rem;
    margin-bottom: 1.5rem;
  }

  .experiment-header-summary {
    flex: 1 1 32rem;
  }

  .experiment-header-apps {
    flex: 0 1 36rem;
  }

  .experiment-header-progress {
    flex: 1 1 100%;
  }
</style>
