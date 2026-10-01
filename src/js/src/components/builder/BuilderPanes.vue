<!--
  The editor's three columns: Add nodes and the Outline, the canvas and the
  Inspector, with a splitter between each side column and the canvas (WAI-ARIA
  APG window splitter). A splitter is dragged, or focused and moved with the
  arrow keys; Home and End make its column narrowest and widest, and Enter or
  a double-click restores the default width. The canvas always keeps a usable
  width, and each viewer's widths are remembered in this browser (see
  builder/panes.js).

  Dragging is not the only way with a pointer (WCAG 2.5.7): a Widen toggle
  above each splitter makes its column as wide as it can be, and restores the
  width it had when pressed again. Each splitter takes hold of the pointer
  24px wide, over the edge of the canvas beside it (WCAG 2.5.8).

  A Hide toggle under the Widen toggle folds its column into a narrow strip,
  which gives the canvas the room; the toggle stays in the strip, named Show,
  to bring the column back. The column stays mounted while it is hidden, so
  the Inspector keeps what it holds. Hidden columns are remembered with the
  widths. A node or connection pressed on the canvas shows the Inspector
  again (see BuilderCanvas.vue), and so do the commands that go to a column.

  In the stacked narrow layout the columns span the window, so the splitters
  and toggles are not shown, and every column shows, hidden or not. When a
  wider window hides such a column again, focus in it moves to its Show
  toggle, as when it is hidden.
