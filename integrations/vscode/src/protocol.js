"use strict";

const CONTROL_CHARACTERS = /[\u0000-\u001f\u007f-\u009f]/g;
const SAFE_ID = /^[A-Za-z0-9][A-Za-z0-9._~:-]*$/;
const SEVERITIES = new Set(["ordinary", "info", "warning", "urgent", "error", "critical"]);

class ProtocolError extends Error {
  constructor(message) {
    super(message);
    this.name = "ProtocolError";
    this.code = "invalid-protocol";
  }
}

function plainText(value, maximum) {
  if (typeof value !== "string") {
    return "";
  }
  const clean = value
    .replace(CONTROL_CHARACTERS, " ")
    .replace(/\$\(/g, "$ (")
    .replace(/\s+/g, " ")
    .trim();
  return clean.length > maximum ? `${clean.slice(0, Math.max(0, maximum - 1))}…` : clean;
}

function requiredID(value, field, maximum = 256) {
  if (typeof value !== "string" || value.length === 0 || value.length > maximum || !SAFE_ID.test(value)) {
    throw new ProtocolError(`${field} is missing or malformed`);
  }
  return value;
}

function optionalID(value, field, maximum = 256) {
  if (value === undefined || value === null || value === "") {
    return undefined;
  }
  return requiredID(value, field, maximum);
}

function object(value, field) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new ProtocolError(`${field} must be an object`);
  }
  return value;
}

function parseJSON(stdout) {
  let value;
  try {
    value = JSON.parse(stdout);
  } catch {
    throw new ProtocolError("CLI output is not valid JSON");
  }
  return object(value, "response");
}

function parseLevel(value) {
  const source = object(value, "level");
  const quiet = source.quiet === true;
  const severity = normalizeSeverity(source.severity);
  const routeToken = optionalID(source.route_token, "level.route_token");
  if (!quiet && !routeToken) {
    throw new ProtocolError("level.route_token is required when level is not quiet");
  }
  return {
    line: plainText(source.line, 160),
    newCount: boundedInteger(source.new),
    urgentCount: boundedInteger(source.urgent),
    staleCount: boundedInteger(source.stale),
    update: plainText(source.update, 80),
    severity,
    quiet,
    routeToken
  };
}

function parseStatus(stdout) {
  const source = parseJSON(stdout);
  const availableChannels = stringList(source.available_channels, 8);
  const installedChannels = stringList(source.installed_channels, 8);
  const level = parseLevel(source.level);
  let project;
  if (source.project !== undefined && source.project !== null) {
    const projectSource = object(source.project, "project");
    project = {
      id: requiredID(projectSource.id, "project.id", 128),
      channels: stringList(projectSource.channels, 8)
    };
  }
  return { availableChannels, installedChannels, project, level };
}

function parseEntry(value) {
  const source = object(value, "entry");
  if (source.schema_version !== 1) {
    throw new ProtocolError("entry.schema_version is unsupported");
  }
  const kind = plainText(source.kind, 64);
  if (!kind) {
    throw new ProtocolError("entry.kind is missing");
  }
  const spaceId = optionalID(source.space_id, "entry.space_id", 128);
  const artifactId = optionalID(source.artifact_id, "entry.artifact_id", 128);
  const eventId = optionalID(source.event_id, "entry.event_id", 128);
  if (!kind.startsWith("update_") && (!spaceId || !artifactId)) {
    throw new ProtocolError("artifact entry identity is incomplete");
  }
  return {
    entryId: requiredID(source.entry_id, "entry.entry_id", 128),
    projectId: requiredID(source.project_id, "entry.project_id", 128),
    spaceId,
    artifactId,
    eventId,
    kind,
    severity: normalizeSeverity(source.severity),
    title: plainText(source.title, 120) || "A2A notification",
    summary: plainText(source.summary, 240),
    routeToken: requiredID(source.route_token, "entry.route_token")
  };
}

function parseClaim(stdout) {
  const source = parseJSON(stdout);
  if (!Array.isArray(source.entries) || source.entries.length > 50) {
    throw new ProtocolError("entries must be a bounded array");
  }
  if (source.entries.length === 0) {
    return { leaseToken: undefined, entries: [] };
  }
  const lease = object(source.lease, "lease");
  return {
    leaseToken: requiredID(lease.token, "lease.token"),
    entries: source.entries.map(parseEntry)
  };
}

function parseAck(stdout) {
  const source = parseJSON(stdout);
  return {
    acked: idList(source.acked, "acked"),
    alreadyAcked: idList(source.already_acked, "already_acked")
  };
}

function boundedInteger(value) {
  return Number.isSafeInteger(value) && value >= 0 && value <= 999999 ? value : 0;
}

function normalizeSeverity(value) {
  if (typeof value === "string" && SEVERITIES.has(value)) {
    return value;
  }
  if (Number.isSafeInteger(value)) {
    if (value >= 11) {
      return "urgent";
    }
    if (value >= 10) {
      return "ordinary";
    }
  }
  return "ordinary";
}

function stringList(value, maximum) {
  if (!Array.isArray(value) || value.length > maximum) {
    return [];
  }
  return value
    .filter((item) => typeof item === "string")
    .map((item) => plainText(item, 64))
    .filter(Boolean);
}

function idList(value, field) {
  if (!Array.isArray(value) || value.length > 50) {
    throw new ProtocolError(`${field} must be a bounded array`);
  }
  return value.map((item) => requiredID(item, field, 128));
}

function notificationGrade(entry) {
  if (entry.severity === "critical" || entry.severity === "error") {
    return "error";
  }
  if (entry.severity === "urgent" || entry.severity === "warning" || entry.kind.includes("gate")) {
    return "warning";
  }
  return "information";
}

module.exports = {
  ProtocolError,
  notificationGrade,
  parseAck,
  parseClaim,
  parseStatus,
  plainText,
  requiredID
};
