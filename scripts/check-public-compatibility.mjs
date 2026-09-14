import { readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

const annotation = key => ['description', 'summary', 'title', 'examples', 'example', 'deprecated', 'x-known-values', 'x-review-status', 'x-service-state'].includes(key);
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

// Conservative same-major checker: additions are permitted; changes whose
// compatibility cannot be proved here require a separately versioned contract.
export function breakingChanges(before, after) {
 const failures = [];
 const fail = path => failures.push(path);
 function walk(old, next, path) {
  if (old === null || typeof old !== 'object') { if (!same(old, next)) fail(path); return; }
  if (next === null || typeof next !== 'object') { fail(path); return; }
  if (Array.isArray(old)) { if (!same(old, next)) fail(path); return; }
  const containers = ['/paths','/schemas','/properties','/responses','/content','/headers'];
  const isContainer = containers.some(suffix => path.endsWith(suffix)) || /^\/paths\/[^/]+$/.test(path);
  for (const [key, value] of Object.entries(old)) {
   // A property named description/title is data, not schema annotation.
   if (!isContainer && annotation(key)) continue;
   if (!(key in next)) { fail(`${path}/${key}`); continue; }
   if (!isContainer && key === 'required' && Array.isArray(value)) {
    // Preserve old required fields and do not require any new input fields.
    // Response additions also use optional fields for older generated clients.
    if (!same([...value].sort((a,b)=>a.localeCompare(b)), [...next[key]].sort((a,b)=>a.localeCompare(b)))) fail(`${path}/${key}`);
   } else if (!isContainer && ['oneOf','anyOf'].includes(key)) {
    if (!Array.isArray(next[key]) || next[key].length < value.length) fail(`${path}/${key}`);
    else value.forEach((item, i) => walk(item, next[key][i], `${path}/${key}/${i}`));
   } else if (!isContainer && key === 'parameters') {
    for (const p of value) {
     const q = next[key]?.find(item => item.name === p.name && item.in === p.in);
     walk(p, q, `${path}/parameters/${p.in}:${p.name}`);
    }
    for (const p of next[key] ?? []) if (p.required && !value.some(q => q.name === p.name && q.in === p.in)) fail(`${path}/parameters/new-required:${p.name}`);
   } else walk(value, next[key], `${path}/${encodeURIComponent(key)}`);
  }
  // An added constraint on an existing schema can narrow it just as removal
  // can. Only container members and annotations can be added implicitly.
  if (!isContainer) for (const key of Object.keys(next)) if (!(key in old) && !annotation(key)) fail(`${path}/added:${key}`);
 }
 if (before.openapi !== after.openapi || before.info.version.split('.')[0] !== after.info.version.split('.')[0]) fail('/version');
 walk(before.paths, after.paths, '/paths');
 walk(before.components, after.components, '/components');
 walk(before.security, after.security, '/security');
 walk(before.servers, after.servers, '/servers');
 return failures;
}

export function checkPublicCompatibility() {
 const released = 'contracts/releases/v1.0.0.openapi.json';
 const baselineBytes = readFileSync(released);
 const baseline = JSON.parse(baselineBytes);
 const current = JSON.parse(readFileSync('contracts/v1.openapi.json'));
 const failures = breakingChanges(baseline, current);
 const base = process.env.PUBLIC_API_BASE_REF;
 if (base && !/^0+$/.test(base)) {
  if (!/^[0-9a-f]{40}$/.test(base)) throw new Error('PUBLIC_API_BASE_REF must be a full commit SHA');
  execFileSync('git', ['cat-file','-e',`${base}^{commit}`], { stdio: 'pipe', windowsHide: true });
  const previous = execFileSync('git', ['ls-tree','-r','--name-only',base,'--','contracts/releases/'], { encoding: 'utf8', windowsHide: true }).trim().split('\n').filter(Boolean);
  for (const file of previous) {
   const bytes = execFileSync('git', ['show', `${base}:${file}`], { maxBuffer: 16 << 20, windowsHide: true });
   if (!bytes.equals(readFileSync(file))) throw new Error(`Published baseline is immutable: ${file}`);
   if (file.endsWith('.openapi.json')) failures.push(...breakingChanges(JSON.parse(bytes), current));
  }
 }
 if (failures.length) throw new Error(`Breaking public API changes:\n${[...new Set(failures)].join('\n')}`);
 console.log('Public v1 compatibility and immutable baseline checks passed');
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) checkPublicCompatibility();
