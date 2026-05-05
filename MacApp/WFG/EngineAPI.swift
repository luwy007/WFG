import Foundation

enum EngineError: LocalizedError {
    case engineUnreachable
    case timeout
    case cancelled
    case http(status: Int, body: String)
    case decode(Error)
    case other(Error)

    var errorDescription: String? {
        switch self {
        case .engineUnreachable:
            return "无法连接 WFG 引擎（127.0.0.1:\(EngineLauncher.apiPort)）。引擎未启动或已崩溃。"
        case .timeout:
            return "引擎响应超时"
        case .cancelled:
            return "请求已取消"
        case .http(let status, let body):
            return "引擎返回错误 \(status)：\(body)"
        case .decode(let err):
            return "解析引擎响应失败：\(err.localizedDescription)"
        case .other(let err):
            return err.localizedDescription
        }
    }
}

enum EngineAPI {
    static let baseURL = "http://127.0.0.1:\(EngineLauncher.apiPort)"

    /// 状态查询 / 列表类用短超时，避免 UI 挂起。
    static let session: URLSession = {
        let cfg = URLSessionConfiguration.default
        cfg.timeoutIntervalForRequest = 3
        cfg.timeoutIntervalForResource = 5
        cfg.waitsForConnectivity = false
        return URLSession(configuration: cfg)
    }()

    /// 启/停代理等"长"操作用：需要给 mihomo 留出下载 GeoData + waitForMihomoAPI 的时间。
    static let longSession: URLSession = {
        let cfg = URLSessionConfiguration.default
        cfg.timeoutIntervalForRequest = 120
        cfg.timeoutIntervalForResource = 180
        cfg.waitsForConnectivity = false
        return URLSession(configuration: cfg)
    }()

    static func get<T: Decodable>(_ path: String, long: Bool = false) async throws -> T {
        let url = URL(string: baseURL + path)!
        let data = try await perform(URLRequest(url: url), long: long)
        return try decode(T.self, from: data)
    }

    static func post<B: Encodable, T: Decodable>(_ path: String, body: B, long: Bool = false) async throws -> T {
        var req = URLRequest(url: URL(string: baseURL + path)!)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(body)
        let data = try await perform(req, long: long)
        return try decode(T.self, from: data)
    }

    static func put<B: Encodable, T: Decodable>(_ path: String, body: B, long: Bool = false) async throws -> T {
        var req = URLRequest(url: URL(string: baseURL + path)!)
        req.httpMethod = "PUT"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(body)
        let data = try await perform(req, long: long)
        return try decode(T.self, from: data)
    }

    static func delete<T: Decodable>(_ path: String) async throws -> T {
        var req = URLRequest(url: URL(string: baseURL + path)!)
        req.httpMethod = "DELETE"
        let data = try await perform(req, long: false)
        return try decode(T.self, from: data)
    }

    // MARK: - Internals

    private static func perform(_ req: URLRequest, long: Bool) async throws -> Data {
        let s = long ? longSession : session
        do {
            let (data, resp) = try await s.data(for: req)
            if let http = resp as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
                let body = String(data: data, encoding: .utf8) ?? ""
                throw EngineError.http(status: http.statusCode, body: body)
            }
            return data
        } catch let e as EngineError {
            throw e
        } catch let urlErr as URLError {
            switch urlErr.code {
            case .cannotConnectToHost, .cannotFindHost, .networkConnectionLost, .notConnectedToInternet:
                throw EngineError.engineUnreachable
            case .timedOut:
                throw EngineError.timeout
            case .cancelled:
                throw EngineError.cancelled
            default:
                throw EngineError.other(urlErr)
            }
        } catch {
            throw EngineError.other(error)
        }
    }

    private static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        do { return try decoder.decode(type, from: data) }
        catch { throw EngineError.decode(error) }
    }

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()
}
