import { Link } from 'react-router-dom';
import { Icon } from '../components/Icon';
import { ErrorNote, JobStatusBadge } from '../components/ui';
import { Dashboard } from '../lib/api';
import { ago, bytes } from '../lib/format';
import { useFetch } from '../lib/hooks';

export function DashboardPage() {
  const { data: d, error } = useFetch<Dashboard>('/dashboard', { poll: 15000 });
  return (
    <>
      <div className="page-head"><h1>Dashboard</h1></div>
      <ErrorNote error={error} />
      {d && !d.docker.connected && (
        <div className="note danger"><Icon name="warn" />
          <span><strong>Docker is not reachable</strong> at <code>{d.docker.host}</code>: {d.docker.error}. Check that Container Manager is running and that xDocker can access the Docker socket.</span>
        </div>
      )}
      {d && (
        <>
          <div className="grid-4">
            <Link className="stat" to="/containers"><span className="label">Containers</span><span className="value">{d.containers.running}<span className="muted" style={{ fontSize: 16 }}> / {d.containers.total}</span></span><span className="sub">running / total</span></Link>
            <Link className="stat" to="/images"><span className="label">Images</span><span className="value">{d.images.total}</span><span className="sub">{d.images.unused} unused · {d.images.dangling} dangling</span></Link>
            <Link className="stat" to="/stacks"><span className="label">Stacks</span><span className="value">{d.stacks}</span><span className="sub">compose projects</span></Link>
            <Link className="stat" to="/policies"><span className="label">Retention policies</span><span className="value">{d.policies}</span><span className="sub">enabled</span></Link>
          </div>
          {d.portConflicts > 0 && (
            <div className="note danger"><Icon name="warn" /><span>{d.portConflicts} port conflict(s). <Link to="/ports">Open Ports</Link></span></div>
          )}
          <div className="grid-2">
            <section className="card">
              <div className="card-head"><h2>Disk usage</h2><span className="muted" style={{ fontSize: 12 }}>docker system df</span></div>
              <dl className="kv">
                <dt>Images</dt><dd>{bytes(d.disk.images)}</dd>
                <dt>Unused images</dt><dd>{bytes(d.disk.reclaimable)} <Link to="/images?unused=1" style={{ marginLeft: 8 }}>Review</Link></dd>
                <dt>Containers (writable)</dt><dd>{bytes(d.disk.containers)}</dd>
                <dt>Volumes</dt><dd>{bytes(d.disk.volumes)}</dd>
                <dt>Build cache</dt><dd>{bytes(d.disk.buildCache)}</dd>
              </dl>
            </section>
            <section className="card">
              <div className="card-head"><h2>Recent jobs</h2><Link to="/jobs" style={{ fontSize: 13 }}>All jobs</Link></div>
              {d.recentJobs.length === 0 && <div className="muted">No jobs yet.</div>}
              {d.recentJobs.map((j) => (
                <Link key={j.id} to={`/jobs/${j.id}`} style={{ display: 'flex', gap: 10, alignItems: 'center', textDecoration: 'none', color: 'inherit' }}>
                  <JobStatusBadge status={j.status} />
                  <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{j.title}</span>
                  <span className="muted nowrap" style={{ fontSize: 12 }}>{ago(j.createdAt)}</span>
                </Link>
              ))}
            </section>
          </div>
          <div className="muted" style={{ fontSize: 12 }}>
            xDocker {d.version} · Docker {d.docker.version || '—'} · {d.registries} registr{d.registries === 1 ? 'y' : 'ies'} configured
          </div>
        </>
      )}
    </>
  );
}
