const $ = id => document.getElementById(id);

const wsColor = '#00c2ff';
const wtColor = '#ff6b35';

let running = false;
let stopReq = false;

const samples = { ws: [], wt: [] };

function stats(arr) {
  if (!arr.length) return null;
  const s = [...arr].sort((a, b) => a - b);
  const avg = s.reduce((a, b) => a + b, 0) / s.length;
  const variance = s.reduce((a, b) => a + (b - avg) ** 2, 0) / s.length;
  return {
    avg,
    min: s[0],
    max: s[s.length - 1],
    p95: s[Math.floor(s.length * 0.95)],
    std: Math.sqrt(variance),
  };
}

function fmt(n) { return n == null ? '—' : n.toFixed(2); }

function updateStats(key) {
  const st = stats(samples[key]);
  if (!st) return;
  $(`${key}Avg`).innerHTML = `${fmt(st.avg)}<span>ms</span>`;
  $(`${key}Min`).textContent = fmt(st.min);
  $(`${key}Max`).textContent = fmt(st.max);
  $(`${key}P95`).textContent = fmt(st.p95);
  $(`${key}Std`).textContent = fmt(st.std);
  $(`${key}Done`).textContent = samples[key].length;
}

function drawChart() {
  const canvas = $('chart');
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.parentElement.clientWidth - 40;
  const h = 140;
  canvas.style.width = w + 'px';
  canvas.style.height = h + 'px';
  canvas.width = w * dpr;
  canvas.height = h * dpr;

  const ctx = canvas.getContext('2d');
  ctx.scale(dpr, dpr);
  ctx.clearRect(0, 0, w, h);

  const all = [...samples.ws, ...samples.wt];
  if (!all.length) return;

  const maxVal = Math.max(...all) * 1.1;
  const minVal = 0;

  function drawLine(data, color) {
    if (!data.length) return;
    ctx.beginPath();
    ctx.strokeStyle = color;
    ctx.lineWidth = 1.5;
    ctx.globalAlpha = 0.85;
    data.forEach((v, i) => {
      const x = (i / (data.length - 1 || 1)) * w;
      const y = h - ((v - minVal) / (maxVal - minVal)) * h;
      i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y);
    });
    ctx.stroke();
    ctx.globalAlpha = 1;
  }

  // Grid lines
  ctx.strokeStyle = '#1a1a1a';
  ctx.lineWidth = 1;
  for (let i = 0; i <= 4; i++) {
    const y = (i / 4) * h;
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(w, y);
    ctx.stroke();
  }

  drawLine(samples.ws, wsColor);
  drawLine(samples.wt, wtColor);
}

async function pingWS(url, count, interval, onPing, onLost) {
  return new Promise(resolve => {
    const ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';
    let seq = 0;
    const pending = new Map();
    let done = 0;
    let lost = 0;

    ws.onopen = () => {
      const tick = setInterval(() => {
        if (stopReq || seq >= count) {
          clearInterval(tick);
          setTimeout(() => {
            pending.forEach(() => { lost++; onLost(); });
            pending.clear();
            ws.close();
            resolve({ lost });
          }, 1000);
          return;
        }
        const buf = new Uint8Array(9);
        const view = new DataView(buf.buffer);
        view.setUint8(0, 0x02);
        view.setUint32(1, seq, false);
        pending.set(seq, performance.now());
        ws.send(buf);
        seq++;
      }, interval);
    };

    ws.onmessage = e => {
      const view = new DataView(e.data instanceof ArrayBuffer ? e.data : new ArrayBuffer(0));
      if (e.data instanceof ArrayBuffer && e.data.byteLength >= 5) {
        const s = view.getUint32(1, false);
        if (pending.has(s)) {
          const rtt = performance.now() - pending.get(s);
          pending.delete(s);
          done++;
          onPing(rtt);
        }
      }
    };

    ws.onerror = () => resolve({ lost: count });
    ws.onclose = () => {};
  });
}

