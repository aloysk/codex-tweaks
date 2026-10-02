#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# shellcheck source=scripts/application-identity.sh
source "$ROOT_DIR/scripts/application-identity.sh"
PRODUCT_NAME="$(application_identity name)"
ARTIFACT_PREFIX="$(application_identity artifactPrefix)"
EXPECTED_BUNDLE_IDENTIFIER="$(application_identity bundleIdentifier)"
UPDATES_ENABLED="$(application_identity updatesEnabled)"

RELEASE_TAG="${RELEASE_TAG:-${1:-}}"
BUILD_NUMBER="${BUILD_NUMBER:-1}"
DIST_DIR="${DIST_DIR:-dist}"
EXPECTED_MINOS="13.0"
EXPECTED_BACKEND_MINOS="12.0"
EXPECTED_SIGNING_CERT_SHA256="${MACOS_SIGNING_CERT_SHA256:-}"

if [[ ! "$RELEASE_TAG" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]]; then
  echo "RELEASE_TAG 必须是 v1.2.3 或 v1.2.3-beta.1 形式" >&2
  exit 1
fi

MARKETING_VERSION="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}"
RELEASE_VERSION="${RELEASE_TAG#v}"

if [[ ! "$BUILD_NUMBER" =~ ^[1-9][0-9]*$ ]]; then
  echo "BUILD_NUMBER 必须是正整数" >&2
  exit 1
fi

normalize_fingerprint() {
  tr '[:lower:]' '[:upper:]' | tr -cd '0-9A-F'
}

