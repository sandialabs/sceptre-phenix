<template>
  <article
    :class="[
      'vlan-card',
      card.severity && `is-${card.severity}`,
      { 'is-selected': selected },
    ]"
    :data-card="card.key"
    :aria-labelledby="headingId">
    <header class="vlan-card-head">
      <h3 :id="headingId" class="vlan-card-name">
        <button
          type="button"
          class="plain-button vlan-card-select"
          :aria-pressed="selected ? 'true' : 'false'"
          :title="selectTitle"
          @click="$emit('select', card.key)">
          {{ card.name }}
        </button>
      </h3>
      <span :class="['tag', status.tag]">{{ status.text }}</span>
      <span
        v-if="card.severity"
        :class="['tag', severityTag[card.severity]]"
        class="vlan-card-worst"
        >{{ severityText[card.severity] }}</span
      >
    </header>

    <div
      v-if="card.range"
      class="vlan-mini-bar"
      aria-hidden="true"
      :title="`Range ${rangeLabel}`">
      <span class="vlan-mini-range" :style="barStyle(card.range)"></span>
      <span
        v-for="id in card.ids"
        :key="id"
        class="vlan-mini-id"
        :style="{ left: `${pct(id)}%` }"></span>
    </div>
    <p class="vlan-card-meta">{{ meta }}</p>

    <span
      v-if="card.kind === 'blacklist' && !removable"
      :id="`${headingId}-no-removal`"
      class="is-sr-only"
      >minimega cannot remove IDs from its blacklist</span
    >
    <ul v-if="card.kind === 'blacklist'" class="vlan-alias-list">
      <li v-if="!card.ids.length" class="vlan-card-none">No IDs</li>
      <li v-for="id in card.ids" :key="id" class="vlan-alias">
        <button
          v-if="validVlan(id)"
          type="button"
          class="plain-button link-button vlan-id"
          :aria-label="`Show VLAN ${id}`"
          @click="$emit('open', id)">
          {{ id }}
        </button>
        <span v-else class="vlan-blacklist-invalid"
          >{{ id }} <em>(not a VLAN ID)</em></span
        >
        <b-tooltip
          v-if="canRemoveFromBlacklist && validVlan(id)"
          :label="
            removable
              ? `Remove ${id} from the blacklist`
              : 'minimega cannot remove IDs from its blacklist'
          "
          type="is-light"
          position="is-left">
          <button
            type="button"
            :class="[
              'button',
              'is-small',
              'is-light',
              'vlan-blacklist-remove',
              { 'is-loading': removing.includes(id) },
            ]"
            :aria-label="`Remove ${id} from the blacklist`"
            :aria-disabled="removable ? undefined : 'true'"
            :aria-describedby="
              removable ? undefined : `${headingId}-no-removal`
            "
            @click="
              removable &&
              !removing.includes(id) &&
              act('blacklist-remove', { id })
            ">
            <b-icon icon="trash" size="is-small" aria-hidden="true" />
          </button>
        </b-tooltip>
      </li>
    </ul>

    <ul v-else class="vlan-alias-list">
      <li v-if="!card.aliases.length" class="vlan-card-none">No aliases</li>
      <li
        v-for="a in card.aliases"
        :key="a.alias"
        :class="[
          'vlan-alias',
          a.pinned ? 'vlan-alias-pinned' : 'vlan-alias-unpinned',
          aliasSeverity(a) && `is-${aliasSeverity(a)}`,
        ]">
        <span class="vlan-alias-name"
          ><b-icon
            v-if="a.pinned"
            icon="thumbtack"
            size="is-small"
            class="vlan-pin-icon"
            aria-hidden="true" />{{ a.alias }}</span
        >
        <span class="vlan-alias-ids">
          <template v-if="!a.pinned && !a.live">
            <span class="vlan-card-none">Not pinned</span>
          </template>
          <template v-else>
            <template v-for="(id, i) in idsOf(a)" :key="id">
              <span v-if="i" aria-hidden="true"> → </span>
              <button
                type="button"
                class="plain-button link-button vlan-id"
                :aria-label="`Show VLAN ${id}`"
                @click="$emit('open', id)">
                {{ id }}
              </button>
            </template>
          </template>
        </span>
        <span class="vlan-sources">
          <span
            v-if="card.kind === 'experiment'"
            :class="['vlan-source', { 'is-set': !!a.pinned }]"
            :title="a.pinned ? `phēnix pins ${a.pinned}` : 'Not pinned'"
            >phēnix<span class="is-sr-only">{{
              a.pinned ? ` pins ${a.pinned}` : ' does not pin it'
            }}</span></span
          >
          <span
            :class="['vlan-source', { 'is-set': !!a.live }]"
            :title="`minimega ${liveText(a)}`"
            >minimega<span class="is-sr-only">{{
              ` ${liveText(a)}`
            }}</span></span
          >
        </span>
        <span v-if="worst(a) || a.messages.length" class="vlan-alias-note">
          <span
            v-if="worst(a)"
            :class="['tag', 'vlan-issue-tag', severityTag[aliasSeverity(a)]]"
            >{{ issueTag(worst(a)) }}</span
          >
          <span
            v-for="message in a.messages"
            :key="message"
            class="vlan-alias-message"
            >{{ message }}</span
          >
        </span>
      </li>
    </ul>

    <p v-if="card.kind === 'blacklist'" class="vlan-card-busy" role="status">
      <template v-for="text in busyTexts" :key="text">
        <span class="vlan-card-busy-item">
          <b-icon
            icon="spinner"
            size="is-small"
            custom-class="fa-spin"
            aria-hidden="true" />
          <span>{{ text }}</span>
        </span>
      </template>
    </p>

    <p v-for="message in card.messages" :key="message" class="vlan-card-note">
      {{ message }}
    </p>

    <div v-if="actions.length || openRoute" class="vlan-card-actions">
      <template v-for="(action, index) in actions" :key="action.name">
        <b-tooltip
          v-if="action.disabledTip || action.icon"
          :label="actionTip(action)"
          type="is-light"
          position="is-top"
          :delay="action.disabledTip ? null : 500"
          multilined>
          <button
            type="button"
            :class="['button', 'is-small', action.type ?? 'is-light']"
            :data-action="action.disabledTip ? undefined : action.name"
            :aria-label="action.icon ? action.label : undefined"
            :aria-disabled="action.disabledTip ? 'true' : undefined"
            :aria-describedby="
              action.disabledTip || action.description
                ? `${headingId}-why-${index}`
                : undefined
            "
            @click="!action.disabledTip && act(action.name, action.args)">
            <b-icon
              v-if="action.icon"
              :icon="action.icon"
              size="is-small"
              aria-hidden="true" />
            <template v-else>{{ action.label }}</template>
          </button>
          <span
            v-if="action.disabledTip || action.description"
            :id="`${headingId}-why-${index}`"
            class="is-sr-only"
            >{{ action.disabledTip ?? action.description }}</span
          >
        </b-tooltip>
        <button
          v-else
          type="button"
          :class="[
            'button',
            'is-small',
            action.type ?? 'is-light',
            { 'is-loading': action.loading },
          ]"
          :data-action="action.name"
          @click="!action.loading && act(action.name, action.args)">
          {{ action.label }}
        </button>
      </template>
      <b-tooltip
        v-if="openRoute"
        label="Open the experiment"
        type="is-light"
        position="is-top"
        :delay="500">
        <router-link
          :to="openRoute"
          class="button is-small is-light"
          :aria-label="`Open the experiment ${card.name}`">
          <b-icon icon="flask" size="is-small" aria-hidden="true" />
        </router-link>
      </b-tooltip>
    </div>
  </article>
