// Tests for the station status page -- server/static/{index.html,
// style.css,app.js}.
//
// Why these exist: PR #11 set out to change the status labels and
// changed a code comment instead, so nothing moved on screen and
// nothing caught it. Every test here is about what a visitor actually
// sees after the page renders real /api/status data, which is the one
// thing a reading of the diff didn't prove.
//
// The page is loaded from disk through its own <script src> tag, so a
// renamed or mis-referenced asset fails here too -- not just a bug in
// the code the tag pulls in.
//
// Run: npm test   (from the repo root)
'use strict';

const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');

const STATIC = path.join(__dirname, '..', 'static');
const PAGE_URL = 'file://' + path.join(STATIC, 'index.html');

const VERSION = { version: '2026.10.11', commit: 'abc1234', deployedAt: '2026-10-15T12:00:00Z' };

// Every booted page keeps polling on its own 3s timer, exactly as it
// does in a browser -- which would hold the test run open indefinitely,
// so each window is closed once the file's tests are done.
const windows = [];
after(() => windows.forEach((w) => w.close()));

// Boots the real page with a stubbed API and waits for the first render.
async function boot(status, { version = VERSION, failStatus = false } = {}) {
  const dom = new JSDOM(fs.readFileSync(path.join(STATIC, 'index.html'), 'utf8'), {
    runScripts: 'dangerously',
    resources: 'usable', // so <script src="app.js?v=N"> is really fetched
    url: PAGE_URL,
    beforeParse(window) {
      window.fetch = async (url) => {
        if (String(url).includes('/api/version')) return { json: async () => version };
        if (failStatus) throw new Error('connection refused');
        return { json: async () => status };
      };
    },
  });
  windows.push(dom.window);
  const doc = dom.window.document;
  for (let i = 0; i < 100 && doc.getElementById('content').textContent === 'Loading…'; i++) {
    await new Promise((r) => setTimeout(r, 10));
  }
  return doc;
}

const radio = (over = {}) => ({
  label: 'FLEX-6600', status: 'online', freq_hz: 14074000, mode: 'FT8',
  operator: 'WT2P', source: 'tci', tx: false, ...over,
});
const station = (radios, over = {}) => ({ stations: [{ station_name: 'WT2P', radios, ...over }] });

const cells = (tr) => [...tr.querySelectorAll('td')].map((td) => td.textContent.trim());

test('the pills say IN USE and AVAILABLE, not ONLINE and OFFLINE', async () => {
  // The change PR #11 meant to make. If someone edits STATUS_LABEL in
  // app.js but nothing calls it -- the exact shape of that failure --
  // this is what fails.
  const doc = await boot(station([radio(), radio({ label: 'IC-7300', status: 'offline' })]));
  const pills = [...doc.querySelectorAll('.status-pill')].map((p) => p.textContent);
  assert.deepEqual(pills, ['IN USE', 'AVAILABLE']);
  assert.ok(!/ONLINE|OFFLINE/.test(doc.body.textContent), 'the old words are gone from the page');
});

test('the pill keeps the API\'s own value as its class, which is what the colours hang off', async () => {
  const doc = await boot(station([radio(), radio({ status: 'offline' })]));
  const classes = [...doc.querySelectorAll('.status-pill')].map((p) => p.className);
  assert.deepEqual(classes, ['status-pill online', 'status-pill offline']);
  assert.deepEqual([...doc.querySelectorAll('tbody tr')].map((tr) => tr.className), ['online', 'offline']);
});

