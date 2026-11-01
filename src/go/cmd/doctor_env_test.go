package cmd

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/sys/unix"
	"pgregory.net/rapid"
)

// fakeDoctorEnv is a doctorEnv holding only vars, with vim on PATH, the user
// alice, the groups 100 (which it is in) and 200, the built-in roles, the real
// file system and no /proc.
func fakeDoctorEnv(vars map[string]string, root bool) doctorEnv {
	return doctorEnv{
		lookup: func(name string) (string, bool) {
			value, ok := vars[name]

			return value, ok
		},
		environ: func() []string {
			entries := make([]string, 0, len(vars))
			for name, value := range vars {
				entries = append(entries, name+"="+value)
			}

			return entries
		},
		lookPath: func(name string) (string, error) {
			if name == "vim" {
				return "/usr/bin/vim", nil
			}

			return "", exec.ErrNotFound
		},
		lookupPort: doctorEnvLookupPort,
		lookupUser: func(name string) (*user.User, error) {
			if name == "alice" {
				return &user.User{Uid: "1000", Gid: "1000", Username: "alice", Name: "", HomeDir: "/home/alice"}, nil
			}

			return nil, user.UnknownUserError(name)
		},
		lookupGroup: func(id string) (*user.Group, error) {
			switch id {
			case "100":
				return &user.Group{Gid: id, Name: "users"}, nil
			case "200":
				return &user.Group{Gid: id, Name: "wheel"}, nil
			}

			return nil, user.UnknownGroupIdError(id)
		},
		lookupUID: func(id string) (*user.User, error) {
			switch id {
			case "0":
				return &user.User{Uid: id, Gid: id, Username: "root", Name: "", HomeDir: "/root"}, nil
			case "1000":
				return &user.User{Uid: id, Gid: id, Username: "alice", Name: "", HomeDir: "/home/alice"}, nil
			}

			return nil, user.UnknownUserIdError(0)
		},
		stat:     os.Stat,
		lstat:    os.Lstat,
		access:   unix.Access,
		readFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
		readAt:   osReadAt,
		readDir:  os.ReadDir,
		fsType:   func(string) (string, error) { return "ext4", nil },
		groups:   func() ([]int, error) { return []int{1000, 100}, nil },
		roles:    builtinRoles(),
		pageSize: 4096,
		root:     root,
	}
}

func row(name string, status doctorStatus, detail string) doctorCheck {
	return doctorCheck{Name: name, Status: status, Detail: detail}
}

