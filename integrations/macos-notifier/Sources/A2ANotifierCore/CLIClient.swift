import Foundation

public struct ProcessOutput: Equatable {
    public let status: Int32
    public let stdout: Data
    public let stderr: Data

    public init(status: Int32, stdout: Data, stderr: Data) {
        self.status = status
        self.stdout = stdout
        self.stderr = stderr
    }
}

public protocol ProcessRunning: AnyObject {
    func run(executable: URL, arguments: [String]) throws -> ProcessOutput
    func cancel()
}

public extension ProcessRunning {
    func cancel() {}
}

public final class FoundationProcessRunner: ProcessRunning {
    private let maximumOutputBytes: Int
    private let maximumExecutionTime: TimeInterval
    private let lock = NSLock()
    private var activeProcess: Process?
    private var cancelled = false

    public convenience init(maximumOutputBytes: Int = 1_048_576) {
        self.init(maximumOutputBytes: maximumOutputBytes, maximumExecutionTime: 30)
    }

    public init(maximumOutputBytes: Int = 1_048_576, maximumExecutionTime: TimeInterval) {
        self.maximumOutputBytes = maximumOutputBytes
        self.maximumExecutionTime = maximumExecutionTime
    }

    public func run(executable: URL, arguments: [String]) throws -> ProcessOutput {
        let process = Process()
        process.executableURL = executable
        process.arguments = arguments
        process.currentDirectoryURL = URL(fileURLWithPath: "/", isDirectory: true)

        let stdoutURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("a2a-notifier-\(UUID().uuidString).stdout")
        let stderrURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("a2a-notifier-\(UUID().uuidString).stderr")
        guard FileManager.default.createFile(atPath: stdoutURL.path, contents: nil),
              FileManager.default.createFile(atPath: stderrURL.path, contents: nil)
        else {
            throw CocoaError(.fileWriteUnknown)
        }
        defer {
            try? FileManager.default.removeItem(at: stdoutURL)
            try? FileManager.default.removeItem(at: stderrURL)
        }

        let stdoutHandle = try FileHandle(forWritingTo: stdoutURL)
        let stderrHandle = try FileHandle(forWritingTo: stderrURL)
        defer {
            try? stdoutHandle.close()
            try? stderrHandle.close()
        }
        process.standardOutput = stdoutHandle
        process.standardError = stderrHandle

        let completion = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in completion.signal() }
        let wasCancelled = lock.withLock { () -> Bool in
            if cancelled {
                return true
            }
            activeProcess = process
            return false
        }
        guard !wasCancelled else {
            throw CocoaError(.userCancelled)
        }
        defer { lock.withLock { activeProcess = nil } }
        try process.run()
        if lock.withLock({ cancelled }) {
            process.terminate()
        }
        guard completion.wait(timeout: .now() + maximumExecutionTime) == .success else {
            process.terminate()
            if completion.wait(timeout: .now() + 2) != .success {
                // reason: cancellation is already decided; ESRCH only means the child exited first.
                kill(process.processIdentifier, SIGKILL)
                _ = completion.wait(timeout: .now() + 2)
            }
            throw AdapterProtocolError.commandTimedOut
        }

        let stdout = try Self.boundedRead(stdoutURL, maximumBytes: maximumOutputBytes)
        let stderr = try Self.boundedRead(stderrURL, maximumBytes: maximumOutputBytes)
        return ProcessOutput(status: process.terminationStatus, stdout: stdout, stderr: stderr)
    }

    public func cancel() {
        lock.withLock {
            cancelled = true
            if activeProcess?.isRunning == true {
                activeProcess?.terminate()
            }
        }
    }

    private static func boundedRead(_ url: URL, maximumBytes: Int) throws -> Data {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        let data = try handle.read(upToCount: maximumBytes + 1) ?? Data()
        guard data.count <= maximumBytes else {
            throw AdapterProtocolError.oversizedOutput
        }
        return data
    }
}