test('available is green and in use is red', () => {
  // Read from the stylesheet, because the swap is only half-done if the
  // rule still points at the colour that used to mean "link is up".
  const css = fs.readFileSync(path.join(STATIC, 'style.css'), 'utf8');
  assert.match(css, /--status-available:\s*#1a7f37/);
  assert.match(css, /--status-in-use:\s*#ff3b3b/);
  assert.match(css, /\.status-pill\.offline\s*{\s*color:\s*var\(--status-available\)/);
  assert.match(css, /\.status-pill\.online\s*{\s*color:\s*var\(--status-in-use\)/);
});

test('a status the server invents later still shows up, upper-cased', async () => {
  const doc = await boot(station([radio({ status: 'degraded' })]));
  assert.equal(doc.querySelector('.status-pill').textContent, 'DEGRADED');
});

test('an available radio shows dashes, not its last known reading', async () => {
  const doc = await boot(station([
    radio({ status: 'offline', freq_hz: 14074000, mode: 'FT8', operator: 'WT2P' }),
  ]));
  const [label, freq, mode, operator] = cells(doc.querySelector('tbody tr'));
  assert.equal(label, 'FLEX-6600');
  assert.deepEqual([freq, mode, operator], ['—', '—', '—']);
});

test('an in-use radio shows frequency, mode and operator', async () => {
  const doc = await boot(station([radio({ operator: 'K9CT' })]));
  assert.deepEqual(cells(doc.querySelector('tbody tr')).slice(1, 4), ['14.0740 MHz', 'FT8', 'K9CT']);
});

test('contest mode shows the band behind a padlock and never the frequency', async () => {
  const doc = await boot(station([radio({ freq_hz: null, band: '40M' })]));
  const masked = doc.querySelector('.band-masked');
  assert.ok(masked, 'no masked-band cell');
  assert.match(masked.textContent, /40M/);
  assert.ok(masked.querySelector('svg.lock-icon'), 'no padlock');
  assert.match(masked.getAttribute('title'), /never leaves the LAN/);
  assert.ok(!/MHz/.test(doc.body.textContent), 'a masked reading must not print a frequency');
  assert.ok(doc.querySelector('.legend'), 'the contest legend explains the padlock');
});

test('the TX chip means a TCI radio keyed right now, and nothing else', async () => {
  const keyed = await boot(station([radio({ tx: true })]));
  assert.ok(keyed.querySelector('.tx-chip'), 'a keyed TCI radio should show TX');

  for (const over of [{ tx: false }, { tx: true, source: 'n1mm' }, { tx: true, status: 'offline' }]) {
    const doc = await boot(station([radio(over)]));
    assert.equal(doc.querySelector('.tx-chip'), null, `TX shown for ${JSON.stringify(over)}`);
  }
});

test('the source tag names the route, and guesses at nothing', async () => {
  for (const [source, expected] of [['tci', 'TCI'], ['n1mm', 'Logger'], ['dxlog', 'Logger']]) {
    const doc = await boot(station([radio({ source })]));
    assert.equal(doc.querySelector('.source-tag').textContent, expected);
  }
  const unknown = await boot(station([radio({ source: 'something-new' })]));
  assert.equal(unknown.querySelector('.source-tag'), null);
});

test('data from the shack LAN renders as text, never as markup', async () => {
  // Callsigns, modes and operator names come off N1MM/DXLog and ADIF
  // records -- not from our own config.
  const doc = await boot(station(
    [radio({ mode: '<img src=x onerror=boom>', operator: '<b>OP</b>' })],
    { last_worked: { call: '<script>x</script>', band: '20M', mode: 'CW', source: 'qrzero', at: '2026-10-15T11:22:00Z' } },
  ));
  assert.equal(doc.querySelector('#content img'), null);
  assert.equal(doc.querySelector('#content b'), null);
  assert.equal(doc.querySelector('#content script'), null);
  assert.match(doc.body.textContent, /<b>OP<\/b>/, 'it shows as the text it is');
});

test('the last-worked line reads call, band, mode, route, time', async () => {
  const doc = await boot(station([radio()], {
    last_worked: { call: 'DL1ABC', band: '20M', mode: 'CW', source: 'qrzero', at: '2026-10-15T11:22:00Z' },
  }));
  const lw = doc.querySelector('.last-worked');
  assert.match(lw.textContent, /Last worked/);
  assert.equal(lw.querySelector('.lw-call').textContent, 'DL1ABC');
  assert.equal(lw.querySelector('.lw-band').textContent, '20M');
  assert.equal(lw.querySelector('.lw-mode').textContent, 'CW');
  assert.equal(lw.querySelector('.source-tag').textContent, 'QRZero Logged');
  assert.equal(lw.querySelector('.lw-when').textContent, '2026-10-15 11:22Z');

  // Another route gets no chip rather than a wrong one -- an ADIF
  // record doesn't say which program sent it.
  const other = await boot(station([radio()], {
    last_worked: { call: 'G0ABC', at: '2026-10-15T11:22:00Z', source: 'elsewhere' },
  }));
  assert.equal(other.querySelector('.last-worked .source-tag'), null);
});

test('no stations reporting says so', async () => {
  const doc = await boot({ stations: [] });
  assert.equal(doc.querySelector('.empty-state').textContent, 'No stations reporting yet.');
});

test('a failed poll says what went wrong instead of going blank', async () => {
  const doc = await boot(null, { failStatus: true });
  assert.match(doc.getElementById('content').textContent, /Error loading status:/);
});

test('the footer stamps the running version, and stays hidden without one', async () => {
  const doc = await boot(station([radio()]));
  const stamp = doc.getElementById('deploy-info');
  assert.equal(stamp.hidden, false);
  assert.match(stamp.textContent, /v2026\.10\.11 · deployed 2026-10-15 12:00:00Z \(abc1234\)/);

  const bare = await boot(station([radio()]), { version: {} });
  assert.equal(bare.getElementById('deploy-info').hidden, true);
});

test('the page links assets that exist, at the version it claims', () => {
  // A rename that misses index.html is a blank page in production and
  // a passing unit test everywhere else, so check the wiring itself.
  const html = fs.readFileSync(path.join(STATIC, 'index.html'), 'utf8');
  for (const asset of ['style.css', 'app.js']) {
    const ref = new RegExp(`(?:href|src)="(?:/)?${asset.replace('.', '\\.')}\\?v=(\\d+)"`);
    assert.match(html, ref, `index.html doesn't reference ${asset}`);
    assert.ok(fs.existsSync(path.join(STATIC, asset)), `${asset} is referenced but missing`);
  }
  // 404.html shares the stylesheet rather than carrying its own copy,
  // and must use an absolute path: it's served at whatever URL was
  // mistyped, including ones with slashes in them.
  const notFound = fs.readFileSync(path.join(STATIC, '404.html'), 'utf8');
  assert.match(notFound, /<link rel="stylesheet" href="\/style\.css\?v=\d+">/);
  assert.ok(!/<style>/.test(notFound), '404.html should not carry its own copy of the CSS');
});