// Each set variable phenix reads gets one row, checked as the code that reads
// it parses it; unset variables get none, and a misspelled PHENIX_ one warns.
//
//nolint:maintidx // one table
func TestEnvChecks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeTestFile(t, dir, "file.pem", "")
	missing := filepath.Join(dir, "missing")
	longSocket := filepath.Join(dir, strings.Repeat("s", 120)+".sock")

	tests := map[string]struct {
		vars    map[string]string
		root    bool
		sources map[string]string
		want    []doctorCheck
	}{
		"unset": {
			vars: map[string]string{"HOME": "/home/alice", "TERM": "xterm"},
			want: []doctorCheck{},
		},
		"bool": {
			vars: map[string]string{"PHENIX_USE_GRE_MESH": "TRUE", "PHENIX_UI_MINIMEGA_CONSOLE": "yes"},
			want: []doctorCheck{
				row("PHENIX_UI_MINIMEGA_CONSOLE", doctorFail, `"yes" is not true or false, so phenix reads it as false`),
				row("PHENIX_USE_GRE_MESH", doctorPass, "true"),
			},
		},
		"int": {
			vars: map[string]string{
				"PHENIX_LOG_SYSTEM_MAX_BACKUPS": "five",
				"PHENIX_LOG_SYSTEM_MAX_AGE":     "-3",
				"PHENIX_LOG_SYSTEM_MAX_SIZE":    "010",
			},
			want: []doctorCheck{
				row("PHENIX_LOG_SYSTEM_MAX_AGE", doctorWarn, "-3 is negative, which phenix treats as 0 (no limit)"),
				row("PHENIX_LOG_SYSTEM_MAX_BACKUPS", doctorWarn, `"five" is not a whole number, so phenix reads it as 0 (no limit)`),
				row("PHENIX_LOG_SYSTEM_MAX_SIZE", doctorWarn, `phenix reads "010" as 8`),
			},
		},
		"log size": {
			vars: map[string]string{"PHENIX_LOG_SYSTEM_MAX_SIZE": "-1", "PHENIX_LOG_SYSTEM_MAX_AGE": "0"},
			want: []doctorCheck{
				row("PHENIX_LOG_SYSTEM_MAX_AGE", doctorPass, "0 (no limit)"),
				row("PHENIX_LOG_SYSTEM_MAX_SIZE", doctorFail, "-1 is negative, so every write to the system log fails"),
			},
		},
		"misread log settings": {
			vars: map[string]string{"PHENIX_LOG_SYSTEM_MAX_SIZE": "big", "PHENIX_LOG_LEVEL": "loud"},
			want: []doctorCheck{
				row("PHENIX_LOG_LEVEL", doctorWarn,
					`"loud" is not a log level (debug, info, warn or error), so phenix ignores it and logs at info`),
				row("PHENIX_LOG_SYSTEM_MAX_SIZE", doctorWarn,
					`"big" is not a whole number, so phenix reads it as 0, and the system log rotates at 100 MB`),
			},
		},
		"enum": {
			vars: map[string]string{
				"PHENIX_BRIDGE_MODE":   "sideways",
				"PHENIX_DEPLOY_MODE":   "No-Headnode",
				"PHENIX_LOG_LEVEL":     "none",
				"PHENIX_UI_LOGS_LEVEL": "warning",
			},
			want: []doctorCheck{
				row("PHENIX_BRIDGE_MODE", doctorFail,
					"unknown bridge mode provided: sideways, so phenix commands refuse it (options: manual | auto)"),
				row("PHENIX_DEPLOY_MODE", doctorPass, "no-headnode"),
				row("PHENIX_LOG_LEVEL", doctorPass, "none, so phenix logs nothing"),
				row("PHENIX_UI_LOGS_LEVEL", doctorWarn,
					`"warning" is not a log level (debug, info, warn or error), so phenix ui uses info`),
			},
		},
		"none is not a ui log level": {
			vars: map[string]string{"PHENIX_UI_LOGS_LEVEL": "none", "PHENIX_LOG_LEVEL": "DEBUG"},
			want: []doctorCheck{
				row("PHENIX_LOG_LEVEL", doctorPass, "debug"),
				row("PHENIX_UI_LOGS_LEVEL", doctorWarn,
					`"none" is not a log level (debug, info, warn or error), so phenix ui uses info`),
			},
		},
		"duration": {
			vars: map[string]string{"PHENIX_UI_JWT_LIFETIME": "24"},
			want: []doctorCheck{row("PHENIX_UI_JWT_LIFETIME", doctorFail,
				`"24" has no unit, so phenix reads it as nanoseconds and tokens expire immediately. Add a unit, such as 24h`)},
		},
		"short duration": {
			vars: map[string]string{"PHENIX_UI_JWT_LIFETIME": "30s"},
			want: []doctorCheck{
				row("PHENIX_UI_JWT_LIFETIME", doctorFail, "30s is under a minute, so tokens expire before anyone can use them"),
			},
		},
		"bad duration": {
			vars: map[string]string{"PHENIX_UI_JWT_LIFETIME": "1d"},
			want: []doctorCheck{
				row("PHENIX_UI_JWT_LIFETIME", doctorFail, `"1d" is not a duration such as 24h, so tokens expire immediately`),
			},
		},
		"negative duration": {
			vars: map[string]string{"PHENIX_UI_JWT_LIFETIME": "-1h"},
			want: []doctorCheck{row("PHENIX_UI_JWT_LIFETIME", doctorFail, "-1h0m0s is not positive, so tokens expire immediately")},
		},
		"good duration": {
			vars: map[string]string{"PHENIX_UI_JWT_LIFETIME": "12h"},
			want: []doctorCheck{row("PHENIX_UI_JWT_LIFETIME", doctorPass, "12h0m0s")},
		},
		"size": {
			vars: map[string]string{"PHENIX_UI_NETFLOW_MAX_SIZE": "-5", "PHENIX_UI_SHUTDOWN_GRACE_PERIOD": "10"},
			want: []doctorCheck{
				row("PHENIX_UI_NETFLOW_MAX_SIZE", doctorFail, `invalid ui.netflow.max-size "-5": it must not be negative`),
				row("PHENIX_UI_SHUTDOWN_GRACE_PERIOD", doctorPass, "10s"),
			},
		},
		"grace period with a unit": {
			vars: map[string]string{"PHENIX_UI_SHUTDOWN_GRACE_PERIOD": "10s", "PHENIX_UI_NETFLOW_MAX_SIZE": "50"},
			want: []doctorCheck{
				row("PHENIX_UI_NETFLOW_MAX_SIZE", doctorPass, "50 MiB"),
				row("PHENIX_UI_SHUTDOWN_GRACE_PERIOD", doctorFail,
					`invalid ui.shutdown-grace-period "10s": it must be a whole number of seconds, such as 5`),
			},
		},
		"listen endpoint": {
			vars: map[string]string{"PHENIX_UI_LISTEN_ENDPOINT": "3080"},
			want: []doctorCheck{
				row("PHENIX_UI_LISTEN_ENDPOINT", doctorFail, `"3080" is not host:port, so phenix ui cannot listen on it`),
			},
		},
		"listen port out of range": {
			vars: map[string]string{"PHENIX_UI_LISTEN_ENDPOINT": "0.0.0.0:70000"},
			want: []doctorCheck{row("PHENIX_UI_LISTEN_ENDPOINT", doctorFail,
				"port 70000 is out of range 1-65535, so phenix ui cannot listen on it")},
		},
		"listen without a port": {
			vars: map[string]string{"PHENIX_UI_LISTEN_ENDPOINT": "127.0.0.1:"},
			want: []doctorCheck{
				row("PHENIX_UI_LISTEN_ENDPOINT", doctorWarn, "127.0.0.1: has no port, so phenix ui listens on a random one"),
			},
		},
		"listen on a port": {
			vars: map[string]string{"PHENIX_UI_LISTEN_ENDPOINT": ":3080"},
			want: []doctorCheck{row("PHENIX_UI_LISTEN_ENDPOINT", doctorPass, ":3080")},
		},
		"file server": {
			vars: map[string]string{"PHENIX_UI_FILE_SERVER_ENDPOINT": "8081"},
			want: []doctorCheck{row("PHENIX_UI_FILE_SERVER_ENDPOINT", doctorPass, net.JoinHostPort("127.0.0.1", "8081"))},
		},
		"file server off": {
			vars: map[string]string{"PHENIX_UI_FILE_SERVER_ENDPOINT": "0"},
			want: []doctorCheck{row("PHENIX_UI_FILE_SERVER_ENDPOINT", doctorPass, "0, so the file server is off")},
		},
		"bad file server": {
			vars: map[string]string{"PHENIX_UI_FILE_SERVER_ENDPOINT": "99999"},
			want: []doctorCheck{
				row("PHENIX_UI_FILE_SERVER_ENDPOINT", doctorFail, `phenix ui refuses "99999": expected port or host:port`),
			},
		},
		"base path": {
			vars: map[string]string{"PHENIX_UI_BASE_PATH": "phenix"},
			want: []doctorCheck{row("PHENIX_UI_BASE_PATH", doctorPass, "/phenix/")},
		},
		"root base path": {
			vars: map[string]string{"PHENIX_UI_BASE_PATH": "/"},
			want: []doctorCheck{row("PHENIX_UI_BASE_PATH", doctorPass, "/")},
		},
		"base path with doubled slashes": {
			vars: map[string]string{"PHENIX_UI_BASE_PATH": "//a//"},
			want: []doctorCheck{row("PHENIX_UI_BASE_PATH", doctorPass, "//a//")},
		},
		"base path URL": {
			vars: map[string]string{"PHENIX_UI_BASE_PATH": "https://example.com/phenix"},
			want: []doctorCheck{
				row("PHENIX_UI_BASE_PATH", doctorWarn, `"https://example.com/phenix" is a URL, but phenix ui uses it as a path`),
			},
		},
		"header": {
			vars: map[string]string{"PHENIX_UI_PROXY_AUTH_HEADER": "X Forwarded User"},
			want: []doctorCheck{row("PHENIX_UI_PROXY_AUTH_HEADER", doctorFail,
				`"X Forwarded User" is not an HTTP header name, so no request carries it`)},
		},
		"users": {
			vars: map[string]string{"PHENIX_UI_USERS": "admin:pw:global-admin bob:pw:experiment-viewer:exp1"},
			want: []doctorCheck{row("PHENIX_UI_USERS", doctorPass, "2 users")},
		},
		"user with a custom role": {
			vars: map[string]string{"PHENIX_UI_USERS": "admin:pw:global-admin bob:pw:viewer:exp1"},
			want: []doctorCheck{row("PHENIX_UI_USERS", doctorWarn,
				`user 2 ("bob") has a role that is not built in, so phenix ui skips the user unless the store holds the role`)},
		},
		"comma-separated users": {
			vars: map[string]string{"PHENIX_UI_USERS": "admin:pw:global-admin,bob:pw:viewer"},
			want: []doctorCheck{row("PHENIX_UI_USERS", doctorFail,
				`user 1 ("admin") has a comma in its role. phenix splits PHENIX_UI_USERS on spaces, so separate users with spaces`)},
		},
		"malformed user": {
			vars: map[string]string{"PHENIX_UI_USERS": "admin:pw:global-admin bob:pw"},
			want: []doctorCheck{row("PHENIX_UI_USERS", doctorFail,
				`default user 2 ("bob") is not name:password:role[:resource...], so phenix ui skips it`)},
		},
		"features": {
			vars: map[string]string{"PHENIX_UI_FEATURES": "vm-mount,webshark"},
			want: []doctorCheck{row("PHENIX_UI_FEATURES", doctorFail,
				`"vm-mount,webshark" holds a comma. phenix splits PHENIX_UI_FEATURES on spaces, so separate features with spaces`)},
		},
		"unknown feature": {
			vars: map[string]string{"PHENIX_UI_FEATURES": "vm-mount webshrak"},
			want: []doctorCheck{
				row("PHENIX_UI_FEATURES", doctorWarn, `unknown feature "webshrak" (did you mean webshark?)`),
			},
		},
		"directories": {
			vars: map[string]string{
				"PHENIX_BASE_DIR_PHENIX":     dir,
				"PHENIX_BASE_DIR_MINIMEGA":   missing,
				"PHENIX_BASE_DIR_TOPOLOGIES": file,
				"PHENIX_MOUNT_DIR":           ".",
			},
			want: []doctorCheck{
				row("PHENIX_BASE_DIR_MINIMEGA", doctorWarn, missing+" does not exist"),
				row("PHENIX_BASE_DIR_PHENIX", doctorPass, dir),
				row("PHENIX_BASE_DIR_TOPOLOGIES", doctorFail, file+" is not a directory"),
				row("PHENIX_MOUNT_DIR", doctorWarn, ". is relative, so it depends on the working directory"),
			},
		},
		"files": {
			vars: map[string]string{
				"PHENIX_STORE_ENDPOINT":        "bolt://" + filepath.Join(missing, "store.bdb"),
				"PHENIX_LOG_SYSTEM_PATH":       dir,
				"PHENIX_LOG_CONSOLE":           "stdout",
				"PHENIX_UI_TLS_CERT":           missing,
				"PHENIX_UI_TLS_KEY":            file,
				"PHENIX_UI_LOGS_MINIMEGA_PATH": missing,
			},
			want: []doctorCheck{
				row("PHENIX_LOG_CONSOLE", doctorPass, "stdout"),
				row("PHENIX_LOG_SYSTEM_PATH", doctorPass, filepath.Join(dir, "phenix.log")+" (created on first use)"),
				row("PHENIX_STORE_ENDPOINT", doctorFail,
					"cannot create "+filepath.Join(missing, "store.bdb")+": "+missing+" does not exist"),
				row("PHENIX_UI_LOGS_MINIMEGA_PATH", doctorWarn, missing+" does not exist"),
				row("PHENIX_UI_TLS_CERT", doctorFail, missing+" does not exist"),
				row("PHENIX_UI_TLS_KEY", doctorPass, file),
			},
		},
		"socket": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET": filepath.Join(dir, "phenix.sock")},
			want: []doctorCheck{row("PHENIX_UNIX_SOCKET", doctorPass, filepath.Join(dir, "phenix.sock"))},
		},
		"socket in a missing directory": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET": filepath.Join(missing, "phenix.sock")},
			want: []doctorCheck{
				row("PHENIX_UNIX_SOCKET", doctorFail, missing+" does not exist, so phenix ui cannot create the socket"),
			},
		},
		"socket over a file": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET": file},
			want: []doctorCheck{
				row("PHENIX_UNIX_SOCKET", doctorWarn, file+" is not a socket, and phenix ui deletes it to create one"),
			},
		},
		"socket path too long": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET": longSocket},
			want: []doctorCheck{row("PHENIX_UNIX_SOCKET", doctorFail, longSocketDetail(longSocket))},
		},
		"socket group": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET_GID": "4242", "PHENIX_LOG_SYSTEM_MAX_BACKUPS": "0x10"},
			want: []doctorCheck{
				row("PHENIX_LOG_SYSTEM_MAX_BACKUPS", doctorWarn, `phenix reads "0x10" as 16`),
				row("PHENIX_UNIX_SOCKET_GID", doctorFail, "no group has id 4242, so phenix ui exits when it sets the socket's group"),
			},
		},
		"socket group, as root": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET_GID": "4242"},
			root: true,
			want: []doctorCheck{row("PHENIX_UNIX_SOCKET_GID", doctorWarn, "no group has id 4242, so no group can use the socket")},
		},
		"socket group of others": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET_GID": "200"},
			want: []doctorCheck{row("PHENIX_UNIX_SOCKET_GID", doctorFail,
				"this user is not root or in group 200 (wheel), so phenix ui exits when it sets the socket's group")},
		},
		"socket group of others, as root": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET_GID": "200"},
			root: true,
			want: []doctorCheck{row("PHENIX_UNIX_SOCKET_GID", doctorPass, "200")},
		},
		"socket group of this user with no name": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET_GID": "1000"},
			want: []doctorCheck{row("PHENIX_UNIX_SOCKET_GID", doctorPass, "1000, a group of this user with no name")},
		},
		"known socket group": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET_GID": "100"},
			want: []doctorCheck{row("PHENIX_UNIX_SOCKET_GID", doctorPass, "100")},
		},
		"bad socket group": {
			vars: map[string]string{"PHENIX_UNIX_SOCKET_GID": "-2"},
			want: []doctorCheck{row("PHENIX_UNIX_SOCKET_GID", doctorFail, "-2 is not a group id, so phenix ui fails to start")},
		},
		"hostname suffixes": {
			vars: map[string]string{"PHENIX_HOSTNAME_SUFFIXES": "-minimega, -phenix"},
			want: []doctorCheck{
				row("PHENIX_HOSTNAME_SUFFIXES", doctorWarn, `suffix " -phenix" has spaces around it, which phenix keeps`),
			},
		},
		"dev key": {
			vars: map[string]string{"PHENIX_UI_JWT_SIGNING_KEY": "dev|admin|global-viewer"},
			want: []doctorCheck{
				row("PHENIX_UI_JWT_SIGNING_KEY", doctorWarn, "a dev| key, so phenix ui uses development authentication"),
			},
		},
		"dev key without a role": {
			vars: map[string]string{"PHENIX_UI_JWT_SIGNING_KEY": "dev|admin"},
			want: []doctorCheck{row("PHENIX_UI_JWT_SIGNING_KEY", doctorFail,
				"a dev| key that is not dev|<user>|<role>, so phenix ui refuses every request")},
		},
		"dev key with a custom role": {
			vars: map[string]string{"PHENIX_UI_JWT_SIGNING_KEY": "dev|admin|wizard"},
			want: []doctorCheck{row("PHENIX_UI_JWT_SIGNING_KEY", doctorWarn,
				"a dev| key whose role is not built in, so phenix ui refuses every request unless the store holds the role")},
		},
		"empty": {
			vars: map[string]string{"PHENIX_LOG_LEVEL": ""},
			want: []doctorCheck{row("PHENIX_LOG_LEVEL", doctorWarn, "empty, so phenix ignores it")},
		},
		"overridden": {
			vars:    map[string]string{"PHENIX_LOG_LEVEL": "debug", "PHENIX_USE_GRE_MESH": "yes"},
			sources: map[string]string{"log.level": sourceFlag, "use-gre-mesh": sourceFlag},
			want: []doctorCheck{
				row("PHENIX_LOG_LEVEL", doctorWarn, "debug, but unused, because the flag sets log.level"),
				row("PHENIX_USE_GRE_MESH", doctorWarn,
					`"yes" is not true or false, so phenix reads it as false, but unused, because the flag sets use-gre-mesh`),
			},
		},
		"unknown": {
			vars: map[string]string{
				"PHENIX_UI_LISTEN_ENDPIONT": ":3080",
				"PHENIX_DRYRUN":             "true",
				"PHENIX_BASE_PATH":          "/phenix/",
				"PHENIX_TOKEN":              "abc",
			},
			want: []doctorCheck{
				row("PHENIX_BASE_PATH", doctorWarn,
					"a build argument of the phenix image, which phenix ignores when it runs (did you mean PHENIX_UI_BASE_PATH?)"),
				row("PHENIX_DRYRUN", doctorWarn, "phenix sets it for the apps it runs, and ignores it itself"),
				row("PHENIX_TOKEN", doctorWarn, "matches no phenix setting, so phenix ignores it"),
				row("PHENIX_UI_LISTEN_ENDPIONT", doctorWarn,
					"matches no phenix setting, so phenix ignores it (did you mean PHENIX_UI_LISTEN_ENDPOINT?)"),
			},
		},
		"path": {
			vars: map[string]string{"PATH": "/usr/bin::bin"},
			want: []doctorCheck{
				row("PATH", doctorWarn, `relative entries "", "bin", which phenix will not run commands from`),
			},
		},
		"good path": {
			vars: map[string]string{"PATH": "/usr/bin:/bin"},
			want: []doctorCheck{row("PATH", doctorPass, "2 directories")},
		},
		"editor": {
			vars: map[string]string{"EDITOR": "code -w"},
			want: []doctorCheck{
				row(
					"EDITOR",
					doctorFail,
					`"code -w" is not a program on PATH, so phenix cannot open an editor. phenix runs EDITOR as one program, without arguments`,
				),
			},
		},
		"empty editor": {
			vars: map[string]string{"EDITOR": ""},
			want: []doctorCheck{row("EDITOR", doctorPass, "empty, so phenix uses /usr/bin/vim")},
		},
		"sudo, not root": {
			vars: map[string]string{"SUDO_USER": "alice", "SUDO_COMMAND": "/usr/bin/phenix ui"},
			want: []doctorCheck{
				row("SUDO_COMMAND", doctorPass, "unused, because phenix is not running as root"),
				row("SUDO_USER", doctorPass, "unused, because phenix is not running as root"),
			},
		},
		"sudo phenix": {
			vars: map[string]string{"SUDO_USER": "alice", "SUDO_COMMAND": "/usr/bin/phenix ui"},
			root: true,
			want: []doctorCheck{
				row("SUDO_COMMAND", doctorPass, "names phenix, so phenix uses SUDO_USER's config, store and logs"),
				row("SUDO_USER", doctorPass, "phenix uses the config, store and logs of alice (/home/alice)"),
			},
		},
		"sudo phenix, no such user": {
			vars: map[string]string{"SUDO_USER": "ghost", "SUDO_COMMAND": "/usr/bin/phenix ui"},
			root: true,
			want: []doctorCheck{
				row("SUDO_COMMAND", doctorPass, "names phenix, so phenix uses SUDO_USER's config, store and logs"),
				row("SUDO_USER", doctorWarn, `no user "ghost", so phenix uses root's config, store and logs`),
			},
		},
		"sudo shell": {
			vars: map[string]string{"SUDO_USER": "alice", "SUDO_COMMAND": "/bin/bash"},
			root: true,
			want: []doctorCheck{
				row("SUDO_COMMAND", doctorPass, "does not name phenix, so phenix ignores SUDO_USER"),
				row("SUDO_USER", doctorPass, "unused, because SUDO_COMMAND does not name phenix"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := envChecks(fakeDoctorEnv(tc.vars, tc.root), tc.sources)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("envChecks(%v) mismatch (-want +got):\n%s", tc.vars, diff)
			}
		})
	}
}

