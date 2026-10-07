import { ReactNode, useState } from 'react';
import { Link } from 'react-router-dom';
import { DeletePlan, Job } from '../lib/api';
import { bytes, plural } from '../lib/format';
import { useJob } from '../lib/hooks';
import { Icon } from './Icon';
import { Modal } from './ui';

/**
 * The shared bulk delete flow: preview → type the count → background job.
 * `run(confirm)` starts the job and returns its id.
 */
export function DeleteDialog({ what, plan, onClose, run, onFinished, options, freedNote }: {
  what: string; // "images" | "tags"
  plan: DeletePlan;
  onClose: () => void;
  run: (confirm: number) => Promise<number>;
  onFinished: (j: Job) => void;
  options?: ReactNode;
  freedNote?: string;
}) {
  const [typed, setTyped] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [jobId, setJobId] = useState<number | null>(null);
  const job = useJob(jobId, onFinished);
  const n = plan.deleteCount;
  const ok = typed.trim() === String(n) && n > 0;

  const go = async () => {
    if (!ok) return;
    setBusy(true);
    setErr('');
    try {
      setJobId(await run(n));
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  if (jobId) {
    const pct = job && job.total ? Math.round((job.done / job.total) * 100) : 0;
    const finished = job && ['done', 'failed', 'cancelled'].includes(job.status);
    return (
      <Modal title={finished ? 'Delete finished' : `Deleting ${plural(n, what.replace(/s$/, ''), what)}…`} onClose={onClose}
        footer={<><span className="spacer">Logged to the audit log.</span>
          <Link className="btn" to={`/jobs/${jobId}`} onClick={onClose}>Open job</Link>
          <button className="btn primary" onClick={onClose}>{finished ? 'Close' : 'Run in background'}</button></>}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontWeight: 600 }}>
          <span className={'dot ' + (job?.status === 'failed' ? 'bad' : finished ? 'ok' : 'run')} />
          Job #{jobId} · {job?.status ?? 'queued'}
        </div>
        <div className="progress" aria-label="progress"><div style={{ width: pct + '%' }} /></div>
        <div className="muted">{job ? `${job.done} of ${job.total} done · ${bytes(job.bytes)} freed so far` : 'Starting…'}</div>
        {finished && job?.summary && <div className={'note ' + (job.status === 'done' ? 'ok' : 'danger')}>{job.summary}</div>}
      </Modal>
    );
  }

  const skipped = plan.items.filter((i) => i.action === 'skip');
  const also = plan.items.flatMap((i) => i.alsoRemoves || []);
  return (
    <Modal
      title={`Delete ${plural(plan.items.length, 'selected ' + what.replace(/s$/, ''), 'selected ' + what)}?`}
      icon={<span className="icon-circle danger"><Icon name="trash" /></span>}
      onClose={onClose}
      footer={<>
        <span className="spacer">Runs in the background.</span>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn danger" disabled={!ok || busy} onClick={go}>Delete {plural(n, what.replace(/s$/, ''), what)}</button>
      </>}>
      <div className="grid-4" style={{ gridTemplateColumns: 'repeat(3, minmax(0,1fr))' }}>
        <div className="tile del"><div className="big">{n}</div><div className="small">will be deleted</div></div>
        <div className="tile warn"><div className="big">{skipped.length}</div><div className="small">skipped</div></div>
        <div className="tile keep"><div className="big">{bytes(plan.bytes)}</div><div className="small">{freedNote || 'space freed'}</div></div>
      </div>
      {also.length > 0 && (
        <div className="note warn"><Icon name="warn" />
          <span>These tags share a digest with the selection and <strong>will also disappear</strong>: {also.join(', ')}</span>
        </div>
      )}
      <div className="plan-list">
        {plan.items.map((it) => (
          <div className="plan-row" key={it.ref}>
            <span className="mono" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={it.ref}>{it.ref}</span>
            <span className="muted num" style={{ textAlign: 'right' }}>{it.size ? bytes(it.size) : '—'}</span>
            <span>
              {it.action === 'delete'
                ? <span className="badge danger">Delete</span>
                : <span className="badge warn">Skip</span>}
              {it.reason && <span className="muted" style={{ marginLeft: 6, fontSize: 12 }}>{it.reason}</span>}
            </span>
          </div>
        ))}
      </div>
      {options}
      <label className="field">
        <span>Type <code style={{ background: 'var(--bg-soft)', padding: '1px 6px', borderRadius: 4 }}>{n}</code> to confirm</span>
        <input className="input" inputMode="numeric" value={typed} onChange={(e) => setTyped(e.target.value)}
          placeholder={`Number of ${what} to delete`} onKeyDown={(e) => e.key === 'Enter' && go()} disabled={n === 0} />
      </label>
      {n === 0 && <div className="note warn"><Icon name="info" />Nothing can be deleted: every selected item is skipped.</div>}
      {err && <div className="error-text" role="alert">{err}</div>}
    </Modal>
  );
}
