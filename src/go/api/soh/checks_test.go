package soh

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/app"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/mm"
	"phenix/util/testenv"
)

const (
	// What the web UI and REST API show for a check that C2 kept from running.
	noC2Result = "error: c2 not active on host"
	listening  = "Netid State\ntcp LISTEN"
)

// errNotActive is what minimega returns once c2Timeout passes with no C2 client.
var errNotActive = fmt.Errorf("cannot execute command: %w", mm.ErrC2ClientNotActive)

// execNotActive returns the result of a check whose C2 command found no C2 client.
func execNotActive() string {
	return "error: executing C2 command: " + errNotActive.Error()
}

// noC2Metadata configures every check for web and for rtu, and for spare (which
// does not boot) and ghost (which is not in the topology). md replaces entries.
func noC2Metadata(md map[string]any) map[string]any {
	all := map[string]any{
		"c2Timeout":        "1m",
		"testReachability": "off",
		"hostFiles": map[string]any{
			"web": []any{"/etc/motd"}, "rtu": []any{"/etc/rtu.conf", "/opt/rtu/config.xml"},
			"spare": []any{"/etc/spare"}, "ghost": []any{"/etc/ghost"},
		},
		"hostFilesAbsent":  map[string]any{"rtu": []any{"/tmp/rtu.lock"}},
		"hostServices":     map[string]any{"web": []any{"sshd"}, "rtu": []any{"rtu"}},
		"hostProcesses":    map[string]any{"web": []any{"sshd"}, "rtu": []any{"rtud"}},
		"hostListeners":    map[string]any{"web": []any{":22"}, "rtu": []any{":502"}},
		"dockerContainers": map[string]any{"web": []any{"nginx"}, "rtu": []any{"historian"}},
		"hostCustomTests": map[string]any{"rtu": []any{
			map[string]any{"name": "ready", "testScript": "echo ok", "testStdout": "ok"},
		}},
	}

	maps.Copy(all, md)

	return all
}

// rtuProfiles is another app that gives rtu, spare and ghost an SoH profile.
func rtuProfiles() *v2.ScenarioApp {
	profile := map[string]any{"sohProfile": map[string]any{
		"processes":   []any{"rtu-agent"},
		"listeners":   []any{":20000"},
		"customTests": []any{map[string]any{"name": "profile", "testScript": "true", "testStdout": "x"}},
	}}

	return &v2.ScenarioApp{NameF: "rtu-app", HostsF: []*v2.ScenarioAppHost{
		{HostnameF: "rtu", MetadataF: profile},
		{HostnameF: "spare", MetadataF: profile},
		{HostnameF: "ghost", MetadataF: profile},
	}}
}

// noC2Nodes are web, rtu and spare, which does not boot.
func noC2Nodes() []*v1.Node {
	dnb := true

	spare := sohNode("spare", "linux", sohIface("eth0", "A", "static", "10.0.0.3", ""))
	spare.GeneralF.DoNotBootF = &dnb

	return []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
		sohNode("rtu", "linux", sohIface("eth0", "A", "static", "10.0.0.2", "")),
		spare,
	}
}

// webAnswers returns the answers of web, which passes each of its checks.
func webAnswers() map[string]string {
	return map[string]string{
		"exp/web: ip addr":                                      "inet 10.0.0.1/24",
		"exp/web: stat -c present -- /etc/motd":                 "present",
		"exp/web: systemctl is-active -- sshd":                  "active",
		"exp/web: pgrep -f -- sshd":                             "1",
		"exp/web: ss -lntu state all 'sport = 22'":              listening,
		"exp/web: docker inspect " + dockerFormat + " -- nginx": "running",
		"exp/web: cat /proc/loadavg":                            "0.25 0.30 0.35 1/200 4242",
	}
}

// answerNoC2 answers as answers do, but finds no C2 client on the hosts in noC2.
func answerNoC2(answers map[string]string, noC2 ...string) func(c2Call) (string, error) {
	answer := answerFrom(answers)

	return func(c c2Call) (string, error) {
		if slices.Contains(noC2, c.VM) {
			return "", errNotActive
		}

		return answer(c)
	}
}

// keysWithout returns the keys of m, without the given keys.
func keysWithout(m map[string]string, without ...string) []string {
	keys := make([]string, 0, len(m))

	for k := range m {
		if !slices.Contains(without, k) {
			keys = append(keys, k)
		}
	}

	return keys
}

