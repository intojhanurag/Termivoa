// Build-time only. Keep the separate site lockfile visible to vulnerability CI.
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const reviewed = new Map([
  ['GHSA-ch52-4w7c-c8xp', { name: 'http-cache-semantics', version: '4.2.0', path: '.>astro>http-cache-semantics' }],
  ['GHSA-vfj7-8cjw-p6xm', { name: 'braces', version: '3.0.3', path: '.>starlight-llms-txt>micromatch>braces' }],
]);

// These are two reviewed, unpatched build-only findings, not package-wide
// exclusions. See docs/dependencies.md for reachability and upstream evidence.
const severities = ['info', 'low', 'moderate', 'high', 'critical'];
const plainObject = value => value !== null && typeof value === 'object'
  && !Array.isArray(value) && Object.getPrototypeOf(value) === Object.prototype;

export function assessSiteAudit(report) {
  if (!plainObject(report) || report.error || !plainObject(report.advisories)
    || !plainObject(report.metadata) || !plainObject(report.metadata.vulnerabilities)
    || Object.keys(report.metadata.vulnerabilities).some(key => !severities.includes(key))) {
    throw new Error('site audit returned an error or an unsupported report');
  }
  const findings = Object.values(report.advisories);
  const blocked = [], retained = [];
  for (const finding of findings) {
    if (!plainObject(finding) || !Array.isArray(finding.findings) || finding.findings.length === 0
      || finding.findings.some(entry => !plainObject(entry) || typeof entry.version !== 'string'
        || !Array.isArray(entry.paths) || entry.paths.length === 0 || entry.paths.some(path => typeof path !== 'string'))) {
      throw new Error('site audit returned a malformed advisory');
    }
    const decision = reviewed.get(finding.github_advisory_id);
    const exact = decision && finding.module_name === decision.name && finding.severity === 'high'
      && finding.patched_versions === '<0.0.0' && finding.findings?.length === 1
      && finding.findings[0].version === decision.version
      && finding.findings[0].paths?.length === 1 && finding.findings[0].paths[0] === decision.path;
    if (exact) retained.push(finding.github_advisory_id);
    else if (['moderate', 'high', 'critical'].includes(finding.severity)) blocked.push(finding.github_advisory_id ?? finding.module_name);
    else if (!['low', 'info'].includes(finding.severity)) throw new Error('site audit returned an unknown severity');
  }
  // Never accept an empty/malformed advisory list while summary counts report
  // findings. This also makes future pnpm report-format changes fail closed.
  const counts = report.metadata.vulnerabilities;
  for (const severity of severities) {
    const actual = findings.filter(finding => finding.severity === severity).length;
    if (!Number.isInteger(counts[severity]) || counts[severity] !== actual) throw new Error('site audit summary does not match its advisories');
  }
  return { blocked, retained };
}

export function runSiteAudit() {
  const result = spawnSync('pnpm', ['audit', '--ignore-workspace', '--json'], {
    cwd: fileURLToPath(new URL('../site/', import.meta.url)), encoding: 'utf8', timeout: 60_000, maxBuffer: 2 * 1024 * 1024,
  });
  if (result.error || result.signal || ![0, 1].includes(result.status)) throw new Error('site audit command failed');
  const report = JSON.parse(result.stdout);
  const assessment = assessSiteAudit(report);
  console.log(`Separate site audit: ${JSON.stringify(report.metadata.vulnerabilities)}`);
  for (const id of assessment.retained) console.log(`RETAINED unpatched build-only finding: ${id} (docs/dependencies.md)`);
  if (assessment.blocked.length) throw new Error(`Unreviewed or patchable site findings: ${assessment.blocked.join(', ')}`);
  console.log('Site audit gate passed; retained findings remain visible above. This is not a zero-advisory claim.');
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try { runSiteAudit(); } catch (error) { console.error(error.message); process.exitCode = 1; }
}
