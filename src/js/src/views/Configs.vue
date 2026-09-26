<template>
  <ConfigsList
    v-if="!editorActive"
    :focus-config="returnTo"
    @edit="handleEdit"
    @create="handleCreate" />
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
        // The config whose edit button focus returns to when the editor
        // closes (see ConfigsList's focusConfig).
        returnTo: '',
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
        this.returnTo = `${config.kind}/${config.metadata.name}`;
        this.editorActive = true;
        this.editorMode = 'edit';
        this.editorConfig = config;
      },
      handleCreate() {
        this.returnTo = '';
        this.editorActive = true;
        this.editorMode = 'create';
        this.editorConfig = null;
      },
    },
  };
</script>
