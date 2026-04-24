#!/usr/bin/env node
// Cross-platform dispatcher for the pincher plugin's binary installer.
// Invoked from hooks/hooks.json on SessionStart. Forwards to the platform-
// specific script (install.sh on POSIX, install.ps1 on Windows) so each
// script can speak its native shell idioms without a polyglot header.
//
// Node is chosen as the dispatcher because every Claude Code install already
// has Node (Claude Code itself is a Node CLI). No third-party modules, no
// install step — just child_process + path.
'use strict';

const { spawnSync } = require('node:child_process');
const path = require('node:path');

const root = process.env.CLAUDE_PLUGIN_ROOT;
if (!root) {
  console.error('pincher-plugin: CLAUDE_PLUGIN_ROOT is unset — aborting');
  process.exit(1);
}

const isWindows = process.platform === 'win32';
const scriptPath = isWindows
  ? path.join(root, 'scripts', 'install.ps1')
  : path.join(root, 'scripts', 'install.sh');

const { cmd, args } = isWindows
  ? { cmd: 'powershell', args: ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', scriptPath] }
  : { cmd: 'sh',          args: [scriptPath] };

const result = spawnSync(cmd, args, { stdio: 'inherit', env: process.env });

// Preserve exit semantics so SessionStart can decide what to do with the
// status (a non-zero from the install script surfaces as a warning to the
// user; blocking errors use exit 2, which neither script emits today).
process.exit(result.status ?? 1);
