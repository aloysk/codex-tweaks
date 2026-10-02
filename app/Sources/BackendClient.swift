import Darwin
import Foundation

enum BackendClientError: LocalizedError {
    case executableNotFound
    case notRunning
    case terminated(Int32)
    case malformedResponse
    case transportFailure
    case timedOut
    case shutdownIncomplete
    case unsupported(String)
    case remote(String)

    var errorDescription: String? {
        switch self {
        case .executableNotFound: return PresentationText.resolve(.appBackendMissing)
        case .notRunning: return PresentationText.resolve(.appBackendNotRunning)
        case let .terminated(status):
            return PresentationText.resolve(.appBackendTerminated, replacements: ["status": String(status)])
        case .malformedResponse: return PresentationText.resolve(.appBackendMalformed)
        case .transportFailure: return PresentationText.resolve(.appBackendRequestFailed)
        case .timedOut: return PresentationText.resolve(.appBackendTimedOut)
        case .shutdownIncomplete: return PresentationText.resolve(.appBackendShutdownIncomplete)
        case let .remote(message), let .unsupported(message): return message
        }
    }
}

final class BackendClient: @unchecked Sendable {
    static let shared = BackendClient()

    var stateHandler: (@MainActor @Sendable (BackendAppSnapshot) -> Void)? {
        get { queue.sync { storedStateHandler } }
        set { queue.sync { storedStateHandler = newValue } }
    }

    var pendingRequestCount: Int { queue.sync { pending.count } }
    var ownedProcessIdentifier: Int32? { queue.sync { process?.processIdentifier } }

    private struct PendingRequest {
        let timeout: DispatchWorkItem
        let complete: @Sendable (Swift.Result<Data, Error>) -> Void
    }

    private let queue = DispatchQueue(label: ApplicationIdentity.bundleIdentifier + ".backend")
    private let executableOverride: URL?
    private let arguments: [String]
    private let requestTimeout: TimeInterval
    private let shutdownGrace: TimeInterval
    private let diagnostic: @Sendable (String, UInt64) -> Void
    private var storedStateHandler: (@MainActor @Sendable (BackendAppSnapshot) -> Void)?
    private var process: Process?
    private var inputChannel: DispatchIO?
    private var outputHandle: FileHandle?
    private var errorHandle: FileHandle?
    private var outputBuffer = Data()
    private var outputEnded = false
    private var stderrCounter = BackendStderrCounter()
    private var nextRequestID: Int64 = 1
    private var pending: [Int64: PendingRequest] = [:]
    private var stopping = false
    private var stopTask: Task<Void, Error>?
    private static let maximumFrameBytes = 4 * 1024 * 1024

    init(
        executableURL: URL? = nil,
        arguments: [String] = [],
        requestTimeout: TimeInterval = 30,
        shutdownGrace: TimeInterval = BackendProtocolContract.shutdownGraceSeconds,
        diagnostic: @escaping @Sendable (String, UInt64) -> Void = { event, bytes in
            NSLog("Companion backend event=%@ observedBytes=%llu", event, bytes)
        }
    ) {
        precondition(requestTimeout > 0 && shutdownGrace > 0)
        executableOverride = executableURL
        self.arguments = arguments
        self.requestTimeout = requestTimeout
        self.shutdownGrace = shutdownGrace
        self.diagnostic = diagnostic
    }

    func start(params: BackendInitializeParams) async throws -> BackendAppSnapshot {
        try launchIfNeeded()
        return try await request(method: "initialize", params: params)
    }

    func send<Params: Encodable & Sendable>(method: String, params: Params) async throws {
        let _: BackendAccepted = try await request(method: method, params: params)
    }

    func send(method: String) async throws {
        try await send(method: method, params: EmptyParams())
    }

