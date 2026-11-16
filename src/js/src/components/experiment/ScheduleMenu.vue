<template>
  <b-tooltip
    label="assign every VM to a host with a scheduler"
    type="is-light"
    position="is-top"
    :delay="500"
    :active="!open">
    <b-dropdown
      ref="dropdown"
      class="schedule-menu"
      aria-role="menu"
      position="is-bottom-left"
      :disabled="disabled || scheduling"
      @active-change="(active) => (open = active)">
      <template #trigger>
        <b-button
          class="schedule-menu-trigger"
          type="is-light"
          icon-left="server"
          icon-right="caret-down"
          aria-label="Schedule VMs"
          :loading="scheduling"
          :disabled="disabled || scheduling"
          >Schedule</b-button
        >
      </template>
      <b-dropdown-item
        :id="noteId"
        custom
        :focusable="false"
        class="schedule-menu-note"
        aria-hidden="true">
        {{ stateText }}
      </b-dropdown-item>
      <hr class="dropdown-divider" />
      <b-dropdown-item
        v-for="scheduler in schedulers"
        :key="scheduler.name"
        class="schedule-menu-item"
        aria-role="menuitem"
        :aria-description="describe(scheduler)"
        @click="$emit('schedule', scheduler.name)">
        <b-tooltip
          :label="describe(scheduler)"
          type="is-light"
          position="is-left"
          multilined
          append-to-body
          class="schedule-menu-tip">
          <span>{{ scheduler.name }}</span>
          <span v-if="scheduler.builtin" class="tag is-dark">built-in</span>
        </b-tooltip>
      </b-dropdown-item>
    </b-dropdown>
  </b-tooltip>
</template>

<script>
  import { useId } from 'vue';
  import { plural } from '@/utils/plural.js';

  // The Schedule button of a stopped experiment and its menu of schedulers,
  // which says how many VMs have a host now. It asks the page, by event, to
  // schedule with the one picked.
  export default {
    name: 'ScheduleMenu',
    setup() {
      return { noteId: useId() };
    },
    props: {
      // the schedulers to offer, as { name, builtin, description }
      schedulers: { type: Array, default: () => [] },
      // how many VMs the schedule gives a host, null while not known
      pinned: { type: Number, default: null },
      // how many VMs the experiment has, null while not known
      total: { type: Number, default: null },
      // whether a schedule waits on the server
      scheduling: { type: Boolean, default: false },
      disabled: { type: Boolean, default: false },
    },
    // (name): schedule the VMs with this scheduler
    emits: ['schedule'],
    data() {
      return { open: false };
    },
    // Buefy puts the menu role on an inner element, which gets its name and
    // the note as its description here.
    mounted() {
      const menu = this.$refs.dropdown.$el.querySelector('.dropdown-content');
      menu?.setAttribute('aria-label', 'Schedulers');
      menu?.setAttribute('aria-describedby', this.noteId);
    },
    methods: {
      // what the scheduler does: the server describes only the built-in ones
      describe({ name, builtin, description }) {
        if (description) return description;
        return builtin
          ? 'A built-in scheduler.'
          : `A user scheduler: the server runs phenix-scheduler-${name}.`;
      },
    },
    computed: {
      stateText() {
        if (this.pinned === null) {
          return 'Pick a scheduler to assign every VM to a host';
        }
        if (this.pinned === 0) return 'No VM has a host yet';
        const verb = this.pinned == 1 ? 'has' : 'have';
        if (this.total === null) {
          return `${plural(this.pinned, 'VM')} ${verb} a host`;
        }
        return `${this.pinned} of ${plural(this.total, 'VM')} ${verb} a host`;
      },
    },
  };
</script>

<style scoped>
  .schedule-menu-note {
    white-space: nowrap;
  }

  /* the tooltip's trigger fills the row, so the whole row shows it */
  .schedule-menu-item :deep(.schedule-menu-tip),
  .schedule-menu-item :deep(.schedule-menu-tip .tooltip-trigger) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    width: 100%;
  }
</style>
