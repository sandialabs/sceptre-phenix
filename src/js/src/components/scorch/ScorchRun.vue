<template>
  <div class="content">
    <div class="run-header">
      <div>
        <b-tooltip
          v-if="loop > 0"
          label="return to previous loop"
          type="is-light is-right"
          :delay="1000">
          <button
            class="button is-dark"
            aria-label="Return to the previous loop"
            @click="rewinder(exp, run)">
            <b-icon icon="history" />
          </button>
        </b-tooltip>
      </div>
      <span class="run-title">{{ title }}</span>
      <div class="run-status">
        <span class="tag is-medium" :class="statusDecorator">
          {{ status }}
        </span>
        <b-tooltip
          v-if="clearable"
          label="clear this run's status"
          type="is-light is-left">
          <button
            class="button is-dark"
            :disabled="!canClear"
            @click="clearer(exp, run)">
            <b-icon icon="eraser" />
            <span>Clear</span>
          </button>
        </b-tooltip>
        <b-tooltip
          v-if="hasCleanup"
          label="run only this run's cleanup stage"
          type="is-light is-left">
          <button
            class="button is-dark"
            :disabled="!canCleanUp"
            @click="cleaner(exp, run)">
            <b-icon icon="broom" />
            <span>Cleanup</span>
          </button>
        </b-tooltip>
        <b-tooltip :label="statusLabel" type="is-light is-left">
          <button
            class="button is-dark"
            :class="{ 'is-loading': pending }"
            :disabled="pending"
            :aria-label="statusLabel"
            @click="controller(exp, run)">
            <b-icon :icon="running ? 'stop' : 'play'" />
          </button>
        </b-tooltip>
      </div>
    </div>
    <div
      style="margin-top: 10px; border: 2px solid whitesmoke; background: #333">
      <vue-pipeline :pipeline="nodes" @select="viewer" />
    </div>
  </div>
</template>

<script>
  import VuePipeline from '@/components/pipeline/Pipeline.vue';

  export default {
    components: {
      'vue-pipeline': VuePipeline,
    },

    props: {
      exp: {
        type: String,
      },
      run: {
        type: Number,
        default: 0,
      },
      name: {
        type: String,
      },
      loop: {
        type: Number,
        default: 0,
      },
      running: {
        type: Boolean,
        default: false,
      },
      // a start or stop request is waiting on the server
      pending: {
        type: Boolean,
        default: false,
      },
      // whether the run defines cleanup components
      hasCleanup: {
        type: Boolean,
        default: false,
      },
      // false while any run of the experiment is busy
      canCleanUp: {
        type: Boolean,
        default: true,
      },
      // whether to offer clearing the run's status, and whether it can be
      // cleared now
      clearable: {
        type: Boolean,
        default: false,
      },
      canClear: {
        type: Boolean,
        default: false,
      },
      nodes: {
        type: Array,
        default: () => [],
      },
      viewer: {
        type: Function,
      },
      controller: {
        type: Function,
      },
      cleaner: {
        type: Function,
      },
      clearer: {
        type: Function,
      },
      rewinder: {
        type: Function,
      },
    },

    computed: {
      title() {
        const title = `Run: ${this.name || this.run}`;
        return this.loop == 0 ? title : `${title} (loop ${this.loop})`;
      },

      status() {
        return this.running ? 'running' : 'stopped';
      },

      statusLabel() {
        return this.running ? 'stop this run' : 'start this run';
      },

      statusDecorator() {
        return this.running ? 'is-success' : 'is-danger';
      },
    },
  };
</script>

<style scoped>
  /* loop button left, run name centered, status and actions right */
  .run-header {
    display: grid;
    grid-template-columns: 1fr auto 1fr;
    align-items: center;
    gap: 1rem;
  }

  .run-title {
    font-weight: bold;
    font-size: x-large;
  }

  .run-status {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 0.5rem;
  }
</style>
