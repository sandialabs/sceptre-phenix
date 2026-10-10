<!--
  Inspector.

  Entirely schema driven: the form is generated from the builder schema served
  by GET /api/v1/schemas/builder/v1 (with a bundled fallback), so fields the
  server adds show up without touching this component. The Builder's own
  JSON Forms renderers (components/builder/inspector/) label, describe and
  flag every field; see adapters/forms.js.

  The form edits a *working copy*. Nothing reaches the document until Apply is
  pressed, and Apply refuses while a field the edits changed has an error, so
  a half-typed hostname never becomes a history commit (and therefore never
  becomes a server snapshot). Errors an element had already, on fields left
  as they were, show nowhere and block nothing (see workingCopyErrors).
  Applying merges the edits into the element as it is then (see
  mergeFormData), so a change made elsewhere meanwhile, such as an interface
  added and connected, stays. Apply and Cancel are shown only while there
  are unapplied edits, or while focus is on one of them, in a bar that says
  so and sticks to the bottom of the scrolling panel; each field an edit
  changed is marked until it is applied or cancelled. Changing the
  selection applies valid unapplied edits, or discards them, and says which.

  A field that is not set shows the value it comes to all the same, marked
  as the default (see fieldDefault): Memory the megabytes phenix gives a VM,
  a connection's Label its network's name.

  What it reads and edits is its `host`: the Builder store, for the canvas,
  or an object with the same members, for another document. The template
  editor (dialogs/TemplateDialog.vue) mounts it on a document that holds the
  one device a template describes, with the variant 'template': the form,
  its checks and its renderers are the canvas's, and what belongs to a
  canvas (the heading, the checks of the diagram, the connection points,
  the position, Apply and Cancel) is left out. Its edits then wait in the
  working copy for the dialog's Save, which calls settle().
