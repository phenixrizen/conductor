# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Run only through the isolated Go HTTPS/issuer/PostgreSQL acceptance harness."""
import json
import os
from pathlib import Path
import ssl
import urllib.parse
import urllib.request

from playwright.sync_api import expect, sync_playwright

origin = os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
change_id = os.environ["CONDUCTOR_BROWSER_CHANGE_ID"]
author_token = os.environ["CONDUCTOR_BROWSER_AUTHOR_TOKEN"]
parsed = urllib.parse.urlparse(origin)
assert parsed.scheme == "https" and parsed.hostname in {"127.0.0.1", "::1"}, "requires the isolated loopback HTTPS fixture"
change_path = "/api/v1/changes/" + urllib.parse.quote(change_id, safe="")


def author_command(path, body):
    request = urllib.request.Request(
        origin + path, data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + author_token,
                 "X-Conductor-Workspace": "team", "X-Conductor-Repository": "application"},
    )
    # Only this synthetic loopback fixture uses httptest's local certificate.
    with urllib.request.urlopen(request, context=ssl._create_unverified_context(), timeout=10) as response:
        return json.load(response)


with sync_playwright() as playwright:
    browser = playwright.chromium.launch(
        executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"), headless=True)
    context = browser.new_context(ignore_https_errors=True, viewport={"width": 1440, "height": 1100})
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(origin)
    expect(page.get_by_role("heading", name="Sign in to shared review", exact=True)).to_be_visible()
    page.get_by_role("link", name="Sign in", exact=True).click()
    page.get_by_role("button", name="Sign in as reviewer", exact=True).click()
    expect(page.locator(".signed-in-identity")).to_contain_text("person-reviewer")
    assert "conductor-session" not in page.evaluate("document.cookie"), "session cookie exposed to JavaScript"
    assert page.evaluate("localStorage.length + sessionStorage.length") == 0, "credentials persisted in browser storage"

    workspace = page.get_by_label("Workspace", exact=True)
    repository = page.get_by_label("Managed repository", exact=True)
    workspace.select_option("team")
    expect(repository).to_be_enabled()
    repository.select_option("application")
    page.get_by_role("button", name="Browse shared work", exact=True).click()
    page.get_by_role("button", name="Inspect change " + change_id, exact=True).click()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    approve = page.get_by_role("button", name="Approve inspected revision", exact=True)
    expect(approve).to_be_enabled()
    page.wait_for_load_state("networkidle")

    # Another engineer edits the shared package. The stale action must send the
    # originally inspected tuple and perform no hidden GET inside approval.
    author_command(change_path + "/revisions", {"expectedRevision": 1, "content": {
        "intent": {"title": "Changed by another authenticated engineer"}, "futureField": {"retained": True}}})
    author_command(change_path + "/review-requests", {"revision": 2})
    requests = []
    capture = lambda request: requests.append((request.method, request.url))
    page.on("request", capture)
    approve.click()
    expect(page.get_by_text("Renewed inspection required.", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    expect(approve).to_be_disabled()
    assert not any(method == "GET" and "/api/" in url for method, url in requests), requests
    page.remove_listener("request", capture)

    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name="Revision 2", exact=True)).to_be_visible()
    approve.click()
    expect(page.get_by_text("DESIGN APPROVED", exact=True)).to_be_visible()
    author_command(change_path + "/revisions", {"expectedRevision": 2, "content": {
        "intent": {"title": "Third revision awaits independent review"}, "futureField": {"retained": True}}})
    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name="Revision 3", exact=True)).to_be_visible()
    page.get_by_role("button", name="Inspect historical revision 2", exact=True).click()
    expect(page.get_by_text("HISTORICAL VIEW", exact=True)).to_be_visible()
    expect(page.get_by_text("Historical approval for revision 2", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name="Approve inspected revision", exact=True)).to_have_count(0)

    screenshot = Path(os.environ.get("CONDUCTOR_BROWSER_SCREENSHOT", "/tmp/conductor-authenticated-workbench.png"))
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path=str(screenshot), full_page=True)
    page.set_viewport_size({"width": 390, "height": 844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "mobile horizontal overflow"
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path=str(screenshot.with_name(screenshot.stem + "-mobile.png")), full_page=True)
    page.set_viewport_size({"width": 1440, "height": 1100})

    # Repository and workspace changes discard content, history, comparison,
    # and approval state before a user can act in a different scope.
    repository.select_option("private")
    expect(page.get_by_text("HISTORICAL VIEW", exact=True)).to_have_count(0)
    expect(page.get_by_role("heading", name="Revision 2", exact=True)).to_have_count(0)
    expect(page.get_by_label("Change ID", exact=True)).to_have_value("")
    workspace.select_option("empty-team")
    expect(page.get_by_label("Change ID", exact=True)).to_have_count(0)
    expect(page.get_by_text("No readable repositories are available in this workspace. Ask a workspace operator for access.", exact=True)).to_be_visible()
    workspace.select_option("team")
    repository.select_option("application")
    page.get_by_label("Change ID", exact=True).fill(change_id)
    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name="Revision 3", exact=True)).to_be_visible()
    page.wait_for_load_state("networkidle")

    # Changing accounts in another tab changes the HttpOnly session cookie. The
    # old tab's captured CSRF must be rejected and its previous inspection hidden.
    second = context.new_page()
    second.goto(origin + "/api/v1/auth/login")
    second.get_by_role("button", name="Sign in as author", exact=True).click()
    expect(second.locator(".signed-in-identity")).to_contain_text("person-author")
    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name="Check your access", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name="Revision 3", exact=True)).to_have_count(0)
    page.get_by_role("button", name="Reload session", exact=True).click()
    expect(page.locator(".signed-in-identity")).to_contain_text("person-author")
    expect(page.get_by_label("Workspace", exact=True)).to_have_value("")
    page.get_by_role("button", name="Sign out", exact=True).click()
    expect(page.get_by_role("heading", name="Sign in to shared review", exact=True)).to_be_visible()
    expect(page.get_by_label("Change ID", exact=True)).to_have_count(0)
    assert not errors, errors
    browser.close()
    print("PASS: real code-flow login, scoped discovery, stale approval without refresh, exact independent approval, historical records, scope clearing, account-switch isolation, logout, and mobile layout")
    print(f"Screenshots: {screenshot}, {screenshot.with_name(screenshot.stem + '-mobile.png')}")