</template>

<script>
  import { plural, pluralWord } from '@/utils/plural.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import {
    ISSUE_KINDS,
    MAX_VLAN,
    RUNNING_TIP,
    SEVERITY_TAG,
    rangeText,
    validVlan,
    worstKind,
    worstSeverity,
  } from '@/utils/vlans.js';

  const PIN_LIVE_TIP =
    'Pin live IDs: copy the VLAN IDs that minimega allocated to this ' +
    'running experiment into its spec, so that the next start gives the ' +
    'same IDs';

  // the IDs of the busy entries for op, such as 42 for 'add:42'
  const busyIds = (busy, op) =>
    busy
      .filter((key) => key.startsWith(`${op}:`))
      .map((key) => Number(key.slice(op.length + 1)));

  const SEVERITY_TEXT = {
    conflict: 'Conflict',
    difference: 'Difference',
    warning: 'Warning',
  };

  // One card of the By experiment view: an experiment, a minimega namespace
  // that no experiment owns, or the blacklist, with the fixes for its issues.
  export default {
    name: 'VlanCard',
    props: {
      // a card from vlanCards (utils/vlans.js)
      card: { type: Object, required: true },
      selected: { type: Boolean, default: false },
      // whether minimega can remove IDs from its blacklist
      removable: { type: Boolean, default: false },
      // whether minimega answered; when not, its IDs are not known
      minimegaOk: { type: Boolean, default: true },
      // a free ID to offer each conflicting alias, by alias
      suggestions: { type: Object, default: () => ({}) },
      // the blacklist changes in progress, such as 'add:42' and 'remove:7'
      busy: { type: Array, default: () => [] },
    },
    // select(key); open(id): show an ID's details; act({ name, card, ... })
    emits: ['select', 'open', 'act'],
    setup() {
      return { severityTag: SEVERITY_TAG, severityText: SEVERITY_TEXT };
    },
    computed: {
      headingId() {
        return `vlan-card-${this.card.key.replace(/[^\w-]/g, '_')}`;
      },
      selectTitle() {
        return this.selected
          ? 'Clear the selection'
          : 'Select to outline its IDs in the ID space view';
      },
      status() {
        switch (this.card.kind) {
          case 'unowned':
            return { text: 'Unowned', tag: 'is-danger' };
          case 'blacklist':
            return { text: 'minimega', tag: 'is-light' };
          default:
            return this.card.running
              ? { text: 'Running', tag: 'is-success' }
              : { text: 'Stopped', tag: 'is-light' };
        }
      },
      rangeLabel() {
        return this.card.range ? rangeText(this.card.range) : '';
      },
      meta() {
        const card = this.card;
        if (card.kind === 'blacklist') {
          return 'minimega gives these IDs to no alias.';
        }
        if (card.kind === 'unowned') {
          return `minimega namespace with no experiment · ${card.aliases.length} ${pluralWord(card.aliases.length, 'alias', 'aliases')}`;
        }
        const parts = [card.range ? `Range ${this.rangeLabel}` : 'No range'];
        parts.push(
          `${card.aliases.length} ${pluralWord(card.aliases.length, 'alias', 'aliases')}`,
        );
        if (card.running) parts.push(plural(card.vms, 'VM'));
        return parts.join(' · ');
      },
      canPatch() {
        return (
          this.card.kind === 'experiment' &&
          roleAllowed('experiments', 'patch', this.card.name)
        );
      },
      canRemoveFromBlacklist() {
        return roleAllowed('vlans/blacklist', 'delete', '*');
      },
      openRoute() {
        if (this.card.kind !== 'experiment') return null;
        if (!roleAllowed('experiments', 'get', this.card.name)) return null;
        return { name: 'experiment', params: { id: this.card.name } };
      },
      // the IDs that are being added to or removed from the blacklist
      adding() {
        return busyIds(this.busy, 'add');
      },
      removing() {
        return busyIds(this.busy, 'remove');
      },
      busyTexts() {
        return [
          ...this.adding.map((id) => `Adding ${id} to the blacklist…`),
          ...this.removing.map((id) => `Removing ${id} from the blacklist…`),
        ];
      },
      // the card's buttons, each { name, label, type?, args?, disabledTip?,
      // loading? }, and for an icon button its icon, tip and description?
      actions() {
        const card = this.card;
        const list = [];
        if (card.kind === 'blacklist') {
          if (roleAllowed('vlans/blacklist', 'create', '*')) {
            list.push({
              name: 'blacklist-add',
              label: 'Add an ID…',
              loading: this.adding.length > 0,
            });
          }
          return list;
        }
        if (card.kind === 'unowned') {
          if (roleAllowed('vlans/allocations', 'delete', '*')) {
            list.push({
              name: 'release',
              label: `Release ${plural(card.aliases.length, 'ID')}…`,
              type: 'is-danger',
            });
          }
          return list;
        }
        if (!this.canPatch) return list;
        const running = card.running;
        const locked = (item) =>
          running ? { ...item, disabledTip: RUNNING_TIP } : item;
        for (const a of card.aliases) {
          const id = this.suggestions[a.alias];
          if (!id || worstSeverity(a.issues) !== 'conflict') continue;
          list.push(
            locked({
              name: 'change',
              label: `Change ${a.alias} to ${id}…`,
              type: 'is-success',
              args: { alias: a.alias, id },
            }),
          );
        }
        if (card.aliases.length) {
          list.push(
            locked({
              name: 'edit-ids',
              label: 'Edit IDs…',
              icon: 'pencil',
              tip: 'Edit IDs',
            }),
          );
        }
        const overlap = card.issues.includes('range-overlap');
        list.push(
          locked({
            name: 'range',
            label: overlap ? 'Suggest a range…' : 'Set range…',
            icon: 'arrows-left-right',
            tip: overlap ? 'Suggest a range' : 'Set range',
            args: { suggest: overlap },
          }),
        );
        if (
          running &&
          card.aliases.some((a) => a.live && a.live !== a.pinned)
        ) {
          list.push({
            name: 'pin-live',
            label: 'Pin live IDs…',
            icon: 'thumbtack',
            tip: PIN_LIVE_TIP,
            description: PIN_LIVE_TIP,
          });
        }
        return list;
      },
    },
    methods: {
      validVlan,
      // the tooltip of an action: its tip, and why it is disabled
      actionTip(action) {
        if (!action.disabledTip) return action.tip;
        return action.icon
          ? `${action.tip}: ${action.disabledTip}`
          : action.disabledTip;
      },
      // what minimega gives the alias, in words
      liveText(a) {
        if (a.live) return `allocated ${a.live}`;
        return this.minimegaOk ? 'did not allocate it' : 'ID unknown';
      },
      pct(id) {
        return ((id - 1) / (MAX_VLAN - 1)) * 100;
      },
      barStyle([min, max]) {
        return {
          left: `${this.pct(min)}%`,
          width: `max(2px, ${this.pct(max) - this.pct(min)}%)`,
        };
      },
      // the alias's IDs: the pin, then minimega's when it differs
      idsOf(a) {
        if (a.pinned && a.live && a.pinned !== a.live)
          return [a.pinned, a.live];
        return [a.pinned || a.live];
      },
      worst(a) {
        return worstKind(a.issues);
      },
      aliasSeverity(a) {
        return worstSeverity(a.issues);
      },
      issueTag(kind) {
        return ISSUE_KINDS[kind]?.tag ?? kind;
      },
      act(name, args = {}) {
        this.$emit('act', { name, card: this.card, ...args });
      },
    },
  };
