package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/hostagent"
	"github.com/phenixrizen/conductor/internal/proto"
)

// runHostAgent is hostagent.Run: a test replaces it to see the options.
var runHostAgent = hostagent.Run

// hostingBanner starts the line the host prints on stderr once it has read the
// server's registration reply; the session ID and its URL follow.
const hostingBanner = "conductor: hosting session "

func runHost(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("host", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", envOr("CONDUCTOR_SERVER", "http://localhost:8080"), "conductor server URL (env CONDUCTOR_SERVER)")
	token := fs.String("token", "", "host token (env CONDUCTOR_HOST_TOKEN); none for a switchyard that admits open hosts")
	name := fs.String("name", "", "session name shown in the UI")
	hostName := fs.String("host-name", "", "machine label (default: hostname)")
	agentID := fs.String("agent", "", "agent id shown in the UI (default: command name); the id of an adapter with a launch route (claude, codex, pi, aider) also wires Conductor's hooks into the command at launch; other adapters install by hand (conductor hooks install)")
	cwd := fs.String("cwd", "", "working directory for the command (default: current)")
	relayOnly := fs.Bool("relay-only", false, "never use WebRTC; relay through the server")
	noLocal := fs.Bool("no-local", false, "do not attach this terminal to the session")
	stun := fs.String("stun", "", "comma separated ICE server URLs overriding the server's list")
	iceUDPPort := fs.Int("ice-udp-port", envInt("CONDUCTOR_ICE_UDP_PORT"), "one UDP port for every WebRTC connection (env CONDUCTOR_ICE_UDP_PORT); 0 lets each connection pick its own")
	icePublicIP := fs.String("ice-public-ip", envOr("CONDUCTOR_ICE_PUBLIC_IP", ""), "the address advertised as this host's own (env CONDUCTOR_ICE_PUBLIC_IP): a forwarder's, such as the desktop app's on Windows in front of WSL")
	scrollback := fs.Int("scrollback", 256<<10, "scrollback bytes replayed to late viewers")
	fileView := fs.String("file-view", "view", "which roles may read files: view, control, off")
	fileEdit := fs.String("file-edit", "control", "whether the control role may edit files through the editor's Neovim on this machine: control, off")
	serverConfig := fs.String("server-config", "", "the config file of a conductor serve on this machine: the Files tab refuses it, its dataDir and its catalogPath (absolute paths), as that server's own sessions do (the data directory CONDUCTOR_DATA_DIR names, ~/.conductor and the catalog file CONDUCTOR_CATALOG_PATH names are refused without it)")
	signalPattern := fs.String("signal-pattern", "", "regular expression (RE2, at most 200 bytes, not matching an empty line) for the last line of the terminal: a match after 500 ms without output marks the session as needing input")
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
	// No token is fine on a switchyard that admits open hosts; elsewhere
	// the server answers 401 and says so.
	switch *fileView {
	case "view", "control", "off":
	default:
		return 2, fmt.Errorf("invalid --file-view %q", *fileView)
	}
	if *fileEdit != "control" && *fileEdit != "off" {
		return 2, fmt.Errorf("invalid --file-edit %q", *fileEdit)
	}
	// The Files tab of a hosted session refuses what a session of the
	// server on this machine refuses: its data directory, config file and
	// catalog file, and the copies beside the two files.
	deny, err := config.LocalFileDeny(*serverConfig)
	if err != nil {
		return 2, fmt.Errorf("the files of the server on this machine, which the Files tab refuses: %w", err)
	}
	var pattern *regexp.Regexp
	if *signalPattern != "" {
		re, err := catalog.CompilePattern(*signalPattern)
		if err != nil {
			return 2, fmt.Errorf("invalid --signal-pattern: %w", err)
		}
		pattern = re
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
		ICE:             hostagent.ICE{UDPPort: *iceUDPPort, PublicIP: *icePublicIP},
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
		FileEdit:        *fileEdit,
		FileDeny:        deny,
		Pattern:         pattern,
		Adapter:         *agentID,
		Log:             log,
		Registered: func(sessionID, base string) {
			fmt.Fprintf(stderr, "%s%s at %s/sessions/%s\r\n", hostingBanner, sessionID, base, sessionID)
		},
	}
	if f, ok := stdin.(*os.File); ok {
		opts.Stdin = f
	}
	if f, ok := stdout.(*os.File); ok {
		opts.Stdout = f
	}
	res, err := runHostAgent(ctx, opts)
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

// envInt reads an integer from the environment; unset or unreadable is 0.
func envInt(key string) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return 0
	}
	return n
}