// Once a check finds that a host has no active C2, each later check of that
// host, its custom tests and the checks of SoH profiles from other apps
// included, is recorded as failed and sends no C2 command. A host in
// skipHosts, a host that does not boot and a host not in the topology get no
// results.
func TestNoC2HostReportsEveryCheck(t *testing.T) { //nolint:paralleltest // replaces package globals
	laterChecks := []string{
		"file absent map[host:rtu path:/tmp/rtu.lock] = " + noC2Result,
		"service map[host:rtu service:rtu] = " + noC2Result,
		"process map[host:rtu proc:rtud] = " + noC2Result,
		"process map[host:rtu proc:rtu-agent] = " + noC2Result,
		"listener map[host:rtu port::502] = " + noC2Result,
		"listener map[host:rtu port::20000] = " + noC2Result,
		"container map[container:historian host:rtu] = " + noC2Result,
		"custom test map[host:rtu test:ready] = " + noC2Result,
		"custom test map[host:rtu test:profile] = " + noC2Result,
	}

	webChecks := []string{
		"file map[host:web path:/etc/motd] = file found",
		"service map[host:web service:sshd] = service running",
		"process map[host:web proc:sshd] = process running",
		"listener map[host:web port::22] = listening on port",
		"container map[container:nginx host:web] = container running",
	}

	tests := map[string]struct {
		md      map[string]any
		rtuCmds []string // C2 commands sent to rtu
		want    map[string]hostResult
		wantErr bool
	}{
		"network check finds no C2": {
			rtuCmds: []string{"exp/rtu: ip addr"},
			want: map[string]hostResult{
				"web": {CPULoad: "0.25", Checks: sorted(append(webChecks, "network map[host:web] = IP 10.0.0.1 configured")...)},
				"rtu": {CPULoad: "c2 not active on host", Checks: sorted(append(laterChecks,
					"network map[host:rtu] = "+execNotActive(),
					"file map[host:rtu path:/etc/rtu.conf] = "+noC2Result,
					"file map[host:rtu path:/opt/rtu/config.xml] = "+noC2Result,
				)...)},
			},
			wantErr: true,
		},
		"file check finds no C2": {
			md:      map[string]any{"skipInitialNetworkConfigTests": true},
			rtuCmds: []string{"exp/rtu: stat -c present -- /etc/rtu.conf", "exp/rtu: stat -c present -- /opt/rtu/config.xml"},
			want: map[string]hostResult{
				"web": {CPULoad: "0.25", Checks: sorted(webChecks...)},
				"rtu": {CPULoad: "c2 not active on host", Checks: sorted(append(laterChecks,
					"file map[host:rtu path:/etc/rtu.conf] = "+execNotActive(),
					"file map[host:rtu path:/opt/rtu/config.xml] = "+execNotActive(),
				)...)},
			},
			wantErr: true,
		},
		"host in skipHosts": {
			md: map[string]any{"skipHosts": []any{"rtu"}},
			want: map[string]hostResult{
				"web": {CPULoad: "0.25", Checks: sorted(append(webChecks, "network map[host:web] = IP 10.0.0.1 configured")...)},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			exp := sohExperiment(t, noC2Metadata(tt.md), noC2Nodes(), rtuProfiles())
			fake := useSOHMM(t, answerNoC2(webAnswers(), "rtu"))

			webCmds := keysWithout(webAnswers())
			if tt.md["skipInitialNetworkConfigTests"] == true {
				webCmds = keysWithout(webAnswers(), "exp/web: ip addr")
			}

			if err := newSOH().Running(t.Context(), exp); (err != nil) != tt.wantErr {
				t.Errorf("Running(exp) error = %v, want error %t", err, tt.wantErr)
			}

			checkSent(t, "Running(exp)", fake, append(webCmds, tt.rtuCmds...), nil)

			if diff := cmp.Diff(tt.want, storedResults(t)); diff != "" {
				t.Errorf("Running(exp) stored results mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The first check that finds no C2 on a host, whichever it is, stops the
// later checks of that host: its CPU load is not read.
func TestFirstCheckWithoutC2StopsLaterChecks(t *testing.T) { //nolint:paralleltest // replaces package globals
	reach := func(ip, target string) string {
		return fmt.Sprintf("reachability map[host:rtu ip:%s target:%s] = %s", ip, target, execNotActive())
	}

	tests := map[string]struct {
		checks []string
		cmds   []string
		want   []string
		load   string // the CPU load result, c2 not active on host if empty
	}{
		"reachability": {
			checks: []string{"network-config", "reachability"},
			cmds:   []string{"exp/rtu: ip addr", "exp/rtu: ping -c 1 10.0.0.2"},
			want: []string{
				"network map[host:rtu] = IP 10.0.0.2 configured",
				reach("10.0.0.2", "rtu"), reach("10.0.0.2", "rtu"),
			},
		},
		"custom reachability": {
			checks: []string{"network-config", "custom-reachability"},
			cmds:   []string{"exp/rtu: ip addr", "exp/rtu: test-conn tcp 10.0.0.9 80 wait 5s"},
			want: []string{
				"network map[host:rtu] = IP 10.0.0.2 configured",
				"reachability map[host:rtu ip:10.0.0.9 port:80 proto:tcp target:10.0.0.9] = " + execNotActive(),
			},
		},
		"files": {
			checks: []string{"files"},
			cmds:   []string{"exp/rtu: stat -c present -- /etc/rtu.conf"},
			want:   []string{"file map[host:rtu path:/etc/rtu.conf] = " + execNotActive()},
		},
		"absent files": {
			checks: []string{"files-absent"},
			cmds:   []string{"exp/rtu: stat -c present -- /tmp/rtu.lock"},
			want:   []string{"file absent map[host:rtu path:/tmp/rtu.lock] = " + execNotActive()},
		},
		"services": {
			checks: []string{"services"},
			cmds:   []string{"exp/rtu: systemctl is-active -- rtu"},
			want:   []string{"service map[host:rtu service:rtu] = " + execNotActive()},
		},
		"processes": {
			checks: []string{"processes"},
			cmds:   []string{"exp/rtu: pgrep -f -- rtud", "exp/rtu: pgrep -f -- rtu-agent"},
			want: []string{
				"process map[host:rtu proc:rtud] = " + execNotActive(),
				"process map[host:rtu proc:rtu-agent] = " + execNotActive(),
			},
		},
		"listeners": {
			checks: []string{"ports"},
			cmds:   []string{"exp/rtu: ss -lntu state all 'sport = 502'", "exp/rtu: ss -lntu state all 'sport = 20000'"},
			want: []string{
				"listener map[host:rtu port::502] = " + execNotActive(),
				"listener map[host:rtu port::20000] = " + execNotActive(),
			},
		},
		"containers": {
			checks: []string{"docker"},
			cmds:   []string{"exp/rtu: docker inspect " + dockerFormat + " -- historian"},
			want:   []string{"container map[container:historian host:rtu] = " + execNotActive()},
		},
		"custom tests": {
			checks: []string{"custom"},
			cmds: []string{
				"exp/rtu: send exp/rtu-ready; bash /tmp/miniccc/files/exp/rtu-ready",
				"exp/rtu: send exp/rtu-profile; bash /tmp/miniccc/files/exp/rtu-profile",
			},
			want: []string{
				"custom test map[host:rtu test:ready] = " + execNotActive(),
				"custom test map[host:rtu test:profile] = " + execNotActive(),
			},
		},
		"CPU load": {
			cmds: []string{},
			load: "executing command 'cat /proc/loadavg': " + errNotActive.Error(),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useSOHFilesDir(t)

			md := noC2Metadata(map[string]any{
				"testReachability":       "full",
				"testCustomReachability": []any{map[string]any{"src": "rtu", "dst": "10.0.0.9", "proto": "tcp", "port": 80}},
				"hostFiles":              map[string]any{"rtu": []any{"/etc/rtu.conf"}},
			})
			exp := sohExperiment(t, md, []*v1.Node{noC2Nodes()[1]}, rtuProfiles())

			// rtu answers ip addr, then has no C2 for each later command
			fake := useSOHMM(t, func(c c2Call) (string, error) {
				if c.String() == "exp/rtu: ip addr" {
					return "inet 10.0.0.2/24", nil
				}

				return "", errNotActive
			})

			s := newSOH()
			if err := s.decodeMetadata(exp); err != nil {
				t.Fatalf("decodeMetadata(exp) error = %v", err)
			}

			s.apps = exp.Spec.Scenario().Apps()

			ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": append(tt.checks, "cpu-load")})
			if err := s.runChecks(ctx, exp); err == nil {
				t.Errorf("runChecks(%q) error = nil, want an error", tt.checks)
			}

			cmds := tt.cmds
			if tt.load != "" {
				cmds = append(cmds, "exp/rtu: cat /proc/loadavg")
			}

			checkSent(t, fmt.Sprintf("runChecks(%q)", tt.checks), fake, cmds, nil)

			load := tt.load
			if load == "" {
				load = "c2 not active on host"
			}

			want := map[string]hostResult{"rtu": {CPULoad: load, Checks: sorted(tt.want...)}}
			if tt.want == nil {
				want["rtu"] = hostResult{CPULoad: load}
			}

			if diff := cmp.Diff(want, storedResults(t)); diff != "" {
				t.Errorf("runChecks(%q) stored results mismatch (-want +got):\n%s", tt.checks, diff)
			}
		})
	}
}

// A custom reachability test runs from the host it names, with the wait and
// UDP packet it gives, and to an address or a host's interface. A test from a
// host without C2 or without networking is recorded as failed without a C2
// command, and a test from a host in skipHosts or not in the topology gives no
// result.
func TestCustomReachability(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := sohExperiment(t, map[string]any{
		"c2Timeout":                 "200ms",
		"testReachability":          "off",
		"skipHosts":                 []any{"old"},
		"hostsToUseUUIDForC2Active": "web",
		"testCustomReachability": []any{
			map[string]any{"src": "web", "dst": "10.0.0.9", "proto": "tcp", "port": 22, "wait": "2s"},
			map[string]any{"src": "web", "dst": "rtu|eth0", "proto": "udp", "port": 53, "wait": "soon", "udpPacketBase64": "aGVsbG8="},
			map[string]any{"src": "rtu", "dst": "10.0.0.1", "proto": "tcp", "port": 80},
			map[string]any{"src": "lost", "dst": "10.0.0.1", "proto": "tcp", "port": 80},
			map[string]any{"src": "old", "dst": "10.0.0.1", "proto": "tcp", "port": 80},
			map[string]any{"src": "nosuch", "dst": "10.0.0.1", "proto": "tcp", "port": 80},
		},
	}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
		sohNode("rtu", "linux", sohIface("eth0", "A", "static", "10.0.0.2", "")),
		sohNode("lost", "linux", sohIface("eth0", "A", "static", "10.0.0.3", "")),
		sohNode("old", "linux", sohIface("eth0", "A", "static", "10.0.0.4", "")),
	})

	answers := map[string]string{
		"exp/web: ip addr (uuid)":                                    "inet 10.0.0.1/24",
		"exp/web: test-conn tcp 10.0.0.9 22 wait 2s (uuid)":          "success",
		"exp/web: test-conn udp 10.0.0.2 53 wait 5s aGVsbG8= (uuid)": "fail",
		"exp/lost: ip addr":                                          "inet 10.9.9.9/24",
	}

	fake := useSOHMM(t, answerNoC2(answers, "rtu"))

	s := newSOH(withRetryDelay(time.Millisecond))
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"network-config", "custom-reachability"}})
	if err := s.runChecks(ctx, exp); err == nil {
		t.Error("runChecks(custom reachability) error = nil, want an error")
	}

	checkSent(t, "runChecks(custom reachability)", fake,
		[]string{
			"exp/rtu: ip addr",
			"exp/web: ip addr (uuid)",
			"exp/web: test-conn tcp 10.0.0.9 22 wait 2s (uuid)",
			"exp/web: test-conn udp 10.0.0.2 53 wait 5s aGVsbG8= (uuid)",
		},
		[]string{"exp/lost: ip addr"},
	)

	var (
		toWeb = "reachability map[host:%s ip:10.0.0.1 target:web] = error: %s"
		udp   = "reachability map[host:web ip:10.0.0.2 port:53 proto:udp target:rtu] = error: failed to connect to udp://10.0.0.2:53"
		fromR = fmt.Sprintf(toWeb, "rtu", "c2 not active on host")
		fromL = fmt.Sprintf(toWeb, "lost", "networking not configured on host")
	)

	want := map[string]hostResult{
		"web": {Checks: sorted(
			"network map[host:web] = IP 10.0.0.1 configured",
			"reachability map[host:web ip:10.0.0.9 port:22 proto:tcp target:10.0.0.9] = connection to tcp://10.0.0.9:22 succeeded",
			udp, fromR, fromL,
		)},
		"rtu":  {Checks: sorted("network map[host:rtu] = "+execNotActive(), udp, fromR)},
		"lost": {Checks: sorted("network map[host:lost] = error: retry time expired waiting for IP to be set", fromL)},
	}
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("runChecks(custom reachability) stored results mismatch (-want +got):\n%s", diff)
	}
}

