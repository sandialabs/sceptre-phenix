package printer

import (
	"io"

	"phenix/store"

	"github.com/olekukonko/tablewriter"
)

// PrintTableOfEvents writes the given events to the given writer as an ASCII
// table.
func PrintTableOfEvents(writer io.Writer, events store.Events, showID bool) {
	table := tablewriter.NewWriter(writer)
	table.SetAutoMergeCellsByColumnIndex([]int{0, 1, 2})
	table.SetAutoWrapText(false)
	table.SetRowLine(true)

	if showID {
		table.SetHeader([]string{"ID", "Timestamp", "Type", "Source", "Message"})
	} else {
		table.SetHeader([]string{"Timestamp", "Type", "Source", "Message"})
	}

	for _, e := range events {
		ts := e.Timestamp.Format("01/02/2006 15:04 MST")

		if showID {
			table.Append([]string{e.ID, ts, string(e.Type), e.Source, e.Message})
		} else {
			table.Append([]string{ts, string(e.Type), e.Source, e.Message})
		}
	}

	table.Render()
}

func PrintEventTable(writer io.Writer, event store.Event) {
	table := tablewriter.NewWriter(writer)
	table.SetAutoWrapText(false)
	table.SetRowLine(true)

	table.SetHeader([]string{"Key", "Value"})

	table.Append([]string{"ID", event.ID})
	table.Append([]string{"Timestamp", event.Timestamp.Format("01/02/2006 15:04 MST")})
	table.Append([]string{"Type", string(event.Type)})
	table.Append([]string{"Source", event.Source})
	table.Append([]string{"Message", event.Message})

	if event.Metadata != nil {
		table.Append([]string{"Metadata", ""})

		for k, v := range event.Metadata {
			table.Append([]string{k, v})
		}
	}

	table.Render()
}
