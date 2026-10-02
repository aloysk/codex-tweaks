package core

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This synthetic DOM executes the actual package activation and cleanup scripts.
// It has no browser, network, official process, user draft or external assets.
const rendererDOMFixture = `
const assert = require("node:assert/strict");
const elements = new Set();
class Element {
  constructor(tag) { this.tagName = tag; this.id = ""; this.style = {}; this.children = []; this.parent = null; this.attributes = new Map(); elements.add(this); }
  appendChild(child) { child.remove(); this.children.push(child); child.parent = this; return child; }
  remove() { if (this.parent) this.parent.children = this.parent.children.filter((item) => item !== this); this.parent = null; }
  setAttribute(name,value) { this.attributes.set(name,value); }
  get isConnected() { return this === html || Boolean(this.parent?.isConnected); }
}
const html = new Element("html");
const head = new Element("head"); const body = new Element("body");
html.appendChild(head); html.appendChild(body);
globalThis.document = {
  documentElement: html, head, body,
  createElement: (tag) => new Element(tag),
  getElementById: (id) => [...elements].find((element) => element.isConnected && element.id === id) ?? null,
  querySelectorAll: () => []
};
const connectedCount = () => [...elements].filter((element) => element.isConnected).length;
`

func runRendererDOMFixture(t *testing.T, source string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required to execute the synthetic renderer fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := newNodeRuntimeCommand(ctx, node, "-")
	command.Stdin = strings.NewReader(rendererDOMFixture + "\n(async () => {\n" + source + "\n})().catch((error) => { console.error(error); process.exitCode = 1; });")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic renderer: %v\n%s", err, output)
	}
}

func TestRendererCleanupPropagatesCallbackFailureAndRetriesOnlyFailures(t *testing.T) {
	payload := Payload{Version: "cleanup", Packages: []CompiledPackage{{ID: "sample", Name: "sample", Version: "1", JavaScript: `
module.exports.activate = ({ onCleanup }) => {
  globalThis.callbackAttempts = 0; globalThis.successfulCleanupCalls = 0;
  onCleanup(() => { if (++globalThis.callbackAttempts === 1) throw new Error("fixture cleanup failed"); });
  return async () => { globalThis.successfulCleanupCalls++; };
};`}}}
	source := strings.Join([]string{
		"assert.equal((await eval(" + JSONLiteral(runtimeLeaseClaimScript(rendererFixtureOwner, 1)) + " )).status, 'claimed');",
		"assert.equal((await eval(" + JSONLiteral(InjectionScript(payload, 0)) + " )).status, 'injected');",
		"const failed = await eval(" + JSONLiteral(CleanupScript) + ");",
		"assert.equal(failed.status, 'cleanupFailed'); assert.equal(failed.errors[0].message, 'fixture cleanup failed'); assert.ok(document.getElementById('codex-tweaks-root'));",
		"assert.equal((await eval(" + JSONLiteral(CleanupScript) + ")).status, 'cleaned');",
		"assert.equal(globalThis.callbackAttempts,2); assert.equal(globalThis.successfulCleanupCalls,1); assert.equal(connectedCount(),3); assert.equal(globalThis.__CODEX_TWEAKS__,undefined);",
	}, "\n")
	runRendererDOMFixture(t, source)
}

func TestRendererLeaseBlocksLateInjectionAndRemainsBoundedAcrossTwentyCycles(t *testing.T) {
	payload := Payload{Version: "lease", Packages: []CompiledPackage{{ID: "sample", Name: "sample", Version: "1", JavaScript: `module.exports.activate = () => { globalThis.activations = (globalThis.activations ?? 0) + 1; };`}}}
	late := injectionScriptOwned(payload, 0, "", nil, nil, rendererFixtureOwner, 1)
	source := "await eval(" + JSONLiteral(runtimeLeaseClaimScript(rendererFixtureOwner, 1)) + ");\n"
	source += "assert.equal((await eval(" + JSONLiteral(cleanupScriptOwned(rendererFixtureOwner, 1)) + ")).status,'cleaned');\n"
	source += "await assert.rejects(() => eval(" + JSONLiteral(late) + "), /lease has stopped/); assert.equal(globalThis.activations,undefined);\n"
	for epoch := uint64(2); epoch <= 21; epoch++ {
		source += "await eval(" + JSONLiteral(runtimeLeaseClaimScript(rendererFixtureOwner, epoch)) + ");\n"
		source += "await eval(" + JSONLiteral(injectionScriptOwned(payload, 0, "", nil, nil, rendererFixtureOwner, epoch)) + ");\n"
		source += "assert.equal((await eval(" + JSONLiteral(cleanupScriptOwned(rendererFixtureOwner, epoch)) + ")).status,'cleaned'); assert.equal(connectedCount(),3);\n"
	}
	source += "assert.equal(globalThis.activations,20); assert.deepEqual(Object.keys(globalThis.__CODEX_COMPANION_RUNTIME_LEASE__).sort(), ['active','epoch','instance']); assert.equal(globalThis.__CODEX_COMPANION_RUNTIME_LEASE__.active,false); assert.equal(Object.keys(globalThis).filter((key)=>key==='__CODEX_COMPANION_RUNTIME_LEASE__').length,1);\n"
	source += "assert.equal((await eval(" + JSONLiteral(runtimeLeaseClaimScript(rendererFixtureOwner, 1)) + ")).status,'stopped'); await assert.rejects(() => eval(" + JSONLiteral(late) + "), /lease has stopped/); assert.equal(connectedCount(),3);"
	runRendererDOMFixture(t, source)
}

