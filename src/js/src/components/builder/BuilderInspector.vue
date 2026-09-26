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
-->
<template>
  <section
    ref="panel"
    class="builder-inspector builder-panel"
    aria-labelledby="inspector-title"
    @pointerdown="onPress">
    <h2
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
      <p class="builder-inspector__subject">{{ target.title }}</p>

      <!-- The errors and warnings of what the Inspector shows, as applied;
           the checks button in the header lists the whole diagram's. -->
      <div
        v-if="ownIssues.length"
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
           topology, or a network one is on (see inspectorLock). -->
      <p
        v-if="lock.note"
        class="builder-inspector__note"
        data-testid="inspector-included-note">
        {{ lock.note }}
      </p>

      <p
        v-if="store.schemaError"
        class="builder-inspector__schema-error"
        role="alert"
        data-testid="inspector-schema-error">
        {{ store.schemaError }}
      </p>

      <p v-if="schema.required?.length" class="builder-inspector__hint">
        Fields marked * are required.
      </p>

      <!-- novalidate: the Inspector checks the fields itself, and JSON
           Forms shows the errors that count on their fields (see
           validation). The browser's own check of a number field's bounds
           would stop Enter from applying. -->
      <form
        ref="form"
        novalidate
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
          :readonly="store.readOnly || lock.all"
          validation-mode="NoValidation"
          :additional-errors="validation.errors"
          @change="onChange" />

        <div
          v-if="errors.length"
          :key="errorSummary"
          class="builder-inspector__errors"
          role="alert">
          <p data-testid="inspector-errors">
            {{ errors.length }}
            {{ errors.length === 1 ? 'field needs' : 'fields need' }}
            attention before these changes can be applied.
          </p>
          <ul data-testid="inspector-error-list">
            <li v-for="error in errors" :key="error.path">
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
          :data-state="errors.length ? 'error' : changed ? 'changed' : 'none'"
          data-testid="inspector-actions">
          <span class="builder-inspector__state">
            <builder-icon v-if="errors.length" name="close" :size="14" />
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

      <!-- Named apart from the node's own "Interfaces" list above: these
           act at once, while that list is part of the working copy. Its
           hint is a tooltip on its heading, shown on hover and on keyboard
           focus of its buttons; screen readers read it after the heading,
           and as Add connection point's description. -->
      <div
        v-if="target.kind === 'device'"
        class="builder-inspector__ifaces"
        @focusin="ifacesTip?.onFocusIn($event, ifacesTitle)"
        @focusout="ifacesTip?.hide()">
        <h3
          ref="ifacesTitle"
          class="label--described"
          @mouseenter="ifacesTip?.show(ifacesTitle)"
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
                :disabled="store.readOnly"
                @click="disconnect(handle.id)">
                Disconnect
              </button>
              <button
                type="button"
                class="builder-button builder-button--danger builder-inspector__iface-remove"
                :aria-label="`Remove connection point ${handle.name}`"
                :disabled="store.readOnly"
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
          :disabled="store.readOnly"
          @click="store.addInterface(target.target.id, {})">
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
        v-if="selection.type === 'node' && target.target?.position"
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
          @mouseenter="positionTip?.show(positionTitle)"
          @mouseleave="positionTip?.scheduleHide()">
          Position
        </h3>
        <p id="inspector-position-hint" hidden>{{ positionHint }}</p>
        <div class="builder-inspector__position-fields">
          <div class="builder-field">
            <label for="inspector-position-x">X</label>
            <input
              id="inspector-position-x"
              v-model.number="position.x"
              type="number"
              step="1"
              aria-describedby="inspector-position-hint"
              :disabled="store.readOnly" />
          </div>
          <div class="builder-field">
            <label for="inspector-position-y">Y</label>
            <input
              id="inspector-position-y"
              v-model.number="position.y"
              type="number"
              step="1"
              aria-describedby="inspector-position-hint"
              :disabled="store.readOnly" />
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
  import { JsonForms } from '@jsonforms/vue';

  import BuilderIcon from './BuilderIcon.vue';
  import InspectorTooltip from './inspector/InspectorTooltip.vue';
  import {
    INSPECTOR_ANNOUNCE,
    INSPECTOR_CHANGED,
    INSPECTOR_DEFAULTS,
    INSPECTOR_DRAWN_COLOR,
    INSPECTOR_FIELD_WARNINGS,
    INSPECTOR_LOCAL_PROBLEMS,
    INSPECTOR_LOCKED,
    INSPECTOR_NEW_ITEM,
    INSPECTOR_RESETS,
    INSPECTOR_SUGGESTIONS,
  } from './inspector/control.js';
  import { heldCommit, keyEffect } from './inspector/heldCommit.js';

  import {
    applyFormData,
    fieldChanged,
    fieldDefault,
    formDataChanged,
    inspectorI18n,
    inspectorLock,
    inspectorName,
    inspectorRenderers,
    inspectorTarget,
    issueText,
    mergeFormData,
    newListItem,
    relevantErrors,
    relevantFieldErrors,
    uiSchemaForKind,
  } from '@/builder/adapters/forms.js';
  import { listOf } from '@/builder/announce.js';
  import { drawnColor, drawnNetworkColor } from '@/builder/colors.js';
  import {
    createFormValidator,
    workingCopyErrors,
  } from '@/builder/form-validator.js';
  import { countsText, issueCounts, issuesAbout } from '@/builder/issues.js';
  import { connectionChanges, findNetwork } from '@/builder/model.js';
  import { schemaForKind } from '@/builder/schema.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { deviceFieldWarnings } from '@/builder/validate.js';

  const store = useBuilderStore();
  const renderers = inspectorRenderers;
  const validator = createFormValidator();

  provide(INSPECTOR_ANNOUNCE, (message) => store.announce(message));

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

  const selection = computed(() => store.inspectorSelection);
  const target = computed(() => inspectorTarget(store.doc, selection.value));
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
    return schemaForKind(store.schema, element?.kind || 'document', {
      spec: element?.data?.spec,
      iconKey: element?.data?.iconKey,
    });
  }

  const schema = computed(() => schemaFor(target.value));
  const lock = computed(() => inspectorLock(store.doc, selection.value));

  // The working copy's errors that count (see workingCopyErrors): `errors`
  // for JSON Forms to show on their fields, and `fields`, the summary. Each
  // field shows one message, that of its most relevant error (see
  // relevantErrors).
  const validation = computed((previous) => {
    const found = target.value
      ? workingCopyErrors(validator, schema.value, draft.value, loaded.value)
      : { errors: [], fields: [] };
    const next = {
      errors: relevantErrors(found.errors),
      fields: relevantFieldErrors(found.fields, found.errors, schema.value),
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

  // An included device's fields are locked rather than disabled, so they
  // stay readable and reachable with Tab; in a read-only draft they are
  // disabled like the rest (see useInspectorLocked).
  provide(
    INSPECTOR_LOCKED,
    computed(() => lock.value.all && !store.readOnly),
  );

  // A new interface added in the form is named and set up the way one drawn
  // on the canvas is (see newListItem).
  provide(INSPECTOR_NEW_ITEM, (path, data) =>
    newListItem(store.doc, selection.value, path, data ?? draft.value),
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
  provide(
    INSPECTOR_CHANGED,
    computed(() => {
      const [now, was] = [draft.value, loadedData()];

      return (path, key) => fieldChanged(now, was, path, key);
    }),
  );

  // A switch's Color is its network's, which the canvas draws in the theme's
  // token when it is a color addNetwork picks; the picker's chip and
  // swatches show it the same way (see drawnNetworkColor).
  provide(
    INSPECTOR_DRAWN_COLOR,
    computed(() =>
      target.value?.kind === 'switch' ? drawnNetworkColor : drawnColor,
    ),
  );

  // The drive image field suggests the server's disk images, once known.
  provide(
    INSPECTOR_SUGGESTIONS,
    computed(() => ({ disks: store.disks })),
  );

  // Warnings about the working copy's fields, which each field shows once
  // it commits its value, before Apply (see deviceFieldWarnings). A device
  // from an included topology is not this diagram's to fix, so its fields
  // get none, as in the diagram checks.
  provide(
    INSPECTOR_FIELD_WARNINGS,
    computed(() =>
      target.value?.kind === 'device' && !lock.value.all
        ? deviceFieldWarnings(store.doc, draft.value?.spec, {
            disks: store.disks,
          })
        : {},
    ),
  );
  const uiSchema = computed(() =>
    uiSchemaForKind(store.schema, target.value?.kind || 'document', {
      spec: target.value?.data?.spec,
      readonly: lock.value.fields,
    }),
  );
  const i18n = computed(() => inspectorI18n(schema.value));

  // Apply and Cancel appear with the first keystroke of an edit rather than
  // when the field commits it on change, which the press of a click
  // elsewhere does: the Inspector would then shift under that click. They
  // go again when the text is typed back, but not while focus or a click is
  // on its way to them (see onFieldBlur). Apply commits the field before it
  // checks for errors.
  const changed = computed(
    () => dirty.value || typing.value || localErrors.value.length > 0,
  );
  const pending = computed(
    () => (changed.value || held.value) && !store.readOnly,
  );

  const canApply = computed(
    () => changed.value && !store.readOnly && errors.value.length === 0,
  );

  const errorSummary = computed(() =>
    errors.value.map((error) => error.message).join('\n'),
  );

  const stateText = computed(() => {
    if (errors.value.length > 0) {
      return 'Fix the fields marked with errors';
    }

    return changed.value ? 'Unapplied changes' : 'No changes to apply';
  });

  // The state line is not a live region: a second region that changes along
  // with the Builder's one (on Apply, Cancel or a selection change) is often
  // not read. Those changes announce themselves, and so does the error
  // summary. Edits becoming unapplied are announced here, a moment later and
  // only if they still are: Enter in a field, or a click on Apply straight
  // from it, applies them at once, and "Updated ..." says so instead.
  const UNAPPLIED_DELAY_MS = 400;
  let unappliedTimer = null;

  watch(dirty, (now) => {
    clearTimeout(unappliedTimer);

    if (now && !store.readOnly) {
      unappliedTimer = setTimeout(() => {
        if (dirty.value) {
          store.announce('Unapplied changes.');
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

  // A device's icon is presentation only, so a new one is applied without
  // Apply, as an edit of its own, like Add connection point. Left to Apply,
  // which on a device's long form is far below the Icon field, it reached
  // the canvas only once the selection changed and applied it. Other
  // unapplied edits stay unapplied.
  //
  // One choice is one edit (see heldCommit): an icon chosen from the list
  // with the pointer commits at once, and one stepped to with keys when the
  // choice is made: when focus leaves the field, on Enter or another key
  // that is no step (a shortcut), on Apply or Cancel, or when the selection
  // changes. See onFieldKey, holdIcon and commitIcon.
  const icon = heldCommit(commitIcon);

  onBeforeUnmount(() => icon.flush());

  function reset() {
    const data = target.value
      ? JSON.parse(JSON.stringify(target.value.data))
      : {};

    draft.value = JSON.parse(JSON.stringify(data));
    loaded.value = data;
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
      store.doc,
      editing.selection,
      mergeFormData(loaded.value, draft.value, element.data),
    );
  }

  // Unapplied edits never vanish silently when the selection changes. Valid
  // edits are applied to the element they were made on, merged into it as it
  // is now; edits with errors are discarded, and the announcement says which
  // happened. Nothing is applied while a redo is pending, so an undo is not
  // undone by it. A held icon goes first, to the device it was chosen for.
  function settleUnapplied() {
    icon.flush();

    if (
      !(dirty.value || localErrors.value.length > 0) ||
      !editing ||
      store.readOnly
    ) {
      return;
    }

    const previous = inspectorTarget(store.doc, editing.selection);

    if (!previous) {
      return;
    }

    // The form shows the new selection by now.
    const found = failing(previous);

    if (found.length > 0) {
      store.announce(
        `Discarded unapplied changes to ${editing.title}: ${found.length === 1 ? 'a field has' : 'fields have'} errors.`,
      );

      return;
    }

    if (store.canRedo) {
      store.announce(`Discarded unapplied changes to ${editing.title}.`);

      return;
    }

    const next = applied(previous);

    store.commit(
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
   * (see settleEdits in BuilderBeta.vue).
   *
   * @returns {string} why edits are left unapplied, or '' when none are
   */
  function settle() {
    icon.flush();

    if (
      !(dirty.value || localErrors.value.length > 0) ||
      !editing ||
      store.readOnly
    ) {
      return '';
    }

    const previous = inspectorTarget(store.doc, editing.selection);

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

    store.commit(
      next,
      appliedLabel(`Applied changes to ${editing.title}`, next, editing),
    );
    dirty.value = false;

    return '';
  }

  // What applying edits says, and undoes as: the edit, and each connection
  // an interface VLAN it set made, moved or removed (see connectByVLAN).
  function appliedLabel(label, next, { selection: applied }) {
    return listOf([
      label,
      ...(applied.type === 'node'
        ? connectionChanges(store.doc, next, applied.id)
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
  // silently.
  watch(
    () => store.doc,
    () => {
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

    const next = JSON.stringify(event.data ?? {});

    if (next === JSON.stringify(draft.value)) {
      return;
    }

    draft.value = event.data;
    holdIcon(event.data);
    dirty.value = formDataChanged(event.data, loadedData());

    // Edits typed back leave nothing to apply, so the form shows the element
    // as it is now, which an edit elsewhere may have changed meanwhile.
    if (!dirty.value && formDataChanged(target.value?.data, loaded.value)) {
      reset();
    }
  }

  // Commits an icon chosen in the form, or holds it while it is stepped to
  // with keys (see `icon`), unless it is the one the device has or is
  // getting.
  function holdIcon(data) {
    const current = target.value;
    const iconKey = data?.iconKey || '';

    if (
      current?.kind !== 'device' ||
      store.readOnly ||
      lock.value.all ||
      iconKey === (icon.held?.iconKey ?? (current.data.iconKey || ''))
    ) {
      return;
    }

    icon.change({ selection: { ...selection.value }, iconKey });
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
      icon.key();
    } else if (effect === 'flush') {
      icon.flush();
    }
  }

  // Gives the device the icon chosen for it, unless it has it already.
  // Returns whether it did.
  function commitIcon({ selection: chosenFor, iconKey }) {
    const device = inspectorTarget(store.doc, chosenFor);

    if (
      device?.kind !== 'device' ||
      store.readOnly ||
      iconKey === (device.data.iconKey || '')
    ) {
      return false;
    }

    store.commit(
      applyFormData(store.doc, chosenFor, { ...device.data, iconKey }),
      `Changed the icon of ${device.title} to ${iconKey || 'the default'}`,
    );

    // The icon is no unapplied edit of the device's now.
    if (editing?.selection.id === chosenFor.id) {
      loaded.value = { ...loaded.value, iconKey };
    }

    return true;
  }

  // The element's data as the form loaded it, counting a held icon as
  // applied: it is no edit for Apply.
  function loadedData() {
    const data = loaded.value;

    return icon.held ? { ...data, iconKey: icon.held.iconKey } : data;
  }

  // Inputs whose typed text reaches JSON Forms only on change. A color
  // input is the color picker's own (see InspectorColorControl).
  const NOT_TYPED = [
    'checkbox',
    'radio',
    'button',
    'submit',
    'reset',
    'file',
    'color',
  ];

  function isTextEntry(field) {
    return (
      !field.readOnly &&
      (field.tagName === 'TEXTAREA' ||
        (field.tagName === 'INPUT' && !NOT_TYPED.includes(field.type)))
    );
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
  let pressing = false;
  let settleAfterPress = false;

  function onPress(event) {
    // An icon chosen with the pointer commits at once (see `icon`).
    icon.point();

    // A secondary button opens a menu, and may never send pointerup here.
    if (pressing || event.button !== 0) {
      return;
    }

    pressing = true;

    const release = () => {
      document.removeEventListener('pointerup', release, true);
      document.removeEventListener('pointercancel', release, true);
      // The click follows pointerup in the same task.
      setTimeout(() => {
        pressing = false;

        if (settleAfterPress) {
          settleAfterPress = false;
          typing.value = false;
        }
      });
    };

    document.addEventListener('pointerup', release, true);
    document.addEventListener('pointercancel', release, true);
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
      if (!pressing && document.activeElement === field) {
        typing.value = false;
      }
    });
  }

  // Focus leaving a field, Apply or Cancel. It follows the change a field
  // sends as it loses focus.
  function onFieldBlur(event) {
    const field = event.target;
    const next = event.relatedTarget;

    // Only the Icon field holds an icon, and the choice is made once focus
    // leaves it.
    icon.flush();

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

    if (pressing) {
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
    const active = document.activeElement;
    const owned =
      !active ||
      active === document.body ||
      Boolean(actions.value?.contains(active));

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
    // A held icon is an edit of its own, and says so.
    const iconChanged = icon.flush();

    await commitFocusedField();

    // Edits changed back leave nothing to apply. Committing the unchanged
    // element would still add an Undo step and a server snapshot. Edits a
    // renderer holds back until they are fixed are something to apply, which
    // their errors refuse.
    if (!dirty.value && localErrors.value.length === 0) {
      typing.value = false;
      held.value = false;

      if (!store.readOnly && !lock.value.all && !iconChanged) {
        store.announce('No changes to apply.');
      }

      await refocus();

      return;
    }

    if (!canApply.value) {
      return;
    }

    const next = applied(target.value);

    // Named after the edit, so a rename says the new name.
    store.commit(
      next,
      appliedLabel(`Updated ${inspectorName(next, selection.value)}`, next, {
        selection: selection.value,
      }),
    );
    dirty.value = false;
    held.value = false;
    await refocus();
  }

  // Cancel reached from a field has taken that field's change, so `dirty`
  // says whether there is anything to discard.
  async function cancel() {
    if (!pending.value) {
      return;
    }

    const refocus = keepFocus();

    // An icon is applied without Apply, so Cancel keeps it.
    icon.flush();

    const discarded = dirty.value || localErrors.value.length > 0;

    reset();
    held.value = false;
    store.announce(
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

  const position = ref({ x: 0, y: 0 });
  const nodePosition = computed(() =>
    selection.value.type === 'node' ? target.value?.target?.position : null,
  );

  watch(
    () => [formKey.value, nodePosition.value?.x, nodePosition.value?.y],
    () => {
      position.value = {
        x: Math.round(nodePosition.value?.x ?? 0),
        y: Math.round(nodePosition.value?.y ?? 0),
      };
    },
    { immediate: true },
  );

  // Where Move puts the node, in whole canvas pixels: the form is
  // novalidate, so a fraction typed in a field (step 1) reaches here.
  const moveTo = computed(() => ({
    x: Math.round(position.value.x),
    y: Math.round(position.value.y),
  }));

  const canMove = computed(
    () =>
      !store.readOnly &&
      Number.isFinite(position.value.x) &&
      Number.isFinite(position.value.y) &&
      (moveTo.value.x !== Math.round(nodePosition.value?.x ?? 0) ||
        moveTo.value.y !== Math.round(nodePosition.value?.y ?? 0)),
  );

  function move() {
    if (!canMove.value) {
      return;
    }

    store.moveNodes([
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

    store.removeInterface(target.value.target.id, handleId);
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

    store.remove({
      nodes: [],
      edges: connectionsOf(handleId).map((edge) => edge.id),
    });
    await nextTick();

    (removeButtons()[index] || addInterfaceButton.value)?.focus();
  }

  function connectionsOf(handleId) {
    return (store.doc.edges || []).filter(
      (entry) =>
        entry.sourceHandleId === handleId || entry.targetHandleId === handleId,
    );
  }

  function networkFor(handleId) {
    const [edge] = connectionsOf(handleId);

    if (!edge) {
      return 'not connected';
    }

    const network = findNetwork(store.doc, edge.networkId);

    return network ? `network ${network.name}` : 'unknown network';
  }

  const issues = computed(() => store.issues);
  const ownIssues = computed(() =>
    issuesAbout(store.doc, issues.value, selection.value),
  );
  const ownCounts = computed(() => issueCounts(ownIssues.value));

  function plural(count, word) {
    return `${count} ${word}${count === 1 ? '' : 's'}`;
  }

  // Announces diagram errors an edit introduces, which otherwise appear only
  // in the checks lists, often far from the edit. Opening another diagram is
  // not an edit, so its existing errors are not announced. The message goes
  // through the Builder's one live region, after the edit's own announcement.
  watch(
    () => ({
      id: store.doc?.id,
      texts: issues.value
        .filter((issue) => issue.level === 'error')
        .map((issue) => issueText(store.doc, issue)),
    }),
    (now, before) => {
      const added =
        now.id === before?.id
          ? now.texts.filter((text) => !before.texts.includes(text))
          : [];

      if (added.length) {
        store.announce(
          `${plural(added.length, 'new diagram error')}: ${added.join('; ')}`,
        );
      }
    },
  );

  defineExpose({ apply, cancel, settle, draft, errors });
</script>

<style scoped>
  .builder-inspector__title {
    font-weight: 700;
    font-size: 0.9rem;
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
