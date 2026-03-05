package printer

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/olekukonko/tablewriter"

	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
)

const (
	colWidth             = 50
	imageConfigFixedCols = 7
)

// PrintTableOfConfigs writes the given configs to the given writer as an ASCII
// table. The table headers are set to Kind, Version, Name, and Created.
func PrintTableOfConfigs(writer io.Writer, configs store.Configs) {
	table := tablewriter.NewWriter(writer)

	table.SetHeader([]string{"Kind", "Version", "Name", "Created"})

	for _, c := range configs {
		table.Append([]string{c.Kind, c.Version, c.Metadata.Name, c.Metadata.Created})
	}

	table.Render()
}

// PrintTableOfExperiments writes the given experiments to the given writer as
// an ASCII table. The table headers are set to Name, Topology, Scenario,
// Started, VM Count, VLAN Count, and Apps.
func PrintTableOfExperiments(writer io.Writer, exps ...types.Experiment) {
	table := tablewriter.NewWriter(writer)

	table.SetHeader(
		[]string{"Name", "Topology", "Scenario", "Started", "VM Count", "VLAN Count", "Apps"},
	)

	for _, exp := range exps {
		apps := make([]string, 0, len(exp.Apps()))

		for _, app := range exp.Apps() {
			apps = append(apps, app.Name())
		}

		table.Append([]string{
			exp.Spec.ExperimentName(),
			exp.Metadata.Annotations["topology"],
			exp.Metadata.Annotations["scenario"],
			exp.Status.StartTime(),
			strconv.Itoa(len(exp.Spec.Topology().Nodes())),
			strconv.Itoa(len(exp.Spec.VLANs().Aliases())),
			strings.Join(apps, ", "),
		})
	}

	table.Render()
}

// PrintTableOfVMs writes the given VMs to the given writer as an ASCII table.
// The table headers are set to Host, Name, Running, Disk, Interfaces, and
// Uptime.
func PrintTableOfVMs(writer io.Writer, vms ...mm.VM) {
	table := tablewriter.NewWriter(writer)

	switch len(vms) {
	case 0:
		return
	case 1:
		buildSingleVMTable(table, vms[0])
	default:
		buildMultipleVMTable(table, vms...)
	}

	table.Render()
}

func buildMultipleVMTable(table *tablewriter.Table, vms ...mm.VM) {
	table.SetHeader(
		[]string{
			"Host",
			"Name",
			"Running",
			"Disk",
			"Interfaces",
			"Uptime",
			"Memory",
			"VCPUs",
			"OS Type",
		},
	)
	table.SetAutoWrapText(false)
	table.SetColWidth(colWidth)

	for _, vm := range vms {
		var (
			running = strconv.FormatBool(vm.Running)
			ifaces  = make([]string, 0, len(vm.Networks))
			uptime  string
		)

		for idx, nw := range vm.Networks {
			ifaces = append(ifaces, fmt.Sprintf("ID: %d, IP: %s, VLAN: %s", idx, vm.IPv4[idx], nw))
		}

		if vm.Running {
			uptime = (time.Duration(vm.Uptime) * time.Second).String()
		}

		table.Append(
			[]string{
				vm.Host,
				vm.Name,
				running,
				vm.Disk,
				strings.Join(ifaces, "\n"),
				uptime,
				strconv.Itoa(vm.RAM),
				strconv.Itoa(vm.CPUs),
				vm.OSType,
			},
		)
	}
}

func buildSingleVMTable(table *tablewriter.Table, vm mm.VM) {
	table.SetHeader([]string{"Setting", "Value"})
	table.SetAutoWrapText(false)
	table.SetColWidth(colWidth)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	var (
		ifaces   = make([]string, 0, len(vm.Networks))
		uptime   string
		metadata []byte
	)

	for idx, nw := range vm.Networks {
		ifaces = append(ifaces, fmt.Sprintf("ID: %d, IP: %s, VLAN: %s", idx, vm.IPv4[idx], nw))
	}

	if vm.Running {
		uptime = (time.Duration(vm.Uptime) * time.Second).String()
	}

	if len(vm.Metadata) > 0 {
		metadata, _ = json.MarshalIndent(vm.Metadata, "", "  ")
	}

	table.Append([]string{"Host", vm.Host})
	table.Append([]string{"Name", vm.Name})
	table.Append([]string{"Running", strconv.FormatBool(vm.Running)})
	table.Append([]string{"Disk", vm.Disk})
	table.Append([]string{"Interfaces", strings.Join(ifaces, "\n")})
	table.Append([]string{"Uptime", uptime})
	table.Append([]string{"VCPUs", strconv.Itoa(vm.CPUs)})
	table.Append([]string{"Memory", strconv.Itoa(vm.RAM)})
	table.Append([]string{"OS Type", vm.OSType})
	table.Append([]string{"Metadata", string(metadata)})
}

