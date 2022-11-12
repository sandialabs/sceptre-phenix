<template>
  <div class="xterm" ref="xterm"></div>
</template>

<script>
  import 'xterm/dist/xterm.css'
  import { Terminal } from 'xterm'

  import * as attach from 'xterm/dist/addons/attach/attach.js'

  Terminal.applyAddon(attach)

  export default {
    props: [
      'loc'
    ],

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
      this.term.resize(80, 40);

      if (this.$store.getters.token) {
        this.loc += `?token=${this.$store.getters.token}`;
      }

      let proto = window.location.protocol == "https:" ? "wss://" : "ws://";
      let url   = proto + window.location.host + this.loc;

      this.socket = new WebSocket(url);
      this.socket.onopen = this.runTerminal;
    },

    methods: {
      runTerminal () {
        this.term.attach(this.socket);
        this.term._initialized = true;
      }
    }
  }
</script>
