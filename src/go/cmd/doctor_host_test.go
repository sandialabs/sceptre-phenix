package cmd

// doctorHintOf is the hint runDoctorChecks gives c: none for a row that passes.
func doctorHintOf(c doctorCheck, hint func(doctorCheck) string) string {
	if c.Status == doctorPass {
		return ""
	}

	return hint(c)
}