EXPECTED_SIGNING_CERT_SHA256="$(
  printf '%s' "$EXPECTED_SIGNING_CERT_SHA256" | normalize_fingerprint
)"
if [[ -n "$EXPECTED_SIGNING_CERT_SHA256" ]] \
  && [[ ${#EXPECTED_SIGNING_CERT_SHA256} -ne 64 ]]; then
  echo "MACOS_SIGNING_CERT_SHA256 必须是完整的 SHA-256 指纹。" >&2
  exit 1
fi

verify_signing_certificate() {
  local target_path="$1"
  local certificate_dir
  local actual_sha256

  if [[ -z "$EXPECTED_SIGNING_CERT_SHA256" ]]; then
    return
  fi

  certificate_dir="$(mktemp -d "${TMPDIR:-/tmp}/codex-tweaks-signature.XXXXXX")"
  if ! codesign \
    --display \
    --extract-certificates="${certificate_dir}/certificate-" \
    "$target_path" \
    >/dev/null 2>&1; then
    find "$certificate_dir" -depth -delete 2>/dev/null || true
    echo "无法从签名中提取证书：${target_path}" >&2
    return 1
  fi
  if [[ ! -f "${certificate_dir}/certificate-0" ]]; then
    find "$certificate_dir" -depth -delete 2>/dev/null || true
    echo "产物不是证书签名（可能仍为 ad-hoc）：${target_path}" >&2
    return 1
  fi

  actual_sha256="$(
    openssl x509 \
      -inform DER \
      -in "${certificate_dir}/certificate-0" \
      -noout \
      -fingerprint \
      -sha256 \
      | cut -d= -f2 \
      | normalize_fingerprint
  )"
  find "$certificate_dir" -depth -delete 2>/dev/null || true

  if [[ "$actual_sha256" != "$EXPECTED_SIGNING_CERT_SHA256" ]]; then
    echo "签名证书 SHA-256 不匹配：${target_path}" >&2
    echo "期望：${EXPECTED_SIGNING_CERT_SHA256}" >&2
    echo "实际：${actual_sha256}" >&2
    return 1
  fi
}

verify_app() {
  local app_path="$1"
  local expected_archs="$2"
  local binary="$app_path/Contents/MacOS/${PRODUCT_NAME}"
  local backend="$app_path/Contents/Resources/codex-tweaks-backend"
  local actual_archs
  local actual_version
  local actual_release_version
  local actual_build
  local actual_bundle_identifier
  local automatic_checks
  local allows_automatic_updates
  local automatically_downloads_updates
  local backend_minos_values
  local minos_values

  if [[ ! -f "$binary" ]]; then
    echo "缺少可执行文件：$binary" >&2
    return 1
  fi
  if [[ ! -x "$backend" ]]; then
    echo "缺少 Go 后端可执行文件：$backend" >&2
    return 1
  fi

  actual_archs="$(lipo -archs "$binary" | tr ' ' '\n' | sort | xargs)"
  expected_archs="$(tr ' ' '\n' <<< "$expected_archs" | sort | xargs)"
  if [[ "$actual_archs" != "$expected_archs" ]]; then
    echo "${binary} 架构错误：期望 ${expected_archs}，实际 ${actual_archs}" >&2
    return 1
  fi

  actual_archs="$(lipo -archs "$backend" | tr ' ' '\n' | sort | xargs)"
  if [[ "$actual_archs" != "$expected_archs" ]]; then
    echo "${backend} 架构错误：期望 ${expected_archs}，实际 ${actual_archs}" >&2
    return 1
  fi
  if [[ "$("$backend" --version)" != "$RELEASE_VERSION" ]]; then
    echo "${backend} 版本错误：期望 ${RELEASE_VERSION}" >&2
    return 1
  fi

  backend_minos_values="$(vtool -show-build "$backend" | awk '$1 == "minos" { print $2 }' | sort -u)"
  if [[ -z "$backend_minos_values" ]] || [[ "$backend_minos_values" != "$EXPECTED_BACKEND_MINOS" ]]; then
    echo "${backend} 最低系统版本错误：期望 ${EXPECTED_BACKEND_MINOS}，实际 ${backend_minos_values:-未知}" >&2
    return 1
  fi

  minos_values="$(vtool -show-build "$binary" | awk '$1 == "minos" { print $2 }' | sort -u)"
  if [[ -z "$minos_values" ]] || [[ "$minos_values" != "$EXPECTED_MINOS" ]]; then
    echo "${binary} 最低系统版本错误：期望 ${EXPECTED_MINOS}，实际 ${minos_values:-未知}" >&2
    return 1
  fi

  actual_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app_path/Contents/Info.plist")"
  actual_release_version="$(/usr/libexec/PlistBuddy -c 'Print :CodexTweaksReleaseVersion' "$app_path/Contents/Info.plist")"
  actual_build="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$app_path/Contents/Info.plist")"
  actual_bundle_identifier="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app_path/Contents/Info.plist")"
  if [[ "$actual_bundle_identifier" != "$EXPECTED_BUNDLE_IDENTIFIER" ]]; then
    echo "${app_path} bundle identity mismatch: expected ${EXPECTED_BUNDLE_IDENTIFIER}, got ${actual_bundle_identifier}" >&2
    return 1
  fi
  if [[ "$actual_version" != "$MARKETING_VERSION" ]] \
    || [[ "$actual_release_version" != "$RELEASE_VERSION" ]] \
    || [[ "$actual_build" != "$BUILD_NUMBER" ]]; then
    echo "${app_path} 版本错误：期望 ${RELEASE_VERSION} / ${MARKETING_VERSION} (${BUILD_NUMBER})，实际 ${actual_release_version} / ${actual_version} (${actual_build})" >&2
    return 1
  fi

  automatic_checks="$(/usr/libexec/PlistBuddy -c 'Print :SUEnableAutomaticChecks' "$app_path/Contents/Info.plist")"
  allows_automatic_updates="$(/usr/libexec/PlistBuddy -c 'Print :SUAllowsAutomaticUpdates' "$app_path/Contents/Info.plist")"
  automatically_downloads_updates="$(/usr/libexec/PlistBuddy -c 'Print :SUAutomaticallyUpdate' "$app_path/Contents/Info.plist")"
  if [[ "$automatic_checks" != false ]] \
    || [[ "$allows_automatic_updates" != false ]] \
    || [[ "$automatically_downloads_updates" != false ]]; then
    echo "${app_path} 更新策略错误：应默认关闭检查、自动安装和后台下载" >&2
    return 1
  fi
  if [[ "$UPDATES_ENABLED" != true ]]; then
    if /usr/libexec/PlistBuddy -c 'Print :SUFeedURL' "$app_path/Contents/Info.plist" >/dev/null 2>&1 \
      || /usr/libexec/PlistBuddy -c 'Print :SUPublicEDKey' "$app_path/Contents/Info.plist" >/dev/null 2>&1; then
      echo "${app_path} disabled updates must not contain an update feed or signing key." >&2
      return 1
    fi
  fi

  codesign --verify --strict --verbose=2 "$backend"
  codesign --verify --deep --strict --verbose=2 "$app_path"
  verify_signing_certificate "$backend"
  verify_signing_certificate "$app_path"
  echo "已校验 ${app_path}：${actual_archs}，macOS ${EXPECTED_MINOS}+"
}

verify_app "${DIST_DIR}/${PRODUCT_NAME}.app" "arm64 x86_64"
verify_app "${DIST_DIR}/${PRODUCT_NAME}-arm64.app" arm64
verify_app "${DIST_DIR}/${PRODUCT_NAME}-x86_64.app" x86_64

for dmg in \
  "${DIST_DIR}/${ARTIFACT_PREFIX}-${RELEASE_TAG}.dmg" \
  "${DIST_DIR}/${ARTIFACT_PREFIX}-${RELEASE_TAG}-arm64.dmg" \
  "${DIST_DIR}/${ARTIFACT_PREFIX}-${RELEASE_TAG}-x86_64.dmg"; do
  if [[ ! -f "$dmg" ]]; then
    echo "缺少 DMG：$dmg" >&2
    exit 1
  fi
  hdiutil verify "$dmg"
  if codesign --display "$dmg" >/dev/null 2>&1; then
    echo "DMG 容器应保持未签名；稳定自签名仅用于内部 App：$dmg" >&2
    exit 1
  fi
done

echo "Release ${RELEASE_TAG} 的全部产物校验通过"
