package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
)

// Read a selected secret file without allowing a named pipe to block startup.
// The secret is used only for the provider token endpoint, never browser output.
func browserClientSecret(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("cannot open OIDC client secret file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", errors.New("OIDC client secret must be a regular file of at most 4096 bytes")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return "", errors.New("cannot read OIDC client secret within its size limit")
	}
	secret := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if secret == "" || strings.ContainsAny(secret, "\r\n\x00") {
		return "", errors.New("OIDC client secret must be nonempty and contain no line breaks or NUL")
	}
	return secret, nil
}
