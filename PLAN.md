# xDocker — Docker Management App for Synology DSM

> Design document. Phases 0–6 are implemented (see section 13); the README covers installing and running.

| Item | Value |
|---|---|
| Target device | Synology RS3621RPxs (Intel Xeon D-1531, x86_64, 6C/12T, ECC RAM) |
| Target OS | DSM 7.3.2 |
| Docker engine | Provided by Synology **Container Manager** package (must be installed) |
| Working name | xDocker |
| Document date | 2026-10-07 |

---

## 1. Goals

Build a web app, launched from the DSM desktop like *Container Manager*, that adds the features Container Manager lacks:

1. **Containers**: list, inspect, start/stop/restart/kill, remove, logs, stats, exec shell.
2. **Images**: list, search/filter, pull, tag, push, inspect, **bulk delete**.
3. **Stacks (Compose projects)**: create, edit, deploy, stop, remove, view logs.
4. **Ports**: overview of every published port, conflict detection.
5. **Private registry**: browse repos and tags, search, **bulk delete**, garbage collection.
6. **Retention scheduler**: configurable automatic cleanup that keeps e.g. **one image per month** (local images and registry tags).

### Non-goals (v1)
- Docker Swarm / Kubernetes.
- Multi-host management (single NAS only; designed so it could be added later).
- Replacing Container Manager. Both apps share the same Docker engine and work side by side.

---

## 2. Platform constraints (DSM 7.3)

| Constraint | Impact | Decision |
|---|---|---|
| DSM 7 third-party packages cannot run as root | A native SPK cannot open `/var/run/docker.sock` directly | Run the backend **as a container** with the Docker socket mounted |
| Container Manager owns the Docker daemon | We must not reconfigure the daemon | Use only the Docker Engine API; never edit `dockerd.json` |
| DSM uses ports 5000/5001 for its web UI | The default `registry:2` port 5000 clashes | Run the registry on a different port (e.g. 5050) |
| Container Manager stores projects in `/volume1/docker/<project>` | Stacks created in either app should be visible in both | Use the same Compose project labels (`com.docker.compose.project`) and the same folder layout |
| DSM reverse proxy and certificates | We want HTTPS and an SSO-style login | Expose through the DSM Reverse Proxy (Control Panel → Login Portal → Advanced) |

### Delivery model (three ways to install)

1. **Core app (required):** a Docker container `xdocker` that holds the backend, the frontend, and the scheduler. Installed by hand as a Container Manager project (section 7).
2. **SPK package (phase 6):** a DSM package (`.spk`) that depends on Container Manager, deploys the `xdocker` container for you, and adds a **desktop icon** to the DSM main menu. It can be installed manually (Package Center → Manual Install).
3. **SynoCommunity (phase 8):** the same SPK, built with the SynoCommunity `spksrc` framework and published in the SynoCommunity repository, so it shows up under **Package Center → Community** and gets updates automatically. See section 12.

---

## 3. Architecture

```
┌──────────────────── DSM 7.3.2 (RS3621RPxs) ────────────────────┐
│                                                                │
│  DSM Desktop ──(icon / SPK)──► DSM Reverse Proxy (HTTPS)       │
│                                     │                          │
│                     ┌───────────────▼───────────────┐          │
│                     │ xdocker container             │          │
│                     │  ├─ Web UI (React SPA)        │          │
│                     │  ├─ REST + WebSocket API (Go) │          │
│                     │  ├─ Scheduler (cron engine)   │          │
│                     │  └─ SQLite (/data/xdocker.db) │          │
│                     └───┬───────────────────┬───────┘          │
│        /var/run/docker.sock            HTTP(S) Registry API v2 │
│                         │                   │                  │
│              ┌──────────▼─────┐   ┌─────────▼────────┐         │
│              │ Docker Engine  │   │ registry:2       │         │
│              │ (Container Mgr)│   │ container :5050  │         │
│              └────────────────┘   └──────────────────┘         │
└────────────────────────────────────────────────────────────────┘
```

### Tech stack (recommended)

