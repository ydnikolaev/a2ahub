import Foundation
import A2ANotifierCore

// OperationQueue requires a Sendable capture. All client access is serialized
// by this owned queue; stop() cancels queued work and the runner itself is locked.
final class ClickRouter: @unchecked Sendable {
    private let client: FeedClient
    private let queue: OperationQueue

    init(client: FeedClient) {
        self.client = client
        queue = OperationQueue()
        queue.name = "io.a2ahub.notifier.click"
        queue.maxConcurrentOperationCount = 1
    }

    func open(_ routeToken: String) {
        queue.addOperation { [weak self] in
            do {
                try self?.client.open(routeToken: routeToken)
            } catch {
                FileHandle.standardError.write(Data("A2A Notifier: click routing failed\n".utf8))
            }
        }
    }

    func stop() {
        queue.cancelAllOperations()
        client.cancel()
    }
}
