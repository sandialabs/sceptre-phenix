<!-- One noVNC RFB connection. It connects one RFB for each new `connection`
     value (from useVncSession) and disconnects it when the value becomes
     null. The parent gives the RFB events to the session: connect →
     connected(), disconnect → disconnected(), securityfailure →
     securityFailure(reason). Actual scaling at a zoom other than 100 % sizes
     the screen box to the zoomed VM screen, and noVNC fits the screen to the
     box, so the pointer still maps to the VM screen. -->
<script setup>
  import {
    computed,
    markRaw,
    onBeforeUnmount,
    ref,
    shallowRef,
    watch,
  } from 'vue';

  import { loadRfb } from './loadRfb.js';

  const props = defineProps({
    // { url, attempt } from useVncSession, or null for no connection
    connection: { type: Object, default: null },
    // the accessible name, such as "Console of VM web in experiment demo"
    label: { type: String, required: true },
    // RFB sends no keys or pointer events, as for a live tile
    viewOnly: { type: Boolean, default: false },
    // fit: scale to the pane; actual: full size with scrollbars;
    // remote: ask the VM to resize its screen to the pane
    scaling: {
      type: String,
      default: 'fit',
      validator: (value) => ['fit', 'actual', 'remote'].includes(value),
    },
    // the zoom of Actual scaling, 1 for the size of the VM screen
    zoom: { type: Number, default: 1 },
    // the Tight JPEG quality, 0 to 9
    quality: { type: Number, default: 6 },
    // the compression level, 0 to 9
    compression: { type: Number, default: 2 },
  });

  const emit = defineEmits([
    'connect',
    'disconnect',
    'securityfailure',
    'clipboard',
    'bell',
    'desktopname',
  ]);

  const screen = ref(null);
  const rfb = shallowRef(null);
  // each connection makes the RFB of an older one stale
  let generation = 0;
  let detachListeners = () => {};
  // the size of the VM screen, which the canvas size attributes follow
  const frame = ref({ width: 0, height: 0 });
  let frameObserver = null;

  const zoomed = computed(() => props.scaling === 'actual' && props.zoom !== 1);
  const screenStyle = computed(() =>
    zoomed.value
      ? {
          width: `${Math.round(frame.value.width * props.zoom)}px`,
          height: `${Math.round(frame.value.height * props.zoom)}px`,
        }
      : null,
  );

  function followFrame(canvas) {
    frameObserver?.disconnect();
    frameObserver = null;
    if (!canvas) return;
    const read = () =>
      (frame.value = { width: canvas.width, height: canvas.height });
    read();
    frameObserver = new MutationObserver(read);
    frameObserver.observe(canvas, { attributeFilter: ['width', 'height'] });
  }

  function applySettings(target) {
    target.viewOnly = props.viewOnly;
    target.clipViewport = false;
    target.scaleViewport = props.scaling === 'fit' || zoomed.value;
    target.resizeSession = props.scaling === 'remote';
    target.qualityLevel = props.quality;
    target.compressionLevel = props.compression;
  }

  function listen(target) {
    const handlers = {
      connect: () => emit('connect'),
      disconnect: (e) => {
        rfb.value = null;
        detachListeners();
        emit('disconnect', { clean: Boolean(e.detail?.clean) });
      },
      securityfailure: (e) => emit('securityfailure', e.detail?.reason ?? ''),
      clipboard: (e) => emit('clipboard', e.detail?.text ?? ''),
      bell: () => emit('bell'),
      desktopname: (e) => emit('desktopname', e.detail?.name ?? ''),
    };
    for (const [type, handler] of Object.entries(handlers)) {
      target.addEventListener(type, handler);
    }
    detachListeners = () => {
      for (const [type, handler] of Object.entries(handlers)) {
        target.removeEventListener(type, handler);
      }
      detachListeners = () => {};
    };
  }

  // drops the RFB without telling the parent: the session asked for it
  function close() {
    generation += 1;
    const old = rfb.value;
    rfb.value = null;
    detachListeners();
    followFrame(null);
    old?.disconnect();
  }

  async function open(connection) {
    close();
    if (!connection) return;
    const mine = generation;
    try {
      const RFB = await loadRfb();
      if (mine !== generation || !screen.value) return;
      const target = markRaw(
        new RFB(screen.value, connection.url, { shared: true }),
      );
      applySettings(target);
      listen(target);
      rfb.value = target;
      const canvas = screen.value.querySelector('canvas');
      canvas?.setAttribute('aria-label', props.label);
      followFrame(canvas);
    } catch (err) {
      if (mine !== generation) return;
      console.warn('failed to open the VNC console', err);
      emit('disconnect', { clean: false });
    }
  }

  watch(() => props.connection, open, { immediate: true });
  watch(
    () => [
      props.viewOnly,
      props.scaling,
      props.zoom,
      props.quality,
      props.compression,
    ],
    () => rfb.value && applySettings(rfb.value),
  );

  onBeforeUnmount(close);

  /**
   * Sends key events to the VM.
   *
   * @param {[number, string, boolean][]} events keysym, code, down
   */
  function sendKeys(events) {
    for (const [keysym, code, down] of events) {
      rfb.value?.sendKey(keysym, code, down);
    }
  }

  /** Sends Ctrl+Alt+Del to the VM. */
  function sendCtrlAltDel() {
    rfb.value?.sendCtrlAltDel();
  }

  /**
   * Sends text to the clipboard of the VM. In paste-as-keys mode, the
   * minimega shim types it.
   *
   * @param {string} text
   */
  function paste(text) {
    rfb.value?.clipboardPasteFrom(text);
  }

  /**
   * The screen as an image.
   *
   * @param {string} [type] default image/png
   * @returns {Promise<Blob|null>} null without a connection
   */
  function toBlob(type = 'image/png') {
    const target = rfb.value;
    if (!target) return Promise.resolve(null);
    return new Promise((resolve) => target.toBlob(resolve, type));
  }

  /** Gives keyboard input to the console. */
  function focus() {
    rfb.value?.focus({ preventScroll: true });
  }

  /**
   * The size of the VM screen, and the scale at which the pane shows it.
   *
   * @returns {{ width: number, height: number, scale: number|null }}
   */
  function screenSize() {
    const canvas = rfb.value ? screen.value?.querySelector('canvas') : null;
    if (!canvas?.width) return { width: 0, height: 0, scale: null };
    const shown = canvas.getBoundingClientRect().width;
    return {
      width: canvas.width,
      height: canvas.height,
      scale: shown ? shown / canvas.width : null,
    };
  }

  // A keyboard user reaches the console with Tab; RFB listens on its canvas.
  function onFocus(event) {
    if (event.target === event.currentTarget) focus();
  }

  defineExpose({ sendKeys, sendCtrlAltDel, paste, toBlob, focus, screenSize });
</script>

<template>
  <div
    :class="['vnc-console', { 'is-zoomed': zoomed }]"
    role="application"
    :aria-label="label"
    :tabindex="viewOnly ? -1 : 0"
    @focus="onFocus">
    <div ref="screen" class="vnc-screen" :style="screenStyle"></div>
  </div>
</template>

<style scoped>
  .vnc-console {
    position: relative;
    flex: 1 1 auto;
    min-width: 0;
    min-height: 0;
    background: #000;
  }

  .vnc-console:focus-visible {
    outline: 2px solid #62c0d7;
    outline-offset: -2px;
  }

  .vnc-screen {
    position: absolute;
    inset: 0;
  }

  /* the zoomed screen scrolls in the pane, centered while it is smaller */
  .vnc-console.is-zoomed {
    display: flex;
    overflow: auto;
  }

  .is-zoomed .vnc-screen {
    position: relative;
    inset: auto;
    flex: none;
    margin: auto;
  }
</style>
