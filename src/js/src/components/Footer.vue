<!-- 
This is the footer included with all views based on the App.vue
component. 
-->

<template>
  <div>
    <hr>
    <div>
      <small>
        <p style="float: left; color: whitesmoke">Copyright &copy; <b>2019-2022 Sandia National Laboratories</b>. All Rights Reserved.</p>
        <p style="float: right; color: whitesmoke">{{ version }}</p>
      </small>
    </div>
  </div>
</template>

<script>
  export default {
    async created () {
      try {
        let URL = '/version';

        if ( process.env.BASE_URL !== '/' ) {
          URL = `${process.env.BASE_URL}/version`;
        }

        let resp    = await fetch(URL);
        let version = await resp.json();

        this.version = `Version ${version.commit} (built on ${version.buildDate})`
      } catch (err) {
        console.log(`ERROR getting version: ${err}`);
      }
    },

    data () {
      return {
        version: ''
      }
    }
  }
</script>