| Layer | Choice | Reason |
|---|---|---|
| Backend | **Go** + a small own Engine API client (`internal/dockerapi`) | One static binary, small image, low RAM; the official SDK was dropped to keep the SPK small |
| Compose | `docker compose` CLI v2 inside the image (or `compose-go` library) | Same behaviour as Container Manager projects |
| Registry client | Own v2 client (`internal/registry`) | Handles Docker v2 and OCI manifests/indexes, Basic and Bearer auth |
| Scheduler | `github.com/robfig/cron/v3` | Cron expressions, time zones |
| Database | SQLite (`modernc.org/sqlite`, no CGO) | Settings, policies, job history, audit log |
| Frontend | React + Vite + TypeScript, plain tables, textarea YAML editor (validated by `docker compose config`) | Fewer dependencies; Monaco can be added later |
| Realtime | WebSocket (exec terminal, xterm.js), server-sent events (log follow), polling (stats, job progress) | Simplest transport for each case |
| Image | Multi-stage build → `alpine` or `distroless`, `linux/amd64` | Target CPU is x86_64 |

---

## 4. Functional specification

### 4.1 Containers
- Table: name, image, status, health, uptime, CPU/RAM, ports, stack.
- Actions (single and bulk): start, stop, restart, pause, kill, remove (optionally with volumes).
- Details: inspect JSON, environment, mounts, networks, labels.
- Live logs (follow, tail N, timestamps, search, download).
- Live stats chart.
- Exec terminal (`/bin/sh` or `/bin/bash`) via xterm.js.
- Recreate with new image (pull and redeploy).

### 4.2 Images (including bulk search and delete)
- Table columns: repository, tag, image ID, digest, size, created date, **in use by** (containers), dangling flag.
- **Search and filter bar:**
  - Free text and **wildcard/regex** on `repository:tag` (e.g. `myapp/*:2024-*`, `^registry.local:5050/.*`).
  - Filters: created before/after, size larger than, dangling only, unused only, repository group.
- **Bulk selection:** select all matching, select page, invert selection.
- **Bulk delete flow:**
  1. The user selects images, or clicks *Select all results*.
  2. **Preview dialog:** count, total size reclaimed, and a list. Images used by containers are flagged and skipped unless *force* is ticked.
  3. Confirm by typing the count (protects against accidents).
  4. Runs as a background job with a progress bar and a per-image result (deleted, skipped, error).
- Other actions: pull (with registry credentials), tag, push to the private registry, export (`docker save`), prune dangling images.

### 4.3 Stacks (Compose)
- List of Compose projects found through container labels and in the stacks folder.
- Create or edit `compose.yaml` and `.env` in a YAML editor with validation (`docker compose config`).
- Actions: deploy (`up -d`), pull and redeploy, stop, down, down with volumes, logs.
- Storage path is configurable, default `/volume1/docker/<stack>` (mounted into xdocker), so stacks stay compatible with Container Manager projects.
- Optional: import from a Git repo URL (phase 7).

### 4.4 Ports
- One table of every published port: host IP, host port, container port, protocol, container, stack.
- **Conflict checker:** warns before deploying a stack or container if a host port is already used by:
  - another **running** container (a stopped container does not hold a port), or
  - a DSM service (reserved list, e.g. 80, 443, 5000, 5001, 22, 139, 445, 873, 6690; editable in settings), or
  - a listening socket on the host (checked from inside the container with `network_mode: host` access to `/proc/net/tcp*`, or with a test bind).
- "Find free port" helper.
- Port table status: **conflict** = a stopped container whose port is held by a running one (it would fail to start), or a container on a DSM-reserved port; **shared** (warning) = containers that are not running claim the same port, which is normal when old and new versions of a project exist.

### 4.5 Private registry
- **Registry management:**
  - Option A: xdocker deploys and manages its own `registry:2` stack (port 5050, data at `/volume1/docker/registry/data`, `REGISTRY_STORAGE_DELETE_ENABLED=true`, htpasswd auth, TLS through the DSM reverse proxy).
  - Option B: connect to an existing registry (URL plus credentials). Several registries can be configured.
- **Browse:** repositories (`/v2/_catalog` with pagination), tags (`/v2/<name>/tags/list`), per tag: digest, size, created date (read from the image config blob), platforms (OCI index).
- **Search:** same wildcard/regex filter as for local images, plus a date range.
- **Bulk delete tags:**
  - Resolve tag → digest (`HEAD` manifest with the correct `Accept` headers for Docker v2 and OCI).
  - `DELETE /v2/<name>/manifests/<digest>`.
  - Warn when several tags share one digest (deleting the digest removes all of them).
  - Preview, confirmation, and background job, as for images.
