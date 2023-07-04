<template>
  <div class="content">
    <b-modal :active.sync="isCreateActive" :on-cancel="resetLocalUser" has-modal-card>
      <div class="modal-card" style="width:25em">
        <header class="modal-card-head">
          <p class="modal-card-title">Create a New User</p>
        </header>
        <section class="modal-card-body">
          <b-field label="User Name" 
            :type="{ 'is-danger' : userExists }" 
            :message="{ 'User already exists' : userExists }">
            <b-input type="text" v-model="user.username" minlength="4" maxlength="32" autofocus></b-input>
          </b-field>
          <b-field label="First Name">
            <b-input type="text" v-model="user.first_name"></b-input>
          </b-field>
          <b-field label="Last Name">
            <b-input type="text" v-model="user.last_name"></b-input>
          </b-field>
          <b-field label="Password">
            <b-input type="password" minlength="8" maxlength="32" v-model="user.password"></b-input>
          </b-field>
          <b-field label="Confirm Password">
            <b-input type="password" minlength="8" maxlength="32" v-model="user.confirmPassword"></b-input>
          </b-field>
          <b-field label="Role">
            <b-select v-model="user.role_name" expanded>
              <option v-for="(r, i) in roleNames" :key="i">
                {{ r }}
              </option>
            </b-select>
          </b-field>
          <b-tooltip label="resource names can include wildcards for broad
                  assignment (ex. *inv*); they can also include `!` to not
                  allow access to a resource (ex. !*inv*)"
                  type="is-light is-right"
                  multilined>
          <b-field label="Resource Name(s)"></b-field>
          <b-icon icon="question-circle" style="color:#383838"></b-icon>
          </b-tooltip>
          <b-input type="text" v-model="user.resource_names"></b-input>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button is-light" @click="createUser">Create User</button>
        </footer>
      </div>
    </b-modal>
    <b-modal :active.sync="isEditActive" :on-cancel="resetLocalUser" has-modal-card>
      <div class="modal-card" style="width:25em">
        <header class="modal-card-head">
          <p class="modal-card-title">User {{ user.username }}</p>
        </header>
        <section class="modal-card-body">
          <b-field label="First Name">
            <b-input type="text" v-model="user.first_name" autofocus></b-input>
          </b-field>
          <b-field label="Last Name">
            <b-input type="text" v-model="user.last_name"></b-input>
          </b-field>
          <b-field label="Password">
            <b-input type="password" v-model="user.password"></b-input>
          </b-field>
          <b-field label="New Password">
            <b-input type="password" minlength="8" maxlength="32" v-model="user.new_password"></b-input>
          </b-field>
          <b-field v-if="roleAllowed('users', 'create')" label="Role">
            <b-select v-model="user.role_name" expanded>
              <option v-for="(r, i) in roleNames" :key="i">
                {{ r }}
              </option>
            </b-select>
          </b-field>
          <b-field v-if="roleAllowed('users', 'create')" label="Resource Name(s)">
            <b-input type="text" v-model="user.resource_names"></b-input>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button is-light" @click="updateUser">Update User</button>
        </footer>
      </div>
    </b-modal>
    <b-modal :active.sync="isNewTokenActive" :on-cancel="resetLocalUser" has-modal-card>
      <div class="modal-card" style="width:25em">
        <header class="modal-card-head">
          <p class="modal-card-title">New token for {{ user.username }}</p>
        </header>
        <section class="modal-card-body">
          <template v-if="user.token">
            <b-field label="Token">
              <b-input type="text" ref="clone" v-model="user.token" readonly></b-input>
            </b-field>
            <b-button size="is-small" icon-left="copy" @click="copy">
            </b-button>
            <b-field label="Expires">
              <b-input type="text" v-model="user.token_exp" readonly></b-input>
            </b-field>
          </template>
          <template v-else>
            <b-field label="Description">
              <b-input type="text" v-model="user.token_desc" autofocus></b-input>
            </b-field>
            <b-field label="Lifetime (days)">
              <b-input type="text" v-model="user.token_lifetime"></b-input>
            </b-field>
            <b-tooltip label="number of days this token should be valid"
              type="is-light is-right">
              <b-icon icon="question-circle" style="color:#383838"></b-icon>
            </b-tooltip>
          </template>
        </section>
        <footer v-if="!user.token" class="modal-card-foot buttons is-right">
          <button class="button is-light" @click="createNewToken">Create Token</button>
        </footer>
      </div>
    </b-modal>
    <template>
      <hr>
      <b-field grouped position="is-right">
        <p v-if="roleAllowed('users', 'create')" class="control">
          <b-tooltip label="create a new user" type="is-light is-left">
            <button class="button is-light" @click="isCreateActive = true">
              <b-icon icon="plus"></b-icon>
            </button>
          </b-tooltip>
        </p>
      </b-field>
      <div>
        <b-table
          :data="users"
          :paginated="table.isPaginated"
          :per-page="table.perPage"
          :current-page.sync="table.currentPage"
          :pagination-simple="table.isPaginationSimple"
          :pagination-size="table.paginationSize"
          :default-sort-direction="table.defaultSortDirection"
          default-sort="username">
          <b-table-column field="username" label="User" sortable v-slot="props">
            <template>
              <b-tooltip label="change user settings" type="is-dark">
                <div class="field">
                  <div @click="editUser( props.row.username )">
                    {{ props.row.username }}
                  </div>
                </div>
              </b-tooltip>
            </template>
          </b-table-column>
          <b-table-column field="first_name" label="First Name" v-slot="props">
            {{ props.row.first_name }}
          </b-table-column>
          <b-table-column field="last_name" label="Last Name" sortable v-slot="props">
            {{ props.row.last_name }}
          </b-table-column>
          <b-table-column field="role" label="Role" sortable v-slot="props">
            {{ props.row.role_name ? props.row.role_name : "Not yet assigned" }}
          </b-table-column>
          <b-table-column label="Actions" width="100" centered v-slot="props">
            <button class="button is-light is-small action" @click="newToken( props.row.username )">
              <b-icon icon="key"></b-icon>
            </button>
            <button v-if="roleAllowed('users', 'delete', props.row.username)" class="button is-light is-small action" @click="deleteUser( props.row.username )">
              <b-icon icon="trash"></b-icon>
            </button>
          </b-table-column>
        </b-table>
        <br>
        <b-field v-if="paginationNeeded" grouped position="is-right">
          <div class="control is-flex">
            <b-switch v-model="table.isPaginated" size="is-small" type="is-light" @input="changePaginate()">Paginate</b-switch>
          </div>
        </b-field>
      </div>
    </template>
    <b-loading :is-full-page="true" :active.sync="isWaiting" :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  export default {
    
    beforeDestroy () {
      this.$options.sockets.onmessage = null;
    },
    
    async created () {
      this.$options.sockets.onmessage = this.handler;
      this.updateUsers();
    },

    computed: {
      paginationNeeded () {
        var user = localStorage.getItem( 'user' );

        if ( localStorage.getItem( user + '.lastPaginate' ) ) {
          this.table.isPaginated = localStorage.getItem( user + '.lastPaginate' )  == 'true';
        }

        if ( this.users.length <= 10 ) {
          this.table.isPaginated = false;
          return false;
        } else {
          return true;
        }
      }
    },

    methods: {
      handler ( event ) {
        event.data.split( /\r?\n/ ).forEach( m => {
          let msg = JSON.parse( m );
          this.handle( msg );
        });
      },
    
      handle ( msg ) {
        // We only care about publishes pertaining to a user resource.
        if ( msg.resource.type != 'user' ) {
          return;
        }

        let users = this.users;

        switch ( msg.resource.action ) {
          case 'create': {
            let user = msg.result;

            user.resource_names = user.resource_names.join( ' ' );
            users.push( user );
      
            this.users = [ ...users ];
          
            this.$buefy.toast.open({
              message: 'The ' + msg.resource.name + ' user was created.',
              type: 'is-success'
            });

            break;
          }

          case 'update': {
            for ( let i = 0; i < users.length; i++ ) {
              if ( users[ i ].username == msg.resource.name ) {
                let user = msg.result;

                user.resource_names = user.resource_names.join( ' ' );
                users[ i ] = user;

                break;
              }
            }
          
            this.users = [ ...users ];
          
            this.$buefy.toast.open({
              message: 'The ' + msg.resource.name + ' user was updated.',
              type: 'is-success'
            });

            break;
          }

          case 'delete': {
            for ( let i = 0; i < users.length; i++ ) {
              if ( users[ i ].username == msg.resource.name ) {
                users.splice( i, 1 );
                break;
              }
            }
          
            this.users = [ ...users ];
          
            this.$buefy.toast.open({
              message: 'The ' + msg.resource.name + ' user was deleted.',
              type: 'is-success'
            });

            break;
          }
        }
      },

      updateUsers () {
        this.$http.get( 'users' ).then(
          response => {
            response.json().then(
              state => {
                // pull role name into user object since this UI doesn't care about the role itself
                state.users.forEach(u => u.role_name = u.role.name)
                this.users = state.users;
                this.isWaiting = false;
              }
            );
          }, err => {
            this.errorNotification(err);            
            this.isWaiting = false;
          }
        );
        // this is only used when creating/editing a user for the role dropdown
        if (this.roleAllowed('roles', 'list')) {
          this.$http.get( 'roles' ).then(
            response => {
              response.json().then(
                state => {
                  this.roleNames = state.roles.map(r => r.name);
                  this.isWaiting = false;
                }
              );
            }, err => {
              this.errorNotification(err);            
              this.isWaiting = false;
            }
          );
        }
      },

      changePaginate () {
        var user = localStorage.getItem( 'user' );
        localStorage.setItem( user + '.lastPaginate', this.table.isPaginated );
      },

      createUser () {
        for ( let i = 0; i < this.users.length; i++ ) {
          if ( this.users[ i ].username == this.user.username ) {
            this.userExists = true;
            return;
          }
        }

        if ( !this.user.username ) {
          this.$buefy.toast.open({
            message: 'You must include an username',
            type: 'is-warning',
            duration: 4000
          });

          return;
        }

        if ( !this.user.first_name ) {
          this.$buefy.toast.open({
            message: 'You must include a first name',
            type: 'is-warning',
            duration: 4000
          });

          return;
        }

        if ( !this.user.last_name ) {
          this.$buefy.toast.open({
            message: 'You must include a last name',
            type: 'is-warning',
            duration: 4000
          });

          return;
        }

        if ( !this.user.password ) {
          this.$buefy.toast.open({
            message: 'You must include a password',
            type: 'is-warning',
            duration: 4000
          });

          return;
        }

        if ( !this.user.confirmPassword ) {
          this.$buefy.toast.open({
            message: 'You must include a password confirmation',
            type: 'is-warning',
            duration: 4000
          });

          return;
        }

        if ( this.user.password != this.user.confirmPassword ) {
          this.$buefy.toast.open({
            message: 'Your passwords do not match',
            type: 'is-warning',
            duration: 4000
          });

          return;
        }

        delete this.user.confirmPassword;

        if ( !this.user.role_name ) {
          this.$buefy.toast.open({
            message: 'You must select a role',
            type: 'is-warning',
            duration: 4000
          });

          return;
        }

        if ( this.user.resource_names ) {
          this.user.resource_names = this.user.resource_names.split( ' ' );
        }

        this.isWaiting = true;
        
        let name = this.user.username;
        
        this.$http.post(
          'users', this.user
        ).then(
          response => {            
            this.isWaiting = false;
          }, err => {
            this.errorNotification(err);            
            this.isWaiting = false;
          }
        )

        this.resetLocalUser();
        this.isCreateActive = false;
      },

      editUser ( username ) {
        for ( let i = 0; i < this.users.length; i++ ) {
          if ( this.users[ i ].username == username ) {
            this.user = this.users[ i ];
            break;
          }
        }

        this.user.resource_names = _.uniq( this.user.resource_names ).join( ' ' );

        this.isEditActive = true;
      },

      updateUser () {
        if ( this.$store.getters.username == this.user.username && this.$store.getters.role.name != this.user.role_name ) {
          this.$buefy.toast.open({
            message: 'You cannot change the role of the user you are currently logged in as.',
            type: 'is-danger',
            duration: 5000
          });
          
          this.resetLocalUser();
          this.isWaiting = false;
          this.isEditActive = false;
          
          return;
        }
        
        delete this.user.id;
        
        let user = this.user;

        user.resource_names = user.resource_names.split( ' ' );
        
        this.isEditActive = false;
        this.isWaiting = true;

        this.$http.patch( 
          'users/' + user.username, user 
        ).then(
          _ => {
            delete user.password;
            delete user.new_password;

            let users = this.users;

            for ( let i = 0; i < users.length; i++ ) {
              if ( users[ i ].username == user.username ) {
                users[ i ] = user;
                break;
              }
            }
            
            this.users = [ ...users ];       
            this.isWaiting = false;
          }, err => {
            this.errorNotification(err);            
            this.isWaiting = false;
          }
        )

        this.resetLocalUser();
      },

      deleteUser ( username ) {
        if ( username === this.$store.getters.username ) {
          this.$buefy.toast.open({
            message: 'You cannot delete the user you are currently logged in as.',
            type: 'is-danger',
            duration: 4000
          });

          return;
        }

        this.$buefy.dialog.confirm({
          title: 'Delete the User',
          message: 'This will DELETE the ' + username + ' user. Are you sure you want to do this?',
          cancelText: 'Cancel',
          confirmText: 'Delete',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;
            
            this.$http.delete(
              'users/' + username
            ).then(
              response => {
                let users = this.users;
                  
                for ( let i = 0; i < users.length; i++ ) {
                  if ( users[ i ].username == username ) {
                    users.splice( i, 1 );
                    break;
                  }
                }
                
                this.users = [ ...users ];
            
                this.isWaiting = false;
              }, err => {
                this.errorNotification(err);
              }
            )
          }
        })
      },

      newToken (username) {
        this.user.username = username;
        this.isNewTokenActive = true;
      },

      async createNewToken () {
        this.isWaiting = true;

        try {
          let data = {'desc': this.user.token_desc, 'lifetime': this.user.token_lifetime};

          let resp  = await this.$http.post(`users/${this.user.username}/tokens`, data);
          let token = await resp.json();

          this.user.token     = token.token;
          this.user.token_exp = token.exp;
        } catch (err) {
          this.errorNotification(err);
        } finally {
          this.isWaiting = false;
        }
      },

      copy () {
        navigator.clipboard.writeText(this.$refs.clone.value);
      },

      resetLocalUser () {
        this.user = {};
      }
    },

    data () {
      return {
        table: {
          striped: true,
          isPaginated: false,
          isPaginationSimple: true,
          paginationSize: 'is-small',
          defaultSortDirection: 'asc',
          currentPage: 1,
          perPage: 10
        },
        roleNames: [],
        users: [],
        user: {},
        userExists: false,
        isCreateActive: false,
        isEditActive: false,
        isNewTokenActive: false,
        isWaiting: true
      }
    }
  }
</script>

<style scoped>
  button.action {
    margin-right: 5px;
  }

  a.action {
    margin-right: 5px;
  }
</style>
