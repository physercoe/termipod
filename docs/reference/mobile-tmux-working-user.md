# Mobile tmux working user

> **Type:** reference
> **Status:** Current (2026-10-08)
> **Audience:** mobile users and contributors
> **Last verified vs code:** 2026.824.751-alpha
> **Freshness:** rolling

The mobile SSH connection can authenticate as one operating-system user
and control tmux as another. The working user is the account that owns the
tmux server, its panes, and the processes started in those panes.

## Connection settings

Keep **Username** and the SSH password or key set to the account allowed
to log in to the host. Select **Tmux**, then set **Working user** to the
target account and **Working-user password** to that account's `su`
password. **Test connection** authenticates both accounts and checks for
tmux in the target account's environment.

Leave **Working user** empty to control tmux as the SSH login account.
When editing an existing working-user connection, leaving its password
empty retains the saved password. Changing the working username requires
a password for the new account. Changing either working credential closes
the existing SSH connection and clears its cached tmux entries.

The host must allow the login account to run `su - <working-user>` and
must support SSH PTYs, `su` with a shell command, `stty`, and `/bin/sh`.
The target account needs a usable login shell and access to tmux.
Ordinary password authentication is supported; additional PAM challenges
such as one-time codes and password-expiration changes are not automated.
Passwords containing terminal control characters are rejected.

## Execution contract

<!-- verify symbol lib/services/ssh/work_user_shell.dart WorkUserShell -->
<!-- verify symbol lib/services/ssh/ssh_client.dart prepareTerminal -->
<!-- verify symbol lib/services/ssh/ssh_client.dart execTerminal -->

SSH transport authentication remains independent of terminal setup.
`prepareTerminal()` opens one private PTY channel, disables password echo,
and authenticates `su`. After switching, it verifies `id -un`, disables
terminal input/output transformations, and starts a non-interactive POSIX
command loop under the working user's login environment.

`execTerminal()` and `execTerminalWithExitCode()` use this authenticated
channel for tmux path discovery, session
listing and creation, pane capture, input, resizing, and other terminal
commands. Connection-card discovery, home-screen refresh, and terminal
setup all prepare this channel before inspecting tmux. This keeps the
mobile pane renderer, action bar, and session navigation available for
the working user's own tmux server, including existing sessions.

Commands are serialized and framed with channel-specific markers and exit
codes. Their payload encoding preserves quoting, embedded newlines, and
Unicode. Raw PTY input avoids canonical terminal line limits during large
pastes. PTY stderr is combined with stdout; exit status remains separate.

Each reconnect establishes and authenticates a new working-user channel.
A failed switch never falls back to the login account. A command timeout
closes the channel and never automatically replays that command, since a
mutation may already have executed. The terminal reports the failure; a
connection retry creates a fresh channel while remote tmux panes persist.

Raw-shell mode, SSH forwarding, and SFTP continue to use the SSH login
account. Working-user configuration applies to native tmux terminal setup.

## Persistence

<!-- verify symbol lib/providers/connection_provider.dart workUsername -->
<!-- verify symbol lib/services/ssh/connection_options.dart loadSshOptions -->

Connection metadata stores the optional `workUsername`. The separate
working-user password lives in secure storage using the existing password
store with the `<connection-id>_su` credential identifier. It is never
placed in connection metadata, remote command strings, or authentication
diagnostics. Password export and encrypted vault synchronization carry
this credential alongside the corresponding connection. Deleting the
connection or removing its working user deletes the local credential.
