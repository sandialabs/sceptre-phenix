<!--
  The buttons the editor header and the drafts landing's header share, in
  one order on both: Commands, the theme, Shortcuts (the editor's only),
  Settings, Help and Focus mode. The view (Builder.vue) puts them in
  both headers, gives them their tooltips and descriptions, which it shows
  on both views, and does what they ask.

  Commands shows the palette's key as key caps, and Shortcuts the key that
  opens the sheet. The theme button shows the theme in use; its name and
  tooltip say what a press changes it to. In a narrower header the theme,
  Shortcuts, Settings and Help show only their icons, as Focus mode always
  does, and Commands its name without its keys; in a narrow one Commands
  shows only its icon too (see .builder-header__label in builder.css).
  Shortcuts keeps its key beside its icon at every width, so the button
  itself says what opens the sheet. The labels stay as their names.
  Focus mode is named Exit focus mode while it is on, and stays one element,
  so focus stays on it.
-->
<template>
  <!-- The key caps are only a picture of the keys: the description and
       aria-keyshortcuts say them to screen readers. -->
  <button
    type="button"
    class="builder-button builder-header__button"
    :data-testid="`${view}-commands`"
    aria-haspopup="dialog"
    :aria-keyshortcuts="tips.commands.aria"
    :aria-describedby="describedBy('commands')"
    v-on="tipFor('commands')"
    @click="$emit('commands')">
    <builder-icon name="command" :size="14" />
    <span class="builder-header__label">
      Commands
      <builder-keycaps v-if="paletteKey" :spec="paletteKey" />
    </span>
  </button>
  <button
    type="button"
    class="builder-button builder-header__button"
    :data-testid="`${view}-theme`"
    :aria-label="themeButton.name"
    :aria-keyshortcuts="tips.theme.aria"
    :aria-describedby="describedBy('theme')"
    v-on="tipFor('theme')"
    @click="$emit('cycle-theme')">
    <builder-icon :name="themeButton.icon" :size="14" />
    <span class="builder-header__label builder-header__label--wide">
      {{ themeButton.label }}
    </span>
  </button>
  <button
    v-if="shortcuts"
    type="button"
    class="builder-button builder-header__button"
    :data-testid="`${view}-shortcuts`"
    aria-haspopup="dialog"
    :aria-keyshortcuts="tips.shortcuts.aria"
    :aria-describedby="describedBy('shortcuts')"
    v-on="tipFor('shortcuts')"
    @click="$emit('shortcuts')">
    <builder-icon name="keyboard" :size="14" />
    <span class="builder-header__label builder-header__label--wide">
      Shortcuts
    </span>
    <!-- Outside the label, so it stays when the header hides its labels. -->
    <builder-keycaps v-if="shortcutsKey" :spec="shortcutsKey" />
  </button>
  <button
    type="button"
    class="builder-button builder-header__button"
    :data-testid="`${view}-settings`"
    aria-haspopup="dialog"
    :aria-keyshortcuts="tips.settings.aria"
    :aria-describedby="describedBy('settings')"
    v-on="tipFor('settings')"
    @click="$emit('settings')">
    <builder-icon name="settings" :size="14" />
    <span class="builder-header__label builder-header__label--wide">
      Settings
    </span>
  </button>
  <a
    class="builder-button builder-header__button builder-help-link"
    :href="HELP_URL"
    target="_blank"
    rel="noopener"
    :data-testid="`${view}-help`"
    v-on="tipFor('help')">
    <builder-icon name="help" :size="14" />
    <span class="builder-header__label builder-header__label--wide">
      Help
      <span class="builder-visually-hidden">(opens in a new tab)</span>
    </span>
  </a>
  <button
    type="button"
    class="builder-button builder-header__button"
    :data-testid="`${view}-focus-mode`"
    :aria-keyshortcuts="tips.focus.aria"
    :aria-describedby="describedBy('focus')"
    v-on="tipFor('focus')"
    @click="$emit('focus-mode')">
    <builder-icon :name="focusMode.on ? 'focus-exit' : 'focus'" :size="14" />
    <span class="builder-visually-hidden">
      {{ focusMode.on ? 'Exit focus mode' : 'Focus mode' }}
    </span>
  </button>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';
  import BuilderKeycaps from './BuilderKeycaps.vue';

  import { commandKeys } from '@/builder/commands.js';
  import { focusMode } from '@/builder/focusMode.js';
  import { HELP_URL } from '@/builder/help.js';

  const props = defineProps({
    // Which header: 'editor' or 'drafts', for the test ids.
    view: { type: String, required: true },
    // Whether to show Shortcuts.
    shortcuts: { type: Boolean, default: false },
    // Per button: the tooltip's text, the description screen readers get
    // in its place (shown as header-tip-<key>), and aria-keyshortcuts.
    tips: { type: Object, required: true },
    // (key) the tooltip's event handlers for a button.
    tipFor: { type: Function, required: true },
    // The theme button's icon, label and name.
    themeButton: { type: Object, required: true },
  });

  defineEmits([
    'commands',
    'cycle-theme',
    'shortcuts',
    'settings',
    'focus-mode',
  ]);

  // The first key of the command palette, shown on its button.
  const paletteKey = computed(() => commandKeys('palette.open')[0] || '');
  // The first key of the shortcut sheet, shown on its button: none while
  // single-key shortcuts are off, unless the user gave it another key.
  const shortcutsKey = computed(() => commandKeys('shortcuts.open')[0] || '');

  function describedBy(key) {
    return props.tips[key].description ? `header-tip-${key}` : undefined;
  }
</script>
