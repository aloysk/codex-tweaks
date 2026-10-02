package core

// Persist an accepted candidate before publishing it or starting dependent work.
// Callers hold c.mu and keep related transient state unchanged until this succeeds.
func (c *Controller) persistConfigurationCandidateLocked(next AppConfiguration) error {
	next.DisabledPackageIDs = sortedTrueKeys(c.disabledPackageIDs)
	if err := writeJSONAtomic(c.configPath, next); err != nil {
		return err
	}
	c.config = next
	return nil
}
