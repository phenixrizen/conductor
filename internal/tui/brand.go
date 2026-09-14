package tui

// The Switch retains two readable C routes. A single slanted stroke stands in
// for its small junction at character-cell resolution; drawing a closed box here
// makes the accent dominate the mark. Small terminals keep a plain heading.
func switchHeader(width, height int) []string {
	if width < 60 || height < 24 {
		return nil
	}
	return []string{
		"   _______ /",
		"  /  ____",
		" |  /          Conductor",
		" |  \\____      Engineering intent, orchestrated.",
		"  \\______",
	}
}
