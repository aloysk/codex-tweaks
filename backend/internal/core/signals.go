package core

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Signals are transient. Account scopes, thread identities and cache contents
// never enter configuration, logs or the native presentation contract.
type SignalPresentation struct {
	Label           string `json:"label"`
	Value           string `json:"value"`
	Status          string `json:"status"`
	Detail          string `json:"detail"`
	Source          string `json:"source"`
	ObservedAt      string `json:"observedAt"`
	CacheUpdatedAt  string `json:"cacheUpdatedAt"`
	SourceUpdatedAt string `json:"sourceUpdatedAt"`
}

type QuotaWindow struct {
	Title     string `json:"title"`
	Remaining string `json:"remaining"`
	ResetAt   string `json:"resetAt"`
}

type CapsuleSettings struct {
	Enabled   bool `json:"enabled"`
	Collapsed bool `json:"collapsed"`
}

type SignalsSnapshot struct {
	Task    SignalPresentation `json:"task"`
	Rate    SignalPresentation `json:"rate"`
	Quota   SignalPresentation `json:"quota"`
	Windows []QuotaWindow      `json:"windows"`
	Capsule CapsuleSettings    `json:"capsule"`
}

type quotaObservationWindow struct {
	UsedPercent *float64 `json:"usedPercent"`
	Seconds     *float64 `json:"seconds"`
	ResetAt     *float64 `json:"resetAt"`
}

type signalsObservation struct {
	TargetID       string                   `json:"targetID"`
	Scope          string                   `json:"scope"`
	Status         string                   `json:"status"`
	CacheUpdatedAt float64                  `json:"cacheUpdatedAt"`
	Windows        []quotaObservationWindow `json:"windows"`
}

// This optional interface preserves synthetic runtime isolation.
type signalsRuntime interface {
	readSignals(context.Context, bool) (signalsObservation, error)
}

type signalsState struct {
	observation signalsObservation
	observedAt  time.Time
	lastRead    time.Time
}

func (c *Controller) SetCapsule(settings CapsuleSettings) error {
	c.mu.Lock()
	if c.shuttingDown {
		c.mu.Unlock()
		return context.Canceled
	}
	next := c.config
	next.Capsule = settings
	err := c.persistConfigurationCandidateLocked(next)
	c.mu.Unlock()
	if err == nil {
		c.emit()
	}
	return err
}

func (c *Controller) refreshSignals(ctx context.Context, epoch uint64) {
	runtime, supported := c.cdp.(signalsRuntime)
	if !supported {
		return
	}
	c.mu.Lock()
	readQuota := c.signals.lastRead.IsZero() || time.Since(c.signals.lastRead) >= time.Minute
	if readQuota {
		c.signals.lastRead = time.Now()
	}
	c.mu.Unlock()
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	observation, err := runtime.readSignals(probeCtx, readQuota)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runtimeEpoch != epoch || c.shuttingDown || !c.config.Enabled {
		return
	}
	if err != nil || observation.Status != "ready" || observation.Scope == "" || observation.TargetID == "" {
		c.signals.observation = signalsObservation{}
		c.signals.observedAt = time.Time{}
		return
	}
	old := c.signals.observation
	changed := old.TargetID != observation.TargetID || old.Scope != observation.Scope
	if changed {
		c.signals.observation = signalsObservation{}
		c.signals.observedAt = time.Time{}
		// Observe the new identity immediately, but keep the cache-read budget.
		// Its values become available at the next scheduled quota read.
	}
	if readQuota {
		c.signals.observation = observation
		c.signals.observedAt = time.Now()
	}
}

func (c *Controller) signalsSnapshotLocked(text map[string]string, now time.Time) SignalsSnapshot {
	unknown := func(label, reason string) SignalPresentation {
		return SignalPresentation{Label: text[label], Value: text["signals.unavailable"], Status: "unavailable", Detail: text[reason], SourceUpdatedAt: text["signals.timeUnknown"]}
	}
	result := SignalsSnapshot{
		Task:    unknown("signals.task", "signals.taskUnsupported"),
		Rate:    unknown("signals.rate", "signals.rateUnsupported"),
		Quota:   unknown("signals.quota", "signals.noSource"),
		Windows: []QuotaWindow{}, Capsule: c.config.Capsule,
	}
	if !c.config.Enabled || c.status.Kind != StatusConnected || c.signals.observedAt.IsZero() {
		return result
	}
	observation := c.signals.observation
	for _, window := range observation.Windows {
		if window.UsedPercent == nil || window.Seconds == nil || !finite(*window.UsedPercent) || !finite(*window.Seconds) || *window.Seconds <= 0 || *window.Seconds > 365*24*60*60 {
			continue
		}
		remaining := math.Max(0, math.Min(100, 100-*window.UsedPercent))
		reset := ""
		if window.ResetAt != nil && finite(*window.ResetAt) && *window.ResetAt > 0 && *window.ResetAt < 253402300799 {
			reset = time.Unix(int64(*window.ResetAt), 0).UTC().Format(time.RFC3339)
		}
		result.Windows = append(result.Windows, QuotaWindow{Title: quotaDuration(*window.Seconds), Remaining: fmt.Sprintf("%.0f%%", remaining), ResetAt: reset})
	}
	if len(result.Windows) == 0 {
		result.Quota.Detail = text["signals.invalidFields"]
		return result
	}
	result.Quota = SignalPresentation{
		Label: text["signals.quota"], Value: result.Windows[0].Title + " · " + result.Windows[0].Remaining,
		Status: "available", Detail: text["signals.quotaDetail"], Source: text["signals.quotaSource"],
		ObservedAt: c.signals.observedAt.UTC().Format(time.RFC3339), SourceUpdatedAt: text["signals.timeUnknown"],
	}
	if finite(observation.CacheUpdatedAt) && observation.CacheUpdatedAt > 0 && observation.CacheUpdatedAt < 253402300799000 {
		result.Quota.CacheUpdatedAt = time.UnixMilli(int64(observation.CacheUpdatedAt)).UTC().Format(time.RFC3339)
	}
	// Re-reading a cache does not make its data newer. An old cache timestamp
	// stays stale even if the most recent read succeeded.
	if now.Sub(c.signals.observedAt) > 120*time.Second || result.Quota.CacheUpdatedAt == "" || now.Sub(time.UnixMilli(int64(observation.CacheUpdatedAt))) > 120*time.Second {
		result.Quota.Status = "stale"
		result.Quota.Value = text["signals.staleMarker"] + " · " + result.Quota.Value
		result.Quota.Detail = text["signals.stale"]
	}
	return result
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func quotaDuration(seconds float64) string {
	if seconds >= 86400 && math.Mod(seconds, 86400) == 0 {
		return fmt.Sprintf("%.0f d", seconds/86400)
	}
	if seconds >= 3600 && math.Mod(seconds, 3600) == 0 {
		return fmt.Sprintf("%.0f h", seconds/3600)
	}
	if seconds >= 60 && math.Mod(seconds, 60) == 0 {
		return fmt.Sprintf("%.0f min", seconds/60)
	}
	return fmt.Sprintf("%.0f s", seconds)
}
