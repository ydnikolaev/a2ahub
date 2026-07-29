import AppKit
import Foundation
import A2ANotifierCore

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let service: PollingService
    private let clickRouter: ClickRouter
    private let notificationCenter: NativeNotificationCenter

    init(configuration: NotifierConfiguration) {
        let pollClient = CLIClient(
            executable: URL(fileURLWithPath: configuration.cliPath),
            consumerID: configuration.consumerID
        )
        let clickClient = CLIClient(
            executable: URL(fileURLWithPath: configuration.cliPath),
            consumerID: configuration.consumerID
        )
        clickRouter = ClickRouter(client: clickClient)

        var center: NativeNotificationCenter!
        center = NativeNotificationCenter { [weak clickRouter] token in
            clickRouter?.open(token)
        }
        notificationCenter = center
        let coordinator = PollCoordinator(
            projects: configuration.projects,
            feed: pollClient,
            presenter: center
        )
        service = PollingService(
            coordinator: coordinator,
            intervalSeconds: configuration.pollInterval,
            cancellation: { pollClient.cancel() }
        )
        super.init()
    }

    func applicationDidFinishLaunching(_: Notification) {
        service.start()
    }

    func applicationWillTerminate(_: Notification) {
        service.stop()
        clickRouter.stop()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_: NSApplication) -> Bool {
        false
    }
}
