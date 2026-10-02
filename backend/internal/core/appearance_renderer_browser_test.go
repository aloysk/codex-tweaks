package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// This fixture uses Chromium's real CSS cascade, layout, hit testing, media
// queries and image decoder. Native colors and content are synthetic; token
// names and the composer-surface alias represent the verified adapter seams.
// It never includes official application code, a profile or a session.
const appearanceBrowserHTML = `<!doctype html>
<html data-theme="light"><head><meta charset="utf-8">
<style data-codex-app-themes>
:root[data-theme="light"] { --app-color-background-surface:#FFFFFF; --app-color-background-surface-under:#F8F8F8; --app-color-text-foreground:#292929; }
:root[data-theme="dark"] { --app-color-background-surface:#111111; --app-color-background-surface-under:#1B1B1B; --app-color-text-foreground:#DBDBDB; }
:root[data-theme="light"] { --app-color-text-foreground-secondary:#505050; --app-color-text-secondary:#505050; --app-color-icon-secondary:#505050;
  --app-color-background-elevated-primary-opaque:#FFFFFF; --color-background-composer-primary:#F8F8F8; --app-color-background-control:#F8F8F8; }
:root[data-theme="dark"] { --app-color-text-foreground-secondary:#AFAFAF; --app-color-text-secondary:#AFAFAF; --app-color-icon-secondary:#AFAFAF;
  --app-color-background-elevated-primary-opaque:#222222; --color-background-composer-primary:#1B1B1B; --app-color-background-control:#1B1B1B; }
:root { --color-background-composer-surface:var(--app-color-background-elevated-primary-opaque);
  --color-text-composer-primary:var(--app-color-text-foreground); --color-background-control-opaque:var(--app-color-background-control);
  --app-color-background-application-menu:var(--app-color-background-surface); --app-color-foreground-application-menu:var(--app-color-text-foreground); }
html,body { margin:0; width:100%; height:100%; overflow:hidden; }
body { color:var(--app-color-text-foreground); background:var(--app-color-background-surface-under); font:14px sans-serif; }
main { width:760px; height:680px; margin:12px; }
.thread-scroll-container { height:500px; overflow:auto; background:var(--app-color-background-surface); }
article { min-height:1400px; padding:24px 70px; }
pre { white-space:pre; overflow-x:auto; background:#f0f1f2; color:#202020; }
.diff { width:550px; overflow-x:auto; background:#edf7ed; color:#184018; }
.approval { background:#fff6d8; color:#402d00; border:1px solid #a68835; padding:10px; }
textarea { width:700px; height:70px; margin-top:10px; color:var(--app-color-text-foreground); background:var(--color-background-composer-surface); }
.secondary { color:var(--app-color-text-foreground-secondary); }
.muted { color:var(--app-color-text-secondary); }
.icon { color:var(--app-color-icon-secondary); width:16px; height:16px; }
#composer-label { color:var(--color-text-composer-primary); background:var(--color-background-composer-primary); }
#control-label { color:var(--app-color-text-foreground); background:var(--color-background-control-opaque); }
#popup-label { color:var(--app-color-foreground-application-menu); background:var(--app-color-background-application-menu); }
#primary-action { background:#3B63FB; color:#FFFFFF; }
.opaque { position:absolute; inset:0; min-height:1400px; background:#ffffff; }
.thread-scroll-container.covered { position:relative; }
.thread-scroll-container.pseudo::before { content:""; position:absolute; inset:0; background:#ffffff; }
</style><style id="foreign-style">.foreign { color: purple; }</style></head>
<body><main data-app-shell-main-surface="default">
<div class="thread-scroll-container"><article>
<h1>Synthetic conversation</h1><p>Ordinary prose keeps native geometry.</p>
<p id="secondary" class="secondary">Secondary text</p><p id="muted" class="muted">Muted text</p>
<svg id="icon" class="icon" aria-label="Synthetic icon"><path fill="currentColor" d="M2 2H14V14H2Z" /></svg>
<pre id="code">const result = "synthetic";</pre>
<div id="diff" class="diff">+ synthetic diff line</div>
<section id="approval" class="approval"><button id="approve">Approve synthetic action</button></section>
</article></div>
<div id="composer-label">Composer label</div>
<textarea id="composer" aria-label="Synthetic draft">synthetic draft: preserve selection</textarea>
<span id="control-label">Control label</span><span id="popup-label">Popup label</span><button id="primary-action">Primary action</button>
</main></body></html>`

