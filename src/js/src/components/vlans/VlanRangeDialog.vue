<template>
  <form class="modal-card vlan-dialog" @submit.prevent="save">
    <header class="modal-card-head">
      <h2 class="modal-card-title">Set the VLAN range</h2>
    </header>
    <section class="modal-card-body">
      <p class="vlan-dialog-meta">
        Experiment {{ card.name }}, {{ card.running ? 'running' : 'stopped' }},
        {{ aliasCount }}. minimega gives its aliases IDs from this range.
      </p>
      <div class="vlan-range-fields">
        <b-field
          label="Minimum"
          :label-for="`${uid}-min`"
          :type="errors.min ? 'is-danger' : undefined"
          :message="errors.min">
          <input
            :id="`${uid}-min`"
            v-model="min"
            class="input"
            type="number"
            min="1"
            max="4094"
            step="1"
            :disabled="saving" />
        </b-field>
        <b-field
          label="Maximum"
          :label-for="`${uid}-max`"
          :type="errors.max ? 'is-danger' : undefined"
          :message="errors.max">
          <input
            :id="`${uid}-max`"
            v-model="max"
            class="input"
            type="number"
            min="1"
            max="4094"
            step="1"
            :disabled="saving" />
        </b-field>
      </div>
      <b-button
        size="is-small"
        icon-left="wand-magic-sparkles"
        class="vlan-range-suggest"
        :loading="suggesting"
        :disabled="saving"
        @click="suggest"
        >Suggest a free range</b-button
      >
      <div class="vlan-dialog-check" aria-live="polite">
        <p v-if="suggestNote">{{ suggestNote }}</p>
        <p v-if="check.checking">Checking the range in the cluster…</p>
        <p v-else-if="check.failed">
          Could not check the range in the cluster.
        </p>
        <ul v-else-if="check.issues?.length" class="vlan-dialog-issues">
          <li v-for="message in issueMessages" :key="message">
            <b-icon icon="exclamation-triangle" size="is-small" />
            {{ message }}
          </li>
        </ul>
        <p v-else-if="check.issues && changed">
          The range has no issues in the cluster.
        </p>
      </div>
    </section>
    <footer class="modal-card-foot buttons is-right">
      <b-button
        v-if="card.range"
        label="Clear the range"
        type="is-danger"
        class="vlan-range-clear"
        :disabled="saving"
        @click="clear" />
      <b-button label="Cancel" :disabled="saving" @click="$emit('close')" />
      <b-button
        label="Save"
        type="is-success"
        native-type="submit"
        :loading="saving"
        :disabled="!changed || !valid" />
    </footer>
  </form>
</template>

<script>
  import * as vlansApi from '@/api/vlans.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import { pluralWord } from '@/utils/plural.js';
  import { createVlanCheck, saveWithForce } from '@/utils/vlanActions.js';
  import { rangeText, vlanInputError } from '@/utils/vlans.js';

  let dialogs = 0;

  // The dialog that sets or clears an experiment's VLAN range, with a free
  // range to suggest for its alias count, checked against the cluster.
  export default {
    name: 'VlanRangeDialog',
    props: {
      // an experiment card (vlanCards in utils/vlans.js)
      card: { type: Object, required: true },
      // whether to suggest a free range as it opens
      suggestFirst: { type: Boolean, default: false },
      // a range to start with, such as a free block found, or null
      initialRange: { type: Array, default: null },
    },
    // saved(): the server took the range; busy(saving)
    emits: ['close', 'saved', 'busy'],
    data() {
      return {
        uid: `vlan-range-${++dialogs}`,
        min: String((this.initialRange ?? this.card.range)?.[0] ?? ''),
        max: String((this.initialRange ?? this.card.range)?.[1] ?? ''),
        saving: false,
        suggesting: false,
        suggestNote: '',
        check: { checking: false, issues: null, failed: false },
      };
    },
    computed: {
      aliasCount() {
        const n = this.card.aliases.length;
        return `${n} ${pluralWord(n, 'alias', 'aliases')}`;
      },
      errors() {
        const min = vlanInputError(this.min);
        const max = vlanInputError(this.max);
        if (!min && !max && Number(this.min) > Number(this.max)) {
          return { min: 'The minimum must not be more than the maximum.' };
        }
        return { min, max };
      },
      valid() {
        return !this.errors.min && !this.errors.max;
      },
      changed() {
        const [lo, hi] = this.card.range ?? [0, 0];
        return Number(this.min) !== lo || Number(this.max) !== hi;
      },
      issueMessages() {
        return [...new Set((this.check.issues ?? []).map((i) => i.message))];
      },
      // the range typed, for the check
      proposed() {
        if (!this.valid || !this.changed) return null;
        return [Number(this.min), Number(this.max)];
      },
    },
    watch: {
      saving(saving) {
        this.$emit('busy', saving);
      },
      proposed: {
        handler(range) {
          if (!range) {
            this.runCheck.cancel();
            this.check = { checking: false, issues: null, failed: false };
            return;
          }
          const aliases = {};
          for (const a of this.card.aliases) {
            if (a.pinned) aliases[a.alias] = a.pinned;
          }
          this.runCheck({
            experiment: this.card.name,
            aliases,
            min: range[0],
            max: range[1],
          });
        },
        immediate: true,
      },
    },
    beforeCreate() {
      this.runCheck = createVlanCheck((check) => (this.check = check));
    },
    mounted() {
      if (this.suggestFirst) this.suggest();
    },
    beforeUnmount() {
      this.runCheck.cancel();
    },
    methods: {
      // fills in the first free block, outside every range, for the aliases
      async suggest() {
        this.suggesting = true;
        this.suggestNote = '';
        try {
          const { data } = await vlansApi.findFreeVlans({
            count: Math.max(this.card.aliases.length, 1),
            outsideRanges: true,
          });
          this.min = String(data.start);
          this.max = String(data.end);
          this.suggestNote = `Suggested a free range: ${rangeText([data.start, data.end])}.`;
        } catch (err) {
          if (err?.response?.status === 404) {
            this.suggestNote = 'No free range is outside all ranges.';
          } else {
            useErrorNotification(err);
          }
        } finally {
          this.suggesting = false;
        }
      },
      async save() {
        if (this.saving || !this.changed || !this.valid) return;
        const name = this.card.name;
        const min = Number(this.min);
        const max = Number(this.max);
        this.saving = true;
        try {
          const done = await saveWithForce(this.$buefy, name, (force) =>
            vlansApi.setVlanRange(name, min, max, { force }),
          );
          if (done === null) return;
          this.$buefy.toast.open({
            message: escapeHTML(
              `Set the VLAN range of ${name} to ${rangeText([min, max])}`,
            ),
            type: 'is-success',
          });
          this.$emit('saved');
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.saving = false;
        }
      },
      async clear() {
        const name = this.card.name;
        this.saving = true;
        try {
          await vlansApi.clearVlanRange(name);
          this.$buefy.toast.open({
            message: escapeHTML(`Cleared the VLAN range of ${name}`),
            type: 'is-success',
          });
          this.$emit('saved');
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.saving = false;
        }
      },
    },
  };
</script>

<style scoped>
  .vlan-dialog {
    width: min(32rem, 95vw);
  }

  .vlan-range-fields {
    display: flex;
    gap: 1rem;
  }

  .vlan-range-suggest {
    margin-bottom: 0.75rem;
  }

  .content ul.vlan-dialog-issues {
    margin: 0;
    list-style: none;
  }

  /* one column; the global ul rule makes two */
  ul.vlan-dialog-issues {
    columns: auto;
  }
</style>
