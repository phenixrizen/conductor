# Round 6 plan: switchyard, invites the app opens itself, and ICE from WSL without mirrored networking

Asked on 2026-10-03 after Round 5 landed. The sharing model of Round 5 makes
one server reachable (a mapped port and a certificate). This round covers the
other shape the owner wants: everyone runs Conductor locally, in the desktop
app, and a small public coordinator, **switchyard**, introduces two apps to
each other so the terminal goes over WebRTC between them. Switchyard serves
no page to the apps and relays only for the pairs ICE cannot connect.

**Decisions (2026-10-03):**

- **Mirrored networking is never required or recommended.** It changes WSL's
  network stack for Docker and every other tool. ICE from a WSL-hosted server
  goes through Hyper-V's NAT with a UDP forwarder the desktop app runs on
  Windows, and the server advertises the Windows address.
- **Switchyard is Conductor in a mode**, not a second program: the signaling
  package, the host route and the share links of a normal server, with
  launching, crews and the catalog switched off, the relay behind a flag, and
  the page still served (it costs nothing and a phone can still view).
- **An invite is a share link minted on switchyard**, opened by the app
  through the `conductor:` URL scheme, which renders the join view from its
  own bundle and talks to switchyard only for signaling.
- **Nothing in Round 5 is undone.** The reachable-server path stays for
  servers with a public address; switchyard is the path for everyone else.

## R6-1. Switchyard mode (Go)

`conductor switchyard [serve flags]`, which is `serve` with
`switchyard.enabled` (`CONDUCTOR_SWITCHYARD=1`) and `switchyard.relay`
(`CONDUCTOR_SWITCHYARD_RELAY`, default on). In that mode the server mounts no
launch, crew, run, catalog or integration route (each answers `403
switchyard`, "this server coordinates hosted sessions and launches none"),
needs no catalog and no agents, keeps `/ws/host`, `/ws/sessions/{id}` for
hosted sessions, the share links, `/api/join/{token}`, the events stream, the
health and reach routes and the workbench. With the relay off, a viewer's
`relay` gets `error{code:"relay_off"}` and a host registering with
`relayOnly` is refused (`relay_off`). `GET /api/health` reports `switchyard`
and `relay`. The workbench reads `switchyard` from `/api/whoami` and shows
the Sessions page and the Wall as they are, a notice on Agents and Crews.
Tests: `TestSwitchyardLaunchesNothing`, `TestSwitchyardCoordinatesAHostAndAViewer`
(a host registers, a viewer joins by link, signaling passes both ways, the
relay works), `TestSwitchyardRelayOff`, `config_test` for the switches,
`serve_test` for the command. Docs: README "Switchyard" under Sharing, the
Configuration rows, `docs/protocol.md` (`relay_off`, the 403), architecture.

## R6-2. ICE through Hyper-V's NAT (Go, then the app)

**Go.** `config.ICE{UDPPort, PublicIP}` (`CONDUCTOR_ICE_UDP_PORT`,
`CONDUCTOR_ICE_PUBLIC_IP`): the host agent's setting engine (shared by
`conductor host` and the server's rendezvous publishing) multiplexes every
peer connection on that one UDP port (pion's ICE UDP mux) and advertises
`PublicIP` as its host candidate (NAT 1:1). Test: a loopback peer connection
whose host candidates carry the configured address and port, and a connection
that still completes through the mux.

**Desktop, Windows only.** `src/udp-forwarder.ts`: a user-space NAT in the
main process. It listens on the same UDP port on Windows, keeps a table from
each remote address to a socket of its own towards `<wsl address>:<port>`,
forwards inbound packets there and returns the replies to the remote through
the listening socket, and drops idle entries after 60 s. The WSL launcher
learns the distribution's address after the handshake (`hostname -I` inside
it), passes `CONDUCTOR_ICE_UDP_PORT` and `CONDUCTOR_ICE_PUBLIC_IP` (the
Windows LAN address) through WSLENV, and starts the forwarder with the
server; in mirrored mode (detected as today) it starts none. The NSIS
installer adds the inbound UDP firewall rule for the port and removes it on
uninstall. The setup screen's text about mirrored networking goes. Tests:
vitest for the table (two sockets, a reply, expiry), the launcher's env, the
installer script's presence of the rule.

## R6-3. Invites the app opens itself (protocol, web, desktop)

**Host link messages (protocol change: `internal/proto`, `protocol.ts`,
`docs/protocol.md`).** Host → server `link{requestId, role, ttlSeconds?,
label?}` (label ≤ 120 bytes, ttl ≤ 24 h, 5 a minute per host), server → host
`link_created{requestId, url, invite, link}` or `error`. A server with a
rendezvous asks it when `POST /api/sessions/{id}/links` is called for a
published session, so the app's Share dialog returns the switchyard link and
the `conductor://` invite. Tests: the limits, the round trip through
`uplink_test`, the API route on a published session.

**The invite.** `conductor://<switchyard host>/join/<token>` (https implied;
`http` only for loopback). The app registers the scheme (electron-builder
`protocols`, `setAsDefaultProtocolClient`, the `open-url` and
`second-instance` paths) and opens its workbench at
`/join/<token>?server=https://<switchyard host>`. The Share dialog shows
both the link and the invite with a copy button each.

**The web.** The join page takes `?server=`: `https://` or a loopback
`http://`, nothing else; `api.join` and the transport use that base for that
page alone, with no admin token. Switchyard answers the cross-origin
`GET /api/join/{token}` and accepts the WebSocket from loopback origins:
`switchyard.allowedOrigins` defaults to `http://127.0.0.1:*` and
`http://localhost:*`, and the join route sends CORS headers for them. Tests:
vitest for the `server` validation, Go for the CORS headers and origins,
Playwright `switchyard.spec.ts`: the harness starts a second server in
switchyard mode, the first publishes to it, the browser opens the first
workbench's join page with `?server=` and sees the terminal.

**Desktop settings.** A Switchyard section: server, host token, the name
shown there; saved as `rendezvous` in the server's environment; a status
line from the local server (`published at …`).

## R6-4. Docs and the gate

README Sharing rewritten in three paths (a reachable server, switchyard, a
proxy), the desktop README's Windows page without mirrored mode,
`docs/architecture.md` "Switchyard", `docs/features.md` Round 6, AGENTS.md
rows. Gate: the usual checks, `make test-e2e` with `switchyard.spec.ts`,
`make desktop-test`; by hand: two apps on two networks connected through a
switchyard on a VPS, one of them from WSL in NAT mode.

## Order

R6-1 → R6-2 (Go, then desktop) → R6-3 (protocol, web, desktop) → R6-4.
One commit per task with the subjects:
`serve: switchyard, a Conductor that coordinates hosted sessions and launches nothing`,
`hostagent: ICE on one UDP port with the address a forwarder gives it`,
`desktop: a UDP forwarder on Windows carries ICE into WSL, with the firewall rule from the installer`,
`host: a published session mints its links on the rendezvous, as a link and as an invite`,
`join: an invite opens in the app, which signals to switchyard from its own page`,
`docs: sharing through switchyard`.
