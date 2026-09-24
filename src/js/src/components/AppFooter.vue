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
        <p style="float: right; color: whitesmoke">{{ version }}</p>
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