- **Garbage collection** (frees disk space after deletion):
  - Switch the registry to read-only mode (or stop it), run `registry garbage-collect --delete-untagged /etc/docker/registry/config.yml` in the registry container, then restart it.
  - Can be run manually or scheduled after retention jobs.
  - Shows disk usage before and after.

### 4.6 Retention scheduler (key feature)

A **policy** says which images to keep and when cleanup runs. Policies apply to **local images**, **registry tags**, or both.

#### Policy settings (all configurable in the UI)

| Field | Example | Description |
|---|---|---|
| Name | `myapp monthly` | Label |
| Enabled | yes/no | |
| Target | `local` / `registry:<name>` / both | Where it applies |
| Repository match | `myapp/*`, regex | Which repositories are in scope |
| Tag include / exclude | exclude `latest`, `^v\d+\.\d+$` | Protected tags are never deleted |
| **Grouping period** | `month` (also `day`, `week`, `year`) | Bucket used for "keep one per …" |
| **Keep per period** | `1` | How many images to keep in each bucket |
| **Which to keep** | `newest` / `oldest` in bucket | E.g. the last build of each month |
| Keep last N (always) | `5` | The N most recent images are always kept, whatever the bucket |
| Minimum age | `7 days` | Never delete anything younger than this |
| Max age | `24 months` (optional) | Delete everything older, even monthly keepers |
| Date source | image `created` / registry push time / date parsed from tag (regex) | How the date is found |
| Skip in-use images | yes (default) | Never remove images used by containers |
| Schedule | cron, e.g. `0 3 * * 0` (Sun 03:00) | When it runs |
| Time zone | `Europe/Bucharest` | For bucket boundaries and cron |
| Dry run | yes/no | Only report, do not delete |
| Run GC after | yes/no | Registry garbage collection after deletion |
| Notify | email / webhook / DSM notification | Summary after each run |

#### Algorithm (per repository)
1. Collect candidates (local images or registry tags) that match the repository and tag filters.
2. Remove protected items (excluded tags, in use, younger than minimum age).
3. Sort by date (newest first). Mark the first *Keep last N* as **keep**.
4. Group the rest by period bucket (e.g. `2026-09`). In each bucket, mark *Keep per period* items as **keep** (newest or oldest).
5. Apply *Max age*: keepers older than the limit become **delete**.
6. Everything not marked keep becomes **delete**.
7. With dry run: save the plan only. Otherwise delete, record results, optionally run GC, and send a notification.

#### Example
Policy: `myapp/*`, keep 1 per month (newest), keep last 3, exclude `latest`.
Images: 2026-10-05, 10-03, 10-01, 09-28, 09-15, 09-02, 08-30, 08-10
→ Keep: 10-05, 10-03, 10-01 (last 3), 09-28 (September), 08-30 (August)
→ Delete: 09-15, 09-02, 08-10

#### UI
- Policy list with next run time, last result, enabled toggle.
- Policy editor with a **live preview**: "this policy would delete 42 images / 18.3 GB", shown as a keep/delete list.
- *Run now* and *Dry run now* buttons.
- Job history: start/end time, deleted/kept/skipped/errors, space reclaimed, full log.

### 4.7 Cross-cutting
- **Authentication:** local admin account (bcrypt) with optional **OIDC against Synology SSO Server**. Session cookie, CSRF protection.
- **Roles (v1):** `admin` (everything) and `viewer` (read-only).
- **Audit log:** every destructive action, with who, what, when, and the result.
- **Settings:** stacks path, reserved ports, registries, notifications (SMTP / webhook), time zone, theme.
- **Dashboard:** counts of containers, images, stacks; disk usage (`docker system df`); registry size; recent jobs.

---

## 5. Data model (SQLite)