// Sample reachability pings one address of each VLAN from each host. A VLAN
// whose hosts all failed their network check gives a failed result for one of
// its addresses, and a host whose network check failed pings nothing.
func TestSampleReachability(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := sohExperiment(t, map[string]any{"c2Timeout": "200ms", "testReachability": "sample"}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
		sohNode("win", "windows", sohIface("eth0", "B", "static", "10.0.1.1", "")),
		sohNode("lost", "linux", sohIface("eth0", "C", "static", "10.0.2.1", "")),
		sohNode("bare", "linux", sohIface("eth0", "A", "static", "", "")),
	})

	answers := map[string]string{
		"exp/web: ip addr":             "inet 10.0.0.1/24",
		"exp/web: ping -c 1 10.0.0.1":  "1 received",
		"exp/web: ping -c 1 10.0.1.1":  "1 received",
		"exp/win: ipconfig /all":       "IPv4 Address: 10.0.1.1",
		"exp/win: ping -n 1 10.0.0.1":  "Destination host unreachable",
		"exp/win: ping -n 1 10.0.1.1":  "Reply from 10.0.1.1",
		"exp/lost: ip addr":            "inet 10.9.9.9/24",
		"exp/bare: ip addr":            "",
		"exp/bare: ping -c 1 10.0.0.1": "1 received",
		"exp/bare: ping -c 1 10.0.1.1": "1 received",
	}

	fake := useSOHMM(t, answerFrom(answers))

	s := newSOH(withRetryDelay(time.Millisecond))
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"network-config", "reachability"}})
	if err := s.runChecks(ctx, exp); err == nil {
		t.Error("runChecks(sample reachability) error = nil, want an error")
	}

	checkSent(t, "runChecks(sample reachability)", fake, keysWithout(answers, "exp/lost: ip addr"), []string{"exp/lost: ip addr"})

	reach := func(from, to, ip, outcome string) string {
		return fmt.Sprintf("reachability map[host:%s ip:%s target:%s] = %s", from, ip, to, outcome)
	}

	var (
		noNetDst = "error: networking not configured on target"
		noNet    = "error: networking not configured on host"
		pinged   = func(ip string) string { return "pinging " + ip + " succeeded" }

		webWeb  = reach("web", "web", "10.0.0.1", pinged("10.0.0.1"))
		webWin  = reach("web", "win", "10.0.1.1", pinged("10.0.1.1"))
		webLost = reach("web", "lost", "10.0.2.1", noNetDst)
		winWeb  = reach("win", "web", "10.0.0.1", "error: no successful pings")
		winWin  = reach("win", "win", "10.0.1.1", pinged("10.0.1.1"))
		winLost = reach("win", "lost", "10.0.2.1", noNetDst)
		lostWeb = reach("lost", "web", "10.0.0.1", noNet)
		lostWin = reach("lost", "win", "10.0.1.1", noNet)
		lostL   = reach("lost", "lost", "10.0.2.1", noNetDst)
		bareWeb = reach("bare", "web", "10.0.0.1", pinged("10.0.0.1"))
		bareWin = reach("bare", "win", "10.0.1.1", pinged("10.0.1.1"))
		bareL   = reach("bare", "lost", "10.0.2.1", noNetDst)
	)

	want := map[string]hostResult{
		"web": {Checks: sorted("network map[host:web] = IP 10.0.0.1 configured",
			webWeb, webWeb, webWin, webLost, winWeb, lostWeb, bareWeb)},
		"win": {Checks: sorted("network map[host:win] = IP 10.0.1.1 configured",
			winWin, winWin, winWeb, winLost, webWin, lostWin, bareWin)},
		"lost": {Checks: sorted("network map[host:lost] = error: retry time expired waiting for IP to be set",
			lostL, lostL, lostWeb, lostWin, webLost, winLost, bareL)},
		"bare": {Checks: sorted(bareWeb, bareWin, bareL)},
	}
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("runChecks(sample reachability) stored results mismatch (-want +got):\n%s", diff)
	}
}

