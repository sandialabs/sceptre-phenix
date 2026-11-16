<!--
The VLANs page: every VLAN ID in the cluster, from the phēnix experiment
specs and from minimega, with the differences and conflicts between them.
It has two views of the same data under one header: a card for each
experiment, and a grid of the whole ID space.
-->
<template>
  <div class="content vlans-page">
    <h1 class="is-sr-only">VLANs</h1>

    <b-modal
      v-model="dialog.active"
      has-modal-card
      aria-role="dialog"
      aria-modal
      :can-cancel="dialog.busy ? [] : ['escape', 'outside']">
      <VlanIdsDialog
        v-if="dialog.kind === 'ids'"
        :card="dialog.card"
        :alias="dialog.alias"
        :values="dialog.values"
        @busy="(busy) => (dialog.busy = busy)"
        @close="dialog.active = false"
        @saved="afterChange" />
      <VlanRangeDialog
        v-else-if="dialog.kind === 'range'"
        :card="dialog.card"
        :suggest-first="dialog.suggest"
        :initial-range="dialog.range"
        @busy="(busy) => (dialog.busy = busy)"
        @close="dialog.active = false"
        @saved="afterChange" />
    </b-modal>

    <div class="vlans-head">
      <stat-strip
        v-if="loaded"
        label="VLAN totals"
        :stats="stats"
        @select="selectStat" />
      <div class="vlans-tools">
        <b-field class="vlans-search">
          <b-input
            :model-value="query.search"
            type="search"
            icon="search"
            placeholder="Search ID, alias or experiment"
            aria-label="Search by ID, alias or experiment"
            @update:model-value="(q) => setQuery({ search: q })" />
        </b-field>
        <b-button
          icon-left="wand-magic-sparkles"
          :aria-expanded="finderOpen ? 'true' : 'false'"
          @click="toggleFinder"
          >Find free IDs</b-button
        >
        <b-dropdown aria-role="list" position="is-bottom-left">
          <template #trigger>
            <b-button icon-left="download" icon-right="caret-down">
              Export
            </b-button>
          </template>
          <b-dropdown-item aria-role="listitem" @click="exportAs('csv')"
            >CSV</b-dropdown-item
          >
          <b-dropdown-item aria-role="listitem" @click="exportAs('json')"
            >JSON</b-dropdown-item
          >
        </b-dropdown>
      </div>
    </div>

    <b-notification
      v-if="loaded && !data.minimega.ok"
      type="is-warning"
      :closable="false"
      role="status"
      class="vlans-notice">
      Could not read minimega<template v-if="data.minimega.error"
        >: {{ data.minimega.error }}</template
      >. The page shows the phēnix experiment specs only, with no differences.
    </b-notification>

    <div v-if="summary.total" class="vlans-banner" role="status">
      <b-icon icon="exclamation-triangle" aria-hidden="true" />
      <span class="vlans-banner-text">{{ summary.text }}</span>
      <b-button
        size="is-small"
        :aria-pressed="query.filter === 'issues' ? 'true' : 'false'"
        @click="toggleIssues"
        >Show issues only</b-button
      >
    </div>

    <div class="vlans-toolbar">
      <div
        v-if="query.view !== 'blacklist'"
        class="vlans-chips"
        role="group"
        aria-label="Filter the cards">
        <button
          v-for="chip in chips"
          :key="chip.key"
          type="button"
          :class="[
            'button',
            'is-small',
            'vlans-chip',
            { 'is-active': chip.on },
          ]"
          :aria-pressed="chip.on ? 'true' : 'false'"
          @click="setQuery({ filter: chip.key })">
          {{ chip.label }}
          <span class="vlans-chip-count">{{ chip.count }}</span>
        </button>
        <span v-if="query.experiment" class="tag is-medium vlans-exp-chip">
          Experiment: {{ query.experiment }}
          <button
            type="button"
            class="delete is-small"
            :aria-label="`Show all experiments, not only ${query.experiment}`"
            @click="setQuery({ experiment: '' })"></button>
        </span>
      </div>
      <div class="vlans-views" role="group" aria-label="View">
        <button
          type="button"
          :class="[
            'button',
            'is-small',
            { 'is-active': query.view === 'cards' },
          ]"
          :aria-pressed="query.view === 'cards' ? 'true' : 'false'"
          @click="setQuery({ view: 'cards' })">
          <b-icon icon="table-list" size="is-small" aria-hidden="true" />
          <span>By experiment</span>
        </button>
        <button
          type="button"
          :class="[
            'button',
            'is-small',
            { 'is-active': query.view === 'grid' },
          ]"
          :aria-pressed="query.view === 'grid' ? 'true' : 'false'"
          @click="setQuery({ view: 'grid' })">
          <b-icon icon="table-cells" size="is-small" aria-hidden="true" />
          <span>ID space</span>
        </button>
        <button
          v-if="data.blacklist !== null"
          type="button"
          :class="[
            'button',
            'is-small',
            { 'is-active': query.view === 'blacklist' },
          ]"
          :aria-pressed="query.view === 'blacklist' ? 'true' : 'false'"
          @click="setQuery({ view: 'blacklist' })">
          <b-icon icon="ban" size="is-small" aria-hidden="true" />
          <span>Blacklist</span>
        </button>
      </div>
    </div>

    <VlanFreeFinder
      v-if="finderOpen && loaded"
      :data="data"
      @close="closeFinder"
      @found="foundBlock"
      @set-range="setRangeFromBlock" />

    <div :class="['vlans-main', { 'has-panel': panelOpen }]">
      <div class="vlans-view">
        <template v-if="query.view === 'grid'">
          <VlanGrid
            v-if="loaded"
            v-model:active="activeId"
            :data="data"
            :states="states"
            :outlines="outlines"
            :shown="filtering ? shownIds : null"
            @open="openVlan"
            @select="openRange" />
          <p v-else class="vlans-empty">{{ loadingText('VLANs') }}</p>
        </template>
        <template v-else>
          <p v-if="!shownCards.length" class="vlans-empty">{{ emptyText }}</p>
          <div v-else-if="query.view === 'blacklist'" class="vlans-blacklist">
            <VlanCard
              v-for="card in shownCards"
              :key="card.key"
              :card="card"
              :selected="selected === card.key"
              :removable="data.minimega.blacklistRemoval"
              :minimega-ok="data.minimega.ok"
              :busy="blacklistBusy"
              @select="select"
              @open="openVlan"
              @act="act" />
          </div>
          <div v-else v-masonry class="vlans-cards">
            <VlanCard
              v-for="card in shownCards"
              :key="card.key"
              :card="card"
              :selected="selected === card.key"
              :removable="data.minimega.blacklistRemoval"
              :minimega-ok="data.minimega.ok"
              :busy="blacklistBusy"
              :suggestions="suggestionsFor(card)"
              @select="select"
              @open="openVlan"
              @act="act" />
          </div>
        </template>
      </div>
      <VlanRangeDetails
        v-if="panelOpen && query.range"
        :range="query.range"
        class="vlans-panel"
        :data="data"
        :states="states"
        @close="closePanel"
        @open="openVlan" />
      <VlanDetails
        v-else-if="panelOpen"
        :vlan="query.vlan"
        class="vlans-panel"
        :data="data"
        :states="states"
        :cards="cards"
        :removable="data.minimega.blacklistRemoval"
        :busy="blacklistBusy"
        @close="closePanel"
        @act="act"
        @show-card="showCard" />
    </div>
  </div>
