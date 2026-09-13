import { useEffect, useRef, useState } from 'react';
import { APIError, errorMessage, request } from './api';
import type { AuthConfig, BrowserAccess, BrowserSession, ManagedRepository, RepositoryPage } from './api';
import { ReviewWorkbench, WorkbenchHeader } from './ReviewWorkbench';
import { captureTrackerLinkHint } from './trackerLinkHint';

type Authentication =
  | { kind: 'loading' }
  | { kind: 'local' }
  | { kind: 'ready'; config: AuthConfig; session: BrowserSession }
  | { kind: 'signedOut'; config: AuthConfig; message?: string }
  | { kind: 'blocked'; config: AuthConfig; message: string }
  | { kind: 'loggingOut'; config: AuthConfig }
  | { kind: 'unavailable'; config?: AuthConfig; message: string };

export function App() {
  useEffect(captureTrackerLinkHint, []);
  const [authentication, setAuthentication] = useState<Authentication>({ kind: 'loading' });
  const [reload, setReload] = useState(0);
  const [workspaceID, setWorkspaceID] = useState('');
  const [repositoryID, setRepositoryID] = useState('');
  const [repositories, setRepositories] = useState<RepositoryPage>();
  const [repositoryPending, setRepositoryPending] = useState(false);
  const [repositoryError, setRepositoryError] = useState('');
  const authSequence = useRef(0);
  const scopeSequence = useRef(0);
  const authController = useRef<AbortController | undefined>(undefined);
  const scopeController = useRef<AbortController | undefined>(undefined);

  function clearScope() {
    scopeSequence.current++;
    scopeController.current?.abort();
    setWorkspaceID('');
    setRepositoryID('');
    setRepositories(undefined);
    setRepositoryPending(false);
    setRepositoryError('');
  }

  useEffect(() => {
    const generation = ++authSequence.current;
    const controller = new AbortController();
    authController.current = controller;
    clearScope();
    setAuthentication({ kind: 'loading' });
    const current = () => !controller.signal.aborted && authSequence.current === generation;
    void (async () => {
      let config: AuthConfig | undefined;
      try {
        config = await request<AuthConfig>('/auth/config', undefined, controller.signal);
        if (!current()) return;
        if (!config || (config.mode !== 'local' && config.mode !== 'oidc') || typeof config.browserLogin !== 'boolean') {
          throw new Error('The server returned an invalid authentication configuration.');
        }
        if (config.mode === 'local') {
          setAuthentication({ kind: 'local' });
          return;
        }
        if (!config.browserLogin) {
          setAuthentication({ kind: 'unavailable', config, message: 'Browser sign-in is not configured on this server. Ask a workspace operator to enable it.' });
          return;
        }
        const session = await request<BrowserSession>('/auth/session', undefined, controller.signal);
        validateSession(session);
        if (current()) setAuthentication({ kind: 'ready', config, session });
      } catch (failure) {
        if (!current()) return;
        if (config?.mode === 'oidc' && failure instanceof APIError && failure.status === 401) {
          setAuthentication({ kind: 'signedOut', config });
        } else setAuthentication({ kind: 'unavailable', config, message: errorMessage(failure) });
      }
    })();
    return () => { controller.abort(); scopeSequence.current++; scopeController.current?.abort(); };
  }, [reload]);

  // A denied command never triggers a refresh inside its action. Hide all review
  // state first; the user explicitly reloads the session or signs in again.
  function denied(failure: APIError) {
    authSequence.current++;
    authController.current?.abort();
    clearScope();
    setAuthentication(previous => {
      if (previous.kind !== 'ready') return previous;
      return failure.status === 401
        ? { kind: 'signedOut', config: previous.config, message: 'Your session has ended. Sign in again, then inspect the package before continuing.' }
        : { kind: 'blocked', config: previous.config, message: 'Your access could not be confirmed. Reload your session and choose an available workspace and repository before inspecting again.' };
    });
  }

  async function chooseWorkspace(next: string) {
    if (authentication.kind !== 'ready') return;
    clearScope();
    if (!next) return;
    // Only choices returned for this verified session may establish scope.
    if (!authentication.session.session.workspaces.some(workspace => workspace.id === next)) return;
    setWorkspaceID(next);
    setRepositoryPending(true);
    const generation = ++scopeSequence.current;
    const controller = new AbortController();
    scopeController.current = controller;
    try {
      const page = await request<RepositoryPage>('/repositories', accessFor(authentication.session, next), controller.signal);
      if (controller.signal.aborted || generation !== scopeSequence.current) return;
      validateRepositories(page, next);
      setRepositories(page);
    } catch (failure) {
      if (controller.signal.aborted || generation !== scopeSequence.current) return;
      if (failure instanceof APIError && (failure.status === 401 || failure.status === 403)) denied(failure);
      else setRepositoryError(errorMessage(failure));
    } finally {
      if (!controller.signal.aborted && generation === scopeSequence.current) setRepositoryPending(false);
    }
  }

  async function signOut() {
    if (authentication.kind !== 'ready') return;
    const { config, session } = authentication;
    const generation = ++authSequence.current;
    authController.current?.abort();
    const controller = new AbortController();
    authController.current = controller;
    clearScope();
    setAuthentication({ kind: 'loggingOut', config });
    try {
      const result = await request<{ signedOut: boolean }>('/auth/logout', accessFor(session), controller.signal, {});
      if (!result?.signedOut) throw new Error('The server did not confirm sign-out.');
      if (!controller.signal.aborted && generation === authSequence.current) setAuthentication({ kind: 'signedOut', config, message: 'You have signed out of Conductor.' });
    } catch (failure) {
      if (controller.signal.aborted || generation !== authSequence.current) return;
      if (failure instanceof APIError && failure.status === 401) setAuthentication({ kind: 'signedOut', config });
      else setAuthentication({ kind: 'unavailable', config, message: 'Sign-out could not be confirmed. Reload your session to check its status. ' + errorMessage(failure) });
    }
  }

  if (authentication.kind === 'local') return <ReviewWorkbench />;

  if (authentication.kind !== 'ready') return <main>
    <WorkbenchHeader />
    <section className="authentication panel" aria-labelledby="authentication-title" aria-busy={authentication.kind === 'loading' || authentication.kind === 'loggingOut'}>
      <p className="eyebrow">SHARED WORKSPACE ACCESS</p>
      <h2 id="authentication-title">{authentication.kind === 'signedOut' ? 'Sign in to shared review' : authentication.kind === 'blocked' ? 'Check your access' : authentication.kind === 'unavailable' ? 'Shared review is unavailable' : 'Connecting to your workspace'}</h2>
      {(authentication.kind === 'loading' || authentication.kind === 'loggingOut') && <p role="status">{authentication.kind === 'loggingOut' ? 'Signing out and clearing this inspection…' : 'Checking the server and your session…'}</p>}
      {authentication.kind === 'signedOut' && <>
        <p role="status">{authentication.message || 'Use your organization’s sign-in to find shared packages and review their exact content.'}</p>
        <a className="button-link" href="/api/v1/auth/login">Sign in</a>
        <button className="secondary" onClick={() => setReload(value => value + 1)}>Check session</button>
      </>}
      {(authentication.kind === 'blocked' || authentication.kind === 'unavailable') && <>
        <p role="alert" className="error">{authentication.message}</p>
        <button className="secondary" onClick={() => setReload(value => value + 1)}>Reload session</button>
      </>}
    </section>
  </main>;

  const session = authentication.session;
  const principal = session.session.principal;
  const selectedRepository = repositories?.repositories.find(repository => repository.id === repositoryID);
  const controls = <section className="access-panel panel" aria-labelledby="access-title">
    <div className="section-heading">
      <div><p className="eyebrow">SIGNED IN / SHARED RECORDS</p><h2 id="access-title">Your workspace</h2></div>
      <div className="session-actions"><button className="secondary" onClick={() => setReload(value => value + 1)}>Reload access</button><button className="secondary" onClick={() => void signOut()}>Sign out</button></div>
    </div>
    <p className="signed-in-identity">Signed in as <strong>{principal.id}</strong> <span className="tag">{principal.kind === 'human' ? 'Human' : 'Agent'}</span></p>
    <div className="control-row">
      <label htmlFor="workspace-selection">Workspace<select id="workspace-selection" aria-label="Workspace" value={workspaceID} onChange={event => void chooseWorkspace(event.target.value)}>
        <option value="">Choose a workspace</option>
        {session.session.workspaces.map(workspace => <option key={workspace.id} value={workspace.id}>{workspace.name} ({workspace.id})</option>)}
      </select></label>
      <label htmlFor="repository-selection">Managed repository<select id="repository-selection" aria-label="Managed repository" value={repositoryID} disabled={!repositories || repositoryPending} onChange={event => setRepositoryID(event.target.value)}>
        <option value="">{repositoryPending ? 'Loading repositories…' : 'Choose a repository'}</option>
        {repositories?.repositories.map(repository => <option key={repository.id} value={repository.id}>{repository.name} · {repository.provider} · {repository.host} ({repository.id})</option>)}
      </select></label>
    </div>
    {session.session.workspaces.length === 0 && <p className="empty-list">No active workspace memberships are available. Ask a workspace operator for access, then reload access.</p>}
    {session.session.truncated && <p className="warning">The workspace list is truncated. Only the first 100 available workspaces are shown; ask a workspace operator about a missing workspace.</p>}
    {repositoryPending && <p role="status">Loading repositories for the selected workspace…</p>}
    {repositoryError && <div className="error" role="alert"><p>{repositoryError}</p><button className="secondary" onClick={() => void chooseWorkspace(workspaceID)}>Retry repositories</button></div>}
    {repositories?.repositories.length === 0 && <p className="empty-list">No readable repositories are available in this workspace. Ask a workspace operator for access.</p>}
    {repositories?.truncated && <p className="warning">The repository list is truncated. Only the first 100 readable repositories are shown; ask a workspace operator about a missing repository.</p>}
    {selectedRepository && <details className="repository-access"><summary>Repository permissions and identity</summary>
      <p>Selected repository: <strong>{selectedRepository.name}</strong> · {selectedRepository.provider} · {selectedRepository.host}</p>
      <p className="muted">Canonical ID: <code>{selectedRepository.id}</code> · Provider ID: <code>{selectedRepository.providerId}</code></p>
      <ul className="coverage" aria-label="Repository permissions"><li>Read: allowed</li><li>Author: {selectedRepository.canAuthor ? 'allowed' : 'not granted'}</li><li>Approve: {selectedRepository.canApprove && principal.kind === 'human' ? 'allowed after independent inspection' : 'not granted'}</li></ul>
    </details>}
  </section>;

  if (!selectedRepository) return <main><WorkbenchHeader />{controls}<section className="empty"><h2>Choose where to review.</h2><p>Select a workspace and managed repository to discover its shared packages. Changing either selection clears the previous inspection.</p></section></main>;

  const access = accessFor(session, workspaceID, selectedRepository);
  // Remounting removes every package, historical record, comparison, discovery
  // page and pending request when identity, session, or canonical scope changes.
  return <ReviewWorkbench key={JSON.stringify([principal.id, session.csrfToken, workspaceID, repositoryID])}
    access={access} sessionControls={controls} onAccessFailure={denied} />;
}