// Instrumentation delegates to the browser implementations; it does not mock
// styles, geometry, media delivery, decoding, mutation delivery or hit testing.
const appearanceBrowserInstrumentation = `
const listenerEntries = new Map();
const nativeAdd = EventTarget.prototype.addEventListener, nativeRemove = EventTarget.prototype.removeEventListener;
const tracked = (target, type) => target === window && type === "resize" || target instanceof MediaQueryList && type === "change";
EventTarget.prototype.addEventListener = function(type, callback, options) {
  if (tracked(this, type)) { let entries = listenerEntries.get(this); if (!entries) listenerEntries.set(this, entries = new Set()); entries.add(callback); }
  return nativeAdd.call(this, type, callback, options);
};
EventTarget.prototype.removeEventListener = function(type, callback, options) {
  if (tracked(this, type)) { const entries = listenerEntries.get(this); entries?.delete(callback); if (!entries?.size) listenerEntries.delete(this); }
  return nativeRemove.call(this, type, callback, options);
};
const observerEntries = new Set(), NativeMutationObserver = MutationObserver;
globalThis.MutationObserver = class extends NativeMutationObserver {
  observe(...args) { observerEntries.add(this); return super.observe(...args); }
  disconnect() { observerEntries.delete(this); return super.disconnect(); }
};
globalThis.fixtureRestoreInstrumentation = () => {
  EventTarget.prototype.addEventListener = nativeAdd; EventTarget.prototype.removeEventListener = nativeRemove;
  globalThis.MutationObserver = NativeMutationObserver;
  if (globalThis.fixtureOriginalDecode) HTMLImageElement.prototype.decode = globalThis.fixtureOriginalDecode;
};
globalThis.fixtureCounts = () => ({
  styles: document.querySelectorAll("style[data-codex-companion-appearance]").length,
  markers: document.querySelectorAll("[data-codex-companion-background]").length,
  listeners: [...listenerEntries.values()].reduce((total, entries) => total + entries.size, 0),
  observers: observerEntries.size
});
globalThis.fixtureState = () => {
  const composer = document.querySelector("#composer"), thread = document.querySelector(".thread-scroll-container");
  return { draft: composer.value, selection: [composer.selectionStart, composer.selectionEnd, composer.selectionDirection], focus: document.activeElement?.id,
    scroll: [scrollX, scrollY, thread.scrollLeft, thread.scrollTop],
    rootTheme: document.documentElement.dataset.theme, rootStyle: document.documentElement.getAttribute("style"),
    composerStyle: composer.getAttribute("style"), threadStyle: thread.getAttribute("style"),
    islands: ["code", "diff", "approval", "primary-action"].map((id) => { const node = document.getElementById(id), css = getComputedStyle(node), rect = node.getBoundingClientRect();
      return [node.textContent, css.backgroundColor, css.color, css.overflowX, rect.width, rect.height]; }),
    nativeSheet: document.querySelector("style[data-codex-app-themes]").textContent,
    foreignSheet: document.querySelector("#foreign-style").textContent };
};
globalThis.__CODEX_COMPANION_RUNTIME_LEASE__ = { instance: "synthetic-fixture", epoch: 1, active: true };
globalThis.__CODEX_TWEAKS__ = { owner: "synthetic-fixture", epoch: 1, stopping: false };
const composer = document.querySelector("#composer"); composer.focus(); composer.setSelectionRange(10, 18, "backward");
document.querySelector(".thread-scroll-container").scrollTop = 280;
`

type appearanceBrowserFixture struct {
	t          *testing.T
	ctx        context.Context
	debugger   string
	connection *websocket.Conn
	nextID     int
	dialer     websocket.Dialer
	httpClient http.Client
}

