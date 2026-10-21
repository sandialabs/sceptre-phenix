<template>
  <div class="content">
    <div class="columns">
      <div class="column is-1">
        <b-tooltip
          label="return to previous loop"
          type="is-light is-right"
          :delay="1000">
          <button
            class="button is-dark"
            @click="rewinder(exp, run)"
            :disabled="loop == 0">
            <b-icon icon="history" />
          </button>
        </b-tooltip>
      </div>
      <div class="column has-text-centered">
        <span style="font-weight: bold; font-size: x-large" justify="center"
          >Experiment: {{ runName() }}</span
        >
      </div>
      <div class="column is-1">
        <b-tooltip :label="statusLabel()" type="is-light is-left" :delay="1000">
          <button
            class="button is-small"
            :class="statusDecorator()"
            @click="controller(exp, run)"
            :disabled="
              disabled ||
              (running ? !canCancel : !canStart) ||
              ['canceling', 'finalizing', 'interrupted'].includes(
                execution?.state,
              )
            ">
            {{ running ? 'Cancel' : 'Start' }}
          </button>
        </b-tooltip>
      </div>
    </div>
    <button
      v-if="execution?.state === 'interrupted' && canStart"
      class="button is-small"
      @click="recoverer(exp, run)">
      Recover after manual cleanup
    </button>
    <p>
      {{ status }}<span v-if="execution?.error">: {{ execution.error }}</span>
    </p>
    <button
      v-if="execution?.wait === 'breakpoint' && execution?.controller === 'web'"
      class="button is-small"
      @click="openBreakpoint(exp, run)">
      Open breakpoint terminal
    </button>
    <p
      v-if="
        execution?.wait === 'breakpoint' && execution?.controller === 'cli'
      ">
      Continue this breakpoint in its CLI terminal.
    </p>
    <div
      style="margin-top: 10px; border: 2px solid whitesmoke; background: #333">
      <vue-pipeline :ref="runRef()" :pipeline="nodes" @select="viewer" />
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
      execution: { type: Object, default: null },
      disabled: Boolean,
      canStart: Boolean,
      canCancel: Boolean,
      openBreakpoint: Function,
      recoverer: Function,
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
      rewinder: {
        type: Function,
      },
    },

    computed: {
      status() {
        if (this.execution?.wait === 'breakpoint')
          return 'Waiting at breakpoint';
        if (this.execution?.wait === 'timer')
          return `Timed pause until ${this.execution.deadline}`;
        return this.execution?.state ?? 'idle';
      },
    },

    methods: {
      runName() {
        let name = this.run;

        if (this.name) {
          name = this.name;
        }

        if (this.loop == 0) {
          return `${this.exp} - Run ${name}`;
        }

        return `${this.exp} - Run ${name} (loop ${this.loop})`;
      },

      runRef() {
        return 'pipeline-' + this.run + '-' + this.loop;
      },

      statusLabel() {
        return this.running ? 'cancel scorch run' : 'start scorch run';
      },

      statusDecorator() {
        return this.running ? 'is-success' : 'is-danger';
      },

      control() {},

      rewind() {},
    },

    data() {
      return {};
    },
  };
</script>
