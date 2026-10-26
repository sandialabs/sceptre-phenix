package mmcli

import (
	"errors"
	"strings"
	"time"

	"github.com/activeshadow/libminimega/miniclient"

	"phenix/util/metrics"
)

// subcommands are the minimega commands phenix counts by name, each with the
// subcommands named in its label. Any other command is counted as "other",
// and a subcommand not listed by its command alone, so arguments such as VM
// or file names never become label values.
//
//nolint:gochecknoglobals // constant table
var subcommands = map[string][]string{
	"background": nil,
	"bridge":     nil,
	"capture":    {"pcap", "netflow"},
	"cc": {
		"background", "clients", "commands", "delete", "exec", "filter", "log", "mount",
		"prefix", "process", "recv", "response", "send", "tunnel", "rtunnel",
	},
	"clear":   {"capture", "cc", "namespace", "tap", "vlans", "vm"},
	"disk":    {"commit", "create", "info", "inject", "rebase", "resize", "set-backing", "snapshot"},
	"dnsmasq": nil,
	"file":    {"delete", "get", "list", "status", "stream"},
	"host":    nil,
	"log":     nil,
	"mesh":    {"degree", "dial", "hangup", "list", "send", "status", "timeout"},
	"ns": {
		"add-host", "bridge", "del-host", "flush", "hosts", "load", "queue", "queueing",
		"run", "schedule", "snapshot",
	},
	"qos":     nil,
	"read":    nil,
	"router":  nil,
	"shell":   nil,
	"tap":     {"create", "delete", "mirror"},
	"version": nil,
	"vlans":   {"add", "blacklist", "range"},
	"vm": {
		"cdrom", "config", "flush", "hotplug", "info", "kill", "launch", "migrate", "net",
		"qmp", "save", "screenshot", "snapshot", "start", "stop", "tag", "top",
	},
	"vnc": nil,
}

// commandLabel is the metrics label of a minimega command: its name and,
// for the commands subcommands lists, its subcommand, such as "vm info". It
// skips the "namespace <name>" and dot-command (".columns <list>") prefixes.
func commandLabel(command string) string {
	words := strings.Fields(command)

	for len(words) > 1 && (strings.HasPrefix(words[0], ".") || words[0] == "namespace") {
		words = words[2:]
	}

	if len(words) == 0 {
		return metrics.Other
	}

	subs, ok := subcommands[words[0]]
	if !ok {
		return metrics.Other
	}

	if len(words) > 1 {
		for _, sub := range subs {
			if words[1] == sub {
				return words[0] + " " + sub
			}
		}
	}

	return words[0]
}

// commandObserver records one minimega command in the metrics once its
// responses end.
type commandObserver struct {
	label string
	sent  time.Time
	// set when minimega answered with an error
	failed bool
}

// observe starts recording the command c, as it is sent.
func observe(c *Command) *commandObserver {
	return &commandObserver{label: commandLabel(c.Command), sent: time.Now(), failed: false}
}

// saw notes a response of the command.
func (o *commandObserver) saw(resp *miniclient.Response) {
	for _, r := range resp.Resp {
		if r.Error != "" {
			o.failed = true
		}
	}
}

// done records the command, which ended with err from the connection, or nil.
func (o *commandObserver) done(err error) {
	outcome := metrics.OK

	switch {
	case errors.Is(err, ErrTimeout):
		outcome = metrics.CommandTimeout
	case err != nil:
		outcome = metrics.CommandLost
	case o.failed:
		outcome = metrics.CommandError
	}

	metrics.MinimegaCommand(o.label, outcome, time.Since(o.sent))
}

// notSent records the command c, which was never sent.
func notSent(c *Command) {
	metrics.MinimegaCommand(commandLabel(c.Command), metrics.CommandNotSent, 0)
}
