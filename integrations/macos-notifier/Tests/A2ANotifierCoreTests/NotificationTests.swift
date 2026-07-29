import Foundation
import Testing
@testable import A2ANotifierCore

@Test func notificationUsesStableEntryIDAndBoundedPlainText() throws {
    let entry = FeedEntry(
        schemaVersion: 1,
        entryID: "01JENTRY",
        projectID: "01JPROJECT",
        spaceID: "delivery",
        artifactID: "XW-item",
        eventID: "01JEVENT",
        kind: "gate_pending",
        severity: "urgent",
        title: String(repeating: "x", count: 400) + "\ncommand",
        summary: "safe\u{0000} summary",
        occurredAt: "2026-07-29T10:15:00Z",
        sourceRevision: "abc",
        coalesceKey: "delivery:gate_pending",
        routeToken: "01JROUTE"
    )

    let request = try NotificationEnvelope(entry: entry)
    #expect(request.requestID == "01JENTRY")
    #expect(request.title.count <= 120)
    #expect(!request.title.contains("\n"))
    #expect(!request.body.contains("\u{0000}"))
    #expect(request.sound == .warning)
    #expect(request.routeToken == "01JROUTE")
}

@Test func malformedRouteTokenFailsClosed() throws {
    let entry = FeedEntry(
        schemaVersion: 1,
        entryID: "entry",
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
        routeToken: "bad\nroute"
    )

    #expect(throws: AdapterProtocolError.invalidRouteToken) {
        try NotificationEnvelope(entry: entry)
    }
}
