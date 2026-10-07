import { FormEvent, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Icon } from '../components/Icon';
import { Empty, ErrorNote } from '../components/ui';
import { get, PortConflict, PortsView } from '../lib/api';
import { useFetch } from '../lib/hooks';

interface CheckResult { port: number; conflicts: PortConflict[]; free: boolean; suggestions: number[] }

export function PortsPage() {
  const { data, error, loading, reload } = useFetch<PortsView>('/ports', { poll: 20000 });
  const [filter, setFilter] = useState<'all' | 'conflict' | 'tcp' | 'udp'>('all');
  const [port, setPort] = useState('');
  const [proto, setProto] = useState('tcp');
  const [res, setRes] = useState<CheckResult | null>(null);
  const [checkErr, setCheckErr] = useState('');

  const rows = useMemo(() => (data?.ports || []).filter((p) =>
    filter === 'all' || (filter === 'conflict' ? p.status === 'conflict' : p.proto === filter)), [data, filter]);
  const conflicts = (data?.ports || []).filter((p) => p.status === 'conflict').length;

  const check = async (e?: FormEvent, p = port) => {
    e?.preventDefault();
    setCheckErr('');
    try {
      setRes(await get<CheckResult>(`/ports/check?port=${encodeURIComponent(p)}&proto=${proto}`));
    } catch (err: any) {
      setCheckErr(err.message);
      setRes(null);
    }
  };

  return (
    <>
      <div className="page-head">
        <h1>Ports</h1>
        <div className="seg" role="group" aria-label="Filter ports">
          <button aria-pressed={filter === 'all'} onClick={() => setFilter('all')}>All ({data?.ports.length ?? 0})</button>
          <button aria-pressed={filter === 'conflict'} onClick={() => setFilter('conflict')} style={{ color: filter === 'conflict' ? undefined : 'var(--danger-ink)', fontWeight: 600 }}>Conflicts ({conflicts})</button>
          <button aria-pressed={filter === 'tcp'} onClick={() => setFilter('tcp')}>TCP</button>
          <button aria-pressed={filter === 'udp'} onClick={() => setFilter('udp')}>UDP</button>
        </div>
        <button className="btn ghost icon" aria-label="Refresh" onClick={() => reload()}><Icon name="refresh" size={16} className={loading ? 'spin' : ''} /></button>
      </div>
      <ErrorNote error={error} />
      <div className="grid-2" style={{ gridTemplateColumns: 'minmax(0,1.3fr) minmax(0,1fr)' }}>
        <form className="card" onSubmit={check}>
          <h2>Check a port before you use it</h2>
          <div className="btn-row" style={{ alignItems: 'flex-end' }}>
            <label className="field">Host port<input className="input mono" style={{ width: 120 }} inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value)} placeholder="5050" /></label>
            <label className="field">Protocol<select className="select" value={proto} onChange={(e) => setProto(e.target.value)}><option value="tcp">TCP</option><option value="udp">UDP</option></select></label>
            <button className="btn primary" disabled={!port}>Check</button>
          </div>
          {checkErr && <div className="error-text">{checkErr}</div>}
          {res && (res.free
            ? <div className="note ok"><Icon name="check" style={{ color: 'var(--ok)' }} /><span><strong>{res.port}/{proto} is free.</strong></span></div>
            : <div className="note danger" role="status"><Icon name="warn" style={{ color: 'var(--danger)' }} />
                <span><strong>{res.port}/{proto} is taken</strong> by {res.conflicts.map((c) => c.name).join(', ')}.</span></div>)}
          {res && res.suggestions.length > 0 && (
            <div className="btn-row">
              <span className="muted" style={{ fontSize: 13 }}>{res.free ? 'Also free:' : 'Next free ports:'}</span>
              {res.suggestions.filter((s) => s !== res.port).map((s) => (
                <button type="button" key={s} className="chip mono" style={{ borderColor: 'var(--ok)', background: 'var(--ok-soft)', color: 'var(--ok-ink)' }}
                  onClick={() => { setPort(String(s)); check(undefined, String(s)); }}>{s}</button>
              ))}
            </div>
          )}
        </form>
        <section className="card">
          <div className="card-head"><h2>Reserved by DSM</h2><Link to="/settings" style={{ fontSize: 13 }}>Edit list</Link></div>
          <p className="muted" style={{ margin: 0, fontSize: 13 }}>Containers should not publish these. Checked before every stack deploy.</p>
          <div className="btn-row" style={{ gap: 6 }}>
            {data?.reserved.map((r) => <span key={r.port + r.proto} className="badge muted mono">{r.port}{r.proto === 'udp' ? '/udp' : ''} {r.name}</span>)}
          </div>
          {data && !data.hostChecked && <div className="hint-line">Host services are not checked: mount the host's /proc (see README) to include them.</div>}
          {data?.hostListeners && data.hostListeners.length > 0 && (
            <details><summary className="hint-line" style={{ cursor: 'pointer' }}>{data.hostListeners.length} other listening ports on the NAS</summary>
              <div className="btn-row" style={{ gap: 6, marginTop: 8 }}>{data.hostListeners.map((l) => <span key={l.ip + l.port + l.proto} className="badge muted mono">{l.port}/{l.proto}</span>)}</div>
            </details>
          )}
        </section>
      </div>
      <div className="table-wrap" style={{ flex: 1 }}>
        <table className="t">
          <thead><tr><th>Host IP</th><th>Host port</th><th>Proto</th><th>Container</th><th>Container port</th><th>Stack</th><th>Status</th></tr></thead>
          <tbody>
            {rows.map((p, i) => (
              <tr key={i} style={p.status === 'conflict' ? { background: 'var(--danger-soft)' } : undefined}>
                <td className="mono">{p.hostIp || '0.0.0.0'}</td>
                <td className="mono"><strong>{p.hostPort}</strong></td>
                <td>{p.proto}</td>
                <td><strong>{p.container}</strong> {p.state !== 'running' && <span className="sub">({p.state})</span>}</td>
                <td className="mono">{p.containerPort}</td>
                <td>{p.stack || <span className="muted">—</span>}</td>
                <td>{p.status === 'conflict' ? <span className="badge danger">{p.conflict}</span> : p.status === 'shared' ? <span className="badge warn">{p.conflict}</span> : p.status === 'local' ? <span className="badge info">Local only</span> : <span className="badge ok">In use</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {!loading && rows.length === 0 && <Empty>No published ports.</Empty>}
      </div>
    </>
  );
}
