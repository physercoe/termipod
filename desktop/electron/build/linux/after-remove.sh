#!/bin/sh
# Post-remove for the TermiPod deb (wired via electron-builder.yml
# deb.afterRemove). Drops the AppArmor profile installed by after-install.sh;
# on upgrade the subsequent after-install run recreates it.
set -e

PROFILE=/etc/apparmor.d/termipod-electron
if [ -f "$PROFILE" ]; then
  if command -v apparmor_parser >/dev/null 2>&1; then
    apparmor_parser -R "$PROFILE" 2>/dev/null || true
  fi
  rm -f "$PROFILE"
fi
