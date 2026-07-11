/*
Copyright 2021 The Skaffold Authors

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

package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/config"
	eventV2 "github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/event/v2"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/graph"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/kubernetes/client"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/kubernetes/manifest"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner/runcontext"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	"github.com/GoogleContainerTools/skaffold/v2/testutil"
	testEvent "github.com/GoogleContainerTools/skaffold/v2/testutil/event"
)

func TestDeploy(t *testing.T) {
	tests := []struct {
		description string
		testBench   *TestBench
		shouldErr   bool
	}{
		{
			description: "deploy succeeds",
			testBench:   &TestBench{},
		},
		{
			description: "deploy fails",
			testBench:   &TestBench{deployErrors: []error{errors.New("deploy error")}},
			shouldErr:   true,
		},
	}

	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			t.SetupFakeKubernetesContext(api.Config{CurrentContext: "cluster1"})
			t.Override(&client.Client, mockK8sClient)

			r := createRunner(t, test.testBench, nil, []*latest.Artifact{{ImageName: "img1"}, {ImageName: "img2"}}, nil)
			out := new(bytes.Buffer)

			err := r.Deploy(context.Background(), out, []graph.Artifact{
				{ImageName: "img1", Tag: "img1:tag1"},
				{ImageName: "img2", Tag: "img2:tag2"},
			}, manifest.ManifestListByConfig{})
			t.CheckError(test.shouldErr, err)
		})
	}
}

func TestDeployStatusLogFailureMarksTaskFailed(t *testing.T) {
	testutil.Run(t, "status log creation fails", func(t *testutil.T) {
		t.SetupFakeKubernetesContext(api.Config{CurrentContext: "cluster1"})
		t.Override(&client.Client, mockK8sClient)

		r := createRunner(t, &TestBench{}, nil, nil, nil)
		r.runCtx.Opts.Muted.Phases = []string{"status-check"}
		blocked := filepath.Join(t.TempDir(), "blocked")
		t.RequireNoError(os.WriteFile(blocked, nil, 0o600))
		t.Setenv("TMPDIR", blocked)
		testEvent.InitializeState([]latest.Pipeline{{}})

		err := r.Deploy(context.Background(), io.Discard, nil, manifest.ManifestListByConfig{})
		if err == nil {
			t.Fatal("Deploy() error = nil, want status log creation error")
		}
		deadline := time.Now().Add(time.Second)
		for {
			state, stateErr := eventV2.GetState()
			t.RequireNoError(stateErr)
			if state.DeployState.Status == eventV2.Failed {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("deploy state = %q, want %q", state.DeployState.Status, eventV2.Failed)
			}
			time.Sleep(time.Millisecond)
		}
	})
}

func TestSkaffoldDeployRenderOnly(t *testing.T) {
	testutil.Run(t, "does not make kubectl calls", func(t *testutil.T) {
		runCtx := &runcontext.RunContext{
			Opts: config.SkaffoldOptions{
				Namespace:  "testNamespace",
				RenderOnly: true,
			},
			KubeContext: "does-not-exist",
		}

		deployer, err := GetDeployer(context.Background(), runCtx, nil, "", false)
		t.RequireNoError(err)
		r := SkaffoldRunner{
			runCtx:   runCtx,
			deployer: deployer,
		}
		var builds []graph.Artifact

		err = r.Deploy(context.Background(), io.Discard, builds, manifest.ManifestListByConfig{})

		t.CheckNoError(err)
	})
}
