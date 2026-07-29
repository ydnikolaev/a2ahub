"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { PollCoordinator, enrolledProjects } = require("../src/coordinator");

const quietLevel = { line: "", severity: "ordinary", quiet: true };

test("project enrollment is deduplicated across windows/status contexts", () => {
  assert.deepEqual(enrolledProjects([
    { project: { id: "b", channels: ["vscode"] }, level: quietLevel },
    { project: { id: "a", channels: ["vscode"] }, level: quietLevel },
    { project: { id: "a", channels: ["vscode"] }, level: quietLevel },
    { project: { id: "c", channels: ["macos"] }, level: quietLevel }
  ]), ["a", "b"]);
});

test("managed project roots are polled with no open workspace", async () => {
  const statusRoots = [];
  const coordinator = new PollCoordinator({
    cli: {
      status: async (cwd) => {
        statusRoots.push(cwd);
        return { level: quietLevel };
      },
      dispose: () => {}
    },
    consumerId: "vscode-profile-1",
    contexts: async () => [],
    managedProjectRoots: ["/managed/b", "/managed/a"],
    intervalMs: 60_000,
    quiet: true,
    presentation: {
      health: () => assert.fail("unexpected health error"),
      renderStatus: () => {},
      notify: () => true
    }
  });
  await coordinator.refresh();
  coordinator.stop();

  assert.deepEqual(statusRoots, ["/managed/a", "/managed/b"]);
});

test("managed roots and open local workspaces form one deduplicated poll set", async () => {
  const statusRoots = [];
  const coordinator = new PollCoordinator({
    cli: {
      status: async (cwd) => {
        statusRoots.push(cwd);
        return { level: quietLevel };
      },
      dispose: () => {}
    },
    consumerId: "vscode-profile-1",
    contexts: async () => [
      { cwd: "/workspace/open" },
      { cwd: "/managed/a" }
    ],
    managedProjectRoots: ["/managed/a", "/managed/closed"],
    intervalMs: 60_000,
    quiet: true,
    presentation: {
      health: () => assert.fail("unexpected health error"),
      renderStatus: () => {},
      notify: () => true
    }
  });
  await coordinator.refresh();
  coordinator.stop();

  assert.deepEqual(statusRoots, ["/managed/a", "/managed/closed", "/workspace/open"]);
});

test("one profile consumer claims, presents, and acknowledges accepted entries", async () => {
  const calls = [];
  const cli = {
    status: async (cwd) => {
      calls.push(["status", cwd]);
      return {
        project: { id: "project-1", channels: ["vscode"] },
        level: quietLevel
      };
    },
    claim: async (project, consumer) => {
      calls.push(["claim", project, consumer]);
      return {
        leaseToken: "lease-1",
        entries: [
          { entryId: "entry-1" },
          { entryId: "entry-2" }
        ]
      };
    },
    ack: async (lease, entries) => {
      calls.push(["ack", lease, entries]);
      return { acked: entries, alreadyAcked: [] };
    },
    dispose: () => calls.push(["dispose"])
  };
  const shown = [];
  const coordinator = new PollCoordinator({
    cli,
    consumerId: "vscode-profile-1",
    contexts: async () => [{ cwd: "/one" }, { cwd: "/one" }],
    intervalMs: 60_000,
    quiet: false,
    presentation: {
      health: () => assert.fail("unexpected health error"),
      renderStatus: () => {},
      notify: (entry) => {
        shown.push(entry.entryId);
        return entry.entryId === "entry-1";
      }
    }
  });
  await coordinator.refresh();
  coordinator.stop();

  assert.deepEqual(shown, ["entry-1", "entry-2"]);
  assert.deepEqual(calls.filter(([name]) => name === "claim"), [
    ["claim", "project-1", "vscode-profile-1"]
  ]);
  assert.deepEqual(calls.find(([name]) => name === "ack"), [
    "ack", "lease-1", ["entry-1"]
  ]);
});

test("quiet presentation does not claim or consume transitions", async () => {
  let claimed = false;
  const coordinator = new PollCoordinator({
    cli: {
      status: async () => ({
        project: { id: "project-1", channels: ["vscode"] },
        level: quietLevel
      }),
      claim: async () => {
        claimed = true;
      },
      dispose: () => {}
    },
    consumerId: "vscode-profile-1",
    contexts: async () => [{ cwd: "/one" }],
    intervalMs: 60_000,
    quiet: true,
    presentation: {
      health: () => {},
      renderStatus: () => {},
      notify: () => true
    }
  });
  await coordinator.refresh();
  coordinator.stop();
  assert.equal(claimed, false);
});

test("stop aborts the active poll and owns CLI shutdown", async () => {
  let disposed = false;
  let observedAbort = false;
  const coordinator = new PollCoordinator({
    cli: {
      status: (_cwd, signal) => new Promise((_resolve, reject) => {
        signal.addEventListener("abort", () => {
          observedAbort = true;
          reject(new Error("aborted"));
        }, { once: true });
      }),
      dispose: () => {
        disposed = true;
      }
    },
    consumerId: "vscode-profile-1",
    contexts: async () => [{ cwd: "/one" }],
    intervalMs: 60_000,
    quiet: false,
    presentation: {
      health: () => {},
      renderStatus: () => {},
      notify: () => true
    }
  });
  const pending = coordinator.refresh();
  await new Promise((resolve) => setImmediate(resolve));
  coordinator.stop();
  await pending;
  assert.equal(observedAbort, true);
  assert.equal(disposed, true);
});
