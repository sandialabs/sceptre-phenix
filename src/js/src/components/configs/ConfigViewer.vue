<template>
  <div class="modal-card config-viewer">
    <header class="modal-card-head x-modal-dark config-viewer-head">
      <h2 class="modal-card-title x-config-text">{{ title }}</h2>
      <b-tooltip
        v-if="obj && experimentLink"
        :label="`Close this card and open the ${obj.metadata.name} experiment page`"
        type="is-light"
        position="is-bottom"
        multilined>
        <router-link
          :to="experimentLink"
          class="button is-small is-light config-viewer-go"
          data-testid="viewer-experiment"
          :aria-label="`Go to experiment ${obj.metadata.name}`"
          @click="$emit('leave')">
          <b-icon icon="flask" size="is-small" aria-hidden="true" />
        </router-link>
      </b-tooltip>
      <ul
        v-if="obj && annotations.length"
        class="config-annotations"
        aria-label="Annotations">
        <li
          v-for="a in annotations"
          :key="a.key"
          class="tag config-annotation"
          :title="a.text">
          {{ a.text }}
        </li>
      </ul>
    </header>
    <section class="modal-card-body x-modal-dark config-viewer-body">
      <template v-if="obj">
        <dl class="config-meta" data-testid="viewer-meta">
          <div v-for="item in details" :key="item.label">
            <dt>{{ item.label }}</dt>
            <dd>
              <time-with-age
                v-if="item.time"
                :time="item.value"
                fallback="unknown" />
              <template v-else>{{ item.value }}</template>
            </dd>
          </div>
        </dl>
        <div v-if="usedBy" class="config-links" data-testid="viewer-used-by">
          <span>Experiments that use this {{ kindName }}:</span>
          {{ ' ' }}
          <span v-if="usedBy.length === 0">none</span>
          <ul v-else class="config-used-by">
            <li v-for="exp in usedBy.slice(0, USED_BY_SHOWN)" :key="exp">
              <router-link
                v-if="roleAllowed('experiments', 'get', exp)"
                :to="{ name: 'experiment', params: { id: exp } }"
                @click="$emit('leave')">
                {{ exp }}
              </router-link>
              <template v-else>{{ exp }}</template>
            </li>
            <li v-if="usedBy.length > USED_BY_SHOWN">
              and {{ usedBy.length - USED_BY_SHOWN }} more
            </li>
          </ul>
        </div>
      </template>
      <div class="config-tools">
        <div class="buttons has-addons" role="group" aria-label="Format">
          <button
            v-for="f in FORMATS"
            :key="f.value"
            type="button"
            class="button is-small"
            :class="{ 'is-info': format === f.value }"
            :aria-pressed="String(format === f.value)"
            @click="format = f.value">
            {{ f.label }}
          </button>
        </div>
        <b-switch v-model="wrap" size="is-small" type="is-light">
          Wrap lines
        </b-switch>
        <button
          type="button"
          class="button is-small"
          data-testid="viewer-copy"
          :disabled="!obj"
          @click="copy">
          <b-icon :icon="copied ? 'check-circle' : 'clipboard'" />
          <span>{{ copied ? 'Copied' : 'Copy' }}</span>
        </button>
        <button
          type="button"
          class="button is-small"
          data-testid="viewer-download"
          :class="{ 'is-loading': downloading }"
          :aria-busy="String(downloading)"
          @click="$emit('download')">
          <b-icon icon="download" />
          <span>Download</span>
        </button>
        <button
          v-if="canEdit"
          type="button"
          class="button is-small"
          data-testid="viewer-edit"
          @click="$emit('edit')">
          <b-icon icon="edit" />
          <span>Edit</span>
        </button>
        <span class="is-sr-only" role="status">{{ copyStatus }}</span>
      </div>
      <textarea
        ref="text"
        class="textarea x-config-text has-fixed-size config-viewer-text"
        :class="{ 'is-unwrapped': !wrap }"
        :wrap="wrap ? 'soft' : 'off'"
        :aria-label="`Config, as ${formatLabel}`"
        :value="text"
        readonly />
    </section>
    <footer
      v-if="builder"
      class="modal-card-foot x-modal-dark buttons is-right">
      <!-- A topology opens in the Builder: its diagram when it has one,
           else the Import dialog, which makes one. The button has the
           colors of the list's Builder tag, whose text meets the 4.5:1
           contrast that white on the info color does not. -->
      <button
        v-prefetch-route="{ name: 'builder' }"
        class="button is-info is-light"
        data-testid="viewer-builder"
        @click="$emit('builder')">
        {{ builder === 'open' ? 'Open in Builder' : 'Import into Builder' }}
      </button>
    </footer>
  </div>
</template>

