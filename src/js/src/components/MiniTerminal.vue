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
        if (this.socket != null) {
          this.socket.close();
        }

        this.setupTerminal();
      },
    },

    beforeUnmount() {
      if (this.handleResize) {
        window.removeEventListener('resize', this.handleResize);
      }

      if (this.socket) {
        this.socket.close();
      }
    },

    mounted() {
      this.socket = new WebSocket(this.getWsUrl());

      const term = new Terminal();

      this.fit = new FitAddon();
      this.attach = new AttachAddon(this.socket);

      term.loadAddon(this.fit);
      term.loadAddon(this.attach);

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
    },
    methods: {
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
