<!-- The diagram's vCPUs and memory, and its selection's totals (see
     resources.js). Plain text: edits announce themselves. -->
<template>
  <p
    v-if="resources.diagram.devices"
    class="builder-resources"
    data-testid="builder-resources">
    <span class="builder-resources__total">
      <span class="builder-visually-hidden">Devices take </span>
      <span>{{ diagram[0] }}</span
      ><span class="builder-resources__comma">, </span
      ><span>{{ diagram[1] }}</span>
    </span>
    <span
      v-if="resources.selection.devices"
      class="builder-resources__total builder-resources__total--selection"
      data-testid="resources-selection">
      <span class="builder-visually-hidden">. </span>
      Selected: {{ resourcesText(resources.selection) }}
    </span>
  </p>
</template>

<script setup>
  import { computed } from 'vue';

  import {
    diagramResources,
    resourcesParts,
    resourcesText,
    selectionResources,
  } from '@/builder/resources.js';
  import { useBuilderStore } from '@/builder/store.js';

  const store = useBuilderStore();

  const resources = computed(() => ({
    diagram: diagramResources(store.doc),
    selection: selectionResources(store.doc, store.selection.nodes),
  }));

  const diagram = computed(() => resourcesParts(resources.value.diagram));
</script>

<style scoped>
  /* In a thin box as tall as the counts' box. Each total stays on one
     line. */
  .builder-resources {
    display: flex;
    flex: none;
    flex-direction: column;
    justify-content: center;
    min-height: calc(24px + 0.7rem + 2px);
    margin: 0;
    padding: 0.1rem 0.5rem;
    border: 1px solid var(--bx-border);
    border-radius: var(--bx-radius);
    font-size: 0.8rem;
    line-height: 1.15;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .builder-resources__total--selection {
    color: var(--bx-text-muted);
  }

  /* The step at which every header button shows its icon alone (see
     builder.css). */
  @container builder-header (max-width: 77rem) {
    .builder-resources {
      padding-inline: 0.4rem;
      font-size: 0.75rem;
    }

    .builder-resources__total {
      display: flex;
      flex-direction: column;
    }

    .builder-resources__comma,
    .builder-resources__total--selection {
      position: absolute;
      width: 1px;
      height: 1px;
      margin: -1px;
      padding: 0;
      overflow: hidden;
      clip: rect(0 0 0 0);
      white-space: nowrap;
      border: 0;
    }
  }
</style>