-->
<template>
  <section
    ref="panel"
    class="builder-inspector"
    :class="template ? 'builder-inspector--template' : 'builder-panel'"
    :aria-labelledby="labelledby"
    @pointerdown="onPress">
    <h2
      v-if="!template"
      id="inspector-title"
      ref="heading"
      class="builder-inspector__title"
      tabindex="-1">
      Inspector
    </h2>

    <p v-if="!target" class="builder-inspector__empty">
      Select a node or connection to edit its properties.
    </p>

    <template v-else>
      <!-- Which schema the form comes from is said only when it is not the
           server's, in the schema error below. -->
      <p v-if="!template" class="builder-inspector__subject">
        {{ target.title }}
      </p>

      <!-- The errors and warnings of what the Inspector shows, as applied;
           the checks button in the header lists the whole diagram's. -->
      <div
        v-if="!template && ownIssues.length"
        class="builder-inspector__issues"
        :data-level="ownCounts.errors ? 'error' : 'warning'"
        data-testid="inspector-checks">
        <h3>Checks: {{ countsText(ownCounts) }}</h3>
        <ul>
          <li
            v-for="(issue, index) in ownIssues"
            :key="`${issue.path}-${index}`"
            :data-level="issue.level">
            <builder-icon
              :name="issue.level === 'error' ? 'close' : 'warning'"
              :size="12" />
            <span>
              <strong>
                {{ issue.level === 'error' ? 'Error' : 'Warning' }}:
              </strong>
              {{ issue.text }}
            </span>
          </li>
        </ul>
      </div>

      <!-- Why fields below cannot change: a device from an included
           topology, or a network one is on (see inspectorLock). An included
           device can be changed in a copy of the diagram, which a role that
           may create drafts can make from here. -->
      <p
        v-if="!template && lock.note"
        id="inspector-included-note"
        class="builder-inspector__note"
        data-testid="inspector-included-note">
        {{ lock.note }}
      </p>
      <button
        v-if="!template && lock.all && host.canCreateDrafts"
        type="button"
        class="builder-button builder-inspector__combine"
        aria-describedby="inspector-included-note"
        data-testid="inspector-combine"
        @click="$emit('combine')">
        Combine into a new draft
      </button>

      <p
        v-if="host.schemaError"
        class="builder-inspector__schema-error"
        role="alert"
        data-testid="inspector-schema-error">
        {{ host.schemaError }}
      </p>

      <p v-if="schema.required?.length" class="builder-inspector__hint">
        Fields marked * are required.
      </p>

      <!-- novalidate: the Inspector checks the fields itself, and JSON
           Forms shows the errors that count on their fields (see
           validation). The browser's own check of a number field's bounds
           would stop Enter from applying.
           autocomplete off: Firefox keeps what each Apply or Enter sends,
           and offers it back under the same field of any element, once
           its lookup returns. It first scrolls that field into view, which
           moved the Inspector away from the pointer as it pressed a button
           below. -->
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
            attention before
            {{
              template
                ? 'this template can be saved.'
                : 'these changes can be applied.'
            }}
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
             form; the panel's scroll padding, and uncover, keep a focused
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
        @scenario="$emit('scenario')" />

      <!-- Named apart from the node's own "Interfaces" list above: these
           act at once, while that list is part of the working copy. Its
           hint is a tooltip on its heading, shown on hover and on keyboard
           focus of its buttons; screen readers read it after the heading,
           and as Add connection point's description. -->
      <div
        v-if="!template && target.kind === 'device'"
        class="builder-inspector__ifaces"
        @focusin="ifacesTip?.onFocusIn($event, ifacesTitle)"
        @focusout="ifacesTip?.hide()">
        <h3
          ref="ifacesTitle"
          class="label--described"
          @mouseenter="ifacesTip?.show(ifacesTitle, $event)"
          @mouseleave="ifacesTip?.scheduleHide()">
          Connection points
        </h3>
        <p id="inspector-ifaces-hint" class="builder-visually-hidden">
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
          aria-describedby="inspector-ifaces-hint"
          :disabled="host.readOnly"
          @click="host.addInterface(target.target.id, {})">
          <builder-icon name="plus" :size="14" />
          Add connection point
        </button>
        <inspector-tooltip ref="ifacesTip" :text="ifacesHint" />
      </div>

      <!-- Below the fields and the connection points, which are looked
           for more often. Its hint is a tooltip on its heading, shown on
           hover and on keyboard focus of its fields, and the fields'
           description. -->
      <form
        v-if="!template && selection.type === 'node' && target.target?.position"
        class="builder-inspector__position"
        aria-labelledby="inspector-position-title"
        data-testid="inspector-position"
        novalidate
        @submit.prevent="move"
        @focusin="positionTip?.onFocusIn($event, positionTitle)"
        @focusout="positionTip?.hide()">
        <h3
          id="inspector-position-title"
          ref="positionTitle"
          class="label--described"
          @mouseenter="positionTip?.show(positionTitle, $event)"
          @mouseleave="positionTip?.scheduleHide()">
          Position
        </h3>
        <p id="inspector-position-hint" hidden>{{ positionHint }}</p>
        <!-- Text fields read as numbers, like the whole-number fields (see
             InspectorInputControl): Firefox's number input took " 3 " for
             no number at all. No numeric keyboard: a phone's has no minus
             sign, and a position can be negative. Read only, not disabled,
             in a read-only draft, as the fields above are. -->
        <div class="builder-inspector__position-fields">
          <div v-for="axis in ['x', 'y']" :key="axis" class="builder-field">
            <label :for="`inspector-position-${axis}`">{{
              axis.toUpperCase()
            }}</label>
            <input
              :id="`inspector-position-${axis}`"
              v-model="position[axis]"
              type="text"
              role="spinbutton"
              autocomplete="off"
              :aria-valuenow="shownPosition(axis)"
              aria-describedby="inspector-position-hint"
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
    provide,
    readonly,
    ref,
    shallowRef,
    watch,
  } from 'vue';
  import { UPDATE_DATA } from '@jsonforms/core';
  import { JsonForms } from '@jsonforms/vue';

  import BuilderIcon from './BuilderIcon.vue';
  import InspectorDiagram from './inspector/InspectorDiagram.vue';
  import InspectorTooltip from './inspector/InspectorTooltip.vue';
  import {
    INSPECTOR_ANNOUNCE,
    INSPECTOR_CHANGED,
    INSPECTOR_DEFAULTS,
    INSPECTOR_DRAWN_COLOR,
    INSPECTOR_FIELD_WARNINGS,
    INSPECTOR_ICON_LIBRARY,
    INSPECTOR_ICONS,
    INSPECTOR_INSERT_ITEM,
    INSPECTOR_LOCAL_PROBLEMS,
    INSPECTOR_LOCKED,
    INSPECTOR_NEW_ITEM,
    INSPECTOR_RESETS,
    INSPECTOR_SUGGESTIONS,
    readNumberText,
  } from './inspector/control.js';
  import { heldCommit, keyEffect } from './inspector/heldCommit.js';

  import {
    applyFormData,
    fieldChanged,
    fieldDefault,
    formDataChanged,
    insertedListItem,
    inspectorI18n,
    inspectorLock,
    inspectorName,
    inspectorRenderers,
    inspectorTarget,
    issueText,
    lookChangeLabel,
    mergeFormData,
    newListItem,
    relevantErrors,
    uiSchemaForKind,
  } from '@/builder/adapters/forms.js';
  import { count, listOf } from '@/builder/announce.js';
  import { drawnColor, drawnNetworkColor } from '@/builder/colors.js';
  import { focusLost, isTextEntry } from '@/builder/commands.js';
  import {
    createFormValidator,
    fieldLabel,
    workingCopyErrors,
  } from '@/builder/form-validator.js';
  import { SAVED_UNAPPLIED } from '@/builder/history.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { MAX_DOCUMENT_ICONS } from '@/builder/icons.js';
  import { countsText, issueCounts, issuesAbout } from '@/builder/issues.js';
  import {
    connectionChanges,
    findNetwork,
    HEX_COLOR,
    LOOK_KEYS,
    lookOf,
    moveNodes,
    NODE_COLOR_KEYS,
    sameButStamp,
  } from '@/builder/model.js';
  import { schemaForKind } from '@/builder/schema.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { deviceFieldWarnings } from '@/builder/validate.js';

  // Edit scenarios, in the Diagram section, asks for the Scenario dialog;
  // Combine into a new draft, under an included device's note, for the
  // draft the view makes (see combineIncluded in Builder.vue).
  defineEmits(['scenario', 'combine']);

  const props = defineProps({
    // What the Inspector reads and edits, in place of the Builder store,
    // which it is by default: a reactive object with the store's members
    // the Inspector uses. Those are doc, inspectorSelection, schema,
    // schemaError, readOnly, disks, issues, canRedo, canCreateDrafts and
    // iconShelf, and the actions commit(doc, label), announce(message),
    // addInterface, removeInterface, remove, moveNodes and
    // shelveIcons(icons). A commit that is not the store's makes the
    // document carry the icons it names, as the store's does (see
    // settleIcons in icons.js), and returns whether it took the document.
    // The Inspector keeps the host it is set up with.
    host: { type: Object, default: null },
    // 'canvas', or 'template' for the template editor (see the top of this
    // file).
    variant: {
      type: String,
      default: 'canvas',
      validator: (value) => ['canvas', 'template'].includes(value),
    },
    // The id of the heading that names the Inspector's section: its own,
    // which the template variant does not show.
    labelledby: { type: String, default: 'inspector-title' },
  });

  const host = props.host || useBuilderStore();
  const template = props.variant === 'template';
  const renderers = inspectorRenderers;
  const validator = createFormValidator();

  // The form's path of an interface's VLAN (see fieldWarnings).
  const INTERFACE_VLAN_FIELD = /^spec\.network\.interfaces\.\d+\.vlan$/;

  provide(INSPECTOR_ANNOUNCE, (message) => host.announce(message));

  // Counts the times reset() reloads the form's data from the document
  // (Cancel, undo, another selection, and after Apply or an outline edit), so
  // renderers that keep state of their own, such as the oneOf kind picker,
  // can tell data replaced from outside the form from an edit made in it.
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
  // The text each field held when it took focus or last committed. Text
  // typed back to it is no change: the field sends no change event for it.
  const committedText = new WeakMap();
  // Focus is on Apply or Cancel, which then stay until used or left.
  const held = ref(false);
  // A press in the Inspector is under way, from pointerdown until its click
  // is over (see onPress).
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
  const target = computed(() => inspectorTarget(host.doc, selection.value));
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
      iconKey: element?.data?.iconKey,
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

  // What the Inspector shows of the working copy's checks: the error and
  // the warnings under each field, the summary under the form, and the
  // state beside Apply. The field a press takes focus from commits its
  // value then, and a line that came or went above the button being pressed
  // would move it away from the pointer, and lose its click. So during a
  // press they stay as they were, and follow once its click is over. Apply
  // goes by the checks themselves.
  const duringPress = (checks) =>
    computed((shown) => (pressing.value && shown) || checks.value);
  const shownValidation = duringPress(validation);
  const shownErrors = duringPress(errors);

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
  // theme's token when it is a color addNetwork picks; the picker's chip and
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

  // The custom icons the Custom icon field and its dialog work with (see
  // INSPECTOR_ICONS): those the host's document carries, and those chosen
  // in the dialog, which the host keeps until the edit that names one is
  // committed (see shelveIcons and commit in the store).
  function iconEntry(id) {
    const carried = host.doc.icons;

    return carried && Object.hasOwn(carried, id)
      ? carried[id]
      : host.iconShelf.get(id);
  }

  provide(INSPECTOR_ICONS, {
    entry: iconEntry,
    shelve: (id, entry) => host.shelveIcons({ [id]: entry }),
    diagram: () =>
      Object.entries(host.doc.icons || {}).map(([id, entry]) => ({
        id,
        name: entry?.name || '',
        data: entry?.data,
      })),
    full: (id) => {
      const carried = host.doc.icons || {};

      return (
        Object.keys(carried).length >= MAX_DOCUMENT_ICONS &&
        !Object.hasOwn(carried, id)
      );
    },
  });

  // The user's icon library on the server, for the same dialog.
  provide(INSPECTOR_ICON_LIBRARY, iconLibrary);

  // The drive image field suggests the server's disk images, once known.
  provide(
    INSPECTOR_SUGGESTIONS,
    computed(() => ({ disks: host.disks })),
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
  const uiSchema = computed(() =>
    uiSchemaForKind(host.schema, target.value?.kind || 'document', {
      spec: target.value?.data?.spec,
      readonly: lock.value.fields,
      template,
    }),
  );
  const i18n = computed(() => inspectorI18n(schema.value));

  // Apply and Cancel appear with the first keystroke of an edit rather than
  // when the field commits it on change, which the press of a click
  // elsewhere does: the Inspector would then shift under that click. They
  // go again when the text is typed back, but not while focus or a click is
  // on its way to them (see onFieldBlur). Apply commits the field before it
  // checks for errors. A template's edits have neither: they wait for the
  // template editor's Save.
  const changed = computed(
    () => dirty.value || typing.value || localErrors.value.length > 0,
  );
  const pending = computed(
    () => !template && (changed.value || held.value) && !host.readOnly,
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

  // The state line is not a live region: a second region that changes along
  // with the Builder's one (on Apply, Cancel or a selection change) is often
  // not read. Those changes announce themselves, and so does the error
  // summary. Edits becoming unapplied are announced here, a moment later and
  // only if they still are: Enter in a field, or a click on Apply straight
  // from it, applies them at once, and "Updated ..." says so instead. A
  // template's edits are not announced as unapplied: nothing applies them
  // but Save.
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
  // it, for focus to return to when Apply or Cancel goes.
  let lastEdited = null;

  // A device's look (its icon, its custom icon, its outline color and its
  // fill color, see LOOK_KEYS) is presentation only, so a new one is
  // applied without Apply, as an edit of its own, like Add connection
  // point. Left to Apply, which on a device's long form is far below these
  // fields, it reached the canvas only once the selection changed and
  // applied it. Other unapplied edits stay unapplied. A switch's short form
  // waits for Apply.
  //
  // One choice is one edit (see heldCommit): an icon chosen from the list
  // with the pointer commits at once, and one stepped to with keys when the
  // choice is made: when focus leaves the field, on Enter or another key
  // that is no step (a shortcut), on Apply or Cancel, or when the selection
  // changes. A color arrives as one change, from its picker or its text
  // field, and a custom icon as one, from its dialog or Remove: each
  // commits at once. See onFieldKey, holdLook and commitLook.
  const look = heldCommit(commitLook);
  // A look was committed in the task under way: Enter in a color's text
  // field commits the color and, where the browser submits the form for it
  // (Firefox does, before the field's change), asks to apply what is left.
  let lookJustCommitted = false;

  onBeforeUnmount(() => look.flush());

  function reset() {
    const data = target.value
      ? JSON.parse(JSON.stringify(target.value.data))
      : {};

    draft.value = JSON.parse(JSON.stringify(data));
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
  // were made on, merged into it as it is now (see mergeFormData).
  function applied(element) {
    return applyFormData(
      host.doc,
      editing.selection,
      mergeFormData(loaded.value, draft.value, element.data),
    );
  }

  // Unapplied edits never vanish silently when the selection changes. Valid
  // edits are applied to the element they were made on, merged into it as it
  // is now; edits with errors are discarded, and the announcement says which
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

  /**
   * Settles unapplied edits before a save (Save now and its key), as a
   * selection change does: valid edits are applied, merged into the element
   * as it is now. Saving is asked for as Apply is, so unlike a selection
   * change it applies them while a redo is pending too, which clears Redo
   * as Apply does. The form stays open, so edits that cannot be applied are
   * kept, not discarded. The field being typed in has committed already
   * (see settleEdits in Builder.vue).
   *
   * @returns {string} why edits are left unapplied, or '' when none are
   */
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
      return 'the conflict is being resolved';
    }

    dirty.value = false;

    return '';
  }

  /**
   * Saves unapplied edits before the diagram is left or read whole: Back to
   * drafts, another page, Upload, a reload, Publish and Download (see
   * leave.js). Valid edits, and a position typed and not moved to, are
   * applied as one edit named SAVED_UNAPPLIED, which the History dialog
   * marks; like a save, this applies them while a redo is pending too. It
   * works at once, as a reload cannot wait: a field's change JSON Forms has
   * not sent yet is taken first (see catchUp). Edits that cannot be applied
   * stay in the form.
   *
   * @returns {{title: string, fields: string[]}|null} the element's title
   *   and the fields whose edits cannot be applied, or null
   */
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
      dirty.value = false;
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

  // Reset when the document changes underneath us (undo, outline edits) unless
  // the user has unapplied work in progress, which must never be discarded
  // silently. A save's answer changes only who saved the document and when
  // (see stampEntry in store.js), which no form shows: a reset then would
  // drop the text being typed in a field.
  watch(
    () => host.doc,
    (next, previous) => {
      if (sameButStamp(next, previous)) {
        return;
      }

      if (!dirty.value && localErrors.value.length === 0) {
        reset();
      }
    },
  );

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

  // Commits what a change of the form's data changed of the device's look,
  // or holds it while its icon is stepped to with keys (see `look`). Only
  // the look fields this change set count, told from the data the form had
  // before it: the form keeps its data while it has unapplied edits, and a
  // look the device was given elsewhere meanwhile is not put back by an
  // edit of another field. A color that is no #rrggbb is left out: its
  // field shows the error, and the device keeps the color it has.
  function holdLook(data, before) {
    const current = target.value;

    if (current?.kind !== 'device' || host.readOnly || lock.value.all) {
      return;
    }

    const [chosen, shown] = [lookOf(data), lookOf(before)];
    const refused = (key) =>
      NODE_COLOR_KEYS.includes(key) &&
      chosen[key] !== '' &&
      !HEX_COLOR.test(chosen[key]);
    const changed = LOOK_KEYS.filter(
      (key) => chosen[key] !== shown[key] && !refused(key),
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

  // A key on the Icon select: one that steps it holds the icon it steps to,
  // and any other, such as Enter or a shortcut, commits the icon held first.
  function onFieldKey(event) {
    const field = event.target;

    if (
      field.tagName !== 'SELECT' ||
      field.closest('[data-path]')?.dataset.path !== 'iconKey'
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

  // Gives the device the look fields chosen for it, unless it has them
  // already, as an edit named after the first field it changes (see
  // lookChangeLabel). Returns whether it did.
  function commitLook({ selection: chosenFor, change }) {
    const device = inspectorTarget(host.doc, chosenFor);

    if (device?.kind !== 'device' || host.readOnly) {
      return false;
    }

    const has = lookOf(device.data);
    const label = lookChangeLabel(
      device.title,
      has,
      { ...has, ...change },
      (id) => iconEntry(id)?.name,
    );

    if (!label) {
      return false;
    }

    // A commit refused while a conflict is resolved leaves the look in the
    // form, as an unapplied edit.
    if (
      !host.commit(
        applyFormData(host.doc, chosenFor, { ...device.data, ...change }),
        label,
      )
    ) {
      return false;
    }

    // The look is no unapplied edit of the device's now.
    if (editing?.selection.id === chosenFor.id) {
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

    // Only the Icon field holds a look, and the choice is made once focus
    // leaves it.
    look.flush();

    // The window lost focus: the element gets it back, so nothing settles.
    if (!next && document.activeElement === field) {
      return;
    }

    // Tab from the last field, or a click, onto Apply or Cancel: they stay
    // for it, even when the change left nothing to apply, rather than go
    // from under focus and drop it on <body>.
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

  /**
   * Apply and Cancel go once the changes they act on are gone. If one of
   * them had focus (or nothing had, as after a click in Safari), focus moves
   * to the field edited last, or to the Inspector heading when that field is
   * gone, rather than falling to <body> (WCAG 2.4.3).
   *
   * @returns {Function} call once the action is done
   */
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
  // that change fires, so commit the focused field first and let JSON Forms
  // report back before deciding whether there is anything to apply. Selects
  // and checkboxes have committed already, and a change sent to a select
  // would read a "Not set" picker as a choice.
  async function commitFocusedField() {
    const field = document.activeElement;

    if (form.value?.contains(field) && isTextEntry(field)) {
      field.dispatchEvent(new Event('change', { bubbles: true }));
      await nextTick();
    }
  }

  async function apply(event) {
    // Buttons inside JSON Forms renderers are type="button"; anything else
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
    host.commit(
      next,
      appliedLabel(`Updated ${inspectorName(next, selection.value)}`, next, {
        selection: selection.value,
      }),
    );
    dirty.value = false;
    held.value = false;

    // The checks above the form list the warnings the edit brought, far
    // from Apply: they are announced after the edit.
    const added = ownWarnings().filter((text) => !warned.includes(text));

    if (added.length) {
      host.announce(
        `${count(added.length, 'new warning')}: ${added.join('; ')}`,
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

  // Moves focus to the field an error summary entry names: the innermost
  // control for its data path (a oneOf value rather than its kind picker).
  // A list with too few items has no input yet, so its Add button takes
  // focus; a field the form does not show falls back to the nearest field
  // that contains it. A closed section with the field opens. Returns
  // whether a control took focus.
  function focusField(path) {
    const fields = [...(form.value?.querySelectorAll('[data-path]') || [])];
    const segments = String(path).split('.');

    for (let length = segments.length; length > 0; length -= 1) {
      const scope = segments.slice(0, length).join('.');
      const field = fields
        .filter((element) => element.dataset.path === scope)
        .pop();
      const control =
        field?.querySelector(
          'input:not([type="hidden"]):not(:disabled), select:not(:disabled), textarea:not(:disabled)',
        ) || field?.querySelector('button:not(:disabled)');

      if (control) {
        for (
          let section = control.closest('details');
          section && form.value?.contains(section);
          section = section.parentElement?.closest('details')
        ) {
          section.open = true;
        }

        control.focus();

        return true;
      }
    }

    return false;
  }

  const positionHint = computed(
    () =>
      `Canvas pixels${target.value?.target?.parentId ? ' inside its group' : ''}. Moves the node without dragging.`,
  );

  const ifacesHint = computed(() =>
    lock.value.all
      ? 'Interfaces of this device, as its included topology defines them.'
      : 'Interfaces with a handle on the canvas to connect from. Adding, disconnecting or removing one takes effect at once, without Apply.',
  );

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

  // The numbers typed, spaces around them left out; undefined for text
  // that is no number.
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
  const ownIssues = computed(() =>
    issuesAbout(host.doc, issues.value, selection.value),
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
          `${count(added.length, 'new diagram error')}: ${added.join('; ')}`,
        );
      }
    },
  );

  defineExpose({
    apply,
    cancel,
    settle,
    saveUnapplied,
    draft,
    errors,
    changed,
  });
</script>

<style scoped>
  /* The column's name, smaller and quieter than what it shows: the muted
     text keeps 4.5:1 on the panel in either theme. */
  .builder-inspector__title {
    font-weight: 600;
    font-size: 0.75rem;
    color: var(--bx-text-muted);
    margin: 0 0 0.35rem;
  }

  .builder-inspector__subject {
    font-size: 0.85rem;
    margin-bottom: 0.5rem;
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

  .builder-inspector__ifaces h3,
  .builder-inspector__position h3 {
    font-weight: 700;
    font-size: 0.85rem;
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
    font-size: 0.85rem;
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
