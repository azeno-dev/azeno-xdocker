# xDocker

Docker management for Synology DSM 7.2+ (built for an RS3621RPxs on DSM 7.3.2), next to Container Manager:

- **Containers**: list, start/stop/restart/remove in bulk, live logs, stats, an interactive shell, inspect.
- **Images**: search with text, wildcards (`azeno/*:2026.0*`) or `/regex/`; filter unused, dangling or old images; **select all results and delete them in bulk**. A preview shows what is deleted, skipped and freed, and you type the count to confirm.
- **Stacks**: compose projects in the same folder Container Manager uses (`/volume1/docker/<name>`). Edit, validate, deploy, pull & redeploy, stop, delete.
- **Ports**: every published port, conflicts (e.g. a stopped container whose port is held by a running one, or DSM's own ports), and a free-port finder.
- **Private registry**: deploy one on the NAS in one click, or connect an existing one. Browse, search, bulk-delete tags (with a warning when tags share a digest) and run garbage collection.
- **Retention policies**: scheduled cleanup such as *keep the newest image per month, always keep the last 3, never touch images younger than 7 days or in use*. Applies to local images, a registry, or both. A live preview shows exactly what the policy keeps and deletes; dry-run mode, email/webhook notifications.
- Jobs with per-item results, an audit log, admin/viewer accounts.

See [PLAN.md](PLAN.md) for the design.

---

## 1. Install as a container (Container Manager project)

1. In File Station, create `/volume1/docker/xdocker`.
2. Copy [`deploy/compose.yaml`](deploy/compose.yaml) into it as `compose.yaml`. Set `TZ` to your time zone.
3. Container Manager → **Project** → **Create** → path `/volume1/docker/xdocker` → use the existing `compose.yaml`.
4. Open `http://<nas>:8790` and create the admin account.

> The stacks folder is mounted at the **same path** inside the container (`/volume1/docker:/volume1/docker`). Do not change that: `docker compose` resolves relative paths such as `./data` itself, so they must mean the same thing inside xDocker and on the NAS.

The image is `ghcr.io/azeno/xdocker` once a release is published (see §6). Until then, build it on any machine with Docker:

```sh
docker build -f deploy/Dockerfile -t ghcr.io/azeno/xdocker:latest .   # linux/amd64 for the RS3621RPxs
```

## 2. Install as a DSM package (.spk)

```sh
make spk            # → dist/xdocker-0.1.0-0001-x86_64.spk
```

Package Center → **Manual Install** → choose the `.spk`. It depends on Container Manager, adds **xDocker** to the DSM main menu and listens on port 8790.

**Docker access.** DSM 7 runs third-party packages as their own user (`xdocker`), and that user cannot open `/var/run/docker.sock` by default. Until this is set up, xDocker shows "Docker not reachable". Over SSH, once:

```sh
sudo synogroup --add docker xdocker          # create the group with the package user
```

Then in Control Panel → Task Scheduler → Create → Triggered Task → User-defined script, user **root**, event **Boot-up**:

```sh
chown root:docker /var/run/docker.sock
```

Run it once now, then restart the package. DSM recreates the socket when Container Manager restarts, which is why this is a boot-up task. *This is option A from PLAN.md §12.3. It has not been tried on DSM 7.3.2 yet; check with `ls -l /var/run/docker.sock`.*

## 3. HTTPS through the DSM reverse proxy

Control Panel → Login Portal → Advanced → Reverse Proxy → Create:

| Source | Destination |
|---|---|
| `https://xdocker.<your-domain>:443` | `http://localhost:8790` |
| `https://registry.<your-domain>:443` | `http://localhost:5050` (only if you use the managed registry) |

For xDocker: Custom Header → Create → **WebSocket** (needed for the terminal). For the registry, image pushes need large uploads; the DSM proxy allows this by default. Keep 8790 and 5050 closed in the firewall and allow only 443 from the LAN/VPN.

## 4. Typical first steps

1. **Registry** → *Deploy a registry on this NAS* (port 5050, never 5000/5001: those are DSM's).
2. **Images** → search `myapp/*` → *Select all results* → *Delete…* to clean up by hand.
3. **Retention policies** → *New policy*: repositories `myapp/*`, group by **Month**, keep **1**, newest, always keep the last 3, minimum age 7 days, protected tags `latest`. Leave **Dry run** on for a week and read the job reports (Jobs). Then turn dry run off.

## 5. Security

xDocker controls Docker, which is **root-equivalent on the NAS**. Use strong passwords, HTTPS, and LAN/VPN only. Built-in safeguards:
- It never stops or removes its own container or stack.
- It never deletes an image used by a running container.
- Every delete needs the typed count, checked again on the server.
- Every destructive action is written to the audit log.
- Sessions use HttpOnly cookies with a CSRF token. Sign-in is throttled after 5 failures. Registry and SMTP passwords are encrypted at rest.

## 6. Publish (GitHub + SynoCommunity)

- Push to GitHub and publish a release `v0.1.0`. [`.github/workflows/ci.yml`](.github/workflows/ci.yml) then builds and attaches the `.spk` files (x86_64 and armv8) and a source tarball with the UI prebuilt, and pushes the multi-arch image to `ghcr.io/<owner>/xdocker`.
- SynoCommunity: see [`spksrc/README.md`](spksrc/README.md) and PLAN.md §12. Open an issue on `SynoCommunity/spksrc` first.
- Replace the placeholder owner `azeno` (Go module path, image name, homepage) with your GitHub account.

## 7. Development

```sh
make test     # go vet + go test + TypeScript check
make build    # web UI + dist/xdocker
make run      # run locally against Docker Desktop on :8790
cd frontend && npm run dev   # UI with hot reload, proxied to :8790
```

Configuration (environment variables): `XDOCKER_LISTEN` (`:8790`), `XDOCKER_DATA_DIR` (`/data`), `XDOCKER_DOCKER_HOST`, `XDOCKER_DOCKER_BIN`, `XDOCKER_STACKS_DIR` (`/volume1/docker`), `XDOCKER_HOST_PROC`, `XDOCKER_HOST_ADDR`, `TZ`. `XDOCKER_REGISTRY_URL`/`_USER`/`_PASSWORD`/`_CONTAINER` add a registry on first start.

Layout: `backend/` (Go: `internal/retention` is the policy engine, `internal/dockerapi` and `internal/registry` are the API clients, `internal/app` the operations, `internal/api` HTTP), `frontend/` (React + Vite), `deploy/` (Dockerfile, compose), `spk/` (DSM package), `spksrc/` (SynoCommunity port).
