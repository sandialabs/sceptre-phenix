package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"go.etcd.io/bbolt"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

// storedInput is an input with a store file that holds configs, by kind, and with no PATH programs but vim. It
// counts the reads of the store in reads.
func storedInput(t *testing.T, configs map[string]store.Configs, err error) (*doctorInput, string, *int) {
	t.Helper()

	dir := newDoctorTestDir(t)
	in := fakeDoctorInput(dir, nil)
	path := strings.TrimPrefix(in.text("store.endpoint"), "bolt://")
	writeSized(t, path, 1)

	reads := new(int)
	in.storeConfigs = func(_ context.Context, p, kind string, _ time.Duration) (store.Configs, error) {
		*reads++

		if p != path {
			return nil, fmt.Errorf("storeConfigs(%s)", p)
		}

		return configs[kind], err
	}
	in.found = &doctorFound{}
	in.env.lookPath = lookPathIn(nil)

	return in, dir, reads
}

// experimentConfig is the stored config of the experiment name, with baseDir, nodes and scenario apps.
func experimentConfig(t *testing.T, name, baseDir string, nodes []*v1.Node, apps []*v2.ScenarioApp) store.Config {
	t.Helper()

	exp := types.NewExperiment(store.ConfigMetadata{Name: name})
	exp.Spec.SetExperimentName(name)
	exp.Spec.SetBaseDir(baseDir)
	exp.Spec.SetTopology(&v1.TopologySpec{NodesF: nodes})
	exp.Spec.SetScenario(&v2.ScenarioSpec{AppsF: apps})

	return store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Experiment", Metadata: store.ConfigMetadata{Name: name},
		Spec:   structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status: structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	}
}

// specConfig is a stored config of kind and name at version, with spec.
func specConfig(version, kind, name string, spec map[string]any) store.Config {
	return store.Config{
		Version: "phenix.sandia.gov/" + version, Kind: kind, Metadata: store.ConfigMetadata{Name: name}, Spec: spec,
	}
}

// snapshotNode is a booted node with snapshot on, its first drive and injects with each source in srcs.
func snapshotNode(name string, srcs ...string) *v1.Node {
	yes := true
	node := &v1.Node{
		GeneralF:  &v1.General{HostnameF: name, SnapshotF: &yes},
		HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{{ImageF: name + ".qc2"}}},
	}

	for _, src := range srcs {
		node.InjectionsF = append(node.InjectionsF, &v1.Injection{SrcF: src, DstF: "/" + filepath.Base(src)})
	}

	return node
}

