<!-- The top or bottom banner of one console pane, such as a classification
     marking. The lines are the server's plain text, drawn as text. A banner
     with no lines draws nothing. -->
<script setup>
  import { computed } from 'vue';

  const props = defineProps({
    // the banner of the session JSON: { lines, backgroundColor, textColor }
    banner: { type: Object, default: null },
    position: { type: String, default: 'top' },
  });

  const lines = computed(() => props.banner?.lines ?? []);
  const style = computed(() => ({
    backgroundColor: props.banner?.backgroundColor,
    color: props.banner?.textColor,
  }));
</script>

<template>
  <div
    v-if="lines.length"
    :class="['vnc-banner', `vnc-banner--${position}`]"
    :style="style">
    <div
      v-for="(line, i) in lines"
      :key="i"
      class="vnc-banner-line"
      v-text="line"></div>
  </div>
</template>

<style scoped>
  .vnc-banner {
    flex: none;
    padding: 1px 8px;
    font-size: 0.8rem;
    font-weight: 700;
    line-height: 1.3;
    text-align: center;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
</style>
