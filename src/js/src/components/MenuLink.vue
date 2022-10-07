<template>
  <a v-if="external" :href="to" target="_blank">
    <slot></slot>
  </a>
  <a v-else :href="resolved.href" :class="{ 'is-active': resolved.route.name == $route.name }" @click.prevent="clicked">
    <slot></slot>
  </a>
</template>

<script>
  import EventBus from '@/event-bus'

  export default {
    name: 'menu-link',
    props: {
      to: {
        type: [Object, String],
        default: {name: 'home'}
      },
      external: {
        type: Boolean,
        default: false
      }
    },

    computed: {
      isExactActive() {
        return this.$route.path == this.resolved.route.path
      },

      resolved() {
        return this.$router.resolve(this.to)
      }
    },

    methods: {
      clicked() {
        if (this.isExactActive) {
          return EventBus.$emit('page-reload', this.resolved.route)
        }

        this.$router.push(this.to)
      }
    }
  }
</script>