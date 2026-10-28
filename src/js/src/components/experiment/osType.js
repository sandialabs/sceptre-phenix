// How the VM details card shows a VM's guest operating system, the os_type
// its topology node gives: a label, the family that colors it, and an icon.

// the icon of each family (see utils/icons.js for linux and windows)
const FAMILY_ICONS = {
  linux: 'linux',
  windows: 'windows',
  router: 'route',
  other: 'desktop',
};

// the os_type values the topology schema allows
const OS_TYPES = {
  linux: { label: 'Linux', family: 'linux' },
  centos: { label: 'CentOS', family: 'linux' },
  rhel: { label: 'RHEL', family: 'linux' },
  windows: { label: 'Windows', family: 'windows' },
  vyos: { label: 'VyOS', family: 'router' },
  vyatta: { label: 'Vyatta', family: 'router' },
  minirouter: { label: 'minirouter', family: 'router' },
  other: { label: 'Other', family: 'other' },
};

// The label, family and icon of an os_type, in any case; one the schema does
// not name is shown as given, in the other family. Null when there is none.
export function osTypeInfo(osType) {
  const text = String(osType ?? '').trim();
  if (!text) return null;
  const key = text.toLowerCase();
  const { label, family } = Object.hasOwn(OS_TYPES, key)
    ? OS_TYPES[key]
    : { label: text, family: 'other' };
  return { label, family, icon: FAMILY_ICONS[family] };
}
