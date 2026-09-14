# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Signed browser, native agent API, and real PostgreSQL; no paid inference."""
import json
import os
import ssl
import urllib.request
from urllib.parse import urlparse
from playwright.sync_api import expect, sync_playwright

origin = os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme == "https" and urlparse(origin).hostname in {"127.0.0.1", "::1"}
change = os.environ["CONDUCTOR_BROWSER_CHANGE_ID"]
path = "/changes/" + change

def api(path, body=None, identity="AUTHOR", key=None):
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + os.environ["CONDUCTOR_BROWSER_" + identity + "_TOKEN"],
               "X-Conductor-Workspace": "team", "X-Conductor-Repository": "application"}
    if key:
        headers["Idempotency-Key"] = key
    req = urllib.request.Request(origin + "/api/v1" + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
    with urllib.request.urlopen(req, context=ssl._create_unverified_context(), timeout=10) as result:
        return json.load(result)

def proposal(record, values):
    return api("/design-assistance/" + record["id"] + "/suggestion", {"requestDigest": record["digest"], "sections": values,
        "note": "Synthetic unverified proposal; shared workflow transport only."}, "AGENT", "synthetic-proposal-" + record["id"])

with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"), headless=True)
    errors = []
    def login(identity):
        context = browser.new_context(ignore_https_errors=True, viewport={"width": 1440, "height": 1050})
        page = context.new_page(); page.set_default_timeout(10000)
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(origin)
        expect(page.get_by_role("img", name="Conductor", exact=True)).to_be_visible()
        page.get_by_role("link", name="Sign in", exact=True).click()
        page.get_by_role("button", name="Sign in as " + identity, exact=True).click()
        expect(page.get_by_label("Workspace", exact=True)).to_be_visible()
        page.get_by_label("Workspace", exact=True).select_option("team")
        page.get_by_label("Managed repository", exact=True).select_option("application")
        return page
    def button(page, name):
        return page.get_by_role("button", name=name, exact=True)
    def inspect(page):
        page.get_by_label("Change ID", exact=True).fill(change)
        button(page, "Inspect latest revision").click()
        expect(button(page, "Inspect latest revision")).to_be_enabled()
    def panel(page):
        return page.get_by_role("region", name="Improve a Design with an assistant", exact=True)
    def start(page, instruction):
        button(page, "Ask for design help").click()
        page.get_by_label("Help wanted", exact=True).fill(instruction)
    def save_request(page):
        button(page, "Preview assistance request").click()
        with page.expect_response(lambda r: r.url == origin + "/api/v1/design-assistance" and r.request.method == "POST") as response:
            button(page, "Save assistance request").click()
        value = response.value.json()
        expect(button(page, "Check for suggestion")).to_be_enabled()
        return value
    page = login("author"); inspect(page)
    original = api(path)
    commands = []
    page.on("request", lambda req: commands.append((req.method, req.url, req.post_data, req.headers.get("idempotency-key"))) if "/api/" in req.url else None)
    start(page, "Clarify the approach and propose a verification plan.")
    expect(panel(page).get_by_label("Scope", exact=False)).to_be_disabled()
    expect(panel(page).get_by_label("Planned work", exact=False)).to_be_disabled()
    panel(page).get_by_label("Verification plan", exact=True).check()
    button(page, "Preview assistance request").click()
    expect(page.get_by_role("heading", name="Review assistance request", exact=True)).to_be_visible()
    assert not commands, commands
    page.get_by_role("tab", name="Agent work", exact=True).click()
    page.get_by_role("tab", name="Review", exact=True).click()
    expect(button(page, "Save assistance request")).to_have_count(0)
    # Leaving a tab drops only the unsent confirmation, retaining the input.
    expect(page.get_by_label("Help wanted", exact=True)).to_have_value("Clarify the approach and propose a verification plan.")
    button(page, "Preview assistance request").click()
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path="/tmp/conductor-branded-workbench-desktop.png")
    page.screenshot(path="/tmp/conductor-assistance-request-desktop.png", full_page=True)
    page.set_viewport_size({"width": 390, "height": 844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path="/tmp/conductor-branded-workbench-mobile.png")
    page.screenshot(path="/tmp/conductor-assistance-request-mobile.png", full_page=True)
    page.set_viewport_size({"width": 1440, "height": 1050})
    # Lose an actual committed response. Recovery is explicit and carries the same key/input.
    lost = []
    def lose_response(route):
        response = route.fetch(); lost.append(response.json()); route.abort("failed")
    endpoint = origin + "/api/v1/design-assistance"
    page.route(endpoint, lose_response)
    button(page, "Save assistance request").click()
    expect(page.get_by_role("heading", name="Retained assistance command", exact=True)).to_be_visible()
    page.unroute(endpoint, lose_response)
    assert len(lost) == 1 and len(commands) == 1 and commands[0][0] == "POST", commands
    first = commands[0]
    page.get_by_role("tab", name="Source & graph", exact=True).click(); page.get_by_role("tab", name="Review", exact=True).click()
    assert len(commands) == 1, commands
    expect(button(page, "New change")).to_be_disabled()
    button(page, "Retry same assistance command").click()
    expect(page.get_by_role("heading", name="Continue in your native assistant", exact=True)).to_be_visible()
    assert len(commands) == 2 and commands[1] == first, commands
    record = lost[0]
    assert api(path) == original
    expect(page.locator(".native-handoff")).to_contain_text(record["id"])
    expect(page.locator(".native-handoff")).to_contain_text("conductor_propose_design_sections")
    assert "connected" not in panel(page).inner_text().lower()
    proposed = "A bounded Design suggestion with <script>literal markup</script> and an escaped control \u202e sequence."
    proposal(record, {"design": proposed, "verification": ""})
    commands.clear(); button(page, "Check for suggestion").click()
    expect(page.get_by_role("heading", name="Review the suggestion", exact=True)).to_be_visible()
    expect(panel(page).get_by_text("Existing Design", exact=True)).to_be_visible()
    expect(panel(page).get_by_text("(Section absent)", exact=True)).to_be_visible()
    assert len(commands) == 1 and commands[0][0] == "GET"
    assert "person-agent" in panel(page).inner_text()
    assert "\u202e" not in panel(page).inner_text()
    # Other readers see the same saved proposal, with no requester authority.
    reviewer = login("reviewer"); inspect(reviewer)
    button(reviewer, "Browse assistance requests").click()
    button(reviewer, "Inspect assistance request " + record["id"]).click()
    expect(button(reviewer, "Preview selected suggestions")).to_be_disabled()
    expect(panel(reviewer).get_by_text("Only the human who requested this assistance may apply it.", exact=True)).to_be_visible()
    reader = login("reader"); inspect(reader)
    expect(button(reader, "Ask for design help")).to_have_count(0)
    # Select just Design; the absent verification remains absent and extensions remain exact.
    panel(page).get_by_label("Apply Verification plan", exact=True).uncheck()
    commands.clear(); button(page, "Preview selected suggestions").click()
    expect(page.get_by_role("heading", name="Complete resulting Design", exact=True)).to_be_visible()
    page.screenshot(path="/tmp/conductor-assistance-suggestion-desktop.png", full_page=True)
    page.set_viewport_size({"width": 390, "height": 844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
    page.screenshot(path="/tmp/conductor-assistance-suggestion-mobile.png", full_page=True)
    page.set_viewport_size({"width": 1440, "height": 1050})
    assert not commands
    endpoint = origin + "/api/v1/design-assistance/" + record["id"] + "/application"
    lost.clear(); page.route(endpoint, lose_response)
    button(page, "Apply selected suggestions").click()
    expect(page.get_by_role("heading", name="Retained assistance command", exact=True)).to_be_visible()
    page.unroute(endpoint, lose_response)
    assert len(commands) == 1 and commands[0][0] == "POST", commands
    application_command = commands[0]
    body = json.loads(application_command[2])
    assert body == {"requestDigest": record["digest"], "suggestionDigest": lost[0]["suggestion"]["digest"], "expectedRevision": 1,
                    "expectedDigest": original["revision"]["digest"], "sections": ["design"]}
    page.get_by_role("tab", name="Agent work", exact=True).click(); page.get_by_role("tab", name="Review", exact=True).click()
    assert len(commands) == 1
    button(page, "Retry same assistance command").click()
    expect(page.get_by_role("heading", name="Suggestions applied", exact=True)).to_be_visible()
    assert len(commands) == 2 and commands[1] == application_command, commands
    expect(page.get_by_text("INSPECTION STALE", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    expect(button(page, "Revise design")).to_be_disabled()
    revised = api(path)
    assert revised["revision"]["number"] == 2 and revised["revision"]["author"] == "person-author" and not revised["approved"]
    assert revised["revision"]["content"] == {**original["revision"]["content"], "design": proposed}
    inspect(page)
    button(page, "Request design review").click(); button(page, "Confirm design review request").click()
    expect(button(page, "Approve inspected revision")).to_be_disabled()
    inspect(reviewer); button(reviewer, "Approve inspected revision").click()
    expect(button(reviewer, "Design approval recorded")).to_be_disabled()
    inspect(page); button(page, "Inspect historical revision 1").click()
    expect(button(page, "Ask for design help")).to_be_disabled()
    inspect(page)
    # A concurrent revision cannot be silently rebased by application confirmation.
    start(page, "Improve this exact Design only.")
    stale = save_request(page); proposal(stale, {"design": "Must not overwrite a concurrent revision"})
    button(page, "Check for suggestion").click(); button(page, "Preview selected suggestions").click()
    newer = api(path + "/revisions", {"expectedRevision": 2, "content": {**revised["revision"]["content"], "design": "Another human advanced the Design"}})
    commands.clear(); button(page, "Apply selected suggestions").click()
    expect(page.get_by_text("INSPECTION STALE", exact=True)).to_be_visible()
    assert len(commands) == 1 and commands[0][0] == "POST" and json.loads(commands[0][2])["expectedRevision"] == 2
    assert api(path)["revision"]["number"] == 3
    assert api(path)["revision"]["content"] == newer["revision"]["content"]
    inspect(page)
    # Unchanged sections produce no application command.
    start(page, "Check whether any change is necessary.")
    unchanged = save_request(page); proposal(unchanged, {"design": newer["revision"]["content"]["design"]})
    button(page, "Check for suggestion").click()
    commands.clear(); button(page, "Preview selected suggestions").click()
    expect(panel(page).get_by_role("alert")).to_contain_text("do not change the saved Design")
    assert not commands
    # Malformed optional facts are unavailable, never an empty/waiting success.
    def malformed(route):
        response = route.fetch(); value = response.json(); value["suggestion"] = None
        route.fulfill(response=response, json=value)
    endpoint = origin + "/api/v1/design-assistance/" + unchanged["id"]
    page.route(endpoint, malformed); button(page, "Check for suggestion").click()
    expect(panel(page).get_by_role("alert")).to_contain_text("suggestion is incomplete")
    expect(button(page, "Preview selected suggestions")).to_have_count(0)
    page.unroute(endpoint, malformed)
    button(page, "Retry request inspection").click()
    expect(page.get_by_role("heading", name="Review the suggestion", exact=True)).to_be_visible()
    # A pending read is fenced on tab leave, including a late successful response.
    held_reads = []
    def hold_read(route):
        held_reads.append((route, route.fetch()))
    endpoint = origin + "/api/v1/design-assistance/" + unchanged["id"]
    page.route(endpoint, hold_read)
    commands.clear(); button(page, "Check for suggestion").click()
    expect(panel(page).get_by_role("status")).to_contain_text("Checking the saved request")
    page.get_by_role("tab", name="Agent work", exact=True).click(); page.get_by_role("tab", name="Review", exact=True).click()
    assert len(held_reads) == 1
    held_reads[0][0].fulfill(response=held_reads[0][1])
    expect(page.get_by_role("heading", name="Review the suggestion", exact=True)).to_have_count(0)
    assert len(commands) == 1 and commands[0][0] == "GET"
    page.unroute(endpoint, hold_read)
    # A write interrupted after commit also retains its exact recoverable input.
    start(page, "Recover an interrupted native help request.")
    button(page, "Preview assistance request").click()
    held_writes = []
    def hold_write(route):
        held_writes.append((route, route.fetch()))
    endpoint = origin + "/api/v1/design-assistance"
    page.route(endpoint, hold_write)
    commands.clear(); button(page, "Save assistance request").click()
    expect(panel(page).get_by_role("status")).to_contain_text("Saving assistance request")
    page.get_by_role("tab", name="Agent work", exact=True).click(); page.get_by_role("tab", name="Review", exact=True).click()
    expect(page.get_by_role("heading", name="Retained assistance command", exact=True)).to_be_visible()
    assert len(held_writes) == 1 and len(commands) == 1
    interrupted = held_writes[0][1].json()
    sent = commands[0]
    held_writes[0][0].fulfill(response=held_writes[0][1])
    expect(page.get_by_role("heading", name="Retained assistance command", exact=True)).to_be_visible()
    page.unroute(endpoint, hold_write)
    button(page, "Retry same assistance command").click()
    expect(page.get_by_role("heading", name="Continue in your native assistant", exact=True)).to_be_visible()
    assert len(commands) == 2 and commands[1] == sent
    expect(page.locator(".native-handoff")).to_contain_text(interrupted["id"])
    # Revoked/expired response headers clear every private assistance state.
    def denied(route):
        route.fulfill(status=403, content_type="application/json", body='{"error":{"message":"denied"}}')
    endpoint = origin + "/api/v1/design-assistance/" + interrupted["id"]
    page.route(endpoint, denied); button(page, "Check for suggestion").click()
    expect(panel(page)).to_have_count(0)
    expect(page.get_by_text(interrupted["input"]["instruction"], exact=True)).to_have_count(0)
    page.unroute(endpoint, denied)
    # Fresh scope change clears unsent private input too.
    private = login("author"); inspect(private); start(private, "Private unsent assistance text")
    private.get_by_label("Managed repository", exact=True).select_option("private")
    expect(private.get_by_label("Help wanted", exact=True)).to_have_count(0)
    expect(private.get_by_text("Private unsent assistance text", exact=True)).to_have_count(0)
    # The actual app adopts the passive brand assets/tokens with no font service.
    assert private.locator('link[rel="icon"]').get_attribute("href") == "/brand/conductor-favicon.svg"
    assert private.get_by_role("img", name="Conductor", exact=True).evaluate("img => img.complete && img.naturalWidth > 0")
    assert private.locator("h1").evaluate("e => getComputedStyle(e).fontFamily").startswith("Inter")
    assert private.locator(".panel").first.evaluate("e => getComputedStyle(e).boxShadow") == "none"
    private.goto(origin + "/brand/")
    private.screenshot(path="/tmp/conductor-brand-specimen-desktop.png", full_page=True)
    private.set_viewport_size({"width": 390, "height": 844})
    assert private.evaluate("document.documentElement.scrollWidth <= innerWidth")
    private.screenshot(path="/tmp/conductor-brand-specimen-mobile.png", full_page=True)
    assert not errors, errors
    browser.close()
print("Signed assistance request, agent proposal, selected application, shared review, stale/no-op/lost-response recovery, scope denial and Switch brand desktop/mobile checks passed.")
