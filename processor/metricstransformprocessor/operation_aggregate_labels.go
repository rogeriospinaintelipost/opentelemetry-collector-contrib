// Copyright 2020 OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metricstransformprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/metricstransformprocessor"

import (
	"encoding/json"
	"math"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

type aggGroups struct {
	gauge        map[string]pmetric.NumberDataPointSlice
	sum          map[string]pmetric.NumberDataPointSlice
	histogram    map[string]pmetric.HistogramDataPointSlice
	expHistogram map[string]pmetric.ExponentialHistogramDataPointSlice
}

// aggregateLabelsOp aggregates points that have the labels excluded in label_set
func aggregateLabelsOp(metric pmetric.Metric, mtpOp internalOperation) {
	filterAttrs(metric, mtpOp.labelSetMap)
	newMetric := pmetric.NewMetric()
	copyMetricDetails(metric, newMetric)
	ag := groupDataPoints(metric, aggGroups{})
	mergeDataPoints(newMetric, mtpOp.configOperation.AggregationType, ag)
	newMetric.MoveTo(metric)
}

// groupMetrics groups all the provided timeseries that will be aggregated together based on all the label values.
// Returns a map of grouped timeseries and the corresponding selected labels
// canBeCombined must be callled before.
func groupMetrics(metrics pmetric.MetricSlice, aggType AggregationType, to pmetric.Metric) {
	var ag aggGroups
	for i := 0; i < metrics.Len(); i++ {
		ag = groupDataPoints(metrics.At(i), ag)
	}
	mergeDataPoints(to, aggType, ag)
}

func groupDataPoints(metric pmetric.Metric, ag aggGroups) aggGroups {
	switch metric.DataType() {
	case pmetric.MetricDataTypeGauge:
		if ag.gauge == nil {
			ag.gauge = map[string]pmetric.NumberDataPointSlice{}
		}
		groupNumberDataPoints(metric.Gauge().DataPoints(), false, ag.gauge)
	case pmetric.MetricDataTypeSum:
		if ag.sum == nil {
			ag.sum = map[string]pmetric.NumberDataPointSlice{}
		}
		groupByStartTime := metric.Sum().AggregationTemporality() == pmetric.MetricAggregationTemporalityDelta
		groupNumberDataPoints(metric.Sum().DataPoints(), groupByStartTime, ag.sum)
	case pmetric.MetricDataTypeHistogram:
		if ag.histogram == nil {
			ag.histogram = map[string]pmetric.HistogramDataPointSlice{}
		}
		groupByStartTime := metric.Histogram().AggregationTemporality() == pmetric.MetricAggregationTemporalityDelta
		groupHistogramDataPoints(metric.Histogram().DataPoints(), groupByStartTime, ag.histogram)
	case pmetric.MetricDataTypeExponentialHistogram:
		if ag.expHistogram == nil {
			ag.expHistogram = map[string]pmetric.ExponentialHistogramDataPointSlice{}
		}
		groupByStartTime := metric.ExponentialHistogram().AggregationTemporality() == pmetric.MetricAggregationTemporalityDelta
		groupExponentialHistogramDataPoints(metric.ExponentialHistogram().DataPoints(), groupByStartTime, ag.expHistogram)
	}
	return ag
}

func mergeDataPoints(to pmetric.Metric, aggType AggregationType, ag aggGroups) {
	switch to.DataType() {
	case pmetric.MetricDataTypeGauge:
		mergeNumberDataPoints(ag.gauge, aggType, to.Gauge().DataPoints())
	case pmetric.MetricDataTypeSum:
		mergeNumberDataPoints(ag.sum, aggType, to.Sum().DataPoints())
	case pmetric.MetricDataTypeHistogram:
		mergeHistogramDataPoints(ag.histogram, to.Histogram().DataPoints())
	case pmetric.MetricDataTypeExponentialHistogram:
		mergeExponentialHistogramDataPoints(ag.expHistogram, to.ExponentialHistogram().DataPoints())
	}
}

func groupNumberDataPoints(dps pmetric.NumberDataPointSlice, useStartTime bool,
	dpsByAttrsAndTs map[string]pmetric.NumberDataPointSlice) {
	var keyHashParts []interface{}
	for i := 0; i < dps.Len(); i++ {
		if useStartTime {
			keyHashParts = []interface{}{dps.At(i).StartTimestamp().String()}
		}
		key := dataPointHashKey(dps.At(i).Attributes(), dps.At(i).Timestamp(), keyHashParts...)
		if _, ok := dpsByAttrsAndTs[key]; !ok {
			dpsByAttrsAndTs[key] = pmetric.NewNumberDataPointSlice()
		}
		dps.At(i).MoveTo(dpsByAttrsAndTs[key].AppendEmpty())
	}
}

func groupHistogramDataPoints(dps pmetric.HistogramDataPointSlice, useStartTime bool,
	dpsByAttrsAndTs map[string]pmetric.HistogramDataPointSlice) {
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		keyHashParts := make([]interface{}, 0, dp.ExplicitBounds().Len()+4)
		for b := 0; b < dp.ExplicitBounds().Len(); b++ {
			keyHashParts = append(keyHashParts, dp.ExplicitBounds().At(b))
		}
		if useStartTime {
			keyHashParts = append(keyHashParts, dp.StartTimestamp().String())
		}

		keyHashParts = append(keyHashParts, dp.HasMin(), dp.HasMax(), uint32(dp.Flags()))
		key := dataPointHashKey(dps.At(i).Attributes(), dp.Timestamp(), keyHashParts...)
		if _, ok := dpsByAttrsAndTs[key]; !ok {
			dpsByAttrsAndTs[key] = pmetric.NewHistogramDataPointSlice()
		}
		dp.MoveTo(dpsByAttrsAndTs[key].AppendEmpty())
	}
}