-->
<template>
  <div
    ref="layoutEl"
    class="builder-layout"
    :class="{
      'is-resizing': Boolean(drag),
      'is-start-hidden': isHidden('start'),
      'is-end-hidden': isHidden('end'),
    }"
    :style="columnWidths"
    @focusin="onFocusin">
    <p :id="HINT_ID" hidden>
      Left and Right arrows resize the column, Home and End make it narrowest
      and widest, and Enter restores its default width.
    </p>

    <div
      :id="PANE_IDS.start"
      ref="startEl"
      class="builder-layout__side builder-layout__side--start"
      :class="{ 'builder-layout__side--hidden': isHidden('start') }">
      <slot name="start" />
    </div>

    <div
      ref="startGutterEl"
      class="builder-layout__gutter builder-layout__gutter--start">
      <button
        v-if="!isHidden('start')"
        v-bind="toggleAttributes('start')"
        v-on="TOGGLE_EVENTS.start">
        <builder-icon :name="toggleIcon('start')" :size="14" />
      </button>
      <button
        :ref="(element) => (hideEls.start = element)"
        v-bind="hideAttributes('start')"
        v-on="HIDE_EVENTS.start">
        <builder-icon :name="hideIcon('start')" :size="14" />
        <span v-if="isHidden('start')" class="builder-layout__strip-label">
          {{ PANE_NAMES.start }}
        </span>
      </button>
      <div
        v-if="!isHidden('start')"
        v-bind="splitterAttributes('start')"
        v-on="SPLITTER_EVENTS.start">
        <span class="builder-splitter__grip" />
      </div>
      <!-- The toggles' names, which reach screen readers already. -->
      <builder-fixed-tooltip
        :tooltip="TIPS.start"
        testid="pane-toggle-tooltip-start" />
    </div>

    <div ref="mainEl" class="builder-layout__main">
      <slot />
    </div>

    <div class="builder-layout__gutter builder-layout__gutter--end">
      <button
        v-if="!isHidden('end')"
        v-bind="toggleAttributes('end')"
        v-on="TOGGLE_EVENTS.end">
        <builder-icon :name="toggleIcon('end')" :size="14" />
      </button>
      <button
        :ref="(element) => (hideEls.end = element)"
        v-bind="hideAttributes('end')"
        v-on="HIDE_EVENTS.end">
        <builder-icon :name="hideIcon('end')" :size="14" />
        <span v-if="isHidden('end')" class="builder-layout__strip-label">
          {{ PANE_NAMES.end }}
        </span>
      </button>
      <div
        v-if="!isHidden('end')"
        v-bind="splitterAttributes('end')"
        v-on="SPLITTER_EVENTS.end">
        <span class="builder-splitter__grip" />
      </div>
      <builder-fixed-tooltip
        :tooltip="TIPS.end"
        testid="pane-toggle-tooltip-end" />
    </div>

    <div
      :id="PANE_IDS.end"
      ref="endEl"
      class="builder-layout__side builder-layout__side--end"
      :class="{ 'builder-layout__side--hidden': isHidden('end') }">
      <slot name="end" />
    </div>
  </div>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    provide,
    reactive,
    ref,
  } from 'vue';

  import BuilderFixedTooltip from './BuilderFixedTooltip.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import { focusLost } from '@/builder/commands.js';
  import {
    PANE_STEP_REM,
    clampPane,
    loadPanes,
    paneKeyWidth,
    paneLimits,
    savePanes,
  } from '@/builder/panes.js';
  import { useBuilderStore } from '@/builder/store.js';

  const HINT_ID = 'builder-splitter-hint';
  const PANE_IDS = { start: 'builder-pane-start', end: 'builder-pane-end' };
  // The side columns' headings, which the splitters and toggles are named
  // after.
  const PANE_NAMES = { start: 'Add nodes and Outline', end: 'Inspector' };

  const store = useBuilderStore();

  const layoutEl = ref(null);
  const startEl = ref(null);
  const endEl = ref(null);
  const mainEl = ref(null);
  const startGutterEl = ref(null);
  // The Hide toggles, which keep focus while their column is hidden.
  const hideEls = { start: null, end: null };

  const saved = loadPanes();
  // The widths this viewer chose; a side without one has the default width.
  const chosen = reactive(saved.widths);
  // The sides widened with their toggle, each with the width it goes back
  // to: null for the default width.
  const widened = reactive(saved.widened);
  // The sides hidden with their toggle.
  const hidden = reactive(new Set(saved.hidden));
  // The width each side had when it was hidden, which it takes again when
  // shown.
  const widthBeforeHiding = {};
  // The layout as drawn: CSS keeps a chosen width within what the window
  // allows, so the splitters report what is shown. Stacked: the narrow
  // layout, which has no splitters or toggles.
  const measured = reactive({
    layout: 0,
    start: 0,
    end: 0,
    rem: 16,
    stacked: false,
  });
  const drag = ref(null);

  // Each gutter's tooltip, for its toggles, lies over the canvas.
  const TIPS = {
    start: useFixedTooltip({ side: 'end' }),
    end: useFixedTooltip({ side: 'start' }),
  };

  const columnWidths = computed(() =>
    Object.fromEntries(
      Object.entries(chosen).map(([side, width]) => [
        `--builder-pane-${side}`,
        `${width}px`,
      ]),
    ),
  );

  function other(side) {
    return side === 'start' ? 'end' : 'start';
  }

  function isWidened(side) {
    return Object.hasOwn(widened, side);
  }

  function isHidden(side) {
    return hidden.has(side);
  }

  function limitsOf(side) {
    return paneLimits(side, {
      layout: measured.layout,
      other: measured[other(side)],
      rem: measured.rem,
      otherHidden: isHidden(other(side)),
    });
  }

  function splitterAttributes(side) {
    const limits = limitsOf(side);
    const width = clampPane(measured[side], limits);

    return {
      class: `builder-splitter builder-splitter--${side}`,
      role: 'separator',
      tabindex: 0,
      'aria-orientation': 'vertical',
      'aria-label': `Resize ${PANE_NAMES[side]}`,
      'aria-controls': PANE_IDS[side],
      'aria-valuenow': width,
      'aria-valuemin': limits.min,
      'aria-valuemax': limits.max,
      'aria-valuetext': `${width} pixels wide`,
      'aria-describedby': HINT_ID,
      'data-testid': `splitter-${side}`,
    };
  }

  function toggleAttributes(side) {
    return {
      type: 'button',
      class: 'builder-button builder-layout__toggle',
      'aria-label': `Widen ${PANE_NAMES[side]}`,
      'aria-pressed': String(isWidened(side)),
      'aria-controls': PANE_IDS[side],
      'data-testid': `pane-toggle-${side}`,
    };
  }

  // 'Hide Inspector', or 'Show Inspector' while it is hidden.
  function hideName(side) {
    return `${isHidden(side) ? 'Show' : 'Hide'} ${PANE_NAMES[side]}`;
  }

  function hideAttributes(side) {
    return {
      type: 'button',
      class: [
        'builder-button builder-layout__toggle',
        { 'builder-layout__strip': isHidden(side) },
      ],
      'aria-label': hideName(side),
      'aria-expanded': String(!isHidden(side)),
      'aria-controls': PANE_IDS[side],
      'data-testid': `pane-hide-${side}`,
    };
  }

  // Which way the splitter moves when the toggle is pressed.
  function toggleIcon(side) {
    return (side === 'start') !== isWidened(side)
      ? 'chevrons-right'
      : 'chevrons-left';
  }

  // A panel at the column's side, and which way it goes when pressed.
  function hideIcon(side) {
    const panel = side === 'start' ? 'panel-left' : 'panel-right';

    return `${panel}-${isHidden(side) ? 'open' : 'close'}`;
  }

  function save() {
    savePanes({ widths: chosen, widened, hidden });
  }

  // A width set any other way than the toggle ends the column's widening.
  function setWidth(side, width) {
    const next = clampPane(width, limitsOf(side));

    if (next !== chosen[side]) {
      delete widened[side];
    }

    chosen[side] = next;
  }

  function reset(side) {
    delete chosen[side];
    delete widened[side];
    save();
  }

  // Reset view: both columns show, at their default widths again, which
  // also forgets the widths stored for them. A drag under way ends there.
  function resetWidths() {
    drag.value = null;
    hidden.clear();
    reset('start');
    reset('end');
  }

  function restore(side) {
    const before = widened[side];

    delete widened[side];

    if (before === null) {
      delete chosen[side];
    } else {
      chosen[side] = before;
    }
  }

  // As End on the splitter, but pressed again it puts the width back. Only
  // one column is widened at a time: the other goes back first, so there is
  // room to widen into.
  async function toggleWidened(side) {
    if (isWidened(side)) {
      restore(side);
      save();
      return;
    }

    if (isWidened(other(side))) {
      restore(other(side));
      await nextTick();
      measure();
    }

    const before = chosen[side] ?? null;

    setWidth(side, limitsOf(side).max);
    widened[side] = before;
    save();
  }

  /**
   * Hides a side column. Focus in it, or on its splitter or Widen toggle,
   * moves to the toggle that shows it again, rather than to the page.
   *
   * @param {'start'|'end'} side
   */
  function hide(side) {
    if (isHidden(side)) {
      return;
    }

    const active = document.activeElement;
    const refocus =
      active &&
      active !== hideEls[side] &&
      Boolean(
        columnOf(side)?.contains(active) ||
          hideEls[side]?.parentElement?.contains(active),
      );

    widthBeforeHiding[side] = measured[side];
    hidden.add(side);
    save();
    store.announce(`${PANE_NAMES[side]} hidden.`);

    if (refocus) {
      nextTick(() => hideEls[side]?.focus());
    }
  }

  /**
   * Shows a hidden side column. A column widened while it was hidden makes
   * room for it again: it becomes as wide as it can be beside the column
   * at the width it had. The stacked layout shows every column already, so
   * there the column is only no longer hidden once the window is wider.
   *
   * @param {'start'|'end'} side
   * @returns {boolean} whether the column came into view
   */
  function show(side) {
    if (!isHidden(side)) {
      return false;
    }

    const beside = other(side);

    hidden.delete(side);

    if (isWidened(beside)) {
      const before = widened[beside];
      const { max } = paneLimits(beside, {
        layout: measured.layout,
        other: widthBeforeHiding[side] || chosen[side] || 0,
        rem: measured.rem,
      });

      chosen[beside] = Math.min(chosen[beside] ?? max, max);
      widened[beside] = before;
    }

    save();

    if (measured.stacked) {
      return false;
    }

    store.announce(`${PANE_NAMES[side]} shown.`);

    return true;
  }

  function toggleHidden(side) {
    if (isHidden(side)) {
      show(side);
    } else {
      hide(side);
    }
  }

  function onKeydown(side, event) {
    if (event.key === 'Escape' && drag.value) {
      cancelDrag();
      return;
    }

    if (event.key === 'Enter') {
      event.preventDefault();
      reset(side);
      return;
    }

    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) {
      return;
    }

    // From the width last asked for: a key held down repeats faster than
    // the layout is measured again.
    const limits = limitsOf(side);
    const width = paneKeyWidth(
      side,
      event.key,
      clampPane(chosen[side] ?? measured[side], limits),
      limits,
      PANE_STEP_REM * measured.rem,
    );

    if (width === undefined) {
      return;
    }

    event.preventDefault();
    setWidth(side, width);
    save();
  }

  // A drag keeps the pointer, so it goes on over the canvas and the panels;
  // it is stored when it ends, and Escape puts the width back.
  function onPointerDown(side, event) {
    if (event.button !== 0 || drag.value) {
      return;
    }

    event.preventDefault();
    event.currentTarget.focus({ preventScroll: true });
    event.currentTarget.setPointerCapture?.(event.pointerId);
    drag.value = {
      side,
      pointerId: event.pointerId,
      x: event.clientX,
      width: measured[side],
      before: chosen[side],
      widened: isWidened(side) ? { before: widened[side] } : null,
      moved: false,
    };
  }

  function onPointerMove(event) {
    const current = drag.value;

    if (!current || event.pointerId !== current.pointerId) {
      return;
    }

    const dx = event.clientX - current.x;

    if (!current.moved && Math.abs(dx) < 1) {
      return;
    }

    current.moved = true;
    setWidth(
      current.side,
      current.side === 'start' ? current.width + dx : current.width - dx,
    );
  }

  function endDrag(event) {
    const current = drag.value;

    if (!current || (event && event.pointerId !== current.pointerId)) {
      return;
    }

    drag.value = null;

    if (current.moved) {
      save();
    }
  }

  function cancelDrag() {
    const current = drag.value;

    if (!current) {
      return;
    }

    drag.value = null;

    if (current.before === undefined) {
      delete chosen[current.side];
    } else {
      chosen[current.side] = current.before;
    }

    if (current.widened) {
      widened[current.side] = current.widened.before;
    }
  }

  function splitterEvents(side) {
    return {
      keydown: (event) => onKeydown(side, event),
      pointerdown: (event) => onPointerDown(side, event),
      pointermove: onPointerMove,
      pointerup: endDrag,
      lostpointercapture: endDrag,
      pointercancel: cancelDrag,
      dblclick: () => reset(side),
      blur: onBlur,
    };
  }

  // A toggle is named by an icon only, so its name is its tooltip too
  // (WCAG 1.4.13). Pressing it moves it (with its splitter, or into the
  // strip and out), so the tooltip goes rather than stay behind where the
  // toggle was.
  function tipEvents(side, name, press) {
    const tips = TIPS[side];

    return {
      ...tips.tipEvents(name),
      click: () => {
        tips.hideTip();
        press();
      },
      blur: (event) => {
        tips.hideTip();
        onBlur(event);
      },
    };
  }

  // Enlarged text or a narrower window can stack the columns, which hides
  // the splitters and toggles. Focus on one then moves on to the canvas
  // rather than being lost to the page: Chromium drops it (and says so with
  // a blur), and Firefox leaves it on the hidden control until the layout
  // is measured.
  function focusCanvas() {
    const active = document.activeElement;

    if (
      focusLost() ||
      (active.closest('.builder-layout__gutter') && !active.offsetParent)
    ) {
      mainEl.value?.querySelector('[tabindex]')?.focus({ preventScroll: true });
    }
  }

  function onBlur(event) {
    if (!event.relatedTarget && !event.currentTarget.offsetParent) {
      setTimeout(focusCanvas);
    }
  }

  function columnOf(side) {
    return (side === 'start' ? startEl : endEl).value;
  }

  // The side column that last took focus, or null once focus moved
  // elsewhere in the layout.
  let focusedSide = null;

  function onFocusin(event) {
    focusedSide =
      ['start', 'end'].find((side) => columnOf(side)?.contains(event.target)) ??
      null;
  }

  // A wider window ends the stacked layout, which hides again a column the
  // user hid. Focus that was in it has fallen to the page (or, in Firefox,
  // stays on a control no longer shown), so it goes to the column's Show
  // toggle, as when the column is hidden, and the change is announced.
  function refocusHidden() {
    const side = focusedSide;

    if (
      !side ||
      !isHidden(side) ||
      (!focusLost() && !columnOf(side)?.contains(document.activeElement))
    ) {
      return;
    }

    hideEls[side]?.focus();
    store.announce(`${PANE_NAMES[side]} hidden.`);
  }

  const SPLITTER_EVENTS = {
    start: splitterEvents('start'),
    end: splitterEvents('end'),
  };

  const TOGGLE_EVENTS = Object.fromEntries(
    ['start', 'end'].map((side) => [
      side,
      tipEvents(
        side,
        () => `Widen ${PANE_NAMES[side]}`,
        () => toggleWidened(side),
      ),
    ]),
  );

  const HIDE_EVENTS = Object.fromEntries(
    ['start', 'end'].map((side) => [
      side,
      tipEvents(
        side,
        () => hideName(side),
        () => toggleHidden(side),
      ),
    ]),
  );

  function measure() {
    const width = (element) =>
      Math.round(element?.getBoundingClientRect().width || 0);

    measured.layout = layoutEl.value?.clientWidth || 0;
    measured.start = width(startEl.value);
    measured.end = width(endEl.value);
    measured.rem =
      parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;

    const wasStacked = measured.stacked;

    measured.stacked = Boolean(
      startGutterEl.value &&
        getComputedStyle(startGutterEl.value).display === 'none',
    );

    if (wasStacked && !measured.stacked) {
      refocusHidden();
    }

    if (layoutEl.value?.contains(document.activeElement)) {
      focusCanvas();
    }
  }

  let observer = null;

  onMounted(() => {
    measure();

    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(measure);
      [layoutEl, startEl, endEl].forEach((element) =>
        observer.observe(element.value),
      );
    }
  });

  onBeforeUnmount(() => observer?.disconnect());

  // For the canvas, which shows the Inspector when a node is pressed. The
  // stacked layout shows every column, hidden or not.
  provide('builderPanes', {
    isHidden: (side) => isHidden(side) && !measured.stacked,
    show,
  });

  // For the view's commands: which columns are hidden, and whether the
  // narrow layout stacks them (and so shows them all).
  const state = computed(() => ({
    hidden: { start: isHidden('start'), end: isHidden('end') },
    stacked: measured.stacked,
  }));

  defineExpose({ resetWidths, state, show, toggleHidden });
</script>
