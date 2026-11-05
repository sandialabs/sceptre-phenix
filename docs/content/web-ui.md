# Using the Web UI

The phēnix web UI is a single page in the browser. Whichever tab you are on,
the banner at the top holds the page links and menus, the refresh button, and
a `Docs` button that opens this documentation. This page describes the
behavior that is the same everywhere. The pages themselves are described
under [Experiments](experiments.md), [VMs](vms.md), [Disks](disks.md),
[Hosts](hosts.md), [VLANs](vlans.md), and the rest.

## The Navbar

![The navbar with the Experiments, Builder, SCORCH and SoH links, the Resources and Admin menus, and the Docs, refresh, background jobs and logout buttons.](images/navbar.png){: width=800 .center}

The links and menus appear in this order, and only when your role and the
server allow them. Each link has an icon and a tooltip.

| Link | Needs |
| :--- | :--- |
| `Experiments` | `list` on `experiments` |
| `Builder` | `use` on `builder` |
| `SCORCH` | `list` on `experiments` |
| `SoH` (State of Health) | `list` on `experiments` |
| `WebShark` | the `webshark` feature, plus `get` on `experiments/files` or `list` on `vms/captures` |
| `Resources` menu: `Configs` | `list` on `configs` |
| `Resources` menu: `Disks` | `list` on `disks` |
| `Resources` menu: `Hosts` | `list` on `hosts` |
| `Resources` menu: `VLANs` | `list` on `vlans` |
| `Admin` menu: `Users` | any role other than `Disabled` |
| `Admin` menu: `Logs` | `get` on `logs` |
| `Admin` menu: `Settings` | `update` on `settings` |
| `Admin` menu: `Console` | `post` on `miniconsole` |

A menu shows only the links that your role may open. When your role may open
none of them, the menu does not appear.

With a mouse on a wide window, a menu opens when the pointer is on it. It
closes a short time after the pointer leaves it. A click also opens a menu,
and the menu then stays open.

