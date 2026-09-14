#!/usr/bin/env python3
"""Run through TestAuthenticatedTerminalWorkbench, using only synthetic identity.

The compiled CLI communicates with the real authenticated API and PostgreSQL.
The recording proxy retains credential labels, never bearer values. Linux PTYs
and the Python standard library are sufficient; no identity vendor is simulated
by the terminal or substituted for the API's signed-token verification.
"""

import copy
import http.server
import json
import os
from pathlib import Path
import re
import tempfile
import threading
import urllib.error
import urllib.parse
import urllib.request

from review import OPENER, Terminal, fill_design


class AuthenticatedProxy:
    """Forward real credentials, recording only safe synthetic identity labels."""

    def __init__(self, api, tokens):
        self.requests = []
        self.lock = threading.Lock()
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
                credential = next((name for name, token in tokens.items()
                                   if bearer == "Bearer " + token), "unrecognized")
                observed = {
                    "method": self.command, "path": self.path,
                    "body": None if body is None else json.loads(body), "status": None,
                    "credential": credential,
                    "workspace": self.headers.get("X-Conductor-Workspace", ""),
                    "repository": self.headers.get("X-Conductor-Repository", ""),
                    "localActor": "X-Conductor-Actor" in self.headers,
                }
                with owner.lock:
                    owner.requests.append(observed)
                # The proxy neither derives actor identity nor rewrites scope.
                # The real API rejects any incorrect or missing credential.
                headers = {"Content-Type": "application/json", "Authorization": bearer}
                for name in ("X-Conductor-Workspace", "X-Conductor-Repository", "X-Conductor-Actor"):
                    if name in self.headers:
                        headers[name] = self.headers[name]
                request = urllib.request.Request(api + self.path, data=body,
                                                 method=self.command, headers=headers)
                status = 502
                try:
                    try:
                        response = OPENER.open(request, timeout=10)
                    except urllib.error.HTTPError as error:
                        response = error
                    with response:
                        data = response.read((2 << 20) + 1)
                        status = response.code
                        correlation = response.headers.get("X-Correlation-ID")
                    if len(data) > 2 << 20:
                        raise ValueError("oversized fixture response")
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(data)))
                    if correlation:
                        self.send_header("X-Correlation-ID", correlation)
                    self.end_headers()
                    self.wfile.write(data)
                except Exception as error:
                    # Exception text can contain transport details. Retain only
                    # its class; the fixture must never log credentials.
                    observed["proxyError"] = type(error).__name__
                    self.send_error(502, "Synthetic proxy transport failed")
                finally:
                    with owner.lock:
                        observed["status"] = status

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


class ProtectedTerminal(Terminal):
    def __init__(self, binary, proxy_url, args, token_file, tokens):
        self.tokens = tokens
        super().__init__(binary, proxy_url, args, environment_overrides={
            "CONDUCTOR_TOKEN_FILE": str(token_file), "CONDUCTOR_TOKEN": None,
            "CONDUCTOR_ACTOR": None, "CONDUCTOR_WORKSPACE": None,
            "CONDUCTOR_REPOSITORY_ID": None,
        })

    def text(self, start=0):
        # Raw output is checked separately. If the product ever leaks a token,
        # failure diagnostics still must not repeat it into the test log.
        text = super().text(start)
        for token in self.tokens.values():
            text = text.replace(token, "[redacted credential]")
        return text


