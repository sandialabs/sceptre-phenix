<template>
  <div>
    <b-modal
      v-model="fileViewerModal.active"
      @close="resetFileViewerModal"
      has-modal-card>
      <div class="modal-card" style="width: 50em">
        <header class="modal-card-head x-modal-dark">
          <p class="modal-card-title x-config-text">
            {{ fileViewerModal.title }}
          </p>
        </header>
        <section class="modal-card-body x-modal-dark">
          <div class="control">
            <textarea
              class="textarea has-fixed-size file-viewer"
              rows="30"
              v-model="fileViewerModal.contents"
              readonly></textarea>
          </div>
        </section>
        <footer class="modal-card-foot x-modal-dark buttons is-right">
          <button class="button is-dark" @click="resetFileViewerModal">
            Exit
          </button>
        </footer>
      </div>
    </b-modal>
    <div
      v-if="selectedFiles.length > 0 || filesPaginationNeeded"
      class="files-bar">
      <!-- actions on the checked files; the bar above is full here -->
      <FileSelectionActions
        v-if="selectedFiles.length > 0"
        :count="selectedFiles.length"
        :can-download="canDownloadFiles"
        :can-delete="canDeleteFiles"
        :downloading="filesDownloading"
        :deleting="filesBulkDeleting"
        @download="downloadSelectedFiles"
        @delete="confirmDeleteSelectedFiles"
        @clear="clearFileSelection" />
      <b-field
        v-if="filesPaginationNeeded"
        class="files-paginate"
        grouped
        position="is-right">
        <div class="control is-flex">
          <b-switch
            v-model="filesTable.isPaginated"
            @update:modelValue="updateFiles()"
            size="is-small"
            type="is-light"
            >Paginate</b-switch
          >
        </div>
      </b-field>
    </div>
    <b-table
      :data="files"
      :paginated="filesTable.isPaginated && filesPaginationNeeded"
      aria-next-label="Next page"
      aria-previous-label="Previous page"
      aria-page-label="Page"
      aria-current-label="Current page"
      backend-pagination
      :total="filesTable.total"
      :per-page="filesTable.perPage"
      v-model:current-page="filesTable.currentPage"
      @page-change="onFilesPageChange"
      :pagination-simple="filesTable.isPaginationSimple"
      :pagination-size="filesTable.paginationSize"
      backend-sorting
      :default-sort-direction="filesTable.defaultSortDirection"
      default-sort="date"
      @sort="onFilesSort">
      <template #empty>
        <section class="section">
          <div class="content has-text-white has-text-centered">
            {{ filesEmptyText }}
          </div>
        </section>
      </template>
      <b-table-column v-if="filesSelectable" field="multiselect" label="">
        <template v-slot:header>
          <b-tooltip label="Select/Unselect All" type="is-dark">
            <b-checkbox
              v-model="allFilesSelected"
              :indeterminate="someFilesSelected"
              :disabled="files.length == 0">
              <span class="is-sr-only">Select all files</span>
            </b-checkbox>
          </b-tooltip>
        </template>
        <template v-slot:default="props">
          <b-checkbox v-model="selectedFiles" :native-value="props.row.path">
            <span class="is-sr-only">Select file {{ props.row.path }}</span>
          </b-checkbox>
        </template>
      </b-table-column>
      <b-table-column
        field="name"
        label="Name"
        sortable
        header-class="sort-inline"
        v-slot="props">
        <template v-if="props.row.plainText">
          <b-tooltip label="view file" type="is-dark">
            <div class="field is-clickable">
              <div @click="viewFile(props.row)">
                {{ props.row.name }}
              </div>
            </div>
          </b-tooltip>
        </template>
        <template v-else-if="webSharkLink(props.row)">
          <b-tooltip label="Open in WebShark" type="is-dark">
            <div class="field is-clickable">
              <div @click="openFileInWebShark(props.row)">
                {{ props.row.name }}
              </div>
            </div>
          </b-tooltip>
        </template>
        <template v-else>
          {{ props.row.name }}
        </template>
      </b-table-column>
      <b-table-column field="path" label="Path" centered v-slot="props">
        <b-tooltip
          :label="
            '/phenix/images/' + $route.params.id + '/files/' + props.row.path
          "
          type="is-dark">
          <b-icon icon="info-circle" size="is-small" />
        </b-tooltip>
      </b-table-column>
      <b-table-column field="categories" label="Category" v-slot="props">
        <b-taglist>
          <b-tag
            v-for="(c, index) in props.row.categories"
            :key="index"
            type="is-light"
            >{{ c }}</b-tag
          >
        </b-taglist>
      </b-table-column>
      <b-table-column
        field="date"
        label="Date"
        sortable
        header-class="sort-inline"
        centered
        v-slot="props">
        {{ props.row.date }}
      </b-table-column>
      <b-table-column
        field="size"
        label="Size"
        sortable
        header-class="sort-inline"
        centered
        v-slot="props">
        {{ formatFileSize(props.row.size) }}
      </b-table-column>
      <b-table-column
        field="actions"
        label="Actions"
        centered
        cell-class="nowrap-cell"
        v-slot="props">
        <div class="file-actions">
          <!-- first, the button that opens the file to read it; rows
               with no such button keep an invisible one in its place so
               every row's download button lines up -->
          <b-tooltip
            v-if="webSharkLink(props.row)"
            label="Open in WebShark"
            type="is-dark">
            <b-button
              tag="router-link"
              :to="webSharkLink(props.row)"
              class="button is-light is-small action file-webshark"
              :icon-left="captureIcon"
              :aria-label="`Open ${props.row.name} in WebShark`">
            </b-button>
          </b-tooltip>
          <b-tooltip
            v-else-if="props.row.plainText"
            label="view file"
            type="is-dark">
            <b-button
              class="button is-light is-small action file-view"
              icon-left="file-alt"
              :aria-label="`View ${props.row.name}`"
              @click="viewFile(props.row)">
            </b-button>
          </b-tooltip>
          <b-tooltip v-else :active="false">
            <b-button
              class="button is-light is-small action file-action-placeholder"
              icon-left="file-alt"
              tabindex="-1"
              aria-hidden="true">
            </b-button>
          </b-tooltip>
          <b-tooltip label="download" type="is-dark">
            <b-button
              class="button is-light is-small action"
              icon-left="file-download"
              :aria-label="`Download ${props.row.name}`"
              @click="downloadFile(props.row)">
            </b-button>
          </b-tooltip>
          <b-tooltip v-if="canDeleteFiles" label="delete" type="is-dark">
            <b-button
              class="button is-danger is-small action file-delete"
              icon-left="trash"
              :aria-label="`Delete ${props.row.name}`"
              :loading="isDeletingFile(props.row)"
              :disabled="filesBulkDeleting"
              @click="confirmDeleteFile(props.row)">
            </b-button>
          </b-tooltip>
        </div>
      </b-table-column>
    </b-table>
  </div>
