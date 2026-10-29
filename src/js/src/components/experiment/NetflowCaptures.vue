<template>
  <div class="netflow-captures">
    <b-button ref="trigger" icon-left="download" size="is-small" @click="open">
      Download captures
    </b-button>
    <b-modal
      v-model="active"
      has-modal-card
      trap-focus
      aria-role="dialog"
      aria-modal
      aria-label="Netflow captures">
      <div class="modal-card netflow-captures-card">
        <header class="modal-card-head">
          <h2 class="modal-card-title">Recorded netflow captures</h2>
          <button
            type="button"
            class="delete"
            aria-label="Close"
            @click="active = false" />
        </header>
        <section
          ref="body"
          class="modal-card-body"
          tabindex="-1"
          :aria-busy="loading">
          <div class="netflow-captures-state">
            <p role="status">{{ status }}</p>
            <b-button
              v-if="failed"
              size="is-small"
              :aria-disabled="loading"
              @click="load">
              Retry
            </b-button>
          </div>
          <template v-if="captures.length > 0">
            <table class="table is-fullwidth is-narrow netflow-captures-table">
              <caption class="is-sr-only">
                Recorded netflow captures, newest first
              </caption>
              <thead>
                <tr>
                  <th scope="col">Started</th>
                  <th scope="col">Stopped</th>
                  <th scope="col" class="has-text-right">Flows</th>
                  <th scope="col" class="has-text-right">Size</th>
                  <th scope="col">Download</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="capture in captures" :key="capture.id">
                  <td><time-with-age :time="capture.started" /></td>
                  <td>
                    <b-tag
                      v-if="capture.state === 'recording' && capture.truncated"
                      type="is-warning">
                      Live only
                    </b-tag>
                    <b-tag
                      v-else-if="capture.state === 'recording'"
                      type="is-info">
                      Recording
                    </b-tag>
                    <b-tag
                      v-else-if="capture.state === 'interrupted'"
                      type="is-light">
                      Interrupted
                    </b-tag>
                    <time-with-age v-else :time="capture.stopped" />
                  </td>
                  <td class="has-text-right">
                    {{ flows(capture) }}
                    <b-tag v-if="capture.truncated" type="is-warning">
                      Truncated
                    </b-tag>
                  </td>
                  <td class="has-text-right netflow-captures-size">
                    {{ bytesText(capture.size) }}
                  </td>
                  <td>
                    <div class="buttons are-small netflow-captures-formats">
                      <b-button
                        v-for="format in formats"
                        :key="format.value"
                        size="is-small"
                        :aria-label="`Download capture started ${started(capture)} as ${format.name}`"
                        @click="download(capture, format.value)">
                        {{ format.label }}
                      </b-button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
            <p v-if="anyTruncated" class="help netflow-captures-note">
              Truncated captures stopped recording at the size limit
              (ui.netflow.max-size) or after a write error; later flows were
              only shown live, so a running one is listed as Live only.
            </p>
          </template>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <b-button :aria-disabled="loading" @click="load">Refresh</b-button>
          <b-button @click="active = false">Close</b-button>
        </footer>
      </div>
    </b-modal>
  </div>
</template>

<script>
  import {
    headNetflowCapture,
    listNetflowCaptures,
    netflowCaptureURL,
  } from '@/api/experiments.js';
  import { usePhenixStore } from '@/store.js';
  import { bytesText } from '@/utils/bytes.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import { isoTimeParts } from '@/utils/relativeTime.js';
  import TimeWithAge from '@/components/TimeWithAge.vue';

  // the formats a capture downloads as: its file extension, button label and
  // the name its button's accessible name uses
  const FORMATS = [
    { value: 'text', ext: 'txt', label: 'Text', name: 'text' },
    { value: 'csv', ext: 'csv', label: 'CSV', name: 'CSV' },
    { value: 'jsonl', ext: 'jsonl', label: 'JSONL', name: 'JSONL' },
  ];

  // The Download captures button of the netflow tab and its dialog, which
  // lists the experiment's recorded captures, newest first, to download.
  export default {
    name: 'NetflowCaptures',
    components: { TimeWithAge },
    props: {
      // the experiment whose captures are listed
      experiment: { type: String, required: true },
    },

    setup() {
      return { bytesText, formats: FORMATS };
    },

    data() {
      return {
        active: false,
        loading: false,
        failed: false,
        loaded: false,
        captures: [],
      };
    },

    computed: {
      anyTruncated() {
        return this.captures.some((c) => c.truncated);
      },

      // the dialog's state, announced as it changes
      status() {
        if (this.loading) return 'Loading captures...';
        if (this.failed) return 'Could not load captures.';
        if (this.loaded && this.captures.length === 0) {
          return 'No captures recorded yet. Starting a netflow capture records it.';
        }
        return '';
      },
    },

    watch: {
      // Buefy leaves focus in the closed dialog; return it to the button
      active(on) {
        if (!on) this.$nextTick(() => this.$refs.trigger?.$el?.focus());
      },
    },

    methods: {
      open() {
        this.active = true;
        this.load();
      },

      // Lists the captures, keeping the list and its buttons until the new
      // one arrives; focus lost with a removed button returns to the dialog.
      async load() {
        if (this.loading) return;
        this.loading = true;
        try {
          const { data } = await listNetflowCaptures(this.experiment);
          this.captures = data?.captures ?? [];
          this.failed = false;
          this.loaded = true;
        } catch (err) {
          this.failed = true;
          useErrorNotification(err);
        } finally {
          this.loading = false;
        }
        await this.$nextTick();
        const focused = document.activeElement;
        if (this.active && (!focused || focused === document.body)) {
          this.$refs.body?.focus();
        }
      },

      // the start time as its cell shows it, to the second
      started(capture) {
        return isoTimeParts(capture.started ?? '').join('');
      },

      flows(capture) {
        return capture.flows == null
          ? 'Unknown'
          : capture.flows.toLocaleString('en-US');
      },

      // Checks the capture is still there, so a gone one is reported rather
      // than saved as an error body, then downloads it through a link.
      async download(capture, format) {
        try {
          await headNetflowCapture(this.experiment, capture.id, format);
        } catch (err) {
          useErrorNotification(
            err?.response?.status === 404
              ? `Netflow capture ${capture.id} is no longer available.`
              : err,
          );
          this.load();
          return;
        }

        const ext = FORMATS.find((f) => f.value === format).ext;
        const link = document.createElement('a');
        link.href = netflowCaptureURL(
          this.experiment,
          capture.id,
          format,
          usePhenixStore().token,
        );
        link.download = `${this.experiment}-netflow-${capture.id}.${ext}`;
        document.body.appendChild(link);
        link.click();
        link.remove();
      },
    },
  };
</script>

<style scoped>
  .netflow-captures-card {
    width: min(56rem, 90vw);
  }

  .netflow-captures-formats {
    flex-wrap: nowrap;
  }

  /* the dialog opens inside the page's .content, whose h2 and table cell
     rules would make the title larger and spaced, and the cells top-aligned */
  .netflow-captures-card .modal-card-title {
    margin-bottom: 0;
    font-size: 1.5rem;
    font-weight: 400;
    line-height: 1.25;
  }

  .netflow-captures-table td {
    vertical-align: middle;
  }

  /* a size never splits from its unit beside the two-line times */
  .netflow-captures-size {
    white-space: nowrap;
  }

  /* Bulma's info blue gives white text only 3.5:1; its dark shade gives 5.3:1 */
  .netflow-captures-table .tag.is-info {
    background-color: #296fa8;
  }

  .netflow-captures-state {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
</style>
