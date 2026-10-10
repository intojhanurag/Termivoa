import { StrictMode, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { flushSync } from 'react-dom';
import { Sessions } from '../../src/screens/Sessions';
import { ItemMenuSheet } from '../../src/screens/ProviderManage';
import { ApiError } from '../../src/api/http';
import { group, item, mac, okEntry, ready, target, vps } from '../../src/screens/providerFixtures';
import type { ProviderEntry } from '../../src/screens/providers';
import type { Computer } from '../../src/computers';

const root = createRoot(document.querySelector('#root')!);
const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
const tabs = () => [target('w1:p1.demo', 'working', { agent: 'claude' })];
const main = () => item('w1', 'main', tabs());
const project = () => group('g-demo', 'shop', [main()]);
const button = (text: string) => [...document.querySelectorAll<HTMLButtonElement>('button')].find((one) => (() => { const copy = one.cloneNode(true) as HTMLElement; copy.querySelectorAll('[aria-hidden="true"]').forEach((hidden) => hidden.remove()); return copy.textContent?.trim() === text; })())!;
const click = (element: HTMLElement) => flushSync(() => element.click());
const check = (ok: boolean, why: string) => { if (!ok) throw new Error(why); };
function input(value: string) {
  const field = document.querySelector<HTMLInputElement>('#create-name')!;
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(field, value);
  flushSync(() => field.dispatchEvent(new Event('input', { bubbles: true })));
}

let reads: string[] = [];
let pending: { resolve: (kinds: string[]) => void; reject: () => void }[] = [];
let sent: unknown[] = [];
function Harness({ computer }: { computer: Computer }) {
  const [entry, setEntry] = useState(() => ready(computer, [project()]));
  return <Sessions entries={[okEntry(computer)]} sessions={[]} providers={[entry]} tab="herdr" offerPush={false}
    onOpen={() => undefined} onNew={() => undefined} onWall={() => undefined} onSettings={() => undefined}
    onReload={async () => undefined} onRetry={async () => undefined} onProviderRetry={async () => undefined}
    onCreated={() => undefined} onFailure={() => 'Fixed demo refusal'}
    onReadSettings={(origin) => {
      reads.push(origin);
      return new Promise<void>((resolve, reject) => pending.push({
        resolve: (kinds) => { flushSync(() => setEntry((old) => ({ ...old, info: { ...old.info, agentKinds: kinds } }))); resolve(); },
        reject: () => reject(new Error('Demo settings read failed')),
      }));
    }} />;
}
async function reset(computer = mac) {
  flushSync(() => root.render(null)); await tick();
  reads = []; pending = []; sent = [];
  // Synthetic requests only: no broker, real provider or credentials.
  window.fetch = async (_url, init) => { sent.push(JSON.parse(String(init?.body))); return Response.json({ itemId: 'w1', targetId: 'w1:p2.demo', agentStarted: true }); };
  flushSync(() => root.render(<StrictMode><Harness computer={computer} /></StrictMode>)); await tick();
}
async function settle(kinds: string[]) { pending.shift()!.resolve(kinds); await tick(); await tick(); }
async function menu() { click(document.querySelector<HTMLElement>('[aria-label="Menu for main"]')!); await tick(); }
async function chooser() { click(button('New tab')); await tick(); }

async function runChecks() {
  const results: { name: string; passed: boolean; error?: string }[] = [];
  async function test(name: string, work: () => Promise<void>) {
    try { await work(); results.push({ name, passed: true }); }
    catch (error) { results.push({ name, passed: false, error: String(error) }); }
  }
  for (const computer of [mac, vps]) {
    await test(`Sessions New worktree reads ${computer.label}, waits and uses latest kinds`, async () => {
      await reset(computer);
      click(document.querySelector<HTMLElement>('[aria-label="New worktree in shop"]')!); await tick();
      check(reads.join() === computer.origin, 'New worktree did not ask the tapped computer for settings');
      input('fix/demo');
      check(button('Create and start').disabled, 'Create and start accepts cached agent while settings are pending');
      click(button('Create and start'));
      flushSync(() => document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
      check(sent.length === 0, 'Pending read started an agent');
      await settle(['codex']);
      check(!button('No agent').disabled && button('No agent').getAttribute('aria-checked') === 'true', 'Removed choice did not become No agent');
      check(document.body.textContent!.includes('claude is not running in herdr now.'), 'Dropped choice guidance missing');
      check(![...document.querySelectorAll('button')].some((one) => one.textContent === 'claude'), 'Sheet retained stale entry');
      click(button('codex')); check(!button('Create and start').disabled, 'Fresh available agent remains blocked');
      click(button('Create and start')); await tick();
      check(JSON.stringify(sent).includes('"agent":"codex"') && sent.length === 1, 'Fresh worktree choice did not submit once');
    });
    await test(`Sessions menu and chooser read ${computer.label}, cancel does not read`, async () => {
      await reset(computer); await menu();
      check(reads.join() === computer.origin, 'Menu settings read missing or wrong origin'); await settle(['claude', 'codex']);
      await chooser(); click(button('claude'));
      check(reads.length === 2 && reads[1] === computer.origin, 'Chooser settings read missing or wrong origin');
      check(button('Start').disabled, 'Start accepts cached kind before quiet read finishes');
      click(button('Start')); check(sent.length === 0, 'Pending chooser sent agent request');
      await settle(['codex']);
      check(!document.querySelector('button.primary'), 'Removed chooser selection silently became another agent');
      check(document.body.textContent!.includes('claude is not running in herdr now.'), 'Chooser dropped guidance missing');
      click(button('codex')); check(!button('Start').disabled, 'Fresh chooser Start stays blocked');
      click(button('Cancel')); await tick(); check(reads.length === 2, 'Closing chooser re-read settings');
      await chooser(); await settle(['codex']); click(button('Start')); await tick();
      check(sent.length === 1 && JSON.stringify(sent).includes('"agent":"codex"'), 'Fresh new-tab choice did not submit once');
    });
  }
  await test('No agent and Shell stay available during settings refresh', async () => {
    await reset(); click(document.querySelector<HTMLElement>('[aria-label="New worktree in shop"]')!); await tick();
    input('fix/demo'); click(button('No agent')); check(!button('Create').disabled, 'No agent unnecessarily waits for agent kinds');
    click(button('Create')); await tick(); check(sent.length === 1 && !JSON.stringify(sent).includes('"agent"'), 'No-agent request gained an agent');
    await reset(); await menu(); await settle(['claude']); await chooser();
    check(!button('Shell').disabled, 'Shell unnecessarily waits for kinds');
    await new Promise((resolve) => setTimeout(resolve, 450)); click(button('Shell')); await tick();
    check(sent.length === 1 && !JSON.stringify(sent).includes('"agent"'), 'Shell request gained an agent');
  });
  await test('Failed quiet read settles without an unhandled rejection', async () => {
    await reset(); await menu(); await settle(['claude']); await chooser(); click(button('claude'));
    let unhandled = false; const listener = (event: PromiseRejectionEvent) => { unhandled = true; event.preventDefault(); };
    window.addEventListener('unhandledrejection', listener);
    pending.shift()!.reject(); await tick(); await tick();
    window.removeEventListener('unhandledrejection', listener);
    check(!unhandled && !button('Start').disabled, 'Failed quiet read did not settle safely');
  });
  await test('Shared main-folder last tab names an available action', async () => {
    await reset(); const current = main(); const shared = group('g-demo', 'shop', [current, item('w2', 'other', tabs())]);
    const entry: ProviderEntry = ready(mac, [shared]);
    flushSync(() => root.render(<ItemMenuSheet entry={entry} group={shared} item={current} onOpened={() => undefined} onReload={async () => undefined} onFailure={() => 'fixed'} onClose={() => undefined} />)); await tick();
    check(button('Close workspace').disabled, 'Shared close unexpectedly permitted');
    check(!document.body.textContent!.includes('Close the workspace to end its last tab.'), 'Last tab points to disabled Close workspace');
    check(document.body.textContent!.includes('Close them in herdr on mac.'), 'Available computer action missing');
  });
  await test('Stale close conflict explains computer close without exposing error details', async () => {
    await reset(); const current = main(); const entry = ready(mac, [project()]);
    flushSync(() => root.render(<ItemMenuSheet entry={entry} group={project()} item={current} onOpened={() => undefined} onReload={async () => undefined} onFailure={() => 'fixed'} onClose={() => undefined}
      manage={async () => { throw new ApiError(409, 'operation_conflict', 'UNTRUSTED_DEMO_DETAIL'); }} />)); await tick();
    click(button('Close workspace')); await tick(); await new Promise((resolve) => setTimeout(resolve, 450)); click(button('Close workspace')); await tick(); await tick();
    check(document.body.textContent!.includes('main folder'), 'Stale close conflict hid shared-folder guidance');
    check(!document.body.textContent!.includes('UNTRUSTED_DEMO_DETAIL'), 'Conflict exposed computer error detail');
  });
  flushSync(() => root.render(null)); return results;
}
declare global { interface Window { runProviderKindChecks: typeof runChecks } }
window.runProviderKindChecks = runChecks;
