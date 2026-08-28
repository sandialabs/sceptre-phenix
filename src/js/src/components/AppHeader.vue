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
      <div class="navbar-item theme-toggle-item">
        <button
          type="button"
          class="theme-toggle"
          :aria-label="themeToggleLabel"
          :title="themeToggleLabel"
          @click="toggleTheme">
          <font-awesome-icon :icon="themeIcon" aria-hidden="true" />
        </button>
      </div>
      <b-navbar-item v-if="proxyAuth" class="navbar-item" @click="logout"
        >Reauthorize
      </b-navbar-item>
      <b-navbar-item v-else-if="auth" class="navbar-item" @click="logout"
        >Logout
      </b-navbar-item>
    </template>
  </b-navbar>
</template>

<script>
  import { usePhenixStore } from '@/store.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import axiosInstance from '@/utils/axios.js';
  import { useTheme } from '@/utils/theme.js';

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

      tunneler() {
        return usePhenixStore().features.includes('tunneler-download');
      },
      activeTheme() {
        return useTheme().activeTheme.value;
      },
      themeIcon() {
        return this.activeTheme === 'dark' ? 'moon' : 'sun';
      },
      themeToggleLabel() {
        return this.activeTheme === 'dark'
          ? 'Switch to light mode'
          : 'Switch to dark mode';
      },
    },

    methods: {
      toggleTheme() {
        try {
          useTheme().toggleTheme();
        } catch (error) {
          this.$buefy.toast.open({
            message: error.message,
            type: 'is-danger',
            duration: 5000,
          });
        }
      },
      //  These methods are used to logout a user; or, present
      //  routable link based on a Global user role.
      logout() {
        axiosInstance.get('logout').then((response) => {
          if (response.status == 204) {
            usePhenixStore().logout();

            if (this.proxyAuth) {
              this.$buefy.toast.open({
                message: 'Your account has been reauthorized',
                type: 'is-success',
                duration: 4000,
              });
            }
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
  .theme-toggle-item {
    display: flex;
    align-items: center;
  }

  .theme-toggle {
    display: inline-flex;
    width: 2.5rem;
    height: 2.5rem;
    align-items: center;
    justify-content: center;
    color: var(--text-primary);
    background: transparent;
    border: 1px solid transparent;
    border-radius: 0.375rem;
    cursor: pointer;
  }

  .theme-toggle:hover {
    color: var(--text-primary);
    background: var(--surface-hover);
  }

  .theme-toggle:focus-visible {
    outline: 3px solid var(--focus-ring);
    outline-offset: 2px;
  }
</style>
