<!--
A header menu: a button that opens a group of page links. It is a
disclosure, as the jobs menu is, not an ARIA menu.
-->
<template>
  <div
    ref="root"
    class="nav-menu"
    :class="{ 'is-active': open, 'is-current': current }"
    @keydown.esc="close(true)"
    @focusout="closeOnFocusOut">
    <b-tooltip
      :label="label"
      :active="!open"
      type="is-light"
      position="is-bottom"
      class="nav-tip">
      <button
        ref="trigger"
        type="button"
        class="navbar-item nav-menu-button"
        :aria-expanded="open ? 'true' : 'false'"
        :aria-controls="open ? panelId : null"
        @click="open = !open">
        <b-icon :icon="icon" size="is-small" aria-hidden="true"></b-icon>
        <span class="nav-text">{{ label }}</span>
        <b-icon
          :icon="open ? 'chevron-up' : 'chevron-down'"
          size="is-small"
          class="nav-menu-chevron"
          aria-hidden="true"></b-icon>
      </button>
    </b-tooltip>
    <ul v-if="open" :id="panelId" class="nav-menu-panel">
      <li v-for="link in links" :key="link.route">
        <router-link
          :to="{ name: link.route }"
          class="nav-menu-link"
          @click="close(false)">
          <b-icon :icon="link.icon" size="is-small" aria-hidden="true"></b-icon>
          <span>{{ link.label }}</span>
        </router-link>
      </li>
    </ul>
  </div>
</template>

<script>
  export default {
    props: {
      // the button's text and tooltip
      label: { type: String, required: true },
      icon: { type: String, required: true },
      // the links it holds, each { route, label, icon }
      links: { type: Array, required: true },
    },
    data() {
      return { open: false };
    },
    computed: {
      panelId() {
        return `nav-${this.label.toLowerCase()}-panel`;
      },
      // whether the current page is one of its links
      current() {
        const name = this.$route?.name;
        return this.links.some((link) => link.route === name);
      },
    },
    watch: {
      '$route.name'() {
        this.open = false;
      },
    },
    mounted() {
      document.addEventListener('click', this.closeOutside, true);
    },
    beforeUnmount() {
      document.removeEventListener('click', this.closeOutside, true);
    },
    methods: {
      // Closes the panel, and with refocus puts focus back on the button.
      close(refocus) {
        if (!this.open) return;
        this.open = false;
        if (refocus) this.$refs.trigger?.focus();
      },
      // Tabbing out of it closes it, as a click outside does.
      closeOnFocusOut(event) {
        const to = event.relatedTarget;
        if (to && !this.$refs.root?.contains(to)) this.close(false);
      },
      closeOutside(event) {
        if (this.open && !this.$refs.root?.contains(event.target)) {
          this.close(false);
        }
      },
    },
  };
</script>

<style scoped>
  .nav-menu {
    display: flex;
    align-items: stretch;
    position: relative;
  }

  /* a button that looks like the links beside it */
  .nav-menu-button {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding-inline: 0.5rem;
    background: transparent;
    border: 0;
    color: inherit;
    cursor: pointer;
    font: inherit;
  }

  .nav-menu-button:hover,
  .nav-menu-button:focus-visible,
  .nav-menu.is-active .nav-menu-button,
  .nav-menu.is-current .nav-menu-button {
    background-color: #575757;
  }

  .nav-menu-chevron {
    width: 0.75rem;
  }

  .nav-menu-panel {
    position: absolute;
    top: 100%;
    left: 0;
    z-index: 31;
    min-width: 12rem;
    padding-block: 0.375rem;
    columns: auto;
    border-radius: 0 0 6px 6px;
    background-color: #575757;
    box-shadow: 0 0.5em 1em -0.125em rgba(0, 0, 0, 0.5);
  }

  .nav-menu-link {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 1rem;
    color: white;
  }

  /* the current page: a bar and bold text, not color alone */
  .nav-menu-link.router-link-exact-active {
    box-shadow: inset 4px 0 0 white;
    font-weight: 600;
  }

  .nav-menu-link:hover,
  .nav-menu-link:focus-visible {
    background-color: #333;
    color: white;
  }

  /* inside the panel, which would cut off a ring drawn outside the link */
  .nav-menu-link:focus-visible {
    outline-offset: -3px;
    box-shadow: inset 0 0 0 5px #1a1a1a;
  }

  /* In the burger menu, the button is a full row and the links open below it. */
  @media (max-width: 1023px) {
    .nav-menu {
      display: block;
    }

    .nav-menu-button {
      width: 100%;
    }

    .nav-menu-panel {
      position: static;
      min-width: 0;
      border-radius: 0;
      box-shadow: none;
    }

    .nav-menu-link {
      padding-inline-start: 2rem;
    }
  }
</style>
