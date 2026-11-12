<template>
  <div class="experiment-captures">
    <b-notification
      v-if="!support.available"
      class="captures-unsupported"
      type="is-warning"
      :closable="false"
      role="status">
      {{ unsupportedText }}
    </b-notification>
    <b-notification
      v-for="(line, i) in banner"
      :key="i"
      class="captures-budget"
      :type="line.type"
      :closable="false"
      role="status">
      {{ line.text }}
    </b-notification>
    <b-notification
      v-if="budgetError"
      class="captures-budget-error"
      type="is-warning"
      :closable="false"
      role="status">
      The Open vSwitch mirrors cannot be counted: {{ budgetError }}
    </b-notification>

    <div class="captures-bar">
      <p class="captures-intro">
        Experiment captures merge the traffic of all hosts into one file on the
        head node.
      </p>
      <b-tooltip
        v-if="canCreate"
        :label="unavailableLabel"
        :active="!support.available"
        type="is-dark"
        position="is-left"
        multilined>
        <b-button
          class="captures-new"
          type="is-info"
          icon-left="circle-dot"
          :aria-describedby="
            support.available ? undefined : 'captures-new-reason'
          "
          :aria-disabled="support.available ? undefined : 'true'"
          @click="support.available && $emit('capture')">
          Capture all experiment traffic
        </b-button>
        <span
          v-if="!support.available"
          id="captures-new-reason"
          class="is-sr-only">
          {{ unavailableLabel }}
        </span>
      </b-tooltip>
    </div>

    <p v-if="error" class="help is-danger captures-error" role="alert">
      Could not read the captures: {{ error }}
    </p>

    <b-table
      :data="captures"
      detailed
      detail-key="id"
      :opened-detailed="opened"
      :show-detail-icon="false">
      <template #empty>
        <section class="section">
          <div class="content has-text-white has-text-centered">
            {{ emptyText }}
          </div>
        </section>
      </template>
      <b-table-column field="expand" label="Expand" v-slot="props">
        <button
          v-if="props.row.streams?.length"
          type="button"
          class="plain-button streams-toggle"
          :aria-expanded="opened.includes(props.row.id) ? 'true' : 'false'"
          :aria-label="`Hosts of capture ${props.row.id}`"
          @click="toggle(props.row.id)">
          <b-icon
            icon="angle-right"
            :class="{ 'is-expanded': opened.includes(props.row.id) }" />
        </button>
      </b-table-column>
      <b-table-column field="id" label="ID" v-slot="props">
        {{ props.row.id }}
      </b-table-column>
      <b-table-column field="state" label="Status" v-slot="props">
        <b-tag :type="statusType(props.row)" class="capture-state">
          {{ statusText(props.row) }}
        </b-tag>
      </b-table-column>
      <b-table-column field="scope" label="Scope" v-slot="props">
        <span v-if="props.row.state === 'failed' && props.row.error">
          {{ props.row.error }}
        </span>
        <span v-else>{{ captureScope(props.row) }}</span>
      </b-table-column>
      <b-table-column field="start" label="Started" v-slot="props">
        <time-with-age :time="props.row.start" />
      </b-table-column>
      <b-table-column field="size" label="Size" numeric v-slot="props">
        {{ bytesText(Number(props.row.size ?? 0)) }}
      </b-table-column>
      <b-table-column field="packets" label="Packets" numeric v-slot="props">
        {{ countText(props.row.counters?.packets) }}
      </b-table-column>
      <b-table-column field="drops" label="Drops" numeric v-slot="props">
        {{ countText(captureDrops(props.row.counters)) }}
      </b-table-column>
      <b-table-column
        field="actions"
        label="Actions"
        cell-class="nowrap-cell"
        v-slot="props">
        <div class="capture-actions">
          <router-link
            v-if="webSharkLink(props.row)"
            class="button is-light is-small capture-webshark"
            :to="webSharkLink(props.row)">
            <b-icon :icon="captureIcon" size="is-small" />
            <span>Open in WebShark</span>
          </router-link>
          <b-button
            v-if="isRunning(props.row) && canStop"
            class="capture-stop"
            type="is-danger"
            size="is-small"
            icon-left="stop"
            :loading="!!stopping[props.row.id]"
            @click="stop(props.row)">
            Stop
          </b-button>
          <template v-if="!isRunning(props.row) && props.row.file">
            <b-button
              v-if="props.row.state !== 'failed' && canDownload"
              class="capture-download"
              type="is-light"
              size="is-small"
              icon-left="file-download"
              :loading="!!downloading[props.row.id]"
              @click="download(props.row)">
              Download
            </b-button>
            <b-button
              v-if="canDelete"
              class="capture-delete"
              type="is-danger"
              size="is-small"
              icon-left="trash"
              :loading="!!deleting[props.row.id]"
              @click="confirmDelete(props.row)">
              Delete
            </b-button>
          </template>
        </div>
      </b-table-column>

      <template #detail="props">
        <table class="table capture-streams">
          <caption class="is-sr-only">
            Hosts of capture
            {{
              props.row.id
            }}
          </caption>
          <thead>
            <tr>
              <th scope="col">Host</th>
              <th scope="col">Bridge</th>
              <th scope="col" class="has-text-right">Ports</th>
              <th scope="col" class="has-text-right">Packets</th>
              <th scope="col" class="has-text-right">Drops</th>
              <th scope="col" class="has-text-right">Lost</th>
              <th scope="col" class="has-text-right">Late</th>
              <th scope="col" class="has-text-right">Lag</th>
              <th scope="col" class="has-text-right">Skew</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in props.row.streams" :key="`${s.host}/${s.bridge}`">
              <td>{{ s.host }}</td>
              <td>{{ s.bridge }}</td>
              <td class="has-text-right">{{ countText(s.counters?.ports) }}</td>
              <td class="has-text-right">
                {{ countText(s.counters?.packets) }}
              </td>
              <td class="has-text-right">
                {{ countText(captureDrops(s.counters)) }}
              </td>
              <td class="has-text-right">{{ countText(s.counters?.lost) }}</td>
              <td class="has-text-right">{{ countText(s.counters?.late) }}</td>
              <td class="has-text-right">{{ msText(s.counters?.lagMs) }}</td>
              <td class="has-text-right stream-skew">
                <span v-if="s.skewWarning" class="skew-warning">
                  <b-icon icon="exclamation-triangle" size="is-small" />
                  {{ msText(s.counters?.skewMs, { signed: true }) }}
                  (warning: more than half the reorder window)
                </span>
                <template v-else>
                  {{ msText(s.counters?.skewMs, { signed: true }) }}
                </template>
              </td>
            </tr>
          </tbody>
        </table>
      </template>
    </b-table>
  </div>
