<!-- 
The header component is available on all views based on the 
App.vue component. The available routable links are available 
based on whether the user is logged in and furthermore based on 
their role. Based on the current limitations per user role, these 
are only available to Global Administrator or Global Viewer.
 -->

<template>
  <div>
    <a :href="homeLoc()">
      <img src="@/assets/phenix-banner.png" width="240">
    </a>

    <nav class="navbar is-light" role="navigation" aria-label="main navigation">
      <div id="navbarBasicExample" class="navbar-menu">
        <div class="navbar-start">
          <menu-link v-if="auth && roleAllowed('experiments', 'list')"
                       :to="{name: 'experiments'}"
                       class="navbar-item">Experiments</menu-link>
          <menu-link v-if="auth && roleAllowed('configs', 'list')"
                       :to="{name: 'configs'}"
                       class="navbar-item">Configs</menu-link>
          <menu-link v-if="auth && roleAllowed('hosts', 'list')"
                       :to="{name: 'hosts'}"
                       class="navbar-item">Hosts</menu-link>
          <menu-link v-if="auth"
                       :to="{name: 'users'}"
                       class="navbar-item">Users</menu-link>
          <menu-link v-if="auth && roleAllowed('logs', 'list')"
                       :to="{name: 'log'}"
                       class="navbar-item">Log</menu-link>
          <menu-link v-if="auth && roleAllowed('experiments', 'list')"
                       :to="{name: 'scorch'}"
                       class="navbar-item">Scorch</menu-link>
          <menu-link v-if="auth && roleAllowed('experiments', 'list')"
                        :to="builderLoc()"
                        external
                        class="navbar-item">Builder</menu-link>
          <menu-link v-if="auth && roleAllowed('miniconsole', 'post')"
                        :to="{name: 'console'}"
                        class="navbar-item">Console</menu-link>
          <menu-link v-if="auth && tunneler"
                        :to="{name: 'tunneler'}"
                        class="navbar-item">Tunneler</menu-link>
        </div>
      </div>

      <div class="navbar-end">
        <div v-if="proxyAuth" class="navbar-item">
          <a role="button" class="button navbar-item is-light" @click="logout">Reauthorize</a>
        </div>
        <div v-else-if="auth" class="navbar-item">
          <a role="button" class="button navbar-item is-light" @click="logout">Logout</a>
        </div>
      </div>
    </nav>
  </div>
</template>

<script>
  import MenuLink from '@/components/MenuLink.vue'

  export default {
    components: {
      menuLink: MenuLink
    },

    //  The computed elements determine if the user is already logged 
    //  in; if so, the routable links are available. If not, the sign 
    //  in routable link is the only one available. The role getter 
    //  determines what the role of the user is; this is used to present 
    //  routable links in the header row.
    computed: {
      auth () {
        return this.$store.getters.auth;
      },

      proxyAuth () {
        return process.env.VUE_APP_AUTH === 'proxy';
      },

      tunneler () {
        return this.$store.getters.features.includes('tunneler-download');
      }
    },
    
    methods: {
      //  These methods are used to logout a user; or, present 
      //  routable link based on a Global user role.
      logout () {
        this.$http.get( 'logout' ).then(
          response => {
            if ( response.status == 204 ) {
              this.$store.commit( 'LOGOUT' );

              if ( this.proxyAuth ) {
                this.$buefy.toast.open({
                  message: 'Your account has been reauthorized',
                  type: 'is-success',
                  duration: 4000
                });
              }
            }
          }
        );
      },

      homeLoc () {
        return this.$router.resolve({name: 'home'}).href
      },

      builderLoc () {
        return this.$router.resolve({name: 'builder', params: {token: this.$store.getters.token}}).href;
      }
    }
  }
</script>
