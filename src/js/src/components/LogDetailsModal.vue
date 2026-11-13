<template>
  <b-modal
    :model-value="entry !== null"
    has-modal-card
    aria-label="Log message details"
    @close="$emit('close')">
    <div v-if="shown" class="modal-card log-details">
      <header class="modal-card-head">
        <h2 class="modal-card-title">Log message details</h2>
      </header>
      <section class="modal-card-body">
        <p class="log-details-meta">
          <log-level-tag :level="shown.level" />
          <span class="log-details-time">{{ shown.timestamp }}</span>
          <b-tag class="log-details-type">{{ shown.type }}</b-tag>
        </p>
        <p v-if="parts.text" class="log-details-text">{{ parts.text }}</p>
        <template v-if="parts.fields.length">
          <h3 class="log-details-heading">Fields</h3>
          <dl class="log-details-fields">
            <div v-for="(field, i) in parts.fields" :key="i" class="log-field">
              <dt>{{ field.key }}</dt>
              <dd>{{ field.value }}</dd>
            </div>
          </dl>
        </template>
        <pre v-if="parts.trailing" class="log-details-trailing">{{
          parts.trailing
        }}</pre>
        <div class="log-details-raw-head">
          <h3 id="log-details-raw-title" class="log-details-heading">
            Full message
          </h3>
          <b-button
            class="log-details-copy"
            size="is-small"
            :icon-left="copied ? 'check-circle' : 'copy'"
            @click="copy">
            Copy
          </b-button>
          <span class="log-details-status" role="status" aria-live="polite">{{
            copyStatus
          }}</span>
        </div>
        <pre
          ref="raw"
          class="log-details-raw"
          role="region"
          tabindex="0"
          aria-labelledby="log-details-raw-title"
          >{{ shown.msg }}</pre
        >
      </section>
      <footer class="modal-card-foot">
        <span v-if="index >= 0" class="log-details-position">
          Entry {{ index + 1 }} of {{ total }}
        </span>
        <span class="buttons">
          <template v-if="index >= 0">
            <b-button
              icon-left="angle-left"
              :aria-disabled="index === 0 ? 'true' : undefined"
              @click="index > 0 && $emit('previous')">
              Previous
            </b-button>
            <b-button
              icon-right="angle-right"
              :aria-disabled="index === total - 1 ? 'true' : undefined"
              @click="index < total - 1 && $emit('next')">
              Next
            </b-button>
          </template>
          <b-button @click="$emit('close')">Close</b-button>
        </span>
      </footer>
    </div>
  </b-modal>
</template>

<script>
  import LogLevelTag from '@/components/LogLevelTag.vue';
  import { parseLogMessage } from '@/utils/logFields.js';

  // how long "Copied" stays after a copy
  const COPIED_MS = 3000;

  // The Logs page's one details dialog: the entry's level, time and type,
  // its message split into text and key=value fields, and the full message
  // to copy. The page passes null to close it.
  export default {
    name: 'LogDetailsModal',
    components: { LogLevelTag },
    props: {
      // the log entry shown, or null when the dialog is closed
      entry: { type: Object, default: null },
      // the entry's place in the page's filtered list (-1 when not in it),
      // and how many entries that list has
      index: { type: Number, default: -1 },
      total: { type: Number, default: 0 },
    },
    emits: ['close', 'previous', 'next'],
    data() {
      return {
        // the last entry opened, kept while the dialog closes
        shown: this.entry,
        copied: false,
        copyStatus: '',
      };
    },
    computed: {
      parts() {
        return parseLogMessage(this.shown?.msg);
      },
    },
    watch: {
      entry(entry) {
        if (!entry) return;
        this.shown = entry;
        this.copied = false;
        this.copyStatus = '';
      },
    },
    beforeUnmount() {
      clearTimeout(this.copyTimer);
    },
    methods: {
      // Copies the full message; the page has no Clipboard API over plain
      // http, so on a failure the text is selected for the user to copy.
      async copy() {
        clearTimeout(this.copyTimer);
        this.copyStatus = '';
        try {
          await navigator.clipboard.writeText(this.shown.msg);
        } catch {
          this.copied = false;
          this.selectRaw();
          this.copyStatus =
            'Could not copy. The message is selected: press Ctrl+C to copy it.';
          return;
        }
        this.copied = true;
        this.copyStatus = 'Copied';
        this.copyTimer = setTimeout(() => {
          this.copied = false;
          this.copyStatus = '';
        }, COPIED_MS);
      },
      selectRaw() {
        const raw = this.$refs.raw;
        if (!raw) return;
        raw.focus();
        window.getSelection()?.selectAllChildren(raw);
      },
    },
  };
</script>

<style scoped>
  /* Text is white (8.9:1) or #d0ecf3 (7.2:1) on the card's #4a4a4a, and
     white (12.6:1) on the full message's #333. */
  .log-details {
    width: min(90ch, calc(100vw - 40px));
    max-height: calc(100vh - 40px);
  }

  .log-details .modal-card-body {
    line-height: 1.5;
    overflow-y: auto;
  }

  .log-details-meta {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 0.75rem;
    margin-bottom: 0.75rem;
    font-family: monospace;
  }

  .log-details-text {
    margin-bottom: 1rem;
    font-size: 1.1em;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .log-details-heading {
    margin: 0 0 0.25rem;
    font-size: 0.85em;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  /* key and value columns; a long key or value wraps rather than widening
     the dialog */
  .log-details-fields {
    display: grid;
    grid-template-columns: fit-content(40%) minmax(0, 1fr);
    margin: 0 0 1rem;
    font-family: monospace;
  }

  .log-field {
    display: grid;
    grid-column: 1 / -1;
    grid-template-columns: subgrid;
    column-gap: 1.5rem;
    padding: 0.125rem 0.5rem;
  }

  .log-field:nth-child(odd) {
    background-color: #404040;
  }

  .log-field dt {
    color: #d0ecf3;
    overflow-wrap: anywhere;
  }

  .log-field dd {
    margin: 0;
    color: #ffffff;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .log-details-raw-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 0.75rem;
    margin-bottom: 0.25rem;
  }

  .log-details-raw-head .log-details-heading {
    margin: 0;
  }

  /* the pre's border is 5.9:1 on #333 and 4.1:1 on the card */
  .log-details-raw,
  .log-details-trailing {
    max-height: 12em;
    overflow: auto;
    padding: 0.5rem 0.75rem;
    border: 1px solid #b1b1b1;
    background-color: #333333;
    color: #ffffff;
    font-size: 0.85em;
    line-height: 1.5;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .log-details-trailing {
    margin-bottom: 1rem;
  }

  .log-details .modal-card-foot {
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  .log-details .modal-card-foot .buttons {
    margin: 0 0 0 auto;
  }

  .log-details .modal-card-foot .buttons :deep(.button) {
    margin-bottom: 0;
  }

  .log-details .button[aria-disabled='true'] {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
