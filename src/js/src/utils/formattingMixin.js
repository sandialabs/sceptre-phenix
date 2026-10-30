import { bytesText, mebibytesText } from '@/utils/bytes.js';
import { plural } from '@/utils/plural.js';
import { keptSearch } from '@/utils/searchHistory.js';

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
    // the distinct items, sorted, keeping only those keptSearch keeps
    getUniqueItems(items) {
      const kept = items.filter(keptSearch);
      return [...new Set(kept)].sort();
    },
  },
};
