// Copyright 2016 Palantir Technologies, Inc.
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

package pluginapi

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"unicode"

	"github.com/pkg/errors"

	"github.com/palantir/godel/framework/godellauncher"
)

// TaskInfo is a JSON-serializable interface that can be translated into a godellauncher.Task. Refer to that struct for
// field documentation.
type TaskInfo interface {
	Name() string
	Description() string
	Command() []string
	GlobalFlagOptions() GlobalFlagOptions
	VerifyOptions() VerifyOptions

	toTask(pluginExecPath, cfgFileName string) godellauncher.Task
}

type taskInfoImpl struct {
	NameVar              string                 `json:"name"`
	DescriptionVar       string                 `json:"description"`
	CommandVar           []string               `json:"command"`
	GlobalFlagOptionsVar *globalFlagOptionsImpl `json:"globalFlagOptions"`
	VerifyOptionsVar     *verifyOptionsImpl     `json:"verifyOptions"`
}

type TaskInfoParam interface {
	apply(*taskInfoImpl)
}

type taskInfoParamFunc func(*taskInfoImpl)

func (f taskInfoParamFunc) apply(impl *taskInfoImpl) {
	f(impl)
}

func TaskInfoCommand(command ...string) TaskInfoParam {
	return taskInfoParamFunc(func(impl *taskInfoImpl) {
		impl.CommandVar = command
	})
}

func TaskInfoGlobalFlagOptions(globalFlagOpts GlobalFlagOptions) TaskInfoParam {
	return taskInfoParamFunc(func(impl *taskInfoImpl) {
		impl.GlobalFlagOptionsVar = &globalFlagOptionsImpl{
			DebugFlagVar:       globalFlagOpts.DebugFlag(),
			ProjectDirFlagVar:  globalFlagOpts.ProjectDirFlag(),
			GodelConfigFlagVar: globalFlagOpts.GodelConfigFlag(),
			ConfigFlagVar:      globalFlagOpts.ConfigFlag(),
		}
	})
}

func TaskInfoVerifyOptions(verifyOpts VerifyOptions) TaskInfoParam {
	return taskInfoParamFunc(func(impl *taskInfoImpl) {
		var verifyImpls []verifyFlagImpl
		for _, v := range verifyOpts.VerifyTaskFlags() {
			verifyImpls = append(verifyImpls, verifyFlagImpl{
				NameVar:        v.Name(),
				DescriptionVar: v.Description(),
				TypeVar:        v.Type(),
			})
		}
		impl.VerifyOptionsVar = &verifyOptionsImpl{
			VerifyTaskFlagsVar: verifyImpls,
			OrderingVar:        verifyOpts.Ordering(),
			ApplyTrueArgsVar:   verifyOpts.ApplyTrueArgs(),
			ApplyFalseArgsVar:  verifyOpts.ApplyFalseArgs(),
		}
	})
}

func MustNewTaskInfo(name, description string, params ...TaskInfoParam) TaskInfo {
	ti, err := NewTaskInfo(name, description, params...)
	if err != nil {
		panic(err)
	}
	return ti
}

func NewTaskInfo(name, description string, params ...TaskInfoParam) (TaskInfo, error) {
	for _, r := range name {
		if unicode.IsSpace(r) {
			return nil, errors.Errorf("task name cannot contain whitespace: %q", name)
		}
	}
	impl := &taskInfoImpl{
		NameVar:        name,
		DescriptionVar: description,
	}
	for _, p := range params {
		if p == nil {
			continue
		}
		p.apply(impl)
	}
	return impl, nil
}

func (ti *taskInfoImpl) Name() string {
	return ti.NameVar
}

func (ti *taskInfoImpl) Description() string {
	return ti.DescriptionVar
}

func (ti *taskInfoImpl) Command() []string {
	return ti.CommandVar
}

func (ti *taskInfoImpl) VerifyOptions() VerifyOptions {
	if ti.VerifyOptionsVar == nil {
		return nil
	}
	return ti.VerifyOptionsVar
}

func (ti *taskInfoImpl) GlobalFlagOptions() GlobalFlagOptions {
	if ti.GlobalFlagOptionsVar == nil {
		return nil
	}
	return ti.GlobalFlagOptionsVar
}

func (ti *taskInfoImpl) toTask(pluginExecPath, cfgFileName string) godellauncher.Task {
	var verifyOpts *godellauncher.VerifyOptions
	if ti.VerifyOptions() != nil {
		opts := ti.VerifyOptionsVar.toGodelVerifyOptions()
		verifyOpts = &opts
	}
	var globalFlagOpts godellauncher.GlobalFlagOptions
	if ti.GlobalFlagOptionsVar != nil {
		globalFlagOpts = ti.GlobalFlagOptionsVar.toGodelGlobalFlagOptions()
	}
	return godellauncher.Task{
		Name:           ti.NameVar,
		Description:    ti.DescriptionVar,
		ConfigFile:     cfgFileName,
		Verify:         verifyOpts,
		GlobalFlagOpts: globalFlagOpts,
		RunImpl: func(t *godellauncher.Task, global godellauncher.GlobalConfig, stdout io.Writer) error {
			cmdArgs, err := ti.globalFlagArgs(t, global)
			if err != nil {
				return err
			}
			cmdArgs = append(cmdArgs, ti.CommandVar...)
			cmdArgs = append(cmdArgs, global.TaskArgs...)
			cmd := exec.Command(pluginExecPath, cmdArgs...)
			cmd.Stdout = stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			if err := cmd.Run(); err != nil {
				if _, ok := err.(*exec.ExitError); ok {
					// create empty error because command will likely print its own error
					return fmt.Errorf("")
				}
				return errors.Wrapf(err, "plugin execution failed")
			}
			return nil
		},
	}
}

func (ti *taskInfoImpl) globalFlagArgs(t *godellauncher.Task, global godellauncher.GlobalConfig) ([]string, error) {
	var args []string
	if global.Debug && t.GlobalFlagOpts.DebugFlag != "" {
		args = append(args, t.GlobalFlagOpts.DebugFlag)
	}

	// the rest of the arguments depend on "--wrapper" being specified in the global configuration
	if global.Wrapper == "" {
		return args, nil
	}

	projectDir := path.Dir(global.Wrapper)
	if t.GlobalFlagOpts.ProjectDirFlag != "" {
		args = append(args, t.GlobalFlagOpts.ProjectDirFlag, projectDir)
	}

	// if config dir flags were not specified, nothing more to do
	if t.GlobalFlagOpts.GodelConfigFlag == "" && (t.GlobalFlagOpts.ConfigFlag == "" || t.ConfigFile == "") {
		return args, nil
	}

	cfgDir, err := godellauncher.ConfigDirPath(projectDir)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to determine config directory path")
	}

	if t.GlobalFlagOpts.GodelConfigFlag != "" {
		args = append(args, t.GlobalFlagOpts.GodelConfigFlag, path.Join(cfgDir, godellauncher.GödelConfigYML))
	}

	if t.GlobalFlagOpts.ConfigFlag != "" && t.ConfigFile != "" {
		args = append(args, t.GlobalFlagOpts.ConfigFlag, path.Join(cfgDir, t.ConfigFile))
	}

	return args, nil
}
