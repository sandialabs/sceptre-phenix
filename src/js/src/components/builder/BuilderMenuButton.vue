<!--
  A menu button (WAI-ARIA APG menu button and menu patterns): the toolbar's
  layout and Auto-group choices.

  Enter, Space, Down Arrow or a click opens the menu on its first item, Up
  Arrow on its last. In the menu, Up and Down move and wrap, Home and End
  jump to the ends, a letter moves to the next item starting with it, and
  Enter or Space chooses. Choosing, Escape, and a click outside the menu
  close it and give focus back to the button (a click on something that
  takes focus then takes it); Tab closes it and goes on from the button.
  So focus never falls to the page.

  An item is {id, label, description?, checked?, separator?}: a menuitemradio
  when it has `checked`, a menuitem otherwise; the description is its
  accessible description rather than part of its name; `separator` draws a
  rule before it.

  The button is a plain <button> and takes the attributes and listeners
  given to this component (a name, a tooltip), so a toolbar's roving Tab stop
  takes it with its other buttons; the items are not buttons, and stay out
  of it. The slot is the button's content, before its arrow.
-->
<template>
  <div ref="rootEl" class="builder-menu">
    <button
      :id="buttonId"
      ref="buttonEl"
      type="button"
      class="builder-button builder-menu__button"
      aria-haspopup="menu"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-controls="open ? menuId : undefined"
      :aria-disabled="disabled || undefined"
      :aria-busy="busy || undefined"
      :data-testid="testid"
      v-bind="$attrs"
      @click="onClick"
      @keydown="onButtonKeydown">
      <slot />
      <builder-icon
        name="chevron-down"
        :size="12"
        class="builder-menu__arrow" />
    </button>
    <!-- mousedown on the menu keeps focus where it is: a click on its
         padding would otherwise drop focus to the page. -->
    <ul
      v-if="open"
      :id="menuId"
      ref="menuEl"
      role="menu"
      :aria-labelledby="buttonId"
      class="builder-menu__list builder-panel"
      :class="{ 'builder-menu__list--end': alignEnd }"
      :style="shift ? { left: `${-shift}px` } : undefined"
      :data-testid="testid ? `${testid}-menu` : undefined"
      @mousedown.prevent
      @keydown="onMenuKeydown">
      <template v-for="(item, index) in items" :key="item.id">
        <li
          v-if="item.separator && index > 0"
          role="separator"
          class="builder-menu__separator"></li>
        <li
          :role="item.checked === undefined ? 'menuitem' : 'menuitemradio'"
          :aria-checked="
            item.checked === undefined ? undefined : String(item.checked)
          "
          :aria-describedby="
            item.description ? `${menuId}-description-${index}` : undefined
          "
          tabindex="-1"
          class="builder-menu__item"
          :data-testid="testid ? `${testid}-item-${item.id}` : undefined"
          @click="choose(item)"
          @mousemove="focusItem($event.currentTarget)">
          <span class="builder-menu__check" aria-hidden="true">
            <builder-icon v-if="item.checked" name="check" :size="14" />
          </span>
          <span class="builder-menu__text">
            <span>{{ item.label }}</span>
            <span
              v-if="item.description"
              :id="`${menuId}-description-${index}`"
              class="builder-menu__description"
              aria-hidden="true">
              {{ item.description }}
            </span>
          </span>
        </li>
      </template>
    </ul>
  </div>
</template>