// With ICMP reachability off and no custom reachability tests, the
// reachability checks send nothing and record nothing.
func TestReachabilityOff(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := sohExperiment(t, map[string]any{"testReachability": "off"}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
	})

	fake := useSOHMM(t, answerFrom(map[string]string{"exp/web: ip addr": "inet 10.0.0.1/24"}))

	s := newSOH()
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"network-config", "reachability"}})
	if err := s.runChecks(ctx, exp); err != nil {
		t.Errorf("runChecks(reachability off) error = %v", err)
	}

	checkSent(t, "runChecks(reachability off)", fake, []string{"exp/web: ip addr"}, nil)

	want := map[string]hostResult{"web": {Checks: []string{"network map[host:web] = IP 10.0.0.1 configured"}}}
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("runChecks(reachability off) stored results mismatch (-want +got):\n%s", diff)
	}
}

// answerInTurn answers each C2 command with each of its answers in turn, then
// with the last one again.
func answerInTurn(answers map[string][]string) func(c2Call) (string, error) {
	var (
		mu    sync.Mutex
		calls = map[string]int{}
	)

	return func(c c2Call) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		replies, ok := answers[c.String()]
		if !ok {
			return "", fmt.Errorf("unexpected C2 command %s", c)
		}

		reply := replies[min(calls[c.String()], len(replies)-1)]
		calls[c.String()]++

		return reply, nil
	}
}