func appearanceBrowserExecutable(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("CODEX_TWEAKS_TEST_BROWSER"); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
		t.Fatalf("CODEX_TWEAKS_TEST_BROWSER is not an executable file: %q", configured)
	}
	candidates := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
	if runtime.GOOS == "windows" {
		candidates = []string{
			filepath.Join(os.Getenv("PROGRAMFILES"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
		}
	} else if runtime.GOOS == "darwin" {
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", "/Applications/Chromium.app/Contents/MacOS/Chromium"}
	}
	for _, candidate := range candidates {
		if executable, err := exec.LookPath(candidate); err == nil {
			return executable
		}
	}
	t.Fatal("Chromium is required for the appearance DOM fixture; install Chrome/Edge or set CODEX_TWEAKS_TEST_BROWSER (this verification never skips)")
	return ""
}

func newAppearanceBrowserFixture(t *testing.T) *appearanceBrowserFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	profile := t.TempDir()
	log, err := os.Create(filepath.Join(profile, "browser.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, appearanceBrowserExecutable(t),
		"--headless=new", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-sync",
		"--disable-component-update", "--window-size=1024,768", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0",
		"--user-data-dir="+profile, "about:blank")
	configureCommand(command)
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	fixture := &appearanceBrowserFixture{t: t, ctx: ctx, dialer: websocket.Dialer{HandshakeTimeout: 3 * time.Second, Proxy: nil},
		httpClient: http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("fixture redirects are forbidden") }}}
	var browserSocket string
	t.Cleanup(func() {
		// Browser.close shuts down this profile and its children. The only forced
		// fallback is the exact PID returned by our own command.Start.
		if browserSocket != "" {
			closeCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			_, _ = fixture.call(closeCtx, browserSocket, "Browser.close", nil)
			stop()
		}
		if fixture.connection != nil {
			_ = fixture.connection.Close()
		}
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			_ = command.Process.Kill()
			<-finished
		}
		_ = log.Close()
	})
	deadline := time.Now().Add(15 * time.Second)
	var address string
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort")); err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) == 2 {
				address = "127.0.0.1:" + strings.TrimSpace(lines[0])
				browserSocket = "ws://" + address + strings.TrimSpace(lines[1])
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if address == "" {
		data, _ := os.ReadFile(filepath.Join(profile, "browser.log"))
		t.Fatalf("owned headless browser did not expose its ephemeral debugger: %s", data)
	}
	response, err := fixture.httpClient.Get("http://" + address + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var targets []CDPTarget
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&targets); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if target.Type == "page" && target.URL == "about:blank" && target.WebSocketDebuggerURL != nil {
			fixture.debugger = *target.WebSocketDebuggerURL
			break
		}
	}
	if fixture.debugger == "" {
		t.Fatal("owned browser did not create the blank fixture page")
	}
	fixture.command("Network.enable", nil)
	fixture.command("Network.setBlockedURLs", map[string]any{"urls": []string{"http://*", "https://*"}})
	t.Logf("appearance fixture: %s, owned PID %d, ephemeral debugger; no official process or profile", filepath.Base(command.Path), command.Process.Pid)
	return fixture
}

func (fixture *appearanceBrowserFixture) call(ctx context.Context, socket, method string, params any) (map[string]json.RawMessage, error) {
	connection, _, err := fixture.dialer.DialContext(ctx, socket, nil)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	return fixture.exchange(ctx, connection, 1, method, params)
}

func (fixture *appearanceBrowserFixture) exchange(ctx context.Context, connection *websocket.Conn, id int, method string, params any) (map[string]json.RawMessage, error) {
	connection.SetReadLimit(2 << 20)
	deadline, _ := ctx.Deadline()
	_ = connection.SetReadDeadline(deadline)
	_ = connection.SetWriteDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if err := connection.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		var response struct {
			ID     int                        `json:"id"`
			Result map[string]json.RawMessage `json:"result"`
			Error  json.RawMessage            `json:"error"`
		}
		if err := connection.ReadJSON(&response); err != nil {
			return nil, err
		}
		if response.ID != id {
			continue
		}
		if len(response.Error) != 0 {
			return nil, fmt.Errorf("%s: %s", method, response.Error)
		}
		return response.Result, nil
	}
}

func (fixture *appearanceBrowserFixture) command(method string, params any) map[string]json.RawMessage {
	fixture.t.Helper()
	if fixture.connection == nil {
		connection, _, err := fixture.dialer.DialContext(fixture.ctx, fixture.debugger, nil)
		if err != nil {
			fixture.t.Fatal(err)
		}
		fixture.connection = connection
	}
	fixture.nextID++
	result, err := fixture.exchange(fixture.ctx, fixture.connection, fixture.nextID, method, params)
	if err != nil {
		fixture.t.Fatalf("browser %s: %v", method, err)
	}
	return result
}

func (fixture *appearanceBrowserFixture) reset() {
	fixture.t.Helper()
	fixture.eval(`(() => { globalThis.__CODEX_COMPANION_APPEARANCE__?.cleanup(); globalThis.fixtureRestoreInstrumentation?.(); return {ok:true}; })()`)
	fixture.command("Emulation.setEmulatedMedia", map[string]any{"features": []any{}})
	result := fixture.command("Page.getFrameTree", nil)
	var tree struct {
		Frame struct {
			ID string `json:"id"`
		} `json:"frame"`
	}
	if err := json.Unmarshal(result["frameTree"], &tree); err != nil {
		fixture.t.Fatal(err)
	}
	fixture.command("Page.setDocumentContent", map[string]any{"frameId": tree.Frame.ID, "html": appearanceBrowserHTML})
	fixture.eval(`(() => { delete globalThis.__CODEX_COMPANION_APPEARANCE__; delete globalThis.__CODEX_COMPANION_APPEARANCE_LEASE__; ` + appearanceBrowserInstrumentation + `; return {ok:true}; })()`)
}

