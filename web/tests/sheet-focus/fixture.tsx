import { StrictMode, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { flushSync } from 'react-dom';
import { Sheet, SheetHeader } from '../../src/screens/Sheet';

type State = { open: boolean; nested: boolean; trigger: 'normal' | 'removed' | 'disabled' | 'hidden'; childFocus: boolean };
const initial: State = { open: false, nested: false, trigger: 'normal', childFocus: false };
let update: (patch: Partial<State>) => void;

// This effect deliberately focuses before the containing Sheet's passive
// effect, proving that opener capture does not accidentally remember a child.
function ChildFocus() {
  useEffect(() => { document.querySelector<HTMLInputElement>('#child-input')?.focus(); }, []);
  return <input id="child-input" aria-label="Demo field" />;
}

function Fixture() {
  const [state, setState] = useState(initial);
  update = (patch) => setState((previous) => ({ ...previous, ...patch }));
  return <>
    {state.trigger !== 'removed' && <button id="opener" hidden={state.trigger === 'hidden'} disabled={state.trigger === 'disabled'} onClick={() => update({ open: true })}>Open sheet</button>}
    <button id="outside">Other control</button>
    {state.open && <Sheet labelledBy="parent-title" onClose={() => update({ open: false, nested: false })}>
      <SheetHeader id="parent-title" title="Demo sheet"><button id="close" onClick={() => update({ open: false, nested: false })}>Close</button></SheetHeader>
      <button id="nested-opener" onClick={() => update({ nested: true })}>Open nested sheet</button>
      {state.childFocus && <ChildFocus />}
      {state.nested && <Sheet labelledBy="nested-title" onClose={() => update({ nested: false })}>
        <SheetHeader id="nested-title" title="Nested demo sheet"><button id="nested-close" onClick={() => update({ nested: false })}>Close nested sheet</button></SheetHeader>
      </Sheet>}
    </Sheet>}
  </>;
}

const root = createRoot(document.querySelector('#root')!);
flushSync(() => root.render(<StrictMode><Fixture /></StrictMode>));
const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
const element = (id: string) => document.getElementById(id)!;
const active = () => document.activeElement?.id || document.activeElement?.tagName;
function click(id: string) { const target = element(id); target.focus(); flushSync(() => target.click()); }
function change(patch: Partial<State>) { flushSync(() => update(patch)); }
const check = (condition: boolean, message: string) => { if (!condition) throw new Error(`${message}; active=${active()}`); };

async function runChecks() {
  const results: { name: string; passed: boolean; error?: string }[] = [];
  async function test(name: string, work: () => Promise<void>) {
    change(initial); await tick();
    try { await work(); results.push({ name, passed: true }); }
    catch (error) { results.push({ name, passed: false, error: String(error) }); }
  }
  await test('Close restores the opener', async () => {
    click('opener'); await tick(); check(document.activeElement?.getAttribute('role') === 'dialog', 'sheet did not gain focus');
    click('close'); await tick(); check(active() === 'opener', 'Close lost opener focus');
  });
  await test('Escape restores the opener', async () => {
    click('opener'); await tick();
    flushSync(() => document.activeElement?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })));
    await tick(); check(active() === 'opener', 'Escape lost opener focus');
  });
  await test('Unmount restores the opener', async () => {
    click('opener'); await tick(); change({ open: false }); await tick();
    check(active() === 'opener', 'unmount lost opener focus');
  });
  await test('Nested close returns to its own trigger', async () => {
    click('opener'); await tick(); click('nested-opener'); await tick(); click('nested-close'); await tick();
    check(active() === 'nested-opener', 'nested close skipped parent trigger');
    click('close'); await tick(); check(active() === 'opener', 'parent close lost original trigger');
  });
  await test('Unmounting both sheets restores the outer trigger', async () => {
    click('opener'); await tick(); click('nested-opener'); await tick(); change({ open: false, nested: false }); await tick();
    check(active() === 'opener', 'nested cleanup stole outer restoration');
  });
  await test('Deliberate focus elsewhere stays there', async () => {
    click('opener'); await tick(); element('outside').focus(); change({ open: false }); await tick();
    check(active() === 'outside', 'cleanup stole deliberate focus');
  });
  for (const trigger of ['removed', 'disabled', 'hidden'] as const) {
    await test(`An unavailable ${trigger} trigger is not focused`, async () => {
      click('opener'); await tick(); change({ trigger }); change({ open: false }); await tick();
      check(active() !== 'opener', 'cleanup focused an unavailable trigger');
      check(!document.activeElement?.closest('[role="dialog"]'), 'cleanup kept a removed dialog focused');
    });
  }
  await test('Child focus effects do not become the opener', async () => {
    change({ childFocus: true }); click('opener'); await tick(); click('close'); await tick();
    check(active() === 'opener', 'cleanup remembered a child instead of the trigger');
  });
  change(initial);
  document.querySelector('#results')!.textContent = JSON.stringify(results, null, 2);
  return results;
}

Object.assign(window, { runSheetFocusChecks: runChecks });
