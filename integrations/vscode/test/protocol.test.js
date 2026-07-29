"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const {
  ProtocolError,
  notificationGrade,
  parseAck,
  parseClaim,
  parseStatus,
  plainText
} = require("../src/protocol");

test("status requires a core-minted route for every visible level", () => {
  assert.throws(
    () => parseStatus(JSON.stringify({
      available_channels: ["vscode"],
      installed_channels: ["vscode"],
      level: { line: "1 new", quiet: false, severity: "ordinary" }
    })),
    ProtocolError
  );

  const status = parseStatus(JSON.stringify({
    available_channels: ["vscode", "future-channel"],
    installed_channels: ["vscode"],
    project: { id: "01PROJECT", root: "/ignored", channels: ["vscode"] },
    level: {
      line: "  1\u0000 new\nitem ",
      new: 1,
      urgent: 0,
      stale: 0,
      update: "",
      severity: 11,
      quiet: false,
      route_token: "01ROUTE"
    },
    ignored_future_field: true
  }));
  assert.equal(status.level.line, "1 new item");
  assert.equal(status.level.routeToken, "01ROUTE");
  assert.equal(status.level.severity, "urgent");
  assert.deepEqual(status.project, { id: "01PROJECT", channels: ["vscode"] });
});

test("quiet status may omit a route", () => {
  const status = parseStatus(JSON.stringify({
    available_channels: ["vscode"],
    installed_channels: ["vscode"],
    level: { line: "", severity: "ordinary", quiet: true }
  }));
  assert.equal(status.level.quiet, true);
  assert.equal(status.level.routeToken, undefined);
});

test("claim is fail-closed on a missing identity or lease", () => {
  const valid = {
    lease: { token: "lease-1", expires_at: "2026-07-29T12:00:00Z" },
    entries: [{
      schema_version: 1,
      entry_id: "entry-1",
      project_id: "project-1",
      space_id: "delivery",
      artifact_id: "XW-123",
      event_id: "01EVENT",
      kind: "gate_pending",
      severity: "urgent",
      title: "Gate\u0007 needed",
      summary: "delivery\nXW-123",
      route_token: "route-1"
    }]
  };
  const claim = parseClaim(JSON.stringify(valid));
  assert.equal(claim.entries[0].title, "Gate needed");
  assert.equal(claim.entries[0].summary, "delivery XW-123");
  assert.equal(notificationGrade(claim.entries[0]), "warning");

  const missingEntryID = structuredClone(valid);
  delete missingEntryID.entries[0].entry_id;
  assert.throws(() => parseClaim(JSON.stringify(missingEntryID)), ProtocolError);

  const missingLease = structuredClone(valid);
  delete missingLease.lease.token;
  assert.throws(() => parseClaim(JSON.stringify(missingLease)), ProtocolError);
});

test("empty claims need no lease and update entries need no artifact identity", () => {
  assert.deepEqual(parseClaim('{"entries":[]}'), { leaseToken: undefined, entries: [] });
  const update = parseClaim(JSON.stringify({
    lease: { token: "lease-1" },
    entries: [{
      schema_version: 1,
      entry_id: "entry-update",
      project_id: "project-1",
      kind: "update_available",
      severity: "ordinary",
      title: "update v1 to v2",
      summary: "details available",
      route_token: "route-update"
    }]
  }));
  assert.equal(update.entries[0].artifactId, undefined);

  const future = JSON.parse(JSON.stringify({
    lease: { token: "lease-1" },
    entries: [{
      schema_version: 2,
      entry_id: "entry-update",
      project_id: "project-1",
      kind: "update_available",
      severity: "ordinary",
      title: "future",
      summary: "",
      route_token: "route-update"
    }]
  }));
  assert.throws(() => parseClaim(JSON.stringify(future)), ProtocolError);
});

test("ack parses only bounded identifier arrays", () => {
  assert.deepEqual(
    parseAck('{"acked":["entry-1"],"already_acked":["entry-2"]}'),
    { acked: ["entry-1"], alreadyAcked: ["entry-2"] }
  );
  assert.throws(() => parseAck('{"acked":"entry-1","already_acked":[]}'), ProtocolError);
});

test("plain text strips controls, flattens whitespace, and bounds length", () => {
  assert.equal(plainText("a\u0000b\nc", 20), "a b c");
  assert.equal(plainText("abcdefgh", 5), "abcd…");
  assert.equal(plainText("hostile $(gear) icon", 40), "hostile $ (gear) icon");
  assert.equal(plainText({ body: "not text" }, 10), "");
});