func (fixture *appearanceBrowserFixture) eval(source string) map[string]any {
	fixture.t.Helper()
	result := fixture.command("Runtime.evaluate", map[string]any{"expression": source, "returnByValue": true, "awaitPromise": true})
	if exception := result["exceptionDetails"]; len(exception) != 0 {
		fixture.t.Fatalf("browser fixture assertion: %s", exception)
	}
	var object struct {
		Value map[string]any `json:"value"`
	}
	if err := json.Unmarshal(result["result"], &object); err != nil {
		fixture.t.Fatal(err)
	}
	return object.Value
}

func (fixture *appearanceBrowserFixture) assert(source string) {
	fixture.t.Helper()
	fixture.eval(`(async () => {
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  const equal = (actual, expected, message) => assert(JSON.stringify(actual) === JSON.stringify(expected), message + ": " + JSON.stringify(actual));
  const frame = () => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  const rgb = (value) => value.match(/[\d.]+/g).slice(0,3).map(Number);
  const luminance = (rgb) => rgb.map((value) => { const normalized = value / 255; return normalized <= 0.04045 ? normalized / 12.92 : Math.pow((normalized + 0.055) / 1.055,2.4); })
    .reduce((sum,value,index) => sum + value * [0.2126,0.7152,0.0722][index],0);
  const contrastRatio = (foreground,background) => {
    const text=luminance(foreground), surface=luminance(background);
    return (Math.max(text,surface)+0.05)/(Math.min(text,surface)+0.05);
  };
  const paintedText = (cssColor, background) => {
    const canvas=document.createElement('canvas'); canvas.width=canvas.height=1;
    const context=canvas.getContext('2d');
    context.fillStyle='rgb(' + background.join(',') + ')'; context.fillRect(0,0,1,1);
    context.fillStyle=cssColor; context.fillRect(0,0,1,1);
    return [...context.getImageData(0,0,1,1).data].slice(0,3);
  };
  const themeContrast = () => {
    const surface = rgb(getComputedStyle(document.querySelector('.thread-scroll-container')).backgroundColor);
    for (const id of ["secondary","muted"]) assert(contrastRatio(paintedText(getComputedStyle(document.getElementById(id)).color,surface),surface) >= 4.5,id + " text remains readable");
    assert(contrastRatio(paintedText(getComputedStyle(document.getElementById('icon')).color,surface),surface) >= 3,"icon remains distinguishable");
    for (const id of ["composer","composer-label","control-label","popup-label","primary-action"]) {
      const css=getComputedStyle(document.getElementById(id));
      const background=rgb(css.backgroundColor);
      assert(contrastRatio(paintedText(css.color,background),background) >= 4.5,id + " text against its actual opaque surface remains readable");
    }
  };
  const wallpaperContrast = () => {
    const foregrounds = [document.body,document.getElementById('secondary')].map((node) => getComputedStyle(node).color);
    const imageCSS = getComputedStyle(document.querySelector('.thread-scroll-container')).backgroundImage;
    const scrim = imageCSS.match(/rgba?\(([^)]+)\)/)[1].match(/[\d.]+/g).map(Number);
    const opacity = scrim.length === 4 ? scrim[3] : 1;
    for (const extreme of [0,255]) {
      const surface = scrim.slice(0,3).map((channel) => channel * opacity + extreme * (1-opacity));
      for (const cssColor of foregrounds) {
        const foreground=paintedText(cssColor,surface);
        const ratio=contrastRatio(foreground,surface);
        assert(ratio >= 4.5,"computed wallpaper scrim contrast " + ratio.toFixed(3) + " < 4.5; foreground=" + foreground + "; scrim=" + scrim + "; image pixel=" + extreme);
      }
    }
  };
` + source + `; return {ok:true}; })()`)
}

func appearanceBrowserRequest(settings AppearanceSettings, revision uint64, dataURL string) string {
	return appearanceApplyScript(rendererFixtureOwner, 1, AppearanceRuntimeRequest{Settings: settings, TargetID: "fixture-main", Revision: revision, DataURL: dataURL})
}

