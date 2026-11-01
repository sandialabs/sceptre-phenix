package cmd

// doctorFound is what earlier checks of one run found, for the checks that
// build on them.
type doctorFound struct {
	experiments *doctorExperiments // the stored experiments, once a check read them
	inventory   *doctorInventory   // minimega's namespaces, VMs, taps and captures, once a check read them
	mesh        *doctorMesh        // the nodes of minimega's mesh, once a check read them

	stored    map[string]*doctorStored // the stored configs of each kind, once a check read them
	storeBusy string                   // why a read of the store stopped at its lock, which later reads give
	users     []string                 // the stored user names that rbac-users names, which --report redacts

	certNames []string // the DNS names of the TLS certificate, which --report redacts
}
