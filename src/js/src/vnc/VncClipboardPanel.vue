<!-- The Clipboard panel of the console top bar (M6a). In paste-as-keys mode,
     a pre-check names the characters that will be typed as "?" or skipped,
     and waits for "Type anyway" or "Cancel". -->
<script setup>
  import { computed, ref, watch } from 'vue';

  import { plural } from '@/utils/plural.js';
  import { CLIPBOARD_MODES } from './connectionStatus.js';
  import {
    BIDIRECTIONAL_HINT_CHARS,
    charLabel,
    typingTime,
    usePastePrecheck,
  } from './pasteCheck.js';

  const props = defineProps({
    // the clipboard.mode of the session JSON, or null before it loads
    mode: { type: String, default: null },
    // the last text that the VM copied (bidirectional mode)
    received: { type: String, default: '' },
    // why the console cannot send text now, or null
    disabledReason: { type: String, default: null },
  });

  const emit = defineEmits(['paste']);

  const text = ref(props.received);
  watch(
    () => props.received,
    (value) => (text.value = value),
  );

  const asKeys = computed(() => props.mode !== 'bidirectional');
  const title = computed(
    () =>
      `Clipboard · ${CLIPBOARD_MODES[props.mode] ?? CLIPBOARD_MODES.pasteAsKeys}`,
  );

  const long = computed(
    () => asKeys.value && [...text.value].length > BIDIRECTIONAL_HINT_CHARS,
  );

  const precheck = usePastePrecheck((value) => emit('paste', value));
  const pending = precheck.pending;

  // the Clipboard API reads only in a secure context
  const canReadClipboard = computed(
    () =>
      globalThis.isSecureContext === true &&
      typeof globalThis.navigator?.clipboard?.readText === 'function',
  );
  const readError = ref('');

  function send(value = text.value) {
    if (props.disabledReason || !value) return;
    if (asKeys.value) precheck.paste(value);
    else emit('paste', value);
  }

  async function pasteFromClipboard() {
    if (!canReadClipboard.value || props.disabledReason) return;
    readError.value = '';
    try {
      const value = await navigator.clipboard.readText();
      text.value = value;
      send(value);
    } catch {
      readError.value = 'The browser did not give the clipboard text';
    }
  }

  // empties the paste box, and drops a paste that waits for an answer
  function clear() {
    if (pending.value) precheck.cancel();
    text.value = '';
    readError.value = '';
  }

  const listText = (chars) => chars.map(charLabel).join(' ');
</script>

<template>
  <div class="vnc-clipboard">
    <h2 class="vnc-panel-title">{{ title }}</h2>
    <p v-if="disabledReason" class="vnc-panel-note">{{ disabledReason }}</p>
    <textarea
      v-model="text"
      class="textarea is-small vnc-clipboard-text"
      rows="3"
      aria-label="Text to send to the VM"></textarea>
    <template v-if="pending">
      <p v-if="pending.asQuestionCount" class="vnc-panel-warning">
        {{ plural(pending.asQuestionCount, 'character') }} will be typed as ?:
        {{ listText(pending.asQuestion) }}
      </p>
      <p v-if="pending.skippedCount" class="vnc-panel-warning">
        {{ plural(pending.skippedCount, 'character') }} will be skipped:
        {{ listText(pending.skipped) }}
      </p>
      <div class="vnc-panel-row">
        <b-button size="is-small" type="is-link" @click="precheck.typeAnyway">
          Type anyway · {{ typingTime(pending.seconds) }}
        </b-button>
        <b-button size="is-small" @click="precheck.cancel">Cancel</b-button>
      </div>
    </template>
    <div class="vnc-panel-row vnc-panel-row--single">
      <b-button
        v-if="!pending"
        size="is-small"
        type="is-link"
        :aria-disabled="disabledReason || !text ? 'true' : undefined"
        @click="send()">
        Send
      </b-button>
      <b-tooltip
        :label="
          canReadClipboard
            ? 'Send the text of your clipboard to the VM'
            : 'Reading your clipboard needs HTTPS'
        "
        type="is-dark"
        position="is-bottom">
        <b-button
          size="is-small"
          :aria-disabled="
            !canReadClipboard || disabledReason ? 'true' : undefined
          "
          @click="pasteFromClipboard">
          Paste from clipboard
        </b-button>
      </b-tooltip>
      <b-tooltip label="Empty the text box" type="is-dark" position="is-bottom">
        <b-button
          size="is-small"
          icon-left="eraser"
          :aria-disabled="!text && !pending ? 'true' : undefined"
          @click="clear">
          Clear
        </b-button>
      </b-tooltip>
    </div>
    <p v-if="readError" class="vnc-panel-warning">{{ readError }}</p>
    <p v-if="long" class="vnc-panel-hint">
      For long text, the bidirectional copy/paste mode is faster.
    </p>
    <p v-if="asKeys" class="vnc-panel-hint">
      ASCII only in this mode. Turn on bidirectional copy/paste for UTF-8.
    </p>
  </div>
</template>