func groupExponentialHistogramDataPoints(dps pmetric.ExponentialHistogramDataPointSlice, useStartTime bool,
	dpsByAttrsAndTs map[string]pmetric.ExponentialHistogramDataPointSlice) {
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		keyHashParts := make([]interface{}, 0, 5)
		keyHashParts = append(keyHashParts, dp.Scale(), dp.HasMin(), dp.HasMax(), uint32(dp.Flags()), dp.Negative().Offset(),
			dp.Positive().Offset())
		if useStartTime {
			keyHashParts = append(keyHashParts, dp.StartTimestamp().String())
		}
		key := dataPointHashKey(dps.At(i).Attributes(), dp.Timestamp(), keyHashParts...)
		if _, ok := dpsByAttrsAndTs[key]; !ok {
			dpsByAttrsAndTs[key] = pmetric.NewExponentialHistogramDataPointSlice()
		}
		dp.MoveTo(dpsByAttrsAndTs[key].AppendEmpty())
	}
}

func filterAttrs(metric pmetric.Metric, filterAttrKeys map[string]bool) {
	if filterAttrKeys == nil {
		return
	}
	rangeDataPointAttributes(metric, func(attrs pcommon.Map) bool {
		attrs.RemoveIf(func(k string, v pcommon.Value) bool {
			return !filterAttrKeys[k]
		})
		return true
	})
}

func dataPointHashKey(atts pcommon.Map, ts pcommon.Timestamp, other ...interface{}) string {
	hashParts := []interface{}{atts.AsRaw(), ts.String()}
	jsonStr, _ := json.Marshal(append(hashParts, other...))
	return string(jsonStr)
}

func mergeNumberDataPoints(dpsMap map[string]pmetric.NumberDataPointSlice, agg AggregationType, to pmetric.NumberDataPointSlice) {
	for _, dps := range dpsMap {
		dp := to.AppendEmpty()
		dps.At(0).MoveTo(dp)
		switch dp.ValueType() {
		case pmetric.NumberDataPointValueTypeDouble:
			for i := 1; i < dps.Len(); i++ {
				switch agg {
				case Sum, Mean:
					dp.SetDoubleVal(dp.DoubleVal() + doubleVal(dps.At(i)))
				case Max:
					dp.SetDoubleVal(math.Max(dp.DoubleVal(), doubleVal(dps.At(i))))
				case Min:
					dp.SetDoubleVal(math.Min(dp.DoubleVal(), doubleVal(dps.At(i))))
				}
				if dps.At(i).StartTimestamp() < dp.StartTimestamp() {
					dp.SetStartTimestamp(dps.At(i).StartTimestamp())
				}
			}
			if agg == Mean {
				dp.SetDoubleVal(dp.DoubleVal() / float64(dps.Len()))
			}
		case pmetric.NumberDataPointValueTypeInt:
			for i := 1; i < dps.Len(); i++ {
				switch agg {
				case Sum, Mean:
					dp.SetIntVal(dp.IntVal() + dps.At(i).IntVal())
				case Max:
					if dp.IntVal() < intVal(dps.At(i)) {
						dp.SetIntVal(intVal(dps.At(i)))
					}
				case Min:
					if dp.IntVal() > intVal(dps.At(i)) {
						dp.SetIntVal(intVal(dps.At(i)))
					}
				}
				if dps.At(i).StartTimestamp() < dp.StartTimestamp() {
					dp.SetStartTimestamp(dps.At(i).StartTimestamp())
				}
			}
			if agg == Mean {
				dp.SetIntVal(dp.IntVal() / int64(dps.Len()))
			}
		}
	}
}