async function pingWT(url, count, interval, onPing, onLost) {
  return new Promise(async resolve => {
    let wt;
    try {
      wt = new WebTransport(url);
      await wt.ready;
    } catch(e) {
      resolve({ lost: count, error: e.message });
      return;
    }

    let seq = 0;
    const pending = new Map();
    let lost = 0;

    const reader = wt.datagrams.readable.getReader();
    (async () => {
      while (true) {
        let result;
        try { result = await reader.read(); } catch { break; }
        if (result.done) break;
        const view = new DataView(result.value.buffer);
        if (result.value.byteLength >= 9 && view.getUint8(0) === 0x02) {
          const s = view.getUint32(1, false);
          if (pending.has(s)) {
            const rtt = performance.now() - pending.get(s);
            pending.delete(s);
            onPing(rtt);
          }
        }
      }
    })();

    const writer = wt.datagrams.writable.getWriter();

    const tick = setInterval(async () => {
      if (stopReq || seq >= count) {
        clearInterval(tick);
        setTimeout(() => {
          pending.forEach(() => { lost++; onLost(); });
          pending.clear();
          wt.close();
          resolve({ lost });
        }, 1000);
        return;
      }
      const buf = new Uint8Array(9);
      const view = new DataView(buf.buffer);
      view.setUint8(0, 0x02);
      view.setUint32(1, seq, false);
      pending.set(seq, performance.now());
      seq++;
      try { await writer.write(buf); } catch {}
    }, interval);
  });
}

async function run() {
  const wsUrl = $('wsUrl').value.trim();
  const wtUrl = $('wtUrl').value.trim();
  const count = parseInt($('pingCount').value);
  const interval = parseInt($('pingInterval').value);

  samples.ws = [];
  samples.wt = [];
  stopReq = false;
  running = true;

  $('btnRun').disabled = true;
  $('btnStop').style.display = 'inline-block';

  ['ws','wt'].forEach(k => {
    $(`${k}Avg`).innerHTML = `—<span>ms</span>`;
    ['Min','Max','P95','Std','Lost','Done'].forEach(s => $(`${k}${s}`).textContent = '—');
  });

  setStatus('connecting...');

  let wsLost = 0, wtLost = 0;

  const wsPromise = pingWS(wsUrl, count, interval,
    rtt => { samples.ws.push(rtt); updateStats('ws'); drawChart(); },
    () => { wsLost++; $('wsLost').textContent = wsLost; }
  ).catch(e => ({ lost: count, error: e.message }));

  const wtPromise = pingWT(wtUrl, count, interval,
    rtt => { samples.wt.push(rtt); updateStats('wt'); drawChart(); },
    () => { wtLost++; $('wtLost').textContent = wtLost; }
  ).catch(e => ({ lost: count, error: e.message }));

  setStatus('running...');

  const [wsRes, wtRes] = await Promise.all([wsPromise, wtPromise]);

  $('wsLost').textContent = wsRes.lost || 0;
  $('wtLost').textContent = wtRes.lost || 0;

  running = false;
  $('btnRun').disabled = false;
  $('btnStop').style.display = 'none';

  if (wsRes.error || wtRes.error) {
    setStatus((wsRes.error ? 'ws: ' + wsRes.error + ' ' : '') + (wtRes.error ? 'wt: ' + wtRes.error : ''), 'error');
  } else if (stopReq) {
    setStatus('stopped', '');
  } else {
    const wsAvg = stats(samples.ws)?.avg;
    const wtAvg = stats(samples.wt)?.avg;
    if (wsAvg && wtAvg) {
      const faster = wsAvg < wtAvg ? 'websocket' : 'webtransport';
      const diff = Math.abs(wsAvg - wtAvg).toFixed(2);
      setStatus(`${faster} faster by ${diff}ms avg`, 'ok');
    } else {
      setStatus('done', 'ok');
    }
  }
}

function setStatus(msg, cls = '') {
  const el = $('status');
  el.textContent = msg;
  el.className = 'status ' + cls;
}

$('btnRun').onclick = run;
$('btnStop').onclick = () => { stopReq = true; setStatus('stopping...'); };
