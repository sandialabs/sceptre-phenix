<!-- The Console page's toolbar: the console's state and namespace, the
     minimega commands it can type, and what can be done with its output. -->
<template>
  <div class="console-toolbar" role="group" aria-label="Console controls">
    <span class="console-state" :class="stateView.class" role="status">
      <b-icon :icon="stateView.icon" size="is-small" />
      <span>{{ stateView.text }}</span>
    </span>

    <span class="console-namespace">
      <label class="label is-small" for="console-namespace">Namespace</label>
      <b-select
        id="console-namespace"
        ref="picker"
        :compat-fallthrough="false"
        size="is-small"
        :model-value="namespace"
        :disabled="!canPickNamespace"
        @focus="$emit('experiments')"
        @update:model-value="pickNamespace">
        <option value="">All (no namespace)</option>
        <optgroup v-if="running.length" label="Running">
          <option v-for="name in running" :key="name" :value="name">
            {{ name }}
          </option>
        </optgroup>
        <optgroup v-if="stopped.length" label="Not running">
          <option v-for="name in stopped" :key="name" :value="name">
            {{ name }}
          </option>
        </optgroup>
      </b-select>
    </span>

    <b-dropdown
      ref="commands"
      class="console-commands"
      aria-role="menu"
      :disabled="!open"
      @active-change="menuOpen = $event">
      <template #trigger>
        <b-button
          size="is-small"
          icon-left="terminal"
          icon-right="chevron-down"
          :disabled="!open"
          aria-haspopup="menu"
          :aria-expanded="String(menuOpen)">
          Commands
        </b-button>
      </template>
      <b-dropdown-item
        v-for="c in commands"
        :key="c.command"
        class="console-command"
        aria-role="menuitem"
        @click="$emit('command', c)"
        @keydown.enter.prevent="pick(c)"
        @keydown.space.prevent="pick(c)">
        <code>{{ c.command.trim() }}</code
        >{{ ' ' }}
        <small>{{ c.hint }}{{ c.run ? '' : ' (not entered)' }}</small>
      </b-dropdown-item>
      <hr class="dropdown-divider" />
      <b-dropdown-item has-link aria-role="menuitem">
        <a :href="reference" target="_blank" rel="noopener noreferrer">
          <b-icon icon="book" size="is-small" />
          minimega command reference
        </a>
      </b-dropdown-item>
    </b-dropdown>

    <b-button
      size="is-small"
      icon-left="search"
      :aria-pressed="String(searching)"
      @click="$emit('search')">
      Search
    </b-button>

    <span class="console-font" role="group" aria-label="Text size">
      <b-button
        size="is-small"
        icon-left="minus"
        aria-label="Smaller text"
        :disabled="fontSize <= FONT_SIZE.min"
        @click="$emit('font-size', fontSize - 1)" />
      <span class="console-font-size">{{ fontSize }} px</span>
      <b-button
        size="is-small"
        icon-left="plus"
        aria-label="Larger text"
        :disabled="fontSize >= FONT_SIZE.max"
        @click="$emit('font-size', fontSize + 1)" />
      <b-button
        size="is-small"
        icon-left="undo-alt"
        aria-label="Reset the text size"
        :disabled="fontSize === FONT_SIZE.default"
        @click="$emit('font-size', FONT_SIZE.default)" />
    </span>

    <b-button size="is-small" icon-left="eraser" @click="$emit('clear')">
      Clear
    </b-button>
    <b-button size="is-small" icon-left="copy" @click="$emit('copy')">
      Copy all
    </b-button>
    <b-button
      size="is-small"
      icon-left="download"
      aria-label="Download the transcript"
      @click="$emit('download')">
      Download
    </b-button>

    <span class="console-status" role="status" aria-live="polite">
      {{ status }}
    </span>

    <b-button
      class="console-end"
      size="is-small"
      type="is-danger"
      icon-left="power-off"
      :disabled="!live"
      @click="$emit('end')">
      End console
    </b-button>
  </div>
</template>

<script>
  import {
    FONT_SIZE,
    MINIMEGA_COMMANDS,
    MINIMEGA_REFERENCE,
  } from '@/components/console/consoleTools.js';

  const STATES = {
    connecting: {
      icon: 'sync-alt',
      text: 'Connecting',
      class: 'is-connecting',
    },
    open: { icon: 'check-circle', text: 'Connected', class: 'is-open' },
    ended: { icon: 'ban', text: 'Ended', class: 'is-ended' },
    lost: {
      icon: 'exclamation-triangle',
      text: 'Connection lost',
      class: 'is-lost',
    },
  };

  export default {
    name: 'ConsoleToolbar',
    props: {
      // the session's state: connecting, open, ended or lost
      state: { type: String, default: 'connecting' },
      namespace: { type: String, default: '' },
      // [{ name, running }], the experiments the namespace picker offers
      experiments: { type: Array, default: () => [] },
      fontSize: { type: Number, default: FONT_SIZE.default },
      searching: { type: Boolean, default: false },
      // what the last copy did, for screen readers
      status: { type: String, default: '' },
    },
    emits: [
      'clear',
      'command',
      'copy',
      'download',
      'end',
      'experiments',
      'font-size',
      'namespace',
      'search',
    ],
    data() {
      return { menuOpen: false };
    },
    computed: {
      FONT_SIZE: () => FONT_SIZE,
      commands: () => MINIMEGA_COMMANDS,
      reference: () => MINIMEGA_REFERENCE,
      stateView() {
        return STATES[this.state] ?? STATES.connecting;
      },
      open() {
        return this.state === 'open';
      },
      // the console still runs, or may, after a lost connection
      live() {
        return this.state !== 'ended';
      },
      canPickNamespace() {
        return this.state !== 'connecting';
      },
      // the names of the experiments, with the console's own namespace even
      // when the list does not have it
      names() {
        const names = this.experiments.map((exp) => exp.name);
        return this.namespace && !names.includes(this.namespace)
          ? [this.namespace, ...names]
          : names;
      },
      running() {
        const running = new Set(
          this.experiments.filter((exp) => exp.running).map((exp) => exp.name),
        );
        return this.names.filter((name) => running.has(name)).sort();
      },
      stopped() {
        const running = new Set(this.running);
        return this.names.filter((name) => !running.has(name)).sort();
      },
    },
    methods: {
      // a command chosen with the keyboard: Buefy's items answer only clicks
      pick(command) {
        this.$emit('command', command);
        if (this.$refs.commands) this.$refs.commands.isActive = false;
      },
      // The select goes on showing the console's namespace: the page asks
      // first, and the namespace changes only once the new console starts.
      pickNamespace(name) {
        if (name !== this.namespace) {
          this.$emit('namespace', name);
          this.$nextTick(() => {
            if (this.$refs.picker) this.$refs.picker.selected = this.namespace;
          });
        }
      },
    },
  };
</script>

<style scoped>
  .console-toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 1.5rem;
    margin-bottom: 0.5rem;
  }

  .console-state {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    font-weight: 600;
    min-width: 9rem;
  }

  .console-namespace,
  .console-font {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
  }

  .console-namespace .label {
    margin: 0;
  }

  .console-font-size {
    min-width: 3rem;
    text-align: center;
  }

  .console-command {
    display: flex !important;
    flex-direction: column;
    white-space: nowrap;
  }

  .console-commands :deep(.dropdown-content) {
    max-height: 60vh;
    overflow-y: auto;
  }

  .console-status:empty {
    display: none;
  }

  /* the right edge of the toolbar is the right edge of the terminal */
  .console-end {
    margin-left: auto;
  }
</style>
