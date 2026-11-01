package experiment

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// The formats a recorded netflow capture downloads as.
const (
	NetflowFormatText  = "text"
	NetflowFormatCSV   = "csv"
	NetflowFormatJSONL = "jsonl"
)

const (
	netflowLineBuffer   = 64 << 10
	netflowOutputBuffer = 32 << 10
	netflowCancelCheck  = 1024
)

// ErrInvalidNetflowFormat is returned for a format ExportNetflow cannot write.
var ErrInvalidNetflowFormat = errors.New("netflow format must be text, csv or jsonl")

// netflowRecord is one parsed minimega ascii netflow line.
type netflowRecord struct {
	Start          time.Time
	Duration       time.Duration
	Proto          uint8
	Src, Dst       netip.Addr
	SPort, DPort   uint16
	Packets, Bytes uint64
}

// netflowColumns are the exported fields, in order: the CSV header and the
// JSONL keys.
var netflowColumns = []string{ //nolint:gochecknoglobals // constant list
	"event.start", "event.end", "event.duration", "source.ip", "source.port", "destination.ip",
	"destination.port", "network.transport", "network.iana_number", "network.packets", "network.bytes",
}

// netflowTransports are the IANA keywords of the protocols ECS names.
var netflowTransports = map[uint8]string{ //nolint:gochecknoglobals // constant table
	1: "icmp", 2: "igmp", 6: "tcp", 17: "udp", 47: "gre", 50: "esp", 51: "ah", 58: "ipv6-icmp", 132: "sctp",
}

// netflowJSON is a record as a JSONL line; its fields are in netflowColumns'
// order.
type netflowJSON struct {
	Start      string `json:"event.start"`
	End        string `json:"event.end"`
	Duration   int64  `json:"event.duration"`
	SrcIP      string `json:"source.ip"`
	SrcPort    uint16 `json:"source.port"`
	DstIP      string `json:"destination.ip"`
	DstPort    uint16 `json:"destination.port"`
	Transport  string `json:"network.transport,omitempty"`
	IANANumber string `json:"network.iana_number"`
	Packets    uint64 `json:"network.packets"`
	Bytes      uint64 `json:"network.bytes"`
}

// parseNetflowRecord strictly parses a minimega ascii netflow line:
// start, duration, protocol, source, "<->", destination, packets and bytes.
func parseNetflowRecord(line string) (netflowRecord, bool) {
	var rec netflowRecord

	fields := strings.Fields(line)
	if len(fields) != netflowMinFieldCount {
		return rec, false
	}

	start, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return rec, false
	}

	duration, err := time.ParseDuration(fields[1])
	if err != nil || duration < 0 || start.Add(duration).Before(start) {
		return rec, false
	}

	f, ok := parseNetflowFields(fields)
	if !ok {
		return rec, false
	}

	return netflowRecord{
		Start: start, Duration: duration, Proto: f.proto, Src: f.src.Unmap(), Dst: f.dst.Unmap(),
		SPort: f.sport, DPort: f.dport, Packets: f.packets, Bytes: f.bytes,
	}, true
}

// json is the record as a JSONL line's object.
func (r netflowRecord) json() netflowJSON {
	return netflowJSON{
		Start:      r.Start.UTC().Format(time.RFC3339Nano),
		End:        r.Start.Add(r.Duration).UTC().Format(time.RFC3339Nano),
		Duration:   int64(r.Duration),
		SrcIP:      r.Src.String(),
		SrcPort:    r.SPort,
		DstIP:      r.Dst.String(),
		DstPort:    r.DPort,
		Transport:  netflowTransports[r.Proto],
		IANANumber: strconv.FormatUint(uint64(r.Proto), 10),
		Packets:    r.Packets,
		Bytes:      r.Bytes,
	}
}

// csv is the record as a CSV row.
func (r netflowRecord) csv() []string {
	j := r.json()

	return []string{
		j.Start, j.End, strconv.FormatInt(j.Duration, 10), j.SrcIP, strconv.FormatUint(uint64(j.SrcPort), 10),
		j.DstIP, strconv.FormatUint(uint64(j.DstPort), 10), j.Transport, j.IANANumber,
		strconv.FormatUint(j.Packets, 10), strconv.FormatUint(j.Bytes, 10),
	}
}

// NetflowExport is what ExportNetflow converted: the flows written and the
// lines skipped because they did not parse.
type NetflowExport struct{ Flows, Skipped int64 }

// lazyWriter calls start for the writer on its first write.
type lazyWriter struct {
	start func() io.Writer
	w     io.Writer
}

func (l *lazyWriter) Write(b []byte) (int, error) {
	if l.w == nil {
		l.w = l.start()
	}

	return l.w.Write(b) //nolint:wrapcheck // the caller's writer
}

// ExportNetflow converts a capture's lines from src to CSV or JSONL. It calls
// w for the output only once it writes, so an error before then sends nothing.
// A final line with no newline is a write in progress, and is skipped.
func ExportNetflow(ctx context.Context, w func() io.Writer, src io.Reader, format string) (NetflowExport, error) {
	var stats NetflowExport

	if format != NetflowFormatCSV && format != NetflowFormatJSONL {
		return stats, ErrInvalidNetflowFormat
	}

	var (
		in   = bufio.NewReaderSize(src, netflowLineBuffer)
		lazy = &lazyWriter{start: w, w: nil}
		out  = bufio.NewWriterSize(lazy, netflowOutputBuffer)
		rows = csv.NewWriter(out)
		enc  = json.NewEncoder(out)
		read int
	)

	if format == NetflowFormatCSV {
		_ = rows.Write(netflowColumns)
	}

	for {
		if read%netflowCancelCheck == 0 && ctx.Err() != nil {
			return stats, fmt.Errorf("exporting netflow capture: %w", ctx.Err())
		}

		read++

		line, complete, err := readNetflowLine(in)
		if err != nil {
			return stats, fmt.Errorf("reading netflow capture: %w", err)
		}

		if line == nil {
			break
		}

		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		rec, ok := parseNetflowRecord(string(line))
		if !complete || !ok {
			stats.Skipped++

			continue
		}

		if format == NetflowFormatCSV {
			err = rows.Write(rec.csv())
		} else {
			err = enc.Encode(rec.json())
		}

		if err != nil {
			return stats, fmt.Errorf("writing netflow export: %w", err)
		}

		stats.Flows++
	}

	rows.Flush()

	if err := rows.Error(); err != nil {
		return stats, fmt.Errorf("writing netflow export: %w", err)
	}

	if err := out.Flush(); err != nil {
		return stats, fmt.Errorf("writing netflow export: %w", err)
	}

	// an empty JSONL export still answers
	if lazy.w == nil {
		lazy.w = w()
	}

	return stats, nil
}

// readNetflowLine reads the next line, without its newline, and whether it
// ended in one; nil at the end. A line longer than the buffer is consumed
// and returned as a one-byte placeholder that never parses.
func readNetflowLine(in *bufio.Reader) ([]byte, bool, error) {
	line, err := in.ReadSlice('\n')

	switch {
	case err == nil:
		return line[:len(line)-1], true, nil
	case errors.Is(err, io.EOF):
		if len(line) == 0 {
			return nil, false, nil
		}

		return line, false, nil
	case errors.Is(err, bufio.ErrBufferFull):
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = in.ReadSlice('\n')
		}

		if err != nil && !errors.Is(err, io.EOF) {
			return nil, false, err //nolint:wrapcheck // wrapped by the caller
		}

		return []byte{'-'}, true, nil
	default:
		return nil, false, err //nolint:wrapcheck // wrapped by the caller
	}
}
