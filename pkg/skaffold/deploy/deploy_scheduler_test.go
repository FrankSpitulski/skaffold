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
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/graph"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/kubernetes/manifest"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	testEvent "github.com/GoogleContainerTools/skaffold/v2/testutil/event"
)

const deploySchedulerTestTimeout = 5 * time.Second

type controlledDeployer struct {
	*MockDeployer
	deployFn func(context.Context, io.Writer) error
}

func newControlledDeployer(name string, deployFn func(context.Context, io.Writer) error) *controlledDeployer {
	deployer := NewMockDeployer()
	deployer.configName = name
	return &controlledDeployer{MockDeployer: deployer, deployFn: deployFn}
}

func (d *controlledDeployer) Deploy(ctx context.Context, out io.Writer, _ []graph.Artifact, _ manifest.ManifestListByConfig) error {
	if d.deployFn == nil {
		return nil
	}
	return d.deployFn(ctx, out)
}

func initializeDeployEvents() {
	testEvent.InitializeState([]latest.Pipeline{{}})
}

func waitFor[T any](t *testing.T, values <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(deploySchedulerTestTimeout):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func assertNotSignaled(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatalf("%s was signaled", description)
	default:
	}
}

func waitForRelease(ctx context.Context, release <-chan struct{}) error {
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestDeployerMuxHonorsConcurrencyLimit(t *testing.T) {
	initializeDeployEvents()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{}, 3)
	release := make(chan struct{}, 3)
	var deployers []Deployer
	for _, name := range []string{"a", "b", "c"} {
		deployers = append(deployers, newControlledDeployer(name, func(ctx context.Context, _ io.Writer) error {
			started <- struct{}{}
			return waitForRelease(ctx, release)
		}))
	}
	deployer, err := NewConcurrentDeployerMux(deployers, []string{"a", "b", "c"}, nil, false, 2)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- deployer.Deploy(ctx, io.Discard, nil, manifest.NewManifestListByConfig())
	}()

	waitFor(t, started, "first configuration to start")
	waitFor(t, started, "second configuration to start")
	assertNotSignaled(t, started, "third configuration before a permit is released")
	release <- struct{}{}
	waitFor(t, started, "third configuration after a permit is released")
	release <- struct{}{}
	release <- struct{}{}
	if err := waitFor(t, result, "deployment"); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestDeployerMuxSchedulesInOrderAtConcurrencyOne(t *testing.T) {
	initializeDeployEvents()
	var started []string
	var deployers []Deployer
	for _, name := range []string{"a", "b", "c"} {
		deployers = append(deployers, newControlledDeployer(name, func(context.Context, io.Writer) error {
			started = append(started, name)
			return nil
		}))
	}
	deployer, err := NewConcurrentDeployerMux(deployers, []string{"a", "b", "c"}, nil, false, 1)
	if err != nil {
		t.Fatal(err)
	}

	if err := deployer.Deploy(context.Background(), io.Discard, nil, manifest.NewManifestListByConfig()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(started, ","); got != "a,b,c" {
		t.Fatalf("deployment order = %q, want %q", got, "a,b,c")
	}
}

func TestDeployerMuxIsWorkConserving(t *testing.T) {
	initializeDeployEvents()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slowStarted := make(chan struct{}, 1)
	fastStarted := make(chan struct{}, 1)
	leafStarted := make(chan struct{}, 1)
	slowRelease := make(chan struct{})
	fastRelease := make(chan struct{})
	deployer, err := NewConcurrentDeployerMux([]Deployer{
		newControlledDeployer("slow", func(ctx context.Context, _ io.Writer) error {
			slowStarted <- struct{}{}
			return waitForRelease(ctx, slowRelease)
		}),
		newControlledDeployer("fast", func(ctx context.Context, _ io.Writer) error {
			fastStarted <- struct{}{}
			return waitForRelease(ctx, fastRelease)
		}),
		newControlledDeployer("leaf", func(context.Context, io.Writer) error {
			leafStarted <- struct{}{}
			return nil
		}),
	}, []string{"slow", "fast", "leaf"}, map[int][]int{2: {1}}, false, 2)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- deployer.Deploy(ctx, io.Discard, nil, manifest.NewManifestListByConfig())
	}()

	waitFor(t, slowStarted, "slow branch to start")
	waitFor(t, fastStarted, "fast branch to start")
	close(fastRelease)
	waitFor(t, leafStarted, "fast branch dependent while slow branch remains active")
	close(slowRelease)
	if err := waitFor(t, result, "deployment"); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestDeployerMuxKeepsDeployersInOneConfigSerial(t *testing.T) {
	initializeDeployEvents()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aFirstStarted := make(chan struct{}, 1)
	aSecondStarted := make(chan struct{}, 1)
	bStarted := make(chan struct{}, 1)
	aFirstRelease := make(chan struct{})
	aSecondRelease := make(chan struct{})
	bRelease := make(chan struct{})
	deployer, err := NewConcurrentDeployerMux([]Deployer{
		newControlledDeployer("a", func(ctx context.Context, _ io.Writer) error {
			aFirstStarted <- struct{}{}
			return waitForRelease(ctx, aFirstRelease)
		}),
		newControlledDeployer("a", func(ctx context.Context, _ io.Writer) error {
			aSecondStarted <- struct{}{}
			return waitForRelease(ctx, aSecondRelease)
		}),
		newControlledDeployer("b", func(ctx context.Context, _ io.Writer) error {
			bStarted <- struct{}{}
			return waitForRelease(ctx, bRelease)
		}),
	}, []string{"a", "b"}, nil, false, 2)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- deployer.Deploy(ctx, io.Discard, nil, manifest.NewManifestListByConfig())
	}()

	waitFor(t, aFirstStarted, "first deployer in configuration a")
	waitFor(t, bStarted, "independent configuration b")
	assertNotSignaled(t, aSecondStarted, "second deployer in configuration a before first completes")
	close(aFirstRelease)
	waitFor(t, aSecondStarted, "second deployer in configuration a")
	close(aSecondRelease)
	close(bRelease)
	if err := waitFor(t, result, "deployment"); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestDeployerMuxFailureBlocksDependentAndCancelsSibling(t *testing.T) {
	initializeDeployEvents()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wantErr := errors.New("deploy failed")
	failingStarted := make(chan struct{}, 1)
	failingRelease := make(chan struct{})
	siblingStarted := make(chan struct{}, 1)
	siblingCanceled := make(chan struct{}, 1)
	dependentStarted := make(chan struct{}, 1)
	deployer, err := NewConcurrentDeployerMux([]Deployer{
		newControlledDeployer("failing", func(ctx context.Context, _ io.Writer) error {
			failingStarted <- struct{}{}
			if err := waitForRelease(ctx, failingRelease); err != nil {
				return err
			}
			return wantErr
		}),
		newControlledDeployer("sibling", func(ctx context.Context, _ io.Writer) error {
			siblingStarted <- struct{}{}
			<-ctx.Done()
			siblingCanceled <- struct{}{}
			return ctx.Err()
		}),
		newControlledDeployer("dependent", func(context.Context, io.Writer) error {
			dependentStarted <- struct{}{}
			return nil
		}),
	}, []string{"failing", "sibling", "dependent"}, map[int][]int{2: {0}}, false, 0)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- deployer.Deploy(ctx, io.Discard, nil, manifest.NewManifestListByConfig())
	}()

	waitFor(t, failingStarted, "failing configuration to start")
	waitFor(t, siblingStarted, "independent sibling to start")
	close(failingRelease)
	err = waitFor(t, result, "deployment")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Deploy() error = %v, want error wrapping %v", err, wantErr)
	}
	waitFor(t, siblingCanceled, "sibling cancellation")
	assertNotSignaled(t, dependentStarted, "dependent after prerequisite failure")
}

