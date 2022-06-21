module.exports = {
  publicPath: process.env.VUE_BASE_PATH || '/',
  assetsDir: 'assets',

  devServer: {
    proxy: {
      '/api/v1': {
        target: 'http://localhost:3000',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true
      }
    }
  }
}
