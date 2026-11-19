# Accessibility

This page is for you if you use only a keyboard, a screen reader, speech
input or browser zoom, or if you need more contrast or less motion.

The project's target for changes to the web UI is WCAG 2.2 level AA. Its
browser tests scan most pages with axe-core: the main UI with the A, AA and
AAA rules (which include 7:1 contrast for text), and Builder with A and AA
rules in both of its themes.

## Keyboard

* `Skip to main content` is the first link on every page, so press
  <kbd>Tab</kbd> at the top of a page to reach it. The Builder editor starts
  with a second one, `Skip to diagram canvas`.
* A tooltip shows when you move focus to its control, not just on hover.
  That's how an icon-only button in the navbar gives its name.
* The `Resources` and `Admin` menus in the navbar are buttons.
  <kbd>Enter</kbd> opens one, <kbd>Tab</kbd> moves through its links and
  <kbd>Esc</kbd> closes it.
* When a dialog or a menu closes, focus goes back to what opened it.
* In a table, <kbd>Tab</kbd> stops on each sortable column heading, and
  <kbd>Enter</kbd> or <kbd>Space</kbd> sorts by it.

[Builder](#builder), the [VM consoles](#vm-consoles), the
[config editor](#the-config-editor) and the
[minimega console](minimega.md#web-ui) have keys of their own.

## Screen Readers

* Each page has its own title, with the most specific part first, for
  example `bennu - Experiment - phēnix`.
* A failure notification is an alert, so your screen reader reads it when it
  opens.
* The refresh button in the navbar tells you when a reload failed or the
  data may be out of date.
* The screen of a VM is a picture, so a screen reader can't read it. The
  console page reads out each change of a VM's connection, such as
  `web: Connected`.
* In Builder, use your screen reader's focus mode (forms mode in JAWS) for
  the keys of the canvas. Builder reads out what each change did. A device
  or a switch has the facts of its
  [info tooltip](builder/details/editor.md#info-tooltips), and what the
  checks found, as its description.

## Display

### Zoom and Narrow Windows

phēnix is laid out for desktop browsers, not phones. Browser zoom works like
a narrower window:

* From 1024 to 1399 pixels wide, the navbar links and menus show only their
  icons, with their names in tooltips. Below 1024 pixels, a menu button
  holds them, with their names.
* In Builder, the side columns stack and the page scrolls (see
  [Side Columns](builder/details/editor.md#side-columns)). They also stack
  when you enlarge only the text.

### Themes and Contrast

* The main UI has one theme, which is dark. The project's rule for it is a
  text contrast of at least 7:1.
* Builder has a `Theme` of its own: `System`, `Light` or `Dark` (see
  [Themes](builder/details/editor.md#themes)). With a Windows contrast
  theme, Builder draws the lines and the selection on the canvas in your
  system colors.

### Motion

The web UI follows your device's setting for reduced motion: animations and
transitions stop, and a loading spinner stays on screen but doesn't turn.
Builder also has `Reduce motion` in `Builder settings` (off by default), for
when your device doesn't ask.

### Text Size

Besides browser zoom, the [minimega console](minimega.md#web-ui)
has `Text size` buttons (10 to 24 pixels), and a
[VM console](vms.md#the-console-page) shows the VM's screen at 50% to 200%
with `Actual` in its `View` panel. Builder has zoom buttons on its canvas, and
`Node icon size` in [Settings](builder/details/editor.md#settings).

## Builder

You can use Builder with only a keyboard, or with a screen reader. It has a
[command palette](builder/editor.md#command-palette), and you can
[change](builder/details/palette-and-shortcuts.md#customizing-shortcuts) its
[shortcuts](builder/reference.md#keyboard-shortcuts).

* The diagram, the toolbar and the header counts are each one
  <kbd>Tab</kbd> stop. The arrow keys, <kbd>Home</kbd> and <kbd>End</kbd>
  move between the toolbar buttons and between the counts. On the diagram,
  the arrow keys move between nodes (see
  [Keys on the Canvas](builder/details/palette-and-shortcuts.md#keys-on-the-canvas)).
* To add and connect nodes without a pointer, use `Nodes & Objects` and the
  [Inspector header](builder/details/editor.md#the-inspector-header), or
  type `connect` or `group` in the command palette.
* To find a node without moving through the canvas, use `Go to node` in the
  command palette.
* The cards of the drafts page are one <kbd>Tab</kbd> stop for each tab (see
  [Selecting with the Keyboard](builder/details/drafts.md#selecting-with-the-keyboard)).
* A few shortcuts are a key with no <kbd>Ctrl</kbd> or <kbd>⌘</kbd>, such as
  <kbd>N</kbd> and <kbd>?</kbd>. If speech input or your screen reader
  presses them by mistake, turn off `Single-key shortcuts` (see
  [Single-Key Shortcuts](builder/details/palette-and-shortcuts.md#single-key-shortcuts)).

## VM Consoles

A VM console sends every key to the VM, <kbd>Tab</kbd> included. To move
focus out of the console, press <kbd>Ctrl</kbd>+<kbd>]</kbd> and then
<kbd>Tab</kbd> (see [The Keyboard Chord](vms.md#the-keyboard-chord)).

The terminal that [SCORCH](scorch.md) opens for a component and the
[minimega console](minimega.md#web-ui) work the same way:
<kbd>Ctrl</kbd>+<kbd>]</kbd>, then <kbd>Tab</kbd> or
<kbd>Shift</kbd>+<kbd>Tab</kbd>, leaves the terminal.

## The Config Editor

On the `Configs` page, the name of a config is a button: <kbd>Tab</kbd> to
it and press <kbd>Enter</kbd> to open the viewer.

In the [editor window](configuration.md#editor-window), <kbd>Tab</kbd> stops
on the line numbers and then on the text. On the text, press
<kbd>Enter</kbd> to type, and <kbd>Esc</kbd> to stop typing so that
<kbd>Tab</kbd> moves on. With `Vim Mode` on, the first <kbd>Esc</kbd> leaves
insert mode, so press it a second time.

## The Command Line

phēnix colors statuses, warnings and errors in its output, and the text is
the same without the colors. To turn them off, pass `--color off` or set
`NO_COLOR` (see [Colors](settings.md#colors)).

## See Also

* [Using the Web UI](web-ui.md): the navbar, tables and notifications.
* [Builder Reference](builder/reference.md#keyboard-shortcuts): every Builder shortcut.
* [VMs](vms.md#vm-vnc-access): the console page and its keyboard chord.
* [Settings](settings.md#colors): the `color` setting and `--color`.
