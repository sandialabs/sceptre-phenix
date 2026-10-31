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
  import router from '@/router';
  import { usePhenixStore } from '@/store';
  import { storeToRefs } from 'pinia';
  import { watch } from 'vue';

  import { TimeoutTool } from '@/utils/timeout.js';

  const store = usePhenixStore();
  const timeout = new TimeoutTool();

  fetch(router.resolve({ name: 'features' }).href)
    .then((resp) => resp.json())
    .then((data) => {
      store.features = data.features;
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
    <a class="skip-link" href="#main">Skip to main content</a>
    <app-header></app-header>
    <main id="main" class="row container is-fullhd px-4" tabindex="-1">
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

  /* Header links wrap in a narrow window or with enlarged text instead of
     running off the page (WCAG 1.4.4, 1.4.10). */
  #app :deep(.navbar-menu) {
    flex-shrink: 1;
    min-width: 0;
  }

  #app :deep(.navbar-start) {
    flex-wrap: wrap;
    min-width: 0;
  }

  // Visually hidden until it receives keyboard focus.
  .skip-link {
    position: absolute;
    top: -100px;
    left: 8px;
    z-index: 100;
    padding: 8px 12px;
    color: #20252b;
    background: #ffd166;
    border-radius: 4px;
  }

  .skip-link:focus {
    top: 8px;
  }
</style>
