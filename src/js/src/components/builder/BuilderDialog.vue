<!--
  Modal shell shared by the builder dialogs.

  A native <dialog> opened with showModal(): the browser keeps it above the
  page and makes everything behind it inert, so neither focus nor a screen
  reader can reach the editor while it is open. On top of that it handles the
  rest of the modal dialog pattern once: a labelled dialog, Tab and Shift+Tab
  wrapping inside it, Escape or a click outside it to close, initial focus and
  focus return to the invoking control.
-->
<template>
  <dialog
    ref="panel"
    class="builder-dialog"
    data-testid="builder-dialog"
    aria-modal="true"
    :aria-labelledby="titleId"
    tabindex="-1"
    @cancel.prevent="$emit('close')"
    @keydown="onKeydown"
    @pointerdown="onPointerdown"
    @click="onClick">
    <div v-if="!hideHeader" class="builder-dialog__header">
      <h2 :id="titleId">{{ title }}</h2>
      <button
        type="button"
        class="builder-button"
        aria-label="Close dialog"
        @click="$emit('close')">
        <builder-icon name="close" :size="14" />
      </button>
    </div>
    <h2 v-else :id="titleId" class="builder-visually-hidden">{{ title }}</h2>

    <slot />
  </dialog>
</template>

<script setup>
  import { onBeforeUnmount, onMounted, ref } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';

  defineProps({
    title: { type: String, required: true },
    titleId: { type: String, default: 'builder-dialog-title' },
    // Leaves the title to screen readers, where it still names the dialog,
    // and the Close button to the content: for a dialog whose first row is
    // its own heading, such as the command palette's search field.
    hideHeader: { type: Boolean, default: false },
  });

  const emit = defineEmits(['close']);

  const panel = ref(null);
  let previous = null;

  const FOCUSABLE = 'a[href], button, input, select, textarea, [tabindex]';

  // The controls Tab visits, in order. A radio group is one stop: its
  // unchecked buttons are skipped when one of them is checked.
  function tabStops() {
    const dialog = panel.value;

    return [...dialog.querySelectorAll(FOCUSABLE)].filter((element) => {
      if (
        element.disabled ||
        element.tabIndex < 0 ||
        element.getClientRects().length === 0
      ) {
        return false;
      }

      if (element.type === 'radio' && element.name && !element.checked) {
        return !dialog.querySelector(
          `input[type="radio"][name="${CSS.escape(element.name)}"]:checked`,
        );
      }

      return true;
    });
  }

  function onKeydown(event) {
    if (event.key === 'Escape') {
      // Cancelling the key also stops the browser's own close request, and
      // stopping it keeps the view's global Escape handler out.
      event.preventDefault();
      event.stopPropagation();
      emit('close');

      return;
    }

    if (event.key !== 'Tab') {
      return;
    }

    // The page behind is inert, but Tab past either end would still leave
    // for the browser's own controls; wrap to the other end instead.
    const stops = tabStops();
    const active = document.activeElement;

    if (stops.length === 0) {
      event.preventDefault();
    } else if (
      event.shiftKey &&
      (active === stops[0] || active === panel.value)
    ) {
      event.preventDefault();
      stops[stops.length - 1].focus();
    } else if (!event.shiftKey && active === stops[stops.length - 1]) {
      event.preventDefault();
      stops[0].focus();
    }
  }

  // A press and a release on the backdrop close the dialog as Escape does.
  // The browser reports backdrop clicks on the <dialog> itself, as it does
  // clicks on its padding, so the pointer must be outside the dialog's box
  // both times: a text selection dragged out of the dialog also ends in a
  // click on it, and must leave it open.
  let pressedOutside = false;

  function outsideBox(event) {
    const dialog = panel.value;

    if (event.target !== dialog) {
      return false;
    }

    const box = dialog.getBoundingClientRect();

    return (
      event.clientX < box.left ||
      event.clientX > box.right ||
      event.clientY < box.top ||
      event.clientY > box.bottom
    );
  }

  function onPointerdown(event) {
    pressedOutside = outsideBox(event);
  }

  function onClick(event) {
    if (pressedOutside && outsideBox(event)) {
      emit('close');
    }

    pressedOutside = false;
  }

  onMounted(() => {
    previous = document.activeElement;
    panel.value.showModal();
    panel.value.focus();
  });

  onBeforeUnmount(() => {
    // Close first: while the dialog is modal the invoking control is inert
    // and cannot take focus back.
    panel.value?.close();

    if (previous?.isConnected && typeof previous.focus === 'function') {
      previous.focus();
    }
  });
</script>

<style scoped>
  .builder-dialog__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    margin-bottom: 0.75rem;
  }

  .builder-dialog__header h2 {
    font-size: 1.05rem;
    font-weight: 700;
    margin: 0;
  }
</style>
