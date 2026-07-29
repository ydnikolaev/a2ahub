import Foundation
import Testing
@testable import A2ANotifierCore

@Test func configurationLoadIsSizeBounded() throws {
    let url = FileManager.default.temporaryDirectory
        .appendingPathComponent("a2a-notifier-config-\(UUID().uuidString).json")
    try Data(repeating: 0x78, count: 33).write(to: url, options: .atomic)
    defer { try? FileManager.default.removeItem(at: url) }

    #expect(throws: AdapterProtocolError.oversizedOutput) {
        try NotifierConfiguration.load(from: url, maximumBytes: 32)
    }
}
