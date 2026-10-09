package nvim

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// swapOf reads swapinfo()'s dictionary for the swap file file.
func swapOf(file string, info interface{}) Swap {
	sw := Swap{File: file}
	m, _ := info.(map[string]interface{})
	if m == nil {
		return sw
	}
	sw.Pid = toInt(m["pid"])
	sw.User, _ = m["user"].(string)
	sw.Host, _ = m["host"].(string)
	sw.Modified = toInt(m["dirty"]) != 0
	sw.Mtime = int64(toInt(m["mtime"]))
	if host, err := os.Hostname(); err == nil && (sw.Host == "" || strings.EqualFold(sw.Host, host)) {
		sw.Running = processRuns(sw.Pid)
	}
	return sw
}

// removeSwap removes a swap file: only a regular file whose name is a
// Neovim or Vim swap file's (.swp, .swo, … ending in .sw?), so a swap path
// Neovim reported is all it can remove.
func removeSwap(file string) error {
	ext := filepath.Ext(file)
	if len(ext) != 4 || !strings.HasPrefix(ext, ".sw") {
		return errors.New("nvim: not a swap file")
	}
	fi, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("nvim: not a swap file")
	}
	return os.Remove(file)
}
