# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Signed browser/API/PostgreSQL inspection of explicitly synthetic telemetry."""
import json
from datetime import datetime, timezone
import os
from urllib.parse import urlparse
from playwright.sync_api import expect, sync_playwright

expect.set_options(timeout=15000)
origin = os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme == "https" and urlparse(origin).hostname in {"127.0.0.1", "::1"}
shared_id = os.environ["CONDUCTOR_BROWSER_RUNTIME_ID"]
shared_url = origin + "/api/v1/runtime-evidence/" + shared_id

def sign_in(page, who):
    page.goto(origin)
    page.get_by_role("link", name="Sign in", exact=True).click()
    page.get_by_role("button", name="Sign in as " + who, exact=True).click()
    expect(page.locator(".signed-in-identity")).to_contain_text("person-" + who)
    page.get_by_label("Workspace", exact=True).select_option("team")
    page.get_by_label("Managed repository", exact=True).select_option("application")

with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"), headless=True)
    context = browser.new_context(ignore_https_errors=True, viewport={"width":1440,"height":1100})
    page = context.new_page(); page.set_default_timeout(15000)
    errors = []; page.on("pageerror", lambda e: errors.append(str(e)))
    sign_in(page, "reviewer"); panel = page.locator(".runtime-evidence")
    panel.get_by_role("button", name="Refresh runtime evidence", exact=True).click()
    panel.get_by_role("button", name="Inspect runtime evidence " + shared_id, exact=True).click()
    inspected = panel.get_by_role("article", name="Inspected runtime evidence")
    expect(inspected).to_contain_text(os.environ["CONDUCTOR_BROWSER_RUNTIME_DIGEST"])
    expect(inspected.get_by_role("region", name="Criterion latency", exact=True)).to_contain_text("Met in the inspected window")
    expect(inspected.get_by_role("region", name="Criterion strict-latency", exact=True)).to_contain_text("Not met in the inspected window")
    expect(inspected.get_by_role("region", name="Criterion two-series", exact=True)).to_contain_text("Not verified")
    expect(inspected).to_contain_text("Broader production outcome: not verified")
    expect(inspected.get_by_role("region", name="Runtime execution observation")).to_contain_text("Progress unknown")
    logs = inspected.get_by_role("region", name="Runtime signal logs", exact=True)
    logs.locator("summary").click()
    expect(logs).to_contain_text("\\u001b[2J")
    expect(logs).to_contain_text("\\u202e")
    assert page.evaluate("window.runtimeInjected === undefined")
    expect(inspected.get_by_role("region", name="Runtime signal traces", exact=True)).to_contain_text("truncated")
    # A response claiming a different approved criterion must not remain visible.
    def wrong_policy(route):
        response = route.fetch(); assert response.status == 200
        value = response.json(); value["receipt"]["evaluations"][0]["criterionDigest"] = "f" * 64
        route.fulfill(response=response, json=value)
    page.route(shared_url, wrong_policy)
    panel.get_by_role("button", name="Refresh inspected runtime evidence", exact=True).click()
    expect(panel.get_by_role("alert")).to_contain_text("do not match the inspected policy")
    expect(inspected).to_have_count(0)
    page.unroute(shared_url, wrong_policy)
    panel.get_by_role("button", name="Inspect shared runtime evidence", exact=True).click()
    expect(inspected).to_be_visible()
    # The local display clock ages the immutable result without a provider query.
    page.clock.install(); page.clock.fast_forward(660000)
    expect(inspected).to_contain_text("Stale runtime window")
    expect(inspected.get_by_role("region", name="Criterion latency", exact=True)).to_contain_text("Met in the inspected window")
    page.clock.set_fixed_time(datetime.now(timezone.utc))
    panel.locator("summary").filter(has_text="Request evidence for an inspected deployment").click()
    panel.get_by_label("Runtime request JSON", exact=True).fill('{"deliveryId":"a","deliveryId":"b"}')
    panel.get_by_role("button", name="Preview runtime request", exact=True).click()
    expect(panel.get_by_role("alert")).to_contain_text("Duplicate")
    panel.get_by_label("Runtime request JSON file", exact=True).set_input_files(os.environ["CONDUCTOR_BROWSER_RUNTIME_FILE"])
    preview = panel.get_by_role("region", name="Runtime request preview")
    expect(preview).to_be_visible()
    expected = json.load(open(os.environ["CONDUCTOR_BROWSER_RUNTIME_FILE"]))
    expect(preview).to_contain_text(expected["deliveryDigest"])
    collection_url = origin + "/api/v1/runtime-evidence"
    attempts = []; requests = []
    def capture(request):
        if "/api/" in request.url: requests.append((request.method, urlparse(request.url).path))
    def lose_creation(route):
        response = route.fetch(); assert response.status == 202, response.text()
        attempts.append((route.request.post_data_json, route.request.headers["idempotency-key"], response.json()))
        route.abort()
    page.on("request", capture); page.route(collection_url, lose_creation)
    panel.get_by_role("button", name="Record runtime request", exact=True).click()
    expect(panel.get_by_role("button", name="Retry exact runtime request", exact=True)).to_be_visible()
    assert len(attempts) == 1 and requests == [("POST", "/api/v1/runtime-evidence")], requests
    page.unroute(collection_url, lose_creation)
    with page.expect_request(lambda r: r.method == "POST" and r.url == collection_url) as retry:
        panel.get_by_role("button", name="Retry exact runtime request", exact=True).click()
    expect(panel.get_by_text("Runtime request recorded.", exact=False)).to_be_visible()
    assert retry.value.post_data_json == attempts[0][0] == expected
    assert retry.value.headers["idempotency-key"] == attempts[0][1]
    assert requests == [("POST", "/api/v1/runtime-evidence")] * 2, requests
    page.remove_listener("request", capture)
    expect(inspected).to_contain_text("No retained telemetry receipt")
    expect(inspected.get_by_role("region", name="Retained runtime receipt")).to_have_count(0)
    panel.get_by_label("Runtime evidence ID", exact=True).fill(shared_id)
    panel.get_by_role("button", name="Inspect shared runtime evidence", exact=True).click()
    expect(inspected.get_by_role("region", name="Retained runtime receipt")).to_be_visible()
    page.screenshot(path="/tmp/conductor-runtime-workbench.png", full_page=True)
    page.set_viewport_size({"width":390,"height":844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "mobile runtime overflow"
    page.screenshot(path="/tmp/conductor-runtime-workbench-mobile.png", full_page=True)
    page.route(shared_url, lambda route: route.fulfill(status=403, body=""))
    panel.get_by_role("button", name="Refresh inspected runtime evidence", exact=True).click()
    expect(inspected).to_have_count(0)
    expect(panel.get_by_role("region", name="Runtime request preview")).to_have_count(0)
    context.close()
    context = browser.new_context(ignore_https_errors=True)
    page = context.new_page(); sign_in(page, "reader"); panel = page.locator(".runtime-evidence")
    expect(panel.get_by_label("Runtime request JSON file", exact=True)).to_have_count(0)
    panel.get_by_label("Runtime evidence ID", exact=True).fill(shared_id)
    panel.get_by_role("button", name="Inspect shared runtime evidence", exact=True).click()
    expect(panel.get_by_role("article", name="Inspected runtime evidence")).to_be_visible()
    page.get_by_label("Managed repository", exact=True).select_option("")
    expect(page.get_by_role("article", name="Inspected runtime evidence")).to_have_count(0)
    assert not errors, errors
    context.close(); browser.close()
print("Runtime browser acceptance passed: exact policy, explicit gaps, stale display, same-key retry, isolation and escaped source.")
