# Deployment

The supported setup is one static binary, one data directory and a reverse proxy for TLS. Files referenced here are in [`deploy/`](../deploy).

## Requirements

- Linux with `git` (2.30+) on the `PATH` of the service.
- A reverse proxy terminating TLS (Caddy or nginx; examples included).
- Optional, for CI: rootless `podman`.
- For backups: `sqlite3` and `rsync`.

## Install

```
make build
sudo install -m755 bin/edugit /usr/local/bin/edugit
sudo useradd --system --home-dir /var/lib/edugit --shell /usr/sbin/nologin edugit
sudo install -d -m750 -o root -g edugit /etc/edugit
sudo install -m640 -g edugit deploy/edugit.env /etc/edugit/edugit.env   # then edit it
sudo install -m644 deploy/edugit.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now edugit
```

The service listens on `127.0.0.1:8080`, keeps all state under `/var/lib/edugit` (`StateDirectory`) and runs with `ProtectSystem=strict`, no capabilities and no new privileges. Put the Caddy or nginx example in front, using the same public hostname as `EDUGIT_PUBLIC_URL`. Register `<public-url>/saml/metadata` and `<public-url>/saml/acs` with the identity provider.

Behind the proxy set `EDUGIT_TRUST_PROXY=true`. The proxy must **overwrite** `X-Forwarded-For` (the nginx example does; Caddy does by default); never enable it on a directly reachable server, or clients can spoof their address and dodge the token-failure throttle. Cookies are marked `Secure` automatically when the public URL is `https://`.

Check it: `curl https://git.example.edu/healthz` answers `ok` and is suitable for uptime monitors. Logs go to the journal (`journalctl -u edugit`); adjust with `EDUGIT_LOG_LEVEL`.

## CI runner isolation

CI is off unless `-ci-runtime podman` is set. Student code then runs in rootless containers with no network, dropped capabilities, a read-only root filesystem and CPU/memory/time limits, never in the server process. Rootless podman needs user namespaces, which the hardened unit forbids, so `deploy/edugit-ci.conf` is a systemd drop-in that relaxes only `RestrictNamespaces` and gives podman a home; follow the one-time `subuid`/`subgid` setup in its header. For stronger isolation run the server and the CI host on different machines in a later iteration; the runner talks to the runtime only through its command line.

## Backups

`deploy/backup.sh` takes a consistent SQLite snapshot (`VACUUM INTO`), rsyncs the repositories and copies the SAML service-provider keypair, then keeps the last 14. Run it from a systemd timer or cron as the `edugit` user, and copy the result off the machine. Losing the keypair means re-registering the service provider with the identity provider, so it is part of the backup.

Restore: stop the service, put `edugit.db`, `repos/` and the keypair from one backup back into `/var/lib/edugit` (delete stale `edugit.db-wal` and `-shm`), fix ownership, start.

## Upgrades

1. Take a backup.
2. Build or download the new binary, `install` it over `/usr/local/bin/edugit` and `systemctl restart edugit`.

Database migrations are forward-only and run automatically at start, each in a transaction. Rolling back to an older binary after a migration is not supported: restore the backup instead. Existing repositories keep their installed git hooks, which call `edugit hook <name>` by absolute path, so the binary must stay at the same path.

## Docker (optional)

```
docker build -t edugit .
docker run -d -p 127.0.0.1:8080:8080 -v edugit-data:/data --env-file edugit.env edugit
```

The image holds the binary and `git`; state is the `/data` volume. CI with podman is not supported inside the container.