A feature is something the server found at startup, not a permission: a
WebShark build for `webshark` (see [WebShark](webshark.md)), or a
`downloads/tunneler` directory for `tunneler-download`. With the
`tunneler-download` feature, a `Tunneler` button (an archway icon) at the
right-hand end of the navbar opens the [Tunneler](tunneler.md) download page.
The `Console` page works only when the server runs with
`phenix ui --minimega-console` (see [minimega](minimega.md#from-the-web-ui)).

The right-hand end of the navbar holds these buttons, in this order:
`Tunneler`, `Docs`, the refresh button, the
[background jobs menu](#background-jobs-menu), and `Logout`. Each button shows
only its icon, and its tooltip gives its name. `Docs` (a book icon) opens this
documentation in a new browser tab. With proxy authentication, `Logout` is
`Reauthorize` (a user-shield icon), with the tooltip
`Reauthorize (reload your session and role)`.

The navbar stays on one row in a narrow window:

* Below 1400 pixels wide, the links and menus show only their icons. Hover
  over an icon, or move the focus to it, to see its name in the tooltip.
* Below 1024 pixels wide, a menu button holds all of the links, menus and
  buttons. The links and menus show their icons and text.

## Page Freshness and Refresh

Each page shows the data it last loaded straight away, and loads a fresh copy
behind it. The refresh button (`Refresh this page's data`) at the right-hand
end of the navbar reloads only the page's data. Its color shows how old the
data on screen is. Hover over the button, or move the focus to it, to see
when the data was updated.

![Refresh button with a green icon, and its tooltip that reads Updated just now.](images/header_refresh.png){: width=200 .center}

The tooltip reads:

* `Refreshing…` while a load runs
* `Updated just now`, then `Updated 3 min ago`, and the time of day once the
  data is an hour old
* `Refresh failed` when the last load failed. The page keeps what it had.
* `· may be out of date:` and the reason after the age, when the data may no
  longer be current

The reason tells why: minimega was busy, the data changed while you were on
another page, live updates disconnected, the phēnix server is shutting down
or restarting, or the page has not reloaded for longer than it expects.

![Refresh button with an orange icon, and its tooltip that reads Updated 3 min ago · may be out of date: older than 90 s.](images/header_refresh_stale.png){: width=400 .center}

The button's icon is green while the data is fresh. It is yellow while the
data ages: after half of the page's age limit, or after 1 minute on a page
with no limit. It is orange when the data may be out of date, or after 5
minutes on a page with no limit. It is red after a load failed. The icon
spins while a load runs.

Pages keep the data they last loaded, so returning to a page opens it filled
in while a fresh copy loads. The kept data is cleared when you log out.

## Background Jobs Menu

Next to the refresh button, a button with a checklist icon opens the
[background jobs](jobs.md) you started, such as bulk VM actions, file archives,
disk clones and the scheduled runs of the apps of a started experiment, along
with the jobs of other users your role may see. A badge
on the button counts the jobs still running. When jobs have finished since you
last opened the menu, the button turns red if any of them failed, and green if
they all succeeded.

The menu lists every running or queued job and the ten most recent finished
ones, each with its title, status and progress. A running job has a `Cancel`
button, a finished file archive has a `Download` button, and a finished job
has a `Dismiss` button that removes it. Jobs keep running when you leave the
page, reload or lose the connection, and the menu catches up when the page
reconnects.

![The open jobs menu: two running jobs with progress bars and Cancel buttons, a finished file archive with Download and Dismiss buttons, and a partly succeeded job with a Dismiss button](images/jobs_menu.png){: width=450 .center}

## Tables

The `Paginate` toggle sits above its table, on the right, and appears only
when the table has more rows than one page holds. Each table remembers its own
`Paginate` setting in the browser, not in your account. On a running
experiment's page, the VM table's toggle sits in the toolbar to the left of
the search box.

Click a column heading to sort by it. Tab reaches each sortable heading, and
Enter or Space sorts by it. A table wider than the window scrolls sideways
inside itself.

An empty table says why it is empty. On Disks, for example, it reads
`Loading disks…`, `Could not load disks`, `No disk images found`, or
`No disk images match your search`. Other tables word the same four cases for
what they list.

![Empty Disks table saying No disk images found under its column headings.](images/empty_table.png){: width=600 .center}

## Search Boxes

Every page's search box sits at the right above its table. The Experiments,
Configs, Users, Disks, Logs and SCORCH pages count what they list in a strip
at the left, level with the search box. The counts cover the whole list, not
just the rows the search shows. On an experiment's page, the address bar
names the open tab and its search, for example
`/experiment/demo?tab=files&q=log`, so a shared link opens the same view.

## Timestamps

Times the server records show as an ISO 8601 timestamp with its UTC offset,
followed by how long ago that was, such as `2026-10-28T14:03:22Z (3 hours
ago)`. The age is kept current while the page is open.

## Errors

A failure opens a red notification at the top of the page, which stays until
you dismiss it. Successes and progress show as short messages that go away by
themselves. When the server nests one error inside another, the notification
lists each step on a line of its own, with the root cause last and in bold.

## Permissions

If you open a page your role cannot use, the UI sends you to the first page
your role can use. Buttons and menu items your role cannot use are hidden or
disabled, and a disabled button says why in its tooltip. For what a role may
do, see [User Authn/Authz in phenix](user-administration.md).

## Refresh Rates

Some pages ask the server for fresh data on their own:

| What | How often |
| :--- | :--- |
| The Hosts page | every 30 seconds |
| The VM tiles | every 60 seconds, with screenshots of only the tiles on screen |
| Screenshots of the VMs on a running experiment's page | every 15 seconds, running VMs only |
| The State of Health overview, while a run is in progress | every 15 seconds |

These pages skip a refresh while the last one is still waiting, or while their
browser tab is hidden.

## When an Experiment Goes Away

If a running experiment is stopped or deleted somewhere else, its page says so,
for example `The foo experiment was stopped elsewhere.`, and returns you to
the experiment list.
