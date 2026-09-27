import type { KeyName, Modifiers } from '../generated/protocol';
import type { RunFirstRequest } from '../screens/presets';
import { canSendWith, type ConnectionStatus } from './Connection';
import { NO_MODIFIERS } from './InputController';

export interface RunFirstTarget {
  subscribe(listener: () => void): () => void;
  getStatus(): ConnectionStatus;
  takeControl(): void;
  sendText(text: string): boolean;
  sendKey(key: KeyName, modifiers: Modifiers): boolean;
}

/**
 * Types a new session's "run first" command exactly once.
 *
 * After the first snapshot is applied (link becomes live), it asks for control
 * only when no one holds it, then sends the command and Enter and keeps
 * control. Any other outcome (someone else has control, the grant fails, the
 * link drops, the shell ends) gives up without sending and reports why. It
 * never retries: a reconnect starts read-only like every other attach.
 */
export function runFirstCommand(
  target: RunFirstTarget,
  request: RunFirstRequest,
  report: (message: string) => void,
): () => void {
  const quoted = `“${request.command}”`;
  let acquiring = false;
  let unsubscribe: () => void = () => undefined;

  const settle = (message?: string) => {
    request.settled = true;
    unsubscribe();
    if (message !== undefined) report(message);
  };

  const check = () => {
    if (request.settled) return;
    const status = target.getStatus();
    const ended = status.sessionState === 'exited' || status.sessionState === 'exiting' || status.link === 'lost';
    if (!acquiring) {
      if (ended) {
        settle(`Did not run ${quoted}: the session ended.`);
        return;
      }
      if (status.link !== 'live') return;
      if (status.otherController || status.control !== 'observer') {
        settle(`Did not run ${quoted}: another connection has control. Nothing was sent.`);
        return;
      }
      acquiring = true;
      target.takeControl();
      return;
    }
    if (status.control === 'acquiring' && !ended) return;
    if (status.control === 'controller' && canSendWith(status)) {
      // Settle first: sending updates the status and re-enters this listener.
      settle();
      if (!target.sendText(request.command) || !target.sendKey('enter', NO_MODIFIERS)) {
        report(`${quoted} may not have been sent. Check the terminal before sending again.`);
      }
      return;
    }
    settle(`Did not run ${quoted}: this phone could not take control. Nothing was sent.`);
  };

  unsubscribe = target.subscribe(check);
  check();
  return () => unsubscribe();
}
