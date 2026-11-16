#!/bin/bash
# deploy/test/autodeploy-test.sh -- runs deploy/autodeploy.sh for real
# against a scratch "origin" and checkout, with fakes for systemctl, curl
# (the GitHub checks API and /api/version) and journalctl. Needs bash,
# git and python3. Run by .github/workflows/deploy-test.yml.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/../autodeploy.sh"
fails=0

setup() {
    T="$(mktemp -d)"
    export FAKE="$T/fake" GIT_CONFIG_GLOBAL="$T/gitconfig"
    mkdir -p "$FAKE/bin"
    git config --global user.email t@example.org
    git config --global user.name test
    git config --global init.defaultBranch main
    git init -q --bare "$T/origin.git"
    git clone -q "$T/origin.git" "$T/work" 2>/dev/null
    (cd "$T/work" && echo v1 > app.txt && git add app.txt && git commit -qm v1 && git push -q origin main)
    git clone -q "$T/origin.git" "$T/box"
    export AUTODEPLOY_REPO="$T/box" AUTODEPLOY_USER="$(id -un)" AUTODEPLOY_STATE="$T/state" \
           AUTODEPLOY_VERSION_URL="http://version" AUTODEPLOY_CHECKS_API="http://checks" \
           AUTODEPLOY_HEALTH_TRIES=3 AUTODEPLOY_HEALTH_WAIT=0
    : > "$FAKE/restarts"; : > "$FAKE/api-calls"
    echo passed > "$FAKE/checks"     # passed | pending | failed | none
    echo good > "$FAKE/server"       # good: reports the checkout's commit; broken: no answer
    cat > "$FAKE/bin/systemctl" <<'F'
#!/bin/bash
[[ "$1" == restart ]] && echo "$2" >> "$FAKE/restarts"
exit 0
F
    cat > "$FAKE/bin/curl" <<'F'
#!/bin/bash
url="${!#}"
case "$url" in
  http://checks/*)
    echo "$url" >> "$FAKE/api-calls"
    case "$(cat "$FAKE/checks")" in
      passed)  echo '{"check_runs":[{"name":"web","status":"completed","conclusion":"success"},{"name":"release","status":"completed","conclusion":"skipped"}]}' ;;
      pending) echo '{"check_runs":[{"name":"web","status":"in_progress","conclusion":null}]}' ;;
      failed)  echo '{"check_runs":[{"name":"web","status":"completed","conclusion":"failure"}]}' ;;
      none)    echo '{"check_runs":[]}' ;;
    esac ;;
  http://version)
    [[ "$(cat "$FAKE/server")" == good ]] || exit 7
    echo "{\"commit\":\"$(git -C "$AUTODEPLOY_REPO" rev-parse --short HEAD)\"}" ;;
esac
F
    printf '#!/bin/bash\nexit 0\n' > "$FAKE/bin/journalctl"
    chmod +x "$FAKE/bin"/*
}

push_commit() { (cd "$T/work" && echo "$1" > app.txt && git commit -qam "$1" && git push -q origin main); }
run() { PATH="$FAKE/bin:$PATH" bash "$SCRIPT" > "$T/out" 2>&1; echo $? > "$T/rc"; }
check() { local what="$1"; shift; if "$@"; then echo "ok   $what"; else echo "FAIL $what"; fails=$((fails+1)); sed 's/^/     | /' "$T/out"; fi; }
rc() { [[ "$(cat "$T/rc")" == "$1" ]]; }
on() { [[ "$(cat "$T/box/app.txt")" == "$1" ]]; }
restarts() { [[ "$(wc -l < "$FAKE/restarts")" -eq "$1" ]]; }
healthy() { [[ -e "$T/state/last-ok" ]]; }
not_healthy() { [[ ! -e "$T/state/last-ok" ]]; }
fresh_mark() { rm -f "$T/state/last-ok"; }

echo "# nothing new"
setup; run
check "exit 0" rc 0
check "no restart" restarts 0
check "marked healthy" healthy
check "GitHub not asked" test ! -s "$FAKE/api-calls"

echo "# new commit, checks passed"
setup; push_commit v2; run
check "exit 0" rc 0
check "box on v2" on v2
check "restarted once" restarts 1
check "marked healthy" healthy

echo "# new commit, checks still running, then done"
setup; push_commit v2; echo pending > "$FAKE/checks"; run
check "waits (exit 0)" rc 0
check "still on v1" on v1
check "no restart" restarts 0
echo none > "$FAKE/checks"; run
check "no checks registered yet: still waits" on v1
echo passed > "$FAKE/checks"; run
check "deploys once they pass" on v2

echo "# new commit, checks failed"
setup; push_commit v2; echo failed > "$FAKE/checks"; run
check "exit 0 (the box is fine)" rc 0
check "still on v1" on v1
check "no restart" restarts 0
check "says why" grep -q "checks failed: web" "$T/out"
: > "$FAKE/api-calls"; run
check "a known-failed commit isn't asked about again" test ! -s "$FAKE/api-calls"
push_commit v3; echo passed > "$FAKE/checks"; run
check "a fixed main deploys" on v3

echo "# new commit doesn't come up -> rolled back, and keeps alerting"
setup; push_commit v2; echo broken > "$FAKE/server"; run
check "exit 1" rc 1
check "rolled back to v1" on v1
check "restarted twice (deploy, rollback)" restarts 2
check "not marked healthy" not_healthy
echo good > "$FAKE/server"; run
check "next run still fails (the box is behind main)" rc 1
check "still not healthy" not_healthy
check "and doesn't try the same commit again" restarts 2
push_commit v3; run
check "a new commit on main deploys" on v3
check "and clears the alert" healthy

echo "# local edits on the box"
setup; push_commit v2; echo hacked > "$T/box/app.txt"; run
check "exit 1" rc 1
check "edit left alone" on hacked
check "no restart" restarts 0
check "not healthy" not_healthy

echo "# main rewritten (not a fast-forward)"
setup; (cd "$T/box" && echo local > app.txt && git commit -qam local); push_commit v2; run
check "exit 1" rc 1
check "says fast-forward" grep -q "not a fast-forward" "$T/out"
check "no restart" restarts 0

echo "# GitHub API unreachable"
setup; push_commit v2; printf '#!/bin/bash\nexit 6\n' > "$FAKE/bin/curl"; run
check "exit 1" rc 1
check "still on v1" on v1
check "not healthy" not_healthy

if (( fails )); then echo "$fails check(s) failed"; exit 1; fi
echo "all checks passed"
