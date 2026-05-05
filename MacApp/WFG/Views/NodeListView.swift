import SwiftUI

struct NodeListView: View {
    @EnvironmentObject var state: AppState
    @State private var searchText = ""
    @State private var testingAll = false

    var filteredNodes: [ProxyNode] {
        if searchText.isEmpty { return state.nodes }
        return state.nodes.filter {
            $0.name.localizedCaseInsensitiveContains(searchText)
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            // 搜索 + 测速
            HStack(spacing: 8) {
                HStack {
                    Image(systemName: "magnifyingglass")
                        .foregroundColor(.secondary)
                        .font(.caption)
                    TextField("搜索节点", text: $searchText)
                        .textFieldStyle(.plain)
                        .font(.system(size: 13))
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 5)
                .background(Color(NSColor.controlBackgroundColor), in: RoundedRectangle(cornerRadius: 6))

                Button {
                    Task { await testAllLatency() }
                } label: {
                    if testingAll {
                        ProgressView().scaleEffect(0.6)
                    } else {
                        Image(systemName: "speedometer")
                    }
                }
                .buttonStyle(.plain)
                .disabled(testingAll)
                .help("批量测速")
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)

            Divider()

            if state.nodes.isEmpty {
                ContentUnavailableView("暂无节点", systemImage: "server.rack",
                    description: Text("请先在「订阅」页面添加机场订阅"))
            } else {
                List(filteredNodes) { node in
                    NodeRowView(node: node)
                        .listRowInsets(EdgeInsets(top: 2, leading: 8, bottom: 2, trailing: 8))
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
        .task { await state.fetchNodes() }
    }

    func testAllLatency() async {
        testingAll = true
        defer { testingAll = false }
        for node in state.nodes {
            _ = await state.testLatency(nodeName: node.name)
        }
        await state.fetchNodes()
    }
}

struct NodeRowView: View {
    @EnvironmentObject var state: AppState
    let node: ProxyNode
    @State private var isTesting = false

    var body: some View {
        HStack(spacing: 10) {
            // 协议标签
            Text(node.type.uppercased())
                .font(.system(size: 9, weight: .bold))
                .foregroundColor(.white)
                .padding(.horizontal, 5)
                .padding(.vertical, 2)
                .background(protocolColor(node.type), in: RoundedRectangle(cornerRadius: 3))

            Text(node.name)
                .font(.system(size: 12))
                .lineLimit(1)
                .truncationMode(.middle)

            Spacer()

            // 延迟
            if isTesting {
                ProgressView().scaleEffect(0.5).frame(width: 40)
            } else {
                Text(node.latencyText)
                    .font(.system(size: 11, weight: .medium))
                    .foregroundColor(node.latencyColor)
                    .frame(width: 44, alignment: .trailing)
            }

            // 选择按钮
            Button {
                Task { await state.selectNode(name: node.name) }
            } label: {
                let isSelected = state.selectedNode == node.name
                Image(systemName: isSelected ? "checkmark.circle.fill" : "checkmark.circle")
                    .font(.system(size: 14))
                    .foregroundColor(isSelected ? .accentColor : .secondary)
            }
            .buttonStyle(.plain)
            .help("选择此节点")

            // 单独测速
            Button {
                Task {
                    isTesting = true
                    _ = await state.testLatency(nodeName: node.name)
                    await state.fetchNodes()
                    isTesting = false
                }
            } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .disabled(isTesting)
            .help("测速")
        }
        .padding(.vertical, 4)
    }

    func protocolColor(_ type: String) -> Color {
        switch type.lowercased() {
        case "vmess":  return .blue
        case "vless":  return .purple
        case "ss":     return .orange
        case "trojan": return .red
        default:       return .gray
        }
    }
}