def main():
    api = os.environ["CONDUCTOR_API_URL"].rstrip("/")
    control = os.environ["CONDUCTOR_TERMINAL_CONTROL_URL"].rstrip("/")
    binary = str(Path(os.environ["CONDUCTOR_BIN"]).resolve(strict=True))
    files = {name: Path(path) for name, path in json.loads(
        os.environ["CONDUCTOR_TERMINAL_CREDENTIAL_FILES"]).items()}
    tokens = {name: path.read_text().rstrip("\n") for name, path in files.items()}
    for base in (api, control):
        parsed = urllib.parse.urlsplit(base)
        assert parsed.scheme == "http" and parsed.hostname == "127.0.0.1", "requires isolated loopback fixture"
    proxy = AuthenticatedProxy(api, tokens)
    terminals = []

    def command(path, identity="author", body=None):
        request = urllib.request.Request(api + "/api/v1" + path,
            data=None if body is None else json.dumps(body).encode(),
            headers={"Content-Type": "application/json", "Authorization": "Bearer " + tokens[identity],
                     "X-Conductor-Workspace": "team", "X-Conductor-Repository": "application"})
        with OPENER.open(request, timeout=10) as response:
            return json.load(response)

    def operator(action):
        request = urllib.request.Request(control + "/" + action, method="POST")
        with OPENER.open(request, timeout=10) as response:
            assert response.status == 200

    def launch(identity, args):
        terminal = ProtectedTerminal(binary, proxy.url,
            ["--workspace", "team", "--repository-id", "application", *args], files[identity], tokens)
        terminals.append(terminal)
        return terminal

    def observed(terminal, since, method, path, body, status, identity):
        terminal.wait(lambda: any(item["status"] is not None for item in proxy.snapshot()[since:]),
                      "authenticated terminal request did not finish")
        terminal.settle()
        requests = proxy.snapshot()[since:]
        assert len(requests) == 1, ("command refreshed or replayed unexpectedly", requests)
        assert requests[0] == {"method": method, "path": "/api/v1" + path, "body": body,
                              "status": status, "credential": identity, "workspace": "team",
                              "repository": "application", "localActor": False}, requests

    def refresh(terminal, identity, digest=None, retained=False):
        since = len(proxy.snapshot())
        terminal.send("r")
        terminal.wait(lambda: len(proxy.snapshot()[since:]) >= 3
                      and all(item["status"] is not None for item in proxy.snapshot()[since:]),
                      "authenticated refresh did not complete its three reads")
        # A fast refresh can finish between renderer ticks and leave the exact
        # same view. Bubble Tea correctly suppresses that redundant frame. Force
        # a complete render so the assertions inspect the current model without
        # depending on a transient loading frame or matching old screen history.
        start = len(terminal.output)
        terminal.resize(121, 46)
        terminal.wait_text("Saved work loaded" if retained else "Loaded latest revision" if digest else "Shared work loaded", start)
        if digest:
            terminal.wait_text(digest, start)
        terminal.resize(120, 45)
        terminal.settle()
        requests = proxy.snapshot()[since:]
        assert [item["path"] for item in requests[:2]] == ["/api/v1/session", "/api/v1/repositories"], requests
        assert len(requests) == 3 and all(item["method"] == "GET" for item in requests), requests
        assert all(item["credential"] == identity and item["status"] == 200 for item in requests), requests

    def open_change(terminal, change_id):
        terminal.send("o")
        terminal.wait_text("Package ID")
        terminal.send(change_id + "\r")

    def blocked_without_request(terminal, keys):
        before = len(proxy.snapshot())
        terminal.send(keys)
        terminal.settle()
        assert len(proxy.snapshot()) == before, "blocked action sent a request"

    def assert_cleared(terminal, change_id):
        # Force a fresh complete render, since PTY output keeps previous screen
        # history and Bubble Tea may otherwise transmit only changed rows.
        start = len(terminal.output)
        terminal.resize(121, 46)
        terminal.settle()
        current = terminal.text(start)
        assert "ID: " + change_id not in current, "denied inspection stayed on screen"
        assert "PREVIEW:" not in current, "denied preview stayed on screen"
        assert "Access not confirmed" in current, "denied capabilities stayed on screen"
        assert "Read: allowed" not in current, "denied read permission stayed on screen"
        terminal.resize(120, 45)
        terminal.settle()

    content = {"intent": {"request": "Synthetic authenticated terminal review"},
               "futureExtension": {"retain": ["unknown", True, None]},
               "untrustedText": "\x1b]52;c;ZGF0YQ==\x07",
               "verification": {"state": "unexecuted", "required": ["synthetic check"]}}
    try:
        guided = launch("author", [])
        guided.wait_text("Shared work loaded")
        guided_content = {"title": "Authenticated guided change", "intent": "Draft without a JSON file"}
        before = len(proxy.snapshot())
        fill_design(guided, guided_content)
        assert len(proxy.snapshot()) == before, "guided draft contacted the API"
        guided.send("s")
        guided.wait_text("Type create")
        before = len(proxy.snapshot())
        guided.send("create\r")
        observed(guided, before, "POST", "/changes", {"content": guided_content}, 201, "author")
        guided.wait_text("create recorded")
        guided_id = re.search(r"ID: (CHG-[0-9a-f]+)", guided.text()).group(1)
        guided_path = "/changes/" + guided_id
        assert command(guided_path)["revision"]["author"] == "person-author"
        # An existing structured field must remain read-only in the form and
        # survive a neighboring string edit without coercion or dropped data.
        retained = {**guided_content, "scope": None, "tasks": ["keep structured task"],
                    "futureExtension": {"retain": [True, None]}}
        existing = command(guided_path + "/revisions", body={"expectedRevision": 1, "content": retained})
        refresh(guided, "author", existing["revision"]["digest"])
        before = len(proxy.snapshot())
        guided.send("e")
        guided.wait_text("FORM: Edit Design")
        guided.send("\t\t\r")
        guided.wait_text("Structured value: preserved")
        guided.send("\x1b[Z\x1b[Z\r\x15Updated guided title\x13")
        guided.wait_text("PREVIEW: revise")
        assert len(proxy.snapshot()) == before, "editing refreshed its inspected revision"
        guided.send("s")
        guided.wait_text("Type revise")
        updated_guided = {**retained, "title": "Updated guided title"}
        concurrent_guided = {**retained, "verification": "A concurrently added verification plan"}
        saved_concurrent = command(guided_path + "/revisions",
            body={"expectedRevision": 2, "content": concurrent_guided})
        before = len(proxy.snapshot())
        guided.send("revise\r")
        observed(guided, before, "POST", guided_path + "/revisions",
                 {"expectedRevision": 2, "content": updated_guided}, 409, "author")
        guided.wait_text("Stale inspection")
        refresh(guided, "author", saved_concurrent["revision"]["digest"], retained=True)
        before = len(proxy.snapshot())
        guided.send("v")
        guided.wait_text("RECOVERED INPUT")
        assert len(proxy.snapshot()) == before, "recovery silently read or retried"
        guided.send("s")
        guided.wait_text("Type revise")
        before = len(proxy.snapshot())
        guided.send("revise\r")
        observed(guided, before, "POST", guided_path + "/revisions",
                 {"expectedRevision": 3, "content": updated_guided}, 201, "author")
        assert command(guided_path)["revision"]["content"] == updated_guided
        with tempfile.TemporaryDirectory(prefix="conductor-authenticated-terminal-") as directory:
            source = Path(directory) / "package.json"
            source.write_text(json.dumps(content))
            before = len(proxy.snapshot())
            writer = launch("author", ["--file", str(source)])
            writer.wait_text("Shared work loaded")
            writer.wait_text("principal: person-author (human)")
            writer.wait_text("Workspace: team | Repository ID: application")
            writer.wait_text("Read: allowed | Author: allowed | Approve: allowed after independent inspection")
            writer.settle()
            startup = proxy.snapshot()[before:]
            assert [request["path"].split("?")[0] for request in startup] == [
                "/api/v1/session", "/api/v1/repositories", "/api/v1/changes"], startup
            writer.send("i")
            writer.wait_text("JSON file path")
            writer.send("\r")
            writer.wait_text("PREVIEW: create")
            assert len(proxy.snapshot()) == before + 3, "file preview contacted the API"
            writer.send("s")
            writer.wait_text("Type create")
            before = len(proxy.snapshot())
            writer.send("create\r")
            observed(writer, before, "POST", "/changes", {"content": content}, 201, "author")
            writer.wait_text("create recorded")
            match = re.search(r"ID: (CHG-[0-9a-f]+)", writer.text())
            assert match, "created package ID was not displayed"
            change_id = match.group(1)
            path = "/changes/" + change_id
            created = command(path)
            assert created["revision"]["content"] == content
            assert created["revision"]["author"] == "person-author"
            assert (created["workspaceId"], created["repositoryId"]) == ("team", "application")
            writer.send("u")
            writer.wait_text("Type submit")
            before = len(proxy.snapshot())
            writer.send("submit\r")
            observed(writer, before, "POST", path + "/review-requests", {"revision": 1}, 200, "author")
            writer.wait_text("submit recorded")
            blocked_without_request(writer, "a")

            reviewer = launch("reviewer", [change_id])
            reviewer.wait_text("Loaded latest revision")
            reviewer.wait_text("principal: person-reviewer (human)")
            reviewer.wait_text(created["revision"]["digest"])
            reviewer.settle()
            agent = launch("agent", [change_id])
            agent.wait_text("Loaded latest revision")
            agent.wait_text("principal: person-agent (agent)")
            agent.wait_text("Read: allowed | Author: allowed | Approve: not granted")
            agent.settle()
            start = len(agent.output)
            blocked_without_request(agent, "a")
            agent.wait_text("Action blocked:", start)
            read_only = launch("reader", ["--file", str(source), change_id])
            read_only.wait_text("Loaded latest revision")
            read_only.wait_text("Read: allowed | Author: not granted | Approve: not granted")
            read_only.settle()
            for key in "aceui":
                blocked_without_request(read_only, key)
                read_only.wait_text("Action blocked:")

            # A running process keeps its initial credential. Replacing its file
            # must not let the next refresh or approval silently change identity.
            files["reviewer"].write_text(tokens["author"] + "\n")
            refresh(reviewer, "reviewer", created["revision"]["digest"])
            reviewer.wait_text("principal: person-reviewer (human)")
            updated = copy.deepcopy(content)
            updated["intent"]["request"] = "Another engineer changed the shared revision"
            changed = command(path + "/revisions", body={"expectedRevision": 1, "content": updated})
            command(path + "/review-requests", body={"revision": 2})
            reviewer.send("a")
            reviewer.wait_text("Type approve")
            before = len(proxy.snapshot())
            reviewer.send("approve\r")
            observed(reviewer, before, "POST", path + "/approvals",
                     {"revision": 1, "digest": created["revision"]["digest"]}, 409, "reviewer")
            reviewer.wait_text("Stale inspection")
            blocked_without_request(reviewer, "a")
            refresh(reviewer, "reviewer", changed["revision"]["digest"])
            reviewer.send("a")
            reviewer.settle()
            before = len(proxy.snapshot())
            reviewer.send("approve\r")
            observed(reviewer, before, "POST", path + "/approvals",
                     {"revision": 2, "digest": changed["revision"]["digest"]}, 201, "reviewer")
            reviewer.wait_text("Design approval recorded")
            approved = command(path)
            assert approved["approved"] and approved["approval"]["reviewer"] == "person-reviewer"

            replacement = copy.deepcopy(updated)
            replacement["intent"]["request"] = "A later terminal file revision requires new approval"
            source.write_text(json.dumps(replacement))
            refresh(writer, "author", changed["revision"]["digest"])
            writer.send("i")
            writer.settle()
            writer.send("\r")
            writer.wait_text("PREVIEW: revise")
            writer.send("s")
            writer.wait_text("Type revise")
            before = len(proxy.snapshot())
            writer.send("revise\r")
            observed(writer, before, "POST", path + "/revisions",
                     {"expectedRevision": 2, "content": replacement}, 201, "author")
            writer.wait_text("revise recorded")
            latest = command(path)
            assert latest["revision"]["number"] == 3 and not latest["approved"]
            assert latest["revision"]["content"] == replacement
            historical = command(path + "/revisions/2")
            assert historical["approvals"] == [approved["approval"]]
            writer.send("u")
            writer.settle()
            before = len(proxy.snapshot())
            writer.send("submit\r")
            observed(writer, before, "POST", path + "/review-requests", {"revision": 3}, 200, "author")
            refresh(reviewer, "reviewer", latest["revision"]["digest"])

            # Permission can change after inspection. The consequential command
            # must reach the real service, fail, and clear previously held access.
            operator("reviewer-read-only")
            reviewer.send("a")
            reviewer.settle()
            before = len(proxy.snapshot())
            start = len(reviewer.output)
            reviewer.send("approve\r")
            observed(reviewer, before, "POST", path + "/approvals",
                     {"revision": 3, "digest": latest["revision"]["digest"]}, 403, "reviewer")
            reviewer.wait_text("Access unavailable:", start)
            assert_cleared(reviewer, change_id)
            blocked_without_request(reviewer, "aces")
            refresh(reviewer, "reviewer", latest["revision"]["digest"])
            start = len(reviewer.output)
            blocked_without_request(reviewer, "a")
            reviewer.wait_text("Action blocked:", start)
            operator("reviewer-full-grant")
            refresh(reviewer, "reviewer", latest["revision"]["digest"])

            # An ordinary package read observes revoked workspace membership,
            # while a later explicit reload rechecks server capabilities first.
            operator("reviewer-no-membership")
            before = len(proxy.snapshot())
            start = len(reviewer.output)
            open_change(reviewer, change_id)
            observed(reviewer, before, "GET", path, None, 403, "reviewer")
            reviewer.wait_text("Access unavailable:", start)
            assert_cleared(reviewer, change_id)
            blocked_without_request(reviewer, "aces")
            operator("reviewer-membership")
            refresh(reviewer, "reviewer", latest["revision"]["digest"])
            operator("reviewer-inactive")
            before = len(proxy.snapshot())
            start = len(reviewer.output)
            open_change(reviewer, change_id)
            observed(reviewer, before, "GET", path, None, 401, "reviewer")
            reviewer.wait_text("Access unavailable:", start)
            assert_cleared(reviewer, change_id)
            before = len(proxy.snapshot())
            reviewer.send("r")
            observed(reviewer, before, "GET", "/session", None, 401, "reviewer")
            blocked_without_request(reviewer, "aces")

            for terminal in terminals:
                for token in tokens.values():
                    assert token.encode() not in terminal.output, "credential leaked into terminal output"
                assert b"\x1b]52;c;ZGF0YQ==\x07" not in terminal.output, "package content controlled the terminal"
                terminal.send("q")
                terminal.wait(lambda: terminal.process.poll() is not None, "terminal did not exit")
                assert terminal.process.returncode == 0, "terminal exited unsuccessfully"
            recorded = json.dumps(proxy.snapshot())
            assert all(token not in recorded for token in tokens.values()), "credential entered proxy records"
            assert all(not request["localActor"] and request["credential"] != "unrecognized"
                       and request["workspace"] == "team" and request["repository"] == "application"
                       for request in proxy.snapshot()), "authentication or scope was not forwarded"
            print("PASS: authenticated guided form create/edit, structured field preservation, retained-input recovery, "
                  "advanced file create/revise, separate save/submit, shared inspection, "
                  "stale approval without GET, independent approval, agent denial, grant/membership/principal "
                  "revocation, explicit capability refresh, fixed token identity, no credential output, and clean exit")
    finally:
        for terminal in reversed(terminals):
            terminal.close()
        proxy.close()


if __name__ == "__main__":
    main()
