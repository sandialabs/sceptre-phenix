<template>
  <a :href="resolvedRoute.route.fullPath" :class="{ 'is-active': resolvedRoute.route.name == $route.name }" @click.prevent="clicked">
    <slot></slot>
  </a>
</template>

<script>
  import EventBus from '@/event-bus'

  export default {
    name: 'menu-link',
    props: {
      to: {
        type: String,
        default: '/'
      }
    },

    computed: {
      isExactActive() {
        return this.$route.fullPath == this.resolvedRoute.route.fullPath
      },

      resolvedRoute() {
        return this.$router.resolve(this.to)
      }
    },

    methods: {
      clicked() {
        if (this.isExactActive) {
          return EventBus.$emit('page-reload', this.resolvedRoute.route)
        }

        this.$router.push(this.to)
      }
    }
  }
</script>