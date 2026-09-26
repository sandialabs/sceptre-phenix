<!--
  Diagram checks, in the editor header.

  A button that counts the diagram's errors and warnings, and opens a dialog
  listing them by the node or connection they are about. Choosing an issue
  there selects its node or connection, scrolls the canvas into view, and
  moves focus to it on the canvas, which pans it into view. The button is
  always there, so the header does not shift as the first issue comes or the
  last one goes. Its text changes with every edit and is not a live region:
  the Inspector announces the errors an edit makes, and the counts are read
  when the button is.

  Drive images are checked against the disk images the server has, so the
  editor reads them here as it opens, and again when the page comes back into
  view after a while, since images are added elsewhere.
-->
<template>
  <button
    type="button"
    class="builder-button builder-checks"
    aria-haspopup="dialog"
    data-testid="builder-checks"
    @click="open = true">
    <!-- Named by the hidden text, which says the same as the counts shown
         and holds the commas that the icons stand in for. -->
    <span
      v-for="part in parts"
      :key="part.level"
      class="builder-checks__count"
      :data-level="part.level"
      aria-hidden="true">
      <builder-icon :name="part.icon" :size="14" />
      {{ part.text }}
    </span>
    <span class="builder-visually-hidden">{{ name }}</span>
  </button>

  <builder-dialog
    v-if="open"
    title="Diagram checks"
    title-id="builder-checks-title"
    data-testid="checks-dialog"
    @close="open = false">
    <p data-testid="checks-summary">{{ summary }}</p>
    <p v-if="store.disks === null" class="builder-hint">
      Drive images are not checked: the server did not list its disk images.
    </p>

    <div
      v-for="section in sections"
      :key="section.kind"
      class="builder-checks__section">
      <h3>{{ section.title }}</h3>
      <div
        v-for="(group, groupIndex) in section.groups"
        :key="group.id"
        class="builder-checks__group">
        <h4 :id="`builder-checks-${section.kind}-${groupIndex}`">
          {{ group.title }}
        </h4>
        <!-- Each button is named by its issue and described by the element
             it shows, which its heading names. -->
        <ul class="builder-checks__list">
          <li
            v-for="(issue, index) in group.issues"
            :key="`${issue.path}-${index}`"
            :data-level="issue.level">
            <button
              type="button"
              class="builder-checks__issue"
              :aria-describedby="`builder-checks-${section.kind}-${groupIndex}`"
              @click="show(section.kind, group.id)">
              <builder-icon :name="ICONS[issue.level]" :size="14" />
              <span>
                <strong>{{ LABELS[issue.level] }}:</strong>
                {{ issue.text }}
              </span>
            </button>
          </li>
        </ul>
      </div>
    </div>

    <div v-if="groups.diagram.length" class="builder-checks__section">
      <h3>Diagram</h3>
      <ul class="builder-checks__list">
        <li
          v-for="(issue, index) in groups.diagram"
          :key="`${issue.path}-${index}`"
          :data-level="issue.level"
          class="builder-checks__text">
          <builder-icon :name="ICONS[issue.level]" :size="14" />
          <span>
            <strong>{{ LABELS[issue.level] }}:</strong>
            {{ issue.text }}
          </span>
        </li>
      </ul>
    </div>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';

  import BuilderDialog from './BuilderDialog.vue';
  import BuilderIcon from './BuilderIcon.vue';

  import { count } from '@/builder/announce.js';
  import { countsText, issueCounts, issueGroups } from '@/builder/issues.js';
  import { selectionItemName } from '@/builder/selection.js';
  import { useBuilderStore } from '@/builder/store.js';

  const store = useBuilderStore();

  // Told apart by shape as well as color.
  const ICONS = { error: 'close', warning: 'warning' };
  const LABELS = { error: 'Error', warning: 'Warning' };

  const open = ref(false);

  const counts = computed(() => issueCounts(store.issues));

  // What the button shows: each count there is, or that there is none.
  const parts = computed(() => {
    const { errors, warnings } = counts.value;
    const shown = [
      errors && {
        level: 'error',
        icon: ICONS.error,
        text: count(errors, 'error'),
      },
      warnings && {
        level: 'warning',
        icon: ICONS.warning,
        text: count(warnings, 'warning'),
      },
    ].filter(Boolean);

    return shown.length
      ? shown
      : [{ level: 'none', icon: 'check', text: 'No issues' }];
  });
  const name = computed(
    () => `${parts.value.map((part) => part.text).join(', ')} in this diagram`,
  );
  const groups = computed(() => issueGroups(store.doc, store.issues));
  const sections = computed(() =>
    [
      { kind: 'nodes', title: 'Nodes', groups: groups.value.nodes },
      { kind: 'edges', title: 'Connections', groups: groups.value.edges },
    ].filter((section) => section.groups.length),
  );

  const summary = computed(() => {
    const text = countsText(counts.value).replace(', ', ' and ');

    if (!text) {
      return 'This diagram has no errors or warnings.';
    }

    const blocking = counts.value.errors
      ? ' Errors must be fixed before the diagram can be published.'
      : ' Warnings do not stop the diagram from being published.';
    const choose = sections.value.length
      ? ' Choose one to show its node or connection on the canvas.'
      : '';

    return `This diagram has ${text}.${blocking}${choose}`;
  });

  /**
   * Closes the dialog, selects the node or connection, and moves focus to
   * it on the canvas. Focus there is shown as keyboard focus, whatever
   * pressed the button: the canvas pans only such focus into view, and it
   * marks where focus went. Should the item not be on the canvas, focus
   * stays on the checks button, where the dialog returned it.
   *
   * The page does not scroll to the focused item, which would scroll the
   * canvas's own clipped layers; in the narrow stacked layout the canvas
   * can be below the fold, so the page scrolls the canvas into view first.
   *
   * @param {'nodes'|'edges'} kind
   * @param {string} id
   */
  async function show(kind, id) {
    open.value = false;
    await nextTick();

    const item = { kind, id };

    store.select({ nodes: [], edges: [], [kind]: [id] });
    store.announce(`Selected ${selectionItemName(store.doc, item)}`);
    await nextTick();

    const wrapper = kind === 'nodes' ? 'vue-flow__node' : 'vue-flow__edge';
    const canvas = document.getElementById('builder-canvas');
    const element = canvas?.querySelector(
      `.${wrapper}[data-id="${CSS.escape(id)}"]`,
    );

    if (element) {
      canvas.scrollIntoView({ block: 'nearest' });
      element.focus({ preventScroll: true, focusVisible: true });
    }
  }

  // The images are read as the editor opens, and again when the page comes
  // back into view once they are a while old. Reading them asks minimega
  // about every image file, so not every time.
  const DISKS_STALE_MS = 30000;
  let disksReadAt = 0;

  function readDisks() {
    disksReadAt = Date.now();
    store.fetchDisks();
  }

  function onVisibility() {
    if (
      document.visibilityState === 'visible' &&
      Date.now() - disksReadAt >= DISKS_STALE_MS
    ) {
      readDisks();
    }
  }

  onMounted(() => {
    readDisks();
    document.addEventListener('visibilitychange', onVisibility);
  });

  onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', onVisibility);
  });
