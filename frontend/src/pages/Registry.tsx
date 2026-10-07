import { useEffect, useMemo, useState } from 'react';
import { useAuth } from '../components/auth';
import { DeleteDialog } from '../components/DeleteDialog';
import { Icon } from '../components/Icon';
import { useTrackJob } from '../components/JobToast';
import { Empty, ErrorNote, Modal, useToast } from '../components/ui';
import { del, DeletePlan, post, put, qs, Registry, RepoRow, TagRow } from '../lib/api';
import { bytes, date, plural } from '../lib/format';
import { useDebounced, useFetch, useStored } from '../lib/hooks';

export function RegistryPage() {
  const { isAdmin } = useAuth();
  const toast = useToast();
  const track = useTrackJob();
  const { data: regs, error: regErr, reload: reloadRegs } = useFetch<Registry[]>('/registries');
  const [regId, setRegId] = useStored<number>('registry', 0);
  const reg = regs?.find((r) => r.id === regId) || regs?.[0];
  const [repoQ, setRepoQ] = useState('');
  const dRepoQ = useDebounced(repoQ, 300);
  const { data: repos, error: repoErr, loading: reposLoading, reload: reloadRepos } = useFetch<RepoRow[]>(reg ? `/registries/${reg.id}/repos${qs({ q: dRepoQ })}` : null);
  const [repo, setRepo] = useState('');
  useEffect(() => {
    if (repos && (!repo || !repos.some((r) => r.name === repo))) setRepo(repos[0]?.name || '');
  }, [repos, repo]);

  const [tagQ, setTagQ] = useState('');
  const [beforeDate, setBeforeDate] = useState('');
  const dTagQ = useDebounced(tagQ, 300);
  const before = beforeDate ? Math.floor(new Date(beforeDate).getTime() / 1000) : undefined;
  const { data: tags, error: tagErr, loading: tagsLoading, reload: reloadTags } = useFetch<TagRow[]>(
    reg && repo ? `/registries/${reg.id}/tags${qs({ repo, q: dTagQ, before })}` : null);
  const [sel, setSel] = useState<Set<string>>(new Set());
  useEffect(() => setSel(new Set()), [repo, reg?.id]);
  const [plan, setPlan] = useState<DeletePlan | null>(null);
  const [gcAfter, setGcAfter] = useState(true);
  const [editing, setEditing] = useState<Registry | 'new' | null>(null);
  const [deploying, setDeploying] = useState(false);

  const rows = tags || [];
  const all = rows.length > 0 && rows.every((t) => sel.has(t.tag));
  const selRows = rows.filter((t) => sel.has(t.tag));
  const selSize = useMemo(() => {
    const seen = new Set<string>();
    return selRows.reduce((n, t) => (seen.has(t.digest) ? n : (seen.add(t.digest), n + t.size)), 0);
  }, [selRows]);
  const shared = rows.filter((t) => t.sharedWith.length > 0 && sel.has(t.tag) && t.sharedWith.some((o) => !sel.has(o)));
  const hostOf = (u: string) => u.replace(/^https?:\/\//, '').split('/')[0];

  const preview = async () => {
    if (!reg) return;
    try {
      setPlan(await post<DeletePlan>(`/registries/${reg.id}/bulk-delete/preview`, { items: selRows.map((t) => ({ repo, tag: t.tag })) }));
    } catch (e: any) {
      toast(e.message, true);
    }
  };

  const gc = async () => {
    if (!reg || !confirm(`Run garbage collection on ${reg.name}? The registry is stopped while it runs (usually a few minutes).`)) return;
    try {
      const r = await post<{ jobId: number }>(`/registries/${reg.id}/gc`);
      track(r.jobId);
      toast('Garbage collection started');
    } catch (e: any) {
      toast(e.message, true);
    }
  };

  if (regs && regs.length === 0) {
    return (
      <>
        <div className="page-head"><h1>Registry</h1></div>
        <ErrorNote error={regErr} />
        <div className="card" style={{ maxWidth: 720 }}>
          <h2>No registry yet</h2>
          <p className="muted" style={{ margin: 0 }}>Deploy a private registry on this NAS (managed by xDocker, port 5050 by default), or connect an existing one.</p>
          {isAdmin && <div className="btn-row">
            <button className="btn primary" onClick={() => setDeploying(true)}>Deploy a registry on this NAS</button>
            <button className="btn" onClick={() => setEditing('new')}>Connect an existing registry</button>
          </div>}
        </div>
        {editing && <RegistryDialog reg={editing === 'new' ? null : editing} onClose={() => setEditing(null)} onSaved={(id) => { setRegId(id); setEditing(null); reloadRegs(); }} />}
        {deploying && <DeployDialog onClose={() => setDeploying(false)} onStarted={(id) => { setDeploying(false); track(id, () => reloadRegs()); }} />}
      </>
    );
  }

  return (
    <>
      <div className="page-head">
        <h1 style={{ flex: 'none' }}>Registry</h1>
        {regs && (
          <select className="select" aria-label="Registry" value={reg?.id} onChange={(e) => setRegId(Number(e.target.value))} style={{ maxWidth: 360 }}>
            {regs.map((g) => <option key={g.id} value={g.id}>{g.name} · {hostOf(g.url)}</option>)}
          </select>
        )}
        <span className="muted" style={{ flex: 1, fontSize: 13 }}>{reg?.managed ? 'Managed by xDocker' : 'External'}{repos ? ` · ${repos.length} repositories` : ''}</span>
        {isAdmin && reg && <>
          <button className="btn" onClick={() => setEditing(reg)}><Icon name="edit" size={16} />Edit</button>
          <button className="btn" onClick={() => setEditing('new')}><Icon name="plus" size={16} />Add registry</button>
        </>}
      </div>
      <ErrorNote error={regErr || repoErr} />
      <div className="split">
        <section className="side card flush" aria-label="Repositories">
          <div style={{ padding: 10, borderBottom: '1px solid var(--border)' }}>
            <label className="search" style={{ height: 36 }}><Icon name="search" size={15} /><input aria-label="Filter repositories" value={repoQ} onChange={(e) => setRepoQ(e.target.value)} placeholder="Filter repositories" /></label>
          </div>
          <div style={{ overflow: 'auto', flex: 1 }}>
            {(repos || []).map((r) => (
              <button key={r.name} className={'list-item' + (r.name === repo ? ' active' : '')} onClick={() => setRepo(r.name)}>
                <span className="name">{r.name}</span><span className="meta">{r.tags} tags</span>
              </button>
            ))}
            {!reposLoading && repos?.length === 0 && <Empty>No repositories.</Empty>}
            {reposLoading && !repos && <Empty>Loading…</Empty>}
          </div>
          {reg && (
            <div className="card" style={{ margin: 10, background: 'var(--bg-sunken)', gap: 8 }}>
              <h3>Garbage collection</h3>
              <div className="muted" style={{ fontSize: 12 }}>Frees disk space after tags are deleted. The registry is stopped while it runs.</div>
              {isAdmin && <button className="btn outline sm" onClick={gc} disabled={!reg.container} title={reg.container ? '' : 'Set the registry container name first'}>Run garbage collection</button>}
              {!reg.container && <div className="muted" style={{ fontSize: 12 }}>Needs the registry's container name (Edit).</div>}
            </div>
          )}
        </section>

        <section className="rest" aria-label={'Tags of ' + repo}>
          {repo && (
            <div className="btn-row">
              <h2 style={{ flex: 1 }}>{repo}</h2>
              {reg && <code className="badge muted" style={{ fontSize: 12 }}>docker pull {hostOf(reg.url)}/{repo}</code>}
              <button className="btn ghost icon" aria-label="Refresh" onClick={() => { reloadRepos(); reloadTags(); }}><Icon name="refresh" size={16} className={tagsLoading ? 'spin' : ''} /></button>
            </div>
          )}
          <div className="btn-row">
            <label className="search"><Icon name="search" size={15} /><input aria-label="Search tags" value={tagQ} onChange={(e) => setTagQ(e.target.value)} placeholder="Search tags — wildcards * or /regex/" /></label>
            <label className="field" style={{ flexDirection: 'row', alignItems: 'center', gap: 8 }}>Created before
              <input type="date" className="input" value={beforeDate} onChange={(e) => setBeforeDate(e.target.value)} />
            </label>
          </div>
          <ErrorNote error={tagErr} />
          {shared.length > 0 && (
            <div className="note warn" role="note"><Icon name="warn" />
              <span>{shared.map((t) => <span key={t.tag}><strong>{t.tag}</strong> shares its digest with <strong>{t.sharedWith.join(', ')}</strong>. </span>)}Deleting removes both.</span>
            </div>
          )}
          <div className="table-wrap" style={{ flex: 1, minHeight: 200 }}>
            <table className="t">
              <thead><tr>
                {isAdmin && <th className="cb"><input type="checkbox" aria-label="Select all shown tags" checked={all} onChange={() => setSel(all ? new Set() : new Set(rows.map((t) => t.tag)))} /></th>}
                <th>Tag</th><th>Digest</th><th className="num">Size</th><th>Created</th><th>Platforms</th>
              </tr></thead>
              <tbody>
                {rows.map((t) => (
                  <tr key={t.tag} className={sel.has(t.tag) ? 'sel' : ''}>
                    {isAdmin && <td className="cb"><input type="checkbox" aria-label={'Select ' + t.tag} checked={sel.has(t.tag)} onChange={() => { const s = new Set(sel); s.has(t.tag) ? s.delete(t.tag) : s.add(t.tag); setSel(s); }} /></td>}
                    <td><span className="mono">{t.tag}</span> {t.sharedWith.length > 0 && <span className="badge warn" title={'Same digest as ' + t.sharedWith.join(', ')}>shared digest</span>} {t.error && <span className="badge danger" title={t.error}>error</span>}</td>
                    <td className="mono" style={{ fontSize: 12 }}>{t.digest ? t.digest.slice(0, 19) : '—'}</td>
                    <td className="num">{bytes(t.size)}</td>
                    <td className="nowrap">{date(t.created)}</td>
                    <td className="sub">{t.platforms?.join(', ') || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!tagsLoading && repo && rows.length === 0 && <Empty>No tags match.</Empty>}
            {tagsLoading && !tags && <Empty>Loading tags…</Empty>}
          </div>
          {isAdmin && sel.size > 0 && (
            <div className="bulkbar">
              <span className="info">{plural(sel.size, 'tag')} selected · {bytes(selSize)}</span>
              <span className="warn" style={{ color: '#c8d1da' }}>Space is freed after garbage collection; shared layers may reduce it.</span>
              <span className="spacer" />
              <button className="btn sm" onClick={() => setSel(new Set())}>Clear</button>
              <button className="btn danger sm" onClick={preview}><Icon name="trash" size={14} />Delete {plural(sel.size, 'tag')}…</button>
            </div>
          )}
        </section>
      </div>
      {plan && reg && (
        <DeleteDialog what="tags" plan={plan} freedNote="before GC (estimate)" onClose={() => setPlan(null)}
          run={async (confirm) => (await post<{ jobId: number }>(`/registries/${reg.id}/bulk-delete`, { items: selRows.map((t) => ({ repo, tag: t.tag })), gcAfter: gcAfter && !!reg.container, confirm })).jobId}
          onFinished={() => { setSel(new Set()); reloadTags(); reloadRepos(); }}
          options={<label className="check"><input type="checkbox" checked={gcAfter && !!reg.container} disabled={!reg.container} onChange={(e) => setGcAfter(e.target.checked)} />Run garbage collection afterwards{!reg.container && ' (needs the container name)'}</label>} />
      )}
      {editing && <RegistryDialog reg={editing === 'new' ? null : editing} onClose={() => setEditing(null)} onSaved={(id) => { setRegId(id); setEditing(null); reloadRegs(); }} onDeleted={() => { setEditing(null); setRegId(0); reloadRegs(); }} onDeploy={() => { setEditing(null); setDeploying(true); }} />}
      {deploying && <DeployDialog onClose={() => setDeploying(false)} onStarted={(id) => { setDeploying(false); track(id, () => reloadRegs()); }} />}
    </>
  );
}

function RegistryDialog({ reg, onClose, onSaved, onDeleted, onDeploy }: { reg: Registry | null; onClose: () => void; onSaved: (id: number) => void; onDeleted?: () => void; onDeploy?: () => void }) {
  const [f, setF] = useState({ name: reg?.name || '', url: reg?.url || 'https://', username: reg?.username || '', password: '', insecure: reg?.insecure || false, container: reg?.container || '' });
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    setErr('');
    try {
      const r = reg ? await put<{ id: number }>(`/registries/${reg.id}`, f) : await post<{ id: number }>('/registries', f);
      onSaved(r.id);
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    if (!reg || !confirm(`Remove ${reg.name} from xDocker? The registry itself and its images are not touched.`)) return;
    try {
      await del(`/registries/${reg.id}`);
      onDeleted?.();
    } catch (e: any) {
      setErr(e.message);
    }
  };
  return (
    <Modal title={reg ? `Edit ${reg.name}` : 'Add a registry'} onClose={onClose}
      footer={<>
        {reg && <button className="btn ghost" style={{ color: 'var(--danger)' }} onClick={remove}>Remove</button>}
        {!reg && onDeploy && <button className="btn ghost" onClick={onDeploy}>Deploy one on this NAS instead</button>}
        <span className="spacer" />
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn primary" disabled={busy} onClick={save}>{busy ? 'Testing…' : 'Test & save'}</button>
      </>}>
      <div className="field-row">
        <label className="field">Name<input className="input" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="nas-registry" /></label>
        <label className="field">URL<input className="input mono" value={f.url} onChange={(e) => setF({ ...f, url: e.target.value })} placeholder="https://registry.nas.lan" /></label>
        <label className="field">Username<input className="input" autoComplete="off" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} /></label>
        <label className="field">Password<input className="input" type="password" autoComplete="new-password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} placeholder={reg?.hasPassword ? '(unchanged)' : ''} /></label>
        <label className="field">Registry container name<input className="input mono" value={f.container} onChange={(e) => setF({ ...f, container: e.target.value })} placeholder="xdocker-registry" /><span className="hint">Needed for garbage collection (the registry must run on this NAS).</span></label>
      </div>
      <label className="check"><input type="checkbox" checked={f.insecure} onChange={(e) => setF({ ...f, insecure: e.target.checked })} />Skip TLS certificate check (self-signed certificate)</label>
      {err && <div className="error-text">{err}</div>}
    </Modal>
  );
}

function DeployDialog({ onClose, onStarted }: { onClose: () => void; onStarted: (jobId: number) => void }) {
  const [f, setF] = useState({ port: 5050, username: 'admin', password: '', image: 'registry:2' });
  const [err, setErr] = useState('');
  const go = async () => {
    setErr('');
    try {
      onStarted((await post<{ jobId: number }>('/registries/managed', f)).jobId);
    } catch (e: any) {
      setErr(e.message);
    }
  };
  return (
    <Modal title="Deploy a private registry on this NAS" onClose={onClose}
      footer={<><span className="spacer" /><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={go}>Deploy</button></>}>
      <p className="muted" style={{ margin: 0, fontSize: 13 }}>Creates the stack <code>xdocker-registry</code> with password authentication and deletes enabled. Do not use port 5000/5001 (DSM). Put it behind the DSM reverse proxy for HTTPS.</p>
      <div className="field-row">
        <label className="field">Port<input className="input mono" inputMode="numeric" value={f.port} onChange={(e) => setF({ ...f, port: Number(e.target.value) || 0 })} /></label>
        <label className="field">Image<input className="input mono" value={f.image} onChange={(e) => setF({ ...f, image: e.target.value })} /></label>
        <label className="field">Username<input className="input" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} /></label>
        <label className="field">Password<input className="input" type="password" autoComplete="new-password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} /><span className="hint">At least 8 characters.</span></label>
      </div>
      {err && <div className="error-text">{err}</div>}
    </Modal>
  );
}
