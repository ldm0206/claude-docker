import { spawn as ptySpawn } from "node-pty";

export function createPtyManager({ cwd, env, command = "claude", args = [], cols = 80, rows = 24 }) {
  let proc = null;
  const dataCbs = new Set();
  const exitCbs = new Set();

  return {
    start() {
      if (proc) return;
      proc = ptySpawn(command, args, {
        name: "xterm-256color",
        cols, rows,
        cwd,
        env,
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
