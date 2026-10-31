<template>
  <div>
    <div class="xterm" ref="xterm"></div>
    <p :id="hintId" class="help">
      Press Ctrl+], then Tab or Shift+Tab, to leave the terminal.
    </p>
  </div>
</template>

<script>
  import axiosInstance from '@/utils/axios.js';
  import { usePhenixStore } from '@/store.js';

  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import { AttachAddon } from '@xterm/addon-attach';

  import '@xterm/xterm/css/xterm.css';

  let hintCount = 0;

  function isReleaseChord(ev) {
    return (
      ev.ctrlKey && !ev.shiftKey && !ev.altKey && !ev.metaKey && ev.key === ']'
    );
  }

  export default {
    props: ['wsPath', 'resizePath'],
    data() {
      hintCount += 1;
      return {
        socket: null,
        term: null,
        fit: null,
        attach: null,
        hintId: `terminal-hint-${hintCount}`,
        tabReleased: false,
      };
    },
    watch: {
      wsPath: function () {
        this.teardownSocket();
        this.setupTerminal();
      },
    },

    beforeUnmount() {
      window.removeEventListener('resize', this.handleResize);
      this.teardownSocket();
    },

    mounted() {
      const term = new Terminal();

      this.fit = new FitAddon();
      term.loadAddon(this.fit);

      term.attachCustomKeyEventHandler(this.handleKey);
      term.open(this.$refs.xterm);
      term.textarea?.setAttribute('aria-describedby', this.hintId);
      // A Ctrl+] that focus left behind does not apply when focus comes back.
      term.textarea?.addEventListener('blur', () => {
        this.tabReleased = false;
      });

      if (this.resizePath === undefined) {
        term.resize(80, 30);
      } else {
        term.onResize((size) => {
          var url =
            this.resizePath + '?cols=' + size.cols + '&rows=' + size.rows;
          axiosInstance.post(url).catch((err) => {
            console.log(err);
          });
        });

        this.fit.fit();
        window.addEventListener('resize', this.handleResize);
      }

      this.term = term;
      this.setupTerminal();
    },
    methods: {
      // Tab goes to the shell, so Ctrl+] (the telnet escape) lets the next
      // Tab or Shift+Tab move focus out of the terminal instead (WCAG 2.1.2).
      handleKey(ev) {
        if (
          ev.type !== 'keydown' ||
          ['Shift', 'Control', 'Alt', 'Meta'].includes(ev.key)
        ) {
          return true;
        }

        if (this.tabReleased) {
          this.tabReleased = false;
          if (ev.key === 'Tab') {
            return false;
          }
          // Not a way out, so the shell gets the Ctrl+] it held back.
          this.term.input('\x1d');
          return true;
        }

        if (isReleaseChord(ev)) {
          ev.preventDefault();
          this.tabReleased = true;
          return false;
        }

        return true;
      },
      setupTerminal() {
        this.socket = new WebSocket(this.getWsUrl());

        // the attach addon throws on input if it is loaded before the
        // socket is open, so wait for onopen to wire it up
        this.socket.onopen = () => {
          this.attach = new AttachAddon(this.socket);
          this.term.loadAddon(this.attach);

          if (this.resizePath !== undefined) {
            this.fit.fit();
          }
        };

        this.socket.onerror = () => {
          this.term.write('\r\n[terminal connection error]\r\n');
        };

        this.socket.onclose = () => {
          this.term.write('\r\n[terminal connection closed]\r\n');
        };
      },
      teardownSocket() {
        this.tabReleased = false;
        if (this.socket) {
          this.socket.onopen = null;
          this.socket.onerror = null;
          this.socket.onclose = null;
          this.socket.close();
          this.socket = null;
        }

        if (this.attach) {
          this.attach.dispose();
          this.attach = null;
        }
      },
      getWsUrl() {
        const phenixStore = usePhenixStore();
        const token = phenixStore.token;
        let path = this.wsPath;

        if (token) {
          path += `?token=${token}`;
        }

        let proto = window.location.protocol == 'https:' ? 'wss://' : 'ws://';
        return proto + window.location.host + path;
      },
      handleResize() {
        if (this.fit) {
          this.fit.fit();
        }
      },
    },
  };
</script>
