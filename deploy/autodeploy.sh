#!/bin/bash
# deploy/autodeploy.sh -- keeps the box on origin/main, automatically.
# Installed root-owned as /usr/local/sbin/station-status-autodeploy and run
# every 2 minutes by station-status-autodeploy.timer (deploy/DEPLOY.md §10).
#
# Pull, not push: nothing on GitHub can reach the box. No deploy key, no
# SSH, no secret. All this can ever do is what `merge --ff-only
# origin/main` + restart would do by hand -- the §9 steps -- and only for
# a commit whose checks all passed.
#
# Each run:
#   1. fetch origin main; nothing new -> mark healthy, done.
#   2. refuse (exit 1, no healthy mark) if the checkout has local edits or
#      main isn't a fast-forward: those need a person.
#   3. read the commit's GitHub check runs (public API, no token): any
#      still running -> wait for the next run; any failed -> don't deploy
#      that commit (logged once, remembered so the API isn't asked again;
#      the box itself is fine, so this doesn't alert -- GitHub reports CI).
#   4. fast-forward, restart, and wait for /api/version to report the new
#      commit. If it doesn't (a crash on start, a missing Python package),
#      reset to the previous commit, restart, exit 1 -- and keep exiting 1
#      on every run until main moves past that commit, so the alert stays.
#
# "Marked healthy" = $STATE/last-ok touched. The ops dashboard alerts when
# it's older than 15 minutes (file_age_checks on wt2p-vpserver-chi), so a
# stuck or failing loop pages. Every AUTODEPLOY_* variable exists for
# deploy/test/autodeploy-test.sh; the systemd unit sets none of them.
set -euo pipefail

REPO="${AUTODEPLOY_REPO:-/opt/station-status}"
RUN_AS="${AUTODEPLOY_USER:-stationstatus}"
UNIT="${AUTODEPLOY_UNIT:-station-status}"
VERSION_URL="${AUTODEPLOY_VERSION_URL:-http://127.0.0.1:8003/api/version}"
CHECKS_API="${AUTODEPLOY_CHECKS_API:-https://api.github.com/repos/chibondking/station-status/commits}"
STATE="${AUTODEPLOY_STATE:-/var/lib/station-status-autodeploy}"
HEALTH_TRIES="${AUTODEPLOY_HEALTH_TRIES:-15}"
HEALTH_WAIT="${AUTODEPLOY_HEALTH_WAIT:-2}"

say() { echo "autodeploy: $*"; }
die() { echo "autodeploy: ERROR $*" >&2; exit 1; }
healthy() { touch "$STATE/last-ok"; }

as_user() {
    if [[ "$(id -un)" == "$RUN_AS" ]]; then "$@"; else runuser -u "$RUN_AS" -- "$@"; fi
}
git_() { as_user git -C "$REPO" "$@"; }

mkdir -p "$STATE"
exec 9>"$STATE/lock"
flock -n 9 || { say "another run is in progress"; exit 0; }

git_ fetch --quiet origin main || die "git fetch failed"
local_sha="$(git_ rev-parse HEAD)"
remote_sha="$(git_ rev-parse origin/main)"

if [[ "$local_sha" == "$remote_sha" ]]; then
    healthy
    exit 0
fi
if [[ -n "$(git_ status --porcelain --untracked-files=no)" ]]; then
    die "the checkout at $REPO has local edits -- not deploying ${remote_sha:0:7}; commit or discard them by hand"
fi
if ! git_ merge-base --is-ancestor "$local_sha" "$remote_sha"; then
    die "origin/main (${remote_sha:0:7}) is not a fast-forward of ${local_sha:0:7} -- needs a manual deploy"
fi

# A commit that failed to start was rolled back. Keep failing (no healthy
# mark, so the dashboard keeps alerting) until main moves on: the box is
# behind main and someone needs to know.
if [[ "$(cat "$STATE/rolled-back-sha" 2>/dev/null || true)" == "$remote_sha" ]]; then
    die "still on ${local_sha:0:7}: ${remote_sha:0:7} failed to start and was rolled back -- fix main, or deploy by hand"
fi
if [[ "$(cat "$STATE/failed-sha" 2>/dev/null || true)" == "$remote_sha" ]]; then
    healthy # the box is fine, still on the last good commit; CI is GitHub's to report
    exit 0
fi

checks="$(curl -fsS --max-time 20 -H 'Accept: application/vnd.github+json' \
    "$CHECKS_API/$remote_sha/check-runs?per_page=100")" || die "couldn't read the checks for ${remote_sha:0:7}"
verdict="$(printf '%s' "$checks" | python3 -c '
import json, sys
runs = json.load(sys.stdin).get("check_runs", [])
if not runs:
    print("pending"); sys.exit()
if any(r.get("status") != "completed" for r in runs):
    print("pending"); sys.exit()
bad = sorted(r.get("name", "?") for r in runs if r.get("conclusion") not in ("success", "skipped", "neutral"))
print("failed " + ", ".join(bad) if bad else "passed")
')"
case "$verdict" in
    pending)
        say "${remote_sha:0:7}: checks still running -- next run"
        healthy
        exit 0 ;;
    failed*)
        say "${remote_sha:0:7}: not deploying, checks failed: ${verdict#failed }"
        echo "$remote_sha" > "$STATE/failed-sha"
        healthy
        exit 0 ;;
    passed) ;;
    *) die "couldn't make sense of the checks for ${remote_sha:0:7}" ;;
esac

say "deploying ${local_sha:0:7} -> ${remote_sha:0:7}"
git_ merge --ff-only --quiet "$remote_sha"
systemctl restart "$UNIT"

ok=0
for _ in $(seq 1 "$HEALTH_TRIES"); do
    sleep "$HEALTH_WAIT"
    commit="$(curl -fsS --max-time 3 "$VERSION_URL" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("commit",""))' 2>/dev/null || true)"
    if [[ -n "$commit" && "$remote_sha" == "$commit"* ]]; then ok=1; break; fi
done
if (( ! ok )); then
    echo "autodeploy: ERROR ${remote_sha:0:7} didn't come up (no /api/version reporting it) -- rolling back to ${local_sha:0:7}" >&2
    journalctl -u "$UNIT" -n 20 --no-pager >&2 2>/dev/null || true
    git_ reset --hard --quiet "$local_sha"
    systemctl restart "$UNIT"
    echo "$remote_sha" > "$STATE/rolled-back-sha"
    exit 1
fi
rm -f "$STATE/failed-sha" "$STATE/rolled-back-sha"
say "deployed ${remote_sha:0:7}"
healthy
