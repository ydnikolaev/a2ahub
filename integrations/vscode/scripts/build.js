"use strict";

const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const root = path.resolve(__dirname, "..");
const manifest = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));

if (manifest.main !== "./src/extension.js") {
  throw new Error("package main must remain the audited local UI entry point");
}
if (JSON.stringify(manifest.extensionKind) !== JSON.stringify(["ui"])) {
  throw new Error("extensionKind must be exactly [\"ui\"]");
}
if (!manifest.activationEvents.includes("onStartupFinished")) {
  throw new Error("onStartupFinished activation is required");
}

for (const directory of ["src", "scripts", "test"]) {
  for (const name of fs.readdirSync(path.join(root, directory))) {
    if (!name.endsWith(".js")) {
      continue;
    }
    execFileSync(process.execPath, ["--check", path.join(root, directory, name)], {
      stdio: "inherit"
    });
  }
}

process.stdout.write("VS Code adapter build checks passed\n");
