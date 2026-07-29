import Foundation
import Testing
@testable import A2ANotifierCore

@Test func configurationRequiresAbsoluteExecutableCLI() throws {
    let data = Data("""
    {
      "schema_version": 1,
      "cli_path": "a2a",
      "cli_version": "0.8.0",
      "consumer_id": "macos:01JTEST",
      "poll_interval_seconds": 60,
      "projects": [{"id":"01JPROJECT","root":"/tmp/project"}]
    }
    """.utf8)

    #expect(throws: ConfigurationError.cliPathNotAbsolute) {
        try NotifierConfiguration.decode(data)
    }
}

@Test func configurationRejectsUnsafeAndUnboundedFields() throws {
    let data = Data("""
    {
      "schema_version": 1,
      "cli_path": "/usr/local/bin/a2a",
      "cli_version": "0.8.0",
      "consumer_id": "macos:\\u000aBAD",
      "poll_interval_seconds": 1,
      "projects": [{"id":"","root":"relative"}]
    }
    """.utf8)

    #expect(throws: ConfigurationError.invalidConsumerID) {
        try NotifierConfiguration.decode(data)
    }
}

@Test func configurationLoadsRegisteredProjectsAndBoundsInterval() throws {
    let data = Data("""
    {
      "schema_version": 1,
      "cli_path": "/usr/local/bin/a2a",
      "cli_version": "0.8.0",
      "consumer_id": "macos:01JTEST",
      "poll_interval_seconds": 2,
      "projects": [
        {"id":"01JPROJECTA","root":"/tmp/a"},
        {"id":"01JPROJECTB","root":"/tmp/b"}
      ]
    }
    """.utf8)

    let config = try NotifierConfiguration.decode(data)
    #expect(config.pollInterval == 10)
    #expect(config.projects.map(\.id) == ["01JPROJECTA", "01JPROJECTB"])
}