    func request<Response: Decodable & Sendable, Params: Encodable & Sendable>(
        method: String,
        params: Params,
        timeout: TimeInterval? = nil
    ) async throws -> Response {
        let cancellation = BackendRequestCancellation()
        let requestID = queue.sync { () -> Int64 in
            defer { nextRequestID &+= 1 }
            return nextRequestID
        }
        return try await withTaskCancellationHandler(operation: {
            try Task.checkCancellation()
            return try await withCheckedThrowingContinuation { continuation in
                queue.async { [self] in
                    guard !cancellation.isCancelled else {
                        continuation.resume(throwing: CancellationError())
                        return
                    }
                    registerRequest(id: requestID, method: method, params: params,
                                    timeout: timeout ?? requestTimeout) { result in
                        do {
                            let responseData = try result.get()
                            let response: BackendResponse<Response>
                            do {
                                response = try BackendJSON.makeDecoder().decode(
                                    BackendResponse<Response>.self, from: responseData)
                            } catch {
                                throw BackendClientError.malformedResponse
                            }
                            if let error = response.error {
                                switch error.code {
                                case "timeout": continuation.resume(throwing: BackendClientError.timedOut)
                                case "cancelled": continuation.resume(throwing: CancellationError())
                                case "unsupported": continuation.resume(throwing: BackendClientError.unsupported(error.message))
                                default: continuation.resume(throwing: BackendClientError.remote(error.message))
                                }
                            } else if let result = response.result {
                                continuation.resume(returning: result)
                            } else {
                                continuation.resume(throwing: BackendClientError.malformedResponse)
                            }
                        } catch {
                            continuation.resume(throwing: error)
                        }
                    }
                }
            }
        }, onCancel: { [self] in
            cancellation.cancel()
            queue.async {
                self.finishRequest(requestID, .failure(CancellationError()))
            }
        })
    }

    private func registerRequest<Params: Encodable>(
        id: Int64, method: String, params: Params, timeout: TimeInterval,
        complete: @escaping @Sendable (Swift.Result<Data, Error>) -> Void
    ) {
        guard let inputChannel, process?.isRunning == true, !outputEnded,
              !stopping || method == "shutdown" else {
            complete(.failure(BackendClientError.notRunning))
            return
        }
        let data: Data
        do {
            var encoded = try BackendJSON.makeEncoder().encode(BackendRequest(id: id, method: method, params: params))
            encoded.append(0x0A)
            guard encoded.count <= Self.maximumFrameBytes else {
                complete(.failure(BackendClientError.transportFailure))
                return
            }
            data = encoded
        } catch {
            complete(.failure(BackendClientError.transportFailure))
            return
        }
        let deadline = DispatchWorkItem { [weak self] in
            guard let self, pending[id] != nil else { return }
            diagnostic("request_timeout", 0)
            finishRequest(id, .failure(BackendClientError.timedOut))
        }
        pending[id] = PendingRequest(timeout: deadline, complete: complete)
        queue.asyncAfter(deadline: .now() + timeout, execute: deadline)
        let payload = data.withUnsafeBytes { DispatchData(bytes: $0) }
        // DispatchIO owns nonblocking writes so a sidecar that stops reading
        // cannot block the queue responsible for request deadlines and shutdown.
        inputChannel.write(offset: 0, data: payload, queue: queue) { [weak self] _, _, error in
            guard let self, error != 0, pending[id] != nil else { return }
            diagnostic("stdin_write_failed", 0)
            failPending(BackendClientError.transportFailure)
        }
    }

    private func finishRequest(_ id: Int64, _ result: Swift.Result<Data, Error>) {
        guard let request = pending.removeValue(forKey: id) else { return }
        request.timeout.cancel()
        request.complete(result)
    }

    private func failPending(_ error: Error) {
        for id in Array(pending.keys) {
            finishRequest(id, .failure(error))
        }
    }

    func stop() async throws {
        let operation = queue.sync { () -> Task<Void, Error> in
            if let stopTask { return stopTask }
            stopping = true
            let owned = process
            let task = Task { [self] in try await stopOwnedProcess(owned) }
            stopTask = task
            return task
        }
        try await operation.value
    }

