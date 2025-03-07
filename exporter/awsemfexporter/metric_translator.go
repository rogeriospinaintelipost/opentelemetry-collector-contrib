// Copyright 2020, OpenTelemetry Authors
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

package awsemfexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter"

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/cwlogs"
)

const (
	// OTel instrumentation lib name as dimension
	oTellibDimensionKey          = "OTelLib"
	defaultNamespace             = "default"
	noInstrumentationLibraryName = "Undefined"

	// DimensionRollupOptions
	zeroAndSingleDimensionRollup = "ZeroAndSingleDimensionRollup"
	singleDimensionRollupOnly    = "SingleDimensionRollupOnly"

	prometheusReceiver        = "prometheus"
	attributeReceiver         = "receiver"
	fieldPrometheusMetricType = "prom_metric_type"
)

var fieldPrometheusTypes = map[pmetric.MetricDataType]string{
	pmetric.MetricDataTypeNone:      "",
	pmetric.MetricDataTypeGauge:     "gauge",
	pmetric.MetricDataTypeSum:       "counter",
	pmetric.MetricDataTypeHistogram: "histogram",
	pmetric.MetricDataTypeSummary:   "summary",
}

type cWMetrics struct {
	measurements []cWMeasurement
	timestampMs  int64
	fields       map[string]interface{}
}

type cWMeasurement struct {
	Namespace  string
	Dimensions [][]string
	Metrics    []map[string]string
}

type cWMetricStats struct {
	Max   float64
	Min   float64
	Count uint64
	Sum   float64
}

type groupedMetricMetadata struct {
	namespace   string
	timestampMs int64
	logGroup    string
	logStream   string
}

// cWMetricMetadata represents the metadata associated with a given CloudWatch metric
type cWMetricMetadata struct {
	groupedMetricMetadata
	instrumentationLibraryName string

	receiver       string
	metricDataType pmetric.MetricDataType
}

type metricTranslator struct {
	metricDescriptor map[string]MetricDescriptor
}

func newMetricTranslator(config Config) metricTranslator {
	mt := map[string]MetricDescriptor{}
	for _, descriptor := range config.MetricDescriptors {
		mt[descriptor.metricName] = descriptor
	}
	return metricTranslator{
		metricDescriptor: mt,
	}
}