func appearanceBrowserPNG(t *testing.T) string {
	t.Helper()
	bitmap := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	bitmap.Set(0, 0, color.NRGBA{R: 255, A: 255})
	bitmap.Set(1, 0, color.NRGBA{G: 255, A: 255})
	bitmap.Set(2, 0, color.NRGBA{B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

func TestAppearanceBrowserThemeCascadeRestorationAndWorkflowPreservation(t *testing.T) {
	fixture := newAppearanceBrowserFixture(t)
	fixture.reset()
	mint := DefaultAppearanceSettings()
	mint.Theme = "mint"
	dark := mint
	dark.Theme = "dark"
	fixture.assert(`const initial = fixtureState();
themeContrast();
equal(initial.focus,"composer","fixture focus"); equal(initial.selection,[10,18,"backward"],"fixture selection");
assert(document.querySelector('.thread-scroll-container').getBoundingClientRect().height === 500,"real thread geometry");
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(mint, 1, "")) + `)).status,"applied","mint applies");
equal(getComputedStyle(document.documentElement).getPropertyValue('--app-color-background-surface').trim(),"#DEF3E5","mint light palette");
themeContrast();
equal(fixtureState(),initial,"theme preserves draft, selection, focus, scroll, code, diff, approval and native styles");
document.documentElement.dataset.theme = "dark"; await frame();
equal(getComputedStyle(document.documentElement).getPropertyValue('--app-color-background-surface').trim(),"#1C3024","mint follows native dark");
themeContrast();
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 1, mint)) + `)).status,"applied","dark change remains verified");
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(dark, 2, "")) + `)).status,"applied","explicit dark applies");
document.documentElement.dataset.theme = "light"; await frame();
equal(getComputedStyle(document.documentElement).getPropertyValue('--app-color-background-surface').trim(),"#151C18","explicit dark remains dark");
themeContrast();
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(DefaultAppearanceSettings(), 3, "")) + `)).status,"native","native settings restore the official palette");
equal(fixtureCounts(),{styles:0,markers:0,listeners:0,observers:0},"native settings release owned DOM and subscriptions");
equal((await eval(` + JSONLiteral(appearanceCleanupScript(rendererFixtureOwner, 1)) + `)).status,"native","restore confirms native");
equal(getComputedStyle(document.documentElement).getPropertyValue('--app-color-background-surface').trim(),"#FFFFFF","native light returns");
themeContrast();
equal(fixtureState(),initial,"restore preserves original workflow and style ownership");
equal(fixtureCounts(),{styles:0,markers:0,listeners:0,observers:0},"restore releases owned DOM and subscriptions");`)
	fixture.assert(`equal((await eval(` + JSONLiteral(appearanceBrowserRequest(mint, 4, "")) + `)).status,"applied","theme reactivation applies");
document.documentElement.dataset.codexWindowType='extension'; await frame();
equal(fixtureCounts(),{styles:0,markers:0,listeners:0,observers:0},"window changing to extension releases its previous appearance");
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 4, mint)) + `)).status,"missing","extension window no longer claims an appearance");
delete document.documentElement.dataset.codexWindowType;
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(mint, 5, "")) + `)).status,"applied","ordinary window reactivation applies");
document.documentElement.dataset.theme='unknown'; await frame();
equal(fixtureCounts(),{styles:0,markers:0,listeners:0,observers:0},"unsupported native theme releases appearance");
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 5, mint)) + `)).status,"missing","unknown native theme fails closed");`)
}

func TestAppearanceBrowserBackgroundDecodeRollbackAndMediaPreferences(t *testing.T) {
	fixture := newAppearanceBrowserFixture(t)
	fixture.reset()
	imageSettings := DefaultAppearanceSettings()
	imageSettings.BackgroundMode = "local-image"
	imageSettings.OverlayOpacity = 65
	imageData := appearanceBrowserPNG(t)
	unsupported := imageSettings
	unsupported.ReadingLayout = "comfortable"
	fixture.assert(`const initial = fixtureState(), thread = document.querySelector('.thread-scroll-container');
