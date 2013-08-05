// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"errors"

	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/juju"
	"launchpad.net/juju-core/state/api/params"
	"launchpad.net/juju-core/state/statecmd"
)

// UnexposeCommand is responsible exposing services.
type UnexposeCommand struct {
	EnvCommandBase
	ServiceName string
}

func (c *UnexposeCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "unexpose",
		Args:    "<service>",
		Purpose: "unexpose a service",
	}
}

func (c *UnexposeCommand) Init(args []string) error {
	if len(args) == 0 {
		return errors.New("no service name specified")
	}
	c.ServiceName = args[0]
	return cmd.CheckEmpty(args[1:])
}

// Run changes the juju-managed firewall to hide any
// ports that were also explicitly marked by units as closed.
func (c *UnexposeCommand) Run(ctx *cmd.Context) error {
	conn, err := juju.NewConnFromName(c.EnvName)
	if err != nil {
		return c.envOpenFailure(err, ctx.Stderr)
	}
	defer conn.Close()
	params := params.ServiceUnexpose{ServiceName: c.ServiceName}
	return statecmd.ServiceUnexpose(conn.State, params)
}
