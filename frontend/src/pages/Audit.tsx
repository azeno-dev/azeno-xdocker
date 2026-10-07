import { useState } from 'react';
import { Icon } from '../components/Icon';
import { Empty, ErrorNote } from '../components/ui';
import { AuditEntry, qs } from '../lib/api';
import { dateTime } from '../lib/format';
import { useDebounced, useFetch } from '../lib/hooks';

export function AuditPage() {
  const [q, setQ] = useState('');
  const dq = useDebounced(q, 300);
  const { data, error, loading } = useFetch<AuditEntry[]>('/audit' + qs({ q: dq }));
  return (
    <>
      <div className="page-head"><h1>Audit log</h1></div>
      <div className="btn-row">
        <label className="search"><Icon name="search" size={16} /><input aria-label="Search audit log" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter by user, action or object" style={{ fontFamily: 'inherit' }} /></label>
      </div>
      <ErrorNote error={error} />
      <div className="table-wrap" style={{ flex: 1 }}>
        <table className="t">
          <thead><tr><th>When</th><th>User</th><th>Action</th><th>Object</th><th>Result</th></tr></thead>
          <tbody>
            {(data || []).map((e) => (
              <tr key={e.id}>
                <td className="nowrap">{dateTime(e.ts)}</td>
                <td>{e.user}</td>
                <td className="mono" style={{ fontSize: 13 }}>{e.action}</td>
                <td>{e.object}{e.details && <div className="sub mono" style={{ wordBreak: 'break-all' }}>{e.details}</div>}</td>
                <td>{e.result === 'ok' ? <span className="badge ok">ok</span> : <span className="badge danger" title={e.result} style={{ whiteSpace: 'normal' }}>{e.result}</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {!loading && data?.length === 0 && <Empty>No entries.</Empty>}
      </div>
    </>
  );
}
