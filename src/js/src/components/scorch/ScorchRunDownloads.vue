<template>
  <b-tooltip
    v-if="canDownload"
    class="run-downloads"
    :label="state.reason ?? ''"
    :active="state.disabled"
    type="is-light"
    position="is-left"
    multilined>
    <button
      v-if="state.disabled"
      class="button is-dark"
      :aria-label="label"
      disabled>
      <b-icon icon="download" />
      <span>Download</span>
    </button>
    <b-dropdown
      v-else
      position="is-bottom-left"
      aria-role="dialog"
      :trigger-tabindex="-1">
      <template #trigger="{ active }">
        <button
          class="button is-dark"
          :class="{ 'is-loading': downloading.length > 0 }"
          :aria-label="label"
          :aria-expanded="String(active)"
          :aria-busy="String(downloading.length > 0)">
          <b-icon icon="download" />
          <span>Download</span>
          <b-icon icon="chevron-down" size="is-small" />
        </button>
      </template>
      <b-dropdown-item custom :focusable="false" class="run-downloads-head">
        Archived artifacts, newest first
      </b-dropdown-item>
      <b-dropdown-item
        v-for="archive in archives"
        :key="archive.path"
        class="run-downloads-item"
        has-link>
        <a
          :href="archive.url"
          download
          :aria-busy="String(isDownloading(archive))"
          @click.prevent="save(archive)">
          <time-with-age :time="archive.started" :fallback="archive.name" />
          <span class="run-downloads-size">
            <span
              v-if="isDownloading(archive)"
              class="run-downloads-spinner"
              aria-hidden="true" />
            {{ archive.sizeText }}
          </span>
        </a>
      </b-dropdown-item>
    </b-dropdown>
  </b-tooltip>
</template>

<script>
  import FileSaver from 'file-saver';

  import { downloadExperimentFile } from '@/api/experimentFiles.js';
  import TimeWithAge from '@/components/TimeWithAge.vue';
  import { usePhenixStore } from '@/store.js';
  import { bytesText } from '@/utils/bytes.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import {
    downloadsAsBlob,
    experimentFileURL,
    saveURL,
  } from '@/utils/experimentFiles.js';
  import { actionState } from '@/utils/operations.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { runArchives } from '@/utils/scorchArtifacts.js';

  // how long the menu spins for a download the browser itself shows
  const LINK_DOWNLOAD_SPIN_MS = 1500;

  // A run's Download menu of its SCORCH archives, greyed with the reason when
  // there is none; hidden from a role that may not download experiment files.
  export default {
    name: 'ScorchRunDownloads',
    components: { TimeWithAge },
    props: {
      exp: { type: String, required: true },
      run: { type: Number, required: true },
      // the run's name, for the button's accessible name
      name: { type: String, default: '' },
      // the experiment's listed files; null until they have loaded
      files: { type: Array, default: null },
      // whether the latest listing failed
      failed: { type: Boolean, default: false },
    },
    data() {
      return { downloading: [] }; // paths of the archives being saved
    },
    computed: {
      canDownload() {
        return roleAllowed('experiments/files', 'get', this.exp);
      },

      label() {
        return `Download the artifacts of run ${this.name || this.run}`;
      },

      archives() {
        const token = usePhenixStore().token;
        return runArchives(this.files, this.run).map((archive) => ({
          ...archive,
          url: experimentFileURL(this.exp, archive, token),
          sizeText: bytesText(archive.size),
        }));
      },

      state() {
        let blocked = null;
        if (!roleAllowed('experiments/files', 'list', this.exp)) {
          blocked = "you do not have permission to list the experiment's files";
        } else if (this.failed && !this.files) {
          blocked = "the list of the experiment's files did not load";
        } else if (!this.files) {
          blocked = "the list of the experiment's files is still loading";
        } else if (this.archives.length === 0) {
          blocked =
            'the run has no archive yet. SCORCH archives its artifacts each time it finishes';
        }
        return actionState(
          { name: 'download', label: 'Download' },
          { blocked },
        );
      },
    },
    methods: {
      isDownloading(archive) {
        return this.downloading.includes(archive.path);
      },

      // Saves an archive as the Files tab saves a file: up to 256 MiB through
      // a request, the menu spinning until it is saved; a larger one, or of
      // unknown size, through the browser's own download, after a brief spin.
      async save(archive) {
        if (this.isDownloading(archive)) return;
        this.downloading = [...this.downloading, archive.path];

        try {
          if (downloadsAsBlob(archive)) {
            const response = await downloadExperimentFile(this.exp, archive);
            FileSaver.saveAs(response.data, archive.name);
          } else {
            saveURL(archive.url, archive.name);
            await new Promise((done) =>
              setTimeout(done, LINK_DOWNLOAD_SPIN_MS),
            );
          }
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.downloading = this.downloading.filter((p) => p !== archive.path);
        }
      },
    },
  };
</script>

<style scoped>
  /* as wide as its longest line: shrunk to the button, times broke at the T */
  .run-downloads :deep(.dropdown-menu) {
    width: max-content;
  }

  .run-downloads-head {
    font-weight: bold;
  }

  .run-downloads-item a {
    display: flex !important;
    justify-content: space-between;
    gap: 1rem;
    white-space: nowrap;
  }

  /* the age's usual grey is too faint on the menu's background */
  .run-downloads-item :deep(.time-with-age-age) {
    color: inherit !important;
  }

  .run-downloads-size {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    font-variant-numeric: tabular-nums;
  }

  .run-downloads-spinner {
    width: 1em;
    height: 1em;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-radius: 50%;
    animation: run-downloads-spin 0.75s linear infinite;
  }

  @keyframes run-downloads-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
