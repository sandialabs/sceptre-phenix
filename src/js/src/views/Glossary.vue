<!--
The glossary defines the terms and abbreviations that the UI shows (WCAG
3.1.3, 3.1.4). Other pages can link to a term, as in /glossary#vlan.
-->
<template>
  <div class="content glossary">
    <p>
      These are the terms and abbreviations that phēnix uses. For more
      information, see the
      <a
        href="https://phenix.sceptre.dev/latest/"
        target="_blank"
        rel="noopener noreferrer"
        >phēnix documentation<span class="is-sr-only">
          (opens in new tab)</span
        ></a
      >.
    </p>
    <dl>
      <div v-for="entry in glossary" :key="entry.id" class="glossary__entry">
        <dt :id="entry.id">
          <dfn v-if="entry.expansion"
            ><abbr :title="entry.expansion">{{ entry.term }}</abbr></dfn
          >
          <dfn v-else>{{ entry.term }}</dfn>
          <span v-if="entry.expansion"> ({{ entry.expansion }})</span>
        </dt>
        <dd>{{ entry.definition }}</dd>
      </div>
    </dl>
  </div>
</template>

<script>
  import { glossary } from '@/utils/glossary.js';

  export default {
    setup() {
      return { glossary };
    },
  };
</script>

<style scoped>
  .glossary {
    max-width: 80ch;
  }

  /* The underline marks the link in the text, apart from its color. */
  .glossary a {
    text-decoration: underline;
  }

  .glossary dl {
    margin: 0;
  }

  .glossary__entry {
    margin-bottom: 1em;
  }

  .glossary dt {
    font-weight: bold;
    scroll-margin-top: 1em;
  }

  .glossary dfn {
    font-style: normal;
  }

  .glossary dd {
    margin-left: 0;
  }

  /* The term that a link such as /glossary#vlan opened. */
  .glossary dt:target {
    outline: 3px solid #ffd166;
    outline-offset: 2px;
  }
</style>
