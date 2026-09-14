import { visibleControls } from './sourceText';
import type { Content } from './api';

export const packageFields = [
  ['title', 'Change title', 'A short name for this change'],
  ['intent', 'Intended outcome', 'What should change, and why?'],
  ['scope', 'Scope', 'What is included? What is outside this change?'],
  ['design', 'Design', 'Describe the approach, constraints and decisions to review.'],
  ['tasks', 'Planned work', 'Describe the implementation steps and dependencies.'],
  ['verification', 'Verification plan', 'How will you check that the intended outcome was achieved?'],
] as const;

export function PackageContent({ content, advanced = true }: { content: Content; advanced?: boolean }) {
  return <div className="package-content">
    {packageFields.map(([key, label]) => Object.hasOwn(content, key) && <section key={key}>
      <h4>{label}</h4>
      {typeof content[key] === 'string' ? <p className="package-prose">{visibleControls(content[key] || '(Empty text)')}</p>
        : <><p className="muted">Structured content retained as recorded.</p><pre>{visibleControls(JSON.stringify(content[key], null, 2))}</pre></>}
    </section>)}
    {advanced && <details className="complete-package"><summary>Advanced: complete change JSON</summary>
      <p className="muted">All saved fields, including source context and extensions.</p><pre>{visibleControls(JSON.stringify(content, null, 2))}</pre>
    </details>}
  </div>;
}

export function PackageFields({ content, original, disabled, onChange }: { content: Content; original: Content; disabled: boolean; onChange: (content: Content) => void }) {
  function changeField(key: string, value: string) {
    const next = { ...content };
    // Returning an originally absent field to empty is a net no-op. Existing
    // empty strings remain explicit content, including after recovery reuse.
    if (value === '' && !Object.hasOwn(original, key)) delete next[key];
    else next[key] = value;
    onChange(next);
  }
  return <div className="package-fields">{packageFields.map(([key, label, hint]) => {
    const value = content[key];
    const editable = !Object.hasOwn(content, key) || typeof value === 'string';
    return <section key={key}>{editable ? <label htmlFor={`change-${key}`}>{label}
      {key === 'title' ? <input id={`change-${key}`} aria-label={label} value={typeof value === 'string' ? value : ''} disabled={disabled} maxLength={4096} placeholder={hint}
        onChange={event => changeField(key, event.target.value)} />
        : <textarea id={`change-${key}`} aria-label={label} value={typeof value === 'string' ? value : ''} disabled={disabled} rows={key === 'intent' ? 3 : 4} maxLength={131072} placeholder={hint}
          onChange={event => changeField(key, event.target.value)} />}
    </label> : <><h4>{label}</h4><p className="muted">This field contains structured content. It stays unchanged when you save; use the CLI or API to edit its structure.</p><pre>{visibleControls(JSON.stringify(value, null, 2))}</pre></>}</section>;
  })}</div>;
}
