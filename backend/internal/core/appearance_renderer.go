package core

import "fmt"

func appearanceProbeScript(owner string, epoch uint64, targetID string, revision uint64, expectedSettings AppearanceSettings) string {
	return fmt.Sprintf(`(() => {
  const lease = globalThis.__CODEX_COMPANION_RUNTIME_LEASE__;
  const appearance = globalThis.__CODEX_COMPANION_APPEARANCE__;
  if (!lease || lease.instance !== %s || lease.epoch !== %d || !lease.active) return { status: "stopped" };
  if (!appearance) return { status: "missing" };
  if (appearance.owner !== %s || appearance.epoch !== %d || appearance.targetID !== %s) return { status: "foreign" };
  if (appearance.revision !== %d) return { status: "stale" };
  const expectedSettingsKey = JSON.stringify(%s);
  if (JSON.stringify(appearance.settings) !== expectedSettingsKey) return { status: "stale" };
  return appearance.verify();
})()`, JSONLiteral(owner), epoch, JSONLiteral(owner), epoch, JSONLiteral(targetID), revision, JSONLiteral(expectedSettings))
}

func appearanceCleanupScript(owner string, epoch uint64) string {
	return fmt.Sprintf(`(() => {
  const owner = %s, epoch = %d;
  const lease = globalThis.__CODEX_COMPANION_APPEARANCE_LEASE__;
  const appearance = globalThis.__CODEX_COMPANION_APPEARANCE__;
  if ((lease && (lease.owner !== owner || lease.epoch > epoch)) || (appearance && appearance.owner !== owner)) return { status: "foreign" };
  if (lease) lease.active = false;
  if (appearance) appearance.cleanup();
  return { status: globalThis.__CODEX_COMPANION_APPEARANCE__ ? "cleanupFailed" : "native" };
})()`, JSONLiteral(owner), epoch)
}

