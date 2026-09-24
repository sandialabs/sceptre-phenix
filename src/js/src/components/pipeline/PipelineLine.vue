<template>
  <g :class="'pipeline-line ' + 'weight' + weight">
    <path
      stroke-width="3.5"
      :d="this.path"
      fill="none"
      :marker-end="getMarkerEnd()">
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
    data() {
      return {
        ystep: this.y1 + 30,
      };
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
    stroke: var(--status-neutral);
  }
</style>
