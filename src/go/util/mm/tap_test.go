package mm

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
)

// tapOn is the command TapVLAN sends to run base on compute1.
func tapOn(base string) string { return ".record false mesh send compute1 " + base }

// tapInExpOn is the command TapVLAN sends to run base in namespace exp on
// compute1.
func tapInExpOn(base string) string {
	return `.record false mesh send compute1 namespace "exp" ` + base
}

func TestTapVLAN(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	nsSteps := []string{
		"ip netns add t1 || exit 100",
		"ip link set dev t1 netns t1 || exit 101",
		"ip netns exec t1 ip link set dev t1 up || exit 102",
		"ip netns exec t1 ip addr add 10.0.0.1/24 dev t1 || exit 103",
	}

	create := tapInExpOn("tap create exp//EXP_1 bridge br0 name t1")
	netns := []string{create, tapOn(`shell sh -c "` + strings.Join(nsSteps, "; ") + `"`)}
	netnsNoIP := []string{create, tapOn(`shell sh -c "` + strings.Join(nsSteps[:3], "; ") + `"`)}

	for name, tc := range map[string]struct {
		opts    []TapOption
		failing string // the command minimega fails, "" for none
		status  string // the error of the failing command, "boom" when ""
		want    []string
		wantMsg string // a part of the error, "" for none
	}{
		"on the headnode by default": {
			opts: []TapOption{TapNS("exp"), TapName("t1"), TapVLANAlias("EXP_1")},
			want: []string{inExp("tap create exp//EXP_1 bridge phenix name t1")},
		},
		"with an IP": {
			opts: []TapOption{TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("EXP_1"), TapIP("10.0.0.1/24")},
			want: []string{tapInExpOn("tap create exp//EXP_1 bridge phenix ip 10.0.0.1/24 t1")},
		},
		"keeps a VLAN that names its namespace": {
			opts: []TapOption{TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("other//EXP_1")},
			want: []string{tapInExpOn("tap create other//EXP_1 bridge phenix name t1")},
		},
		"in a network namespace": {
			opts: []TapOption{
				TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("EXP_1"), TapBridge("br0"),
				TapIP("10.0.0.1/24"), TapNetNS("t1"),
			},
			want: netns,
		},
		"in a network namespace without an IP": {
			opts: []TapOption{TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("EXP_1"), TapBridge("br0"), TapNetNS("t1")},
			want: netnsNoIP,
		},
		"reports a failed create": {
			opts: []TapOption{
				TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("EXP_1"), TapBridge("br0"),
				TapIP("10.0.0.1/24"), TapNetNS("t1"),
			},
			failing: netns[0], want: netns[:1], wantMsg: "boom",
		},
		"reports a failed network namespace": {
			opts: []TapOption{
				TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("EXP_1"), TapBridge("br0"),
				TapIP("10.0.0.1/24"), TapNetNS("t1"),
			},
			failing: netns[1], status: "exit status 100", want: netns, wantMsg: "creating network namespace",
		},
		"reports a failed move": {
			opts: []TapOption{
				TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("EXP_1"), TapBridge("br0"),
				TapIP("10.0.0.1/24"), TapNetNS("t1"),
			},
			failing: netns[1], status: "exit status 101", want: netns, wantMsg: "moving tap to network namespace",
		},
		"reports a failed link up": {
			opts: []TapOption{
				TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("EXP_1"), TapBridge("br0"),
				TapIP("10.0.0.1/24"), TapNetNS("t1"),
			},
			failing: netns[1], status: "exit status 102", want: netns, wantMsg: "bringing tap up",
		},
		"reports a failed address": {
			opts: []TapOption{
				TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("EXP_1"), TapBridge("br0"),
				TapIP("10.0.0.1/24"), TapNetNS("t1"),
			},
			failing: netns[1], status: "exit status 103", want: netns, wantMsg: "setting IP address for tap",
		},
		"deletes the tap and its network namespace": {
			opts: []TapOption{TapNS("exp"), TapHost("compute1"), TapName("t1"), TapNetNS("t1"), TapDelete()},
			want: []string{tapInExpOn("tap delete t1"), tapOn("shell ip netns delete t1")},
		},
		"deletes the network namespace of a tap it fails to delete": {
			opts:    []TapOption{TapNS("exp"), TapHost("compute1"), TapName("t1"), TapNetNS("t1"), TapDelete()},
			failing: tapInExpOn("tap delete t1"),
			want:    []string{tapInExpOn("tap delete t1"), tapOn("shell ip netns delete t1")},
			wantMsg: "boom",
		},
		"reports a failed network namespace delete": {
			opts:    []TapOption{TapNS("exp"), TapHost("compute1"), TapName("t1"), TapNetNS("t1"), TapDelete()},
			failing: tapOn("shell ip netns delete t1"),
			want:    []string{tapInExpOn("tap delete t1"), tapOn("shell ip netns delete t1")},
			wantMsg: "boom",
		},
		"deletes a tap without a network namespace": {
			opts: []TapOption{TapNS("exp"), TapHost("compute1"), TapName("t1"), TapDelete()},
			want: []string{tapInExpOn("tap delete t1")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Raw == tc.failing {
					msg := tc.status
					if msg == "" {
						msg = "boom"
					}

					return []*minicli.Response{mmtest.Error("compute1", msg)}
				}

				return answering(nil)(cmd)
			})

			err := TapVLAN(tc.opts...)

			switch {
			case tc.wantMsg == "" && err != nil:
				t.Errorf("TapVLAN() error = %v, want nil", err)
			case tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)):
				t.Errorf("TapVLAN() error = %v, want one holding %q", err, tc.wantMsg)
			}

			if diff := cmp.Diff(tc.want, sentRaw(received())); diff != "" {
				t.Errorf("TapVLAN() sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TapVLAN sends nothing when minimega would not read a value back as the one
// argument TapVLAN writes it as. A delete does not use the VLAN or bridge.
func TestTapVLANRefusesUnsafeValues(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		opt     TapOption
		deletes bool // whether a delete uses the value too
	}{
		"name":              {opt: TapName("t 1"), deletes: true},
		"empty name":        {opt: TapName(""), deletes: true},
		"host":              {opt: TapHost("compute1 #"), deletes: true},
		"IP":                {opt: TapIP("10.0.0.1/24 t2"), deletes: true},
		"network namespace": {opt: TapNetNS("t'1"), deletes: true},
		"VLAN":              {opt: TapVLANAlias("EXP_1 #"), deletes: false},
		"bridge":            {opt: TapBridge("$BRIDGE"), deletes: false},
	} {
		for _, del := range []bool{false, true} {
			if del && !tc.deletes {
				continue
			}

			t.Run(fmt.Sprintf("%s/delete=%t", name, del), func(t *testing.T) {
				received := useFakeMinimega(t, nil)

				opts := []TapOption{TapNS("exp"), TapHost("compute1"), TapName("t1"), TapVLANAlias("EXP_1"), TapBridge("br0"), tc.opt}
				if del {
					opts = append(opts, TapDelete())
				}

				if err := TapVLAN(opts...); !errors.Is(err, ErrUnsafeArg) {
					t.Errorf("TapVLAN(%s, delete %t) error = %v, want %v", name, del, err, ErrUnsafeArg)
				}

				if diff := cmp.Diff([]mmtest.Command(nil), received()); diff != "" {
					t.Errorf("TapVLAN(%s, delete %t) sent commands mismatch (-want +got):\n%s", name, del, diff)
				}
			})
		}
	}
}
