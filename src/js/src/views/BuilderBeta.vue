<!--
  Builder Beta view.

  Owns the editor shell: theme, live regions, conflict banner, dialogs and the
  drafts landing. The legacy /builder editor is untouched and still reachable
  from the header.
-->
<template>
  <div
    ref="rootEl"
    class="builder-root"
    :class="{ 'builder-root--editing': editing }"
    :data-builder-reduced-motion="String(reducedMotion)">
    <!-- The target is the canvas <section> itself, which is named and
         handles the canvas keys. -->
    <a v-if="editing" class="builder-skip-link" href="#builder-canvas">
      Skip to diagram canvas
    </a>

    <builder-live-region />

    <!-- Hidden while a dialog is open: the dialog shows its own errors, and
         this alert would repeat them behind the modal. -->
    <div
      v-if="store.error"
      v-show="!dialog"
      ref="errorPanel"
      class="builder-panel builder-error builder-page-error"
      data-testid="builder-error">
      <p role="alert">
        <!-- Keyed, so the same failure twice is announced twice. -->
        <span :key="store.errorSeq">{{ store.error }}</span>
      </p>
      <button
        type="button"
        class="builder-button"
        data-testid="builder-error-dismiss"
        @click="store.clearError()">
        Dismiss
      </button>
    </div>

    <template v-if="!editing">
      <builder-drafts
        ref="drafts"
        :mine="store.drafts.mine"
        :shared="store.drafts.shared"
        :published="store.documents"
        :loading="store.loading && !listed"
        :can-create="store.canCreateDrafts"
        :can-delete="store.canDeleteDrafts"
        :busy="busy"
        @blank="startBlank"
        @import="openLanding('import')"
        @generate="openLanding('generate')"
        @open="openDraft"
        @delete="deleteDraft"
        @commands="commandView.openPalette()"
        @settings="runCommand('settings.open', commandContext)" />
    </template>

    <template v-else>
      <div class="builder-header">
        <!-- The name field shows the diagram name; the heading gives the view
             a title and takes focus when the editor opens. -->
        <h1 ref="editorHeading" class="builder-visually-hidden" tabindex="-1">
          {{ diagramName }} – Builder Flow
        </h1>
        <button
          type="button"
          class="builder-button"
          data-testid="editor-back"
          @click="closeEditor">
          <builder-icon name="arrow-left" :size="14" />
          Back to drafts
        </button>
        <!-- Its label is read rather than shown; a tooltip gives it on
             hover. -->
        <div class="builder-field builder-header__name">
          <label for="builder-doc-name" class="builder-visually-hidden">
            Diagram name
          </label>
          <input
            id="builder-doc-name"
            v-model="nameField"
            :readonly="store.readOnly"
            type="text"
            placeholder="Diagram name"
            data-testid="builder-name"
            @mouseenter="showTip($event, 'Diagram name')"
            @mouseleave="scheduleHide"
            @focus="nameFocused = true"
            @blur="onNameBlur"
            @change="rename(nameField)" />
        </div>

        <builder-counts />

        <!-- The save state, then the buttons. Reset view's tooltip says what
             it resets, Shortcuts' and Settings' name what they open, Help's
             repeats its name, and Focus mode's says what it does; each
             gives the command's keys, if it has any, as the toolbar's do.
             In a narrow header, Reset view, Shortcuts, Settings and Help
             show only their icons, as Focus mode always does (see the
             styles below); their labels stay as their names. -->
        <div class="builder-header__actions">
          <!-- Shown but not spoken: it changes on every edit, so the store
               announces only the transitions that matter (a new problem, or
               the recovery from one) through the live region. Retry saving
               is in the toolbar, with the other actions. -->
          <p
            class="builder-status builder-header__save"
            :class="`builder-status--${store.saveState.status}`"
            data-testid="builder-save-state">
            <span class="builder-status__dot" aria-hidden="true"></span>
            <!-- Decorative: the text beside it says the same. -->
            <builder-icon v-if="saveNeedsAttention" name="warning" :size="14" />
            {{ store.saveStateText }}
          </p>
          <button
            type="button"
            class="builder-button builder-header__button"
            data-testid="editor-reset-view"
            :aria-keyshortcuts="headerTips.reset.aria"
            aria-describedby="editor-tip-reset"
            v-on="headerTip('reset')"
            @click="resetView">
            <builder-icon name="reset-view" :size="14" />
            <span class="builder-header__label">Reset view</span>
          </button>
          <button
            type="button"
            class="builder-button builder-header__button"
            data-testid="editor-shortcuts"
            aria-haspopup="dialog"
            :aria-keyshortcuts="headerTips.shortcuts.aria"
            :aria-describedby="
              headerTips.shortcuts.description
                ? 'editor-tip-shortcuts'
                : undefined
            "
            v-on="headerTip('shortcuts')"
            @click="runCommand('shortcuts.open', commandContext)">
            <builder-icon name="keyboard" :size="14" />
            <span class="builder-header__label">Shortcuts</span>
          </button>
          <button
            type="button"
            class="builder-button builder-header__button"
            data-testid="editor-settings"
            aria-haspopup="dialog"
            :aria-keyshortcuts="headerTips.settings.aria"
            :aria-describedby="
              headerTips.settings.description
                ? 'editor-tip-settings'
                : undefined
            "
            v-on="headerTip('settings')"
            @click="runCommand('settings.open', commandContext)">
            <builder-icon name="settings" :size="14" />
            <span class="builder-header__label">Settings</span>
          </button>
          <a
            class="builder-button builder-header__button builder-help-link"
            :href="HELP_URL"
            target="_blank"
            rel="noopener"
            data-testid="editor-help"
            v-on="headerTip('help')">
            <builder-icon name="help" :size="14" />
            <span class="builder-header__label">
              Help
              <span class="builder-visually-hidden">(opens in a new tab)</span>
            </span>
          </a>
          <builder-checks />
          <!-- An icon, named for screen readers: Exit focus mode while focus
               mode is on, since the phenix navigation bar has gone. It stays
               one element, so focus stays on it. -->
          <button
            ref="focusButton"
            type="button"
            class="builder-button builder-header__button"
            data-testid="editor-focus-mode"
            :aria-keyshortcuts="headerTips.focus.aria"
            :aria-describedby="
              headerTips.focus.description ? 'editor-tip-focus' : undefined
            "
            v-on="headerTip('focus')"
            @click="toggleFocusMode">
            <builder-icon
              :name="focusMode.on ? 'focus-exit' : 'focus'"
              :size="14" />
            <span class="builder-visually-hidden">
              {{ focusMode.on ? 'Exit focus mode' : 'Focus mode' }}
            </span>
          </button>
        </div>

        <!-- The tooltips' text reaches screen readers as the buttons' names
             and descriptions; the tooltips themselves are aria-hidden. -->
        <span
          v-for="(entry, key) in headerTips"
          :id="`editor-tip-${key}`"
          :key="key"
          hidden>
          {{ entry.description }}
        </span>
        <div
          v-if="tip"
          ref="tipEl"
          class="builder-tooltip builder-tooltip--fixed"
          data-testid="header-tooltip"
          aria-hidden="true"
          :style="{ top: `${tip.top}px`, left: `${tip.left}px` }">
          {{ tip.text }}
        </div>
      </div>

      <!-- Not modal: the diagram stays readable (and exportable) while the
           user decides. The explanation is an alert, and focus moves to the
           heading, so the two choices are the next Tab stops. -->
      <section
        v-if="store.hasConflict"
        class="builder-panel builder-conflict"
        aria-labelledby="conflict-title"
        data-testid="builder-conflict">
        <h2 id="conflict-title" ref="conflictHeading" tabindex="-1">
          This draft changed on the server
        </h2>
        <!-- The alert stays unchanged while the panel is open; the count
             below changes with every edit and must not be re-announced. -->
        <p role="alert">
          Someone else saved a newer version of this draft, so your changes
          cannot be written over it.
        </p>
        <p>
          Your {{ unsavedSummary }}. Choose how to keep your work: edits you
          make now are not saved until you choose.
        </p>
        <div class="builder-conflict__actions">
          <button
            type="button"
            class="builder-button builder-button--primary"
            data-testid="conflict-fork"
            :aria-disabled="resolving || undefined"
            @click="resolveConflict('fork')">
            Save my history as a new draft
          </button>
          <button
            type="button"
            class="builder-button"
            data-testid="conflict-reload"
            :aria-disabled="resolving || undefined"
            @click="resolving || (confirmingDiscard = true)">
            Discard mine and load the server version
          </button>
        </div>
      </section>

      <builder-confirm
        v-if="confirmingDiscard"
        id="conflict-discard"
        title="Discard your unsaved changes?"
        :message="`Your ${unsavedSummary}. Discarding deletes that work and loads the server version. This cannot be undone. To keep your work, cancel and choose Save my history as a new draft.`"
        confirm-label="Discard and load the server version"
        @cancel="confirmingDiscard = false"
        @confirm="discardLocal" />

      <!-- Leaving a draft whose edits the server does not have yet asks
           first (see mayLeave). -->
      <builder-confirm
        v-if="leaving"
        id="leave-unsaved"
        title="Leave with unsaved changes?"
        :message="leaveMessage"
        confirm-label="Leave anyway"
        cancel-label="Stay"
        @cancel="settleLeave(false)"
        @confirm="settleLeave(true)" />

      <!-- A published diagram opens read only, with no draft of its own:
           one is made only when the user chooses to edit it. The
           live region has said it opened read only. -->
      <div
        v-if="store.published"
        class="builder-panel builder-published"
        data-testid="builder-published">
        <p>
          You are viewing the published diagram {{ store.published.name }}.
          <template v-if="store.canCreateDrafts">
            Edit it as a draft to make changes.
          </template>
          <template v-else>
            Your role cannot create drafts, so it cannot be edited. Use Export
            to keep a copy.
          </template>
        </p>
        <button
          v-if="store.canCreateDrafts"
          type="button"
          class="builder-button builder-button--primary"
          data-testid="published-edit"
          :aria-disabled="busy || undefined"
          @click="editPublished">
          Edit as a draft
        </button>
      </div>

      <div
        v-else-if="store.readOnly"
        class="builder-panel builder-error"
        role="status"
        data-testid="builder-readonly">
        You can view this draft but not change it. Use Export to keep a copy.
      </div>

      <builder-toolbar
        :minimap="showMinimap"
        @publish="dialog = 'publish'"
        @export="dialog = 'export'"
        @import="openUpload"
        @scenario="dialog = 'scenario'"
        @history="openHistory"
        @cycle-theme="cycleTheme"
        @toggle-minimap="showMinimap = !showMinimap"
        @commands="commandView.openPalette()" />

      <builder-panes ref="panes">
        <template #start>
          <builder-palette />
          <builder-outline />
        </template>

        <!-- A new canvas for each diagram, as Upload opens one without
             leaving the editor: it opens with the zoom the settings ask
             for. -->
        <builder-canvas
          :key="openDiagram"
          ref="canvas"
          :show-minimap="showMinimap"
          :reduced-motion="reducedMotion" />

        <template #end>
          <builder-inspector ref="inspector" />
        </template>
      </builder-panes>
    </template>

    <publish-dialog v-if="dialog === 'publish'" @close="dialog = ''" />
    <!-- Upload, on the landing and in the toolbar. Import on the landing
         opens the generate dialog. -->
    <import-dialog
      v-if="dialog === 'import'"
      @close="dialog = ''"
      @imported="afterImport" />
    <generate-dialog
      v-if="dialog === 'generate'"
      @close="dialog = ''"
      @generated="afterImport" />
    <export-dialog
      v-if="dialog === 'export'"
      :viewport-element="viewportElement"
      @close="dialog = ''" />
    <scenario-dialog v-if="dialog === 'scenario'" @close="dialog = ''" />

    <history-dialog v-if="dialog === 'history'" @close="dialog = ''" />

    <!-- The command palette, opened by the palette.open command (Mod+K), the
         Commands buttons and commands that ask for a choice, through
         commandView.openPalette, which leaves what to show first in
         paletteRequest. -->
    <builder-command-palette
      v-if="dialog === 'commands'"
      :context="commandContext"
      :request="paletteRequest"
      @close="dialog = ''" />

    <!-- The shortcut sheet, where shortcuts are changed too, opened by the
         shortcuts.open command (? or the palette) through
         commandView.openShortcuts, and by the canvas's Keyboard help. -->
    <builder-shortcuts
      v-if="dialog === 'shortcuts'"
      data-testid="shortcuts-dialog"
      @close="dialog = ''" />

    <!-- Builder settings, opened by the header's and the landing's
         Settings and the settings.open command (the palette). Its Theme is
         the toolbar toggle's, set without an announcement. -->
    <builder-settings
      v-if="dialog === 'settings'"
      @theme="(theme) => store.setTheme(theme, rootEl, { announce: false })"
      @close="dialog = ''" />
  </div>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    provide,
    ref,
    watch,
    watchEffect,
  } from 'vue';
  import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';

  import '@/builder/builder.css';

  import BuilderCanvas from '@/components/builder/BuilderCanvas.vue';
  import BuilderChecks from '@/components/builder/BuilderChecks.vue';
  import BuilderCommandPalette from '@/components/builder/BuilderCommandPalette.vue';
  import BuilderConfirm from '@/components/builder/BuilderConfirm.vue';
  import BuilderCounts from '@/components/builder/BuilderCounts.vue';
  import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';
  import BuilderIcon from '@/components/builder/BuilderIcon.vue';
  import BuilderInspector from '@/components/builder/BuilderInspector.vue';
  import BuilderLiveRegion from '@/components/builder/BuilderLiveRegion.vue';
  import BuilderOutline from '@/components/builder/BuilderOutline.vue';
  import BuilderPalette from '@/components/builder/BuilderPalette.vue';
  import BuilderPanes from '@/components/builder/BuilderPanes.vue';
  import BuilderSettings from '@/components/builder/BuilderSettings.vue';
  import BuilderShortcuts from '@/components/builder/BuilderShortcuts.vue';
  import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
  import ExportDialog from '@/components/builder/dialogs/ExportDialog.vue';
  import GenerateDialog from '@/components/builder/dialogs/GenerateDialog.vue';
  import HistoryDialog from '@/components/builder/dialogs/HistoryDialog.vue';
  import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';
  import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';
  import ScenarioDialog from '@/components/builder/dialogs/ScenarioDialog.vue';
  import { useFixedTooltip } from '@/components/builder/fixedTooltip.js';
  import { closeSections } from '@/components/builder/inspector/InspectorSectionRenderer.vue';

  import {
    applyTheme,
    prefersReducedMotion,
    watchSystemTheme,
  } from '@/builder/theme.js';
  import { count } from '@/builder/announce.js';
  import {
    TYPING,
    ariaShortcuts,
    createCommandContext,
    dispatchKeydown,
    runCommand,
    shortcutLabel,
    withShortcut,
  } from '@/builder/commands.js';
  import {
    enterFocusMode,
    exitFocusMode,
    focusMode,
    followFullScreen,
  } from '@/builder/focusMode.js';
  import { HELP_URL } from '@/builder/help.js';
  import { uniqueName } from '@/builder/ids.js';
  import { followShortcutSettings } from '@/builder/keymap.js';
  import { stopLayoutEngine } from '@/builder/layouts/index.js';
  // Not builderSettings: <builder-settings> would name it as well as the
  // dialog.
  import { builderSettings as editorSettings } from '@/builder/settings.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { usePhenixStore } from '@/store.js';

  const store = useBuilderStore();
  const route = useRoute();
  const router = useRouter();

  const rootEl = ref(null);
  const panes = ref(null);
  const canvas = ref(null);
  const inspector = ref(null);
  const drafts = ref(null);
  const editorHeading = ref(null);
  const conflictHeading = ref(null);
  const errorPanel = ref(null);
  const editing = ref(false);
  // The open dialog: publish, import, generate, export, scenario, history,
  // commands (the command palette), shortcuts (the shortcut sheet) or
  // settings.
  const dialog = ref('');
  // What the command palette shows first: a query ('@' for Go to node) or
  // the choices of one command (its id), as commandView.openPalette asked.
  const paletteRequest = ref({ query: '', command: '' });
  // The minimap as the settings have it; the toolbar's toggle changes it
  // until the next diagram opens.
  const showMinimap = ref(editorSettings.showMinimap);
  // Motion is reduced as the system asks, or always if the settings say so.
  const systemReducesMotion = ref(false);
  const reducedMotion = computed(
    () => editorSettings.reduceMotion || systemReducesMotion.value,
  );

  // Changes each time a diagram is opened in the editor (a draft, a
  // published diagram shown read only, or an upload in the editor), but not
  // when a conflict forks the draft, which keeps the view as it is.
  const openDiagram = computed(() => store.openedSeq);

  // Each diagram opens with the minimap as the settings have it, and a
  // change to the setting shows or hides it at once. Separate sources, so
  // only a real change does: another tab rewriting the settings leaves the
  // toolbar's toggle alone.
  watch([editing, openDiagram, () => editorSettings.showMinimap], ([open]) => {
    if (open) {
      showMinimap.value = editorSettings.showMinimap;
    }
  });
  const resolving = ref(false);
  const confirmingDiscard = ref(false);
  // A draft is being made or opened: the landing's buttons that would make
  // or open another wait for it (see whileBusy).
  const busy = ref(false);
  let stopWatchingSystemTheme = () => {};
  let stopFollowingShortcuts = () => {};
  let stopFollowingFullScreen = () => {};

  // The name field keeps what the user is typing: it follows the diagram
  // name only while it is not focused, and commits on change. Any re-render
  // of this view would otherwise put the stored name back mid-typing.
  const nameField = ref('');
  const nameFocused = ref(false);
  watch(
    () => store.doc.name,
    (name) => {
      if (!nameFocused.value) {
        nameField.value = name;
      }
    },
    { immediate: true },
  );

  function onNameBlur() {
    nameFocused.value = false;
    nameField.value = store.doc.name;
  }

  // Dismissing the page alert, or clearing it any other way, removes the
  // focused Dismiss button with it; focus moves on to what follows it
  // rather than falling to <body> (WCAG 2.4.3).
  watch(
    () => store.error,
    (now) => {
      if (now || !errorPanel.value?.contains(document.activeElement)) {
        return;
      }

      if (editing.value) {
        editorHeading.value?.focus();
      } else {
        drafts.value?.focusActiveTab();
      }
    },
    { flush: 'pre' },
  );

  // Publish, Import and Generate show their own failures. What they showed
  // is cleared when they close, so it does not reappear over the editor.
  const OWN_ERRORS = ['publish', 'import', 'generate'];
  let errorSeqAtOpen = 0;
  watch(dialog, (now, before) => {
    if (now) {
      errorSeqAtOpen = store.errorSeq;
    } else if (
      OWN_ERRORS.includes(before) &&
      store.errorSeq !== errorSeqAtOpen
    ) {
      store.clearError();
    }
  });

  // "2 unsaved changes are kept on this device"
  const unsavedSummary = computed(() => {
    const pending = store.saveState.pending;
    const where = store.saveState.storageFailed
      ? 'in this tab only'
      : 'on this device';

    return `${count(pending, 'unsaved change')} ${pending === 1 ? 'is' : 'are'} kept ${where}`;
  });

  // A conflict blocks every save, so it takes focus: the choices are then
  // the next Tab stops instead of a long way back up the page. Not from a
  // field being typed into, though: the keys that follow would be lost, and
  // the panel's alert announces the conflict anyway.
  watch(
    () => store.hasConflict,
    (now) => {
      if (now && !document.activeElement?.matches?.(TYPING)) {
        conflictHeading.value?.focus();
      }
    },
    { flush: 'post' },
  );

  const diagramName = computed(() => store.doc.name || 'Untitled diagram');

  // The page title names the view and, in the editor, the open diagram
  // (WCAG 2.4.2). Leaving the Builder restores the app's own title.
  const appTitle = document.title;
  watchEffect(() => {
    document.title = editing.value
      ? `${diagramName.value} – Builder Flow – ${appTitle}`
      : `Builder Flow – ${appTitle}`;
  });

  // Opening the editor replaces the landing, which held focus, so focus
  // moves to the editor's heading rather than to <body> (WCAG 2.4.3).
  watch(
    editing,
    (now) => {
      if (now) {
        editorHeading.value?.focus();
      }
    },
    { flush: 'post' },
  );

  // So does another diagram opened in the editor (Upload) when focus, or
  // the control a dialog gives it back to, went with the old canvas.
  watch(
    openDiagram,
    () => {
      const active = document.activeElement;

      if (editing.value && (!active || active === document.body)) {
        editorHeading.value?.focus();
      }
    },
    { flush: 'post' },
  );

  function viewportElement() {
    return canvas.value?.viewportElement?.() || null;
  }

  // The listing in flight, when the last one finished, and the page alert a
  // failed listing raised. The lists show "Loading…" until they are first
  // read; later reads keep them on screen, and announce nothing.
  let listing = null;
  let listedAt = 0;
  let listingErrorSeq = 0;
  const listed = ref(false);

  async function refresh() {
    const errors = store.errorSeq;
    const current = Promise.all([store.fetchDrafts(), store.fetchDocuments()]);

    listing = current;
    try {
      await current;
    } finally {
      if (listing === current) {
        listing = null;
      }
      listedAt = Date.now();
      listed.value = true;
      if (store.error && store.errorSeq !== errors) {
        listingErrorSeq = store.errorSeq;
      }
      if (relistWhenListed && !listing) {
        relistWhenListed = false;
        relist();
      }
    }
  }

  // The lists are read again whenever they come back into view, since drafts
  // are created, shared and published elsewhere meanwhile: when the landing
  // replaces the editor, and when the browser tab or window comes back to
  // the front. focus and visibilitychange often arrive together, so the read
  // waits a moment. A read asked for while another is under way waits for it
  // to end and is then made once: the one under way may have started before
  // whatever changed. None is made while a dialog is open, or while the page
  // alert shows something other than a failed listing: a listing clears the
  // alert, which the user has not dismissed yet.
  const RELIST_DELAY_MS = 250;
  let relistTimer = null;
  let relistWhenListed = false;

  function relist() {
    clearTimeout(relistTimer);
    relistTimer = setTimeout(() => {
      if (listing) {
        relistWhenListed = true;
        return;
      }
      if (
        editing.value ||
        dialog.value ||
        document.visibilityState === 'hidden' ||
        (store.error && store.errorSeq !== listingErrorSeq)
      ) {
        return;
      }

      refresh();
    }, RELIST_DELAY_MS);
  }

  // closeEditor has just read the lists when it shows the landing.
  const LISTED_FRESH_MS = 1000;

  watch(editing, (now, before) => {
    if (!now && before && Date.now() - listedAt >= LISTED_FRESH_MS) {
      relist();
    }
  });

  // Runs one of the ways to make or open a draft, unless one is under way:
  // a double click must not make two drafts.
  async function whileBusy(work) {
    if (busy.value) {
      return null;
    }

    busy.value = true;

    try {
      return await work();
    } finally {
      busy.value = false;
    }
  }

  // Blank drafts are numbered, so the landing can tell them apart.
  function untitledName() {
    return uniqueName(
      'Untitled topology',
      store.drafts.mine.map((item) => item.title || item.name || ''),
      (name) => name.trim().toLowerCase(),
      ' ',
    );
  }

  function startBlank() {
    return whileBusy(async () => {
      const title = untitledName();

      store.newDocument({ name: title });

      const created = await store.createDraft({ title });

      editing.value = Boolean(created);
    });
  }

  function openLanding(which) {
    if (busy.value) {
      return;
    }

    store.newDocument({ name: 'Untitled topology' });
    editing.value = false;
    dialog.value = which;
  }

  // A published diagram opens read only, with no draft.
  function openDraft(item) {
    return whileBusy(async () => {
      let opened = null;

      if (item.owner && item.id) {
        opened = await store.loadDraft(item.owner, item.id);
      } else if (item.id) {
        opened = await store.viewPublishedDocument(item.id);
      }

      editing.value = Boolean(opened);
    });
  }

  // Edits the published diagram shown read only, in the user's draft of it
  // or a new one. The panel and its button go, so focus moves on to the
  // editor's heading; a failure leaves both, and the page alert says why.
  function editPublished() {
    return whileBusy(async () => {
      if (await store.editPublished()) {
        await nextTick();
        editorHeading.value?.focus();
      }
    });
  }

  // BuilderDrafts has asked for confirmation already, naming the draft as
  // its card does (label); the announcement uses the same name. The store
  // refreshes the list after a delete; refreshing here too would clear the
  // error of a delete that failed.
  async function deleteDraft(item, label) {
    await store.deleteDraft(
      item.owner,
      item.id,
      item.etag,
      label || item.name || item.title || item.id,
    );
  }

  async function resolveConflict(choice) {
    if (resolving.value) {
      return;
    }

    resolving.value = true;

    try {
      await store.resolveConflict(choice);
    } finally {
      resolving.value = false;
    }

    // The banner and its buttons are gone once resolved; focus goes to the
    // editor heading instead of falling to <body>. A failed attempt keeps
    // the banner, and focus returns to its heading.
    await nextTick();
    (store.hasConflict ? conflictHeading.value : editorHeading.value)?.focus();
  }

  function discardLocal() {
    confirmingDiscard.value = false;

    return resolveConflict('reload');
  }

  // The dialog opens at once and reads the draft's snapshots itself.
  function openHistory() {
    dialog.value = 'history';
  }

  function afterImport(result = {}) {
    return whileBusy(() => importReady(result));
  }

  async function importReady(result) {
    let ready = Boolean(result.draftCreated || store.etag);

    if (!result.draftCreated && !store.etag) {
      const source = result.source;
      const sourceToken = source?.fullName
        ? source.stored
          ? source.fullName
          : `uploaded/${source.fullName}`
        : '';

      // An Import says what it imported once the draft exists (see
      // GenerateDialog); an Upload has said so already.
      ready = Boolean(
        await store.createDraft({
          document: result.document || store.doc,
          title: (result.document || store.doc).name,
          sourceToken,
          announcement: result.announcement,
        }),
      );
    }

    editing.value = ready;
  }

  function rename(name) {
    if (name === store.doc.name) {
      return;
    }

    store.setInfo({ name });
  }

  // Edits the server does not have yet: queued, or being sent.
  function unsavedWork() {
    return (
      editing.value &&
      !store.readOnly &&
      (store.saveState.pending > 0 || store.saveState.status === 'saving')
    );
  }

  // How long leaving waits for a save under way before it asks.
  const LEAVE_SAVE_WAIT_MS = 2000;

  // The resolver of the Leave question while it is asked (see mayLeave).
  const leaving = ref(null);
  let leaveCheck = null;

  const leaveMessage = computed(() => {
    const one = store.saveState.pending === 1;

    return `Your ${unsavedSummary.value}. ${
      store.saveState.storageFailed
        ? `Leaving loses ${one ? 'it' : 'them'}.`
        : `${one ? 'It is' : 'They are'} saved when you next open this draft in this browser, unless you log out first.`
    }`;
  });

  function settleLeave(leave) {
    const resolve = leaving.value;

    leaving.value = null;
    resolve?.(leave);
  }

  // A save that lands while the question is asked leaves nothing to lose:
  // leaving goes ahead, rather than asking about no changes.
  watch(
    () => Boolean(leaving.value) && !unsavedWork(),
    (saved) => {
      if (saved) {
        settleLeave(true);
      }
    },
  );

  async function askToLeave() {
    if (!unsavedWork()) {
      return true;
    }

    // A save that fails says so in the save state; leaving still asks.
    let timer;
    await Promise.race([
      store.saveNow().catch(() => null),
      new Promise((resolve) => {
        timer = setTimeout(resolve, LEAVE_SAVE_WAIT_MS);
      }),
    ]);
    clearTimeout(timer);

    if (!unsavedWork()) {
      return true;
    }

    return new Promise((resolve) => {
      leaving.value = resolve;
    });
  }

  /**
   * Whether the draft may be left: for the drafts, another page, or a new
   * draft (Upload). What is not saved yet is sent first; if the server
   * still lacks some of it, the user is asked, since it is then kept only
   * on this device. Asked twice at once, it asks once.
   *
   * @returns {Promise<boolean>}
   */
  function mayLeave() {
    if (!leaveCheck) {
      leaveCheck = askToLeave().finally(() => {
        leaveCheck = null;
      });
    }

    return leaveCheck;
  }

  // Closing or reloading the tab asks too, in the browser's own words.
  function onBeforeUnload(event) {
    if (unsavedWork()) {
      event.preventDefault();
      // Some browsers ask only when returnValue is set.
      event.returnValue = '';
    }
  }

  // So does following a link out of the Builder. A logout has ended the
  // Builder session already (see endBuilderSession) and is not held up.
  onBeforeRouteLeave(() => !usePhenixStore().auth || mayLeave());

  // Upload makes a new draft, which leaves this one.
  async function openUpload() {
    if (await mayLeave()) {
      dialog.value = 'import';
    }
  }

  // The lists are refreshed before the landing replaces the editor, so focus
  // can move from "Back to drafts" straight to the closed draft's card, or
  // the published diagram's.
  async function closeEditor() {
    if (!(await mayLeave())) {
      return;
    }

    const closed = store.published?.id || store.draftId;

    await refresh();
    editing.value = false;
    await nextTick();
    if (!(await drafts.value?.focusDraft(closed))) {
      drafts.value?.focusActiveTab();
    }
  }

  function cycleTheme() {
    store.cycleTheme(rootEl.value);
  }

  // --- header -----------------------------------------------------------------

  const saveNeedsAttention = computed(() =>
    ['conflict', 'forbidden', 'error', 'offline'].includes(
      store.saveState.status,
    ),
  );

  // Per button: the tooltip's text, the description screen readers get in
  // its place, and aria-keyshortcuts. Reset view's says what it resets, with
  // its keys if the user gave it some, and names the button too while it
  // shows only its icon (iconText); Shortcuts' and Settings' name what they
  // open and describe the buttons with their keys; Help's repeats its name;
  // Focus mode's, always an icon, names the button and says what a press
  // does.
  const RESET_TIP = 'Reset column widths, zoom, minimap and scrolling';
  const FOCUS_TIPS = {
    enter: 'Hide the navigation bar and fill the screen',
    exit: 'Show the navigation bar again',
  };

  // 'Focus mode: hide the navigation bar…'
  function named(name, text) {
    return `${name}: ${text.replace(/^./, (first) => first.toLowerCase())}`;
  }

  const headerTips = computed(() => {
    const resetKeys = shortcutLabel('view.reset');
    const reset = resetKeys ? `${RESET_TIP} (${resetKeys})` : RESET_TIP;
    const sheetKeys = shortcutLabel('shortcuts.open');
    const focus = withShortcut(
      FOCUS_TIPS[focusMode.on ? 'exit' : 'enter'],
      'view.focusMode',
    );

    return {
      reset: {
        text: reset,
        iconText: named('Reset view', reset),
        description: reset,
        aria: ariaShortcuts('view.reset'),
      },
      shortcuts: {
        text: sheetKeys
          ? `Keyboard shortcuts (${sheetKeys})`
          : 'Keyboard shortcuts',
        description: sheetKeys,
        aria: ariaShortcuts('shortcuts.open'),
      },
      settings: {
        text: withShortcut('Builder settings', 'settings.open'),
        description: shortcutLabel('settings.open'),
        aria: ariaShortcuts('settings.open'),
      },
      help: {
        text: 'Help (opens in a new tab)',
        description: '',
      },
      focus: {
        text: named(focusMode.on ? 'Exit focus mode' : 'Focus mode', focus),
        description: focus,
        aria: ariaShortcuts('view.focusMode'),
      },
    };
  });

  const { tip, tipEl, showTip, scheduleHide, hideTip } = useFixedTooltip({
    side: 'below',
  });

  // Whether a header button shows only its icon: a narrow header hides its
  // label (see the styles below).
  function iconOnly(button) {
    const label = button.querySelector('.builder-header__label');

    return Boolean(label) && getComputedStyle(label).position === 'absolute';
  }

  // Read when shown, so a tooltip follows the keys and the header's width.
  function headerTip(key) {
    const show = (event) => {
      const { text, iconText } = headerTips.value[key];

      showTip(
        event,
        iconText && iconOnly(event.currentTarget) ? iconText : text,
      );
    };

    return {
      mouseenter: show,
      mouseleave: scheduleHide,
      focus: show,
      blur: hideTip,
    };
  }

  // --- focus mode ---------------------------------------------------------------

  const focusButton = ref(null);

  // A press from the keyboard leaves Focus mode's tooltip up, and the press
  // changes what the button does, so the tooltip changes with it; only
  // while the button still has focus or the pointer, so a pending hide is
  // not cancelled.
  watch(
    () => headerTips.value.focus.text,
    (text, before) => {
      const button = focusButton.value;

      if (
        tip.value?.text === before &&
        button &&
        (button === document.activeElement || button.matches(':hover'))
      ) {
        showTip({ currentTarget: button }, text);
      }
    },
  );

  // How to leave focus mode, for its announcements.
  function focusModeExit() {
    const keys = shortcutLabel('view.focusMode', { text: true });

    return keys ? `Exit focus mode or ${keys}` : 'Exit focus mode';
  }

  /**
   * Turns focus mode (see focusMode.js) on or off, from the header's button
   * or the view.focusMode command, and says which. Focus stays where it is:
   * nothing that can hold it is hidden, and the button stays one element.
   */
  function toggleFocusMode() {
    if (focusMode.on) {
      exitFocusMode();
      store.announce('Focus mode off: the navigation bar is back.');

      return;
    }

    enterFocusMode();
    store.announce(
      `Focus mode on: the navigation bar is hidden. ${focusModeExit()} brings it back.`,
    );
  }

  // The browser's Escape, or its own controls, left full screen: focus mode
  // stays on, in the window.
  function onFullScreenLeft() {
    store.announce(
      `Full screen off. Focus mode is still on: ${focusModeExit()} leaves it.`,
    );
  }

  // Focus mode belongs to the editor: the drafts have no button to leave
  // it.
  watch(editing, (now) => {
    if (!now) {
      exitFocusMode();
    }
  });

  // What scrolls in the editor besides the Builder root: the side columns,
  // and in the stacked layout the Outline and the Inspector.
  const SCROLLED =
    '.builder-layout__side, .builder-outline, .builder-inspector';

  /**
   * Reset view: the editor's view as a new session shows it. The side
   * columns take their default widths, and the widths stored for them are
   * forgotten; the minimap shows or hides as the settings have it; the
   * panels scroll to the top; the sections that open (the canvas's Keyboard
   * help, the Inspector's More settings) close; and the canvas has the zoom
   * and pan it opens with. The diagram, its undo history and the settings
   * are left as they are. Focus stays where it is, unless a section that closes held
   * it: it moves to that section's summary rather than to <body>.
   */
  function resetView() {
    const root = rootEl.value;

    showMinimap.value = editorSettings.showMinimap;
    canvas.value?.resetViewport();
    panes.value?.resetWidths();
    closeSections();

    for (const details of root?.querySelectorAll('details[open]') || []) {
      if (details.closest('dialog')) {
        continue;
      }

      const summary = details.querySelector(':scope > summary');
      if (
        details.contains(document.activeElement) &&
        !summary?.contains(document.activeElement)
      ) {
        summary?.focus();
      }
      details.open = false;
    }

    for (const element of [root, ...(root?.querySelectorAll(SCROLLED) || [])]) {
      element?.scrollTo(0, 0);
    }

    // Scrolling back to the top can carry the focused control out of view.
    const active = document.activeElement;
    if (active && active !== root && root?.contains(active)) {
      active.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }

    store.announce(
      'View reset: default column widths, zoom, minimap and scrolling.',
    );
  }

  // --- links ------------------------------------------------------------------

  /**
   * Opens what the address names: ?draft=owner/id a draft, ?topology=name
   * the published diagram of a topology (Configs' edit links here). A role
   * that may create drafts edits that diagram, in its draft of it or a new
   * one (see openPublishedDocument); any other role views it read only.
   *
   * @param {{draft: string, topology: string}} link
   */
  async function openLinked({ draft, topology }) {
    if (draft) {
      const split = draft.lastIndexOf('/');

      if (split <= 0 || split === draft.length - 1) {
        store.setError(
          'This link does not name a draft. Open one from the lists instead.',
        );

        return;
      }

      await openDraft({
        owner: draft.slice(0, split),
        id: draft.slice(split + 1),
      });

      return;
    }

    if (!topology) {
      return;
    }

    const published = store.documents.find(
      (document) => document.target === topology,
    );

    if (!published) {
      store.setError(
        `No published Builder Flow document exists for topology ${topology}. ${
          store.canCreateDrafts
            ? 'Use Import to make a diagram from it.'
            : 'Select its name in Configs to view it.'
        }`,
      );

      return;
    }

    await whileBusy(async () => {
      const opened = store.canCreateDrafts
        ? await store.openPublishedDocument(published.id, {
            announcement: `Opened topology ${topology} in Builder Flow as a new draft.`,
            resumed: `Opened topology ${topology} in Builder Flow, in your draft of it.`,
          })
        : await store.viewPublishedDocument(published.id);

      editing.value = Boolean(opened);
    });
  }

  // Where the address names a diagram, it follows the draft that is open,
  // so a reload reopens that draft, edits and all, rather than making
  // another from the published diagram. Leaving the editor drops it;
  // a published diagram shown read only keeps the link that opened it.
  watch(
    () => {
      if (!editing.value) {
        return '';
      }

      return store.published || !store.owner || !store.draftId
        ? null
        : `${store.owner}/${store.draftId}`;
    },
    (draft) => {
      const named = route.query.draft || route.query.topology;

      if (draft === null || !named || route.query.draft === draft) {
        return;
      }

      router.replace({ query: draft ? { draft } : {} });
    },
  );

  // --- commands -------------------------------------------------------------

  function focusOutline() {
    (
      document.querySelector(
        '[data-testid="builder-outline"] button[tabindex="0"]',
      ) || document.getElementById('outline-title')
    )?.focus();
  }

  // The Inspector's heading, or with `field` its first field, which is the
  // name of the node (or the label of the connection) it shows. The form
  // is rebuilt after a selection changes, so this waits for it.
  async function focusInspector({ field = false } = {}) {
    await nextTick();
    await nextTick();

    const first =
      field &&
      document.querySelector(
        '.builder-inspector form :is(input, select, textarea):not([type="hidden"]):not(:disabled)',
      );

    (first || document.getElementById('inspector-title'))?.focus();
  }

  // Inputs whose text the form reads on change; a change sent to a select
  // or a checkbox would be read as a new choice.
  const TYPED_FIELD =
    'textarea, input:not([type="checkbox"], [type="radio"], [type="button"], [type="submit"], [type="reset"], [type="file"])';

  // Before a save: the focused field commits what it holds, as its change
  // event would (the Diagram name commits only on change), and the
  // Inspector settles its unapplied edits. Resolves to why some stay
  // unapplied, or ''.
  async function settleEdits() {
    const field = document.activeElement;

    if (
      field?.matches?.(TYPED_FIELD) &&
      !field.readOnly &&
      rootEl.value?.contains(field) &&
      !field.closest('dialog')
    ) {
      field.dispatchEvent(new Event('change', { bubbles: true }));
      await nextTick();
    }

    return (await inspector.value?.settle?.()) || '';
  }

  // What the commands (builder/commands.js) can do to this view.
  const commandView = {
    get editing() {
      return editing.value;
    },
    get dialog() {
      return dialog.value;
    },
    get draftsTab() {
      return editing.value ? '' : drafts.value?.activeTab || '';
    },
    get showMinimap() {
      return showMinimap.value;
    },
    get canZoomIn() {
      return !canvas.value?.atMaxZoom;
    },
    get canZoomOut() {
      return !canvas.value?.atMinZoom;
    },
    get focusMode() {
      return focusMode.on;
    },
    openDialog(name) {
      if (name === 'import' && editing.value) {
        return openUpload();
      }

      dialog.value = name;
    },
    openPalette({ query = '', command = '' } = {}) {
      paletteRequest.value = { query, command };
      dialog.value = 'commands';
    },
    openShortcuts() {
      dialog.value = 'shortcuts';
    },
    openSettings() {
      dialog.value = 'settings';
    },
    openHistory,
    openLanding,
    startBlank,
    openDraft,
    closeEditor,
    showDraftsTab(id) {
      drafts.value?.showTab(id);
    },
    toggleMinimap() {
      showMinimap.value = !showMinimap.value;
    },
    toggleFocusMode,
    resetView,
    setTheme(theme) {
      store.setTheme(theme, rootEl.value);
    },
    zoomIn() {
      canvas.value?.zoomIn();
    },
    zoomOut() {
      canvas.value?.zoomOut();
    },
    fitView() {
      canvas.value?.fitView();
    },
    focusCanvas() {
      document.getElementById('builder-canvas')?.focus();
    },
    focusOutline,
    focusInspector,
    showNode(id) {
      canvas.value?.showNode(id);
    },
    visibleArea() {
      return canvas.value?.visibleArea?.() || null;
    },
    revealNode(id) {
      canvas.value?.revealNode?.(id);
    },
    settleEdits,
  };

  const commandContext = createCommandContext({ store, view: commandView });

  // The toolbar and the command palette run commands in this context.
  provide('builderCommands', commandContext);

  // Every shortcut of the view, on the landing and in the editor, goes
  // through the one dispatcher; see SCOPES in commands.js for where each
  // works. Controls with keys of their own (canvas items, outline rows, the
  // toolbar's arrows, dialogs) handle those first.
  function onGlobalKeydown(event) {
    if (event.key === 'Escape' && dialog.value) {
      dialog.value = '';
    }

    dispatchKeydown(event, commandContext, { root: rootEl.value });
  }

  onMounted(async () => {
    const matchMedia =
      typeof window !== 'undefined' && window.matchMedia
        ? window.matchMedia.bind(window)
        : undefined;

    store.initTheme(rootEl.value);
    systemReducesMotion.value = prefersReducedMotion(matchMedia);
    stopWatchingSystemTheme = watchSystemTheme(matchMedia, () => {
      if (store.theme === 'system') {
        store.resolvedTheme = applyTheme(rootEl.value, store.theme, matchMedia);
      }
    });

    window.addEventListener('keydown', onGlobalKeydown);
    window.addEventListener('beforeunload', onBeforeUnload);
    stopFollowingShortcuts = followShortcutSettings(window);
    stopFollowingFullScreen = followFullScreen(onFullScreenLeft);
    window.addEventListener('focus', relist);
    document.addEventListener('visibilitychange', relist);

    const link = {
      draft: String(route.query.draft || ''),
      topology: String(route.query.topology || ''),
    };

    await Promise.all([store.fetchSchema(), refresh(), store.fetchSources()]);
    await openLinked(link);
  });

  onBeforeUnmount(() => {
    document.title = appTitle;
    window.removeEventListener('keydown', onGlobalKeydown);
    window.removeEventListener('beforeunload', onBeforeUnload);
    stopFollowingShortcuts();
    // Leaving the Builder, for another page, ends focus mode, so the app
    // shows its navigation bar again.
    stopFollowingFullScreen();
    exitFocusMode();
    window.removeEventListener('focus', relist);
    document.removeEventListener('visibilitychange', relist);
    clearTimeout(relistTimer);
    relistWhenListed = false;
    stopWatchingSystemTheme();
    store.autosave?.dispose();
    // ELK's worker stays while the Builder is open (see layouts/elk.js).
    stopLayoutEngine();
  });
