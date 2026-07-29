"use strict";

const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const { PassThrough } = require("node:stream");
const test = require("node:test");
const { CLIClient, CLIError, loadManagedConfig, resolveCLIPath } = require("../src/cli");

function fakeSpawnWith(stdoutValue, capture) {
  return (file, args, options) => {
    capture.push({ file, args, options });
    const child = new EventEmitter();
    child.stdout = new PassThrough();
    child.stderr = new PassThrough();
    child.killed = false;
    child.kill = () => {
      child.killed = true;
      queueMicrotask(() => child.emit("close", null, "SIGTERM"));
      return true;
    };
    queueMicrotask(() => {
      child.stdout.end(stdoutValue);
      child.stderr.end();
      child.emit("close", 0, null);
    });
    return child;
  };
}

test("client uses the exact executable and fixed status argv without a shell", async () => {
  const calls = [];
  const cli = new CLIClient("/trusted/a2a", {
    spawnProcess: fakeSpawnWith(JSON.stringify({
      available_channels: ["vscode"],
      installed_channels: ["vscode"],
      level: { line: "", quiet: true, severity: "ordinary" }
    }), calls)
  });
  await cli.status("/local/project");
  assert.equal(calls[0].file, "/trusted/a2a");
  assert.deepEqual(calls[0].args, ["notifications", "status", "--channel", "vscode", "--json"]);
  assert.equal(calls[0].options.cwd, "/local/project");
  assert.equal(calls[0].options.shell, false);
  assert.equal(calls[0].options.detached, false);
  cli.dispose();
});

test("claim and ack argv contain only validated protocol identifiers", async () => {
  const calls = [];
  const claimJSON = JSON.stringify({ lease: { token: "lease-1" }, entries: [] });
  const cli = new CLIClient("/trusted/a2a", {
    spawnProcess: fakeSpawnWith(claimJSON, calls)
  });
  await cli.claim("project-1", "vscode-consumer-1");
  assert.deepEqual(calls[0].args, [
    "notifications", "claim",
    "--channel", "vscode",
    "--consumer", "vscode-consumer-1",
    "--project", "project-1",
    "--limit", "20",
    "--json"
  ]);
  assert.throws(() => cli.claim("../workspace", "consumer"), /malformed/);
  cli.dispose();
});

test("dispose owns and terminates an in-flight child", async () => {
  let child;
  const spawnProcess = () => {
    child = new EventEmitter();
    child.stdout = new PassThrough();
    child.stderr = new PassThrough();
    child.killed = false;
    child.kill = () => {
      child.killed = true;
      queueMicrotask(() => child.emit("close", null, "SIGTERM"));
      return true;
    };
    return child;
  };
  const cli = new CLIClient("/trusted/a2a", { spawnProcess, timeoutMs: 1000 });
  const pending = cli.status("/local/project");
  cli.dispose();
  await assert.rejects(pending, CLIError);
  assert.equal(child.killed, true);
});

test("path resolver rejects relative and non-file configured paths", async () => {
  await assert.rejects(() => resolveCLIPath("a2a"), { code: "cli-path" });
  await assert.rejects(() => resolveCLIPath("/missing/a2a", {
    realpath: async () => {
      throw new Error("missing");
    }
  }), { code: "cli-path" });
});

test("managed config is bounded, private, and protocol pinned", async () => {
  const valid = JSON.stringify({
    schema_version: 1,
    protocol_version: 1,
    cli_path: "/trusted/a2a",
    cli_version: "v1.2.3",
    projects: [
      { id: "project-b", root: "/projects/b" },
      { id: "project-a", root: "/projects/a" },
      { id: "project-a-copy", root: "/projects/a" }
    ]
  });
  const loaded = await loadManagedConfig({
    stat: async () => ({ isFile: () => true, size: valid.length, mode: 0o100600 }),
    readFile: async () => valid
  }, "/home/user");
  assert.deepEqual(loaded, {
    cliPath: "/trusted/a2a",
    cliVersion: "v1.2.3",
    projectRoots: ["/projects/a", "/projects/b"]
  });

  await assert.rejects(() => loadManagedConfig({
    stat: async () => ({ isFile: () => true, size: valid.length, mode: 0o100644 }),
    readFile: async () => valid
  }, "/home/user"), { code: "managed-config" });
});

test("managed config rejects missing, relative, or malformed project roots", async () => {
  const base = {
    schema_version: 1,
    protocol_version: 1,
    cli_path: "/trusted/a2a",
    cli_version: "v1.2.3"
  };
  for (const projects of [
    undefined,
    [{ id: "project-1", root: "relative/project" }],
    [{ id: "../project", root: "/projects/one" }],
    [{ id: "project-1", root: "" }]
  ]) {
    const raw = JSON.stringify({ ...base, projects });
    await assert.rejects(() => loadManagedConfig({
      stat: async () => ({ isFile: () => true, size: raw.length, mode: 0o100600 }),
      readFile: async () => raw
    }, "/home/user"), { code: "managed-config" });
  }
});

test("version handshake rejects a mixed release cohort", async () => {
  const calls = [];
  const cli = new CLIClient("/trusted/a2a", {
    spawnProcess: fakeSpawnWith("a2a v1.2.2 (abc)\\n", calls)
  });
  await assert.rejects(() => cli.verifyVersion("v1.2.3"), { code: "version" });
  assert.deepEqual(calls[0].args, ["version"]);
  cli.dispose();
});
