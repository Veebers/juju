// Copyright 2012-2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package jujuc

import (
	"fmt"
	"time"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/utils/keyvalues"
	"gopkg.in/juju/charm.v6-unstable"
)

// Metric represents a single metric set by the charm.
type Metric struct {
	Key   string
	Value string
	Time  time.Time
}

// AddMetricCommand implements the add-metric command.
type AddMetricCommand struct {
	cmd.CommandBase
	ctx     Context
	Metrics []Metric
}

// NewAddMetricCommand generates a new AddMetricCommand.
func NewAddMetricCommand(ctx Context) (cmd.Command, error) {
	return &AddMetricCommand{ctx: ctx}, nil
}

// Info returns the command infor structure for the add-metric command.
func (c *AddMetricCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "add-metric",
		Args:    "key1=value1 [key2=value2 ...]",
		Purpose: "send metrics",
	}
}

// Init parses the command's parameters.
func (c *AddMetricCommand) Init(args []string) error {
	// TODO(fwereade): 2016-03-17 lp:1558657
	now := time.Now()
	if len(args) == 0 {
		return fmt.Errorf("no metrics specified")
	}
	options, err := keyvalues.Parse(args, false)
	if err != nil {
		return err
	}
	for key, value := range options {
		c.Metrics = append(c.Metrics, Metric{key, value, now})
	}
	return nil
}

// Run adds metrics to the hook context.
func (c *AddMetricCommand) Run(ctx *cmd.Context) (err error) {
	for _, metric := range c.Metrics {
		if charm.IsBuiltinMetric(metric.Key) {
			return errors.Errorf("%v uses a reserved prefix", metric.Key)
		}
		err := c.ctx.AddMetric(metric.Key, metric.Value, metric.Time)
		if err != nil {
			return errors.Annotate(err, "cannot record metric")
		}
	}
	return nil
}
