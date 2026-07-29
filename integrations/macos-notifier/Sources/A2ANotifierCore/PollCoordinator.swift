import Foundation

public enum NotificationAuthorization: String, Codable {
    case authorized
    case denied
    case notDetermined = "not-determined"
}

public protocol NotificationPresenting: AnyObject {
    func authorization() async -> NotificationAuthorization
    func present(_ envelope: NotificationEnvelope) async throws
}

public struct PollResult: Equatable {
    public let claimed: Int
    public let presented: Int
    public let acknowledged: Int

    public init(claimed: Int, presented: Int, acknowledged: Int) {
        self.claimed = claimed
        self.presented = presented
        self.acknowledged = acknowledged
    }
}

public actor PollCoordinator {
    private let projects: [ProjectRegistration]
    private let feed: FeedClient
    private let presenter: NotificationPresenting
    private let limit: Int

    public init(
        projects: [ProjectRegistration],
        feed: FeedClient,
        presenter: NotificationPresenting,
        limit: Int = 20
    ) {
        self.projects = projects
        self.feed = feed
        self.presenter = presenter
        self.limit = min(max(limit, 1), 50)
    }

    @discardableResult
    public func pollOnce() async -> PollResult {
        guard await presenter.authorization() == .authorized else {
            return PollResult(claimed: 0, presented: 0, acknowledged: 0)
        }

        var claimedCount = 0
        var presentedCount = 0
        var acknowledgedCount = 0
        for project in projects {
            if Task.isCancelled {
                break
            }
            do {
                let claim = try feed.claim(projectID: project.id, limit: limit)
                claimedCount += claim.entries.count
                var accepted: [String] = []
                for entry in claim.entries {
                    do {
                        let envelope = try NotificationEnvelope(entry: entry)
                        try await presenter.present(envelope)
                        accepted.append(entry.entryID)
                        presentedCount += 1
                    } catch {
                        // An unaccepted platform request remains leased and is redelivered.
                    }
                }
                if !accepted.isEmpty, let lease = claim.lease, let token = lease.token {
                    // reason: successful decoding is enough; the core owns idempotent ack state.
                    _ = try feed.ack(lease: token, entryIDs: accepted)
                    acknowledgedCount += accepted.count
                }
            } catch {
                // A project failure is isolated; the next poll retries without cursor loss.
                continue
            }
        }
        return PollResult(
            claimed: claimedCount,
            presented: presentedCount,
            acknowledged: acknowledgedCount
        )
    }

}

public final class PollingService {
    private let coordinator: PollCoordinator
    private let interval: Duration
    private let cancellation: () -> Void
    private let lock = NSLock()
    private var worker: Task<Void, Never>?

    public init(
        coordinator: PollCoordinator,
        intervalSeconds: TimeInterval,
        cancellation: @escaping () -> Void
    ) {
        self.coordinator = coordinator
        interval = .seconds(max(10, intervalSeconds))
        self.cancellation = cancellation
    }

    public func start() {
        lock.withLock {
            guard worker == nil else {
                return
            }
            worker = Task { [coordinator, interval] in
                while !Task.isCancelled {
                    _ = await coordinator.pollOnce()
                    do {
                        try await Task.sleep(for: interval)
                    } catch {
                        break
                    }
                }
            }
        }
    }

    public func stop() {
        let active = lock.withLock { () -> Task<Void, Never>? in
            let active = worker
            worker = nil
            return active
        }
        active?.cancel()
        cancellation()
    }
}