<script setup>
  import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';

  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    items: { type: Array, required: true },
    // aria-disabled: the button keeps focus, and does not open.
    disabled: { type: Boolean, default: false },
    busy: { type: Boolean, default: false },
    testid: { type: String, default: '' },
  });

  const emit = defineEmits(['select', 'toggle']);

  const id = useId();
  const buttonId = `${id}-button`;
  const menuId = `${id}-menu`;

  const rootEl = ref(null);
  const buttonEl = ref(null);
  const menuEl = ref(null);
  const open = ref(false);
  // Aligned to the button's end when it would run off the window's and that
  // keeps it on screen; otherwise moved left by `shift` px, as far as fits.
  const alignEnd = ref(false);
  const shift = ref(0);

  function menuItems() {
    return [...(menuEl.value?.querySelectorAll('[role^="menuitem"]') || [])];
  }

  function focusItem(item) {
    if (item && item !== document.activeElement) {
      item.focus();
    }
  }

  async function show(at) {
    if (props.disabled) {
      return;
    }

    open.value = true;
    alignEnd.value = false;
    shift.value = 0;
    emit('toggle', true);
    listen(true);
    await nextTick();

    const box = menuEl.value?.getBoundingClientRect();
    const button = buttonEl.value?.getBoundingClientRect();
    const overflow = box && button ? box.right - (window.innerWidth - 8) : 0;

    alignEnd.value = overflow > 0 && button.right - box.width >= 8;
    shift.value =
      overflow > 0 && !alignEnd.value
        ? Math.max(0, Math.min(overflow, box.left - 8))
        : 0;

    const items = menuItems();

    focusItem(at === 'last' ? items[items.length - 1] : items[0]);
  }

  // Focus goes back to the button before the menu goes, so it never falls
  // to the page with the item that had it.
  function hide({ focus = true } = {}) {
    if (!open.value) {
      return;
    }

    if (focus) {
      buttonEl.value?.focus({ preventScroll: true });
    }

    open.value = false;
    emit('toggle', false);
    listen(false);
  }

  function choose(item) {
    hide();
    emit('select', item);
  }

  function onClick() {
    if (open.value) {
      hide();
    } else {
      show('first');
    }
  }

  function onButtonKeydown(event) {
    if (event.altKey || event.ctrlKey || event.metaKey) {
      return;
    }

    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      show(event.key === 'ArrowUp' ? 'last' : 'first');
    }
  }

  function onMenuKeydown(event) {
    const items = menuItems();
    const index = items.indexOf(document.activeElement);
    const key = event.key;

    if (key === 'Tab') {
      // Tab goes on from the button, as though the menu were not there.
      hide();

      return;
    }

    if (event.altKey || event.ctrlKey || event.metaKey) {
      return;
    }

    const moves = {
      ArrowDown: (index + 1) % items.length,
      ArrowUp: (index - 1 + items.length) % items.length,
      Home: 0,
      End: items.length - 1,
    };

    if (key in moves) {
      focusItem(items[moves[key]]);
    } else if (key === 'Escape') {
      hide();
    } else if (key === 'Enter' || key === ' ') {
      // The items render in order, separators aside.
      if (props.items[index]) {
        choose(props.items[index]);
      }
    } else if (key.length === 1 && key !== ' ') {
      // The next item whose label starts with the letter, wrapping.
      const letter = key.toLowerCase();
      const order = [...items.slice(index + 1), ...items.slice(0, index + 1)];

      focusItem(
        order.find((item) =>
          item.textContent.trim().toLowerCase().startsWith(letter),
        ),
      );
    } else {
      return;
    }

    // Handled here: the Builder's own keys leave it alone (see
    // dispatchKeydown).
    event.preventDefault();
    event.stopPropagation();
  }

  // A press outside closes the menu. Focus goes to the button first; what
  // was pressed takes it from there if it takes focus.
  function onPointerdown(event) {
    if (!rootEl.value?.contains(event.target)) {
      hide();
    }
  }

  // Focus that leaves for somewhere else on the page closes the menu.
  function onFocusin(event) {
    if (!rootEl.value?.contains(event.target)) {
      hide({ focus: false });
    }
  }

  function listen(on) {
    const method = on ? 'addEventListener' : 'removeEventListener';

    window[method]('pointerdown', onPointerdown, true);
    window[method]('focusin', onFocusin, true);
  }

  // An item that goes while it has focus (Restore previous layout, once the
  // diagram changes) hands focus to the item now in its place. Read before
  // the menu re-renders.
  watch(
    () => props.items,
    () => {
      const index = menuItems().indexOf(document.activeElement);

      if (!open.value || index < 0) {
        return;
      }

      nextTick(() => {
        const active = document.activeElement;

        if (!active || active === document.body || !active.isConnected) {
          const items = menuItems();

          focusItem(items[Math.min(index, items.length - 1)] || buttonEl.value);
        }
      });
    },
    { flush: 'pre' },
  );

  // A button that becomes unavailable while its menu is open closes it.
  watch(
    () => props.disabled,
    (disabled) => {
      if (disabled && open.value) {
        hide();
      }
    },
  );

  onBeforeUnmount(() => listen(false));
</script>

<style scoped>
  .builder-menu {
    position: relative;
    display: inline-flex;
  }

  .builder-menu__arrow {
    flex: none;
    margin-inline-start: -0.1rem;
  }

  .builder-menu__list {
    position: absolute;
    top: calc(100% + 4px);
    left: 0;
    z-index: 45;
    width: max-content;
    min-width: 100%;
    max-width: min(22rem, calc(100vw - 2rem));
    margin: 0;
    padding: 0.25rem;
    list-style: none;
  }

  .builder-menu__list--end {
    right: 0;
    left: auto;
  }

  .builder-menu__item {
    display: flex;
    align-items: flex-start;
    gap: 0.45rem;
    min-height: 24px;
    padding: 0.35rem 0.6rem 0.35rem 0.4rem;
    border: 1px solid transparent;
    border-radius: var(--bx-radius);
    color: var(--bx-text);
    font-size: 0.9rem;
    cursor: pointer;
  }

  .builder-menu__item:focus {
    border-color: var(--bx-accent);
    background: var(--bx-selected-bg);
  }

  .builder-menu__item:focus-visible {
    outline-offset: -3px;
  }

  .builder-menu__check {
    display: inline-flex;
    flex: none;
    width: 14px;
    padding-top: 0.15rem;
  }

  .builder-menu__text {
    display: flex;
    flex-direction: column;
    gap: 0.1rem;
    min-width: 0;
  }

  .builder-menu__description {
    color: var(--bx-text-muted);
    font-size: 0.8rem;
    overflow-wrap: anywhere;
  }

  .builder-menu__separator {
    margin: 0.25rem 0.2rem;
    border-top: 1px solid var(--bx-border);
  }

  /* The item in focus, in the system's highlight: backgrounds are dropped,
     and transparent borders drawn, so the others' borders take the
     canvas's color. */
  @media (forced-colors: active) {
    .builder-menu__item {
      border-color: Canvas;
    }

    .builder-menu__item:focus {
      forced-color-adjust: none;
      border-color: Highlight;
      background: Highlight;
      color: HighlightText;
    }

    .builder-menu__item:focus .builder-menu__description {
      color: HighlightText;
    }
  }
</style>
