package cmd

func hostSkip(detail string) doctorCheck {
	return doctorCheck{Name: "", Status: doctorSkip, Detail: "not checked, because " + detail}
}
