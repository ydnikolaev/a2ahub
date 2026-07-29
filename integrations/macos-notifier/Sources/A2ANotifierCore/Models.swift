import Foundation

func containsControlCharacters(_ value: String) -> Bool {
    value.unicodeScalars.contains { CharacterSet.controlCharacters.contains($0) }
}

public enum AdapterProtocolError: Error, Equatable {
    case commandFailed(status: Int32, message: String)
    case commandTimedOut
    case oversizedOutput
    case malformedJSON
    case missingLeaseToken
    case missingEntryID
    case missingRouteToken
    case invalidRouteToken
    case unsupportedSchemaVersion(Int)
}

public struct ProjectRegistration: Codable, Equatable {
    public let id: String
    public let root: String

    public init(id: String, root: String) {
        self.id = id
        self.root = root
    }
}

public struct FeedEntry: Codable, Equatable {
    public let schemaVersion: Int
    public let entryID: String
    public let projectID: String
    public let spaceID: String?
    public let artifactID: String?
    public let eventID: String?
    public let kind: String
    public let severity: String
    public let title: String
    public let summary: String
    public let occurredAt: String
    public let sourceRevision: String?
    public let coalesceKey: String
    public let routeToken: String

    private enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"
        case entryID = "entry_id"
        case projectID = "project_id"
        case spaceID = "space_id"
        case artifactID = "artifact_id"
        case eventID = "event_id"
        case kind
        case severity
        case title
        case summary
        case occurredAt = "occurred_at"
        case sourceRevision = "source_revision"
        case coalesceKey = "coalesce_key"
        case routeToken = "route_token"
    }

    public init(
        schemaVersion: Int,
        entryID: String,
        projectID: String,
        spaceID: String?,
        artifactID: String?,
        eventID: String?,
        kind: String,
        severity: String,
        title: String,
        summary: String,
        occurredAt: String,
        sourceRevision: String?,
        coalesceKey: String,
        routeToken: String
    ) {
        self.schemaVersion = schemaVersion
        self.entryID = entryID
        self.projectID = projectID
        self.spaceID = spaceID
        self.artifactID = artifactID
        self.eventID = eventID
        self.kind = kind
        self.severity = severity
        self.title = title
        self.summary = summary
        self.occurredAt = occurredAt
        self.sourceRevision = sourceRevision
        self.coalesceKey = coalesceKey
        self.routeToken = routeToken
    }
}

public struct Lease: Codable, Equatable {
    public let token: String?
    public let expiresAt: String?

    private enum CodingKeys: String, CodingKey {
        case token
        case expiresAt = "expires_at"
    }

    public init(token: String?, expiresAt: String?) {
        self.token = token
        self.expiresAt = expiresAt
    }
}

public struct ClaimResponse: Codable, Equatable {
    public let lease: Lease?
    public let entries: [FeedEntry]

    public init(lease: Lease?, entries: [FeedEntry]) {
        self.lease = lease
        self.entries = entries
    }
}

public struct AckResponse: Codable, Equatable {
    public let acked: [String]
    public let alreadyAcked: [String]

    private enum CodingKeys: String, CodingKey {
        case acked
        case alreadyAcked = "already_acked"
    }

    public init(acked: [String], alreadyAcked: [String]) {
        self.acked = acked
        self.alreadyAcked = alreadyAcked
    }
}

public struct NotificationStatus: Codable, Equatable {
    public struct Project: Codable, Equatable {
        public let id: String
        public let root: String
        public let channels: [String]
    }

    public struct Level: Codable, Equatable {
        public let line: String
        public let new: Int
        public let urgent: Int
        public let stale: Int
        public let update: String?
        public let severity: String
        public let quiet: Bool
        public let routeToken: String?

        private enum CodingKeys: String, CodingKey {
            case line
            case new
            case urgent
            case stale
            case update
            case severity
            case quiet
            case routeToken = "route_token"
        }

        public init(
            line: String,
            new: Int,
            urgent: Int,
            stale: Int,
            update: String?,
            severity: String,
            quiet: Bool,
            routeToken: String? = nil
        ) {
            self.line = line
            self.new = new
            self.urgent = urgent
            self.stale = stale
            self.update = update
            self.severity = severity
            self.quiet = quiet
            self.routeToken = routeToken
        }
    }

    public struct Offer: Codable, Equatable {
        public let state: String
        public let scope: String
        public let remindAfter: String?

        private enum CodingKeys: String, CodingKey {
            case state
            case scope
            case remindAfter = "remind_after"
        }

        public init(state: String, scope: String, remindAfter: String?) {
            self.state = state
            self.scope = scope
            self.remindAfter = remindAfter
        }
    }

    public let availableChannels: [String]
    public let installedChannels: [String]
    public let project: Project?
    public let level: Level
    public let offer: Offer

    private enum CodingKeys: String, CodingKey {
        case availableChannels = "available_channels"
        case installedChannels = "installed_channels"
        case project
        case level
        case offer
    }

    public init(
        availableChannels: [String],
        installedChannels: [String],
        project: Project?,
        level: Level,
        offer: Offer
    ) {
        self.availableChannels = availableChannels
        self.installedChannels = installedChannels
        self.project = project
        self.level = level
        self.offer = offer
    }
}

public enum NotificationSound: Equatable {
    case none
    case warning
}

public struct NotificationEnvelope: Equatable {
    public let requestID: String
    public let title: String
    public let body: String
    public let sound: NotificationSound
    public let routeToken: String

    public init(entry: FeedEntry) throws {
        guard entry.schemaVersion == 1 else {
            throw AdapterProtocolError.unsupportedSchemaVersion(entry.schemaVersion)
        }
        guard Self.isOpaqueToken(entry.entryID) else {
            throw AdapterProtocolError.missingEntryID
        }
        guard !entry.routeToken.isEmpty else {
            throw AdapterProtocolError.missingRouteToken
        }
        guard Self.isOpaqueToken(entry.routeToken) else {
            throw AdapterProtocolError.invalidRouteToken
        }

        requestID = entry.entryID
        title = Self.plainText(entry.title, limit: 120)
        body = Self.plainText(entry.summary, limit: 240)
        sound = Self.isUrgent(entry) ? .warning : .none
        routeToken = entry.routeToken
    }

    private static func isUrgent(_ entry: FeedEntry) -> Bool {
        entry.severity == "urgent" || entry.kind == "gate_pending"
    }

    private static func isOpaqueToken(_ value: String) -> Bool {
        !value.isEmpty &&
            value.utf8.count <= 256 &&
            !containsControlCharacters(value)
    }

    private static func plainText(_ value: String, limit: Int) -> String {
        let scalars = value.unicodeScalars.map { scalar -> Character in
            if CharacterSet.controlCharacters.contains(scalar) {
                return " "
            }
            return Character(String(scalar))
        }
        return String(scalars.prefix(limit))
    }
}

extension JSONDecoder {
    static var a2a: JSONDecoder {
        JSONDecoder()
    }
}
