<template>
  <div class="content">
    <!-- CREATE -->
    <b-modal
      v-model="isCreateActive"
      @close="resetLocalUser"
      has-modal-card
      aria-role="dialog"
      aria-modal
      aria-label="Create a New User"
      close-button-aria-label="Close">
      <div class="modal-card" style="width: 25em">
        <header class="modal-card-head">
          <h2 class="modal-card-title">Create a New User</h2>
        </header>
        <section class="modal-card-body">
          <b-field
            label="User Name"
            label-for="create-user-username"
            :type="{ 'is-danger': userExists }"
            :message="{ 'User already exists': userExists }">
            <b-input
              id="create-user-username"
              :compat-fallthrough="false"
              ref="createUsername"
              type="text"
              autocomplete="off"
              v-model="user.username"
              minlength="4"
              maxlength="32"
              autofocus></b-input>
          </b-field>
          <b-field label="First Name" label-for="create-user-first-name">
            <b-input
              id="create-user-first-name"
              :compat-fallthrough="false"
              type="text"
              autocomplete="off"
              v-model="user.first_name"></b-input>
          </b-field>
          <b-field label="Last Name" label-for="create-user-last-name">
            <b-input
              id="create-user-last-name"
              :compat-fallthrough="false"
              type="text"
              autocomplete="off"
              v-model="user.last_name"></b-input>
          </b-field>
          <b-field
            label="Password"
            label-for="create-user-password"
            :type="createModalErrors.passwordErrorLevel">
            <b-input
              id="create-user-password"
              :compat-fallthrough="false"
              type="password"
              autocomplete="new-password"
              minlength="8"
              maxlength="32"
              :aria-invalid="
                createModalErrors.passwordErrorMessage ? 'true' : null
              "
              :aria-describedby="
                createModalErrors.passwordErrorMessage
                  ? 'create-user-password-error'
                  : null
              "
              v-model="user.password"></b-input>
            <template #message>
              <span
                v-if="createModalErrors.passwordErrorMessage"
                id="create-user-password-error"
                >{{ createModalErrors.passwordErrorMessage }}</span
              >
            </template>
          </b-field>
          <b-field
            label="Confirm Password"
            label-for="create-user-confirm-password">
            <b-input
              id="create-user-confirm-password"
              :compat-fallthrough="false"
              type="password"
              autocomplete="new-password"
              minlength="8"
              maxlength="32"
              v-model="user.confirmPassword"></b-input>
          </b-field>
          <b-field label="Role" label-for="create-user-role">
            <b-select
              id="create-user-role"
              :compat-fallthrough="false"
              v-model="user.role_name"
              expanded>
              <option v-for="(r, i) in roleNames" :key="i">
                {{ r }}
              </option>
            </b-select>
          </b-field>
          <!-- This padding isn't strictly necessary, but helps prevent tooltip from breaking view -->
          <b-field
            label="Resource Name(s)"
            label-for="create-user-resource-names"
            grouped
            style="padding-bottom: 20px">
            <b-input
              id="create-user-resource-names"
              :compat-fallthrough="false"
              type="text"
              autocomplete="off"
              aria-describedby="create-user-resource-names-help"
              v-model="user.resource_names"
              expanded></b-input>
            <b-tooltip
              :label="resourceNamesHelp"
              :triggers="['hover', 'focus']"
              type="is-light is-left"
              multilined>
              <button
                type="button"
                class="users__help"
                aria-label="Help for resource names"
                aria-describedby="create-user-resource-names-help">
                <b-icon icon="question-circle"></b-icon>
              </button>
            </b-tooltip>
            <span id="create-user-resource-names-help" class="is-sr-only">{{
              resourceNamesHelp
            }}</span>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button
            class="button is-light"
            @click="createUser"
            :disabled="!check_password_validity()">
            Create User
          </button>
        </footer>
      </div>
    </b-modal>
    <!-- EDIT -->
    <b-modal
      v-model="isEditActive"
      @close="resetLocalUser"
      has-modal-card
      aria-role="dialog"
      aria-modal
      :aria-label="`User ${user.username}`"
      close-button-aria-label="Close">
      <div class="modal-card" style="width: 25em">
        <header class="modal-card-head">
          <h2 class="modal-card-title">User {{ user.username }}</h2>
        </header>
        <section class="modal-card-body">
          <b-field label="First Name" label-for="edit-user-first-name">
            <b-input
              id="edit-user-first-name"
              :compat-fallthrough="false"
              type="text"
              autocomplete="off"
              v-model="user.first_name"
              autofocus></b-input>
          </b-field>
          <b-field label="Last Name" label-for="edit-user-last-name">
            <b-input
              id="edit-user-last-name"
              :compat-fallthrough="false"
              type="text"
              autocomplete="off"
              v-model="user.last_name"></b-input>
          </b-field>
          <!-- The user's current password. A password manager fills it only
               when users edit their own account. -->
          <b-field label="Password" label-for="edit-user-password">
            <b-input
              id="edit-user-password"
              :compat-fallthrough="false"
              type="password"
              :autocomplete="editingSelf ? 'current-password' : 'off'"
              v-model="user.password"></b-input>
          </b-field>
          <b-field label="New Password" label-for="edit-user-new-password">
            <b-input
              id="edit-user-new-password"
              :compat-fallthrough="false"
              type="password"
              autocomplete="new-password"
              minlength="8"
              maxlength="32"
              v-model="user.new_password"></b-input>
          </b-field>
          <b-field
            v-if="roleAllowed('users', 'create')"
            label="Role"
            label-for="edit-user-role">
            <b-select
              id="edit-user-role"
              :compat-fallthrough="false"
              v-model="user.role_name"
              expanded>
              <option v-for="(r, i) in roleNames" :key="i">
                {{ r }}
              </option>
            </b-select>
          </b-field>
          <!-- This padding isn't strictly necessary, but helps prevent tooltip from breaking view -->
          <b-field
            label="Resource Name(s)"
            label-for="edit-user-resource-names"
            grouped
            style="padding-bottom: 20px"
            v-if="roleAllowed('users', 'create')">
            <b-input
              id="edit-user-resource-names"
              :compat-fallthrough="false"
              type="text"
              autocomplete="off"
              aria-describedby="edit-user-resource-names-help"
              v-model="user.resource_names"
              expanded></b-input>
            <b-tooltip
              :label="resourceNamesHelp"
              :triggers="['hover', 'focus']"
              type="is-light is-left"
              multilined>
              <button
                type="button"
                class="users__help"
                aria-label="Help for resource names"
                aria-describedby="edit-user-resource-names-help">
                <b-icon icon="question-circle"></b-icon>
              </button>
            </b-tooltip>
            <span id="edit-user-resource-names-help" class="is-sr-only">{{
              resourceNamesHelp
            }}</span>
          </b-field>
        </section>
        <footer class="modal-card-foot buttons is-right">
          <button class="button is-light" @click="updateUser">
            Update User
          </button>
        </footer>
      </div>
    </b-modal>
    <!-- TOKEN -->
    <b-modal
      v-model="isNewTokenActive"
      @close="resetLocalUser"
      has-modal-card
      aria-role="dialog"
      aria-modal
      :aria-label="`New token for ${user.username}`"
      close-button-aria-label="Close">
      <div class="modal-card" style="width: 25em">
        <header class="modal-card-head">
          <h2 class="modal-card-title">New token for {{ user.username }}</h2>
        </header>
        <section class="modal-card-body">
          <template v-if="user.token">
            <b-field label="Token" label-for="token-value">
              <b-input
                id="token-value"
                :compat-fallthrough="false"
                type="text"
                v-model="user.token"
                readonly></b-input>
            </b-field>

            <b-button
              size="is-small"
              icon-left="copy"
              aria-label="Copy token"
              @click="copy">
            </b-button>

            <b-field label="Expires" label-for="token-expires">
              <b-input
                id="token-expires"
                :compat-fallthrough="false"
                type="text"
                v-model="user.token_exp"
                readonly></b-input>
            </b-field>
          </template>
          <template v-else>
            <b-field label="Description" label-for="token-description">
              <b-input
                id="token-description"
                :compat-fallthrough="false"
                type="text"
                autocomplete="off"
                v-model="user.token_desc"
                autofocus></b-input>
            </b-field>
            <b-field label="Lifetime (days)" label-for="token-lifetime">
              <b-input
                id="token-lifetime"
                :compat-fallthrough="false"
                type="text"
                autocomplete="off"
                aria-describedby="token-lifetime-help"
                v-model="user.token_lifetime"></b-input>
            </b-field>
            <b-tooltip
              :label="tokenLifetimeHelp"
              :triggers="['hover', 'focus']"
              type="is-light is-right">
              <button
                type="button"
                class="users__help"
                aria-label="Help for lifetime"
                aria-describedby="token-lifetime-help">
                <b-icon icon="question-circle"></b-icon>
              </button>
            </b-tooltip>
            <span id="token-lifetime-help" class="is-sr-only">{{
              tokenLifetimeHelp
            }}</span>
          </template>
        </section>
        <footer v-if="!user.token" class="modal-card-foot buttons is-right">
          <template v-if="user.proxy_token">
            <button v-if="isProxyTokenCopied" class="button is-light" disabled>
              Proxy Token Copied!
            </button>
            <button
              v-else
              class="button is-light"
              @click="copyProxyToken(user.proxy_token)">
              Copy Proxy Token
            </button>
          </template>
          <button class="button is-light" @click="createNewToken">
            Create Token
          </button>
        </footer>
      </div>
    </b-modal>
    <!-- BODY -->
    <b-field grouped position="is-right">
      <p v-if="roleAllowed('users', 'create')" class="control">
        <b-tooltip label="create a new user" type="is-light is-left">
          <button
            class="button is-light"
            aria-label="Create a new user"
            @click="isCreateActive = true">
            <b-icon icon="plus"></b-icon>
          </button>
        </b-tooltip>
      </p>
    </b-field>
    <div>
      <b-table
        :data="users"
        :paginated="table.isPaginated"
        aria-next-label="Next page"
        aria-previous-label="Previous page"
        aria-page-label="Page"
        aria-current-label="Current page"
        :per-page="table.perPage"
        v-model:current-page="table.currentPage"
        :pagination-simple="table.isPaginationSimple"
        :pagination-size="table.paginationSize"
        :default-sort-direction="table.defaultSortDirection"
        default-sort="username">
        <b-table-column field="username" label="User" sortable v-slot="props">
          <!-- A button, so the keyboard reaches it too. -->
          <b-tooltip label="change user settings" type="is-dark">
            <button
              type="button"
              class="users__name"
              @click="editUser(props.row.username)">
              {{ props.row.username }}
            </button>
          </b-tooltip>
        </b-table-column>
        <b-table-column field="first_name" label="First Name" v-slot="props">
          {{ props.row.first_name }}
        </b-table-column>
        <b-table-column
          field="last_name"
          label="Last Name"
          sortable
          v-slot="props">
          {{ props.row.last_name }}
        </b-table-column>
        <b-table-column field="role" label="Role" sortable v-slot="props">
          {{ props.row.role_name ? props.row.role_name : 'Not yet assigned' }}
        </b-table-column>
        <b-table-column label="Actions" width="150" centered v-slot="props">
          <b-tooltip
            class="action"
            :delay="500"
            label="create new user token"
            type="is-light"
            multilined>
            <button
              class="button is-light is-small"
              :aria-label="`Create token for ${props.row.username}`"
              @click="newToken(props.row.username)">
              <b-icon icon="key"></b-icon>
            </button>
          </b-tooltip>
          <b-tooltip
            class="action"
            :delay="500"
            label="delete user"
            type="is-light"
            multilined>
            <button
              v-if="roleAllowed('users', 'delete', props.row.username)"
              class="button is-light is-small"
              :aria-label="`Delete user ${props.row.username}`"
              @click="deleteUser(props.row.username)">
              <b-icon icon="trash"></b-icon>
            </button>
          </b-tooltip>
          <b-tooltip
            class="action"
            :delay="500"
            label="edit user"
            type="is-light"
            multilined>
            <button
              v-if="roleAllowed('users', 'patch', props.row.username)"
              class="button is-light is-small"
              :aria-label="`Edit user ${props.row.username}`"
              @click="editUser(props.row.username)">
              <b-icon icon="pencil"></b-icon>
            </button>
          </b-tooltip>
        </b-table-column>
      </b-table>
      <br />
      <b-field v-if="paginationNeeded" grouped position="is-right">
        <div class="control is-flex">
          <b-switch
            v-model="table.isPaginated"
            size="is-small"
            type="is-light"
            @input="changePaginate()"
            >Paginate</b-switch
          >
        </div>
      </b-field>
    </div>
    <b-loading
      :is-full-page="false"
      v-model="isWaiting"
      :can-cancel="false"></b-loading>
  </div>