// The network check waits, on Linux and on Windows, for the address, then for
// the default gateway, then for a ping of the gateway, and fails at the first
// of them that is not ready before c2Timeout.
func TestNetworkCheckWaitsForEachStep(t *testing.T) { //nolint:paralleltest // replaces package globals
	const gw = "10.0.0.254"

	nodes := []*v1.Node{
		sohNode("lnx-ok", "linux", sohIface("eth0", "A", "static", "10.0.0.1", gw)),
		sohNode("lnx-nogw", "linux", sohIface("eth0", "A", "static", "10.0.0.2", gw)),
		sohNode("lnx-down", "linux", sohIface("eth0", "A", "static", "10.0.0.3", gw)),
		sohNode("win-ok", "windows", sohIface("eth0", "A", "static", "10.0.0.11", gw)),
		sohNode("win-noip", "windows", sohIface("eth0", "A", "static", "10.0.0.12", gw)),
		sohNode("win-nogw", "windows", sohIface("eth0", "A", "static", "10.0.0.13", gw)),
		sohNode("win-down", "windows", sohIface("eth0", "A", "static", "10.0.0.14", gw)),
	}

	exp := sohExperiment(t, map[string]any{"c2Timeout": "500ms"}, nodes)

	var (
		route   = "0.0.0.0    0.0.0.0    " + gw
		answers = map[string][]string{}
		linux   = func(host string, ipR, routeR, pingR []string) {
			answers["exp/"+host+": ip addr"] = append([]string{""}, ipR...)
			answers["exp/"+host+": ip route"] = append([]string{""}, routeR...)
			answers["exp/"+host+": ping -c 1 "+gw] = pingR
		}
		windows = func(host string, ipR, routeR, pingR []string) {
			answers["exp/"+host+": ipconfig /all"] = append([]string{""}, ipR...)
			answers["exp/"+host+": route print"] = append([]string{""}, routeR...)
			answers["exp/"+host+": ping -n 1 "+gw] = pingR
		}
		up      = "1 received"
		down    = "1 packets transmitted, 0 received"
		reply   = "Reply from " + gw
		noReply = "Destination host unreachable"
	)

	linux("lnx-ok", []string{"inet 10.0.0.1/24"}, []string{"default via " + gw}, []string{down, up})
	linux("lnx-nogw", []string{"inet 10.0.0.2/24"}, []string{""}, nil)
	linux("lnx-down", []string{"inet 10.0.0.3/24"}, []string{"default via " + gw}, []string{down})
	windows("win-ok", []string{"10.0.0.11"}, []string{route}, []string{noReply, reply})
	windows("win-noip", []string{""}, nil, nil)
	windows("win-nogw", []string{"10.0.0.13"}, []string{""}, nil)
	windows("win-down", []string{"10.0.0.14"}, []string{route}, []string{noReply})

	useSOHMM(t, answerInTurn(answers))

	s := newSOH(withRetryDelay(time.Millisecond))
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"network-config"}})
	if err := s.runChecks(ctx, exp); err == nil {
		t.Error("runChecks(network-config) error = nil, want an error")
	}

	net := func(host string, outcomes ...string) hostResult {
		checks := make([]string, 0, len(outcomes))
		for _, o := range outcomes {
			checks = append(checks, "network map[host:"+host+"] = "+o)
		}

		return hostResult{Checks: sorted(checks...)}
	}

	var (
		gwSet   = "gateway " + gw + " configured"
		gwUp    = "gateway " + gw + " is up"
		noGW    = "error: retry time expired waiting for gateway to be set"
		noGWUp  = "error: retry time expired waiting for gateway to be up"
		ipSet   = func(ip string) string { return "IP " + ip + " configured" }
		noIPSet = "error: retry time expired waiting for IP to be set"
	)

	want := map[string]hostResult{
		"lnx-ok":   net("lnx-ok", ipSet("10.0.0.1"), gwSet, gwUp),
		"lnx-nogw": net("lnx-nogw", ipSet("10.0.0.2"), noGW),
		"lnx-down": net("lnx-down", ipSet("10.0.0.3"), gwSet, noGWUp),
		"win-ok":   net("win-ok", ipSet("10.0.0.11"), gwSet, gwUp),
		"win-noip": net("win-noip", noIPSet),
		"win-nogw": net("win-nogw", ipSet("10.0.0.13"), noGW),
		"win-down": net("win-down", ipSet("10.0.0.14"), gwSet, noGWUp),
	}
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("runChecks(network-config) stored results mismatch (-want +got):\n%s", diff)
	}
}

