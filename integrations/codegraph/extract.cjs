'use strict';
// This bridge is pinned to CodeGraph 1.6.0's published library and schema. It
// runs only inside the credential-free, network-disabled indexing container.
const fs = require('node:fs');
const path = require('node:path');
const { DatabaseSync } = require('node:sqlite');
const { CodeGraph } = require('/opt/codegraph/lib/dist/index.js');
const { getKernel } = require('/opt/codegraph/lib/dist/extraction/kernel/loader.js');
const { tryKernelExtract } = require('/opt/codegraph/lib/dist/extraction/kernel/index.js');
const { detectLanguage } = require('/opt/codegraph/lib/dist/extraction/index.js');
const MAX_NODES = 4096, MAX_EDGES = 8192;
async function main() {
  if (!/^NoNewPrivs:\s+1$/m.test(fs.readFileSync('/proc/self/status', 'utf8'))) throw Error('no_new_privs required');
  let bytes = 0; const chunks = [];
  for await (const chunk of process.stdin) { bytes += chunk.length; if (bytes > 8 * 1024 * 1024) throw Error('input bound'); chunks.push(chunk); }
  const input = JSON.parse(Buffer.concat(chunks));
  const project = '/work/project'; fs.mkdirSync(project, { recursive: true });
  const files = [], allowed = new Map(); let nativeFiles = 0;
  const kernel = getKernel(); if (!kernel) throw Error('native kernel unavailable');
  for (const file of input.files) {
    if (typeof file.path !== 'string' || typeof file.text !== 'string' || path.posix.normalize(file.path) !== file.path || path.isAbsolute(file.path) || file.path.startsWith('../') || file.path.split('/').some(p => p === '.git' || p === '.codegraph') || file.path.includes('\\')) throw Error('invalid path');
    const target = path.join(project, file.path); fs.mkdirSync(path.dirname(target), { recursive: true }); fs.writeFileSync(target, file.text, { flag: 'wx', mode: 0o600 });
    allowed.set(file.path, file.text.split('\n').length);
    const language = detectLanguage(file.path);
    const native = language ? tryKernelExtract(file.path, file.text, language) : null;
    if (native) nativeFiles++;
    files.push({ path: file.path, state: native ? 'native' : 'unsupported_or_deferred' });
  }
  // Library initialization does not run the CLI installer or register an agent,
  // start a watcher, execute package scripts, or give source instructions authority.
  const graph = await CodeGraph.init(project);
  await graph.indexAll(); graph.close();
  const db = new DatabaseSync(path.join(project, '.codegraph/codegraph.db'), { readOnly: true });
  const rawNodes = db.prepare('SELECT id, kind, name, file_path, start_line FROM nodes ORDER BY id LIMIT ?').all(MAX_NODES + 1);
  const rawEdges = db.prepare('SELECT source,target,kind,provenance FROM edges ORDER BY source,target,kind,id LIMIT ?').all(MAX_EDGES + 1);
  const nodes = [], ids = new Set(); let truncated = rawNodes.length > MAX_NODES || rawEdges.length > MAX_EDGES;
  for (const n of rawNodes.slice(0, MAX_NODES)) {
    const rel = path.isAbsolute(n.file_path) ? path.relative(project, n.file_path) : n.file_path;
    if (!allowed.has(rel) || typeof n.name !== 'string' || n.name.length > 1024 || n.start_line < 0 || n.start_line > allowed.get(rel)) { truncated = true; continue; }
    nodes.push({ id: n.id, kind: n.kind, name: n.name, path: rel, line: n.start_line }); ids.add(n.id);
  }
  const edges = rawEdges.slice(0, MAX_EDGES).filter(e => ids.has(e.source) && ids.has(e.target)).map(e => ({ from: e.source, to: e.target, kind: e.kind, provenance: e.provenance || 'unspecified' }));
  const unresolved = db.prepare('SELECT count(*) AS count FROM unresolved_refs').get().count;
  db.close();
  process.stdout.write(JSON.stringify({ indexer: 'colbymchenry/codegraph/1.6.0', kernelVersion: kernel.contractInfo().kernelVersion, nativeFiles, files, nodes, edges, unresolved, truncated }));
}
// Source-bearing diagnostics never cross the activity boundary or enter history.
main().catch(() => { process.stderr.write('CodeGraph indexing unavailable\n'); process.exitCode = 1; });
