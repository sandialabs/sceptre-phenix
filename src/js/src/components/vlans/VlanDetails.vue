<template>
  <aside class="vlan-details" :aria-labelledby="`${uid}-title`">
    <header class="vlan-details-head">
      <h2
        :id="`${uid}-title`"
        ref="title"
        class="vlan-details-title"
        tabindex="-1">
        VLAN {{ vlan }}
      </h2>
      <span :class="['tag', stateTag]">{{ stateLabel }}</span>
      <button
        type="button"
        class="delete vlan-details-close"
        aria-label="Close the VLAN details"
        @click="$emit('close')"></button>
    </header>

    <p v-if="!rows.length && !blacklisted" class="vlan-details-none">
      No experiment or minimega namespace uses this ID.
    </p>

    <dl class="vlan-details-list">
      <template v-if="pins.length">
        <dt>phēnix</dt>
        <dd v-for="row in pins" :key="`p-${row.experiment}-${row.alias}`">
          {{ row.experiment }} pins {{ row.alias }} = {{ vlan }}
        </dd>
      </template>
      <template v-if="lives.length">
        <dt>minimega</dt>
        <dd v-for="row in lives" :key="`m-${row.namespace}-${row.alias}`">
          {{ row.namespace }}: {{ row.alias || 'no alias' }} = {{ vlan }}
        </dd>
      </template>
      <template v-if="blacklisted">
        <dt>Blacklist</dt>
        <dd>minimega gives this ID to no alias.</dd>
      </template>
      <template v-if="ranges.length">
        <dt>Range</dt>
        <dd v-for="exp in ranges" :key="exp.name">
          {{ exp.name }} {{ rangeText([exp.min, exp.max]) }}
        </dd>
      </template>
      <template v-if="vmCount">
        <dt>VMs</dt>
        <dd>
          {{ vmText }}
          <ul v-if="vms.length" class="vlan-details-vms">
            <li v-for="vm in vms.slice(0, VMS_SHOWN)" :key="vmKey(vm)">
              {{ vm.vm }} ({{ vm.experiment }}, {{ vm.interface
              }}<template v-if="vm.host">, {{ vm.host }}</template
              >)
            </li>
            <li v-if="vms.length > VMS_SHOWN">
              and {{ vms.length - VMS_SHOWN }} more
            </li>
          </ul>
        </dd>
      </template>
      <template v-if="hosts.length">
        <dt>Hosts</dt>
        <dd>{{ hosts.join(', ') }}</dd>
      </template>
    </dl>

    <ul v-if="messages.length" class="vlan-details-issues">
      <li v-for="message in messages" :key="message">
        <b-icon icon="exclamation-triangle" size="is-small" />
        {{ message }}
      </li>
    </ul>

    <div v-if="actions.length" class="vlan-details-actions">
      <template v-for="(action, index) in actions" :key="action.key">
        <b-tooltip
          v-if="action.disabledTip"
          :label="action.disabledTip"
          type="is-light"
          position="is-top"
          multilined>
          <button
            type="button"
            :class="['button', 'is-small', action.type ?? 'is-light']"
            aria-disabled="true"
            :aria-describedby="`${uid}-why-${index}`">
            {{ action.label }}
          </button>
          <span :id="`${uid}-why-${index}`" class="is-sr-only">{{
            action.disabledTip
          }}</span>
        </b-tooltip>
        <button
          v-else
          type="button"
          :class="['button', 'is-small', action.type ?? 'is-light']"
          :data-action="action.name"
          @click="$emit('act', action.event)">
          {{ action.label }}
        </button>
      </template>
    </div>

    <section
      v-for="link in linkGroups"
      :key="link.experiment"
      class="vlan-details-links">
      <h3 class="vlan-details-subtitle">{{ link.experiment }}</h3>
      <div class="buttons">
        <button
          v-if="link.card"
          type="button"
          class="button is-small is-light"
          @click="$emit('show-card', link.card)">
          Show the card
        </button>
        <router-link
          v-for="to in link.routes"
          :key="to.label"
          :to="to.route"
          class="button is-small is-light">
          <b-icon :icon="to.icon" size="is-small" aria-hidden="true" />
          <span>{{ to.label }}</span>
        </router-link>
        <button
          v-if="link.capture"
          type="button"
          class="button is-small is-light"
          data-action="capture"
          @click="$emit('act', link.capture)">
          Capture…
        </button>
      </div>
    </section>
  </aside>