// Each check records an unsafe argument, a response that never passes and a
// response that passes after retries, and the checks of an SoH profile that
// does not decode are left out.
func TestChecksReportEachOutcome(t *testing.T) { //nolint:paralleltest // replaces package globals
	testenv.RequireCommands(t, "bash")
	useSOHFilesDir(t)

	exp := sohExperiment(t, map[string]any{
		"c2Timeout":                 "1m",
		"hostsToUseUUIDForC2Active": "win",
		"hostFiles":                 map[string]any{"web": []any{"$HOME/x"}},
		"hostFilesAbsent":           map[string]any{"web": []any{"$y", "/etc/passwd"}},
		"hostServices":              map[string]any{"web": []any{"$s"}},
		"dockerContainers":          map[string]any{"web": []any{"$c", "boot", "old"}},
		"hostListeners":             map[string]any{"win": []any{":3389", ":445"}},
		"hostCustomTests": map[string]any{"web": []any{
			map[string]any{"name": "stderr", "testScript": "x", "testStderr": "warn"},
			map[string]any{"name": "stdout-miss", "testScript": "x", "testStdout": "nope"},
			map[string]any{"name": "stderr-miss", "testScript": "x", "testStderr": "nope"},
			map[string]any{"name": "validated", "testScript": "x", "validateStdout": "grep -q warn"},
			map[string]any{"name": "invalid", "testScript": "x", "validateStderr": "grep -q nope"},
			map[string]any{"name": "valid-stderr", "testScript": "x", "validateStderr": "grep -q warn"},
			map[string]any{"name": "invalid-stdout", "testScript": "x", "validateStdout": "grep -q nope"},
		}},
	}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
		sohNode("win", "windows", sohIface("eth0", "A", "static", "10.0.0.2", "")),
	},
		&v2.ScenarioApp{NameF: "bad-app", HostsF: []*v2.ScenarioAppHost{
			{HostnameF: "web", MetadataF: map[string]any{"sohProfile": "not a profile"}},
		}},
		&v2.ScenarioApp{NameF: "win-app", HostsF: []*v2.ScenarioAppHost{
			{HostnameF: "win", MetadataF: map[string]any{"sohProfile": map[string]any{
				"customTests": []any{map[string]any{"name": "t", "testScript": "x", "testStdout": "ok"}},
			}}},
		}},
	)

	winPort := func(port string) string {
		return "exp/win: powershell -NoProfile -Command '@(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue; " +
			"Get-NetUDPEndpoint -ErrorAction SilentlyContinue) | Where-Object LocalPort -eq " + port + " | Select-Object -First 1' (uuid)"
	}

	answers := map[string]string{
		"exp/web: stat -c present -- /etc/passwd":              "present",
		"exp/web: docker inspect " + dockerFormat + " -- boot": "starting",
		"exp/web: docker inspect " + dockerFormat + " -- old":  "exited",
		winPort("3389"): "",
		winPort("445"):  "\nTCP 0.0.0.0:445 Listen\n",
		"exp/web: send exp/web-stderr; bash /tmp/miniccc/files/exp/web-stderr":                                                     "warn",
		"exp/web: send exp/web-stdout-miss; bash /tmp/miniccc/files/exp/web-stdout-miss":                                           "warn",
		"exp/web: send exp/web-stderr-miss; bash /tmp/miniccc/files/exp/web-stderr-miss":                                           "warn",
		"exp/web: send exp/web-validated; bash /tmp/miniccc/files/exp/web-validated":                                               "warn",
		"exp/web: send exp/web-invalid; bash /tmp/miniccc/files/exp/web-invalid":                                                   "warn",
		"exp/web: send exp/web-valid-stderr; bash /tmp/miniccc/files/exp/web-valid-stderr":                                         "warn",
		"exp/web: send exp/web-invalid-stdout; bash /tmp/miniccc/files/exp/web-invalid-stdout":                                     "warn",
		"exp/win: send exp/win-t.ps1; powershell -NoProfile -ExecutionPolicy bypass -File /tmp/miniccc/files/exp/win-t.ps1 (uuid)": "ok",
	}

	fake := useSOHMM(t, answerFrom(answers))

	s := newSOH(withRetryDelay(time.Millisecond))
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	s.apps = exp.Spec.Scenario().Apps()

	checks := []string{"files", "files-absent", "services", "processes", "ports", "docker", "custom"}
	if err := s.runChecks(app.SetContextMetadata(t.Context(), map[string]any{"checks": checks}), exp); err == nil {
		t.Error("runChecks(each outcome) error = nil, want an error")
	}

	retried := []string{
		"exp/web: stat -c present -- /etc/passwd",
		"exp/web: docker inspect " + dockerFormat + " -- boot",
		"exp/web: docker inspect " + dockerFormat + " -- old",
		winPort("3389"),
	}
	checkSent(t, "runChecks(each outcome)", fake, keysWithout(answers, retried...), retried)

	unsafe := "error: " + errUnsafeC2Arg.Error() + `: minimega expands the $ in "%s" as an environment variable`

	want := map[string]hostResult{
		"web": {Checks: sorted(
			"file map[host:web path:$HOME/x] = "+fmt.Sprintf(unsafe, "$HOME/x"),
			"file absent map[host:web path:$y] = "+fmt.Sprintf(unsafe, "$y"),
			"file absent map[host:web path:/etc/passwd] = error: file exists",
			"service map[host:web service:$s] = "+fmt.Sprintf(unsafe, "$s"),
			"container map[container:$c host:web] = "+fmt.Sprintf(unsafe, "$c"),
			"container map[container:boot host:web] = error: container not up",
			"container map[container:old host:web] = error: container not up (state: exited)",
			"custom test map[host:web test:stderr] = STDERR contained warn",
			"custom test map[host:web test:stdout-miss] = error: script STDOUT did not contain test output",
			"custom test map[host:web test:stderr-miss] = error: script STDERR did not contain test output",
			"custom test map[host:web test:validated] = STDOUT validated",
			"custom test map[host:web test:invalid] = error: script STDERR was not valid",
			"custom test map[host:web test:valid-stderr] = STDERR validated",
			"custom test map[host:web test:invalid-stdout] = error: script STDOUT was not valid",
		)},
		"win": {Checks: sorted(
			"listener map[host:win port::3389] = error: not listening on port",
			"listener map[host:win port::445] = listening on port",
			"custom test map[host:win test:t] = STDOUT contained ok",
		)},
	}
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("runChecks(each outcome) stored results mismatch (-want +got):\n%s", diff)
	}
}