    private func stopOwnedProcess(_ owned: Process?) async throws {
        guard let owned else { return }
        let deadline = ProcessInfo.processInfo.systemUptime + shutdownGrace
        let forceReserve = min(0.5, shutdownGrace / 2)
        var cleanupConfirmed = false
        do {
            let result: BackendAccepted = try await request(
                method: "shutdown", params: EmptyParams(), timeout: shutdownGrace - forceReserve)
            cleanupConfirmed = result.shutdown == true
        } catch {
            // The final error describes unconfirmed cleanup without logging a
            // server message, filesystem path or raw transport error.
            cleanupConfirmed = false
        }
        queue.sync { inputChannel?.close(flags: .stop) }
        await waitForExit(owned, until: deadline - forceReserve)
        var forced = false
        if owned.isRunning {
            forced = signalOwnedProcess(owned, force: false)
            await waitForExit(owned, until: deadline - min(0.1, forceReserve / 2))
        }
        if owned.isRunning {
            forced = signalOwnedProcess(owned, force: true) || forced
            await waitForExit(owned, until: deadline)
        }
        let exitedCleanly = !owned.isRunning && owned.terminationStatus == 0
        queue.sync {
            if process === owned { closeProcess() }
        }
        if !cleanupConfirmed || !exitedCleanly || forced {
            diagnostic("shutdown_unconfirmed", 0)
            throw BackendClientError.shutdownIncomplete
        }
    }

    private func waitForExit(_ owned: Process, until deadline: TimeInterval) async {
        while owned.isRunning {
            let remaining = deadline - ProcessInfo.processInfo.systemUptime
            guard remaining > 0 else { return }
            await withCheckedContinuation { continuation in
                queue.asyncAfter(deadline: .now() + min(0.02, remaining)) {
                    continuation.resume()
                }
            }
        }
    }

    private func signalOwnedProcess(_ owned: Process, force: Bool) -> Bool {
        queue.sync {
            guard process === owned, owned.isRunning, owned.processIdentifier > 0 else { return false }
            // This Process was created by this client. Never signal a name,
            // process group, descendant tree, or the official Codex process.
            if Darwin.kill(owned.processIdentifier, force ? SIGKILL : SIGTERM) != 0 {
                diagnostic("sidecar_signal_failed", 0)
                return false
            }
            diagnostic("sidecar_termination_requested", 0)
            return true
        }
    }

    func launchIfNeeded() throws {
        try queue.sync {
            guard !stopping else { throw BackendClientError.notRunning }
            guard process?.isRunning != true else { return }
            guard let executableURL = executableOverride ?? Self.backendExecutableURL() else {
                throw BackendClientError.executableNotFound
            }
            let owned = Process()
            let input = Pipe()
            let output = Pipe()
            let stderr = Pipe()
            let counter = BackendStderrCounter()
            owned.executableURL = executableURL
            owned.arguments = arguments
            owned.standardInput = input
            owned.standardOutput = output
            owned.standardError = stderr
            owned.terminationHandler = { [weak self] terminated in
                self?.queue.async {
                    guard let self, process === terminated else { return }
                    // stdout may still contain the final shutdown response.
                    // Its EOF handler drains that response before failing requests.
                    if outputEnded { closeProcess() }
                }
            }
            do {
                try owned.run()
            } catch {
                diagnostic("sidecar_launch_failed", 0)
                throw BackendClientError.transportFailure
            }
            process = owned
            outputEnded = false
            stderrCounter = counter
            let writer = input.fileHandleForWriting
            inputChannel = DispatchIO(type: .stream, fileDescriptor: writer.fileDescriptor, queue: queue) { [diagnostic] _ in
                do { try writer.close() }
                catch { diagnostic("stdin_close_failed", 0) }
            }
            outputHandle = output.fileHandleForReading
            errorHandle = stderr.fileHandleForReading
            output.fileHandleForReading.readabilityHandler = { [weak self, weak owned] handle in
                do {
                    let data = try handle.read(upToCount: 64 * 1024) ?? Data()
                    if data.isEmpty { handle.readabilityHandler = nil }
                    self?.queue.async {
                        guard let self, let owned, process === owned else { return }
                        consumeOutput(data)
                    }
                } catch {
                    self?.queue.async {
                        guard let self, let owned, process === owned else { return }
                        diagnostic("stdout_read_failed", 0)
                        outputEnded = true
                        failPending(BackendClientError.transportFailure)
                    }
                }
            }
            stderr.fileHandleForReading.readabilityHandler = { [diagnostic] handle in
                do {
                    let data = try handle.read(upToCount: 4096) ?? Data()
                    if data.isEmpty { handle.readabilityHandler = nil }
                    else { counter.add(data.count) }
                } catch {
                    handle.readabilityHandler = nil
                    diagnostic("stderr_read_failed", 0)
                }
            }
        }
    }

