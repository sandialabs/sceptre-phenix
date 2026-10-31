<!-- One level of the Node Editor's table of contents (see outlineTree in
     formOutline.js). The deeper levels are this component again. -->
<template>
  <ul class="builder-node-editor-toc__list">
    <li v-for="entry in entries" :key="entry.key">
      <button
        type="button"
        class="builder-node-editor-toc__link"
        :class="{ 'is-active': entry.key === active }"
        :aria-current="entry.key === active ? 'location' : undefined"
        @click="$emit('go', entry.key)">
        <span class="builder-node-editor-toc__label"
          >{{ entry.label
          }}<template v-if="entry.count !== undefined">
            ({{ entry.count }})</template
          ></span
        >
        <span v-if="entry.errors" class="builder-node-editor-toc__error">
          <builder-icon name="close" :size="12" />
          <span class="builder-visually-hidden">, has errors</span>
        </span>
      </button>
      <node-editor-toc
        v-if="entry.children.length"
        :entries="entry.children"
        :active="active"
        @go="$emit('go', $event)" />
    </li>
  </ul>
</template>

<script setup>
  import BuilderIcon from '../BuilderIcon.vue';

  defineProps({
    // The entries of this level, each {key, label, count, errors,
    // children}.
    entries: { type: Array, required: true },
    // The key of the entry in view, or ''.
    active: { type: String, default: '' },
  });

  defineEmits(['go']);
</script>

<style scoped>
  .builder-node-editor-toc__list {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  /* A deeper level is indented, with a line at its side. */
  .builder-node-editor-toc__list .builder-node-editor-toc__list {
    margin-left: 0.6rem;
    padding-left: 0.4rem;
    border-left: 1px solid var(--bx-border);
  }

  /* At least 24px tall (WCAG 2.5.8). The entry in view has a bar at its
     side and is bold, as well as its fill, so forced colors keep it. */
  .builder-node-editor-toc__link {
    display: flex;
    align-items: baseline;
    gap: 0.3rem;
    width: 100%;
    min-height: 24px;
    padding: 0.15rem 0.4rem;
    border: 0;
    border-left: 3px solid transparent;
    border-radius: 0 var(--bx-radius) var(--bx-radius) 0;
    background: none;
    color: var(--bx-text);
    font: inherit;
    font-size: 0.8rem;
    text-align: left;
    cursor: pointer;
  }

  .builder-node-editor-toc__link:hover {
    background: var(--bx-bg-alt);
    text-decoration: underline;
  }

  .builder-node-editor-toc__link.is-active {
    border-left-color: var(--bx-accent);
    background: var(--bx-selected-bg);
    font-weight: 700;
  }

  .builder-node-editor-toc__label {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .builder-node-editor-toc__error {
    flex: none;
    color: var(--bx-danger);
  }
</style>
