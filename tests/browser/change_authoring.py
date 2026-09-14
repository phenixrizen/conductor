# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Actual signed browser/API/PostgreSQL authoring; synthetic isolated Go fixture only."""
import json
import os
import ssl
import urllib.request
from urllib.parse import urlparse
from playwright.sync_api import expect, sync_playwright

origin = os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme == "https" and urlparse(origin).hostname in {"127.0.0.1", "::1"}
legacy_id = os.environ["CONDUCTOR_BROWSER_LEGACY_ID"]

def api(path, body=None):
    req = urllib.request.Request(origin + "/api/v1" + path, data=None if body is None else json.dumps(body).encode(), headers={
        "Content-Type": "application/json", "Authorization": "Bearer " + os.environ["CONDUCTOR_BROWSER_AUTHOR_TOKEN"],
        "X-Conductor-Workspace": "team", "X-Conductor-Repository": "application"})
    with urllib.request.urlopen(req, context=ssl._create_unverified_context(), timeout=10) as result:
        return json.load(result)

with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"), headless=True)
    errors = []
    def login(identity):
        context = browser.new_context(ignore_https_errors=True, viewport={"width": 1440, "height": 1050})
        page = context.new_page(); page.set_default_timeout(10000)
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(origin); page.get_by_role("link", name="Sign in", exact=True).click()
        page.get_by_role("button", name="Sign in as " + identity, exact=True).click()
        page.get_by_label("Workspace", exact=True).select_option("team")
        page.get_by_label("Managed repository", exact=True).select_option("application")
        return page
    def inspect(page, change):
        page.get_by_label("Change ID", exact=True).fill(change)
        page.get_by_role("button", name="Inspect latest revision", exact=True).click()
        expect(page.get_by_role("button", name="Inspect latest revision", exact=True)).to_be_enabled()
    def button(page, name):
        return page.get_by_role("button", name=name, exact=True)
    def create_change(page, base_url):
        endpoint = base_url + "/api/v1/changes"
        saved = []
        def capture(route):
            assert route.request.method == "POST"
            # Read the actual server response before delivering it unchanged;
            # Chromium's separate DevTools body copy can be unavailable in CI.
            response = route.fetch(max_retries=0, max_redirects=0)
            assert response.status == 201, response.text()
            value = response.json()
            assert route.request.post_data_json == {"content": value["revision"]["content"]}
            saved.append(value)
            route.fulfill(response=response)
        page.route(endpoint, capture)
        try:
            button(page, "Create change").click()
            expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
            assert len(saved) == 1, saved
            expect(page.get_by_label("Change ID", exact=True)).to_have_value(saved[0]["id"])
            return saved[0]
        finally:
            page.unroute(endpoint, capture)
    page = login("author")
    button(page, "New change").click()
    button(page, "Preview design").click()
    expect(page.get_by_role("alert")).to_contain_text("Give the change a title")
    page.get_by_label("Change title", exact=True).fill("Synthetic readable change")
    page.get_by_label("Intended outcome", exact=True).fill("Let an engineer start a reviewed change without writing JSON.")
    page.get_by_label("Design", exact=True).fill("Keep shared immutable revisions and exact approval.")
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path="/tmp/conductor-browser-change-form-desktop.png", full_page=True)
    page.set_viewport_size({"width": 390, "height": 844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
    page.screenshot(path="/tmp/conductor-browser-change-form-mobile.png", full_page=True)
    page.set_viewport_size({"width": 1440, "height": 1050})
    # A preview is local. Leaving the workflow dismisses its confirmation without writing.
    button(page, "Preview design").click()
    page.get_by_role("tab", name="Agent work", exact=True).click()
    page.get_by_role("tab", name="Review", exact=True).click()
    expect(button(page, "Create change")).to_have_count(0)
    expect(page.get_by_label("Change title", exact=True)).to_have_value("Synthetic readable change")
    button(page, "Preview design").click()
    commands = []
    page.on("request", lambda req: commands.append((req.method, req.url, req.post_data)) if "/api/" in req.url else None)
    created = create_change(page, origin); change = created["id"]; path = "/changes/" + change
    assert created["revision"]["content"] == {"title": "Synthetic readable change", "intent": "Let an engineer start a reviewed change without writing JSON.", "design": "Keep shared immutable revisions and exact approval."}
    assert [entry[0] for entry in commands] == ["POST"], commands
    expect(page.locator(".content .package-prose").first).to_have_text("Synthetic readable change")
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path="/tmp/conductor-browser-change-authoring.png", full_page=True)
    page.screenshot(path="/tmp/conductor-browser-change-saved-desktop.png", full_page=True)
    page.set_viewport_size({"width": 390, "height": 844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path="/tmp/conductor-browser-change-saved-mobile.png", full_page=True)
    page.set_viewport_size({"width": 1440, "height": 1050})
    commands.clear()
    button(page, "Request design review").click()
    button(page, "Confirm design review request").click()
    expect(page.get_by_text("Design review requested. An independent reviewer can now inspect and approve this revision.", exact=True)).to_be_visible()
    assert len(commands) == 1 and commands[0][0] == "POST" and json.loads(commands[0][2]) == {"revision": 1}, commands
    expect(button(page, "Approve inspected revision")).to_be_disabled()
    reviewer = login("reviewer"); inspect(reviewer, change)
    button(reviewer, "Approve inspected revision").click()
    expect(button(reviewer, "Design approval recorded")).to_be_disabled()
    inspect(page, change)
    button(page, "Revise design").click()
    commands.clear()
    page.get_by_label("Scope", exact=True).fill("Temporary optional field")
    page.get_by_label("Scope", exact=True).fill("")
    button(page, "Preview design").click()
    expect(page.get_by_role("alert")).to_contain_text("No content changed")
    assert not commands, commands
    page.get_by_label("Scope", exact=True).fill("A focused browser authoring workflow.")
    button(page, "Preview design").click(); button(page, "Save new revision").click()
    expect(page.get_by_role("heading", name="Revision 2", exact=True)).to_be_visible()
    revised = api(path)
    assert not revised["approved"] and "submittedAt" not in revised["revision"]
    assert revised["revision"]["content"]["scope"] == "A focused browser authoring workflow."
    assert "tasks" not in revised["revision"]["content"]
    button(page, "Browse shared work").click()
    card = button(page, "Inspect change " + change)
    expect(card).to_contain_text("Synthetic readable change")
    expect(card).to_contain_text("Let an engineer start a reviewed change")

    inspect(reviewer, change)
    button(reviewer, "Inspect historical revision 1").click()
    expect(reviewer.get_by_text("HISTORICAL VIEW", exact=True)).to_be_visible()
    expect(button(reviewer, "Revise design")).to_have_count(0)
    expect(button(reviewer, "Request design review")).to_have_count(0)

    # All legacy fields survive, including absent, empty, null, arrays and objects.
    inspect(page, legacy_id); original = api("/changes/" + legacy_id)
    button(page, "Revise design").click()
    expect(page.get_by_label("Change title", exact=True)).to_have_count(0)
    expect(page.get_by_label("Intended outcome", exact=True)).to_have_count(0)
    page.get_by_label("Design", exact=True).fill("Temporary existing empty field")
    page.get_by_label("Design", exact=True).fill("")
    button(page, "Preview design").click()
    expect(page.get_by_role("alert")).to_contain_text("No content changed")
    page.get_by_label("Design", exact=True).fill("One explicitly changed field")
    button(page, "Preview design").click(); button(page, "Save new revision").click()
    expect(page.get_by_role("heading", name="Revision 2", exact=True)).to_be_visible()
    legacy = api("/changes/" + legacy_id)
    assert legacy["revision"]["content"] == {**original["revision"]["content"], "design": "One explicitly changed field"}

    # A stale save sends the captured version without refreshing or overwriting a concurrent edit.
    button(page, "Revise design").click()
    page.get_by_label("Design", exact=True).fill("Stale edit must not overwrite")
    button(page, "Preview design").click()
    other = {**legacy["revision"]["content"], "design": "Another engineer advanced this revision"}
    api("/changes/" + legacy_id + "/revisions", {"expectedRevision": 2, "content": other})
    commands.clear(); button(page, "Save new revision").click()
    expect(page.get_by_role("heading", name="Retained command input", exact=True)).to_be_visible()
    assert len(commands) == 1 and json.loads(commands[0][2])["expectedRevision"] == 2
    expect(button(page, "New change")).to_be_disabled()
    button(page, "Inspect saved change").click()
    expect(button(page, "Dismiss retained input after inspection")).to_be_enabled()
    assert api("/changes/" + legacy_id)["revision"]["content"] == other
    commands.clear()
    button(page, "Use retained content as replacement draft").click()
    expect(page.get_by_label("Design", exact=True)).to_have_value("Stale edit must not overwrite")
    assert not commands, commands
    button(page, "Preview design").click(); button(page, "Save new revision").click()
    expect(page.get_by_role("heading", name="Revision 4", exact=True)).to_be_visible()
    assert len(commands) == 1 and json.loads(commands[0][2])["expectedRevision"] == 3
    assert api("/changes/" + legacy_id)["revision"]["content"] == {**legacy["revision"]["content"], "design": "Stale edit must not overwrite"}

    # A real committed response is lost. Preserve input across tabs and never replay.
    button(page, "Revise design").click(); page.get_by_label("Design", exact=True).fill("Saved before acknowledgment was lost")
    button(page, "Preview design").click()
    lost = []
    def lose_response(route):
        response = route.fetch(); lost.append(response.json()); route.abort("failed")
    endpoint = origin + "/api/v1/changes/" + legacy_id + "/revisions"
    page.route(endpoint, lose_response)
    commands.clear(); button(page, "Save new revision").click()
    expect(page.get_by_role("heading", name="Retained command input", exact=True)).to_be_visible()
    page.unroute(endpoint, lose_response)
    page.get_by_role("tab", name="Agent work", exact=True).click(); page.get_by_role("tab", name="Review", exact=True).click()
    expect(page.get_by_role("heading", name="Retained command input", exact=True)).to_be_visible()
    assert len(lost) == 1 and len(commands) == 1 and lost[0]["revision"]["number"] == 5
    button(page, "Inspect saved change").click()
    expect(page.get_by_role("heading", name="Revision 5", exact=True)).to_be_visible()
    commands.clear(); button(page, "Use retained content as replacement draft").click()
    button(page, "Preview design").click()
    expect(page.get_by_role("alert")).to_contain_text("No content changed")
    assert not commands, commands
    button(page, "Discard unsent draft").click()

    # Scope changes erase unsent draft text, and readers cannot author.
    button(page, "New change").click(); page.get_by_label("Change title", exact=True).fill("Private unsaved text")
    page.get_by_label("Managed repository", exact=True).select_option("private")
    expect(page.get_by_role("region", name="Change authoring", exact=True)).to_have_count(0)
    expect(page.get_by_text("Private unsaved text", exact=True)).to_have_count(0)
    # A first create can fail before commit in an empty repository. Explicit list
    # inspection enables deliberate abandonment, without pretending absence proves failure.
    held_discovery = []
    def hold_discovery(route):
        response = route.fetch(max_retries=0, max_redirects=0)
        assert response.status == 200, response.text()
        held_discovery.append((route, response))
        page.evaluate("window.authoringDiscoveryCaptured = true")
    page.route(origin + "/api/v1/changes?*", hold_discovery)
    button(page, "Browse shared work").click()
    expect(button(page, "Browse shared work")).to_be_disabled()
    # Retain the earlier server read before starting the creation. A disabled
    # button alone does not establish that the intercepted read has finished.
    page.wait_for_function("window.authoringDiscoveryCaptured === true")
    button(page, "New change").click()
    page.get_by_label("Change title", exact=True).fill("First private change")
    page.get_by_label("Intended outcome", exact=True).fill("Recover a request that failed before reaching the server")
    button(page, "Preview design").click()
    creation_requests = []
    def fail_creation(route):
        assert route.request.method == "POST"
        creation_requests.append(route.request.post_data)
        route.abort("failed")
    page.route(origin + "/api/v1/changes", fail_creation)
    button(page, "Create change").click()
    expect(page.get_by_role("heading", name="Retained command input", exact=True)).to_be_visible()
    page.unroute(origin + "/api/v1/changes", fail_creation)
    expect(button(page, "Dismiss creation input after checking shared work")).to_be_disabled()
    assert len(held_discovery) == 1
    held_discovery[0][0].fulfill(response=held_discovery[0][1])
    expect(button(page, "Browse shared work")).to_be_enabled()
    expect(page.get_by_text("No shared work matches this repository filter.", exact=True)).to_be_visible()
    expect(button(page, "Dismiss creation input after checking shared work")).to_be_disabled()
    expect(button(page, "Edit retained input as a separate new change")).to_be_disabled()
    page.unroute(origin + "/api/v1/changes?*", hold_discovery)
    button(page, "Browse shared work").click()
    expect(page.get_by_text("No shared work matches this repository filter.", exact=True)).to_be_visible()
    expect(button(page, "Dismiss creation input after checking shared work")).to_be_enabled()
    button(page, "Edit retained input as a separate new change").click()
    expect(page.get_by_label("Change title", exact=True)).to_have_value("First private change")
    expect(page.get_by_text("This is a separate new change using retained input. The earlier creation may still have committed; saving again can create a duplicate.", exact=True)).to_be_visible()
    assert len(creation_requests) == 1
    reader = login("reader")
    expect(button(reader, "New change")).to_have_count(0)
    inspect(reader, change)
    expect(button(reader, "Revise design")).to_have_count(0)
    expect(button(reader, "Request design review")).to_have_count(0)
    # Local actors are normalized like the server; authored text is not trimmed.
    local_origin = os.environ["CONDUCTOR_BROWSER_LOCAL_URL"]
    assert urlparse(local_origin).scheme == "http" and urlparse(local_origin).hostname == "127.0.0.1"
    local = browser.new_context(viewport={"width": 1100, "height": 900}).new_page()
    local.on("pageerror", lambda error: errors.append(str(error)))
    local.goto(local_origin)
    local.get_by_label("Local reviewer", exact=True).fill("  local-author  ")
    button(local, "New change").click()
    local.get_by_label("Change title", exact=True).fill("  Retain exact title text  ")
    local.get_by_label("Intended outcome", exact=True).fill("  Preserve author content whitespace  ")
    local.get_by_label("Design", exact=True).fill("<script>window.unsafeDesign = true</script> \u202e")
    button(local, "Preview design").click()
    local_record = create_change(local, local_origin)
    assert local_record["revision"]["content"]["design"] == "<script>window.unsafeDesign = true</script> \u202e"
    expect(local.locator(".content")).to_contain_text("\\u202e")
    assert local.evaluate("window.unsafeDesign === undefined")
    assert local_record["revision"]["author"] == "local-author"
    assert local_record["revision"]["content"]["title"] == "  Retain exact title text  "
    expect(local.get_by_role("heading", name="Retained command input", exact=True)).to_have_count(0)
    assert not errors, errors
    browser.close()
print("PASS: signed browser create/submit/independent approval/revise, readable discovery, structured legacy preservation, stale and lost-write recovery, tab/scope clearing and reader controls; screenshot /tmp/conductor-browser-change-authoring.png")
