package plog_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/plog"
)

func TestParsePart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		num, total any
		want       plog.Part
		wantOK     bool
	}{
		{name: "first of three", num: 1.0, total: 3.0, want: plog.Part{Num: 1, Total: 3}, wantOK: true},
		{name: "last of three", num: 3.0, total: 3.0, want: plog.Part{Num: 3, Total: 3}, wantOK: true},
		{name: "one of one", num: 1.0, total: 1.0, want: plog.Part{Num: 1, Total: 1}, wantOK: true},
		{name: "missing", num: nil, total: nil},
		{name: "missing total", num: 1.0, total: nil},
		{name: "string", num: "1", total: "3"},
		{name: "fraction", num: 1.5, total: 3.0},
		{name: "zero", num: 0.0, total: 3.0},
		{name: "negative", num: -1.0, total: 3.0},
		{name: "past total", num: 4.0, total: 3.0},
		{name: "too large", num: 1.0, total: 1e12},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := plog.ParsePart(tc.num, tc.total)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("ParsePart(%v, %v) = %v, %t, want %v, %t", tc.num, tc.total, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

type joined struct {
	Level string
	First int
	Msg   string
}

func TestPartJoiner(t *testing.T) {
	t.Parallel()

	type add struct {
		level      string
		num, total int
		msg        string
	}

	tests := []struct {
		name string
		adds []add
		want []joined
	}{
		{
			name: "complete message",
			adds: []add{{"INFO", 1, 3, "a"}, {"INFO", 2, 3, "b"}, {"INFO", 3, 3, "c"}},
			want: []joined{{Level: "INFO", First: 0, Msg: "a\nb\nc"}},
		},
		{
			name: "two messages",
			adds: []add{{"INFO", 1, 2, "a"}, {"INFO", 2, 2, "b"}, {"WARN", 1, 2, "c"}, {"WARN", 2, 2, "d"}},
			want: []joined{{Level: "INFO", First: 0, Msg: "a\nb"}, {Level: "WARN", First: 2, Msg: "c\nd"}},
		},
		{
			name: "skipped part",
			adds: []add{{"INFO", 1, 3, "a"}, {"INFO", 3, 3, "c"}},
			want: []joined{{Level: "INFO", First: 0, Msg: "a"}, {Level: "INFO", First: 1, Msg: "c"}},
		},
		{
			name: "other total",
			adds: []add{{"INFO", 1, 3, "a"}, {"INFO", 2, 2, "b"}},
			want: []joined{{Level: "INFO", First: 0, Msg: "a"}, {Level: "INFO", First: 1, Msg: "b"}},
		},
		{
			name: "pending until flush",
			adds: []add{{"INFO", 2, 3, "b"}},
			want: []joined{{Level: "INFO", First: 0, Msg: "b"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []joined

			j := plog.NewPartJoiner(func(level string, first int, msg string) {
				got = append(got, joined{Level: level, First: first, Msg: msg})
			})

			for i, a := range tc.adds {
				j.Add(a.level, plog.Part{Num: a.num, Total: a.total}, a.msg, i)
			}

			j.Flush()

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("PartJoiner(%v) messages mismatch (-want +got):\n%s", tc.adds, diff)
			}

			if j.Pending() {
				t.Error("Pending() after Flush() = true, want false")
			}
		})
	}
}

// A PartJoiner loses no part and changes no order, whatever parts it gets.
func TestPartJoinerKeepsEveryPart(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		count := rapid.IntRange(0, 20).Draw(t, "count")

		in := make([]string, 0, count)

		var out []string

		j := plog.NewPartJoiner(func(_ string, _ struct{}, msg string) { out = append(out, msg) })

		for range count {
			total := rapid.IntRange(1, 4).Draw(t, "total")
			num := rapid.IntRange(1, total).Draw(t, "num")
			level := rapid.SampledFrom([]string{"INFO", "ERROR"}).Draw(t, "level")
			msg := rapid.StringMatching(`[a-z]{0,3}`).Draw(t, "msg")

			in = append(in, msg)
			j.Add(level, plog.Part{Num: num, Total: total}, msg, struct{}{})
		}

		j.Flush()

		if got, want := strings.Join(out, "\n"), strings.Join(in, "\n"); got != want {
			t.Fatalf("joined messages = %q, want %q", got, want)
		}

		if len(out) > len(in) {
			t.Fatalf("PartJoiner emitted %d messages for %d parts", len(out), len(in))
		}
	})
}
