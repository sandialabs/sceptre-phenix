<template>
  <ConfigsList v-if="!editorActive" @edit="handleEdit" @create="handleCreate" />
  <component
    ref="editor"
    :is="editorComponent"
    v-else
    :mode="editorMode"
    :editorConfig="editorConfig"
    @is-done="handleDone" />
</template>
<script>
  import ConfigsList from '@/components/configs/ConfigsList.vue';

  export default {
    components: {
      ConfigsList,
      // ConfigsEditor
    },
    data() {
      return {
        isWaiting: false,
        editorActive: false,
        editorMode: null,
        editorConfig: null,

        editorComponent: null,
      };
    },
    async beforeRouteLeave() {
      if (this.editorActive) {
        return this.$refs.editor.confirmResetEditor();
      } else {
        return true;
      }
    },
    async mounted() {
      this.editorComponent = (
        await import('@/components/configs/ConfigsEditor.vue')
      ).default;
    },
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
