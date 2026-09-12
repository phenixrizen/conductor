import type { Revision } from './api';

interface Difference { path: string; before: unknown; after: unknown; kind: string }

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

// Object key order is not a content change. Arrays retain their meaningful order.
function equal(left: unknown, right: unknown): boolean {
  if (left === right) return true;
  if (Array.isArray(left) && Array.isArray(right)) {
    return left.length === right.length && left.every((value, index) => equal(value, right[index]));
  }
  if (!isObject(left) || !isObject(right)) return false;
  const keys = Object.keys(left);
  return keys.length === Object.keys(right).length
    && keys.every(key => Object.hasOwn(right, key) && equal(left[key], right[key]));
}

function differences(before: unknown, after: unknown, path = '$', depth = 0): Difference[] {
  if (equal(before, after)) return [];
  if (isObject(before) && isObject(after) && depth < 20) {
    return [...new Set([...Object.keys(before), ...Object.keys(after)])].sort().flatMap(key =>
      differences(Object.hasOwn(before, key) ? before[key] : undefined,
        Object.hasOwn(after, key) ? after[key] : undefined, `${path}[${JSON.stringify(key)}]`, depth + 1));
  }
  return [{ path, before, after, kind: before === undefined ? 'Added' : after === undefined ? 'Removed' : 'Changed' }];
}

function valueLabel(value: unknown): string {
  return value === undefined ? '(absent)' : JSON.stringify(value, null, 2);
}

export function Comparison({ before, after }: { before: Revision; after: Revision }) {
  const changes = differences(before.content, after.content);
  return <section className="comparison" aria-labelledby="comparison-result-title">
    <h4 id="comparison-result-title">Revision {before.number} → inspected revision {after.number}</h4>
    <p className="muted">{changes.length} content {changes.length === 1 ? 'difference' : 'differences'}. Arrays are compared as ordered values.</p>
    <dl><dt>From digest</dt><dd><code>{before.digest}</code></dd><dt>To digest</dt><dd><code>{after.digest}</code></dd></dl>
    {changes.length === 0 && <p>No content changes between these revisions.</p>}
    {changes.length > 100 && <p className="warning">Showing the first 100 differences. Inspect each revision's full content for the remaining changes.</p>}
    {changes.slice(0, 100).map(change => <details key={change.path} className="difference" open={changes.length <= 5}>
      <summary><span className="tag">{change.kind}</span><code>{change.path}</code></summary>
      <div className="comparison-values">
        <div><p>Revision {before.number}</p><pre>{valueLabel(change.before)}</pre></div>
        <div><p>Inspected revision {after.number}</p><pre>{valueLabel(change.after)}</pre></div>
      </div>
    </details>)}
  </section>;
}