const image = new Image(); image.src = ` + JSONLiteral(imageData) + `; await image.decode();
equal([image.naturalWidth,image.naturalHeight],[3,2],"Chromium decodes actual PNG bytes");
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(imageSettings, 1, imageData)) + `)).status,"applied","PNG background applies");
assert(getComputedStyle(thread).backgroundImage.includes(` + JSONLiteral(imageData) + `),"actual computed CSS contains decoded local image");
assert(getComputedStyle(thread).backgroundImage.includes("linear-gradient"),"readability scrim is rendered");
wallpaperContrast();
const accepted = getComputedStyle(thread).backgroundImage;
equal(fixtureState(),initial,"wallpaper preserves native workflow and content islands");
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(imageSettings, 2, "data:image/png;base64,broken")) + `)).status,"invalidImage","corrupt PNG rejected");
equal(getComputedStyle(thread).backgroundImage,accepted,"invalid decode retains accepted image");
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(unsupported, 3, imageData)) + `)).status,"unsupportedLayout","unverified reading layout rejected");
equal(getComputedStyle(thread).backgroundImage,accepted,"unsupported layout retains accepted image");
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 1, imageSettings)) + `)).status,"applied","accepted revision stays live during failed candidates");
document.documentElement.dataset.theme="dark"; await frame();
assert(getComputedStyle(thread).backgroundImage !== accepted,"native theme change recalculates scrim");
wallpaperContrast();
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 1, imageSettings)) + `)).status,"applied","native dark remains verified");`)
	fixture.command("Emulation.setEmulatedMedia", map[string]any{"features": []any{map[string]string{"name": "forced-colors", "value": "active"}}})
	fixture.assert(`await frame(); assert(matchMedia('(forced-colors: active)').matches,"real forced colors preference");
equal(fixtureCounts(),{styles:1,markers:0,listeners:3,observers:1},"forced colors disables wallpaper without duplicate subscriptions");
equal(getComputedStyle(document.querySelector('.thread-scroll-container')).backgroundImage,"none","forced colors removes the computed background image");
equal(document.querySelector('style[data-codex-companion-appearance]').textContent,"","forced colors releases custom palette and image CSS");`)
	fixture.command("Emulation.setEmulatedMedia", map[string]any{"features": []any{map[string]string{"name": "prefers-reduced-motion", "value": "reduce"}}})
	fixture.assert(`await frame(); assert(matchMedia('(prefers-reduced-motion: reduce)').matches,"real reduced motion preference");
equal(fixtureCounts(),{styles:1,markers:1,listeners:3,observers:1},"media changes keep subscriptions bounded");
assert(getComputedStyle(document.querySelector('.thread-scroll-container')).backgroundImage.includes(` + JSONLiteral(imageData) + `),"wallpaper returns after forced colors ends");
const css = document.querySelector('style[data-codex-companion-appearance]').textContent;
assert(!/animation|transition/.test(css),"static wallpaper adds no motion under reduced motion");
equal((await eval(` + JSONLiteral(appearanceCleanupScript(rendererFixtureOwner, 1)) + `)).status,"native","media cleanup confirms native");
equal(fixtureCounts(),{styles:0,markers:0,listeners:0,observers:0},"media cleanup releases all subscriptions");`)
}

func TestAppearanceBrowserNativeTranslucentTextOverWallpaper(t *testing.T) {
	fixture := newAppearanceBrowserFixture(t)
	settings := DefaultAppearanceSettings()
	settings.BackgroundMode = "local-image"
	settings.OverlayOpacity = 65
	imageData := appearanceBrowserPNG(t)
	for _, format := range []string{"rgba", "color-mix"} {
		t.Run(format, func(t *testing.T) {
			fixture.t = t
			fixture.reset()
			light, dark := "rgba(0,0,0,.65)", "rgba(255,255,255,.65)"
			if format == "color-mix" {
				light, dark = "color-mix(in srgb, rgb(0,0,0) 65%, transparent)", "color-mix(in srgb, rgb(255,255,255) 65%, transparent)"
			}
			css := ":root[data-theme=light]{--app-color-text-foreground-secondary:" + light + "}:root[data-theme=dark]{--app-color-text-foreground-secondary:" + dark + "}"
			fixture.assert(`document.querySelector('style[data-codex-app-themes]').textContent += ` + JSONLiteral(css) + `;
const secondary=document.getElementById('secondary'), initialColor=getComputedStyle(secondary).color;
themeContrast();
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(settings, 1, imageData)) + `)).status,"applied","native translucent text wallpaper applies");
equal(getComputedStyle(secondary).color,initialColor,"wallpaper preserves native translucent foreground");
wallpaperContrast();
document.documentElement.dataset.theme='dark'; await frame();
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 1, settings)) + `)).status,"applied","native translucent dark theme remains supported");
wallpaperContrast();
equal((await eval(` + JSONLiteral(appearanceCleanupScript(rendererFixtureOwner, 1)) + `)).status,"native","translucent native text cleanup");
equal(fixtureCounts(),{styles:0,markers:0,listeners:0,observers:0},"translucent text effects release owned state");`)
		})
	}
	fixture.t = t
}