</template>

<script>
  import FileSaver from 'file-saver';

  import TimeWithAge from '@/components/TimeWithAge.vue';
  import * as experimentFilesApi from '@/api/experimentFiles.js';
  import * as experimentsApi from '@/api/experiments.js';
  import { usePhenixStore } from '@/store.js';
  import { bytesText } from '@/utils/bytes.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import {
    downloadsAsBlob,
    experimentFileURL,
    saveURL,
  } from '@/utils/experimentFiles.js';
  import { inForeground } from '@/utils/foreground.js';
  import { RESOURCES, startPoll } from '@/utils/freshness.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { webSharkInstalled } from '@/utils/webshark.js';
  import {
    captureDrops,
    captureScope,
    countText,
    isRunning,
    msText,
    statusText,
    statusType,
    unsupportedNotice,
    budgetBanner,
  } from './experimentCaptures.js';
  import { CAPTURE_ICON } from './vmActions.js';

  // how long a download the browser takes over keeps its button spinning
  const LINK_DOWNLOAD_SPIN_MS = 1500;

  // a capture's file as the experiment files API takes it; the size is
  // unknown (NaN) where minimega gives 0, so a large file is not a blob
  const fileOf = (capture) => {
    const size = Number(capture.size);
    return {
      name: capture.file.slice(capture.file.lastIndexOf('/') + 1),
      path: capture.file,
      size: Number.isFinite(size) && size > 0 ? size : NaN,
    };
  };

  // The Captures tab of the running experiment page: its experiment
  // captures, each one traffic capture of all hosts merged into one file on
  // the head node, with their per-host streams, the Open vSwitch mirror
  // budget, and the actions on them. The page owns the list; this tab asks
  // for it again every few seconds while it shows (`reload`), and after it
  // stops or deletes a capture.
  export default {
    name: 'ExperimentCapturesTab',
    components: { TimeWithAge },
    props: {
      experiment: { type: String, required: true },
      // whether the tab shows, which it polls only while it does
      active: { type: Boolean, default: false },
      captures: { type: Array, default: () => [] },
      budget: { type: Array, default: () => [] },
      budgetError: { type: String, default: '' },
      // { available, reason }: whether minimega supports experiment captures
      support: { type: Object, required: true },
      // why the list could not be read, if it could not
      error: { type: String, default: '' },
      loaded: { type: Boolean, default: false },
      loading: { type: Boolean, default: false },
    },
    // capture: open the capture dialog; reload: list the captures again;
    // stopping (id): the tab asks the server to stop the capture
    emits: ['capture', 'reload', 'stopping'],
    setup() {
      return {
        bytesText,
        captureDrops,
        captureScope,
        countText,
        isRunning,
        msText,
        statusText,
        statusType,
        captureIcon: CAPTURE_ICON,
      };
    },
    data() {
      return { opened: [], stopping: {}, downloading: {}, deleting: {} };
    },
    created() {
      if (this.active) this.startPolling();
    },
    beforeUnmount() {
      this.stopPolling();
      this.left = true;
    },
    watch: {
      // the tab polls only while it shows, and lists at once when shown
      active(on) {
        if (!on) {
          this.stopPolling();
          return;
        }
        this.$emit('reload');
        this.startPolling();
      },
    },
    computed: {
      canCreate() {
        return roleAllowed('experiments/captures', 'create', this.experiment);
      },
      canStop() {
        return roleAllowed('experiments/captures', 'delete', this.experiment);
      },
      canDownload() {
        return roleAllowed('experiments/files', 'get', this.experiment);
      },
      canDelete() {
        return roleAllowed('experiments/files', 'delete', this.experiment);
      },
      canOpenInWebShark() {
        return (
          webSharkInstalled(usePhenixStore().features) &&
          roleAllowed('experiments/captures', 'get', this.experiment) &&
          this.canDownload
        );
      },
      unavailableLabel() {
        return `Capture all experiment traffic is not available: ${this.support.reason}`;
      },
      unsupportedText() {
        return unsupportedNotice(this.support.reason);
      },
      banner() {
        return budgetBanner(this.budget, this.experiment);
      },
      emptyText() {
        if (!this.loaded) return 'Loading captures…';
        return 'This experiment has no experiment captures';
      },
    },
    methods: {
      startPolling() {
        this.stopPolling();
        this.stopPoll = startPoll(
          () => this.$emit('reload'),
          RESOURCES.captures.pollMs,
          // also without support: a later list can show that support started
          { when: () => inForeground() && !this.loading },
        );
      },

      stopPolling() {
        this.stopPoll?.();
        this.stopPoll = null;
      },

      // a capture's streams table opens and closes under its row
      toggle(id) {
        this.opened = this.opened.includes(id)
          ? this.opened.filter((o) => o !== id)
          : [...this.opened, id];
      },

      webSharkLink(capture) {
        if (!this.canOpenInWebShark || capture.state === 'failed') return null;
        if (!isRunning(capture) && !capture.file) return null;
        return {
          name: 'webshark',
          query: { exp: this.experiment, capture: capture.id },
        };
      },

      setFlag(name, id, on) {
        const { [id]: _, ...rest } = this[name];
        this[name] = on ? { ...rest, [id]: true } : rest;
      },

      // stops a capture at once: a capture stop asks nothing
      async stop(capture) {
        if (this.stopping[capture.id]) return;
        this.setFlag('stopping', capture.id, true);
        this.$emit('stopping', capture.id);
        try {
          await experimentsApi.stopExperimentCapture(
            this.experiment,
            capture.id,
          );
          if (this.left) return;
          this.$buefy.toast.open({
            message: `Stopped experiment capture ${capture.id}.`,
            type: 'is-success',
          });
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.setFlag('stopping', capture.id, false);
          if (!this.left) this.$emit('reload');
        }
      },

      // Saves a capture's file as the Files tab does: through a request up
      // to 256 MiB, else through the browser's own download.
      async download(capture) {
        if (this.downloading[capture.id]) return;
        this.setFlag('downloading', capture.id, true);
        const file = fileOf(capture);
        try {
          if (downloadsAsBlob(file)) {
            const response = await experimentFilesApi.downloadExperimentFile(
              this.experiment,
              file,
            );
            FileSaver.saveAs(response.data, file.name);
          } else {
            saveURL(
              experimentFileURL(this.experiment, file, usePhenixStore().token),
              file.name,
            );
            await new Promise((done) =>
              setTimeout(done, LINK_DOWNLOAD_SPIN_MS),
            );
          }
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.setFlag('downloading', capture.id, false);
        }
      },

      // asks before deleting a capture's file, as the Files tab does
      confirmDelete(capture) {
        const file = fileOf(capture);
        this.$buefy.dialog.confirm({
          title: 'Delete File',
          message:
            `Delete <b>${escapeHTML(file.name)}</b>` +
            (file.path != file.name ? ` (${escapeHTML(file.path)})` : '') +
            ` from experiment ${escapeHTML(this.experiment)}?` +
            ' It is removed from every cluster node and cannot be recovered.',
          cancelText: 'Cancel',
          confirmText: 'Delete',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => this.deleteFile(capture),
        });
      },

      async deleteFile(capture) {
        if (this.deleting[capture.id]) return;
        this.setFlag('deleting', capture.id, true);
        const file = fileOf(capture);
        try {
          await experimentFilesApi.deleteExperimentFile(this.experiment, file);
          if (this.left) return;
          this.$buefy.toast.open({
            message: `Deleted ${file.name}`,
            type: 'is-success',
          });
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.setFlag('deleting', capture.id, false);
          if (!this.left) this.$emit('reload');
        }
      },
    },
  };
</script>

<style scoped>
  .captures-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem 1rem;
    margin-bottom: 0.75rem;
  }

  .captures-intro {
    margin: 0 !important;
  }

  .capture-actions {
    display: inline-flex;
    gap: 5px;
  }

  .streams-toggle .icon.is-expanded {
    transform: rotate(90deg);
  }

  .skew-warning {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
  }

  /* beats Buefy's light detail row, as the state of health overview does */
  .experiment-captures :deep(.b-table .table tr.detail) {
    background: #3b3b3b;
  }

  .capture-streams {
    width: 100%;
    margin-bottom: 0;
    background: transparent;
    font-size: 0.9rem;
  }

  .capture-streams th,
  .capture-streams td {
    color: white;
  }
</style>
