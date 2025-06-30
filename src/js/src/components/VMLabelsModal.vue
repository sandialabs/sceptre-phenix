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
              rows="1"
              v-model="props.row.value"
              @input="resizeNoteTextArea(`note${props.index}`)"
              @keypress="(evt) => noteKeyHandler(evt, `note${props.index}`)"
              :ref="`note${props.index}`"></textarea>
          </template>
          <template v-else>
            {{ props.row.value }}
          </template>
        </b-table-column>
        <b-table-column v-slot="props" v-if="canEdit()" width="32px">
          <div @click="deleteNote(props.row)" class="is-clickable">
            <font-awesome-icon icon="trash" />
          </div>
        </b-table-column>
        <template #footer v-if="canEdit()">
          <b-button
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
              v-model="props.row.key"
              v-on:keyup.native.enter="addTag()"
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
              v-model="props.row.value"
              v-on:keyup.native.enter="addTag()" />
          </template>
          <template v-else>
            {{ props.row.value }}
          </template>
        </b-table-column>
        <b-table-column v-slot="props" v-if="canEdit()" width="32px">
          <div @click="deleteTag(props.row)" class="is-clickable">
            <font-awesome-icon icon="trash" />
          </div>
        </b-table-column>
        <template #footer v-if="canEdit()">
          <b-button
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
  import axiosInstance from '@/utils/axios.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { roleAllowed } from '@/utils/rbac';

  const NOTES_KEY = '__notes_';
  export default {
    props: {
      vmName: String,
      experiment: String,
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
      // start with blank space
      if (this.workingTags.length == 0) {
        this.addTag();
      }
      if (this.workingNotes.length == 0) {
        this.addNote();
      }

      this.$nextTick(() => {
        for (let i = 0; i < this.workingNotes.length; i++) {
          this.resizeNoteTextArea(`note${i}`);
        }
      });
    },

    methods: {
      canEdit() {
        return roleAllowed(
          'vms',
          'patch',
          this.$route.params.id + '/' + this.vmName,
        );
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
      addNote() {
        this.workingNotes.push({
          key: NOTES_KEY + new Date().toISOString(),
          value: '',
        });
        this.$nextTick(() => {
          this.$refs[`note${this.workingNotes.length - 1}`].focus();
        });
      },
      resizeNoteTextArea(ref) {
        const textArea = this.$refs[ref];
        textArea.style.height = 'auto';
        this.$nextTick(() => {
          textArea.style.height = textArea.scrollHeight + 'px';
        });
      },
      noteKeyHandler(evt, ref) {
        if (evt.key === 'Enter' && evt.shiftKey) {
          this.addNote();
          evt.preventDefault();
        } else {
          this.resizeNoteTextArea(ref);
        }
      },
      save() {
        var finalTags = {};
        for (const row of this.workingTags) {
          if (row.key == '' || row.key in finalTags) continue;
          finalTags[row.key] = row.value;
        }

        for (const row of this.workingNotes) {
          if (row.key == '' || row.key in finalTags) continue;
          finalTags[row.key] = row.value;
        }

        let update = { tag_update_mode: 'SET', tags: finalTags };

        if (_.isEqual(finalTags, this.tags)) {
          console.log('No change made. Closing');
          this.$emit('close');
          return;
        }

        axiosInstance
          .patch(
            'experiments/' + this.$route.params.id + '/vms/' + this.vmName,
            update,
          )
          .then((response) => {
            if (response.ok) {
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
