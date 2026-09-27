package benchmark

type Comparison struct {
	Baseline         string   `json:"baseline"`
	Candidate        string   `json:"candidate"`
	MeasurementValid bool     `json:"measurement_valid"`
	Comparable       bool     `json:"comparable"`
	Reasons          []string `json:"reasons,omitempty"`
	Deltas           []Delta  `json:"deltas"`
}

type Delta struct {
	Name          string   `json:"name"`
	Case          string   `json:"case,omitempty"`
	Unit          string   `json:"unit"`
	Baseline      float64  `json:"baseline"`
	Candidate     float64  `json:"candidate"`
	Change        float64  `json:"change"`
	ChangePercent *float64 `json:"change_percent,omitempty"`
}

func Compare(baselineDir, candidateDir string) (Comparison, error) {
	base, err := LoadResult(baselineDir)
	if err != nil {
		return Comparison{}, err
	}
	cand, err := LoadResult(candidateDir)
	if err != nil {
		return Comparison{}, err
	}
	c := Comparison{
		Baseline:         baselineDir,
		Candidate:        candidateDir,
		MeasurementValid: base.MeasurementValid && cand.MeasurementValid,
	}
	if !base.MeasurementValid {
		c.Reasons = append(c.Reasons, "baseline measurement is invalid")
	}
	if !cand.MeasurementValid {
		c.Reasons = append(c.Reasons, "candidate measurement is invalid")
	}
	if base.Model.Digest == "" || base.Model.Digest != cand.Model.Digest {
		c.Reasons = append(c.Reasons, "model digests differ or are missing")
	}
	if base.Digests.Workload != cand.Digests.Workload {
		c.Reasons = append(c.Reasons, "workloads differ")
	}
	if base.Runtime.Engine != cand.Runtime.Engine {
		c.Reasons = append(c.Reasons, "engines differ")
	}
	if !sameStrings(base.GPUs, cand.GPUs) {
		c.Reasons = append(c.Reasons, "gpus differ")
	}
	c.Comparable = len(c.Reasons) == 0

	for _, b := range base.Metrics {
		a, ok := cand.Metric(b.Name, b.Case)
		if !ok {
			continue
		}
		d := Delta{Name: b.Name, Case: b.Case, Unit: b.Unit, Baseline: b.Value, Candidate: a.Value, Change: a.Value - b.Value}
		if b.Value != 0 {
			p := d.Change / b.Value * 100
			d.ChangePercent = &p
		}
		c.Deltas = append(c.Deltas, d)
	}
	return c, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
