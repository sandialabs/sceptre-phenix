<template>
  <div class="modal-card" style="width: auto">
    <header class="modal-card-head">
      <p class="modal-card-title mr-6">{{ this.vmName }} Labels</p>
    </header>
    <section class="modal-card-body fixed-height">
      <p class="title is-4">Notes</p>
      <b-table :data="workingNotes" ref="notesTable">
        <b-table-column field="value" label="Value" v-slot="props">
          <template v-if="canEdit()">
            <textarea
              class="textarea has-fixed-size"
              :aria-label="`Note ${props.index + 1}`"
              rows="1"
              v-model="props.row.value"
              @input="(evt) => resizeNoteTextArea(evt.target)"
              @keypress="noteKeyHandler"
              @vue:mounted="({ el }) => resizeNoteTextArea(el)"
              :ref="`note${props.index}`"></textarea>
          </template>
          <template v-else>
            {{ props.row.value }}
          </template>
        </b-table-column>
        <b-table-column v-slot="props" v-if="canEdit()" width="32px">
          <button
            type="button"
            class="plain-button"
            aria-label="Delete note"
            @click="deleteNote(props.row)">
            <font-awesome-icon icon="trash" />
          </button>
        </b-table-column>
        <template #footer v-if="canEdit()">
          <b-button
            aria-label="Add note"
            icon-right="plus"
            type="is-text"
            size="is-small"
            expanded
            @click="addNote()" />
        </template>
      </b-table>
      <hr />
      <p class="title is-4">Labels</p>
      <b-table :data="workingTags">
        <b-table-column field="key" label="Key" v-slot="props" width="192px">
          <template v-if="canEdit()">
            <b-input
              :aria-label="`Label ${props.index + 1} key`"
              v-model="props.row.key"
              v-on:keyup.enter="addTag()"
              :ref="`tagKey${props.index}`" />
          </template>
          <template v-else>
            {{ props.row.key }}
          </template>
        </b-table-column>
        <b-table-column
          field="value"
          label="Value"
          v-slot="props"
          width="512px">
          <template v-if="canEdit()">
            <b-input
              :aria-label="`Label ${props.index + 1} value`"
              v-model="props.row.value"
              v-on:keyup.enter="addTag()" />
          </template>
          <template v-else>
            {{ props.row.value }}
          </template>
        </b-table-column>
        <b-table-column v-slot="props" v-if="canEdit()" width="32px">
          <button
            type="button"
            class="plain-button"
            :aria-label="`Delete label ${props.row.key}`.trim()"
            @click="deleteTag(props.row)">
            <font-awesome-icon icon="trash" />
          </button>
        </b-table-column>
        <template #footer v-if="canEdit()">
          <b-button
            aria-label="Add tag"
            icon-right="plus"
            type="is-text"
            size="is-small"
            expanded
            @click="addTag()" />
        </template>
      </b-table>
    </section>
    <footer class="modal-card-foot buttons is-right">
      <b-button label="Close" @click="$emit('close')" />
      <b-button
        v-if="canEdit()"
        label="Save"
        type="is-primary"
        @click="save()" />
    </footer>
  </div>
</template>

<script>
  import * as vmsApi from '@/api/vms.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { roleAllowed } from '@/utils/rbac';
  import { isEqual } from 'lodash-es';

  const NOTES_KEY = '__notes_';
  export default {
    props: {
      vmName: String,
      experiment: { type: String, required: true },
      tags: Object,
    },

    data() {
      return {
        workingTags: [],
        workingNotes: [],
      };
    },

    beforeMount() {
      // copy tags object into an array of key,values for ui use
      for (const [key, value] of Object.entries(this.tags)) {
        if (key.startsWith('__')) {
          if (key.startsWith(NOTES_KEY)) {
            this.workingNotes.push({ key: key, value: value });
          }
        } else {
          this.workingTags.push({ key: key, value: value });
        }
      }
      // start with blank space; b-table draws its rows after its columns, so
      // nothing can be focused yet
      if (this.workingTags.length == 0) {
        this.workingTags.push({ key: '', value: '' });
      }
      if (this.workingNotes.length == 0) {
        this.workingNotes.push(this.blankNote());
      }
    },

    methods: {
      canEdit() {
        return roleAllowed('vms', 'patch', this.experiment + '/' + this.vmName);
      },
      deleteTag(row) {
        this.workingTags = this.workingTags.filter((e) => e !== row);
      },
      addTag() {
        this.workingTags.push({ key: '', value: '' });
        this.$nextTick(() => {
          this.$refs[`tagKey${this.workingTags.length - 1}`].focus();
        });
      },
      deleteNote(row) {
        this.workingNotes = this.workingNotes.filter((e) => e !== row);
      },
      blankNote() {
        return { key: NOTES_KEY + new Date().toISOString(), value: '' };
      },
      addNote() {
        this.workingNotes.push(this.blankNote());
        this.$nextTick(() => {
          this.$refs[`note${this.workingNotes.length - 1}`].focus();
        });
      },
      resizeNoteTextArea(textArea) {
        textArea.style.height = 'auto';
        this.$nextTick(() => {
          textArea.style.height = textArea.scrollHeight + 'px';
        });
      },
      noteKeyHandler(evt) {
        if (evt.key === 'Enter' && evt.shiftKey) {
          this.addNote();
          evt.preventDefault();
        } else {
          this.resizeNoteTextArea(evt.target);
        }
      },
      save() {
        var finalTags = {};
        for (const row of this.workingTags) {
          if (row.key == '' || row.key in finalTags) continue;
          finalTags[row.key] = row.value;
        }

        // a note with no text is left out
        for (const row of this.workingNotes) {
          if (row.value.trim() == '' || row.key in finalTags) continue;
          finalTags[row.key] = row.value;
        }

        // SET replaces every tag, so keep the hidden ones this modal does not show
        for (const [key, value] of Object.entries(this.tags)) {
          if (!key.startsWith('__') || key.startsWith(NOTES_KEY)) continue;
          if (!(key in finalTags)) finalTags[key] = value;
        }

        let update = { tag_update_mode: 'SET', tags: finalTags };

        if (isEqual(finalTags, this.tags)) {
          this.$emit('close');
          return;
        }

        vmsApi
          .updateVm(this.experiment, this.vmName, update)
          .then((response) => {
            if (response.status === 200) {
              this.$emit('saved');
              this.$emit('close');
            }
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },
    },
  };
</script>

<style scoped>
  .fixed-height {
    height: 75vh;
    overflow: auto;
  }

  textarea {
    overflow-y: hidden;
  }
</style>
