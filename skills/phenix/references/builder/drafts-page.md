# Drafts page

Part of the [Builder references](../builder.md). The drafts page's tabs and
cards, bulk actions, and the keyboard model every selectable list shares.

## Tabs and cards

Tabs in order: My Drafts, Shared Drafts, Published Diagrams, Node Templates
(the view's `templates` prop and slot of `BuilderDrafts`), Other users'
drafts (shown when it lists something). A card shows `Owner: …` on one line
and `Updated …` on the next; Open, Share, Delete, Publish and Exp are one
size. Publish on a card (`draft-publish-<id>`, own drafts, edit shares, and
other users' drafts the role may change) loads the draft into the store
without the editor (`store.loadDraft(owner, id, {quiet: true})`) and opens
the editor's Publish dialog over the drafts page, titled `Publish <name>`.
Exp (`published-experiment-<id>` on a published row, and in the editor
toolbar after Share; `store.experiment`, `experimentName`) navigates to the
experiment in the same tab; palette command "Open experiment".

## Bulk actions

Bulk actions are a client loop over the per-item routes (`builder/bulk.js`
`runBulk`, `listSelection.js`, `BuilderBulkBar.vue`, `BuilderBulkSummary.vue`,
`dialogs/BulkShareDialog.vue`): at most 4 requests at once (topology deletes
and downloads 1), one retry on a 412 using the `ETag` the 412 carries. Rows:
Select all, "{n} of {m} selected", Share selected (My Drafts, with sign-in),
Download selected (`bulk-download-<tab>`: My Drafts and Shared Drafts
readable drafts, Published Diagrams rows but `source: "file"`; with the note
`bulk-download-note-<tab>` that the browser may ask to allow several
downloads), Delete selected (My Drafts, Other users' drafts with
`canDelete`, Published Diagrams stored topologies). A card has a checkbox
when one of its tab's actions applies (`selectable` in `BuilderDrafts.vue`).
Download selected (`store.downloadDrafts`, `store.downloadPublishedMany`,
`downloadEach`) reads each document (`GET /builder/drafts/{owner}/{draft}`,
or `readPublished`) and saves it one after another as the open diagram's
Download does (`downloadedDiagram`, `diagramFile` in `session.js`: JSON,
custom icons embedded), announcing "Downloaded 2 drafts." ("diagrams" on
Published Diagrams). Bulk Share merges the chosen users and access into
each draft's list; a draft that would pass 25 users fails. A session that
ends, or a server out of reach, stops the run (`endsBulk` in `api.js`,
runBulk's `stop`; the rest are "Not attempted."), for every batch: the
drafts page's and the Custom icons dialog's (the icon library's `ends`, as
the dialog never imports the API client). A download that fails lists
why: the draft deleted (or no longer visible), the published diagram
deleted or published again, or a diagram this version cannot read
(`DocumentError`).

## Keyboard model of selectable lists

The keyboard model of every selectable list (the four card tabs, the Node
Templates library, both lists of the Custom icons dialog) is
`listSelection.js`: `useListSelection(items, keyOf, {selectable})` keeps
the selection by key, the anchor of a range (the item last toggled;
Select all, `selectAll` and `clear` drop it; `extendTo(item, {from, on})`
lays a range over the selection as it was then, so a shorter range gives
items back), and the item that is the list's one Tab stop (`tabStop`,
`focused` on `focusin`, so focus on a control in a card makes that card
the stop: roving tabindex on the `<li>`, named by `aria-labelledby` its
title and a hidden "selected"/"not selected"). `listKeyAction` and
`onListKeydown`: arrow keys move (Up and Down wrap in a column; in a grid
of cards, `columnsOf`, Left and Right move one card and Up and Down a
row), Home and End, Space toggles (only on the item itself, not a control
in it), Shift+Space and Shift with a move always select the range, also
after a press that unselected (from the focused item when there is no
anchor), Mod+A selects all (`matchesKey`, so on a layout without Latin
letters the key where A is), Escape clears (and is stopped, so a dialog
stays open; with nothing selected it goes on), Delete (Backspace on macOS
too) calls `onDelete` when the list can delete; text fields keep every
key, and `locked` (a batch running) leaves only the moves. A checkbox
pressed with Shift (`shiftPress`: a click, or Space on the box;
`press(item, {checked, range})`) makes the range as the box now is:
checked selects it, cleared unselects it; with no anchor it changes its
item alone, as a plain press. Every change is announced "{n} of {m} selected." (the store's live
region; in the Custom icons dialog its status region, as the live region
waits while a modal is open).
