<!--
  The rest of the Inspector's Diagram section, below the diagram's Name and
  Description: the annotations of the config the diagram was imported from,
  and its scenario, with the hosts each app runs on. Both are read only
  here. Edit scenario opens the Scenario dialog the toolbar's Scenario
  button opens, and like it, not in a read-only draft.

  A stored scenario reference carries no content (see ScenarioDialog), so
  the apps of a stored scenario are read from its config (see the store's
  fetchScenario), when the role may read it.
-->
<template>
  <!-- A diagram drawn here, not imported, has no source to list. -->
  <div
    v-if="source"
    class="inspector-diagram"
    data-testid="inspector-annotations">
    <h3>Annotations</h3>
    <p class="inspector-diagram__from" data-testid="inspector-source">
      From {{ source.kind }} <strong>{{ source.name }}</strong
      ><template v-if="source.imported"
        >, imported
        <time :datetime="source.importedAt">{{
          source.imported
        }}</time></template
      >
    </p>
    <dl v-if="annotations.length" class="inspector-diagram__list">
      <div v-for="([key, value], index) in annotations" :key="key">
        <dt :id="`inspector-annotation-${index}`">{{ key }}</dt>
        <!-- A long value scrolls in its box, which then takes focus, so
             the keys scroll it too. v-text keeps its line breaks and
             spaces exactly, with nothing around them. -->
        <dd>
          <div
            :ref="(element) => observe(key, element)"
            v-text="value"
            class="inspector-diagram__value"
            :role="scrolling.has(key) ? 'region' : undefined"
            :aria-labelledby="
              scrolling.has(key) ? `inspector-annotation-${index}` : undefined
            "
            :tabindex="scrolling.has(key) ? 0 : undefined" />
        </dd>
      </div>
    </dl>
    <p v-else class="inspector-diagram__none">No annotations.</p>
  </div>

  <div class="inspector-diagram" data-testid="inspector-scenario">
    <h3>Scenario</h3>
    <p v-if="!scenario" class="inspector-diagram__none">No scenario.</p>
    <template v-else>
      <p class="inspector-diagram__from" data-testid="inspector-scenario-name">
        {{ scenario.kind === 'stored' ? 'Stored' : 'Uploaded' }} scenario
        <strong v-if="scenario.name">{{ scenario.name }}</strong>
      </p>
      <p v-if="apps.note" class="inspector-diagram__none">{{ apps.note }}</p>
      <template v-else-if="apps.list.length">
        <p class="inspector-diagram__label">Apps and their hosts</p>
        <dl
          class="inspector-diagram__list"
          data-testid="inspector-scenario-apps">
          <div v-for="(app, index) in apps.list" :key="`${index}-${app.name}`">
            <dt>
              {{ app.name }}
              <span v-if="app.disabled" class="inspector-diagram__muted"
                >(disabled)</span
              >
            </dt>
            <dd class="inspector-diagram__hosts">
              {{ app.hosts.length ? app.hosts.join(', ') : 'No hosts' }}
            </dd>
          </div>
        </dl>
      </template>
      <p v-else class="inspector-diagram__none">No apps.</p>
    </template>
    <!-- One button, whose name follows the scenario, so focus stays on it
         when the dialog closes after adding or removing one. -->
    <button
      v-if="!store.readOnly"
      type="button"
      class="builder-button"
      aria-haspopup="dialog"
      data-testid="inspector-scenario-edit"
      @click="$emit('scenario')">
      <builder-icon name="document" :size="14" />
      {{ scenario ? 'Edit scenario' : 'Add scenario' }}
    </button>
  </div>
</template>

