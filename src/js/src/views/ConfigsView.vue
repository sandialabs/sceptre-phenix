<template>
  <ConfigsEditor
    v-if="editorActive"
    :mode="editorMode"
    :editorConfig="editorConfig"
    @is-done="handleDone" />
  <ConfigsList v-else @edit="handleEdit" @create="handleCreate" />
</template>
<script>
  import ConfigsList from '@/components/configs/ConfigsList.vue';
  import ConfigsEditor from '@/components/configs/ConfigsEditor.vue';
  import axiosInstance from '@/utils/axios.js';

  export default {
    components: { ConfigsList, ConfigsEditor },
    data() {
      return {
        isWaiting: false,
        editorActive: false,
        editorMode: null,
        editorConfig: null,
      };
    },
    created() {},
    methods: {
      handleDone(msg) {
        if (msg !== '') {
          this.$buefy.toast.open({
            message: msg,
            type: 'is-success',
            duration: 3000,
          });
        }
        this.editorActive = false;
      },
      handleEdit(config) {
        this.editorActive = true;
        this.editorMode = 'edit';
        this.editorConfig = config;
      },
      handleCreate() {
        this.editorActive = true;
        this.editorMode = 'create';
        this.editorConfig = null;
      },
    },
  };
</script>
