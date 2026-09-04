import Foundation
import SwiftUI

// References User, declared in Models.swift, with no import statement:
// same-module Swift files never import each other, so no file-level edge
// exists for the graph to find.
final class UserViewModel: ObservableObject {
    @Published var user: User?

    func load() {
        user = User(id: "1", name: "ada")
    }
}
