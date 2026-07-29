"use strict";

const { notificationGrade, plainText } = require("./protocol");

function statusPresentation(statuses, state) {
  if (state.quiet) {
    return { hidden: true };
  }
  const distinctLevels = new Map();
  for (const status of statuses) {
    if (!status.level.quiet) {
      distinctLevels.set(status.level.routeToken, status.level);
    }
  }
  const levels = [...distinctLevels.values()];
  if (levels.length === 0) {
    if (state.failures > 0) {
      return {
        hidden: false,
        text: "$(warning) A2A unavailable",
        tooltip: "A2A Notifications cannot use the configured local CLI. Open the A2A health output for details.",
        command: "a2ahub.notifications.showOutput",
        warning: true
      };
    }
    return { hidden: true };
  }

  const highest = levels.reduce((left, right) =>
    severityRank(right.severity) > severityRank(left.severity) ? right : left
  );
  const uniqueLines = [...new Set(levels.map((level) => level.line).filter(Boolean))];
  const line = uniqueLines.length === 1
    ? uniqueLines[0]
    : `A2A: ${levels.length} local projects`;
  const scope = state.remote
    ? "This remote window shows registered local-machine A2A state only; remote workspace content is never executed."
    : "This UI extension invokes only the configured local a2a executable.";
  const safeLine = plainText(line, 160) || "A2A notifications";
  return {
    hidden: false,
    text: `$(bell) ${plainText(safeLine, 80)}`,
    tooltip: `${safeLine}\n\nClick to open the trusted aggregate A2A view. ${scope}`,
    command: "a2ahub.notifications.open",
    warning: severityRank(highest.severity) >= 2,
    routeToken: highest.routeToken
  };
}

function notificationPresentation(entry) {
  return {
    grade: notificationGrade(entry),
    message: plainText(entry.summary ? `${entry.title} — ${entry.summary}` : entry.title, 360)
  };
}

function severityRank(severity) {
  if (severity === "critical" || severity === "error") {
    return 3;
  }
  if (severity === "urgent" || severity === "warning") {
    return 2;
  }
  if (severity === "info") {
    return 1;
  }
  return 0;
}

module.exports = { notificationPresentation, severityRank, statusPresentation };
