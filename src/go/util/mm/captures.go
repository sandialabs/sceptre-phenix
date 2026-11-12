package mm

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"phenix/util/mm/mmcli"
)

// The capture file formats of minimega 3.4.0.
const (
	CaptureFormatPcapng = "pcapng"
	CaptureFormatPcap   = "pcap"
)

const (
	namespaceCaptureCmd = "capture pcap namespace"
	bridgeMirrorsCmd    = "bridge mirrors"
	minSnaplen          = 14
	maxSnaplen          = 65535
	maxFilterLength     = 1024
)

// ErrInvalidCaptureOption is wrapped by the error of a capture start whose snaplen, format or host is not valid.
var ErrInvalidCaptureOption = errors.New("invalid capture option")

var (
	// captureFilterRegex is the BPF filter text that phenix passes in double quotes: no quote, backslash or "#".
	// Go's regexp allows at most 1000 repeats, so maxFilterLength is checked apart.
	captureFilterRegex = regexp.MustCompile(`^[A-Za-z0-9 .:/\[\]()&|!<>=+\-*,]*$`)

	// mirrorHostRegex is a node name that phenix writes into a mesh send host list.
	mirrorHostRegex = regexp.MustCompile(`^[A-Za-z0-9._+=@:-]+$`)
)

// captureFormatArg returns the format= argument for format f, "" for none, or an error for a format minimega lacks.
func captureFormatArg(f string) (string, error) {
	switch f {
	case "":
		return "", nil
	case CaptureFormatPcapng, CaptureFormatPcap:
		return " format=" + f, nil
	}

	return "", fmt.Errorf("%w: format %q is not %s or %s", ErrInvalidCaptureOption, f, CaptureFormatPcapng, CaptureFormatPcap)
}

// captureMaxSizeArg returns the maxsize= argument for mib MiB, "" for no limit.
func captureMaxSizeArg(mib int64) string {
	if mib <= 0 {
		return ""
	}

	return fmt.Sprintf(" maxsize=%dM", mib)
}

// formatOfPath is the capture format that the extension of path gives, "" for no path.
func formatOfPath(path string) string {
	switch {
	case path == "":
		return ""
	case strings.EqualFold(filepath.Ext(path), "."+CaptureFormatPcapng):
		return CaptureFormatPcapng
	}

	return CaptureFormatPcap
}

// ValidateCaptureFilter returns an error wrapping ErrUnsafeArg for a BPF filter that phenix cannot give minimega in
// double quotes: one of more than 1024 bytes, or with a character other than letters, digits, spaces and
// . : / [ ] ( ) & | ! < > = + - * , (thus no quote, backslash or "#"). An empty filter is valid.
func ValidateCaptureFilter(f string) error {
	if len(f) > maxFilterLength || !captureFilterRegex.MatchString(f) {
		return fmt.Errorf("%w: capture filter %q", ErrUnsafeArg, f)
	}

	return nil
}

// namespaceCaptureCommand is the capture pcap namespace start command for o, or an error for an option minimega
// would not take as written.
func namespaceCaptureCommand(o options) (string, error) {
	if o.captureFile == "" || filepath.IsAbs(o.captureFile) {
		return "", fmt.Errorf("%w: the capture file %q must be a relative path", ErrInvalidCaptureOption, o.captureFile)
	}

	if err := ValidateArg(o.captureFile); err != nil {
		return "", fmt.Errorf("capture file: %w", err)
	}

	var b strings.Builder

	b.WriteString(namespaceCaptureCmd + " " + o.captureFile)

	if len(o.captureVLANs) > 0 {
		for _, vlan := range o.captureVLANs {
			if err := ValidateArg(vlan); err != nil {
				return "", fmt.Errorf("capture VLAN: %w", err)
			}

			if strings.Contains(vlan, ",") {
				return "", fmt.Errorf("capture VLAN: %w: %q contains ','", ErrUnsafeArg, vlan)
			}
		}

		b.WriteString(" vlans=" + strings.Join(o.captureVLANs, ","))
	}

	if err := ValidateCaptureFilter(o.captureFilter); err != nil {
		return "", err
	}

	if o.captureFilter != "" {
		b.WriteString(` filter="` + o.captureFilter + `"`)
	}

	if o.captureSnaplen != 0 {
		if o.captureSnaplen < minSnaplen || o.captureSnaplen > maxSnaplen {
			return "", fmt.Errorf("%w: snaplen %d is not from %d to %d",
				ErrInvalidCaptureOption, o.captureSnaplen, minSnaplen, maxSnaplen)
		}

		b.WriteString(" snaplen=" + strconv.Itoa(o.captureSnaplen))
	}

	format, err := captureFormatArg(o.captureFormat)
	if err != nil {
		return "", err
	}

	b.WriteString(format + captureMaxSizeArg(o.captureMaxSize))

	return b.String(), nil
}

