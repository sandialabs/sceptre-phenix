# phenix web

Vue 3 single-page application leveraging pinia, vue-router, and axios libraries.

## Project Setup

Requires node 24. It is recommended to install node using [nvm](https://github.com/nvm-sh/nvm).

```
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
nvm install 24
nvm use 24
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

**Run tests**

```
npm run test
```

## Code Details

### Structure

- `src`
    - `assets`: images and css
    - `components`: UI components that are used to make full views. These should be relatively small and may be reusable
    - `utils`: Various helper functions imported in Vue files
    - `views`: Full UI pages, typically referenced in `router.js`. May use components
    - `App.vue`: Base UI page that contains common elements (header, footer). Shows a single `view` page at a time
    - `main.js`: Creates the Vue app
    - `router.js`: Defines routes and hooks using `vue-router`
    - `store.js`: Defines the `pinia` store
- `test`: unit tests
- `*.env`: Files which define environment variables used during build
- `index.html`: Base page loaded by browser. Loads the rest of the app
- `vite.config.js`: Defines parameters for building

### Dependencies Used

- Using vue3 (recently upgraded from vue2)
- `pinia`: store for state. Replaces `vuex` in vue2. Obtain an instance by calling `usePhenixStore()`
- `axios`: http library. Replaces `vue resource` in vue2. Make calls using `axiosInstance`
- `vue-router`: handles routing within app
- `vite`: build tools. Replaces `vue-cli` in vue2
- `Buefy`: UI library. Recently upgraded to support vue3
- `Bulma`: css library used by Buefy
    - Note: currently Buefy uses Bulma 0.9.4. See docs here: https://versions.bulma.io/0.9.4/documentation/

### Notes

- Font Awesome icons are imported individually in `main.js` to reduce bundle size. Add any new icons there.

### Themes

The application loads `/theme.js` before its styles to apply the initial
theme without a flash. Theme resolution order is:

1. `phenix.theme` in browser-local storage (`light` or `dark`).
2. The server's `ui.default-theme` value (`system`, `light`, or `dark`).
3. `prefers-color-scheme` when the server default is `system`.

The header toggle changes only the browser-local choice. The Settings view
changes the shared server default unless `phenix ui --default-theme` was
explicitly supplied.

All runtime assets are bundled or same-origin; theme behavior requires no
network access. The sun and moon SVG icons come from Font Awesome Free,
copyright Fonticons, Inc., and are locally bundled under the CC BY 4.0 icon
license. Font Awesome Free describes this distribution as GPL friendly.
