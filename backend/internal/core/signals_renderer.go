package core

import (
	"context"
	"encoding/json"
	"errors"
)

func (s *CDPService) readSignals(ctx context.Context, readQuota bool) (signalsObservation, error) {
	if err := lockWithContext(ctx, &s.mu); err != nil {
		return signalsObservation{}, err
	}
	defer s.mu.Unlock()
	if s.stopped {
		return signalsObservation{}, context.Canceled
	}
	targets, err := s.discoverTargets(ctx)
	if err != nil {
		return signalsObservation{}, err
	}
	if len(targets) != 1 {
		return signalsObservation{}, errors.New("signals require one verified renderer")
	}
	target := targets[0]
	value, err := s.evaluate(ctx, signalsProbeScript(readQuota), *target.WebSocketDebuggerURL)
	if err != nil {
		return signalsObservation{}, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return signalsObservation{}, err
	}
	var observation signalsObservation
	if err := json.Unmarshal(data, &observation); err != nil {
		return observation, err
	}
	observation.TargetID = target.ID
	return observation, nil
}

// Observe existing query state only: no imports, subscriptions, cache writes,
// network requests, DOM changes, timers, text extraction or account persistence.
func signalsProbeScript(readQuota bool) string {
	return `(async () => {
  const readQuota = ` + JSONLiteral(readQuota) + `;
  const unavailable = { status: "unavailable", scope: "", windows: [] };
  try {
    const clients = new Set(), seen = new Set(), stack = [];
    for (const element of [document.documentElement, document.body, ...Array.from(document.querySelectorAll("#root,nav")).slice(0, 8)]) {
      if (!element) continue;
      for (const key of Object.getOwnPropertyNames(element)) {
        if (!key.startsWith("__reactFiber$") && !key.startsWith("__reactContainer$")) continue;
        let node = element[key]?.current ?? element[key];
        let budget = 100;
        while (node?.return && budget-- > 0) node = node.return;
        if (budget > 0 && node) stack.push(node);
      }
    }
    const add = value => { if (value && typeof value.getQueryCache === "function") clients.add(value); };
    let budget = 2000;
    while (stack.length && budget-- > 0) {
      const node = stack.pop(); if (!node || seen.has(node)) continue; seen.add(node);
      add(node.memoizedProps?.client); add(node.memoizedProps?.queryClient);
      let hook = node.memoizedState;
      for (let count = 0; hook && count < 40; count++, hook = hook.next) {
        add(hook.memoizedState);
        if (Array.isArray(hook.memoizedState)) for (const value of hook.memoizedState.slice(0, 8)) add(value);
      }
      if (node.child) stack.push(node.child); if (node.sibling) stack.push(node.sibling);
    }
    // A truncated traversal cannot establish that another account/client is absent.
    if (stack.length || clients.size !== 1) return unavailable;
    const client = [...clients][0];
    const activeQueries = () => {
      const queries = client.getQueryCache()?.getAll?.();
      if (!Array.isArray(queries) || queries.length > 10000) return null;
      return queries.filter(query => {
      const key = query.queryKey;
      return Array.isArray(key) && key[0] === "rate-limit-status" &&
        (key.length === 1 || key.length === 3 && key[1] !== "image-generation" && key.slice(1).every(v => typeof v === "string" && v.length > 0 && v.length <= 256)) && query.isActive?.() === true;
      });
    };
    const active = activeQueries();
    // Unscoped data cannot prove account identity; never retain it across a switch.
    if (!active || active.length !== 1 || active[0].queryKey.length !== 3) return unavailable;
    const query = active[0], state = query.state;
    if (state?.status !== "success") return unavailable;
    const encoded = new TextEncoder().encode(JSON.stringify(query.queryKey));
    const digest = await crypto.subtle.digest("SHA-256", encoded);
    const scope = Array.from(new Uint8Array(digest), v => v.toString(16).padStart(2,"0")).join("");
    // Revalidate after the asynchronous hash so a switching account cannot leak old values.
    const current = activeQueries();
    if (!current || current.length !== 1 || current[0] !== query || query.state !== state) return unavailable;
    const result = {status:"ready", scope, windows:[], cacheUpdatedAt:0};
    if (!readQuota) return result;
    const rate = state?.status === "success" ? state.data?.rate_limit : null;
    if (!rate || typeof rate !== "object") return unavailable;
    for (const window of [rate.primary_window, rate.secondary_window]) {
      const number = value => typeof value === "number" && Number.isFinite(value) ? value : null;
      result.windows.push({usedPercent:number(window?.used_percent), seconds:number(window?.limit_window_seconds), resetAt:number(window?.reset_at)});
    }
    result.cacheUpdatedAt = typeof state.dataUpdatedAt === "number" && Number.isFinite(state.dataUpdatedAt) ? state.dataUpdatedAt : 0;
    return result;
  } catch { return unavailable; }
})()`
}
