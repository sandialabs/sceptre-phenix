package cmd

import "phenix/api/settings"

// Setting keys that several commands and doctor checks read.
const (
	keyLogMaxSize  = "log.system.max-size"
	keyUseGREMesh  = "use-gre-mesh"
	keyJWTLifetime = "ui.jwt-lifetime"
	keyUIUsers     = "ui.users"

	keyCaptureMaxSize = "ui.captures.max-size"

	keyMinimegaReadConnections = settings.MinimegaReadConnectionsKey
)
