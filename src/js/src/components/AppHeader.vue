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
      <b-navbar-item
        v-if="roleAllowed('experiments', 'list')"
        tag="router-link"
        :to="{ name: 'experiments' }"
        >Experiments</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && roleAllowed('configs', 'list')"
        tag="router-link"
        :to="{ name: 'configs' }"
        >Configs</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && roleAllowed('disks', 'list')"
        tag="router-link"
        :to="{ name: 'disks' }"
        >Disks</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && roleAllowed('hosts', 'list')"
        tag="router-link"
        :to="{ name: 'hosts' }"
        >Hosts</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && usersPageAllowed()"
        tag="router-link"
        :to="{ name: 'users' }"
        >Users</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && roleAllowed('logs', 'get')"
        tag="router-link"
        :to="{ name: 'log' }"
        >Logs</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && roleAllowed('experiments', 'list')"
        tag="router-link"
        :to="{ name: 'scorch' }"
        >Scorch</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && roleAllowed('experiments', 'list')"
        tag="router-link"
        :to="{ name: 'sohoverview' }"
        >State of Health</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && roleAllowed('miniconsole', 'post')"
        tag="router-link"
        :to="{ name: 'console' }"
        >Console</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && webshark"
        tag="router-link"
        :to="{ name: 'webshark' }"
        >WebShark</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && roleAllowed('settings', 'update')"
        tag="router-link"
        :to="{ name: 'settings' }"
        >Settings</b-navbar-item
      >
    </template>

    <template #end>
      <b-navbar-item v-if="auth && tunneler" tag="div" class="tunneler-item">
        <b-button
          tag="router-link"
          :to="{ name: 'tunneler' }"
          type="is-primary"
          size="is-medium"
          icon-left="download"
          class="tunneler-button"
          @click="menuOpen = false"
          >Tunneler</b-button
        >
      </b-navbar-item>
      <b-navbar-item v-if="auth" tag="div">
        <refresh-status></refresh-status>
      </b-navbar-item>
      <b-navbar-item v-if="auth" tag="div">
        <jobs-menu></jobs-menu>
      </b-navbar-item>
      <!-- Buttons, so the keyboard reaches them too. While a logout waits
           for the server, they say so. -->
      <b-navbar-item
        v-if="proxyAuth"
        tag="button"
        type="button"
        class="navbar-item app-header__logout"
        :aria-busy="loggingOut ? 'true' : undefined"
        @click="logout"
        >{{ loggingOut ? 'Logging out…' : 'Reauthorize' }}
      </b-navbar-item>
      <b-navbar-item
        v-else-if="auth"
        tag="button"
        type="button"
        class="navbar-item app-header__logout"
        data-testid="nav-logout"
        :aria-busy="loggingOut ? 'true' : undefined"
        @click="logout"
        >{{ loggingOut ? 'Logging out…' : 'Logout' }}
      </b-navbar-item>
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

  export default {
    components: { RefreshStatus, JobsMenu },
    setup() {
      return { roleAllowed, usersPageAllowed };
    },
    // The burger menu's state: Buefy leaves it open after a click on a button's
    // label, so the Tunneler button closes it itself.
    data() {
      return { menuOpen: false, loggingOut: false };
    },
    // Links but Experiments wait for login; a proxy gets Reauthorize, not Logout; Tunneler and WebShark need
    // the server (WebShark also capture access); signed-in users get the jobs menu.
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
  /* The logout items are buttons that look like the links beside them. */
  .app-header__logout {
    background: transparent;
    border: 0;
    color: inherit;
    cursor: pointer;
    font: inherit;
    text-align: start;
  }

  .app-header__logout:hover,
  .app-header__logout:focus-visible {
    background-color: rgba(255, 255, 255, 0.12);
  }

  /* In the collapsed menu, the row is the button, as it is for a link. */
  @media (max-width: 1023px) {
    .app-header__logout {
      width: 100%;
    }
  }

  .tunneler-item {
    padding-block: 0.25rem;
  }

  /* darker than $primary so white text keeps 5:1 (AA) */
  .tunneler-button.button.is-primary {
    background-color: #b5501f;
  }

  .tunneler-button.button.is-primary:hover,
  .tunneler-button.button.is-primary.router-link-exact-active {
    background-color: #9a4420;
  }

  .tunneler-button.router-link-exact-active {
    box-shadow: inset 0 -3px 0 whitesmoke;
  }
</style>
