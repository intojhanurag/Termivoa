import { useEffect, useState, type MouseEvent } from 'react';
import type { AgentQuestion } from '../generated/protocol';
import type { ConnectionStatus } from '../terminal/Connection';
import { since } from './activity';

const SHORTCUT = /\s*\((esc|[a-z])\)$/iu;
const keepFocus = (event: MouseEvent) => event.preventDefault();

/** The question the card offers, or undefined once this phone's answer was accepted. */
export function openQuestion(status: ConnectionStatus): AgentQuestion | undefined {
  const agent = status.agent;
  if (status.link !== 'live' || agent?.state !== 'waiting' || status.answer?.phase === 'sent') return undefined;
  return agent.question;
}

/** Splits a trailing "(esc)" / "(y)" shortcut hint off an option label. */
export function splitShortcut(label: string): { readonly text: string; readonly hint: string | undefined } {
  const match = SHORTCUT.exec(label);
  return match === null ? { text: label, hint: undefined } : { text: label.slice(0, match.index), hint: match[1]?.toLowerCase() };
}

/**
 * Bottom card for a waiting agent question. All text is terminal output and
 * renders as text only. One tap sends at most one answer; nothing answers or
 * retries on its own. Buttons keep focus where it is, so the keyboard stays.
 */
export function AnswerCard({ title, question, stateSince, status, canTake, onAnswer, onTakeControl, onHide }: {
  readonly title: string;
  readonly question: AgentQuestion;
  readonly stateSince: string;
  readonly status: ConnectionStatus;
  readonly canTake: boolean;
  readonly onAnswer: (optionIndex: number) => void;
  readonly onTakeControl: () => void;
  readonly onHide: () => void;
}) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const controller = status.control === 'controller';
  const answer = status.answer?.questionId === question.id ? status.answer : undefined;
  const changed = status.answer?.phase === 'changed';
  // Pending: wait for the result. Changed on this same question: wait for the fresh state.
  const disabled = !controller || status.answer?.phase === 'pending' || answer?.phase === 'changed';
  const elapsed = since(stateSince, now);
  return (
    <section className="answer-card" aria-labelledby="answer-question">
      <div className="grabber" aria-hidden="true" />
      <div className="answer-head">
        <p className="answer-asks">
          <span className="dot amber" aria-hidden="true" />
          {`${title.toUpperCase()} ASKS${elapsed === '' ? '' : ` · ${elapsed}`}`}
        </p>
        <button type="button" className="link" onMouseDown={keepFocus} onClick={onHide}>terminal</button>
      </div>
      <h2 id="answer-question" className="answer-question">{question.text}</h2>
      {question.detail !== undefined && <pre className="answer-detail">{question.detail}</pre>}
      {changed && <p className="answer-note" role="status">The question changed. Check the new one.</p>}
      {!controller && (
        <button type="button" className="primary answer-take" disabled={!canTake} onMouseDown={keepFocus} onClick={onTakeControl}>
          {status.control === 'acquiring' ? 'Taking control…' : 'Take control to answer'}
        </button>
      )}
      <div className="answer-options" role="group" aria-label="Answers">
        {question.options.map((option, index) => {
          const { text, hint } = splitShortcut(option.label);
          const pending = answer?.phase === 'pending' && answer.optionIndex === index;
          return (
            <button
              // Options are positional; a new question remounts the list.
              key={`${question.id}/${index}`}
              type="button"
              className={index === 0 ? 'answer-option first' : 'answer-option'}
              aria-label={`Answer ${index + 1}: ${text}`}
              {...(pending ? { 'aria-busy': true } : {})}
              disabled={disabled}
              onMouseDown={keepFocus}
              onClick={() => onAnswer(index)}
            >
              <span className="answer-number" aria-hidden="true">{index + 1}</span>
              <span className="answer-label">{text}</span>
              {pending ? <span className="spinner" aria-hidden="true" /> : hint !== undefined && <span className="answer-hint" aria-hidden="true">{hint}</span>}
            </button>
          );
        })}
      </div>
    </section>
  );
}

/** The folded card: a small amber pill above the dock that brings it back. */
export function AnswerPill({ onOpen }: { readonly onOpen: () => void }) {
  return (
    <button type="button" className="answer-pill" aria-label="Answer the question" onClick={onOpen}>
      <span aria-hidden="true">↑</span> answer
    </button>
  );
}