// scenario-apps warns about each app and SCORCH component type that a stored scenario or experiment names, that is
// not built in, and that has no program on PATH, with the configs that name it.
func TestScenarioAppsCheck(t *testing.T) {
	t.Parallel()

	scorch := &v2.ScenarioApp{NameF: "scorch", MetadataF: map[string]any{"components": []any{
		map[string]any{"name": "pause"}, map[string]any{"name": "c1", "type": "custom"},
		map[string]any{"name": "nope"},
	}}}

	scenario := specConfig("v2", "Scenario", "s1", map[string]any{"apps": []any{
		map[string]any{"name": "ntp"}, map[string]any{"name": "missing"}, map[string]any{"name": "mine"},
	}})

	tests := map[string]struct {
		found   map[string]string
		err     error
		want    doctorCheck
		wantHnt string
	}{
		"all found": {
			found: map[string]string{
				"phenix-app-mine": "/usr/bin/phenix-app-mine", "phenix-app-missing": "/usr/bin/phenix-app-missing",
				"phenix-scorch-component-custom": "/x", "phenix-scorch-component-nope": "/y",
			},
			want: row("", doctorPass, "the stored scenarios and experiments name 5 apps and 3 SCORCH component "+
				"types, and each is built in or on PATH"),
		},
		"two apps missing": {
			found: map[string]string{"phenix-scorch-component-custom": "/x", "phenix-scorch-component-nope": "/y"},
			want: row("", doctorWarn, "these apps are not built in and are not on PATH, so phenix cannot run them: "+
				"phenix-app-mine (scenario s1), phenix-app-missing (experiment e1, scenario s1)"),
			wantHnt: "install the app, or add its directory to PATH for the user that runs phenix. phenix runs " +
				"phenix-app-<app> and phenix-scorch-component-<type>",
		},
		"missing": {
			found: map[string]string{"phenix-app-mine": "/usr/bin/phenix-app-mine", "phenix-scorch-component-custom": "/x"},
			want: row("", doctorWarn, "this app is not built in and is not on PATH, so phenix cannot run it: "+
				"phenix-app-missing (experiment e1, scenario s1). This SCORCH component is not built in and is not "+
				"on PATH, so SCORCH cannot run it: phenix-scorch-component-nope (experiment e1)"),
			wantHnt: "install the app, or add its directory to PATH for the user that runs phenix. phenix runs " +
				"phenix-app-<app> and phenix-scorch-component-<type>",
		},
		"store locked": {
			err:  bbolt.ErrTimeout,
			want: row("", doctorSkip, "not checked, because another process held the lock of STORE for 1s"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			exp := experimentConfig(t, "e1", "/phenix/experiments/e1", []*v1.Node{snapshotNode("a")},
				[]*v2.ScenarioApp{{NameF: "missing"}, {NameF: "soh"}, scorch})

			in, _, reads := storedInput(t, map[string]store.Configs{
				"Scenario": {scenario}, "Experiment": {exp},
			}, tc.err)
			in.env.lookPath = lookPathIn(tc.found)
			path := strings.TrimPrefix(in.text("store.endpoint"), "bolt://")

			got := scenarioAppsCheck(t.Context(), in)
			got.Detail = strings.ReplaceAll(got.Detail, path, "STORE")

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("scenarioAppsCheck() mismatch (-want +got):\n%s", diff)
			}

			if hint := doctorHintOf(got, scenarioAppsHint); hint != tc.wantHnt {
				t.Errorf("scenarioAppsHint() = %q, want %q", hint, tc.wantHnt)
			}

			// a lock that stops one read stops the reads of the other kinds without a wait
			if tc.err != nil && *reads != 1 {
				t.Errorf("storeConfigs ran %d times, want once", *reads)
			}
		})
	}
}

// Each kind is read from the store once in a run, and a read that the lock or the context stops gives its reason to
// the later reads of other kinds, so a running phenix ui costs at most one wait. Other errors stay with their kind.
func TestStoredConfigsOncePerRun(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err       error
		wantReads int
		wantWhy   string
	}{
		"read":          {wantReads: 2},
		"locked":        {err: bbolt.ErrTimeout, wantReads: 1, wantWhy: "another process held the lock of STORE for 1s"},
		"context ended": {err: context.DeadlineExceeded, wantReads: 1, wantWhy: "cannot read STORE: context deadline exceeded"},
		"broken":        {err: errors.New("invalid database"), wantReads: 2, wantWhy: "cannot read STORE: invalid database"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in, _, reads := storedInput(t, nil, tc.err)
			path := strings.TrimPrefix(in.text("store.endpoint"), "bolt://")

			for range 2 {
				for _, kind := range []string{"Image", "User"} {
					_, _, why := in.storedConfigs(t.Context(), kind)
					if why = strings.ReplaceAll(why, path, "STORE"); why != tc.wantWhy {
						t.Errorf("storedConfigs(%s) why = %q, want %q", kind, why, tc.wantWhy)
					}
				}
			}

			if *reads != tc.wantReads {
				t.Errorf("storeConfigs ran %d times, want %d", *reads, tc.wantReads)
			}
		})
	}
}

