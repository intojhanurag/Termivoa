#!/usr/bin/env node
// Neutral stand-in for a coding agent, used only for the docs screenshots.
// Its busy line ("esc to interrupt") and numbered question follow the common
// agent prompt shape on purpose, so the host detects Working and Waiting.
// capture.mjs copies it to $HOME/bin/agent in the demo home.
// Usage: agent work            works forever (busy line with "esc to interrupt")
//        agent ask [seconds]   works for some seconds, then asks a numbered question
import { basename } from 'node:path';

const [mode = 'ask', secondsArg = '0'] = process.argv.slice(2);
const workFor = mode === 'work' ? Infinity : Number(secondsArg);
const project = basename(process.cwd());
const write = (text) => process.stdout.write(text);
const columns = Math.max(30, (process.stdout.columns || 44) - 1);
// Word-wraps text to the terminal width, so nothing breaks mid-word.
const wrap = (text, indent = '') => {
  const lines = []; let line = '';
  for (const word of text.split(' ')) {
    if (line && (indent + line + ' ' + word).length > columns) { lines.push(indent + line); line = word; } else line = line ? `${line} ${word}` : word;
  }
  lines.push(indent + line);
  return lines.join('\n');
};
const c = { dim: (s) => `\x1b[2m${s}\x1b[0m`, bold: (s) => `\x1b[1m${s}\x1b[0m`, cyan: (s) => `\x1b[36m${s}\x1b[0m`, green: (s) => `\x1b[32m${s}\x1b[0m`, red: (s) => `\x1b[31m${s}\x1b[0m`, amber: (s) => `\x1b[33m${s}\x1b[0m` };

const stories = {
  'acme-web': {
    task: 'Add rate limiting to the /api routes and cover it with tests.',
    steps: [
      ['Read', 'src/server.ts', '9 lines'],
      ['Read', 'src/lib/cache.ts', '17 lines'],
      ['Create', 'src/lib/rate-limit.ts', '+22'],
      ['Update', 'src/server.ts', '+2'],
      ['Update', 'src/lib/cache.ts', '+3 −1'],
    ],
    busy: 'Writing tests',
    note: 'Rate limits are in place: 60 requests a minute per client.',
    question: 'Run the test suite?',
    detail: 'npm test · 24 tests in 6 files',
    options: ['Yes', "Yes, and don't ask again", 'No, tell the agent what to do (esc)'],
  },
  'billing-api': {
    task: 'Move invoice totals to the new tax table and keep old invoices unchanged.',
    steps: [
      ['Read', 'src/invoices.ts', '112 lines'],
      ['Read', 'src/tax.ts', '64 lines'],
      ['Update', 'src/tax.ts', '+18 −7'],
      ['Update', 'src/invoices.ts', '+9 −4'],
      ['Run', 'npm test -- invoices', '14 passed'],
    ],
    busy: 'Checking totals',
    note: 'Totals now read rates from the new tax table.',
    question: 'Apply the migration to the local database?',
    detail: 'npm run db:migrate · 1 new migration',
    options: ['Yes', 'No (esc)'],
  },
};
const story = stories[project] ?? stories['acme-web'];

write('\x1b[?25l');
const restore = () => write('\x1b[?25h');
process.on('exit', restore);

write(`${c.cyan('◆')} ${c.bold('agent')} ${c.dim(`· ${project}`)}\n\n`);
write(`${c.dim('›')} ${wrap(story.task, '  ').slice(2)}\n\n`);
for (const [verb, what, info] of story.steps) {
  write(`${c.green('●')} ${c.bold(verb)} ${what} ${c.dim(`(${info})`)}\n`);
}
write('\n');

const started = Date.now();
const frames = ['✻', '✳', '✢', '✶'];
let frame = 0;
function busyLine() {
  const seconds = Math.floor((Date.now() - started) / 1000);
  write(`\r\x1b[2K${c.amber(frames[frame++ % frames.length])} ${story.busy}… ${c.dim(`${seconds}s · esc to interrupt`)}`);
}

function ask() {
  write('\r\x1b[2K');
  write(`${wrap(story.note, '  ')}\n\n`);
  write(`${c.dim('─'.repeat(Math.min(columns, 40)))}\n`);
  write(` ${c.bold(story.question)}\n`);
  write(` ${c.dim(story.detail)}\n\n`);
  story.options.forEach((option, index) => {
    write(index === 0 ? `${c.cyan('❯')} ${c.cyan(`${index + 1}. ${option}`)}\n` : `  ${index + 1}. ${option}\n`);
  });
  write(`\n ${c.dim('Enter to confirm · Esc to cancel')}`);
  process.stdin.setRawMode(true);
  process.stdin.resume();
  process.stdin.on('data', (data) => {
    const key = data.toString();
    if (key === '\x1b' || key === '\x03' || key === String(story.options.length)) {
      write(`\n\n${c.dim('Stopped. Tell me what to do instead.')}\n`);
      process.exit(0);
    }
    if (key === '\r' || key === '1' || key === '2') {
      write(`\n\n${c.green('●')} ${c.bold('Run')} ${story.detail.split(' · ')[0]}\n  ${c.green('✓')} 24 passed ${c.dim('(1.8s)')}\n\n`);
      process.exit(0);
    }
  });
}

busyLine();
const timer = setInterval(() => {
  if ((Date.now() - started) / 1000 >= workFor) {
    clearInterval(timer);
    ask();
  } else {
    busyLine();
  }
}, 1000);
if (workFor === 0) {
  clearInterval(timer);
  ask();
}
