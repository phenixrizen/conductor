#!/usr/bin/env python3
"""Exercise the compiled terminal workbench against an explicitly selected API.

CONDUCTOR_API_URL and CONDUCTOR_BIN are required. Run against an isolated test
database: this creates synthetic persisted history, which the API cannot delete.
The recording proxy forwards every command to that real API; it does not provide
mock package behavior. Linux PTYs and the Python standard library are sufficient.
"""

import copy
import errno
import fcntl
import http.server
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import tempfile
import termios
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        return None


OPENER = urllib.request.build_opener(NoRedirect)
CSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
OSC = re.compile(r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")


def command(api, path, actor, body=None):
    request = urllib.request.Request(
        api + "/api/v1" + path,
        data=None if body is None else json.dumps(body).encode(),
        headers={"Content-Type": "application/json", "X-Conductor-Actor": actor},
    )
    with OPENER.open(request, timeout=10) as response:
        return json.load(response)


class RecordingProxy:
    """Observe actual client requests without replacing the service/store path."""

    def __init__(self, api):
        self.requests = []
        self.drop_next_path = None
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
                observed = {
                    "method": self.command,
                    "path": self.path,
                    "body": None if body is None else json.loads(body),
                    "status": None,
                }
                with owner.lock:
                    owner.requests.append(observed)
                request = urllib.request.Request(
                    api + self.path,
                    data=body,
                    method=self.command,
                    headers={
                        "Content-Type": "application/json",
                        "X-Conductor-Actor": self.headers.get("X-Conductor-Actor", ""),
                    },
                )
                try:
                    try:
                        response = OPENER.open(request, timeout=10)
                    except urllib.error.HTTPError as error:
                        response = error
                    with response:
                        data = response.read((2 << 20) + 1)
                        status = response.code
                        correlation = response.headers.get("X-Correlation-ID")
                    with owner.lock:
                        drop_ack = owner.drop_next_path == self.path
                        if drop_ack:
                            owner.drop_next_path = None
                    if drop_ack:
                        # The real API committed; lose only its acknowledgment.
                        self.close_connection = True
                        return
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(data)))
                    if correlation:
                        self.send_header("X-Correlation-ID", correlation)
                    self.end_headers()
                    self.wfile.write(data)
                except Exception as error:
                    status = 502
                    observed["proxyError"] = repr(error)
                    self.send_error(status, "Test proxy could not reach the configured API")
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


class Terminal:
    def __init__(self, binary, proxy_url, args, environment_overrides=None):
        self.master, slave = pty.openpty()
        self.output = bytearray()
        self.resize(120, 45)
        environment = os.environ.copy()
        environment.update({"CONDUCTOR_URL": proxy_url, "TERM": "xterm-256color"})
        # Authenticated acceptance selects a credential file for each child,
        # without mutating the parent process or placing credentials in argv.
        for name, value in (environment_overrides or {}).items():
            if value is None:
                environment.pop(name, None)
            else:
                environment[name] = value
        try:
            self.process = subprocess.Popen(
                [binary, "tui", *args],
                stdin=slave,
                stdout=slave,
                stderr=slave,
                env=environment,
                start_new_session=True,
                close_fds=True,
            )
        finally:
            os.close(slave)

    def resize(self, columns, rows):
        fcntl.ioctl(self.master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))
        if hasattr(self, "process") and self.process.poll() is None:
            os.kill(self.process.pid, signal.SIGWINCH)

    def pump(self, timeout=0.05):
        ready, _, _ = select.select([self.master], [], [], timeout)
        if not ready:
            return
        try:
            data = os.read(self.master, 65536)
        except OSError as error:
            if error.errno == errno.EIO:
                return
            raise
        self.output.extend(data)
        # Answer the terminal capability probes used by Bubble Tea. They are
        # protocol responses, never user actions or approval confirmations.
        if b"\x1b[6n" in data:
            os.write(self.master, b"\x1b[1;1R")
        if b"\x1b]11;?" in data:
            os.write(self.master, b"\x1b]11;rgb:0000/0000/0000\x1b\\")

    def text(self, start=0):
        raw = self.output[start:].decode("utf-8", errors="replace")
        return CSI.sub("", OSC.sub("", raw)).replace("\r", "")

    def wait(self, predicate, description, timeout=12):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            self.pump()
            if predicate():
                return
            if self.process.poll() is not None:
                # Exit can become visible between the predicate and this poll.
                # Recheck it before treating exit as a failed wait; callers also
                # use this helper to wait for a successful process termination.
                if predicate():
                    return
                break
        # Escape controls in the diagnostic so an unexpected server value cannot
        # turn a failed acceptance run into active terminal escape sequences.
        raise AssertionError(description + ": " + repr(self.text()[-5000:]))

    def wait_text(self, text, start=0):
        self.wait(lambda: text in self.text(start), "terminal did not show " + repr(text))

    def send(self, keys):
        os.write(self.master, keys.encode())

    def settle(self):
        deadline = time.monotonic() + 0.25
        while time.monotonic() < deadline:
            self.pump()

    def close(self):
        try:
            if self.process.poll() is None:
                self.process.terminate()
                try:
                    self.process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    self.process.kill()
                    self.process.wait(timeout=3)
        finally:
            os.close(self.master)


