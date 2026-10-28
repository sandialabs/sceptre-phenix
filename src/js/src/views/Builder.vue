<!--
  Builder view: the editor shell, with the theme, live regions, conflict banner,
  other-tab notice, dialogs and the drafts landing.
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

    <!-- A role without the Builder permission gets this notice, and no
         drafts or editor: the server refuses it every Builder request. -->
    <section
      v-if="!store.canUseBuilder"
      class="builder-no-access"
      aria-labelledby="builder-no-access-title"
      data-testid="builder-no-access">
      <h1 id="builder-no-access-title">Builder</h1>
      <p>Your role does not include the Builder.</p>
    </section>

    <template v-else-if="!editing">
      <builder-drafts
        ref="drafts"
        :drafts="store.drafts.all"
        :published="store.documents"
        :loading="store.loading && !listed"
        :can-publish="store.canPublish"
        :can-delete-published="canDeleteConfigs"
        :deleting-published="store.deletingDocuments"
        :busy="busy"
        :opening="opening"
        :publishing="publishing"
        :duplicating="duplicating"
        :damaged="store.damagedDrafts"
        :saves="backgroundCards"
        :bulk="bulk"
        :bulk-result="bulkResult"
        :templates="templatesTab"
        :icons="iconsTab"
        :layers="layersTab"
        @blank="startBlank"
        @import="openLanding('import')"
        @upload="openLanding('upload')"
        @open="openDraft"
        @delete="deleteDraft"
        @delete-published="store.deletePublished"
        @publish="publishListed"
        @duplicate="duplicateDraft"
        @experiment="(item) => openExperiment(item.experiment)"
        @bulk-delete="deleteSelectedDrafts"
        @bulk-delete-published="deleteSelectedPublished"
        @bulk-download="downloadSelected"
        @dismiss-bulk-result="bulkResult = null"
        @announce="(text, slot) => store.announce(text, { slot })">
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
        <!-- The Node Templates tab: the server's template library. -->
        <template #templates>
          <builder-templates
            ref="templatesPanel"
            :active="drafts?.activeTab === 'templates'"
            @template="(options) => openDialog('template', options)"
            @collection="(options) => openDialog('collection', options)" />
        </template>
        <!-- The Layers tab: the server's layer set library. -->
        <template #layers>
          <builder-layer-sets @lost-focus="drafts?.focusActiveTab()" />
        </template>
        <!-- The Icons tab: the server's icon library. -->
        <template #icons>
          <builder-icons
            ref="iconsPanel"
            @lost-focus="drafts?.focusActiveTab()" />
        </template>
      </builder-drafts>
    </template>

    <template v-else>
      <div ref="editorHeader" class="builder-header">
        <!-- The name below shows the diagram name. The heading gives the
             view a title and takes focus when the editor opens. -->
        <h1 ref="editorHeading" class="builder-visually-hidden" tabindex="-1">
          {{ diagramName }} – Builder
        </h1>
        <!-- The Drafts button and the name at the start. The counts in the
             middle. The actions at the end. -->
        <div class="builder-header__start">
          <!-- While it waits for the saves, then for the lists: a turning
               ring in place of the arrow (reduced motion stops it turning)
               and what it waits for. The button is as wide as its label,
               so it is a little wider while it waits. -->
          <button
            type="button"
            class="builder-button"
            data-testid="editor-back"
            :aria-disabled="Boolean(closing) || undefined"
            :aria-busy="Boolean(closing) || undefined"
            aria-describedby="header-tip-drafts"
            v-on="headerTip('drafts')"
            @click="closeEditor">
            <span
              v-if="closing"
              class="builder-toolbar__spinner"
              aria-hidden="true"></span>
            <builder-icon v-else name="arrow-left" :size="14" />
            <span>{{ CLOSING_LABELS[closing] || 'Drafts' }}</span>
          </button>
          <!-- The name as text, cut off with an ellipsis when long (the
               text is still whole, and a tooltip shows it), then Edit
               diagram name, which puts a field in their place (see
               startNameEdit). A read-only diagram has no pencil. The field's
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
                :class="{ 'is-empty': !store.doc.metadata?.name }"
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
          <builder-resources />
        </div>

        <builder-counts />

        <!-- The Warnings button, Reset view, and the buttons the drafts' header
             has too (BuilderHeaderButtons.vue). Reset view keeps its name in a
             narrow header, where it shows only its icon. -->
        <div class="builder-header__actions">
          <builder-checks :tip-for="headerTip" />
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
          aria-haspopup="dialog"
          @click="dialog = 'tabs'">
          Choose which to save
        </button>
      </div>

      <!-- After an undo or a restore: the next change removes the newer
           snapshots (see BuilderNewerHistory.vue). The conflict panel
           takes its place while it shows. -->
      <builder-newer-history
        v-if="!store.conflictShown"
        @lost-focus="editorHeading?.focus()" />

      <!-- Not modal: the diagram stays readable and downloadable while the user
           decides. Focus moves to the heading, so the choices are the next Tab
           stops. -->
      <section
        v-if="store.conflictShown"
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
        <!-- The alert stays unchanged while the panel is open. The count
             below changes with every edit and must not be re-announced. -->
        <p role="alert">{{ conflictMessage }}</p>
        <p>
          {{ unsavedSummary }}. Choose how to keep your work. The Builder does
          not save the edits you make now until you choose.
        </p>
        <!-- Why the changes were not merged on their own: some fields
             clash, or merging is not available. -->
        <p v-if="mergeNote" data-testid="conflict-merge-note">
          {{ mergeNote }}
        </p>
        <div class="builder-conflict__actions">
          <!-- The merge dialog: a choice for each clashing field. -->
          <button
            v-if="mergeReview"
            type="button"
            class="builder-button builder-button--primary"
            data-testid="conflict-merge"
            aria-haspopup="dialog"
            :aria-disabled="resolving || undefined"
            @click="resolving || (dialog = 'merge')">
            Review and merge
          </button>
          <button
            type="button"
            class="builder-button"
            :class="{ 'builder-button--primary': !mergeReview }"
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
        :message="`${unsavedSummary}. Discarding deletes that work and loads the server version. You cannot undo this. To keep your work, cancel and select Save my history as a new draft.`"
        confirm-label="Discard and load the server version"
        @cancel="confirmingDiscard = false"
        @confirm="discardLocal" />

      <!-- A published diagram opens read-only, with no draft. A draft is made
           only when the user chooses to edit it. -->
      <div
        v-if="store.published"
        class="builder-panel builder-published"
        data-testid="builder-published">
        <p>
          <template v-if="store.published.source === 'file'">
            You are viewing the diagram of topology
            {{ store.published.target }}, read from
            {{ store.published.path }} on the phenix server.
            <template v-if="store.published.topologyDiffers">
              It differs from topology {{ store.published.target }} as stored.
              You can publish a draft made from it as a new topology, not as an
              update of {{ store.published.target }}.
            </template>
          </template>
          <template v-else>
            You are viewing the published diagram {{ store.published.name }}.
          </template>
          Edit it as a draft to make changes.
        </p>
        <builder-busy-button
          class="builder-button--primary"
          data-testid="published-edit"
          :busy="Boolean(busyTask(BUSY_GROUPS.editPublished))"
          :disabled="busy"
          @click="editPublished">
          Edit as a draft
        </builder-busy-button>
      </div>

      <div
        v-else-if="store.readOnly"
        class="builder-panel builder-error"
        role="status">
        {{ readOnlyMessage }}
      </div>

      <builder-toolbar
        :minimap="showMinimap"
        @publish="openDialog('publish')"
        @download="openDialog('download')"
        @upload="openUpload"
        @scenario="dialog = 'scenario'"
        @history="openHistory"
        @experiment="openExperiment()"
        @group-pattern="openDialog('group-pattern')"
        @toggle-minimap="showMinimap = !showMinimap" />

      <builder-panes ref="panes">
        <template #start>
          <builder-palette />
        </template>

        <!-- A new canvas for each diagram, as Upload opens one without
             leaving the editor: it opens with the zoom the settings ask
             for. -->
        <builder-canvas
          :key="openDiagram"
          ref="canvas"
          :show-minimap="showMinimap"
          :reduced-motion="reducedMotion"
          @edit="editNode" />

        <template #end>
          <builder-inspector
            ref="inspector"
            @scenario="dialog = 'scenario'"
            @combine="combineIncluded"
            @layers="openLayers"
            @edit="editNode"
            @connect="runCommand('dialog.connect', commandContext)"
            @regroup="runCommand('dialog.regroup', commandContext)" />
        </template>
      </builder-panes>
    </template>

    <!-- The headers' tooltips, in the editor and on the drafts. Their text
         reaches screen readers as the buttons' names and descriptions. The
         tooltips themselves are aria-hidden. -->
    <span
      v-for="(entry, key) in headerTips"
      :id="`header-tip-${key}`"
      :key="key"
      hidden>
      {{ entry.description }}
    </span>
    <builder-fixed-tooltip :tooltip="tooltip" testid="header-tooltip" />

    <!-- Without the Builder permission, no dialog opens. -->
    <template v-if="store.canUseBuilder">
      <!-- Publish, for the open draft, or on the drafts for the draft a card's
           Publish loaded (see publishListed). -->
      <publish-dialog
        v-if="dialog === 'publish'"
        :unapplied="unappliedBefore"
        :landing="!editing"
        :name="publishedCard?.name || ''"
        @open-draft="openPublished"
        @close="dialog = ''" />
      <!-- Upload, on the landing and in the toolbar. Import, on the landing
           only. -->
      <upload-dialog
        v-if="dialog === 'upload'"
        @close="dialog = ''"
        @uploaded="afterImport" />
      <import-dialog
        v-if="dialog === 'import'"
        :initial="importInitial"
        @close="dialog = ''"
        @imported="afterImport" />
      <download-dialog
        v-if="dialog === 'download'"
        :viewport-element="viewportElement"
        :unapplied="unappliedBefore"
        :start="downloadStart"
        @close="dialog = ''" />
      <scenario-dialog v-if="dialog === 'scenario'" @close="dialog = ''" />
      <!-- Auto-group by name pattern, from the toolbar's Auto-group menu and
           the palette. -->
      <group-pattern-dialog
        v-if="dialog === 'group-pattern'"
        @close="dialog = ''" />
      <!-- Add a connection and Move to a group, from the Inspector's
           buttons, the palette's commands and their keys. Each opens on the
           selection. -->
      <connect-dialog v-if="dialog === 'connect'" @close="dialog = ''" />
      <regroup-dialog v-if="dialog === 'regroup'" @close="dialog = ''" />
      <!-- The template editor, from the palette's "+" and the menu of a
           template of the diagram, and from the command palette. For a
           template of the library, from the Node Templates tab. -->
      <template-dialog
        v-if="dialog === 'template' && templateRequest"
        :mode="templateRequest.mode"
        :template="templateRequest.template"
        :icons="templateRequest.icons"
        :icon-size="templateRequest.iconSize"
        :layers="templateRequest.layers"
        :match-conflict="templateRequest.matchConflict"
        @close="dialog = ''" />
      <!-- The Node Editor, from Open Node Editor: the Inspector's button, a
           double-click on a node, its keys and the command palette. -->
      <node-editor-dialog
        v-if="dialog === 'node-editor' && nodeEditorRequest"
        ref="nodeEditor"
        :key="nodeEditorRequest.id"
        :node-id="nodeEditorRequest.id"
        @combine="combineFromNodeEditor"
        @layers="openLayers"
        @close="closeNodeEditor" />
      <!-- A collection of the library, from the Node Templates tab. -->
      <collection-dialog
        v-if="dialog === 'collection' && collectionRequest"
        :collection="collectionRequest.collection"
        :template-ids="collectionRequest.templateIds"
        @close="dialog = ''" />

      <history-dialog v-if="dialog === 'history'" @close="dialog = ''" />

      <!-- Review and merge, from the conflict panel: the user chooses which
           version of each clashing field to keep. -->
      <merge-dialog
        v-if="dialog === 'merge' && mergeReview"
        @merged="afterMerge"
        @close="dialog = ''" />

      <!-- Which tab's changes to save, when more than one holds some (see
           builder/tabs.js). -->
      <builder-tabs-dialog
        v-if="dialog === 'tabs'"
        :rows="tabRows"
        :busy="choosing"
        :download-version="downloadVersion"
        @choose="chooseVersion"
        @close="dialog = ''" />

      <!-- Leaving a draft whose edits the server does not have yet, or the
           Inspector cannot apply, asks first (see leave.js). So does leaving
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
           shortcuts.open command (?, the header's Shortcuts or the palette)
           through commandView.openShortcuts. -->
      <builder-shortcuts
        v-if="dialog === 'shortcuts'"
        data-testid="shortcuts-dialog"
        @close="dialog = ''" />

      <!-- Builder settings. Over the Node Editor it opens as a dialog of its
           own (settingsOver), and the Node Editor stays open under it. -->
      <builder-settings
        v-if="dialog === 'settings' || settingsOver"
        :diagram="editing"
        @theme="(theme) => store.setTheme(theme, rootEl, { announce: false })"
        @layers="layersFromSettings"
        @close="closeSettings" />
      <!-- The layers editor of the open diagram, opened by Edit layers… in
           the Inspector, the Node Editor and Settings, and by the
           settings.layers command (the palette). Over the Node Editor it
           opens as a dialog of its own (layersOver), as Settings does. -->
      <layers-dialog
        v-if="editing && (dialog === 'layers' || layersOver)"
        mode="diagram"
        @close="closeLayers" />
    </template>
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
    toRaw,
    watch,
  } from 'vue';
  import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
  import { saveAs } from 'file-saver';

  import '@/builder/builder.css';

  import BuilderBusyButton from '@/components/builder/BuilderBusyButton.vue';
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
  import BuilderLayerSets from '@/components/builder/BuilderLayerSets.vue';
  import BuilderLiveRegion from '@/components/builder/BuilderLiveRegion.vue';
  import BuilderNewerHistory from '@/components/builder/BuilderNewerHistory.vue';
  import BuilderPalette from '@/components/builder/BuilderPalette.vue';
  import BuilderPanes from '@/components/builder/BuilderPanes.vue';
  import BuilderResources from '@/components/builder/BuilderResources.vue';
  import BuilderSettings from '@/components/builder/BuilderSettings.vue';
  import BuilderShortcuts from '@/components/builder/BuilderShortcuts.vue';
  import BuilderTabsDialog from '@/components/builder/BuilderTabsDialog.vue';
  import BuilderIcons from '@/components/builder/BuilderIcons.vue';
  import BuilderTemplates from '@/components/builder/BuilderTemplates.vue';
  import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
  import CollectionDialog from '@/components/builder/dialogs/CollectionDialog.vue';
  import ConnectDialog from '@/components/builder/dialogs/ConnectDialog.vue';
  import DownloadDialog from '@/components/builder/dialogs/DownloadDialog.vue';
  import GroupPatternDialog from '@/components/builder/dialogs/GroupPatternDialog.vue';
  import HistoryDialog from '@/components/builder/dialogs/HistoryDialog.vue';
  import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';
  import LayersDialog from '@/components/builder/dialogs/LayersDialog.vue';
  import MergeDialog from '@/components/builder/dialogs/MergeDialog.vue';
  import NodeEditorDialog from '@/components/builder/dialogs/NodeEditorDialog.vue';
  import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';
  import RegroupDialog from '@/components/builder/dialogs/RegroupDialog.vue';
  import ScenarioDialog from '@/components/builder/dialogs/ScenarioDialog.vue';
  import TemplateDialog from '@/components/builder/dialogs/TemplateDialog.vue';
  import UploadDialog from '@/components/builder/dialogs/UploadDialog.vue';
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
  import { builderApi, experimentError } from '@/builder/api.js';
  import { iconsTabCount } from '@/builder/builtinIcons.js';
  import { bulkDoneMessage, bulkFailureHeading } from '@/builder/bulk.js';
  import {
    BUSY_GROUPS,
    afterPaint,
    busyTask,
    runBusy,
  } from '@/builder/busy.js';
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
  import { iconLibrary } from '@/builder/iconLibrary.js';
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
  import { clashNote } from '@/builder/merge.js';
  import {
    documentIconSize,
    findNode,
    layerSetOptions,
  } from '@/builder/model.js';
  import { selectionItemName } from '@/builder/selection.js';
  // Not builderSettings: <builder-settings> would name it as well as the
  // dialog.
  import {
    builderSettings as editorSettings,
    iconSizeMessage,
    setSetting as setEditorSetting,
  } from '@/builder/settings.js';
  import { conflictMessage as describeConflict } from '@/builder/draftMessages.js';
  import {
    diagramFile,
    downloadedDiagram,
    queuedDiagram,
  } from '@/builder/diagramFile.js';
  import { forkTitle, signedIn, useBuilderStore } from '@/builder/store.js';
  import {
    applyChoice,
    choiceRows,
    forkClosedQueue,
    needsChoice,
    tabsNotice,
  } from '@/builder/tabs.js';
  import {
    blankTemplate,
    builtinTemplates,
    templateFromNode,
  } from '@/builder/templates.js';
  import { pageTitle } from '@/router';
  import { usePhenixStore } from '@/store.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { tokenExpired } from '@/utils/token.js';

  const store = useBuilderStore();
  const route = useRoute();
  const router = useRouter();

  const rootEl = ref(null);
  const panes = ref(null);
  const canvas = ref(null);
  const inspector = ref(null);
  const drafts = ref(null);
  // The drafts page's Icons tab (BuilderIcons.vue).
  const iconsPanel = ref(null);
  const templatesPanel = ref(null);
  const editorHeading = ref(null);
  const conflictHeading = ref(null);
  const errorPanel = ref(null);
  const editing = ref(false);
  // The open dialog, by name, such as 'publish', 'settings', 'template' or
  // 'merge', or '' for none.
  const dialog = ref('');
  // What the command palette shows first: a query ('@' for Go to node) or
  // the choices of one command (its id), as commandView.openPalette asked.
  const paletteRequest = ref({ query: '', command: '' });
  // The minimap as the settings have it. The toolbar's toggle changes it
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
  // so, and what the Drafts button is waiting for: 'saving' or 'loading'.
  const opening = ref('');
  const closing = ref('');
  // What the Drafts button says while it waits.
  const CLOSING_LABELS = { saving: 'Saving…', loading: 'Loading…' };
  // The draft a card's Publish loaded, while its dialog is open on the
  // drafts: { id, name }, the name as its card gives it. The store
  // holds it as it holds an open draft, save queue and all, but the editor
  // is not shown (see publishListed). And the listed draft being loaded
  // for it (see cardKey), whose Publish says so.
  const publishedCard = ref(null);
  const publishing = ref('');
  // The listed draft being copied by its card's Duplicate (see cardKey).
  const duplicating = ref('');
  // Whether the store holds a draft: one open in the editor, or one a
  // card's Publish loaded. Leaving the page counts the work of either.
  const draftLoaded = () => editing.value || Boolean(publishedCard.value);
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

  // Edit diagram name puts a field over the name. Enter or blur renames (see
  // rename), and Escape keeps the name.
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
    nameField.value = store.doc.metadata?.name || '';
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

  // Another diagram, or the drafts, remove the field without a save.
  watch([editing, () => store.openedSeq], () => {
    editingName.value = false;
  });

  // Dismissing the page alert, or clearing it any other way, removes the
  // focused Dismiss button with it. Focus moves to what follows it and
  // does not fall to <body> (WCAG 2.4.3).
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

  // Publish, Upload and Import show their own failures. What they showed
  // is cleared when they close, so it does not reappear over the editor.
  const OWN_ERRORS = ['publish', 'upload', 'import'];
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

  // "This device keeps your 2 unsaved changes"
  const unsavedSummary = computed(() => {
    const pending = store.saveState.pending;
    const keeper = store.saveState.storageFailed
      ? 'Only this tab keeps'
      : 'This device keeps';

    return `${keeper} your ${count(pending, 'unsaved change')}`;
  });

  // A conflict blocks every save, so it takes focus: the choices are then
  // the next Tab stops instead of a long way back up the page. Not from a
  // field being typed into, though: the keys that follow would be lost, and
  // the panel's alert announces the conflict anyway.
  watch(
    () => store.conflictShown,
    (now) => {
      if (now && !document.activeElement?.matches?.(TYPING)) {
        conflictHeading.value?.focus();
      }
    },
    { flush: 'post' },
  );

  // Whether the conflict panel offers Review and merge (see mergeConflict
  // in the store).
  const mergeReview = computed(
    () => store.merge?.status === 'review' && !store.saveState.otherTab,
  );

  // Why the changes were not merged without asking: how many fields clash,
  // that the merged diagram would be refused, or why merging is not
  // available.
  const mergeNote = computed(() => {
    const { merge } = store;

    if (!merge || store.saveState.otherTab) {
      return '';
    }

    if (merge.status === 'unavailable') {
      return merge.reason;
    }

    if (merge.status !== 'review') {
      return '';
    }

    return clashNote(merge.clashes, merge.from);
  });

  // The merge dialog closes once the review is no longer offered: the merged
  // diagram was saved, or the conflict ended another way. The dialog is gone
  // before its own merged event could arrive, so focus moves on from here
  // (see afterMerge).
  watch(mergeReview, (offered) => {
    if (!offered && dialog.value === 'merge') {
      afterMerge();
    }
  });

  // Who saved the version the conflict is with, as the server said.
  // Without sign-in everyone is the same user, so no one is named.
  const conflictMessage = computed(() =>
    store.saveState.otherTab
      ? 'The Builder cannot save the changes here to this draft, because you chose to save the changes from another tab.'
      : describeConflict(
          signedIn() ? store.saveState.lastModifiedBy : '',
          usePhenixStore().username,
        ),
  );

  // Why the open diagram is read only, when no panel of its own says so.
  const readOnlyMessage =
    'You can view this diagram but not change it. Select Download to keep a copy.';

  const diagramName = computed(
    () => store.doc.metadata?.name || 'Untitled diagram',
  );

  // A published topology's Delete deletes a phenix config, which the role
  // must allow.
  const canDeleteConfigs = computed(() => roleAllowed('configs', 'delete'));

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
  // failed listing raised. The lists show "Loading…" until the first read.
  // Later reads keep them on screen, and announce nothing.
  let listing = null;
  let listedAt = 0;
  let listingErrorSeq = 0;
  const listed = ref(false);

  // Reads the server's icon library, which draws the custom icons nodes and
  // templates name. A library that cannot be read leaves the built-in icons
  // drawn, and the Custom icons dialog says why.
  function loadIcons() {
    iconLibrary.load().catch(() => {});
  }

  // With keepError, the page alert keeps saying what failed before.
  async function refresh({ keepError = false } = {}) {
    const errors = store.errorSeq;
    // The template, layer set and icon libraries are read with the lists:
    // their tabs are among theirs, with their counts. Each says itself when
    // it cannot be read.
    const current = Promise.all([
      store.fetchDrafts({ keepError }),
      store.fetchDocuments(),
      store.fetchTemplates(),
      store.fetchLayerSets(),
      iconLibrary.load().catch(() => {}),
    ]);

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

  // The lists are read again when they show again, because users change drafts
  // elsewhere. The read waits a moment, because focus and visibilitychange
  // often arrive together.
  // No read starts while a dialog is open, or while the page alert shows
  // something else, because a listing clears the alert.
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
        !store.canUseBuilder ||
        dialog.value ||
        document.visibilityState === 'hidden'
      ) {
        return;
      }
      if (editing.value) {
        store.fetchTemplates();
        store.fetchLayerSets();
        loadIcons();
        return;
      }
      if (store.error && store.errorSeq !== listingErrorSeq) {
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

    // The palette of the draft that opens lists the template library as
    // it is now, and its nodes draw the icon library's icons as they are
    // now.
    if (now && !before) {
      store.fetchTemplates();
      store.fetchLayerSets();
      loadIcons();
    }
  });

  // Runs one of the ways to make or open a draft, unless one is in progress:
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
      store.drafts.all.map((item) => item.title || item.name || ''),
      (name) => name.trim().toLowerCase(),
      ' ',
    );
  }

  function startBlank() {
    return whileBusy(() =>
      runBusy(BUSY_GROUPS.blank, 'blank', async () => {
        const title = untitledName();

        // A new blank diagram copies the server's default layer set.
        await store.layerSetsReady();
        store.newDocument({ name: title });

        const created = await store.createDraft({ title });

        editing.value = Boolean(created);
      }),
    );
  }

  // The topology the Import dialog opens on, when a link from Configs named
  // one that has no diagram to open (see openLinked): { kind, name }.
  const importInitial = ref(null);

  function openLanding(which, { initial = null } = {}) {
    if (busy.value) {
      return;
    }

    store.newDocument({ name: 'Untitled topology' });
    editing.value = false;
    importInitial.value = which === 'import' ? initial : null;
    dialog.value = which;
  }

  // How long a wait lasts before the live region says what it is for. A
  // shorter one says nothing: its button has shown it, and what it ends
  // with is then heard at once rather than after the wait's message.
  const WAIT_ANNOUNCE_MS = 1000;

  /**
   * Says what a wait is for once it has lasted WAIT_ANNOUNCE_MS. The returned
   * function ends the wait.
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
  // focus once it shows (see the watch on editing). A failure leaves the
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

  // A published diagram opens read only, with no draft: a published
  // document by its id, the diagram of a topology's Builder file by the
  // handle its row has for one. The landing names the item as its card does
  // (label).
  function openDraft(item, label = '') {
    const name = label || item.name || item.title || item.target || 'the draft';

    return openListed(item, name, async () => {
      // A published diagram is one of the listed documents. Anything else,
      // a card or a link, is a draft.
      const published = store.documents.some(
        (document) => toRaw(document) === toRaw(item),
      );

      if (!published && item.id) {
        // Its saves in the background end, and the draft's own queue sends
        // what is left (see createBackgroundSaves).
        await background.release(item.id);

        return store.loadDraft(item.id);
      }

      return item.id ? store.viewPublishedDocument(item.id) : null;
    });
  }

  // Edits the published diagram shown read only, in the user's draft of it
  // or a new one. The panel and its button disappear, so focus moves to the
  // editor's heading. A failure leaves both, and the page alert says why.
  function editPublished() {
    return whileBusy(() =>
      runBusy(BUSY_GROUPS.editPublished, 'edit', async () => {
        if (await store.editPublished()) {
          await nextTick();
          editorHeading.value?.focus();
        }
      }),
    );
  }

  // A card's Publish: loads the draft as Open does, without the editor, and
  // opens the Publish dialog on the drafts page.
  function publishListed(item, label = '') {
    const name = label || item.title || item.name || 'the draft';

    return whileBusy(async () => {
      publishing.value = cardKey(item);
      const waited = announceWait(`Loading ${name}…`);

      // From here on the store holds the draft, or is about to: what it
      // recovers from this device counts as work to keep (see draftLoaded).
      publishedCard.value = { id: item.id, name };

      try {
        await background.release(item.id);

        const loaded = await store.loadDraft(item.id, { quiet: true });

        if (!loaded) {
          // The lists are read again: the draft may be gone.
          releaseListed();
          store.fetchDrafts({ keepError: true });

          return;
        }

        unappliedBefore.value = null;
        dialog.value = 'publish';
      } catch (error) {
        releaseListed();

        throw error;
      } finally {
        waited();
        publishing.value = '';
      }
    });
  }

  // Releases the draft a card's Publish loaded, or began to load: its queue
  // continues to send what is left in the background, as after Back to
  // drafts (see sendInBackground). The store is left with no draft, as the
  // drafts have it.
  function releaseListed() {
    publishedCard.value = null;
    sendInBackground();
    store.newDocument({ name: 'Untitled topology' });
  }

  // Resolves once no publish is in progress.
  function publishSettled() {
    if (!store.publishing) {
      return Promise.resolve();
    }

    return new Promise((resolve) => {
      const stop = watch(
        () => store.publishing,
        (now) => {
          if (!now) {
            stop();
            resolve();
          }
        },
      );
    });
  }

  // The dialog of a card's Publish has closed: the draft is handed back, and
  // the lists are read again. A publish in progress ends first.
  let unloading = false;

  async function unloadListed() {
    if (unloading || !publishedCard.value) {
      return;
    }

    const errors = store.errorSeq;
    const waited = store.publishing;

    if (waited) {
      unloading = true;
      busy.value = true;

      try {
        await publishSettled();
      } finally {
        unloading = false;
        busy.value = false;
      }

      // The view was left meanwhile, which ended the draft's queue.
      if (!rootEl.value) {
        return;
      }
    }

    const failed = waited && Boolean(store.error) && store.errorSeq !== errors;

    releaseListed();

    if (!failed) {
      store.clearError();
    }

    await refresh({ keepError: failed });
  }

  watch(dialog, (_, before) => {
    if (before === 'publish' && publishedCard.value) {
      unloadListed();
    }
  });

  // The dialog's Open draft: the editor shows the loaded draft. Go to on an
  // issue also asks for the issue, which the editor acts on once it is drawn.
  function openPublished(issue) {
    publishedCard.value = null;
    dialog.value = '';
    editing.value = true;

    if (issue && typeof issue === 'object') {
      store.goToIssue(issue);
    }
  }

  // Exp: opens the experiment of the diagram's publication in this tab. It
  // reads the experiment first, so one deleted meanwhile leaves the Builder as
  // it is.
  async function openExperiment(name = store.experimentName) {
    if (!name) {
      return;
    }

    await runBusy(BUSY_GROUPS.experiment, name, async () => {
      store.clearError();

      try {
        const { running } = await builderApi.experimentState(name);

        await router.push({
          name: 'experiment',
          params: { id: name },
          state: { running },
        });
      } catch (error) {
        store.setError(experimentError(name, error));
      }
    });
  }

  // BuilderDrafts has asked for confirmation already, naming the draft as
  // its card does (label). The announcement uses the same name. The store
  // refreshes the list after a delete. A refresh here too would clear the
  // error of a delete that failed.
  async function deleteDraft(item, label) {
    await background.release(item.id);
    await store.deleteDraft(
      item.id,
      item.etag,
      label || item.name || item.title || item.id,
    );
  }

  // --- bulk actions on the drafts page ----------------------------------

  // The drafts page's Node Templates tab, which says how many templates the
  // library holds. Its panel is BuilderTemplates.vue.
  const templatesTab = computed(() => ({
    count: store.libraryTemplates.length,
  }));

  // The Icons and Layers tabs of the drafts page. Until a library is read, its
  // count is null, so the tab shows no false 0.
  const layersTab = computed(() => ({
    count: store.layerSets.loaded ? store.layerSets.items.length : null,
  }));

  // The Icons tab also counts the built-in icons.
  const iconsTab = computed(() => ({
    count: iconsTabCount(iconLibrary.state),
  }));

  // The batch in progress on the selection of a tab of the drafts page, which
  // its row shows: { tab, label, done, total }. And what the last batch
  // left undone, listed on its tab until it is dismissed or the next batch
  // starts: { tab, heading, items }, each item { key, name, reason }.
  const bulk = ref(null);
  const bulkResult = ref(null);

  // Runs a batch on the selected items with `run`, with progress on the tab's
  // row. Items that fail stay selected, and a summary on the tab lists them
  // with why.
  function runSelected({ tab, items, names, noun, plural, label, done, run }) {
    return whileBusy(async () => {
      const total = items.length;
      const waited = announceWait(`${label} ${count(total, noun, plural)}…`);

      bulkResult.value = null;
      bulk.value = { tab, label, done: 0, total };

      try {
        const outcome = await run((ended) => {
          bulk.value = { ...bulk.value, done: ended };
        });

        if (outcome.failures.length) {
          bulkResult.value = {
            tab,
            heading: bulkFailureHeading(
              outcome.failures.length,
              total,
              noun,
              plural,
              done,
            ),
            items: outcome.failures.map(({ item, reason }) => ({
              key: cardKey(item),
              name: names[cardKey(item)] || item.title || item.id,
              reason,
            })),
          };
        } else {
          store.announce(
            bulkDoneMessage(outcome.done.length, noun, plural, done),
          );
        }
      } finally {
        waited();
        bulk.value = null;
      }
    });
  }

  function deleteSelected(options) {
    return runSelected({ ...options, label: 'Deleting', done: 'deleted' });
  }

  // Download selected: a Builder JSON file for each selected draft or
  // published diagram, saved one after another as Download saves the open
  // diagram (see downloadDrafts and downloadPublishedMany in store.js).
  function downloadSelected(tab, items, names = {}) {
    const published = tab === 'published';
    const save = (file) =>
      saveAs(
        new Blob([file.text], { type: 'application/json;charset=utf-8' }),
        file.name,
      );

    return runSelected({
      tab,
      items,
      names,
      noun: published ? 'diagram' : 'draft',
      plural: published ? 'diagrams' : 'drafts',
      label: 'Downloading',
      done: 'downloaded',
      run: (onProgress) =>
        published
          ? store.downloadPublishedMany(items, { save, onProgress })
          : store.downloadDrafts(items, { save, onProgress }),
    });
  }

  // Each draft's saves in the background end before it is deleted, as for
  // the Delete of one card.
  function deleteSelectedDrafts(tab, items, names = {}) {
    return deleteSelected({
      tab,
      items,
      names,
      noun: 'draft',
      plural: 'drafts',
      run: (onProgress) =>
        store.deleteDrafts(items, {
          before: (item) => background.release(item.id),
          onProgress,
        }),
    });
  }

  function deleteSelectedPublished(items, names = {}) {
    return deleteSelected({
      tab: 'published',
      items,
      names,
      noun: 'topology',
      plural: 'topologies',
      run: (onProgress) => store.deletePublishedMany(items, { onProgress }),
    });
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

    await nextTick();
    focusAfterConflict();
  }

  // The panel and its buttons are gone once a conflict is resolved. Focus
  // goes to the editor heading instead of falling to <body>. A failed
  // attempt keeps the panel, and focus returns to its heading.
  function focusAfterConflict() {
    (store.conflictShown
      ? conflictHeading.value
      : editorHeading.value
    )?.focus();
  }

  function discardLocal() {
    confirmingDiscard.value = false;

    return resolveConflict('reload');
  }

  // The merge dialog closed with the review: once the merged diagram is
  // saved the conflict panel is gone too, and focus goes to the editor's
  // heading rather than to <body>.
  async function afterMerge() {
    dialog.value = '';
    await nextTick();
    focusAfterConflict();
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
      // A token the dialog gives is the draft's: a legacy diagram converted
      // without a topology names no config for one to be made from. A copy
      // of a stored topology, or one combined with its included topologies,
      // is linked to no config, so its draft has no token. One made from a
      // config file is still an upload's.
      const sourceToken =
        result.sourceToken ||
        (result.detached && source?.stored
          ? ''
          : source?.fullName
            ? source.stored
              ? source.fullName
              : `uploaded/${source.fullName}`
            : '');

      // An Import, and an Upload that converted a legacy diagram, say what
      // they made once the draft exists (see ImportDialog and
      // UploadDialog). Any other Upload has said so already. Each names
      // the file it read, when it read one, which the draft records.
      ready = Boolean(
        await store.createDraft({
          document: result.document || store.doc,
          title: (result.document || store.doc).name,
          sourceToken,
          sourceFile: result.sourceFile,
          announcement: result.announcement,
        }),
      );
    }

    editing.value = ready;
  }

  // Combine included nodes into a new draft (see combineIncluded in the store).
  // Focus moves to the editor's heading, because the Inspector's button
  // disappears with the node.
  async function combineIncluded() {
    if (busyTask(BUSY_GROUPS.combine) || !(await mayLeave())) {
      return;
    }

    await runBusy(BUSY_GROUPS.combine, 'combine', () =>
      whileBusy(async () => {
        const waited = announceWait(
          'Making a new draft with the included nodes…',
        );

        try {
          editing.value = Boolean(await store.combineIncluded());
        } finally {
          waited();
        }
        await nextTick();

        if (editing.value && focusLost()) {
          editorHeading.value?.focus();
        }
      }),
    );
  }

  // Duplicate draft: a new draft with a copy of a draft's diagram, which then
  // opens (see duplicateDraft in the store).
  async function duplicateDraft(item = null) {
    if (!item) {
      if (!(await mayLeave())) {
        return;
      }

      await whileBusy(async () => {
        const created = await store.duplicateDraft({
          id: store.draftId,
          document: toRaw(store.doc),
        });

        await nextTick();

        if (created && focusLost()) {
          editorHeading.value?.focus();
        }
      });

      return;
    }

    const name = item.title || item.name || 'the draft';

    await whileBusy(async () => {
      duplicating.value = cardKey(item);
      const waited = announceWait(`Duplicating ${name}…`);

      try {
        if (backgroundCards.value[cardKey(item)]) {
          await background.flush();
        }

        const created = await store.duplicateDraft({ id: item.id });

        if (created) {
          await afterPaint();
        }

        editing.value = Boolean(created);
      } finally {
        waited();
        duplicating.value = '';
      }
    });
  }

  // Returns whether the name changed.
  function rename(name) {
    if (name === (store.doc.metadata?.name || '')) {
      return false;
    }

    store.setInfo({ name });

    return true;
  }

  // The saves of drafts closed for the drafts, which continue in the
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
  // (see leave.js). The question is asked only when some of it is not
  // saved yet. The Drafts button does not wait: the save continues in the
  // background.
  const leaveGuard = createLeaveGuard({
    store,
    editing: draftLoaded,
    saveUnapplied,
    background,
    ask(unapplied, scope = '') {
      unsaveable.value = unapplied;
      leavingScope.value = scope;

      return new Promise((resolve) => {
        leaving.value = resolve;
      });
    },
    sessionOver() {
      const phenix = usePhenixStore();

      return !phenix.auth || tokenExpired(phenix.token);
    },
  });
  const { unsavedWork, mayLeave, mayClose } = leaveGuard;

  // The Drafts button leaves the queued changes to be sent in the background,
  // so its question names only the Inspector's edits.
  const leaveMessage = computed(() => {
    const one = store.saveState.pending === 1;
    const queued =
      leavingScope.value !== 'close' && unsavedWork()
        ? `${unsavedSummary.value}. ${
            store.saveState.storageFailed
              ? `Leaving loses ${one ? 'it' : 'them'}.`
              : `The Builder saves ${one ? 'it' : 'them'} when you next open this draft in this browser, unless you log out first.`
          }`
        : '';
    const { changes, drafts: closed } =
      leavingScope.value === 'all'
        ? backgroundPending.value
        : { changes: 0, drafts: 0 };
    const behind = changes
      ? `This device keeps your ${count(changes, 'unsaved change')} to ${closed === 1 ? 'a draft you closed' : 'drafts you closed'}. The Builder saves ${changes === 1 ? 'it' : 'them'} when you next open ${closed === 1 ? 'that draft' : 'those drafts'} in this browser, unless you log out first.`
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
  // leaving continues, and does not ask about no changes. Edits the
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
  // Builder session already (see endBuilderSession) and is not stopped.
  // With an expired token, the router logs out instead.
  onBeforeRouteLeave(() => leaveGuard.mayFollowLink());

  // Upload makes a new draft, which leaves this one.
  async function openUpload() {
    if (await mayLeave()) {
      dialog.value = 'upload';
    }
  }

  // Publish and Download read the whole diagram, so what the Inspector holds
  // unapplied is saved first. The dialog names the edits the Inspector
  // cannot apply, and does not leave them out.
  const READS_DIAGRAM = ['publish', 'download'];
  const unappliedBefore = ref(null);
  // The format the Download dialog downloads as it opens, for the palette's
  // commands that name one ('png'). '' when it opens to offer them all.
  const downloadStart = ref('');

  // What the template editor opens on: its mode, the template it starts
  // from, and the custom icons kept where that template is and the icon
  // size of the diagram it is in (see TemplateDialog.vue).
  const templateRequest = ref(null);

  // The template editor on a template of the library, from the Node
  // Templates tab: 'library-edit' edits the one `id` names, 'library-new'
  // starts a new one from a plain device, with no name. 'view' shows the
  // template of `source` ('library', 'preloaded' or 'builtin') that `id`
  // names, read only.
  function openLibraryTemplate(mode, id, source = '') {
    const template = {
      'library-edit': () =>
        store.libraryTemplates.find((entry) => entry.id === id),
      'library-new': () => blankTemplate(),
      view: () =>
        source === 'builtin'
          ? builtinTemplates(store.templates).find((entry) => entry.id === id)
          : store.templates.items.find(
              (entry) => entry.source === source && entry.id === id,
            ),
    }[mode]?.();

    // A template of a library names its custom icon, which the icon
    // library resolves: it comes with no copies of icons. It is in no
    // diagram, so it has no diagram's icon size, and offers the layers of
    // the server's default layer set.
    templateRequest.value = template
      ? {
          mode,
          template,
          icons: null,
          iconSize: '',
          layers: store.defaultLayers(),
          matchConflict: store.defaultLayerSet?.matchConflict || '',
        }
      : null;

    return Boolean(template);
  }

  // Opens the template editor. 'diagram-new' starts from the one selected
  // device, without its VLANs, else from a plain device. Returns whether there
  // is a template.
  function openTemplate({ mode = 'diagram-new', id = '', source = '' } = {}) {
    if (mode.startsWith('library-') || mode === 'view') {
      return openLibraryTemplate(mode, id, source);
    }

    if (!editing.value || store.readOnly) {
      return false;
    }

    if (mode === 'diagram-edit') {
      const template = (store.doc.templates || []).find(
        (entry) => entry.id === id,
      );

      templateRequest.value = template
        ? {
            mode,
            template,
            icons: store.doc.icons || null,
            iconSize: documentIconSize(store.doc),
            layers: store.doc.layers || [],
            matchConflict: layerSetOptions(store.doc).matchConflict,
          }
        : null;

      return Boolean(template);
    }

    if (store.selectedNode?.kind === 'device') {
      saveUnapplied();
    }

    const node = store.selectedNode;

    templateRequest.value = {
      mode: 'diagram-new',
      template:
        node?.kind === 'device'
          ? templateFromNode(node, {
              name: node.device.hostname,
              clearVLANs: true,
            })
          : blankTemplate(),
      icons: store.doc.icons || null,
      iconSize: documentIconSize(store.doc),
      layers: store.doc.layers || [],
      matchConflict: layerSetOptions(store.doc).matchConflict,
    };

    return true;
  }

  // What the collection dialog opens on: the collection of the library to
  // edit, or null for a new one, and the templates a new one starts with,
  // by id (see CollectionDialog.vue).
  const collectionRequest = ref(null);

  // Returns whether there is something to open the dialog on: a collection
  // that `id` names may have left the library.
  function openCollection({ id = '', templateIds = [] } = {}) {
    const collection = id
      ? store.libraryCollections.find((entry) => entry.id === id)
      : null;

    collectionRequest.value =
      id && !collection ? null : { collection, templateIds };

    return Boolean(collectionRequest.value);
  }

  // The node the Node Editor edits: {id}.
  const nodeEditorRequest = ref(null);
  // The Node Editor while it is open (see saveUnapplied).
  const nodeEditor = ref(null);

  // Open Node Editor from the Inspector's button or a double-click on a
  // node: the command, for that node (see node.edit in commands.js).
  function editNode(id) {
    runCommand('node.edit', { ...commandContext, item: { kind: 'nodes', id } });
  }

  // The Node Editor's form and the Inspector's never hold edits at the
  // same time. The Inspector first applies its valid unapplied edits, as a
  // change of selection does. Edits it cannot apply keep the dialog closed:
  // focus moves to the Inspector's list of the fields to fix.
  async function openNodeEditor({ id = '' } = {}) {
    if (!editing.value || !findNode(store.doc, id)) {
      return;
    }

    // A command run on another node has selected it: the Inspector settles
    // the edits of the node it showed first, as for any change of selection.
    await nextTick();

    const unapplied = await settleEdits();

    if (unapplied) {
      store.announce(
        `Could not open the Node Editor: ${unapplied} in the Inspector.`,
      );
      panes.value?.show('end');
      await nextTick();
      document
        .querySelector('.builder-panel .builder-inspector__error-link')
        ?.focus();

      return;
    }

    nodeEditorRequest.value = { id };
    dialog.value = 'node-editor';
  }

  // Focus goes back to the node on the canvas, once the dialog has given
  // it back to the control that opened it.
  async function closeNodeEditor() {
    const id = nodeEditorRequest.value?.id;

    settingsOver.value = false;
    layersOver.value = false;
    dialog.value = '';
    nodeEditorRequest.value = null;
    await nextTick();

    if (id && findNode(store.doc, id)) {
      canvas.value?.showNode(id);
    }
  }

  // Combine into a new draft, under the note of an included device.
  function combineFromNodeEditor() {
    dialog.value = '';
    nodeEditorRequest.value = null;
    combineIncluded();
  }

  // Over the Node Editor, Settings and the layers editor open as a second
  // dialog (settingsOver, layersOver), so the Node Editor keeps its
  // unapplied edits.
  const settingsOver = ref(false);
  const layersOver = ref(false);

  function openSettings() {
    if (dialog.value === 'node-editor') {
      settingsOver.value = true;
    } else {
      dialog.value = 'settings';
    }
  }

  function closeSettings() {
    if (settingsOver.value) {
      settingsOver.value = false;
    } else {
      dialog.value = '';
    }
  }

  // The layers editor of the open diagram (LayersDialog.vue). On the
  // drafts page, the Layers tab, which edits the server's layer sets.
  function openLayers() {
    if (!editing.value) {
      drafts.value?.showTab('layers');
    } else if (dialog.value === 'node-editor') {
      layersOver.value = true;
    } else {
      dialog.value = 'layers';
    }
  }

  function closeLayers() {
    if (layersOver.value) {
      layersOver.value = false;
    } else {
      dialog.value = '';
    }
  }

  // Edit layers… at the bottom of Settings: Settings closes, and gives focus
  // back to its opener, which the layers editor then gives it back to.
  async function layersFromSettings() {
    closeSettings();
    await nextTick();
    openLayers();
  }

  function openDialog(name, options = {}) {
    if (name === 'upload' && editing.value) {
      return openUpload();
    }

    if (name === 'node-editor') {
      return openNodeEditor(options);
    }

    if (name === 'template' && !openTemplate(options)) {
      return;
    }

    if (name === 'collection' && !openCollection(options)) {
      return;
    }

    if (READS_DIAGRAM.includes(name)) {
      unappliedBefore.value = editing.value ? saveUnapplied() : null;
    }

    downloadStart.value = name === 'download' ? options.start || '' : '';
    dialog.value = name;
  }

  // Whether the lists hold a draft or published diagram.
  function inLists(id) {
    return [store.drafts.all, store.documents].some((items) =>
      (items || []).some((item) => item.id === id),
    );
  }

  // The Drafts button. The Inspector's unapplied edits are saved, and unsent
  // changes go on in the background. Focus moves to the closed draft's card.
  // Asked twice, it closes once.
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
        waited = announceWait('Loading the drafts…');
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

  // The palette's library button: back to the drafts, as closeEditor, then
  // their Node Templates tab, which takes focus. With `show`, a value of
  // the tab's Show field, the tab shows that list (a collection, from the
  // command palette), and the Show field takes focus. When the user stays
  // in the editor, nothing more happens.
  async function openTemplateLibrary(show = '') {
    if (editing.value) {
      await closeEditor();
    }

    if (editing.value) {
      return;
    }

    await nextTick();
    drafts.value?.showTab('templates');

    if (show) {
      await nextTick();
      await templatesPanel.value?.showList(show);
    }
  }
  // Back to the drafts, as closeEditor, then their Icons tab, which takes
  // focus. With a name, the preview of that icon opens there too (see
  // showIcon in BuilderIcons.vue). When the user stays in the editor,
  // nothing more happens.
  async function openIcons(name = '') {
    if (editing.value) {
      await closeEditor();
    }

    if (editing.value) {
      return;
    }

    await nextTick();
    drafts.value?.showTab('icons');

    if (name) {
      await nextTick();
      await iconsPanel.value?.showIcon(name);
    }
  }

  // The closed draft's queue continues to send what is left, in the
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

  // The theme button of both headers shows the theme in use. Its name and
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
  // Whether the dialog is applying the choice now.
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
          'Nothing is left to choose. The other tabs saved or kept their changes.',
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
  // draft, as the conflict panel's Save my history as a new draft does.
  watch(
    () =>
      editing.value && store.hasConflict && Boolean(store.saveState.otherTab),
    (now) => {
      if (now && !choosing.value) {
        resolveConflict('fork');
      }
    },
  );

  // Saves the version the user chose. The others are kept as new drafts
  // (see applyChoice). The dialog stays open, busy, until
  // it is done. Focus then goes back where it was, or to the editor's
  // heading when that is gone (see the watch on the dialog).
  async function chooseVersion(id) {
    if (choosing.value || !store.autosave) {
      return;
    }

    const { draftId } = store;
    const local = createDraftStore();
    const actor = usePhenixStore().username || 'anonymous';
    let outcome = null;

    choosing.value = true;

    try {
      outcome = await applyChoice({
        choice: id,
        rows: tabRows.value,
        queue: store.autosave,
        keepMine: async () => Boolean(await store.resolveConflict('fork')),
        keepClosed: (version) =>
          forkClosedQueue({
            api: builderApi,
            store: local,
            actor,
            key: version.key,
            title: forkTitle,
          }),
        reopen: () => store.loadDraft(draftId),
      });
    } catch (error) {
      store.setError(store.describeError(error, 'save the changes you chose'));
    } finally {
      choosing.value = false;
    }

    // The tab chosen has gone: its changes are listed as a closed tab's,
    // and the choice stays open.
    if (outcome?.gone) {
      store.announce('That tab is closed. Choose again.');

      return;
    }

    dialog.value = '';

    // A failure the page alert has said already leaves no outcome.
    if (outcome?.failed.length > 0) {
      store.setError(
        'Could not save the changes a closed tab left as a new draft. This browser keeps them.',
      );
    } else if (outcome?.kept.length > 0) {
      store.announce(
        outcome.kept.length === 1
          ? 'Saved the changes a closed tab left as a new draft.'
          : `Saved the changes ${outcome.kept.length} closed tabs left as new drafts.`,
      );
    }
  }

  // The file Download saves for a version this tab holds: the diagram as it
  // is shown, or as a closed tab's changes leave it, with a copy of every
  // custom icon it uses (see downloadedDiagram).
  async function downloadVersion(row) {
    if (row.where === 'this') {
      return diagramFile(await downloadedDiagram(store.doc));
    }

    const record = await createDraftStore()
      .get(row.key)
      .catch(() => null);
    const doc = record ? queuedDiagram(record) : null;

    return doc ? diagramFile(await downloadedDiagram(doc)) : null;
  }

  // --- header -----------------------------------------------------------------

  // Per button: the tooltip's text, the description screen readers get in its
  // place, and aria-keyshortcuts.
  const RESET_TIP = 'Reset column widths, zoom, minimap and scrolling';
  const DRAFTS_TIP =
    'Return to the Drafts page. The Builder saves your work automatically, so you do not lose it.';
  const CHECKS_TIP =
    'Diagram checks: the errors and warnings in this diagram. Select to see the list.';
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
      drafts: { text: DRAFTS_TIP, description: DRAFTS_TIP },
      checks: { text: CHECKS_TIP, description: CHECKS_TIP },
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
      // Commands' names the palette. The theme's says what a press does,
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

  // Read when the tooltip shows, so it follows the keys and the header's
  // width.
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

  // A press from the keyboard keeps a tooltip open, and a press of the theme
  // or Focus mode changes what the button does next, so the tooltip changes
  // with it. This happens only while the tooltip is still that button's, and
  // the button still has focus or the pointer, so a pending hide is not
  // cancelled.
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
   * Turns focus mode (see focusMode.js) on or off, and says which. Focus stays
   * where it is, because nothing that can hold it is hidden.
   */
  function toggleFocusMode() {
    if (focusMode.on) {
      exitFocusMode();
      store.announce('Focus mode off: the navigation bar is back.');

      return;
    }

    enterFocusMode();
    store.announce(
      `Focus mode on: the navigation bar is hidden. ${focusModeExit()} shows it again.`,
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
  // and in the stacked layout the Inspector.
  const SCROLLED = '.builder-layout__side, .builder-inspector';

  /**
   * Reset view: the editor's view as a new session shows it. The diagram, its
   * undo history and the settings stay. Focus moves to a closed section's
   * summary, not to <body>.
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
   * Opens what the address names: ?draft=id or ?topology=name. A topology
   * without a diagram opens the Import dialog, so following the link twice
   * makes no two drafts.
   */
  async function openLinked({ draft, topology }) {
    if (draft) {
      // A draft ID has no slash.
      if (draft.includes('/')) {
        store.setError(
          'This link does not name a draft. Open one from the lists instead.',
        );

        return;
      }

      await openDraft({ id: draft });

      return;
    }

    if (!topology) {
      return;
    }

    const published = store.documents.find(
      (document) => document.target === topology,
    );

    if (published) {
      await openListed(published, `topology ${topology}`, () =>
        store.openPublishedDocument(published.id, {
          announcement: `Opened topology ${topology} in the Builder as a new draft.`,
          resumed: `Opened topology ${topology} in the Builder, in its existing draft.`,
          publisher: `Opened topology ${topology} in the Builder, in the draft that published it.`,
        }),
      );

      return;
    }

    const listed = (store.sources.topologies || []).some(
      (entry) => (typeof entry === 'string' ? entry : entry?.name) === topology,
    );

    if (!listed) {
      store.setError(
        `Topology ${topology} does not exist, or you do not have permission to read it.`,
      );

      return;
    }

    // The dialog gives focus back to the control that had it, which is the
    // landing's Import, as though it had opened the dialog.
    rootEl.value?.querySelector('[data-testid="drafts-import"]')?.focus();
    openLanding('import', { initial: { kind: 'topology', name: topology } });
    router.replace({ query: {} });
  }

  // An open draft always puts its id in the address, so a reload reopens it.
  // Leaving the editor drops it. A read-only published diagram keeps its link.
  watch(
    () => {
      if (!editing.value) {
        return '';
      }

      return store.published || !store.autosave || !store.draftId
        ? null
        : store.draftId;
    },
    (draft) => {
      const named = route.query.draft || route.query.topology;

      if (draft === null || (!draft && !named) || route.query.draft === draft) {
        return;
      }

      router.replace({ query: draft ? { draft } : {} });
    },
  );

  // --- commands -------------------------------------------------------------

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

  // Go to from a list of checks: the Inspector shows, and focus moves to the
  // field or the element. The page does not scroll to it, because that scrolls
  // the canvas's clipped layers. It runs once the view is drawn.
  watch(
    () => store.focusRequest?.token,
    async (token) => {
      const request = store.focusRequest;

      if (!token || !request || !editing.value) {
        return;
      }

      panes.value?.show('end');

      if (request.field) {
        const edge =
          request.kind === 'edges'
            ? (store.doc.edges || []).find((entry) => entry.id === request.id)
            : null;

        canvas.value?.revealNode?.(
          edge ? [edge.sourceNodeId, edge.targetNodeId] : request.id,
          { zoomIn: !edge },
        );

        return;
      }

      const selected = `Selected ${selectionItemName(store.doc, { kind: request.kind, id: request.id })}`;

      store.announce(
        request.hiddenEdge
          ? `Connections are hidden. ${selected}, the connection's source.`
          : selected,
      );
      await nextTick();

      const wrapper =
        request.kind === 'nodes' ? 'vue-flow__node' : 'vue-flow__edge';
      const area = document.getElementById('builder-canvas');
      const element = area?.querySelector(
        `.${wrapper}[data-id="${CSS.escape(request.id)}"]`,
      );

      if (element && store.focusRequest?.token === token) {
        area.scrollIntoView({ block: 'nearest' });
        element.focus({ preventScroll: true, focusVisible: true });

        // A node gone to is shown at a readable zoom, as by Go to node.
        if (request.kind === 'nodes') {
          canvas.value?.revealNode?.(request.id, { zoomIn: true });
        }
      }
    },
    { flush: 'post' },
  );

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
  // at once, as a reload cannot wait. So does the Node Editor while it is
  // open. Only one of them holds edits (see openNodeEditor). Returns what
  // cannot be saved, or null.
  function saveUnapplied() {
    commitFocusedField();

    const side = inspector.value?.saveUnapplied?.() || null;
    const dialogEdits = nodeEditor.value?.saveUnapplied?.() || null;

    return side || dialogEdits;
  }

  // What the commands (builder/commands.js) can do to this view.
  const commandView = {
    get editing() {
      return editing.value;
    },
    get draftsTab() {
      return editing.value ? '' : drafts.value?.activeTab || '';
    },
    get showMinimap() {
      return showMinimap.value;
    },
    get showNodeIcons() {
      return editorSettings.showNodeIcons;
    },
    get showNodeDescriptions() {
      return editorSettings.showNodeDescriptions;
    },
    get showConnections() {
      return editorSettings.showConnections;
    },
    get nodeIconSize() {
      return editorSettings.nodeIconSize;
    },
    get snapToGrid() {
      return editorSettings.snapToGrid;
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
    get icons() {
      return iconLibrary.state.icons;
    },
    openDialog,
    openPalette({ query = '', command = '' } = {}) {
      paletteRequest.value = { query, command };
      dialog.value = 'commands';
    },
    openShortcuts() {
      dialog.value = 'shortcuts';
    },
    openSettings,
    openLayers,
    openHistory,
    openLanding,
    startBlank,
    openDraft,
    duplicateDraft,
    combineIncluded,
    openExperiment: () => openExperiment(),
    closeEditor,
    openTemplateLibrary,
    openIcons,
    showDraftsTab(id) {
      drafts.value?.showTab(id);
    },
    toggleMinimap() {
      showMinimap.value = !showMinimap.value;
    },
    // A setting, kept as the Settings dialog keeps it. The icons only show
    // or hide, so the change is announced. The document keeps its icons.
    toggleNodeIcons() {
      const show = !editorSettings.showNodeIcons;

      setEditorSetting('showNodeIcons', show);
      store.announce(show ? 'Node icons shown.' : 'Node icons hidden.');
    },
    // A setting, as toggleNodeIcons. The document keeps its descriptions.
    toggleNodeDescriptions() {
      const show = !editorSettings.showNodeDescriptions;

      setEditorSetting('showNodeDescriptions', show);
      store.announce(
        show ? 'Node descriptions shown.' : 'Node descriptions hidden.',
      );
    },
    // A setting, as toggleNodeIcons. The document keeps its connections.
    toggleConnections() {
      const show = !editorSettings.showConnections;

      setEditorSetting('showConnections', show);
      store.announce(show ? 'Connections shown.' : 'Connections hidden.');
    },
    // A setting, as toggleNodeIcons. The document keeps its sizes.
    setNodeIconSize(size) {
      if (setEditorSetting('nodeIconSize', size)) {
        store.announce(iconSizeMessage(size));
      }
    },
    // A setting, as toggleNodeIcons. Nothing on the canvas shows the
    // change, so it is announced.
    toggleSnapToGrid() {
      const snap = !editorSettings.snapToGrid;

      setEditorSetting('snapToGrid', snap);
      store.announce(snap ? 'Snap to grid is on.' : 'Snap to grid is off.');
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
    focusInspector,
    showNode(id, options) {
      canvas.value?.showNode(id, options);
    },
    visibleArea() {
      return canvas.value?.visibleArea?.() || null;
    },
    revealNode(id, options) {
      canvas.value?.revealNode?.(id, options);
    },
    settleEdits,
  };

  const commandContext = createCommandContext({ store, view: commandView });

  // The toolbar and the command palette run commands in this context.
  provide('builderCommands', commandContext);

  // Every shortcut of the view, on the landing and in the editor, goes
  // through the one dispatcher. See SCOPES in commands.js for where each
  // works. Controls with keys of their own (canvas items, the toolbar's
  // arrows, dialogs) handle those first.
  function onGlobalKeydown(event) {
    if (!store.canUseBuilder) {
      return;
    }

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

    // A role without the Builder permission sees the notice only, and the
    // server would refuse every read.
    if (!store.canUseBuilder) {
      return;
    }

    const link = {
      draft: String(route.query.draft || ''),
      topology: String(route.query.topology || ''),
    };

    await Promise.all([store.fetchSchema(), refresh(), store.fetchSources()]);

    // The view was left meanwhile: the link is not for the page now shown.
    if (!rootEl.value) {
      return;
    }

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
    // ELK's worker stays while the Builder is open (see layouts/elk.js).
    stopLayoutEngine();
  });
</script>

<style scoped>
  /* One row, left to right: the Drafts button, the name, the resource
     totals, the counts, then the actions at the right edge. A narrower
     window wraps it. It is the container its buttons' labels follow (see
     below). */
  .builder-header {
    container: builder-header / inline-size;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem 0.75rem;
  }

  /* The Drafts button, the name and the totals wrap as the header's
     own items do, until the header is wide enough for one row. Until then
     the counts are centered in the room their row leaves them: on a row
     of their own, centered on the header. */
  .builder-header__start {
    display: contents;
  }

  .builder-header > .builder-counts {
    margin-inline: auto;
  }

  /* Wide enough for one row (from 66rem): the start and the actions take equal
     shares, which centers the counts (see builder/headerLabels.js). */
  @container builder-editor (min-width: 66rem) {
    .builder-header__start {
      display: flex;
      flex: 1 1 0;
      align-items: center;
      gap: 0.75rem;
    }

    /* The start shrinks to the Drafts button whole, an 8rem name and the
       totals, as the header's own items do in a narrower header. Then the
       header wraps. */
    .builder-header__start > .builder-button {
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
     down to 8rem to stay beside the Drafts button in a narrow window. */
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

  .builder-no-access h1 {
    font-size: 1.2rem;
    font-weight: 700;
    margin: 0 0 0.75rem;
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
