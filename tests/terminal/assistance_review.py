#!/usr/bin/env python3
"""Actual signed API/PostgreSQL assistance journey through compiled CLI PTYs."""

import copy
import http.server
import json
import os
from pathlib import Path
import socket
import threading
import urllib.error
import urllib.parse
import urllib.request

from authenticated import ProtectedTerminal
from review import OPENER


class AssistanceProxy:
    def __init__(self, api, tokens):
        self.requests = []
        self.lock = threading.Lock()
        self.drop_path = None
        owner = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_GET(self):
                self.forward()

            def do_POST(self):
                self.forward()

            def forward(self):
                length = int(self.headers.get("Content-Length", "0"))
                if length > 1 << 20:
                    self.send_error(413)
                    return
                body = self.rfile.read(length) if length else None
                bearer = self.headers.get("Authorization", "")
                observed = {"method": self.command, "path": self.path,
                            "body": json.loads(body) if body else None, "status": None,
                            "key": self.headers.get("Idempotency-Key", ""),
                            "identity": next((name for name, token in tokens.items()
                                              if bearer == "Bearer " + token), "unknown")}
                with owner.lock:
                    owner.requests.append(observed)
                headers = {"Authorization": bearer, "Content-Type": "application/json"}
                for name in ("X-Conductor-Workspace", "X-Conductor-Repository", "Idempotency-Key"):
                    if name in self.headers:
                        headers[name] = self.headers[name]
                request = urllib.request.Request(api + self.path, data=body, method=self.command, headers=headers)
                try:
                    try:
                        response = OPENER.open(request, timeout=10)
                    except urllib.error.HTTPError as error:
                        response = error
                    with response:
                        data, status = response.read((4 << 20) + 1), response.code
                    assert len(data) <= 4 << 20
                    with owner.lock:
                        observed["status"] = status
                        drop = owner.drop_path == self.path and self.command == "POST" and status == 201
                        if drop:
                            owner.drop_path = None
                    if drop:
                        self.close_connection = True
                        self.connection.shutdown(socket.SHUT_RDWR)
                        return
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(data)))
                    self.end_headers()
                    self.wfile.write(data)
                except (OSError, ValueError):
                    self.close_connection = True

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = "http://127.0.0.1:" + str(self.server.server_address[1])

    def snapshot(self):
        with self.lock:
            return copy.deepcopy(self.requests)

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)


