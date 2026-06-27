import { spawn as ptySpawn } from "node-pty";
import { existsSync } from "node:fs";

/**
 * Resolve an env value that may be a plain object or a lazy factory function.
 * PTY env is resolved at start() time so a restart re-evaluates dynamic inputs
 * (e.g. proxy routing that depends on whether the MITM is up).
 */
export function resolveEnv(env) {
  return typeof env === "function" ? env() : env;
}

const CLAUDE_BIN_PATH = "/home/claude/.local/bin/claude";

export function createPtyManager({ cwd, env, command = "claude", args = [], cols = 80, rows = 24 }) {
  let proc = null;
  const dataCbs = new Set();
  const exitCbs = new Set();
  let effectiveCommand = command;
  let effectiveArgs = args;

  return {
    start() {
      if (proc) return;
      // Resolve env lazily on every start() so restarts pick up changes
      // (e.g. debugProxy.isUp() flipping true after capture is enabled).
      const envObj = resolveEnv(env);
      // If claude binary is missing, fall back to bash so the user can
      // inspect the container environment and troubleshoot.
      if (effectiveCommand === "claude" && !existsSync(CLAUDE_BIN_PATH)) {
        effectiveCommand = "/bin/bash";
        effectiveArgs = [];
        // Print a warning that will appear in the terminal
        const warning = "\r\n⚠ claude not found at " + CLAUDE_BIN_PATH + ", falling back to bash\r\n\r\n";
        for (const cb of dataCbs) cb(warning);
      }
      proc = ptySpawn(effectiveCommand, effectiveArgs, {
        name: "xterm-256color",
        cols, rows,
        cwd,
        env: envObj,
      });
      for (const cb of dataCbs) proc.onData(cb);
      proc.onExit(({ exitCode }) => {
        const was = proc;
        proc = null;
        for (const cb of exitCbs) cb(exitCode);
        if (was) was.kill();
      });
    },
    onData(cb) {
      dataCbs.add(cb);
      if (proc) proc.onData(cb);
      return () => dataCbs.delete(cb);
    },
    write(data) { if (proc) proc.write(data); },
    resize(c, r) { if (proc) proc.resize(c, r); },
    onExit(cb) { exitCbs.add(cb); return () => exitCbs.delete(cb); },
    kill() { if (proc) { proc.kill(); proc = null; } },
    get alive() { return !!proc; },
  };
}
