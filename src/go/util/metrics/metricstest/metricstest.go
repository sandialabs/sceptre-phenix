// Package metricstest reads the phenix metrics back in tests.
package metricstest

import (
	"testing"

	dto "github.com/prometheus/client_model/go"

	"phenix/util/metrics"
)

// Value is the sum, over the series of the metric family name whose labels
// include labels, of each counter's or gauge's value, or each histogram's
// observation count.
func Value(tb testing.TB, name string, labels map[string]string) float64 {
	tb.Helper()

	families, err := metrics.Registry.Gather()
	if err != nil {
		tb.Fatalf("gathering metrics: %v", err)
	}

	var sum float64

	for _, family := range families {
		if family.GetName() != name {
			continue
		}

		for _, m := range family.GetMetric() {
			if matches(m, labels) {
				sum += value(m)
			}
		}
	}

	return sum
}

func matches(m *dto.Metric, labels map[string]string) bool {
	found := 0

	for _, pair := range m.GetLabel() {
		want, ok := labels[pair.GetName()]
		if !ok {
			continue
		}

		if pair.GetValue() != want {
			return false
		}

		found++
	}

	return found == len(labels)
}

func value(m *dto.Metric) float64 {
	switch {
	case m.GetCounter() != nil:
		return m.GetCounter().GetValue()
	case m.GetGauge() != nil:
		return m.GetGauge().GetValue()
	case m.GetHistogram() != nil:
		return float64(m.GetHistogram().GetSampleCount())
	}

	return 0
}
