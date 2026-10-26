<template>
  <div class="xterm" ref="xterm"></div>
</template>

<script>
  import { markRaw } from 'vue';

  import { usePhenixStore } from '@/store.js';
  import {
    serverShuttingDown,
    shutdownWithin,
    wsCloseCause,
  } from '@/utils/freshness.js';

  import { Terminal } from '@xterm/xterm';
  import { AttachAddon } from '@xterm/addon-attach';

  import '@xterm/xterm/css/xterm.css';

  export default {
    props: ['wsPath'],
    data() {
      return {
        socket: null,
        term: null,
        attach: null,
      };
    },
    watch: {
      wsPath: function () {
        this.teardownSocket();
        this.setupTerminal();
      },
    },

    beforeUnmount() {
      this.teardownSocket();
      if (this.term) {
        this.term.dispose();
        this.term = null;
      }
    },

    mounted() {
      // xterm objects are markRaw: deep reactive proxies around their
      // internals only add overhead to every write
      const term = markRaw(new Terminal());

      term.open(this.$refs.xterm);
      term.resize(80, 30);

      this.term = term;
      this.setupTerminal();
    },
    methods: {
      setupTerminal() {
        const socket = markRaw(new WebSocket(this.getWsUrl()));
        this.socket = socket;

        // the attach addon throws on input if it is loaded before the
        // socket is open, so wait for onopen to wire it up
        this.socket.onopen = () => {
          this.attach = markRaw(new AttachAddon(this.socket));
          this.term.loadAddon(this.attach);
        };

        this.socket.onerror = () => {
          this.term.write('\r\n[terminal connection error]\r\n');
        };

        this.socket.onclose = async (event) => {
          // a dropped socket may be the first sign of a shutdown
          const shutdown =
            wsCloseCause(event) === 'shutdown' ||
            (event.code === 1000
              ? serverShuttingDown()
              : await shutdownWithin());
          // the terminal may have closed or moved on while it waited
          if (!this.term || this.socket !== socket) return;
          this.term.write(
            shutdown
              ? '\r\n[phēnix server shutting down; terminal closed]\r\n'
              : '\r\n[terminal connection closed]\r\n',
          );
        };
      },
      teardownSocket() {
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
    },
  };
</script>
