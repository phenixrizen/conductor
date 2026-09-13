#!/usr/bin/env python3
"""Actual signed-issuer/PostgreSQL collection acceptance through Linux PTYs.

The recording proxy forwards real commands, including idempotency keys. Its one
lost-response case closes the socket after a real committed request, proving the
terminal retains the same explicit retry rather than silently creating new work.
Provider and Temporal execution are covered in their separate acceptance suites.
"""

import copy
import http.server
import json
import os
from pathlib import Path
import socket
import tempfile
import threading
import urllib.error
import urllib.parse
import urllib.request

from authenticated import ProtectedTerminal
from review import OPENER


class CollectionProxy:
    def __init__(self, api, tokens):
        self.requests = []
        self.lock = threading.Lock()
        self.drop_next_create = False
        self.delay_next_read = 0
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
                label = next((name for name, token in tokens.items()
                              if bearer == "Bearer " + token), "unrecognized")
                observed = {"method": self.command, "path": self.path,
                            "body": json.loads(body) if body else None,
                            "status": None, "identity": label,
                            "key": self.headers.get("Idempotency-Key", ""),
                            "workspace": self.headers.get("X-Conductor-Workspace", ""),
                            "repository": self.headers.get("X-Conductor-Repository", ""),
                            "localActor": "X-Conductor-Actor" in self.headers}
                with owner.lock:
                    owner.requests.append(observed)
                    delay = owner.delay_next_read if self.command == "GET" else 0
                    if self.command == "GET":
                        owner.delay_next_read = 0
                if delay:
                    threading.Event().wait(delay)
                headers = {"Content-Type": "application/json", "Authorization": bearer}
                for name in ("X-Conductor-Workspace", "X-Conductor-Repository", "Idempotency-Key"):
                    if name in self.headers:
                        headers[name] = self.headers[name]
                request = urllib.request.Request(api + self.path, data=body,
                                                 method=self.command, headers=headers)
                try:
                    try:
                        response = OPENER.open(request, timeout=10)
                    except urllib.error.HTTPError as error:
                        response = error
                    with response:
                        data = response.read((2 << 20) + 1)
                        status = response.code
                    assert len(data) <= 2 << 20
                    with owner.lock:
                        observed["status"] = status
                        drop = (owner.drop_next_create and self.command == "POST"
                                and self.path == "/api/v1/context-collections" and status == 202)
                        if drop:
                            owner.drop_next_create = False
                    if drop:
                        self.close_connection = True
                        self.connection.shutdown(socket.SHUT_RDWR)
                        return
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(data)))
                    self.end_headers()
                    self.wfile.write(data)
                except (OSError, ValueError) as error:
                    with owner.lock:
                        observed["proxyError"] = type(error).__name__
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
    files = {name: Path(path) for name, path in json.loads(
        os.environ["CONDUCTOR_TERMINAL_CREDENTIAL_FILES"]).items()}
    tokens = {name: path.read_text().rstrip("\n") for name, path in files.items()}
    setup = json.loads(os.environ["CONDUCTOR_TERMINAL_COLLECTION_SETUP"])
    for base in (api, control):
        parsed = urllib.parse.urlsplit(base)
        assert parsed.scheme == "http" and parsed.hostname == "127.0.0.1"
    proxy = CollectionProxy(api, tokens)
    terminals = []

    def command(path, body=None, identity="author"):
        request = urllib.request.Request(api + "/api/v1" + path,
            data=None if body is None else json.dumps(body).encode(),
            headers={"Content-Type": "application/json", "Authorization": "Bearer " + tokens[identity],
                     "X-Conductor-Workspace": "team", "X-Conductor-Repository": "application"})
        with OPENER.open(request, timeout=10) as response:
            return json.load(response)

    def operator(action):
        with OPENER.open(urllib.request.Request(control + "/" + action, method="POST"), timeout=10) as response:
            assert response.status == 200

    def launch(identity, package=None):
        args = ["--workspace", "team", "--repository-id", "application"]
        if package:
            args += [package]
        terminal = ProtectedTerminal(binary, proxy.url, args, files[identity], tokens)
        terminals.append(terminal)
        terminal.wait_text("Loaded latest revision" if package else "Shared work loaded")
        return terminal

    def open_collection(terminal, identifier):
        start = len(terminal.output)
        terminal.send("o")
        terminal.wait_text("Collection ID", start)
        terminal.send(identifier + "\r")
        terminal.wait_text("Collection inspected", start)
        terminal.wait_text(identifier, start)
        terminal.settle()

    def navigate_page(terminal, key, number):
        since = len(proxy.snapshot())
        # A page footer changes before the response arrives. Deliberately keep
        # this read slower than settle() so headers/history cannot prove ready.
        with proxy.lock:
            proxy.delay_next_read = 0.8
        terminal.send(key)
        terminal.wait(lambda: len(proxy.snapshot()) > since
                      and all(item["status"] is not None for item in proxy.snapshot()[since:]),
                      "collection page request did not finish")
        requests = proxy.snapshot()[since:]
        assert len(requests) == 1 and requests[0]["method"] == "GET" and requests[0]["status"] == 200, requests
        assert requests[0]["path"].startswith("/api/v1/context-collections?"), requests
        # Force a current frame after the captured read. This also handles a
        # fast response coalescing with a redraw; old page text cannot satisfy it.
        start = len(terminal.output)
        terminal.resize(121, 46)
        terminal.wait_text("Shared collections loaded", start)
        terminal.wait_text("Collection page " + str(number), start)
        terminal.resize(120, 45)
        terminal.settle()
        return requests[0]

    def send_confirmation(terminal, key, action):
        start = len(terminal.output)
        terminal.send(key)
        terminal.wait_text("Type " + action, start)
        terminal.send(action + "\r")
        return start

    def only_post(terminal, since, path, body, status, key=""):
        terminal.wait(lambda: len(proxy.snapshot()) > since and proxy.snapshot()[-1]["status"] is not None,
                      "collection command did not finish")
        terminal.settle()
        observed = proxy.snapshot()[since:]
        assert len(observed) == 1, ("command refreshed or replayed", observed)
        item = observed[0]
        assert item["method"] == "POST" and item["path"] == "/api/v1" + path, item
        assert item["body"] == body and item["status"] == status and item["key"] == key, item
        assert item["workspace"] == "team" and item["repository"] == "application" and not item["localActor"], item

    def no_command(terminal, keys):
        before = len(proxy.snapshot())
        terminal.send(keys)
        terminal.settle()
        assert len(proxy.snapshot()) == before, ("blocked key sent a request", keys)

    def load_request(terminal, path):
        start = len(terminal.output)
        terminal.send("c")
        terminal.wait_text("JSON request file path", start)
        terminal.send(str(path) + "\r")
        terminal.wait_text("Request file loaded for preview", start)
        terminal.wait_text("PREVIEW: exact collection request", start)
        terminal.settle()

    pkg = setup["package"]
    shared = setup["collection"]
    receipt = setup["receipt"]
    try:
        reviewer = launch("reviewer", pkg["id"])
        start = len(reviewer.output)
        reviewer.send("g")
        reviewer.wait_text("Shared collections loaded", start)
        assert "before=" in navigate_page(reviewer, "n", 2)["path"]
        assert "before=" not in navigate_page(reviewer, "p", 1)["path"]
        open_collection(reviewer, shared["id"])
        reviewer.wait_text("Coverage: docs/missing.md | missing")
        reviewer.wait_text("unknown: no execution observation")
        no_command(reviewer, "aesx")  # package approval cannot leak into collection mode; requester differs
        changed = command("/changes/" + pkg["id"] + "/revisions",
                          {"expectedRevision": 1, "content": {**pkg["revision"]["content"], "concurrent": True}})
        since = len(proxy.snapshot())
        start = send_confirmation(reviewer, "t", "attach")
        reviewer.wait_text("Stale inspection", start)
        only_post(reviewer, since, "/changes/" + pkg["id"] + "/context-attachments",
                  {"expectedRevision": 1, "collectionId": shared["id"], "digest": receipt["digest"]}, 409)
        no_command(reviewer, "t")
        reviewer.send("g")
        reviewer.settle()
        start = len(reviewer.output)
        reviewer.send("r")
        reviewer.wait_text("Loaded latest revision", start)
        reviewer.wait_text(changed["revision"]["digest"], start)
        reviewer.send("g")
        reviewer.wait_text("Shared collections loaded", start)
        open_collection(reviewer, shared["id"])
        since = len(proxy.snapshot())
        start = send_confirmation(reviewer, "t", "attach")
        reviewer.wait_text("attach recorded", start)
        only_post(reviewer, since, "/changes/" + pkg["id"] + "/context-attachments",
                  {"expectedRevision": 2, "collectionId": shared["id"], "digest": receipt["digest"]}, 201)
        attached = command("/changes/" + pkg["id"])
        assert attached["revision"]["number"] == 3 and not attached["approved"]
        assert attached["revision"]["content"]["futureExtension"] == pkg["revision"]["content"]["futureExtension"]

        with tempfile.TemporaryDirectory(prefix="conductor-terminal-collections-") as directory:
            path = Path(directory) / "request.json"
            draft = {"commit": "a" * 40, "paths": ["docs/missing.md", "README.md"], "idempotencyKey": "terminal-explicit-retry", "fullSource": True}
            path.write_text(json.dumps(draft))
            reviewer.send("g")
            reviewer.settle()
            load_request(reviewer, path)
            proxy.drop_next_create = True
            since = len(proxy.snapshot())
            start = send_confirmation(reviewer, "s", "collect")
            reviewer.wait_text("Collection write not confirmed", start)
            body = {"commit": draft["commit"], "paths": sorted(draft["paths"]), "fullSource": True}
            only_post(reviewer, since, "/context-collections", body, 202, draft["idempotencyKey"])
            since = len(proxy.snapshot())
            start = send_confirmation(reviewer, "s", "collect")
            reviewer.wait_text("Collection request accepted", start)
            only_post(reviewer, since, "/context-collections", body, 202, draft["idempotencyKey"])
            page = command("/context-collections")
            mine = [c for c in page["collections"] if c["requesterId"] == "person-reviewer"]
            assert len(mine) == 1, "unknown response retry created duplicate work"
            identifier = mine[0]["id"]
            since = len(proxy.snapshot())
            start = send_confirmation(reviewer, "x", "cancel-collection")
            reviewer.wait_text("Cancellation response received", start)
            reviewer.wait_text("cancellation requested", start)
            only_post(reviewer, since, "/context-collections/" + identifier + "/cancellation", {}, 202)
            no_command(reviewer, "x")
            recorded = command("/context-collections/" + identifier)
            assert recorded.get("cancelRequestedAt") and not recorded.get("execution"), "intent invented confirmed cancellation"

            # Revocation clears preview/key, receipt and capabilities, and r
            # recovers through server discovery with the same original credential.
            load_request(reviewer, path)
            operator("revoke")
            start = len(reviewer.output)
            reviewer.send("s")
            reviewer.wait_text("Type collect", start)
            reviewer.send("collect\r")
            reviewer.wait_text("Access unavailable", start)
            no_command(reviewer, "ctsxag")
            operator("restore")
            since = len(proxy.snapshot())
            start = len(reviewer.output)
            reviewer.send("r")
            reviewer.wait_text("Shared collections loaded", start)
            calls = proxy.snapshot()[since:]
            assert [c["path"] for c in calls[:2]] == ["/api/v1/session", "/api/v1/repositories"], calls
            no_command(reviewer, "s")

            reader = launch("reader")
            reader.send("g")
            reader.wait_text("Shared collections loaded")
            open_collection(reader, shared["id"])
            reader.wait_text("Coverage: docs/missing.md | missing")
            no_command(reader, "ctsxae")

            agent = launch("agent", pkg["id"])
            agent.send("g")
            agent.wait_text("Shared collections loaded")
            draft["idempotencyKey"] = "terminal-agent-context"
            path.write_text(json.dumps(draft))
            load_request(agent, path)
            start = send_confirmation(agent, "s", "collect")
            agent.wait_text("Collection request accepted", start)
            no_command(agent, "a")
            page = command("/context-collections")
            assert any(c["requesterId"] == "person-agent" for c in page["collections"])

        for terminal in terminals:
            assert all(token.encode() not in terminal.output for token in tokens.values()), "credential leaked into PTY output"
            start = len(terminal.output)
            terminal.send("q")
            terminal.wait(lambda: terminal.process.poll() is not None, "terminal did not exit")
            assert terminal.process.returncode == 0
        print("PASS authenticated collection PTYs: shared paging/coverage, exact stale and successful attachment, lost-response explicit idempotent retry, requester cancellation, revocation/recovery, reader and agent controls")
    finally:
        for terminal in terminals:
            terminal.close()
        proxy.close()


if __name__ == "__main__":
    main()