func TestAppearanceBrowserBackgroundFailClosedAndPriorThemeRollback(t *testing.T) {
	fixture := newAppearanceBrowserFixture(t)
	mint := DefaultAppearanceSettings()
	mint.Theme = "mint"
	background := mint
	background.BackgroundMode = "solid"
	for _, mode := range []string{"opaque-descendant", "opaque-pseudo", "ambiguous", "foreign-marker", "missing-tokens"} {
		t.Run(mode, func(t *testing.T) {
			fixture.t = t
			fixture.reset()
			mutation := map[string]string{
				"opaque-descendant": `thread.classList.add('covered'); const cover=document.createElement('div'); cover.className='opaque'; thread.append(cover);`,
				"opaque-pseudo":     `thread.classList.add('covered','pseudo');`,
				"ambiguous":         `const copy=thread.cloneNode(false); copy.style.cssText='position:fixed;left:800px;top:0;width:180px;height:150px'; document.querySelector('main').append(copy);`,
				"foreign-marker":    `thread.setAttribute('data-codex-companion-background','foreign');`,
				"missing-tokens":    `const sheet=document.querySelector('style[data-codex-app-themes]'); sheet.textContent=sheet.textContent.replace(/--app-color-[\w-]+:[^;}]+;?/g,'');`,
			}[mode]
			baseline := mint
			if mode == "missing-tokens" {
				// Mint supplies the color tokens itself, so native background is
				// the candidate that must reject a missing native palette.
				background.Theme = "native"
			} else {
				background.Theme = "mint"
			}
			fixture.assert(`equal((await eval(` + JSONLiteral(appearanceBrowserRequest(baseline, 1, "")) + `)).status,"applied","prior theme applies");
const thread=document.querySelector('.thread-scroll-container'); ` + mutation + `
await frame();
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(background, 2, "")) + `)).status,"unsupported","unexposed or ambiguous background rejected");
equal(document.querySelectorAll('style[data-codex-companion-appearance]').length,1,"failed candidate restores single prior stylesheet");
equal(getComputedStyle(document.documentElement).getPropertyValue('--app-color-background-surface').trim(),"#DEF3E5","failed candidate restores prior theme");
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 2, baseline)) + `)).status,"applied","rollback is confirmed under candidate watermark");
equal(fixtureCounts().listeners,3,"rollback subscriptions bounded");
equal((await eval(` + JSONLiteral(appearanceCleanupScript(rendererFixtureOwner, 1)) + `)).status,"native","rollback cleanup");
equal(document.querySelector('#foreign-style').textContent,'.foreign { color: purple; }',"foreign stylesheet preserved");`)
			if mode == "foreign-marker" {
				fixture.assert(`equal(document.querySelector('.thread-scroll-container').getAttribute('data-codex-companion-background'),'foreign',"foreign marker preserved");`)
			}
		})
	}
	fixture.t = t
}

