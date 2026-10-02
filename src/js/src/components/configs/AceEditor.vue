<template>
  <div ref="editor" class="editor"></div>
  <b-loading :is-full-page="false" v-model="isLoading"></b-loading>
</template>

<script>
  import { loadAce } from '@/utils/loadAce.js';

  export default {
    props: {
      value: {
        type: String,
        default: '',
        required: true,
      },
      lang: {
        type: String,
        default: 'json',
      },
      vim: {
        type: Boolean,
        default: false,
      },
    },
    emits: ['update:value', 'save', 'reset'],
    data() {
      return {
        editor: null,
        ace: null,
        isLoading: false,
      };
    },
    async mounted() {
      this.isLoading = true;

      this.ace = await loadAce();
      // the editor was closed while Ace loaded
      if (!this.$refs.editor) return;
      this.ace.require('ace/ext/language_tools');

      this.editor = this.ace.edit(this.$refs.editor, {
        theme: 'ace/theme/dracula',
        mode: 'ace/mode/' + this.lang,
        useWorker: false,
        tabSize: 2,
      });
      if (this.vim) {
        this.editor.setKeyboardHandler('ace/keyboard/vim');
      }

      this.editor.setValue(this.value, 1); // Initialize with `value` prop

      // Emit input event to update v-model binding
      this.editor.getSession().on('change', () => {
        this.$emit('update:value', this.editor.getValue());
      });

      this.loadVimCommands();
      this.isLoading = false;
    },
    watch: {
      value(newValue) {
        if (this.editor && this.editor.getValue() !== newValue) {
          this.editor.setValue(newValue, 1); // 1 is to move cursor to the start
        }
      },
      lang(newLang) {
        if (this.editor) {
          this.editor.session.setMode(`ace/mode/${newLang}`);
        }
      },
      vim(vimActive) {
        if (!this.editor) {
          return;
        }
        if (vimActive) {
          this.editor.setKeyboardHandler('ace/keyboard/vim');
        } else {
          this.editor.setKeyboardHandler('');
        }
      },
    },
    beforeUnmount() {
      this.editor?.destroy();
    },
    methods: {
      loadVimCommands() {
        // loadAce bundles the vim keybinding, so it is already registered
        const VimApi = this.ace.require('ace/keyboard/vim').CodeMirror.Vim;

        VimApi.defineEx('wq', null, () => {
          this.$emit('save');
        });

        VimApi.defineEx('q', null, () => {
          this.$emit('reset');
        });
      },
    },
  };
</script>

<style scoped>
  .editor {
    height: 500px;
    width: 100%;
  }
</style>