func PrintTableOfImageConfigs(writer io.Writer, optional []string, imgs ...types.Image) {
	var (
		table = tablewriter.NewWriter(writer)
		cols  = make([]string, 0, imageConfigFixedCols+len(optional))
	)

	cols = append(cols, "Name", "Size", "Variant", "Release", "Overlays", "Packages", "Scripts")
	cols = append(cols, optional...)

	table.SetHeader(cols)

	for _, img := range imgs {
		scripts := make([]string, 0, len(img.Spec.Scripts))

		for s := range img.Spec.Scripts {
			scripts = append(scripts, s)
		}

		row := []string{
			img.Metadata.Name,
			img.Spec.Size,
			img.Spec.Variant,
			img.Spec.Release,
			strings.Join(img.Spec.Overlays, "\n"),
			strings.Join(img.Spec.Packages, "\n"),
			strings.Join(scripts, "\n"),
		}

		for _, col := range optional {
			switch col {
			case "Format":
				row = append(row, string(img.Spec.Format))
			case "Compressed":
				row = append(row, strconv.FormatBool(img.Spec.Compress))
			case "Mirror":
				row = append(row, img.Spec.Mirror)
			}
		}

		table.Append(row)
	}

	table.Render()
}

func PrintTableOfVLANAliases(writer io.Writer, info map[string]map[string]int) {
	table := tablewriter.NewWriter(writer)
	table.SetHeader([]string{"Experiment", "VLAN Alias", "VLAN ID"})

	experiments := make([]string, 0, len(info))

	for exp := range info {
		experiments = append(experiments, exp)
	}

	sort.Strings(experiments)

	for _, exp := range experiments {
		aliases := make([]string, 0, len(info[exp]))

		for alias := range info[exp] {
			aliases = append(aliases, alias)
		}

		sort.Strings(aliases)

		for _, alias := range aliases {
			table.Append([]string{exp, alias, strconv.Itoa(info[exp][alias])})
		}
	}

	table.Render()
}

func PrintTableOfVLANRanges(writer io.Writer, info map[string][2]int) {
	table := tablewriter.NewWriter(writer)
	table.SetHeader([]string{"Experiment", "VLAN Range"})

	experiments := make([]string, 0, len(info))

	for exp := range info {
		experiments = append(experiments, exp)
	}

	sort.Strings(experiments)

	for _, exp := range experiments {
		r := fmt.Sprintf("%d - %d", info[exp][0], info[exp][1])

		table.Append([]string{exp, r})
	}

	table.Render()
}

func PrintTableOfSubnetCaptures(writer io.Writer, captures []mm.Capture) {
	table := tablewriter.NewWriter(writer)
	table.SetHeader([]string{"Name", "Interface Index", "File Path"})

	for _, capture := range captures {
		table.Append([]string{capture.VM, strconv.Itoa(capture.Interface), capture.Filepath})
	}

	table.Render()
}

func PrintTableOfSettings(writer io.Writer, settings []types.Setting) {
	var (
		table = tablewriter.NewWriter(writer)
		cols  = []string{"Name", "Category", "Value"}
	)

	table.SetHeader(cols)

	table.SetColumnAlignment([]int{
		tablewriter.ALIGN_DEFAULT,
		tablewriter.ALIGN_DEFAULT,
		tablewriter.ALIGN_RIGHT,
	})

	for _, setting := range settings {
		row := []string{
			setting.Spec.Name,
			setting.Spec.Category,
			setting.Spec.Value,
		}

		table.Append(row)
	}

	table.Render()
}

func PrintTableOfRuntimeSettings(writer io.Writer, settings map[string]any) {
	table := tablewriter.NewWriter(writer)
	table.SetHeader([]string{"Key", "Value"})
	table.SetAutoWrapText(false)
	table.SetColWidth(colWidth)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	var data [][]string

	var flatten func(prefix string, m map[string]any)
	flatten = func(prefix string, m map[string]any) {
		for k, v := range m {
			newPrefix := k
			if prefix != "" {
				newPrefix = prefix + "." + k
			}
			switch val := v.(type) {
			case map[string]any:
				flatten(newPrefix, val)
			default:
				data = append(data, []string{newPrefix, fmt.Sprintf("%v", val)})
			}
		}
	}

	flatten("", settings)

	// Sort data by key
	sort.Slice(data, func(i, j int) bool {
		return data[i][0] < data[j][0]
	})

	table.AppendBulk(data)
	table.Render()
}
