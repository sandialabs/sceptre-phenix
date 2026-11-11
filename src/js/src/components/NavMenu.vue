<!--
A header menu: a button that opens a group of page links. It is a
disclosure, as the jobs menu is, not an ARIA menu. With a mouse on a wide
screen it also opens on hover.
-->
<template>
  <div
    ref="root"
    class="nav-menu"
    :class="{ 'is-active': open, 'is-current': current }"
    @keydown.esc="close(true)"
    @focusout="closeOnFocusOut"
    @mouseenter="hoverOpen"
    @mouseleave="hoverClose">
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
        @click="toggle">
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
        <b-tooltip
          v-if="link.tip"
          :label="link.tip"
          type="is-light"
          position="is-right"
          class="nav-menu-tip">
          <router-link
            v-prefetch-route="{ name: link.route }"
            :to="{ name: link.route }"
            class="nav-menu-link"
            @click="close(false)">
            <b-icon
              :icon="link.icon"
              size="is-small"
              aria-hidden="true"></b-icon>
            <span>{{ link.label }}</span>
          </router-link>
        </b-tooltip>
        <router-link
          v-else
          v-prefetch-route="{ name: link.route }"
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
  import { vPrefetchRoute } from '@/utils/prefetch.js';

  // the wait before a menu opened by hover closes, so a pointer can cross a gap
  const LEAVE_MS = 150;
  // the burger menu starts below this width, and it opens on click only
  const HOVER_QUERY = '(hover: hover) and (min-width: 1024px)';

  export default {
    directives: { prefetchRoute: vPrefetchRoute },
    props: {
      // the button's text and tooltip
      label: { type: String, required: true },
      icon: { type: String, required: true },
      // the links it holds, each { route, label, icon, tip? }
      links: { type: Array, required: true },
    },
    data() {
      // hovered: it is open because of hover, not because of a click
      return { open: false, hovered: false };
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
        this.hovered = false;
      },
    },
    mounted() {
      document.addEventListener('click', this.closeOutside, true);
    },
    beforeUnmount() {
      document.removeEventListener('click', this.closeOutside, true);
      clearTimeout(this.leaveTimer);
    },
    methods: {
      // Closes the panel, and with refocus puts focus back on the button.
      close(refocus) {
        clearTimeout(this.leaveTimer);
        this.hovered = false;
        if (!this.open) return;
        this.open = false;
        if (refocus) this.$refs.trigger?.focus();
      },
      // A click on a menu that hover opened keeps it open.
      toggle() {
        if (this.hovered) {
          clearTimeout(this.leaveTimer);
          this.hovered = false;
          return;
        }
        this.open = !this.open;
      },
      hoverOpen() {
        if (!window.matchMedia?.(HOVER_QUERY).matches) return;
        clearTimeout(this.leaveTimer);
        if (this.open) return;
        this.open = true;
        this.hovered = true;
      },
      // The pointer left. Focus inside keeps it open until focus leaves too.
      hoverClose() {
        if (!this.hovered) return;
        clearTimeout(this.leaveTimer);
        this.leaveTimer = setTimeout(() => {
          if (this.$refs.root?.contains(document.activeElement)) {
            this.hovered = false;
          } else {
            this.close(false);
          }
        }, LEAVE_MS);
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

  /* a link with a tooltip fills the row, as the others do */
  .nav-menu-tip,
  .nav-menu-tip :deep(.tooltip-trigger) {
    display: block;
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
