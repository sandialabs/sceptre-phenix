<template>
  <div class="xterm" ref="xterm"></div>
</template>

<script>
  import axiosInstance from '@/utils/axios.js';
  import { usePhenixStore } from '@/store.js';

  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import { AttachAddon } from '@xterm/addon-attach';

  import '@xterm/xterm/css/xterm.css';

  export default {
    props: ['wsPath', 'resizePath'],
    data() {
      return {
        socket: null,
        term: null,
        fit: null,
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
      window.removeEventListener('resize', this.handleResize);
      this.teardownSocket();
    },

    mounted() {
      const term = new Terminal();

      this.fit = new FitAddon();
      term.loadAddon(this.fit);

      term.open(this.$refs.xterm);

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
