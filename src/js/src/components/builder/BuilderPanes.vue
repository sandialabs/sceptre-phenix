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

  In the stacked narrow layout the columns span the window, so the splitters
  and toggles are not shown.
-->
<template>
  <div
    ref="layoutEl"
    class="builder-layout"
    :class="{ 'is-resizing': Boolean(drag) }"
    :style="columnWidths">
    <p :id="HINT_ID" hidden>
      Left and Right arrows resize the column, Home and End make it narrowest
      and widest, and Enter restores its default width.
    </p>

    <div :id="PANE_IDS.start" ref="startEl" class="builder-layout__side">
      <slot name="start" />
    </div>

    <div class="builder-layout__gutter">
      <button v-bind="toggleAttributes('start')" v-on="TOGGLE_EVENTS.start">
        <builder-icon :name="toggleIcon('start')" :size="14" />
      </button>
      <div v-bind="splitterAttributes('start')" v-on="SPLITTER_EVENTS.start">
        <span class="builder-splitter__grip" />
      </div>
      <!-- The toggle's name, which reaches screen readers already. -->
      <div
        v-if="TIPS.start.tip.value"
        :ref="(element) => (TIPS.start.tipEl.value = element)"
        class="builder-tooltip builder-tooltip--fixed"
        data-testid="pane-toggle-tooltip-start"
        aria-hidden="true"
        :style="tipPosition(TIPS.start.tip.value)">
        {{ TIPS.start.tip.value.text }}
      </div>
    </div>

    <div ref="mainEl" class="builder-layout__main">
      <slot />
    </div>

    <div class="builder-layout__gutter">
      <button v-bind="toggleAttributes('end')" v-on="TOGGLE_EVENTS.end">
        <builder-icon :name="toggleIcon('end')" :size="14" />
      </button>
      <div v-bind="splitterAttributes('end')" v-on="SPLITTER_EVENTS.end">
        <span class="builder-splitter__grip" />
      </div>
      <div
        v-if="TIPS.end.tip.value"
        :ref="(element) => (TIPS.end.tipEl.value = element)"
        class="builder-tooltip builder-tooltip--fixed"
        data-testid="pane-toggle-tooltip-end"
        aria-hidden="true"
        :style="tipPosition(TIPS.end.tip.value)">
        {{ TIPS.end.tip.value.text }}
      </div>
    </div>

    <div :id="PANE_IDS.end" ref="endEl" class="builder-layout__side">
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
    reactive,
    ref,
  } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import {
    PANE_STEP_REM,
    clampPane,
    loadPanes,
    paneKeyWidth,
    paneLimits,
    savePanes,
  } from '@/builder/panes.js';

  const HINT_ID = 'builder-splitter-hint';
  const PANE_IDS = { start: 'builder-pane-start', end: 'builder-pane-end' };
  // The side columns' headings, which the splitters and toggles are named
  // after.
  const PANE_NAMES = { start: 'Add nodes and Outline', end: 'Inspector' };

  const layoutEl = ref(null);
  const startEl = ref(null);
  const endEl = ref(null);
  const mainEl = ref(null);

  const saved = loadPanes();
  // The widths this viewer chose; a side without one has the default width.
  const chosen = reactive(saved.widths);
  // The sides widened with their toggle, each with the width it goes back
  // to: null for the default width.
  const widened = reactive(saved.widened);
  // The layout as drawn: CSS keeps a chosen width within what the window
  // allows, so the splitters report what is shown.
  const measured = reactive({ layout: 0, start: 0, end: 0, rem: 16 });
  const drag = ref(null);

  // Each toggle's tooltip lies over the canvas.
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

  function limitsOf(side) {
    return paneLimits(side, {
      layout: measured.layout,
      other: measured[other(side)],
      rem: measured.rem,
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

  // Which way the splitter moves when the toggle is pressed.
  function toggleIcon(side) {
    return (side === 'start') !== isWidened(side)
      ? 'chevrons-right'
      : 'chevrons-left';
  }

  function tipPosition(tip) {
    return { top: `${tip.top}px`, left: `${tip.left}px` };
  }

  function save() {
    savePanes({ widths: chosen, widened });
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

  // Reset view: both columns take their default widths again, which also
  // forgets the widths stored for them. A drag under way ends there.
  function resetWidths() {
    drag.value = null;
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

  // The toggle is named by an icon only, so its name is its tooltip too
  // (WCAG 1.4.13). Pressing it moves it with its splitter, so the tooltip
  // goes rather than stay behind where the toggle was.
  function toggleEvents(side) {
    const tips = TIPS[side];
    const showTip = (event) => tips.showTip(event, `Widen ${PANE_NAMES[side]}`);

    return {
      click: () => {
        tips.hideTip();
        toggleWidened(side);
      },
      mouseenter: showTip,
      mouseleave: tips.scheduleHide,
      focus: showTip,
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
      !active ||
      active === document.body ||
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

  const SPLITTER_EVENTS = {
    start: splitterEvents('start'),
    end: splitterEvents('end'),
  };

  const TOGGLE_EVENTS = {
    start: toggleEvents('start'),
    end: toggleEvents('end'),
  };

  function measure() {
    const width = (element) =>
      Math.round(element?.getBoundingClientRect().width || 0);

    measured.layout = layoutEl.value?.clientWidth || 0;
    measured.start = width(startEl.value);
    measured.end = width(endEl.value);
    measured.rem =
      parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;

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

  defineExpose({ resetWidths });
</script>
