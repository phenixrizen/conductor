package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/phenixrizen/conductor/pkg/client"
)

func clientForCommand(command, actor string, actorSet bool, workspace, repositoryID string) (*client.Client, error) {
	// These operations only inspect local files/Git. Unrelated credential
	// environment configuration must not read files or make them fail.
	if command == "context" || command == "context-check" {
		return nil, nil
	}
	token, configured, err := accessTokenFromEnvironment()
	if err != nil {
		return nil, err
	}
	base := env("CONDUCTOR_URL", "http://localhost:8080")
	if configured {
		if actorSet || actor != "" {
			return nil, errors.New("--actor cannot be combined with an access token; authenticated identity comes from the server")
		}
		if command == "tui" && (workspace == "" || repositoryID == "") {
			return nil, errors.New("authenticated terminal review requires --workspace and --repository-id (or CONDUCTOR_WORKSPACE and CONDUCTOR_REPOSITORY_ID)")
		}
		if collectionCommand(command) && (workspace == "" || repositoryID == "") {
			return nil, errors.New("remote context commands require --workspace and --repository-id (or CONDUCTOR_WORKSPACE and CONDUCTOR_REPOSITORY_ID)")
		}
		return client.NewAuthenticated(base, token, workspace, repositoryID)
	}
	if command == "session" || command == "repositories" || collectionCommand(command) {
		return nil, errors.New("this command requires CONDUCTOR_TOKEN_FILE or CONDUCTOR_TOKEN")
	}
	if workspace != "" || repositoryID != "" {
		return nil, errors.New("workspace and repository IDs require authenticated mode")
	}
	if actor == "" {
		return nil, errors.New("--actor is required for local development; use CONDUCTOR_TOKEN_FILE or CONDUCTOR_TOKEN for authenticated review")
	}
	return client.New(base, actor), nil
}

func accessTokenFromEnvironment() (string, bool, error) {
	token, tokenSet := os.LookupEnv("CONDUCTOR_TOKEN")
	path, fileSet := os.LookupEnv("CONDUCTOR_TOKEN_FILE")
	if tokenSet && fileSet {
		return "", true, errors.New("set only one of CONDUCTOR_TOKEN_FILE and CONDUCTOR_TOKEN")
	}
	if tokenSet {
		return token, true, nil
	}
	if !fileSet {
		return "", false, nil
	}
	// A token file is a bounded regular file, never stdin or a pipe. Validate
	// the opened descriptor too; nonblocking open handles a FIFO replacement
	// between the initial stat and open without hanging before validation.
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > client.MaxTokenBytes {
		return "", true, errors.New("CONDUCTOR_TOKEN_FILE must name a readable regular file of at most 16 KiB")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", true, errors.New("cannot open CONDUCTOR_TOKEN_FILE")
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > client.MaxTokenBytes {
		return "", true, errors.New("CONDUCTOR_TOKEN_FILE must be a regular file of at most 16 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, client.MaxTokenBytes+1))
	if err != nil || len(data) > client.MaxTokenBytes {
		return "", true, errors.New("cannot read CONDUCTOR_TOKEN_FILE within the 16 KiB limit")
	}
	// Permit one editor-added LF or CRLF, without hiding spaces or extra lines
	// that would make an Authorization header invalid.
	token = string(data)
	if strings.HasSuffix(token, "\n") {
		token = strings.TrimSuffix(strings.TrimSuffix(token, "\n"), "\r")
	}
	return token, true, nil
}
