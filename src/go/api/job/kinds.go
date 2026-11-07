package job

// The kinds of the jobs that the phenix ui server runs. GET /jobs and
// phenix job list filter by them.
const (
	// KindVMAction is the kind of the bulk VM action jobs.
	KindVMAction = "vms.action"
	// KindArchive is the kind of the jobs that archive experiment files.
	KindArchive = "files.archive"
	// KindClone is the kind of the jobs that clone a disk image.
	KindClone = "disk.clone"
	// KindExperimentsStart is the kind of the jobs that start every stopped experiment.
	KindExperimentsStart = "experiments.start"
	// KindExperimentsTrigger is the kind of the jobs that trigger an app stage in every experiment.
	KindExperimentsTrigger = "experiments.trigger"
	// KindExperimentsPeriodic is the kind of the jobs that run the apps' running stage of one experiment on their
	// schedule.
	KindExperimentsPeriodic = "experiments.periodic"
)

// Kinds returns each kind of job that the phenix ui server runs.
func Kinds() []string {
	return []string{
		KindVMAction, KindArchive, KindClone, KindExperimentsStart, KindExperimentsTrigger, KindExperimentsPeriodic,
	}
}
