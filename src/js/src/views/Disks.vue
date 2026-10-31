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
      has-modal-card
      width="min(1280px, 95vw)">
      <DiskDetailsCard
        :disk="detailsDisk"
        :disks="disks"
        :files-dir="filesDir"
        :actions="cardActions"
        :clone-jobs="cloneJobsOf(detailsDisk)"
        @action="runCardAction"
        @open="openBacking" />
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
                  detailsDisk.fullPath,
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
              () => commitDisk(detailsDisk.fullPath, commitModal.delete)
            " />
        </footer>
      </div>
    </b-modal>
    <!-- CONTENT -->
    <div class="level disks-toolbar">
      <div class="level-left">
        <stat-strip v-if="loaded" label="Disk totals" :stats="totalsShown" />
      </div>
      <div class="level-right">
        <div class="level-item">
          <b-field grouped position="is-right">
            <div
              v-if="paginationNeeded"
              class="control is-flex is-align-items-center">
              <b-switch
                v-model="table.isPaginated"
                size="is-small"
                type="is-light"
                >Paginate</b-switch
              >
            </div>
            <b-field :addons="false" class="has-addons">
              <b-autocomplete
                v-model="filterString"
                placeholder="Find a Disk"
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
                currentUploadProgress == null
                  ? 'Upload a disk'
                  : 'Upload progress'
              "
              type="is-light is-left">
              <button
                class="button is-light"
                style="margin-left: 8px"
                aria-label="Upload a disk"
                @click="uploader.active = true">
                <b-icon
                  v-if="currentUploadProgress == null"
                  icon="upload"></b-icon>
                <span v-else style="width: 32px"
                  >{{ currentUploadProgress }}%</span
                >
              </button>
            </b-tooltip>
          </b-field>
        </div>
      </div>
    </div>

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
        width="30%"
        sortable
        header-class="sort-inline"
        :custom-sort="sortByLabel"
        v-slot="props">
        <span class="disk-name">
          <span class="disk-label">{{ diskLabel(props.row) }}</span>
          <b-tooltip
            v-if="props.row.brokenChain"
            class="name-warning"
            :label="chainProblem(props.row)"
            type="is-dark"
            multilined
            append-to-body
            :triggers="['hover', 'focus']">
            <!-- the problem is in the name too, for a screen reader -->
            <button
              type="button"
              class="tag is-danger is-light broken-tag"
              @click.stop="rowClick(props.row)">
              Broken chain<span class="is-sr-only"
                >: {{ chainProblem(props.row) }}</span
              >
            </button>
          </b-tooltip>
          <span
            v-if="props.row.outsideFilesDir"
            class="name-warning"
            @click.stop>
            <DiskWarning :reasons="['outside']" :files-dir="filesDir" />
          </span>
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
        field="format"
        label="Type"
        sortable
        header-class="sort-inline"
        :custom-sort="sortByType"
        v-slot="props">
        <DiskFormat :disk="props.row" />
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
          <!-- the last column: a tooltip above a button would leave the window -->
          <b-tooltip
            v-for="action in rowActions"
            :key="action.name"
            :label="actionTooltip(action, props.row)"
            type="is-dark"
            position="is-left"
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
  import DiskDetailsCard from '@/components/DiskDetailsCard.vue';
  import DiskFormat from '@/components/DiskFormat.vue';
  import DiskWarning from '@/components/DiskWarning.vue';
  import StatStrip from '@/components/StatStrip.vue';
  import { useJobStore } from '@/utils/jobs.js';
  import { bytesText, parseBytes } from '@/utils/bytes.js';
  import { diskTotals } from '@/utils/diskTotals.js';
  import {
    errorLine,
    notifyOutcome,
    outcomeFromJob,
  } from '@/utils/operations.js';
  import * as disksApi from '@/api/disks.js';
  import {
    chainProblem,
    diskLabel,
    filesDir,
    findDisk,
    minimegaSafe,
  } from '@/utils/diskChain.js';
  import { compareDiskTypes } from '@/utils/diskFormat.js';
  import {
    diskActionState,
    diskDisabledReason,
    diskRunningText,
  } from '@/utils/diskActions.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif';
  import { usePhenixStore } from '@/store.js';
  import { useTable } from '@/utils/useTable.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';

  // the disk kinds phenix recognizes, see knownImageExtensions in
  // src/go/api/disk/details.go
  const UPLOAD_TYPES = [
    '.qcow2',
    '.qc2',
    '.qcow',
    '_rootfs.tgz',
    '.hdd',
    '.iso',
  ];

  // a header refresh asked the server to inspect every image again; the next
  // disk list request to start takes it, whichever load that request serves
  let rescanPending = false;

  export default {
    components: { DiskDetailsCard, DiskFormat, DiskWarning, StatStrip },
    setup() {
      return {
        ...useTable({ name: 'disks' }),
        roleAllowed,
        chainProblem,
        diskLabel,
      };
    },
    async created() {
      this.loader = createPageLoader({
        key: 'disks',
        fetch: (signal, meta) => {
          const rescan = rescanPending;
          rescanPending = false;
          return pageFetchers.disks(signal, meta, { rescan });
        },
        apply: (disks) => {
          this.disks = disks;
          this.loaded = true;
        },
        // the header button has the server inspect every image again, not
        // just the ones that changed
        refresh: () => {
          rescanPending = true;
          return this.loader.load({ force: true });
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
      // the details window's actions that each row offers too
      rowActions() {
        return this.diskActions.filter((action) => action.row);
      },
      // the disk the details window shows, as the list has it now
      detailsDisk() {
        const shown = this.detailsModal.disk;
        return findDisk(this.disks, shown.fullPath) ?? shown;
      },
      // the details window's actions, each disabled with the reason when it
      // cannot be used on the disk
      cardActions() {
        return this.diskActions.map((action) => ({
          name: action.name,
          label: action.label,
          description: action.description,
          icon: action.icon,
          disabled: this.shouldDisableAction(action.name, this.detailsDisk),
          tooltip: this.actionTooltip(action, this.detailsDisk),
        }));
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
      // the counts beside the search box, of every disk, not only those shown
      totalsShown() {
        const totals = diskTotals(this.disks);
        return [
          { key: 'disks', label: 'Disks', value: totals.disks },
          { key: 'in-use', label: 'In use', value: totals.inUse },
          {
            key: 'disk-size',
            label: 'Total size on disk',
            value: bytesText(totals.sizeBytes),
            detail: totals.unknownDiskSizes
              ? `(${totals.unknownDiskSizes} unknown)`
              : '',
          },
        ];
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
        const current = this.detailsDisk.fullPath;
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
          this.loader.load({ force: true });
        }
      },
      // closes any open dialog and reloads the list after a change
      updateDisks() {
        this.resetData();
        this.loader.load({ force: true });
      },
      rowClick(row) {
        this.detailsModal.disk = row;
        this.detailsModal.active = true;
      },
      // shows the details of another listed image, from the open disk's
      // backing chain or the images built on it
      openBacking(path) {
        const disk = findDisk(this.disks, path);
        if (disk) this.detailsModal.disk = disk;
      },
      runCardAction(name) {
        const action = this.diskActions.find((a) => a.name === name);
        action?.run(this.detailsDisk.fullPath);
      },
      // why an action can't be used on a disk, or null when it can
      disabledReason(action, disk) {
        return diskDisabledReason(action, disk, {
          disks: this.disks,
          can: (verb, name) =>
            name === undefined
              ? roleAllowed('disks', verb)
              : roleAllowed('disks', verb, name),
        });
      },
      shouldDisableAction(action, disk = this.detailsDisk) {
        return this.disabledReason(action, disk) !== null;
      },
      // Sends an action's request (send returns it), then reloads the list
      // and closes the dialog, or reports the error.
      actionWrapper(send, dialog = null) {
        if (dialog != null) {
          dialog.startLoading();
        }

        send()
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
      // a failed commit or rebase leaves its dialog open, ready to cancel
      commitDisk(path, deleteOnSuccess) {
        this.commitModal.isWaiting = true;
        disksApi
          .commitDisk(path)
          .then(() => deleteOnSuccess && disksApi.deleteDisk(path))
          .then(() => this.updateDisks())
          .catch((err) => {
            useErrorNotification(err);
            this.commitModal.isWaiting = false;
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
              () => disksApi.snapshotDisk(path, value),
              dialog,
            ),
        });
      },
      rebaseDisk(path, dst, unsafe) {
        this.rebaseModal.isWaiting = true;
        disksApi
          .rebaseDisk(path, dst, unsafe)
          .then(() => this.updateDisks())
          .catch((err) => {
            useErrorNotification(err);
            this.rebaseModal.isWaiting = false;
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
            this.actionWrapper(() => disksApi.resizeDisk(path, value), dialog),
        });
      },
      // Clones the disk in a background job: the prompt closes once the
      // server has started it, the jobs menu and the disk's details show its
      // progress, and the list reloads when it ends.
      cloneDisk(path) {
        this.$buefy.dialog.prompt({
          message:
            'Are you sure you want to clone this disk? The copy is made in ' +
            'the background.',
          inputAttrs: {
            type: 'text',
            placeholder: 'New image name',
            value: path.split('/').pop(),
          },
          canCancel: ['button'],
          closeOnConfirm: false,
          onConfirm: async (value, dialog) => {
            dialog.startLoading();
            let job;
            try {
              job = await disksApi.cloneDisk(path, value);
            } catch (err) {
              useErrorNotification(err);
              dialog.cancelLoading();
              return;
            }
            dialog.close();
            try {
              // a canceled clone needs no report: the user stopped it
              const done = await useJobStore().whenDone(job.id);
              if (done.status !== 'canceled')
                notifyOutcome(this.$buefy, {
                  verbPast: 'Cloned',
                  noun: 'disk image',
                  items: outcomeFromJob(done),
                });
            } catch (err) {
              useErrorNotification(err);
            }
            this.loader.load({ force: true });
          },
        });
      },
      // the clone jobs of a disk, as the jobs store keeps them
      cloneJobsOf(disk) {
        return useJobStore().list.filter(
          (job) =>
            job.kind === 'disk.clone' && job.params?.disk === disk.fullPath,
        );
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
            this.actionWrapper(() => disksApi.renameDisk(path, value), dialog),
        });
      },
      deleteDisk(path) {
        this.$buefy.dialog.confirm({
          message:
            'Are you sure you want to delete this disk? <b class="has-text-danger">If this disk backs others, they will become invalid.</b>',
          canCancel: ['button'],
          closeOnConfirm: false,
          onConfirm: (_, dialog) =>
            this.actionWrapper(() => disksApi.deleteDisk(path), dialog),
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
        disksApi
          .uploadDisk(formData, (event) => {
            this.currentUploadProgress = Math.round(
              (event.loaded / event.total) * 100,
            );
          })
          .then(() => {
            this.currentUploadProgress = null;
            this.uploader.active = false;
            this.updateDisks();
          })
          .catch((err) => {
            useErrorNotification(`Error uploading: ${errorLine(err)}`);
            this.currentUploadProgress = null;
          });
      },
      // sorts by a human-readable size field; unknown sizes sort first
      sortBy(field) {
        const bytes = (disk) => parseBytes(disk[field]) ?? -1;
        return (diskA, diskB, isAsc) =>
          (bytes(diskA) - bytes(diskB)) * (isAsc ? 1 : -1);
      },
      // sorts by the names the Name column shows
      sortByLabel(diskA, diskB, isAsc) {
        return (
          diskLabel(diskA).localeCompare(diskLabel(diskB)) * (isAsc ? 1 : -1)
        );
      },
      // sorts by the types the Type column shows, such as qcow2 v3
      sortByType(diskA, diskB, isAsc) {
        return compareDiskTypes(diskA, diskB) * (isAsc ? 1 : -1);
      },
      // the running experiments using a disk, which hold it in use
      runningText(disk) {
        return diskRunningText(disk);
      },
      // an action's name, or why it cannot be used on the disk
      actionTooltip(action, disk) {
        return (
          diskActionState(action, disk, {
            disks: this.disks,
            can: (verb, name) =>
              name === undefined
                ? roleAllowed('disks', verb)
                : roleAllowed('disks', verb, name),
          }).reason ?? action.label
        );
      },
    },

    data() {
      return {
        currentUploadProgress: null,
        uploader: { active: false, name: null },
        disks: [],
        // the details window's actions, each run with the disk's full path;
        // row marks the ones each row offers too
        diskActions: [
          {
            name: 'snapshot',
            label: 'Snapshot',
            description: 'Creates a new image backed by this image',
            icon: 'camera',
            row: true,
            run: this.snapshotDisk,
          },
          {
            name: 'commit',
            label: 'Commit',
            description: 'Commits change in this image to its backing image',
            icon: 'save',
            run: () => (this.commitModal.active = true),
          },
          {
            name: 'rebase',
            label: 'Rebase',
            description:
              'Updates image and rebases onto a different backing image',
            icon: 'route',
            run: () => (this.rebaseModal.active = true),
          },
          {
            name: 'clone',
            label: 'Clone',
            description: 'Creates a copy of the disk file',
            icon: 'copy',
            row: true,
            run: this.cloneDisk,
          },
          {
            name: 'resize',
            label: 'Resize',
            description: 'Changes the virtual size of the disk',
            icon: 'expand',
            run: this.resizeDisk,
          },
          {
            name: 'download',
            label: 'Download',
            description: 'Saves the image file to this computer',
            icon: 'download',
            row: true,
            run: this.downloadDisk,
          },
          {
            name: 'rename',
            label: 'Rename',
            description: 'Gives the image a new name',
            icon: 'pencil',
            row: true,
            run: this.renameDisk,
          },
          {
            name: 'delete',
            label: 'Delete',
            description: 'Removes the image file',
            icon: 'trash',
            row: true,
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

  .row-actions {
    display: inline-flex;
    gap: 5px;
  }

  /* as on the Configs page: the controls drop below the counts together when
     both do not fit on one row */
  .level.disks-toolbar {
    flex-wrap: wrap;
    gap: 0.75rem;
    margin: 12px 0;
  }

  .disks-toolbar .level-right {
    margin-left: auto;
  }

  /* a long name wraps beside its warning icon, never under it */
  .disk-name {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    max-width: 100%;
  }

  .disk-label {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .name-warning {
    flex: none;
  }
</style>
