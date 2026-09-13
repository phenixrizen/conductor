#!/usr/bin/env python3
"""Actual signed-API/PostgreSQL release workbench acceptance through Linux PTYs.

Source and implementation receipts are explicitly labelled fixtures. This verifies
shared inspection and decisions, not native agent calls or remote publication.
"""
import copy
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import urllib.error
import urllib.parse
import urllib.request

from authenticated import ProtectedTerminal
from review import OPENER

class ReleaseProxy:
    def __init__(self, api, tokens):
        self.requests = []
        self.lock = threading.Lock()
        self.drop_next_create = False
        self.drop_path = "/api/v1/coordination-runs"
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
                        data = response.read((17 << 20) + 1)
                        status = response.code
                    assert len(data) <= 17 << 20
                    with owner.lock:
                        observed["status"] = status
                        drop = (owner.drop_next_create and self.command == "POST"
                                and self.path == owner.drop_path and status in (201, 202))
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
    files = {name: Path(path) for name, path in json.loads(os.environ["CONDUCTOR_TERMINAL_CREDENTIAL_FILES"]).items()}
    tokens = {name: path.read_text().rstrip("\n") for name, path in files.items()}
    setup = json.loads(os.environ["CONDUCTOR_TERMINAL_RELEASE_SETUP"])
    proxy = ReleaseProxy(api, tokens)
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

    def launch(identity, view, identifier=None):
        args = ["--workspace", "team", "--repository-id", "application", "--view", view]
        if identifier:
            args.append(identifier)
        terminal = ProtectedTerminal(binary, proxy.url, args, files[identity], tokens)
        terminals.append(terminal)
        terminal.wait_text("Shared record inspected" if identifier else "Shared " + view + " loaded")
        terminal.settle()
        return terminal

    def open_record(terminal, identifier):
        start = len(terminal.output)
        terminal.send("o")
        terminal.wait_text("Record ID", start)
        terminal.send(identifier + "\r")
        terminal.wait_text("ID: " + identifier, start)
        terminal.settle()

    def import_file(terminal, path, key="c"):
        start = len(terminal.output)
        terminal.send(key)
        terminal.wait_text("Request JSON file path", start)
        terminal.send(str(path) + "\r")
        terminal.wait_text("Request preview loaded", start)
        terminal.settle()

    def confirm(terminal, key, action, expected):
        start = len(terminal.output)
        terminal.send(key)
        terminal.wait_text("Type " + action, start)
        terminal.send(action + "\r")
        terminal.wait_text(expected, start)
        terminal.settle()

    def cli(identity, op, arguments):
        env = os.environ.copy()
        for name in ("CONDUCTOR_TOKEN", "CONDUCTOR_TOKEN_FILE", "CONDUCTOR_WORKSPACE", "CONDUCTOR_REPOSITORY_ID"):
            env.pop(name, None)
        env.update({"CONDUCTOR_URL": proxy.url, "CONDUCTOR_TOKEN_FILE": str(files[identity])})
        result = subprocess.run([binary, op, "--workspace", "team", "--repository-id", "application", *arguments], env=env, capture_output=True, timeout=15)
        for token in tokens.values():
            assert token.encode() not in result.stdout + result.stderr
        assert result.returncode == 0, result.stderr.decode(errors="replace")
        return json.loads(result.stdout)

    try:
        with tempfile.TemporaryDirectory(prefix="conductor-release-review-") as directory:
            directory = Path(directory)
            def request_file(name, key, data):
                path = directory / name
                path.write_text(json.dumps({"idempotencyKey": key, "input": data}))
                return path

            graph_file = request_file("graph.json", "terminal-graph-create", setup["graphInput"])
            graph_preview = cli("author", "graph-preview", ["--file", str(graph_file)])
            graph = cli("author", "graph-create", ["--file", str(graph_file), "--digest", graph_preview["digest"]])
            assert graph["workspaceId"] == "team"
            graph_terminal = launch("author", "graphs", graph["id"])
            start = len(graph_terminal.output)
            graph_terminal.send("/")
            graph_terminal.wait_text("Graph search", start)
            graph_terminal.send("README\r")
            graph_terminal.wait_text("Graph query inspected", start)
            assert cli("author", "graph-query", ["--search", "README", graph["id"]])["digest"] == graph["digest"]
            source = graph["snapshot"]["sources"][0]
            artifact = cli("author", "graph-artifact", ["--digest", graph["digest"], "--source-repository", source["repositoryId"], "--collection-id", source["collectionId"], "--receipt-digest", source["digest"], "--full-source-digest", source["fullSourceDigest"], "--path", "README.md", graph["id"]])
            assert artifact["artifact"]["state"] == "collected" and artifact["source"]["repositoryId"] == "application"
            start = len(graph_terminal.output)
            graph_terminal.send("f")
            graph_terminal.wait_text("Repository ID from inspected graph sources", start)
            graph_terminal.send("application\r")
            graph_terminal.wait_text("Exact relative source path", start)
            graph_terminal.send("README.md\r")
            graph_terminal.wait_text("Graph source artifact inspected", start)


            stale = setup["staleRun"]
            reviewer = launch("reviewer", "runs", stale["id"])
            start = len(reviewer.output)
            reviewer.send("a")
            reviewer.wait_text("Type authorize-run", start)
            before = len(proxy.snapshot())
            package = setup["stalePackage"]
            command("/changes/" + package["id"] + "/revisions", {"expectedRevision": 1, "content": {"intent": "Concurrent terminal edit"}})
            reviewer.send("authorize-run\r")
            reviewer.wait_text("Write not confirmed", start)
            reviewer.settle()
            calls = proxy.snapshot()[before:]
            assert len(calls) == 1 and calls[0]["method"] == "POST" and calls[0]["body"] == {"digest": stale["digest"]}
            assert calls[0]["status"] == 409
            before = len(proxy.snapshot())
            reviewer.send("a")
            reviewer.wait_text("Action blocked", start)
            reviewer.settle()
            assert len(proxy.snapshot()) == before

            author = launch("author", "runs")
            plan_file = request_file("plan.json", "terminal-exact-run", setup["plan"])
            import_file(author, plan_file)
            proxy.drop_next_create = True
            confirm(author, "s", "propose-run", "Write not confirmed")
            confirm(author, "s", "propose-run", "Proposal recorded")
            creates = [r for r in proxy.snapshot() if r["method"] == "POST" and r["path"] == "/api/v1/coordination-runs"]
            assert len(creates) == 2 and creates[0]["key"] == creates[1]["key"] == "terminal-exact-run" and creates[0]["body"] == creates[1]["body"]
            runs = cli("reviewer", "runs", ["--limit", "100"])["runs"]
            candidates = [r for r in runs if r["proposerId"] == "person-author"]
            assert len(candidates) == 1
            run = candidates[0]
            start = len(reviewer.output)
            reviewer.send("r")
            reviewer.wait_text("Shared record inspected", start)
            open_record(reviewer, run["id"])
            confirm(reviewer, "a", "authorize-run", "Authorization recorded")
            confirm(reviewer, "x", "cancel-run", "Cancellation requested")
            commands = [r for r in proxy.snapshot() if r["path"].startswith("/api/v1/coordination-runs/" + run["id"] + "/")]
            assert len(commands) == 2 and all(r["body"] == {"digest": run["digest"]} for r in commands)
            for identity in ("agent", "reader"):
                limited = launch(identity, "runs", run["id"])
                count = len(proxy.snapshot())
                start = len(limited.output)
                limited.send("a")
                limited.wait_text("Action blocked", start)
                limited.settle()
                assert len(proxy.snapshot()) == count

            delivery = setup["delivery"]
            publication = launch("reviewer", "deliveries", delivery["id"])
            start = len(publication.output)
            count = len(proxy.snapshot())
            publication.send("a")
            publication.wait_text("press v", start)
            publication.settle()
            assert len(proxy.snapshot()) == count
            publication.send("v")
            publication.wait_text("Exact artifact inspected", start)
            publication.wait_text("synthetic fixture: worker execution is tested separately", start)
            artifact = cli("reviewer", "delivery-artifact", [delivery["id"]])
            assert artifact["artifactDigest"] == delivery["input"]["artifactDigest"]
            confirm(publication, "a", "authorize-publication", "Authorization recorded")
            operator("observe")
            start = len(publication.output)
            publication.send("r")
            publication.wait_text("Shared record inspected", start)
            reconciliation = request_file("reconcile.json", "terminal-delivery-reconcile", {"digest": delivery["digest"]})
            import_file(publication, reconciliation, "u")
            confirm(publication, "s", "reconcile-delivery", "Reconciliation requested")

            link = setup["trackerLink"]
            tracker = launch("reviewer", "tracker", link["id"])
            sync_file = request_file("sync.json", "terminal-tracker-refresh", {"linkDigest": link["digest"], "mode": "refresh"})
            import_file(tracker, sync_file, "u")
            confirm(tracker, "s", "sync-ticket", "Tracker sync requested")
            start = len(tracker.output)
            tracker.send("r")
            tracker.wait_text("Shared record inspected", start)
            tracker.send("i")
            tracker.wait_text("Tracker sync inspected", start)
            assert cli("reviewer", "tracker", [])["provider"] == "linear"
            assert cli("reviewer", "tracker-link", [link["id"]])["digest"] == link["digest"]

            # Access recovery keeps the process's original token even if its file
            # is replaced, and removes imported private previews on revocation.
            import_file(reviewer, plan_file)
            operator("revoke")
            files["reviewer"].write_text(tokens["agent"] + "\n")
            start = len(reviewer.output)
            reviewer.send("r")
            reviewer.wait_text("Access unavailable", start)
            reviewer.settle()
            assert "PREVIEW" not in reviewer.text(start)
            operator("restore")
            start = len(reviewer.output)
            reviewer.send("r")
            reviewer.wait_text("Shared record inspected", start)
            assert proxy.snapshot()[-1]["identity"] == "reviewer"
            files["reviewer"].write_text(tokens["reviewer"] + "\n")
            reviewer.resize(35, 10)
            start = len(reviewer.output)
            reviewer.wait_text("enlarge terminal", start)
            count = len(proxy.snapshot())
            reviewer.send("a")
            reviewer.settle()
            assert len(proxy.snapshot()) == count

            for terminal in terminals:
                assert b"\x1b]52;c;YQ==" not in terminal.output
                for token in tokens.values():
                    assert token.encode() not in terminal.output
            calls = proxy.snapshot()
            assert all(not r["localActor"] and r["workspace"] == "team" and r["repository"] == "application" for r in calls)
            print("PASS release CLI and actual PTYs: graph query, exact plan preview/retry, stale approval, human authorization/cancellation, artifact-gated publication, tracker synchronization, revoked access recovery, fixed token and terminal controls")
    finally:
        for terminal in terminals:
            terminal.close()
        proxy.close()

if __name__ == "__main__":
    main()
