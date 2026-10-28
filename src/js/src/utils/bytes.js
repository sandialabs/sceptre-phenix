// a byte count for people: 1.5 GiB
export function bytesText(bytes) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let value = bytes ?? 0;
  let unit = 0;
  // compares the value as shown, so 1048575 B reads 1.0 MiB, not 1024.0 KiB
  while (
    (unit ? Number(value.toFixed(1)) : value) >= 1024 &&
    unit < units.length - 1
  ) {
    value /= 1024;
    unit += 1;
  }
  return `${unit ? value.toFixed(1) : value} ${units[unit]}`;
}

// a size minimega gives in MiB (VM memory, host memory) for people: 1.5 GiB
export const mebibytesText = (mebibytes) =>
  bytesText((mebibytes ?? 0) * 1024 ** 2);