function accessFor(value: BrowserSession, workspaceID = '', repository?: ManagedRepository): BrowserAccess {
  return { mode: 'browser', principalID: value.session.principal.id, principalKind: value.session.principal.kind,
    csrfToken: value.csrfToken, workspaceID, repositoryID: repository?.id ?? '',
    canAuthor: repository?.canAuthor === true, canApprove: repository?.canApprove === true };
}

function identifier(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= 128 && value.trim() === value && !/[\u0000-\u001f\u007f-\u009f]/.test(value);
}

function validateSession(value: BrowserSession) {
  const principal = value?.session?.principal;
  const workspaces = value?.session?.workspaces;
  if (!principal || !identifier(principal.id) || !['human', 'agent'].includes(principal.kind)
    || typeof value.csrfToken !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(value.csrfToken)
    || !Array.isArray(workspaces) || workspaces.length > 100 || typeof value.session.truncated !== 'boolean'
    || workspaces.some(workspace => !workspace || !identifier(workspace.id) || typeof workspace.name !== 'string' || workspace.name.length < 1 || workspace.name.length > 256)
    || new Set(workspaces.map(workspace => workspace.id)).size !== workspaces.length) {
    throw new Error('The server returned an invalid session. Reload your session before reviewing work.');
  }
}

function validateRepositories(value: RepositoryPage, workspaceID: string) {
  if (!value || !Array.isArray(value.repositories) || value.repositories.length > 100 || typeof value.truncated !== 'boolean'
    || value.repositories.some(repository => !repository || !identifier(repository.id) || repository.workspaceId !== workspaceID
      || !['github', 'gitlab'].includes(repository.provider) || typeof repository.host !== 'string' || !repository.host || repository.host.length > 253
      || typeof repository.providerId !== 'string' || !repository.providerId || repository.providerId.length > 256
      || typeof repository.name !== 'string' || !repository.name || repository.name.length > 512
      || repository.canRead !== true || typeof repository.canAuthor !== 'boolean' || typeof repository.canApprove !== 'boolean')
    || new Set(value.repositories.map(repository => repository.id)).size !== value.repositories.length) {
    throw new Error('The server returned an invalid repository selection. Reload access before reviewing work.');
  }
}
