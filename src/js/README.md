# Phenix VUE 3

## Installation

Requires node 22. Recommended to install node using [nvm](https://github.com/nvm-sh/nvm).

```
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
nvm install --lts
nvm use --lts
cd /project-dir
npm run dev
```

## MAJOR DIFFERENCES

- vue 2 -> vue3, obviously
- store vuex -> pinia
  - instead of this.$store.commit.. we import a function usePhenixStore, call it inside the method we want to use the store, and use the store from there. See Hosts line 76 and 116 for an example of getting
- http requests vue resource -> axios
  - vue resource is vue 2 only. seems like standard is to use axios. minor changes to syntax. similar to pinia we'll import an axiosInstance in each component that does requests and use the requests with that. error response is also separated out into a catch method -- see Hosts.vue line 94 for an example
  - Data is now at `response.data` already in json
- Websockets: now rolling our own rather than a library
  - In a view, import and call `addWsHandler(handler)` and `removeWsHandler(handler)` in place of `this.$options.sockets.onmessage`. The handler should accept json objects
- Build tool vue-cli -> vite
  - just a vue 3 thing. builds the dev server pretty fast
- yarn -> npm
  - this one doesn't really matter at all; we can always pivot to yarn
- filters -> mixins
  - vue 2 has filters where you can apply a method on a piece of data. like
  - {{ experiment_name | lowercase }} where experiment_name is a string variable and lowercase is the filter to make it all lowercase. Now I just have a mixin class (a set of global variables you can import into a component) that has all the formatting that used to be done with the filters. See Hosts.vue lines 78, 82, and 40
- rbac now has to be imported `import { roleAllowed } from '@/utils/rbac';`
- errorNotification now has to be imported
- EventBus doesn't exist
  - any component that uses EventBus now has to have the line at top of script:
    `import EventBus from '@/utils/eventBus.js'`.
  - have to import a different package to get the same functionality (before reassessing what it does.. vue3 docs says eventbus is bad practice)
- moved css from inside app.vue to assets/main.scss
- Attempting to structure it so that different pages are in views/ and components used in those pages are in components/. This one might have to wait until the core functionality is working
- VUE_APP_AUTH is now VITE_AUTH
  - view router/index.js line 49 for example
- Some buefy components now require you to set `v-model`
  - `b-select`: `value` -> `v-model`
  - `b-modal`: `active.sync` -> `v-model`
- Empty `<template>` tags (e.g., no `v-if` or `v-for`) no longer render. Remove the empty tag

## Useful References

- Bulma `0.9.3` docs: https://versions.bulma.io/0.9.3/documentation/customize/variables/ (we're on 0.9.4, but there isn't a page for that version)
- Buefy Next docs: https://v3.buefy.org/documentation/
- Buefy Next issues: https://github.com/ntohq/buefy-next/issues

## TODO List

### General / Bugs

- [ ] remove eventBus?
- [ ] Docker/readmes/docs/etc
- [ ] cleanup file organization

### Public Phenix

#### Core functionality

- [x] router
- [x] rbac
- [x] requests (axios)
- [x] store
- [x] css
- [x] header
- [x] footer
- [x] websockets

### Pages

- [x] Experiments
  - [x] Experiment
  - [x] StoppedExperiment
  - [x] RunningExperiment
  - [x] Mount modal - Jacob
  - [x] File tabs - Jacob
  - [x] VM Tiles
- [x] Configs - Connor
  - [x] Config List
  - [x] Edit Config Window
- [x] Hosts
- [x] Users
- [ ] Logs (new version)
- [ ] Scorch - Connor
  - [x] Scorch List
  - [x] Scorch pipeline
  - [ ] Scorch interactivity
  - [ ] Get someone who uses scorch to test
- [x] Experiment Builder
- [x] Console
- [x] SignIn
- [ ] Tunneler - Jacob
- [x] SOH
  - [x] SOH Graph
  - [x] SOH color picker
  - [x] SOH labels

### Not yet public phenix but in merge requests

#### Pages

- [ ] Disks - Jacob
- [ ] Settings - Connor

### THE local

#### Pages

- [ ] Image Builder - Connor
