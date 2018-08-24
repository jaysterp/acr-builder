// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/Azure/acr-builder/builder"
	"github.com/Azure/acr-builder/graph"
	"github.com/Azure/acr-builder/pkg/procmanager"
	"github.com/Azure/acr-builder/pkg/volume"
	"github.com/Azure/acr-builder/templating"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const execLongDesc = `
This command can be used to execute a task.
`

type execCmd struct {
	out    io.Writer
	dryRun bool

	registryUser string
	registryPw   string

	opts *templating.BaseRenderOptions
}

func newExecCmd(out io.Writer) *cobra.Command {
	e := &execCmd{
		out:  out,
		opts: &templating.BaseRenderOptions{},
	}

	cmd := &cobra.Command{
		Use:   "exec",
		Short: "Execute a task",
		Long:  execLongDesc,
		RunE:  e.run,
	}

	f := cmd.Flags()

	f.StringVarP(&e.registryUser, "username", "u", "", "the username to use when logging into the registry")
	f.StringVarP(&e.registryPw, "password", "p", "", "the password to use when logging into the registry")
	f.BoolVar(&e.dryRun, "dry-run", false, "evaluates the task but doesn't execute it")

	AddBaseRenderingOptions(f, e.opts, cmd, true)
	return cmd
}

func (e *execCmd) run(cmd *cobra.Command, args []string) error {
	if e.opts.TaskFile == "" && e.opts.Base64EncodedTaskFile == "" {
		return errors.New("A task file or Base64 encoded task file is required")
	}

	ctx := context.Background()

	procManager := procmanager.NewProcManager(e.dryRun)
	if e.opts.SharedContextDirectory == "" {
		if !e.dryRun {
			homeVolName := fmt.Sprintf("%s%s", volume.VolumePrefix, uuid.New())
			e.opts.SharedContextDirectory = homeVolName
			v := volume.NewVolume(homeVolName, procManager)
			if msg, err := v.Create(ctx); err != nil {
				return fmt.Errorf("Err creating docker vol. Msg: %s, Err: %v", msg, err)
			}
			defer func() {
				_, _ = v.Delete(ctx)
			}()
		}
	}
	log.Printf("Using %s as the home volume\n", e.opts.SharedContextDirectory)

	var template *templating.Template
	var err error
	if e.opts.TaskFile == "" {
		if template, err = templating.DecodeTemplate(e.opts.Base64EncodedTaskFile); err != nil {
			return err
		}
	} else {
		if template, err = templating.LoadTemplate(e.opts.TaskFile); err != nil {
			return err
		}
	}

	rendered, err := templating.LoadAndRenderSteps(template, e.opts)
	if err != nil {
		return err
	}

	if debug {
		fmt.Println("Rendered template:")
		fmt.Println(rendered)
	}

	task, err := graph.UnmarshalTaskFromString(rendered, e.opts.Registry, e.registryUser, e.registryPw)
	if err != nil {
		return err
	}

	if err := e.validateCmdArgs(); err != nil {
		return err
	}

	timeout := time.Duration(task.TotalTimeout) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	builder := builder.NewBuilder(procManager, debug, e.opts.SharedContextDirectory)
	defer builder.CleanTask(context.Background(), task)
	return builder.RunTask(ctx, task)
}

func (e *execCmd) validateCmdArgs() error {
	return validateRegistryCreds(e.registryUser, e.registryPw)
}
