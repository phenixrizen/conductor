package tui

// The monochrome Switch keeps two C routes and a detached, right-leaning
// parallelogram. Short terminals use a compact mark; very small terminals retain
// the plain heading and inspected facts. The mark never represents status.
func switchHeader(width, height int) []string {
	if width < 60 || height < 24 {
		return nil
	}
	if width < 72 || height < 36 {
		return []string{
			"  //====      ___   Conductor",
			" ||          /__/   Engineering intent, orchestrated.",
			"  \\\\====",
		}
	}
	return []string{
		"   _______       ____",
		"  /  ____       /___/   Conductor",
		" |  /                   Engineering intent, orchestrated.",
		" |  \\____",
		"  \\______",
	}
}
