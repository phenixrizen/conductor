// conductor-mcp is a stdio MCP bridge to one authenticated Conductor scope.
// Standard output is exclusively protocol traffic; credentials never enter it.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/phenixrizen/conductor/internal/mcpserver"
	"github.com/phenixrizen/conductor/pkg/client"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "conductor-mcp:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context) error {
	if len(os.Args) != 1 {
		return errors.New("configure CONDUCTOR_URL, CONDUCTOR_TOKEN_FILE, CONDUCTOR_WORKSPACE and CONDUCTOR_REPOSITORY_ID; no command arguments are accepted")
	}
	if _, set := os.LookupEnv("CONDUCTOR_TOKEN"); set {
		return errors.New("use CONDUCTOR_TOKEN_FILE, not CONDUCTOR_TOKEN")
	}
	token, err := readToken(os.Getenv("CONDUCTOR_TOKEN_FILE"))
	if err != nil {
		return err
	}
	api, err := client.NewAuthenticated(os.Getenv("CONDUCTOR_URL"), token, os.Getenv("CONDUCTOR_WORKSPACE"), os.Getenv("CONDUCTOR_REPOSITORY_ID"))
	if err != nil {
		return err
	}
	bridge, err := mcpserver.New(api)
	if err != nil {
		return err
	}
	if err := bridge.Run(ctx, &mcpserver.Transport{Reader: os.Stdin, Writer: os.Stdout}); err != nil {
		return errors.New("MCP connection ended with a protocol or transport failure")
	}
	return nil
}
func readToken(path string) (string, error) {
	// O_NONBLOCK prevents a FIFO swapped into the path from hanging before the
	// descriptor's regular-file check. Credentials are read once until exit.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("CONDUCTOR_TOKEN_FILE must be a readable regular file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > client.MaxTokenBytes {
		return "", errors.New("CONDUCTOR_TOKEN_FILE must be a regular file of at most 16 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, client.MaxTokenBytes+1))
	if err != nil || len(data) > client.MaxTokenBytes {
		return "", errors.New("could not read bounded token file")
	}
	token := string(data)
	if strings.HasSuffix(token, "\n") {
		token = strings.TrimSuffix(strings.TrimSuffix(token, "\n"), "\r")
	}
	return token, nil
}