func longSocketDetail(path string) string {
	limit := len(unix.RawSockaddrUnix{}.Path)

	return fmt.Sprintf("%s is %d bytes, longer than a unix socket path may be (%d)", path, len(path), limit-1)
}

// Directories phenix cannot read or search warn, as do ones it writes to and
// cannot write, and files it reads fail when they cannot be read. Root can
// read and write anything, so this needs another user.
func TestEnvChecksPermissions(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root can read and write any directory")
	}

	locked := filepath.Join(t.TempDir(), "locked")
	readOnly := filepath.Join(t.TempDir(), "read-only")
	unreadable := writeTestFile(t, t.TempDir(), "key.pem", "")

	for path, mode := range map[string]os.FileMode{locked: 0o000, readOnly: 0o500} {
		if err := os.Mkdir(path, mode); err != nil {
			t.Fatalf("os.Mkdir(%s) error = %v", path, err)
		}

		t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
	}

	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("os.Chmod(%s) error = %v", unreadable, err)
	}

	vars := map[string]string{
		"PHENIX_BASE_DIR_TOPOLOGIES": locked,
		"PHENIX_BASE_DIR_MINIMEGA":   readOnly,
		"PHENIX_MOUNT_DIR":           readOnly,
		"PHENIX_UI_TLS_KEY":          unreadable,
		"PHENIX_UNIX_SOCKET":         filepath.Join(readOnly, "phenix.sock"),
	}

	want := []doctorCheck{
		row("PHENIX_BASE_DIR_MINIMEGA", doctorPass, readOnly),
		row("PHENIX_BASE_DIR_TOPOLOGIES", doctorWarn, locked+" is not readable and searchable by this user"),
		row("PHENIX_MOUNT_DIR", doctorWarn, readOnly+" is not writable by this user"),
		row("PHENIX_UI_TLS_KEY", doctorFail, unreadable+" is not readable by this user"),
		row("PHENIX_UNIX_SOCKET", doctorFail,
			"phenix ui cannot create "+filepath.Join(readOnly, "phenix.sock")+": "+readOnly+" is not writable by this user"),
	}

	if diff := cmp.Diff(want, envChecks(fakeDoctorEnv(vars, false), nil)); diff != "" {
		t.Errorf("envChecks() mismatch (-want +got):\n%s", diff)
	}
}

