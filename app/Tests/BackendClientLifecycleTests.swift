import Darwin
import Foundation
import XCTest

final class BackendClientLifecycleTests: XCTestCase {
    func testTimeoutRemovesRequestAndLaterReplyDoesNotCompleteItAgain() async throws {
        let client = makeClient(requestTimeout: 0.15)
        try client.launchIfNeeded()
        do {
            let _: BackendAccepted = try await client.request(method: "wait", params: Params())
            XCTFail("Expected request deadline")
        } catch BackendClientError.timedOut {}
        XCTAssertEqual(client.pendingRequestCount, 0)
        let reply: BackendAccepted = try await client.request(method: "ping", params: Params(), timeout: 2)
        XCTAssertEqual(reply.accepted, true)
        XCTAssertEqual(client.pendingRequestCount, 0)
        try await client.stop()
    }

    func testCancellationBeforeAndAfterRegistrationDoesNotLeakContinuations() async throws {
        let client = makeClient()
        try client.launchIfNeeded()
        let registered = Task<BackendAccepted, Error> {
            try await client.request(method: "wait", params: Params())
        }
        try await waitForPendingRequest(client)
        registered.cancel()
        do {
            _ = try await registered.value
            XCTFail("Expected cancellation")
        } catch is CancellationError {}
        XCTAssertEqual(client.pendingRequestCount, 0)

        let immediate = Task<BackendAccepted, Error> {
            try await client.request(method: "wait", params: Params())
        }
        immediate.cancel()
        do {
            _ = try await immediate.value
            XCTFail("Expected early cancellation")
        } catch is CancellationError {}
        let reply: BackendAccepted = try await client.request(method: "ping", params: Params())
        XCTAssertEqual(reply.accepted, true)
        XCTAssertEqual(client.pendingRequestCount, 0)
        try await client.stop()
    }

    func testGoErrorCodesPreserveTimeoutCancellationAndUnsupportedMeaning() async throws {
        let client = makeClient()
        try client.launchIfNeeded()
        do {
            let _: BackendAccepted = try await client.request(method: "server-timeout", params: Params())
            XCTFail("Expected server timeout")
        } catch BackendClientError.timedOut {}
        do {
            let _: BackendAccepted = try await client.request(method: "server-cancelled", params: Params())
            XCTFail("Expected server cancellation")
        } catch is CancellationError {}
        do {
            let _: BackendAccepted = try await client.request(method: "server-unsupported", params: Params())
            XCTFail("Expected unsupported action")
        } catch BackendClientError.unsupported(let message) {
            XCTAssertEqual(message, "Unsupported action")
        }
        try await client.stop()
    }

    func testConcurrentStopDrainsTheFinalResponseBeforeProcessExit() async throws {
        for _ in 0..<5 {
            let client = makeClient()
            try client.launchIfNeeded()
            let _: BackendAccepted = try await client.request(method: "ping", params: Params())
            async let first: Void = client.stop()
            async let second: Void = client.stop()
            try await first
            try await second
            XCTAssertEqual(client.pendingRequestCount, 0)
            XCTAssertNil(client.ownedProcessIdentifier)
        }
    }

    func testBlockedPipeAndUnresponsiveShutdownAreBoundedAndOnlyStopOwnedProcess() async throws {
        let stubborn = makeClient(script: #"trap '' TERM; while :; do :; done"#,
                                  requestTimeout: 0.15, shutdownGrace: 0.6)
        let peer = makeClient()
        try stubborn.launchIfNeeded()
        try peer.launchIfNeeded()
        let ownedPID = try XCTUnwrap(stubborn.ownedProcessIdentifier)
        let _: BackendAccepted = try await peer.request(method: "ping", params: Params())
        do {
            let _: BackendAccepted = try await stubborn.request(
                method: "blocked", params: LargeParams(payload: String(repeating: "x", count: 256 * 1024)))
            XCTFail("Expected blocked-write request to time out")
        } catch BackendClientError.timedOut {}
        let started = ProcessInfo.processInfo.systemUptime
        do {
            try await stubborn.stop()
            XCTFail("Forced termination must not count as confirmed cleanup")
        } catch BackendClientError.shutdownIncomplete {}
        XCTAssertLessThan(ProcessInfo.processInfo.systemUptime - started, 2)
        XCTAssertEqual(stubborn.pendingRequestCount, 0)
        let probe = Darwin.kill(ownedPID, 0)
        let probeError = errno
        XCTAssertEqual(probe, -1)
        XCTAssertEqual(probeError, ESRCH)
        let unaffected: BackendAccepted = try await peer.request(method: "ping", params: Params())
        XCTAssertEqual(unaffected.accepted, true)
        try await peer.stop()
    }

