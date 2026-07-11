/*
Copyright 2025 The Skaffold Authors

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

package deploy

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/sync/errgroup"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/graph"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/kubernetes/manifest"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/output"
)

type configDeployer struct {
	deployers     []Deployer
	dependencies  []int
	subtaskOffset int
}

// NewConcurrentDeployerMux groups deployers by configuration and schedules each
// group after its required configurations have completed.
func NewConcurrentDeployerMux(deployers []Deployer, orderedConfigs []string, dependencies map[int][]int, iterativeStatusCheck bool, concurrency int) (Deployer, error) {
	if concurrency < 0 {
		return nil, fmt.Errorf("deploy concurrency must be greater than or equal to zero")
	}

	configs := make([]configDeployer, len(orderedConfigs))
	configIndexes := make(map[string]int, len(orderedConfigs))
	for index, name := range orderedConfigs {
		for _, dependency := range dependencies[index] {
			if dependency < 0 || dependency >= len(orderedConfigs) {
				return nil, fmt.Errorf("configuration %q requires unknown configuration index %d", name, dependency)
			}
			if dependency >= index {
				return nil, fmt.Errorf("configuration %q requires configuration index %d that does not precede it", name, dependency)
			}
		}
		configs[index].dependencies = append([]int(nil), dependencies[index]...)
		configIndexes[name] = index
	}
	for id, deployer := range deployers {
		name := deployer.ConfigName()
		index, found := configIndexes[name]
		if !found {
			return nil, fmt.Errorf("deployer references unknown configuration %q", name)
		}
		config := &configs[index]
		if len(config.deployers) == 0 {
			config.subtaskOffset = id
		}
		config.deployers = append(config.deployers, deployer)
	}

	if concurrency == 0 || concurrency > len(configs) {
		concurrency = max(1, len(configs))
	}
	if concurrency == 1 {
		for index := 1; index < len(configs); index++ {
			configs[index].dependencies = append(configs[index].dependencies, index-1)
		}
	}

	return DeployerMux{
		iterativeStatusCheck: iterativeStatusCheck,
		deployers:            deployers,
		configs:              configs,
		concurrency:          concurrency,
	}, nil
}

func (m DeployerMux) Deploy(ctx context.Context, w io.Writer, artifacts []graph.Artifact, manifests manifest.ManifestListByConfig) error {
	w = output.SynchronizeWriter(w)
	completed := make([]chan struct{}, len(m.configs))
	for index := range completed {
		completed[index] = make(chan struct{})
	}

	semaphore := make(chan struct{}, m.concurrency)
	group, groupCtx := errgroup.WithContext(ctx)
	for index, config := range m.configs {
		group.Go(func() error {
			for _, dependency := range config.dependencies {
				select {
				case <-groupCtx.Done():
					return groupCtx.Err()
				case <-completed[dependency]:
				}
			}

			select {
			case <-groupCtx.Done():
				return groupCtx.Err()
			case semaphore <- struct{}{}:
			}
			defer func() { <-semaphore }()

			if err := m.deployConfig(groupCtx, w, artifacts, manifests, config); err != nil {
				return err
			}
			close(completed[index])
			return nil
		})
	}
	return group.Wait()
}
