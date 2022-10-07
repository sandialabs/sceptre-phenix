module.exports = {
  publicPath: process.env.VUE_BASE_PATH || '/',
  assetsDir: 'assets',

  devServer: {
    proxy: {
      '/phenix/api/v1': {
        target: 'http://localhost',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true
      },
      '/phenix/version': {
        target: 'http://localhost',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true
      }
    }
  }
}
