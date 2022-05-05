module.exports = {
  publicPath: process.env.VUE_BASE_PATH || '/',
  assetsDir: 'assets',

  devServer: {
    proxy: {
      '/api/v1': {
        target: 'http://10.2.0.40:3000',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true
      }
    }
  }
}