public protocol FeedClient: AnyObject {
    func status() throws -> NotificationStatus
    func claim(projectID: String, limit: Int) throws -> ClaimResponse
    func ack(lease: String, entryIDs: [String]) throws -> AckResponse
    func open(routeToken: String) throws
    func cancel()
}

public extension FeedClient {
    func cancel() {}
}

public final class CLIClient: FeedClient {
    private let executable: URL
    private let consumerID: String
    private let runner: ProcessRunning

    public init(executable: URL, consumerID: String, runner: ProcessRunning = FoundationProcessRunner()) {
        self.executable = executable
        self.consumerID = consumerID
        self.runner = runner
    }

    public func status() throws -> NotificationStatus {
        let response: NotificationStatus = try decode(run([
            "notifications", "status", "--channel", "macos", "--json",
        ]))
        if !response.level.quiet {
            guard let token = response.level.routeToken, !token.isEmpty else {
                throw AdapterProtocolError.missingRouteToken
            }
        }
        return response
    }

    public func verifyVersion(_ expectedVersion: String) throws {
        let output = try run(["version"])
        let stamp = String(decoding: output, as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let prefix = "a2a \(expectedVersion)"
        guard stamp == prefix || stamp.hasPrefix(prefix + " ") else {
            throw AdapterProtocolError.commandFailed(
                status: -1,
                message: "configured CLI version mismatch: expected \(expectedVersion)"
            )
        }
    }

    public func claim(projectID: String, limit: Int) throws -> ClaimResponse {
        let response: ClaimResponse = try decode(run([
            "notifications", "claim", "--channel", "macos",
            "--consumer", consumerID, "--project", projectID,
            "--limit", String(min(max(limit, 1), 50)), "--json",
        ]))
        if response.entries.isEmpty {
            guard response.lease == nil else {
                throw AdapterProtocolError.malformedJSON
            }
            return response
        }
        guard let lease = response.lease,
              let token = lease.token,
              !token.isEmpty else {
            throw AdapterProtocolError.missingLeaseToken
        }
        for entry in response.entries {
            guard !entry.entryID.isEmpty else {
                throw AdapterProtocolError.missingEntryID
            }
            guard !entry.routeToken.isEmpty else {
                throw AdapterProtocolError.missingRouteToken
            }
        }
        return response
    }

    public func ack(lease: String, entryIDs: [String]) throws -> AckResponse {
        guard !lease.isEmpty else {
            throw AdapterProtocolError.missingLeaseToken
        }
        guard !entryIDs.isEmpty, entryIDs.allSatisfy({ !$0.isEmpty }) else {
            throw AdapterProtocolError.missingEntryID
        }
        var arguments = ["notifications", "ack", "--lease", lease]
        for entryID in entryIDs {
            arguments.append(contentsOf: ["--entry", entryID])
        }
        arguments.append("--json")
        return try decode(run(arguments))
    }

    public func open(routeToken: String) throws {
        guard !routeToken.isEmpty else {
            throw AdapterProtocolError.missingRouteToken
        }
        guard !containsControlCharacters(routeToken),
              routeToken.utf8.count <= 256
        else {
            throw AdapterProtocolError.invalidRouteToken
        }
        _ = try run(["notifications", "open", routeToken, "--json"])
    }

    public func cancel() {
        runner.cancel()
    }

    private func run(_ arguments: [String]) throws -> Data {
        let output = try runner.run(executable: executable, arguments: arguments)
        guard output.status == 0 else {
            let message = String(decoding: output.stderr.prefix(2_048), as: UTF8.self)
            throw AdapterProtocolError.commandFailed(status: output.status, message: message)
        }
        return output.stdout
    }

    private func decode<T: Decodable>(_ data: Data) throws -> T {
        do {
            return try JSONDecoder.a2a.decode(T.self, from: data)
        } catch {
            throw AdapterProtocolError.malformedJSON
        }
    }
}
