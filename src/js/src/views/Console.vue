<template>
  <div class="console-page">
    <console-toolbar
      v-if="session"
      :state="state"
      :namespace="namespace"
      :experiments="experiments"
      :font-size="fontSize"
      :searching="searching"
      :status="status"
      @clear="session.clear()"
      @command="typeCommand"
      @copy="copyAll"
      @download="download"
      @end="confirmEnd"
      @experiments="loadExperiments"
      @font-size="setFontSize"
      @namespace="changeNamespace"
      @search="toggleSearch" />
    <console-search
      v-if="session && searching"
      ref="search"
      :find="find"
      @clear="session?.clearSearch()"
      @close="closeSearch" />

    <div
      v-if="quitWarning"
      class="notification is-warning console-warning"
      role="alert">
      <button
        type="button"
        class="delete"
        aria-label="Dismiss the warning"
        @click="dismissQuitWarning"></button>
      <p v-if="quitWarning === 'now'">
        <strong><code>quit</code> with a delay stops minimega.</strong>
        minimega stops after the delay, for every user and every experiment. It
        does not ask again.
      </p>
      <p v-else>
        <strong><code>quit</code> stops minimega</strong> for every user and
        every experiment, not only this console. minimega asks you to enter
        <code>quit</code> again before it stops. To close only this console,
        enter <code>disconnect</code> or press <kbd>Ctrl+D</kbd>.
      </p>
    </div>

    <div
      v-if="reconnect"
      class="notification is-light console-reconnect"
      role="status">
      <span>
        Connection lost. Reconnecting in {{ reconnect.remaining }} s (try
        {{ reconnect.attempt }} of {{ reconnect.of }}).
      </span>
      <b-button size="is-small" type="is-primary" @click="reconnectNow">
        Reconnect now
      </b-button>
      <b-button size="is-small" @click="cancelReconnect">Cancel</b-button>
    </div>

    <section v-if="!session && message" class="hero is-light is-bold is-large">
      <div class="hero-body">
        <div class="container" style="text-align: center">
          <h1 class="title">{{ message }}</h1>
        </div>
      </div>
    </section>
    <div
      ref="host"
      class="console-host"
      :style="height ? { height: `${height}px` } : null"></div>
    <div v-if="restartable" class="mt-3">
      <b-button type="is-primary" @click="restartByHand">
        {{ restartLabel }}
      </b-button>
    </div>
  </div>
</template>

