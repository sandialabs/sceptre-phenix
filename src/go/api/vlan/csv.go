package vlan

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// csvHeader is the header row of WriteCSV.
func csvHeader() []string {
	return []string{"id", "alias", "experiment", "namespace", "running", "states", "phenix", "minimega", "vms", "hosts", "issues"}
}

// WriteCSV writes the rows of rep, which q filtered, as CSV to w: a header row,
// then one row for each VLAN row. When q selects no VLAN ID, state or issue,
// it also writes a row with no ID for each experiment alias that has no pin
// and no live ID. Lists in a cell are separated by ";". A text cell that a
// spreadsheet reads as a formula gets an apostrophe at its start.
func WriteCSV(w io.Writer, rep Report, q Query) error {
	unset := q.VLAN == 0 && q.State == "" && !q.IssuesOnly

	optional := func(id *int) string {
		if id == nil {
			return ""
		}

		return strconv.Itoa(*id)
	}

	out := csv.NewWriter(w)
	_ = out.Write(csvHeader())

	for _, row := range rep.VLANs {
		_ = out.Write([]string{
			strconv.Itoa(row.ID), csvText(row.Alias), csvText(row.Experiment), csvText(row.Namespace),
			strconv.FormatBool(row.Running), strings.Join(row.States, ";"), optional(row.Sources.Phenix),
			optional(row.Sources.Minimega), strconv.Itoa(row.VMs), csvTexts(row.Hosts), strings.Join(row.Issues, ";"),
		})
	}

	for _, e := range rep.Experiments {
		for _, a := range e.Aliases {
			if !unset || a.Pinned != 0 || a.Live != 0 {
				continue
			}

			_ = out.Write([]string{
				"", csvText(a.Alias), csvText(e.Name), "", strconv.FormatBool(e.Running), "", "", "",
				strconv.Itoa(a.VMs), "", "",
			})
		}
	}

	out.Flush()

	if err := out.Error(); err != nil {
		return fmt.Errorf("writing VLAN CSV: %w", err)
	}

	return nil
}

// csvText makes a text cell safe for a spreadsheet: a cell that starts as a
// formula does gets a leading apostrophe.
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}

	return s
}

// csvTexts joins text cells with ";", made safe as one cell.
func csvTexts(list []string) string {
	return csvText(strings.Join(list, ";"))
}