| Table | Key fields |
|---|---|
| `users` | id, username, password_hash, role, oidc_sub |
| `registries` | id, name, url, username, password_enc, insecure, managed (bool) |
| `policies` | id, name, enabled, target, repo_pattern, tag_include, tag_exclude, period, keep_per_period, keep_mode, keep_last_n, min_age, max_age, date_source, date_regex, skip_in_use, cron, timezone, dry_run, gc_after, notify |
| `jobs` | id, type (bulk_delete/retention/gc/deploy), policy_id, status, started_at, finished_at, summary_json |
| `job_items` | job_id, target, ref (image/tag), digest, action (keep/delete/skip), reason, result, bytes |
| `audit_log` | ts, user, action, object, details_json, result |
| `settings` | key, value |

Secrets (registry passwords, SMTP) are encrypted with a key held in `/data/secret.key`.

---

## 6. API outline (REST, `/api/v1`)

```
GET    /containers                      POST /containers/{id}/{start|stop|restart|kill}
DELETE /containers/{id}                 POST /containers/bulk   {ids, action}
GET    /containers/{id}/logs (ws)       GET  /containers/{id}/exec (ws)

GET    /images?q=&regex=&before=&unused=   POST /images/pull
POST   /images/bulk-delete/preview         POST /images/bulk-delete  -> job id
POST   /images/prune

GET    /stacks                          POST /stacks          PUT /stacks/{name}
POST   /stacks/{name}/{up|down|pull|stop}   DELETE /stacks/{name}

GET    /ports                           POST /ports/check     GET /ports/free

GET    /registries                      POST /registries
GET    /registries/{id}/repos?q=        GET  /registries/{id}/repos/{name}/tags
POST   /registries/{id}/bulk-delete/preview   POST /registries/{id}/bulk-delete
POST   /registries/{id}/gc

GET    /policies   POST /policies   PUT /policies/{id}   DELETE /policies/{id}
POST   /policies/{id}/preview           POST /policies/{id}/run?dry=true

GET    /jobs   GET /jobs/{id}   GET /jobs/{id}/events (ws)
GET    /audit  GET/PUT /settings
```

---

## 7. Deployment on the RS3621RPxs

### 7.1 Prerequisites
- DSM 7.3.2 with **Container Manager** installed from Package Center.
- A shared folder `docker` on `/volume1`.
- Optional: a DSM certificate for the hostname (e.g. `xdocker.nas.lan`, `registry.nas.lan`).

### 7.2 Compose (deployed as a Container Manager project)

```yaml
services:
  xdocker:
    image: xdocker:latest
    container_name: xdocker
    restart: unless-stopped
    ports:
      - "8790:8080"
    environment:
      - TZ=Europe/Bucharest
      - XDOCKER_STACKS_DIR=/volume1/docker
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /volume1/docker/xdocker/data:/data
      - /volume1/docker:/volume1/docker   # SAME path: compose resolves ./relative mounts client-side
      - /proc:/host/proc:ro               # host port check
```

The full file is `deploy/compose.yaml`. **Correction to the first draft:** the stacks folder was mounted at `/stacks`, which breaks every stack with a relative bind mount (`./data` would resolve to `/stacks/<name>/data` on the host). It must be mounted at the same path. This was verified on a real engine.

The registry is **not** bundled in this file any more. xDocker deploys it as its own stack (`xdocker-registry`) from Registry → *Deploy a registry on this NAS*: it writes the htpasswd file first, then starts `registry:2` with deletes enabled.

### 7.3 DSM configuration
- Reverse proxy: `https://xdocker.<domain>` → `http://localhost:8790` (enable WebSocket headers).
- Reverse proxy: `https://registry.<domain>` → `http://localhost:5050` (set a large body size for image pushes).
- Firewall: allow 443 only from the LAN or VPN; do not expose 8790 or 5050 directly.
- Hyper Backup: back up `/volume1/docker/xdocker/data` (policies and history). Registry data is optional.

### 7.4 Security notes
- Mounting `docker.sock` gives the container **root-equivalent** control of the NAS. Mitigations: strong auth, HTTPS only, LAN/VPN only, a read-only root filesystem for the xdocker container, no other services inside it. A socket proxy (e.g. `tecnativa/docker-socket-proxy`) can be considered, but exec/delete need wide permissions anyway.
- xdocker must never delete its own image, the registry image, or images of running containers (hard-coded protection).

---

## 8. Project structure (planned)

