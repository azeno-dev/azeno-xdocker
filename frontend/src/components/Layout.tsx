import { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { Dashboard } from '../lib/api';
import { useFetch, useStored } from '../lib/hooks';
import { useAuth } from './auth';
import { Icon } from './Icon';

interface NavItem { to: string; label: string; icon: string; count?: number; badge?: string; danger?: boolean; adminOnly?: boolean }

export function Layout() {
  const { user, logout, isAdmin, version } = useAuth();
  const { data: dash } = useFetch<Dashboard>('/dashboard', { poll: 30000 });
  const [open, setOpen] = useState(false);
  const [theme, setTheme] = useStored<'auto' | 'light' | 'dark'>('theme', 'auto');
  const loc = useLocation();
  useEffect(() => setOpen(false), [loc.pathname]);
  useEffect(() => {
    if (theme === 'auto') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.setAttribute('data-theme', theme);
  }, [theme]);

  const running = dash?.recentJobs.filter((j) => j.status === 'running' || j.status === 'queued').length || 0;
  const groups: [string, NavItem[]][] = [
    ['Runtime', [
      { to: '/', label: 'Dashboard', icon: 'dashboard' },
      { to: '/containers', label: 'Containers', icon: 'containers', count: dash?.containers.total },
      { to: '/stacks', label: 'Stacks', icon: 'stacks', count: dash?.stacks },
      { to: '/ports', label: 'Ports', icon: 'ports', badge: dash?.portConflicts ? String(dash.portConflicts) : undefined, danger: true },
    ]],
    ['Images & registry', [
      { to: '/images', label: 'Images', icon: 'images', count: dash?.images.total },
      { to: '/registry', label: 'Registry', icon: 'registry' },
    ]],
    ['Automation', [
      { to: '/policies', label: 'Retention policies', icon: 'retention', count: dash?.policies },
      { to: '/jobs', label: 'Jobs', icon: 'jobs', badge: running ? `${running} running` : undefined },
    ]],
    ['System', [
      { to: '/settings', label: 'Settings', icon: 'settings' },
      { to: '/users', label: 'Users & access', icon: 'users', adminOnly: true },
      { to: '/audit', label: 'Audit log', icon: 'audit' },
    ]],
  ];

  return (
    <div className="shell">
      <nav className={'sidebar' + (open ? ' open' : '')} aria-label="xDocker sections">
        <div className="brand"><span className="brand-mark"><Icon name="cube" size={16} /></span>xDocker</div>
        {groups.map(([g, items]) => (
          <div key={g} style={{ display: 'contents' }}>
            <div className="nav-group">{g}</div>
            {items.filter((i) => !i.adminOnly || isAdmin).map((i) => (
              <NavLink key={i.to} to={i.to} end={i.to === '/'} className={({ isActive }) => 'nav-link' + (isActive ? ' active' : '')}>
                <Icon name={i.icon} />
                {i.label}
                {i.badge ? <span className={'nav-badge' + (i.danger ? ' danger' : '')}>{i.badge}</span> : i.count !== undefined ? <span className="count">{i.count}</span> : null}
              </NavLink>
            ))}
          </div>
        ))}
        <div className="engine" style={{ marginTop: 16 }}>
          <div className="engine-line">
            <span className={'dot ' + (dash ? (dash.docker.connected ? 'ok' : 'bad') : '')} />
            {dash ? (dash.docker.connected ? 'Docker engine connected' : 'Docker not reachable') : 'Checking Docker…'}
          </div>
          <div className="mono muted" style={{ fontSize: 11, wordBreak: 'break-all' }}>
            {dash?.docker.connected ? `Engine ${dash.docker.version} · API ${dash.docker.apiVersion}` : dash?.docker.error || dash?.docker.host}
          </div>
        </div>
      </nav>
      {open && <div className="drawer-overlay" style={{ zIndex: 25 }} onClick={() => setOpen(false)} />}
      <div className="main">
        <header className="topbar">
          <button className="btn ghost icon menu-toggle" aria-label="Open menu" onClick={() => setOpen(true)}><Icon name="menu" /></button>
          <span className="spacer" />
          <label className="sr-only" htmlFor="theme">Theme</label>
          <select id="theme" className="select" style={{ height: 32, fontSize: 13 }} value={theme} onChange={(e) => setTheme(e.target.value as any)}>
            <option value="auto">Theme: auto</option>
            <option value="light">Theme: light</option>
            <option value="dark">Theme: dark</option>
          </select>
          <span className="muted" style={{ fontSize: 13 }}>{user.username} · {user.role}</span>
          <button className="btn ghost sm" onClick={logout} title={`xDocker ${version}`}><Icon name="logout" size={16} />Sign out</button>
        </header>
        <main className="page" id="main">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
