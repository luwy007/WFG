import Foundation
import SwiftUI

@MainActor
class AppState: ObservableObject {
    @Published var isRunning = false
    @Published var proxyEnabled = false
    @Published var selectedNode: String? = nil
    @Published var proxyMode: String = "rule"
    @Published var tunEnabled: Bool = false
    @Published var subscriptions: [Subscription] = []
    @Published var nodes: [ProxyNode] = []
    @Published var rules: [Rule] = []
    @Published var errorMessage: String?
    @Published var isLoading = false
    @Published var pingResults: [String: Int] = [:]
    @Published var isPinging = false
    @Published var geoReady: Bool = true
    @Published var isDownloadingGeo = false

    private func isCancellation(_ error: Error) -> Bool {
        if error is CancellationError {
            return true
        }
        if let engineError = error as? EngineError, case .cancelled = engineError {
            return true
        }
        if let urlError = error as? URLError, urlError.code == .cancelled {
            return true
        }
        return false
    }

    private func showError(_ error: Error, context: String? = nil, full: Bool = false) {
        guard !isCancellation(error) else { return }
        errorMessage = full ? fullErrorMessage(error, context: context) : error.localizedDescription
    }

    private func fullErrorMessage(_ error: Error, context: String? = nil) -> String {
        var parts: [String] = []
        if let context {
            parts.append(context)
        }
        parts.append(error.localizedDescription)

        let reflected = String(reflecting: error)
        if reflected != error.localizedDescription {
            parts.append("底层错误：\(reflected)")
        }
        return parts.joined(separator: "\n\n")
    }

    // MARK: - Status

    func fetchStatus() async {
        do {
            let status: StatusResponse = try await EngineAPI.get("/api/status")
            isRunning = status.running
            proxyEnabled = status.proxyEnabled
            selectedNode = status.selectedNode
            proxyMode = status.proxyMode ?? "rule"
            tunEnabled = status.tunEnabled ?? false
        } catch let e as EngineError {
            if case .engineUnreachable = e { return }
            showError(e)
        } catch {
            showError(error)
        }
    }

    // MARK: - Proxy Control

