package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/phenixrizen/conductor/internal/hostagent"
	"github.com/phenixrizen/conductor/internal/proto"
)

func runHost(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("host", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", envOr("CONDUCTOR_SERVER", "http://localhost:8080"), "conductor server URL (env CONDUCTOR_SERVER)")
	token := fs.String("token", "", "host token (env CONDUCTOR_HOST_TOKEN)")
	name := fs.String("name", "", "session name shown in the UI")
	hostName := fs.String("host-name", "", "machine label (default: hostname)")
	agentID := fs.String("agent", "", "agent id label (default: command name)")
	cwd := fs.String("cwd", "", "working directory for the command (default: current)")
	relayOnly := fs.Bool("relay-only", false, "never use WebRTC; relay through the server")
	noLocal := fs.Bool("no-local", false, "do not attach this terminal to the session")
	stun := fs.String("stun", "", "comma separated ICE server URLs overriding the server's list")
	scrollback := fs.Int("scrollback", 256<<10, "scrollback bytes replayed to late viewers")
	fileView := fs.String("file-view", "view", "which roles may read files: view, control, off")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn, error")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: conductor host [flags] -- <command...>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	argv := fs.Args()
	if len(argv) == 0 {
		fs.Usage()
		return 2, errors.New("a command is required after --")
	}
	if *token == "" {
		*token = os.Getenv("CONDUCTOR_HOST_TOKEN")
	}
	if *token == "" {
		return 2, errors.New("a host token is required (--token or CONDUCTOR_HOST_TOKEN)")
	}
	switch *fileView {
	case "view", "control", "off":
	default:
		return 2, fmt.Errorf("invalid --file-view %q", *fileView)
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		return 2, fmt.Errorf("invalid log level %q", *logLevel)
	}
	// Logs go to stderr; when the local terminal is attached they would
	// corrupt the PTY output, so keep them at warn unless asked otherwise.
	logOut := stderr
	if !*noLocal && *logLevel == "info" {
		level = slog.LevelWarn
	}
	log := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: level}))

	var ice []proto.ICEServer
	if *stun != "" {
		for _, u := range strings.Split(*stun, ",") {
			if u = strings.TrimSpace(u); u != "" {
				ice = append(ice, proto.ICEServer{URLs: []string{u}})
			}
		}
	}
	opts := hostagent.Options{
		ServerURL:       *server,
		Token:           *token,
		Name:            *name,
		HostName:        *hostName,
		AgentID:         *agentID,
		Argv:            argv,
		Dir:             *cwd,
		RelayOnly:       *relayOnly,
		LocalAttach:     !*noLocal,
		ICEServers:      ice,
		ScrollbackBytes: *scrollback,
		FileView:        *fileView,
		Log:             log,
		Registered: func(sessionID, base string) {
			fmt.Fprintf(stderr, "conductor: hosting session %s at %s/sessions/%s\r\n", sessionID, base, sessionID)
		},
	}
	if f, ok := stdin.(*os.File); ok {
		opts.Stdin = f
	}
	if f, ok := stdout.(*os.File); ok {
		opts.Stdout = f
	}
	res, err := hostagent.Run(ctx, opts)
	if err != nil {
		return 1, err
	}
	return res.ExitCode, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