</template>

<script>
  import * as vlansApi from '@/api/vlans.js';
  import { useErrorNotification } from '@/utils/errorNotif.js';
  import { plural } from '@/utils/plural.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import {
    RUNNING_TIP,
    STATE,
    STATE_INFO,
    issueText,
    rangeText,
    rangesHolding,
    rowsOf,
    suggestId,
    unownedRow,
  } from '@/utils/vlans.js';

  const VMS_SHOWN = 10;

  let panels = 0;

  const STATE_TAG = {
    [STATE.CONFLICT]: 'is-danger',
    [STATE.ALLOCATED]: 'is-success',
    [STATE.PINNED]: 'is-warning',
    [STATE.BLACKLISTED]: 'is-light',
  };

  // The details of one VLAN ID, for both views: who holds it and how, its
  // range, its VMs and issues, the fixes the role may make and links to the
  // experiment's pages.
  export default {
    name: 'VlanDetails',
    props: {
      // the page data (normalizeVlans), its idStates and its cards
      data: { type: Object, required: true },
      states: { type: Uint8Array, required: true },
      cards: { type: Array, required: true },
      vlan: { type: Number, required: true },
      // whether minimega can remove IDs from its blacklist
      removable: { type: Boolean, default: false },
    },
    // act({ name, card, ... }) as VlanCard emits; show-card(key)
    emits: ['close', 'act', 'show-card'],
    setup() {
      return { rangeText, VMS_SHOWN };
    },
    data() {
      return { uid: `vlan-details-${++panels}`, details: null };
    },
    computed: {
      rows() {
        return rowsOf(this.data, this.vlan);
      },
      stateLabel() {
        return STATE_INFO[this.states[this.vlan]].label;
      },
      stateTag() {
        return STATE_TAG[this.states[this.vlan]] ?? 'is-light';
      },
      pins() {
        return this.rows.filter((row) => row.states?.includes('pinned'));
      },
      lives() {
        return this.rows.filter((row) => row.states?.includes('allocated'));
      },
      blacklisted() {
        return (
          (this.data.blacklist ?? []).includes(this.vlan) ||
          this.rows.some((row) => row.states?.includes('blacklisted'))
        );
      },
      ranges() {
        return rangesHolding(this.data, this.vlan);
      },
      vms() {
        return this.details?.vms ?? [];
      },
      vmCount() {
        return (
          this.vms.length || this.rows.reduce((n, r) => n + (r.vms ?? 0), 0)
        );
      },
      vmText() {
        return plural(this.vmCount, 'VM');
      },
      hosts() {
        return [...new Set(this.rows.flatMap((row) => row.hosts ?? []))].sort();
      },
      messages() {
        const issues = [
          ...this.data.issues.filter((issue) => issue.vlan === this.vlan),
          ...(this.details?.issues ?? []),
        ];
        return [...new Set(issues.map(issueText))];
      },
      cardsByKey() {
        return new Map(this.cards.map((card) => [card.key, card]));
      },
      // the fixes the role may make here
      actions() {
        const list = [];
        const seen = new Set();
        for (const row of this.pins) {
          const card = this.cardsByKey.get(`experiment:${row.experiment}`);
          if (!card || !roleAllowed('experiments', 'patch', row.experiment)) {
            continue;
          }
          const key = `change-${row.experiment}-${row.alias}`;
          if (seen.has(key)) continue;
          seen.add(key);
          const action = {
            key,
            name: 'change',
            label: `Change ${row.experiment} ${row.alias}…`,
            event: {
              name: 'change',
              card,
              alias: row.alias,
              id: suggestId(this.data, row.experiment) || this.vlan,
            },
          };
          list.push(
            card.running ? { ...action, disabledTip: RUNNING_TIP } : action,
          );
        }
        for (const row of this.rows.filter(unownedRow)) {
          const card = this.cardsByKey.get(`namespace:${row.namespace}`);
          const key = `release-${row.namespace}`;
          if (!card || seen.has(key)) continue;
          if (!roleAllowed('vlans/allocations', 'delete', '*')) continue;
          seen.add(key);
          list.push({
            key,
            name: 'release',
            label: `Release the IDs of ${row.namespace}…`,
            type: 'is-danger',
            event: { name: 'release', card },
          });
        }
        if (this.blacklisted) {
          if (roleAllowed('vlans/blacklist', 'delete', '*')) {
            list.push({
              key: 'blacklist-remove',
              name: 'blacklist-remove',
              label: 'Remove from the blacklist…',
              event: { name: 'blacklist-remove', id: this.vlan },
              ...(!this.removable && {
                disabledTip: 'minimega cannot remove IDs from its blacklist',
              }),
            });
          }
        } else if (roleAllowed('vlans/blacklist', 'create', '*')) {
          list.push({
            key: 'blacklist-add',
            name: 'blacklist-add',
            label: 'Add to the blacklist…',
            event: { name: 'blacklist-add', id: this.vlan },
          });
        }
        return list;
      },
      // for each experiment with the ID: its card and its pages
      linkGroups() {
        const byExp = new Map();
        for (const row of this.rows) {
          if (!row.experiment) continue;
          const group = byExp.get(row.experiment) ?? {
            experiment: row.experiment,
            aliases: new Set(),
            running: !!row.running,
          };
          if (row.alias) group.aliases.add(row.alias);
          byExp.set(row.experiment, group);
        }
        return [...byExp.values()].map((group) => {
          const name = group.experiment;
          const alias = [...group.aliases][0] ?? '';
          const routes = [];
          if (roleAllowed('experiments', 'get', name)) {
            const term = group.running
              ? `"${alias.toLowerCase()} (${this.vlan})"`
              : alias;
            routes.push({
              label: 'Show VMs',
              icon: 'desktop',
              route: {
                name: 'experiment',
                params: { id: name },
                query: alias ? { q: term } : {},
              },
            });
            routes.push({
              label: 'SoH group',
              icon: 'heartbeat',
              route: { name: 'soh', params: { id: name } },
            });
          }
          if (
            group.running &&
            roleAllowed('experiments', 'get', name) &&
            roleAllowed('experiments/netflow', 'get', name)
          ) {
            routes.push({
              label: 'Netflow',
              icon: 'circle-nodes',
              route: {
                name: 'experiment',
                params: { id: name },
                query: { tab: 'netflow', q: String(this.vlan) },
              },
            });
          }
          const capture =
            group.running &&
            alias &&
            roleAllowed('vms/captures', 'create', `${name}/*`)
              ? { name: 'capture', experiment: name, alias }
              : null;
          const key = `experiment:${name}`;
          return {
            experiment: name,
            card: this.cardsByKey.has(key) ? key : null,
            routes,
            capture,
          };
        });
      },
    },
    watch: {
      vlan: { handler: 'load', immediate: true },
      // a new read of the page keeps the old details until the new ones come
      data() {
        this.load({ keep: true });
      },
    },
    mounted() {
      this.$refs.title?.focus({ preventScroll: true });
    },
    beforeUnmount() {
      this.controller?.abort();
    },
    methods: {
      // the ID's VMs and issues, read from the server; keep shows the old ones
      // until then
      async load({ keep = false } = {}) {
        this.controller?.abort();
        if (!keep) this.details = null;
        if (!roleAllowed('vlans', 'get')) return;
        const controller = new AbortController();
        this.controller = controller;
        const id = this.vlan;
        try {
          const { data } = await vlansApi.getVlan(id, {
            signal: controller.signal,
          });
          if (id === this.vlan) this.details = data;
        } catch (err) {
          if (!controller.signal.aborted && err?.response?.status !== 404) {
            useErrorNotification(err);
          }
        }
      },
      vmKey(vm) {
        return `${vm.experiment}/${vm.vm}/${vm.interface}`;
      },
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

  .content ul.vlan-details-vms,
  .content ul.vlan-details-issues {
    margin: 0.25rem 0 0;
    list-style: none;
    font-size: 0.85rem;
  }

  .content ul.vlan-details-issues {
    color: #ffe08a;
  }

  .vlan-details-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }

  .content h3.vlan-details-subtitle {
    margin: 0 0 0.35rem;
    font-size: 0.9rem;
  }

  .vlan-details-links .buttons {
    margin: 0;
  }

  .vlan-details-none {
    font-style: italic;
  }

  /* one column; the global ul rule makes two */
  ul.vlan-details-vms,
  ul.vlan-details-issues {
    columns: auto;
  }
</style>