</script>

<style scoped>
  .builder-checks {
    flex: none;
    gap: 0.6rem;
  }

  .builder-checks__count {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
  }

  .builder-checks__count[data-level='error'] .builder-icon,
  .builder-checks__list [data-level='error'] .builder-icon,
  .builder-checks__list [data-level='error'] strong {
    color: var(--bx-danger);
  }

  .builder-checks__count[data-level='warning'] .builder-icon,
  .builder-checks__list [data-level='warning'] .builder-icon,
  .builder-checks__list [data-level='warning'] strong {
    color: var(--bx-warning);
  }

  .builder-checks__count[data-level='none'] .builder-icon {
    color: var(--bx-success);
  }

  .builder-checks__section h3 {
    font-weight: 700;
    font-size: 0.95rem;
    margin: 0.75rem 0 0.35rem;
  }

  .builder-checks__group h4 {
    font-weight: 600;
    font-size: 0.85rem;
    margin: 0.5rem 0 0.25rem;
  }

  .builder-checks__list {
    list-style: none;
    margin: 0;
    padding: 0;
    font-size: 0.85rem;
  }

  .builder-checks__list li + li {
    margin-top: 0.25rem;
  }

  .builder-checks__issue,
  .builder-checks__text {
    display: flex;
    align-items: baseline;
    gap: 0.4rem;
    width: 100%;
    padding: 0.35rem 0.5rem;
    text-align: left;
    color: var(--bx-text);
    overflow-wrap: anywhere;
  }

  .builder-checks__issue {
    min-height: 2rem;
    border: 1px solid var(--bx-border);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    font: inherit;
    cursor: pointer;
  }

  .builder-checks__issue:hover {
    background: var(--bx-bg-alt);
  }

  .builder-checks__issue .builder-icon,
  .builder-checks__text .builder-icon {
    align-self: center;
  }
</style>