</template>

<script>
  import FileSaver from 'file-saver';

  import * as experimentsApi from '@/api/experiments.js';
  import * as vlansApi from '@/api/vlans.js';
  import StatStrip from '@/components/StatStrip.vue';
  import VlanCard from '@/components/vlans/VlanCard.vue';
  import VlanDetails from '@/components/vlans/VlanDetails.vue';
  import VlanFreeFinder from '@/components/vlans/VlanFreeFinder.vue';
  import VlanGrid from '@/components/vlans/VlanGrid.vue';
  import VlanIdsDialog from '@/components/vlans/VlanIdsDialog.vue';
  import VlanRangeDetails from '@/components/vlans/VlanRangeDetails.vue';
  import VlanRangeDialog from '@/components/vlans/VlanRangeDialog.vue';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import { showError, useErrorNotification } from '@/utils/errorNotif.js';
  import { vMasonry } from '@/utils/masonry.js';
  import { confirmOperation } from '@/utils/operations.js';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import { pageFetchers } from '@/utils/pageData.js';
  import { listText, plural, pluralWord } from '@/utils/plural.js';
  import {
    FILTERS,
    MIN_VLAN,
    SELECTION_COLOR,
    cardIds,
    conflictSuggestions,
    idStates,
    issueSummary,
    normalizeVlans,
    shownData,
    vlanCards,
    vlansCsv,
    vlanCounts,
    vlanInputError,
    vlansQuery,
    vlansQueryOf,
    viewCards,
  } from '@/utils/vlans.js';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';

  // experiment messages that can change its VLAN IDs
  const EXPERIMENT_ACTIONS = new Set([
    'create',
    'start',
    'stop',
    'update',
    'delete',
  ]);

  // the outline colors of the ID space view
  const OUTLINE = {
    range: '#62c0d7',
    ids: '#ffffff',
    block: '#ffd257',
  };

  export default {
    components: {
      StatStrip,
      VlanCard,
      VlanDetails,
      VlanFreeFinder,
      VlanGrid,
      VlanIdsDialog,
      VlanRangeDetails,
      VlanRangeDialog,
    },
    directives: { masonry: vMasonry },
    data() {
      return {
        data: normalizeVlans(null),
        loaded: false,
        selected: null,
        activeId: MIN_VLAN,
        finderOpen: false,
        // the blacklist changes in progress, such as 'add:42' and 'remove:7'
        blacklistBusy: [],
        block: null,
        dialog: {
          active: false,
          busy: false,
          kind: null,
          card: null,
          alias: '',
          values: {},
          suggest: false,
          range: null,
        },
      };
    },
    computed: {
      query() {
        return vlansQuery(this.$route.query);
      },
      states() {
        return idStates(this.data);
      },
      cards() {
        return vlanCards(this.data);
      },
      shownCards() {
        return viewCards(this.cards, this.query);
      },
      filtering() {
        return (
          this.query.filter !== 'all' ||
          !!this.query.search.trim() ||
          !!this.query.experiment
        );
      },
      shownIds() {
        return cardIds(this.shownCards);
      },
      summary() {
        return issueSummary(this.data.issues);
      },
      stats() {
        const counts = vlanCounts(this.data);
        const issuesOnly = this.query.filter === 'issues';
        return [
          { key: 'allocated', label: 'Allocated', value: counts.allocated },
          { key: 'pinned', label: 'Pinned', value: counts.pinned },
          {
            key: 'blacklisted',
            label: 'Blacklisted',
            value: counts.blacklisted,
          },
          {
            key: 'issues',
            label: 'Issues',
            value: counts.issues,
            filter: issuesOnly
              ? 'Show all cards'
              : 'Show only the cards with issues',
            active: issuesOnly,
          },
          {
            key: 'free',
            label: 'Free',
            value: counts.free.toLocaleString('en-US'),
          },
        ];
      },
      chips() {
        const filter = this.query.filter;
        const cards = this.cards.filter((card) => card.kind !== 'blacklist');
        return FILTERS.map((chip) => ({
          key: chip.key,
          label: chip.label,
          count: cards.filter(chip.matches).length,
          on: filter === chip.key,
        }));
      },
      emptyText() {
        if (!this.loaded) return loadingText('VLANs');
        if (!this.cards.length) return 'No VLAN IDs are in use.';
        return 'No cards match the filter or the search.';
      },
      panelOpen() {
        return (
          this.loaded && (this.query.vlan !== null || this.query.range !== null)
        );
      },
      selectedCard() {
        return this.cards.find((card) => card.key === this.selected) ?? null;
      },
      // the IDs selected in the ID space view, the selected card's range and
      // IDs, and a free block found
      outlines() {
        const list = [];
        if (this.query.range) {
          const [first, last] = this.query.range;
          const ids = new Set();
          for (let id = first; id <= last; id++) ids.add(id);
          list.push({ label: 'Selected IDs', color: SELECTION_COLOR, ids });
        }
        const card = this.selectedCard;
        if (card?.range) {
          const ids = new Set();
          for (let id = card.range[0]; id <= card.range[1]; id++) ids.add(id);
          list.push({
            label: `Range of ${card.name}`,
            color: OUTLINE.range,
            ids,
          });
        }
        if (card?.ids.length) {
          list.push({
            label: `IDs of ${card.name}`,
            color: OUTLINE.ids,
            ids: new Set(card.ids),
          });
        }
        if (this.block) {
          const ids = new Set();
          for (let id = this.block.start; id <= this.block.end; id++)
            ids.add(id);
          list.push({ label: 'Free block found', color: OUTLINE.block, ids });
        }
        return list;
      },
    },
    watch: {
      'query.vlan': {
        handler(id) {
          if (id !== null) this.activeId = id;
        },
        immediate: true,
      },
      'query.experiment': {
        handler(name) {
          if (name) this.selected = `experiment:${name}`;
        },
        immediate: true,
      },
    },
    created() {
      this.loader = createPageLoader({
        key: 'vlans',
        fetch: pageFetchers.vlans,
        apply: (data) => {
          this.data = data;
          this.loaded = true;
        },
        // the header's refresh has the server read minimega again
        refresh: () => this.loader.load({ force: true }),
      });
      this.loader.start();
      addWsHandler(this.handleWs);
    },
    beforeUnmount() {
      removeWsHandler(this.handleWs);
      this.loader.stop();
    },
    methods: {
      loadingText,
      // Changes the page's query string keys in change, keeping the others.
      // Only typing a search replaces the history entry, so Back undoes the rest.
      setQuery(change) {
        const query = vlansQueryOf({ ...this.query, ...change });
        const typing = Object.keys(change).every((key) => key === 'search');
        this.$router[typing ? 'replace' : 'push']({ query }).catch(() => {});
      },
      handleWs(msg) {
        const type = msg.resource?.type;
        const changed =
          type === 'vlans' ||
          (type === 'experiment' &&
            EXPERIMENT_ACTIONS.has(msg.resource.action)) ||
          (type === 'config' &&
            !!msg.resource.name?.startsWith('Experiment/') &&
            msg.resource.action !== 'get');
        if (changed) this.loader.load({ force: true });
      },
      reload() {
        return this.loader.load({ force: true });
      },
      afterChange() {
        this.dialog.active = false;
        this.reload();
      },
      selectStat(key) {
        if (key === 'issues') this.toggleIssues();
      },
      toggleIssues() {
        this.setQuery({
          filter: this.query.filter === 'issues' ? 'all' : 'issues',
        });
      },
      toggleFinder() {
        if (this.finderOpen) this.closeFinder();
        else this.finderOpen = true;
      },
      closeFinder() {
        this.finderOpen = false;
        this.block = null;
      },
      foundBlock(block) {
        this.block = block;
        if (block) {
          this.activeId = block.start;
          this.setQuery({ view: 'grid' });
        }
      },
      setRangeFromBlock(name, range) {
        const card = this.cards.find((c) => c.key === `experiment:${name}`);
        if (card) this.openDialog('range', { card, range });
      },
      select(key) {
        this.selected = this.selected === key ? null : key;
      },
      // opens the details of an ID; closing them puts focus back here
      openVlan(id) {
        if (!this.panelOpen) this.opener = document.activeElement;
        this.setQuery({ vlan: id, range: null });
      },
      // opens the details of each ID of a range, [first, last]
      openRange(range) {
        if (!this.panelOpen) this.opener = document.activeElement;
        this.setQuery({ vlan: null, range });
      },
      closePanel() {
        const opener = this.opener;
        this.opener = null;
        this.setQuery({ vlan: null, range: null });
        this.$nextTick(() => {
          if (opener?.isConnected) opener.focus();
        });
      },
      // shows the card of an owner, selected, in the By experiment view
      showCard(key) {
        this.selected = key;
        this.setQuery({
          view: 'cards',
          filter: 'all',
          search: '',
          experiment: '',
        });
        this.$nextTick(() => {
          const el = this.$el.querySelector(
            `[data-card="${CSS.escape(key)}"] .vlan-card-select`,
          );
          el?.scrollIntoView?.({ block: 'nearest' });
          el?.focus();
        });
      },
      suggestionsFor(card) {
        return card.kind === 'experiment' && !card.running
          ? conflictSuggestions(this.data, card)
          : {};
      },
      openDialog(
        kind,
        { card, alias = '', values = {}, suggest = false, range = null },
      ) {
        Object.assign(this.dialog, {
          active: true,
          busy: false,
          kind,
          card,
          alias,
          values,
          suggest,
          range,
        });
      },
      // the fixes the cards and the details panel offer
      act(event) {
        const { name, card } = event;
        switch (name) {
          case 'change':
            return this.openDialog('ids', {
              card,
              alias: event.alias,
              values: { [event.alias]: event.id },
            });
          case 'edit-ids':
            return this.openDialog('ids', { card });
          case 'range':
            return this.openDialog('range', { card, suggest: !!event.suggest });
          case 'pin-live':
            return this.pinLive(card);
          case 'release':
            return this.release(card);
          case 'blacklist-add':
            return event.id
              ? this.addToBlacklist(event.id)
              : this.promptBlacklist();
          case 'blacklist-remove':
            return this.removeFromBlacklist(event.id);
          case 'capture':
            return this.capture(event.experiment, event.alias);
        }
      },
      // sends a change, then reports it and reloads, or reports the error
      async send(request, message) {
        try {
          await request();
        } catch (err) {
          useErrorNotification(err);
          return false;
        }
        this.$buefy.toast.open({
          message: escapeHTML(message),
          type: 'is-success',
        });
        await this.reload();
        return true;
      },
      // sends a blacklist change, marked busy (op:id) until the page reloads
      async changeBlacklist(op, id, request, message) {
        const key = `${op}:${id}`;
        this.blacklistBusy = [...this.blacklistBusy, key];
        try {
          return await this.send(request, message);
        } finally {
          this.blacklistBusy = this.blacklistBusy.filter((k) => k !== key);
        }
      },
      async pinLive(card) {
        const changes = card.aliases
          .filter((a) => a.live && a.live !== a.pinned)
          .map(
            (a) =>
              `${escapeHTML(a.alias)}: ${a.pinned || 'not pinned'} → ${a.live}`,
          );
        const sure = await confirmOperation(this.$buefy, {
          title: 'Pin the Live IDs',
          message:
            `This copies the IDs that minimega allocated into the spec of ` +
            `<b>${escapeHTML(card.name)}</b>, so that the next start gives ` +
            `the same IDs.</p><p>${changes.join('<br>')}`,
          confirmText: 'Pin live IDs',
          type: 'is-success',
        });
        if (!sure) return;
        await this.send(
          () => vlansApi.pinLiveVlans(card.name),
          `Pinned the live VLAN IDs of ${card.name}`,
        );
      },
      async release(card) {
        const ids = card.aliases.map((a) => a.live);
        const count = card.aliases.length;
        const vms = card.vms;
        const sure = await confirmOperation(this.$buefy, {
          title: 'Release Unowned VLANs',
          message:
            `This clears ${count} ${pluralWord(count, 'alias', 'aliases')} of ` +
            `the minimega namespace <b>${escapeHTML(card.name)}</b>: ` +
            `${escapeHTML(listText(ids.map(String)))}.</p><p>` +
            (vms
              ? `${plural(vms, 'VM')} still ${pluralWord(vms, 'uses', 'use')} ` +
                'these IDs, so minimega can refuse to release them.'
              : 'No phēnix experiment has this namespace, and no VMs use ' +
                'these IDs. minimega can then give the IDs to other experiments.'),
          confirmText: 'Release',
          type: 'is-danger',
        });
        if (!sure) return;
        await this.send(
          () => vlansApi.releaseVlanAllocations(card.name),
          `Released the VLAN IDs of ${card.name}`,
        );
      },
      // the warning that a blacklist entry may stay for good
      blacklistNote() {
        return this.data.minimega.blacklistRemoval
          ? ''
          : '</p><p>minimega cannot remove IDs from its blacklist, so you ' +
              'cannot undo this.';
      },
      promptBlacklist() {
        this.$buefy.dialog.prompt({
          title: 'Add to the Blacklist',
          message:
            'minimega gives a blacklisted ID to no alias, for example an ID ' +
            `that a hardware switch uses.${this.blacklistNote()}`,
          inputAttrs: {
            type: 'number',
            min: 1,
            max: 4094,
            placeholder: 'VLAN ID',
            'aria-label': 'VLAN ID to blacklist',
          },
          confirmText: 'Add',
          cancelText: 'Cancel',
          type: 'is-warning',
          onConfirm: (value) => {
            const error = vlanInputError(value);
            if (error) {
              showError(error);
              return;
            }
            this.changeBlacklist(
              'add',
              Number(value),
              () => vlansApi.addVlanToBlacklist(Number(value)),
              `Added ${Number(value)} to the blacklist`,
            );
          },
        });
      },
      async addToBlacklist(id) {
        const sure = await confirmOperation(this.$buefy, {
          title: 'Add to the Blacklist',
          message:
            `This adds ${id} to the minimega blacklist. minimega then gives ` +
            `it to no alias.${this.blacklistNote()}`,
          confirmText: 'Add',
          type: 'is-warning',
        });
        if (!sure) return;
        await this.changeBlacklist(
          'add',
          id,
          () => vlansApi.addVlanToBlacklist(id),
          `Added ${id} to the blacklist`,
        );
      },
      async removeFromBlacklist(id) {
        if (!this.data.minimega.blacklistRemoval) return;
        const sure = await confirmOperation(this.$buefy, {
          title: 'Remove from the Blacklist',
          message: `This removes ${id} from the minimega blacklist, so minimega can give it to an alias again.`,
          confirmText: 'Remove',
          type: 'is-warning',
        });
        if (!sure) return;
        await this.changeBlacklist(
          'remove',
          id,
          () => vlansApi.removeVlanFromBlacklist(id),
          `Removed ${id} from the blacklist`,
        );
      },
      async capture(experiment, alias) {
        const sure = await confirmOperation(this.$buefy, {
          title: 'Capture the VLAN',
          message:
            `This starts packet captures on every interface on the ` +
            `<b>${escapeHTML(alias)}</b> VLAN of ${escapeHTML(experiment)}.`,
          confirmText: 'Start captures',
          type: 'is-success',
        });
        if (!sure) return;
        try {
          const resp = await experimentsApi.startVlanCaptures(
            experiment,
            alias,
          );
          const count = resp.data?.captures?.length ?? 0;
          this.$buefy.toast.open({
            message: escapeHTML(
              `Started ${plural(count, 'packet capture')} on the ${alias} VLAN.`,
            ),
            type: count ? 'is-success' : 'is-info',
          });
        } catch (err) {
          useErrorNotification(err);
        }
      },
      // downloads what the shown cards show, as GET /vlans would give it
      exportAs(format) {
        const shown = shownData(this.data, this.shownCards);
        const blob =
          format === 'csv'
            ? new Blob([vlansCsv(shown)], { type: 'text/csv' })
            : new Blob([JSON.stringify(shown, null, 2)], {
                type: 'application/json',
              });
        FileSaver.saveAs(blob, `vlans.${format}`);
      },
    },
  };
