module.exports = {
  publicPath: process.env.VUE_BASE_PATH || '/',
  assetsDir: 'assets',

  devServer: {
    proxy: {
      '/api/v1': {
        target: 'http://localhost:9090',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true
      },
      '/version': {
        target: 'http://localhost:9090',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true
      },
      '/features': {
        target: 'http://localhost:9090',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true
      }
    }
  }
}