```
azeno-xdocker/
├── PLAN.md
├── backend/
│   ├── cmd/xdocker/main.go
│   ├── internal/
│   │   ├── api/          # HTTP handlers, ws
│   │   ├── auth/
│   │   ├── docker/       # containers, images, ports
│   │   ├── compose/      # stacks
│   │   ├── registry/     # v2 client, bulk delete, gc
│   │   ├── retention/    # policy engine (pure, unit-tested)
│   │   ├── scheduler/
│   │   ├── jobs/
│   │   └── store/        # sqlite, migrations
│   └── go.mod
├── frontend/
│   ├── src/pages/{Dashboard,Containers,Images,Stacks,Ports,Registry,Policies,Jobs,Settings}
│   └── vite.config.ts
├── deploy/
│   ├── Dockerfile
│   └── compose.yaml
└── spk/                  # DSM package for manual install; ported to SynoCommunity spksrc later (section 12)
    ├── INFO
    ├── conf/{privilege,resource}
    ├── ui/{config,images}
    └── scripts/
```

---

## 9. Roadmap / milestones

| Phase | Scope | Done when |
|---|---|---|
| **0. Spike** | Go app in a container on the NAS, reads `docker.sock`, lists containers and images; check the Docker API version used by Container Manager on DSM 7.3.2 | It runs on the RS3621RPxs |
| **1. Foundation** | Auth, SQLite, job runner, audit log, UI shell, dashboard | Login works, dashboard shows counts |
| **2. Containers and images** | Container actions, logs, stats, exec; image list, **search, bulk delete with preview** | 100+ images bulk-deleted with correct skip handling |
| **3. Stacks and ports** | Compose CRUD and deploy, port table, conflict checker | A stack deployed from xdocker shows up in Container Manager |
| **4. Private registry** | Managed `registry:2`, browse, search, **bulk tag delete**, GC | Disk space goes down after delete and GC |
| **5. Retention scheduler** | Policy engine, editor with live preview, cron, dry run, history, notifications | The "1 per month" policy runs on schedule and the result matches the preview |
| **6. DSM integration** | SPK with desktop icon, reverse proxy guide, SSO (OIDC) | Opens from the DSM main menu |
| **7. Nice to have** | Git-based stacks, image update checker, multiple hosts, Prometheus metrics | — |
| **8. SynoCommunity** | Port the SPK to `spksrc`, own test repo, pull request to SynoCommunity (section 12) | xDocker installs from Package Center → Community on the RS3621RPxs |

---

## 10. Testing strategy
- **Retention engine:** pure-function unit tests with table-driven cases (bucket edges, time zones, DST, `keep_last_n` overlaps, shared digests, protected tags).
- **Registry:** integration tests against a throwaway `registry:2` container (push, delete, GC, check the catalog).
- **Docker:** integration tests with Docker-in-Docker in CI.
- **UI:** Playwright end-to-end tests for the bulk delete and policy editor flows.
- **On NAS:** staging run with **dry run on** for at least one week before deletion is enabled.

---

## 11. Risks and open questions

