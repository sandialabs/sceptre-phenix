<template>
  <div class="exp-progress">
    <p v-if="failure" class="notification is-danger exp-progress-failure">
      {{ failureText }}
    </p>
    <template v-else>
      <b-progress
        ref="bar"
        size="is-medium"
        type="is-warning"
        show-value
        :value="percent"
        format="percent" />
      <p v-if="label" class="exp-progress-label">{{ label }}</p>
    </template>
    <span class="is-sr-only" aria-live="polite">{{ announced }}</span>
  </div>
</template>

<script>
  import {
    failureLabel,
    progressLabel,
    progressPercent,
    progressPhaseKey,
  } from '@/utils/experimentProgress.js';

  // The bar of an experiment start or stop with the phase it is in, or why it
  // failed. Screen readers hear each new phase or failure, not each count.
  export default {
    name: 'ExperimentProgress',
    props: {
      // the experiment's name, for the bar's accessible name
      name: { type: String, default: '' },
      // the server's progress (see experimentProgress.js), or null
      progress: { type: Object, default: null },
      // the failure from errorStarting or errorStopping; it replaces the bar
      failure: { type: Object, default: null },
    },
    data() {
      return { announced: '' };
    },
    computed: {
      percent() {
        return progressPercent(this.progress);
      },
      label() {
        return progressLabel(this.progress);
      },
      failureText() {
        return this.failure ? failureLabel(this.failure) : '';
      },
      barName() {
        const operation = this.progress?.operation;
        const what = ['start', 'stop'].includes(operation)
          ? `the ${operation} of experiment`
          : 'experiment';
        return `Progress of ${what} ${this.name}`.trim();
      },
      // changes only with the operation, the phase or the failure
      phaseKey() {
        return this.failure
          ? `failed:${this.failureText}`
          : progressPhaseKey(this.progress);
      },
    },
    watch: {
      phaseKey: {
        handler() {
          this.announced = this.failure ? this.failureText : this.label;
        },
        immediate: true,
      },
    },
    mounted() {
      this.describeBar();
    },
    updated() {
      this.describeBar();
    },
    methods: {
      // b-progress gives its attributes to its wrapper, not to <progress>
      describeBar() {
        const bar = this.$refs.bar?.$el?.querySelector('progress');
        if (!bar) return;
        bar.setAttribute('aria-label', this.barName);
        if (this.label) bar.setAttribute('aria-valuetext', this.label);
        else bar.removeAttribute('aria-valuetext');
      },
    },
  };
</script>

<style scoped>
  /* each selector outranks `.content p:not(:last-child)` and Buefy's
     `.progress-wrapper:not(:last-child)` margins */
  .exp-progress :deep(.progress-wrapper) {
    margin-bottom: 0;
  }

  .exp-progress .exp-progress-label {
    margin: 0.25rem 0 0;
    font-size: 0.75rem;
    line-height: 1.25;
    overflow-wrap: anywhere;
  }

  .exp-progress .exp-progress-failure {
    margin: 0;
    padding: 0.375rem 0.5rem;
    font-size: 0.75rem;
    line-height: 1.25;
    text-align: start;
    overflow-wrap: anywhere;
  }
</style>
