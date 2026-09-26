<script setup>
  import { RouterView } from 'vue-router';
  import {
    connectWebsocket,
    disconnectWebsocket,
    isWsConnected,
  } from './utils/websocket';
  import AppHeader from '@/components/AppHeader.vue';
  import AppFooter from '@/components/AppFooter.vue';
  import { computed, onUnmounted, onMounted } from 'vue';
  import { useRoute } from 'vue-router';
  import { usePhenixStore } from '@/store';
  import { storeToRefs } from 'pinia';
  import { watch } from 'vue';

  import { TimeoutTool } from '@/utils/timeout.js';
  import { focusMode } from '@/builder/focusMode.js';

  const store = usePhenixStore();
  const timeout = new TimeoutTool();
  const route = useRoute();

  // Routes such as the Builder Beta editor fill the viewport under the header
  // instead of sitting in the centered, scrolling page container.
  const fullBleed = computed(() => Boolean(route.meta.fullBleed));

  // single-flight: the router's feature guards await the same promise
  store.ensureFeatures().catch((err) => {
    console.error('Unable to load server features.', err);
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
  <div
    id="app"
    :class="{ 'app--full-bleed': fullBleed }"
    @click="timeout.resetTimer"
    @keydown="timeout.resetTimer">
    <!-- The Builder's focus mode hides the navigation bar, so the editor
         fills the window (builder/focusMode.js); v-show keeps its state. -->
    <app-header v-show="!focusMode.on"></app-header>
    <main
      id="main"
      :class="fullBleed ? 'main--full-bleed' : 'row container is-fullhd px-4'">
      <router-view></router-view>
    </main>
    <app-footer v-if="!fullBleed"></app-footer>
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

  #app.app--full-bleed {
    height: 100vh;
    height: 100dvh;
    min-height: 0;
    overflow: hidden;
  }

  .app--full-bleed :deep(.navbar) {
    flex: none;
    margin-bottom: 0 !important;
  }

  /* The page itself never scrolls here, so the expanded mobile menu must. */
  .app--full-bleed :deep(.navbar-menu.is-active) {
    max-height: calc(100dvh - 3.25rem);
    overflow-y: auto;
  }

  /* Nor can it scroll sideways, so links that do not fit on one line, in a
     narrow window or with enlarged text, wrap instead of being cut off
     (WCAG 1.4.4, 1.4.10). */
  .app--full-bleed :deep(.navbar-menu) {
    flex-shrink: 1;
    min-width: 0;
  }

  .app--full-bleed :deep(.navbar-start) {
    flex-wrap: wrap;
    min-width: 0;
  }

  #main.main--full-bleed {
    display: flex;
    flex: 1 1 auto;
    min-height: 0;
    max-width: none;
    padding: 0;
  }
</style>