func TestRendererStopsAnAwaitingActivationWithoutClaimingEarlyCleanup(t *testing.T) {
	payload := Payload{Version: "pending", Packages: []CompiledPackage{{ID: "sample", Name: "sample", Version: "1", JavaScript: `module.exports.activate = async ({onCleanup}) => { await new Promise((resolve) => { globalThis.releaseActivation = resolve; }); onCleanup(() => { globalThis.cleanedAfterActivation = true; }); };`}}}
	source := "await eval(" + JSONLiteral(runtimeLeaseClaimScript(rendererFixtureOwner, 1)) + ");\n"
	source += "const injecting = eval(" + JSONLiteral(InjectionScript(payload, 0)) + "); await new Promise(setImmediate);\n"
	source += "assert.equal((await eval(" + JSONLiteral(CleanupScript) + ")).status, 'cleanupPending'); globalThis.releaseActivation(); await injecting;\n"
	source += "assert.equal(globalThis.cleanedAfterActivation,true); assert.equal((await eval(" + JSONLiteral(CleanupScript) + ")).status,'cleaned'); assert.equal(connectedCount(),3);"
	runRendererDOMFixture(t, source)
}

func TestRendererPreservesForeignRuntimeAndLease(t *testing.T) {
	source := `globalThis.foreignCleanupCalls=0; const foreign={ owner:'upstream', cleanup(){ globalThis.foreignCleanupCalls++; } }; globalThis.__CODEX_TWEAKS__=foreign;`
	source += "assert.equal((await eval(" + JSONLiteral(runtimeLeaseClaimScript(rendererFixtureOwner, 1)) + ")).status,'foreign');\n"
	source += "assert.equal((await eval(" + JSONLiteral(CleanupScript) + ")).status,'foreign'); assert.equal(globalThis.__CODEX_TWEAKS__,foreign); assert.equal(globalThis.foreignCleanupCalls,0); assert.equal(globalThis.__CODEX_COMPANION_RUNTIME_LEASE__,undefined);\n"
	source += "delete globalThis.__CODEX_TWEAKS__; const lease={instance:'other-companion',epoch:1,active:false}; globalThis.__CODEX_COMPANION_RUNTIME_LEASE__=lease; assert.equal((await eval(" + JSONLiteral(runtimeLeaseClaimScript(rendererFixtureOwner, 2)) + ")).status,'foreign'); assert.equal(globalThis.__CODEX_COMPANION_RUNTIME_LEASE__,lease);"
	runRendererDOMFixture(t, source)
}

func TestRendererReplacementPreservesOwnedBridgeUntilStop(t *testing.T) {
	payload := Payload{Version: "first", Packages: []CompiledPackage{{ID: "sample", Name: "sample", Version: "1", Node: &CompiledPackageNode{AuthorizationID: "synthetic"}, JavaScript: `module.exports.activate = ({node}) => { if (typeof node.invoke !== 'function') throw new Error('missing bridge'); };`}}}
	source := `const binding=()=>{}; Object.defineProperty(binding,'__codexTweaksOwner',{value:'synthetic-fixture'}); globalThis.__codexTweaksHostBridge=binding;`
	source += "await eval(" + JSONLiteral(runtimeLeaseClaimScript(rendererFixtureOwner, 1)) + "); await eval(" + JSONLiteral(injectionScriptOwned(payload, 0, "bridge", map[string]string{"sample": "token"}, nil, rendererFixtureOwner, 1)) + ");\n"
	payload.Version = "second"
	source += "assert.equal((await eval(" + JSONLiteral(injectionScriptOwned(payload, 0, "bridge", map[string]string{"sample": "token"}, nil, rendererFixtureOwner, 1)) + ")).status,'injected'); assert.equal(globalThis.__codexTweaksHostBridge,binding);\n"
	source += "assert.equal((await eval(" + JSONLiteral(CleanupScript) + ")).status,'cleaned'); assert.equal(globalThis.__codexTweaksHostBridge,undefined); assert.equal(connectedCount(),3);"
	runRendererDOMFixture(t, source)
}
