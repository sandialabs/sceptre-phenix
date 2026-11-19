package main

import (
	"fmt"
	"strings"
)

// docsDir is the documentation root, from a reference page.
const docsDir = "../"

// docLink is a documentation page that a reference page links to: its title, its path from the
// documentation root, and what the reader finds there.
type docLink struct {
	Title string
	Path  string
	About string
}

// relatedPages returns the documentation pages for what each top-level command manages, by
// command name. A command that no page describes has no entry.
func relatedPages() map[string][]docLink {
	var (
		users   = docLink{"User Administration", "user-administration.md", "users, roles and permissions"}
		doctor  = docLink{"Doctor", "doctor.md", "what each check means and how to fix it"}
		health  = docLink{"Health Checks", "health.md", "the health endpoints of the server"}
		disks   = docLink{"Disks", "disks.md", "the Disks page, backing chains and image formats"}
		images  = docLink{"Images", "image.md", "how to build a disk image"}
		scorch  = docLink{"Scorch", "scorch.md", "how to write and run Scorch components"}
		setting = docLink{"Settings", "settings.md", "every setting, with its flag and environment variable"}
		configs = docLink{"Configuration Files", "configuration.md", "the kinds of config and how to write them"}
		pcaps   = docLink{"Packet Captures", "packet-captures.md", "capture options, limits and troubleshooting"}
	)

	return map[string][]docLink{
		"admin": {users, {"API", "api.md#authentication", "how to use an API token"}},
		"completion": {
			{"Shell Completion", "index.md#accessing-the-cli-shell-completion", "the wrapper script and shell setup"},
		},
		"config": {
			configs,
			{"Schema", "schema.md", "the schema of each config kind"},
			{"Workflow", "workflow.md", "deploy a directory of configs with one command"},
		},
		"disk":   {disks, images},
		"doctor": {doctor, {"Troubleshooting", "troubleshooting.md", "fixes for common problems"}, health},
		"experiment": {
			{"Experiments", "experiments.md", "create, start, schedule and stop experiments"},
			{"Experiment Files", "experiments.md#experiment-files", "the files of an experiment"},
			{"Apps", "apps.md", "the apps that a scenario can add"},
			scorch,
			{"State of Health", "state-of-health.md", "the checks of the soh app and its network view"},
			{"Netflow", "netflow.md", "netflow capture and its recorded files"},
			pcaps,
		},
		"healthcheck": {health, doctor},
		"host":        {{"Hosts", "hosts.md", "the Hosts page"}},
		"image":       {images, disks},
		"job":         {{"Background Jobs", "jobs.md", "what runs as a job and how to watch one"}},
		"logs":        {{"Logging", "logging.md", "log outputs, levels, fields and the audit log"}},
		"mm":          {{"minimega", "minimega.md", "how phēnix uses minimega"}},
		"settings":    {setting, doctor},
		"ui": {
			{"Web UI", "web-ui.md", "a tour of the web UI"},
			setting,
			users,
			health,
		},
		"util": {
			{"Apps", "apps.md#user-apps", "the JSON that a user app reads"},
			scorch,
			users,
		},
		"vlan": {{"VLANs", "vlans.md", "VLAN aliases, ranges and the VLANs page"}},
		"vm": {
			{"VMs", "vms.md", "VM actions in the web UI and from the command line"},
			pcaps,
			{"WebShark", "webshark.md", "view or stream a running capture"},
			{"Tunneler", "tunneler.md", "port forwards to a VM"},
			disks,
		},
		"workflow": {{"Workflow", "workflow.md", "the topology directory layout and how apply works"}, configs},
	}
}

// seeAlso returns a See Also section that lists links, and nothing when there are none.
func seeAlso(links []docLink) string {
	if len(links) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("## See Also\n\n")

	for _, l := range links {
		fmt.Fprintf(&b, "* [%s](%s%s): %s.\n", l.Title, docsDir, l.Path, l.About)
	}

	return b.String()
}
