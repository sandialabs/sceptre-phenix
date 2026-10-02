<template>
  <div class="content">
    <!-- UPLOAD MODAL -->
    <b-modal v-model="uploader.active" has-modal-card>
      <div class="modal-card" style="width: auto">
        <header class="modal-card-head">
          <p class="modal-card-title">Upload a Disk</p>
        </header>
        <section class="modal-card-body">
          <b-field v-if="currentUploadProgress == null">
            <b-upload
              :model-value="null"
              drag-drop
              :accept="uploadAccept"
              @update:modelValue="uploadDisk">
              <section class="section">
                <div class="content has-text-centered">
                  <p>
                    <b-icon icon="upload" size="is-large"></b-icon>
                  </p>
                  <p>Drop your disk here or click to upload</p>
                  <p>(Valid file types are {{ uploadTypesText }})</p>
                </div>
              </section>
            </b-upload>
          </b-field>
          <template v-else>
            <p>Uploading {{ uploader.name }}</p>
            <b-progress
              :value="currentUploadProgress"
              show-value
              format="percent"
              type="is-success"
              size="is-medium" />
            <p class="is-size-7">
              Closing this window does not stop the upload; the upload button
              shows its progress.
            </p>
          </template>
        </section>
      </div>
    </b-modal>

    <!-- DETAILS MODAL -->
    <b-modal
      v-model="detailsModal.active"
      @close="() => (detailsModal.active = false)"
      has-modal-card>
      <div class="modal-card">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ diskLabel(detailsModal.disk) }}</p>
        </header>
        <section class="modal-card-body">
          <p class="title is-5">Details</p>
          <dl>
            <div>
              <dt>Full Path:</dt>
              <dd>{{ detailsModal.disk.fullPath }}</dd>
            </div>
            <div>
              <dt>Location:</dt>
              <dd>
                {{ locationText(detailsModal.disk) }}
                <DiskWarning
                  v-if="detailsModal.disk.outsideFilesDir"
                  reason="outside"
                  :files-dir="filesDir" />
              </dd>
            </div>
            <div>
              <dt>Kind:</dt>
              <dd>{{ detailsModal.disk.kind }}</dd>
            </div>
            <div>
              <dt>Size on Disk:</dt>
              <dd>{{ detailsModal.disk.size }}</dd>
            </div>
            <div>
              <dt>Virtual Size:</dt>
              <dd>{{ detailsModal.disk.virtualSize }}</dd>
            </div>
            <div>
              <dt>Experiments:</dt>
              <dd>{{ experimentsText(detailsModal.disk) }}</dd>
            </div>
            <div>
              <dt>In Use:</dt>
              <dd>{{ detailsModal.disk.inUse }}</dd>
            </div>
          </dl>
          <div
            v-if="
              detailsModal.disk.backingImages &&
              detailsModal.disk.backingImages.length > 0
            ">
            <hr />
            <p class="title is-5">Backing Chain</p>
            <div style="text-align: center">
              <b>{{ diskLabel(detailsModal.disk) }}</b>
              <div
                v-for="image in detailsModal.disk.backingImages"
                :key="image">
                &darr;<br />
                <button
                  v-if="findDisk(disks, image)"
                  type="button"
                  class="button is-ghost backing-link"
                  @click="openBacking(image)">
                  {{ diskLabel(findDisk(disks, image)) }}
                </button>
                <span v-else>{{ image }}</span>
              </div>
            </div>
          </div>

          <div class="actions">
            <hr />
            <p class="title is-5">Actions</p>
            <template v-for="(action, index) in diskActions" :key="action.name">
              <hr v-if="index > 0" class="action-separator" />
              <b-tooltip
                class="action-tooltip"
                :label="actionTooltip(action, detailsModal.disk)"
                :active="shouldDisableAction(action.name)"
                type="is-dark"
                multilined>
                <b-button
                  type="is-text"
                  expanded
                  @click="action.run(detailsModal.disk.fullPath)"
                  :disabled="shouldDisableAction(action.name)">
                  <b>{{ action.label }}</b>
                  <span v-if="action.description">
                    - {{ action.description }}</span
                  >
                </b-button>
              </b-tooltip>
            </template>
          </div>
        </section>
      </div>
    </b-modal>
    <!-- REBASE MODAL -->
    <b-modal v-model="rebaseModal.active" :can-cancel="false" has-modal-card>
      <div class="modal-card" style="max-width: 460px">
        <section class="modal-card-body">
          Are you sure you want to rebase this image onto a different backing
          image?<br />
          By default changes between the old and new backing images will be
          written to this image. Selecting "None" for the backing image will
          cause the image to become independent.<br />
          Selecting "Change Reference Only" will only change the backing image
          name without updating files.
          <b-select
            placeholder="New Backing Image"
            aria-label="New backing image"
            v-model="rebaseModal.dst"
            style="margin-bottom: 8px; margin-top: 16px">
            <option value="">None</option>
            <option
              v-for="d in rebaseTargets"
              :key="d.fullPath"
              :value="d.fullPath">
              {{ diskLabel(d) }}
            </option>
          </b-select>
          <b-checkbox v-model="rebaseModal.unsafe"
            >Change reference only</b-checkbox
          >
        </section>
        <footer class="modal-card-foot" style="justify-content: flex-end">
          <b-button
            label="Cancel"
            @click="() => (rebaseModal.active = false)"
            :disabled="rebaseModal.isWaiting" />
          <b-button
            label="OK"
            type="is-primary"
            :loading="rebaseModal.isWaiting"
            @click="
              () =>
                rebaseDisk(
                  detailsModal.disk.fullPath,
                  rebaseModal.dst,
                  rebaseModal.unsafe,
                )
            " />
        </footer>
      </div>
    </b-modal>
    <!-- COMMIT MODAL -->
    <b-modal v-model="commitModal.active" :can-cancel="false" has-modal-card>
      <div class="modal-card" style="max-width: 460px">
        <section class="modal-card-body">
          Are you sure you want to commit the changes in this disk to its
          parent?<br />
          By default this disk is left unchanged, but you may select to delete
          it if it's no longer needed.
          <b-field style="margin-top: 16px">
            <b-checkbox v-model="commitModal.delete"
              >Delete this disk after commit</b-checkbox
            >
          </b-field>
        </section>
        <footer class="modal-card-foot" style="justify-content: flex-end">
          <b-button
            label="Cancel"
            @click="() => (commitModal.active = false)"
            :disabled="commitModal.isWaiting" />
          <b-button
            label="OK"
            type="is-primary"
            :loading="commitModal.isWaiting"
            @click="
              () => commitDisk(detailsModal.disk.fullPath, commitModal.delete)
            " />
        </footer>
      </div>
    </b-modal>
    <!-- CONTENT -->
    <b-field grouped position="is-right" style="margin: 12px 0px">
      <div
        v-if="paginationNeeded"
        class="control is-flex is-align-items-center">
        <b-switch v-model="table.isPaginated" size="is-small" type="is-light"
          >Paginate</b-switch
        >
      </div>
      <b-field>
        <b-autocomplete
          v-model="filterString"
          placeholder="Find a disk"
          icon="search"
          @select="(option) => (selected = option)"
          :data="filteredDisks.map(diskLabel)">
        </b-autocomplete>

        <p v-if="filterString" class="control">
          <button
            class="button input-button"
            aria-label="Clear disk search"
            @click="filterString = ''">
            <b-icon icon="window-close"></b-icon>
          </button>
        </p>
      </b-field>
      <b-tooltip
        v-if="roleAllowed('disks', 'upload')"
        :label="
          currentUploadProgress == null ? 'Upload a disk' : 'Upload progress'
        "
        type="is-light is-left">
        <button
          class="button is-light"
          style="margin-left: 8px"
          aria-label="Upload a disk"
          @click="uploader.active = true">
          <b-icon v-if="currentUploadProgress == null" icon="upload"></b-icon>
          <span v-else style="width: 32px">{{ currentUploadProgress }}%</span>
        </button>
      </b-tooltip>
    </b-field>

    <b-table
      :data="filteredDisks"
      @click="rowClick"
      :row-class="(r, i) => 'is-clickable'"
      :paginated="table.isPaginated && paginationNeeded"
      aria-next-label="Next page"
      aria-previous-label="Previous page"
      aria-page-label="Page"
      aria-current-label="Current page"
      :per-page="table.perPage"
      v-model:current-page="table.currentPage"
      :pagination-simple="table.isPaginationSimple"
      :pagination-size="table.paginationSize"
      :default-sort-direction="table.defaultSortDirection"
      sortable
      hoverable
      default-sort="name">
      <template #empty>
        <section class="section">
          <div class="content has-text-white has-text-centered">
            {{ emptyText }}
          </div>
        </section>
      </template>

      <b-table-column
        field="name"
        label="Name"
        width="27%"
        sortable
        header-class="sort-inline"
        :custom-sort="sortByLabel"
        v-slot="props">
        <span class="disk-label">{{ diskLabel(props.row) }}</span>
        <span v-if="props.row.outsideFilesDir" class="name-warning" @click.stop>
          <DiskWarning reason="outside" :files-dir="filesDir" />
        </span>
      </b-table-column>

      <b-table-column
        field="kind"
        label="Kind"
        sortable
        header-class="sort-inline"
        v-slot="props">
        {{ props.row.kind }}
      </b-table-column>

      <b-table-column
        field="inUse"
        label="In Use"
        width="7em"
        centered
        sortable
        header-class="sort-inline"
        v-slot="props">
        <b-tooltip
          v-if="props.row.inUse"
          :label="`In use by ${runningText(props.row)}`"
          type="is-dark"
          multilined>
          <b-icon icon="play-circle" size="is-small" />
        </b-tooltip>
      </b-table-column>

      <b-table-column
        field="size"
        label="Size on Disk"
        sortable
        header-class="sort-inline"
        :custom-sort="sortBy('size')"
        v-slot="props">
        {{ props.row.size }}
      </b-table-column>

      <b-table-column
        field="virtualSize"
        label="Virtual Size"
        sortable
        header-class="sort-inline"
        :custom-sort="sortBy('virtualSize')"
        v-slot="props">
        {{ props.row.virtualSize }}
      </b-table-column>

      <b-table-column label="Actions" centered v-slot="props">
        <div class="row-actions" @click.stop>
          <b-tooltip
            v-for="action in rowActions"
            :key="action.name"
            :label="actionTooltip(action, props.row)"
            type="is-dark"
            multilined>
            <button
              class="button is-light is-small action"
              :aria-label="action.label"
              :disabled="shouldDisableAction(action.name, props.row)"
              @click="action.run(props.row.fullPath)">
              <b-icon :icon="action.icon" />
            </button>
          </b-tooltip>
        </div>
      </b-table-column>
    </b-table>
  </div>
