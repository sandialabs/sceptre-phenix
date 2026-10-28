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
  import { markRaw } from 'vue';
  import ConfigsList from '@/components/configs/ConfigsList.vue';

  export default {
    components: {
      ConfigsList,
    },
    data() {
      return {
        editorActive: false,
        editorMode: null,
        editorConfig: null,

        editorComponent: null,
        // The config whose edit button takes focus when the editor closes.
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
      // a component kept in data must not be made reactive
      this.editorComponent = markRaw(
        (await import('@/components/configs/ConfigsEditor.vue')).default,
      );
    },
    methods: {
      handleDone(msg, savedName = '') {
        if (savedName) {
          this.returnTo = savedName;
        }
        if (msg !== '') {
          this.$buefy.toast.open({
            message: msg,
            type: 'is-success',
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
