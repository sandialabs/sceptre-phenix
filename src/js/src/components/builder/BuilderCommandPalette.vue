<!--
  Command palette.

  Every command of the open view (builder/commands.js), found by name. It is
  an editable combobox with list autocomplete (WAI-ARIA APG) in a modal
  dialog: focus stays in the search field, which names the highlighted
  option with aria-activedescendant. The options are grouped (labelled
  role="group"), their matched letters are marked, and a command that
  cannot run stays listed and reachable, aria-disabled, with the reason.
  builder/commandSearch.js decides what is listed.

  Up and Down Arrow move the highlight and wrap; Page Up and Page Down move
  to the previous or next group; Home and End stay the field's. Enter runs
  the highlighted option. A command that asks for a choice (Add device,
  Connect) continues here: a chip names it, the field searches the choices,
  and Backspace in the empty field goes back a step. On a node, Shift+Enter
  adds it to the selection, or takes it out, and the palette stays open.
  @ searches nodes, # networks, ? lists these prefixes, and > is ignored.

  The palette closes before a command runs, so focus is back where it was
  when the command moves it (Go to node) or opens a dialog (Export…), and
  the command's own announcement is spoken once no modal hides the live
  region. While open, it speaks through a status line of its own: why a
  command cannot run, what Shift+Enter changed, and the result count once
  typing pauses. Closed after Shift+Enter, it says what is selected, once.
