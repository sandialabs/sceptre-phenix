<template>
  <svg class="pipeline" :width="width" :height="height">
    <pipeline-line
      v-for="(item, index) in lineList"
      :key="'line' + index"
      :path="item.path"
      :weight="item.weight" />
    <pipeline-node
      v-for="(item, idx) in nodeList"
      :key="'node' + idx"
      :hint="item.hint"
      :status="item.status"
      :label="item.name"
      :x="item.x"
      :y="item.y"
      :node="item"
      :index="idx"
      @click="handleClick" />
  </svg>
</template>
<script>
  import PipelineNode from '@/components/pipeline/PipelineNode.vue';
  import PipelineLine from '@/components/pipeline/PipelineLine.vue';
  import { Pipeline } from '@/components/pipeline/service.js';

  export default {
    // Explicit multi-word name: this component is already registered
    // elsewhere as `vue-pipeline` (see ScorchRun.vue); this also satisfies
    // vue/multi-word-component-names, which otherwise flags the filename
    // "Pipeline" as ambiguous with a future native <pipeline> element.
    name: 'VuePipeline',
    components: {
      PipelineNode,
      PipelineLine,
    },
    emits: ['select'],
    props: {
      x: {
        type: Number,
        default: 70,
      },
      y: {
        type: Number,
        default: 100,
      },
      xstep: {
        type: Number,
        default: 125,
      },
      ystep: {
        type: Number,
        default: 75,
      },
      pipeline: {
        type: Array,
        default: () => [],
      },
    },

    data() {
      return {
        nodeList: [],
        width: 300,
        height: 300,
        lineList: [],
        service: {},
      };
    },

    watch: {
      pipeline: 'render',
    },

    methods: {
      handleClick(_, node) {
        this.$emit('select', node);
      },

      render() {
        this.service = new Pipeline(
          this.pipeline,
          this.x,
          this.y,
          this.xstep,
          this.ystep,
        );

        if (this.service.hasCircle()) {
          throw new Error(
            'Error data, The graph should not contain any circle!',
          );
        }

        this.service.calculateAllPosition();
        this.nodeList = this.service.nodes;
        this.lineList = this.service.getLines();
        this.width = this.service.width;
        this.height = this.service.height;
      },
    },

    mounted() {
      this.render();
    },
  };
</script>

<style>
  .pipeline .weight0 {
    stroke: #f5f5f5;
  }

  .pipeline .weight1 {
    stroke: #f6b44b;
  }

  .pipeline .weight2 {
    stroke: #8cc04f;
  }
</style>