    func startProxy() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let _: OKResponse = try await EngineAPI.post("/api/proxy/start", body: EmptyBody(), long: true)
            isRunning = true
            proxyEnabled = true
        } catch {
            showError(error)
        }
    }

    func stopProxy() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let _: OKResponse = try await EngineAPI.post("/api/proxy/stop", body: EmptyBody(), long: true)
            isRunning = false
            proxyEnabled = false
        } catch {
            showError(error)
        }
    }

    func startInSystemProxyMode() async {
        isLoading = true
        defer { isLoading = false }
        do {
            struct TunRequest: Encodable { let enabled: Bool }
            if isRunning && !tunEnabled {
                // 已是系统代理模式 → 停止
                let _: OKResponse = try await EngineAPI.post("/api/proxy/stop", body: EmptyBody(), long: true)
                isRunning = false
                proxyEnabled = false
            } else if isRunning && tunEnabled {
                // TUN 模式 → 切换到系统代理（/api/tun 内部会重启）
                let _: OKResponse = try await EngineAPI.post("/api/tun", body: TunRequest(enabled: false), long: true)
                await fetchStatus()
            } else {
                // 未运行 → 设置为系统代理模式后启动
                let _: OKResponse = try await EngineAPI.post("/api/tun", body: TunRequest(enabled: false), long: true)
                let _: OKResponse = try await EngineAPI.post("/api/proxy/start", body: EmptyBody(), long: true)
                await fetchStatus()
            }
        } catch {
            showError(error)
        }
    }

    func startInTunMode() async {
        isLoading = true
        defer { isLoading = false }
        do {
            struct TunRequest: Encodable { let enabled: Bool }
            if isRunning && tunEnabled {
                // 已是 TUN 模式 → 停止
                let _: OKResponse = try await EngineAPI.post("/api/proxy/stop", body: EmptyBody(), long: true)
                isRunning = false
                proxyEnabled = false
            } else if isRunning && !tunEnabled {
                // 系统代理模式 → 切换到 TUN（/api/tun 内部会重启）
                let _: OKResponse = try await EngineAPI.post("/api/tun", body: TunRequest(enabled: true), long: true)
                await fetchStatus()
            } else {
                // 未运行 → 设置 TUN 模式后启动
                let _: OKResponse = try await EngineAPI.post("/api/tun", body: TunRequest(enabled: true), long: true)
                let _: OKResponse = try await EngineAPI.post("/api/proxy/start", body: EmptyBody(), long: true)
                await fetchStatus()
            }
        } catch {
            showError(error)
        }
    }

    func pingWebsites() async {
        isPinging = true
        defer { isPinging = false }
        do {
            let result: [String: Int] = try await EngineAPI.get("/api/ping", long: true)
            pingResults = result
        } catch {
            pingResults = [:]
        }
    }

    func checkGeoStatus() async {
        do {
            let result: GeoStatusResponse = try await EngineAPI.get("/api/geo/status")
            geoReady = result.ready
        } catch {
            // 连接失败时不改变状态，避免误报 Geo 数据缺失
        }
    }

    func downloadGeoData() async {
        isDownloadingGeo = true
        defer { isDownloadingGeo = false }
        do {
            let _: OKResponse = try await EngineAPI.post("/api/geo/update", body: EmptyBody(), long: true)
            geoReady = true
        } catch {
            guard !isCancellation(error) else { return }
            errorMessage = "Geo 数据下载失败，请确保系统代理已启动：\(error.localizedDescription)"
        }
    }

    // MARK: - Subscriptions

    func fetchSubscriptions() async {
        do {
            subscriptions = try await EngineAPI.get("/api/subscriptions")
        } catch let e as EngineError {
            if case .engineUnreachable = e { return }
            showError(e)
        } catch {
            showError(error)
        }
    }

    func addSubscription(name: String, url: String, autoRefresh: Bool, refreshInterval: Int = 60) async {
        isLoading = true
        defer { isLoading = false }
        do {
            let body = AddSubRequest(name: name, url: url, autoRefresh: autoRefresh, refreshInterval: refreshInterval)
            let _: OKResponse = try await EngineAPI.post("/api/subscriptions", body: body, long: true)
            await fetchSubscriptions()
            await fetchNodes()
        } catch {
            showError(error, context: "添加订阅失败：\(name)", full: true)
        }
    }

    func fetchSubLogs(subID: String = "") async -> [SubLogEntry] {
        do {
            let encodedSubID = subID.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? subID
            let entries: [SubLogEntry] = try await EngineAPI.get("/api/subscriptions/logs?sub_id=\(encodedSubID)")
            return entries
        } catch {
            return []
        }
    }

    func clearSubLogs() async {
        do {
            let _: OKResponse = try await EngineAPI.delete("/api/subscriptions/logs")
        } catch {
            showError(error)
        }
    }

    func updateSubscription(id: String, autoRefresh: Bool, refreshInterval: Int) async {
        do {
            let clampedInterval = min(max(refreshInterval, 1), 300)
            let body = UpdateSubRequest(autoRefresh: autoRefresh, refreshInterval: clampedInterval)
            let _: OKResponse = try await EngineAPI.put("/api/subscriptions/\(id)", body: body, long: true)
            await fetchSubscriptions()
        } catch {
            showError(error, context: "保存订阅自动刷新设置失败", full: true)
        }
    }

    func refreshSubscription(id: String) async {
        isLoading = true
        defer { isLoading = false }
        do {
            let _: OKResponse = try await EngineAPI.post("/api/subscriptions/\(id)/refresh", body: EmptyBody(), long: true)
            await fetchSubscriptions()
            await fetchNodes()
        } catch {
            showError(error, context: "订阅更新失败", full: true)
        }
    }

    func deleteSubscription(id: String) async {
        do {
            let _: OKResponse = try await EngineAPI.delete("/api/subscriptions/\(id)")
            await fetchSubscriptions()
            await fetchNodes()
        } catch {
            showError(error)
        }
    }

    // MARK: - Nodes

    func fetchNodes() async {
        do {
            // 引擎在无订阅时可能返回 null，用可选解码容错
            if let result: [ProxyNode] = try? await EngineAPI.get("/api/nodes") {
                nodes = result
            }
        }
    }

    func selectNode(name: String, group: String = "手动选择") async {
        do {
            let body = SelectNodeRequest(group: group, node: name)
            let _: OKResponse = try await EngineAPI.post("/api/nodes/select", body: body)
            selectedNode = name
        } catch {
            showError(error)
        }
    }

    func setTunEnabled(_ enabled: Bool) async {
        isLoading = true
        defer { isLoading = false }
        do {
            struct TunRequest: Encodable { let enabled: Bool }
            let _: OKResponse = try await EngineAPI.post("/api/tun", body: TunRequest(enabled: enabled), long: true)
            tunEnabled = enabled
            // 切换后状态可能变了（系统代理、运行状态），刷新一次
            await fetchStatus()
        } catch {
            showError(error)
        }
    }

    func setProxyMode(_ mode: String) async {
        do {
            struct ModeRequest: Encodable { let mode: String }
            let _: OKResponse = try await EngineAPI.post("/api/proxy/mode", body: ModeRequest(mode: mode))
            proxyMode = mode
        } catch {
            showError(error)
        }
    }

    func testLatency(nodeName: String) async -> Int? {
        guard let encoded = nodeName.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) else { return nil }
        do {
            let result: LatencyResponse = try await EngineAPI.get("/api/nodes/latency?name=\(encoded)", long: true)
            if let index = nodes.firstIndex(where: { $0.name == nodeName }) {
                nodes[index].latency = result.latencyMs
            }
            return result.latencyMs
        } catch {
            showError(error, context: "节点测速失败：\(nodeName)", full: true)
            return nil
        }
    }

    // MARK: - Rules

    func fetchRules() async {
        do {
            rules = try await EngineAPI.get("/api/rules")
        } catch {
            showError(error)
        }
    }
}

