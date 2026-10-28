<template>
  <span v-if="!offered">{{ label }}</span>
  <template v-else>
    <button
      ref="trigger"
      type="button"
      class="plain-button vm-network-trigger"
      aria-haspopup="menu"
      :aria-label="`Network menu for interface ${index} of VM ${vm.name}: ${label}`"
      :aria-expanded="String(open)"
      :aria-controls="open ? menuId : undefined"
      :aria-busy="pending"
      @click="toggle"
      @keydown.down.prevent="show"
      @keydown.esc="onTriggerEscape"
      @keydown.tab="hide(false)">
      {{ label }}
      <b-icon icon="caret-down" size="is-small" />
      <span
        v-if="pending"
        class="capture-spinner"
        title="waiting for the server"></span>
    </button>
    <!-- on the page body while open, so the table's scrolling clips nothing -->
    <Teleport v-if="open" to="body">
      <div class="dropdown is-active vm-network-menu" :style="place">
        <div class="dropdown-menu">
          <div
            :id="menuId"
            ref="menu"
            class="dropdown-content vm-network-items"
            role="menu"
            :aria-label="`Network of interface ${index} of VM ${vm.name}`"
            @keydown="onKeydown">
            <template v-for="item in items" :key="item.name">
              <router-link
                v-if="item.to"
                :to="item.to"
                role="menuitem"
                :class="['dropdown-item', `vm-network-${item.name}`]"
                @click="hide(true)">
                {{ item.label }}
              </router-link>
              <button
                v-else
                type="button"
                role="menuitem"
                :class="['dropdown-item', `vm-network-${item.name}`]"
                :disabled="item.disabled"
                @click="choose(item)">
                {{ item.label }}
              </button>
            </template>
          </div>
        </div>
      </div>
    </Teleport>
  </template>
</template>

