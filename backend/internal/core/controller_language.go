package core

import "fmt"

func (c *Controller) SetLanguage(language AppLanguage) error {
	if normalized := NormalizeAppLanguage(language); normalized != language {
		return fmt.Errorf("unsupported app language: %s", language)
	}
	c.mu.Lock()
	if c.config.Language == language {
		c.mu.Unlock()
		return nil
	}
	next := c.config
	next.Language = language
	err := c.persistConfigurationCandidateLocked(next)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.emit()
	return nil
}
