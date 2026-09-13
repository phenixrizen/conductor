# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Real browser commands against the owned signed-issuer/PostgreSQL fixture.

Provider receipts are seeded facts. This proves client interaction and shared
authorization, not live provider collection or Temporal execution.
"""
import json
import os
from pathlib import Path
import ssl
import urllib.parse
import urllib.request

from playwright.sync_api import Error, expect, sync_playwright

origin = os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
change_id = os.environ["CONDUCTOR_BROWSER_CHANGE_ID"]
collection_id = os.environ["CONDUCTOR_BROWSER_COLLECTION_ID"]
receipt_digest = os.environ["CONDUCTOR_BROWSER_RECEIPT_DIGEST"]
parsed = urllib.parse.urlparse(origin)
assert parsed.scheme == "https" and parsed.hostname in {"127.0.0.1", "::1"}, "requires the isolated loopback HTTPS fixture"
change_path = "/api/v1/changes/" + urllib.parse.quote(change_id, safe="")
collection_url = origin + "/api/v1/context-collections/" + collection_id
collections_url = origin + "/api/v1/context-collections"


def author_command(path, body):
    request = urllib.request.Request(
        origin + path, data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + os.environ["CONDUCTOR_BROWSER_AUTHOR_TOKEN"],
                 "X-Conductor-Workspace": "team", "X-Conductor-Repository": "application"})
    # The owned loopback fixture has an httptest certificate.
    with urllib.request.urlopen(request, context=ssl._create_unverified_context(), timeout=10) as response:
        return json.load(response)


def sign_in(page, identity):
    page.goto(origin)
    page.get_by_role("link", name="Sign in", exact=True).click()
    page.get_by_role("button", name="Sign in as " + identity, exact=True).click()
    expect(page.locator(".signed-in-identity")).to_contain_text("person-" + identity)
    page.get_by_label("Workspace", exact=True).select_option("team")
    page.get_by_label("Managed repository", exact=True).select_option("application")


def inspect_package(page, revision):
    page.get_by_label("Change ID", exact=True).fill(change_id)
    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name=f"Revision {revision}", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name="Inspect latest revision", exact=True)).to_be_enabled()


def inspect_receipt(page):
    page.get_by_role("button", name="Refresh collections", exact=True).click()
    page.get_by_role("button", name="Inspect collection " + collection_id, exact=True).click()
    expect(page.get_by_role("region", name="Inspected collection receipt")).to_contain_text(receipt_digest)


def confirm_command(page, button_name, expected_path, expected_body):
    # Do not let a mutation refresh facts after the user confirms them.
    requests = []
    capture = lambda request: requests.append(request)
    page.on("request", capture)
    try:
        with page.expect_response(lambda response: urllib.parse.urlparse(response.url).path == expected_path
                                  and response.request.method == "POST"):
            page.get_by_role("button", name=button_name, exact=True).click()
        expect(page.get_by_role("button", name="Refresh collections", exact=True)).to_be_enabled()
        commands = [request for request in requests if "/api/" in request.url]
        assert [(request.method, urllib.parse.urlparse(request.url).path) for request in commands] == [("POST", expected_path)]
        assert commands[0].post_data_json == expected_body
    finally:
        page.remove_listener("request", capture)


with sync_playwright() as playwright:
    browser = playwright.chromium.launch(
        executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"), headless=True)
    context = browser.new_context(ignore_https_errors=True, viewport={"width": 1440, "height": 1100})
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    sign_in(page, "reviewer")
    assert "conductor-session" not in page.evaluate("document.cookie")
    assert page.evaluate("localStorage.length + sessionStorage.length") == 0
    inspect_package(page, 1)
    expect(page.get_by_text("DESIGN APPROVED", exact=True)).to_be_visible()

    # The client keeps one bounded page, and makes omitted records explicit.
    page.get_by_role("button", name="Refresh collections", exact=True).click()
    shared = page.get_by_role("list", name="Shared collections", exact=True)
    expect(shared.get_by_role("button")).to_have_count(20)
    page.get_by_role("button", name="Load more collections", exact=True).click()
    expect(shared.get_by_role("button")).to_have_count(2)
    expect(page.get_by_role("button", name="Load more collections", exact=True)).to_have_count(0)
    inspect_receipt(page)
    receipt = page.get_by_role("region", name="Inspected collection receipt")
    expect(receipt.get_by_role("list", name="Context coverage")).to_contain_text("1 missing")
    expect(page.get_by_role("region", name="Execution observation")).to_contain_text("Progress unknown")
    receipt.locator("summary").filter(has_text="README.md").click()
    expect(receipt.locator(".source-text")).to_contain_text("Synthetic shared source")

    attach = page.get_by_role("button", name="Attach receipt to inspected revision", exact=True)
    page.get_by_role("button", name="Inspect historical revision 1", exact=True).click()
    expect(page.get_by_text("HISTORICAL VIEW", exact=True)).to_be_visible()
    expect(attach).to_be_disabled()
    inspect_package(page, 1)
    attach.click()
    dialog = page.get_by_role("dialog", name="Confirm context attachment")
    expect(dialog).to_contain_text(receipt_digest)
    expect(dialog).to_contain_text(change_id)

    # Another engineer changes the package after this exact confirmation opened.
    author_command(change_path + "/revisions", {"expectedRevision": 1, "content": {
        "intent": {"title": "Concurrent author update"}, "futureField": {"retained": True}}})
    author_command(change_path + "/review-requests", {"revision": 2})
    confirm_command(page, "Confirm attachment", change_path + "/context-attachments",
                    {"expectedRevision": 1, "collectionId": collection_id, "digest": receipt_digest})
    expect(page.get_by_text("Renewed inspection required.", exact=True)).to_be_visible()
    expect(page.get_by_text("Renewed collection inspection required before another mutation.", exact=True)).to_be_visible()
    expect(attach).to_be_disabled()
    inspect_package(page, 2)
    expect(page.get_by_role("button", name="Approve inspected revision", exact=True)).to_be_disabled()
    expect(attach).to_be_disabled()
    page.get_by_role("button", name="Refresh inspected collection", exact=True).click()
    expect(attach).to_be_enabled()
    expect(page.get_by_role("button", name="Approve inspected revision", exact=True)).to_be_enabled()
    attach.click()
    confirm_command(page, "Confirm attachment", change_path + "/context-attachments",
                    {"expectedRevision": 2, "collectionId": collection_id, "digest": receipt_digest})
    expect(page.get_by_role("heading", name="Revision 3", exact=True)).to_be_visible()
    expect(page.get_by_text("DRAFT", exact=True)).to_be_visible()
    expect(page.get_by_text("This inspected revision has no effective approval.", exact=True)).to_be_visible()

    # Lose only the acknowledgment after a real request commits. A new request or
    # automatic retry would create unseen work; the explicit retry keeps its key.
    page.locator("summary").filter(has_text="Request repository context").click()
    page.get_by_label("Exact commit", exact=True).fill("c" * 40)
    page.get_by_label("Explicit paths (one per line)", exact=True).fill("😀.md\n\ue000.md")
    page.get_by_label("Idempotency key", exact=True).fill("browser-lost-response")
    created = []
    posted = []

    def lose_creation_ack(route):
        posted.append((route.request.post_data_json, route.request.headers.get("idempotency-key")))
        response = route.fetch()
        assert response.status == 202
        created.append(response.json()["id"])
        route.abort()

    page.route(collections_url, lose_creation_ack)
    page.get_by_role("button", name="Request collection", exact=True).click()
    expect(page.get_by_role("button", name="Retry same request", exact=True)).to_be_visible()
    expect(page.get_by_label("Exact commit", exact=True)).to_be_disabled()
    expect(page.get_by_label("Idempotency key", exact=True)).to_be_disabled()
    assert len(created) == 1, "request retried without explicit action"
    page.unroute(collections_url, lose_creation_ack)
    with page.expect_request(lambda request: request.method == "POST" and request.url == collections_url) as retried:
        page.get_by_role("button", name="Retry same request", exact=True).click()
    expect(page.get_by_text("Collection request recorded.", exact=False)).to_be_visible()
    assert retried.value.post_data_json == posted[0][0]
    assert retried.value.headers.get("idempotency-key") == posted[0][1]
    expect(page.get_by_role("article", name="Collection inspection")).to_contain_text(created[0])
    page.get_by_role("button", name="Request cancellation", exact=True).click()
    confirm_command(page, "Confirm cancellation request", "/api/v1/context-collections/" + created[0] + "/cancellation", {})
    expect(page.get_by_role("region", name="Execution observation")).to_contain_text("Progress unknown")
    expect(page.get_by_role("article", name="Collection inspection")).to_contain_text("This request is not proof that execution stopped")
    expect(page.get_by_role("button", name="Request cancellation", exact=True)).to_be_disabled()

    # A committed attachment with a lost acknowledgment must stay uncertain. The
    # client must not refresh or replay it automatically, even though the server
    # has already created the new draft.
    recovery = author_command("/api/v1/changes", {"content": {
        "intent": {"title": "Uncertain browser attachment"}, "futureField": {"retained": True}}})
    recovery_id = recovery["id"]
    page.get_by_label("Change ID", exact=True).fill(recovery_id)
    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    inspect_receipt(page)
    attach.click()
    attachment_url = origin + "/api/v1/changes/" + recovery_id + "/context-attachments"
    lost_attachments = []
    mutation_requests = []

    def lose_attachment_ack(route):
        response = route.fetch()
        assert response.status == 201
        lost_attachments.append(response.json())
        route.abort()

    capture_mutation = lambda request: mutation_requests.append((request.method, request.url))
    page.route(attachment_url, lose_attachment_ack)
    page.on("request", capture_mutation)
    page.get_by_role("button", name="Confirm attachment", exact=True).click()
    expect(page.get_by_text("Attachment recovery requires both inspections.", exact=True)).to_be_visible()
    assert len(lost_attachments) == 1 and lost_attachments[0]["revision"]["number"] == 2
    assert [item for item in mutation_requests if "/api/" in item[1]] == [("POST", attachment_url)]
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    expect(attach).to_be_disabled()
    page.remove_listener("request", capture_mutation)
    page.unroute(attachment_url, lose_attachment_ack)
    page.get_by_role("button", name="Inspect recovery package", exact=True).click()
    expect(page.get_by_role("heading", name="Revision 2", exact=True)).to_be_visible()
    expect(attach).to_be_disabled()
    page.get_by_role("button", name="Inspect recovery collection", exact=True).click()
    expect(attach).to_be_enabled()
    expect(page.get_by_text("Attachment recovery requires both inspections.", exact=True)).to_have_count(0)
    inspect_package(page, 3)

    # A response already read by the proxy must not restore content after a scope
    # change. The browser abort and generation fence both protect this boundary.
    inspect_receipt(page)
    held = []

    def hold_collection(route):
        held.append((route, route.fetch()))

    page.route(collection_url, hold_collection)
    page.get_by_role("button", name="Refresh inspected collection", exact=True).click()
    expect(page.get_by_role("button", name="Refresh collections", exact=True)).to_be_disabled()
    page.get_by_label("Managed repository", exact=True).select_option("private")
    expect(page.get_by_role("heading", name="Collection inspection", exact=True)).to_have_count(0)
    expect(page.get_by_role("heading", name="Revision 3", exact=True)).to_have_count(0)
    assert len(held) == 1
    try:
        held[0][0].fulfill(response=held[0][1])
    except Error as error:
        assert any(reason in str(error).lower() for reason in ("cancel", "closed", "already handled")), str(error)
    page.unroute(collection_url, hold_collection)
    expect(page.get_by_text(receipt_digest, exact=True)).to_have_count(0)
    page.get_by_label("Managed repository", exact=True).select_option("application")
    inspect_package(page, 3)
    # Package JSON alone must not claim linkage until this scoped receipt is read.
    expect(page.get_by_text("Remote receipt reference has not been checked", exact=False)).to_be_visible()
    page.get_by_role("button", name="Inspect linked collection", exact=True).click()
    expect(page.get_by_text("The complete snapshot matches the server-stored receipt", exact=False)).to_have_count(2)

    # This controlled response tests observation display aging only. The workflow
    # execution itself is verified separately against an owned Temporal server.
    page.clock.install()

    def current_observation(route):
        response = route.fetch()
        record = response.json()
        record["execution"] = {"namespace": "synthetic", "workflowId": "synthetic-workflow",
                               "runId": "synthetic-run", "state": "running", "current": True,
                               "observedAt": page.evaluate("new Date().toISOString()")}
        route.fulfill(response=response, json=record)

    page.route(collection_url, current_observation)
    page.get_by_role("button", name="Refresh inspected collection", exact=True).click()
    observation = page.get_by_role("region", name="Execution observation")
    expect(observation).to_contain_text("Observed execution: running")
    page.unroute(collection_url, current_observation)
    aging_requests = []
    capture_age = lambda request: aging_requests.append(request.url)
    page.on("request", capture_age)
    page.clock.fast_forward(31000)
    expect(observation).to_contain_text("Stale observation: running")
    assert not [url for url in aging_requests if "/api/" in url], "display clock polled the API"
    page.remove_listener("request", capture_age)
    # Restore the actual server observation before capturing the normal screen.
    page.get_by_role("button", name="Refresh inspected collection", exact=True).click()
    expect(observation).to_contain_text("Progress unknown")

    screenshot = Path(os.environ.get("CONDUCTOR_BROWSER_SCREENSHOT", "/tmp/conductor-context-workbench.png"))
    page.screenshot(path=str(screenshot), full_page=True)
    page.set_viewport_size({"width": 390, "height": 844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "mobile horizontal overflow"
    page.screenshot(path=str(screenshot.with_name(screenshot.stem + "-mobile.png")), full_page=True)

    # A forbidden status is decisive even when its response body does not finish.
    def deny_with_stalled_body(route):
        headers = dict(route.request.headers)
        headers["x-conductor-test-response"] = "denied-stall"
        route.continue_(headers=headers)

    page.route(collection_url, deny_with_stalled_body)
    page.get_by_role("button", name="Refresh inspected collection", exact=True).click()
    expect(page.get_by_role("heading", name="Check your access", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name="Revision 3", exact=True)).to_have_count(0)
    expect(page.get_by_role("heading", name="Collection inspection", exact=True)).to_have_count(0)
    expect(page.get_by_label("Exact commit", exact=True)).to_have_count(0)
    expect(page.get_by_role("dialog")).to_have_count(0)
    context.close()

    # A second reader sees the same stored source without gaining author controls.
    reader_context = browser.new_context(ignore_https_errors=True)
    reader = reader_context.new_page()
    reader.on("pageerror", lambda error: errors.append(str(error)))
    sign_in(reader, "reader")
    inspect_receipt(reader)
    expect(reader.get_by_role("region", name="Inspected collection receipt")).to_contain_text(receipt_digest)
    expect(reader.get_by_text("Read-only repository access.", exact=False)).to_be_visible()
    expect(reader.get_by_label("Exact commit", exact=True)).to_have_count(0)
    expect(reader.get_by_role("button", name="Request cancellation", exact=True)).to_have_count(0)
    expect(reader.get_by_role("button", name="Attach receipt to inspected revision", exact=True)).to_have_count(0)
    reader_context.close()
    assert not errors, errors
    browser.close()
    print("PASS: shared paginated collection discovery, receipt coverage, historical/stale attachment controls, exact confirmation without refresh, preserved draft, lost request acknowledgment and same-key retry, uncertain attachment recovery, cancellation intent, late-response scope isolation, trusted linkage, observation aging without polling, stalled-body denial, read-only collaboration, and mobile layout")
    print(f"Screenshots: {screenshot}, {screenshot.with_name(screenshot.stem + '-mobile.png')}")
