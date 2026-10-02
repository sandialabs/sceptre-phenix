<template>
  <div>
    <section v-if="!session && message" class="hero is-light is-bold is-large">
      <div class="hero-body">
        <div class="container" style="text-align: center">
          <h1 class="title">{{ message }}</h1>
        </div>
      </div>
    </section>
    <div ref="host"></div>
    <div v-if="state === 'ended' || state === 'lost'" class="mt-3">
      <b-button type="is-primary" @click="restart">
        {{ state === 'ended' ? 'Start a new console' : 'Reconnect' }}
      </b-button>
    </div>
  </div>
</template>

<script>
  import { debounce } from 'lodash-es';

  import { currentConsole } from '@/utils/consoleSession.js';
  import { consolePhase, openConsole } from '@/utils/consoleTerminal.js';

  // The console session outlives this page (see consoleSession.js): leaving
  // the page only takes its terminal out of the page, and coming back puts
  // the same terminal, with its output, back in.
  export default {
    data() {
      return {
        session: null,
        state: null,
        error: null,
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
    },

    created() {
      this.handleResize = debounce(() => this.session?.fit(), 100);
    },

    mounted() {
      window.addEventListener('resize', this.handleResize);

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
      this.release();
    },

    methods: {
      async open() {
        this.error = null;

        try {
          const session = await openConsole();
          // left while it opened: the session waits for the next visit
          if (!this.left) {
            this.show(session);
          }
        } catch (err) {
          if (this.left) {
            return;
          }

          switch (err.response?.status) {
            // the server was started without --minimega-console
            case 405:
              this.error = 'Console access is not configured.';
              break;
            case 403:
              this.error = 'You do not have access to the console.';
              break;
            default:
              this.error = 'Could not start the console.';
              console.warn('failed to start the console', err);
          }
        }
      },

      show(session) {
        this.session = session;
        this.state = session.state;
        this.stopWatching = session.onChange((state) => {
          this.state = state;
        });
        session.mount(this.$refs.host);
      },

      release() {
        this.stopWatching?.();
        this.stopWatching = null;
        this.session?.unmount();
        this.session = null;
        this.state = null;
      },

      restart() {
        this.release();
        this.open();
      },
    },
  };
</script>