<script>
  import { debounce } from 'lodash-es';

  import { listExperiments } from '@/api/experiments.js';
  import ConsoleSearch from '@/components/console/ConsoleSearch.vue';
  import ConsoleToolbar from '@/components/console/ConsoleToolbar.vue';
  import {
    clampFontSize,
    copyText,
    darkTerminal,
    onMac,
    quitCommand,
    readFontSize,
    RECONNECT_DELAYS,
    reconnectDelay,
    transcriptName,
    watchPageTheme,
    writeFontSize,
  } from '@/components/console/consoleTools.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import { saveURL } from '@/utils/experimentFiles.js';
  import { currentConsole } from '@/utils/consoleSession.js';
  import {
    consolePhase,
    openConsole,
    replaceConsole,
  } from '@/utils/consoleTerminal.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { usePhenixStore } from '@/store.js';
  import { noResponseMessage, SHUTTING_DOWN } from '@/utils/unreachable.js';
  import { roomInWindow } from '@/utils/viewportFit.js';

  // the terminal is never shorter than this, in pixels
  const MIN_HEIGHT = 240;
  const STATUS_MS = 4000;

  // the pages each key scrolls the output
  const PAGE_KEYS = { PageUp: -1, PageDown: 1 };

  // the server's own message in a failed request's body, or null
  const serverMessage = (err) => {
    const data = err?.response?.data;
    const text = typeof data === 'string' ? data : data?.message;
    return typeof text === 'string' && text.trim() ? text.trim() : null;
  };

  // a failure to open the console that a later try may not have
  const passing = (err) => !err.response || err.response.status >= 500;

  // failures that trying again by hand does not change
  const FINAL = new Set([403, 405]);

  // The console session outlives this page (see consoleSession.js): leaving
  // the page only takes its terminal out of the page, and coming back puts
  // the same terminal, with its output, back in.
  export default {
    components: { ConsoleSearch, ConsoleToolbar },

    data() {
      return {
        session: null,
        state: null,
        error: null,
        // the namespace of the console to start next
        wanted: '',
        experiments: [],
        fontSize: readFontSize(),
        searching: false,
        status: '',
        // 'confirm' or 'now' once the user has entered a quit
        quitWarning: null,
        // tries made to reconnect since the console was last open
        attempts: 0,
        // whether trying again by hand may open the console
        retryable: false,
        // { attempt, of, remaining } while a reconnect waits
        reconnect: null,
        height: null,
        mac: onMac(),
      };
    },

    computed: {
      message() {
        if (this.error) {
          return this.error;
        }

        switch (consolePhase.value) {
          case 'reconnecting':
            return 'Reconnecting to the console…';
          case 'starting':
            return 'Starting console…';
          default:
            return null;
        }
      },
      namespace() {
        return this.session?.namespace ?? this.wanted;
      },
      // the console ended, or its connection was lost and is not being
      // reconnected
      restartable() {
        if (this.reconnect) return false;
        if (this.state === 'ended' || this.state === 'lost') return true;
        if (this.session || !this.error) return false;
        return this.attempts > 0 || this.retryable;
      },
      restartLabel() {
        if (this.state === 'ended') return 'Start a new console';
        if (this.state === 'lost' || this.attempts > 0) return 'Reconnect';
        return 'Try again';
      },
      // what takes room above the terminal, which then fits again
      above() {
        return [this.searching, !!this.quitWarning, !!this.reconnect].join();
      },
    },

    watch: {
      above() {
        this.$nextTick(() => this.fitTerminal());
      },
      state(state) {
        if (state === 'open') {
          this.attempts = 0;
        } else if (state === 'lost') {
          this.scheduleReconnect();
        } else if (state === 'ended') {
          this.cancelReconnect();
        }
      },
    },

    created() {
      this.handleResize = debounce(() => this.fitTerminal(), 100);
    },

    mounted() {
      window.addEventListener('resize', this.handleResize);
      if (typeof ResizeObserver === 'function') {
        this.observer = new ResizeObserver(() => this.handleResize());
        this.observer.observe(this.$refs.host);
      }
      this.stopWatchingTheme = watchPageTheme((dark) =>
        this.session?.setTheme(dark),
      );
      this.stopWatchingLogout = usePhenixStore().$onAction?.(({ name }) => {
        if (name === 'logout') this.cancelReconnect();
      });
      this.loadExperiments();

      // the console this tab already has open shows straight away
      const session = currentConsole();
      if (session) {
        this.show(session);
      } else {
        this.open();
      }
    },

    beforeUnmount() {
      this.left = true;
      window.removeEventListener('resize', this.handleResize);
      this.handleResize.cancel();
      this.observer?.disconnect();
      this.stopWatchingTheme?.();
      this.stopWatchingLogout?.();
      this.cancelReconnect();
      clearTimeout(this.statusTimer);
      this.release();
    },

    methods: {
      async open() {
        this.error = null;

        try {
          const session = await openConsole(this.wanted);
          // left while it opened: the session waits for the next visit
          if (!this.left) {
            this.show(session);
          }
        } catch (err) {
          if (this.left) {
            return;
          }

          this.retryable = !FINAL.has(err.response?.status);
          switch (err.response?.status) {
            // the server was started without --minimega-console
            case 405:
              this.error = 'Console access is not configured.';
              break;
            case 403:
              this.error = 'You do not have access to the console.';
              break;
            case 503:
              this.error = SHUTTING_DOWN;
              break;
            default:
              this.error =
                noResponseMessage(err) ??
                serverMessage(err) ??
                'Could not start the console.';
              console.warn('failed to start the console', err);
          }

          if (this.attempts > 0 && passing(err)) {
            this.scheduleReconnect();
          }
        }
      },

      show(session) {
        this.session = session;
        this.state = session.state;
        this.wanted = session.namespace;
        this.stopWatching = session.onChange((state) => {
          this.state = state;
        });
        this.stopSubmits = session.onSubmit((line) => this.submitted(line));
        session.setKeyHandler((event) => this.handleKey(event));
        session.setTheme(darkTerminal());
        session.mount(this.$refs.host);
        session.setFontSize(this.fontSize);
        this.fitTerminal();
      },

      release() {
        this.stopWatching?.();
        this.stopWatching = null;
        this.stopSubmits?.();
        this.stopSubmits = null;
        this.session?.setKeyHandler(null);
        this.session?.unmount();
        this.session = null;
        this.state = null;
        this.searching = false;
        this.quitWarning = null;
      },

      restart() {
        this.release();
        this.open();
      },

      restartByHand() {
        this.attempts = 0;
        this.restart();
      },

      // Gives the terminal the height left above the footer, then fits it.
      fitTerminal() {
        const host = this.$refs.host;
        const room = host ? roomInWindow(host) : null;
        if (room !== null) {
          const height = Math.max(MIN_HEIGHT, Math.floor(room));
          if (height !== this.height) this.height = height;
        }
        this.$nextTick(() => this.session?.fit());
      },

      async loadExperiments() {
        if (!roleAllowed('experiments', 'list') || this.listing) return;
        this.listing = true;
        try {
          const resp = await listExperiments();
          const list = resp?.data?.experiments ?? [];
          this.experiments = list.map((exp) => ({
            name: exp.name,
            running: Boolean(exp.running),
          }));
        } catch (err) {
          console.warn('failed to list the experiments for the console', err);
        } finally {
          this.listing = false;
        }
      },

      handleKey(event) {
        if (event.type !== 'keydown') return true;
        const key = event.key?.toLowerCase();
        const command = this.mac ? event.metaKey : event.ctrlKey;

        if (command && !event.shiftKey && !event.altKey && key === 'f') {
          event.preventDefault();
          this.openSearch();
          return false;
        }
        if (!this.mac && event.ctrlKey && event.shiftKey && key === 'c') {
          event.preventDefault();
          const text = this.session?.selection();
          if (text) this.copy(text, 'the selected text');
          return false;
        }
        // the browser pastes into the terminal
        if (!this.mac && event.ctrlKey && event.shiftKey && key === 'v') {
          return false;
        }
        return this.handleTerminalKey(event, key);
      },

      // Ctrl+L, the page keys and Ctrl+R, the same on every platform.
      handleTerminalKey(event, key) {
        const ctrlOnly =
          event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey;
        const noModifier = !event.ctrlKey && !event.metaKey && !event.altKey;

        if (ctrlOnly && key === 'l') {
          event.preventDefault();
          this.session?.clear();
          return false;
        }
        // the terminal sends it on to minimega's reverse search; the
        // browser would reload the page
        if (ctrlOnly && key === 'r') {
          event.preventDefault();
          return true;
        }
        if (noModifier && !event.shiftKey && PAGE_KEYS[event.key]) {
          event.preventDefault();
          this.session?.scrollPages(PAGE_KEYS[event.key]);
          return false;
        }
        if (noModifier && event.shiftKey && event.key === 'Home') {
          event.preventDefault();
          this.session?.scrollToTop();
          return false;
        }
        if (noModifier && event.shiftKey && event.key === 'End') {
          event.preventDefault();
          this.session?.scrollToBottom();
          return false;
        }
        return true;
      },

      submitted(line) {
        const quit = quitCommand(line);
        if (quit) {
          this.quitWarning = quit;
        } else if (line.trim()) {
          this.quitWarning = null;
        }
      },

      dismissQuitWarning() {
        this.quitWarning = null;
        this.session?.focus();
      },

      // Ctrl+E and Ctrl+U first erase what the line has, so the command
      // is not added to it
      typeCommand({ command, run }) {
        this.session?.type(`\x05\x15${command}${run ? '\r' : ''}`);
      },

      find(text, options) {
        return this.session?.find(text, options) ?? false;
      },

      toggleSearch() {
        if (this.searching) {
          this.closeSearch();
        } else {
          this.openSearch();
        }
      },

      openSearch() {
        if (this.searching) {
          this.$refs.search?.focus();
        } else {
          this.searching = true;
        }
      },

      closeSearch() {
        this.searching = false;
        this.session?.clearSearch();
        this.session?.focus();
      },

      setFontSize(size) {
        this.fontSize = clampFontSize(size);
        writeFontSize(this.fontSize);
        this.session?.setFontSize(this.fontSize);
        this.session?.focus();
      },

      say(text) {
        clearTimeout(this.statusTimer);
        this.status = text;
        this.statusTimer = setTimeout(() => {
          this.status = '';
        }, STATUS_MS);
      },

      async copy(text, what, kept = '') {
        const done = await copyText(text);
        this.say(
          done ? `Copied ${what}.${kept}` : `The browser did not copy ${what}.`,
        );
        this.session?.focus();
      },

      // what to add when the terminal has dropped its oldest output
      kept() {
        const lines = this.session?.keptLines();
        return lines
          ? ` It has only the last ${lines.toLocaleString('en-US')} lines.`
          : '';
      },

      copyAll() {
        const text = this.session?.text() ?? '';
        return this.copy(text, 'the console output', this.kept());
      },

      download() {
        const text = this.session?.text() ?? '';
        const url = URL.createObjectURL(
          new Blob([`${text}\n`], { type: 'text/plain;charset=utf-8' }),
        );
        saveURL(url, transcriptName(new Date(), this.namespace));
        setTimeout(() => URL.revokeObjectURL(url), 1000);
        // the download shows itself; only a cut transcript needs a word
        const kept = this.session?.keptLines();
        if (kept) {
          this.say(
            `The transcript has only the last ${kept.toLocaleString('en-US')} lines.`,
          );
        }
        this.session?.focus();
      },

      confirmEnd() {
        this.$buefy.dialog.confirm({
          title: 'End the Console',
          message:
            'This ends your minimega command line. minimega and its experiments keep running.',
          cancelText: 'Cancel',
          confirmText: 'End console',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => this.endSession(),
          onCancel: () => this.session?.focus(),
        });
      },

      async endSession() {
        this.cancelReconnect();
        try {
          await this.session?.end();
        } catch (err) {
          useErrorNotification(err);
        }
      },

      changeNamespace(name) {
        const start = () => {
          this.wanted = name;
          return this.replaceConsole();
        };
        if (!this.session || this.state === 'ended') {
          return start();
        }
        this.$buefy.dialog.confirm({
          title: 'Change the Namespace',
          message: `This ends the console and starts one in ${
            name ? `the ${name} namespace` : 'no namespace'
          }.`,
          cancelText: 'Cancel',
          confirmText: 'Change',
          type: 'is-warning',
          hasIcon: true,
          onConfirm: start,
          onCancel: () => this.session?.focus(),
        });
      },

      // Starts a console in the wanted namespace, then ends the user's
      // console. When the start fails, the console the page shows stays.
      async replaceConsole() {
        this.cancelReconnect();
        this.attempts = 0;
        try {
          const session = await replaceConsole(this.wanted);
          if (this.left) return;
          this.release();
          this.show(session);
        } catch (err) {
          if (this.left) return;
          this.wanted = this.session?.namespace ?? '';
          useErrorNotification(err);
        }
      },

      scheduleReconnect() {
        clearTimeout(this.reconnectTimer);
        const delay = reconnectDelay(this.attempts);
        if (delay === null) {
          this.reconnect = null;
          return;
        }
        this.reconnect = {
          attempt: this.attempts + 1,
          of: RECONNECT_DELAYS.length,
          remaining: delay,
        };
        this.tick();
      },

      tick() {
        this.reconnectTimer = setTimeout(() => {
          if (!this.reconnect) return;
          this.reconnect.remaining--;
          if (this.reconnect.remaining <= 0) {
            this.reconnectNow();
          } else {
            this.tick();
          }
        }, 1000);
      },

      reconnectNow() {
        clearTimeout(this.reconnectTimer);
        this.reconnect = null;
        this.attempts++;
        this.restart();
      },

      cancelReconnect() {
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = null;
        this.reconnect = null;
      },
    },
  };
</script>

<style scoped>
  .console-host {
    min-height: 240px;
  }

  .console-host :deep(.console-terminal) {
    height: 100%;
  }

  .console-reconnect {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem;
  }
</style>
