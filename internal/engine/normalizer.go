package engine

import (
	"sort"

	"lotsman/internal/model"
)

// sortSignals orders signals ascending by timestamp (timeline order).
func sortSignals(sigs []model.Signal) {
	sort.SliceStable(sigs, func(i, j int) bool {
		return sigs[i].Timestamp.Before(sigs[j].Timestamp)
	})
}

// maxSeverity returns the highest severity across signals (the incident severity).
func maxSeverity(sigs []model.Signal) model.Severity {
	sev := model.SeverityInfo
	for _, s := range sigs {
		if s.Severity > sev {
			sev = s.Severity
		}
	}
	return sev
}