// Every setting phenix reads has an environment variable check, so a new
// setting cannot be left out.
func TestSettingValueCheckCoversEverySetting(t *testing.T) {
	t.Parallel()

	env := fakeDoctorEnv(nil, false)

	for _, key := range knownSettings() {
		if _, ok := settingValueCheck(env, key, "x"); !ok {
			t.Errorf("settingValueCheck(%s) has no check for the setting", key)
		}
	}
}

// The int, bool and duration checks read a value as viper does: what they
// pass or warn about is the value viper reads, and what they fail viper
// misreads.
func TestEnvParsersMatchViper(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		value := rapid.OneOf(
			rapid.StringMatching(`[-+]?(0x|0)?[0-9a-fhmnsu.]{0,6}`),
			rapid.SampledFrom([]string{"true", "F", "1", "yes", "24h", "24", "1.5h", "-1m"}),
			rapid.String(),
		).Draw(t, "value")

		v := viperWith("k", value)

		if n, check := intVarCheck(value); check.Status == doctorFail {
			if got := v.GetInt("k"); got != 0 {
				t.Fatalf("intVarCheck(%q) fails, but viper reads %d", value, got)
			}
		} else if got := v.GetInt("k"); got != n {
			t.Fatalf("intVarCheck(%q) = %d, viper reads %d", value, n, got)
		}

		if check := boolVarCheck(value); check.Status == doctorPass {
			if got := v.GetBool("k"); strconv.FormatBool(got) != check.Detail {
				t.Fatalf("boolVarCheck(%q) = %s, viper reads %t", value, check.Detail, got)
			}
		} else if v.GetBool("k") {
			t.Fatalf("boolVarCheck(%q) fails, but viper reads true", value)
		}

		if check := jwtLifetimeCheck(value); check.Status != doctorFail {
			if got := v.GetDuration("k"); got <= 0 || !strings.HasPrefix(check.Detail, got.String()) {
				t.Fatalf("jwtLifetimeCheck(%q) = %+v, viper reads %s", value, check, got)
			}
		}
	})
}

