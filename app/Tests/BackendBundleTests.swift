import Foundation
import XCTest

final class BackendBundleTests: XCTestCase {
    func testBundledBackendIncludesThirdPartyNotices() throws {
        let appBundle = try BuiltAppBundle.load(for: Self.self)
        let url = try XCTUnwrap(appBundle.url(forResource: "THIRD-PARTY-NOTICES", withExtension: "txt"))
        let notices = try String(contentsOf: url, encoding: .utf8)
        XCTAssertTrue(notices.contains("golang.org/x/image v0.46.0"))
        XCTAssertTrue(notices.contains("Copyright 2009 The Go Authors."))
        XCTAssertTrue(notices.contains("github.com/gorilla/websocket v1.5.3"))
    }

    func testDebugAppUsesAnIsolatedBundleIdentifier() throws {
        let appBundle = try BuiltAppBundle.load(for: Self.self)
        let configuration = appBundle.bundleURL.deletingLastPathComponent().lastPathComponent
        try XCTSkipUnless(configuration == "Debug")
        XCTAssertEqual(appBundle.bundleIdentifier, "io.github.aloysk.codexcompanion.debug")
    }

    func testBundledBackendVersionMatchesTheBuiltApp() throws {
        let appBundle = try BuiltAppBundle.load(for: Self.self)
        let executable = try XCTUnwrap(backendExecutableURL(in: appBundle))
        let output = try run(executable, arguments: ["--version"])
        let expected = appBundle.object(
            forInfoDictionaryKey: "CodexTweaksReleaseVersion"
        ) as? String
        XCTAssertEqual(output.trimmingCharacters(in: .whitespacesAndNewlines), expected)
    }

    func testDevelopmentDistributionDoesNotConfigureAnUpdater() throws {
        let appBundle = try BuiltAppBundle.load(for: Self.self)
        XCTAssertEqual(appBundle.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String,
                       "Codex Tweaks Companion")
        XCTAssertNil(appBundle.object(forInfoDictionaryKey: "SUFeedURL"))
        XCTAssertNil(appBundle.object(forInfoDictionaryKey: "SUPublicEDKey"))
        XCTAssertEqual(appBundle.object(forInfoDictionaryKey: "SUEnableAutomaticChecks") as? Bool, false)
        XCTAssertEqual(appBundle.object(forInfoDictionaryKey: "SUAutomaticallyUpdate") as? Bool, false)
    }

    func testBundledBackendIsExecutableAndAnswersPing() throws {
        let appBundle = try BuiltAppBundle.load(for: Self.self)
        let executable = try XCTUnwrap(backendExecutableURL(in: appBundle))
        XCTAssertTrue(FileManager.default.isExecutableFile(atPath: executable.path))

        let process = Process()
        let input = Pipe()
        let output = Pipe()
        let error = Pipe()
        process.executableURL = executable
        process.standardInput = input
        process.standardOutput = output
        process.standardError = error

        try process.run()
        try input.fileHandleForWriting.write(
            contentsOf: Data(#"{"id":1,"method":"ping","params":{}}"#.utf8) + Data([0x0A])
        )
        try input.fileHandleForWriting.close()

        let responseData = output.fileHandleForReading.readDataToEndOfFile()
        let errorData = error.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()

        XCTAssertEqual(
            process.terminationStatus,
            0,
            String(data: errorData, encoding: .utf8) ?? "Go 后端异常退出"
        )
        let response = try JSONDecoder().decode(PingResponse.self, from: responseData)
        XCTAssertEqual(response.id, 1)
        XCTAssertEqual(response.result.protocolVersion, BackendProtocolContract.protocolVersion)
        XCTAssertEqual(response.result.backend, "go")
    }

    private func backendExecutableURL(in appBundle: Bundle) -> URL? {
        appBundle.url(forResource: "codex-tweaks-backend", withExtension: nil)
    }

    private func run(_ executable: URL, arguments: [String]) throws -> String {
        let process = Process()
        let output = Pipe()
        let error = Pipe()
        process.executableURL = executable
        process.arguments = arguments
        process.standardOutput = output
        process.standardError = error
        try process.run()
        let outputData = output.fileHandleForReading.readDataToEndOfFile()
        let errorData = error.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        XCTAssertEqual(
            process.terminationStatus,
            0,
            String(data: errorData, encoding: .utf8) ?? "Go 后端异常退出"
        )
        return String(decoding: outputData, as: UTF8.self)
    }
}

private struct PingResponse: Decodable {
    struct Result: Decodable {
        let protocolVersion: Int
        let backend: String
    }

    let id: Int
    let result: Result
}
