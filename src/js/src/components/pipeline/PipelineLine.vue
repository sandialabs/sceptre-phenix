<template>
  <g :class="'pipeline-line ' + 'weight' + weight">
    <path stroke-width="3.5" :d="path" fill="none"></path>
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
    },
  };
</script>
