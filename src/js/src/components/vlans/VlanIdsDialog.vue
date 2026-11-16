<template>
  <form class="modal-card vlan-dialog" @submit.prevent="save">
    <header class="modal-card-head">
      <h2 class="modal-card-title">{{ title }}</h2>
    </header>
    <section class="modal-card-body">
      <p class="vlan-dialog-meta">
        Experiment {{ card.name }}, {{ card.running ? 'running' : 'stopped' }}.
        Type 0 to clear a pin, so that minimega picks the ID at start.
      </p>
      <table class="vlan-dialog-table">
        <tbody>
          <tr v-for="(row, index) in rows" :key="row.alias">
            <th scope="row">
              <label :for="`${uid}-${index}`">{{ row.alias }}</label>
            </th>
            <td>
              <input
                :id="`${uid}-${index}`"
                v-model="row.value"
                class="input is-small"
                type="number"
                min="0"
                max="4094"
                step="1"
                :aria-invalid="errors[index] ? 'true' : undefined"
                :aria-describedby="`${uid}-${index}-help`"
                :disabled="saving" />
            </td>
            <td :id="`${uid}-${index}-help`" class="vlan-dialog-help">
              <span v-if="errors[index]" class="vlan-dialog-error">{{
                errors[index]
              }}</span>
              <template v-else-if="String(row.was) !== String(row.value).trim()"
                >Was {{ row.was || 'not pinned' }}.</template
              >
            </td>
          </tr>
        </tbody>
      </table>
      <div class="vlan-dialog-check" aria-live="polite">
        <p v-if="check.checking">Checking the IDs in the cluster…</p>
        <p v-else-if="check.failed">Could not check the IDs in the cluster.</p>
        <ul v-else-if="check.issues?.length" class="vlan-dialog-issues">
          <li v-for="message in issueMessages" :key="message">
            <b-icon icon="exclamation-triangle" size="is-small" />
            {{ message }}
          </li>
        </ul>
        <p v-else-if="check.issues && changed.length">{{ okText }}</p>
      </div>
    </section>
    <footer class="modal-card-foot buttons is-right">
      <b-button label="Cancel" :disabled="saving" @click="$emit('close')" />
      <b-button
        label="Save"
        type="is-success"
        native-type="submit"
        :loading="saving"
        :disabled="!changed.length || errors.some(Boolean)" />
    </footer>
  </form>
</template>

<script>
  import * as vlansApi from '@/api/vlans.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import { pluralWord } from '@/utils/plural.js';
  import { createVlanCheck, saveWithForce } from '@/utils/vlanActions.js';
  import { vlanInputError } from '@/utils/vlans.js';

  let dialogs = 0;

  // The dialog that changes the pinned VLAN IDs of an experiment's aliases.
  // It checks the new IDs against the cluster as they are typed.
  export default {
    name: 'VlanIdsDialog',
    props: {
      // an experiment card (vlanCards in utils/vlans.js)
      card: { type: Object, required: true },
      // the one alias to change, or '' for all of them
      alias: { type: String, default: '' },
      // IDs to start with, by alias, such as a suggested free ID
      values: { type: Object, default: () => ({}) },
    },
    // saved(): the server took all the new IDs; busy(saving)
    emits: ['close', 'saved', 'busy'],
    data() {
      const aliases = this.alias
        ? this.card.aliases.filter((a) => a.alias === this.alias)
        : this.card.aliases;
      return {
        uid: `vlan-ids-${++dialogs}`,
        saving: false,
        check: { checking: false, issues: null, failed: false },
        rows: aliases.map((a) => ({
          alias: a.alias,
          was: a.pinned,
          value: String(this.values[a.alias] ?? a.pinned),
        })),
      };
    },
    computed: {
      title() {
        return this.alias
          ? `Change the VLAN ID of ${this.alias}`
          : `Edit the VLAN IDs of ${this.card.name}`;
      },
      errors() {
        return this.rows.map((row) =>
          vlanInputError(row.value, { allowZero: true }),
        );
      },
      changed() {
        return this.rows.filter(
          (row) => String(row.was) !== String(row.value).trim(),
        );
      },
      // every alias's pin as it would be saved
      proposed() {
        const aliases = {};
        for (const a of this.card.aliases) {
          if (a.pinned) aliases[a.alias] = a.pinned;
        }
        for (const row of this.rows) {
          const id = Number(row.value);
          if (id > 0) aliases[row.alias] = id;
          else delete aliases[row.alias];
        }
        return aliases;
      },
      issueMessages() {
        return [...new Set((this.check.issues ?? []).map((i) => i.message))];
      },
      okText() {
        const ids = this.changed
          .map((row) => Number(row.value))
          .filter(Boolean);
        if (ids.length === 1) return `${ids[0]} is free in the cluster.`;
        return 'The new IDs have no issues in the cluster.';
      },
    },
    watch: {
      saving(saving) {
        this.$emit('busy', saving);
      },
      proposed: {
        handler() {
          if (!this.changed.length || this.errors.some(Boolean)) {
            this.runCheck.cancel();
            this.check = { checking: false, issues: null, failed: false };
            return;
          }
          this.runCheck({
            experiment: this.card.name,
            aliases: this.proposed,
            min: this.card.range?.[0] ?? 0,
            max: this.card.range?.[1] ?? 0,
          });
        },
        immediate: true,
      },
    },
    beforeCreate() {
      this.runCheck = createVlanCheck((check) => (this.check = check));
    },
    beforeUnmount() {
      this.runCheck.cancel();
    },
    methods: {
      async save() {
        if (this.saving || !this.changed.length || this.errors.some(Boolean)) {
          return;
        }
        const name = this.card.name;
        this.saving = true;
        let saved = 0;
        let all = false;
        try {
          for (const row of [...this.changed]) {
            const id = Number(row.value);
            const done = await saveWithForce(this.$buefy, name, (force) =>
              id
                ? vlansApi.pinVlanAlias(name, row.alias, id, { force })
                : vlansApi.unpinVlanAlias(name, row.alias),
            );
            if (done === null) break;
            row.was = id;
            saved += 1;
          }
          all = !this.changed.length;
        } catch (err) {
          useErrorNotification(err);
        } finally {
          this.saving = false;
        }
        if (!saved) return;
        this.$buefy.toast.open({
          message: escapeHTML(
            `Saved the VLAN IDs of ${saved} ${pluralWord(saved, 'alias', 'aliases')} of ${name}`,
          ),
          type: 'is-success',
        });
        // the dialog stays open with the IDs that did not save
        if (all) this.$emit('saved');
      },
    },
  };
</script>

<style scoped>
  .vlan-dialog {
    width: min(36rem, 95vw);
  }

  .vlan-dialog-table th,
  .vlan-dialog-table td {
    padding: 0.25rem 0.5rem;
    vertical-align: middle;
    border: 0;
  }

  .vlan-dialog-table input {
    width: 7rem;
  }

  .vlan-dialog-help {
    font-size: 0.85rem;
  }

  .vlan-dialog-error {
    font-weight: 700;
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
