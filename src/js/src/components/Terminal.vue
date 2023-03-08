<template>
  <div class="xterm" ref="xterm"></div>
</template>

<script>
  import 'xterm/dist/xterm.css'
  import { Terminal } from 'xterm'

  import * as attach from 'xterm/dist/addons/attach/attach.js'
  import * as fit    from 'xterm/dist/addons/fit/fit.js'

  Terminal.applyAddon( attach )
  Terminal.applyAddon( fit )

  export default {
    props: [
      'wsPath',
      'resizePath',
    ],

    watch: {
      wsPath: function() {
        if (this.socket != null) {
          this.socket.close();
        }

        this.setupTerminal()
      }
    },

    data () {
      return {
        socket: null,
        term:   null
      }
    },

    beforeDestroy () {
      this.socket.close();
    },

    mounted () {
      this.term = new Terminal();

      this.term.open(this.$refs.xterm);
      this.setupTerminal()

      if (this.resizePath === undefined) {
        this.term.resize(80, 30);
      } else {
        this.term.on('resize', (size) => {
          var url = this.resizePath + '?cols=' + size.cols + '&rows=' + size.rows;

          try {
            this.$http.post(url);
          } catch(e) {
            console.log(e);
          }
        });

        this.term.fit();

        window.addEventListener('resize', () => {
          this.term.fit();
        });
      }
    },

    methods: {
      setupTerminal () {
        let path = this.wsPath;

        if (this.$store.getters.token) {
          path += `?token=${this.$store.getters.token}`;
        }

        let proto = window.location.protocol == "https:" ? "wss://" : "ws://";
        let url   = proto + window.location.host + path;

        console.log(url);

        this.socket = new WebSocket(url);
        this.socket.onopen = this.runTerminal;
      },

      runTerminal () {
        this.term.attach(this.socket);
        this.term._initialized = true;

        if (this.resizePath !== undefined) {
          this.term.fit();
        }
      }
    }
  }
</script>
