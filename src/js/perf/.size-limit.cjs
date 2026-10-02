// Bundle size budgets for the built UI (run `npm run build` in src/js first).
// Sizes are gzipped unless noted, matching what the phenix server sends.
// Raise a limit only deliberately, to about 5 to 10% above the size it
// guards, and say why in the pull request.
module.exports = [
  {
    name: 'Entry JS (loaded on every page)',
    path: '../dist/assets/index-*.js',
    gzip: true,
    limit: '155 kB',
  },
  {
    name: 'Entry CSS',
    path: '../dist/assets/index-*.css',
    gzip: true,
    limit: '52 kB',
  },
  {
    name: 'All JS (entry and lazy-loaded views)',
    path: '../dist/assets/*.js',
    gzip: true,
    // includes the State of Health layout libraries, which load only when a
    // layout is picked: ELK (elkjs, about 440 kB), dagre and cola.js
    limit: '1160 kB',
  },
  {
    name: 'Images (uncompressed)',
    path: '../dist/assets/*.{png,svg,jpg,jpeg,gif,webp}',
    brotli: false,
    limit: '120 kB',
  },
];
