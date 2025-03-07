// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build windows
// +build windows

package windowseventlogreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/windowseventlogreceiver"

import (
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/adapter"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/windows"
)

const (
	typeStr   = "windowseventlog"
	stability = component.StabilityLevelAlpha
)

// NewFactory creates a factory for windowseventlog receiver
func NewFactory() component.ReceiverFactory {
	return adapter.NewFactory(ReceiverType{}, stability)
}

// ReceiverType implements adapter.LogReceiverType
// to create a file tailing receiver
type ReceiverType struct{}

// Type is the receiver type
func (f ReceiverType) Type() config.Type {
	return typeStr
}

// CreateDefaultConfig creates a config with type and version
func (f ReceiverType) CreateDefaultConfig() config.Receiver {
	return &WindowsLogConfig{
		BaseConfig: adapter.BaseConfig{
			ReceiverSettings: config.NewReceiverSettings(config.NewComponentID(typeStr)),
			Operators:        adapter.OperatorConfigs{},
			Converter:        adapter.ConverterConfig{},
		},
		InputConfig: *windows.NewConfig(),
	}
}

// BaseConfig gets the base config from config, for now
func (f ReceiverType) BaseConfig(cfg config.Receiver) adapter.BaseConfig {
	return cfg.(*WindowsLogConfig).BaseConfig
}

// InputConfig unmarshals the input operator
func (f ReceiverType) InputConfig(cfg config.Receiver) operator.Config {
	return operator.NewConfig(&cfg.(*WindowsLogConfig).InputConfig)
}

// WindowsLogConfig defines configuration for the windowseventlog receiver
type WindowsLogConfig struct {
	InputConfig        windows.Config `mapstructure:",squash"`
	adapter.BaseConfig `mapstructure:",squash"`
}
