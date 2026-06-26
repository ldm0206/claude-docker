import { spawn as ptySpawn } from "node-pty";

/**
 * Resolve an env value that may be a plain object or a lazy factory function.
 * PTY env is resolved at start() time so a restart re-evaluates dynamic inputs
 * (e.g. proxy routing that depends on whether the MITM is up).
 */
export function resolveEnv(env) {
  return typeof env === "function" ? env() : env;
}

export function createPtyManager({ cwd, env, command = "claude", args = [], cols = 80, rows = 24 }) {
  let proc = null;
  const dataCbs = new Set();
  const exitCbs = new Set();

  return {
    start() {
      if (proc) return;
      // Resolve env lazily on every start() so restarts pick up changes
      // (e.g. debugProxy.isUp() flipping true after capture is enabled).
      const envObj = resolveEnv(env);
      proc = ptySpawn(command, args, {
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
