<template>
  <span class="tag level-tag" :class="badge.cls" :title="level">
    {{ badge.label }}
  </span>
</template>

<script>
  import { levelBadge } from '@/utils/logLevels.js';

  // A log entry's level as a short badge, colored by level, for the Logs
  // page's rows and its details dialog.
  export default {
    name: 'LogLevelTag',
    props: {
      level: { type: String, default: '' },
    },
    computed: {
      badge() {
        return levelBadge(this.level);
      },
    },
  };
</script>

<style scoped>
  /* each level's badge; text on fill is at least 7:1 */
  .level-tag {
    font-weight: 700;
    letter-spacing: 0.04em;
    white-space: nowrap;
  }

  .level-tag.level-debug {
    background-color: #2b2b2b;
    color: #e0e0e0;
    box-shadow: inset 0 0 0 1px #8a8a8a;
  }

  .level-tag.level-info {
    background-color: hsl(207, 61%, 33%);
    color: #fff;
  }

  .level-tag.level-warn {
    background-color: #ffe08a;
    color: #363636;
  }

  .level-tag.level-error {
    background-color: #a91f3d;
    color: #fff;
  }
</style>
