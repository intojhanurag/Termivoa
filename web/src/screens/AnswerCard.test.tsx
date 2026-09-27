import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { AgentQuestion } from '../generated/protocol';
import { INITIAL_STATUS, type AnswerState, type ConnectionStatus } from '../terminal/Connection';
import { AnswerCard, AnswerPill, openQuestion, splitShortcut } from './AnswerCard';

const noop = () => undefined;
const id = 'ab'.repeat(16);
const question: AgentQuestion = {
  id,
  text: 'Do you want to proceed?',
  detail: 'Bash command\n  touch <x>.txt',
  options: [{ label: 'Yes' }, { label: "Yes, and don't ask again" }, { label: 'No, and tell Claude what to do differently (esc)' }],
};
const agent = { state: 'waiting' as const, stateSince: new Date(Date.now() - 12_000).toISOString(), question };
const controller: ConnectionStatus = { ...INITIAL_STATUS, link: 'live', control: 'controller', sessionState: 'running', agent };

function card(status: ConnectionStatus, shown: AgentQuestion = question, canTake = false) {
  return renderToStaticMarkup(
    <AnswerCard title="claude" question={shown} stateSince={agent.stateSince} status={status} canTake={canTake}
      onAnswer={noop} onTakeControl={noop} onHide={noop} />,
  );
}

const options = (html: string) => [...html.matchAll(/<button type="button" class="(answer-option[^"]*)" aria-label="([^"]+)"([^>]*)>/gu)];

describe('answer card', () => {
  it('shows the asker, the question, the detail as text, and one button per option for the controller', () => {
    const html = card(controller);
    expect(html).toContain('aria-labelledby="answer-question"');
    expect(html).toMatch(/CLAUDE ASKS · 1[23]s/u);
    expect(html).toContain('<h2 id="answer-question" class="answer-question">Do you want to proceed?</h2>');
    // Terminal text is escaped, never markup.
    expect(html).toContain('<pre class="answer-detail">Bash command\n  touch &lt;x&gt;.txt</pre>');
    expect(html).toContain('>terminal</button>');
    const buttons = options(html);
    expect(buttons.map((match) => [match[1], match[2]])).toEqual([
      ['answer-option first', 'Answer 1: Yes'],
      ['answer-option', 'Answer 2: Yes, and don&#x27;t ask again'],
      ['answer-option', 'Answer 3: No, and tell Claude what to do differently'],
    ]);
    expect(buttons.some((match) => match[3]?.includes('disabled'))).toBe(false);
    expect(html).toContain('<span class="answer-hint" aria-hidden="true">esc</span>');
    expect(html).not.toContain('Take control');
    expect(html).not.toContain('question changed');
  });

  it('leaves out the detail block when there is none', () => {
    const { detail: _detail, ...plain } = question;
    expect(card(controller, plain)).not.toContain('answer-detail');
  });

  it('offers Take control to a read-only phone and disables the options', () => {
    const html = card({ ...controller, control: 'observer' }, question, true);
    expect(html).toMatch(/<button type="button" class="primary answer-take">Take control to answer<\/button>/u);
    expect(options(html).every((match) => match[3]?.includes('disabled'))).toBe(true);
    expect(card({ ...controller, control: 'acquiring' })).toContain('Taking control…');
  });

  it('disables every option while an answer is pending and marks the tapped one', () => {
    const answer: AnswerState = { questionId: id, optionIndex: 1, phase: 'pending' };
    const html = card({ ...controller, answer });
    const buttons = options(html);
    expect(buttons.every((match) => match[3]?.includes('disabled'))).toBe(true);
    expect(buttons[1]?.[3]).toContain('aria-busy="true"');
    expect(html.match(/class="spinner"/gu)).toHaveLength(1);
  });

  it('says the question changed and waits for a fresh question before enabling answers', () => {
    const answer: AnswerState = { questionId: id, optionIndex: 0, phase: 'changed' };
    const old = card({ ...controller, answer });
    expect(old).toContain('The question changed — check the new one');
    expect(options(old).every((match) => match[3]?.includes('disabled'))).toBe(true);

    const fresh = card({ ...controller, answer }, { ...question, id: 'cd'.repeat(16) });
    expect(fresh).toContain('The question changed — check the new one');
    expect(options(fresh).some((match) => match[3]?.includes('disabled'))).toBe(false);
  });

  it('opens only for a waiting question that this phone has not answered yet', () => {
    expect(openQuestion(controller)).toBe(question);
    expect(openQuestion({ ...controller, link: 'reconnecting' })).toBeUndefined();
    expect(openQuestion({ ...controller, agent: { state: 'working', stateSince: agent.stateSince } })).toBeUndefined();
    expect(openQuestion({ ...controller, answer: { questionId: id, optionIndex: 0, phase: 'sent' } })).toBeUndefined();
    expect(openQuestion({ ...controller, control: 'observer' })).toBe(question);
  });

  it('splits a trailing shortcut hint off a label', () => {
    expect(splitShortcut('No (esc)')).toEqual({ text: 'No', hint: 'esc' });
    expect(splitShortcut('Yes (Y)')).toEqual({ text: 'Yes', hint: 'y' });
    expect(splitShortcut('Yes (and more)')).toEqual({ text: 'Yes (and more)', hint: undefined });
  });

  it('folds to a small pill', () => {
    expect(renderToStaticMarkup(<AnswerPill onOpen={noop} />)).toContain('class="answer-pill"');
  });
});
