# Building a Diagram

This page shows how to draw and change a diagram: devices, switches and the
connections between them, device settings, network addresses, groups, notes,
shapes, icons and lines, colors, line styles, custom icons, layers, layouts
and the scenarios. For the
parts of the editor that these tasks use, see [The Editor](editor.md).

The examples on this page use the Riverside Water draft of the
[example lab](index.md#the-drafts-on-these-pages).

## Devices, switches and connections

A diagram describes one phenix Topology config. Each part of the diagram maps
to the topology like this:

| In the diagram | In the published topology |
|---|---|
| A device | A node in `spec.nodes`, with all its settings, its notes in `general.notes` |
| A switch | A network. The network's name is the VLAN of every interface on it. The switch's notes stay in the diagram |
| A connection from a device to a switch | An interface of the device, whose `vlan` is the network's name |
| A device from an included topology | Nothing: `includeTopologies` names that topology |
| A note or a group | Nothing: notes and groups only help people read the diagram |
| A shape, an icon or a line | Nothing: they are drawings in the diagram |
| A network's **Subnet** and **Gateway** | Nothing: they stay in the Builder document. The addresses that Builder gives interfaces from them are in the interfaces |
| Colors, line styles, icon sizes, custom icons, layers and the diagram's templates | Nothing: they stay in the Builder document |

For example, the connection from ws-01 to the CORP switch is this interface
of ws-01:

```yaml
network:
  interfaces:
    - name: eth0
      type: ethernet
      vlan: CORP
      proto: static
      address: 10.10.20.101
      mask: 24
      gateway: 10.10.20.1
```

A connection always joins a device to a switch. Devices never connect
directly to each other. When you draw a connection from one device to
another, Builder adds a switch between them (see
[Connecting interfaces](#connecting-interfaces)).

## Adding devices

**Nodes & Objects**, at the top of the left column, lists what you can add.
To add a device:

- Select the item. The device appears in free space in the part of the
  canvas that you see. When that part has no free space, the device appears
  in free space outside it. Builder selects the device, so the Inspector
  shows its fields.
- Or drag the item to where you want it on the canvas.
- Or, in the command palette, choose **Add device**, then a template (see
  [Command palette](editor.md#command-palette)).

**Device** adds a device with default settings. The device templates under
**Templates** set more settings. The **Built-in** section of **Templates**
always lists the five built-in templates. This table shows **Device** and
the built-in templates:

| Item | Hostname | Type | OS type | Image | Description (its tooltip) |
|---|---|---|---|---|---|
| **Device** | node | VirtualMachine | linux | `ubuntu.qc2` | A virtual machine, container or external device |
| **Server** | server | VirtualMachine | linux | `ubuntu.qc2` | Generic Linux server |
| **Workstation** | workstation | VirtualMachine | windows | `windows10.qc2` | Operator workstation |
| **Router** | router | Router | minirouter | `minirouter.qc2` | Layer 3 router |
| **Firewall** | firewall | Firewall | vyos | `vyos.qc2` | Perimeter firewall |
| **External device** | external | HIL (external) | None | None | Hardware in the loop device |

The built-in templates are read only. To change one, duplicate it into the
template library of the server. You can also make your own templates, in a
diagram or in the template library, which all users of the server share
(see [Node Templates](templates.md) and
[Templates in the editor](templates.md#templates-in-the-editor)).

When the hostname is taken, the new device gets a number, for example
server-2. A new device has no interfaces, so the checks warn "device
"workstation" has no interfaces" until you add one.

For example, to add an engineering workstation to Riverside Water:

1. Under **Templates**, select **Workstation**. A device named workstation
   appears.
2. In the Inspector, set **Hostname** to `ws-03` and **Description** to
   `CAD workstation`.
3. Select **Apply**.

To connect ws-03 to the CORP network, see
[Connecting interfaces](#connecting-interfaces).

### Routers and firewalls

The **Router** template runs minirouter, on `minirouter.qc2`. The
**Firewall** template runs VyOS: **OS type** `vyos` and the image
`vyos.qc2`. phenix's `vrouter` app configures the interfaces, routes and
rulesets of a device whose type is Router or Firewall (see
[vrouter App](../apps.md#vrouter-app)). For a VyOS router, such as
edge-rtr, set **OS type** to `vyos` and the drive's **Image** to `vyos.qc2`.

### External devices

An external device is hardware that the experiment connects to, such as
plc-01, the pump station PLC of the example lab. phenix does not start a VM
for it, so it has no image. Give it interfaces as for any other device: they
tell phenix which VLANs the hardware is on. See
[External Nodes](../configuration.md#external-nodes).

## Adding switches and networks

A switch is one network. To add a network:

1. Under **Nodes & Objects**, select **Switch**, or choose **Add switch**
   in the command palette. A switch appears, with a new network named EXP
   (EXP-2 for the next one, and so on). The new network gets a subnet that
   no other network of the diagram uses (see
   [Subnets and addresses](#subnets-and-addresses)).
2. In the Inspector, set **Name** to the network's name, for example
   `SCADA`, and select **Apply**.

When you rename a network, the VLAN of every interface on it changes.

On the canvas, a switch shows its icon and its name, which is the name of
its network. Its outline has the color of its network's connections. An
**Outline Color** of the switch itself goes first (see [Colors](#colors)).
A screen reader names the switch, its network and its VLAN alias.

The Inspector of a switch, for example "Network CORP", has these fields:

- **Name**: the network's name, which is the VLAN of the interfaces on it.
- **VLAN alias**: an optional number from 1 to 4094. When you publish the
  diagram with an experiment, it becomes that network's entry in the
  experiment's `vlans.aliases` (see
  [Publishing a topology and an experiment](publishing.md#publishing-a-topology-and-an-experiment)).
- **Subnet**: an optional IPv4 network in CIDR form, such as
  `10.1.0.0/24` (see [Subnets and addresses](#subnets-and-addresses)).
- **Gateway**: an optional IPv4 address of the router on this network. It
  must be a host address of the subnet, such as `10.1.0.1` in
  `10.1.0.0/24`.
- **Description**.
- **Edge Color**, the network's color, and **Line style**, the pattern of
  its connections (see [Colors](#colors) and [Line styles](#line-styles)).
- **Outline Color** and **Fill Color** of this switch.
- **Icon size** of this switch (see [Icon size](#icon-size)).
- **Notes** of this switch, which stay in the diagram (see
  [Notes on devices and switches](#notes-on-devices-and-switches)).
- Below these fields: **Layer** (see [Layers](#layers)),
  **Addresses** with **Assign addresses** (see
  [Subnets and addresses](#subnets-and-addresses)), and **Position**.

For example, to give CORP the VLAN alias 120, select the CORP switch, type
`120` in **VLAN alias**, and select **Apply**.

![The Inspector for network CORP. Its header names Network CORP and has the Add connection, Move to group and Open Node Editor (a pencil) buttons, all of one size. Below: a note that CORP cannot be renamed because ntp-01 from the included topology corp-services is on it, Name CORP (read only), VLAN alias 120 marked as changed, empty Subnet and Gateway fields, Description, Edge Color, Line style, Outline Color, Fill Color, Icon size Diagram default (Small), Notes with No notes. and Add note, Unapplied changes with Apply and Cancel, Layer None with its hint and the Edit layers… button, Addresses with This network has no subnet. Apply a Subnet to assign addresses. and an unavailable Assign addresses button, and Position.](../images/builder/inspector-network.png){ width="352" }

A network that a device from an included topology is on cannot be renamed or
removed. Its other fields can still change. In Riverside Water, that is CORP,
where ntp-01 and dns-01 are.

To find a network, type `#` in the command palette (see
[Command palette](editor.md#command-palette)). **Remove network** in the
command palette removes a network, its switches and their connections.
A network can have more than one switch on the canvas. For example, a
pasted switch is another switch of the same network.

When you delete the last switch of a network, Builder also removes the
network in the same edit. **Undo** restores the switch and the network.
The network stays when the **VLAN** of an interface with no connection on
the canvas still names it. The checks then warn that the network has no
switch. To correct this, add a switch for the network, change the VLAN, or
use **Remove network**. A diagram that you open or upload keeps all its
networks.

### Subnets and addresses

A network can have a **Subnet** and a **Gateway**. Builder uses them to
give addresses to the static interfaces on the network. A static interface
has the **Interface kind** **Static or OSPF** (see
[Editing device settings](#editing-device-settings)). Builder gives an
address only to a static interface that has no address. It never changes an
address that an interface has, and it never changes a device from an
included topology.

An assigned address is the next free host address of the subnet. A free
address is not the network's gateway, and no other interface on the network
uses it. The interface also gets the subnet's prefix length as its
**Mask**. When the interface has no **Gateway**, it gets the network's
gateway. For example, on a network with the subnet `10.1.0.0/24` and the
gateway `10.1.0.1`, the first interface gets `10.1.0.2`, mask `24` and
gateway `10.1.0.1`.

A new network gets a suggested subnet: the first `10.N.M.0/24` that
overlaps no subnet of the diagram and holds no address of an interface or a
gateway of the diagram. For example, the first new network of an empty
diagram gets `10.1.0.0/24`. Change the subnet or clear it in the Inspector.
A network that you import or upload keeps its own subnet, or has none.

Builder gives addresses at these times:

- When you connect a static interface to a switch of the network: with a
  drag, with **Add a connection**, or with **Paste** or **Duplicate** of a
  device and its switch. After a drag or **Add a connection**, Builder says
  the address, for example "Set the address of eth0 of ws-03 to
  10.1.0.2/24."
- When you select **Assign addresses** in the Inspector of a switch, or
  choose **Assign addresses** and then the network in the command palette.
  Builder gives an address to each static interface on the network that
  has no address, as one step of **Undo**, for example "Assigned 3
  addresses on network SCADA".

![The Inspector for network CORP with Subnet 10.10.20.0/24 and Gateway 10.10.20.1. Below Layer, Addresses says Select Assign addresses to give each static interface on this network that has no address the next free address in 10.10.20.0/24., above the Assign addresses button.](../images/builder/inspector-network-subnet.png){ width="352" }

The **Assign addresses on connect** setting, in the **Editing** section of
[Settings](editor.md#settings), is on by default. When it is off, a new
connection, paste or duplicate gives no address. After a drag or **Add a
connection**, Builder then says "No address set. Type an
address for eth0 of ws-03 in the Inspector, or select Assign addresses."
**Assign addresses** works when the setting is on or off. A new network
gets its suggested subnet in both cases.

A network without a subnet gives no address. When you connect a static
interface to it, Builder says "No address set: network CORP has no subnet.
Type an address for eth0 of ws-03 in the Inspector." **Assign addresses**
is then not available, and the Inspector says "This network has no subnet.
Apply a Subnet to assign addresses." When the subnet has no free address
left, Builder says so, for example "No address set: subnet 10.1.0.0/30 of
network SCADA is full."

A static interface without an address stops **Apply** of its device.
Type an address in the device's Inspector, or use **Assign addresses**.

Publishing writes no subnet and no gateway of a network. The addresses
that Builder gave the interfaces are in the interfaces, so publishing
writes them.

## Connecting interfaces

A connection puts a device's interface on a network. You can make one in
three ways. Each example connects ws-03 (see
[Adding devices](#adding-devices)) to the CORP network. CORP of Riverside
Water has no subnet, so Builder gives eth0 no address. Type its address in
the Inspector of ws-03, for example `10.10.20.103`, or give CORP a subnet
first (see [Subnets and addresses](#subnets-and-addresses)).

### Drag on the canvas

1. Point at ws-03. Each interface has a connection point (a circle) on the
   device's sides, and a **+** handle at its bottom ("Drag to connect ws-03
   on a new interface").
2. Drag from the **+** handle, or from an unconnected connection point, to
   the CORP switch.

A drag from **+** adds a new interface to the device. The new interface is
a static interface, so it gets the next free address of the network's
subnet (see [Subnets and addresses](#subnets-and-addresses)). The new
connection appears on the canvas. A published diagram and a device from an
included topology have no **+** handle.

A drag from one device to another adds a new switch between them, with a
new network named EXP and a suggested subnet, and connects both devices to
it. Rename the network in the switch's Inspector. You cannot connect two
switches, because each switch is one network.

### Add a connection without dragging

To connect without dragging, select a device or a switch. Then select
**Add connection** (the link button) in the Inspector's header, to the left
of **Open Node Editor**. You can also choose **Add a connection…** in the
[command palette](editor.md#command-palette). The command has no default
key, but you can give it one (see
[Customizing shortcuts](editor.md#customizing-shortcuts)). The **Add a
connection** dialog opens with the selection filled in: a selected device
in **Device**, a selected switch in **Switch**.

1. In **Device**, choose `ws-03`.
2. Keep **Interface** set to **Add a new interface**, or choose an interface
   that has no connection yet.
3. In **Switch**, choose **CORP (CORP)**.
4. Select **Connect**.

The dialog before step 4:

![The Add a connection dialog with Device ws-03, Interface Add a new interface and Switch CORP (CORP), and the Cancel and Connect buttons.](../images/builder/connect-dialog.png){ width="496" }

The dialog closes and focus returns to where it was before the dialog
opened. A static interface with no address gets an address, as for a
drag. The dialog stays open in these cases:

- A field is empty. **Connect** says which one and moves focus to it.
- Builder cannot make the connection. The dialog says why.

**Cancel** or <kbd>Esc</kbd> closes the dialog without a change.

### Type the VLAN in the Inspector

When you type a network's name in an interface's **VLAN** field and apply
it, Builder connects the interface to that network's switch. This is how the
[quick start](index.md#quick-start) connects ws-03:

1. Select ws-03.
2. In the Inspector, under **Network**, select **Add interface**. The new
   interface is named eth0. Its **Interface kind** is **Static or OSPF**,
   with **Mask** `24`, and focus goes to its **Address**.
3. Enter **Address** `10.10.20.103` and **Gateway** `10.10.20.1`.
4. Enter **VLAN** `CORP`.
5. Select **Apply**. eth0 connects to the CORP switch. **Draft History**
   names the change "Updated device ws-03 and connected eth0 to network
   CORP".

The interface before step 5:

![Interface eth0 of ws-03 in the Inspector, in field order: Address 10.10.20.103, Mask 24, Gateway 10.10.20.1, Protocol static, Name eth0, VLAN CORP, and an empty MAC address field with its Generate a random MAC address button. The changed fields are marked as changed, and Unapplied changes with Apply and Cancel is at the bottom.](../images/builder/quickstart-ws03.png){ width="354" }

The VLAN matches a network's name in any letter case. When you type the name
of another network, the connection moves to that network. When the VLAN
names no network of the diagram, Builder keeps it, but the interface stays
unconnected. A VLAN that you type gives no address from the network's
subnet. Type the address, or use **Assign addresses** (see
[Subnets and addresses](#subnets-and-addresses)).

### Disconnecting

- In the Inspector's **Connection points**, select **Disconnect** next to
  the interface, for example "eth0 — network CORP".
- Or select the connection on the canvas and press <kbd>⌫</kbd> on macOS or
  <kbd>Delete</kbd> on Windows and Linux.

The interface stays, with no VLAN. An interface with no VLAN blocks
publishing (see [What blocks publishing](publishing.md#what-blocks-publishing)).
Connect it again, type a VLAN for it, or remove it. To remove it, select
the trash button next to it in **Connection points** (**Remove connection
point** and its name). **Add connection point** adds a static interface
without a connection and without an address. The buttons under
**Connection points** take effect at once, without **Apply**.

## Editing device settings

Select a device to change its settings in the Inspector. Change the fields,
then select **Apply**, or press <kbd>Enter</kbd> in a field. See
[Editing a node](editor.md#editing-a-node) for how the fields and **Apply**
work. For more room, double-click the device to open the
[Node Editor](editor.md#node-editor), a large dialog with the same fields.

An interface has an **Interface kind**:

- **Static or OSPF**: an address that you give, or that Builder gives from
  the network's subnet (see [Subnets and addresses](#subnets-and-addresses)).
  A new interface starts as this kind, with **Mask** `24`. Its fields start
  with **Address**, **Mask** and **Gateway**, then **Protocol**, **Name**,
  **VLAN**, **MAC address** and the other fields. When you add an interface
  in the Inspector, focus goes to its **Address**. A static interface needs
  an **Address** before you can apply the device.
- **DHCP or manual**: the address comes from DHCP, or is set inside the VM.
- **Serial**: a serial link.

The interface of an external device has no **Interface kind**. It is a
static interface with **Mask** `24`, and its fields start with
**Protocol**, then **Address**, **Mask** and **Gateway**.

When you change the kind, Builder asks "Switch Interface kind to DHCP or
manual?" (or the kind you chose). The dialog says "This change clears the
Interface kind values that you entered." **Switch and clear** changes the kind
and empties the fields of that interface, **Name** and **VLAN** included.
**Keep current value** does not change the interface. Set the kind first,
then enter the other fields.

To give an interface a random MAC address, select the button beside
**MAC address** (**Generate a random MAC address**). Builder writes in the
field a locally administered unicast address that no other interface of
the diagram uses, and says it, for example "Generated MAC address
02:5e:a1:07:c3:9b." Select **Apply** to keep it.

### Duplicating a device

**Duplicate** pastes a copy of the selected nodes next to them (see
[Copy, paste, duplicate and delete](#copy-paste-duplicate-and-delete)). The
copy keeps every setting, addresses included, but not its connections.

For example, to add a second historian:

1. Select historian-01.
2. Press <kbd>⌘</kbd>+<kbd>D</kbd> (<kbd>Ctrl</kbd>+<kbd>D</kbd>). A copy
   named historian-01-2 appears, with the address 10.10.30.20 and an eth0
   that is not connected.
3. The header now says **1 warning**: eth0 of historian-01-2 has no VLAN
   (see [Checks and warnings](editor.md#checks-and-warnings)). Both
   historians use 10.10.30.20. This also becomes a warning when the eth0 of
   the copy is on the network of historian-01.

The Riverside Water expansion draft is a copy of Riverside Water after
step 2 (see [The drafts on these pages](index.md#the-drafts-on-these-pages)).
To keep Riverside Water unchanged, select **Undo** after step 3. To fix the
warning, give the copy its own hostname and address, and connect it: see
[Example: Riverside Water expansion](publishing.md#example-riverside-water-expansion).

### Drive images

Each drive has an **Image**. A drive's fields are in this order:
**Image**, **Inject partition**, **Cache mode**, **Interface**, then the
other fields. When your role can list the disk images of the server, the
**Image** field suggests them, and a drive image that the server does not
have is a warning. The checks do not look at images when the server lists
none (see [Checks and warnings](editor.md#checks-and-warnings)).

### Routes and rulesets

**Routes** and **Firewall rulesets** are under **Network**. For example:

- edge-rtr has **Route 1: 10.10.30.0/24**, with **Destination**
  `10.10.30.0/24`, **Next hop** `10.10.20.254` (ot-fw) and **Cost** `1`.
  **Add route** adds another.
- ot-fw has **Firewall ruleset 1: corp-to-ot**, with **Default** `drop` and
  **Rule 1: HTTPS to the historian**, which accepts `tcp` from
  `10.10.20.0/24` to `10.10.30.20` port `443`. **Add firewall ruleset** adds
  another. eth0 uses the ruleset: its **Inbound ruleset** is `corp-to-ot`.

The `vrouter` app applies them when the experiment starts (see
[vrouter App](../apps.md#vrouter-app)).

**miniccc commands**, under **More settings**, lists the commands that
phenix gives miniccc to run in the VM, such as `exec df -h`. **Add miniccc
command** adds one.

## Labels and annotations

A device's labels and annotations are under **More settings**, at the bottom
of its fields. The summary line counts them, for example "Labels (2),
Annotations (1)". Each row has a **Name** and a **Value**, and a remove
button.

For example, web-01 has the labels `zone: dmz` and `role: web`, and the
annotation `phenix/startup-autotunnel` with the value `["8080:80"]`:

![The Labels and Annotations sections of web-01 in the Inspector's More settings, each with a collapse arrow before its name: two labels, role web and zone dmz, and the annotation phenix/startup-autotunnel with a list holding 8080:80, each row with Name and Value fields and a remove button, and the Add label and Add annotation buttons.](../images/builder/inspector-labels.png)

To add a label:

1. Select web-01, and open **More settings**.
2. Under **Labels**, select **Add label**.
3. Enter **Name** `team` and **Value** `blue`, and select **Apply**.

**Add annotation** works the same way. Builder reads the value of an
annotation as YAML: `true`, `false` and numbers become those values, and
`["8080:80"]` is a list. Apps read annotations to configure a node (see
[Apps](../apps.md)).

## Notes

A note is text on the canvas. Publishing ignores it.

1. Under **Nodes & Objects**, select **Note**.
2. In the Inspector, enter the **Text**, for example `OT has no route to the
   internet. From CORP, only HTTPS to historian-01 passes ot-fw.`
3. Select **Apply**.

A screen reader names a note after the start of its text. A note also has
a **Color** (see [Colors](#colors)).

### Notes on devices and switches

A device or a switch can have notes of its own. The canvas shows them in a
card below the node: one line for each note, each cut after three lines,
and at most five notes, then "+N more". The card moves with the node, and
Builder selects it with the node. The node's info tooltip lists the notes,
and screen readers read them as part of the node's description.

1. Select the device or the switch.
2. In the Inspector, under **Notes** (for a device, in the **General**
   section), select **Add note** and type the note. Each note has a box of
   its own, and **Remove** removes one.
3. Select **Apply**.

For example, the pump station of
[pump-station.builder.json](examples/pump-station.builder.json) after adding
two notes to rtu-01 and one to the STATION switch:

![Part of the pump station diagram: below rtu-01, a card with the notes Modbus TCP on port 502 and Firmware 2.4: update it before the exercise; below the STATION switch, which shows only its name in a blue outline, a card with the note Field network, no route to the internet.](../images/builder/node-notes.png)

A device's notes are its node's `general.notes`. Publishing writes them to
the topology, and a new experiment copies them to the VM's notes (see
[Node notes](../configuration.md#node-notes)). A switch is not part of the
topology, so its notes stay in the diagram. A device or a switch holds at
most 100 notes. Each note of a device holds at most 4096 characters, and
each note of a switch at most 4096 bytes. **Show node notes** in the
Settings, or **Show or hide node notes** in the command palette, hides the
cards (see [Settings](editor.md#settings)). While the cards show:

- A layout leaves room for them.
- A group holds the cards of its members (when you group nodes, move one
  into a group or resize a group).
- **Fit diagram to view** keeps them in view.

## Groups

A group is a box around nodes, with a title. Publishing ignores it. Moving a
group moves the nodes in it.

To group nodes:

1. Select the nodes: click the first, then Shift-click the others.
2. Select **Group** in the toolbar, or press <kbd>⌘</kbd>+<kbd>G</kbd> on
   macOS or <kbd>Ctrl</kbd>+<kbd>G</kbd> on Windows and Linux.

The new group is named "Group", or "Group 2" and so on when that name is
taken. Select the group to change its fields in the Inspector:

- **Title**: shown at the top of the group.
- **Description**: shown under the title on the canvas, on one line.
  Screen readers read it after the group's name.
- **Color** (see [Colors](#colors)).
- **Border pattern**: **Dashed** (the default), **Solid**, **Dotted** or
  **Double**.
- **Icon**: the icon beside the title. The default is the container icon.
  You can also use an image of your own (see [Custom icons](#custom-icons)).
- **Icon size**: the size of the icon beside the title (see
  [Icon size](#icon-size)).

**Color**, **Border pattern**, **Icon** and **Icon size** take effect at
once. **Title** and **Description** wait for **Apply**.

To remove a group and keep its nodes, select the group and select
**Ungroup** (<kbd>⇧</kbd>+<kbd>⌘</kbd>+<kbd>G</kbd> or
<kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>G</kbd>). Its nodes stay where they are.

**Group** under **Nodes & Objects** adds an empty group. When you drag a node onto
a group, the node does not go into the group. Select the node, then select
**Move to group** (the group button) in the Inspector's header, to the left
of **Open Node Editor**. You can also choose **Move to a group…** in the
[command palette](editor.md#command-palette). The command has no default
key, but you can give it one. The **Move to a group** dialog opens on the
selected node, and **Group** shows the group it is in:

1. In **Node**, choose the node, for example `web-01 (device)`. When two
   nodes have the same name, such as two lines without a label, the list
   adds their position, for example `Line (line) at 320, 400`.
2. In **Group**, choose the group, or **No group** to remove the node from
   its group.
3. Select **Move**. The dialog closes and focus returns to where it was
   before the dialog opened.

To resize a selected group, drag the handles on its corners and sides, or
press <kbd>⌥</kbd>+<kbd>⇧</kbd> (<kbd>Alt</kbd>+<kbd>Shift</kbd>) with an
arrow key. A group is never smaller than its members need, with the notes
cards below them while they show. You resize notes the same way.

## Shapes, icons and lines

Rectangles, circles, icons and lines are drawings. They help people read the
diagram, and publishing ignores them, as it ignores notes and groups. They
take no connections. Add one under **Nodes & Objects** (**Rectangle**,
**Circle**, **Icon** or **Line**), or with **Add rectangle**, **Add
circle**, **Add icon** and **Add line** in the command palette. As with any
node, you can move a drawing, put it in a group, copy, duplicate and delete
it. The layouts do not move a drawing. A drawing in a group moves with its
group, and the group grows to hold it when the layout makes the group
smaller.

![The pump station with drawings: a dashed blue rectangle labeled Pump house behind eng-ws-01 and rtu-01, a green circle labeled Reservoir, the built-in external icon, a dashed monitor, labeled Cell tower, a dashed amber line labeled LTE uplink with an arrowhead at each end between station-rtr and the cell tower, and a gray line labeled Fiber to the reservoir that runs down from the reservoir, bends and ends in an arrowhead beside the WAN switch. The STATION and WAN switches show only their names, each outlined in the color of its network.](../images/builder/drawings.png)

Select a drawing to change it in the Inspector:

- A **rectangle** or a **circle** (**Shape**) fills its box. A circle in a
  box that is not square is an ellipse. It has a **Label** at its center, a
  **Fill Color** (see-through without one), an **Outline Color**, a **Border
  pattern** (**Solid**, the default, **Dashed**, **Dotted** or **Double**),
  and a **Width** and **Height**.
- An **icon** shows one of the built-in icons or an image of your own,
  which you choose in **Icon** (see [Custom icons](#custom-icons)), scaled
  to its box. It has an optional **Label** under it, and a **Width** and
  **Height**.
- A **line** joins no node or network. It has a **Label** at its middle, a
  **Color** and **Line style** like a connection's (solid without one), an
  **Arrowhead at the start** and an **Arrowhead at the end**, and its
  **Points**, from its start to its end, each with an **X** and **Y** on the
  canvas: 2 to 64 of them. A new line runs 160 pixels across.

Some of these fields take effect at once, without **Apply**: the colors and
**Border pattern** of a rectangle or a circle, the **Icon** of an icon, and
the **Label**, **Color** and **Line style** of a line. The other fields wait
for **Apply**.

Rectangles, circles and lines lie under the devices and switches, also
while they are selected, so a drawing never hides a device or its
connection points. The handles of a selected drawing are drawn over
everything.

To resize a selected rectangle, circle or icon, drag the handles on its
corners and sides, or press <kbd>⌥</kbd>+<kbd>⇧</kbd>
(<kbd>Alt</kbd>+<kbd>Shift</kbd>) with an arrow key. A selected line shows
a handle on each point: a circle on each end and a square on each bend.
Drag a handle to move its point, and double-click the line to add a bend
there. While **Snap to grid** is on, a dragged point and a new bend go to
the grid (see [The grid](#the-grid)). After you click a handle:

- The arrow keys move its point by a grid step (by a pixel with
  <kbd>⇧</kbd>).
- <kbd>Delete</kbd> or <kbd>Backspace</kbd> removes the point. A line keeps
  at least two points.
- <kbd>Esc</kbd> goes back to the line.

Each of these is one step for **Undo**.

Without a mouse, edit the points in **Points** in the Inspector:

- Change the **X** and **Y** of a point.
- **Add point** adds a new end.
- **Remove point N** removes a point.
- **Insert point after point N** adds a bend halfway to the next point.

Select **Apply** to make the changes, as one step for **Undo**.

## Auto-group

**Auto-group** in the toolbar puts nodes into new groups for you:

- **By network**: "Each network’s switch with its devices". A device on
  several networks goes with the smallest of them.
- **By name**: "Devices with names like web-01 and web-02". Devices whose
  hostnames share a stem go together. Switches stay out.
- **By name pattern…**: "Nodes whose names match a regular expression".
  See [Grouping by a name pattern](#grouping-by-a-name-pattern).

Auto-group groups only the devices and switches that are in no group yet,
and it does not change the groups that exist. When nodes are selected, it
groups only those. A group needs at least two members. After grouping, it
arranges the diagram with the draft's layout (see [Layouts](#layouts)), so
the groups do not overlap.

For example, **By network** on the imported riverside-water makes the groups
CORP, DMZ, INTERNET and OT, as in the Riverside Water draft. **By name** on
Metro Campus makes six groups: app, db, dc, dist, web and ws. When there is
nothing left to group, Auto-group changes nothing.

The command palette has **Auto-group by network**, **Auto-group by name**
and **Auto-group by name pattern…**. Auto-group by network also has a key
(see [Keyboard shortcuts](editor.md#keyboard-shortcuts)).

**Undo** removes the groups again.

### Grouping by a name pattern

**By name pattern…** opens the **Auto-group by name pattern** dialog. Type a
JavaScript regular expression in **Name pattern**, and select **Group**.
Builder matches the pattern against each name, ignoring case: a device's
hostname and a switch's name. Names with the same matched text go in one
group, named after that text. When the pattern has parentheses, Builder
uses the text of the first pair.

For example:

- `^[a-z]+` puts web-01 and web-02 in group web, and db-01 and db-02 in
  group db.
- `^(\w+)-(east|west)` groups by the part of the name before the site.
- `-(\d)\d$` groups by the tens digit of the number at the end.

As with the other rules, it groups only devices and switches that are in no
group, only the selected ones when nodes are selected, and at least two
nodes to a group. One **Undo** removes all the groups it made.

Limits:

- A pattern has at most 200 characters.
- Only the first 255 characters of a name are matched.
- A group's name is cut to 80 characters.
- A pattern that takes more than 2 seconds is stopped, with "This pattern
  takes too long to match. Use a simpler one."

When nothing can be grouped, the dialog says why, for example "Nothing to
group: the pattern matches no ungrouped device or switch." The dialog offers
the pattern you used last. The browser keeps it until you log out. Builder
does not save it in the diagram or send it to the server. The command has no
default key.

When the pattern cannot run, the dialog says "The pattern could not be
checked. Reload the page to try again." (see
[Troubleshooting](administration.md#troubleshooting)).

## Colors

Each kind of node and connection has its own color fields:

| Where | Field | What it colors | Values |
|---|---|---|---|
| Switch | **Edge Color** | The network: its connections, and the outline of its switches | Any CSS color |
| Connection | **Color** | This connection, in place of its network's color | Any CSS color |
| Device, switch | **Outline Color** | The node's border | `#rrggbb` only |
| Device, switch | **Fill Color** | The node's background | `#rrggbb` only |
| Note, group | **Color** | The note or the group | Any CSS color |
| Rectangle, circle | **Outline Color**, **Fill Color** | The shape's border and inside | `#rrggbb` only |
| Line | **Color** | The line and its arrowheads | `#rrggbb` only |

To set a color:

- Select the swatch button before the field to pick a color: one of the
  suggested colors (Blue, Orange, Green, Purple, Red, Teal, Olive and Plum),
  **Custom color**, or **No color**.
- Or type a color in the field, for example `#1f7a5a`, or `teal` where any
  CSS color is allowed.

**Outline Color** and **Fill Color** take a hex color such as `#2f6fbf`
only: no color names, no short form and no transparency. Anything else shows
"Outline Color must be a hex color, such as #2f6fbf" (or "Fill Color …") and
is not applied. The **Outline Color** and **Fill Color** of a switch belong
to that switch: two switches of one network can be different. A switch
without an **Outline Color** has the color of its network's connections as
its outline.

Each color field takes effect at once, without **Apply**. Each change is
one step of **Undo**, for example "Changed the color of Note to #1f7a5a".

On a fill, the text is black or white, for a contrast of at least 4.5 to 1.
An outline near the canvas color gets a thin ring, so the node stays
visible in both themes. A selected node shows the selection border in place
of its outline. In a Windows contrast theme (forced colors), Builder shows
the system colors, not the chosen ones.

The outline of a device or a switch has the first of these colors:

1. The node's own **Outline Color**.
2. The color of its layer, when the diagram colors node outlines with the
   layer color (see [Layer outlines](#layer-outlines)).
3. For a switch, the color of its network's connections.

The built-in icon of a device or a switch has the color of the node's
outline. Without an outline, the icon has the theme's accent color. The
icon of a group always has the accent color. When the outline color has a
contrast of less than 3 to 1 against the node in a theme, the icon has the
node's text color in that theme. On a **Fill Color**, the icon has the
node's text color, unless the node's outline has a hex color with a
contrast of at least 3 to 1 against the fill. A custom icon keeps its own
colors.

A new network gets the suggested color that the fewest networks use. The
suggested colors keep their contrast in the light and the dark theme.

## Line styles

Builder draws a connection with a line pattern. **Line style** sets it, on a
network (in the switch's Inspector, for all its connections) or on one
connection (in place of its network's):

- **Auto**: the pattern that Builder selects from the network's place in
  the diagram, so that networks are different without color. The choice
  names that
  pattern, for example "Auto (Solid)". On a connection, **Auto** follows
  its network.
- **Solid**, **Dashed**, **Dotted** or **Dash-dot**.

A chosen pattern can repeat on several networks. The connection's label, the
switch it joins and its name for screen readers still name its network.
**Line style** takes effect at once, without **Apply**, on a switch and on
a connection. Each change is one step of **Undo**, for example "Changed the
line style of Network EXP to Dotted".

## Built-in icons

Devices, groups and icon drawings show one of the built-in icons. Select
it under **Icon** in the Inspector. The icon is only a picture: it does
not change the phenix settings of a device. A template gives each device
made from it the template's icon.

| Icon | Use it for |
|---|---|
| **Server** | A server, or any virtual machine. A device with no icon shows it. |
| **Desktop** | A desktop computer or a workstation. |
| **Laptop** | A laptop computer. |
| **Smartphone** | A mobile phone or a tablet. |
| **Printer** | A printer. |
| **NTP Server** | A time server. |
| **Router** | A router. |
| **Switch** | A network switch or a hub. |
| **Firewall** | A firewall. |
| **VLAN** | A virtual network or a VLAN. |
| **Container** | A container, or a group of devices. A group with no icon shows it. |
| **External device** | A device outside the experiment, such as hardware in the loop. |
| **PLC** | A programmable logic controller. |
| **RTU** | A remote terminal unit in the field. |
| **RTAC** | An automation controller or a data gateway in a substation or a plant. |
| **Relay** | A protective relay. |
| **Transmission** | A transmission tower or a transmission line. |
| **Distribution** | A distribution pole or a distribution line. |
| **Cell Tower** | A cell tower or a radio link. |
| **Linux** | A computer that runs Linux. |
| **CentOS** | A computer that runs CentOS. |
| **Red Hat** | A computer that runs Red Hat Enterprise Linux. |
| **Ubuntu** | A computer that runs Ubuntu. |
| **Fedora** | A computer that runs Fedora. |
| **openSUSE** | A computer that runs openSUSE. |
| **Alpine** | A computer or a container that runs Alpine Linux. |
| **Kali Linux** | A computer that runs Kali Linux. |
| **BSD** | A computer that runs FreeBSD, OpenBSD or another BSD system. |
| **Windows** | A computer that runs Windows. |
| **Apple** | A computer or a device that runs an Apple operating system. |
| **Android** | A device that runs Android. |
| **Terminal** | A command line, or a computer that a user operates from a terminal. |
| **pfSense** | A pfSense firewall. |
| **Wireshark** | A computer that captures network traffic with Wireshark. |
| **Zeek** | A network sensor, such as a Zeek monitor. |
| **Hacker** | An attacker, or a computer of a red team. |
| **Black Hat** | A malicious attacker or a threat actor. |
| **Human** | A person, such as a user or an operator. |
| **Robot** | An automated agent, such as a bot or a script that acts as a user. |
| **Lizard** | A role that has no other icon, such as a team mascot. |
| **Fox** | A role that has no other icon, such as a team mascot. |
| **phēnix** | A phenix server. |
| **minimega** | A minimega server. |

To preview a
built-in icon, or to download it as an SVG file, use the **Icons** tab of
the drafts page (see
[Built-in icons in the Icons tab](#built-in-icons-in-the-icons-tab)).

Names and logos of products are trademarks of their owners. phenix is not
affiliated with or endorsed by them. The
[icon notices](https://github.com/sandialabs/sceptre-phenix/blob/main/src/js/src/components/builder/ICON_NOTICES.md)
give the source and the license of each icon that comes from another work.

## Icon size

Devices, switches and groups draw their icons at one of three sizes:
**Small** (16 pixels), **Medium** (24 pixels) or **Large** (32 pixels). Set
the size for the whole diagram under **Icon size** in the Inspector's
**Diagram** section (select an empty part of the canvas). It takes effect at
once, as one step of **Undo**. A new diagram draws **Small** icons.

A device, a switch or a group can have a size of its own. **Icon size** in
its Inspector offers **Diagram default**, which names the diagram's size, for
example "Diagram default (Large)", and the three sizes. It takes effect at
once, without **Apply**, as one step of **Undo**.

Nodes keep their size on the canvas. A **Small** icon is before the node's
name. A **Medium** or **Large** icon is left of the node's lines, which
make a column beside it. The icon is centered on a device or a switch, and
at the top left of a group, beside its title and description. Text
that no longer fits, such as a long hostname,
ends with an ellipsis, and the Inspector and the node's tooltip show all of
it. PNG and SVG downloads draw the icons at their sizes. Icon sizes stay in
the Builder document: publishing writes none of them.

![Three devices of the pump station: eng-ws-01 with a Small icon before its name, rtu-01 with a Medium icon and station-rtr with a Large icon, each left of the node's lines, which end with an ellipsis where they no longer fit.](../images/builder/icon-sizes.png)

### Your own icon size and hidden icons

Two settings change how your canvas draws icons. They do not change the
diagram, and other users do not see them. This browser keeps them (see
[Settings](editor.md#settings)):

- **Node icon size**, in the **Canvas** section: **Off** (the default),
  **Small**, **Medium** or **Large**. A size other than **Off** draws the
  icon of every device, switch and group at that size, on your canvas and
  in PNG and SVG downloads. The diagram keeps its own sizes, in its history
  and in its files too. While the setting is not **Off**, each **Icon
  size** field in the Inspector says, for example, "Your Node icon size
  setting (Large) overrides this on your canvas." The command palette has
  **Node icon size: Off**, **Node icon size: Small**, **Node icon size:
  Medium** and **Node icon size: Large**.
- **Show node icons**, in the **Canvas** section, is on by default. When it
  is off, devices, switches and groups show no icon, on the canvas and in
  PNG and SVG downloads, and their text uses all the width. The diagram
  keeps its icons. **Show or hide node icons** in the command palette
  changes the setting.

These commands have no default key, but you can give them one (see
[Customizing shortcuts](editor.md#customizing-shortcuts)).

## Custom icons

A device, a group or an icon can show an image of your own in place of its
built-in icon. The server holds at most 2,000 icons, of all users
together. Use the **Icons** tab of the drafts
page (see [The Icons tab](#the-icons-tab)), the editor or the REST API to
manage the icon library. To choose an icon:

1. Select the device, the group or the icon.
2. In the Inspector, select **Icon**. A list of the built-in icons opens.
3. At the end of the list, select **Custom icon…**. The **Custom icons**
   dialog opens.
4. Select **Upload icon…** and choose an image file, or select **Use** on an
   icon the dialog lists.

**Icon** then shows the image and its name, followed by "(custom icon)".
The change takes effect at once, without **Apply**, as one step of
**Undo**. To use another image, select **Custom icon…** again. To use a
built-in icon again, select it in the list. A switch and a note have no
custom icon.

The phenix server keeps one icon library, which all users of the server
share. A node names its custom icon, and the node shows the server's icon
of that name. When the server has no icon of that name (it was deleted, or
the diagram came from another server), **Icon** says "(custom icon not
found: the node shows its built-in icon)" after the name. The node keeps
its built-in icon until someone uploads an icon of that name.

The dialog lists:

- **In this diagram**: copies of icons the diagram carries, from a file it
  was uploaded from (see [The diagram and its icons](#the-diagram-and-its-icons)).
  Each has **Use**, and **Add to server** when the server does not have it.
  "On the server" says that the server has the same icon.
- **Server icons**: every icon on the server, with who uploaded it, its
  size and its other names, and how many icons the library holds, for
  example "The library holds 2 of 2000 icons." **Filter icons** narrows
  the list by name, other name or uploader. Each icon has **Use**, and
  **Rename** and **Delete** when a user uploaded it.

![The Custom icons dialog: its description, Upload icon…, and Server icons (3) with The library holds 3 of 2000 icons., the Filter icons field, the row with Select all, 0 of 3 selected and Delete selected, and the icons plc, pump and valve, each with a checkbox, uploaded by e2e-admin, with its size and the Use, Rename and Delete buttons; and Close.](../images/builder/custom-icons-dialog.png){ width="624" }

### The Icons tab

The **Icons** tab of the drafts page shows every icon on the server, and
the built-in icons. It is to the right of the **Layers** tab. In
the command palette, select **Show Icons**. To open the preview of one
icon, type `i:` and part of its name. The palette also finds an icon by
another name, by its uploader and by the user who renamed it last (see
[Command palette](editor.md#command-palette)).

The tab holds:

- A description at the top: what an icon is, the sizes a node draws it at,
  and the file types that upload converts to PNG.
- **Upload icon…** (see [Uploading an icon](#uploading-an-icon)).
- How many icons the library holds, for example "The library holds 2 of
  2000 icons."
- **Show**, which shows **Built-in icons** (the default, see
  [Built-in icons in the Icons tab](#built-in-icons-in-the-icons-tab)) or
  **Server icons**. When you upload an icon, or open the preview of an
  icon with `i:` in the command palette, **Show** changes to **Server
  icons**, which lists that icon.
- **Filter icons**, which narrows the cards by name, other name or uploader.
- With **Server icons**, a row for selecting several icons, when you can
  delete some of them.
- With **Server icons**, a card for each icon on the server. The card
  shows the icon and its name, its other names ("Also named" and the
  names), who uploaded it ("Uploaded by alice", or "Server" for an icon that
  the server added from a template file), and its size, for example "48 ×
  24 pixels, 1.2 KiB". It has **Preview**, **Download**, and **Delete** when
  a user uploaded the icon. A card that you can delete also has a checkbox
  at its top right.

![The Icons tab of the drafts page: its description, Upload icon…, The library holds 5 of 2000 icons., Show changed to Server icons, the Filter icons field, the row with Select all, 0 of 5 selected and Delete selected, and the cards gauge and tank uploaded by alice and plc, pump and valve uploaded by e2e-admin. Each card shows the icon, its name, Uploaded by and the user, its size and a checkbox at its top right, and has the Preview, Download and Delete buttons.](../images/builder/icons-tab.png)

**Preview** opens a dialog that shows the icon at its stored size, at most
96 by 96 pixels, with each pixel sharp. Below it, **At node sizes** shows
the icon at 16, 24 and 32 pixels, as a node draws it. The dialog also
lists the name, the other names, the source, and the size in pixels and
bytes. The source is the same text as on the card: "Uploaded by" and the
user, or "Server". **Download** in the dialog saves the icon too.

![The preview of the icon pump: the icon at its stored size, 64 × 64 pixels; under At node sizes, the icon at 16, 24 and 32 pixels; Name pump, Source Uploaded by e2e-admin and Size 64 × 64 pixels, 2,030 bytes; and the Download and Close buttons.](../images/builder/icon-preview.png){ width="528" }

**Download** saves the icon as a PNG file, exactly as the server stores
it. The file has the name of the icon, for example `plc.png`.

**Delete** asks first, as in the **Custom icons** dialog. To delete several
icons, select their checkboxes, then select **Delete selected**. The
question names the icons, for example "Delete 2 icons?". The keys of the
cards are the same as on the other tabs (see
[Selecting several drafts](drafts.md#selecting-several-drafts)). Each
Builder user can delete each icon that a user uploaded. When the tab shows
an icon that the server added, it says "Icons from the server's template
files cannot be deleted."

With **Server icons**, until the first read is complete, the tab says
"Loading…". A server with no icons says "The server has no icons yet."
When Builder cannot read the library, the tab says "Could not load the
server's icons." and why, with **Retry**. It says so with either setting
of **Show**.

#### Built-in icons in the Icons tab

The tab first shows the icons that phenix includes: **Show** is set to
**Built-in icons**. The cards show the icons in the order of the table in
[Built-in icons](#built-in-icons), which tells what each icon is for.
**Filter icons** then narrows these cards by name, key or the word
"Built-in". Each card shows:

- The icon and its name, for example "Router".
- Its key, for example "Key: router". A diagram stores the key.
- "Built-in".

A built-in icon has **Preview** and **Download**. You cannot select, delete
or rename it. **Preview** shows the icon at 96 by 96 pixels, and at 16, 24
and 32 pixels as a node draws it. **Download** saves the icon as an SVG
file with the name of its key, for example `router.svg`. The file draws the
icon in a dark color, so use it on a light background.

The number on the **Icons** tab counts the icons on the server only.

### Uploading an icon

**Upload icon…** takes a PNG, JPEG, GIF, WebP or SVG file of at most 5 MiB.
The browser converts it to a PNG of at most 96 by 96 pixels, in the
picture's own proportions. The browser does not enlarge a small picture,
and draws an SVG as large as an icon can be. The dialog then asks for the
icon's name, proposed from the file's name, and **Add icon** uploads it to
the server.

A name is 1 to 64 letters, digits, `_`, `@`, `.` or `-`. It is unique on
the server, ignoring case, and the first upload of a name keeps it. The
server refuses a name that it already has for another image, and names who
uploaded that icon, for example "icon name "plc" is taken by an icon alice
uploaded. Choose another name". When you upload the same image under its own
name again, the dialog says "The server already has this icon as" and its
name.

An SVG is drawn alone: Builder does not load what it refers to outside its
own file (style sheets, images, fonts), so those parts are missing from the
icon. When the SVG cannot be converted, the dialog says "This image could
not be converted. Save it as a PNG and upload it again." Then save the
picture as a PNG.

On the canvas, an icon is drawn at the node's [icon size](#icon-size) (16,
24 or 32 pixels). It keeps its own colors, the same
in the light and the dark theme. Choose an icon that is clear on both, or
give the node a **Fill Color**.

### The diagram and its icons

A draft names its icons. Except in the cases that this section gives below,
it holds no image of them, so a draft saved on the server stays small. A
download as Builder JSON or YAML holds a copy of each icon that the diagram
names, at most 50, so the file shows its icons on another phenix server. PNG
and SVG downloads draw them. When the server has no icon of a name, the file
does not hold it, and the download says so. The **Download** that logging
out offers, for changes that the server does not have yet, also holds the
icons.

Some actions add icons from a file to the server's library:

- Uploading a file that holds icons.
- **Edit as a draft** on the diagram of a topology read from its Builder
  file.

Builder adds each icon that the server does not have, as uploaded by you,
and the draft names it. When the server already has the same image under
that name, Builder discards the copy. The draft keeps its own copy of an
icon, and Builder says so, in these cases:

- The server has another image under that name.
- The server could not take the icon (you reached your upload limit, for
  example).

The draft shows its copy in place of the server's icon. **Add to server** in
the dialog adds a kept copy later. When no node or template of the diagram
uses an icon any more, the icon leaves the diagram with the edit that
removed its last use. **Undo** brings it back.

Copy and paste include the copies of the nodes' icons that the diagram
holds, also into another diagram in the same browser tab.

### Renaming and deleting icons

Each Builder user can rename and delete each icon that a user uploaded,
whoever uploaded it (see [The libraries](administration.md#the-libraries)).
An icon that the server added from a template file shows as from "Server".
No user can rename or delete it (see
[Template files on the server](administration.md#template-files-on-the-server)).

**Rename** asks for the new name. The old name continues to name the icon,
so diagrams and templates that use it continue to show it: "Renamed OLD to
NEW. OLD keeps working as another name of it." The icon lists its other
names. The command palette shows the user who renamed the icon last as
"Updated by", also when that user is not the uploader.

**Delete** asks "Delete icon?" and says "Delete NAME from the server?
Diagrams and templates that use it, by any of its names, will show their
built-in icon instead." Deleting an icon removes its other names too, and
changes no diagram: its nodes keep the name.

### Several icons at once

In the Custom icons dialog, these icons have a checkbox: a copy in the
diagram that the server does not have, and a server icon that you can
delete. Above each list, a row has **Select all**, how many are selected
("2 of 5 selected"), and its action. The dialog's status line, under
**Upload icon…**, repeats the count of the list whose selection changed
last, for example "2 of 3 selected.", so that a screen reader says it.
The actions are:

- **Add selected to server**, above **In this diagram**, adds each selected
  copy to the server under its name: "Added 2 icons to the server. The
  diagram drops its copies with its next edit." When someone added a copy to
  the server after the dialog opened, the count does not include it, and the
  dialog names it: "The server already has plc."
- **Delete selected**, above **Server icons**, asks once, for example
  "Delete 2 icons?" and "Delete plc and pump from the server? Diagrams and
  templates that use them, by any of their names, will show their built-in
  icon instead." Select **Delete 2 icons**. For one icon, the question is
  the same as for its own **Delete**.

In this example, rtu-01 and eng-ws-01 of the pump station use the icons
gauge and tank. The diagram holds them, and the server does not. The two
copies and the server icons plc and pump are selected:

![The Custom icons dialog with icons selected: the status line 2 of 3 selected. under Upload icon…, for the last change of a selection; under In this diagram (2), the row with Select all ticked, 2 of 2 selected and Add selected to server, and the copies gauge and tank, both ticked, each with Use and Add to server; under Server icons (3), the row with Select all in a mixed state, 2 of 3 selected and Delete selected, and plc and pump ticked and valve not, each with Use, Rename and Delete; and Close.](../images/builder/icons-selected.png){ width="624" }

The rows of a list use the keys of the drafts page (see
[Selecting with the keyboard](drafts.md#selecting-with-the-keyboard)), with
these differences:

- **Escape** with nothing selected closes the dialog.
- **Delete** works only on the server icons.
- **Filter icons** limits what **Select all** of the server icons selects.
  Icons that the filter hides are not selected.

When some icons could not be added or deleted, a summary under the row lists
each with the reason. When your session ends, or the server cannot be
reached, Builder stops and lists the rest as "Not attempted."

## Layers

A diagram has a list of **layers**. Each layer has an ID, an integer from 0
to 99, and a label, for example "Operations". A layer can also have an
icon, a color and node matchers. The editor names a layer by its label,
for example "Operations". When two layers have the same label, the editor
adds the ID, for example "Control (ID 3)". A device or a switch can be at
one layer: a layer that you choose for it, or for a device, a layer that
its fields match (see [Node matchers](#node-matchers)). The
[By layer](#by-layer) layout puts the layers one above the other, the
highest ID at the top.

Layers are for the picture only. The diagram keeps them when you save,
download or upload it. Publishing writes none of them into the Topology,
and an import from a topology sets none.

A diagram keeps its own copy of its layers. The copy can come from a layer
set of the server's library, and stay linked to that set (see
[Layer sets](#layer-sets)).

While a device or a switch is at a layer, the editor header counts the
layers of the diagram, the placeholder layers included (see
[Header](editor.md#header)). Select the count to select every device and
switch that is at a layer.

Limits:

- A diagram has at most 32 layers. Each ID is used one time.
- A label has 1 to 64 bytes, and no control characters, such as a tab.
- An icon is a built-in icon or a custom icon of the server (see
  [Custom icons](#custom-icons)).
- A color is `#` and 6 hex digits, such as `#2f6fbf`.
- A layer has at most 16 node matchers. A field has at most 128 bytes, and
  a pattern at most 256 bytes.

### Placeholder layers

A diagram that has no layers of its own has 7 **placeholder layers**:
IDs 0 to 6, with the labels "Layer 0" to "Layer 6". You can use them as
they are. The first change to them in the layers editor (see
[Editing the layers](#editing-the-layers)) saves the 7 layers in the
diagram, with that change. A diagram whose layers are again the 7
placeholder layers saves no list.

A new blank diagram starts with a copy of the default layer set, when the
server has one (see [Layers for new diagrams](#layers-for-new-diagrams)).

### Setting the layer of a device or a switch

1. Select the device or the switch.
2. In the Inspector, under **Layer**, select a layer.

The list shows **None**, then the layers of the diagram, the highest ID
first, for example "Operations". The icon and the color of the node's
layer show before the list. The change takes effect at once, as one step
of **Undo**, for example "Changed the layer of Device scada-01 to Level 3
Operations". Select **None** to remove the layer.

A device or a switch can be at an ID that is not in the list, for example
after you remove its layer. The list then also shows that ID, with the
label "Layer N", for example "Layer 5".

To set the layer of several devices and switches, select them. Then choose
**Set layer of selection** in the
[command palette](editor.md#command-palette), and choose a layer, or **No
layer** to remove it. Each layer shows its icon, its color and how many
nodes are at it. All the selected devices and switches change as one step
of **Undo**, for example "Changed the layer of 3 nodes to Level 1 Basic
control". Devices from an included topology do not change. The command
also works with the placeholder layers.

To select every node at a layer, choose **Select nodes on layer** in the
command palette, and choose the layer. It selects the nodes that you put at
the layer and the devices that its node matchers put there, and says how
many, for example "Selected 3 nodes on layer Level 1 Basic control". The
command is available while a device or a switch is at a layer. The two
commands have no default key, but you can give them one (see
[Customizing shortcuts](editor.md#customizing-shortcuts)).

The [Node Editor](editor.md#node-editor) shows **Layer** as a field after
the colors. A [node template](templates.md) can also have a layer, which a
new device gets.

### Node matchers

A node matcher puts a device at its layer by the device's fields. It has a
field and a pattern:

- The field is a path into the device's phenix spec, the fields of the Node
  Editor: keys of letters, digits, `_` or `-`, joined by dots. For example
  `general.hostname`, `hardware.os_type` or `network.interfaces.0.vlan`. A
  key of digits is the index of an entry of a list, from 0. A list at the
  end of the path matches when one of its entries matches. For example,
  `general.notes` matches when one note matches.
- The pattern is a regular expression. It matches when it finds a match
  anywhere in the value of the field, and letter case counts. Use `^` and
  `$` to match the start and the end, for example `^plc-`.

A number or a boolean matches as its text, for example `10` or `true`. An
object, a null value and a field that the device does not have never
match. `general.hostname` also matches the device's hostname when the spec
has no hostname.

Only devices match. A switch has no spec, so it is at a layer only when you
choose one for it.

The editor accepts only a pattern that the server and the editor read the
same way. It refuses these patterns, with "This is not a regular
expression that the server and the editor read the same way. Do not use
lookarounds, backreferences, flags or \p classes.":

- Lookarounds, such as `(?=x)` and `(?<!x)`.
- Backreferences, such as `\1` and `\k<name>`.
- Flags, such as `(?i)`, and `(?P<name>…)`.
- The escapes `\c`, `\u`, `\p`, `\A`, `\z`, `\Q…\E` and `\x{41}`.
- `[[:alpha:]]` and the empty class `[]`.
- A class escape at the end of a range, such as `[a-\d]`.
- Repeats in repeats that make more than 1000 copies, such as
  `(a{500}){500}`.

When the matchers of more than one layer match a device, the diagram's
rule chooses: **Lowest layer ID wins**, the default, or **Highest layer ID
wins** (see [Editing the layers](#editing-the-layers)).

A layer that you choose for a device goes first. A device whose fields
match a layer, and that has no layer that you chose, is at the matched
layer. Its first choice under **Layer** in the Inspector then names the
matched layer and the field, for example "Level 1 Basic control
(matched general.hostname)", in place of **None**. When you choose another
layer, the Inspector says "Its fields match Level 1 Basic control
(general.hostname). The layer that you chose goes first." **Use matched
layer** removes your choice, so the device returns to the matched layer,
as one step of **Undo**.

The matched layer counts as the device's layer everywhere: in the **By
layer** layout, in the layer counts, in **Select nodes on layer** and in
the layer outlines.

For example, give layer 1 the node matcher `general.hostname` with the
pattern `^plc-`. Each device whose hostname starts with `plc-`, such as
plc-intake, is then at layer 1 when no one chose a layer for it.

### Layer outlines

The option **Color node outlines with the layer color** is off by default.
When it is on, a device or a switch at a layer that has a color gets an
outline in that color. An **Outline Color** of the node itself goes first.
For a switch, the layer color goes before the color of its network (see
[Colors](#colors)).

In the layers editor, the Inspector and the command palette, a layer's
label has the layer's color when the label keeps a contrast of 4.5 to 1 in
the theme shown. Otherwise, a swatch of the color shows beside the label.

### Editing the layers

The layers editor, **Layers of this diagram**, edits the diagram's layers.
To open it, use one of these:

- **Edit layers…** under **Layer** in the Inspector of a device or a
  switch, or in the Node Editor.
- **Edit layers…** under **Layers** in the Inspector, with nothing
  selected. This part also lists the layers, each with its icon and color,
  and says how they stand to their layer set.
- The **Edit layers…** command in the
  [command palette](editor.md#command-palette).
- **Edit layers…** at the bottom of [Settings](editor.md#settings).

The editor has three parts:

- **Layer set**: how the layers stand to the layer set they came from, and
  the buttons that apply, update, save and unlink a set (see
  [Layer sets](#layer-sets)).
- **Options**:
    - **When a device matches more than one layer**: **Lowest layer ID
      wins** or **Highest layer ID wins** (see
      [Node matchers](#node-matchers)).
    - **Color node outlines with the layer color** (see
      [Layer outlines](#layer-outlines)).
- **Layers**: a row for each layer, the highest ID first.

![The Layers of this diagram dialog of Lakeside Water Treatment. Under Layer set: Linked to layer set Lakeside plant zones, version 1., Save as new set… and Unlink, then Use a layer set set to Lakeside plant zones with Apply and its hint. Under Options: When a device matches more than one layer set to Lowest layer ID wins, and Color node outlines with the layer color On. Under Layers: the rows of L5 Enterprise (ID 6), L4 Business (ID 5), L3.5 Industrial DMZ (ID 4) and L3 Operations (ID 3), each with its ID, Icon menu, Label, color, the number of nodes at the layer, and a general.hostname node matcher such as ^mail- or ^(corp|erp)-, with Add node matcher. The other rows are further down the list. At the bottom is Done.](../images/builder/layers-dialog.png){ width="752" }

Each row has these fields:

- **ID**: type a new ID, then press <kbd>Tab</kbd> or <kbd>Enter</kbd>. The
  devices, switches and templates at the layer move to the new ID. The row
  moves to its new place in the list.
- **Icon**: a menu with **No icon**, the built-in icons and **Custom
  icon…**, which opens the **Custom icons** dialog.
- **Label**: type a new label, then press <kbd>Tab</kbd> or
  <kbd>Enter</kbd>.
- **Remove** (the trash can), named for its layer, for example "Remove
  layer 3 – Level 3 Operations". The devices and switches at the layer keep
  its ID, which then shows as "Layer N". A line under the list names the
  IDs that are not in the list.
- **Color**: type a color such as `#2f6fbf`, or choose one with the color
  picker before the field. **Remove color** removes it.
- How many devices and switches are at the layer, for example "2 nodes are
  at this layer."
- **Node matchers**: a row for each matcher, with its field, its pattern
  and a remove button. **Add node matcher** adds an empty row. The editor
  writes the matcher into the layer when both of its fields are set. A
  layer has one matcher for each field.

**Add layer** adds a layer with the next free ID, one above the highest,
and the label "Layer N". Focus moves to its label, so you can type a name.

Each change is one step of **Undo** of the diagram. A field changes the
diagram when you leave it, so the text you type in one field is one step.
**Done** closes the editor. A value that the diagram cannot keep shows an
error under its row, and the diagram does not change until you correct it:

| Error | Cause |
|---|---|
| "Enter a layer ID from 0 to 99." | The ID is empty, not an integer, or out of range. |
| "Layer ID 3 is in use. Enter a different ID." | Another layer has the ID. |
| "Type a label for the layer." | The label is empty. |
| "A layer label holds at most 64 bytes in UTF-8." | The label is too long. |
| "A layer label cannot hold control characters, such as a tab." | The label holds a tab or another control character. |
| "A layer color is # and 6 hex digits, such as #2f6fbf." | The color is not `#rrggbb`. |
| "Type a field, such as general.hostname." | The field of a matcher is empty. |
| "A field is keys of letters, digits, _ or -, joined by dots, such as general.hostname." | The field of a matcher has another character. |
| "Type a regular expression, such as ^plc-." | The pattern of a matcher is empty. |
| "Layer 1 has a node matcher for general.hostname already. Enter a different field." | Another matcher of the layer has the field. |

In a published diagram, the fields are read only.

### Layer sets

A layer set is a list of layers, with the two options, that the server
keeps in its library. Every user of the server can use the sets. phenix
has one built-in set, **Purdue Model**, which no one can change. The
**Layers** tab of the [drafts page](drafts.md#the-drafts-page) lists the
sets, and adds, changes and deletes them.

A diagram holds a copy of a set's layers and options, and a link to the set
and its version. A change to the diagram's layers changes only the copy.
The **Layer set** part of the layers editor says how the copy stands:

| Text | What it means |
|---|---|
| "These layers are not linked to a layer set." | The diagram has no link. |
| "Linked to layer set NAME, version N." | The copy is that version of the set. |
| "Linked to layer set NAME, version N. You changed the layers in this diagram after the copy." | You changed the copy. |
| "Layer set NAME has a newer version, M. This diagram has a copy of version N." | Someone saved the set after the copy. |
| "Linked to layer set NAME, which the server does not have now." | Someone deleted the set, or the diagram came from another server. |

The **Layers** part of the Inspector, with nothing selected, says the same
in short, for example "From layer set Purdue Model, version 1." When the
browser window becomes active again, the editor reads the library again, so a
newer version shows.

The layers editor has these buttons. Each change of the diagram is one
step of **Undo**:

- **Use a layer set** and **Apply**: replaces the diagram's layers and
  options with a copy of the set that you choose, and links the diagram to
  it. Devices and switches keep the layers that you chose for them. The
  list shows first the set that the diagram is linked to, else the default
  set.
- **Update from layer set**: shows when the set has a newer version. It
  copies the newer version, and replaces the changes that you made in the
  diagram. The Inspector, with nothing selected, also has this button.
- **Save to layer set**: shows when you changed the copy, and the set has
  no newer version. It saves the diagram's layers and options to the set,
  which gets the next version, and links the diagram to that version. The
  set keeps its name, description and icon. A built-in set has no **Save to
  layer set**. When the set has a newer version, the server refuses the
  save with "The layer set has a newer version. Update from the layer set
  first."
- **Save as new set…**: asks for **Name of the new set** and
  **Description**. **Save layer set** adds the set to the library and links
  the diagram to it.
- **Unlink**: removes the link. The layers and the options stay.

Custom icons that the layers use and that the server does not have go into
the icon library when you save a set.

### Layers for new diagrams

On the **Layers** tab of the drafts page, **Set as default** makes a set
the default set of the server. The server has one default set for all
users, and each Builder user can change it. Each new blank diagram starts
with a copy of the default set, linked to it. **Clear default** removes the
choice, and new diagrams then have the placeholder layers.

A diagram that you import or upload does not use the default set. It has
the layers of the file it comes from, or the placeholder layers.

### Example: map layers to the Purdue model

The Purdue model divides an industrial network into levels, from the
enterprise at the top to the physical process at the bottom. The built-in
layer set **Purdue Model** gives each level a layer:

| Layer ID | Label | Icon | Color | Typical devices |
|---|---|---|---|---|
| 6 | L5 Enterprise | Server | `#2f6fbf` | Enterprise IT systems, email and the connection to the internet |
| 5 | L4 Business | Laptop | `#8b5cf6` | Business systems of the site, such as ERP |
| 4 | L3.5 Industrial DMZ | Firewall | `#b45309` | Jump hosts, patch servers and historian copies between business and operations |
| 3 | L3 Operations | Terminal | `#0f766e` | SCADA servers, historians and site-wide operations systems |
| 2 | L2 Supervisory control | Desktop | `#15803d` | HMIs and engineering workstations |
| 1 | L1 Basic control | PLC | `#db2777` | PLCs, RTUs and IEDs |
| 0 | L0 Process | Transmission | `#dc2626` | Pumps, valves and sensors |

A layer ID is an integer, so Level 3.5 cannot be ID 3.5. The set gives each
level the next integer, from 0 at the bottom, and Level 3.5 gets ID 4. The
label keeps the Purdue name, so the Inspector shows "L3.5 Industrial
DMZ". The IDs keep the order of the levels, so the **By layer** layout
stacks them as the Purdue model does. The set has no node matchers.

To use these layers in a diagram:

1. Open the layers editor (see [Editing the layers](#editing-the-layers)).
2. In **Use a layer set**, choose **Purdue Model (built-in)**.
3. Select **Apply**.

To start each new blank diagram with these layers, select **Set as
default** on the card of **Purdue Model** in the **Layers** tab.

For example, Lakeside Water Treatment is a small water treatment plant
with 17 devices on 5 networks. Its layers have the IDs of the Purdue Model
set, with longer labels, such as "Level 5 Enterprise", and no icons or
colors. Each device and each switch is at the layer of its Purdue level:

| Node | Icon | Networks | Layer ID | Layer |
|---|---|---|---|---|
| mail-01 | Server | CORP | 6 | Level 5 Enterprise |
| corp-ws-01 | Windows | CORP | 5 | Level 4 Business |
| erp-01 | Server | CORP | 5 | Level 4 Business |
| CORP (switch) | — | CORP | 5 | Level 4 Business |
| it-fw | Firewall | CORP, DMZ | 4 | Level 3.5 Industrial DMZ |
| hist-replica | Linux | DMZ | 4 | Level 3.5 Industrial DMZ |
| jump-01 | Windows | DMZ | 4 | Level 3.5 Industrial DMZ |
| ot-fw | Firewall | DMZ, OPS, CONTROL | 4 | Level 3.5 Industrial DMZ |
| DMZ (switch) | — | DMZ | 4 | Level 3.5 Industrial DMZ |
| scada-01 | Server | OPS | 3 | Level 3 Operations |
| historian-01 | Linux | OPS | 3 | Level 3 Operations |
| OPS (switch) | — | OPS | 3 | Level 3 Operations |
| hmi-01 | Windows | CONTROL | 2 | Level 2 Supervisory control |
| ews-01 | Windows | CONTROL | 2 | Level 2 Supervisory control |
| CONTROL (switch) | — | CONTROL | 2 | Level 2 Supervisory control |
| plc-intake | PLC | CONTROL, FIELD | 1 | Level 1 Basic control |
| plc-dosing | PLC | CONTROL, FIELD | 1 | Level 1 Basic control |
| rtu-reservoir | RTU | CONTROL | 1 | Level 1 Basic control |
| intake-pump | External device | FIELD | 0 | Level 0 Process |
| dosing-valve | External device | FIELD | 0 | Level 0 Process |
| turbidity-sensor | External device | FIELD | 0 | Level 0 Process |
| FIELD (switch) | — | FIELD | 0 | Level 0 Process |

The pumps, the valve and the sensor are external devices: hardware in the
loop, which the PLCs control through the FIELD network.

The saved diagram holds the layers at its top, the highest ID first:

```json
"layers": [
  { "id": 6, "label": "Level 5 Enterprise" },
  { "id": 5, "label": "Level 4 Business" },
  { "id": 4, "label": "Level 3.5 Industrial DMZ" },
  { "id": 3, "label": "Level 3 Operations" },
  { "id": 2, "label": "Level 2 Supervisory control" },
  { "id": 1, "label": "Level 1 Basic control" },
  { "id": 0, "label": "Level 0 Process" }
]
```

Each device and each switch holds the ID of its layer in `layer`, for
example `"layer": 3` in the `device` of scada-01 and in the `switch` of
OPS.

To try it, download
[lakeside-treatment.builder.json](examples/lakeside-treatment.builder.json)
and upload it (see
[Uploading a Builder document](import-upload-download.md#uploading-a-builder-document)).
Then choose **By layer** in the layout menu to stack the layers.

![Lakeside Water Treatment after By layer, from top to bottom: mail-01; the CORP switch; corp-ws-01 and erp-01; it-fw and ot-fw; the DMZ switch; hist-replica and jump-01; the OPS switch; scada-01 and historian-01; the CONTROL switch; hmi-01 and ews-01; plc-intake, plc-dosing and rtu-reservoir; the FIELD switch; and intake-pump, dosing-valve and turbidity-sensor. The connections go down from each layer to the next.](../images/builder/lakeside-by-layer.png){ width="476" }

## Layouts

The layout menu in the toolbar arranges the whole diagram. It shows the
layout that last arranged the draft, or **Default** when no layout ran. An
imported, uploaded or blank draft starts at **Default**. An import puts its
devices in rows on a grid, with the switches below them.

![The layout menu listing ELK layered, By layer, Network cards, Dagre, Standard, Yifan Hu, Force and Radial, each with a one-line summary, and a check mark at ELK layered, the current layout.](../images/builder/layout-menu.png){ width="288" }

| Layout | Menu summary | What it does |
|---|---|---|
| **ELK layered** | Clusters by network, left to right | Clusters each network's switch with its devices, a device on several networks in its smallest one, and runs connections left to right between the clusters. |
| **By layer** | Diagram layers, top to bottom | Puts each node at its [layer](#layers), the highest layer ID at the top, and runs connections down from the higher layer to the lower one. |
| **Network cards** | A card per network, on a grid | A card per network, its devices in a column grouped by name with the switch at their head, and the cards on a grid. |
| **Dagre** | Networks in layers, left to right | Each network's devices in a column beside their switch, and the networks in layers along the connections between them. |
| **Standard** | Devices above switches, top to bottom | Every device in a row above the switches, from top to bottom. |
| **Yifan Hu** | Spring model, fast on large diagrams | Graphviz sfdp, the spring model of Yifan Hu. A connection keeps its nodes near each other, and each node keeps a distance from the others. Parts of the diagram with many connections become clusters. |
| **Force** | Spring model, for small diagrams | A spring model like Yifan Hu. |
| **Radial** | Rings around one node | Graphviz twopi. One node is at the center, and the other nodes are in rings by how many connections away they are. |

**ELK layered** suits most diagrams. The **Default layout** setting
describes each layout the same way.

**Yifan Hu**, **Force** and **Radial** show the shape of the network. They
do not put connections in a direction, such as left to right:

- Use **Yifan Hu** for a large diagram with many networks. It shows which
  networks are near each other.
- Use **Force** for a small diagram with few networks.
- Use **Radial** to show the diagram from the point of view of one node.
  Select that node, then choose **Radial**. With no node selected, or more
  than one, the center is a router or firewall, by its icon or its node
  type. Without one, the center is the switch with the most devices.

The same diagram and the same selection always get the same layout.
**Yifan Hu** and **Radial** load when you first use one of them. If they
cannot load, the page shows "Auto layout failed. The layout engine could not
be loaded. Reload the page to try again."

Layouts keep groups together. A group's members stay inside it, and no other
node goes in.

For example, Metro Campus has 42 devices on 7 networks. Imported, it uses
the **Default** grid:

![Metro Campus as imported, with the Default layout: 42 devices in rows on a grid and 7 switches below them, with long connections crossing the diagram.](../images/builder/metro-default.png)

Open the layout menu and choose **ELK layered**:

![Metro Campus after ELK layered: each network's switch clustered with its devices, and the clusters in layers from left to right along the connections between them.](../images/builder/metro-elk.png)

The draft keeps the layout, and the menu then shows it. Right after a layout
runs, the menu also offers **Restore previous layout** ("Move every node to
where it was"), until you change the diagram in a different way. **Undo**
also moves the nodes to where they were.

**Auto layout** in the command palette, and its key (see
[Keyboard shortcuts](editor.md#keyboard-shortcuts)), runs the draft's layout
again. On a draft at **Default**, **Auto layout** and **Auto-group** use the
**Default layout** of [Settings](editor.md#settings), **ELK layered** by
default.

Publishing, and a download as Topology YAML, ignore positions and layouts.

### By layer

**By layer** arranges the diagram from top to bottom by
[layer](#layers). The highest layer ID is at the top and the lowest is at
the bottom. Each connection goes down from the higher layer to the lower
one. Only the IDs count: a layer need not be in the diagram's list.

- A device or a switch without a layer goes to the layer of the nearest
  connected node that has one. When two such nodes are equally near, it
  goes to the higher layer.
- A connected node that no layer reaches goes below every layer. A node
  without connections, such as a note, goes at the bottom.
- A group is at the layer of its highest member. Inside the group, its
  members are in the order of their layers.

In a diagram whose devices and switches have no layer, and inside one
layer, the layout starts from a top node. Each other node goes below the
node that leads to it from the top node. The top node is the first of
these that the diagram has:

1. The selected device or switch.
2. A firewall.
3. A router.
4. The switch with the most connections.

The layout uses the icon or the node type to find a firewall or a router.
An [external device](#external-devices) is a top node only when you select
it, because hardware in the loop, such as a PLC, is usually low in a
plant.

## Arranging by hand

- Drag a node, or several selected nodes. The nodes move as far as the
  pointer moves, from the moment you press. While **Snap to grid** is on, a
  drag puts them on the diagram's grid (see [The grid](#the-grid)). A press
  is a click while the pointer stays within 4 screen pixels of the point
  you pressed (8 pixels with a finger or a pen). This is true at any zoom.
  A click does not move the nodes, with **Snap to grid** on or off. When
  Builder cannot take a move, for example while a save conflict is open,
  the nodes return to their old places.
- Press <kbd>⇧</kbd> (<kbd>Shift</kbd>) with an arrow key to move the
  selected nodes 10 pixels. They do not snap to the grid, and they stay where
  you put them when the diagram opens again.
- Type a position: under **Position** in the Inspector, enter **X** and
  **Y**, then select **Move**. For example, move the note of Riverside Water
  to **X** `1248` and **Y** `880`.

A move by hand does not change the layout that the layout menu shows.

### The grid

The canvas shows the diagram's grid. **Grid size**, in the Inspector with
nothing selected, sets the distance between the lines of the grid, in
pixels. A new diagram has a grid size of 16. Type a whole number from 4 to
200, then press <kbd>Enter</kbd> or leave the field. The change is one step
of **Undo**, for example "Changed the grid size to 20". Another value shows
"Grid size must be a whole number from 4 to 200.", and the diagram keeps its
size. A published diagram shows the size as text, for example "16 pixels".

The **Snap to grid** setting, in the **Editing** section of
[Settings](editor.md#settings), is off by default. While it is on, these go
to the nearest point of the grid:

- Nodes that you drag, and nodes that you drop from **Nodes & Objects**.
- New nodes that you add.
- The points of a line that you drag or add (see
  [Shapes, icons and lines](#shapes-icons-and-lines)).

While it is off, they go where you put them, on a whole pixel. The setting
is yours, not the diagram's: this browser keeps it, and other users have
their own. **Snap to grid** in the
[command palette](editor.md#command-palette) turns it on or off. It has no
default key, but you can give it one (see
[Customizing shortcuts](editor.md#customizing-shortcuts)).

## Copy, paste, duplicate and delete

**Copy**, **Paste** and **Delete** are in the toolbar, and **Duplicate** is
in the command palette. Each also has a key (see
[Keyboard shortcuts](editor.md#keyboard-shortcuts)).

**Copy** and **Paste** use the Builder clipboard, not the system clipboard.
They work between drafts that you open one after the other in the same
browser tab. **Duplicate** copies and pastes the selected nodes in one step,
and does not change the Builder clipboard. Builder duplicates a connection
only with both of its nodes. With only connections selected,
**Duplicate** does nothing and says "Select the nodes to duplicate. A
connection is copied with both of its nodes."

- A paste goes 40 pixels down and to the right of the original, and each
  further paste 40 pixels further. Builder selects the pasted nodes.
- A pasted device gets a free hostname, such as server-2, and keeps its
  settings. Builder pastes a connection only when you copy both of its ends.
  Copy a device with its switch to keep the connection. A pasted device
  without its connection has interfaces with no VLAN.
- Copying a group copies the nodes in it.

**Delete** deletes the selected nodes and connections. When you delete a
device, Builder also deletes its connections. You cannot delete devices from
included topologies or their connections. Builder says, for example, "Included
topology corp-services defines dns-01, so it is read only here. Change it in
corp-services."

## Undo and redo

**Undo** and **Redo** in the toolbar, and their keys (see
[Keyboard shortcuts](editor.md#keyboard-shortcuts)), go back and forward
through your changes. Each change is a snapshot of the draft on the server,
so **Undo** moves the draft itself back. **Undo** and **Redo** reach only
the changes since you opened the draft in this tab, or since your last
restore. To go back further, use **Draft History** (see
[Draft History](drafts.md#draft-history)).

## Scenarios

A diagram lists the Scenario configs it is used with, up to 20, by name. A
scenario says which apps run on which devices. The diagram holds only their
names. The scenarios are configs on the server. Publishing adds the
topology to each one's `topology` annotation, and an experiment published
with the topology uses one of them (see
[Scenarios](publishing.md#scenarios)).

The Riverside Water draft lists the stored scenario riverside-water. To add
it to another diagram, such as your
[quick start](index.md#quick-start) draft:

1. Select **Scenarios** in the toolbar. With nothing selected, **Add
   scenario** (or **Edit scenarios**) in the Inspector does the same.
2. Under **Scenarios of this diagram**, select the checkbox of
   `riverside-water`.
3. Select **Save scenarios**.

The Scenarios dialog of a diagram that uses two scenarios:

![The Scenarios dialog: the hint that publishing adds this diagram's topology to each selected scenario; under Scenarios of this diagram, the hint Select each scenario that this diagram uses., and pump-station-ntp, riverside-ntp, riverside-red-team and riverside-water, each with a checkbox and the View and Edit buttons, with riverside-ntp and riverside-water selected; 2 scenarios selected.; and Create new scenario. Below them are Upload a scenario file with its file field and hint, and the Cancel and Save scenarios buttons.](../images/builder/scenario-dialog.png)

With nothing selected, the Inspector now shows the sub-heading "Scenario
riverside-water (2 apps, 6 hosts)", with each app and its hosts under it:
vrouter on edge-rtr, and ntp on ntp-01, ws-01, ws-02, hmi-01 and
historian-01 (see [With nothing selected](editor.md#with-nothing-selected)).

### The list of scenarios

**Scenarios of this diagram** lists each scenario that your role can list
on the server, sorted by name. Each scenario has a checkbox, which
is selected when the diagram uses the scenario. Select a checkbox to add
the scenario to the end of the diagram's list. Clear it to remove the
scenario from the list. The scenario on the server does not change.

- A scenario that the diagram lists, and that the server does not list,
  shows "Not found on the server".
- With more than 8 scenarios, **Filter scenarios** shows the scenarios
  whose names contain the text that you type, for example "Showing 2 of
  12 scenarios." <kbd>Enter</kbd> in this field does not save the dialog.
- Under the list, the dialog counts the selected scenarios, for example "1
  scenario selected."
- When the diagram lists 20 scenarios, the dialog says "A diagram lists at
  most 20 scenarios. To select another, clear one first." The checkboxes of
  the other scenarios are then not available.

**Save scenarios** writes the list into the diagram, as one step of
**Undo**. **Cancel** or <kbd>Esc</kbd> does not change the list, but a
scenario that you stored stays on the server.

### Viewing and editing a scenario

Each scenario of the server has **View** and **Edit**. While Builder reads
the scenario from the server, a spinner shows in place of the button.

- **View** opens the scenario as YAML, read only, in the dialog **Scenario
  NAME**. **Copy YAML** copies it to the clipboard. **Close** closes the
  dialog.
- **Edit** opens the scenario as YAML in a text field, in the dialog **Edit
  scenario NAME**. Change the config, then select **Save scenario**. Builder
  replaces the stored scenario with the text, its annotations included. The
  name must stay the same: "The name must stay NAME. To store the scenario
  with a new name, select Create new scenario."

![The Scenario riverside-water dialog: This is the Scenario config that the server stores., the YAML of the scenario, which starts with apiVersion phenix.sandia.gov/v2, kind Scenario and the metadata, and the Copy YAML and Close buttons.](../images/builder/scenario-yaml-view.png){ width="912" }

![The Edit scenario riverside-ntp dialog: the Scenario config (YAML) field with the hint Change the config as necessary. Select Save scenario to replace scenario riverside-ntp on the server., the YAML of the scenario with its ntp app, and the Cancel and Save scenario buttons.](../images/builder/scenario-yaml-edit.png){ width="912" }

When the server refuses the config, the editor shows why, with each
problem that the server found. The editor stays open, so you can correct
the text. When you changed the text, **Cancel** and <kbd>Esc</kbd> ask
"Discard your changes?". **Discard changes** closes the editor, and **Keep
editing** returns to the text.

### Creating a scenario

**Create new scenario** opens the editor with a new Scenario config:

- Its name is the diagram's name made into a config name, then
  `-scenario`, for example `Riverside-Water-scenario`. When the name is
  taken, Builder adds a number, such as `-2`.
- Its `topology` annotation names the topology that the
  [Publish dialog](publishing.md#scenarios) offers first.
- It has no apps.

Change the config, then select **Create scenario**. Builder stores the
scenario on the server and selects its checkbox: "Created scenario NAME on
the server and added it to the list. Save scenarios to keep the list in
this diagram."

### Uploading a scenario file

To add a scenario that you have as a file, choose it in **Upload a scenario
file**: a `phenix.sandia.gov/v2` Scenario config in JSON or YAML, up to
5 MiB. As **Scenario name**, the dialog proposes the file's `metadata.name`,
or the file name made into a config name. You can change it. **Store and
add**, or <kbd>Enter</kbd> in **Scenario name**, stores the file on the
server as a Scenario config and selects its checkbox.

When the server already has a scenario of that name, the hint under the name
says that storing replaces its spec and keeps its annotations. Builder asks
"Replace scenario NAME?" first. The replaced scenario gets the file's spec.
It keeps its own annotations and adds those of the file. Its `topology`
annotation keeps every topology it named and adds those that the file names,
so experiments of those topologies can still use it.

To store a new scenario, your role needs `configs` `create`. To replace one,
it needs `configs` `get` and `update`. When the server refuses, the dialog
shows what the server says. A name that the list already has in another
letter case, such as `Riverside-Water` for `riverside-water`, does not show
twice. The list uses the spelling of the stored scenario.

In a published diagram, the checkboxes are not available, and each scenario
has only **View**. The dialog has no **Create new scenario** and no
**Upload a scenario file**, and it has **Close** in place of **Cancel** and
**Save scenarios**.

## Included topologies

riverside-water includes the topology corp-services, so dns-01 and ntp-01
are in the diagram:

- They have a dashed border and say "Included from corp-services".
- Their fields are read only (see
  [Fields you cannot change](editor.md#fields-you-cannot-change)), but you
  can move them.
- They cannot be deleted or connected, and their connections cannot be
  deleted.
- The networks they are on (CORP) cannot be renamed or removed.
- The checks give them no warnings, but they check this diagram's devices
  against them (see [Checks and warnings](editor.md#checks-and-warnings)).

To change dns-01, change the topology corp-services, then import
riverside-water again (see
[Importing a topology or experiment](import-upload-download.md#importing-a-topology-or-experiment)),
or make the included devices part of a new diagram (see
[Making included devices editable](#making-included-devices-editable)).
Publishing does not copy included devices: the published topology names
corp-services in `includeTopologies` (see
[Included topologies](publishing.md#included-topologies)).

### Making included devices editable

To change included devices in the diagram itself, make a copy of the diagram
in which they are its own nodes:

- When you import the topology, choose **Combine into one new topology**
  under **Included topologies** (see
  [Importing a topology or experiment](import-upload-download.md#importing-a-topology-or-experiment)).
- In a draft that is open, run **Combine included nodes into a new draft**
  from the command palette, or select an included device and select
  **Combine into a new draft** in the Inspector.

The command makes a new draft and opens it. The draft you had open does not
change. The new draft is named after the diagram, with "-combined" at the
end, for example Riverside-Water-combined. It gets a number when a topology
or a draft of any user already has that name. Builder says, for example,
"Combined 2 included nodes into new draft Riverside-Water-combined. Draft
Riverside Water is unchanged."

In the new draft, the included devices are normal devices (they are not
read only). You can change, delete and connect them, and the checks include
them. The draft has no link to riverside-water, so publishing it makes a
new topology. When the diagram
never had the nodes of an included topology, `includeTopologies` still names
it, and Builder says so: "It still includes", the topology's name, and
"whose nodes are not in the diagram."
