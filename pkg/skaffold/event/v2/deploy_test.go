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

package v2

import (
	"errors"
	"testing"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/constants"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
)

func TestDeployTaskStateIgnoresSubtaskTerminalState(t *testing.T) {
	defer func() { handler = newHandler() }()
	handler = newHandler()
	handler.state = emptyState(mockCfg([]latest.Pipeline{{}}, "test"))

	TaskInProgress(constants.Deploy, "deploy")
	wait(t, func() bool { return handler.getState().DeployState.Status == InProgress })
	DeploySucceeded(0)
	wait(t, func() bool {
		handler.logLock.Lock()
		defer handler.logLock.Unlock()
		return len(handler.eventLog) == 2
	})
	if status := handler.getState().DeployState.Status; status != InProgress {
		t.Fatalf("deploy state = %q after subtask success, want %q", status, InProgress)
	}
	TaskFailed(constants.Deploy, errors.New("deploy failed"))
	wait(t, func() bool { return handler.getState().DeployState.Status == Failed })
}
