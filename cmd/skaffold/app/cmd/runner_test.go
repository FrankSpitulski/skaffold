/*
Copyright 2019 The Skaffold Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/blang/semver"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/config"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/docker"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/parser"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner/runcontext"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/validation"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/update"
	"github.com/GoogleContainerTools/skaffold/v2/testutil"
)

func TestGetConfigDependencies(t *testing.T) {
	required := &parser.SkaffoldConfigEntry{SkaffoldConfig: &latest.SkaffoldConfig{}, SourceFile: "required.yaml", SourceIndex: 0}
	root := &parser.SkaffoldConfigEntry{
		SkaffoldConfig:    &latest.SkaffoldConfig{},
		SourceFile:        "root.yaml",
		SourceIndex:       0,
		RequiredConfigIDs: []parser.ConfigID{required.ID()},
	}

	dependencies, err := getConfigDependencies(parser.SkaffoldConfigSet{required, root})
	if err != nil {
		t.Fatal(err)
	}
	testutil.CheckDeepEqual(t, runcontext.ConfigDependencies{1: []int{0}}, dependencies)

	root.RequiredConfigIDs = []parser.ConfigID{{SourceFile: "missing.yaml", SourceIndex: 0}}
	_, err = getConfigDependencies(parser.SkaffoldConfigSet{required, root})
	if err == nil {
		t.Fatal("expected unresolved configuration error")
	}
	testutil.CheckContains(t, "requires unresolved configuration", err.Error())
}

func TestCreateNewRunner(t *testing.T) {
	tests := []struct {
		description   string
		config        string
		options       config.SkaffoldOptions
		shouldErr     bool
		expectedError string
	}{
		{
			description: "valid config",
			config:      "",
			options: config.SkaffoldOptions{
				ConfigurationFile: "skaffold.yaml",
				Trigger:           "polling",
			},
			shouldErr: false,
		},
		{
			description: "invalid config",
			config:      "invalid",
			options: config.SkaffoldOptions{
				ConfigurationFile: "skaffold.yaml",
			},
			shouldErr: true,
		},
		{
			description: "missing config",
			config:      "",
			options: config.SkaffoldOptions{
				ConfigurationFile: "missing-skaffold.yaml",
			},
			shouldErr: true,
		},
		{
			description: "unknown profile",
			config:      "",
			options: config.SkaffoldOptions{
				ConfigurationFile: "skaffold.yaml",
				Profiles:          []string{"unknown-profile"},
			},
			shouldErr:     true,
			expectedError: `profile selection ["unknown-profile"] did not match those defined in any configurations`,
		},
		{
			description: "unsupported trigger",
			config:      "",
			options: config.SkaffoldOptions{
				ConfigurationFile: "skaffold.yaml",
				Trigger:           "unknown trigger",
			},
			shouldErr:     true,
			expectedError: "unsupported trigger",
		},
	}
	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			t.Override(&validation.DefaultConfig, validation.Options{CheckDeploySource: false})
			t.Override(&docker.NewAPIClient, func(context.Context, docker.Config) (docker.LocalDaemon, error) {
				return docker.NewLocalDaemon(&testutil.FakeAPIClient{
					ErrVersion: true,
				}, nil, false, nil), nil
			})

			t.Override(&update.GetLatestAndCurrentVersion, func() (semver.Version, semver.Version, error) {
				return semver.Version{}, semver.Version{}, nil
			})
			t.NewTempDir().
				Write("skaffold.yaml", fmt.Sprintf("apiVersion: %s\nkind: Config\n%s", latest.Version, test.config)).
				Chdir()

			_, _, _, err := createNewRunner(context.Background(), io.Discard, test.options)

			t.CheckError(test.shouldErr, err)
			if test.expectedError != "" {
				t.CheckErrorContains(test.expectedError, err)
			}
		})
	}
}