// A suggestion is the closest known variable, and none is made for one far
// from every setting.
func TestUnknownEnvDetail(t *testing.T) {
	t.Parallel()

	known := map[string]string{"PHENIX_LOG_LEVEL": "log.level", "PHENIX_LOG_CONSOLE": "log.console"}

	tests := map[string]string{
		"PHENIX_LOG_LEVL":   "matches no phenix setting, so phenix ignores it (did you mean PHENIX_LOG_LEVEL?)",
		"PHENIX_SOMETHING":  "matches no phenix setting, so phenix ignores it",
		"PHENIX_FILES_DIR":  "phenix sets it for the apps it runs, and ignores it itself",
		"PHENIX_LOG_CONSOL": "matches no phenix setting, so phenix ignores it (did you mean PHENIX_LOG_CONSOLE?)",
	}

	for name, want := range tests {
		if got := unknownEnvDetail(name, known); got != want {
			t.Errorf("unknownEnvDetail(%s) = %q, want %q", name, got, want)
		}
	}
}

// Whether the socket's group has a name or not, a member of it passes, a user
// that is not root and not in it fails, and root never fails.
func TestGidCheckProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		gid := rapid.IntRange(0, 5000).Draw(t, "gid")
		member := rapid.Bool().Draw(t, "member")
		named := rapid.Bool().Draw(t, "named")
		root := rapid.Bool().Draw(t, "root")

		env := fakeDoctorEnv(nil, root)
		env.lookupGroup = func(id string) (*user.Group, error) {
			if named {
				return &user.Group{Gid: id, Name: "g" + id}, nil
			}

			return nil, user.UnknownGroupIdError(id)
		}
		env.groups = func() ([]int, error) {
			if member {
				return []int{gid}, nil
			}

			return []int{gid + 1}, nil
		}

		got := gidCheck(env, strconv.Itoa(gid))

		switch {
		case root && got.Status == doctorFail:
			t.Fatalf("gidCheck(%d) as root = %+v, want no failure", gid, got)
		case !root && member && got.Status != doctorPass:
			t.Fatalf("gidCheck(%d) for a member = %+v, want pass", gid, got)
		case !root && !member && got.Status != doctorFail:
			t.Fatalf("gidCheck(%d) for a user not in the group = %+v, want fail", gid, got)
		}
	})
}