    func testStderrFloodIsDrainedButOnlyCountsReachDiagnostics() async throws {
        let recorder = Diagnostics()
        let script = #"""
        index=0
        while [ "$index" -lt 4096 ]; do
          printf '%s\n' 'synthetic-secret-never-log /tmp/private-synthetic-file' >&2
          index=$((index + 1))
        done
        """# + "\n" + Self.responder
        let client = makeClient(script: script, diagnostics: recorder)
        try client.launchIfNeeded()
        let reply: BackendAccepted = try await client.request(method: "ping", params: Params())
        XCTAssertEqual(reply.accepted, true)
        try await client.stop()
        let events = recorder.entries
        XCTAssertGreaterThan(events.filter { $0.event == "stderr_discarded" }.reduce(UInt64(0)) { $0 + $1.bytes }, 64 * 1024)
        XCTAssertFalse(events.contains { $0.event.contains("synthetic-secret") || $0.event.contains("private-synthetic") })
    }

    func testCleanupFailureRemainsVisibleOnRepeatedStop() async throws {
        let recorder = Diagnostics()
        let client = makeClient(script: Self.cleanupFailureResponder, diagnostics: recorder)
        try client.launchIfNeeded()
        let _: BackendAccepted = try await client.request(method: "ping", params: Params())
        for _ in 0..<2 {
            do {
                try await client.stop()
                XCTFail("Expected unconfirmed cleanup")
            } catch BackendClientError.shutdownIncomplete {
                XCTAssertFalse(BackendClientError.shutdownIncomplete.localizedDescription.contains("synthetic-secret"))
            }
        }
        XCTAssertFalse(recorder.entries.contains { $0.event.contains("synthetic-secret") })
        XCTAssertEqual(client.pendingRequestCount, 0)
    }

    private func makeClient(
        script: String = BackendClientLifecycleTests.responder,
        requestTimeout: TimeInterval = 3,
        shutdownGrace: TimeInterval = 1,
        diagnostics: Diagnostics = Diagnostics()
    ) -> BackendClient {
        let client = BackendClient(
            executableURL: URL(fileURLWithPath: "/bin/sh"), arguments: ["-c", script],
            requestTimeout: requestTimeout, shutdownGrace: shutdownGrace,
            diagnostic: { event, bytes in diagnostics.record(event, bytes) })
        addTeardownBlock {
            // Failure cases assert the result in the test; cleanup still runs
            // when an earlier assertion throws and never starts the real app.
            _ = try? await client.stop()
        }
        return client
    }

    private func waitForPendingRequest(_ client: BackendClient) async throws {
        let deadline = ProcessInfo.processInfo.systemUptime + 2
        while client.pendingRequestCount == 0 && ProcessInfo.processInfo.systemUptime < deadline {
            try await Task.sleep(nanoseconds: 1_000_000)
        }
        XCTAssertEqual(client.pendingRequestCount, 1)
    }

    private struct Params: Encodable, Sendable {}
    private struct LargeParams: Encodable, Sendable { let payload: String }

    private final class Diagnostics: @unchecked Sendable {
        struct Entry { let event: String; let bytes: UInt64 }
        private let lock = NSLock()
        private var stored: [Entry] = []
        var entries: [Entry] { lock.lock(); defer { lock.unlock() }; return stored }
        func record(_ event: String, _ bytes: UInt64) {
            lock.lock()
            stored.append(Entry(event: event, bytes: bytes))
            lock.unlock()
        }
    }

    private static let responder = #"""
    late=""
    while IFS= read -r line; do
      id=${line#*\"id\":}
      id=${id%%,*}
      id=${id%%\}*}
      case "$line" in
        *'"method":"shutdown"'*)
          printf '{"id":%s,"result":{"shutdown":true}}\n' "$id"
          exit 0
          ;;
        *'"method":"wait"'*) late="$id" ;;
        *'"method":"server-timeout"'*)
          printf '{"id":%s,"error":{"code":"timeout","message":"Request timed out"}}\n' "$id" ;;
        *'"method":"server-cancelled"'*)
          printf '{"id":%s,"error":{"code":"cancelled","message":"Request cancelled"}}\n' "$id" ;;
        *'"method":"server-unsupported"'*)
          printf '{"id":%s,"error":{"code":"unsupported","message":"Unsupported action"}}\n' "$id" ;;
        *)
          if [ -n "$late" ]; then
            printf '{"id":%s,"result":{"accepted":true}}\n' "$late"
            printf '{"id":%s,"result":{"accepted":true}}\n' "$late"
            late=""
          fi
          printf '{"id":%s,"result":{"accepted":true}}\n' "$id"
          ;;
      esac
    done
    """#

    private static let cleanupFailureResponder = #"""
    while IFS= read -r line; do
      id=${line#*\"id\":}
      id=${id%%,*}
      id=${id%%\}*}
      case "$line" in
        *'"method":"shutdown"'*)
          printf '{"id":%s,"error":{"code":"request_failed","message":"synthetic-secret cleanup error"}}\n' "$id"
          exit 0
          ;;
        *) printf '{"id":%s,"result":{"accepted":true}}\n' "$id" ;;
      esac
    done
    """#
}