</script>

<style scoped>
  .vlans-page {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .vlans-head,
  .vlans-toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
  }

  .vlans-tools {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-left: auto;
  }

  .vlans-search {
    margin-bottom: 0 !important;
    min-width: 18rem;
  }

  .vlans-notice {
    margin-bottom: 0 !important;
  }

  /* the issue counts: #ffe08a text is 8.4:1 on the card gray */
  .vlans-banner {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 0.75rem;
    padding: 0.5rem 0.9rem;
    background-color: #3d3d3d;
    border: 1px solid #686868;
    border-left: 4px solid #ffd257;
    border-radius: 6px;
    color: #ffe08a;
  }

  .vlans-banner-text {
    flex: 1;
  }

  .vlans-chips,
  .vlans-views {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
  }

  .vlans-chip {
    border-radius: 999px;
  }

  .vlans-chips .button.is-active,
  .vlans-views .button.is-active {
    background-color: white;
    border-color: white;
    color: #1a1a1a;
    font-weight: 700;
  }

  .vlans-chip-count {
    margin-left: 0.3rem;
    font-variant-numeric: tabular-nums;
  }

  .vlans-exp-chip {
    gap: 0.4rem;
  }

  .vlans-main {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 1rem;
    align-items: start;
  }

  .vlans-main.has-panel {
    grid-template-columns: minmax(0, 1fr) minmax(18rem, 24rem);
  }

  .vlans-panel {
    position: sticky;
    top: 1rem;
  }

  .vlans-cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(20rem, 1fr));
    gap: 0.9rem;
    align-items: start;
  }

  /* v-masonry: MASONRY_ROW-pixel rows, each card spans its height and the gap */
  .vlans-cards.is-masonry {
    grid-auto-rows: 4px;
    row-gap: 0;
  }

  /* the Blacklist view: its one card, wide enough for a long list */
  .vlans-blacklist {
    max-width: 40rem;
  }

  .vlans-empty {
    padding: 2rem;
    text-align: center;
  }

  @media (max-width: 1023px) {
    .vlans-main.has-panel {
      grid-template-columns: minmax(0, 1fr);
    }

    .vlans-panel {
      position: static;
    }
  }
</style>
