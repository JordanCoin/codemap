import SwiftUI

struct ContentView: View {
    @StateObject var model = UserViewModel()

    var body: some View {
        Text(model.user?.name ?? "")
    }
}