</script>

<style scoped>
  /* One row, left to right: Back to drafts, the name, the counts, then the
     actions at the right edge. A narrower window wraps it. It is the
     container its buttons' labels follow (see below). */
  .builder-header {
    container: builder-header / inline-size;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem 0.75rem;
  }

  /* The heading is visually hidden, so while it has focus the header it
     titles shows the focus indicator instead (WCAG 2.4.7). */
  .builder-header:has(> h1:focus-visible) {
    outline: 3px solid var(--bx-focus);
    outline-offset: 2px;
    border-radius: var(--bx-radius);
  }

  /* 13rem wide, or down to 8rem to stay beside Back to drafts in a narrow
     window. */
  .builder-header__name {
    flex: 1 1 8rem;
    max-width: 13rem;
    margin-bottom: 0;
  }

  /* The save state and the buttons after it wrap as one group, which stays
     at the right edge, on a line of its own if need be. */
  .builder-header__actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 0.4rem;
    margin-inline-start: auto;
  }

  /* As tall as the buttons beside it, and ending next to them, so they stay
     put as it changes. Wide enough for the usual states ("Saving 2
     changes", "All changes saved"), so the header does not rewrap on every
     save. A longer message wraps. */
  .builder-header__save {
    align-self: stretch;
    justify-content: flex-end;
    min-width: 7.5rem;
    max-width: 18rem;
    margin: 0 0.35rem 0 0;
  }

  /* An icon alone is as tall as a labelled button: a line of text inside
     the same padding and border. None stretches to fill a row that a
     wrapped save state makes taller. */
  .builder-header__button {
    box-sizing: content-box;
    min-height: 1lh;
    justify-content: center;
  }

  /* Too narrow for the labels on one row, with room for longer counts and
     checks (below a 1392px window at the default text size): Reset view,
     Shortcuts, Settings and Help show their icons alone. The labels stay,
     visually hidden, as their names, and the tooltips name them. */
  @container builder-header (max-width: 85rem) {
    .builder-header__label {
      position: absolute;
      width: 1px;
      height: 1px;
      margin: -1px;
      padding: 0;
      overflow: hidden;
      clip: rect(0 0 0 0);
      white-space: nowrap;
      border: 0;
    }
  }

  .builder-error,
  .builder-conflict,
  .builder-published {
    padding: 0.6rem 0.75rem;
  }

  .builder-published {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.4rem;
  }

  .builder-published p {
    margin: 0;
    overflow-wrap: anywhere;
  }

  .builder-conflict h2 {
    font-weight: 700;
    margin: 0 0 0.25rem;
  }

  .builder-conflict__actions,
  .builder-page-error {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
  }

  .builder-page-error {
    justify-content: space-between;
  }

  .builder-page-error p {
    margin: 0;
    overflow-wrap: anywhere;
  }
</style>