</template>

<script>
  import axiosInstance from '@/utils/axios.js';
  import { uniq } from 'lodash-es';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';
  import { usePhenixStore } from '@/store';
  import { useTable } from '@/utils/useTable.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { useErrorNotification } from '@/utils/errorNotif';

  export default {
    setup() {
      return { ...useTable(), roleAllowed };
    },
    beforeUnmount() {
      removeWsHandler(this.handleWs);
    },
    async created() {
      addWsHandler(this.handleWs);
      this.updateUsers();
      this.getPasswordRequirements();
    },
    computed: {
      // Whether the edit dialog shows the account of the signed-in user.
      editingSelf() {
        return this.user.username === usePhenixStore().username;
      },
      // Intentionally restores the persisted pagination toggle as a side
      // effect on first access.
      /* eslint-disable vue/no-side-effects-in-computed-properties */
      paginationNeeded() {
        this.restorePaginate();

        if (this.users.length <= 10) {
          this.table.isPaginated = false;
          return false;
        } else {
          return true;
        }
      },
      /* eslint-enable vue/no-side-effects-in-computed-properties */
    },

    methods: {
      handleWs(msg) {
        // We only care about publishes pertaining to a user resource.
        if (msg.resource.type != 'user') {
          return;
        }

        let users = this.users;

        switch (msg.resource.action) {
          case 'create': {
            let user = msg.result;

            user.resource_names = user.resource_names.join(' ');
            user.role_name = user.role.name;
            users.push(user);

            this.users = [...users];

            this.$buefy.toast.open({
              message: 'The ' + msg.resource.name + ' user was created.',
              type: 'is-success',
            });

            break;
          }

          case 'update': {
            for (let i = 0; i < users.length; i++) {
              if (users[i].username == msg.resource.name) {
                let user = msg.result;

                user.resource_names =
                  user.resource_names === null
                    ? null
                    : user.resource_names.join(' ');
                users[i] = user;

                break;
              }
            }

            this.users = [...users];

            this.$buefy.toast.open({
              message: 'The ' + msg.resource.name + ' user was updated.',
              type: 'is-success',
            });

            break;
          }

          case 'delete': {
            for (let i = 0; i < users.length; i++) {
              if (users[i].username == msg.resource.name) {
                users.splice(i, 1);
                break;
              }
            }

            this.users = [...users];

            this.$buefy.toast.open({
              message: 'The ' + msg.resource.name + ' user was deleted.',
              type: 'is-success',
            });

            break;
          }
        }
      },
      updateUsers() {
        axiosInstance
          .get('users')
          .then((response) => {
            var state = response.data;
            state.users.forEach((u) => (u.role_name = u.role.name));
            this.users = state.users;
            this.isWaiting = false;
          })
          .catch((err) => {
            useErrorNotification(err);
            this.isWaiting = false;
          });

        // this is only used when creating/editing a user for the role dropdown
        if (roleAllowed('roles', 'list')) {
          axiosInstance
            .get('roles')
            .then((response) => {
              const state = response.data;
              this.roleNames = state.roles.map((r) => r.name);
              this.isWaiting = false;
            })
            .catch((err) => {
              useErrorNotification(err);
              this.isWaiting = false;
            });
        }
      },

      createUser() {
        for (let i = 0; i < this.users.length; i++) {
          if (this.users[i].username == this.user.username) {
            this.showUserExists();
            return;
          }
        }

        if (!this.user.username) {
          this.$buefy.toast.open({
            message: 'You must include a username',
            type: 'is-warning',
            duration: 4000,
          });

          return;
        }

        if (!this.user.first_name) {
          this.$buefy.toast.open({
            message: 'You must include a first name',
            type: 'is-warning',
            duration: 4000,
          });

          return;
        }

        if (!this.user.last_name) {
          this.$buefy.toast.open({
            message: 'You must include a last name',
            type: 'is-warning',
            duration: 4000,
          });

          return;
        }

        if (!this.user.password) {
          this.$buefy.toast.open({
            message: 'You must include a password',
            type: 'is-warning',
            duration: 4000,
          });

          return;
        }

        if (!this.check_password_validity(this.user.password)) {
          //check_password_validity
          this.$buefy.toast.open({
            message: 'Password does not meet requirements',
            type: 'is-warning',
            duration: 4000,
          });
          return;
        }

        if (!this.user.confirmPassword) {
          this.$buefy.toast.open({
            message: 'You must include a password confirmation',
            type: 'is-warning',
            duration: 4000,
          });

          return;
        }

        if (this.user.password != this.user.confirmPassword) {
          this.$buefy.toast.open({
            message: 'Your passwords do not match',
            type: 'is-warning',
            duration: 4000,
          });

          return;
        }

        if (!this.user.role_name) {
          this.$buefy.toast.open({
            message: 'You must select a role',
            type: 'is-warning',
            duration: 4000,
          });

          return;
        }

        // The request is a copy, so the dialog keeps what was typed when
        // the server refuses it.
        const user = { ...this.user };
        delete user.confirmPassword;

        if (user.resource_names) {
          user.resource_names = user.resource_names.split(' ');
        }

        this.isWaiting = true;

        axiosInstance
          .post('users', user)
          .then((_) => {
            this.isWaiting = false;
            this.resetLocalUser();
            this.isCreateActive = false;
          })
          .catch((err) => {
            this.isWaiting = false;

            // A user the list did not show yet has the name.
            if (err.response?.status === 409) {
              this.showUserExists();

              return;
            }

            useErrorNotification(err);
          });
      },
      editUser(username) {
        for (let i = 0; i < this.users.length; i++) {
          if (this.users[i].username == username) {
            this.user = this.users[i];
            break;
          }
        }

        this.user.resource_names = uniq(this.user.resource_names).join(' ');

        this.isEditActive = true;
      },

      updateUser() {
        const store = usePhenixStore();
        if (
          store.username == this.user.username &&
          store.role.name != this.user.role_name
        ) {
          this.$buefy.toast.open({
            message:
              'You cannot change the role of the user you are currently logged in as.',
            type: 'is-danger',
            duration: 5000,
          });

          this.resetLocalUser();
          this.isWaiting = false;
          this.isEditActive = false;

          return;
        }

        delete this.user.id;

        let user = this.user;

        user.resource_names = user.resource_names.split(' ');

        this.isEditActive = false;
        this.isWaiting = true;

        axiosInstance
          .patch('users/' + user.username, user)
          .then((_) => {
            delete user.password;
            delete user.new_password;

            let users = this.users;

            for (let i = 0; i < users.length; i++) {
              if (users[i].username == user.username) {
                users[i] = user;
                break;
              }
            }

            this.users = [...users];
            this.isWaiting = false;
          })
          .catch((err) => {
            useErrorNotification(err);
            this.isWaiting = false;
          });

        this.resetLocalUser();
      },

      deleteUser(username) {
        if (username === usePhenixStore().username) {
          this.$buefy.toast.open({
            message:
              'You cannot delete the user you are currently logged in as.',
            type: 'is-danger',
            duration: 4000,
          });

          return;
        }

        this.$buefy.dialog.confirm({
          title: 'Delete the User',
          message:
            'This will DELETE the ' +
            username +
            ' user. Are you sure you want to do this?',
          cancelText: 'Cancel',
          confirmText: 'Delete',
          type: 'is-danger',
          hasIcon: true,
          onConfirm: () => {
            this.isWaiting = true;

            axiosInstance
              .delete('users/' + username)
              .then((_) => {
                let users = this.users;

                for (let i = 0; i < users.length; i++) {
                  if (users[i].username == username) {
                    users.splice(i, 1);
                    break;
                  }
                }
                this.users = [...users];
                this.isWaiting = false;
              })
              .catch((err) => {
                useErrorNotification(err);
              });
          },
        });
      },

      newToken(username) {
        this.user.username = username;

        let users = this.users;
        for (let i = 0; i < users.length; i++) {
          if (users[i].username == username) {
            this.user.proxy_token = users[i].proxy_token;
            break;
          }
        }

        this.isProxyTokenCopied = false;
        this.isNewTokenActive = true;
      },

      async createNewToken() {
        this.isWaiting = true;

        let data = {
          desc: this.user.token_desc,
          lifetime: this.user.token_lifetime,
        };

        axiosInstance
          .post(`users/${this.user.username}/tokens`, data)
          .then((resp) => {
            let token = resp.data;

            this.user.token = token.token;
            this.user.token_exp = token.exp;
          })
          .catch((err) => {
            useErrorNotification(err);
          })
          .finally(() => {
            this.isWaiting = false;
          });
      },

      copyProxyToken(token) {
        navigator.clipboard.writeText(atob(token));
        this.isProxyTokenCopied = true;
      },

      copy() {
        navigator.clipboard.writeText(this.user.token);
      },

      resetLocalUser() {
        this.user = {};
      },
      // Another user has the name: the User Name field says so, a toast
      // announces it, and focus goes back to the field.
      showUserExists() {
        this.userExists = true;
        this.$buefy.toast.open({
          message: 'User already exists',
          type: 'is-warning',
          duration: 4000,
        });
        this.$refs.createUsername?.focus();
      },
      check_password_validity() {
        const password = this.user.password;
        if (password == undefined) {
          return true;
        }
        if (password.length == 0) {
          //don't want errors immediately on modal if they haven't typed in anything yet
          this.createModalErrors.passwordErrorMessage = null;
          this.createModalErrors.passwordErrorLevel = null;
          return true;
        }
        if (password.length < this.passwordReqs.min_length) {
          this.createModalErrors.passwordErrorMessage =
            'Password must be longer than ' +
            this.passwordReqs.min_length +
            ' characters.';
          this.createModalErrors.passwordErrorLevel = 'is-danger';
          return false;
        }
        if (!/[a-z]/.test(password) && this.passwordReqs.lowercase_req) {
          this.createModalErrors.passwordErrorMessage =
            'Password must contain a lowercase letter';
          this.createModalErrors.passwordErrorLevel = 'is-danger';
          return false;
        }
        if (!/[A-Z]/.test(password) && this.passwordReqs.uppercase_req) {
          this.createModalErrors.passwordErrorMessage =
            'Password must contain an uppercase letter';
          this.createModalErrors.passwordErrorLevel = 'is-danger';
          return false;
        }
        if (!/\d/.test(password) && this.passwordReqs.number_req) {
          this.createModalErrors.passwordErrorMessage =
            'Password must contain a number';
          this.createModalErrors.passwordErrorLevel = 'is-danger';
          return false;
        }
        if (!/\W/.test(password) && this.passwordReqs.symbol_req) {
          this.createModalErrors.passwordErrorMessage =
            'Password must contain a symbol';
          this.createModalErrors.passwordErrorLevel = 'is-danger';
          return false;
        }

        this.createModalErrors.passwordErrorMessage = null;
        this.createModalErrors.passwordErrorLevel = null;
        return true;
      },
      getPasswordRequirements() {
        axiosInstance.get('settings/password').then((response) => {
          this.passwordReqs = response.data;
        });
      },
    },

    watch: {
      // After the user changes the name, the form does not know if it is
      // taken.
      'user.username'() {
        this.userExists = false;
      },
    },

    data() {
      return {
        resourceNamesHelp:
          'resource names can include wildcards for broad assignment (ex. *inv*); they can also include `!` to not allow access to a resource (ex. !*inv*). VM and config names are namespaced (ex. exp1/vm1, Topology/foo) and `*` does not match `/`, so use patterns such as exp1/* or */vm1',
        tokenLifetimeHelp: 'number of days this token should be valid',
        roleNames: [],
        users: [],
        user: {},
        userExists: false,
        isCreateActive: false,
        isEditActive: false,
        isNewTokenActive: false,
        isProxyTokenCopied: false,
        isWaiting: true,

        passwordReqs: {
          number_req: false,
          symbol_req: false,
          lowercase_req: false,
          uppercase_req: false,
          min_length: 8,
        },
        createModalErrors: {
          passwordErrorMessage: null,
          passwordErrorLevel: null,
        },
      };
    },
  };
</script>

<style scoped>
  /* The name button looks like the text it replaces, and shows its focus. */
  .users__name {
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-align: start;
    cursor: pointer;
  }

  /* The help icon is a button, so the keyboard can show its tooltip. */
  .users__help {
    display: inline-flex;
    align-items: center;
    height: 100%;
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    cursor: help;
  }

  /* Buefy fields keep their message slot; it shows only with an error. */
  .modal-card :deep(.help:empty) {
    display: none;
  }
</style>