func TestAppearanceBrowserLateDecodedCandidateCannotReviveStoppedOrNewerEffect(t *testing.T) {
	fixture := newAppearanceBrowserFixture(t)
	imageSettings := DefaultAppearanceSettings()
	imageSettings.BackgroundMode = "local-image"
	mint := DefaultAppearanceSettings()
	mint.Theme = "mint"
	imageData := appearanceBrowserPNG(t)
	for _, newer := range []bool{false, true} {
		t.Run(fmt.Sprintf("newer=%t", newer), func(t *testing.T) {
			fixture.t = t
			fixture.reset()
			fixture.assert(`const originalDecode=HTMLImageElement.prototype.decode;
let release; const gate=new Promise((resolve)=>{ release=resolve; });
HTMLImageElement.prototype.decode=async function(){ await originalDecode.call(this); await gate; };
globalThis.fixtureReleaseDecode=release; globalThis.fixtureOriginalDecode=originalDecode;
globalThis.fixturePendingApply=eval(` + JSONLiteral(appearanceBrowserRequest(imageSettings, 1, imageData)) + `);
await frame(); equal(fixtureCounts().styles,0,"pending real decode has no effect");`)
			if newer {
				fixture.assert(`equal((await eval(` + JSONLiteral(appearanceBrowserRequest(mint, 2, "")) + `)).status,"applied","newer theme applies during image decode");`)
			} else {
				fixture.assert(`equal((await eval(` + JSONLiteral(appearanceCleanupScript(rendererFixtureOwner, 1)) + `)).status,"native","stop while decode pending");`)
			}
			fixture.assert(`fixtureReleaseDecode(); equal((await fixturePendingApply).status,"stopped","late decode cannot claim effect");
HTMLImageElement.prototype.decode=fixtureOriginalDecode; delete globalThis.fixturePendingApply;
assert(!getComputedStyle(document.querySelector('.thread-scroll-container')).backgroundImage.includes('data:'),"late image never becomes visible");`)
			if newer {
				fixture.assert(`equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 2, mint)) + `)).status,"applied","newer theme remains accepted");
equal((await eval(` + JSONLiteral(appearanceCleanupScript(rendererFixtureOwner, 1)) + `)).status,"native","newer theme cleanup");`)
			}
			fixture.assert(`equal(fixtureCounts(),{styles:0,markers:0,listeners:0,observers:0},"late cancellation leaves no DOM or subscription residue");`)
		})
	}
	fixture.t = t
}

func TestAppearanceBrowserTwentyCyclesKeepDOMAndSubscriptionsBounded(t *testing.T) {
	fixture := newAppearanceBrowserFixture(t)
	fixture.reset()
	settings := DefaultAppearanceSettings()
	settings.Theme = "mint"
	settings.BackgroundMode = "solid"
	for revision := uint64(1); revision <= 20; revision++ {
		fixture.assert(`const before=fixtureState();
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(settings, revision, "")) + `)).status,"applied","cycle applies");
equal(fixtureCounts(),{styles:1,markers:1,listeners:3,observers:1},"single active ownership in cycle");
equal(fixtureState(),before,"cycle preserves composer and protected content");
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(settings, revision, "")) + `)).status,"applied","replacement in cycle applies");
equal(fixtureCounts(),{styles:1,markers:1,listeners:3,observers:1},"replacement releases the previous effect's subscriptions");
equal((await eval(` + JSONLiteral(appearanceCleanupScript(rendererFixtureOwner, 1)) + `)).status,"native","cycle cleanup confirms native");
equal(fixtureCounts(),{styles:0,markers:0,listeners:0,observers:0},"cycle returns to zero ownership");
equal(fixtureState(),before,"cycle restores original workflow and styles");`)
	}
}

func TestAppearanceBrowserSameRevisionRestoreRejectsLateImageAndMismatchedProbe(t *testing.T) {
	fixture := newAppearanceBrowserFixture(t)
	fixture.reset()
	mint := DefaultAppearanceSettings()
	mint.Theme = "mint"
	imageSettings := DefaultAppearanceSettings()
	imageSettings.BackgroundMode = "local-image"
	imageData := appearanceBrowserPNG(t)
	imageBytes, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(imageData, "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	imageSettings.ImageAssetID = stringPointer(SecureFingerprintBytes(imageBytes))
	fixture.assert(`equal((await eval(` + JSONLiteral(appearanceBrowserRequest(mint, 1, "")) + `)).status,"applied","saved mint is visible");
const initial=fixtureState(), originalDecode=HTMLImageElement.prototype.decode;
let release, decoded; const gate=new Promise((resolve)=>{release=resolve;});
const decoding=new Promise((resolve)=>{decoded=resolve;});
HTMLImageElement.prototype.decode=async function(){await originalDecode.call(this); decoded(); await gate;};
const pending=eval(` + JSONLiteral(appearanceBrowserRequest(imageSettings, 2, imageData)) + `);
await decoding;
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 1, mint)) + `)).status,"applied","decoding candidate leaves saved mint active");
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(DefaultAppearanceSettings(), 2, "")) + `)).status,"native","same-revision native barrier retires the candidate");
equal((await eval(` + JSONLiteral(appearanceBrowserRequest(mint, 2, "")) + `)).status,"applied","same-revision saved mint restoration applies");
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 2, imageSettings)) + `)).status,"stale","different settings cannot be confirmed at the same revision");
release(); equal((await pending).status,"stopped","late decoded image cannot replace restored mint at the same revision");
HTMLImageElement.prototype.decode=originalDecode;
equal((await eval(` + JSONLiteral(appearanceProbeScript(rendererFixtureOwner, 1, "fixture-main", 2, mint)) + `)).status,"applied","restored saved settings remain confirmed");
equal(getComputedStyle(document.querySelector('.thread-scroll-container')).backgroundImage,"none","late image never paints");
equal(fixtureState(),initial,"same-revision recovery preserves composer and protected content");
equal(fixtureCounts(),{styles:1,markers:0,listeners:3,observers:1},"same-revision recovery retains one owned effect");
equal((await eval(` + JSONLiteral(appearanceCleanupScript(rendererFixtureOwner, 1)) + `)).status,"native","same-revision recovery cleanup");
equal(fixtureCounts(),{styles:0,markers:0,listeners:0,observers:0},"same-revision recovery leaves no owned residue");`)
}