<script>
  import YAML from 'js-yaml';

  import TimeWithAge from '@/components/TimeWithAge.vue';
  import { LEGACY_ANNOTATION, isBuilderAnnotation } from '@/builder/configs.js';
  import { showError } from '@/utils/errorNotif';
  import { roleAllowed } from '@/utils/rbac.js';
  import { vPrefetchRoute } from '@/utils/prefetch.js';

  const FORMATS = [
    { value: 'yaml', label: 'YAML' },
    { value: 'json', label: 'JSON' },
  ];
  const USED_BY_SHOWN = 10;
  const COPIED_MS = 2000;

  // The body of the Configs page's read-only viewer: the config's details,
  // then its text as YAML or JSON. The page owns the modal and the actions.
  export default {
    name: 'ConfigViewer',
    components: { TimeWithAge },
    directives: { prefetchRoute: vPrefetchRoute },
    props: {
      title: { type: String, default: '' },
      // the full config, or null while it loads
      obj: { type: Object, default: null },
      // what the Builder does with the config: 'open', 'import', or ''
      builder: { type: String, default: '' },
      canEdit: { type: Boolean, default: false },
      downloading: { type: Boolean, default: false },
      // the experiments that use a topology or scenario; null for other kinds
      usedBy: { type: Array, default: null },
    },
    emits: ['builder', 'edit', 'download', 'leave'],
    setup() {
      return { roleAllowed, FORMATS, USED_BY_SHOWN };
    },
    data() {
      return { format: 'yaml', wrap: true, copied: false, copyStatus: '' };
    },
    beforeUnmount() {
      clearTimeout(this.copyTimer);
    },
    computed: {
      formatLabel() {
        return FORMATS.find((f) => f.value === this.format).label;
      },
      text() {
        if (!this.obj) return 'Loading…';
        return this.format === 'json'
          ? JSON.stringify(this.obj, null, 2)
          : YAML.dump(this.obj);
      },
      kindName() {
        return String(this.obj?.kind ?? '').toLowerCase();
      },
      details() {
        const meta = this.obj.metadata ?? {};
        return [
          { label: 'Kind', value: this.obj.kind },
          { label: 'Name', value: meta.name },
          { label: 'Created', value: meta.created ?? '', time: true },
          { label: 'Updated', value: meta.updated ?? '', time: true },
        ];
      },
      // The Builder's own annotations show their keys only: their values are
      // long documents. The legacy diagram is left out (its text is snipped).
      annotations() {
        const all = this.obj.metadata?.annotations ?? {};
        return Object.entries(all)
          .filter(([key]) => key !== LEGACY_ANNOTATION)
          .map(([key, value]) => ({
            key,
            text: isBuilderAnnotation(key) ? key : `${key}: ${value}`,
          }));
      },
      experimentLink() {
        const name = this.obj.metadata?.name;
        if (this.obj.kind !== 'Experiment' || !name) return null;
        if (!roleAllowed('experiments', 'get', name)) return null;
        return { name: 'experiment', params: { id: name } };
      },
    },
    watch: {
      obj() {
        this.resetCopied();
      },
    },
    methods: {
      // Copies the text shown. The page has no Clipboard API when it is
      // served over plain http, so the text area's selection is copied then.
      async copy() {
        let done;
        try {
          await navigator.clipboard.writeText(this.text);
          done = true;
        } catch {
          this.$refs.text.select();
          done = document.execCommand?.('copy') === true;
        }
        clearTimeout(this.copyTimer);
        this.copied = done;
        this.copyStatus = done
          ? `Copied the config as ${this.formatLabel}.`
          : '';
        if (!done) {
          showError(
            'The browser did not copy the config.',
            'Select the text and copy it by hand.',
          );
        }
        this.copyTimer = setTimeout(this.resetCopied, COPIED_MS);
      },
      resetCopied() {
        clearTimeout(this.copyTimer);
        this.copied = false;
        this.copyStatus = '';
      },
    },
  };
</script>

<style scoped>
  /* wide and tall when the window has room; the text scrolls */
  .config-viewer {
    width: min(90vw, 80rem);
    height: calc(100vh - 40px);
  }

  /* the annotations sit on the title's line, and wrap below it when long */
  .config-viewer-head {
    flex-wrap: wrap;
    gap: 0.5rem 1rem;
    padding-bottom: 0.75rem;
  }

  .config-viewer-head .modal-card-title {
    flex: 0 1 auto;
    margin: 0;
  }

  /* the link sits just right of the title, before the annotations */
  .config-viewer-head .modal-card-title + .b-tooltip {
    flex: none;
  }

  .config-viewer-body {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    padding-top: 0.5rem;
  }

  .config-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1.5rem;
    margin: 0;
  }

  .config-meta dt {
    font-weight: bold;
  }

  .config-meta dd {
    margin: 0;
    overflow-wrap: anywhere;
  }

  /* the age's usual grey is too faint on the dialog's background */
  .config-meta :deep(.time-with-age-age) {
    color: inherit !important;
  }

  .config-annotations,
  .config-used-by {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 0.5rem;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .config-annotation {
    max-width: 30rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: inline-block;
    line-height: 2em;
  }

  .config-links {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin: 0;
  }

  .config-tools {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 1rem;
  }

  .config-tools .buttons {
    margin-bottom: 0;
  }

  .config-tools .buttons .button {
    margin-bottom: 0;
  }

  .config-viewer-text {
    flex: 1 1 auto;
    min-height: 12rem;
    max-height: none;
    font-family: monospace;
    tab-size: 2;
    /* 7.2:1; #686868 and whitesmoke were 5.1:1. */
    background-color: #575757;
    color: white;
  }

  .config-viewer-text.is-unwrapped {
    white-space: pre;
    overflow-x: auto;
  }
</style>
