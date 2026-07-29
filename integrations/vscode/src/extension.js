"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs");
const vscode = require("vscode");
const { CLIClient, loadManagedConfig, resolveCLIPath } = require("./cli");
const { PollCoordinator } = require("./coordinator");
const { notificationPresentation, statusPresentation } = require("./presentation");
const { plainText } = require("./protocol");

let activeRuntime;

async function activate(context) {
  const output = vscode.window.createOutputChannel("A2A Notifications");
  const statusItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 20);
  const runtime = new ExtensionRuntime(context, output, statusItem);
  activeRuntime = runtime;

  context.subscriptions.push(
    output,
    statusItem,
    vscode.commands.registerCommand("a2ahub.notifications.open", () => runtime.openAggregate()),
    vscode.commands.registerCommand("a2ahub.notifications.refresh", () => runtime.refresh()),
    vscode.commands.registerCommand("a2ahub.notifications.showOutput", () => output.show(true)),
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration("a2ahub.notifications")) {
        void runtime.restart();
      }
    }),
    { dispose: () => runtime.dispose() }
  );

  await runtime.restart();
}

function deactivate() {
  activeRuntime?.dispose();
  activeRuntime = undefined;
}

class ExtensionRuntime {
  constructor(context, output, statusItem) {
    this.context = context;
    this.output = output;
    this.statusItem = statusItem;
    this.coordinator = undefined;
    this.cli = undefined;
    this.routeToken = undefined;
    this.generation = 0;
    this.remoteWindow = Boolean(vscode.env.remoteName);
  }

  async restart() {
    const generation = ++this.generation;
    this.stopCoordinator();
    this.statusItem.hide();
    this.routeToken = undefined;

    if (vscode.env.uiKind === vscode.UIKind.Web) {
      this.health("setup", { code: "web-host" });
      return;
    }

    const settings = machineSettings();
    let managed;
    let cliPath;
    try {
      managed = await loadManagedConfig();
      cliPath = await resolveCLIPath(settings.cliPath || managed.cliPath);
    } catch (error) {
      if (generation !== this.generation) {
        return;
      }
      this.health("setup", error);
      this.renderUnavailable(settings.quiet);
      return;
    }
    if (generation !== this.generation) {
      return;
    }

    this.cli = new CLIClient(cliPath);
    try {
      await this.cli.verifyVersion(managed.cliVersion);
    } catch (error) {
      this.health("setup", error);
      this.renderUnavailable(settings.quiet);
      this.cli.dispose();
      this.cli = undefined;
      return;
    }
    const readyKey = `ready:${managed.cliVersion}`;
    if (this.context.globalState.get(readyKey) !== true) {
      await vscode.window.showInformationMessage("A2A notifications are ready in VS Code.");
      await this.context.globalState.update(readyKey, true);
    }
    this.coordinator = new PollCoordinator({
      cli: this.cli,
      consumerId: consumerId(this.context, vscode.env.machineId),
      contexts: () => localContexts(this.remoteWindow),
      managedProjectRoots: managed.projectRoots,
      intervalMs: settings.intervalSeconds * 1000,
      quiet: settings.quiet,
      presentation: {
        health: (operation, error) => this.health(operation, error),
        notify: (entry) => this.notify(entry),
        renderStatus: (statuses, state) => this.renderStatus(statuses, state)
      }
    });
    this.coordinator.start();
  }

  refresh() {
    return this.coordinator?.refresh();
  }

  renderUnavailable(quiet) {
    this.applyStatusPresentation(statusPresentation([], { failures: 1, quiet, remote: this.remoteWindow }));
  }

  renderStatus(statuses, state) {
    this.applyStatusPresentation(statusPresentation(statuses, {
      ...state,
      remote: this.remoteWindow
    }));
  }

  applyStatusPresentation(presentation) {
    this.routeToken = presentation.routeToken;
    if (presentation.hidden) {
      this.statusItem.hide();
      return;
    }
    this.statusItem.text = presentation.text;
    this.statusItem.tooltip = presentation.tooltip;
    this.statusItem.command = presentation.command;
    this.statusItem.backgroundColor = presentation.warning
      ? new vscode.ThemeColor("statusBarItem.warningBackground")
      : undefined;
    this.statusItem.show();
  }

  notify(entry) {
    const { grade, message } = notificationPresentation(entry);
    let result;
    if (grade === "error") {
      result = vscode.window.showErrorMessage(message, "Open A2A");
    } else if (grade === "warning") {
      result = vscode.window.showWarningMessage(message, "Open A2A");
    } else {
      result = vscode.window.showInformationMessage(message, "Open A2A");
    }

    Promise.resolve(result)
      .then((choice) => {
        if (choice === "Open A2A") {
          return this.openRoute(entry.routeToken);
        }
        return undefined;
      })
      .catch((error) => this.health("notification-action", error));
    return true;
  }

  async openAggregate() {
    if (!this.routeToken) {
      this.health("open", { code: "missing-route" });
      await vscode.window.showWarningMessage("A trusted A2A route is not currently available.");
      return;
    }
    await this.openRoute(this.routeToken);
  }

  async openRoute(routeToken) {
    try {
      await this.cli?.open(routeToken);
    } catch (error) {
      this.health("open", error);
      await vscode.window.showWarningMessage("A2A could not open the local view. See A2A notification health for details.");
    }
  }

  health(operation, error) {
    const code = plainText(error?.code, 48) || "unexpected";
    this.output.appendLine(`${new Date().toISOString()} ${plainText(operation, 32)} failed (${code})`);
  }

  stopCoordinator() {
    this.coordinator?.stop();
    this.coordinator = undefined;
    this.cli?.dispose();
    this.cli = undefined;
  }

  dispose() {
    this.generation += 1;
    this.stopCoordinator();
    this.statusItem.hide();
  }
}

function machineSettings() {
  const configuration = vscode.workspace.getConfiguration("a2ahub.notifications");
  const cliInspection = configuration.inspect("cliPath");
  const intervalInspection = configuration.inspect("pollIntervalSeconds");
  const quietInspection = configuration.inspect("quietPresentation");
  return {
    cliPath: typeof cliInspection?.globalValue === "string" ? cliInspection.globalValue : "",
    intervalSeconds: boundedInterval(intervalInspection?.globalValue ?? intervalInspection?.defaultValue),
    quiet: quietInspection?.globalValue === true
  };
}

function boundedInterval(value) {
  return Number.isFinite(value) ? Math.min(3600, Math.max(15, Math.round(value))) : 60;
}

function consumerId(context, machineId) {
  const identity = `${plainText(machineId, 256)}\0${context.globalStorageUri.fsPath}`;
  return `vscode-${crypto.createHash("sha256").update(identity).digest("hex").slice(0, 32)}`;
}

async function localContexts(remoteWindow) {
  if (remoteWindow) {
    return [];
  }
  const folders = vscode.workspace.workspaceFolders || [];
  const roots = [];
  for (const folder of folders) {
    if (folder.uri.scheme !== "file") {
      continue;
    }
    try {
      const real = await fs.promises.realpath(folder.uri.fsPath);
      const stat = await fs.promises.stat(real);
      if (stat.isDirectory()) {
        roots.push(real);
      }
    } catch {
      // A moved/deleted project is isolated; other local folders still poll.
    }
  }
  return [...new Set(roots)].sort().map((cwd) => ({ cwd }));
}

module.exports = {
  ExtensionRuntime,
  activate,
  boundedInterval,
  consumerId,
  deactivate
};