| # | Item | Notes |
|---|---|---|
| 1 | Docker engine version in Container Manager on DSM 7.3.2 | Handled: the client negotiates the API version (daemon version, capped at 1.47, never below the daemon's minimum). Tested against Docker 29 (API 1.56, minimum 1.44); DSM's Docker 24 uses 1.43 |
| 2 | Compose v2 availability | Bundle the `docker compose` plugin in the xdocker image instead of relying on DSM's copy |
| 3 | Registry created date | `registry:2` has no push timestamp; use the image config `created` field, or parse the date from the tag |
| 4 | Multi-arch / OCI index deletion | Delete the index digest. GC runs **without** `--delete-untagged` by default, because with `registry:2` that flag can remove platform manifests of multi-arch images; it can be turned on in Settings |
| 5 | GC downtime | The registry is read-only for the GC duration. Schedule at night |
| 6 | Shared digests between tags | Deleting one tag's digest removes all its tags. The preview must show this |
| 7 | DSM updates | A DSM or Container Manager update may restart Docker. xdocker uses `restart: unless-stopped` and resumes missed jobs on start |
| 8 | Open: "one per month" keep newest or oldest? | Configurable; default **newest** |
| 9 | Open: should retention also cover dangling/untagged images? | Implemented as a separate *Prune dangling* button on Images; not yet a scheduled policy |
| 10 | SynoCommunity acceptance | Maintainers decide. A package that only wraps a Docker image may be refused; a native Go binary is more likely to be accepted. Ask in a GitHub issue before doing the work (section 12.6) |
| 11 | Docker socket access from a non-root package | Must be proven in phase 0 (section 12.3) |

---

## 12. Distribution through SynoCommunity

### 12.1 How SynoCommunity works

The SynoCommunity source (`https://packages.synocommunity.com`) that is already added in your Package Center is **read-only for users**. You cannot upload a package to it yourself. The flow is:

1. All SynoCommunity packages are built from one GitHub repo: **`github.com/SynoCommunity/spksrc`** (a cross-compile framework based on Makefiles).
2. You add your package to that repo (`spk/xdocker/` and, if needed, `cross/xdocker/`) and open a **pull request**.
3. SynoCommunity maintainers review it. GitHub Actions build it for each architecture and DSM version.
4. After the merge, a maintainer **publishes** the build to the repository.
5. Every NAS that has the SynoCommunity source added sees **xDocker** under **Package Center → Community**, including your RS3621RPxs. Updates arrive the same way.

### 12.2 Package design for spksrc

| Item | Plan |
|---|---|
| Package name | `xdocker` (check it is not already taken) |
| Architectures | `x64` first (the RS3621RPxs is a Xeon D-1531, i.e. `broadwellnk`), `aarch64` later. Go cross-compiles easily for both |
| DSM version | `REQUIRED_MIN_DSM = 7.2` (Container Manager exists only from 7.2) |
| Dependency | `install_dep_packages = "ContainerManager"` in `INFO`, so Package Center installs Container Manager first |
| Runs as | The package user `sc-xdocker` (DSM 7 does not allow root for third-party packages) |
| Contents | Native Go binary (backend, scheduler, embedded web UI). **No** Docker image needed for xDocker itself |
| Data | `/var/packages/xdocker/var/` (SQLite, keys) |
| Web UI | DSM desktop icon (`app/config`) that opens the app through a DSM reverse proxy path or its own port (`SERVICE_PORT`, e.g. 8790) |
| Firewall | Port 8790 registered via `SERVICE_PORT` so it appears in DSM's firewall rules |
| Install wizard | Asks for: stacks folder, registry port, whether to deploy the managed registry |

So xDocker becomes a **native DSM package** that talks to Docker, instead of a container itself. This is also more likely to be accepted by SynoCommunity than a package that only pulls a Docker image.

### 12.3 The hard part: Docker socket access without root

On DSM 7, `/var/run/docker.sock` belongs to root. A non-root package user cannot open it by default. Options, to be tested in **phase 0** on the RS3621RPxs:

| Option | How | Trade-off |
|---|---|---|
| **A. Docker group** | Check the socket's owner group on DSM 7.3.2 (`ls -l /var/run/docker.sock`). If DSM uses a group (e.g. `docker` or `administrators`), the user adds `sc-xdocker` to it once (documented in the install wizard) | Simple; needs one manual step by the user |
| **B. Helper container** | The package uses the DSM 7.2+ `docker-project` resource worker (in `conf/resource`) to let Container Manager start a small socket proxy container that exposes a limited Docker API on a local port or socket that `sc-xdocker` can read | No manual step; but it is a container again, and the worker must be confirmed in the DSM 7.2/7.3 developer guide |
| **C. Wrapper only** | The SPK deploys the existing `xdocker` container through the `docker-project` worker and only adds the desktop icon | Easiest; lowest chance of SynoCommunity accepting it |

**Recommendation:** try A first, keep B as the fallback. Use C only for your own repository (12.5), not for SynoCommunity.

### 12.4 Planned spksrc layout

```
spksrc/
├── cross/xdocker/          # builds the Go binary from a release tarball (tag + checksum)
│   ├── Makefile
│   └── digests
└── spk/xdocker/
    ├── Makefile            # SPK_NAME, SPK_VERS, SPK_REV, DEPENDS, SERVICE_*, REQUIRED_MIN_DSM
    ├── src/
    │   ├── service-setup.sh        # start/stop, data dir setup, socket check
    │   ├── xdocker.png             # icon
    │   ├── app/config              # DSM desktop shortcut
    │   ├── conf/resource           # port-config, (docker-project if option B)
    │   └── wizard/install_uifile   # install wizard
    └── PLIST                       # files to package
```

Source code stays in **our own GitHub repo** with tagged releases. spksrc downloads the release tarball, checks it against `digests`, and builds it.

### 12.5 Steps to get there

1. **Phase 0:** test socket access on DSM 7.3.2 (12.3) and pick option A or B.
2. Build the SPK in a fork of `spksrc` using its Docker build environment (`ghcr.io/synocommunity/spksrc`): `make arch-x64-7.2` in `spk/xdocker`.
3. **Manual install** of the `.spk` on the RS3621RPxs (Package Center → Manual Install). Test install, upgrade, uninstall and the "keep data" option.
4. **Optional: own package source.** Host the `.spk` files plus a package index (e.g. with SSPKS, the open-source package server, or GitHub Pages). Add it under Package Center → Settings → Package Sources, next to SynoCommunity. This gives automatic updates before (or without) SynoCommunity.
5. Open a GitHub **issue** on `SynoCommunity/spksrc` describing xDocker and the socket approach, and ask whether they would accept it.
6. Open the **pull request** following their contribution guide (code style, `PLIST`, changelog, licence, maintainer info).
7. Fix review comments. After the merge a maintainer publishes it, and xDocker appears under **Community** in Package Center.
8. For each new version: tag a release in our repo, then open a pull request to spksrc that bumps the version and digests.

### 12.6 Requirements and risks
- **Licence:** the code must be open source (e.g. MIT or Apache-2.0) with a public repository.
- **No guaranteed timeline:** review and publishing depend on volunteer maintainers.
- **Security review:** a package that controls Docker is root-equivalent on the NAS. Expect questions; document the socket approach and the mitigations from section 7.4.
- **Updates:** SynoCommunity releases are slower than our own. Running our own package source (12.5 step 4) covers urgent fixes.

---

## 13. Implementation status (2026-10-07)

| Phase | Status |
|---|---|
| 0 Spike | Done. Runs against a real engine; API negotiation for Docker 24 and 29+ |
| 1 Foundation | Done: auth (bcrypt, sessions, CSRF, login throttling), SQLite + migrations, job runner, audit log, dashboard |
| 2 Containers & images | Done: bulk actions, logs (follow), stats, exec terminal, inspect; image search (text/wildcard/regex) + filters, select all results, bulk delete with preview, server-checked typed confirmation, pull/tag/push/prune |
| 3 Stacks & ports | Done: compose editor with validation and rollback, deploy/pull/stop/delete, pre-deploy port check, port table, free-port finder, host listeners via `/proc` |
| 4 Private registry | Done: managed `registry:2` deploy, external registries, browse/search, shared-digest warnings, bulk tag delete, garbage collection (stop → GC in a temporary container → start, size before/after) |
| 5 Retention scheduler | Done: policy engine (unit-tested: month/week/year buckets, time zones, ISO week across New Year, keep-last overlap, protected tags, in-use, min/max age, date from tag), live preview, cron per time zone, dry run, run now, history, email/webhook |
| 6 DSM integration | Done: SPK (desktop icon, port, `docker` share, Container Manager dependency, install wizard), reverse proxy guide. **Not done:** Synology SSO (OIDC) login |
| 7 Nice to have | Not started |
| 8 SynoCommunity | Files prepared in `spksrc/` (not yet built inside spksrc); CI release workflow produces the source tarball |

**Verified live** (Docker Desktop, Engine 29.8.0): image listing and bulk delete via the UI; registry deploy, tag + push with stored credentials, browse, tag delete, garbage collection (8.4 MB → 5.7 MB); retention preview and a real run deleting exactly the planned tags; the cron scheduler firing on time; stack deploy with relative bind mounts from inside the xDocker container; self-protection; exec terminal, log streaming, stats.

**Not verified yet, needs the RS3621RPxs:**
- Docker socket permissions and the `docker` group workaround for the native SPK (§12.3).
- The SPK installer itself: wizard, `data-share` resource and desktop icon on DSM 7.3.2.
- `extra_hosts: host-gateway` on DSM's Docker 24.
