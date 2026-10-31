package control

import "controls/internal/telemetry"

func (c *Controller) addResources(record *Execution) error {
	if c.Sessions == nil {
		return nil
	}
	sessions, err := c.Sessions.List(record.ID)
	if err != nil {
		return err
	}
	total := telemetry.Summary{}
	for _, session := range sessions {
		if session.Runner == "hook" {
			continue
		}
		total.Include(session.Resources)
	}
	record.Resources = &total
	for i := range record.Steps {
		summary := telemetry.Summary{}
		for _, session := range sessions {
			if session.StepID == record.Steps[i].ID {
				summary.Include(session.Resources)
			}
		}
		record.Steps[i].Resources = &summary
	}
	return nil
}
