<template>
  <div id="signup">
    <!-- A form, so Enter submits it and password managers know its fields. -->
    <form class="signup-form" novalidate @submit.prevent="onSubmit">
      <b-field label="Username" label-for="proxy-signup-username">
        <b-input
          id="proxy-signup-username"
          :compat-fallthrough="false"
          type="text"
          autocomplete="username"
          :model-value="username"
          disabled></b-input>
      </b-field>
      <b-field
        label="First Name"
        label-for="proxy-signup-first-name"
        :type="errors.firstName ? 'is-danger' : null">
        <b-input
          id="proxy-signup-first-name"
          :compat-fallthrough="false"
          ref="firstName"
          type="text"
          autocomplete="given-name"
          v-model="firstName"
          :aria-invalid="errors.firstName ? 'true' : null"
          :aria-describedby="
            errors.firstName ? 'proxy-signup-first-name-error' : null
          "
          autofocus></b-input>
        <template #message>
          <span v-if="errors.firstName" id="proxy-signup-first-name-error">{{
            errors.firstName
          }}</span>
        </template>
      </b-field>
      <b-field
        label="Last Name"
        label-for="proxy-signup-last-name"
        :type="errors.lastName ? 'is-danger' : null">
        <b-input
          id="proxy-signup-last-name"
          :compat-fallthrough="false"
          ref="lastName"
          type="text"
          autocomplete="family-name"
          v-model="lastName"
          :aria-invalid="errors.lastName ? 'true' : null"
          :aria-describedby="
            errors.lastName ? 'proxy-signup-last-name-error' : null
          "></b-input>
        <template #message>
          <span v-if="errors.lastName" id="proxy-signup-last-name-error">{{
            errors.lastName
          }}</span>
        </template>
      </b-field>
      <br />
      <button type="submit" class="button is-light">Submit</button>
    </form>
  </div>
</template>

<script>
  import { usePhenixStore } from '@/store.js';
  import axiosInstance from '@/utils/axios.js';
  import { useErrorNotification } from '@/utils/errorNotif';

  export default {
    props: {
      username: {
        type: String,
        default: '',
      },
    },

    created() {
      if (!this.username) {
        this.$router.replace('/');
      }
    },

    methods: {
      onSubmit() {
        this.errors = {
          firstName: this.firstName ? null : 'You must include a first name',
          lastName: this.lastName ? null : 'You must include a last name',
        };

        if (this.errors.firstName || this.errors.lastName) {
          (this.errors.firstName
            ? this.$refs.firstName
            : this.$refs.lastName
          )?.focus();
          return;
        }

        let params = {
          username: this.username,
          first_name: this.firstName,
          last_name: this.lastName,
        };

        axiosInstance
          .post('signup', params)
          .then((response) => {
            const store = usePhenixStore();
            store.login(response.data, false);
          })
          .catch((err) => {
            useErrorNotification(err);
          });
      },
    },

    data() {
      return {
        firstName: null,
        lastName: null,
        errors: { firstName: null, lastName: null },
      };
    },
  };
</script>

<!-- This styling is used for the sign up form. -->
<style scoped>
  .signup-form {
    width: 400px;
    max-width: calc(100% - 32px);
    margin: 30px auto;
    border: 1px solid #eee;
    padding: 20px;
    box-shadow: 0 2px 3px #ccc;
  }

  .signup-form :deep(.label) {
    color: whitesmoke;
  }

  /* Buefy fields keep their message slot; it shows only with an error. */
  .signup-form :deep(.help:empty) {
    display: none;
  }
</style>
