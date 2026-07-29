"use strict";

class PollCoordinator {
  constructor(options) {
    this.cli = options.cli;
    this.consumerId = options.consumerId;
    this.contexts = options.contexts;
    this.managedProjectRoots = options.managedProjectRoots || [];
    this.presentation = options.presentation;
    this.intervalMs = options.intervalMs;
    this.quiet = options.quiet;
    this.timer = undefined;
    this.abortController = undefined;
    this.running = false;
    this.stopped = false;
  }

  start() {
    if (this.stopped) {
      return;
    }
    void this.refresh();
  }

  async refresh() {
    if (this.stopped || this.running) {
      return;
    }
    this.running = true;
    this.abortController = new AbortController();
    const signal = this.abortController.signal;
    try {
      const contexts = pollContexts(await this.contexts(), this.managedProjectRoots);
      const results = await Promise.allSettled(
        contexts.map((context) => this.cli.status(context.cwd, signal))
      );
      const statuses = [];
      let failures = 0;
      for (const result of results) {
        if (result.status === "fulfilled") {
          statuses.push(result.value);
        } else if (!signal.aborted) {
          failures += 1;
          this.presentation.health("status", result.reason);
        }
      }
      this.presentation.renderStatus(statuses, { failures, quiet: this.quiet });

      if (!this.quiet) {
        const projects = enrolledProjects(statuses);
        for (const projectId of projects) {
          if (signal.aborted) {
            break;
          }
          await this.claimProject(projectId, signal);
        }
      }
    } catch (error) {
      if (!signal.aborted) {
        this.presentation.health("poll", error);
        this.presentation.renderStatus([], { failures: 1, quiet: this.quiet });
      }
    } finally {
      this.running = false;
      this.abortController = undefined;
      this.schedule();
    }
  }

  async claimProject(projectId, signal) {
    let claim;
    try {
      claim = await this.cli.claim(projectId, this.consumerId, signal);
    } catch (error) {
      if (!signal.aborted) {
        this.presentation.health("claim", error);
      }
      return;
    }
    if (claim.entries.length === 0) {
      return;
    }

    const accepted = [];
    for (const entry of claim.entries) {
      if (signal.aborted) {
        return;
      }
      try {
        if (this.presentation.notify(entry)) {
          accepted.push(entry.entryId);
        }
      } catch (error) {
        this.presentation.health("present", error);
      }
    }
    if (accepted.length === 0) {
      return;
    }
    try {
      await this.cli.ack(claim.leaseToken, accepted, signal);
    } catch (error) {
      if (!signal.aborted) {
        this.presentation.health("ack", error);
      }
    }
  }

  schedule() {
    if (this.stopped) {
      return;
    }
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.refresh(), this.intervalMs);
  }

  stop() {
    this.stopped = true;
    clearTimeout(this.timer);
    this.abortController?.abort();
    this.cli.dispose();
  }
}

function pollContexts(workspaceContexts, managedProjectRoots) {
  const roots = new Set(managedProjectRoots);
  for (const context of workspaceContexts) {
    roots.add(context.cwd);
  }
  return [...roots].sort().map((cwd) => ({ cwd }));
}

function enrolledProjects(statuses) {
  const ids = new Set();
  for (const status of statuses) {
    if (status.project?.channels.includes("vscode")) {
      ids.add(status.project.id);
    }
  }
  return [...ids].sort();
}

module.exports = { PollCoordinator, enrolledProjects, pollContexts };
