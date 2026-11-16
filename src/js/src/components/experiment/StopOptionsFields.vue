<!--
The options of an experiment stop in the stop and restart dialogs, each with a
help line. v-model is the form (see STOP_DEFAULTS).
-->
<template>
  <fieldset class="options">
    <legend class="label">{{ legend }}</legend>
    <div v-for="option in options" :key="option.key">
      <!-- Buefy's checkbox markup and keys (Enter and Space toggle, Enter does
           not submit), so that the input names its help line -->
      <label
        class="b-checkbox checkbox"
        @click="$event.currentTarget.querySelector('input').focus()"
        @keydown.prevent.enter="$event.currentTarget.click()"
        @keydown.prevent.space="$event.currentTarget.click()">
        <input
          type="checkbox"
          :checked="modelValue[option.key]"
          :aria-describedby="`${uid}-${option.key}-help`"
          @click.stop
          @change="set(option.key, $event.target.checked)" />
        <span class="check" />
        <span class="control-label">{{ option.label }}</span>
      </label>
      <p :id="`${uid}-${option.key}-help`" class="help stop-help">
        {{ option.help }}
      </p>
    </div>
  </fieldset>
</template>

<script>
  let fields = 0;

  export default {
    name: 'StopOptionsFields',
    props: {
      modelValue: { type: Object, required: true },
      legend: { type: String, default: 'Options' },
    },
    emits: ['update:modelValue'],
    data() {
      return {
        uid: `stop-options-${++fields}`,
        options: [
          {
            key: 'keepInjectionSnapshots',
            label: 'Keep the injection snapshots',
            help: 'phēnix deletes them after a stop otherwise.',
          },
          {
            key: 'force',
            label: 'Force the stop',
            help:
              'Marks the experiment stopped even if minimega cannot kill ' +
              'its VMs. You must then clear those VMs in minimega by hand.',
          },
        ],
      };
    },
    methods: {
      set(key, value) {
        this.$emit('update:modelValue', { ...this.modelValue, [key]: value });
      },
    },
  };
</script>

<style scoped>
  /* under the label, not the box */
  .stop-help {
    margin-top: 0;
    margin-bottom: 0.25rem;
    padding-left: 1.75rem;
  }
</style>
