<!--
  Share templates and collections of the user's library: with named people,
  who then find them under Shared with me and can use and copy them but not
  change them, and, for a role that may publish, server-wide, for everyone
  who can use the Builder.

  Opened from a card's Share and Share selected on the Node Templates tab,
  and from Share on a collection shown there. A share is a change, not a
  list: the people added are added to every item, those removed are taken
  off it, and everyone else keeps what they have, so two dialogs never
  overwrite each other. For one item the dialog lists who has access, each
  with Remove that turns into Keep in place, so focus never jumps; a share
  whose account was removed starts marked for removal. For several it only
  adds.

  People are named through the user field the draft Share dialog has
  (BuilderUserCombobox.vue). The dialog is opened only for a user who may
  share with people (canShare), which needs an account of their own. With
  sign-in off there is none, so the Node Templates tab offers no Share, as
  the drafts page offers none.

  Nothing changes until Save, which sends who was added and removed, then
  whether the items are server-wide. A person the server refuses is listed
  in an error summary, and nothing is shared; an item that would pass the
  most people allowed is listed after the others took the change. Nothing
  typed is lost either way. Cancel or Escape with changes asks first, in
  the footer. The page behind the dialog is inert, so its status and alert
  regions are its own; what a save came to is said by the page once the
  dialog has closed.
