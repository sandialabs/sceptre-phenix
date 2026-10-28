<!--
  A layer's icon and title. The title takes the layer's color only where it
  keeps 4.5:1 (see layerInk), else a swatch shows. The icon library comes in as
  a prop or injection, so the renderers stay free of the API client.
-->
<template>
  <span
    class="builder-layer-name"
    :class="{
      'has-ink-light': !bare && ink.light,
      'has-ink-dark': !bare && ink.dark,
      'is-bare': bare,
    }"
    :style="color ? { '--layer-color': color } : undefined"
    data-testid="layer-name">
    <builder-icon
      v-if="iconKey || src"
      class="builder-layer-name__icon"
      :name="iconKey || 'image'"
      :src="src"
      :size="16"
      data-testid="layer-name-icon" />
    <span v-if="color" class="builder-layer-name__swatch" aria-hidden="true" />
    <span v-if="!bare" class="builder-layer-name__title">
      {{ layer.label }}
    </span>
  </span>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';

  import { useInspectorIconLibrary } from '../inspector/control.js';

  import { layerInk, layerLook } from '@/builder/layerLook.js';

  const props = defineProps({
    // {id, label, iconKey?, icon?, color?}
    layer: { type: Object, required: true },
    // The custom icons a diagram carries (its `icons`), which go before the
    // server's icon library's.
    icons: { type: Object, default: null },
    // The server's icon library (see createIconLibrary in iconLibrary.js),
    // where no Inspector or layers editor provides one.
    library: { type: Object, default: null },
    // Only the icon and the swatch (see above).
    bare: { type: Boolean, default: false },
  });

  const provided = useInspectorIconLibrary();
  const look = computed(() =>
    layerLook(props.layer, props.icons, props.library || provided),
  );
  const iconKey = computed(() => look.value.iconKey);
  const src = computed(() => look.value.src);
  const color = computed(() => look.value.color);
  const ink = computed(() => layerInk(color.value));
</script>

<style scoped>
  .builder-layer-name {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    min-width: 0;
    max-width: 100%;
  }

  .builder-layer-name__icon {
    flex: none;
  }

  .builder-layer-name__title {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  /* A small square of the layer's color, framed in the text color so it
     shows on either theme (WCAG 1.4.11). */
  .builder-layer-name__swatch {
    flex: none;
    width: 0.8em;
    height: 0.8em;
    border: 1px solid var(--bx-text);
    border-radius: 2px;
    background: var(--layer-color);
  }
</style>

<!-- The theme shown is an attribute of the page's root, outside this
     component, so these rules are not scoped. -->
<style>
  /* In a theme where the title reads in the layer's color, the title takes
     it and the swatch goes. */
  .builder-root:not([data-builder-theme='dark'])
    .builder-layer-name.has-ink-light
    .builder-layer-name__title,
  .builder-root[data-builder-theme='dark']
    .builder-layer-name.has-ink-dark
    .builder-layer-name__title {
    color: var(--layer-color);
  }

  .builder-root:not([data-builder-theme='dark'])
    .builder-layer-name.has-ink-light
    .builder-layer-name__swatch,
  .builder-root[data-builder-theme='dark']
    .builder-layer-name.has-ink-dark
    .builder-layer-name__swatch {
    display: none;
  }

  /* Forced colors replace the title's color, so the swatch shows in every
     theme, in its own color. It wins over the theme rules above. */
  @media (forced-colors: active) {
    .builder-layer-name .builder-layer-name__swatch {
      display: inline-block !important;
      forced-color-adjust: none;
      border-color: CanvasText;
    }
  }
</style>
