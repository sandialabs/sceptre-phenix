<template>
  <div class="content">
    <section class="hero is-light is-bold is-large">
      <div class="hero-body">
        <div class="container" style="text-align: center">
          <h1 class="title">
            Your account is currently disabled. This is most likely due to you
            being a new user. An admin needs to configure a user role for you.
          </h1>
        </div>
      </div>
    </section>
  </div>
</template>

<script>
  export default {
    created () {
      if ( this.$store.getters.role !== 'Disabled' ) {
        this.$router.replace( '/' );
      }
    }
  }
</script>
