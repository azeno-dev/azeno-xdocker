export function bytes(n: number | undefined | null): string {
  if (!n || n < 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return (i === 0 ? v.toFixed(0) : v >= 100 ? v.toFixed(0) : v.toFixed(1)) + ' ' + units[i];
}

export function date(unixOrIso: number | string | undefined | null): string {
  if (!unixOrIso) return '—';
  const d = typeof unixOrIso === 'number' ? new Date(unixOrIso * 1000) : new Date(unixOrIso);
  if (isNaN(d.getTime()) || d.getFullYear() < 1990) return '—';
  return d.toLocaleDateString(undefined, { year: 'numeric', month: '2-digit', day: '2-digit' });
}

export function dateTime(unixOrIso: number | string | undefined | null): string {
  if (!unixOrIso) return '—';
  const d = typeof unixOrIso === 'number' ? new Date(unixOrIso * 1000) : new Date(unixOrIso);
  if (isNaN(d.getTime()) || d.getFullYear() < 1990) return '—';
  return d.toLocaleString(undefined, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}

export function ago(unix: number | undefined | null): string {
  if (!unix) return '—';
  const s = Math.round(Date.now() / 1000 - unix);
  if (s < 60) return 'just now';
  if (s < 3600) return Math.floor(s / 60) + ' min ago';
  if (s < 86400) return Math.floor(s / 3600) + ' h ago';
  const d = Math.floor(s / 86400);
  if (d < 60) return d + (d === 1 ? ' day ago' : ' days ago');
  return date(unix);
}

export function shortId(id: string) {
  return id.replace(/^sha256:/, '').slice(0, 12);
}

export function plural(n: number, one: string, many = one + 's') {
  return `${n} ${n === 1 ? one : many}`;
}
