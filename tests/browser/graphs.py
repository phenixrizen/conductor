# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Actual browser/shared PostgreSQL graph workflow using retained source fixtures."""
import os
from urllib.parse import urlparse, parse_qs
from playwright.sync_api import expect, sync_playwright

origin = os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme == "https" and urlparse(origin).hostname in {"127.0.0.1", "::1"}
collection = os.environ["CONDUCTOR_BROWSER_COLLECTION_ID"]
other = os.environ["CONDUCTOR_BROWSER_OTHER_COLLECTION_ID"]
bundle = os.environ["CONDUCTOR_BROWSER_BUNDLE_DIGEST"]

def sign_in(page, identity):
    page.goto(origin)
    page.get_by_role("link", name="Sign in", exact=True).click()
    page.get_by_role("button", name="Sign in as " + identity, exact=True).click()
    expect(page.locator(".signed-in-identity")).to_contain_text("person-" + identity)
    page.get_by_label("Workspace", exact=True).select_option("team")
    page.get_by_label("Managed repository", exact=True).select_option("application")

with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"), headless=True)
    context = browser.new_context(ignore_https_errors=True, viewport={"width":1440,"height":1100})
    page = context.new_page()
    page.set_default_timeout(8000)
    errors=[]
    page.on("pageerror", lambda e: errors.append(str(e)))
    sign_in(page,"reviewer")
    panel=page.locator(".repository-graphs")
    panel.get_by_role("button",name="Refresh graphs",exact=True).click()
    expect(panel.get_by_text("No shared graphs are available in this scope.",exact=True)).to_be_visible()
    panel.locator("summary").filter(has_text="Build a graph from inspected receipts").click()
    panel.get_by_role("button",name="Load graph repositories",exact=True).click()
    expect(panel.get_by_role("button",name="Load graph repositories",exact=True)).to_be_enabled()
    assert not panel.get_by_role("alert").count(), panel.inner_text()
    for repo, receipt in [("application",collection),("library",other)]:
        panel.get_by_label("Source repository",exact=True).select_option(repo)
        panel.get_by_role("button",name="Load source receipts",exact=True).click()
        panel.get_by_role("button",name="Inspect source "+receipt,exact=True).click()
        expect(panel.locator(".source-inspection")).to_contain_text(bundle)
        expect(panel.get_by_label("Use the retained whole-repository index",exact=True)).to_be_checked()
        panel.get_by_role("button",name="Add inspected source",exact=True).click()
    expect(panel.get_by_role("list",name="Graph sources to record").get_by_role("listitem")).to_have_count(2)
    writes=[]
    commands=[]
    def lost_ack(route):
        writes.append((route.request.post_data_json,route.request.headers["idempotency-key"]))
        response=route.fetch()
        assert response.status==201, response.text()
        writes[-1]+=(response.json(),)
        route.abort()
    def capture(request):
        if "/api/" in request.url: commands.append((request.method,urlparse(request.url).path))
    page.route(origin+"/api/v1/repository-graphs",lost_ack)
    page.on("request",capture)
    panel.get_by_role("button",name="Record shared graph",exact=True).click()
    expect(panel.get_by_role("button",name="Retry exact graph request",exact=True)).to_be_visible()
    assert len(writes)==1 and commands==[("POST","/api/v1/repository-graphs")],commands
    assert all(s["fullSourceDigest"]==bundle for s in writes[0][0]["sources"])
    page.unroute(origin+"/api/v1/repository-graphs",lost_ack)
    with page.expect_request(lambda r:r.method=="POST" and r.url==origin+"/api/v1/repository-graphs") as retry:
        panel.get_by_role("button",name="Retry exact graph request",exact=True).click()
    expect(panel.get_by_role("heading",name="Inspected repository graph",exact=True)).to_be_visible()
    assert retry.value.post_data_json==writes[0][0] and retry.value.headers["idempotency-key"]==writes[0][1]
    assert commands==[("POST","/api/v1/repository-graphs")]*2,commands
    page.remove_listener("request",capture)
    graph=writes[0][2]
    expect(panel.locator(".graph-inspection")).to_contain_text(graph["digest"])
    expect(panel.locator(".graph-gaps")).to_contain_text("unsupported")
    panel.get_by_label("Find a symbol or path",exact=True).fill("fixture.go")
    panel.get_by_role("button",name="Search relationships",exact=True).click()
    expect(panel.get_by_role("region",name="Graph query results")).to_contain_text("fixture.go")
    expect(panel.get_by_role("region",name="Graph query results")).to_contain_text("library")
    panel.get_by_role("button",name="Inspect relations",exact=True).first.click()
    expect(panel.get_by_role("button",name="Search relationships",exact=True)).to_be_enabled()
    panel.get_by_label("Retained source repository",exact=True).select_option("library")
    panel.get_by_label("Retained source path",exact=True).fill("fixture.go")
    with page.expect_request(lambda r:"/artifact?" in r.url) as source_request:
        panel.get_by_role("button",name="Read graph source",exact=True).click()
    source_view=panel.get_by_role("region",name="Inspected graph source text")
    expect(source_view).to_contain_text("package fixture")
    expect(source_view).to_contain_text("full_source")
    source=next(s for s in graph["snapshot"]["sources"] if s["repositoryId"]=="library")
    assert parse_qs(urlparse(source_request.value.url).query)=={k:[v] for k,v in {"graphDigest":graph["digest"],"repositoryId":"library","collectionId":source["collectionId"],"receiptDigest":source["digest"],"fullSourceDigest":source["fullSourceDigest"],"path":"fixture.go"}.items()}
    assert source_request.value.headers["x-conductor-repository"]=="application"
    artifact_pattern=origin+"/api/v1/repository-graphs/"+graph["id"]+"/artifact?*"
    def tamper_source(route):
        response=route.fetch();value=response.json();value["artifact"]["text"]="forged source";route.fulfill(response=response,json=value)
    page.route(artifact_pattern,tamper_source)
    panel.get_by_role("button",name="Read graph source",exact=True).click()
    expect(panel.get_by_role("alert")).to_contain_text("does not match its retained text digest")
    expect(source_view).to_have_count(0)
    page.unroute(artifact_pattern,tamper_source)
    panel.get_by_label("Retained source path",exact=True).fill("not-retained.go")
    panel.get_by_role("button",name="Read graph source",exact=True).click()
    expect(source_view).to_contain_text("No complete source text is retained")
    panel.get_by_label("Retained source path",exact=True).fill("fixture.go")
    panel.get_by_role("button",name="Read graph source",exact=True).click()
    expect(source_view).to_contain_text("package fixture")
    page.screenshot(path="/tmp/conductor-graph-workbench.png",full_page=True)
    page.set_viewport_size({"width":390,"height":844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), page.evaluate("Array.from(document.querySelectorAll('*')).filter(e=>e.getBoundingClientRect().right>innerWidth).map(e=>({tag:e.tagName,cls:e.className,text:e.textContent.slice(0,80)})).slice(-15)")
    page.screenshot(path="/tmp/conductor-graph-workbench-mobile.png",full_page=True)
    page.set_viewport_size({"width":1440,"height":1100})
    # Scope changes clear source inspection and every captured graph input.
    page.get_by_label("Managed repository",exact=True).select_option("library")
    expect(panel.get_by_role("heading",name="Inspected repository graph",exact=True)).to_have_count(0)
    expect(panel.get_by_role("list",name="Graph sources to record").get_by_role("listitem")).to_have_count(0)
    page.get_by_label("Managed repository",exact=True).select_option("application")
    # The collection control sends the complete explicitly chosen source intent.
    page.locator("summary").filter(has_text="Request repository context").click()
    page.get_by_label("Exact commit",exact=True).fill(os.environ["CONDUCTOR_BROWSER_COMMIT"])
    page.get_by_label("Explicit paths (one per line)",exact=True).fill("README.md")
    page.get_by_label("Idempotency key",exact=True).fill("browser-whole-source-request")
    page.get_by_label("Include whole-repository source for graph and coding work",exact=True).check()
    with page.expect_request(lambda r:r.method=="POST" and r.url==origin+"/api/v1/context-collections") as requested:
        page.get_by_role("button",name="Request collection",exact=True).click()
    expect(page.get_by_text("Collection request recorded.",exact=False)).to_be_visible()
    assert requested.value.post_data_json=={"commit":os.environ["CONDUCTOR_BROWSER_COMMIT"],"paths":["README.md"],"fullSource":True}
    panel.get_by_role("button",name="Refresh graphs",exact=True).click()
    panel.get_by_role("button",name="Inspect graph "+graph["id"],exact=True).click()
    expect(panel.get_by_role("heading",name="Inspected repository graph",exact=True)).to_be_visible()
    panel.get_by_label("Retained source repository",exact=True).select_option("library")
    panel.get_by_label("Retained source path",exact=True).fill("fixture.go")
    panel.get_by_role("button",name="Read graph source",exact=True).click()
    expect(panel.get_by_role("region",name="Inspected graph source text")).to_contain_text("package fixture")
    # A hidden source is a decisive denial: discard graph and source text immediately.
    graph_url=origin+"/api/v1/repository-graphs/"+graph["id"]
    page.route(graph_url+"/artifact?*", lambda route:route.fulfill(status=404,content_type="application/json",body='{"error":{"code":"not_found","message":"Resource unavailable","correlationId":"synthetic"}}'))
    panel.get_by_role("button",name="Read graph source",exact=True).click()
    expect(panel.get_by_role("heading",name="Inspected repository graph",exact=True)).to_have_count(0)
    expect(panel.get_by_role("region",name="Inspected graph source text")).to_have_count(0)
    expect(panel.get_by_role("button",name="Refresh graphs",exact=True)).to_be_enabled()
    context.close()
    reader_context=browser.new_context(ignore_https_errors=True)
    reader=reader_context.new_page()
    sign_in(reader,"reader")
    shared=reader.locator(".repository-graphs")
    shared.get_by_role("button",name="Refresh graphs",exact=True).click()
    shared.get_by_role("button",name="Inspect graph "+graph["id"],exact=True).click()
    expect(shared.locator(".graph-inspection")).to_contain_text(graph["digest"])
    expect(shared.get_by_role("button",name="Record shared graph",exact=True)).to_have_count(0)
    reader_context.close()
    assert not errors,errors
    browser.close()
    print("PASS: two-repository source inspection, explicit whole-source request, exact graph creation/retry without refresh, graph query, exact related source reads, tampered source denial, coverage gaps, shared read-only access, scope clearing, denial clearing, desktop/mobile screenshots")
