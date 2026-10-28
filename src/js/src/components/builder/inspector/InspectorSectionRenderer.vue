<!--
  A node's rarely used fields, in a closed native disclosure (see specLayout in
  adapters/forms.js). The summary names the set fields, so nothing set is
  hidden.
-->
<template>
  <details
    v-if="layout.visible"
    class="inspector-section"
    :open="open"
    :data-outline="outlineKey"
    :data-outline-label="layout.label"
    :data-outline-errors="errors ? 'true' : undefined"
    data-testid="inspector-section"
    @toggle="onToggle">
    <!-- nokey: Vue Flow's pan key handler would otherwise take Space from
         the summary, which Space opens and closes. -->
    <summary class="inspector-section__summary nokey" data-outline-focus>
      <builder-icon
        class="inspector-section__chevron"
        name="chevron-down"
        :size="16" />
      <span
        >{{ layout.label
        }}<span class="inspector-section__contents"
          >: {{ contents }}</span
        ></span
      >
      <span v-if="!open && errors" class="inspector-disclosure__error">
        <builder-icon name="close" :size="12" />
        <span class="builder-visually-hidden">, has errors</span>
      </span>
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
  import { resetCollapsed } from './collapse.js';

  // Whether the section was left open, for the next form that shows it.
  let leftOpen = false;

  // Reset view: the next form shows the section closed, as it first was.
  // One on screen closes with the view's other disclosures. The sections
  // and entries that collapse expand again (see collapse.js).
  export function closeSections() {
    leftOpen = false;
    resetCollapsed();
  }
</script>

<script setup>
  import { computed, ref } from 'vue';
  import { composePaths } from '@jsonforms/core';
  import {
    DispatchRenderer,
    rendererProps,
    useJsonFormsLayout,
  } from '@jsonforms/vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import { useInspectorCollapse } from './collapse.js';

  import { startCase } from '@/builder/form-validator.js';

  const props = defineProps(rendererProps());
  const { layout } = useJsonFormsLayout(props);

  const open = ref(leftOpen);
  const collapse = useInspectorCollapse();

  function keyOf(element) {
    return String(element.scope || '')
      .split('/')
      .pop();
  }

  // The data paths of the fields the section holds, and whether one with
  // an error is among them.
  const paths = computed(() =>
    layout.value.uischema.elements.map((element) =>
      composePaths(layout.value.path, keyOf(element)),
    ),
  );
  const errors = computed(() => collapse.hasErrors(paths.value));
  const outlineKey = computed(() => `${layout.value.path}#more-settings`);

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
        const key = keyOf(element);
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

  /* The marker of the browser is replaced by the chevron. */
  .inspector-section__summary {
    display: flex;
    align-items: flex-start;
    gap: 0.4rem;
    min-height: 32px;
    padding: 0.45rem 0.6rem;
    border-radius: var(--bx-radius);
    background: var(--bx-bg-alt);
    color: var(--bx-accent);
    font-size: 0.95rem;
    font-weight: 700;
    list-style: none;
    cursor: pointer;
  }

  .inspector-section__summary::-webkit-details-marker {
    display: none;
  }

  .inspector-section[open] > .inspector-section__summary {
    border-bottom: 1px solid var(--bx-border);
    border-radius: var(--bx-radius) var(--bx-radius) 0 0;
  }

  .inspector-section__summary:hover {
    background: var(--bx-selected-bg);
    text-decoration: underline;
  }

  .inspector-section__chevron {
    flex: none;
    margin-top: 0.1rem;
    transform: rotate(-90deg);
    transition: transform var(--bx-motion);
  }

  .inspector-section[open]
    > .inspector-section__summary
    .inspector-section__chevron {
    transform: none;
  }

  .inspector-section__contents {
    font-weight: 400;
    color: var(--bx-text-muted);
  }

  .inspector-section__item {
    padding: 0 0.5rem;
  }

  .inspector-section[open] > .inspector-section__item:first-of-type {
    padding-top: 0.5rem;
  }

  .inspector-section[open] > .inspector-section__item:last-child {
    padding-bottom: 0.5rem;
  }

  @media (prefers-reduced-motion: reduce) {
    .inspector-section__chevron {
      transition: none;
    }
  }
</style>
