import { useEffect, useMemo, useRef, useState } from 'react';
import { useAuth } from '../components/auth';
import { Icon } from '../components/Icon';
import { Empty, ErrorNote, Modal, StateBadge, useToast } from '../components/ui';
import { ContainerRow, get, post, Stats } from '../lib/api';
import { ago, bytes } from '../lib/format';
import { useFetch } from '../lib/hooks';
import { match } from '../lib/match';
import { Terminal } from './Terminal';

export function ContainersPage() {
  const { isAdmin } = useAuth();
  const toast = useToast();
  const { data, error, loading, reload } = useFetch<ContainerRow[]>('/containers', { poll: 10000 });
  const [q, setQ] = useState('');
  const [onlyRunning, setOnlyRunning] = useState(false);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<ContainerRow | null>(null);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [removeVolumes, setRemoveVolumes] = useState(false);

  const rows = useMemo(() => {
    const m = match(q);
    return (data || []).filter((c) => (m(c.name) || m(c.image) || m(c.stack)) && (!onlyRunning || c.state === 'running'));
  }, [data, q, onlyRunning]);
  const all = rows.length > 0 && rows.every((r) => sel.has(r.id));

  const bulk = async (action: string) => {
    try {
      const res = await post<{ id: string; name: string; error?: string }[]>('/containers/bulk', { ids: [...sel], action, removeVolumes });
      const failed = res.filter((r) => r.error);
      if (failed.length) toast(failed.map((f) => `${f.name}: ${f.error}`).join('\n'), true);
      else toast(`${action}: ${res.length} container(s) done`);
      setSel(new Set());
      reload();
    } catch (e: any) {
      toast(e.message, true);
    }
  };

  return (
    <>
      <div className="page-head"><h1>Containers</h1></div>
      <div className="btn-row">
        <label className="search"><Icon name="search" size={16} /><span className="sr-only">Search containers</span>
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter by name, image or stack" />
        </label>
        <button className="chip" aria-pressed={onlyRunning} onClick={() => setOnlyRunning(!onlyRunning)}>Running only</button>
        <button className="btn ghost icon" aria-label="Refresh" onClick={() => reload()}><Icon name="refresh" size={16} className={loading ? 'spin' : ''} /></button>
      </div>
      <ErrorNote error={error} />
      <div className="table-wrap" style={{ flex: 1, minHeight: 200 }}>
        <table className="t">
          <thead><tr>
            {isAdmin && <th className="cb"><input type="checkbox" aria-label="Select all" checked={all} onChange={() => setSel(all ? new Set() : new Set(rows.map((r) => r.id)))} /></th>}
            <th>Name</th><th>Status</th><th>Image</th><th>Ports</th><th>Stack</th><th>Created</th>
          </tr></thead>
          <tbody>
            {rows.map((c) => (
              <tr key={c.id} className={'clickable' + (sel.has(c.id) ? ' sel' : '')} onClick={() => setOpen(c)}>
                {isAdmin && <td className="cb" onClick={(e) => e.stopPropagation()}>
                  <input type="checkbox" aria-label={'Select ' + c.name} checked={sel.has(c.id)} onChange={() => { const s = new Set(sel); s.has(c.id) ? s.delete(c.id) : s.add(c.id); setSel(s); }} />
                </td>}
                <td><strong>{c.name}</strong>{c.self && <span className="badge info" style={{ marginLeft: 6 }}>xDocker</span>}</td>
                <td><StateBadge state={c.state} /> {c.health && <span className={'badge ' + (c.health === 'healthy' ? 'ok' : c.health === 'unhealthy' ? 'danger' : 'warn')}>{c.health}</span>}<div className="sub">{c.status}</div></td>
                <td className="mono" style={{ maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={c.image}>{c.image}</td>
                <td className="mono" style={{ fontSize: 12 }}>{portsText(c)}</td>
                <td>{c.stack || <span className="muted">—</span>}</td>
                <td className="nowrap">{ago(c.created)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {!loading && rows.length === 0 && <Empty>No containers.</Empty>}
      </div>
      {isAdmin && sel.size > 0 && (
        <div className="bulkbar">
          <span className="info">{sel.size} selected</span>
          <span className="spacer" />
          <button className="btn sm" onClick={() => setSel(new Set())}>Clear</button>
          <button className="btn sm" onClick={() => bulk('start')}><Icon name="play" size={14} />Start</button>
          <button className="btn sm" onClick={() => bulk('stop')}><Icon name="stop" size={14} />Stop</button>
          <button className="btn sm" onClick={() => bulk('restart')}><Icon name="restart" size={14} />Restart</button>
          <button className="btn danger sm" onClick={() => setConfirmRemove(true)}><Icon name="trash" size={14} />Remove…</button>
        </div>
      )}
      {confirmRemove && (
        <Modal title={`Remove ${sel.size} container(s)?`} onClose={() => setConfirmRemove(false)}
          footer={<><span className="spacer" /><button className="btn" onClick={() => setConfirmRemove(false)}>Cancel</button>
            <button className="btn danger" onClick={() => { setConfirmRemove(false); bulk('remove'); }}>Remove</button></>}>
          <p style={{ margin: 0 }}>Running containers are stopped first. This cannot be undone.</p>
          <ul style={{ margin: 0 }}>{(data || []).filter((c) => sel.has(c.id)).map((c) => <li key={c.id}>{c.name}</li>)}</ul>
          <label className="check"><input type="checkbox" checked={removeVolumes} onChange={(e) => setRemoveVolumes(e.target.checked)} />Also remove anonymous volumes</label>
        </Modal>
      )}
      {open && <ContainerDrawer c={open} onClose={() => { setOpen(null); reload(); }} />}
    </>
  );
}

function portsText(c: ContainerRow) {
  const seen = new Set<string>();
  return c.ports.filter((p) => p.PublicPort).map((p) => {
    const s = `${p.PublicPort}→${p.PrivatePort}${p.Type === 'udp' ? '/udp' : ''}`;
    if (seen.has(s)) return null;
    seen.add(s);
    return s;
  }).filter(Boolean).join(', ') || '—';
}

function ContainerDrawer({ c, onClose }: { c: ContainerRow; onClose: () => void }) {
  const { isAdmin } = useAuth();
  const toast = useToast();
  const [tab, setTab] = useState<'overview' | 'logs' | 'stats' | 'terminal' | 'inspect'>('overview');
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && tab !== 'terminal' && onClose();
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose, tab]);
  const act = async (action: string) => {
    try {
      const r = await post<{ error?: string }[]>('/containers/bulk', { ids: [c.id], action });
      if (r[0]?.error) toast(r[0].error, true);
      else toast(`${c.name}: ${action} done`);
    } catch (e: any) {
      toast(e.message, true);
    }
  };
  const tabs = ['overview', 'logs', 'stats', ...(isAdmin && c.state === 'running' ? ['terminal'] : []), 'inspect'] as const;
  return (
    <div className="drawer-overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <aside className="drawer" role="dialog" aria-modal="true" aria-label={c.name}>
        <div className="drawer-head">
          <h2>{c.name}</h2>
          <StateBadge state={c.state} />
          {isAdmin && <div className="btn-row">
            <button className="btn sm" onClick={() => act('start')}>Start</button>
            <button className="btn sm" onClick={() => act('stop')}>Stop</button>
            <button className="btn sm" onClick={() => act('restart')}>Restart</button>
          </div>}
          <button className="btn ghost icon" aria-label="Close" onClick={onClose}><Icon name="close" size={16} /></button>
        </div>
        <div className="tabs" role="tablist">
          {tabs.map((t) => <button key={t} role="tab" aria-selected={tab === t} onClick={() => setTab(t as any)}>{t[0].toUpperCase() + t.slice(1)}</button>)}
        </div>
        <div className="drawer-body">
          {tab === 'overview' && (
            <dl className="kv">
              <dt>ID</dt><dd className="mono">{c.id.slice(0, 12)}</dd>
              <dt>Image</dt><dd className="mono">{c.image}</dd>
              <dt>Status</dt><dd>{c.status}</dd>
              <dt>Stack</dt><dd>{c.stack || '—'}</dd>
              <dt>Ports</dt><dd className="mono">{portsText(c)}</dd>
              <dt>Created</dt><dd>{new Date(c.created * 1000).toLocaleString()}</dd>
            </dl>
          )}
          {tab === 'logs' && <Logs id={c.id} />}
          {tab === 'stats' && <StatsView id={c.id} running={c.state === 'running'} />}
          {tab === 'terminal' && <Terminal containerId={c.id} />}
          {tab === 'inspect' && <Inspect id={c.id} />}
        </div>
      </aside>
    </div>
  );
}

function Logs({ id }: { id: string }) {
  const [lines, setLines] = useState<string[]>([]);
  const [follow, setFollow] = useState(true);
  const [filter, setFilter] = useState('');
  const [timestamps, setTimestamps] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    setLines([]);
    if (!follow) {
      get<string>(`/containers/${id}/logs?tail=1000${timestamps ? '&timestamps=1' : ''}`).then((t) => setLines(String(t).split('\n'))).catch((e) => setLines([String(e.message)]));
      return;
    }
    const es = new EventSource(`/api/v1/containers/${id}/logs?tail=500&follow=1${timestamps ? '&timestamps=1' : ''}`);
    let buf: string[] = [];
    const flush = setInterval(() => {
      if (buf.length) {
        const add = buf;
        buf = [];
        setLines((l) => [...l, ...add].slice(-5000));
      }
    }, 250);
    es.onmessage = (e) => buf.push(JSON.parse(e.data));
    es.addEventListener('end', () => es.close());
    return () => {
      es.close();
      clearInterval(flush);
    };
  }, [id, follow, timestamps]);
  useEffect(() => {
    if (follow && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [lines, follow]);
  const m = match(filter);
  const shown = filter ? lines.filter((l) => m(l)) : lines;
  const download = () => {
    const blob = new Blob([lines.join('\n')], { type: 'text/plain' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `${id.slice(0, 12)}.log`;
    a.click();
    URL.revokeObjectURL(a.href);
  };
  return (
    <>
      <div className="btn-row">
        <label className="search" style={{ flex: '1 1 200px' }}><Icon name="search" size={16} /><input aria-label="Filter log lines" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter lines" /></label>
        <label className="check"><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />Follow</label>
        <label className="check"><input type="checkbox" checked={timestamps} onChange={(e) => setTimestamps(e.target.checked)} />Timestamps</label>
        <button className="btn sm" onClick={download}><Icon name="download" size={14} />Download</button>
      </div>
      <div className="logbox" ref={box}>{shown.join('\n') || 'No log output.'}</div>
    </>
  );
}

function StatsView({ id, running }: { id: string; running: boolean }) {
  const [hist, setHist] = useState<Stats[]>([]);
  const [err, setErr] = useState('');
  useEffect(() => {
    if (!running) return;
    let stop = false;
    const tick = async () => {
      try {
        const s = await get<Stats>(`/containers/${id}/stats`);
        if (!stop) setHist((h) => [...h, s].slice(-40));
      } catch (e: any) {
        setErr(e.message);
      }
      if (!stop) setTimeout(tick, 1000);
    };
    tick();
    return () => {
      stop = true;
    };
  }, [id, running]);
  if (!running) return <div className="muted">The container is not running.</div>;
  const s = hist[hist.length - 1];
  return (
    <>
      <ErrorNote error={err} />
      {s && (
        <div className="grid-4">
          <div className="stat"><span className="label">CPU</span><span className="value">{s.cpuPercent.toFixed(1)}%</span><Spark values={hist.map((h) => h.cpuPercent)} /></div>
          <div className="stat"><span className="label">Memory</span><span className="value" style={{ fontSize: 20 }}>{bytes(s.memUsage)}</span><span className="sub">of {bytes(s.memLimit)}</span><Spark values={hist.map((h) => h.memUsage)} /></div>
          <div className="stat"><span className="label">Network rx / tx</span><span className="value" style={{ fontSize: 18 }}>{bytes(s.netRx)} / {bytes(s.netTx)}</span></div>
          <div className="stat"><span className="label">Disk read / write</span><span className="value" style={{ fontSize: 18 }}>{bytes(s.blockRead)} / {bytes(s.blockWrite)}</span><span className="sub">{s.pids} processes</span></div>
        </div>
      )}
      {!s && !err && <div className="muted">Collecting…</div>}
    </>
  );
}

function Spark({ values }: { values: number[] }) {
  if (values.length < 2) return null;
  const max = Math.max(...values, 0.0001);
  const pts = values.map((v, i) => `${(i / (values.length - 1)) * 100},${30 - (v / max) * 28}`).join(' ');
  return (
    <svg viewBox="0 0 100 30" preserveAspectRatio="none" style={{ width: '100%', height: 32 }} aria-hidden="true">
      <polyline points={pts} fill="none" stroke="var(--accent)" strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

function Inspect({ id }: { id: string }) {
  const { data, error } = useFetch<any>(`/containers/${id}`);
  return (
    <>
      <ErrorNote error={error} />
      <div className="logbox" style={{ background: 'var(--bg-sunken)', color: 'var(--text)' }}>{data ? JSON.stringify(data, null, 2) : 'Loading…'}</div>
    </>
  );
}
