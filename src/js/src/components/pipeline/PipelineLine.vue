<template>
  <g :class="'pipeline-line ' + 'weight' + weight">
    <path stroke-width="3.5" :d="path" fill="none" :marker-end="getMarkerEnd()">
    </path>
  </g>
</template>

<script>
  export default {
    props: {
      path: {
        type: String,
      },
      weight: {
        type: Number,
        default: 0,
      },
      showArrow: {
        type: Boolean,
        default: false,
      },
    },
    computed: {},
    methods: {
      getMarkerEnd() {
        if (this.showArrow) {
          return `url(#idArrow${this.weight})`;
        }
      },
    },
  };
</script>
<style lang="css">
  .pipeline-connector {
    stroke: #949393;
  }
</style>
