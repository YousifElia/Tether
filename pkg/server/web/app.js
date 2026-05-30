// Phase 2 client. Connects to /ws, renders the shared shell with xterm.js, and
// respects the role announced by the server.
//
// Protocol
//   browser -> server : keystrokes as BINARY; resize as TEXT {"type":"resize",...}
//   server -> browser : shell output as BINARY; control/status as TEXT
//                       (role announce: {"type":"role","role":"owner|spectator"})
const term = new Terminal({
  cursorBlink: true,
  fontFamily: 'Menlo, Consolas, "DejaVu Sans Mono", monospace',
  fontSize: 14,
  theme: { background: '#0b0b0e' },
});
const fitAddon = new FitAddon.FitAddon();
term.loadAddon(fitAddon);
term.open(document.getElementById('terminal'));
fitAddon.fit();

const proto = location.protocol === 'https:' ? 'wss' : 'ws';
const ws = new WebSocket(`${proto}://${location.host}/ws`);
ws.binaryType = 'arraybuffer';
const enc = new TextEncoder();

let readOnly = false;

ws.onopen = () => { sendResize(); term.focus(); };

ws.onmessage = (ev) => {
  if (typeof ev.data === 'string') {
    // Either a JSON control message or plain status text.
    let handled = false;
    try {
      const msg = JSON.parse(ev.data);
      if (msg && msg.type === 'role') {
        readOnly = msg.role !== 'owner';
        applyRole();
        handled = true;
      }
    } catch (_) { /* not JSON: fall through to status text */ }
    if (!handled) term.write(ev.data);
  } else {
    term.write(new Uint8Array(ev.data));
  }
};

ws.onclose = () => term.write('\r\n\x1b[90m[disconnected]\x1b[0m\r\n');
ws.onerror = () => term.write('\r\n\x1b[31m[connection error]\x1b[0m\r\n');

// Keystrokes: owners only. Spectators send nothing (server also ignores input).
term.onData((data) => {
  if (readOnly) return;
  if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(data));
});

// Only the owner controls the shared shell's size.
function sendResize() {
  if (readOnly) return;
  if (ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
  }
}
term.onResize(sendResize);
window.addEventListener('resize', () => fitAddon.fit());

function applyRole() {
  const badge = document.getElementById('ro-badge');
  if (readOnly) {
    badge.hidden = false;
    term.options.cursorBlink = false;
  } else {
    badge.hidden = true;
  }
}
