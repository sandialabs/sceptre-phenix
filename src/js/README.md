# phenix web

Vue 3 single-page application leveraging pinia, vue-router, and axios libraries.

## Project Setup

Requires node 22. It is recommended to install node using [nvm](https://github.com/nvm-sh/nvm).

```
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
nvm install 22
```

Run `npm install` afterwards to install all modules defined in `package.json`

## Useful commands

**Run the development server with hot-reload:**

(Be sure to have a regular build of the phenix backend running to connect to)
```
npm run dev
```


**Compile for Production**

(Or use `make dist/index.html`)
```
npm run build
```

**Format Code**
```
npm run format
```

## Code Details

### Structure

* `src`
  * `assets`: images and css
  * `components`: UI components that are used to make full views. These should be relatively small and may be reusable
  * `utils`: Various helper functions imported in Vue files
  * `views`: Full UI pages, typically referenced in `router.js`. May use components
  * `App.vue`: Base UI page that contains common elements (header, footer). Shows a single `view` page at a time
  * `main.js`: Creates the Vue app
  * `router.js`: Defines routes and hooks using `vue-router` 
  * `store.js`: Defines the `pinia` store
* `*.env`: Files which define environment variables used during build
* `index.html`: Base page loaded by browser. Loads the rest of the app
* `vite.config.js`: Defines parameters for building


### Dependencies Used

* Using vue3 (recently upgraded from vue2)
* `pinia`: store for state. Replaces `vuex` in vue2. Obtain an instance by calling `usePhenixStore()`
* `axios`: http library. Replaces `vue resource` in vue2. Make calls using `axiosInstance`
* `vue-router`: handles routing within app
* `vite`: build tools. Replaces `vue-cli` in vue2
* `Buefy`: UI library. Recently upgraded to support vue3
* `Bulma`: css library used by Buefy
  * Note: currently Buefy uses Bulma 0.9.4. See docs here: https://versions.bulma.io/0.9.4/documentation/

### Notes
* Font Awesome icons are imported individually in `main.js` to reduce bundle size. Add any new icons there