// The adapter changes only its stylesheet and a single owned background marker.
// Official data-theme, inline styles, layout, focus and scroll are untouched.
// Static seams are pinned to Appx 26.930.2377.0 / Electron 26.930.21537; each
// activation still checks live geometry and computed CSS rather than assuming it.
func appearanceApplyScript(owner string, epoch uint64, request AppearanceRuntimeRequest) string {
	return fmt.Sprintf(`(async () => {
  const owner = %s, epoch = %d, targetID = %s, revision = %d;
  let settings = %s;
  let imageData = %s;
	const requestSettingsKey = JSON.stringify(settings);
  const runtimeLease = globalThis.__CODEX_COMPANION_RUNTIME_LEASE__;
  const packageRuntime = globalThis.__CODEX_TWEAKS__;
  if (!runtimeLease || runtimeLease.instance !== owner || runtimeLease.epoch !== epoch || !runtimeLease.active
      || !packageRuntime || packageRuntime.owner !== owner || packageRuntime.epoch !== epoch || packageRuntime.stopping) return { status: "stopped" };
  const leaseKey = "__CODEX_COMPANION_APPEARANCE_LEASE__";
  const previousLease = globalThis[leaseKey];
  if (previousLease && previousLease.owner !== owner) return { status: "foreign" };
  if (previousLease && (previousLease.epoch > epoch || previousLease.epoch === epoch &&
      ((previousLease.pendingRevision ?? previousLease.revision) > revision || (previousLease.pendingRevision ?? previousLease.revision) === revision && !previousLease.active && previousLease.pendingSettingsKey === requestSettingsKey))) return { status: "stopped" };
  const active = () => {
    const runtime = globalThis.__CODEX_COMPANION_RUNTIME_LEASE__;
    const lease = globalThis[leaseKey];
    return Boolean(runtime?.instance === owner && runtime.epoch === epoch && runtime.active &&
      lease?.owner === owner && lease.epoch === epoch && lease.revision === revision && lease.settingsKey === JSON.stringify(settings) && lease.active);
  };
  const requestCurrent = () => {
    const runtime = globalThis.__CODEX_COMPANION_RUNTIME_LEASE__, lease = globalThis[leaseKey];
    return Boolean(runtime?.instance === owner && runtime.epoch === epoch && runtime.active &&
      lease?.owner === owner && lease.epoch === epoch && lease.pendingRevision === revision && lease.pendingSettingsKey === requestSettingsKey && lease.active);
  };
  // Decoding a candidate must not invalidate the currently visible appearance.
  // Keep a monotonic request watermark alongside the active effect revision.
  globalThis[leaseKey] = { owner, epoch, revision: previousLease?.epoch === epoch ? previousLease.revision : revision,
    settingsKey: previousLease?.epoch === epoch ? previousLease.settingsKey : requestSettingsKey,
    pendingRevision: revision, pendingSettingsKey: requestSettingsKey, active: true };
  if (settings.readingLayout !== "native") return { status: "unsupportedLayout", targetID, revision };
  if (settings.backgroundMode === "local-image") {
    if (!/^data:image\/(png|jpeg|webp);base64,/.test(imageData)) return { status: "invalidImage", targetID, revision };
    const image = new Image();
    image.src = imageData;
    try { await image.decode(); } catch { return { status: "invalidImage", targetID, revision }; }
    if (!requestCurrent() || !image.naturalWidth || !image.naturalHeight) return { status: "stopped" };
  }
  if (!requestCurrent()) return { status: "stopped" };
  globalThis[leaseKey].revision = revision;
  globalThis[leaseKey].settingsKey = requestSettingsKey;
  const old = globalThis.__CODEX_COMPANION_APPEARANCE__;
  if (old && old.owner !== owner) return { status: "foreign" };
  old?.cleanup({ replacing: true });
  const install = () => {
  const root = document.documentElement;
  if (!root || root.dataset.codexWindowType === "extension" || !["light", "dark"].includes(root.dataset.theme)) return { status: "unsupported", targetID, revision };
  if (settings.theme === "native" && settings.backgroundMode === "off") return { status: "native", targetID, revision, settingsKey: JSON.stringify(settings) };
  const style = document.createElement("style");
  style.dataset.codexCompanionAppearance = owner;
  style.dataset.codexCompanionRevision = String(revision);
  const marker = "data-codex-companion-background";
  const forcedColors = matchMedia("(forced-colors: active)");
  const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
  let background = null, stopped = false, observer = null;
  const cleanup = ({ replacing = false } = {}) => {
    stopped = true;
    observer?.disconnect();
    forcedColors.removeEventListener("change", refresh);
    reducedMotion.removeEventListener("change", refresh);
    window.removeEventListener("resize", refresh);
    style.remove();
    if (background?.getAttribute(marker) === owner) background.removeAttribute(marker);
    background = null;
    if (!replacing && globalThis[leaseKey]?.revision === revision) globalThis[leaseKey].active = false;
    if (globalThis.__CODEX_COMPANION_APPEARANCE__ === appearance) delete globalThis.__CODEX_COMPANION_APPEARANCE__;
  };
  const palette = () => {
    if (settings.theme === "dark") return { surface: "#151C18", under: "#101612", text: "#E1EEE5", secondary: "#B4CABE" };
    if (settings.theme === "mint") return root.dataset.theme === "dark"
      ? { surface: "#1C3024", under: "#16281D", text: "#DEF3E5", secondary: "#B3CDBC" }
      : { surface: "#DEF3E5", under: "#D0E8D8", text: "#22352A", secondary: "#466052" };
    return null;
  };
  // These aliases are declared by the pinned official theme factory/CSS.
  // Opaque input and popup surfaces need the same palette as their inherited
  // foreground; changing only the root foreground can leave light-on-light text.
  const paletteTokens = (p) => ({
    "--app-color-background-surface": p.surface,
    "--app-color-background-surface-under": p.under,
    "--app-color-text-foreground": p.text,
    "--app-color-text-foreground-secondary": p.secondary,
    "--app-color-text-foreground-tertiary": p.secondary,
    "--app-color-text-secondary": p.secondary,
    "--app-color-icon-primary": p.text,
    "--app-color-icon-secondary": p.secondary,
    "--app-color-icon-tertiary": p.secondary,
    "--color-background-composer-primary": p.under,
    "--color-background-composer-surface": p.under,
    "--color-background-composer-action-bar": p.under,
    "--color-text-composer-primary": p.text,
    "--app-color-background-control": p.under,
    "--color-background-control-opaque": p.under,
    "--app-color-background-elevated-primary": p.surface,
    "--app-color-background-elevated-primary-opaque": p.surface,
    "--app-color-background-elevated-secondary": p.under,
    "--app-color-background-elevated-secondary-opaque": p.under,
    "--app-color-background-application-menu": p.surface,
    "--app-color-foreground-application-menu": p.text
  });
  const rgb = (color) => {
    const literal = String(color).trim();
    if (!literal) return null;
    if (/^#[0-9a-f]{6}$/i.test(literal)) return [1, 3, 5].map((offset) => parseInt(literal.slice(offset, offset + 2), 16));
    if (/^#[0-9a-f]{3}$/i.test(literal)) return [1, 2, 3].map((offset) => parseInt(literal[offset] + literal[offset], 16));
    const probe = document.createElement("span");
    probe.style.cssText = "position:fixed;visibility:hidden;pointer-events:none";
    probe.style.color = color;
    if (!probe.style.color) return null;
    root.append(probe);
    const value = getComputedStyle(probe).color;
    probe.remove();
    const match = value.match(/^rgba?\(\s*([0-9.]+)[, ]+\s*([0-9.]+)[, ]+\s*([0-9.]+)(?:\s*[,/]\s*([0-9.]+))?\s*\)$/);
    if (match) return match[4] === undefined ? match.slice(1, 4).map(Number) : match.slice(1, 5).map(Number);
    // Chromium can serialize a native color-mix as color(srgb ... / alpha).
    // Normalize supported CSS colors through a private single-pixel canvas.
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 1;
    const context = canvas.getContext("2d", { willReadFrequently: true });
    if (!context || !value) return null;
    context.fillStyle = value;
    context.fillRect(0, 0, 1, 1);
    const pixel = context.getImageData(0, 0, 1, 1).data;
    return [pixel[0], pixel[1], pixel[2], pixel[3] / 255];
  };
  const luminance = (color) => color.slice(0, 3).map((channel) => { const c = channel / 255; return c <= .04045 ? c / 12.92 : ((c + .055) / 1.055) ** 2.4; })
    .reduce((sum, channel, index) => sum + channel * [.2126, .7152, .0722][index], 0);
  const contrast = (a, b) => { const x = luminance(a), y = luminance(b); return (Math.max(x, y) + .05) / (Math.min(x, y) + .05); };
  const overlay = () => {
    const computed = getComputedStyle(root);
    const surface = rgb(computed.getPropertyValue("--app-color-background-surface"));
    const foreground = rgb(computed.getPropertyValue("--app-color-text-foreground"));
    const secondary = rgb(computed.getPropertyValue("--app-color-text-foreground-secondary"));
    const foregrounds = [foreground, secondary];
    const onSurface = (color, background) => color.slice(0, 3).map((channel, index) => channel * (color[3] ?? 1) + background[index] * (1 - (color[3] ?? 1)));
    if (!surface || (surface[3] ?? 1) < 1 || foregrounds.some((color) => !color || contrast(surface, onSurface(color, surface)) < 4.5)) throw new Error("appearanceContrast");
    let opacity = settings.overlayOpacity / 100;
    // Raise the scrim only as needed for the worst possible image pixel. The
    // requested value is retained; native theme changes recalculate this bound.
    while (opacity < 1 && foregrounds.some((color) => [0, 255].some((pixel) => {
      const background = surface.slice(0, 3).map((v) => v * opacity + pixel * (1 - opacity));
      return contrast(onSurface(color, background), background) < 4.5;
    }))) opacity = Math.min(1, opacity + .01);
    return 'rgba(' + surface.slice(0, 3).join(',') + ',' + opacity.toFixed(2) + ')';
  };
  const visible = (node) => {
    const rect = node.getBoundingClientRect(), css = getComputedStyle(node);
    return rect.width >= 160 && rect.height >= 100 && css.display !== "none" && css.visibility !== "hidden" &&
      rect.right > 0 && rect.bottom > 0 && rect.left < innerWidth && rect.top < innerHeight;
  };
  const findBackground = () => {
    const nodes = [...document.querySelectorAll('main[data-app-shell-main-surface="default"] .thread-scroll-container')]
      .filter((node) => !node.closest('#codex-tweaks-root,[data-codex-window-type="extension"]') && visible(node));
    if (nodes.length !== 1) throw new Error("appearanceBackgroundScope");
    return nodes[0];
  };
  const backgroundExposed = () => {
    if (!background || !visible(background)) return false;
    const rect = background.getBoundingClientRect();
    // An opaque descendant or existing pseudo-element can hide a valid CSS
    // background. Require at least one visible sample of the chosen surface.
    const before = getComputedStyle(background, "::before"), after = getComputedStyle(background, "::after");
    if ([before, after].some((css) => css.content !== "none" && css.content !== "normal" &&
        (css.backgroundImage !== "none" || css.backgroundColor !== "transparent" && css.backgroundColor !== "rgba(0, 0, 0, 0)"))) return false;
    if (getComputedStyle(background).maskImage !== "none") return false;
    for (const [dx, dy] of [[.04,.2],[.96,.2],[.04,.7],[.96,.7],[.5,.5]]) {
      const x = Math.max(0, Math.min(innerWidth - 1, rect.left + rect.width * dx));
      const y = Math.max(0, Math.min(innerHeight - 1, rect.top + rect.height * dy));
      let node = document.elementFromPoint(x, y), exposed = Boolean(node && background.contains(node));
      while (exposed && node && node !== background) {
        const css = getComputedStyle(node), color = css.backgroundColor;
        if (css.backgroundImage !== "none" || color && color !== "transparent" && color !== "rgba(0, 0, 0, 0)" && !/rgba\([^)]*,\s*0\)$/.test(color)) exposed = false;
        node = node.parentElement;
      }
      if (exposed) return true;
    }
    return false;
  };
  const verify = () => {
    if (stopped || !active()) return { status: "stopped" };
    if (root.dataset.codexWindowType === "extension" || !["light", "dark"].includes(root.dataset.theme)) return { status: "invalid" };
    const p = palette();
    if (!style.isConnected || style.dataset.codexCompanionRevision !== String(revision)) return { status: "invalid" };
    if (!forcedColors.matches && p) {
      const css = getComputedStyle(root);
      if (Object.entries(paletteTokens(p)).some(([token, color]) =>
          rgb(css.getPropertyValue(token))?.join() !== rgb(color)?.join())) return { status: "invalid" };
    }
    if (!forcedColors.matches && settings.backgroundMode !== "off" && (!background ||
        background.getAttribute(marker) !== owner || getComputedStyle(background).backgroundImage === "none" || !backgroundExposed())) return { status: "invalid" };
    return { status: settings.theme === "native" && settings.backgroundMode === "off" ? "native" : "applied", targetID, revision, settingsKey: JSON.stringify(settings) };
  };
  const refresh = () => {
    if (stopped || !active()) { cleanup(); return; }
    try {
      if (background?.getAttribute(marker) === owner) background.removeAttribute(marker);
      background = null;
      let css = "";
      const p = palette();
      if (!forcedColors.matches && p) css += ':root:not([data-codex-window-type="extension"])[data-theme], :root:not([data-codex-window-type="extension"]) [data-theme] {' +
        Object.entries(paletteTokens(p)).map(([token, color]) => token + ':' + color + '!important;').join('') + '}';
      // Resolve the palette before computing a scrim. The official style remains
      // untouched and wins again when this owned stylesheet is removed.
      style.textContent = css;
      if (!forcedColors.matches && settings.backgroundMode !== "off") {
        background = findBackground();
        const previousMarker = background.getAttribute(marker);
        if (previousMarker && previousMarker !== owner) throw new Error("appearanceBackgroundOwnership");
        background.setAttribute(marker, owner);
        const scrim = overlay();
        const image = settings.backgroundMode === "solid" ? 'linear-gradient(' + settings.solidColor + ',' + settings.solidColor + ')' : 'url("' + imageData + '")';
        css += '[data-codex-companion-background="' + owner + '"] {background-image:linear-gradient(' + scrim + ',' + scrim + '),' + image + '!important;background-size:cover!important;background-position:center!important;background-repeat:no-repeat!important;}';
        style.textContent = css;
      }
      if (verify().status === "invalid") throw new Error("appearanceVerification");
    } catch { cleanup(); }
  };
  const appearance = { owner, epoch, targetID, revision, settings, imageData, verify, cleanup };
  globalThis.__CODEX_COMPANION_APPEARANCE__ = appearance;
  document.head.append(style);
  forcedColors.addEventListener("change", refresh);
  reducedMotion.addEventListener("change", refresh);
  window.addEventListener("resize", refresh);
  observer = new MutationObserver((records) => {
    if (records.some((record) => record.type === "attributes" && record.target === root && ["data-theme", "data-codex-window-type"].includes(record.attributeName)
       || record.type === "childList" && record.target?.matches?.("style[data-codex-app-themes]")
       || record.type === "childList" && [...record.addedNodes, ...record.removedNodes].some((node) => node.nodeType === 1 && node !== style &&
          (node.matches?.("style[data-codex-app-themes],.thread-scroll-container") || node.querySelector?.(".thread-scroll-container"))))) refresh();
  });
  observer.observe(root, { attributes: true, attributeFilter: ["data-theme", "data-codex-window-type"], childList: true, subtree: true });
  refresh();
  return stopped ? { status: "unsupported", targetID, revision } : verify();
  };
  const result = install();
  if (!["native", "applied"].includes(result.status) && old && active() === false) {
    // A candidate owns its rollback. Keep the prior accepted choices visible
    // under the newer revision so a delayed old candidate still cannot win.
    settings = old.settings;
    imageData = old.imageData;
    globalThis[leaseKey] = { owner, epoch, revision, settingsKey: JSON.stringify(settings), pendingRevision: revision, pendingSettingsKey: requestSettingsKey, active: true };
    install();
  }
  return result;
})()`, JSONLiteral(owner), epoch, JSONLiteral(request.TargetID), request.Revision, JSONLiteral(request.Settings), JSONLiteral(request.DataURL))
}
