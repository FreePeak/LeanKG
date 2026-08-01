import Foundation

/// Demo protocol used by the Session class.
public protocol Authenticating: AnyObject {
    func authenticate()
}

public protocol Resettable {
    func reset()
}