// inject-sources warns about each inject source that does not exist as phenix resolves it, and leaves out the
// files that apps write under the experiment's base directory at the start and the relative sources of topologies.
func TestInjectSourcesCheck(t *testing.T) {
	t.Parallel()

	no, zero := false, 0

	in, dir, _ := storedInput(t, nil, nil)
	base := filepath.Join(dir, "experiments", "e1")
	writeSized(t, filepath.Join(base, "rel.txt"), 1)
	writeSized(t, filepath.Join(dir, "injects", "here.txt"), 1)

	noSnapshot := snapshotNode("ns", "/nowhere/ns.txt")
	noSnapshot.GeneralF.SnapshotF = &no
	noPartition := snapshotNode("np", "/nowhere/np.txt")
	noPartition.HardwareF.DrivesF[0].InjectPartitionF = &zero

	exp := experimentConfig(t, "e1", base, []*v1.Node{
		snapshotNode("a", filepath.Join(dir, "injects", "here.txt"), filepath.Join(dir, "injects", "gone.txt")),
		snapshotNode("b", "rel.txt", "gone.txt", filepath.Join(base, "startup", "b.sh")),
		noSnapshot, noPartition,
	}, nil)

	topology := specConfig("v1", "Topology", "t1", map[string]any{"nodes": []any{
		map[string]any{"general": map[string]any{"hostname": "t"}, "injections": []any{
			map[string]any{"src": "/nowhere/t.txt", "dst": "/t.txt"}, map[string]any{"src": "relative.txt", "dst": "/r"},
		}},
		map[string]any{"external": true, "general": map[string]any{"hostname": "x"}, "injections": []any{
			map[string]any{"src": "/nowhere/x.txt", "dst": "/x.txt"},
		}},
	}})

	in.storeConfigs = func(_ context.Context, _, kind string, _ time.Duration) (store.Configs, error) {
		return map[string]store.Configs{"Experiment": {exp}, "Topology": {topology}}[kind], nil
	}

	got := injectSourcesCheck(t.Context(), in)
	got.Detail = strings.ReplaceAll(got.Detail, dir, "DIR")

	want := row("", doctorWarn, "3 inject sources of 5 do not exist here, so minimega disk inject cannot copy them "+
		"at the start: experiment e1 node a: DIR/injects/gone.txt, experiment e1 node b: DIR/experiments/e1/gone.txt, "+
		"topology t1 node t: /nowhere/t.txt")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("injectSourcesCheck() mismatch (-want +got):\n%s", diff)
	}

	wantHint := "put the file at the path, or change the injection src in the topology or experiment. In a container, " +
		"mount the same path in the containers of phenix and minimega"
	if hint := doctorHintOf(got, injectSourcesHint); hint != wantHint {
		t.Errorf("injectSourcesHint() = %q, want %q", hint, wantHint)
	}

	one, _, _ := storedInput(t, map[string]store.Configs{"Experiment": {
		experimentConfig(t, "e2", base, []*v1.Node{snapshotNode("c", filepath.Join(dir, "injects", "gone.txt"))}, nil),
	}}, nil)

	got = injectSourcesCheck(t.Context(), one)
	got.Detail = strings.ReplaceAll(got.Detail, dir, "DIR")

	want = row("", doctorWarn, "1 inject source of 1 does not exist here, so minimega disk inject cannot copy it "+
		"at the start: experiment e2 node c: DIR/injects/gone.txt")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("injectSourcesCheck() with one source mismatch (-want +got):\n%s", diff)
	}

	none, _, _ := storedInput(t, map[string]store.Configs{}, nil)
	if got := injectSourcesCheck(
		t.Context(),
		none,
	); got != row(
		"",
		doctorPass,
		"no stored experiment or topology has an inject source to check",
	) {
		t.Errorf("injectSourcesCheck() with no configs = %v, want a pass", got)
	}
}

