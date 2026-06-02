// Client. Connects to /ws, renders the shared shell with xterm.js, respects the
// announced role, and (for spectators) mirrors the owner's exact terminal size
// so replayed scrollback and live output line up. Phase 6 adds a collapsible
// file panel (FileBrowser) for upload/download/delete behind --share-dir.
//
// Protocol
//   browser -> server : keystrokes as BINARY; resize as TEXT {"type":"resize",...}
//   server -> browser : shell output as BINARY; control/status as TEXT
//                       role:  {"type":"role","role":"owner|spectator"}
//                       size:  {"type":"size","cols":N,"rows":N}
const term = new Terminal({
  cursorBlink: true,
  fontFamily: 'Menlo, Consolas, "DejaVu Sans Mono", monospace',
  fontSize: 14,
  scrollback: 5000,
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
      } else if (msg && msg.type === 'size') {
        applySize(msg.cols, msg.rows);
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

// Only the owner controls the shared shell's size; the owner's window fit is the
// source of truth. Spectators are sized by the server's size messages instead.
function sendResize() {
  if (readOnly) return;
  if (ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
  }
}
term.onResize(sendResize);
window.addEventListener('resize', () => { if (!readOnly) fitAddon.fit(); });

// Spectators mirror the owner's exact dimensions so the shared screen aligns.
function applySize(cols, rows) {
  if (!readOnly) return;
  if (cols > 0 && rows > 0) term.resize(cols, rows);
}

function applyRole() {
  const badge = document.getElementById('ro-badge');
  if (readOnly) {
    badge.hidden = false;
    term.options.cursorBlink = false;
  } else {
    badge.hidden = true;
    fitAddon.fit(); // owner: size to our own window
    sendResize();
  }
  FileBrowser.setReadOnly(readOnly);
}

// FileBrowser drives the optional file panel. It probes /api/files once on
// load: if the server was started without --share-dir the probe 404s and the
// panel stays hidden, so the same bundled UI works with or without the feature.
const FileBrowser = (() => {
  const els = {
    toggle: document.getElementById('files-toggle'),
    count: document.getElementById('files-count'),
    panel: document.getElementById('file-panel'),
    backdrop: document.getElementById('fp-backdrop'),
    close: document.getElementById('fp-close'),
    list: document.getElementById('fp-list'),
    empty: document.getElementById('fp-empty'),
    upload: document.getElementById('fp-upload'),
    drop: document.getElementById('fp-drop'),
    input: document.getElementById('fp-input'),
    status: document.getElementById('fp-status'),
  };
  let enabled = false;
  let ro = true;       // assume read-only until a role arrives
  let entries = [];    // last-known listing, used for overwrite detection

  const SVG = {
    download: '<svg viewBox="0 0 24 24" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12m0 0l-4-4m4 4l4-4M4 21h16"/></svg>',
    trash: '<svg viewBox="0 0 24 24" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2m2 0v12a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V7"/></svg>',
  };

  function setStatus(msg, kind) {
    els.status.textContent = msg || '';
    els.status.className = kind || '';
  }

  function humanSize(n) {
    if (n < 1024) return n + ' B';
    const u = ['KB', 'MB', 'GB', 'TB'];
    let i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
    return n.toFixed(n < 10 ? 1 : 0) + ' ' + u[i];
  }

  function when(unixSeconds) {
    try { return new Date(unixSeconds * 1000).toLocaleString(); }
    catch (_) { return ''; }
  }

  function open() {
    els.panel.hidden = false;
    els.backdrop.hidden = false;
    els.toggle.setAttribute('aria-expanded', 'true');
    refresh();
  }
  function close() {
    els.panel.hidden = true;
    els.backdrop.hidden = true;
    els.toggle.setAttribute('aria-expanded', 'false');
    term.focus();
  }
  function toggle() { els.panel.hidden ? open() : close(); }

  function render() {
    els.list.innerHTML = '';
    els.empty.hidden = entries.length > 0;
    els.count.textContent = entries.length ? String(entries.length) : '';
    for (const e of entries) {
      const li = document.createElement('li');
      li.className = 'fp-row';

      const meta = document.createElement('div');
      meta.className = 'fp-meta';
      const name = document.createElement('span');
      name.className = 'fp-name';
      name.textContent = e.name;              // textContent: no HTML injection
      name.title = e.name;
      const sub = document.createElement('span');
      sub.className = 'fp-sub';
      sub.textContent = humanSize(e.size) + ' \u00b7 ' + when(e.modified);
      meta.appendChild(name);
      meta.appendChild(sub);

      const dl = document.createElement('a');
      dl.className = 'fp-act fp-download';
      dl.href = '/api/download/' + encodeURIComponent(e.name);
      dl.setAttribute('download', e.name);
      dl.setAttribute('aria-label', 'Download ' + e.name);
      dl.innerHTML = SVG.download;

      li.appendChild(meta);
      li.appendChild(dl);

      if (!ro) {
        const del = document.createElement('button');
        del.type = 'button';
        del.className = 'fp-act fp-delete';
        del.setAttribute('aria-label', 'Delete ' + e.name);
        del.innerHTML = SVG.trash;
        del.addEventListener('click', () => remove(e.name));
        li.appendChild(del);
      }
      els.list.appendChild(li);
    }
  }

  async function probeAndMaybeEnable() {
    try {
      const res = await fetch('/api/files', { headers: { Accept: 'application/json' } });
      const ctype = res.headers.get('Content-Type') || '';
      if (!res.ok || !ctype.includes('application/json')) return; // feature off
      const data = await res.json();
      entries = Array.isArray(data.files) ? data.files : [];
      enabled = true;
      els.toggle.hidden = false;
      els.count.textContent = entries.length ? String(entries.length) : '';
      wire();
    } catch (_) { /* network/feature unavailable: leave panel hidden */ }
  }

  async function refresh() {
    if (!enabled) return;
    try {
      const res = await fetch('/api/files', { headers: { Accept: 'application/json' } });
      if (!res.ok) { setStatus('Could not load files.', 'error'); return; }
      const data = await res.json();
      entries = Array.isArray(data.files) ? data.files : [];
      render();
    } catch (_) {
      setStatus('Could not load files.', 'error');
    }
  }

  async function upload(fileList) {
    if (ro || !fileList || fileList.length === 0) return;

    // Warn before clobbering: collect names that already exist.
    const existing = new Set(entries.map((e) => e.name));
    const collisions = Array.from(fileList)
      .map((f) => f.name)
      .filter((n) => existing.has(n));
    if (collisions.length > 0) {
      const list = collisions.slice(0, 8).join('\n  ');
      const more = collisions.length > 8 ? `\n  ...and ${collisions.length - 8} more` : '';
      const ok = window.confirm(
        `These files already exist and will be overwritten:\n  ${list}${more}\n\nContinue?`
      );
      if (!ok) { setStatus('Upload cancelled.', ''); return; }
    }

    const form = new FormData();
    for (const f of fileList) form.append('files', f, f.name);
    setStatus(`Uploading ${fileList.length} file(s)\u2026`, '');
    try {
      const res = await fetch('/api/upload', { method: 'POST', body: form });
      const data = await res.json().catch(() => ({}));
      const up = (data.uploaded || []).length;
      const failed = data.failed || [];
      if (failed.length === 0 && up > 0) {
        setStatus(`Uploaded ${up} file(s).`, 'ok');
      } else if (up > 0) {
        setStatus(`Uploaded ${up}; ${failed.length} failed (${failed.map((f) => f.name).join(', ')}).`, 'error');
      } else {
        const why = failed[0] && failed[0].reason ? ` (${failed[0].reason})` : '';
        setStatus(`Upload failed${why}.`, 'error');
      }
      await refresh();
    } catch (_) {
      setStatus('Upload failed.', 'error');
    }
  }

  async function remove(name) {
    if (ro) return;
    const ok = window.confirm(`Delete ${name}? This cannot be undone.`);
    if (!ok) return;
    setStatus(`Deleting ${name}...`, '');
    try {
      const res = await fetch('/api/delete/' + encodeURIComponent(name), { method: 'POST' });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setStatus(`Delete failed${data.error ? ` (${data.error})` : ''}.`, 'error');
        return;
      }
      setStatus(`Deleted ${name}.`, 'ok');
      await refresh();
    } catch (_) {
      setStatus('Delete failed.', 'error');
    }
  }

  function bindUpload() {
    els.drop.addEventListener('dragover', (ev) => {
      if (ro) return;
      ev.preventDefault();
      els.drop.classList.add('dragover');
    });
    els.drop.addEventListener('dragleave', () => els.drop.classList.remove('dragover'));
    els.drop.addEventListener('drop', (ev) => {
      if (ro) return;
      ev.preventDefault();
      els.drop.classList.remove('dragover');
      upload(ev.dataTransfer.files);
    });
    els.drop.addEventListener('click', () => { if (!ro) els.input.click(); });
    els.input.addEventListener('change', () => upload(els.input.files));
  }

  function wire() {
    els.toggle.addEventListener('click', toggle);
    els.close.addEventListener('click', close);
    els.backdrop.addEventListener('click', close);
    bindUpload();
    setReadOnly(readOnly);
    render();
  }

  function setReadOnly(next) {
    ro = next;
    if (!enabled) return;
    els.upload.hidden = ro;
    if (ro && els.panel.hidden === false) setStatus('Read-only (spectator).', '');
  }

  probeAndMaybeEnable();

  return { setReadOnly };
})();