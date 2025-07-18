<script setup>
  import { RouterView } from 'vue-router';
  import {
    connectWebsocket,
    disconnectWebsocket,
    isWsConnected,
  } from './utils/websocket';
  import AppHeader from '@/components/AppHeader.vue';
  import AppFooter from '@/components/AppFooter.vue';
  import { onUnmounted, onMounted } from 'vue';
  import axios from 'axios';
  import { usePhenixStore } from '@/store';
  import { storeToRefs } from 'pinia';
  import { watch } from 'vue';

  import { TimeoutTool } from '@/utils/timeout.js';

  const store = usePhenixStore();
  const timeout = new TimeoutTool();

  axios
    .get('/features')
    .then((resp) => {
      store.features = resp.data.features;
    })
    .catch((err) => {
      console.log(err);
    });

  onMounted(() => {
    // connect websockets once user has authenticated (or auth disabled)
    if (import.meta.env.VITE_AUTH === 'disabled' || store.auth) {
      connectWebsocket();
      timeout.fetchAndStart();
    } else {
      const { auth } = storeToRefs(store);
      watch(auth, async (newAuth) => {
        if (newAuth && !isWsConnected()) {
          connectWebsocket();
        } else if (!newAuth && isWsConnected()) {
          disconnectWebsocket();
        }

        if (newAuth) {
          timeout.fetchAndStart();
        }
      });
    }
  });

  onUnmounted(() => {
    disconnectWebsocket();
  });
</script>

<template>
  <div id="app" @click="timeout.resetTimer" @keydown="timeout.resetTimer">
    <app-header></app-header>
    <main id="main" class="row container is-fullhd px-4">
      <router-view></router-view>
    </main>
    <app-footer></app-footer>
  </div>
</template>
<style lang="scss" scoped>
  #app {
    display: flex;
    flex-direction: column;
    min-height: 100vh;
  }

  #main {
    width: 100%;
    max-width: 1500px;
    padding-bottom: 20px;
  }
</style>
