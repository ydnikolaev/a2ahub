import AppKit
import Foundation
import ServiceManagement
import A2ANotifierCore

private let appProductVersion =
    Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"
private let appProtocolVersion = 1

private enum Mode {
    case run
    case install
    case unregister
    case status
    case test
}

private struct Arguments {
    var mode: Mode = .run
    var configurationURL = NotifierConfiguration.defaultURL

    init(_ values: [String]) throws {
        var index = 0
        while index < values.count {
            switch values[index] {
            case "--run":
                mode = .run
            case "--install":
                mode = .install
            case "--unregister":
                mode = .unregister
            case "--status":
                mode = .status
            case "--test":
                mode = .test
            case "--config":
                index += 1
                guard index < values.count else {
                    throw NSError(domain: "io.a2ahub.notifier.arguments", code: 2)
                }
                configurationURL = URL(fileURLWithPath: values[index])
            default:
                throw NSError(domain: "io.a2ahub.notifier.arguments", code: 2)
            }
            index += 1
        }
    }
}

private struct CompanionStatus: Encodable {
    let bundleID = "io.a2ahub.notifier"
    let productVersion = appProductVersion
    let protocolVersion = appProtocolVersion
    let permission: String
    let loginItem: String
    let cliPath: String
    let cliVersion: String
    let cliHandshake: String
}

private func loginItemStatus() -> String {
    switch SMAppService.mainApp.status {
    case .enabled:
        return "enabled"
    case .notRegistered:
        return "not-registered"
    case .requiresApproval:
        return "approval-required"
    case .notFound:
        return "not-found"
    @unknown default:
        return "unknown"
    }
}

private func permissionStatus(_ center: NativeNotificationCenter) async -> String {
    await center.authorization().rawValue
}

private func emitJSON<T: Encodable>(_ value: T) {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.sortedKeys]
    encoder.keyEncodingStrategy = .convertToSnakeCase
    let data: Data
    do {
        data = try encoder.encode(value)
    } catch {
        FileHandle.standardError.write(Data("A2A Notifier: could not encode status\n".utf8))
        return
    }
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data([0x0a]))
}

@MainActor
private func execute() async -> Int32 {
    let arguments: Arguments
    let config: NotifierConfiguration
    do {
        arguments = try Arguments(Array(CommandLine.arguments.dropFirst()))
        config = try NotifierConfiguration.load(from: arguments.configurationURL)
        if arguments.mode != .unregister {
            try config.validateExecutable()
        }
    } catch {
        FileHandle.standardError.write(Data("A2A Notifier: invalid configuration: \(error)\n".utf8))
        return 2
    }

    let client = CLIClient(
        executable: URL(fileURLWithPath: config.cliPath),
        consumerID: config.consumerID
    )
    let clickRouter = ClickRouter(client: client)
    let center = NativeNotificationCenter { routeToken in
        clickRouter.open(routeToken)
    }

    if arguments.mode != .unregister && arguments.mode != .status {
        do {
            try client.verifyVersion(config.cliVersion)
        } catch {
            FileHandle.standardError.write(Data("A2A Notifier: CLI handshake failed: \(error)\n".utf8))
            return 3
        }
    }

    switch arguments.mode {
    case .run:
        let delegate = AppDelegate(configuration: config)
        let application = NSApplication.shared
        application.setActivationPolicy(.accessory)
        application.delegate = delegate
        application.run()
        withExtendedLifetime(delegate) {}
        return 0

    case .install:
        do {
            let permission: NotificationAuthorization
            if await center.authorization() == .notDetermined {
                // reason: the settings read below is authoritative even if the bool races a settings change.
                _ = try await center.requestAuthorization()
            }
            permission = await center.authorization()
            guard permission == .authorized else {
                emitJSON([
                    "permission": permission.rawValue,
                    "login_item": loginItemStatus(),
                    "system_settings": "x-apple.systempreferences:com.apple.Notifications-Settings.extension",
                ])
                return 4
            }
            if SMAppService.mainApp.status == .notRegistered {
                try SMAppService.mainApp.register()
            }
            emitJSON([
                "permission": permission.rawValue,
                "login_item": loginItemStatus(),
            ])
            return 0
        } catch {
            FileHandle.standardError.write(Data("A2A Notifier: install failed: \(error)\n".utf8))
            return 5
        }

    case .unregister:
        do {
            if SMAppService.mainApp.status != .notRegistered {
                try await SMAppService.mainApp.unregister()
            }
            emitJSON(["login_item": loginItemStatus()])
            return 0
        } catch {
            FileHandle.standardError.write(Data("A2A Notifier: unregister failed: \(error)\n".utf8))
            return 5
        }

    case .status:
        var handshake = "ok"
        do {
            try client.verifyVersion(config.cliVersion)
        } catch {
            handshake = "mismatch"
        }
        emitJSON(CompanionStatus(
            permission: await permissionStatus(center),
            loginItem: loginItemStatus(),
            cliPath: config.cliPath,
            cliVersion: config.cliVersion,
            cliHandshake: handshake
        ))
        return handshake == "ok" ? 0 : 3

    case .test:
        guard await center.authorization() == .authorized else {
            FileHandle.standardError.write(
                Data("A2A Notifier: notifications are not authorized; open System Settings > Notifications.\n".utf8)
            )
            return 4
        }
        do {
            try await center.presentReadyTest()
            emitJSON(["delivered": 1])
            return 0
        } catch {
            FileHandle.standardError.write(
                Data("A2A Notifier: readiness notification failed: \(error)\n".utf8)
            )
            return 5
        }
    }
}

Task { @MainActor in
    exit(await execute())
}
dispatchMain()
