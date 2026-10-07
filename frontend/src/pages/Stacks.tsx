import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../components/auth';
import { Icon } from '../components/Icon';
import { useTrackJob } from '../components/JobToast';
import { Empty, ErrorNote, Modal, useToast } from '../components/ui';
import { ApiError, del, post, StackRow } from '../lib/api';
import { ago } from '../lib/format';
import { useFetch } from '../lib/hooks';

export function StacksPage() {
  const { isAdmin } = useAuth();
  const toast = useToast();
  const track = useTrackJob();
  const nav = useNavigate();
  const { data, error, loading, reload } = useFetch<{ stacks: StackRow[]; dir: string }>('/stacks', { poll: 15000 });
  const [deleting, setDeleting] = useState<StackRow | null>(null);
  const [removeFiles, setRemoveFiles] = useState(false);

  const action = async (s: StackRow, a: string, force = false) => {
    try {
      const r = await post<{ jobId: number }>(`/stacks/${s.name}/action`, { action: a, force });
      track(r.jobId, () => reload());
      toast(`${s.name}: ${a} started`);
    } catch (e: any) {
      if (e instanceof ApiError && e.body?.conflicts) {
        if (confirm(`${e.message}\n\nDeploy anyway?`)) action(s, a, true);
      } else toast(e.message, true);
    }
  };

  const remove = async () => {
    if (!deleting) return;
    try {
      await del(`/stacks/${deleting.name}?removeFiles=${removeFiles ? 1 : 0}`);
      toast(`Stack ${deleting.name} removed`);
      setDeleting(null);
      reload();
    } catch (e: any) {
      toast(e.message, true);
    }
  };

  const badge = (s: string) => (s === 'running' ? 'ok' : s === 'partial' ? 'warn' : 'muted');
  return (
    <>
      <div className="page-head">
        <h1>Stacks</h1>
        {isAdmin && <button className="btn primary" onClick={() => nav('/stacks/new')}><Icon name="plus" size={16} />New stack</button>}
      </div>
      <div className="hint-line">Stack folders live in <code>{data?.dir}</code>, the same place Container Manager keeps its projects, so stacks show up in both apps.</div>
      <ErrorNote error={error} />
      <div className="table-wrap">
        <table className="t">
          <thead><tr><th>Name</th><th>Status</th><th>Containers</th><th>Folder</th><th>Changed</th>{isAdmin && <th></th>}</tr></thead>
          <tbody>
            {(data?.stacks || []).map((s) => (
              <tr key={s.name}>
                <td>{s.hasFolder ? <Link to={`/stacks/${s.name}`}><strong>{s.name}</strong></Link> : <strong>{s.name}</strong>}</td>
                <td><span className={'badge ' + badge(s.status)}>{s.status}</span> <span className="sub">{s.running}/{s.total}</span></td>
                <td className="sub" style={{ maxWidth: 300 }}>{s.containers.join(', ') || '—'}</td>
                <td className="mono" style={{ fontSize: 12 }}>{s.hasFolder ? s.dir : <span className="muted" title={s.workingDir}>external{s.workingDir ? ` (${s.workingDir})` : ''}</span>}</td>
                <td className="nowrap">{s.modTime ? ago(s.modTime) : '—'}</td>
                {isAdmin && (
                  <td>
                    <div className="btn-row" style={{ flexWrap: 'nowrap' }}>
                      {s.hasFolder && <button className="btn sm" onClick={() => action(s, 'up')}><Icon name="play" size={14} />Deploy</button>}
                      {s.hasFolder && <button className="btn sm" onClick={() => action(s, 'pull')} title="Pull new images and redeploy">Pull &amp; redeploy</button>}
                      <button className="btn sm" onClick={() => action(s, 'stop')}>Stop</button>
                      <button className="btn ghost icon sm" aria-label={'Delete ' + s.name} onClick={() => { setRemoveFiles(false); setDeleting(s); }}><Icon name="trash" size={14} /></button>
                    </div>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
        {!loading && !data?.stacks.length && <Empty>No stacks yet.</Empty>}
      </div>
      {deleting && (
        <Modal title={`Delete stack ${deleting.name}?`} onClose={() => setDeleting(null)}
          footer={<><span className="spacer" /><button className="btn" onClick={() => setDeleting(null)}>Cancel</button><button className="btn danger" onClick={remove}>Delete</button></>}>
          <p style={{ margin: 0 }}>Runs <code>docker compose down</code>: the stack's containers and networks are removed. Named volumes are kept.</p>
          {deleting.hasFolder && <label className="check"><input type="checkbox" checked={removeFiles} onChange={(e) => setRemoveFiles(e.target.checked)} />Also delete the folder <code>{deleting.dir}</code> (including any data stored inside it)</label>}
        </Modal>
      )}
    </>
  );
}
