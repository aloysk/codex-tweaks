package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func (c *Controller) CheckAppUpdate(prompt bool) error {
	if !ApplicationUpdatesEnabled {
		err := c.applicationUpdatesUnavailable()
		c.mu.Lock()
		message := err.Error()
		c.updateLastError = &message
		c.mu.Unlock()
		c.emit()
		return err
	}
	c.mu.Lock()
	if c.updateChecking {
		c.mu.Unlock()
		return nil
	}
	c.updateChecking = true
	c.updateLastError = nil
	channel := c.config.UpdateChannel
	currentVersion := c.currentVersion
	c.mu.Unlock()
	c.emit()
	go func() {
		release, err := c.updates.Check(c.ctx, channel, currentVersion)
		c.mu.Lock()
		c.updateChecking = false
		if err != nil {
			message := err.Error()
			if strings.Contains(strings.ToLower(message), "network is unreachable") || strings.Contains(strings.ToLower(message), "no route to host") {
				locale := ResolveAppLanguage(c.config.Language, c.preferredLanguages)
				message = PresentationTextForLocale(locale)["update.networkUnavailable"]
			}
			c.updateLastError = &message
			c.mu.Unlock()
			c.emit()
			return
		}
		c.latestRelease = release
		now := NewCodableTime(time.Now())
		next := c.config
		next.UpdateLastCheckAt = &now
		if release != nil {
			hasNewer := HasNewerVersion(release, c.currentVersion)
			skipped := containsString(c.config.UpdateSkippedVersions, NormalizeVersion(release.TagName))
			if prompt && hasNewer && !skipped {
				copy := *release
				c.pendingUpdate = &copy
			} else if c.pendingUpdate == nil || c.pendingUpdate.TagName != release.TagName || !hasNewer || skipped {
				c.pendingUpdate = nil
			}
		} else {
			c.pendingUpdate = nil
		}
		persistErr := c.persistConfigurationCandidateLocked(next)
		c.mu.Unlock()
		if persistErr != nil {
			c.logger.Error("保存更新状态失败：" + persistErr.Error())
		}
		c.emit()
	}()
	return nil
}

func (c *Controller) applicationUpdatesUnavailable() error {
	return fmt.Errorf("%w: %s", errors.ErrUnsupported, c.presentationText()["update.notConfigured"])
}

func (c *Controller) SetUpdateChannel(channel UpdateChannel) error {
	if !ApplicationUpdatesEnabled {
		return c.applicationUpdatesUnavailable()
	}
	if channel != UpdateBeta {
		channel = UpdateStable
	}
	c.mu.Lock()
	if c.config.UpdateChannel == channel {
		c.mu.Unlock()
		return nil
	}
	next := c.config
	next.UpdateChannel = channel
	if err := c.persistConfigurationCandidateLocked(next); err != nil {
		c.mu.Unlock()
		return err
	}
	c.latestRelease = nil
	c.pendingUpdate = nil
	c.updateLastError = nil
	c.mu.Unlock()
	c.emit()
	return nil
}

func (c *Controller) SetUpdateAutoCheck(enabled bool) error {
	if enabled && !ApplicationUpdatesEnabled {
		return c.applicationUpdatesUnavailable()
	}
	c.mu.Lock()
	next := c.config
	next.UpdateAutoCheck = enabled
	err := c.persistConfigurationCandidateLocked(next)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.emit()
	return nil
}

func (c *Controller) DismissUpdate() {
	c.mu.Lock()
	c.pendingUpdate = nil
	c.mu.Unlock()
	c.emit()
}

func (c *Controller) SkipUpdate(tagName string) error {
	version := NormalizeVersion(tagName)
	c.mu.Lock()
	next := c.config
	if !containsString(next.UpdateSkippedVersions, version) {
		next.UpdateSkippedVersions = append(append([]string(nil), next.UpdateSkippedVersions...), version)
		next.UpdateSkippedVersions = uniqueSorted(next.UpdateSkippedVersions)
	}
	if err := c.persistConfigurationCandidateLocked(next); err != nil {
		c.mu.Unlock()
		return err
	}
	c.pendingUpdate = nil
	c.mu.Unlock()
	c.emit()
	return nil
}

func (c *Controller) UnskipAndPromptUpdate() error {
	c.mu.Lock()
	if c.latestRelease == nil || !HasNewerVersion(c.latestRelease, c.currentVersion) {
		c.mu.Unlock()
		return nil
	}
	version := NormalizeVersion(c.latestRelease.TagName)
	filtered := []string{}
	for _, skipped := range c.config.UpdateSkippedVersions {
		if skipped != version {
			filtered = append(filtered, skipped)
		}
	}
	next := c.config
	next.UpdateSkippedVersions = filtered
	if err := c.persistConfigurationCandidateLocked(next); err != nil {
		c.mu.Unlock()
		return err
	}
	copy := *c.latestRelease
	c.pendingUpdate = &copy
	c.mu.Unlock()
	c.emit()
	return nil
}
