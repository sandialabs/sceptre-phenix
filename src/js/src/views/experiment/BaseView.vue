<!-- 
This component will display an experiment available to the user; 
it will include a specific component rendering based on whether 
the experiment is running or not. If the user is VM Viewer role, 
this will only show a list of VMs that a user can view.
 -->

<template>
  <component :is="component"></component>
</template>

<script setup>
  import { usePhenixStore } from '@/store';
  import axiosInstance from '@/utils/axios';
  import { defineAsyncComponent } from 'vue';
  import { useRoute } from 'vue-router';

  const route = useRoute();
  const component = defineAsyncComponent(() => {
    let isRunning = window.history.state.running;
    if (isRunning === undefined) {
      console.log(
        'Could not get running status from history, manually querying',
      );
      return axiosInstance
        .get('experiments/' + route.params.id)
        .then((resp) => {
          getComponentAsync(resp.data.running);
        });
    } else {
      return getComponentAsync(isRunning);
    }
  });

  function getComponentAsync(isRunning) {
    if (isRunning) {
      if (usePhenixStore().role.name === 'VM Viewer') {
        return import('./VMtilesView.vue');
      } else {
        return import('./RunningExperimentView.vue');
      }
    } else {
      return import('./StoppedExperimentView.vue');
    }
  }

  //   methods: {
  //     showError(msg) {
  //       this.$buefy.toast.open({
  //           message: msg,
  //           type: 'is-danger',
  //           duration: 4000
  //         });
  //     },
  //   },

  //   data () {
  //     return {
  //       running: null
  //     }
  //   }
  // }
</script>
