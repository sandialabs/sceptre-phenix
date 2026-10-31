<!-- Inspector: a schema-driven form (see adapters/forms.js) that edits a working
     copy until Apply. TemplateDialog and NodeEditorDialog mount it on their own
     host and call settle() from their Save or Apply. -->
<template>
  <section
    ref="panel"
    class="builder-inspector"
    :class="
      side
        ? 'builder-panel'
        : ['builder-inspector--wide', `builder-inspector--${variant}`]
    "
    :aria-labelledby="labelledby"
    @pointerdown="onPress">
    <!-- Read, not shown: it names the section, screen readers find it in
         the headings, and focus moves to it (see REGION_FOCUS in
         commands.js). -->
    <h2
      v-if="side"
      id="inspector-title"
      ref="heading"
      class="builder-inspector__title builder-visually-hidden"
      tabindex="-1">
      Inspector
    </h2>

    <p v-if="!target" class="builder-inspector__empty">
      Select a node or connection to edit its properties.
    </p>

    <template v-else>
      <!-- Which schema the form comes from is said only when it is not the
           server's, in the schema error below. -->
      <!-- Add connection and Move to group open their dialogs on the
           selection, each while its command can run for this node. Open
           Node Editor, the same size and in the accent color, shows the
           node's fields in a large dialog. Each tooltip names the button's
           keys. -->
      <div v-if="side" class="builder-inspector__head">
        <p class="builder-inspector__subject">
          {{ target.title }}
        </p>
        <div
          v-if="selection.type === 'node'"
          class="builder-inspector__head-actions">
          <button
            v-for="action in headActions"
            :key="action.command"
            type="button"
            class="builder-button builder-inspector__head-button"
            :aria-label="action.name"
            aria-haspopup="dialog"
            :aria-keyshortcuts="ariaShortcuts(action.command)"
            :data-testid="`inspector-${action.event}`"
            v-on="
              editorTip.tipEvents(() =>
                withShortcut(action.name, action.command),
              )
            "
            @click="runHeadAction(action)">
            <builder-icon :name="action.icon" :size="22.4" />
          </button>
          <button
            type="button"
            class="builder-button builder-button--primary builder-inspector__head-button builder-inspector__open-editor"
            aria-label="Open Node Editor"
            aria-haspopup="dialog"
            :aria-keyshortcuts="ariaShortcuts('node.edit')"
            data-testid="inspector-node-editor"
            v-on="
              editorTip.tipEvents(() =>
                withShortcut('Open Node Editor', 'node.edit'),
              )
            "
            @click="openEditor">
            <builder-icon name="pencil" :size="22.4" />
          </button>
        </div>
        <builder-fixed-tooltip
          :tooltip="editorTip"
          testid="inspector-head-tooltip" />
      </div>

      <!-- The errors and warnings of what the Inspector shows, as applied.
           The checks button in the header lists the whole diagram's. An
           issue about one of the form's fields has Go to, which focuses
           that field. -->
      <div
        v-if="!template && ownIssues.length"
        class="builder-inspector__issues"
        :data-level="ownCounts.errors ? 'error' : 'warning'"
        data-outline="#checks"
        data-outline-label="Checks"
        :data-outline-errors="ownCounts.errors ? 'true' : undefined"
        data-testid="inspector-checks">
        <h3 tabindex="-1" data-outline-focus>
          Checks: {{ countsText(ownCounts) }}
        </h3>
        <ul>
          <li
            v-for="(issue, index) in ownIssues"
            :key="`${issue.path}-${index}`"
            :data-level="issue.level">
            <builder-icon
              :name="issue.level === 'error' ? 'close' : 'warning'"
              :size="12" />
            <span class="builder-inspector__issue-text">
              <strong>
                {{ issue.level === 'error' ? 'Error' : 'Warning' }}:
              </strong>
              {{ issue.text }}
            </span>
            <button
              v-if="issue.field"
              type="button"
              class="builder-button builder-inspector__issue-go"
              :aria-label="`Go to ${issue.fieldName}: ${issue.text}`"
              data-testid="inspector-check-go-to"
              @click="goToField(issue.field)">
              Go to
            </button>
          </li>
        </ul>
      </div>

      <!-- Why fields below cannot change: a device from an included
           topology, or a network one is on (see inspectorLock). An included
           device can be changed in a copy of the diagram, which a role that
           may create drafts can make from here. -->
      <p
        v-if="!template && lock.note"
        :id="`${idPrefix}-included-note`"
        class="builder-inspector__note"
        data-testid="inspector-included-note">
        {{ lock.note }}
      </p>
      <!-- While the new draft is made, a ring turns over its label. -->
      <builder-busy-button
        v-if="!template && lock.all"
        class="builder-inspector__combine"
        :aria-describedby="`${idPrefix}-included-note`"
        :busy="Boolean(busyTask(BUSY_GROUPS.combine))"
        data-testid="inspector-combine"
        @click="$emit('combine')">
        Combine into a new draft
      </builder-busy-button>

      <p
        v-if="host.schemaError"
        class="builder-inspector__schema-error"
        role="alert"
        data-testid="inspector-schema-error">
        {{ host.schemaError }}
      </p>

      <p v-if="schema.required?.length" class="builder-inspector__hint">
        Fields marked * need a value.
      </p>

      <!-- novalidate: the browser's bound check would stop Enter from applying.
           autocomplete off: Firefox autofill scrolled the field away from the pointer. -->
      <form
        ref="form"
        novalidate
        autocomplete="off"
        @submit.prevent="apply"
        @focusin="onFieldFocus"
        @input="onFieldInput"
        @change="onFieldChange"
        @keydown="onFieldKey"
        @focusout="onFieldBlur">
        <json-forms
          :key="formKey"
          :data="draft"
          :schema="schema"
          :uischema="uiSchema"
          :renderers="renderers"
          :ajv="validator"
          :i18n="i18n"
          :readonly="host.readOnly || lock.all"
          validation-mode="NoValidation"
          :additional-errors="shownValidation.errors"
          :middleware="middleware"
          @change="onChange" />

        <div
          v-if="shownErrors.length"
          :key="errorSummary"
          class="builder-inspector__errors"
          role="alert">
          <p data-testid="inspector-errors">
            {{ shownErrors.length }}
            {{ shownErrors.length === 1 ? 'field needs' : 'fields need' }}
            attention before you can
            {{ template ? 'save this template.' : 'apply these changes.' }}
          </p>
          <ul data-testid="inspector-error-list">
            <li v-for="error in shownErrors" :key="error.path">
              <button
                type="button"
                class="builder-inspector__error-link"
                @click="focusField(error.path)">
                {{ error.message }}
              </button>
            </li>
          </ul>
        </div>

        <!-- Shown only while there are changes to apply or cancel, or
             focus is on one of these. It sticks to the bottom of the
             scrolling panel, so Apply is in view from any field of a long
             form. The panel's scroll padding, and uncover, keep a focused
             field clear of it (see builder.css). -->
        <div
          v-if="pending"
          ref="actions"
          class="builder-inspector__actions"
          :data-state="
            shownErrors.length ? 'error' : changed ? 'changed' : 'none'
          "
          data-testid="inspector-actions">
          <span class="builder-inspector__state">
            <builder-icon v-if="shownErrors.length" name="close" :size="14" />
            {{ stateText }}
          </span>
          <span class="builder-inspector__buttons">
            <!-- aria-disabled rather than disabled: Apply keeps keyboard
                 focus while fields need fixing, and a click that lands while
                 a field is still committing is not lost. -->
            <button
              ref="applyButton"
              type="submit"
              class="builder-button builder-button--primary"
              data-testid="inspector-apply"
              :aria-disabled="!canApply">
              <builder-icon name="check" :size="14" />
              Apply
            </button>
            <button
              type="button"
              class="builder-button"
              data-testid="inspector-cancel"
              @click="cancel">
              <builder-icon name="close" :size="14" />
              Cancel
            </button>
          </span>
        </div>
      </form>

      <!-- The diagram's annotations and scenario, read only, below its
           Name and Description. -->
      <inspector-diagram
        v-if="target.kind === 'document'"
        @scenario="$emit('scenario')"
        @layers="$emit('layers')" />

      <!-- The node's layer applies at once (see InspectorLayer.vue). The template
           editor and the Node Editor show it as a form field (INSPECTOR_LAYER). -->
      <inspector-layer
        v-if="layerSection"
        as="section"
        v-bind="layerState"
        @choose="chooseLayer"
        @use-matched="chooseLayer('')"
        @edit-layers="$emit('layers')" />

      <!-- Named apart from the node's own "Interfaces" list above: these
           act at once, while that list is part of the working copy. Its
           hint is a tooltip on its heading, shown on hover and on keyboard
           focus of its buttons. Screen readers read it after the heading,
           and as Add connection point's description. -->
      <div
        v-if="!template && target.kind === 'device'"
        class="builder-inspector__ifaces"
        data-outline="#connection-points"
        data-outline-label="Connection points"
        @focusin="ifacesTip?.onFocusIn($event, ifacesTitle)"
        @focusout="ifacesTip?.hide()">
        <h3
          ref="ifacesTitle"
          class="label--described"
          tabindex="-1"
          data-outline-focus
          @mouseenter="ifacesTip?.show(ifacesTitle, $event)"
          @mouseleave="ifacesTip?.scheduleHide()">
          Connection points
        </h3>
        <p :id="`${idPrefix}-ifaces-hint`" class="builder-visually-hidden">
          {{ ifacesHint }}
        </p>
        <ul v-if="target.interfaces.length" ref="interfaceList">
          <li v-for="handle in target.interfaces" :key="handle.id">
            <span class="builder-inspector__iface-name"
              >{{ handle.name }} — {{ networkFor(handle.id) }}</span
            >
            <span v-if="!lock.all" class="builder-inspector__iface-buttons">
              <!-- The connection goes, the connection point stays. -->
              <button
                v-if="connectionsOf(handle.id).length"
                type="button"
                class="builder-button"
                data-testid="inspector-disconnect"
                :aria-label="`Disconnect ${handle.name} from ${networkFor(handle.id)}`"
                :disabled="host.readOnly"
                @click="disconnect(handle.id)">
                Disconnect
              </button>
              <button
                type="button"
                class="builder-button builder-button--danger builder-inspector__iface-remove"
                :aria-label="`Remove connection point ${handle.name}`"
                :disabled="host.readOnly"
                @click="removeInterface(handle.id)">
                <builder-icon name="trash" :size="12" />
              </button>
            </span>
          </li>
        </ul>
        <p v-else class="builder-inspector__empty">No connection points.</p>
        <button
          v-if="!lock.all"
          ref="addInterfaceButton"
          type="button"
          class="builder-button"
          data-testid="inspector-add-interface"
          :aria-describedby="`${idPrefix}-ifaces-hint`"
          :disabled="host.readOnly"
          @click="host.addInterface(target.target.id, {})">
          <builder-icon name="plus" :size="14" />
          Add connection point
        </button>
        <inspector-tooltip ref="ifacesTip" :text="ifacesHint" />
      </div>

      <!-- The addresses of a switch's network. Assign addresses acts at
           once, as one Undo step, on the network as the diagram has it,
           not on the form's unapplied fields. Its hint says what it does,
           or why it is not available. -->
      <div
        v-if="addressesShown"
        class="builder-inspector__ifaces"
        data-testid="inspector-addresses">
        <h3>Addresses</h3>
        <p :id="`${idPrefix}-addresses-hint`" class="builder-inspector__empty">
          {{ addressesHint }}
        </p>
        <button
          type="button"
          class="builder-button"
          data-testid="inspector-assign-addresses"
          :aria-describedby="`${idPrefix}-addresses-hint`"
          :disabled="host.readOnly || !target.network?.subnet"
          @click="host.assignAddresses(target.network.id)">
          Assign addresses
        </button>
      </div>

      <!-- Below the fields and the connection points, which users look
           for more often. Its hint is a tooltip on its heading, shown on
           hover and on keyboard focus of its fields, and the fields'
           description. -->
      <form
        v-if="!template && selection.type === 'node' && target.target?.position"
        class="builder-inspector__position"
        :aria-labelledby="`${idPrefix}-position-title`"
        data-outline="#position"
        data-outline-label="Position"
        data-testid="inspector-position"
        novalidate
        @submit.prevent="move"
        @focusin="positionTip?.onFocusIn($event, positionTitle)"
        @focusout="positionTip?.hide()">
        <h3
          :id="`${idPrefix}-position-title`"
          ref="positionTitle"
          class="label--described"
          tabindex="-1"
          data-outline-focus
          @mouseenter="positionTip?.show(positionTitle, $event)"
          @mouseleave="positionTip?.scheduleHide()">
          Position
        </h3>
        <p :id="`${idPrefix}-position-hint`" hidden>{{ positionHint }}</p>
        <!-- Text fields read as numbers, like the whole-number fields (see
             InspectorInputControl): Firefox's number input took " 3 " for
             no number at all. No numeric keyboard: a phone's has no minus
             sign, and a position can be negative. Read only, not disabled,
             in a read-only draft, as the fields above are. -->
        <div class="builder-inspector__position-fields">
          <div v-for="axis in ['x', 'y']" :key="axis" class="builder-field">
            <label :for="`${idPrefix}-position-${axis}`">{{
              axis.toUpperCase()
            }}</label>
            <input
              :id="`${idPrefix}-position-${axis}`"
              v-model="position[axis]"
              type="text"
              role="spinbutton"
              autocomplete="off"
              :aria-valuenow="shownPosition(axis)"
              :aria-describedby="`${idPrefix}-position-hint`"
              :readonly="host.readOnly"
              :aria-readonly="host.readOnly || undefined"
              @keydown="stepPosition($event, axis)" />
          </div>
          <button
            type="submit"
            class="builder-button"
            data-testid="inspector-move"
            :aria-disabled="!canMove">
            Move
          </button>
        </div>
        <inspector-tooltip ref="positionTip" :text="positionHint" />
      </form>
    </template>
  </section>
