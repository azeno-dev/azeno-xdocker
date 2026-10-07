import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../components/auth';
import { Icon } from '../components/Icon';
import { useTrackJob } from '../components/JobToast';
import { Empty, ErrorNote, Switch, useToast } from '../components/ui';
import { Policy, post, put } from '../lib/api';
import { ago, dateTime } from '../lib/format';
import { useFetch } from '../lib/hooks';

export function describePolicy(p: Pick<Policy, 'keepMode' | 'keepPerPeriod' | 'period' | 'keepLastN' | 'minAgeDays' | 'skipInUse' | 'maxAgeDays'>) {
  let s = `Keep the ${p.keepMode} ${p.keepPerPeriod === 1 ? 'image' : p.keepPerPeriod + ' images'} per ${p.period}`;
  if (p.keepLastN > 0) s += `, always keep the last ${p.keepLastN}`;
  s += `, never touch images younger than ${p.minAgeDays} day${p.minAgeDays === 1 ? '' : 's'}`;
  if (p.skipInUse) s += ' or used by a container';
  if (p.maxAgeDays > 0) s += `; delete everything older than ${p.maxAgeDays} days`;
  return s + '.';
}

export function PoliciesPage() {
  const { isAdmin } = useAuth();
  const nav = useNavigate();
  const toast = useToast();
  const track = useTrackJob();
  const { data, error, loading, reload } = useFetch<Policy[]>('/policies', { poll: 30000 });

  const toggle = async (p: Policy, enabled: boolean) => {
    try {
      await put(`/policies/${p.id}`, { ...p, enabled });
      reload();
    } catch (e: any) {
      toast(e.message, true);
    }
  };
  const run = async (p: Policy, dry: boolean) => {
    try {
      const r = await post<{ jobId: number }>(`/policies/${p.id}/run?dry=${dry ? 1 : 0}`);
      track(r.jobId, () => reload());
      toast(`${p.name}: ${dry || p.dryRun ? 'dry run' : 'run'} started`);
    } catch (e: any) {
      toast(e.message, true);
    }
  };

  return (
    <>
      <div className="page-head">
        <h1>Retention policies</h1>
        {isAdmin && <button className="btn primary" onClick={() => nav('/policies/new')}><Icon name="plus" size={16} />New policy</button>}
      </div>
      <div className="hint-line">A policy deletes old images on a schedule, e.g. keep one image per month. New policies start in dry-run mode: they only report what they would delete.</div>
      <ErrorNote error={error} />
      <div className="table-wrap">
        <table className="t">
          <thead><tr><th>Enabled</th><th>Policy</th><th>Applies to</th><th>Schedule</th><th>Next run</th><th>Last run</th>{isAdmin && <th></th>}</tr></thead>
          <tbody>
            {(data || []).map((p) => (
              <tr key={p.id}>
                <td><Switch checked={p.enabled} onChange={(v) => toggle(p, v)} label={'Enable ' + p.name} disabled={!isAdmin} /></td>
                <td>
                  <Link to={`/policies/${p.id}`}><strong>{p.name}</strong></Link> {p.dryRun && <span className="badge warn">dry run</span>}
                  <div className="sub">{describePolicy(p)}</div>
                </td>
                <td><span className="mono" style={{ fontSize: 13 }}>{p.repoPattern || '*'}</span><div className="sub">{p.target === 'both' ? 'local + registry' : p.target}</div></td>
                <td className="mono" style={{ fontSize: 13 }}>{p.cron}<div className="sub">{p.timezone || 'default time zone'}</div></td>
                <td className="nowrap">{p.enabled && p.nextRun ? dateTime(p.nextRun) : <span className="muted">—</span>}</td>
                <td className="nowrap">{p.lastRunAt ? <Link to={`/jobs/${p.lastJobId}`}>{ago(p.lastRunAt)}</Link> : <span className="muted">never</span>}</td>
                {isAdmin && <td><div className="btn-row" style={{ flexWrap: 'nowrap' }}>
                  <button className="btn sm outline" onClick={() => run(p, true)}>Dry run now</button>
                  <button className="btn sm" onClick={() => run(p, false)} title={p.dryRun ? 'The policy is in dry-run mode: this only reports' : 'Deletes images now'}>Run now</button>
                </div></td>}
              </tr>
            ))}
          </tbody>
        </table>
        {!loading && data?.length === 0 && <Empty>No policies yet. Create one to clean up old images automatically.</Empty>}
      </div>
    </>
  );
}
