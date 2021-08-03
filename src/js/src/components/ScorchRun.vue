<template>
  <div class="content">
    <div class="columns"> 
      <div class="column is-1">
        <b-tooltip label="return to previous loop" type="is-light is-right" :delay="1000">
          <button class="button is-dark" @click="rewinder(exp, run)" :disabled="loop == 0">
            <b-icon icon="history" />
          </button>
        </b-tooltip>
      </div>
      <div class="column has-text-centered">
        <span style="font-weight: bold; font-size: x-large;" justify="center">Experiment: {{ runName() }}</span>
      </div>
      <div class="column is-1">
        <b-tooltip :label="statusLabel()" type="is-light is-left" :delay="1000">
          <span class="tag is-medium" :class="statusDecorator()">
            <div class="field" @click="controller(exp, run)">
              {{ status }}
            </div>
          </span>
        </b-tooltip>
      </div>
    </div>
    <div style="margin-top: 10px; border: 2px solid whitesmoke; background: #333;">
      <vue-pipeline :ref="runRef()" :pipeline="nodes" @select="viewer" />
    </div>
  </div>
</template>

<script>
  import VuePipeline from './pipeline/Pipeline.vue'

  export default {
    components: {
      'vue-pipeline': VuePipeline
    },

    props: {
      exp: {
        type: String
      },
      run: {
        type: Number,
        default: 0
      },
      loop: {
        type: Number,
        default: 0
      },
      running: {
        type: Boolean,
        default: false
      },
      nodes: {
        type: Array,
        default: () => []
      },
      viewer: {
        type: Function
      },
      controller: {
        type: Function
      },
      rewinder: {
        type: Function
      }
    },

    computed: {
      status () {
        return this.running ? "running" : "stopped";
      },
    },

    methods: {
      runName () {
        if (this.loop == 0) {
          return this.exp + ' - Run ' + this.run;
        }

        return this.exp + ' - Run ' + this.run + ' (loop ' + this.loop + ')'
      },

      runRef () {
        return 'pipeline-' + this.run + '-' + this.loop;
      },

      statusLabel () {
        return this.running ? "cancel scorch run" : "start scorch run";
      },

      statusDecorator () {
        return this.running ? 'is-success' : 'is-danger';
      }, 

      control () {

      },

      rewind () {

      }
    },

    data () {
      return {}
    }
  }
</script>
