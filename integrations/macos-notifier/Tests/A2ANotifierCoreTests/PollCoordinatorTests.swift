import Foundation
import Testing
@testable import A2ANotifierCore

private final class StubFeedClient: FeedClient {
    var claims: [ClaimResponse] = []
    var acked: [(String, [String])] = []
    var opened: [String] = []

    func status() throws -> NotificationStatus {
        .fixture
    }

    func claim(projectID: String, limit: Int) throws -> ClaimResponse {
        claims.removeFirst()
    }

    func ack(lease: String, entryIDs: [String]) throws -> AckResponse {
        acked.append((lease, entryIDs))
        return AckResponse(acked: entryIDs, alreadyAcked: [])
    }

    func open(routeToken: String) throws {
        opened.append(routeToken)
    }
}

private final class StubPresenter: NotificationPresenting {
    var rejectedIDs: Set<String> = []
    var presented: [NotificationEnvelope] = []

    func authorization() async -> NotificationAuthorization {
        .authorized
    }

    func present(_ envelope: NotificationEnvelope) async throws {
        if rejectedIDs.contains(envelope.requestID) {
            throw StubError.rejected
        }
        presented.append(envelope)
    }

    enum StubError: Error {
        case rejected
    }
}

@Test func pollAcknowledgesOnlyRequestsAcceptedByThePlatform() async throws {
    let entries = [
        FeedEntry.fixture(id: "accepted", route: "route-a"),
        FeedEntry.fixture(id: "rejected", route: "route-b"),
    ]
    let feed = StubFeedClient()
    feed.claims = [ClaimResponse(lease: .init(token: "lease", expiresAt: "later"), entries: entries)]
    let presenter = StubPresenter()
    presenter.rejectedIDs.insert("rejected")
    let coordinator = PollCoordinator(
        projects: [.init(id: "project", root: "/tmp/project")],
        feed: feed,
        presenter: presenter
    )

    let result = await coordinator.pollOnce()

    #expect(presenter.presented.map(\.requestID) == ["accepted"])
    #expect(feed.acked.count == 1)
    #expect(feed.acked[0].0 == "lease")
    #expect(feed.acked[0].1 == ["accepted"])
    #expect(result == PollResult(claimed: 2, presented: 1, acknowledged: 1))
}

@Test func deniedPermissionDoesNotClaimOrAdvance() async {
    let feed = StubFeedClient()
    let presenter = DeniedPresenter()
    let coordinator = PollCoordinator(
        projects: [.init(id: "project", root: "/tmp/project")],
        feed: feed,
        presenter: presenter
    )

    let result = await coordinator.pollOnce()

    #expect(feed.acked.isEmpty)
    #expect(feed.claims.isEmpty)
    #expect(result == PollResult(claimed: 0, presented: 0, acknowledged: 0))
}

private final class DeniedPresenter: NotificationPresenting {
    func authorization() async -> NotificationAuthorization { .denied }
    func present(_: NotificationEnvelope) async throws {}
}

extension NotificationStatus {
    fileprivate static let fixture = NotificationStatus(
        availableChannels: ["macos"],
        installedChannels: ["macos"],
        project: nil,
        level: .init(line: "", new: 0, urgent: 0, stale: 0, update: nil, severity: "quiet", quiet: true),
        offer: .init(state: "enabled", scope: "project", remindAfter: nil)
    )
}

extension FeedEntry {
    fileprivate static func fixture(id: String, route: String) -> FeedEntry {
        FeedEntry(
            schemaVersion: 1,
            entryID: id,
            projectID: "project",
            spaceID: "space",
            artifactID: "artifact",
            eventID: "event",
            kind: "note",
            severity: "ordinary",
            title: "Title",
            summary: "Summary",
            occurredAt: "now",
            sourceRevision: "rev",
            coalesceKey: "key",
            routeToken: route
        )
    }
}