-->
<template>
  <builder-dialog
    title="Command palette"
    title-id="command-palette-title"
    hide-header
    class="builder-command-palette"
    data-testid="commands-dialog"
    @keydown="onDialogKeydown"
    @close="close">
    <div class="builder-command-palette__search">
      <builder-icon name="search" :size="16" />
      <span
        v-if="step"
        class="builder-command-palette__chip"
        data-testid="command-palette-step"
        aria-hidden="true">
        {{ stepPath }} ›
      </span>
      <label for="command-palette-input" class="builder-visually-hidden">
        {{ fieldLabel }}
      </label>
      <input
        id="command-palette-input"
        ref="field"
        :value="query"
        type="text"
        role="combobox"
        aria-autocomplete="list"
        :aria-expanded="String(options.length > 0)"
        aria-controls="command-palette-list"
        :aria-activedescendant="activeItem ? optionId(activeItem) : undefined"
        aria-describedby="command-palette-hint"
        autocomplete="off"
        spellcheck="false"
        :placeholder="placeholder"
        data-testid="command-palette-input"
        @input="onInput"
        @keydown="onKeydown" />
      <button
        type="button"
        class="builder-button builder-command-palette__close"
        aria-label="Close dialog"
        @click="close">
        <builder-icon name="close" :size="14" />
      </button>
    </div>

    <div
      v-if="results.empty"
      class="builder-command-palette__empty"
      data-testid="command-palette-empty">
      <p>
        <strong>{{ results.empty.title }}</strong>
      </p>
      <p v-if="results.empty.tip" class="builder-command-palette__detail">
        {{ results.empty.tip }}
      </p>
    </div>

    <!-- The list scrolls, as the combobox's popup, while focus stays in the
         field: a press on an option does not take it. -->
    <div
      id="command-palette-list"
      ref="scroller"
      role="listbox"
      class="builder-command-palette__list"
      :aria-label="listLabel"
      :hidden="!options.length"
      data-testid="command-palette-list"
      @mousedown.prevent
      @mousemove="onPointerMove">
      <div
        v-for="(group, groupIndex) in results.groups"
        :key="group.id"
        role="group"
        :aria-labelledby="`command-palette-group-${groupIndex}`">
        <div
          :id="`command-palette-group-${groupIndex}`"
          role="presentation"
          class="builder-command-palette__group">
          {{ group.label }}
        </div>
        <div
          v-for="item in group.items"
          :id="optionId(item)"
          :key="item.key"
          role="option"
          class="builder-command-palette__option"
          :aria-selected="String(item === activeItem)"
          :aria-disabled="item.disabled ? 'true' : undefined"
          :aria-labelledby="`${optionId(item)}-title`"
          :aria-describedby="describedBy(item)"
          :data-key="item.key"
          @click="choose(item, { additive: $event.shiftKey })">
          <builder-icon :name="item.icon" :size="16" />
          <span class="builder-command-palette__text">
            <span
              :id="`${optionId(item)}-title`"
              class="builder-command-palette__title">
              <template
                v-for="(part, index) in highlightParts(item.title, item.ranges)"
                :key="index">
                <mark v-if="part.match">{{ part.text }}</mark>
                <template v-else>{{ part.text }}</template>
              </template>
            </span>
            <span
              v-if="item.disabled || item.detail || item.field"
              :id="`${optionId(item)}-detail`"
              class="builder-command-palette__detail">
              <template v-if="item.disabled">
                <strong class="builder-command-palette__off">
                  Unavailable:
                </strong>
                {{ item.disabled }}
              </template>
              <template v-else>
                <template
                  v-for="(part, index) in detailParts(item)"
                  :key="index">
                  <mark v-if="part.match">{{ part.text }}</mark>
                  <template v-else>{{ part.text }}</template>
                </template>
              </template>
            </span>
          </span>
          <template v-if="item.keys.length">
            <builder-keycaps :spec="item.keys[0]" />
            <span :id="`${optionId(item)}-keys`" hidden>
              {{ spokenKey(item.keys[0]) }}
            </span>
          </template>
          <span
            v-if="item.next || item.hint"
            class="builder-command-palette__next"
            aria-hidden="true">
            {{ item.hint || '›' }}
          </span>
        </div>
      </div>
    </div>

    <p
      v-if="results.more"
      class="builder-command-palette__more builder-command-palette__detail">
      Showing {{ options.length }} of {{ countOf(results.total) }}. Type more to
      narrow them.
    </p>

    <!-- Rendered, empty, from the start, so screen readers know it before
         its first message; keyed, so a repeated message is spoken again. -->
    <p
      class="builder-command-palette__message"
      role="status"
      data-testid="command-palette-message">
      <span :key="message.key">{{ message.text }}</span>
    </p>

    <div class="builder-command-palette__foot">
      <span
        v-for="hint in footHints"
        :key="hint.text"
        class="builder-command-palette__hint"
        aria-hidden="true">
        <builder-keycaps v-for="key in hint.keys" :key="key" :spec="key" />
        {{ hint.text }}
      </span>
      <span
        class="builder-command-palette__count"
        role="status"
        data-testid="command-palette-count">
        {{ countText }}
      </span>
    </div>

    <p id="command-palette-hint" hidden>{{ fieldHint }}</p>
  </builder-dialog>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    onUnmounted,
    ref,
    watch,
  } from 'vue';

  import BuilderDialog from './BuilderDialog.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderKeycaps from './BuilderKeycaps.vue';

  import { count } from '@/builder/announce.js';
  import {
    commandKeys,
    commandTitle,
    getCommand,
    runCommand,
    viewOf,
  } from '@/builder/commands.js';
  import {
    createChoiceCache,
    detailParts,
    paletteResults,
    selectionSummary,
    stepCount,
  } from '@/builder/commandSearch.js';
  import { highlightParts } from '@/builder/fuzzy.js';
  import { matchesKey, spokenKey, typesCharacter } from '@/builder/keymap.js';
  import { readRecent, rememberCommand } from '@/builder/recent.js';
  import { pressSelection } from '@/builder/selection.js';

  const props = defineProps({
    // The command context (createCommandContext), shared with the view.
    context: { type: Object, required: true },
    // What to show first: a query ('@' for Go to node), or the choices of
    // one command, by id (commandView.openPalette).
    request: { type: Object, default: () => ({ query: '', command: '' }) },
  });

  const emit = defineEmits(['close']);

  const field = ref(null);
  const scroller = ref(null);
  const query = ref('');
  // A command whose choices are being made, and those made so far.
  const step = ref(null);
  // The queries typed before each step, for Backspace to put back.
  const trail = [];
  const recent = ref(readRecent());
  // The highlighted option, by key: it stays on the same option while the
  // list changes under it, and goes back to the first when nothing matches.
  const activeKey = ref('');
  const message = ref({ text: '', key: 0 });
  const countText = ref('');

  const editing = computed(() => viewOf(props.context) === 'editor');

  // The nodes are listed once per document, not on every key.
  const cache = createChoiceCache();

  const results = computed(() =>
    paletteResults(props.context, {
      query: query.value,
      command: step.value?.command,
      picked: step.value?.picked,
      recent: recent.value,
      cache,
    }),
  );

  const options = computed(() =>
    results.value.groups.flatMap((group) => group.items),
  );

  const activeItem = computed(
    () =>
      options.value.find((item) => item.key === activeKey.value) ||
      options.value[0] ||
      null,
  );

  // Option ids are handed out once per key, so aria-activedescendant names
  // an option, not a position that another option may take.
  const ids = new Map();

  function optionId(item) {
    if (!ids.has(item.key)) {
      ids.set(item.key, `command-palette-option-${ids.size}`);
    }

    return ids.get(item.key);
  }

  function describedBy(item) {
    const id = optionId(item);

    return (
      [
        (item.disabled || item.detail || item.field) && `${id}-detail`,
        item.keys.length && `${id}-keys`,
      ]
        .filter(Boolean)
        .join(' ') || undefined
    );
  }

  // --- what the field and the lines around it say -----------------------------

  const stepPath = computed(() =>
    step.value
      ? [
          commandTitle(step.value.command, props.context),
          ...step.value.picked.map((choice) => choice.title),
        ].join(' › ')
      : '',
  );

  const stepName = computed(() => {
    const { command, picked } = step.value || {};

    return command?.steps?.[picked.length] || 'Choice';
  });

  const fieldLabel = computed(() =>
    step.value
      ? `${stepPath.value}: choose a ${stepName.value.toLowerCase()}`
      : 'Search commands',
  );

  const placeholder = computed(() => {
    if (step.value) {
      return stepName.value;
    }

    return editing.value
      ? 'Type a command, @ for nodes, # for networks'
      : 'Type a command or a draft name';
  });

  const listLabel = computed(() => {
    if (step.value) {
      return `${stepName.value} choices`;
    }

    return { '@': 'Nodes', '#': 'Networks' }[results.value.mode] || 'Commands';
  });

  const fieldHint = computed(() =>
    [
      'Up and Down arrows move through the results, Page Up and Page Down between groups, and Enter runs the highlighted one.',
      step.value && 'Backspace in the empty field goes back a step.',
      results.value.mode === '@' &&
        'Shift+Enter adds a node to the selection, or takes it out.',
      !step.value &&
        (editing.value
          ? 'Type @ to find a node, # a network, or ? for help.'
          : 'Type ? for help.'),
    ]
      .filter(Boolean)
      .join(' '),
  );

  const footHints = computed(() => {
    const hints = [
      { keys: ['ArrowUp', 'ArrowDown'], text: 'move' },
      { keys: ['Enter'], text: step.value ? 'choose' : 'run' },
    ];

    if (results.value.mode === '@') {
      hints.push({ keys: ['Shift+Enter'], text: 'add to selection' });
    }

    if (step.value) {
      hints.push({ keys: ['Backspace'], text: 'back' });
    }

    hints.push({ keys: ['Escape'], text: 'close' });

    return hints;
  });

  function countOf(total) {
    return count(total, results.value.noun);
  }

  // The count waits for typing to pause, so it is spoken once, not for
  // every letter.
  const COUNT_DELAY_MS = 300;
  let countTimer = null;

  watch(
    results,
    (now) => {
      clearTimeout(countTimer);
      countTimer = setTimeout(() => {
        countText.value = now.total ? countOf(now.total) : 'No results';
      }, COUNT_DELAY_MS);
    },
    { immediate: true },
  );

  function say(text) {
    message.value = { text, key: message.value.key + 1 };
  }

  // --- moving through the list -----------------------------------------------

  function show(item) {
    activeKey.value = item?.key || '';
    nextTick(() => {
      if (item) {
        document
          .getElementById(optionId(item))
          ?.scrollIntoView?.({ block: 'nearest' });
      }
    });
  }

  function move(by) {
    const all = options.value;

    if (all.length) {
      const index = Math.max(all.indexOf(activeItem.value), 0);

      show(all[(index + by + all.length) % all.length]);
    }
  }

  function moveGroup(by) {
    const groups = results.value.groups.filter((group) => group.items.length);
    const index = groups.findIndex((group) =>
      group.items.includes(activeItem.value),
    );

    if (groups.length) {
      show(groups[(index + by + groups.length) % groups.length].items[0]);
    }
  }

  // Something new to search: the highlight goes back to the first option.
  function reset(text) {
    query.value = text;
    activeKey.value = '';
    say('');
    nextTick(() => {
      if (scroller.value) {
        scroller.value.scrollTop = 0;
      }
    });
  }

  function onInput(event) {
    reset(event.target.value);
  }

  // Only a pointer that moved takes the highlight: browsers also report a
  // move when the list changes under a resting pointer, which would undo
  // the arrow keys.
  let pointer = '';

  function onPointerMove(event) {
    const at = `${event.clientX},${event.clientY}`;
    const key = event.target.closest?.('[role="option"]')?.dataset.key;

    if (at !== pointer && key) {
      activeKey.value = key;
    }

    pointer = at;
  }

  // --- steps -------------------------------------------------------------------

  function enterStep(command, picked = []) {
    trail.push(query.value);
    step.value = { command, picked };
    reset('');
  }

  function back() {
    const { command, picked } = step.value;

    step.value = picked.length
      ? { command, picked: picked.slice(0, -1) }
      : null;
    reset(trail.pop() ?? '');
  }

  // --- running -----------------------------------------------------------------

  let closing = false;
  // Shift+Enter changed the selection, and no command has run since.
  let toggled = false;

  function close() {
    if (!closing) {
      closing = true;
      emit('close');
    }
  }

  // Closes first: the view's dialog is gone and focus is back where it was
  // when the command runs.
  async function run(command, choice, picked = []) {
    if (!command.prefix) {
      recent.value = rememberCommand(
        command.id,
        picked.map((entry) => entry.id),
      );
    }

    toggled = false;
    close();
    await nextTick();
    runCommand(
      command,
      { ...props.context, source: 'palette' },
      choice,
      choice === undefined ? undefined : picked,
    );
  }

  // Shift+Enter on a node: the selection changes and the palette stays, so
  // the next node can be added. Its own status line says what changed, and
  // Go to node leaves the live region alone (it waits behind the dialog,
  // and would speak every change late); the palette sums the selection up
  // once it has closed.
  function toggleNode(item) {
    toggled = true;
    const { store } = props.context;
    const { message: text } = pressSelection(
      store.doc,
      store.selection,
      { kind: 'nodes', id: item.choice.value },
      true,
    );

    runCommand(
      item.command,
      { ...props.context, source: 'palette', additive: true },
      item.choice,
      [item.choice],
    );
    say(`${text}.`);
  }

  function choose(item, { additive = false } = {}) {
    if (!item) {
      return;
    }

    show(item);

    if (item.disabled) {
      say(`${item.title} is unavailable. ${item.disabled}`);

      return;
    }

    const { command } = item;

    switch (item.kind) {
      case 'prefix':
        reset(item.query);
        break;
      case 'recent':
        run(command, item.choice, item.picked);
        break;
      case 'command':
        if (command.prefix) {
          reset(command.prefix);
        } else if (command.choices) {
          enterStep(command, item.picked);
        } else {
          run(command);
        }
        break;
      case 'choice': {
        const picked = [...item.picked, item.choice];

        if (picked.length < stepCount(command)) {
          enterStep(command, picked);
        } else if (additive && command.id === 'goto.node') {
          toggleNode(item);
        } else {
          run(command, item.choice, picked);
        }
        break;
      }
      default:
        break;
    }
  }

  // The palette's own key closes it, from the field or the Close button,
  // rather than reaching the browser (Firefox would move focus to its
  // search bar). The view's key dispatcher leaves keys in dialogs alone,
  // and the field keeps a key that types in it, as every text field does
  // (see dispatchKeydown): on macOS, one pressed with ⌥.
  function onDialogKeydown(event) {
    const typing = event.target === field.value;

    if (
      !event.defaultPrevented &&
      !event.isComposing &&
      event.keyCode !== 229 &&
      commandKeys('palette.open').some(
        (key) => !(typing && typesCharacter(key)) && matchesKey(event, key),
      )
    ) {
      event.preventDefault();
      close();
    }
  }

  function onKeydown(event) {
    if (event.isComposing || event.keyCode === 229) {
      return;
    }

    if (event.altKey || event.ctrlKey || event.metaKey) {
      return;
    }

    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowUp':
        event.preventDefault();
        move(event.key === 'ArrowDown' ? 1 : -1);
        break;
      case 'PageDown':
      case 'PageUp':
        event.preventDefault();
        moveGroup(event.key === 'PageDown' ? 1 : -1);
        break;
      case 'Enter':
        event.preventDefault();
        choose(activeItem.value, { additive: event.shiftKey });
        break;
      case 'Backspace':
        if (!query.value && step.value) {
          event.preventDefault();
          back();
        }
        break;
      default:
        break;
    }
  }

  onMounted(() => {
    const command = props.request?.command
      ? getCommand(props.request.command)
      : null;

    if (command?.choices) {
      step.value = { command, picked: [] };
    } else {
      query.value = props.request?.query || '';
    }

    field.value?.focus();
  });

  onBeforeUnmount(() => clearTimeout(countTimer));

  // BuilderDialog has put focus back where it was. Should that control be
  // gone (or focus was on the page itself), it goes to the canvas or the
  // landing's tab rather than staying on <body> (WCAG 2.4.3).
  onUnmounted(() => {
    const active = document.activeElement;

    if (active && active !== document.body) {
      return;
    }

    if (viewOf(props.context) === 'editor') {
      props.context.view.focusCanvas();
    } else {
      document.querySelector('.builder-tab[aria-selected="true"]')?.focus();
    }
  });

  // Closed after Shift+Enter, without running a command: one summary of the
  // selection, now that the live region can be read.
  onUnmounted(() => {
    if (toggled) {
      props.context.store.announce(selectionSummary(props.context.store));
    }
  });
