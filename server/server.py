"""
Central status server for the station status page.

Responsibilities:
  - Accept POST /api/report from agents running on shack computers (auth via
    shared bearer token).
  - Serve GET /api/status with the current, computed view (including
    online/offline determination) for the frontend to poll.
  - Serve the static frontend page.

Run:
  pip install fastapi uvicorn
  API_TOKEN=changeme python3 server.py
"""

import os
import json
import time
import datetime
import subprocess
import threading
from typing import Dict, Any, List, Optional

from fastapi import FastAPI, Header, HTTPException, Request
from fastapi.responses import JSONResponse
from fastapi.staticfiles import StaticFiles

# ---- Config (env vars, with defaults for local testing) -------------------

API_TOKEN = os.environ.get("STATUS_API_TOKEN", "changeme")
# How long (seconds) since an agent's last report before we call it Offline.
# This is independent of how often frequency actually changes -- agents send
# periodic heartbeats even when nothing has changed, specifically so this
# threshold has something fresh to check against.
OFFLINE_THRESHOLD_SECONDS = float(os.environ.get("STATUS_OFFLINE_THRESHOLD", "20"))
HOST = os.environ.get("STATUS_HOST", "0.0.0.0")
PORT = int(os.environ.get("STATUS_PORT", "8000"))

app = FastAPI()

# ---- In-memory state --------------------------------------------------
# station_id -> {
#   "name": str,
#   "last_report_at": float (epoch seconds),
#   "radios": {
#       radio_id: {
#           "label": str,
#           "freq_hz": Optional[int],
#           "band": Optional[str],   # set instead of freq_hz when the agent
#                                    # is in contest_mode -- the exact freq
#                                    # never leaves the shack LAN, only the
#                                    # band name ("40M")
#           "mode": Optional[str],
#           "operator": Optional[str],
#           "connected": bool,   # was the agent's data source (TCI/N1MM) connected
#           "source": str,       # "tci" | "n1mm" | etc, informational
#       }
#   }
# }
_state: Dict[str, Dict[str, Any]] = {}
_lock = threading.Lock()


def _check_auth(authorization: Optional[str]):
    expected = f"Bearer {API_TOKEN}"
    if authorization != expected:
        raise HTTPException(status_code=401, detail="bad or missing token")


@app.post("/api/report")
async def report(request: Request, authorization: Optional[str] = Header(None)):
    _check_auth(authorization)
    payload = await request.json()

    station_id = payload.get("station_id")
    if not station_id:
        raise HTTPException(status_code=400, detail="station_id required")

    station_name = payload.get("station_name", station_id)
    radios_in = payload.get("radios", [])

    now = time.time()
    with _lock:
        station = _state.setdefault(station_id, {"name": station_name, "radios": {}})
        station["name"] = station_name
        station["last_report_at"] = now

        radios = station["radios"]
        for r in radios_in:
            rid = r.get("id")
            if not rid:
                continue
            radios[rid] = {
                "label": r.get("label", rid),
                "freq_hz": r.get("freq_hz"),
                "band": r.get("band"),
                "mode": r.get("mode"),
                "operator": r.get("operator"),
                "connected": bool(r.get("connected", False)),
                "source": r.get("source", "unknown"),
                "_last_report_at": now,
            }

    return {"ok": True}


def _build_stations_snapshot() -> List[Dict[str, Any]]:
    """The online/offline-computed view -- shared by /api/status (the
    frontend's own poll) and /api/health (a summarized count for an
    external monitor), so the two can never drift on what "online" means.
    Caller holds _lock.
    """
    now = time.time()
    out: List[Dict[str, Any]] = []
    for station_id, station in _state.items():
        station_stale = (now - station["last_report_at"]) > OFFLINE_THRESHOLD_SECONDS
        radios_out = []
        for rid, r in station["radios"].items():
            radio_stale = (now - r["_last_report_at"]) > OFFLINE_THRESHOLD_SECONDS
            is_online = r["connected"] and not station_stale and not radio_stale
            radios_out.append({
                "id": rid,
                "label": r["label"],
                "freq_hz": r["freq_hz"] if is_online else None,
                "band": r.get("band") if is_online else None,
                "mode": r["mode"] if is_online else None,
                "operator": r["operator"] if is_online else None,
                "source": r["source"],
                "status": "online" if is_online else "offline",
            })
        out.append({
            "station_id": station_id,
            "station_name": station["name"],
            "radios": radios_out,
        })
    return out


