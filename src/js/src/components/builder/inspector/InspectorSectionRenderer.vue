<!--
  A node's rarely used fields (Commands, Delay, Injections, Advanced
  settings, Labels, Annotations), in a section that starts closed, so the
  fields looked for most (Type, General, Hardware, Network) come first. See
  specLayout in adapters/forms.js.

  A native disclosure: its summary is a button that says whether the section
  is open. It names each field the section holds, and how many entries each
  one that is set has, so nothing set is hidden without a word. Whether the
  section is open carries over to the next element selected.
-->
<template>
  <details
    v-if="layout.visible"
    class="inspector-section"
    :open="open"
    data-testid="inspector-section"
    @toggle="onToggle">
    <!-- nokey: Vue Flow's pan key handler would otherwise take Space from
         the summary, which Space opens and closes. -->
    <summary class="inspector-section__summary nokey">
      {{ layout.label
      }}<span class="inspector-section__contents">: {{ contents }}</span>
    </summary>
    <div
      v-for="(element, index) in layout.uischema.elements"
      :key="`${layout.path}-${index}`"
      class="inspector-section__item">
      <dispatch-renderer
        :schema="layout.schema"
        :uischema="element"
        :path="layout.path"
        :enabled="layout.enabled"
        :renderers="layout.renderers"
        :cells="layout.cells" />
    </div>
  </details>
</template>

<script>
  // Whether the section was left open, for the next form that shows it.
  let leftOpen = false;

  // Reset view: the next form shows the section closed, as it first was.
  // One on screen closes with the view's other disclosures.
  export function closeSections() {
    leftOpen = false;
  }
</script>

<script setup>
  import { computed, ref } from 'vue';
  import {
    DispatchRenderer,
    rendererProps,
    useJsonFormsLayout,
  } from '@jsonforms/vue';

  import { startCase } from '@/builder/form-validator.js';

  const props = defineProps(rendererProps());
  const { layout } = useJsonFormsLayout(props);

  const open = ref(leftOpen);

  function onToggle(event) {
    open.value = event.target.open;
    leftOpen = open.value;
  }

  // How many entries a field holds: items of a list, keys of a group or map
  // that are set, or 1 for any other value that is set.
  function entries(value) {
    if (Array.isArray(value)) {
      return value.length;
    }

    if (value && typeof value === 'object') {
      return Object.values(value).filter(
        (entry) => entry !== undefined && entry !== null && entry !== '',
      ).length;
    }

    return value === undefined || value === null || value === '' ? 0 : 1;
  }

  const contents = computed(() =>
    layout.value.uischema.elements
      .map((element) => {
        const key = String(element.scope || '')
          .split('/')
          .pop();
        const title =
          layout.value.schema?.properties?.[key]?.title || startCase(key);
        const count = entries(layout.value.data?.[key]);

        return count ? `${title} (${count})` : title;
      })
      .join(', '),
  );
</script>

<style scoped>
  .inspector-section {
    margin-bottom: 0.75rem;
    border: 1px solid var(--bx-border);
    border-radius: var(--bx-radius);
  }

  .inspector-section__summary {
    min-height: 24px;
    padding: 0.3rem 0.5rem;
    font-size: 0.85rem;
    font-weight: 700;
    cursor: pointer;
  }

  .inspector-section__contents {
    font-weight: 400;
    color: var(--bx-text-muted);
  }

  .inspector-section__item {
    padding: 0 0.5rem;
  }

  .inspector-section[open] > .inspector-section__item:last-child {
    padding-bottom: 0.5rem;
  }
</style>
