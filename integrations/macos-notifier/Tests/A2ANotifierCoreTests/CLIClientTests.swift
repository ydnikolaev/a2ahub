import Foundation
import Testing
@testable import A2ANotifierCore

private final class RecordingRunner: ProcessRunning {
    struct Invocation: Equatable {
        var executable: URL
        var arguments: [String]
    }

    var invocations: [Invocation] = []
    var outputs: [ProcessOutput]

    init(outputs: [ProcessOutput]) {
        self.outputs = outputs
    }

    func run(executable: URL, arguments: [String]) throws -> ProcessOutput {
        invocations.append(.init(executable: executable, arguments: arguments))
        return outputs.removeFirst()
    }
}

@Test func clientUsesOnlyTheConfiguredExecutableAndControlledArguments() throws {
    let claimJSON = """
    {
      "lease":{"token":"lease-1","expires_at":"2026-07-29T11:00:00Z"},
      "entries":[{
        "schema_version":1,
        "entry_id":"01JENTRY",
        "project_id":"01JPROJECT",
        "space_id":"delivery",
        "artifact_id":"XW-item",
        "event_id":"01JEVENT",
        "kind":"gate_pending",
        "severity":"urgent",
        "title":"Gate needed",
        "summary":"delivery · XW-item",
        "occurred_at":"2026-07-29T10:15:00Z",
        "source_revision":"abc123",
        "coalesce_key":"delivery:gate_pending",
        "route_token":"01JROUTE"
      }]
    }
    """
    let runner = RecordingRunner(outputs: [
        ProcessOutput(status: 0, stdout: Data(claimJSON.utf8), stderr: Data()),
        ProcessOutput(status: 0, stdout: Data(#"{"acked":["01JENTRY"],"already_acked":[]}"#.utf8), stderr: Data()),
        ProcessOutput(status: 0, stdout: Data(#"{"opened":true}"#.utf8), stderr: Data()),
    ])
    let client = CLIClient(
        executable: URL(fileURLWithPath: "/opt/a2a/bin/a2a"),
        consumerID: "macos:01JCONSUMER",
        runner: runner
    )

    let claim = try client.claim(projectID: "01JPROJECT", limit: 20)
    let lease = try #require(claim.lease)
    let leaseToken = try #require(lease.token)
    _ = try client.ack(lease: leaseToken, entryIDs: ["01JENTRY"])
    try client.open(routeToken: "01JROUTE")

    #expect(runner.invocations == [
        .init(executable: URL(fileURLWithPath: "/opt/a2a/bin/a2a"), arguments: [
            "notifications", "claim", "--channel", "macos",
            "--consumer", "macos:01JCONSUMER", "--project", "01JPROJECT",
            "--limit", "20", "--json",
        ]),
        .init(executable: URL(fileURLWithPath: "/opt/a2a/bin/a2a"), arguments: [
            "notifications", "ack", "--lease", "lease-1",
            "--entry", "01JENTRY", "--json",
        ]),
        .init(executable: URL(fileURLWithPath: "/opt/a2a/bin/a2a"), arguments: [
            "notifications", "open", "01JROUTE", "--json",
        ]),
    ])
}

@Test func clientFailsClosedWhenClaimHasNoLeaseTokenOrEntryIdentity() throws {
    let runner = RecordingRunner(outputs: [
        ProcessOutput(status: 0, stdout: Data("""
        {"lease":{"token":"","expires_at":"x"},"entries":[{
          "schema_version":1,"entry_id":"entry","project_id":"p","space_id":"s",
          "artifact_id":"a","event_id":"e","kind":"note","severity":"ordinary",
          "title":"t","summary":"s","occurred_at":"x","source_revision":"r",
          "coalesce_key":"c","route_token":"route"
        }]}
        """.utf8), stderr: Data()),
        ProcessOutput(status: 0, stdout: Data("""
        {"lease":{"token":"lease","expires_at":"x"},"entries":[{
          "schema_version":1,"entry_id":"","project_id":"p","space_id":"s",
          "artifact_id":"a","event_id":"e","kind":"note","severity":"ordinary",
          "title":"t","summary":"s","occurred_at":"x","source_revision":"r",
          "coalesce_key":"c","route_token":"route"
        }]}
        """.utf8), stderr: Data()),
    ])
    let client = CLIClient(
        executable: URL(fileURLWithPath: "/opt/a2a/bin/a2a"),
        consumerID: "macos:consumer",
        runner: runner
    )

    #expect(throws: AdapterProtocolError.missingLeaseToken) {
        try client.claim(projectID: "project", limit: 20)
    }
    #expect(throws: AdapterProtocolError.missingEntryID) {
        try client.claim(projectID: "project", limit: 20)
    }
}

@Test func clientDecodesAdditiveStatusContract() throws {
    let runner = RecordingRunner(outputs: [
        ProcessOutput(status: 0, stdout: Data("""
        {
          "available_channels":["macos","vscode"],
          "installed_channels":["macos"],
          "project":{"id":"01JP","root":"/tmp/p","channels":["macos"]},
          "level":{"line":"2 actionable","new":1,"urgent":1,"stale":0,"update":null,"severity":"urgent","quiet":false,"route_token":"01JAGGREGATE"},
          "offer":{"state":"enabled","scope":"project","remind_after":null},
          "future_field":"ignored"
        }
        """.utf8), stderr: Data()),
    ])
    let client = CLIClient(
        executable: URL(fileURLWithPath: "/opt/a2a/bin/a2a"),
        consumerID: "macos:consumer",
        runner: runner
    )

    let status = try client.status()
    #expect(status.availableChannels == ["macos", "vscode"])
    #expect(status.level.severity == "urgent")
    #expect(status.level.routeToken == "01JAGGREGATE")
    #expect(runner.invocations[0].arguments == [
        "notifications", "status", "--channel", "macos", "--json",
    ])
}

@Test func clientVersionHandshakeUsesConfiguredExecutable() throws {
    let runner = RecordingRunner(outputs: [
        ProcessOutput(status: 0, stdout: Data("a2a 0.8.0 (abc123)\n".utf8), stderr: Data()),
    ])
    let client = CLIClient(
        executable: URL(fileURLWithPath: "/opt/a2a/bin/a2a"),
        consumerID: "macos:consumer",
        runner: runner
    )

    try client.verifyVersion("0.8.0")

    #expect(runner.invocations == [
        .init(executable: URL(fileURLWithPath: "/opt/a2a/bin/a2a"), arguments: ["version"]),
    ])
}

@Test func clientVersionHandshakeFailsClosedOnMismatch() throws {
    let runner = RecordingRunner(outputs: [
        ProcessOutput(status: 0, stdout: Data("a2a 0.7.0 (old)\n".utf8), stderr: Data()),
    ])
    let client = CLIClient(
        executable: URL(fileURLWithPath: "/opt/a2a/bin/a2a"),
        consumerID: "macos:consumer",
        runner: runner
    )

    #expect(throws: (any Error).self) {
        try client.verifyVersion("0.8.0")
    }
}

@Test func processRunnerOwnsAndBoundsItsChild() throws {
    let runner = FoundationProcessRunner(maximumExecutionTime: 0.05)
    let started = ContinuousClock.now

    #expect(throws: AdapterProtocolError.commandTimedOut) {
        try runner.run(
            executable: URL(fileURLWithPath: "/bin/sleep"),
            arguments: ["5"]
        )
    }
    #expect(started.duration(to: .now) < .seconds(3))
}
