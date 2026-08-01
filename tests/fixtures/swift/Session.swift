import Foundation

/// Demo Swift class covering heritage + call edges for live tests.
public class Session: NSObject, Authenticating, Resettable {
    public var token: String = ""

    public func authenticate() {
        // leaf
    }

    public func reset() {
        token = ""
    }

    public func start() {
        authenticate()
        reset()
        helper.doWork()
    }
}

struct Point: Codable, Equatable {
    var x: Int
    var y: Int
}
