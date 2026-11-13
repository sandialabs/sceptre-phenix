<!-- Finds text in the console's output: Enter finds the next match,
     Shift+Enter the one before, and Escape closes the search. -->
<template>
  <div class="console-search" role="search" aria-label="Search the output">
    <input
      ref="input"
      v-model="text"
      class="input is-small"
      type="search"
      aria-label="Text to find in the console output"
      placeholder="Find in the output"
      @input="search({ incremental: true })"
      @keydown.enter.exact.prevent="search()"
      @keydown.shift.enter.prevent="search({ backwards: true })"
      @keydown.esc.prevent="$emit('close')" />
    <b-button
      size="is-small"
      icon-left="chevron-up"
      aria-label="Find the previous match"
      :disabled="!text"
      @click="search({ backwards: true })" />
    <b-button
      size="is-small"
      icon-left="chevron-down"
      aria-label="Find the next match"
      :disabled="!text"
      @click="search()" />
    <b-checkbox
      v-model="caseSensitive"
      size="is-small"
      @update:model-value="search({ incremental: true })">
      Match case
    </b-checkbox>
    <span class="console-search-result" role="status" aria-live="polite">
      {{ result }}
    </span>
    <b-button
      size="is-small"
      icon-left="times-circle"
      aria-label="Close the search"
      @click="$emit('close')" />
  </div>
</template>

<script>
  export default {
    name: 'ConsoleSearch',
    props: {
      // find(text, { backwards, caseSensitive, incremental }) reports whether
      // it found the text
      find: { type: Function, required: true },
    },
    emits: ['clear', 'close'],
    data() {
      return { text: '', caseSensitive: false, result: '' };
    },
    mounted() {
      this.focus();
    },
    methods: {
      focus() {
        this.$refs.input.focus();
        this.$refs.input.select();
      },
      search(options = {}) {
        if (!this.text) {
          this.result = '';
          // the text was erased: its match is highlighted no more
          if (options.incremental) this.$emit('clear');
          return;
        }
        const found = this.find(this.text, {
          backwards: false,
          incremental: false,
          ...options,
          caseSensitive: this.caseSensitive,
        });
        this.result = found ? '' : 'No match';
      },
    },
  };
</script>

<style scoped>
  .console-search {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
  }

  .console-search .input {
    width: 18rem;
  }

  .console-search-result:empty {
    display: none;
  }
</style>
