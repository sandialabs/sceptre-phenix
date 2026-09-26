<!--
  The diagram's counts, in the editor header: an icon and a number for each
  kind of element. Screen readers read each count in words ("2 devices"),
  and each count shows its words as a tooltip on hover and on focus (WCAG
  1.4.13), so the icons can be learned without hovering. The list is one
  Tab stop: the arrow keys, Home and End move between the counts, as in a
  toolbar, and the count last focused keeps the Tab stop. The counts change
  with every edit and are not a live region: edits announce themselves.
-->
<template>
  <div class="builder-counts">
    <!-- role="list": Safari drops a list's role along with its bullets. -->
    <ul
      class="builder-counts__list"
      role="list"
      aria-label="Diagram contents"
      data-testid="builder-summary"
      @keydown="onKeydown">
      <li
        v-for="(entry, index) in counts"
        :key="entry.key"
        class="builder-counts__item"
        :data-kind="entry.key"
        :tabindex="index === current ? 0 : -1"
        @focus="onFocus($event, index, entry)"
        @blur="hideTip"
        @mouseenter="showTip($event, entry.text)"
        @mouseleave="leaveOne">
        <builder-icon :name="ICONS[entry.key]" :size="14" />
        <!-- The number is shown and the words after it are read, with the
             commas, so the list's text is one line: "2 devices, 1 switch". -->
        <span>{{ entry.count }}</span>
        <span class="builder-visually-hidden">{{ words(entry, index) }}</span>
      </li>
    </ul>

    <div
      v-if="tip"
      ref="tipEl"
      class="builder-tooltip builder-tooltip--fixed"
      data-testid="counts-tooltip"
      aria-hidden="true"
      :style="{ top: `${tip.top}px`, left: `${tip.left}px` }">
      {{ tip.text }}
    </div>
  </div>
</template>

<script setup>
  import { computed, ref } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import { diagramCounts } from '@/builder/outline.js';
  import { rowTarget } from '@/builder/roving.js';
  import { useBuilderStore } from '@/builder/store.js';

  const store = useBuilderStore();

  // The Add nodes palette's icons for the node kinds, the Connect button's
  // for connections.
  const ICONS = {
    devices: 'server',
    switches: 'switch',
    networks: 'network',
    links: 'link',
    groups: 'object-group',
    notes: 'sticky-note',
  };

  const counts = computed(() => diagramCounts(store.doc));

  function words(entry, index) {
    return ` ${entry.noun}${index < counts.value.length - 1 ? ', ' : ''}`;
  }

  const { tip, tipEl, showTip, scheduleHide, hideTip } = useFixedTooltip({
    side: 'below',
  });

  // The count that holds the list's Tab stop: the last one focused.
  const current = ref(0);

  function onFocus(event, index, entry) {
    current.value = index;
    showTip(event, entry.text);
  }

  function onKeydown(event) {
    if (event.altKey || event.ctrlKey || event.metaKey) {
      return;
    }

    const items = [...event.currentTarget.children];
    const next = rowTarget(
      event.key,
      items.indexOf(event.target),
      items.length,
    );
    if (next === undefined) {
      return;
    }

    event.preventDefault();
    items[next].focus();
  }

  // While a count has focus, its tooltip comes back once the pointer leaves
  // another, so it lasts as long as the focus does (WCAG 1.4.13).
  function leaveOne(event) {
    const focused =
      event.currentTarget.parentElement?.querySelector(':scope > li:focus');

    if (focused) {
      showTip({ currentTarget: focused }, counts.value[current.value].text);
    } else {
      scheduleHide();
    }
  }
</script>

<style scoped>
  .builder-counts {
    flex: 0 1 auto;
    min-width: 0;
  }

  .builder-counts__list {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem 0.8rem;
    margin: 0;
    padding: 0.25rem 0.4rem;
    list-style: none;
    border-radius: var(--bx-radius);
    font-size: 0.9rem;
    font-variant-numeric: tabular-nums;
  }

  .builder-counts__item {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    cursor: default;
  }

  .builder-counts__item .builder-icon {
    color: var(--bx-text-muted);
  }
</style>
