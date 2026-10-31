<!-- 
This is the footer included with all views based on the App.vue
component. 
-->

<template>
  <footer>
    <hr class="mb-4" />
    <div class="container is-fluid">
      <small>
        <p style="float: left; color: whitesmoke; padding-bottom: 16px">
          Copyright &copy; <b>2019-2026 Sandia National Laboratories</b>. All
          Rights Reserved.
        </p>
        <div class="app-footer__end">
          <nav class="app-footer__links" aria-label="Help">
            <a
              href="https://phenix.sceptre.dev/latest/"
              target="_blank"
              rel="noopener noreferrer"
              >Help<span class="is-sr-only"> (opens in new tab)</span></a
            >
            <router-link v-if="auth" :to="{ name: 'glossary' }"
              >Glossary</router-link
            >
          </nav>
          <p v-if="version">{{ version }}</p>
        </div>
      </small>
    </div>
  </footer>
</template>

<script>
  import { usePhenixStore } from '@/store.js';
  import { formatVersion } from '@/utils/version.js';

  export default {
    computed: {
      // The glossary is a page for signed-in users, as other pages are.
      auth() {
        return usePhenixStore().auth;
      },
    },
    async created() {
      try {
        let resp = await fetch(this.$router.resolve({ name: 'version' }).href);
        let version = await resp.json();

        this.version = formatVersion(version);
      } catch (err) {
        console.log(`ERROR getting version: ${err}`);
      }
    },

    data() {
      return {
        version: '',
      };
    },
  };
</script>

<style scoped>
  .app-footer__end {
    float: right;
    display: flex;
    flex-wrap: wrap;
    gap: 0 1.5em;
    color: whitesmoke;
  }

  .app-footer__links {
    display: flex;
    gap: 1em;
  }

  .app-footer__links a {
    text-decoration: underline;
  }
</style>