// The CPU load check records a Windows host that gives no load and a host
// whose response does not come.
func TestCPULoadErrors(t *testing.T) { //nolint:paralleltest // replaces package globals
	errWait := errors.New("wait failed")

	tests := map[string]struct {
		osType  string
		answer  string
		waitErr error
		want    string
	}{
		"Windows without a load": {osType: "windows", want: "no response for command '" + windowsCPULoad + "'"},
		"no response": {
			osType: "linux", waitErr: errWait,
			want: "getting response for command 'cat /proc/loadavg': " + errWait.Error(),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			exp := sohExperiment(t, map[string]any{}, []*v1.Node{sohNode("web", tt.osType)})
			fake := useSOHMM(t, func(c2Call) (string, error) { return tt.answer, nil })
			fake.waitErr = tt.waitErr

			s := newSOH()
			if err := s.decodeMetadata(exp); err != nil {
				t.Fatalf("decodeMetadata(exp) error = %v", err)
			}

			ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"cpu-load"}})
			if err := s.runChecks(ctx, exp); err == nil {
				t.Error("runChecks(cpu-load) error = nil, want an error")
			}

			if diff := cmp.Diff(map[string]hostResult{"web": {CPULoad: tt.want}}, storedResults(t)); diff != "" {
				t.Errorf("runChecks(cpu-load) stored results mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The CPU load and flows checks wait for their responses on the VM's host,
// with the C2 timeout and graces of the run.
func TestC2WaitsUseTheRunOptions(t *testing.T) { //nolint:paralleltest // replaces package globals
	elastic := sohNode("elastic", "linux")
	elastic.LabelsF = map[string]string{"soh-elastic-server": "true"}

	exp := sohExperiment(t, map[string]any{
		"c2Timeout": "11s", "c2AppearGrace": "7s", "c2ClientGrace": "9s",
	}, []*v1.Node{sohNode("web", "linux"), elastic})

	fake := useSOHMM(t, func(c c2Call) (string, error) {
		if c.Command == "query-flows.sh" {
			return flowsResponse(), nil
		}

		return "0.50 0.40 0.30 1/2 3", nil
	})

	s := newSOH()
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"cpu-load", "flows"}})
	if err := s.runChecks(ctx, exp); err != nil {
		t.Fatalf("runChecks(cpu-load, flows) error = %v", err)
	}

	wait := func(vm string) c2Wait {
		return c2Wait{VM: vm, Timeout: 11 * time.Second, AppearGrace: 7 * time.Second, ClientGrace: 9 * time.Second}
	}

	want := []c2Wait{wait("elastic"), wait("elastic"), wait("web")}
	sortWaits := cmpopts.SortSlices(func(a, b c2Wait) bool { return a.VM < b.VM })

	if diff := cmp.Diff(want, fake.waits, sortWaits); diff != "" {
		t.Errorf("runChecks(cpu-load, flows) C2 waits mismatch (-want +got):\n%s", diff)
	}
}