// image-configs warns about each overlay directory that does not exist, and names the script files that are not
// on disk in the detail without a warning, because the build uses their stored copies.
func TestImageConfigsCheck(t *testing.T) {
	t.Parallel()

	image := func(name string, overlays []any, scripts map[string]any) store.Config {
		return specConfig("v1", "Image", name, map[string]any{"release": "noble", "overlays": overlays, "scripts": scripts})
	}

	tests := map[string]struct {
		configs func(dir string) store.Configs
		want    doctorCheck
	}{
		"all found": {
			configs: func(dir string) store.Configs {
				return store.Configs{image("a", []any{filepath.Join(dir, "overlay")}, map[string]any{
					"POSTBUILD_APT_CLEANUP": "apt clean", filepath.Join(dir, "script.sh"): "echo", "https://x/y.sh": "echo",
				})}
			},
			want: row("", doctorPass, "the overlays and scripts of the 1 stored image config exist"),
		},
		"scripts gone": {
			configs: func(dir string) store.Configs {
				return store.Configs{image("a", []any{filepath.Join(dir, "overlay")}, map[string]any{"/gone/a.sh": "echo"})}
			},
			want: row("", doctorPass, "1 script file is not on disk, so phenix image update keeps its stored "+
				"copy: a: /gone/a.sh"),
		},
		"one overlay gone": {
			configs: func(string) store.Configs {
				return store.Configs{image("a", []any{"/gone/overlay"}, map[string]any{"/gone/a.sh": "echo", "/gone/b.sh": "echo"})}
			},
			want: row("", doctorWarn, "1 overlay directory does not exist, so phenix image build fails: a: /gone/overlay. "+
				"2 script files are not on disk, so phenix image update keeps their stored copies: a: /gone/a.sh, a: /gone/b.sh"),
		},
		"overlays gone": {
			configs: func(dir string) store.Configs {
				return store.Configs{
					image("a", []any{"/gone/overlay", filepath.Join(dir, "file")}, map[string]any{"/gone/b.sh": "echo"}),
					image("b", []any{filepath.Join(dir, "overlay"), "relative/overlay"}, nil),
				}
			},
			want: row("", doctorWarn, "3 overlay directories do not exist, so phenix image build fails: "+
				"a: /gone/overlay, a: DIR/file (not a directory), b: relative/overlay (under /home/alice). 1 script file "+
				"is not on disk, so phenix image update keeps its stored copy: a: /gone/b.sh"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in, dir, _ := storedInput(t, nil, nil)
			if err := os.MkdirAll(filepath.Join(dir, "overlay"), 0o750); err != nil {
				t.Fatalf("os.MkdirAll() error = %v", err)
			}

			writeSized(t, filepath.Join(dir, "script.sh"), 1)
			writeSized(t, filepath.Join(dir, "file"), 1)

			configs := tc.configs(dir)
			in.storeConfigs = func(context.Context, string, string, time.Duration) (store.Configs, error) {
				return configs, nil
			}

			got := imageConfigsCheck(t.Context(), in)
			got.Detail = strings.ReplaceAll(got.Detail, dir, "DIR")

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("imageConfigsCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// scenario-hosts warns about each host that a scenario app of a stored experiment names but that is not a node.
func TestScenarioHostsCheck(t *testing.T) {
	t.Parallel()

	exp := experimentConfig(t, "e1", "/phenix/experiments/e1", []*v1.Node{snapshotNode("a"), snapshotNode("b")},
		[]*v2.ScenarioApp{
			{NameF: "ntp", HostsF: []*v2.ScenarioAppHost{{HostnameF: "a"}, {HostnameF: "A"}}},
			{NameF: "vrouter", HostsF: []*v2.ScenarioAppHost{{HostnameF: "b"}}},
		})

	in, _, _ := storedInput(t, map[string]store.Configs{"Experiment": {exp}}, nil)

	got := scenarioHostsCheck(t.Context(), in)
	want := row("", doctorWarn, "apps name hosts that are not nodes of the topology, so the apps skip them: "+
		"experiment e1 app ntp host A")

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("scenarioHostsCheck() mismatch (-want +got):\n%s", diff)
	}

	in, _, _ = storedInput(t, map[string]store.Configs{"Experiment": {
		experimentConfig(t, "e2", "/x", []*v1.Node{snapshotNode("a")}, []*v2.ScenarioApp{
			{NameF: "ntp", HostsF: []*v2.ScenarioAppHost{{HostnameF: "a"}}},
		}),
	}}, nil)

	if got := scenarioHostsCheck(t.Context(), in); got.Status != doctorPass {
		t.Errorf("scenarioHostsCheck() = %v, want a pass", got)
	}
}

// rbac-users runs only with sign-in on. It warns about a stored user whose role is not stored or built in, and about
// a literal resource name of a role that names an experiment or a user that the store does not have. It never shows
// a password, and gives the user names it shows to --report.
func TestRBACUsersCheck(t *testing.T) {
	t.Parallel()

	const hash = "$2a$10$secrethashsecrethashsecrethashsecret"

	policy := func(resources, names []string) map[string]any {
		return map[string]any{"resources": resources, "resourceNames": names, "verbs": []string{"get"}}
	}

	user := func(name, role string, policies ...any) store.Config {
		return specConfig("v1", "User", name, map[string]any{
			"username": name, "password": hash, "tokens": map[string]any{"tok": "note"},
			"rbac": map[string]any{"roleName": role, "policies": policies},
		})
	}

	exp := experimentConfig(t, "e1", "/x", []*v1.Node{snapshotNode("a")}, nil)
	custom := specConfig("v1", "Role", "lab-viewer", map[string]any{"roleName": "Lab Viewer", "policies": []any{
		policy([]string{"experiments", "experiments/*"}, []string{"e1", "e9", "lab-*", "!e8"}),
		policy([]string{"vms"}, []string{"e1/a", "e7/a"}),
		policy([]string{"disks"}, []string{"gone.qc2"}),
		policy([]string{"users"}, []string{"alice@x.org", "bob@x.org"}),
	}})

	tests := map[string]struct {
		off   bool // sign-in off
		users store.Configs
		want  doctorCheck
		hint  string
	}{
		"sign-in off": {
			off:  true,
			want: row("", doctorPass, "unused, because ui.jwt-signing-key is empty, so phenix ui does not check roles"),
		},
		"names of deleted configs": {
			users: store.Configs{
				user("alice@x.org", "Global Admin"), user("carol@x.org", "lab-viewer"),
				specConfig("v1", "User", "dave@x.org", map[string]any{"username": "dave@x.org"}),
			},
			want: row("", doctorWarn, "role lab-viewer names e9, which the store does not have, role lab-viewer names "+
				"e7/a, which the store does not have, role lab-viewer names bob@x.org, which the store does not have"),
			hint: "remove the names of deleted experiments and users from the role on the Users page, or with " +
				"phenix config edit user/<user> or phenix config edit role/<role>",
		},
		"unknown role": {
			users: store.Configs{
				user("alice@x.org", "Gone Role", policy([]string{"experiments"}, []string{"e1", "e5"})),
				user("bob@x.org", "Lab Viewer"),
			},
			want: row("", doctorWarn, `the role "Gone Role" of user alice@x.org is not in the store and is not `+
				"built in, role lab-viewer names e9, which the store does not have, role lab-viewer names e7/a, which "+
				"the store does not have, the role of user alice@x.org names e5, which the store does not have"),
			hint: "give the user a role on the Users page, or fix its rbac.roleName with phenix config edit " +
				"user/<user>. Remove the names of deleted experiments and users from the role in the same way",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in, _, _ := storedInput(t, map[string]store.Configs{
				"User": tc.users, "Role": {custom}, "Experiment": {exp},
			}, nil)
			if tc.off {
				in.settings["ui.jwt-signing-key"] = resolvedSetting{value: "", text: "", source: sourceDefault}
			}

			got := rbacUsersCheck(t.Context(), in)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("rbacUsersCheck() mismatch (-want +got):\n%s", diff)
			}

			if strings.Contains(got.Detail, hash) || strings.Contains(got.Detail, "tok") {
				t.Errorf("rbacUsersCheck() detail %q shows a password hash or a token", got.Detail)
			}

			if hint := doctorHintOf(got, rbacUsersHint); hint != tc.hint {
				t.Errorf("rbacUsersHint() = %q, want %q", hint, tc.hint)
			}

			for _, u := range tc.users {
				if !strings.Contains(strings.Join(in.found.users, " "), u.Metadata.Name) &&
					u.Spec["rbac"] != nil {
					t.Errorf("found users %v lack %s, which --report must redact", in.found.users, u.Metadata.Name)
				}
			}
		})
	}
}

// A stored user name that rbac-users shows is replaced in --report.
func TestDoctorReportRedactsStoredUsers(t *testing.T) {
	t.Parallel()

	in := fakeDoctorInput(newDoctorTestDir(t), nil)
	in.found = &doctorFound{users: []string{"labuser7"}}

	r := newDoctorRedactor(in)
	if got := r.redact("the role of user labuser7 names e5"); strings.Contains(got, "labuser7") {
		t.Errorf("redact() = %q, want labuser7 replaced", got)
	}
}