// doctorEnvMode is file information of mode, for a socket the tests do not create.
type doctorEnvMode struct {
	os.FileInfo

	mode os.FileMode
}

func (m doctorEnvMode) Mode() os.FileMode { return m.mode }

// A directory this user cannot write fails the socket check, unless a socket
// is already there, such as one a phenix ui of another user serves.
func TestSocketCheckUnwritableDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "phenix.sock")

	tests := map[string]struct {
		mode os.FileMode // of the file at path; 0 is none
		want doctorCheck
	}{
		"no socket": {want: failCheck("phenix ui cannot create " + path + ": " + dir + " is not writable by this user")},
		"a socket": {
			mode: os.ModeSocket | 0o660,
			want: warnCheck(dir + " is not writable by this user, so phenix ui run as this user cannot replace " + path),
		},
		"a regular file": {mode: 0o600, want: failCheck("phenix ui cannot create " + path + ": " + dir + " is not writable by this user")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, false)
			env.access = func(string, uint32) error { return unix.EACCES }
			env.lstat = func(string) (os.FileInfo, error) {
				if tc.mode == 0 {
					return nil, os.ErrNotExist
				}

				return doctorEnvMode{FileInfo: nil, mode: tc.mode}, nil
			}

			if diff := cmp.Diff(tc.want, env.socketCheck(path)); diff != "" {
				t.Errorf("socketCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
