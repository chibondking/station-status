// station-status -- the page's behaviour: poll /api/status every few
// seconds and redraw the station tables.
//
// Split out of index.html 2026-10-15, and reorganised around one rule:
// anything a person reads on the page is declared at the top of this
// file, not buried in the middle of the code that draws it. The labels
// used to live inside a template literal 500 lines into an HTML file,
// which is how "change Online to In Use" turned into a one-line edit of
// a code comment that changed nothing on screen.
//
// Two vocabularies, deliberately kept apart:
//   the WIRE values -- "online" / "offline" from /api/status. They are
//     the API contract (the ops dashboard charts /api/health's counts),
//     they drive the CSS class names, and they decide which cells get
//     blanked. Changing one is a server + agent + dashboard change.
//   the LABELS below -- what a human reads. Changing one is a word.

// ---------------------------------------------------------------------
// Wording. Everything the page says, in one place.
// ---------------------------------------------------------------------

// Wire status -> the text in the Status pill. On a shared station the
// useful question isn't "is the radio's link up" but "is someone on it"
// (CJ, 2026-10-15), hence IN USE / AVAILABLE rather than ONLINE /
// OFFLINE. A status we don't recognise falls back to its own name
// upper-cased, so a new one from the server shows up rather than
// vanishing.
const STATUS_LABEL = {
  online: 'IN USE',
  offline: 'AVAILABLE',
};

// Where a reading came from -- shown as a small tag beside the status.
// Anything not listed gets no tag rather than a guess.
const SOURCE_TAG = {
  tci: 'TCI',
  n1mm: 'Logger',
  dxlog: 'Logger',
};

const TEXT = {
  loading: 'Loading…',
  noStations: 'No stations reporting yet.',
  errorPrefix: 'Error loading status: ',
  blank: '—', // em dash: a cell with nothing to say
  lastWorked: 'Last worked',
  lastWorkedChip: 'QRZero Logged',
  refreshed: 'Last refreshed: ',
  contestLegend: 'Contest mode — exact frequency kept on the shack LAN',
  txChip: 'TX',
  txTitle: 'Transmitting right now (live from the radio over TCI)',
  maskTitle: 'Contest mode — the shack agent is publishing only the band; ' +
             'the exact frequency never leaves the LAN.',
  columns: ['Radio', 'Freq', 'Mode', 'Operator', 'Status'],
};

// How often to re-poll /api/status. The server recomputes online/offline
// on every request, so this is also how quickly a radio going quiet
// shows up.
const POLL_MS = 3000;

// ---------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------

// Every piece of text on this page goes through textContent, never
// innerHTML. Callsigns, modes and operator names arrive from N1MM/DXLog
// and from ADIF records off the shack LAN -- not from our own config --
// and this is the one rule that keeps that from mattering.
function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = text;
  return node;
}

function fmtFreq(hz) {
  if (hz === null || hz === undefined) return TEXT.blank;
  return (hz / 1e6).toFixed(4) + ' MHz';
}

// UTC ("Zulu"), the standard on-air time reference, used for every time
// on this page.
function fmtZulu(date) {
  return date.toISOString().slice(11, 19) + 'Z';
}

// Date and time of a QSO as UTC -- the two fields on their own, since
// that's what an operator reads off a log. Falls back to the raw string
// if it isn't a timestamp we can parse, rather than printing
// "Invalid Date".
function fmtQsoWhen(at) {
  const d = new Date(at);
  if (isNaN(d.getTime())) return { date: at || '', time: '' };
  const iso = d.toISOString();
  return { date: iso.slice(0, 10), time: iso.slice(11, 16) + 'Z' };
}

// Feather-style padlock, sized to the surrounding text (1em), inheriting
// colour via currentColor. A fixed string of our own -- the only markup
// on the page that isn't built as elements.
const LOCK_SVG =
  '<svg class="lock-icon" viewBox="0 0 24 24" width="1em" height="1em"' +
  ' fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"' +
  ' stroke-linejoin="round" aria-hidden="true">' +
  '<rect x="4" y="11" width="16" height="10" rx="2"></rect>' +
  '<path d="M8 11V7a4 4 0 0 1 8 0v4"></path></svg>';

// ---------------------------------------------------------------------
// The pieces of a radio row
// ---------------------------------------------------------------------

// In contest mode the agent sends a band name ("40M") instead of the
// exact frequency (freq_hz is null then). Show the band behind a padlock
// so it reads as withheld on purpose, not missing.
function freqCell(radio) {
  if (radio.band) {
    const span = el('span', 'band-masked');
    span.title = TEXT.maskTitle;
    span.innerHTML = LOCK_SVG + '&nbsp;';
    span.appendChild(document.createTextNode(radio.band));
    return span;
  }
  return document.createTextNode(fmtFreq(radio.freq_hz));
}

// The [TX] chip: a TCI radio, in use, keyed right now. Gated on the
// source as well as the flag -- the server already sends tx:false for
// the logger sources, but the point of the chip is that it means
// something definite, and the only source that can say "keyed" rather
// than "probably keyed" is the direct radio link. A future source that
// can't really tell shouldn't be able to light this up with a hopeful
// true.
function txChip(radio) {
  if (radio.status !== 'online' || radio.source !== 'tci' || !radio.tx) return null;
  const chip = el('span', 'tx-chip', TEXT.txChip);
  chip.title = TEXT.txTitle;
  chip.setAttribute('role', 'status');
  chip.setAttribute('aria-label', 'Transmitting');
  return chip;
}

