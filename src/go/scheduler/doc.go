/*
Package scheduler contains phenix schedulers customize VM scheduling
on cluster nodes using VM hardware requirements, network configurations,
etc. and cluster node capacity.

Default Schedulers

  - isolate.go:            isolates all experiment VMs on a single cluster node
  - round-robin.go:        assigns experiment VMs to cluster nodes in a
    round-robin fashion
  - subnet-compute.go:     assigns experiment VMs to cluster nodes based on
    interface VLAN assignments. It puts a VM that has no interfaces after
    the other VMs, on the node that has the most VMs of the experiment and
    sufficient memory.

# Warnings

A schedule can have warnings. A warning does not stop the schedule. The
built-in schedulers give a warning when a VM is set to a node that is not a
schedulable cluster node, and for each node where the VMs need more memory
or more CPUs than the node has. The capacity checks include the VMs that
have do_not_boot set, because these VMs can boot later. A node that gives a
capacity of 0 is not checked. Nodes have no VM limit.

A built-in scheduler must finish within BuiltinTimeout, with its read of the
cluster nodes.

# Custom User Schedulers

Custom user schedulers are interacted with through STDIN and STDOUT. The
phenix `user.go` scheduler will pass a JSON blob to the custom user scheduler
through STDIN. This JSON blob will contain the experiment data based on the
latest version of the `Experiment` schema (`spec`) and the schedulable cluster
hosts with their disk usage (`hosts`). The phenix will block further
actions and wait for STDOUT return of an updated JSON blob from the custom
user scheduler. phenix uses only `spec.schedules` of that blob, and adds its
entries to the schedules of the experiment. phenix also uses the optional
top-level `warnings` array of strings of that blob as the warnings of the
schedule. A user scheduler run, with the read of the cluster hosts, must
finish within UserTimeout, or phenix stops it. In addition to the JSON blob,
the custom user scheduler should exit with a value of 0. If the custom user scheduler is returning
log(s) or any error messages, those should be written to STDERR (and in the
case of an error, the exit value should be non-0). Custom user schedulers
must 1) be in the user's PATH, 2) be executable, and 3) follow the naming
convention `phenix-scheduler-<name>`.

Example Custom User Scheduler

	import json, sys

	def eprint(*args):
	  print(*args, file=sys.stderr)

	def main() :
	  if len(sys.argv) != 1:
	    eprint("no arguments expected on the command line")
	    sys.exit(1)

	  spec = json.loads(sys.stdin.read())
	  sched = spec.schedules

	  for n in spec['topology']['nodes']:
	    for h in n['general']['hostname']:
	      sched[h] = 'compute0'

	  print(json.dumps(spec))
*/
package scheduler