// runChecks stops, with the context's error, once the context ends during any
// of its checks.
func TestRunChecksStopsAfterEachCheck(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := map[string]struct {
		checks []string
		prefix string // the command during which the context ends
	}{
		"reachability": {checks: []string{"network-config", "reachability"}, prefix: "ping"},
		"files":        {checks: []string{"files"}, prefix: "stat"},
		"absent files": {checks: []string{"files-absent"}, prefix: "stat"},
		"services":     {checks: []string{"services"}, prefix: "systemctl"},
		"processes":    {checks: []string{"processes"}, prefix: "pgrep"},
		"listeners":    {checks: []string{"ports"}, prefix: "ss"},
		"containers":   {checks: []string{"docker"}, prefix: "docker"},
		"custom tests": {checks: []string{"custom"}, prefix: "bash"},
		"CPU load":     {checks: []string{"cpu-load"}, prefix: "cat"},
		"flows":        {checks: []string{"flows"}, prefix: "query-flows.sh"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useSOHFilesDir(t)

			elastic := sohNode("elastic", "linux")
			elastic.LabelsF = map[string]string{"soh-elastic-server": "true"}

			exp := sohExperiment(t, map[string]any{
				"testReachability": "full",
				"hostFiles":        map[string]any{"web": []any{"/etc/motd"}},
				"hostFilesAbsent":  map[string]any{"web": []any{"/tmp/gone"}},
				"hostServices":     map[string]any{"web": []any{"sshd"}},
				"hostProcesses":    map[string]any{"web": []any{"sshd"}},
				"hostListeners":    map[string]any{"web": []any{":22"}},
				"dockerContainers": map[string]any{"web": []any{"nginx"}},
				"hostCustomTests": map[string]any{"web": []any{
					map[string]any{"name": "t", "testScript": "x", "testStdout": "ok"},
				}},
			}, []*v1.Node{sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")), elastic})

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			useSOHMM(t, func(c c2Call) (string, error) {
				if strings.HasPrefix(c.Command, tt.prefix) || strings.Contains(c.Command, " "+tt.prefix+" ") {
					cancel()
				}

				if c.String() == "exp/web: ip addr" {
					return "inet 10.0.0.1/24", nil
				}

				return "", nil
			})

			s := newSOH(withRetryDelay(time.Millisecond))
			if err := s.decodeMetadata(exp); err != nil {
				t.Fatalf("decodeMetadata(exp) error = %v", err)
			}

			ctx = app.SetContextMetadata(ctx, map[string]any{"checks": tt.checks})
			if err := s.runChecks(ctx, exp); !errors.Is(err, context.Canceled) {
				t.Errorf("runChecks(%q, context ends) error = %v, want %v", tt.checks, err, context.Canceled)
			}
		})
	}
}

// Running needs soh metadata that decodes.
func TestRunningNeedsMetadata(t *testing.T) { //nolint:paralleltest // replaces package globals
	nodes := []*v1.Node{sohNode("web", "linux")}

	for name, md := range map[string]map[string]any{
		"no metadata":        nil,
		"metadata of a type": {"hostFiles": "web"},
		"bad C2 timeout":     {"c2Timeout": "soon"},
	} {
		exp := sohExperiment(t, md, nodes)
		fake := useSOHMM(t, nil)

		if err := newSOH().Running(t.Context(), exp); err == nil {
			t.Errorf("Running(%s) error = nil, want an error", name)
		}

		if got := fake.commands(); len(got) != 0 {
			t.Errorf("Running(%s) C2 commands = %q, want none", name, got)
		}
	}
}

// A custom test whose script soh cannot write gives an error and sends nothing.
func TestCustomTestScriptErrors(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := map[string]struct {
		ns    string
		block func(images string) error // stops soh from writing the script
	}{
		"namespace folder is a file": {
			ns:    "blocked",
			block: func(images string) error { return os.WriteFile(filepath.Join(images, "blocked"), nil, 0o600) },
		},
		"script is a folder": {
			ns:    "exp",
			block: func(images string) error { return os.MkdirAll(filepath.Join(images, "exp", "web-t"), 0o750) },
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			base := useSOHFilesDir(t)
			if err := tt.block(filepath.Join(base, "images")); err != nil {
				t.Fatalf("blocking the script: %v", err)
			}

			fake := useSOHMM(t, func(c2Call) (string, error) { return "ok", nil })

			wg := new(mm.StateGroup)
			test := customHostTest{Name: "t", TestScript: "echo ok", TestStdout: "ok"}

			newSOH().customTest(t.Context(), wg, tt.ns, sohNode("web", "linux"), test)
			wg.Wait()

			var pathErr *fs.PathError
			if len(wg.States) != 1 || !errors.As(wg.States[0].Err, &pathErr) {
				t.Errorf("customTest(%s) results = %+v, want one file error", name, wg.States)
			}

			if got := fake.commands(); len(got) != 0 {
				t.Errorf("customTest(%s) C2 commands = %q, want none", name, got)
			}
		})
	}
}

// runValidator fails when it cannot write its script or find bash.
func TestRunValidatorErrors(t *testing.T) { //nolint:paralleltest // changes the environment
	testenv.RequireCommands(t, "bash")

	if err := runValidator(t.Context(), "exit 0", "", "STDOUT"); err != nil {
		t.Fatalf("runValidator(exit 0) error = %v", err)
	}

	t.Run("no temporary folder", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))

		if err := runValidator(t.Context(), "exit 0", "", "STDOUT"); err == nil {
			t.Error("runValidator(no temporary folder) error = nil, want an error")
		}
	})

	t.Run("no bash", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		if err := runValidator(t.Context(), "exit 0", "", "STDOUT"); err == nil {
			t.Error("runValidator(no bash) error = nil, want an error")
		}
	})
}

// chanWriter sends each write to its channel, and drops it when the channel is full.
type chanWriter chan string

func (c chanWriter) Write(p []byte) (int, error) {
	select {
	case c <- string(p):
	default:
	}

	return len(p), nil
}

// periodicallyNotify logs its message at debug level each interval until it is canceled.
func TestPeriodicallyNotify(t *testing.T) {
	t.Parallel()

	logs := make(chanWriter, 1)
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cancel := periodicallyNotify(t.Context(), logger, "still waiting", time.Millisecond)
	defer cancel()

	select {
	case got := <-logs:
		if !strings.Contains(got, `msg="still waiting"`) {
			t.Errorf("periodicallyNotify(still waiting) logged %q, want the message", got)
		}
	case <-time.After(10 * time.Second):
		t.Error("periodicallyNotify(still waiting) logged nothing")
	}
}
