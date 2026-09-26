<!--
  Editor toolbar.

  Every control is a labelled button. Its tooltip gives its keyboard
  shortcut, if it has one, from the command registry (commands.js), so the
  keys are this platform's and follow the user's changes. The groups, left
  to right: undo and redo, the clipboard, grouping (Group, Ungroup and the
  Auto-group menu), the layout menu and scenario, the ways out (Export,
  Upload, Publish), the view, then History and Commands at the right end.
  The layout menu is named after the draft's layout; it lays the diagram out
  with the one chosen, which the draft keeps, and after a layout it offers
  to put the previous one back. Edits are saved as they are made, so there
  is no Save button (Save now is a key and a command); the save state shows
  in the editor header (BuilderBeta.vue), and Retry saving shows at the end,
  while a save needs it.

  It follows the APG toolbar pattern: the toolbar is one Tab stop, and the
  arrow keys, Home and End move between its buttons. Unavailable buttons are
  aria-disabled rather than disabled, so they stay in that sequence and a
  button that becomes unavailable while focused keeps focus. The two menu
  buttons (BuilderMenuButton.vue) are buttons in that sequence; Down and Up
  open their menus.
-->
<template>
  <div
    ref="toolbarEl"
    class="builder-toolbar builder-panel"
    role="toolbar"
    aria-label="Builder actions"
    @focusin="onFocusin"
    @keydown="onKeydown">
    <div class="builder-toolbar__group">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-undo"
        :aria-disabled="off.undo || undefined"
        :aria-keyshortcuts="tips.undo.aria"
        :aria-describedby="
          tips.undo.description ? 'toolbar-tip-undo' : undefined
        "
        v-on="tipFor('undo')"
        @click="run('undo', () => store.undo())">
        <builder-icon name="undo" :size="14" />
        Undo
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-redo"
        :aria-disabled="off.redo || undefined"
        :aria-keyshortcuts="tips.redo.aria"
        :aria-describedby="
          tips.redo.description ? 'toolbar-tip-redo' : undefined
        "
        v-on="tipFor('redo')"
        @click="run('redo', () => store.redo())">
        <builder-icon name="redo" :size="14" />
        Redo
      </button>
    </div>

    <div class="builder-toolbar__group">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-copy"
        :aria-keyshortcuts="tips.copy.aria"
        :aria-describedby="
          tips.copy.description ? 'toolbar-tip-copy' : undefined
        "
        v-on="tipFor('copy')"
        @click="store.copy()">
        <builder-icon name="copy" :size="14" />
        Copy
      </button>
      <button
        type="button"
        class="builder-button"
        :aria-disabled="off.paste || undefined"
        data-testid="toolbar-paste"
        :aria-keyshortcuts="tips.paste.aria"
        :aria-describedby="
          tips.paste.description ? 'toolbar-tip-paste' : undefined
        "
        v-on="tipFor('paste')"
        @click="run('paste', () => store.paste())">
        <builder-icon name="paste" :size="14" />
        Paste
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-delete"
        :aria-keyshortcuts="tips.delete.aria"
        :aria-describedby="
          tips.delete.description ? 'toolbar-tip-delete' : undefined
        "
        v-on="tipFor('delete')"
        :aria-disabled="off.delete || undefined"
        @click="run('delete', deleteSelectionHere)">
        <builder-icon name="trash" :size="14" />
        Delete
      </button>
    </div>

    <div class="builder-toolbar__group">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-group"
        :aria-keyshortcuts="tips.group.aria"
        :aria-describedby="
          tips.group.description ? 'toolbar-tip-group' : undefined
        "
        v-on="tipFor('group')"
        :aria-disabled="off.group || undefined"
        @click="run('group', () => store.group())">
        <builder-icon name="group" :size="14" />
        Group
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-ungroup"
        :aria-keyshortcuts="tips.ungroup.aria"
        :aria-describedby="
          tips.ungroup.description ? 'toolbar-tip-ungroup' : undefined
        "
        v-on="tipFor('ungroup')"
        :aria-disabled="off.ungroup || undefined"
        @click="run('ungroup', ungroupSelectionHere)">
        <builder-icon name="ungroup" :size="14" />
        Ungroup
      </button>
      <builder-menu-button
        testid="toolbar-auto-group"
        :items="autoGroupItems"
        :disabled="off.autoGroup"
        :busy="store.autoGrouping"
        :aria-describedby="
          tips.autoGroup.description ? 'toolbar-tip-autoGroup' : undefined
        "
        v-on="tipFor('autoGroup')"
        @toggle="onMenuToggle('autoGroup', $event)"
        @select="store.autoGroup($event.id)">
        <span
          v-if="store.autoGrouping"
          class="builder-toolbar__spinner"
          aria-hidden="true"></span>
        <builder-icon v-else name="auto-group" :size="14" />
        Auto-group
      </builder-menu-button>
    </div>

    <div class="builder-toolbar__group">
      <!-- Named after the draft's layout, with "layout" after it for screen
           readers. While a layout runs, a turning ring in place of the icon;
           reduced motion stops it turning (see builder.css). -->
      <builder-menu-button
        testid="toolbar-layout"
        :items="layoutItems"
        :disabled="off.layout"
        :busy="store.layoutRunning && !store.autoGrouping"
        :aria-label="`${currentLayout.label} layout`"
        :aria-describedby="
          tips.layout.description ? 'toolbar-tip-layout' : undefined
        "
        v-on="tipFor('layout')"
        @toggle="onMenuToggle('layout', $event)"
        @select="chooseLayout">
        <span
          v-if="store.layoutRunning && !store.autoGrouping"
          class="builder-toolbar__spinner"
          aria-hidden="true"></span>
        <builder-icon v-else name="layout" :size="14" />
        <!-- Every layout's name holds the button's width, so it stays under
             the pointer when the layout changes; the hidden ones are not
             shown or named. -->
        <span class="builder-button__swap">
          <span
            v-for="algorithm in LAYOUT_ALGORITHMS"
            :key="algorithm.id"
            :class="{ 'is-off': algorithm.id !== currentLayout.id }">
            {{ algorithm.label }}
          </span>
        </span>
      </builder-menu-button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-scenario"
        :aria-keyshortcuts="tips.scenario.aria"
        :aria-describedby="
          tips.scenario.description ? 'toolbar-tip-scenario' : undefined
        "
        v-on="tipFor('scenario')"
        aria-haspopup="dialog"
        :aria-disabled="off.scenario || undefined"
        @click="run('scenario', () => $emit('scenario'))">
        <builder-icon name="document" :size="14" />
        Scenario
      </button>
    </div>

    <div class="builder-toolbar__group">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-export"
        :aria-keyshortcuts="tips.export.aria"
        :aria-describedby="
          tips.export.description ? 'toolbar-tip-export' : undefined
        "
        v-on="tipFor('export')"
        aria-haspopup="dialog"
        @click="$emit('export')">
        <builder-icon name="download" :size="14" />
        Export
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-import"
        :aria-keyshortcuts="tips.import.aria"
        :aria-describedby="
          tips.import.description ? 'toolbar-tip-import' : undefined
        "
        v-on="tipFor('import')"
        aria-haspopup="dialog"
        :aria-disabled="off.import || undefined"
        @click="run('import', () => $emit('import'))">
        <builder-icon name="upload" :size="14" />
        Upload
      </button>
      <button
        type="button"
        class="builder-button builder-button--primary"
        data-testid="toolbar-publish"
        :aria-keyshortcuts="tips.publish.aria"
        :aria-describedby="
          tips.publish.description ? 'toolbar-tip-publish' : undefined
        "
        v-on="tipFor('publish')"
        aria-haspopup="dialog"
        :aria-disabled="off.publish || undefined"
        @click="run('publish', () => $emit('publish'))">
        <builder-icon name="publish" :size="14" />
        Publish
      </button>
    </div>

    <div class="builder-toolbar__group">
      <!-- It shows the theme in use; its name and tooltip say what a press
           changes it to. -->
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-theme"
        :aria-label="themeButton.name"
        :aria-keyshortcuts="tips.theme.aria"
        :aria-describedby="
          tips.theme.description ? 'toolbar-tip-theme' : undefined
        "
        v-on="tipFor('theme')"
        @click="$emit('cycle-theme')">
        <builder-icon :name="themeButton.icon" :size="14" />
        {{ themeButton.label }}
      </button>
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-minimap"
        :aria-keyshortcuts="tips.minimap.aria"
        :aria-describedby="
          tips.minimap.description ? 'toolbar-tip-minimap' : undefined
        "
        v-on="tipFor('minimap')"
        :aria-pressed="String(minimap)"
        @click="$emit('toggle-minimap')">
        <builder-icon name="image" :size="14" />
        Minimap
      </button>
    </div>

    <div class="builder-toolbar__group builder-toolbar__group--end">
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-history"
        :aria-keyshortcuts="tips.history.aria"
        :aria-describedby="
          tips.history.description ? 'toolbar-tip-history' : undefined
        "
        v-on="tipFor('history')"
        aria-haspopup="dialog"
        @click="$emit('history')">
        <builder-icon name="refresh" :size="14" />
        History
      </button>
      <!-- The key caps show the palette's key; the description and
           aria-keyshortcuts say it to screen readers. -->
      <button
        type="button"
        class="builder-button"
        data-testid="toolbar-commands"
        aria-haspopup="dialog"
        :aria-keyshortcuts="tips.commands.aria"
        :aria-describedby="
          tips.commands.description ? 'toolbar-tip-commands' : undefined
        "
        v-on="tipFor('commands')"
        @click="$emit('commands')">
        <builder-icon name="command" :size="14" />
        Commands
        <builder-keycaps v-if="paletteKey" :spec="paletteKey" />
      </button>
      <!-- Last, so the others stay put while it comes and goes. -->
      <button
        v-if="canRetry"
        ref="retryEl"
        type="button"
        class="builder-button"
        data-testid="toolbar-retry"
        @click="store.retrySave({ announce: true })">
        <builder-icon name="refresh" :size="14" />
        Retry saving
      </button>
    </div>

    <!-- The tooltips' text reaches screen readers as the buttons'
         descriptions; the tooltips themselves are aria-hidden. -->
    <span
      v-for="(entry, key) in tips"
      :id="`toolbar-tip-${key}`"
      :key="key"
      hidden>
      {{ entry.description }}
    </span>
    <div
      v-if="tip"
      ref="tipEl"
      class="builder-tooltip builder-tooltip--fixed"
      data-testid="toolbar-tooltip"
      aria-hidden="true"
      :style="{ top: `${tip.top}px`, left: `${tip.left}px` }">
      {{ tip.text }}
    </div>
  </div>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    onUpdated,
    ref,
    watch,
  } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';
  import BuilderKeycaps from './BuilderKeycaps.vue';
  import BuilderMenuButton from './BuilderMenuButton.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import {
    ariaShortcuts,
    commandKeys,
    deleteSelection,
    shortcutLabel,
    ungroupSelection,
    withShortcut,
  } from '@/builder/commands.js';
  import { GROUPING_STRATEGIES } from '@/builder/grouping.js';
  import {
    LAYOUT_ALGORITHMS,
    layoutAlgorithm,
  } from '@/builder/layouts/index.js';
  import { rowTarget } from '@/builder/roving.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    nextTheme,
    resolveTheme,
    watchSystemTheme,
  } from '@/builder/theme.js';

  defineProps({
    minimap: { type: Boolean, default: true },
  });

  defineEmits([
    'publish',
    'export',
    'import',
    'scenario',
    'history',
    'cycle-theme',
    'toggle-minimap',
    'commands',
  ]);

  const store = useBuilderStore();
  const toolbarEl = ref(null);
  const retryEl = ref(null);

  const hasSelection = computed(
    () => store.selection.nodes.length > 0 || store.selection.edges.length > 0,
  );

  // Ungroup acts on the first selected node, which must be a group.
  const selectedGroup = computed(() => {
    const id = store.selection.nodes[0];

    return (store.doc.nodes || []).find(
      (node) => node.id === id && node.kind === 'group',
    );
  });

  // The actions that are unavailable right now, by name.
  const off = computed(() => ({
    undo: store.readOnly || !store.canUndo,
    redo: store.readOnly || !store.canRedo,
    paste: store.readOnly,
    delete: store.readOnly || !hasSelection.value,
    group: store.readOnly || !store.selection.nodes.length,
    ungroup: store.readOnly || !selectedGroup.value,
    autoGroup: store.readOnly,
    layout: store.readOnly,
    scenario: store.readOnly,
    // Upload makes a new draft; Publish writes configs (see the store's
    // canCreateDrafts and canPublish).
    import: !store.canCreateDrafts,
    publish: store.readOnly || !store.canPublish,
  }));

  function run(action, handler) {
    if (!off.value[action]) {
      handler();
    }
  }

  // --- menus -------------------------------------------------------------------

  const currentLayout = computed(() => layoutAlgorithm(store.currentLayout));

  // The layouts, the draft's checked; choosing one runs it and the draft
  // keeps it, the checked one included. Right after a layout, until the
  // diagram changes some other way, the previous one can be put back.
  const layoutItems = computed(() => [
    ...LAYOUT_ALGORITHMS.map((algorithm) => ({
      id: algorithm.id,
      label: algorithm.label,
      description: algorithm.summary,
      checked: algorithm.id === store.currentLayout,
    })),
    ...(store.canRestoreLayout
      ? [
          {
            id: 'restore',
            label: 'Restore previous layout',
            description: 'Put every node back where it was',
            separator: true,
          },
        ]
      : []),
  ]);

  function chooseLayout(item) {
    if (item.id === 'restore') {
      store.restoreLayout();
    } else {
      store.layout({ algorithm: item.id });
    }
  }

  const autoGroupItems = GROUPING_STRATEGIES.map((strategy) => ({
    id: strategy.id,
    label: strategy.label,
    description: strategy.summary,
  }));

  // The menu that is open, whose button shows no tooltip over it.
  const openMenu = ref('');

  function onMenuToggle(key, open) {
    openMenu.value = open ? key : '';

    if (open) {
      hideTip();
    }
  }

  // --- roving Tab stop -------------------------------------------------------

  // The button that holds the toolbar's Tab stop: the last one focused.
  let current = null;

  function buttons() {
    return [...(toolbarEl.value?.querySelectorAll('button') || [])];
  }

  // The template leaves tabindex unbound, so a re-render keeps these values;
  // a button added later (Retry saving) is brought into line on update.
  function syncTabStop() {
    const all = buttons();
    if (!all.includes(current)) {
      current = all[0] || null;
    }

    for (const button of all) {
      button.tabIndex = button === current ? 0 : -1;
    }
  }

  function onFocusin(event) {
    const button = event.target.closest?.('button');
    if (button && buttons().includes(button)) {
      current = button;
      syncTabStop();
    }
  }

  function onKeydown(event) {
    const all = buttons();
    const index = all.indexOf(event.target);
    if (event.altKey || event.ctrlKey || event.metaKey) {
      return;
    }

    const next = rowTarget(event.key, index, all.length);
    if (next === undefined) {
      return;
    }

    event.preventDefault();
    all[next].focus();
  }

  onMounted(syncTabStop);
  onUpdated(syncTabStop);

  // Delete and Ungroup move focus on to the outline row that takes the
  // removed node's place, as the commands do (see commands.js).
  function deleteSelectionHere() {
    return deleteSelection(store);
  }

  function ungroupSelectionHere() {
    return ungroupSelection(store);
  }

  // --- theme -------------------------------------------------------------------

  const THEMES = {
    system: { icon: 'system', label: 'System' },
    light: { icon: 'sun', label: 'Light' },
    dark: { icon: 'moon', label: 'Dark' },
  };

  const matchMedia =
    typeof window !== 'undefined' && window.matchMedia
      ? window.matchMedia.bind(window)
      : undefined;

  // What System shows, which decides the theme a press moves to (see
  // nextTheme). It follows the system's changes while any theme is chosen.
  const systemTheme = ref(resolveTheme('system', matchMedia));
  const stopWatchingSystemTheme = watchSystemTheme(matchMedia, () => {
    systemTheme.value = resolveTheme('system', matchMedia);
  });

  onBeforeUnmount(stopWatchingSystemTheme);

  const themeButton = computed(() => {
    const theme = THEMES[store.theme] ? store.theme : 'system';
    const next = nextTheme(theme, systemTheme.value);
    const tip = `Switch to ${THEMES[next].label} theme`;

    return {
      ...THEMES[theme],
      next,
      tip,
      name: `Theme: ${THEMES[theme].label}. ${tip}.`,
    };
  });

  // --- tooltips ----------------------------------------------------------------

  // The command each button runs, for its keys, and its name in the
  // tooltip. aria-keyshortcuts only where the keys work with focus here:
  // Delete works on the canvas and in the outline.
  const TIPS = {
    undo: { command: 'edit.undo', name: 'Undo' },
    redo: { command: 'edit.redo', name: 'Redo' },
    copy: { command: 'edit.copy', name: 'Copy' },
    paste: { command: 'edit.paste', name: 'Paste' },
    delete: { command: 'edit.delete', name: 'Delete selection', local: true },
    group: { command: 'structure.group', name: 'Group selection' },
    ungroup: { command: 'structure.ungroup', name: 'Ungroup' },
    history: { command: 'draft.history', name: 'Draft history' },
    export: { command: 'draft.export', name: 'Export' },
    import: { command: 'draft.upload', name: 'Upload' },
    scenario: { command: 'draft.scenario', name: 'Scenario' },
    publish: { command: 'draft.publish', name: 'Publish' },
    minimap: { command: 'view.minimap', name: 'Minimap' },
    commands: { command: 'palette.open', name: 'Command palette' },
  };

  // The first key of the command palette, shown on its button.
  const paletteKey = computed(() => commandKeys('palette.open')[0] || '');

  // Per button: the tooltip's text ('' for none), the description screen
  // readers get in its place (the keys: the name is the button's own), and
  // aria-keyshortcuts. The menus' tooltips say what they do, keys or not;
  // the layout's keys run the layout again rather than open its menu.
  const tips = computed(() => {
    const entries = Object.entries(TIPS).map(([key, entry]) => {
      const keys = shortcutLabel(entry.command);

      return [
        key,
        {
          text: keys ? `${entry.name} (${keys})` : '',
          description: keys,
          aria: entry.local ? undefined : ariaShortcuts(entry.command),
        },
      ];
    });
    const layoutKeys = shortcutLabel('structure.layout');
    const layoutText = `Choose a layout, or run it again${
      layoutKeys ? `. ${layoutKeys} runs it again` : ''
    }`;
    const autoGroupText = store.selection.nodes.length
      ? 'Group the selected nodes automatically'
      : 'Group the ungrouped nodes automatically';
    // The theme button's keys are those of the palette's command for the
    // theme it moves to; its name says the rest.
    const themeCommand = `view.theme.${themeButton.value.next}`;

    return {
      ...Object.fromEntries(entries),
      layout: { text: layoutText, description: layoutText },
      autoGroup: { text: autoGroupText, description: autoGroupText },
      theme: {
        text: withShortcut(themeButton.value.tip, themeCommand),
        description: shortcutLabel(themeCommand),
        aria: ariaShortcuts(themeCommand),
      },
    };
  });

  const { tip, tipEl, showTip, scheduleHide, hideTip } = useFixedTooltip({
    side: 'below',
  });

  // The button whose tooltip is shown.
  let shown = null;

  // Read when shown, so a tooltip follows the button's state. None over an
  // open menu.
  function tipFor(key) {
    const show = (event) => {
      if (tips.value[key].text && openMenu.value !== key) {
        shown = { key, target: event.currentTarget };
        showTip(event, tips.value[key].text);
      }
    };

    return {
      mouseenter: show,
      mouseleave: scheduleHide,
      focus: show,
      blur: hideTip,
    };
  }

  // A press from the keyboard leaves the tooltip up, so when the press
  // changes what the button does next (the theme), the tooltip changes
  // with it rather than describing the press just made.
  watch(
    () => Boolean(tip.value) && shown && tips.value[shown.key].text,
    (text) => {
      if (!text || text === tip.value?.text || !shown.target.isConnected) {
        return;
      }

      // Only while the button still has focus or the pointer: a hide that
      // is already pending must not be cancelled.
      const target = shown.target;
      if (target === document.activeElement || target.matches(':hover')) {
        showTip({ currentTarget: target }, text);
      }
    },
  );

  // Not for an error that sending again cannot fix (a refused snapshot):
  // the save state says what to change instead.
  const canRetry = computed(
    () =>
      store.saveState.status === 'offline' ||
      (store.saveState.status === 'error' &&
        store.saveState.retryable !== false),
  );

  // Retry saving goes away as soon as a save starts or succeeds, whether the
  // user pressed it or the automatic retry ran. If it had focus, the button
  // before it (Commands) takes the toolbar's Tab stop, and focus moves there
  // instead of falling to <body> (WCAG 2.4.3). The check runs before the
  // button is removed. The same update may have given focus a better place:
  // a conflict moves it to the conflict panel, which then keeps it.
  watch(
    canRetry,
    (now) => {
      if (now || !retryEl.value?.contains(document.activeElement)) {
        return;
      }

      const all = buttons();
      const before = all[all.indexOf(retryEl.value) - 1];

      nextTick(() => {
        if (!before?.isConnected) {
          return;
        }

        current = before;
        syncTabStop();

        const active = document.activeElement;
        if (!active || active === document.body || !active.isConnected) {
          before.focus();
        }
      });
    },
    { flush: 'pre' },
  );
</script>