</template>

<script>
  import FileSelectionActions from '@/components/experiment/FileSelectionActions.vue';
  import { CAPTURE_ICON } from '@/components/experiment/vmActions.js';
  import { usePhenixStore } from '@/store.js';
  import axiosInstance from '@/utils/axios.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import {
    deleteExperimentFile,
    deleteExperimentFiles,
    downloadExperimentFiles,
    keepListed,
  } from '@/utils/experimentFiles.js';
  import { fileText } from '@/utils/fileText.js';
  import { formattingMixin } from '@/utils/formattingMixin.js';
  import { plural } from '@/utils/plural.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { useTable } from '@/utils/useTable.js';
  import { isCaptureFile, webSharkInstalled } from '@/utils/webshark.js';

  // failures listed by name after deleting several files; the rest are counted
  const LISTED_FAILURES = 10;

  // The Files tab of the running and stopped experiment pages: listing,
  // viewing, downloading and deleting the experiment's files. The search box
  // and category picker sit in the page's header, so the page passes them in
  // and calls reload() when either changes or the tab opens.
  export default {
    name: 'ExperimentFilesTab',
    components: { FileSelectionActions },
    mixins: [formattingMixin],
    props: {
      filter: { type: String, default: '' },
      category: { type: String, default: null },
      // the name the table's Paginate setting is remembered under
      paginateKey: { type: String, required: true },
    },
    // categories: every category listed so far, for the page's picker
    // found: a search matched files, for the page's search history
    // waiting: whether a file is being fetched to view
    emits: ['categories', 'found', 'waiting'],

    setup(props) {
      const { table } = useTable({
        name: props.paginateKey,
        defaultSortDirection: 'desc',
        fields: { total: 0, sortColumn: 'date' },
      });
      return { filesTable: table };
    },

    data() {
      return {
        categories: [],
        files: [],
        filesLoaded: false,
        fileViewerModal: {
          active: false,
          title: null,
          contents: null,
        },
        // paths of the checked rows
        selectedFiles: [],
        // paths of the rows being deleted one at a time
        deletingFiles: [],
        filesDownloading: false,
        filesBulkDeleting: false,
      };
    },

    computed: {
      canListFiles() {
        return roleAllowed('experiments/files', 'list', this.$route.params.id);
      },

      canDownloadFiles() {
        return roleAllowed('experiments/files', 'get', this.$route.params.id);
      },

      canDeleteFiles() {
        return roleAllowed(
          'experiments/files',
          'delete',
          this.$route.params.id,
        );
      },

      // capture files open in WebShark with the icon that starts captures
      captureIcon() {
        return CAPTURE_ICON;
      },

      // whether capture files get an Open in WebShark button
      canOpenInWebShark() {
        return (
          webSharkInstalled(usePhenixStore().features) && this.canDownloadFiles
        );
      },

      // whether the table has its checkbox column
      filesSelectable() {
        return this.canDownloadFiles || this.canDeleteFiles;
      },

      // the header checkbox: checks or clears every row shown
      allFilesSelected: {
        get() {
          return (
            this.files.length > 0 &&
            this.files.every((f) => this.selectedFiles.includes(f.path))
          );
        },
        set(on) {
          this.selectedFiles = on ? this.files.map((f) => f.path) : [];
        },
      },

      someFilesSelected() {
        return this.selectedFiles.length > 0 && !this.allFilesSelected;
      },

      filesBusy() {
        return this.filesDownloading || this.filesBulkDeleting;
      },

      filesPaginationNeeded() {
        return this.filesTable.total > this.filesTable.perPage;
      },

      filesEmptyText() {
        if (!this.filesLoaded) return 'Loading files…';
        if (this.filter || this.category) return 'No files match your search';
        return 'This experiment has no files yet';
      },
    },

    methods: {
      // lists the files once the page's new search or category has reached
      // this component
      async reload() {
        await this.$nextTick();
        this.updateFiles();
      },

      updateFiles() {
        if (!this.canListFiles) {
          return;
        }

        const params = {
          filter: this.filter,
          sortCol: this.filesTable.sortColumn,
          sortDir: this.filesTable.defaultSortDirection,
        };

        if (this.filesTable.isPaginated) {
          params.pageNum = this.filesTable.currentPage;
          params.perPage = this.filesTable.perPage;
        }

        // only the newest request's answer is shown, as an older one can
        // arrive after it
        const request = (this.filesRequestID = (this.filesRequestID ?? 0) + 1);
        const current = () => request === this.filesRequestID;

        axiosInstance
          .get('experiments/' + this.$route.params.id + '/files', { params })
          .then(
            (response) => {
              if (!current()) return;
              this.filesLoaded = true;
              const files = response.data.files ?? [];
              this.filesTable.total = response.data.total ?? files.length;

              this.categories = this.getUniqueItems([
                ...this.categories,
                ...files.flatMap((f) => f.categories ?? []),
              ]);
              this.$emit('categories', this.categories);

              this.files = this.category
                ? files.filter((f) => f.categories?.includes(this.category))
                : files;

              this.pruneFileSelection();

              if (this.filter && this.files.length > 0) {
                this.$emit('found');
              }
            },
            (err) => {
              if (!current()) return;
              useErrorNotification(err);
              this.filesLoaded = true;
            },
          );
      },

      onFilesPageChange(page) {
        this.filesTable.currentPage = page;
        this.updateFiles();
      },

      onFilesSort(column, order) {
        this.filesTable.sortColumn = column;
        this.filesTable.defaultSortDirection = order;
        this.updateFiles();
      },

      viewFile(file) {
        this.$emit('waiting', true);

        axiosInstance
          .get(
            `experiments/${this.$route.params.id}/files/${encodeURIComponent(file.name)}`,
            {
              params: { path: file.path },
              headers: { Accept: 'text/plain' },
              // keep JSON files as text for fileText, which indents them,
              // rather than letting axios parse them
              responseType: 'text',
            },
          )
          .then(
            (response) => {
              this.fileViewerModal.title = file.path;
              this.fileViewerModal.contents = fileText(
                file.name,
                response.data,
              );
              this.fileViewerModal.active = true;
            },
            (err) => {
              useErrorNotification(err);
            },
          )
          .finally(() => {
            this.$emit('waiting', false);
          });
      },

      resetFileViewerModal() {
        this.fileViewerModal.active = false;
        this.fileViewerModal.title = null;
        this.fileViewerModal.contents = null;
      },

      downloadFile(file) {
        const url = `${import.meta.env.BASE_URL}api/v1/experiments/${this.$route.params.id}/files/${encodeURIComponent(file.name)}`;
        const queryParams = new URLSearchParams({
          path: file.path,
          token: usePhenixStore().token,
        });

        window.open(`${url}?${queryParams}`, '_blank');
      },

      // the WebShark page for a row's capture file, or null when the row is
      // no capture file or WebShark cannot open it
      webSharkLink(file) {
        if (
          !this.canOpenInWebShark ||
          file.isDir ||
          !isCaptureFile(file.name)
        ) {
          return null;
        }
        return {
          name: 'webshark',
          query: { exp: this.$route.params.id, file: file.path },
        };
      },

      openFileInWebShark(file) {
        this.$router.push(this.webSharkLink(file));
      },

      // drops selected rows the list no longer shows (another page, search,
      // or category, or deleted), so the toolbar acts only on rows one can
      // see
      pruneFileSelection() {
        this.selectedFiles = keepListed(this.selectedFiles, this.files);
      },

      clearFileSelection() {
        this.selectedFiles = [];
      },

      isDeletingFile(file) {
        return this.deletingFiles.includes(file.path);
      },

      // forgets deleted rows at once; the list is asked for again after
      removeFileRows(paths) {
        const gone = new Set(paths);
        const before = this.files.length;
        this.files = this.files.filter((f) => !gone.has(f.path));
        this.filesTable.total = Math.max(
          0,
          this.filesTable.total - (before - this.files.length),
        );
        this.selectedFiles = this.selectedFiles.filter((p) => !gone.has(p));
      },

      confirmDeleteFile(file) {
        this.$buefy.dialog.confirm({
          title: 'Delete File',
          message:
            `Delete <b>${escapeHTML(file.name)}</b>` +
            (file.path != file.name ? ` (${escapeHTML(file.path)})` : '') +
            ` from experiment ${escapeHTML(this.$route.params.id)}?` +
            ' It is removed from every cluster node and cannot be recovered.',
          cancelText: 'Cancel',
          confirmText: 'Delete',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => this.deleteFile(file),
        });
      },

      async deleteFile(file) {
        if (this.isDeletingFile(file)) return;
        this.deletingFiles = [...this.deletingFiles, file.path];

        try {
          await deleteExperimentFile(this.$route.params.id, file);
          this.removeFileRows([file.path]);
          this.$buefy.toast.open({
            message: `Deleted ${file.name}`,
            type: 'is-success',
            duration: 4000,
          });
          this.updateFiles();
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.deletingFiles = this.deletingFiles.filter((p) => p != file.path);
        }
      },

      confirmDeleteSelectedFiles() {
        const count = this.selectedFiles.length;
        if (count == 0) return;

        this.$buefy.dialog.confirm({
          title: 'Delete Files',
          message:
            `Delete <b>${plural(count, 'selected file')}</b> from experiment` +
            ` ${escapeHTML(this.$route.params.id)}? They are removed from every` +
            ' cluster node and cannot be recovered.',
          cancelText: 'Cancel',
          confirmText: `Delete ${plural(count, 'File')}`,
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => this.deleteSelectedFiles(),
        });
      },

      async deleteSelectedFiles() {
        const paths = [...this.selectedFiles];
        if (paths.length == 0 || this.filesBusy) return;
        this.filesBulkDeleting = true;

        try {
          const { deleted, failed } = await deleteExperimentFiles(
            this.$route.params.id,
            paths,
          );
          this.removeFileRows(deleted);

          if (failed.length == 0) {
            this.$buefy.toast.open({
              message: `Deleted ${plural(deleted.length, 'file')}`,
              type: 'is-success',
              duration: 4000,
            });
          } else {
            // the failed rows stay selected, to retry or download
            const lines = failed
              .slice(0, LISTED_FAILURES)
              .map((f) => `${f.path}: ${f.error}`);
            if (failed.length > LISTED_FAILURES) {
              lines.push(`and ${failed.length - LISTED_FAILURES} more`);
            }
            showError(
              `Deleted ${deleted.length} of ${plural(paths.length, 'file')};` +
                ` ${failed.length} could not be deleted`,
              lines.join('\n'),
            );
          }
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.filesBulkDeleting = false;
          // whatever happened, show what is there now
          this.updateFiles();
        }
      },

      async downloadSelectedFiles() {
        const paths = [...this.selectedFiles];
        if (paths.length == 0 || this.filesBusy) return;
        this.filesDownloading = true;

        try {
          await downloadExperimentFiles(this.$route.params.id, paths);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.filesDownloading = false;
        }
      },
    },
  };
</script>

<style scoped>
  .file-actions {
    display: inline-flex;
    gap: 5px;
  }

  .file-action-placeholder {
    visibility: hidden;
  }

  .files-bar {
    display: flex;
    align-items: center;
    margin-bottom: 0.75rem;
  }

  .files-bar > .field {
    margin-bottom: 0;
  }

  .files-paginate {
    margin-left: auto;
  }

  /* the configs viewer's colors */
  .file-viewer {
    background-color: #686868;
    color: whitesmoke;
    font-family: monospace;
    white-space: pre;
  }
</style>
