import Foundation
import XCTest

final class AppearanceContractTests: XCTestCase {
    func testGoSnapshotKeepsSavedPreviewAndCapabilitiesDistinct() throws {
        let snapshot = try BackendJSON.makeDecoder().decode(
            BackendAppearanceSnapshot.self, from: Data(Self.previewJSON.utf8)
        )

        XCTAssertEqual(snapshot.saved.theme, "native")
        XCTAssertNil(snapshot.saved.imageAssetId)
        XCTAssertEqual(snapshot.preview?.theme, "mint")
        XCTAssertEqual(snapshot.preview?.imageAssetId, "owned-image-42")
        XCTAssertEqual(snapshot.preview?.overlayOpacity, 88)
        XCTAssertEqual(snapshot.revision, 9_007_199_254_740_993)
        XCTAssertEqual(snapshot.targetId, "verified-window-1")
        XCTAssertTrue(snapshot.actions.cancelPreview)
        XCTAssertFalse(snapshot.actions.apply)
        XCTAssertEqual(snapshot.options.readingLayouts.last?.value, "future-layout")
        XCTAssertEqual(snapshot.options.readingLayouts.last?.textKey, "appearance.layout.future")
        XCTAssertEqual(snapshot.options.readingLayouts.last?.supported, false)
    }