// MARK: - Request/Response Models

struct StatusResponse: Codable {
    let running: Bool
    let proxyEnabled: Bool
    let proxyPort: String?
    let selectedNode: String?
    let proxyMode: String?
    let tunEnabled: Bool?
    enum CodingKeys: String, CodingKey {
        case running
        case proxyEnabled = "proxy_enabled"
        case proxyPort = "proxy_port"
        case selectedNode = "selected_node"
        case proxyMode = "proxy_mode"
        case tunEnabled = "tun_enabled"
    }
}

struct OKResponse: Codable {
    let ok: Bool?
    let warn: String?
    let error: String?
}

struct EmptyBody: Codable {}

struct AddSubRequest: Codable {
    let name: String
    let url: String
    let autoRefresh: Bool
    let refreshInterval: Int
    enum CodingKeys: String, CodingKey {
        case name, url
        case autoRefresh = "auto_refresh"
        case refreshInterval = "refresh_interval"
    }
}

struct UpdateSubRequest: Codable {
    let autoRefresh: Bool
    let refreshInterval: Int
    enum CodingKeys: String, CodingKey {
        case autoRefresh = "auto_refresh"
        case refreshInterval = "refresh_interval"
    }
}

struct SelectNodeRequest: Codable {
    let group: String
    let node: String
}

struct GeoStatusResponse: Codable {
    let ready: Bool
    let files: [String: Bool]?
}

struct LatencyResponse: Codable {
    let latencyMs: Int
    enum CodingKeys: String, CodingKey {
        case latencyMs = "latency_ms"
    }
}

struct Subscription: Codable, Identifiable {
    let id: String
    let name: String
    let url: String
    let format: String?
    let updatedAt: String?
    let nodeCount: Int
    let userInfo: UserInfo?
    let autoRefresh: Bool
    let refreshInterval: Int
    enum CodingKeys: String, CodingKey {
        case id, name, url, format
        case updatedAt = "updated_at"
        case nodeCount = "node_count"
        case userInfo = "user_info"
        case autoRefresh = "auto_refresh"
        case refreshInterval = "refresh_interval"
    }
}

struct SubLogEntry: Codable, Identifiable {
    let time: String
    let subID: String
    let subName: String
    let success: Bool
    let message: String
    let nodeCount: Int
    let ips: [String]
    var id: String { time + subID }
    enum CodingKeys: String, CodingKey {
        case time
        case subID = "sub_id"
        case subName = "sub_name"
        case success
        case message
        case nodeCount = "node_count"
        case ips
    }
}

struct UserInfo: Codable {
    let upload: Int64
    let download: Int64
    let total: Int64
    let expire: Int64
}

struct ProxyNode: Codable, Identifiable {
    let id: String
    let name: String
    let type: String
    let subscriptionId: String
    var latency: Int
    enum CodingKeys: String, CodingKey {
        case id, name, type
        case subscriptionId = "subscription_id"
        case latency = "latency_ms"
    }

    var latencyColor: Color {
        if latency < 0 { return .gray }
        if latency == 0 { return .red }
        if latency < 150 { return .green }
        if latency < 300 { return .yellow }
        return .orange
    }

    var latencyText: String {
        if latency < 0 { return "未测试" }
        if latency == 0 { return "超时" }
        return "\(latency)ms"
    }
}

struct Rule: Codable, Identifiable {
    var id: UUID = UUID()
    let type: String
    let payload: String
    let policy: String
    enum CodingKeys: String, CodingKey {
        case type, payload, policy
    }
}
