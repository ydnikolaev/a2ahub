"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { notificationPresentation, statusPresentation } = require("../src/presentation");

test("status rendering uses a core route and urgent theme emphasis", () => {
  const rendered = statusPresentation([{
    level: {
      line: "a2a: 1 urgent $(gear)",
      quiet: false,
      severity: "urgent",
      routeToken: "route-1"
    }
  }], { failures: 0, quiet: false, remote: false });
  assert.equal(rendered.routeToken, "route-1");
  assert.equal(rendered.text, "$(bell) a2a: 1 urgent $ (gear)");
  assert.equal(rendered.warning, true);
  assert.equal(rendered.command, "a2ahub.notifications.open");
});

test("repeated global levels from multi-root contexts render once", () => {
  const level = {
    line: "a2a: 2 new",
    quiet: false,
    severity: "ordinary",
    routeToken: "aggregate-route"
  };
  const rendered = statusPresentation([{ level }, { level }], {
    failures: 0,
    quiet: false,
    remote: false
  });
  assert.equal(rendered.text, "$(bell) a2a: 2 new");
  assert.equal(rendered.routeToken, "aggregate-route");
});

test("remote tooltip states local-only scope and quiet hides everything", () => {
  const remote = statusPresentation([{
    level: {
      line: "a2a: 1 new",
      quiet: false,
      severity: "ordinary",
      routeToken: "route-1"
    }
  }], { failures: 0, quiet: false, remote: true });
  assert.match(remote.tooltip, /local-machine/);
  assert.match(remote.tooltip, /remote workspace content is never executed/);
  assert.deepEqual(statusPresentation([], {
    failures: 1,
    quiet: true,
    remote: false
  }), { hidden: true });
});

test("feed rendering maps ordinary and gate signals to native notification grades", () => {
  assert.deepEqual(notificationPresentation({
    title: "Reply",
    summary: "plain summary",
    severity: "ordinary",
    kind: "response_pending"
  }), {
    grade: "information",
    message: "Reply — plain summary"
  });
  assert.equal(notificationPresentation({
    title: "Gate",
    summary: "",
    severity: "ordinary",
    kind: "gate_pending"
  }).grade, "warning");
});