<script>
  import { usePhenixStore } from '@/store.js';
  import { isInterfaceCapturing } from '@/utils/captures.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { canUseWebShark, webSharkInstalled } from '@/utils/webshark.js';
  import { TAB } from './experimentTabs.js';
  import { vlanAlias } from './vmActions.js';

  let menus = 0;

  // The menu of a VM interface's network in the VM table: changing or
  // disconnecting its VLAN, setting its link quality, starting or stopping
  // packet captures on it or on its whole VLAN, listing the VMs on its VLAN,
  // and links to its capture files and live capture. A network with no item
  // to press, and no request waiting, shows as text.
  export default {
    name: 'VmNetworkMenu',
    props: {
      vm: { type: Object, required: true },
      index: { type: Number, required: true },
      // the experiment's name, which RBAC checks VM permissions under
      experiment: { type: String, default: '' },
      // whether some interface on this interface's VLAN is captured
      vlanCaptured: { type: Boolean, default: false },
      // whether a VLAN change, a capture of this interface, or captures of
      // its VLAN wait on the server
      vlanChangePending: { type: Boolean, default: false },
      capturePending: { type: Boolean, default: false },
      vlanCapturePending: { type: Boolean, default: false },
    },
    emits: [
      'change-vlan',
      'disconnect',
      'link-quality',
      'capture',
      // (alias, start): start or stop the captures on the interface's VLAN
      'capture-vlan',
      // (term): search the VM table for the VMs on the interface's VLAN
      'show-vlan',
    ],
    data() {
      return { open: false, place: {}, menuId: `vm-network-menu-${++menus}` };
    },
    beforeUnmount() {
      this.unlisten();
    },
    computed: {
      network() {
        return this.vm.networks?.[this.index] ?? '';
      },
      label() {
        return this.network.toLowerCase();
      },
      alias() {
        return vlanAlias(this.network);
      },
      fullName() {
        return `${this.experiment}/${this.vm.name}`;
      },
      // the menu's items for the role and the interface
      items() {
        const may = (resource, verb) =>
          roleAllowed(resource, verb, this.fullName);
        const off = !this.vm.running;
        // a capture item the VM's state rules out says why
        const why = off ? ' (the VM is not running)' : '';
        const capturing = isInterfaceCapturing(this.vm.captures, this.index);
        const vlan = this.label.replace(/\s*\(\d+\)$/, '');
        const items = [];
        if (this.vm.running && may('vms', 'patch')) {
          items.push({ name: 'change', label: 'Change VLAN…' });
          if (vlan !== 'disconnected') {
            items.push({ name: 'disconnect', label: 'Disconnect' });
          }
          items.push({ name: 'link-quality', label: 'Set link quality…' });
        }
        if (may('vms/captures', capturing ? 'delete' : 'create')) {
          items.push({
            name: capturing ? 'stop' : 'start',
            label: `${capturing ? 'Stop' : 'Start'} capture on this interface${why}`,
            disabled: off || this.capturePending || (!capturing && !this.alias),
          });
        }
        if (this.alias && may('vms/captures', 'create')) {
          items.push({
            name: 'start-vlan',
            label: `Start captures on VLAN ${vlan}${why}`,
            disabled: off || this.vlanCapturePending,
          });
        }
        if (this.alias && this.vlanCaptured && may('vms/captures', 'delete')) {
          items.push({
            name: 'stop-vlan',
            label: `Stop captures on VLAN ${vlan}${why}`,
            disabled: off || this.vlanCapturePending,
          });
        }
        if (this.vlanTerm && may('vms', 'list')) {
          items.push({ name: 'show-vlan', label: `Show VMs on VLAN ${vlan}` });
        }
        if (roleAllowed('experiments/files', 'list', this.experiment)) {
          items.push({
            name: 'files',
            label: 'Show capture files in the Files tab',
            to: {
              name: 'experiment',
              params: { id: this.experiment },
              query: { tab: TAB.FILES, q: `${this.vm.name}_${this.index}_` },
            },
          });
        }
        if (
          capturing &&
          webSharkInstalled(usePhenixStore().features) &&
          canUseWebShark() &&
          may('vms/captures', 'list')
        ) {
          items.push({
            name: 'webshark',
            label: 'Open live capture in WebShark',
            to: {
              name: 'webshark',
              query: {
                exp: this.experiment,
                vm: this.vm.name,
                iface: String(this.index),
              },
            },
          });
        }
        return items;
      },
      // The search that lists the VMs on the interface's VLAN: its quoted
      // name with the VLAN ID, so EXP does not find the VMs on EXP_2 too.
      vlanTerm() {
        if (!this.alias || this.label.includes('"')) return '';
        return `"${this.label}"`;
      },
      offered() {
        return (
          !this.vm.busy &&
          !this.vm.external &&
          (this.pending || this.items.some((item) => !item.disabled))
        );
      },
      pending() {
        return (
          this.vlanChangePending ||
          this.capturePending ||
          this.vlanCapturePending
        );
      },
    },
    watch: {
      // a VM that turns busy or loses its items closes its menu
      offered(on) {
        if (!on) this.hide(false);
      },
      // a row given another VM closes the menu opened for the last one
      'vm.name'() {
        this.hide(false);
      },
    },
    methods: {
      toggle() {
        if (this.open) this.hide(false);
        else this.show();
      },

      // Opens the menu below its button, its first item focused; a click
      // elsewhere, a resize or a scroll closes it.
      async show() {
        if (this.open) return;
        const rect = this.$refs.trigger.getBoundingClientRect();
        this.place = {
          position: 'absolute',
          top: `${rect.bottom + window.scrollY}px`,
          left: `${rect.left + window.scrollX}px`,
          zIndex: 40,
        };
        this.open = true;
        document.addEventListener('click', this.onOutside, true);
        window.addEventListener('resize', this.onResize);
        window.addEventListener('scroll', this.onScroll, true);
        await this.$nextTick();
        this.buttons()[0]?.focus({ preventScroll: true });
      },

      // closes the menu, giving the focus back to its button when asked
      hide(refocus) {
        if (!this.open) return;
        this.open = false;
        this.unlisten();
        if (refocus) this.$refs.trigger?.focus();
      },

      unlisten() {
        document.removeEventListener('click', this.onOutside, true);
        window.removeEventListener('resize', this.onResize);
        window.removeEventListener('scroll', this.onScroll, true);
      },

      onOutside(event) {
        const inside = [this.$refs.trigger, this.$refs.menu].some((el) =>
          el?.contains(event.target),
        );
        if (!inside) this.hide(false);
      },

      onResize() {
        this.hide(false);
      },

      // the menu stays where it opened, so a scroll of anything else closes it
      onScroll(event) {
        if (!this.$refs.menu?.contains(event.target)) this.hide(false);
      },

      onTriggerEscape(event) {
        if (!this.open) return;
        event.preventDefault();
        this.hide(true);
      },

      choose(item) {
        this.hide(true);
        switch (item.name) {
          case 'change':
            this.$emit('change-vlan');
            break;
          case 'disconnect':
            this.$emit('disconnect');
            break;
          case 'link-quality':
            this.$emit('link-quality');
            break;
          case 'show-vlan':
            this.$emit('show-vlan', this.vlanTerm);
            break;
          case 'start':
          case 'stop':
            this.$emit('capture');
            break;
          default:
            this.$emit('capture-vlan', this.alias, item.name === 'start-vlan');
        }
      },

      // the menu's buttons and links that can be pressed, in order
      buttons() {
        return [
          ...(this.$refs.menu?.querySelectorAll(
            'button:not([disabled]), a[href]',
          ) ?? []),
        ];
      },

      // The arrow, Home and End keys move between the items; Escape and Tab
      // close the menu and give the focus back to its button.
      onKeydown(event) {
        if (event.key === 'Escape' || event.key === 'Tab') {
          event.preventDefault();
          this.hide(true);
          return;
        }
        const buttons = this.buttons();
        const at = buttons.indexOf(document.activeElement);
        const last = buttons.length - 1;
        const to = {
          ArrowDown: at < last ? at + 1 : 0,
          ArrowUp: at > 0 ? at - 1 : last,
          Home: 0,
          End: last,
        }[event.key];
        if (to === undefined || !buttons.length) return;
        event.preventDefault();
        buttons[to].focus({ preventScroll: true });
      },
    },
  };
</script>

<style scoped>
  .vm-network-trigger {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
  }

  /* a small spinner beside a network whose request is waiting */
  .capture-spinner {
    display: inline-block;
    width: 0.85em;
    height: 0.85em;
    margin-left: 0.1em;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-top-color: transparent;
    border-radius: 50%;
    animation: capture-spin 0.6s linear infinite;
  }

  @keyframes capture-spin {
    to {
      transform: rotate(360deg);
    }
  }

  /* Bulma's menu item look on a button; hover keeps Bulma's highlight */
  .vm-network-items button.dropdown-item {
    border: 0;
    font-family: inherit;
    cursor: pointer;
  }

  .vm-network-items button.dropdown-item:not(:hover),
  .vm-network-items button.dropdown-item[disabled] {
    background-color: transparent;
  }

  .vm-network-items button.dropdown-item[disabled] {
    cursor: default;
  }
</style>