// translateOTelToGroupedMetric converts OT metrics to Grouped Metric format.
func (mt metricTranslator) translateOTelToGroupedMetric(rm pmetric.ResourceMetrics, groupedMetrics map[interface{}]*groupedMetric, config *Config) error {
	timestamp := time.Now().UnixNano() / int64(time.Millisecond)
	var instrumentationLibName string
	cWNamespace := getNamespace(rm, config.Namespace)
	logGroup, logStream, patternReplaceSucceeded := getLogInfo(rm, cWNamespace, config)

	ilms := rm.ScopeMetrics()
	var metricReceiver string
	if receiver, ok := rm.Resource().Attributes().Get(attributeReceiver); ok {
		metricReceiver = receiver.StringVal()
	}
	for j := 0; j < ilms.Len(); j++ {
		ilm := ilms.At(j)
		if ilm.Scope().Name() == "" {
			instrumentationLibName = noInstrumentationLibraryName
		} else {
			instrumentationLibName = ilm.Scope().Name()
		}

		metrics := ilm.Metrics()
		for k := 0; k < metrics.Len(); k++ {
			metric := metrics.At(k)
			metadata := cWMetricMetadata{
				groupedMetricMetadata: groupedMetricMetadata{
					namespace:   cWNamespace,
					timestampMs: timestamp,
					logGroup:    logGroup,
					logStream:   logStream,
				},
				instrumentationLibraryName: instrumentationLibName,
				receiver:                   metricReceiver,
				metricDataType:             metric.DataType(),
			}
			err := addToGroupedMetric(metric, groupedMetrics, metadata, patternReplaceSucceeded, config.logger, mt.metricDescriptor, config)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// translateGroupedMetricToCWMetric converts Grouped Metric format to CloudWatch Metric format.
func translateGroupedMetricToCWMetric(groupedMetric *groupedMetric, config *Config) *cWMetrics {
	labels := groupedMetric.labels
	fieldsLength := len(labels) + len(groupedMetric.metrics)

	isPrometheusMetric := groupedMetric.metadata.receiver == prometheusReceiver
	if isPrometheusMetric {
		fieldsLength++
	}
	fields := make(map[string]interface{}, fieldsLength)

	// Add labels to fields
	for k, v := range labels {
		fields[k] = v
	}
	// Add metrics to fields
	for metricName, metricInfo := range groupedMetric.metrics {
		fields[metricName] = metricInfo.value
	}
	if isPrometheusMetric {
		fields[fieldPrometheusMetricType] = fieldPrometheusTypes[groupedMetric.metadata.metricDataType]
	}

	var cWMeasurements []cWMeasurement
	if len(config.MetricDeclarations) == 0 {
		// If there are no metric declarations defined, translate grouped metric
		// into the corresponding CW Measurement
		cwm := groupedMetricToCWMeasurement(groupedMetric, config)
		cWMeasurements = []cWMeasurement{cwm}
	} else {
		// If metric declarations are defined, filter grouped metric's metrics using
		// metric declarations and translate into the corresponding list of CW Measurements
		cWMeasurements = groupedMetricToCWMeasurementsWithFilters(groupedMetric, config)
	}

	return &cWMetrics{
		measurements: cWMeasurements,
		timestampMs:  groupedMetric.metadata.timestampMs,
		fields:       fields,
	}
}

// groupedMetricToCWMeasurement creates a single CW Measurement from a grouped metric.
func groupedMetricToCWMeasurement(groupedMetric *groupedMetric, config *Config) cWMeasurement {
	labels := groupedMetric.labels
	dimensionRollupOption := config.DimensionRollupOption

	// Create a dimension set containing list of label names
	dimSet := make([]string, len(labels))
	idx := 0
	for labelName := range labels {
		dimSet[idx] = labelName
		idx++
	}
	dimensions := [][]string{dimSet}

	// Apply single/zero dimension rollup to labels
	rollupDimensionArray := dimensionRollup(dimensionRollupOption, labels)

	if len(rollupDimensionArray) > 0 {
		// Perform duplication check for edge case with a single label and single dimension roll-up
		_, hasOTelLibKey := labels[oTellibDimensionKey]
		isSingleLabel := len(dimSet) <= 1 || (len(dimSet) == 2 && hasOTelLibKey)
		singleDimRollup := dimensionRollupOption == singleDimensionRollupOnly ||
			dimensionRollupOption == zeroAndSingleDimensionRollup
		if isSingleLabel && singleDimRollup {
			// Remove duplicated dimension set before adding on rolled-up dimensions
			dimensions = nil
		}
	}

	// Add on rolled-up dimensions
	dimensions = append(dimensions, rollupDimensionArray...)

	metrics := make([]map[string]string, len(groupedMetric.metrics))
	idx = 0
	for metricName, metricInfo := range groupedMetric.metrics {
		metrics[idx] = map[string]string{
			"Name": metricName,
		}
		if metricInfo.unit != "" {
			metrics[idx]["Unit"] = metricInfo.unit
		}
		idx++
	}

	return cWMeasurement{
		Namespace:  groupedMetric.metadata.namespace,
		Dimensions: dimensions,
		Metrics:    metrics,
	}
}

// groupedMetricToCWMeasurementsWithFilters filters the grouped metric using the given list of metric
// declarations and returns the corresponding list of CW Measurements.
func groupedMetricToCWMeasurementsWithFilters(groupedMetric *groupedMetric, config *Config) (cWMeasurements []cWMeasurement) {
	labels := groupedMetric.labels

	// Filter metric declarations by labels
	metricDeclarations := make([]*MetricDeclaration, 0, len(config.MetricDeclarations))
	for _, metricDeclaration := range config.MetricDeclarations {
		if metricDeclaration.MatchesLabels(labels) {
			metricDeclarations = append(metricDeclarations, metricDeclaration)
		}
	}

	// If the whole batch of metrics don't match any metric declarations, drop them
	if len(metricDeclarations) == 0 {
		labelsStr, _ := json.Marshal(labels)
		var metricNames []string
		for metricName := range groupedMetric.metrics {
			metricNames = append(metricNames, metricName)
		}
		config.logger.Debug(
			"Dropped batch of metrics: no metric declaration matched labels",
			zap.String("Labels", string(labelsStr)),
			zap.Strings("Metric Names", metricNames),
		)
		return
	}

	// Group metrics by matched metric declarations
	type metricDeclarationGroup struct {
		metricDeclIdxList []int
		metrics           []map[string]string
	}

	metricDeclGroups := make(map[string]*metricDeclarationGroup)
	for metricName, metricInfo := range groupedMetric.metrics {
		// Filter metric declarations by metric name
		var metricDeclIdx []int
		for i, metricDeclaration := range metricDeclarations {
			if metricDeclaration.MatchesName(metricName) {
				metricDeclIdx = append(metricDeclIdx, i)
			}
		}

		if len(metricDeclIdx) == 0 {
			config.logger.Debug(
				"Dropped metric: no metric declaration matched metric name",
				zap.String("Metric name", metricName),
			)
			continue
		}

		metric := map[string]string{
			"Name": metricName,
		}
		if metricInfo.unit != "" {
			metric["Unit"] = metricInfo.unit
		}
		metricDeclKey := fmt.Sprint(metricDeclIdx)
		if group, ok := metricDeclGroups[metricDeclKey]; ok {
			group.metrics = append(group.metrics, metric)
		} else {
			metricDeclGroups[metricDeclKey] = &metricDeclarationGroup{
				metricDeclIdxList: metricDeclIdx,
				metrics:           []map[string]string{metric},
			}
		}
	}

	if len(metricDeclGroups) == 0 {
		return
	}

	// Apply single/zero dimension rollup to labels
	rollupDimensionArray := dimensionRollup(config.DimensionRollupOption, labels)

	// Translate each group into a CW Measurement
	cWMeasurements = make([]cWMeasurement, 0, len(metricDeclGroups))
	for _, group := range metricDeclGroups {
		var dimensions [][]string
		// Extract dimensions from matched metric declarations
		for _, metricDeclIdx := range group.metricDeclIdxList {
			dims := metricDeclarations[metricDeclIdx].ExtractDimensions(labels)
			dimensions = append(dimensions, dims...)
		}
		dimensions = append(dimensions, rollupDimensionArray...)

		// De-duplicate dimensions
		dimensions = dedupDimensions(dimensions)

		// Export metrics only with non-empty dimensions list
		if len(dimensions) > 0 {
			cwm := cWMeasurement{
				Namespace:  groupedMetric.metadata.namespace,
				Dimensions: dimensions,
				Metrics:    group.metrics,
			}
			cWMeasurements = append(cWMeasurements, cwm)
		}
	}

	return
}

// translateCWMetricToEMF converts CloudWatch Metric format to EMF.
func translateCWMetricToEMF(cWMetric *cWMetrics, config *Config) *cwlogs.Event {
	// convert CWMetric into map format for compatible with PLE input
	cWMetricMap := make(map[string]interface{})
	fieldMap := cWMetric.fields

	// restore the json objects that are stored as string in attributes
	for _, key := range config.ParseJSONEncodedAttributeValues {
		if fieldMap[key] == nil {
			continue
		}

		if val, ok := fieldMap[key].(string); ok {
			var f interface{}
			err := json.Unmarshal([]byte(val), &f)
			if err != nil {
				config.logger.Debug(
					"Failed to parse json-encoded string",
					zap.String("label key", key),
					zap.String("label value", val),
					zap.Error(err),
				)
				continue
			}
			fieldMap[key] = f
		} else {
			config.logger.Debug(
				"Invalid json-encoded data. A string is expected",
				zap.Any("type", reflect.TypeOf(fieldMap[key])),
				zap.Any("value", reflect.ValueOf(fieldMap[key])),
			)
		}
	}

	// Create `_aws` section only if there are measurements
	if len(cWMetric.measurements) > 0 {
		// Create `_aws` section only if there are measurements
		cWMetricMap["CloudWatchMetrics"] = cWMetric.measurements
		cWMetricMap["Timestamp"] = cWMetric.timestampMs
		fieldMap["_aws"] = cWMetricMap
	}

	pleMsg, err := json.Marshal(fieldMap)
	if err != nil {
		return nil
	}

	metricCreationTime := cWMetric.timestampMs
	logEvent := cwlogs.NewEvent(
		metricCreationTime,
		string(pleMsg),
	)
	logEvent.GeneratedTime = time.Unix(0, metricCreationTime*int64(time.Millisecond))

	return logEvent
}
