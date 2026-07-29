import Foundation

public enum ConfigurationError: Error, Equatable {
    case unsupportedSchemaVersion(Int)
    case cliPathNotAbsolute
    case invalidCLIVersion
    case invalidConsumerID
    case invalidProject
    case duplicateProject
}

public struct NotifierConfiguration: Codable, Equatable {
    public static let schemaVersion = 1
    public static let defaultURL = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Application Support/io.a2ahub.notifier/config.json")

    public let cliPath: String
    public let cliVersion: String
    public let consumerID: String
    public let pollIntervalSeconds: Int
    public let projects: [ProjectRegistration]

    public var pollInterval: TimeInterval {
        TimeInterval(min(max(pollIntervalSeconds, 10), 3_600))
    }

    private let schemaVersionValue: Int

    private enum CodingKeys: String, CodingKey {
        case schemaVersionValue = "schema_version"
        case cliPath = "cli_path"
        case cliVersion = "cli_version"
        case consumerID = "consumer_id"
        case pollIntervalSeconds = "poll_interval_seconds"
        case projects
    }

    public static func load(from url: URL = defaultURL, maximumBytes: Int = 256 * 1_024) throws -> NotifierConfiguration {
        let values = try url.resourceValues(forKeys: [.isRegularFileKey])
        guard values.isRegularFile == true else {
            throw AdapterProtocolError.oversizedOutput
        }
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        let data = try handle.read(upToCount: maximumBytes + 1) ?? Data()
        guard data.count <= maximumBytes else {
            throw AdapterProtocolError.oversizedOutput
        }
        return try decode(data)
    }

    public static func decode(_ data: Data) throws -> NotifierConfiguration {
        let config = try JSONDecoder().decode(NotifierConfiguration.self, from: data)
        guard config.schemaVersionValue == schemaVersion else {
            throw ConfigurationError.unsupportedSchemaVersion(config.schemaVersionValue)
        }
        guard config.cliPath.hasPrefix("/") else {
            throw ConfigurationError.cliPathNotAbsolute
        }
        guard isSafe(config.cliVersion, maximumBytes: 128), !config.cliVersion.isEmpty else {
            throw ConfigurationError.invalidCLIVersion
        }
        guard isSafe(config.consumerID, maximumBytes: 256), !config.consumerID.isEmpty else {
            throw ConfigurationError.invalidConsumerID
        }
        var seen = Set<String>()
        for project in config.projects {
            guard isSafe(project.id, maximumBytes: 256),
                  !project.id.isEmpty,
                  project.root.hasPrefix("/"),
                  isSafe(project.root, maximumBytes: 4_096)
            else {
                throw ConfigurationError.invalidProject
            }
            guard seen.insert(project.id).inserted else {
                throw ConfigurationError.duplicateProject
            }
        }
        return config
    }

    public func validateExecutable() throws {
        guard FileManager.default.isExecutableFile(atPath: cliPath) else {
            throw CocoaError(.executableNotLoadable)
        }
    }

    private static func isSafe(_ value: String, maximumBytes: Int) -> Bool {
        value.utf8.count <= maximumBytes &&
            !containsControlCharacters(value)
    }
}
