<!-- 
The header component is available on all views based on the 
App.vue component. The available routable links are available 
based on whether the user is logged in and furthermore based on 
their role. Based on the current limitations per user role, these 
are only available to Global Administrator or Global Viewer.
 -->
<template>
  <b-navbar class="mb-4">
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
        v-if="auth && !disabled"
        tag="router-link"
        :to="{ name: 'users' }"
        >Users</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && roleAllowed('logs', 'list')"
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
        tag="a"
        :href="builderLoc()"
        target="_blank"
        class="navbar-item"
        >Builder</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && builderBeta && roleAllowed('configs', 'list')"
        tag="router-link"
        :to="{ name: 'builder-beta' }"
        data-testid="nav-builder-beta"
        >Builder Flow
        <!-- The tag is part of the link's name: "Builder Flow beta". -->
        <b-tag class="ml-1" type="is-info is-light">beta</b-tag>
      </b-navbar-item>
      <b-navbar-item
        v-if="auth && roleAllowed('miniconsole', 'post')"
        tag="router-link"
        :to="{ name: 'console' }"
        >Console</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && tunneler"
        tag="router-link"
        :to="{ name: 'tunneler' }">
        Tunneler
      </b-navbar-item>
      <b-navbar-item
        v-if="auth && roleAllowed('settings', 'edit')"
        tag="router-link"
        :to="{ name: 'settings' }"
        >Settings</b-navbar-item
      >
    </template>

    <template #end>
      <!-- Buttons, so the keyboard reaches them too; while a logout checks
           for Builder Flow changes the server does not have, they say so. -->
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
  import { roleAllowed } from '@/utils/rbac.js';
  import { BUILDER_BETA_FEATURE, isFeatureEnabled } from '@/utils/features.js';

  export default {
    setup() {
      return { roleAllowed };
    },
    //  The computed elements determine if the user is already logged
    //  in; if so, the routable links are available. If not, the sign
    //  in routable link is the only one available. The role getter
    //  determines what the role of the user is; this is used to present
    //  routable links in the header row.
    computed: {
      auth() {
        const phenixStore = usePhenixStore();
        return phenixStore.auth;
      },
      disabled() {
        const phenixStore = usePhenixStore();
        return phenixStore.role.name === 'Disabled';
      },
      proxyAuth() {
        return import.meta.env.VITE_AUTH === 'proxy';
      },

      // A logout under way, but not its warning, which asks for an answer.
      loggingOut() {
        const phenixStore = usePhenixStore();
        return phenixStore.loggingOut && !phenixStore.logoutWarning;
      },

      tunneler() {
        return usePhenixStore().features.includes('tunneler-download');
      },

      builderBeta() {
        return isFeatureEnabled(
          usePhenixStore().features,
          BUILDER_BETA_FEATURE,
        );
      },
    },

    methods: {
      //  These methods are used to logout a user; or, present
      //  routable link based on a Global user role.
      //  Builder Flow changes the server does not have are sent first,
      //  and a warning asks when some remain (see utils/logout.js).
      logout(event) {
        const button = event?.currentTarget;

        usePhenixStore()
          .requestLogout('manual')
          .then((outcome) => {
            // A logout the server did not answer leaves the session as it
            // was, and says so; focus goes back to Logout, as it does on
            // Stay signed in.
            if (outcome === 'failed') {
              this.$buefy.toast.open({
                message:
                  'Could not log out. Check your connection and try again.',
                type: 'is-danger',
                duration: 8000,
              });
              button?.focus();
            }

            // A narrow window's menu, which held Logout, closes on a click;
            // focus goes back to the button that opens it.
            if (
              (outcome === 'stayed' || outcome === 'failed') &&
              !button?.getClientRects().length
            ) {
              this.$el.querySelector('.navbar-burger')?.focus();
            }

            if (outcome === 'logged-out' && this.proxyAuth) {
              this.$buefy.toast.open({
                message: 'Your account has been reauthorized',
                type: 'is-success',
                duration: 4000,
              });
            }
          });
      },

      builderLoc() {
        const phenixStore = usePhenixStore();
        return this.$router.resolve({
          name: 'builder',
          params: { token: phenixStore.token },
        }).href;
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

  .app-header__logout:focus-visible {
    outline: 2px solid currentColor;
    outline-offset: -2px;
  }

  /* In the collapsed menu, the row is the button, as it is for a link. */
  @media (max-width: 1023px) {
    .app-header__logout {
      width: 100%;
    }
  }
</style>
