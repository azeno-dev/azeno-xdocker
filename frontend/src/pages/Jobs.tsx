import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useAuth } from '../components/auth';
import { Empty, ErrorNote, JobStatusBadge, useToast } from '../components/ui';
import { Job, JobItem, post } from '../lib/api';
import { ago, bytes, dateTime } from '../lib/format';
import { useFetch } from '../lib/hooks';

export function JobsPage() {
  const { data, error, loading } = useFetch<Job[]>('/jobs?limit=200', { poll: 5000 });
  return (
    <>
      <div className="page-head"><h1>Jobs</h1></div>
      <ErrorNote error={error} />
      <div className="table-wrap" style={{ flex: 1 }}>
        <table className="t">
          <thead><tr><th>#</th><th>Job</th><th>Status</th><th>Progress</th><th className="num">Freed</th><th>By</th><th>Started</th></tr></thead>
          <tbody>
            {(data || []).map((j) => (
              <tr key={j.id}>
                <td className="mono">{j.id}</td>
                <td><Link to={`/jobs/${j.id}`}><strong>{j.title}</strong></Link><div className="sub">{j.summary}</div></td>
                <td><JobStatusBadge status={j.status} /></td>
                <td className="nowrap">{j.total ? `${j.done} / ${j.total}` : j.done || '—'}</td>
                <td className="num">{j.bytes ? bytes(j.bytes) : '—'}</td>
                <td>{j.user}</td>
                <td className="nowrap">{ago(j.startedAt || j.createdAt)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {!loading && data?.length === 0 && <Empty>No jobs yet.</Empty>}
      </div>
    </>
  );
}

export function JobPage() {
  const { id } = useParams();
  const { isAdmin } = useAuth();
  const toast = useToast();
  const { data, error } = useFetch<{ job: Job; items: JobItem[] }>(`/jobs/${id}`, { poll: 2000 });
  const [filter, setFilter] = useState<'all' | 'delete' | 'keep' | 'error'>('all');
  const j = data?.job;
  const items = (data?.items || []).filter((i) =>
    filter === 'all' || (filter === 'error' ? i.result === 'error' : filter === 'keep' ? i.action === 'keep' : i.action === 'delete'));
  const running = j && (j.status === 'running' || j.status === 'queued');
  const cancel = async () => {
    try {
      await post(`/jobs/${id}/cancel`);
      toast('Cancel requested');
    } catch (e: any) {
      toast(e.message, true);
    }
  };
  const resultBadge = (r: string) => (r === 'deleted' ? 'danger' : r === 'kept' ? 'ok' : r === 'error' ? 'danger' : r === 'dry-run' ? 'warn' : 'muted');
  return (
    <>
      <div className="page-head">
        <div style={{ flex: 1 }}>
          <div className="crumb"><Link to="/jobs">Jobs</Link> › #{id}</div>
          <h1>{j?.title || 'Job'}</h1>
        </div>
        {j && <JobStatusBadge status={j.status} />}
        {running && isAdmin && <button className="btn" onClick={cancel}>Cancel</button>}
      </div>
      <ErrorNote error={error} />
      {j && (
        <>
          <dl className="kv card" style={{ display: 'grid' }}>
            <dt>Summary</dt><dd>{j.summary || '—'}</dd>
            <dt>Progress</dt><dd>{j.done}{j.total ? ' / ' + j.total : ''}</dd>
            <dt>Space freed</dt><dd>{bytes(j.bytes)}</dd>
            <dt>Started by</dt><dd>{j.user}{j.dryRun && <span className="badge warn" style={{ marginLeft: 8 }}>dry run</span>}</dd>
            <dt>Started</dt><dd>{dateTime(j.startedAt)}</dd>
            <dt>Finished</dt><dd>{dateTime(j.finishedAt)}</dd>
          </dl>
          {running && j.total > 0 && <div className="progress"><div style={{ width: Math.round((j.done / j.total) * 100) + '%' }} /></div>}
          {data!.items.length > 0 && (
            <>
              <div className="seg" role="group" aria-label="Filter items" style={{ alignSelf: 'flex-start' }}>
                {(['all', 'delete', 'keep', 'error'] as const).map((f) => <button key={f} aria-pressed={filter === f} onClick={() => setFilter(f)}>{f[0].toUpperCase() + f.slice(1)}</button>)}
              </div>
              <div className="table-wrap" style={{ maxHeight: 480 }}>
                <table className="t">
                  <thead><tr><th>Target</th><th>Item</th><th>Decision</th><th>Result</th><th className="num">Size</th></tr></thead>
                  <tbody>
                    {items.map((i) => (
                      <tr key={i.id}>
                        <td className="sub">{i.target}</td>
                        <td className="mono" style={{ fontSize: 13 }}>{i.ref}</td>
                        <td>{i.action}<div className="sub">{i.reason}</div></td>
                        <td><span className={'badge ' + resultBadge(i.result)}>{i.result}</span>{i.error && <div className="error-text">{i.error}</div>}</td>
                        <td className="num">{i.bytes ? bytes(i.bytes) : '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
          <h2>Log</h2>
          <div className="logbox" style={{ minHeight: 160, maxHeight: 420 }}>{j.log || 'No log output.'}</div>
        </>
      )}
    </>
  );
}