-->
<template>
  <builder-dialog
    class="builder-template-share"
    :title="`Share ${subject}`"
    title-id="template-share-title"
    describedby="template-share-intro"
    data-testid="template-share-dialog"
    @close="requestClose">
    <p id="template-share-intro" class="builder-hint">
      <template v-if="one">
        People you add find this under Shared with me in their Node Templates
        and in Add nodes. They can use it and copy it, and they see your later
        changes. They cannot change it.
      </template>
      <template v-else>
        People you add find these under Shared with me in their Node Templates
        and in Add nodes. They can use them and copy them, and they see your
        later changes. They cannot change them.
      </template>
    </p>

    <form class="builder-template-share__add" novalidate @submit.prevent="add">
      <div class="builder-field">
        <label for="template-share-user">User</label>
        <builder-user-combobox
          id="template-share-user"
          ref="userField"
          v-model="addName"
          :users="users"
          :exclude="listed"
          :disabled="phase === 'saving'"
          :invalid="Boolean(fieldError.text)"
          :describedby="userDescription"
          @update:model-value="fieldError.clear()"
          @submit="add" />
        <p
          id="template-share-users-note"
          class="builder-template-share__users-note"
          :class="{ 'builder-dialog__error': usersPhase === 'failed' }"
          role="status"
          data-testid="template-share-users-note">
          <template v-if="usersPhase === 'loading'">
            <span class="builder-toolbar__spinner" aria-hidden="true"></span>
            <span>Loading users…</span>
          </template>
          <template v-else-if="usersPhase === 'failed'">
            <span>{{ USERS_FAILED }}</span>
            <button
              type="button"
              class="builder-button"
              data-testid="template-share-users-retry"
              @click="retryUsers">
              Retry
            </button>
          </template>
        </p>
        <!-- An alert: Enter in the field leaves focus where it is, so the
             field's new description would not be read. -->
        <p
          id="template-share-user-error"
          class="builder-dialog__message builder-dialog__error"
          role="alert"
          data-testid="template-share-user-error">
          <span v-if="fieldError.text" :key="fieldError.key">{{
            fieldError.text
          }}</span>
        </p>
      </div>

      <button
        type="submit"
        class="builder-button builder-template-share__add-button"
        data-testid="template-share-add"
        :aria-disabled="phase !== 'ready' || undefined">
        <builder-icon name="plus" :size="14" />
        Add
      </button>
    </form>

    <h3 id="template-share-adding-title">To add</h3>
    <p
      v-if="!people.length"
      class="builder-template-share__empty"
      data-testid="template-share-empty">
      No one yet.
    </p>
    <ul
      v-else
      class="builder-template-share__people"
      aria-labelledby="template-share-adding-title"
      data-testid="template-share-adding">
      <li
        v-for="user in people"
        :key="user"
        class="builder-template-share__row"
        :data-testid="`template-share-adding-${user}`">
        <span class="builder-template-share__name">{{ user }}</span>
        <button
          type="button"
          class="builder-button"
          :aria-label="`Remove ${user}`"
          :aria-disabled="phase !== 'ready' || undefined"
          :data-testid="`template-share-unadd-${user}`"
          @click="unadd(user)">
          Remove
        </button>
      </li>
    </ul>

    <template v-if="one">
      <h3 id="template-share-people-title">People with access</h3>
      <ul
        class="builder-template-share__people"
        aria-labelledby="template-share-people-title"
        :aria-busy="phase === 'saving' || undefined"
        data-testid="template-share-people">
        <li
          class="builder-template-share__row builder-template-share__row--owner">
          <span class="builder-template-share__name">{{ owner }} (you)</span>
          <span class="builder-template-share__note">Owner</span>
        </li>
        <li
          v-for="row in shownRows"
          :key="row.user"
          class="builder-template-share__row"
          :class="{ 'is-removed': row.removed }"
          :data-testid="`template-share-row-${row.user}`">
          <span class="builder-template-share__name">{{ row.user }}</span>
          <span
            v-if="rowNote(row)"
            :id="noteId(row)"
            class="builder-template-share__note">
            {{ rowNote(row) }}
          </span>
          <!-- One button that turns into the other, so focus stays on
               it. -->
          <button
            type="button"
            class="builder-button"
            :aria-label="`${row.removed ? 'Keep' : 'Remove'} ${row.user}`"
            :aria-describedby="rowNote(row) ? noteId(row) : undefined"
            :aria-disabled="row.stale || phase !== 'ready' || undefined"
            :data-testid="`template-share-remove-${row.user}`"
            @click="toggleRemove(row)">
            {{ row.removed ? 'Keep' : 'Remove' }}
          </button>
        </li>
      </ul>
    </template>
    <p v-else class="builder-hint" data-testid="template-share-several">
      Adds these people to every selected item. People who already have access
      keep it.
    </p>

    <!-- Publishing needs a permission of its own (builder-templates
         publish). -->
    <template v-if="canPublish">
      <div v-if="one" class="builder-template-share__server">
        <h3>Server-wide</h3>
        <label class="builder-template-share__check">
          <input
            v-model="published"
            type="checkbox"
            :disabled="phase === 'saving'"
            data-testid="template-share-server-wide" />
          Published server-wide: anyone who can use the Builder on this server
          can use it
        </label>
      </div>
      <fieldset
        v-else
        class="builder-template-share__server"
        data-testid="template-share-server-wide">
        <legend>Server-wide</legend>
        <label
          v-for="choice in SERVER_CHOICES"
          :key="choice.value"
          class="builder-template-share__check">
          <input
            v-model="serverChoice"
            type="radio"
            name="template-share-server-wide"
            :value="choice.value"
            :disabled="phase === 'saving'"
            :data-testid="`template-share-server-wide-${choice.value}`" />
          {{ choice.label }}
        </label>
      </fieldset>
    </template>

    <!-- A refused person: nothing was shared. -->
    <div
      v-if="refusals.length"
      ref="refusalsEl"
      class="builder-template-share__summary"
      role="alert"
      tabindex="-1"
      aria-labelledby="template-share-refusals-title"
      data-testid="template-share-refusals">
      <h3 id="template-share-refusals-title">
        Some people could not be added.
      </h3>
      <ul>
        <li v-for="(text, index) in refusals" :key="index">{{ text }}</li>
      </ul>
    </div>

    <!-- Items the server left as they were, while the others changed. -->
    <div
      v-if="failures.length"
      ref="failuresEl"
      class="builder-template-share__summary"
      role="alert"
      tabindex="-1"
      aria-labelledby="template-share-failed-title"
      data-testid="template-share-failed">
      <h3 id="template-share-failed-title">{{ failedHeading }}</h3>
      <ul>
        <li v-for="(text, index) in failures" :key="index">{{ text }}</li>
      </ul>
    </div>

    <p
      id="template-share-alert"
      class="builder-dialog__message builder-dialog__error"
      role="alert"
      data-testid="template-share-alert">
      <span v-if="alert.text" :key="alert.key">{{ alert.text }}</span>
    </p>

    <p
      class="builder-visually-hidden"
      role="status"
      data-testid="template-share-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>

    <div class="builder-template-share__footer">
      <p
        v-if="change.count"
        id="template-share-changes"
        class="builder-template-share__changes"
        data-testid="template-share-changes">
        {{ count(change.count, 'change') }} not saved
      </p>
      <div
        v-if="confirmingDiscard"
        class="builder-dialog__actions"
        role="group"
        aria-labelledby="template-share-discard-question"
        data-testid="template-share-discard">
        <p
          id="template-share-discard-question"
          class="builder-template-share__question">
          Discard {{ count(change.asked, 'unsaved change') }}?
        </p>
        <button
          ref="keepEditingButton"
          type="button"
          class="builder-button"
          data-testid="template-share-keep-editing"
          @click="keepEditing">
          Keep editing
        </button>
        <button
          type="button"
          class="builder-button builder-button--danger"
          data-testid="template-share-discard-confirm"
          @click="$emit('close')">
          Discard
        </button>
      </div>
      <div v-else class="builder-dialog__actions">
        <button
          ref="cancelButton"
          type="button"
          class="builder-button"
          data-testid="template-share-cancel"
          :aria-disabled="phase === 'saving' || undefined"
          @click="requestClose">
          Cancel
        </button>
        <button
          type="button"
          class="builder-button builder-button--primary"
          data-testid="template-share-save"
          :aria-describedby="
            change.count ? 'template-share-changes' : undefined
          "
          :aria-disabled="phase !== 'ready' || !change.count || undefined"
          :aria-busy="phase === 'saving' || undefined"
          @click="save">
          {{ phase === 'saving' ? 'Saving…' : 'Save' }}
        </button>
      </div>
    </div>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import BuilderUserCombobox from '../BuilderUserCombobox.vue';
  import { useMessage } from './message.js';

  import { count } from '@/builder/announce.js';
  import { shareErrors } from '@/builder/api.js';
  import { bulkFailureHeading } from '@/builder/bulk.js';
  import { findUser } from '@/builder/combobox.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    reachedThroughCollection,
    shareRows,
    shareSubject,
    templateShareChange,
    templateShareFailures,
    templateShareReason,
    templateSharedMessage,
    validateTemplateShareAdd,
  } from '@/builder/templates.js';
  import { usePhenixStore } from '@/store.js';

  const props = defineProps({
    // The items to share, as the library lists them, each with its kind:
    // 'template' or 'collection'. All are the user's own.
    targets: { type: Array, required: true },
  });

  const emit = defineEmits(['close']);

  const store = useBuilderStore();

  const USERS_FAILED =
    'Users could not be loaded. You can still type a username.';
  const SERVER_CHOICES = [
    { value: 'keep', label: 'Leave as it is' },
    { value: 'publish', label: 'Publish server-wide' },
    { value: 'remove', label: 'Remove from server-wide' },
  ];

  // The items as the dialog knows them: what a save changed is kept here,
  // so a second Save sends only what is still to change.
  const items = ref(props.targets.map((target) => ({ ...target })));
  const one = computed(() => items.value.length === 1);
  const subject = computed(() => shareSubject(items.value));
  const canPublish = computed(() => store.templates.canPublish);
  const owner = computed(() => store.templates.owner);
  const maxShares = computed(() => store.templates.limits?.shares);

  // ready, or saving.
  const phase = ref('ready');

  // The people to add, in the order they were added; the one item's people
  // (see shareRows); whether the one item is to be server-wide, or what to
  // do with several (SERVER_CHOICES).
  const people = ref([]);
  const rows = ref(one.value ? shareRows(items.value[0]) : []);
  const published = ref(one.value && Boolean(items.value[0].serverWide));
  const serverChoice = ref('keep');

  const addName = ref('');
  const fieldError = useMessage();
  const alert = useMessage();
  const status = useMessage();
  // Why the server refused people, and the items it left as they were, a
  // sentence each.
  const refusals = ref([]);
  const failures = ref([]);
  const failedHeading = ref('');
  const confirmingDiscard = ref(false);

  const userField = ref(null);
  const refusalsEl = ref(null);
  const failuresEl = ref(null);
  const cancelButton = ref(null);
  const keepEditingButton = ref(null);
  // Where focus was when Cancel or Escape asked first.
  let beforeDiscard = null;

  // Someone added again is listed under To add, not among the people with
  // access, whose share they replace.
  const shownRows = computed(() =>
    rows.value.filter((row) => !people.value.includes(row.user)),
  );

  function serverWideValue() {
    if (!canPublish.value) {
      return null;
    }

    if (one.value) {
      return published.value;
    }

    return { keep: null, publish: true, remove: false }[serverChoice.value];
  }

  const change = computed(() =>
    templateShareChange({
      targets: items.value,
      people: people.value,
      rows: rows.value,
      serverWide: serverWideValue(),
    }),
  );

  // --- the people ------------------------------------------------------

  // The users the items may be shared with, once read; loading, failed or
  // ready.
  const users = ref(null);
  const usersPhase = ref('loading');

  // The owner, the user and anyone listed with access or to add are not
  // offered. Someone marked removed is: adding them keeps them.
  const listed = computed(() => [
    owner.value,
    usePhenixStore().username,
    ...people.value,
    ...rows.value
      .filter((row) => !row.stale && !row.removed)
      .map((row) => row.user),
  ]);
  // The usernames Add checks against, once they are known.
  const knownUsers = computed(() =>
    users.value ? users.value.map((user) => user.username) : null,
  );

  const userDescription = computed(() =>
    [
      usersPhase.value !== 'ready' ? 'template-share-users-note' : '',
      fieldError.text ? 'template-share-user-error' : '',
    ]
      .filter(Boolean)
      .join(' '),
  );

  async function loadUsers() {
    usersPhase.value = 'loading';

    try {
      users.value = await store.loadTemplateShareCandidates();
      usersPhase.value = 'ready';
    } catch {
      usersPhase.value = 'failed';
    }
  }

  // Retry goes while the users are read, so focus moves to the field first.
  function retryUsers() {
    userField.value?.focus();
    loadUsers();
  }

  async function add() {
    if (phase.value !== 'ready') {
      return;
    }

    userField.value?.closeList();

    // A user chosen from the list is named by their label.
    const check = validateTemplateShareAdd(
      findUser(users.value, addName.value)?.username ?? addName.value,
      {
        targets: items.value,
        people: people.value,
        rows: rows.value,
        owner: owner.value,
        knownUsers: knownUsers.value,
        max: maxShares.value,
      },
    );

    if (check.error) {
      fieldError.set(check.error, 'user');
      await nextTick();
      userField.value?.focus();

      return;
    }

    if (check.row) {
      check.row.removed = false;
      status.set(`${check.user} kept.`);
    } else {
      people.value = [...people.value, check.user];
      status.set(`${check.user} added to the list.`);
    }

    fieldError.clear();
    addName.value = '';
    userField.value?.focus();
  }

  // The row goes, and its button with it, so focus moves to the field.
  async function unadd(user) {
    if (phase.value !== 'ready') {
      return;
    }

    people.value = people.value.filter((name) => name !== user);
    status.set(`${user} removed from the list.`);
    await nextTick();
    userField.value?.focus();
  }

  function rowNote(row) {
    if (row.stale) {
      return 'Account removed';
    }

    return row.removed ? 'Removed when you save' : '';
  }

  // Usernames may hold spaces, so a row's note is named by its place.
  function noteId(row) {
    return `template-share-note-${rows.value.indexOf(row)}`;
  }

  function toggleRemove(row) {
    if (row.stale || phase.value !== 'ready') {
      return;
    }

    row.removed = !row.removed;
    status.set(
      row.removed
        ? `${row.user} will be removed when you save.`
        : `${row.user} kept.`,
    );
  }

  // --- saving ----------------------------------------------------------

  // Moves focus to a summary once it shows.
  async function focusOn(element) {
    await nextTick();
    element.value?.focus();
  }

  async function save() {
    if (phase.value !== 'ready' || !change.value.count) {
      return;
    }

    const { add: adding, remove, publish } = change.value;
    const ids = {
      templates: items.value
        .filter((item) => item.kind !== 'collection')
        .map((item) => item.id),
      collections: items.value
        .filter((item) => item.kind === 'collection')
        .map((item) => item.id),
    };
    // What the page says once everything took the change, from the items
    // as they were before it.
    const message = templateSharedMessage(items.value, change.value, {
      rows: rows.value,
      reached:
        one.value && reachedThroughCollection(store.templates, items.value[0]),
    });
    const failed = [];
    let step = `share ${subject.value}`;

    phase.value = 'saving';
    fieldError.clear();
    alert.clear();
    refusals.value = [];
    failures.value = [];

    try {
      if (adding.length || remove.length) {
        const result = await store.shareLibraryItems(ids, {
          add: adding,
          remove,
        });

        failed.push(...result.failed);

        // Taken by every item: the people part starts again from what the
        // items have now. An item that refused keeps what was typed.
        if (!result.failed.length) {
          rows.value = [
            ...rows.value.filter(
              (row) => !row.removed && !adding.includes(row.user),
            ),
            ...adding.map((user) => ({ user, stale: false, removed: false })),
          ];
          people.value = [];
        }
      }

      if (publish !== null) {
        step =
          publish === true
            ? `publish ${subject.value} server-wide`
            : `remove ${subject.value} from server-wide`;

        const result = await store.publishLibraryItems(ids, publish);

        failed.push(...result.failed);

        if (!result.failed.length) {
          items.value = items.value.map((item) => ({
            ...item,
            serverWide: publish,
          }));
          serverChoice.value = 'keep';
        }
      }
    } catch (error) {
      phase.value = 'ready';

      const refused = shareErrors(error);

      if (refused.length) {
        refusals.value = refused.map(({ user, reason }) =>
          templateShareReason(reason, user, items.value, maxShares.value),
        );
        await focusOn(refusalsEl);
      } else {
        alert.set(store.describeLibraryError(error, step));
      }

      return;
    }

    phase.value = 'ready';

    if (failed.length) {
      failures.value = templateShareFailures(
        items.value,
        failed,
        maxShares.value,
      );
      failedHeading.value = one.value
        ? 'Some changes were not saved.'
        : bulkFailureHeading(
            new Set(failed.map((entry) => `${entry.kind}/${entry.id}`)).size,
            items.value.length,
            'item',
            'items',
            'changed',
          );
      await focusOn(failuresEl);

      return;
    }

    store.announce(message);
    emit('close');
  }

  // --- closing ---------------------------------------------------------

  async function requestClose() {
    if (phase.value === 'saving') {
      return;
    }

    // Escape again, while asked, keeps editing.
    if (confirmingDiscard.value) {
      keepEditing();

      return;
    }

    if (change.value.asked <= 0) {
      emit('close');

      return;
    }

    beforeDiscard = document.activeElement;
    confirmingDiscard.value = true;
    await nextTick();
    keepEditingButton.value?.focus();
  }

  async function keepEditing() {
    confirmingDiscard.value = false;
    await nextTick();

    const back =
      beforeDiscard?.isConnected && beforeDiscard.closest?.('dialog')
        ? beforeDiscard
        : cancelButton.value;

    beforeDiscard = null;
    back?.focus();
  }

  // Focus starts on the user field, and the users it offers are read.
  onMounted(() => {
    userField.value?.focus();
    loadUsers();
  });
