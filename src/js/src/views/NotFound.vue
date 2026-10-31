<!-- What an address that matches no page shows, with a way back. -->
<template>
  <section class="section has-text-centered">
    <h1 class="title">Page not found</h1>
    <p>
      There is no page at <code>{{ $route.fullPath }}</code
      >.
    </p>
    <p class="mt-4">
      <router-link :to="{ name: 'home' }">Go to the home page</router-link>
    </p>
  </section>
</template>

<style scoped>
  code {
    color: inherit;
    background-color: rgba(255, 255, 255, 0.1);
  }
</style>
