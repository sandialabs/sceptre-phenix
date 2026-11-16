<template>
  <aside
    class="vlan-details vlan-range-details"
    :aria-labelledby="`${uid}-title`">
    <header class="vlan-details-head">
      <h2
        :id="`${uid}-title`"
        ref="title"
        class="vlan-details-title"
        tabindex="-1">
        VLANs {{ rangeText(range) }}
      </h2>
      <button
        type="button"
        class="delete vlan-details-close"
        aria-label="Close the VLAN details"
        @click="$emit('close')"></button>
    </header>

    <p class="vlan-range-summary">{{ summary }}</p>

    <dl v-if="ranges.length" class="vlan-details-list">
      <dt>Range</dt>
      <dd v-for="exp in ranges" :key="exp.name">
        {{ exp.name }} {{ rangeText([exp.min, exp.max]) }}
      </dd>
    </dl>

    <p v-if="!entries.length" class="vlan-details-none">
      No experiment or minimega namespace uses these IDs.
    </p>
    <ul v-else class="vlan-range-ids">
      <li
        v-for="entry in entries.slice(0, IDS_SHOWN)"
        :key="entry.id"
        class="vlan-range-id">
        <div class="vlan-range-id-head">
          <button
            type="button"
            class="plain-button link-button vlan-id"
            :aria-label="`Show VLAN ${entry.id}`"
            @click="$emit('open', entry.id)">
            VLAN {{ entry.id }}
          </button>
          <span :class="['tag', entry.tag]">{{ entry.label }}</span>
        </div>
        <ul class="vlan-range-lines">
          <li v-for="line in entry.lines" :key="line">{{ line }}</li>
        </ul>
      </li>
      <li v-if="entries.length > IDS_SHOWN" class="vlan-range-more">
        and {{ plural(entries.length - IDS_SHOWN, 'more ID') }} in use
      </li>
    </ul>

    <ul v-if="messages.length" class="vlan-details-issues">
      <li v-for="message in messages" :key="message">
        <b-icon icon="exclamation-triangle" size="is-small" />
        {{ message }}
      </li>
    </ul>
  </aside>
</template>

<script>
  import { plural } from '@/utils/plural.js';
  import {
    STATE_INFO,
    STATE_TAG,
    issueText,
    rangeText,
    rangesOverlapping,
    rowsOf,
  } from '@/utils/vlans.js';

  const IDS_SHOWN = 50;

  let panels = 0;

  // The details of each ID of a range, which a drag or a Shift selection in
  // the ID space view chose: for each ID in use, its state and who holds it
  // and how, from the page data. Each ID opens its own details, which read
  // its VMs from the server.
  export default {
    name: 'VlanRangeDetails',
    props: {
      // the page data (normalizeVlans) and its idStates
      data: { type: Object, required: true },
      states: { type: Uint8Array, required: true },
      // [first, last], both valid IDs
      range: { type: Array, required: true },
    },
    // open(id): show an ID's own details
    emits: ['close', 'open'],
    setup() {
      return { rangeText, plural, IDS_SHOWN };
    },
    data() {
      return { uid: `vlan-range-${++panels}` };
    },
    computed: {
      ids() {
        const [first, last] = this.range;
        return Array.from({ length: last - first + 1 }, (_, i) => first + i);
      },
      // how many IDs of the range are in each state, in words; a state's
      // label can hold a comma, so semicolons part them
      summary() {
        const counts = new Map();
        for (const id of this.ids) {
          const state = this.states[id];
          counts.set(state, (counts.get(state) ?? 0) + 1);
        }
        const parts = [...counts]
          .sort(([a], [b]) => a - b)
          .map(([state, n]) => `${n} ${STATE_INFO[state].label.toLowerCase()}`);
        return `${plural(this.ids.length, 'ID')}: ${parts.join('; ')}.`;
      },
      ranges() {
        const [first, last] = this.range;
        return rangesOverlapping(this.data, first, last);
      },
      blacklist() {
        return new Set(this.data.blacklist ?? []);
      },
      // each ID that a row or the blacklist holds, with what its details
      // panel says of its owners and sources
      entries() {
        const list = [];
        for (const id of this.ids) {
          const rows = rowsOf(this.data, id);
          const blacklisted =
            this.blacklist.has(id) ||
            rows.some((row) => row.states?.includes('blacklisted'));
          if (!rows.length && !blacklisted) continue;
          const lines = [];
          for (const row of rows) {
            if (row.states?.includes('pinned')) {
              lines.push(`phēnix: ${row.experiment} pins ${row.alias}`);
            }
            if (row.states?.includes('allocated')) {
              lines.push(
                `minimega: ${row.namespace}: ${row.alias || 'no alias'}`,
              );
            }
          }
          if (blacklisted)
            lines.push('Blacklist: minimega gives it to no alias');
          const state = this.states[id];
          list.push({
            id,
            label: STATE_INFO[state].label,
            tag: STATE_TAG[state] ?? 'is-light',
            lines: [...new Set(lines)],
          });
        }
        return list;
      },
      messages() {
        const [first, last] = this.range;
        const issues = this.data.issues.filter(
          (issue) => issue.vlan >= first && issue.vlan <= last,
        );
        return [...new Set(issues.map(issueText))];
      },
    },
    watch: {
      range() {
        this.$nextTick(() => this.$refs.title?.focus({ preventScroll: true }));
      },
    },
    mounted() {
      this.$refs.title?.focus({ preventScroll: true });
    },
  };
</script>

<style scoped>
  .vlan-details {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    padding: 0.9rem 1rem;
    background-color: #3d3d3d;
    border: 1px solid #686868;
    border-left: 4px solid #da622d;
    border-radius: 8px;
  }

  .vlan-details-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .content h2.vlan-details-title {
    flex: 1;
    margin: 0;
    font-size: 1.25rem;
  }

  .content p.vlan-range-summary {
    margin: 0;
    font-size: 0.875rem;
  }

  .content dl.vlan-details-list {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.25rem 0.75rem;
    margin: 0;
  }

  .vlan-details-list dt {
    grid-column: 1;
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #e6e6e6;
  }

  .content .vlan-details-list dd {
    grid-column: 2;
    margin: 0;
    overflow-wrap: anywhere;
  }

  .content ul.vlan-range-ids {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    max-height: 60vh;
    margin: 0;
    overflow-y: auto;
    list-style: none;
  }

  .content .vlan-range-id {
    margin: 0;
  }

  .vlan-range-id-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  /* the link text color is 5.8:1 on the panel; white with an underline is 7.2:1 */
  .plain-button.link-button.vlan-id {
    color: white;
    font-weight: 600;
    text-decoration: underline;
  }

  .content ul.vlan-range-lines {
    margin: 0.15rem 0 0 1rem;
    list-style: none;
    font-size: 0.85rem;
    overflow-wrap: anywhere;
  }

  .content ul.vlan-range-lines li {
    margin: 0;
  }

  .vlan-range-more,
  .vlan-details-none {
    font-style: italic;
  }

  .content ul.vlan-details-issues {
    margin: 0;
    list-style: none;
    font-size: 0.85rem;
    color: #ffe08a;
  }

  /* one column; the global ul rule makes two */
  ul.vlan-range-ids,
  ul.vlan-range-lines,
  ul.vlan-details-issues {
    columns: auto;
  }
</style>