@app.get("/api/status")
async def status():
    with _lock:
        stations = _build_stations_snapshot()
    return JSONResponse({"stations": stations, "server_time": time.time()})


@app.get("/api/health")
async def health():
    """For an external monitor (the ops dashboard), not the frontend.

    Unlike contestscore's /api/health, this process has almost no internal
    state to go wrong independent of the data it's reporting -- there's no
    database, no socket to bind, just an in-memory dict behind a lock. So
    `status` here is close to "did this request complete at all" (FastAPI
    wouldn't have routed here otherwise), and the genuinely useful signal
    is the online/offline breakdown itself -- a monitor that only checked
    "does the process respond" would never notice every station going
    quiet, since that failure is entirely in the agents, not this server.
    """
    with _lock:
        stations = _build_stations_snapshot()

    radios_total = 0
    radios_online = 0
    for station in stations:
        for radio in station["radios"]:
            radios_total += 1
            if radio["status"] == "online":
                radios_online += 1

    return JSONResponse({
        "status": "ok",
        "stations_reporting": len(stations),
        "radios_total": radios_total,
        "radios_online": radios_online,
        "offline_threshold_seconds": OFFLINE_THRESHOLD_SECONDS,
    })


# ---- Deploy metadata (for the frontend footer's "deployed at" line) -------
# Lets a viewer tell at a glance whether they're looking at a cached/stale
# page: this only changes when the service is actually restarted/redeployed,
# never on its own. deploy-info.json (git-ignored, optionally written at
# deploy time) wins if present; otherwise we fall back to this process's own
# start time, which for this project's "git pull + systemctl restart" deploy
# flow *is* the moment of deploy.
_PROCESS_STARTED_AT = datetime.datetime.now(datetime.timezone.utc).isoformat()
_DEPLOY_INFO_PATH = os.path.join(os.path.dirname(__file__), "deploy-info.json")
# The repo-root VERSION file is the one version for both halves: the agent
# build stamps it into the binary (see agent/main.go), and it's reported
# here, so a station's agent can be matched against the server it talks to.
_VERSION_PATH = os.path.join(os.path.dirname(__file__), "..", "VERSION")


def _read_version() -> Optional[str]:
    try:
        with open(_VERSION_PATH) as f:
            return f.read().strip() or None
    except OSError:
        return None


def _git_short_commit() -> Optional[str]:
    try:
        out = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            cwd=os.path.dirname(__file__),
            capture_output=True,
            text=True,
            timeout=2,
        )
        return (out.stdout.strip() or None) if out.returncode == 0 else None
    except Exception:
        return None


def _compute_version_info() -> Dict[str, Any]:
    try:
        with open(_DEPLOY_INFO_PATH) as f:
            data = json.load(f)
        if not isinstance(data, dict):
            data = {}
    except (OSError, ValueError):
        data = {}
    return {
        "version": _read_version(),
        "commit": data.get("commit") or _git_short_commit(),
        "deployedAt": data.get("deployedAt") or _PROCESS_STARTED_AT,
    }


# Computed once: the commit and process start time don't change without a
# restart, and a per-request git subprocess would stall uvicorn's single
# event loop (blocking every client's /api/status poll while it runs).
_VERSION_INFO = _compute_version_info()


@app.get("/api/version")
async def version():
    """Version and when this instance was last deployed, shown in the page footer."""
    return _VERSION_INFO


# Serve the frontend (index.html + assets) from ../server/static
_static_dir = os.path.join(os.path.dirname(__file__), "static")
app.mount("/", StaticFiles(directory=_static_dir, html=True), name="static")


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host=HOST, port=PORT)
