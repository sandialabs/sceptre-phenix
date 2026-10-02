<!--
  The diagram's counts, in the editor header: an icon and a number for each
  kind of element, each a button that selects every item of its kind on the
  canvas (the networks count selects the networks' switches; see
  kindSelection). Screen readers read each count in words ("2 devices"), and
  each count says what pressing it selects as a tooltip on hover and on
  focus (WCAG 1.4.13), which is also its description. A count of none is
  aria-disabled: it keeps focus, and pressing it says there is nothing to
  select. The list is one Tab stop: the arrow keys, Home and End move
  between the counts, as in a toolbar, and the count last focused keeps the
  Tab stop. The counts change with every edit and are not a live region:
  edits announce themselves.
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
        :data-kind="entry.key">
        <button
          type="button"
          class="builder-counts__button"
          :data-testid="`count-${entry.key}`"
          :tabindex="index === current ? 0 : -1"
          :aria-disabled="entry.count === 0 ? 'true' : undefined"
          :aria-describedby="
            entry.count > 0 ? `count-tip-${entry.key}` : undefined
          "
          @click="press(entry)"
          @focus="onFocus($event, index, entry)"
          @blur="hideTip"
          @mouseenter="showTip($event, entry.tip)"
          @mouseleave="leaveOne">
          <builder-icon :name="ICONS[entry.key]" :size="17" />
          <!-- The number is shown and the word after it is read: the
               button's name is "2 devices". -->
          <span>{{ entry.count }}</span>
          <span class="builder-visually-hidden">{{ ` ${entry.noun}` }}</span>
        </button>
        <!-- The comma is read after the button, not as part of its name, so
             the list's text is one line: "2 devices, 1 switch". -->
        <span v-if="index < counts.length - 1" class="builder-visually-hidden"
          >,
        </span>
      </li>
    </ul>

    <!-- What each count selects, as its button's description. Outside the
         list, whose text is the counts alone. -->
    <template v-for="entry in counts" :key="entry.key">
      <span v-if="entry.count > 0" :id="`count-tip-${entry.key}`" hidden>
        {{ entry.tip }}
      </span>
    </template>

    <builder-fixed-tooltip :tooltip="tooltip" testid="counts-tooltip" />
  </div>
</template>

<script setup>
  import { computed, ref } from 'vue';

  import BuilderFixedTooltip from './BuilderFixedTooltip.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import { diagramCounts } from '@/builder/outline.js';
  import { rowTarget } from '@/builder/roving.js';
  import { kindSelection } from '@/builder/selection.js';
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

  const tooltip = useFixedTooltip({ side: 'below' });
  const { showTip, scheduleHide, hideTip } = tooltip;

  // The count that holds the list's Tab stop: the last one focused.
  const current = ref(0);

  function onFocus(event, index, entry) {
    current.value = index;
    showTip(event, entry.tip);
  }

  // Selects every item of the count's kind, in place of the selection, and
  // says so. With nothing to select the selection stays, and the reason is
  // said. Focus stays on the count.
  function press(entry) {
    const { selection, message } = kindSelection(store.doc, entry.key);

    if (selection) {
      store.select(selection);
    }

    if (message) {
      store.announce(message);
    }
  }

  function buttons(list) {
    return [...list.querySelectorAll('.builder-counts__button')];
  }

  function onKeydown(event) {
    if (event.altKey || event.ctrlKey || event.metaKey) {
      return;
    }

    const items = buttons(event.currentTarget);
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
    const focused = event.currentTarget
      .closest('.builder-counts__list')
      ?.querySelector('.builder-counts__button:focus');

    if (focused) {
      showTip({ currentTarget: focused }, counts.value[current.value].tip);
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

  /* In a thin box, which sets the counts apart in the header and shows in
     forced colors too. Its padding keeps a count's focus ring inside it,
     and the box as tall as the header's buttons. */
  .builder-counts__list {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem 0.3rem;
    margin: 0;
    padding: 0.35rem 0.5rem;
    list-style: none;
    border: 1px solid var(--bx-border);
    border-radius: var(--bx-radius);
    font-size: 0.9rem;
    font-variant-numeric: tabular-nums;
  }

  .builder-counts__item {
    display: inline-flex;
    align-items: center;
  }

  /* A button that looks like the count it was: no border or background of
     its own, and at least 24px each way (WCAG 2.5.8). */
  .builder-counts__button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.3rem;
    min-width: 24px;
    min-height: 24px;
    margin: 0;
    padding: 0 0.25rem;
    border: 0;
    border-radius: var(--bx-radius);
    background: none;
    cursor: pointer;
  }

  .builder-counts__button:hover:not([aria-disabled='true']) {
    background: var(--bx-bg-alt);
  }

  /* Nothing to select: it keeps focus and its tooltip, and says so when
     pressed. */
  .builder-counts__button[aria-disabled='true'] {
    cursor: default;
  }

  .builder-counts__button .builder-icon {
    color: var(--bx-text-muted);
  }
</style>
