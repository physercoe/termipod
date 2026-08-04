#!/bin/sh
# Post-install for the TermiPod deb (wired via electron-builder.yml
# deb.afterInstall). Fixes two launch-time failures seen on Ubuntu 24.04:
#
# 1. chrome-sandbox must be setuid-root. The package files install it 0755,
#    and Electron aborts at launch: "The SUID sandbox helper binary was
#    found, but is not configured correctly ... owned by root, mode 4755".
#
# 2. Ubuntu 24.04+ restricts unprivileged user namespaces via AppArmor
#    (kernel.apparmor_restrict_unprivileged_userns=1). Without a profile,
#    Chromium's sandbox is denied cap_sys_admin inside the userns
#    (apparmor="DENIED" ... profile="unprivileged_userns") and the app dies
#    instantly — the desktop-shell "closed unexpectedly" report. Ship a
#    profile granting userns creation to the installed binary, the same
#    shape Ubuntu uses for Chrome itself. The sysctl guard keeps this
#    off distros without the restriction.
set -e

APP_DIR="/opt/TermiPod"
BIN="$APP_DIR/termipod-electron"

chmod 4755 "$APP_DIR/chrome-sandbox"

if [ "$(sysctl -n kernel.apparmor_restrict_unprivileged_userns 2>/dev/null)" = "1" ]; then
  PROFILE=/etc/apparmor.d/termipod-electron
  cat > "$PROFILE" <<EOF
abi <abi/4.0>,
include <tunables/global>

profile termipod-electron $BIN flags=(unconfined) {
  userns,
}
EOF
  if command -v apparmor_parser >/dev/null 2>&1; then
    apparmor_parser -r "$PROFILE" || true
  fi
fi
