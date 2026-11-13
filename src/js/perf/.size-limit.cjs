// Bundle size budgets for the built UI (run `npm run build` in src/js first).
// Sizes are gzipped unless noted, matching what the phenix server sends.
// Raise a limit only deliberately, to about 5 to 10% above the size it
// guards, and say why in the pull request.
const fs = require('node:fs');
const path = require('node:path');

// The JS files only the Builder loads (builder-assets.json, see
// plugins/builder-assets.js), less the ELK files it shares with SoH.
const builderJS = JSON.parse(
  fs.readFileSync(path.join(__dirname, '../dist/builder-assets.json'), 'utf8'),
)
  .filter(
    (file) =>
      file.endsWith('.js') && !/^assets\/elk-(api|worker\.min)-/.test(file),
  )
  .map((file) => `../dist/${file}`);

module.exports = [
  {
    name: 'Entry JS (loaded on every page)',
    path: '../dist/assets/index-*.js',
    gzip: true,
    limit: '200 kB',
  },
  {
    name: 'Entry CSS',
    path: '../dist/assets/index-*.css',
    gzip: true,
    limit: '52 kB',
  },
  {
    name: 'All JS (entry and lazy-loaded views)',
    // less the files only the Builder loads, which the next budget guards
    path: ['../dist/assets/*.js', ...builderJS.map((file) => `!${file}`)],
    gzip: true,
    // includes the State of Health layout libraries, loaded when a layout is
    // picked: ELK (elkjs, about 460 kB, shared with the Builder), dagre, cola.js,
    // and the VNC files that the next budget guards
    limit: '1465 kB',
  },
  {
    name: 'VNC JS (lazy-loaded)',
    // noVNC (rfb, which only src/vnc/loadRfb.js imports), the console page,
    // the live tiles, the console link handler, and the console's Actions menu
    // and port forwards panel
    path: [
      '../dist/assets/rfb-*.js',
      '../dist/assets/Vnc*.js',
      '../dist/assets/openConsole-*.js',
    ],
    gzip: true,
    limit: '92 kB',
  },
  {
    name: 'Builder JS (lazy-loaded)',
    path: builderJS,
    gzip: true,
    // the Builder view with Vue Flow, JSON Forms and Ajv; the rest of the UI
    // never loads these files
    limit: '610 kB',
  },
  {
    name: 'Images (uncompressed)',
    path: '../dist/assets/*.{png,svg,jpg,jpeg,gif,webp}',
    brotli: false,
    limit: '120 kB',
  },
];
