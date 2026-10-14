<!--
A background job's progress: its title, status, a progress bar, a status
line read out as it changes, and buttons to cancel it, download what it
built, and dismiss it once finished. Details of a job that did not fully
succeed list each item's outcome.
-->
<template>
  <div class="job-progress" :class="{ 'is-compact': compact }">
    <div class="job-heading">
      <span class="job-title">{{ job.title || job.kind }}</span>
      <b-tag :type="statusInfo.type" class="job-status">
        {{ statusInfo.label }}
      </b-tag>
    </div>
    <progress
      v-if="active"
      class="progress is-small"
      :class="statusInfo.type"
      :value="progress.value"
      :max="progress.max"
      :aria-label="`${job.title || job.kind} progress`">
      {{ progress.value }}
    </progress>
    <p class="job-status-line" aria-live="polite">{{ statusLine }}</p>
    <div class="job-buttons">
      <b-button
        v-if="active && job.cancelable"
        size="is-small"
        icon-left="ban"
        :disabled="job.cancelRequested || busy"
        :aria-label="`Cancel ${job.title || job.kind}`"
        @click="cancel">
        {{ job.cancelRequested ? 'Canceling…' : 'Cancel' }}
      </b-button>
      <b-button
        v-if="job.artifact"
        size="is-small"
        :type="job.artifact.incomplete ? 'is-warning' : 'is-primary'"
        icon-left="download"
        @click="download">
        {{
          job.artifact.incomplete ? 'Download incomplete archive' : 'Download'
        }}
      </b-button>
      <b-button
        v-if="!active && dismissible"
        size="is-small"
        icon-left="times-circle"
        :aria-label="`Dismiss ${job.title || job.kind}`"
        :disabled="busy"
        @click="dismiss">
        Dismiss
      </b-button>
    </div>
    <operation-outcome
      v-if="showOutcome"
      :items="outcome"
      :noun="noun"
      class="mt-2"></operation-outcome>
  </div>
</template>

<script>
  import OperationOutcome from '@/components/jobs/OperationOutcome.vue';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import { bytesText, isActiveJob, useJobStore } from '@/utils/jobs.js';
  import { outcomeFromJob, summarizeOutcome } from '@/utils/operations.js';
  import { plural } from '@/utils/plural.js';

  // how each status reads, and its Bulma color; the label carries the
  // meaning, so it does not rest on the color alone
  const STATUSES = {
    queued: { label: 'Queued', type: 'is-light' },
    running: { label: 'Running', type: 'is-info' },
    succeeded: { label: 'Succeeded', type: 'is-success' },
    partial: { label: 'Partly succeeded', type: 'is-warning' },
    failed: { label: 'Failed', type: 'is-danger' },
    canceled: { label: 'Canceled', type: 'is-dark' },
  };

  // what each kind's items are
  const NOUNS = {
    'vms.action': 'VM',
    'files.archive': 'file',
    'disk.clone': 'disk image',
  };

  export default {
    components: { OperationOutcome },
    props: {
      job: { type: Object, required: true },
      // fewer details, as the jobs menu shows them
      compact: { type: Boolean, default: false },
      dismissible: { type: Boolean, default: true },
    },
    emits: ['dismissed'],
    data() {
      return { busy: false };
    },
    computed: {
      active() {
        return isActiveJob(this.job);
      },
      statusInfo() {
        return (
          STATUSES[this.job.status] ?? { label: this.job.status, type: null }
        );
      },
      noun() {
        return NOUNS[this.job.kind] ?? 'item';
      },
      // the bar measures bytes when the job counts them, items otherwise
      progress() {
        const p = this.job.progress ?? {};
        if (p.bytesTotal) return { value: p.bytes ?? 0, max: p.bytesTotal };
        return { value: p.done ?? 0, max: p.total || 1 };
      },
      outcome() {
        return outcomeFromJob(this.job);
      },
      // a single item's outcome shows only when it has a reason to give
      showOutcome() {
        return (
          !this.compact &&
          !this.active &&
          this.job.status !== 'succeeded' &&
          (this.outcome.length > 1 || this.outcome.some((item) => item.error))
        );
      },
      // a finished job whose copy, such as a websocket message's, leaves out
      // some items, so its counts need the whole job
      partial() {
        return !this.active && this.job.itemsOmitted > 0;
      },
      statusLine() {
        const p = this.job.progress ?? {};
        const total = plural(p.total ?? 0, this.noun);
        if (this.job.status === 'queued') return 'Waiting for other jobs';
        if (this.active) {
          const bytes = p.bytesTotal
            ? ` (${bytesText(p.bytes)} of ${bytesText(p.bytesTotal)})`
            : '';
          const canceling = this.job.cancelRequested
            ? '; stopping after the ones in progress'
            : '';
          return `${p.done ?? 0} of ${total} done${bytes}${canceling}`;
        }
        if (this.partial) return `${p.done ?? 0} of ${total} done`;
        const counts = summarizeOutcome(this.outcome);
        const parts = [`${counts.succeeded} of ${total} succeeded`];
        for (const status of ['failed', 'skipped', 'canceled']) {
          if (counts[status]) parts.push(`${counts[status]} ${status}`);
        }
        if (this.job.error) parts.push(this.job.error);
        // the menu lists no outcome, so a lone item's reason goes here
        if (this.outcome.length === 1 && this.outcome[0].error) {
          parts.push(this.outcome[0].error);
        }
        return parts.join(', ');
      },
    },
    watch: {
      partial: {
        handler(partial) {
          if (partial) this.fetchWhole();
        },
      },
    },
    mounted() {
      if (this.partial) this.fetchWhole();
    },
    methods: {
      // reads the whole job, for the items a shortened copy left out; the
      // status line keeps its count of done items if that fails
      fetchWhole() {
        useJobStore()
          .fetch(this.job.id)
          .catch(() => {});
      },
      async cancel() {
        this.busy = true;
        try {
          await useJobStore().cancel(this.job.id);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.busy = false;
        }
      },
      async dismiss() {
        this.busy = true;
        try {
          await useJobStore().dismiss(this.job.id);
          this.$emit('dismissed', this.job);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.busy = false;
        }
      },
      download() {
        const open = () => useJobStore().downloadArtifact(this.job);

        if (!this.job.artifact.incomplete) {
          open();
          return;
        }

        // an incomplete archive downloads only once its gaps are known
        this.$buefy.dialog.confirm({
          title: 'Download incomplete archive',
          message:
            'Some files could not be added to this archive. Its MISSING.txt ' +
            'lists them and why. Download it anyway?',
          confirmText: 'Download',
          cancelText: 'Cancel',
          type: 'is-warning',
          hasIcon: true,
          onConfirm: open,
        });
      },
    },
  };
</script>

<style scoped>
  .job-progress {
    min-width: 18rem;
  }

  .job-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    margin-bottom: 0.25rem;
  }

  .job-title {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .job-progress .progress {
    margin-bottom: 0.25rem;
  }

  .job-status-line {
    font-size: 0.85rem;
    margin-bottom: 0.25rem;
  }

  .job-buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }
</style>