</template>

<script>
  import DiskWarning from '@/components/DiskWarning.vue';
  import axiosInstance from '@/utils/axios.js';
  import {
    baseName,
    diskLabel,
    filesDir,
    findDisk,
    minimegaSafe,
  } from '@/utils/diskChain.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif';
  import { usePhenixStore } from '@/store.js';
  import { useTable } from '@/utils/useTable.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';

  // the disk kinds phenix recognizes, see knownImageExtensions in
  // src/go/api/disk/details.go
  const UPLOAD_TYPES = ['.qcow2', '.qc2', '_rootfs.tgz', '.hdd', '.iso'];

  // What each disk action needs: the RBAC verb, the disk name the role is
  // checked against (none to create a disk), whether minimega runs it on the
  // disk's path, whether a running experiment using the disk rules it out,
  // and, for actions only VM disks allow, the word for a disk they were used
  // on.
  const byName = (disk) => disk.name;
  const DISK_ACTION_RULES = {
    snapshot: {
      verb: 'create',
      minimega: true,
      idle: true,
      vmOnly: 'snapshotted',
    },
    commit: {
      verb: 'update',
      // committing writes the image into its backing image, so the role
      // must be allowed to update both
      name: (disk) => [disk.name, baseName(disk.backingImages[0])],
      minimega: true,
      idle: true,
      vmOnly: 'committed',
    },
    rebase: {
      verb: 'update',
      name: byName,
      minimega: true,
      idle: true,
      vmOnly: 'rebased',
    },
    clone: { verb: 'create' },
    resize: { verb: 'update', name: byName, minimega: true, idle: true },
    download: { verb: 'get', name: byName },
    rename: { verb: 'update', name: byName, idle: true },
    delete: { verb: 'delete', name: byName, idle: true },
  };

  // Why the server refuses every action on a disk it lists as read-only:
  // phēnix acts only on images in the minimega files directory, and not on
  // the paths there the disk list leaves out, such as hidden files and the
  // folders it keeps for experiment files.
  const readOnlyReason = (disk) =>
    disk.outsideFilesDir
      ? 'outside the standard images directory'
      : 'at a path the disk list leaves out';

  export default {
    components: { DiskWarning },
    setup() {
      return {
        ...useTable({ name: 'disks' }),
        roleAllowed,
        diskLabel,
        findDisk,
      };
    },
    async created() {
      this.rescan = false;
      this.loader = createPageLoader({
        key: 'disks',
        fetch: (signal) => pageFetchers.disks(signal, { rescan: this.rescan }),
        apply: (disks) => {
          this.disks = disks;
          this.loaded = true;
        },
        // the header button has the server inspect every image again, not
        // just the ones that changed
        refresh: async () => {
          this.rescan = true;
          try {
            return await this.loader.load();
          } finally {
            this.rescan = false;
          }
        },
      });
      this.loader.start();
      addWsHandler(this.handleWs);
    },

    beforeUnmount() {
      removeWsHandler(this.handleWs);
      this.loader.stop();
    },

    computed: {
      // the details window's actions with an icon, offered on each row too
      rowActions() {
        return this.diskActions.filter((action) => action.icon);
      },
      // file pickers match extensions only, so rootfs archives show as .tgz
      uploadAccept() {
        return UPLOAD_TYPES.map((t) => t.replace('_rootfs', '')).join(',');
      },
      uploadTypesText() {
        const types = UPLOAD_TYPES.map((t) =>
          t.startsWith('.') ? t : `*${t}`,
        );
        return `${types.slice(0, -1).join(', ')}, and ${types.at(-1)}`;
      },
      emptyText() {
        if (!this.loaded) return loadingText('disks');
        if (this.disks.length === 0) return 'No disk images found';
        return 'No disk images match your search';
      },
      paginationNeeded() {
        return this.disks.length > this.table.perPage;
      },
      filteredDisks() {
        const filter = this.filterString.toLowerCase();
        return (this.disks ?? []).filter((disk) =>
          diskLabel(disk).toLowerCase().includes(filter),
        );
      },
      // the minimega files directory, as the list shows it
      filesDir() {
        return filesDir(this.disks);
      },
      // the disks the open disk can be rebased onto, by label: the others
      // phēnix can act on and minimega can take
      rebaseTargets() {
        const current = this.detailsModal.disk.fullPath;
        return this.disks
          .filter(
            (disk) =>
              !disk.readOnly &&
              disk.fullPath !== current &&
              minimegaSafe(disk.fullPath),
          )
          .sort((a, b) => diskLabel(a).localeCompare(diskLabel(b)));
      },
    },

    methods: {
      resetData() {
        this.detailsModal.active = false;
        this.rebaseModal = {
          active: false,
          unsafe: false,
          isWaiting: false,
          dst: '',
        };
        this.commitModal = {
          active: false,
          delete: false,
          isWaiting: false,
        };
      },
      handleWs(msg) {
        // the server saw images added, changed or removed
        if (msg.resource.type === 'disks' && msg.resource.action === 'update') {
          this.loader.load();
        }
      },
      // closes any open dialog and reloads the list after a change
      updateDisks() {
        this.resetData();
        this.loader.load();
      },
      rowClick(row) {
        this.detailsModal.disk = row;
        this.detailsModal.active = true;
      },
      // shows the details of an image in the open disk's backing chain
      openBacking(path) {
        this.detailsModal.disk = findDisk(this.disks, path);
      },
      // why an action can't be used on a disk, or null when it can
      disabledReason(action, disk) {
        const rule = DISK_ACTION_RULES[action];
        if (!rule) return null;

        if (disk.readOnly) {
          return `the disk is ${readOnlyReason(disk)}`;
        }
        if (rule.minimega && !minimegaSafe(disk.fullPath)) {
          return "minimega cannot use the disk's path";
        }
        if (rule.idle && disk.inUse) {
          return `the disk is in use by ${this.runningText(disk)}`;
        }
        if (rule.vmOnly && disk.kind != 'VM') {
          return `only VM disks can be ${rule.vmOnly}`;
        }
        if (action === 'commit') {
          if (!disk.backingImages?.length) {
            return 'the disk has no backing image to commit into';
          }
          const backing = findDisk(this.disks, disk.backingImages[0]);
          if (backing?.readOnly) {
            return `its backing image is ${readOnlyReason(backing)}`;
          }
        }
        const permitted = rule.name
          ? [rule.name(disk)]
              .flat()
              .every((name) => roleAllowed('disks', rule.verb, name))
          : roleAllowed('disks', rule.verb);
        return permitted ? null : "you don't have permission";
      },
      shouldDisableAction(action, disk = this.detailsModal.disk) {
        return this.disabledReason(action, disk) !== null;
      },
      // Sends an action's request, its parameters in the query string, then
      // reloads the list and closes the dialog, or reports the error.
      actionWrapper(url, params, dialog = null, method = 'post') {
        if (dialog != null) {
          dialog.startLoading();
        }

        axiosInstance
          .request({ method, url, params })
          .then(() => {
            this.updateDisks();
            if (dialog != null) {
              dialog.close();
            }
          })
          .catch((err) => {
            useErrorNotification(err);
            if (dialog != null) {
              dialog.cancelLoading();
            }
          });
      },
      commitDisk(path, deleteOnSuccess) {
        this.commitModal.isWaiting = true;
        axiosInstance
          .post('disks/commit', null, { params: { disk: path } })
          .then(() => {
            if (deleteOnSuccess) {
              this.actionWrapper('disks', { disk: path }, null, 'delete');
            } else {
              this.updateDisks();
            }
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },
      snapshotDisk(path) {
        this.$buefy.dialog.prompt({
          message:
            'Are you sure you want to snapshot this disk? This will create a new disk backed by this image.',
          inputAttrs: {
            type: 'text',
            placeholder: 'New image name',
          },
          canCancel: ['button'],
          closeOnConfirm: false,
          onConfirm: (value, dialog) =>
            this.actionWrapper(
              'disks/snapshot',
              { disk: path, new: value },
              dialog,
            ),
        });
      },
      rebaseDisk(path, dst, unsafe) {
        this.rebaseModal.isWaiting = true;
        this.actionWrapper('disks/rebase', {
          disk: path,
          backing: dst,
          unsafe,
        });
      },
      resizeDisk(path) {
        this.$buefy.dialog.prompt({
          message:
            'Are you sure you want to resize this disk? The size must end with one of "K,M,G,T,P,E" and may be relative by prefixing with +/- (e.g., "50G" or "-512M").<br>Resizing must be accompanied by VM OS changes to either expand partitions after resizing or shrink partitions beforehand. <b class="has-text-danger">Data loss will occur if size is reduced without modifying the OS first.</b>',
          inputAttrs: {
            type: 'text',
            placeholder: 'New size',
            pattern: '[+-]?\\d+[KMGTPE]',
          },
          canCancel: ['button'],
          closeOnConfirm: false,
          onConfirm: (value, dialog) =>
            this.actionWrapper(
              'disks/resize',
              { disk: path, size: value },
              dialog,
            ),
        });
      },
      cloneDisk(path) {
        this.$buefy.dialog.prompt({
          message: 'Are you sure you want to clone this disk?',
          inputAttrs: {
            type: 'text',
            placeholder: 'New image name',
            value: path.split('/').pop(),
          },
          canCancel: ['button'],
          closeOnConfirm: false,
          onConfirm: (value, dialog) =>
            this.actionWrapper(
              'disks/clone',
              { disk: path, new: value },
              dialog,
            ),
        });
      },
      renameDisk(path) {
        this.$buefy.dialog.prompt({
          message:
            'Are you sure you want to rename this disk? <b class="has-text-danger">If this disk backs others, they must be rebased to use the new name.</b>',
          inputAttrs: {
            type: 'text',
            placeholder: 'New name',
            value: path.split('/').pop(),
          },
          canCancel: ['button'],
          closeOnConfirm: false,
          onConfirm: (value, dialog) =>
            this.actionWrapper(
              'disks/rename',
              { disk: path, new: value },
              dialog,
            ),
        });
      },
      deleteDisk(path) {
        this.$buefy.dialog.confirm({
          message:
            'Are you sure you want to delete this disk? <b class="has-text-danger">If this disk backs others, they will become invalid.</b>',
          canCancel: ['button'],
          closeOnConfirm: false,
          onConfirm: (_, dialog) =>
            this.actionWrapper('disks', { disk: path }, dialog, 'delete'),
        });
      },
      downloadDisk(path) {
        this.$buefy.dialog.confirm({
          message: 'Are you sure you want to download this disk?',
          onConfirm: () => {
            const store = usePhenixStore();
            const basePath = import.meta.env.BASE_URL;
            window.open(
              `${basePath}api/v1/disks/download?token=${store.token}&disk=${encodeURIComponent(path)}`,
              '_blank',
            );
          },
        });
      },
      uploadDisk(file) {
        if (!file) return;
        if (!UPLOAD_TYPES.some((ext) => file.name.endsWith(ext))) {
          showError(
            `Cannot upload ${file.name}`,
            `Valid disk file types are ${this.uploadTypesText}.`,
          );
          return;
        }

        let formData = new FormData();
        formData.append('file', file);
        this.uploader.name = file.name;
        this.currentUploadProgress = 0;
        axiosInstance
          .post(`disks`, formData, {
            headers: { 'Content-Type': 'multipart/form-data' },
            onUploadProgress: (event) => {
              this.currentUploadProgress = Math.round(
                (event.loaded / event.total) * 100,
              );
            },
          })
          .then(() => {
            this.currentUploadProgress = null;
            this.uploader.active = false;
            this.updateDisks();
          })
          .catch((err) => {
            useErrorNotification(
              `Error uploading: ${err.response?.data ?? err.message}`,
            );
            this.currentUploadProgress = null;
          });
      },
      // converts a human-readable string in IEC format to a byte count
      toByteCount(s) {
        // "512 B", "2.1 GiB" or minimega's "2.1G"; unknown sizes sort first
        const match = /([\d.]+)\s*([KMGTPE])?/.exec(s ?? '');
        if (!match) return -1;
        const power = match[2] ? 'KMGTPE'.indexOf(match[2]) + 1 : 0;
        return parseFloat(match[1]) * Math.pow(1024, power);
      },
      // sorts by a human-readable size field
      sortBy(field) {
        return (diskA, diskB, isAsc) =>
          (this.toByteCount(diskA[field]) - this.toByteCount(diskB[field])) *
          (isAsc ? 1 : -1);
      },
      // sorts by the names the Name column shows
      sortByLabel(diskA, diskB, isAsc) {
        return (
          diskLabel(diskA).localeCompare(diskLabel(diskB)) * (isAsc ? 1 : -1)
        );
      },
      // where a disk is, for its details
      locationText(disk) {
        if (disk.outsideFilesDir) {
          return 'Outside the standard images directory';
        }
        const where = this.filesDir
          ? `The standard images directory (${this.filesDir})`
          : 'The standard images directory';
        return disk.readOnly
          ? `${where}, at a path the disk list leaves out`
          : where;
      },
      // the running experiments using a disk, which hold it in use
      runningText(disk) {
        const running = (disk.experiments ?? []).filter((exp) => exp.running);
        return running.length
          ? running.map((exp) => exp.name).join(', ')
          : 'a running experiment';
      },
      // an action's name, or why it cannot be used on the disk
      actionTooltip(action, disk) {
        const reason = this.disabledReason(action.name, disk);
        return reason
          ? `Can't ${action.label.toLowerCase()}: ${reason}`
          : action.label;
      },
      // the experiments using a disk, noting the stopped ones
      experimentsText(disk) {
        if (!disk.experiments?.length) return 'None';
        return disk.experiments
          .map((exp) => (exp.running ? exp.name : `${exp.name} (stopped)`))
          .join(', ');
      },
    },

    data() {
      return {
        currentUploadProgress: null,
        uploader: { active: false, name: null },
        disks: [],
        // the details window's actions, each run with the disk's full path
        diskActions: [
          {
            name: 'snapshot',
            label: 'Snapshot',
            description: 'Creates a new image backed by this image',
            icon: 'camera',
            run: this.snapshotDisk,
          },
          {
            name: 'commit',
            label: 'Commit',
            description: 'Commits change in this image to its backing image',
            run: () => (this.commitModal.active = true),
          },
          {
            name: 'rebase',
            label: 'Rebase',
            description:
              'Updates image and rebases onto a different backing image',
            run: () => (this.rebaseModal.active = true),
          },
          {
            name: 'clone',
            label: 'Clone',
            description: 'Creates a copy of the disk file',
            icon: 'copy',
            run: this.cloneDisk,
          },
          { name: 'resize', label: 'Resize', run: this.resizeDisk },
          {
            name: 'download',
            label: 'Download',
            icon: 'download',
            run: this.downloadDisk,
          },
          {
            name: 'rename',
            label: 'Rename',
            icon: 'pencil',
            run: this.renameDisk,
          },
          {
            name: 'delete',
            label: 'Delete',
            icon: 'trash',
            run: this.deleteDisk,
          },
        ],
        filterString: '',
        loaded: false, // false until the first list arrives
        detailsModal: {
          active: false,
          disk: {},
        },
        rebaseModal: {
          active: false,
          isWaiting: false,
          unsafe: false,
          dst: '',
        },
        commitModal: {
          active: false,
          isWaiting: false,
          delete: false,
        },
      };
    },
  };
</script>

<style scoped>
  .b-tooltip:after {
    white-space: pre !important;
  }

  dl {
    display: table;
  }

  dl > div {
    display: table-row;
  }

  dl > div > dt,
  dl > div > dd {
    display: table-cell;
    padding: 0.25em;
  }

  dl > div > dt {
    font-weight: bold;
    width: 20%;
  }

  hr {
    margin: 4px 0px;
  }

  .action-button {
    color: dimgray;
    padding: 8px;
    cursor: pointer !important;
  }

  .action-button:hover {
    background-color: #ddd;
  }

  .row-actions {
    display: inline-flex;
    gap: 5px;
  }

  .disk-label {
    overflow-wrap: anywhere;
  }

  .name-warning {
    margin-left: 0.35rem;
  }

  .button.backing-link {
    height: auto;
    padding: 0;
    white-space: normal;
  }

  /* $link (#62c0d7) is 3.3:1 on the details window's background
     ($background-ter); this lighter tint of it is 4.7:1 */
  .button.backing-link,
  .button.backing-link:hover,
  .button.backing-link:focus {
    color: #abddea;
  }

  .action-separator {
    margin: 0 8px;
  }

  .action-tooltip {
    display: flex;
  }

  .actions button {
    text-align: start;
    /* color: blue; */
    text-decoration: none;
    display: inline;
  }

  .file-cta,
  .file-cta > p,
  .file-cta:hover {
    border: none;
    background-color: #686868;
    color: whitesmoke !important;
  }
</style>
