// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package fileexporter

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/testdata"
)

func TestFileTracesExporter(t *testing.T) {
	fe := newFileExporter(&Config{
		Path: tempFileName(t),
	})
	require.NotNil(t, fe)

	td := testdata.GenerateTracesTwoSpansSameResource()
	assert.NoError(t, fe.Start(context.Background(), componenttest.NewNopHost()))
	assert.NoError(t, fe.ConsumeTraces(context.Background(), td))
	assert.NoError(t, fe.Shutdown(context.Background()))

	unmarshaler := ptrace.NewJSONUnmarshaler()
	buf, err := os.ReadFile(fe.path)
	assert.NoError(t, err)
	got, err := unmarshaler.UnmarshalTraces(buf)
	assert.NoError(t, err)
	assert.EqualValues(t, td, got)
}

func TestFileTracesExporterError(t *testing.T) {
	mf := &errorWriter{}
	fe := &fileExporter{file: mf}
	require.NotNil(t, fe)

	td := testdata.GenerateTracesTwoSpansSameResource()
	// Cannot call Start since we inject directly the WriterCloser.
	assert.Error(t, fe.ConsumeTraces(context.Background(), td))
	assert.NoError(t, fe.Shutdown(context.Background()))
}

func TestFileMetricsExporter(t *testing.T) {
	fe := newFileExporter(&Config{
		Path: tempFileName(t),
	})
	require.NotNil(t, fe)

	md := testdata.GenerateMetricsTwoMetrics()
	assert.NoError(t, fe.Start(context.Background(), componenttest.NewNopHost()))
	assert.NoError(t, fe.ConsumeMetrics(context.Background(), md))
	assert.NoError(t, fe.Shutdown(context.Background()))

	unmarshaler := pmetric.NewJSONUnmarshaler()
	buf, err := os.ReadFile(fe.path)
	assert.NoError(t, err)
	got, err := unmarshaler.UnmarshalMetrics(buf)
	assert.NoError(t, err)
	assert.EqualValues(t, md, got)
}

func TestFileMetricsExporterError(t *testing.T) {
	mf := &errorWriter{}
	fe := &fileExporter{file: mf}
	require.NotNil(t, fe)

	md := testdata.GenerateMetricsTwoMetrics()
	// Cannot call Start since we inject directly the WriterCloser.
	assert.Error(t, fe.ConsumeMetrics(context.Background(), md))
	assert.NoError(t, fe.Shutdown(context.Background()))
}

func TestFileLogsExporter(t *testing.T) {
	fe := newFileExporter(&Config{
		Path: tempFileName(t),
	})
	require.NotNil(t, fe)

	ld := testdata.GenerateLogsTwoLogRecordsSameResource()
	assert.NoError(t, fe.Start(context.Background(), componenttest.NewNopHost()))
	assert.NoError(t, fe.ConsumeLogs(context.Background(), ld))
	assert.NoError(t, fe.Shutdown(context.Background()))

	unmarshaler := plog.NewJSONUnmarshaler()
	buf, err := os.ReadFile(fe.path)
	assert.NoError(t, err)
	got, err := unmarshaler.UnmarshalLogs(buf)
	assert.NoError(t, err)
	assert.EqualValues(t, ld, got)
}

func TestFileLogsExporterErrors(t *testing.T) {
	mf := &errorWriter{}
	fe := &fileExporter{file: mf}
	require.NotNil(t, fe)

	ld := testdata.GenerateLogsTwoLogRecordsSameResource()
	// Cannot call Start since we inject directly the WriterCloser.
	assert.Error(t, fe.ConsumeLogs(context.Background(), ld))
	assert.NoError(t, fe.Shutdown(context.Background()))
}

func Test_fileExporter_Capabilities(t *testing.T) {
	fe := newFileExporter(
		&Config{
			Path:     tempFileName(t),
			Rotation: Rotation{MaxMegabytes: 1},
		})
	require.NotNil(t, fe)
	require.NotNil(t, fe.Capabilities())
}

// tempFileName provides a temporary file name for testing.
func tempFileName(t *testing.T) string {
	tmpfile, err := os.CreateTemp("", "*.json")
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())
	socket := tmpfile.Name()
	require.NoError(t, os.Remove(socket))
	return socket
}

// errorWriter is an io.Writer that will return an error all ways
type errorWriter struct {
}

func (e errorWriter) Write([]byte) (n int, err error) {
	return 0, errors.New("all ways return error")
}

func (e *errorWriter) Close() error {
	return nil
}
