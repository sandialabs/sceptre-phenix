package cmd

// doctorStuck is what the stored status of a running experiment marks as in progress.
type doctorStuck struct {
	apps        []string // the apps whose running flag is set, other than scorch
	scorch      bool     // the running flag of scorch is set
	scorchRun   int      // the SCORCH run that the status of scorch names, or -1
	pending     bool     // delayed VM starts or post-start apps wait for phenix ui
	interrupted bool     // phenix could not resume the start
}
