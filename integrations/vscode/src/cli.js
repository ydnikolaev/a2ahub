"use strict";

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { parseAck, parseClaim, parseStatus, requiredID } = require("./protocol");

const MAX_STDOUT_BYTES = 512 * 1024;
const MAX_STDERR_BYTES = 32 * 1024;
const MAX_CONFIG_BYTES = 64 * 1024;
const MAX_MANAGED_PROJECTS = 512;
const MAX_PROJECT_ROOT_BYTES = 4096;
const DEFAULT_TIMEOUT_MS = 10_000;
const MANAGED_PROTOCOL_VERSION = 1;

class CLIError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "CLIError";
    this.code = code;
  }
}

async function resolveCLIPath(configuredPath, fileSystem = fs.promises) {
  if (typeof configuredPath !== "string" || !path.isAbsolute(configuredPath)) {
    throw new CLIError("cli-path", "Configure an absolute CLI path in machine settings");
  }
  let resolved;
  try {
    resolved = await fileSystem.realpath(configuredPath);
    const stat = await fileSystem.stat(resolved);
    if (!stat.isFile()) {
      throw new Error("not a regular file");
    }
    if (process.platform !== "win32") {
      await fileSystem.access(resolved, fs.constants.X_OK);
    }
  } catch {
    throw new CLIError("cli-path", "The configured CLI path is not an executable file");
  }
  return resolved;
}

async function loadManagedConfig(fileSystem = fs.promises, home = os.homedir()) {
  const configPath = path.join(home, ".config", "a2a", "vscode-notifier.json");
  let stat;
  let raw;
  try {
    stat = await fileSystem.stat(configPath);
    if (!stat.isFile() || stat.size > MAX_CONFIG_BYTES) {
      throw new Error("not a bounded regular file");
    }
    if (process.platform !== "win32" && (stat.mode & 0o077) !== 0) {
      throw new Error("config permissions are not private");
    }
    raw = await fileSystem.readFile(configPath, "utf8");
  } catch {
    throw new CLIError("managed-config", "Run `a2a notifications install --channel vscode`");
  }
  let value;
  try {
    value = JSON.parse(raw);
  } catch {
    throw new CLIError("managed-config", "The CLI-managed notification config is malformed");
  }
  if (
    !value || typeof value !== "object" || Array.isArray(value) ||
    value.schema_version !== 1 ||
    value.protocol_version !== MANAGED_PROTOCOL_VERSION ||
    typeof value.cli_path !== "string" ||
    typeof value.cli_version !== "string" ||
    value.cli_version.length === 0 || value.cli_version.length > 64
  ) {
    throw new CLIError("managed-config", "The CLI-managed notification config is incompatible");
  }
  let projectRoots;
  try {
    projectRoots = parseManagedProjectRoots(value.projects);
  } catch {
    throw new CLIError("managed-config", "The CLI-managed notification config is incompatible");
  }
  return { cliPath: value.cli_path, cliVersion: value.cli_version, projectRoots };
}

function parseManagedProjectRoots(projects) {
  if (!Array.isArray(projects) || projects.length > MAX_MANAGED_PROJECTS) {
    throw new Error("projects must be a bounded array");
  }
  const roots = new Set();
  for (const project of projects) {
    if (!project || typeof project !== "object" || Array.isArray(project)) {
      throw new Error("project must be an object");
    }
    requiredID(project.id, "project.id", 128);
    if (
      typeof project.root !== "string" ||
      project.root.length === 0 ||
      Buffer.byteLength(project.root) > MAX_PROJECT_ROOT_BYTES ||
      project.root.includes("\0") ||
      !path.isAbsolute(project.root)
    ) {
      throw new Error("project.root must be a bounded absolute path");
    }
    roots.add(project.root);
  }
  return [...roots].sort();
}

class CLIClient {
  constructor(cliPath, options = {}) {
    if (!path.isAbsolute(cliPath)) {
      throw new CLIError("cli-path", "CLI path must be absolute");
    }
    this.cliPath = cliPath;
    this.timeoutMs = options.timeoutMs || DEFAULT_TIMEOUT_MS;
    this.spawnProcess = options.spawnProcess || spawn;
    this.children = new Set();
    this.disposed = false;
  }

  status(cwd, signal) {
    return this.run(["notifications", "status", "--channel", "vscode", "--json"], cwd, signal)
      .then(parseStatus);
  }

