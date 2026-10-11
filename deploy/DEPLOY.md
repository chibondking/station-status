# Deploying to your Oracle VPS (nginx + Cloudflare Tunnel already running)

Assumes: nginx already serves wt2p.us on this box, cloudflared is already
running and tunneling to it, and port 80 is already open locally for your
existing sites. SSL is handled at Cloudflare's edge -- this box never
needs to terminate TLS itself. Port 8000 is internal only: the status
server binds to 127.0.0.1 and is never reached by anything except nginx
on the same machine.

## 1. Get the code onto the VPS

`git clone` your repo, or copy the zip over and unzip it. Land it at
`/opt/station-status`.

## 2. Create a dedicated service user (don't run this as root)

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin stationstatus
sudo chown -R stationstatus:stationstatus /opt/station-status
```

## 3. Set up the Python venv for the server

```bash
cd /opt/station-status/server
sudo -u stationstatus python3 -m venv venv
sudo -u stationstatus ./venv/bin/pip install fastapi uvicorn
```

## 4. Configure secrets

```bash
sudo cp /opt/station-status/deploy/status.env.example /opt/station-status/server/status.env
sudo nano /opt/station-status/server/status.env   # set a real STATUS_API_TOKEN, e.g. `openssl rand -hex 32`
sudo chown stationstatus:stationstatus /opt/station-status/server/status.env
sudo chmod 600 /opt/station-status/server/status.env
```

You'll need this same token in every agent's `config.json` later.

## 5. Install the systemd service

```bash
sudo cp /opt/station-status/deploy/station-status.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now station-status
sudo systemctl status station-status   # should show "active (running)"
```

Confirm it's listening locally (and only locally):
```bash
curl -s http://127.0.0.1:8000/api/status
```
Logs: `sudo journalctl -u station-status -f`

## 6. nginx site

```bash
sudo cp /opt/station-status/deploy/nginx-remote.wt2p.us.conf /etc/nginx/sites-available/remote.wt2p.us
sudo ln -s /etc/nginx/sites-available/remote.wt2p.us /etc/nginx/sites-enabled/
sudo nginx -t   # check syntax before reloading
sudo systemctl reload nginx
```

At this point `curl -H "Host: remote.wt2p.us" http://localhost/` from the
VPS itself should return the page -- nginx is doing host-based routing to
the right backend the same way it already does for your other sites.

## 7. Cloudflare Tunnel — add the public hostname

In the Cloudflare Zero Trust dashboard: Networks -> Tunnels -> select
your existing tunnel -> Public Hostname -> Add a public hostname.

- Subdomain: `remote`
- Domain: `wt2p.us`
- Service Type: HTTP
- URL: whatever your other hostname entries point at for this box --
  almost certainly `localhost:80` or `127.0.0.1:80`, matching however
  wt2p.us itself is configured in the same tunnel. Mirror that exact
  entry rather than guessing; if in doubt, open one of your existing
  Public Hostname entries and copy its Service/URL settings.

No certbot, no cert files, no port 443 anywhere on this box -- Cloudflare
terminates TLS at its edge and the tunnel carries the request in.

## 8. Verify from outside

```bash
curl -s https://remote.wt2p.us/api/status
```
Then open it in a browser. Once an agent (see the agent README section)
starts reporting, the page should update within a few seconds.

`GET /api/health` is the route an external monitor (the ops dashboard)
should poll instead -- `{ status: "ok", stations_reporting, radios_total,
radios_online, offline_threshold_seconds }`. `status` here is close to
"did this request complete at all" (this process has no DB, no socket to
bind, just an in-memory dict), so the genuinely useful signal for a
monitor is the online/offline counts -- every station going quiet is an
agent-side failure, not something a bare "is the process up" check would
ever catch.

## 9. Updating an existing install

The box has a plain git checkout at `/opt/station-status`, owned by the
`stationstatus` service user, and the service runs `server.py` straight
out of it. So an update is a fast-forward and a restart:

```bash
sudo -u stationstatus git -C /opt/station-status fetch --tags --force origin || true
sudo -u stationstatus git -C /opt/station-status merge --ff-only origin/main
sudo systemctl restart station-status
systemctl is-active station-status
curl -s https://remote.wt2p.us/api/version
```

Three things worth knowing:

- **Run git as `stationstatus`, not root.** The checkout is owned by that
  user, and git refuses to operate on a repo owned by someone else
  ("detected dubious ownership"). `sudo -u stationstatus` is right;
  adding a `safe.directory` exception for root is not.
- **`|| true` on the fetch is deliberate.** CI force-pushes the rolling
  `agent-latest` tag on every merge to `main`, so a plain `fetch --tags`
  reports `[rejected] agent-latest (would clobber existing tag)` and exits
  non-zero. With `--force` it's fetched cleanly, and the `|| true` keeps a
  `set -e` script from treating a tag note as a failed deploy.
- **`/api/version` is the check that it took.** It serves the repo's
  `VERSION` plus the commit, so if it still shows the old one, the restart
  didn't pick up the new checkout. `deployedAt` is this process's start
  time, which for this pull-and-restart flow *is* the deploy moment.

Nothing else to do: there's no build step, and the venv only needs
touching if a release adds a Python dependency (none so far -- still just
`fastapi` and `uvicorn`).

**The agent updates separately**, on each shack computer: download the
binary for its OS/arch from the Release and restart it. An agent and
server on different versions keep working -- `/api/report` is additive --
but the status page footer shows the server's version and
`stationagent -version` shows the agent's, so you can tell them apart
when something looks off.

## Note on firewalls

Because this rides the existing cloudflared tunnel, there's nothing new
to open -- no OS firewall change, no OCI Security List change. cloudflared
only makes an outbound connection to Cloudflare; nothing needs to accept
new inbound connections on this box for remote.wt2p.us to work, the same
as your existing sites.

That holds for the "last station worked" feature too (README: *Last
station worked*). Its UDP port -- 14060 by default -- is opened by the
**agent**, on the shack computer, where the logger already lives. The VPS
listens on nothing new: the QSO arrives inside the agent's existing
authenticated HTTPS report. If anything needs allowing, it's the shack
machine's own firewall, and only if the logger runs on a different
computer than the agent.
