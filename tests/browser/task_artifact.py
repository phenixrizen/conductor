# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Complete failed report inspection requires neither a patch nor publication."""
import os
from urllib.parse import urlparse, parse_qs
from playwright.sync_api import expect, sync_playwright
expect.set_options(timeout=15000)
origin = os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme == "https" and urlparse(origin).hostname in {"127.0.0.1", "::1"}
run_id = os.environ["CONDUCTOR_BROWSER_REPORT_RUN"]
with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"), headless=True)
    context = browser.new_context(ignore_https_errors=True, viewport={"width":1440,"height":1100})
    page = context.new_page(); page.set_default_timeout(15000)
    errors = []; page.on("pageerror", lambda e: errors.append(str(e)))
    page.goto(origin); page.get_by_role("link", name="Sign in", exact=True).click()
    page.get_by_role("button", name="Sign in as reader", exact=True).click()
    expect(page.locator(".signed-in-identity")).to_contain_text("person-reader")
    page.get_by_label("Workspace", exact=True).select_option("team")
    page.get_by_label("Managed repository", exact=True).select_option("application")
    panel = page.locator(".coordinated-runs")
    panel.get_by_label("Run ID", exact=True).fill(run_id)
    panel.get_by_role("button", name="Inspect shared run", exact=True).click()
    request_paths = []
    def capture(request):
        if "/api/" in request.url: request_paths.append(request)
    page.on("request", capture)
    panel.get_by_role("button", name="Inspect task artifact code", exact=True).click()
    artifact = panel.get_by_role("region", name="Inspected task artifact", exact=True)
    expect(artifact).to_contain_text("No repository patches retained")
    expect(artifact.get_by_role("region", name="Producer evidence", exact=True)).to_contain_text("failed")
    expect(artifact).to_contain_text("Unknown; publication blocked")
    expect(artifact).to_contain_text("No independent check results retained")
    expect(artifact).to_contain_text("\\u001b[2J")
    expect(artifact).to_contain_text("\\u202e")
    assert page.evaluate("window.reportInjected === undefined")
    assert len(request_paths) == 1 and request_paths[0].method == "GET"
    expected = {"runDigest":[os.environ["CONDUCTOR_BROWSER_REPORT_DIGEST"]], "taskId":[os.environ["CONDUCTOR_BROWSER_REPORT_TASK"]], "artifactDigest":[os.environ["CONDUCTOR_BROWSER_REPORT_ARTIFACT"]]}
    assert parse_qs(urlparse(request_paths[0].url).query) == expected
    assert request_paths[0].headers["x-conductor-repository"] == "application"
    page.remove_listener("request", capture)
    artifact_url = request_paths[0].url
    def corrupt(route):
        response = route.fetch(); assert response.status == 200
        value = response.json(); value["artifact"]["producer"]["output"] = "Substituted report"
        route.fulfill(response=response, json=value)
    page.route(artifact_url, corrupt)
    panel.get_by_role("button", name="Inspect task artifact code", exact=True).click()
    expect(panel.get_by_role("alert")).to_contain_text("does not match its retained output digest")
    expect(artifact).to_have_count(0)
    page.unroute(artifact_url, corrupt)
    panel.get_by_role("button", name="Inspect task artifact code", exact=True).click()
    expect(artifact).to_be_visible()
    page.screenshot(path="/tmp/conductor-task-artifact.png", full_page=True)
    page.set_viewport_size({"width":390,"height":844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "mobile report overflow"
    page.screenshot(path="/tmp/conductor-task-artifact-mobile.png", full_page=True)
    page.route(artifact_url, lambda route: route.fulfill(status=403, body=""))
    panel.get_by_role("button", name="Inspect task artifact code", exact=True).click()
    expect(artifact).to_have_count(0)
    expect(panel.get_by_role("article", name="Inspected coordinated run")).to_have_count(0)
    assert not errors, errors
    context.close(); browser.close()
print("Failed report browser acceptance passed: exact receipt, complete escaped output, altered-output denial and source revocation.")
