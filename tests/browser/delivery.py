# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Real browser publication review with retained synthetic execution evidence."""
import json
import os
from urllib.parse import urlparse
from playwright.sync_api import expect, sync_playwright
origin=os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme=="https" and urlparse(origin).hostname in {"127.0.0.1","::1"}
initial_id=os.environ["CONDUCTOR_BROWSER_DELIVERY_ID"]
input_value=json.loads(os.environ["CONDUCTOR_BROWSER_DELIVERY_INPUT"])

def sign_in(page,who):
    page.goto(origin)
    page.get_by_role("link",name="Sign in",exact=True).click()
    page.get_by_role("button",name="Sign in as "+who,exact=True).click()
    expect(page.locator(".signed-in-identity")).to_contain_text("person-"+who)
    page.get_by_label("Workspace",exact=True).select_option("team")
    page.get_by_label("Managed repository",exact=True).select_option("application")

with sync_playwright() as p:
    browser=p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME","/usr/bin/google-chrome"),headless=True)
    context=browser.new_context(ignore_https_errors=True,viewport={"width":1440,"height":1100})
    page=context.new_page();page.set_default_timeout(15000)
    errors=[];page.on("pageerror",lambda e:errors.append(str(e)))
    sign_in(page,"reviewer");panel=page.locator(".repository-deliveries")
    panel.get_by_role("button",name="Refresh deliveries and access",exact=True).click()
    panel.get_by_role("button",name="Inspect delivery "+initial_id,exact=True).click()
    inspected=panel.get_by_role("article",name="Inspected delivery")
    expect(inspected).to_contain_text(os.environ["CONDUCTOR_BROWSER_DELIVERY_DIGEST"])
    expect(panel.get_by_role("button",name="Authorize inspected publication",exact=True)).to_be_disabled()
    artifact_url=origin+"/api/v1/repository-deliveries/"+initial_id+"/artifact"
    def corrupt_artifact(route):
        response=route.fetch();assert response.status==200,response.text()
        value=response.json();value["artifact"]["patches"][0]["patch"]="Zm9yZ2Vk"
        route.fulfill(response=response,json=value)
    page.route(artifact_url,corrupt_artifact)
    panel.get_by_role("button",name="Inspect complete implementation artifact",exact=True).click()
    expect(panel.get_by_role("alert")).to_contain_text("does not match its retained byte digest")
    expect(panel.get_by_role("button",name="Authorize inspected publication",exact=True)).to_be_disabled()
    page.unroute(artifact_url,corrupt_artifact)
    panel.get_by_role("button",name="Inspect complete implementation artifact",exact=True).click()
    expect(panel.get_by_role("region",name="Inspected implementation artifact")).to_be_visible()
    expect(panel.get_by_role("region",name="Patch for application")).to_contain_text(os.environ["CONDUCTOR_BROWSER_PATCH_BYTES"]+" bytes")
    assert page.evaluate("window.untrustedExecuted === undefined")
    expect(panel.locator(".patch-output")).to_contain_text("\\u001b[2J")
    panel.locator("summary").filter(has_text="Propose publication from a retained task artifact").click()
    for key,label in [("runId","Implementation run ID"),("taskId","Retained task ID"),("artifactDigest","Implementation artifact digest"),("baseBranch","Target base branch"),("title","Draft PR or MR title"),("description","Draft PR or MR description")]:
        panel.get_by_label(label,exact=True).fill(input_value[key])
    panel.get_by_role("button",name="Preview publication proposal",exact=True).click()
    expect(panel.get_by_role("region",name="Publication proposal preview")).to_contain_text(input_value["artifactDigest"])
    proposal_url=origin+"/api/v1/repository-deliveries"
    proposals=[];requests=[]
    def capture(request):
        if "/api/" in request.url:requests.append((request.method,urlparse(request.url).path,request.post_data_json if request.method=="POST" else None))
    def lose_proposal(route):
        response=route.fetch();assert response.status==201,response.text()
        proposals.append((route.request.post_data_json,route.request.headers["idempotency-key"],response.json()));route.abort()
    page.route(proposal_url,lose_proposal);page.on("request",capture)
    panel.get_by_role("button",name="Record publication proposal",exact=True).click()
    expect(panel.get_by_role("button",name="Retry exact publication proposal",exact=True)).to_be_visible()
    assert len(proposals)==1 and len(requests)==1 and requests[0][0]=="POST",requests
    page.unroute(proposal_url,lose_proposal)
    with page.expect_request(lambda r:r.method=="POST" and r.url==proposal_url) as retry:
        panel.get_by_role("button",name="Retry exact publication proposal",exact=True).click()
    expect(panel.get_by_text("Publication proposal recorded.",exact=False)).to_be_visible()
    assert retry.value.post_data_json==proposals[0][0] and retry.value.headers["idempotency-key"]==proposals[0][1]
    assert len(requests)==2 and all(r[0]=="POST" for r in requests),requests
    page.remove_listener("request",capture);created=proposals[0][2]
    panel.get_by_role("button",name="Inspect complete implementation artifact",exact=True).click()
    checkbox=panel.get_by_role("checkbox",name="I inspected these complete patches, producer output and independent check results.",exact=True)
    expect(checkbox).to_be_enabled();checkbox.check()
    panel.get_by_role("button",name="Authorize inspected publication",exact=True).click()
    dialog=panel.get_by_role("dialog",name="Confirm publication decision")
    expect(dialog).to_contain_text(created["digest"])
    authorization_url=proposal_url+"/"+created["id"]+"/authorizations"
    authorizations=[]
    def lose_authorization(route):
        response=route.fetch();assert response.status==202,response.text()
        authorizations.append(response.json());route.abort()
    page.route(authorization_url,lose_authorization);requests.clear();page.on("request",capture)
    panel.get_by_role("button",name="Confirm publication authorization",exact=True).click()
    expect(panel.get_by_text("Renewed delivery and artifact inspection required before publication authorization.",exact=True)).to_be_visible()
    assert len(authorizations)==1 and requests==[("POST",urlparse(authorization_url).path,{"digest":created["digest"]})],requests
    expect(panel.get_by_role("region",name="Inspected implementation artifact")).to_have_count(0)
    page.remove_listener("request",capture);page.unroute(authorization_url,lose_authorization)
    panel.get_by_role("button",name="Refresh inspected delivery",exact=True).click()
    expect(panel.get_by_role("button",name="Request provider observation refresh",exact=True)).to_be_enabled()
    expect(panel.get_by_role("button",name="Authorize inspected publication",exact=True)).to_have_count(0)
    expect(inspected).to_contain_text("No provider checks retained; verification unknown.")
    expect(inspected).to_contain_text("Stale or uncertain provider observation")
    reconciliation_url=proposal_url+"/"+created["id"]+"/reconciliations"
    refreshes=[]
    def lose_refresh(route):
        response=route.fetch();assert response.status==202,response.text()
        refreshes.append((route.request.post_data_json,route.request.headers["idempotency-key"]));route.abort()
    page.route(reconciliation_url,lose_refresh);requests.clear();page.on("request",capture)
    panel.get_by_role("button",name="Request provider observation refresh",exact=True).click()
    expect(panel.get_by_role("button",name="Retry exact provider refresh",exact=True)).to_be_visible()
    page.unroute(reconciliation_url,lose_refresh)
    with page.expect_request(lambda r:r.method=="POST" and r.url==reconciliation_url) as retry:
        panel.get_by_role("button",name="Retry exact provider refresh",exact=True).click()
    expect(panel.get_by_text("Provider refresh requested.",exact=False)).to_be_visible()
    assert len(refreshes)==1 and retry.value.post_data_json==refreshes[0][0] and retry.value.headers["idempotency-key"]==refreshes[0][1]
    assert len(requests)==2 and all(r[0]=="POST" for r in requests),requests
    page.remove_listener("request",capture)
    panel.get_by_role("button",name="Inspect complete implementation artifact",exact=True).click()
    expect(panel.get_by_role("region",name="Inspected implementation artifact")).to_be_visible()
    page.screenshot(path="/tmp/conductor-delivery-workbench.png",full_page=True)
    page.set_viewport_size({"width":390,"height":844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"),"mobile delivery overflow"
    page.screenshot(path="/tmp/conductor-delivery-workbench-mobile.png",full_page=True)
    # A denied source read must clear prior private patches even if its error body
    # is empty. The same browser session may no longer display cached inspection.
    denied_url=proposal_url+"/"+created["id"]+"/artifact"
    page.route(denied_url,lambda route:route.fulfill(status=403,body=""))
    panel.get_by_role("button",name="Inspect complete implementation artifact",exact=True).click()
    expect(page.get_by_role("region",name="Inspected implementation artifact")).to_have_count(0)
    expect(page.get_by_role("article",name="Inspected delivery")).to_have_count(0)
    context.close()
    reader_context=browser.new_context(ignore_https_errors=True)
    reader=reader_context.new_page();sign_in(reader,"reader");shared=reader.locator(".repository-deliveries")
    shared.get_by_role("button",name="Refresh deliveries and access",exact=True).click()
    shared.get_by_label("Delivery ID",exact=True).fill(created["id"])
    shared.get_by_role("button",name="Inspect shared delivery",exact=True).click()
    expect(shared.get_by_role("article",name="Inspected delivery")).to_contain_text(created["digest"])
    expect(shared.get_by_role("button",name="Authorize inspected publication",exact=True)).to_have_count(0)
    expect(shared.get_by_role("button",name="Request provider observation refresh",exact=True)).to_have_count(0)
    reader.get_by_label("Managed repository",exact=True).select_option("")
    expect(reader.get_by_role("article",name="Inspected delivery")).to_have_count(0)
    reader_context.close();assert not errors,errors;browser.close()
    print("PASS: shared agent proposal, full artifact above ordinary response bound, tampered patch denial, escaped source, exact proposal retry, inspected digest authorization without GET, lost decision recovery, exact provider refresh retry, unknown provider evidence, read-only controls, denied source clearing and desktop/mobile screenshots")
