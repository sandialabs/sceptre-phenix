<!--
  Builder v2 view.

  Owns the editor shell: theme, live regions, conflict banner, the notice
  of other tabs with the draft open, dialogs and the drafts landing. The
  legacy /builder editor is untouched and still reachable from the header.
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

    <!-- Signing in again when the session ends, without leaving the page
         (see builder/signin.js). -->
    <builder-sign-in @signed-in="afterSignIn" />

    <template v-if="!editing">
      <builder-drafts
        ref="drafts"
        :mine="store.drafts.mine"
        :shared="store.drafts.shared"
        :published="store.documents"
        :others="store.drafts.others"
        :loading="store.loading && !listed"
        :can-create="store.canCreateDrafts"
        :can-delete="store.canDeleteDrafts"
        :deleting-published="store.deletingDocuments"
        :busy="busy"
        :opening="opening"
        :damaged="store.damagedDrafts"
        :saves="backgroundCards"
        @blank="startBlank"
        @import="openLanding('import')"
        @generate="openLanding('generate')"
        @open="openDraft"
        @delete="deleteDraft"
        @delete-published="store.deletePublished"
        @share="shareListed">
        <template #buttons>
          <builder-header-buttons
            view="drafts"
            :tips="headerTips"
            :tip-for="headerTip"
            :theme-button="themeButton"
            @commands="commandView.openPalette()"
            @cycle-theme="cycleTheme"
            @settings="runCommand('settings.open', commandContext)"
            @focus-mode="toggleFocusMode" />
        </template>
      </builder-drafts>
    </template>

    <template v-else>
      <div ref="editorHeader" class="builder-header">
        <!-- The name below shows the diagram name; the heading gives the
             view a title and takes focus when the editor opens. -->
        <h1 ref="editorHeading" class="builder-visually-hidden" tabindex="-1">
          {{ diagramName
          }}{{ ownerOfOthers ? ` – ${ownerOfOthers}'s draft` : '' }}
          – Builder v2
        </h1>
        <!-- Back to drafts, the name, and who shared the draft, at the
             start; the counts in the middle; the actions at the end. -->
        <div class="builder-header__start">
          <!-- While it waits for the saves, then for the lists: a turning
               ring in place of the arrow (reduced motion stops it turning)
               and what it waits for. The labels hold the button's width;
               the hidden ones are not named. -->
          <button
            type="button"
            class="builder-button"
            data-testid="editor-back"
            :aria-disabled="Boolean(closing) || undefined"
            :aria-busy="Boolean(closing) || undefined"
            @click="closeEditor">
            <span
              v-if="closing"
              class="builder-toolbar__spinner"
              aria-hidden="true"></span>
            <builder-icon v-else name="arrow-left" :size="14" />
            <span class="builder-button__swap">
              <span :class="{ 'is-off': closing }">Back to drafts</span>
              <span :class="{ 'is-off': closing !== 'saving' }">Saving…</span>
              <span :class="{ 'is-off': closing !== 'loading' }">Loading…</span>
            </span>
          </button>
          <!-- The name as text, cut off with an ellipsis when long (the
               text is still whole, and a tooltip shows it), then Edit
               diagram name, which puts a field in their place (see
               startNameEdit). A view-only user has no pencil. The field's
               label is read rather than shown. -->
          <div class="builder-field builder-header__name">
            <template v-if="editingName">
              <label for="builder-doc-name" class="builder-visually-hidden">
                Diagram name
              </label>
              <input
                id="builder-doc-name"
                ref="nameInput"
                v-model="nameField"
                :readonly="store.readOnly"
                type="text"
                class="builder-header__name-field"
                placeholder="Diagram name"
                data-testid="builder-name-field"
                @keydown.enter.prevent="finishNameEdit(true)"
                @keydown.esc.prevent="cancelNameEdit"
                @change="onNameChange"
                @blur="onNameBlur" />
            </template>
            <template v-else>
              <span
                id="builder-name-text"
                ref="nameText"
                class="builder-header__name-text"
                :class="{ 'is-empty': !store.doc.name }"
                data-testid="builder-name"
                v-on="nameTip">
                {{ diagramName }}
              </span>
              <button
                v-if="!store.readOnly"
                ref="nameButton"
                type="button"
                class="builder-button builder-header__name-edit"
                aria-label="Edit diagram name"
                aria-describedby="builder-name-text"
                data-testid="builder-name-edit"
                v-on="editNameTip"
                @click="startNameEdit">
                <builder-icon name="pencil" :size="14" />
              </button>
            </template>
          </div>

          <!-- Someone shared the draft: who, and what the user may do,
               until it is no longer shared with them. -->
          <p
            v-if="store.sharedBy"
            class="builder-header__shared"
            data-testid="editor-shared-by">
            Shared by {{ store.sharedBy }} · {{ accessLabel(store.access) }}
          </p>
        </div>

        <builder-counts />

        <!-- The Warnings button (the checks), Reset view, and the buttons the
             drafts' header has too (BuilderHeaderButtons.vue). Reset view's
             tooltip says what it resets, with the command's keys, if it has
             any, as the toolbar's do. Reset view keeps its label as long as
             Commands does, and in a narrow header shows only its icon (see
             .builder-header__label in builder.css); its label stays as its
             name. The save state is in the toolbar. -->
        <div class="builder-header__actions">
          <builder-checks />
          <button
            type="button"
            class="builder-button builder-header__button"
            data-testid="editor-reset-view"
            :aria-keyshortcuts="headerTips.reset.aria"
            aria-describedby="header-tip-reset"
            v-on="headerTip('reset')"
            @click="resetView">
            <builder-icon name="reset-view" :size="14" />
            <span class="builder-header__label">Reset view</span>
          </button>
          <builder-header-buttons
            view="editor"
            shortcuts
            :tips="headerTips"
            :tip-for="headerTip"
            :theme-button="themeButton"
            @commands="commandView.openPalette()"
            @cycle-theme="cycleTheme"
            @shortcuts="runCommand('shortcuts.open', commandContext)"
            @settings="runCommand('settings.open', commandContext)"
            @focus-mode="toggleFocusMode" />
        </div>
      </div>

      <!-- Other tabs of this browser with the draft open, and changes
           closed tabs left to it (see builder/tabs.js). Not a live region:
           its counts change with the other tabs' edits. Another tab opening
           the draft, and a choice to make, are announced instead. The
           warning sign is decorative. -->
      <div
        v-if="tabsText"
        class="builder-panel builder-notice"
        data-testid="builder-tabs">
        <p>
          <builder-icon name="warning" :size="14" />
          <span>{{ tabsText }}</span>
        </p>
        <button
          v-if="store.saveState.versions.length"
          type="button"
          class="builder-button"
          data-testid="tabs-choose"
          aria-haspopup="dialog"
          @click="dialog = 'tabs'">
          Choose which to save
        </button>
      </div>

      <!-- Not modal: the diagram stays readable (and exportable) while the
           user decides. The explanation is an alert, and focus moves to the
           heading, so the two choices are the next Tab stops. A conflict
           can be the user's choice of another tab's changes too (see
           builder/tabs.js). -->
      <section
        v-if="store.hasConflict"
        class="builder-panel builder-conflict"
        aria-labelledby="conflict-title"
        data-testid="builder-conflict">
        <h2 id="conflict-title" ref="conflictHeading" tabindex="-1">
          {{
            store.saveState.otherTab
              ? "You chose another tab's changes"
              : 'This draft changed on the server'
          }}
        </h2>
        <!-- The alert stays unchanged while the panel is open; the count
             below changes with every edit and must not be re-announced. -->
        <p role="alert">{{ conflictMessage }}</p>
        <p>
          Your {{ unsavedSummary }}.
          <template v-if="store.canCreateDrafts">
            Choose how to keep your work: edits you make now are not saved until
            you choose.
          </template>
          <template v-else>
            Your role cannot create drafts, so use Export to keep a copy of your
            work before you load the server version.
          </template>
        </p>
        <div class="builder-conflict__actions">
          <!-- Saving a new draft needs a role that may create one; without
               it, Export keeps a copy. -->
          <button
            v-if="store.canCreateDrafts"
            type="button"
            class="builder-button builder-button--primary"
            data-testid="conflict-fork"
            :aria-disabled="resolving || undefined"
            @click="resolveConflict('fork')">
            Save my history as a new draft
          </button>
          <button
            v-else
            type="button"
            class="builder-button builder-button--primary"
            data-testid="conflict-export"
            aria-haspopup="dialog"
            @click="openDialog('export')">
            Export
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

      <!-- A draft someone shared that the user may no longer change (see
           accessLost in autosave.js): nothing more can be saved to it, so,
           as for a conflict, focus moves to the heading and the choices
           follow. -->
      <section
        v-else-if="store.accessLost"
        class="builder-panel builder-conflict"
        aria-labelledby="access-lost-title"
        data-testid="builder-access-lost">
        <h2 id="access-lost-title" ref="accessLostHeading" tabindex="-1">
          Your access to this draft changed
        </h2>
        <p role="alert">{{ accessLostMessage }}</p>
        <p>
          <template v-if="store.saveState.pending">
            Your {{ unsavedSummary }}.
          </template>
          {{
            store.canCreateDrafts
              ? 'To keep your work, save your history as a new draft of your own, or use Export to keep a copy.'
              : 'To keep your work, use Export to keep a copy.'
          }}
        </p>
        <div class="builder-conflict__actions">
          <button
            v-if="store.canCreateDrafts"
            type="button"
            class="builder-button builder-button--primary"
            data-testid="access-lost-fork"
            :aria-disabled="resolving || undefined"
            @click="resolveConflict('fork')">
            Save my history as a new draft
          </button>
          <button
            type="button"
            class="builder-button"
            data-testid="access-lost-export"
            aria-haspopup="dialog"
            @click="openDialog('export')">
            Export
          </button>
        </div>
      </section>

      <builder-confirm
        v-if="confirmingDiscard"
        id="conflict-discard"
        title="Discard your unsaved changes?"
        :message="`Your ${unsavedSummary}. Discarding deletes that work and loads the server version. This cannot be undone. To keep your work, cancel and ${store.canCreateDrafts ? 'choose Save my history as a new draft' : 'use Export'}.`"
        confirm-label="Discard and load the server version"
        @cancel="confirmingDiscard = false"
        @confirm="discardLocal" />

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
        v-else-if="store.readOnly && !store.accessLost"
        class="builder-panel builder-error"
        role="status"
        data-testid="builder-readonly">
        {{ readOnlyMessage }}
      </div>

      <builder-toolbar
        :minimap="showMinimap"
        @publish="openDialog('publish')"
        @share="dialog = 'share'"
        @export="openDialog('export')"
        @import="openUpload"
        @scenario="dialog = 'scenario'"
        @history="openHistory"
        @toggle-minimap="showMinimap = !showMinimap" />

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
          <builder-inspector ref="inspector" @scenario="dialog = 'scenario'" />
        </template>
      </builder-panes>
    </template>

    <!-- The headers' tooltips, in the editor and on the drafts. Their text
         reaches screen readers as the buttons' names and descriptions; the
         tooltips themselves are aria-hidden. -->
    <span
      v-for="(entry, key) in headerTips"
      :id="`header-tip-${key}`"
      :key="key"
      hidden>
      {{ entry.description }}
    </span>
    <builder-fixed-tooltip :tooltip="tooltip" testid="header-tooltip" />

    <publish-dialog
      v-if="dialog === 'publish'"
      :unapplied="unappliedBefore"
      @close="dialog = ''" />
    <!-- Share, for the open draft (the toolbar and the palette) or a draft
         of mine on the landing. -->
    <share-dialog
      v-if="dialog === 'share' && shareTarget"
      :key="`${shareTarget.owner}/${shareTarget.id}`"
      :target="shareTarget"
      @close="dialog = ''" />
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
      :unapplied="unappliedBefore"
      @close="dialog = ''" />
    <scenario-dialog v-if="dialog === 'scenario'" @close="dialog = ''" />

    <history-dialog v-if="dialog === 'history'" @close="dialog = ''" />

    <!-- Which tab's changes to save, when more than one holds some (see
         builder/tabs.js). -->
    <builder-tabs-dialog
      v-if="dialog === 'tabs'"
      :rows="tabRows"
      :can-create="store.canCreateDrafts"
      :busy="choosing"
      :export-version="exportVersion"
      @choose="chooseVersion"
      @close="dialog = ''" />

    <!-- Leaving a draft whose edits the server does not have yet, or the
         Inspector cannot apply, asks first (see leave.js); so does leaving
         the Builder while drafts closed before are still being saved. -->
    <builder-confirm
      v-if="leaving"
      id="leave-unsaved"
      title="Leave with unsaved changes?"
      :message="leaveMessage"
      :confirm-label="unsaveable ? 'Leave without them' : 'Leave anyway'"
      cancel-label="Stay"
      @cancel="settleLeave(false)"
      @confirm="settleLeave(true)" />

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
         the headers' theme button's, set without an announcement. -->
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
  } from 'vue';
  import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';

  import '@/builder/builder.css';

  import BuilderCanvas from '@/components/builder/BuilderCanvas.vue';
  import BuilderChecks from '@/components/builder/BuilderChecks.vue';
  import BuilderCommandPalette from '@/components/builder/BuilderCommandPalette.vue';
  import BuilderConfirm from '@/components/builder/BuilderConfirm.vue';
  import BuilderCounts from '@/components/builder/BuilderCounts.vue';
  import BuilderDrafts, {
    cardKey,
  } from '@/components/builder/BuilderDrafts.vue';
  import BuilderFixedTooltip from '@/components/builder/BuilderFixedTooltip.vue';
  import BuilderHeaderButtons from '@/components/builder/BuilderHeaderButtons.vue';
  import BuilderIcon from '@/components/builder/BuilderIcon.vue';
  import BuilderInspector from '@/components/builder/BuilderInspector.vue';
  import BuilderLiveRegion from '@/components/builder/BuilderLiveRegion.vue';
  import BuilderOutline from '@/components/builder/BuilderOutline.vue';
  import BuilderPalette from '@/components/builder/BuilderPalette.vue';
  import BuilderPanes from '@/components/builder/BuilderPanes.vue';
  import BuilderSettings from '@/components/builder/BuilderSettings.vue';
  import BuilderShortcuts from '@/components/builder/BuilderShortcuts.vue';
  import BuilderSignIn from '@/components/builder/BuilderSignIn.vue';
  import BuilderTabsDialog from '@/components/builder/BuilderTabsDialog.vue';
  import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
  import ExportDialog from '@/components/builder/dialogs/ExportDialog.vue';
  import GenerateDialog from '@/components/builder/dialogs/GenerateDialog.vue';
  import HistoryDialog from '@/components/builder/dialogs/HistoryDialog.vue';
  import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';
  import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';
  import ScenarioDialog from '@/components/builder/dialogs/ScenarioDialog.vue';
  import ShareDialog from '@/components/builder/dialogs/ShareDialog.vue';
  import { useFixedTooltip } from '@/components/builder/fixedTooltip.js';
  import { closeSections } from '@/components/builder/inspector/InspectorSectionRenderer.vue';

  import {
    applyTheme,
    nextTheme,
    pageMatchMedia,
    prefersReducedMotion,
    resolveTheme,
    watchSystemTheme,
  } from '@/builder/theme.js';
  import { count } from '@/builder/announce.js';
  import { builderApi } from '@/builder/api.js';
  import {
    TYPING,
    ariaShortcuts,
    createCommandContext,
    dispatchKeydown,
    focusLost,
    isTextEntry,
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
  import { formatTimestamp } from '@/builder/format.js';
  import { followHeaderLabels } from '@/builder/headerLabels.js';
  import { createDraftStore } from '@/builder/idb.js';
  import { uniqueName } from '@/builder/ids.js';
  import { followShortcutSettings } from '@/builder/keymap.js';
  import {
    createBackgroundSaves,
    createLeaveGuard,
    queueBusy,
    unappliedText,
  } from '@/builder/leave.js';
  import { stopLayoutEngine } from '@/builder/layouts/index.js';
  // Not builderSettings: <builder-settings> would name it as well as the
  // dialog.
  import { builderSettings as editorSettings } from '@/builder/settings.js';
  import {
    accessLabel,
    conflictMessage as describeConflict,
    shareLink,
  } from '@/builder/share.js';
  import {
    diagramFile,
    queuedDiagram,
    registerOpenDraft,
  } from '@/builder/session.js';
  import { signIn } from '@/builder/signin.js';
  import { forkTitle, useBuilderStore } from '@/builder/store.js';
  import {
    applyChoice,
    choiceRows,
    forkClosedQueue,
    needsChoice,
    tabsNotice,
  } from '@/builder/tabs.js';
  import { pageTitle } from '@/router';
  import { tokenExpired, usePhenixStore } from '@/store.js';

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
  const accessLostHeading = ref(null);
  const errorPanel = ref(null);
  const editing = ref(false);
  // The open dialog: publish, share, import, generate, export, scenario,
  // history, commands (the command palette), shortcuts (the shortcut sheet)
  // or settings.
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
  // The listed draft or diagram being opened (see cardKey), whose Open says
  // so, and what Back to drafts is waiting for: 'saving' or 'loading'.
  const opening = ref('');
  const closing = ref('');
  let stopWatchingSystemTheme = () => {};
  let stopFollowingShortcuts = () => {};
  let stopFollowingFullScreen = () => {};

  // The header's buttons show the labels that leave its counts centered
  // (see builder/headerLabels.js), while the editor is open.
  const editorHeader = ref(null);
  let stopFittingLabels = () => {};

  watch(
    editorHeader,
    (header) => {
      stopFittingLabels();
      stopFittingLabels = header
        ? followHeaderLabels(header, rootEl.value)
        : () => {};
    },
    { flush: 'post' },
  );

  // The header shows the diagram name as text. Edit diagram name puts a
  // field in its place, holding the name selected, which keeps what the
  // user types however the diagram changes meanwhile. Enter, or leaving the
  // field, renames the diagram (see rename); Escape keeps the name. Enter
  // and Escape give focus back to the pencil and say what happened; leaving
  // the field leaves focus where it went, as the outline's rename does.
  const editingName = ref(false);
  const nameField = ref('');
  const nameText = ref(null);
  const nameInput = ref(null);
  const nameButton = ref(null);

  async function startNameEdit() {
    if (store.readOnly) {
      return;
    }

    hideTip();
    nameField.value = store.doc.name || '';
    editingName.value = true;
    await nextTick();
    nameInput.value?.focus();
    nameInput.value?.select();
  }

  async function endNameEdit(refocus) {
    editingName.value = false;

    if (refocus) {
      await nextTick();
      nameButton.value?.focus();
    }
  }

  // `quiet` says nothing of a name left as it was.
  function finishNameEdit(refocus, { quiet = false } = {}) {
    if (!editingName.value) {
      return;
    }

    const name = nameField.value;

    endNameEdit(refocus);

    // A rename says so itself (see commit in the store).
    if (!rename(name) && refocus && !quiet) {
      store.announce('Diagram name not changed.');
    }
  }

  function cancelNameEdit() {
    if (!editingName.value) {
      return;
    }

    endNameEdit(true);
    store.announce('Diagram name not changed.');
  }

  // Save now commits the focused field by sending it a change (see
  // commitFocusedField), which renames as Enter does, without a word when
  // the name is unchanged: the save says what happened. The change the
  // browser sends as focus leaves, with focus already gone, renames as
  // leaving does.
  function onNameChange(event) {
    finishNameEdit(document.activeElement === event.target, { quiet: true });
  }

  // Focus that goes to a dialog (the command palette), or to another
  // window, comes back to the field, which stays open for it.
  function onNameBlur(event) {
    if (event.relatedTarget?.closest?.('dialog') || !document.hasFocus()) {
      return;
    }

    finishNameEdit(false);
  }

  // Another diagram, or the drafts, take the field away unsaved.
  watch([editing, () => store.openedSeq], () => {
    editingName.value = false;
  });

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

  // Access lost takes focus as a conflict does, and for the same reason.
  watch(
    () => store.accessLost,
    (now) => {
      if (now && !document.activeElement?.matches?.(TYPING)) {
        accessLostHeading.value?.focus();
      }
    },
    { flush: 'post' },
  );

  // Who saved the version the conflict is with, as the server said.
  // Without sign-in everyone is the same user, so no one is named.
  const signedIn =
    Boolean(import.meta.env.VITE_AUTH) &&
    import.meta.env.VITE_AUTH !== 'disabled';
  const conflictMessage = computed(() =>
    store.saveState.otherTab
      ? 'You chose to save the changes made in another tab, so the changes here cannot be saved to this draft.'
      : describeConflict(
          signedIn ? store.saveState.lastModifiedBy : '',
          usePhenixStore().username,
        ),
  );

  // Why a draft someone shared can no longer be saved (see accessLost in
  // autosave.js).
  const accessLostMessage = computed(() => {
    switch (store.accessLost) {
      case 'view-only':
        return `You can no longer edit this draft. ${store.owner} changed your access to view only.`;
      case 'role':
        return 'You can no longer edit this draft. Your role cannot change configs.';
      default:
        return 'This draft is no longer shared with you, or it was deleted.';
    }
  });

  // Why the open draft is read only: shared to view, shared to edit with a
  // role that may not change configs, or the role alone.
  const readOnlyMessage = computed(() => {
    if (store.sharedBy && store.access === 'view') {
      return `${store.sharedBy} shared this draft with you to view. Use Export to keep a copy.`;
    }

    if (store.sharedBy && store.access === 'edit') {
      return 'You were given edit access, but your role cannot change configs, so this draft opens view only.';
    }

    return 'You can view this draft but not change it. Use Export to keep a copy.';
  });

  const diagramName = computed(() => store.doc.name || 'Untitled diagram');

  // The owner of the open draft when it is not the user's, for the heading.
  const ownerOfOthers = computed(() =>
    store.isOwner ? '' : store.owner || '',
  );

  // The draft of mine the landing's Share was pressed for, with its name
  // as its card gives it.
  const sharedCard = ref(null);

  // The draft Share is for: in the editor the open one, which only its
  // owner may share; on the landing the card's.
  const shareTarget = computed(() => {
    const draft = editing.value
      ? store.canShare && store.draftId
        ? {
            owner: store.owner,
            id: store.draftId,
            name: diagramName.value,
            shares: store.shares,
          }
        : null
      : sharedCard.value;

    return draft
      ? { ...draft, link: shareLink(router, draft.owner, draft.id) }
      : null;
  });

  function shareListed(item, label) {
    sharedCard.value = {
      owner: item.owner,
      id: item.id,
      name: label || item.title || item.name || item.id,
      shares: item.shares || [],
    };
    dialog.value = 'share';
  }

  // The page title names the view and, in the editor, the open diagram
  // (WCAG 2.4.2). Every navigation sets the route's own title (router.js),
  // this route's too when the draft in its query changes, so the title is
  // set again after each, while the route is still the Builder's.
  const viewRoute = route.name;
  const viewTitle = computed(() =>
    editing.value
      ? pageTitle(diagramName.value, route.meta.title)
      : pageTitle(route.meta.title),
  );
  watch(
    [viewTitle, () => route.fullPath],
    () => {
      if (route.name === viewRoute) {
        document.title = viewTitle.value;
      }
    },
    { immediate: true },
  );

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
      if (editing.value && focusLost()) {
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

  // closeEditor has just read the lists when it shows the landing, or is
  // reading them.
  const LISTED_FRESH_MS = 1000;

  watch(editing, (now, before) => {
    if (
      !now &&
      before &&
      !listing &&
      Date.now() - listedAt >= LISTED_FRESH_MS
    ) {
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

  /**
   * Resolves once the browser has painted what changed so far, so a busy
   * button shows its spinner before a large diagram's long first render; at
   * once in a hidden tab, which paints nothing.
   *
   * @returns {Promise<void>}
   */
  function afterPaint() {
    if (document.visibilityState === 'hidden') {
      return Promise.resolve();
    }

    return new Promise((resolve) => {
      requestAnimationFrame(() => setTimeout(resolve, 0));
    });
  }

  // How long a wait lasts before the live region says what it is for. A
  // shorter one says nothing: its button has shown it, and what it ends
  // with is then heard at once rather than after the wait's message.
  const WAIT_ANNOUNCE_MS = 1000;

  /**
   * Says what a wait is for once it has lasted WAIT_ANNOUNCE_MS.
   *
   * @param {string} message
   * @returns {() => void} ends the wait, saying nothing if it was short
   */
  function announceWait(message) {
    const timer = setTimeout(
      () => store.announce(message, { slot: 'busy' }),
      WAIT_ANNOUNCE_MS,
    );

    return () => clearTimeout(timer);
  }

  // Opens a listed draft or diagram with `open`, while its Open says so, as
  // the live region does if it takes a while. The editor's heading takes
  // focus once it shows (see the watch on editing); a failure leaves the
  // landing, and focus, as they were, and the page alert says why.
  function openListed(item, name, open) {
    return whileBusy(async () => {
      opening.value = cardKey(item);
      const waited = announceWait(`Opening ${name}…`);

      try {
        const opened = await open();

        if (opened) {
          await afterPaint();
        }

        editing.value = Boolean(opened);
      } finally {
        waited();
        opening.value = '';
      }
    });
  }

  // A published diagram opens read only, with no draft. The landing names
  // the item as its card does (label).
  function openDraft(item, label = '') {
    const name = label || item.name || item.title || item.target || 'the draft';

    return openListed(item, name, async () => {
      if (item.owner && item.id) {
        // Its saves in the background end, and the draft's own queue takes
        // over what is left (see createBackgroundSaves).
        await background.release(item.owner, item.id);

        return store.loadDraft(item.owner, item.id);
      }

      return item.id ? store.viewPublishedDocument(item.id) : null;
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
    await background.release(item.owner, item.id);
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

    // The panel and its buttons are gone once resolved; focus goes to the
    // editor heading instead of falling to <body>. A failed attempt keeps
    // the panel, and focus returns to its heading.
    await nextTick();
    (store.hasConflict
      ? conflictHeading.value
      : store.accessLost
        ? accessLostHeading.value
        : editorHeading.value
    )?.focus();
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

  // Returns whether the name changed.
  function rename(name) {
    if (name === (store.doc.name || '')) {
      return false;
    }

    store.setInfo({ name });

    return true;
  }

  // The saves of drafts closed for the drafts, which go on in the
  // background, and what each card says of them (see
  // createBackgroundSaves). Once a draft's are done, the lists are read
  // again, for the time it changed.
  const backgroundCards = ref({});
  const background = createBackgroundSaves({
    onChange(cards) {
      backgroundCards.value = cards;
    },
    announce: (message) => store.announce(message),
    saved: () => relist(),
  });

  // The changes still being sent in the background, and of how many drafts.
  const backgroundPending = computed(() => {
    const sending = Object.values(backgroundCards.value).filter(
      (card) => card.pending > 0,
    );

    return {
      changes: sending.reduce((sum, card) => sum + card.pending, 0),
      drafts: sending.length,
    };
  });

  // The resolver of the Leave question while it is asked, the Inspector's
  // edits it names, which cannot be applied, and what is left (see ask in
  // leave.js): '' the open draft, 'close' the open draft for the drafts,
  // 'all' the Builder, with the saves of drafts closed before.
  const leaving = ref(null);
  const unsaveable = ref(null);
  const leavingScope = ref('');

  // Leaving saves what the Inspector holds unapplied and waits for the save
  // (see leave.js); the question is asked only when some of it is not
  // saved yet. Back to drafts does not wait: the save goes on in the
  // background.
  const leaveGuard = createLeaveGuard({
    store,
    editing: () => editing.value,
    saveUnapplied,
    background,
    ask(unapplied, scope = '') {
      unsaveable.value = unapplied;
      leavingScope.value = scope;

      return new Promise((resolve) => {
        leaving.value = resolve;
      });
    },
    // Signing in again holds the page (see builder/signin.js).
    sessionOver() {
      const phenix = usePhenixStore();

      return !phenix.auth || tokenExpired(phenix.token) || signIn.open;
    },
  });
  const { unsavedWork, mayLeave, mayClose } = leaveGuard;

  // Logging out saves and counts the same work (see session.js).
  const unregisterOpenDraft = registerOpenDraft({
    store,
    editing: () => editing.value,
    saveUnapplied,
    describe: unappliedText,
  });

  // Back to drafts leaves the queued changes to be sent in the background,
  // so its question names only the Inspector's edits.
  const leaveMessage = computed(() => {
    const one = store.saveState.pending === 1;
    const queued =
      leavingScope.value !== 'close' && unsavedWork()
        ? `Your ${unsavedSummary.value}. ${
            store.saveState.storageFailed
              ? `Leaving loses ${one ? 'it' : 'them'}.`
              : `${one ? 'It is' : 'They are'} saved when you next open this draft in this browser, unless you log out first.`
          }`
        : '';
    const { changes, drafts: closed } =
      leavingScope.value === 'all'
        ? backgroundPending.value
        : { changes: 0, drafts: 0 };
    const behind = changes
      ? `Your ${count(changes, 'unsaved change')} to ${closed === 1 ? 'a draft you closed' : 'drafts you closed'} ${changes === 1 ? 'is' : 'are'} kept on this device. ${changes === 1 ? 'It is' : 'They are'} saved when you next open ${closed === 1 ? 'that draft' : 'those drafts'} in this browser, unless you log out first.`
      : '';

    return [unsaveable.value && unappliedText(unsaveable.value), queued, behind]
      .filter(Boolean)
      .join(' ');
  });

  function settleLeave(leave) {
    const resolve = leaving.value;

    leaving.value = null;
    unsaveable.value = null;
    leavingScope.value = '';
    resolve?.(leave);
  }

  // A save that lands while the question is asked leaves nothing to lose:
  // leaving goes ahead, rather than asking about no changes. Edits the
  // Inspector cannot apply stay unsaved.
  watch(
    () =>
      Boolean(leaving.value) &&
      !unsaveable.value &&
      !unsavedWork() &&
      !(leavingScope.value === 'all' && backgroundPending.value.changes > 0),
    (saved) => {
      if (saved) {
        settleLeave(true);
      }
    },
  );

  // Closing or reloading the tab asks too, in the browser's own words.
  function onBeforeUnload(event) {
    leaveGuard.beforeUnload(event);
  }

  // So does following a link out of the Builder. A logout has ended the
  // Builder session already (see endBuilderSession) and is not held up;
  // with an expired token, the router shows the logout's warning instead.
  onBeforeRouteLeave(() => leaveGuard.mayFollowLink());

  // Upload makes a new draft, which leaves this one.
  async function openUpload() {
    if (await mayLeave()) {
      dialog.value = 'import';
    }
  }

  // Publish and Export read the whole diagram, so what the Inspector holds
  // unapplied is saved first; edits it cannot apply, the dialog names in
  // place of leaving them out.
  const READS_DIAGRAM = ['publish', 'export'];
  const unappliedBefore = ref(null);

  function openDialog(name) {
    if (name === 'import' && editing.value) {
      return openUpload();
    }

    if (READS_DIAGRAM.includes(name)) {
      unappliedBefore.value = editing.value ? saveUnapplied() : null;
    }

    dialog.value = name;
  }

  // Whether the lists hold a draft or published diagram.
  function inLists(id) {
    return [store.drafts.mine, store.drafts.shared, store.documents].some(
      (items) => (items || []).some((item) => item.id === id),
    );
  }

  // Back to drafts. What the Inspector holds unapplied is saved, and what
  // is not sent yet goes on being sent in the background (see mayClose and
  // sendInBackground), the draft's card saying how it goes. Only when this
  // device cannot keep it is it sent first, which the button says, as the
  // live region does if it takes a while. The landing then replaces the
  // editor, and focus moves from the button straight to the closed draft's
  // card, or the published diagram's: at once when the lists hold it,
  // while they are read again behind the landing; otherwise once they are
  // read, which the button says too. Staying gives the button back. Asked
  // twice at once, it closes once.
  let closeRun = null;

  function closeEditor() {
    closeRun ||= leaveEditor().finally(() => {
      closeRun = null;
    });

    return closeRun;
  }

  async function leaveEditor() {
    let waited = () => {};
    const saving = () => {
      closing.value = 'saving';
      waited = announceWait('Saving your changes…');
    };

    try {
      if (!(await mayClose({ saving }))) {
        return;
      }

      const closed = store.published?.id || store.draftId;

      waited();
      sendInBackground();
      if (inLists(closed)) {
        refresh();
      } else {
        closing.value = 'loading';
        waited = announceWait('Loading your drafts…');
        await refresh();
      }

      editing.value = false;
      await nextTick();
      if (!(await drafts.value?.focusDraft(closed))) {
        drafts.value?.focusActiveTab();
      }
    } finally {
      waited();
      closing.value = '';
    }
  }

  // The closed draft's queue goes on sending what is left, in the
  // background (see createBackgroundSaves). One with nothing left stops,
  // and the other tabs hear that the draft is no longer open here.
  function sendInBackground() {
    const queue = store.autosave;

    if (!queue) {
      return;
    }

    store.autosave = null;

    if (queueBusy(queue)) {
      background.take(queue, {
        owner: store.owner,
        id: store.draftId,
        name: diagramName.value,
      });
    } else {
      queue.dispose();
    }
  }

  function cycleTheme() {
    store.cycleTheme(rootEl.value);
  }

  // Signed in again: the lists refused meanwhile are read again, and focus
  // that went with the dialog, or with what opened it, goes to the view's
  // heading or tabs rather than to <body>.
  async function afterSignIn() {
    if (!editing.value) {
      refresh();
    }

    await nextTick();

    if (focusLost()) {
      if (editing.value) {
        editorHeading.value?.focus();
      } else {
        drafts.value?.focusActiveTab();
      }
    }
  }

  // The theme button of both headers shows the theme in use; its name and
  // tooltip say what a press changes it to (see nextTheme).
  const THEMES = {
    system: { icon: 'system', label: 'System' },
    light: { icon: 'sun', label: 'Light' },
    dark: { icon: 'moon', label: 'Dark' },
  };

  const matchMedia = pageMatchMedia();

  // What System shows, which decides the theme a press moves to. It follows
  // the system's changes while any theme is chosen (see onMounted).
  const systemTheme = ref(resolveTheme('system', matchMedia));

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

  // --- other tabs --------------------------------------------------------------

  // Other tabs of this browser with the draft open, and the changes closed
  // tabs left to it (see builder/tabs.js).
  const tabsText = computed(() =>
    editing.value ? tabsNotice(store.saveState) : '',
  );
  const tabRows = computed(() =>
    choiceRows(store.saveState, (value) =>
      formatTimestamp(value, { seconds: true }),
    ),
  );
  // The choice is being carried out.
  const choosing = ref(false);

  // Another tab opening the draft, or the draft opening while another tab
  // has it, is said once.
  watch(
    () =>
      editing.value && (store.saveState.tabs || []).some((peer) => peer.open),
    (now) => {
      if (now) {
        store.announce('This draft is also open in another tab.');
      }
    },
  );

  // A send waits for the user's choice, or closed tabs left more than one
  // version: the choice opens, unless the user is typing or in another
  // dialog. The notice's button opens it then, and the live region says so.
  watch(
    () => editing.value && needsChoice(store.saveState),
    (now) => {
      if (!now || dialog.value === 'tabs') {
        return;
      }

      if (
        !dialog.value &&
        !leaving.value &&
        !confirmingDiscard.value &&
        !document.activeElement?.matches?.(TYPING)
      ) {
        dialog.value = 'tabs';
      } else {
        store.announce(
          'This draft has unsaved changes in another tab. Choose which to save.',
        );
      }
    },
    { flush: 'post' },
  );

  // Nothing left to choose between: the other tabs sent their changes, or
  // kept them. The choice closes. When another tab's were chosen, the
  // conflict panel's alert says so instead.
  watch(
    () =>
      dialog.value === 'tabs' &&
      !choosing.value &&
      (store.saveState.versions || []).length === 0,
    (gone) => {
      if (!gone) {
        return;
      }

      dialog.value = '';

      if (!store.saveState.otherTab) {
        store.announce(
          'Nothing is left to choose: the other changes were saved or kept.',
        );
      }
    },
  );

  // The choice closed, and the control focus goes back to went with it (the
  // notice's button goes with the versions): focus moves to the editor's
  // heading rather than falling to <body>.
  watch(dialog, async (now, before) => {
    if (before !== 'tabs' || now) {
      return;
    }

    await nextTick();

    if (editing.value && focusLost()) {
      editorHeading.value?.focus();
    }
  });

  // The user chose another tab's changes: this tab's are saved as a new
  // draft, as the conflict panel's Save my history as a new draft does. A
  // role that cannot make drafts keeps the panel, which offers Export.
  watch(
    () =>
      editing.value && store.hasConflict && Boolean(store.saveState.otherTab),
    (now) => {
      if (now && !choosing.value && store.canCreateDrafts) {
        resolveConflict('fork');
      }
    },
  );

  // Saves the version the user chose; the others are kept as new drafts, or
  // deleted from this browser for a role that cannot make drafts, which the
  // dialog says first (see applyChoice). The dialog stays open, busy, until
  // it is done. Focus then goes back where it was, or to the editor's
  // heading when that is gone (see the watch on the dialog).
  async function chooseVersion(id) {
    if (choosing.value || !store.autosave) {
      return;
    }

    const { owner, draftId } = store;
    const local = createDraftStore();
    const actor = usePhenixStore().username || 'anonymous';
    const creates = store.canCreateDrafts;
    let outcome = null;

    choosing.value = true;

    try {
      outcome = await applyChoice({
        choice: id,
        rows: tabRows.value,
        queue: store.autosave,
        keepMine: async () =>
          Boolean(await store.resolveConflict(creates ? 'fork' : 'reload')),
        keepClosed: (version) =>
          creates
            ? forkClosedQueue({
                api: builderApi,
                store: local,
                actor,
                key: version.key,
                title: forkTitle,
              })
            : local.remove(version.key),
        reopen: () => store.loadDraft(owner, draftId),
      });
    } catch (error) {
      store.setError(store.describeError(error, 'save the changes you chose'));
    } finally {
      choosing.value = false;
    }

    // The tab chosen has gone: its changes are listed as a closed tab's,
    // and the choice stays open.
    if (outcome?.gone) {
      store.announce('That tab has closed. Choose again.');

      return;
    }

    dialog.value = '';

    // A failure the page alert has said already leaves no outcome.
    if (outcome?.failed.length > 0) {
      store.setError(
        'Could not save the changes a closed tab left as a new draft. They are kept in this browser.',
      );
    } else if (creates && outcome?.kept.length > 0) {
      store.announce(
        outcome.kept.length === 1
          ? 'Saved the changes a closed tab left as a new draft.'
          : `Saved the changes ${outcome.kept.length} closed tabs left as new drafts.`,
      );
    }
  }

  // The file Export saves for a version this tab holds: the diagram as it
  // is shown, or as a closed tab's changes leave it.
  async function exportVersion(row) {
    if (row.where === 'this') {
      return diagramFile(store.doc);
    }

    const record = await createDraftStore()
      .get(row.key)
      .catch(() => null);
    const doc = record ? queuedDiagram(record) : null;

    return doc ? diagramFile(doc) : null;
  }

  // --- header -----------------------------------------------------------------

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
    // The theme button's keys are those of the palette's command for the
    // theme it moves to.
    const { label, tip: themeTip, next } = themeButton.value;
    const themeCommand = `view.theme.${next}`;

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
      // Commands' names the palette; the theme's says what a press does,
      // and names the button too while it shows only its icon.
      commands: {
        text: withShortcut('Command palette', 'palette.open'),
        description: shortcutLabel('palette.open'),
        aria: ariaShortcuts('palette.open'),
      },
      theme: {
        text: withShortcut(themeTip, themeCommand),
        iconText: withShortcut(`Theme: ${label}. ${themeTip}`, themeCommand),
        description: shortcutLabel(themeCommand),
        aria: ariaShortcuts(themeCommand),
      },
    };
  });

  const tooltip = useFixedTooltip({ side: 'below' });
  const { tip, showTip, scheduleHide, hideTip, tipEvents } = tooltip;

  // Whether the diagram name is cut off with an ellipsis.
  function nameCutOff() {
    const text = nameText.value;

    return Boolean(text) && text.scrollWidth > text.clientWidth;
  }

  // The name's tooltip gives it whole, while it is cut off. The pencil's
  // says what it does, with the name while it is cut off, so keyboard users
  // see it too.
  const nameTip = tipEvents(() => nameCutOff() && diagramName.value);

  const editNameTip = tipEvents(() =>
    nameCutOff()
      ? `Edit diagram name: ${diagramName.value}`
      : 'Edit diagram name',
  );

  // Whether a header button shows only its icon: a narrow header hides its
  // label (see .builder-header__label in builder.css).
  function iconOnly(button) {
    const label = button.querySelector('.builder-header__label');

    return Boolean(label) && getComputedStyle(label).position === 'absolute';
  }

  function headerText(key, button) {
    const { text, iconText } = headerTips.value[key];

    return iconText && iconOnly(button) ? iconText : text;
  }

  // The header button whose tooltip is shown, and the text it showed.
  let shown = null;

  // Read when shown, so a tooltip follows the keys and the header's width.
  // The pointer leaving a button hides only that button's tooltip: when
  // focus mode's full screen moves the header's buttons, one can leave the
  // pointer while another, which has focus, shows its tooltip.
  function headerTip(key) {
    return tipEvents((button) => headerText(key, button), {
      onShow: (event, text) => {
        shown = { key, target: event.currentTarget, text };
      },
      onLeave: (event) => {
        if (event.currentTarget === shown?.target) {
          scheduleHide();
        }
      },
    });
  }

  // A press from the keyboard leaves a tooltip up, and a press of the theme
  // or Focus mode changes what the button does next, so the tooltip changes
  // with it; only while it is still that button's, and the button still has
  // focus or the pointer, so a pending hide is not cancelled.
  watch(headerTips, () => {
    const target = shown?.target;

    if (
      !target?.isConnected ||
      tip.value?.text !== shown.text ||
      (target !== document.activeElement && !target.matches(':hover'))
    ) {
      return;
    }

    const text = headerText(shown.key, target);
    if (text !== shown.text) {
      shown.text = text;
      showTip({ currentTarget: target }, text);
    }
  });

  // --- focus mode ---------------------------------------------------------------

  // How to leave focus mode, for its announcements.
  function focusModeExit() {
    const keys = shortcutLabel('view.focusMode', { text: true });

    return keys ? `Exit focus mode or ${keys}` : 'Exit focus mode';
  }

  /**
   * Turns focus mode (see focusMode.js) on or off, from the headers' button
   * or the view.focusMode command, and says which. Focus stays where it is:
   * nothing that can hold it is hidden, and the button stays one element.
   * It stays on as the editor and the drafts replace each other, until the
   * user turns it off or leaves the Builder (see onBeforeUnmount).
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

  // What scrolls in the editor besides the Builder root: the side columns,
  // and in the stacked layout the Outline and the Inspector.
  const SCROLLED =
    '.builder-layout__side, .builder-outline, .builder-inspector';

  /**
   * Reset view: the editor's view as a new session shows it. The side
   * columns show, at their default widths, and the widths stored for them
   * are forgotten; the minimap shows or hides as the settings have it, at
   * its default size; the panels scroll to the top; the sections that open
   * (the canvas's Keyboard help, the Inspector's More settings) close; and
   * the canvas has the zoom and pan it opens with. The diagram, its undo
   * history and the settings are left as they are. Focus stays where it
   * is, unless a section that closes held it: it moves to that section's
   * summary rather than to <body>.
   */
  function resetView() {
    const root = rootEl.value;

    showMinimap.value = editorSettings.showMinimap;
    canvas.value?.resetMinimap();
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
        `No published Builder v2 document exists for topology ${topology}. ${
          store.canCreateDrafts
            ? 'Use Import to make a diagram from it.'
            : 'Select its name in Configs to view it.'
        }`,
      );

      return;
    }

    await openListed(published, `topology ${topology}`, () =>
      store.canCreateDrafts
        ? store.openPublishedDocument(published.id, {
            announcement: `Opened topology ${topology} in Builder v2 as a new draft.`,
            resumed: `Opened topology ${topology} in Builder v2, in your draft of it.`,
          })
        : store.viewPublishedDocument(published.id),
    );
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

  // A column that is hidden shows first.
  async function focusOutline() {
    if (panes.value?.show('start')) {
      await nextTick();
    }

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
    panes.value?.show('end');
    await nextTick();
    await nextTick();

    const first =
      field &&
      document.querySelector(
        '.builder-inspector form :is(input, select, textarea):not([type="hidden"]):not(:disabled)',
      );

    (first || document.getElementById('inspector-title'))?.focus();
  }

  // The focused field commits what it holds, as its change event would (the
  // Diagram name commits only on change). Returns whether it was sent one.
  // Fields in a dialog (a native one, or a popup such as the color
  // picker's) are left to the dialog.
  function commitFocusedField() {
    const field = document.activeElement;

    if (
      !isTextEntry(field) ||
      !rootEl.value?.contains(field) ||
      field.closest('dialog, [role="dialog"]')
    ) {
      return false;
    }

    field.dispatchEvent(new Event('change', { bubbles: true }));

    return true;
  }

  // Before a save: the focused field commits, and the Inspector settles its
  // unapplied edits. Resolves to why some stay unapplied, or ''.
  async function settleEdits() {
    if (commitFocusedField()) {
      await nextTick();
    }

    return (await inspector.value?.settle?.()) || '';
  }

  // Before the diagram is left or read whole: the focused field commits,
  // and the Inspector saves its unapplied edits (see saveUnapplied there),
  // at once, as a reload cannot wait. Returns what cannot be saved, or
  // null.
  function saveUnapplied() {
    commitFocusedField();

    return inspector.value?.saveUnapplied?.() || null;
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
    get minimapSize() {
      return canvas.value?.minimapState || null;
    },
    get panes() {
      return panes.value?.state || { hidden: {}, stacked: false };
    },
    get canZoomIn() {
      return !canvas.value?.atMaxZoom;
    },
    get canZoomOut() {
      return !canvas.value?.atMinZoom;
    },
    get fitRestores() {
      return Boolean(canvas.value?.fitRestores);
    },
    get focusMode() {
      return focusMode.on;
    },
    openDialog,
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
    resizeMinimap(width) {
      canvas.value?.resizeMinimap(width);
    },
    togglePane(side) {
      panes.value?.toggleHidden(side);
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
    store.initTheme(rootEl.value);
    systemReducesMotion.value = prefersReducedMotion(matchMedia);
    stopWatchingSystemTheme = watchSystemTheme(matchMedia, () => {
      systemTheme.value = resolveTheme('system', matchMedia);
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
    window.removeEventListener('keydown', onGlobalKeydown);
    window.removeEventListener('beforeunload', onBeforeUnload);
    stopFollowingShortcuts();
    // Leaving the Builder, for another page, ends focus mode, so the app
    // shows its navigation bar again.
    stopFollowingFullScreen();
    stopFittingLabels();
    exitFocusMode();
    window.removeEventListener('focus', relist);
    document.removeEventListener('visibilitychange', relist);
    clearTimeout(relistTimer);
    relistWhenListed = false;
    stopWatchingSystemTheme();
    store.autosave?.dispose();
    background.dispose();
    unregisterOpenDraft();
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

  /* Back to drafts, the name and who shared the draft wrap as the header's
     own items do, until the header is wide enough for one row. Until then
     the counts are centered in the room their row leaves them: on a row
     of their own, centered on the header. */
  .builder-header__start {
    display: contents;
  }

  .builder-header > .builder-counts {
    margin-inline: auto;
  }

  /* Wide enough for one row (a header from 64rem, about a 1060px window):
     the start and the actions take the same share of the room the counts
     leave, which centers the counts on the header. The buttons show only
     the labels that fit in their share (see builder/headerLabels.js).
     Whichever needs more than its share even so takes it, and the counts
     move toward the other, never over either. */
  @container builder-editor (min-width: 64rem) {
    .builder-header__start {
      display: flex;
      flex: 1 1 0;
      align-items: center;
      gap: 0.75rem;
    }

    /* The start gives way down to Back to drafts whole, an 8rem name and
       who shared the draft on one line, as the header's own items do in a
       narrower header; then the header wraps. */
    .builder-header__start > .builder-button,
    .builder-header__shared {
      white-space: nowrap;
    }

    .builder-header__start > .builder-header__name {
      width: 8rem;
      min-width: 8rem;
    }

    .builder-header__actions {
      flex: 1 1 0;
      min-width: max-content;
    }
  }

  /* The heading is visually hidden, so while it has focus the header it
     titles shows the focus indicator instead (WCAG 2.4.7). */
  .builder-header:has(> h1:focus-visible) {
    outline: 3px solid var(--bx-focus);
    outline-offset: 2px;
    border-radius: var(--bx-radius);
  }

  .builder-header .builder-button[aria-busy='true'] {
    cursor: progress;
  }

  /* The name and its pencil, or the field in their place: 13rem wide, or
     down to 8rem to stay beside Back to drafts in a narrow window. */
  .builder-header__name {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    flex: 1 1 8rem;
    min-width: 0;
    max-width: 13rem;
    margin-bottom: 0;
  }

  .builder-header__name-text {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 600;
  }

  .builder-header__name-text.is-empty {
    color: var(--bx-text-muted);
    font-weight: 400;
  }

  /* An icon alone, as small as a target may be (WCAG 2.5.8), its frame
     shown on hover. */
  .builder-header__name-edit {
    flex: none;
    justify-content: center;
    min-width: 24px;
    min-height: 24px;
    padding: 0.2rem;
    border-color: transparent;
    background: none;
    color: var(--bx-text-muted);
  }

  .builder-header__name-edit:hover {
    border-color: var(--bx-border);
    color: var(--bx-text);
  }

  .builder-header__name-field {
    width: 100%;
    min-width: 0;
  }

  /* The checks and the buttons after them wrap as one group, which stays at
     the right edge, on a line of its own if need be. */
  .builder-header__actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 0.4rem;
    margin-inline-start: auto;
  }

  .builder-header__shared {
    margin: 0;
    font-size: 0.85rem;
    color: var(--bx-text-muted);
    overflow-wrap: anywhere;
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
