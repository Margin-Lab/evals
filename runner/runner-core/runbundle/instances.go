package runbundle

import (
	"fmt"
	"strings"
)

func BuildInstanceKey(caseID string, sampleIndex int) string {
	return fmt.Sprintf("%s#%d", strings.TrimSpace(caseID), sampleIndex)
}

func BuildInstanceSpecs(cases []Case, samplesPerCase int) []InstanceSpec {
	if samplesPerCase <= 0 {
		return nil
	}
	instances := make([]InstanceSpec, 0, len(cases)*samplesPerCase)
	for caseOrdinal, c := range cases {
		caseID := strings.TrimSpace(c.CaseID)
		for sampleIndex := 1; sampleIndex <= samplesPerCase; sampleIndex++ {
			instances = append(instances, InstanceSpec{
				InstanceKey: BuildInstanceKey(caseID, sampleIndex),
				CaseID:      caseID,
				CaseOrdinal: caseOrdinal,
				SampleIndex: sampleIndex,
				SampleCount: samplesPerCase,
			})
		}
	}
	return instances
}
