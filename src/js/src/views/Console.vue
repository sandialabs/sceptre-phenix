<template>
  <div>
    <template v-if="pid == 0">
      <section class="hero is-light is-bold is-large">
        <div class="hero-body">
          <div class="container" style="text-align: center">
            <h1 class="title">Console access is not configured.</h1>
          </div>
        </div>
      </section>
    </template>
    <template v-else>
      <Terminal :wsPath="terminalPath" :resizePath="resizePath" />
    </template>
  </div>
</template>

<script>
  import Terminal from '@/components/MiniTerminal.vue';
  import axiosInstance from '@/utils/axios.js';

  export default {
    components: {
      Terminal,
    },

    data() {
      return {
        pid: 0,
      };
    },

    computed: {
      terminalPath() {
        return this.$router.resolve({
          name: 'console-ws',
          params: { pid: this.pid },
        }).href;
      },

      resizePath() {
        return this.$router.resolve({
          name: 'console-size',
          params: { pid: this.pid },
        }).href;
      },
    },
    mounted() {
      axiosInstance
        .post('console')
        .then((resp) => {
          this.pid = resp.data.pid;
        })
        .catch((err) => {
          console.log(err.message);
          if (err.response) {
            console.log(err.response.data);
          }
        });
    },
  };
</script>