func doubleVal(dp pmetric.NumberDataPoint) float64 {
	switch dp.ValueType() {
	case pmetric.NumberDataPointValueTypeDouble:
		return dp.DoubleVal()
	case pmetric.NumberDataPointValueTypeInt:
		return float64(dp.IntVal())
	}
	return 0
}

func intVal(dp pmetric.NumberDataPoint) int64 {
	switch dp.ValueType() {
	case pmetric.NumberDataPointValueTypeDouble:
		return int64(dp.DoubleVal())
	case pmetric.NumberDataPointValueTypeInt:
		return dp.IntVal()
	}
	return 0
}

func mergeHistogramDataPoints(dpsMap map[string]pmetric.HistogramDataPointSlice, to pmetric.HistogramDataPointSlice) {
	for _, dps := range dpsMap {
		dp := to.AppendEmpty()
		dps.At(0).MoveTo(dp)
		counts := dp.BucketCounts()
		for i := 1; i < dps.Len(); i++ {
			if dps.At(i).Count() == 0 {
				continue
			}
			dp.SetCount(dp.Count() + dps.At(i).Count())
			dp.SetSum(dp.Sum() + dps.At(i).Sum())
			if dp.HasMin() && dp.Min() > dps.At(i).Min() {
				dp.SetMin(dps.At(i).Min())
			}
			if dp.HasMax() && dp.Max() < dps.At(i).Max() {
				dp.SetMax(dps.At(i).Max())
			}
			dps.At(i).Exemplars().MoveAndAppendTo(dp.Exemplars())
			for b := 0; b < dps.At(i).BucketCounts().Len(); b++ {
				counts.SetAt(b, counts.At(b)+dps.At(i).BucketCounts().At(b))
			}
			dps.At(i).Exemplars().MoveAndAppendTo(dp.Exemplars())
			if dps.At(i).StartTimestamp() < dp.StartTimestamp() {
				dp.SetStartTimestamp(dps.At(i).StartTimestamp())
			}
		}
	}
}

func mergeExponentialHistogramDataPoints(dpsMap map[string]pmetric.ExponentialHistogramDataPointSlice,
	to pmetric.ExponentialHistogramDataPointSlice) {
	for _, dps := range dpsMap {
		dp := to.AppendEmpty()
		dps.At(0).MoveTo(dp)
		negatives := dp.Negative().BucketCounts()
		positives := dp.Positive().BucketCounts()
		for i := 1; i < dps.Len(); i++ {
			if dps.At(i).Count() == 0 {
				continue
			}
			dp.SetCount(dp.Count() + dps.At(i).Count())
			dp.SetSum(dp.Sum() + dps.At(i).Sum())
			if dp.HasMin() && dp.Min() > dps.At(i).Min() {
				dp.SetMin(dps.At(i).Min())
			}
			if dp.HasMax() && dp.Max() < dps.At(i).Max() {
				dp.SetMax(dps.At(i).Max())
			}
			for b := 0; b < dps.At(i).Negative().BucketCounts().Len(); b++ {
				negatives.SetAt(b, negatives.At(b)+dps.At(i).Negative().BucketCounts().At(b))
			}
			for b := 0; b < dps.At(i).Positive().BucketCounts().Len(); b++ {
				positives.SetAt(b, positives.At(b)+dps.At(i).Positive().BucketCounts().At(b))
			}
			dps.At(i).Exemplars().MoveAndAppendTo(dp.Exemplars())
			if dps.At(i).StartTimestamp() < dp.StartTimestamp() {
				dp.SetStartTimestamp(dps.At(i).StartTimestamp())
			}
		}
	}
}