def observed_command(terminal, proxy, since, path, expected_body, status):
    terminal.wait(
        lambda: any(item["status"] is not None for item in proxy.snapshot()[since:]),
        "command did not finish",
    )
    terminal.settle()
    observed = proxy.snapshot()[since:]
    assert len(observed) == 1, ("command refreshed or replayed unexpectedly", observed)
    request = observed[0]
    assert request == {
        "method": "POST", "path": "/api/v1" + path,
        "body": expected_body, "status": status,
    }, request


def fill_design(terminal, content):
    """Use only visible form controls; no content file or API-assisted draft."""
    terminal.send("c")
    terminal.wait_text("FORM: New Change")
    fields = ("title", "intent", "scope", "design", "tasks", "verification")
    for index, field in enumerate(fields):
        terminal.wait_text("Field " + str(index + 1) + "/6:")
        terminal.send("\r" + content.get(field, "").replace("\n", "\r"))
        terminal.settle()
        if index < len(fields) - 1:
            terminal.send("\t")
    terminal.send("\x13")
    terminal.wait_text("PREVIEW: create")


def guided_review(binary, api, proxy, author, reviewer, terminals):
    content = {"title": "Quick synthetic rate limit change", "intent": "Protect login\nKeep recovery available",
               "scope": "Synthetic login only", "design": "Count bounded attempts",
               "tasks": "Add the counter\nCheck the response", "verification": "Planned checks; unexecuted"}
    writer = Terminal(binary, proxy.url, ["--actor", author])
    terminals.append(writer)
    writer.wait_text("Shared work loaded")
    before = len(proxy.snapshot())
    fill_design(writer, content)
    assert len(proxy.snapshot()) == before, "form performed hidden API operations"
    writer.send("s")
    writer.wait_text("Type create")
    before = len(proxy.snapshot())
    writer.send("create\r")
    observed_command(writer, proxy, before, "/changes", {"content": content}, 201)
    writer.wait_text("create recorded")
    change_id = re.search(r"ID: (CHG-[0-9a-f]+)", writer.text()).group(1)
    path = "/changes/" + change_id
    created = command(api, path, author)
    assert created["revision"]["content"] == content and not created["revision"].get("submittedAt")
    writer.send("s")
    writer.settle()
    assert len(proxy.snapshot()) == before + 1, "save key also submitted"
    writer.send("u")
    writer.wait_text("Type submit")
    before = len(proxy.snapshot())
    writer.send("submit\r")
    observed_command(writer, proxy, before, path + "/review-requests", {"revision": 1}, 200)
    reader = Terminal(binary, proxy.url, ["--actor", reviewer, change_id])
    terminals.append(reader)
    reader.wait_text(created["revision"]["digest"])
    reader.wait_text(content["title"])
    reader.send("a")
    reader.wait_text("Type approve")
    before = len(proxy.snapshot())
    reader.send("approve\r")
    observed_command(reader, proxy, before, path + "/approvals",
                     {"revision": 1, "digest": created["revision"]["digest"]}, 201)
    writer.send("e")
    writer.wait_text("FORM: Edit Design")
    writer.send("\t\r\x15Add per-account limits\x13")
    writer.wait_text("PREVIEW: revise")
    writer.send("s")
    writer.wait_text("Type revise")
    updated = {**content, "intent": "Add per-account limits"}
    concurrent = {**content, "design": "Another engineer's design"}
    changed = command(api, path + "/revisions", author,
                      {"expectedRevision": 1, "content": concurrent})
    before = len(proxy.snapshot())
    writer.send("revise\r")
    observed_command(writer, proxy, before, path + "/revisions",
                     {"expectedRevision": 1, "content": updated}, 409)
    writer.wait_text("Stale inspection")
    writer.send("r")
    writer.wait_text(changed["revision"]["digest"])
    writer.settle()
    before = len(proxy.snapshot())
    writer.send("v")
    writer.wait_text("RECOVERED INPUT")
    writer.wait_text("CURRENT SAVED DESIGN")
    assert len(proxy.snapshot()) == before, "recovering input performed a hidden request"
    writer.send("s")
    writer.wait_text("Type revise")
    before = len(proxy.snapshot())
    writer.send("revise\r")
    observed_command(writer, proxy, before, path + "/revisions",
                     {"expectedRevision": 2, "content": updated}, 201)
    latest = command(api, path, author)
    assert latest["revision"]["content"] == updated and not latest["approved"]
    assert command(api, path + "/revisions/1", author)["approvals"], "guided edit lost historical approval"

    # A real committed revision with a lost acknowledgment keeps the typed input.
    # Explicit inspection recognizes matching saved content without replaying it.
    writer.send("e")
    writer.wait_text("FORM: Edit Design")
    writer.send("\r\x15Recovered after lost acknowledgment\x13")
    writer.wait_text("PREVIEW: revise")
    writer.send("s")
    writer.wait_text("Type revise")
    committed = {**updated, "title": "Recovered after lost acknowledgment"}
    before = len(proxy.snapshot())
    proxy.drop_next_path = "/api/v1" + path + "/revisions"
    writer.send("revise\r")
    observed_command(writer, proxy, before, path + "/revisions",
                     {"expectedRevision": 3, "content": committed}, 201)
    writer.wait_text("Write not confirmed")
    saved = command(api, path, author)
    assert saved["revision"]["number"] == 4 and saved["revision"]["content"] == committed
    writer.send("r")
    writer.wait_text(saved["revision"]["digest"])
    writer.settle()
    before = len(proxy.snapshot())
    writer.send("v")
    writer.wait_text("Retained design matches")
    writer.send("s")
    writer.settle()
    assert len(proxy.snapshot()) == before, "recovery replayed a committed revision"