// StartNamespaceCapture starts an experiment capture of the namespace (NS) into CaptureFile, relative to minimega's
// -filepath, with the CaptureVLANs, CaptureFilter, CaptureSnaplen, CaptureFormat and CaptureMaxSize options. It
// returns the capture's ID.
func (Minimega) StartNamespaceCapture(opts ...Option) (string, error) {
	o := NewOptions(opts...)

	if err := captureSupport(o.ctx).unsupported(); err != nil {
		return "", err
	}

	command, err := namespaceCaptureCommand(o)
	if err != nil {
		return "", err
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = command

	id, err := mmcli.SingleResponse(o.run(cmd))
	if err != nil {
		return "", fmt.Errorf("starting an experiment capture in namespace %s: %w", o.ns, err)
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("starting an experiment capture in namespace %s: minimega gave no capture ID", o.ns)
	}

	return id, nil
}

// StopNamespaceCapture stops the experiment capture CaptureID of the namespace (NS) and returns its final rows.
func (Minimega) StopNamespaceCapture(opts ...Option) ([]NamespaceCapture, error) {
	o := NewOptions(opts...)

	if err := captureSupport(o.ctx).unsupported(); err != nil {
		return nil, err
	}

	if err := ValidateArg(o.captureID); err != nil {
		return nil, fmt.Errorf("capture ID: %w", err)
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "capture pcap delete namespace " + o.captureID

	rows, err := mmcli.RunTabularCheckedContext(o.ctx, cmd, nil)
	if err != nil {
		return nil, fmt.Errorf("stopping experiment capture %s in namespace %s: %w", o.captureID, o.ns, err)
	}

	return parseNamespaceCaptures(rows), nil
}

// GetNamespaceCaptures lists the experiment captures of the namespace (NS), sorted by ID.
func (Minimega) GetNamespaceCaptures(opts ...Option) ([]NamespaceCapture, error) {
	o := NewOptions(opts...)

	if err := captureSupport(o.ctx).unsupported(); err != nil {
		return nil, err
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = namespaceCaptureCmd

	rows, err := mmcli.RunTabularReadOnlyChecked(o.ctx, nsLine(o.ns), cmd, nil)
	if err != nil {
		return nil, fmt.Errorf("listing the experiment captures of namespace %s: %w", o.ns, err)
	}

	return parseNamespaceCaptures(rows), nil
}

// GetBridgeMirrors lists the Open vSwitch mirrors of the MirrorHosts hosts: the head node's with a local `bridge
// mirrors`, the others' with one mesh send. Each mirror names the host that answered.
func (Minimega) GetBridgeMirrors(opts ...Option) ([]BridgeMirror, error) {
	o := NewOptions(opts...)

	if err := captureSupport(o.ctx).unsupported(); err != nil {
		return nil, err
	}

	var (
		local  bool
		remote []string
	)

	for _, host := range o.mirrorHosts {
		if !mirrorHostRegex.MatchString(host) {
			return nil, fmt.Errorf("%w: host name %q", ErrInvalidCaptureOption, host)
		}

		switch {
		case IsHeadnode(host):
			local = true
		case !slices.Contains(remote, host):
			remote = append(remote, host)
		}
	}

	var mirrors []BridgeMirror

	if local {
		cmd := mmcli.NewCommand()
		cmd.Command = bridgeMirrorsCmd

		rows, err := mmcli.RunTabularReadOnlyChecked(o.ctx, mmcli.HeadRead, cmd, nil)
		if err != nil {
			return nil, fmt.Errorf("listing the Open vSwitch mirrors of the head node: %w", err)
		}

		head := Headnode()

		for _, row := range rows {
			mirrors = append(mirrors, bridgeMirror(head, row))
		}
	}

	if len(remote) > 0 {
		cmd := mmcli.NewCommand()
		cmd.Command = "mesh send " + strings.Join(remote, ",") + " " + bridgeMirrorsCmd

		rows, err := mmcli.RunTabularReadOnlyChecked(o.ctx, mmcli.MeshRead, cmd, nil)
		if err != nil {
			return nil, fmt.Errorf("listing the Open vSwitch mirrors of %s: %w", strings.Join(remote, ", "), err)
		}

		for _, row := range rows {
			mirrors = append(mirrors, bridgeMirror(row[hostColumn], row))
		}
	}

	return mirrors, nil
}

func bridgeMirror(host string, row map[string]string) BridgeMirror {
	return BridgeMirror{
		Host:      host,
		Bridge:    row["bridge"],
		Mirror:    row["mirror"],
		Output:    row["output"],
		Namespace: row["namespace"],
		Kind:      row["kind"],
	}
}

// parseNamespaceCaptures groups the rows of a capture pcap namespace listing by id. The row with an empty host
// and bridge is a capture's summary; mmcli keeps minimega's own host column, so the head node's name does not fill
// it. The other rows are its streams. A row with no id is dropped.
func parseNamespaceCaptures(rows []map[string]string) []NamespaceCapture {
	var (
		captures   []NamespaceCapture
		index      = make(map[string]int)
		summarized = make(map[string]bool)
	)

	for _, row := range rows {
		id := strings.TrimSpace(row["id"])
		if id == "" {
			continue
		}

		i, ok := index[id]
		if !ok {
			i = len(captures)
			index[id] = i

			captures = append(captures, NamespaceCapture{ID: id}) //nolint:exhaustruct // filled from its rows
		}

		c := &captures[i]

		if row[hostColumn] == "" && row["bridge"] == "" {
			if !summarized[id] {
				summarized[id] = true

				summarize(c, row)
			}

			continue
		}

		c.Streams = append(c.Streams, CaptureStream{
			Host:            row[hostColumn],
			Bridge:          row["bridge"],
			State:           row[stateColumn],
			CaptureCounters: captureCounters(row),
		})
	}

	for i := range captures {
		slices.SortStableFunc(captures[i].Streams, func(a, b CaptureStream) int {
			if c := strings.Compare(a.Host, b.Host); c != 0 {
				return c
			}

			return strings.Compare(a.Bridge, b.Bridge)
		})
	}

	slices.SortStableFunc(captures, func(a, b NamespaceCapture) int { return CompareCaptureIDs(a.ID, b.ID) })

	return captures
}

// summarize fills c from its summary row.
func summarize(c *NamespaceCapture, row map[string]string) {
	c.State = row[stateColumn]
	c.Path = row["path"]
	c.Format = formatOfPath(c.Path)
	c.Filter = row["filter"]
	c.Start = row["start"]
	c.Error = row["error"]
	c.Size = parseInt64(row["size"])
	c.CaptureCounters = captureCounters(row)

	c.VLANs = nil

	for vlan := range strings.SplitSeq(row["vlans"], ",") {
		if vlan = strings.TrimSpace(vlan); vlan != "" {
			c.VLANs = append(c.VLANs, vlan)
		}
	}
}

// CompareCaptureIDs orders numeric IDs by number, before any other ID, which sorts by text.
func CompareCaptureIDs(a, b string) int {
	na, errA := strconv.ParseUint(a, 10, 64)
	nb, errB := strconv.ParseUint(b, 10, 64)

	switch {
	case errA == nil && errB == nil:
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		}

		return 0
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}

	return strings.Compare(a, b)
}

func captureCounters(row map[string]string) CaptureCounters {
	return CaptureCounters{
		Ports:   parseUint64(row["ports"]),
		Packets: parseUint64(row["packets"]),
		Bytes:   parseUint64(row["bytes"]),
		KDrops:  parseUint64(row["kdrops"]),
		PDrops:  parseUint64(row["pdrops"]),
		QDrops:  parseUint64(row["qdrops"]),
		Lost:    parseUint64(row["lost"]),
		Late:    parseUint64(row["late"]),
		Lag:     parseDuration(row["lag"]),
		Skew:    parseDuration(row["skew"]),
	}
}

// parseUint64 is s as a number, 0 when it is not one.
func parseUint64(s string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}

	return n
}

// parseInt64 is s as a number, 0 when it is not one.
func parseInt64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}

	return n
}

// parseDuration is s, such as 95ms or -600µs, as a duration, 0 when it is empty or not one.
func parseDuration(s string) time.Duration {
	d, _ := time.ParseDuration(strings.TrimSpace(s))

	return d
}

// vmCapture is a row of the capture listing of minimega 3.4.0 or later, read by column name, and false for a row
// that is not on a VM interface.
func vmCapture(row map[string]string) (Capture, bool) {
	vm, idx, ok := vmInterface(row["interface"])
	if !ok {
		return Capture{}, false
	}

	format := row["format"]
	if format == "" {
		format = formatOfPath(row["path"])
	}

	return Capture{
		VM:        vm,
		Interface: idx,
		Filepath:  row["path"],
		ID:        row["id"],
		State:     row[stateColumn],
		Format:    format,
		Packets:   parseUint64(row["packets"]),
		KDrops:    parseUint64(row["kdrops"]),
		Size:      parseInt64(row["size"]),
		Error:     row["error"],
	}, true
}
