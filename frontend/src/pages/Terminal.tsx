import { useEffect, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';

/** Interactive shell in a container over /api/v1/containers/{id}/exec. */
export function Terminal({ containerId }: { containerId: string }) {
  const host = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState('connecting…');
  useEffect(() => {
    if (!host.current) return;
    const term = new XTerm({ cursorBlink: true, fontSize: 13, fontFamily: 'JetBrains Mono, Menlo, monospace', theme: { background: '#0f1418' } });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host.current);
    fit.fit();
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${proto}//${location.host}/api/v1/containers/${containerId}/exec`);
    ws.binaryType = 'arraybuffer';
    const sendSize = () => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ resize: { rows: term.rows, cols: term.cols } }));
    };
    ws.onopen = () => {
      setStatus('connected');
      sendSize();
      term.focus();
    };
    ws.onmessage = (e) => term.write(typeof e.data === 'string' ? e.data : new Uint8Array(e.data));
    ws.onclose = (e) => {
      setStatus('closed' + (e.reason ? ': ' + e.reason : ''));
      term.write('\r\n\x1b[90m[session closed]\x1b[0m\r\n');
    };
    const enc = new TextEncoder();
    const sub = term.onData((d) => ws.readyState === WebSocket.OPEN && ws.send(enc.encode(d)));
    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
        sendSize();
      } catch {
        /* element hidden */
      }
    });
    ro.observe(host.current);
    return () => {
      ro.disconnect();
      sub.dispose();
      ws.close();
      term.dispose();
    };
  }, [containerId]);
  return (
    <>
      <div className="muted" style={{ fontSize: 12 }}>Shell: bash if available, else sh · {status}</div>
      <div className="term" ref={host} />
    </>
  );
}
