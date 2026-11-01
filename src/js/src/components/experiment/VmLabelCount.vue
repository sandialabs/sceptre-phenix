<!-- A VM's tag icon with how many labels and notes it has; none shows no count. -->
<template>
  <span class="vm-label-count">
    <font-awesome-icon icon="tag" />
    <span v-if="count > 0" class="vm-label-count-badge">{{ count }}</span>
  </span>
</template>

<script>
  import { tagCount } from '@/utils/tagCount';

  export default {
    props: {
      tags: { type: Object, default: null },
    },

    computed: {
      count() {
        return tagCount(this.tags);
      },
    },
  };
</script>

<style scoped>
  .vm-label-count {
    position: relative;
    display: inline-block;
  }

  /* the app's tag gray, not an alert red; white text on it is 5.6:1 */
  .vm-label-count-badge {
    position: absolute;
    top: -0.7em;
    left: 75%;
    min-width: 1.5em;
    padding: 0 0.35em;
    border-radius: 1em;
    background-color: #686868;
    color: #ffffff;
    font-size: 0.7em;
    font-weight: 700;
    line-height: 1.5;
    text-align: center;
  }
</style>
