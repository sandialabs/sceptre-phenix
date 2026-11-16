<template>
  <section class="vlan-finder" :aria-labelledby="`${uid}-title`">
    <h2 :id="`${uid}-title`" class="vlan-finder-title">Find free IDs</h2>
    <form class="vlan-finder-form" @submit.prevent="find">
      <b-field
        label="IDs in a row"
        :label-for="`${uid}-count`"
        :type="countError ? 'is-danger' : undefined"
        :message="countError">
        <input
          :id="`${uid}-count`"
          v-model="count"
          class="input is-small"
          type="number"
          min="1"
          max="4094"
          step="1" />
      </b-field>
      <div
        class="vlan-finder-mode"
        role="radiogroup"
        aria-label="Where to look">
        <b-radio v-model="mode" native-value="any" size="is-small"
          >Any free</b-radio
        >
        <b-radio v-model="mode" native-value="outside" size="is-small"
          >Outside all ranges</b-radio
        >
      </div>
      <b-button
        native-type="submit"
        type="is-success"
        size="is-small"
        icon-left="wand-magic-sparkles"
        :loading="finding"
        :disabled="!!countError"
        >Find {{ countLabel }}</b-button
      >
      <b-button size="is-small" @click="$emit('close')">Close</b-button>
    </form>
    <div class="vlan-finder-result" aria-live="polite">
      <template v-if="block">
        <p>
          <b>Free block found:</b> IDs
          {{ rangeText([block.start, block.end]) }}.
          <template v-if="near.length">
            The block is inside the range of {{ nearText }}. Use it only for
            {{ near.length === 1 ? 'that experiment' : 'those experiments' }},
            or choose "Outside all ranges".
          </template>
        </p>
        <div v-if="targets.length" class="vlan-finder-set">
          <b-select
            v-model="target"
            size="is-small"
            aria-label="Experiment to give the range">
            <option v-for="name in targets" :key="name" :value="name">
              {{ name }}
            </option>
          </b-select>
          <b-button size="is-small" :disabled="!target" @click="setRange"
            >Set as the range of {{ target || 'an experiment' }}…</b-button
          >
        </div>
      </template>
      <p v-else-if="notFound">No block of {{ countLabel }} is free.</p>
    </div>
  </section>
</template>

<script>
  import * as vlansApi from '@/api/vlans.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import { listText, plural } from '@/utils/plural.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { rangeText, rangesOverlapping } from '@/utils/vlans.js';

  let finders = 0;

  // Finds a block of free IDs in a row through the server, for the ID space
  // view to outline, and offers it as the range of a stopped experiment.
  export default {
    name: 'VlanFreeFinder',
    props: {
      // the page data (normalizeVlans)
      data: { type: Object, required: true },
    },
    // found({ start, end } | null); set-range(name, [start, end])
    emits: ['close', 'found', 'set-range'],
    setup() {
      return { rangeText };
    },
    data() {
      return {
        uid: `vlan-finder-${++finders}`,
        count: '16',
        mode: 'any',
        finding: false,
        block: null,
        notFound: false,
        target: '',
      };
    },
    computed: {
      countError() {
        const n = Number(this.count);
        return Number.isInteger(n) && n >= 1 && n <= 4094
          ? ''
          : 'Type a number from 1 to 4094.';
      },
      countLabel() {
        return plural(Number(this.count) || 0, 'free ID');
      },
      near() {
        if (!this.block) return [];
        return rangesOverlapping(this.data, this.block.start, this.block.end);
      },
      nearText() {
        return listText(this.near.map((exp) => exp.name));
      },
      // the stopped experiments the role may give a range
      targets() {
        return this.data.experiments
          .filter(
            (exp) =>
              !exp.running && roleAllowed('experiments', 'patch', exp.name),
          )
          .map((exp) => exp.name)
          .sort();
      },
    },
    methods: {
      async find() {
        if (this.countError || this.finding) return;
        this.finding = true;
        this.notFound = false;
        try {
          const { data } = await vlansApi.findFreeVlans({
            count: Number(this.count),
            outsideRanges: this.mode === 'outside',
          });
          this.block = { start: data.start, end: data.end };
          this.target = this.target || this.targets[0] || '';
        } catch (err) {
          this.block = null;
          if (err?.response?.status === 404) this.notFound = true;
          else useErrorNotification(err);
        } finally {
          this.finding = false;
          this.$emit('found', this.block);
        }
      },
      setRange() {
        this.$emit('set-range', this.target, [
          this.block.start,
          this.block.end,
        ]);
      },
    },
  };
</script>

<style scoped>
  .vlan-finder {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 0.75rem 1rem;
    background-color: #3d3d3d;
    border: 1px solid #686868;
    border-radius: 8px;
  }

  .content h2.vlan-finder-title {
    margin: 0;
    font-size: 1rem;
  }

  .vlan-finder-form {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 0.75rem;
  }

  .vlan-finder-form .field {
    margin-bottom: 0;
  }

  .vlan-finder-form input {
    width: 6rem;
  }

  .vlan-finder-mode {
    display: flex;
    gap: 0.75rem;
    padding-bottom: 0.3rem;
  }

  .content .vlan-finder-result p {
    margin: 0;
  }

  .vlan-finder-set {
    display: flex;
    gap: 0.5rem;
    margin-top: 0.5rem;
  }
</style>