    private func consumeOutput(_ data: Data) {
        guard !data.isEmpty else {
            outputEnded = true
            outputHandle?.readabilityHandler = nil
            failPending(outputBuffer.isEmpty ? BackendClientError.transportFailure : BackendClientError.malformedResponse)
            if process?.isRunning == false { closeProcess() }
            return
        }
        outputBuffer.append(data)
        while let newline = outputBuffer.firstIndex(of: 0x0A) {
            let line = Data(outputBuffer[..<newline])
            outputBuffer.removeSubrange(...newline)
            guard line.count <= Self.maximumFrameBytes else {
                failPending(BackendClientError.malformedResponse)
                outputEnded = true
                outputHandle?.readabilityHandler = nil
                return
            }
            if !line.isEmpty { handleLine(line) }
        }
        if outputBuffer.count > Self.maximumFrameBytes {
            outputBuffer.removeAll(keepingCapacity: false)
            outputEnded = true
            outputHandle?.readabilityHandler = nil
            failPending(BackendClientError.malformedResponse)
        }
    }

    private func handleLine(_ data: Data) {
        guard let header = try? BackendJSON.makeDecoder().decode(BackendMessageHeader.self, from: data) else { return }
        if header.event == "state", !stopping,
           let message = try? BackendJSON.makeDecoder().decode(BackendStateEvent.self, from: data) {
            let handler = storedStateHandler
            Task { @MainActor in handler?(message.data) }
            return
        }
        if let id = header.id { finishRequest(id, .success(data)) }
    }

    private func closeProcess() {
        failPending(BackendClientError.notRunning)
        inputChannel?.close(flags: .stop)
        inputChannel = nil
        for handle in [outputHandle, errorHandle].compactMap({ $0 }) {
            handle.readabilityHandler = nil
            do { try handle.close() }
            catch { diagnostic("output_close_failed", 0) }
        }
        diagnostic("stderr_discarded", stderrCounter.byteCount)
        outputHandle = nil
        errorHandle = nil
        process?.terminationHandler = nil
        process = nil
        outputBuffer.removeAll(keepingCapacity: false)
    }

    private static func backendExecutableURL() -> URL? {
        if let override = ProcessInfo.processInfo.environment[ApplicationIdentity.environmentPrefix + "BACKEND_PATH"],
           FileManager.default.isExecutableFile(atPath: override) {
            return URL(fileURLWithPath: override)
        }
        return Bundle.main.url(forResource: "codex-tweaks-backend", withExtension: nil)
    }
}

private final class BackendRequestCancellation: @unchecked Sendable {
    private let lock = NSLock()
    private var cancelled = false
    var isCancelled: Bool { lock.lock(); defer { lock.unlock() }; return cancelled }
    func cancel() { lock.lock(); cancelled = true; lock.unlock() }
}

private final class BackendStderrCounter: @unchecked Sendable {
    private let lock = NSLock()
    private var bytes: UInt64 = 0
    var byteCount: UInt64 { lock.lock(); defer { lock.unlock() }; return bytes }
    func add(_ count: Int) {
        lock.lock()
        defer { lock.unlock() }
        let addition = bytes.addingReportingOverflow(UInt64(count))
        bytes = addition.overflow ? UInt64.max : addition.partialValue
    }
}

private struct EmptyParams: Encodable, Sendable {}
private struct BackendRequest<Params: Encodable>: Encodable {
    let id: Int64
    let method: String
    let params: Params
}
private struct BackendMessageHeader: Decodable {
    let id: Int64?
    let event: String?
}
private struct BackendStateEvent: Decodable {
    let event: String
    let data: BackendAppSnapshot
}
private struct BackendErrorPayload: Decodable {
    let code: String
    let message: String
}
private struct BackendResponse<Response: Decodable>: Decodable {
    let id: Int64
    let result: Response?
    let error: BackendErrorPayload?
}