  async verifyVersion(expectedVersion, signal) {
    if (typeof expectedVersion !== "string" || expectedVersion.length === 0 || expectedVersion.length > 64) {
      throw new CLIError("version", "Managed CLI version is malformed");
    }
    const stamp = (await this.run(["version"], undefined, signal)).trim();
    if (!stamp.startsWith(`a2a ${expectedVersion} (`)) {
      throw new CLIError("version", "The installed extension and a2a CLI cohort do not match");
    }
  }

  claim(projectId, consumerId, signal) {
    requiredID(projectId, "project", 128);
    requiredID(consumerId, "consumer", 128);
    return this.run([
      "notifications", "claim",
      "--channel", "vscode",
      "--consumer", consumerId,
      "--project", projectId,
      "--limit", "20",
      "--json"
    ], undefined, signal).then(parseClaim);
  }

  ack(leaseToken, entryIds, signal) {
    requiredID(leaseToken, "lease");
    if (!Array.isArray(entryIds) || entryIds.length === 0 || entryIds.length > 50) {
      throw new CLIError("invalid-argument", "ack requires a bounded entry list");
    }
    for (const entryId of entryIds) {
      requiredID(entryId, "entry", 128);
    }
    return this.run([
      "notifications", "ack",
      "--lease", leaseToken,
      "--entry", ...entryIds,
      "--json"
    ], undefined, signal).then(parseAck);
  }

  open(routeToken, signal) {
    requiredID(routeToken, "route");
    return this.run(["notifications", "open", routeToken, "--json"], undefined, signal)
      .then(() => undefined);
  }

  run(args, cwd, signal) {
    if (this.disposed) {
      return Promise.reject(new CLIError("disposed", "CLI client is stopped"));
    }
    if (signal?.aborted) {
      return Promise.reject(new CLIError("cancelled", "CLI request was cancelled"));
    }
    const childCwd = cwd || os.homedir();
    return new Promise((resolve, reject) => {
      let settled = false;
      let stdout = Buffer.alloc(0);
      let stderrBytes = 0;
      let terminationError;
      let forceKillTimer;
      let child;
      try {
        child = this.spawnProcess(this.cliPath, args, {
          cwd: childCwd,
          shell: false,
          windowsHide: true,
          detached: false,
          stdio: ["ignore", "pipe", "pipe"]
        });
      } catch {
        reject(new CLIError("spawn", "Unable to start configured CLI"));
        return;
      }
      this.children.add(child);

      const finish = (error, value) => {
        if (settled) {
          return;
        }
        settled = true;
        clearTimeout(timer);
        clearTimeout(forceKillTimer);
        signal?.removeEventListener("abort", abort);
        this.children.delete(child);
        if (error) {
          reject(error);
        } else {
          resolve(value);
        }
      };
      const terminate = (error) => {
        terminationError = error;
        if (!child.killed) {
          child.kill();
          forceKillTimer = setTimeout(() => child.kill("SIGKILL"), 1000);
        }
      };
      const abort = () => {
        terminate(new CLIError("cancelled", "CLI request was cancelled"));
      };
      const timer = setTimeout(() => {
        terminate(new CLIError("timeout", "CLI request timed out"));
      }, this.timeoutMs);

      signal?.addEventListener("abort", abort, { once: true });
      child.once("error", () => finish(new CLIError("spawn", "Unable to start configured CLI")));
      child.stdout.on("data", (chunk) => {
        if (stdout.length + chunk.length > MAX_STDOUT_BYTES) {
          terminate(new CLIError("output-limit", "CLI JSON exceeded the bounded output limit"));
          return;
        }
        stdout = Buffer.concat([stdout, chunk]);
      });
      child.stderr.on("data", (chunk) => {
        stderrBytes += chunk.length;
        if (stderrBytes > MAX_STDERR_BYTES) {
          terminate(new CLIError("output-limit", "CLI error output exceeded the bounded limit"));
        }
      });
      child.once("close", (code, closeSignal) => {
        if (terminationError) {
          finish(terminationError);
          return;
        }
        if (code !== 0) {
          finish(new CLIError("exit", `CLI exited unsuccessfully (${code ?? closeSignal ?? "unknown"})`));
          return;
        }
        finish(undefined, stdout.toString("utf8"));
      });
    });
  }

  dispose() {
    this.disposed = true;
    for (const child of this.children) {
      if (!child.killed) {
        child.kill("SIGKILL");
      }
    }
  }
}

module.exports = { CLIClient, CLIError, loadManagedConfig, resolveCLIPath };