<script setup>
  import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';

  import { formatTimestamp } from '@/builder/format.js';
  import {
    scenarioApps,
    sourceAnnotations,
    storedScenarioName,
  } from '@/builder/model.js';
  import { useBuilderStore } from '@/builder/store.js';

  defineEmits(['scenario']);

  const store = useBuilderStore();

  const KINDS = { topology: 'Topology', experiment: 'Experiment' };

  // Where the diagram came from, when it was imported from a config, and
  // when, in the viewer's locale.
  const source = computed(() => {
    const { kind, name, importedAt } = store.doc.source || {};

    return KINDS[kind] && name
      ? {
          kind: KINDS[kind],
          name,
          importedAt,
          imported: formatTimestamp(importedAt),
        }
      : null;
  });

  const annotations = computed(() => sourceAnnotations(store.doc));

  const scenario = computed(() => store.doc.scenario || null);

  // A stored reference without content names the scenario to read.
  const storedName = computed(() => storedScenarioName(scenario.value));

  watch(
    storedName,
    (name) => {
      if (name) {
        store.fetchScenario(name);
      }
    },
    { immediate: true },
  );

  const PROBLEMS = {
    forbidden:
      'Your role cannot read this scenario, so its apps are not listed.',
    missing: 'The server has no scenario of this name.',
    invalid: 'The server has no scenario of this name.',
  };

  // The apps to list, or what to say instead of them.
  const apps = computed(() => {
    if (!storedName.value) {
      return { list: scenarioApps(scenario.value?.content), note: '' };
    }

    const read = store.storedScenarios[storedName.value];

    if (read?.content) {
      return { list: scenarioApps(read.content), note: '' };
    }

    if (!read || read.loading) {
      return { list: [], note: 'Reading its apps…' };
    }

    return {
      list: [],
      note: PROBLEMS[read.problem] || 'Its apps could not be read.',
    };
  });

  // --- values that scroll ----------------------------------------------

  // The keys whose values are taller than their box, which then scrolls.
  const scrolling = ref(new Set());
  const boxes = new Map();
  let resizing = null;

  function measure() {
    const next = [...boxes]
      .filter(([, element]) => element.scrollHeight > element.clientHeight)
      .map(([key]) => key);

    if (
      next.length !== scrolling.value.size ||
      next.some((key) => !scrolling.value.has(key))
    ) {
      scrolling.value = new Set(next);
    }
  }

  function observe(key, element) {
    const previous = boxes.get(key);

    if (previous === element) {
      return;
    }

    if (previous) {
      resizing?.unobserve(previous);
      boxes.delete(key);
    }

    if (element) {
      boxes.set(key, element);
      resizing?.observe(element);
    }
  }

  onMounted(() => {
    if (typeof ResizeObserver !== 'undefined') {
      resizing = new ResizeObserver(measure);
      boxes.forEach((element) => resizing.observe(element));
    }

    measure();
  });

  onBeforeUnmount(() => resizing?.disconnect());
</script>

<style scoped>
  .inspector-diagram {
    font-size: 0.8rem;
  }

  .inspector-diagram h3 {
    font-weight: 700;
    font-size: 0.85rem;
    margin: 0.75rem 0 0.25rem;
  }

  .inspector-diagram p {
    margin: 0 0 0.35rem;
  }

  .inspector-diagram__label {
    font-weight: 600;
  }

  .inspector-diagram__none,
  .inspector-diagram__muted {
    color: var(--bx-text-muted);
  }

  /* Each entry is a key over its value, indented, which reads better in a
     narrow column than two columns would. */
  .inspector-diagram__list {
    margin: 0 0 0.4rem;
  }

  .inspector-diagram__list > div + div {
    margin-top: 0.3rem;
  }

  .inspector-diagram__list dt {
    font-weight: 600;
  }

  .inspector-diagram__list dd {
    margin: 0 0 0 0.6rem;
  }

  /* Values keep their line breaks and wrap anywhere; a long one scrolls
     in a box of about eight lines. */
  .inspector-diagram__value {
    max-height: 8.5rem;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .inspector-diagram__hosts {
    overflow-wrap: anywhere;
  }

  .inspector-diagram__value[tabindex] {
    padding: 0.15rem 0.3rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: var(--bx-radius);
    background: var(--bx-bg-alt);
  }
</style>
