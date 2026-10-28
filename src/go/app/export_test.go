package app

// UnregisterUserApp removes a user app a test registered, so the test can run again.
func UnregisterUserApp(name string) {
	delete(apps, name)
}
