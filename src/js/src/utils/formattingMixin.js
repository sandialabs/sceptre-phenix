import { bytesText, mebibytesText } from '@/utils/bytes.js';
import { plural } from '@/utils/plural.js';

export const formattingMixin = {
  methods: {
    formatLowercase(value) {
      return value?.toLowerCase() ?? '';
    },
    formatStringify(value) {
      if (value == null || value.length == 0) {
        return 'none';
      }
      return value.join(', ').toLowerCase();
    },
    // memory minimega gives in MiB: 1.5 GiB
    formatRAM(value) {
      return mebibytesText(value);
    },
    formatUptime(value) {
      if (value == null) {
        return value;
      }

      var uptime;
      var seconds = parseInt(value, 10);

      var days = Math.floor(seconds / (3600 * 24));
      seconds -= days * 3600 * 24;
      var hrs = Math.floor(seconds / 3600);
      seconds -= hrs * 3600;
      var mnts = Math.floor(seconds / 60);
      seconds -= mnts * 60;
      if (days >= 1) {
        uptime =
          plural(days, 'day') +
          ', ' +
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
    // a file's size as the rest of the UI gives bytes: 21.7 KiB
    formatFileSize(value) {
      return bytesText(value);
    },
    // the last part of a path, to list disk images by file name
    getBaseName(path) {
      return path.substring(path.lastIndexOf('/') + 1);
    },
    // the distinct items, sorted; those under four characters are dropped,
    // except "dnb", the search for VMs set not to boot
    getUniqueItems(items) {
      const kept = items.filter((item) => item.length >= 4 || item == 'dnb');
      return [...new Set(kept)].sort();
    },
  },
};
