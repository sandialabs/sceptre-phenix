package app

// UnregisterUserApp removes a user app a test registered, so the test can run again.
func UnregisterUserApp(name string) {
	delete(apps, name)
}

// PublishRunResult exposes publishRunResult to tests.
var PublishRunResult = publishRunResult //nolint:gochecknoglobals // test export

// WithStageRuntime exposes withStageRuntime to tests.
var WithStageRuntime = withStageRuntime //nolint:gochecknoglobals // test export

// PopulateStageRuntime exposes populateStageRuntime to tests.
var PopulateStageRuntime = populateStageRuntime //nolint:gochecknoglobals // test export
