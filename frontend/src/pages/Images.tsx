import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useAuth } from '../components/auth';
import { DeleteDialog } from '../components/DeleteDialog';
import { Icon } from '../components/Icon';
import { useTrackJob } from '../components/JobToast';
import { Empty, ErrorNote, Modal, useToast } from '../components/ui';
import { DeletePlan, ImageRow, post, qs, Registry } from '../lib/api';
import { bytes, date, plural } from '../lib/format';
import { useDebounced, useFetch } from '../lib/hooks';

export function ImagesPage() {
  const { isAdmin } = useAuth();
  const toast = useToast();
  const track = useTrackJob();
  const [params, setParams] = useSearchParams();
  const [q, setQ] = useState(params.get('q') || '');
  const unused = params.get('unused') === '1';
  const dangling = params.get('dangling') === '1';
  const old = params.get('old') || '';
  const dq = useDebounced(q, 250);
  const before = old ? Math.floor(Date.now() / 1000) - parseInt(old, 10) * 86400 : undefined;
  const path = '/images' + qs({ q: dq, unused, dangling, before });
  const { data, error, loading, reload } = useFetch<ImageRow[]>(path);
  const rows = data || [];
  const [sel, setSel] = useState<Record<string, ImageRow>>({});
  const [plan, setPlan] = useState<DeletePlan | null>(null);
  const [force, setForce] = useState(false);
  const [pullOpen, setPullOpen] = useState(false);
  const [tagOf, setTagOf] = useState<ImageRow | null>(null);

  const toggleParam = (k: string, v: string) => {
    const p = new URLSearchParams(params);
    if (p.get(k) === v) p.delete(k);
    else p.set(k, v);
    setParams(p, { replace: true });
  };

  const selected = Object.values(sel);
  const allShown = rows.length > 0 && rows.every((r) => sel[r.ref]);
  const toggleAll = () => {
    const next = { ...sel };
    rows.forEach((r) => (allShown ? delete next[r.ref] : (next[r.ref] = r)));
    setSel(next);
  };
  const toggle = (r: ImageRow) => {
    const next = { ...sel };
    if (next[r.ref]) delete next[r.ref];
    else next[r.ref] = r;
    setSel(next);
  };
  const totals = useMemo(() => {
    const ids = new Set<string>();
    let size = 0;
    let inUse = 0;
    for (const r of selected) {
      if (!ids.has(r.id)) {
        ids.add(r.id);
        size += r.size;
      }
      if (r.usedBy.length) inUse++;
    }
    return { size, inUse };
  }, [selected]);

  const openPreview = async (f = force) => {
    try {
      setPlan(await post<DeletePlan>('/images/bulk-delete/preview', { refs: selected.map((r) => r.ref), force: f }));
    } catch (e: any) {
      toast(e.message, true);
    }
  };

  const prune = async () => {
    try {
      const r = await post<{ reclaimed: number }>('/images/prune');
      toast(`Removed dangling images, ${bytes(r.reclaimed)} freed`);
      reload();
    } catch (e: any) {
      toast(e.message, true);
    }
  };

  return (
    <>
      <div className="page-head">
        <h1>Images</h1>
        {isAdmin && (
          <div className="btn-row">
            <button className="btn" onClick={() => setPullOpen(true)}><Icon name="download" size={16} />Pull</button>
            <button className="btn" onClick={prune}><Icon name="broom" size={16} />Prune dangling</button>
          </div>
        )}
      </div>

      <div className="btn-row">
        <label className="search">
          <Icon name="search" size={16} />
          <span className="sr-only">Search images</span>
          <input value={q} placeholder="Search repository:tag — wildcards * or /regex/"
            onChange={(e) => { setQ(e.target.value); const p = new URLSearchParams(params); e.target.value ? p.set('q', e.target.value) : p.delete('q'); setParams(p, { replace: true }); }} />
        </label>
        <button className="chip" aria-pressed={unused} onClick={() => toggleParam('unused', '1')}>Unused only</button>
        <button className="chip" aria-pressed={dangling} onClick={() => toggleParam('dangling', '1')}>Dangling</button>
        <button className="chip" aria-pressed={old === '30'} onClick={() => toggleParam('old', '30')}>Older than 30 days</button>
        <button className="chip" aria-pressed={old === '90'} onClick={() => toggleParam('old', '90')}>Older than 90 days</button>
        <button className="btn ghost icon" aria-label="Refresh" onClick={() => reload()}><Icon name="refresh" size={16} className={loading ? 'spin' : ''} /></button>
      </div>
      <ErrorNote error={error} />
      <div className="hint-line">
        Showing <strong style={{ color: 'var(--text)' }}>{rows.length}</strong> image tags · {bytes(sumUnique(rows))}
        {rows.length > 0 && isAdmin && !allShown && <> · <button className="btn ghost sm" style={{ height: 24 }} onClick={toggleAll}>Select all {rows.length} results</button></>}
      </div>

      <div className="table-wrap" style={{ flex: 1, minHeight: 200 }}>
        <table className="t">
          <thead>
            <tr>
              {isAdmin && <th className="cb"><input type="checkbox" aria-label="Select all shown images" checked={allShown} onChange={toggleAll} /></th>}
              <th>Repository : tag</th>
              <th>Image ID</th>
              <th className="num">Size</th>
              <th>Created</th>
              <th>Used by</th>
              {isAdmin && <th style={{ width: 50 }}></th>}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.ref} className={sel[r.ref] ? 'sel' : ''}>
                {isAdmin && <td className="cb"><input type="checkbox" aria-label={'Select ' + r.ref} checked={!!sel[r.ref]} onChange={() => toggle(r)} /></td>}
                <td>
                  <div className="cell-ref">
                    <strong>{r.repo}</strong><span className="tag">:{r.tag}</span>
                    {r.dangling && <span className="badge warn">dangling</span>}
                    {r.tagCount > 1 && <span className="badge muted" title="This image has more than one tag">{r.tagCount} tags</span>}
                  </div>
                </td>
                <td className="mono">{r.shortId}</td>
                <td className="num">{bytes(r.size)}</td>
                <td className="nowrap">{date(r.created)}</td>
                <td>
                  {r.usedBy.length ? r.usedBy.map((c) => (
                    <span key={c} className={'badge ' + (r.running ? 'ok' : 'muted')} style={{ marginRight: 4 }}>{r.running && <span className="dot ok" style={{ width: 6, height: 6 }} />}{c}</span>
                  )) : <span className="muted">—</span>}
                </td>
                {isAdmin && <td><button className="btn ghost icon sm" aria-label={'Tag or push ' + r.ref} title="Tag / push" onClick={() => setTagOf(r)}><Icon name="tag" size={16} /></button></td>}
              </tr>
            ))}
          </tbody>
        </table>
        {!loading && rows.length === 0 && <Empty>No images match. Try a wildcard such as <code>azeno/*:2026.0*</code></Empty>}
      </div>

      {isAdmin && (selected.length > 0 ? (
        <div className="bulkbar">
          <span className="info">{selected.length} selected · {bytes(totals.size)}</span>
          {totals.inUse > 0 && <span className="warn">{totals.inUse} in use, will be skipped</span>}
          <span className="spacer" />
          <button className="btn sm" onClick={() => setSel({})}>Clear</button>
          <button className="btn danger sm" onClick={() => openPreview()}><Icon name="trash" size={14} />Delete {plural(selected.length, 'image')}…</button>
        </div>
      ) : (
        <div className="hint-line">Tick images, or use the header box to select every shown result, then delete them in one go.</div>
      ))}

      {plan && (
        <DeleteDialog what="images" plan={plan} onClose={() => setPlan(null)}
          run={async (confirm) => (await post<{ jobId: number }>('/images/bulk-delete', { refs: selected.map((r) => r.ref), force, confirm })).jobId}
          onFinished={() => { setSel({}); reload(); }}
          options={
            <label className="check">
              <input type="checkbox" checked={force} onChange={(e) => { setForce(e.target.checked); openPreview(e.target.checked); }} />
              <span>Force: also remove images used by <em>stopped</em> containers. Images of running containers are never removed.</span>
            </label>
          } />
      )}
      {pullOpen && <PullDialog onClose={() => setPullOpen(false)} onStarted={(id) => { track(id, () => reload()); setPullOpen(false); }} />}
      {tagOf && <TagDialog image={tagOf} onClose={() => setTagOf(null)} onDone={(id) => { if (id) track(id, () => reload()); setTagOf(null); reload(); }} />}
    </>
  );
}

function sumUnique(rows: ImageRow[]) {
  const seen = new Set<string>();
  let n = 0;
  for (const r of rows) if (!seen.has(r.id)) { seen.add(r.id); n += r.size; }
  return n;
}

function PullDialog({ onClose, onStarted }: { onClose: () => void; onStarted: (jobId: number) => void }) {
  const [ref, setRef] = useState('');
  const [err, setErr] = useState('');
  const go = async () => {
    try {
      onStarted((await post<{ jobId: number }>('/images/pull', { ref })).jobId);
    } catch (e: any) {
      setErr(e.message);
    }
  };
  return (
    <Modal title="Pull an image" onClose={onClose} footer={<><span className="spacer" /><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={!ref.trim()} onClick={go}>Pull</button></>}>
      <label className="field">Image reference
        <input className="input mono" value={ref} onChange={(e) => setRef(e.target.value)} placeholder="nginx:1.27-alpine or registry.nas.lan:5050/azeno/api:latest" onKeyDown={(e) => e.key === 'Enter' && go()} />
        <span className="hint">Credentials of a configured registry are used automatically when the reference starts with its host.</span>
      </label>
      {err && <div className="error-text">{err}</div>}
    </Modal>
  );
}

function TagDialog({ image, onClose, onDone }: { image: ImageRow; onClose: () => void; onDone: (jobId?: number) => void }) {
  const { data: regs } = useFetch<Registry[]>('/registries');
  const hostOf = (u: string) => u.replace(/^https?:\/\//, '').split('/')[0];
  const [target, setTarget] = useState(image.dangling ? '' : `${image.repo}:${image.tag}`);
  const [push, setPush] = useState(true);
  const [err, setErr] = useState('');
  const go = async () => {
    try {
      await post('/images/tag', { source: image.dangling ? image.id : image.ref, target });
      if (push && target.includes('/') && target.split('/')[0].match(/[.:]/)) {
        onDone((await post<{ jobId: number }>('/images/push', { ref: target })).jobId);
      } else onDone();
    } catch (e: any) {
      setErr(e.message);
    }
  };
  return (
    <Modal title={`Tag ${image.ref}`} onClose={onClose} footer={<><span className="spacer" /><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={!target.trim()} onClick={go}>{push ? 'Tag and push' : 'Tag'}</button></>}>
      <label className="field">New reference
        <input className="input mono" value={target} onChange={(e) => setTarget(e.target.value)} />
      </label>
      {regs && regs.length > 0 && (
        <div className="btn-row">
          <span className="muted" style={{ fontSize: 13 }}>Prefix with registry:</span>
          {regs.map((g) => (
            <button key={g.id} className="chip" onClick={() => setTarget(hostOf(g.url) + '/' + target.replace(/^[^/]*[.:][^/]*\//, ''))}>{g.name}</button>
          ))}
        </div>
      )}
      <label className="check"><input type="checkbox" checked={push} onChange={(e) => setPush(e.target.checked)} />Push after tagging (when the reference names a registry host)</label>
      {err && <div className="error-text">{err}</div>}
    </Modal>
  );
}
