<!-- Builder icon set: inline SVG that inherits currentColor. v-html only ever
     gets a constant drawing of builder/iconPaths.js, and a custom icon draws as
     an <img> only for a PNG data URL (isIconSrc), so it runs no script. -->
<template>
  <img
    v-if="custom"
    class="builder-icon builder-icon--custom"
    :src="src"
    :width="size"
    :height="size"
    :style="box"
    :alt="title"
    :aria-hidden="title ? undefined : 'true'"
    draggable="false"
    decoding="async" />
  <svg
    v-else
    class="builder-icon"
    :class="`builder-icon--${name}`"
    viewBox="0 0 24 24"
    :width="size"
    :height="size"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    :role="title ? 'img' : 'presentation'"
    :aria-hidden="title ? undefined : 'true'"
    :aria-label="title || undefined">
    <title v-if="title">{{ title }}</title>
    <g v-html="path" />
  </svg>
</template>

<script setup>
  import { computed } from 'vue';

  import { iconPath } from '@/builder/iconPaths.js';
  import { isIconSrc } from '@/builder/icons.js';

  const props = defineProps({
    name: { type: String, required: true },
    size: { type: [Number, String], default: 18 },
    title: { type: String, default: '' },
    // A custom icon's address, from iconSrc in icons.js.
    src: { type: String, default: '' },
  });

  const custom = computed(() => isIconSrc(props.src));

  // The square a custom icon is drawn in, whatever the image's own
  // proportions: the application's styles size an <img> by its picture,
  // which its width and height attributes alone do not prevent.
  const box = computed(() => {
    const side = /^\d+(\.\d+)?$/.test(String(props.size))
      ? `${props.size}px`
      : String(props.size);

    return { width: side, height: side };
  });

  const path = computed(() => iconPath(props.name));
</script>

<style scoped>
  .builder-icon {
    flex: none;
    vertical-align: middle;
  }

  /* All of the image, in its own proportions, inside the icon's square. */
  .builder-icon--custom {
    object-fit: contain;
  }

  /* Forced colors leave the fixed fill of the Black Hat alone. Fill it in
     the color of the text, as its outline, so that the system's colors draw
     all of it. */
  @media (forced-colors: active) {
    .builder-icon--blackhat :deep(.builder-icon__ink) {
      fill: currentColor;
    }
  }
</style>