</script>

<style scoped>
  /* A narrower dialog near the top of the window, as command palettes are,
     so the list grows downward as it fills. */
  .builder-command-palette {
    width: min(36rem, calc(100vw - 2rem));
    max-height: min(38rem, 84vh);
    margin: 10vh auto auto;
    padding: 0;
    overflow: hidden;
  }

  .builder-command-palette[open] {
    display: flex;
    flex-direction: column;
  }

  .builder-command-palette__search {
    display: flex;
    flex: none;
    align-items: center;
    gap: 0.5rem;
    padding: 0.45rem 0.45rem 0.45rem 0.75rem;
    border-bottom: 1px solid var(--bx-border);
    color: var(--bx-text-muted);
  }

  /* The field has no border of its own; the whole row shows its focus. */
  .builder-command-palette__search:focus-within {
    outline: 3px solid var(--bx-focus);
    outline-offset: -3px;
  }

  .builder-command-palette__search input {
    flex: 1 1 auto;
    min-width: 0;
    padding: 0.3rem 0;
    border: 0;
    background: transparent;
    color: var(--bx-text);
    font-size: 1rem;
  }

  .builder-command-palette__search input:focus-visible {
    outline: none;
  }

  .builder-command-palette__search input::placeholder {
    color: var(--bx-text-muted);
    opacity: 1;
  }

  .builder-command-palette__chip {
    flex: none;
    max-width: 50%;
    padding: 0.05rem 0.5rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: 999px;
    background: var(--bx-bg-alt);
    color: var(--bx-text);
    font-size: 0.8rem;
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .builder-command-palette__close {
    flex: none;
    justify-content: center;
    min-width: 24px;
    min-height: 24px;
    padding: 0.25rem;
  }

  .builder-command-palette__list {
    flex: 1 1 auto;
    min-height: 0;
    overflow-y: auto;
    padding: 0.3rem;
  }

  .builder-command-palette__empty {
    flex: none;
    padding: 0.6rem 0.75rem 0.25rem;
  }

  .builder-command-palette__empty p {
    margin: 0 0 0.25rem;
    overflow-wrap: anywhere;
  }

  .builder-command-palette__group {
    padding: 0.45rem 0.5rem 0.2rem;
    color: var(--bx-text-muted);
    font-size: 0.7rem;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .builder-command-palette__option {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    min-height: 2.25rem;
    padding: 0.3rem 0.5rem;
    border: 1px solid transparent;
    border-radius: var(--bx-radius);
    color: var(--bx-text);
    cursor: pointer;
  }

  .builder-command-palette__option > .builder-icon {
    color: var(--bx-text-muted);
  }

  .builder-command-palette__option[aria-selected='true'] {
    border-color: var(--bx-accent);
    background: var(--bx-selected-bg);
  }

  .builder-command-palette__option[aria-selected='true'] > .builder-icon {
    color: var(--bx-accent);
  }

  .builder-command-palette__option[aria-disabled='true'] {
    color: var(--bx-text-muted);
    cursor: default;
  }

  .builder-command-palette__text {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
  }

  /* Long names and addresses wrap rather than being cut off, so nothing is
     lost at narrow widths or with enlarged text. */
  .builder-command-palette__title {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .builder-command-palette__option[aria-disabled='true']
    .builder-command-palette__title {
    font-weight: 500;
  }

  .builder-command-palette__detail {
    color: var(--bx-text-muted);
    font-size: 0.8rem;
    overflow-wrap: anywhere;
  }

  .builder-command-palette__off {
    color: var(--bx-text);
  }

  /* Matched letters are bold and underlined, not only colored. */
  .builder-command-palette mark {
    background: transparent;
    color: inherit;
    font-weight: 700;
    text-decoration: underline 2px var(--bx-accent);
    text-underline-offset: 3px;
  }

  .builder-command-palette__next {
    flex: none;
    color: var(--bx-text-muted);
    font-weight: 600;
  }

  .builder-command-palette__more {
    flex: none;
    margin: 0;
    padding: 0.35rem 0.75rem;
    border-top: 1px solid var(--bx-border);
  }

  .builder-command-palette__message {
    flex: none;
    margin: 0;
    padding: 0 0.75rem;
  }

  .builder-command-palette__message > span:not(:empty) {
    display: block;
    padding: 0.35rem 0;
    overflow-wrap: anywhere;
  }

  .builder-command-palette__foot {
    display: flex;
    flex: none;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.3rem 0.9rem;
    padding: 0.4rem 0.75rem;
    border-top: 1px solid var(--bx-border);
    background: var(--bx-bg-alt);
    color: var(--bx-text-muted);
    font-size: 0.75rem;
  }

  .builder-command-palette__hint {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
  }

  .builder-command-palette__count {
    margin-inline-start: auto;
    font-variant-numeric: tabular-nums;
  }

  /* Forced colors draw transparent borders, so only the highlighted option
     keeps its frame, in the system's highlight. */
  @media (forced-colors: active) {
    .builder-command-palette__option {
      border-color: Canvas;
    }

    .builder-command-palette__option[aria-selected='true'] {
      forced-color-adjust: none;
      border-color: Highlight;
      background: Highlight;
      color: HighlightText;
    }

    .builder-command-palette__option[aria-selected='true'] > .builder-icon,
    .builder-command-palette__option[aria-selected='true']
      .builder-command-palette__detail,
    .builder-command-palette__option[aria-selected='true']
      .builder-command-palette__off,
    .builder-command-palette__option[aria-selected='true']
      .builder-command-palette__next {
      color: HighlightText;
    }

    .builder-command-palette__option[aria-disabled='true']:not(
        [aria-selected='true']
      ) {
      color: GrayText;
    }

    /* A mark is forced to MarkText, which vanishes on a dark Canvas without
       its Mark background: a match takes its row's text color instead,
       bold and underlined. */
    .builder-command-palette mark {
      color: CanvasText;
      text-decoration-color: currentColor;
    }

    .builder-command-palette__option[aria-disabled='true'] mark {
      color: GrayText;
    }

    .builder-command-palette__option[aria-selected='true'] mark {
      color: HighlightText;
    }
  }
</style>