func TestNewDeployerMuxRejectsInvalidGraphs(t *testing.T) {
	tests := []struct {
		name           string
		deployers      []Deployer
		orderedConfigs []string
		dependencies   map[int][]int
		concurrency    int
		wantError      string
	}{
		{
			name:           "negative concurrency",
			orderedConfigs: []string{"a"},
			concurrency:    -1,
			wantError:      "deploy concurrency must be greater than or equal to zero",
		},
		{
			name:           "dependency does not precede config",
			orderedConfigs: []string{"a", "b"},
			dependencies:   map[int][]int{0: {1}, 1: {0}},
			wantError:      `configuration "a" requires configuration index 1 that does not precede it`,
		},
		{
			name:           "unknown dependency",
			orderedConfigs: []string{"a"},
			dependencies:   map[int][]int{0: {1}},
			wantError:      `configuration "a" requires unknown configuration index 1`,
		},
		{
			name: "deployer for unknown configuration",
			deployers: []Deployer{
				newControlledDeployer("missing", nil),
			},
			orderedConfigs: []string{"a"},
			wantError:      `deployer references unknown configuration "missing"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewConcurrentDeployerMux(test.deployers, test.orderedConfigs, test.dependencies, false, test.concurrency)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("NewConcurrentDeployerMux() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}
