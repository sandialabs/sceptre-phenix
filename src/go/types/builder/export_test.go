package builder

// IconKeyForSpec exposes iconKeyForSpec to the external tests.
func IconKeyForSpec(spec map[string]any) string {
	return iconKeyForSpec(spec)
}
