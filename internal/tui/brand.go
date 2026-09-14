package tui

import "strings"

// Keep the owner's ASCII reference intact. The caller reserves factual header,
// footer and content rows first, so artwork cannot crowd out the workbench.
func switchHeader(width, availableRows int) []string {
	mark := []string{
		"############     ________",
		"     ####         ##    /       /",
		"   ###    #########    /_______/",
		"  ##    ###",
		" ##    ##",
		" ##    ##",
		"  ##    ###",
		"   ###    ###########",
		"     ####",
		"        #############",
	}
	if availableRows < len(mark) {
		return nil
	}
	for _, line := range mark {
		if len(line) > width {
			return nil
		}
	}
	const labelColumn = 36
	const tagline = "Engineering intent, orchestrated."
	if width >= labelColumn+len(tagline) {
		mark[3] += strings.Repeat(" ", labelColumn-len(mark[3])) + "Conductor"
		mark[5] += strings.Repeat(" ", labelColumn-len(mark[5])) + tagline
	}
	return mark
}