</template>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    provide,
    readonly,
    ref,
    shallowRef,
    watch,
  } from 'vue';
  import { UPDATE_DATA } from '@jsonforms/core';
  import { JsonForms } from '@jsonforms/vue';

  import BuilderBusyButton from './BuilderBusyButton.vue';
  import BuilderFixedTooltip from './BuilderFixedTooltip.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import { useFixedTooltip } from './fixedTooltip.js';
  import InspectorDiagram from './inspector/InspectorDiagram.vue';
  import InspectorLayer from './inspector/InspectorLayer.vue';
  import InspectorTooltip from './inspector/InspectorTooltip.vue';
  import {
    INSPECTOR_COLLAPSE,
    useCollapseState,
  } from './inspector/collapse.js';
  import {
    INSPECTOR_ANNOUNCE,
    INSPECTOR_CHANGED,
    INSPECTOR_DEFAULTS,
    INSPECTOR_DESCRIPTION,
    INSPECTOR_DRAWN_COLOR,
    INSPECTOR_FIELD_NOTES,
    INSPECTOR_FIELD_WARNINGS,
    INSPECTOR_HEADING_LEVEL,
    INSPECTOR_ICON_LIBRARY,
    INSPECTOR_ICONS,
    INSPECTOR_INSERT_ITEM,
    INSPECTOR_LOCAL_PROBLEMS,
    INSPECTOR_LOCKED,
    INSPECTOR_NEW_ITEM,
    INSPECTOR_LAYER,
    INSPECTOR_RANDOM_MAC,
    INSPECTOR_RESETS,
    INSPECTOR_SUGGESTIONS,
    readNumberText,
  } from './inspector/control.js';
  import { heldCommit, keyEffect } from './inspector/heldCommit.js';

  import {
    applyFormData,
    descriptionPath,
    descriptionShownLabel,
    fieldChanged,
    fieldDefault,
    formDataChanged,
    insertedListItem,
    inspectorI18n,
    inspectorLock,
    inspectorName,
    inspectorRenderers,
    inspectorTarget,
    instantChangeLabel,
    instantKeys,
    instantValues,
    issueText,
    keepsWorkingCopy,
    mergeFormData,
    newListItem,
    rebasedWorkingCopy,
    relevantErrors,
    uiSchemaForKind,
  } from '@/builder/adapters/forms.js';
  import { documentMacs, macKey, randomMac } from '@/builder/addressing.js';
  import { count, listOf } from '@/builder/announce.js';
  import { BUSY_GROUPS, busyTask } from '@/builder/busy.js';
  import { drawnColor, drawnNetworkColor } from '@/builder/colors.js';
  import {
    ariaShortcuts,
    availability,
    focusLost,
    isTextEntry,
    withShortcut,
  } from '@/builder/commands.js';
  import {
    createFormValidator,
    fieldLabel,
    workingCopyErrors,
  } from '@/builder/form-validator.js';
  import { SAVED_UNAPPLIED } from '@/builder/history.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { resolveIcon } from '@/builder/icons.js';
  import {
    countsText,
    issueCounts,
    issueField,
    issuesAbout,
  } from '@/builder/issues.js';
  import {
    connectionChanges,
    effectiveLayers,
    findNetwork,
    findNode,
    ICON_SIZE_KINDS,
    LAYER_KINDS,
    moveNodes,
    nodeDescriptionHidden,
    nodeLayer,
    nodeLayerState,
    sameButStamp,
    setDescriptionHidden,
  } from '@/builder/model.js';
  import { schemaForKind } from '@/builder/schema.js';
  import { builderSettings, iconSizeOverrideNote } from '@/builder/settings.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    deviceFieldWarnings,
    interfaceNames,
    rulesetNames,
  } from '@/builder/validate.js';
  import { deepCopy } from '@/builder/values.js';

  // Each event asks the view (Builder.vue) to open a dialog or make a draft.
  const emit = defineEmits([
    'scenario',
    'combine',
    'layers',
    'edit',
    'connect',
    'regroup',
  ]);

  const props = defineProps({
    // What the Inspector reads and edits in place of the store: an object with
    // the store's members it uses. Its commit settles icons as the store's does
    // (see settleIcons in icons.js) and returns whether it took the document.
    host: { type: Object, default: null },
    // 'canvas', 'template' for the template editor, or 'editor' for the
    // Node Editor.
    variant: {
      type: String,
      default: 'canvas',
      validator: (value) => ['canvas', 'template', 'editor'].includes(value),
    },
    // The id of the heading that names the Inspector's section: its own,
    // which the template variant does not show.
    labelledby: { type: String, default: 'inspector-title' },
  });

  const host = props.host || useBuilderStore();
  const template = props.variant === 'template';
  const editor = props.variant === 'editor';
  // The Inspector beside the canvas, not one in a dialog.
  const side = !template && !editor;
  // The layer shows in its own section beside the canvas, else after the look fields.
  const layerPlace = side ? 'section' : 'look';
  // The ids of the parts the Inspector names. A dialog's Inspector is on
  // the page with the one beside the canvas, so each has its own.
  const idPrefix = side ? 'inspector' : template ? 'template' : 'node-editor';
  // The tooltip of the header's buttons, beside the canvas only.
  const editorTip = side ? useFixedTooltip({ side: 'start' }) : null;
  const renderers = inspectorRenderers;
  const validator = createFormValidator();

  // The form's path of an interface's VLAN (see fieldWarnings).
  const INTERFACE_VLAN_FIELD = /^spec\.network\.interfaces\.\d+\.vlan$/;

  provide(INSPECTOR_ANNOUNCE, (message) => host.announce(message));

  // Counts the times reset() reloads the form's data from the document
  // (Cancel, undo, another selection, and after Apply, a save of unapplied
  // edits or an edit on the canvas), so renderers that keep state of their
  // own, such as the oneOf kind picker, can tell data replaced from outside
  // the form from an edit made in it.
  const resets = ref(0);

  provide(INSPECTOR_RESETS, readonly(resets));

  // Problems renderers keep to themselves, by the path of the renderer that
  // reports them (see useInspectorLocalProblems): edits that are not in the
  // working copy yet, and cannot be until they are fixed.
  const localProblems = shallowRef({});

  provide(INSPECTOR_LOCAL_PROBLEMS, (path, found) => {
    const { [path]: previous, ...others } = localProblems.value;

    if (found.length > 0) {
      localProblems.value = { ...others, [path]: found };
    } else if (previous) {
      localProblems.value = others;
    }
  });

  const localErrors = computed(() => Object.values(localProblems.value).flat());

  const draft = ref({});
  // The element's data as the form loaded it: what the working copy's
  // edits are told from (see validation and applied).
  const loaded = shallowRef({});
  const dirty = ref(false);
  // A text field is being edited: it holds typed text that differs from what
  // it last committed, or focus has not settled since it was left.
  const typing = ref(false);
  // The text field being edited applies at once (see instantKeys), so the
  // text typed in it is no edit for Apply.
  const typingInstant = ref(false);
  // The text each field held when it took focus or last committed. Text
  // typed back to it is no change: the field sends no change event for it.
  const committedText = new WeakMap();
  // Focus is on Apply or Cancel, which then stay until used or left.
  const held = ref(false);
  // A press in the Inspector is in progress, from pointerdown until its
  // click is over (see onPress).
  const pressing = ref(false);
  const heading = ref();
  const panel = ref();
  const form = ref();
  const actions = ref();
  const applyButton = ref();
  const interfaceList = ref();
  const addInterfaceButton = ref();
  const positionTitle = ref();
  const positionTip = ref();
  const ifacesTitle = ref();
  const ifacesTip = ref();

  const selection = computed(() => host.inspectorSelection);
  const target = computed(() =>
    inspectorTarget(host.doc, selection.value, { template }),
  );
  const formKey = computed(
    () => `${selection.value.type}-${selection.value.id || 'document'}`,
  );

  function validationKey({ errors: found, fields }) {
    return JSON.stringify([
      found.map((error) => [
        error.instancePath,
        error.schemaPath,
        error.message,
      ]),
      fields,
    ]);
  }

  // The same object while the element's kind and spec variant stay (see
  // schemaForKind), so an edit elsewhere compiles nothing again.
  function schemaFor(element) {
    return schemaForKind(host.schema, element?.kind || 'document', {
      spec: element?.data?.spec,
      template,
    });
  }

  const schema = computed(() => schemaFor(target.value));
  const lock = computed(() => inspectorLock(host.doc, selection.value));

  // The working copy's errors that count (see workingCopyErrors): `errors`
  // for JSON Forms to show on their fields, and `fields`, the summary. Each
  // field shows one message, that of its most relevant error (see
  // relevantErrors), which the summary gives too (see fieldErrors).
  const validation = computed((previous) => {
    const found = target.value
      ? workingCopyErrors(validator, schema.value, draft.value, loaded.value)
      : { errors: [], fields: [] };
    const next = {
      errors: relevantErrors(found.errors),
      fields: found.fields,
    };

    // The same errors keep the same arrays, which JSON Forms does not
    // render again.
    return previous && validationKey(previous) === validationKey(next)
      ? previous
      : next;
  });
  const errors = computed(() => [
    ...validation.value.fields,
    ...localErrors.value,
  ]);

  // During a press, the checks shown stay as they were until the click ends: a
  // line that came or went above the button would move it from the pointer.
  const duringPress = (checks) =>
    computed((shown) => (pressing.value && shown) || checks.value);
  const shownValidation = duringPress(validation);
  const shownErrors = duringPress(errors);

  // The sections and list entries the user collapsed, kept for each
  // variant for the session (see collapse.js). A collapsed one marks the
  // errors of the fields in it.
  const collapse = useCollapseState(props.variant, () =>
    shownErrors.value.map((error) => error.path),
  );

  provide(INSPECTOR_COLLAPSE, collapse);

  // The node's fields have a heading (see InspectorGroupRenderer), under
  // the template editor's own h3 there.
  provide(INSPECTOR_HEADING_LEVEL, template ? 4 : 3);

  // An included device's fields, and every field of a read-only draft, are
  // locked rather than disabled, so they stay readable and reachable with
  // Tab, which shows their descriptions (see useInspectorLocked).
  provide(
    INSPECTOR_LOCKED,
    computed(() => lock.value.all || host.readOnly),
  );

  // A new interface added in the form is named and set up the way one drawn
  // on the canvas is (see newListItem).
  provide(INSPECTOR_NEW_ITEM, (path, data) =>
    newListItem(host.doc, selection.value, path, data ?? draft.value),
  );

  // An interface's MAC address field has a Generate button. The address it
  // generates is one that no interface of the diagram or of the form's data
  // uses.
  provide(INSPECTOR_RANDOM_MAC, () => {
    const typed = (draft.value?.spec?.network?.interfaces || [])
      .map((iface) => macKey(iface?.mac))
      .filter(Boolean);

    return randomMac(new Set([...documentMacs(host.doc), ...typed]));
  });

  // A point inserted in a line's Points list is a bend halfway along the
  // segment after the point it follows (see insertedListItem).
  provide(INSPECTOR_INSERT_ITEM, (path, index, data) =>
    insertedListItem(
      host.doc,
      selection.value,
      path,
      index,
      data ?? draft.value,
    ),
  );

  // What a field shows while it is not set (see fieldDefault): Memory the
  // megabytes phenix gives a VM, a connection's Label its network's name.
  provide(
    INSPECTOR_DEFAULTS,
    computed(() => {
      const current = target.value;

      return (path, fieldSchema) => fieldDefault(current, path, fieldSchema);
    }),
  );

  // Whether the working copy changed a field's value, or an entry of a map
  // field, for the mark on the field (see useFieldChanged and fieldChanged).
  // A template's fields have no mark: the mark says a change waits for
  // Apply, and the template editor has none.
  provide(
    INSPECTOR_CHANGED,
    computed(() => {
      const [now, was] = [draft.value, loadedData()];

      return (path, key) => !template && fieldChanged(now, was, path, key);
    }),
  );

  // A switch's Edge Color is its network's, which the canvas draws in the
  // theme's token when it is a color addNetwork picks. The picker's chip and
  // swatches show it the same way (see drawnNetworkColor). Its outline and
  // its fill, like every other color, are drawn as chosen.
  provide(
    INSPECTOR_DRAWN_COLOR,
    computed(() => {
      const network = target.value?.kind === 'switch';

      return (value, path) =>
        network && path === 'color'
          ? drawnNetworkColor(value)
          : drawnColor(value);
    }),
  );

  // The custom icons that the Icon field's Custom icon… item and its dialog
  // work with (see INSPECTOR_ICONS): a name resolves to the host's document's copy of the
  // icon, else to the server's icon library's (see resolveIcon).
  function iconEntry(name) {
    const icon = resolveIcon(name, host.doc.icons, iconLibrary);

    return icon ? { name, ...icon } : undefined;
  }

  provide(INSPECTOR_ICONS, {
    entry: iconEntry,
    diagram: () =>
      Object.entries(host.doc.icons || {}).map(([name, entry]) => ({
        name,
        data: entry?.data,
      })),
  });

  // The server's icon library, for the same dialog.
  provide(INSPECTOR_ICON_LIBRARY, iconLibrary);

  // The drive image field suggests the server's disk images, once known, an
  // interface's ruleset fields the names of the device's own rulesets, and
  // the ntp-server label and vrouter/enable-ssh annotation the names of its
  // interfaces, as the working copy has them.
  provide(
    INSPECTOR_SUGGESTIONS,
    computed(() => ({
      disks: host.disks,
      rulesets: rulesetNames(draft.value?.spec),
      interfaces: interfaceNames(draft.value?.spec),
    })),
  );

  // Warnings about the working copy's fields, which each field shows once
  // it commits its value, before Apply (see deviceFieldWarnings). A device
  // from an included topology is not this diagram's to fix, so its fields
  // get none, as in the diagram checks. A template is in no diagram, so an
  // interface's VLAN gets none for naming a network no diagram has.
  const fieldWarnings = computed(() => {
    if (target.value?.kind !== 'device' || lock.value.all) {
      return {};
    }

    const warnings = deviceFieldWarnings(host.doc, draft.value?.spec, {
      disks: host.disks,
      nodeId: target.value.target.id,
      hostname: draft.value?.hostname,
    });

    return template
      ? Object.fromEntries(
          Object.entries(warnings).filter(
            ([field]) => !INTERFACE_VLAN_FIELD.test(field),
          ),
        )
      : warnings;
  });

  provide(INSPECTOR_FIELD_WARNINGS, duringPress(fieldWarnings));

  // A note under Icon size while the viewer's Node icon size overrides it, and
  // under Description while the viewer's Show node descriptions is off.
  provide(
    INSPECTOR_FIELD_NOTES,
    computed(() => {
      const kind = target.value?.kind;
      const notes = {
        iconSize: ICON_SIZE_KINDS.includes(kind) ? iconSizeOverrideNote() : '',
      };
      const description = descriptionPath(kind);

      if (description && !builderSettings.showNodeDescriptions) {
        notes[description] =
          'Your canvas shows no descriptions, because your Show node descriptions setting is off.';
      }

      return (path) => notes[path] || '';
    }),
  );

  // The toggle beside the Description field of a device or a group. It
  // applies at once, as one Undo step, like the look fields.
  provide(
    INSPECTOR_DESCRIPTION,
    computed(() => {
      const path = descriptionPath(target.value?.kind);
      const node = target.value?.target;

      return path && node
        ? {
            path,
            shown: !nodeDescriptionHidden(node),
            locked: Boolean(host.readOnly || lock.value.all),
            set: setDescriptionShown,
          }
        : null;
    }),
  );

  function setDescriptionShown(shown) {
    const element = target.value;

    if (!element?.target || host.readOnly || lock.value.all) {
      return;
    }

    const next = setDescriptionHidden(host.doc, element.target.id, !shown);

    if (next !== host.doc) {
      host.commit(next, descriptionShownLabel(element.title, shown));
    }
  }

  const uiSchema = computed(() =>
    uiSchemaForKind(host.schema, target.value?.kind || 'document', {
      spec: target.value?.data?.spec,
      readonly: lock.value.fields,
      template,
      layer: layerPlace === 'look' && LAYER_KINDS.includes(target.value?.kind),
    }),
  );
  const i18n = computed(() => inspectorI18n(schema.value));

  // Apply and Cancel appear with the first keystroke, not on commit, so the
  // Inspector does not shift under a click elsewhere (see onFieldBlur).
  const changed = computed(
    () =>
      dirty.value ||
      (typing.value && !typingInstant.value) ||
      localErrors.value.length > 0,
  );
  const pending = computed(
    () => side && (changed.value || held.value) && !host.readOnly,
  );

  const canApply = computed(
    () => changed.value && !host.readOnly && errors.value.length === 0,
  );

  const errorSummary = computed(() =>
    shownErrors.value.map((error) => error.message).join('\n'),
  );

  const stateText = computed(() => {
    if (shownErrors.value.length > 0) {
      return 'Fix the fields marked with errors';
    }

    return changed.value ? 'Unapplied changes' : 'No changes to apply';
  });

  // The state line is not a live region, because a second region that changes
  // with the Builder's one is often not read. Unapplied edits are announced late.
  const UNAPPLIED_DELAY_MS = 400;
  let unappliedTimer = null;

  watch(dirty, (now) => {
    clearTimeout(unappliedTimer);

    if (now && !host.readOnly && !template) {
      unappliedTimer = setTimeout(() => {
        if (dirty.value) {
          host.announce('Unapplied changes.');
        }
      }, UNAPPLIED_DELAY_MS);
    }
  });

  onBeforeUnmount(() => clearTimeout(unappliedTimer));

  // What the form is editing: the selection and title it was opened for, so
  // a selection change can settle unapplied edits on the element they were
  // made on.
  let editing = null;
  // The field edited last, and its data path in case a re-render replaces
  // it, for focus to return to when Apply or Cancel disappears.
  let lastEdited = null;

  // Look fields (instantKeys) are presentation only, so each change applies
  // without Apply as one edit. A choice stepped to with keys commits when it is
  // made (see heldCommit, onFieldKey, holdLook and commitLook).
  const look = heldCommit(commitLook);
  // The look fields that are selects, whose keys step through the choices.
  const HELD_LOOK_PATHS = ['iconSize', 'lineStyle', 'borderStyle'];
  // A look was committed in the current task: Enter in a color's text
  // field commits the color and, where the browser submits the form for it
  // (Firefox does, before the field's change), asks to apply what is left.
  let lookJustCommitted = false;

  onBeforeUnmount(() => look.flush());

  function reset() {
    const data = target.value ? deepCopy(target.value.data) : {};

    draft.value = deepCopy(data);
    loaded.value = data;
    unsent = null;
    dirty.value = false;
    typing.value = false;
    resets.value += 1;
    editing = target.value
      ? { selection: { ...selection.value }, title: target.value.title }
      : null;
  }

  // The errors that keep the working copy from being applied to the element
  // it was made on, which the form may no longer show.
  function failing(element) {
    return [
      ...workingCopyErrors(
        validator,
        schemaFor(element),
        draft.value,
        loaded.value,
      ).fields,
      ...localErrors.value,
    ];
  }

  // The document with the working copy's edits applied to the element they
  // were made on, merged into it as it is now (see mergeFormData). A switch
  // that a changed VLAN adds is on the grid while Snap to grid is on.
  function applied(element) {
    return applyFormData(
      host.doc,
      editing.selection,
      mergeFormData(loaded.value, draft.value, element.data),
      { snap: builderSettings.snapToGrid },
    );
  }

  // Unapplied edits never vanish silently when the selection changes. Valid
  // edits are applied to the element they were made on, merged into it as it
  // is now. Edits with errors are discarded, and the announcement says which
  // happened. Nothing is applied while a redo is pending, so an undo is not
  // undone by it. A held look goes first, to the device it was chosen for.
  function settleUnapplied() {
    look.flush();

    if (
      !(dirty.value || localErrors.value.length > 0) ||
      !editing ||
      host.readOnly
    ) {
      return;
    }

    const previous = inspectorTarget(host.doc, editing.selection);

    if (!previous) {
      return;
    }

    // The form shows the new selection by now.
    const found = failing(previous);

    if (found.length > 0) {
      host.announce(
        `Discarded unapplied changes to ${editing.title}: ${found.length === 1 ? 'a field has' : 'fields have'} errors.`,
      );

      return;
    }

    if (host.canRedo) {
      host.announce(`Discarded unapplied changes to ${editing.title}.`);

      return;
    }

    const next = applied(previous);

    host.commit(
      next,
      appliedLabel(`Applied changes to ${editing.title}`, next, editing),
    );
  }

  // Applies valid unapplied edits before a save, also with a redo pending.
  // Returns why edits stay unapplied, or ''.
  function settle() {
    look.flush();

    if (
      !(dirty.value || localErrors.value.length > 0) ||
      !editing ||
      host.readOnly
    ) {
      return '';
    }

    const previous = inspectorTarget(host.doc, editing.selection);

    if (!previous) {
      return `${editing.title} is no longer in the diagram`;
    }

    const found = failing(previous);

    if (found.length > 0) {
      // The collapsed sections and entries with the errors expand, so the
      // fields that need attention show.
      found.forEach((error) => collapse.reveal(error.path));

      return found.length === 1
        ? '1 field needs attention'
        : `${found.length} fields need attention`;
    }

    const next = applied(previous);

    // A commit refused while a conflict is resolved leaves them unapplied.
    if (
      !host.commit(
        next,
        appliedLabel(`Applied changes to ${editing.title}`, next, editing),
      )
    ) {
      return 'the conflict is not resolved';
    }

    // The form shows the element as the document has it (see apply).
    reset();

    return '';
  }

  // Applies valid unapplied edits as one SAVED_UNAPPLIED edit before the diagram
  // is left or read whole (see leave.js). It is synchronous, because a reload
  // cannot wait. Returns {title, fields} for edits it cannot apply, or null.
  function saveUnapplied() {
    catchUp();
    look.flush();

    const element =
      editing && !host.readOnly
        ? inspectorTarget(host.doc, editing.selection)
        : null;

    if (!element) {
      return null;
    }

    const edited = dirty.value || localErrors.value.length > 0;
    const found = edited ? failing(element) : [];
    const blocked = [
      ...found.map((error) => fieldName(element, error.path)),
      ...unreadablePosition(),
    ];
    let next = edited && found.length === 0 ? applied(element) : null;

    // The Position fields are the selected node's, which the form is
    // editing too.
    if (canMove.value && editing.selection.id === target.value?.target?.id) {
      next = moveNodes(next || host.doc, [
        { id: editing.selection.id, position: { ...moveTo.value } },
      ]);
    }

    // A commit refused while a conflict is resolved leaves them unapplied.
    if (
      next &&
      host.commit(
        next,
        appliedLabel(`${SAVED_UNAPPLIED} to ${editing.title}`, next, editing),
      ) &&
      edited &&
      found.length === 0
    ) {
      // The form shows the element as the document has it (see apply).
      // The focused field has committed its text (see saveUnapplied in
      // Builder.vue), and catchUp took it.
      reset();
    }

    return blocked.length > 0
      ? { title: editing.title, fields: [...new Set(blocked)] }
      : null;
  }

  // A field's name for a list of those that need fixing: "Memory", or
  // "MAC address (Interface 1)".
  function fieldName(element, path) {
    const { label, context } = fieldLabel(schemaFor(element), path);

    return context ? `${label} (${context})` : label;
  }

  // The Position fields holding text that is no number.
  function unreadablePosition() {
    return ['x', 'y']
      .filter(
        (axis) =>
          position.value[axis].trim() !== '' &&
          !Number.isFinite(typedPosition.value[axis]),
      )
      .map((axis) => `Position ${axis.toUpperCase()}`);
  }

  // JSON Forms takes a field's change at once, and sends its data to
  // onChange on its next update. A reload does not wait for that: the data
  // it holds and has not sent is kept here (see middleware) and taken now.
  let unsent = null;

  function middleware(state, action, reducer) {
    const next = reducer(state, action);

    if (action.type === UPDATE_DATA) {
      unsent = next.data;
    }

    return next;
  }

  function catchUp() {
    if (unsent) {
      onChange({ data: unsent });
    }
  }

  // What applying edits says, and undoes as: the edit, and each connection
  // an interface VLAN it set made, moved or removed (see connectByVLAN).
  function appliedLabel(label, next, { selection: applied }) {
    return listOf([
      label,
      ...(applied.type === 'node'
        ? connectionChanges(host.doc, next, applied.id)
        : []),
    ]);
  }

  watch(
    formKey,
    () => {
      settleUnapplied();
      reset();
      lastEdited = null;
    },
    { immediate: true },
  );

  // The document changed under the form. The form reloads, or rebases a working
  // copy that holds something of the user's (see keepsWorkingCopy and
  // rebasedWorkingCopy). A change of the save stamp alone changes nothing.
  function onDocumentChange(next, previous) {
    if (sameButStamp(next, previous)) {
      return;
    }

    if (
      !keepsWorkingCopy({
        dirty: dirty.value,
        typing: typing.value,
        held: held.value,
        holding: look.held !== null,
        unsent: unsent !== null,
        problems: localErrors.value.length,
      })
    ) {
      reset();

      return;
    }

    // A change JSON Forms holds is taken first, so the move keeps it.
    catchUp();

    const element = target.value;
    const same =
      editing?.selection.type === selection.value.type &&
      editing?.selection.id === selection.value.id;

    // Another element's form is opened by the selection's watch.
    if (!element || !same) {
      return;
    }

    const rebased = rebasedWorkingCopy(loaded.value, draft.value, element.data);

    if (rebased) {
      loaded.value = rebased.loaded;
      draft.value = rebased.draft;
      dirty.value = formDataChanged(draft.value, loadedData());
    }

    // A rename made elsewhere names the element in what settling says.
    editing = { selection: { ...selection.value }, title: element.title };
  }

  watch(() => host.doc, onDocumentChange);

  // The form's errors come from its validation, not from JSON Forms, which
  // does not validate (see the template).
  function onChange(event) {
    if (!event) {
      return;
    }

    unsent = null;

    const next = JSON.stringify(event.data ?? {});

    if (next === JSON.stringify(draft.value)) {
      return;
    }

    const warned = fieldWarnings.value;
    const before = draft.value;

    draft.value = event.data;
    announceWarnings(warned, fieldWarnings.value);
    holdLook(event.data, before);
    dirty.value = formDataChanged(event.data, loadedData());

    // Edits typed back leave nothing to apply, so the form shows the element
    // as it is now, which an edit elsewhere may have changed meanwhile.
    if (!dirty.value && formDataChanged(target.value?.data, loaded.value)) {
      reset();
    }
  }

  // A field's warning shows under it as the field commits its value, which
  // leaves focus where it was, so its new description would not be read:
  // each new warning is announced with the field's label.
  function announceWarnings(before, after) {
    for (const [path, messages] of Object.entries(after)) {
      const label = form.value
        ?.querySelector(`[data-path="${CSS.escape(path)}"] > label`)
        ?.textContent.replace(/\s*\*$/, '')
        .trim();

      for (const message of messages) {
        if (!before[path]?.includes(message)) {
          host.announce(
            label ? `Warning for ${label}: ${message}` : `Warning: ${message}`,
          );
        }
      }
    }
  }

  // Commits the look fields that this form change set (compared with `before`),
  // so a look set elsewhere is not put back. Holds a choice stepped to with
  // keys. A value with an error does not reach the element.
  function holdLook(data, before) {
    const current = target.value;
    const keys = instantKeys(current?.kind);

    if (keys.length === 0 || host.readOnly || lock.value.all) {
      return;
    }

    const [chosen, shown] = [
      instantValues(current.kind, data),
      instantValues(current.kind, before),
    ];
    const failing = new Set(validation.value.fields.map((error) => error.path));
    const changed = keys.filter(
      (key) => chosen[key] !== shown[key] && !failing.has(key),
    );

    if (changed.length === 0) {
      return;
    }

    look.change({
      selection: { ...selection.value },
      change: {
        ...look.held?.change,
        ...Object.fromEntries(changed.map((key) => [key, chosen[key]])),
      },
    });
  }

  // A key on a look select, such as Icon size: one that steps it holds the
  // choice it steps to, and any other, such as Enter or a shortcut, commits
  // the choice held first. Escape in a look text field, such as a line's
  // Label, puts back the text it committed last.
  function onFieldKey(event) {
    const field = event.target;

    if (event.key === 'Escape' && restoreText(field)) {
      event.preventDefault();
      event.stopPropagation();

      return;
    }

    const path = field.closest?.('[data-path]')?.dataset.path;

    if (
      field.tagName !== 'SELECT' ||
      !HELD_LOOK_PATHS.includes(path) ||
      !instantKeys(target.value?.kind).includes(path)
    ) {
      return;
    }

    const effect = keyEffect(event);

    if (effect === 'hold') {
      look.key();
    } else if (effect === 'flush') {
      look.flush();
    }
  }

  // Puts back the text a look text field committed last (see
  // instantKeys), and says whether there was other text to take back.
  function restoreText(field) {
    const path = field.closest?.('[data-path]')?.dataset.path;

    if (
      !isTextEntry(field) ||
      !form.value?.contains(field) ||
      !instantKeys(target.value?.kind).includes(path) ||
      !committedText.has(field) ||
      field.value === committedText.get(field)
    ) {
      return false;
    }

    field.value = committedText.get(field);
    field.dispatchEvent(new Event('input', { bubbles: true }));

    return true;
  }

  // Gives the element the look fields chosen for it, unless it has them
  // already, as an edit named after the first field it changes (see
  // instantChangeLabel). Returns whether it did.
  function commitLook({ selection: chosenFor, change }) {
    const element = inspectorTarget(host.doc, chosenFor);

    if (!element || host.readOnly) {
      return false;
    }

    const has = instantValues(element.kind, element.data);
    const label = instantChangeLabel(element.kind, element.title, has, {
      ...has,
      ...change,
    });

    if (!label) {
      return false;
    }

    // A commit refused while a conflict is resolved leaves the look in the
    // form, as an unapplied edit.
    if (
      !host.commit(
        applyFormData(host.doc, chosenFor, { ...element.data, ...change }),
        label,
      )
    ) {
      return false;
    }

    // The look is no unapplied edit of the element's now.
    if (
      editing?.selection.type === chosenFor.type &&
      editing?.selection.id === chosenFor.id
    ) {
      loaded.value = { ...loaded.value, ...change };
    }

    lookJustCommitted = true;
    setTimeout(() => {
      lookJustCommitted = false;
    });

    return true;
  }

  // The element's data as the form loaded it, counting a held look as
  // applied: it is no edit for Apply.
  function loadedData() {
    const data = loaded.value;

    return look.held ? { ...data, ...look.held.change } : data;
  }

  function remember(field) {
    const path = field.closest?.('[data-path]')?.dataset.path;

    if (path !== undefined) {
      lastEdited = { field, path };
    }
  }

  // A press in the Inspector, from pointerdown until its click is over. A
  // field it takes focus from settles only then, so nothing moves under the
  // click, and a click on Apply or Cancel lands although the field's change
  // left nothing to apply.
  let settleAfterPress = false;

  function onPress(event) {
    // An icon chosen with the pointer commits at once (see `look`).
    look.point();

    // A secondary button opens a menu, and may never send pointerup here.
    if (pressing.value || event.button !== 0) {
      return;
    }

    pressing.value = true;

    // A press that opens a menu (Control and a click, on macOS) ends there:
    // its pointerup may never come.
    const ends = ['pointerup', 'pointercancel', 'contextmenu'];
    const release = () => {
      ends.forEach((type) => {
        document.removeEventListener(type, release, true);
      });
      // The click follows pointerup in the same task.
      setTimeout(() => {
        pressing.value = false;

        if (settleAfterPress) {
          settleAfterPress = false;
          typing.value = false;
        }
      });
    };

    ends.forEach((type) => {
      document.addEventListener(type, release, true);
    });
  }

  function onFieldFocus(event) {
    const field = event.target;

    if (actions.value?.contains(field)) {
      held.value = true;
    } else if (isTextEntry(field)) {
      committedText.set(field, field.value);
    }

    // Once the browser has scrolled the field into view, if it does.
    requestAnimationFrame(() => uncover(field));
  }

  // Apply and Cancel's height, which grows when they wrap to two rows at
  // phone width, as they do while errors show, for the scroll padding that
  // keeps a field that takes focus clear of them (see builder.css). It is
  // written on the column the Inspector is in, which scrolls in the
  // three-column layout.
  const ACTIONS_HEIGHT = '--builder-inspector-actions';
  // Space kept between a field scrolled clear of them and their top.
  const UNCOVER_GAP = 8;
  let actionsObserver = null;

  function column() {
    return panel.value?.parentElement ?? null;
  }

  watch(
    actions,
    (bar) => {
      actionsObserver?.disconnect();
      actionsObserver = null;

      if (!bar) {
        column()?.style.removeProperty(ACTIONS_HEIGHT);

        return;
      }

      if (typeof ResizeObserver === 'function') {
        actionsObserver = new ResizeObserver(() => {
          column()?.style.setProperty(ACTIONS_HEIGHT, `${bar.offsetHeight}px`);
          uncover(document.activeElement);
        });
        actionsObserver.observe(bar);
      }
    },
    { flush: 'post' },
  );

  onBeforeUnmount(() => {
    actionsObserver?.disconnect();
    column()?.style.removeProperty(ACTIONS_HEIGHT);
  });

  // The element Apply and Cancel stick in: the nearest that scrolls.
  function scroller(element) {
    for (let at = element.parentElement; at; at = at.parentElement) {
      if (
        /(auto|scroll)/.test(getComputedStyle(at).overflowY) &&
        at.scrollHeight > at.clientHeight
      ) {
        return at;
      }
    }

    return null;
  }

  // The scroll padding keeps a field clear of Apply and Cancel only where
  // the browser scrolls it into view: not one in view but under them, nor
  // one they grow over. Such a field with focus is scrolled clear of them
  // (WCAG 2.4.11).
  function uncover(field) {
    const bar = actions.value;

    if (!bar || !form.value?.contains(field) || bar.contains(field)) {
      return;
    }

    const overlap =
      field.getBoundingClientRect().bottom - bar.getBoundingClientRect().top;

    if (overlap > 0) {
      scroller(bar)?.scrollBy({ top: overlap + UNCOVER_GAP });
    }
  }

  function onFieldInput(event) {
    const field = event.target;

    remember(field);

    if (isTextEntry(field)) {
      typing.value = field.value !== committedText.get(field);
      typingInstant.value = instantKeys(target.value?.kind).includes(
        field.closest?.('[data-path]')?.dataset.path,
      );
    }
  }

  // JSON Forms takes the committed value, and `dirty` then says whether
  // anything is left to apply. `typing` stays until focus has settled:
  // leaving the field settles it in onFieldBlur, and Enter in Apply. A
  // change that does neither, from a number field's arrows, settles here.
  function onFieldChange(event) {
    const field = event.target;

    remember(field);

    if (!isTextEntry(field)) {
      return;
    }

    committedText.set(field, field.value);
    setTimeout(() => {
      if (!pressing.value && document.activeElement === field) {
        typing.value = false;
      }
    });
  }

  // Focus leaving a field, Apply or Cancel. It follows the change a field
  // sends as it loses focus.
  function onFieldBlur(event) {
    const field = event.target;
    const next = event.relatedTarget;

    // A look select holds a choice stepped to with keys, and the choice is
    // made once focus leaves it.
    look.flush();

    // The window lost focus: the element gets it back, so nothing settles.
    if (!next && document.activeElement === field) {
      return;
    }

    // Tab from the last field, or a click, onto Apply or Cancel: they stay
    // for it, even when the change left nothing to apply. They do not
    // disappear from under focus and drop it on <body>.
    if (next && actions.value?.contains(next)) {
      held.value = true;
    } else if (actions.value?.contains(field)) {
      held.value = false;
    }

    if (!isTextEntry(field)) {
      return;
    }

    // Text other than what the field committed, with no change sent: its
    // value was replaced while it had focus, so the browser saw no edit.
    // Commit it, so the form holds what the field shows.
    if (
      field.value !== committedText.get(field) &&
      form.value?.contains(field)
    ) {
      field.dispatchEvent(new Event('change', { bubbles: true }));
    }

    if (pressing.value) {
      settleAfterPress = true;
    } else {
      typing.value = false;
    }
  }

  function focusable(field) {
    return (
      field?.isConnected &&
      form.value?.contains(field) &&
      !field.disabled &&
      field.offsetParent !== null
    );
  }

  // When Apply and Cancel go away, focus moves from them to the last edited
  // field or the heading (WCAG 2.4.3). Call the result after the action.
  function keepFocus() {
    const owned =
      focusLost() || Boolean(actions.value?.contains(document.activeElement));

    return async () => {
      await nextTick();

      if (!owned || actions.value?.contains(document.activeElement)) {
        return;
      }

      if (focusable(lastEdited?.field)) {
        lastEdited.field.focus();
      } else if (!(lastEdited && focusField(lastEdited.path))) {
        heading.value?.focus();
      }
    };
  }

  // A text field commits on change. Firefox submits the form on Enter before
  // that change fires. So this commits the focused field first, and lets
  // JSON Forms report the change, before it decides whether there is
  // anything to apply. Selects and checkboxes have committed already, and a
  // change sent to a select would read a "Not set" picker as a choice.
  async function commitFocusedField() {
    const field = document.activeElement;

    if (form.value?.contains(field) && isTextEntry(field)) {
      field.dispatchEvent(new Event('change', { bubbles: true }));
      await nextTick();
    }
  }

  async function apply(event) {
    // Buttons inside JSON Forms renderers are type="button". Anything else
    // that submits is not a request to apply.
    if (event?.submitter && event.submitter !== applyButton.value) {
      return;
    }

    const refocus = keepFocus();
    // A held look is an edit of its own, and says so.
    const flushed = look.flush();

    await commitFocusedField();

    // So is a color its text field has just committed, on the Enter that
    // asks for this.
    const lookChanged = flushed || lookJustCommitted;

    // Edits changed back leave nothing to apply. Committing the unchanged
    // element would still add an Undo step and a server snapshot. Edits a
    // renderer holds back until they are fixed are something to apply, which
    // their errors refuse.
    if (!dirty.value && localErrors.value.length === 0) {
      typing.value = false;
      held.value = false;

      if (!host.readOnly && !lock.value.all && !lookChanged) {
        host.announce('No changes to apply.');
      }

      await refocus();

      return;
    }

    if (!canApply.value) {
      return;
    }

    const next = applied(target.value);
    const warned = ownWarnings();

    // Named after the edit, so a rename says the new name.
    const took = host.commit(
      next,
      appliedLabel(`Updated ${inspectorName(next, selection.value)}`, next, {
        selection: selection.value,
      }),
    );

    // The form now shows the values the document took (VLAN "exp" as EXP).
    // Every field has committed its text, so no typed text is lost.
    if (took) {
      reset();
    }

    dirty.value = false;
    held.value = false;

    // The checks above the form list the warnings the edit brought, far
    // from Apply: they are announced after the edit.
    const added = ownWarnings().filter((text) => !warned.includes(text));

    if (added.length) {
      host.announce(
        `${count(added.length, 'new warning')}: ${added.join('. ')}`,
      );
    }

    await refocus();
  }

  function ownWarnings() {
    return ownIssues.value
      .filter((issue) => issue.level !== 'error')
      .map((issue) => issue.text);
  }

  // Cancel reached from a field has taken that field's change, so `dirty`
  // says whether there is anything to discard.
  async function cancel() {
    if (!pending.value) {
      return;
    }

    const refocus = keepFocus();

    // A look is applied without Apply, so Cancel keeps it.
    look.flush();

    const discarded = dirty.value || localErrors.value.length > 0;

    reset();
    held.value = false;
    host.announce(
      discarded ? 'Discarded unapplied changes.' : 'No changes to discard.',
    );
    await refocus();
  }

  // Focuses the innermost control of an error's data path, or the nearest field
  // that holds it, and opens collapsed sections. Returns that control, or null.
  function focusField(path) {
    const fields = [...(form.value?.querySelectorAll('[data-path]') || [])];
    const entries = [
      ...(form.value?.querySelectorAll('[data-entry-path]') || []),
    ];
    const segments = String(path).split('.');

    for (let length = segments.length; length > 0; length -= 1) {
      const scope = segments.slice(0, length).join('.');
      // A label or an annotation is also at the path of its name
      // (spec.labels.ntp-server), at its value.
      const field =
        fields.filter((element) => element.dataset.path === scope).pop() ||
        entries.find((element) => element.dataset.entryPath === scope);
      const control =
        field?.querySelector(
          'input:not([type="hidden"]):not(:disabled), select:not(:disabled), textarea:not(:disabled)',
        ) ||
        field?.querySelector('.array-list-add:not(:disabled)') ||
        field?.querySelector('button:not(:disabled)');

      if (control) {
        for (
          let section = control.closest('details');
          section && form.value?.contains(section);
          section = section.parentElement?.closest('details')
        ) {
          section.open = true;
        }

        // A collapsed section or entry with the field expands, and shows
        // the field once it is drawn again.
        if (collapse.reveal(control.closest('[data-path]')?.dataset.path)) {
          nextTick(() => control.focus());
        } else {
          control.focus();
        }

        return control;
      }
    }

    return null;
  }

  // Go to from a check: focuses the field, or the heading, and announces it with
  // the element. Returns whether a field took focus.
  function goToField(path) {
    const fields = [...(form.value?.querySelectorAll('[data-path]') || [])];
    const exact = fields.some((element) => element.dataset.path === path);
    const inside = exact
      ? null
      : fields.find((element) => element.dataset.path.startsWith(`${path}.`));
    const control = focusField(inside ? inside.dataset.path : path);
    const title = target.value?.title || '';
    const where = title && `${title[0].toLowerCase()}${title.slice(1)}`;

    if (!control) {
      heading.value?.focus();
      host.announce(`Selected ${where}`);

      return false;
    }

    const focused = control.closest('[data-path]')?.dataset.path ?? path;
    // A label or an annotation is named by its key, as "ntp-server (Labels)".
    const entry = control.closest('[data-entry-path]')?.dataset.entryPath;
    const map = control.closest('fieldset[data-path]')?.dataset.path ?? '';
    const name = entry?.startsWith(`${map}.`)
      ? `${entry.slice(map.length + 1)} (${fieldName(target.value, map)})`
      : fieldName(target.value, focused);

    host.announce(`${name} in ${where}`);

    return true;
  }

  // Takes a Go to request (see goToIssue in store.js) once the form shows its
  // element, also one made before the Inspector mounted. Each is taken once.
  async function takeFocusRequest() {
    const token = side ? host.focusRequest?.token || 0 : 0;

    if (!token || !host.focusRequest?.field) {
      return;
    }

    await nextTick();

    const request = host.takeFocusRequest?.(token);

    if (request && selection.value.id === request.id) {
      goToField(request.field);
    }
  }

  watch(() => (side ? host.focusRequest?.token || 0 : 0), takeFocusRequest, {
    flush: 'post',
  });

  onMounted(takeFocusRequest);

  const positionHint = computed(
    () =>
      `X and Y are in canvas pixels${target.value?.target?.parentId ? " inside the node's group" : ''}. Select Move to move the node without dragging it.`,
  );

  // The ids of the layer's select and hint (see idPrefix).
  const layerIds = {
    select: `${idPrefix}-layer-select`,
    hint: `${idPrefix}-layer-hint`,
  };

  // The layer of the device or switch shown: what InspectorLayer shows, in
  // its section or in the form (see layerPlace). The template editor's
  // layers are a copy of the diagram's, so it has no Edit layers….
  const layerState = computed(() => {
    const locked = Boolean(host.readOnly || lock.value.all);

    return {
      layer: nodeLayer(target.value?.target) ?? '',
      matched: nodeLayerState(host.doc, target.value?.target).matched,
      layers: effectiveLayers(host.doc),
      icons: host.doc?.icons || null,
      locked,
      ids: layerIds,
      editable: !template && !locked,
    };
  });
  const hasLayer = computed(() => LAYER_KINDS.includes(target.value?.kind));
  const layerSection = computed(
    () => layerPlace === 'section' && hasLayer.value,
  );

  provide(INSPECTOR_LAYER, {
    state: computed(() => (hasLayer.value ? layerState.value : {})),
    choose: (layer) => chooseLayer(layer),
    useMatched: () => chooseLayer(''),
    editLayers: () => emit('layers'),
  });

  // Puts the selected device or switch at the layer chosen, at once. The
  // select shows the node's layer again when the change is refused, such
  // as while a conflict is resolved.
  function chooseLayer(layer) {
    const node = target.value?.target;

    if (!node || host.readOnly || lock.value.all) {
      return;
    }

    host.setNodeLayer(node.id, layer);

    const select = panel.value?.querySelector(`#${layerIds.select}`);

    if (select) {
      select.value = nodeLayer(findNode(host.doc, node.id)) ?? '';
    }
  }

  // Open Node Editor: the node this Inspector shows.
  function openEditor() {
    editorTip.hideTip();
    emit('edit', selection.value.id);
  }

  // The header's Add connection and Move to group, beside the canvas: each
  // while its command can run (a read-only draft has neither), and only for
  // a node it acts on. Add connection joins a device or a switch, but not
  // a device of an included topology, which keeps its connections. Move to
  // group moves any node.
  const HEAD_ACTIONS = [
    {
      command: 'dialog.connect',
      event: 'connect',
      run: () => emit('connect'),
      name: 'Add connection',
      icon: 'link',
      kinds: ['device', 'switch'],
    },
    {
      command: 'dialog.regroup',
      event: 'regroup',
      run: () => emit('regroup'),
      name: 'Move to group',
      icon: 'group',
      kinds: null,
    },
  ];

  const headActions = computed(() => {
    if (!side || selection.value.type !== 'node') {
      return [];
    }

    const kind = target.value?.kind;
    const ctx = { store: host, view: { editing: true } };

    return HEAD_ACTIONS.filter(
      (action) =>
        (!action.kinds ||
          (action.kinds.includes(kind) &&
            !(action.event === 'connect' && lock.value.all))) &&
        availability(action.command, ctx) === true,
    );
  });

  function runHeadAction(action) {
    editorTip.hideTip();
    action.run();
  }

  const ifacesHint = computed(() =>
    lock.value.all
      ? 'These are the interfaces of this device, as its included topology defines them.'
      : 'These are the interfaces of this device. Each interface has a connection point on the canvas to connect from. When you add, disconnect or remove an interface, the change occurs at once, without Apply.',
  );

  // Assign addresses, under a switch's fields on the canvas (see
  // assignAddresses in the store).
  const addressesShown = computed(
    () =>
      !template &&
      target.value?.kind === 'switch' &&
      Boolean(target.value.network) &&
      typeof host.assignAddresses === 'function',
  );
  const addressesHint = computed(() => {
    const subnet = target.value?.network?.subnet;

    return subnet
      ? `Select Assign addresses to give each static interface on this network that has no address the next free address in ${subnet}.`
      : 'This network has no subnet. Apply a Subnet to assign addresses.';
  });

  // The text of the X and Y fields.
  const position = ref({ x: '0', y: '0' });
  const nodePosition = computed(() =>
    selection.value.type === 'node' ? target.value?.target?.position : null,
  );

  watch(
    () => [formKey.value, nodePosition.value?.x, nodePosition.value?.y],
    () => {
      position.value = {
        x: String(Math.round(nodePosition.value?.x ?? 0)),
        y: String(Math.round(nodePosition.value?.y ?? 0)),
      };
    },
    { immediate: true },
  );

  // The numbers typed, without the spaces around them. Undefined for text
  // that is not a number.
  const typedPosition = computed(() => ({
    x: readNumberText(position.value.x, false),
    y: readNumberText(position.value.y, false),
  }));

  // Where Move puts the node, in whole canvas pixels: a fraction typed in
  // a field reaches here.
  const moveTo = computed(() => ({
    x: Math.round(typedPosition.value.x),
    y: Math.round(typedPosition.value.y),
  }));

  const canMove = computed(
    () =>
      !host.readOnly &&
      Number.isFinite(typedPosition.value.x) &&
      Number.isFinite(typedPosition.value.y) &&
      (moveTo.value.x !== Math.round(nodePosition.value?.x ?? 0) ||
        moveTo.value.y !== Math.round(nodePosition.value?.y ?? 0)),
  );

  // The number a field shows, for its spin button role.
  function shownPosition(axis) {
    const shown = typedPosition.value[axis];

    return Number.isFinite(shown) ? shown : undefined;
  }

  // ArrowUp and ArrowDown step a field to the next whole pixel, and an
  // empty one from 0, as a number input's arrow keys did. Text that is no
  // number is left to the keys' usual work.
  function stepPosition(event, axis) {
    const up = event.key === 'ArrowUp';

    if (
      host.readOnly ||
      (!up && event.key !== 'ArrowDown') ||
      event.altKey ||
      event.ctrlKey ||
      event.metaKey
    ) {
      return;
    }

    const typed = position.value[axis].trim();
    const current = typed === '' ? 0 : readNumberText(typed, false);

    if (!Number.isFinite(current)) {
      return;
    }

    event.preventDefault();
    position.value = {
      ...position.value,
      [axis]: String(up ? Math.floor(current) + 1 : Math.ceil(current) - 1),
    };
  }

  function move() {
    if (!canMove.value) {
      return;
    }

    host.moveNodes([
      { id: target.value.target.id, position: { ...moveTo.value } },
    ]);
  }

  // The Remove buttons of the connection points, in order.
  function removeButtons() {
    return (
      interfaceList.value?.querySelectorAll(
        '.builder-inspector__iface-remove',
      ) || []
    );
  }

  // Keeps focus in the list: the next row's Remove button, the previous
  // row's when the last row went, or Add interface when none are left.
  async function removeInterface(handleId) {
    const index = target.value.interfaces.findIndex(
      (handle) => handle.id === handleId,
    );

    host.removeInterface(target.value.target.id, handleId);
    await nextTick();

    const buttons = removeButtons();
    const next = buttons[Math.min(index, buttons.length - 1)];

    (next || addInterfaceButton.value)?.focus();
  }

  // Removes a connection point's connections, as Delete on the canvas does,
  // and keeps the connection point, free to connect again. Focus moves from
  // Disconnect, which goes with them, to the row's Remove button.
  async function disconnect(handleId) {
    const index = target.value.interfaces.findIndex(
      (handle) => handle.id === handleId,
    );

    host.remove({
      nodes: [],
      edges: connectionsOf(handleId).map((edge) => edge.id),
    });
    await nextTick();

    (removeButtons()[index] || addInterfaceButton.value)?.focus();
  }

  function connectionsOf(handleId) {
    return (host.doc.edges || []).filter(
      (entry) =>
        entry.sourceHandleId === handleId || entry.targetHandleId === handleId,
    );
  }

  function networkFor(handleId) {
    const [edge] = connectionsOf(handleId);

    if (!edge) {
      return 'not connected';
    }

    const network = findNetwork(host.doc, edge.networkId);

    return network ? `network ${network.name}` : 'unknown network';
  }

  const issues = computed(() => host.issues);
  // Each with the form's field it is about, if any, and the field's name.
  const ownIssues = computed(() =>
    issuesAbout(host.doc, issues.value, selection.value).map((issue) => {
      const field =
        selection.value.type === 'document' ? '' : issueField(host.doc, issue);

      return {
        ...issue,
        field,
        fieldName: field ? fieldName(target.value, field) : '',
      };
    }),
  );
  const ownCounts = computed(() => issueCounts(ownIssues.value));

  // Announces diagram errors an edit introduces, which otherwise appear only
  // in the checks lists, often far from the edit. Opening another diagram is
  // not an edit, so its existing errors are not announced. The message goes
  // through the Builder's one live region, after the edit's own announcement.
  watch(
    () => ({
      id: host.doc?.metadata?.id,
      texts: issues.value
        .filter((issue) => issue.level === 'error')
        .map((issue) => issueText(host.doc, issue)),
    }),
    (now, before) => {
      const added =
        now.id === before?.id
          ? now.texts.filter((text) => !before.texts.includes(text))
          : [];

      if (added.length) {
        host.announce(
          `${count(added.length, 'new diagram error')}: ${added.join('. ')}`,
        );
      }
    },
  );

  // --- sections and list entries that collapse -------------------------

  // The sections, lists and entries that collapse, and the More settings
  // section, which opens and closes.
  function collapsibles() {
    return [...(panel.value?.querySelectorAll('[data-collapse-path]') || [])];
  }

  function sections() {
    return [...(panel.value?.querySelectorAll('details') || [])];
  }

  // Collapse all and Expand all, in the Node Editor's header.
  function collapseAll() {
    collapse.collapseAll(
      collapsibles().map((element) => element.dataset.collapsePath),
    );
    sections().forEach((section) => {
      section.open = false;
    });
  }

  function expandAll() {
    collapse.expandAll();
    sections().forEach((section) => {
      section.open = true;
    });
  }

  // The Node Editor's search expands all, then puts back what was
  // collapsed and closed (see NodeEditorDialog.vue).
  function collapsedState() {
    return {
      paths: collapse.collapsedPaths(),
      closed: sections().filter((section) => !section.open),
    };
  }

  function restoreCollapsed({ paths, closed }) {
    collapse.collapseAll(paths);
    sections().forEach((section) => {
      section.open = !closed.includes(section);
    });
  }

  // Expands an outline entry (data-outline), scrolls to it and focuses its
  // header. Returns whether the entry is in the form.
  async function reveal(key) {
    const entry = panel.value?.querySelector(
      `[data-outline="${CSS.escape(key)}"]`,
    );

    if (!entry) {
      return false;
    }

    for (
      let at = entry;
      at && panel.value?.contains(at);
      at = at.parentElement?.closest('[data-collapse-path], details')
    ) {
      if (at.dataset.collapsePath) {
        collapse.setCollapsed(at.dataset.collapsePath, false);
      } else if (at.tagName === 'DETAILS') {
        at.open = true;
      }
    }

    await nextTick();
    entry.scrollIntoView({ block: 'start' });
    (entry.querySelector('[data-outline-focus]') || entry).focus({
      preventScroll: true,
    });

    return true;
  }

  defineExpose({
    settle,
    saveUnapplied,
    errors,
    changed,
    collapseAll,
    expandAll,
    collapsedState,
    restoreCollapsed,
    reveal,
  });
