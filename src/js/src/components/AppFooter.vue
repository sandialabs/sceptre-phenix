<!-- 
This is the footer included with all views based on the App.vue
component. 
-->

<template>
  <div>
    <hr class="mb-4" />
    <div class="container is-fluid">
      <small>
        <p class="copyright">
          Copyright &copy; <b>2019-2025 Sandia National Laboratories</b>. All
          Rights Reserved.
        </p>
        <p class="version">{{ version }}</p>
      </small>
    </div>
  </div>
</template>

<script>
  export default {
    async created() {
      try {
        let resp = await fetch(this.$router.resolve({ name: 'version' }).href);
        let version = await resp.json();

        this.version = `Version ${version.commit} (built on ${version.buildDate})`;
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
  .copyright {
    float: left;
    padding-bottom: 16px;
    color: var(--text-primary);
  }

  .version {
    float: right;
    color: var(--text-primary);
  }
</style>
