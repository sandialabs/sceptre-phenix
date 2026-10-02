<!--
  Share a draft: who else may open it, and whether they can view or edit it.

  Only the owner opens this dialog. It reads the share list when it opens
  (showing the one the draft was listed with meanwhile), edits a copy of it
  as rows (see share.js), and saves the whole list at once. Nothing changes
  until Save: a change of access or a Remove only marks its row, and Remove
  turns into Keep in place, so focus never jumps. A share whose account was
  removed starts marked for removal.

  People are added from the users the owner may share the draft with (see
  loadShareCandidates in store.js), less those listed already, through the
  user field (BuilderUserCombobox.vue). Until the users are read, or when
  they cannot be, a username can still be typed; the server checks it on
  save.

  The page behind the dialog is inert, so its status and alert regions are
  its own. A save that meets a list changed elsewhere (412) reads the list
  again and puts the user's changes on it; a refused name (422) is listed in
  an error summary; nothing typed is lost either way. Cancel or Escape with
  changes asks first, in the footer.
-->
<template>
  <builder-dialog
    class="builder-share"
    :title="`Share ${target.name}`"
    title-id="share-dialog-title"
    describedby="share-intro"
    data-testid="share-dialog"
    @close="requestClose">
    <p id="share-intro" class="builder-hint">
      People you add find this draft under Shared Drafts and can see all of it,
      including its history. Their role must also allow viewing or changing
      configs. Can edit lets them change, undo and publish under their own
      permissions, and replace older history. Only you can delete this draft or
      change who has access.
    </p>

    <form
      v-if="phase !== 'locked'"
      class="builder-share__add"
      novalidate
      @submit.prevent="add">
      <div class="builder-field builder-share__user">
        <label for="share-user">User</label>
        <builder-user-combobox
          id="share-user"
          ref="userField"
          v-model="addName"
          :users="users"
          :exclude="listed"
          :disabled="phase === 'saving'"
          :invalid="fieldError.field === 'user'"
          :describedby="userDescription"
          @update:model-value="fieldError.clear()"
          @submit="add" />
        <p
          id="share-users-note"
          class="builder-share__users-note"
          :class="{ 'builder-dialog__error': usersPhase === 'failed' }"
          role="status"
          data-testid="share-users-note">
          <template v-if="usersPhase === 'loading'">
            <span class="builder-toolbar__spinner" aria-hidden="true"></span>
            <span>Loading users…</span>
          </template>
          <template v-else-if="usersPhase === 'failed'">
            <span :key="usersError.key">{{ usersError.text }}</span>
            <button
              type="button"
              class="builder-button builder-share__retry"
              data-testid="share-users-retry"
              @click="retryUsers">
              Retry
            </button>
          </template>
          <span v-else-if="noneLeft">{{ NONE_LEFT }}</span>
        </p>
        <!-- An alert: Enter in the field leaves focus where it is, so the
             field's new description would not be read. -->
        <p
          id="share-user-error"
          class="builder-dialog__message builder-dialog__error"
          role="alert"
          data-testid="share-user-error">
          <span v-if="fieldError.text" :key="fieldError.key">{{
            fieldError.text
          }}</span>
        </p>
      </div>

      <div class="builder-field builder-share__access">
        <label for="share-access">Access</label>
        <select
          id="share-access"
          v-model="addAccess"
          :disabled="phase === 'saving'"
          data-testid="share-access">
          <option value="view">{{ ACCESS_LABELS.view }}</option>
          <option value="edit">{{ ACCESS_LABELS.edit }}</option>
        </select>
      </div>

      <button
        type="submit"
        class="builder-button builder-share__add-button"
        data-testid="share-add"
        :aria-disabled="phase !== 'ready' || noneLeft || undefined"
        :aria-describedby="noneLeft ? 'share-users-note' : undefined">
        <builder-icon name="plus" :size="14" />
        Add
      </button>
    </form>

    <!-- A refused save lists each problem; each one's button goes to its
         row. -->
    <div
      v-if="refusals.length"
      ref="summaryEl"
      class="builder-share__summary"
      role="alert"
      tabindex="-1"
      aria-labelledby="share-summary-title"
      data-testid="share-summary">
      <h3 id="share-summary-title">Some people could not be added.</h3>
      <ul>
        <li v-for="(refusal, index) in refusals" :key="index">
          <button
            v-if="rowFor(refusal.user)"
            type="button"
            class="builder-share__link"
            @click="focusRow(refusal.user)">
            {{ refusal.text }}
          </button>
          <template v-else>{{ refusal.text }}</template>
        </li>
      </ul>
    </div>

    <div
      id="share-alert"
      ref="alertEl"
      class="builder-dialog__message builder-dialog__error"
      role="alert"
      tabindex="-1"
      data-testid="share-alert">
      <template v-if="alert.text">
        <span :key="alert.key">{{ alert.text }}</span>
        <button
          v-if="loadFailed"
          type="button"
          class="builder-button builder-share__retry"
          data-testid="share-retry"
          :aria-disabled="phase === 'loading' || undefined"
          @click="load">
          Retry
        </button>
      </template>
    </div>

    <h3 id="share-people-title" class="builder-share__heading">
      People with access
    </h3>
    <p class="builder-share__loading" role="status" data-testid="share-loading">
      <template v-if="phase === 'loading'">
        <span class="builder-toolbar__spinner" aria-hidden="true"></span>
        Loading who has access…
      </template>
    </p>
    <ul
      class="builder-share__people"
      aria-labelledby="share-people-title"
      :aria-busy="phase === 'loading' || phase === 'saving' || undefined"
      data-testid="share-people">
      <li class="builder-share__row builder-share__row--owner">
        <span class="builder-share__name">{{ target.owner }} (you)</span>
        <span class="builder-share__note">Owner</span>
      </li>
      <li
        v-for="row in rows"
        :key="row.key"
        class="builder-share__row"
        :class="{ 'is-removed': row.removed }"
        :data-testid="`share-row-${row.user}`">
        <span class="builder-share__name">{{ row.user }}</span>
        <span
          v-if="rowNote(base, row)"
          :id="`${row.key}-note`"
          class="builder-share__note"
          :data-change="rowChange(base, row) || undefined">
          {{ rowNote(base, row) }}
        </span>
        <span class="builder-share__controls">
          <select
            :id="`${row.key}-access`"
            :aria-label="`Access for ${row.user}`"
            :aria-describedby="rowDescription(row)"
            :aria-invalid="rowErrors[row.user] ? 'true' : undefined"
            :disabled="row.stale || row.removed || phase !== 'ready'"
            :data-testid="`share-row-access-${row.user}`"
            @change="setAccess(row, $event.target.value)">
            <option value="view" :selected="row.access === 'view'">
              {{ ACCESS_LABELS.view }}
            </option>
            <option value="edit" :selected="row.access === 'edit'">
              {{ ACCESS_LABELS.edit }}
            </option>
          </select>
          <!-- One button that turns into the other, so focus stays on it. -->
          <button
            :id="`${row.key}-remove`"
            type="button"
            class="builder-button"
            :aria-label="`${row.removed ? 'Keep' : 'Remove'} ${row.user}`"
            :aria-describedby="rowDescription(row)"
            :aria-disabled="row.stale || phase !== 'ready' || undefined"
            :data-testid="`share-row-remove-${row.user}`"
            @click="toggleRemove(row)">
            {{ row.removed ? 'Keep' : 'Remove' }}
          </button>
        </span>
        <p
          v-if="rowErrors[row.user]"
          :id="`${row.key}-error`"
          class="builder-share__row-error builder-dialog__error">
          {{ rowErrors[row.user] }}
        </p>
      </li>
    </ul>

    <div class="builder-share__link-row">
      <button
        type="button"
        class="builder-button"
        data-testid="share-copy-link"
        @click="copyLink">
        Copy link
      </button>
      <p
        class="builder-dialog__message builder-share__copied"
        role="status"
        data-testid="share-copy-status">
        <span v-if="copyStatus.text" :key="copyStatus.key">{{
          copyStatus.text
        }}</span>
      </p>
    </div>
    <div v-if="linkShown" class="builder-field">
      <label for="share-link">Link to this draft</label>
      <input
        id="share-link"
        ref="linkField"
        type="text"
        readonly
        :value="target.link"
        data-testid="share-link" />
    </div>

    <p class="builder-visually-hidden" role="status" data-testid="share-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>

    <div class="builder-share__footer">
      <p
        v-if="changes && phase !== 'locked'"
        id="share-changes"
        class="builder-share__changes"
        data-testid="share-changes">
        {{ count(changes, 'change') }} not saved
      </p>
      <div
        v-if="confirmingDiscard"
        class="builder-share__actions"
        role="group"
        aria-labelledby="share-discard-question"
        data-testid="share-discard">
        <p id="share-discard-question" class="builder-share__question">
          Discard {{ count(userChanges, 'unsaved change') }}?
        </p>
        <button
          ref="keepEditingButton"
          type="button"
          class="builder-button"
          data-testid="share-keep-editing"
          @click="keepEditing">
          Keep editing
        </button>
        <button
          type="button"
          class="builder-button builder-button--danger"
          data-testid="share-discard-confirm"
          @click="$emit('close')">
          Discard
        </button>
      </div>
      <div v-else class="builder-share__actions">
        <button
          ref="cancelButton"
          type="button"
          class="builder-button"
          data-testid="share-cancel"
          @click="requestClose">
          {{ phase === 'locked' ? 'Close' : 'Cancel' }}
        </button>
        <button
          v-if="phase !== 'locked'"
          type="button"
          class="builder-button builder-button--primary"
          data-testid="share-save"
          :aria-describedby="changes ? 'share-changes' : undefined"
          :aria-disabled="phase !== 'ready' || !changes || undefined"
          @click="save">
          {{ phase === 'saving' ? 'Saving…' : 'Save' }}
        </button>
      </div>
    </div>
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import BuilderUserCombobox from '../BuilderUserCombobox.vue';
  import { useMessage } from './message.js';

  import { count } from '@/builder/announce.js';
  import { errorMessage, serverReason, shareErrors } from '@/builder/api.js';
  import { findUser, userOptions } from '@/builder/combobox.js';
  import { detectPlatform } from '@/builder/keymap.js';
  import {
    ACCESS_LABELS,
    addedMessage,
    changeCount,
    mergeShares,
    reasonMessage,
    rowChange,
    rowNote,
    rowsOf,
    savedMessage,
    sharesOf,
    validateAdd,
  } from '@/builder/share.js';
  import { signIn } from '@/builder/signin.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { usePhenixStore } from '@/store.js';

  const props = defineProps({
    // The draft: {owner, id, name, link}, and shares, the list it was
    // listed with, shown until the dialog reads the list itself.
    target: { type: Object, required: true },
  });

  const emit = defineEmits(['close']);

  const store = useBuilderStore();

  const LOCKED = 'You can no longer change sharing for this draft.';
  const NONE_LEFT = 'No other users to share with.';
  const CHANGED_ELSEWHERE =
    'Sharing for this draft changed in another tab or window. The list shows the latest access with your changes applied. Check it and save again.';

  // loading, failed (to read the list), ready, saving, or locked: the
  // list cannot be changed any more.
  const phase = ref('loading');
  // Whether the last read failed: Retry stays, and keeps focus, while it
  // reads again.
  const loadFailed = ref(false);
  // Whether the list is being read.
  let reading = false;
  // Each user's row keeps its key, and so its element and focus, when the
  // list is read again. Usernames may hold spaces, so ids are made up.
  const keys = new Map();

  // The list as last read, and the rows the dialog edits over it.
  const base = ref(props.target.shares || []);
  const rows = ref(keyed(rowsOf(base.value)));
  const sharesEtag = ref(null);
  const maxShares = ref(undefined);

  const addName = ref('');
  const addAccess = ref('view');
  const fieldError = useMessage();
  const alert = useMessage();
  const status = useMessage();
  const copyStatus = useMessage();
  // Why the server refused each person, by user, and the list of them.
  const rowErrors = reactive({});
  const refusals = ref([]);

  const confirmingDiscard = ref(false);
  const linkShown = ref(false);

  // The user field: it takes focus, and closes its list (see
  // BuilderUserCombobox.vue).
  const userField = ref(null);
  const alertEl = ref(null);
  const summaryEl = ref(null);
  const cancelButton = ref(null);
  const keepEditingButton = ref(null);
  const linkField = ref(null);
  // Where focus was when Cancel or Escape asked first.
  let beforeDiscard = null;

  // --- rows ------------------------------------------------------------

  function keyed(list) {
    return list.map((row) => {
      if (!keys.has(row.user)) {
        keys.set(row.user, `share-row-${keys.size}`);
      }

      return { ...row, key: keys.get(row.user) };
    });
  }

  function rowFor(user) {
    return rows.value.find((row) => row.user === user);
  }

  const changes = computed(() => changeCount(base.value, rows.value));
  // Stale shares start marked removed; leaving without saving that is
  // nothing to ask about.
  const userChanges = computed(
    () => changes.value - rows.value.filter((row) => row.stale).length,
  );

  function rowDescription(row) {
    return (
      [
        rowNote(base.value, row) ? `${row.key}-note` : '',
        rowErrors[row.user] ? `${row.key}-error` : '',
        fieldError.field === `row:${row.user}` ? 'share-user-error' : '',
      ]
        .filter(Boolean)
        .join(' ') || undefined
    );
  }

  function setAccess(row, access) {
    row.access = access === 'edit' ? 'edit' : 'view';
    delete rowErrors[row.user];
  }

  function toggleRemove(row) {
    if (row.stale || phase.value !== 'ready') {
      return;
    }

    row.removed = !row.removed;
    delete rowErrors[row.user];

    const added = rowChange(base.value, { ...row, removed: false }) === 'added';

    if (!row.removed) {
      status.set(`${row.user} kept.`);
    } else {
      status.set(
        added
          ? `${row.user} will not be added.`
          : `${row.user} will be removed when you save.`,
      );
    }
  }

  // Focus on a row's first control that takes it: its access, or its
  // Remove (Keep) when the access cannot change.
  function focusRow(user) {
    const row = rowFor(user);

    if (!row) {
      return;
    }

    const select = document.getElementById(`${row.key}-access`);

    (select && !select.disabled
      ? select
      : document.getElementById(`${row.key}-remove`)
    )?.focus();
  }

  // --- the users -------------------------------------------------------

  // The users the draft may be shared with, once read; loading, failed or
  // ready.
  const users = ref(null);
  const usersPhase = ref('loading');
  const usersError = useMessage();

  // Anyone listed already, the owner and the user are left out. Someone
  // marked removed is listed again: adding them keeps them.
  const listed = computed(() => [
    props.target.owner,
    usePhenixStore().username,
    ...rows.value
      .filter((row) => !row.removed || row.stale)
      .map((row) => row.user),
  ]);
  // Everyone the draft may be shared with has access already.
  const noneLeft = computed(
    () =>
      usersPhase.value === 'ready' &&
      userOptions(users.value, '', { exclude: listed.value }).length === 0,
  );
  // The usernames Add checks against, once they are known.
  const knownUsers = computed(() =>
    users.value ? users.value.map((user) => user.username) : null,
  );

  const userDescription = computed(
    () =>
      [
        usersPhase.value !== 'ready' || noneLeft.value
          ? 'share-users-note'
          : '',
        fieldError.text ? 'share-user-error' : '',
      ]
        .filter(Boolean)
        .join(' ') || undefined,
  );

  async function loadUsers() {
    usersPhase.value = 'loading';

    try {
      users.value = await store.loadShareCandidates(props.target);
      usersPhase.value = 'ready';
    } catch (error) {
      const kind = classify(error);

      // The owner may no longer share it: the share list says so too.
      if (kind === 'forbidden' || kind === 'missing') {
        usersPhase.value = 'ready';
        await lock();

        return;
      }

      usersPhase.value = 'failed';
      usersError.set(
        `Could not load users. ${
          error instanceof TypeError ? error.message : errorMessage(kind, error)
        }`,
      );
    }
  }

  // Retry goes while the users are read, so focus moves to the field first.
  function retryUsers() {
    userField.value?.focus();
    loadUsers();
  }

  // --- adding ----------------------------------------------------------

  async function add() {
    // Adding checks against the list, so it waits for one; the field's
    // alert says why nothing was added, and the name stays for later.
    if (phase.value === 'loading' || phase.value === 'failed') {
      const why =
        phase.value === 'loading'
          ? 'Wait until the list has loaded.'
          : 'Press Retry to load the list first.';

      userField.value?.closeList();
      fieldError.set(why, 'wait');

      return;
    }

    if (phase.value !== 'ready') {
      return;
    }

    userField.value?.closeList();

    if (noneLeft.value) {
      status.set(NONE_LEFT);

      return;
    }

    // A user chosen from the list is named by their label.
    const check = validateAdd(
      findUser(users.value, addName.value)?.username ?? addName.value,
      rows.value,
      props.target.owner,
      knownUsers.value,
      maxShares.value,
    );

    if (check.error) {
      // Someone listed already: their access is what to change, so it takes
      // focus, described by the message.
      const wanted = check.row && !check.row.stale && !check.row.removed;

      fieldError.set(check.error, wanted ? `row:${check.user}` : 'user');
      await nextTick();
      (wanted
        ? document.getElementById(`${check.row.key}-access`)
        : userField.value
      )?.focus();

      return;
    }

    if (check.row) {
      check.row.removed = false;
      check.row.access = addAccess.value;
    } else {
      rows.value.push(
        ...keyed([
          {
            user: check.user,
            access: addAccess.value,
            stale: false,
            removed: false,
          },
        ]),
      );
    }

    delete rowErrors[check.user];
    fieldError.clear();
    addName.value = '';
    status.set(addedMessage(check.user, addAccess.value));
    // Each person starts at the least access; edit is chosen every time.
    addAccess.value = 'view';
    userField.value?.focus();
  }

  // --- reading and saving ----------------------------------------------

  function show(list) {
    base.value = list.shares;
    rows.value = keyed(rowsOf(list.shares));
    sharesEtag.value = list.sharesEtag;
    maxShares.value = list.maxShares;
  }

  // Focus that was in the dialog, on something no longer there, goes to
  // the username field rather than to <body>.
  async function keepFocusInDialog() {
    await nextTick();

    const panel = alertEl.value?.closest('dialog');

    if (panel && !panel.contains(document.activeElement)) {
      (userField.value || cancelButton.value)?.focus();
    }
  }

  async function lock() {
    phase.value = 'locked';
    loadFailed.value = false;
    alert.set(LOCKED);
    await nextTick();
    alertEl.value?.focus();
  }

  async function load() {
    if (phase.value !== 'loading' && phase.value !== 'failed') {
      return;
    }

    if (reading) {
      return;
    }

    const retried = phase.value === 'failed';

    reading = true;
    phase.value = 'loading';

    try {
      show(await store.loadShares(props.target));
      phase.value = 'ready';
      loadFailed.value = false;
      alert.clear();

      if (fieldError.field === 'wait') {
        fieldError.clear();
      }

      if (retried) {
        // Retry is gone with the alert.
        await nextTick();
        userField.value?.focus();
      } else {
        await keepFocusInDialog();
      }
    } catch (error) {
      const kind = classify(error);

      if (kind === 'forbidden' || kind === 'missing') {
        await lock();

        return;
      }

      // Nothing is saved over a list that could not be read.
      phase.value = 'failed';
      loadFailed.value = true;
      alert.set(`Could not load who has access. ${errorMessage(kind, error)}`);
    } finally {
      reading = false;
    }
  }

  // Only what the dialog tells apart: see classifyError in api.js.
  function classify(error) {
    const code = error?.response?.status;

    if (code === 403) {
      return 'forbidden';
    }

    if (code === 404) {
      return 'missing';
    }

    if (code === 401) {
      return 'unauthenticated';
    }

    return error?.response ? 'error' : 'offline';
  }

  // After a 412: the list as it is now, with the user's changes on it.
  async function recover() {
    try {
      const fresh = await store.loadShares(props.target);
      const merged = mergeShares(fresh.shares, base.value, rows.value);

      base.value = fresh.shares;
      rows.value = keyed(merged);
      sharesEtag.value = fresh.sharesEtag;
      maxShares.value = fresh.maxShares;
      phase.value = 'ready';
      alert.set(CHANGED_ELSEWHERE);
      await nextTick();
      alertEl.value?.focus();
    } catch (error) {
      const kind = classify(error);

      if (kind === 'forbidden' || kind === 'missing') {
        await lock();

        return;
      }

      phase.value = 'ready';
      alert.set('Could not save. Try again.');
    }
  }

  async function refuse(errors) {
    refusals.value = errors.map((entry) => ({
      user: entry.user,
      text: reasonMessage(entry.reason, entry.user),
    }));

    for (const entry of refusals.value) {
      if (entry.user && rowFor(entry.user)) {
        rowErrors[entry.user] = entry.text;
      }
    }

    phase.value = 'ready';
    await nextTick();
    summaryEl.value?.focus();
  }

  async function save() {
    if (phase.value !== 'ready' || !changes.value) {
      return;
    }

    phase.value = 'saving';
    alert.clear();
    refusals.value = [];

    for (const user of Object.keys(rowErrors)) {
      delete rowErrors[user];
    }

    try {
      const saved = await store.saveShares(
        props.target,
        sharesOf(rows.value),
        sharesEtag.value,
      );

      store.announce(savedMessage(props.target.name, saved.shares));
      emit('close');
    } catch (error) {
      const code = error?.response?.status;
      const errors = shareErrors(error);

      if (code === 412) {
        await recover();
      } else if (errors.length) {
        await refuse(errors);
      } else if (code === 403 || code === 404) {
        await lock();
      } else if (code === 401) {
        // Tagged, so signing in again takes it away (see below).
        phase.value = 'ready';
        alert.set(errorMessage('unauthenticated', error), 'session');
      } else {
        // A refused request says why; a server or connection failure can
        // be tried again.
        phase.value = 'ready';
        alert.set(
          `Could not save. ${(code < 500 && serverReason(error)) || 'Try again.'}`,
        );
      }
    }
  }

  // A 401 opens Sign in again over this dialog. Once the user has signed in,
  // the alert that the session ended no longer holds, and the page's
  // "Signed in again" waits behind this dialog, so its own status says it.
  // Post flush: Sign in again has closed, and this dialog can be read.
  watch(
    () => signIn.needed,
    (needed) => {
      if (!needed && alert.field === 'session') {
        alert.clear();
        status.set('Signed in again. Save to apply your changes.');
      }
    },
    { flush: 'post' },
  );

  // --- the link --------------------------------------------------------

  async function copyLink() {
    try {
      if (!globalThis.isSecureContext || !navigator.clipboard?.writeText) {
        throw new Error('The clipboard is not available.');
      }

      await navigator.clipboard.writeText(props.target.link);
      copyStatus.set('Link copied.');
    } catch {
      // Plain HTTP has no clipboard: the link is shown, selected, to copy.
      linkShown.value = true;
      await nextTick();
      linkField.value?.focus();
      linkField.value?.select();
      copyStatus.set(
        `Press ${detectPlatform() === 'mac' ? '⌘C' : 'Ctrl+C'} to copy the link.`,
      );
    }
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

    if (phase.value === 'locked' || userChanges.value <= 0) {
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

  onMounted(async () => {
    userField.value?.focus();
    await Promise.all([load(), loadUsers()]);
  });
</script>

<style scoped>
  .builder-share h3 {
    font-size: 0.95rem;
    font-weight: 700;
    margin: 0.75rem 0 0.35rem;
  }

  .builder-share__add {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    gap: 0 0.5rem;
    align-items: start;
    margin-top: 0.75rem;
  }

  /* Add sits level with the fields, under their labels. */
  .builder-share__add-button {
    margin-top: 1.4rem;
    min-height: 2rem;
  }

  .builder-share__users-note {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
    margin: 0.25rem 0 0;
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }

  .builder-share__users-note.builder-dialog__error {
    color: var(--bx-danger);
  }

  .builder-share__users-note:empty {
    margin: 0;
  }

  .builder-share__users-note .builder-share__retry {
    margin-inline-start: 0;
  }

  .builder-share__summary {
    margin: 0.5rem 0;
    padding: 0.5rem 0.75rem;
    border: 2px solid var(--bx-danger);
    border-radius: var(--bx-radius);
  }

  .builder-share__summary h3 {
    margin-top: 0;
    color: var(--bx-danger);
  }

  .builder-share__summary ul {
    margin: 0;
    padding-left: 1.1rem;
  }

  /* A problem in the summary is a button that looks like a link. */
  .builder-share__link {
    min-height: 24px;
    padding: 0;
    border: 0;
    background: none;
    color: var(--bx-accent);
    text-align: start;
    text-decoration: underline;
    cursor: pointer;
  }

  .builder-share__retry {
    margin-inline-start: 0.5rem;
  }

  .builder-share__loading {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin: 0 0 0.35rem;
    color: var(--bx-text-muted);
  }

  .builder-share__loading:empty {
    margin: 0;
  }

  .builder-share__people {
    margin: 0;
    padding: 0;
    list-style: none;
    border-top: 1px solid var(--bx-border);
  }

  .builder-share__row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    align-items: center;
    gap: 0.25rem 0.5rem;
    padding: 0.4rem 0;
    border-bottom: 1px solid var(--bx-border);
  }

  .builder-share__name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .builder-share__row.is-removed .builder-share__name {
    text-decoration: line-through;
  }

  .builder-share__note {
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }

  .builder-share__note[data-change] {
    font-style: italic;
  }

  .builder-share__controls {
    display: flex;
    gap: 0.4rem;
  }

  .builder-share__controls select {
    min-height: 2rem;
    padding: 0.25rem 0.4rem;
    border: 1px solid var(--bx-field-border);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
  }

  .builder-share__controls select[aria-invalid='true'] {
    border-color: var(--bx-danger);
    box-shadow: inset 0 0 0 1px var(--bx-danger);
  }

  .builder-share__row-error {
    grid-column: 1 / -1;
    margin: 0;
    font-size: 0.85em;
  }

  .builder-share__link-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin: 0.75rem 0 0.5rem;
  }

  .builder-share__copied {
    margin: 0;
  }

  .builder-share__footer {
    margin-top: 0.75rem;
  }

  .builder-share__changes {
    margin: 0 0 0.4rem;
    text-align: end;
    font-size: 0.85em;
    color: var(--bx-text-muted);
  }

  .builder-share__actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 0.5rem;
  }

  .builder-share__question {
    flex: 1 1 12rem;
    margin: 0;
    font-weight: 600;
  }

  /* At 40rem and narrower the dialog fills the viewport, scrolls inside,
     and keeps its footer in view; rows stack. */
  @media (max-width: 40rem) {
    .builder-share {
      box-sizing: border-box;
      width: 100%;
      max-width: none;
      height: 100%;
      max-height: none;
      margin: 0;
      padding-bottom: 0;
      border-radius: 0;
      scroll-padding-bottom: 7rem;
    }

    .builder-share__add {
      grid-template-columns: minmax(0, 1fr);
    }

    .builder-share__add-button {
      justify-self: start;
      margin-top: 0;
    }

    .builder-share__row {
      grid-template-columns: minmax(0, 1fr);
    }

    .builder-share__footer {
      position: sticky;
      bottom: 0;
      margin: 0.75rem -1rem 0;
      padding: 0.6rem 1rem 1rem;
      border-top: 1px solid var(--bx-border);
      background: var(--bx-surface);
    }
  }

  @media (pointer: coarse) {
    .builder-share .builder-button,
    .builder-share select,
    .builder-share input,
    .builder-share__link {
      min-height: 44px;
    }
  }

  @media (forced-colors: active) {
    .builder-share__link {
      color: LinkText;
    }
  }
</style>
