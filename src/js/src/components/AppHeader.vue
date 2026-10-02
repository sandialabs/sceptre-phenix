<!-- 
The header component is available on all views based on the 
App.vue component. The available routable links are available 
based on whether the user is logged in and furthermore based on 
what their role allows.
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
        v-if="auth && roleAllowed('configs', 'list')"
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
        v-if="auth && webshark"
        tag="router-link"
        :to="{ name: 'webshark' }"
        >WebShark</b-navbar-item
      >
      <b-navbar-item
        v-if="auth && tunneler"
        tag="router-link"
        :to="{ name: 'tunneler' }">
        Tunneler
      </b-navbar-item>
      <b-navbar-item
        v-if="auth && roleAllowed('settings', 'update')"
        tag="router-link"
        :to="{ name: 'settings' }"
        >Settings</b-navbar-item
      >
    </template>

    <template #end>
      <b-navbar-item v-if="auth" tag="div">
        <refresh-status></refresh-status>
      </b-navbar-item>
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
  import { canUseWebShark, webSharkInstalled } from '@/utils/webshark.js';
  import axiosInstance from '@/utils/axios.js';
  import { endConsole } from '@/utils/consoleSession.js';
  import RefreshStatus from '@/components/RefreshStatus.vue';

  export default {
    components: { RefreshStatus },
    setup() {
      return { roleAllowed };
    },
    //  The computed elements decide what the header shows: every link
    //  but Experiments waits for the user to log in, a Disabled role gets
    //  no Users link, an authenticating proxy gets Reauthorize rather than
    //  Logout, and Tunneler and WebShark show only when the server offers
    //  them (WebShark also needs a role that can open captures).
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

      webshark() {
        return webSharkInstalled(usePhenixStore().features) && canUseWebShark();
      },
    },

    methods: {
      async logout() {
        // end the console first, while the token still allows it
        await endConsole();

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
