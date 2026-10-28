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
          Copyright &copy; <b>2019-2026 Sandia National Laboratories</b>. All
          Rights Reserved.
        </p>
        <div class="app-footer__end">
          <p v-if="version">{{ version }}</p>
        </div>
      </small>
    </div>
  </footer>
</template>

<script>
  import { formatVersion } from '@/utils/version.js';

  export default {
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
  .app-footer__end {
    float: right;
    display: flex;
    flex-wrap: wrap;
    gap: 0 1.5em;
    color: whitesmoke;
  }
</style>
