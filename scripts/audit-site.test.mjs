import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assessSiteAudit } from './audit-site.mjs';
const known = { github_advisory_id: 'GHSA-ch52-4w7c-c8xp', module_name: 'http-cache-semantics', severity: 'high', patched_versions: '<0.0.0', findings: [{ version: '4.2.0', paths: ['.>astro>http-cache-semantics'] }] };
function report(findings = []) {
  return { advisories: Object.fromEntries(findings.map((finding, i) => [i, finding])), metadata: { vulnerabilities: Object.fromEntries(['info', 'low', 'moderate', 'high', 'critical'].map(severity => [severity, findings.filter(finding => finding.severity === severity).length])) } };
}
test('retains only the exact reviewed unpatched site finding', () => {
  assert.deepEqual(assessSiteAudit(report([known])), { blocked: [], retained: [known.github_advisory_id] });
  assert.deepEqual(assessSiteAudit(report()), { blocked: [], retained: [] });
});
test('a patch, new package version, severity or dependency path requires fresh action', () => {
  for (const finding of [
    { ...known, patched_versions: '>=4.2.1' },
    { ...known, findings: [{ version: '4.2.1', paths: known.findings[0].paths }] },
    { ...known, findings: [{ version: '4.2.0', paths: ['.>new-server>http-cache-semantics'] }] },
    { ...known, severity: 'critical' },
    { ...known, module_name: 'other' },
    { ...known, findings: [...known.findings, known.findings[0]] },
  ]) assert.deepEqual(assessSiteAudit(report([finding])).blocked, [known.github_advisory_id]);
});
test('new moderate, high or critical findings fail the gate', () => {
  for (const severity of ['moderate', 'high', 'critical']) {
    assert.deepEqual(assessSiteAudit(report([{ ...known, github_advisory_id: 'GHSA-new', severity }])).blocked, ['GHSA-new']);
  }
});
test('audit errors, report drift and inconsistent summary fail closed', () => {
  for (const malformed of [null, {}, { ...report(), error: 'registry unavailable' }, { ...report(), metadata: { vulnerabilities: { high: 1 } } }, report([{ ...known, severity: 'unknown' }])]) {
    assert.throws(() => assessSiteAudit(malformed));
  }
});

test('unsupported containers and new summary severity fail closed', () => {
  assert.throws(() => assessSiteAudit({ ...report(), advisories: [] }));
  assert.throws(() => assessSiteAudit({ ...report(), metadata: { vulnerabilities: { ...report().metadata.vulnerabilities, severe: 1 } } }));
  assert.throws(() => assessSiteAudit({ ...report(), advisories: { bad: null } }));
});

test('array-like nested containers cannot hide new paths', () => {
  assert.throws(() => assessSiteAudit(report([{ ...known, findings: { 0: known.findings[0], length: 1 } }])));
  assert.throws(() => assessSiteAudit(report([{ ...known, findings: [{ version: '4.2.0', paths: { 0: known.findings[0].paths[0], 1: '.>new-server>http-cache-semantics', length: 1 } }] }])));
});