</script>

<style scoped>
  .builder-template-share h3 {
    font-size: 0.95rem;
    font-weight: 700;
    margin: 0.75rem 0 0.35rem;
  }

  .builder-template-share__add {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 0 0.5rem;
    align-items: start;
    margin-top: 0.75rem;
  }

  /* Add sits level with the field, under its label. */
  .builder-template-share__add-button {
    margin-top: 1.4rem;
    min-height: 2rem;
  }

  .builder-template-share__users-note {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
    margin: 0.25rem 0 0;
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }

  .builder-template-share__users-note.builder-dialog__error {
    color: var(--bx-danger);
  }

  .builder-template-share__users-note:empty {
    margin: 0;
  }

  .builder-template-share__empty {
    margin: 0;
    color: var(--bx-text-muted);
  }

  .builder-template-share__people {
    margin: 0;
    padding: 0;
    list-style: none;
    border-top: 1px solid var(--bx-border);
  }

  .builder-template-share__row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    align-items: center;
    gap: 0.25rem 0.5rem;
    padding: 0.4rem 0;
    border-bottom: 1px solid var(--bx-border);
  }

  .builder-template-share__name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .builder-template-share__row.is-removed .builder-template-share__name {
    text-decoration: line-through;
  }

  .builder-template-share__note {
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }

  .builder-template-share__server {
    margin: 0.75rem 0 0;
    padding: 0;
    border: 0;
  }

  .builder-template-share__server legend {
    margin: 0 0 0.35rem;
    padding: 0;
    font-size: 0.95rem;
    font-weight: 700;
  }

  .builder-template-share__check {
    display: flex;
    align-items: flex-start;
    gap: 0.5rem;
    min-height: 24px;
    margin: 0.2rem 0;
    font-weight: normal;
  }

  .builder-template-share__check input {
    flex: none;
    width: 1rem;
    height: 1rem;
    margin: 0.15em 0 0;
  }

  .builder-template-share__summary {
    margin: 0.75rem 0 0.5rem;
    padding: 0.5rem 0.75rem;
    border: 2px solid var(--bx-danger);
    border-radius: var(--bx-radius);
    overflow-wrap: anywhere;
  }

  .builder-template-share__summary h3 {
    margin-top: 0;
    color: var(--bx-danger);
  }

  .builder-template-share__summary ul {
    margin: 0;
    padding-left: 1.1rem;
  }

  .builder-template-share__footer {
    margin-top: 0.75rem;
  }

  .builder-template-share__changes {
    margin: 0 0 0.4rem;
    text-align: end;
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }

  .builder-template-share__footer .builder-dialog__actions {
    align-items: center;
  }

  .builder-template-share__question {
    flex: 1 1 12rem;
    margin: 0;
    font-weight: 600;
  }

  @media (max-width: 40rem) {
    .builder-template-share__add {
      grid-template-columns: minmax(0, 1fr);
    }

    .builder-template-share__add-button {
      justify-self: start;
      margin-top: 0;
    }

    .builder-template-share__row {
      grid-template-columns: minmax(0, 1fr) auto;
    }
  }

  @media (pointer: coarse) {
    .builder-template-share .builder-button,
    .builder-template-share__check {
      min-height: 44px;
    }
  }
</style>