def main():
    api = os.environ.get("CONDUCTOR_API_URL", "").rstrip("/")
    binary = os.environ.get("CONDUCTOR_BIN", "")
    if not api or not binary:
        raise SystemExit("Set CONDUCTOR_API_URL and CONDUCTOR_BIN explicitly; use an isolated test database.")
    parsed = urllib.parse.urlsplit(api)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.query or parsed.fragment:
        raise SystemExit("CONDUCTOR_API_URL must be an HTTP(S) API base URL without a query or fragment.")
    binary = str(Path(binary).resolve(strict=True))
    if not os.access(binary, os.X_OK):
        raise SystemExit("CONDUCTOR_BIN must name an executable compiled conductor binary.")
    run_id = uuid.uuid4().hex
    author, reviewer = "terminal-author-" + run_id, "terminal-reviewer-" + run_id
    content = {
        "intent": {"request": "Review synthetic terminal work " + run_id},
        "futureExtension": {"retain": ["unknown", True, None]},
        "untrustedText": "\x1b]52;c;ZGF0YQ==\x07",
        "verification": {"state": "unexecuted", "required": ["synthetic check"]},
    }
    proxy = RecordingProxy(api)
    terminals = []
    try:
        guided_review(binary, api, proxy, author, reviewer, terminals)
        with tempfile.TemporaryDirectory(prefix="conductor-terminal-") as directory:
            source = Path(directory) / "package.json"
            source.write_text(json.dumps(content))
            writer = Terminal(binary, proxy.url, ["--actor", author, "--file", str(source)])
            terminals.append(writer)
            writer.wait_text("Shared work loaded")
            writer.send("i")
            writer.wait_text("JSON file path")
            writer.send("\r")
            writer.wait_text("PREVIEW: create")
            writer.send("s")
            writer.wait_text("Type create")
            before = len(proxy.snapshot())
            writer.send("create\r")
            observed_command(writer, proxy, before, "/changes", {"content": content}, 201)
            writer.wait_text("create recorded")
            match = re.search(r"ID: (CHG-[0-9a-f]+)", writer.text())
            assert match, "created package ID was not displayed"
            change_id = match.group(1)
            path = "/changes/" + change_id
            created = command(api, path, author)
            assert created["revision"]["content"] == content
            assert not created["approved"]

            writer.send("u")
            writer.wait_text("Type submit")
            before = len(proxy.snapshot())
            writer.send("submit\r")
            observed_command(writer, proxy, before, path + "/review-requests", {"revision": 1}, 200)
            writer.wait_text("submit recorded")
            inspected = command(api, path, reviewer)
            assert inspected["revision"]["submittedAt"]

            reader = Terminal(binary, proxy.url, ["--actor", reviewer, change_id])
            terminals.append(reader)
            reader.wait_text("Loaded latest revision")
            reader.wait_text(inspected["revision"]["digest"])
            reader.send("\x1b[F")
            reader.settle()
            reader.send("\x1b[H")
            reader.settle()

            # Another real client edits after the reviewer inspects revision 1.
            updated = copy.deepcopy(content)
            updated["intent"]["request"] = "Changed while the terminal was reviewing"
            changed = command(api, path + "/revisions", author,
                              {"expectedRevision": 1, "content": updated})
            command(api, path + "/review-requests", author, {"revision": 2})
            reader.send("a")
            reader.wait_text("Type approve")
            before = len(proxy.snapshot())
            reader.send("approve\r")
            observed_command(reader, proxy, before, path + "/approvals",
                             {"revision": 1, "digest": inspected["revision"]["digest"]}, 409)
            reader.wait_text("Stale inspection")
            before = len(proxy.snapshot())
            reader.send("a")
            reader.settle()
            assert len(proxy.snapshot()) == before, "stale inspection allowed another command"

            reader.send("r")
            reader.wait_text(changed["revision"]["digest"])
            reader.settle()
            reader.send("a")
            reader.settle()
            before = len(proxy.snapshot())
            # A hidden confirmation must not authorize work in a tiny viewport.
            reader.resize(20, 8)
            reader.wait_text("enlarge")
            reader.send("approve\r")
            reader.settle()
            assert len(proxy.snapshot()) == before, "hidden confirmation sent a command"
            reader.resize(120, 45)
            reader.settle()
            reader.send("\r")
            observed_command(reader, proxy, before, path + "/approvals",
                             {"revision": 2, "digest": changed["revision"]["digest"]}, 201)
            reader.wait_text("Design approval recorded")
            approved = command(api, path, reviewer)
            assert approved["approved"] and approved["approval"]["reviewer"] == reviewer

            # File revision through a separate terminal must retain extensions
            # and invalidate the now-historical approval in the durable service.
            replacement = copy.deepcopy(updated)
            replacement["intent"]["request"] = "A later file revision needs a new review"
            source.write_text(json.dumps(replacement))
            writer.send("r")
            writer.wait_text(changed["revision"]["digest"])
            writer.settle()
            writer.send("i")
            writer.settle()
            writer.send("\r")
            writer.wait_text("PREVIEW: revise")
            writer.send("s")
            writer.wait_text("Type revise")
            before = len(proxy.snapshot())
            writer.send("revise\r")
            observed_command(writer, proxy, before, path + "/revisions",
                             {"expectedRevision": 2, "content": replacement}, 201)
            writer.wait_text("revise recorded")
            latest = command(api, path, reviewer)
            historical = command(api, path + "/revisions/2", reviewer)
            assert latest["revision"]["number"] == 3 and not latest["approved"]
            assert latest["revision"]["content"] == replacement
            assert historical["approvals"] == [approved["approval"]]
            assert historical["revision"]["content"] == updated
            for terminal in terminals:
                assert b"\x1b]52;c;ZGF0YQ==\x07" not in terminal.output, "source controlled the terminal"
                terminal.send("q")
                terminal.wait(lambda: terminal.process.poll() is not None, "terminal did not exit")
                assert terminal.process.returncode == 0, terminal.process.returncode
            print("PASS: guided form create/edit without JSON, separate save/submit, readable review, "
                  "retained input after stale/lost-acknowledgment saves, "
                  "advanced JSON import, exact stale approval without refresh, renewed inspection, "
                  "small-screen guard, independent approval, revision invalidation, and retained history")
            print("Synthetic package: " + change_id)
    finally:
        for terminal in reversed(terminals):
            terminal.close()
        proxy.close()


if __name__ == "__main__":
    main()
