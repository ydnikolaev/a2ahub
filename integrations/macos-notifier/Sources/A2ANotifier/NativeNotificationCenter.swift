import Foundation
import UserNotifications
import A2ANotifierCore

final class NativeNotificationCenter: NSObject, NotificationPresenting, UNUserNotificationCenterDelegate {
    static let openActionIdentifier = "OPEN_A2A"
    static let categoryIdentifier = "A2A_ACTIONABLE"
    static let routeTokenKey = "route_token"

    private let center: UNUserNotificationCenter
    private let routeHandler: (String) -> Void

    init(
        center: UNUserNotificationCenter = .current(),
        routeHandler: @escaping (String) -> Void
    ) {
        self.center = center
        self.routeHandler = routeHandler
        super.init()
        center.delegate = self
        let open = UNNotificationAction(
            identifier: Self.openActionIdentifier,
            title: "Open A2A",
            options: [.foreground]
        )
        center.setNotificationCategories([
            UNNotificationCategory(
                identifier: Self.categoryIdentifier,
                actions: [open],
                intentIdentifiers: [],
                options: []
            ),
        ])
    }

    func authorization() async -> NotificationAuthorization {
        let settings = await center.notificationSettings()
        switch settings.authorizationStatus {
        case .authorized, .provisional, .ephemeral:
            return .authorized
        case .denied:
            return .denied
        case .notDetermined:
            return .notDetermined
        @unknown default:
            return .denied
        }
    }

    func requestAuthorization() async throws -> Bool {
        try await center.requestAuthorization(options: [.alert, .sound])
    }

    func present(_ envelope: NotificationEnvelope) async throws {
        let content = UNMutableNotificationContent()
        content.title = envelope.title
        content.body = envelope.body
        content.categoryIdentifier = Self.categoryIdentifier
        content.userInfo = [Self.routeTokenKey: envelope.routeToken]
        if envelope.sound == .warning {
            content.sound = .default
        }
        let request = UNNotificationRequest(
            identifier: envelope.requestID,
            content: content,
            trigger: nil
        )
        try await center.add(request)
    }

    func presentReadyTest() async throws {
        let content = UNMutableNotificationContent()
        content.title = "A2A notifications are ready"
        content.body = "Native delivery is enabled on this Mac."
        // The synthetic readiness check has no project route and therefore
        // deliberately exposes no Open A2A action.
        let request = UNNotificationRequest(
            identifier: "a2a-ready-test",
            content: content,
            trigger: nil
        )
        try await center.add(request)
    }

    func userNotificationCenter(
        _: UNUserNotificationCenter,
        willPresent _: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .list, .sound]
    }

    func userNotificationCenter(
        _: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse
    ) async {
        guard response.actionIdentifier == UNNotificationDefaultActionIdentifier ||
                response.actionIdentifier == Self.openActionIdentifier,
              let routeToken = response.notification.request.content
                .userInfo[Self.routeTokenKey] as? String,
              !routeToken.isEmpty,
              routeToken.utf8.count <= 256,
              !routeToken.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) })
        else {
            return
        }
        routeHandler(routeToken)
    }
}
