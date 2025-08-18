export const formattingMixin = {
  methods: {
    formatLowercase(value) {
      if (value === null) {
        return value;
      }
      return value.toLowerCase();
    },
    formatStringify(value) {
      if (value == null || value.length == 0) {
        return 'none';
      }
      return value.join(', ').toLowerCase();
    },
    formatRAM(value) {
      if (value == 0) {
        return '0 Byte';
      }
      let size = ['MB', 'GB', 'TB'];
      let i = parseInt(Math.floor(Math.log(value) / Math.log(1024)));
      let output = Math.round(value / Math.pow(1024, i), 2) + ' ' + size[i];
      return output;
    },
    formatUptime(value) {
      if (value == null) {
        return value;
      }

      var uptime = null;
      var seconds = parseInt(value, 10);

      var days = Math.floor(seconds / (3600 * 24));
      seconds -= days * 3600 * 24;
      var hrs = Math.floor(seconds / 3600);
      seconds -= hrs * 3600;
      var mnts = Math.floor(seconds / 60);
      seconds -= mnts * 60;
      if (days >= 1) {
        uptime =
          days +
          ' days, ' +
          ('0' + hrs).slice(-2) +
          ':' +
          ('0' + mnts).slice(-2) +
          ':' +
          ('0' + seconds).slice(-2);
      } else {
        uptime =
          ('0' + hrs).slice(-2) +
          ':' +
          ('0' + mnts).slice(-2) +
          ':' +
          ('0' + seconds).slice(-2);
      }
      return uptime;
    },
    formatFileSize(value) {
      if (value < Math.pow(10, 3)) {
        return value.toFixed(2) + ' B';
      } else if (value >= Math.pow(10, 3) && value < Math.pow(10, 6)) {
        return (value / Math.pow(10, 3)).toFixed(2) + ' KB';
      } else if (value >= Math.pow(10, 6) && value < Math.pow(10, 9)) {
        return (value / Math.pow(10, 6)).toFixed(2) + ' MB';
      } else if (value >= Math.pow(10, 9)) {
        return (value / Math.pow(10, 9)).toFixed(2) + ' GB';
      }
      return value;
    },
  },
};