</script>

<style scoped>
  /* The heading is visually hidden, so while it has focus the Inspector
     shows the focus indicator instead (WCAG 2.4.7). Only a script gives the
     heading focus, often after a shortcut such as Ctrl+A. Chromium does not
     match :focus-visible after a shortcut with Ctrl, so :focus is used. */
  .builder-inspector:has(> .builder-inspector__title:focus) {
    outline: 3px solid var(--bx-focus);
    outline-offset: -3px;
  }

  /* The subject, and the buttons at the right: Add connection and Move to
     group, then Open Node Editor. */
  .builder-inspector__head {
    display: flex;
    align-items: flex-start;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
  }

  .builder-inspector__head-actions {
    display: flex;
    flex: none;
    align-items: center;
    gap: 0.35rem;
  }

  .builder-inspector__subject {
    flex: 1 1 auto;
    min-width: 0;
    margin: 0;
    font-size: 0.85rem;
    overflow-wrap: anywhere;
  }

  /* The three header buttons are one size: 2.2rem square, at least 24 by
     24 pixels (WCAG 2.5.8). */
  .builder-inspector__head-button {
    flex: none;
    justify-content: center;
    min-width: 2.2rem;
    min-height: 2.2rem;
    padding: 0.28rem;
  }

  /* Open Node Editor is in the accent color. Forced colors drop the fill:
     a thicker border keeps it apart from the buttons beside it. */
  @media (forced-colors: active) {
    .builder-inspector__open-editor {
      border: 2px solid ButtonText;
    }
  }

  .builder-inspector__note {
    font-size: 0.78rem;
    margin: 0 0 0.5rem;
    padding-left: 0.5rem;
    border-left: 3px dashed var(--bx-border-strong);
  }

  .builder-inspector__combine {
    margin: 0 0 0.75rem;
  }

  /* Across the panel's width, over the fields that scroll under it, with a
     rule in the accent color (the danger color while fields need fixing)
     and a shadow that set it apart from them. */
  .builder-inspector__actions {
    position: sticky;
    bottom: 0;
    z-index: 20;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.4rem 0.6rem;
    margin: 0.5rem -0.6rem 0;
    padding: 0.45rem 0.6rem;
    border-top: 2px solid var(--bx-accent);
    background: var(--bx-surface);
    box-shadow: 0 -2px 6px rgba(16, 24, 40, 0.16);
  }

  .builder-inspector__actions[data-state='error'] {
    border-top-color: var(--bx-danger);
  }

  /* Stacked in a short window, they would cover the field with focus: they
     end the form instead (see builder.css). */
  @container builder-editor (max-width: 54.25rem) {
    @media (max-height: 24rem) {
      .builder-inspector__actions {
        position: static;
      }
    }
  }

  .builder-inspector__state {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-size: 0.8rem;
    font-weight: 700;
  }

  .builder-inspector__actions[data-state='error'] .builder-inspector__state {
    color: var(--bx-danger);
  }

  .builder-inspector__buttons {
    display: flex;
    gap: 0.4rem;
    margin-left: auto;
  }

  .builder-inspector__errors,
  .builder-inspector__schema-error {
    font-size: 0.78rem;
    margin: 0.35rem 0;
  }

  .builder-inspector__errors ul {
    list-style: none;
    margin: 0.25rem 0 0;
    padding: 0;
  }

  .builder-inspector__ifaces ul {
    list-style: none;
    margin: 0 0 0.4rem;
    padding: 0;
  }

  .builder-inspector__ifaces li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.4rem;
    font-size: 0.8rem;
  }

  .builder-inspector__iface-buttons {
    display: flex;
    flex: none;
    gap: 0.3rem;
  }

  /* The headings of the Inspector's parts are a little larger than the
     fields, in the text color of either theme (as in InspectorDiagram.vue). */
  .builder-inspector__ifaces h3,
  .builder-inspector__position h3 {
    font-weight: 700;
    font-size: 0.95rem;
    color: var(--bx-text);
    margin: 0.75rem 0 0.25rem;
  }

  /* Marked by a bar in the color of its worst issue, and each issue by its
     icon's shape as well as its color. */
  .builder-inspector__issues {
    margin: 0 0 0.6rem;
    padding: 0.35rem 0.5rem;
    border-left: 3px solid var(--bx-warning);
    background: var(--bx-bg-alt);
    border-radius: 0 var(--bx-radius) var(--bx-radius) 0;
  }

  .builder-inspector__issues[data-level='error'] {
    border-left-color: var(--bx-danger);
  }

  .builder-inspector__issues h3 {
    font-weight: 700;
    font-size: 0.95rem;
    color: var(--bx-text);
    margin: 0 0 0.25rem;
  }

  .builder-inspector__issues ul {
    list-style: none;
    margin: 0;
    padding: 0;
    font-size: 0.8rem;
  }

  .builder-inspector__issues li {
    display: flex;
    align-items: baseline;
    gap: 0.35rem;
    overflow-wrap: anywhere;
  }

  .builder-inspector__issues li + li {
    margin-top: 0.2rem;
  }

  .builder-inspector__issues li[data-level='error'] :is(.builder-icon, strong) {
    color: var(--bx-danger);
  }

  .builder-inspector__issues
    li[data-level='warning']
    :is(.builder-icon, strong) {
    color: var(--bx-warning);
  }

  .builder-inspector__issues .builder-icon {
    align-self: center;
  }

  .builder-inspector__issue-text {
    flex: 1 1 auto;
    min-width: 0;
  }

  .builder-inspector__issue-go {
    flex: none;
    align-self: center;
  }

  .builder-inspector__position-fields {
    display: flex;
    align-items: flex-end;
    gap: 0.4rem;
  }

  .builder-inspector__position-fields .builder-field {
    flex: 1 1 0;
    min-width: 0;
    margin-bottom: 0;
  }
</style>
