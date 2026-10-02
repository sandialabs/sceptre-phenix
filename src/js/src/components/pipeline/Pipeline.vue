<template>
  <svg class="pipeline" :width="width" :height="height">
    <defs>
      <marker
        :id="'idArrow' + i"
        v-for="i in [0, 1, 2, 3, 4, 5]"
        :key="'arrow' + i"
        :class="'weight' + i"
        viewBox="0 0 20 20"
        refX="13"
        refY="10"
        markerUnits="strokeWidth"
        markerWidth="3"
        markerHeight="10"
        orient="auto">
        <path d="M 0 0 L 20 10 L 0 20 z" />
      </marker>
    </defs>

    <pipeline-line
      v-for="(item, index) in lineList"
      :key="'line' + index"
      :showArrow="showArrow"
      :path="item.path"
      :weight="item.weight"
      :lineStyle="lineStyle" />
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
      @click="handleClick"
      @mouseenter="handleMouseEnter"
      @mouseleave="handleMouseLeave" />
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
    emits: ['select', 'mouseenter', 'mouseleave'],
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
      lineStyle: {
        type: String,
        default: 'default',
      },
      showArrow: {
        type: Boolean,
        default: false,
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

      handleMouseEnter(_, node) {
        this.$emit('mouseenter', node);
      },

      handleMouseLeave(_, node) {
        this.$emit('mouseleave', node);
      },

      render() {
        this.service = new Pipeline(
          this.pipeline,
          this.x,
          this.y,
          this.xstep,
          this.ystep,
          this.lineStyle,
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
  .pipeline {
    /* transform: rotate(90deg) */
  }

  .pipeline .weight0 {
    fill: #f5f5f5;
    stroke: #f5f5f5;
  }

  .pipeline .weight1 {
    fill: #f6b44b;
    stroke: #f6b44b;
  }

  .pipeline .weight2 {
    fill: #8cc04f;
    stroke: #8cc04f;
  }

  /* .pipeline .pipeline-node{
  transform: rotate(90deg)
} */
  .pipeline-node-terminal {
    fill: #949393;
  }
  .pipeline-connector-skipped {
    stroke: #949393;
    stroke-opacity: 0.25;
  }
  .pipeline-small-label {
    font-size: 80%;
  }
  .pipeline-big-label.selected,
  .pipeline-small-label.selected {
    font-weight: bold;
  }
  .pipeline-node-terminal {
    fill: #949393;
  }
  .svgResultStatus.no-background .circle-bg {
    opacity: 0;
  }

  .jdl-table td .svgResultStatus {
    vertical-align: middle;
  }

  .pipeline-big-label.selected,
  .pipeline-small-label.selected {
    font-weight: bold;
  }
</style>