</script>

<style scoped>
  .vlan-card {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    min-width: 0;
    padding: 0.75rem 0.9rem;
    background-color: #3d3d3d;
    border: 1px solid #686868;
    border-top: 4px solid #686868;
    border-radius: 8px;
  }

  .vlan-card.is-conflict {
    border-top-color: #ff5c4d;
  }

  .vlan-card.is-difference,
  .vlan-card.is-warning {
    border-top-color: #ffd257;
  }

  .vlan-card.is-selected {
    outline: 3px solid white;
    outline-offset: 2px;
  }

  .vlan-card-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
  }

  .content h3.vlan-card-name {
    flex: 1 1 auto;
    min-width: 0;
    margin: 0;
    font-size: 1.1rem;
    overflow-wrap: anywhere;
  }

  .vlan-card-select:hover {
    text-decoration: underline;
  }

  .vlan-mini-bar {
    position: relative;
    height: 8px;
    border-radius: 4px;
    background-color: #5e5e5e;
  }

  .vlan-mini-range {
    position: absolute;
    top: 0;
    bottom: 0;
    border-radius: 4px;
    background-color: #62c0d7;
  }

  .vlan-mini-id {
    position: absolute;
    top: -2px;
    width: 2px;
    height: 12px;
    background-color: white;
  }

  .content p.vlan-card-meta {
    margin: 0;
    font-size: 0.8rem;
    color: #e6e6e6;
  }

  .content ul.vlan-alias-list {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  /* alias, IDs and sources, with the tag and message on a line under them */
  .vlan-alias {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    align-items: center;
    gap: 0.15rem 0.5rem;
    margin: 0 -0.4rem;
    padding: 0.2rem 0.4rem;
    border-radius: 4px;
  }

  /* A pinned alias has a solid cyan edge, a thumbtack and a blue ground
     (white is 9.8:1 on it); an alias with no pin has a dashed gray edge.
     An issue's ground below replaces the blue. */
  .vlan-alias-pinned {
    border-left: 3px solid #62c0d7;
    background-color: #2f4750;
  }

  .vlan-alias-unpinned {
    border-left: 3px dashed #b5b5b5;
  }

  .vlan-pin-icon {
    margin-right: 0.25rem;
    color: #62c0d7;
  }

  /* white text is 13:1 on the red and 8.9:1 on the yellow */
  .vlan-alias.is-conflict {
    background-color: #5c2323;
  }

  .vlan-alias.is-difference {
    background-color: #574a12;
  }

  .vlan-alias-name {
    font-weight: 600;
    overflow-wrap: break-word;
  }

  .vlan-alias-ids {
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .vlan-sources {
    display: inline-flex;
    gap: 0.25rem;
  }

  /* solid when the source has the row, dashed when it does not */
  .vlan-source {
    padding: 0 0.3rem;
    border: 1px dashed #b5b5b5;
    border-radius: 3px;
    font-size: 0.7rem;
    color: #e6e6e6;
  }

  /* the link text color is 5.8:1 on the card; white with an underline is 7.2:1 */
  .plain-button.link-button.vlan-id {
    color: white;
    text-decoration: underline;
  }

  .vlan-source.is-set {
    border-style: solid;
    border-color: white;
    background-color: #575757;
    color: white;
  }

  .vlan-alias-note {
    grid-column: 1 / -1;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.15rem 0.5rem;
  }

  .vlan-alias-message {
    font-size: 0.8rem;
  }

  .vlan-blacklist-invalid {
    color: #e6e6e6;
  }

  .vlan-card-none {
    font-style: italic;
    color: #e6e6e6;
  }

  .content p.vlan-card-note {
    margin: 0;
    font-size: 0.85rem;
    color: #ffe08a;
  }

  /* a live region, so it is there (and empty) while nothing is busy */
  .content p.vlan-card-busy {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    margin: 0;
    font-size: 0.85rem;
    color: #e6e6e6;
  }

  .vlan-card-busy-item {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
  }

  .vlan-card-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    margin-top: auto;
  }

  /* one column; the global ul rule makes two */
  ul.vlan-alias-list {
    columns: auto;
  }
</style>
