/**
 * Terms and abbreviations that the UI shows (WCAG 3.1.3, 3.1.4), in the
 * alphabetical order of the Glossary page. Each `id` is the term's anchor, as
 * in `/glossary#vlan`. An `expansion` is the `title` of the term's `<abbr>`.
 */
export const glossary = [
  {
    id: 'phenix-app',
    term: 'app',
    definition:
      'A program that phēnix runs at each stage of an experiment, for example to configure VMs before they start. A scenario lists the apps of an experiment.',
  },
  {
    id: 'backing-image',
    term: 'backing image',
    definition:
      'A disk image that another disk image is built on. The newer image keeps only its changes, so it needs its backing image to work.',
  },
  {
    id: 'cc',
    term: 'CC',
    expansion: 'command and control',
    definition:
      'The minimega system that sends commands and files to VMs and gets the results back. It uses the miniccc agent in each VM.',
  },
  {
    id: 'cc-active',
    term: 'CC Active',
    definition:
      'Shows if the miniccc agent in a VM is connected to minimega. If it is not connected, phēnix cannot send commands or files to that VM.',
  },
  {
    id: 'cpu',
    term: 'CPU',
    expansion: 'central processing unit',
    definition:
      'The part of a computer that runs programs. For a VM, it is the number of virtual processors that the VM gets.',
  },
  {
    id: 'disk',
    term: 'disk',
    definition:
      'An image file that a VM uses as its hard drive or optical disc, such as a qcow2 or ISO file. The Disks page lists these files.',
  },
  {
    id: 'dnb',
    term: 'DNB',
    expansion: 'do not boot',
    definition:
      'A VM setting. The VM is part of the experiment, but phēnix does not start it.',
  },
  {
    id: 'experiment',
    term: 'experiment',
    definition:
      'A topology and an optional scenario that phēnix can deploy and run. When an experiment starts, minimega creates its VMs and networks.',
  },
  {
    id: 'gib',
    term: 'GiB',
    expansion: 'gibibytes',
    definition: 'A unit of data size. One gibibyte is 1,024 mebibytes.',
  },
  {
    id: 'hil',
    term: 'HIL',
    expansion: 'hardware in the loop',
    definition:
      'A real, physical device that is part of an experiment. The topology includes it as an external node, and phēnix does not start it as a VM.',
  },
  {
    id: 'host',
    term: 'host',
    definition:
      'A server in the minimega cluster that runs experiment VMs. The Hosts page shows the load and memory use of each host.',
  },
  {
    id: 'ip',
    term: 'IP',
    expansion: 'Internet Protocol',
    definition:
      'The rules that computers use to send data across a network. An IP address identifies one network interface.',
  },
  {
    id: 'iso',
    term: 'ISO',
    expansion: 'ISO 9660 optical disc image',
    definition:
      'A file that holds the contents of a CD or DVD. A VM can use it as an optical disc.',
  },
  {
    id: 'jwt',
    term: 'JWT',
    expansion: 'JSON Web Token',
    definition:
      'A signed text string that shows who you are after you sign in. phēnix sends it with each request to the server.',
  },
  {
    id: 'mib',
    term: 'MiB',
    expansion: 'mebibytes',
    definition: 'A unit of data size. One mebibyte is 1,048,576 bytes.',
  },
  {
    id: 'miniccc',
    term: 'miniccc',
    definition:
      'The minimega agent that runs inside a VM. It lets minimega send commands and files to the VM and get the results (command and control).',
  },
  {
    id: 'minimega',
    term: 'minimega',
    definition:
      'The tool that phēnix uses to run VMs and virtual networks. phēnix tells minimega what to start and stop.',
  },
  {
    id: 'nic',
    term: 'NIC',
    expansion: 'network interface card',
    definition:
      'A network connection of a computer or VM. Each NIC of a VM connects to one VLAN.',
  },
  {
    id: 'rbac',
    term: 'RBAC',
    expansion: 'role-based access control',
    definition:
      'A way to control access. Each user has a role, and the role sets what the user can see and change.',
  },
  {
    id: 'rdp',
    term: 'RDP',
    expansion: 'Remote Desktop Protocol',
    definition:
      'A protocol that lets you see and use the desktop of a remote Windows computer.',
  },
  {
    id: 'scenario',
    term: 'scenario',
    definition:
      'A config that lists the apps of an experiment and their settings. You can use the same scenario with more than one topology.',
  },
  {
    id: 'scorch',
    term: 'SCORCH',
    expansion: 'scenario orchestration',
    definition:
      'A phēnix app that runs a set of components in stages on a running experiment, for example to capture data. It can run the same steps many times.',
  },
  {
    id: 'snapshot',
    term: 'snapshot',
    definition:
      'A saved copy of a VM or disk at one point in time. A disk snapshot is a new disk image that uses the original image as its backing image.',
  },
  {
    id: 'soh',
    term: 'SoH',
    expansion: 'state of health',
    definition: 'See state of health.',
  },
  {
    id: 'state-of-health',
    term: 'state of health',
    definition:
      'A phēnix app that checks a running experiment, for example if VMs can reach each other. It shows the results on a network graph.',
  },
  {
    id: 'tap',
    term: 'tap',
    definition:
      'A network interface on a host that connects the host to an experiment VLAN. The tap app uses taps to give an experiment access to external networks.',
  },
  {
    id: 'topology',
    term: 'topology',
    definition:
      'A config that describes the network of an experiment: its nodes, such as VMs, routers, and external devices, and how they connect.',
  },
  {
    id: 'tunneler',
    term: 'Tunneler',
    definition:
      'The phenix-tunneler program, which you run on your own computer. It forwards traffic from a port on a VM to a local port, for example for an RDP client.',
  },
  {
    id: 'vlan',
    term: 'VLAN',
    expansion: 'virtual local area network',
    definition:
      'A separate network that connects a group of VMs, as if they were connected to one switch.',
  },
  {
    id: 'vlan-alias',
    term: 'VLAN alias',
    definition:
      'The name that a topology uses for a VLAN, such as MGMT. phēnix maps each alias to a VLAN number.',
  },
  {
    id: 'vm',
    term: 'VM',
    expansion: 'virtual machine',
    definition: 'A computer that runs as software on a host.',
  },
  {
    id: 'vnc',
    term: 'VNC',
    expansion: 'Virtual Network Computing',
    definition:
      'A system to see and control the screen of a remote computer. phēnix uses noVNC to show the screens of VMs in the browser.',
  },
  {
    id: 'yaml',
    term: 'YAML',
    expansion: "YAML Ain't Markup Language",
    definition: 'A text format for data. phēnix configs use YAML or JSON.',
  },
];

/** The order of the glossary: alphabetical, without regard to case. */
export function compareTerms(a, b) {
  return a.localeCompare(b, 'en-US', { sensitivity: 'base' });
}
