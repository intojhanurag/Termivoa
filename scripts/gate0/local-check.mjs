// Local two-host check for the Gate 0 harness (see README.md, "Local check").
//
//   node scripts/gate0/local-check.mjs chrome <url>...   Google Chrome over CDP, no packages
//   PW=<playwright-core dir> node scripts/gate0/local-check.mjs webkit <url>...
//
// Opens each URL in one browser profile, in order, and prints the page's results.
// A URL with ?auto=1 runs checks 1, 2 and 4; a URL without it prints check 3.
// Chrome maps *.tailx.ts.net and *.taily.ts.net to 127.0.0.1 with --host-resolver-rules;
// WebKit has no such flag, so a small CONNECT proxy sends every host to 127.0.0.1.
import { spawn } from 'node:child_process';
import net from 'node:net';

const [engine, ...urls] = process.argv.slice(2);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const resultsJS = "(location.search ? document.getElementById('summary').textContent : results[3] && JSON.stringify(results)) || ''";
const print = (url, out) => console.log('== ' + url + '\n' + JSON.stringify(JSON.parse(out || '{}'), null, 1));

if (engine === 'chrome') {
  const chrome = spawn('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', ['--headless',
    '--user-data-dir=/tmp/gate0-chrome', '--remote-debugging-port=9333', '--ignore-certificate-errors',
    '--host-resolver-rules=MAP *.tailx.ts.net 127.0.0.1, MAP *.taily.ts.net 127.0.0.1', 'about:blank'], { stdio: 'ignore' });
  try {
    let targets;
    for (let i = 0; i < 50 && !targets; i++) {
      await sleep(200);
      targets = await fetch('http://127.0.0.1:9333/json').then((r) => r.json()).catch(() => null);
    }
    const ws = new WebSocket(targets.find((t) => t.type === 'page').webSocketDebuggerUrl);
    await new Promise((r) => ws.addEventListener('open', r));
    let id = 0;
    const pending = {};
    ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); pending[m.id]?.(m.result); });
    const send = (method, params = {}) => new Promise((r) => { pending[++id] = r; ws.send(JSON.stringify({ id, method, params })); });
    console.log('Chrome ' + (await send('Browser.getVersion')).product);
    for (const url of urls) {
      await send('Page.navigate', { url });
      let out = '';
      for (let i = 0; i < 60 && !out; i++) {
        await sleep(500);
        out = (await send('Runtime.evaluate', { expression: resultsJS })).result?.value;
      }
      print(url, out);
    }
    send('Browser.close'); // a clean exit writes localStorage and cookies to disk
    await Promise.race([new Promise((r) => chrome.once('exit', r)), sleep(5000)]);
  } finally {
    chrome.kill();
  }
} else if (engine === 'webkit') {
  const { webkit } = await import(process.env.PW + '/index.mjs');
  const proxy = net.createServer((client) => {
    client.once('data', (head) => {
      const port = /^CONNECT [^:]+:(\d+)/.exec(head.toString())?.[1];
      if (!port) return client.destroy();
      const upstream = net.connect(Number(port), '127.0.0.1', () => {
        client.write('HTTP/1.1 200 Connection Established\r\n\r\n');
        client.pipe(upstream).pipe(client);
      });
      upstream.on('error', () => client.destroy());
    });
  }).listen(9334, '127.0.0.1');
  const browser = await webkit.launch({ proxy: { server: 'http://127.0.0.1:9334' } });
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  console.log('WebKit ' + browser.version());
  try {
    for (const url of urls) {
      const page = await context.newPage();
      await page.goto(url);
      await page.waitForFunction(resultsJS, null, { timeout: 30000 });
      print(url, await page.evaluate(resultsJS));
    }
  } finally {
    await browser.close();
    proxy.close();
  }
} else {
  console.error('usage: node scripts/gate0/local-check.mjs chrome|webkit <url>...');
  process.exit(2);
}
