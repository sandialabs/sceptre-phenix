<!-- 
The header component is available on all views based on the 
App.vue component. The available routable links are available 
based on whether the user is logged in and furthermore based on 
what their role allows.
 -->
<template>
  <b-navbar v-model="menuOpen" class="mb-4">
    <template #brand>
      <b-navbar-item tag="router-link" :to="{ name: 'home' }" :active="false">
        <img src="@/assets/imgs/phenix-banner.png" alt="phenix" />
      </b-navbar-item>
    </template>
    <template #start>
      <template v-for="(item, i) in navItems" :key="item.label">
        <span v-if="i" class="nav-dot" aria-hidden="true">·</span>
        <nav-menu
          v-if="item.links"
          :label="item.label"
          :icon="item.icon"
          :links="item.links"></nav-menu>
        <b-tooltip
          v-else
          :label="item.tip ?? item.label"
          type="is-light"
          position="is-bottom"
          class="nav-tip">
          <router-link
            :to="{ name: item.route }"
            class="navbar-item nav-link"
            :data-testid="item.testid">
            <b-icon
              :icon="item.icon"
              size="is-small"
              aria-hidden="true"></b-icon>
            <span class="nav-text">{{ item.label }}</span>
          </router-link>
        </b-tooltip>
      </template>
    </template>

    <template #end>
      <div class="navbar-item nav-tools">
        <b-tooltip
          v-if="auth && tunneler"
          label="Tunneler"
          type="is-light"
          position="is-bottom">
          <b-button
            tag="router-link"
            :to="{ name: 'tunneler' }"
            type="is-primary"
            icon-left="archway"
            class="nav-icon-button tunneler-button"
            ><span class="tunneler-label is-sr-only">Tunneler</span></b-button
          >
        </b-tooltip>
        <b-tooltip label="Docs" type="is-light" position="is-bottom">
          <a
            :href="DOCS_URL"
            target="_blank"
            rel="noopener noreferrer"
            class="button nav-icon-button docs-button"
            aria-label="Docs (opens in new tab)"
            ><b-icon icon="book" size="is-small" aria-hidden="true"></b-icon
          ></a>
        </b-tooltip>
        <span v-if="auth" class="nav-sep" aria-hidden="true"></span>
        <refresh-status v-if="auth"></refresh-status>
        <jobs-menu v-if="auth"></jobs-menu>
        <!-- a proxy gets Reauthorize, not Logout; while a logout waits for
             the server, it says so -->
        <template v-if="proxyAuth || auth">
          <span class="nav-sep" aria-hidden="true"></span>
          <!-- left of the button, which is at the window's right edge -->
          <b-tooltip :label="logoutTip" type="is-light" position="is-left">
            <button
              type="button"
              class="button nav-icon-button app-header__logout"
              :data-testid="proxyAuth ? null : 'nav-logout'"
              :aria-label="logoutLabel"
              :aria-busy="loggingOut ? 'true' : undefined"
              @click="logout">
              <b-icon
                :icon="proxyAuth ? 'user-shield' : 'right-from-bracket'"
                size="is-small"
                aria-hidden="true"></b-icon>
            </button>
          </b-tooltip>
        </template>
      </div>
    </template>
  </b-navbar>
</template>

