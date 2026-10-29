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
          :aria-label="label"
          :aria-expanded="String(active)">
          <b-icon icon="download" />
          <span>Download</span>
          <b-icon icon="chevron-down" size="is-small" />
        </button>
      </template>
      <b-dropdown-item custom class="run-downloads-head">
        Archived artifacts, newest first
      </b-dropdown-item>
      <b-dropdown-item
        v-for="archive in archives"
        :key="archive.path"
        class="run-downloads-item"
        has-link>
        <a :href="archive.url" download>
          <time-with-age :time="archive.started" :fallback="archive.name" />
          <span class="run-downloads-size">{{ archive.sizeText }}</span>
        </a>
      </b-dropdown-item>
    </b-dropdown>
  </b-tooltip>
</template>

<script>
  import TimeWithAge from '@/components/TimeWithAge.vue';
  import { usePhenixStore } from '@/store.js';
  import { bytesText } from '@/utils/bytes.js';
  import { experimentFileURL } from '@/utils/experimentFiles.js';
  import { actionState } from '@/utils/operations.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { runArchives } from '@/utils/scorchArtifacts.js';

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
    font-variant-numeric: tabular-nums;
  }
</style>