    func testNullablePreviewAndImageMetadataDecodeWithoutSourcePaths() throws {
        let snapshot = AppearanceTestFixture.snapshot()
        let data = try BackendJSON.makeEncoder().encode(snapshot)
        let decoded = try BackendJSON.makeDecoder().decode(BackendAppearanceSnapshot.self, from: data)
        XCTAssertNil(decoded.preview)
        XCTAssertNil(decoded.targetId)
        XCTAssertNil(decoded.errorTextKey)

        let image = try BackendJSON.makeDecoder().decode(
            BackendAppearanceImageResult.self,
            from: Data(#"{"assetId":"owned-image-42","width":640,"height":480,"format":"png"}"#.utf8)
        )
        XCTAssertEqual(image.assetId, "owned-image-42")
        XCTAssertEqual(image.width, 640)
        XCTAssertEqual(image.height, 480)
        XCTAssertEqual(image.format, "png")
    }

    func testSettingsRequestUsesTheGoShapeAndIntegerOpacity() throws {
        let snapshot = try BackendJSON.makeDecoder().decode(
            BackendAppearanceSnapshot.self, from: Data(Self.previewJSON.utf8)
        )
        let settings = try XCTUnwrap(snapshot.preview)
        let data = try BackendJSON.makeEncoder().encode(AppearanceSettingsParameter(settings: settings))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(Set(object.keys), ["settings"])
        let values = try XCTUnwrap(object["settings"] as? [String: Any])
        XCTAssertEqual(Set(values.keys), [
            "theme", "readingLayout", "backgroundMode", "solidColor", "imageAssetId", "overlayOpacity",
        ])
        XCTAssertEqual(values["overlayOpacity"] as? Int, 88)
        XCTAssertEqual(values["imageAssetId"] as? String, "owned-image-42")
        XCTAssertNil(values["path"])
        XCTAssertNil(values["dataURL"])
    }

    func testImageImportRequestContainsOnlyTheExplicitlySelectedPath() throws {
        let selectedPath = "/tmp/explicit-selection.png"
        let data = try BackendJSON.makeEncoder().encode(AppearanceImageParameter(path: selectedPath))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: String])
        XCTAssertEqual(object, ["path": selectedPath])
    }

    func testSnapshotRefreshPreservesUnsentDraftValuesForGoValidation() {
        var draft = AppearanceDraft()
        draft.receive(AppearanceTestFixture.snapshot())
        draft.theme = "mint"
        draft.solidColor = "unfinished color input"
        let refreshed = AppearanceTestFixture.snapshot(saved: AppearanceTestFixture.darkSettings)
        draft.receive(refreshed)

        XCTAssertEqual(draft.theme, "mint")
        XCTAssertEqual(draft.solidColor, "unfinished color input")
        XCTAssertTrue(draft.hasLocalEdits)
        XCTAssertEqual(refreshed.saved.theme, "dark")
    }

    func testCleanDraftAdoptsPreviewAndSuccessfulCancelReturnsToSavedSettings() {
        var draft = AppearanceDraft()
        let preview = AppearanceTestFixture.snapshot(preview: AppearanceTestFixture.darkSettings)
        draft.receive(preview)
        XCTAssertEqual(draft.settings, preview.preview)
        XCTAssertFalse(draft.hasLocalEdits)

        draft.solidColor = "#ABCDEF"
        let cancelled = AppearanceTestFixture.snapshot()
        draft.accept(cancelled)
        XCTAssertEqual(draft.settings, cancelled.saved)
        XCTAssertFalse(draft.hasLocalEdits)
    }

    func testFailedApplySnapshotDoesNotEraseEditsOrClaimTheyWereSaved() {
        var draft = AppearanceDraft()
        draft.receive(AppearanceTestFixture.snapshot(preview: AppearanceTestFixture.darkSettings))
        draft.theme = "mint"
        let failed = AppearanceTestFixture.snapshot(
            preview: AppearanceTestFixture.darkSettings,
            status: "unsaved", errorTextKey: "appearance.error.persistence"
        )
        draft.receive(failed)

        XCTAssertEqual(draft.theme, "mint")
        XCTAssertTrue(draft.hasLocalEdits)
        XCTAssertEqual(failed.saved.theme, "native")
        XCTAssertEqual(failed.preview?.theme, "dark")
        XCTAssertEqual(failed.errorTextKey, "appearance.error.persistence")
    }

    private static let previewJSON = #"""
    {
      "saved": {"theme":"native","readingLayout":"native","backgroundMode":"off","solidColor":"#DEF3E5","imageAssetId":null,"overlayOpacity":88},
      "preview": {"theme":"mint","readingLayout":"native","backgroundMode":"local-image","solidColor":"#DEF3E5","imageAssetId":"owned-image-42","overlayOpacity":88},
      "status":"preview",
      "statusTextKey":"appearance.status.preview",
      "errorTextKey":null,
      "targetId":"verified-window-1",
      "revision":9007199254740993,
      "actions":{"preview":true,"apply":false,"cancelPreview":true,"restoreNative":true,"importImage":true},
      "options":{
        "themes":[{"value":"native","textKey":"appearance.theme.native","supported":true}],
        "readingLayouts":[{"value":"native","textKey":"appearance.layout.native","supported":true},{"value":"future-layout","textKey":"appearance.layout.future","supported":false}],
        "backgroundModes":[{"value":"off","textKey":"appearance.background.off","supported":true}],
        "overlayMinimum":50,"overlayMaximum":100
      }
    }
    """#
}

enum AppearanceTestFixture {
    static let darkSettings = BackendAppearanceSettings(
        theme: "dark", readingLayout: "native", backgroundMode: "solid",
        solidColor: "#18212A", imageAssetId: nil, overlayOpacity: 88
    )

    static func snapshot(
        saved: BackendAppearanceSettings = GeneratedAppearanceDefaults.settings,
        preview: BackendAppearanceSettings? = nil,
        status: String = "unavailable",
        errorTextKey: String? = nil
    ) -> BackendAppearanceSnapshot {
        BackendAppearanceSnapshot(
            saved: saved, preview: preview, status: status,
            statusTextKey: "appearance.status.unavailable", errorTextKey: errorTextKey,
            targetId: nil, revision: 1,
            actions: BackendAppearanceActions(
                preview: false, apply: false, cancelPreview: false, restoreNative: false, importImage: true
            ),
            options: BackendAppearanceOptions(
                themes: [BackendAppearanceOption(value: "native", textKey: "appearance.theme.native", supported: true)],
                readingLayouts: [BackendAppearanceOption(value: "native", textKey: "appearance.layout.native", supported: true)],
                backgroundModes: [BackendAppearanceOption(value: "off", textKey: "appearance.background.off", supported: true)],
                overlayMinimum: 50, overlayMaximum: 100
            )
        )
    }
}
