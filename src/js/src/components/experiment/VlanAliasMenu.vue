<template>
  <b-tooltip
    label="assign VLAN IDs to aliases"
    type="is-light"
    position="is-top"
    :delay="500"
    :active="!open">
    <b-dropdown
      ref="dropdown"
      class="vlan-menu"
      aria-role="dialog"
      position="is-bottom-left"
      :disabled="disabled"
      @active-change="onActive">
      <template #trigger>
        <b-button
          class="vlan-menu-trigger"
          type="is-light"
          icon-left="network-wired"
          icon-right="caret-down"
          aria-label="Assign VLAN IDs to aliases"
          :loading="saving"
          :disabled="disabled"
          >VLAN IDs</b-button
        >
      </template>
      <b-dropdown-item custom :focusable="false" class="vlan-menu-body">
        <form @submit.prevent="save">
          <p v-if="!rows.length" class="vlan-menu-note">
            This experiment has no VLAN aliases
          </p>
          <template v-else>
            <p class="vlan-menu-note">
              Set an ID from 1 to 4094, or 0 to let minimega pick one when the
              experiment starts.
            </p>
            <table class="vlan-menu-table">
              <tbody>
                <tr v-for="(row, index) in rows" :key="row.alias">
                  <th scope="row">{{ row.alias }}</th>
                  <td>
                    <input
                      v-model="row.vlan"
                      class="input is-small"
                      type="number"
                      min="0"
                      max="4094"
                      step="1"
                      :aria-label="`VLAN ID for alias ${row.alias}`"
                      :aria-invalid="errors[index] ? 'true' : undefined"
                      :aria-describedby="
                        errors[index] ? `vlan-error-${index}` : undefined
                      "
                      :disabled="saving" />
                    <p
                      v-if="errors[index]"
                      :id="`vlan-error-${index}`"
                      class="vlan-menu-error">
                      <b-icon icon="exclamation-circle" size="is-small" />
                      {{ errors[index] }}
                    </p>
                  </td>
                </tr>
              </tbody>
            </table>
            <p v-if="clusterLink" class="vlan-menu-note">
              <router-link :to="clusterLink" class="vlan-menu-cluster"
                >See all VLANs in the cluster</router-link
              >
            </p>
            <div class="buttons is-right vlan-menu-buttons">
              <b-button
                size="is-small"
                type="is-success"
                native-type="submit"
                class="vlan-menu-save"
                :loading="saving"
                :disabled="!changed.length || errors.some(Boolean)"
                >Save VLAN IDs</b-button
              >
            </div>
          </template>
        </form>
      </b-dropdown-item>
    </b-dropdown>
  </b-tooltip>
</template>

<script>
  import * as experimentsApi from '@/api/experiments.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { pluralWord } from '@/utils/plural.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { checkBeforeSave } from '@/utils/vlanActions.js';
  import { vlanIdError } from './vmSettings.js';

  // The VLAN IDs button of a stopped experiment and its menu, which lists
  // each VLAN alias with its ID to change in place and saves the IDs changed.
  export default {
    name: 'VlanAliasMenu',
    props: {
      experiment: { type: String, default: '' },
      // the experiment's VLAN aliases, as { alias, vlan }
      vlans: { type: Array, default: () => [] },
      // the experiment's VLAN range, 0 for none, for the conflict check
      min: { type: Number, default: 0 },
      max: { type: Number, default: 0 },
      disabled: { type: Boolean, default: false },
    },
    // (vlans): the aliases, as { alias, vlan }, once the server saved them
    emits: ['saved'],
    data() {
      return { open: false, saving: false, rows: [] };
    },
    computed: {
      // the VLANs page, showing this experiment
      clusterLink() {
        if (!roleAllowed('vlans', 'list', this.experiment)) return null;
        return { name: 'vlans', query: { experiment: this.experiment } };
      },
      errors() {
        return this.rows.map((row) => vlanIdError(row.vlan));
      },
      // the rows whose ID differs from the one saved
      changed() {
        return this.rows.filter(
          (row, index) =>
            String(row.vlan).trim() !== String(this.vlans[index]?.vlan ?? ''),
        );
      },
    },
    // Buefy puts the dialog role on an inner element, which is named here.
    mounted() {
      this.$refs.dropdown.$el
        .querySelector('.dropdown-content')
        ?.setAttribute('aria-label', 'VLAN IDs of the aliases');
    },
    watch: {
      vlans: {
        handler() {
          if (!this.saving) this.resetRows();
        },
        immediate: true,
      },
    },
    methods: {
      // the IDs as saved
      resetRows() {
        this.rows = this.vlans.map(({ alias, vlan }) => ({
          alias,
          vlan: String(vlan ?? 0),
        }));
      },
      // a closed menu drops the changes not saved, so it opens on the IDs
      // saved
      onActive(active) {
        this.open = active;
        if (!active && !this.saving) this.resetRows();
      },
      async save() {
        if (this.saving || !this.changed.length || this.errors.some(Boolean)) {
          return;
        }
        const count = this.changed.length;
        const ids = Object.fromEntries(
          this.changed.map((row) => [row.alias, Number(row.vlan)]),
        );
        this.saving = true;
        // every alias's ID as it would be saved, checked against the cluster
        const aliases = {};
        for (const row of this.rows) {
          if (Number(row.vlan) > 0) aliases[row.alias] = Number(row.vlan);
        }
        const go = await checkBeforeSave(this.$buefy, {
          experiment: this.experiment,
          aliases,
          min: this.min,
          max: this.max,
        });
        if (!go) {
          this.saving = false;
          return;
        }
        try {
          await experimentsApi.updateExperiment(
            this.experiment,
            JSON.stringify(ids),
          );
        } catch (err) {
          useErrorNotification(err);
          return;
        } finally {
          this.saving = false;
        }
        this.$emit(
          'saved',
          this.vlans.map((v) => ({ ...v, vlan: ids[v.alias] ?? v.vlan })),
        );
        this.$buefy.toast.open({
          message: `Saved the VLAN IDs of ${count} ${pluralWord(count, 'alias', 'aliases')}`,
          type: 'is-success',
        });
        if (this.$refs.dropdown?.isActive) this.$refs.dropdown.toggle();
      },
    },
  };
</script>

<style scoped>
  .vlan-menu-body {
    min-width: 18rem;
  }

  .vlan-menu-note {
    margin-bottom: 0.5rem !important;
    white-space: normal;
  }

  .content table.vlan-menu-table,
  .vlan-menu-table {
    margin-bottom: 0.5rem;
  }

  .vlan-menu-table th,
  .vlan-menu-table td {
    padding: 0.2rem 0.4rem;
    vertical-align: middle;
    border: 0;
  }

  .vlan-menu-table input {
    width: 7rem;
  }

  .vlan-menu-error {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.75rem;
    font-weight: 700;
  }
</style>