def main():
    api = os.environ["CONDUCTOR_API_URL"].rstrip("/")
    control = os.environ["CONDUCTOR_TERMINAL_CONTROL_URL"].rstrip("/")
    binary = str(Path(os.environ["CONDUCTOR_BIN"]).resolve(strict=True))
    files = {name: Path(path) for name, path in json.loads(os.environ["CONDUCTOR_TERMINAL_CREDENTIAL_FILES"]).items()}
    tokens = {name: path.read_text().rstrip("\n") for name, path in files.items()}
    package = json.loads(os.environ["CONDUCTOR_TERMINAL_ASSISTANCE_SETUP"])
    for base in (api, control):
        parsed = urllib.parse.urlsplit(base)
        assert parsed.scheme == "http" and parsed.hostname == "127.0.0.1"
    proxy = AssistanceProxy(api, tokens)
    terminals = []

    def command(path, body=None, identity="author", key=None):
        headers = {"Content-Type": "application/json", "Authorization": "Bearer " + tokens[identity],
                   "X-Conductor-Workspace": "team", "X-Conductor-Repository": "application"}
        if key:
            headers["Idempotency-Key"] = key
        request = urllib.request.Request(api + "/api/v1" + path,
                                         data=None if body is None else json.dumps(body).encode(), headers=headers)
        with OPENER.open(request, timeout=10) as response:
            return json.load(response)

    def launch(identity):
        terminal = ProtectedTerminal(binary, proxy.url,
            ["--workspace", "team", "--repository-id", "application", package["id"]], files[identity], tokens)
        terminals.append(terminal)
        terminal.wait_text("Loaded latest revision")
        terminal.wait_text("_______ /")
        terminal.wait_text("Engineering intent, orchestrated.")
        terminal.settle()
        return terminal

    def press(terminal, key, text):
        start = len(terminal.output)
        terminal.send(key)
        terminal.wait_text(text, start)
        terminal.settle()

    def confirm(terminal, action, expected, key="s"):
        since = len(proxy.snapshot())
        press(terminal, key, "Type " + action)
        press(terminal, action + "\r", expected)
        calls = proxy.snapshot()[since:]
        assert len(calls) == 1 and calls[0]["method"] == "POST", ("confirmation refreshed or replayed", calls)
        return calls[0]

    def refresh(terminal, expected="Assistance inspected"):
        since = len(proxy.snapshot())
        press(terminal, "r", expected)
        calls = proxy.snapshot()[since:]
        assert [c["path"] for c in calls[:2]] == ["/api/v1/session", "/api/v1/repositories"], calls
        assert calls[2]["path"] == "/api/v1/changes/" + package["id"], calls
        return calls

    def no_command(terminal, keys):
        before = len(proxy.snapshot())
        for key in keys:
            terminal.send(key)
            terminal.settle()
        assert len(proxy.snapshot()) == before, ("unavailable control sent request", keys)

    def form(terminal, instruction):
        press(terminal, "c", "FORM: Request Design assistance")
        # Numeric selection avoids constructing JSON. Scope is structured and
        # deliberately remains read-only even when 3 is pressed.
        terminal.send("2")
        terminal.settle()
        terminal.send("3")
        terminal.settle()
        terminal.send("4")
        terminal.settle()
        for _ in range(6):
            terminal.send("\t")
            terminal.settle()
        terminal.send("\r")
        terminal.settle()
        terminal.send(instruction)
        terminal.settle()
        press(terminal, "\x13", "Request preview ready")

    def operator(action):
        with OPENER.open(urllib.request.Request(control + "/" + action, method="POST"), timeout=10) as response:
            assert response.status == 200

    try:
        author = launch("author")
        press(author, "h", "Shared assistance requests loaded")
        form(author, "Improve the outcome and Design without changing legacy fields")
        proxy.drop_path = "/api/v1/design-assistance"
        first = confirm(author, "assist-request", "Assistance write not confirmed")
        assert first["status"] == 201 and first["key"]
        assert first["body"] == {"changeId": package["id"], "expectedRevision": 1,
            "expectedDigest": package["revision"]["digest"],
            "instruction": "Improve the outcome and Design without changing legacy fields",
            "sections": ["intent", "design"]}, first
        no_command(author, "sv")
        refresh(author, "Shared assistance requests loaded")
        press(author, "v", "Retained command preview")
        recovered = confirm(author, "assist-request", "Assistance request saved")
        assert recovered["key"] == first["key"] and recovered["body"] == first["body"]
        page = command("/design-assistance?changeId=" + package["id"])
        assert len(page["requests"]) == 1
        request = command("/design-assistance/" + page["requests"][0]["id"])
        author.wait_text("conductor_propose_design_sections")
        # Scroll retained content to prove full inspection beyond the handoff.
        press(author, "\x1b[F", "futureExtension")
        author.send("\x1b[H")
        author.settle()
        assert command("/changes/" + package["id"])["revision"]["number"] == 1
        proposal = {"requestDigest": request["digest"], "sections": {
            "intent": "Proposed outcome intentionally not applied", "design": "Reviewed synthetic approach"},
            "note": "Synthetic unverified proposal; checks have not run."}
        request = command("/design-assistance/" + request["id"] + "/suggestion", proposal, "agent", "terminal-agent-proposal")
        refresh(author)
        # Deselect intent, preview and inspect the entire selected merge.
        author.send("2")
        author.settle()
        press(author, "a", "Application preview ready")
        press(author, "\x1b[F", "Reviewed synthetic approach")
        assert "futureExtension" in author.text()
        proxy.drop_path = "/api/v1/design-assistance/" + request["id"] + "/application"
        first_apply = confirm(author, "assist-apply", "Assistance write not confirmed")
        assert first_apply["body"] == {"requestDigest": request["digest"],
            "suggestionDigest": request["suggestion"]["digest"], "expectedRevision": 1,
            "expectedDigest": package["revision"]["digest"], "sections": ["design"]}
        refresh(author)
        press(author, "v", "Retained command preview")
        recovered_apply = confirm(author, "assist-apply", "Applied as revision 2")
        assert recovered_apply["key"] == first_apply["key"] and recovered_apply["body"] == first_apply["body"]
        changed = command("/changes/" + package["id"])
        assert changed["revision"]["number"] == 2 and changed["revision"]["author"] == "person-author" and not changed["approved"]
        assert changed["revision"]["content"] == {**package["revision"]["content"], "design": "Reviewed synthetic approach"}
        no_command(author, "as")
        refresh(author)
        press(author, "h", "Returned to Change")
        confirm(author, "submit", "submit recorded", "u")
        no_command(author, "a")
        reviewer = launch("reviewer")
        confirm(reviewer, "approve", "Design approval recorded", "a")
        assert command("/changes/" + package["id"])["approved"]

        # An author can inspect another human's suggestion but cannot apply it.
        press(reviewer, "h", "Shared assistance requests loaded")
        press(reviewer, "\r", "Assistance inspected")
        no_command(reviewer, "as")
        for identity in ("reader", "agent"):
            terminal = launch(identity)
            press(terminal, "h", "Shared assistance requests loaded")
            press(terminal, "\r", "Assistance inspected")
            no_command(terminal, "cas")

        press(author, "h", "Shared assistance requests loaded")
        form(author, "A private instruction that must disappear on denied access")
        operator("revoke")
        denied = confirm(author, "assist-request", "Access unavailable")
        assert denied["status"] == 403
        no_command(author, "csavh")
        operator("restore")
        refresh(author, "Loaded latest revision")
        press(author, "h", "Shared assistance requests loaded")
        press(author, "v", "No uncertain assistance command")
        # A different human cannot apply an unapplied suggestion. A concurrent
        # edit makes the author's already-confirmable proposal stale, without
        # inserting any read into its application command.
        current = command("/changes/" + package["id"])
        stale_request = command("/design-assistance", {
            "changeId": package["id"], "expectedRevision": current["revision"]["number"],
            "expectedDigest": current["revision"]["digest"], "instruction": "Review a stale Design proposal",
            "sections": ["design"]}, key="terminal-stale-request")
        command("/design-assistance/" + stale_request["id"] + "/suggestion", {
            "requestDigest": stale_request["digest"], "sections": {"design": "Stale proposed text"}},
            "agent", "terminal-stale-proposal")
        press(reviewer, "b", "Shared assistance requests loaded")
        press(reviewer, "\r", "Assistance inspected")
        no_command(reviewer, "as")
        press(author, "b", "Shared assistance requests loaded")
        press(author, "\r", "Assistance inspected")
        press(author, "a", "Application preview ready")
        command("/changes/" + package["id"] + "/revisions", {
            "expectedRevision": current["revision"]["number"],
            "content": {**current["revision"]["content"], "concurrentExtension": True}}, "agent")
        rejected = confirm(author, "assist-apply", "Assistance command rejected")
        assert rejected["status"] == 409 and rejected["body"]["expectedRevision"] == 2
        no_command(author, "as")
        refresh(author)
        press(author, "c", "FORM: Request Design assistance")
        press(author, "\x1b", "Request form discarded")
        # Normal 80x24 terminals keep a readable C and a tiny junction. Very small
        # terminals prioritize exact inspection using a plain heading.
        since = len(author.output)
        author.resize(80, 24)
        author.wait_text("_______ /", since)
        author.settle()
        assert "/___/" not in author.text(since)
        Path("/tmp/conductor-terminal-assistance-compact.txt").write_text(author.text(since))
        since = len(author.output)
        author.resize(50, 23)
        author.wait_text("Conductor | principal", since)
        assert "_______ /" not in author.text(since)
        author.resize(121, 46)
        author.settle()
        since = len(author.output)
        author.resize(120, 45)
        author.wait_text("_______ /", since)
        author.settle()
        Path("/tmp/conductor-terminal-assistance-brand.txt").write_text(author.text(since))
        for terminal in terminals:
            assert all(token.encode() not in terminal.output for token in tokens.values())
            terminal.send("q")
            terminal.wait(lambda: terminal.process.poll() is not None, "terminal did not exit")
            assert terminal.process.returncode == 0
        print("PASS assistance PTYs: no-JSON request, native handoff, selected merge, exact lost-response recovery, independent approval, reader/agent/requester restrictions, denial/recovery, Switch ASCII and compact fallback")
    finally:
        for terminal in terminals:
            terminal.close()
        proxy.close()


if __name__ == "__main__":
    main()
