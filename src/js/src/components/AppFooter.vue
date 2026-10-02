<!-- 
This is the footer included with all views based on the App.vue
component. 
-->

<template>
  <footer class="app-footer">
    <hr class="mb-4" />
    <div class="container is-fluid">
      <small>
        <p style="float: left; color: whitesmoke; padding-bottom: 16px">
          <b-tooltip label="phēnix documentation" type="is-light is-right">
            <a
              class="docs-link"
              :href="DOCS_URL"
              target="_blank"
              rel="noopener"
              aria-label="phēnix documentation">
              <b-icon icon="book" />
            </a>
          </b-tooltip>
          Copyright &copy; <b>2019-2026 Sandia National Laboratories</b>. All
          Rights Reserved.
        </p>
        <p style="float: right; color: whitesmoke">{{ version }}</p>
      </small>
    </div>
  </footer>
</template>

<script>
  import { formatVersion } from '@/utils/version.js';
  import { DOCS_URL } from '@/utils/docs.js';

  export default {
    setup() {
      return { DOCS_URL };
    },
    async created() {
      try {
        let resp = await fetch(this.$router.resolve({ name: 'version' }).href);
        let version = await resp.json();

        this.version = formatVersion(version);
      } catch (err) {
        console.warn('failed to get the phenix version', err);
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
  .docs-link {
    color: whitesmoke;
    margin-right: 0.5rem;
    vertical-align: middle;
  }

  .docs-link:hover {
    color: #ffffff;
  }
</style>