<script>
  import { usePhenixStore } from '@/store.js';
  import { roleAllowed, usersPageAllowed } from '@/utils/rbac.js';
  import { canUseWebShark, webSharkInstalled } from '@/utils/webshark.js';
  import * as authApi from '@/api/auth.js';
  import { showError } from '@/utils/errorNotif.js';
  import { endConsole } from '@/utils/consoleSession.js';
  import RefreshStatus from '@/components/RefreshStatus.vue';
  import JobsMenu from '@/components/jobs/JobsMenu.vue';
  import NavMenu from '@/components/NavMenu.vue';
  import { DOCS_URL } from '@/utils/docs.js';

  export default {
    components: { RefreshStatus, JobsMenu, NavMenu },
    setup() {
      return { DOCS_URL };
    },
    // The burger menu's state, closed when a link in it is followed.
    data() {
      return { menuOpen: false, loggingOut: false };
    },
    // Links but Experiments wait for login; Tunneler and WebShark need the
    // server (WebShark also capture access); signed-in users get the jobs menu.
    computed: {
      auth() {
        const phenixStore = usePhenixStore();
        return phenixStore.auth;
      },
      proxyAuth() {
        return import.meta.env.VITE_AUTH === 'proxy';
      },

      tunneler() {
        return usePhenixStore().features.includes('tunneler-download');
      },

      webshark() {
        return webSharkInstalled(usePhenixStore().features) && canUseWebShark();
      },

      logoutLabel() {
        if (this.loggingOut) return 'Logging out…';
        return this.proxyAuth ? 'Reauthorize' : 'Logout';
      },

      // Reauthorize's tooltip says what it does: the proxy signs you in again
      logoutTip() {
        return this.proxyAuth && !this.loggingOut
          ? 'Reauthorize (reload your session and role)'
          : this.logoutLabel;
      },

      // The links and menus the role may open, in order. A menu shows the
      // links in it the role may open, and only if there is one.
      navItems() {
        const auth = this.auth;
        const may = (resource, verb) => auth && roleAllowed(resource, verb);
        const menu = (label, icon, links) => ({
          label,
          icon,
          links: links.filter((link) => link.show),
        });
        return [
          {
            route: 'experiments',
            label: 'Experiments',
            icon: 'flask',
            show: roleAllowed('experiments', 'list'),
          },
          {
            route: 'builder',
            label: 'Builder',
            icon: 'diagram-project',
            show: may('builder', 'use'),
            testid: 'nav-builder',
          },
          {
            route: 'scorch',
            label: 'SCORCH',
            icon: 'fire',
            show: may('experiments', 'list'),
          },
          {
            route: 'sohoverview',
            label: 'SoH',
            tip: 'State of Health',
            icon: 'heart-pulse',
            show: may('experiments', 'list'),
          },
          {
            route: 'webshark',
            label: 'WebShark',
            icon: 'shark-fin',
            show: auth && this.webshark,
          },
          menu('Resources', 'cubes', [
            {
              route: 'configs',
              label: 'Configs',
              icon: 'file-code',
              show: may('configs', 'list'),
            },
            {
              route: 'disks',
              label: 'Disks',
              icon: 'hard-drive',
              show: may('disks', 'list'),
            },
            {
              route: 'hosts',
              label: 'Hosts',
              icon: 'server',
              show: may('hosts', 'list'),
            },
            {
              route: 'vlans',
              label: 'VLANs',
              icon: 'network-wired',
              tip: 'VLAN IDs in use, aliases and ranges for all experiments',
              show: may('vlans', 'list'),
            },
          ]),
          menu('Admin', 'screwdriver-wrench', [
            {
              route: 'users',
              label: 'Users',
              icon: 'users',
              show: auth && usersPageAllowed(),
            },
            {
              route: 'log',
              label: 'Logs',
              icon: 'file-lines',
              show: may('logs', 'get'),
            },
            {
              route: 'settings',
              label: 'Settings',
              icon: 'gear',
              show: may('settings', 'update'),
            },
            {
              route: 'console',
              label: 'Console',
              icon: 'terminal',
              show: may('miniconsole', 'post'),
            },
          ]),
        ].filter((item) => (item.links ? item.links.length : item.show));
      },
    },

    watch: {
      // Buefy closes the burger menu only on a click on its own items.
      '$route.fullPath'() {
        this.menuOpen = false;
      },
    },

    methods: {
      async logout(event) {
        const button = event?.currentTarget;

        if (this.loggingOut) {
          return;
        }

        this.loggingOut = true;

        // end the console first, while the token still allows it
        await endConsole();

        authApi
          .logOut()
          .then(
            (response) => response.status,
            (error) => error.response?.status,
          )
          .then((status) => {
            this.loggingOut = false;

            // The server no longer knows an expired token, and replies 401.
            if (status === 204 || status === 401) {
              usePhenixStore().logout();

              if (this.proxyAuth) {
                this.$buefy.toast.open({
                  message: 'Your account has been reauthorized',
                  type: 'is-success',
                });
              }

              return;
            }

            // When the server does not answer the logout, the session stays
            // as it was and an error says so. Focus goes back to Logout.
            showError(
              'Could not log out. Check your connection and try again.',
            );
            button?.focus();

            // In a narrow window, the menu that held Logout closes on a
            // click. Focus goes back to the button that opens the menu.
            if (!button?.getClientRects().length) {
              this.$el.querySelector('.navbar-burger')?.focus();
            }
          });
      },
    },
  };
</script>

<style scoped>
  /* flex rows in the burger menu too, where Bulma makes them blocks */
  .nav-link {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding-inline: 0.5rem;
  }

  .nav-dot {
    display: flex;
    align-items: center;
    padding-inline: 0.1rem;
    color: #e6e6e6;
  }

  .navbar-item.nav-tools {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
  }

  .nav-sep {
    align-self: stretch;
    width: 1px;
    margin-block: 0.25rem;
    background-color: #8a8a8a;
  }

  /* darker than the primary fill (#9a4420); white is 7.6:1 on it */
  .tunneler-button.button.is-primary:hover,
  .tunneler-button.button.is-primary.router-link-exact-active {
    background-color: #8a3d1c;
  }

  .tunneler-button.router-link-exact-active {
    box-shadow: inset 0 -3px 0 whitesmoke;
  }

  /* In the burger menu, the links show their text, without the dots. */
  @media (max-width: 1023px) {
    .nav-dot {
      display: none;
    }
  }
</style>