function sourceTag(source) {
  const label = SOURCE_TAG[source];
  return label ? el('span', 'source-tag', label) : null;
}

function statusCell(radio) {
  const td = el('td');
  td.setAttribute('data-label', 'Status');
  const label = STATUS_LABEL[radio.status] || String(radio.status || '').toUpperCase();
  td.appendChild(el('span', 'status-pill ' + radio.status, label));
  const tag = sourceTag(radio.source);
  if (tag) td.appendChild(tag);
  return td;
}

// Freq, Mode and Operator only mean anything while a radio is in use;
// an available one shows a dash in all three rather than its last known
// reading, which would look current and isn't.
function radioRow(radio) {
  const inUse = radio.status === 'online';
  const tr = el('tr', radio.status); // the wire value, which the CSS keys off

  const radioTd = el('td', null, radio.label);
  radioTd.setAttribute('data-label', 'Radio');
  const tx = txChip(radio);
  if (tx) radioTd.appendChild(tx);
  tr.appendChild(radioTd);

  const freqTd = el('td');
  freqTd.setAttribute('data-label', 'Freq');
  freqTd.appendChild(inUse ? freqCell(radio) : document.createTextNode(TEXT.blank));
  tr.appendChild(freqTd);

  for (const [column, value] of [['Mode', radio.mode], ['Operator', radio.operator]]) {
    const td = el('td', null, (inUse && value) ? value : TEXT.blank);
    td.setAttribute('data-label', column);
    tr.appendChild(td);
  }

  tr.appendChild(statusCell(radio));
  return tr;
}

// The chip on a last-worked QSO names the route it came in on -- the
// agent's ADIF-over-UDP port, which is QRZero's "QSO logged" connection
// -- not a detection of which program sent it; an ADIF record doesn't
// say.
function lastWorkedEl(lw) {
  const when = fmtQsoWhen(lw.at);
  const row = el('div', 'last-worked');
  row.appendChild(el('span', null, TEXT.lastWorked));
  row.appendChild(el('span', 'lw-call', lw.call));
  // Band, never a frequency: the agent masks a logged QSO's frequency
  // the way contest mode masks a radio's, so a band name is all that
  // reaches the server (see agent/adif.go).
  if (lw.band) row.appendChild(el('span', 'lw-band', lw.band));
  if (lw.mode) row.appendChild(el('span', 'lw-mode', lw.mode));
  if (lw.source === 'qrzero') row.appendChild(el('span', 'source-tag', TEXT.lastWorkedChip));
  row.appendChild(el('span', 'lw-when', [when.date, when.time].filter(Boolean).join(' ')));
  return row;
}

// ---------------------------------------------------------------------
// Drawing the page
// ---------------------------------------------------------------------

function stationTable(station) {
  const table = el('table');
  const thead = el('thead');
  const headRow = el('tr');
  for (const column of TEXT.columns) headRow.appendChild(el('th', null, column));
  thead.appendChild(headRow);
  table.appendChild(thead);

  const tbody = el('tbody');
  for (const radio of station.radios) tbody.appendChild(radioRow(radio));
  table.appendChild(tbody);
  return table;
}

function render(data) {
  const content = document.getElementById('content');
  content.innerHTML = '';

  const stations = data.stations || [];
  if (stations.length === 0) {
    // Leaves "Last refreshed" showing its previous value, as it always
    // has. Arguably it should still tick -- a stale timestamp under an
    // empty page reads as frozen -- but that's a behaviour change, not
    // a tidy-up, so it stays as it was.
    content.appendChild(el('div', 'empty-state', TEXT.noStations));
    return;
  }

  let anyMasked = false;
  for (const station of stations) {
    content.appendChild(el('div', 'station-name', station.station_name));
    for (const radio of station.radios) {
      if (radio.status === 'online' && radio.band) anyMasked = true;
    }
    content.appendChild(stationTable(station));
    if (station.last_worked && station.last_worked.call) {
      content.appendChild(lastWorkedEl(station.last_worked));
    }
  }

  if (anyMasked) {
    const legend = el('div', 'legend');
    legend.innerHTML = LOCK_SVG + ' ';
    legend.appendChild(document.createTextNode(TEXT.contestLegend));
    content.appendChild(legend);
  }

  document.getElementById('updated').textContent = TEXT.refreshed + fmtZulu(new Date());
}

// ---------------------------------------------------------------------
// Polling, and the deploy stamp
// ---------------------------------------------------------------------

async function poll() {
  try {
    const res = await fetch('/api/status', { cache: 'no-store' });
    render(await res.json());
  } catch (e) {
    document.getElementById('content').textContent = TEXT.errorPrefix + e;
  }
}

poll();
setInterval(poll, POLL_MS);

// Deploy stamp in the footer -- lets a viewer tell whether they're
// looking at a stale/cached page, since it only changes on an actual
// redeploy. Fetched once; no need to poll it.
fetch('/api/version', { cache: 'no-store' })
  .then((r) => r.json())
  .then((v) => {
    if (!v || !v.deployedAt) return;
    const d = new Date(v.deployedAt);
    if (isNaN(d.getTime())) return;
    const when = d.toISOString().slice(0, 19).replace('T', ' ') + 'Z';
    const stamp = document.getElementById('deploy-info');
    stamp.textContent = (v.version ? 'v' + v.version + ' · ' : '') +
      'deployed ' + when + (v.commit ? ' (' + v.commit + ')' : '');
    stamp.hidden = false;
  })
  .catch(() => {});
